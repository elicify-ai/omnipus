// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// thinking_level_zero_trace_test.go is the RED test for the D1 deletion of the
// superseded thinking_level mechanism (spec
// docs/internal/specs/thinking-reasoning-spec.md, Section 2.2 row
// "pkg/agent/loop_run_turn.go thinking-level gate": "the whole
// thinking_level/ThinkingLevel/parseThinkingLevel mechanism
// (pkg/config/config.go::ModelConfig.ThinkingLevel, pkg/agent/thinking.go)
// goes: superseded config is deleted outright, no alias kept (D1)"; Section 16
// test 17's thinking_level half). It is the sibling of
// reasoning_channel_zero_trace_test.go (WP-A's sweep, US-9) with a DIFFERENT
// token list — the two sweeps are separate files because each is a committed
// RED artifact of its own work package and must not rewrite the other's
// provenance; the spec's single "zero-trace sweep (reasoning channel AND
// thinking_level)" row is satisfied by both running in this package.
//
// The spec requires that after the deletion lands, a repository-wide sweep —
// excluding ONLY docs/internal/specs/ and docs/internal/_archive/ — finds:
//
//   - the Go identifier          (ThinkingLevel)
//   - the config/wire key        (thinking_level)
//   - the parse helper symbol    (parseThinkingLevel)
//
// ...zero hits across source, tests, configuration and docs alike. The
// deletion is deliberate and total (D1: deleted outright, no shim, no
// deprecation comment), so there is no legitimate carrier left for these
// tokens anywhere in the tracked tree except the enforcement surfaces
// themselves:
//
//   - this file (the sweep's own token table and failure message)
//
// This test was written and committed BEFORE the deletion (RED): against the
// pre-deletion tree it fails with the full list of remaining traces. It goes
// green only when the deletion lands. If it fails after a merge or rebase
// from an older branch, that branch predates D1 and re-added the mechanism as
// an ordinary, conflict-free addition — resolve by keeping the deletion.
package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// thinkingLevelBannedTokens is the token list from the spec's Section 2.2
// deletion row and Section 16 test 17: the Go identifier, the config/wire
// key, and the parse helper symbol. Matching is a plain substring check per
// line; "ThinkingLevel" also catches every compound identifier built on it
// (parseThinkingLevel, ThinkingLevelOff-style constants), and the dedicated
// parseThinkingLevel entry keeps the failure output self-explanatory.
var thinkingLevelBannedTokens = []string{
	"ThinkingLevel",
	"thinking_level",
	"parseThinkingLevel",
}

// thinkingLevelSkipDirs are excluded from the sweep. Per the spec the ONLY
// spec-mandated exclusions are the spec itself and the archived pre-ADR
// workspaces; the rest are build outputs, VCS state, and local tool indexes
// that are not part of the tracked source tree under review.
var thinkingLevelSkipDirs = map[string]bool{
	".git":                   true,
	".gitnexus":              true,
	"node_modules":           true,
	"dist":                   true,
	"docs/internal/specs":    true, // spec-mandated exclusion
	"docs/internal/_archive": true, // spec-mandated exclusion
	// The embedded SPA build output (gitignored, populated by a Vite build)
	// is not source under review; the pre-build stub is empty by design.
	"pkg/gateway/spa": true,
}

// thinkingLevelAllowedFiles are the enforcement surfaces that MUST carry the
// tokens (this sweep's own token table and failure message, and the wiring
// test that proves the deleted "thinking_level" key never reaches a provider
// request again) and are therefore excluded by exact path — never by
// directory, so a retired token re-added anywhere else in the same directory
// still fails.
var thinkingLevelAllowedFiles = map[string]bool{
	"pkg/config/thinking_level_zero_trace_test.go": true,
	"pkg/agent/reasoning_effort_llmopts_test.go":   true,
}

// thinkingLevelMinScannedFiles is the instrument check for this sweep: a walk
// that silently scans nothing would report zero traces forever (this repo's
// docs/internal/false-green-patterns.md records exactly that failure). The
// tracked tree carries thousands of text files; a scan of fewer than this
// floor means the walk is broken, not that the tree is clean.
const thinkingLevelMinScannedFiles = 500

// TestThinkingLevelZeroTraceSweep sweeps the tracked tree for the deleted
// thinking_level mechanism's tokens and fails with the full list of remaining
// traces. Traces to: Section 2.2 row "pkg/agent/loop_run_turn.go thinking-level
// gate"; Section 16 test 17 (thinking_level half); D1.
func TestThinkingLevelZeroTraceSweep(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	var matches []string
	scanned := 0

	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if thinkingLevelSkipDirs[rel] || thinkingLevelSkipDirs[d.Name()] {
				if rel != "." {
					return filepath.SkipDir
				}
				return nil
			}
			return nil
		}
		if thinkingLevelAllowedFiles[rel] {
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		if info.Size() > 2<<20 { // 2 MiB — not a plausible source file
			t.Logf("zero-trace sweep: skipped oversized file %s (%d bytes)", rel, info.Size())
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.IndexByte(data, 0) >= 0 {
			// Binary (images, packed assets) — the tokens cannot appear in
			// them as live config/code; skipped, and logged so the skip is
			// never silent.
			t.Logf("zero-trace sweep: skipped binary file %s", rel)
			return nil
		}

		scanned++
		for i, line := range strings.Split(string(data), "\n") {
			for _, tok := range thinkingLevelBannedTokens {
				if strings.Contains(line, tok) {
					matches = append(matches, rel+":"+itoaOneBased(i+1)+": "+tok)
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("BLOCKED: sweep walk failed (the result below is not a clean verdict): %v", walkErr)
	}

	if scanned < thinkingLevelMinScannedFiles {
		t.Fatalf("BLOCKED: the sweep scanned only %d files (floor %d) — the walk is broken, "+
			"and a guard that guards nothing passes forever. Fix the sweep before trusting any zero-trace verdict.",
			scanned, thinkingLevelMinScannedFiles)
	}

	if len(matches) > 0 {
		t.Fatalf("the deleted thinking_level mechanism still leaves %d trace(s) in the tree "+
			"(spec §2.2 deletion row, test 17, D1: the deletion must be total — resolve by keeping "+
			"the deletion, no shim, no deprecation comment):\n%s",
			len(matches), strings.Join(matches, "\n"))
	}
}
