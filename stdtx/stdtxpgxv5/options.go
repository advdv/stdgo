package stdtxpgxv5

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap/zapcore"
)

// TxBeginSQLFunc is the signature for determining custom sql run at the start of the transaction.
type TxBeginSQLFunc = func(context.Context, *strings.Builder, pgx.Tx) (*strings.Builder, error)

type options struct {
	txIsoLevel          pgx.TxIsoLevel
	txAccessMode        pgx.TxAccessMode
	txBeginSQL          TxBeginSQLFunc
	discourageSeqScans  bool
	maxQueryPlanCosts   float64
	txExecQueryLogLevel zapcore.Level
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
		o.txIsoLevel = v
	}
}

// ExecQueryLoggingLevel configures the level at which transaction's exec and query sql are logged.
func ExecQueryLoggingLevel(v zapcore.Level) Option {
	return func(o *options) {
		o.txExecQueryLogLevel = v
	}
}

// TestForMaxQueryPlanCosts will enable EXPLAIN on every query that is executed with
// the driver and fail when the cost of the resulting query is above the maximum. Together
// with the enable_seqscan=OFF it can help test of infefficient queries do to missing
// indexes.
func TestForMaxQueryPlanCosts(maxCost float64) Option {
	return func(o *options) {
		o.maxQueryPlanCosts = maxCost
	}
}
