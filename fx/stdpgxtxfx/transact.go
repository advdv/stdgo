// Package stdpgxtxfx provides database transactors.
package stdpgxtxfx

import (
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
	RW             *pgxpool.Pool             `name:"rw"`
	RO             *pgxpool.Pool             `name:"ro"`
	TxBeginSQL     stdtxpgxv5.TxBeginSQLFunc `optional:"true"`
	OnCommitTxHook stdtxpgxv5.TxOnCommitFunc `optional:"true"`
}

// Result describes the fx components this package produces.
type Result struct {
	fx.Out
	ReadWrite *stdtx.Transactor[pgx.Tx] `name:"rw"`
	ReadOnly  *stdtx.Transactor[pgx.Tx] `name:"ro"`
}

// New provides the transactors.
func New(params Params) (Result, error) {
	// We always run in serializable mode, any other mode can cause write-skew. Which makes it hard to guarantee
	// min/max counts and check across rows.
	opts := []stdtxpgxv5.Option{stdtxpgxv5.IsolationMode(pgx.Serializable)}
	if params.TxBeginSQL != nil {
		opts = append(opts, stdtxpgxv5.BeginWithSQL(params.TxBeginSQL))
	}

	if params.OnCommitTxHook != nil {
		opts = append(opts, stdtxpgxv5.OnTxCommit(params.OnCommitTxHook))
	}

	if params.TestMaxQueryCosts > 0 {
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

// Provide provides the standard read-write/read-only separation.
func Provide(applicationName string) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdpgxtxfx", New,

		// configure an application name for the connection. The "ro" pool's
		// host rewriting (for Aurora cluster endpoints) is handled by stdpgxfx.
		stdpgxfx.ProvideDeriver("rw", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			base.ConnConfig.RuntimeParams["application_name"] = applicationName + "-rw"
			return base
		}),
		stdpgxfx.ProvideDeriver("ro", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			base.ConnConfig.RuntimeParams["application_name"] = applicationName + "-ro"
			return base
		}),
	)
}

// TestProvide provides project-specific transactor config to make it easy for any test package
// to interact with the database.
func TestProvide(applicationName, endRoleUsername, endRolePassword string) fx.Option {
	return fx.Options(
		Provide(applicationName),
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
