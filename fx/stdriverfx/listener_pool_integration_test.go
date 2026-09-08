package stdriverfx_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdriverfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverdatabasesql"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

type listenerPoolArgs struct{}

func (listenerPoolArgs) Kind() string { return "listener_pool_integration" }

type listenerPoolWorker struct {
	river.WorkerDefaults[listenerPoolArgs]
	db *sql.DB
}

func (w *listenerPoolWorker) Work(ctx context.Context, job *river.Job[listenerPoolArgs]) error {
	for _, commit := range []bool{false, true} {
		tx, err := w.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `INSERT INTO workers.business (job_id) VALUES ($1)`, job.ID); err != nil {
			return err
		}
		if _, err := river.JobCompleteTx[*riverdatabasesql.Driver](ctx, tx, job); err != nil {
			return err
		}
		if commit {
			return tx.Commit()
		}
		if err := tx.Rollback(); err != nil {
			return err
		}
		var unchanged bool
		if err := w.db.QueryRowContext(ctx, `SELECT state = 'running' AND
			NOT EXISTS (SELECT 1 FROM workers.business WHERE job_id = $1)
			FROM workers.river_job WHERE id = $1`, job.ID).Scan(&unchanged); err != nil {
			return err
		}
		if !unchanged {
			return fmt.Errorf("business write and job completion did not roll back together")
		}
	}
	return nil
}

// Synchronize with the initial empty fetch rather than guessing at startup timing.
type (
	listenerFetchTrace struct{ done chan struct{} }
	listenerFetchKey   struct{}
)

func (f *listenerFetchTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, listenerFetchKey{}, strings.Contains(data.SQL, "-- name: JobGetAvailable :many"))
}

func (f *listenerFetchTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if fetching, _ := ctx.Value(listenerFetchKey{}).(bool); fetching && data.Err == nil {
		select {
		case f.done <- struct{}{}:
		default:
		}
	}
}

func TestListenerPoolIntegration(t *testing.T) {
	trace := &listenerFetchTrace{done: make(chan struct{}, 1)}
	var deps struct {
		fx.In
		DB       *sql.DB       `name:"rw"`
		Listener *pgxpool.Pool `name:"river-listener"`
	}
	app := fxtest.New(t,
		fx.Populate(&deps),
		fx.Supply(stdenvcfg.Environment{"STDPGX_MAIN_DATABASE_URL": "postgresql://postgres:postgres@localhost:5440/postgres"}),
		stdzapfx.Fx(), stdzapfx.TestProvide(t),
		stdpgxfx.TestProvide(t, pgtestdb.NoopMigrator{}, stdpgxfx.NewStandardDriver(), "rw"),
		stdpgxfx.ProvideDeriver("rw", func(_ *zap.Logger, cfg *pgxpool.Config) *pgxpool.Config {
			cfg.ConnConfig.Tracer = trace
			return cfg
		}),
		stdriverfx.ProvideListenerPool("rw"),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := deps.DB.ExecContext(ctx, `CREATE SCHEMA workers; CREATE TABLE workers.business (job_id bigint PRIMARY KEY)`)
	require.NoError(t, err)
	driver := riverdatabasesql.NewWithPgxListener(deps.DB, deps.Listener)
	migrator, err := rivermigrate.New(driver, &rivermigrate.Config{Schema: "workers"})
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)
	workers := river.NewWorkers()
	river.AddWorker(workers, &listenerPoolWorker{db: deps.DB})
	client, err := river.NewClient(driver, &river.Config{
		FetchPollInterval: time.Hour,
		Queues:            map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}},
		Schema:            "workers", Workers: workers,
	})
	require.NoError(t, err)
	events, unsubscribe := client.Subscribe(river.EventKindJobCompleted)
	defer unsubscribe()
	require.NoError(t, client.Start(ctx))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, client.Stop(stopCtx))
	})
	select {
	case <-trace.done:
	case <-ctx.Done():
		t.Fatal("initial empty fetch did not complete", ctx.Err())
	}
	// A separate client cannot wake this worker in-process. With startup fetching
	// finished and polling an hour away, completion requires LISTEN/NOTIFY.
	inserter, err := river.NewClient(riverdatabasesql.New(deps.DB), &river.Config{Schema: "workers"})
	require.NoError(t, err)
	inserted, err := inserter.Insert(ctx, listenerPoolArgs{}, nil)
	require.NoError(t, err)
	select {
	case event := <-events:
		require.Equal(t, inserted.Job.ID, event.Job.ID)
		require.Equal(t, rivertype.JobStateCompleted, event.Job.State)
	case <-ctx.Done():
		t.Fatal("LISTEN-driven transactional completion did not finish", ctx.Err())
	}
	var writes int
	require.NoError(t, deps.DB.QueryRowContext(ctx, `SELECT count(*) FROM workers.business WHERE job_id = $1`, inserted.Job.ID).Scan(&writes))
	require.Equal(t, 1, writes)
}
