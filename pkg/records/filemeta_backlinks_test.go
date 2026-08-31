// Omnipus — tests for FR-132: file.backlinks derived, scoped, and bounded by a
// mid-scan abort with a named refusal.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// streamOf turns a fixed row slice into a LinkEdgeStream and counts how many
// rows the consumer actually pulled.
//
// The counter is the instrument the bound test depends on: without it, "the
// scan aborted" and "the scan ran to completion and then reported an error"
// look identical from the outside, and only one of them is FR-132.
func streamOf(rows []FileLinkRow, visited *int) LinkEdgeStream {
	return func(visit func(FileLinkRow) error) error {
		for _, r := range rows {
			if visited != nil {
				*visited++
			}
			if err := visit(r); err != nil {
				return err
			}
		}
		return nil
	}
}

func backlinkFixture() []FileLinkRow {
	return []FileLinkRow{
		// Three spellings of one reference, from three different notes.
		{NotePath: "Index/Clients.md", Target: "Clients/Acme Corp.md", Raw: "[[Clients/Acme Corp.md]]"},
		{NotePath: "Deals/Q3 Renewal.md", Target: "Clients/Acme Corp", Raw: "[[Clients/Acme Corp]]"},
		{NotePath: "People/Jane Roe.md", Target: "Acme Corp", Raw: "[[Acme Corp]]"},
		// An EMBED counts: a reader asking "what references this?" means both.
		{NotePath: "Boards/Wall.md", Target: "Acme Corp", Raw: "![[Acme Corp]]", Embed: true},
		// The same note linking twice is ONE backlink.
		{NotePath: "Index/Clients.md", Target: "Acme Corp", Raw: "[[Acme Corp]]"},
		// A self-link is not its own backlink.
		{NotePath: "Clients/Acme Corp.md", Target: "Acme Corp", Raw: "[[Acme Corp]]"},
		// Case: the fold is the package's one folding function.
		{NotePath: "Notes/lower.md", Target: "clients/acme corp", Raw: "[[clients/acme corp]]"},
		// Unrelated edge — proves the index is not answering "everything".
		{NotePath: "Notes/Other.md", Target: "Somewhere Else", Raw: "[[Somewhere Else]]"},
	}
}

func TestBacklinks_DerivedInverseOnAFixture(t *testing.T) {
	var visited int
	ix, err := BuildBacklinkIndex(BacklinkScope{PathPrefix: "vaults/main"}, streamOf(backlinkFixture(), &visited))
	if err != nil {
		t.Fatalf("BuildBacklinkIndex: %v", err)
	}
	if visited != len(backlinkFixture()) {
		t.Fatalf("visited %d edges of %d — the stream was not consumed once through", visited, len(backlinkFixture()))
	}
	if ix.EdgeCount() != len(backlinkFixture()) {
		t.Fatalf("EdgeCount = %d, want %d", ix.EdgeCount(), len(backlinkFixture()))
	}
	if ix.Scope().PathPrefix != "vaults/main" {
		t.Fatalf("scope = %+v", ix.Scope())
	}

	got := targetsOf(ix.For("Clients/Acme Corp.md"))
	want := []string{
		"Boards/Wall.md",
		"Deals/Q3 Renewal.md",
		"Index/Clients.md",
		"Notes/lower.md",
		"People/Jane Roe.md",
	}
	if !equalStrings(got, want) {
		t.Fatalf("backlinks = %v, want %v (sorted, deduped, self-link dropped, embed included)", got, want)
	}

	// A note nobody links to gets an empty list — and, resolved, an ABSENT
	// value, so `file.backlinks IS NULL` answers "orphan".
	if bl := ix.For("Notes/Other.md"); len(bl) != 0 {
		t.Fatalf("an unreferenced note has backlinks %v", targetsOf(bl))
	}

	m := FileMeta{Path: "Notes/Other.md"}
	ix.Apply(&m)
	if !m.BacklinksDerived {
		t.Fatal("Apply left BacklinksDerived false")
	}
	pv := mustResolve(t, FileBacklinksProp, m)
	if pv.State != StateAbsent {
		t.Fatalf("an unreferenced note's file.backlinks = %v, want absent", pv.State)
	}

	// And the referenced note resolves to the same five, through the property
	// layer rather than through the index's own accessor.
	m2 := FileMeta{Path: "Clients/Acme Corp.md"}
	ix.Apply(&m2)
	pv2 := mustResolve(t, FileBacklinksProp, m2)
	if got := texts(pv2); !equalStrings(got, want) {
		t.Fatalf("resolved file.backlinks = %v, want %v", got, want)
	}
}

func targetsOf(links []Wikilink) []string {
	out := make([]string, 0, len(links))
	for _, l := range links {
		out = append(out, l.Target)
	}
	return out
}

// FR-132's bound: the abort is MID-SCAN, and the refusal names the count, the
// scope and a remedy.
func TestBacklinks_RefusesAtTheEdgeBoundMidScan(t *testing.T) {
	original := MaxBacklinkEdges
	t.Cleanup(func() { MaxBacklinkEdges = original })

	// The real bound is 200,000. It is shrunk here so the far side of the
	// boundary can be observed without writing 200,001 rows — and the real
	// value is asserted separately below, so shrinking it in a test cannot
	// become a way of never testing the real number.
	MaxBacklinkEdges = 10

	const rows = 500
	edges := make([]FileLinkRow, 0, rows)
	for i := 0; i < rows; i++ {
		edges = append(edges, FileLinkRow{
			NotePath: fmt.Sprintf("Notes/n%04d.md", i),
			Target:   "Clients/Acme Corp.md",
		})
	}

	var visited int
	ix, err := BuildBacklinkIndex(BacklinkScope{PathPrefix: "vaults/main"}, streamOf(edges, &visited))
	if err == nil {
		t.Fatalf("built an index over %d edges with a bound of %d", ix.EdgeCount(), MaxBacklinkEdges)
	}
	if !IsBacklinkBoundExceeded(err) {
		t.Fatalf("error is %T (%v), want *BacklinkBoundError", err, err)
	}
	if ix != nil {
		t.Fatal("a refused derivation returned an index; a partial answer is the silent wrong result the bound exists to prevent")
	}

	// THE MID-SCAN ASSERTION. The stream must have stopped at the bound plus
	// one, not run to 500 and complained afterwards.
	if visited != MaxBacklinkEdges+1 {
		t.Fatalf("the stream delivered %d of %d rows; a mid-scan abort stops at %d",
			visited, rows, MaxBacklinkEdges+1)
	}

	var be *BacklinkBoundError
	if !errors.As(err, &be) {
		t.Fatalf("errors.As failed on %v", err)
	}
	if be.Limit != MaxBacklinkEdges {
		t.Fatalf("refusal reports limit %d, want %d", be.Limit, MaxBacklinkEdges)
	}
	if be.Count != MaxBacklinkEdges+1 {
		t.Fatalf("refusal reports count %d, want the edge that crossed the bound (%d)", be.Count, MaxBacklinkEdges+1)
	}
	msg := err.Error()
	for _, want := range []string{"vaults/main", "narrow the workspace scope", "derived, never stored"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal %q does not contain %q", msg, want)
		}
	}

	// Just under the bound still answers — otherwise the assertion above would
	// pass for an implementation that refused everything.
	MaxBacklinkEdges = rows
	visited = 0
	ok, err := BuildBacklinkIndex(BacklinkScope{}, streamOf(edges, &visited))
	if err != nil {
		t.Fatalf("exactly at the bound: %v", err)
	}
	if ok.EdgeCount() != rows {
		t.Fatalf("EdgeCount = %d, want %d", ok.EdgeCount(), rows)
	}
	if n := len(ok.For("Clients/Acme Corp.md")); n != rows {
		t.Fatalf("%d backlinks, want %d", n, rows)
	}
}

// The bound the SPEC states, asserted against the constant the test above is
// allowed to shrink. Without this, shrinking it in one test and never checking
// the real value would be indistinguishable from having no bound at all.
func TestBacklinks_BoundIsTheNumberTheSpecStates(t *testing.T) {
	if MaxBacklinkEdges != 200_000 {
		t.Fatalf("MaxBacklinkEdges = %d, want 200000 (FR-132)", MaxBacklinkEdges)
	}
}

// A backlinks question on a candidate nobody derived backlinks for is a caller
// defect, and it refuses instead of reporting "nothing links here".
func TestBacklinks_NotDerivedRefusesRatherThanAnsweringEmpty(t *testing.T) {
	m := FileMeta{Path: "Clients/Acme Corp.md"} // BacklinksDerived is false
	pv, err := ResolveFileProperty(FileBacklinksProp, m)
	if err == nil {
		t.Fatalf("resolved to %+v; an underived index must refuse, not report an orphan", pv)
	}
	var qe *QueryError
	if !errors.As(err, &qe) {
		t.Fatalf("error is %T, want *QueryError", err)
	}
	if !strings.Contains(err.Error(), "never derived") {
		t.Fatalf("refusal %q does not say the index was never derived", err.Error())
	}
	if qe.Remedy == "" {
		t.Fatal("refusal carries no remedy")
	}

	// The same candidate, with the index applied, answers.
	ix, err := BuildBacklinkIndex(BacklinkScope{}, streamOf(backlinkFixture(), nil))
	if err != nil {
		t.Fatalf("BuildBacklinkIndex: %v", err)
	}
	ix.Apply(&m)
	if _, err := ResolveFileProperty(FileBacklinksProp, m); err != nil {
		t.Fatalf("after Apply: %v", err)
	}
}

func TestBacklinks_NilStreamIsRefused(t *testing.T) {
	if _, err := BuildBacklinkIndex(BacklinkScope{}, nil); err == nil {
		t.Fatal("a nil stream built an index")
	}
}

// Determinism: Go randomises map iteration, so an unsorted answer would reorder
// a rendered column between two identical calls.
func TestBacklinks_OrderIsStableAcrossRuns(t *testing.T) {
	ix, err := BuildBacklinkIndex(BacklinkScope{}, streamOf(backlinkFixture(), nil))
	if err != nil {
		t.Fatalf("BuildBacklinkIndex: %v", err)
	}
	first := targetsOf(ix.For("Clients/Acme Corp.md"))
	for i := 0; i < 50; i++ {
		if got := targetsOf(ix.For("Clients/Acme Corp.md")); !equalStrings(got, first) {
			t.Fatalf("run %d gave %v, first run gave %v", i, got, first)
		}
	}
}

// An edge with no source or no target connects nothing and must not create a
// backlink from "" — which would otherwise show up as a phantom entry.
func TestBacklinks_EmptyEndpointsAreDropped(t *testing.T) {
	rows := []FileLinkRow{
		{NotePath: "", Target: "Acme"},
		{NotePath: "Notes/a.md", Target: ""},
		{NotePath: "Notes/b.md", Target: "Acme"},
	}
	var visited int
	ix, err := BuildBacklinkIndex(BacklinkScope{}, streamOf(rows, &visited))
	if err != nil {
		t.Fatalf("BuildBacklinkIndex: %v", err)
	}
	// They still COUNT against the bound — they were streamed, and the bound is
	// about work done, not rows kept.
	if ix.EdgeCount() != 3 {
		t.Fatalf("EdgeCount = %d, want 3", ix.EdgeCount())
	}
	if got := targetsOf(ix.For("Acme.md")); !equalStrings(got, []string{"Notes/b.md"}) {
		t.Fatalf("backlinks = %v, want only Notes/b.md", got)
	}
	if got := ix.For(""); got != nil {
		t.Fatalf("an empty path resolved to %v", targetsOf(got))
	}
}
