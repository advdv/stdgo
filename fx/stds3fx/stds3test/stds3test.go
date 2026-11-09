// Package stds3test sends, in tests, the requests that stds3fx pre-signs, the
// way clients send them.
package stds3test

import (
	"bytes"
	"maps"
	"mime/multipart"
	"net/http"
	"slices"
	"testing"

	"github.com/advdv/stdgo/fx/stds3fx"
	"github.com/carlmjohnson/requests"
	"github.com/stretchr/testify/require"
)

// Send sends req with body, or without one when body is nil, as a client
// would: with the request's headers and nothing else, and for a POST, as a
// multipart/form-data body of the request's fields followed by body as the
// "file" field. It returns the response's headers and body, and an error for a
// response outside 2xx, which requests.HasStatusErr matches; object storage
// answers 4xx when a request breaks its signature or policy.
func Send(tb testing.TB, req *stds3fx.PresignedRequest, body []byte) (http.Header, []byte, error) {
	tb.Helper()

	var (
		headers = http.Header{}
		out     bytes.Buffer
	)

	// CopyHeaders adds a validator, which would replace the default status
	// check, so add that check first.
	builder := requests.URL(req.URL).
		Method(req.Method).
		AddValidator(requests.DefaultValidator).
		CopyHeaders(headers).
		ToBytesBuffer(&out)

	switch {
	case req.Method == http.MethodPost:
		form, contentType := postForm(tb, req.Fields, body)
		builder = builder.BodyBytes(form).ContentType(contentType)
	case body != nil:
		builder = builder.BodyBytes(body)
	}

	for name, values := range req.Headers {
		builder = builder.Header(name, values...)
	}

	err := builder.Fetch(tb.Context())

	return headers, out.Bytes(), err
}

// postForm encodes the multipart/form-data body of a POST upload: the fields,
// then the object as the "file" field, which object storage requires last.
func postForm(tb testing.TB, fields map[string]string, object []byte) ([]byte, string) {
	tb.Helper()

	var form bytes.Buffer

	writer := multipart.NewWriter(&form)

	for _, name := range slices.Sorted(maps.Keys(fields)) {
		require.NoError(tb, writer.WriteField(name, fields[name]))
	}

	file, err := writer.CreateFormFile("file", "object")
	require.NoError(tb, err)

	_, err = file.Write(object)
	require.NoError(tb, err)
	require.NoError(tb, writer.Close())

	return form.Bytes(), writer.FormDataContentType()
}
