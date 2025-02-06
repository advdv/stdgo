package stdentsaas_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	entdialect "entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/advdv/stdgo/stdentsaas"
	"github.com/stretchr/testify/require"
)

type testDriver1 struct{ entdialect.Driver }

func (d testDriver1) Tx(context.Context) (entdialect.Tx, error) {
	return nil, nil
}

type testTx1 struct {
	entdialect.Tx
	sqls []string
}

func (tx *testTx1) Exec(_ context.Context, sql string, _, _ any) error {
	tx.sqls = append(tx.sqls, sql)
	return nil
}

type testDriver2 struct {
	entdialect.Driver
	calledOpts *sql.TxOptions
}

func (d *testDriver2) BeginTx(_ context.Context, opts *sql.TxOptions) (entdialect.Tx, error) {
	d.calledOpts = opts

	return &testTx1{}, nil
}

func readTxSettings(t *testing.T, ctx context.Context, tx entdialect.Tx) (
	string, string, string, string,
) {
	t.Helper()

	var rows entsql.Rows
	require.NoError(t, tx.Query(ctx, `SELECT current_setting('auth.user_id')`, []any{}, &rows))
	currentUserID, err := entsql.ScanString(rows)
	require.NoError(t, err)

	require.NoError(t, tx.Query(ctx, `SELECT current_setting('auth.organizations')`, []any{}, &rows))
	currentOrgs, err := entsql.ScanString(rows)
	require.NoError(t, err)

	require.NoError(t, tx.Query(ctx, `SHOW transaction_isolation`, []any{}, &rows))
	currentIsolation, err := entsql.ScanString(rows)
	require.NoError(t, err)

	require.NoError(t, tx.Query(ctx, `SHOW transaction_timeout`, []any{}, &rows))
	transactionTimeout, err := entsql.ScanString(rows)
	require.NoError(t, err)

	return currentUserID, currentOrgs, currentIsolation, transactionTimeout
}

func TestDriverWithoutAuth(t *testing.T) {
	ctx := setup(t)
	tx := setupTx(t, ctx, 0)
	currentUserID, currentOrgs, currentIsolation, transactionTimeout := readTxSettings(t, ctx, tx)
	require.Equal(t, "", currentUserID)
	require.Equal(t, "[]", currentOrgs)
	require.Equal(t, "serializable", currentIsolation)
	require.Equal(t, "0", transactionTimeout)
}

func TestDriverWithAuth(t *testing.T) {
	ctx := setup(t, time.Second*10)

	ctx = stdentsaas.WithAuthenticatedOrganizations(ctx,
		stdentsaas.OrganizationRole{OrganizationID: "1", Role: "member"},
		stdentsaas.OrganizationRole{OrganizationID: "2", Role: "admin"})
	ctx = stdentsaas.WithAuthenticatedUser(ctx, "a2a0a29c-dbc1-4d0b-b379-afa2af5ab00f")

	tx := setupTx(t, ctx, 0)
	currentUserID, currentOrgs, currentIsolation, transactionTimeout := readTxSettings(t, ctx, tx)
	require.Equal(t, "a2a0a29c-dbc1-4d0b-b379-afa2af5ab00f", currentUserID)
	require.JSONEq(t, "[{\"organization_id\":\"1\",\"role\":\"member\"},{\"organization_id\":\"2\",\"role\":\"admin\"}]", currentOrgs)
	require.Equal(t, "serializable", currentIsolation)
	require.Contains(t, transactionTimeout, "ms")
}

func TestNonLinearlizable(t *testing.T) {
	ctx := setup(t)
	base := &testDriver2{}
	wrapped := stdentsaas.NewDriver(base,
		stdentsaas.AuthenticatedUserSetting("auth.u"),
		stdentsaas.AuthenticatedOrganizationsSetting("auth.o"),
		stdentsaas.AnonymousUserID(""))

	_, err := wrapped.BeginTx(ctx, &sql.TxOptions{})
	require.ErrorContains(t, err, "only serializable")

	err = wrapped.Exec(ctx, "", nil, nil)
	require.ErrorContains(t, err, "is not supported")

	err = wrapped.Query(ctx, "", nil, nil)
	require.ErrorContains(t, err, "is not supported")
}

func TestNoBeginTx(t *testing.T) {
	ctx := setup(t)
	base := &testDriver1{}
	wrapped := stdentsaas.NewDriver(base,
		stdentsaas.AuthenticatedUserSetting("auth.u"),
		stdentsaas.AuthenticatedOrganizationsSetting("auth.o"),
		stdentsaas.AnonymousUserID(""))

	_, err := wrapped.BeginTx(ctx, &sql.TxOptions{})
	require.ErrorContains(t, err, "not supported")
}
