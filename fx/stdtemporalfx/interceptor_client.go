package stdtemporalfx

import (
	"context"
	"fmt"

	"buf.build/go/protovalidate"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
)

// DefaultClientInterceptor validates workflow & signal/update args on the caller side.
type DefaultClientInterceptor struct {
	interceptor.ClientInterceptorBase

	validator protovalidate.Validator
}

func NewDefaultClientInterceptor(val protovalidate.Validator) *DefaultClientInterceptor {
	return &DefaultClientInterceptor{validator: val}
}

func (c *DefaultClientInterceptor) InterceptClient(
	next interceptor.ClientOutboundInterceptor,
) interceptor.ClientOutboundInterceptor {
	ci := &clientOutboundInterceptor{validator: c.validator}
	ci.Next = next
	return ci
}

type clientOutboundInterceptor struct {
	interceptor.ClientOutboundInterceptorBase

	validator protovalidate.Validator
}

// ExecuteWorkflow -> client.ExecuteWorkflow(...)
func (i *clientOutboundInterceptor) ExecuteWorkflow(
	ctx context.Context,
	in *interceptor.ClientExecuteWorkflowInput,
) (client.WorkflowRun, error) {
	if err := validateArgs(i.validator, in.Args); err != nil {
		return nil, fmt.Errorf("client validation: %w", err)
	}
	return i.Next.ExecuteWorkflow(ctx, in)
}

// SignalWithStartWorkflow -> client.SignalWithStartWorkflow(...)
func (i *clientOutboundInterceptor) SignalWithStartWorkflow(
	ctx context.Context,
	in *interceptor.ClientSignalWithStartWorkflowInput,
) (client.WorkflowRun, error) {
	// Validate the signal payload if present
	if in.SignalArg != nil {
		if err := validateArgs(i.validator, []any{in.SignalArg}); err != nil {
			return nil, err
		}
	}
	// Validate the start args too
	if err := validateArgs(i.validator, in.Args); err != nil {
		return nil, err
	}
	return i.Next.SignalWithStartWorkflow(ctx, in)
}

// SignalWorkflow -> client.SignalWorkflow(...)
func (i *clientOutboundInterceptor) SignalWorkflow(
	ctx context.Context,
	in *interceptor.ClientSignalWorkflowInput,
) error {
	if in.Arg != nil {
		if err := validateArgs(i.validator, []any{in.Arg}); err != nil {
			return err
		}
	}
	return i.Next.SignalWorkflow(ctx, in)
}

// UpdateWorkflow -> client.UpdateWorkflow(...)
func (i *clientOutboundInterceptor) UpdateWorkflow(
	ctx context.Context,
	in *interceptor.ClientUpdateWorkflowInput,
) (client.WorkflowUpdateHandle, error) {
	if err := validateArgs(i.validator, in.Args); err != nil {
		return nil, err
	}
	return i.Next.UpdateWorkflow(ctx, in)
}
