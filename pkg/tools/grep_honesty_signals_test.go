// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Code review findings I5 / L9 / L10 (2026-09): the theme is that the HUMAN
// search surface (rest_library_files_search.go) reports honesty signals
// the AGENT surface (this package's renderGrepResult/grepStatsLine) has
// been silently dropping. Most tests here are checked directly against a
// synthetic filegrep.Result — the same style grep_stats_test.go already
// uses for F2/F3/F4 — since the defect (and the fix) lives entirely in this
// package's rendering, not in the engine's own walk behavior (pkg/filegrep
// is a sibling's package and is not touched here). The L9 test below is the
// one exception, run end-to-end through Execute, since the message it
// checks is assembled from a real filegrep.Search error rather than a
// synthetic Result.
package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/filegrep"
)

// TestGrepStatsLine_NamesSkippedBinaryAndUnreadableIgnoreFiles pins finding
// I5: Stats.FilesSkippedBinary (F-A) and Stats.IgnoreFilesUnreadable (F-E)
// exist specifically to be surfaced — filegrep.go's own comment on
// FilesSkippedBinary says a UTF-16 file "was silently content-invisible
// with no observable trace anywhere in the response. This counter is that
// trace." grepStatsLine rendered NEITHER before this fix. This result also
// carries a real HIT, proving the second half of I5's failure mode too:
// "When there ARE hits, nothing is printed at all" — the old code's only
// mention of binary files lived in the zero-hit branch, so a result with
// hits never surfaced either counter regardless of stats.
func TestGrepStatsLine_NamesSkippedBinaryAndUnreadableIgnoreFiles(t *testing.T) {
	res := filegrep.Result{
		Hits: []filegrep.Hit{{Path: "a.txt", Kind: filegrep.KindContent, Line: 1, Excerpt: "needle"}},
		Stats: filegrep.Stats{
			FilesVisited:          6,
			FilesSkippedBinary:    3,
			IgnoreFilesUnreadable: 2,
		},
	}
	out := renderGrepResult("needle", false, filegrep.CaseSmart, res)
	if !strings.Contains(out, "3 file(s) skipped as binary") {
		t.Fatalf("expected the stats line to name the 3 binary-skipped files, got:\n%s", out)
	}
	if !strings.Contains(out, "2 .gitignore/.ignore file(s) unreadable") {
		t.Fatalf("expected the stats line to name the 2 unreadable ignore files, got:\n%s", out)
	}
}

// TestGrepStatsLine_SilentWhenNothingSkipped is DoD requirement #2: the two
// counters above must NOT print when nothing was actually skipped, even on
// a result that otherwise has plenty of activity (files visited, a real
// hit) — mirroring the discipline every other optional counter in
// grepStatsLine already follows (FilesSkippedProblems, FilesPrunedIgnored,
// etc., all gated on ">0").
func TestGrepStatsLine_SilentWhenNothingSkipped(t *testing.T) {
	res := filegrep.Result{
		Hits: []filegrep.Hit{{Path: "a.txt", Kind: filegrep.KindContent, Line: 1, Excerpt: "needle"}},
		Stats: filegrep.Stats{
			FilesVisited:          6,
			FilesSkippedBinary:    0,
			IgnoreFilesUnreadable: 0,
		},
	}
	out := renderGrepResult("needle", false, filegrep.CaseSmart, res)
	if strings.Contains(out, "skipped as binary") {
		t.Fatalf("expected no binary-skipped mention when FilesSkippedBinary is zero, got:\n%s", out)
	}
	if strings.Contains(out, ".gitignore/.ignore file(s) unreadable") {
		t.Fatalf("expected no unreadable-ignore-file mention when IgnoreFilesUnreadable is zero, got:\n%s", out)
	}
}

// TestGrepNarrowingHint_Canceled pins finding L10's first half: the
// grepNarrowingHint switch covered every filegrep.TruncatedReason except the
// newly added ReasonCanceled (filegrep's finding F-H), so a canceled search
// fell through to the default case's narrowing advice — telling an agent
// interrupted by a user Stop to narrow its query with `path`/globs/
// `max_matches`, none of which had anything to do with why it stopped.
func TestGrepNarrowingHint_Canceled(t *testing.T) {
	hint := grepNarrowingHint(filegrep.ReasonCanceled)
	if !strings.Contains(hint, "interrup") {
		t.Fatalf("expected the canceled hint to name an interruption, got: %q", hint)
	}
	if strings.Contains(hint, "narrow with") {
		t.Fatalf("expected the canceled hint NOT to reuse the generic narrowing advice "+
			"(a canceled search was not too big — that was never its problem), got: %q", hint)
	}
}

// TestGrepRenderResult_CanceledReachesAgent is the end-to-end counterpart of
// the above: a Result carrying Truncated=true/ReasonCanceled must render
// the honest, canceled-specific message in the body an agent actually
// reads, not just in the standalone helper.
func TestGrepRenderResult_CanceledReachesAgent(t *testing.T) {
	res := filegrep.Result{Truncated: true, TruncatedReason: filegrep.ReasonCanceled}
	out := renderGrepResult("needle", false, filegrep.CaseSmart, res)
	if !strings.Contains(out, "reason: canceled") {
		t.Fatalf("expected the reason itself to be stated, got:\n%s", out)
	}
	if !strings.Contains(out, "interrup") {
		t.Fatalf("expected the canceled-specific explanation to reach the rendered body, got:\n%s", out)
	}
	if strings.Contains(out, "or a lower `max_matches`") {
		t.Fatalf("expected the canceled case NOT to fall through to the generic default hint, got:\n%s", out)
	}
}

// TestGrepRenderResult_TruncatedRootNamed pins finding L10's second half:
// Result.TruncatedRoot names WHICH root (Root.Name — the mount name for a
// mount) produced ReasonRootLost, but renderGrepResult never rendered it —
// root_lost reached the agent without ever saying which mount was lost.
func TestGrepRenderResult_TruncatedRootNamed(t *testing.T) {
	res := filegrep.Result{
		Truncated: true, TruncatedReason: filegrep.ReasonRootLost, TruncatedRoot: "external-drive",
	}
	out := renderGrepResult("needle", false, filegrep.CaseSmart, res)
	if !strings.Contains(out, `mount "external-drive"`) {
		t.Fatalf("expected the lost root's name to be rendered, got:\n%s", out)
	}
}

// TestGrepRenderResult_TruncatedRootEmptyOmitsNote proves the root-lost note
// is only added when TruncatedRoot actually names something — matching the
// same convention the REST renderer already uses
// (rest_library_files_search.go: "result.TruncatedRoot != \"\""), so a
// single-root search (where TruncatedRoot legitimately stays empty per
// filegrep.Result's own doc comment) does not print a bogus, empty root
// name.
func TestGrepRenderResult_TruncatedRootEmptyOmitsNote(t *testing.T) {
	res := filegrep.Result{
		Truncated: true, TruncatedReason: filegrep.ReasonRootLost, TruncatedRoot: "",
	}
	out := renderGrepResult("needle", false, filegrep.CaseSmart, res)
	if strings.Contains(out, "root:") {
		t.Fatalf("expected no root note when TruncatedRoot is empty, got:\n%s", out)
	}
	if !strings.Contains(out, "reason: root_lost") {
		t.Fatalf("expected the reason itself to still be stated, got:\n%s", out)
	}
}

// TestGrepTool_MalformedGlob_LabeledAsGlobNotRegex pins finding L9:
// filegrep.Search returns a bad include_globs/exclude_globs entry
// (validateGlobs, finding F-B) through the exact same error slot as a bad
// regex (regexp.Compile), and grep.go labeled BOTH "grep: invalid pattern:
// ...". A glob problem should say glob, so an agent debugging a rejected
// search looks in the right argument. "a[b" is this codebase's own
// established known-bad glob (pkg/filegrep/silent_failure_audit_test.go:
// `doublestar.ValidatePattern("a[b") == false`).
func TestGrepTool_MalformedGlob_LabeledAsGlobNotRegex(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.txt"), "needle\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern":       "needle",
		"include_globs": []any{"a[b"},
	})
	if !res.IsError {
		t.Fatalf("a malformed include_globs entry must be refused, got success:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "invalid glob pattern") {
		t.Fatalf("expected the error to say GLOB, not the generic/regex phrasing, got: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "invalid pattern:") && !strings.Contains(res.ForLLM, "invalid glob pattern:") {
		t.Fatalf("expected the glob-specific label, not the bare regex-style label, got: %s", res.ForLLM)
	}
}

// TestGrepTool_MalformedRegex_StillLabeledAsPattern is the control case for
// L9: a genuinely bad REGEX must still say "invalid pattern" (never
// "invalid glob pattern") — proving grepIsGlobPatternError distinguishes
// the two causes rather than reclassifying everything as a glob problem.
func TestGrepTool_MalformedRegex_StillLabeledAsPattern(t *testing.T) {
	dir := t.TempDir()
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern": "(unterminated",
		"regex":   true,
	})
	if !res.IsError {
		t.Fatalf("a malformed regex must be refused, got success:\n%s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "invalid glob pattern") {
		t.Fatalf("a bad REGEX must not be mislabeled as a glob problem, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "invalid pattern") {
		t.Fatalf("expected the regex case to keep the pattern label, got: %s", res.ForLLM)
	}
}
