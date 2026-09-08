package stdpgxfx

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// FinalPgxPoolConfig contains a pool's resolved configuration after its named
// deriver and host overrides or conventions have been applied. Provide exposes
// it under the same Fx name as the pool. Consumers must copy Config before use.
type FinalPgxPoolConfig struct {
	*pgxpool.Config
}

func provideFinalPoolConfig(name string) fx.Option {
	tag := fmt.Sprintf(`name:%q`, name)
	return fx.Provide(fx.Annotate(
		func(deriver Deriver, base *pgxpool.Config, cfg Config, logs *zap.Logger) FinalPgxPoolConfig {
			resolved := base.Copy()
			if deriver != nil {
				resolved = deriver(logs, resolved)
			}
			if !ApplyPoolHostOverride(name, resolved, cfg.Hosts, logs) {
				ApplyPoolHostConventions(name, resolved, logs)
			}
			return FinalPgxPoolConfig{Config: resolved}
		},
		fx.ParamTags(tag+` optional:"true"`),
		fx.ResultTags(tag),
	))
}
