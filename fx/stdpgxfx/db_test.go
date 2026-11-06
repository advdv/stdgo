package stdpgxfx_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

func TestIamAuthWithoutTokenFuncFails(t *testing.T) {
	t.Parallel()

	_, err := stdpgxfx.New(stdpgxfx.Params{
		Config: stdpgxfx.Config{
			MainDatabaseURL: "postgresql://postgres@localhost:5432/postgres",
			IamAuth:         true,
		},
		Logs: zap.NewNop(),
	})
	require.ErrorContains(t, err, "no IamTokenFunc is provided")
}

func TestIamAuthBeforeConnectUsesTokenFunc(t *testing.T) {
	t.Parallel()

	res, err := stdpgxfx.New(stdpgxfx.Params{
		Config: stdpgxfx.Config{
			MainDatabaseURL: "postgresql://postgres@localhost:5432/postgres",
			IamAuth:         true,
		},
		Logs: zap.NewNop(),
		IamToken: func(_ context.Context, cc *pgx.ConnConfig) (string, error) {
			return "token-for-" + cc.Host, nil
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res.PoolConfig.BeforeConnect)

	// the token func must see the live (possibly re-pointed) host.
	pgc := res.PoolConfig.ConnConfig.Copy()
	pgc.Host = "some-host"
	require.NoError(t, res.PoolConfig.BeforeConnect(t.Context(), pgc))
	require.Equal(t, "token-for-some-host", pgc.Password)
}

func TestIamAuthTokenFuncErrorIsReturned(t *testing.T) {
	t.Parallel()

	res, err := stdpgxfx.New(stdpgxfx.Params{
		Config: stdpgxfx.Config{
			MainDatabaseURL: "postgresql://postgres@localhost:5432/postgres",
			IamAuth:         true,
		},
		Logs: zap.NewNop(),
		IamToken: func(context.Context, *pgx.ConnConfig) (string, error) {
			return "", errors.New("boom")
		},
	})
	require.NoError(t, err)

	err = res.PoolConfig.BeforeConnect(t.Context(), res.PoolConfig.ConnConfig.Copy())
	require.ErrorContains(t, err, "boom")
}

func TestIamAuthDisabledLeavesBeforeConnectUntouched(t *testing.T) {
	t.Parallel()

	res, err := stdpgxfx.New(stdpgxfx.Params{
		Config: stdpgxfx.Config{
			MainDatabaseURL: "postgresql://postgres@localhost:5432/postgres",
		},
		Logs: zap.NewNop(),
		IamToken: func(context.Context, *pgx.ConnConfig) (string, error) {
			return "unused", nil
		},
	})
	require.NoError(t, err)
	require.Nil(t, res.PoolConfig.BeforeConnect)
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

func TestPerPoolHostOverride(t *testing.T) {
	const (
		fraHost      = "mycluster.cluster-abc123.eu-central-1.rds.amazonaws.com"
		overrideHost = "mycluster.cluster-ro-def456.ap-southeast-1.rds.amazonaws.com"
	)

	pcfg, err := pgxpool.ParseConfig("postgresql://app@" + fraHost + ":5432/db")
	require.NoError(t, err)

	hosts := map[string]string{"ro": overrideHost}

	// rw pool: no override registered → returns false, host unchanged.
	rwCfg := pcfg.Copy()
	applied := stdpgxfx.ApplyPoolHostOverride("rw", rwCfg, hosts, zap.NewNop())
	require.False(t, applied, "rw should not have an override applied")
	require.Equal(t, fraHost, rwCfg.ConnConfig.Host)

	// ro pool: override applied, host swapped, returns true.
	roCfg := pcfg.Copy()
	applied = stdpgxfx.ApplyPoolHostOverride("ro", roCfg, hosts, zap.NewNop())
	require.True(t, applied, "ro should have its override applied")
	require.Equal(t, overrideHost, roCfg.ConnConfig.Host)
}

func TestPerPoolHostOverrideEmptyValueIsIgnored(t *testing.T) {
	const fraHost = "mycluster.cluster-abc123.eu-central-1.rds.amazonaws.com"

	pcfg, err := pgxpool.ParseConfig("postgresql://app@" + fraHost + ":5432/db")
	require.NoError(t, err)

	// An empty-string value for the pool name should be treated as "no override".
	applied := stdpgxfx.ApplyPoolHostOverride("ro", pcfg, map[string]string{"ro": ""}, zap.NewNop())
	require.False(t, applied)
	require.Equal(t, fraHost, pcfg.ConnConfig.Host)
}

func TestPerPoolHostOverrideViaEnv(t *testing.T) {
	// End-to-end: STDPGX_HOSTS env var parses, plumbs into Config.Hosts,
	// reaches newDB, and the resulting *pgxpool.Pool's Config has the
	// overridden host. We use 127.0.0.1 for the "ro" override (resolves to
	// the same Postgres as localhost) so pool construction succeeds.
	var res struct {
		fx.In
		RW *pgxpool.Pool `name:"rw"`
		RO *pgxpool.Pool `name:"ro"`
	}

	app := fxtest.New(t,
		stdzapfx.Fx(),
		stdzapfx.TestProvide(t),
		stdenvcfg.ProvideExplicitEnvironment(map[string]string{
			"STDPGX_MAIN_DATABASE_URL": "postgresql://postgres:postgres@localhost:5440/postgres",
			"STDPGX_HOSTS":             "ro:127.0.0.1",
		}),
		stdpgxfx.TestProvide(t, pgtestdb.NoopMigrator{}, stdpgxfx.NewPgxV5Driver(), "rw", "ro"),
		fx.Populate(&res))
	app.RequireStart()
	t.Cleanup(app.RequireStop)

	require.Equal(t, "localhost", res.RW.Config().ConnConfig.Host,
		"rw pool should use the main URL host (no override registered)")
	require.Equal(t, "127.0.0.1", res.RO.Config().ConnConfig.Host,
		"ro pool should use the STDPGX_HOSTS[ro] override")
}

func TestPerPoolHostOverrideWinsOverConvention(t *testing.T) {
	// Drive the same code path newDB hits: try override first; if not applied,
	// fall through to convention. With override set, the cluster-ro convention
	// must NOT run.
	const (
		fraHost      = "mycluster.cluster-abc123.eu-central-1.rds.amazonaws.com"
		overrideHost = "totally-different-host.example.com"
	)

	pcfg, err := pgxpool.ParseConfig("postgresql://app@" + fraHost + ":5432/db")
	require.NoError(t, err)

	hosts := map[string]string{"ro": overrideHost}
	logger := zap.NewNop()

	if !stdpgxfx.ApplyPoolHostOverride("ro", pcfg, hosts, logger) {
		stdpgxfx.ApplyPoolHostConventions("ro", pcfg, logger)
	}
	require.Equal(t, overrideHost, pcfg.ConnConfig.Host,
		"override must win over the cluster-ro convention")
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
