// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// F5 (review finding): `path` was never normalised before use. Verified by
// the reviewer: os.Root.Stat accepts an unnormalized spelling like "./src",
// and (*os.Root).Name/resolveScopedRoot's `name := subPath` takes the raw
// string as-is, so hits reported under a "./src" scope come back prefixed
// "./src/..." — a spelling that "**/"-prefixed globs still happen to match,
// but an anchored include_globs like "src/**" does not:
// doublestar.Match("src/**", "./src/foo.go") is false, even though the file
// is exactly the one "src/**" means to name. This file pins that
// normalizeGrepScope closes the gap: the reported path and the caller's own
// glob must agree on the scope's spelling.

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGrepNormalizeScope is a direct, spec-derived table test of
// normalizeGrepScope's contract (path.Clean semantics, "" and "." both
// meaning "the whole workspace") — independent of any real directory walk.
func TestGrepNormalizeScope(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{".", ""},
		{"./src", "src"},
		{"src/", "src"},
		{"src//build", "src/build"},
		{"src/./build", "src/build"},
		{"already/clean", "already/clean"},
		{"a", "a"},
	}
	for _, tc := range cases {
		if got := normalizeGrepScope(tc.in); got != tc.want {
			t.Errorf("normalizeGrepScope(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestGrepTool_ScopeNormalized_GlobsAndReportedPathAgree is the field-shape
// reproduction: an agent scopes to "./src" (a spelling (*os.Root).Stat
// happily accepts) and ALSO supplies an anchored include_globs of "src/**"
// (the doublestar-correct way to match everything under "src"). Before the
// fix, the reported hit path carried the raw "./src/..." prefix, which
// "src/**" does not match — a caller who did everything "right" (anchored
// glob, matching the scope they asked for) got a silent, unexplained
// zero-hit result.
func TestGrepTool_ScopeNormalized_GlobsAndReportedPathAgree(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "src", "foo.go"), "needle\n")
	tool := newTestGrepTool(t, dir)

	res := tool.Execute(context.Background(), map[string]any{
		"pattern":       "needle",
		"path":          "./src",
		"include_globs": []any{"src/**"},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "1 match(es)") {
		t.Fatalf("expected the anchored include_globs to match once `path` is normalized to agree with it "+
			"(F5), got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "src/foo.go:1:") {
		t.Fatalf("expected the reported hit path to be the normalized \"src/foo.go\", got:\n%s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "./src/foo.go") {
		t.Fatalf("reported path must not retain the unnormalized \"./\" prefix, got:\n%s", res.ForLLM)
	}
}

// TestGrepTool_ScopeNormalized_TrailingSlashAndDoubleSlash extends the same
// proof to two more unnormalized spellings ("src/" and "src//sub") that
// os.Root.Stat also accepts as-is.
func TestGrepTool_ScopeNormalized_TrailingSlashAndDoubleSlash(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "sub"), 0o700); err != nil {
		t.Fatalf("seed dir: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "src", "top.txt"), "needle-top\n")
	mustWriteFile(t, filepath.Join(dir, "src", "sub", "nested.txt"), "needle-nested\n")

	t.Run("trailing slash", func(t *testing.T) {
		tool := newTestGrepTool(t, dir)
		res := tool.Execute(context.Background(), map[string]any{
			"pattern":       "needle",
			"path":          "src/",
			"include_globs": []any{"src/**"},
		})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "2 match(es)") {
			t.Fatalf("expected both files under a trailing-slash-normalized scope to match \"src/**\", got:\n%s", res.ForLLM)
		}
	})

	t.Run("doubled slash", func(t *testing.T) {
		tool := newTestGrepTool(t, dir)
		res := tool.Execute(context.Background(), map[string]any{
			"pattern":       "needle-nested",
			"path":          "src//sub",
			"include_globs": []any{"src/sub/**"},
		})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "src/sub/nested.txt:1:") {
			t.Fatalf("expected the doubled-slash scope to normalize and match \"src/sub/**\", got:\n%s", res.ForLLM)
		}
	})
}
