package stdwebauthn

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckRedirect(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		str    string
		exp    string
		expErr string
	}{
		{"/", "/", ""},
		{"https://", "", "Bad Request: invalid redirect_to: must be https and have a host"},
		{"http://foo", "", "Bad Request: invalid redirect_to: must be https and have a host"},
		{"https://bar", "https://bar", "Bad Request: invalid redirect_to: host not allowed"},
		{"https://example.com", "https://example.com", ""},
	} {
		t.Run(tt.str, func(t *testing.T) {
			t.Parallel()
			act, err := validatedUserRedirectURL(tt.str, []string{"a.com", "example.com"})
			if tt.expErr == "" {
				require.NoError(t, err)
				require.Equal(t, tt.exp, act.String())
			} else {
				require.Error(t, err, tt.expErr)
				require.Nil(t, act)
			}
		})
	}
}
