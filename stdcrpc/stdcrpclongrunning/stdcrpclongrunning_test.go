package stdcrpclongrunning_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"cloud.google.com/go/longrunning/autogen/longrunningpb"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/advdv/stdgo/stdcrpc/stdcrpclongrunning"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// operations implements only GetOperation, which knows one operation.
type operations struct {
	stdcrpclongrunning.UnimplementedOperationsHandler
}

func (operations) GetOperation(
	_ context.Context, req *longrunningpb.GetOperationRequest,
) (*longrunningpb.Operation, error) {
	if req.GetName() != "operations/known" {
		return nil, connect.NewError(connect.CodeNotFound, "no such operation")
	}

	return &longrunningpb.Operation{Name: req.GetName(), Done: true}, nil
}

func setup(tb testing.TB) stdcrpclongrunning.OperationsClient {
	tb.Helper()

	server := connect.NewServer()
	stdcrpclongrunning.RegisterOperationsHandler(server, operations{})

	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)

	srv := httptest.NewServer(mux)
	tb.Cleanup(srv.Close)

	return stdcrpclongrunning.NewOperationsClient(
		connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
}

func TestGetOperation(t *testing.T) {
	t.Parallel()
	client := setup(t)

	op, err := client.GetOperation(t.Context(), &longrunningpb.GetOperationRequest{Name: "operations/known"})
	require.NoError(t, err)
	require.True(t, proto.Equal(&longrunningpb.Operation{Name: "operations/known", Done: true}, op))
}

func TestGetOperationReturnsHandlerError(t *testing.T) {
	t.Parallel()
	client := setup(t)

	_, err := client.GetOperation(t.Context(), &longrunningpb.GetOperationRequest{Name: "operations/other"})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestUnimplementedMethods(t *testing.T) {
	t.Parallel()
	client := setup(t)
	ctx := t.Context()

	for name, call := range map[string]func() error{
		"ListOperations": func() error {
			_, err := client.ListOperations(ctx, &longrunningpb.ListOperationsRequest{})
			return err
		},
		"DeleteOperation": func() error {
			_, err := client.DeleteOperation(ctx, &longrunningpb.DeleteOperationRequest{})
			return err
		},
		"CancelOperation": func() error {
			_, err := client.CancelOperation(ctx, &longrunningpb.CancelOperationRequest{})
			return err
		},
		"WaitOperation": func() error {
			_, err := client.WaitOperation(ctx, &longrunningpb.WaitOperationRequest{})
			return err
		},
	} {
		require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(call()), name)
	}
}
