// Omnipus — tests for String's rendering bound (the maxRescaleGap defect's twin).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"math"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE IS SHAPED THE WAY IT IS
//
// String had the same defect align/rescale had: it turned a caller-supplied
// SCALE into that many literal characters, so the cost of rendering was set by
// a number the value's own information had nothing to do with. Both of its
// branches did it, by two different mechanisms — pow10 on the negative side,
// strings.Repeat on the positive side — which is why the bound is stated over
// the scale rather than bolted onto pow10, and why the tests below cover both
// directions rather than the one that was reported.
//
// The change is only defensible if NOTHING a vault can produce renders
// differently, so that claim gets the heavy coverage and it is proved twice,
// against two references that share no code with each other:
//
//	ORACLE      the digits peeled off arithmetically, by repeated division by
//	            ten, using none of the three things String is built from
//	            (big.Int.String, pow10, strings.Repeat). It says what the
//	            number IS. This is the correctness proof.
//	REFERENCE   a frozen, verbatim copy of the PRE-FIX String body. It says
//	            what the old code DID. This is the compatibility proof, and it
//	            is the only thing that can answer "byte-identical before and
//	            after" directly rather than by inference.
//
// Neither alone is enough. The oracle would stay green if the old code had
// always been wrong in some corner; the reference would stay green if the fix
// faithfully reproduced an old bug. Agreement of all three is the evidence.
// ---------------------------------------------------------------------------

// preFixString is the EXACT body String had at c8a9e5e6, before the bound.
//
// It is deliberately a frozen copy and not a call into anything: its whole job
// is to be the old behaviour, so it must not track edits to the new code. It is
// NOT an oracle — it is unbounded and would hang on the very inputs this file
// exists to fix, so it is only ever called with in-domain scales.
func preFixString(d Decimal) string {
	n := d.int()
	if d.scale <= 0 {
		if d.scale == 0 {
			return n.String()
		}
		out := new(big.Int).Mul(new(big.Int).Abs(n), pow10(-int64(d.scale)))
		if n.Sign() < 0 {
			return "-" + out.String()
		}
		return out.String()
	}
	digits := new(big.Int).Abs(n).String()
	scale := int(d.scale)
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	cut := len(digits) - scale
	s := digits[:cut] + "." + digits[cut:]
	if n.Sign() < 0 {
		s = "-" + s
	}
	return s
}

// oracleString derives the expected text from the DEFINITION of a Decimal —
// unscaled x 10^-scale, written with exactly max(scale, 0) fractional digits —
// and derives it ARITHMETICALLY, sharing no machinery with the code under test.
//
// The obvious oracle was math/big's own rational formatter, and it is not used
// here: this package forbids every identifier containing "Float" and enforces
// that mechanically with an allowlist that is empty ON PURPOSE
// (decimal_no_float_test.go — "adding an entry is the argument for a float").
// That formatter is exact and touches no binary float, but exempting a whole
// file from FR-020b's guard to borrow it would trade a real control for a test
// convenience. The guard is right to be blunt; the oracle moved instead.
//
// So the digits are PEELED OFF by repeated division by ten, and a negative
// scale's trailing zeros are EMITTED as digits rather than multiplied in.
// Neither big.Int.String nor pow10 nor strings.Repeat is involved — which are
// exactly the three things String is built from, so agreement between the two
// is evidence about the digits and not just about where the point was put.
func oracleString(d Decimal) string {
	n := d.Unscaled()
	neg := n.Sign() < 0
	m := new(big.Int).Abs(n)

	// A negative scale is trailing zeros; a positive scale is fractional
	// digits. Zero is zero at every scale, so it grows no trailing zeros.
	frac, zeros := 0, 0
	if s := d.Scale(); s < 0 {
		if m.Sign() != 0 {
			zeros = int(-s)
		}
	} else {
		frac = int(s)
	}

	// digits[j] is the 10^j digit, lowest first.
	digits := make([]byte, 0, zeros+frac+8)
	for i := 0; i < zeros; i++ {
		digits = append(digits, '0')
	}
	ten, rem := big.NewInt(10), new(big.Int)
	for m.Sign() != 0 {
		m.DivMod(m, ten, rem)
		digits = append(digits, byte('0'+rem.Int64()))
	}
	// Always at least one digit before the point, zero included.
	for len(digits) < frac+1 {
		digits = append(digits, '0')
	}

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i := len(digits) - 1; i >= 0; i-- {
		b.WriteByte(digits[i])
		if frac > 0 && i == frac {
			b.WriteByte('.')
		}
	}
	return b.String()
}

// inDomainUnscaled is the magnitude fixture set. It is chosen around the two
// places the rendering branches: the sign of the value, and whether the digit
// count is below, at, or above the scale (which decides whether the positive
// branch left-pads with zeros at all).
func inDomainUnscaled(t *testing.T) []*big.Int {
	t.Helper()
	texts := []string{
		"0",
		"1", "-1", "5", "-5", "9", "-9",
		"10", "-10", "99", "100", "101", "105", "-105",
		"999", "1000", "-1000", "1005", "10000", "34998", "-34998",
		"100000000000000000000", // 1 followed by 20 zeros — trailing-zero shape
		"9007199254740993",      // 2^53 + 1, DS-1
		"-9007199254740993",
		"1234567890123456789012345678901",
		"-1234567890123456789012345678901",
		// 101 digits: one longer than maxDecimalScale, so it straddles the
		// len(digits) <= scale branch at the top of the in-domain range.
		"1" + strings.Repeat("0", 100),
		"9" + strings.Repeat("9", 100),
		"-" + "5" + strings.Repeat("7", 100),
		// 150 digits: comfortably past every scale in range.
		strings.Repeat("123456789", 16) + "123456",
	}
	out := make([]*big.Int, 0, len(texts))
	for _, txt := range texts {
		n, ok := new(big.Int).SetString(txt, 10)
		if !ok {
			t.Fatalf("fixture %q is not an integer", txt)
		}
		out = append(out, n)
	}
	return out
}

// checkInDomainRendering asserts the full contract for one in-domain value:
// String agrees with the independent oracle, AND is byte-identical to the
// pre-fix code, AND its output re-parses to the same number.
func checkInDomainRendering(t *testing.T, label string, d Decimal) {
	t.Helper()

	got := d.String()

	if want := oracleString(d); got != want {
		t.Fatalf("%s: NewDecimal(%s, %d).String() = %q, but the value IS %q",
			label, d.Unscaled(), d.Scale(), got, want)
	}
	if want := preFixString(d); got != want {
		t.Fatalf("%s: NewDecimal(%s, %d).String() = %q, but the rendering before the bound was %q — an in-domain rendering changed, which is the one thing this fix must not do",
			label, d.Unscaled(), d.Scale(), got, want)
	}
	if strings.ContainsAny(got, "<>") {
		t.Fatalf("%s: NewDecimal(%s, %d).String() = %q — an in-domain value was abbreviated; the bound is set too tight and real vault data would stop rendering",
			label, d.Unscaled(), d.Scale(), got)
	}

	// The property that decides the rendering FORM: every in-domain output is
	// a literal this package's own parser accepts, and re-reading it gives back
	// the same number. This is what exponent notation would have broken, so it
	// is asserted rather than assumed.
	back, err := ParseDecimal(got)
	if err != nil {
		t.Fatalf("%s: String() produced %q, which ParseDecimal rejects (%v) — a rendering that cannot be read back is not a rendering of this package's number type",
			label, got, err)
	}
	if !back.Equal(d) {
		t.Fatalf("%s: String() produced %q, which re-parses as %s at scale %d — a different number from %s at scale %d",
			label, got, back.Unscaled(), back.Scale(), d.Unscaled(), d.Scale())
	}
}

// TestDecimalString_IsUnchangedAcrossTheWholeInDomainScaleRange is the
// byte-identical proof. It sweeps EVERY scale ParseDecimal can produce —
// [-maxDecimalScale, +maxDecimalScale], all 201 of them — against every
// magnitude fixture, and requires the oracle and the frozen pre-fix code to
// agree with the new String on every one.
//
// The range is exhaustive rather than sampled because it is small enough to be,
// and because "we checked some scales" is exactly the reasoning that left the
// positive-scale half of this defect unfound.
func TestDecimalString_IsUnchangedAcrossTheWholeInDomainScaleRange(t *testing.T) {
	fixtures := inDomainUnscaled(t)

	checked := 0
	for _, n := range fixtures {
		for scale := int32(-maxDecimalScale); scale <= maxDecimalScale; scale++ {
			checkInDomainRendering(t, "in-domain sweep", NewDecimal(n, scale))
			checked++
		}
	}

	t.Logf("in-domain sweep: %d renderings checked against both the arithmetic oracle and the pre-fix reference", checked)

	wantAtLeast := len(fixtures) * (2*maxDecimalScale + 1)
	if checked != wantAtLeast {
		t.Fatalf("the sweep covered %d renderings, want %d — it did not cover the whole scale range", checked, wantAtLeast)
	}
	if checked < 5000 {
		t.Fatalf("the sweep covered only %d renderings; it is not dense enough to be evidence", checked)
	}
}

// TestDecimalString_MatchesTheOracleOnRandomInDomainValues sweeps magnitudes
// and scales the fixture table would never think of. The seed is fixed so a
// failure is reproducible.
//
// Digit counts are drawn to straddle the scale on both sides, because the
// positive branch's left-padding only engages when the digits are shorter than
// the scale, and a generator biased towards long numbers would never run it.
func TestDecimalString_MatchesTheOracleOnRandomInDomainValues(t *testing.T) {
	rng := rand.New(rand.NewSource(20260826))

	const iterations = 20000
	padded, unpadded := 0, 0

	for i := 0; i < iterations; i++ {
		digits := 1 + rng.Intn(120)
		buf := make([]byte, digits)
		buf[0] = byte('1' + rng.Intn(9)) // no leading zero; big.Int never renders one
		for j := 1; j < digits; j++ {
			buf[j] = byte('0' + rng.Intn(10))
		}
		n, ok := new(big.Int).SetString(string(buf), 10)
		if !ok {
			t.Fatalf("generated %q is not an integer", buf)
		}
		if rng.Intn(2) == 0 {
			n.Neg(n)
		}
		if rng.Intn(20) == 0 {
			n.SetInt64(0)
		}

		scale := int32(rng.Intn(2*maxDecimalScale+1) - maxDecimalScale)
		d := NewDecimal(n, scale)

		if scale > 0 && len(new(big.Int).Abs(n).String()) <= int(scale) {
			padded++
		} else {
			unpadded++
		}

		checkInDomainRendering(t, "random in-domain", d)
	}

	t.Logf("random in-domain sweep: %d values, %d took the zero-padding branch, %d did not", iterations, padded, unpadded)

	// Both branches must actually have run, or the sweep proves half of what it
	// claims. The positive branch's padding is the half that was never reported
	// and never tested.
	if padded < 1000 {
		t.Fatalf("only %d of %d random values took the zero-padding branch — the sweep is not exercising the positive-scale rendering it exists to pin", padded, iterations)
	}
	if unpadded < 1000 {
		t.Fatalf("only %d of %d random values skipped the zero-padding branch — the sweep is not exercising the ordinary rendering", unpadded, iterations)
	}
}

// TestDecimalString_AbbreviatesExactlyBeyondTheInDomainRange pins WHERE the
// disposition changes. A bound nobody has probed on both sides is a bound
// nobody knows the position of.
func TestDecimalString_AbbreviatesExactlyBeyondTheInDomainRange(t *testing.T) {
	one := big.NewInt(1)

	for _, scale := range []int32{-maxDecimalScale, -maxDecimalScale + 1, 0, maxDecimalScale - 1, maxDecimalScale} {
		got := NewDecimal(one, scale).String()
		if strings.HasPrefix(got, "<") {
			t.Fatalf("scale %d is inside the parseable range and must render in full, got %q", scale, got)
		}
		if want := preFixString(NewDecimal(one, scale)); got != want {
			t.Fatalf("scale %d: got %q, pre-fix rendering was %q", scale, got, want)
		}
	}

	for _, scale := range []int32{-maxDecimalScale - 1, maxDecimalScale + 1} {
		got := NewDecimal(one, scale).String()
		if !strings.HasPrefix(got, "<") {
			t.Fatalf("scale %d is outside the parseable range and must be abbreviated, got %q", scale, got)
		}
	}
}

// TestDecimalString_IsBoundedAtEveryReachableScale is the defect's own test.
//
// ⚠️ READ BEFORE REVERTING THE BOUND TO WATCH THIS FAIL. With the bound
// removed, math.MinInt32 asks for 10^2147483648 (about 890 MB) and then renders
// it as a 2,147,483,649-character string, and math.MaxInt32 allocates 2 GB of
// '0' bytes. Neither returns; a machine has already been hung on exactly this.
// To confirm this test can fail, mutate against scale -100000 (a few hundred
// kilobytes, fails in well under a second) — never against the extremes.
func TestDecimalString_IsBoundedAtEveryReachableScale(t *testing.T) {
	// The full population reachable through the exported API: NewDecimal and
	// DecimalFromInt64 take any int32, so the extremes are ordinary arguments,
	// not hypotheticals.
	scales := []int32{
		-1000, -100000, -10000000, -1000000000, math.MinInt32,
		1000, 100000, 10000000, 1000000000, math.MaxInt32,
	}

	for _, scale := range scales {
		for _, d := range []Decimal{
			NewDecimal(big.NewInt(1), scale),
			NewDecimal(big.NewInt(-15), scale),
			DecimalFromInt64(0, scale),
		} {
			got := d.String()

			// Bounded by the value's OWN digits plus a fixed marker, never by
			// the scale. This is the whole property: two values one digit long
			// render to the same length whether their scale is 1000 or
			// 2147483647.
			digits := len(d.Unscaled().String())
			if len(got) > digits+160 {
				t.Fatalf("String() at scale %d produced %d characters for a %d-digit value — the scale is still driving the output length",
					scale, len(got), digits)
			}

			// Not mistakable for a complete number, in the strongest sense
			// available: the package's own parser refuses it outright rather
			// than reading a prefix and returning a different number.
			if !strings.HasPrefix(got, "<") || !strings.HasSuffix(got, ">") {
				t.Fatalf("String() at scale %d = %q — the abbreviation must be visibly not-a-number", scale, got)
			}
			if back, err := ParseDecimal(got); err == nil {
				t.Fatalf("String() at scale %d produced %q, which ParseDecimal ACCEPTED as %s at scale %d — an abbreviation a parser accepts is a silently wrong number",
					scale, got, back.Unscaled(), back.Scale())
			}

			// It must still say WHICH number it declined to write out.
			// (unscaled, scale) is the value by definition, so naming both
			// loses nothing; a truncated digit run would.
			if !strings.Contains(got, d.Unscaled().String()) {
				t.Fatalf("String() at scale %d = %q — it does not name the unscaled value, so the reading is not recoverable from it", scale, got)
			}
		}
	}
}

// TestDecimalString_AbbreviationNamesBothHalvesOfTheValue pins the marker's
// content, so a future edit cannot quietly reduce it to "<too long>".
func TestDecimalString_AbbreviationNamesBothHalvesOfTheValue(t *testing.T) {
	d := NewDecimal(big.NewInt(-12345), math.MinInt32)
	got := d.String()

	for _, want := range []string{"-12345", "-2147483648", "100"} {
		if !strings.Contains(got, want) {
			t.Fatalf("abbreviation %q does not mention %q; it must name the unscaled value, the scale, and the bound it exceeded", got, want)
		}
	}

	// Two DIFFERENT out-of-range values must not render alike — that is what a
	// truncated digit string would have done, and it is why the marker names
	// the components instead of showing a prefix of the expansion.
	other := NewDecimal(big.NewInt(-12345), math.MinInt32+1).String()
	if other == got {
		t.Fatalf("scales %d and %d both render as %q — the abbreviation is not distinguishing values", math.MinInt32, math.MinInt32+1, got)
	}
}
