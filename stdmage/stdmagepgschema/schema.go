// Package stdmagepgschema provides standardized database schema handling.
package stdmagepgschema

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/advdv/stdgo/stdlo"
	"github.com/advdv/stdgo/stdmage"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/magefile/mage/sh"
	"github.com/pressly/goose/v3"
)

var migrationsDir = "not_initialized"

// Init inits the mage targets. The weird signature is to make Mage ignore this when importing.
func Init(dir string, _ ...[]string) {
	migrationsDir = dir

	if _, err := os.Stat(migrationsDir); err != nil {
		panic("failed to stat migrations dir '" + migrationsDir + "', make sure it exists")
	}
}

// Status returns the database status.
func Status(ctx context.Context, env string) error {
	return runWithSQL(ctx, env, func(ctx context.Context, db *sql.DB) error {
		if err := goose.RunContext(ctx, "status", db, migrationsDir); err != nil {
			return fmt.Errorf("failed to run goose: %w", err)
		}

		return nil
	})
}

// Up the schema up status.
func Up(ctx context.Context, env string) error {
	return runWithSQL(ctx, env, func(ctx context.Context, db *sql.DB) error {
		if err := goose.RunContext(ctx, "up", db, migrationsDir); err != nil {
			return fmt.Errorf("failed to run goose: %w", err)
		}

		return nil
	})
}

// Snapshot a cleanly migrated database into a sql file.
func Snapshot(ctx context.Context) error {
	if err := stdmage.LoadEnv("dev"); err != nil {
		return fmt.Errorf("failed to load env: %w", err)
	}

	// we reset the database by restarting it since it runs on an in-memory disk.
	if err := sh.Run(`docker`, `compose`, `restart`, `postgres`); err != nil {
		return fmt.Errorf("failed to restart postgres to clean it: %w", err)
	}

	if _, err := failsafe.Get(func() (bool, error) {
		if err := Up(ctx, "dev"); err != nil {
			return false, fmt.Errorf("failed to migrated database: %w", err)
		}

		return true, nil
	}, retrypolicy.
		Builder[bool]().
		WithDelay(time.Millisecond*500).
		WithMaxDuration(time.Second*10).
		Build()); err != nil {
		return fmt.Errorf("failed to retry: %w", err)
	}

	// run pg_dump to sql representation
	ccfg := stdlo.Must1(pgx.ParseConfig(os.Getenv("DATABASE_URL")))
	stdlo.Must0(os.MkdirAll(filepath.Join("migrations", "snapshot"), 0o744))
	if err := sh.Run("docker", "run",
		"--rm", "--network", "host",
		"-v", filepath.Join(stdlo.Must1(os.Getwd()), migrationsDir)+":/migrations",
		"-e", "PGPASSWORD=postgres",
		"postgres:17.2-alpine", "pg_dump",
		"-h", "localhost",
		"-p", fmt.Sprintf("%d", ccfg.Port),
		"-U", "postgres",
		"--schema-only",
		"--no-comments",
		"--no-owner", // remove some comments
		"-b", "-v", "-f", "/migrations/snapshot/snapshot.sql", "postgres"); err != nil {
		return fmt.Errorf("failed to run pg_dump: %w", err)
	}

	// remove comments to shrink the file and increase the change it fits in a LLM
	if err := sh.Run("sed", "-i", "", "/^--/d;/^$/d",
		filepath.Join(migrationsDir, "snapshot", "snapshot.sql"),
	); err != nil {
		return fmt.Errorf("failed to clean comments and empty lines: %w", err)
	}

	return nil
}

func runWithSQL(ctx context.Context, env string, runf func(ctx context.Context, db *sql.DB) error) error {
	if err := stdmage.LoadEnv(env); err != nil {
		return fmt.Errorf("failed to load env: %w", err)
	}

	connCfg, err := pgx.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		return fmt.Errorf("failed to parse database config: %w", err)
	}

	db := stdlib.OpenDB(*connCfg)
	defer db.Close()

	return runf(ctx, db)
}
