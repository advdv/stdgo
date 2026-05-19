package stdent

import (
	"context"
	"sync/atomic"
)

// writeObservedKey is the unexported context key stamped by
// [WithWriteObserver] and consulted by [noteWriteObserved]. Kept
// unexported on purpose: the only way to attach an observer is
// through [WithWriteObserver], so the seam is greppable and
// reviewable — no caller can synthesise a ctx that opaquely
// carries an observer.
type writeObservedKey struct{}

// WithWriteObserver attaches a fresh write observer to ctx and
// returns both the derived ctx and the same *atomic.Bool that the
// stdent transact layer will flip on the first successful commit of
// a non-read-only transaction within ctx's lifetime.
//
// It exists because [context.Context] only flows downward: a plain
// bool stamped by [Transact1] would be invisible to a caller running
// above the handler (e.g. an HTTP middleware that wants to react to
// "a write happened during this request"). The *atomic.Bool sidecar
// bridges that gap — the middleware keeps the pointer, calls the
// next handler with the derived ctx, and inspects the bool after the
// handler returns.
//
// The observer mechanism is intentionally independent from
// [WithReadPromotion]: it does not care which transactor the write
// went through, nor whether the caller participates in ro/rw
// routing. Any successful commit of a transactor with
// [Transactor.IsReadOnly] == false trips it.
//
// Callers that never attach an observer pay only a single ctx lookup
// per commit (see [noteWriteObserved]), so non-HTTP consumers
// (Temporal activities, bootstrap, tests) are unaffected.
//
// Re-calling [WithWriteObserver] on an already-observed ctx attaches
// a fresh observer that shadows the previous one for descendants of
// the new ctx; the previously-returned pointer keeps observing only
// what is reachable through the older ctx.
func WithWriteObserver(ctx context.Context) (context.Context, *atomic.Bool) {
	obs := &atomic.Bool{}

	return context.WithValue(ctx, writeObservedKey{}, obs), obs
}

// noteWriteObserved flips the observer attached to ctx, if any.
// Called from [Transact1] right after a successful commit when the
// chosen transactor is not configured as read-only. A no-op (one
// ctx lookup, type assertion fails) when no observer is attached,
// so the cost on hot paths without an observer is negligible.
func noteWriteObserved(ctx context.Context) {
	if obs, ok := ctx.Value(writeObservedKey{}).(*atomic.Bool); ok {
		obs.Store(true)
	}
}
