package stdpgtest

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"

	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/common"
)

// NewAtlasDirMigrator returns a [AtlasDirMigrator], which is a pgtestdb.Migrator that
// uses the `atlas` CLI tool to perform migrations.
//
//	atlas migrate apply --url $DB --dir file://$migrationsDirPath
func NewAtlasDirMigrator(
	migrationsDirPath string,
	atlasEnv string,
	atlasConfig string,
	execute func(ctx context.Context, stdin io.Reader, program string, args ...string) (string, error),
) *AtlasDirMigrator {
	return &AtlasDirMigrator{
		MigrationsDirPath: migrationsDirPath,
		AtlasEnv:          atlasEnv,
		AtlasConfig:       atlasConfig,
		execute:           execute,
	}
}

// AtlasDirMigrator is a pgtestdb.Migrator that uses the `atlas` CLI
// tool to perform migrations.
//
//	atlas migrate apply --url $DB --dir file://$migrationsDirPath
//
// AtlasDirMigrator requires that it runs in an environment where the `atlas` CLI is
// in the $PATH. It shells out to that program to perform its migrations,
// as recommended by the Atlas maintainers.
//
// AtlasDirMigrator does not perform any Verify() or Prepare() logic.
type AtlasDirMigrator struct {
	MigrationsDirPath string
	AtlasEnv          string
	AtlasConfig       string

	execute func(ctx context.Context, stdin io.Reader, program string, args ...string) (string, error)
}

func (m *AtlasDirMigrator) Hash() (string, error) {
	return common.HashFile(filepath.Join(m.MigrationsDirPath, "atlas.sum"))
}

// Migrate shells out to the `atlas` CLI program to migrate the template
// database.
//
//	atlas migrate apply --url $DB --dir file://$migrationsDirPath
func (m *AtlasDirMigrator) Migrate(
	ctx context.Context,
	_ *sql.DB,
	templateConf pgtestdb.Config,
) error {
	_, err := m.execute(ctx, nil,
		"atlas",
		"migrate",
		"apply",
		"--url",
		templateConf.URL(),
		"--env", m.AtlasEnv,
		"--config", m.AtlasConfig,
	)

	return err
}
