// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// F2/F3/F4 (review findings), all about renderGrepResult/grepCapOutput's own
// rendering logic — tested here directly against synthetic filegrep.Result
// values rather than through a real directory walk, since the defect (and
// the fix) lives entirely in this package's rendering, not in the engine's
// walk behavior (pkg/filegrep is a sibling's package and is not touched
// here).

package tools

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/filegrep"
)

// TestGrepRenderResult_DirsVisitedReachesAgent is F2: grep.go rendered
// FilesVisited and BytesScanned but omitted DirsVisited entirely, even
// though the engine added that counter specifically so a directory-heavy
// search explains itself instead of reporting "0 files searched" with no
// account of where the work went. REST already carries DirsVisited
// (rest_library_files_search.go); this pins that the agent-facing renderer
// does too.
func TestGrepRenderResult_DirsVisitedReachesAgent(t *testing.T) {
	res := filegrep.Result{
		Hits: []filegrep.Hit{{Path: "a.txt", Kind: filegrep.KindContent, Line: 1, Excerpt: "needle"}},
		Stats: filegrep.Stats{
			FilesVisited: 3,
			DirsVisited:  7,
			BytesScanned: 42,
		},
	}
	out := renderGrepResult("needle", false, filegrep.CaseSmart, res)
	if !strings.Contains(out, "7 dir(s) visited") {
		t.Fatalf("expected DirsVisited to reach the agent-facing stats line, got:\n%s", out)
	}
}

// TestGrepRenderResult_DirsVisitedUnconditional confirms DirsVisited is
// rendered even when it is legitimately zero (a shallow, no-subdirectory
// search) — unlike the optional counters below it (FilesSkippedProblems
// etc.), which are gated on ">0", DirsVisited sits alongside FilesVisited
// and BytesScanned as a primary, always-present count.
func TestGrepRenderResult_DirsVisitedUnconditional(t *testing.T) {
	res := filegrep.Result{
		Hits:  []filegrep.Hit{{Path: "a.txt", Kind: filegrep.KindContent, Line: 1, Excerpt: "needle"}},
		Stats: filegrep.Stats{FilesVisited: 1, DirsVisited: 0, BytesScanned: 6},
	}
	out := renderGrepResult("needle", false, filegrep.CaseSmart, res)
	if !strings.Contains(out, "0 dir(s) visited") {
		t.Fatalf("expected DirsVisited=0 to still be stated explicitly, got:\n%s", out)
	}
}

// TestGrepRenderResult_ZeroHitExplainsPrunedIgnored is F3's first half: the
// OBS-G1 zero-hit explanation fired only for include_globs/exclude_globs
// filtering. FilesPrunedIgnored (.gitignore/.ignore/hidden-file pruning) is
// the commonest real cause of a surprising zero-hit result — a search term
// living in a gitignored directory like "dist/" — and the same reasoning
// (a small, zero-hit result gives a reader no cause to scroll to a stats
// footer) applies to it verbatim.
func TestGrepRenderResult_ZeroHitExplainsPrunedIgnored(t *testing.T) {
	res := filegrep.Result{
		Stats: filegrep.Stats{FilesVisited: 5, FilesPrunedIgnored: 9},
	}
	out := renderGrepResult("needle", false, filegrep.CaseSmart, res)
	if !strings.Contains(out, "9 file(s)/director(ies) were pruned by .gitignore") {
		t.Fatalf("expected the zero-hit result to explain that files were pruned by ignore rules, got:\n%s", out)
	}
}

// TestGrepRenderResult_ZeroHitExplainsPrunedIgnored_Absent proves the new
// explanation is CONDITIONAL, not appended unconditionally to every
// zero-hit result — mirroring the existing glob-filtered case's own
// discipline (TestGrepTool_GlobFilteredSome_NotEnoughForAnchoringHint).
func TestGrepRenderResult_ZeroHitExplainsPrunedIgnored_Absent(t *testing.T) {
	res := filegrep.Result{Stats: filegrep.Stats{FilesPrunedIgnored: 0}}
	out := renderGrepResult("needle", false, filegrep.CaseSmart, res)
	if strings.Contains(out, "were pruned by .gitignore") {
		t.Fatalf("expected no pruned-ignored explanation when FilesPrunedIgnored is zero, got:\n%s", out)
	}
}

// TestGrepRenderResult_ZeroHitExplainsBinarySkip is F3's second half: binary
// files are content-unsearchable by contract (FR-005) but have no dedicated
// Stats counter of their own — so unlike the glob/ignore cases, this note is
// unconditional whenever at least one file was visited (there being no
// counter to gate on), stating a real, otherwise-invisible reason a search
// can legitimately find nothing.
func TestGrepRenderResult_ZeroHitExplainsBinarySkip(t *testing.T) {
	res := filegrep.Result{Stats: filegrep.Stats{FilesVisited: 4}}
	out := renderGrepResult("needle", false, filegrep.CaseSmart, res)
	if !strings.Contains(out, "Binary files") || !strings.Contains(out, "name-matchable only") {
		t.Fatalf("expected the zero-hit result to note binary files are content-unsearchable, got:\n%s", out)
	}
}

// TestGrepRenderResult_ZeroHitExplainsBinarySkip_AbsentWhenNothingVisited
// proves the binary note does not fire on a genuinely empty search (nothing
// visited at all) — there being nothing that plausibly could have been
// binary.
func TestGrepRenderResult_ZeroHitExplainsBinarySkip_AbsentWhenNothingVisited(t *testing.T) {
	res := filegrep.Result{Stats: filegrep.Stats{FilesVisited: 0}}
	out := renderGrepResult("needle", false, filegrep.CaseSmart, res)
	if strings.Contains(out, "Binary files") {
		t.Fatalf("expected no binary-skip note when zero files were visited, got:\n%s", out)
	}
}

// TestGrepRenderResult_StatsSurviveOutputCap is F4: grepCapOutput slices the
// rendered body from the START, so on any large result the ENTIRE stats
// footer — previously rendered only after every hit — was destroyed. The
// truncation verdict was deliberately hoisted to the top for this exact
// reason (MV-3a); this proves the accounting now rides along with it,
// surviving the cap the same way.
func TestGrepRenderResult_StatsSurviveOutputCap(t *testing.T) {
	// Build enough hits that the rendered body comfortably exceeds the
	// 64,000-character tool cap.
	hits := make([]filegrep.Hit, 0, 3000)
	for i := 0; i < 3000; i++ {
		hits = append(hits, filegrep.Hit{
			Path: "pkg/some/very/long/nested/directory/tree/file.go", Kind: filegrep.KindContent,
			Line: i + 1, Excerpt: "needle appears in a moderately long excerpt line here for padding",
		})
	}
	res := filegrep.Result{
		Hits:      hits,
		Truncated: true, TruncatedReason: filegrep.ReasonMaxMatches,
		Stats: filegrep.Stats{FilesVisited: 500, DirsVisited: 42, BytesScanned: 999999},
	}
	rendered := renderGrepResult("needle", false, filegrep.CaseSmart, res)
	if len([]rune(rendered)) <= config.DefaultBuiltinSuccessCap {
		t.Fatalf("precondition failed: rendered body (%d runes) must exceed the %d cap for this test to mean anything",
			len([]rune(rendered)), config.DefaultBuiltinSuccessCap)
	}
	capped := grepCapOutput(rendered, res.Truncated)
	if !strings.Contains(capped, "42 dir(s) visited") || !strings.Contains(capped, "500 file(s) visited") {
		t.Fatalf("expected the stats accounting to survive the output cap, got head:\n%s\n...\ngot tail:\n%s",
			capped[:300], capped[len(capped)-300:])
	}
}
