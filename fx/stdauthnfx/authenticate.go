package stdauthnfx

import (
	"context"
	"strings"

	"github.com/advdv/stdgo/stdctx"
	"github.com/cockroachdb/errors"
	"go.uber.org/zap"
)

// Authenticate a HTTP authorization header value. If an empty string is passed, it is considered as not set
// and the "anonymous" access behavior is triggered.
func (ac *AccessControl) Authenticate(ctx context.Context, authzHeader string) (context.Context, error) {
	logs := stdctx.Log(ctx)
	if authzHeader == "" {
		logs.Info("anonymous: no authorization header")
		return WithAnonymousAccess(ctx, ac.validator), nil
	}

	bearer, ok := bearerToken(authzHeader)
	if !ok {
		return ctx, errors.Errorf("invalid authorization header: %s", authzHeader)
	}

	return ac.authenticate(ctx, bearer)
}

// authenticate a bearer token.
func (ac *AccessControl) authenticate(ctx context.Context, bearer string) (_ context.Context, err error) {
	logs := stdctx.Log(ctx)
	if strings.HasPrefix(bearer, APIKeyPrefix) {
		logs.Info("api key authentication", zap.String("bearer", bearer))
		ctx, err = ac.authenticateAPIKey(ctx, bearer)
	} else {
		logs.Info("access token authentication", zap.String("bearer", bearer))
		ctx, err = ac.authenticateAccessToken(ctx, bearer)
	}

	if err != nil {
		logs.Info("failed to authenticate", zap.Error(err))
	} else {
		logs.Info("authenticated", zap.Any("access", FromContext(ctx)))
	}

	return ctx, err
}

// bearerToken returns the bearer token provided in the request's Authorization
// header, if any.
func bearerToken(auth string) (string, bool) {
	const prefix = "Bearer "
	// Case insensitive prefix match. See RFC 9110 Section 11.1.
	if len(auth) < len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return "", false
	}

	return auth[len(prefix):], true
}
