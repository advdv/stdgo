package stdenttypeid_test

import (
	"testing"

	"github.com/advdv/stdgo/stdent/stdenttypeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// knownSuffix is the printable base32 form this package produces
// for UUID `01890a5d-ac96-774b-bdca-add6c8bee2c4` under the
// left-padded 130-bit Crockford encoding [encodeUUIDBase32]
// implements (which mirrors the companion SQL `base32_encode()`
// helper, not the canonical jetify TypeID spec — those use a
// different bit-slicing and would produce a different suffix).
const knownSuffix = "upld_01h455vb4pex5vvjndtv4bxrp4"

func TestID_String(t *testing.T) {
	t.Parallel()

	assert.Equal(t, knownSuffix, stdenttypeid.ID(knownSuffix).String())
	assert.Empty(t, stdenttypeid.ID("").String())
}

func TestID_Value(t *testing.T) {
	t.Parallel()

	v, err := stdenttypeid.ID(knownSuffix).Value()
	require.NoError(t, err)
	assert.Equal(t, knownSuffix, v)
}

func TestID_FormatParam(t *testing.T) {
	t.Parallel()

	id := stdenttypeid.ID(knownSuffix)

	assert.Equal(t, "public.typeid_parse($1)", id.FormatParam("$1", nil))
	assert.Equal(t, "public.typeid_parse($42)", id.FormatParam("$42", nil))
}

func TestID_Scan_PrintableString(t *testing.T) {
	t.Parallel()

	var id stdenttypeid.ID
	require.NoError(t, id.Scan(knownSuffix))
	assert.Equal(t, stdenttypeid.ID(knownSuffix), id)
}

func TestID_Scan_PrintableBytes(t *testing.T) {
	t.Parallel()

	var id stdenttypeid.ID
	require.NoError(t, id.Scan([]byte(knownSuffix)))
	assert.Equal(t, stdenttypeid.ID(knownSuffix), id)
}

// TestID_Scan_CompositeLiteral pins the Go-side composite-literal
// normalisation against [knownSuffix]: the printable form
// [encodeUUIDBase32] derives from UUID
// `01890a5d-ac96-774b-bdca-add6c8bee2c4`. Consumers that pair this
// package with a Postgres `typeid_print()` are expected to pin the
// same vector end-to-end in their own integration tests.
func TestID_Scan_CompositeLiteral(t *testing.T) {
	t.Parallel()

	var id stdenttypeid.ID
	require.NoError(t, id.Scan("(upld,01890a5d-ac96-774b-bdca-add6c8bee2c4)"))
	assert.Equal(t, stdenttypeid.ID(knownSuffix), id)
}

func TestID_Scan_CompositeLiteralAsBytes(t *testing.T) {
	t.Parallel()

	var id stdenttypeid.ID
	require.NoError(t, id.Scan([]byte("(upld,01890a5d-ac96-774b-bdca-add6c8bee2c4)")))
	assert.Equal(t, stdenttypeid.ID(knownSuffix), id)
}

// TestID_Scan_AllZeroUUID exercises the boundary where every 5-bit
// group is 0; the printable suffix is 26 `0`s.
func TestID_Scan_AllZeroUUID(t *testing.T) {
	t.Parallel()

	var id stdenttypeid.ID
	require.NoError(t, id.Scan("(zero,00000000-0000-0000-0000-000000000000)"))
	assert.Equal(t, stdenttypeid.ID("zero_00000000000000000000000000"), id)
}

// TestID_Scan_AllOnesUUID exercises the opposite boundary: every
// data bit set. The 128 data bits are left-padded with 2 zero
// bits up to 130 bits, so the leading 5-bit group is `00111` = 7
// and the remaining 25 groups are all `11111` = 31 = `z`. This
// pins the documented invariant that the leading character can
// only ever take a value in 0..7.
func TestID_Scan_AllOnesUUID(t *testing.T) {
	t.Parallel()

	var id stdenttypeid.ID
	require.NoError(t, id.Scan("(full,ffffffff-ffff-ffff-ffff-ffffffffffff)"))
	assert.Equal(t, stdenttypeid.ID("full_7zzzzzzzzzzzzzzzzzzzzzzzzz"), id)
}

func TestID_Scan_Nil(t *testing.T) {
	t.Parallel()

	id := stdenttypeid.ID("upld_01h455vb4pex5vsknk084sn02q")
	require.NoError(t, id.Scan(nil))
	assert.Empty(t, id)
}

func TestID_Scan_UnsupportedType(t *testing.T) {
	t.Parallel()

	var id stdenttypeid.ID
	err := id.Scan(42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot scan int")
}

func TestID_Scan_MissingClosingParen(t *testing.T) {
	t.Parallel()

	var id stdenttypeid.ID
	err := id.Scan("(upld,01890a5d-ac96-774b-bdca-add6c8bee2c4")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid composite literal")
}

func TestID_Scan_MissingComma(t *testing.T) {
	t.Parallel()

	var id stdenttypeid.ID
	err := id.Scan("(upld01890a5dac96774bbdcaaddc8bee2c4)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid composite literal")
}

func TestID_Scan_EmptyPrefix(t *testing.T) {
	t.Parallel()

	var id stdenttypeid.ID
	err := id.Scan("(,01890a5d-ac96-774b-bdca-add6c8bee2c4)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty prefix")
}

func TestID_Scan_InvalidUUID(t *testing.T) {
	t.Parallel()

	var id stdenttypeid.ID
	err := id.Scan("(upld,not-a-uuid)")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse uuid")
}
