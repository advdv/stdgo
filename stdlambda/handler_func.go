package stdlambda

import (
	"context"

	"github.com/aws/aws-lambda-go/lambda"
)

// HandlerFunc implements lambda.Handler, similar to http.HandlerFunc.
type HandlerFunc func(ctx context.Context, payload []byte) ([]byte, error)

func (f HandlerFunc) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
	return f(ctx, payload)
}

var _ lambda.Handler = HandlerFunc(func(context.Context, []byte) ([]byte, error) {
	return nil, nil
})
