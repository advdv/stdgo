// Package stdcrpctest provides test assertions for Connect RPC code built on
// connect-go v2 (connectrpc.com/connect/v2).
package stdcrpctest

import (
	"testing"

	validatepb "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
)

// ProtoEq requires two protobuf messages to be equal, and shows a diff when
// they are not.
func ProtoEq(tb testing.TB, want, got proto.Message, msgAndArgs ...any) {
	tb.Helper()

	require.Empty(tb, cmp.Diff(want, got, protocmp.Transform()), msgAndArgs...)
}

// RequireViolations requires err to be an INVALID_ARGUMENT Connect error whose
// protovalidate violations have exactly the given rule IDs, in any order, such
// as "string.uuid". It returns the violations for further assertions.
func RequireViolations(tb testing.TB, err error, ruleIDs ...string) []*validatepb.Violation {
	tb.Helper()

	require.Equal(tb, connect.CodeInvalidArgument, connect.CodeOf(err), "error: %v", err)

	var connectErr *connect.Error
	require.ErrorAs(tb, err, &connectErr)

	var violations []*validatepb.Violation

	for _, detail := range connectErr.Details() {
		msg, err := connectproto.UnmarshalErrorDetail(detail)
		require.NoError(tb, err)

		if vs, ok := msg.(*validatepb.Violations); ok {
			violations = append(violations, vs.GetViolations()...)
		}
	}

	gotIDs := make([]string, 0, len(violations))
	for _, violation := range violations {
		gotIDs = append(gotIDs, violation.GetRuleId())
	}

	require.ElementsMatch(tb, ruleIDs, gotIDs, "violation rule IDs")

	return violations
}
