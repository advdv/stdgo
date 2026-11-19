// Package stdcrpcapikey authenticates connect-go v2 RPCs with API keys that a
// database stores. A key is "<prefix><id>_<random>", such as
// "ak_<id>_<random>" with the default prefix: the prefix lets secret scanners
// and people tell a key apart, the ID finds the stored key
// without searching by its hash, and the random part, 130 bits, makes it
// unguessable. The database stores only the key's SHA-256 hash, so a leaked
// row does not leak the key, and a deleted row revokes the key at once.
//
// [New] mints keys. [NewServerInterceptor] authenticates every RPC with the
// key in its "Authorization: Bearer <key>" header, before the request is
// decoded, and puts the [Caller] in the context. It finds the stored key with
// a [Lookup] the application provides, which typically calls a query that
// sqlc generates.
//
// The package is generic over the type of a key's ID, KID, and of a user's
// ID, UID, such as uuid.UUID for both. A key's ID goes in the key as its
// text, so KID implements encoding.TextMarshaler, and *KID
// encoding.TextUnmarshaler; the text must not contain whitespace. The
// package only passes a UID on.
//
// [New] and [NewServerInterceptor] take the same [Option]s: pass both the
// same [WithPrefix], or neither, so the interceptor accepts the keys New
// mints.
//
// An optional bootstrap key, [WithBootstrapKey], belongs to no user and may
// only call the procedures it is given, such as those that create a system's
// first user and its key.
package stdcrpcapikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding"
	"encoding/hex"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5"
)

// DefaultPrefix starts every key unless [WithPrefix] gives another prefix.
const DefaultPrefix = "ak_"

// New mints a key for the stored key with ID id, and returns it with the hash
// to store, its SHA-256 in lower-case hex. It fails when id has no text, or
// when its MarshalText does. Of opts, it uses only [WithPrefix].
func New[KID encoding.TextMarshaler](id KID, opts ...Option) (key, hash string, err error) {
	cfg := newOptions(opts)

	text, err := id.MarshalText()
	if err != nil {
		return "", "", errors.Wrap(err, "marshal key ID")
	}

	if len(text) == 0 {
		return "", "", errors.New("key ID has no text")
	}

	key = cfg.prefix + string(text) + "_" + rand.Text()

	return key, hashOf(key), nil
}

// hashOf returns the hash of key that New returns with it.
func hashOf(key string) string {
	sum := sha256.Sum256([]byte(key))

	return hex.EncodeToString(sum[:])
}

// parse returns the ID in a key of the form New returns, and false when key
// is not of that form. It splits the key at its last "_", which the random
// part never contains, so an ID's text may contain "_".
func parse[KID any, PKID interface {
	*KID
	encoding.TextUnmarshaler
}](prefix, key string) (KID, bool) {
	var id KID

	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return id, false
	}

	text, random, ok := strings.CutLast(rest, "_")
	if !ok || text == "" || random == "" {
		return id, false
	}

	if err := PKID(&id).UnmarshalText([]byte(text)); err != nil {
		return id, false
	}

	return id, true
}

// Row is what the interceptor reads from a stored key. Give the row type
// that the query returns these methods, such as in a file next to the
// generated code.
type Row[UID any] interface {
	// KeyUserID returns the ID of the user the key authenticates.
	KeyUserID() UID
	// KeyHash returns the hash that [New] returned with the key.
	KeyHash() string
	// KeyExpireTime returns when the key expires; nil when it does not.
	KeyExpireTime() *time.Time
}

// Lookup finds the stored key with ID id, and fails with pgx.ErrNoRows when
// there is none, as a query that sqlc generates for pgx does. It decides how
// to reach the database, such as in a read-only transaction or on a pool.
type Lookup[KID, UID any] func(ctx context.Context, id KID) (Row[UID], error)

// errUnauthenticated is the error of authenticate for a key it does not
// accept.
var errUnauthenticated = errors.New("unauthenticated")

// authenticate returns the ID of the user that key belongs to. It fails with
// errUnauthenticated unless key is of the form [New] returns, lookup finds a
// stored key with its ID and its hash, and that key has not expired.
func authenticate[KID any, PKID interface {
	*KID
	encoding.TextUnmarshaler
}, UID any](ctx context.Context, lookup Lookup[KID, UID], prefix, key string) (UID, error) {
	var none UID

	id, ok := parse[KID, PKID](prefix, key)
	if !ok {
		return none, errUnauthenticated
	}

	row, err := lookup(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return none, errUnauthenticated
	} else if err != nil {
		return none, errors.Wrap(err, "look up API key")
	}

	if subtle.ConstantTimeCompare([]byte(hashOf(key)), []byte(row.KeyHash())) != 1 {
		return none, errUnauthenticated
	}

	if expire := row.KeyExpireTime(); expire != nil && !time.Now().Before(*expire) {
		return none, errUnauthenticated
	}

	return row.KeyUserID(), nil
}

// Caller is who calls an RPC: a user, through one of its keys, or the
// bootstrap key, which is no user.
type Caller[UID any] struct {
	// UserID is the ID of the user; the zero UID for the bootstrap key.
	UserID UID
	// Bootstrap is true for a call with the bootstrap key.
	Bootstrap bool
}

// callerKey is the context key of the [Caller].
type callerKey struct{}

// CallerOf returns the caller of the RPC that ctx belongs to, which the
// interceptor of [NewServerInterceptor] put there, and false outside such an
// RPC, or when the interceptor's UID is another type.
func CallerOf[UID any](ctx context.Context) (Caller[UID], bool) {
	caller, ok := ctx.Value(callerKey{}).(Caller[UID])

	return caller, ok
}

// options configure [New] and [NewServerInterceptor].
type options struct {
	prefix              string
	bootstrapKey        string
	bootstrapProcedures map[string]bool
}

// newOptions returns the defaults with opts applied.
func newOptions(opts []Option) options {
	cfg := options{prefix: DefaultPrefix}
	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

// Option configures [New] and [NewServerInterceptor].
type Option func(*options)

// WithPrefix starts keys with prefix instead of [DefaultPrefix], such as
// "acme_"; it is used as it is, so include a separator. Pass it to both [New]
// and [NewServerInterceptor].
func WithPrefix(prefix string) Option {
	return func(o *options) { o.prefix = prefix }
}

// WithBootstrapKey makes [NewServerInterceptor] accept key as the bootstrap
// key, which is no user's and may call only procedures, such as
// "/acme.v1.UserService/CreateUser"; it fails every other call with
// PERMISSION_DENIED. An empty key leaves the bootstrap key out. [New] ignores
// it.
func WithBootstrapKey(key string, procedures ...string) Option {
	return func(o *options) {
		o.bootstrapKey = key
		o.bootstrapProcedures = make(map[string]bool, len(procedures))

		for _, procedure := range procedures {
			o.bootstrapProcedures[procedure] = true
		}
	}
}

// NewServerInterceptor returns the interceptor that authenticates every RPC
// with the key in its "Authorization: Bearer <key>" header, and puts the
// [Caller] in the context. It fails a call with UNAUTHENTICATED, the same
// error whichever way the key is wrong: no key, a key of another form, one no
// stored key has the ID or the hash of, and one that expired. Go infers its
// type arguments from lookup. Install it inside an interceptor that logs and
// maps errors, and before any that reads the request message, such as
// validation, so a call without a valid key is never decoded.
func NewServerInterceptor[KID, UID any, PKID interface {
	*KID
	encoding.TextUnmarshaler
}](lookup Lookup[KID, UID], opts ...Option) connect.ServerInterceptor {
	cfg := newOptions(opts)

	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			unauthenticated := connect.NewError(connect.CodeUnauthenticated, "a valid API key is required")

			var header string
			if info, ok := connect.CallInfoForServerContext(ctx); ok {
				header = info.RequestHeader().Get("Authorization")
			}

			scheme, key, ok := strings.Cut(header, " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || key == "" {
				return unauthenticated
			}

			var caller Caller[UID]

			if cfg.bootstrapKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(cfg.bootstrapKey)) == 1 {
				if !cfg.bootstrapProcedures[spec.Procedure] {
					return connect.NewError(connect.CodePermissionDenied, "the bootstrap key may not call "+spec.Procedure)
				}

				caller = Caller[UID]{Bootstrap: true}
			} else {
				userID, err := authenticate[KID, PKID](ctx, lookup, cfg.prefix, key)
				if errors.Is(err, errUnauthenticated) {
					return unauthenticated
				} else if err != nil {
					return err
				}

				caller = Caller[UID]{UserID: userID}
			}

			return next(context.WithValue(ctx, callerKey{}, caller), spec, stream)
		}
	}
}
