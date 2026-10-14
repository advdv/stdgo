// Package stdhttpserverfx provides a web server.
package stdhttpserverfx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/advdv/stdgo/stdfx"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Config configures the package.
type Config struct {
	// BindAddrPort configures where the web server will listen for incoming tcp traffic
	BindAddrPort string `env:"BIND_ADDR_PORT" envDefault:"0.0.0.0:8282"`
	// HTTP read timeout, See: https://blog.cloudflare.com/exposing-go-on-the-internet/
	ReadTimeout time.Duration `env:"READ_TIMEOUT" envDefault:"5s"`
	// HTTP read header timeout, See: https://blog.cloudflare.com/exposing-go-on-the-internet/
	ReadHeaderTimeout time.Duration `env:"READ_HEADER_TIMEOUT" envDefault:"5s"`
	// HTTP write timeout, See: https://blog.cloudflare.com/exposing-go-on-the-internet/
	WriteTimeout time.Duration `env:"WRITE_TIMEOUT" envDefault:"12s"`
	// HTTP idle timeout, See: https://blog.cloudflare.com/exposing-go-on-the-internet/
	IdleTimeout time.Duration `env:"IDLE_TIMEOUT" envDefault:"120s"`
}

func newAddr(cfg Config) (netip.AddrPort, error) {
	ap, err := netip.ParseAddrPort(cfg.BindAddrPort)
	if err != nil {
		return ap, fmt.Errorf("failed to parse addr/port: %w", err)
	}

	return ap, nil
}

// New inits the http server.
func New(
	lifecycle fx.Lifecycle,
	hdlr http.Handler,
	addr netip.AddrPort,
	cfg Config,
	logs *zap.Logger,
	shut fx.Shutdowner,
) (*http.Server, error) {
	srv := &http.Server{
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		Handler:           hdlr,
		ErrorLog:          zap.NewStdLog(logs),
	}

	lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			// Not injected: a listener bound at construction leaks when the app fails before (or never) starts.
			lnr, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr.String())
			if err != nil {
				return fmt.Errorf("failed to listen: %w", err)
			}

			go func() {
				// Serve only returns ErrServerClosed on a regular shutdown. Anything else means the server
				// stopped accepting connections, so we shut the app down instead of running without it.
				if err := srv.Serve(lnr); !errors.Is(err, http.ErrServerClosed) {
					logs.Error("http server stopped serving", zap.Error(err))

					if err := shut.Shutdown(fx.ExitCode(1)); err != nil {
						logs.Error("failed to request shutdown", zap.Error(err))
					}
				}
			}()

			logs.Info("http server started", zap.Stringer("addr", lnr.Addr()))

			return nil
		},
		OnStop: func(ctx context.Context) error {
			dl, hasdl := ctx.Deadline()
			logs.Info("shutting down http server", zap.Bool("has_dl", hasdl), zap.Duration("dl", time.Until(dl)))

			if err := srv.Shutdown(ctx); err != nil {
				return fmt.Errorf("failed to shut down: %w", err)
			}

			return nil
		},
	})

	return srv, nil
}

// Provide dependencies.
func Provide(name ...string) fx.Option {
	newAnns := []fx.Annotation{}
	addrAnns := []fx.Annotation{}
	invAnns := []fx.Annotation{}

	if len(name) > 0 {
		newAnns = []fx.Annotation{fx.ParamTags(``, tag(name[0]), tag(name[0]), tag(name[0])), fx.ResultTags(tag(name[0]))}
		addrAnns = []fx.Annotation{fx.ParamTags(tag(name[0])), fx.ResultTags(tag(name[0]))}
		invAnns = []fx.Annotation{fx.ParamTags(tag(name[0]))}
	}

	opts := []fx.Option{
		fx.Provide(fx.Annotate(New, newAnns...)),
		fx.Provide(fx.Annotate(newAddr, addrAnns...)),
		fx.Invoke(fx.Annotate(func(*http.Server) {}, invAnns...)),
	}

	if len(name) < 1 {
		return stdfx.NoProvideZapEnvCfgModule[Config]("stdhttpserver", opts...)
	}

	return stdfx.NamedNoProvideZapEnvCfgModule[Config]("stdhttpserver", name[0], opts...)
}

func tag(name string) string {
	return fmt.Sprintf(`name:"%s"`, name)
}
