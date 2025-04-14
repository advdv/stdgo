package stdent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	entdialect "entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/advdv/stdgo/stdctx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// WTx wraps a Ent transaction to provide us with the ability to hook any sql
// before it's being executed. In our case we ant to fail tests when the
// to-be-executed query plan has a cost that is too high.
type WTx struct {
	entdialect.Tx
	MaxQueryPlanCosts float64
	execQueryLogLevel zapcore.Level
}

// Exec executes a query that does not return records. For example, in SQL, INSERT or UPDATE.
// It scans the result into the pointer v. For SQL drivers, it is dialect/sql.Result.
func (tx WTx) Exec(ctx context.Context, query string, args, v any) error {
	stdctx.Log(ctx).Log(tx.execQueryLogLevel, "exec", zap.String("sql", query), zap.Any("args", args))

	return tx.do(ctx, query, args, v, tx.Tx.Exec)
}

// Query executes a query that returns rows, typically a SELECT in SQL.
// It scans the result into the pointer v. For SQL drivers, it is *dialect/sql.Rows.
func (tx WTx) Query(ctx context.Context, query string, args, v any) error {
	stdctx.Log(ctx).Log(tx.execQueryLogLevel, "query", zap.String("sql", query), zap.Any("args", args))

	return tx.do(ctx, query, args, v, tx.Tx.Query)
}

// toSQLTx casts to standard sql.Tx.
func (tx WTx) toSQLTx() (*sql.Tx, error) {
	entTx, ok := tx.Tx.(*entsql.Tx)
	if !ok {
		return nil, fmt.Errorf("WTx does not implement *entgo.io/ent/dialect/sql.Tx")
	}

	sqlTx, ok := entTx.Conn.ExecQuerier.(*sql.Tx)
	if !ok {
		return nil, fmt.Errorf("WTx does not contain *sql.Tx")
	}

	return sqlTx, nil
}

// QueryContext implements a way to execute raw sql.
func (tx WTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	sqlTx, err := tx.toSQLTx()
	if err != nil {
		return nil, err
	}

	return sqlTx.QueryContext(ctx, query, args...)
}

// ExecContext implements a way to execute raw sql.
func (tx WTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	sqlTx, err := tx.toSQLTx()
	if err != nil {
		return nil, err
	}

	return sqlTx.ExecContext(ctx, query, args...)
}

func (tx WTx) do(
	ctx context.Context, query string, args, val any,
	dof func(ctx context.Context, query string, args, v any) error,
) error {
	if tx.MaxQueryPlanCosts <= 0 || NoTestForMaxQueryPlanCosts(ctx) {
		return dof(ctx, query, args, val) // just execute
	}

	// This check enforces during tests that any use of our custom driver will allow access to the
	// raw sql.Tx if we need it down the line. We want to force this in our abstraction early.
	if _, err := tx.toSQLTx(); err != nil {
		return fmt.Errorf("failed to cast WTx into sql.Tx: %w", err)
	}

	// plan describes a query plan with the cost, see:
	// https://github.com/postgres/postgres/blob/master/src/backend/commands/explain.c#L1297
	type plan struct {
		NodeType     string  `json:"Node Type"`
		Operation    string  `json:"Operation"`
		RelationName string  `json:"Relation Name"`
		IndexName    string  `json:"Index Name"`
		TotalCost    float64 `json:"Total Cost"`
	}

	// explanation of the query.
	type explanation []struct {
		Plan plan `json:"Plan"`
	}

	// run EXPLAIN first to ask the query planner for a cost estimation. Prior art:
	// https://github.com/crewlinker/atsback/blob/main/model/model_pgdb.go
	var rows entsql.Rows
	if err := tx.Tx.Query(ctx, `EXPLAIN (FORMAT JSON) `+query, args, &rows); err != nil {
		return fmt.Errorf("failed to query explain to determine plan cost: %w, query: %s", err, query)
	}

	explJSON, err := entsql.ScanString(rows)
	if err != nil {
		return fmt.Errorf("failed to scan EXPLAIN json: %w", err)
	}

	var expl explanation
	if err := json.Unmarshal([]byte(explJSON), &expl); err != nil {
		return fmt.Errorf("failed to unmarshal query plan json: %w", err)
	}

	var cumCostOfAllPlans float64
	for i, plan := range expl {
		stdctx.Log(ctx).Log(tx.execQueryLogLevel, "explained query plan",
			zap.Int("plan_idx", i),
			zap.String("plan_node_type", plan.Plan.NodeType),
			zap.String("plan_operation", plan.Plan.NodeType),
			zap.Float64("plan_total_cost", plan.Plan.TotalCost))

		cumCostOfAllPlans += plan.Plan.TotalCost
	}

	if cumCostOfAllPlans > tx.MaxQueryPlanCosts {
		return fmt.Errorf("query plan cost exceeds maximum %v > %v, query: %s, plan: %s",
			cumCostOfAllPlans, tx.MaxQueryPlanCosts, query, explJSON)
	}

	// finally, run the actual query
	return dof(ctx, query, args, val)
}

// the transaction must also implement this interface to support the raw sql feature flag.
var _ interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
} = &WTx{}
