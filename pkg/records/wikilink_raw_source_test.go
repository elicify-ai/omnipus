// Omnipus — FR-030a / ADR-068 D24.6 ruling 2: the raw source text decides what
// a wikilink is.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func relationProp(t *testing.T, name string, many bool) *Property {
	t.Helper()
	p, err := NewProperty(Property{Name: name, Type: TypeRelation, Many: many, To: "company", RecordType: "deal"})
	if err != nil {
		t.Fatalf("NewProperty(relation): %v", err)
	}
	return p
}

// TestWikilink_RawSourceDecidesNotTheParsersShape is FR-030a's discriminating
// case, and it is built so that it CANNOT pass without the re-slicing mechanism
// and CANNOT pass for the wrong reason.
//
// The two inputs below parse to STRUCTURALLY IDENTICAL yaml trees. Executed
// against yaml.v3, both `- [[Target]]` and `- [ [Target] ]` produce:
//
//	seq(block) -> seq(flow) -> seq(flow) -> scalar "Target"
//
// with identical Kind, Style, Tag and Value at every node. The ONLY thing that
// differs is Column: 5/6/7 for the first, 5/7/8 for the second. So any
// implementation that decides from the parsed tree — its shape, its tags, its
// values, its nesting depth — MUST give both the same answer. Only going back
// to the operator's own bytes can tell them apart, which is the ruling.
//
// The first is a wikilink (66 real findings came from it being read as a list).
// The second is a genuine nested list and must stay one.
func TestWikilink_RawSourceDecidesNotTheParsersShape(t *testing.T) {
	const linkSrc = "---\ntype: deal\nrelated:\n  - [[Target]]\n---\n"
	const listSrc = "---\ntype: deal\nrelated:\n  - [ [Target] ]\n---\n"

	// FIRST, PROVE THE PREMISE. If these two ever stop being identical trees,
	// the test below is no longer discriminating and would pass for a reason
	// that has nothing to do with FR-030a.
	t.Run("the premise: both inputs parse to identical yaml trees", func(t *testing.T) {
		shape := func(src string) string {
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte(strings.SplitN(src, "---\n", 2)[1]), &doc); err != nil {
				t.Fatalf("yaml: %v", err)
			}
			var b strings.Builder
			var walk func(n *yaml.Node)
			walk = func(n *yaml.Node) {
				if n == nil {
					return
				}
				// Everything EXCEPT position. If the trees differ anywhere in
				// this projection, the premise is broken.
				b.WriteString(string(rune('0'+int(n.Kind))) + "/" + string(rune('0'+int(n.Style))) + "/" + n.Tag + "/" + n.Value + ";")
				for _, c := range n.Content {
					walk(c)
				}
			}
			walk(&doc)
			return b.String()
		}
		if a, b := shape(linkSrc), shape(listSrc); a != b {
			t.Fatalf("PREMISE BROKEN — the two inputs no longer parse identically, so this test no longer discriminates:\n  %s\n  %s", a, b)
		}
	})

	t.Run("`- [[Target]]` IS a wikilink", func(t *testing.T) {
		rec := ParseRecord("deal.md", []byte(linkSrc))
		p := relationProp(t, "related", true)
		pv := ResolveProperty(rec, p)

		if pv.State != StatePresent {
			t.Fatalf("FR-030a: an unquoted `- [[Target]]` must resolve as a link; got %s with findings %v", pv.State, pv.Findings)
		}
		if len(pv.Findings) != 0 {
			t.Fatalf("FR-030a: no finding is due; got %v", pv.Findings)
		}
		if len(pv.Values) != 1 {
			t.Fatalf("want exactly one link, got %v", pv.Values)
		}
		if got := pv.Values[0].Link.Target; got != "Target" {
			t.Errorf("link target = %q, want %q", got, "Target")
		}
		// The Node records that its SHAPE was decided by the source, and it
		// carries the operator's own bytes.
		n, _ := rec.Frontmatter.Get("related")
		if len(n.Items) != 1 || n.Items[0].RawSource != "[[Target]]" {
			t.Errorf("the element must carry its raw source `[[Target]]`; got %+v", n.Items)
		}
	})

	t.Run("`- [ [Target] ]` is a genuine nested list and stays one", func(t *testing.T) {
		rec := ParseRecord("deal.md", []byte(listSrc))
		p := relationProp(t, "related", true)
		pv := ResolveProperty(rec, p)

		if pv.State != StateNonConforming {
			t.Fatalf("FR-030a: `- [ [Target] ]` is a nested list, not a link; got %s", pv.State)
		}
		if len(pv.Findings) == 0 {
			t.Fatal("a nested list where a link belongs must be REPORTED")
		}
		if len(pv.Values) != 0 {
			t.Fatalf("a nested list must yield no link; got %v", pv.Values)
		}
		n, _ := rec.Frontmatter.Get("related")
		if len(n.Items) != 1 || n.Items[0].RawSource != "" {
			t.Errorf("a genuine list must NOT be re-shaped from source; got %+v", n.Items)
		}
	})
}

// TestWikilink_ColumnIsARuneIndexNotAByteIndex is the regression that ASCII
// fixtures cannot catch.
//
// yaml.v3's scanner advances Column once per CHARACTER, whatever its UTF-8
// width. So the naive `lineStart + Column - 1` is correct on ASCII and wrong by
// one byte for every multi-byte rune before the bracket — it passes every test
// written in English and mis-slices the moment a key or a value holds an
// accent. The founder's vault holds "Müller", "Straße" and Greek.
//
// Executed: for `Müller_straße: [[Ада]]` the flow sequence reports Column 16.
// The key is 13 RUNES but 15 BYTES, so the bracket is at byte offset 17 and
// byte-indexing would start the slice inside the `ß`.
func TestWikilink_ColumnIsARuneIndexNotAByteIndex(t *testing.T) {
	for _, tc := range []struct {
		name, key, target string
	}{
		{"german sharp s and umlaut", "Müller_straße", "Ада"},
		{"greek", "σίσυφος", "Acme"},
		{"cjk", "会社", "Acme"},
		{"emoji, which is 4 bytes and 1 rune", "🏢_owner", "Acme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "---\ntype: deal\n" + tc.key + ": [[" + tc.target + "]]\n---\n"
			// Sanity: this fixture really does have more bytes than runes
			// before the bracket, or it would not discriminate.
			if len([]byte(tc.key)) == len([]rune(tc.key)) {
				t.Fatalf("fixture %q is pure ASCII and cannot catch byte-vs-rune indexing", tc.key)
			}
			rec := ParseRecord("deal.md", []byte(src))
			p := relationProp(t, tc.key, false)
			pv := ResolveProperty(rec, p)

			if pv.State != StatePresent {
				t.Fatalf("FR-030a: `%s: [[%s]]` must resolve as a link; got %s with findings %v",
					tc.key, tc.target, pv.State, pv.Findings)
			}
			if len(pv.Values) != 1 || pv.Values[0].Link.Target != tc.target {
				t.Fatalf("link target = %v, want %q — a byte-indexed slice would start mid-rune and produce garbage",
					pv.Values, tc.target)
			}
		})
	}
}

// TestWikilink_RawTextRefusalsAreNormative covers the cases FR-030a's own
// parenthetical rules out, and the block scalar in the other direction.
func TestWikilink_RawTextRefusalsAreNormative(t *testing.T) {
	t.Run("`[[a, b]]` is a genuine two-element list, never a link", func(t *testing.T) {
		// FR-030a, normative per the operator's ruling: commas and brackets are
		// excluded from the inner text. Without this clause `[[a, b]]` becomes a
		// link to a note called "a, b" — the mirror image of the defect the
		// ruling exists to fix, and harder to see.
		rec := ParseRecord("deal.md", []byte("---\ntype: deal\nrelated:\n  - [[a, b]]\n---\n"))
		pv := ResolveProperty(rec, relationProp(t, "related", true))
		if pv.State != StateNonConforming {
			t.Fatalf("`[[a, b]]` must stay a list; got %s with values %v", pv.State, pv.Values)
		}
	})

	t.Run("`[[a],[b]]` is two lists, never a link to `a],[b`", func(t *testing.T) {
		// ParseWikilink alone would ACCEPT this: it only rejects an inner `[[`
		// or `]]`, and this has neither. The bracket exclusion is what stops it.
		if isExactWikilinkSource("[[a],[b]]") {
			t.Error("`[[a],[b]]` must not be read as wikilink source")
		}
	})

	t.Run("a multi-line flow sequence is not a link", func(t *testing.T) {
		// The span is read CORRECTLY across the newline; the content simply is
		// not a wikilink, because a note name does not span two lines.
		if isExactWikilinkSource("[[Very Long\n   Target]]") {
			t.Error("a wikilink cannot span lines")
		}
		rec := ParseRecord("deal.md", []byte("---\ntype: deal\nrelated:\n  - [[Very Long,\n     Target]]\n---\n"))
		pv := ResolveProperty(rec, relationProp(t, "related", true))
		if pv.State == StatePresent && len(pv.Values) > 0 {
			t.Errorf("a multi-line flow sequence must not become a link; got %v", pv.Values)
		}
	})

	t.Run("a block scalar reading `[[Acme]]` is NOT a link", func(t *testing.T) {
		// The IFF in the other direction. The scalar's folded Text is
		// "[[Acme]]\n" and ParseWikilink, which trims, would accept it — but the
		// RAW source is a `|` indicator plus an indented body, which is not
		// wikilink syntax. Accepting it would be the parser's shape overruling
		// the operator's bytes in the direction that happens to flatter us.
		rec := ParseRecord("deal.md", []byte("---\ntype: deal\ncompany: |\n  [[Acme]]\n---\n"))
		n, ok := rec.Frontmatter.Get("company")
		if !ok || !n.Block {
			t.Fatalf("fixture: the value must parse as a BLOCK scalar; got %+v", n)
		}
		pv := ResolveProperty(rec, relationProp(t, "company", false))
		if pv.State != StateNonConforming {
			t.Fatalf("FR-030a: a block scalar is not a wikilink; got %s with values %v", pv.State, pv.Values)
		}
		if len(pv.Findings) == 0 || pv.Findings[0].Code != FindingNotAWikilink {
			t.Fatalf("want a not-a-wikilink finding; got %v", pv.Findings)
		}
		if !strings.Contains(pv.Findings[0].Reason, "[[Target]]") {
			t.Errorf("the refusal must name the inline form that works; got %q", pv.Findings[0].Reason)
		}
	})

	t.Run("a QUOTED wikilink is unaffected, commas and all", func(t *testing.T) {
		// D5.1's convention is what our own writes produce, and a quoted scalar
		// never reaches the flow reading — so the comma exclusion above costs
		// only the UNQUOTED spelling, and the recovery is to quote it.
		rec := ParseRecord("deal.md", []byte("---\ntype: deal\ncompany: \"[[Smith, Jones]]\"\n---\n"))
		n, _ := rec.Frontmatter.Get("company")
		if !n.Quoted || n.Kind != KindScalar {
			t.Fatalf("fixture: expected a quoted scalar, got %+v", n)
		}
		pv := ResolveProperty(rec, relationProp(t, "company", false))
		if pv.State != StatePresent || len(pv.Values) != 1 {
			t.Fatalf("a quoted wikilink must still resolve; got %s / %v", pv.State, pv.Values)
		}
		if got := pv.Values[0].Link.Target; got != "Smith, Jones" {
			t.Errorf("target = %q, want %q — quoting is the recovery for a comma-named note", got, "Smith, Jones")
		}
	})

	t.Run("a scalar relation written unquoted resolves too", func(t *testing.T) {
		// `company: [[Acme]]` — the SCALAR case. ResolveProperty checks arity
		// before any value, so without the frontmatter-layer reshape this would
		// be refused as "holds a list" and never reach a value parser at all.
		rec := ParseRecord("deal.md", []byte("---\ntype: deal\ncompany: [[Acme]]\n---\n"))
		pv := ResolveProperty(rec, relationProp(t, "company", false))
		if pv.State != StatePresent {
			t.Fatalf("FR-030a must apply to a SCALAR property too; got %s with findings %v", pv.State, pv.Findings)
		}
		if len(pv.Values) != 1 || pv.Values[0].Link.Target != "Acme" {
			t.Fatalf("want a link to Acme; got %v", pv.Values)
		}
	})

	t.Run("display and heading forms survive the re-slice", func(t *testing.T) {
		for _, tc := range []struct{ src, target, display, heading string }{
			{"[[Acme|Acme Corp]]", "Acme", "Acme Corp", ""},
			{"[[Acme#Billing]]", "Acme", "", "Billing"},
			{"[[Acme#Billing|Bills]]", "Acme", "Bills", "Billing"},
		} {
			rec := ParseRecord("deal.md", []byte("---\ntype: deal\ncompany: "+tc.src+"\n---\n"))
			pv := ResolveProperty(rec, relationProp(t, "company", false))
			if pv.State != StatePresent || len(pv.Values) != 1 {
				t.Fatalf("%s: want one link, got %s / %v", tc.src, pv.State, pv.Values)
			}
			l := pv.Values[0].Link
			if l.Target != tc.target || l.Display != tc.display || l.Heading != tc.heading {
				t.Errorf("%s: got target=%q display=%q heading=%q", tc.src, l.Target, l.Display, l.Heading)
			}
		}
	})
}
