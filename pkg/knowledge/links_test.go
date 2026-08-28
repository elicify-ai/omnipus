// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// links_test.go — FR-040 (resolution order), FR-041 (ambiguity is resolved AND
// reported), FR-042 (no match is reported unresolved), FR-045 (no language
// model in the indexing, resolution or rewriting path), and FR-034a's
// streaming clause.
//
// Expected values here come from the spec's own datasets — DS-1 (link
// resolution) and DS-3 (filenames) — not from what the parser happens to do.

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// b3Graph builds a graph over a fresh temp collection whose files are given as
// rel path -> content.
func b3Graph(t *testing.T, files map[string]string) (*LinkGraph, CollectionRoot, *b3RecordingFS) {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		b3WriteNote(t, root, rel, body)
	}
	fake := b3Recording()
	cr := b3Root(t, fake, root)
	g, err := BuildLinkGraph(fake, cr)
	if err != nil {
		t.Fatalf("BuildLinkGraph: %v", err)
	}
	return g, cr, fake
}

func b3LinkTo(t *testing.T, g *LinkGraph, from, rawTarget string) ResolvedLink {
	t.Helper()
	for _, l := range g.Links(from) {
		if l.Target == rawTarget {
			return l
		}
	}
	t.Fatalf("no link with target %q found in %q; links = %+v", rawTarget, from, g.Links(from))
	return ResolvedLink{}
}

// TestResolveLink_AllFourForms is spec test 17 (US-7 AS-1, US-8 AS-2,
// DS-1 rows 1-4, FR-040, FR-060).
//
// The four spellings are four ways of writing the same edge. Each must land on
// the same note; the alias and the heading are additional information carried
// alongside, never a different destination.
func TestResolveLink_AllFourForms(t *testing.T) {
	g, _, _ := b3Graph(t, map[string]string{
		"Target.md":        "# Target\n\nintro\n\n## Heading\n\nbody\n",
		"folder/Nested.md": "# Nested\n",
		"Source.md": strings.Join([]string{
			"# Source",
			"bare: [[Target]]",
			"alias: [[Target|a different name]]",
			"heading: [[Target#Heading]]",
			"path: [[folder/Nested]]",
			"",
		}, "\n"),
	})

	cases := []struct {
		rawTarget string
		wantTo    string
		wantAlias string
		wantHead  string
	}{
		{"Target", "Target.md", "", ""},
		{"Target", "Target.md", "a different name", ""},
		{"Target", "Target.md", "", "Heading"},
		{"folder/Nested", "folder/Nested.md", "", ""},
	}
	links := g.Links("Source.md")
	if len(links) != len(cases) {
		t.Fatalf("expected %d links in Source.md, got %d: %+v", len(cases), len(links), links)
	}
	for i, tc := range cases {
		l := links[i]
		if l.State != ResolveResolved {
			t.Errorf("form %d (%q) did not resolve: state=%q reason=%q", i+1, l.Raw, l.State, l.Reason)
			continue
		}
		if l.To != tc.wantTo {
			t.Errorf("form %d (%q) resolved to %q, want %q", i+1, l.Raw, l.To, tc.wantTo)
		}
		if l.Alias != tc.wantAlias {
			t.Errorf("form %d (%q) alias = %q, want %q", i+1, l.Raw, l.Alias, tc.wantAlias)
		}
		if l.Heading != tc.wantHead {
			t.Errorf("form %d (%q) heading = %q, want %q", i+1, l.Raw, l.Heading, tc.wantHead)
		}
	}

	// DS-1 row 3: the heading link resolves TO the heading, so it must have
	// been located inside the target — not merely carried as a string.
	headingLink := links[2]
	if !headingLink.HeadingFound {
		t.Errorf("[[Target#Heading]] did not locate the heading in Target.md; outline = %+v", g.Outline("Target.md"))
	}
	if headingLink.HeadingLine != 5 {
		t.Errorf("[[Target#Heading]] resolved to line %d, want 5 (the '## Heading' line)", headingLink.HeadingLine)
	}

	// A heading anchor that does not exist must be reported as not found while
	// the link itself still resolves — the note is real, the section is not.
	g2, _, _ := b3Graph(t, map[string]string{
		"Target.md": "# Target\n",
		"Source.md": "[[Target#Nope]]\n",
	})
	l := b3LinkTo(t, g2, "Source.md", "Target")
	if l.State != ResolveResolved {
		t.Errorf("a link with a missing heading must still resolve to the note; got %q", l.State)
	}
	if l.HeadingFound {
		t.Errorf("a heading that does not exist must not be reported as found")
	}
}

// TestResolveLink_TieBreakAndAmbiguityReport is spec test 18 (US-11 AS-3,
// DS-1 row 5, FR-040, FR-041).
//
// Resolving silently is the failure mode this guards: the reader believes the
// link went where they meant, and nothing afterwards can tell them it did not.
// So the assertions are paired — it resolves by the stated rule, AND it is
// listed as ambiguous.
func TestResolveLink_TieBreakAndAmbiguityReport(t *testing.T) {
	// DS-1 row 5 exactly: a/Index.md and b/c/Index.md.
	g, _, _ := b3Graph(t, map[string]string{
		"a/Index.md":   "# A index\n",
		"b/c/Index.md": "# C index\n",
		"Source.md":    "see [[Index]]\n",
	})
	l := b3LinkTo(t, g, "Source.md", "Index")
	if l.State != ResolveResolved {
		t.Fatalf("an ambiguous basename must still RESOLVE (FR-041); got state=%q reason=%q", l.State, l.Reason)
	}
	if l.To != "a/Index.md" {
		t.Errorf("tie-break chose %q, want %q — the shortest collection-relative path wins", l.To, "a/Index.md")
	}
	if !l.Ambiguous {
		t.Fatalf("the ambiguity was not reported; determinism must never hide it (FR-041)")
	}
	if got, want := strings.Join(l.Candidates, ","), "a/Index.md,b/c/Index.md"; got != want {
		t.Errorf("candidates = %q, want %q (tie-break order)", got, want)
	}
	amb := g.Ambiguous()
	if len(amb) != 1 || amb[0].To != "a/Index.md" {
		t.Errorf("Ambiguous() = %+v, want exactly the one link that went through the tie-break", amb)
	}

	// Second tier of FR-040: when the paths are the same length, the
	// lexicographically first wins. "aa/Index.md" and "bb/Index.md" are both
	// 11 runes, so only the lexicographic rule can decide.
	g2, _, _ := b3Graph(t, map[string]string{
		"bb/Index.md": "# bb\n",
		"aa/Index.md": "# aa\n",
		"Source.md":   "see [[Index]]\n",
	})
	l2 := b3LinkTo(t, g2, "Source.md", "Index")
	if l2.To != "aa/Index.md" {
		t.Errorf("equal-length tie resolved to %q, want %q (lexicographically first)", l2.To, "aa/Index.md")
	}
	if !l2.Ambiguous {
		t.Errorf("the equal-length tie must also be reported ambiguous")
	}

	// An EXACT path match is never ambiguous, even when the basename is.
	g3, _, _ := b3Graph(t, map[string]string{
		"a/Index.md":   "# A\n",
		"b/c/Index.md": "# C\n",
		"Source.md":    "see [[b/c/Index]]\n",
	})
	l3 := b3LinkTo(t, g3, "Source.md", "b/c/Index")
	if l3.To != "b/c/Index.md" {
		t.Errorf("an exact path must win over the basename tie-break: got %q", l3.To)
	}
	if l3.Ambiguous {
		t.Errorf("an exact path match is not ambiguous")
	}

	// The case the two fixtures above cannot reach, and the one a real
	// collection hits first: one of the duplicates sits at the COLLECTION
	// ROOT. "[[Index]]" is a bare basename — Obsidian treats it as one and so
	// must the report — but "Index.md" is also a literal path, so the
	// exact-path rung of FR-040 matches and returns. Every duplicate-basename
	// collision in such a collection was therefore reported UNAMBIGUOUS while
	// resolving, which is precisely the silent resolution FR-041 exists to
	// prevent: the target is right, and nothing tells the reader that two
	// other notes answered to the same name.
	//
	// Both halves are asserted, because either alone is passable by a broken
	// implementation: sending the bare name to the basename rung would report
	// the ambiguity but could change the target, and the target must stay the
	// root file (shortest path, FR-040).
	g4, _, _ := b3Graph(t, map[string]string{
		"Index.md":        "# root\n",
		"a/Index.md":      "# a\n",
		"b/deep/Index.md": "# b\n",
		"z/Note.md":       "see [[Index]]\n",
	})
	l4 := b3LinkTo(t, g4, "z/Note.md", "Index")
	if l4.State != ResolveResolved || l4.To != "Index.md" {
		t.Errorf("root-level duplicate resolved to %q (state %q), want %q — shortest path wins",
			l4.To, l4.State, "Index.md")
	}
	if !l4.Ambiguous {
		t.Fatalf("a bare basename matching three notes was reported UNAMBIGUOUS because one of " +
			"them is at the collection root (FR-041)")
	}
	if got, want := strings.Join(l4.Candidates, ","), "Index.md,a/Index.md,b/deep/Index.md"; got != want {
		t.Errorf("candidates = %q, want %q (tie-break order)", got, want)
	}
	if amb4 := g4.Ambiguous(); len(amb4) != 1 {
		t.Errorf("Ambiguous() = %+v, want exactly one entry — the collection-wide report is the "+
			"only way an operator can discover this after the fact", amb4)
	}
}

// TestResolveLink_UnresolvedIsReportedNotDropped covers FR-042 and DS-1 rows 6
// and 9 (US-7 AS-8, E-6). A broken link must be visible: the reader has to be
// able to tell "this points nowhere" from "there was no link here".
func TestResolveLink_UnresolvedIsReportedNotDropped(t *testing.T) {
	g, _, _ := b3Graph(t, map[string]string{
		"Target.md": "# Target\n",
		"Source.md": "missing: [[Missing]]\nempty: [[]]\ngood: [[Target]]\n",
	})
	byReason := map[UnresolvedReason]string{}
	for _, u := range g.Unresolved() {
		byReason[u.Reason] = u.Target
	}
	if got, ok := byReason[ReasonNoMatch]; !ok || got != "Missing" {
		t.Errorf("[[Missing]] must be reported unresolved with reason %q; unresolved=%+v", ReasonNoMatch, g.Unresolved())
	}
	if _, ok := byReason[ReasonEmptyTarget]; !ok {
		t.Errorf("[[]] must be reported unresolved with reason %q, not crash and not vanish; unresolved=%+v",
			ReasonEmptyTarget, g.Unresolved())
	}
	if len(g.Unresolved()) != 2 {
		t.Errorf("expected exactly 2 unresolved links, got %d: %+v", len(g.Unresolved()), g.Unresolved())
	}
	// The unresolved links are still LINKS of the note — the outline of what
	// the note points at includes the broken ones.
	if got := len(g.Links("Source.md")); got != 3 {
		t.Errorf("Source.md has %d links, want 3 (two broken, one good)", got)
	}
}

// TestResolveLink_AwkwardFilenamesAreAddressable covers DS-3: the operator's
// own filenames. Stage 0 exists because Omnipus was refusing files it did not
// name; a link to one of them must resolve like any other.
func TestResolveLink_AwkwardFilenamesAreAddressable(t *testing.T) {
	files := map[string]string{
		"Ordinary Note.md":       "# Ordinary\n",
		"Meeting: 2026-01-01.md": "# Meeting\n",
		"Why?.md":                "# Why\n",
		"Ünïcödé — Näme.md":      "# Unicode\n",
		"elicify-* packages.md":  "# Packages\n",
	}
	source := "# Source\n"
	for name := range files {
		source += "- [[" + strings.TrimSuffix(name, ".md") + "]]\n"
	}
	files["Source.md"] = source

	g, _, _ := b3Graph(t, files)
	for name := range files {
		if name == "Source.md" {
			continue
		}
		stem := strings.TrimSuffix(name, ".md")
		l := b3LinkTo(t, g, "Source.md", stem)
		if l.State != ResolveResolved || l.To != name {
			t.Errorf("[[%s]] resolved to %q (state %q, reason %q), want %q",
				stem, l.To, l.State, l.Reason, name)
		}
	}
}

// TestExtractLinks_CodeIsNotALink asserts that link syntax inside a fenced
// block or an inline code span is sample text, not an edge in the graph. A
// note documenting wikilink syntax must not acquire links to notes it merely
// describes.
func TestExtractLinks_CodeIsNotALink(t *testing.T) {
	src := strings.Join([]string{
		"# Doc",
		"real: [[Alpha]]",
		"inline: `[[Beta]]` is how you write it",
		"```",
		"[[Gamma]]",
		"```",
		"~~~markdown",
		"[[Delta]]",
		"~~~",
		"after the fence: [[Epsilon]]",
		"",
	}, "\n")
	extracted := ExtractLinks([]byte(src))
	got := make([]string, 0, len(extracted))
	for _, l := range extracted {
		got = append(got, l.Target)
	}
	want := "Alpha,Epsilon"
	if strings.Join(got, ",") != want {
		t.Errorf("ExtractLinks found %v, want [%s] — code samples are not links", got, want)
	}
}

// TestExtractLinks_MarkdownFormsAndExternalTargets covers relative markdown
// links (which are edges) and everything that is not one.
func TestExtractLinks_MarkdownFormsAndExternalTargets(t *testing.T) {
	src := strings.Join([]string{
		"# Doc",
		"[rel](../folder/Target.md)",
		"[encoded](My%20Note.md)",
		"[angled](<spaced name.md>)",
		"[titled](Target.md \"a title\")",
		"[anchored](Target.md#Section)",
		"[web](https://example.com/x)",
		"[mail](mailto:someone@example.com)",
		"[same-note](#Section)",
		"![embedded](assets/diagram.png)",
		"![[assets/diagram.png]]",
		"",
	}, "\n")
	links := ExtractLinks([]byte(src))
	got := make([]string, 0, len(links))
	for _, l := range links {
		got = append(got, l.Target)
	}
	want := []string{
		"../folder/Target.md",
		"My Note.md",
		"spaced name.md",
		"Target.md",
		"Target.md",
		"assets/diagram.png",
		"assets/diagram.png",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("ExtractLinks targets = %v, want %v", got, want)
	}
	if links[4].Heading != "Section" {
		t.Errorf("markdown anchor = %q, want %q", links[4].Heading, "Section")
	}
	if !links[5].Embed || !links[6].Embed {
		t.Errorf("the two image forms must both be marked as embeds: %+v %+v", links[5], links[6])
	}
	if links[6].Kind != LinkWikilink {
		t.Errorf("![[…]] must be a wikilink embed, got kind %q", links[6].Kind)
	}
}

// TestExtractHeadings_OutlineExcludesCodeAndFrontmatter covers FR-062 and
// US-7 AS-4: frontmatter is metadata, and a "#" in a shell example is a
// comment, not a section.
func TestExtractHeadings_OutlineExcludesCodeAndFrontmatter(t *testing.T) {
	src := strings.Join([]string{
		"---",
		"title: Note",
		"# not a heading, this is a YAML comment",
		"---",
		"# Title",
		"intro",
		"## Section One ##",
		"```sh",
		"# rm -rf /",
		"```",
		"###### Deep",
		"#NotAHeading",
		"####### TooDeep",
		"",
	}, "\n")
	got := ExtractHeadings([]byte(src))
	want := []struct {
		level int
		text  string
		line  int
	}{
		{1, "Title", 5},
		{2, "Section One", 7},
		{6, "Deep", 11},
	}
	if len(got) != len(want) {
		t.Fatalf("outline = %+v, want %d headings", got, len(want))
	}
	for i, w := range want {
		if got[i].Level != w.level || got[i].Text != w.text || got[i].Line != w.line {
			t.Errorf("heading %d = {level:%d text:%q line:%d}, want {level:%d text:%q line:%d}",
				i, got[i].Level, got[i].Text, got[i].Line, w.level, w.text, w.line)
		}
	}
}

// TestExtractLinks_FrontmatterLinksAreLinks — a note can name another note in
// a frontmatter field, and a rename has to rewrite it (US-13 AS-2). So
// frontmatter is excluded from the OUTLINE but not from the graph.
func TestExtractLinks_FrontmatterLinksAreLinks(t *testing.T) {
	src := "---\nrelated: \"[[Target]]\"\n---\n# Body\n"
	links := ExtractLinks([]byte(src))
	if len(links) != 1 || links[0].Target != "Target" {
		t.Fatalf("frontmatter link not extracted: %+v", links)
	}
	if len(ExtractHeadings([]byte(src))) != 1 {
		t.Errorf("frontmatter must not contribute headings to the outline")
	}
}

// b3RepeatingReader yields a pattern indefinitely up to total bytes without
// ever materialising it, so a large note can be scanned without the TEST
// allocating what the scanner is being asked not to allocate.
type b3RepeatingReader struct {
	pattern []byte
	total   int64
	sent    int64
	pos     int
}

func (r *b3RepeatingReader) Read(p []byte) (int, error) {
	if r.sent >= r.total {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && r.sent < r.total {
		p[n] = r.pattern[r.pos]
		r.pos = (r.pos + 1) % len(r.pattern)
		n++
		r.sent++
	}
	return n, nil
}

// TestScanNote_StreamsInABoundedWindow covers FR-034a: a note has no maximum
// size, and link and heading extraction stream over the whole file.
//
// The oracle is the scanner's own high-water buffer mark — a discrete count of
// the property being claimed. An implementation that read the note into memory
// first (io.ReadAll, os.ReadFile) reports a high-water mark the size of the
// note and fails immediately, while still producing every correct link.
func TestScanNote_StreamsInABoundedWindow(t *testing.T) {
	// One 200-byte line repeated 125,000 times: a 25 MB note, one link a line.
	// An exact multiple, so the expected link count is exact rather than
	// approximate — an off-by-one here would weaken the assertion.
	line := "filler text [[Target]] more filler " + strings.Repeat("x", 164) + "\n"
	if len(line) != 200 {
		t.Fatalf("test fixture: line is %d bytes, want 200", len(line))
	}
	const lines = 125_000
	const total = 200 * lines
	r := &b3RepeatingReader{pattern: []byte(line), total: total}

	scan, err := ScanNote(r)
	if err != nil {
		t.Fatalf("ScanNote: %v", err)
	}
	if scan.Stats.BytesRead != total {
		t.Errorf("BytesRead = %d, want %d — the whole note must be scanned, never truncated (FR-034a)",
			scan.Stats.BytesRead, total)
	}
	wantLinks := lines
	if len(scan.Links) != wantLinks {
		t.Errorf("found %d links, want %d — one per line, none lost at a chunk boundary",
			len(scan.Links), wantLinks)
	}
	limit := maxSegmentBytes + maxLinkBytes + scanReadChunk
	if scan.Stats.MaxBufferedBytes > limit {
		t.Errorf("scanner buffered %d bytes at peak (limit %d) while scanning %d bytes: it is not streaming",
			scan.Stats.MaxBufferedBytes, limit, total)
	}
	// And the peak must be far below the note's size, or "bounded" would be
	// satisfied by a bound larger than any real note.
	if int64(scan.Stats.MaxBufferedBytes) > total/8 {
		t.Errorf("peak buffer %d is not small relative to the %d-byte note", scan.Stats.MaxBufferedBytes, total)
	}
}

// TestScanNote_SingleEnormousLineIsScannedExactlyOnce is the awkward half of
// FR-034a: a note that is one line has no newline to split on, so the scanner
// must cut it into pieces itself — with an overlap, so a link straddling the
// cut is still found, and with de-duplication, so it is found ONCE.
func TestScanNote_SingleEnormousLineIsScannedExactlyOnce(t *testing.T) {
	// The fixture places one link in each of the four positions that matter,
	// with byte-exact arithmetic so the cut lands where it is claimed to:
	//
	//	Alpha  — before the cut, outside the overlap window
	//	Delta  — before the cut but INSIDE the overlap window, so it is scanned
	//	         twice and must still be reported once
	//	Beta   — straddling the cut, so it is only complete on the second pass
	//	Gamma  — after the cut
	//
	// Delta is the case a naive overlap gets wrong (duplicate) and Beta is the
	// case no overlap at all gets wrong (lost).
	const deltaStart = maxSegmentBytes - 2000 // inside the final maxLinkBytes
	const betaStart = maxSegmentBytes - 4     // spans the cut
	var b strings.Builder
	b.WriteString("[[Alpha]] ")
	b.WriteString(strings.Repeat("z", deltaStart-b.Len()))
	b.WriteString("[[Delta]]")
	b.WriteString(strings.Repeat("z", betaStart-b.Len()))
	b.WriteString("[[Beta]]")
	b.WriteString(strings.Repeat("y", 4096))
	b.WriteString("[[Gamma]]")
	if deltaStart <= maxSegmentBytes-maxLinkBytes {
		t.Fatalf("test fixture: Delta at %d is not inside the overlap window", deltaStart)
	}

	scan, err := ScanNote(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("ScanNote: %v", err)
	}
	got := make([]string, 0, len(scan.Links))
	for _, l := range scan.Links {
		got = append(got, l.Target)
	}
	if strings.Join(got, ",") != "Alpha,Delta,Beta,Gamma" {
		t.Errorf("links across the internal cut = %v, want [Alpha Delta Beta Gamma] — each exactly once", got)
	}
	limit := maxSegmentBytes + maxLinkBytes + scanReadChunk
	if scan.Stats.MaxBufferedBytes > limit {
		t.Errorf("peak buffer %d exceeds the bound %d on a single-line note", scan.Stats.MaxBufferedBytes, limit)
	}
}

// forbiddenModelImports are the packages that mean "a language model is being
// called". pkg/providers is this repository's LLM client layer; pkg/agent is
// the turn engine that drives it; the two SDKs are the direct vendor clients.
var forbiddenModelImports = []string{
	"github.com/elicify-ai/omnipus/pkg/providers",
	"github.com/elicify-ai/omnipus/pkg/agent",
	"github.com/anthropics/anthropic-sdk-go",
	"github.com/openai/openai-go",
}

// scanForModelImports parses every non-test Go file in dir and returns the
// forbidden imports it finds, together with how many files it read.
//
// It parses rather than greps: an identifier in a comment is not an import,
// and a substring scan cannot tell the difference.
func scanForModelImports(dir string) (found []string, filesScanned int, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0, err
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
		if perr != nil {
			return nil, filesScanned, fmt.Errorf("parse %s: %w", e.Name(), perr)
		}
		filesScanned++
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbiddenModelImports {
				if path == bad || strings.HasPrefix(path, bad+"/") {
					found = append(found, e.Name()+" -> "+path)
				}
			}
		}
	}
	return found, filesScanned, nil
}

// directImportsOf parses every non-test Go file in the package directory and
// returns file name -> the import paths it declares.
func directImportsOf(dir string) (map[string][]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string)
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if perr != nil {
			return nil, fmt.Errorf("parse %s: %w", name, perr)
		}
		for _, imp := range f.Imports {
			out[name] = append(out[name], strings.Trim(imp.Path.Value, `"`))
		}
	}
	return out, nil
}

// transitiveDeps returns the full dependency closure of the given packages, as
// go list computes it. An error here FAILS the caller — a closure this test
// could not compute proves nothing, and skipping would be a green.
func transitiveDeps(pkgs ...string) (map[string]struct{}, error) {
	if len(pkgs) == 0 {
		return map[string]struct{}{}, nil
	}
	args := append([]string{"list", "-deps", "-tags", "goolm,stdjson"}, pkgs...)
	cmd := exec.Command("go", args...)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.Output()
	if err != nil {
		detail := ""
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			detail = string(ee.Stderr)
		}
		return nil, fmt.Errorf("go list -deps: %w: %s", err, detail)
	}
	set := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[line] = struct{}{}
		}
	}
	return set, nil
}

// modelPackagesIn returns the forbidden model packages present in a closure.
func modelPackagesIn(closure map[string]struct{}) []string {
	var found []string
	for pkg := range closure {
		for _, bad := range forbiddenModelImports {
			if pkg == bad || strings.HasPrefix(pkg, bad+"/") {
				found = append(found, pkg)
			}
		}
	}
	sort.Strings(found)
	return found
}

// toolsPackagePath is the one package in this package's import list whose own
// dependency closure reaches a language-model client.
const toolsPackagePath = "github.com/elicify-ai/omnipus/pkg/tools"

// allowedToolsSelectors is every symbol pkg/knowledge may use from pkg/tools.
//
// It is a pin, not a style rule. pkg/tools is the tool-registry package: it
// carries the Tool interface this package must implement, and its own
// dependency closure reaches a provider client and two vendor SDKs. Confining
// the door to a named set of symbols — none of which can issue a model request
// — is what turns "we did not import a model" into "we could not have called
// one". A new entry here is not a failure; it is a REVIEW: add it only after
// checking that the symbol cannot reach a model.
var allowedToolsSelectors = []string{
	"BaseTool",          // embedded struct: name/schema plumbing only
	"CategoryMemory",    // a category constant
	"ErrorResult",       // constructs a ToolResult
	"MemoryRateLimiter", // the limiter type reused by FR-055
	// NewToolResult wraps a string in a ToolResult and does nothing else
	// (pkg/tools/result.go). vault_describe needs it because its response is
	// COMPACT TEXT the model must SEE (FR-072), where SilentResult's content
	// is withheld from the transcript. Reviewed against this list's own rule:
	// it takes a string, returns a struct, and touches no provider, no
	// registry and no client.
	"NewToolResult",
	"ScopeGeneral",    // a scope constant
	"SilentResult",    // constructs a ToolResult
	"Tool",            // the interface being implemented
	"ToolAgentID",     // reads the calling agent id out of the context
	"ToolCategory",    // a type
	"ToolResult",      // the result type
	"ToolScope",       // a type
	"ToolWorkspaceID", // reads the calling workspace id out of the context
}

// TestKnowledge_NoLanguageModelInTheGraphPath is spec test 33 (US-11 AS-4,
// FR-045, NB-5).
//
// Extraction, resolution and rewriting are deterministic text processing. If a
// model were ever consulted, the same notes would stop producing the same graph
// and every downstream answer would inherit the drift.
//
// # What this test proves, and what it deliberately does not
//
// It does NOT assert that pkg/knowledge's dependency closure is free of model
// clients. That property is FALSE and cannot be made true: implementing the
// agent-facing tools requires pkg/tools, whose own closure reaches
// github.com/anthropics/anthropic-sdk-go and pkg/providers. An earlier version
// of this test scanned direct imports of one directory and reported a clean
// verdict, which read as the stronger claim — a test that "reports safety"
// while being unable to fail for the realistic way FR-045 breaks.
//
// It proves three things instead, each of which CAN fail:
//
//	A. No file in this package imports a model client directly.
//	B. Every non-test file EXCEPT tools.go has a dependency closure with no
//	   model client in it at all. That is the whole indexing, scanning,
//	   resolution and rewriting path — FR-045's actual subject.
//	C. tools.go's use of pkg/tools is pinned to a named selector set, so the one
//	   file that does have a reachable model client cannot grow a new call into
//	   it without this test failing.
//
// Each part carries a positive control, because each part's clean result is
// otherwise indistinguishable from a scanner that stopped matching.
func TestKnowledge_NoLanguageModelInTheGraphPath(t *testing.T) {
	// ---- A. no direct import of a model client, anywhere in the package ----
	found, scanned, err := scanForModelImports(".")
	if err != nil {
		t.Fatalf("scanning the package: %v", err)
	}
	if scanned == 0 {
		t.Fatalf("the scanner read zero files — a green here would mean nothing")
	}
	if scanned < 3 {
		t.Fatalf("the scanner read only %d files; the package has more than that", scanned)
	}
	if len(found) != 0 {
		t.Errorf("pkg/knowledge imports a language-model client, which FR-045 forbids: %v", found)
	}

	fixture := t.TempDir()
	control := "package fixture\n\nimport (\n\t_ \"github.com/elicify-ai/omnipus/pkg/providers\"\n)\n"
	if writeErr := os.WriteFile(filepath.Join(fixture, "control.go"), []byte(control), 0o600); writeErr != nil {
		t.Fatalf("write control fixture: %v", writeErr)
	}
	clean := "package fixture\n\nimport (\n\t_ \"strings\"\n)\n"
	if writeErr := os.WriteFile(filepath.Join(fixture, "clean.go"), []byte(clean), 0o600); writeErr != nil {
		t.Fatalf("write clean fixture: %v", writeErr)
	}
	ctlFound, ctlScanned, err := scanForModelImports(fixture)
	if err != nil {
		t.Fatalf("scanning the control fixture: %v", err)
	}
	if ctlScanned != 2 {
		t.Fatalf("control fixture: scanned %d files, want 2", ctlScanned)
	}
	if len(ctlFound) != 1 || !strings.Contains(ctlFound[0], "pkg/providers") {
		t.Fatalf("the scanner failed to detect a known-bad import: found=%v — the clean result above proves nothing", ctlFound)
	}

	// ---- B. the graph path's transitive closure carries no model client ----
	byFile, err := directImportsOf(".")
	if err != nil {
		t.Fatalf("reading the package's imports: %v", err)
	}
	if len(byFile) < 10 {
		t.Fatalf("parsed %d non-test files; this package has more than that — the scan is not seeing the package", len(byFile))
	}

	seen := make(map[string]struct{})
	var graphPathImports []string
	toolsImporters := []string(nil)
	for file, imports := range byFile {
		for _, imp := range imports {
			if imp == toolsPackagePath || strings.HasPrefix(imp, toolsPackagePath+"/") {
				toolsImporters = append(toolsImporters, file)
				continue
			}
			if _, dup := seen[imp]; dup {
				continue
			}
			seen[imp] = struct{}{}
			graphPathImports = append(graphPathImports, imp)
		}
	}
	sort.Strings(toolsImporters)
	// The allow-list is the set of TOOL-ADAPTER files — the ones whose job is
	// to present this package to an agent. tools.go is the retrieval half;
	// authoring_tools.go (ADR-067 stage 3) is the authoring half, added when
	// the write path landed; scope_turn.go resolves which workspace a tool
	// call belongs to. All three are adapters and none is on the graph path,
	// which is what FR-045 is about.
	//
	// This stays an EXPLICIT literal rather than a "*_tools.go" pattern: the
	// point of the guard is that adding pkg/tools to a new file is a decision
	// somebody has to make on purpose, and a pattern would silently admit the
	// next file that happened to be named to fit.
	want := []string{"authoring_tools.go", "scope_turn.go", "tools.go"}
	if strings.Join(toolsImporters, ",") != strings.Join(want, ",") {
		t.Fatalf("pkg/tools is imported by %v, want exactly %v. It is the only import here whose own "+
			"closure reaches a language-model client, so it belongs in the tool-adapter files and "+
			"nowhere near the indexing, resolution or rewriting path (FR-045)", toolsImporters, want)
	}
	sort.Strings(graphPathImports)

	closure, err := transitiveDeps(graphPathImports...)
	if err != nil {
		t.Fatalf("computing the graph path's dependency closure: %v "+
			"(a closure this test cannot compute proves nothing, so this is a failure, not a skip)", err)
	}
	if len(closure) < 50 {
		t.Fatalf("the closure has %d packages, which is implausibly small — go list is not answering "+
			"about the real dependency graph", len(closure))
	}
	if bad := modelPackagesIn(closure); len(bad) != 0 {
		t.Errorf("the indexing/resolution/rewriting path can reach a language-model client through "+
			"its ordinary imports: %v (FR-045)", bad)
	}

	// Positive control for part B, and the honest statement of the situation:
	// pkg/tools DOES reach a model client, so the closure check above is a
	// check and not a function that always returns nothing.
	toolsClosure, err := transitiveDeps(toolsPackagePath)
	if err != nil {
		t.Fatalf("computing pkg/tools' closure (the positive control): %v", err)
	}
	if bad := modelPackagesIn(toolsClosure); len(bad) == 0 {
		t.Fatalf("the closure check found NO model client in pkg/tools, which is known to reach one. " +
			"Either go list is answering about the wrong thing or forbiddenModelImports has gone " +
			"stale — either way the clean result above proves nothing")
	}

	// ---- C. EVERY importer's use of pkg/tools is pinned ----
	//
	// Every importer, not just tools.go. Part B admits a file to the boundary;
	// part C is what says the boundary is narrow. Running C over one file while
	// B admits three means the other two may call anything pkg/tools exposes —
	// including, transitively, a provider client — and this test would stay
	// green. That was the state after authoring_tools.go was added to B's
	// allow-list and never reached C: the guard's own stated mechanism, "turns
	// 'we did not import a model' into 'we could not have called one'", did not
	// cover the rewriting path FR-045 newly names.
	allowed := make(map[string]struct{}, len(allowedToolsSelectors))
	for _, a := range allowedToolsSelectors {
		allowed[a] = struct{}{}
	}
	usedPerFile := map[string][]string{}
	for _, file := range toolsImporters {
		used, uErr := selectorsUsedFrom(file, toolsPackagePath)
		if uErr != nil {
			t.Fatalf("reading %s's use of pkg/tools: %v", file, uErr)
		}
		if len(used) == 0 {
			t.Fatalf("found zero pkg/tools selectors in %s, which imports it — the selector scan is blind", file)
		}
		usedPerFile[file] = used
		var unpinned []string
		for _, u := range used {
			if _, ok := allowed[u]; !ok {
				unpinned = append(unpinned, u)
			}
		}
		if len(unpinned) != 0 {
			t.Errorf("%s uses pkg/tools symbols that are not on the reviewed allow-list: %v. "+
				"pkg/tools can reach a language-model client; every symbol crossing that boundary must "+
				"be checked and listed in allowedToolsSelectors (FR-045)", file, unpinned)
		}
	}

	// Positive control for part C: the scanner finds selectors that are there,
	// in each of the two files whose contents this test knows.
	if !containsString(usedPerFile["tools.go"], "Tool") {
		t.Fatalf("the selector scan did not find tools.Tool, which tools.go certainly uses — " +
			"the clean result above proves nothing")
	}
	if !containsString(usedPerFile["authoring_tools.go"], "ToolResult") {
		t.Fatalf("the selector scan did not find tools.ToolResult in authoring_tools.go, which every " +
			"authoring tool returns — the clean result above proves nothing")
	}
}

// selectorsUsedFrom returns every symbol file selects from the package at
// importPath, e.g. "Tool" for a `tools.Tool` expression.
//
// The import's local name is read from the file rather than assumed, so an
// aliased import cannot slip past the scan.
func selectorsUsedFrom(file, importPath string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		return nil, err
	}
	local := ""
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != importPath {
			continue
		}
		if imp.Name != nil {
			local = imp.Name.Name
		} else {
			local = importPath[strings.LastIndex(importPath, "/")+1:]
		}
	}
	if local == "" {
		return nil, fmt.Errorf("%s does not import %s", file, importPath)
	}
	seen := make(map[string]struct{})
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != local || ident.Obj != nil {
			return true
		}
		if _, dup := seen[sel.Sel.Name]; dup {
			return true
		}
		seen[sel.Sel.Name] = struct{}{}
		out = append(out, sel.Sel.Name)
		return true
	})
	sort.Strings(out)
	return out, nil
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestNoteIndex_ResolutionOrderIsExactlyFR040 exercises the resolution ladder
// directly, without touching a filesystem, so each rung can be isolated.
func TestNoteIndex_ResolutionOrderIsExactlyFR040(t *testing.T) {
	ni := NewNoteIndex([]string{
		"Target.md",
		"a/Index.md",
		"b/c/Index.md",
		"assets/diagram-v3.png",
		"deep/folder/Target.md",
		"Solo.md", // the only genuinely unique basename in this fixture
	})
	wl := func(target string) Link { return Link{Kind: LinkWikilink, Target: target} }

	cases := []struct {
		name   string
		from   string
		link   Link
		wantTo string
		amb    bool
		reason UnresolvedReason
	}{
		{"exact path with extension", "Source.md", wl("deep/folder/Target.md"), "deep/folder/Target.md", false, ReasonNone},
		{"exact path without extension", "Source.md", wl("deep/folder/Target"), "deep/folder/Target.md", false, ReasonNone},
		{"unique basename", "Source.md", wl("Solo"), "Solo.md", false, ReasonNone},
		// FR-041 is about the BASENAME being ambiguous, not about which rung
		// of FR-040 happened to resolve it. "Target" names both Target.md and
		// deep/folder/Target.md, so the exact-path rung wins the resolution
		// (correctly, and the root file is the tie-break winner anyway) AND
		// the ambiguity is still reported. The oracle here was previously
		// `false`, read off an implementation that returned from the
		// exact-path rung before the ambiguity was ever computed.
		{"root-level duplicate still reports ambiguity", "Source.md", wl("Target"), "Target.md", true, ReasonNone},
		{"attachment by full name", "Source.md", wl("diagram-v3.png"), "assets/diagram-v3.png", false, ReasonNone},
		{"attachment by stem", "Source.md", wl("diagram-v3"), "assets/diagram-v3.png", false, ReasonNone},
		{"ambiguous basename", "Source.md", wl("Index"), "a/Index.md", true, ReasonNone},
		{"no match", "Source.md", wl("Nowhere"), "", false, ReasonNoMatch},
		{"empty", "Source.md", wl(""), "", false, ReasonEmptyTarget},
		{"absolute", "Source.md", wl("/etc/passwd"), "", false, ReasonAbsoluteTarget},
		{"traversal", "Source.md", wl("../../etc/passwd"), "", false, ReasonOutsideRoot},
		{
			"markdown link is relative to its note",
			"deep/folder/Source.md",
			Link{Kind: LinkMarkdown, Target: "Target.md"},
			"deep/folder/Target.md", false, ReasonNone,
		},
		{
			"markdown link is a path, never a basename search",
			"deep/Source.md",
			Link{Kind: LinkMarkdown, Target: "Target.md"},
			"", false, ReasonNoMatch,
		},
	}
	for _, tc := range cases {
		got := ni.Resolve(tc.from, tc.link)
		if tc.wantTo == "" {
			if got.State != ResolveUnresolved {
				t.Errorf("%s: state = %q, want unresolved", tc.name, got.State)
			}
			if got.Reason != tc.reason {
				t.Errorf("%s: reason = %q, want %q", tc.name, got.Reason, tc.reason)
			}
			continue
		}
		if got.State != ResolveResolved || got.To != tc.wantTo {
			t.Errorf("%s: resolved to %q (state %q, reason %q), want %q",
				tc.name, got.To, got.State, got.Reason, tc.wantTo)
		}
		if got.Ambiguous != tc.amb {
			t.Errorf("%s: ambiguous = %v, want %v", tc.name, got.Ambiguous, tc.amb)
		}
	}
}
