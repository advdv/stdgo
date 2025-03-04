// Package stdpgxfx provides sql.DB connection pools usng pgx/v5.
package stdpgxfx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/advdv/stdgo/stdfx"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/rds/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Config configures the module.
type Config struct {
	// MainDatabaseURL configures the database connection string for the main connection.
	MainDatabaseURL string `env:"MAIN_DATABASE_URL,required"`

	// IamAuthRegion when set cause the password to be replaced by an IAM token for authentication.
	IamAuthRegion string `env:"IAM_AUTH_REGION"`
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

// New is the main constructor. In this package it only provides the pool configuration
// used through out the package.
func New(params Params) (r Result, err error) {
	pcfg, err := pgxpool.ParseConfig(params.Config.MainDatabaseURL)
	if err != nil {
		return r, fmt.Errorf("failed to parse connecting string: %w", err)
	}

	// we log notices from the database so debugging on the Go side is easier
	pcfg.ConnConfig.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) {
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

	if params.Config.IamAuthRegion != "" {
		if params.AwsConfig.Credentials == nil {
			return r, errors.New("IAM authentication is enabled but no AWS configuration provided")
		}

		// For IAM Auth we need to build a token as a password on every connection attempt
		pcfg.BeforeConnect = func(ctx context.Context, pgc *pgx.ConnConfig) error {
			tok, err := buildIamAuthToken(
				ctx, params.Logs,
				pcfg.ConnConfig.Port,
				pcfg.ConnConfig.User,
				params.Config.IamAuthRegion,
				params.AwsConfig,
				pcfg.ConnConfig.Host)
			if err != nil {
				return fmt.Errorf("failed to build iam token: %w", err)
			}

			pgc.Password = tok

			return nil
		}
	}

	return Result{PoolConfig: pcfg}, nil
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
// called for both the main pool and the derived pools.
func newDB(
	deriver Deriver, // optional
	lc fx.Lifecycle,
	_ Config,
	pcfg *pgxpool.Config,
	_ *zap.Logger,
) (*sql.DB, error) {
	if deriver != nil {
		pcfg = deriver(pcfg.Copy())
	}

	opts := []stdlib.OptionOpenDB{}
	if pcfg.BeforeConnect != nil {
		opts = append(opts, stdlib.OptionBeforeConnect(pcfg.BeforeConnect))
	}

	db := stdlib.OpenDB(*pcfg.ConnConfig, opts...)
	lc.Append(fx.Hook{
		OnStop: func(context.Context) error {
			return db.Close()
		},
	})

	return db, nil
}

// Provide components as fx dependencies.
func Provide(mainPoolName string, derivedPoolNames ...string) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdpgx", New,
		// provide the "main" pool
		fx.Provide(fx.Annotate(newDB,
			fx.ParamTags(`name:"`+mainPoolName+`" optional:"true"`),
			fx.ResultTags(`name:"`+mainPoolName+`"`))),
		// provide the "derived" pools (if any)
		withDerivedPools(mainPoolName, derivedPoolNames...),
	)
}

// Deriver needs to be provided by the user of this module if derived pools are created.
type Deriver func(base *pgxpool.Config) *pgxpool.Config

// ProvideDeriver is a short-hande function for providing a named deriver function that.
func ProvideDeriver(name string, deriver Deriver) fx.Option {
	return fx.Provide(
		fx.Annotate(func() Deriver {
			return deriver
		}, fx.ResultTags(`name:"`+name+`"`)))
}

// withDerivedPools dynamically adds fx provides for creating connection pools that
// take the base pool as input.
func withDerivedPools(mainName string, names ...string) fx.Option {
	options := make([]fx.Option, 0, len(names))
	for _, name := range names {
		options = append(options, fx.Provide(
			fx.Annotate(func(
				_ *sql.DB, deriver Deriver, pcfg *pgxpool.Config, lc fx.Lifecycle, cfg Config, logs *zap.Logger,
			) (derived *sql.DB, err error) {
				// create the pool for each named derived.
				derived, err = newDB(deriver, lc, cfg, pcfg, logs)
				if err != nil {
					return nil, fmt.Errorf("failed to created derived pool: %w", err)
				}

				logs.Info("initialized derived pool", zap.String("name", name))
				return derived, nil
			},
				fx.ParamTags(`name:"`+mainName+`"`, `name:"`+name+`" optional:"true"`),
				fx.ResultTags(`name:"`+name+`"`))))
	}

	return fx.Options(options...)
}
