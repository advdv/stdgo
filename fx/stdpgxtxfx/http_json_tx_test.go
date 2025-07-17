package stdpgxtxfx_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advdv/stdgo/fx/stdpgxtxfx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type (
	FooInput  struct{ In string }
	FooOutput struct{ Out string }
)

func TestHTTPJSONOk(t *testing.T) {
	t.Parallel()

	ctx, rw, _ := setup(t)

	resp, req := httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"In":"bar"}`))
	err := stdpgxtxfx.HJTx(ctx, rw, resp, req,
		func(ctx context.Context, l *zap.Logger, tx pgx.Tx, i *FooInput) (*FooOutput, error) {
			return &FooOutput{Out: i.In}, nil
		})

	require.NoError(t, err)
	require.JSONEq(t, `{"Out":"bar"}`, resp.Body.String())
}

func TestHTTPJSONOtherError(t *testing.T) {
	t.Parallel()

	ctx, rw, _ := setup(t)

	resp, req := httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"In":"bar"}`))
	err := stdpgxtxfx.HJTx(ctx, rw, resp, req,
		func(ctx context.Context, l *zap.Logger, tx pgx.Tx, i *FooInput) (*FooOutput, error) {
			return nil, errors.New("foo")
		})

	require.EqualError(t, err, "foo")
	require.Equal(t, ``, resp.Body.String())
}

func TestHTTPJSONPgError(t *testing.T) {
	t.Parallel()

	ctx, rw, _ := setup(t)

	resp, req := httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"In":"bar"}`))
	err := stdpgxtxfx.HJTx(ctx, rw, resp, req,
		func(ctx context.Context, l *zap.Logger, tx pgx.Tx, i *FooInput) (*FooOutput, error) {
			return nil, &pgconn.PgError{Code: "42501"}
		})

	require.EqualError(t, err, "permission_denied: database permission denied")
	require.Equal(t, ``, resp.Body.String())
}
