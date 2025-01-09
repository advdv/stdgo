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
}

// New inits the main component in this module.
func New(cfg Config) (acfg aws.Config, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.LoadConfigTimeout)
	defer cancel()

	if acfg, err = config.LoadDefaultConfig(ctx); err != nil {
		return acfg, fmt.Errorf("failed to load default config: %w", err)
	}

	return acfg, nil
}

// Provide provides the package's components as an fx module.
func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdaws", New)
}
