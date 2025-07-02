package stdhttpware

import (
	"net/http"
	"net/url"
	"slices"
	"strings"

	connectcors "connectrpc.com/cors"
	"github.com/advdv/stdgo/stdctx"
	"github.com/rs/cors"
	"go.uber.org/zap"
)

// RootDomainOfHost returns the "root" part of the host provided.
func RootDomainOfHost(h string) string {
	parts := strings.Split(h, ".")
	root := parts[max(0, len(parts)-2):]
	return strings.Join(root, ".")
}

// NewConnectCORSMiddleware initializes the CORS middleware. In our case CORS is allowed if the origin of the request is
// on the same root domain as the requests host, i.e: it is a first party request.
func NewConnectCORSMiddleware(maxAgeSeconds int, whiteList ...string) func(http.Handler) http.Handler {
	corsh := cors.New(cors.Options{
		AllowOriginVaryRequestFunc: func(r *http.Request, origin string) (bool, []string) {
			if slices.Contains(whiteList, origin) {
				return true, nil
			}

			originURL, err := url.Parse(origin)
			if err != nil {
				stdctx.Log(r.Context()).Info("invalid origin header received",
					zap.Error(err), zap.String("origin", origin))

				return false, nil
			}

			return RootDomainOfHost(r.Host) == RootDomainOfHost(originURL.Host), nil
		},
		AllowedMethods: connectcors.AllowedMethods(),
		AllowedHeaders: append(connectcors.AllowedHeaders(),
			"Authorization", "Cookie"),
		ExposedHeaders:   connectcors.ExposedHeaders(),
		AllowCredentials: true,
		MaxAge:           maxAgeSeconds,
	})

	return corsh.Handler
}
