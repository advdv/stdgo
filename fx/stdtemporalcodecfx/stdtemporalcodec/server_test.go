package stdtemporalcodec_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advdv/stdgo/fx/stdtemporalcodecfx/stdtemporalcodec"
	"github.com/advdv/stdgo/fx/stdtemporalcodecfx/stdtemporalcodec/stdtemporalcodectest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/encoding/protojson"
)

func newServer(t *testing.T, allowed []string) (*httptest.Server, *stdtemporalcodec.Codec, *stdtemporalcodectest.FakeKMS) {
	t.Helper()
	return newServerWith(t, stdtemporalcodec.HandlerOptions{AllowedNamespaces: allowed})
}

func newServerWith(t *testing.T, opts stdtemporalcodec.HandlerOptions) (*httptest.Server, *stdtemporalcodec.Codec, *stdtemporalcodectest.FakeKMS) {
	t.Helper()
	fk := stdtemporalcodectest.NewFakeKMS()
	codec, err := stdtemporalcodec.New(fk, stdtemporalcodec.Options{
		KeyID:     "arn:aws:kms:us-east-1:123:key/abc",
		Namespace: "unused-default",
	})
	require.NoError(t, err)

	opts.Codec = codec
	h, err := stdtemporalcodec.Handler(opts)
	require.NoError(t, err)

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, codec, fk
}

func marshalPayloads(t *testing.T, payloads []*commonpb.Payload) []byte {
	t.Helper()
	bs, err := protojson.Marshal(&commonpb.Payloads{Payloads: payloads})
	require.NoError(t, err)
	return bs
}

func unmarshalPayloads(t *testing.T, body []byte) []*commonpb.Payload {
	t.Helper()
	var out commonpb.Payloads
	// Server writes via encoding/json over the proto struct.
	require.NoError(t, json.Unmarshal(body, &out))
	return out.GetPayloads()
}

// response captures the bits of an HTTP response we want to assert on while
// ensuring the response body is always closed.
type response struct {
	StatusCode int
	Body       []byte
}

func post(t *testing.T, srv *httptest.Server, path, ns string, body []byte) response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(body))
	require.NoError(t, err)
	if ns != "" {
		req.Header.Set(stdtemporalcodec.HeaderNamespace, ns)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	bs, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return response{StatusCode: resp.StatusCode, Body: bs}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, []string{"tenant-a"})

	input := []*commonpb.Payload{
		{Metadata: map[string][]byte{"encoding": []byte("json/plain")}, Data: []byte(`{"k":1}`)},
	}

	encResp := post(t, srv, "/encode", "tenant-a", marshalPayloads(t, input))
	require.Equal(t, http.StatusOK, encResp.StatusCode)
	encoded := unmarshalPayloads(t, encResp.Body)
	require.Len(t, encoded, 1)
	assert.Equal(t, []byte("binary/encrypted"), encoded[0].GetMetadata()["encoding"])
	assert.Equal(t, []byte("tenant-a"), encoded[0].GetMetadata()[stdtemporalcodec.MetadataContextNamespace])

	decResp := post(t, srv, "/decode", "tenant-a", marshalPayloads(t, encoded))
	require.Equal(t, http.StatusOK, decResp.StatusCode)
	decoded := unmarshalPayloads(t, decResp.Body)
	require.Len(t, decoded, 1)
	assert.Equal(t, input[0].GetData(), decoded[0].GetData())
	assert.Equal(t, input[0].GetMetadata()["encoding"], decoded[0].GetMetadata()["encoding"])
}

func TestMethodNotAllowed(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, []string{"tenant-a"})
	resp, err := http.Get(srv.URL + "/encode")
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestUnknownPath(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, []string{"tenant-a"})
	resp := post(t, srv, "/bogus", "tenant-a", []byte("{}"))
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestMissingNamespaceHeader(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, []string{"tenant-a"})
	resp := post(t, srv, "/encode", "", marshalPayloads(t, nil))
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestNamespaceNotAllowed(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, []string{"tenant-a"})
	resp := post(t, srv, "/encode", "tenant-b", marshalPayloads(t, nil))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Contains(t, string(resp.Body), "tenant-b")
}

func TestEmptyAllowlistRejectsAll(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, nil)
	resp := post(t, srv, "/encode", "tenant-a", marshalPayloads(t, nil))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestMalformedBody(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, []string{"tenant-a"})
	resp := post(t, srv, "/encode", "tenant-a", []byte("not-json"))
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestRouteSuffixMatching(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, []string{"tenant-a"})

	// Mounted under arbitrary prefix should still route based on suffix.
	resp := post(t, srv, "/some/prefix/encode", "tenant-a", marshalPayloads(t, nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandlerRequiresCodec(t *testing.T) {
	t.Parallel()
	_, err := stdtemporalcodec.Handler(stdtemporalcodec.HandlerOptions{AllowedNamespaces: []string{"x"}})
	require.Error(t, err)
}

func TestStripCloudAccountSuffix(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "tenant-a", stdtemporalcodec.StripCloudAccountSuffix("tenant-a.abc123"))
	assert.Equal(t, "tenant-a", stdtemporalcodec.StripCloudAccountSuffix("tenant-a"))
	assert.Equal(t, "a.b", stdtemporalcodec.StripCloudAccountSuffix("a.b.c"))
	assert.Empty(t, stdtemporalcodec.StripCloudAccountSuffix(""))
}

func TestNamespaceNormalizerStripsSuffix(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServerWith(t, stdtemporalcodec.HandlerOptions{
		AllowedNamespaces:  []string{"tenant-a"},
		NormalizeNamespace: stdtemporalcodec.StripCloudAccountSuffix,
	})

	input := []*commonpb.Payload{
		{Metadata: map[string][]byte{"encoding": []byte("json/plain")}, Data: []byte(`{"k":1}`)},
	}

	// Cloud UI form: <name>.<accountID> — must be accepted and normalized
	// to the bare allowlist entry.
	encResp := post(t, srv, "/encode", "tenant-a.acct-123", marshalPayloads(t, input))
	require.Equal(t, http.StatusOK, encResp.StatusCode)
	encoded := unmarshalPayloads(t, encResp.Body)
	assert.Equal(t, []byte("tenant-a"), encoded[0].GetMetadata()[stdtemporalcodec.MetadataContextNamespace])

	// Decoding with the normalized form also works (callers may pass
	// either; the server normalizes both).
	decResp := post(t, srv, "/decode", "tenant-a.acct-123", marshalPayloads(t, encoded))
	require.Equal(t, http.StatusOK, decResp.StatusCode)
}

func TestNamespaceNormalizerStillEnforcesAllowlist(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServerWith(t, stdtemporalcodec.HandlerOptions{
		AllowedNamespaces:  []string{"tenant-a"},
		NormalizeNamespace: stdtemporalcodec.StripCloudAccountSuffix,
	})

	resp := post(t, srv, "/encode", "tenant-b.acct-123", marshalPayloads(t, nil))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestLoggerEmitsAuthDenial(t *testing.T) {
	t.Parallel()
	core, recorded := observer.New(zap.WarnLevel)
	srv, _, _ := newServerWith(t, stdtemporalcodec.HandlerOptions{
		AllowedNamespaces: []string{"tenant-a"},
		Logger:            zap.New(core),
	})

	resp := post(t, srv, "/encode", "tenant-x", marshalPayloads(t, nil))
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	entries := recorded.FilterMessage("namespace not allowed").All()
	require.Len(t, entries, 1)
	assert.Equal(t, zap.WarnLevel, entries[0].Level)
}

func TestDecodeNamespaceMismatchFails(t *testing.T) {
	t.Parallel()
	srv, _, _ := newServer(t, []string{"tenant-a", "tenant-b"})

	input := []*commonpb.Payload{
		{Metadata: map[string][]byte{"encoding": []byte("json/plain")}, Data: []byte(`"x"`)},
	}
	encResp := post(t, srv, "/encode", "tenant-a", marshalPayloads(t, input))
	require.Equal(t, http.StatusOK, encResp.StatusCode)
	encoded := unmarshalPayloads(t, encResp.Body)

	// Try decoding the tenant-a ciphertext from tenant-b.
	decResp := post(t, srv, "/decode", "tenant-b", marshalPayloads(t, encoded))
	require.Equal(t, http.StatusBadRequest, decResp.StatusCode)
	body := strings.ToLower(string(decResp.Body))
	assert.True(t,
		strings.Contains(body, "context") || strings.Contains(body, "decrypt") || strings.Contains(body, "hmac"),
		"expected decryption-related error, got %q", body)
}
