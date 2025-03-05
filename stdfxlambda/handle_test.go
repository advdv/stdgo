package stdfxlambda_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/advdv/stdgo/stdfxlambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type (
	inputA struct {
		Bar string `json:"bar"`
	}
	inputB struct {
		Foo string `json:"foo"`
	}
)

func TestMarshalUnmarshal(t *testing.T) {
	for i, tt := range []struct {
		ctx                      context.Context
		expRequestID, expFuncARN string
	}{
		{
			ctx:          t.Context(),
			expRequestID: "not-in-lambda-id",
			expFuncARN:   "arn:aws:lambda:::not-in-lambda",
		},
		{
			ctx: lambdacontext.NewContext(t.Context(), &lambdacontext.LambdaContext{
				AwsRequestID:       "actual-request-id",
				InvokedFunctionArn: "func_arn",
			}),
			expRequestID: "actual-request-id",
			expFuncARN:   "func_arn",
		},
	} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			logs, err := zap.NewDevelopment()
			require.NoError(t, err)

			var actInp inputA
			var actLogger *zap.Logger
			var actReqID string
			var actFuncARN string

			handleFn := func(ctx stdfxlambda.Context, inp inputA) (out inputB, err error) {
				actLogger = ctx.Log()
				actReqID = ctx.AWSRequestID()
				actFuncARN = ctx.InvokedFunctionARN()
				actInp = inp
				out.Foo = "bar"
				return
			}

			outb, err := stdfxlambda.Handle(tt.ctx, logs, []byte(`{"bar":"foo"}`), handleFn)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo":"bar"}`, string(outb))
			require.Equal(t, inputA{Bar: "foo"}, actInp)
			require.NotNil(t, actLogger)
			require.Equal(t, tt.expFuncARN, actFuncARN)
			require.Equal(t, tt.expRequestID, actReqID)
		})
	}
}

func TestHandle_PanicWithoutLambdaContext(t *testing.T) {
	logs, err := zap.NewDevelopment()
	require.NoError(t, err)
	t.Setenv("AWS_LAMBDA_RUNTIME_API", "dummy-runtime-api")

	handleFn := func(ctx stdfxlambda.Context, inp inputA) (out inputB, err error) {
		return
	}

	require.PanicsWithValue(t, "no lambda context available", func() {
		stdfxlambda.Handle(t.Context(), logs, []byte(`{"bar":"foo"}`), handleFn)
	})
}
