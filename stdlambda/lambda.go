// Package stdlambda allows a lambda to be implemented via a Uber's fx dependency.
package stdlambda

import (
	"context"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"go.uber.org/fx"
)

// Handler is an interface that can be implemented to handle Lambda events.
type Handler[I, O any] interface {
	Handle(ctx context.Context, input I) (O, error)
}

// we have a about 500ms until SIGKILL is sent, so we set the startup context something just short of that
// https://pkg.go.dev/github.com/aws/aws-lambda-go/lambda#WithEnableSIGTERM
const lambdaStopTimeout = time.Millisecond * (500 - 10) //nolint:mnd

// allow this to be overwritten in testing scenarios.
var lambdaStartFunc = lambda.StartWithOptions

const (
	// exit codes for various failure scenarios.
	failedToStartExitCode = 1
	failedToStopExitCode  = 2
	notOnLambdaExitCode   = 3
)

// RunApp runs  an fx app in a Lambda environment.
func RunApp(app *fx.App, hdlr lambda.Handler, opts ...lambda.Option) (exitCode int) {
	startCtx, cancel := context.WithTimeout(context.Background(), app.StartTimeout())
	defer cancel()

	if err := app.Start(startCtx); err != nil {
		return failedToStartExitCode // fx will report the error
	}

	shutdownFn := func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), lambdaStopTimeout)
		defer cancel()

		if err := app.Stop(stopCtx); err != nil {
			exitCode = failedToStopExitCode // fx will report the error

			return
		}
	}

	// if the app is not run on lambda, shut down right away.
	if os.Getenv("AWS_LAMBDA_RUNTIME_API") == "" {
		shutdownFn()

		// if there is a failure in shutdown we report that error code instead
		// of the error for running locally.
		if exitCode == 0 {
			return notOnLambdaExitCode
		}

		return exitCode
	}

	// if on lambda we call the lambda start with a shutdown procedure.
	opts = append(opts, lambda.WithEnableSIGTERM(shutdownFn))
	lambdaStartFunc(hdlr, opts...)

	return exitCode
}
