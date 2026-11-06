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

	url := stdtestcontainer.Postgres(t, "postgres:18")
	require.Equal(t, url, stdtestcontainer.Postgres(t, "postgres:18"), "later calls share the server")

	conn, err := pgx.Connect(t.Context(), url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(t.Context())) })

	var version int
	require.NoError(t, conn.QueryRow(t.Context(), `SELECT current_setting('server_version_num')::int`).Scan(&version))
	require.GreaterOrEqual(t, version, 180000, "runs the requested image")
}
