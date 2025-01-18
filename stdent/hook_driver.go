// Package stdent provides re-usable code for interacting with the Ent orm.
package stdent

import (
	"context"
	"database/sql"
	"fmt"

	entdialect "entgo.io/ent/dialect"
)

// TxHookDriver is an Ent driver that wraps a base driver to allow a hook to
// be configured for every transaction that is started. Useful for sql
// settings scoped to the transaction such as the current_user_id, or the role.
type TxHookDriver struct {
	entdialect.Driver
	hook TxHookFunc
}

type TxHookFunc func(ctx context.Context, tx entdialect.Tx) error

func NewTxHookDriver(base entdialect.Driver, hook TxHookFunc) *TxHookDriver {
	return &TxHookDriver{Driver: base, hook: hook}
}

func (d TxHookDriver) withHook(ctx context.Context, tx entdialect.Tx) (entdialect.Tx, error) {
	if err := d.hook(ctx, tx); err != nil {
		return nil, fmt.Errorf("failed to run hook: %w", err)
	}

	return tx, nil
}

// Tx calls the base driver's method with the same symbol and invokes our hook.
func (d TxHookDriver) Tx(ctx context.Context) (entdialect.Tx, error) {
	tx, err := d.Driver.Tx(ctx)
	if err != nil {
		return nil, err
	}

	return d.withHook(ctx, tx)
}

// BeginTx calls the base driver's method if it's supported and calls our hook.
func (d TxHookDriver) BeginTx(ctx context.Context, opts *sql.TxOptions) (entdialect.Tx, error) {
	drv, ok := d.Driver.(interface {
		BeginTx(ctx context.Context, opts *sql.TxOptions) (entdialect.Tx, error)
	})
	if !ok {
		return nil, fmt.Errorf("Driver.BeginTx is not supported")
	}

	tx, err := drv.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}

	return d.withHook(ctx, tx)
}

var (
	_ entdialect.Driver = &TxHookDriver{}

	// this interface may also be asserted for if users want to change transaction options.
	_ interface {
		BeginTx(ctx context.Context, opts *sql.TxOptions) (entdialect.Tx, error)
	} = &TxHookDriver{}
)
