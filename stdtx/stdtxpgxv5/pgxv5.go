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

// TxBeginSQLFunc is the signature for determining custom sql run at the start of the transaction.
type TxBeginSQLFunc = func(context.Context, *strings.Builder, pgx.Tx) (*strings.Builder, error)

type options struct {
	TxIsoLevel         pgx.TxIsoLevel
	txAccessMode       pgx.TxAccessMode
	txBeginSQL         TxBeginSQLFunc // @TODO add option
	discourageSeqScans bool           // @TODO add option
}

// Option configures the pgxv5 driver.
type Option func(o *options)

// AccessMode configure the access mode for transactions created for the driver.
func AccessMode(v pgx.TxAccessMode) Option {
	return func(o *options) {
		o.txAccessMode = v
	}
}

// DiscourageSeqScan configure the access mode for transactions created for the driver.
func DiscourageSeqScan(v bool) Option {
	return func(o *options) {
		o.discourageSeqScans = v
	}
}

// BeginWithSQL configure the access mode for transactions created for the driver.
func BeginWithSQL(v TxBeginSQLFunc) Option {
	return func(o *options) {
		o.txBeginSQL = v
	}
}

// IsolationMode configure the access mode for transactions created for the driver.
func IsolationMode(v pgx.TxIsoLevel) Option {
	return func(o *options) {
		o.TxIsoLevel = v
	}
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

	if _, err := tx.Exec(ctx, sql.String()); err != nil {
		return fmt.Errorf("execute tx begin sql: %w", err)
	}

	return nil
}

// BeginTx implements the starting of a transaction.
func (d driver) BeginTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := d.db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   d.opts.TxIsoLevel,
		AccessMode: d.opts.txAccessMode,
	})
	if err != nil {
		return nil, err // return transparently.
	}

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
