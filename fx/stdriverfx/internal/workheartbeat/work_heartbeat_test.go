package workheartbeat_test

import (
	"context"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdriverfx"
	"github.com/advdv/stdgo/fx/stdriverfx/internal/testsnapshot"
	"github.com/advdv/stdgo/fx/stdriverfx/internal/workheartbeat"
	workheartbeatv1 "github.com/advdv/stdgo/fx/stdriverfx/internal/workheartbeat/v1"
	"github.com/advdv/stdgo/fx/stdriverfx/stdrivertest"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdtx"
	"github.com/advdv/stdgo/stdtx/stdtxpgxv5"
	"github.com/jackc/pgx/v5"
	"github.com/peterldowns/pgtestdb"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestHeartbeat(t *testing.T) {
	t.Parallel()

	var heartbeat stdriverfx.Enqueuer[*workheartbeatv1.Args]
	ctx, wrks, txr := setup(t, &heartbeat)

	args := workheartbeatv1.Args_builder{BlockFor: durationpb.New(time.Millisecond)}.Build()
	stdrivertest.EnqueueJob(ctx, t, txr, heartbeat, args)

	jobs := stdrivertest.WaitForJob(ctx, t, txr, wrks, args, 1,
		stdrivertest.JobInState(rivertype.JobStateRunning, rivertype.JobStateCompleted))
	require.Len(t, jobs, 1)
}

func setup(tb testing.TB, populateMore ...any) (context.Context, *stdriverfx.Workers, *stdtx.Transactor[pgx.Tx]) {
	tb.Helper()

	var deps struct {
		fx.In
		*zap.Logger
		*stdriverfx.Workers
		*stdtx.Transactor[pgx.Tx] `name:"rw"`
		*observer.ObservedLogs
	}

	app := fxtest.New(tb,
		fx.Populate(&deps),
		fx.Supply(stdenvcfg.Environment{
			"STDRIVER_SOFT_STOP_TIMEOUT": "100ms",
			"STDPGX_MAIN_DATABASE_URL":   "postgresql://postgres:postgres@localhost:5440/postgres",
		}),

		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdpgxfx.TestProvide(tb, testsnapshot.Migrator, stdpgxfx.NewPgxV5Driver(), "rw"),
		fx.Provide(fx.Annotate(stdtxpgxv5.New, fx.ParamTags(`name:"rw"`))),
		fx.Provide(fx.Annotate(stdtx.NewTransactor[pgx.Tx], fx.ResultTags(`name:"rw"`))),
		fx.Supply(
			&pgtestdb.Role{
				Username: "postgres", Password: "postgres",
			}, &stdpgxfx.EndRole{
				Username: "postgres",
				Password: "postgres",
			}),

		stdriverfx.Provide(),
		workheartbeat.Provide(),

		fx.Provide(protovalidate.New),
		fx.Populate(populateMore...),
	)

	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	ctx := tb.Context()
	ctx = stdctx.WithLogger(ctx, deps.Logger)

	return ctx, deps.Workers, deps.Transactor
}
