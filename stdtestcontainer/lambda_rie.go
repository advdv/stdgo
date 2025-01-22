// Package stdtestcontainer provides re-usable code for using testcontainer-go.
package stdtestcontainer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/carlmjohnson/requests"
	"github.com/docker/docker/api/types"
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

// testLogConsumer will log container logs to the test logs.
type testLogConsumer struct{ testing.TB }

func (lc *testLogConsumer) Accept(l testcontainers.Log) {
	lc.Logf("%s: %s", l.LogType, l.Content)
}

// SetupLambdaRIEContainer will use testcontainers-go to setup a container for e2e testing of a lambda handler.
//
//nolint:revive
func SetupLambdaRIEContainer(
	t *testing.T,
	ctx context.Context,
	dockerFilePath string, // e.g: filepath.Join("lambda", "describedocument", "Dockerfile")
	buildTarget string, // e.g: describedocument
	entrypoint []string, // e.g: []string{"/describedocument"}
	platform string, // e.g: linux/amd64
	env map[string]string, // e.g: map[string]string{ "AWS_REGION":  "eu-central-1", "AWS_PROFILE": "cl-ats" }
	hostRieFilePath string, // default: filepath.Abs(filepath.Join(hdir, ".aws-lambda-rie", "aws-lambda-rie-amd64"))
	hostAWSCredentialsFilePath string, // default: filepath.Abs( filepath.Join(hdir, ".aws", "credentials")))
) *requests.Builder {
	t.Helper()

	wdir, err := os.Getwd()
	require.NoError(t, err)
	hdir, err := os.UserHomeDir()
	require.NoError(t, err)

	logConsumer := &testLogConsumer{}

	if hostRieFilePath == "" {
		hostRieFilePath, err = filepath.Abs(filepath.Join(hdir, ".aws-lambda-rie", "aws-lambda-rie-amd64"))
		require.NoError(t, err)
	}

	if hostAWSCredentialsFilePath == "" {
		hostAWSCredentialsFilePath, err = filepath.Abs(filepath.Join(hdir, ".aws", "credentials"))
		require.NoError(t, err)
	}

	container, err := CreateGenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:        wdir,
				Dockerfile:     dockerFilePath,
				BuildLogWriter: os.Stderr,
				BuildOptionsModifier: func(ibo *types.ImageBuildOptions) {
					ibo.Platform = platform
					ibo.Target = buildTarget
				},
			},
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
