// Package stdenvcfg provides environment configuration as an fx dependency.
package stdenvcfg

import (
	"fmt"
	"os"

	"github.com/caarlos0/env/v11"
	"github.com/iancoleman/strcase"
	"go.uber.org/fx"
)

// Environment allows providing environment variables pre-set. Useful for testing.
type Environment map[string]string

// envConfigurer returns a function that parses environment variables into a configuration struct T. If the
// prefix is provided it will set a prefix for the underlying environment parser.
func envConfigurer[T any](prefix ...string) func(o env.Options, vars Environment) (T, error) {
	return func(envo env.Options, vars Environment) (T, error) {
		var cfg T

		// we always use an explicit environment.
		envo.Environment = vars

		if len(prefix) > 0 {
			envo.Prefix = prefix[0]
		}

		if err := env.ParseWithOptions(&cfg, envo); err != nil {
			return cfg, fmt.Errorf("failed to parse environment: %w", err)
		}

		return cfg, nil
	}
}

// Provide configuration T as an fx dependency that parses the environment with an optional prefix.
func Provide[T any](prefix ...string) fx.Option {
	return fx.Provide(fx.Annotate(
		envConfigurer[T](prefix...),
		fx.ParamTags(`optional:"true"`)))
}

// ProvideNamed configuration T as an fx dependency that parses the environment with an optional prefix.
func ProvideNamed[T any](name string, prefix ...string) fx.Option {
	if len(prefix) > 0 {
		prefix[0] += strcase.ToScreamingSnake(name) + "_"
	}

	return fx.Provide(fx.Annotate(
		envConfigurer[T](prefix...),
		fx.ParamTags(`optional:"true"`),
		fx.ResultTags(`name:"`+name+`"`),
	))
}

// ProvideExplicitEnvironment provides env options with environment options pre-set. Useful for testing.
func ProvideExplicitEnvironment(vars map[string]string) fx.Option {
	return fx.Supply(Environment(vars))
}

// ProvideOSEnvironment provides an Environment from the os.Environ.
func ProvideOSEnvironment() fx.Option {
	return fx.Supply(Environment(env.ToMap(os.Environ())))
}
