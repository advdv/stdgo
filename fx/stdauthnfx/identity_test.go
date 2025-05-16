package stdauthnfx_test

import (
	"encoding/json"
	"testing"

	"github.com/advdv/stdgo/fx/stdauthnfx"
	"github.com/stretchr/testify/require"
)

func TestIdentity(t *testing.T) {
	t.Parallel()

	t.Run("anonymous", func(t *testing.T) {
		t.Parallel()

		require.PanicsWithValue(t, "stdauthnfx: anonymous identity should never be serialized", func() {
			json.Marshal(stdauthnfx.Anonymous{})
		})

		require.PanicsWithValue(t, "stdauthnfx: anonymous identity should never be deserialized", func() {
			var idn stdauthnfx.Anonymous
			json.Unmarshal([]byte(`{}`), &idn)
		})
	})

	t.Run("authenticated", func(t *testing.T) {
		t.Parallel()

		orig := stdauthnfx.NewAuthenticated("linkedin|-2190ddf", "person@example.com")

		b, err := json.Marshal(orig)
		require.NoError(t, err)

		var got stdauthnfx.Authenticated
		err = json.Unmarshal(b, &got)
		require.NoError(t, err)

		require.Equal(t, orig.Email(), got.Email())
		require.Equal(t, "person@example.com", got.Email())
		require.Equal(t, orig.ID(), got.ID())
		require.Equal(t, "linkedin|-2190ddf", got.ID())
	})
}
