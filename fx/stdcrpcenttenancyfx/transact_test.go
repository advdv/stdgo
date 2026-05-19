package stdcrpcenttenancyfx_test

// Black-box unit tests for the tenant-aware [stdcrpcenttenancyfx.TransactR]
// / [stdcrpcenttenancyfx.TransactR0] forwarders. These tests focus on
// the *integration* between the role-and-managed-tx gate
// ([stdcrpcenttenancyfx.enterManagedTx]) and the underlying
// [stdent.TransactR] routing primitive:
//
//   - With the gate satisfied, the forwarders MUST hand off to the
//     stdent primitive so the routing rule selects ro by default and
//     rw under [stdent.WithReadPromotion].
//   - Without the gate satisfied, the forwarders MUST surface
//     [stdcrpcenttenancyfx.ErrMissingDatabaseRole] before any BeginTx
//     is issued — regardless of whether promotion would have selected
//     ro or rw.
//
// The routing rule itself is covered exhaustively (and without any
// tenancy concerns) in [stdent]'s readpromote_test.go; here we only
// need to prove that the tenancy package threads the right ctx into
// the right primitive call.

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/advdv/stdgo/fx/stdcrpcenttenancyfx"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeTx is a minimal [stdent.Tx]. The assertions in these tests
// look at the parent [fakeClient] counters instead, because that's
// where the routing decision is observable.
type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return sql.ErrTxDone }

// fakeClient counts BeginTx calls so the tests can assert which
// transactor (ro vs rw) the routing rule selected.
type fakeClient struct {
	calls atomic.Int64
}

func (c *fakeClient) BeginTx(context.Context, *entsql.TxOptions) (fakeTx, error) {
	c.calls.Add(1)

	return fakeTx{}, nil
}

// newPair returns a fresh ro / rw pair plus the corresponding
// transactors, mirroring the production wiring shape (two distinct
// pools behind two distinct [stdent.Transactor] instances).
func newPair() (roC, rwC *fakeClient, ro, rw *stdent.Transactor[fakeTx]) {
	roC = &fakeClient{}
	rwC = &fakeClient{}
	ro = stdent.New[fakeTx](roC, stdent.ReadOnly(true))
	rw = stdent.New[fakeTx](rwC)

	return roC, rwC, ro, rw
}

// testCtx returns a ctx with the no-op logger (stdent.Transact1 calls
// stdctx.Log) and a sysuser role on it — the role gate is satisfied
// here so each test can concentrate on the forwarding behaviour.
func testCtx(t *testing.T) context.Context {
	t.Helper()

	ctx := stdctx.WithLogger(t.Context(), zap.NewNop())
	ctx = stdcrpcenttenancyfx.WithDatabaseRole(ctx, stdcrpcenttenancyfx.DatabaseRoleSysuser)

	return ctx
}

func TestTransactR_routes_to_ro_by_default(t *testing.T) {
	t.Parallel()

	// Tenancy TransactR with the gate satisfied must delegate to
	// stdent.TransactR which, with no promotion, opens the ro pool.
	roC, rwC, ro, rw := newPair()

	type in struct{ V int }
	type out struct{ V int }

	got, err := stdcrpcenttenancyfx.TransactR(testCtx(t), ro, rw, &in{V: 7},
		func(_ context.Context, _ fakeTx, inp *in) (*out, error) {
			return &out{V: inp.V * 2}, nil
		})

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 14, got.V, "fn must receive the typed input and return the typed output")
	assert.Equal(t, int64(1), roC.calls.Load(), "ro pool must be driven exactly once")
	assert.Equal(t, int64(0), rwC.calls.Load(), "rw pool MUST NOT be driven without promotion")
}

func TestTransactR_routes_to_rw_when_promoted(t *testing.T) {
	t.Parallel()

	// With [stdent.WithReadPromotion] the forwarder must still hand
	// off to stdent.TransactR, which then swaps pools.
	roC, rwC, ro, rw := newPair()

	ctx := stdent.WithReadPromotion(testCtx(t))

	_, err := stdcrpcenttenancyfx.TransactR(ctx, ro, rw, &struct{}{},
		func(context.Context, fakeTx, *struct{}) (*struct{}, error) {
			return &struct{}{}, nil
		})

	require.NoError(t, err)
	assert.Equal(t, int64(0), roC.calls.Load(), "ro pool MUST NOT be driven when promoted")
	assert.Equal(t, int64(1), rwC.calls.Load(), "rw pool must be driven exactly once when promoted")
}

func TestTransactR0_routes_to_ro_by_default(t *testing.T) {
	t.Parallel()

	roC, rwC, ro, rw := newPair()

	var ran bool

	err := stdcrpcenttenancyfx.TransactR0(testCtx(t), ro, rw,
		func(context.Context, fakeTx) error {
			ran = true
			return nil
		})

	require.NoError(t, err)
	assert.True(t, ran, "inner fn must be invoked")
	assert.Equal(t, int64(1), roC.calls.Load())
	assert.Equal(t, int64(0), rwC.calls.Load())
}

func TestTransactR0_routes_to_rw_when_promoted(t *testing.T) {
	t.Parallel()

	roC, rwC, ro, rw := newPair()

	ctx := stdent.WithReadPromotion(testCtx(t))

	err := stdcrpcenttenancyfx.TransactR0(ctx, ro, rw,
		func(context.Context, fakeTx) error { return nil })

	require.NoError(t, err)
	assert.Equal(t, int64(0), roC.calls.Load())
	assert.Equal(t, int64(1), rwC.calls.Load())
}

func TestTransactR_requires_database_role(t *testing.T) {
	t.Parallel()

	// The role gate inherited from [Transact] must still fire on the
	// R variants — both with and without promotion — otherwise a
	// promoted read could quietly skip the per-tenant role switch.
	roC, rwC, ro, rw := newPair()

	bare := stdctx.WithLogger(t.Context(), zap.NewNop())

	_, err := stdcrpcenttenancyfx.TransactR(bare, ro, rw, &struct{}{},
		func(context.Context, fakeTx, *struct{}) (*struct{}, error) {
			t.Fatalf("fn must not run when the role is missing")
			return nil, nil
		})
	require.ErrorIs(t, err, stdcrpcenttenancyfx.ErrMissingDatabaseRole)

	_, err = stdcrpcenttenancyfx.TransactR(
		stdent.WithReadPromotion(bare), ro, rw, &struct{}{},
		func(context.Context, fakeTx, *struct{}) (*struct{}, error) {
			t.Fatalf("fn must not run when the role is missing (promoted)")
			return nil, nil
		})
	require.ErrorIs(t, err, stdcrpcenttenancyfx.ErrMissingDatabaseRole)

	assert.Equal(t, int64(0), roC.calls.Load(),
		"missing-role rejection must happen before any BeginTx")
	assert.Equal(t, int64(0), rwC.calls.Load(),
		"missing-role rejection must happen before any BeginTx")
}

func TestTransactR0_requires_database_role(t *testing.T) {
	t.Parallel()

	roC, rwC, ro, rw := newPair()

	bare := stdctx.WithLogger(t.Context(), zap.NewNop())

	err := stdcrpcenttenancyfx.TransactR0(bare, ro, rw,
		func(context.Context, fakeTx) error {
			t.Fatalf("fn must not run when the role is missing")
			return nil
		})
	require.ErrorIs(t, err, stdcrpcenttenancyfx.ErrMissingDatabaseRole)

	err = stdcrpcenttenancyfx.TransactR0(
		stdent.WithReadPromotion(bare), ro, rw,
		func(context.Context, fakeTx) error {
			t.Fatalf("fn must not run when the role is missing (promoted)")
			return nil
		})
	require.ErrorIs(t, err, stdcrpcenttenancyfx.ErrMissingDatabaseRole)

	assert.Equal(t, int64(0), roC.calls.Load(),
		"missing-role rejection must happen before any BeginTx")
	assert.Equal(t, int64(0), rwC.calls.Load(),
		"missing-role rejection must happen before any BeginTx")
}
