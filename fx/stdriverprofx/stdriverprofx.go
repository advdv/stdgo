// Package stdriverprofx builds River Pro's clients for package stdriverfx
// (github.com/advdv/stdgo/fx/stdriverfx/v2), which builds on open-source River
// only, and inserts River Pro's workflows with them.
//
// It is a module of its own: River Pro is private, and downloading it needs
// River Pro's license key, which the rest of stdgo must build without.
package stdriverprofx

import (
	"github.com/cockroachdb/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"riverqueue.com/riverpro"
	"riverqueue.com/riverpro/driver/riverpropgxv5"
)

// Client is a River Pro client on a pgx pool.
type Client = riverpro.Client[pgx.Tx]

// NewClient builds a River Pro client on pool, configured by cfg. Pass it to
// stdriverfx.Provide for River Pro's clients.
func NewClient(pool *pgxpool.Pool, cfg *river.Config) (*Client, error) {
	client, err := riverpro.NewClient(riverpropgxv5.New(pool), &riverpro.Config{Config: *cfg})

	return client, errors.Wrap(err, "new river pro client")
}
