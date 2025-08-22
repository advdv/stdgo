// Package stdpgxstdtxfx provides database transactors.
//
//go:generate go tool entgo.io/ent/cmd/ent generate ./testdata/schema --target testdata/model
package stdpgxstdtxfx

import (
	"context"
	"database/sql"
	"strings"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/stdent"
	"github.com/advdv/stdgo/stdfx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"entgo.io/ent/dialect"
	entdialect "entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// Config configures the transact components.
type Config struct {
	TestMaxQueryCosts float64 `env:"TEST_MAX_QUERY_COSTS"`
}

type ClientFactoryFunc[T stdent.Tx, C stdent.Client[T]] = func(driver dialect.Driver) C

// Params describe fx params for creating the transactors.
type Params[T stdent.Tx, C stdent.Client[T]] struct {
	fx.In
	fx.Lifecycle
	Config
	RW            *sql.DB `name:"rw"`
	RO            *sql.DB `name:"ro"`
	ClientFactory ClientFactoryFunc[T, C]
	TxBeginSQL    stdent.BeginHookFunc `optional:"true"`
}

// Result describes the fx components this package produces.
type Result[T stdent.Tx] struct {
	fx.Out
	ReadWrite *stdent.Transactor[T] `name:"rw"`
	ReadOnly  *stdent.Transactor[T] `name:"ro"`
}

// New provides the transactors.
func New[T stdent.Tx, C stdent.Client[T]](params Params[T, C]) (Result[T], error) {
	opts := []stdent.DriverOption{}

	// when enabled we can check on every query if indexes are used correctly.
	if params.TestMaxQueryCosts > 0 {
		opts = append(opts,
			stdent.TxExecQueryLoggingLevel(zapcore.InfoLevel),
			stdent.DiscourageSequentialScans(),
			stdent.TestForMaxQueryPlanCosts(params.TestMaxQueryCosts),
		)
	}

	// allow some logic to be run at the beginning of every transaction. Primarily to setu
	// for Row-level security.
	if params.TxBeginSQL != nil {
		stdent.BeginHook(params.TxBeginSQL)
	}

	// read-write side
	rwBaseDrv := entsql.NewDriver(entdialect.Postgres, entsql.Conn{ExecQuerier: params.RW})
	rwDrv := stdent.NewDriver(rwBaseDrv, opts...)
	rwClient := params.ClientFactory(rwDrv)
	params.Lifecycle.Append(fx.Hook{OnStop: func(context.Context) error { return params.RW.Close() }})

	// read-only side
	roBaseDrv := entsql.NewDriver(entdialect.Postgres, entsql.Conn{ExecQuerier: params.RO})
	roDrv := stdent.NewDriver(roBaseDrv, opts...)
	roClient := params.ClientFactory(roDrv)
	params.Lifecycle.Append(fx.Hook{OnStop: func(context.Context) error { return params.RO.Close() }})

	return Result[T]{
		ReadWrite: stdent.New(rwClient),
		ReadOnly:  stdent.New(roClient),
	}, nil
}

// Provide provides the standard read-write/read-only separation.
func Provide[T stdent.Tx, C stdent.Client[T]](applicationName string) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdpgxstdtxfx", New[T, C],

		// configure an application name for the connection.
		stdpgxfx.ProvideDeriver("rw", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			base.ConnConfig.RuntimeParams["application_name"] = applicationName + "-rw"
			return base
		}),

		// on Aurora we split the read and write side to different endpoints.
		stdpgxfx.ProvideDeriver("ro", func(logs *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			baseHost := base.ConnConfig.Host
			base.ConnConfig.RuntimeParams["application_name"] = applicationName + "-ro"

			// special clause for RDS aurora cluster so we can route to the read-only endpoint.
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
func TestProvide[T stdent.Tx, C stdent.Client[T]](
	applicationName,
	endRoleUsername,
	endRolePassword string,
	clientFactory ClientFactoryFunc[T, C],
) fx.Option {
	return fx.Options(
		Provide[T, C](applicationName),
		fx.Supply(clientFactory),
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
