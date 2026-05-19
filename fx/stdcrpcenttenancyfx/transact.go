package stdcrpcenttenancyfx

import (
	"context"

	"github.com/advdv/stdgo/stdent"
)

// Transact is the supported way for handler / activity code to open
// an ent transaction. It funnels through [stdent.Transact1], which
// fires this package's [Authorize.BeginHook] before the first query —
// so every transaction observably runs as the per-tenant Postgres
// role driven by ctx (anonymous / webuser / sysuser) and carries the
// `access.tenant_id` GUC when authenticated.
//
// The role decision is made at the wire boundary by the
// stdcrpcenttenancyfx Connect interceptor, which reads the procedure's
// proto annotation and stamps a [DatabaseRole] on ctx via
// [WithDatabaseRole]. Trusted internal callers without an inbound RPC
// (Temporal activities, system bootstrap, test seed helpers) call
// [WithDatabaseRole] directly with [DatabaseRoleSysuser] (gated by a
// project-wide forbidigo lint rule that requires a justification
// comment). Either way, by the time Transact runs, ctx MUST already
// carry a role — it does not infer one from claim shape. A ctx with
// no role is rejected with [ErrMissingDatabaseRole] before any query
// runs.
//
// Splitting handlers into an outer method (calls Transact) and an
// inner method (receives T) is what lets this package own the proof
// that the role switch happened: the package's three-posture
// integration test exercises the same Transact helper production
// calls and observes `current_user` / `current_setting` inside the
// resulting tx. No black-box wire introspection (e.g. echoing
// `current_user` over an RPC) is needed.
//
// Generic over T = stdent.Tx so the package stays free of any
// consumer's generated `*ent.Tx` type. Handlers instantiate it as
// `Transact[*ent.Tx, ReqT, RespT]`.
//
// The inp / fn shape (taking a typed input + returning a typed output)
// mirrors common request/response transactional helpers.
//
// Going around Transact (e.g. calling [stdent.Transact1] directly with
// the same transactor) is detected at runtime: see [managedTxKey] and
// the check at the top of [Authorize.BeginHook]. Such a tx will be
// rolled back at begin time with an actionable error pointing the
// developer at this helper.
func Transact[T stdent.Tx, I any, O any, IP interface{ *I }, OP interface{ *O }](
	ctx context.Context,
	txr *stdent.Transactor[T],
	inp IP,
	fn func(ctx context.Context, tx T, inp IP) (OP, error),
) (OP, error) {
	ctx, err := enterManagedTx(ctx)
	if err != nil {
		var zero OP

		return zero, err
	}

	return stdent.Transact1(ctx, txr, func(ctx context.Context, tx T) (OP, error) {
		return fn(ctx, tx, inp)
	})
}

// Transact0 is [Transact] for the common case where the inner function
// neither needs a typed input nor returns a typed output (probes,
// fire-and-forget mutations). Mirrors [stdent.Transact0] but funnels
// through this package so the BeginHook is guaranteed to run AND so
// the runtime "managed tx" check (see [Transact]) accepts the begin.
func Transact0[T stdent.Tx](
	ctx context.Context,
	txr *stdent.Transactor[T],
	fn func(ctx context.Context, tx T) error,
) error {
	ctx, err := enterManagedTx(ctx)
	if err != nil {
		return err
	}

	return stdent.Transact0(ctx, txr, fn)
}

// TransactR is the tenant-aware version of [stdent.TransactR]: it
// runs the same role + managed-tx gate as [Transact] and then
// delegates the actual pool-routing-and-begin to [stdent.TransactR],
// so the read-promotion rule has exactly one implementation (in
// stdent) and the tenancy chokepoint has exactly one implementation
// (in [enterManagedTx]).
//
// Handlers that want read-your-writes for a specific invocation
// stamp ctx with [stdent.WithReadPromotion] (typically from a
// middleware or interceptor) — the rest of the handler body is
// identical to a normal [Transact] call.
func TransactR[T stdent.Tx, I any, O any, IP interface{ *I }, OP interface{ *O }](
	ctx context.Context,
	ro, rw *stdent.Transactor[T],
	inp IP,
	fn func(ctx context.Context, tx T, inp IP) (OP, error),
) (OP, error) {
	ctx, err := enterManagedTx(ctx)
	if err != nil {
		var zero OP

		return zero, err
	}

	return stdent.TransactR(ctx, ro, rw, inp, fn)
}

// TransactR0 is [TransactR] for the common case where the inner
// function neither needs a typed input nor returns a typed output;
// mirrors [stdent.TransactR0] and inherits the same role + managed-tx
// gate as [Transact0].
func TransactR0[T stdent.Tx](
	ctx context.Context,
	ro, rw *stdent.Transactor[T],
	fn func(ctx context.Context, tx T) error,
) error {
	ctx, err := enterManagedTx(ctx)
	if err != nil {
		return err
	}

	return stdent.TransactR0(ctx, ro, rw, fn)
}

// enterManagedTx is the single gate every public Transact* entry
// point in this package goes through before calling into stdent:
//
//  1. It rejects a ctx that does not carry a [DatabaseRole] with
//     [ErrMissingDatabaseRole] — proof that either the
//     stdcrpcenttenancyfx Connect interceptor stamped the role
//     (production) or a trusted internal caller did so explicitly
//     via [WithDatabaseRole] (Temporal activities, bootstrap, tests).
//  2. It stamps the package-internal "managed tx" marker so
//     [Authorize.BeginHook] accepts the next tx open against this
//     ctx — see [managedTxKey] for why bypassing this chokepoint is
//     a programmer error.
//
// Centralising the gate guarantees that adding a new Transact*
// variant (TransactR, a future TransactRW-only helper, …) cannot
// accidentally skip either step.
func enterManagedTx(ctx context.Context) (context.Context, error) {
	if _, ok := DatabaseRoleFromContext(ctx); !ok {
		return ctx, ErrMissingDatabaseRole
	}

	return contextWithManagedTx(ctx), nil
}

// managedTxKey is the context key [Transact] / [Transact0] /
// [TransactR] / [TransactR0] stamp on ctx (via [enterManagedTx])
// before calling into stdent. The package's [Authorize.BeginHook]
// requires this marker on ctx as a runtime backstop against
// developers calling [stdent.Transact1] / [stdent.Transact0]
// directly with one of the rw / ro transactors. Calling stdent
// directly would still fire the BeginHook (it's wired to the driver,
// not the call site), so today the role switch would still happen —
// but bypassing the package's Transact* helpers:
//
//   - bypasses the audit chokepoint where future cross-cutting
//     concerns (request-scoped retries, error normalisation, API-key
//     revocation checks) will live;
//   - hides the role-stamping decision from code review (every direct
//     stdent call is a place a future maintainer might forget to
//     [WithDatabaseRole] the ctx when needed);
//   - leaves any hand-built transactor that doesn't wire the BeginHook
//     running as the privilege-less connection role with no grants,
//     which is exactly the failure mode this marker turns from "silent
//     permission_denied somewhere downstream" into "loud begin-tx error
//     pointing at this file".
//
// The marker is unexported and has no public stamper — the only way
// to obtain a ctx that satisfies the BeginHook is to call Transact /
// Transact0 / TransactR / TransactR0 from this package. Reflection /
// unsafe is not a path a reasonable code-review process would let
// through.
type managedTxKey struct{}

// contextWithManagedTx stamps ctx so the BeginHook accepts the next
// tx open against this ctx. Internal — not exported.
func contextWithManagedTx(ctx context.Context) context.Context {
	return context.WithValue(ctx, managedTxKey{}, true)
}

// hasManagedTx reports whether ctx was stamped by [enterManagedTx].
// Read by [Authorize.BeginHook] as a runtime gate.
func hasManagedTx(ctx context.Context) bool {
	v, _ := ctx.Value(managedTxKey{}).(bool)

	return v
}
