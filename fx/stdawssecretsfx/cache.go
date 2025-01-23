package stdawssecretsfx

import (
	"github.com/advdv/stdgo/stdfx"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-secretsmanager-caching-go/v2/secretcache"
	"go.uber.org/fx"
)

type Config struct{}

type Params struct {
	fx.In
	Config    Config
	AWSConfig aws.Config
}

func New(p Params) (*secretcache.Cache, error) {
	return secretcache.New(func(c *secretcache.Cache) {
		c.Client = secretsmanager.NewFromConfig(p.AWSConfig)
	})
}

func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdawssecretsfx", New)
}
