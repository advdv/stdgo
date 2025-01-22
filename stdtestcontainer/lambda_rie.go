// Package stdtestcontainer provides re-usable code for using testcontainer-go.
package stdtestcontainer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/carlmjohnson/requests"
	"github.com/docker/go-connections/nat"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	// CreateGenericContainer sets the function used for creating the generic container, can be replaced for testing.
	CreateGenericContainer = testcontainers.GenericContainer
	// CleanupContainer sets the function used for cleaning up the container, can be replaced for testing.
	CleanupContainer = testcontainers.CleanupContainer
)

// testLogger will log container logs to the test logs.
type testLogger struct{ testing.TB }

func (lc *testLogger) Accept(l testcontainers.Log) {
	lc.Logf("%s: %s", l.LogType, l.Content)
}

func (lc *testLogger) Write(p []byte) (n int, err error) {
	lc.Logf("%s", string(p))

	return
}

// SetupLambdaRIEContainer will use testcontainers-go to setup a container for e2e testing of a lambda handler.
//
//nolint:revive
func SetupLambdaRIEContainer(
	t *testing.T,
	ctx context.Context,
	imageName string,
	entrypoint []string, // e.g: []string{"/describedocument"}
	platform string, // e.g: linux/amd64
	env map[string]string, // e.g: map[string]string{ "AWS_REGION":  "eu-central-1", "AWS_PROFILE": "cl-ats" }
	hostRieFilePath string, // default: filepath.Abs(filepath.Join(hdir, ".aws-lambda-rie", "aws-lambda-rie-amd64"))
	hostAWSCredentialsFilePath string, // default: filepath.Abs( filepath.Join(hdir, ".aws", "credentials")))
) *requests.Builder {
	t.Helper()

	hdir, err := os.UserHomeDir()
	require.NoError(t, err)

	logConsumer := &testLogger{t}

	if hostRieFilePath == "" {
		binaryName := "aws-lambda-rie-arm64"
		if platform == "linux/amd64" {
			binaryName = "aws-lambda-rie-amd64"
		}

		hostRieFilePath, err = filepath.Abs(filepath.Join(hdir, ".aws-lambda-rie", binaryName))
		require.NoError(t, err)
	}

	if hostAWSCredentialsFilePath == "" {
		hostAWSCredentialsFilePath, err = filepath.Abs(filepath.Join(hdir, ".aws", "credentials"))
		require.NoError(t, err)
	}

	container, err := CreateGenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:         imageName,
			ImagePlatform: platform,
			ExposedPorts:  []string{"8080/tcp"},
			Files: []testcontainers.ContainerFile{
				{
					HostFilePath:      hostRieFilePath,
					ContainerFilePath: "/aws-lambda-rie",
					FileMode:          0o7777,
				},
				{
					HostFilePath:      hostAWSCredentialsFilePath,
					ContainerFilePath: "/root/.aws/credentials",
					FileMode:          0o7777,
				},
			},
			Env:        env,
			Entrypoint: append([]string{"/aws-lambda-rie"}, entrypoint...),
			WaitingFor: wait.ForAll(
				wait.ForLog("[INFO] (rapid) exec"),
				wait.ForListeningPort(nat.Port("8080/tcp")).SkipInternalCheck(),
			),
			LogConsumerCfg: &testcontainers.LogConsumerConfig{
				Opts:      []testcontainers.LogProductionOption{testcontainers.WithLogProductionTimeout(10 * time.Second)},
				Consumers: []testcontainers.LogConsumer{logConsumer},
			},
		},
		Started: true,
	})

	CleanupContainer(t, container)
	require.NoError(t, err)

	ep, err := container.Endpoint(ctx, "")
	require.NoError(t, err)

	return requests.URL("http://" + ep + "/2015-03-31/functions/function/invocations").Post()
}
