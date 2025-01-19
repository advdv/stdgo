package stdentsaas_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/advdv/stdgo/stdentsaas"
	"github.com/stretchr/testify/require"
)

type testDriver1 struct{ dialect.Driver }

// Tx calls the base driver's method with the same symbol and invokes our hook.
func (d testDriver1) Tx(context.Context) (dialect.Tx, error) {
	return nil, nil
}

type testTx1 struct {
	dialect.Tx
	sqls []string
}

func (tx *testTx1) Exec(_ context.Context, sql string, _, _ any) error {
	tx.sqls = append(tx.sqls, sql)
	return nil
}

type testDriver2 struct {
	dialect.Driver
	calledOpts *sql.TxOptions
}

func (d *testDriver2) BeginTx(_ context.Context, opts *sql.TxOptions) (dialect.Tx, error) {
	d.calledOpts = opts

	return &testTx1{}, nil
}

func TestDriverWithoutAuth(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := &testDriver2{}
	wrapped := stdentsaas.NewDriver(base,
		stdentsaas.AuthenticatedUserSetting("auth.u"),
		stdentsaas.AuthenticatedOrganizationsSetting("auth.o"),
		stdentsaas.AnonymousUserID(""),
	)

	tx1, err := wrapped.Tx(ctx)
	require.NoError(t, err)
	require.Equal(t, &sql.TxOptions{Isolation: sql.LevelLinearizable}, base.calledOpts)
	require.Equal(t, []string{`SET LOCAL auth.u = '';SET LOCAL auth.o = '[]';`}, tx1.(*testTx1).sqls)

	err = wrapped.Exec(ctx, "", nil, nil)
	require.ErrorContains(t, err, "is not supported")

	err = wrapped.Query(ctx, "", nil, nil)
	require.ErrorContains(t, err, "is not supported")
}

func TestDriverWithNonOptionals(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	base := &testDriver2{}
	wrapped := stdentsaas.NewDriver(base,
		stdentsaas.AuthenticatedUserSetting("auth.u"),
		stdentsaas.AuthenticatedOrganizationsSetting("auth.o"),
		stdentsaas.AnonymousUserID(""))

	ctx = stdentsaas.WithAuthenticatedOrganizations(ctx,
		stdentsaas.OrganizationRole{OrganizationID: "1", Role: "member"},
		stdentsaas.OrganizationRole{OrganizationID: "2", Role: "admin"})
	ctx = stdentsaas.WithAuthenticatedUser(ctx, "a2a0a29c-dbc1-4d0b-b379-afa2af5ab00f")

	tx1, err := wrapped.Tx(ctx)
	require.NoError(t, err)
	require.Equal(t, &sql.TxOptions{Isolation: sql.LevelLinearizable}, base.calledOpts)

	sqls := tx1.(*testTx1).sqls
	require.Len(t, sqls, 1)
	require.Contains(t, sqls[0], `SET LOCAL auth.u = 'a2a0a29c-dbc1-4d0b-b379-afa2af5ab00f';`)
	require.Contains(t, sqls[0], `SET LOCAL auth.o = '[{`)
	require.Contains(t, sqls[0], `SET LOCAL transaction_timeout = 299`)
}

func TestNonLinearlizable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := &testDriver2{}
	wrapped := stdentsaas.NewDriver(base,
		stdentsaas.AuthenticatedUserSetting("auth.u"),
		stdentsaas.AuthenticatedOrganizationsSetting("auth.o"),
		stdentsaas.AnonymousUserID(""))

	_, err := wrapped.BeginTx(ctx, &sql.TxOptions{})
	require.ErrorContains(t, err, "only linearlizable")
}

func TestNoBeginTx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := &testDriver1{}
	wrapped := stdentsaas.NewDriver(base,
		stdentsaas.AuthenticatedUserSetting("auth.u"),
		stdentsaas.AuthenticatedOrganizationsSetting("auth.o"),
		stdentsaas.AnonymousUserID(""))

	_, err := wrapped.BeginTx(ctx, &sql.TxOptions{})
	require.ErrorContains(t, err, "not supported")
}
