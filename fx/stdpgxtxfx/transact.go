// Package stdpgxtxfx provides database transactors.
package stdpgxtxfx

import (
	"context"
	"strings"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/stdfx"
	"github.com/advdv/stdgo/stdtx"
	"github.com/advdv/stdgo/stdtx/stdtxpgxv5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config configures the transact components.
type Config struct {
	TestMaxQueryCosts float64 `env:"TEST_MAX_QUERY_COSTS"`
}

// Params describe fx params for creating the transactors.
type Params struct {
	fx.In
	Config
	RW *pgxpool.Pool `name:"rw"`
	RO *pgxpool.Pool `name:"ro"`
}

// Result describes the fx components this package produces.
type Result struct {
	fx.Out
	ReadWrite *stdtx.Transactor[pgx.Tx] `name:"rw"`
	ReadOnly  *stdtx.Transactor[pgx.Tx] `name:"ro"`
}

// New provides the transactors.
func New(params Params) (Result, error) {
	opts := []stdtxpgxv5.Option{
		stdtxpgxv5.BeginWithSQL(beginTxSQLHook(params.Config)),
	}

	if params.Config.TestMaxQueryCosts > 0 {
		opts = append(opts,
			stdtxpgxv5.TestForMaxQueryPlanCosts(params.TestMaxQueryCosts),
			stdtxpgxv5.DiscourageSeqScan(true),
		)
	}

	return Result{
		ReadWrite: stdtx.NewTransactor(stdtxpgxv5.New(params.RW, opts...)),
		ReadOnly: stdtx.NewTransactor(stdtxpgxv5.New(params.RO, append(opts, []stdtxpgxv5.Option{
			stdtxpgxv5.AccessMode(pgx.ReadOnly),
		}...)...)),
	}, nil
}

// beginTxSQLHook returns a function that is run at the start of every transaction. It allows for
// setting up cross-cutting concerns for all sql transactions. Such as parameters required for
// row-level security policies.
func beginTxSQLHook(Config) stdtxpgxv5.TxBeginSQLFunc {
	return func(_ context.Context, sql *strings.Builder, _ pgx.Tx) (*strings.Builder, error) {
		return sql, nil
	}
}

// Provide provides the standard read-write/read-only separation.
func Provide(applicationName string) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("transact", New,

		// configure an application name for the connection.
		stdpgxfx.ProvideDeriver("rw", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			base.ConnConfig.RuntimeParams["application_name"] = applicationName + "-rw"
			return base
		}),

		// on Aurora we split the read and write side to different endpoints.
		stdpgxfx.ProvideDeriver("ro", func(logs *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			baseHost := base.ConnConfig.Host
			base.ConnConfig.RuntimeParams["application_name"] = applicationName + "-ro"

			// special clause for RDS aurora cluster.
			if strings.HasSuffix(baseHost, ".rds.amazonaws.com") && strings.Contains(baseHost, ".cluster-") {
				base.ConnConfig.Host = strings.Replace(baseHost, ".cluster-", ".cluster-ro-", 1)
				logs.Info("derived read-only RDS cluster host",
					zap.String("new_host", base.ConnConfig.Host),
					zap.String("base_host", baseHost))
			}

			return base
		}),
	)
}

// TestProvide provides project-specific transactor config to make it easy for any test package
// to interact with the database.
func TestProvide(applicationName, endRoleUsername, endRolePassword string) fx.Option {
	return fx.Options(
		Provide(applicationName),
		fx.Decorate(func(c Config) Config {
			c.TestMaxQueryCosts = 100 // number obtained heuristically
			return c
		}),
		fx.Supply(
			&pgtestdb.Role{
				// role for migrations
				Username: "postgres", Password: "postgres",
			}, &stdpgxfx.EndRole{
				// role for actual testing code
				Username: endRoleUsername,
				Password: endRolePassword,
			}),
	)
}
