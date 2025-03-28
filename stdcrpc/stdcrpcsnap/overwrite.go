package stdcrpcsnap

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Overwrite allows a test case to overwrite part of the actual message before it is compared with
// the snapshot. This is useful for asserting data that changes by definition. It is a last-resort
// option and making sure the data is stable to begin with is preferred.
type Overwrite struct {
	Path   string
	Value  any
	Assert OverwriteAssertFunc
}

// OverwriteAssertFunc allows asserting overwitten values.
type OverwriteAssertFunc func(t assert.TestingT, v gjson.Result)

// ApplyOverwrite returns a version of the message where the overwrites are applied and
// assertions checked.
func ApplyOverwrite(tb require.TestingT, actMsg []byte, actOverwrites ...Overwrite) []byte {
	var err error
	for _, actOverwrite := range actOverwrites {
		actVal := gjson.GetBytes(actMsg, actOverwrite.Path)
		// if actOverwrite.Assert != nil {
		// 	if len(actVal.Paths(string(actMsg))) == 0 {
		// 		actOverwrite.Assert(tb, actVal)
		// 	} else {
		// 		for _, res := range actVal.Array() {
		// 			actOverwrite.Assert(tb, res)
		// 		}
		// 	}
		// }

		// if tb.Failed() {
		// 	tb.Logf("overwrite assert failed, actual JSON: %s",
		// 		stdlo.Must1(formatJSONData(actMsg)))
		// }

		// in case the paths is NOT a wildcared path, the Paths actually returns an empty slice.
		paths := actVal.Paths(string(actMsg))
		if len(paths) == 0 {
			p := actVal.Path(string(actMsg))
			if p != "" && p != "@this" { // capture some not found cases.
				paths = append(paths, p)
			}
		}

		// fo paths we actually found, perform assertion.
		if actOverwrite.Assert != nil {
			for _, path := range paths {
				v := gjson.Get(string(actMsg), path)
				actOverwrite.Assert(tb, v)
			}
		}

		// a path may match multiple, we set the pinned value for each.
		for _, pathForSet := range paths {
			actMsg, err = sjson.SetBytes(actMsg, pathForSet, actOverwrite.Value)
			require.NoError(tb, err, "set bytes for path: %s", pathForSet)
		}
	}

	return actMsg
}
