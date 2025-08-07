// Package stdtxpgxv5 implements the stdtx.Driver for pgx/v5 postgres driver.
package stdtxpgxv5

import (
	"context"
	"fmt"
	"strings"

	"github.com/advdv/stdgo/stdtx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// driver implements the stdtx.Driver.
type driver struct {
	db   *pgxpool.Pool
	opts options
}

// New implements the driver for pgx v5.
func New(db *pgxpool.Pool, opts ...Option) stdtx.Driver[pgx.Tx] {
	drv := driver{db: db}

	AccessMode(pgx.ReadWrite)(&drv.opts)
	IsolationMode(pgx.Serializable)(&drv.opts)
	DiscourageSeqScan(false)(&drv.opts)
	BeginWithSQL(func(_ context.Context, sql *strings.Builder, _ pgx.Tx) (*strings.Builder, error) {
		return sql, nil
	})(&drv.opts)
	OnTxCommit(func(context.Context, pgx.TxAccessMode, pgx.Tx) error { return nil })(&drv.opts)

	for _, opt := range opts {
		opt(&drv.opts)
	}

	return drv
}

// setup the tx when it's created. Allows additional sql to be run on every tx.
func (d driver) setupTx(ctx context.Context, tx pgx.Tx) (err error) {
	sql := &strings.Builder{}

	// call any customization to sql ran at the start of the tx.
	sql, err = d.opts.txBeginSQL(ctx, sql, tx)
	if err != nil {
		return fmt.Errorf("setup hook: %w", err)
	}

	// build-in option to discourage sequential scans.
	if d.opts.discourageSeqScans {
		sql.WriteString(`SET LOCAL enable_seqscan = OFF;`)
	}

	// no sql to execute
	if sql.String() == "" {
		return nil
	}

	// begin sql is never asserted for max query costs.
	ctx = stdtx.WithNoTestForMaxQueryPlanCosts(ctx)
	if _, err := tx.Exec(ctx, sql.String()); err != nil {
		return fmt.Errorf("execute tx begin sql: %w", err)
	}

	return nil
}

// BeginTx implements the starting of a transaction.
func (d driver) BeginTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := d.db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   d.opts.txIsoLevel,
		AccessMode: d.opts.txAccessMode,
	})
	if err != nil {
		return nil, err // return transparently.
	}

	// wrap it immediately so hook sql threated the same
	tx = wtx{tx, d.opts.maxQueryPlanCosts, d.opts.txExecQueryLogLevel}

	if err := d.setupTx(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return nil, fmt.Errorf("setup tx, rolled back: %w", err)
	}

	return tx, nil
}

// RollbackTx implements the rolling back of a transaction.
func (d driver) RollbackTx(ctx context.Context, tx pgx.Tx) error {
	return tx.Rollback(ctx)
}

// CommitTx implements the committing of a transaction.
func (d driver) CommitTx(ctx context.Context, tx pgx.Tx) error {
	if err := d.opts.onTxCommit(ctx, d.opts.txAccessMode, tx); err != nil {
		return fmt.Errorf("on tx commit hook: %w", err)
	}

	return tx.Commit(ctx)
}

// SerializationFailureCodes returns which error codes can be retried for serialization errors.
func (d driver) SerializationFailureCodes() []string {
	return []string{"40001"}
}

// SerializationFailureMaxRetries configures how many retries are done when a serialization failure occurs.
func (d driver) SerializationFailureMaxRetries() int {
	return 50
}

// TxDoneError what error is returned by the tx if it's already done.
func (d driver) TxDoneError() error {
	return pgx.ErrTxClosed
}
