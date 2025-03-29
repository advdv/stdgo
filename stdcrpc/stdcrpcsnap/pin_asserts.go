package stdcrpcsnap

import (
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

// AssertRFC3339Nano is a re-usable check to assert overwritten values.
func AssertRFC3339Nano() OverwriteAssertFunc {
	return func(t assert.TestingT, v gjson.Result) {
		exp, err := time.Parse(time.RFC3339Nano, v.Str)
		assert.False(t, exp.IsZero(), "got: %v", v)
		assert.NoError(t, err, "got: %v", v)
	}
}

// AssertUUID is a re-usable check to assert overwritten values.
func AssertUUID() OverwriteAssertFunc {
	return func(t assert.TestingT, v gjson.Result) {
		assert.NoError(t, uuid.Validate(v.Str), "got: %v", v)
	}
}

// AssertStringOfLength is a re-usable check to assert just length values.
func AssertStringOfLength(n int) OverwriteAssertFunc {
	return func(t assert.TestingT, v gjson.Result) {
		assert.Len(t, v.Str, n, "got: %v", v)
	}
}
