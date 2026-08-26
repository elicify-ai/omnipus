// Omnipus — tests for FR-013 and FR-020b: exact decimal, no binary floats.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"math/big"
	"testing"
)

// TestDecimal_ParsesAndRendersExactly checks the lexical round trip. The
// oracle is arithmetic, not the implementation: each expectation is what the
// decimal value IS, written out.
func TestDecimal_ParsesAndRendersExactly(t *testing.T) {
	cases := []struct {
		text  string
		want  string
		scale int32
	}{
		{"0", "0", 0},
		{"349.98", "349.98", 2},
		{"-349.98", "-349.98", 2},
		{"+7", "7", 0},
		{"0.10", "0.10", 2},
		{".5", "0.5", 1},
		{"1.005", "1.005", 3},
		{"9007199254740993", "9007199254740993", 0}, // 2^53 + 1 — DS-1
		{"0.000000000000000001", "0.000000000000000001", 18},
		{"1e3", "1000", -3},
		{"1.5e2", "150", -1},
		{"12345e-2", "123.45", 2},
	}
	for _, tc := range cases {
		d, err := ParseDecimal(tc.text)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", tc.text, err)
		}
		if got := d.String(); got != tc.want {
			t.Fatalf("ParseDecimal(%q).String() = %q, want %q", tc.text, got, tc.want)
		}
		if d.Scale() != tc.scale {
			t.Fatalf("ParseDecimal(%q).Scale() = %d, want %d", tc.text, d.Scale(), tc.scale)
		}
	}
}

// TestDecimal_HoldsValuesBinary64Cannot is FR-020b's positive case. Every value
// here is provably wrong in float64.
func TestDecimal_HoldsValuesBinary64Cannot(t *testing.T) {
	t.Run("2^53+1 survives", func(t *testing.T) {
		// float64(9007199254740993) == 9007199254740992. DS-1 requires exact.
		d, err := ParseDecimal("9007199254740993")
		if err != nil {
			t.Fatalf("%v", err)
		}
		want, _ := new(big.Int).SetString("9007199254740993", 10)
		if d.Unscaled().Cmp(want) != 0 {
			t.Fatalf("2^53+1 must survive exactly; got %s", d.Unscaled())
		}
		// And it must be distinguishable from 2^53, which float64 conflates it with.
		other, _ := ParseDecimal("9007199254740992")
		if d.Equal(other) {
			t.Fatalf("2^53 and 2^53+1 must not compare equal — that is precisely the float64 defect")
		}
	})

	t.Run("0.1 + 0.2 is exactly 0.3", func(t *testing.T) {
		a, _ := ParseDecimal("0.1")
		b, _ := ParseDecimal("0.2")
		sum, err := a.Add(b)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got := sum.String(); got != "0.3" {
			t.Fatalf("FR-013: 0.1 + 0.2 must be exactly 0.3 (float64 gives 0.30000000000000004); got %s", got)
		}
		three, _ := ParseDecimal("0.3")
		if !sum.Equal(three) {
			t.Fatalf("0.1 + 0.2 must equal 0.3")
		}
	})

	t.Run("a 40-digit value is exact", func(t *testing.T) {
		text := "1234567890123456789012345678901234567890.12345"
		d, err := ParseDecimal(text)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got := d.String(); got != text {
			t.Fatalf("want %s, got %s", text, got)
		}
	})

	t.Run("comparison is by value, not representation", func(t *testing.T) {
		a, _ := ParseDecimal("349.98")
		b, _ := ParseDecimal("349.9800")
		if !a.Equal(b) {
			t.Fatalf("349.98 and 349.9800 are the same number and must compare equal")
		}
		if a.Scale() == b.Scale() {
			t.Fatalf("the fixture is pointless unless the scales differ")
		}
	})
}

// TestDecimal_RejectsWhatIsNotANumber pins DS-1's `PLACEHOLDER — unknown` row
// and the near-misses that must not be quietly accepted.
func TestDecimal_RejectsWhatIsNotANumber(t *testing.T) {
	bad := []string{
		"", " ", ".", "-", "+",
		"PLACEHOLDER — unknown",
		"1.2.3",
		"NaN", "Inf", "-Inf", "inf",
		"0x1f",
		"1,000",
		"1_000",
		"349.98 SGD", // money has its own parser; a glued currency is ambiguous
		"12abc",
		"1e",
		"1e2.5",
	}
	for _, text := range bad {
		if d, err := ParseDecimal(text); err == nil {
			t.Fatalf("ParseDecimal(%q) must be rejected; it returned %s", text, d.String())
		}
	}
}

// TestDecimal_ZeroValueIsUsableAndNeverPanics guards §8 R-11's "total and never
// panics" at the numeric layer: a zero Decimal has a nil big.Int inside.
func TestDecimal_ZeroValueIsUsableAndNeverPanics(t *testing.T) {
	var zero Decimal
	if !zero.IsZero() || zero.Sign() != 0 {
		t.Fatalf("the zero value must behave as zero")
	}
	if got := zero.String(); got != "0" {
		t.Fatalf("want 0, got %q", got)
	}
	one, _ := ParseDecimal("1")
	sum, err := zero.Add(one)
	if err != nil || sum.String() != "1" {
		t.Fatalf("0 + 1 must be 1; got %s err=%v", sum.String(), err)
	}
	if zero.Cmp(one) >= 0 {
		t.Fatalf("0 < 1")
	}
	if zero.Unscaled().Sign() != 0 {
		t.Fatalf("Unscaled on a zero value must give 0, not panic")
	}
}
