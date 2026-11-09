package stdriverfx_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdpgxfx/stdpgxtestfx"
	"github.com/advdv/stdgo/fx/stdriverfx/v2"
	"github.com/advdv/stdgo/fx/stdriverfx/v2/internal/testmigrate"
	"github.com/advdv/stdgo/fx/stdriverfx/v2/stdrivertest"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/internal/stdtestpg"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// echoArgs is the job kind the tests insert and work.
type echoArgs struct {
	Message string `json:"message"`
}

func (echoArgs) Kind() string { return "stdriver_test_echo" }

// echoWorker logs the message of every job it works.
type echoWorker struct {
	river.WorkerDefaults[echoArgs]
}

func newEchoWorker() *echoWorker { return &echoWorker{} }

func (w *echoWorker) Work(ctx context.Context, job *river.Job[echoArgs]) error {
	stdctx.Log(ctx).Info("echo", zap.String("message", job.Args.Message))

	return nil
}

// completeTxArgs is the job kind that completes in a transaction of its own,
// in which it also inserts an echo job, and then fails when Fail is set.
type completeTxArgs struct {
	Fail bool `json:"fail"`
}

func (completeTxArgs) Kind() string { return "stdriver_test_complete_tx" }

type completeTxWorker struct {
	river.WorkerDefaults[completeTxArgs]

	pool *pgxpool.Pool
}

type completeTxParams struct {
	fx.In

	Pool *pgxpool.Pool `name:"rw"`
}

func newCompleteTxWorker(p completeTxParams) *completeTxWorker {
	return &completeTxWorker{pool: p.Pool}
}

var errFail = errors.New("fail")

func (w *completeTxWorker) Work(ctx context.Context, job *river.Job[completeTxArgs]) error {
	return stdriverfx.CompleteTx(ctx, w.pool, job, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := river.ClientFromContext[pgx.Tx](ctx).InsertTx(ctx, tx, echoArgs{Message: "in tx"}, nil); err != nil {
			return err
		}
		if job.Args.Fail {
			return errFail
		}

		return nil
	})
}

type deps struct {
	fx.In

	Obs  *observer.ObservedLogs
	Pool *pgxpool.Pool         `name:"rw"`
	RW   *river.Client[pgx.Tx] `name:"rw"`
	RO   *river.Client[pgx.Tx] `name:"ro"`
}

// provide gives a test app open-source River's clients, on a database of its
// own whose River tables testmigrate created in the default schema. The "ro"
// pool is read-only, as an app's would be.
func provide(tb testing.TB) fx.Option {
	tb.Helper()

	return fx.Options(
		stdpgxtestfx.Provide(tb, stdtestpg.Image, testmigrate.Migrator, stdpgxfx.NewPgxV5Driver(), "rw", "ro"),
		stdpgxfx.ProvideDeriver("ro", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			base.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"

			return base
		}),
		stdriverfx.Provide(stdriverfx.NewClient),
	)
}

// newApp builds an app with the clients, the echo worker registered, and env as
// the environment.
func newApp(tb testing.TB, env map[string]string, d *deps) *fxtest.App {
	tb.Helper()

	return fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		provide(tb),
		stdriverfx.ProvideWorker[echoArgs](newEchoWorker),
		stdriverfx.ProvideWorker[completeTxArgs](newCompleteTxWorker),
		fx.Populate(d),
	)
}

// setup starts an app from newApp with the default configuration.
func setup(tb testing.TB) deps {
	tb.Helper()

	var d deps

	app := newApp(tb, map[string]string{}, &d)
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return d
}

// A registered worker is worked by the started "rw" client, and the "ro"
// client reads the job from the same schema.
func TestWithWorker(t *testing.T) {
	t.Parallel()

	d := setup(t)

	res, err := d.RW.Insert(t.Context(), echoArgs{Message: "hello"}, nil)
	require.NoError(t, err)
	stdrivertest.RequireJobCompleted(t, d.RO, res.Job.ID)
}

// A worker logs with the logger in its job's context: to the app's logs, with
// the job's fields, and to the attempt's log that River stores with the job.
func TestWithWorkerLogs(t *testing.T) {
	t.Parallel()

	d := setup(t)

	res, err := d.RW.Insert(t.Context(), echoArgs{Message: "hello"}, nil)
	require.NoError(t, err)

	job := stdrivertest.RequireJobCompleted(t, d.RO, res.Job.ID)

	entries := d.Obs.FilterMessage("echo").All()
	require.Len(t, entries, 1)
	require.Equal(t, map[string]any{
		"job_id":      res.Job.ID,
		"job_kind":    echoArgs{}.Kind(),
		"job_attempt": int64(1),
		"message":     "hello",
	}, entries[0].ContextMap())

	var metadata struct {
		Log []struct {
			Attempt int    `json:"attempt"`
			Log     string `json:"log"`
		} `json:"river:log"`
	}
	require.NoError(t, json.Unmarshal(job.Metadata, &metadata))
	require.Len(t, metadata.Log, 1)
	require.Equal(t, 1, metadata.Log[0].Attempt)
	require.Contains(t, metadata.Log[0].Log, "echo")
	require.Contains(t, metadata.Log[0].Log, `"message": "hello"`)
}

// The job's writes and its completion commit together.
func TestCompleteTx(t *testing.T) {
	t.Parallel()

	d := setup(t)

	res, err := d.RW.Insert(t.Context(), completeTxArgs{}, nil)
	require.NoError(t, err)
	stdrivertest.RequireJobCompleted(t, d.RW, res.Job.ID)

	jobs, err := d.RO.JobList(t.Context(), river.NewJobListParams().Kinds(echoArgs{}.Kind()))
	require.NoError(t, err)
	require.Len(t, jobs.Jobs, 1)
}

// When the job's writes fail, neither they nor its completion commit.
func TestCompleteTxRollsBack(t *testing.T) {
	t.Parallel()

	d := setup(t)

	res, err := d.RW.Insert(t.Context(), completeTxArgs{Fail: true}, &river.InsertOpts{MaxAttempts: 1})
	require.NoError(t, err)

	job := stdrivertest.Job(t, d.RW, res.Job.ID)
	require.Equal(t, rivertype.JobStateDiscarded, job.State)
	require.Len(t, job.Errors, 1)
	require.Contains(t, job.Errors[0].Error, errFail.Error())

	jobs, err := d.RO.JobList(t.Context(), river.NewJobListParams().Kinds(echoArgs{}.Kind()))
	require.NoError(t, err)
	require.Empty(t, jobs.Jobs)
}

func TestInsertReadOnlyFails(t *testing.T) {
	t.Parallel()

	_, err := setup(t).RO.Insert(t.Context(), echoArgs{Message: "denied"}, nil)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "25006", pgErr.Code) // read_only_sql_transaction
}

// The "rw" client stops before the pools close, so shutdown neither waits out
// the stop timeout nor logs errors.
func TestStopsBeforePoolsClose(t *testing.T) {
	t.Parallel()

	var d deps

	app := newApp(t, map[string]string{"STDRIVER_STOP_TIMEOUT": "10s"}, &d)
	app.RequireStart()

	started := time.Now()
	app.RequireStop()
	require.Less(t, time.Since(started), 5*time.Second)
	require.Empty(t, d.Obs.FilterLevelExact(zap.ErrorLevel).All())
	require.Empty(t, d.Obs.FilterLevelExact(zap.WarnLevel).All())
}

// Without workers River refuses to start, so the "rw" client is not started.
func TestStartsWithoutWorkers(t *testing.T) {
	t.Parallel()

	var obs *observer.ObservedLogs

	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{}),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		provide(t),
		fx.Populate(&obs),
	)
	app.RequireStart()
	app.RequireStop()

	require.Len(t, obs.FilterMessage("no river workers registered, not working jobs").All(), 1)
}

// Both clients read River's tables from the configured schema: here one
// without them. The app registers no workers, so neither client works jobs.
func TestSchemaFromConfig(t *testing.T) {
	t.Parallel()

	var clients struct {
		fx.In

		RW *river.Client[pgx.Tx] `name:"rw"`
		RO *river.Client[pgx.Tx] `name:"ro"`
	}

	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{"STDRIVER_SCHEMA": "public"}),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		provide(t),
		fx.Populate(&clients),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	for _, client := range []*river.Client[pgx.Tx]{clients.RW, clients.RO} {
		_, err := client.JobList(t.Context(), river.NewJobListParams())

		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		require.Equal(t, "42P01", pgErr.Code) // undefined_table
	}
}
