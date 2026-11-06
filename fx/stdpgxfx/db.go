// Package stdpgxfx provides sql.DB connection pools usng pgx/v5.
package stdpgxfx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/advdv/stdgo/stdfx"
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
	// IamAuth enables IAM authentication. When enabled, the password of every new
	// connection is replaced by the token returned by the provided IamTokenFunc.
	// The stdpgxiamfx sub-package provides an RDS implementation; see its docs for
	// how the signing region is derived from the host.
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
	// overridden, the cluster-ro auto-rewrite is skipped).
	Hosts map[string]string `env:"HOSTS"`
}

// IamTokenFunc returns the password to use for a new connection when IamAuth is enabled.
// It receives the live connection config so the token can be built for the
// connection's actual host, which may differ per (derived) pool.
type IamTokenFunc func(ctx context.Context, cc *pgx.ConnConfig) (string, error)

type (
	// Params describe the main parameters for providing components.
	Params struct {
		fx.In

		Config   Config
		Logs     *zap.Logger
		IamToken IamTokenFunc `optional:"true"`
	}

	// Result describe the main components provided for this module.
	Result struct {
		fx.Out

		PoolConfig *pgxpool.Config
	}
)

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
// replaces the password with a token from the provided IamTokenFunc on every new
// connection. The token func receives the live *pgx.ConnConfig so that derived
// pools (re-pointed by a Deriver to a different host) get a token for that host.
//
// Any pre-existing BeforeConnect hook (e.g., installed by a Deriver or the
// caller of pgxpool.ParseConfig) is preserved and invoked first; the token is
// only assigned to pgc.Password if the prior hook returns no error.
func installIamAuthBeforeConnect(pcfg *pgxpool.Config, params Params) error {
	if params.IamToken == nil {
		return errors.New("STDPGX_IAM_AUTH is set but no IamTokenFunc is provided (add stdpgxiamfx.Provide())")
	}

	iamToken := params.IamToken
	prev := pcfg.BeforeConnect

	pcfg.BeforeConnect = func(ctx context.Context, pgc *pgx.ConnConfig) error {
		if prev != nil {
			if err := prev(ctx, pgc); err != nil {
				return err
			}
		}

		tok, err := iamToken(ctx, pgc)
		if err != nil {
			return fmt.Errorf("failed to get iam token: %w", err)
		}

		pgc.Password = tok

		return nil
	}

	return nil
}

// newDB constructs a database from its resolved configuration and owns its lifecycle.
func newDB[DBT any](
	lcl fx.Lifecycle,
	pcfg FinalPgxPoolConfig,
	drv Driver[DBT],
) (DBT, error) {
	db, err := drv.NewPool(pcfg.Copy())
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
		provideFinalPoolConfig(mainPoolName),
		// provide the "main" pool
		fx.Provide(fx.Annotate(
			newDB[DBT],
			fx.ParamTags("", `name:"`+mainPoolName+`"`),
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
		options = append(options, provideFinalPoolConfig(name), fx.Provide(
			fx.Annotate(func(
				_ DBT, pcfg FinalPgxPoolConfig, lc fx.Lifecycle, logs *zap.Logger,
			) (derived DBT, err error) {
				// create the pool for each named derived.
				derived, err = newDB(lc, pcfg, drv)
				if err != nil {
					return derived, fmt.Errorf("failed to created derived pool: %w", err)
				}

				logs.Info("initialized derived pool", zap.String("name", name))

				return derived, nil
			},
				fx.ParamTags(`name:"`+mainName+`"`, `name:"`+name+`"`),
				fx.ResultTags(`name:"`+name+`"`))))
	}

	return fx.Options(options...)
}
