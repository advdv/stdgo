package stdtx_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdtx"
	"github.com/advdv/stdgo/stdtx/stdtxpgxv5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestSetup(t *testing.T) {
	ctx, ro, rw, _, _, _ := setup(t)
	require.NotNil(t, ctx)
	require.NotNil(t, ro)
	require.NotNil(t, rw)
}

func TestBasicReadTransaction(t *testing.T) {
	ctx, ro, rw, _, _, _ := setup(t)

	for _, txr := range []*stdtx.Transactor[pgx.Tx]{ro, rw} {
		res, err := stdtx.Transact1(ctx, txr, func(ctx context.Context, tx pgx.Tx) (v int, _ error) {
			require.Equal(t, 1, stdtx.AttemptFromContext(ctx))
			require.NoError(t, tx.QueryRow(ctx, `SELECT 42`).Scan(&v))
			return v, nil
		})

		require.NoError(t, err)
		require.Equal(t, 42, res)
	}
}

func TestBasicWriteTransaction(t *testing.T) {
	ctx, ro, rw, _, _, _ := setup(t)

	err := stdtx.Transact0(ctx, ro, func(ctx context.Context, tx pgx.Tx) error {
		require.Equal(t, 1, stdtx.AttemptFromContext(ctx))
		_, err := tx.Exec(ctx, `INSERT INTO test_table (id, value) VALUES (2, 200);`)
		return err
	})

	require.ErrorContains(t, err, "INSERT in a read-only transaction")

	err = stdtx.Transact0(ctx, rw, func(ctx context.Context, tx pgx.Tx) error {
		require.Equal(t, 1, stdtx.AttemptFromContext(ctx))
		_, err := tx.Exec(ctx, `INSERT INTO test_table (id, value) VALUES (2, 200);`)
		return err
	})

	require.NoError(t, err)
}

func TestAlreadyInTransactionScope(t *testing.T) {
	ctx, _, rw, _, _, _ := setup(t)

	err := stdtx.Transact0(ctx, rw, func(ctx context.Context, tx pgx.Tx) error {
		return stdtx.Transact0(ctx, rw, func(context.Context, pgx.Tx) error {
			return nil
		})
	})

	require.ErrorIs(t, err, stdtx.ErrAlreadyInTransactionScope)
}

func TestMaxRetries(t *testing.T) {
	ctx, _, rw, _, _, _ := setup(t)

	require.ErrorContains(t, stdtx.Transact0(ctx, rw, func(ctx context.Context, _ pgx.Tx) error {
		if stdtx.AttemptFromContext(ctx) <= 51 {
			return &pgconn.PgError{Code: "40001"}
		}

		return nil
	}), "retries exceeded")
}

func TestPanicRollback(t *testing.T) {
	ctx, _, rw, obs, _, rwDrv := setup(t)

	require.PanicsWithValue(t, "some foo", func() {
		stdtx.Transact0(ctx, rw, func(ctx context.Context, _ pgx.Tx) error {
			panic("some foo")
		})
	})

	require.Len(t, obs.FilterMessage("recovered panic in tx, rolling back").All(), 1) // panic rollback

	require.Equal(t, int64(2), rwDrv.RollbackCount)
	require.Equal(t, int64(0), rwDrv.CommitCount)
}

func TestRegularRollback(t *testing.T) {
	ctx, _, rw, obs, _, rwDrv := setup(t)

	require.ErrorContains(t, stdtx.Transact0(ctx, rw, func(ctx context.Context, _ pgx.Tx) error {
		return errors.New("some error")
	}), "some error")

	require.Len(t, obs.FilterMessage("transaction handler failed, rolling back transaction").All(), 1)

	require.Equal(t, int64(2), rwDrv.RollbackCount)
	require.Equal(t, int64(0), rwDrv.CommitCount)
}

func TestAlreadyDoneIsOK(t *testing.T) {
	ctx, _, rw, _, _, rwDrv := setup(t)

	require.NoError(t, stdtx.Transact0(ctx, rw, func(ctx context.Context, tx pgx.Tx) error {
		tx.Rollback(ctx)
		return nil
	}), "some error")

	require.Equal(t, int64(1), rwDrv.RollbackCount) // by the implementation itself.
	require.Equal(t, int64(1), rwDrv.CommitCount)   // by the transactor.
}

func TestGoexitRollback(t *testing.T) {
	ctx, _, rw, _, _, rwDrv := setup(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		stdtx.Transact0(ctx, rw, func(ctx context.Context, _ pgx.Tx) error {
			runtime.Goexit()
			return nil
		})
	}()
	<-done

	require.Equal(t, int64(1), rwDrv.RollbackCount)
}

func TestSerializableFailure(t *testing.T) {
	ctx, _, rw, _, _, rwDrv := setup(t)
	start := make(chan struct{})
	var numAttempts int64

	// this work on the transaction creates contention and forces the retry.
	txWork := func(ctx context.Context, tx pgx.Tx) error {
		<-start

		atomic.AddInt64(&numAttempts, int64(stdtx.AttemptFromContext(ctx)))

		var value int
		if err := tx.
			QueryRow(ctx, `SELECT value FROM test_table WHERE id = 1`).
			Scan(&value); err != nil {
			panic("should have rows")
		}

		time.Sleep(time.Millisecond * 50)

		if _, err := tx.Exec(ctx, `UPDATE test_table SET value = value + 10 WHERE id = 1`); err != nil {
			return err
		}

		return nil
	}

	// setup both transactions, waiting to be executed
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		assert.NoError(t, stdtx.Transact0(ctx, rw, txWork))
	}()

	go func() {
		defer wg.Done()
		assert.NoError(t, stdtx.Transact0(ctx, rw, txWork))
	}()

	// unblock both transaction and wait to be done
	close(start)
	wg.Wait()

	// due to retries the total nr of attempts across the transactions is 4. It is not 3 because retrying
	// transaction would first have an attempt count of 1, then 2. Plus the first tx attempt 1. Makes 4.
	require.Equal(t, int64(4), numAttempts)
	require.Equal(t, int64(2), rwDrv.CommitCount)
	require.Equal(t, int64(4), rwDrv.RollbackCount)
}

func setup(t *testing.T) (context.Context, *stdtx.Transactor[pgx.Tx], *stdtx.Transactor[pgx.Tx], *observer.ObservedLogs, *countingPgxV5Driver, *countingPgxV5Driver) {
	t.Helper()

	zc, obs := observer.New(zapcore.DebugLevel)
	tzc := zapcore.NewCore(zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
		zaptest.NewTestingWriter(t), zapcore.DebugLevel)
	ctx := stdctx.WithLogger(t.Context(), zap.New(zapcore.NewTee(zc, tzc)))

	cfg, err := pgx.ParseConfig(`postgresql://postgres:postgres@localhost:5440/postgres`)
	require.NoError(t, err)

	pgtCfg := pgtestdb.Custom(t, pgtestdb.Config{
		DriverName: "pgx",
		Host:       cfg.Host,
		Port:       fmt.Sprintf("%d", cfg.Port),
		User:       cfg.User,
		Password:   cfg.Password,
		Database:   cfg.Database,
	}, pgtestdb.NoopMigrator{})

	db, err := pgxpool.New(ctx, pgtCfg.URL())
	require.NoError(t, err)
	t.Cleanup(db.Close)

	_, err = db.Exec(ctx, `CREATE TABLE test_table (id SERIAL PRIMARY KEY, value INT);`)
	require.NoError(t, err)

	_, err = db.Exec(ctx, `INSERT INTO test_table (id, value) VALUES (1, 100);`)
	require.NoError(t, err)

	roDrv := &countingPgxV5Driver{stdtxpgxv5.New(db, stdtxpgxv5.AccessMode(pgx.ReadOnly)), 0, 0}
	rwDrv := &countingPgxV5Driver{stdtxpgxv5.New(db), 0, 0}

	return ctx,
		stdtx.NewTransactor(roDrv),
		stdtx.NewTransactor(rwDrv),
		obs,
		roDrv,
		rwDrv
}

type countingPgxV5Driver struct {
	stdtx.Driver[pgx.Tx]
	RollbackCount int64
	CommitCount   int64
}

func (d *countingPgxV5Driver) RollbackTx(ctx context.Context, tx pgx.Tx) error {
	atomic.AddInt64(&d.RollbackCount, 1)
	return d.Driver.RollbackTx(ctx, tx)
}

func (d *countingPgxV5Driver) CommitTx(ctx context.Context, tx pgx.Tx) error {
	atomic.AddInt64(&d.CommitCount, 1)
	return d.Driver.CommitTx(ctx, tx)
}
