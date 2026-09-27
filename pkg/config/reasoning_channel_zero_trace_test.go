// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// reasoning_channel_zero_trace_test.go is the RED test for the D1 deletion of
// the messenger reasoning channel (spec
// docs/internal/specs/thinking-reasoning-spec.md, US-9 acceptance scenarios
// 1-2, Group F scenario "The messenger reasoning channel leaves no trace").
//
// The spec requires that after the deletion lands, a repository-wide sweep —
// excluding ONLY docs/internal/specs/ and docs/internal/_archive/ — finds:
//
//   - the reasoning-channel config key  (reasoning_channel_id)
//   - its per-channel env var names     (OMNIPUS_CHANNELS_*_REASONING_CHANNEL_ID)
//   - the publish path symbols          (handleReasoning / spawnReasoningPublish)
//   - the Go identifier                 (ReasoningChannelID)
//
// ...zero hits across source, configuration schemas, the shipped config files,
// environment-variable bindings and docs alike. The deletion is deliberate and
// total (D1: deleted outright, no shim, no deprecation comment), so there is
// no legitimate carrier left for these tokens anywhere in the tracked tree
// except the enforcement surfaces themselves:
//
//   - this file (the sweep's own token table and failure message)
//   - scripts/check-no-reasoning-channel.sh        (the guard that enforces
//     the same absence in CI via scripts/guards.sh)
//   - scripts/check-no-reasoning-channel-selfcheck.sh
//     (the guard's proof-of-failure companion)
//
// This test was written and committed BEFORE the deletion (RED): against the
// pre-deletion tree it fails with the full list of remaining traces. It goes
// green only when the deletion lands. If it fails after a merge or rebase
// from an older branch, that branch predates D1 and re-added the machinery as
// an ordinary, conflict-free addition — resolve by keeping the deletion.
package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reasoningChannelBannedTokens is the token list from the spec's US-9
// acceptance scenario 2 and Group F scenario 1: the config key, its env var
// spelling, the publish-path symbols, and the Go identifier.
var reasoningChannelBannedTokens = []string{
	"ReasoningChannelID",
	"reasoning_channel_id",
	"REASONING_CHANNEL_ID",
	"spawnReasoningPublish",
	"handleReasoning",
}

// reasoningChannelSkipDirs are excluded from the sweep. Per the spec the ONLY
// spec-mandated exclusions are the spec itself and the archived pre-ADR
// workspaces; the rest are build outputs, VCS state, and local tool indexes
// that are not part of the tracked source tree under review.
var reasoningChannelSkipDirs = map[string]bool{
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

// reasoningChannelAllowedFiles are the enforcement surfaces that MUST carry
// the tokens (the sweep list, the guard, its selfcheck) and are therefore
// excluded by exact path — never by directory, so a retired token re-added
// anywhere else in the same directory still fails.
var reasoningChannelAllowedFiles = map[string]bool{
	"pkg/config/reasoning_channel_zero_trace_test.go": true,
	"scripts/check-no-reasoning-channel.sh":           true,
	"scripts/check-no-reasoning-channel-selfcheck.sh": true,
}

// reasoningChannelMinScannedFiles is the instrument check for this sweep: a
// walk that silently scans nothing would report zero traces forever (this
// repo's docs/internal/false-green-patterns.md records exactly that failure —
// a guard passing 673/673 with the feature it guarded already deleted). The
// tracked tree carries thousands of text files; a scan of fewer than this
// floor means the walk is broken, not that the tree is clean.
const reasoningChannelMinScannedFiles = 500

// TestReasoningChannelZeroTraceSweep sweeps the tracked tree for the deleted
// messenger reasoning channel's tokens and fails with the full list of
// remaining traces. Traces to: US-9 acceptance scenarios 1-2; Group F
// scenario "The messenger reasoning channel leaves no trace"; D1.
func TestReasoningChannelZeroTraceSweep(t *testing.T) {
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
			if reasoningChannelSkipDirs[rel] || reasoningChannelSkipDirs[d.Name()] {
				if rel != "." {
					return filepath.SkipDir
				}
				return nil
			}
			return nil
		}
		if reasoningChannelAllowedFiles[rel] {
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
			for _, tok := range reasoningChannelBannedTokens {
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

	if scanned < reasoningChannelMinScannedFiles {
		t.Fatalf("BLOCKED: the sweep scanned only %d files (floor %d) — the walk is broken, "+
			"and a guard that guards nothing passes forever. Fix the sweep before trusting any zero-trace verdict.",
			scanned, reasoningChannelMinScannedFiles)
	}

	if len(matches) > 0 {
		t.Fatalf("the messenger reasoning channel still leaves %d trace(s) in the tree "+
			"(US-9 AS1-2, Group F: the deletion must be total — resolve by keeping the deletion, "+
			"no shim, no deprecation comment):\n%s",
			len(matches), strings.Join(matches, "\n"))
	}
}

// itoaOneBased is a tiny allocation-free line-number formatter so the sweep
// does not import strconv for one call.
func itoaOneBased(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
