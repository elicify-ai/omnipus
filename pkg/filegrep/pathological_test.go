// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import (
	"strings"
	"testing"
	"time"
)

// TestFileGrep_PathologicalPattern64KiB pins MV-4/SC-004: a 64 KiB
// alternation-bomb regex pattern must never hang — RE2 (grafana/regexp,
// same guarantee as the standard library) is linear-time in the input it
// scans, and Go's own compiler rejects patterns whose compiled program would
// be too large, so the only two allowed outcomes are a compile-time reject
// or a bounded completion, both well within the 10s SC-004 budget.
func TestFileGrep_PathologicalPattern64KiB(t *testing.T) {
	// A classic backtracking bomb ((a?){n}a{n}) is harmless under RE2 (no
	// backtracking engine to blow up) but still exercises a large compiled
	// program; build it out to ~64 KiB of pattern source.
	var b strings.Builder
	n := 0
	for b.Len() < 64*1024 {
		b.WriteString("(a?)")
		n++
	}
	b.WriteString(strings.Repeat("a", n))
	pattern := b.String()
	if len(pattern) < 64*1024 {
		t.Fatalf("test setup: pattern only %d bytes, want >= 64KiB", len(pattern))
	}

	done := make(chan struct{})
	var m *matcher
	var compileErr error
	start := time.Now()
	go func() {
		m, compileErr = compile(Options{Query: pattern, Regex: true})
		close(done)
	}()

	select {
	case <-done:
		elapsed := time.Since(start)
		if elapsed > 10*time.Second {
			t.Fatalf("compile took %v, want <= 10s (SC-004)", elapsed)
		}
		if compileErr != nil {
			// Compile-reject is an explicitly allowed outcome (MV-4).
			t.Logf("pattern rejected at compile time (allowed outcome): %v", compileErr)
			return
		}
		// Bounded completion is the other allowed outcome: prove a scan
		// over ordinary content actually returns promptly.
		scanStart := time.Now()
		line := []byte(strings.Repeat("x", 1000))
		_, _ = m.lineMatch(line)
		if elapsed := time.Since(scanStart); elapsed > 10*time.Second {
			t.Fatalf("lineMatch took %v after successful compile, want <= 10s", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("compile did not return within 10s (SC-004) — this is a hang, the one disallowed outcome")
	}
}

// TestFileGrep_PathologicalPattern64KiB_ViaSearch exercises the same bomb
// through the full Search entry point (a bad-pattern compile error is
// surfaced as a Go error, never a hang, matching Search's documented
// contract: "the error return is reserved for a bad pattern").
func TestFileGrep_PathologicalPattern64KiB_ViaSearch(t *testing.T) {
	var b strings.Builder
	for b.Len() < 64*1024 {
		b.WriteString("(a|b|c|d|e|f|g|h){0,3}")
	}
	pattern := b.String()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = Search(t.Context(), oneRoot(buildFS(map[string]string{"f.txt": "hello\n"})), Options{
			Query: pattern,
			Regex: true,
		})
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Search did not return within 10s on a pathological pattern")
	}
}
