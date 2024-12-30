// Package stdlambda provides a typed context to standardize handling in our Lambda functions.
package stdlambda

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/aws/aws-lambda-go/lambdacontext"
	"go.uber.org/zap"
)

// Context interface with extra components that we want to use
// across our lambda functions.
type Context interface {
	Log() *zap.Logger
	InvokedFunctionARN() string
	AWSRequestID() string
	context.Context
}

// lambdaContext implements the standard Lambda context.
type lambdaContext struct {
	awsRequestID       string
	invokedFunctionARN string

	logs *zap.Logger
	context.Context
}

func (ctx lambdaContext) InvokedFunctionARN() string {
	return ctx.invokedFunctionARN
}

func (ctx lambdaContext) AWSRequestID() string {
	return ctx.awsRequestID
}

func (ctx lambdaContext) Log() *zap.Logger {
	return ctx.logs
}

// Handle generalizes the handling of lambda inputs.
func Handle[I, O any](
	ctx context.Context,
	logs *zap.Logger,
	inb []byte,
	hfn func(Context, I) (O, error),
) (outb []byte, err error) {
	l2ctx := &lambdaContext{
		Context: ctx,
	}

	l1ctx, ok := lambdacontext.FromContext(ctx)
	switch {
	case ok:
		l2ctx.awsRequestID = l1ctx.AwsRequestID
		l2ctx.invokedFunctionARN = l1ctx.InvokedFunctionArn
	case !ok && !isRunningInLambda(): // for local/testing purposees
		l2ctx.awsRequestID = "not-in-lambda-id"
		l2ctx.invokedFunctionARN = "arn:aws:lambda:::not-in-lambda"
	default:
		panic("no lambda context available")
	}

	l2ctx.logs = logs.With(
		zap.String("aws_request_id", l2ctx.awsRequestID),
		zap.String("invoked_function_arn", l2ctx.invokedFunctionARN))

	var in I
	if err := json.Unmarshal(inb, &in); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input: %w, input: %s", err, string(inb))
	}

	out, err := hfn(l2ctx, in)
	if err != nil {
		return nil, err
	}

	if outb, err = json.Marshal(out); err != nil {
		return nil, fmt.Errorf("failed to marshal output: %w, output: %+v", err, out)
	}

	return outb, nil
}

func isRunningInLambda() bool {
	return os.Getenv("AWS_LAMBDA_RUNTIME_API") != ""
}
