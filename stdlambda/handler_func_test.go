package stdlambda_test

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/advdv/stdgo/stdlambda"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"
	"github.com/stretchr/testify/require"
)

func TestJSONHandler(t *testing.T) {
	type Input struct{ Foo string }
	type Output struct{ Bar string }

	type GWInput = events.APIGatewayProxyRequest
	type GWOutput = events.APIGatewayProxyResponse

	adapt := httpadapter.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello, world")
	}))

	for idx, tt := range []struct {
		handler lambda.Handler
		payload []byte
		assert  func(t *testing.T, err error, data []byte)
	}{
		// consider all variants of pointers and no pointers
		{
			handler: stdlambda.JSONHandlerFunc[Input, *Output](
				func(ctx context.Context, in Input) (*Output, error) {
					return &Output{Bar: "np,p"}, nil
				}),
			payload: []byte(`{}`),
			assert: func(t *testing.T, err error, data []byte) {
				require.NoError(t, err)
				require.JSONEq(t, `{"Bar":"np,p"}`, string(data))
			},
		},
		{
			handler: stdlambda.JSONHandlerFunc[Input, *Output](
				func(ctx context.Context, in Input) (*Output, error) {
					return nil, nil
				}),
			payload: []byte(`{}`),
			assert: func(t *testing.T, err error, data []byte) {
				require.NoError(t, err)
				require.JSONEq(t, `null`, string(data))
			},
		},
		{
			handler: stdlambda.JSONHandlerFunc[Input, Output](
				func(ctx context.Context, in Input) (Output, error) {
					return Output{Bar: "np,np"}, nil
				}),
			payload: []byte(`{}`),
			assert: func(t *testing.T, err error, data []byte) {
				require.NoError(t, err)
				require.JSONEq(t, `{"Bar":"np,np"}`, string(data))
			},
		},
		{
			handler: stdlambda.JSONHandlerFunc[*Input, *Output](
				func(ctx context.Context, in *Input) (*Output, error) {
					return &Output{"p,p"}, nil
				}),
			payload: []byte(`{}`),
			assert: func(t *testing.T, err error, data []byte) {
				require.NoError(t, err)
				require.JSONEq(t, `{"Bar":"p,p"}`, string(data))
			},
		},
		{
			handler: stdlambda.JSONHandlerFunc[*Input, Output](
				func(ctx context.Context, in *Input) (Output, error) {
					return Output{"p,np"}, nil
				}),
			payload: []byte(`{}`),
			assert: func(t *testing.T, err error, data []byte) {
				require.NoError(t, err)
				require.JSONEq(t, `{"Bar":"p,np"}`, string(data))
			},
		},
		// imagine a common lambda event
		{
			handler: stdlambda.JSONHandlerFunc[GWInput, GWOutput](adapt.ProxyWithContext),
			assert: func(t *testing.T, err error, data []byte) {
				require.ErrorContains(t, err, "failed to unmarshal payload")
			},
		},
		{
			handler: stdlambda.JSONHandlerFunc[GWInput, GWOutput](adapt.ProxyWithContext),
			payload: []byte(`{}`),
			assert: func(t *testing.T, err error, data []byte) {
				require.NoError(t, err)
				require.Contains(t, string(data), "hello, world")
			},
		},
	} {
		t.Run(strconv.Itoa(idx), func(t *testing.T) {
			data, err := tt.handler.Invoke(t.Context(), tt.payload)
			tt.assert(t, err, data)
		})
	}
}
