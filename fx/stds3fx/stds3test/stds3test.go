// Package stds3test sends, in tests, the requests that stds3fx pre-signs, the
// way clients send them.
package stds3test

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/advdv/stdgo/fx/stds3fx"
	"github.com/carlmjohnson/requests"
)

// Send sends req with body, or without one when body is nil, as a client
// would: with the request's headers and nothing else. It returns the
// response's headers and body, and an error for a response outside 2xx, which
// requests.HasStatusErr matches; object storage answers 403 when a request
// breaks its signature.
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

	if body != nil {
		builder = builder.BodyBytes(body)
	}

	for name, values := range req.Headers {
		builder = builder.Header(name, values...)
	}

	err := builder.Fetch(tb.Context())

	return headers, out.Bytes(), err
}
