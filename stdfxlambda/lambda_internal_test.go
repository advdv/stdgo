package stdfxlambda

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

type testHandlers struct{}

func (testHandlers) Invoke(context.Context, []byte) ([]byte, error) {
	return nil, nil
}

func TestRunAppOutsideLambda(t *testing.T) {
	lambdaStartFunc = func(any, ...lambda.Option) { t.Error("should not be  called") }

	hdlr := &testHandlers{}
	app := fx.New(fx.WithLogger(func() fxevent.Logger { return fxevent.NopLogger }))

	exitCode := RunApp(app, hdlr, lambda.WithSetIndent("", " "))
	require.Equal(t, notOnLambdaExitCode, exitCode)
}

func TestRunAppOnLambda(t *testing.T) {
	t.Setenv("AWS_LAMBDA_RUNTIME_API", "testing")

	var actHandler any
	var actOpts []lambda.Option
	lambdaStartFunc = func(handler any, options ...lambda.Option) { actHandler = handler; actOpts = options }

	hdlr := &testHandlers{}
	app := fx.New(fx.WithLogger(func() fxevent.Logger { return fxevent.NopLogger }))

	exitCode := RunApp(app, hdlr, lambda.WithSetIndent("", " "))
	require.Equal(t, 0, exitCode)
	require.Len(t, actOpts, 2)
	require.Equal(t, hdlr, actHandler)
}

func TestRunNewApp(t *testing.T) {
	t.Setenv("AWS_LAMBDA_RUNTIME_API", "testing")

	var actHandler any
	var actOpts []lambda.Option
	lambdaStartFunc = func(handler any, options ...lambda.Option) { actHandler = handler; actOpts = options }

	hdlr := &testHandlers{}

	exitCode := RunNewApp(
		fx.Provide(func() lambda.Handler { return hdlr }),
		fx.Supply([]lambda.Option{lambda.WithSetIndent("", " ")}),
	)

	require.Equal(t, 0, exitCode)
	require.Len(t, actOpts, 2)
	require.Equal(t, hdlr, actHandler)
}

func TestRunAppFailedToStart(t *testing.T) {
	lambdaStartFunc = func(any, ...lambda.Option) { t.Error("should not be called") }

	app := fx.New(
		fx.WithLogger(func() fxevent.Logger { return fxevent.NopLogger }),
		fx.Invoke(func() error { return errors.New("startup failure") }),
	)

	hdlr := &testHandlers{}
	exitCode := RunApp(app, hdlr, lambda.WithSetIndent("", " "))
	require.Equal(t, failedToStartExitCode, exitCode)
}

func TestRunAppFailedToStop(t *testing.T) {
	app := fx.New(
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{
				OnStart: func(context.Context) error { return nil },
				OnStop: func(context.Context) error {
					return errors.New("shutdown failure")
				}, // Simulates failure on stop
			})
		}),
	)

	hdlr := &testHandlers{}
	exitCode := RunApp(app, hdlr, lambda.WithSetIndent("", " "))
	require.Equal(t, failedToStopExitCode, exitCode)
}
