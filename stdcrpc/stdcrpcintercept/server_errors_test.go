package stdcrpcintercept_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/advdv/stdgo/stdcrpc/stdcrpcintercept"
	"github.com/advdv/stdgo/stdctx"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// failing is a handler that fails with err.
func failing(err error) connect.ServerFunc {
	return func(context.Context, connect.Spec, connect.ServerStream) error { return err }
}

// errTaken is a domain error that takenMapper maps.
var errTaken = errors.New("name taken")

// takenMapper maps errTaken to ALREADY_EXISTS, and leaves other errors to the next mapper.
func takenMapper(_ context.Context, _ *zap.Logger, err error) *connect.Error {
	if errors.Is(err, errTaken) {
		return connect.NewError(connect.CodeAlreadyExists, "name taken").WithCause(err)
	}

	return nil
}

func TestNewServerErrors(t *testing.T) {
	t.Parallel()

	notFound := connect.NewError(connect.CodeNotFound, "thing not found")
	// claimsTaken maps every error to ALREADY_EXISTS, to show what runs before the mappers.
	claimsTaken := func(_ context.Context, _ *zap.Logger, err error) *connect.Error {
		return connect.NewError(connect.CodeAlreadyExists, "claimed").WithCause(err)
	}

	for _, tc := range []struct {
		name     string
		err      error
		mappers  []stdcrpcintercept.ErrorMapper
		wantCode connect.Code
		wantMsg  string
		// wantIs is an error the result must match; err when nil.
		wantIs  error
		wantLog bool
	}{
		{
			// Unwrapped from the rest of the chain, so it reaches the client as the handler returned it.
			name: "handler verdict passes unwrapped", err: fmt.Errorf("transact: %w", notFound),
			wantCode: connect.CodeNotFound, wantMsg: "thing not found", wantIs: notFound,
		},
		{
			name: "remote verdict is internal", err: notFound.WithRemote(),
			wantCode: connect.CodeInternal, wantMsg: "internal error", wantLog: true,
		},
		{
			name: "canceled", err: fmt.Errorf("query: %w", context.Canceled),
			wantCode: connect.CodeCanceled, wantMsg: "canceled",
		},
		{
			name: "deadline", err: fmt.Errorf("query: %w", context.DeadlineExceeded),
			wantCode: connect.CodeDeadlineExceeded, wantMsg: "deadline exceeded",
		},
		{
			name: "other is internal", err: errors.New("connection refused to 10.0.0.1"),
			wantCode: connect.CodeInternal, wantMsg: "internal error", wantLog: true,
		},
		{
			name: "mapper maps its error", err: fmt.Errorf("insert: %w", errTaken),
			mappers:  []stdcrpcintercept.ErrorMapper{takenMapper},
			wantCode: connect.CodeAlreadyExists, wantMsg: "name taken",
		},
		{
			name: "mapper leaves other errors to the default", err: errors.New("boom"),
			mappers:  []stdcrpcintercept.ErrorMapper{takenMapper},
			wantCode: connect.CodeInternal, wantMsg: "internal error", wantLog: true,
		},
		{
			name: "handler verdict comes before the mappers", err: notFound,
			mappers:  []stdcrpcintercept.ErrorMapper{claimsTaken},
			wantCode: connect.CodeNotFound, wantMsg: "thing not found", wantIs: notFound,
		},
		{
			name: "first mapper that maps wins", err: errTaken,
			mappers:  []stdcrpcintercept.ErrorMapper{takenMapper, claimsTaken},
			wantCode: connect.CodeAlreadyExists, wantMsg: "name taken",
		},
		{
			name: "mappers come before cancellation", err: context.Canceled,
			mappers:  []stdcrpcintercept.ErrorMapper{claimsTaken},
			wantCode: connect.CodeAlreadyExists, wantMsg: "claimed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			core, logs := observer.New(zapcore.DebugLevel)
			next := stdcrpcintercept.NewServerErrors(zap.New(core), tc.mappers...)(failing(tc.err))

			err := next(t.Context(), connect.Spec{}, nil)

			connectErr, ok := errors.AsType[*connect.Error](err)
			require.True(t, ok)
			require.Equal(t, tc.wantCode, connectErr.Code())
			require.Equal(t, tc.wantMsg, connectErr.Message())
			require.Equal(t, tc.wantLog, logs.FilterMessage("rpc failed").Len() == 1)

			wantIs := tc.wantIs
			if wantIs == nil {
				wantIs = tc.err
			}

			require.ErrorIs(t, err, wantIs)
		})
	}
}

func TestNewServerErrorsLogger(t *testing.T) {
	t.Parallel()

	t.Run("passes success through", func(t *testing.T) {
		t.Parallel()

		next := stdcrpcintercept.NewServerErrors(zap.NewNop())(failing(nil))
		require.NoError(t, next(t.Context(), connect.Spec{}, nil))
	})

	t.Run("logs to the context logger", func(t *testing.T) {
		t.Parallel()

		ctxCore, ctxLogs := observer.New(zapcore.DebugLevel)
		fallbackCore, fallbackLogs := observer.New(zapcore.DebugLevel)
		ctx := stdctx.WithLogger(t.Context(), zap.New(ctxCore))

		next := stdcrpcintercept.NewServerErrors(zap.New(fallbackCore))(failing(errors.New("boom")))
		require.Equal(t, connect.CodeInternal, connect.CodeOf(next(ctx, connect.Spec{}, nil)))
		require.Equal(t, 1, ctxLogs.FilterMessage("rpc failed").Len())
		require.Equal(t, 0, fallbackLogs.Len())
	})

	t.Run("logs to the fallback logger without one in the context", func(t *testing.T) {
		t.Parallel()

		fallbackCore, fallbackLogs := observer.New(zapcore.DebugLevel)

		next := stdcrpcintercept.NewServerErrors(zap.New(fallbackCore))(failing(errors.New("boom")))
		require.Equal(t, connect.CodeInternal, connect.CodeOf(next(t.Context(), connect.Spec{}, nil)))
		require.Equal(t, 1, fallbackLogs.FilterMessage("rpc failed").Len())
	})
}
