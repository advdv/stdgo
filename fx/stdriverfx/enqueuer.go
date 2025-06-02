package stdriverfx

import (
	"context"
	"fmt"

	"buf.build/go/protovalidate"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"go.uber.org/fx"
)

// Enqueuer describes a type that can enqueue work for the workers given certain arguments.
type Enqueuer[T JobArgs] interface {
	Enqueue(ctx context.Context, tx pgx.Tx, args T) error
}

// enqueuer implements an enqueuer generically.
type enqueuer[T JobArgs] struct {
	client    Client
	opts      river.InsertOpts
	validator protovalidate.Validator
}

// Enqueue a job with the provided job arguments.
func (e enqueuer[T]) Enqueue(
	ctx context.Context, tx pgx.Tx, args T,
) error {
	if err := e.validator.Validate(args); err != nil {
		return fmt.Errorf("validate args: %w", err)
	}

	_, err := e.client.InsertTx(ctx, tx, args, &e.opts)
	return err
}

// ProvideEnqueuer can be called by the work packages to provide an enqueuer for its args easily. When inserting it
// will always use the provided insert Opts.
func ProvideEnqueuer[T JobArgs](opts river.InsertOpts) fx.Option {
	return fx.Provide(func(c Client, v protovalidate.Validator) Enqueuer[T] {
		return enqueuer[T]{client: c, opts: opts, validator: v}
	})
}
