package stdlambda

import (
	"context"
	"encoding/json"
	"fmt"

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

// JSONHandlerFunc implements lambda.Handler but decodes and encodes as JSON.
type JSONHandlerFunc[
	I, O any] func(ctx context.Context, payload I) (O, error)

func (f JSONHandlerFunc[I, O]) Invoke(ctx context.Context, payload []byte) ([]byte, error) {
	inputp := new(I)
	if err := json.Unmarshal(payload, inputp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	output, err := f(ctx, *inputp)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal output: %w", err)
	}

	return data, nil
}
