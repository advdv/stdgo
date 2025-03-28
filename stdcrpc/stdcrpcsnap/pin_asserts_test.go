package stdcrpcsnap_test

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/advdv/stdgo/stdcrpc/stdcrpcsnap"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type recordT struct {
	msg string
}

func (t *recordT) Errorf(format string, args ...interface{}) {
	t.msg = fmt.Sprintf(format, args...)
}

func TestAsserts(t *testing.T) {
	for idx, tt := range []struct {
		inp string
		ass stdcrpcsnap.OverwriteAssertFunc
		exp string
	}{
		{`"bogus"`, stdcrpcsnap.AssertUUID(), "invalid UUID length"},
		{`"bde4ad57-bdf6-44b3-98dc-ea95ac2c83d3"`, stdcrpcsnap.AssertUUID(), ""},
		{`"bogus"`, stdcrpcsnap.AssertRFC3339Nano(), "parsing time"},
		{`"2025-03-28T09:03:02.742002+01:00"`, stdcrpcsnap.AssertRFC3339Nano(), ""},
		{`100`, stdcrpcsnap.AssertStringOfLength(3), "should have 3 item"},
		{`"100"`, stdcrpcsnap.AssertStringOfLength(3), ""},
	} {
		t.Run(strconv.Itoa(idx), func(t *testing.T) {
			rec := recordT{}
			tt.ass(&rec, gjson.Parse(tt.inp))
			if tt.exp == "" {
				require.Empty(t, rec.msg)
			} else {
				require.Contains(t, rec.msg, tt.exp)
			}
		})
	}
}
