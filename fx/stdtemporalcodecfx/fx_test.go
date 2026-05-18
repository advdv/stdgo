package stdtemporalcodecfx_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advdv/stdgo/fx/stdtemporalcodecfx"
	"github.com/advdv/stdgo/fx/stdtemporalcodecfx/stdtemporalcodec"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"google.golang.org/protobuf/encoding/protojson"
)

// freshKeyset returns a base64-encoded JSON cleartext AES-256-GCM Tink
// keyset suitable for the STDTEMPORALCODEC*_KEYSET env vars.
func freshKeyset(t *testing.T) string {
	t.Helper()
	handle, err := keyset.NewHandle(aead.AES256GCMKeyTemplate())
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, insecurecleartextkeyset.Write(handle, keyset.NewJSONWriter(&buf)))
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestProvide_Disabled_ProvidesDefaultConverter(t *testing.T) {
	t.Parallel()

	var dc converter.DataConverter
	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{}),
		stdzapfx.TestProvide(t),
		stdtemporalcodecfx.Provide(),
		fx.Populate(&dc),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, dc)

	// A round-trip through the default converter should preserve a string.
	payloads, err := dc.ToPayloads("hello")
	require.NoError(t, err)
	var got string
	require.NoError(t, dc.FromPayloads(payloads, &got))
	require.Equal(t, "hello", got)
}

func TestProvide_Enabled_ProvidesCodecWrappedConverter(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"STDTEMPORALCODEC_ENABLED":   "true",
		"STDTEMPORALCODEC_KEYSET":    freshKeyset(t),
		"STDTEMPORALCODEC_NAMESPACE": "tenant-a",
	}

	var dc converter.DataConverter
	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.TestProvide(t),
		stdtemporalcodecfx.Provide(),
		fx.Populate(&dc),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, dc)

	// Round-trip a string and verify it actually went through the codec.
	payloads, err := dc.ToPayloads("secret-value")
	require.NoError(t, err)
	require.NotEmpty(t, payloads.GetPayloads())
	// Each payload should now be tagged binary/encrypted.
	for _, p := range payloads.GetPayloads() {
		assert.Equal(t, []byte("binary/encrypted"), p.GetMetadata()["encoding"])
		assert.Equal(t, []byte("tenant-a"), p.GetMetadata()[stdtemporalcodec.MetadataContextNamespace])
	}

	var got string
	require.NoError(t, dc.FromPayloads(payloads, &got))
	require.Equal(t, "secret-value", got)
}

func TestProvide_Enabled_RejectsInvalidKeyset(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"STDTEMPORALCODEC_ENABLED":   "true",
		"STDTEMPORALCODEC_KEYSET":    "not-base64!!!",
		"STDTEMPORALCODEC_NAMESPACE": "tenant-a",
	}

	var dc converter.DataConverter
	app := fx.New(
		fx.NopLogger,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.TestProvide(t),
		stdtemporalcodecfx.Provide(),
		fx.Populate(&dc),
	)
	require.Error(t, app.Err())
}

func TestProvideServer_RoundTrip(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"STDTEMPORALCODECSERVER_ENABLED":            "true",
		"STDTEMPORALCODECSERVER_KEYSET":             freshKeyset(t),
		"STDTEMPORALCODECSERVER_ALLOWED_NAMESPACES": "tenant-a,tenant-b",
		"STDTEMPORALCODECSERVER_STRIP_CLOUD_SUFFIX": "true",
	}

	var deps struct {
		fx.In
		Handler http.Handler `name:"codec"`
	}
	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.TestProvide(t),
		stdtemporalcodecfx.ProvideServer(),
		fx.Populate(&deps),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	require.NotNil(t, deps.Handler)

	srv := httptest.NewServer(deps.Handler)
	t.Cleanup(srv.Close)

	// Encode a payload under tenant-a (using the Cloud-style suffixed
	// header to also exercise StripCloudAccountSuffix).
	input := []*commonpb.Payload{{
		Metadata: map[string][]byte{"encoding": []byte("json/plain")},
		Data:     []byte(`{"hello":"world"}`),
	}}
	encoded := roundTrip(t, srv, "/encode", "tenant-a.acct-123", input)
	require.Len(t, encoded, 1)
	assert.Equal(t, []byte("binary/encrypted"), encoded[0].GetMetadata()["encoding"])
	assert.Equal(t, []byte("tenant-a"), encoded[0].GetMetadata()[stdtemporalcodec.MetadataContextNamespace])

	// Decode it back.
	decoded := roundTrip(t, srv, "/decode", "tenant-a.acct-123", encoded)
	require.Len(t, decoded, 1)
	assert.Equal(t, input[0].GetData(), decoded[0].GetData())
}

func TestProvideServer_RejectsUnknownNamespace(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"STDTEMPORALCODECSERVER_ENABLED":            "true",
		"STDTEMPORALCODECSERVER_KEYSET":             freshKeyset(t),
		"STDTEMPORALCODECSERVER_ALLOWED_NAMESPACES": "tenant-a",
	}

	var deps struct {
		fx.In
		Handler http.Handler `name:"codec"`
	}
	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.TestProvide(t),
		stdtemporalcodecfx.ProvideServer(),
		fx.Populate(&deps),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	srv := httptest.NewServer(deps.Handler)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/encode", "tenant-b", marshalPayloads(t, nil))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// TestProvideServer_Disabled_ProducesStubHandler proves the default-off
// path: when STDTEMPORALCODECSERVER_ENABLED is unset (or "false"), the
// graph still produces a named "codec" http.Handler so consumers can
// mount it unconditionally — but every request to it must 404. This
// keeps prod composition roots free of conditional wiring while
// guaranteeing the codec endpoint is inert until the operator opts in.
func TestProvideServer_Disabled_ProducesStubHandler(t *testing.T) {
	t.Parallel()

	var deps struct {
		fx.In
		Handler http.Handler `name:"codec"`
	}
	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{}),
		stdzapfx.TestProvide(t),
		stdtemporalcodecfx.ProvideServer(),
		fx.Populate(&deps),
	)
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	require.NotNil(t, deps.Handler)

	srv := httptest.NewServer(deps.Handler)
	t.Cleanup(srv.Close)

	resp := post(t, srv, "/encode", "tenant-a", marshalPayloads(t, nil))
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"disabled codec server must reject all requests with 404")
}

// roundTrip POSTs payloads to the given codec endpoint and returns the
// decoded response payloads, failing the test on any non-200 status.
func roundTrip(t *testing.T, srv *httptest.Server, path, ns string, payloads []*commonpb.Payload) []*commonpb.Payload {
	t.Helper()
	resp := post(t, srv, path, ns, marshalPayloads(t, payloads))
	require.Equal(t, http.StatusOK, resp.StatusCode, "body=%s", string(resp.Body))
	var out commonpb.Payloads
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	return out.GetPayloads()
}

type response struct {
	StatusCode int
	Body       []byte
}

func post(t *testing.T, srv *httptest.Server, path, ns string, body []byte) response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set(stdtemporalcodec.HeaderNamespace, ns)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	bs, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return response{StatusCode: resp.StatusCode, Body: bs}
}

func marshalPayloads(t *testing.T, payloads []*commonpb.Payload) []byte {
	t.Helper()
	bs, err := protojson.Marshal(&commonpb.Payloads{Payloads: payloads})
	require.NoError(t, err)
	return bs
}
