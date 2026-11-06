package stdpgxiamfx_test

import (
	"testing"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdpgxfx/stdpgxiamfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

func staticAwsConfig(t *testing.T) aws.Config {
	t.Helper()

	awsCfg, err := awsconfig.LoadDefaultConfig(t.Context(),
		awsconfig.WithRegion("eu-central-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("A", "B", "")))
	require.NoError(t, err)

	return awsCfg
}

func TestDeriveSigningRegion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		host string
		want string
	}{
		{
			name: "regional cluster (writer) endpoint",
			host: "mycluster.cluster-abc123.eu-central-1.rds.amazonaws.com",
			want: "eu-central-1",
		},
		{
			name: "regional cluster reader endpoint",
			host: "mycluster.cluster-ro-abc123.eu-central-1.rds.amazonaws.com",
			want: "eu-central-1",
		},
		{
			name: "regional instance endpoint",
			host: "instance-1.abc123.ap-southeast-1.rds.amazonaws.com",
			want: "ap-southeast-1",
		},
		{
			name: "trailing dot",
			host: "mycluster.cluster-abc123.us-west-2.rds.amazonaws.com.",
			want: "us-west-2",
		},
		{
			name: "global writer endpoint does NOT match",
			host: "mycluster.global-abc123.global.rds.amazonaws.com",
			want: "",
		},
		{
			name: "non-RDS host",
			host: "myproxy.example.com",
			want: "",
		},
		{
			name: "localhost",
			host: "localhost",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, stdpgxiamfx.DeriveSigningRegion(tc.host))
		})
	}
}

func TestNewRequiresCredentials(t *testing.T) {
	t.Parallel()

	_, err := stdpgxiamfx.New(aws.Config{}, zap.NewNop())
	require.ErrorContains(t, err, "requires an AWS configuration with credentials")
}

func TestBeforeConnectUsesPerPoolHost(t *testing.T) {
	t.Parallel()

	// Simulate a Frankfurt-primary, Singapore-secondary deployment:
	// - rw pool stays on the Frankfurt cluster endpoint (signs eu-central-1)
	// - ro pool gets re-pointed to the Singapore reader endpoint (signs ap-southeast-1)
	const (
		fraHost = "mycluster.cluster-abc123.eu-central-1.rds.amazonaws.com"
		sgpHost = "mycluster.cluster-ro-def456.ap-southeast-1.rds.amazonaws.com"
	)

	tokf, err := stdpgxiamfx.New(staticAwsConfig(t), zap.NewNop())
	require.NoError(t, err)

	res, err := stdpgxfx.New(stdpgxfx.Params{
		Config: stdpgxfx.Config{
			MainDatabaseURL: "postgresql://postgres:postgres@" + fraHost + ":5432/postgres",
			IamAuth:         true,
		},
		IamToken: tokf,
		Logs:     zap.NewNop(),
	})
	require.NoError(t, err)
	require.NotNil(t, res.PoolConfig.BeforeConnect)

	// BuildAuthToken returns a presigned URL whose credential scope
	// (X-Amz-Credential=<key>/<date>/<region>/...) includes the signing region.
	for _, tc := range []struct {
		host       string
		wantRegion string
	}{
		{fraHost, "eu-central-1"},
		{sgpHost, "ap-southeast-1"},
	} {
		pgc := res.PoolConfig.ConnConfig.Copy()
		pgc.Host = tc.host
		err := res.PoolConfig.BeforeConnect(t.Context(), pgc)
		require.NoErrorf(t, err, "BeforeConnect should succeed for host=%s", tc.host)
		require.Containsf(t, pgc.Password, "%2F"+tc.wantRegion+"%2F",
			"token for host=%s should be signed for region=%s but was: %s",
			tc.host, tc.wantRegion, pgc.Password)
	}
}

func TestBeforeConnectErrorsOnNonRegionalHost(t *testing.T) {
	t.Parallel()

	tokf, err := stdpgxiamfx.New(staticAwsConfig(t), zap.NewNop())
	require.NoError(t, err)

	res, err := stdpgxfx.New(stdpgxfx.Params{
		Config: stdpgxfx.Config{
			MainDatabaseURL: "postgresql://postgres:postgres@localhost:5432/postgres",
			IamAuth:         true,
		},
		IamToken: tokf,
		Logs:     zap.NewNop(),
	})
	require.NoError(t, err)

	err = res.PoolConfig.BeforeConnect(t.Context(), res.PoolConfig.ConnConfig.Copy())
	require.ErrorContains(t, err, "could not derive IAM signing region from host")
}

func TestProvideWiresTokenFunc(t *testing.T) {
	t.Parallel()

	var pcfg *pgxpool.Config

	app := fxtest.New(t,
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDPGX_MAIN_DATABASE_URL": "postgresql://app@mycluster.cluster-abc123.eu-central-1.rds.amazonaws.com:5432/db",
			"STDPGX_IAM_AUTH":          "true",
		}),
		fx.Supply(staticAwsConfig(t)),
		stdpgxiamfx.Provide(),
		stdpgxfx.Provide(stdpgxfx.NewStandardDriver(), "rw"),
		fx.Populate(&pcfg))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	pgc := pcfg.ConnConfig.Copy()
	require.NoError(t, pcfg.BeforeConnect(t.Context(), pgc))
	require.Contains(t, pgc.Password, "%2Feu-central-1%2F")
}
