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
