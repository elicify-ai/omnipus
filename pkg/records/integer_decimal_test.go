// Omnipus — FR-004 / FR-013: the integer/decimal split and its bounds.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// ADR-068 D3 (revision 7, operator ruling) DELETED `money` and SPLIT `number`
// into `integer` and `decimal`. Three things follow that a test has to hold:
//
//	FR-004  the type set is still SEVEN, with the membership changed
//	FR-013  `integer` is int64 and REFUSES outside it; `decimal` is bounded at
//	        maxDecimalScale (100), and the retired money bound of 12 must NOT
//	        have been inherited
//	R-1     the two are ONE declared type for COMPARISON — `3 = 3.0` is true —
//	        so the split decides storage and bounds, not a comparison domain
//
// The refusals are the point. Every one of them has a silent alternative that
// a reasonable implementation might have chosen — truncate, round, saturate,
// widen to a float — and each of those is a wrong number that looks right.
// ---------------------------------------------------------------------------

func numericSchema(t *testing.T) *SchemaSet {
	t.Helper()
	return loadSet(t, map[string]string{"m.yaml": `
schema_version: 1
type: m
properties:
  count:  { type: integer }
  amount: { type: decimal }
  many_i: { type: integer, many: true }
`})
}

func numericProp(t *testing.T, name string) *Property {
	t.Helper()
	sc, ok := numericSchema(t).Get("m")
	if !ok {
		t.Fatal("fixture schema `m` did not load")
	}
	p, ok := sc.Property(name)
	if !ok {
		t.Fatalf("fixture property %q did not load", name)
	}
	return p
}

// TestTypes_StillSevenWithMoneyGoneAndNumberSplit is FR-004's arithmetic,
// asserted rather than asserted-in-a-comment.
//
// THE NAME IS NOW HISTORICAL AND IS KEPT DELIBERATELY. It records revision 7's
// arithmetic — −1 (money) −1 (number) +2 (integer, decimal) = still SEVEN — which
// is the sum a previous reader nearly got wrong and the reason this is a test
// rather than a sentence. Draft 11 then added an EIGHTH, `checkbox` (FR-004c),
// so the live count is eight and both facts are asserted below. Renaming the
// function would break the spec §7 traceability row that cites it by name and
// would erase the very arithmetic it exists to pin.
func TestTypes_StillSevenWithMoneyGoneAndNumberSplit(t *testing.T) {
	if len(PropertyTypes) != 8 {
		t.Fatalf("FR-004 as amended by FR-004c: the type set is EIGHT; got %d (%v)", len(PropertyTypes), PropertyTypes)
	}
	want := map[PropertyType]bool{
		TypeText: true, TypeEnum: true, TypeRelation: true, TypeDate: true,
		TypeInteger: true, TypeDecimal: true, TypePerson: true, TypeCheckbox: true,
	}
	// Revision 7's arithmetic, still pinned: the seven that survived `money`'s
	// deletion and `number`'s split are all present, and `checkbox` is the ONE
	// addition on top of them. Asserted as 7+1 rather than as a bare 8 so that
	// deleting one of the original seven and adding two more could not pass.
	if len(want)-1 != 7 {
		t.Fatalf("FR-004: the pre-checkbox set must still be the seven of revision 7; the expectation above lists %d besides checkbox", len(want)-1)
	}
	for _, ty := range PropertyTypes {
		if !want[ty] {
			t.Fatalf("FR-004: %q is not one of the eight declared types", ty)
		}
		delete(want, ty)
	}
	if len(want) != 0 {
		t.Fatalf("FR-004: these declared types are missing from PropertyTypes: %v", want)
	}

	// The two retired names must not resolve, in either direction: a schema
	// declaring them is rejected, and the rejection lists what IS allowed.
	for _, retired := range []string{"money", "number"} {
		if isKnownPropertyType(PropertyType(retired)) {
			t.Fatalf("%q is a RETIRED property type and must not be known", retired)
		}
		root := writeVaultSchema(t, "", "r.yaml",
			"schema_version: 1\ntype: r\nproperties:\n  p: { type: "+retired+" }\n")
		_, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if report.OK() {
			t.Fatalf("a schema declaring the retired type %q must be REJECTED, not loaded", retired)
		}
		var msg string
		for _, rej := range report.Rejections {
			msg += rej.Reason
		}
		for _, live := range []string{"integer", "decimal"} {
			if !strings.Contains(msg, live) {
				t.Fatalf("the rejection of %q must name the live types the author can use instead; %q missing from %q", retired, live, msg)
			}
		}
	}
}

// TestInteger_RefusesOutsideInt64 is FR-013's bound and D3's "a large
// identifier silently truncated".
func TestInteger_RefusesOutsideInt64(t *testing.T) {
	p := numericProp(t, "count")

	t.Run("the bounds themselves are accepted, exactly", func(t *testing.T) {
		for _, want := range []int64{math.MinInt64, math.MaxInt64, 0, -1, 1} {
			text := fmt.Sprintf("%d", want)
			tv, verr := ParseValue(p, Node{Kind: KindScalar, Text: text})
			if verr != nil {
				t.Fatalf("%s is inside int64 and must be accepted: %v", text, verr)
			}
			got, exact := tv.Number.Int64()
			if !exact || got != want {
				t.Fatalf("%s round-tripped as (%d, exact=%v); an in-range integer must survive EXACTLY", text, got, exact)
			}
			if tv.Type != TypeInteger {
				t.Fatalf("%s parsed as %q, want integer", text, tv.Type)
			}
		}
	})

	t.Run("one past each bound is REFUSED, not saturated", func(t *testing.T) {
		// SQLite's CAST saturates '9223372036854775808' to MaxInt64 in silence
		// and a float64 rounds it; both produce a wrong number that looks
		// right. The refusal is the whole value of the type.
		for _, text := range []string{"9223372036854775808", "-9223372036854775809", "99999999999999999999999999"} {
			tv, verr := ParseValue(p, Node{Kind: KindScalar, Text: text})
			if verr == nil {
				got, _ := tv.Number.Int64()
				t.Fatalf("%s is outside int64 and must be REFUSED; it was accepted as %d — saturating or widening here is the silent-truncation defect FR-013 closes", text, got)
			}
			if verr.Code != FindingIntegerOutOfRange {
				t.Fatalf("%s: code = %q, want %q — an out-of-range value is a DIFFERENT fault from an unparseable one and the operator fixes it differently", text, verr.Code, FindingIntegerOutOfRange)
			}
			// The refusal must name the bound, or the author cannot tell how
			// far over they are.
			if !strings.Contains(verr.Reason, "9223372036854775807") {
				t.Fatalf("%s: the refusal must NAME the bound; got %q", text, verr.Reason)
			}
		}
	})

	t.Run("a fractional value is refused with a DIFFERENT code, naming the remedy", func(t *testing.T) {
		for _, text := range []string{"3.5", "-0.1", "1.0000000001"} {
			_, verr := ParseValue(p, Node{Kind: KindScalar, Text: text})
			if verr == nil {
				t.Fatalf("%s is not whole and must be refused in an integer property — truncating to 3 or rounding to 4 are both silent changes to the value", text)
			}
			if verr.Code != FindingIntegerNotWhole {
				t.Fatalf("%s: code = %q, want %q", text, verr.Code, FindingIntegerNotWhole)
			}
			if !strings.Contains(verr.Reason, "decimal") {
				t.Fatalf("%s: the refusal must name the remedy (declare the property `decimal`); got %q", text, verr.Reason)
			}
		}
	})

	t.Run("a whole number written with a fractional zero or an exponent IS accepted", func(t *testing.T) {
		// `3.0` has scale 1 and is a whole number. Answering from the scale
		// alone — "Scale() > 0 means fractional" — would reject a value the
		// author plainly wrote as whole and buy nothing in exchange.
		for _, text := range []string{"3.0", "3.000", "3e0", "3e2", "300e-2"} {
			tv, verr := ParseValue(p, Node{Kind: KindScalar, Text: text})
			if verr != nil {
				t.Fatalf("%s is a whole number and must be accepted in an integer property: %v", text, verr)
			}
			got, exact := tv.Number.Int64()
			want := int64(3)
			if text == "3e2" {
				want = 300
			}
			if !exact || got != want {
				t.Fatalf("%s must be %d exactly; got (%d, exact=%v)", text, want, got, exact)
			}
		}
	})

	t.Run("the parsed value is CANONICAL, so R-1's `3 = 3.0` needs no special case", func(t *testing.T) {
		a, verr := ParseValue(p, Node{Kind: KindScalar, Text: "3"})
		if verr != nil {
			t.Fatalf("ParseValue(3): %v", verr)
		}
		b, verr := ParseValue(p, Node{Kind: KindScalar, Text: "3.0"})
		if verr != nil {
			t.Fatalf("ParseValue(3.0): %v", verr)
		}
		if a.Number.Scale() != 0 || b.Number.Scale() != 0 {
			t.Fatalf("an integer value must be stored at scale 0 however it was spelled; got %d and %d", a.Number.Scale(), b.Number.Scale())
		}
		if a.Number.Cmp(b.Number) != 0 {
			t.Fatalf("R-1: 3 and 3.0 are the same number")
		}
		// Raw keeps the file's own spelling either way.
		if a.Raw != "3" || b.Raw != "3.0" {
			t.Fatalf("Raw must keep the file's spelling; got %q and %q", a.Raw, b.Raw)
		}
	})
}

// TestDecimal_BoundIsMaxDecimalScaleNotTheRetiredMoneyBound is the FR-013
// clause the operator stated in their own words: "make sure its precision after
// digits is high enough to be precise".
func TestDecimal_BoundIsMaxDecimalScaleNotTheRetiredMoneyBound(t *testing.T) {
	p := numericProp(t, "amount")

	t.Run("far more than twelve places is accepted — the money bound is NOT inherited", func(t *testing.T) {
		// 12 was `maxMoneyScale`, a CURRENCY-shaped limit for a type that is
		// not currency-shaped. ADR-068 D3 is explicit that it dies with money
		// and must not be inherited. 13 is the first place past it; 100 is the
		// live bound.
		for _, scale := range []int{13, 30, maxDecimalScale} {
			text := "0." + strings.Repeat("0", scale-1) + "1"
			tv, verr := ParseValue(p, Node{Kind: KindScalar, Text: text})
			if verr != nil {
				t.Fatalf("FR-013: a decimal must carry %d places; %q was refused: %v — this is the retired 12-place money bound being inherited", scale, text, verr)
			}
			if got := int(tv.Number.Scale()); got != scale {
				t.Fatalf("%q must parse at scale %d; got %d", text, scale, got)
			}
		}
	})

	t.Run("one place past the bound is REFUSED, naming the bound, never rounded", func(t *testing.T) {
		text := "0." + strings.Repeat("0", maxDecimalScale) + "1" // maxDecimalScale+1 places
		tv, verr := ParseValue(p, Node{Kind: KindScalar, Text: text})
		if verr == nil {
			t.Fatalf("a value with %d places is past the bound and must be refused; it parsed at scale %d — ROUNDING TO SATISFY A BOUND IS A SILENT CHANGE TO A NUMBER", maxDecimalScale+1, tv.Number.Scale())
		}
		if !strings.Contains(verr.Reason, fmt.Sprintf("%d", maxDecimalScale)) {
			t.Fatalf("the refusal must NAME the bound (%d) so the author knows where the line is; got %q", maxDecimalScale, verr.Reason)
		}
	})

	t.Run("the value is exact, with no float anywhere in the path", func(t *testing.T) {
		// 349.98 is not representable in binary floating point; a float64 path
		// yields 349.97999999999996 and nothing reports it.
		tv, verr := ParseValue(p, Node{Kind: KindScalar, Text: "349.98"})
		if verr != nil {
			t.Fatalf("ParseValue: %v", verr)
		}
		if got := tv.Number.String(); got != "349.98" {
			t.Fatalf("FR-020b: 349.98 must round-trip exactly; got %q", got)
		}
		// DS-1: 2^53+1 must survive, which a float64 cannot represent.
		big, verr := ParseValue(p, Node{Kind: KindScalar, Text: "9007199254740993"})
		if verr != nil {
			t.Fatalf("ParseValue(2^53+1): %v", verr)
		}
		if got := big.Number.String(); got != "9007199254740993" {
			t.Fatalf("DS-1: 2^53+1 must survive exactly; got %q — a float64 renders 9007199254740992", got)
		}
	})
}

// TestNumeric_IntegerAndDecimalAreOneComparisonDomain is R-1's clause, at the
// layer this file owns: an author chooses the STORAGE, not a comparison domain.
func TestNumeric_IntegerAndDecimalAreOneComparisonDomain(t *testing.T) {
	ip := numericProp(t, "count")
	dp := numericProp(t, "amount")

	iv, verr := ParseValue(ip, Node{Kind: KindScalar, Text: "3"})
	if verr != nil {
		t.Fatalf("ParseValue(integer 3): %v", verr)
	}
	dv, verr := ParseValue(dp, Node{Kind: KindScalar, Text: "3.0"})
	if verr != nil {
		t.Fatalf("ParseValue(decimal 3.0): %v", verr)
	}
	if iv.Type == dv.Type {
		t.Fatal("the two must keep DISTINCT declared types — the split decides storage and bounds")
	}
	if iv.Number.Cmp(dv.Number) != 0 {
		t.Fatalf("R-1: an integer 3 and a decimal 3.0 are the same number and must compare equal")
	}
	if !isNumericType(iv.Type) || !isNumericType(dv.Type) {
		t.Fatal("R-1: both declared types must report as numeric, which is what puts them in one comparison domain")
	}
	for _, ty := range []PropertyType{TypeText, TypeEnum, TypeDate, TypeRelation, TypePerson} {
		if isNumericType(ty) {
			t.Fatalf("%q must not be numeric", ty)
		}
	}
}

// TestInteger_UnitIsDeclaredOnBothNumericTypes covers D3's `exercise: 60
// minutes` failure — the unit is metadata on the declaration, never glued onto
// the figure.
func TestInteger_UnitIsDeclaredOnBothNumericTypes(t *testing.T) {
	for _, ty := range []string{"integer", "decimal"} {
		root := writeVaultSchema(t, "", "u.yaml",
			"schema_version: 1\ntype: u\nproperties:\n  x: { type: "+ty+", unit: minutes }\n")
		set, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if !report.OK() {
			t.Fatalf("`unit` must be valid on %s: %v", ty, report.Rejections)
		}
		sc, _ := set.Get("u")
		p, _ := sc.Property("x")
		if p.Unit != "minutes" {
			t.Fatalf("%s: Unit = %q, want minutes", ty, p.Unit)
		}
		// And the figure itself must NOT carry it.
		if _, verr := ParseValue(p, Node{Kind: KindScalar, Text: "60 minutes"}); verr == nil {
			t.Fatalf("%s: `60 minutes` must be refused — the unit belongs in the declaration, and accepting it here recreates D3's failure", ty)
		}
	}

	// `unit` on a non-numeric type is refused, naming both live numeric types.
	root := writeVaultSchema(t, "", "bad.yaml",
		"schema_version: 1\ntype: bad\nproperties:\n  x: { type: text, unit: minutes }\n")
	_, report, err := LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if report.OK() {
		t.Fatal("`unit` on a text property must be refused")
	}
	var msg string
	for _, rej := range report.Rejections {
		msg += rej.Reason
	}
	if !strings.Contains(msg, "integer") || !strings.Contains(msg, "decimal") {
		t.Fatalf("the refusal must name BOTH types `unit` is valid on; got %q", msg)
	}
}

// TestNumeric_ExpectedShapeNamesTheBound is FR-006/FR-042's "say what was
// expected", for the two new types.
func TestNumeric_ExpectedShapeNamesTheBound(t *testing.T) {
	ip := numericProp(t, "count")
	if s := ip.ExpectedShape(); !strings.Contains(s, "9223372036854775807") || !strings.Contains(s, "-9223372036854775808") {
		t.Fatalf("an integer's expected shape must name BOTH bounds; got %q", s)
	}
	dp := numericProp(t, "amount")
	if s := dp.ExpectedShape(); !strings.Contains(s, fmt.Sprintf("%d", maxDecimalScale)) {
		t.Fatalf("a decimal's expected shape must name its scale bound (%d); got %q", maxDecimalScale, dp.ExpectedShape())
	}
	// Arity is part of the shape, and a `many` integer must say so.
	mp := numericProp(t, "many_i")
	if s := mp.ExpectedShape(); !strings.HasPrefix(s, "a list of ") {
		t.Fatalf("FR-006: a `many` property's expected shape must name the arity; got %q", s)
	}
}

// TestNumeric_BoundConstantsAreTypedInt64 guards a PORTABILITY class, not a
// value.
//
// math.MinInt64 and math.MaxInt64 are UNTYPED constants, and fmt.Sprintf's %d
// defaults a bare untyped constant to `int` — 32 bits on linux/mipsle, the one
// shipped 32-bit target. Naming them directly at a call site made this package
// fail to COMPILE there, on a target no host build and no `go build ./...` on
// an amd64 or arm64 machine can see. MinInteger and MaxInteger are typed int64
// so the width is part of the identifier.
//
// This test cannot catch a regression on its own — a compile failure on mipsle
// is what catches that, and the Stage 1 gate cross-builds for it. What it holds
// is that the typed constants still EXIST and still carry the right values, so
// nobody deletes them as redundant.
func TestNumeric_BoundConstantsAreTypedInt64(t *testing.T) {
	var min64 int64 = MinInteger
	var max64 int64 = MaxInteger
	if min64 != math.MinInt64 || max64 != math.MaxInt64 {
		t.Fatalf("MinInteger/MaxInteger = %d/%d, want the int64 bounds", min64, max64)
	}
	// The rendering must be the full 64-bit value on every architecture. On a
	// 32-bit target an untyped constant reaching %d would not compile at all,
	// so this assertion is about the VALUE surviving, which the message says.
	if got := fmt.Sprintf("%d %d", MinInteger, MaxInteger); got != "-9223372036854775808 9223372036854775807" {
		t.Fatalf("the bounds must render as the full int64 range on every architecture; got %q", got)
	}
}
