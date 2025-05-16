package stdauthnfx

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/advdv/bhttp"
)

func (a *Authentication) validatedUserRedirect(req *http.Request) (*url.URL, error) {
	redirVal := req.URL.Query().Get("redirect_to")
	if redirVal == "" {
		return nil, bhttp.NewError(bhttp.CodeBadRequest, fmt.Errorf("no redirect_to query parameter"))
	}

	redirectTo, err := validatedUserRedirectURL(redirVal, a.cfg.AllowedRedirectHosts)
	if err != nil {
		return nil, err
	}

	return redirectTo, nil
}

func validatedUserRedirectURL(str string, whitelist []string) (*url.URL, error) {
	redirectTo, err := url.Parse(str)
	if err != nil {
		return nil, bhttp.NewError(bhttp.CodeBadRequest, fmt.Errorf("invalid redirect_to value: %w", err))
	}

	if redirectTo.Host == "" && redirectTo.Scheme == "" {
		return redirectTo, nil
	}

	if redirectTo.Host == "" || redirectTo.Scheme != "https" {
		return nil, bhttp.NewError(bhttp.CodeBadRequest, errors.New("invalid redirect_to: must be https and have a host"))
	}

	if !slices.Contains(whitelist, redirectTo.Host) {
		return nil, bhttp.NewError(bhttp.CodeBadRequest, errors.New("invalid redirect_to: host not allowed"))
	}

	return redirectTo, nil
}
