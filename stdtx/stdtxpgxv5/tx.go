package stdtxpgxv5

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdtx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// wtx wraps a pgx.Tx to allow unilateral functionality for each sql being executed.
type wtx struct {
	pgx.Tx

	maxQueryPlanCosts float64
	execQueryLogLevel zapcore.Level
}

// Exec calls the underlying Exec while logging and asserting query costs.
func (tx wtx) Exec(ctx context.Context, sql string, args ...any) (commandTag pgconn.CommandTag, err error) {
	if err := tx.logAndAssertQueryPlanCosts(ctx, "exec", sql, args...); err != nil {
		return commandTag, err
	}

	return tx.Tx.Exec(ctx, sql, args...)
}

// Exec calls the underlying Query while logging and asserting query costs.
func (tx wtx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := tx.logAndAssertQueryPlanCosts(ctx, "query", sql, args...); err != nil {
		return nil, err
	}

	return tx.Tx.Query(ctx, sql, args...)
}

type errRow struct{ err error }

func (er errRow) Scan(...any) error {
	return er.err
}

// Exec calls the underlying QueryRow while logging and asserting query costs.
func (tx wtx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := tx.logAndAssertQueryPlanCosts(ctx, "query row", sql, args...); err != nil {
		return errRow{err}
	}

	return tx.Tx.QueryRow(ctx, sql, args...)
}

// logAndAssertQueryPlanCosts does the heavy lifting of asserting the query plan costs.
func (tx wtx) logAndAssertQueryPlanCosts(ctx context.Context, logMsg, sql string, args ...any) error {
	stdctx.Log(ctx).Log(tx.execQueryLogLevel, logMsg, zap.String("sql", sql), zap.Any("args", args))
	if tx.maxQueryPlanCosts <= 0 || stdtx.NoTestForMaxQueryPlanCosts(ctx) {
		return nil // do nothing
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

	expSQL := `EXPLAIN (FORMAT JSON) ` + sql
	var explJSON string
	if err := tx.Tx.QueryRow(ctx, expSQL, args...).Scan(&explJSON); err != nil {
		return fmt.Errorf("query row for EXPLAIN, sql: '%s', error: %w", expSQL, err)
	}

	var expl explanation
	if err := json.Unmarshal([]byte(explJSON), &expl); err != nil {
		return fmt.Errorf("unmarshal query plan json: %w", err)
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

	if cumCostOfAllPlans > tx.maxQueryPlanCosts {
		return fmt.Errorf("query plan cost exceeds maximum %v > %v, query: %s, plan: %s",
			cumCostOfAllPlans, tx.maxQueryPlanCosts, sql, explJSON)
	}

	return nil
}
