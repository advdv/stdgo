package stds3fx

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/cockroachdb/errors"
)

// PresignedRequest is a request that a client sends to object storage
// directly, without credentials of its own: the signature in URL grants it,
// until ExpiresAt. Clients send it with stds3test.Send in tests.
type PresignedRequest struct {
	Method string
	URL    string
	// Headers must be sent as they are; the signature covers them.
	Headers   http.Header
	ExpiresAt time.Time
}

// PresignPut signs a PUT of an object to key in bucket, valid for ttl. The
// signature covers headers, such as Content-Type and Content-Length, so object
// storage refuses an upload that sends other values. The returned request
// carries headers without Content-Length, which clients set from the body
// themselves; browsers refuse to set it.
func (c *Client) PresignPut(
	ctx context.Context, bucket, key string, ttl time.Duration, headers http.Header,
) (*PresignedRequest, error) {
	signed := headers.Clone()
	if signed == nil {
		signed = http.Header{}
	}

	expiresAt := time.Now().Add(ttl)

	presigned, err := c.s3.PresignHeader(ctx, http.MethodPut, bucket, key, ttl, nil, signed)
	if err != nil {
		return nil, errors.Wrap(err, "presign put")
	}

	signed.Del("Content-Length")

	return &PresignedRequest{
		Method:    http.MethodPut,
		URL:       presigned.String(),
		Headers:   signed,
		ExpiresAt: expiresAt,
	}, nil
}

// PresignGet signs a GET of the object at key in bucket, valid for ttl. Params
// may override headers of the response, such as response-content-disposition
// to have browsers save the object under a filename.
func (c *Client) PresignGet(
	ctx context.Context, bucket, key string, ttl time.Duration, params url.Values,
) (*PresignedRequest, error) {
	expiresAt := time.Now().Add(ttl)

	presigned, err := c.s3.PresignedGetObject(ctx, bucket, key, ttl, params)
	if err != nil {
		return nil, errors.Wrap(err, "presign get")
	}

	return &PresignedRequest{
		Method:    http.MethodGet,
		URL:       presigned.String(),
		Headers:   http.Header{},
		ExpiresAt: expiresAt,
	}, nil
}
