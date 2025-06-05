// Package workheartbeat provides working logic for a heartbeat job.
package workheartbeat

import (
	"context"
	"time"

	"buf.build/go/protovalidate"
	"github.com/advdv/stdgo/fx/stdriverfx"
	workheartbeatv1 "github.com/advdv/stdgo/fx/stdriverfx/internal/workheartbeat/v1"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/advdv/stdgo/stdfx"
	"github.com/advdv/stdgo/stdtx"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type Config struct{}

type (
	Params struct {
		fx.In
		Config
		RW        *stdtx.Transactor[pgx.Tx] `name:"rw"`
		Validator protovalidate.Validator
	}

	Result struct {
		fx.Out
		Worker    river.Worker[*workheartbeatv1.Args]
		Periodics stdriverfx.PeriodicWorker `group:"periodic_workers"`
	}
)

// worker implements working the job.
type worker struct {
	river.WorkerDefaults[*workheartbeatv1.Args]
	*stdriverfx.TransactWorker[*workheartbeatv1.Args, *workheartbeatv1.Output]
}

func New(par Params) (Result, error) {
	res := &worker{}
	res.TransactWorker = stdriverfx.NewTransactWorker(par.RW, par.Validator, res.work)
	return Result{Worker: res, Periodics: res}, nil
}

func (w worker) Timeout(*river.Job[*workheartbeatv1.Args]) time.Duration {
	return time.Second * 10
}

func (w worker) PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Second*10),
			func() (river.JobArgs, *river.InsertOpts) {
				return &workheartbeatv1.Args{}, &river.InsertOpts{MaxAttempts: 1}
			}, &river.PeriodicJobOpts{RunOnStart: true}),
	}
}

func (w worker) work(
	ctx context.Context, _ pgx.Tx, job *river.Job[*workheartbeatv1.Args],
) (*workheartbeatv1.Output, error) {
	stdriverfx.Log(ctx).Info("starting heartbeat job", zap.String("args", job.Args.String()))
	defer stdriverfx.Log(ctx).Info("done with heartbeat job")

	tStart := time.Now()
	select {
	case <-time.After(job.Args.GetBlockFor().AsDuration()):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	return workheartbeatv1.Output_builder{
		BlockTook: durationpb.New(time.Since(tStart)),
	}.Build(), nil
}

// Provide the worker as a dependecy.
func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("workheartbeat", New,
		stdriverfx.WithWorker[*workheartbeatv1.Args](),
		stdriverfx.ProvideEnqueuer[*workheartbeatv1.Args](river.InsertOpts{
			MaxAttempts: 1,
		}))
}
