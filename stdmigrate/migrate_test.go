package stdmigrate_test

import (
	"context"
	"database/sql"
	"embed"
	"testing"

	stdmigrate "github.com/advdv/stdgo/stdmigrate"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/*.sql
var testData embed.FS

type mockTx1 struct{ lastQuery string }

func (tx *mockTx1) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	tx.lastQuery = query
	return nil, nil
}

func (*mockTx1) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return nil, nil
}
func (*mockTx1) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row { return nil }

func TestExecf(t *testing.T) {
	tx := &mockTx1{}

	stdmigrate.ExecFile(t.Context(), tx, testData, "testdata/some_sql.sql")
}

func TestUp(t *testing.T) {
	tx := &mockTx1{}

	require.NoError(t, stdmigrate.Up(func(ctx context.Context, tx stdmigrate.Tx) {
		stdmigrate.ExecFile(ctx, tx, testData, "testdata/some_sql.sql")
	})(t.Context(), tx))

	require.Equal(t, "CREATE TABLE users();\n\n", tx.lastQuery)
}

func TestUpError(t *testing.T) {
	tx := &mockTx1{}

	require.ErrorContains(t, stdmigrate.Up(func(ctx context.Context, tx stdmigrate.Tx) {
		stdmigrate.ExecFile(ctx, tx, testData, "testdaata/some_sql.sql")
	})(t.Context(), tx), "file does not exist")
}

func TestUpStdNoOp(t *testing.T) {
	tx := &sql.Tx{}

	require.NoError(t, stdmigrate.Up(func(ctx context.Context, tx stdmigrate.Tx) {
	})(t.Context(), tx))
}
