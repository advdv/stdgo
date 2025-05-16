// Package stdauthnfx provides web client authentication.
package stdauthnfx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/advdv/bhttp"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdfx"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/destel/rill"
	"github.com/go-playground/validator/v10"
	"github.com/gorilla/securecookie"
	"github.com/gorilla/sessions"
	"go.uber.org/fx"
	"golang.org/x/oauth2"
)

// per-provider environment configuration.
type providerConfig struct {
	// oauth client id
	ClientID string `env:"CLIENT_ID" validate:"required"`
	// oauth client provider
	ClientSecret string `env:"CLIENT_SECRET" validate:"required"`
	// issuer url
	Issuer string `env:"ISSUER"`
	// specify the issuer that is checked for off-spec provider such as Azure.
	OffSpecIssuer string `env:"OFF_SPEC_ISSUER"`
	// the scopes requested for this provider
	Scopes []string `env:"SCOPES" envDefault:"openid,email,profile"`
}

// Config configures the package's components.
type Config struct {
	// configure which social providers are enabled.
	EnabledProviders []string `env:"ENABLED_PROVIDERS"`
	// configure the exterior url clients will be re-directed back to.
	BaseCallbackURL string `env:"BASE_CALLBACK_URL,required"`
	// SessionKeyPairs configures the keys used for signing en encrypting the session cookies.
	SessionKeyPairs []stdenvcfg.HexBytes `env:"SESSION_KEY_PAIRS"`
	// the max age of the session cookie, in seconds. Defaults to a year.
	SessionDefaultMaxAgeSeconds int64 `env:"SESSION_DEFAULT_MAX_AGE_SECONDS" envDefault:"31556926"`

	// how long the session that keeps state between login and callback remains valid.
	StateMaxAgeSeconds int `env:"STATE_MAX_AGE_SECONDS" envDefault:"3600"`
	// name of the cookie used to keep the auth (flow) state from login to callback.
	StateCookieName string `env:"STATE_COOKIE_NAME" envDefault:"AUTHSTATE"`
	// name of the cookie used to keep the user's session between requests.
	SessionCookieName string `env:"SESSION_COOKIE_NAME" envDefault:"AUTHSESS"`
	// white list of hosts where the backend will redirect to.
	AllowedRedirectHosts []string `env:"ALLOWED_REDIRECT_HOSTS"`

	// configuration for each supported social provider.
	Google    providerConfig `envPrefix:"GOOGLE_"`
	LinkedIn  providerConfig `envPrefix:"LINKEDIN_"`
	Microsoft providerConfig `envPrefix:"MICROSOFT_"`
}

type (
	// Params into our component.
	Params struct {
		fx.In
		fx.Lifecycle
		Config
		Backend `optional:"true"`
	}

	// Result from our component.
	Result struct {
		fx.Out
		*Authentication
	}
)

// Authentication provides authentication of web clients.
type Authentication struct {
	cfg       Config
	providers map[string]*provider
	sessions  *sessions.CookieStore
	backend   Backend
	mu        sync.RWMutex
}

// newCookieStore inits the cookie store.
func newCookieStore(cfg Config) (*sessions.CookieStore, error) {
	pairs := make([][]byte, 0, len(cfg.SessionKeyPairs))
	for idx, v := range cfg.SessionKeyPairs {
		if len(v) < 32 {
			return nil, fmt.Errorf("session key pair value %d must be at least 32 bytes long, got: %d", idx, len(v))
		}

		pairs = append(pairs, []byte(v))
	}

	store := &sessions.CookieStore{
		Codecs: securecookie.CodecsFromPairs(pairs...),
		Options: &sessions.Options{
			Path:     "/",
			MaxAge:   int(cfg.SessionDefaultMaxAgeSeconds),
			SameSite: http.SameSiteLaxMode,
			Secure:   true,
			HttpOnly: true,
		},
	}

	if _, err := securecookie.EncodeMulti("test", "test", store.Codecs...); err != nil {
		return nil, fmt.Errorf("session cookie codec not setup correctly: %w", err)
	}

	return store, nil
}

// New inits the auth component.
func New(params Params) (res Result, err error) {
	auth := &Authentication{
		cfg:       params.Config,
		providers: map[string]*provider{},
		backend:   params.Backend,
	}

	if auth.backend == nil {
		auth.backend = &realBackend{}
	}

	auth.sessions, err = newCookieStore(params.Config)
	if err != nil {
		return res, fmt.Errorf("init cookie store: %w", err)
	}

	params.Lifecycle.Append(fx.Hook{OnStart: auth.start})
	return Result{Authentication: auth}, nil
}

// Login implements the start of the authentication flow.
func (a *Authentication) Login() (string, bhttp.HandlerFunc[context.Context]) {
	return "/auth/{provider}/login", func(_ context.Context, resp bhttp.ResponseWriter, req *http.Request) error {
		provider, err := a.getProvider(req)
		if err != nil {
			return err
		}

		redirectTo, err := a.validatedUserRedirect(req)
		if err != nil {
			return err
		}

		state, err := a.keepState(a.sessions, resp, req, redirectTo)
		if err != nil {
			return fmt.Errorf("keep state: %w", err)
		}

		redirectURL := provider.oauth.AuthCodeURL(state)
		http.Redirect(resp, req, redirectURL, http.StatusSeeOther)

		return nil
	}
}

// Callback implements the return of the client from the provider.
func (a *Authentication) Callback() (string, bhttp.HandlerFunc[context.Context]) {
	return "/auth/{provider}/callback", func(ctx context.Context, resp bhttp.ResponseWriter, req *http.Request) error {
		provider, err := a.getProvider(req)
		if err != nil {
			return err
		}

		code := req.URL.Query().Get("code")
		if code == "" {
			return bhttp.NewError(bhttp.CodeBadRequest, errors.New("code parameter not provided"))
		}

		redirectTo, err := a.verifyState(a.sessions, resp, req)
		if err != nil {
			return fmt.Errorf("verify state: %w", err)
		}

		identity, err := a.backend.AuthenticateCode(ctx, provider, code)
		if err != nil {
			return fmt.Errorf("authenticate code: %w", err)
		}

		if err := a.startSession(identity, resp, req); err != nil {
			return fmt.Errorf("start session: %w", err)
		}

		http.Redirect(resp, req, redirectTo.String(), http.StatusSeeOther)

		return nil
	}
}

func (a *Authentication) Logout() (string, bhttp.HandlerFunc[context.Context]) {
	return "/auth/logout", func(_ context.Context, resp bhttp.ResponseWriter, req *http.Request) error {
		if err := a.endSession(resp, req); err != nil {
			return fmt.Errorf("end session: %w", err)
		}

		redirectTo, err := a.validatedUserRedirect(req)
		if err != nil {
			return err
		}

		http.Redirect(resp, req, redirectTo.String(), http.StatusSeeOther)
		return nil
	}
}

func (a *Authentication) getProvider(req *http.Request) (prov provider, _ error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	providerName := req.PathValue("provider")
	provider, ok := a.providers[providerName]
	if !ok {
		return prov, bhttp.NewError(bhttp.CodeBadRequest, fmt.Errorf("no such provider: '%s'", providerName))
	}

	return *provider, nil
}

func (a *Authentication) start(ctx context.Context) (err error) {
	val := validator.New(validator.WithRequiredStructEnabled())

	if err = rill.ForEach(rill.FromSlice(a.cfg.EnabledProviders, nil), 4, func(providerName string) error {
		providerName = strings.ToLower(providerName)
		ctx := ctx
		var provider provider

		var envConfig providerConfig
		switch providerName {
		case ProviderKindGoogle.String():
			provider.kind = ProviderKindGoogle
			envConfig = a.cfg.Google
			if envConfig.Issuer == "" {
				envConfig.Issuer = "https://accounts.google.com"
			}

		case ProviderKindLinkedIn.String():
			provider.kind = ProviderKindLinkedIn
			envConfig = a.cfg.LinkedIn
			if envConfig.Issuer == "" {
				envConfig.Issuer = "https://www.linkedin.com/oauth"
			}

		case ProviderKindMicrosoft.String():
			provider.kind = ProviderKindMicrosoft
			envConfig = a.cfg.Microsoft
			if envConfig.Issuer == "" {
				envConfig.Issuer = "https://login.microsoftonline.com/common/v2.0"
			}

			// microsoft is mentioned in the official docs as an off-spec example so we will roll with that.
			// https://github.com/coreos/go-oidc/blob/a7c457eacb849c163a496b29274242474a8f44ab/oidc/oidc.go#L72
			if envConfig.OffSpecIssuer == "" {
				return fmt.Errorf("microsoft provider requires the OFF_SPEC_ISSUER configuration")
			}

			ctx = oidc.InsecureIssuerURLContext(ctx, envConfig.OffSpecIssuer)
		default:
			return fmt.Errorf("unsupported provider: %s", providerName)
		}

		if err := val.Struct(envConfig); err != nil {
			return fmt.Errorf("%s: invalid provider configuration: %w", providerName, err)
		}

		provider.oidc, err = oidc.NewProvider(ctx, envConfig.Issuer)
		if err != nil {
			return fmt.Errorf("%s: init oidc provider: %w", providerName, err)
		}

		provider.oauth = &oauth2.Config{
			ClientID:     envConfig.ClientID,
			ClientSecret: envConfig.ClientSecret,
			Scopes:       envConfig.Scopes,
			Endpoint:     provider.oidc.Endpoint(),
			RedirectURL:  fmt.Sprintf("%s/auth/%s/callback", a.cfg.BaseCallbackURL, providerName),
		}

		a.mu.Lock()
		a.providers[providerName] = &provider
		a.mu.Unlock()

		return nil
	}); err != nil {
		return fmt.Errorf("setup providers: %w", err)
	}

	return nil
}

// Provide the components.
func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdauthn", New)
}
