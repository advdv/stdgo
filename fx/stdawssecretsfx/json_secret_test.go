package stdawssecretsfx_test

import (
	"context"
	"testing"

	"github.com/advdv/stdgo/fx/stdawssecretsfx"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-secretsmanager-caching-go/v2/secretcache"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

type secret1 struct {
	MySecret string `json:"my_secret"`
}

type config1 struct{}

func (config1) AWSSecretID() string {
	return "some:json:secret"
}

type client1 struct {
	secretcache.SecretsManagerAPIClient
}

func (client1) DescribeSecret(ctx context.Context, params *secretsmanager.DescribeSecretInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error) {
	return &secretsmanager.DescribeSecretOutput{
		ARN:  aws.String("some:arn"),
		Name: aws.String("some/name"),
		VersionIdsToStages: map[string][]string{
			"some:version:id": {"AWSCURRENT"},
		},
	}, nil
}

func (client1) GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	switch *params.SecretId {
	case "some:string:secret":
		return &secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(`sosecret`),
		}, nil
	case "some:json:secret":
		return &secretsmanager.GetSecretValueOutput{
			SecretString: aws.String(`{"my_secret":"sosecret"}`),
		}, nil
	default:
		panic("unsupported, got: " + *params.SecretId)
	}
}

func TestJSONSecret(t *testing.T) {
	var jsecret1 *stdawssecretsfx.JSONSecret[secret1]

	cache, err := secretcache.New(func(c *secretcache.Cache) { c.Client = client1{} })
	require.NoError(t, err)

	app := fxtest.New(t,
		fx.Supply(cache, config1{}),
		fx.Populate(&jsecret1),
		stdawssecretsfx.ProvideJSONSecret[secret1, config1]())
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.Equal(t, "sosecret", jsecret1.Static().MySecret)
}
