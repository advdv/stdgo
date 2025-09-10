// Package stdawsfx provides a AWS configuration for the various clients as an fx module.
package stdawsfx

import (
	"context"
	"fmt"
	"time"

	"github.com/advdv/stdgo/stdfx"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"go.uber.org/fx"
)

// Config configures this module.
type Config struct {
	// LoadConfigTimeout bounds the time given to config loading
	LoadConfigTimeout time.Duration `env:"LOAD_CONFIG_TIMEOUT" envDefault:"100ms"`
	// OverwriteSharedConfigProfile can be set to overwrite the AWS_PROFILE value, useful during testing.
	OverwriteSharedConfigProfile string `env:"OVERWRITE_SHARED_CONFIG_PROFILE"`
}

// New inits the main component in this module.
func New(cfg Config) (acfg aws.Config, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.LoadConfigTimeout)
	defer cancel()

	opts := []func(*config.LoadOptions) error{}
	if cfg.OverwriteSharedConfigProfile != "" {
		opts = append(opts, config.WithSharedConfigProfile(cfg.OverwriteSharedConfigProfile))
	}

	if acfg, err = config.LoadDefaultConfig(ctx, opts...); err != nil {
		return acfg, fmt.Errorf("failed to load default config: %w", err)
	}

	return acfg, nil
}

// Provide provides the package's components as an fx module.
func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdaws", New)
}
