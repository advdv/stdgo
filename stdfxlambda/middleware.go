package stdfxlambda

import (
	"context"

	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdlambda"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"go.uber.org/zap"
)

// WithLogger decorates the context with a zap handler that logs each with the request id and other lambda
// information.
func WithLogger(next lambda.Handler, logs *zap.Logger) lambda.Handler {
	return stdlambda.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		lctx, ok := lambdacontext.FromContext(ctx)
		if !ok {
			panic("stdfxlambda: no lambda context available")
		}

		logs = logs.With(
			zap.String("aws_request_id", lctx.AwsRequestID),
			zap.String("invoked_function_arn", lctx.InvokedFunctionArn))

		ctx = stdzapfx.WithLogger(ctx, logs)

		return next.Invoke(ctx, payload)
	})
}
