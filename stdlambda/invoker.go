// Package stdlambda implements re-usable logic for AWS Lambda that is not tied to fx.
package stdlambda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/aws/aws-lambda-go/lambda/messages"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/tidwall/gjson"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Lambda is implemented by the official SDK's client.
type Lambda interface {
	Invoke(ctx context.Context, params *lambda.InvokeInput, optFns ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

// ProtoInvoker invokes lambdas that use protobuf encoding.
type ProtoInvoker[I, O any, IP interface {
	*I
	proto.Message
}, OP interface {
	*O
	proto.Message
}] struct {
	baseInvoker
}

// NewProtoInvoker inits the invoker.
func NewProtoInvoker[I, O any, IP interface {
	*I
	proto.Message
}, OP interface {
	*O
	proto.Message
}](lambdaClient Lambda, functionName string) *ProtoInvoker[I, O, IP, OP] {
	return &ProtoInvoker[I, O, IP, OP]{
		baseInvoker: baseInvoker{client: lambdaClient, functionName: functionName},
	}
}

// Invoke invokes the lambda function but encodes it using protobuf JSON.
func (inv ProtoInvoker[I, O, IP, OP]) Invoke(ctx context.Context, input IP) (output OP, err error) {
	output = new(O)
	boundMarshalf := func() ([]byte, error) { return protojson.Marshal(input) }
	boundUnmarshalf := func(data []byte) error { return protojson.Unmarshal(data, output) }
	if err := inv.baseInvoker.invoke(ctx, boundMarshalf, boundUnmarshalf); err != nil {
		return nil, err
	}

	return output, nil
}

// Invoker invokes a lambda using "I" as the input type and "O" as the output type.
type Invoker[I, O any] struct {
	baseInvoker
}

// NewInvoker inits the invoker.
func NewInvoker[I, O any](lambdaClient Lambda, functionName string) *Invoker[I, O] {
	return &Invoker[I, O]{baseInvoker: baseInvoker{client: lambdaClient, functionName: functionName}}
}

// Invoke invokes the lambda function.
func (inv Invoker[I, O]) Invoke(ctx context.Context, input I) (output *O, err error) {
	output = new(O)
	boundMarshalf := func() ([]byte, error) { return json.Marshal(input) }
	boundUnmarshalf := func(data []byte) error { return json.Unmarshal(data, output) }
	if err := inv.baseInvoker.invoke(ctx, boundMarshalf, boundUnmarshalf); err != nil {
		return nil, err
	}

	return output, nil
}

// baseInvoker contains logic used for both the protobuf invoker and the JSON invoker.
type baseInvoker struct {
	client       Lambda
	functionName string
}

func (inv baseInvoker) invoke(
	ctx context.Context,
	boundMarshalf func() ([]byte, error),
	boundUnmarshalf func(data []byte) error,
) error {
	inPayload, err := boundMarshalf()
	if err != nil {
		return fmt.Errorf("failed to marshal input: %w", err)
	}

	result, err := inv.client.Invoke(ctx, &lambda.InvokeInput{
		FunctionName: aws.String(inv.functionName),
		Payload:      inPayload,
	})
	if err != nil {
		return fmt.Errorf("failed to invoke: %w", err)
	}

	if result.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %v", result.StatusCode)
	}

	if gjson.GetBytes(result.Payload, "errorMessage").Str != "" {
		var respError messages.InvokeResponse_Error

		if err := json.Unmarshal(result.Payload, &respError); err != nil {
			return fmt.Errorf("failed to marshal invoke response error: %w", err)
		}

		return fmt.Errorf("%s: %w", respError.Message, respError)
	}

	if err := boundUnmarshalf(result.Payload); err != nil {
		return fmt.Errorf("failed to unmarshal output: %w", err)
	}

	return nil
}
