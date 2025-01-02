// Package stdhttpserverfx provides a web server.
package stdhttpserverfx

import (
	"context"
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
	BindAddrPort string `env:"BIND_ADDR_PORT" envDefault:"127.0.0.1:8282"`
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

func newListener(ap netip.AddrPort) (*net.TCPListener, error) {
	ln, err := net.ListenTCP("tcp", net.TCPAddrFromAddrPort(ap))
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	return ln, nil
}

// Params describe the fx parameters.
type Params struct {
	fx.In
	fx.Lifecycle

	Config   Config
	Logs     *zap.Logger
	Handler  http.Handler
	Listener *net.TCPListener
}

// Result describe the fx results.
type Result struct {
	fx.Out
	Server *http.Server
}

// New inits the http server.
func New(params Params) (Result, error) {
	srv := &http.Server{
		ReadTimeout:       params.Config.ReadTimeout,
		ReadHeaderTimeout: params.Config.ReadTimeout,
		WriteTimeout:      params.Config.WriteTimeout,
		IdleTimeout:       params.Config.IdleTimeout,
		Handler:           params.Handler,
		ErrorLog:          zap.NewStdLog(params.Logs),
	}

	params.Lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go srv.Serve(params.Listener) //nolint:errcheck

			params.Logs.Info("http server started", zap.Stringer("addr", params.Listener.Addr()))

			return nil
		},
		OnStop: func(ctx context.Context) error {
			dl, hasdl := ctx.Deadline()
			params.Logs.Info("shutting down http server", zap.Bool("has_dl", hasdl), zap.Duration("dl", time.Until(dl)))

			if err := srv.Shutdown(ctx); err != nil {
				return fmt.Errorf("failed to shut down: %w", err)
			}

			return nil
		},
	})

	return Result{Server: srv}, nil
}

// Provide dependencies.
func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdhttpserver", New,
		fx.Provide(newAddr),
		fx.Provide(fx.Private, newListener),
		fx.Invoke(func(*http.Server) {}),
	)
}
