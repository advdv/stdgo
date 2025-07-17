package stdpgxtxfx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/advdv/bhttp"
	"github.com/advdv/stdgo/stdctx"
	"github.com/advdv/stdgo/stdtx"
	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// BHJTx creates an bhttp handler that transacts on json request and response.
func BHJTx[C bhttp.Context, I, O any](
	txr *stdtx.Transactor[pgx.Tx],
	fn func(context.Context, *zap.Logger, pgx.Tx, *I) (*O, error),
) bhttp.HandlerFunc[C] {
	return func(ctx C, w bhttp.ResponseWriter, r *http.Request) error {
		return HJTx(ctx, txr, w, r, fn)
	}
}

// HJTx transacts while encoding and decoding a buffered JSON HTTP request/response.
func HJTx[I, O any](
	ctx context.Context,
	txr *stdtx.Transactor[pgx.Tx],
	resp http.ResponseWriter,
	req *http.Request,
	fn func(context.Context, *zap.Logger, pgx.Tx, *I) (*O, error),
) error {
	var inp I
	if err := json.NewDecoder(req.Body).Decode(&inp); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode request: %w", err)
	}

	out, err := stdtx.Transact1(ctx, txr,
		func(ctx context.Context, tx pgx.Tx) (*O, error) {
			return fn(ctx, stdctx.Log(ctx), tx, &inp)
		})
	if err != nil {
		return txError(ctx, err)
	}

	if err := json.NewEncoder(resp).Encode(out); err != nil {
		return fmt.Errorf("encode response: %w", err)
	}

	return nil
}
