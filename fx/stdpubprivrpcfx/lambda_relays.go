package stdpubprivrpcfx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"

	"github.com/advdv/bhttp"
	"github.com/advdv/stdgo/stdctx"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// ProvideLambdaRelay can be used to init a lambda relay while adding it to the right fx group.
func ProvideLambdaRelay[C, E any](
	slug string,
	handler LambdaRelayHandler[C, E],
) fx.Option {
	return fx.Options(
		fx.Provide(fx.Annotate(func() *LambdaRelay {
			return NewLambdaRelay(slug, handler)
		}, fx.ResultTags(`group:"lambda_relays"`))),
	)
}

// NewLambdaRelay inits a lambda relay definition.
func NewLambdaRelay[C, E any](slug string, handler LambdaRelayHandler[C, E]) *LambdaRelay {
	return &LambdaRelay{
		// slug on which it will the relay will be mounted: eg /lamda/<name>
		Slug: slug,
		// create a bhttp handler that decodes into type E (a lambda event type).
		CreateHandlerFromSysClient: func(sys any) bhttp.HandlerFunc[context.Context] {
			c, ok := sys.(C)
			if !ok {
				// we do it at runtime because a type mismatch causes very hard to debug issues of
				// relays simply not appearing.
				typeOf := reflect.TypeOf(sys)
				panic(fmt.Sprintf("lambda relay %q: wrong sys client type: got %T:%v", slug, sys, typeOf))
			} else if sys == nil {
				panic(fmt.Sprintf("lambda relay %q: sys client was nil", slug))
			}

			// for every request, invoke the actual relay handler with the system client.
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
}

// LambdaRelay adds a http endpoint that decodes a json object and calls the system service. This is used
// to let the service handle lambda calls.
type LambdaRelay struct {
	Slug                       string
	CreateHandlerFromSysClient func(any) bhttp.HandlerFunc[context.Context]
}

type LambdaRelayHandler[C, E any] func(ctx context.Context, ev E, sys C) error
