package stdpgtest_test

import (
	"context"
	"database/sql"
	"testing"

	_ "embed"

	"github.com/advdv/stdgo/stdpgtest"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"
)

type mockDB struct {
	lastSQL string
}

func (mdb *mockDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	mdb.lastSQL = query
	return nil, nil
}

//go:embed testdata/snapshot.sql
var snapshot []byte

func TestSnapshotMigrator(t *testing.T) {
	db := &mockDB{}
	mig := stdpgtest.SnapshotMigrator[*mockDB](snapshot)

	h1, err := mig.Hash()
	require.NoError(t, err)
	require.Equal(t, "b8b7229b3d14d716013c68cf2e1c108733d81ed7bca91a810ac4b4685b2b7097", h1)

	require.NoError(t, mig.Migrate(t.Context(), db, pgtestdb.Config{}))
	require.Contains(t, db.lastSQL, "CREATE TABLE")
}
