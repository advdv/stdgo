package stdpgxfx_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestProvideNoDeriver(t *testing.T) {
	_, shared := setup(t)

	var res struct {
		fx.In
		DB0 *sql.DB `name:"db0"`
		DB1 *sql.DB `name:"db1"`
	}

	app := fxtest.New(t, shared,
		stdpgxfx.Provide(stdpgxfx.NewStandardDriver(), "db0", "db1"),
		fx.Populate(&res))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, res.DB0)
	require.NotNil(t, res.DB1)
}

func TestNoticeLogging(t *testing.T) {
	ctx, shared := setup(t)

	var db *sql.DB

	var obs *observer.ObservedLogs

	app := fxtest.New(t, shared, stdpgxfx.TestProvide(t, pgtestdb.NoopMigrator{}, stdpgxfx.NewStandardDriver(), "rw"), fx.Populate(&db, &obs))
	app.RequireStart()
	t.Cleanup(app.RequireStop)
	require.NotNil(t, db)

	_, err := db.ExecContext(ctx, `DO language plpgsql $$
BEGIN
  raise info 'information message' ;
  raise warning 'warning message';
  raise notice 'notice message';
END
$$;`)
	require.NoError(t, err)

	require.Equal(t, 1, obs.FilterMessage("notice: information message").Len())
	require.Equal(t, 1, obs.FilterMessage("notice: warning message").Len())
	require.Equal(t, 1, obs.FilterMessage("notice: notice message").Len())
}

func TestIamAuthRegionFromHost(t *testing.T) {
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
			require.Equal(t, tc.want, stdpgxfx.DeriveSigningRegion(tc.host))
		})
	}
}

func TestIamAuthBeforeConnectUsesPerPoolHost(t *testing.T) {
	// Simulate a Frankfurt-primary, Singapore-secondary deployment:
	// - rw pool stays on the Frankfurt cluster endpoint (signs eu-central-1)
	// - ro pool gets re-pointed by its Deriver to the Singapore reader endpoint
	//   (signs ap-southeast-1)
	// We build the *pgxpool.Config directly via stdpgxfx.New (rather than the
	// full fx graph) so we can drive BeforeConnect with arbitrary live conns.
	const (
		fraHost = "mycluster.cluster-abc123.eu-central-1.rds.amazonaws.com"
		sgpHost = "mycluster.cluster-ro-def456.ap-southeast-1.rds.amazonaws.com"
	)

	awsCfg, err := awsconfig.LoadDefaultConfig(t.Context(),
		awsconfig.WithRegion("eu-central-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("A", "B", "")))
	require.NoError(t, err)

	res, err := stdpgxfx.New(stdpgxfx.Params{
		Config: stdpgxfx.Config{
			MainDatabaseURL: "postgresql://postgres:postgres@" + fraHost + ":5432/postgres",
			IamAuth:         true,
		},
		AwsConfig: awsCfg,
		Logs:      zap.NewNop(),
	})
	require.NoError(t, err)
	require.NotNil(t, res.PoolConfig.BeforeConnect)

	// Drive BeforeConnect with a Frankfurt-shaped *pgx.ConnConfig and a
	// Singapore-shaped one. We can't inspect the actual SigV4 region without
	// decoding the auth token, but we CAN assert that token generation succeeds
	// (i.e., region resolution succeeds) and the password gets set — and we
	// inspect the produced token's URL since BuildAuthToken returns it as a
	// presigned URL containing X-Amz-Credential=<key>/<date>/<region>/...
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
		require.NotEmpty(t, pgc.Password, "token should be set for host=%s", tc.host)
		// the BuildAuthToken result is a presigned URL; the credential scope
		// includes the signing region.
		require.Containsf(t, pgc.Password, "%2F"+tc.wantRegion+"%2F",
			"token for host=%s should be signed for region=%s but was: %s",
			tc.host, tc.wantRegion, pgc.Password)
	}
}

func TestIamAuthBeforeConnectErrorsOnNonRegionalHost(t *testing.T) {
	awsCfg, err := awsconfig.LoadDefaultConfig(t.Context(),
		awsconfig.WithRegion("eu-central-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("A", "B", "")))
	require.NoError(t, err)

	res, err := stdpgxfx.New(stdpgxfx.Params{
		Config: stdpgxfx.Config{
			MainDatabaseURL: "postgresql://postgres:postgres@localhost:5432/postgres",
			IamAuth:         true,
		},
		AwsConfig: awsCfg,
		Logs:      zap.NewNop(),
	})
	require.NoError(t, err)
	require.NotNil(t, res.PoolConfig.BeforeConnect)

	pgc := res.PoolConfig.ConnConfig.Copy()
	err = res.PoolConfig.BeforeConnect(t.Context(), pgc)
	require.ErrorContains(t, err, "could not derive IAM signing region from host")
}

func TestRoPoolAutoRewritesAuroraClusterHost(t *testing.T) {
	// Use TestProvide so we don't actually need to connect — pgtestdb's NoopMigrator
	// gives us a base config we can inspect via Deriver invocations. We simulate
	// an Aurora cluster MAIN_DATABASE_URL and verify that the derived "ro" pool's
	// final host has been rewritten cluster- → cluster-ro- by stdpgxfx itself
	// (i.e., not by any stdenttxfx/stdpgxtxfx deriver).
	const fraHost = "mycluster.cluster-abc123.eu-central-1.rds.amazonaws.com"

	// We cannot easily observe pcfg post-conventions through fx (the pool is
	// built and conventions applied inside newDB), so we test the conventions
	// helper directly. This is the same code path newDB hits.
	pcfg, err := pgxpool.ParseConfig("postgresql://app@" + fraHost + ":5432/db")
	require.NoError(t, err)

	// Sanity: rw pool name leaves the host alone.
	rwCfg := pcfg.Copy()
	stdpgxfx.ApplyPoolHostConventions("rw", rwCfg, zap.NewNop())
	require.Equal(t, fraHost, rwCfg.ConnConfig.Host, "rw pool must not rewrite host")

	// ro pool name rewrites cluster- → cluster-ro- on RDS hosts.
	roCfg := pcfg.Copy()
	stdpgxfx.ApplyPoolHostConventions("ro", roCfg, zap.NewNop())
	require.Equal(t,
		"mycluster.cluster-ro-abc123.eu-central-1.rds.amazonaws.com",
		roCfg.ConnConfig.Host, "ro pool must rewrite cluster- to cluster-ro-")

	// ro pool name does NOT rewrite non-RDS hosts.
	localPcfg, err := pgxpool.ParseConfig("postgresql://postgres@localhost:5432/postgres")
	require.NoError(t, err)
	stdpgxfx.ApplyPoolHostConventions("ro", localPcfg, zap.NewNop())
	require.Equal(t, "localhost", localPcfg.ConnConfig.Host, "ro pool must not rewrite non-RDS hosts")

	// ro pool name does NOT rewrite RDS hosts that don't look like cluster endpoints.
	instPcfg, err := pgxpool.ParseConfig("postgresql://app@instance-1.abc123.eu-central-1.rds.amazonaws.com:5432/db")
	require.NoError(t, err)
	stdpgxfx.ApplyPoolHostConventions("ro", instPcfg, zap.NewNop())
	require.Equal(t,
		"instance-1.abc123.eu-central-1.rds.amazonaws.com",
		instPcfg.ConnConfig.Host, "ro pool must not rewrite non-cluster RDS hosts")
}

func TestProvideWithDeriver(t *testing.T) {
	_, shared := setup(t)

	var res struct {
		fx.In
		DB0 *sql.DB `name:"db0"`
		DB1 *sql.DB `name:"db1"`
	}

	var deriver0Name string

	var deriver1Name string

	app := fxtest.New(t, shared,
		stdpgxfx.Provide(stdpgxfx.NewStandardDriver(), "db0", "db1"),
		stdpgxfx.ProvideDeriver("db0", func(logs *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			deriver0Name = "db0"
			return base
		}),
		stdpgxfx.ProvideDeriver("db1", func(logs *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			deriver1Name = "db1"
			return base
		}),

		fx.Populate(&res))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, res.DB0)
	require.NotNil(t, res.DB1)
	require.Equal(t, "db0", deriver0Name)
	require.Equal(t, "db1", deriver1Name)
}

func TestProvideTest(t *testing.T) {
	_, shared := setup(t)

	var res struct {
		fx.In
		DB0 *sql.DB `name:"db0"`
		DB1 *sql.DB `name:"db1"`
	}

	app := fxtest.New(t, shared,
		stdpgxfx.TestProvide(t, pgtestdb.NoopMigrator{}, stdpgxfx.NewStandardDriver(), "db0", "db1"),
		fx.Populate(&res))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, res.DB0)
	require.NotNil(t, res.DB1)
}

func TestProvideTestWithMigrator(t *testing.T) {
	ctx, shared := setup(t)

	var res struct {
		fx.In
		DB0 *sql.DB `name:"db0"`
		DB1 *sql.DB `name:"db1"`
	}

	var db1DerivedUser string
	var db1DerivedPassword string
	var db0DerivedUser string
	var db0DerivedPassword string

	mig := goosemigrator.New(filepath.Join("testdata", "migrations1"))

	app := fxtest.New(t, shared,
		stdpgxfx.TestProvide(t, mig, stdpgxfx.NewStandardDriver(), "db0", "db1"),
		// imagine an after migration user, such that the deriver can see it.
		fx.Supply(&pgtestdb.Role{
			Username: "postgres",
			Password: "postgres",
		}),
		// derived databases should get the "after" role
		stdpgxfx.ProvideDeriver("db0", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			db0DerivedUser = base.ConnConfig.User
			db0DerivedPassword = base.ConnConfig.Password
			return base
		}),
		stdpgxfx.ProvideDeriver("db1", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			db1DerivedUser = base.ConnConfig.User
			db1DerivedPassword = base.ConnConfig.Password
			return base
		}),

		fx.Populate(&res))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, res.DB0)
	require.NotNil(t, res.DB1)

	_, err := res.DB0.ExecContext(ctx, `INSERT INTO foo(id) VALUES($1)`, uuid.New())
	require.NoError(t, err)
	_, err = res.DB1.ExecContext(ctx, `INSERT INTO foo(id) VALUES($1)`, uuid.New())
	require.NoError(t, err)

	require.Equal(t, "postgres", db0DerivedUser)
	require.Equal(t, "postgres", db0DerivedPassword)
	require.Equal(t, "postgres", db1DerivedUser)
	require.Equal(t, "postgres", db1DerivedPassword)
}

func TestProvidePgxPoolTestWithMigrator(t *testing.T) {
	ctx, shared := setup(t)

	var res struct {
		fx.In
		DB0 *pgxpool.Pool `name:"db0"`
		DB1 *pgxpool.Pool `name:"db1"`
	}

	var db1DerivedUser string
	var db1DerivedPassword string

	var db0DerivedUser string

	var db0DerivedPassword string

	mig := goosemigrator.New(filepath.Join("testdata", "migrations1"))

	app := fxtest.New(t, shared,
		stdpgxfx.TestProvide(t, mig, stdpgxfx.NewPgxV5Driver(), "db0", "db1"),
		// imagine an after migration user, such that the deriver can see it.
		fx.Supply(&pgtestdb.Role{
			Username: "postgres",
			Password: "postgres",
		}),
		// derived databases should get the "after" role
		stdpgxfx.ProvideDeriver("db0", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			db0DerivedUser = base.ConnConfig.User
			db0DerivedPassword = base.ConnConfig.Password
			return base
		}),
		stdpgxfx.ProvideDeriver("db1", func(_ *zap.Logger, base *pgxpool.Config) *pgxpool.Config {
			db1DerivedUser = base.ConnConfig.User
			db1DerivedPassword = base.ConnConfig.Password
			return base
		}),

		fx.Populate(&res))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.NotNil(t, res.DB0)
	require.NotNil(t, res.DB1)

	_, err := res.DB0.Exec(ctx, `INSERT INTO foo(id) VALUES($1)`, uuid.New())
	require.NoError(t, err)
	_, err = res.DB1.Exec(ctx, `INSERT INTO foo(id) VALUES($1)`, uuid.New())
	require.NoError(t, err)

	require.Equal(t, "postgres", db0DerivedUser)
	require.Equal(t, "postgres", db0DerivedPassword)
	require.Equal(t, "postgres", db1DerivedUser)
	require.Equal(t, "postgres", db1DerivedPassword)
}

func setup(tb testing.TB) (context.Context, fx.Option) {
	return tb.Context(), fx.Options(
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDPGX_MAIN_DATABASE_URL": "postgresql://postgres:postgres@localhost:5440/postgres",
		}),
	)
}
