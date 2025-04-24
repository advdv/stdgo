package stdtx

import "context"

type ctxKey string

// contextWithAttempts stores which execution attempt it is.
func contextWithAttempts(ctx context.Context, v int) context.Context {
	return context.WithValue(ctx, ctxKey("attempts"), v)
}

// AttemptFromContext returns which execution attempt it is. Panics if this information is not present.
func AttemptFromContext(ctx context.Context) int {
	v, ok := attemptsFromContext(ctx)
	if !ok {
		panic("stdtx: no execution attempts in context")
	}

	return v
}

func attemptsFromContext(ctx context.Context) (vv int, ok bool) {
	v := ctx.Value(ctxKey("attempts"))
	if v == nil {
		return vv, false
	}

	vt, ok := v.(int)
	if !ok {
		return vv, false
	}

	return vt, true
}

// WithNoTestForMaxQueryPlanCosts allow disabling the plan cost check.
func WithNoTestForMaxQueryPlanCosts(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKey("no_test_for_max_query_plan_costs"), true)
}

// NoTestForMaxQueryPlanCosts returns whether the cost check is disabled.
func NoTestForMaxQueryPlanCosts(ctx context.Context) bool {
	v, ok := ctx.Value(ctxKey("no_test_for_max_query_plan_costs")).(bool)
	if !ok {
		return false
	}

	return v
}
