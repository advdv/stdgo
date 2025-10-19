package stdauthnfx

import (
	"context"

	stdauthnfxv1 "github.com/advdv/stdgo/fx/stdauthnfx/v1"
	"github.com/advdv/stdgo/stdctx"
	"github.com/cockroachdb/errors"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

// authenticateAccessToken verifies the access token the client got from Auth0.
func (ac *AccessControl) authenticateAccessToken(
	ctx context.Context, accessToken string,
) (context.Context, error) {
	set, err := ac.accessTokens.cache.Lookup(ctx, ac.config.TokenValidationJWKSEndpoint)
	if err != nil {
		return ctx, errors.Errorf("lookup jwks: %w", err)
	}

	token, err := jwt.Parse(
		[]byte(accessToken),
		jwt.WithAudience(ac.config.TokenAudience),
		jwt.WithIssuer(ac.config.TokenIssuer),
		jwt.WithClock(ac.accessTokens.clock),
		jwt.WithKeySet(set),
		jwt.WithVerify(true),
		jwt.WithValidate(true))
	if err != nil {
		return ctx, errors.Errorf("parse and validate: %w", err)
	}

	sub, ok := token.Subject()
	if !ok {
		return nil, errors.Errorf("no sub claim: %w", err)
	}

	stdctx.Log(ctx).Info("authenticated", zap.String("subject", sub))
	return WithWebUserAccess(ctx, ac.validator, stdauthnfxv1.AccessIdentity_builder{
		Subject: proto.String(sub),
	}.Build()), nil
}
