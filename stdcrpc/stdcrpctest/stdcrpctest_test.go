package stdcrpctest_test

import (
	"errors"
	"runtime"
	"testing"

	validatepb "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/advdv/stdgo/stdcrpc/stdcrpctest"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestProtoEq(t *testing.T) {
	t.Parallel()

	require.False(t, fails(func(tb testing.TB) {
		stdcrpctest.ProtoEq(tb, wrapperspb.String("a"), wrapperspb.String("a"))
	}))
	require.True(t, fails(func(tb testing.TB) {
		stdcrpctest.ProtoEq(tb, wrapperspb.String("a"), wrapperspb.String("b"))
	}))
}

func TestRequireViolations(t *testing.T) {
	t.Parallel()

	err := violationsError(t, "string.uuid", "int32.gte")

	violations := stdcrpctest.RequireViolations(t, err, "int32.gte", "string.uuid")
	require.Len(t, violations, 2)
}

func TestRequireViolationsFails(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		err     error
		ruleIDs []string
	}{
		{name: "nil error", err: nil, ruleIDs: []string{"string.uuid"}},
		{name: "not a connect error", err: errors.New("boom"), ruleIDs: []string{"string.uuid"}},
		{
			name:    "other code",
			err:     connect.NewError(connect.CodeNotFound, "not found"),
			ruleIDs: []string{"string.uuid"},
		},
		{name: "other rule", err: violationsError(t, "string.uuid"), ruleIDs: []string{"int32.gte"}},
		{name: "missing rule", err: violationsError(t, "string.uuid"), ruleIDs: []string{"string.uuid", "int32.gte"}},
		{name: "extra rule", err: violationsError(t, "string.uuid", "int32.gte"), ruleIDs: []string{"string.uuid"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.True(t, fails(func(tb testing.TB) {
				stdcrpctest.RequireViolations(tb, tc.err, tc.ruleIDs...)
			}))
		})
	}
}

// violationsError builds the INVALID_ARGUMENT error that protovalidate's
// Connect interceptor returns, with a violation for each rule ID.
func violationsError(tb testing.TB, ruleIDs ...string) error {
	tb.Helper()

	violations := &validatepb.Violations{}
	for _, id := range ruleIDs {
		violations.Violations = append(violations.Violations, &validatepb.Violation{RuleId: new(id)})
	}

	detail, err := connectproto.NewErrorDetail(violations)
	require.NoError(tb, err)

	return connect.NewError(connect.CodeInvalidArgument, "invalid request").WithDetail(detail)
}

// fails reports whether the assertions in fn fail, running them against a
// recording testing.TB instead of the test's own.
func fails(fn func(tb testing.TB)) bool {
	rec := &recorder{}
	done := make(chan struct{})

	go func() {
		defer close(done)

		fn(rec)
	}()

	<-done

	return rec.failed
}

// recorder records whether an assertion failed. FailNow ends the goroutine
// that runs the assertions, as it does for a real test.
type recorder struct {
	testing.TB

	failed bool
}

func (r *recorder) Helper() {}

func (r *recorder) Name() string { return "recorder" }

func (r *recorder) Errorf(string, ...any) { r.failed = true }

func (r *recorder) FailNow() {
	r.failed = true

	runtime.Goexit()
}
