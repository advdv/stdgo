package stdcrpcintercept_test

import (
	"context"
	"testing"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/advdv/stdgo/stdcrpc/stdcrpcintercept"
	internalv1 "github.com/advdv/stdgo/stdcrpc/stdcrpcintercept/internal/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestValidateResponse(t *testing.T) {
	val, err := protovalidate.New()
	require.NoError(t, err)

	cept := stdcrpcintercept.NewValidateResponse(val)
	req := connect.NewRequest(wrapperspb.String("hello"))

	resp, err := cept.WrapUnary(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&internalv1.Greeting{}), nil
	})(t.Context(), req)

	require.NotNil(t, resp)
	require.ErrorContains(t, err, "response: validation error")
}
