package stdcrpcenttenancyfx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdcrpcenttenancyfx"
	"github.com/stretchr/testify/assert"
)

func TestWithDatabaseRole_marker_roundtrip(t *testing.T) {
	t.Parallel()

	// WithDatabaseRole / DatabaseRoleFromContext are the public
	// stamper / reader pair. Production callers either go through the
	// stdcrpcenttenancyfx Connect interceptor (which uses these
	// internally) or call WithDatabaseRole directly when there is no
	// inbound RPC to drive the interceptor (Temporal activities,
	// system bootstrap, test seed helpers).
	ctx := t.Context()

	role, ok := stdcrpcenttenancyfx.DatabaseRoleFromContext(ctx)
	assert.False(t, ok, "plain context must carry no role")
	assert.Equal(t, stdcrpcenttenancyfx.DatabaseRoleUnspecified, role,
		"missing role must read as the zero value")

	for _, want := range []stdcrpcenttenancyfx.DatabaseRole{
		stdcrpcenttenancyfx.DatabaseRoleAnonymous,
		stdcrpcenttenancyfx.DatabaseRoleWebuser,
		stdcrpcenttenancyfx.DatabaseRoleSysuser,
	} {
		wrapped := stdcrpcenttenancyfx.WithDatabaseRole(ctx, want)

		got, ok := stdcrpcenttenancyfx.DatabaseRoleFromContext(wrapped)
		assert.True(t, ok, "wrapped context must carry a role for %s", want)
		assert.Equal(t, want, got)

		// The marker must not leak back into the parent context.
		_, ok = stdcrpcenttenancyfx.DatabaseRoleFromContext(ctx)
		assert.False(t, ok, "wrapping must not mutate the parent context")
	}
}
