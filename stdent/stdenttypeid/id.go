// Package stdenttypeid is the Go-side companion to a Postgres `typeid`
// composite type. It carries a typeid value as its canonical
// *printable* form (e.g. `upld_01j8...`) on the Go side while
// letting ent / pgx round-trip it transparently against a
// `public.typeid` column.
//
// Why this exists: the Postgres `typeid` is a composite type of
// the shape `(prefix text, uuid uuid)`. pgx encodes a Go string
// parameter as an anonymous record, which Postgres rejects with
// `input of anonymous composite types is not implemented
// (SQLSTATE 0A000)` for every `WHERE id = $1` / `DELETE WHERE
// id = $1` query. Reads surface as the raw composite literal
// `(upld,019e...)` rather than the printable typeid form a wire
// API wants.
//
// [ID] solves both halves at the ent column-type layer:
//
//   - [ID.Scan] normalises whatever Postgres returns (either the
//     composite literal `(prefix,uuid)` or an already-printable
//     `prefix_base32suffix`) into the printable form. The Go-side
//     value of a typeid column is therefore always the same shape a
//     wire API consumes.
//
//   - [ID.FormatParam] makes ent's SQL builder render the parameter
//     placeholder as `public.typeid_parse($N)` so Postgres receives
//     a real `typeid` expression, not an anonymous composite. Every
//     ent predicate that takes a typeid value (IDEQ, IDGT, IDIn,
//     DeleteOneID, …) inherits the wrap for free.
//
//   - [ID.Value] passes the printable string through to
//     `typeid_parse()` unchanged.
//
// The base32 alphabet mirrors the Crockford-style alphabet
// (`0123456789abcdefghjkmnpqrstvwxyz`) that the companion SQL
// `typeid_print()` / `base32_encode()` functions use EXACTLY so
// the Go-side normalisation produces the same string `typeid_print()`
// does for the same composite — round-trip tests against a known
// vector pin this end-to-end.
//
// The matching Postgres-side schema (the `typeid` composite, the
// `typeid_parse()` / `typeid_print()` / `typeid_generate()`
// functions, and the Crockford `base32_encode()` helper) is the
// caller's responsibility; this package is the Go-side adapter
// only.
package stdenttypeid

import (
	"database/sql/driver"
	"math/big"
	"strings"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
)

// base32Alphabet is the Crockford-style alphabet (digits + lowercase
// letters minus `i`, `l`, `o`, `u`) used by the SQL `base32_encode()`
// helper this package mirrors. Any drift here silently corrupts ids;
// a round-trip test pins it against a known vector.
const base32Alphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// ID is the Go-side companion to a Postgres `typeid` composite. The
// zero value is the empty string; non-empty values are always in
// printable form (`prefix_base32suffix`).
//
// Use ID via ent's `field.String("...").GoType(stdenttypeid.ID(""))`
// declaration; ent will hand out and accept ID values everywhere
// the column appears (predicates, builders, mutations, edges that
// reuse the column via `.Field("...")`).
//
// Receiver mix is deliberate: [ID.Value], [ID.String] and
// [ID.FormatParam] are value receivers because ent passes the ID by
// value into its SQL builder (where the interface assertions live),
// and [ID.Scan] is a pointer receiver because the
// [database/sql.Scanner] contract requires it. This is the same
// pattern github.com/google/uuid uses; the recvcheck linter is
// silenced at the type declaration only.
type ID string //nolint:recvcheck // sql.Scanner needs pointer; ent calls Value/FormatParam on value.

// String returns the printable form of the id.
func (id ID) String() string { return string(id) }

// Value implements [database/sql/driver.Valuer]. The driver sees
// the printable string; [ID.FormatParam] ensures Postgres receives
// it via `public.typeid_parse($N)`.
func (id ID) Value() (driver.Value, error) {
	return string(id), nil
}

// FormatParam implements ent's [entsql.ParamFormatter] interface,
// which ent's SQL builder calls for every parameter that is bound
// from this Go type. Wrapping the placeholder with
// `public.typeid_parse(...)` makes Postgres convert the bound text
// into a real `typeid` composite before evaluating the predicate.
// The `public` schema is named explicitly so the rewrite is not
// search_path-dependent (per-tenant roles often have search_path
// pinned away from public; the function is expected to live in
// public).
func (id ID) FormatParam(placeholder string, _ *entsql.StmtInfo) string {
	return "public.typeid_parse(" + placeholder + ")"
}

// Scan implements [database/sql.Scanner]. Accepts both the raw
// composite literal Postgres emits for a `typeid` column
// (`(prefix,uuid)`) and the already-printable form (`prefix_base32`)
// — the latter so callers that select `typeid_print(id)` directly
// continue to work unchanged.
func (id *ID) Scan(src any) error {
	if src == nil {
		*id = ""

		return nil
	}

	var raw string

	switch typed := src.(type) {
	case string:
		raw = typed
	case []byte:
		raw = string(typed)
	default:
		return errors.Newf("stdenttypeid: cannot scan %T into stdenttypeid.ID", src)
	}

	normalised, err := normalise(raw)
	if err != nil {
		return err
	}

	*id = normalised

	return nil
}

// normalise turns whichever shape the driver hands us into the
// printable form. Composite literals start with `(` (Postgres's
// canonical text representation of a composite); everything else
// is treated as already-printable and returned verbatim.
func normalise(raw string) (ID, error) {
	if !strings.HasPrefix(raw, "(") {
		return ID(raw), nil
	}

	if !strings.HasSuffix(raw, ")") {
		return "", errors.Newf("stdenttypeid: invalid composite literal %q", raw)
	}

	body := strings.TrimSuffix(strings.TrimPrefix(raw, "("), ")")

	prefix, uuidText, ok := strings.Cut(body, ",")
	if !ok {
		return "", errors.Newf("stdenttypeid: invalid composite literal %q", raw)
	}

	if prefix == "" {
		return "", errors.Newf("stdenttypeid: empty prefix in composite literal %q", raw)
	}

	parsed, err := uuid.Parse(uuidText)
	if err != nil {
		return "", errors.Wrapf(err, "stdenttypeid: parse uuid from %q", raw)
	}

	return ID(prefix + "_" + encodeUUIDBase32(parsed)), nil
}

// encodeUUIDBase32 renders the 16-byte UUID as a 26-character
// Crockford-style base32 string. Mirrors the SQL `base32_encode()`
// function this package's Postgres companion provides; both pad
// the 128-bit input out to 130 bits (5 × 26) on the left, so the
// leading character only ever takes one of the first 8 values
// (0..7).
func encodeUUIDBase32(u uuid.UUID) string {
	const (
		mask  = 31
		shift = 5
		// out length: 26 characters for 130 bits (16 bytes × 8 +
		// 2 leading padding bits, rounded up to 5-bit groups).
		out = 26
	)

	n := new(big.Int).SetBytes(u[:])
	maskInt := big.NewInt(mask)

	buf := make([]byte, out)
	for i := out - 1; i >= 0; i-- {
		idx := new(big.Int).And(n, maskInt).Int64()
		buf[i] = base32Alphabet[idx]
		n.Rsh(n, shift)
	}

	return string(buf)
}
