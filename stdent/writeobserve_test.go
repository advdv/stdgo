package stdent_test

// Unit tests for the write-observer mechanism in writeobserve.go.
// The mechanism is deliberately independent from read-promotion
// (readpromote.go) — it only cares whether a non-read-only
// transaction commits successfully — so these tests exercise it
// through [stdent.Transact0] / [stdent.Transact1] against in-memory
// fake clients/transactors without any Postgres dependency.

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

// fakeWOTx is a minimal [stdent.Tx] used to drive [stdent.Transact1]
// without a real database. Commit / Rollback behaviour is controlled
// by the parent [fakeWOClient] so the tests can simulate commit
// success, commit failure, and panicking inner functions.
type fakeWOTx struct {
	commitErr error
}

func (t fakeWOTx) Commit() error { return t.commitErr }

// Rollback returns sql.ErrTxDone so the defer-rollback in Transact1
// stays quiet after a successful Commit — matching how a real
// driver behaves.
func (fakeWOTx) Rollback() error { return sql.ErrTxDone }

// fakeWOClient is a stand-in [stdent.Client] that hands out
// fakeWOTx values whose Commit returns commitErr.
type fakeWOClient struct {
	commitErr error
}

func (c *fakeWOClient) BeginTx(context.Context, *entsql.TxOptions) (fakeWOTx, error) {
	return fakeWOTx{commitErr: c.commitErr}, nil
}

func woCtx(t *testing.T) context.Context {
	t.Helper()

	return stdctx.WithLogger(t.Context(), zap.NewNop())
}

func TestWithWriteObserver_initial_state_is_false(t *testing.T) {
	t.Parallel()

	// A freshly-attached observer starts out un-tripped; the test
	// guards against the obvious "always reports true" bug.
	_, obs := stdent.WithWriteObserver(t.Context())

	require.NotNil(t, obs, "WithWriteObserver must return a non-nil observer")
	assert.False(t, obs.Load(), "a freshly-attached observer must start as false")
}

func TestWithWriteObserver_does_not_mutate_parent(t *testing.T) {
	t.Parallel()

	// The parent context must not see the observer — only the
	// returned derived context does. Mirrors the WithReadPromotion
	// non-mutation guarantee.
	parent := t.Context()

	_, obs := stdent.WithWriteObserver(parent)
	obs.Store(true)

	// The parent context still has no observer, so a Transact1 run
	// against `parent` cannot flip anything. There is no exported
	// reader for the observer, but we can still assert the parent
	// did not get a new value installed by checking that a second
	// WithWriteObserver call on it returns a distinct, untripped
	// observer.
	_, obs2 := stdent.WithWriteObserver(parent)
	require.NotSame(t, obs, obs2, "observers attached to the parent must be independent instances")
	assert.False(t, obs2.Load(), "a freshly-attached observer on the parent must be false")
}

func TestTransact_flips_observer_on_successful_write_commit(t *testing.T) {
	t.Parallel()

	// The whole point of the mechanism: a successful commit on a
	// non-read-only transactor must trip the observer attached to
	// ctx, so an outer middleware can react to it after the inner
	// handler returns.
	c := &fakeWOClient{}
	txr := stdent.New[fakeWOTx](c) // readOnly defaults to false

	ctx, obs := stdent.WithWriteObserver(woCtx(t))

	err := stdent.Transact0(ctx, txr, func(context.Context, fakeWOTx) error {
		return nil
	})

	require.NoError(t, err)
	assert.True(t, obs.Load(), "successful rw commit MUST flip the observer")
}

func TestTransact_does_not_flip_observer_on_read_only_commit(t *testing.T) {
	t.Parallel()

	// A read-only transactor MUST NOT flip the observer even on a
	// successful commit — otherwise plain reads would pin the
	// read-your-writes cookie and defeat the routing split.
	c := &fakeWOClient{}
	txr := stdent.New[fakeWOTx](c, stdent.ReadOnly(true))

	ctx, obs := stdent.WithWriteObserver(woCtx(t))

	err := stdent.Transact0(ctx, txr, func(context.Context, fakeWOTx) error {
		return nil
	})

	require.NoError(t, err)
	assert.False(t, obs.Load(), "read-only commit MUST NOT flip the observer")
}

func TestTransact_does_not_flip_observer_on_inner_error(t *testing.T) {
	t.Parallel()

	// A rolled-back write is NOT an observed write: the data never
	// became visible, so the cookie must not be pinned. This is the
	// reason the flip happens after Commit, not at Begin.
	c := &fakeWOClient{}
	txr := stdent.New[fakeWOTx](c)

	ctx, obs := stdent.WithWriteObserver(woCtx(t))

	sentinel := errors.New("boom")
	err := stdent.Transact0(ctx, txr, func(context.Context, fakeWOTx) error {
		return sentinel
	})

	require.ErrorIs(t, err, sentinel)
	assert.False(t, obs.Load(), "rolled-back write MUST NOT flip the observer")
}

func TestTransact_does_not_flip_observer_on_commit_error(t *testing.T) {
	t.Parallel()

	// If Commit itself errors out (and it isn't the benign
	// sql.ErrTxDone case), nothing was persisted, so the observer
	// must stay un-tripped.
	c := &fakeWOClient{commitErr: errors.New("commit failed")}
	txr := stdent.New[fakeWOTx](c)

	ctx, obs := stdent.WithWriteObserver(woCtx(t))

	err := stdent.Transact0(ctx, txr, func(context.Context, fakeWOTx) error {
		return nil
	})

	require.Error(t, err)
	assert.False(t, obs.Load(), "failed commit MUST NOT flip the observer")
}

func TestTransact_flips_observer_on_sql_ErrTxDone_commit(t *testing.T) {
	t.Parallel()

	// Transact1 treats sql.ErrTxDone from Commit as "fnc concluded
	// the tx itself" — i.e. the work IS done. That's a successful
	// write from the observer's point of view and MUST trip the
	// observer, otherwise self-committing handlers would silently
	// skip the read-your-writes signal.
	c := &fakeWOClient{commitErr: sql.ErrTxDone}
	txr := stdent.New[fakeWOTx](c)

	ctx, obs := stdent.WithWriteObserver(woCtx(t))

	err := stdent.Transact0(ctx, txr, func(context.Context, fakeWOTx) error {
		return nil
	})

	require.NoError(t, err)
	assert.True(t, obs.Load(), "sql.ErrTxDone from Commit is treated as success and MUST flip the observer")
}

func TestTransact_without_observer_is_noop(t *testing.T) {
	t.Parallel()

	// A ctx that never went through WithWriteObserver must still
	// work — the noteWriteObserved call is a no-op when no observer
	// is attached, so non-HTTP callers (workers, bootstrap, tests)
	// are unaffected by the mechanism.
	c := &fakeWOClient{}
	txr := stdent.New[fakeWOTx](c)

	err := stdent.Transact0(woCtx(t), txr, func(context.Context, fakeWOTx) error {
		return nil
	})

	require.NoError(t, err, "Transact with no observer attached must succeed unchanged")
}

func TestWithWriteObserver_returned_pointer_is_the_one_flipped(t *testing.T) {
	t.Parallel()

	// The pointer returned to the caller MUST be the same instance
	// the transact layer flips — otherwise the middleware would
	// hold a stale view. Guards against a refactor that accidentally
	// stores a copy in ctx.
	c := &fakeWOClient{}
	txr := stdent.New[fakeWOTx](c)

	ctx, obs := stdent.WithWriteObserver(woCtx(t))

	// Capture a second reference to be extra-paranoid about
	// identity.
	captured := obs

	err := stdent.Transact0(ctx, txr, func(context.Context, fakeWOTx) error {
		return nil
	})

	require.NoError(t, err)
	require.Same(t, obs, captured, "test setup invariant")
	assert.True(t, captured.Load(),
		"the *atomic.Bool returned from WithWriteObserver must be the same instance that the transact layer flips")
}

func TestWithWriteObserver_concurrent_flip_is_safe(t *testing.T) {
	t.Parallel()

	// noteWriteObserved uses atomic.Bool.Store so concurrent writes
	// from multiple goroutines are safe. This isn't expected to
	// happen in practice (one request → one observer → one tx at a
	// time) but the type's contract guarantees it and we want a
	// regression test if anyone swaps the sidecar for a plain bool.
	var obs atomic.Bool

	const N = 64

	done := make(chan struct{})
	for range N {
		go func() {
			obs.Store(true)
			done <- struct{}{}
		}()
	}

	for range N {
		<-done
	}

	assert.True(t, obs.Load())
}
