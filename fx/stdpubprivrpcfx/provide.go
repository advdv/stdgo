package stdpubprivrpcfx

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"

	"buf.build/go/protovalidate"
	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/advdv/bhttp"
	"github.com/advdv/stdgo/stdcrpc/stdcrpcintercept"
	"github.com/advdv/stdgo/stdfx"
	"github.com/advdv/stdgo/stdhttpware"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/rs/cors"
	"go.akshayshah.org/memhttp"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type Config struct {
	// allow health endpoint to panic, for testing purposes.
	AllowForcedPanics bool `env:"ALLOW_FORCED_PANICS"`
	// response validation can be enabled in testing to catch errors early.
	ResponseValidation bool `env:"RESPONSE_VALIDATION"`
	// cache the pre-flight response more readily, it is not dynamic.
	ConnectCORSMaxAgeSeconds int `env:"CONNECT_CORS_MAX_AGE_SECONDS" envDefault:"3600"`
	// allow configuration of CORS allowed origins
	ConnectCORSAllowedOrigins []string `env:"CONNECT_CORS_ALLOWED_ORIGINS"`
	// for making the hosted openapi spec fully descriptive, the environmnet must specify how to reach it externally.
	OpenAPIExternalBaseURL *url.URL `env:"OPENAPI_EXTERNAL_BASE_URL"`

	// configuration set via a depdency.
	basePath RPCBasePath
}

func New[PUBRO, PUBRW, PRIVRW, PRIVRWC any](deps struct {
	fx.In
	fx.Lifecycle

	Config              Config
	BasePath            RPCBasePath
	Logger              *zap.Logger
	Validator           protovalidate.Validator
	AuthMiddleware      *authn.Middleware
	HealthCheck         HealthCheck
	PublicReadOnly      PUBRO
	PublicReadWrite     PUBRW
	PrivateReadWrite    PRIVRW
	NewPublicReadOnly   func(svc PUBRO, opts ...connect.HandlerOption) (string, http.Handler)
	NewPublicReadWrite  func(svc PUBRW, opts ...connect.HandlerOption) (string, http.Handler)
	NewPrivateReadWrite func(svc PRIVRW, opts ...connect.HandlerOption) (string, http.Handler)

	// dependencies for building lambda relays.
	NewPrivateReadWriteClient func(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) PRIVRWC
	LambdaRelays              []*LambdaRelay `group:"lambda_relays"`

	// optionally server a public.
	PublicOpenAPIMount *openAPIMount `optional:"true"`
}) (res struct {
	fx.Out

	Public  http.Handler `name:"public"`
	Private http.Handler `name:"private"`
}, err error,
) {
	deps.Config.basePath = deps.BasePath

	reqValidator, err := validate.NewInterceptor(validate.WithValidator(deps.Validator))
	if err != nil {
		return res, fmt.Errorf("init validate interceptor: %w", err)
	}

	// optionally, we can also validate responses.
	interceptors := []connect.Interceptor{reqValidator}
	if deps.Config.ResponseValidation {
		interceptors = append(interceptors, stdcrpcintercept.NewValidateResponse(deps.Validator))
	}

	// setup the muxs
	pubMux, privMux := http.NewServeMux(), http.NewServeMux()
	path, handler := deps.NewPublicReadOnly(deps.PublicReadOnly, connect.WithInterceptors(interceptors...))
	pubMux.Handle(path, handler)

	path, handler = deps.NewPublicReadWrite(deps.PublicReadWrite, connect.WithInterceptors(interceptors...))
	privMux.Handle(path, handler)

	path, handler = deps.NewPrivateReadWrite(deps.PrivateReadWrite, connect.WithInterceptors(interceptors...))
	privMux.Handle(path, handler)

	// CORS for this part of the API, so web clients can call it.
	corsMiddleware := stdhttpware.NewConnectCORSMiddleware(
		deps.Config.ConnectCORSMaxAgeSeconds, deps.Config.ConnectCORSAllowedOrigins...)

	// setup HTTP middleware for the public Connect RPC handler.
	pubHdlr := deps.AuthMiddleware.Wrap(pubMux)
	/* ^ */ pubHdlr = corsMiddleware(pubHdlr)

	// public RPC and OpenAPI
	res.Public = withNonRPCHandling(
		deps.Lifecycle,
		pubHdlr, deps.Logger, deps.Config, false, deps.HealthCheck, nil, deps.NewPrivateReadWriteClient,
		deps.PublicOpenAPIMount)

	// private RPC and never a OpenAPI
	res.Private = withNonRPCHandling(
		deps.Lifecycle,
		privMux, deps.Logger, deps.Config, true, deps.HealthCheck, deps.LambdaRelays, deps.NewPrivateReadWriteClient,
		deps.PublicOpenAPIMount)

	return res, nil
}

// newInMemSysClient uses an in-memory http server to create a rpc client.
func newInMemSysClient[PRIVRWC any](
	lc fx.Lifecycle,
	cfg Config,
	hdlr http.Handler,
	newPrivateClientFn func(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) PRIVRWC,
) (cln PRIVRWC, err error) {
	srv, err := memhttp.New(hdlr)
	if err != nil {
		return cln, fmt.Errorf("init memhttp: %w", err)
	}

	lc.Append(fx.Hook{OnStop: srv.Shutdown})

	client := newPrivateClientFn(srv.Client(), srv.URL()+cfg.basePath.V)
	return client, nil
}

type openAPIMount struct {
	pattern  string
	stripped http.Handler
}

// newOpenAPI optionally sets up an Huma openapi instance for the application to
func newOpenAPI(deps struct {
	fx.In
	Config    Config
	BasePath  RPCBasePath
	APIConfig huma.Config
	CORS      cors.Options `name:"openapi"`
},
) (res struct {
	fx.Out
	API          huma.API
	OpenAPIMOunt *openAPIMount
},
) {
	apiBasePath := deps.BasePath.V + "/o"

	// the url as set in the spec
	serverURL := apiBasePath
	if deps.Config.OpenAPIExternalBaseURL != nil {
		deps.Config.OpenAPIExternalBaseURL.Path = apiBasePath
		serverURL = deps.Config.OpenAPIExternalBaseURL.String()
	}

	deps.APIConfig.Servers = append(deps.APIConfig.Servers, &huma.Server{URL: serverURL})
	apiRouter := http.NewServeMux()

	res.API = humago.New(apiRouter, deps.APIConfig)

	// middleware specific to the OpenAPI endpoint.
	corsed := cors.New(deps.CORS).Handler(apiRouter)
	stripped := http.StripPrefix(apiBasePath, corsed)

	res.OpenAPIMOunt = &openAPIMount{
		pattern:  apiBasePath + "/",
		stripped: stripped,
	}

	return res
}

// newPublicHandler turns a rpc handler into a http handler.
func withNonRPCHandling[PRIVRWC any](
	licecycle fx.Lifecycle,
	rpcHandler http.Handler,
	logs *zap.Logger,
	cfg Config,
	isPrivate bool,
	hcheck HealthCheck,
	LambdaRelays []*LambdaRelay,
	newPrivateClientFn func(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) PRIVRWC,
	oapiMount *openAPIMount,
) http.Handler {
	// mount the rpc API.
	base := http.NewServeMux()
	base.Handle(cfg.basePath.V+"/", http.StripPrefix(cfg.basePath.V, rpcHandler))

	mux := bhttp.NewCustomServeMux(
		bhttp.StdContextInit,
		20*1024*1024, // 20MiB
		bhttp.NewStdLogger(zap.NewStdLog(logs)),
		base,
		bhttp.NewReverser(),
	)

	// for the public side, mount a OpenAPI if provided.
	if !isPrivate && oapiMount != nil {
		logs.Info("mounting OpenAPI handler", zap.String("pattern", oapiMount.pattern))
		base.Handle(oapiMount.pattern, oapiMount.stripped)
	}

	// handle server errors.
	mux.Use(
		/* ^ */ errorMiddleware(logs),
	)

	// mount some none-rpc endpoints
	mux.HandleFunc("/healthz", healthz(cfg, hcheck, isPrivate)) // health check endpoint.

	// lambda relays need to call to an in-memory server of the final mux setup.
	final := stdhttpware.Apply(mux, logs)
	if len(LambdaRelays) > 0 {
		sys, err := newInMemSysClient(licecycle, cfg, final, newPrivateClientFn)
		if err != nil {
			panic(fmt.Sprintf("init memsys for lambda relays: %v", err))
		}

		// create endpoints for every configured relay. But they are mounted on the non-final mux.
		for _, relay := range LambdaRelays {
			pattern := "/lambda/" + relay.Slug
			logs.Info("mounting lambda relay", zap.String("slug", relay.Slug), zap.String("pattern", pattern))
			mux.HandleFunc(pattern, relay.CreateHandlerFromSysClient(sys))
		}
	}

	return final
}

// Provide the components as fx dependencies.
func Provide[PUBRO, PUBRW, PRIVRW, PRIVRWC any](
	rpcBasePath string, withOpenAPI bool,
	newPubROFunc func(svc PUBRO, opts ...connect.HandlerOption) (string, http.Handler),
	newPubRWFunc func(svc PUBRW, opts ...connect.HandlerOption) (string, http.Handler),
	newprivRWFunc func(svc PRIVRW, opts ...connect.HandlerOption) (string, http.Handler),
	newPrivRWCFunc func(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) PRIVRWC,
) fx.Option {
	opts := []fx.Option{
		fx.Supply(RPCBasePath{rpcBasePath}),
		fx.Supply(
			newPubROFunc,
			newPubRWFunc,
			newprivRWFunc,
			newPrivRWCFunc,
		),
	}

	if withOpenAPI {
		// if enabled, we provide and enforce it produces an API.
		opts = append(opts, fx.Provide(newOpenAPI), fx.Invoke(func(huma.API) {}))
	}

	return stdfx.ZapEnvCfgModule[Config]("stdpubprivrpc",
		New[PUBRO, PUBRW, PRIVRW, PRIVRWC],
		opts...,
	)
}

// type to carry the rpc base path.
type RPCBasePath struct{ V string }

// TestProvide provides API clients for testing. We use the actual web public
// and private HTTP handler to get as much parity as possible.
func TestProvide[PUBRO, PUBRW, PRIVRW, PUBROC, PUBRWC, PRIVRWC any](
	rpcBasePath string, withOpenAPI bool,
	newPubROFunc func(svc PUBRO, opts ...connect.HandlerOption) (string, http.Handler),
	newPubRWFunc func(svc PUBRW, opts ...connect.HandlerOption) (string, http.Handler),
	newprivRWFunc func(svc PRIVRW, opts ...connect.HandlerOption) (string, http.Handler),
	newPubROCFunc func(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) PUBROC,
	newPubRWCFunc func(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) PUBRWC,
	newPrivRWCFunc func(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) PRIVRWC,
	clientOpts ...connect.ClientOption,
) fx.Option {
	type testServers struct {
		Public  *httptest.Server
		Private *httptest.Server
	}

	return fx.Options(
		Provide(rpcBasePath, withOpenAPI, newPubROFunc, newPubRWFunc, newprivRWFunc, newPrivRWCFunc),
		fx.Provide(func(p struct {
			fx.In

			Logger  *zap.Logger
			Public  http.Handler `name:"public"`
			Private http.Handler `name:"private"`
		},
		) testServers {
			return testServers{
				httptest.NewServer(p.Public),
				httptest.NewServer(p.Private),
			}
		}),

		fx.Provide(func(ts testServers, bp RPCBasePath) PUBROC {
			return newPubROCFunc(ts.Public.Client(), ts.Public.URL+bp.V, clientOpts...)
		}),
		fx.Provide(func(ts testServers, bp RPCBasePath) PUBRWC {
			return newPubRWCFunc(ts.Public.Client(), ts.Public.URL+bp.V, clientOpts...)
		}),
		fx.Provide(func(ts testServers, bp RPCBasePath) PRIVRWC {
			return newPrivRWCFunc(ts.Private.Client(), ts.Private.URL+bp.V, clientOpts...)
		}),
	)
}
