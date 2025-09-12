package stdtemporalfx

import (
	"context"
	"fmt"
	"strings"

	"buf.build/go/protovalidate"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

type WorkerInterceptor struct {
	interceptor.InterceptorBase

	validator protovalidate.Validator
}

// NewWorkerInterceptor initializes an interceptor for our worker. It implements
// cross-cutting concerns for all activity and workflow execution. Such as
// input validation.
func NewWorkerInterceptor(
	val protovalidate.Validator,
	logs *zap.Logger,
) *WorkerInterceptor {
	return &WorkerInterceptor{
		validator: val,
	}
}

// InterceptWorkflow intercepts workflows.
func (w *WorkerInterceptor) InterceptWorkflow(
	ctx workflow.Context, next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	i := &workflowInboundInterceptor{validator: w.validator}
	i.Next = next
	return i
}

// InterceptActivity intercepts activities.
func (w *WorkerInterceptor) InterceptActivity(
	ctx context.Context, next interceptor.ActivityInboundInterceptor,
) interceptor.ActivityInboundInterceptor {
	i := &activityInboundInterceptor{validator: w.validator}
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

	validator protovalidate.Validator
}

func (i *activityInboundInterceptor) ExecuteActivity(
	ctx context.Context, inp *interceptor.ExecuteActivityInput,
) (any, error) {
	if err := validateArgs(i.validator, inp.Args); err != nil {
		return nil, err
	}

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
