// Omnipus — the `and:`/`or:`/`not:` tree, under test. The property that
// matters here is not "what does it translate" but "what does it refuse to
// translate PARTIALLY": a half-translated boolean group is exactly how a
// view broadens (FR-105).
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

func leafProps(leaves []RawLeaf) []string {
	out := make([]string, 0, len(leaves))
	for _, l := range leaves {
		s := l.Property + " " + l.Op
		if l.Negate {
			s = "NOT(" + s + ")"
		}
		out = append(out, s)
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
	if got, want := strings.Join(leafProps(tr.Leaves), " | "), "status eq | priority gte"; got != want {
		t.Errorf("leaves = %q, want %q", got, want)
	}
	if len(tr.TypeLiterals) != 1 || tr.TypeLiterals[0] != "decision" {
		t.Errorf("type literals = %v, want [decision]", tr.TypeLiterals)
	}
	if len(tr.Lost) != 0 {
		t.Errorf("nothing should be lost here, got %v", tr.Lost)
	}
}

// TestTranslateFilterTree_OrIsLostWholeAndNeverMined is the single most
// important assertion in this file. Our filter is a flat AND list with no
// disjunction, so ANY clause salvaged out of an `or:` is a clause the view
// now REQUIRES that the original merely offered as one alternative — and a
// type literal salvaged out of one is a type the view now claims on evidence
// that said "either".
func TestTranslateFilterTree_OrIsLostWholeAndNeverMined(t *testing.T) {
	tr := TranslateFilterTree(yamlTree(t, `
or:
  - type == "content"
  - status == "accepted"
  - and:
      - type == "brand-kit"
      - priority >= 3
`))
	if len(tr.Leaves) != 0 {
		t.Errorf("an `or:` group yielded %d AND-leaves (%v) — every one of them narrows the view further than the original did", len(tr.Leaves), leafProps(tr.Leaves))
	}
	if len(tr.TypeLiterals) != 0 {
		t.Errorf("an `or:` group yielded type literals %v — \"either type\" is not evidence for one type", tr.TypeLiterals)
	}
	if len(tr.Lost) != 1 {
		t.Fatalf("an `or:` group must be reported lost as ONE unit, got %d entries: %v", len(tr.Lost), tr.Lost)
	}
	for _, want := range []string{"or:", "content", "brand-kit", "priority >= 3"} {
		if !strings.Contains(tr.Lost[0], want) {
			t.Errorf("the verbatim record of the lost `or:` does not mention %q (FR-101 preserves it whole):\n%s", want, tr.Lost[0])
		}
	}
}

// TestTranslateFilterTree_NotCollapsesOnlyWhenItIsOneCleanLeaf. Obsidian ANDs
// a `not:` list and negates the whole thing, which De Morgan turns into a
// disjunction. Exactly one wrapped leaf is the only case with no disjunction
// in it.
func TestTranslateFilterTree_NotCollapsesOnlyWhenItIsOneCleanLeaf(t *testing.T) {
	t.Run("one clean leaf becomes that leaf's negate flag", func(t *testing.T) {
		tr := TranslateFilterTree(yamlTree(t, `
not:
  - status == "archived"
`))
		if len(tr.Lost) != 0 {
			t.Fatalf("nothing should be lost, got %v", tr.Lost)
		}
		if got, want := strings.Join(leafProps(tr.Leaves), ""), "NOT(status eq)"; got != want {
			t.Errorf("leaves = %q, want %q", got, want)
		}
	})

	t.Run("a double negative returns to the plain leaf", func(t *testing.T) {
		tr := TranslateFilterTree(yamlTree(t, `
not:
  - not:
      - status == "archived"
`))
		if got, want := strings.Join(leafProps(tr.Leaves), ""), "status eq"; got != want || len(tr.Lost) != 0 {
			t.Errorf("leaves = %q lost = %v, want %q and nothing lost", got, tr.Lost, want)
		}
	})

	t.Run("two wrapped clauses are lost whole", func(t *testing.T) {
		tr := TranslateFilterTree(yamlTree(t, `
not:
  - status == "archived"
  - priority >= 3
`))
		if len(tr.Leaves) != 0 {
			t.Errorf("a two-clause `not:` yielded leaves %v — NOT(A AND B) is (NOT A) OR (NOT B), and neither half alone is the original", leafProps(tr.Leaves))
		}
		if len(tr.Lost) != 1 {
			t.Fatalf("want one verbatim loss, got %v", tr.Lost)
		}
	})

	t.Run("a wrapped clause that is itself untranslatable loses the whole not", func(t *testing.T) {
		tr := TranslateFilterTree(yamlTree(t, `
not:
  - file.inFolder("99-Temp")
`))
		if len(tr.Leaves) != 0 {
			t.Errorf("leaves = %v, want none", leafProps(tr.Leaves))
		}
		if len(tr.Lost) != 1 || !strings.Contains(tr.Lost[0], "inFolder") {
			t.Errorf("lost = %v, want one entry naming inFolder verbatim", tr.Lost)
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
		if len(tr.Lost) != 1 {
			t.Errorf("lost = %v, want one verbatim entry", tr.Lost)
		}
	})
}

// TestTranslateFilterTree_TheStandingFR105Example is the example FR-105 is
// written around, end to end through the tree walker: the two folder
// exclusions must both survive as NAMED losses, because dropping them
// silently is what admits every scratch note in the vault.
func TestTranslateFilterTree_TheStandingFR105Example(t *testing.T) {
	tr := TranslateFilterTree(yamlTree(t, `
and:
  - type == "decision"
  - not:
      - file.inFolder("99-Temp")
  - not:
      - file.inFolder("00-Inbox")
`))
	if len(tr.Lost) != 2 {
		t.Fatalf("want both folder exclusions reported lost, got %d: %v", len(tr.Lost), tr.Lost)
	}
	joined := strings.Join(tr.Lost, "\n")
	for _, want := range []string{"99-Temp", "00-Inbox"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the lost clauses do not name %q — a loss nobody can read is not a named loss:\n%s", want, joined)
		}
	}
	if len(tr.Leaves) != 0 {
		t.Errorf("leaves = %v, want none — no part of an untranslatable exclusion may be kept", leafProps(tr.Leaves))
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := strings.ReplaceAll(tc.src, `\n`, "\n")
			tr := TranslateFilterTree(yamlTree(t, src))
			if len(tr.Leaves) != 0 || len(tr.TypeLiterals) != 0 {
				t.Errorf("an unrecognised shape produced leaves %v / types %v — it must be refused whole", leafProps(tr.Leaves), tr.TypeLiterals)
			}
			if len(tr.Lost) != 1 {
				t.Errorf("lost = %v, want exactly one verbatim entry", tr.Lost)
			}
		})
	}
}

func TestTranslateFilterTree_NilIsNotALoss(t *testing.T) {
	tr := TranslateFilterTree(nil)
	if len(tr.Leaves) != 0 || len(tr.TypeLiterals) != 0 || len(tr.Lost) != 0 {
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
