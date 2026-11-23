package stdtestcontainer

import (
	"context"
	"fmt"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Gotenberg returns a Gotenberg server (https://gotenberg.dev) that is shared
// by every test, and every test package, of the same `go test` run, the way
// [S3] is. It runs with WEBHOOK_ENABLE_SYNC_MODE, as stdgotenbergfx requires,
// and joins the [Network], so it reaches the servers there that the requests
// it is handed address, such as [S3]'s NetworkEndpoint.
//
// Gotenberg keeps no state between requests, so tests share it safely. It
// requires a running Docker daemon.
func Gotenberg(tb testing.TB, image string) Server {
	tb.Helper()

	return shared(tb, "gotenberg", image, func(
		ctx context.Context, name string, network, reuse testcontainers.CustomizeRequestOption,
	) (Server, error) {
		ctr, err := testcontainers.Run(ctx, image, reuse, network,
			testcontainers.WithEnv(map[string]string{
				"WEBHOOK_ENABLE_SYNC_MODE": "true",
				"GOTENBERG_HIDE_BANNER":    "true",
			}),
			testcontainers.WithExposedPorts("3000/tcp"),
			testcontainers.WithWaitStrategy(wait.ForHTTP("/health").WithPort("3000/tcp")),
		)
		if err != nil {
			return Server{}, fmt.Errorf("run %s container: %w", image, err)
		}

		endpoint, err := ctr.PortEndpoint(ctx, "3000/tcp", "http")
		if err != nil {
			return Server{}, fmt.Errorf("endpoint: %w", err)
		}

		return Server{Endpoint: endpoint, NetworkEndpoint: "http://" + name + ":3000"}, nil
	})
}
