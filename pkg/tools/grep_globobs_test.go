// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// OBS-G1 (field report): `include_globs: ["spike.txt"]` (a bare filename,
// no "**/" prefix) silently matched nothing for a file that existed at
// "sub/spike.txt" — no error, no explanation, a zero-result indistinguishable
// from "the term genuinely is not there". This engine's stated contract
// (filegrep.go's own doc comment: "every truncation is honest") already
// tracks Stats.FilesFilteredGlob for exactly this; the agent-facing renderer
// (renderGrepResult, grep.go) just never surfaced it. This file pins that
// the fix makes the filtering VISIBLE, without silently auto-anchoring the
// glob (which would be a different kind of surprise for someone else later).

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGrepTool_GlobFilteredEverything_IsExplained is the field report
// itself: a bare filename include_glob against a nested file produces a
// zero-hit result that now STATES it filtered candidates out, rather than
// looking identical to "no match exists".
func TestGrepTool_GlobFilteredEverything_IsExplained(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "sub", "spike.txt"), "needle-content\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern":       "needle",
		"include_globs": []any{"spike.txt"}, // bare filename: only matches AT the root
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "0 match(es)") {
		t.Fatalf("precondition failed: expected zero hits (that's the defect's premise), got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "excluded by include_globs/exclude_globs") {
		t.Fatalf("expected the zero-hit result to explain that files were excluded by the glob, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "**/") {
		t.Fatalf("expected the anchoring hint (\"**/name.ext\") for a heavily-filtered zero-hit result, got:\n%s", res.ForLLM)
	}
}

// TestGrepTool_GlobDoesNotAutoAnchor pins the deliberate non-fix: a bare
// filename glob must keep matching ONLY at the search root — silently
// rewriting it to "**/name" would be exactly the kind of implicit magic the
// fix was told not to introduce. The correct fix is observability, not a
// behavior change.
func TestGrepTool_GlobDoesNotAutoAnchor(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "sub", "spike.txt"), "needle-content\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern":       "needle",
		"include_globs": []any{"spike.txt"},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "spike.txt:1:") {
		t.Fatalf("a bare filename glob must NOT be auto-anchored to match at any depth — the fix is " +
			"observability, not silently changing match semantics")
	}
}

// TestGrepTool_GlobDoubleStarPrefixWorks is the control: the SAME file is
// found once the caller uses the correctly-anchored "**/spike.txt" glob —
// proving the earlier zero-hit result really was a glob-anchoring problem,
// not some other defect.
func TestGrepTool_GlobDoubleStarPrefixWorks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "sub", "spike.txt"), "needle-content\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern":       "needle",
		"include_globs": []any{"**/spike.txt"},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "sub/spike.txt:1:") {
		t.Fatalf("expected \"**/spike.txt\" to find the nested file, got:\n%s", res.ForLLM)
	}
}

// TestGrepTool_GlobFilteredSome_NotEnoughForAnchoringHint proves the
// anchoring hint is CONDITIONAL ("large relative to files visited" per the
// spec), not appended to every glob-filtered result: a glob that legitimately
// narrows a search down from a few candidates to fewer still, while STILL
// producing real hits, does not need — and should not get — a hint implying
// the caller misused doublestar syntax.
func TestGrepTool_GlobFilteredSome_NotEnoughForAnchoringHint(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.go"), "needle in go file\n")
	mustWriteFile(t, filepath.Join(dir, "b.txt"), "needle in text file\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern":       "needle",
		"include_globs": []any{"*.go"},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "a.go:1:") {
		t.Fatalf("expected the glob to still find a.go, got:\n%s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "b.txt") {
		t.Fatalf("the glob must still exclude b.txt, got:\n%s", res.ForLLM)
	}
	// Stats footer still surfaces the (small) filtered count for
	// transparency, but a REAL hit means this is not the defect's "quiet
	// zero" case, so no dedicated zero-hit explanation block is emitted.
	if strings.Contains(res.ForLLM, "before any match was attempted") {
		t.Fatalf("the zero-hit explanation block must only appear on an actual zero-hit result, got:\n%s", res.ForLLM)
	}
}

// TestGrepTool_GlobFilteredStats_VisibleInFooter proves FilesFilteredGlob is
// surfaced in the stats footer even on a search that DOES produce hits — the
// task asked for the stat to be surfaced generally, not only in the
// zero-hit special case.
func TestGrepTool_GlobFilteredStats_VisibleInFooter(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.go"), "needle in go file\n")
	mustWriteFile(t, filepath.Join(dir, "b.txt"), "needle in text file\n")
	mustWriteFile(t, filepath.Join(dir, "c.txt"), "needle in another text file\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern":       "needle",
		"include_globs": []any{"*.go"},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "excluded by include_globs/exclude_globs") {
		t.Fatalf("expected the stats footer to report the excluded file count, got:\n%s", res.ForLLM)
	}
}

// TestGrepTool_ZeroMatches_NoGlobNoise is the negative control for
// TestGrepTool_ZeroMatches (grep_test.go): a genuine zero-hit search with NO
// globs at all must not gain any glob-related text — the fix must not add
// noise to the ordinary "term is not there" case.
func TestGrepTool_ZeroMatches_NoGlobNoise(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "f.txt"), "nothing interesting here\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{"pattern": "absolutely-not-present"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "excluded by include_globs") || strings.Contains(res.ForLLM, "doublestar") {
		t.Fatalf("a search with no globs at all must not mention glob filtering, got:\n%s", res.ForLLM)
	}
}
