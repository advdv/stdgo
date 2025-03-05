package stdlambda_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/advdv/stdgo/stdlambda"
	"github.com/advdv/stdgo/stdlambda/stdlambdamock"
	"github.com/advdv/stdgo/stdlo"
	"github.com/aws/aws-lambda-go/lambda/messages"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/color"
)

func TestInvoker(t *testing.T) {
	for idx, tt := range []struct {
		expInvokeInput *lambda.InvokeInput
		invokeOutput   *lambda.InvokeOutput
		assert         func(t *testing.T, err error, v *color.Color)
	}{
		{
			expInvokeInput: &lambda.InvokeInput{
				FunctionName: aws.String("some:arn"),
				Payload:      []byte(`{}`),
			},
			invokeOutput: &lambda.InvokeOutput{
				StatusCode: 200, Payload: []byte(`{"blue":125}`),
			},
			assert: func(t *testing.T, err error, v *color.Color) {
				require.NoError(t, err)
				assert.NotNil(t, v)
				assert.InEpsilon(t, float32(125), v.GetBlue(), 0.001)
			},
		},

		{
			expInvokeInput: &lambda.InvokeInput{
				FunctionName: aws.String("some:arn"),
				Payload:      []byte(`{}`),
			},
			invokeOutput: &lambda.InvokeOutput{
				StatusCode: 400,
			},
			assert: func(t *testing.T, err error, v *color.Color) {
				require.ErrorContains(t, err, "unexpected status code")
				assert.Nil(t, v)
			},
		},

		{
			expInvokeInput: &lambda.InvokeInput{
				FunctionName: aws.String("some:arn"),
				Payload:      []byte(`{}`),
			},
			invokeOutput: &lambda.InvokeOutput{
				StatusCode: http.StatusOK,
				Payload:    stdlo.Must1(json.Marshal(messages.InvokeResponse_Error{Message: "test error"})),
			},
			assert: func(t *testing.T, err error, v *color.Color) {
				require.ErrorContains(t, err, "test error:")
				assert.Nil(t, v)
			},
		},
	} {
		t.Run(strconv.Itoa(idx), func(t *testing.T) {
			client := stdlambdamock.NewMockLambda(t)
			client.EXPECT().
				Invoke(mock.Anything, tt.expInvokeInput).
				Return(tt.invokeOutput, nil)

			invoker := stdlambda.NewInvoker[color.Color, color.Color](client, "some:arn")
			output, err := invoker.Invoke(t.Context(), color.Color{})
			tt.assert(t, err, output)
		})
	}
}

func TestProtoInvoker(t *testing.T) {
	for idx, tt := range []struct {
		expInvokeInput *lambda.InvokeInput
		invokeOutput   *lambda.InvokeOutput
		assert         func(t *testing.T, err error, v *color.Color)
	}{
		{
			expInvokeInput: &lambda.InvokeInput{
				FunctionName: aws.String("some:arn"),
				Payload:      []byte(`{}`),
			},
			invokeOutput: &lambda.InvokeOutput{
				StatusCode: 200, Payload: []byte(`{"blue":125}`),
			},
			assert: func(t *testing.T, err error, v *color.Color) {
				require.NoError(t, err)
				assert.NotNil(t, v)
				assert.InEpsilon(t, float32(125), v.GetBlue(), 0.001)
			},
		},

		{
			expInvokeInput: &lambda.InvokeInput{
				FunctionName: aws.String("some:arn"),
				Payload:      []byte(`{}`),
			},
			invokeOutput: &lambda.InvokeOutput{
				StatusCode: 400,
			},
			assert: func(t *testing.T, err error, v *color.Color) {
				require.ErrorContains(t, err, "unexpected status code")
				assert.Nil(t, v)
			},
		},

		{
			expInvokeInput: &lambda.InvokeInput{
				FunctionName: aws.String("some:arn"),
				Payload:      []byte(`{}`),
			},
			invokeOutput: &lambda.InvokeOutput{
				StatusCode: http.StatusOK,
				Payload:    stdlo.Must1(json.Marshal(messages.InvokeResponse_Error{Message: "test error"})),
			},
			assert: func(t *testing.T, err error, v *color.Color) {
				require.ErrorContains(t, err, "test error:")
				assert.Nil(t, v)
			},
		},
	} {
		t.Run(strconv.Itoa(idx), func(t *testing.T) {
			client := stdlambdamock.NewMockLambda(t)
			client.EXPECT().
				Invoke(mock.Anything, tt.expInvokeInput).
				Return(tt.invokeOutput, nil)

			invoker := stdlambda.NewProtoInvoker[color.Color, color.Color](client, "some:arn")
			output, err := invoker.Invoke(t.Context(), &color.Color{})
			tt.assert(t, err, output)
		})
	}
}
