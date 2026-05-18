package stdtemporalcodecfx_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advdv/stdgo/fx/stdtemporalcodecfx"
	"github.com/advdv/stdgo/fx/stdtemporalcodecfx/stdtemporalcodec"
	"github.com/advdv/stdgo/fx/stdtemporalcodecfx/stdtemporalcodec/stdtemporalcodectest"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"google.golang.org/protobuf/encoding/protojson"
)

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

	fk := stdtemporalcodectest.NewFakeKMS()
	env := map[string]string{
		"STDTEMPORALCODEC_ENABLED":    "true",
		"STDTEMPORALCODEC_KMS_KEY_ID": "arn:aws:kms:us-east-1:123:key/abc",
		"STDTEMPORALCODEC_NAMESPACE":  "tenant-a",
	}

	var dc converter.DataConverter
	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.TestProvide(t),
		fx.Supply(fx.Annotate(fk, fx.As(new(stdtemporalcodec.KMS)))),
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
	require.Positive(t, fk.GenerateCalls.Load())
	require.Positive(t, fk.DecryptCalls.Load())
}

func TestProvideServer_RoundTrip(t *testing.T) {
	t.Parallel()

	fk := stdtemporalcodectest.NewFakeKMS()
	env := map[string]string{
		"STDTEMPORALCODECSERVER_KMS_KEY_ID":         "arn:aws:kms:us-east-1:123:key/abc",
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
		fx.Supply(fx.Annotate(fk, fx.As(new(stdtemporalcodec.KMS)))),
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

	fk := stdtemporalcodectest.NewFakeKMS()
	env := map[string]string{
		"STDTEMPORALCODECSERVER_KMS_KEY_ID":         "arn:aws:kms:us-east-1:123:key/abc",
		"STDTEMPORALCODECSERVER_ALLOWED_NAMESPACES": "tenant-a",
	}

	var deps struct {
		fx.In
		Handler http.Handler `name:"codec"`
	}
	app := fxtest.New(t,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.TestProvide(t),
		fx.Supply(fx.Annotate(fk, fx.As(new(stdtemporalcodec.KMS)))),
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
