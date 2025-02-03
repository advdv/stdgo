// Package stdenvcfg provides environment configuration as an fx dependency.
package stdenvcfg

import (
	"fmt"

	"github.com/caarlos0/env/v11"
	"go.uber.org/fx"
)

// Environment allows providing environment variables pre-set. Useful for testing.
type Environment map[string]string

// envConfigurer returns a function that parses environment variables into a configuration struct T. If the
// prefix is provided it will set a prefix for the underlying environment parser.
func envConfigurer[T any](prefix ...string) func(o env.Options, vars Environment) (T, error) {
	return func(envo env.Options, vars Environment) (T, error) {
		var cfg T

		if len(vars) > 0 {
			envo.Environment = vars
		}

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
		fx.ParamTags(`optional:"true"`, `optional:"true"`)))
}

// ProvideEnvironment provides env options with environment options pre-set. Useful for testing.
func ProvideEnvironment(vars map[string]string) fx.Option {
	return fx.Supply(Environment(vars))
}
