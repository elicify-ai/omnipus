// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Tests for the DENY-PATTERN layer of the bash guard (applyDenyPatterns over
// defaultDenyPatterns) — layer 1 of the three ADR-068 identified.
//
// These exist because of a bug that survived the entire life of the codebase
// unnoticed: defaultDenyPatterns carried `<<\s*EOF`, and applyDenyPatterns
// lowercases the command before matching (lowerASCII, shell_guard.go:32). An
// uppercase literal cannot match a lowercased string, so the rule blocked
// nothing, ever. Nobody noticed because no test in pkg/tools covered heredocs
// at all — the guard looked present in the source and was absent in behaviour.
//
// ADR-068 §3 ruled: delete the pattern rather than make it fire, because making
// it fire would newly block heredoc writes that work today. The two tests below
// pin both halves of that ruling:
//
//   - TestDefaultDenyPatterns_HeredocsArePermitted pins the BEHAVIOUR, so the
//     deleted rule cannot be quietly reinstated.
//   - TestDefaultDenyPatterns_ContainNoUppercaseOnlyLiterals pins the BUG
//     CLASS, so no future pattern can be added that is unreachable for the same
//     reason. This is the test that would have caught the original defect.

import (
	"regexp/syntax"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDefaultDenyPatterns_HeredocsArePermitted asserts that the deny-pattern
// layer allows heredocs.
//
// Oracle (ADR-068 §3, not the code): a heredoc is an ordinary way to write a
// file and has always worked in practice. The blocked write shapes UAT defect
// 003 reported were blocked by the PATH-CONTAINMENT scan, not by this layer —
// so this layer must keep saying yes to all of them.
func TestDefaultDenyPatterns_HeredocsArePermitted(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{
			name: "uppercase EOF delimiter",
			cmd:  "cat > notes.md << EOF\nhello\nEOF",
		},
		{
			name: "uppercase EOF with no space after the operator",
			cmd:  "cat > notes.md <<EOF\nhello\nEOF",
		},
		{
			name: "lowercase eof delimiter",
			cmd:  "cat > notes.md << eof\nhello\neof",
		},
		{
			name: "quoted delimiter suppressing expansion",
			cmd:  "cat > notes.md << 'EOF'\nhello\nEOF",
		},
		{
			name: "indented heredoc",
			cmd:  "cat > notes.md <<- EOF\n\thello\n\tEOF",
		},
		{
			name: "non-EOF delimiter",
			cmd:  "cat > notes.md << MARKER\nhello\nMARKER",
		},
		{
			name: "here-string",
			cmd:  `cat <<< "hello"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := applyDenyPatterns(tc.cmd, defaultDenyPatterns, nil)
			require.Empty(t, got,
				"the deny-pattern layer must permit heredocs (ADR-068 §3 deleted `<<\\s*EOF`; do not restore it)\ncommand: %q", tc.cmd)
		})
	}
}

// TestDefaultDenyPatterns_ContainNoUppercaseOnlyLiterals is the bug-class
// guard. applyDenyPatterns matches against a LOWERCASED command, so any part of
// a pattern that can only match uppercase input is dead code that looks like a
// security rule.
//
// It parses each pattern with regexp/syntax rather than scanning the source
// text, because a naive "contains an uppercase letter" check would flag every
// legitimate `[A-Za-z]` and `\w` class in the list. The precise property is:
//
//   - no LITERAL uppercase ASCII rune anywhere in the pattern; and
//   - no character class that admits uppercase ASCII but no lowercase ASCII
//     (a bare `[A-Z]`), which is the class-shaped version of the same defect.
//
// `[A-Za-z]`, `\w` and negated classes like `[^}]` all admit lowercase too, so
// they match lowercased input perfectly well and are not flagged.
//
// If this test fails on a pattern other than one you just added, do NOT
// "fix" the pattern by lowercasing it: that would newly BLOCK commands that
// run today, which is a behaviour change requiring its own decision (this is
// exactly the reasoning ADR-068 §3 applied to `<<\s*EOF`). Report it.
func TestDefaultDenyPatterns_ContainNoUppercaseOnlyLiterals(t *testing.T) {
	for _, p := range defaultDenyPatterns {
		require.NotNil(t, p, "nil pattern in defaultDenyPatterns")
		src := p.String()
		t.Run(src, func(t *testing.T) {
			parsed, err := syntax.Parse(src, syntax.Perl)
			require.NoError(t, err, "deny pattern must be parseable: %s", src)

			reasons := uppercaseOnlyParts(parsed)
			require.Empty(t, reasons,
				"deny pattern %q can only match UPPERCASE input, but applyDenyPatterns lowercases the "+
					"command before matching (lowerASCII, shell_guard.go) — so this rule is unreachable "+
					"and blocks nothing. Offending parts: %v", src, reasons)
		})
	}
}

// uppercaseOnlyParts walks a parsed regexp and returns a description of every
// sub-expression that can match uppercase ASCII but not its lowercase form.
func uppercaseOnlyParts(re *syntax.Regexp) []string {
	var found []string
	var walk func(r *syntax.Regexp)
	walk = func(r *syntax.Regexp) {
		if r == nil {
			return
		}
		switch r.Op {
		case syntax.OpLiteral:
			for _, ru := range r.Rune {
				if ru >= 'A' && ru <= 'Z' {
					found = append(found, "literal "+string(ru))
				}
			}
		case syntax.OpCharClass:
			admitsUpper, admitsLower := false, false
			for i := 0; i+1 < len(r.Rune); i += 2 {
				lo, hi := r.Rune[i], r.Rune[i+1]
				if lo <= 'Z' && hi >= 'A' {
					admitsUpper = true
				}
				if lo <= 'z' && hi >= 'a' {
					admitsLower = true
				}
			}
			if admitsUpper && !admitsLower {
				found = append(found, "uppercase-only character class")
			}
		}
		for _, sub := range r.Sub {
			walk(sub)
		}
	}
	walk(re)
	return found
}
