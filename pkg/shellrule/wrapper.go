// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file implements the R3 security-review fix (CRITICAL, 2026-09-24
// founder decision: "look through wrappers and fail closed"): before this
// fix, evaluateSegment resolved exactly ONE head per segment via the
// injected HeadResolver, which only skips shell SYNTAX (keywords,
// `VAR=value` assignments) — never a command WRAPPER. `env rm -rf x`,
// `sudo rm -rf x`, `nice -n 10 rm -rf x`, `timeout 5 rm -rf x`, `command rm
// -rf x` and `xargs rm` all resolved a head of the WRAPPER ("env", "sudo",
// "nice", "timeout", "command", "xargs"), which no operator deny rule keyed
// on "rm" could ever match — Action=ActionNone, ceiling governs, meaning an
// `allow` ceiling (Auto mode's own definition, ADR-092 D1) ran the denied
// command unprompted. This held even under God Mode, which contradicts
// ADR-092 D1/D3's own text ("Operator deny rules stay in force in God
// Mode").
//
// unwrapWrappers below is the SINGLE canonical definition of a "command
// wrapper" this package uses for that fix. IsWrapperCommand exposes the same
// set to SuggestedPrefix (prefix.go, ADR-092 D4/FR-026's suggested-prefix
// algorithm) and pkg/tools/shell_permission_mode.go's D4 prefix-grant
// exclusion, so there is one wrapper list, not three that can drift — the
// two pre-existing call sites (prefix.go's now-removed wrapperTokens,
// shell_permission_mode.go's prefixGrantWrappers) each held their own
// hand-maintained subset of this same concept.
package shellrule

import (
	"regexp"
	"strings"
)

// wrapperSpec describes how to skip PAST one wrapper's own flags/positional
// argument to reach the real command it execs.
type wrapperSpec struct {
	// valueFlags are flag names (with leading dashes stripped, long or
	// short) that consume the FOLLOWING token as their value when not
	// attached via `=` (`-u root` / `--user root`) or, for a single-dash
	// short flag, glued directly onto the flag (`-n10`, left alone —
	// already self-contained, no extra token consumed).
	valueFlags map[string]bool
	// allowsAssignments is true for wrappers (env) whose leading
	// `VAR=value` tokens are themselves consumed before the real command.
	allowsAssignments bool
	// leadingPositional is true for a wrapper (timeout) whose first
	// non-flag token is a REQUIRED value (the duration), not the command.
	leadingPositional bool
}

// wrapperSpecs is ADR-092 R3's canonical wrapper list (founder decision,
// 2026-09-24): "Known wrappers ... env (with -i/-u X/VAR=v args), sudo/doas
// (with flags), nice/ionice (with -n N), nohup, setsid, timeout (with a
// duration), stdbuf, time, command, builtin, exec, and xargs (the command
// after its flags)." A wrapper NOT in this list is a documented blind spot
// (FR-020's own posture, extended here) — see findLaterArgDenyMatch for the
// residual safety net that covers it partially.
var wrapperSpecs = map[string]wrapperSpec{
	"env": {
		allowsAssignments: true,
		valueFlags:        map[string]bool{"u": true, "unset": true, "C": true, "chdir": true, "S": true, "split-string": true},
	},
	"sudo": {
		valueFlags: map[string]bool{
			"u": true, "user": true, "g": true, "group": true, "p": true, "prompt": true,
			"h": true, "host": true, "C": true, "close-from": true, "U": true, "other-user": true,
			"T": true, "command-timeout": true, "R": true, "chroot": true, "D": true, "chdir": true,
			"r": true, "role": true, "t": true, "type": true,
		},
	},
	"doas": {valueFlags: map[string]bool{"u": true, "C": true}},
	"nice": {valueFlags: map[string]bool{"n": true, "adjustment": true}},
	"ionice": {
		valueFlags: map[string]bool{"c": true, "class": true, "n": true, "classdata": true, "p": true, "pid": true, "t": true},
	},
	"nohup":   {},
	"setsid":  {},
	"timeout": {valueFlags: map[string]bool{"s": true, "signal": true, "k": true, "kill-after": true}, leadingPositional: true},
	"stdbuf":  {valueFlags: map[string]bool{"i": true, "input": true, "o": true, "output": true, "e": true, "error": true}},
	"time":    {valueFlags: map[string]bool{"o": true, "output": true}},
	"command": {},
	"builtin": {},
	"exec":    {valueFlags: map[string]bool{"a": true}},
	"xargs": {
		valueFlags: map[string]bool{
			"I": true, "i": true, "n": true, "max-args": true, "P": true, "max-procs": true,
			"L": true, "l": true, "max-lines": true, "d": true, "delimiter": true, "s": true,
			"max-chars": true, "a": true, "arg-file": true, "E": true, "eof-str": true,
			"process-slot-var": true,
		},
	},
}

// IsWrapperCommand reports whether head (already normalised — lowercase,
// directory prefix stripped, as HeadResolver/classifySegment returns it)
// names a known command wrapper. Exported so prefix.go's SuggestedPrefix and
// pkg/tools/shell_permission_mode.go's BashPrefixGrantFor share this
// package's ONE wrapper definition instead of each keeping their own list.
func IsWrapperCommand(head string) bool {
	_, ok := wrapperSpecs[head]
	return ok
}

// flagKeyName strips a flag token's leading dash(es) and any `=value`
// suffix, returning the bare flag name to look up in a wrapperSpec's
// valueFlags.
func flagKeyName(tok string) string {
	trimmed := strings.TrimLeft(tok, "-")
	if eq := strings.IndexByte(trimmed, '='); eq >= 0 {
		return trimmed[:eq]
	}
	return trimmed
}

// skipWrapperArgs walks args past spec's own flags/assignments/leading
// positional to the argument words that name the REAL command it execs.
// Returns ok=false when nothing is left after the wrapper's own arguments
// (`sudo` alone, `env -i`) — a wrapper with no command to run is a blind
// spot, never silently treated as though the wrapper itself were the real
// command.
func skipWrapperArgs(spec wrapperSpec, args []string) (rest []string, ok bool) {
	i := 0
	positionalTaken := !spec.leadingPositional
	for i < len(args) {
		tok := args[i]
		if tok == "--" {
			i++
			continue
		}
		if spec.allowsAssignments && isAssignment(tok) {
			i++
			continue
		}
		if strings.HasPrefix(tok, "-") && tok != "-" {
			key := flagKeyName(tok)
			attachedValue := strings.ContainsRune(tok, '=')
			i++
			if spec.valueFlags[key] && !attachedValue && i < len(args) {
				i++
			}
			continue
		}
		if !positionalTaken {
			positionalTaken = true
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return nil, false
	}
	return args[i:], true
}

// maxWrapperDepth bounds unwrapWrappers' recursion (`env sudo nice timeout
// ... rm`) against a pathological or hostile chain — fail closed rather than
// loop unboundedly.
const maxWrapperDepth = 8

// unwrapWrappers follows a chain of known command wrappers (see
// wrapperSpecs) to the command a D3 rule must actually be evaluated
// against, recursively — `env sudo rm -rf x` unwraps twice, to `rm -rf x`.
//
// seg/head/args are the ALREADY-CLASSIFIED (via classifySegment) head and
// (via splitHeadArgs) argument words of the segment being evaluated.
// Returns the innermost segment TEXT (reconstructed from the remaining
// argument words — this package's own documented "friction layer, not a
// shell-keyword-complete parser" simplification, matching headAndArgsFor's
// identical trade-off), its head and argument words, whether any unwrapping
// happened at all, and — when a wrapper's own head resolves via
// classifySegment to a blind spot, or leaves no command at all — the blind
// verdict FR-020 requires for every other unresolvable shape in this
// package.
func unwrapWrappers(seg, head string, args []string, resolve HeadResolver) (finalSeg, finalHead string, finalArgs []string, wrapped bool, blind bool, blindReason blindSpotReason) {
	curSeg, curHead, curArgs := seg, head, args
	for depth := 0; depth < maxWrapperDepth; depth++ {
		spec, known := wrapperSpecs[curHead]
		if !known {
			return curSeg, curHead, curArgs, wrapped, false, blindNone
		}
		rest, found := skipWrapperArgs(spec, curArgs)
		if !found {
			return "", "", nil, true, true, blindNoResolvableHead
		}
		wrapped = true
		innerSeg := strings.Join(rest, " ")
		nextHead, reason := classifySegment(innerSeg, resolve, false)
		if reason != blindNone {
			return "", "", nil, true, true, reason
		}
		_, nextArgs, _ := splitHeadArgs(innerSeg)
		curSeg, curHead, curArgs = innerSeg, nextHead, nextArgs
	}
	// Depth exhausted (pathological/hostile wrapper chain) — fail closed
	// rather than trust an unbounded chain or loop forever.
	return "", "", nil, true, true, blindNoResolvableHead
}

// interpreterHeads names the ADR-092 R3 "interpreter or eval form" set
// (founder decision, 2026-09-24) whose real, executed command the D3 rule
// engine cannot see at all: bash/sh/zsh only in their `-c SCRIPT` shape,
// perl/ruby/node only in their `-e SCRIPT` shape, and eval/source (which
// take the rest of the line as their script/file with no such flag).
// python*/python2/python3/... is matched by prefix, not exact membership —
// see detectInterpreterForm.
var interpreterHeads = map[string]bool{
	"bash": true, "sh": true, "zsh": true,
	"eval": true, "source": true,
	"perl": true, "ruby": true, "node": true,
}

// detectInterpreterForm reports whether (head, args) is one of the R3
// interpreter/eval shapes, and if so returns the literal text of the
// argument the shell will actually execute — the ONE thing
// findDenyBinaryWordIn/hasAskOrDenyRules can inspect, since no head
// resolution can ever recover the real command from it.
func detectInterpreterForm(head string, args []string) (scriptText string, ok bool) {
	switch {
	case head == "bash" || head == "sh" || head == "zsh":
		if len(args) >= 2 && args[0] == "-c" {
			return strings.Join(args[1:], " "), true
		}
		return "", false
	case head == "eval" || head == "source":
		if len(args) >= 1 {
			return strings.Join(args, " "), true
		}
		return "", false
	case head == "perl" || head == "ruby" || head == "node":
		if len(args) >= 2 && args[0] == "-e" {
			return strings.Join(args[1:], " "), true
		}
		return "", false
	case strings.HasPrefix(head, "python"):
		if len(args) >= 2 && args[0] == "-c" {
			return strings.Join(args[1:], " "), true
		}
		return "", false
	default:
		return "", false
	}
}

// wordBoundaryRe caches one compiled `\bWORD\b` matcher per distinct word —
// interpreter/eval segments are rare relative to ordinary commands, and the
// rule set this runs against is small (operator-authored command_rules),
// so this is not a hot-path concern.
func containsWordBoundary(haystack, word string) bool {
	if word == "" {
		return false
	}
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(word) + `\b`)
	return re.MatchString(haystack)
}

// denyRuleBaseName reduces a DENY rule's Binary field (a bare name or an
// absolute path — see Rule's own doc comment) to the bare word
// findDenyBinaryWordIn scans an interpreter's script text for, lower-cased
// to match containsWordBoundary's case-insensitive search.
func denyRuleBaseName(binary string) string {
	if idx := strings.LastIndexByte(binary, '/'); idx >= 0 {
		binary = binary[idx+1:]
	}
	return strings.ToLower(binary)
}

// findDenyBinaryWordIn implements the R3 interpreter fail-closed rule
// (founder decision, 2026-09-24): "if ANY deny rule exists whose binary
// appears as a word inside the interpreter's argument string, treat it as a
// deny match." Returns the matched rule so the caller can report an
// explainable SEC-17-style denial (which rule, which word).
func findDenyBinaryWordIn(scriptText string, rules []Rule) (*Rule, bool) {
	for i := range rules {
		r := rules[i]
		if r.Action != ActionDeny || r.Binary == "" {
			continue
		}
		if containsWordBoundary(scriptText, denyRuleBaseName(r.Binary)) {
			return &rules[i], true
		}
	}
	return nil, false
}

// hasAskOrDenyRules reports whether rules contains at least one VALID ask or
// deny rule — the R3 interpreter fallback's trigger for "route to ask, not
// allow" when no specific deny-word match was found, and the residual
// blind-spot safety net's trigger for the same reason: an operator who has
// configured ANY restriction at all must never have an interpreter/eval form
// (or an unrecognised wrapper's later argument word) silently pass as though
// D3 had nothing to say about it.
func hasAskOrDenyRules(rules []Rule) bool {
	for _, r := range rules {
		if !r.Action.Valid() {
			continue
		}
		if r.Action == ActionAsk || r.Action == ActionDeny {
			return true
		}
	}
	return false
}

// representativeAskOrDenyRule returns a rule to attach as a segment's
// MatchedRule when hasAskOrDenyRules(rules) is true but no deny rule's
// binary literally appeared as a word (findDenyBinaryWordIn missed): an ASK
// rule is preferred (it best explains why this is asking), falling back to
// the first DENY rule only when no ASK rule exists at all. Never nil when
// hasAskOrDenyRules(rules) is true — the caller relies on that to make this
// segment a "genuine" ask-rule match (VerdictHasGenuineAskRuleMatch,
// pkg/tools/shell_permission_mode.go), so an interpreter/eval blind spot
// with configured rules always reaches a real human approval, not merely an
// inert Action value no enforcement path consults.
func representativeAskOrDenyRule(rules []Rule) *Rule {
	var denyFallback *Rule
	for i := range rules {
		r := rules[i]
		if !r.Action.Valid() {
			continue
		}
		if r.Action == ActionAsk {
			return &rules[i]
		}
		if r.Action == ActionDeny && denyFallback == nil {
			denyFallback = &rules[i]
		}
	}
	return denyFallback
}

// findLaterArgDenyMatch implements the R3 residual "blind spot" safety net
// (founder decision, 2026-09-24): "An unknown wrapper cannot be listed
// exhaustively... make sure a denied binary appearing as a later argument
// word of an unrecognised head is at least ask-routed when deny rules
// exist." Scans args (the argument words following a head that matched no
// explicit D3 rule at all) for any DENY rule's binary name — e.g. `strace rm
// -rf x` (strace is not in wrapperSpecs) with a deny rule on `rm`. This is
// deliberately routed to ASK, never DENY: unlike a known wrapper (whose
// semantics this package models exactly) or an interpreter's script text
// (which the shell truly will execute), an unrecognised head's LATER
// argument word is not confidently "the command about to run" — it could
// just as easily be `grep rm file.txt`'s search pattern. Ask lets a human
// resolve the ambiguity; silently allowing it does not.
func findLaterArgDenyMatch(args []string, rules []Rule) (*Rule, bool) {
	for _, a := range args {
		for i := range rules {
			r := rules[i]
			if r.Action != ActionDeny || r.Binary == "" {
				continue
			}
			if strings.EqualFold(a, denyRuleBaseName(r.Binary)) {
				return &rules[i], true
			}
		}
	}
	return nil, false
}
