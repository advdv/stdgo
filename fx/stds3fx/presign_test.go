package stds3fx_test

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stds3fx"
	"github.com/advdv/stdgo/fx/stds3fx/stds3test"
	"github.com/advdv/stdgo/stdtestcontainer"
	"github.com/carlmjohnson/requests"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// declared returns the headers of an upload of size bytes of contentType.
func declared(contentType string, size int) http.Header {
	headers := http.Header{}
	headers.Set("Content-Type", contentType)
	headers.Set("Content-Length", strconv.Itoa(size))

	return headers
}

// A client uploads with the returned headers, which leave Content-Length to
// the client, and the object is stored as declared.
func TestPresignPut(t *testing.T) {
	t.Parallel()

	client := setup(t, nil)
	bucket := mainBucket(t, client)
	content := []byte("%PDF-1.7 tiny")
	headers := declared("application/pdf", len(content))

	req, err := client.PresignPut(t.Context(), bucket, "uploads/doc.pdf", time.Minute, headers)
	require.NoError(t, err)
	assert.Equal(t, http.MethodPut, req.Method)
	assert.Equal(t, "application/pdf", req.Headers.Get("Content-Type"))
	assert.Empty(t, req.Headers.Get("Content-Length"))
	assert.Equal(t, strconv.Itoa(len(content)), headers.Get("Content-Length"), "the caller's headers are left as they are")
	assert.WithinDuration(t, time.Now().Add(time.Minute), req.ExpiresAt, 5*time.Second)

	_, _, err = stds3test.Send(t, req, content)
	require.NoError(t, err)

	info, err := client.S3().StatObject(t.Context(), bucket, "uploads/doc.pdf", minio.StatObjectOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), info.Size)
	assert.Equal(t, "application/pdf", info.ContentType)
}

// The signature covers the given headers, so object storage refuses an upload
// that breaks one.
func TestPresignPutRejectsUndeclaredUpload(t *testing.T) {
	t.Parallel()

	client := setup(t, nil)
	bucket := mainBucket(t, client)

	for name, tc := range map[string]struct {
		body        []byte
		contentType string
	}{
		"other size":         {body: []byte("too long"), contentType: "application/pdf"},
		"other content type": {body: []byte("tiny"), contentType: "text/html"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req, err := client.PresignPut(t.Context(), bucket, "uploads/doc.pdf", time.Minute, declared("application/pdf", 4))
			require.NoError(t, err)

			req.Headers.Set("Content-Type", tc.contentType)

			_, _, err = stds3test.Send(t, req, tc.body)
			assert.True(t, requests.HasStatusErr(err, http.StatusForbidden), "error: %v", err)
		})
	}
}

// A GET returns the object, with the response headers its params override.
func TestPresignGet(t *testing.T) {
	t.Parallel()

	client := setup(t, nil)
	bucket := mainBucket(t, client)
	content := []byte("%PDF-1.7 tiny")

	put, err := client.PresignPut(t.Context(), bucket, "docs/doc.pdf", time.Minute, declared("application/pdf", len(content)))
	require.NoError(t, err)

	_, _, err = stds3test.Send(t, put, content)
	require.NoError(t, err)

	params := url.Values{}
	params.Set("response-content-disposition", "attachment; filename=report.pdf")

	req, err := client.PresignGet(t.Context(), bucket, "docs/doc.pdf", time.Minute, params)
	require.NoError(t, err)
	assert.Equal(t, http.MethodGet, req.Method)

	headers, body, err := stds3test.Send(t, req, nil)
	require.NoError(t, err)
	assert.Equal(t, content, body)
	assert.Equal(t, "attachment; filename=report.pdf", headers.Get("Content-Disposition"))
}

// Send reports a missing object as a status error.
func TestPresignGetOfMissingObjectFailsToSend(t *testing.T) {
	t.Parallel()

	client := setup(t, nil)

	req, err := client.PresignGet(t.Context(), mainBucket(t, client), "docs/missing.pdf", time.Minute, nil)
	require.NoError(t, err)

	_, _, err = stds3test.Send(t, req, nil)
	assert.True(t, requests.HasStatusErr(err, http.StatusNotFound), "error: %v", err)
}

// A client posts the fields with the object, and it is stored as declared.
func TestPresignPost(t *testing.T) {
	t.Parallel()

	client := setup(t, nil)
	bucket := mainBucket(t, client)
	content := []byte("%PDF-1.7 tiny")

	req, err := client.PresignPost(t.Context(), bucket, "uploads/doc.pdf", time.Minute, stds3fx.PostConditions{
		ContentType: "application/pdf",
		MinSize:     int64(len(content)),
		MaxSize:     int64(len(content)),
	})
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, req.Method)
	assert.Equal(t, "application/pdf", req.Fields["Content-Type"])
	assert.Equal(t, "uploads/doc.pdf", req.Fields["key"])
	assert.Empty(t, req.Headers)
	assert.WithinDuration(t, time.Now().Add(time.Minute), req.ExpiresAt, 5*time.Second)

	_, _, err = stds3test.Send(t, req, content)
	require.NoError(t, err)

	info, err := client.S3().StatObject(t.Context(), bucket, "uploads/doc.pdf", minio.StatObjectOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(len(content)), info.Size)
	assert.Equal(t, "application/pdf", info.ContentType)
}

// The policy's conditions bind the upload, so object storage refuses one that
// breaks them, and accepts any size in the range.
func TestPresignPostEnforcesConditions(t *testing.T) {
	t.Parallel()

	client := setup(t, nil)
	bucket := mainBucket(t, client)

	for name, tc := range map[string]struct {
		body        []byte
		contentType string
		wantStatus  int
	}{
		"smallest size":      {body: []byte("ab"), contentType: "text/plain", wantStatus: http.StatusNoContent},
		"largest size":       {body: []byte("abcd"), contentType: "text/plain", wantStatus: http.StatusNoContent},
		"too small":          {body: []byte("a"), contentType: "text/plain", wantStatus: http.StatusBadRequest},
		"too large":          {body: []byte("abcde"), contentType: "text/plain", wantStatus: http.StatusBadRequest},
		"other content type": {body: []byte("abc"), contentType: "text/html", wantStatus: http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			req, err := client.PresignPost(t.Context(), bucket, "uploads/"+name, time.Minute, stds3fx.PostConditions{
				ContentType: "text/plain",
				MinSize:     2,
				MaxSize:     4,
			})
			require.NoError(t, err)

			req.Fields["Content-Type"] = tc.contentType

			_, _, err = stds3test.Send(t, req, tc.body)
			if tc.wantStatus == http.StatusNoContent {
				require.NoError(t, err)

				return
			}

			assert.True(t, requests.HasStatusErr(err, tc.wantStatus), "error: %v", err)
		})
	}
}

// publicEndpoint returns another address of the test server than the one the
// client connects to, as a browser outside the service's network would use.
func publicEndpoint(tb testing.TB) (internal, public *url.URL) {
	tb.Helper()

	internal, err := url.Parse(stdtestcontainer.S3(tb, s3Image))
	require.NoError(tb, err)

	other := map[string]string{"localhost": "127.0.0.1", "127.0.0.1": "localhost"}[internal.Hostname()]
	require.NotEmpty(tb, other, "endpoint host %q has no alias", internal.Hostname())

	public = &url.URL{Scheme: internal.Scheme, Host: net.JoinHostPort(other, internal.Port())}

	return internal, public
}

// Requests are signed for the public endpoint, which clients reach, and not
// for the endpoint the service connects to: the signature covers the host.
func TestPresignSignsForPublicEndpoint(t *testing.T) {
	t.Parallel()

	internal, public := publicEndpoint(t)
	client := setup(t, map[string]string{"STDS3_PUBLIC_ENDPOINT": public.String()})
	bucket := mainBucket(t, client)
	content := []byte("tiny")

	put, err := client.PresignPut(t.Context(), bucket, "put", time.Minute, declared("text/plain", len(content)))
	require.NoError(t, err)

	post, err := client.PresignPost(t.Context(), bucket, "post", time.Minute, stds3fx.PostConditions{})
	require.NoError(t, err)

	for _, req := range []*stds3fx.PresignedRequest{put, post} {
		requireHost(t, public.Host, req)

		_, _, err = stds3test.Send(t, req, content)
		require.NoError(t, err, req.Method)
	}

	get, err := client.PresignGet(t.Context(), bucket, "put", time.Minute, nil)
	require.NoError(t, err)
	requireHost(t, public.Host, get)

	_, body, err := stds3test.Send(t, get, nil)
	require.NoError(t, err)
	assert.Equal(t, content, body)

	moved, err := url.Parse(get.URL)
	require.NoError(t, err)

	moved.Host = internal.Host
	get.URL = moved.String()

	_, _, err = stds3test.Send(t, get, nil)
	assert.True(t, requests.HasStatusErr(err, http.StatusForbidden), "error: %v", err)
}

// requireHost requires req to be addressed to host.
func requireHost(tb testing.TB, host string, req *stds3fx.PresignedRequest) {
	tb.Helper()

	parsed, err := url.Parse(req.URL)
	require.NoError(tb, err)
	require.Equal(tb, host, parsed.Host, req.Method)
}
