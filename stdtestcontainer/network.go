package stdtestcontainer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
)

// networkTimeout bounds creating the network.
const networkTimeout = time.Minute

var (
	networkOnce sync.Once
	networkName string
	errNetwork  error
)

// Network returns the name of a Docker network that is shared by every test,
// and every test package, of the same `go test` run. The servers started here,
// such as [S3], join it, so containers that tests start on it reach them by
// name instead of through the host, which a container cannot reach on every
// platform: attach them with [WithNetwork]. The first call creates the network;
// later calls, including those from other test packages (which Go runs in
// other processes), reuse it. Testcontainers' reaper (Ryuk) removes it after
// the run. It requires a running Docker daemon.
func Network(tb testing.TB) string {
	tb.Helper()

	networkOnce.Do(func() { networkName, errNetwork = ensureNetwork(tb.Context()) })
	require.NoError(tb, errNetwork, "create shared network (is Docker running?)")

	return networkName
}

// WithNetwork attaches a container to the [Network] under aliases, the host
// names other containers on it reach it at.
func WithNetwork(tb testing.TB, aliases ...string) testcontainers.CustomizeRequestOption {
	tb.Helper()

	return network.WithNetworkName(aliases, Network(tb))
}

// ensureNetwork creates the network of this run, or finds the one another test
// package of this run created.
func ensureNetwork(ctx context.Context) (string, error) {
	// The network outlives the test that happens to create it.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), networkTimeout)
	defer cancel()

	name := "stdgo-" + testcontainers.SessionID()[:min(12, len(testcontainers.SessionID()))]

	docker, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		return "", fmt.Errorf("docker client: %w", err)
	}
	defer docker.Close()

	exists := func() error {
		_, err := docker.NetworkInspect(ctx, name, client.NetworkInspectOptions{})

		return err //nolint:wrapcheck // callers wrap it.
	}

	if err := exists(); err == nil {
		return name, nil
	} else if !errdefs.IsNotFound(err) {
		return "", fmt.Errorf("inspect network: %w", err)
	}

	// The generic request labels the network for the reaper of this session.
	//nolint:staticcheck // network.New picks a random name, which other processes cannot find.
	_, err = testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		Name:   name,
		Driver: "bridge",
		Labels: testcontainers.GenericLabels(),
	})
	if err == nil {
		return name, nil
	}

	// Another test package may have created it in the meantime.
	if exists() == nil {
		return name, nil
	}

	return "", errors.Join(fmt.Errorf("create network %q", name), err)
}
