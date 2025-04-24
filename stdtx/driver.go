package stdtx

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Driver abstracts the sql implementation details for the transactor to function.
type Driver[TTx any] interface {
	BeginTx(ctx context.Context) (TTx, error)
	RollbackTx(ctx context.Context, tx TTx) error
	CommitTx(ctx context.Context, tx TTx) error

	SerializationFailureCodes() []string
	SerializationFailureMaxRetries() int

	TxDoneError() error
}

type pgxV5Driver struct {
	db           *pgxpool.Pool
	txAccessMode pgx.TxAccessMode
}

// NewPgxV5Driver implements the driver for pgx v5.
func NewPgxV5Driver(db *pgxpool.Pool, txAccessMode pgx.TxAccessMode) Driver[pgx.Tx] {
	return pgxV5Driver{db, txAccessMode}
}

func (d pgxV5Driver) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return d.db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.Serializable,
		AccessMode: d.txAccessMode,
	})
}

func (d pgxV5Driver) RollbackTx(ctx context.Context, tx pgx.Tx) error {
	return tx.Rollback(ctx)
}

func (d pgxV5Driver) CommitTx(ctx context.Context, tx pgx.Tx) error {
	return tx.Commit(ctx)
}

func (d pgxV5Driver) SerializationFailureCodes() []string {
	return []string{"40001"}
}

func (d pgxV5Driver) SerializationFailureMaxRetries() int {
	return 50
}

func (d pgxV5Driver) TxDoneError() error {
	return pgx.ErrTxClosed
}
