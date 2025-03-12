package stdcrpcaccess

// AuthBackend represents and OIDC service that we don't control the signing process of.
type AuthBackend interface {
	JWKSEndpoint() string
}

// RealAuthBackend is used when actually deploying.
type RealAuthBackend string

func (ap RealAuthBackend) JWKSEndpoint() string { return string(ap) }
