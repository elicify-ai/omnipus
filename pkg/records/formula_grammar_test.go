// Omnipus — FR-143: the grammar is CLOSED and PINNED to the Bases syntax
// reference as fetched 2026-08-30.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// TestFormula_GrammarIsTheSnapshot pins FR-143's closed surface.
//
// "Closed" is enforceable only if the set is written down somewhere a diff can
// see. The lists below ARE that record: they are transcribed from FR-143's
// sentence, and a change to them is a visible change to a test, which is what
// FR-143 means by "adopting a newer snapshot is a SPEC REVISION with its own
// diff, never a silent code change".
func TestFormula_GrammarIsTheSnapshot(t *testing.T) {
	schema := formulaFixtureSchema()

	t.Run("every construct FR-143 lists parses", func(t *testing.T) {
		// One entry per construct in FR-143's sentence: "arithmetic + - * / % (
		// ), boolean ! && ||, the comparison operators, the function set if,
		// toFixed, mean, round, date, today, now, format, list, link, icon,
		// contains, the .time()/.date()/.hour accessors, and the four file
		// methods."
		for _, src := range []string{
			// arithmetic and parentheses
			"amount + 1", "amount - 1", "amount * 2", "amount / 2",
			"quantity % 2", "(amount + 1) * 2",
			// boolean
			"!(amount > 1)", "amount > 1 && quantity < 5", "amount > 1 || quantity < 5",
			// the comparison operators
			"amount == 1", "amount != 1", "amount < 1", "amount <= 1",
			"amount > 1", "amount >= 1",
			// the function set
			`if(amount > 1, 1, 2)`,
			"toFixed(amount, 2)",
			"mean(sizes)",
			"round(amount)", "round(amount, 2)",
			`date("2026-08-31")`,
			"today()", "now()",
			`format(amount, "{}")`,
			"list(1, 2, 3)",
			"link(name)", `link(name, "Acme")`,
			"icon(name)",
			`contains(name, "cme")`,
			// the accessors, in both the method and function spellings
			"due.time()", "due.date()", "due.hour",
			"toFixed(amount, 2)", "amount.toFixed(2)",
			// the four file methods
			`file.hasTag("project")`,
			`file.inFolder("10-Projects")`,
			`file.hasLink("Acme")`,
			"file.asLink()",
			// the file properties FR-130 declares
			"file.size + 1", "file.mtime > file.ctime", `file.name == "x"`,
		} {
			if _, errs := ValidateFormulaSet(map[string]string{"f": src}, schema); len(errs) != 0 {
				t.Errorf("FR-143 lists this construct; it must parse and type: %q\n  %v", src, formulaErrorMessages(errs))
			}
		}
	})

	t.Run("a construct OUTSIDE the snapshot is refused", func(t *testing.T) {
		// Each of these is documented in some expression language somewhere,
		// which is exactly why a closed grammar has to say no to them by name
		// rather than by accident.
		for _, tc := range []struct{ src, why string }{
			{"sqrt(amount)", "a function outside the set"},
			{"amount ** 2", "an operator no snapshot lists"},
			{"amount & 1", "bitwise operators are not in the set"},
			{"amount ? 1 : 2", "a ternary is not in the set — if() is"},
			{"amount++", "no increment operator"},
			{`amount |> round`, "no pipeline operator"},
			{"amount.length", "no `.length` accessor"},
			{"file.author", "not one of FR-130's thirteen"},
			{"file.tags()", "`file.tags` is a property, not a method"},
			{"note.amount", "no dotted property paths"},
		} {
			if _, errs := ValidateFormulaSet(map[string]string{"f": tc.src}, schema); len(errs) == 0 {
				t.Errorf("FR-143: %q must be refused (%s) — a grammar that quietly grows is a moving target nobody notices moving", tc.src, tc.why)
			}
		}
	})

	t.Run("the precedence ladder is pinned", func(t *testing.T) {
		// Precedence decides what a STORED formula MEANS. A change to the
		// ladder silently re-interprets every formula already on disk, so it is
		// asserted by evaluated ANSWER rather than left to a reader's memory of
		// the parser.
		//
		// Every expected value is computed by hand from FR-143's ladder:
		//   || < && < ==,!= < <,<=,>,>= < +,- < *,/,% < unary < postfix
		c := fixtureCandidate{}
		for _, tc := range []struct {
			src  string
			want string
			why  string
		}{
			{"1 + 2 * 3", "7", "* binds tighter than + — 1+(2*3), not (1+2)*3"},
			{"(1 + 2) * 3", "9", "parentheses override it"},
			{"10 - 2 - 3", "5", "- is LEFT associative — (10-2)-3, not 10-(2-3)"},
			{"12 / 2 / 3", "2", "/ is LEFT associative — (12/2)/3, not 12/(2/3)"},
			{"-2 + 5", "3", "unary - binds to its operand, not to the sum"},
			{"7 % 4 + 1", "4", "%% binds tighter than + — (7%%4)+1"},
			{"2 * 3 % 4", "2", "* and %% are the same level, LEFT associative — (2*3)%%4"},
		} {
			res := evalOne(t, tc.src, c)
			if got := renderNumber(t, res); got != tc.want {
				t.Errorf("FR-143 precedence: %s = %s, want %s (%s)", tc.src, got, tc.want, tc.why)
			}
		}

		// The boolean half, asserted the same way.
		for _, tc := range []struct {
			src  string
			want bool
			why  string
		}{
			{"1 < 2 && 3 < 4", true, "comparison binds tighter than &&"},
			{"1 > 2 || 3 < 4", true, "comparison binds tighter than ||"},
			{"1 > 2 && 1 > 2 || 3 < 4", true, "&& binds tighter than || — (F&&F)||T"},
			{"!(1 > 2)", true, "! applies to the parenthesised comparison"},
			{"1 + 1 == 2", true, "arithmetic binds tighter than =="},
		} {
			res := evalOne(t, tc.src, c)
			vals := res.Values()
			if len(vals) != 1 {
				t.Fatalf("%s produced %d values", tc.src, len(vals))
			}
			got := vals[0].Raw == "true"
			if got != tc.want {
				t.Errorf("FR-143 precedence: %s = %v, want %v (%s)", tc.src, got, tc.want, tc.why)
			}
		}
	})

	t.Run("a bare = names == rather than shrugging", func(t *testing.T) {
		// Ruling R-B spells the FILTER vocabulary's equality `=`; FR-143's
		// expression grammar spells it `==`. Somebody will write `=` in a
		// formula immediately after writing one in a filter, and a generic
		// "unknown operator" does not tell them which character to change.
		_, errs := ValidateFormulaSet(map[string]string{"f": "amount = 1"}, schema)
		if len(errs) == 0 {
			t.Fatal("`=` is not an expression operator and must be refused")
		}
		msg := errs[0].Error()
		if !strings.Contains(msg, "==") {
			t.Errorf("the refusal must name `==`; got:\n  %s", msg)
		}
	})

	t.Run("a number literal never becomes a binary float", func(t *testing.T) {
		// 349.98 is the canonical case: it has no exact binary form, so a
		// float64 round-trip renders 349.97999999999996. The literal must
		// survive as written.
		res := evalOne(t, "349.98", fixtureCandidate{})
		if got := renderNumber(t, res); got != "349.98" {
			t.Errorf("FR-013: the literal 349.98 rendered %s — a binary float is somewhere on the path", got)
		}
		// And a value beyond float64's integer precision survives exactly.
		res = evalOne(t, "9007199254740993 + 0", fixtureCandidate{})
		if got := renderNumber(t, res); got != "9007199254740993" {
			t.Errorf("DS-1: 2^53+1 rendered %s — it must survive exactly", got)
		}
	})

	t.Run("the source bound refuses before the node cap can be reached", func(t *testing.T) {
		// FR-146's caps are stated over the parsed TREE, and a tree cannot be
		// counted before it is built. Without a source bound, a megabyte of
		// `(((((…` would be tokenized and half-parsed before any node cap could
		// fire — the cap would be enforcing a bound on work already done.
		huge := strings.Repeat("(", 5000) + "1" + strings.Repeat(")", 5000)
		_, errs := ValidateFormulaSet(map[string]string{"f": huge}, schema)
		if !hasCode(errs, FormulaErrTooLarge) {
			t.Fatalf("a 10,001-byte expression must be refused before it is parsed; got %v", formulaErrorCodes(errs))
		}
	})

	t.Run("deep nesting inside the source bound is refused, not stack-overflowed", func(t *testing.T) {
		// Under the 4 KiB source bound, 1,000 nested parentheses still recurse
		// once per byte. The parser's own depth guard is what makes this a
		// refusal rather than a crash — FR-146's depth cap is checked over the
		// finished tree, which is too late if building it blew the stack.
		deep := strings.Repeat("(", 1000) + "1" + strings.Repeat(")", 1000)
		if len(deep) > maxFormulaSourceBytes {
			t.Fatalf("the fixture is %d bytes, which the source bound would refuse first", len(deep))
		}
		_, errs := ValidateFormulaSet(map[string]string{"f": deep}, schema)
		if !hasCode(errs, FormulaErrTooLarge) {
			t.Fatalf("1,000 levels of nesting must be refused; got %v", formulaErrorCodes(errs))
		}
	})

	t.Run("a formula name a query could not address is refused", func(t *testing.T) {
		// A query reaches a formula only as `formula.<name>` (FR-140). A name
		// with a dot or a space is a formula nothing can ever refer to — better
		// refused at write time than stored and unreachable.
		for _, name := range []string{"", "a.b", "a b", "1st", "formula.x"} {
			if _, errs := ValidateFormulaSet(map[string]string{name: "1"}, schema); !hasCode(errs, FormulaErrName) {
				t.Errorf("FR-140: %q is not addressable as `formula.<name>` and must be refused; got %v", name, formulaErrorCodes(errs))
			}
		}
		if _, errs := ValidateFormulaSet(map[string]string{"net_value": "1"}, schema); len(errs) != 0 {
			t.Errorf("the control: `net_value` is a perfectly good name and must be accepted; got %v", formulaErrorMessages(errs))
		}
	})
}

// TestFormula_FileMethodsAgreeWithTheirFilterTranslation is FR-134.
//
// The file methods exist in TWO places: as translations to ordinary filter
// leaves (FR-134, where the query path parses nothing) and as formula grammar
// (FR-143). If the two disagree, one base gives two answers to one question
// depending on whether its clause landed in a filter or a formula — the exact
// class of divergence §8's single-implementation rule exists to prevent.
//
// FR-134 fixes hasTag as HIERARCHY-AWARE: `{any: [{tags,=,x},{tags,LIKE,x/%}]}`
// — the tag itself, or any descendant of it. inFolder is the same shape over
// the folder. This asserts the formula form answers identically.
func TestFormula_FileMethodsAgreeWithTheirFilterTranslation(t *testing.T) {
	schema := formulaFixtureSchema()

	tagsProp := &Property{Name: "file.tags", Type: TypeText, Many: true}
	folderProp := &Property{Name: "file.folder", Type: TypeText}

	candidate := fixtureCandidate{files: map[string]PropertyValue{
		"file.tags": {
			Property: tagsProp, State: StatePresent,
			Values: []TypedValue{
				{Type: TypeText, Text: "project/active", Raw: "project/active"},
				{Type: TypeText, Text: "Client", Raw: "Client"},
			},
		},
		"file.folder": {
			Property: folderProp, State: StatePresent,
			Values: []TypedValue{{Type: TypeText, Text: "10-Projects/Acme", Raw: "10-Projects/Acme"}},
		},
	}}

	for _, tc := range []struct {
		src  string
		want bool
		why  string
	}{
		{`file.hasTag("project")`, true, "FR-134: hierarchy-aware — `project` matches `project/active`"},
		{`file.hasTag("project/active")`, true, "the tag itself"},
		{`file.hasTag("proj")`, false, "a PREFIX is not a tag — only a whole segment matches"},
		{`file.hasTag("client")`, true, "FR-011a: tags fold like every other text comparison"},
		{`file.hasTag("archive")`, false, "the control"},
		{`file.inFolder("10-Projects")`, true, "FR-134: the folder AND its descendants"},
		{`file.inFolder("10-Projects/Acme")`, true, "the folder itself"},
		{`file.inFolder("10-Proj")`, false, "a prefix of a folder NAME is not a folder"},
		{`file.inFolder("99-Temp")`, false, "the standing example from FR-105 — this clause must not quietly pass"},
	} {
		set, errs := ValidateFormulaSet(map[string]string{"f": tc.src}, schema)
		if len(errs) != 0 {
			t.Fatalf("%q must validate: %v", tc.src, formulaErrorMessages(errs))
		}
		e := NewFormulaEvaluator(set, testComparator(), formulaTestNow())
		e.Begin(candidate)
		res, _ := e.Evaluate("f")
		vals := res.Values()
		if len(vals) != 1 {
			t.Fatalf("%q produced %d values", tc.src, len(vals))
		}
		if got := vals[0].Raw == "true"; got != tc.want {
			t.Errorf("FR-134: %s = %v, want %v (%s)", tc.src, got, tc.want, tc.why)
		}
	}
}
