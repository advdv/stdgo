package stdtestcontainer

import (
	"context"
	"fmt"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Credentials of the root user of every S3 server started here; not a
// secret.
const (
	S3AccessKey = "stdgo"
	S3SecretKey = "stdgo-secret"
)

// S3 returns an S3-compatible server that is shared by every test, and every
// test package, of the same `go test` run, with the endpoint URLs to reach it
// at. Sign requests with [S3AccessKey] and [S3SecretKey]. The first call
// starts the server in a container; later calls, including those from other
// test packages (which Go runs in other processes), reuse it.
//
// The image is required so the server matches the one the code runs against
// elsewhere, such as in Docker Compose; callers asking for different images get
// different servers. The image must take MinIO's "server" command and
// MINIO_ROOT_USER and MINIO_ROOT_PASSWORD variables, as MinIO's own images and
// forks such as pgsty/silo do.
//
// The server holds no data that tests can rely on: create separate buckets per
// test on top of it, e.g. with stds3testfx. It requires a running Docker
// daemon.
func S3(tb testing.TB, image string) Server {
	tb.Helper()

	return shared(tb, "s3", image, func(
		ctx context.Context, name string, network, reuse testcontainers.CustomizeRequestOption,
	) (Server, error) {
		ctr, err := testcontainers.Run(ctx, image, reuse, network,
			testcontainers.WithCmd("server", "/data"),
			testcontainers.WithEnv(map[string]string{
				"MINIO_ROOT_USER":     S3AccessKey,
				"MINIO_ROOT_PASSWORD": S3SecretKey,
			}),
			testcontainers.WithExposedPorts("9000/tcp"),
			// Tests need no durability: keep the objects in memory.
			testcontainers.WithTmpfs(map[string]string{"/data": "rw"}),
			testcontainers.WithWaitStrategy(wait.ForHTTP("/minio/health/ready").WithPort("9000/tcp")),
		)
		if err != nil {
			return Server{}, fmt.Errorf("run %s container: %w", image, err)
		}

		endpoint, err := ctr.PortEndpoint(ctx, "9000/tcp", "http")
		if err != nil {
			return Server{}, fmt.Errorf("endpoint: %w", err)
		}

		return Server{Endpoint: endpoint, NetworkEndpoint: "http://" + name + ":9000"}, nil
	})
}
