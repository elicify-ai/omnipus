// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package policy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/policy"
)

// FirstToken (pkg/policy/evaluator.go ~line 111) had ZERO direct test
// references anywhere in the codebase before this addition — confirmed by
// grep. It is not purely cosmetic: within evaluator.go it only feeds a
// human-readable denial message, but pkg/security/execapproval.go's
// matchAllowlistPattern (~lines 250, 256) uses FirstToken as the ACTUAL
// match key for "always allow this binary" persisted-allowlist-pattern
// decisions (policy.FirstToken(pattern) == policy.FirstToken(command)), and
// PersistPattern's stored form depends on it too. These are pure
// characterization tests of FirstToken's EXISTING behavior — no behavior
// change. Gap identified in the whole-codebase Backend-High test-gap review
// (2026-07-07); there is no wave-spec BDD scenario for FirstToken in
// isolation, so these are GAP tests, not spec-traced ones.

// TestFirstToken_NormalMultiWordInput verifies the common case: the first
// space-separated token of a multi-word command is returned.
func TestFirstToken_NormalMultiWordInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "two words", input: "git status", want: "git"},
		{name: "three words", input: "npm run build", want: "npm"},
		{name: "many words", input: "git push origin main --force", want: "git"},
		{name: "multiple spaces between tokens", input: "git    status", want: "git"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, policy.FirstToken(tc.input))
		})
	}
}

// TestFirstToken_LeadingTrailingWhitespaceTrimmed verifies that surrounding
// whitespace (spaces, tabs, newlines) is trimmed before token extraction —
// FirstToken calls strings.TrimSpace(s) first, so leading whitespace must
// not become part of (or shift) the returned token, and trailing whitespace
// must not leak into it either.
func TestFirstToken_LeadingTrailingWhitespaceTrimmed(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "leading spaces", input: "   git status", want: "git"},
		{name: "trailing spaces", input: "git status   ", want: "git"},
		{name: "leading and trailing spaces", input: "  git status  ", want: "git"},
		{name: "tabs and newlines", input: "\t\ngit status\n", want: "git"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, policy.FirstToken(tc.input))
		})
	}
}

// TestFirstToken_SingleWordInput verifies that a single-word (no-argument)
// command returns the whole trimmed string, since there is no space to
// split on. This is the case that matters most for execapproval's binary
// allowance (e.g. "htop" with no args).
func TestFirstToken_SingleWordInput(t *testing.T) {
	assert.Equal(t, "htop", policy.FirstToken("htop"))
	assert.Equal(t, "ls", policy.FirstToken("ls"))
}

// TestFirstToken_SingleWordWithSurroundingWhitespace combines the single-word
// and whitespace-trimming cases: a lone token with leading/trailing
// whitespace must return just the trimmed token.
func TestFirstToken_SingleWordWithSurroundingWhitespace(t *testing.T) {
	assert.Equal(t, "htop", policy.FirstToken("  htop  "))
}

// TestFirstToken_EmptyString verifies the empty-string boundary: no token to
// extract, so the empty string is returned (not a panic, not "not found").
func TestFirstToken_EmptyString(t *testing.T) {
	assert.Equal(t, "", policy.FirstToken(""))
}

// TestFirstToken_AllWhitespaceString verifies that a string containing only
// whitespace trims down to empty before the space-search even runs, so the
// result is "" rather than a stray whitespace fragment.
func TestFirstToken_AllWhitespaceString(t *testing.T) {
	assert.Equal(t, "", policy.FirstToken("   "))
	assert.Equal(t, "", policy.FirstToken("\t\n\t"))
}
