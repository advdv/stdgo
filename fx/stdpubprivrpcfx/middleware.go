package stdpubprivrpcfx

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/advdv/bhttp"
	"go.uber.org/zap"
)

// errorMiddleware will handle errors from the bhttp package. It will print client errors but not print server errors.
func errorMiddleware(logs *zap.Logger) func(next bhttp.BareHandler) bhttp.BareHandler {
	return func(next bhttp.BareHandler) bhttp.BareHandler {
		return bhttp.BareHandlerFunc(func(w bhttp.ResponseWriter, r *http.Request) error {
			if err := next.ServeBareBHTTP(w, r); err != nil {
				var herr *bhttp.Error
				if errors.As(err, &herr) && herr.Code() >= 400 && herr.Code() < 500 {
					w.Reset()
					http.Error(w, fmt.Sprintf(`{"message":"%s"}`, err.Error()), int(herr.Code()))
					return nil
				}

				logs.Error("server error", zap.Error(err))
				w.Reset() // undo any writes that have happened
				http.Error(w, `{"message":"server error"}`, http.StatusInternalServerError)

				return nil
			}

			return nil
		})
	}
}
