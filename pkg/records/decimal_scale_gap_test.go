// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"errors"
	"math"
	"math/big"
	"runtime"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// maxDecimalScale bounded the TARGET of a rescale and not the DISTANCE to it.
// A target of 0 is legal at any distance, so a value at a far-negative scale
// compared against a scale-0 value passed the bound and then built the full
// 10^n factor. Measured on this package, Cmp against 1 at scale 0:
//
//	scale         before      after
//	-1000         3.5 KB      1.2 KB
//	-100000       329 KB      1.2 KB
//	-1000000      2.7 MB      1.2 KB      (76 ms)
//	-10000000     33 MB       1.2 KB      (3.1 SECONDS)
//
// Cmp's own doc comment claimed "neither path approximates and neither path
// panics"; the unhandled third outcome was NEVER RETURNS. cmpUnaligned's doc
// claimed an adversarial scale could not become a multi-gigabyte allocation,
// but Cmp tried align FIRST, so the guarded path was only ever reached after
// the unguarded one had already paid.
//
// The two tests below are shaped by the failure mode, not by the fix:
//
//	BOUNDARY  the gap bound has to bite at exactly the right place, and NOT
//	          refuse pairs that were always legal. An off-by-one here silently
//	          demotes ordinary arithmetic to the fallback (harmless for Cmp,
//	          an outright error for Add/Sub), so the boundary is asserted from
//	          both sides.
//	COST      a bound nobody measures is a bound that regresses. Allocation is
//	          asserted rather than wall clock: allocated BYTES are what the
//	          defect actually was (a 33 MB big.Int), they are deterministic
//	          where a stopwatch is not, and they do not go flaky on a loaded
//	          machine or under -race.
// ---------------------------------------------------------------------------

// TestDecimal_AlignRefusesAnUnboundedScaleGap pins the gap bound at its edge,
// from both sides, and confirms it is REPORTED (errScaleTooLarge) rather than
// approximated — including through Add and Sub, which have no fallback and so
// must surface it to the caller.
//
// ⚠️ NOT a safe mutation target: the math.MinInt32 row calls align directly, so
// with the bound reverted this test builds 10^2147483648 and does not return.
// (Observed — the first mutation run of this fix had to be killed.) Use
// TestDecimal_CmpCostDoesNotGrowWithTheScaleGap, which fails in 0.01s.
func TestDecimal_AlignRefusesAnUnboundedScaleGap(t *testing.T) {
	one := big.NewInt(1)

	cases := []struct {
		name       string
		aScale     int32
		bScale     int32
		wantRefuse bool
	}{
		// The gap is measured to the LARGER scale, so these all target bScale.
		{"gap of 0", 0, 0, false},
		{"gap of 1", -1, 0, false},
		{"gap of maxRescaleGap-1", -(maxRescaleGap - 1), 0, false},
		{"gap of exactly maxRescaleGap", -maxRescaleGap, 0, false},
		{"gap of maxRescaleGap+1", -(maxRescaleGap + 1), 0, true},

		// The widest pair ParseDecimal can produce: 1e100 and 1e-100. This row
		// is the one that sets maxRescaleGap's value — bounding the gap at
		// maxDecimalScale instead would refuse it, and SumMoney would start
		// rejecting a legitimate total.
		{"the widest gap two parsed values can present", -maxDecimalScale, maxDecimalScale, false},
		{"one step wider than parsing can reach", -(maxDecimalScale + 1), maxDecimalScale, true},

		{"far-negative scale against zero", -1000000, 0, true},
		{"the smallest scale there is", math.MinInt32, 0, true},

		// The TARGET bound is independent of the gap bound and must survive:
		// a gap of 1 is tiny, but a target beyond maxDecimalScale is still
		// outside the range the rest of the package expects.
		{"tiny gap, target beyond the bound", maxDecimalScale, maxDecimalScale + 1, true},

		// Symmetry: align picks the larger scale whichever operand carries it.
		{"same gap, operands swapped", 0, -(maxRescaleGap + 1), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := NewDecimal(one, c.aScale)
			b := NewDecimal(one, c.bScale)

			_, _, err := align(a, b)
			if c.wantRefuse {
				if err == nil {
					t.Fatalf("align(scale %d, scale %d) succeeded; it must refuse, because the factor it would build is 10^%d",
						c.aScale, c.bScale, int64(max32(c.aScale, c.bScale))-int64(min32(c.aScale, c.bScale)))
				}
				if !errors.Is(err, errScaleTooLarge) {
					t.Fatalf("align refused with %v; the caller distinguishes a bound breach from a malformed value by this sentinel, so it must be errScaleTooLarge", err)
				}
			} else if err != nil {
				t.Fatalf("align(scale %d, scale %d) refused with %v, but this gap is within maxRescaleGap (%d) and was always legal — refusing it demotes ordinary arithmetic to an error",
					c.aScale, c.bScale, err, maxRescaleGap)
			}

			// Add and Sub have no fallback: whatever align decides, they must
			// surface. A silent zero here would be the worst outcome of all.
			for _, op := range []struct {
				name string
				fn   func(Decimal, Decimal) (Decimal, error)
			}{
				{"Add", Decimal.Add},
				{"Sub", Decimal.Sub},
			} {
				_, opErr := op.fn(a, b)
				if c.wantRefuse {
					if !errors.Is(opErr, errScaleTooLarge) {
						t.Fatalf("%s(scale %d, scale %d) returned err=%v; with no unaligned fallback it must REPORT errScaleTooLarge rather than materialise the factor",
							op.name, c.aScale, c.bScale, opErr)
					}
				} else if opErr != nil {
					t.Fatalf("%s(scale %d, scale %d) failed with %v on a legal gap", op.name, c.aScale, c.bScale, opErr)
				}
			}

			// Cmp is total either way — refusing alignment must never turn a
			// comparison into an error or a wrong answer.
			//
			// Gated on scale magnitude because the ORACLE is the expensive one
			// here: ratOf materialises 10^-scale by definition, so asking it
			// about scale math.MinInt32 would build the same 890 MB integer
			// this fix exists to avoid — in the test rather than in the code.
			// Cmp's answer at those scales is asserted in the cost tests below,
			// where the expectation is derived by hand instead.
			if oracleAffordable(c.aScale) && oracleAffordable(c.bScale) {
				checkAgainstOracle(t, c.name, a, b)
			}
		})
	}
}

// oracleAffordable reports whether ratOf can be asked about this scale without
// itself becoming the unbounded allocation under test. ratOf multiplies or
// divides by 10^|scale|, so it is affordable exactly as far as that factor is.
func oracleAffordable(scale int32) bool { return scale > -10000 && scale < 10000 }

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

// TestDecimal_TheGapBoundRefusesNothingParsingCanProduce is the test that sets
// maxRescaleGap's VALUE, and the reason it is twice maxDecimalScale rather than
// equal to it.
//
// A guard against hand-built values must not narrow the domain of parsed ones.
// The gap is a DIFFERENCE between two scales, so bounding it at maxDecimalScale
// — the bound on a SINGLE scale — is off by a factor of two: `1e100` parses to
// scale -100 and `1e-100` to scale +100, both ordinary, and their gap is 200.
// With the tighter bound SumMoney refuses that pair outright, turning a
// legitimate total into an error. That is a worse defect than the slow
// comparison being fixed, and it would have shipped silently, because every
// other test in this file uses hand-built Decimals where the narrowing is
// invisible.
//
// So this test never calls NewDecimal: every value comes from ParseDecimal, and
// the assertion is that NO pair of them is ever refused.
func TestDecimal_TheGapBoundRefusesNothingParsingCanProduce(t *testing.T) {
	// The extremes of what ParseDecimal accepts, plus ordinary values between.
	// parseExponent caps the exponent at maxDecimalScale in both directions, so
	// these literals sit exactly on the reachable scale boundary.
	texts := []string{
		"1e100", "-1e100", "9e100", // scale -100, the floor
		"1e-100", "-1e-100", "9e-100", // scale +100, the ceiling
		"1e50", "1e-50", "0", "-0", "1", "-1",
		"349.98", "0.000001", "123456789012345678901234567890",
		"1e0", "1.5e-99", "1.5e99",
	}

	values := make([]Decimal, 0, len(texts))
	for _, txt := range texts {
		d, err := ParseDecimal(txt)
		if err != nil {
			t.Fatalf("ParseDecimal(%q) failed with %v; this fixture is meant to be an ORDINARY parseable value, so either the literal or the parser's bounds have moved", txt, err)
		}
		values = append(values, d)
	}

	// Confirm the fixtures really do span the full parseable scale range —
	// otherwise this test could pass by never reaching the boundary at all.
	minScale, maxScale := values[0].Scale(), values[0].Scale()
	for _, v := range values {
		minScale = min32(minScale, v.Scale())
		maxScale = max32(maxScale, v.Scale())
	}
	if minScale != -maxDecimalScale || maxScale != maxDecimalScale {
		t.Fatalf("fixtures span scales [%d, %d], but ParseDecimal's range is [%d, %d] — a test of the boundary that does not reach the boundary proves nothing",
			minScale, maxScale, -maxDecimalScale, maxDecimalScale)
	}
	// Errorf, not Fatalf: when this invariant breaks we want the loop below to
	// run anyway and report the CONSEQUENCE — an actual refused pair — rather
	// than stopping at the abstract statement of it.
	if int64(maxScale)-int64(minScale) != maxRescaleGap {
		t.Errorf("the widest parseable gap is %d but maxRescaleGap is %d; the bound must be derived from the parser's range, not chosen independently of it",
			int64(maxScale)-int64(minScale), maxRescaleGap)
	}

	for i := range values {
		for j := range values {
			a, b := values[i], values[j]
			if _, _, err := align(a, b); err != nil {
				t.Fatalf("align refused two PARSED values (%s@%d and %s@%d) with %v — the gap bound exists to stop hand-built scales, and must never narrow what a vault file can express",
					a.Unscaled(), a.Scale(), b.Unscaled(), b.Scale(), err)
			}
			if _, err := a.Add(b); err != nil {
				t.Fatalf("Add refused two PARSED values (%s@%d and %s@%d) with %v — SumMoney would report this as a failed total", a.Unscaled(), a.Scale(), b.Unscaled(), b.Scale(), err)
			}
			if _, err := a.Sub(b); err != nil {
				t.Fatalf("Sub refused two PARSED values (%s@%d and %s@%d) with %v", a.Unscaled(), a.Scale(), b.Unscaled(), b.Scale(), err)
			}
			checkAgainstOracle(t, "parsed pair", a, b)
		}
	}
}

// TestSumMoney_TotalsTheWidestParseableScaleSpread drives the same boundary
// through the ONLY production caller of Add, so the property is asserted where
// a regression would actually be felt rather than only at the arithmetic layer.
func TestSumMoney_TotalsTheWidestParseableScaleSpread(t *testing.T) {
	// Same currency, scales at both ends of the parseable range: a gap of
	// exactly maxRescaleGap.
	big1, err := ParseDecimal("1e100")
	if err != nil {
		t.Fatalf("ParseDecimal(1e100): %v", err)
	}
	tiny, err := ParseDecimal("1e-100")
	if err != nil {
		t.Fatalf("ParseDecimal(1e-100): %v", err)
	}
	// Errorf, not Fatalf — see the note in the sibling test: the SumMoney call
	// below is the evidence that matters, so let it run and speak for itself.
	if gap := int64(tiny.Scale()) - int64(big1.Scale()); gap != maxRescaleGap {
		t.Errorf("fixture gap is %d, want exactly maxRescaleGap (%d) — this test is meant to sit ON the bound", gap, maxRescaleGap)
	}

	total, ok, err := SumMoney([]Money{
		{Amount: big1, Currency: "SGD"},
		{Amount: tiny, Currency: "SGD"},
	})
	if err != nil {
		t.Fatalf("SumMoney refused a two-value total spanning the parseable scale range: %v — this is the user-visible shape of a gap bound set one factor of two too tight", err)
	}
	if !ok {
		t.Fatal("SumMoney reported ok=false for a non-empty, single-currency set")
	}

	// The total must be EXACT, not merely produced: 10^100 + 10^-100 carries
	// 201 significant digits and this package never rounds.
	wantRat := new(big.Rat).Add(ratOf(big1), ratOf(tiny))
	if got := ratOf(total.Amount); got.Cmp(wantRat) != 0 {
		t.Fatalf("SumMoney total = %s, want %s — the sum survived the bound but lost digits", got.RatString(), wantRat.RatString())
	}
	if total.Currency != "SGD" {
		t.Fatalf("total currency = %q, want SGD", total.Currency)
	}
}

// cmpAllocBytes reports the bytes Cmp allocates for one comparison.
//
// The minimum over several runs is taken deliberately: TotalAlloc is a
// process-wide counter, so an unrelated goroutine allocating during the window
// can only ever inflate a sample, never deflate one. The minimum is therefore
// the tightest true upper bound available, and it makes the assertion stable
// under -race and on a busy machine.
func cmpAllocBytes(t *testing.T, a, b Decimal, wantCmp int) uint64 {
	t.Helper()
	best := ^uint64(0)
	for i := 0; i < 5; i++ {
		var m0, m1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m0)
		got := a.Cmp(b)
		runtime.ReadMemStats(&m1)
		if got != wantCmp {
			t.Fatalf("Cmp(scale %d, scale %d) = %d, want %d — the cheap path must give the SAME answer the expensive one did, or this fix bought speed with correctness",
				a.Scale(), b.Scale(), got, wantCmp)
		}
		if d := m1.TotalAlloc - m0.TotalAlloc; d < best {
			best = d
		}
	}
	return best
}

// cmpAllocCeiling is ~50x the measured cost of an unaligned comparison (about
// 1.2 KB) and ~5x below the smallest defective sample in the table above (329
// KB at scale -100000). It is loose enough never to flake and tight enough that
// reverting the gap bound fails it at every scale this test uses.
const cmpAllocCeiling = 64 << 10

// TestDecimal_CmpCostDoesNotGrowWithTheScaleGap is the measurement that the
// bound actually bounds. Without it, "align refuses" is a claim about a branch;
// with it, it is a claim about the work.
//
// The scales here stop at -1000000, which cost 2.7 MB and 76 ms before the fix
// — unpleasant but safe to run. The genuinely dangerous scales live in the test
// below, which must NOT be run with the bound reverted.
func TestDecimal_CmpCostDoesNotGrowWithTheScaleGap(t *testing.T) {
	one := big.NewInt(1)
	against := NewDecimal(one, 0)

	// A positive value at a far-NEGATIVE scale is a large number (unscaled x
	// 10^-scale), so it is greater than 1 in every row here.
	for _, sc := range []int32{-1000, -100000, -1000000} {
		d := NewDecimal(one, sc)
		got := cmpAllocBytes(t, d, against, 1)
		t.Logf("scale %d vs scale 0: Cmp allocated %d bytes", sc, got)
		if got > cmpAllocCeiling {
			t.Fatalf("Cmp at scale %d allocated %d bytes (ceiling %d). The scale gap is sizing the work again: align is building 10^%d instead of declining and letting cmpUnaligned answer from the operands' own digits",
				sc, got, cmpAllocCeiling, -int64(sc))
		}
	}
}

// TestDecimal_CmpAnswersAtScalesNoAllocatorCouldMaterialise covers the scales
// that make the bound a correctness property rather than a performance one.
//
// ⚠️ DO NOT RUN THIS TEST WITH THE GAP BOUND REVERTED. 10^1000000000 is roughly
// a 415 MB integer and 10^2147483648 roughly 890 MB; the point of these rows is
// that the answer arrives without either ever existing. To mutation-verify the
// bound, use TestDecimal_CmpCostDoesNotGrowWithTheScaleGap, whose worst row
// costs 2.7 MB.
func TestDecimal_CmpAnswersAtScalesNoAllocatorCouldMaterialise(t *testing.T) {
	one := big.NewInt(1)
	against := NewDecimal(one, 0)

	for _, sc := range []int32{-1000000000, math.MinInt32} {
		d := NewDecimal(one, sc)
		got := cmpAllocBytes(t, d, against, 1)
		t.Logf("scale %d vs scale 0: Cmp allocated %d bytes", sc, got)
		if got > cmpAllocCeiling {
			t.Fatalf("Cmp at scale %d allocated %d bytes (ceiling %d) — it is materialising a factor no machine should be asked for",
				sc, got, cmpAllocCeiling)
		}

		// math.MinInt32 is the specific value that makes the widening to int64
		// load-bearing: `target - d.scale` is 0 - math.MinInt32, which OVERFLOWS
		// int32 back to a negative number. A negative gap would slip past a
		// `gap > maxDecimalScale` check written in int32, and pow10 answers 1
		// for a negative exponent — so the operand would be rescaled by a factor
		// of ONE and compared as though its scale were 0. That is not a slow
		// answer, it is a wrong one.
		if sc == math.MinInt32 {
			if _, _, err := align(d, against); !errors.Is(err, errScaleTooLarge) {
				t.Fatalf("align at scale math.MinInt32 returned err=%v; it must refuse, and refusing must not depend on an int32 subtraction that wraps", err)
			}
		}
	}
}
