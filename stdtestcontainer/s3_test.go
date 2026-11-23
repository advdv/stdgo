package stdtestcontainer_test

import (
	"testing"

	"github.com/advdv/stdgo/stdtestcontainer"
	"github.com/carlmjohnson/requests"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// s3Image is the S3-compatible server the tests run against.
const s3Image = "docker.io/pgsty/silo:RELEASE.2026-10-26T00-00-00Z@sha256:7bb2aff6b89efe1cb1303c10fc21d579447434ed06649ac9ec05a5e2dbf30ad7"

func TestS3(t *testing.T) {
	t.Parallel()

	server := stdtestcontainer.S3(t, s3Image)
	require.Equal(t, server, stdtestcontainer.S3(t, s3Image), "later calls share the server")

	require.NoError(t, requests.URL(server.Endpoint).Path("/minio/health/ready").Fetch(t.Context()))

	// The server's image has curl.
	requireRunsOnNetwork(t, s3Image, "curl", "-fsS", server.NetworkEndpoint+"/minio/health/ready")
}

// requireRunsOnNetwork runs entrypoint with args in a container of image on
// the shared network, and requires it to succeed.
func requireRunsOnNetwork(tb testing.TB, image, entrypoint string, args ...string) {
	tb.Helper()

	ctr, err := testcontainers.Run(tb.Context(), image,
		stdtestcontainer.WithNetwork(tb),
		testcontainers.WithEntrypoint(entrypoint),
		testcontainers.WithCmd(args...),
		testcontainers.WithWaitStrategy(wait.ForExit()),
	)
	testcontainers.CleanupContainer(tb, ctr)
	require.NoError(tb, err)

	state, err := ctr.State(tb.Context())
	require.NoError(tb, err)
	require.Equal(tb, 0, state.ExitCode, "%s %v", entrypoint, args)
}
