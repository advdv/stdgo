package stdent_test

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	"github.com/advdv/stdgo/stdent"
	"github.com/stretchr/testify/require"
)

func TestAccessibleOrganizationsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx = stdent.WithAccessibleOrganizations(ctx,
		stdent.OrganizationRole{OrganizationID: "1", Role: "member"},
		stdent.OrganizationRole{OrganizationID: "2", Role: "admin"})

	accs1 := stdent.AccessibleOrganizations(ctx)
	require.Len(t, accs1, 2)
	require.Equal(t, "1", accs1[0].OrganizationID)
	require.Equal(t, "member", accs1[0].Role)
	require.Equal(t, "2", accs1[1].OrganizationID)
	require.Equal(t, "admin", accs1[1].Role)
}

func TestAccessibleOrganizationsContextPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.PanicsWithValue(t, "stdent: context doesn't contain accessible organizations", func() {
		stdent.AccessibleOrganizations(ctx)
	})
}

type testDriver3 struct {
	testDriver1
	executedQuery string
}

func (d *testDriver3) Exec(ctx context.Context, query string, args, v any) error {
	d.executedQuery = query

	return nil
}

func TestAccessibleOrganizationsHook(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drv := &testDriver3{}

	ctx = stdent.WithAccessibleOrganizations(ctx,
		stdent.OrganizationRole{OrganizationID: "1", Role: "member"},
		stdent.OrganizationRole{OrganizationID: "2", Role: "admin"})

	require.NoError(t, stdent.NewAccessibleOrganizationsTxHook("foo.bar")(ctx, dialect.NopTx(drv)))
	require.Contains(t, drv.executedQuery, `SET LOCAL foo.bar = '[{`)
}
