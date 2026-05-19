package stdcrpcauthtemporalfx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdcrpcauthfx"
	"github.com/advdv/stdgo/fx/stdcrpcauthfx/stdcrpcauthtemporalfx"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// fakeHeader implements both [workflow.HeaderWriter] and
// [workflow.HeaderReader] backed by a plain map — enough to exercise
// the wire format end-to-end without spinning up the full Temporal
// test environment.
type fakeHeader struct {
	values map[string]*commonpb.Payload
}

func newFakeHeader() *fakeHeader {
	return &fakeHeader{values: map[string]*commonpb.Payload{}}
}

func (h *fakeHeader) Set(key string, value *commonpb.Payload) {
	h.values[key] = value
}

func (h *fakeHeader) Get(key string) (*commonpb.Payload, bool) {
	v, ok := h.values[key]

	return v, ok
}

func (h *fakeHeader) ForEachKey(handler func(string, *commonpb.Payload) error) error {
	for k, v := range h.values {
		if err := handler(k, v); err != nil {
			return err
		}
	}

	return nil
}

// Compile-time guards: ensure the fake satisfies the SDK interfaces
// the propagator is typed against. A future SDK change to either
// interface surfaces here rather than at test runtime.
var (
	_ workflow.HeaderWriter = (*fakeHeader)(nil)
	_ workflow.HeaderReader = (*fakeHeader)(nil)
)

func TestInjectExtractRoundTrip(t *testing.T) {
	t.Parallel()

	prop := stdcrpcauthtemporalfx.New()

	claims := stdcrpcauthfx.Claims{
		Subject:  "auth0|user-123",
		Scopes:   []string{"system:read", "system:write"},
		TenantID: "org_ABC123",
	}

	header := newFakeHeader()
	ctx := stdcrpcauthfx.WithClaims(t.Context(), claims)

	require.NoError(t, prop.Inject(ctx, header))

	// Header must have been written under the namespaced/versioned key.
	require.Len(t, header.values, 1)
	_, ok := header.Get("advdv.stdgo.stdcrpcauth.claims.v1")
	require.True(t, ok, "propagator must write to the namespaced+versioned header key")

	got, err := prop.Extract(t.Context(), header)
	require.NoError(t, err)
	require.Equal(t, claims, stdcrpcauthfx.ClaimsFromContext(got))
}

func TestInjectSkipsWhenClaimsAreEmpty(t *testing.T) {
	t.Parallel()

	prop := stdcrpcauthtemporalfx.New()
	header := newFakeHeader()

	// ctx without any claims — Inject must not write a header,
	// otherwise the receiver would stamp an empty-claims ctx and
	// silently mask the absence of identity downstream.
	require.NoError(t, prop.Inject(t.Context(), header))
	require.Empty(t, header.values)
}

func TestExtractLeavesCtxUnstampedWhenHeaderMissing(t *testing.T) {
	t.Parallel()

	prop := stdcrpcauthtemporalfx.New()
	header := newFakeHeader()

	ctx, err := prop.Extract(t.Context(), header)
	require.NoError(t, err)
	require.Equal(t, stdcrpcauthfx.Claims{}, stdcrpcauthfx.ClaimsFromContext(ctx))
}

func TestExtractLeavesCtxUnstampedWhenHeaderCarriesEmptyClaims(t *testing.T) {
	t.Parallel()

	// Simulate a peer that wrote an empty payload (e.g. older
	// binary, or a bug). The propagator must treat it as missing
	// rather than stamp an empty-claims ctx that still looks
	// "present" to ClaimsFromContext.
	prop := stdcrpcauthtemporalfx.New()

	payload, err := converter.GetDefaultDataConverter().ToPayload(struct{}{})
	require.NoError(t, err)

	header := newFakeHeader()
	header.Set("advdv.stdgo.stdcrpcauth.claims.v1", payload)

	ctx, err := prop.Extract(t.Context(), header)
	require.NoError(t, err)
	require.Equal(t, stdcrpcauthfx.Claims{}, stdcrpcauthfx.ClaimsFromContext(ctx))
}

func TestExtractReturnsErrorOnUndecodablePayload(t *testing.T) {
	t.Parallel()

	prop := stdcrpcauthtemporalfx.New()

	// A payload with no metadata triggers a decode error in the
	// default JSON converter.
	header := newFakeHeader()
	header.Set("advdv.stdgo.stdcrpcauth.claims.v1", &commonpb.Payload{Data: []byte("not-json")})

	_, err := prop.Extract(t.Context(), header)
	require.Error(t, err)
}

func TestProvideWiresPropagator(t *testing.T) {
	t.Parallel()

	var prop *stdcrpcauthtemporalfx.Propagator

	app := fxtest.New(t,
		stdcrpcauthtemporalfx.Provide(),
		fx.Populate(&prop),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, prop)
}
