package stdent_test

import (
	"context"
	"database/sql"
	"testing"

	"entgo.io/ent/dialect"
	"github.com/advdv/stdgo/stdent"
	"github.com/stretchr/testify/require"
)

type testDriver1 struct{ dialect.Driver }

// Tx calls the base driver's method with the same symbol and invokes our hook.
func (d testDriver1) Tx(context.Context) (dialect.Tx, error) {
	return nil, nil
}

type testDriver2 struct{ testDriver1 }

func (d testDriver2) BeginTx(context.Context, *sql.TxOptions) (dialect.Tx, error) {
	return nil, nil
}

func TestHookDriverTx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	base, called := testDriver1{}, false
	hooked := stdent.NewTxHookDriver(base, func(ctx context.Context, tx dialect.Tx) error {
		called = true
		return nil
	})

	_, err := hooked.Tx(ctx)
	require.NoError(t, err)
	require.True(t, called)

	_, err = hooked.BeginTx(ctx, &sql.TxOptions{})
	require.ErrorContains(t, err, "is not supported")
}

func TestHookDriverBeginTx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	base, called := testDriver2{}, false
	hooked := stdent.NewTxHookDriver(base, func(ctx context.Context, tx dialect.Tx) error {
		called = true
		return nil
	})

	_, err := hooked.BeginTx(ctx, &sql.TxOptions{})
	require.NoError(t, err)
	require.True(t, called)
}
