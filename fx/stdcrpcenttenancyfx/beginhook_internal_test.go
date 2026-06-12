package stdcrpcenttenancyfx

// This file lives in package stdcrpcenttenancyfx (NOT
// stdcrpcenttenancyfx_test) so it can stamp ctx with
// [contextWithManagedTx] directly when invoking [Authorize.BeginHook]
// out-of-band — i.e. without going through the [Transact] /
// [Transact0] chokepoint that production code is required to use, and
// without going through the stdcrpcenttenancyfx Connect interceptor
// that stamps the role under production conditions.
//
// External-package tests that exercise the BeginHook through its
// public callers (the three-posture test in [transact_test.go],
// black-box marker behaviour in [stdcrpcenttenancyfx_test.go]) stay
// where they are and stamp the role via the public [WithDatabaseRole].

import (
	"context"
	"strings"
	"testing"

	"github.com/advdv/stdgo/stdent"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

// stubTenantID is a [TenantIDResolver] that returns a fixed value,
// regardless of ctx. Used by these unit tests in place of the
// production binding (which reads stdcrpcauthfx.Claims.TenantID) so
// the package can be exercised without a wire-level authn dependency.
type stubTenantID string

func (s stubTenantID) TenantIDFromContext(context.Context) string { return string(s) }

// stubSubject is a [SubjectResolver] sibling of stubTenantID: a fixed
// value regardless of ctx, in place of the production binding (which
// reads stdcrpcauthfx.Claims.Subject).
type stubSubject string

func (s stubSubject) SubjectFromContext(context.Context) string { return string(s) }

// testCfg returns a Config with deliberately-distinct role names so test
// assertions cannot accidentally cross-match.
func testCfg() Config {
	return Config{
		AnonymousDatabaseRole: "anon_role",
		SystemDatabaseRole:    "sys_role",
		WebUserDatabaseRole:   "web_role",
		TenantIDGUC:           "access.tenant_id",
		SubjectGUC:            "access.subject",
	}
}

// newAuthorize constructs an Authorize through the package's public [New]
// constructor so the test goes through exactly the wiring production uses.
// No DatabaseRoleResolver is wired — these tests exercise the BeginHook
// with the role stamped directly via [WithDatabaseRole]. The tenantID
// resolver is required (mirrors production: TenantIDResolver is a
// required dependency); tests that don't reach the webuser path still
// pass a stub so the constructor signature stays uniform.
func newAuthorize(t *testing.T, cfg Config, tenantID TenantIDResolver) (*Authorize, stdent.BeginHookFunc) {
	t.Helper()

	res, err := New(Params{Config: cfg, Logs: zap.NewNop(), TenantID: tenantID})
	require.NoError(t, err)

	return res.Authorize, res.BeginHook
}

// newAuthorizeWithSubject mirrors newAuthorize with the OPTIONAL
// [SubjectResolver] wired, the way a composition root that opts into
// subject attribution does. Kept as a separate constructor (rather
// than a nil-able parameter on newAuthorize) so the many existing
// resolver-less tests double as the "no resolver wired → no subject
// set_config" regression suite without edits.
func newAuthorizeWithSubject(
	t *testing.T, cfg Config, tenantID TenantIDResolver, subject SubjectResolver,
) (*Authorize, stdent.BeginHookFunc) {
	t.Helper()

	res, err := New(Params{Config: cfg, Logs: zap.NewNop(), TenantID: tenantID, Subject: subject})
	require.NoError(t, err)

	return res.Authorize, res.BeginHook
}

// runHook invokes the BeginHook on a fresh builder and returns the SQL it
// appended. The third arg (ent ExecQuerier) is unused by the hook
// implementation, so passing nil keeps tests free of ent driver scaffolding.
//
// The hook requires both the package-internal "managed tx" marker
// (added by [Transact] / [Transact0]) and a database role on ctx
// (added by the stdcrpcenttenancyfx interceptor in production, or by
// an explicit [WithDatabaseRole] call). These unit tests deliberately
// exercise the BeginHook out-of-band (no real tx, no interceptor),
// so we stamp the managed-tx marker here via the unexported
// [contextWithManagedTx] (possible only because this file is in
// package stdcrpcenttenancyfx) and the role via the public
// [WithDatabaseRole].
func runHook(t *testing.T, hook stdent.BeginHookFunc, ctx context.Context, role DatabaseRole) string {
	t.Helper()

	ctx = contextWithManagedTx(ctx)
	ctx = WithDatabaseRole(ctx, role)

	var sqlb strings.Builder

	got, err := hook(ctx, &sqlb, nil)
	require.NoError(t, err)
	require.Same(t, &sqlb, got, "hook must return the same builder it was given")

	return sqlb.String()
}

func TestBeginHook_sysuser_role_no_guc(t *testing.T) {
	t.Parallel()

	_, hook := newAuthorize(t, testCfg(), stubTenantID(""))

	got := runHook(t, hook, t.Context(), DatabaseRoleSysuser)

	assert.Equal(t, `SET LOCAL ROLE "sys_role"; `, got,
		"sysuser path must skip set_config and switch to the system role")
}

func TestBeginHook_anonymous_role_no_guc(t *testing.T) {
	t.Parallel()

	_, hook := newAuthorize(t, testCfg(), stubTenantID(""))

	got := runHook(t, hook, t.Context(), DatabaseRoleAnonymous)

	assert.Equal(t, `SET LOCAL ROLE "anon_role"; `, got,
		"anonymous path must skip set_config and switch to the anonymous role")
}

func TestBeginHook_webuser_emits_set_config_and_role(t *testing.T) {
	t.Parallel()

	_, hook := newAuthorize(t, testCfg(), stubTenantID("org_ABC"))

	got := runHook(t, hook, t.Context(), DatabaseRoleWebuser)

	assert.Equal(t,
		`SELECT set_config('access.tenant_id', 'org_ABC', true); SET LOCAL ROLE "web_role"; `,
		got)
}

func TestBeginHook_webuser_with_empty_tenant_emits_empty_string(t *testing.T) {
	t.Parallel()

	// A resolver that returns "" (e.g. M2M token with no org claim)
	// must still emit a deterministic empty-string GUC so RLS
	// policies see a value rather than NULL.
	_, hook := newAuthorize(t, testCfg(), stubTenantID(""))

	got := runHook(t, hook, t.Context(), DatabaseRoleWebuser)

	assert.Equal(t,
		`SELECT set_config('access.tenant_id', '', true); SET LOCAL ROLE "web_role"; `,
		got)
}

func TestBeginHook_webuser_with_subject_emits_both_gucs(t *testing.T) {
	t.Parallel()

	_, hook := newAuthorizeWithSubject(t, testCfg(), stubTenantID("org_ABC"), stubSubject("auth0|user1"))

	got := runHook(t, hook, t.Context(), DatabaseRoleWebuser)

	assert.Equal(t,
		`SELECT set_config('access.tenant_id', 'org_ABC', true); `+
			`SELECT set_config('access.subject', 'auth0|user1', true); `+
			`SET LOCAL ROLE "web_role"; `,
		got,
		"webuser path with an authenticated caller must stamp tenant THEN subject before the role switch")
}

func TestBeginHook_sysuser_with_subject_emits_subject_guc(t *testing.T) {
	t.Parallel()

	// The asymmetry under test: sysuser emits NO tenant GUC (BYPASSRLS
	// makes the authorizing GUC moot) but DOES emit the subject GUC —
	// attribution is informational and an authenticated caller on a
	// BYPASSRLS path (admin RPCs, activities with propagated claims)
	// still deserves it.
	_, hook := newAuthorizeWithSubject(t, testCfg(), stubTenantID(""), stubSubject("client_M2M@clients"))

	got := runHook(t, hook, t.Context(), DatabaseRoleSysuser)

	assert.Equal(t,
		`SELECT set_config('access.subject', 'client_M2M@clients', true); SET LOCAL ROLE "sys_role"; `,
		got)
}

func TestBeginHook_empty_subject_emits_no_subject_guc(t *testing.T) {
	t.Parallel()

	// Opposite sentinel convention to the tenant id: an empty subject
	// must OMIT the set_config entirely (unset GUC → SQL NULL under
	// missing_ok → the trigger-side "no authenticated caller" signal),
	// never emit an empty string.
	_, hook := newAuthorizeWithSubject(t, testCfg(), stubTenantID("org_ABC"), stubSubject(""))

	got := runHook(t, hook, t.Context(), DatabaseRoleWebuser)

	assert.Equal(t,
		`SELECT set_config('access.tenant_id', 'org_ABC', true); SET LOCAL ROLE "web_role"; `,
		got)
}

func TestBeginHook_subject_with_embedded_single_quote_is_quoted(t *testing.T) {
	t.Parallel()

	// JWT sub values are attacker-influenced strings; an embedded
	// single quote must not terminate the literal early.
	_, hook := newAuthorizeWithSubject(t, testCfg(), stubTenantID(""), stubSubject("o'brien"))

	got := runHook(t, hook, t.Context(), DatabaseRoleSysuser)

	assert.Equal(t,
		`SELECT set_config('access.subject', 'o''brien', true); SET LOCAL ROLE "sys_role"; `,
		got)
}

func TestBeginHook_appends_to_existing_builder_content(t *testing.T) {
	t.Parallel()

	_, hook := newAuthorize(t, testCfg(), stubTenantID(""))

	var sqlb strings.Builder
	sqlb.WriteString("/* preamble */ ")

	ctx := contextWithManagedTx(t.Context())
	ctx = WithDatabaseRole(ctx, DatabaseRoleAnonymous)

	got, err := hook(ctx, &sqlb, nil)
	require.NoError(t, err)
	require.Same(t, &sqlb, got)

	assert.Equal(t,
		`/* preamble */ SET LOCAL ROLE "anon_role"; `,
		sqlb.String(),
		"hook must append to, not overwrite, the caller's builder")
}

func TestBeginHook_quotes_role_with_embedded_double_quote(t *testing.T) {
	t.Parallel()

	cfg := testCfg()
	// A role name with an embedded double quote would, without proper
	// quoting, terminate the identifier early. pgx.Identifier.Sanitize
	// must double the embedded quote.
	cfg.AnonymousDatabaseRole = `weird"role`

	_, hook := newAuthorize(t, cfg, stubTenantID(""))

	got := runHook(t, hook, t.Context(), DatabaseRoleAnonymous)

	assert.Equal(t, `SET LOCAL ROLE "weird""role"; `, got)
}

func TestBeginHook_quotes_guc_name_and_tenant_with_embedded_single_quote(t *testing.T) {
	t.Parallel()

	cfg := testCfg()
	// A GUC name with an embedded single quote would terminate the
	// literal early; sqlQuoteLiteral must double it.
	cfg.TenantIDGUC = "weird'guc"

	_, hook := newAuthorize(t, cfg, stubTenantID("tenant'with'quotes"))

	got := runHook(t, hook, t.Context(), DatabaseRoleWebuser)

	assert.Equal(t,
		`SELECT set_config('weird''guc', 'tenant''with''quotes', true); `+
			`SET LOCAL ROLE "web_role"; `,
		got)
}

func TestBeginHook_returns_same_builder_pointer(t *testing.T) {
	t.Parallel()

	// The stdent driver flushes the *strings.Builder it handed us, so the
	// hook must not silently swap it for a different one.
	a, _ := newAuthorize(t, testCfg(), stubTenantID(""))

	var sqlb strings.Builder

	ctx := contextWithManagedTx(t.Context())
	ctx = WithDatabaseRole(ctx, DatabaseRoleAnonymous)

	got, err := a.BeginHook(ctx, &sqlb, nil)
	require.NoError(t, err)
	assert.Same(t, &sqlb, got)
}

func TestBeginHook_missing_role_is_rejected(t *testing.T) {
	t.Parallel()

	// A managed-tx ctx with no role on it indicates either a missing
	// stdcrpcenttenancyfx interceptor in the handler chain or a
	// programmer who reached straight for stdent.Transact* — both are
	// programmer errors and must surface as a rolled-back tx with
	// ErrMissingDatabaseRole.
	a, _ := newAuthorize(t, testCfg(), stubTenantID(""))

	ctx := contextWithManagedTx(t.Context())

	var sqlb strings.Builder
	_, err := a.BeginHook(ctx, &sqlb, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingDatabaseRole)
}

func TestBeginHook_unspecified_role_is_rejected(t *testing.T) {
	t.Parallel()

	// The proto enum's zero value is DB_ROLE_UNSPECIFIED. If a method
	// somehow slips past boot validation with a zero-valued role, the
	// BeginHook must still refuse to proceed rather than silently
	// pick a default.
	a, _ := newAuthorize(t, testCfg(), stubTenantID(""))

	ctx := contextWithManagedTx(t.Context())
	ctx = WithDatabaseRole(ctx, DatabaseRoleUnspecified)

	var sqlb strings.Builder
	_, err := a.BeginHook(ctx, &sqlb, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingDatabaseRole)
}

func TestProvide_wires_authorize_and_begin_hook_from_env(t *testing.T) {
	t.Parallel()

	// Exercise the fx wiring + env decoding end-to-end so a typo in the
	// env-var prefix or the struct tags fails here rather than silently
	// in production. No DatabaseRoleResolver is wired (it's optional),
	// so boot-time validation is skipped.
	var deps struct {
		fx.In

		Authorize *Authorize
		BeginHook stdent.BeginHookFunc
	}

	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDCRPCENTTENANCY_ANONYMOUS_DATABASE_ROLE": "env_anon",
			"STDCRPCENTTENANCY_SYSTEM_DATABASE_ROLE":    "env_sys",
			"STDCRPCENTTENANCY_WEBUSER_DATABASE_ROLE":   "env_web",
		}),
		// stdcrpcenttenancyfx.New depends on a *zap.Logger (used by
		// the role-stamping interceptor). Production wires it via
		// stdzapfx; tests supply a no-op directly to avoid pulling
		// the full zap module.
		fx.Supply(zap.NewNop()),
		// TenantIDResolver is a required dependency of [New] —
		// supplying a stub here proves the fx wiring composes.
		// We don't drive the webuser path in the assertions
		// below (the test exercises sysuser + anonymous), so the
		// returned value is irrelevant.
		fx.Supply(fx.Annotate(stubTenantID(""), fx.As(new(TenantIDResolver)))),
		Provide(),
		fx.Populate(&deps),
	)
	app.RequireStart()
	t.Cleanup(func() { app.RequireStop() })

	require.NotNil(t, deps.Authorize)
	require.NotNil(t, deps.BeginHook)

	// The hook fx exposes must be the same one bound to the Authorize the
	// graph constructed — verified by observing its behavior with the
	// env-supplied role names.
	got := runHook(t, deps.BeginHook, t.Context(), DatabaseRoleAnonymous)
	assert.Equal(t, `SET LOCAL ROLE "env_anon"; `, got,
		"BeginHook must reflect the env-supplied AnonymousDatabaseRole")

	gotSys := runHook(t, deps.BeginHook, t.Context(), DatabaseRoleSysuser)
	assert.Equal(t, `SET LOCAL ROLE "env_sys"; `, gotSys,
		"BeginHook must reflect the env-supplied SystemDatabaseRole")
}
