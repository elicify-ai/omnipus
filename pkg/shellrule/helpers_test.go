// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeSegmenter mirrors pkg/tools/shell_subst_guard.go::splitShellSegments's
// documented contract (split at | ; & \n \r) for these tests. Production
// wiring injects the real, unexported function directly by value (see
// doc.go) — this package cannot import pkg/tools to call it, so tests use
// their own faithful stand-in.
func fakeSegmenter(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '|', ';', '&', '\n', '\r':
			return true
		}
		return false
	})
}

// fakeHeadResolver mirrors pkg/tools/shell_subst_guard.go
// ::shellCommandHeadDetailed's documented contract (see the HeadResolver
// doc comment): lowercases, strips a leading VAR=value assignment (and its
// value), strips a directory prefix, and reports fromExpansion/normalised.
// A segment whose head cannot be resolved (e.g. it is a bare redirection)
// returns "".
func fakeHeadResolver(seg string) (string, bool, bool) {
	for {
		i := 0
		for i < len(seg) && (seg[i] == ' ' || seg[i] == '\t' || seg[i] == '(' || seg[i] == '{' || seg[i] == '!') {
			i++
		}
		seg = seg[i:]
		if seg == "" {
			return "", false, false
		}
		end := len(seg)
		for j := 0; j < len(seg); j++ {
			if strings.ContainsRune(" \t\n\r<>|;&(){}`$", rune(seg[j])) {
				end = j
				break
			}
		}
		if end == 0 {
			return "", seg[0] == '$', false
		}
		tok := seg[:end]
		rest := seg[end:]
		if eq := strings.IndexByte(tok, '='); eq > 0 && isAssignment(tok[:eq+1]) {
			seg = rest
			continue
		}
		normalised := false
		lower := strings.ToLower(tok)
		if lower != tok {
			normalised = true
		}
		tok = lower
		if idx := strings.LastIndexByte(tok, '/'); idx >= 0 {
			tok = tok[idx+1:]
			normalised = true
		}
		if end < len(seg) && seg[end] == '$' {
			return tok, true, normalised
		}
		return tok, false, normalised
	}
}

// writeFakeExecutable creates name under dir as a minimal, executable
// (on POSIX) regular file and returns its path exactly as written (not yet
// symlink-resolved or made absolute beyond dir's own form).
func writeFakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("writeFakeExecutable(%q): %v", full, err)
	}
	return full
}

// writeNonExecutable creates name under dir as a regular file with no
// execute bit (POSIX-only check; the test that uses this skips on
// Windows).
func writeNonExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte("not executable\n"), 0o644); err != nil {
		t.Fatalf("writeNonExecutable(%q): %v", full, err)
	}
	return full
}

func skipOnWindows(t *testing.T, reason string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skipf("POSIX-only: %s", reason)
	}
}
