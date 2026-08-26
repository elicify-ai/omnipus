// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"math/big"
	"testing"
)

// TestDecimal_CmpNeverReportsUnequalValuesAsEqual guards a defect where two
// clearly different numbers compared EQUAL.
//
// Cmp aligns both operands to a common scale and compares. When alignment
// failed it fell back to `d.Sign() - o.Sign()`, justified by a comment saying
// "both operands came from bounded parsing, so this is unreachable in
// practice". The type does not enforce that: NewDecimal is exported and takes
// any scale. So NewDecimal(1, 200) and NewDecimal(999, 199) — 1e-200 against
// 9.99e-197 — reported Equal() == true, in the path money comparisons run
// through. The same fallback could also return ±2, contradicting Cmp's own
// documented -1/0/+1 contract.
func TestDecimal_CmpNeverReportsUnequalValuesAsEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b Decimal
		want int
	}{
		{"the reported case: 1e-200 vs 999e-199", NewDecimal(big.NewInt(1), 200), NewDecimal(big.NewInt(999), 199), -1},
		{"reversed", NewDecimal(big.NewInt(999), 199), NewDecimal(big.NewInt(1), 200), 1},
		{"same value, different scale", NewDecimal(big.NewInt(100), 2), NewDecimal(big.NewInt(1), 0), 0},
		{"negative beats positive across wide scales", NewDecimal(big.NewInt(-1), 200), NewDecimal(big.NewInt(1), 199), -1},
		{"two negatives, wide scales", NewDecimal(big.NewInt(-999), 199), NewDecimal(big.NewInt(-1), 200), -1},
		{"zero against a tiny positive", NewDecimal(big.NewInt(0), 0), NewDecimal(big.NewInt(1), 200), -1},
		{"zero against a tiny negative", NewDecimal(big.NewInt(0), 0), NewDecimal(big.NewInt(-1), 200), 1},
		{"both zero, different scales", NewDecimal(big.NewInt(0), 5), NewDecimal(big.NewInt(0), 90), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.a.Cmp(c.b)
			if got != c.want {
				t.Fatalf("Cmp = %d, want %d — a wrong comparison here is a silently wrong answer, not an error", got, c.want)
			}
			if got < -1 || got > 1 {
				t.Fatalf("Cmp returned %d; the contract is -1, 0 or +1", got)
			}
			if (got == 0) != c.a.Equal(c.b) {
				t.Fatalf("Cmp and Equal disagree: Cmp=%d Equal=%v", got, c.a.Equal(c.b))
			}
		})
	}
}
