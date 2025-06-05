package stdriverfx_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdriverfx"
	testsnapshot "github.com/advdv/stdgo/fx/stdriverfx/internal/testsnapshot"
	"github.com/advdv/stdgo/fx/stdriverfx/internal/workheartbeat"
	workheartbeatv1 "github.com/advdv/stdgo/fx/stdriverfx/internal/workheartbeat/v1"
	"github.com/advdv/stdgo/fx/stdriverfx/stdrivertest"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdtx"
	"github.com/advdv/stdgo/stdtx/stdtxpgxv5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestSetup(t *testing.T) {
	t.Parallel()

	// expect a soft stop.
	var obs *observer.ObservedLogs
	t.Cleanup(func() {
		require.Len(t, obs.FilterMessageSnippet("soft stop succeeded, no hard stop necessary").All(), 1)
	})

	var rcfg river.Config

	ctx, wrks, txr := setup(t, &obs, &rcfg)
	require.NotNil(t, ctx)
	require.NotNil(t, wrks)
	require.NotNil(t, txr)
	require.NoError(t, wrks.Ping(t.Context()))

	require.Len(t, rcfg.PeriodicJobs, 1)
}

// setup will use fx to setup a workers instance.
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
		stdpgxfx.TestProvide(tb, testsnapshot.Migrator, stdpgxfx.NewPgxV5Driver(), "rw", "rui"),
		stdpgxfx.ProvideDeriver("rui", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			base.ConnConfig.RuntimeParams["search_path"] = "workers" // so the ui can find the search path
			return base
		}),

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

func TestHardStop(t *testing.T) {
	t.Parallel()

	var obs *observer.ObservedLogs
	t.Cleanup(func() {
		require.Len(t, obs.FilterMessageSnippet("Hard stop started").All(), 1)
	})

	var heartbeat stdriverfx.Enqueuer[*workheartbeatv1.Args]
	ctx, wrks, txr := setup(t, &obs, &heartbeat)

	args := workheartbeatv1.Args_builder{BlockFor: durationpb.New(time.Hour)}.Build() // long time, to trigger hard stop
	stdrivertest.EnqueueJob(ctx, t, txr, heartbeat, args)

	jobs := stdrivertest.WaitForJob(ctx, t, txr, wrks, args, 1,
		stdrivertest.JobInState(rivertype.JobStateRunning))
	require.Len(t, jobs, 1)
}

func TestSoftStopAfterCompleted(t *testing.T) {
	t.Parallel()

	var obs *observer.ObservedLogs
	t.Cleanup(func() {
		require.Len(t, obs.FilterMessageSnippet("soft stop succeeded, no hard stop necessary").All(), 1)
	})

	var heartbeat stdriverfx.Enqueuer[*workheartbeatv1.Args]
	ctx, wrks, txr := setup(t, &obs, &heartbeat)
	args := workheartbeatv1.Args_builder{BlockFor: durationpb.New(time.Millisecond)}.Build()
	stdrivertest.EnqueueJob(ctx, t, txr, heartbeat, args)

	// wait for a job to be completed and to have the log show up in the metadata. NOTE: somehow the metadata
	// is written to the db async so we can't just wait and assert the metadata.
	jobs := stdrivertest.WaitForJob(ctx, t, txr, wrks, args, 1,
		func(job *rivertype.JobRow) bool {
			return stdrivertest.JobInState(rivertype.JobStateCompleted)(job) &&
				bytes.Contains(job.Metadata, []byte("heartbeat job"))
		})
	require.Len(t, jobs, 1)
	require.Contains(t, string(jobs[0].Output()), `blockTook`)
}

func TestEnqeueuValidation(t *testing.T) {
	t.Parallel()

	var heartbeat stdriverfx.Enqueuer[*workheartbeatv1.Args]
	ctx, _, txr := setup(t, &heartbeat)
	args := workheartbeatv1.Args_builder{}.Build()

	err := stdtx.Transact0(ctx, txr, func(ctx context.Context, tx pgx.Tx) error {
		return heartbeat.Enqueue(ctx, tx, args)
	})

	var valErr *protovalidate.ValidationError
	require.ErrorAs(t, err, &valErr)
}

func TestWorkArgumentValidation(t *testing.T) {
	t.Parallel()

	var val protovalidate.Validator
	ctx, _, txr := setup(t, &val)
	tw := stdriverfx.NewTransactWorker(txr, val, func(_ context.Context, _ pgx.Tx, job *river.Job[*workheartbeatv1.Args]) (*workheartbeatv1.Output, error) {
		return workheartbeatv1.Output_builder{}.Build(), nil
	})

	err := tw.Work(ctx, &river.Job[*workheartbeatv1.Args]{})
	var valErr *protovalidate.ValidationError
	require.ErrorAs(t, err, &valErr)
	require.ErrorIs(t, err, &river.JobCancelError{})

	require.ErrorContains(t, err, "validate job arguments")
}

func TestWorkOutputValidation(t *testing.T) {
	t.Parallel()

	var val protovalidate.Validator
	ctx, _, txr := setup(t, &val)
	tw := stdriverfx.NewTransactWorker(txr, val, func(_ context.Context, _ pgx.Tx, job *river.Job[*workheartbeatv1.Args]) (*workheartbeatv1.Output, error) {
		return workheartbeatv1.Output_builder{}.Build(), nil
	})

	err := tw.Work(ctx, &river.Job[*workheartbeatv1.Args]{Args: workheartbeatv1.Args_builder{
		BlockFor: durationpb.New(time.Millisecond * 10),
	}.Build()})

	var valErr *protovalidate.ValidationError
	require.ErrorAs(t, err, &valErr)
	require.ErrorIs(t, err, &river.JobCancelError{})
	require.ErrorContains(t, err, "validate job output")
}

func TestWorkOutputNilValidation(t *testing.T) {
	t.Parallel()

	var val protovalidate.Validator
	ctx, _, txr := setup(t, &val)
	tw := stdriverfx.NewTransactWorker(txr, val, func(_ context.Context, _ pgx.Tx, job *river.Job[*workheartbeatv1.Args]) (*workheartbeatv1.Output, error) {
		return nil, nil
	})

	err := tw.Work(ctx, &river.Job[*workheartbeatv1.Args]{Args: workheartbeatv1.Args_builder{
		BlockFor: durationpb.New(time.Millisecond * 10),
	}.Build()})

	var valErr *protovalidate.ValidationError
	require.ErrorAs(t, err, &valErr)
	require.ErrorIs(t, err, &river.JobCancelError{})
	require.ErrorContains(t, err, "validate job output")
}
