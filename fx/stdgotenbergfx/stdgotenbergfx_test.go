package stdgotenbergfx_test

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/advdv/stdgo/fx/stdgotenbergfx"
	"github.com/advdv/stdgo/fx/stdgotenbergfx/stdgotenbergtestfx"
	"github.com/advdv/stdgo/fx/stdhttpclientfx"
	"github.com/advdv/stdgo/fx/stds3fx"
	"github.com/advdv/stdgo/fx/stds3fx/stds3test"
	"github.com/advdv/stdgo/fx/stds3fx/stds3testfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/cockroachdb/errors"
	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

const (
	gotenbergImage = "docker.io/gotenberg/gotenberg:9.9.3@sha256:9731688d1b6290e4c04d9a47dbd5fb29f92aa0076feb6592c5c777525892cd72"
	s3Image        = "docker.io/pgsty/silo:RELEASE.2026-10-26T00-00-00Z@sha256:7bb2aff6b89efe1cb1303c10fc21d579447434ed06649ac9ec05a5e2dbf30ad7"
)

type deps struct {
	fx.In

	Gotenberg *stdgotenbergfx.Client
	Storage   *stds3fx.Client
}

func setup(tb testing.TB) (deps, string) {
	tb.Helper()

	var d deps

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{"STDS3_BUCKETS": "main:stdgotenberg-test"}),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdhttpclientfx.Provide(),
		stds3testfx.Provide(tb, s3Image),
		stdgotenbergtestfx.Provide(tb, gotenbergImage),
		fx.Populate(&d),
	)
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	bucket, _ := d.Storage.Bucket("main")

	return d, bucket
}

// presign signs, for services, a GET of the object at source that names it
// filename, and a PUT of a PDF to target.
func presign(tb testing.TB, d deps, bucket, source, filename, target string) (get, put *stds3fx.PresignedRequest) {
	tb.Helper()

	get, err := d.Storage.Internal().PresignGet(tb.Context(), bucket, source, time.Minute, url.Values{
		"response-content-disposition": {`attachment; filename="` + filename + `"`},
	})
	require.NoError(tb, err)

	put, err = d.Storage.Internal().PresignPut(tb.Context(), bucket, target, time.Minute, http.Header{
		"Content-Type": {"application/pdf"},
	})
	require.NoError(tb, err)

	return get, put
}

func TestConvertOffice(t *testing.T) {
	t.Parallel()

	d, bucket := setup(t)

	_, err := d.Storage.S3().PutObject(t.Context(), bucket, "report", bytes.NewReader([]byte("Quarterly report")), 16,
		minio.PutObjectOptions{})
	require.NoError(t, err)

	get, put := presign(t, d, bucket, "report", "report.txt", "report.pdf")
	require.NoError(t, d.Gotenberg.ConvertOffice(t.Context(), get, put))

	download, err := d.Storage.PresignGet(t.Context(), bucket, "report.pdf", time.Minute, nil)
	require.NoError(t, err)

	headers, body, err := stds3test.Send(t, download, nil)
	require.NoError(t, err)
	require.Equal(t, "application/pdf", headers.Get("Content-Type"))
	require.True(t, bytes.HasPrefix(body, []byte("%PDF-")), "not a PDF: %.32q", body)
}

func TestConvertOfficeOfMissingSourceIsRejected(t *testing.T) {
	t.Parallel()

	d, bucket := setup(t)

	// Gotenberg downloads the source before it accepts the request.
	get, put := presign(t, d, bucket, "missing", "report.txt", "report.pdf")
	err := d.Gotenberg.ConvertOffice(t.Context(), get, put)
	require.True(t, errors.Is(err, stdgotenbergfx.ErrRejected), err)
}
