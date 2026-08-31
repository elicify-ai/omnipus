// Omnipus — ADR-068 D24.3 / spec FR-144, rule R-15/FR-214: exact rationals in,
// a DECLARED scale out. No binary floating point anywhere.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"math/big"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// FR-144: "Internal arithmetic runs over exact rationals; a result crosses into
// display or comparison at a DECLARED scale." Two halves, and both are load-
// bearing.
//
// THE FIRST HALF is why Decimal is not enough on its own. Decimal (decimal.go)
// is an exact base-10 value — unscaled integer plus scale — and it is exactly
// right for what a FILE holds, because a file holds a written number. It is the
// wrong shape for DIVISION: 1/3 has no exact base-10 form at any scale, so a
// decimal division has to round, and once it rounds mid-expression the rounding
// compounds through every later step invisibly. big.Rat has no such problem:
// 1/3 is the pair (1,3), exactly, and `(1/3) * 3` is 1 with no error term.
//
// THE SECOND HALF is why the rational cannot be the answer either. A rational
// is exact but not comparable to a file's decimal without a decision about
// precision — and FR-152 records what happens when precision is left undeclared
// (the original refusal of `avg` as "a number whose precision nobody
// declared"). So a value crosses the boundary ONCE, at a scale the author
// declared with toFixed/round or the documented default of 10, rounded
// HALF-EVEN, and it is LABELLED as rounded when the exact rational did not fit.
// A rounded number that does not say it is rounded is the whole problem.
//
// NO BINARY FLOAT APPEARS HERE, AND THAT IS ENFORCED, NOT ASSERTED.
// decimal_no_float_test.go walks every .go file in this package with the
// comments switched off and fails on any float32/float64, any identifier
// containing "Float" (big.Float, ParseFloat, FormatFloat, SetFloat64,
// Rat.FloatString — all of them), and any untyped float literal. That guard is
// why this file divides with big.Int.QuoRem rather than calling the one-line
// Rat.FloatString(scale) that would have done the job: FloatString is a banned
// name for a good reason — it is documented to round HALF-AWAY-FROM-ZERO, not
// half-even, so the convenient call would also have been the wrong rounding.
// ---------------------------------------------------------------------------

// ratFromDecimal lifts an exact Decimal into an exact rational.
//
// unscaled × 10^-scale is exact in both directions, so nothing is lost here and
// nothing needs a bound: the Decimal already passed decimal.go's scale bounds
// on the way in.
func ratFromDecimal(d Decimal) *big.Rat {
	num := new(big.Int).Set(d.Unscaled())
	scale := d.Scale()
	if scale == 0 {
		return new(big.Rat).SetInt(num)
	}
	if scale > 0 {
		return new(big.Rat).SetFrac(num, pow10(int64(scale)))
	}
	num.Mul(num, pow10(int64(-scale)))
	return new(big.Rat).SetInt(num)
}

// ratToDecimal renders an exact rational at a DECLARED scale, rounding
// half-even, and reports whether rounding actually happened.
//
// rounded=false means the value was exact at that scale — which is the common
// case for `+`, `-` and `*` and is why a labelled-as-rounded result is
// informative rather than noise.
//
// The algorithm is deliberately all-integer: scale the numerator by 10^scale,
// divide by the denominator with remainder, and decide the last digit from the
// remainder alone. Compare 2×|remainder| with the denominator — below is down,
// above is up, EQUAL is the half-way case and goes to the even quotient. That
// last clause is the whole difference between half-even and the rounding every
// convenience function in the standard library would have given instead.
func ratToDecimal(r *big.Rat, scale int32) (value Decimal, rounded bool) {
	if r == nil {
		return Decimal{}, false
	}
	if scale < 0 {
		scale = 0
	}

	num := new(big.Int).Set(r.Num())
	den := new(big.Int).Set(r.Denom())

	negative := num.Sign() < 0
	if negative {
		num.Neg(num)
	}

	num.Mul(num, pow10(int64(scale)))

	quo := new(big.Int)
	rem := new(big.Int)
	quo.QuoRem(num, den, rem)

	if rem.Sign() != 0 {
		rounded = true
		twice := new(big.Int).Lsh(rem, 1)
		switch twice.Cmp(den) {
		case 1:
			quo.Add(quo, big.NewInt(1))
		case 0:
			// Exactly half: round to even.
			if quo.Bit(0) == 1 {
				quo.Add(quo, big.NewInt(1))
			}
		}
	}

	if negative {
		quo.Neg(quo)
	}
	return NewDecimal(quo, scale), rounded
}

// ratIsIntegral reports whether a rational is a whole number — FR-144's
// precondition for `%`.
func ratIsIntegral(r *big.Rat) bool {
	return r != nil && r.IsInt()
}

// ratMod is FR-144's `%`: defined over integers only.
//
// ok=false means an operand was not whole, and the caller raises the problem
// naming round() as the remedy rather than truncating — truncating is how a
// modulus quietly answers a question nobody asked.
func ratMod(a, b *big.Rat) (*big.Rat, bool) {
	if !ratIsIntegral(a) || !ratIsIntegral(b) || b.Sign() == 0 {
		return nil, false
	}
	x := new(big.Int).Set(a.Num())
	y := new(big.Int).Set(b.Num())
	// Go's Rem takes the sign of the dividend, which is the behaviour every
	// language in this grammar's neighbourhood has for `%`.
	m := new(big.Int).Rem(x, y)
	return new(big.Rat).SetInt(m), true
}
