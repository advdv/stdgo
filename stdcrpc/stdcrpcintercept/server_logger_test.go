package stdcrpcintercept_test

import (
	"context"
	"maps"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/advdv/stdgo/stdcrpc/stdcrpcintercept"
	"github.com/advdv/stdgo/stdctx"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewServerLogger(t *testing.T) {
	t.Parallel()

	spec := connect.Spec{
		Procedure:        "/acme.v1.ThingService/GetThing",
		IdempotencyLevel: connect.IdempotencyNoSideEffects,
	}
	handle := func(ctx context.Context, _ connect.Spec, _ connect.ServerStream) error {
		stdctx.Log(ctx).Info("handled")

		return nil
	}

	// specFields are the fields the interceptor logs for spec.
	specFields := map[string]any{
		"procedure":       "/acme.v1.ThingService/GetThing",
		"rpc.service":     "acme.v1.ThingService",
		"rpc.method":      "GetThing",
		"rpc.idempotency": "no_side_effects",
	}

	t.Run("builds on the logger in the context", func(t *testing.T) {
		t.Parallel()

		ctxCore, ctxLogs := observer.New(zapcore.DebugLevel)
		fallbackCore, fallbackLogs := observer.New(zapcore.DebugLevel)
		ctx := stdctx.WithLogger(t.Context(), zap.New(ctxCore).With(zap.String("request_id", "abc")))

		next := stdcrpcintercept.NewServerLogger(zap.New(fallbackCore))(handle)
		require.NoError(t, next(ctx, spec, nil))

		entries := ctxLogs.FilterMessage("handled").All()
		require.Len(t, entries, 1)

		want := map[string]any{"request_id": "abc"}
		maps.Copy(want, specFields)

		require.Equal(t, want, entries[0].ContextMap())
		require.Equal(t, 0, fallbackLogs.Len())
	})

	t.Run("falls back to logs without one in the context", func(t *testing.T) {
		t.Parallel()

		fallbackCore, fallbackLogs := observer.New(zapcore.DebugLevel)

		next := stdcrpcintercept.NewServerLogger(zap.New(fallbackCore))(handle)
		require.NoError(t, next(t.Context(), spec, nil))

		entries := fallbackLogs.FilterMessage("handled").All()
		require.Len(t, entries, 1)
		require.Equal(t, specFields, entries[0].ContextMap())
	})

	t.Run("logs the protocol and codec of the call", func(t *testing.T) {
		t.Parallel()

		core, logs := observer.New(zapcore.DebugLevel)

		server := connect.NewServer(stdcrpcintercept.NewServerLogger(zap.New(core)))
		server.Register(connect.Method{Spec: spec, Handler: handle})

		err := server.Call(t.Context(), spec.Procedure, &connect.CallInfo{Protocol: "grpc", Codec: "proto"}, nil)
		require.NoError(t, err)

		entries := logs.FilterMessage("handled").All()
		require.Len(t, entries, 1)

		want := map[string]any{"rpc.protocol": "grpc", "rpc.codec": "proto"}
		maps.Copy(want, specFields)

		require.Equal(t, want, entries[0].ContextMap())
	})

	t.Run("leaves out the parts of a malformed procedure", func(t *testing.T) {
		t.Parallel()

		core, logs := observer.New(zapcore.DebugLevel)

		next := stdcrpcintercept.NewServerLogger(zap.New(core))(handle)
		require.NoError(t, next(t.Context(), connect.Spec{Procedure: "nope"}, nil))

		entries := logs.FilterMessage("handled").All()
		require.Len(t, entries, 1)
		require.Equal(t, map[string]any{
			"procedure":       "nope",
			"rpc.idempotency": "idempotency_unknown",
		}, entries[0].ContextMap())
	})

	t.Run("passes the handler's result through", func(t *testing.T) {
		t.Parallel()

		next := stdcrpcintercept.NewServerLogger(zap.NewNop())(failing(nil))
		require.NoError(t, next(t.Context(), spec, nil))

		next = stdcrpcintercept.NewServerLogger(zap.NewNop())(failing(errTaken))
		require.ErrorIs(t, next(t.Context(), spec, nil), errTaken)
	})
}
