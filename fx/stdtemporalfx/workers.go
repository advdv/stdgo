package stdtemporalfx

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

func ProvideRegistration[W, A any](
	queueName string,
	regFn func(worker worker.Worker, wf W, act A),
) fx.Option {
	return fx.Options(
		// provide a registration to be put into the registrations group.
		fx.Provide(fx.Annotate(func(wf W, act A) *Registration {
			return &Registration{
				queueName: queueName,
				regFn: func(w worker.Worker) {
					regFn(w, wf, act)
				},
			}
		}, fx.ResultTags(`group:"registrations"`))),
	)
}

func ProvideActivityRegistration[A any](
	queueName string,
	regFn func(worker worker.Worker, act A),
) fx.Option {
	return fx.Options(
		// provide a registration to be put into the registrations group.
		fx.Provide(fx.Annotate(func(act A) *Registration {
			return &Registration{
				queueName: queueName,
				regFn: func(w worker.Worker) {
					regFn(w, act)
				},
			}
		}, fx.ResultTags(`group:"registrations"`))),
	)
}

func ProvideWorkflowRegistration[W any](
	queueName string,
	regFn func(worker worker.Worker, wf W),
) fx.Option {
	return fx.Options(
		// provide a registration to be put into the registrations group.
		fx.Provide(fx.Annotate(func(wf W) *Registration {
			return &Registration{
				queueName: queueName,
				regFn: func(w worker.Worker) {
					regFn(w, wf)
				},
			}
		}, fx.ResultTags(`group:"registrations"`))),
	)
}

// NewRegistration inits a registration.
func NewRegistration(queueName string, regFn func(worker worker.Worker)) *Registration {
	return &Registration{
		queueName: queueName,
		regFn:     regFn,
	}
}

// Registration describes registering of workflow and activities with a worker.
type Registration struct {
	regFn     func(w worker.Worker)
	queueName string
}

// Workers represent the set of Temporal workers.
type Workers struct {
	logs          *zap.Logger
	temporal      *Temporal
	registrations []*Registration
	workers       []worker.Worker
	interceptors  []interceptor.WorkerInterceptor
}

// NewWorkers inits a new set of workers.
func NewWorkers(par struct {
	fx.In
	fx.Lifecycle
	Logger   *zap.Logger
	Temporal *Temporal

	Interceptors  []interceptor.WorkerInterceptor `optional:"true"`
	Registrations []*Registration                 `group:"registrations"`
},
) (*Workers, error) {
	w := &Workers{
		logs:          par.Logger,
		temporal:      par.Temporal,
		registrations: par.Registrations,
		interceptors:  par.Interceptors,
	}

	// if the workers are disabled we do not start/stop them. This is usefull if the same
	// code as the service is run in a Lambda, or if the workers are started in a separate process.
	if w.temporal.cfg.DisableWorkers == true {
		w.logs.Info("workers are disabled, do not start/stop them")
		return w, nil
	}

	// else add lc hooks for starting/stopping.
	par.Append(fx.Hook{OnStart: w.Start, OnStop: w.Stop})
	return w, nil
}

// Register registeres a Temporal worker for the registration.
func (w *Workers) Register(registration *Registration) error {
	logs := w.logs.Named(registration.queueName)

	worker := worker.New(w.temporal.c, registration.queueName, worker.Options{
		OnFatalError: func(err error) {
			logs.Error("fatal worker error", zap.Error(err))
		},
		Interceptors: w.interceptors,
	})

	registration.regFn(worker)

	if err := worker.Start(); err != nil {
		return fmt.Errorf("start worker: %w", err)
	}

	logs.Info("registered worker", zap.String("queue_name", registration.queueName))

	w.workers = append(w.workers, worker)
	return nil
}

// Start the registered workers.
func (w *Workers) Start(context.Context) error {
	for _, registration := range w.registrations {
		if err := w.Register(registration); err != nil {
			return fmt.Errorf("register: %w", err)
		}
	}

	return nil
}

// Stop the workers.
func (w *Workers) Stop(context.Context) error {
	for _, worker := range w.workers {
		worker.Stop()
	}
	return nil
}
