// Package stdcrpcsnap provides snapshot testing for Connect RPC response.
package stdcrpcsnap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
type OverwriteAssertFunc func(tb assert.TestingT, v gjson.Result)

// PinResponseValue is a helper that allows for dealing with response values that are not static so
// cannot be predicted for the snapshot value. The optional 'assertActual' can still assert the actual
// value even though it wouldn't match the snapshot.
func PinResponseValue(atPath string, toValue any, assertActual ...OverwriteAssertFunc) Overwrite {
	if len(assertActual) > 0 {
		return Overwrite{atPath, toValue, assertActual[0]}
	}

	return Overwrite{atPath, toValue, nil}
}

// MessageSnapshotEq compares the protobuf message against a snapshot. If the snapshot file doesn't exist it
// is created instead.
func MessageSnapshotEq(tb testing.TB, msg proto.Message, actOverwrites ...Overwrite) {
	actMsg, err := protojson.Marshal(msg)
	require.NoError(tb, err)
	SnapshotEq(tb, actMsg, actOverwrites...)
}

// ResponseSnapshotEq asserts that the snapshot equals the RPC response.
func ResponseSnapshotEq[O any](
	tb testing.TB, resp *connect.Response[O], err error, actOverwrites ...Overwrite,
) {
	var connErr *connect.Error
	var actMsg []byte

	if errors.As(err, &connErr) {
		rec := httptest.NewRecorder()
		require.NoError(tb, connect.NewErrorWriter().Write(rec, &http.Request{}, err))

		actMsg = rec.Body.Bytes()
	} else {
		require.NoError(tb, err)

		pmsg, ok := any(resp.Msg).(proto.Message)
		require.True(tb, ok)

		actMsg, err = protojson.Marshal(pmsg)
		require.NoError(tb, err)
	}

	SnapshotEq(tb, actMsg, actOverwrites...)
}

// SnapshotEq compares the protobuf message against a snapshot. If the snapshot file doesn't exist it
// is created instead.
func SnapshotEq(tb testing.TB, actMsg []byte, actOverwrites ...Overwrite) {
	fmtActMsg, err := formatJSONData(actMsg)
	require.NoError(tb, err)

	for _, actOverwrite := range actOverwrites {
		actVal := gjson.GetBytes(actMsg, actOverwrite.Path)
		if actOverwrite.Assert != nil {
			actOverwrite.Assert(tb, actVal)
		}

		if tb.Failed() {
			tb.Logf("overwrite assert failed, actual JSON: %s", fmtActMsg)
		}

		actMsg, err = sjson.SetBytes(actMsg, actOverwrite.Path, actOverwrite.Value)
		require.NoError(tb, err)
	}

	expFilePath := filepath.Join("testdata", tb.Name()+".json")
	expMsg, err := os.ReadFile(expFilePath)
	if os.IsNotExist(err) {
		// in case the expected message json is not found, we assume it is a new test case so we write
		// the actual data into the file.
		require.NoError(tb, os.MkdirAll(filepath.Dir(expFilePath), 0o777))
		require.NoError(tb, os.WriteFile(expFilePath, actMsg, 0o600))
		expMsg = actMsg

		tb.Logf("created snapshot for test %s since it didn't exist: %s", tb.Name(), expFilePath)
	} else {
		require.NoError(tb, err)
	}

	require.JSONEqf(tb, string(expMsg), string(actMsg), "snapshot mismatch, actual JSON: %s", fmtActMsg)
}

func formatJSONData(data []byte) (string, error) {
	var dst bytes.Buffer
	if err := json.Indent(&dst, data, "", " "); err != nil {
		return "", fmt.Errorf("indent json data: %w", err)
	}

	return dst.String(), nil
}
