package stdauthnfx_test

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	"github.com/advdv/stdgo/fx/stdauthnfx"
	"github.com/advdv/stdgo/fx/stdauthnfx/insecureaccesstools"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestSetup(t *testing.T) {
	t.Parallel()
	var obs *observer.ObservedLogs
	ctx, ac := setup(t, nil, &obs)
	require.NotNil(t, ctx)
	require.NotNil(t, ac)
}

func setup(tb testing.TB, anonWhitelist []string, more ...any) (
	ctx context.Context,
	ac *stdauthnfx.AccessControl,
) {
	var deps struct {
		fx.In
		Logger        *zap.Logger
		AccessControl *stdauthnfx.AccessControl
	}

	env := map[string]string{
		// base64 encoded key for signing (well-known)
		"STDAUTHN_SIGNING_KEY_SET_BASE64": base64.StdEncoding.EncodeToString(insecureaccesstools.WellKnownJWKS1),
		"STDAUTHN_SIGNING_KEY_ID":         insecureaccesstools.WellKnownJWKS1KeyID,
		// the endpoint from which to fetch the jwks for validation access tokens.
		"STDAUTHN_TOKEN_VALIDATION_JWKS_ENDPOINT": "https://id-dev.sterndesk.com/.well-known/jwks.json",
		// the issuer for access tokens used in testing.
		"STDAUTHN_TOKEN_ISSUER": "https://id-dev.sterndesk.com/",
		// the audience for access tokens used in audience.
		"STDAUTHN_TOKEN_AUDIENCE": "basewarp-recode-api",
		// set a fixed wall clock as far as access control is concerned (for making test tokens forever usable).
		"STDAUTHN_FIXED_WALL_CLOCK_TIMESTAMP": "1760816072",
		// anonymous access whitelist
		"STDAUTHN_ANONYMOUS_ACCESS_WHITELIST": strings.Join(anonWhitelist, ","),
	}

	app := fxtest.New(tb,
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdauthnfx.Provide(),
		fx.Provide(protovalidate.New),
		fx.Populate(more...),
		fx.Populate(&deps))
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	ctx = tb.Context()
	ctx = stdctx.WithLogger(ctx, deps.Logger)

	return ctx, deps.AccessControl
}
