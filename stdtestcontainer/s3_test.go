package stdtestcontainer_test

import (
	"testing"

	"github.com/advdv/stdgo/stdtestcontainer"
	"github.com/carlmjohnson/requests"
	"github.com/stretchr/testify/require"
)

// s3Image is the S3-compatible server the tests run against.
const s3Image = "docker.io/pgsty/silo:RELEASE.2026-10-26T00-00-00Z@sha256:7bb2aff6b89efe1cb1303c10fc21d579447434ed06649ac9ec05a5e2dbf30ad7"

func TestS3(t *testing.T) {
	t.Parallel()

	endpoint := stdtestcontainer.S3(t, s3Image)
	require.Equal(t, endpoint, stdtestcontainer.S3(t, s3Image), "later calls share the server")

	require.NoError(t, requests.URL(endpoint).Path("/minio/health/ready").Fetch(t.Context()))
}
