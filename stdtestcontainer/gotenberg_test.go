package stdtestcontainer_test

import (
	"testing"

	"github.com/advdv/stdgo/stdtestcontainer"
	"github.com/carlmjohnson/requests"
	"github.com/stretchr/testify/require"
)

// gotenbergImage is the Gotenberg server the tests run against.
const gotenbergImage = "docker.io/gotenberg/gotenberg:9.9.3@sha256:9731688d1b6290e4c04d9a47dbd5fb29f92aa0076feb6592c5c777525892cd72"

func TestGotenberg(t *testing.T) {
	t.Parallel()

	server := stdtestcontainer.Gotenberg(t, gotenbergImage)
	require.Equal(t, server, stdtestcontainer.Gotenberg(t, gotenbergImage), "later calls share the server")

	require.NoError(t, requests.URL(server.Endpoint).Path("/health").Fetch(t.Context()))

	// Gotenberg's image has curl.
	requireRunsOnNetwork(t, gotenbergImage, "curl", "-fsS", server.NetworkEndpoint+"/health")
}
