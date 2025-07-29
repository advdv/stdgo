package stdpgxtxfx_test

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/advdv/stdgo/fx/stdpgxtxfx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestConnectTxOk(t *testing.T) {
	t.Parallel()

	var obs *observer.ObservedLogs
	ctx, rw, _ := setup(t, &obs)

	resp, err := stdpgxtxfx.CTx(ctx, rw, connect.NewRequest(wrapperspb.String("hello")),
		func(ctx context.Context, l *zap.Logger, tx pgx.Tx, i *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
			return i, nil
		})
	require.NoError(t, err)
	require.Equal(t, "hello", resp.Msg.GetValue())
	require.Len(t, obs.FilterMessage("hook called").All(), 1)
}

func TestConnectTxOtherError(t *testing.T) {
	t.Parallel()

	ctx, rw, _ := setup(t)

	resp, err := stdpgxtxfx.CTx(ctx, rw, connect.NewRequest(wrapperspb.String("hello")),
		func(ctx context.Context, l *zap.Logger, tx pgx.Tx, i *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
			return nil, errors.New("foo")
		})
	require.EqualError(t, err, "foo")
	require.Nil(t, resp)
}

func TestConnectTxPgError(t *testing.T) {
	t.Parallel()

	ctx, rw, _ := setup(t)

	resp, err := stdpgxtxfx.CTx(ctx, rw, connect.NewRequest(wrapperspb.String("hello")),
		func(ctx context.Context, l *zap.Logger, tx pgx.Tx, i *wrapperspb.StringValue) (*wrapperspb.StringValue, error) {
			return nil, &pgconn.PgError{Code: "42501"}
		})
	require.EqualError(t, err, "permission_denied: database permission denied")
	require.Nil(t, resp)
}
