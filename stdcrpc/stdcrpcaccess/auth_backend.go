package stdcrpcaccess

// AuthBackend represents what is required of an auth backend.
type AuthBackend interface {
	JWKSEndpoint() string
}

// RealAuthBackend is used when actually deploying.
type RealAuthBackend string

func (ap RealAuthBackend) JWKSEndpoint() string { return string(ap) }
