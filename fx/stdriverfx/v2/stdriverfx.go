// Package stdriverfx provides River clients that enqueue, work, and read jobs,
// for either edition of River: the caller picks the client's type with the
// function that builds it (see [Provide]). It builds on open-source River only,
// and never imports River Pro, so it builds without River Pro's license key.
// It is the second version of package stdriverfx, at the v2 path, which apps
// can adopt next to the first.
//
// River's tables live in their own PostgreSQL schema ([Config.Schema]), which
// the app's migrations create, so River's own migrator never runs here.
//
// Two clients are provided, named like the connection pools they run on: the
// "rw" client inserts jobs, with InsertTx in the transaction of the request,
// and works them once a worker is registered with [WithWorker]; the "ro"
// client only reads job state.
//
// Workers log with stdctx.Log(ctx): each job attempt has a logger in its
// context, whose lines River also stores with the attempt for the River UI.
package stdriverfx

import (
	"context"
	"log/slog"
	"time"

	"github.com/advdv/stdgo/stdfx"
	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	slogzap "github.com/samber/slog-zap/v2"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Client is what the module needs of a client to start and stop it with the
// app. Open-source River's client implements it, and so does River Pro's,
// which embeds it.
type Client interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	StopAndCancel(ctx context.Context) error
}

// NewClientFunc builds a client on pool, configured by cfg. [NewClient] builds
// open-source River's.
type NewClientFunc[C Client] func(pool *pgxpool.Pool, cfg *river.Config) (C, error)

// NewClient builds an open-source River client on pool, configured by cfg.
func NewClient(pool *pgxpool.Pool, cfg *river.Config) (*river.Client[pgx.Tx], error) {
	client, err := river.NewClient(riverpgxv5.New(pool), cfg)

	return client, errors.Wrap(err, "new river client")
}

// Config configures the clients, from the environment with the STDRIVER_ prefix.
type Config struct {
	// Schema is the PostgreSQL schema of River's tables. River's own migrator
	// never runs, so it must be the schema the app's migrations put them in.
	Schema string `env:"SCHEMA" envDefault:"river"`
	// MaxWorkers is how many jobs of the default queue this process works
	// concurrently.
	MaxWorkers int `env:"MAX_WORKERS" envDefault:"10"`
	// StopTimeout is how long shutdown waits for running jobs to finish before
	// cancelling them.
	StopTimeout time.Duration `env:"STOP_TIMEOUT" envDefault:"5s"`
}

// params are the clients' dependencies. Depending on the pools also makes fx
// stop the "rw" client before the pools close.
type params struct {
	fx.In
	fx.Lifecycle

	Config  Config
	Logs    *zap.Logger
	Workers *workers
	RW      *pgxpool.Pool `name:"rw"`
	RO      *pgxpool.Pool `name:"ro"`
}

// result are the clients, named like their pools.
type result[C Client] struct {
	fx.Out

	RW C `name:"rw"`
	RO C `name:"ro"`
}

// newClients builds both clients with newClient, and starts and stops the "rw"
// one with the app.
func newClients[C Client](par params, newClient NewClientFunc[C]) (result[C], error) {
	logs := slog.New(slogzap.Option{Logger: par.Logs}.NewZapHandler())

	rw, err := newClient(par.RW, &river.Config{
		Logger:  logs,
		Schema:  par.Config.Schema,
		Workers: par.Workers.Workers,
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: par.Config.MaxWorkers},
		},
		Middleware: logMiddleware(par.Logs),
	})
	if err != nil {
		return result[C]{}, errors.Wrap(err, "new read-write river client")
	}

	// Without queues the client cannot be started; it only reads.
	ro, err := newClient(par.RO, &river.Config{
		Logger: logs,
		Schema: par.Config.Schema,
	})
	if err != nil {
		return result[C]{}, errors.Wrap(err, "new read-only river client")
	}

	run := runner{cfg: par.Config, logs: par.Logs, client: rw, workers: par.Workers}
	par.Append(fx.Hook{OnStart: run.start, OnStop: run.stop})

	return result[C]{RW: rw, RO: ro}, nil
}

// runner starts and stops the "rw" client with the app.
type runner struct {
	cfg     Config
	logs    *zap.Logger
	client  Client
	workers *workers
}

// start works jobs when a worker is registered: River refuses to start without
// one, and the client inserts and reads jobs without starting. River derives
// every job's context from the one it starts with, so that context must not be
// cancelled when the start hook returns.
func (r runner) start(ctx context.Context) error {
	if len(r.workers.kinds) == 0 {
		r.logs.Info("no river workers registered, not working jobs")

		return nil
	}

	r.logs.Info("starting river client", zap.Strings("kinds", r.workers.kinds))

	return errors.Wrap(r.client.Start(context.WithoutCancel(ctx)), "start river client")
}

// stop fetching jobs and wait up to the stop timeout for running ones, then
// cancel those still running and wait for them to return.
func (r runner) stop(ctx context.Context) error {
	if len(r.workers.kinds) == 0 {
		return nil
	}

	softCtx, cancel := context.WithTimeout(ctx, r.cfg.StopTimeout)
	defer cancel()

	err := r.client.Stop(softCtx)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		return errors.Wrap(err, "stop river client")
	}

	r.logs.Warn("river jobs did not finish in time, cancelling them",
		zap.Duration("stop_timeout", r.cfg.StopTimeout))

	return errors.Wrap(r.client.StopAndCancel(ctx), "stop and cancel river client")
}

// Provide wires the module: the "rw" and "ro" clients, which newClient builds
// as C, on River's tables in the configured schema, from the "rw" and "ro"
// pools. It builds them even when nothing else depends on them, so the "rw"
// client always works the registered workers. Pass [NewClient] for
// open-source River's clients.
func Provide[C Client](newClient NewClientFunc[C]) fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdriver",
		func(p params) (result[C], error) { return newClients(p, newClient) },
		fx.Provide(newWorkers),
		fx.Invoke(fx.Annotate(func(C) {}, fx.ParamTags(`name:"rw"`))),
	)
}
