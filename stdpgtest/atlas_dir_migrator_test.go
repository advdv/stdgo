package stdpgtest_test

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/advdv/stdgo/stdpgtest"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAtlasDirMirator(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var actProgram string
	var actArgs []string
	var actDir string

	exec := func(ctx context.Context, stdin io.Reader, dir, program string, args ...string) (string, error) {
		actDir = dir
		actProgram = program
		actArgs = args

		return "", nil
	}

	dir := filepath.Join("testdata", "migrations1")
	migrator := stdpgtest.NewAtlasDirMigrator(dir, "local", "file://atlas.hcl", "..", exec)

	hash, err := migrator.Hash()
	require.NoError(t, err)
	require.Equal(t, "4a7517b72e8d2715cd3e73d4be8d37ea", hash)
	require.NoError(t, migrator.Migrate(ctx, nil, pgtestdb.Config{
		User:     "user",
		Password: "pass",
		Host:     "host",
		Port:     "port",
		Database: "db",
		Options:  "foo=bar",
	}))

	assert.Equal(t, "..", actDir)
	assert.Equal(t, "atlas", actProgram)
	assert.Equal(t, []string{
		"migrate", "apply",
		"--url", "postgres://user:pass@host:port/db?foo=bar",
		"--env", "local",
		"--config", "file://atlas.hcl",
	}, actArgs)
}
