package stdtemporalfx

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"log/slog"
	"time"

	"github.com/advdv/stdgo/stdfx"
	slogzap "github.com/samber/slog-zap/v2"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/durationpb"

	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

type Config struct {
	// temporal server grpc
	TemporalHostPort string `env:"TEMPORAL_HOST_PORT" envDefault:"localhost:7233"`
	// temporal namespace for this deployment
	TemporalNamespace string `env:"TEMPORAL_NAMESPACE" envDefault:"default"`
	// API key for authentication with the cluster.
	TemporalAPIKey string `env:"TEMPORAL_API_KEY"`
	// create and use randomly-named namespace, mainly for testing.
	CreateAndUseRandomNamespace bool `env:"CREATE_AND_USE_RANDOM_NAMESPACE"`
	// remove the namespace when the client shuts down, only taken into account with: CREATE_AND_USE_RANDOM_NAMESPACE
	AutoRemoveNamespace bool `env:"AUTO_REMOVE_NAMESPACE"`
}

// Temporal holds a reference to the Temporal Temporal.
type Temporal struct {
	cfg       Config
	logs      *zap.Logger
	namespace string
	c         client.Client
	nsClient  client.NamespaceClient
	cintr     *ClientInterceptor
}

// New inits the Temporal client.
func New(par struct {
	fx.In
	fx.Lifecycle

	Config            Config
	Logger            *zap.Logger
	ClientInterceptor *ClientInterceptor
},
) (*Temporal, error) {
	c := &Temporal{
		cfg:   par.Config,
		logs:  par.Logger,
		cintr: par.ClientInterceptor,
	}

	par.Append(fx.Hook{
		OnStart: c.Start,
		OnStop:  c.Stop,
	})

	return c, nil
}

// Start connects to the Temporal server.
func (c *Temporal) Start(ctx context.Context) (err error) {
	slogs := slog.New(slogzap.Option{
		Level:  slog.LevelDebug,
		Logger: c.logs,
	}.NewZapHandler())

	c.namespace = c.cfg.TemporalNamespace

	// having written some actual e2e test it seems that there is no full isolation between workflows
	// if they run on the same worker queue. The easiest way to isolate each test is to create a dedicated
	// namespace.
	if c.cfg.CreateAndUseRandomNamespace {
		c.nsClient, err = client.NewNamespaceClient(client.Options{
			HostPort: c.cfg.TemporalHostPort,
			Logger:   tlog.NewStructuredLogger(slogs),
		})
		if err != nil {
			return fmt.Errorf("init namespace client: %w", err)
		}

		// create a new namespace for this test.
		rngb := make([]byte, 6)
		rand.Read(rngb) //nolint:errcheck
		c.namespace = fmt.Sprintf("%s-%x", c.namespace, rngb)

		if err := c.nsClient.Register(ctx, &workflowservice.RegisterNamespaceRequest{
			Namespace:                        c.namespace,
			WorkflowExecutionRetentionPeriod: durationpb.New(time.Hour),
		}); err != nil {
			return fmt.Errorf("register randomly-named namespace: %w", err)
		}
	}

	opts := client.Options{
		HostPort:           c.cfg.TemporalHostPort,
		Logger:             tlog.NewStructuredLogger(slogs),
		Namespace:          c.namespace,
		ContextPropagators: []workflow.ContextPropagator{},
		Interceptors:       []interceptor.ClientInterceptor{c.cintr},
	}

	if c.cfg.TemporalAPIKey != "" {
		opts.Credentials = client.NewAPIKeyStaticCredentials(c.cfg.TemporalAPIKey)
		opts.ConnectionOptions = client.ConnectionOptions{
			TLS: &tls.Config{}, //nolint:gosec
		}
	}

	c.c, err = client.DialContext(ctx, opts)
	if err != nil {
		return fmt.Errorf("dial temporal: %w", err)
	}

	return nil
}

func (c *Temporal) CheckHealth(ctx context.Context) error {
	// NOTE: frustratingly, the client.CheckHealth doesn't seem to work with Temporal cloud. Others seem to confirm:
	// https://community.temporal.io/t/python-sdk-health-check-to-temporal-cloud-fails-with-request-unauthorized/17112
	_, err := c.c.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{})
	if err != nil {
		return fmt.Errorf("check health: %w", err)
	}

	return nil
}

// Namespace this client is using.
func (c *Temporal) Namespace() string {
	return c.namespace
}

// Stop disconnects from the Temporal server.
func (c *Temporal) Stop(ctx context.Context) (err error) {
	if c.cfg.CreateAndUseRandomNamespace && c.cfg.AutoRemoveNamespace {
		if _, err = c.c.OperatorService().DeleteNamespace(ctx, &operatorservice.DeleteNamespaceRequest{
			Namespace: c.Namespace(),
		}); err != nil {
			return fmt.Errorf("delete namespace: %w", err)
		}
	}

	// close the namespace client, if setup.
	if c.nsClient != nil {
		c.nsClient.Close()
	}

	c.c.Close() // close the client
	return nil
}

func Provide() fx.Option {
	return stdfx.ZapEnvCfgModule[Config]("stdtemporal",
		New,
		fx.Provide(NewWorkerInterceptor, NewClientInterceptor),
		fx.Provide(NewWorkers),
	)
}
