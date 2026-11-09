package stds3fx

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/minio/minio-go/v7"
)

// PresignedRequest is a request that a client sends to object storage
// directly, without credentials of its own: the signature in URL, or in Fields
// for a POST, grants it, until ExpiresAt. It is signed for
// STDS3_PUBLIC_ENDPOINT when that is set. Clients send it with stds3test.Send
// in tests.
type PresignedRequest struct {
	Method string
	URL    string
	// Headers must be sent as they are; the signature covers them.
	Headers http.Header
	// Fields are the form fields of a POST, which a client sends as they are
	// in a multipart/form-data body, before the object in a "file" field. They
	// are empty for other methods.
	Fields    map[string]string
	ExpiresAt time.Time
}

// PostConditions are what object storage requires of an object uploaded with
// a pre-signed POST, as conditions of its policy.
type PostConditions struct {
	// ContentType, when set, is the only content type accepted.
	ContentType string
	// MinSize and MaxSize bound the object's size in bytes when MaxSize is
	// positive; set both to one size to require exactly it.
	MinSize, MaxSize int64
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

	presigned, err := c.signer.PresignHeader(ctx, http.MethodPut, bucket, key, ttl, nil, signed)
	if err != nil {
		return nil, errors.Wrap(err, "presign put")
	}

	signed.Del("Content-Length")

	return &PresignedRequest{
		Method:    http.MethodPut,
		URL:       presigned.String(),
		Headers:   signed,
		Fields:    map[string]string{},
		ExpiresAt: expiresAt,
	}, nil
}

// PresignPost signs a POST of an object to key in bucket, valid for ttl, as an
// HTML form or any client can send it. Its policy carries conditions, so object
// storage refuses an upload that breaks them. Unlike a PUT, a POST lets the
// client send the object with a size anywhere in a range.
func (c *Client) PresignPost(
	ctx context.Context, bucket, key string, ttl time.Duration, conditions PostConditions,
) (*PresignedRequest, error) {
	expiresAt := time.Now().Add(ttl)

	policy := minio.NewPostPolicy()
	if err := errors.Join(
		policy.SetBucket(bucket),
		policy.SetKey(key),
		policy.SetExpires(expiresAt.UTC()),
	); err != nil {
		return nil, errors.Wrap(err, "post policy")
	}

	if conditions.ContentType != "" {
		if err := policy.SetContentType(conditions.ContentType); err != nil {
			return nil, errors.Wrap(err, "post policy content type")
		}
	}

	if conditions.MaxSize > 0 {
		if err := policy.SetContentLengthRange(conditions.MinSize, conditions.MaxSize); err != nil {
			return nil, errors.Wrap(err, "post policy size")
		}
	}

	presigned, fields, err := c.signer.PresignedPostPolicy(ctx, policy)
	if err != nil {
		return nil, errors.Wrap(err, "presign post")
	}

	return &PresignedRequest{
		Method:    http.MethodPost,
		URL:       presigned.String(),
		Headers:   http.Header{},
		Fields:    fields,
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

	presigned, err := c.signer.PresignedGetObject(ctx, bucket, key, ttl, params)
	if err != nil {
		return nil, errors.Wrap(err, "presign get")
	}

	return &PresignedRequest{
		Method:    http.MethodGet,
		URL:       presigned.String(),
		Headers:   http.Header{},
		Fields:    map[string]string{},
		ExpiresAt: expiresAt,
	}, nil
}
