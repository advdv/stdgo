package stdtestcontainer_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/advdv/stdgo/stdtestcontainer"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

type mockContainer struct{ testcontainers.Container }

func (c mockContainer) Endpoint(context.Context, string) (string, error) {
	return "localhost:3939", nil
}

func TestLambdaRIEContainer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hdir, _ := os.UserHomeDir()

	var calledCreate, calledCleanup bool
	stdtestcontainer.CreateGenericContainer = func(ctx context.Context, req testcontainers.GenericContainerRequest) (testcontainers.Container, error) {
		calledCreate = true

		require.Equal(t, "myimage", req.Image)
		require.Equal(t, "linux/amd64", req.ImagePlatform)
		require.Len(t, req.Files, 2)
		require.Equal(t, filepath.Join(hdir, ".aws-lambda-rie", "aws-lambda-rie-amd64"), req.Files[0].HostFilePath)
		require.Equal(t, filepath.Join(hdir, ".aws", "credentials"), req.Files[1].HostFilePath)
		require.Equal(t, []string{"/aws-lambda-rie", "/bar"}, req.Entrypoint)

		return mockContainer{}, nil
	}

	//nolint:thelper
	stdtestcontainer.CleanupContainer = func(tb testing.TB, ctr testcontainers.Container, options ...testcontainers.TerminateOption) {
		calledCleanup = true
	}

	req := stdtestcontainer.SetupLambdaRIEContainer(t, ctx,
		"myimage",
		[]string{"/bar"},
		"linux/amd64",
		map[string]string{"FOO": "BAR"}, "", "")

	require.True(t, calledCreate)
	require.True(t, calledCleanup)
	require.NotNil(t, req)
}
