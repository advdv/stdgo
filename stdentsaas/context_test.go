package stdentsaas_test

import (
	"context"
	"testing"

	"github.com/advdv/stdgo/stdentsaas"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedOrganizationsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx = stdentsaas.WithAuthenticatedOrganizations(ctx,
		stdentsaas.OrganizationRole{OrganizationID: "1", Role: "member"},
		stdentsaas.OrganizationRole{OrganizationID: "2", Role: "admin"})

	accs1, ok := stdentsaas.AuthenticatedOrganizations(ctx)
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

	v, ok := stdentsaas.AuthenticatedOrganizations(ctx)
	require.False(t, ok)
	require.Nil(t, v)
}
