// Omnipus — a `formula.<name>` reference in a FILTER: what parses into a real
// leaf, and what is refused because the view format has nothing to build.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import "testing"

// ---------------------------------------------------------------------------
// THE HOLE THIS FILE CLOSES
//
// `formula.<name> OP operand` is a new leaf shape, and the operand check is
// where a new comparison shape goes wrong. unquoteLiteral hands back an
// UNQUOTED operand unchanged, so `formula.age > formula.threshold` and
// `formula.age > cutoff_days` both looked like ordinary comparisons and built
//
//	{property: formula.age, op: >, value: "formula.threshold"}
//
// — a NUMBER compared against an eighteen-character string. VaultFilterNode's
// `value` is always a literal; the format has no property-to-property
// comparison at all. So there is nothing faithful to build and the clause is
// refused by name, which disables the view. The alternative on offer was a
// clause that runs, matches nothing, and reads as a correct empty view.
// ---------------------------------------------------------------------------

// TestFormulaCompareLeaf_RefusesANonLiteralRightHandSide is the reproduction.
func TestFormulaCompareLeaf_RefusesANonLiteralRightHandSide(t *testing.T) {
	for _, expr := range []string{
		`formula.age > formula.threshold`,
		`formula.age > cutoff_days`,
		`formula.age > file.size`,
		`formula.days_to_due <= due_date`,
	} {
		got := parseLeaf(expr)
		if got.Kind != leafUntranslatable {
			t.Errorf("parseLeaf(%q) = %+v, want UNTRANSLATABLE — the right-hand side NAMES another value rather than being a literal, and the view format's `value` is always a literal, so comparing against the text %q is a clause the operator never wrote",
				expr, got, got.Filter.Values)
		}
	}
}

// TestFormulaCompareLeaf_CarriesAComparisonAgainstALiteral is the other half:
// the shapes that must NOT be refused, or carrying the `formulas:` block buys
// nothing. Every expression here is one the founder's own bases contain.
func TestFormulaCompareLeaf_CarriesAComparisonAgainstALiteral(t *testing.T) {
	cases := []struct {
		expr     string
		wantProp string
		wantOp   string
		wantVal  string
		wantNeg  bool
	}{
		// Contracts.base and Connectors.base — the expiry windows.
		{`formula.days_to_expiry <= 14`, "formula.days_to_expiry", "lte", "14", false},
		{`formula.days_to_expiry >= 0`, "formula.days_to_expiry", "gte", "0", false},
		// Tasks.base — "due today" and the seven-day window.
		{`formula.days_until_due == 0`, "formula.days_until_due", "eq", "0", false},
		{`formula.days_until_due <= 7`, "formula.days_until_due", "lte", "7", false},
		// CRM.base — the stale-contact threshold.
		{`formula.days_since_refresh > 365`, "formula.days_since_refresh", "gt", "365", false},
		// Compliance.base / Tasks.base — a boolean formula.
		{`formula.is_overdue == true`, "formula.is_overdue", "eq", "true", false},
		// A negation desugars the same way an ordinary one does.
		{`formula.is_overdue != true`, "formula.is_overdue", "eq", "true", true},
		// A quoted literal is a literal whatever it spells.
		{`formula.team_name == "T0 · Chief-of-Staff"`, "formula.team_name", "eq", "T0 · Chief-of-Staff", false},
		// A negative bound is a number, not a name.
		{`formula.days_to_expiry > -30`, "formula.days_to_expiry", "gt", "-30", false},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			got := parseLeaf(tc.expr)
			if got.Kind != leafFilter {
				t.Fatalf("parseLeaf(%q) kind = %v, want a translatable filter leaf — the whole point of carrying a base's `formulas:` block is that these resolve", tc.expr, got.Kind)
			}
			// THE FULL DOTTED NAME, not the bare one. Every property position
			// in the view format addresses a formula as `formula.<name>`
			// (FR-140), and a leaf carrying the bare name would resolve against
			// a declared property of that name if one happened to exist.
			if got.Filter.Property != tc.wantProp {
				t.Errorf("property = %q, want %q", got.Filter.Property, tc.wantProp)
			}
			if got.Filter.Op != tc.wantOp {
				t.Errorf("op = %q, want %q", got.Filter.Op, tc.wantOp)
			}
			if len(got.Filter.Values) != 1 || got.Filter.Values[0] != tc.wantVal {
				t.Errorf("values = %q, want [%q]", got.Filter.Values, tc.wantVal)
			}
			if got.Filter.Negate != tc.wantNeg {
				t.Errorf("negate = %v, want %v", got.Filter.Negate, tc.wantNeg)
			}
		})
	}
}

// TestFormulaCompareLeaf_StillRefusesTheShapesWithNoLeaf pins what carrying
// formulas did NOT widen. Each of these matches something formula-shaped and
// has no filter leaf behind it, so it must keep going through the
// untranslatable-marker path rather than being half-recognised.
func TestFormulaCompareLeaf_StillRefusesTheShapesWithNoLeaf(t *testing.T) {
	cases := map[string]string{
		`formula.is_overdue`:               "a bare truthiness test on a computed property",
		`!formula.is_overdue`:              "its negation",
		`formula.team_name.contains("T0")`: "a method call, not a comparison",
		`formula.days_to_due == ""`:        "an absent formula result and the empty string are not the same thing to compare against (R-2 versus FR-007a)",
		`formula.days_to_due <= date(x)`:   "a call in operand position",
		`formula.a <= 5 && formula.b > 1`:  "a compound expression, refused before any pattern runs",
	}
	for expr, why := range cases {
		t.Run(expr, func(t *testing.T) {
			if got := parseLeaf(expr); got.Kind != leafUntranslatable {
				t.Errorf("parseLeaf(%q) = %+v, want UNTRANSLATABLE — %s", expr, got, why)
			}
		})
	}
}
