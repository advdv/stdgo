package stdcrpcapikey_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/advdv/stdgo/fx/stdpgxfx"
	"github.com/advdv/stdgo/fx/stdpgxfx/stdpgxtestfx"
	"github.com/advdv/stdgo/fx/stdpgxtxfx"
	"github.com/advdv/stdgo/fx/stdzapfx"
	"github.com/advdv/stdgo/internal/stdtestpg"
	"github.com/advdv/stdgo/stdcrpc/stdcrpcapikey"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdenvcfg"
	"github.com/advdv/stdgo/stdtx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/peterldowns/pgtestdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

// dbtx is the type the query takes its transaction as, like sqlc's DBTX.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// apiKey is a stored key, like the row type sqlc generates.
type apiKey struct {
	UserID     uuid.UUID
	SecretHash string
	ExpireTime *time.Time
}

func (k apiKey) KeyUserID() uuid.UUID      { return k.UserID }
func (k apiKey) KeyHash() string           { return k.SecretHash }
func (k apiKey) KeyExpireTime() *time.Time { return k.ExpireTime }

// queries stands in for the queries sqlc generates.
type queries struct{}

func (queries) GetAPIKeyByID(ctx context.Context, db dbtx, id uuid.UUID) (apiKey, error) {
	var k apiKey

	err := db.QueryRow(ctx, `SELECT user_id, secret_hash, expire_time FROM api_key WHERE id = $1`, id).
		Scan(&k.UserID, &k.SecretHash, &k.ExpireTime)

	return k, err
}

// The procedures of the test server.
const (
	createProcedure = "/test.v1.UserService/CreateUser"
	listProcedure   = "/test.v1.UserService/ListUsers"
	bootstrapKey    = "test-bootstrap-key"
)

// fixture is a server that authenticates with the keys in a database of its
// own.
type fixture struct {
	ctx    context.Context
	rw     *stdtx.Transactor[pgx.Tx]
	server *connect.Server
}

func setup(tb testing.TB) fixture {
	tb.Helper()

	var deps struct {
		fx.In

		Logger *zap.Logger
		RW     *stdtx.Transactor[pgx.Tx] `name:"rw"`
		RO     *stdtx.Transactor[pgx.Tx] `name:"ro"`
	}

	app := fxtest.New(tb,
		stdenvcfg.ProvideExplicitEnvironment(nil),
		stdzapfx.Fx(),
		stdzapfx.TestProvide(tb),
		stdpgxtestfx.Provide(tb, stdtestpg.Image, pgtestdb.NoopMigrator{}, stdpgxfx.NewPgxV5Driver(), "rw", "ro"),
		stdpgxtxfx.TestProvide("testapp", "postgres", "postgres"),
		fx.Populate(&deps),
	)
	app.RequireStart()
	tb.Cleanup(app.RequireStop)

	ctx := stdctx.WithLogger(tb.Context(), deps.Logger)

	require.NoError(tb, stdtx.Transact0(ctx, deps.RW, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `CREATE TABLE api_key (
			id uuid PRIMARY KEY, user_id uuid NOT NULL, secret_hash text NOT NULL, expire_time timestamptz)`)

		return err
	}))

	// The lookup runs the query in a read-only transaction.
	lookup := func(ctx context.Context, id uuid.UUID) (stdcrpcapikey.Row[uuid.UUID], error) {
		return stdtx.Transact1(ctx, deps.RO, func(ctx context.Context, tx pgx.Tx) (stdcrpcapikey.Row[uuid.UUID], error) {
			return queries{}.GetAPIKeyByID(ctx, tx, id)
		})
	}

	server := connect.NewServer(stdcrpcapikey.NewServerInterceptor(lookup,
		stdcrpcapikey.WithBootstrapKey(bootstrapKey, createProcedure)))

	for _, procedure := range []string{createProcedure, listProcedure} {
		server.Register(connect.Method{
			Spec: connect.Spec{Procedure: procedure, StreamType: connect.StreamTypeUnary},
			Handler: func(ctx context.Context, _ connect.Spec, _ connect.ServerStream) error {
				caller, ok := stdcrpcapikey.CallerOf[uuid.UUID](ctx)
				if !ok {
					return connect.NewError(connect.CodeInternal, "no caller")
				}

				*callerOut(ctx) = caller

				return nil
			},
		})
	}

	return fixture{ctx: ctx, rw: deps.RW, server: server}
}

// callerOutKey is the context key under which a call expects its caller.
type callerOutKey struct{}

func callerOut(ctx context.Context) *stdcrpcapikey.Caller[uuid.UUID] {
	return ctx.Value(callerOutKey{}).(*stdcrpcapikey.Caller[uuid.UUID])
}

// call calls procedure with the Authorization header authz, unset when
// empty, and returns the caller the handler saw.
func (f fixture) call(procedure, authz string) (stdcrpcapikey.Caller[uuid.UUID], error) {
	var caller stdcrpcapikey.Caller[uuid.UUID]

	info := &connect.CallInfo{}
	if authz != "" {
		info.RequestHeader().Set("Authorization", authz)
	}

	err := f.server.Call(context.WithValue(f.ctx, callerOutKey{}, &caller), procedure, info, nil)

	return caller, err
}

// insert stores a new key of userID that expires at expireTime, nil for
// never, and returns it with its ID.
func (f fixture) insert(tb testing.TB, userID uuid.UUID, expireTime *time.Time) (string, uuid.UUID) {
	tb.Helper()

	id := uuid.NewV7()
	key, hash, err := stdcrpcapikey.New(id)
	require.NoError(tb, err)

	require.NoError(tb, stdtx.Transact0(f.ctx, f.rw, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO api_key (id, user_id, secret_hash, expire_time) VALUES ($1, $2, $3, $4)`,
			id, userID, hash, expireTime)

		return err
	}))

	return key, id
}

func TestNew(t *testing.T) {
	t.Parallel()

	id := uuid.NewV7()
	key, hash, err := stdcrpcapikey.New(id)
	require.NoError(t, err)

	other, _, err := stdcrpcapikey.New(id)
	require.NoError(t, err)

	require.True(t, strings.HasPrefix(key, "ak_"+id.String()+"_"), key)
	require.Len(t, hash, 64)
	require.NotContains(t, hash, key)
	require.NotEqual(t, key, other, "keys of one ID differ in their random part")
}

func TestServerInterceptor(t *testing.T) {
	t.Parallel()

	f := setup(t)
	user := uuid.NewV7()
	key, _ := f.insert(t, user, nil)
	future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Second)
	unexpired, _ := f.insert(t, user, &future)
	expired, _ := f.insert(t, user, &past)

	// Another valid key with its last character replaced.
	wrongSecret := key[:len(key)-1] + map[bool]string{true: "A", false: "B"}[!strings.HasSuffix(key, "A")]
	id, _, _ := strings.Cut(strings.TrimPrefix(key, "ak_"), "_")
	unknownID := strings.Replace(key, id, uuid.NewV7().String(), 1)

	for _, tc := range []struct {
		desc      string
		procedure string
		authz     string
		want      stdcrpcapikey.Caller[uuid.UUID]
		wantCode  connect.Code
	}{
		{desc: "user key", procedure: listProcedure, authz: "Bearer " + key, want: stdcrpcapikey.Caller[uuid.UUID]{UserID: user}},
		{desc: "scheme in lower case", procedure: listProcedure, authz: "bearer " + key, want: stdcrpcapikey.Caller[uuid.UUID]{UserID: user}},
		{desc: "unexpired key", procedure: listProcedure, authz: "Bearer " + unexpired, want: stdcrpcapikey.Caller[uuid.UUID]{UserID: user}},
		{desc: "user key on bootstrap procedure", procedure: createProcedure, authz: "Bearer " + key, want: stdcrpcapikey.Caller[uuid.UUID]{UserID: user}},
		{desc: "no header", procedure: listProcedure, wantCode: connect.CodeUnauthenticated},
		{desc: "no key", procedure: listProcedure, authz: "Bearer", wantCode: connect.CodeUnauthenticated},
		{desc: "other scheme", procedure: listProcedure, authz: "Basic " + key, wantCode: connect.CodeUnauthenticated},
		{desc: "not a key", procedure: listProcedure, authz: "Bearer nope", wantCode: connect.CodeUnauthenticated},
		{desc: "no random part", procedure: listProcedure, authz: "Bearer ak_" + id + "_", wantCode: connect.CodeUnauthenticated},
		{desc: "ID not a UUID", procedure: listProcedure, authz: "Bearer ak_nope_ABC", wantCode: connect.CodeUnauthenticated},
		{desc: "unknown ID", procedure: listProcedure, authz: "Bearer " + unknownID, wantCode: connect.CodeUnauthenticated},
		{desc: "wrong secret", procedure: listProcedure, authz: "Bearer " + wrongSecret, wantCode: connect.CodeUnauthenticated},
		{desc: "expired key", procedure: listProcedure, authz: "Bearer " + expired, wantCode: connect.CodeUnauthenticated},
		{desc: "bootstrap key", procedure: createProcedure, authz: "Bearer " + bootstrapKey, want: stdcrpcapikey.Caller[uuid.UUID]{Bootstrap: true}},
		{desc: "bootstrap key elsewhere", procedure: listProcedure, authz: "Bearer " + bootstrapKey, wantCode: connect.CodePermissionDenied},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			got, err := f.call(tc.procedure, tc.authz)
			if tc.wantCode != 0 {
				require.Equal(t, tc.wantCode, connect.CodeOf(err), "error: %v", err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestServerInterceptorRevokesDeletedKey(t *testing.T) {
	t.Parallel()

	f := setup(t)
	key, id := f.insert(t, uuid.NewV7(), nil)

	_, err := f.call(listProcedure, "Bearer "+key)
	require.NoError(t, err)

	require.NoError(t, stdtx.Transact0(f.ctx, f.rw, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM api_key WHERE id = $1`, id)

		return err
	}))

	_, err = f.call(listProcedure, "Bearer "+key)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestServerInterceptorWithoutBootstrapKey(t *testing.T) {
	t.Parallel()

	// An empty bootstrap key does not match an empty bearer token, or any
	// other; every key goes to the lookup, which finds none.
	notFound := func(context.Context, uuid.UUID) (stdcrpcapikey.Row[uuid.UUID], error) { return nil, pgx.ErrNoRows }

	server := connect.NewServer(stdcrpcapikey.NewServerInterceptor(notFound, stdcrpcapikey.WithBootstrapKey("", createProcedure)))
	server.Register(connect.Method{
		Spec:    connect.Spec{Procedure: createProcedure, StreamType: connect.StreamTypeUnary},
		Handler: func(context.Context, connect.Spec, connect.ServerStream) error { return nil },
	})

	for _, authz := range []string{"Bearer ", "Bearer  ", "Bearer nope", "Bearer ak_" + uuid.NewV7().String() + "_X"} {
		info := &connect.CallInfo{}
		info.RequestHeader().Set("Authorization", authz)

		err := server.Call(t.Context(), createProcedure, info, nil)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "authz %q", authz)
	}
}

func TestServerInterceptorFailsWithLookupError(t *testing.T) {
	t.Parallel()

	// A failing database is no verdict on the key: the error passes on, for
	// the error interceptor to log and answer with INTERNAL.
	errDown := errors.New("database down")
	failing := func(context.Context, uuid.UUID) (stdcrpcapikey.Row[uuid.UUID], error) { return nil, errDown }

	server := connect.NewServer(stdcrpcapikey.NewServerInterceptor(failing))
	server.Register(connect.Method{
		Spec:    connect.Spec{Procedure: listProcedure, StreamType: connect.StreamTypeUnary},
		Handler: func(context.Context, connect.Spec, connect.ServerStream) error { return nil },
	})

	key, _, err := stdcrpcapikey.New(uuid.NewV7())
	require.NoError(t, err)

	info := &connect.CallInfo{}
	info.RequestHeader().Set("Authorization", "Bearer "+key)

	err = server.Call(t.Context(), listProcedure, info, nil)
	require.ErrorIs(t, err, errDown)
	require.NotEqual(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// A key whose ID does not parse never reaches the lookup.
	info = &connect.CallInfo{}
	info.RequestHeader().Set("Authorization", "Bearer ak_nope_ABC")

	err = server.Call(t.Context(), listProcedure, info, nil)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

// slug is a key ID whose text may contain "_", unlike a UUID's.
type slug string

func (s slug) MarshalText() ([]byte, error) { return []byte(s), nil }

func (s *slug) UnmarshalText(text []byte) error {
	*s = slug(text)

	return nil
}

// memKey is a stored key of a user with an int64 ID.
type memKey struct {
	userID int64
	hash   string
}

func (k memKey) KeyUserID() int64          { return k.userID }
func (k memKey) KeyHash() string           { return k.hash }
func (k memKey) KeyExpireTime() *time.Time { return nil }

func TestServerInterceptorWithOtherIDTypes(t *testing.T) {
	t.Parallel()

	id := slug("ci_deploy")
	key, hash, err := stdcrpcapikey.New(id)
	require.NoError(t, err)

	keys := map[slug]memKey{id: {userID: 42, hash: hash}}
	lookup := func(_ context.Context, id slug) (stdcrpcapikey.Row[int64], error) {
		k, ok := keys[id]
		if !ok {
			return nil, pgx.ErrNoRows
		}

		return k, nil
	}

	var got stdcrpcapikey.Caller[int64]

	server := connect.NewServer(stdcrpcapikey.NewServerInterceptor(lookup))
	server.Register(connect.Method{
		Spec: connect.Spec{Procedure: listProcedure, StreamType: connect.StreamTypeUnary},
		Handler: func(ctx context.Context, _ connect.Spec, _ connect.ServerStream) error {
			var ok bool
			if got, ok = stdcrpcapikey.CallerOf[int64](ctx); !ok {
				return connect.NewError(connect.CodeInternal, "no caller")
			}

			// The caller is not there for another UID type.
			if _, ok := stdcrpcapikey.CallerOf[uuid.UUID](ctx); ok {
				return connect.NewError(connect.CodeInternal, "caller of another UID type")
			}

			return nil
		},
	})

	call := func(key string) error {
		info := &connect.CallInfo{}
		info.RequestHeader().Set("Authorization", "Bearer "+key)

		return server.Call(t.Context(), listProcedure, info, nil)
	}

	// The ID's "_" stays part of the ID.
	require.NoError(t, call(key))
	require.Equal(t, stdcrpcapikey.Caller[int64]{UserID: 42}, got)

	other, _, err := stdcrpcapikey.New(slug("ci"))
	require.NoError(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(call(other)))

	_, _, err = stdcrpcapikey.New(slug(""))
	require.Error(t, err)
}

func TestPrefix(t *testing.T) {
	t.Parallel()

	keys := map[uuid.UUID]memKey{}
	lookup := func(_ context.Context, id uuid.UUID) (stdcrpcapikey.Row[int64], error) {
		k, ok := keys[id]
		if !ok {
			return nil, pgx.ErrNoRows
		}

		return k, nil
	}

	// mint stores a new key of user 1, minted with opts.
	mint := func(opts ...stdcrpcapikey.Option) string {
		id := uuid.NewV7()
		key, hash, err := stdcrpcapikey.New(id, opts...)
		require.NoError(t, err)

		keys[id] = memKey{userID: 1, hash: hash}

		return key
	}

	// accepts reports whether an interceptor with opts accepts key.
	accepts := func(key string, opts ...stdcrpcapikey.Option) bool {
		server := connect.NewServer(stdcrpcapikey.NewServerInterceptor(lookup, opts...))
		server.Register(connect.Method{
			Spec:    connect.Spec{Procedure: listProcedure, StreamType: connect.StreamTypeUnary},
			Handler: func(context.Context, connect.Spec, connect.ServerStream) error { return nil },
		})

		info := &connect.CallInfo{}
		info.RequestHeader().Set("Authorization", "Bearer "+key)

		err := server.Call(t.Context(), listProcedure, info, nil)
		if err != nil {
			require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err), "error: %v", err)
		}

		return err == nil
	}

	acme := stdcrpcapikey.WithPrefix("acme_")
	none := stdcrpcapikey.WithPrefix("")

	defaultKey, acmeKey, bareKey := mint(), mint(acme), mint(none)
	require.True(t, strings.HasPrefix(defaultKey, stdcrpcapikey.DefaultPrefix), defaultKey)
	require.True(t, strings.HasPrefix(acmeKey, "acme_"), acmeKey)

	require.True(t, accepts(defaultKey))
	require.True(t, accepts(acmeKey, acme))
	require.True(t, accepts(bareKey, none))

	// An interceptor accepts only keys with its own prefix.
	require.False(t, accepts(acmeKey))
	require.False(t, accepts(defaultKey, acme))
	require.False(t, accepts(acmeKey, none))
}
