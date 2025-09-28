package stdtemporalfx

import (
	"context"
	"fmt"
	"strings"

	"buf.build/go/protovalidate"
	"github.com/advdv/stdgo/stdctx"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

type DefaultWorkerInterceptor struct {
	interceptor.InterceptorBase

	logs      *zap.Logger
	validator protovalidate.Validator
}

// NewDefaultWorkerInterceptor initializes an interceptor for our worker. It implements
// cross-cutting concerns for all activity and workflow execution. Such as
// input validation.
func NewDefaultWorkerInterceptor(
	val protovalidate.Validator,
	logs *zap.Logger,
) *DefaultWorkerInterceptor {
	return &DefaultWorkerInterceptor{
		validator: val,
		logs:      logs.Named("worker"),
	}
}

// InterceptWorkflow intercepts workflows.
func (w *DefaultWorkerInterceptor) InterceptWorkflow(
	ctx workflow.Context, next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	i := &workflowInboundInterceptor{validator: w.validator}
	i.Next = next
	return i
}

// InterceptActivity intercepts activities.
func (w *DefaultWorkerInterceptor) InterceptActivity(
	ctx context.Context, next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	i := &activityInboundInterceptor{validator: w.validator, logs: w.logs.Named("actvity")}
	i.Next = next
	return i
}

// workflowInboundInterceptor describes the middleware for workflow execution.
type workflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase

	validator protovalidate.Validator
}

func (i *workflowInboundInterceptor) ExecuteWorkflow(
	ctx workflow.Context, in *interceptor.ExecuteWorkflowInput,
) (any, error) {
	if err := validateArgs(i.validator, in.Args); err != nil {
		return nil, err
	}

	return i.Next.ExecuteWorkflow(ctx, in)
}

// activityInboundInterceptor descreibes interception of activity execution.
type activityInboundInterceptor struct {
	interceptor.ActivityInboundInterceptorBase

	logs      *zap.Logger
	validator protovalidate.Validator
}

func (i *activityInboundInterceptor) ExecuteActivity(
	ctx context.Context, inp *interceptor.ExecuteActivityInput,
) (any, error) {
	if err := validateArgs(i.validator, inp.Args); err != nil {
		return nil, err
	}

	// we setup a new logger for each activity being executed.
	actInfo := activity.GetInfo(ctx)
	logs := i.logs.With(
		zap.String("activity_id", actInfo.ActivityID),
		zap.String("activity_type_name", actInfo.ActivityType.Name),
		zap.String("workflow_execution_id", actInfo.WorkflowExecution.ID),
		zap.String("workflow_execution_run_id", actInfo.WorkflowExecution.RunID),
	)

	// and include it in the context.
	ctx = stdctx.WithLogger(ctx, logs)

	return i.Next.ExecuteActivity(ctx, inp)
}

// validateArgs validate the arguments of activity or workflow execution. It assumes that
// either has at most 1 argument. And that this argumnet is a proto.Message.
func validateArgs(val protovalidate.Validator, args []any) error {
	if len(args) > 1 {
		return validationErrorf("more than 1 args, got: %d", "Input", len(args))
	}

	for _, arg := range args {
		msg, ok := arg.(proto.Message)
		if !ok {
			return validationErrorf("argument is not a proto.Messsage, got: %T", "Input", arg)
		}

		if err := val.Validate(msg); err != nil {
			return validationErrorf("validate input: %w", "Input", err)
		}
	}

	return nil
}

func validationErrorf(format string, side string, args ...any) error {
	return temporal.NewNonRetryableApplicationError(
		"validation failed for "+strings.ToLower(side), "Invalid"+side, fmt.Errorf(format, args...))
}
