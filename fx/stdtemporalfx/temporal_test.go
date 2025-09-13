package stdtemporalfx_test

import (
	"context"
	"testing"

	"buf.build/go/protovalidate"
	"github.com/advdv/stdgo/fx/stdawsfx"
	"github.com/advdv/stdgo/fx/stdtemporalfx"
	internalv1 "github.com/advdv/stdgo/fx/stdtemporalfx/internal/v1"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/require"
	tworker "go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
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
	require.ErrorContains(t, err, "value is required")
}

func TestWorkflowExecution(t *testing.T) {
	t.Parallel()

	ctx, _, tst := setup(t)
	out, err := tst.W.Foo(ctx, internalv1.FooInput_builder{Bar: proto.String("abc")}.Build())
	require.NoError(t, err)
	require.NotNil(t, out)
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

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDTEMPORAL_TEMPORAL_HOST_PORT": "localhost:7244",
		}),
		stdtemporalfx.Provide(),
		stdawsfx.Provide(),

		stdtemporalfx.ProvideClient(internalv1.NewTestServiceClient),
		stdtemporalfx.ProvideRegistration(
			internalv1.TestServiceTaskQueue,
			func(w tworker.Worker, wf *testWorkflows, ac *testActivities) {
				internalv1.RegisterTestServiceWorkflows(w, wf)
				internalv1.RegisterTestServiceActivities(w, ac)
			},
		),

		stdzapfx.TestProvide(tb),

		fx.Provide(newTestWorkflows),
		fx.Provide(newTestActivities),

		fx.Provide(protovalidate.New),
		fx.Populate(&deps),
		fx.Populate(other...))

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

func (testWorkflows) Foo(workflow.Context, *internalv1.FooWorkflowInput) (internalv1.FooWorkflow, error) {
	return &fooWorkflow{}, nil
}

type testActivities struct{}

func newTestActivities() *testActivities {
	return &testActivities{}
}
