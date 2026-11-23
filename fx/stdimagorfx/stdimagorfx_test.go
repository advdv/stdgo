package stdimagorfx_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stdhttpclientfx"
	"github.com/advdv/stdgo/fx/stdimagorfx"
	"github.com/advdv/stdgo/fx/stdimagorfx/stdimagortestfx"
	"github.com/advdv/stdgo/fx/stds3fx"
	"github.com/advdv/stdgo/fx/stds3fx/stds3testfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/cockroachdb/errors"
	"github.com/cshum/imagor/imagorpath"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

const (
	imagorImage = "docker.io/shumc/imagor:1.10.5@sha256:fa1639c4250df130126821fae0d6a8ec690e94d4378d164ce0da021ab3126181"
	s3Image     = "docker.io/pgsty/silo:RELEASE.2026-10-26T00-00-00Z@sha256:7bb2aff6b89efe1cb1303c10fc21d579447434ed06649ac9ec05a5e2dbf30ad7"
	prefix      = "imagor/"
)

type deps struct {
	fx.In

	Imagor  *stdimagorfx.Client
	Storage *stds3fx.Client
}

func setup(tb testing.TB, env map[string]string) (deps, string) {
	tb.Helper()

	var d deps

	env["STDS3_BUCKETS"] = "main:stdimagor-test"

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(env),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdhttpclientfx.Provide(),
		stds3testfx.Provide(tb, s3Image),
		stdimagortestfx.Provide(tb, imagorImage, s3Image, "main", prefix),
		fx.Populate(&d),
	)
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	bucket, _ := d.Storage.Bucket("main")

	return d, bucket
}

// source puts body at key, and signs a GET of it for services.
func source(tb testing.TB, d deps, bucket, key string, body []byte) string {
	tb.Helper()

	_, err := d.Storage.S3().PutObject(tb.Context(), bucket, key, bytes.NewReader(body), int64(len(body)),
		minio.PutObjectOptions{})
	require.NoError(tb, err)

	get, err := d.Storage.Internal().PresignGet(tb.Context(), bucket, key, time.Minute, nil)
	require.NoError(tb, err)

	return get.URL
}

func TestRenderAndAwait(t *testing.T) {
	t.Parallel()

	d, bucket := setup(t, map[string]string{})

	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	img.Set(0, 0, color.White)

	var original bytes.Buffer
	require.NoError(t, png.Encode(&original, img))

	key, err := d.Imagor.Render(t.Context(), imagorpath.Params{
		Width:   10,
		Height:  10,
		FitIn:   true,
		Filters: imagorpath.Filters{{Name: "format", Args: "webp"}},
		Image:   source(t, d, bucket, "original", original.Bytes()),
	})
	require.NoError(t, err)

	// The result arrives under the prefix at the key Render returned.
	var info minio.ObjectInfo

	require.NoError(t, d.Imagor.Await(t.Context(), func(ctx context.Context) (bool, error) {
		info, err = d.Storage.S3().StatObject(ctx, bucket, prefix+key, minio.StatObjectOptions{})
		if minio.ToErrorResponse(err).Code == minio.NoSuchKey {
			return false, nil
		}

		return err == nil, err
	}))
	require.Equal(t, "image/webp", info.ContentType)
}

func TestRenderOfUndecodableSourceIsRejected(t *testing.T) {
	t.Parallel()

	d, bucket := setup(t, map[string]string{})

	_, err := d.Imagor.Render(t.Context(), imagorpath.Params{
		Width: 10, Height: 10, Image: source(t, d, bucket, "original", []byte("not an image")),
	})
	require.True(t, errors.Is(err, stdimagorfx.ErrRejected), err)
}

func TestAwaitFailsWhenNothingArrives(t *testing.T) {
	t.Parallel()

	d, _ := setup(t, map[string]string{"STDIMAGOR_DELIVERY_TIMEOUT": "100ms"})

	err := d.Imagor.Await(t.Context(), func(context.Context) (bool, error) { return false, nil })
	require.ErrorIs(t, err, stdimagorfx.ErrNotDelivered)
}
