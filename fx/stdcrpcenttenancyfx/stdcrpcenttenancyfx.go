// Package stdcrpcenttenancyfx maps a per-RPC `db_role` proto annotation
// to a per-transaction Postgres role plus a tenancy GUC, via the stdent
// [BeginHook] extension point. The role decision is made at the wire
// boundary by a Connect interceptor — NOT by inspecting JWT claim
// shape or context markers at transaction-begin time. Composition
// roots supply the per-tenant role names through
// `STDCRPCENTTENANCY_*_DATABASE_ROLE` env vars (env prefix derived
// from the module name `stdcrpcenttenancy`).
//
// The hook is contributed to the fx graph as an [stdent.BeginHookFunc],
// which [stdenttxfx.New] picks up via its optional `TxBeginSQL` parameter
// and installs on every Transactor it creates. There is no per-package
// opt-in: any package using the rw / ro transactors automatically runs
// through the role switch + GUC injection on transaction begin.
//
// Three switch positions, driven by the [DatabaseRole] stamped on ctx
// by the stdcrpcenttenancyfx interceptor (or, for trusted internal
// callers without an inbound RPC, by [WithDatabaseRole] gated behind a
// project-wide forbidigo lint rule):
//
//  1. [DatabaseRoleSysuser] →
//     `SET LOCAL ROLE <SystemDatabaseRole>` (BYPASSRLS). Selected by
//     the proto annotation `DB_ROLE_SYSUSER` for RPCs that legitimately
//     cross tenant boundaries (admin, dev/test reset, system bootstrap).
//  2. [DatabaseRoleAnonymous] →
//     `SET LOCAL ROLE <AnonymousDatabaseRole>`. Selected by
//     `DB_ROLE_ANONYMOUS`. Cannot read or write tenanted tables,
//     regardless of GUC value.
//  3. [DatabaseRoleWebuser] →
//     `SELECT set_config('<TenantIDGUC>', tenant, true);
//     SET LOCAL ROLE <WebUserDatabaseRole>`. Selected by
//     `DB_ROLE_WEBUSER`. The GUC value is read from the required
//     [TenantIDResolver] (empty-string sentinel when no tenant is
//     in scope for the request — e.g. an M2M token without an org
//     claim). The package is identity-agnostic — binding the
//     resolver to a JWT claim, an API-key lookup, or anything else
//     is the composition root's job, not stdcrpcenttenancyfx's.
//
// `SET LOCAL` is transaction-scoped and reverts at COMMIT/ROLLBACK, so
// the role switch and the GUC are scoped to exactly the transaction
// the hook fires on; the underlying connection (typically a
// privilege-less `_authenticator` LOGIN role) retains its original
// identity for any subsequent transaction.
//
// The "DatabaseRole" name is deliberate: this package's "role" is the
// Postgres role posture a transaction runs in, and a consumer's
// codebase usually also carries an unrelated user-role concept (e.g.
// from JWT claims). Calling this just `Role` would conflate the two
// at the import site.
package stdcrpcenttenancyfx

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	entdialect "entgo.io/ent/dialect"
	"github.com/advdv/stdgo/stdent"
	"github.com/advdv/stdgo/stdfx"
	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config is the env-driven configuration for stdcrpcenttenancyfx.
type Config struct {
	// AnonymousDatabaseRole is the Postgres role assumed when the request's
	// proto annotation resolves to [DatabaseRoleAnonymous]. Must NOT have
	// BYPASSRLS.
	AnonymousDatabaseRole string `env:"ANONYMOUS_DATABASE_ROLE,required"`
	// SystemDatabaseRole is the Postgres role assumed when the request's
	// proto annotation resolves to [DatabaseRoleSysuser] (or when ctx is
	// stamped with [DatabaseRoleSysuser] via [WithDatabaseRole]). Must
	// have BYPASSRLS — it is the only role permitted to read/write
	// across tenants and is reserved for trusted code paths.
	SystemDatabaseRole string `env:"SYSTEM_DATABASE_ROLE,required"`
	// WebUserDatabaseRole is the Postgres role assumed when the request's
	// proto annotation resolves to [DatabaseRoleWebuser]. Must NOT have
	// BYPASSRLS — RLS policies filter rows visible to it based on the
	// [Config.TenantIDGUC] value injected on transaction begin.
	WebUserDatabaseRole string `env:"WEBUSER_DATABASE_ROLE,required"`
	// TenantIDGUC is the Postgres custom GUC name written via `set_config`
	// on transaction begin to carry the caller's opaque tenant id. RLS
	// policies read it via `current_setting(TenantIDGUC, true)`. The
	// default `access.tenant_id` is intentionally generic — the package
	// is data-model-agnostic, so the GUC name does not assume any
	// particular tenant shape (organization, workspace, account, …).
	// Override only if a different name is needed for compatibility.
	TenantIDGUC string `env:"TENANT_ID_GUC" envDefault:"access.tenant_id"`
}

// DatabaseRole is the Postgres role posture an RPC method runs in.
// Selected at the wire boundary by the stdcrpcenttenancyfx interceptor
// from the per-method proto annotation, and read at transaction-begin
// by [Authorize.BeginHook].
//
// The "Database" qualifier is load-bearing: a consumer's codebase
// usually also carries a user-role concept (e.g. from JWT claims); an
// unqualified "Role" type at the import site would conflate the two.
type DatabaseRole int

const (
	// DatabaseRoleUnspecified is the zero value. A ctx that carries this
	// value (or no role at all) is rejected by [Transact] / [Transact0].
	DatabaseRoleUnspecified DatabaseRole = iota
	// DatabaseRoleAnonymous selects the anonymous Postgres role (no
	// BYPASSRLS, no GUC).
	DatabaseRoleAnonymous
	// DatabaseRoleWebuser selects the per-tenant webuser Postgres role
	// and emits a `set_config` for the [Config.TenantIDGUC] populated
	// from the configured [TenantIDResolver].
	DatabaseRoleWebuser
	// DatabaseRoleSysuser selects the BYPASSRLS sysuser Postgres role
	// (no GUC).
	DatabaseRoleSysuser
)

// String returns a human-readable name for the database role; used in
// error messages emitted by [Transact] / [BeginHook].
func (r DatabaseRole) String() string {
	switch r {
	case DatabaseRoleAnonymous:
		return "anonymous"
	case DatabaseRoleWebuser:
		return "webuser"
	case DatabaseRoleSysuser:
		return "sysuser"
	case DatabaseRoleUnspecified:
		return "unspecified"
	default:
		return fmt.Sprintf("DatabaseRole(%d)", int(r))
	}
}

// The wire boundary of stdcrpcenttenancyfx ([WithDatabaseRole] /
// [DatabaseRoleFromContext], the [DatabaseRoleResolver] interface,
// [ProtoExtensionDatabaseRole], and [Authorize.Interceptor]) lives in
// [interceptor.go]. This file owns the BeginHook + fx graph; the two
// halves talk to each other through the unexported ctx key alone.

// Authorize is the per-request → per-tx role/GUC mapper. Held as a
// struct (rather than a free function) so future stateful dependencies
// (e.g. an org-membership lookup that scopes a sysuser tx to a single
// org) become an additive change rather than a signature change.
type Authorize struct {
	cfg      Config
	tenantID TenantIDResolver
}

// ErrUnmanagedTransaction is returned by [Authorize.BeginHook] when a
// transaction is opened against one of the package-managed
// transactors WITHOUT going through [Transact] / [Transact0] first.
// Exposed as a sentinel so callers (and tests) can `errors.Is` it
// rather than match on message text.
//
// In practice the developer who triggers this sees the error wrapped
// twice (`failed to setup tx, rolled back: setup hook: <this>`); the
// stdcrpcenttenancyfx-prefixed message below is the leaf and is
// intentionally long because there is no second chance to direct the
// developer at the right fix.
var ErrUnmanagedTransaction = errors.New(
	"stdcrpcenttenancyfx: refusing to open a transaction that did not go through " +
		"stdcrpcenttenancyfx.Transact / stdcrpcenttenancyfx.Transact0. " +
		"Calling stdent.Transact* directly with the rw/ro transactor bypasses the " +
		"package's audit chokepoint (where future cross-cutting auth concerns will " +
		"live). Fix: replace `stdent.Transact0(ctx, h.rw, fn)` with " +
		"`stdcrpcenttenancyfx.Transact0(ctx, h.rw, fn)` (same signature). " +
		"If this code path is trusted internal code that legitimately needs to " +
		"cross tenant boundaries (Temporal activities, system bootstrap, test seed " +
		"helpers), stamp the ctx with stdcrpcenttenancyfx.WithDatabaseRole(ctx, " +
		"stdcrpcenttenancyfx.DatabaseRoleSysuser) before calling Transact / Transact0 — " +
		"consumers typically gate that import-site form behind a forbidigo lint, " +
		"NOT a direct stdent.Transact* call",
)

// ErrMissingDatabaseRole is returned by [Transact] / [Transact0] (and
// the BeginHook as a defense-in-depth backstop) when ctx carries no
// database role. In production this is a programmer error: every RPC
// handler runs under the stdcrpcenttenancyfx interceptor which stamps
// a role from the proto annotation, and trusted internal callers must
// explicitly stamp a role via [WithDatabaseRole].
var ErrMissingDatabaseRole = errors.New(
	"stdcrpcenttenancyfx: refusing to open a transaction with no database role on ctx. " +
		"Inside an RPC handler this means the stdcrpcenttenancyfx interceptor was " +
		"not wired into the handler chain (or the proto method is missing " +
		"its `db_role` annotation). " +
		"Outside an RPC (Temporal activities, system bootstrap, test seed " +
		"helpers), call stdcrpcenttenancyfx.WithDatabaseRole(ctx, " +
		"stdcrpcenttenancyfx.DatabaseRoleSysuser) before calling " +
		"stdcrpcenttenancyfx.Transact / Transact0",
)

// BeginHook is the [stdent.BeginHookFunc] this package contributes. It
// appends `SET LOCAL ROLE` plus (when the role is webuser) a
// `set_config` of the [Config.TenantIDGUC] to the transaction's setup
// SQL. The hook signature requires us to APPEND to sqlb and return
// the same builder; the stdent driver flushes the accumulated SQL
// inside the transaction.
//
// Two runtime backstops fire here as defense-in-depth against
// developers calling stdent.Transact* directly (see [managedTxKey]
// and [databaseRoleKey] godoc for the rationale):
//
//   - the package-internal "managed tx" marker must be present (set
//     only by [Transact] / [Transact0]); without it the hook
//     returns [ErrUnmanagedTransaction];
//   - a database role must be set on ctx (set by the stdcrpcenttenancyfx
//     interceptor or by an explicit [WithDatabaseRole] call);
//     without it the hook returns [ErrMissingDatabaseRole].
//
// In both cases the stdent driver rolls the tx back before any query
// runs.
func (a *Authorize) BeginHook(
	ctx context.Context, sqlb *strings.Builder, _ entdialect.ExecQuerier,
) (*strings.Builder, error) {
	if !hasManagedTx(ctx) {
		return nil, ErrUnmanagedTransaction
	}

	role, ok := DatabaseRoleFromContext(ctx)
	if !ok || role == DatabaseRoleUnspecified {
		return nil, ErrMissingDatabaseRole
	}

	roleName, gucValue, err := a.resolve(ctx, role)
	if err != nil {
		return nil, err
	}

	if gucValue != nil {
		fmt.Fprintf(sqlb, `SELECT set_config(%s, %s, true); `,
			sqlQuoteLiteral(a.cfg.TenantIDGUC), sqlQuoteLiteral(*gucValue))
	}

	fmt.Fprintf(sqlb, `SET LOCAL ROLE %s; `, pgx.Identifier{roleName}.Sanitize())

	return sqlb, nil
}

// sqlQuoteLiteral quotes a Postgres string literal using the standard
// single-quote-and-double-internal-single-quote rule. Used for the GUC
// name (a string argument to set_config) and the tenant value.
//
// Copied verbatim from pgx/v5's internal sanitize package
// (github.com/jackc/pgx/v5/internal/sanitize.QuoteString) because that
// package is unimportable. Identifier quoting uses the exported
// [pgx.Identifier.Sanitize] instead.
func sqlQuoteLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// TenantIDResolver returns the opaque tenant id associated with a
// request ctx, or "" when no tenant is in scope (anonymous request,
// M2M token without an org claim). The empty string is the
// load-bearing sentinel emitted into the access.tenant_id GUC for
// the webuser path — see [Authorize.BeginHook].
//
// stdcrpcenttenancyfx is identity-agnostic: it does not know whether
// the tenant id originates from a JWT claim, an API-key lookup, or a
// header. Production wires the resolver at the composition root
// (e.g. binding it to a JWT TenantID claim populated by stdcrpcauthfx).
// It is a required dependency: every binary that wires [Provide] must
// also wire a resolver, so the webuser path's tenant id is never
// silently absent.
//
// Implementations must be pure ctx readers — no I/O, no allocation
// hot paths — because the resolver is invoked on every ent
// transaction the webuser path opens.
type TenantIDResolver interface {
	TenantIDFromContext(ctx context.Context) string
}

// TenantIDResolverFunc adapts a plain function into a
// [TenantIDResolver]. Saves callers the boilerplate of declaring a
// one-method type just to bind the interface in their fx graph —
// the same convenience http.HandlerFunc / connect.UnaryFunc give
// for their respective interfaces.
type TenantIDResolverFunc func(ctx context.Context) string

// TenantIDFromContext implements [TenantIDResolver].
func (f TenantIDResolverFunc) TenantIDFromContext(ctx context.Context) string {
	return f(ctx)
}

// resolve maps a [DatabaseRole] (from ctx) to the configured Postgres
// role name and, for the webuser path, the GUC value to inject. Split
// out from [Authorize.BeginHook] so the decision logic is
// straightforward to unit-test without going through the whole stdent
// driver.
//
// A nil gucValue means "do not emit a set_config statement at all"
// (sysuser path: BYPASSRLS makes the GUC moot; anonymous path: no
// tenant to scope by).
func (a *Authorize) resolve(
	ctx context.Context, role DatabaseRole,
) (roleName string, gucValue *string, err error) {
	switch role {
	case DatabaseRoleSysuser:
		return a.cfg.SystemDatabaseRole, nil, nil
	case DatabaseRoleAnonymous:
		return a.cfg.AnonymousDatabaseRole, nil, nil
	case DatabaseRoleWebuser:
		// Authenticated. The resolved tenant id may be empty (e.g.
		// an M2M token with no org claim) — emit it anyway so RLS
		// policies see a deterministic empty-string sentinel rather
		// than a missing GUC, which would cause `current_setting(
		// name, true)` to return NULL and complicate the policy
		// expressions.
		t := a.tenantID.TenantIDFromContext(ctx)

		return a.cfg.WebUserDatabaseRole, &t, nil
	case DatabaseRoleUnspecified:
		// Unreachable: BeginHook screens this before calling resolve.
		// Returned here as a defensive fallthrough so a future caller
		// of resolve doesn't silently get an empty role name.
		return "", nil, ErrMissingDatabaseRole
	default:
		return "", nil, errors.Newf("stdcrpcenttenancyfx: unknown role %v", role)
	}
}

// Params is the fx input for [New]. DatabaseRoleResolver is optional so
// the worker binary (which wires [Provide] for the BeginHook only and
// has no RPC surface to validate) can skip wiring a resolver. When
// absent, [New] skips boot-time validation and returns a no-op
// [Interceptor]; the BeginHook still fires for every transaction
// opened against an stdcrpcenttenancyfx-managed transactor — workers
// must stamp roles via [WithDatabaseRole] (with a //nolint:forbidigo
// justification) on activity boundaries instead.
type Params struct {
	fx.In
	Config       Config
	Logs         *zap.Logger
	RoleResolver DatabaseRoleResolver `optional:"true"`
	// TenantID resolves the opaque tenant id stamped into the
	// access.tenant_id GUC for the webuser path. Required: every
	// binary that wires [Provide] must also wire a resolver, even
	// binaries (e.g. the worker) whose webuser path is unreachable
	// in practice — it is one fx.Provide line at the composition
	// root and prevents a future code change from silently turning
	// the webuser path into a no-op.
	TenantID TenantIDResolver
}

// Result is the fx output for [New]. The BeginHook is exported as
// [stdent.BeginHookFunc] so [stdenttxfx.New] picks it up via its
// optional `TxBeginSQL` parameter — no manual wiring needed in the
// composition root beyond calling [Provide]. The Interceptor is
// exposed so the rpc package can install it next to its other
// interceptors.
type Result struct {
	fx.Out
	Authorize   *Authorize
	BeginHook   stdent.BeginHookFunc
	Interceptor connect.UnaryInterceptorFunc `name:"stdcrpcenttenancyfx"`
}

// New constructs an [Authorize] for the fx graph and, when a
// [DatabaseRoleResolver] is wired, validates that every RPC procedure
// it knows about declares a non-zero `db_role` annotation. The
// validation runs synchronously inside the constructor so a missing
// annotation fails app start instead of surfacing the first time the
// offending RPC is called.
func New(params Params) (Result, error) {
	auth := &Authorize{cfg: params.Config, tenantID: params.TenantID}

	res := Result{
		Authorize: auth,
		BeginHook: auth.BeginHook,
		// Default no-op interceptor for binaries without a resolver
		// (the worker). Overwritten below when a resolver is wired.
		Interceptor: func(next connect.UnaryFunc) connect.UnaryFunc { return next },
	}

	if params.RoleResolver == nil {
		return res, nil
	}

	// Boot-time validation: every RPC in the proto package that owns
	// the db_role extension must declare a non-zero value. The check
	// is cheap (one walk of the proto registry) and prevents an
	// otherwise-silent failure mode where a newly-added RPC reaches
	// production without an annotation and rejects every transaction
	// at request time.
	procs, err := params.RoleResolver.AllProcedures()
	if err != nil {
		return Result{}, errors.Wrap(err, "enumerate procedures for boot-time db_role validation")
	}

	for _, proc := range procs {
		if _, err := params.RoleResolver.RequiredDatabaseRole(proc); err != nil {
			return Result{}, errors.Wrap(err, "boot-time db_role validation")
		}
	}

	res.Interceptor = auth.Interceptor(params.RoleResolver, params.Logs)

	return res, nil
}

// Provide wires the package as an fx module. Env vars are read with
// the prefix `STDCRPCENTTENANCY_` (derived from the module name).
//
// Callers must also supply a [DatabaseRoleResolver] in the same fx
// graph — typically via [ProtoExtensionDatabaseRole] passing the app's
// `db_role` extension type (e.g. `rpcv1.E_DbRole`).
func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdcrpcenttenancy", New)
}
