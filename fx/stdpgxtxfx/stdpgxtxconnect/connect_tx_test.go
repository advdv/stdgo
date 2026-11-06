package stdpgxtxconnect_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdpgxfx/stdpgxtestfx"
	"github.com/advdv/stdgo/fx/stdpgxtxfx"
	"github.com/advdv/stdgo/fx/stdpgxtxfx/stdpgxtxconnect"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/internal/stdtestpg"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdtx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func setup(tb testing.TB) (context.Context, *stdtx.Transactor[pgx.Tx], *stdtx.Transactor[pgx.Tx]) {
	tb.Helper()

	var deps struct {
		fx.In
		*zap.Logger
		RW *stdtx.Transactor[pgx.Tx] `name:"rw"`
		RO *stdtx.Transactor[pgx.Tx] `name:"ro"`
	}

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(nil),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdpgxtestfx.Provide(tb, stdtestpg.Image, pgtestdb.NoopMigrator{}, stdpgxfx.NewPgxV5Driver(), "rw", "ro"),
		stdpgxtxfx.TestProvide("testapp", "postgres", "postgres"),
		fx.Populate(&deps),
	)

	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return stdctx.WithLogger(tb.Context(), deps.Logger), deps.RW, deps.RO
}

func echo(_ context.Context, _ *zap.Logger, _ pgx.Tx, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
	return in, nil
}

func TestConnectTxOk(t *testing.T) {
	t.Parallel()

	ctx, rw, _ := setup(t)

	resp, err := stdpgxtxconnect.CTx(ctx, rw, wrapperspb.String("hello"), echo)
	require.NoError(t, err)
	require.Equal(t, "hello", resp.GetValue())
}

func TestConnectTxCommitsAndRollsBack(t *testing.T) {
	t.Parallel()

	ctx, rw, ro := setup(t)
	failed := errors.New("failed after the insert")

	_, err := stdpgxtxconnect.CTx(ctx, rw, wrapperspb.String("committed"),
		func(ctx context.Context, _ *zap.Logger, tx pgx.Tx, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
			_, err := tx.Exec(ctx, `CREATE TABLE note (body text NOT NULL)`)
			if err != nil {
				return nil, err
			}

			_, err = tx.Exec(ctx, `INSERT INTO note (body) VALUES ($1)`, in.GetValue())

			return in, err
		})
	require.NoError(t, err)

	_, err = stdpgxtxconnect.CTx(ctx, rw, wrapperspb.String("rolled back"),
		func(ctx context.Context, _ *zap.Logger, tx pgx.Tx, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
			if _, err := tx.Exec(ctx, `INSERT INTO note (body) VALUES ($1)`, in.GetValue()); err != nil {
				return nil, err
			}

			return nil, failed
		})
	require.ErrorIs(t, err, failed)

	count, err := stdpgxtxconnect.CTx(ctx, ro, &struct{}{},
		func(ctx context.Context, _ *zap.Logger, tx pgx.Tx, _ *struct{}) (*int, error) {
			var n int
			return &n, tx.QueryRow(ctx, `SELECT count(*) FROM note`).Scan(&n)
		})
	require.NoError(t, err)
	require.Equal(t, 1, *count)
}

func TestConnectTxReturnsConnectErrorUnwrapped(t *testing.T) {
	t.Parallel()

	ctx, rw, _ := setup(t)
	notFound := connect.NewError(connect.CodeNotFound, "thing not found")

	resp, err := stdpgxtxconnect.CTx(ctx, rw, wrapperspb.String("hello"),
		func(context.Context, *zap.Logger, pgx.Tx, *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
			return nil, notFound
		})
	require.Nil(t, resp)
	require.Same(t, notFound, err)
}

func TestConnectTxOtherError(t *testing.T) {
	t.Parallel()

	ctx, rw, _ := setup(t)

	resp, err := stdpgxtxconnect.CTx(ctx, rw, wrapperspb.String("hello"),
		func(context.Context, *zap.Logger, pgx.Tx, *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
			return nil, errors.New("foo")
		})
	require.EqualError(t, err, "foo")
	require.Equal(t, connect.CodeUnknown, connect.CodeOf(err))
	require.Nil(t, resp)
}

func TestConnectTxPgError(t *testing.T) {
	t.Parallel()

	ctx, rw, _ := setup(t)
	pgErr := &pgconn.PgError{Code: "42501"}

	resp, err := stdpgxtxconnect.CTx(ctx, rw, wrapperspb.String("hello"),
		func(context.Context, *zap.Logger, pgx.Tx, *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
			return nil, pgErr
		})
	require.Nil(t, resp)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.EqualError(t, err, "permission_denied: database permission denied")
	require.ErrorIs(t, err, pgErr)
}

func TestReadOnlyTransactorRejectsWrites(t *testing.T) {
	t.Parallel()

	ctx, _, ro := setup(t)

	_, err := stdpgxtxconnect.CTx(ctx, ro, wrapperspb.String("hello"),
		func(ctx context.Context, _ *zap.Logger, tx pgx.Tx, in *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
			_, err := tx.Exec(ctx, `CREATE TABLE note (body text NOT NULL)`)
			return in, err
		})

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "25006", pgErr.Code) // read_only_sql_transaction
}
