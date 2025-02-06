package stdent_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdent"
	"github.com/jackc/pgx/v5"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type mockTx1 struct {
	client *mockClient1
	tx     *sql.Tx
}

func (tx *mockTx1) Commit() error { atomic.AddInt64(&tx.client.numCommits, 1); return tx.tx.Commit() }
func (tx *mockTx1) Rollback() error {
	atomic.AddInt64(&tx.client.numRollbacks, 1)
	return tx.tx.Rollback()
}

type mockClient1 struct {
	tb testing.TB
	db *sql.DB

	numTxs       int64
	numCommits   int64
	numRollbacks int64
}

func (c *mockClient1) BeginTx(ctx context.Context, opts *entsql.TxOptions) (*mockTx1, error) {
	atomic.AddInt64(&c.numTxs, 1)

	tx, err := c.db.BeginTx(ctx, opts)
	require.NoError(c.tb, err)

	return &mockTx1{client: c, tx: tx}, nil
}

func TestWithTx1(t *testing.T) {
	ctx, client, txr := setup(t)

	res, err := stdent.Transact1(ctx, txr, func(ctx context.Context, tx *mockTx1) (int64, error) {
		require.Equal(t, stdent.TxFromContext[*mockTx1](ctx), tx)
		return 42, nil
	})

	require.NoError(t, err)
	require.Equal(t, int64(42), res)
	require.Equal(t, int64(1), client.numTxs)
	require.Equal(t, int64(1), client.numCommits)
	require.Equal(t, int64(0), client.numRollbacks)
}

func TestNestedWithTx(t *testing.T) {
	ctx, client, txr := setup(t)

	var reachedInner bool
	res, err := stdent.Transact1(ctx, txr, func(ctx context.Context, tx1 *mockTx1) (int64, error) {
		return 44, stdent.Transact0(ctx, txr, func(ctx context.Context, tx2 *mockTx1) error {
			reachedInner = true
			return nil
		})
	})

	require.NoError(t, err)
	require.Equal(t, int64(44), res)
	require.Equal(t, int64(1), client.numTxs)
	require.Equal(t, int64(1), client.numCommits)
	require.Equal(t, int64(0), client.numRollbacks)
	require.True(t, reachedInner)
}

func TestMaxRetries(t *testing.T) {
	ctx, client, txr := setup(t)

	require.ErrorContains(t, stdent.Transact0(ctx, txr, func(ctx context.Context, _ *mockTx1) error {
		if stdent.AttemptFromContext(ctx) <= 51 {
			return &pgconn.PgError{Code: "40001"}
		}

		return nil
	}), "retries exceeded")

	_ = client
}

func TestPanicRollback(t *testing.T) {
	ctx, client, txr := setup(t)

	require.PanicsWithValue(t, "some foo", func() {
		stdent.Transact0(ctx, txr, func(ctx context.Context, _ *mockTx1) error {
			panic("some foo")
		})
	})

	require.Equal(t, int64(1), client.numRollbacks)
	require.Equal(t, int64(0), client.numCommits)
}

func TestRegularRollback(t *testing.T) {
	ctx, client, txr := setup(t)

	require.ErrorContains(t, stdent.Transact0(ctx, txr, func(ctx context.Context, _ *mockTx1) error {
		return errors.New("some error")
	}), "some error")

	require.Equal(t, int64(1), client.numRollbacks)
	require.Equal(t, int64(0), client.numCommits)
}

func TestSerializableFailure(t *testing.T) {
	ctx, client, txr := setup(t)
	start := make(chan struct{})
	var numAttempts int64

	// this work on the transaction creates contention and forces the retry.
	txWork := func(ctx context.Context, tx *mockTx1) error {
		<-start

		atomic.AddInt64(&numAttempts, int64(stdent.AttemptFromContext(ctx)))

		var value int
		if err := tx.tx.
			QueryRowContext(ctx, `SELECT value FROM test_table WHERE id = 1`).
			Scan(&value); err != nil {
			panic("should have rows")
		}

		time.Sleep(time.Millisecond * 50)

		if _, err := tx.tx.ExecContext(ctx, `UPDATE test_table SET value = value + 10 WHERE id = 1`); err != nil {
			return err
		}

		return nil
	}

	// setup both transactions, waiting to be executed
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		assert.NoError(t, stdent.Transact0(ctx, txr, txWork))
	}()

	go func() {
		defer wg.Done()
		assert.NoError(t, stdent.Transact0(ctx, txr, txWork))
	}()

	// unblock both transaction and wait to be done
	close(start)
	wg.Wait()

	// due to retries the total nr of attempts across the transactions is 4. It is not 3 because retrying
	// transaction would first have an attempt count of 1, then 2. Plus the first tx attempt 1. Makes 4.
	require.Equal(t, int64(4), numAttempts)
	require.Equal(t, int64(2), client.numCommits)
	require.Equal(t, int64(1), client.numRollbacks)
}

func setup(t *testing.T) (context.Context, *mockClient1, *stdent.Transactor[*mockTx1]) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	ctx = stdctx.WithLogger(ctx, zap.NewNop())

	cfg, err := pgx.ParseConfig(`postgresql://postgres:postgres@localhost:5440/postgres`)
	require.NoError(t, err)

	db := pgtestdb.New(t, pgtestdb.Config{
		DriverName: "pgx",
		Host:       cfg.Host,
		Port:       fmt.Sprintf("%d", cfg.Port),
		User:       cfg.User,
		Password:   cfg.Password,
		Database:   cfg.Database,
	}, pgtestdb.NoopMigrator{})

	client := &mockClient1{tb: t, db: db}

	_, err = db.ExecContext(ctx, `CREATE TABLE test_table (id SERIAL PRIMARY KEY, value INT);`)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `INSERT INTO test_table (id, value) VALUES (1, 100);`)
	require.NoError(t, err)

	return ctx, client, stdent.New(client)
}
