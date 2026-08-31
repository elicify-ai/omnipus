// Omnipus — spec test 100: FR-143a, FR-148, R-18 — cycles and types refused at
// write time; and FR-146's three caps.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"strings"
	"testing"
)

// TestFormula_CyclesAndTypesRefusedAtWrite is spec test 100.
//
// The two faults it covers are unrelated in mechanism and identical in
// consequence: BOTH are invisible to every other check in the layer, and both
// produce a failure that has no error attached to it.
//
//   - A CYCLE (FR-148) parses clean, types clean, and passes every per-formula
//     bound — then recurses forever. An unspecified HANG has no error, no wrong
//     answer and no timeout; the query simply never returns.
//   - A MIXED-TYPE `if()` (FR-143a) parses clean and evaluates fine, and then
//     compares FALSE under R-1's different-domains rule on whichever records
//     took the other branch, reporting NOTHING. A silently wrong answer wearing
//     a type system.
func TestFormula_CyclesAndTypesRefusedAtWrite(t *testing.T) {
	schema := formulaFixtureSchema()

	t.Run("FR-148 — a two-formula cycle is refused NAMING THE PATH", func(t *testing.T) {
		// The specification's own example, verbatim.
		set, errs := ValidateFormulaSet(map[string]string{
			"a": "formula.b + 1",
			"b": "formula.a + 1",
		}, schema)

		if len(errs) == 0 {
			t.Fatal("FR-148: `a: formula.b + 1` / `b: formula.a + 1` was ACCEPTED. It parses clean, types clean and passes every bound — and then recurses forever")
		}
		if set != nil {
			t.Fatal("FR-148: a cycle refusal must store nothing")
		}

		var cycle *FormulaError
		for _, e := range errs {
			if e.Code == FormulaErrCycle {
				cycle = e
			}
		}
		if cycle == nil {
			t.Fatalf("FR-148: the refusal is not coded as a cycle. Got: %v", formulaErrorCodes(errs))
		}

		// "The refusal names its path (`a → b → a`)." The path is derived from
		// the specification's wording, not from what the code emitted.
		wantPath := []string{"a", "b", "a"}
		if got := strings.Join(cycle.Path, ","); got != strings.Join(wantPath, ",") {
			t.Errorf("FR-148: the refusal names the path %v, want %v", cycle.Path, wantPath)
		}
		if !strings.Contains(cycle.Error(), "a → b → a") {
			t.Errorf("FR-148: the refusal must NAME the path in its message so an author does not have to solve the puzzle by hand; got:\n  %s", cycle.Error())
		}

		// ONE refusal, not two. The same cycle found from `a` and from `b` is
		// one problem, and reporting it twice teaches an author to skim.
		cycles := 0
		for _, e := range errs {
			if e.Code == FormulaErrCycle {
				cycles++
			}
		}
		if cycles != 1 {
			t.Errorf("FR-148: one cycle produced %d refusals; the same cycle discovered from two entry points is ONE problem", cycles)
		}
	})

	t.Run("FR-148 — a self-reference is a cycle of length one", func(t *testing.T) {
		_, errs := ValidateFormulaSet(map[string]string{"a": "formula.a + 1"}, schema)
		if !hasCode(errs, FormulaErrCycle) {
			t.Fatalf("FR-148: `a: formula.a + 1` refers to itself and must be refused; got %v", formulaErrorCodes(errs))
		}
	})

	t.Run("FR-148 — a three-formula cycle is refused naming all three", func(t *testing.T) {
		_, errs := ValidateFormulaSet(map[string]string{
			"a": "formula.b + 1",
			"b": "formula.c + 1",
			"c": "formula.a + 1",
		}, schema)
		var cycle *FormulaError
		for _, e := range errs {
			if e.Code == FormulaErrCycle {
				cycle = e
			}
		}
		if cycle == nil {
			t.Fatalf("FR-148: a three-formula cycle must be refused; got %v", formulaErrorCodes(errs))
		}
		for _, name := range []string{"a", "b", "c"} {
			if !strings.Contains(strings.Join(cycle.Path, ","), name) {
				t.Errorf("FR-148: the path %v omits %q — a partial path is a puzzle, not a diagnosis", cycle.Path, name)
			}
		}
	})

	// THE CONTROL. Without this the cycle check could be "refuse every
	// cross-formula reference" and every assertion above would still pass.
	t.Run("the control — a DIAMOND is not a cycle", func(t *testing.T) {
		set, errs := ValidateFormulaSet(map[string]string{
			"base":  "amount * 2",
			"left":  "formula.base + 1",
			"right": "formula.base + 2",
			"top":   "formula.left + formula.right",
		}, schema)
		if len(errs) != 0 {
			t.Fatalf("two formulas legitimately referencing a third is NOT a cycle, but it was refused: %v", formulaErrorMessages(errs))
		}
		if set.Len() != 4 {
			t.Fatalf("the set holds %d formulas, want 4", set.Len())
		}
		// And the shared formula's nodes are counted ONCE (FR-148's last
		// clause), which falls out of summing each tree: `base` is 3 nodes
		// (amount, 2, *), left and right are 3 each (ref, literal, +), top is 3
		// (ref, ref, +) — 12 in total, NOT the 18 an expand-then-count
		// implementation would report.
		if got := set.TotalNodes(); got != 12 {
			t.Errorf("FR-148: the view's total is %d nodes, want 12 — a referenced formula's nodes must be counted ONCE, since it is memoized", got)
		}
	})

	t.Run("FR-143a — if() with disagreeing branches is refused naming BOTH types", func(t *testing.T) {
		// The specification's own example, verbatim: `if(c, 1, "x")`.
		set, errs := ValidateFormulaSet(map[string]string{
			"f": `if(amount > 1, 1, "x")`,
		}, schema)
		if len(errs) == 0 {
			t.Fatal(`FR-143a: if(c, 1, "x") was ACCEPTED. It yields a number on some records and text on others, which compares FALSE under R-1 with NO problem reported`)
		}
		if set != nil {
			t.Fatal("FR-143a: a type refusal must store nothing")
		}
		msg := errs[0].Error()
		if errs[0].Code != FormulaErrType {
			t.Errorf("the refusal is coded %q, want %q", errs[0].Code, FormulaErrType)
		}
		for _, want := range []string{string(FormulaNumber), string(FormulaText)} {
			if !strings.Contains(msg, want) {
				t.Errorf("FR-143a: the refusal must name BOTH branch types; %q is missing from:\n  %s", want, msg)
			}
		}
	})

	t.Run("FR-143a — a two-argument if() is FINE: the missing branch is absence", func(t *testing.T) {
		set, errs := ValidateFormulaSet(map[string]string{"f": "if(amount > 1, amount)"}, schema)
		if len(errs) != 0 {
			t.Fatalf(`FR-143a says branches must agree "or one be absent"; a two-argument if() has an absent branch and must be accepted. Got: %v`, formulaErrorMessages(errs))
		}
		d, _ := set.Get("f")
		if d.Type != FormulaNumber {
			t.Errorf("the surviving branch decides the type: got %q, want %q", d.Type, FormulaNumber)
		}
	})

	t.Run("FR-143a — a formula's declared type is STATIC and visible", func(t *testing.T) {
		// Each expected type is derived from the specification's rules, not
		// from running the inferrer:
		//   `amount * 2`        — arithmetic over a decimal: number
		//   `name`              — a text property: text
		//   `due < today()`     — a comparison: boolean
		//   `link(name)`        — a presentation function: presentation
		//   `list(1, 2, 3)`     — list() makes the arity many; elements number
		for _, tc := range []struct {
			src   string
			typ   FormulaType
			arity FormulaArity
		}{
			{"amount * 2", FormulaNumber, ArityOne},
			{"name", FormulaText, ArityOne},
			{"due < today()", FormulaBoolean, ArityOne},
			{"link(name)", FormulaPresentation, ArityOne},
			{"list(1, 2, 3)", FormulaNumber, ArityMany},
			{"sizes", FormulaNumber, ArityMany},
			{"owner", FormulaLink, ArityOne},
			{"file.mtime", FormulaDate, ArityOne},
			{"file.tags", FormulaText, ArityMany},
		} {
			set, errs := ValidateFormulaSet(map[string]string{"f": tc.src}, schema)
			if len(errs) != 0 {
				t.Errorf("%q should type cleanly; got %v", tc.src, formulaErrorMessages(errs))
				continue
			}
			d, _ := set.Get("f")
			if d.Type != tc.typ || d.Arity != tc.arity {
				t.Errorf("%q declares (%s, %s), want (%s, %s)", tc.src, d.Type, d.Arity, tc.typ, tc.arity)
			}
		}
	})

	t.Run("R-16 — a comparison over a presentation value is refused STATICALLY", func(t *testing.T) {
		for _, src := range []string{
			`link(name) == "x"`,
			`icon(name) != "x"`,
			`format(amount, "{}") == "3"`,
			`file.asLink() == "x"`,
		} {
			_, errs := ValidateFormulaSet(map[string]string{"f": src}, schema)
			if !hasCode(errs, FormulaErrType) {
				t.Errorf("R-16/FR-215: %q compares a DISPLAY value and must be refused; got %v", src, formulaErrorCodes(errs))
				continue
			}
			if !strings.Contains(errs[0].Error(), "R-16") {
				t.Errorf("R-16: the refusal must name the rule; got:\n  %s", errs[0].Error())
			}
		}
	})

	t.Run("R-1 — a cross-domain comparison is refused rather than answered FALSE", func(t *testing.T) {
		_, errs := ValidateFormulaSet(map[string]string{"f": `amount == name`}, schema)
		if !hasCode(errs, FormulaErrType) {
			t.Fatalf("R-1: comparing a number with text answers FALSE on every record with nothing reported; a formula must refuse it at write time. Got %v", formulaErrorCodes(errs))
		}
	})

	t.Run("R-13 — an ordering operator over a many operand is refused", func(t *testing.T) {
		_, errs := ValidateFormulaSet(map[string]string{"f": "sizes > 3"}, schema)
		if !hasCode(errs, FormulaErrType) {
			t.Fatalf("R-13: the ordering operators are not defined over a list; got %v", formulaErrorCodes(errs))
		}
	})

	t.Run("FR-144 — %% over a fractional literal is refused naming round()", func(t *testing.T) {
		_, errs := ValidateFormulaSet(map[string]string{"f": "quantity % 2.5"}, schema)
		if !hasCode(errs, FormulaErrType) {
			t.Fatalf("FR-144: `%%` is defined over integers only; got %v", formulaErrorCodes(errs))
		}
		if !strings.Contains(errs[0].Error(), "round()") {
			t.Errorf("FR-144: the refusal must name round() as the remedy; got:\n  %s", errs[0].Error())
		}
	})

	t.Run("FR-144 — a scale computed per record is not a DECLARATION", func(t *testing.T) {
		_, errs := ValidateFormulaSet(map[string]string{"f": "toFixed(amount, quantity)"}, schema)
		if !hasCode(errs, FormulaErrType) {
			t.Fatalf("FR-144 requires the scale to be DECLARED; a property is not a declaration. Got %v", formulaErrorCodes(errs))
		}
	})

	t.Run("FR-143a — a property the view cannot type is refused, never assumed", func(t *testing.T) {
		// A TYPELESS view (FR-018d) has no schema. A formula over `file.*` and
		// literals still types; a formula over a note property cannot, and the
		// honest answer is a refusal rather than a guessed type.
		set, errs := ValidateFormulaSet(map[string]string{"f": "file.size + 1"}, nil)
		if len(errs) != 0 {
			t.Errorf("a typeless view can still carry a formula over file metadata: %v", formulaErrorMessages(errs))
		} else if d, _ := set.Get("f"); d.Type != FormulaNumber {
			t.Errorf("file.size + 1 is a number; got %q", d.Type)
		}

		_, errs = ValidateFormulaSet(map[string]string{"f": "amount + 1"}, nil)
		if !hasCode(errs, FormulaErrUnknownReference) {
			t.Fatalf("FR-143a: a property operand must have a DECLARED type; a typeless view must refuse rather than assume one. Got %v", formulaErrorCodes(errs))
		}
	})
}

// TestFormula_ViewBudgetCapsAreEnforced is FR-146's three caps.
//
// The numbers are the specification's, written out here rather than referenced
// through the constants: a test that reads its expectation from the same
// constant the code reads passes for any value of the constant, including a
// value somebody widened to make a failure go away.
func TestFormula_ViewBudgetCapsAreEnforced(t *testing.T) {
	schema := formulaFixtureSchema()

	t.Run("16 formulas are accepted and a 17th is refused", func(t *testing.T) {
		sixteen := map[string]string{}
		for i := 0; i < 16; i++ {
			sixteen[fmt.Sprintf("f%02d", i)] = "amount + 1"
		}
		set, errs := ValidateFormulaSet(sixteen, schema)
		if len(errs) != 0 {
			t.Fatalf("FR-146 caps a view at 16 formulas, so exactly 16 must be ACCEPTED. Got: %v", formulaErrorMessages(errs))
		}
		if set.Len() != 16 {
			t.Fatalf("the set holds %d formulas, want 16", set.Len())
		}

		seventeen := map[string]string{}
		for k, v := range sixteen {
			seventeen[k] = v
		}
		seventeen["f16"] = "amount + 1"
		set, errs = ValidateFormulaSet(seventeen, schema)
		if !hasCode(errs, FormulaErrTooLarge) {
			t.Fatalf("FR-146: a 17th formula must be refused; got %v", formulaErrorCodes(errs))
		}
		if set != nil {
			t.Fatal("FR-146: a cap refusal must store nothing")
		}
		msg := formulaMessageWithCode(errs, FormulaErrTooLarge)
		if !strings.Contains(msg, "17") || !strings.Contains(msg, "16") {
			t.Errorf("FR-146: the refusal must name the count AND the cap so the author knows how far over they are; got:\n  %s", msg)
		}
	})

	t.Run("256 total nodes are accepted and 257 are refused", func(t *testing.T) {
		// `amount + 1` is three nodes: the reference, the literal, the operator.
		// Sixteen of them is 48. To reach exactly 256 across 16 formulas each
		// formula needs 16 nodes: a chain of 8 additions over 9 leaves is
		// 9 + 8 = 17, so use 15 leaves in a BALANCED shape instead — depth 8 is
		// the other cap and a left-leaning chain would hit it first.
		at256 := map[string]string{}
		for i := 0; i < 16; i++ {
			at256[fmt.Sprintf("f%02d", i)] = balancedSum(16)
		}
		if got := FormulaNodeCount(mustParse(t, balancedSum(16))); got != 16 {
			t.Fatalf("the fixture is wrong: balancedSum(16) is %d nodes, want 16", got)
		}
		set, errs := ValidateFormulaSet(at256, schema)
		if len(errs) != 0 {
			t.Fatalf("FR-146 caps a view at 256 formula nodes, so exactly 256 must be ACCEPTED. Got: %v", formulaErrorMessages(errs))
		}
		if got := set.TotalNodes(); got != 256 {
			t.Fatalf("the fixture totals %d nodes, want 256", got)
		}

		over := map[string]string{}
		for k, v := range at256 {
			over[k] = v
		}
		over["f15"] = balancedSum(17) // exactly ONE node more
		if got := FormulaNodeCount(mustParse(t, balancedSum(17))); got != 17 {
			t.Fatalf("the fixture is wrong: balancedSum(17) is %d nodes, want 17", got)
		}
		_, errs = ValidateFormulaSet(over, schema)
		if !hasCode(errs, FormulaErrTooLarge) {
			t.Fatalf("FR-146: 257 total nodes must be refused — the cap is 256 and one over is over; got %v", formulaErrorCodes(errs))
		}
		msg := formulaMessageWithCode(errs, FormulaErrTooLarge)
		if !strings.Contains(msg, "256") {
			t.Errorf("FR-146: the refusal must name the 256-node view cap; got:\n  %s", msg)
		}
	})

	t.Run("64 nodes in ONE formula are accepted and 66 are refused", func(t *testing.T) {
		// The per-formula cap. Sixty-four is legal on its own even though a
		// view of sixteen such formulas would blow the total — the two caps are
		// independent and both refusals must exist.
		if _, errs := ValidateFormulaSet(map[string]string{"f": balancedSum(64)}, schema); len(errs) != 0 {
			t.Fatalf("FR-146 caps ONE formula at 64 nodes, so exactly 64 must be accepted. Got: %v", formulaErrorMessages(errs))
		}
		_, errs := ValidateFormulaSet(map[string]string{"f": balancedSum(66)}, schema)
		if !hasCode(errs, FormulaErrTooLarge) {
			t.Fatalf("FR-146: 66 nodes in one formula must be refused; got %v", formulaErrorCodes(errs))
		}
		if msg := formulaMessageWithCode(errs, FormulaErrTooLarge); !strings.Contains(msg, "64") {
			t.Errorf("FR-146: the refusal must name WHICH cap it hit; got:\n  %s", msg)
		}
	})

	t.Run("depth 8 is accepted and depth 9 is refused", func(t *testing.T) {
		// A left-leaning chain of additions: `1 + 1` is depth 2, and each
		// further `+ 1` adds one level. Depth 8 therefore needs 7 additions.
		depth8 := "1" + strings.Repeat(" + 1", 7)
		if got := FormulaDepth(mustParse(t, depth8)); got != 8 {
			t.Fatalf("the fixture is wrong: %q is depth %d, want 8", depth8, got)
		}
		if _, errs := ValidateFormulaSet(map[string]string{"f": depth8}, schema); len(errs) != 0 {
			t.Fatalf("FR-146 caps depth at 8, so exactly 8 must be accepted. Got: %v", formulaErrorMessages(errs))
		}

		depth9 := "1" + strings.Repeat(" + 1", 8)
		_, errs := ValidateFormulaSet(map[string]string{"f": depth9}, schema)
		if !hasCode(errs, FormulaErrTooLarge) {
			t.Fatalf("FR-146: depth 9 must be refused; got %v", formulaErrorCodes(errs))
		}
		if msg := formulaMessageWithCode(errs, FormulaErrTooLarge); !strings.Contains(msg, "nests") {
			t.Errorf("FR-146: the refusal must say it is the DEPTH cap, not one of the other two; got:\n  %s", msg)
		}
	})
}

// balancedSum returns an expression whose tree has exactly n nodes, built
// balanced so the depth cap is not what refuses it.
//
// A sum of k leaves has k-1 operators, so n = 2k-1 and n must be ODD; an even n
// is produced by making one leaf a parenthesised unary negation, which adds
// exactly one node.
func balancedSum(n int) string {
	extra := ""
	if n%2 == 0 {
		n--
		extra = " + -1" // a leaf plus a unary operator: two nodes for one leaf
		n -= 2
	}
	leaves := (n + 1) / 2
	if leaves < 1 {
		leaves = 1
	}
	return balancedSumOf(leaves) + extra
}

func balancedSumOf(leaves int) string {
	if leaves <= 1 {
		return "1"
	}
	half := leaves / 2
	return "(" + balancedSumOf(half) + " + " + balancedSumOf(leaves-half) + ")"
}

func mustParse(t *testing.T, src string) FormulaNode {
	t.Helper()
	n, err := ParseFormula(src)
	if err != nil {
		t.Fatalf("the fixture %q does not parse: %v", src, err)
	}
	return n
}

func hasCode(errs []*FormulaError, code FormulaErrorCode) bool {
	for _, e := range errs {
		if e.Code == code {
			return true
		}
	}
	return false
}

func formulaErrorCodes(errs []*FormulaError) []FormulaErrorCode {
	out := make([]FormulaErrorCode, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Code)
	}
	return out
}

func formulaErrorMessages(errs []*FormulaError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}

func formulaMessageWithCode(errs []*FormulaError, code FormulaErrorCode) string {
	for _, e := range errs {
		if e.Code == code {
			return e.Error()
		}
	}
	return ""
}
