package stdtestcontainer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Credentials of the root user of every S3 server started here; not a
// secret.
const (
	S3AccessKey = "stdgo"
	S3SecretKey = "stdgo-secret"
)

// s3StartTimeout bounds pulling the image and waiting for the server to be
// ready.
const s3StartTimeout = 2 * time.Minute

// s3Server is the server for one image, started at most once per process.
type s3Server struct {
	once     sync.Once
	endpoint string
	err      error
}

var (
	s3ServersMu sync.Mutex
	s3Servers   = map[string]*s3Server{}
)

// S3 returns the endpoint URL of an S3-compatible server that is shared by every
// test, and every test package, of the same `go test` run. Sign requests with
// [S3AccessKey] and [S3SecretKey]. The first call starts the server in a
// container; later calls, including those from other test packages (which Go
// runs in other processes), reuse it. Testcontainers' reaper (Ryuk) removes the
// container after the run, so tests never stop it themselves.
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
func S3(tb testing.TB, image string) string {
	tb.Helper()
	require.NotEmpty(tb, image, "s3 image is required")

	s3ServersMu.Lock()
	srv, ok := s3Servers[image]
	if !ok {
		srv = &s3Server{}
		s3Servers[image] = srv
	}
	s3ServersMu.Unlock()

	srv.once.Do(func() { srv.endpoint, srv.err = startS3(tb.Context(), image) })
	require.NoError(tb, srv.err, "start shared s3 container (is Docker running?)")

	return srv.endpoint
}

// startS3 runs the server for image, or attaches to the one another test
// package of this run already started.
func startS3(ctx context.Context, image string) (string, error) {
	// The server outlives the test that happens to start it, so that test
	// ending must not cancel the start.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s3StartTimeout)
	defer cancel()

	ctr, err := testcontainers.Run(ctx, image,
		// Test packages run in separate processes that share the session ID of
		// the `go test` run, so they all attach to one container. Containers of
		// earlier runs are never reused.
		testcontainers.WithReuseByName(s3ContainerName(image, testcontainers.SessionID())),
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
		return "", fmt.Errorf("run %s container: %w", image, err)
	}

	endpoint, err := ctr.PortEndpoint(ctx, "9000/tcp", "http")
	if err != nil {
		return "", fmt.Errorf("endpoint: %w", err)
	}

	return endpoint, nil
}

// s3ContainerName names the container for image in the given test session.
func s3ContainerName(image, sessionID string) string {
	sum := sha256.Sum256([]byte(image))

	return "stdgo-s3-" + hex.EncodeToString(sum[:4]) + "-" + sessionID[:min(12, len(sessionID))]
}
