// Omnipus — ADR-067 stage 2: the two walkers must agree, and knowledge_graph
// must actually answer.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// FR-046 — one collection, one answer: the two walkers must agree
// ---------------------------------------------------------------------------

// TestWalkContained_SkipsTheSameToolStateScanDoes is the disagreement that made
// knowledge_search and knowledge_graph describe different collections.
//
// This package has two walkers. Scan walks for the INDEX and skips `.obsidian`,
// `.omnipus-vault`, `.git` and `.trash` — scan.go says why: they are tool
// state, not content. WalkContained walks for the LINK GRAPH and skipped
// nothing, so:
//
//   - notes in `.trash` were opened, scanned, and resurfaced as live backlinks
//     on real notes — a deleted note quietly re-entering the collection;
//   - every `.obsidian/plugins/**` file and every `.git` object became an
//     addressable wikilink target through the basename index;
//   - the same question asked of search and of the graph got two answers.
//
// The oracle is the SET EQUALITY of the two walkers over one real collection,
// not a hand-written list — a hand-written list drifts the moment the skip set
// changes, and would then assert the old answer on both sides.
func TestWalkContained_SkipsTheSameToolStateScanDoes(t *testing.T) {
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")

	// Real content, including a nested folder and a dotfile note: the skip set
	// covers named tool directories, NOT hidden files (DS-3 row 6).
	b5Note(t, vault, "Real.md", "see [[workspace]] and [[main]] and [[Deleted secret note]]\n")
	b5Note(t, vault, "sub/Nested.md", "# Nested\n")
	b5Note(t, vault, ".hidden.md", "# Hidden but real\n")

	// Tool state. None of it is content.
	b5Note(t, vault, ".trash/Deleted secret note.md", "# Deleted\n\nlinks to [[Real]]\n")
	b5Note(t, vault, ".git/COMMIT_EDITMSG", "wip\n")
	b5Note(t, vault, ".git/objects/ab/cdef", "binary-ish\n")
	b5Note(t, vault, ".obsidian/workspace.json", "{}\n")
	b5Note(t, vault, ".obsidian/plugins/evil/main.js", "module.exports = {}\n")

	root, err := NewCollectionRoot(OSLinkFS(), vault)
	require.NoError(t, err)

	walk, err := WalkContained(OSLinkFS(), root)
	require.NoError(t, err)

	scanned, err := Scan(vault)
	require.NoError(t, err)
	scanPaths := make([]string, 0, len(scanned.Entries))
	for _, e := range scanned.Entries {
		scanPaths = append(scanPaths, e.RelPath)
	}

	// --- positive control: both walkers actually found the content ---
	require.ElementsMatch(t, []string{"Real.md", "sub/Nested.md", ".hidden.md"}, scanPaths,
		"the index walker must see exactly the collection's content")

	assert.ElementsMatch(t, scanPaths, walk.Files,
		"the link-graph walker and the index walker must enumerate the SAME collection; "+
			"graph saw %v, index saw %v", walk.Files, scanPaths)

	// --- the consequences, asserted where an operator would meet them ---
	opened, restore := b2CountingOpen(t)
	defer restore()

	g, err := BuildLinkGraph(OSLinkFS(), root)
	require.NoError(t, err)

	assert.NotContains(t, g.Notes(), ".trash/Deleted secret note.md",
		"a note in .trash is deleted; it must not be scanned as a live note")
	for _, p := range *opened {
		assert.NotContains(t, p, string(filepath.Separator)+".trash"+string(filepath.Separator),
			"the graph builder opened %q — a deleted note's contents must never be read", p)
	}

	assert.Empty(t, g.Backlinks("Real.md"),
		"a link written inside .trash must not appear as an inbound link on a live note")

	for _, target := range []string{"workspace", "main", "Deleted secret note"} {
		l := b3LinkTo(t, g, "Real.md", target)
		assert.Equal(t, ResolveUnresolved, l.State,
			"[[%s]] resolved to %q — tool state must not be an addressable wikilink target",
			target, l.To)
	}

	// The positive control for resolution itself: a real note in a real
	// subfolder is still reachable by its bare basename.
	g2, _, _ := b3Graph(t, map[string]string{
		"Real.md":       "see [[Nested]]\n",
		"sub/Nested.md": "# Nested\n",
	})
	require.Equal(t, ResolveResolved, b3LinkTo(t, g2, "Real.md", "Nested").State,
		"ordinary resolution must still work, or the refusals above prove nothing")
}

// ---------------------------------------------------------------------------
// FR-051 / FR-054 / FR-055 — knowledge_graph's unexercised branches
// ---------------------------------------------------------------------------

// TestGraphTool_LinksOperationReturnsOutboundEdges covers FR-051's `links`
// operation, which no test invoked. Half of AC-7.2's surface was shipped
// unexecuted: an operation that panicked, returned backlinks instead, or
// returned nothing at all would have been indistinguishable from a green suite.
func TestGraphTool_LinksOperationReturnsOutboundEdges(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "source.md", "see [[target]] and [[nowhere]]\n")
	b5Note(t, vault, "target.md", "# Target\n")
	b5Note(t, vault, "other.md", "points at [[source]]\n")

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	got, res := b5Graph(t, home, "jim", ws, map[string]any{
		"operation": GraphOpLinks, "path": "source.md",
	})
	require.False(t, res.IsError, res.ForLLM)

	byTarget := map[string]graphLink{}
	for _, l := range got.Links {
		byTarget[l.Form] = l
		assert.Equal(t, "source.md", l.From,
			"`links` reports OUTBOUND edges of the named note; %q is not it", l.From)
	}
	require.Len(t, got.Links, 2, "both outbound links must be reported, resolved or not: %+v", got.Links)
	assert.Equal(t, "target.md", byTarget["[[target]]"].To)
	assert.Equal(t, string(ResolveResolved), byTarget["[[target]]"].State)
	assert.Equal(t, string(ResolveUnresolved), byTarget["[[nowhere]]"].State,
		"FR-042: a link with no target is reported, never dropped")
	assert.Equal(t, len(got.Links), got.Count)

	// The distinguishing control: `links` is not `backlinks`. other.md points
	// AT source.md and must not appear in source.md's outbound list.
	for _, l := range got.Links {
		assert.NotEqual(t, "other.md", l.From, "`links` returned an inbound edge")
	}
	back, resB := b5Graph(t, home, "jim", ws, map[string]any{
		"operation": GraphOpBacklinks, "path": "source.md",
	})
	require.False(t, resB.IsError, resB.ForLLM)
	require.Len(t, back.Links, 1)
	assert.Equal(t, "other.md", back.Links[0].From,
		"the two operations must answer different questions, or `links` is untested by construction")
}

// TestGraphTool_MalformedRequestsAreRefusedWithAReason covers the two required
// arguments. A tool that answers a question it was not asked is worse than one
// that refuses: the model receives a confident answer about the wrong note.
func TestGraphTool_MalformedRequestsAreRefusedWithAReason(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "a.md", "# A\n")
	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	_, missingOp := b5Graph(t, home, "jim", ws, map[string]any{"path": "a.md"})
	assert.True(t, missingOp.IsError, "an absent operation is a caller error")
	assert.Contains(t, missingOp.ForLLM, "'operation' is required")

	_, blankOp := b5Graph(t, home, "jim", ws, map[string]any{"operation": "   ", "path": "a.md"})
	assert.True(t, blankOp.IsError, "a whitespace-only operation is the same caller error")
	assert.Contains(t, blankOp.ForLLM, "'operation' is required")

	for _, op := range []string{GraphOpLinks, GraphOpBacklinks, GraphOpNeighborhood} {
		_, res := b5Graph(t, home, "jim", ws, map[string]any{"operation": op})
		assert.True(t, res.IsError, "%s requires a path", op)
		assert.Contains(t, res.ForLLM, "'path' is required")
	}

	// Positive control: the operations that need no path are answered.
	for _, op := range []string{GraphOpUnresolved, GraphOpOrphans} {
		_, res := b5Graph(t, home, "jim", ws, map[string]any{"operation": op})
		assert.False(t, res.IsError, "%s must not require a path: %s", op, res.ForLLM)
	}
}

// TestGraphTool_RateLimitRefusesRatherThanServing is FR-055 for the SECOND
// retrieval tool. The limiter was only ever proven wired into knowledge_search;
// a tool that holds a limiter and never consults it is exactly the shape of a
// control that reports "implemented" and does nothing.
func TestGraphTool_RateLimitRefusesRatherThanServing(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "a.md", "# A\n")
	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	tool := &GraphTool{deps: ToolDeps{
		Home:        home,
		RateLimiter: NewRetrievalRateLimiter(RetrievalRateLimitConfig{PerAgentLimit: 1, Window: time.Hour}),
	}}
	ctx := b5Ctx("mia", ws)
	args := map[string]any{"operation": GraphOpOrphans}

	first := tool.Execute(ctx, args)
	require.False(t, first.IsError, first.ForLLM)

	second := tool.Execute(ctx, args)
	assert.True(t, second.IsError, "the second call must be refused by the rate limiter (FR-055)")
	assert.Contains(t, strings.ToLower(second.ForLLM), "rate limited")

	// Per-agent, not global: a different agent still gets its own budget.
	other := tool.Execute(b5Ctx("jim", ws), args)
	assert.False(t, other.IsError, "one agent exhausting its budget must not silence another")
}

// TestGraphTool_NodeBoundIsReportedNotSilentlyApplied is the half of FR-054 the
// existing bound test never reached: HopsClamped was asserted, NodesClamped was
// not. A neighbourhood cut off at the node bound is a SUBSET, so the answer must
// say so — US-8 AS-3's principle, applied to the other bound.
func TestGraphTool_NodeBoundIsReportedNotSilentlyApplied(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "a.md", "[[b]]\n")
	b5Note(t, vault, "b.md", "[[c]]\n")
	b5Note(t, vault, "c.md", "[[d]]\n")
	b5Note(t, vault, "d.md", "# D\n")
	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	// --- positive control: unbounded, the whole chain comes back ---
	full, resF := b5Graph(t, home, "jim", ws, map[string]any{
		"operation": GraphOpNeighborhood, "path": "a.md", "hops": 3,
	})
	require.False(t, resF.IsError, resF.ForLLM)
	require.Len(t, full.Nodes, 4, "the fixture must have more nodes than the bound below")
	require.False(t, full.NodesClamped)
	require.False(t, full.Incomplete)

	got, res := b5Graph(t, home, "jim", ws, map[string]any{
		"operation": GraphOpNeighborhood, "path": "a.md", "hops": 3, "max_nodes": 2,
	})
	require.False(t, res.IsError, res.ForLLM)
	assert.Len(t, got.Nodes, 2, "the node bound must be honoured")
	assert.True(t, got.NodesClamped, "FR-054: the clamping is REPORTED, not silently applied")
	assert.True(t, got.Incomplete,
		"a neighbourhood cut off at its node bound is a subset and the answer must say so")
	assert.Contains(t, strings.Join(got.Notes, " | "), "subset",
		"the note must tell the model the answer is partial; notes were %v", got.Notes)
}

// TestGraphTool_SkippedEntriesMakeTheAnswerIncomplete is NB-9 / FR-112 at the
// tool boundary: a graph built over a collection the walk could not fully read
// must never read as complete.
func TestGraphTool_SkippedEntriesMakeTheAnswerIncomplete(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "a.md", "# A\n")
	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	// --- positive control: a clean collection reports complete ---
	clean, resC := b5Graph(t, home, "jim", ws, map[string]any{"operation": GraphOpOrphans})
	require.False(t, resC.IsError, resC.ForLLM)
	require.Empty(t, clean.Skipped)
	require.False(t, clean.Incomplete)

	outside := b5Real(t, t.TempDir())
	require.NoError(t, os.WriteFile(filepath.Join(outside, "target.md"), []byte("# out\n"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(outside, "target.md"), filepath.Join(vault, "escape.md")))

	got, res := b5Graph(t, home, "jim", ws, map[string]any{"operation": GraphOpOrphans})
	require.False(t, res.IsError, res.ForLLM)
	require.NotEmpty(t, got.Skipped, "US-10 AS-3: a skipped symlink is REPORTED, never merely skipped")
	assert.Contains(t, strings.Join(got.Skipped, " | "), "escape.md")
	assert.Contains(t, strings.Join(got.Skipped, " | "), string(SkipSymlink))
	assert.True(t, got.Incomplete,
		"an answer built over a partly-unwalkable collection is not complete (NB-9)")
	assert.NotContains(t, res.ForLLM, outside,
		"nothing outside the collection may appear in the answer")
}

// b6ScanUnused keeps the context import honest if the helpers above change.
var _ = context.Background

// TestRetrieval_TruncatedCollectionEnumerationIsReported is FR-035 applied to
// the SCOPE layer rather than the index: when the walk that discovers which
// knowledge bases a workspace can see hits its directory budget, the answer is
// built over a possibly-incomplete list of collections and must say so.
//
// Both tools carry the same branch and neither was ever executed by a test. The
// failure it guards is quiet by construction: the response looks exactly like a
// normal one, and the collection that was never enumerated is simply absent.
func TestRetrieval_TruncatedCollectionEnumerationIsReported(t *testing.T) {
	home := b5Home(t)
	mount := b5Real(t, t.TempDir())

	// The collection must be enumerated BEFORE the budget runs out. The walk
	// is a LIFO stack over os.ReadDir's sorted entries, so the LAST name is
	// visited first.
	vault := b5Vault(t, filepath.Join(mount, "zzzz-vault"), "Vault")
	b5Note(t, vault, "note.md", "# Note\n\nfindable content\n")
	b5Index(t, home, vault)

	// Enough ordinary directories to exhaust ScopeMaxDirs.
	for i := range ScopeMaxDirs + 8 {
		require.NoError(t, os.MkdirAll(filepath.Join(mount, fmt.Sprintf("pad-%05d", i)), 0o755))
	}

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", mount)

	require.True(t, ResolveScope(home, ws).Truncated(),
		"the fixture must actually exhaust the %d-directory budget, or the assertions below "+
			"are about a branch that was never taken", ScopeMaxDirs)

	searched, sRes := b5Search(t, home, "mia", ws, map[string]any{"query": "findable"})
	require.False(t, sRes.IsError, sRes.ForLLM)
	assert.True(t, searched.Incomplete,
		"knowledge_search answered over a truncated collection list and did not say so (FR-035)")
	assert.Contains(t, strings.Join(searched.Notes, " | "), "enumeration",
		"the caller must be told WHY the answer is incomplete; notes were %v", searched.Notes)

	graphed, gRes := b5Graph(t, home, "jim", ws, map[string]any{"operation": GraphOpOrphans})
	require.False(t, gRes.IsError, gRes.ForLLM)
	assert.True(t, graphed.Incomplete,
		"knowledge_graph carries the same branch and the same obligation")
	assert.Contains(t, strings.Join(graphed.Notes, " | "), "enumeration")

	// Positive control: an ordinary workspace, well inside the budget, is not
	// reported incomplete — otherwise "always incomplete" passes the above.
	home2 := b5Home(t)
	small := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Small")
	b5Note(t, small, "note.md", "# Note\n\nfindable content\n")
	b5Index(t, home2, small)
	ws2 := b5Workspace(t, home2)
	b5Mount(t, home2, ws2, "notes", small)

	ok1, r1 := b5Search(t, home2, "mia", ws2, map[string]any{"query": "findable"})
	require.False(t, r1.IsError, r1.ForLLM)
	assert.False(t, ok1.Incomplete, "a fully enumerated workspace must not be reported incomplete")
	ok2, r2 := b5Graph(t, home2, "jim", ws2, map[string]any{"operation": GraphOpOrphans})
	require.False(t, r2.IsError, r2.ForLLM)
	assert.False(t, ok2.Incomplete)
}
