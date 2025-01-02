// Package stdwebservefx provides a web server.
package stdwebservefx

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
func New(p Params) Result {
	return Result{Server: &http.Server{
		ReadTimeout:       p.Config.ReadTimeout,
		ReadHeaderTimeout: p.Config.ReadTimeout,
		WriteTimeout:      p.Config.WriteTimeout,
		IdleTimeout:       p.Config.IdleTimeout,
		Handler:           p.Handler,
		ErrorLog:          zap.NewStdLog(p.Logs),
	}}
}

// Provide dependencies.
func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdwebserve",

		fx.Provide(newAddr),
		fx.Provide(fx.Private, newListener),
		fx.Provide(fx.Private, fx.Annotate(New,
			fx.OnStart(func(_ context.Context, logs *zap.Logger, ln *net.TCPListener, s *http.Server) error {
				go s.Serve(ln) //nolint:errcheck

				logs.Info("http server started", zap.Stringer("addr", ln.Addr()))

				return nil
			}),
			fx.OnStop(func(ctx context.Context, logs *zap.Logger, s *http.Server) error {
				dl, hasdl := ctx.Deadline()
				logs.Info("shutting down http server", zap.Bool("has_dl", hasdl), zap.Duration("dl", time.Until(dl)))

				if err := s.Shutdown(ctx); err != nil {
					return fmt.Errorf("failed to shut down: %w", err)
				}

				return nil
			}))),
		fx.Invoke(func(*http.Server) {}),
	)
}
