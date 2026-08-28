// Omnipus — spec FR-022a..e, FR-024, FR-065, AC-F1: a type mismatch is NEVER a
// silent empty result.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package vaultfind

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestRefusal_UnknownPropertyNamesTheValidOnes is FR-024.
//
// The expected values come from the SPEC, not from the implementation: the
// refusal must name the property that was wrong AND list what would have been
// accepted, because a typo and a genuinely empty result look identical to the
// caller and the typo is far more common.
func TestRefusal_UnknownPropertyNamesTheValidOnes(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	resp := mustRefuse(t, f.deps(), req(withType("plant"), withFilter(leaf("conditon", "=", "growing"))))

	p := resp.Problems[0]
	if p.Code != generated.UnknownProperty {
		t.Errorf("code = %s, want unknown_property", p.Code)
	}
	if !strings.Contains(p.Reason, "conditon") {
		t.Errorf("the refusal does not name the property that was wrong: %q", p.Reason)
	}
	// Every declared property must be offered. Listing SOME of them would send a
	// caller to a second guess.
	for _, want := range []string{"species", "condition", "planted", "height_cm", "cuttings", "bed", "keeper", "labels"} {
		if !strings.Contains(p.Reason, want) {
			t.Errorf("the refusal does not offer the declared property %q: %q", want, p.Reason)
		}
	}
	if p.Permitted == nil || len(*p.Permitted) != 8 {
		t.Errorf("permitted list = %v, want all eight declared properties", p.Permitted)
	}
}

// TestRefusal_UnsupportedSQLNamesTheTenOperators is FR-022c.
//
// A model fluent in SQL reaches for JOIN, BETWEEN, COALESCE and a subquery. Each
// must be refused BY NAME with the supported set — never parsed, never silently
// dropped, and never answered with an empty result, which would be a plausible
// answer to a different question.
func TestRefusal_UnsupportedSQLNamesTheTenOperators(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	for _, construct := range []string{"JOIN", "BETWEEN", "COALESCE", "EXISTS", "REGEXP", "!="} {
		t.Run(construct, func(t *testing.T) {
			resp := mustRefuse(t, f.deps(),
				req(withType("plant"), withFilter(leaf("condition", construct, "growing"))))

			p := resp.Problems[0]
			if p.Code != generated.UnsupportedOperator {
				t.Errorf("code = %s, want unsupported_operator", p.Code)
			}
			if !strings.Contains(p.Reason, construct) {
				t.Errorf("the refusal does not name %q: %q", construct, p.Reason)
			}
			// The ten, by name. The list is the whole value of the refusal: it
			// is what lets the model correct itself without another round trip.
			for _, op := range records.OperatorNames() {
				if !strings.Contains(p.Reason, op) {
					t.Errorf("the refusal for %q does not list the supported operator %q: %q",
						construct, op, p.Reason)
				}
			}
		})
	}
}

// TestRefusal_JOINNamesTheParameterThatDoesTheJob is FR-022c's second clause:
// the refusal names the ARGUMENT that does the job, not merely the operators
// that do not.
func TestRefusal_JOINNamesTheParameterThatDoesTheJob(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	resp := mustRefuse(t, f.deps(), req(withType("plant"), withFilter(leaf("bed", "JOIN", "x"))))
	got := resp.Problems[0].Reason + " " + deref(resp.Problems[0].Fix)
	if !strings.Contains(got, "join") {
		t.Errorf("a refused JOIN does not point at the `join` parameter: %q", got)
	}
}

// TestRefusal_EmptyLikePatternAndEmptyInList are FR-022a and FR-022d.
//
// Both are refusals whose absence would produce a WRONG ANSWER rather than an
// error: `LIKE '%'` matches everything and would return a whole-table result
// presented as a filtered one, and an empty `IN` matches nothing and would
// return zero rows for a query the caller believes selects something.
func TestRefusal_EmptyLikePatternAndEmptyInList(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	t.Run("empty LIKE pattern", func(t *testing.T) {
		for _, pattern := range []string{"", "%"} {
			resp := mustRefuse(t, f.deps(),
				req(withType("plant"), withFilter(leaf("species", "LIKE", pattern))))
			p := resp.Problems[0]
			if p.Code != generated.EmptyLikePattern {
				t.Errorf("pattern %q: code = %s, want empty_like_pattern", pattern, p.Code)
			}
			if !strings.Contains(p.Reason+deref(p.Fix), "IS NOT NULL") {
				t.Errorf("pattern %q: the refusal does not name IS NOT NULL as what was meant: %q",
					pattern, p.Reason)
			}
		}
	})

	t.Run("empty IN list", func(t *testing.T) {
		property, op := "condition", generated.VaultFilterNodeOp("IN")
		empty := []string{}
		node := generated.VaultFilterNode{Property: &property, Op: &op, Values: &empty}
		resp := mustRefuse(t, f.deps(), req(withType("plant"), withFilter(node)))
		if resp.Problems[0].Code != generated.EmptyInList {
			t.Errorf("code = %s, want empty_in_list; reason=%q",
				resp.Problems[0].Code, resp.Problems[0].Reason)
		}
	})
}

// TestRefusal_LiteralInTheWrongType is FR-022e.
func TestRefusal_LiteralInTheWrongType(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	resp := mustRefuse(t, f.deps(),
		req(withType("plant"), withFilter(leaf("cuttings", "=", "three"))))
	p := resp.Problems[0]
	if !strings.Contains(p.Reason, "three") {
		t.Errorf("the refusal does not quote the offending literal: %q", p.Reason)
	}
	if p.Fix == nil || *p.Fix == "" {
		t.Errorf("a literal-type refusal names no remedy")
	}
}

// TestRefusal_UnknownEnumValueListsThePermittedSet is FR-011 plus FR-024.
func TestRefusal_UnknownEnumValueListsThePermittedSet(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	resp := mustRefuse(t, f.deps(),
		req(withType("plant"), withFilter(leaf("condition", "=", "flourishing"))))
	p := resp.Problems[0]
	for _, want := range []string{"seedling", "growing", "dormant"} {
		if !strings.Contains(p.Reason, want) {
			t.Errorf("the refusal does not list the permitted value %q: %q", want, p.Reason)
		}
	}
}

// TestRefusal_OrderingOnAManyProperty is section 8 R-13.
func TestRefusal_OrderingOnAManyProperty(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	resp := mustRefuse(t, f.deps(), req(withType("plant"), withFilter(leaf("labels", ">", "indoor"))))
	p := resp.Problems[0]
	if !strings.Contains(p.Reason, "labels") {
		t.Errorf("the refusal does not name the property: %q", p.Reason)
	}
	// It must name the operators that ARE defined element-wise, or the caller
	// learns only that they were wrong.
	joined := p.Reason + " " + deref(p.Fix)
	if !strings.Contains(joined, "=") || !strings.Contains(joined, "IN") || !strings.Contains(joined, "LIKE") {
		t.Errorf("the refusal does not offer =, IN and LIKE as the defined alternatives: %q", joined)
	}
}

// TestRefusal_ThirdHop is FR-065.
//
// The bound is enforced HERE and not only by the wire schema, because a schema
// violation surfaces as "your body was invalid" — which tells the caller nothing
// about the limit or the remedy.
func TestRefusal_ThirdHop(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	near, hops := "garden/plant-0001.md", 3
	resp := mustRefuse(t, f.deps(), generated.VaultFindRequest{Near: &near, Hops: &hops})

	p := resp.Problems[0]
	if p.Code != generated.HopLimitExceeded {
		t.Errorf("code = %s, want hop_limit_exceeded", p.Code)
	}
	if !strings.Contains(p.Reason, "3") || !strings.Contains(p.Reason, "2") {
		t.Errorf("the refusal states neither the requested hops nor the limit: %q", p.Reason)
	}
	if !strings.Contains(deref(p.Fix), "second vault_find") {
		t.Errorf("the refusal does not name the remedy (a second search): %q", deref(p.Fix))
	}
}

// TestRefusal_UnknownParameterNamesTheAcceptedOnes is FR-022c's parameter half.
//
// encoding/json silently drops a field the target struct does not declare, so
// without the explicit check a `where:` argument would be ACCEPTED and ignored
// and the caller would receive an answer to a question they did not ask.
func TestRefusal_UnknownParameterNamesTheAcceptedOnes(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	for _, tc := range []struct{ arg, wantRemedy string }{
		{`{"type":"plant","where":"condition = 'growing'"}`, "structured filter"},
		{`{"type":"plant","having":"count > 2"}`, "group_by"},
		{`{"type":"plant","order_by":"height_cm"}`, "sort"},
		{`{"type":"plant","offset":20}`, "cursor"},
	} {
		out, err := Call(context.Background(), f.deps(), []byte(tc.arg))
		if err == nil {
			t.Fatalf("%s: accepted an undeclared argument instead of refusing it", tc.arg)
		}
		if !strings.Contains(out, tc.wantRemedy) {
			t.Errorf("%s: the refusal does not name the argument that does the job (%q):\n%s",
				tc.arg, tc.wantRemedy, out)
		}
		for _, name := range AcceptedParameters {
			if !strings.Contains(out, name) {
				t.Errorf("%s: the refusal does not list the accepted argument %q", tc.arg, name)
			}
		}
	}
}

// TestRefusal_UnknownRecordType is FR-024 at the type level, including the case
// an empty vault must not be confused with.
func TestRefusal_UnknownRecordType(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	resp := mustRefuse(t, f.deps(), req(withType("shrub")))
	p := resp.Problems[0]
	if !strings.Contains(p.Reason, "shrub") || !strings.Contains(p.Reason, "plant") {
		t.Errorf("the refusal must name both the type asked for and the types declared: %q", p.Reason)
	}

	// An empty vault must READ DIFFERENTLY from a mistyped name. "declared: "
	// with nothing after it would make the two identical.
	empty := Deps{Schemas: records.NewSchemaSet(), Store: f.store}
	resp2 := mustRefuse(t, empty, req(withType("plant")))
	if !strings.Contains(resp2.Problems[0].Reason, "no record types at all") {
		t.Errorf("an empty vault refuses with the same wording as a typo: %q", resp2.Problems[0].Reason)
	}
}

// TestRefusal_EveryRefusalNamesItsRemedy is AC-F1, over every refusal this
// package can produce in one place.
func TestRefusal_EveryRefusalNamesItsRemedy(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	cases := map[string]generated.VaultFindRequest{
		"unknown property":  req(withType("plant"), withFilter(leaf("nope", "=", "x"))),
		"unknown type":      req(withType("shrub")),
		"unsupported op":    req(withType("plant"), withFilter(leaf("condition", "BETWEEN", "a"))),
		"empty like":        req(withType("plant"), withFilter(leaf("species", "LIKE", "%"))),
		"ordering on many":  req(withType("plant"), withFilter(leaf("labels", ">=", "a"))),
		"bad enum value":    req(withType("plant"), withFilter(leaf("condition", "=", "nope"))),
		"filter without ty": req(withFilter(leaf("condition", "=", "growing"))),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			resp := mustRefuse(t, f.deps(), r)
			out := Render(resp)
			if !strings.Contains(out, "NEXT") {
				t.Errorf("a refusal ends without a next call:\n%s", out)
			}
			for _, p := range resp.Problems {
				if p.Fix == nil || strings.TrimSpace(*p.Fix) == "" {
					t.Errorf("refusal %q states what went wrong and not what to do: %q", name, p.Reason)
				}
			}
		})
	}
}
