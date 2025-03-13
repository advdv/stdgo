// Package stdcrpcaccess implements an access control layer for Connect RPC.
package stdcrpcaccess

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"github.com/advdv/stdgo/stdctx"
	"github.com/go-viper/mapstructure/v2"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"go.uber.org/zap"
)

// Logic defines the auth logic to implement in order to customize the auth process.
type Logic[T any] interface {
	// ProcedurePermissions is implemented to turn the claims into permissions for connect RPC procedure annotation.
	ProcedurePermissions(info T) []string
	// DecorateContext implements how auth information is stored in the context for the rest of the application to use.
	DecorateContext(ctx context.Context, info T) context.Context
	// ReadAccessToken allows the implementation to take information from the access token. This is called
	// AFTER private claims have been decoded from the access token.
	ReadAccessToken(ctx context.Context, info T, tok jwt.Token) (T, error)
	// ToAccessTokenBuilder turns the token into an jwt that can be completed and build by shared code.
	ToAccessTokenBuilder(ctx context.Context, info T) (*jwt.Builder, error)
	// AsAnonymous returns a new copy of the info that is usuable to the application for anonymous access. If false is
	// returned anonymous access is not allowed.
	AsAnonymous(ctx context.Context, req *http.Request) (T, bool)
	// PrivateClaimsDecodeTarget must return a pointer to the value that will be used as a decoding target for
	// private claims.
	PrivateClaimsDecodeTarget(info *T) any
}

// AccessControl implements a simple access control scheme.
type AccessControl[T any] struct {
	logic    Logic[T]
	authn    *authn.Middleware
	audience string
	issuers  struct {
		backend string
		signing string
	}
	backend struct {
		jwkCache    *jwk.Cache
		jwkEndpoint string
	}
	extraValidators []jwt.Validator
	signing         jwk.Set
	stop            func()
}

// New inits the access control.
func New[T any](
	logic Logic[T],
	back AuthBackend,
	signing jwk.Set,
	audience string,
	authBackendIssuer, signingIssuer string,
	extraValidators []jwt.Validator,
) *AccessControl[T] {
	ctx, cancel := context.WithCancel(context.Background())
	act := &AccessControl[T]{
		logic:           logic,
		stop:            cancel,
		signing:         signing,
		audience:        audience,
		extraValidators: extraValidators,
	}

	act.authn = authn.NewMiddleware(act.checkAuthN)
	act.backend.jwkCache = jwk.NewCache(ctx)
	act.backend.jwkEndpoint = back.JWKSEndpoint()

	act.issuers.backend = authBackendIssuer
	act.issuers.signing = signingIssuer

	if err := act.backend.jwkCache.Register(act.backend.jwkEndpoint); err != nil {
		panic("rpcaccess: failed to register jwk cache endpoint: " + err.Error())
	}

	return act
}

// SignAccessToken turns auth information T into an access token that is accepted by auth checks. The audience
// claim is overwritten with what is configured for this access control instance.
func (ac *AccessControl[T]) SignAccessToken(
	ctx context.Context,
	info T,
	signingKeyID string,
	buildFn ...func(*jwt.Builder) *jwt.Builder,
) ([]byte, error) {
	bldr, err := ac.logic.ToAccessTokenBuilder(ctx, info)
	if err != nil {
		return nil, fmt.Errorf("turn into access token builder: %w", err)
	}

	bldr = bldr.
		IssuedAt(time.Now()).
		NotBefore(time.Now().Add(-time.Second * 10)).
		Audience([]string{ac.audience}).
		Issuer(ac.issuers.signing)
	if len(buildFn) > 0 {
		bldr = buildFn[0](bldr)
	}

	tok, err := bldr.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build access token: %w", err)
	}

	key, ok := ac.signing.LookupKeyID(signingKeyID)
	if !ok {
		return nil, fmt.Errorf("no key with id '%s'", signingKeyID)
	}

	signed, err := jwt.Sign(tok, jwt.WithKey(key.Algorithm(), key))
	if err != nil {
		return nil, fmt.Errorf("signing: %w", err)
	}

	return signed, nil
}

// Close cancels the lifecycle context.
func (ac *AccessControl[T]) Close(context.Context) error { ac.stop(); return nil }

// checkAuthN implements the core checkAuthN logic.
func (ac *AccessControl[T]) checkAuthN(ctx context.Context, req *http.Request) (any, error) {
	logs := stdctx.Log(ctx)
	accessToken, ok := authn.BearerToken(req)
	if !ok {
		info, allow := ac.logic.AsAnonymous(ctx, req)
		if !allow {
			return nil, authn.Errorf("no token")
		}

		allowedProcedures := ac.logic.ProcedurePermissions(info)
		logs.Info("authorizing anonymous access", zap.Strings("allowed_procedures", allowedProcedures))

		return info, ac.checkAuthZ(logs, allowedProcedures, req)
	}

	logs.Info("authenticating token", zap.String("token", accessToken))

	keys, err := ac.backend.jwkCache.Get(ctx, ac.backend.jwkEndpoint)
	if err != nil {
		return nil, fmt.Errorf("unable to lookup JWKS: %w", err)
	}

	allowedIssuers := []string{ac.issuers.signing, ac.issuers.backend}
	opts := []jwt.ParseOption{
		jwt.WithAudience(ac.audience), // check that we are the intended audience.
		jwt.WithKeySet(keys),          // from our auth backend
		jwt.WithKeySet(ac.signing),    // from our own signing set
		jwt.WithValidate(true),        // on by default, but let's be explicit about this

		// custom, require ONE OF validator for issuer
		jwt.WithValidator(jwt.ValidatorFunc(func(_ context.Context, t jwt.Token) jwt.ValidationError {
			if slices.Contains(allowedIssuers, t.Issuer()) {
				return nil
			}

			return jwt.ErrInvalidIssuer()
		})),
	}

	// instance of access control might have custom validators.
	for _, val := range ac.extraValidators {
		opts = append(opts, jwt.WithValidator(val))
	}

	tok, err := jwt.ParseString(accessToken, opts...)
	if err != nil {
		logs.Info("client provided invalid token", zap.Error(err))
		return nil, authn.Errorf("invalid token")
	}

	var info T

	claimMap := tok.PrivateClaims()
	if err := mapstructure.Decode(claimMap, ac.logic.PrivateClaimsDecodeTarget(&info)); err != nil {
		return nil, authn.Errorf("failed to decode claims: %w", err)
	}

	info, err = ac.logic.ReadAccessToken(ctx, info, tok)
	if err != nil {
		return nil, authn.Errorf("read access token into auth info: %w", err)
	}

	allowedProcedures := ac.logic.ProcedurePermissions(info)
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

func (ac *AccessControl[T]) Wrap(next http.Handler) http.Handler {
	// create a small middleware that transforms from the authn middleware value into our own type.
	inner := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, _ := authn.GetInfo(r.Context()).(T)
		ctx := ac.logic.DecorateContext(r.Context(), info)
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

func authzErrorf(format string, a ...any) error {
	return connect.NewError(connect.CodePermissionDenied, fmt.Errorf(format, a...))
}
