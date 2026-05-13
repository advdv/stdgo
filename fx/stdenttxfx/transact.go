// Package stdenttxfx provides database transactors.
//
//go:generate go tool entgo.io/ent/cmd/ent generate ./testdata/schema --target testdata/model
package stdenttxfx

import (
	"context"
	"database/sql"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/stdent"
	"github.com/advdv/stdgo/stdfx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// Config configures the transact components.
type Config struct {
	TestMaxQueryCosts float64 `env:"TEST_MAX_QUERY_COSTS"`
}

// ClientFactoryFunc is a function that creates an ent client from a dialect driver.
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

// driverOpts builds the standard set of stdent.DriverOption values shared by
// all providers in this package. Keeping it in one place ensures that every
// binary (web, worker, …) gets identical query-cost gating, sequential-scan
// discouragement and BeginHook wiring.
func driverOpts(cfg Config, hook stdent.BeginHookFunc) []stdent.DriverOption {
	var opts []stdent.DriverOption

	// when enabled we can check on every query if indexes are used correctly.
	if cfg.TestMaxQueryCosts > 0 {
		opts = append(opts,
			stdent.TxExecQueryLoggingLevel(zapcore.InfoLevel),
			stdent.DiscourageSequentialScans(),
			stdent.TestForMaxQueryPlanCosts(cfg.TestMaxQueryCosts),
		)
	}

	// allow some logic to be run at the beginning of every transaction. Primarily to setup
	// for Row-level security.
	if hook != nil {
		opts = append(opts, stdent.BeginHook(hook))
	}

	return opts
}

// newPoolClient wires a single *sql.DB into an ent client through the standard
// stdent driver, registering an OnStop hook on the supplied lifecycle that
// closes the *sql.DB. Shared between the rw+ro and rw-only providers so the
// driver-construction code path is exactly the same in every binary.
func newPoolClient[T stdent.Tx, C stdent.Client[T]](
	db *sql.DB,
	factory ClientFactoryFunc[T, C],
	opts []stdent.DriverOption,
	lc fx.Lifecycle,
) C {
	base := entsql.NewDriver(dialect.Postgres, entsql.Conn{ExecQuerier: db})
	drv := stdent.NewDriver(base, opts...)
	cli := factory(drv)
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return db.Close() }})
	return cli
}

// appNameDeriver returns a stdpgxfx.Deriver that pins the connection's
// application_name runtime parameter. It is shared by Provide and
// ProvideRWOnly so the per-pool naming convention stays identical.
func appNameDeriver(applicationName string) stdpgxfx.Deriver {
	return func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
		base.ConnConfig.RuntimeParams["application_name"] = applicationName
		return base
	}
}

// New provides the transactors.
func New[T stdent.Tx, C stdent.Client[T]](params Params[T, C]) (Result[T], error) {
	opts := driverOpts(params.Config, params.TxBeginSQL)

	rwClient := newPoolClient(params.RW, params.ClientFactory, opts, params.Lifecycle)
	roClient := newPoolClient(params.RO, params.ClientFactory, opts, params.Lifecycle)

	return Result[T]{
		ReadWrite: stdent.New(rwClient),
		ReadOnly:  stdent.New(roClient, stdent.ReadOnly(true)),
	}, nil
}

// Provide provides the standard read-write/read-only separation.
func Provide[T stdent.Tx, C stdent.Client[T]](applicationName string, clientFactory ClientFactoryFunc[T, C]) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdenttxfx",
		New[T, C],
		fx.Supply(clientFactory),

		// configure an application name for the connection. The "ro" pool's
		// host rewriting (for Aurora cluster endpoints) is handled by stdpgxfx.
		stdpgxfx.ProvideDeriver("rw", appNameDeriver(applicationName+"-rw")),
		stdpgxfx.ProvideDeriver("ro", appNameDeriver(applicationName+"-ro")),
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
		Provide(applicationName, clientFactory),
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
