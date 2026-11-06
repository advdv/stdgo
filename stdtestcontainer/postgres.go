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
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// postgresStartTimeout bounds pulling the image and waiting for the server to accept connections.
const postgresStartTimeout = 2 * time.Minute

// postgresServer is the server for one image, started at most once per process.
type postgresServer struct {
	once sync.Once
	url  string
	err  error
}

var (
	postgresServersMu sync.Mutex
	postgresServers   = map[string]*postgresServer{}
)

// Postgres returns the connection URL of a PostgreSQL server that is shared by every test, and
// every test package, of the same `go test` run. The first call starts the server in a container;
// later calls, including those from other test packages (which Go runs in other processes),
// reuse it. Testcontainers' reaper (Ryuk) removes the container after the run, so tests never
// stop it themselves.
//
// The image, e.g. "postgres:18", is required so the server matches the version the code runs
// against in production; callers asking for different images get different servers.
//
// The server holds no data that tests can rely on: create a separate database per test on top of
// it, e.g. with pgtestdb or stdpgxtestfx. It requires a running Docker daemon.
func Postgres(tb testing.TB, image string) string {
	tb.Helper()
	require.NotEmpty(tb, image, "postgres image is required")

	postgresServersMu.Lock()
	srv, ok := postgresServers[image]
	if !ok {
		srv = &postgresServer{}
		postgresServers[image] = srv
	}
	postgresServersMu.Unlock()

	srv.once.Do(func() { srv.url, srv.err = startPostgres(tb.Context(), image) })
	require.NoError(tb, srv.err, "start shared postgres container (is Docker running?)")

	return srv.url
}

// startPostgres runs the server for image, or attaches to the one another test package of this
// run already started.
func startPostgres(ctx context.Context, image string) (string, error) {
	// The server outlives the test that happens to start it, so that test ending must not cancel
	// the start.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), postgresStartTimeout)
	defer cancel()

	ctr, err := postgres.Run(ctx, image,
		// Test packages run in separate processes that share the session ID of the `go test` run,
		// so they all attach to one container. Containers of earlier runs are never reused.
		testcontainers.WithReuseByName(postgresContainerName(image, testcontainers.SessionID())),
		// Tests need no durability: keep the data in memory and write as little WAL as possible.
		testcontainers.WithTmpfs(map[string]string{"/var/lib/postgresql": "rw"}),
		testcontainers.WithCmdArgs("-c", "wal_level=minimal", "-c", "max_wal_senders=0"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return "", fmt.Errorf("run %s container: %w", image, err)
	}

	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", fmt.Errorf("connection string: %w", err)
	}

	return url, nil
}

// postgresContainerName names the container for image in the given test session.
func postgresContainerName(image, sessionID string) string {
	sum := sha256.Sum256([]byte(image))

	return "stdgo-postgres-" + hex.EncodeToString(sum[:4]) + "-" + sessionID[:min(12, len(sessionID))]
}
