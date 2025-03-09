// Package stdcrpcaccess implements access control for our RPC.
package stdcrpcaccess

import (
	"context"
	"fmt"
	"net/http"
	"slices"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"github.com/advdv/stdgo/stdctx"
	"github.com/go-viper/mapstructure/v2"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/lestrrat-go/jwx/v2/jwt/openid"
	"go.uber.org/zap"
)

// Claims constrains the type that will hold authentication claims.
type Claims interface {
	ProcedurePermissions() []string
	DecorateContext(ctx context.Context) context.Context
}

// AccessControl implements a simple access control scheme.
type AccessControl[T Claims] struct {
	authn       *authn.Middleware
	jwkCache    *jwk.Cache
	jwkEndpoint string
	stop        func()
}

// New inits the access control.
func New[T Claims](jwkEndpoint string) *AccessControl[T] {
	ctx, cancel := context.WithCancel(context.Background())

	ac := &AccessControl[T]{stop: cancel}
	ac.authn = authn.NewMiddleware(ac.checkAuth)
	ac.jwkCache = jwk.NewCache(ctx)
	ac.jwkEndpoint = jwkEndpoint

	if err := ac.jwkCache.Register(jwkEndpoint); err != nil {
		panic("rpcaccess: failed to register jwk cache endpoint: " + err.Error())
	}

	return ac
}

// Close cancels the lifecycle context.
func (ac *AccessControl[T]) Close(context.Context) error { ac.stop(); return nil }

// checkAuth implements the core checkAuth logic.
func (ac *AccessControl[T]) checkAuth(ctx context.Context, req *http.Request) (any, error) {
	logs := stdctx.Log(ctx)
	accessToken, ok := authn.BearerToken(req)
	if !ok {
		return nil, authn.Errorf("no token")
	}

	logs.Info("authenticating token", zap.String("token", accessToken))

	keys, err := ac.jwkCache.Get(ctx, ac.jwkEndpoint)
	if err != nil {
		return nil, fmt.Errorf("unable to lookup JWKS: %w", err)
	}

	tok := openid.New()
	if _, err = jwt.ParseString(accessToken, jwt.WithKeySet(keys), jwt.WithToken(tok)); err != nil {
		logs.Info("client provided invalid token", zap.Error(err))
		return nil, authn.Errorf("invalid token")
	}

	var info T

	claimMap := tok.PrivateClaims()
	if err := mapstructure.Decode(claimMap, &info); err != nil {
		return nil, authn.Errorf("failed to decode claims: %w", err)
	}

	allowedProcedures := info.ProcedurePermissions()
	logs.Info("authorizing token",
		zap.Any("all_claims", claimMap),
		zap.Strings("allowed_procedures", allowedProcedures))

	currentProcedure, ok := authn.InferProcedure(req.URL)
	if !ok {
		return nil, authzErrorf("unable to determine RPC procedure")
	}

	if !slices.Contains(allowedProcedures, currentProcedure) {
		logs.Info("access to procedure denied",
			zap.String("current_procedure", currentProcedure),
			zap.Strings("allowed_procedures", allowedProcedures))

		return nil, authzErrorf("procedure not allowed")
	}

	return info, nil
}

func authzErrorf(format string, a ...any) error {
	return connect.NewError(connect.CodePermissionDenied, fmt.Errorf(format, a...))
}

func (ac *AccessControl[T]) Wrap(next http.Handler) http.Handler {
	// create a small middleware that transforms from the authn middleware value into our own type.
	inner := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, _ := authn.GetInfo(r.Context()).(T)
		ctx := info.DecorateContext(r.Context())
		next.ServeHTTP(w, r.WithContext(ctx))
	}))

	// the actual auth logic being performed.
	middle := ac.authn.Wrap(inner)

	// we normalize the header that come from our proxies into a bear token.
	outer := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// from the AWS Load Balancer  when deployed
		if amzAccessToken := r.Header.Get("X-Amzn-Oidc-Accesstoken"); amzAccessToken != "" {
			r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", amzAccessToken))
		}

		// from oauth2-proxy (local development)
		if xfwdAccessToken := r.Header.Get("X-Forwarded-Access-Token"); xfwdAccessToken != "" {
			r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", xfwdAccessToken))
		}

		middle.ServeHTTP(w, r)
	}))

	return outer
}
