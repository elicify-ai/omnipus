// Omnipus — ADR-067 D7/D17: the agent-facing knowledge retrieval tools.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
package knowledge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// b5Index builds the index for a collection, exactly as the indexing lifecycle
// would, so a search in the same test is answering from a real scorch index and
// not from a stub.
func b5Index(t *testing.T, home, root string) {
	t.Helper()
	ix, err := OpenIndex(home, root)
	require.NoError(t, err)
	defer func() { require.NoError(t, ix.Close()) }()
	_, err = ix.Sync(context.Background())
	require.NoError(t, err)
}

// b5Ctx is the tool context the agent loop installs for one turn.
func b5Ctx(agentID, workspaceID string) context.Context {
	return tools.WithWorkspaceID(tools.WithAgentID(context.Background(), agentID), workspaceID)
}

// b5Search runs knowledge_search and decodes its payload.
func b5Search(t *testing.T, home, agentID, wsID string, args map[string]any) (searchResponse, *tools.ToolResult) {
	t.Helper()
	tool := &SearchTool{deps: ToolDeps{Home: home, RateLimiter: NewRetrievalRateLimiter(RetrievalRateLimitConfig{})}}
	res := tool.Execute(b5Ctx(agentID, wsID), args)
	require.NotNil(t, res)
	var out searchResponse
	if !res.IsError {
		require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out), "payload was: %s", res.ForLLM)
	}
	return out, res
}

// b5Graph runs knowledge_graph and decodes its payload.
func b5Graph(t *testing.T, home, agentID, wsID string, args map[string]any) (graphResponse, *tools.ToolResult) {
	t.Helper()
	tool := &GraphTool{deps: ToolDeps{Home: home, RateLimiter: NewRetrievalRateLimiter(RetrievalRateLimitConfig{})}}
	res := tool.Execute(b5Ctx(agentID, wsID), args)
	require.NotNil(t, res)
	var out graphResponse
	if !res.IsError {
		require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out), "payload was: %s", res.ForLLM)
	}
	return out, res
}

// ---------------------------------------------------------------------------
// US-9 (P0) — the cross-workspace negative test, at the TOOL boundary
// ---------------------------------------------------------------------------

// TestSearchScope_CrossWorkspaceReturnsEmpty is spec test 26 and ADR-067
// AC-7.1, run through the real tool: an agent in workspace A searches for a
// phrase that exists ONLY in a knowledge base mounted into workspace B, and
// receives zero results.
//
// Three properties, each of which has its own way of being got wrong:
//
//   - ZERO RESULTS, not an error. MV-12 is explicit that a 403-shaped refusal
//     is itself the disclosure: it confirms the collection exists.
//   - NO DISCLOSURE. The response must not name workspace B's collection, even
//     in a "did you mean" hint.
//   - THE POSITIVE CONTROL. The same query, issued from workspace B, must find
//     the note. Without it every assertion above is satisfied by a search that
//     is simply broken — an unbuilt index, a mis-derived home, a query that
//     matches nothing anywhere. That failure mode is not hypothetical here:
//     this tool returns an empty result set for a never-indexed collection too.
func TestSearchScope_CrossWorkspaceReturnsEmpty(t *testing.T) {
	const phrase = "zarquon-seven"

	home := b5Home(t)
	vaultB := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault-b"), "Vault B")
	b5Note(t, vaultB, "secret.md", "# Secret\n\nThe passphrase is "+phrase+" and nothing else.\n")
	b5Index(t, home, vaultB)

	wsA := b5Workspace(t, home)
	wsB := b5Workspace(t, home)
	b5Mount(t, home, wsB, "notes", vaultB)

	// --- positive control ---
	fromB, resB := b5Search(t, home, "ava", wsB, map[string]any{"query": phrase})
	require.False(t, resB.IsError, "control search failed: %s", resB.ForLLM)
	require.Len(t, fromB.Results, 1,
		"the agent in workspace B must find the note — without this, the zero-result "+
			"assertion below proves nothing about isolation")
	require.Equal(t, "secret.md", fromB.Results[0].Path)

	// --- the requirement ---
	fromA, resA := b5Search(t, home, "ava", wsA, map[string]any{"query": phrase})
	assert.False(t, resA.IsError,
		"an out-of-scope collection must produce an EMPTY RESULT SET, not a permission error (MV-12)")
	assert.Empty(t, fromA.Results,
		"an agent in workspace A must get zero hits from a knowledge base mounted only into workspace B (US-9 AS-1)")
	assert.Empty(t, fromA.CollectionsInScope,
		"the response must not name another workspace's collection")
	assert.NotContains(t, resA.ForLLM, "Vault B",
		"nothing in the payload may disclose the existence of workspace B's collection")
	assert.NotContains(t, resA.ForLLM, "secret.md",
		"no path from workspace B's collection may appear in workspace A's answer. (The QUERY is "+
			"echoed back and is the caller's own input, so it is not a disclosure — the paths, "+
			"names and excerpts are.)")
}

// TestGraphScope_CrossWorkspaceNotAddressable is US-9 AS-2: asking for the
// backlinks of a note in another workspace's knowledge base must not be a
// different experience from asking about a note that does not exist.
func TestGraphScope_CrossWorkspaceNotAddressable(t *testing.T) {
	home := b5Home(t)
	vaultB := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault-b"), "Vault B")
	b5Note(t, vaultB, "Target.md", "# Target\n")
	b5Note(t, vaultB, "Source.md", "See [[Target]].\n")

	wsA := b5Workspace(t, home)
	wsB := b5Workspace(t, home)
	b5Mount(t, home, wsB, "notes", vaultB)

	// Control: workspace B can.
	fromB, resB := b5Graph(t, home, "jim", wsB, map[string]any{
		"operation": GraphOpBacklinks, "path": "Target.md",
	})
	require.False(t, resB.IsError, resB.ForLLM)
	require.Len(t, fromB.Links, 1, "workspace B must see the backlink — otherwise the negative half is vacuous")

	fromA, resA := b5Graph(t, home, "jim", wsA, map[string]any{
		"operation": GraphOpBacklinks, "path": "Target.md", "collection": "Vault B",
	})
	assert.False(t, resA.IsError, "out-of-scope must be empty, not an error (FR-053)")
	assert.Empty(t, fromA.Links, "workspace B's knowledge base must not be addressable from workspace A")
	assert.Empty(t, fromA.CollectionsInScope)
	assert.NotContains(t, resA.ForLLM, "Source.md",
		"no path from workspace B's collection may appear in workspace A's answer")
}

// ---------------------------------------------------------------------------
// FR-050 — ranked results carrying path, title and matched excerpt
// ---------------------------------------------------------------------------

// TestSearch_ReturnsPathTitleAndExcerpt is US-8 AS-1. Each of the three fields
// is asserted for its own reason: the path addresses the note, the title is
// what a reader recognises it by, and the excerpt is the evidence that the hit
// is relevant rather than merely ranked.
func TestSearch_ReturnsPathTitleAndExcerpt(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "notes/heading-titled.md",
		"# The Roadmap Note\n\nSome preamble.\n\nThe migration deadline is quarter three.\n")
	b5Note(t, vault, "notes/frontmatter-titled.md",
		"---\ntitle: Frontmatter Wins\n---\n\n# Ignored Heading\n\nAnother migration mention.\n")
	b5Index(t, home, vault)

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	got, res := b5Search(t, home, "ray", ws, map[string]any{"query": "migration"})
	require.False(t, res.IsError, res.ForLLM)
	require.Len(t, got.Results, 2)

	byPath := map[string]searchHit{}
	for _, h := range got.Results {
		byPath[h.Path] = h
	}

	heading, ok := byPath["notes/heading-titled.md"]
	require.True(t, ok, "got %v", got.Results)
	assert.Equal(t, "The Roadmap Note", heading.Title,
		"a note with no frontmatter title takes its first level-1 heading")
	assert.Contains(t, strings.ToLower(heading.Excerpt), "migration",
		"the excerpt must contain the matched term — that is what makes it evidence")
	assert.Empty(t, heading.ExcerptUnavailable)

	front, ok := byPath["notes/frontmatter-titled.md"]
	require.True(t, ok)
	assert.Equal(t, "Frontmatter Wins", front.Title,
		"the frontmatter's own title outranks a heading below it")

	for _, h := range got.Results {
		assert.LessOrEqual(t, len(h.Excerpt), ExcerptMaxBytes,
			"MV-8 caps an excerpt at %d bytes", ExcerptMaxBytes)
	}
	assert.True(t, got.Report.Complete, "a fully indexed collection reports a complete answer")
	assert.Empty(t, got.Report.Statement, "US-6 AS-4: a finished index shows no incompleteness notice")
}

// TestSearch_TitleFallsBackToFilename pins the floor FR-050a(a) depends on: a
// hit must carry a title even when the file cannot be read at all, so the title
// derivation is never allowed to fail.
func TestSearch_TitleFallsBackToFilename(t *testing.T) {
	assert.Equal(t, "no-such-note", titleFor("/nonexistent/no-such-note.md", "deep/no-such-note.md"),
		"an unreadable file still yields a title — the filename stem")
}

// ---------------------------------------------------------------------------
// FR-050a — the excerpt is re-read at query time
// ---------------------------------------------------------------------------

// TestExcerpt_ReReadAtQueryTimeNotStored is spec test 69.
//
// The note is indexed, then EDITED ON DISK without re-indexing. The excerpt
// must reflect the current bytes, because it is read at query time and not
// stored. An implementation that cached the excerpt at index time passes every
// "an excerpt was returned" assertion and fails only this one.
func TestExcerpt_ReReadAtQueryTimeNotStored(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "note.md", "# Note\n\nThe widget is coloured BEFOREVALUE today.\n")
	b5Index(t, home, vault)

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	before, res := b5Search(t, home, "mia", ws, map[string]any{"query": "widget"})
	require.False(t, res.IsError, res.ForLLM)
	require.Len(t, before.Results, 1)
	require.Contains(t, before.Results[0].Excerpt, "BEFOREVALUE")

	// Edit on disk. No re-index.
	b5Note(t, vault, "note.md", "# Note\n\nThe widget is coloured AFTERVALUE today.\n")

	after, res := b5Search(t, home, "mia", ws, map[string]any{"query": "widget"})
	require.False(t, res.IsError, res.ForLLM)
	require.Len(t, after.Results, 1)
	assert.Contains(t, after.Results[0].Excerpt, "AFTERVALUE",
		"the excerpt is re-read from disk at query time (FR-050a), so it must match current bytes")
	assert.NotContains(t, after.Results[0].Excerpt, "BEFOREVALUE",
		"an excerpt cached at index time is exactly the defect AW-1 rejects")
}

// TestExcerpt_UnavailableIsReportedNotFabricated is spec test 69a.
//
// Deleted, unreadable-by-budget and term-removed. Each returns a hit WITH a
// machine-readable reason. "No panic" would pass all three; so would returning
// "" for each, which is why the reasons are asserted individually and are
// distinct values.
func TestExcerpt_UnavailableIsReportedNotFabricated(t *testing.T) {
	dir := b5Real(t, t.TempDir())
	present := filepath.Join(dir, "present.md")
	require.NoError(t, os.WriteFile(present, []byte("the term appears right here\n"), 0o600))
	terms := []string{"term"}
	future := time.Now().Add(time.Minute)

	t.Run("available", func(t *testing.T) {
		text, reason := excerptAt(present, 0, terms, future)
		assert.Equal(t, ExcerptOK, reason)
		assert.Contains(t, text, "term")
	})

	t.Run("deleted file", func(t *testing.T) {
		text, reason := excerptAt(filepath.Join(dir, "gone.md"), 0, terms, future)
		assert.Equal(t, ExcerptFileMissing, reason,
			"a file indexed and then deleted must be reported as missing, not returned as an empty excerpt")
		assert.Empty(t, text)
	})

	t.Run("term removed since indexing", func(t *testing.T) {
		text, reason := excerptAt(present, 0, []string{"vanished"}, future)
		assert.Equal(t, ExcerptMatchNotFound, reason,
			"a term no longer present must be reported, never invented from surrounding bytes")
		assert.Empty(t, text)
	})

	t.Run("read budget exhausted", func(t *testing.T) {
		text, reason := excerptAt(present, 0, terms, time.Now().Add(-time.Second))
		assert.Equal(t, ExcerptBudgetExhausted, reason,
			"FR-050a(b): the re-reads are budgeted, and running out is reported")
		assert.Empty(t, text)
	})

	// The four reasons must be four DIFFERENT values: collapsing them into one
	// makes the field unusable for the caller it exists for.
	seen := map[ExcerptReason]bool{}
	for _, r := range []ExcerptReason{
		ExcerptFileMissing, ExcerptFileUnreadable, ExcerptMatchNotFound,
		ExcerptBudgetExhausted, ExcerptAttachment,
	} {
		assert.Falsef(t, seen[r], "reason %q is duplicated", r)
		seen[r] = true
		assert.NotEmpty(t, string(r), "every unavailability reason must be machine-readable")
	}
}

// TestExcerpt_OffsetIsAbsoluteWithinTheFile pins FR-050a(c). The hit's offset
// is the start of the best-scoring SEGMENT, absolute within the file, so a
// re-read that treated it as segment-relative would land in the wrong place.
func TestExcerpt_OffsetIsAbsoluteWithinTheFile(t *testing.T) {
	dir := b5Real(t, t.TempDir())
	path := filepath.Join(dir, "big.md")
	head := strings.Repeat("filler line about nothing at all\n", 400)
	require.NoError(t, os.WriteFile(path, []byte(head+"the NEEDLE is here\n"), 0o600))

	// Reading from offset zero finds it; reading from an offset PAST it must
	// not, which is what proves the offset is honoured rather than ignored.
	text, reason := excerptAt(path, 0, []string{"needle"}, time.Now().Add(time.Minute))
	require.Equal(t, ExcerptOK, reason)
	assert.Contains(t, strings.ToLower(text), "needle")

	_, reason = excerptAt(path, int64(len(head)+len("the NEEDLE is here\n")), []string{"needle"}, time.Now().Add(time.Minute))
	assert.Equal(t, ExcerptMatchNotFound, reason,
		"the excerpt read must start AT the absolute offset it was given")
}

// ---------------------------------------------------------------------------
// FR-037 / MV-6 / MV-7 — clamping is reported, never silent
// ---------------------------------------------------------------------------

// TestSearchResultCap_ClampedAndReported is spec test 28 / US-8 AS-3: a request
// above the cap is served at the cap AND the response says so. Serving 100
// silently is the failure — the caller reads a truncated answer as a complete
// one.
func TestSearchTool_ResultCapClampedAndReported(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "one.md", "# One\n\nshared term here\n")
	b5Index(t, home, vault)

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	got, res := b5Search(t, home, "mia", ws, map[string]any{"query": "shared", "top_n": 400})
	require.False(t, res.IsError, res.ForLLM)
	assert.True(t, got.Report.Clamped, "a request above the cap must be reported as clamped (FR-037)")
	assert.Equal(t, 400, got.Report.RequestedTopN)
	assert.Equal(t, SearchMaxTopN, got.Report.AppliedTopN)
	assert.NotEmpty(t, got.Report.Statement, "the clamping must be stated, not merely flagged")

	// The default and the in-range case are not clamps.
	def, _ := b5Search(t, home, "mia", ws, map[string]any{"query": "shared"})
	assert.False(t, def.Report.Clamped, "the default count is not a clamp")
	assert.Equal(t, SearchDefaultTopN, def.Report.AppliedTopN, "MV-7: the default result count is 20")
}

// ---------------------------------------------------------------------------
// US-6 — never a confidently incomplete answer
// ---------------------------------------------------------------------------

// TestSearch_NeverIndexedIsReportedNotZeroResults: a collection that has not
// been indexed yet returns no results, and saying only "no results" would be a
// confidently wrong answer. The state must be named.
func TestSearch_NeverIndexedIsReportedNotZeroResults(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "note.md", "# Note\n\nsomething findable\n")
	// Deliberately NOT indexed.

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	got, res := b5Search(t, home, "mia", ws, map[string]any{"query": "findable"})
	require.False(t, res.IsError, res.ForLLM)
	assert.Empty(t, got.Results)
	assert.Equal(t, "not_built", got.IndexState,
		"a never-indexed collection must say so; 'no results' is a different statement")
	assert.True(t, got.Incomplete)
	assert.NotEmpty(t, got.Notes)
}

// ---------------------------------------------------------------------------
// FR-051 / AC-7.2 — the graph surface
// ---------------------------------------------------------------------------

// TestGraph_BacklinksCoverAllFourLinkForms is ADR-067 AC-7.2 and US-8 AS-2.
// Four notes link to one target using the four different wikilink forms; all
// four must come back. A resolver that handles only the bare form passes any
// "backlinks work" test written with one link in it.
func TestGraph_BacklinksCoverAllFourLinkForms(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "folder/Target.md", "# Target\n\n## Heading\n\nbody\n")
	b5Note(t, vault, "a.md", "plain [[Target]]\n")
	b5Note(t, vault, "b.md", "aliased [[Target|the target]]\n")
	b5Note(t, vault, "c.md", "heading [[Target#Heading]]\n")
	b5Note(t, vault, "d.md", "path [[folder/Target]]\n")

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	got, res := b5Graph(t, home, "jim", ws, map[string]any{
		"operation": GraphOpBacklinks, "path": "folder/Target.md",
	})
	require.False(t, res.IsError, res.ForLLM)

	from := make([]string, 0, len(got.Links))
	for _, l := range got.Links {
		from = append(from, l.From)
	}
	assert.ElementsMatch(t, []string{"a.md", "b.md", "c.md", "d.md"}, from,
		"every wikilink form must produce a backlink (AC-7.2)")
	assert.Equal(t, len(got.Links), got.Count)
}

// TestGraph_UnresolvedAndOrphans covers US-8 AS-5 and the orphan query.
func TestGraph_UnresolvedAndOrphans(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "linked.md", "# Linked\n")
	b5Note(t, vault, "source.md", "[[linked]] and [[NoSuchNote]]\n")
	b5Note(t, vault, "lonely.md", "# Lonely\n")

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	unresolved, res := b5Graph(t, home, "jim", ws, map[string]any{"operation": GraphOpUnresolved})
	require.False(t, res.IsError, res.ForLLM)
	require.Len(t, unresolved.Links, 1)
	assert.Equal(t, "source.md", unresolved.Links[0].From)
	assert.Equal(t, string(ResolveUnresolved), unresolved.Links[0].State)
	assert.Equal(t, string(ReasonNoMatch), unresolved.Links[0].Reason,
		"an unresolved link must say WHY — a broken link and an escape attempt are not the same finding")

	orphans, res := b5Graph(t, home, "jim", ws, map[string]any{"operation": GraphOpOrphans})
	require.False(t, res.IsError, res.ForLLM)
	assert.ElementsMatch(t, []string{"lonely.md", "source.md"}, orphans.Nodes,
		"an orphan is a note nothing links to")
}

// TestGraph_NeighborhoodBoundsAreClampedAndReported is FR-054 / US-8 AS-4.
func TestGraph_NeighborhoodBoundsAreClampedAndReported(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "a.md", "[[b]]\n")
	b5Note(t, vault, "b.md", "[[c]]\n")
	b5Note(t, vault, "c.md", "# C\n")

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	got, res := b5Graph(t, home, "jim", ws, map[string]any{
		"operation": GraphOpNeighborhood, "path": "a.md", "hops": 9,
	})
	require.False(t, res.IsError, res.ForLLM)
	assert.Equal(t, MaxNeighborhoodHops, got.Hops, "FR-054 bounds a neighbourhood at %d hops", MaxNeighborhoodHops)
	assert.True(t, got.HopsClamped, "the clamping must be reported, not silently applied")
	assert.LessOrEqual(t, len(got.Nodes), MaxNeighborhoodNodes)
	assert.NotEmpty(t, got.Notes)
}

// TestGraph_RejectsUnknownOperation: an unrecognised operation is a caller
// error and must say so rather than quietly answering a different question.
func TestGraph_RejectsUnknownOperation(t *testing.T) {
	home := b5Home(t)
	ws := b5Workspace(t, home)
	_, res := b5Graph(t, home, "jim", ws, map[string]any{"operation": "delete_everything"})
	assert.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "unknown operation")
}

// ---------------------------------------------------------------------------
// FR-055 — retrieval is rate-limited
// ---------------------------------------------------------------------------

// TestRetrievalRateLimiter_BoundsPerAgent asserts the limiter admits exactly
// the configured number of calls per window, per agent, and recovers when the
// window rolls.
func TestRetrievalRateLimiter_BoundsPerAgent(t *testing.T) {
	now := time.Now()
	l := NewRetrievalRateLimiter(RetrievalRateLimitConfig{
		PerAgentLimit: 2,
		Window:        time.Minute,
		nowFn:         func() time.Time { return now },
	})

	assert.True(t, l.Allow("mia").Allowed)
	assert.True(t, l.Allow("mia").Allowed)

	denied := l.Allow("mia")
	assert.False(t, denied.Allowed, "the third call inside the window must be refused")
	assert.Positive(t, denied.RetryAfter, "a refusal must say when to retry")

	assert.True(t, l.Allow("jim").Allowed,
		"the bucket is per agent: one agent exhausting its budget must not silence another")

	now = now.Add(time.Minute + time.Second)
	assert.True(t, l.Allow("mia").Allowed, "the window slides")
}

// TestSearch_RateLimitRefusesRatherThanServing proves the limiter is actually
// wired into the tool. A limiter that exists but is never consulted is the
// exact shape of a control that reports "implemented" and does nothing.
func TestSearch_RateLimitRefusesRatherThanServing(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "note.md", "# Note\n\nfindable content\n")
	b5Index(t, home, vault)
	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	tool := &SearchTool{deps: ToolDeps{
		Home:        home,
		RateLimiter: NewRetrievalRateLimiter(RetrievalRateLimitConfig{PerAgentLimit: 1, Window: time.Hour}),
	}}
	ctx := b5Ctx("mia", ws)

	first := tool.Execute(ctx, map[string]any{"query": "findable"})
	require.False(t, first.IsError, first.ForLLM)

	second := tool.Execute(ctx, map[string]any{"query": "findable"})
	assert.True(t, second.IsError, "the second call must be refused by the rate limiter (FR-055)")
	assert.Contains(t, strings.ToLower(second.ForLLM), "rate limited")
}

// TestRetrievalTools_AlwaysCarryALimiter: FR-055 is a requirement, so the
// constructor must never hand out a tool whose limiter is nil. A
// nil-means-bypass convention turns a wiring omission into a silently disabled
// control.
func TestRetrievalTools_AlwaysCarryALimiter(t *testing.T) {
	built := RetrievalTools(ToolDeps{Home: "/tmp/nowhere"})
	require.Len(t, built, 2)
	for _, tool := range built {
		switch v := tool.(type) {
		case *SearchTool:
			assert.NotNil(t, v.deps.RateLimiter, "knowledge_search must never be constructed without a limiter")
		case *GraphTool:
			assert.NotNil(t, v.deps.RateLimiter, "knowledge_graph must never be constructed without a limiter")
		default:
			t.Fatalf("unexpected tool type %T", tool)
		}
	}
}

// ---------------------------------------------------------------------------
// The tool surface itself
// ---------------------------------------------------------------------------

// TestRetrievalTools_NamesMatchTheADR pins the two retrieval names D7 states.
// They are also the two names the D17 seed and pkg/gateway's coverage universe
// carry, and a rename in one place only ships the tool denied.
func TestRetrievalTools_NamesMatchTheADR(t *testing.T) {
	assert.Equal(t, []string{"knowledge_graph", "knowledge_search"}, RetrievalToolNames(),
		"ADR-067 D7 names the retrieval pair knowledge_search and knowledge_graph")
	for _, tool := range RetrievalTools(ToolDeps{}) {
		assert.NotEmpty(t, tool.Description(), "%s needs a description the model can act on", tool.Name())
		params := tool.Parameters()
		assert.Equal(t, "object", params["type"])
		assert.NotEmpty(t, params["required"], "%s must declare its required arguments", tool.Name())
	}
}

// ---------------------------------------------------------------------------
// Argument handling
// ---------------------------------------------------------------------------

// TestSearch_FolderScopingStaysInsideTheCollection: D7 offers folder scoping,
// and the folder argument is model-controlled, so a traversal must reduce to
// "the whole collection" rather than to a path outside it.
func TestSearch_FolderScopingStaysInsideTheCollection(t *testing.T) {
	assert.Equal(t, "projects/2026", normalizeFolder("projects/2026/"))
	assert.Equal(t, "", normalizeFolder("../../etc"))
	assert.Equal(t, "", normalizeFolder(".."))
	assert.Equal(t, "", normalizeFolder(""))
	assert.Equal(t, "", normalizeFolder(nil))
}

// TestSearch_FolderScopingFiltersResults proves the argument does something.
func TestSearch_FolderScopingFiltersResults(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "keep/in.md", "# In\n\ncommonword here\n")
	b5Note(t, vault, "drop/out.md", "# Out\n\ncommonword here too\n")
	b5Index(t, home, vault)

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	all, _ := b5Search(t, home, "mia", ws, map[string]any{"query": "commonword"})
	require.Len(t, all.Results, 2)

	scoped, res := b5Search(t, home, "mia", ws, map[string]any{"query": "commonword", "folder": "keep"})
	require.False(t, res.IsError, res.ForLLM)
	require.Len(t, scoped.Results, 1)
	assert.Equal(t, "keep/in.md", scoped.Results[0].Path)
}

// TestSearch_RequiresAQuery: an empty query is a caller error, not a
// match-everything.
func TestSearch_RequiresAQuery(t *testing.T) {
	home := b5Home(t)
	ws := b5Workspace(t, home)
	_, res := b5Search(t, home, "mia", ws, map[string]any{"query": "   "})
	assert.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "required")
}

// b5UnusedGuard keeps the workspace import honest if the helpers above move.
var _ = workspace.SafeWorkDir
