package stdent

import "context"

// readPromotionKey is the context key stamped by [WithReadPromotion]
// and consulted by [TransactR] / [TransactR0] to decide whether a
// read-bound call opens its transaction against the read-only or the
// read-write transactor.
//
// Kept unexported on purpose: the only way to flip the bit is through
// [WithReadPromotion], so the decision is greppable and reviewable —
// no caller can synthesise a ctx that opaquely promotes reads.
type readPromotionKey struct{}

// WithReadPromotion stamps ctx so that a subsequent [TransactR] or
// [TransactR0] call will open its transaction against the read-write
// transactor instead of the read-only one. It is the single seam
// where read-your-writes routing policy plugs in — e.g. an HTTP
// middleware that observes a signed "write fence" cookie set after a
// recent write, or any caller that knows it needs the writer for a
// specific invocation.
//
// The decision is invisible to the developer-implemented handler
// body: it still receives a single T and has no way to widen its own
// posture (downstream concerns like per-tenant Postgres roles and
// transaction mode continue to enforce what the caller may do).
func WithReadPromotion(ctx context.Context) context.Context {
	return context.WithValue(ctx, readPromotionKey{}, true)
}

// HasReadPromotion reports whether ctx was stamped by
// [WithReadPromotion]. Consumers normally don't call this directly —
// [TransactR] / [TransactR0] consult it on every invocation — but it
// is exported for tests and for diagnostics (e.g. attaching the
// decision to a span).
func HasReadPromotion(ctx context.Context) bool {
	v, _ := ctx.Value(readPromotionKey{}).(bool)

	return v
}

// pickReadTransactor returns rw when [HasReadPromotion] is true,
// otherwise ro. Shared by [TransactR] and [TransactR0] so the
// promotion rule has exactly one implementation.
func pickReadTransactor[T Tx](
	ctx context.Context, ro, rw *Transactor[T],
) *Transactor[T] {
	if HasReadPromotion(ctx) {
		return rw
	}

	return ro
}

// TransactR is [Transact1] for read-bound callers that may, per
// invocation, be transparently promoted to the writer to give the
// caller read-your-writes consistency. The choice is driven by
// [HasReadPromotion] on ctx and forwards to [Transact1] with the
// chosen transactor, so every guarantee Transact1 provides
// (retry-on-serialization-failure, nested-tx reuse, panic rollback)
// applies unchanged.
//
// Callers that bind their handler to ONE boundary at proto-gen /
// compile time can still pass both `ro` and `rw` here without
// leaking the routing policy into generated code or into the
// handler body.
//
// The inp / fn shape (taking a typed input + returning a typed
// output) mirrors common request/response transactional helpers.
func TransactR[T Tx, I any, O any, IP interface{ *I }, OP interface{ *O }](
	ctx context.Context,
	ro, rw *Transactor[T],
	inp IP,
	fn func(ctx context.Context, tx T, inp IP) (OP, error),
) (OP, error) {
	return Transact1(ctx, pickReadTransactor(ctx, ro, rw), func(ctx context.Context, tx T) (OP, error) {
		return fn(ctx, tx, inp)
	})
}

// TransactR0 is [TransactR] for the common case where the inner
// function neither needs a typed input nor returns a typed output;
// mirrors [Transact0] exactly, including all of its safety
// guarantees, and chooses the transactor the same way TransactR does.
func TransactR0[T Tx](
	ctx context.Context,
	ro, rw *Transactor[T],
	fn func(ctx context.Context, tx T) error,
) error {
	return Transact0(ctx, pickReadTransactor(ctx, ro, rw), fn)
}
