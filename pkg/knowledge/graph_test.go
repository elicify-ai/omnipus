// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// graph_test.go — the graph half of the unit: backlinks across all four link
// forms (US-8 AS-2, AC-7.2), reproducibility (US-11 AS-1/AS-2, FR-046),
// orphans and outlines (FR-051, FR-062), attachments indexed but never read
// (FR-039a), and the neighbourhood bounds (FR-054).

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestGraph_BacklinksFromAllFourForms covers US-8 AS-2 / AC-7.2 directly: four
// notes point at one target, each using a different spelling. "What links
// here?" must answer with all four, because the reader does not care which
// syntax the author happened to prefer.
func TestGraph_BacklinksFromAllFourForms(t *testing.T) {
	g, _, _ := b3Graph(t, map[string]string{
		"Target.md":          "# Target\n\n## Heading\n",
		"bare.md":            "[[Target]]\n",
		"aliased.md":         "[[Target|see this]]\n",
		"headed.md":          "[[Target#Heading]]\n",
		"folder/pathed.md":   "[[Target]]\n",
		"folder/relative.md": "[link](../Target.md)\n",
		"unrelated.md":       "nothing here\n",
	})

	back := g.Backlinks("Target.md")
	from := make([]string, 0, len(back))
	for _, l := range back {
		from = append(from, l.From)
	}
	want := []string{"aliased.md", "bare.md", "folder/pathed.md", "folder/relative.md", "headed.md"}
	if strings.Join(from, ",") != strings.Join(want, ",") {
		t.Errorf("Backlinks(Target.md) came from %v, want %v", from, want)
	}
	if len(g.Backlinks("unrelated.md")) != 0 {
		t.Errorf("a note nothing points at must have no backlinks")
	}
}

// TestGraph_RebuildProducesIdenticalAnswers is spec test 32 (US-11 AS-1 and
// AS-2, FR-046, AC-6.1).
//
// Asserted on the ANSWERS, never on bytes: a scorch index is not
// byte-reproducible, so a byte-comparison property test would either fail
// non-deterministically or be weakened into one that asserts nothing.
func TestGraph_RebuildProducesIdenticalAnswers(t *testing.T) {
	files := map[string]string{
		"Target.md":             "# Target\n\n## Heading\n",
		"a/Index.md":            "# A\n[[Target#Heading]]\n",
		"b/c/Index.md":          "# C\n[[Index]]\n[[Missing]]\n",
		"Hub.md":                "[[Target]] [[a/Index]] [[../escape]] [[/etc/passwd]]\n",
		"Lonely.md":             "# Lonely\n",
		"assets/diagram-v3.png": "PNG-BYTES",
	}
	root := t.TempDir()
	for rel, body := range files {
		b3WriteNote(t, root, rel, body)
	}
	fake := b3Recording()
	cr := b3Root(t, fake, root)

	build := func() *LinkGraph {
		t.Helper()
		g, err := BuildLinkGraph(fake, cr)
		if err != nil {
			t.Fatalf("BuildLinkGraph: %v", err)
		}
		return g
	}
	first, second := build(), build()

	if !reflect.DeepEqual(first.Notes(), second.Notes()) {
		t.Errorf("note sets differ between builds:\n%v\n%v", first.Notes(), second.Notes())
	}
	for _, note := range first.Notes() {
		if !reflect.DeepEqual(first.Links(note), second.Links(note)) {
			t.Errorf("links of %q differ between builds", note)
		}
		if !reflect.DeepEqual(first.Backlinks(note), second.Backlinks(note)) {
			t.Errorf("backlinks of %q differ between builds", note)
		}
		if !reflect.DeepEqual(first.Outline(note), second.Outline(note)) {
			t.Errorf("outline of %q differs between builds", note)
		}
	}
	if !reflect.DeepEqual(first.Unresolved(), second.Unresolved()) {
		t.Errorf("unresolved sets differ between builds")
	}
	if !reflect.DeepEqual(first.Orphans(), second.Orphans()) {
		t.Errorf("orphan sets differ between builds:\n%v\n%v", first.Orphans(), second.Orphans())
	}
	if !reflect.DeepEqual(first.Ambiguous(), second.Ambiguous()) {
		t.Errorf("ambiguity reports differ between builds")
	}

	// The fixture must actually exercise every set, or "identical" is a
	// statement about four empty lists.
	if len(first.Unresolved()) == 0 || len(first.Orphans()) == 0 || len(first.Ambiguous()) == 0 {
		t.Fatalf("fixture is too weak to prove anything: unresolved=%d orphans=%d ambiguous=%d",
			len(first.Unresolved()), len(first.Orphans()), len(first.Ambiguous()))
	}
}

// TestGraph_OrphansAreNotesNothingPointsAt covers the orphan query (FR-051,
// DS-2 row 2: a one-note collection lists that note as an orphan).
func TestGraph_OrphansAreNotesNothingPointsAt(t *testing.T) {
	single, _, _ := b3Graph(t, map[string]string{"Only.md": "# Only\n"})
	if got := single.Orphans(); len(got) != 1 || got[0] != "Only.md" {
		t.Errorf("a collection of one note must list it as an orphan; got %v", got)
	}

	g, _, _ := b3Graph(t, map[string]string{
		"Hub.md":      "[[Reached]]\n",
		"Reached.md":  "# Reached\n",
		"Isolated.md": "# Isolated\n",
		"Outbound.md": "[[Reached]]\n",
	})
	got := g.Orphans()
	want := []string{"Hub.md", "Isolated.md", "Outbound.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Orphans() = %v, want %v — a note that links OUT but that nothing links back to is still unreachable",
			got, want)
	}
}

// TestGraph_OutlineIsAvailableForEveryNote covers FR-062's graph half.
func TestGraph_OutlineIsAvailableForEveryNote(t *testing.T) {
	g, _, _ := b3Graph(t, map[string]string{
		"Note.md": "---\ntitle: x\n---\n# One\ntext\n## Two\n### Three\n",
	})
	outline := g.Outline("Note.md")
	if len(outline) != 3 {
		t.Fatalf("outline = %+v, want 3 headings", outline)
	}
	for i, want := range []struct {
		level int
		text  string
	}{{1, "One"}, {2, "Two"}, {3, "Three"}} {
		if outline[i].Level != want.level || outline[i].Text != want.text {
			t.Errorf("heading %d = {%d %q}, want {%d %q}", i, outline[i].Level, outline[i].Text, want.level, want.text)
		}
	}
	if len(g.Outline("no-such-note.md")) != 0 {
		t.Errorf("an unknown note must have an empty outline, not a panic")
	}
}

// TestGraph_AttachmentsAreResolvableButNeverOpened covers FR-039a (AW-2,
// MV-19): an attachment is findable by name and path, and the indexer must
// not open its contents for any reason.
//
// Both halves are asserted, because either one alone is passable by a broken
// implementation — skipping attachments entirely also produces zero reads.
func TestGraph_AttachmentsAreResolvableButNeverOpened(t *testing.T) {
	g, cr, fake := b3Graph(t, map[string]string{
		"assets/diagram-v3.png": "PNG-BYTES-THAT-MUST-NOT-BE-READ",
		"assets/report.pdf":     "PDF-BYTES",
		"Note.md":               "![[diagram-v3.png]]\n[report](assets/report.pdf)\n",
	})

	links := g.Links("Note.md")
	if len(links) != 2 {
		t.Fatalf("expected 2 links, got %+v", links)
	}
	if links[0].To != "assets/diagram-v3.png" || links[0].State != ResolveResolved {
		t.Errorf("![[diagram-v3.png]] resolved to %q (%q), want assets/diagram-v3.png resolved", links[0].To, links[0].State)
	}
	if links[1].To != "assets/report.pdf" || links[1].State != ResolveResolved {
		t.Errorf("[report](assets/report.pdf) resolved to %q (%q)", links[1].To, links[1].State)
	}
	if !g.Backlinks("assets/diagram-v3.png")[0].Embed {
		t.Errorf("the embed must be recorded as an embed")
	}

	fake.mu.Lock()
	opened := append([]string(nil), fake.opened...)
	fake.mu.Unlock()
	for _, p := range opened {
		if strings.HasSuffix(p, ".png") || strings.HasSuffix(p, ".pdf") {
			t.Errorf("an attachment's contents were read: %q (FR-039a forbids opening it for any reason)", p)
		}
	}
	// Positive control: the note itself WAS read, so "opened nothing" cannot
	// pass this test.
	wantNote := filepath.Join(cr.Path(), "Note.md")
	found := false
	for _, p := range opened {
		if p == wantNote {
			found = true
		}
	}
	if !found {
		t.Fatalf("positive control: the markdown note was never opened; opened=%v", opened)
	}
}

// TestGraph_NeighborhoodIsBoundedAndClampingIsReported covers FR-054 and D7's
// bounds. A caller that asked for five hops and received three must be able to
// tell, or it reads a truncated neighbourhood as a complete one.
func TestGraph_NeighborhoodIsBoundedAndClampingIsReported(t *testing.T) {
	// A chain N0 -> N1 -> ... -> N8, so hop distance is exactly index distance.
	files := map[string]string{}
	for i := 0; i < 9; i++ {
		body := fmt.Sprintf("# N%d\n", i)
		if i < 8 {
			body += fmt.Sprintf("[[N%d]]\n", i+1)
		}
		files[fmt.Sprintf("N%d.md", i)] = body
	}
	g, _, _ := b3Graph(t, files)

	one := g.Neighborhood("N4.md", 1, 100)
	if strings.Join(one.Nodes, ",") != "N3.md,N4.md,N5.md" {
		t.Errorf("1-hop neighbourhood = %v, want the note and its two direct neighbours (links are followed both ways)", one.Nodes)
	}
	if one.HopsClamped || one.NodesClamped {
		t.Errorf("a request within both bounds must not report clamping: %+v", one)
	}

	far := g.Neighborhood("N4.md", 99, 100)
	if !far.HopsClamped {
		t.Fatalf("a 99-hop request must be clamped AND report it; got %+v", far)
	}
	if far.Hops != MaxNeighborhoodHops {
		t.Errorf("clamped hop count = %d, want %d", far.Hops, MaxNeighborhoodHops)
	}
	if len(far.Nodes) != 7 {
		t.Errorf("3-hop neighbourhood of N4 = %v, want 7 nodes (N1..N7)", far.Nodes)
	}

	capped := g.Neighborhood("N4.md", 3, 3)
	if len(capped.Nodes) != 3 {
		t.Errorf("node-capped neighbourhood returned %d nodes, want 3", len(capped.Nodes))
	}
	if !capped.NodesClamped {
		t.Errorf("stopping at the node cap must be reported, or a subset reads as the whole neighbourhood")
	}
	over := g.Neighborhood("N4.md", 1, MaxNeighborhoodNodes+1)
	if !over.NodesClamped || over.MaxNodes != MaxNeighborhoodNodes {
		t.Errorf("a node request above the cap must be clamped to %d and reported; got %+v", MaxNeighborhoodNodes, over)
	}
}

// TestGraph_AccessorsReturnCopies guards against a caller mutating the graph
// another caller is reading. The graph is built once and read from many
// places; handing out the internal slice makes that unsafe in a way no test
// of the graph's own logic would ever notice.
func TestGraph_AccessorsReturnCopies(t *testing.T) {
	g, _, _ := b3Graph(t, map[string]string{
		"Target.md": "# Target\n",
		"Source.md": "[[Target]]\n",
	})
	links := g.Links("Source.md")
	if len(links) != 1 {
		t.Fatalf("expected 1 link, got %d", len(links))
	}
	links[0].To = "tampered"
	if g.Links("Source.md")[0].To != "Target.md" {
		t.Errorf("mutating the returned slice changed the graph")
	}

	notes := g.Notes()
	notes[0] = "tampered"
	if g.Notes()[0] == "tampered" {
		t.Errorf("mutating the returned note list changed the graph")
	}
}

// TestGraph_SkippedEntriesSurviveIntoTheGraph — the walk's exclusions are part
// of the graph's answer, not swallowed by it (NB-9, FR-044).
func TestGraph_SkippedEntriesSurviveIntoTheGraph(t *testing.T) {
	root := t.TempDir()
	b3WriteNote(t, root, "Real.md", "# Real\n")
	b3WriteNote(t, root, "sub/Other.md", "# Other\n")
	fake := b3Recording()
	cr := b3Root(t, fake, root)
	fake.dirErrors[filepath.Join(cr.Path(), "sub")] = fmt.Errorf("permission denied")

	g, err := BuildLinkGraph(fake, cr)
	if err != nil {
		t.Fatalf("BuildLinkGraph: %v", err)
	}
	if len(g.Skipped()) != 1 || g.Skipped()[0].RelPath != "sub" {
		t.Fatalf("the unreadable directory did not reach the graph's Skipped(): %+v", g.Skipped())
	}
	if len(g.Notes()) != 1 || g.Notes()[0] != "Real.md" {
		t.Errorf("notes = %v, want [Real.md]", g.Notes())
	}
}

// TestGraph_EvictedNoteIsSkippedNotSilentlyEmpty — FR-111 asked of the
// graph's own read path, not just the write path.
//
// A cloud-dematerialised file (OneDrive/iCloud/rclone) stats with a real size
// and reads as a clean, errorless EOF. Before this fix, BuildLinkGraph took
// that as "a note with no links" and added it to Notes() without ever
// touching Skipped() — the exact silent-omission NB-9 forbids. This asserts
// the corrected shape: the note is ABSENT from Notes() and PRESENT in
// Skipped() with a reason, using the same a4DatalessFS eviction fake
// lifecycle_test.go already built for FR-111's write-path guard.
func TestGraph_EvictedNoteIsSkippedNotSilentlyEmpty(t *testing.T) {
	root := t.TempDir()
	realPath := b3WriteNote(t, root, "Evicted.md", "# Evicted\n\nReal content that a cloud provider later dematerialised.\n")
	b3WriteNote(t, root, "Real.md", "[[Evicted]]\n")

	fi, err := os.Stat(realPath)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}

	dfs := a4NewDatalessFS()
	cr := b3Root(t, dfs, root)
	// Key the fake on the RESOLVED path BuildLinkGraph will actually open —
	// the same path ResolveContained produces, which can differ from the
	// literal join above once symlinks (e.g. macOS's /tmp -> /private/tmp)
	// are evaluated.
	resolvedEvicted, err := cr.ResolveContained(dfs, "Evicted.md")
	if err != nil {
		t.Fatalf("ResolveContained(Evicted.md): %v", err)
	}
	dfs.dataless[resolvedEvicted] = fi.Size()
	g, err := BuildLinkGraph(dfs, cr)
	if err != nil {
		t.Fatalf("BuildLinkGraph: %v", err)
	}

	for _, n := range g.Notes() {
		if n == "Evicted.md" {
			t.Fatalf("Evicted.md must not appear in Notes() — it was never actually read: %v", g.Notes())
		}
	}

	skipped := g.Skipped()
	if len(skipped) != 1 || skipped[0].RelPath != "Evicted.md" {
		t.Fatalf("Evicted.md did not reach Skipped(): %+v", skipped)
	}
	if skipped[0].Reason != SkipUnreadable {
		t.Errorf("Skipped()[0].Reason = %q, want %q", skipped[0].Reason, SkipUnreadable)
	}
	if !strings.Contains(skipped[0].Detail, "evicted") {
		t.Errorf("Skipped()[0].Detail = %q, want it to name eviction so an operator can act on it", skipped[0].Detail)
	}

	// The file it points at existing is not in doubt — Files() lists the
	// walk's own view, independent of whether the content was readable.
	foundInFiles := false
	for _, f := range g.Files() {
		if f == "Evicted.md" {
			foundInFiles = true
		}
	}
	if !foundInFiles {
		t.Errorf("Evicted.md must still appear in Files() — the walk saw the directory entry fine")
	}
}
