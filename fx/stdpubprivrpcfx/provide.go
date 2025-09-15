package stdpubprivrpcfx

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"buf.build/go/protovalidate"
	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/advdv/bhttp"
	"github.com/advdv/stdgo/stdcrpc/stdcrpcintercept"
	"github.com/advdv/stdgo/stdfx"
	"github.com/advdv/stdgo/stdhttpware"
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
	CORSMaxAgeSeconds int `env:"CORS_MAX_AGE_SECONDS" envDefault:"3600"`
	// allow configuration of CORS allowed origins
	CORSAllowedOrigins []string `env:"CORS_ALLOWED_ORIGINS"`

	// configuration set via a depdency.
	basePath BasePath
}

func New[PUBRO, PUBRW, PRIVRW, PRIVRWC any](deps struct {
	fx.In
	fx.Lifecycle

	Config              Config
	BasePath            BasePath
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
	LambdaRelays              []*LambdaRelay[PRIVRWC] `group:"lambda_relays"`
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
		deps.Config.CORSMaxAgeSeconds, deps.Config.CORSAllowedOrigins...)

	// setup HTTP middleware for the public Connect RPC handler.
	pubHdlr := deps.AuthMiddleware.Wrap(pubMux)
	/* ^ */ pubHdlr = corsMiddleware(pubHdlr)

	res.Public = withNonRPCHandling(
		deps.Lifecycle,
		pubHdlr, deps.Logger, deps.Config, false, deps.HealthCheck, nil, deps.NewPrivateReadWriteClient)
	res.Private = withNonRPCHandling(
		deps.Lifecycle,
		privMux, deps.Logger, deps.Config, true, deps.HealthCheck, deps.LambdaRelays, deps.NewPrivateReadWriteClient)
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

// newPublicHandler turns a rpc handler into a http handler.
func withNonRPCHandling[PRIVRWC any](
	licecycle fx.Lifecycle,
	rpcHandler http.Handler,
	logs *zap.Logger,
	cfg Config,
	isPrivate bool,
	hcheck HealthCheck,
	LambdaRelays []*LambdaRelay[PRIVRWC],
	newPrivateClientFn func(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) PRIVRWC,
) http.Handler {
	base := http.NewServeMux()
	mux := bhttp.NewCustomServeMux(
		bhttp.StdContextInit,
		20*1024*1024, // 20MiB
		bhttp.NewStdLogger(zap.NewStdLog(logs)),
		base,
		bhttp.NewReverser(),
	)

	// handle server errors.
	mux.Use(
		/* ^ */ errorMiddleware(logs),
	)

	// mount some none-rpc endpoints
	mux.HandleFunc("/healthz", healthz(cfg, hcheck, isPrivate)) // health check endpoint.

	// mount the rpc API.
	base.Handle(cfg.basePath.V+"/", http.StripPrefix(cfg.basePath.V, rpcHandler))

	// lambda relays are built on the final mux.
	final := stdhttpware.Apply(mux, logs)
	if len(LambdaRelays) > 0 {
		sys, err := newInMemSysClient(licecycle, cfg, final, newPrivateClientFn)
		if err != nil {
			panic(fmt.Sprintf("init memsys for lambda relays: %v", err))
		}

		// create endpoints for every configured relay.
		for _, relay := range LambdaRelays {
			mux.HandleFunc("/lambda/"+relay.Slug, relay.CreateHandlerFromSysClient(sys))
		}
	}

	return final
}

// Provide the components as fx dependencies.
func Provide[PUBRO, PUBRW, PRIVRW, PRIVRWC any](
	newPubROFunc func(svc PUBRO, opts ...connect.HandlerOption) (string, http.Handler),
	newPubRWFunc func(svc PUBRW, opts ...connect.HandlerOption) (string, http.Handler),
	newprivRWFunc func(svc PRIVRW, opts ...connect.HandlerOption) (string, http.Handler),
	newPrivRWCFunc func(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) PRIVRWC,
) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdpubprivrpc",
		New[PUBRO, PUBRW, PRIVRW, PRIVRWC],
		fx.Supply(
			newPubROFunc,
			newPubRWFunc,
			newprivRWFunc,
			newPrivRWCFunc,
		),
	)
}

// TestProvide provides API clients for testing. We use the actual web public
// and private HTTP handler to get as much parity as possible.
func TestProvide[PUBRO, PUBRW, PRIVRW, PUBROC, PUBRWC, PRIVRWC any](
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
		Provide(newPubROFunc, newPubRWFunc, newprivRWFunc, newPrivRWCFunc),
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

		fx.Provide(func(ts testServers, bp BasePath) PUBROC {
			return newPubROCFunc(ts.Public.Client(), ts.Public.URL+bp.V, clientOpts...)
		}),
		fx.Provide(func(ts testServers, bp BasePath) PUBRWC {
			return newPubRWCFunc(ts.Public.Client(), ts.Public.URL+bp.V, clientOpts...)
		}),
		fx.Provide(func(ts testServers, bp BasePath) PRIVRWC {
			return newPrivRWCFunc(ts.Private.Client(), ts.Private.URL+bp.V, clientOpts...)
		}),
	)
}
