package stdent

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	entdialect "entgo.io/ent/dialect"
	"go.uber.org/zap/zapcore"
)

type DriverOption func(*Driver)

// TestForMaxQueryPlanCosts will enable EXPLAIN on every query that is executed with
// the driver and fail when the cost of the resulting query is above the maximum. Together
// with the enable_seqscan=OFF it can help test of infefficient queries do to missing
// indexes.
func TestForMaxQueryPlanCosts(maxCost float64) DriverOption {
	return func(d *Driver) {
		d.maxQueryPlanCosts = maxCost
	}
}

// DiscourageSequentialScans will dis-incentivize the query planner to use sequential
// scans for all transactions. This is mainly useful with the TestForMaxQueryPlanCost
// option to assert that queries under testing are missing an index.
func DiscourageSequentialScans() DriverOption {
	return func(d *Driver) {
		d.discourageSeqScans = true
	}
}

// TxExecQueryLoggingLevel configures the level at which transaction's exec and query sql logs are send to the logger.
func TxExecQueryLoggingLevel(v zapcore.Level) DriverOption {
	return func(d *Driver) {
		d.txExecQueryLogLevel = v
	}
}

// BeginHook may be called right when the transaction has been setup. This allows injecting custom settings into
// transaction. For example to facilitate role switching and Row-level security. This can either be performed by
// extending the sql statement that is already being performed (perferred for simple operations). Or using the
// transaction concretely.
func BeginHook(v func(
	ctx context.Context, sql *strings.Builder, tx entdialect.ExecQuerier) (*strings.Builder, error),
) DriverOption {
	return func(d *Driver) {
		d.beginHook = v
	}
}

// Driver is an opionated Ent driver that wraps a base driver but only allows interactions
// with the database to be done through a transaction with specific isolation
// properties and hooking any sql being executed.
type Driver struct {
	entdialect.Driver

	timeoutExtension    time.Duration
	maxQueryPlanCosts   float64
	discourageSeqScans  bool
	txExecQueryLogLevel zapcore.Level
	beginHook           func(context.Context, *strings.Builder, entdialect.ExecQuerier) (*strings.Builder, error)
}

// NewDriver inits the driver.
func NewDriver(
	base entdialect.Driver,
	opts ...DriverOption,
) *Driver {
	drv := &Driver{Driver: base}
	BeginHook(func(_ context.Context, b *strings.Builder, _ entdialect.ExecQuerier) (*strings.Builder, error) {
		return b, nil
	})(drv)

	for _, opt := range opts {
		opt(drv)
	}

	// if a transaction is created in the scope of a context.Context with a deadline
	// the postgres's transaction timeout is set accordingly. But with some extended
	// window so it doesn't terminate from the server while the request is shutting down.
	if drv.timeoutExtension == 0 {
		drv.timeoutExtension = time.Second * 20
	}

	return drv
}

// Exec executes a query that does not return records. For example, in SQL, INSERT or UPDATE.
// It scans the result into the pointer v. For SQL drivers, it is dialect/sql.Result.
func (d Driver) Exec(_ context.Context, _ string, _, _ any) error {
	return fmt.Errorf("Driver.Exec is not supported: create a transaction instead")
}

// Query executes a query that returns rows, typically a SELECT in SQL.
// It scans the result into the pointer v. For SQL drivers, it is *dialect/sql.Rows.
func (d Driver) Query(_ context.Context, _ string, _, _ any) error {
	return fmt.Errorf("Driver.Query is not supported: create a transaction instead")
}

// Tx will begin a transaction with linearizable isolation level.
func (d Driver) Tx(ctx context.Context) (entdialect.Tx, error) {
	return d.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
}

// BeginTx calls the base driver's method if it's supported and calls our hook.
func (d Driver) BeginTx(ctx context.Context, opts *sql.TxOptions) (entdialect.Tx, error) {
	drv, ok := d.Driver.(interface {
		BeginTx(ctx context.Context, opts *sql.TxOptions) (entdialect.Tx, error)
	})
	if !ok {
		return nil, fmt.Errorf("Driver.BeginTx is not supported")
	}

	if opts.Isolation != sql.LevelSerializable {
		return nil, fmt.Errorf("only serializable (most strict) isolation level is allowed")
	}

	tx, err := drv.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}

	if err := d.setupTx(ctx, tx); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("failed to setup tx, rolled back: %w", err)
	}

	return WTx{tx, d.maxQueryPlanCosts, d.txExecQueryLogLevel}, nil
}

// setupTx preforms shared transaction setup.
func (d Driver) setupTx(ctx context.Context, tx entdialect.Tx) (err error) {
	sql := &strings.Builder{}

	// call any custom hook for beginning the transaction.
	sql, err = d.beginHook(ctx, sql, tx)
	if err != nil {
		return fmt.Errorf("setup hook: %w", err)
	}

	// if the context has a deadline we limit the transaction to that timeout.
	dl, ok := ctx.Deadline()
	if ok {
		sql.WriteString(fmt.Sprintf(`SET LOCAL transaction_timeout = %d;`,
			(time.Until(dl) + d.timeoutExtension).Milliseconds()))
	}

	// if we want to discourage sequential scans
	if d.discourageSeqScans {
		sql.WriteString(`SET LOCAL enable_seqscan = OFF`)
	}

	if err := tx.Exec(ctx, sql.String(), []any{}, nil); err != nil {
		return fmt.Errorf("failed to set authenticated setting: %w", err)
	}

	return nil
}

var (
	// make sure our driver implements the ent driver.
	_ entdialect.Driver = &Driver{}

	// this interface may also be asserted for if users want to change transaction options.
	_ interface {
		BeginTx(ctx context.Context, opts *sql.TxOptions) (entdialect.Tx, error)
	} = &Driver{}
)
