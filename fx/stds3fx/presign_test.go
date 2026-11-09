package stds3fx_test

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stds3fx/stds3test"
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
