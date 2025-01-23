// Package stdfx provides utilities for using Uber's fx.
package stdfx

import (
	"strings"

	"github.com/advdv/stdgo/stdenvcfg"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// ZapEnvCfgModule creates a fx module that already provides a configuration struct parsed from the prefixed
// environment vars and a logger named after the module. P is assumed to be a fx "Params" struct embedding a
// fx.In, see: https://uber-go.github.io/fx/parameter-objects.html and R an results object as described here:
// https://uber-go.github.io/fx/result-objects.html.
func ZapEnvCfgModule[CFG any, P, R any](name string, newf func(P) (R, error), opts ...fx.Option) fx.Option {
	return fx.Module(name, append(opts,
		stdenvcfg.Provide[CFG](strings.ToUpper(name)+"_"),
		fx.Decorate(func(l *zap.Logger) *zap.Logger { return l.Named(name) }),
		fx.Provide(newf),
	)...)
}
