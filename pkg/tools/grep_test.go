// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/filegrep"
)

// newTestGrepTool builds a GrepTool rooted at dir and gives the test its own
// $OMNIPUS_HOME (t.Setenv), so config.OmnipusHomeDir() never touches the
// real machine's ~/.omnipus during ResolveTurnFSPolicy/mount resolution.
func newTestGrepTool(t *testing.T, dir string) *GrepTool {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	return NewGrepTool(dir, true)
}

// --- Argument validation ----------------------------------------------------

func TestGrepTool_ArgValidation(t *testing.T) {
	dir := t.TempDir()
	tool := newTestGrepTool(t, dir)
	ctx := context.Background()

	cases := []struct {
		name string
		args map[string]any
	}{
		{"missing pattern", map[string]any{}},
		{"nil pattern", map[string]any{"pattern": nil}},
		{"empty pattern", map[string]any{"pattern": ""}},
		{"non-string pattern", map[string]any{"pattern": 42}},
		{"invalid case value", map[string]any{"pattern": "x", "case": "loud"}},
		{"non-string case value", map[string]any{"pattern": "x", "case": 1}},
		{"path escapes with ..", map[string]any{"pattern": "x", "path": "../elsewhere"}},
		{"path is absolute", map[string]any{"pattern": "x", "path": "/etc/passwd"}},
		{"path nested ..", map[string]any{"pattern": "x", "path": "sub/../../escape"}},
		{"include_globs wrong type", map[string]any{"pattern": "x", "include_globs": 5}},
		{"include_globs array of non-strings", map[string]any{"pattern": "x", "include_globs": []any{1, 2}}},
		{"exclude_globs wrong type", map[string]any{"pattern": "x", "exclude_globs": map[string]any{}}},
		{"invalid regex pattern", map[string]any{"pattern": "(unterminated", "regex": true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tool.Execute(ctx, tc.args)
			if res == nil || !res.IsError {
				t.Fatalf("%s: expected an error result, got %+v", tc.name, res)
			}
			if res.ForLLM == "" {
				t.Fatalf("%s: error result must carry a non-empty message", tc.name)
			}
		})
	}
}

// TestGrepTool_ValidRegexExecutes proves a WELL-FORMED regex pattern is
// accepted (differentiates the invalid-regex case above from "regex is
// always rejected").
func TestGrepTool_ValidRegexExecutes(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "a.txt"), "needle-1\nnee2dle\nirrelevant\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern": "nee.dle",
		"regex":   true,
	})
	if res.IsError {
		t.Fatalf("valid regex must execute, got error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "a.txt:2:") {
		t.Fatalf("expected a.txt line 2 to match nee.dle, got:\n%s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "a.txt:1:") {
		t.Fatalf("nee.dle (regex) must not match the literal hyphen in a.txt:1, got:\n%s", res.ForLLM)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
}

// --- Case modes (MV-13, US-3 AS-9) ------------------------------------------

func TestGrepTool_CaseModes(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "notes.txt"),
		"line one is fine\ntodo: fix the bug\nTODO: also this one\nlast line\n")

	run := func(pattern, caseMode string) string {
		tool := newTestGrepTool(t, dir)
		args := map[string]any{"pattern": pattern}
		if caseMode != "" {
			args["case"] = caseMode
		}
		res := tool.Execute(context.Background(), args)
		if res.IsError {
			t.Fatalf("pattern=%q case=%q: unexpected error: %s", pattern, caseMode, res.ForLLM)
		}
		return res.ForLLM
	}

	t.Run("sensitive matches only the exact case", func(t *testing.T) {
		out := run("todo", "sensitive")
		if !strings.Contains(out, "notes.txt:2:") {
			t.Errorf("expected line 2 (lowercase todo) to match, got:\n%s", out)
		}
		if strings.Contains(out, "notes.txt:3:") {
			t.Errorf("case sensitive must NOT match line 3 (TODO), got:\n%s", out)
		}
	})

	t.Run("insensitive matches every case", func(t *testing.T) {
		out := run("todo", "insensitive")
		if !strings.Contains(out, "notes.txt:2:") || !strings.Contains(out, "notes.txt:3:") {
			t.Errorf("case insensitive must match both lines 2 and 3, got:\n%s", out)
		}
	})

	t.Run("smart-case lowercase pattern matches every case", func(t *testing.T) {
		out := run("todo", "") // default smart
		if !strings.Contains(out, "notes.txt:2:") || !strings.Contains(out, "notes.txt:3:") {
			t.Errorf("smart-case with an all-lowercase pattern must behave insensitively, got:\n%s", out)
		}
	})

	t.Run("smart-case pattern with an uppercase letter is sensitive", func(t *testing.T) {
		out := run("TODO", "") // default smart
		if strings.Contains(out, "notes.txt:2:") {
			t.Errorf("smart-case with an uppercase-bearing pattern must NOT match lowercase todo, got:\n%s", out)
		}
		if !strings.Contains(out, "notes.txt:3:") {
			t.Errorf("smart-case with an uppercase-bearing pattern must match the exact-case TODO, got:\n%s", out)
		}
	})
}

// --- Content matches, context lines, name matches ---------------------------

func TestGrepTool_ContextLines(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "f.txt"), "l1\nl2\nneedle-here\nl4\nl5\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern":       "needle",
		"context_lines": 2,
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	out := res.ForLLM
	for _, want := range []string{"f.txt-1-", "f.txt-2-", "f.txt:3:", "f.txt-4-", "f.txt-5-"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected rendered output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestGrepTool_NameMatchHasNoLineNumber(t *testing.T) {
	dir := t.TempDir()
	// Binary content (embedded NUL) so only the NAME can match (FR-005).
	if err := os.WriteFile(filepath.Join(dir, "needle-report.bin"), []byte("abc\x00def"), 0o600); err != nil {
		t.Fatalf("seed binary file: %v", err)
	}
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "needle-report.bin  (name match)") {
		t.Fatalf("expected a name-match line with no line number, got:\n%s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "needle-report.bin:") {
		t.Fatalf("a binary file must never be content-scanned (FR-005), got:\n%s", res.ForLLM)
	}
}

func TestGrepTool_ZeroMatches(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "f.txt"), "nothing interesting here\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{"pattern": "absolutely-not-present"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "0 match(es)") || !strings.Contains(res.ForLLM, "(no hits)") {
		t.Fatalf("expected an explicit zero-matches line, got:\n%s", res.ForLLM)
	}
}

// --- Output rendering, 64k cap, marker, MV-3a reason precedence ------------

// TestGrepTool_CapOutput_StandaloneMaxOutput is a function-level pin on
// grepCapOutput's own logic: when the engine did NOT truncate, a cap that
// fires on its own account states reason: max_output.
func TestGrepTool_CapOutput_StandaloneMaxOutput(t *testing.T) {
	big := strings.Repeat("x", config.DefaultBuiltinSuccessCap+5000)
	out := grepCapOutput(big, false)

	runeCount := len([]rune(out))
	if runeCount <= config.DefaultBuiltinSuccessCap {
		t.Fatalf("expected the marker to be appended past the cap (%d), got total length %d", config.DefaultBuiltinSuccessCap, runeCount)
	}
	if !strings.Contains(out, "reason: max_output") {
		t.Fatalf("an un-truncated engine result whose serialization is cut must claim reason: max_output, got tail: %q", out[len(out)-300:])
	}
	if strings.Contains(out, "engine's own truncation reason") {
		t.Fatalf("must not claim an engine reason that was never set, got tail: %q", out[len(out)-300:])
	}
}

// TestGrepTool_CapOutput_EngineReasonPreserved is grepCapOutput's other
// branch: when the engine ALREADY truncated (a reason is already in the
// body), a serialization cap that also fires must not override it with a
// second, different reason (MV-3a).
func TestGrepTool_CapOutput_EngineReasonPreserved(t *testing.T) {
	big := strings.Repeat("x", config.DefaultBuiltinSuccessCap+5000)
	out := grepCapOutput(big, true)

	if strings.Contains(out, "reason: max_output") {
		t.Fatalf("must not claim reason: max_output when the engine already gave its own reason, got tail: %q", out[len(out)-300:])
	}
	if !strings.Contains(out, "engine's own truncation reason above still applies") {
		t.Fatalf("expected the layering note deferring to the engine's own reason, got tail: %q", out[len(out)-300:])
	}
}

// TestGrepTool_CapOutput_NoCapWhenSmall proves grepCapOutput is a no-op
// under the cap — no marker text is invented for output that already fits.
func TestGrepTool_CapOutput_NoCapWhenSmall(t *testing.T) {
	small := "hello world"
	if got := grepCapOutput(small, false); got != small {
		t.Fatalf("expected output under the cap to pass through unchanged, got %q", got)
	}
}

// bulkMatchDir seeds numFiles files of exactly linesPerFile matching lines
// each (long lines, so a handful of hits already dwarfs the 64k tool cap),
// and returns the directory.
func bulkMatchDir(t *testing.T, numFiles, linesPerFile int) string {
	t.Helper()
	dir := t.TempDir()
	line := "needle " + strings.Repeat("z", 500) + "\n"
	content := strings.Repeat(line, linesPerFile)
	for i := 0; i < numFiles; i++ {
		name := fmt.Sprintf("bulk_%03d.txt", i)
		mustWriteFile(t, filepath.Join(dir, name), content)
	}
	return dir
}

// TestGrepTool_OutputCapTruncates_EngineTruncatedFirst is a REAL, end-to-end
// run (US-3 AS-8, MV-3a): filegrep's own default matches cap (1,000) fires
// first — 25 files x 50 (the default per-file cap) = 1,250 available hits —
// and the resulting rendering (each hit carries a ~512-byte excerpt) is
// still far larger than the 64,000-char tool cap, so BOTH truncation layers
// fire on the same real call. The tool cap must not invent a second reason.
func TestGrepTool_OutputCapTruncates_EngineTruncatedFirst(t *testing.T) {
	dir := bulkMatchDir(t, 25, 50)
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}

	runeCount := len([]rune(res.ForLLM))
	if runeCount > config.DefaultBuiltinSuccessCap+2000 {
		t.Fatalf("rendered output (%d runes) is far larger than the cap plus a small marker allowance", runeCount)
	}
	if !strings.Contains(res.ForLLM, "reason: max_matches") {
		t.Fatalf("expected the engine's own max_matches reason to survive in the body, got tail: %q", res.ForLLM[len(res.ForLLM)-400:])
	}
	if strings.Contains(res.ForLLM, "reason: max_output") {
		t.Fatalf("MV-3a: the serialization cap must not claim reason: max_output over an existing engine reason, got tail: %q", res.ForLLM[len(res.ForLLM)-400:])
	}
	if !strings.Contains(res.ForLLM, "engine's own truncation reason above still applies") {
		t.Fatalf("expected the layering note, got tail: %q", res.ForLLM[len(res.ForLLM)-400:])
	}
}

// TestGrepTool_OutputCapTruncates_StandaloneReal is the real-engine
// counterpart of the standalone-max_output unit test above: few enough
// matches (750, under every MV-3 engine bound) that filegrep itself never
// truncates, yet the rendering still exceeds 64,000 chars — so the tool's
// OWN serialization cap must fire and claim max_output on its own account.
func TestGrepTool_OutputCapTruncates_StandaloneReal(t *testing.T) {
	dir := bulkMatchDir(t, 15, 50) // 750 hits: under the 1,000 default cap
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "reason: max_output") {
		t.Fatalf("expected the tool's own serialization cap to fire with reason: max_output, got tail: %q", res.ForLLM[len(res.ForLLM)-400:])
	}
}

// --- Busy path (MV-11) -------------------------------------------------------

// TestGrepTool_BusyReturnsStructuredError saturates the shared 2-slot walk
// semaphore from OUTSIDE the tool, then proves Execute waits (does not
// error instantly) and, once its own 2s budget elapses with no slot freed,
// returns a structured busy refusal rather than blocking forever or
// panicking.
func TestGrepTool_BusyReturnsStructuredError(t *testing.T) {
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "f.txt"), "needle\n")
	tool := newTestGrepTool(t, dir)

	if !filegrep.TryAcquire(context.Background()) {
		t.Fatal("setup: failed to acquire slot 1")
	}
	if !filegrep.TryAcquire(context.Background()) {
		t.Fatal("setup: failed to acquire slot 2")
	}
	defer filegrep.Release()
	defer filegrep.Release()

	start := time.Now()
	res := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	elapsed := time.Since(start)

	if !res.IsError {
		t.Fatalf("expected a busy refusal while both slots are held, got success: %s", res.ForLLM)
	}
	if !strings.Contains(strings.ToLower(res.ForLLM), "busy") {
		t.Fatalf("expected a structured busy message, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "retry") {
		t.Fatalf("expected a retry hint in the busy message, got: %s", res.ForLLM)
	}
	if elapsed < 1500*time.Millisecond {
		t.Fatalf("expected Execute to have waited close to the 2s busy budget before refusing, only waited %s", elapsed)
	}
}

// TestGrepTool_ScopedSearchHonorsAncestorIgnore — the caller half of the
// engine's ancestor-ignore support: narrowing a search with `path` must not
// change WHICH files are ignored. Before the roots carried ScopePrefix and
// AncestorIgnore, `grep x` and `grep x path:"src"` disagreed about the very
// same file — the scoped call re-rooted the fs.FS, so the workspace-root
// .gitignore that pruned src/build simply was not read any more. Same file,
// same query, same reported stats, opposite answer, and nothing in the
// response said the rules had changed.
func TestGrepTool_ScopedSearchHonorsAncestorIgnore(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "build"), 0o700); err != nil {
		t.Fatalf("seed dirs: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, ".gitignore"), "build/\n")
	mustWriteFile(t, filepath.Join(dir, "src", "main.go"), "needle in real source\n")
	mustWriteFile(t, filepath.Join(dir, "src", "build", "generated.go"), "needle in generated output\n")
	tool := newTestGrepTool(t, dir)

	unscoped := tool.Execute(context.Background(), map[string]any{"pattern": "needle"})
	if unscoped.IsError {
		t.Fatalf("unscoped search errored: %s", unscoped.ForLLM)
	}
	if strings.Contains(unscoped.ForLLM, "generated.go") {
		t.Fatalf("precondition failed: the root .gitignore should prune src/build, got:\n%s", unscoped.ForLLM)
	}

	scoped := tool.Execute(context.Background(), map[string]any{"pattern": "needle", "path": "src"})
	if scoped.IsError {
		t.Fatalf("scoped search errored: %s", scoped.ForLLM)
	}
	if !strings.Contains(scoped.ForLLM, "main.go") {
		t.Fatalf("scoping to src must still find src/main.go, got:\n%s", scoped.ForLLM)
	}
	if strings.Contains(scoped.ForLLM, "generated.go") {
		t.Fatalf("scoping to src must not un-ignore what the workspace-root .gitignore prunes; "+
			"the ancestor layer above the scope was dropped. got:\n%s", scoped.ForLLM)
	}
}
