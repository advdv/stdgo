package stdauthnfx_test

import (
	"strings"
	"testing"

	"github.com/advdv/stdgo/fx/stdauthnfx"
	"github.com/advdv/stdgo/fx/stdauthnfx/insecureaccesstools"
	stdauthnfxv1 "github.com/advdv/stdgo/fx/stdauthnfx/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestAuthenticateAccessToken(t *testing.T) {
	t.Parallel()
	ctx, ac := setup(t, nil)

	ctx, err := ac.Authenticate(ctx, "/acme.foo.v1.FooService/Bar", "Bearer "+insecureaccesstools.TestAccessToken3)
	require.NoError(t, err)

	acc1 := stdauthnfx.FromContext(ctx)
	require.False(t, acc1.GetIsSystem())
	require.False(t, acc1.GetIsAnonymous())
	require.Equal(t, "google-oauth2|114814749289287160219",
		acc1.GetWebuserIdentity().GetSubject())

	fp1, ok := stdauthnfx.APIKeyFingerprint(ctx)
	require.Empty(t, fp1)
	require.False(t, ok)
}

func TestAuthenticateAPIKey(t *testing.T) {
	t.Parallel()
	ctx, ac := setup(t, nil)

	key1, err := ac.BuildAndSignAPIKey(stdauthnfxv1.Access_builder{
		IsSystem: proto.Bool(true),
	}.Build())
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(key1, stdauthnfx.APIKeyPrefix))

	ctx, err = ac.Authenticate(ctx, "/acme.foo.v1.FooService/Bar", "Bearer "+key1)
	require.NoError(t, err)

	acc1 := stdauthnfx.FromContext(ctx)
	require.True(t, acc1.GetIsSystem())
	require.False(t, acc1.GetIsAnonymous())
	require.Nil(t, acc1.GetWebuserIdentity())

	// in case of api key authentication we might want
	fp1, ok := stdauthnfx.APIKeyFingerprint(ctx)
	require.NotEmpty(t, fp1)
	require.True(t, ok)
}

func TestAnonymousNoHeader(t *testing.T) {
	t.Parallel()
	ctx, ac := setup(t, nil)

	ctx, err := ac.Authenticate(ctx, "/acme.foo.v1.FooService/Bar", "")
	require.ErrorContains(t, err, "no authorization header")
}

func TestAnonymous(t *testing.T) {
	t.Parallel()
	ctx, ac := setup(t, []string{"/acme.foo.v1.FooService/Bar"})

	ctx, err := ac.Authenticate(ctx, "/acme.foo.v1.FooService/Bar", "")
	require.NoError(t, err)

	acc1 := stdauthnfx.FromContext(ctx)
	require.False(t, acc1.GetIsSystem())
	require.True(t, acc1.GetIsAnonymous())
	require.Nil(t, acc1.GetWebuserIdentity())
}

func TestAnonymousWildcard(t *testing.T) {
	t.Parallel()
	ctx, ac := setup(t, []string{"/*/*"})

	ctx, err := ac.Authenticate(ctx, "/acme.foo.v1.FooService/Bar", "")
	require.NoError(t, err)

	acc1 := stdauthnfx.FromContext(ctx)
	require.False(t, acc1.GetIsSystem())
	require.True(t, acc1.GetIsAnonymous())
	require.Nil(t, acc1.GetWebuserIdentity())
}
