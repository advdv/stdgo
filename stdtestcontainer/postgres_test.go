package stdtestcontainer_test

import (
	"context"
	"testing"

	"github.com/advdv/stdgo/stdtestcontainer"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestPostgres(t *testing.T) {
	t.Parallel()

	server := stdtestcontainer.Postgres(t, "postgres:18")
	require.Equal(t, server, stdtestcontainer.Postgres(t, "postgres:18"), "later calls share the server")

	conn, err := pgx.Connect(t.Context(), server.Endpoint)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(t.Context())) })

	var version int
	require.NoError(t, conn.QueryRow(t.Context(), `SELECT current_setting('server_version_num')::int`).Scan(&version))
	require.GreaterOrEqual(t, version, 180000, "runs the requested image")

	var maxConns int
	require.NoError(t, conn.QueryRow(t.Context(), `SELECT current_setting('max_connections')::int`).Scan(&maxConns))
	require.Equal(t, stdtestcontainer.PostgresMaxConnections, maxConns)

	// Containers on the network connect with the same credentials; the
	// image has psql.
	requireRunsOnNetwork(t, "postgres:18", "psql", server.NetworkEndpoint, "-c", "SELECT 1")
}
