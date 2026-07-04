// Package tools — shared shell command guard helpers.
//
// This file holds applyDenyPatterns/compileDenyPatterns, the deny-pattern
// matching helpers used by the bash tool (shell.go) for BOTH the hardcoded
// baseline (defaultDenyPatterns, defined in shell.go) and the opt-in
// operator-extensible layer (global + per-agent custom patterns). Prior to
// ADR-036 these were shared between the separate ExecTool and
// WorkspaceShellTool; the merge folded both call sites into shell.go alone.

package tools

import (
	"log/slog"
	"regexp"
)

// applyDenyPatterns checks command against a merged list of deny patterns.
// Returns a non-empty error string when the command should be blocked.
// When denyPatterns is empty the function always returns "".
//
// Pattern matching is case-insensitive (command is lowercased before matching).
// customAllowPatterns (per-agent allow overrides) are checked first; if any
// match the command the deny patterns are skipped entirely.
//
// Compile errors on patterns from the custom lists are logged at Warn and
// skipped — invalid patterns do not abort the call.
func applyDenyPatterns(command string, denyPatterns []*regexp.Regexp, customAllowPatterns []*regexp.Regexp) string {
	if len(denyPatterns) == 0 {
		return ""
	}

	lower := lowerASCII(command)

	// Custom allow patterns exempt the command from deny checks.
	for _, p := range customAllowPatterns {
		if p != nil && p.MatchString(lower) {
			return ""
		}
	}

	for _, p := range denyPatterns {
		if p != nil && p.MatchString(lower) {
			return "Command blocked by safety guard (dangerous pattern detected)"
		}
	}
	return ""
}

// compileDenyPatterns compiles a slice of raw regex strings into []*regexp.Regexp.
// Patterns that fail to compile are logged at Warn and skipped. The returned
// slice may be shorter than patterns if any were invalid.
func compileDenyPatterns(patterns []string, label string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, raw := range patterns {
		re, err := regexp.Compile(raw)
		if err != nil {
			slog.Warn("shell_guard: invalid deny pattern skipped",
				"pattern", raw, "label", label, "error", err)
			continue
		}
		out = append(out, re)
	}
	return out
}

// lowerASCII returns a copy of s with ASCII uppercase letters lowercased.
// Avoids importing strings just for ToLower at call sites that already import
// this package. Unicode-aware lowercasing is not needed here — shell commands
// are ASCII.
func lowerASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
