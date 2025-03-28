package stdcrpcsnap_test

import (
	"strconv"
	"testing"

	"github.com/advdv/stdgo/stdcrpc/stdcrpcsnap"
	"github.com/stretchr/testify/require"
)

type recordTB struct{ recordT }

func (recordTB) FailNow() {}

func TestOverwrite(t *testing.T) {
	for idx, tt := range []struct {
		inp    string
		exp    string
		ovr    stdcrpcsnap.Overwrite
		expErr string
	}{
		// if the key doesn't exist, don't overwrite it, don't assert.
		{
			`{}`, `{}`,
			stdcrpcsnap.PinResponseValue("foo", "bar", stdcrpcsnap.AssertRFC3339Nano()),
			"",
		},
		// if they DOES exist, DO overwrite it.
		{`{"foo":"dar"}`, `{"foo":"bar"}`, stdcrpcsnap.PinResponseValue("foo", "bar"), ""},
		// if they DOES exist, also assert it.
		{
			`{"foo":"dar"}`, `{"foo":"bar"}`,
			stdcrpcsnap.PinResponseValue("foo", "bar", stdcrpcsnap.AssertUUID()),
			"invalid UUID",
		},
		// wildcard overwrite operwrite with assert.
		{
			`{"items":[{},{"foo":"dar"},{"foo":"abc"}]}`, `{"items":[{},{"foo":"bar"},{"foo":"bar"}]}`,
			stdcrpcsnap.PinResponseValue("items.#.foo", "bar",
				stdcrpcsnap.AssertStringOfLength(3)),
			"",
		},
		// wildcard overwrite with failing assert.
		{
			`{"items":[{},{"foo":"dar"},{"foo":123}]}`, `{"items":[{},{"foo":"bar"},{"foo":"bar"}]}`,
			stdcrpcsnap.PinResponseValue("items.#.foo", "bar",
				stdcrpcsnap.AssertStringOfLength(3)),
			"should have 3 item",
		},
	} {
		t.Run(strconv.Itoa(idx), func(t *testing.T) {
			rec := &recordTB{recordT{}}
			act := stdcrpcsnap.ApplyOverwrite(rec, []byte(tt.inp), tt.ovr)
			require.JSONEq(t, tt.exp, string(act))

			if tt.expErr == "" {
				require.Empty(t, rec.msg)
			} else {
				require.Contains(t, rec.msg, tt.expErr)
			}
		})
	}
}
