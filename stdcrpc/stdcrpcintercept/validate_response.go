// Package stdcrpcintercept holds re-usable connect rpc interceptors.
package stdcrpcintercept

import (
	"context"
	"errors"
	"fmt"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
)

// NewValidateResponse creates a Connect interceptor that validates responses. Useful for
// test environments.
func NewValidateResponse(val protovalidate.Validator) connect.UnaryInterceptorFunc {
	interceptor := func(next connect.UnaryFunc) connect.UnaryFunc {
		return connect.UnaryFunc(func(
			ctx context.Context,
			req connect.AnyRequest,
		) (connect.AnyResponse, error) {
			if req.Spec().IsClient {
				return next(ctx, req)
			}

			resp, err := next(ctx, req)
			if err != nil {
				return resp, err
			}

			return resp, validate(val, resp.Any())
		})
	}
	return connect.UnaryInterceptorFunc(interceptor)
}

// this is copied from: https://github.com/connectrpc/validate-go/blob/main/validate.go#L148
func validate(validator protovalidate.Validator, msg any) error {
	protoMsg, ok := msg.(proto.Message)
	if !ok {
		return fmt.Errorf("expected proto.Message, got %T", msg)
	}

	err := validator.Validate(protoMsg)
	if err == nil {
		return nil
	}
	connectErr := connect.NewError(connect.CodeInternal, fmt.Errorf("response: %w", err))
	if validationErr := new(protovalidate.ValidationError); errors.As(err, &validationErr) {
		if detail, err := connect.NewErrorDetail(validationErr.ToProto()); err == nil {
			connectErr.AddDetail(detail)
		}
	}
	return connectErr
}
