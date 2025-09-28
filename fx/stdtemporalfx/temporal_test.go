package stdtemporalfx_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"buf.build/go/protovalidate"
	"github.com/advdv/stdgo/fx/stdtemporalfx"
	internalv1 "github.com/advdv/stdgo/fx/stdtemporalfx/internal/v1"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/interceptor"
	tworker "go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/proto"
)

func TestSetup(t *testing.T) {
	t.Parallel()
	ctx, wrks, tst := setup(t)
	require.NotNil(t, ctx)
	require.NotNil(t, wrks)
	require.NotNil(t, tst)
}

func TestValidation(t *testing.T) {
	t.Parallel()

	ctx, _, tst := setup(t)
	_, err := tst.W.Foo(ctx, &internalv1.FooInput{})

	// this asserts that some validation interceptor is included
	require.ErrorContains(t, err, "value is required")
	// make sure it is done on the client (not just on the workers)
	require.ErrorContains(t, err, "client validation")
}

func TestWorkflowExecution(t *testing.T) {
	t.Parallel()

	var obs *observer.ObservedLogs
	ctx, _, tst := setup(t, &obs)
	out, err := tst.W.Foo(ctx, internalv1.FooInput_builder{Bar: proto.String("abc")}.Build())
	require.NoError(t, err)
	require.NotNil(t, out)

	// below asserts that the worker interceptor is being included.
	require.GreaterOrEqual(t, obs.FilterMessage("ran bar").Len(), 1)
}

func setup(tb testing.TB, other ...any) (
	context.Context,
	*stdtemporalfx.Workers,
	*stdtemporalfx.Client[internalv1.TestServiceClient],
) {
	var deps struct {
		fx.In

		Workers *stdtemporalfx.Workers
		Client  *stdtemporalfx.Temporal

		Test *stdtemporalfx.Client[internalv1.TestServiceClient]
	}

	// explicit test environment.
	env := map[string]string{
		// configure the instance we've running in Docker
		"STDTEMPORAL_TEMPORAL_HOST_PORT": "localhost:7244",
		// create a random namespace for each test, to isolate them
		"STDTEMPORAL_CREATE_AND_USE_RANDOM_NAMESPACE": "true",
		// in general we need to clean-up namespaces or they will slow down Temporal.
		"STDTEMPORAL_AUTO_REMOVE_NAMESPACE": "true",
	}

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.TestProvide(tb),
		stdtemporalfx.Provide(),
		stdtemporalfx.ProvideClient(internalv1.NewTestServiceClient),

		// temporal middleware, with order.
		fx.Provide(func(par struct {
			fx.In
			*stdtemporalfx.DefaultWorkerInterceptor
			*stdtemporalfx.DefaultClientInterceptor
		}) (res struct {
			fx.Out
			WorkerInterceptors []interceptor.WorkerInterceptor
			ClientInterceptors []interceptor.ClientInterceptor
		},
		) {
			res.WorkerInterceptors = append(res.WorkerInterceptors, par.DefaultWorkerInterceptor)
			res.ClientInterceptors = append(res.ClientInterceptors, par.DefaultClientInterceptor)
			return
		}),

		// worker registration.
		stdtemporalfx.ProvideRegistration(
			internalv1.TestServiceTaskQueue,
			func(w tworker.Worker, wf *testWorkflows, ac *testActivities) {
				internalv1.RegisterTestServiceWorkflows(w, wf)
				internalv1.RegisterTestServiceActivities(w, ac)
			},
		),

		// workflows and activiites for testing
		fx.Provide(newTestWorkflows),
		fx.Provide(newTestActivities),

		// floating dependencies.
		fx.Provide(protovalidate.New),

		// populate code under test
		fx.Populate(&deps), fx.Populate(other...),

		// in debug mode, print workflow info.
		fx.Invoke(func(lc fx.Lifecycle, temporalClient *stdtemporalfx.Temporal) {
			lc.Append(fx.Hook{OnStart: func(context.Context) error {
				if os.Getenv("DEBUG") != "" {
					fmt.Fprintf(os.Stderr, "%s: Workflow UI: %s\n", tb.Name(),
						fmt.Sprintf("http://localhost:7979/namespaces/%s/workflows", temporalClient.Namespace()))
				}

				return nil
			}})
		}),
	)

	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	ctx := tb.Context()

	return ctx, deps.Workers, deps.Test
}

type testWorkflows struct{}

func newTestWorkflows() *testWorkflows {
	return &testWorkflows{}
}

type fooWorkflow struct{}

func (fooWorkflow) Execute(ctx workflow.Context) (*internalv1.FooOutput, error) {
	return &internalv1.FooOutput{}, nil
}

func (testWorkflows) Foo(ctx workflow.Context, _ *internalv1.FooWorkflowInput) (internalv1.FooWorkflow, error) {
	if _, err := internalv1.Bar(ctx, &internalv1.BarInput{}); err != nil {
		return nil, fmt.Errorf("bar: %w", err)
	}

	return &fooWorkflow{}, nil
}

type testActivities struct{}

func newTestActivities() *testActivities {
	return &testActivities{}
}

func (a *testActivities) Bar(ctx context.Context, _ *internalv1.BarInput) (*internalv1.BarOutput, error) {
	stdctx.Log(ctx).Info("ran bar")

	return &internalv1.BarOutput{}, nil
}
