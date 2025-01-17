package stdpgtest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/advdv/stdgo/stdpgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecute(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	resp, err := stdpgtest.Execute(ctx, nil, "", "echo", "hello world")
	require.NoError(t, err)
	assert.Equal(t, "hello world", resp)
}

func TestExecuteQuoting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	resp, err := stdpgtest.Execute(ctx, nil, "", "bash", "-c", "echo 'hello world'")
	require.NoError(t, err)
	assert.Equal(t, "hello world", resp)
}

func TestExecuteStdin(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	resp, err := stdpgtest.Execute(ctx, strings.NewReader("hello world"), "", "cat")
	require.NoError(t, err)
	assert.Equal(t, "hello world", resp)
}

func TestExecuteStripsOneTrailingNewline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	resp, err := stdpgtest.Execute(ctx, strings.NewReader("hello world\t\n\n"), "", "cat")
	require.NoError(t, err)
	assert.Equal(t, string("hello world\t\n"), resp)
}

func TestExecuteDir(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	resp, err := stdpgtest.Execute(ctx, nil, "testdata", "pwd")
	require.NoError(t, err)
	assert.Contains(t, resp, "stdpgtest/testdata")
}
