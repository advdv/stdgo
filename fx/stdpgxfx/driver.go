package stdpgxfx

import (
	"context"
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// Driver abstracts turning a pgxpool config into connection pool and closing it.
type Driver[DBT any] interface {
	NewPool(pcfg *pgxpool.Config) (DBT, error)
	Close(pool DBT) error
}

type standardDriver struct{}

// NewStandardDriver inits a driver that uses the standard lib sql.DB as a pool.
func NewStandardDriver() Driver[*sql.DB] { return &standardDriver{} }

func (d *standardDriver) NewPool(pcfg *pgxpool.Config) (*sql.DB, error) {
	opts := []stdlib.OptionOpenDB{}
	if pcfg.BeforeConnect != nil {
		opts = append(opts, stdlib.OptionBeforeConnect(pcfg.BeforeConnect))
	}

	db := stdlib.OpenDB(*pcfg.ConnConfig, opts...)
	return db, nil
}

func (d *standardDriver) Close(db *sql.DB) error {
	return db.Close()
}

type pgxv5Driver struct{}

// NewPgxV5Driver inits a driver that uses the pgx v5 pooling.
func NewPgxV5Driver() Driver[*pgxpool.Pool] { return &pgxv5Driver{} }

func (d *pgxv5Driver) NewPool(pcfg *pgxpool.Config) (*pgxpool.Pool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	db, err := pgxpool.NewWithConfig(ctx, pcfg)
	return db, err
}

func (d *pgxv5Driver) Close(db *pgxpool.Pool) error {
	db.Close()
	return nil
}
