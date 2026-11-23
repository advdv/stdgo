package stdtestcontainer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Bifrost returns a Bifrost LLM gateway (https://docs.getbifrost.ai) that is
// shared by every test, and every test package, of the same `go test` run,
// the way [S3] is. It serves the configuration in the file at config, the
// gateway's config.json, which may reference the variables of env as
// env.<NAME>, such as the keys of its providers.
//
// Calls with another image, configuration, or env get another gateway. The
// gateway calls the real providers, so tests share it only to read its
// answers; they must not change its configuration. It requires a running
// Docker daemon.
func Bifrost(tb testing.TB, image, config string, env map[string]string) Server {
	tb.Helper()

	contents, err := os.ReadFile(config)
	require.NoError(tb, err, "read bifrost config")

	// The configuration and environment decide what the gateway does, so they
	// name the server along with the image.
	sum := sha256.New()
	sum.Write(contents)

	for _, name := range slices.Sorted(maps.Keys(env)) {
		sum.Write([]byte("\x00" + name + "=" + env[name]))
	}

	key := image + "@config:" + hex.EncodeToString(sum.Sum(nil))

	return shared(tb, "bifrost", key, func(
		ctx context.Context, name string, network, reuse testcontainers.CustomizeRequestOption,
	) (Server, error) {
		ctr, err := testcontainers.Run(ctx, image, reuse, network,
			testcontainers.WithFiles(testcontainers.ContainerFile{
				HostFilePath:      config,
				ContainerFilePath: "/app/data/config.json",
				FileMode:          0o644,
			}),
			testcontainers.WithEnv(env),
			testcontainers.WithExposedPorts("8080/tcp"),
			testcontainers.WithWaitStrategy(wait.ForHTTP("/health").WithPort("8080/tcp")),
		)
		if err != nil {
			return Server{}, fmt.Errorf("run %s container: %w", image, err)
		}

		endpoint, err := ctr.PortEndpoint(ctx, "8080/tcp", "http")
		if err != nil {
			return Server{}, fmt.Errorf("endpoint: %w", err)
		}

		return Server{Endpoint: endpoint, NetworkEndpoint: "http://" + name + ":8080"}, nil
	})
}
