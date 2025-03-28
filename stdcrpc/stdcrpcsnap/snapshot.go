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
	"github.com/advdv/stdgo/stdlo"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

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
// is created instead. It allows overwriting parts of the message with static values while asserting
// the replaced values precicely. This allows for part of the message to be dynanmic but still tightly
// asserted.
func SnapshotEq(tb testing.TB, actMsg []byte, actOverwrites ...Overwrite) {
	actMsg = ApplyOverwrite(tb, actMsg, actOverwrites...)

	expFilePath := filepath.Join("testdata", tb.Name()+".json")
	expMsg, err := os.ReadFile(expFilePath)
	if os.IsNotExist(err) {
		// in case the expected message json is not found, we assume it is a new test case so we write
		// the actual data into the file.
		require.NoError(tb, os.MkdirAll(filepath.Dir(expFilePath), 0o777), "mkdir: %s", expFilePath)
		require.NoError(tb, os.WriteFile(expFilePath, actMsg, 0o600), "write file: %s", expFilePath)
		expMsg = actMsg

		tb.Logf("created snapshot for test %s since it didn't exist: %s", tb.Name(), expFilePath)
	} else {
		require.NoError(tb, err, "read file: %s", err)
	}

	// finally, actually compare the json.
	require.JSONEqf(tb, string(expMsg), string(actMsg), "snapshot mismatch, actual JSON: %s",
		stdlo.Must1(formatJSONData(actMsg)))
}

func formatJSONData(data []byte) (string, error) {
	var dst bytes.Buffer
	if err := json.Indent(&dst, data, "", " "); err != nil {
		return "", fmt.Errorf("indent json data: %w", err)
	}

	return dst.String(), nil
}
