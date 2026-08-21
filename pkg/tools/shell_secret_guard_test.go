// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Adversarial-review finding #3: the shell text-guard's secrets-subtree
// backstop (v0.2 #155 item 8) had literal regexes for "master.key" and
// "credentials.json" only. It never gained cli.token, config.json, or
// entities — the three names fspolicy.SecretEntriesAlways added afterward.
// The kernel sandbox deny is the real boundary; this guard is a backstop over
// it, which is exactly why its coverage silently falling behind was invisible
// — every test that exercises the actual boundary (the kernel deny) kept
// passing regardless of what this list contained.
//
// shell.go now generates secretGuardPatterns FROM fspolicy.SecretEntriesAlways
// instead of hand-copying names, which is what makes the coverage below hold
// structurally rather than by discipline. This file is the regression that
// couples the two: it fails the moment SecretEntriesAlways gains an entry the
// guard cannot already reach — possible only if that generation is ever
// reverted to a hand-copied literal list, which is precisely how the original
// two-line version fell two (now five) entries behind.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// TestSecretGuardPatterns_CoverEverySecretEntryAlways is the coupling test.
// For every name in fspolicy.SecretEntriesAlways, a command that merely
// MENTIONS the name (no path, no special syntax — the guard is a literal-text
// backstop, not a path resolver) must be blocked by defaultDenyPatterns, and
// an ordinary command that mentions none of them must not be.
func TestSecretGuardPatterns_CoverEverySecretEntryAlways(t *testing.T) {
	if len(fspolicy.SecretEntriesAlways) == 0 {
		t.Fatal("fspolicy.SecretEntriesAlways is empty — this test would pass vacuously and prove nothing")
	}

	for _, name := range fspolicy.SecretEntriesAlways {
		t.Run(name, func(t *testing.T) {
			command := "cat " + name
			if reason := applyDenyPatterns(command, defaultDenyPatterns, nil); reason == "" {
				t.Errorf("shell guard does not cover secret-set entry %q: command %q was NOT blocked.\n"+
					"fspolicy.SecretEntriesAlways gained an entry secretGuardPatterns cannot reach — "+
					"see buildSecretGuardPatterns in shell.go.", name, command)
			}
		})
	}
}

// TestSecretGuardPatterns_OrdinaryCommandsUnaffected is the false-positive
// guard for the coupling test above: proving the generated patterns cover the
// secret set is only meaningful if they do NOT also block everything else.
func TestSecretGuardPatterns_OrdinaryCommandsUnaffected(t *testing.T) {
	for _, command := range []string{
		"ls -la",
		"cat notes.txt",
		"git status",
		"npm run build",
		"echo hello",
	} {
		if reason := applyDenyPatterns(command, defaultDenyPatterns, nil); reason != "" {
			t.Errorf("ordinary command %q was blocked (%q) — the secret-set guard is over-broad", command, reason)
		}
	}
}

// TestSecretGuardPatterns_CaseInsensitive matches applyDenyPatterns' own
// contract (it lowercases the command before matching every deny pattern,
// shell_guard.go) — an UPPERCASE or MixedCase mention must be caught exactly
// like the lowercase form the secret set is spelled in.
func TestSecretGuardPatterns_CaseInsensitive(t *testing.T) {
	for _, name := range fspolicy.SecretEntriesAlways {
		upper := strings.ToUpper(name)
		command := "cat " + upper
		if reason := applyDenyPatterns(command, defaultDenyPatterns, nil); reason == "" {
			t.Errorf("shell guard does not cover uppercase spelling of %q: command %q was NOT blocked", name, command)
		}
	}
}

// TestSecretGuardPatterns_GeneratedFromLiveSecretSet is a narrower, more
// direct pin on the mechanism itself (not just the outcome above): every
// compiled pattern in secretGuardPatterns must actually originate from the
// CURRENT fspolicy.SecretEntriesAlways, one-for-one. This is what a reviewer
// checking "did this get hand-copied again" should be able to run.
func TestSecretGuardPatterns_GeneratedFromLiveSecretSet(t *testing.T) {
	if len(secretGuardPatterns) != len(fspolicy.SecretEntriesAlways) {
		t.Fatalf("secretGuardPatterns has %d entries, fspolicy.SecretEntriesAlways has %d — "+
			"they must be generated 1:1; a hand-copied list would drift in size the moment "+
			"the secret set changes without a matching edit here",
			len(secretGuardPatterns), len(fspolicy.SecretEntriesAlways))
	}
	for i, name := range fspolicy.SecretEntriesAlways {
		want := `\b` + regexp.QuoteMeta(strings.ToLower(name)) + `\b`
		got := secretGuardPatterns[i].String()
		if got != want {
			t.Errorf("secretGuardPatterns[%d] = %q, want %q (generated from %q)", i, got, want, name)
		}
	}
}
