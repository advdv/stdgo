package stdent_test

import (
	"context"
	"database/sql"
	"testing"

	entdialect "entgo.io/ent/dialect"

	"github.com/advdv/stdgo/stdent"
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

func TestNonLinearlizable(t *testing.T) {
	ctx := setup1(t)
	base := &testDriver2{}
	wrapped := stdent.NewDriver(base)

	_, err := wrapped.BeginTx(ctx, &sql.TxOptions{})
	require.ErrorContains(t, err, "only serializable")

	err = wrapped.Exec(ctx, "", nil, nil)
	require.ErrorContains(t, err, "is not supported")

	err = wrapped.Query(ctx, "", nil, nil)
	require.ErrorContains(t, err, "is not supported")
}

func TestNoBeginTx(t *testing.T) {
	ctx := setup1(t)
	base := &testDriver1{}
	wrapped := stdent.NewDriver(base)

	_, err := wrapped.BeginTx(ctx, &sql.TxOptions{})
	require.ErrorContains(t, err, "not supported")
}
