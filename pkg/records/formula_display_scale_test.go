// Omnipus — UAT 2026-09-13 D-61: a formula number is shown at the precision
// its author DECLARED, and a negative date difference says what it is.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// TestFormula_UndeclaredScaleTrimsTrailingZeros is D-61's first half. FR-144
// says a number crosses the boundary at a declared scale, defaulting to 10.
// The default is a ROUNDING bound, not a padding instruction: a `.days` that
// is exactly 223 was rendered `223.0000000000` in both doors (knowledge_find
// and the base preview), which reads as false precision. With no scale
// declared, an exact value is shown in its shortest exact form; a value that
// genuinely needed rounding keeps its ten places and is still labelled.
func TestFormula_UndeclaredScaleTrimsTrailingZeros(t *testing.T) {
	c := fixtureCandidate{}
	for _, tc := range []struct{ src, want string }{
		{`(date("2019-03-09") - date("2019-03-07")).days`, "2"},
		{`(date("2019-09-30") - date("2019-02-19")).days`, "223"},
		{`(date("2019-03-07T12:00:00Z") - date("2019-03-07")).days`, "0.5"},
		{`10 / 4`, "2.5"},
		{`1000 * 1`, "1000"},
		{`0 - 0`, "0"},
	} {
		res := evalOne(t, tc.src, c)
		vals := res.Values()
		if len(vals) != 1 {
			t.Fatalf("%s: want one value, got %d", tc.src, len(vals))
		}
		if vals[0].Raw != tc.want {
			t.Errorf("%s: Raw = %q, want %q (no scale declared → shortest exact form)", tc.src, vals[0].Raw, tc.want)
		}
		if got := vals[0].Number.String(); got != tc.want {
			t.Errorf("%s: Number.String() = %q, want %q — knowledgefind renders the Decimal itself, so the Decimal's own scale must be trimmed too", tc.src, got, tc.want)
		}
		if res.Rounded {
			t.Errorf("%s: an exact value must not be labelled rounded", tc.src)
		}
		if res.ScaleDeclared {
			t.Errorf("%s: no scale was declared", tc.src)
		}
	}
}

// TestFormula_DeclaredScaleKeepsItsZeros — the other side of the same rule:
// toFixed(x, 2) is a DECLARATION, and `223.00` is what the author asked for.
func TestFormula_DeclaredScaleKeepsItsZeros(t *testing.T) {
	c := fixtureCandidate{}
	for _, tc := range []struct{ src, want string }{
		{`toFixed((date("2019-09-30") - date("2019-02-19")).days, 2)`, "223.00"},
		{`toFixed(10 / 4, 3)`, "2.500"},
		{`round(10 / 4, 1)`, "2.5"},
		{`round(10 / 4)`, "2"},
		{`toFixed(1, 10)`, "1.0000000000"},
	} {
		res := evalOne(t, tc.src, c)
		vals := res.Values()
		if len(vals) != 1 {
			t.Fatalf("%s: want one value, got %d", tc.src, len(vals))
		}
		if vals[0].Raw != tc.want {
			t.Errorf("%s: Raw = %q, want %q (a declared scale is kept exactly)", tc.src, vals[0].Raw, tc.want)
		}
		if !res.ScaleDeclared {
			t.Errorf("%s: the scale was declared and the result must say so", tc.src)
		}
	}
}

// TestFormula_RoundedValueKeepsTenPlacesAndTheLabel — trimming removes only
// zeros that carry no information; a value that did not fit at scale 10 is
// still shown at scale 10 and still labelled rounded (FR-144).
func TestFormula_RoundedValueKeepsTenPlacesAndTheLabel(t *testing.T) {
	res := evalOne(t, `1 / 3`, fixtureCandidate{})
	vals := res.Values()
	if len(vals) != 1 {
		t.Fatalf("want one value, got %d", len(vals))
	}
	if vals[0].Raw != "0.3333333333" {
		t.Errorf("1/3 Raw = %q, want %q", vals[0].Raw, "0.3333333333")
	}
	if !res.Rounded {
		t.Error("1/3 at scale 10 is a rounding and must be labelled as one")
	}
	// A rounded value whose last digits happen to be zero is still not
	// trimmed below what the rounding produced: 1/8 = 0.125 exactly, so it is
	// exact, not rounded, and trims; 2/3 is rounded and keeps its ten places.
	res = evalOne(t, `2 / 3`, fixtureCandidate{})
	if got := res.Values()[0].Raw; got != "0.6666666667" {
		t.Errorf("2/3 Raw = %q, want %q", got, "0.6666666667")
	}
}

// TestFormula_NegativeDateDifferenceIsExplained is D-61's second half. A
// project whose start date is in the future shows `days_open = -79`; the
// number is right, but a reader cannot tell a countdown from an elapsed
// count. The evaluator knows the value came from subtracting dates, so it
// says so — on the result, where every consumer can show it.
func TestFormula_NegativeDateDifferenceIsExplained(t *testing.T) {
	c := fixtureCandidate{}

	res := evalOne(t, `(date("2019-03-05") - date("2019-03-07")).days`, c)
	if got := res.Values()[0].Raw; got != "-2" {
		t.Fatalf("Raw = %q, want %q", got, "-2")
	}
	if res.Note == "" {
		t.Fatal("a negative date difference must carry a Note explaining the sign")
	}
	for _, want := range []string{"negative", "later"} {
		if !strings.Contains(res.Note, want) {
			t.Errorf("Note %q should mention %q", res.Note, want)
		}
	}

	// The same subtraction the other way round is an ordinary elapsed count
	// and carries no note — the note is about the sign, not the operation.
	res = evalOne(t, `(date("2019-03-07") - date("2019-03-05")).days`, c)
	if res.Note != "" {
		t.Errorf("a positive date difference needs no explanation; got Note %q", res.Note)
	}

	// A negative that did NOT come from dates is just a number.
	res = evalOne(t, `2 - 5`, c)
	if res.Note != "" {
		t.Errorf("an ordinary negative number needs no explanation; got Note %q", res.Note)
	}

	// Arithmetic downstream of the date difference keeps the provenance: the
	// sign is judged at the boundary, so `-(a - b).days` that ends positive
	// carries no note, and `(a - b).days * 2` that ends negative does.
	res = evalOne(t, `((date("2019-03-05") - date("2019-03-07")).days) * 2`, c)
	if res.Note == "" {
		t.Error("a negative derived from a date difference must still be explained")
	}
	res = evalOne(t, `0 - (date("2019-03-05") - date("2019-03-07")).days`, c)
	if res.Note != "" {
		t.Errorf("a positive value needs no explanation; got Note %q", res.Note)
	}
}

// TestFormula_PresentationTextTrimsUndeclaredZeros — format()/link() text
// built from a number showed the same padded rendering; it follows the same
// rule.
func TestFormula_PresentationTextTrimsUndeclaredZeros(t *testing.T) {
	res := evalOne(t, `format((date("2019-03-09") - date("2019-03-07")).days, "{} days")`, fixtureCandidate{})
	texts := res.Display()
	if len(texts) != 1 {
		t.Fatalf("want one display text, got %d", len(texts))
	}
	if texts[0] != "2 days" {
		t.Errorf("format() rendered %q, want %q", texts[0], "2 days")
	}
}
