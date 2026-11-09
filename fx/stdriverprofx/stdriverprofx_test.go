package stdriverprofx_test

import (
	"context"
	"testing"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdpgxfx/stdpgxtestfx"
	"github.com/advdv/stdgo/fx/stdriverfx/v2"
	"github.com/advdv/stdgo/fx/stdriverfx/v2/stdrivertest"
	"github.com/advdv/stdgo/fx/stdriverprofx"
	"github.com/advdv/stdgo/fx/stdriverprofx/internal/testmigrate"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/internal/stdtestpg"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"riverqueue.com/riverpro"
)

// echoArgs is the job kind the tests insert and work, alone and as the tasks
// of a workflow.
type echoArgs struct {
	Message string `json:"message"`
}

func (echoArgs) Kind() string { return "stdriverpro_test_echo" }

type echoWorker struct {
	river.WorkerDefaults[echoArgs]
}

func newEchoWorker() *echoWorker { return &echoWorker{} }

func (w *echoWorker) Work(context.Context, *river.Job[echoArgs]) error { return nil }

// completeTxArgs is the job kind that completes in a transaction of its own,
// in which it also inserts an echo job with River Pro's client.
type completeTxArgs struct{}

func (completeTxArgs) Kind() string { return "stdriverpro_test_complete_tx" }

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

func (w *completeTxWorker) Work(ctx context.Context, job *river.Job[completeTxArgs]) error {
	return stdriverfx.CompleteTx(ctx, w.pool, job, func(ctx context.Context, tx pgx.Tx) error {
		_, err := riverpro.ClientFromContext[pgx.Tx](ctx).InsertTx(ctx, tx, echoArgs{Message: "in tx"}, nil)

		return err
	})
}

type deps struct {
	fx.In

	Pool *pgxpool.Pool         `name:"rw"`
	RW   *stdriverprofx.Client `name:"rw"`
	RO   *stdriverprofx.Client `name:"ro"`
}

// setup starts an app with River Pro's clients, on a database of its own whose
// River tables, River Pro's included, testmigrate created in the default
// schema, and the test workers registered. The "ro" pool is read-only, as an
// app's would be.
func setup(tb testing.TB) deps {
	tb.Helper()

	var d deps

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{}),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdpgxtestfx.Provide(tb, stdtestpg.Image, testmigrate.Migrator, stdpgxfx.NewPgxV5Driver(), "rw", "ro"),
		stdpgxfx.ProvideDeriver("ro", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			base.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"

			return base
		}),
		stdriverfx.Provide(stdriverprofx.NewClient),
		stdriverfx.ProvideWorker[echoArgs](newEchoWorker),
		stdriverfx.ProvideWorker[completeTxArgs](newCompleteTxWorker),
		fx.Populate(&d),
	)
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return d
}

// stdriverfx.CompleteTx also completes the jobs that River Pro's client works,
// with the job's writes, made with that client, in the same transaction.
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
