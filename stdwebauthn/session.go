package stdwebauthn

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/advdv/bhttp"
	"github.com/advdv/stdgo/stdctx"
	"go.uber.org/zap"
)

// startSession will start the user session by creating a cookie.
func (a *Authentication) startSession(idn Identity, resp http.ResponseWriter, req *http.Request) error {
	sess, err := a.sessions.Get(req, a.cfg.SessionCookieName)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	data, err := json.Marshal(idn)
	if err != nil {
		return fmt.Errorf("marshal identity json: %w", err)
	}

	sess.Values["identity"] = data
	if err := sess.Save(req, resp); err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	return nil
}

// startSession will end the user session by removing the cookie.
func (a *Authentication) endSession(resp http.ResponseWriter, req *http.Request) error {
	sess, err := a.sessions.Get(req, a.cfg.SessionCookieName)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	sess.Options.MaxAge = -1
	if err := sess.Save(req, resp); err != nil {
		return fmt.Errorf("save session: %w", err)
	}

	return nil
}

func (a *Authentication) continueSession(req *http.Request) (Identity, error) {
	sess, err := a.sessions.Get(req, a.cfg.SessionCookieName)
	if err != nil {
		return nil, fmt.Errorf("get session from request: %w", err)
	}

	// when the session is new, it means it disn't exist so we're dealing with a
	// unauthenticated (anonymous) request. Other layers (authZ) should decide what to do
	// at this point.
	if sess.IsNew {
		return Anonymous{}, nil
	}

	// else, we assume that there is an identity in the session.
	data, ok := sess.Values["identity"].([]byte)
	if !ok {
		return nil, fmt.Errorf("invalid session, no identity value")
	}

	idn, err := authenticatedIdentityFromJSON(data)
	if err != nil {
		return nil, fmt.Errorf("invalid session, unmarshal identity json: %w", err)
	}

	return idn, nil
}

// SessionMiddleware provides the middleware that reads the session information for every request
// that passes through the server.
func (a *Authentication) SessionMiddleware() bhttp.Middleware {
	return func(next bhttp.BareHandler) bhttp.BareHandler {
		return bhttp.BareHandlerFunc(func(resp bhttp.ResponseWriter, req *http.Request) error {
			logs := stdctx.Log(req.Context())

			idn, err := a.continueSession(req)
			if err != nil {
				if endErr := a.endSession(resp, req); endErr != nil {
					logs.Error("failed to end session after error in continuing session", zap.Error(endErr))
				}

				logs.Error("failed to continue session", zap.Error(err))

				return bhttp.NewError(bhttp.CodeBadRequest, errors.New("invalid session"))
			}

			return next.ServeBareBHTTP(resp, req.WithContext(WithIdentity(req.Context(), idn)))
		})
	}
}
