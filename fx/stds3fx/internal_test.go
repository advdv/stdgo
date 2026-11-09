package stds3fx

import (
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
)

// Callers wrap the S3 error response, which minio.ToErrorResponse does not
// look behind; the codes must still match, and other errors must not.
func TestIsNoSuchBucketMatchesWrappedErrors(t *testing.T) {
	t.Parallel()

	assert.True(t, isNoSuchBucket(minio.ErrorResponse{Code: minio.NoSuchBucket}))
	assert.True(t, isNoSuchBucket(errors.Wrap(minio.ErrorResponse{Code: minio.NoSuchBucket}, "list object versions")))

	assert.False(t, isNoSuchBucket(minio.ErrorResponse{Code: minio.AccessDenied}))
	assert.False(t, isNoSuchBucket(errors.New("connection refused")))
	assert.False(t, isNoSuchBucket(nil))
}

func TestIsBucketAlreadyOwned(t *testing.T) {
	t.Parallel()

	assert.True(t, isBucketAlreadyOwned(minio.ErrorResponse{Code: minio.BucketAlreadyOwnedByYou}))
	assert.True(t, isBucketAlreadyOwned(minio.ErrorResponse{Code: minio.BucketAlreadyExists}))
	assert.False(t, isBucketAlreadyOwned(minio.ErrorResponse{Code: minio.NoSuchBucket}))
}
