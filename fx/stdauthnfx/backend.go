package stdauthnfx

import (
	"context"
	"fmt"

	"github.com/advdv/stdgo/stdctx"
	"github.com/coreos/go-oidc/v3/oidc"
	"go.uber.org/zap"
)

// Backend implements an authentication backend.
type Backend interface {
	AuthenticateCode(
		ctx context.Context,
		provider Provider,
		code string,
	) (Identity, error)
}

// realBackend implements an authentication backend for real.
type realBackend struct{}

func (b *realBackend) AuthenticateCode(
	ctx context.Context,
	provider Provider,
	code string,
) (Identity, error) {
	tok, err := provider.OAuth().Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("exchange code for token: %w", err)
	}

	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("no id_token claim")
	}

	stdctx.Log(ctx).Info("verifying id token",
		zap.String("raw_id_token", rawIDToken),
		zap.String("auth_url", provider.OIDC().Endpoint().AuthURL))

	verifier := provider.OIDC().Verifier(&oidc.Config{ClientID: provider.OAuth().ClientID})
	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("verify id token: %w", err)
	}

	var claims struct {
		Email string `json:"email"`
		OID   string `json:"oid"`
	}

	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("unmarshal id token claims: %w", err)
	}

	// For most provideres the sub claim is the one to use for our identity id but in case of microsoft
	// the OID is preferred because it is stable between oauth clients. Not totally clear if it is always returned
	// with the profile scope though.
	providerID := idToken.Subject
	if provider.Kind() == ProviderKindMicrosoft && claims.OID != "" {
		providerID = claims.OID
	}

	return NewAuthenticated(
		fmt.Sprintf("%s|%s", provider.Kind(), providerID),
		claims.Email,
	), nil
}

type fixedIdentityBackend struct {
	identity Identity
}

func NewFixedIdentityBackend(idn Identity) Backend {
	return fixedIdentityBackend{idn}
}

func (b fixedIdentityBackend) AuthenticateCode(
	_ context.Context,
	_ Provider,
	_ string,
) (Identity, error) {
	return b.identity, nil
}
