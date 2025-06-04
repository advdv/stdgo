package stdriverfx

import (
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	slogzap "github.com/samber/slog-zap/v2"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"riverqueue.com/riverui"
)

// NewUIServer inits a River UI server.
func NewUIServer(par struct {
	fx.In
	Config
	RiverConfig river.Config
	RUI         *pgxpool.Pool `name:"rui"` // dedicated pool so we can use a different schema (search_path)
	Logs        *zap.Logger
},
) (*riverui.Server, error) {
	client, err := river.NewClient(riverpgxv5.New(par.RUI), &par.RiverConfig)
	if err != nil {
		return nil, fmt.Errorf("init regular client for River UI: %w", err)
	}

	slogs := slog.New(slogzap.Option{
		Logger: par.Logs.Named("ui"),
	}.NewZapHandler())

	return riverui.NewServer(&riverui.ServerOpts{
		Client: client,
		DB:     par.RUI,
		Logger: slogs,
	})
}
