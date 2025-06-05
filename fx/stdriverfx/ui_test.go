package stdriverfx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"riverqueue.com/riverui"
)

func TestUIServer(t *testing.T) {
	var uis *riverui.Server
	_, _, _ = setup(t, &uis)

	require.NotNil(t, uis)

	rec, req := httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/states", nil)
	uis.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Result().StatusCode)
}
