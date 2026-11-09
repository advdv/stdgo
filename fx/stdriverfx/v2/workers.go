package stdriverfx

import (
	"github.com/cockroachdb/errors"
	"github.com/riverqueue/river"
	"go.uber.org/fx"
)

// workers are the workers of the "rw" client, with the job kinds registered
// so far, which River's bundle does not expose. Only [WithWorker] adds to it.
type workers struct {
	*river.Workers

	kinds []string
}

func newWorkers() *workers { return &workers{Workers: river.NewWorkers()} }

// WithWorker registers the river.Worker[T] provided elsewhere in the fx graph
// with the "rw" client, which then works jobs of that kind.
func WithWorker[T river.JobArgs]() fx.Option {
	return fx.Invoke(func(wrks *workers, wrk river.Worker[T]) error {
		var args T
		if err := river.AddWorkerSafely(wrks.Workers, wrk); err != nil {
			return errors.Wrapf(err, "add river worker for %q", args.Kind())
		}

		wrks.kinds = append(wrks.kinds, args.Kind())

		return nil
	})
}

// ProvideWorker provides the worker that newf, a constructor, builds as a
// river.Worker[T], and registers it with [WithWorker].
func ProvideWorker[T river.JobArgs](newf any) fx.Option {
	return fx.Options(
		fx.Provide(fx.Annotate(newf, fx.As(new(river.Worker[T])))),
		WithWorker[T](),
	)
}
