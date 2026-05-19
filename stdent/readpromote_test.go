package stdent_test

// Unit tests for the read-promotion routing primitive added in
// readpromote.go. They use an in-memory stub [stdent.Client] /
// [stdent.Tx] so the routing rule is exercised in isolation, with
// no Postgres dependency — the integration-grade transact tests in
// transact_test.go cover the underlying [stdent.Transact1] behaviour.

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeRPTx is a minimal [stdent.Tx]. Commit / Rollback are both
// no-ops (Rollback reports sql.ErrTxDone so the defer-rollback in
// Transact1 stays quiet after a successful Commit). All assertions
// go through the parent [fakeRPClient] counters.
type fakeRPTx struct{}

func (fakeRPTx) Commit() error   { return nil }
func (fakeRPTx) Rollback() error { return sql.ErrTxDone }

// fakeRPClient counts BeginTx calls so the tests can observe which
// transactor (ro vs rw) the routing rule selected.
type fakeRPClient struct {
	calls atomic.Int64
}

func (c *fakeRPClient) BeginTx(context.Context, *entsql.TxOptions) (fakeRPTx, error) {
	c.calls.Add(1)

	return fakeRPTx{}, nil
}

// newRPPair builds a fresh ro / rw pair of fake clients + their
// transactors, mirroring the production wiring shape (two distinct
// pools behind two distinct [stdent.Transactor] instances).
func newRPPair() (roC, rwC *fakeRPClient, ro, rw *stdent.Transactor[fakeRPTx]) {
	roC = &fakeRPClient{}
	rwC = &fakeRPClient{}
	ro = stdent.New[fakeRPTx](roC, stdent.ReadOnly(true))
	rw = stdent.New[fakeRPTx](rwC)

	return roC, rwC, ro, rw
}

func rpCtx(t *testing.T) context.Context {
	t.Helper()

	return stdctx.WithLogger(t.Context(), zap.NewNop())
}

func TestWithReadPromotion_marker_roundtrip(t *testing.T) {
	t.Parallel()

	// HasReadPromotion is exported precisely so tests / diagnostics
	// can observe the bit independently of the Transact* call sites.
	ctx := t.Context()

	assert.False(t, stdent.HasReadPromotion(ctx),
		"a plain ctx must not be reported as read-promoted")

	promoted := stdent.WithReadPromotion(ctx)
	assert.True(t, stdent.HasReadPromotion(promoted),
		"WithReadPromotion must stamp ctx so HasReadPromotion returns true")

	assert.False(t, stdent.HasReadPromotion(ctx),
		"WithReadPromotion must not mutate the parent context")
}

func TestTransactR_routes_to_ro_by_default(t *testing.T) {
	t.Parallel()

	// Without a [WithReadPromotion] stamp the routing rule MUST keep
	// reads on the read-only pool — that's the whole point of having
	// two pools in the first place.
	roC, rwC, ro, rw := newRPPair()

	type in struct{ V int }
	type out struct{ V int }

	got, err := stdent.TransactR(rpCtx(t), ro, rw, &in{V: 7},
		func(_ context.Context, _ fakeRPTx, inp *in) (*out, error) {
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

	// With the [WithReadPromotion] stamp the rule MUST swap pools so
	// the caller gets read-your-writes consistency. This is the
	// single assertion that proves the promotion bit actually
	// changes the begin target.
	roC, rwC, ro, rw := newRPPair()

	ctx := stdent.WithReadPromotion(rpCtx(t))

	_, err := stdent.TransactR(ctx, ro, rw, &struct{}{},
		func(context.Context, fakeRPTx, *struct{}) (*struct{}, error) {
			return &struct{}{}, nil
		})

	require.NoError(t, err)
	assert.Equal(t, int64(0), roC.calls.Load(), "ro pool MUST NOT be driven when promoted")
	assert.Equal(t, int64(1), rwC.calls.Load(), "rw pool must be driven exactly once when promoted")
}

func TestTransactR0_routes_to_ro_by_default(t *testing.T) {
	t.Parallel()

	roC, rwC, ro, rw := newRPPair()

	var ran bool

	err := stdent.TransactR0(rpCtx(t), ro, rw,
		func(context.Context, fakeRPTx) error {
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

	roC, rwC, ro, rw := newRPPair()

	ctx := stdent.WithReadPromotion(rpCtx(t))

	err := stdent.TransactR0(ctx, ro, rw,
		func(context.Context, fakeRPTx) error { return nil })

	require.NoError(t, err)
	assert.Equal(t, int64(0), roC.calls.Load())
	assert.Equal(t, int64(1), rwC.calls.Load())
}

func TestTransactR_propagates_inner_error_without_touching_other_pool(t *testing.T) {
	t.Parallel()

	// A failure inside the inner fn must surface to the caller and
	// must NOT cause a retry against the OTHER pool — the routing
	// decision is one-shot per invocation.
	roC, rwC, ro, rw := newRPPair()

	sentinel := errors.New("boom")

	_, err := stdent.TransactR(rpCtx(t), ro, rw, &struct{}{},
		func(context.Context, fakeRPTx, *struct{}) (*struct{}, error) {
			return nil, sentinel
		})

	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, int64(1), roC.calls.Load(),
		"the chosen pool is opened exactly once even on inner error")
	assert.Equal(t, int64(0), rwC.calls.Load(),
		"the unchosen pool MUST stay untouched on inner error")
}
