package stdauthnfx

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/sessions"
)

func (a *Authentication) keepState(
	store sessions.Store,
	resp http.ResponseWriter,
	req *http.Request,
	redirectTo *url.URL,
) (state string, err error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	state = base64.RawURLEncoding.EncodeToString(buf[:])

	sess, err := store.Get(req, a.cfg.StateCookieName)
	if err != nil {
		return "", fmt.Errorf("failed to get auth state session: %w", err)
	}

	// the max age here effectively determines how long the user has to get from /login
	// to the callback. Likely pretty short but one might imagine the user going out to
	// look for their credentials or contact an admin to get access.
	sess.Options.MaxAge = a.cfg.StateMaxAgeSeconds
	sess.Values["state"] = state
	sess.Values["redirect_to"] = redirectTo.String()

	if err := sess.Save(req, resp); err != nil {
		return "", fmt.Errorf("failed to save auth state session: %w", err)
	}

	return state, nil
}

func (a *Authentication) verifyState(
	store sessions.Store, resp http.ResponseWriter, req *http.Request,
) (redirectTo *url.URL, err error) {
	got := req.URL.Query().Get("state")
	if got == "" {
		return nil, fmt.Errorf("state missing in callback")
	}

	state, err := store.Get(req, a.cfg.StateCookieName)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth state from session: %w", err)
	}

	want, ok := state.Values["state"].(string)
	if !ok || want == "" {
		return nil, fmt.Errorf("no state stored in session")
	}
	if got != want {
		return nil, fmt.Errorf("state mismatch")
	}

	redirStr, ok := state.Values["redirect_to"].(string)
	if !ok || redirStr == "" {
		return nil, fmt.Errorf("no redirect_to in session")
	}

	redirectTo, err = url.Parse(redirStr)
	if err != nil {
		return nil, fmt.Errorf("invalid redirect_url in session: %w", err)
	}

	state.Options.MaxAge = -1
	if err = state.Save(req, resp); err != nil {
		return nil, fmt.Errorf("failed to remove auth state: %w", err)
	}

	return redirectTo, nil
}
