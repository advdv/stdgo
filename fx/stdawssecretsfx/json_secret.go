// Package stdawssecretsfx provides access to secrets in the AWS Secret manager.
package stdawssecretsfx

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-secretsmanager-caching-go/v2/secretcache"
	"go.uber.org/fx"
)

// SecretIDer describes a type that can return the ID of a secret in the AWS secrets manager.
type SecretIDer interface {
	AWSSecretID() string
}

// JSONSecretParams describes the dependencies for creating the json secret. It requires a type that
// can return the ID of the secret in the AWS secrets manager. Usually this is a configuration
// struct that implements the interface.
type JSONSecretParams[IDR SecretIDer] struct {
	fx.In
	fx.Lifecycle
	Config IDR
	Cache  *secretcache.Cache
}

// JSONSecret holds a secret encoded as JSON with shape "S".
type JSONSecret[S any] struct {
	secretID string
	cache    *secretcache.Cache
	value    S
}

// NewJSONSecret inits the main component in this module.
func NewJSONSecret[S any, IDR SecretIDer](p JSONSecretParams[IDR]) (secr *JSONSecret[S], err error) {
	secr = &JSONSecret[S]{cache: p.Cache, secretID: p.Config.AWSSecretID()}
	p.Lifecycle.Append(fx.Hook{OnStart: secr.Start})

	return secr, nil
}

// Static returns a pointer to the static value of the secret as read on startup.
func (s *JSONSecret[S]) Static() *S {
	return &s.value
}

// Start will read the value of the secret on startup.
func (s *JSONSecret[S]) Start(ctx context.Context) error {
	str, err := s.cache.GetSecretStringWithContext(ctx, s.secretID)
	if err != nil {
		return fmt.Errorf("failed to get secret string: %w", err)
	}

	if err := json.Unmarshal([]byte(str), &s.value); err != nil {
		return fmt.Errorf("failed to unmarshal secret string as JSON: %w", err)
	}

	return nil
}

// ProvideJSONSecret provides a JSON secret with shape "S". It also requires a type that needs to be provided
// through fx as a dependency and implements SecretIDer. Usually this is a configuration struct that
// has the name of the ID as one of its fields, of which the field is loaded from an environment variable. This config
// then only needs to implement the SecretIDer interface by just returing the env value.
func ProvideJSONSecret[S any, IDR SecretIDer]() fx.Option {
	return fx.Provide(NewJSONSecret[S, IDR])
}
