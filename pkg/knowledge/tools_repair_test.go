// Omnipus — ADR-067 stage 2: the agent-facing retrieval boundary, audited.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
//
// WHY THIS FILE EXISTS SEPARATELY FROM tools_test.go
//
// Every test here guards a property that was stated in a comment in tools.go,
// traced in the spec's matrix, and asserted by nothing — each one survived a
// mutation that deleted the production code it was supposed to protect. They
// share one shape: the oracle is the RESPONSE THE MODEL RECEIVES, not an
// internal helper's return value. Three of the four defects they cover were
// invisible one layer down, because the helper being unit-tested was correct
// and the tool never called it.
package knowledge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// b6Search runs knowledge_search with caller-supplied deps (Home is filled in)
// and decodes the payload, returning the raw result too so a test can assert
// on the exact bytes the model would see.
func b6Search(t *testing.T, home, agentID, wsID string, deps ToolDeps, args map[string]any) (searchResponse, *tools.ToolResult) {
	t.Helper()
	deps.Home = home
	if deps.RateLimiter == nil {
		deps.RateLimiter = NewRetrievalRateLimiter(RetrievalRateLimitConfig{})
	}
	tool := &SearchTool{deps: deps}
	res := tool.Execute(b5Ctx(agentID, wsID), args)
	require.NotNil(t, res)
	var out searchResponse
	if !res.IsError {
		require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &out), "payload was: %s", res.ForLLM)
	}
	return out, res
}

// b6HitByPath finds one hit in a decoded response.
func b6HitByPath(t *testing.T, resp searchResponse, path string) searchHit {
	t.Helper()
	for _, h := range resp.Results {
		if h.Path == path {
			return h
		}
	}
	t.Fatalf("no hit for %q in %+v", path, resp.Results)
	return searchHit{}
}

// ---------------------------------------------------------------------------
// FR-043 / FR-044 / US-10 AS-3 — containment at the RETRIEVAL boundary
// ---------------------------------------------------------------------------

// TestSearchTool_SymlinkedHitIsRefusedNotFollowed is US-10 AS-3 applied where
// it was missing: the query path, not the walk.
//
// The walk refuses to index a symlink, and that was taken to mean the retrieval
// path could trust the paths it reads back. It cannot. A manifest entry is a
// RECORD OF THE PAST, and anything that can write into a mounted collection —
// an agent with a shell, a sync client, the operator — can replace an indexed
// note with a symlink afterwards. The retrieval path then joined the collection
// root to the recorded path, which is a LEXICAL containment check, and opened
// the result: one symlink, and the bytes of a file outside the collection were
// delivered to the model as a title and an excerpt.
//
// Four assertions, because the weaker subsets are all passable:
//
//   - the hit is still RETURNED (FR-050a(a) forbids dropping it silently);
//   - it carries the filename-derived title, never one read out of the target;
//   - the reason is machine-readable and the answer says it is incomplete;
//   - NOTHING outside the collection was opened, proven by the read seam —
//     "the excerpt looks wrong" would also be true of an implementation that
//     read the file and then discarded the text.
//
// The positive control is not optional: an ordinary note in the same response
// must come back with a real title and a real excerpt, or "refuse everything"
// passes every assertion above.
func TestSearchTool_SymlinkedHitIsRefusedNotFollowed(t *testing.T) {
	const term = "zarquon"
	const secret = "BEGIN RSA PRIVATE KEY outside-the-collection"

	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "notes/leak.md", "# Leak\n\nan ordinary note about "+term+"\n")
	b5Note(t, vault, "notes/honest.md", "# Honest\n\nanother note about "+term+"\n")
	b5Index(t, home, vault)

	// The file the link will point at, well outside the collection.
	outside := b5Real(t, t.TempDir())
	outsideFile := filepath.Join(outside, "id_rsa.md")
	require.NoError(t, os.WriteFile(outsideFile,
		[]byte("# PWNED-TITLE-FROM-OUTSIDE\n\n"+term+" "+secret+"\n"), 0o600))

	// Swap the indexed note for a symlink to it. The manifest still says
	// notes/leak.md is a note this collection owns.
	leak := filepath.Join(vault, "notes", "leak.md")
	require.NoError(t, os.Remove(leak))
	require.NoError(t, os.Symlink(outsideFile, leak))

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	opened, restore := b2CountingOpen(t)
	defer restore()

	got, res := b5Search(t, home, "mia", ws, map[string]any{"query": term})
	require.False(t, res.IsError, res.ForLLM)

	// --- positive control: the honest note is served completely ---
	honest := b6HitByPath(t, got, "notes/honest.md")
	require.Equal(t, "Honest", honest.Title,
		"the control note's title must come from its contents — otherwise the refusal below "+
			"is indistinguishable from a search that reads nothing at all")
	require.Contains(t, honest.Excerpt, term)

	// --- the requirement ---
	leaked := b6HitByPath(t, got, "notes/leak.md")
	assert.Equal(t, ExcerptNotContained, leaked.ExcerptUnavailable,
		"a path that reaches its target only through a symbolic link must be refused with a "+
			"machine-readable reason (FR-043/FR-044)")
	assert.Empty(t, leaked.Excerpt, "no excerpt may be produced from a file outside the collection")
	assert.Equal(t, "leak", leaked.Title,
		"the title must be derived from the FILENAME; %q is the heading inside the symlink's target",
		leaked.Title)
	assert.True(t, got.Incomplete,
		"an answer that could not read one of its own hits is not complete (FR-035)")

	assert.NotContains(t, res.ForLLM, "PWNED-TITLE-FROM-OUTSIDE",
		"a heading from outside the collection reached the model")
	assert.NotContains(t, res.ForLLM, secret,
		"bytes from outside the collection reached the model")

	for _, p := range *opened {
		assert.NotEqual(t, outsideFile, p,
			"the retrieval path opened %q, which is outside the collection root", p)
		assert.False(t, strings.HasPrefix(p, outside+string(filepath.Separator)),
			"the retrieval path opened %q, which is outside the collection root", p)
	}
}

// TestSearchTool_TraversalPathInTheManifestIsRefused is the other half of
// FR-043 at the query boundary: a recorded path that leaves the root lexically.
//
// It reaches the same refusal by a different route, which matters because the
// two are guarded by different lines: the symlink case is caught by the
// resolved-equals-lexical test, this one by ResolveContained's own refusal. An
// implementation that only handled symlinks would pass the test above.
func TestSearchTool_TraversalPathInTheManifestIsRefused(t *testing.T) {
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	root, err := NewCollectionRoot(OSLinkFS(), vault)
	require.NoError(t, err)

	outside := b5Real(t, t.TempDir())
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.md"), []byte("x"), 0o600))

	for _, rel := range []string{
		"../secret.md",
		"notes/../../secret.md",
		"/etc/passwd",
	} {
		_, rErr := retrievalPath(OSLinkFS(), root, rel)
		assert.Error(t, rErr, "retrievalPath(%q) must refuse — FR-043 admits no lexical escape", rel)
	}

	// Positive control: an ordinary contained path resolves, so the refusals
	// above are refusals and not a function that always errors.
	b5Note(t, vault, "notes/ok.md", "# OK\n")
	okPath, okErr := retrievalPath(OSLinkFS(), root, "notes/ok.md")
	require.NoError(t, okErr)
	assert.Equal(t, filepath.Join(vault, "notes", "ok.md"), okPath)

	// A path that does not exist yet is CONTAINED, not refused: "missing" is
	// the opener's answer to give (ExcerptFileMissing), and turning it into a
	// containment refusal would report the wrong reason.
	missing, missErr := retrievalPath(OSLinkFS(), root, "notes/never-written.md")
	require.NoError(t, missErr)
	assert.Equal(t, filepath.Join(vault, "notes", "never-written.md"), missing)
}

// ---------------------------------------------------------------------------
// FR-039a — an attachment's contents are never opened, at the TOOL boundary
// ---------------------------------------------------------------------------

// TestSearchTool_AttachmentContentsAreNeverOpened is spec test 70's oracle
// ("counted content reads are exactly 0") applied to the query path.
//
// The indexer's half was already tested. The query path's was not, and it was
// broken: the hit's title was derived by opening every hit and reading its
// first 8 KB BEFORE the "is this an attachment?" branch was reached. The
// response therefore said `excerpt_unavailable: attachment_not_read` in the
// same JSON object whose `title` had just been read out of the attachment.
//
// The read-counting seam is the oracle rather than the response text, for the
// reason FR-039a is written the way it is: "the indexer MUST NOT open an
// attachment's contents for any reason" is a statement about reads, and a test
// that only inspects the output passes against an implementation that reads the
// file and then happens not to print it.
func TestSearchTool_AttachmentContentsAreNeverOpened(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	// The attachment's bytes are shaped like a note on purpose: a title
	// deriver that opens it finds a level-1 heading and reports it.
	b5Note(t, vault, "img/diagram-v3.png", "# LEAKED-FROM-ATTACHMENT-CONTENTS\n\nzarquonsecret\n")
	b5Note(t, vault, "notes/diagram-v3 notes.md", "# Diagram notes\n\nabout diagram-v3\n")
	b5Index(t, home, vault)

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	opened, restore := b2CountingOpen(t)
	defer restore()

	got, res := b5Search(t, home, "mia", ws, map[string]any{"query": "diagram-v3"})
	require.False(t, res.IsError, res.ForLLM)

	att := b6HitByPath(t, got, "img/diagram-v3.png")
	assert.Equal(t, string(ScanKindAttachment), att.Kind)
	assert.Equal(t, ExcerptAttachment, att.ExcerptUnavailable)
	assert.Equal(t, "diagram-v3", att.Title,
		"an attachment's title is its filename stem and nothing else; %q was read out of its bytes",
		att.Title)
	assert.Empty(t, att.Excerpt)

	// --- positive control: the read seam IS live in this run ---
	note := b6HitByPath(t, got, "notes/diagram-v3 notes.md")
	require.Equal(t, "Diagram notes", note.Title,
		"the note's title must come from its contents — without a read actually happening in "+
			"this run, the zero-reads assertion below would be vacuous")
	require.NotEmpty(t, *opened,
		"the read seam recorded nothing at all; the assertion below cannot fail")

	attAbs := filepath.Join(vault, "img", "diagram-v3.png")
	for _, p := range *opened {
		assert.NotEqual(t, attAbs, p,
			"the query path opened attachment %q — FR-039a allows zero content reads, for any reason", p)
	}
	assert.NotContains(t, res.ForLLM, "LEAKED-FROM-ATTACHMENT-CONTENTS",
		"bytes from inside an attachment reached the model")
}

// ---------------------------------------------------------------------------
// FR-035 / FR-036 / US-6 (P0) — incompleteness, at the boundary an agent sees
// ---------------------------------------------------------------------------

// TestSearchTool_IncompleteIndexIsStatedInTheSameResponse is spec test 30 at
// the tool boundary (US-6 AS-1/AS-2/AS-3, FR-035, FR-036).
//
// The only assertion the suite previously made here was that a completed search
// reports Complete — which a fresh, entirely unwired ProgressTracker satisfies,
// because idle is not in-flight. Severing the tool from live index progress
// therefore left the whole package green. This drives the negative direction
// through the real tool, both ways it can be wired:
//
//   - via ToolDeps.Progress, the injection point a host uses;
//   - via SharedProgressTracker, the process-wide default whose doc comment
//     states the wiring obligation. If those two ever stop being the same
//     object for the same root, a search during a first index silently reports
//     a tenth of the corpus as the whole of it.
//
// AS-3 is the load-bearing one: the statement must arrive in the SAME payload
// as the results, not through a separate channel.
func TestSearchTool_IncompleteIndexIsStatedInTheSameResponse(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "a.md", "# A\n\nzarquon appears here\n")
	b5Note(t, vault, "b.md", "# B\n\nzarquon appears here too\n")
	b5Index(t, home, vault)

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	t.Run("known total is reported as a ratio", func(t *testing.T) {
		mid := NewProgressTracker()
		mid.BeginEnumeration(false)
		mid.BeginIndexing(1000)
		mid.SetIndexed(7)

		got, res := b6Search(t, home, "mia", ws,
			ToolDeps{Progress: func(string) *ProgressTracker { return mid }},
			map[string]any{"query": "zarquon"})
		require.False(t, res.IsError, res.ForLLM)

		require.NotEmpty(t, got.Results,
			"US-6 AS-3: partial RESULTS are returned — refusing to answer is a different behaviour")
		assert.False(t, got.Report.Complete,
			"a search issued while an index is 7/1000 done must not report completeness")
		assert.True(t, got.Incomplete)
		assert.Equal(t, 7, got.Report.Indexed)
		assert.Equal(t, 1000, got.Report.Total)
		assert.False(t, got.Report.Indeterminate,
			"the total is known, so this is a ratio and not the indeterminate form")
		require.NotEmpty(t, got.Report.Statement)
		assert.Contains(t, got.Notes, got.Report.Statement,
			"FR-035/US-6 AS-3: the incompleteness statement travels in the same payload as the results")
	})

	t.Run("unknown total is indeterminate and carries no ratio", func(t *testing.T) {
		walking := NewProgressTracker()
		walking.BeginEnumeration(false)
		walking.SetFound(42)

		got, res := b6Search(t, home, "mia", ws,
			ToolDeps{Progress: func(string) *ProgressTracker { return walking }},
			map[string]any{"query": "zarquon"})
		require.False(t, res.IsError, res.ForLLM)

		assert.False(t, got.Report.Complete)
		assert.True(t, got.Report.Indeterminate,
			"US-6 AS-1: while the tree is still being walked the state is indeterminate")
		assert.Equal(t, 42, got.Report.Found)
		assert.Zero(t, got.Report.Total, "FR-036 forbids a denominator that was never measured")
		assert.NotContains(t, got.Report.Statement, " of ",
			"the indeterminate statement must not read as a ratio: %q", got.Report.Statement)
	})

	t.Run("the process-wide tracker is the one the tool consults", func(t *testing.T) {
		// No Progress dep at all: this is the default path, and the whole
		// point of SharedProgressTracker's documented obligation.
		root := filepath.Clean(vault)
		shared := SharedProgressTracker(root)
		shared.BeginEnumeration(false)
		shared.BeginIndexing(500)
		shared.SetIndexed(3)
		defer shared.Finish(false)

		got, res := b5Search(t, home, "mia", ws, map[string]any{"query": "zarquon"})
		require.False(t, res.IsError, res.ForLLM)
		assert.False(t, got.Report.Complete,
			"driving SharedProgressTracker(root) must reach the search for that same root — "+
				"if it does not, every indexer that follows the documented contract is still "+
				"invisible to the searcher")
		assert.Equal(t, 3, got.Report.Indexed)
		assert.Equal(t, 500, got.Report.Total)
	})

	t.Run("a finished index states nothing", func(t *testing.T) {
		done := NewProgressTracker()
		done.BeginEnumeration(false)
		done.BeginIndexing(2)
		done.SetIndexed(2)
		done.Finish(false)

		got, res := b6Search(t, home, "mia", ws,
			ToolDeps{Progress: func(string) *ProgressTracker { return done }},
			map[string]any{"query": "zarquon"})
		require.False(t, res.IsError, res.ForLLM)
		assert.True(t, got.Report.Complete, "US-6 AS-4")
		assert.False(t, got.Incomplete)
		assert.Empty(t, got.Report.Statement,
			"a completed index must show NO incompleteness statement (US-6 AS-4)")
	})
}

// ---------------------------------------------------------------------------
// FR-050a(b) — the excerpt read budget, and its report
// ---------------------------------------------------------------------------

// TestSearchTool_ExhaustedExcerptBudgetIsReportedNotHidden covers the six lines
// that turn "budget_exhausted" into a caller-visible incompleteness statement.
//
// The suite proved that excerptAt returns the reason when handed a past
// deadline — by calling excerptAt directly. Nothing drove a real search into
// the budget, so the propagation into resp.Incomplete and resp.Notes could be
// deleted with the whole package green. A hit whose excerpt was skipped for
// time is a partial answer; FR-035 requires the answer to say so.
func TestSearchTool_ExhaustedExcerptBudgetIsReportedNotHidden(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")
	b5Note(t, vault, "a.md", "# A\n\nzarquon one\n")
	b5Note(t, vault, "b.md", "# B\n\nzarquon two\n")
	b5Index(t, home, vault)

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	// --- positive control: with the real budget, excerpts are produced ---
	full, res := b5Search(t, home, "mia", ws, map[string]any{"query": "zarquon"})
	require.False(t, res.IsError, res.ForLLM)
	require.Len(t, full.Results, 2)
	for _, h := range full.Results {
		require.Equal(t, ExcerptOK, h.ExcerptUnavailable, "control: %+v", h)
		require.NotEmpty(t, h.Excerpt)
	}
	require.False(t, full.Incomplete,
		"control: a complete answer with every excerpt read must NOT be marked incomplete")

	// --- the requirement: a budget already spent ---
	starved, res2 := b6Search(t, home, "mia", ws,
		ToolDeps{ExcerptBudget: time.Nanosecond},
		map[string]any{"query": "zarquon"})
	require.False(t, res2.IsError, res2.ForLLM)
	require.Len(t, starved.Results, 2,
		"FR-050a(a): a hit whose excerpt could not be read is still a real match and is still returned")
	for _, h := range starved.Results {
		assert.Equal(t, ExcerptBudgetExhausted, h.ExcerptUnavailable,
			"hit %q must name the budget as the reason, not carry a fabricated or empty excerpt", h.Path)
		assert.Empty(t, h.Excerpt)
	}
	assert.True(t, starved.Incomplete,
		"an answer with unread excerpts must be marked incomplete (FR-050a(b))")
	joined := strings.Join(starved.Notes, " | ")
	assert.Contains(t, joined, "budget",
		"the caller must be told WHY the excerpts are missing; notes were: %q", joined)
}

// ---------------------------------------------------------------------------
// FR-035 / FR-037 — folder scoping is a query, not a filter over the answer
// ---------------------------------------------------------------------------

// TestSearchTool_FolderScopeIsAppliedBeforeTheCap is the case a two-note
// fixture cannot express.
//
// The folder argument used to filter the ALREADY-CLAMPED top-N. On any
// collection with more matches than the cap, every in-folder hit that ranked
// below the cap was silently dropped — and the report attached to the answer
// described the unfiltered set, so the response was a partial answer carrying
// no incompleteness statement. Here the out-of-folder notes are named so they
// win every score tie (the index sorts equal scores by document id ascending),
// which is what makes them fill the cap.
func TestSearchTool_FolderScopeIsAppliedBeforeTheCap(t *testing.T) {
	home := b5Home(t)
	vault := b5Vault(t, filepath.Join(b5Real(t, t.TempDir()), "vault"), "Vault")

	const term = "commonword"
	const perFolder = 30
	for i := range perFolder {
		// "drop/aaa-*" sorts before "keep/zzz-*", so on equal scores these
		// occupy the whole of an unfiltered top-N.
		b5Note(t, vault, fmt.Sprintf("drop/aaa-%02d.md", i), "# Drop\n\n"+term+"\n")
		b5Note(t, vault, fmt.Sprintf("keep/zzz-%02d.md", i), "# Keep\n\n"+term+"\n")
	}
	b5Index(t, home, vault)

	ws := b5Workspace(t, home)
	b5Mount(t, home, ws, "notes", vault)

	// --- the fixture actually has the shape the test needs ---
	all, res := b5Search(t, home, "mia", ws, map[string]any{"query": term})
	require.False(t, res.IsError, res.ForLLM)
	require.Len(t, all.Results, SearchDefaultTopN,
		"the corpus must exceed the cap, or this test proves nothing")
	inKeep := 0
	for _, h := range all.Results {
		if strings.HasPrefix(h.Path, "keep/") {
			inKeep++
		}
	}
	require.Zero(t, inKeep,
		"the unfiltered top-%d must be entirely out-of-folder, or the fixture does not reproduce "+
			"the defect; got %d in-folder hits", SearchDefaultTopN, inKeep)

	// --- the requirement ---
	scoped, res2 := b5Search(t, home, "mia", ws, map[string]any{"query": term, "folder": "keep"})
	require.False(t, res2.IsError, res2.ForLLM)
	assert.Len(t, scoped.Results, SearchDefaultTopN,
		"a folder holding %d matches must fill the cap; filtering the clamped answer instead "+
			"returns whatever survived, which here is nothing", perFolder)
	for _, h := range scoped.Results {
		assert.True(t, strings.HasPrefix(h.Path, "keep/"),
			"folder scoping returned %q, which is outside the requested folder", h.Path)
	}
	assert.Equal(t, len(scoped.Results), scoped.ResultCount,
		"the reported count must describe the results actually returned")

	// A folder that shares a prefix with a real one is a DIFFERENT folder.
	none, res3 := b5Search(t, home, "mia", ws, map[string]any{"query": term, "folder": "kee"})
	require.False(t, res3.IsError, res3.ForLLM)
	assert.Empty(t, none.Results, `"kee" must not match the folder "keep" — membership is by path segment`)
}

// ---------------------------------------------------------------------------
// FR-031 / US-6 (P0) — the indexer and the searcher must find the SAME tracker
// ---------------------------------------------------------------------------

// TestSharedProgressTracker_IsKeyedByTheResolvedRealPath is the footgun in the
// wiring contract SharedProgressTracker's own doc comment describes.
//
// The obligation is that whoever indexes a collection drives the tracker this
// function returns for that root. The obligation can be honoured to the letter
// and still fail, silently, if the two callers spell the root differently — and
// they routinely will: the indexer is handed the mount record's path, the
// searcher is handed the scope layer's resolved one. On macOS every temporary
// directory already differs that way ("/var/…" vs "/private/var/…").
//
// Two spellings meant two trackers. The indexer drove one; the search found the
// other idle and reported a partial corpus as complete. The index itself is
// keyed on the resolved real path (D3/FR-031), so the tracker must be too.
func TestSharedProgressTracker_IsKeyedByTheResolvedRealPath(t *testing.T) {
	realParent := b5Real(t, t.TempDir())
	vault := filepath.Join(realParent, "vault")
	require.NoError(t, os.MkdirAll(vault, 0o755))

	// A second, equally valid spelling of the same directory.
	linkParent := b5Real(t, t.TempDir())
	viaLink := filepath.Join(linkParent, "alias")
	require.NoError(t, os.Symlink(realParent, viaLink))
	aliased := filepath.Join(viaLink, "vault")

	require.NotEqual(t, filepath.Clean(vault), filepath.Clean(aliased),
		"the fixture must supply two DIFFERENT spellings, or the test proves nothing")

	direct := SharedProgressTracker(vault)
	through := SharedProgressTracker(aliased)
	assert.Same(t, direct, through,
		"two spellings of one collection root must resolve to ONE tracker; two trackers means "+
			"the indexer drives one and the search reads the other as idle, i.e. complete (US-6)")

	// The consequence, asserted rather than inferred.
	direct.BeginEnumeration(false)
	direct.BeginIndexing(900)
	direct.SetIndexed(11)
	defer direct.Finish(false)
	assert.True(t, through.Progress().InFlight(),
		"driving the tracker under one spelling must be visible under the other")

	// A genuinely different collection still gets its own tracker.
	other := SharedProgressTracker(b5Real(t, t.TempDir()))
	assert.NotSame(t, direct, other, "distinct roots must not share a tracker")
	assert.False(t, other.Progress().InFlight())
}
