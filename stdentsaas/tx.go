package stdentsaas

import (
	"context"
	"encoding/json"
	"fmt"

	entdialect "entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

// Tx wraps a Ent transaction to provide us with the ability to hook any sql
// before it's being executed. In our case we ant to fail tests when the
// to-be-executed query plan has a cost that is too high.
type Tx struct {
	entdialect.Tx
	MaxQueryPlanCosts float64
}

// Exec executes a query that does not return records. For example, in SQL, INSERT or UPDATE.
// It scans the result into the pointer v. For SQL drivers, it is dialect/sql.Result.
func (tx Tx) Exec(ctx context.Context, query string, args, v any) error {
	return tx.do(ctx, query, args, v, tx.Tx.Exec)
}

// Query executes a query that returns rows, typically a SELECT in SQL.
// It scans the result into the pointer v. For SQL drivers, it is *dialect/sql.Rows.
func (tx Tx) Query(ctx context.Context, query string, args, v any) error {
	return tx.do(ctx, query, args, v, tx.Tx.Query)
}

func (tx Tx) do(
	ctx context.Context, query string, args, val any,
	dof func(ctx context.Context, query string, args, v any) error,
) error {
	if tx.MaxQueryPlanCosts <= 0 {
		return dof(ctx, query, args, val) // just execute
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

	for _, plan := range expl {
		if plan.Plan.TotalCost > tx.MaxQueryPlanCosts {
			return fmt.Errorf("query plan cost exceeds maximum %v > %v", plan.Plan.TotalCost, tx.MaxQueryPlanCosts)
		}
	}

	// finally, run the actual query
	return dof(ctx, query, args, val)
}
