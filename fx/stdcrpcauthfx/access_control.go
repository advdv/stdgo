// Package stdcrpcauthfx provides ConnectRPC authentication and authorization via OIDC/JWKS.
package stdcrpcauthfx

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"connectrpc.com/authn"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/cockroachdb/errors"
	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Config holds the OIDC configuration read from environment variables.
type Config struct {
	TokenIssuer   string `env:"TOKEN_ISSUER,required"`
	TokenAudience string `env:"TOKEN_AUDIENCE,required"`
}

// Claims holds the authentication information extracted from a JWT.
type Claims struct {
	Subject string
	Scopes  []string
}

// ClaimsFromContext retrieves the claims stored by the auth middleware.
func ClaimsFromContext(ctx context.Context) Claims {
	claims, _ := authn.GetInfo(ctx).(Claims)

	return claims
}

// ScopeResolver resolves the required scope for a ConnectRPC procedure.
type ScopeResolver interface {
	RequiredScope(procedure string) (string, error)
}

// protoExtensionScopeResolver resolves scopes by reading a string-typed protobuf
// method option extension and combining it with the lowercased service name.
type protoExtensionScopeResolver struct {
	ext protoreflect.ExtensionType
}

func (r *protoExtensionScopeResolver) RequiredScope(procedure string) (string, error) {
	fullName := strings.TrimPrefix(procedure, "/")
	fullName = strings.Replace(fullName, "/", ".", 1)

	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(fullName))
	if err != nil {
		return "", errors.Wrapf(err, "find descriptor for %q", fullName)
	}

	methodDesc, ok := desc.(protoreflect.MethodDescriptor)
	if !ok {
		return "", errors.Newf("%q is not a method descriptor", fullName)
	}

	opts, ok := methodDesc.Options().(*descriptorpb.MethodOptions)
	if !ok || opts == nil {
		return "", errors.Newf("method %q has no options", fullName)
	}

	if !proto.HasExtension(opts, r.ext) {
		return "", errors.Newf("method %q is missing required scope extension", fullName)
	}

	permission, ok := proto.GetExtension(opts, r.ext).(string)
	if !ok {
		return "", errors.Newf("method %q: scope extension is not a string", fullName)
	}

	name := string(methodDesc.Parent().Name())
	name = strings.TrimSuffix(name, "Service")

	return strings.ToLower(name) + ":" + permission, nil
}

// ProtoExtensionScope returns an fx.Option that provides a ScopeResolver backed
// by the given protobuf method option extension type.
func ProtoExtensionScope(ext protoreflect.ExtensionType) fx.Option {
	return fx.Provide(func() ScopeResolver {
		return &protoExtensionScopeResolver{ext: ext}
	})
}

// AccessControl holds all auth state: JWKS cache, config, and the authn middleware.
type AccessControl struct {
	config        Config
	logs          *zap.Logger
	cancel        context.CancelFunc
	clock         jwt.Clock
	scopeResolver ScopeResolver

	tokens struct {
		set jwk.CachedSet
	}

	middleware *authn.Middleware
}

// Wrap returns an HTTP handler that authenticates and authorizes requests.
func (ac *AccessControl) Wrap(handler http.Handler) http.Handler {
	return ac.middleware.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		procedure, ok := authn.InferProcedure(r.URL)
		if !ok {
			handler.ServeHTTP(w, r)

			return
		}

		requiredScope, err := ac.scopeResolver.RequiredScope(procedure)
		if err != nil {
			ac.logs.Error("failed to look up required scope", zap.String("procedure", procedure), zap.Error(err))
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		claims := ClaimsFromContext(r.Context())
		if !slices.Contains(claims.Scopes, requiredScope) {
			ac.logs.Warn("insufficient scope",
				zap.String("procedure", procedure),
				zap.String("required", requiredScope),
				zap.Strings("provided", claims.Scopes))
			http.Error(w, "insufficient scope", http.StatusForbidden)

			return
		}

		handler.ServeHTTP(w, r)
	}))
}

// Start initializes the JWKS cache and fetches the initial key set.
func (ac *AccessControl) Start(ctx context.Context) (err error) {
	jwksURL := strings.TrimSuffix(ac.config.TokenIssuer, "/") + "/.well-known/jwks.json"

	cacheCtx, cancel := context.WithCancel(context.Background())

	defer func() {
		if err != nil {
			cancel()
		}
	}()

	//nolint:contextcheck // cacheCtx is intentionally detached from the start context
	cache, err := jwk.NewCache(cacheCtx, httprc.NewClient())
	if err != nil {
		return errors.Wrap(err, "create JWKS cache")
	}

	err = cache.Register(ctx, jwksURL)
	if err != nil {
		return errors.Wrapf(err, "register JWKS URL %q", jwksURL)
	}

	resource, err := cache.LookupResource(ctx, jwksURL)
	if err != nil {
		return errors.Wrapf(err, "lookup JWKS resource %q", jwksURL)
	}

	ac.tokens.set = jwk.NewCachedSet(resource)
	ac.cancel = cancel

	ac.logs.Info("JWKS cache initialized",
		zap.String("jwks_url", jwksURL),
		zap.String("issuer", ac.config.TokenIssuer),
		zap.String("audience", ac.config.TokenAudience))

	return nil
}

// Stop cancels the JWKS cache background refresh.
func (ac *AccessControl) Stop(_ context.Context) error {
	ac.cancel()

	return nil
}

// authenticate is the authn.AuthFunc that verifies the JWT and extracts claims.
func (ac *AccessControl) authenticate(_ context.Context, req *http.Request) (any, error) {
	token, ok := authn.BearerToken(req)
	if !ok {
		return nil, authn.Errorf("missing bearer token")
	}

	tok, err := jwt.Parse([]byte(token),
		jwt.WithKeySet(ac.tokens.set),
		jwt.WithIssuer(ac.config.TokenIssuer),
		jwt.WithAudience(ac.config.TokenAudience),
		jwt.WithClock(ac.clock),
	)
	if err != nil {
		ac.logs.Warn("JWT validation failed", zap.Error(err))

		return nil, authn.Errorf("invalid token: %v", err)
	}

	var scopeStr string

	_ = tok.Get("scope", &scopeStr)

	scopes := strings.Fields(scopeStr)

	var rawPermissions interface{}
	if err := tok.Get("permissions", &rawPermissions); err == nil {
		if perms, ok := rawPermissions.([]interface{}); ok {
			for _, p := range perms {
				if s, ok := p.(string); ok {
					scopes = append(scopes, s)
				}
			}
		}
	}
	sub, _ := tok.Subject()

	ac.logs.Info("authenticated request",
		zap.String("subject", sub),
		zap.Strings("scopes", scopes))

	return Claims{Subject: sub, Scopes: scopes}, nil
}

// Params holds the dependencies for constructing AccessControl.
type Params struct {
	fx.In
	fx.Lifecycle

	Logs          *zap.Logger
	Config        Config
	ScopeResolver ScopeResolver
	Clock         jwt.Clock `optional:"true"`
}

// Result holds the components produced by this module.
type Result struct {
	fx.Out

	AccessControl *AccessControl
}

// New constructs a new AccessControl and registers its lifecycle hooks.
func New(params Params) (Result, error) {
	clock := params.Clock
	if clock == nil {
		clock = jwt.ClockFunc(time.Now)
	}

	accessControl := &AccessControl{
		config:        params.Config,
		logs:          params.Logs,
		clock:         clock,
		scopeResolver: params.ScopeResolver,
	}
	accessControl.middleware = authn.NewMiddleware(accessControl.authenticate)

	params.Append(fx.Hook{
		OnStart: accessControl.Start,
		OnStop:  accessControl.Stop,
	})

	return Result{AccessControl: accessControl}, nil
}

// Provide returns an fx.Option that wires the stdauth module with config from the environment.
func Provide() fx.Option {
	return fx.Module("stdcrpcauth",
		fx.Decorate(func(l *zap.Logger) *zap.Logger { return l.Named("stdcrpcauth") }),
		stdenvcfg.Provide[Config](),
		fx.Provide(New),
	)
}
