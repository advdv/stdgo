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
	if _, ok := DatabaseRoleFromContext(ctx); !ok {
		var zero OP

		return zero, ErrMissingDatabaseRole
	}

	ctx = contextWithManagedTx(ctx)

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
	if _, ok := DatabaseRoleFromContext(ctx); !ok {
		return ErrMissingDatabaseRole
	}

	ctx = contextWithManagedTx(ctx)

	return stdent.Transact0(ctx, txr, fn)
}

// managedTxKey is the context key [Transact] / [Transact0] stamp on
// ctx before calling into stdent. The package's [Authorize.BeginHook]
// requires this marker on ctx as a runtime backstop against
// developers calling [stdent.Transact1] / [stdent.Transact0]
// directly with one of the rw / ro transactors. Calling stdent
// directly would still fire the BeginHook (it's wired to the driver,
// not the call site), so today the role switch would still happen —
// but bypassing Transact:
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
// Transact0 from this package. Reflection / unsafe is not a path a
// reasonable code-review process would let through.
type managedTxKey struct{}

// contextWithManagedTx stamps ctx so the BeginHook accepts the next
// tx open against this ctx. Internal — not exported.
func contextWithManagedTx(ctx context.Context) context.Context {
	return context.WithValue(ctx, managedTxKey{}, true)
}

// hasManagedTx reports whether ctx was stamped by [Transact] /
// [Transact0]. Read by [Authorize.BeginHook] as a runtime gate.
func hasManagedTx(ctx context.Context) bool {
	v, _ := ctx.Value(managedTxKey{}).(bool)

	return v
}
