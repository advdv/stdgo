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
	"github.com/advdv/stdgo/stdlo"
	"github.com/go-viper/mapstructure/v2"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/lestrrat-go/jwx/v2/jwt/openid"
	"go.uber.org/zap"
)

type ctxKey string

// PermissionsFromContext returns permissions from the context.
func PermissionsFromContext(ctx context.Context) []string {
	val, ok := ctx.Value(ctxKey("procedure_permissions")).([]string)
	if !ok {
		panic("stdcrpcaccess: no procedure permissions in context")
	}

	return val
}

// WithProcedurePermissions returns a context with permission strings.
func WithProcedurePermissions(ctx context.Context, procs []string) context.Context {
	return context.WithValue(ctx, ctxKey("procedure_permissions"), procs)
}

// RoleFromContext returns permissions from the context.
func RoleFromContext(ctx context.Context) string {
	val, ok := ctx.Value(ctxKey("role")).(string)
	if !ok {
		panic("stdcrpcaccess: no role in context")
	}

	return val
}

// WithRole returns a context with the role added to it.
func WithRole(ctx context.Context, role string) context.Context {
	return context.WithValue(ctx, ctxKey("role"), role)
}

// PermissionToProcedure is used for an authorization scheme were some permission string is compared to
// a procedure name.
type PermissionToProcedure func(perm string, _ int) string

// AccessControl implements a simple access control scheme.
type AccessControl struct {
	authn       *authn.Middleware
	jwkCache    *jwk.Cache
	jwkEndpoint string
	permMapFn   PermissionToProcedure
	stop        func()
}

// New inits the access control.
func New(jwkEndpoint string, permMapFn PermissionToProcedure) *AccessControl {
	ctx, cancel := context.WithCancel(context.Background())

	ac := &AccessControl{stop: cancel}
	ac.authn = authn.NewMiddleware(ac.checkAuth)
	ac.jwkCache = jwk.NewCache(ctx)
	ac.jwkEndpoint = jwkEndpoint
	ac.permMapFn = permMapFn

	if err := ac.jwkCache.Register(jwkEndpoint); err != nil {
		panic("rpcaccess: failed to register jwk cache endpoint: " + err.Error())
	}

	return ac
}

// Close cancels the lifecycle context.
func (ac *AccessControl) Close(context.Context) error { ac.stop(); return nil }

// authInfo describes what is passed between middlewares as result of authentication.
type authInfo struct {
	Role        string
	Permissions []string
}

// checkAuth implements the core checkAuth logic.
func (ac *AccessControl) checkAuth(ctx context.Context, req *http.Request) (any, error) {
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

	claimMap, info := tok.PrivateClaims(), authInfo{}
	if err := mapstructure.Decode(claimMap, &info); err != nil {
		return nil, authn.Errorf("failed to decode claims: %w", err)
	}

	info.Permissions = stdlo.Map(info.Permissions, ac.permMapFn)

	logs.Info("authorizing token",
		zap.String("role", info.Role),
		zap.Any("all_claims", claimMap),
		zap.Strings("allowed_procedures", info.Permissions))

	currentProcedure, ok := authn.InferProcedure(req.URL)
	if !ok {
		return nil, authzErrorf("unable to determine RPC procedure")
	}

	if !slices.Contains(info.Permissions, currentProcedure) {
		logs.Info("access to procedure denied",
			zap.String("current_procedure", currentProcedure),
			zap.Strings("allowed_procedures", info.Permissions))

		return nil, authzErrorf("procedure not allowed")
	}

	return info, nil
}

func authzErrorf(format string, a ...any) error {
	return connect.NewError(connect.CodePermissionDenied, fmt.Errorf(format, a...))
}

func (ac *AccessControl) Wrap(next http.Handler) http.Handler {
	// create a small middleware that transforms from the authn middleware value into our own type.
	inner := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, _ := authn.GetInfo(r.Context()).(authInfo)
		ctx := r.Context()
		ctx = WithProcedurePermissions(ctx, info.Permissions)
		ctx = WithRole(ctx, info.Role)
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
