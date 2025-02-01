// Package stdmageatlas provides mage commands for working with Atlas-based projects.
package stdmageatlas

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/advdv/stdgo/stdlo"
	"github.com/advdv/stdgo/stdmage"
	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/jackc/pgx/v5"
	"github.com/magefile/mage/sh"
)

var (
	inspectEnv    = "inspect"
	migrationsDir = "migrations"
	patchHook     PatchFunc
	devEnv        = "dev"
)

// PatchFunc allows users of the mage targets to patch any migration that is generated.
type PatchFunc = func(latestName, latestFilename string) error

var (
	// targeting the host and container for snapshotting.
	snapshotTarget *pgx.ConnConfig
	// name of the container that is restarted for the snapshot.
	snapshotTargetContainer string
	// directory where the snapshot is stored in.
	snapshotDir = filepath.Join("migrations", "snapshot")
)

// Init inits the mage targets. The weird signature is to make Mage ignore this when importing.
func Init(
	localDevEnv string,
	atlasInspectEnv,
	atlasMigrationsDir string,
	snapshotRelDir string,
	snapshotTargetContainerName string,
	snapshotTargetConnStr string,
	hook PatchFunc,
	_ ...[]string, // just here so Mage doesn't recognize a "init" target.
) {
	inspectEnv = atlasInspectEnv

	migrationsDir = atlasMigrationsDir
	patchHook = hook
	devEnv = localDevEnv

	snapshotTargetContainer = snapshotTargetContainerName
	snapshotTarget, _ = pgx.ParseConfig(snapshotTargetConnStr)
	snapshotDir = snapshotRelDir
}

// Inspect visualizes the schema.
func Inspect() error {
	return sh.Run("atlas", "schema", "inspect",
		"--env", inspectEnv,
		"-w",
	)
}

// Hash runs the logic for re-hashing the Atlas sum file.
func Hash() error {
	if err := stdmage.LoadEnv(devEnv); err != nil {
		return err
	}

	return sh.Run("atlas", "migrate", "hash",
		"--env", devEnv)
}

// Diff generates a named migration for the local environment.
func Diff(name string) error {
	if err := stdmage.LoadEnv(devEnv); err != nil {
		return err
	}

	return sh.Run("atlas", "migrate", "diff", name,
		"--env", devEnv,
	)
}

// Apply applies the migrations for the provided Atlas environment.
func Apply(env string) error {
	if err := stdmage.LoadEnv(env); err != nil {
		return err
	}

	return sh.Run("atlas", "migrate", "apply",
		"--env", env)
}

// Snapshot dumps the migrated schema to an sql file using pg_dump.
func Snapshot() error {
	// restart the container postgres container, expecting it to be empheral
	if err := sh.Run(`docker`, `compose`, `restart`, snapshotTargetContainer); err != nil {
		return fmt.Errorf("failed to restart postgres to clean it: %w", err)
	}

	// wait until the newly started container is migrated
	if _, err := failsafe.Get(func() (bool, error) {
		if err := Apply(devEnv); err != nil {
			return false, fmt.Errorf("failed to migrated database: %w", err)
		}

		return true, nil
	}, retrypolicy.
		Builder[bool]().
		WithMaxRetries(10).
		WithDelay(time.Millisecond*500).
		WithMaxDuration(time.Second*20).
		Build()); err != nil {
		return fmt.Errorf("failed to retry: %w", err)
	}

	// run pg_dump to sql representation
	stdlo.Must0(os.MkdirAll(snapshotDir, 0o744))
	if err := sh.Run("docker", "run",
		"--rm", "--network", "host",
		"-v", filepath.Join(stdlo.Must1(os.Getwd()), snapshotDir)+":/snapshot",
		"-e", "PGPASSWORD="+snapshotTarget.Password,
		"postgres:17.2-alpine", "pg_dump",
		"-h", snapshotTarget.Host,
		"-p", fmt.Sprintf("%d", snapshotTarget.Port),
		"-U", snapshotTarget.User,
		"--schema-only",
		"--no-comments",
		"--no-owner",
		"-b", "-v", "-f", "/snapshot/snapshot.sql", "postgres"); err != nil {
		return fmt.Errorf("failed to run pg_dump: %w", err)
	}

	return nil
}

// Iterate replaces the latest migration file with the same name, for quicker development.
func Iterate(name string) error {
	latestName, latestFilename := getLatestMigrationName()

	fmt.Fprintf(os.Stderr, "latest migration name: '%v' (%s), curr name: '%v'\n", latestName, latestFilename, name)
	if latestName == name {
		fmt.Fprintf(os.Stderr, "latest migration matches provided name, remove to replace it.")

		if err := os.Remove(filepath.Join(migrationsDir, latestFilename)); err != nil {
			return fmt.Errorf("failed to remove name-named latest migration: %w", err)
		}

		if err := Hash(); err != nil {
			return fmt.Errorf("failed to hash migrations: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "diff and update migrations dir with name: '%s'\n", name)
	if err := Diff(name); err != nil {
		return fmt.Errorf("failed to generate migration: %w", err)
	}

	// check the migration dir again. If the latest is now the same as the name provided as the argument
	// it means we iterated, or a new one was created. In that case patch it.
	newLatestName, newLatestFilename := getLatestMigrationName()
	if patchHook != nil && newLatestName == name {
		if err := patchHook(newLatestName, newLatestFilename); err != nil {
			return fmt.Errorf("failed to patch migration: %w", err)
		}
	}

	// finally, write a new snapshot after every iteration.
	return Snapshot()
}

// getLatestMigrationName read the migrations directory for the latest migration defined.
func getLatestMigrationName() (string, string) {
	var latestName, latestFilename string
	for _, entry := range stdlo.Must1(os.ReadDir(migrationsDir)) {
		if matched, _ := regexp.MatchString(`([0-9]+)_(.+)\.sql$`, entry.Name()); !matched { //nolint:staticcheck
			continue
		}

		_, latestName, _ = strings.Cut(strings.TrimSuffix(entry.Name(), ".sql"), "_")
		latestFilename = entry.Name()
	}

	return latestName, latestFilename
}
