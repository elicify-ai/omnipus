// Omnipus — the `and:`/`or:`/`not:` tree, under test. Version 2 of the view
// format stores a TREE, so the property that matters here changed shape: it is
// no longer "what does it refuse to translate PARTIALLY" alone, but "what does
// it now translate WHOLE, and what does it still refuse".
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// yamlTree decodes a snippet of Base filter YAML into the generic shape
// ParseBaseFile hands TranslateFilterTree, so the test's input is written the
// way a `.base` file actually writes it rather than as Go literals.
func yamlTree(t *testing.T, src string) any {
	t.Helper()
	var node any
	if err := yaml.Unmarshal([]byte(src), &node); err != nil {
		t.Fatalf("test fixture is not valid YAML: %v\n%s", err, src)
	}
	return normaliseYAML(node)
}

// normaliseYAML converts yaml.v3's map[string]any / []any shape into exactly
// what ParseBaseFile produces, so the tree walker sees identical input.
func normaliseYAML(v any) any {
	switch m := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range m {
			out[k] = normaliseYAML(val)
		}
		return out
	case []any:
		out := make([]any, 0, len(m))
		for _, e := range m {
			out = append(out, normaliseYAML(e))
		}
		return out
	}
	return v
}

// shapeNames render the intermediate tree as one line, so an assertion reads
// as the boolean structure a person would draw on paper.
var shapeNames = map[leafShape]string{
	shapeCompare:  "cmp",
	shapeContains: "contains",
	shapeTruthy:   "truthy",
	shapeFalsy:    "falsy",
	shapeIsSet:    "isset",
	shapeIsEmpty:  "isempty",
}

// rawShape renders one intermediate node. It deliberately does NOT resolve
// against a schema: the schema is not known while the tree is being walked
// (see translate.go's header), so this is the whole of what the walk decided.
func rawShape(n *rawNode) string {
	if n == nil {
		return "<none>"
	}
	switch n.Kind {
	case rawKindLost:
		return "LOST"
	case rawKindPrebuilt:
		return "FILE"
	case rawKindLeaf:
		s := n.Leaf.Property + ":" + shapeNames[n.Leaf.Shape]
		if n.Leaf.Shape == shapeCompare {
			s = n.Leaf.Property + ":" + string(n.Leaf.Op)
		}
		return s
	case rawKindAll:
		return "all(" + strings.Join(childShapes(n), ", ") + ")"
	case rawKindAny:
		return "any(" + strings.Join(childShapes(n), ", ") + ")"
	case rawKindNot:
		return "not(" + strings.Join(childShapes(n), ", ") + ")"
	}
	return "?"
}

func childShapes(n *rawNode) []string {
	out := make([]string, 0, len(n.Kids))
	for _, k := range n.Kids {
		out = append(out, rawShape(k))
	}
	return out
}

func TestTranslateFilterTree_AndCollectsEveryBranch(t *testing.T) {
	tr := TranslateFilterTree(yamlTree(t, `
and:
  - type == "decision"
  - status == "accepted"
  - priority >= 3
`))
	if got, want := rawShape(tr.Root), "all(status:=, priority:>=)"; got != want {
		t.Errorf("tree = %q, want %q", got, want)
	}
	if len(tr.TypeLiterals) != 1 || tr.TypeLiterals[0] != "decision" {
		t.Errorf("type literals = %v, want [decision]", tr.TypeLiterals)
	}
}

// TestTranslateFilterTree_OrBecomesAny is the capability version 2 exists for.
// Seven filter groups in the founder's vault are disjunctions and NONE of them
// was expressible before; each one disabled its whole view.
func TestTranslateFilterTree_OrBecomesAny(t *testing.T) {
	tr := TranslateFilterTree(yamlTree(t, `
or:
  - status == "accepted"
  - and:
      - priority >= 3
      - status == "proposed"
`))
	if got, want := rawShape(tr.Root), "any(status:=, all(priority:>=, status:=))"; got != want {
		t.Errorf("tree = %q, want %q — an `or:` is a disjunction the v2 format stores directly", got, want)
	}
}

// TestTranslateFilterTree_OrThatAssertsATypeIsStillLostWhole is the assertion
// that survived the version bump, and it is the important one.
//
// `or: [type == "content", type == "brand-kit"]` says "either type". A ViewDef
// declares at most ONE `type:`, and version 2's untyped view spans EVERY note
// in scope — strictly MORE rows than "one of these two". There is no leaf to
// fall back on either: `type` is the discriminator, not a declared property.
// So the group is reported verbatim, exactly as it was under version 1.
func TestTranslateFilterTree_OrThatAssertsATypeIsStillLostWhole(t *testing.T) {
	tr := TranslateFilterTree(yamlTree(t, `
or:
  - type == "content"
  - status == "accepted"
  - and:
      - type == "brand-kit"
      - priority >= 3
`))
	if got := rawShape(tr.Root); got != "LOST" {
		t.Errorf("tree = %q, want LOST — no part of an \"either type\" may be salvaged", got)
	}
	if len(tr.TypeLiterals) != 0 {
		t.Errorf("an `or:` group yielded type literals %v — \"either type\" is not evidence for one type", tr.TypeLiterals)
	}
	for _, want := range []string{"or:", "content", "brand-kit", "priority >= 3"} {
		if !strings.Contains(tr.Root.Verbatim, want) {
			t.Errorf("the verbatim record of the lost `or:` does not mention %q (FR-101 preserves it whole):\n%s", want, tr.Root.Verbatim)
		}
	}
}

// TestTranslateFilterTree_OrFactorsOutATypeEveryBranchAsserts is the one place
// a type literal is harvested from a disjunction, and the reason it is safe is
// arithmetic rather than judgement: `(A AND T) OR (B AND T)` IS `T AND (A OR
// B)`. That is the founder's Subscriptions.base, whose outer filter is two
// folder scopes each re-asserting the same type, and which disabled all four of
// that base's views.
func TestTranslateFilterTree_OrFactorsOutATypeEveryBranchAsserts(t *testing.T) {
	tr := TranslateFilterTree(yamlTree(t, `
or:
  - and:
      - file.inFolder("01-Areas/Subscriptions")
      - type == "subscription"
  - and:
      - file.inFolder("Personal/Subscriptions")
      - type == "subscription"
`))
	if len(tr.TypeLiterals) != 1 || tr.TypeLiterals[0] != "subscription" {
		t.Fatalf("type literals = %v, want [subscription] — every branch asserts it, so the view requires it", tr.TypeLiterals)
	}
	if got, want := rawShape(tr.Root), "any(FILE, FILE)"; got != want {
		t.Errorf("tree = %q, want %q — the remainders are the disjunction", got, want)
	}
}

// TestTranslateFilterTree_OrRefusesEveryFactorisationThatIsNotExact is the
// guard on the test above. Each case below LOOKS close to the factorable shape
// and is not equal to it, so each must be lost whole.
func TestTranslateFilterTree_OrRefusesEveryFactorisationThatIsNotExact(t *testing.T) {
	cases := []struct {
		name string
		src  string
		why  string
	}{
		{
			name: "two different types",
			src: `
or:
  - and:
      - type == "content"
      - status == "accepted"
  - and:
      - type == "brand-kit"
      - status == "accepted"
`,
			why: "a ViewDef declares one type; \"either type\" is not one type, and an untyped view spans MORE than the two",
		},
		{
			name: "one branch does not assert the type at all",
			src: `
or:
  - and:
      - type == "decision"
      - status == "accepted"
  - status == "proposed"
`,
			why: "the second branch does not require the type, so factoring it out would newly require it of that branch",
		},
		{
			name: "a branch that is only the type literal makes the group a tautology",
			src: `
or:
  - type == "decision"
  - and:
      - type == "decision"
      - status == "accepted"
`,
			why: "still exactly equal, but by a DIFFERENT simplification with a different proof — refused rather than folded in",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := TranslateFilterTree(yamlTree(t, tc.src))
			if got := rawShape(tr.Root); got != "LOST" {
				t.Errorf("tree = %q, want LOST — %s", got, tc.why)
			}
			if len(tr.TypeLiterals) != 0 {
				t.Errorf("type literals = %v, want none — %s", tr.TypeLiterals, tc.why)
			}
		})
	}
}

// TestTranslateFilterTree_NotIsTheTreeNegation. Obsidian ANDs a `not:` list and
// negates the whole thing, which version 2 spells `{not: {all: [...]}}` — so
// unlike version 1, a MULTI-clause `not:` loses nothing.
func TestTranslateFilterTree_NotIsTheTreeNegation(t *testing.T) {
	t.Run("one wrapped leaf", func(t *testing.T) {
		tr := TranslateFilterTree(yamlTree(t, `
not:
  - status == "archived"
`))
		if got, want := rawShape(tr.Root), "not(status:=)"; got != want {
			t.Errorf("tree = %q, want %q", got, want)
		}
	})

	t.Run("two wrapped clauses become not(all(...))", func(t *testing.T) {
		tr := TranslateFilterTree(yamlTree(t, `
not:
  - status == "archived"
  - priority >= 3
`))
		if got, want := rawShape(tr.Root), "not(all(status:=, priority:>=))"; got != want {
			t.Errorf("tree = %q, want %q — De Morgan is not needed once the format stores a tree", got, want)
		}
	})

	t.Run("a double negative nests rather than cancelling", func(t *testing.T) {
		tr := TranslateFilterTree(yamlTree(t, `
not:
  - not:
      - status == "archived"
`))
		if got, want := rawShape(tr.Root), "not(not(status:=))"; got != want {
			t.Errorf("tree = %q, want %q", got, want)
		}
	})

	t.Run("a folder exclusion is now a real node", func(t *testing.T) {
		tr := TranslateFilterTree(yamlTree(t, `
not:
  - file.inFolder("99-Temp")
`))
		if got, want := rawShape(tr.Root), "not(FILE)"; got != want {
			t.Errorf("tree = %q, want %q — FR-134 gives file.inFolder a translation", got, want)
		}
	})

	t.Run("a wrapped type literal loses the whole not", func(t *testing.T) {
		tr := TranslateFilterTree(yamlTree(t, `
not:
  - type == "decision"
`))
		if len(tr.TypeLiterals) != 0 {
			t.Errorf("`not: [type == \"decision\"]` yielded type literals %v — that expression EXCLUDES the type, it does not assert it", tr.TypeLiterals)
		}
		if got := rawShape(tr.Root); got != "LOST" {
			t.Errorf("tree = %q, want LOST", got)
		}
	})
}

// TestTranslateFilterTree_TheStandingFR105Example is the example FR-105 is
// written around, end to end through the tree walker — and under version 2 it
// TRANSLATES. That is the whole measured point of this change: the two folder
// exclusions that used to disable Decisions.base now become real nodes, so the
// imported view excludes exactly the scratch notes the original excluded.
func TestTranslateFilterTree_TheStandingFR105Example(t *testing.T) {
	tr := TranslateFilterTree(yamlTree(t, `
and:
  - type == "decision"
  - not:
      - file.inFolder("99-Temp")
  - not:
      - file.inFolder("00-Inbox")
`))
	if got, want := rawShape(tr.Root), "all(not(FILE), not(FILE))"; got != want {
		t.Errorf("tree = %q, want %q", got, want)
	}
	if len(tr.TypeLiterals) != 1 || tr.TypeLiterals[0] != "decision" {
		t.Errorf("type literals = %v, want [decision]", tr.TypeLiterals)
	}

	// The folders themselves have to reach the emitted node, or the exclusion
	// is a shape with nothing in it.
	res := leafResolver{recordType: "decision", schemas: decisionSchema()}
	node, losses := res.resolve(tr.Root, LossBaseOuterFilter)
	if len(losses) != 0 {
		t.Fatalf("the standing example still loses something: %v", losses)
	}
	rendered := renderVerbatim(filterNodeYAML(*node))
	for _, want := range []string{"file.folder", "99-Temp", "00-Inbox", "LIKE", "not:"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the emitted filter does not mention %q:\n%s", want, rendered)
		}
	}
}

func TestTranslateFilterTree_ShapesItDoesNotRecognise(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"a combinator this importer does not know", `xor:\n  - a == "1"`},
		{"a node with two combinator keys at once", "and:\n  - a == \"1\"\nor:\n  - b == \"2\""},
		{"a scalar that is not a string", "42"},
		{"a formula reference", `formula.age > 30`},
		{"a file method with no filter meaning", `file.asLink()`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.ReplaceAll(tc.src, `\n`, "\n")
			tr := TranslateFilterTree(yamlTree(t, src))
			if len(tr.TypeLiterals) != 0 {
				t.Errorf("an unrecognised shape produced types %v — it must be refused whole", tr.TypeLiterals)
			}
			if got := rawShape(tr.Root); got != "LOST" {
				t.Errorf("tree = %q, want LOST", got)
			}
		})
	}
}

func TestTranslateFilterTree_NilIsNotALoss(t *testing.T) {
	tr := TranslateFilterTree(nil)
	if tr.Root != nil || len(tr.TypeLiterals) != 0 {
		t.Errorf("a base with no `filters:` at all produced %+v, want an empty translation", tr)
	}
}

// TestParseBaseFile_ReadsViewsAndFilters covers the read half: a `.base`
// file's own shape.
func TestParseBaseFile_ReadsViewsAndFilters(t *testing.T) {
	pb, err := ParseBaseFile([]byte(`
filters:
  and:
    - type == "decision"
views:
  - type: table
    name: All
    order:
      - status
  - type: cards
    name: Cards
`))
	if err != nil {
		t.Fatalf("ParseBaseFile: %v", err)
	}
	if len(pb.Views) != 2 {
		t.Fatalf("got %d views, want 2", len(pb.Views))
	}
	if pb.Views[0]["name"] != "All" || pb.Views[1]["type"] != "cards" {
		t.Errorf("views decoded wrong: %+v", pb.Views)
	}
	if tr := TranslateFilterTree(pb.Filters); len(tr.TypeLiterals) != 1 {
		t.Errorf("the outer filter's type literal did not survive parsing: %+v", tr)
	}
}

func TestParseBaseFile_RefusesInvalidYAML(t *testing.T) {
	if _, err := ParseBaseFile([]byte("views:\n  - name: [unclosed\n")); err == nil {
		t.Error("a `.base` file that is not valid YAML must be refused, not silently read as empty")
	}
}
