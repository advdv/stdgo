package stdtx

import (
	"context"
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
