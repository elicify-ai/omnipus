// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import (
	"regexp"
	"strings"
)

// assignmentRe matches a bare `VAR=` prefix, mirroring
// pkg/tools/shell_subst_guard.go::shellAssignmentNameRe's shape (this
// package cannot reference that unexported var — see doc.go — so it
// defines its own copy of the same, tiny, well-known POSIX identifier
// rule; it is not the segment/head reuse FR-020 requires).
var assignmentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// isAssignment reports whether tok is a leading `VAR=value` shell
// assignment.
func isAssignment(tok string) bool {
	return assignmentRe.MatchString(tok)
}

// tokenizeWords splits s into whitespace-separated words, honouring single-
// and double-quote pairing (quote characters are stripped from the
// resulting words) and a backslash escape outside single quotes. It is used
// only to recover the ARGUMENT words following an already-resolved head —
// never to find chain operators or the head itself, which is exclusively
// the injected HeadResolver's job (FR-020/FR-040). Unterminated quoting is
// treated as running to the end of the string (fail-open on tokenisation,
// same posture as the segment/head scanners this package wraps).
func tokenizeWords(s string) []string {
	var words []string
	var b strings.Builder
	var inSingle, inDouble bool
	flush := func() {
		if b.Len() > 0 {
			words = append(words, b.String())
			b.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && !inSingle && i+1 < len(s):
			i++
			b.WriteByte(s[i])
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case !inSingle && !inDouble && (c == ' ' || c == '\t' || c == '\n' || c == '\r'):
			flush()
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return words
}

// splitHeadArgs tokenises seg and skips leading `VAR=value` assignments,
// returning the token in head position (by this package's own, simpler
// tokeniser — used only to find where argument words begin, never as the
// authoritative head; see headAndArgsFor) and every word after it.
func splitHeadArgs(seg string) (headToken string, args []string, ok bool) {
	words := tokenizeWords(seg)
	i := 0
	for i < len(words) && isAssignment(words[i]) {
		i++
	}
	if i >= len(words) {
		return "", nil, false
	}
	return words[i], words[i+1:], true
}

// headAndArgsFor resolves seg's authoritative head via resolve (the
// injected HeadResolver) and, if that succeeds, aligns this package's own
// tokeniser to the same position to recover the argument words following
// it. Both skip leading assignments identically (the `VAR=value` shape),
// so the position lines up; a segment whose head is only reachable by
// skipping a shell keyword ("do", "then", and similar) is a case the
// injected resolver handles and this package's simpler tokeniser does not,
// so args may then include a stray keyword token. This is an accepted
// simplification: ArgPrefix matching and prefix suggestion are a friction
// layer over ordinary commands, not a shell-keyword-complete parser
// (ADR-092 D3's own "friction layer, not containment" framing).
func headAndArgsFor(seg string, resolve HeadResolver) (head string, args []string, ok bool) {
	h, reason := classifySegment(seg, resolve, false)
	if reason != blindNone {
		return "", nil, false
	}
	_, args, _ = splitHeadArgs(seg)
	return h, args, true
}
