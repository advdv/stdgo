package stdfxlambda_test

import (
	"context"
	"testing"

	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdfxlambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type mockHandler struct{}

func (m *mockHandler) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
	stdctx.Log(ctx).Info("some info")

	return []byte("0x01"), nil
}

func Test_WithLoggerPanic(t *testing.T) {
	hdlr := stdfxlambda.WithLogger(&mockHandler{}, zap.NewExample())

	ctx := context.Background()
	assert.PanicsWithValue(t, "stdfxlambda: no lambda context available", func() {
		hdlr.Invoke(ctx, []byte("abc"))
	})
}

func Test_WithLogger(t *testing.T) {
	zc, obs := observer.New(zap.InfoLevel)
	logs := zap.New(zc)
	hdlr := stdfxlambda.WithLogger(&mockHandler{}, logs)

	ctx := lambdacontext.NewContext(context.Background(), &lambdacontext.LambdaContext{
		AwsRequestID:       "some-id",
		InvokedFunctionArn: "some-arn",
	})

	out, _ := hdlr.Invoke(ctx, []byte("abc"))
	assert.Equal(t, []byte("0x01"), out)
	assert.Equal(t, 1, obs.FilterMessage("some info").Len())
}
