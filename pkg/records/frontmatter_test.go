// Omnipus — tests for lexical frontmatter reading (FR-005, FR-007, FR-020b).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// TestFrontmatter_PreservesLexicalForm is FR-020b at its source. If the parser
// ever unmarshals into `any`, 349.98 becomes a float64 before this package sees
// it and the exactness promise is already broken — so this asserts the SOURCE
// TEXT survives.
func TestFrontmatter_PreservesLexicalForm(t *testing.T) {
	src := "---\narr: 349.98\nbig: 9007199254740993\nquoted: \"349.98\"\n---\nbody\n"
	fm, err := ParseFrontmatter([]byte(src))
	if err != nil {
		t.Fatalf("%v", err)
	}
	for key, want := range map[string]string{
		"arr":    "349.98",
		"big":    "9007199254740993",
		"quoted": "349.98",
	} {
		n, ok := fm.Get(key)
		if !ok {
			t.Fatalf("%q missing", key)
		}
		if n.Text != want {
			t.Fatalf("FR-020b: %q must keep its source text %q, got %q", key, want, n.Text)
		}
	}
	if n, _ := fm.Get("arr"); n.Tag != "!!float" {
		t.Fatalf("the fixture must be a value YAML types as a float, or the test proves nothing; tag was %q", n.Tag)
	}
	if n, _ := fm.Get("quoted"); !n.Quoted {
		t.Fatalf("quoting must be observable — FR-030 wants relations stored as QUOTED wikilinks")
	}
}

// TestFrontmatter_ThreeStatesAreDistinguishable covers FR-007 and §8 R-3 at the
// parse layer.
func TestFrontmatter_ThreeStatesAreDistinguishable(t *testing.T) {
	fm, err := ParseFrontmatter([]byte("---\na: \"\"\nb:\nc: null\nd: []\n---\n"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if _, ok := fm.Get("missing"); ok {
		t.Fatalf("a key that is not there must not be found")
	}
	if n, _ := fm.Get("a"); n.Kind != KindScalar || n.Text != "" {
		t.Fatalf("R-3: an empty string is a scalar VALUE; got kind=%v text=%q", n.Kind, n.Text)
	}
	for _, key := range []string{"b", "c"} {
		if n, ok := fm.Get(key); !ok || n.Kind != KindNull {
			t.Fatalf("%q: an empty or explicitly null key must be KindNull; got %v", key, n.Kind)
		}
	}
	if n, _ := fm.Get("d"); n.Kind != KindSequence || len(n.Items) != 0 {
		t.Fatalf("R-3: an empty list is a sequence with no items; got %v", n.Kind)
	}
}

// TestFrontmatter_AwkwardRealFiles covers the DS-3 shapes that a naive splitter
// gets wrong.
func TestFrontmatter_AwkwardRealFiles(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		present bool
		wantKey string
		wantVal string
	}{
		{"CRLF line endings", "---\r\ntype: widget\r\nname: A\r\n---\r\nbody\r\n", true, "name", "A"},
		{"UTF-8 BOM", "\xef\xbb\xbf---\ntype: widget\nname: A\n---\n", true, "name", "A"},
		{"comments and blank lines inside frontmatter", "---\n# leading comment\ntype: widget\n\n# between\nname: A\n---\n", true, "name", "A"},
		{"single-quoted scalar", "---\nname: 'A: B'\n---\n", true, "name", "A: B"},
		{"terminated with ...", "---\nname: A\n...\nbody\n", true, "name", "A"},
		{"frontmatter is the whole file", "---\nname: A\n", true, "name", "A"},
		{"no frontmatter", "just prose\n", false, "", ""},
		{"--- appearing later is not frontmatter", "prose\n---\nname: A\n---\n", false, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fm, err := ParseFrontmatter([]byte(tc.src))
			if err != nil {
				t.Fatalf("%v", err)
			}
			if fm.Present != tc.present {
				t.Fatalf("Present = %v, want %v", fm.Present, tc.present)
			}
			if !tc.present {
				return
			}
			n, ok := fm.Get(tc.wantKey)
			if !ok {
				t.Fatalf("%q missing; keys = %v", tc.wantKey, fm.Keys)
			}
			if n.Text != tc.wantVal {
				t.Fatalf("%q = %q, want %q", tc.wantKey, n.Text, tc.wantVal)
			}
		})
	}
}

// TestFrontmatter_DuplicateKeyIsReported guards against YAML's own silence: it
// resolves a duplicate key by keeping one and saying nothing, which means the
// file states two things and a reader cannot see which won.
func TestFrontmatter_DuplicateKeyIsReported(t *testing.T) {
	fm, err := ParseFrontmatter([]byte("---\nstatus: todo\nstatus: done\n---\n"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(fm.Problems) == 0 {
		t.Fatalf("a duplicate frontmatter key must be reported, not silently resolved")
	}
	if !strings.Contains(fm.Problems[0], "status") {
		t.Fatalf("the problem must name the key; got %q", fm.Problems[0])
	}
	if n, _ := fm.Get("status"); n.Text != "todo" {
		t.Fatalf("resolution must be deterministic (first wins); got %q", n.Text)
	}

	set := loadSet(t, map[string]string{"widget.yaml": filterFixture})
	rec := ParseRecord("dup.md", []byte("---\ntype: widget\nstatus: todo\nstatus: done\n---\n"))
	rep := ValidateRecord(set, rec, ValidateOptions{})
	if rep.Valid() {
		t.Fatalf("a duplicate key must surface as a validation finding on the record")
	}
	if rep.Errors()[0].Code != FindingDuplicateKey {
		t.Fatalf("expected %q, got %q", FindingDuplicateKey, rep.Errors()[0].Code)
	}
}

// TestWikilink_ShapeIsD51 covers the on-disk relation form (ADR-068 D5.1) and
// §8 R-8's "identity is the target, never the display text".
func TestWikilink_ShapeIsD51(t *testing.T) {
	good := []struct{ raw, target, heading, display string }{
		{"[[Acme Ltd]]", "Acme Ltd", "", ""},
		{"[[Acme Ltd|Acme]]", "Acme Ltd", "", "Acme"},
		{"[[Acme Ltd#Contacts]]", "Acme Ltd", "Contacts", ""},
		{"[[Acme Ltd#Contacts|People]]", "Acme Ltd", "Contacts", "People"},
	}
	for _, tc := range good {
		w, ok := ParseWikilink(tc.raw)
		if !ok {
			t.Fatalf("%q must parse as a wikilink", tc.raw)
		}
		if w.Target != tc.target || w.Heading != tc.heading || w.Display != tc.display {
			t.Fatalf("%q: got %+v", tc.raw, w)
		}
	}

	for _, bad := range []string{"Acme Ltd", "[Acme]", "[[]]", "[[ ]]", "[[a", "a]]", "[[[[a]]]]"} {
		if _, ok := ParseWikilink(bad); ok {
			t.Fatalf("%q must not parse as a wikilink", bad)
		}
	}

	t.Run("D5.1 the parsed Target is the key the index joins on", func(t *testing.T) {
		// §8 R-8 — "relations compare by target identity, never by display
		// text" — is the COMPARATOR's rule and is covered in full by
		// compare_truthtable_test.go's R-8 case, including the part this
		// parser cannot know: without a resolver the oracle REFUSES to
		// compare rather than falling back to link text.
		//
		// What belongs here is the half the parser owns: two spellings that
		// differ only in display must yield the SAME Target, so the resolver
		// they are handed to sees one key and not two.
		a, _ := ParseWikilink("[[Acme Ltd]]")
		b, _ := ParseWikilink("[[Acme Ltd|the client]]")
		c, _ := ParseWikilink("[[Acme Ltd#Contacts|People]]")
		if a.Target != b.Target || a.Target != c.Target {
			t.Fatalf("D5.1: display text and headings are not identity; got %q, %q, %q", a.Target, b.Target, c.Target)
		}
		if b.Display == "" || c.Heading == "" {
			t.Fatalf("the fixture must actually carry a display and a heading, or it proves nothing")
		}

		// And with a resolver in place, the oracle agrees they are one record.
		resolver := func(l Wikilink) (string, bool) {
			if l.Target == "Acme Ltd" {
				return "CO-0001", true
			}
			return "", false
		}
		cmp := Comparator{ResolveRelation: resolver}
		left := singletonOperand(TypedValue{Type: TypeRelation, Link: a})
		right := singletonOperand(TypedValue{Type: TypeRelation, Link: b})
		equal, problems := cmp.Evaluate(OpEqual, left, right)
		if !equal || len(problems) != 0 {
			t.Fatalf("§8 R-8: two links resolving to one record are equal; got equal=%v problems=%v", equal, problems)
		}
	})
}
