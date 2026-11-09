package stds3fx_test

import (
	"io"
	"maps"
	"strings"
	"testing"

	"github.com/advdv/stdgo/fx/stds3fx"
	"github.com/advdv/stdgo/fx/stds3fx/stds3testfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

// s3Image is the server the tests run against.
const s3Image = "docker.io/pgsty/silo:RELEASE.2026-10-26T00-00-00Z@sha256:7bb2aff6b89efe1cb1303c10fc21d579447434ed06649ac9ec05a5e2dbf30ad7"

// testApp wires the module for an fx application on the test server, with a
// "main" bucket, over the given environment.
func testApp(tb testing.TB, env map[string]string) fx.Option {
	tb.Helper()

	out := map[string]string{"STDS3_BUCKETS": "main:stds3-test-main"}
	maps.Copy(out, env)

	return fx.Options(
		stdenvcfg.ProvideExplicitEnvironment(out),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stds3testfx.Provide(tb, s3Image),
	)
}

// setup boots the module on the test server, with its own random buckets unless
// env configures otherwise, and returns the started client.
func setup(tb testing.TB, env map[string]string) *stds3fx.Client {
	tb.Helper()

	var client *stds3fx.Client

	app := fxtest.New(tb, testApp(tb, env), fx.Populate(&client))
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	return client
}

// mainBucket returns the resolved name of the main bucket.
func mainBucket(tb testing.TB, client *stds3fx.Client) string {
	tb.Helper()

	bucket, ok := client.Bucket("main")
	require.True(tb, ok)

	return bucket
}

func TestBucketResolvesConfiguredAliases(t *testing.T) {
	t.Parallel()

	client := setup(t, nil)

	bucket := mainBucket(t, client)
	assert.True(t, strings.HasPrefix(bucket, "stds3-test-main-"), bucket)
	assert.Len(t, bucket, len("stds3-test-main-")+12, "random suffix is 6 bytes hex")

	_, ok := client.Bucket("nope")
	assert.False(t, ok)
}

// The random bucket exists on start and takes objects through the S3 client,
// so a bucket name from Bucket and the client from S3 are all a caller needs.
func TestPutAndGetObjectRoundTrip(t *testing.T) {
	t.Parallel()

	client := setup(t, nil)
	ctx := t.Context()
	bucket := mainBucket(t, client)

	_, err := client.S3().PutObject(ctx, bucket, "dir/hello.txt", strings.NewReader("hello, blob"), -1,
		minio.PutObjectOptions{ContentType: "text/plain"})
	require.NoError(t, err)

	object, err := client.S3().GetObject(ctx, bucket, "dir/hello.txt", minio.GetObjectOptions{})
	require.NoError(t, err)

	body, err := io.ReadAll(object)
	require.NoError(t, err)

	info, err := object.Stat()
	require.NoError(t, err)
	require.NoError(t, object.Close())

	assert.Equal(t, "hello, blob", string(body))
	assert.Equal(t, "text/plain", info.ContentType)
}

// Two applications with the same configuration get different buckets, which is
// what lets the tests of a run share one server.
func TestRandomBucketsAreIsolatedPerApplication(t *testing.T) {
	t.Parallel()

	first := mainBucket(t, setup(t, nil))
	second := mainBucket(t, setup(t, nil))
	assert.NotEqual(t, first, second)
}

// Stopping the application empties and deletes its random buckets, including
// every version of every object in a versioned bucket.
func TestAutoRemoveDeletesBucketsWithObjects(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	var client *stds3fx.Client

	app := fxtest.New(t, testApp(t, nil), fx.Populate(&client))
	app.RequireStart()

	bucket := mainBucket(t, client)
	require.NoError(t, client.S3().EnableVersioning(ctx, bucket))

	for _, body := range []string{"first", "second"} {
		_, err := client.S3().PutObject(ctx, bucket, "dir/file.txt", strings.NewReader(body), -1, minio.PutObjectOptions{})
		require.NoError(t, err)
	}

	require.NoError(t, client.S3().RemoveObject(ctx, bucket, "dir/file.txt", minio.RemoveObjectOptions{}))

	app.RequireStop()

	observer := setup(t, map[string]string{
		"STDS3_BUCKETS":                       "main:" + bucket,
		"STDS3_CREATE_AND_USE_RANDOM_BUCKETS": "false",
	})

	exists, err := observer.S3().BucketExists(ctx, bucket)
	require.NoError(t, err)
	assert.False(t, exists)
}

// Ping heads every bucket: it passes while all exist and names the alias
// of a bucket that is gone.
func TestPingFailsWhenBucketIsGone(t *testing.T) {
	t.Parallel()

	client := setup(t, map[string]string{"STDS3_BUCKETS": "main:stds3-test-main,archive:stds3-test-archive"})
	ctx := t.Context()

	require.NoError(t, client.Ping(ctx))

	archive, ok := client.Bucket("archive")
	require.True(t, ok)
	require.NoError(t, client.S3().RemoveBucket(ctx, archive))

	err := client.Ping(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `head bucket "archive"`)
}

// Without random buckets the module uses the configured names as they are and
// must not create them: a missing bucket surfaces from Ping, not from start.
func TestFixedBucketsAreNotCreated(t *testing.T) {
	t.Parallel()

	client := setup(t, map[string]string{
		"STDS3_BUCKETS":                       "main:stds3-test-does-not-exist",
		"STDS3_CREATE_AND_USE_RANDOM_BUCKETS": "false",
		"STDS3_AUTO_REMOVE_BUCKETS":           "false",
	})

	assert.Equal(t, "stds3-test-does-not-exist", mainBucket(t, client))
	require.Error(t, client.Ping(t.Context()))
}

func TestConfigurationErrors(t *testing.T) {
	t.Parallel()

	for name, overrides := range map[string]map[string]string{
		"missing buckets":               {"STDS3_BUCKETS": ""},
		"access key without secret key": {"STDS3_SECRET_KEY": ""},
		"endpoint without scheme":       {"STDS3_ENDPOINT": "localhost:9000"},
		"endpoint with path":            {"STDS3_ENDPOINT": "http://localhost:9000/bucket"},
		"endpoint with bad scheme":      {"STDS3_ENDPOINT": "ftp://localhost:9000"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := map[string]string{
				"STDS3_BUCKETS":    "main:stds3-test-main",
				"STDS3_ENDPOINT":   "http://localhost:9000",
				"STDS3_ACCESS_KEY": "key",
				"STDS3_SECRET_KEY": "secret",
			}
			maps.Copy(env, overrides)

			var client *stds3fx.Client

			// Building the app runs the constructor without starting it, so
			// no server is needed.
			err := fx.New(
				stdenvcfg.ProvideExplicitEnvironment(env),
				stdzapfx.Fx(),
				stdzapfx.TestProvide(t),
				stds3fx.Provide(),
				fx.Populate(&client),
			).Err()
			require.Error(t, err)
		})
	}
}
