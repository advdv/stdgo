package stdent_test

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	"github.com/advdv/stdgo/stdent"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedOrganizationsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx = stdent.WithAuthenticatedOrganizations(ctx,
		stdent.OrganizationRole{OrganizationID: "1", Role: "member"},
		stdent.OrganizationRole{OrganizationID: "2", Role: "admin"})

	accs1, ok := stdent.AuthenticatedOrganizations(ctx)
	require.True(t, ok)

	require.Len(t, accs1, 2)
	require.Equal(t, "1", accs1[0].OrganizationID)
	require.Equal(t, "member", accs1[0].Role)
	require.Equal(t, "2", accs1[1].OrganizationID)
	require.Equal(t, "admin", accs1[1].Role)
}

func TestAuthenticatedOrganizationsContextPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	v, ok := stdent.AuthenticatedOrganizations(ctx)
	require.False(t, ok)
	require.Nil(t, v)
}

type testDriver3 struct {
	testDriver1
	executedQuery string
}

func (d *testDriver3) Exec(ctx context.Context, query string, args, v any) error {
	d.executedQuery = query

	return nil
}

func TestAuthenticatedOrganizationsHook(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drv := &testDriver3{}

	ctx = stdent.WithAuthenticatedOrganizations(ctx,
		stdent.OrganizationRole{OrganizationID: "1", Role: "member"},
		stdent.OrganizationRole{OrganizationID: "2", Role: "admin"})

	ctx = stdent.WithAuthenticatedUser(ctx, "a2a0a29c-dbc1-4d0b-b379-afa2af5ab00f")

	require.NoError(t, stdent.NewAuthenticatedTxHook(
		"auth.user",
		"auth.orgs",
		"69d99a0f-ded1-439d-bf61-bbd54c220575",
	)(ctx, dialect.NopTx(drv)))

	require.Contains(t, drv.executedQuery, `SET LOCAL auth.orgs = '[{`)
	require.Contains(t, drv.executedQuery, `SET LOCAL auth.user = 'a2a0a29c-dbc1-4d0b-b379-afa2af5ab00f';`)
}

func TestAuthenticatedOrganizationsHookDefault(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drv := &testDriver3{}

	require.NoError(t, stdent.NewAuthenticatedTxHook(
		"auth.user",
		"auth.orgs",
		"69d99a0f-ded1-439d-bf61-bbd54c220575",
	)(ctx, dialect.NopTx(drv)))
	require.Contains(t, drv.executedQuery, `SET LOCAL auth.orgs = '[]';`)
	require.Contains(t, drv.executedQuery, `SET LOCAL auth.user = '69d99a0f-ded1-439d-bf61-bbd54c220575';`)
}
