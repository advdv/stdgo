// Package stdbuildinfofx provides information determined at built time.
package stdbuildinfofx

import (
	"github.com/advdv/stdgo/stdfx"
	"go.uber.org/fx"
)

// Config configures the package's components.
type Config struct{}

type (
	// Params determine main inputs for creating components.
	Params struct {
		fx.In
		Version string `name:"version"`
	}
	// Result determine main output from creating components.
	Result struct {
		fx.Out
		Info
	}
)

// Info holds the main build info.
type Info struct{ version string }

// New init the components.
func New(p Params) (Result, error) {
	return Result{Info: Info{version: p.Version}}, nil
}

// Version as determined at build time.
func (in Info) Version() string {
	return in.version
}

// Provide the package's components.
func Provide(version string) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdbuildinfo", New,
		fx.Supply(fx.Annotate(version, fx.ResultTags(`name:"version"`))),
	)
}

// TestProvide provides di for testing where no specific version is required to be provided.
func TestProvide() fx.Option {
	return Provide("v0.0.0-test")
}
