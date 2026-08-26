// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"math/big"
	"math/rand"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE IS SHAPED THE WAY IT IS
//
// Cmp's unaligned fallback has been wrong twice, in opposite directions, and
// both times the test written alongside the fix asserted only the handful of
// constants that had been reported:
//
//	v1  fallback `Sign() - Sign()`      -> 1e-200 and 9.99e-197 compared EQUAL
//	v2  magnitude, then raw digits      -> 1.5e-199 and 1.5e-199 compared UNEQUAL
//
// v2's test passed. It contained the four cases v1 got wrong and none of the
// cases v2 got wrong, because both were written by reading the fix rather than
// the contract. So the primary coverage here is not a list of constants: it is
// an INDEPENDENT ORACLE (math/big rationals, which know nothing about scales or
// digit strings) run over a dense cross product of scales and magnitudes, plus
// a seeded random sweep. The named cases are kept underneath as regression
// pins, not as the proof.
// ---------------------------------------------------------------------------

// ratOf is the oracle: the same number as an exact rational, derived from the
// DEFINITION of a Decimal (unscaled x 10^-scale) and nothing else. It shares no
// code with Cmp — no alignment, no digit strings, no magnitude exponent — so
// agreement between the two is evidence rather than a tautology.
func ratOf(d Decimal) *big.Rat {
	r := new(big.Rat).SetInt(d.Unscaled())
	s := int64(d.Scale())
	switch {
	case s > 0:
		r.Quo(r, new(big.Rat).SetInt(pow10(s)))
	case s < 0:
		r.Mul(r, new(big.Rat).SetInt(pow10(-s)))
	}
	return r
}

// checkAgainstOracle asserts everything Cmp promises about one pair.
func checkAgainstOracle(t *testing.T, label string, a, b Decimal) {
	t.Helper()

	want := ratOf(a).Cmp(ratOf(b))
	got := a.Cmp(b)
	if got != want {
		t.Fatalf("%s: Cmp(%s@%d, %s@%d) = %d, want %d (exact values %s and %s) — a wrong comparison is a silently wrong answer, not an error",
			label, a.Unscaled(), a.Scale(), b.Unscaled(), b.Scale(), got, want,
			ratOf(a).RatString(), ratOf(b).RatString())
	}
	if got < -1 || got > 1 {
		t.Fatalf("%s: Cmp returned %d; the contract is -1, 0 or +1", label, got)
	}
	if rev := b.Cmp(a); rev != -got {
		t.Fatalf("%s: Cmp is not antisymmetric: a.Cmp(b)=%d but b.Cmp(a)=%d", label, got, rev)
	}
	if (got == 0) != a.Equal(b) {
		t.Fatalf("%s: Cmp and Equal disagree: Cmp=%d Equal=%v", label, got, a.Equal(b))
	}
}

// usesUnalignedPath reports whether this pair reaches cmpUnaligned rather than
// the align fast path. The sweeps below assert they actually exercised it: a
// property test that only ever ran the fast path would stay green with the
// fallback deleted, which is precisely how v2 shipped.
func usesUnalignedPath(a, b Decimal) bool {
	_, _, err := align(a, b)
	return err != nil
}

// TestDecimal_CmpMatchesAnExactRationalOracle is the class-level proof: over a
// dense cross product of unscaled magnitudes and scales — including the pairs
// that cannot be aligned — Cmp agrees with exact rational arithmetic on every
// single pair, in both directions.
func TestDecimal_CmpMatchesAnExactRationalOracle(t *testing.T) {
	unscaledText := []string{
		"0", "1", "-1", "2", "3", "-3", "15", "-15", "20", "101", "150",
		"999", "1000", "-1000", "9007199254740993",
		"1234567890123456789012345678901",
	}
	// Scales chosen to sit on both sides of maxDecimalScale and to make the
	// "same magnitude exponent, different scale" case common — that is the tie
	// v2 got wrong.
	scales := []int32{-101, -3, 0, 1, 2, 12, 99, 100, 101, 150, 199, 200, 201, 300}

	values := make([]Decimal, 0, len(unscaledText)*len(scales))
	for _, txt := range unscaledText {
		n, ok := new(big.Int).SetString(txt, 10)
		if !ok {
			t.Fatalf("fixture %q is not an integer", txt)
		}
		for _, sc := range scales {
			values = append(values, NewDecimal(n, sc))
		}
	}

	unaligned := 0
	for i := range values {
		for j := range values {
			if usesUnalignedPath(values[i], values[j]) {
				unaligned++
			}
			checkAgainstOracle(t, "cross product", values[i], values[j])
		}
	}

	if len(values) < 200 {
		t.Fatalf("the sweep built only %d values; it is not dense enough to be evidence", len(values))
	}
	if unaligned < 1000 {
		t.Fatalf("only %d of %d pairs reached the unaligned fallback — this sweep is not testing the code path it exists to test",
			unaligned, len(values)*len(values))
	}
}

// TestDecimal_CmpMatchesTheOracleOnRandomPairs sweeps magnitudes and scales the
// hand-written table would never think of. The seed is fixed so a failure is
// reproducible; the generator deliberately produces many pairs whose leading
// digits and magnitudes collide, because that is where the ordering is decided.
func TestDecimal_CmpMatchesTheOracleOnRandomPairs(t *testing.T) {
	rng := rand.New(rand.NewSource(20260826))

	randomDecimal := func() Decimal {
		digits := 1 + rng.Intn(24)
		buf := make([]byte, digits)
		buf[0] = byte('1' + rng.Intn(9)) // no leading zero
		for i := 1; i < digits; i++ {
			buf[i] = byte('0' + rng.Intn(10))
		}
		n, ok := new(big.Int).SetString(string(buf), 10)
		if !ok {
			t.Fatalf("generated %q is not an integer", buf)
		}
		switch rng.Intn(4) {
		case 0:
			n.Neg(n)
		case 1:
			n.SetInt64(0)
		}
		// Concentrated around the alignment bound so both paths get worked.
		return NewDecimal(n, int32(rng.Intn(420)-120))
	}

	unaligned := 0
	const iterations = 20000
	for i := 0; i < iterations; i++ {
		a, b := randomDecimal(), randomDecimal()
		if usesUnalignedPath(a, b) {
			unaligned++
		}
		checkAgainstOracle(t, "random sweep", a, b)

		// Also compare each value against a same-valued rewriting of itself at
		// a larger scale (x10^k). Equality across scales is the property v2
		// broke, and a random sweep alone hits it only by luck.
		k := int32(rng.Intn(60))
		shifted := NewDecimal(new(big.Int).Mul(a.Unscaled(), pow10(int64(k))), a.Scale()+k)
		if !a.Equal(shifted) {
			t.Fatalf("random sweep: %s@%d and the same value rewritten as %s@%d compared unequal (Cmp=%d)",
				a.Unscaled(), a.Scale(), shifted.Unscaled(), shifted.Scale(), a.Cmp(shifted))
		}
		checkAgainstOracle(t, "same value, larger scale", a, shifted)
	}
	if unaligned < iterations/20 {
		t.Fatalf("only %d of %d random pairs reached the unaligned fallback — the generator is not covering it", unaligned, iterations)
	}
}

// TestDecimal_CmpNeverReportsUnequalValuesAsEqual pins the reported constants
// from BOTH defects by name, so a regression report stays readable. The proof
// that the CLASS is closed is the oracle sweeps above; these are the receipts.
func TestDecimal_CmpNeverReportsUnequalValuesAsEqual(t *testing.T) {
	dec := func(unscaled int64, scale int32) Decimal { return NewDecimal(big.NewInt(unscaled), scale) }

	cases := []struct {
		name string
		a, b Decimal
		want int
	}{
		// v1: the fallback was Sign()-Sign(), so these compared EQUAL.
		{"v1: 1e-200 vs 9.99e-197", dec(1, 200), dec(999, 199), -1},
		{"v1 reversed", dec(999, 199), dec(1, 200), 1},

		// v2: magnitudes tied, so it compared raw unscaled digits and ignored
		// that the scales still differed.
		{"v2: 1.5e-199 vs 1.5e-199 written two ways", dec(15, 200), dec(150, 201), 0},
		{"v2 reversed", dec(150, 201), dec(15, 200), 0},
		{"v2: 2e-199 vs 3e-199", dec(20, 200), dec(3, 199), -1},
		{"v2 reversed", dec(3, 199), dec(20, 200), 1},
		{"v2: 2e-199 vs 1.01e-199", dec(20, 200), dec(101, 201), 1},
		{"v2 reversed", dec(101, 201), dec(20, 200), -1},

		// Signs, zeros and the aligned fast path.
		{"same value, different scale", dec(100, 2), dec(1, 0), 0},
		{"negative beats positive across wide scales", dec(-1, 200), dec(1, 199), -1},
		{"two negatives, wide scales", dec(-999, 199), dec(-1, 200), -1},
		{"two negatives, tied magnitude", dec(-15, 200), dec(-150, 201), 0},
		{"two negatives, tied magnitude, unequal", dec(-20, 200), dec(-3, 199), 1},
		{"zero against a tiny positive", dec(0, 0), dec(1, 200), -1},
		{"zero against a tiny negative", dec(0, 0), dec(-1, 200), 1},
		{"both zero, different scales", dec(0, 5), dec(0, 90), 0},
		{"both zero, one beyond the bound", dec(0, 5), dec(0, 900), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.a.Cmp(c.b); got != c.want {
				t.Fatalf("Cmp = %d, want %d — a wrong comparison here is a silently wrong answer, not an error", got, c.want)
			}
			checkAgainstOracle(t, c.name, c.a, c.b)
		})
	}
}
