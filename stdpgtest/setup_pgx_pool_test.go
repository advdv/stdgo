package stdpgtest_test

import (
	"path/filepath"
	"testing"

	"github.com/advdv/stdgo/stdpgtest"
	"github.com/stretchr/testify/require"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const connStr = "postgresql://postgres:postgres@localhost:5440/postgres?sslmode=disable"

func TestSetupPgxPool(t *testing.T) {
	pool := stdpgtest.SetupPgxPool(t.Context(), t, filepath.Join("testdata", "snapshot.sql"), connStr)
	require.NotNil(t, pool)
	require.NoError(t, pool.Ping(t.Context()))
}
