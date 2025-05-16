package stdauthnfx

import (
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type ProviderKind int

const (
	ProviderKindUnknown ProviderKind = iota
	ProviderKindLinkedIn
	ProviderKindGoogle
	ProviderKindMicrosoft
)

func (pk ProviderKind) String() string {
	switch pk {
	case ProviderKindGoogle:
		return "google"
	case ProviderKindLinkedIn:
		return "linkedin"
	case ProviderKindMicrosoft:
		return "microsoft"
	case ProviderKindUnknown:
		fallthrough
	default:
		return "<unknown>"
	}
}

// Provider is what the provider.
type Provider interface {
	Kind() ProviderKind
	OAuth() *oauth2.Config
	OIDC() *oidc.Provider
}

type provider struct {
	kind  ProviderKind
	oidc  *oidc.Provider
	oauth *oauth2.Config
}

func (p provider) OAuth() *oauth2.Config { return p.oauth }
func (p provider) OIDC() *oidc.Provider  { return p.oidc }
func (p provider) Kind() ProviderKind    { return p.kind }
