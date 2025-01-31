package stdent

import (
	"context"
	"fmt"
)

type ctxKey string

// ContextWithAttempts stores which execution attempt it is.
func ContextWithAttempts(ctx context.Context, v int) context.Context {
	return context.WithValue(ctx, ctxKey("attempts"), v)
}

// AttemptFromContext returns which execution attempt it is. Panics if this information is not present.
func AttemptFromContext(ctx context.Context) int {
	v, ok := attemptsFromContext(ctx)
	if !ok {
		panic("stdenttx: no execution attempts in context")
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

// ContextWithTx returns a context with the Tx in it.
func ContextWithTx[T Tx](ctx context.Context, tx T) context.Context {
	ctx = context.WithValue(ctx, ctxKey("tx"), tx)
	return ctx
}

// TxFromContext will get a transaction of type T from the context or panic.
func TxFromContext[T Tx](ctx context.Context) T {
	tx, ok := txFromContext[T](ctx)
	if !ok {
		panic(fmt.Sprintf("stdenttx: no tx in context or not of type: %T", tx))
	}

	return tx
}

func txFromContext[T Tx](ctx context.Context) (vv T, ok bool) {
	v := ctx.Value(ctxKey("tx"))
	if v == nil {
		return vv, false
	}

	vt, ok := v.(T)
	if !ok {
		return vv, false
	}

	return vt, true
}
