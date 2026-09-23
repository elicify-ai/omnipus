// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import "strings"

// wrapperTokens are the command wrappers ADR-092 D4/FR-026 names by name:
// the suggested prefix is derived STARTING FROM the wrapper, never from
// what it executes. Per the spec's pinned reading, the prefix stops at the
// wrapper's first argument — for a bare single-word wrapper that means the
// prefix IS just the wrapper word; "sh -c" is the one two-word wrapper
// (the flag is part of identifying the wrapper, not "its first argument").
var wrapperTokens = map[string]bool{
	"sudo":    true,
	"env":     true,
	"timeout": true,
	"xargs":   true,
}

// SuggestedPrefix implements ADR-092 D4/FR-026's suggested-prefix
// algorithm for the approval dialog's "Allow" scope=prefix option:
// resolved binary plus leading sub-command words, stopping at the first
// flag-, path-, or URL-shaped token. A leading env-assignment is stripped
// before deriving. A known wrapper is not stripped; the prefix is derived
// starting from the wrapper. Returns ("", false) when no prefix can be
// suggested: on Windows (FR-041 — no prefix option at all), or when
// command is a known blind spot (unbalanced quote, brace expansion, no
// resolvable head — the same posture as FR-020's segment-matching blind
// spots: never offer a prefix built from a head we could not confidently
// resolve).
func SuggestedPrefix(command string, platform Platform, resolve HeadResolver) (string, bool) {
	if platform == WindowsPlatform {
		return "", false
	}
	head, args, ok := headAndArgsFor(command, resolve)
	if !ok {
		return "", false
	}

	if head == "sh" && len(args) > 0 && args[0] == "-c" {
		return "sh -c", true
	}
	if wrapperTokens[head] {
		return head, true
	}

	words := []string{head}
	for _, w := range args {
		if isFlagShaped(w) || isPathShaped(w) || isURLShaped(w) {
			break
		}
		words = append(words, w)
	}
	return strings.Join(words, " "), true
}

// isFlagShaped reports whether tok looks like a CLI flag (`-m`,
// `--verbose`), the algorithm's first stop-token class.
func isFlagShaped(tok string) bool {
	return strings.HasPrefix(tok, "-")
}

// isURLShaped reports whether tok contains a URL scheme separator, the
// algorithm's third stop-token class.
func isURLShaped(tok string) bool {
	return strings.Contains(tok, "://")
}
