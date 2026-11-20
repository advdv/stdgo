// Package stdcrpclongrunning serves and calls Google's
// google.longrunning.Operations service (AIP-151) with connect-go v2, in the
// shape protoc-gen-connect-go generates for other services. buf generates only
// from a module's own protobuf files, and this service is Google's, defined in
// google/longrunning/operations.proto; its messages come from longrunningpb.
// The method descriptors carry that file's google.api.http annotations, so a
// REST transcoder such as Vanguard serves the service like any other.
package stdcrpclongrunning

import (
	"context"

	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/emptypb"
)

// OperationsServiceName is the fully-qualified name of the Operations service.
const OperationsServiceName = "google.longrunning.Operations"

// The procedure names of the Operations service's RPCs.
const (
	OperationsListOperationsProcedure  = "/google.longrunning.Operations/ListOperations"
	OperationsGetOperationProcedure    = "/google.longrunning.Operations/GetOperation"
	OperationsDeleteOperationProcedure = "/google.longrunning.Operations/DeleteOperation"
	OperationsCancelOperationProcedure = "/google.longrunning.Operations/CancelOperation"
	OperationsWaitOperationProcedure   = "/google.longrunning.Operations/WaitOperation"
)

// spec describes the Operations service's method name, from the descriptor in
// longrunningpb.
func spec(name, procedure string, idempotency connect.IdempotencyLevel) connect.Spec {
	return connect.Spec{
		StreamType: connect.StreamTypeUnary,
		Schema: longrunningpb.File_google_longrunning_operations_proto.
			Services().ByName("Operations").Methods().ByName(protoreflect.Name(name)),
		Procedure:        procedure,
		IdempotencyLevel: idempotency,
	}
}

// The specs of the Operations service's RPCs.
var (
	listOperationsSpec  = spec("ListOperations", OperationsListOperationsProcedure, connect.IdempotencyNoSideEffects)
	getOperationSpec    = spec("GetOperation", OperationsGetOperationProcedure, connect.IdempotencyNoSideEffects)
	deleteOperationSpec = spec("DeleteOperation", OperationsDeleteOperationProcedure, connect.IdempotencyIdempotent)
	cancelOperationSpec = spec("CancelOperation", OperationsCancelOperationProcedure, connect.IdempotencyUnknown)
	waitOperationSpec   = spec("WaitOperation", OperationsWaitOperationProcedure, connect.IdempotencyUnknown)
)

// OperationsHandler is an implementation of the Operations service. Embed
// UnimplementedOperationsHandler for the methods a server does not support,
// which then fail with UNIMPLEMENTED, as operations.proto allows for every
// method but GetOperation.
type OperationsHandler interface {
	ListOperations(
		ctx context.Context, req *longrunningpb.ListOperationsRequest,
	) (*longrunningpb.ListOperationsResponse, error)
	GetOperation(ctx context.Context, req *longrunningpb.GetOperationRequest) (*longrunningpb.Operation, error)
	DeleteOperation(ctx context.Context, req *longrunningpb.DeleteOperationRequest) (*emptypb.Empty, error)
	CancelOperation(ctx context.Context, req *longrunningpb.CancelOperationRequest) (*emptypb.Empty, error)
	WaitOperation(ctx context.Context, req *longrunningpb.WaitOperationRequest) (*longrunningpb.Operation, error)
}

// RegisterOperationsHandler registers svc as the Operations implementation on
// server.
func RegisterOperationsHandler(server *connect.Server, svc OperationsHandler) {
	server.Register(
		connect.Method{Spec: listOperationsSpec, Handler: handle(svc.ListOperations)},
		connect.Method{Spec: getOperationSpec, Handler: handle(svc.GetOperation)},
		connect.Method{Spec: deleteOperationSpec, Handler: handle(svc.DeleteOperation)},
		connect.Method{Spec: cancelOperationSpec, Handler: handle(svc.CancelOperation)},
		connect.Method{Spec: waitOperationSpec, Handler: handle(svc.WaitOperation)},
	)
}

// handle adapts a unary method to a connect.ServerFunc. Like generated code,
// it returns the errors of the stream and of the method as they are, so a
// method's *connect.Error reaches the interceptors.
func handle[Req, Res any](call func(context.Context, *Req) (*Res, error)) connect.ServerFunc {
	return func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
		var req Req
		if err := stream.Receive(&req); err != nil {
			return err
		}

		res, err := call(ctx, &req)
		if err != nil {
			return err
		}

		return stream.Send(res)
	}
}

// UnimplementedOperationsHandler returns UNIMPLEMENTED from every method.
type UnimplementedOperationsHandler struct{}

// ListOperations implements OperationsHandler.
func (UnimplementedOperationsHandler) ListOperations(
	context.Context, *longrunningpb.ListOperationsRequest,
) (*longrunningpb.ListOperationsResponse, error) {
	return nil, unimplemented("ListOperations")
}

// GetOperation implements OperationsHandler.
func (UnimplementedOperationsHandler) GetOperation(
	context.Context, *longrunningpb.GetOperationRequest,
) (*longrunningpb.Operation, error) {
	return nil, unimplemented("GetOperation")
}

// DeleteOperation implements OperationsHandler.
func (UnimplementedOperationsHandler) DeleteOperation(
	context.Context, *longrunningpb.DeleteOperationRequest,
) (*emptypb.Empty, error) {
	return nil, unimplemented("DeleteOperation")
}

// CancelOperation implements OperationsHandler.
func (UnimplementedOperationsHandler) CancelOperation(
	context.Context, *longrunningpb.CancelOperationRequest,
) (*emptypb.Empty, error) {
	return nil, unimplemented("CancelOperation")
}

// WaitOperation implements OperationsHandler.
func (UnimplementedOperationsHandler) WaitOperation(
	context.Context, *longrunningpb.WaitOperationRequest,
) (*longrunningpb.Operation, error) {
	return nil, unimplemented("WaitOperation")
}

// unimplemented is the error of a method the server does not support.
func unimplemented(method string) error {
	return connect.NewError(connect.CodeUnimplemented, OperationsServiceName+"."+method+" is not implemented")
}

// OperationsClient is a client for the Operations service, which has the
// methods of OperationsHandler.
type OperationsClient = OperationsHandler

// NewOperationsClient constructs a client for the Operations service.
func NewOperationsClient(client *connect.Client) OperationsClient {
	return operationsClient{client: client}
}

type operationsClient struct{ client *connect.Client }

// call calls the unary RPC of spec, and returns its error as it is, as
// generated clients do, so callers read its code with connect.CodeOf.
func call[Res any](ctx context.Context, client *connect.Client, spec connect.Spec, req any) (*Res, error) {
	var res Res
	if err := client.CallUnary(ctx, spec, req, &res); err != nil {
		return nil, err
	}

	return &res, nil
}

func (c operationsClient) ListOperations(
	ctx context.Context, req *longrunningpb.ListOperationsRequest,
) (*longrunningpb.ListOperationsResponse, error) {
	return call[longrunningpb.ListOperationsResponse](ctx, c.client, listOperationsSpec, req)
}

func (c operationsClient) GetOperation(
	ctx context.Context, req *longrunningpb.GetOperationRequest,
) (*longrunningpb.Operation, error) {
	return call[longrunningpb.Operation](ctx, c.client, getOperationSpec, req)
}

func (c operationsClient) DeleteOperation(
	ctx context.Context, req *longrunningpb.DeleteOperationRequest,
) (*emptypb.Empty, error) {
	return call[emptypb.Empty](ctx, c.client, deleteOperationSpec, req)
}

func (c operationsClient) CancelOperation(
	ctx context.Context, req *longrunningpb.CancelOperationRequest,
) (*emptypb.Empty, error) {
	return call[emptypb.Empty](ctx, c.client, cancelOperationSpec, req)
}

func (c operationsClient) WaitOperation(
	ctx context.Context, req *longrunningpb.WaitOperationRequest,
) (*longrunningpb.Operation, error) {
	return call[longrunningpb.Operation](ctx, c.client, waitOperationSpec, req)
}
