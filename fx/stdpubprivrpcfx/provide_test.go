package stdpubprivrpcfx_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"buf.build/go/protovalidate"
	"connectrpc.com/authn"
	"connectrpc.com/connect"
	"github.com/advdv/stdgo/fx/stdpubprivrpcfx"
	foov1 "github.com/advdv/stdgo/fx/stdpubprivrpcfx/internal/foo/v1"
	"github.com/advdv/stdgo/fx/stdpubprivrpcfx/internal/foo/v1/foov1connect"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/aws/aws-lambda-go/events"
	"github.com/danielgtaylor/huma/v2"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

type handler struct{}

func newRPC() (
	foov1connect.ReadOnlyServiceHandler,
	foov1connect.ReadWriteServiceHandler,
	foov1connect.SystemServiceHandler,
) {
	h := &handler{}
	return h, h, h
}

func (handler) WhoAmI(_ context.Context, req *connect.Request[foov1.WhoAmIRequest]) (*connect.Response[foov1.WhoAmIResponse], error) {
	out := foov1.WhoAmIResponse_builder{}.Build()
	if req.Msg.GetEcho() != "" {
		out.SetGreeting(req.Msg.GetEcho())
	}

	if req.Msg.GetEcho() == "panic" {
		panic("panic me")
	}

	return connect.NewResponse(out), nil
}

func (handler) InitOrganization(ctx context.Context, _ *connect.Request[foov1.InitOrganizationRequest]) (*connect.Response[foov1.InitOrganizationResponse], error) {
	stdctx.Log(ctx).Info("init organization")

	return connect.NewResponse(foov1.InitOrganizationResponse_builder{}.Build()), nil
}

func setupAll(tb testing.TB, more ...any) (
	ctx context.Context,
	pubh http.Handler,
	privh http.Handler,
	ro foov1connect.ReadOnlyServiceClient,
	rw foov1connect.ReadWriteServiceClient,
	sys foov1connect.SystemServiceClient,
) {
	var deps struct {
		fx.In

		Public  http.Handler `name:"public"`
		Private http.Handler `name:"private"`
		RO      foov1connect.ReadOnlyServiceClient
		RW      foov1connect.ReadWriteServiceClient
		Sys     foov1connect.SystemServiceClient
	}

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDPUBPRIVRPC_ALLOW_FORCED_PANICS": "true",
			"STDPUBPRIVRPC_RESPONSE_VALIDATION": "true",
		}),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		fx.Populate(more...),
		fx.Provide(newRPC),
		fx.Provide(protovalidate.New),
		fx.Supply(stdpubprivrpcfx.HealthCheck(func(ctx context.Context, r *http.Request, isPrivate bool) error {
			if r.URL.Query().Get("failhc") != "" {
				return fmt.Errorf("fail")
			}
			return nil
		})),
		fx.Supply(huma.DefaultConfig("Test", "v0.0.0")),
		fx.Supply(authn.NewMiddleware(func(ctx context.Context, req *http.Request) (any, error) { return "a", nil })),
		stdpubprivrpcfx.TestProvide(
			testRpcBasePath,
			true,
			foov1connect.NewReadOnlyServiceHandler,
			foov1connect.NewReadWriteServiceHandler,
			foov1connect.NewSystemServiceHandler,
			foov1connect.NewReadOnlyServiceClient,
			foov1connect.NewReadWriteServiceClient,
			foov1connect.NewSystemServiceClient,
		),
		stdpubprivrpcfx.ProvideLambdaRelay("foo-relay-1", func(
			ctx context.Context, ev events.SNSEvent, sys foov1connect.SystemServiceClient,
		) error {
			_, err := sys.InitOrganization(ctx, connect.NewRequest(foov1.InitOrganizationRequest_builder{}.Build()))
			return err
		}),

		fx.Populate(&deps))
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	ctx = tb.Context()
	return ctx, deps.Public, deps.Private, deps.RO, deps.RW, deps.Sys
}
