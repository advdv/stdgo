package stdawssecretsfx

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/aws/aws-secretsmanager-caching-go/v2/secretcache"
	"github.com/tidwall/gjson"
	"go.uber.org/fx"
)

const (
	resolvePrefix     = "$$aws-secret-manager-resolve$$"
	jsonPathSeparator = "$$jsonpath$$"
)

// DecorateEnvironment turns environment references to secret values into their actual secret value. This happens before
// the environment is provided to the rest of the application. In order to trigger this behaviour the environment
// variable needs to be encoded as "$$aws-secret-manager-resolve$$<secret_arn".
func DecorateEnvironment() fx.Option {
	return fx.Decorate(func(env stdenvcfg.Environment, cache *secretcache.Cache) (stdenvcfg.Environment, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()

		for key, val := range env {
			if !strings.HasPrefix(val, resolvePrefix) {
				continue
			}

			secretID := strings.TrimPrefix(val, resolvePrefix)
			secretID, jpath, hasJPath := strings.Cut(secretID, jsonPathSeparator)

			resolved, err := cache.GetSecretStringWithContext(ctx, secretID)
			if err != nil {
				return env, fmt.Errorf("failed to resolve secret %s: %w", secretID, err)
			}

			// if the string encodes a JSON path, resolve it instead of using the whole secret string. The secret string
			// is now expected to be well-formatted JSON.
			if hasJPath {
				env[key] = gjson.Get(resolved, jpath).Str
			} else {
				env[key] = resolved
			}
		}

		return env, nil
	})
}
