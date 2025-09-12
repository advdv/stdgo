package stdtemporalfx

import (
	"context"

	client "go.temporal.io/sdk/client"
	tworker "go.temporal.io/sdk/worker"
	"go.uber.org/fx"
)

// Register registers workflow and activity implementations.
func RegisterMain[T generatedClient, W, A, O any](
	queueName string,
	newClientFn func(client.Client, ...O) T,
	regFn func(worker tworker.Worker, wf W, act A),
	opts ...O,
) fx.Option {
	return register("main", queueName, newClientFn, regFn, opts...)
}

// register registers workflow and activity implementations.
func register[T generatedClient, W, A, O any](
	name, queueName string,
	newClientFn func(client.Client, ...O) T,
	regFn func(worker tworker.Worker, wf W, act A),
	opts ...O,
) fx.Option {
	return fx.Options(
		// provide the service client.
		fx.Provide(func(lc fx.Lifecycle, c *Temporal) (r *Client[T]) {
			client := &Client[T]{}
			lc.Append(fx.Hook{OnStart: func(ctx context.Context) error {
				client.W = newClientFn(c.c, opts...)
				return nil
			}})

			return client
		}),
		// provide the registration, named.
		fx.Provide(fx.Annotate(func(wf W, act A) *Registration {
			return &Registration{
				queueName: queueName,
				regFn: func(w tworker.Worker) {
					regFn(w, wf, act)
				},
			}
		}, fx.ResultTags(`name:"`+name+`"`))),
	)
}

type RegistrationFunc func(w tworker.Worker)

// Registration describes registering of workflow and activities with a worker.
type Registration struct {
	regFn     RegistrationFunc
	queueName string
}

// constraint for generated client.
type generatedClient interface {
	CancelWorkflow(ctx context.Context, workflowID string, runID string) error
	TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string, details ...interface{}) error
}

// Client wraps a generated Workflow client. The wrapping simply exists so we can set the underlying
// temporal client in a "onStart" hook.
type Client[T generatedClient] struct{ W T }
