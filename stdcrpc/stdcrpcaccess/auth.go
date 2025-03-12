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
	"go.uber.org/zap"
)

// Claims constrains the type that will hold authentication claims.
type Claims[T any] interface {
	// ProcedurePermissions is implemented to turn the claims into permissions for connect RPC procedure annotation.
	ProcedurePermissions() []string
	// ReadAccessToken allows the implementation to take information from the access token. This is called
	// AFTER custom claims have been read from the access token.
	ReadAccessToken(ctx context.Context, tok jwt.Token) (T, error)
	// DecorateContext implements how auth information is stored in the context for the rest of the application to use.
	DecorateContext(ctx context.Context) context.Context
	// AsAnonymous returns a copy of the info that is usuable to the application for anonymous access. If false is
	// returned anonymous access is not allowed.
	AsAnonymous(ctx context.Context, req *http.Request) (T, bool)
}

// AccessControl implements a simple access control scheme.
type AccessControl[T Claims[T]] struct {
	authn       *authn.Middleware
	jwkCache    *jwk.Cache
	jwkEndpoint string
	stop        func()
}

// New inits the access control.
func New[T Claims[T]](back AuthBackend) *AccessControl[T] {
	ctx, cancel := context.WithCancel(context.Background())

	ac := &AccessControl[T]{stop: cancel}
	ac.authn = authn.NewMiddleware(ac.checkAuthN)
	ac.jwkCache = jwk.NewCache(ctx)
	ac.jwkEndpoint = back.JWKSEndpoint()

	if err := ac.jwkCache.Register(ac.jwkEndpoint); err != nil {
		panic("rpcaccess: failed to register jwk cache endpoint: " + err.Error())
	}

	return ac
}

// Close cancels the lifecycle context.
func (ac *AccessControl[T]) Close(context.Context) error { ac.stop(); return nil }

// checkAuthN implements the core checkAuthN logic.
func (ac *AccessControl[T]) checkAuthN(ctx context.Context, req *http.Request) (any, error) {
	var info T

	logs := stdctx.Log(ctx)
	accessToken, ok := authn.BearerToken(req)
	if !ok {
		info, allow := info.AsAnonymous(ctx, req)
		if !allow {
			return nil, authn.Errorf("no token")
		}

		allowedProcedures := info.ProcedurePermissions()
		logs.Info("authorizing anonymous access", zap.Strings("allowed_procedures", allowedProcedures))

		return info, ac.checkAuthZ(logs, allowedProcedures, req)
	}

	logs.Info("authenticating token", zap.String("token", accessToken))

	keys, err := ac.jwkCache.Get(ctx, ac.jwkEndpoint)
	if err != nil {
		return nil, fmt.Errorf("unable to lookup JWKS: %w", err)
	}

	tok := jwt.New()
	if _, err = jwt.ParseString(accessToken, jwt.WithKeySet(keys), jwt.WithToken(tok)); err != nil {
		logs.Info("client provided invalid token", zap.Error(err))
		return nil, authn.Errorf("invalid token")
	}

	claimMap := tok.PrivateClaims()
	if err := mapstructure.Decode(claimMap, &info); err != nil {
		return nil, authn.Errorf("failed to decode claims: %w", err)
	}

	info, err = info.ReadAccessToken(ctx, tok)
	if err != nil {
		return nil, authn.Errorf("read access token into auth info: %w", err)
	}

	allowedProcedures := info.ProcedurePermissions()
	logs.Info("authorizing token",
		zap.String("subject", tok.Subject()),
		zap.Any("all_claims", claimMap),
		zap.Strings("allowed_procedures", allowedProcedures))

	return info, ac.checkAuthZ(logs, allowedProcedures, req)
}

// checkAuthZ implements our simple authorization logic.
func (ac *AccessControl[T]) checkAuthZ(
	logs *zap.Logger,
	allowedProcedures []string,
	req *http.Request,
) error {
	currentProcedure, ok := authn.InferProcedure(req.URL)
	if !ok {
		return authzErrorf("unable to determine RPC procedure")
	}

	if !slices.Contains(allowedProcedures, currentProcedure) {
		logs.Info("access to procedure denied",
			zap.String("current_procedure", currentProcedure),
			zap.Strings("allowed_procedures", allowedProcedures))

		return authzErrorf("procedure not allowed")
	}

	return nil
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
