package stdpubprivrpcfx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/advdv/bhttp"
	"github.com/advdv/stdgo/stdctx"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

func ProvideLambdaRelay[C, E any](
	slug string,
	handler LambdaRelayHandler[C, E],
) fx.Option {
	return fx.Options(
		fx.Provide(fx.Annotate(func() *LambdaRelay[C] {
			return &LambdaRelay[C]{
				// slug on which it will the relay will be mounted: eg /lamda/<name>
				Slug: slug,
				// create a bhttp handler that decodes into type E (a lambda event type).
				CreateHandlerFromSysClient: func(c C) bhttp.HandlerFunc[context.Context] {
					return func(ctx context.Context, w bhttp.ResponseWriter, r *http.Request) error {
						var ev E
						if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
							if errors.Is(err, io.EOF) {
								return bhttp.NewError(bhttp.CodeBadRequest, errors.New("no request body"))
							}
						}

						stdctx.Log(ctx).Info("lambda relay", zap.Any("event", ev))
						if err := handler(ctx, ev, c); err != nil {
							return fmt.Errorf("lambda relay handling failed: %w", err)
						}

						fmt.Fprintf(w, `{}`) // just something to return.

						return nil
					}
				},
			}
		}, fx.ResultTags(`group:"lambda_relays"`))),
	)
}

type LambdaRelay[C any] struct {
	Slug                       string
	CreateHandlerFromSysClient func(C) bhttp.HandlerFunc[context.Context]
}

type LambdaRelayHandler[C, E any] func(ctx context.Context, ev E, sys C) error
