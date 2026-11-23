package stdtestcontainer_test

import (
	"testing"

	"github.com/advdv/stdgo/stdtestcontainer"
	"github.com/carlmjohnson/requests"
	"github.com/stretchr/testify/require"
)

// bifrostImage is the Bifrost gateway the tests run against.
const bifrostImage = "docker.io/maximhq/bifrost:v2.6.0@sha256:5968d007ba4f74ee01bc4ceb071decb9927a0b7bb43dbb9072208f75e2d522e3"

func TestBifrost(t *testing.T) {
	t.Parallel()

	env := map[string]string{"ECHO_KEY": "a"}

	server := stdtestcontainer.Bifrost(t, bifrostImage, "testdata/bifrost.json", env)
	require.Equal(t, server, stdtestcontainer.Bifrost(t, bifrostImage, "testdata/bifrost.json", env),
		"later calls share the gateway")

	other := stdtestcontainer.Bifrost(t, bifrostImage, "testdata/bifrost.json", map[string]string{"ECHO_KEY": "b"})
	require.NotEqual(t, server, other, "another environment gets another gateway")

	for _, srv := range []stdtestcontainer.Server{server, other} {
		require.NoError(t, requests.URL(srv.Endpoint).Path("/health").Fetch(t.Context()))
	}

	// Bifrost's image has wget, not curl.
	requireRunsOnNetwork(t, bifrostImage, "wget", "-qO-", server.NetworkEndpoint+"/health")
}
