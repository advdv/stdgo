package stdtemporalfx

import (
	"context"

	"go.temporal.io/sdk/client"
	"go.uber.org/fx"
)

func ProvideClient[T generatedClient, O any](
	newClientFn func(client.Client, ...O) T,
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
	)
}

// constraint for generated client.
type generatedClient interface {
	CancelWorkflow(ctx context.Context, workflowID string, runID string) error
	TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string, details ...interface{}) error
}

// Client wraps a generated Workflow client. The wrapping simply exists so we can set the underlying
// temporal client in a "onStart" hook.
type Client[T generatedClient] struct{ W T }
