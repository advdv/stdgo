package stdcrpcwritefence

import (
	"context"

	"connectrpc.com/connect"
)

// Interceptor returns a server-side Connect unary interceptor that
// flips this package's fence-intent flag on the request ctx whenever
// the inbound procedure's idempotency level is anything other than
// [connect.IdempotencyNoSideEffects] AND the handler returned nil.
//
// The decision is read straight off [connect.Spec.IdempotencyLevel],
// which is in turn driven by the procedure's `idempotency_level`
// proto annotation — no codegen, no bespoke annotation, and no
// handler-body changes required.
//
// The interceptor is a sibling of [Middleware]: both belong to this
// package because they share the unexported fence-intent flag. The
// middleware installs the flag; the interceptor flips it. If the
// middleware did not run for this request (e.g. a worker-internal
// call), the flip becomes a no-op — matching the fail-quiet
// behaviour every ctx-stamped seam in this repo follows.
//
// Wiring: register on the inbound (server-side) Connect interceptor
// chain alongside the rest of the stdcrpc* interceptors. The
// interceptor is a no-op on the client side — fence decisions are
// server-side only.
//
// Default behaviour for procedures that don't declare an idempotency
// level (i.e. [connect.IdempotencyUnknown], the proto-default zero
// value) is to fence on success. This is fail-safe (the cost is at
// most one over-pinned read window per call) and serves as gentle
// pressure on API authors to mark pure reads as NO_SIDE_EFFECTS so
// they also pick up Connect's GET routing.
func Interceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			resp, err := next(ctx, req)
			if err != nil || req.Spec().IsClient {
				return resp, err
			}

			if req.Spec().IdempotencyLevel == connect.IdempotencyNoSideEffects {
				return resp, nil
			}

			MarkFenceIntent(ctx)

			return resp, nil
		}
	}
}
