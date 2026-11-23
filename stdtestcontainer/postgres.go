package stdtestcontainer

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// PostgresMaxConnections is the max_connections of the server that Postgres
// starts. It leaves room for test packages that run in parallel, each with
// parallel tests whose pools stdpgxfx.TestProvide caps (STDPGX_POOL_MAX_CONNS).
// It is one value for the whole run, because every test package shares the
// server, and the first to start it sets its flags.
const PostgresMaxConnections = 2000

// Postgres returns a PostgreSQL server that is shared by every test, and every
// test package, of the same `go test` run, with the connection URLs to reach it
// at. The first call starts the server in a container; later calls, including
// those from other test packages (which Go runs in other processes), reuse it.
//
// The image, e.g. "postgres:18", is required so the server matches the version the code runs
// against in production; callers asking for different images get different servers.
//
// The server holds no data that tests can rely on: create a separate database per test on top of
// it, e.g. with pgtestdb or stdpgxtestfx. It requires a running Docker daemon.
func Postgres(tb testing.TB, image string) Server {
	tb.Helper()

	return shared(tb, "postgres", image, func(
		ctx context.Context, name string, network, reuse testcontainers.CustomizeRequestOption,
	) (Server, error) {
		ctr, err := postgres.Run(ctx, image, reuse, network,
			// Tests need no durability: keep the data in memory and write as little WAL as possible.
			testcontainers.WithTmpfs(map[string]string{"/var/lib/postgresql": "rw"}),
			testcontainers.WithCmdArgs("-c", "wal_level=minimal", "-c", "max_wal_senders=0",
				// Every test package of the run shares this server, each with tests in parallel that
				// open pools of their own; PostgreSQL's default of 100 is sized for one application.
				"-c", "max_connections="+strconv.Itoa(PostgresMaxConnections)),
			postgres.BasicWaitStrategies(),
		)
		if err != nil {
			return Server{}, fmt.Errorf("run %s container: %w", image, err)
		}

		endpoint, err := ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			return Server{}, fmt.Errorf("connection string: %w", err)
		}

		onNetwork, err := url.Parse(endpoint)
		if err != nil {
			return Server{}, fmt.Errorf("parse connection string: %w", err)
		}

		onNetwork.Host = name + ":5432"

		return Server{Endpoint: endpoint, NetworkEndpoint: onNetwork.String()}, nil
	})
}
