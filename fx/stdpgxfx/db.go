// Package stdpgxfx provides sql.DB connection pools usng pgx/v5.
package stdpgxfx

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/advdv/stdgo/stdfx"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config configures the module.
type Config struct {
	// MainDatabaseURL configures the database connection string for the main connection.
	MainDatabaseURL string `env:"MAIN_DATABASE_URL,required"`
	// IamAuth enables RDS IAM authentication. When enabled, the password on every
	// connection attempt is replaced by a freshly built IAM auth token. The signing
	// region is derived per-connection from the RDS hostname (e.g.,
	// "mycluster.cluster-xxx.eu-central-1.rds.amazonaws.com" → "eu-central-1"),
	// which means a derived "ro" pool that points at a different regional endpoint
	// (e.g., a reader endpoint in another region) signs tokens against that region
	// automatically. The hostname must match the standard regional RDS pattern;
	// non-RDS hostnames (e.g., custom domains, RDS Proxy aliases, the global
	// writer endpoint) are not supported.
	IamAuth bool `env:"IAM_AUTH"`
	// Hosts overrides the host for named pools. Useful for Aurora Global Database
	// deployments where the secondary region's reader pool should connect to a
	// local regional reader endpoint while the writer pool stays on the primary
	// region's cluster endpoint.
	//
	// Format: comma-separated <pool>:<host> pairs.
	//
	//	STDPGX_HOSTS=ro:mycluster.cluster-ro-def.ap-southeast-1.rds.amazonaws.com
	//	STDPGX_HOSTS=ro:ro-host.example.com,rw:rw-host.example.com
	//
	// An override wins over name-based conventions (i.e., when a pool's host is
	// overridden, the cluster-ro auto-rewrite is skipped). The IAM signing region
	// is then derived from the overridden host, so cross-region setups Just Work.
	Hosts map[string]string `env:"HOSTS"`
}

type (
	// Params describe the main parameters for providing components.
	Params struct {
		fx.In

		Config    Config
		AwsConfig aws.Config `optional:"true"`
		Logs      *zap.Logger
	}

	// Result describe the main components provided for this module.
	Result struct {
		fx.Out

		PoolConfig *pgxpool.Config
	}
)

// rdsRegionalHostRE matches regional Aurora/RDS endpoints and captures the region.
// Examples that match:
//
//	mycluster.cluster-abc123.eu-central-1.rds.amazonaws.com         → eu-central-1
//	mycluster.cluster-ro-abc123.eu-central-1.rds.amazonaws.com      → eu-central-1
//	instance-1.abc123.ap-southeast-1.rds.amazonaws.com              → ap-southeast-1
//
// Examples that DO NOT match — IAM auth is unsupported for these:
//
//	mycluster.global-abc123.global.rds.amazonaws.com                (global writer endpoint, no region)
//	localhost                                                       (local dev)
//	myproxy.example.com                                             (custom domain / RDS Proxy alias)
var rdsRegionalHostRE = regexp.MustCompile(
	`^[^.]+\.[^.]+\.(?P<region>[a-z]{2}-[a-z]+-\d+)\.rds\.amazonaws\.com\.?$`)

// DeriveSigningRegion attempts to extract the AWS region from the given host
// using the standard RDS regional hostname format. Returns an empty string if
// the host doesn't match the regional pattern.
func DeriveSigningRegion(host string) string {
	m := rdsRegionalHostRE.FindStringSubmatch(host)
	if m == nil {
		return ""
	}
	return m[rdsRegionalHostRE.SubexpIndex("region")]
}

// New is the main constructor. In this package it only provides the pool configuration
// used through out the package.
func New(params Params) (r Result, err error) {
	pcfg, err := pgxpool.ParseConfig(params.Config.MainDatabaseURL)
	if err != nil {
		return r, fmt.Errorf("failed to parse connecting string: %w", err)
	}

	// we log notices from the database so debugging on the Go side is easier
	pcfg.ConnConfig.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) { //nolint:varnamelen // n is clear in context
		level := zapcore.DebugLevel

		switch strings.ToLower(n.SeverityUnlocalized) {
		case "info":
			level = zapcore.InfoLevel
		case "notice":
			level = zapcore.InfoLevel
		case "warning":
			level = zapcore.WarnLevel
		case "exception":
			level = zap.ErrorLevel
		}

		params.Logs.Log(level, "notice: "+n.Message,
			zap.String("code", n.Code),
			zap.String("hint", n.Hint), zap.String("detail", n.Detail))
	}

	if params.Config.IamAuth {
		if err := installIamAuthBeforeConnect(pcfg, params); err != nil {
			return r, err
		}
	}

	return Result{PoolConfig: pcfg}, nil
}

// installIamAuthBeforeConnect wires up a pgxpool.Config.BeforeConnect hook that
// replaces the password with a freshly built RDS IAM auth token on every new
// connection. The signing region is derived from the live *pgx.ConnConfig host
// so that derived pools (re-pointed by a Deriver to a different regional
// endpoint) sign tokens for the right region automatically.
//
// Any pre-existing BeforeConnect hook (e.g., installed by a Deriver or the
// caller of pgxpool.ParseConfig) is preserved and invoked first; the IAM token
// is only assigned to pgc.Password if the prior hook returns no error. This
// matches the chaining pattern used by aurora-dsql-connectors and avoids
// silently clobbering a user-supplied hook.
func installIamAuthBeforeConnect(pcfg *pgxpool.Config, params Params) error {
	if params.AwsConfig.Credentials == nil {
		return errors.New("IAM authentication is enabled but no AWS configuration provided")
	}

	awsCfg := params.AwsConfig
	logs := params.Logs
	prev := pcfg.BeforeConnect

	pcfg.BeforeConnect = func(ctx context.Context, pgc *pgx.ConnConfig) error {
		if prev != nil {
			if err := prev(ctx, pgc); err != nil {
				return err
			}
		}

		region := DeriveSigningRegion(pgc.Host)
		if region == "" {
			return fmt.Errorf("could not derive IAM signing region from host %q: "+
				"hostname must match a regional RDS pattern (*.<region>.rds.amazonaws.com)", pgc.Host)
		}

		tok, err := buildIamAuthToken(ctx, logs, pgc.Port, pgc.User, region, awsCfg, pgc.Host)
		if err != nil {
			return fmt.Errorf("failed to build iam token: %w", err)
		}

		pgc.Password = tok

		return nil
	}

	return nil
}

// buildIamAuthToken will construct a RDS proxy authentication token. We don't run this during the
// lifecycle phase so we timeout manually with our own context.
func buildIamAuthToken(
	ctx context.Context,
	logs *zap.Logger,
	port uint16,
	username, region string,
	awsc aws.Config,
	host string,
) (string, error) {
	ep := host + ":" + fmt.Sprintf("%d", port)

	logs.Debug("building IAM auth token",
		zap.String("username", username),
		zap.String("region", region),
		zap.String("ep", ep))

	tok, err := auth.BuildAuthToken(ctx, ep, region, username, awsc.Credentials)
	if err != nil {
		return "", fmt.Errorf("underlying: %w", err)
	}

	return tok, nil
}

// newDB is the low-level constructor for turning our config into sql databases. It is
// called for both the main pool and the derived pools. The pool name is used to
// apply name-based conventions (e.g., the "ro" pool auto-rewrites Aurora cluster
// writer endpoints to the corresponding reader endpoint) and to look up explicit
// per-pool host overrides from cfg.Hosts.
func newDB[DBT any](
	name string,
	deriver Deriver, // optional
	lcl fx.Lifecycle,
	cfg Config,
	pcfg *pgxpool.Config,
	logs *zap.Logger,
	drv Driver[DBT],
) (DBT, error) {
	// Always work on a copy of the base config — multiple pools share the same
	// *pgxpool.Config pointer via fx, and we mutate Host below for conventions
	// and overrides.
	pcfg = pcfg.Copy()
	if deriver != nil {
		pcfg = deriver(logs, pcfg)
	}

	// Explicit per-pool host overrides win over name-based conventions.
	if !ApplyPoolHostOverride(name, pcfg, cfg.Hosts, logs) {
		ApplyPoolHostConventions(name, pcfg, logs)
	}

	db, err := drv.NewPool(pcfg)
	if err != nil {
		return db, err
	}

	lcl.Append(fx.Hook{
		OnStop: func(context.Context) error {
			return drv.Close(db)
		},
	})

	return db, nil
}

// ApplyPoolHostOverride applies an explicit per-pool host override from the
// given map (typically Config.Hosts) to the given *pgxpool.Config. It returns
// true if an override was applied (so callers can skip name-based conventions).
//
// It is exported so that tests (and other layers that build *pgxpool.Config
// outside of newDB) can apply the same override logic explicitly.
func ApplyPoolHostOverride(name string, pcfg *pgxpool.Config, hosts map[string]string, logs *zap.Logger) bool {
	override, ok := hosts[name]
	if !ok || override == "" {
		return false
	}
	logs.Info("overriding host for pool",
		zap.String("name", name),
		zap.String("base_host", pcfg.ConnConfig.Host),
		zap.String("new_host", override))
	pcfg.ConnConfig.Host = override
	return true
}

// ApplyPoolHostConventions applies name-based host conventions to a pool's
// *pgxpool.Config. Currently, the "ro" pool auto-rewrites an Aurora cluster
// writer endpoint to the corresponding reader endpoint
// (e.g., "*.cluster-xxx.<region>.rds.amazonaws.com" → "*.cluster-ro-xxx.<region>.rds.amazonaws.com").
// This runs AFTER any user/framework-supplied Deriver so that the pool's host
// reflects the routing intent encoded by the pool name.
//
// It is exported so that tests (and other layers that build *pgxpool.Config
// outside of newDB) can apply the same conventions explicitly.
func ApplyPoolHostConventions(name string, pcfg *pgxpool.Config, logs *zap.Logger) {
	if name != "ro" {
		return
	}
	host := pcfg.ConnConfig.Host
	if !strings.HasSuffix(host, ".rds.amazonaws.com") || !strings.Contains(host, ".cluster-") {
		return
	}
	pcfg.ConnConfig.Host = strings.Replace(host, ".cluster-", ".cluster-ro-", 1)
	logs.Info("derived read-only RDS cluster host",
		zap.String("new_host", pcfg.ConnConfig.Host),
		zap.String("base_host", host))
}

// Provide components as fx dependencies.
func Provide[DBT any](drv Driver[DBT], mainPoolName string, derivedPoolNames ...string) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdpgx", New,
		// for some dynamically created provides are dependant on this.
		fx.Provide(func() Driver[DBT] { return drv }),
		// provide the "main" pool
		fx.Provide(fx.Annotate(
			func(
				deriver Deriver, lc fx.Lifecycle, cfg Config,
				pcfg *pgxpool.Config, logs *zap.Logger, drv Driver[DBT],
			) (DBT, error) {
				return newDB(mainPoolName, deriver, lc, cfg, pcfg, logs, drv)
			},
			fx.ParamTags(`name:"`+mainPoolName+`" optional:"true"`),
			fx.ResultTags(`name:"`+mainPoolName+`"`))),
		// provide the "derived" pools (if any)
		withDerivedPools(drv, mainPoolName, derivedPoolNames...),
	)
}

// Deriver needs to be provided by the user of this module if derived pools are created.
type Deriver func(logs *zap.Logger, base *pgxpool.Config) *pgxpool.Config

// ProvideDeriver is a short-hande function for providing a named deriver function that.
func ProvideDeriver(name string, deriver Deriver) fx.Option {
	return fx.Provide(
		fx.Annotate(func() Deriver {
			return deriver
		}, fx.ResultTags(`name:"`+name+`"`)))
}

// withDerivedPools dynamically adds fx provides for creating connection pools that
// take the base pool as input.
func withDerivedPools[DBT any](drv Driver[DBT], mainName string, names ...string) fx.Option {
	options := make([]fx.Option, 0, len(names))
	for _, name := range names {
		options = append(options, fx.Provide(
			fx.Annotate(func(
				_ DBT, deriver Deriver, pcfg *pgxpool.Config, lc fx.Lifecycle, cfg Config, logs *zap.Logger,
			) (derived DBT, err error) {
				// create the pool for each named derived.
				derived, err = newDB(name, deriver, lc, cfg, pcfg, logs, drv)
				if err != nil {
					return derived, fmt.Errorf("failed to created derived pool: %w", err)
				}

				logs.Info("initialized derived pool", zap.String("name", name))

				return derived, nil
			},
				fx.ParamTags(`name:"`+mainName+`"`, `name:"`+name+`" optional:"true"`),
				fx.ResultTags(`name:"`+name+`"`))))
	}

	return fx.Options(options...)
}
