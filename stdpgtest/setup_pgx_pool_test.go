package stdpgtest_test

import (
	"testing"

	"github.com/advdv/stdgo/stdpgtest"
	"github.com/stretchr/testify/require"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const connStr = "postgresql://postgres:postgres@localhost:5440/postgres?sslmode=disable"

func TestSetupPgxPool(t *testing.T) {
	pool := stdpgtest.SetupPgxPool(t.Context(), t, snapshot, connStr)
	require.NotNil(t, pool)
	require.NoError(t, pool.Ping(t.Context()))
}
