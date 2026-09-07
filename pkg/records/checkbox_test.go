// Omnipus — FR-004c / ADR-068 D24.5: the eighth property type.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// checkboxProp is the fixture declaration. NewProperty rather than a struct
// literal, so the test goes through the same rejections the schema loader
// applies — a `checkbox` that quietly accepted `values` or a `unit` would be a
// defect this fixture would hide.
func checkboxProp(t *testing.T, name string, many bool) *Property {
	t.Helper()
	p, err := NewProperty(Property{Name: name, Type: TypeCheckbox, Many: many, RecordType: "day"})
	if err != nil {
		t.Fatalf("NewProperty(checkbox): %v", err)
	}
	return p
}

// TestCheckbox_IsTheEighthTypeAndComparesByEqualityOnly is FR-004c's whole
// disposition, stated from the requirement rather than read off the code.
//
// FR-004c: "The system MUST support a `checkbox` property type holding YAML
// `true`/`false` (the strings `true`/`false`, folded, parse; anything else is
// non-conforming). Absent is the third state. Defined operators: `=`, `<>`,
// `IN`, `IS NULL`, `IS NOT NULL`; the ordering operators are refused naming the
// remedy."
func TestCheckbox_IsTheEighthTypeAndComparesByEqualityOnly(t *testing.T) {
	t.Run("it is the eighth member of the closed type set", func(t *testing.T) {
		if !isKnownPropertyType(TypeCheckbox) {
			t.Fatal("FR-004c: `checkbox` is not a known property type")
		}
		if got := len(PropertyTypes); got != 8 {
			t.Fatalf("FR-004c amends FR-004's count to eight; got %d (%v)", got, PropertyTypes)
		}
		// It is APPENDED, not inserted: PropertyTypes is the list a rejection
		// reads back to the operator, and the seven that came before it must
		// keep their positions.
		if PropertyTypes[7] != TypeCheckbox {
			t.Fatalf("FR-004c: `checkbox` must be the EIGHTH entry so the prior seven keep their order; got %v", PropertyTypes)
		}
	})

	t.Run("the two values parse, case-insensitively", func(t *testing.T) {
		p := checkboxProp(t, "meditated", false)
		for _, tc := range []struct {
			written string
			want    bool
		}{
			{"true", true}, {"false", false},
			// FR-011a's fold, which is the SAME rule that makes `Won` resolve
			// to a declared `won`. A checkbox has two values however many ways
			// they are spelled.
			{"TRUE", true}, {"True", true}, {"FALSE", false}, {"False", false},
		} {
			v, verr := ParseValue(p, Node{Kind: KindScalar, Text: tc.written})
			if verr != nil {
				t.Fatalf("%q must parse as a checkbox: %v", tc.written, verr)
			}
			if v.Type != TypeCheckbox {
				t.Fatalf("%q parsed as %s, want checkbox", tc.written, v.Type)
			}
			if v.Bool != tc.want {
				t.Errorf("%q parsed to %v, want %v", tc.written, v.Bool, tc.want)
			}
			// FR-011c: the file's own spelling survives for a report.
			if v.Raw != tc.written {
				t.Errorf("%q lost its spelling: Raw = %q", tc.written, v.Raw)
			}
			// The RENDERED form is canonical, so one value does not print
			// three ways in one report.
			if want := map[bool]string{true: "true", false: "false"}[tc.want]; v.String() != want {
				t.Errorf("%q renders as %q, want the canonical %q", tc.written, v.String(), want)
			}
		}
	})

	t.Run("anything else is non-conforming, and the refusal names the two values", func(t *testing.T) {
		p := checkboxProp(t, "meditated", false)
		// `yes`/`no`/`on`/`off` are the interesting refusals: YAML 1.1 read them
		// as booleans and YAML 1.2 does not, so yaml.v3 already hands them to us
		// as plain strings. Accepting them here would make this package disagree
		// with the parser every other reader of the same file uses — invisibly,
		// because the note would validate for us and read as text everywhere else.
		for _, bad := range []string{"yes", "no", "on", "off", "1", "0", "y", "n", "checked", "maybe"} {
			_, verr := ParseValue(p, Node{Kind: KindScalar, Text: bad})
			if verr == nil {
				t.Fatalf("FR-004c: %q must NOT parse as a checkbox", bad)
			}
			if verr.Code != FindingNotABoolean {
				t.Errorf("%q: code = %q, want %q", bad, verr.Code, FindingNotABoolean)
			}
			msg := verr.Error()
			for _, must := range []string{"true", "false"} {
				if !strings.Contains(msg, must) {
					t.Errorf("%q: the refusal must name %q so the fix is one word; got %q", bad, must, msg)
				}
			}
		}
	})

	t.Run("absent is the THIRD state, and IS NULL is how it is asked about", func(t *testing.T) {
		p := checkboxProp(t, "meditated", false)
		// D3.2's marquee example: a day with no `meditated` key is a day that
		// is neither checked nor unchecked.
		rec := ParseRecord("day.md", []byte("---\ntype: day\n---\n"))
		pv := ResolveProperty(rec, p)
		if pv.State != StateAbsent {
			t.Fatalf("a checkbox with no key must be ABSENT (the third state); got %s", pv.State)
		}
		if len(pv.Findings) != 0 {
			t.Fatalf("absence is a legitimate state, not a defect; got findings %v", pv.Findings)
		}
		c := Comparator{}
		if ok, _ := c.Evaluate(OpIsNull, pv, PropertyValue{Property: p, State: StateAbsent}); !ok {
			t.Error("R-3: `IS NULL` must be TRUE for an absent checkbox")
		}
		if ok, _ := c.Evaluate(OpIsNotNull, pv, PropertyValue{Property: p, State: StateAbsent}); ok {
			t.Error("R-3: `IS NOT NULL` must be FALSE for an absent checkbox")
		}
		// R-2: an absent operand is false for `=` AND for `<>`. This is the
		// clause that makes "days I did not meditate" answerable only through
		// IS NULL, which is the whole reason absence is the third state.
		for _, op := range []Operator{OpEqual, OpNotEqual} {
			if ok, _ := c.Evaluate(op, pv, present(p, tvBool(false))); ok {
				t.Errorf("R-2: an absent checkbox must be FALSE for %s", op)
			}
		}
	})

	t.Run("equality is defined and ordering is refused naming the remedy", func(t *testing.T) {
		p := checkboxProp(t, "meditated", false)
		c := Comparator{}
		yes, no := present(p, tvBool(true)), present(p, tvBool(false))

		for _, tc := range []struct {
			op         Operator
			left, want bool
		}{
			{OpEqual, true, true}, {OpEqual, false, false},
			{OpNotEqual, true, false}, {OpNotEqual, false, true},
			{OpIn, true, true}, {OpIn, false, false},
		} {
			left := no
			if tc.left {
				left = yes
			}
			got, problems := c.Evaluate(tc.op, left, yes)
			if len(problems) != 0 {
				t.Fatalf("%v %s true: unexpected problems %v", tc.left, tc.op, problems)
			}
			if got != tc.want {
				t.Errorf("%v %s true = %v, want %v", tc.left, tc.op, got, tc.want)
			}
		}

		// R-17/FR-216 — the four ordering operators are REFUSED, and the
		// refusal lists what would have worked (FR-022c). A silent false here
		// would be the defect: the caller would read "no records matched".
		for _, op := range []Operator{OpLess, OpLessOrEqual, OpGreater, OpGreaterOrEqual} {
			got, problems := c.Evaluate(op, yes, no)
			if got {
				t.Errorf("%s over a checkbox must be FALSE", op)
			}
			if len(problems) == 0 {
				t.Fatalf("%s over a checkbox must be REPORTED, not silently false", op)
			}
			if problems[0].Code != CompareOperatorNotDefined {
				t.Errorf("%s: code = %q, want %q", op, problems[0].Code, CompareOperatorNotDefined)
			}
			if len(problems[0].Supported) == 0 {
				t.Fatalf("%s: the refusal must list the supported operators (FR-022c)", op)
			}
			if !strings.Contains(strings.Join(problems[0].Supported, ","), string(OpEqual)) {
				t.Errorf("%s: the remedy list must include `=`; got %v", op, problems[0].Supported)
			}
		}

		// `LIKE` is undefined for the same reason it is on `date`: reaching it
		// would mean coercing the value to text, and this design coerces nothing.
		if _, problems := c.Evaluate(OpLike, yes, no); len(problems) == 0 {
			t.Error("`LIKE` over a checkbox must be refused, not answered")
		}
	})

	t.Run("R-1: a checkbox is its OWN comparison domain", func(t *testing.T) {
		// Not unified with text (`true` the boolean and "true" the string are
		// different declarations), and not with a number (SQLite's 0/1 boolean
		// is a storage convention; adopting it would make `done > 0` a
		// meaningful question with a meaningless answer).
		for _, other := range []PropertyType{TypeText, TypeEnum, TypeInteger, TypeDecimal, TypeDate, TypeRelation, TypePerson} {
			if comparisonDomain(TypeCheckbox) == comparisonDomain(other) {
				t.Errorf("R-1: `checkbox` must not share a comparison domain with %s", other)
			}
		}
	})
}

// TestCheckbox_IsAPropertyTypeNotAMarkdownTask holds the naming rule FR-004c
// collides with, as BEHAVIOUR rather than as a comment.
//
// The product has two things called a checkbox and they live at different
// layers: this one is a PROPERTY in the YAML frontmatter, one value per note;
// FR-076a's is a MARKDOWN TASK — `- [ ] call Ada` — in the note's BODY, many
// per note, each with a line number, indexed into `note_tasks` as a
// `propindex.TaskHit`.
//
// The rule is that `checkbox` names the property type and `task` names the body
// construct. What makes it enforceable is that nothing in this package reads a
// note's body at all, so a task line can never become a value of this type.
func TestCheckbox_IsAPropertyTypeNotAMarkdownTask(t *testing.T) {
	src := []byte(`---
type: day
meditated: false
---

# Monday

- [x] call Ada
- [ ] write the report
- [X] pay the invoice
`)
	rec := ParseRecord("day.md", src)
	p := checkboxProp(t, "meditated", false)
	pv := ResolveProperty(rec, p)

	if pv.State != StatePresent || len(pv.Values) != 1 {
		t.Fatalf("the FRONTMATTER checkbox must resolve; got state %s with %d values", pv.State, len(pv.Values))
	}
	// The frontmatter says false. The body holds two CHECKED task boxes. If the
	// two vocabularies were ever conflated, the body's `[x]` would be the
	// tempting thing to read here.
	if pv.Values[0].Bool {
		t.Error("the checkbox PROPERTY is `false`; the body's `- [x]` lines are markdown TASKS and must not influence it")
	}

	// The body is not parsed at all: the note's frontmatter carries exactly the
	// two keys it declares, and no task ever becomes a property.
	if got := len(rec.Frontmatter.Keys); got != 2 {
		t.Fatalf("frontmatter keys = %v; a body task must never become a property", rec.Frontmatter.Keys)
	}
	for _, k := range rec.Frontmatter.Keys {
		if k != RecordTypeKey && k != "meditated" {
			t.Errorf("unexpected frontmatter key %q — a markdown task leaked into the property space", k)
		}
	}
	// And the refusal vocabulary keeps them apart: a bad checkbox value talks
	// about a checkbox, never about a task.
	_, verr := ParseValue(p, Node{Kind: KindScalar, Text: "[x]"})
	if verr == nil {
		t.Fatal("`[x]` is markdown task syntax and must NOT be a checkbox property value")
	}
	if strings.Contains(strings.ToLower(verr.Error()), "task") {
		t.Errorf("a checkbox refusal must not mention tasks — the two constructs stay named apart; got %q", verr.Error())
	}
}

// TestCheckbox_FoldsWithFullUnicodeNotToLower is the folding receipt this change
// owes, because checkboxSpellings resolves through FoldKey.
//
// The Turkish pair is the one that separates the two candidate implementations
// in the direction nobody notices. `strings.ToLower` maps dotted `İ` onto `i`,
// so it answers TRUE for istanbul/İSTANBUL — a WRONG MATCH, which looks like a
// feature. FoldKey answers FALSE, which is CORRECT: dotted İ and plain i are
// different letters in Turkish.
//
// It is asserted here, beside the parser that depends on it, so that a future
// "simplification" of checkboxSpellings to strings.ToLower has a named test to
// walk past rather than a comment.
func TestCheckbox_FoldsWithFullUnicodeNotToLower(t *testing.T) {
	if FoldKey("istanbul") == FoldKey("İSTANBUL") {
		t.Error("FoldKey must NOT fold Turkish dotted İ onto i — that is the classic Turkish-I wrong match, and strings.ToLower is how it gets in")
	}
	if strings.ToLower("İSTANBUL") == strings.ToLower("istanbul") {
		// Documents WHY the assertion above is not vacuous: the rejected
		// implementation really does answer differently here.
		t.Log("confirmed: strings.ToLower collapses the Turkish pair, which is exactly why it is not used")
	}
	// The fold that IS wanted still works for the vocabulary this type accepts.
	if FoldKey("TRUE") != FoldKey("true") {
		t.Error("`TRUE` and `true` must fold to one key — a checkbox has two values however they are spelled")
	}
}
