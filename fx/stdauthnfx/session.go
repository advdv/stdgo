package stdauthnfx

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
func (a *Authentication) endSession(resp http.ResponseWriter, _ *http.Request) error {
	// we're creating a cookie manually here, because the user should be able to logout even if they have
	// an invalid session that the library can't decode. Some server side configuration can cause this (key switching)
	// so we can't  fully blame the client and let it figure this out.
	cookie := http.Cookie{
		Name:     a.cfg.SessionCookieName,
		MaxAge:   -1,
		Path:     a.cfg.SessionCookiePath,
		SameSite: a.cfg.SessionCookieSameSite,
		Secure:   a.cfg.SessionCookieSecure,
		HttpOnly: a.cfg.SessionCookieHTTPOnly,
	}

	http.SetCookie(resp, &cookie)
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
				// we need the logout endpoint to be always accessible in case the cookie became invalid and the
				// user is trying to reset their session.
				if req.URL.Path == a.cfg.LogoutPath {
					logs.Info("invalid session but the client is logging out, anonymously", zap.Error(err))
					return next.ServeBareBHTTP(resp, req.WithContext(WithIdentity(req.Context(), Anonymous{})))
				}

				// at this point the client has ended up with an invalid session. It needs to logout and log back in.
				logs.Error("failed to continue session", zap.Error(err))
				return bhttp.NewError(bhttp.CodeBadRequest, errors.New("invalid session, logout and log back in"))
			}

			return next.ServeBareBHTTP(resp, req.WithContext(WithIdentity(req.Context(), idn)))
		})
	}
}
