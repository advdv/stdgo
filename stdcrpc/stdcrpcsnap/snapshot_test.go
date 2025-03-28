package stdcrpcsnap_test

import (
	"testing"
	"time"

	"github.com/advdv/stdgo/stdcrpc/stdcrpcsnap"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"
)

func TestSnapshotEqWithWildcards(t *testing.T) {
	msg := []byte(`{"items":[
		{"foo":{"created_at":""}},{},
		{"bar":{"created_at":""}},{}	
		]}`)

	msg, err := sjson.SetBytes(msg, "items.0.foo.created_at", time.Now())
	require.NoError(t, err)
	msg, err = sjson.SetBytes(msg, "items.2.bar.created_at", time.Now())
	require.NoError(t, err)

	stdcrpcsnap.SnapshotEq(t, msg,
		stdcrpcsnap.PinResponseValue("items.#.*.created_at", "2025-03-28T09:03:02.742002+01:00",
			stdcrpcsnap.AssertRFC3339Nano()))
}

func TestSnapshotEqNoWildcards(t *testing.T) {
	msg := []byte(`{"items":[
		{"foo":{"created_at":""}},{}
		]}`)

	msg, err := sjson.SetBytes(msg, "items.0.foo.created_at", time.Now())
	require.NoError(t, err)

	stdcrpcsnap.SnapshotEq(t, msg,
		stdcrpcsnap.PinResponseValue("items.0.foo.created_at", "2025-03-28T09:03:02.742002+01:00",
			stdcrpcsnap.AssertRFC3339Nano()))
}
