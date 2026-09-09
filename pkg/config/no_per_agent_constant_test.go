// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file holds the STRUCTURAL half of the one-memory-mechanism work: the
// assertions that the deleted symbols are actually gone from the whole
// repository, not merely unreferenced from the paths the behavioural tests
// happen to exercise.
//
// A deletion that leaves the symbol behind is not a deletion. The failure mode
// is specific and has happened before in this codebase: a helper survives
// unreferenced, a later change finds it and reasonably assumes it is live, and
// the second mechanism is back with no commit that looks like it reintroduced
// anything. Every behavioural test can be green while that happens.

// repoRoot walks up from this package's directory until it finds go.mod.
//
// A Go test's working directory is its own package directory, so a test that
// needs to read repository files has to locate the root itself. This is the
// agreed seam for that, used by every repo-wide assertion in this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate the repository root (no go.mod found walking up from the package directory)")
	return ""
}

// scanRepoGoFiles calls visit for every .go file in the repository, TEST FILES
// INCLUDED, skipping only vendored and generated trees.
//
// Test files are deliberately in scope. A deleted symbol that survives only in
// a test still compiles, still reads as live code to the next person, and is
// the most likely place for a resurrection to start.
func scanRepoGoFiles(t *testing.T, visit func(path string, content string)) {
	t.Helper()
	root := repoRoot(t)
	skipDirs := map[string]bool{
		"node_modules": true,
		".git":         true,
		"dist":         true,
		"vendor":       true,
		"spa":          true,
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Generated artifacts are regenerated from contracts and are not a
		// place a hand-written constant can hide.
		if strings.Contains(path, string(filepath.Separator)+"generated"+string(filepath.Separator)) {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		visit(rel, string(content))
		return nil
	})
	if err != nil {
		t.Fatalf("walk repository: %v", err)
	}
}

// TestNoResidualPerAgentConstant asserts, repo-wide and including test files,
// that the computed-default machinery is gone as SYMBOLS.
//
// bytesPerAgent is the load-bearing one. It was the per-agent memory cost
// (3.5 MB) that made agent concurrency a SECOND memory mechanism: it sized a
// cap once from a constant while the browser pool sized itself live from the
// same host's real headroom. Deleting the formula but keeping the constant
// leaves the second mechanism one line away from returning.
func TestNoResidualPerAgentConstant(t *testing.T) {
	// Each entry is a symbol that must not appear anywhere, with the reason,
	// so a failure explains itself to whoever hits it rather than just naming
	// a string.
	banned := map[string]string{
		"bytesPerAgent":           "the per-agent memory cost constant — its existence is what made agent concurrency a second, disagreeing memory mechanism",
		"autoDetectMaxParallel":   "the computed-default function; there is no longer a computed default",
		"clampParallel(":          "the auto-detect clamp; only clampParallelExplicit survives, and it governs the explicit path only",
		"autoDetectFloorParallel": "the auto-detect floor; concurrency is bounded by live memory now, not by a floor on a precomputed number",
	}

	var findings []string
	scanRepoGoFiles(t, func(path, content string) {
		// This file names every banned symbol in its own table and doc text.
		if strings.HasSuffix(path, "no_per_agent_constant_test.go") {
			return
		}
		for symbol, why := range banned {
			if strings.Contains(content, symbol) {
				findings = append(findings, path+" still contains "+symbol+" — "+why)
			}
		}
	})

	for _, f := range findings {
		t.Error(f)
	}
	if len(findings) > 0 {
		t.Fatalf("%d residual reference(s) to the deleted computed-default machinery", len(findings))
	}
}

// TestNoSymbol_FallbackTotalRAMBytes asserts the fabricated 4 GB constant is
// gone repo-wide, test files included.
//
// This one is worth its own test because the constant was not merely unused —
// it was ACTIVELY WRONG in a way nothing could see. On a Linux host with an
// unreadable /proc/meminfo (gVisor, distroless, a hardened seccomp profile) the
// process reported 2 GiB of available memory that had measured nothing, and
// every consumer downstream treated it as a reading. A test asserting the
// fabricated value even existed as a name is a test that keeps it reachable.
func TestNoSymbol_FallbackTotalRAMBytes(t *testing.T) {
	var findings []string
	scanRepoGoFiles(t, func(path, content string) {
		if strings.HasSuffix(path, "no_per_agent_constant_test.go") {
			return
		}
		if strings.Contains(content, "fallbackTotalRAMBytes") {
			findings = append(findings, path)
		}
	})

	if len(findings) > 0 {
		t.Fatalf("fallbackTotalRAMBytes still exists in %v — an unreadable /proc/meminfo must report undeterminable, never a fabricated 4 GB. Any surviving reference is a route back to reporting memory this host does not have.", findings)
	}
}

// TestOneMemoryThreshold asserts there is exactly ONE memory-pressure
// threshold constant in the codebase.
//
// This is the property the whole change is FOR. Two mechanisms disagreeing
// about one machine is not a bug in either of them — each is individually
// defensible — so it cannot be caught by testing either one. It can only be
// caught structurally, by asserting the second one does not exist.
//
// The match is on the DECLARED NAME (anything containing "memorypressure",
// case-insensitively), not on the literal 0.85. Matching the value was the
// first attempt and it was wrong: it flagged
// pkg/memrooms/minhash.DefaultThreshold, a MinHash similarity cutoff that
// happens to share the number and has nothing to do with memory. A structural
// test that fires on coincidence gets weakened or deleted by the next person
// who hits it, which costs more than it ever caught.
func TestOneMemoryThreshold(t *testing.T) {
	var declarations []string
	scanRepoGoFiles(t, func(path, content string) {
		if strings.HasSuffix(path, "no_per_agent_constant_test.go") {
			return
		}
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			if !strings.Contains(strings.ToLower(trimmed), "memorypressure") {
				continue
			}
			// A declaration assigns a numeric literal; a USE does not.
			if !strings.Contains(trimmed, "=") || strings.Contains(trimmed, "==") {
				continue
			}
			rhs := trimmed[strings.Index(trimmed, "=")+1:]
			if !strings.Contains(rhs, "0.") {
				continue
			}
			declarations = append(declarations, path+": "+trimmed)
		}
	})

	if len(declarations) != 1 {
		t.Fatalf("expected exactly ONE memory-pressure threshold declaration in the repository, found %d:\n  %s\n\nEvery admission consumer must compare against the same number through config.MemoryPressureHigh. A second threshold constant re-creates the exact defect this work removes: two mechanisms, each defensible alone, disagreeing about one machine.",
			len(declarations), strings.Join(declarations, "\n  "))
	}
}
