package crpcauthtesting_test

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/advdv/stdgo/fx/stdcrpcauthfx/crpcauthtesting"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/stretchr/testify/require"
)

// fakeReq/fakeResp are minimal stand-ins for generated protobuf types — the
// helper is purely generic so any Req/Resp pair works.
type (
	fakeReq  struct{ Echo string }
	fakeResp struct{ Echo string }
)

type ctxMarker struct{}

func TestCallAsSetsBearerHeaderAndInvokesMethod(t *testing.T) {
	t.Parallel()

	_, signer := crpcauthtesting.NewJWKSServer(t, "")

	var (
		gotMarker any
		gotHeader string
		gotMsg    *fakeReq
	)

	method := func(ctx context.Context, req *connect.Request[fakeReq]) (*connect.Response[fakeResp], error) {
		gotMarker = ctx.Value(ctxMarker{})
		gotHeader = req.Header().Get("Authorization")
		gotMsg = req.Msg
		return connect.NewResponse(&fakeResp{Echo: req.Msg.Echo}), nil
	}

	ctx := context.WithValue(t.Context(), ctxMarker{}, "marker-value")

	resp, err := crpcauthtesting.CallAs(ctx, t, signer,
		"test-user", []string{"system:read"}, "",
		method, &fakeReq{Echo: "hello"})
	require.NoError(t, err)

	require.Equal(t, "marker-value", gotMarker)
	require.Equal(t, "hello", gotMsg.Echo)
	require.Equal(t, "hello", resp.Msg.Echo)

	// header is "Bearer <jwt>"; verify the prefix and that the JWT decodes
	// with the expected subject and scope claim.
	require.True(t, strings.HasPrefix(gotHeader, "Bearer "), "got %q", gotHeader)

	tok, err := jwt.ParseInsecure([]byte(strings.TrimPrefix(gotHeader, "Bearer ")))
	require.NoError(t, err)

	var sub string
	require.NoError(t, tok.Get("sub", &sub))
	require.Equal(t, "test-user", sub)

	var scope string
	require.NoError(t, tok.Get("scope", &scope))
	require.Equal(t, "system:read", scope)
}

func TestCallAsTenantIDIsWrittenAtConfiguredClaimPath(t *testing.T) {
	t.Parallel()

	const tenantClaimPath = "https://test.example/org_id"
	_, signer := crpcauthtesting.NewJWKSServer(t, tenantClaimPath)

	var gotHeader string
	method := func(_ context.Context, req *connect.Request[fakeReq]) (*connect.Response[fakeResp], error) {
		gotHeader = req.Header().Get("Authorization")
		return connect.NewResponse(&fakeResp{}), nil
	}

	_, err := crpcauthtesting.CallAs(t.Context(), t, signer,
		"test-user",
		[]string{"system:read", "system:write"},
		"org_TEST",
		method, &fakeReq{})
	require.NoError(t, err)

	tok, err := jwt.ParseInsecure([]byte(strings.TrimPrefix(gotHeader, "Bearer ")))
	require.NoError(t, err)

	// scopes are space-joined.
	var scope string
	require.NoError(t, tok.Get("scope", &scope))
	require.Equal(t, "system:read system:write", scope)

	// tenantID lands at the path the signer was constructed with.
	var orgID string
	require.NoError(t, tok.Get(tenantClaimPath, &orgID))
	require.Equal(t, "org_TEST", orgID)
}

func TestCallAsEmptyTenantIDIsOmitted(t *testing.T) {
	t.Parallel()

	const tenantClaimPath = "https://test.example/org_id"
	_, signer := crpcauthtesting.NewJWKSServer(t, tenantClaimPath)

	var gotHeader string
	method := func(_ context.Context, req *connect.Request[fakeReq]) (*connect.Response[fakeResp], error) {
		gotHeader = req.Header().Get("Authorization")
		return connect.NewResponse(&fakeResp{}), nil
	}

	_, err := crpcauthtesting.CallAs(t.Context(), t, signer,
		"test-user", []string{"system:read"}, "",
		method, &fakeReq{})
	require.NoError(t, err)

	tok, err := jwt.ParseInsecure([]byte(strings.TrimPrefix(gotHeader, "Bearer ")))
	require.NoError(t, err)

	// when tenantID is empty, the tenant claim path must not appear at all so
	// the production middleware leaves Claims.TenantID empty.
	var got string
	require.Error(t, tok.Get(tenantClaimPath, &got),
		"tenant claim must be omitted when tenantID is empty")
}

func TestCallAsPropagatesMethodError(t *testing.T) {
	t.Parallel()

	_, signer := crpcauthtesting.NewJWKSServer(t, "")

	wantErr := connect.NewError(connect.CodePermissionDenied, stringError("nope"))

	method := func(_ context.Context, _ *connect.Request[fakeReq]) (*connect.Response[fakeResp], error) {
		return nil, wantErr
	}

	resp, err := crpcauthtesting.CallAs(t.Context(), t, signer,
		"test-user", []string{"system:read"}, "",
		method, &fakeReq{})
	require.Nil(t, resp)
	require.ErrorIs(t, err, wantErr)
}

// stringError is a tiny error type so we can build connect errors without
// pulling in errors.New just for the test.
type stringError string

func (e stringError) Error() string { return string(e) }
