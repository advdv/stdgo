package stdpubprivrpcfx

import (
	"context"
	"fmt"
	"net/http"

	"github.com/advdv/bhttp"
)

type HealthCheck func(ctx context.Context, r *http.Request, isPrivate bool) error

func healthz(cfg Config, hc HealthCheck, isPrivate bool) bhttp.HandlerFunc[context.Context] {
	return func(ctx context.Context, w bhttp.ResponseWriter, r *http.Request) error {
		if r.URL.Query().Get("force_panic") != "" && cfg.AllowForcedPanics {
			panic("forced panic")
		}

		if err := hc(ctx, r, isPrivate); err != nil {
			return bhttp.NewError(bhttp.CodePreconditionFailed, err)
		}

		_, err := fmt.Fprintln(w, http.StatusText(http.StatusOK))

		return err
	}
}
