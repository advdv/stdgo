// Package stdriverfx provides cross-functional logic and types for job working.
package stdriverfx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdfx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	slogzap "github.com/samber/slog-zap/v2"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

// Config configures the components.
type Config struct {
	// Wait some time for jobs to finish, worker will not accept new jobs in this timeframe.
	SoftStopTimeout time.Duration `env:"SOFT_STOP_TIMEOUT" envDefault:"5s"`
	// If the soft stop fails, we cancel all remaining jobs and then wait for this timeout to let them clean up.
	HardStopTimeout time.Duration `env:"HARD_STOP_TIMEOUT" envDefault:"5s"`
}

// JobArgs declare the shape of job arguments for our purpose. We require the arguments to always be
// a protobuf message. And that the protobuf message implement json.Marshaler and json.Unmarshaler so
// it can be encoded in the database using json.Marashal.
type JobArgs interface {
	river.JobArgs
	json.Marshaler
	json.Unmarshaler
	proto.Message
}

// JobOutput describes the shape of Job outputs.
type JobOutput interface {
	json.Marshaler
	json.Unmarshaler
	proto.Message
}

// Client describes the parts of the River interface we use. It is implemented by both the Pro as the community
// edition.
type Client interface {
	InsertTx(
		ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
	JobList(ctx context.Context, params *river.JobListParams) (*river.JobListResult, error)
	JobListTx(ctx context.Context, tx pgx.Tx, params *river.JobListParams) (*river.JobListResult, error)
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	StopAndCancel(ctx context.Context) error
}

// Workers represents the collection of workers.
type Workers struct {
	cfg    Config
	logs   *zap.Logger
	client Client
	ctx    context.Context
}

// New inits the main workers component. It also takes the enqueuers so they can be embedded for easier access from
// all other parts of the codebase.
func New(par struct {
	fx.In
	fx.Lifecycle
	Config
	Logs   *zap.Logger
	Client Client
},
) (res *Workers, err error) {
	lcCtx := context.Background()                               // lifecycle context.
	lcCtx = stdctx.WithLogger(lcCtx, par.Logs.Named("workers")) // passed to the context of every job.

	res = &Workers{
		cfg:    par.Config,
		logs:   par.Logs,
		ctx:    lcCtx,
		client: par.Client,
	}

	par.Lifecycle.Append(fx.Hook{
		OnStart: res.start,
		OnStop:  res.stop,
	})

	return res, nil
}

// Ping provides a check to see if the worker can access the required tables.
func (w Workers) Ping(ctx context.Context) error {
	if _, err := w.client.JobList(ctx, river.NewJobListParams().First(1)); err != nil {
		return fmt.Errorf("list worker jobs: %w", err)
	}
	return nil
}

// GetJobByKinds returns all jobs by the kind string(s).
func (w Workers) GetJobByKinds(
	ctx context.Context, tx pgx.Tx, kind string, moreKinds ...string,
) (*river.JobListResult, error) {
	res, err := w.client.JobListTx(ctx, tx, river.NewJobListParams().Kinds(append([]string{kind}, moreKinds...)...))
	if err != nil {
		return nil, fmt.Errorf("job list tx: %w", err)
	}

	return res, nil
}

// start working jobs.
func (w Workers) start(context.Context) error {
	if err := w.client.Start(w.ctx); err != nil { //nolint:contextcheck
		return fmt.Errorf("start River client: %w", err)
	}

	return nil
}

// stop working jobs.
func (w Workers) stop(ctx context.Context) error {
	w.logs.Info("stopping workers, issuing soft stop", zap.Duration("soft_stop_timeout", w.cfg.SoftStopTimeout))

	softStopCtx, softStopCtxCancel := context.WithTimeout(ctx, w.cfg.SoftStopTimeout)
	defer softStopCtxCancel()

	// stop fetching new jobs, and wait for existing jobs to finish.
	err := w.client.Stop(softStopCtx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("soft stop: %w", err) // unexpected failure during soft shutdown.
	} else if err == nil {
		w.logs.Info("soft stop succeeded, no hard stop necessary")
		return nil
	}

	w.logs.Info("soft stop timed out, issuing hard stop", zap.Duration("hard_stop_timeout", w.cfg.HardStopTimeout))
	hardStopCtx, hardStopCtxCancel := context.WithTimeout(ctx, w.cfg.HardStopTimeout)
	defer hardStopCtxCancel()

	err = w.client.StopAndCancel(hardStopCtx)
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		w.logs.Error("hard stop took too long; ignoring stop procedure and exiting unsafely")
		return nil
	} else if err != nil {
		return fmt.Errorf("hard stop: %w", err)
	}

	w.logs.Info("hard stop succeeded")
	return nil
}

// newRiverConfig inits the configuration as shared between the regular river queue client. And the Pro one.
func newRiverConfig(wrks *workers, logs *zap.Logger) river.Config {
	slogs := slog.New(slogzap.Option{Logger: logs}.NewZapHandler())

	return river.Config{
		Logger:  slogs,
		Schema:  "workers",
		Workers: wrks.Workers,
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 100},
		},
		Middleware: []rivertype.Middleware{
			NewMiddleware(),
		},
	}
}

// ClientBuilderFunc implements the initialization of a river client. Can be a regular one or a pro client.
type ClientBuilderFunc func(rw *pgxpool.Pool, cfg river.Config) (Client, error)

// NewRegularClient inits a regular River client by implenting [ClientBuilderFunc].
func NewRegularClient(rw *pgxpool.Pool, cfg river.Config) (Client, error) {
	return river.NewClient(riverpgxv5.New(rw), &cfg)
}

// workers is a private type so we can pass the river.Workers around without exposing it to other packages.
type workers struct{ *river.Workers }

func newRiverWorkers() *workers {
	return &workers{river.NewWorkers()}
}

// WithWorker can be provided as an fx option in a worker package to add it to the river worker set.
func WithWorker[T JobArgs]() fx.Option {
	return fx.Invoke(func(wrks *workers, wrk river.Worker[T]) error {
		if err := river.AddWorkerSafely(wrks.Workers, wrk); err != nil {
			return fmt.Errorf("add worker: %w", err)
		}

		return nil
	})
}

// Provide the components.
func Provide(cbf ...ClientBuilderFunc) fx.Option {
	if len(cbf) < 1 {
		cbf = append(cbf, NewRegularClient)
	}

	return stdfx.ZapEnvCfgModule[Config]("stdriver",
		New,
		fx.Provide(newRiverConfig, fx.Private),
		fx.Provide(fx.Annotate(cbf[0], fx.ParamTags(`name:"rw"`))),
		fx.Provide(newRiverWorkers),

		// ensure the client is actually started
		fx.Invoke(func(*Workers) {}),
	)
}
