package stdtestcontainer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

// Server is a server that is shared by every test, and every test package, of
// a `go test` run; see the package documentation for when to share one.
type Server struct {
	// Endpoint is where the tests reach the server, through a port that its
	// container publishes on the host.
	Endpoint string
	// NetworkEndpoint is where containers on the [Network] reach the server,
	// by its container's name, such as services that the code under test
	// hands requests for the server to.
	NetworkEndpoint string
}

// serverStartTimeout bounds pulling a server's image and waiting for it to be
// ready.
const serverStartTimeout = 2 * time.Minute

// sharedServer is a server, started at most once per process.
type sharedServer struct {
	once   sync.Once
	server Server
	err    error
}

var (
	sharedServersMu sync.Mutex
	sharedServers   = map[string]*sharedServer{}
)

// startFunc runs the container of a shared server, named name, with network,
// which attaches it to the [Network] under that name, and reuse, which makes
// processes of the same run share it.
type startFunc func(
	ctx context.Context, name string, network, reuse testcontainers.CustomizeRequestOption,
) (Server, error)

// shared returns the server of kind, such as "s3", for image, which start runs
// the first time any test of this run asks for it. Test packages run in
// separate processes that share the session ID of the `go test` run, so they
// all attach to one container; containers of earlier runs are never reused.
// Testcontainers' reaper (Ryuk) removes it after the run.
func shared(tb testing.TB, kind, image string, start startFunc) Server {
	tb.Helper()
	require.NotEmpty(tb, image, "%s image is required", kind)

	name := serverName(kind, image, testcontainers.SessionID())

	sharedServersMu.Lock()
	srv, ok := sharedServers[name]
	if !ok {
		srv = &sharedServer{}
		sharedServers[name] = srv
	}
	sharedServersMu.Unlock()

	network := WithNetwork(tb, name)

	srv.once.Do(func() {
		// The server outlives the test that happens to start it, so that test
		// ending must not cancel the start.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(tb.Context()), serverStartTimeout)
		defer cancel()

		srv.server, srv.err = start(ctx, name, network, testcontainers.WithReuseByName(name))
	})
	require.NoError(tb, srv.err, "start shared %s container (is Docker running?)", kind)

	return srv.server
}

// serverName names the container of the server of kind for image in the given
// test session. Containers on the network reach it under this name.
func serverName(kind, image, sessionID string) string {
	sum := sha256.Sum256([]byte(image))

	return "stdgo-" + kind + "-" + hex.EncodeToString(sum[:4]) + "-" + sessionID[:min(12, len(sessionID))]
}
