package crpcauthtesting

import (
	"context"
	"testing"

	"connectrpc.com/connect"
)

// CallAs invokes a typed Connect RPC method as the caller identified by
// subject/scopes/tenantID.
//
// The three identity parameters mirror stdcrpcauthfx.Claims so the test-side
// abstraction stays in lockstep with the production middleware: subject
// becomes the JWT "sub" claim, scopes are space-joined into the "scope"
// claim, and tenantID (when non-empty) is written under the JWT path the
// signer was configured with at construction time. Passing a non-empty
// tenantID to a signer without a configured tenant claim path is a test
// failure (see TokenSigner.SignClaims).
//
// The supplied method is typically a method value of a generated Connect
// client (e.g. client.WhoAmI), which keeps the helper free of any per-service
// knowledge.
//
// CallAs is deliberately authenticated-only: tests for the unauthenticated
// path should call the client method directly so the absence of a token is
// explicit at the call site. Tests that need to mint tokens with claim shapes
// outside the Claims model (unknown claim paths, malformed payloads, etc.)
// should call the lower-level TokenSigner methods (Sign, SignWithClaims,
// SignWithPermissions, …) and decorate their own connect.Request.
func CallAs[Req, Resp any](
	ctx context.Context,
	tb testing.TB,
	signer *TokenSigner,
	subject string,
	scopes []string,
	tenantID string,
	method func(context.Context, *connect.Request[Req]) (*connect.Response[Resp], error),
	msg *Req,
) (*connect.Response[Resp], error) {
	tb.Helper()

	token := signer.SignClaims(tb, subject, scopes, tenantID)

	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+token)

	return method(ctx, req)
}
