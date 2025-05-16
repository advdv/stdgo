package stdauthnfx

import (
	"encoding/json"
	"fmt"
)

type Identity interface {
	isIdentity()
	fmt.Stringer
}

func IsAnonymous(idn Identity) bool {
	_, ok := idn.(Anonymous)
	return ok
}

func authenticatedIdentityFromJSON(data []byte) (idn Authenticated, err error) {
	if err := json.Unmarshal(data, &idn); err != nil {
		return idn, fmt.Errorf("unmarshal json: %w", err)
	}

	return idn, nil
}

// Authenticated repesent an authenticated identity. We know who this is.
//
//nolint:recvcheck
type Authenticated struct {
	data struct {
		Email string `json:"email"`
		ID    string `json:"id"`
	}
}

func NewAuthenticated(id string, email string) Authenticated {
	a := Authenticated{}
	a.data.ID = id
	a.data.Email = email
	return a
}

func (idn Authenticated) isIdentity()    {}
func (idn Authenticated) String() string { return idn.data.ID }
func (idn Authenticated) Email() string  { return idn.data.Email }
func (idn Authenticated) ID() string {
	return idn.data.ID
}

func (idn Authenticated) MarshalJSON() ([]byte, error) {
	return json.Marshal(idn.data)
}

func (idn *Authenticated) UnmarshalJSON(data []byte) error {
	return json.Unmarshal(data, &idn.data)
}

// Anonymous represents an identity that is not authenticated. We do not know who this is.
type Anonymous struct{}

func (idn Anonymous) MarshalJSON() ([]byte, error) {
	panic("stdauthnfx: anonymous identity should never be serialized")
}

func (idn Anonymous) UnmarshalJSON([]byte) error {
	panic("stdauthnfx: anonymous identity should never be deserialized")
}

func (idn Anonymous) String() string { return "<anonymous>" }
func (idn Anonymous) isIdentity()    {}

var (
	_ Identity = Authenticated{}
	_ Identity = Anonymous{}
)
