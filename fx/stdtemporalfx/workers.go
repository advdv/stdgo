package stdtemporalfx

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/interceptor"
	tworker "go.temporal.io/sdk/worker"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type Workers struct {
	logs   *zap.Logger
	client *Temporal
	main   tworker.Worker
	wicpt  *WorkerInterceptor

	registration struct {
		main *Registration
	}
}

func NewWorkers(par struct {
	fx.In
	fx.Lifecycle

	Client                 *Temporal
	Logger                 *zap.Logger
	WorkerInterceptor      *WorkerInterceptor
	MainWorkerRegistration *Registration `name:"main"`
},
) (*Workers, error) {
	wrks := &Workers{
		client: par.Client,
		logs:   par.Logger,
		wicpt:  par.WorkerInterceptor,
	}

	wrks.registration.main = par.MainWorkerRegistration

	par.Append(fx.Hook{OnStart: wrks.Start, OnStop: wrks.Stop})
	return wrks, nil
}

func (w *Workers) Start(_ context.Context) (err error) {
	{
		// main queue and workers.
		w.main = tworker.New(w.client.c, w.registration.main.queueName, tworker.Options{
			OnFatalError: func(err error) {
				w.logs.Error("fatal worker error", zap.Error(err))
			},
			Interceptors: []interceptor.WorkerInterceptor{
				w.wicpt,
			},
		})

		w.registration.main.regFn(w.main)

		if err := w.main.Start(); err != nil {
			return fmt.Errorf("start main worker: %w", err)
		}
	}

	return nil
}

func (w *Workers) Stop(context.Context) (err error) {
	w.main.Stop()
	return nil
}
