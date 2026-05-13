// Package stdcrpcenttenancytemporalfx propagates the
// [stdcrpcenttenancyfx.DatabaseRole] stamped on an RPC ctx across the
// Temporal client → workflow → activity boundary. Combined with the
// stdcrpcenttenancyfx [BeginHook], every ent transaction opened by an
// activity automatically inherits the originating RPC's role posture
// — no manual [stdcrpcenttenancyfx.WithDatabaseRole] stamping in
// activity code, no fallback to anonymous, no silent privilege
// escalation.
//
// Layering: this is wire-level role propagation across the Temporal
// boundary, the natural sibling of stdcrpcenttenancyfx (which makes
// the same role decision across the HTTP boundary). It does NOT
// perform authorization decisions — those still live in
// stdcrpcenttenancyfx (role / GUC).
//
// Wiring: contributes a [workflow.ContextPropagator] to the fx graph.
// Composition roots assemble the final []workflow.ContextPropagator
// slice consumed by stdtemporalfx.New themselves so additional sibling
// propagators (e.g. the JWT-claims propagator) can be combined
// alongside without provider conflicts.
package stdcrpcenttenancytemporalfx

import (
	"context"

	"github.com/advdv/stdgo/fx/stdcrpcenttenancyfx"
	"github.com/cockroachdb/errors"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/fx"
)

// temporalHeaderKey is the Temporal header name under which the
// serialized [stdcrpcenttenancyfx.DatabaseRole] travels. Namespaced +
// versioned by default so a future schema change can ship as a new key
// (allowing both to coexist during a rolling deploy). Kept as an
// unexported constant because the propagator is wire-internal —
// production callers never reach for the key directly.
const temporalHeaderKey = "advdv.stdgo.stdcrpcenttenancy.db_role.v1"

// databaseRoleWorkflowCtxKey is the workflow-context key under which
// an extracted [stdcrpcenttenancyfx.DatabaseRole] is stashed by
// [Propagator.ExtractToWorkflow] when the workflow starts. Read by
// [Propagator.InjectFromWorkflow] when the workflow schedules an
// activity, so the activity header carries the same role the workflow
// received from its caller.
//
// Unexported: workflow / activity code MUST NOT read this key
// directly. The path from "I have a role" to "my ent tx runs in the
// right posture" is fully wire-side; surfacing the key would invite
// ad-hoc reads that drift from stdcrpcenttenancyfx's contract.
type databaseRoleWorkflowCtxKey struct{}

// Propagator carries the per-RPC [stdcrpcenttenancyfx.DatabaseRole]
// across the Temporal client → workflow → activity boundary. Combined
// with stdcrpcenttenancyfx's [BeginHook], every ent transaction opened
// by an activity automatically inherits the originating RPC's role
// posture — no manual [stdcrpcenttenancyfx.WithDatabaseRole] stamping
// in activity code, no fallback to anonymous, no silent privilege
// escalation.
//
// The role decision still happens at the wire boundary (the
// stdcrpcenttenancyfx Connect interceptor reads the procedure's
// `db_role` proto annotation and stamps a role on ctx via
// [stdcrpcenttenancyfx.WithDatabaseRole]). The propagator just
// transports that decision through the Temporal header chain so the
// activity ctx ends up with the same role the RPC handler had.
//
// The propagator does NOT participate in the role DECISION — it only
// transports a role that some caller already chose. That keeps it
// orthogonal to wire-level authn (e.g. JWT claim propagation) and
// composable with it: both can be wired side-by-side as separate
// [workflow.ContextPropagator]s.
type Propagator struct {
	converter converter.DataConverter
}

// New constructs a [Propagator] using the SDK's default data
// converter — the same one Temporal uses for activity / workflow
// arguments by default.
func New() *Propagator {
	return &Propagator{converter: converter.GetDefaultDataConverter()}
}

// Inject is called by the Temporal client on the caller side when
// starting a workflow / signaling / etc. We read the role off the
// caller ctx (stamped by the stdcrpcenttenancyfx Connect interceptor
// from the procedure's `db_role` annotation, or by an explicit
// [stdcrpcenttenancyfx.WithDatabaseRole] call in trusted internal
// code) and write it to the workflow header.
//
// When ctx carries no role (or the unspecified zero value) we skip
// the header write entirely. The receiver then sees no header and
// leaves its ctx unstamped, so the BeginHook's fail-closed contract
// kicks in if downstream code tries to open a tx — far better than
// silently picking a default.
func (p *Propagator) Inject(ctx context.Context, writer workflow.HeaderWriter) error {
	role, ok := stdcrpcenttenancyfx.DatabaseRoleFromContext(ctx)
	if !ok || role == stdcrpcenttenancyfx.DatabaseRoleUnspecified {
		return nil
	}

	return p.write(writer, role)
}

// Extract is called by the Temporal worker on the activity side
// before invoking the activity body. We read the role out of the
// activity header (propagated from the workflow header by the SDK
// + [Propagator.InjectFromWorkflow]) and stamp it onto the
// activity ctx via [stdcrpcenttenancyfx.WithDatabaseRole] — the same
// surface the stdcrpcenttenancyfx interceptor writes to in the HTTP
// path, so [stdcrpcenttenancyfx.DatabaseRoleFromContext] picks it
// back up downstream.
//
// When the header is missing or unparseable we deliberately leave
// ctx unstamped rather than fall back to a default. The BeginHook
// then refuses any tx the activity opens with
// [stdcrpcenttenancyfx.ErrMissingDatabaseRole] — fail-closed.
func (p *Propagator) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	role, ok, err := p.read(reader)
	if err != nil || !ok {
		return ctx, err
	}

	// Legitimate (and only) call site of WithDatabaseRole inside
	// this package: the propagator is the only path by which a
	// Temporal activity learns the originating RPC's role posture.
	// Without this stamp the activity ctx arrives role-less and
	// BeginHook fails closed. The stdcrpcenttenancyfx Connect
	// interceptor cannot run here — there is no inbound HTTP
	// request — so the propagator is its functional equivalent on
	// the Temporal boundary. (Consumers that gate
	// stdcrpcenttenancyfx.WithDatabaseRole behind a project-wide
	// forbidigo rule should add this package to the rule's allowlist
	// — the call below is the legitimate exception, not a bypass.)
	return stdcrpcenttenancyfx.WithDatabaseRole(ctx, role), nil
}

// InjectFromWorkflow is called by the Temporal worker when a workflow
// schedules an activity (or child workflow). We read the role from
// the workflow ctx — placed there by [Propagator.ExtractToWorkflow]
// when the workflow started — and propagate it down to the activity
// header. Without this step the workflow header would not carry
// forward to activities, and the activity ctx would arrive role-less.
func (p *Propagator) InjectFromWorkflow(ctx workflow.Context, writer workflow.HeaderWriter) error {
	role, ok := ctx.Value(databaseRoleWorkflowCtxKey{}).(stdcrpcenttenancyfx.DatabaseRole)
	if !ok || role == stdcrpcenttenancyfx.DatabaseRoleUnspecified {
		return nil
	}

	return p.write(writer, role)
}

// ExtractToWorkflow is called by the Temporal worker when the
// workflow starts execution. We stash the decoded role on the
// workflow ctx so [Propagator.InjectFromWorkflow] can find it
// when the workflow later schedules activities. Workflow code itself
// does NOT read this — the key is unexported by design (see
// [databaseRoleWorkflowCtxKey]).
func (p *Propagator) ExtractToWorkflow(
	ctx workflow.Context, reader workflow.HeaderReader,
) (workflow.Context, error) {
	role, ok, err := p.read(reader)
	if err != nil || !ok {
		return ctx, err
	}

	return workflow.WithValue(ctx, databaseRoleWorkflowCtxKey{}, role), nil
}

// write encodes role and stores it on writer under [temporalHeaderKey].
// The wire form is the int32 enum value — small, bounded, and stable
// across the four declared [stdcrpcenttenancyfx.DatabaseRole] values.
func (p *Propagator) write(writer workflow.HeaderWriter, role stdcrpcenttenancyfx.DatabaseRole) error {
	//nolint:gosec // DatabaseRole values are bounded to 0..3; narrowing to int32 is safe.
	encoded, err := p.converter.ToPayload(int32(role))
	if err != nil {
		return errors.Wrap(err, "encode stdcrpcenttenancyfx db_role")
	}

	writer.Set(temporalHeaderKey, encoded)

	return nil
}

// read decodes a [stdcrpcenttenancyfx.DatabaseRole] out of reader's
// [temporalHeaderKey] header. The "ok=false" return distinguishes "no
// header present" from "header present but failed to decode" — only
// the latter is an error worth surfacing. An unknown enum value is
// treated like a decode failure (returned as an error) so a caller
// stamping a future role this binary doesn't recognize fails loudly
// rather than being silently coerced into Unspecified.
func (p *Propagator) read(reader workflow.HeaderReader) (stdcrpcenttenancyfx.DatabaseRole, bool, error) {
	encoded, ok := reader.Get(temporalHeaderKey)
	if !ok || encoded == nil {
		return stdcrpcenttenancyfx.DatabaseRoleUnspecified, false, nil
	}

	var raw int32
	if err := p.converter.FromPayload(encoded, &raw); err != nil {
		return stdcrpcenttenancyfx.DatabaseRoleUnspecified, false,
			errors.Wrap(err, "decode stdcrpcenttenancyfx db_role")
	}

	role := stdcrpcenttenancyfx.DatabaseRole(raw)
	switch role {
	case stdcrpcenttenancyfx.DatabaseRoleAnonymous,
		stdcrpcenttenancyfx.DatabaseRoleWebuser,
		stdcrpcenttenancyfx.DatabaseRoleSysuser:
		return role, true, nil
	case stdcrpcenttenancyfx.DatabaseRoleUnspecified:
		// Wire form should never carry the zero value: Inject /
		// InjectFromWorkflow skip the write when role is unset, so
		// receiving Unspecified here means a peer did write it
		// explicitly — surface that as missing rather than silently
		// picking a default.
		return stdcrpcenttenancyfx.DatabaseRoleUnspecified, false, nil
	default:
		return stdcrpcenttenancyfx.DatabaseRoleUnspecified, false,
			errors.Newf("stdcrpcenttenancytemporalfx: unknown db_role enum value %d on Temporal header", raw)
	}
}

// Compile-time guard that [Propagator] satisfies the SDK contract; a
// future SDK bump that adds methods will fail here rather than at
// runtime when the worker tries to register it.
var _ workflow.ContextPropagator = (*Propagator)(nil)

// Compile-time anchor that keeps the *commonpb.Payload import in
// front of every reviewer of this file. The SDK's HeaderWriter / Reader
// are typed against *commonpb.Payload; a future change to the wire
// shape (e.g. switching off the default data converter) starts here.
var _ *commonpb.Payload = (*commonpb.Payload)(nil)

// Provide wires the [Propagator] into the fx graph. Composition roots
// that already wire stdcrpcenttenancyfx.Provide() add this option
// alongside it; composing them as separate options keeps the
// stdcrpcenttenancyfx BeginHook / interceptor wiring usable in
// non-Temporal binaries (and tests) that do not need the propagator.
//
// The propagator is exposed only as `*Propagator`. Composition roots
// assemble the final `[]workflow.ContextPropagator` slice themselves,
// because the slice is the integration point with stdtemporalfx and
// may need to include propagators contributed by other packages (e.g.
// a JWT-claim propagator). Producing the slice here would conflict
// with any sibling provider.
func Provide() fx.Option {
	return fx.Module("stdcrpcenttenancytemporalfx",
		fx.Provide(New),
	)
}
