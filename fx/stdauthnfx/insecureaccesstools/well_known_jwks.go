package insecureaccesstools

import _ "embed"

// WellKnownJWKS1 is a non-secret set of JWT Keys that can be used for testing.
//
//go:embed well_known_jwks1.json
var WellKnownJWKS1 []byte

// WellKnownJWKS1KeyID is the key id used for testing.
var WellKnownJWKS1KeyID = "key2"
