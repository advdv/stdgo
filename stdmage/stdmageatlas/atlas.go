// Package stdmageatlas provides mage commands for working with Atlas-based projects.
package stdmageatlas

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/advdv/stdgo/stdlo"
	"github.com/magefile/mage/sh"
)

var (
	inspectEnv    = "inspect"
	localEnv      = "local"
	migrationsDir = "migrations"
	patchHook     PatchFunc
)

// PatchFunc allows users of the mage targets to patch any migration that is generated.
type PatchFunc = func(latestName, latestFilename string) error

// Init inits the mage targets. The weird signature is to make Mage ignore this when importing.
func Init(
	atlasInspectEnv,
	atlasLocalEnv,
	atlasMigrationsDir string,
	hook PatchFunc,
	_ ...[]string, // just here so Mage doesn't recognize a "init" target.
) {
	inspectEnv = atlasInspectEnv
	localEnv = atlasLocalEnv
	migrationsDir = atlasMigrationsDir
	patchHook = hook
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
	return sh.Run("atlas", "migrate", "hash",
		"--env", localEnv)
}

// Diff generates a named migration for the local environment.
func Diff(name string) error {
	return sh.Run("atlas", "migrate", "diff", name,
		"--env", localEnv,
	)
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

	return nil
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
