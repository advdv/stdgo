// Package stdmigrate provides some utilities for more readable migrations code.
package stdmigrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/advdv/stdgo/stdlo"
)

// Tx interface narrows what we allow to be used for migrations.
type Tx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Exec executes the sql or panics on failure.
func Exec(ctx context.Context, tx Tx, query string, args ...any) sql.Result {
	return stdlo.Must1(tx.ExecContext(ctx, query, args...))
}

// ExecFile rexecutes sql from a file.
func ExecFile(ctx context.Context, tx Tx, files fs.FS, filename string) sql.Result {
	return Exec(ctx, tx, string(stdlo.Must1(fs.ReadFile(files, filename))))
}

// Up is a utility for running migrations such that panics are turned into errors. It gives it more of
// a scripting feel for easier reading.
func Up[T Tx](f func(ctx context.Context, tx T)) func(ctx context.Context, tx T) error {
	return func(ctx context.Context, tx T) (err error) {
		defer func() {
			if e := recover(); e != nil {
				err = fmt.Errorf("failed to migrate: %v", e)
			}
		}()

		f(ctx, tx)

		return
	}
}
