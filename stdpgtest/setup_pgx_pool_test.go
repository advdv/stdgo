package stdpgtest_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/advdv/stdgo/stdpgtest"
	"github.com/stretchr/testify/require"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const connStr = "postgresql://postgres:postgres@localhost:5440/postgres?sslmode=disable"

func TestSetupPgxPool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	pool := stdpgtest.SetupPgxPool(ctx, t, filepath.Join("testdata", "snapshot.sql"), connStr)
	require.NotNil(t, pool)
}
