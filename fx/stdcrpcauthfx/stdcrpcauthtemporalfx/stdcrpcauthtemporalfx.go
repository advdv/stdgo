// Package stdcrpcauthtemporalfx propagates the
// [stdcrpcauthfx.Claims] stamped on an RPC ctx (most importantly the
// per-request tenant identity) across the Temporal client → workflow
// → activity boundary. Combined with stdcrpcauthfx's
// [stdcrpcauthfx.ProvideTenantIDResolver] (and consequently the
// stdcrpcenttenancyfx [BeginHook]), every ent transaction opened by
// an activity automatically inherits the originating RPC's tenant —
// no manual claim stamping in activity code, no fallback to a
// missing/zero tenant, no cross-tenant leakage from a stale ctx.
//
// Layering: this is wire-level claim propagation across the Temporal
// boundary, the natural sibling of stdcrpcauthfx (which makes the
// same authentication decision across the HTTP boundary via its
// authn middleware). It does NOT perform authorization decisions —
// those still live in stdcrpcauthfx (scope check / permission
// resolver).
//
// Wiring: contributes a [workflow.ContextPropagator] to the fx graph.
// Composition roots assemble the final []workflow.ContextPropagator
// slice consumed by stdtemporalfx.New themselves so additional sibling
// propagators (e.g. the
// [github.com/advdv/stdgo/fx/stdcrpcenttenancyfx/stdcrpcenttenancytemporalfx]
// db_role propagator) can be combined alongside without provider
// conflicts.
package stdcrpcauthtemporalfx

import (
	"context"

	"github.com/advdv/stdgo/fx/stdcrpcauthfx"
	"github.com/cockroachdb/errors"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/fx"
)

// temporalHeaderKey is the Temporal header name under which the
// serialized [stdcrpcauthfx.Claims] travel. Namespaced + versioned
// by default so a future schema change can ship as a new key
// (allowing both to coexist during a rolling deploy). Kept as an
// unexported constant because the propagator is wire-internal —
// production callers never reach for the key directly.
const temporalHeaderKey = "advdv.stdgo.stdcrpcauth.claims.v1"

// claimsWorkflowCtxKey is the workflow-context key under which the
// extracted [stdcrpcauthfx.Claims] are stashed by
// [Propagator.ExtractToWorkflow] when the workflow starts. Read by
// [Propagator.InjectFromWorkflow] when the workflow schedules an
// activity, so the activity header carries the same claims the
// workflow received from its caller.
//
// Unexported: workflow / activity code MUST NOT read this key
// directly. The path from "I have claims" to "my ent tx runs in the
// right tenant" is fully wire-side; surfacing the key would invite
// ad-hoc reads that drift from stdcrpcauthfx's contract.
type claimsWorkflowCtxKey struct{}

// wireClaims is the on-the-wire shape of [stdcrpcauthfx.Claims].
// Defined explicitly (rather than serializing the exported struct
// directly) so the wire format is stable against future field
// additions/renames in [stdcrpcauthfx.Claims].
type wireClaims struct {
	Subject  string   `json:"sub,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
	TenantID string   `json:"tid,omitempty"`
}

// Propagator carries the per-RPC [stdcrpcauthfx.Claims] across the
// Temporal client → workflow → activity boundary. Combined with the
// stdcrpcenttenancyfx [BeginHook] (when stdcrpcauthfx's
// TenantIDResolver is wired), every ent transaction opened by an
// activity automatically inherits the originating RPC's tenant
// posture — no manual [stdcrpcauthfx.WithClaims] stamping in activity
// code, no silent cross-tenant leakage.
//
// The authn decision still happens at the wire boundary (the
// stdcrpcauthfx Connect middleware verifies the JWT and stamps the
// resulting [stdcrpcauthfx.Claims] on ctx). The propagator just
// transports that decision through the Temporal header chain so the
// activity ctx ends up with the same claims the RPC handler had.
//
// The propagator does NOT participate in the authn DECISION — it
// only transports claims that some caller already authenticated.
// That keeps it orthogonal to wire-level role propagation (e.g. the
// stdcrpcenttenancytemporalfx db_role propagator) and composable
// with it: both can be wired side-by-side as separate
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
// starting a workflow / signaling / etc. We read the claims off the
// caller ctx (stamped by the stdcrpcauthfx Connect middleware from
// the verified JWT, or by an explicit [stdcrpcauthfx.WithClaims] call
// in trusted internal code) and write them to the workflow header.
//
// When ctx carries no claims (zero value with no Subject, no Scopes,
// no TenantID) we skip the header write entirely. The receiver then
// sees no header and leaves its ctx unstamped, so downstream
// fail-closed contracts (e.g. the stdcrpcenttenancyfx BeginHook
// gated by the TenantIDResolver) kick in if downstream code expects
// a tenant — far better than silently picking a default.
func (p *Propagator) Inject(ctx context.Context, writer workflow.HeaderWriter) error {
	claims := stdcrpcauthfx.ClaimsFromContext(ctx)
	if isEmpty(claims) {
		return nil
	}

	return p.write(writer, claims)
}

// Extract is called by the Temporal worker on the activity side
// before invoking the activity body. We read the claims out of the
// activity header (propagated from the workflow header by the SDK
// + [Propagator.InjectFromWorkflow]) and stamp them onto the
// activity ctx via [stdcrpcauthfx.WithClaims] — the same surface
// the stdcrpcauthfx middleware writes to in the HTTP path, so
// [stdcrpcauthfx.ClaimsFromContext] (and the TenantIDResolver it
// backs) picks them back up downstream.
//
// When the header is missing or unparseable we deliberately leave
// ctx unstamped rather than fall back to a default. Any subsequent
// attempt to open a tenanted transaction then fails (via the
// BeginHook) instead of running in an unexpected tenant.
func (p *Propagator) Extract(ctx context.Context, reader workflow.HeaderReader) (context.Context, error) {
	claims, ok, err := p.read(reader)
	if err != nil || !ok {
		return ctx, err
	}

	// Legitimate (and only) call site of WithClaims inside this
	// package: the propagator is the only path by which a Temporal
	// activity learns the originating RPC's claims. Without this
	// stamp the activity ctx arrives claim-less and downstream
	// tenant resolution returns empty. The stdcrpcauthfx middleware
	// cannot run here — there is no inbound HTTP request — so the
	// propagator is its functional equivalent on the Temporal
	// boundary.
	return stdcrpcauthfx.WithClaims(ctx, claims), nil
}

// InjectFromWorkflow is called by the Temporal worker when a workflow
// schedules an activity (or child workflow). We read the claims from
// the workflow ctx — placed there by [Propagator.ExtractToWorkflow]
// when the workflow started — and propagate them down to the activity
// header. Without this step the workflow header would not carry
// forward to activities, and the activity ctx would arrive claim-less.
func (p *Propagator) InjectFromWorkflow(ctx workflow.Context, writer workflow.HeaderWriter) error {
	claims, ok := ctx.Value(claimsWorkflowCtxKey{}).(stdcrpcauthfx.Claims)
	if !ok || isEmpty(claims) {
		return nil
	}

	return p.write(writer, claims)
}

// ExtractToWorkflow is called by the Temporal worker when the
// workflow starts execution. We stash the decoded claims on the
// workflow ctx so [Propagator.InjectFromWorkflow] can find them
// when the workflow later schedules activities. Workflow code itself
// does NOT read this — the key is unexported by design (see
// [claimsWorkflowCtxKey]).
func (p *Propagator) ExtractToWorkflow(
	ctx workflow.Context, reader workflow.HeaderReader,
) (workflow.Context, error) {
	claims, ok, err := p.read(reader)
	if err != nil || !ok {
		return ctx, err
	}

	return workflow.WithValue(ctx, claimsWorkflowCtxKey{}, claims), nil
}

// write encodes claims and stores them on writer under
// [temporalHeaderKey]. The wire form is a fixed [wireClaims] JSON
// shape — stable independently of future [stdcrpcauthfx.Claims]
// field changes.
func (p *Propagator) write(writer workflow.HeaderWriter, claims stdcrpcauthfx.Claims) error {
	encoded, err := p.converter.ToPayload(wireClaims{
		Subject:  claims.Subject,
		Scopes:   claims.Scopes,
		TenantID: claims.TenantID,
	})
	if err != nil {
		return errors.Wrap(err, "encode stdcrpcauth claims")
	}

	writer.Set(temporalHeaderKey, encoded)

	return nil
}

// read decodes [stdcrpcauthfx.Claims] out of reader's
// [temporalHeaderKey] header. The "ok=false" return distinguishes
// "no header present" from "header present but failed to decode" —
// only the latter is an error worth surfacing. An empty payload
// (all fields blank) is treated as missing so a peer that wrote an
// empty header does not silently stamp an empty-claims ctx.
func (p *Propagator) read(reader workflow.HeaderReader) (stdcrpcauthfx.Claims, bool, error) {
	encoded, ok := reader.Get(temporalHeaderKey)
	if !ok || encoded == nil {
		return stdcrpcauthfx.Claims{}, false, nil
	}

	var raw wireClaims
	if err := p.converter.FromPayload(encoded, &raw); err != nil {
		return stdcrpcauthfx.Claims{}, false,
			errors.Wrap(err, "decode stdcrpcauth claims")
	}

	claims := stdcrpcauthfx.Claims{
		Subject:  raw.Subject,
		Scopes:   raw.Scopes,
		TenantID: raw.TenantID,
	}
	if isEmpty(claims) {
		return stdcrpcauthfx.Claims{}, false, nil
	}

	return claims, true, nil
}

// isEmpty reports whether claims carry no usable identity. Used by
// Inject / InjectFromWorkflow to skip empty header writes, and by
// read to treat an all-blank payload as "no claims" rather than
// stamping an empty-claims ctx that would still look "present" to
// [stdcrpcauthfx.ClaimsFromContext].
func isEmpty(c stdcrpcauthfx.Claims) bool {
	return c.Subject == "" && c.TenantID == "" && len(c.Scopes) == 0
}

// Compile-time guard that [Propagator] satisfies the SDK contract; a
// future SDK bump that adds methods will fail here rather than at
// runtime when the worker tries to register it.
var _ workflow.ContextPropagator = (*Propagator)(nil)

// Compile-time anchor that keeps the *commonpb.Payload import in
// front of every reviewer of this file. The SDK's HeaderWriter /
// Reader are typed against *commonpb.Payload; a future change to the
// wire shape (e.g. switching off the default data converter) starts
// here.
var _ *commonpb.Payload = (*commonpb.Payload)(nil)

// Provide wires the [Propagator] into the fx graph. Composition roots
// that already wire stdcrpcauthfx.Provide() add this option
// alongside it; composing them as separate options keeps the
// stdcrpcauthfx middleware wiring usable in non-Temporal binaries
// (and tests) that do not need the propagator.
//
// The propagator is exposed only as `*Propagator`. Composition roots
// assemble the final `[]workflow.ContextPropagator` slice themselves,
// because the slice is the integration point with stdtemporalfx and
// may need to include propagators contributed by other packages
// (e.g. the stdcrpcenttenancytemporalfx db_role propagator).
// Producing the slice here would conflict with any sibling provider.
func Provide() fx.Option {
	return fx.Module("stdcrpcauthtemporalfx",
		fx.Provide(New),
	)
}
