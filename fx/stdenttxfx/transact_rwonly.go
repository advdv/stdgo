package stdenttxfx

import (
	"database/sql"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/stdent"
	"github.com/advdv/stdgo/stdfx"
	"github.com/peterldowns/pgtestdb"
	"go.uber.org/fx"
)

// ParamsRWOnly is the read-write-only counterpart of Params: it asks for the
// "rw" *sql.DB but does NOT ask for an "ro" pool. Use it from binaries (such
// as background workers, CLIs, or migration tools) that have no need to talk
// to a read replica and shouldn't open a connection pool against one.
type ParamsRWOnly[T stdent.Tx, C stdent.Client[T]] struct {
	fx.In
	fx.Lifecycle
	Config
	RW            *sql.DB `name:"rw"`
	ClientFactory ClientFactoryFunc[T, C]
	TxBeginSQL    stdent.BeginHookFunc `optional:"true"`
}

// ResultRWOnly only produces the "rw" transactor. The named output stays the
// same as Result.ReadWrite so downstream code that injects `name:"rw"` works
// unchanged regardless of which provider was used.
type ResultRWOnly[T stdent.Tx] struct {
	fx.Out
	ReadWrite *stdent.Transactor[T] `name:"rw"`
}

// NewRWOnly is the read-write-only counterpart of New. It uses the same
// driverOpts and newPoolClient helpers as New so the rw transactor it returns
// is byte-identical to the one Provide produces — in particular, the
// BeginHook and standard driver options are wired in the same way.
func NewRWOnly[T stdent.Tx, C stdent.Client[T]](params ParamsRWOnly[T, C]) (ResultRWOnly[T], error) {
	opts := driverOpts(params.Config, params.TxBeginSQL)

	rwClient := newPoolClient(params.RW, params.ClientFactory, opts, params.Lifecycle)

	return ResultRWOnly[T]{
		ReadWrite: stdent.New(rwClient),
	}, nil
}

// ProvideRWOnly is the read-write-only counterpart of Provide. It deliberately
// does NOT register a "ro" deriver — callers that wire stdpgxfx must also
// avoid declaring "ro" as a derived pool, so no read-replica connection pool
// is opened. The "-rw" application_name suffix is kept so DBA tooling can
// still distinguish worker traffic from the corresponding web traffic.
func ProvideRWOnly[T stdent.Tx, C stdent.Client[T]](
	applicationName string,
	clientFactory ClientFactoryFunc[T, C],
) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdenttxfx",
		NewRWOnly[T, C],
		fx.Supply(clientFactory),

		// configure an application name for the connection. Only the "rw" pool
		// is registered here — the "ro" deriver is intentionally omitted.
		stdpgxfx.ProvideDeriver("rw", appNameDeriver(applicationName+"-rw")),
	)
}

// TestProvideRWOnly is the read-write-only counterpart of TestProvide. It
// supplies the same pgtestdb.Role and stdpgxfx.EndRole values as TestProvide
// so test setups can swap between the two providers without changing the
// surrounding wiring.
func TestProvideRWOnly[T stdent.Tx, C stdent.Client[T]](
	applicationName,
	endRoleUsername,
	endRolePassword string,
	clientFactory ClientFactoryFunc[T, C],
) fx.Option {
	return fx.Options(
		ProvideRWOnly(applicationName, clientFactory),
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
