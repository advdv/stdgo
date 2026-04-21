package stdawssecretsfx

import (
	"github.com/advdv/stdgo/stdfx"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-secretsmanager-caching-go/v2/secretcache"
	"go.uber.org/fx"
)

// Config holds configuration for the AWS secrets cache.
type Config struct{}

// Params holds the dependencies for creating the secrets cache.
type Params struct {
	fx.In

	Config    Config
	AWSConfig aws.Config
}

// New creates a new AWS Secrets Manager cache.
func New(p Params) (*secretcache.Cache, error) {
	return secretcache.New(func(c *secretcache.Cache) {
		c.Client = secretsmanager.NewFromConfig(p.AWSConfig)
	})
}

// Provide returns an fx.Option that provides the secrets cache module.
func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdawssecretsfx", New)
}
