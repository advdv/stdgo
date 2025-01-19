// Package stdentsaas provides re-usable SaaS code using Ent as the ORM.
package stdentsaas

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	entdialect "entgo.io/ent/dialect"
)

type DriverOption func(*Driver)

// AuthenticatedUserSetting configures the transaction-scoped postgres setting
// that will cary which user is authenticated for.
func AuthenticatedUserSetting(s string) DriverOption {
	return func(d *Driver) {
		d.userSetting = s
	}
}

// AuthenticatedOrganizationsSetting configures the transaction-scoped postgres setting
// that will cary which organizations the user is autthenticated for.
func AuthenticatedOrganizationsSetting(s string) DriverOption {
	return func(d *Driver) {
		d.orgsSetting = s
	}
}

// AnonymousUserID configures the id that will be used in the setting when the user
// is not authenticated.
func AnonymousUserID(s string) DriverOption {
	return func(d *Driver) {
		d.anonUserID = s
	}
}

// Driver is an opionated Ent driver that wraps a base driver but only allows interactions
// with the database to be done through a transaction with specific isolation
// properties and auth settings applied properly.
type Driver struct {
	entdialect.Driver

	userSetting      string
	orgsSetting      string
	anonUserID       string
	timeoutExtension time.Duration
}

// NewDriver inits the driver.
func NewDriver(
	base entdialect.Driver,
	opts ...DriverOption,
) *Driver {
	drv := &Driver{Driver: base}
	for _, opt := range opts {
		opt(drv)
	}

	// if a transaction is created in the scope of a context.Context with a deadline
	// the postgres's transaction timeout is set accordingly. But with some extended
	// window so it doesn't terminate from the server while the request is shutting down.
	if drv.timeoutExtension == 0 {
		drv.timeoutExtension = time.Second * 20
	}

	return drv
}

// Exec executes a query that does not return records. For example, in SQL, INSERT or UPDATE.
// It scans the result into the pointer v. For SQL drivers, it is dialect/sql.Result.
func (d Driver) Exec(_ context.Context, _ string, _, _ any) error {
	return fmt.Errorf("Driver.Exec is not supported: create a transaction instead")
}

// Query executes a query that returns rows, typically a SELECT in SQL.
// It scans the result into the pointer v. For SQL drivers, it is *dialect/sql.Rows.
func (d Driver) Query(_ context.Context, _ string, _, _ any) error {
	return fmt.Errorf("Driver.Query is not supported: create a transaction instead")
}

// Tx will begin a transaction with linearizable isolation level.
func (d Driver) Tx(ctx context.Context) (entdialect.Tx, error) {
	return d.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
}

// BeginTx calls the base driver's method if it's supported and calls our hook.
func (d Driver) BeginTx(ctx context.Context, opts *sql.TxOptions) (entdialect.Tx, error) {
	drv, ok := d.Driver.(interface {
		BeginTx(ctx context.Context, opts *sql.TxOptions) (entdialect.Tx, error)
	})
	if !ok {
		return nil, fmt.Errorf("Driver.BeginTx is not supported")
	}

	if opts.Isolation != sql.LevelSerializable {
		return nil, fmt.Errorf("only serializable (most strict) isolation level is allowed")
	}

	tx, err := drv.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}

	if err := d.setupTx(ctx, tx); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("failed to setup tx, rolled back: %w", err)
	}

	return tx, nil
}

// setupTx preforms shared transaction setup.
func (d Driver) setupTx(ctx context.Context, tx entdialect.Tx) error {
	accsOrgs, ok := AuthenticatedOrganizations(ctx)
	if !ok {
		accsOrgs = []OrganizationRole{} // so the setting is never 'null'
	}

	jsond, err := json.Marshal(accsOrgs)
	if err != nil {
		return fmt.Errorf("failed to marshal authenticated_organizations json: %w", err)
	}

	accsUserID, ok := AuthenticatedUser(ctx)
	if !ok {
		accsUserID = d.anonUserID
	}

	// set transaction-local settings to be used for authorization (policies)
	var sql strings.Builder
	sql.WriteString(fmt.Sprintf(`SET LOCAL %s = '%s';`, d.userSetting, accsUserID))
	sql.WriteString(fmt.Sprintf(`SET LOCAL %s = '%s';`, d.orgsSetting, string(jsond)))

	// if the context has a deadline we limit the transaction to that timeout.
	dl, ok := ctx.Deadline()
	if ok {
		sql.WriteString(fmt.Sprintf(`SET LOCAL transaction_timeout = %d;`,
			(time.Until(dl) + d.timeoutExtension).Milliseconds()))
	}

	if err := tx.Exec(ctx, sql.String(), []any{}, nil); err != nil {
		return fmt.Errorf("failed to set authenticated setting: %w", err)
	}

	return nil
}

var (
	// make sure our driver implements the ent driver.
	_ entdialect.Driver = &Driver{}

	// this interface may also be asserted for if users want to change transaction options.
	_ interface {
		BeginTx(ctx context.Context, opts *sql.TxOptions) (entdialect.Tx, error)
	} = &Driver{}
)
