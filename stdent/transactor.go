package stdent

import "database/sql"

type options struct {
	serializationFailureMaxRetries int
	serializationFailureCodes      []string
	isolationLevel                 sql.IsolationLevel
	readOnly                       bool
}

// Option configures a Transactor.
type Option func(opts *options)

// SerializationFailureMaxRetries configures the maximum number of retries in case the transacted
// code encounters a serialization failure.
func SerializationFailureMaxRetries(v int) Option {
	return func(opts *options) { opts.serializationFailureMaxRetries = v }
}

// SerializationFailureCodes configures which PostgreSQL error codes should be considered serialization failures.
func SerializationFailureCodes(v ...string) Option {
	return func(opts *options) { opts.serializationFailureCodes = v }
}

// IsolationLevel specifies the isolation level for new transactions.
func IsolationLevel(v sql.IsolationLevel) Option {
	return func(opts *options) { opts.isolationLevel = v }
}

// ReadOnly sets the transaction to be read-only.
func ReadOnly(v bool) Option {
	return func(opts *options) { opts.readOnly = v }
}

// Transactor manages transactional operations with retry logic for serialization failures.
type Transactor[T Tx] struct {
	opts   options
	client Client[T]
}

// IsReadOnly reports whether the transactor is configured for read-only transactions.
func (txr Transactor[T]) IsReadOnly() bool {
	return txr.opts.readOnly
}

// New creates a new Transactor with the given client and options.
func New[T Tx](client Client[T], opts ...Option) *Transactor[T] {
	txr := &Transactor[T]{client: client}

	// this default is taken from the CockroachDB source code.
	SerializationFailureMaxRetries(50)(&txr.opts)
	// see https://www.postgresql.org/docs/current/mvcc-serialization-failure-handling.html
	SerializationFailureCodes("40001")(&txr.opts)
	// repeatable-read gives snapshot semantics and is the strictest level that works
	// against Aurora hot standbys. Callers can opt-in to serializable explicitly.
	IsolationLevel(sql.LevelRepeatableRead)(&txr.opts)
	// common to read and wrtie
	ReadOnly(false)(&txr.opts)

	for _, opt := range opts {
		opt(&txr.opts)
	}

	return txr
}
