// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import "strings"

// Action is the verdict a Rule carries, and the verdict Decide/EvaluateCommand
// returns for a segment or a whole command.
type Action string

const (
	// ActionDeny hard-refuses the matched command, in every mode including
	// God Mode (ADR-091 D1: "Operator deny rules stay in force in God Mode").
	ActionDeny Action = "deny"
	// ActionAsk requires the approval dialog even under an `allow` ceiling.
	ActionAsk Action = "ask"
	// ActionAllow pre-approves the matched command, suppressing the dialog
	// even under an `ask` ceiling — the D3 replacement for the retired
	// per-binary exec allowlist.
	ActionAllow Action = "allow"
	// ActionNone means no rule matched. It is not a wire value: a Rule is
	// never authored with this Action. It is the zero value Decide and
	// EvaluateCommand return when nothing applies, so the caller's ceiling
	// policy (ADR-077) governs unmodified — D3 is silent, not permissive.
	ActionNone Action = ""
)

// Valid reports whether a is one of the three wire actions a Rule may be
// authored with. ActionNone is deliberately excluded: it is an internal
// "nothing matched" sentinel, never a valid rule action.
func (a Action) Valid() bool {
	switch a {
	case ActionDeny, ActionAsk, ActionAllow:
		return true
	default:
		return false
	}
}

// Rule is the one shape operator `command_rules` (config.SandboxConfig,
// config-file-only, no wire schema) and user grants (ADR-091 D4) share.
//
// Binary is a bare command name (e.g. "git") or an absolute path; it is
// never matched against raw command text — see ResolveBinary and Verify.
// ArgPrefix, when set, is matched token-boundary (ADR-091 D4: "npm run
// test" does not match "npm run testfoo") against the segment's argument
// words following the resolved binary. An empty ArgPrefix matches any
// arguments (a bare-binary rule).
type Rule struct {
	Action    Action `json:"action"`
	Binary    string `json:"binary"`
	ArgPrefix string `json:"arg_prefix,omitempty"`
}

// Decide implements ADR-091 D3's "deny beats ask beats allow,
// specificity-blind" precedence over a set of rules already established to
// match the same segment. It does not itself decide whether a rule matches
// — that is MatchRules/EvaluateCommand's job — only which action wins once
// the matching set is known. A narrower rule (e.g. one with an ArgPrefix)
// is never preferred for being narrower: only the Action value matters.
func Decide(matched []Rule) Action {
	sawAsk := false
	sawAllow := false
	for _, r := range matched {
		switch r.Action {
		case ActionDeny:
			return ActionDeny
		case ActionAsk:
			sawAsk = true
		case ActionAllow:
			sawAllow = true
		}
	}
	switch {
	case sawAsk:
		return ActionAsk
	case sawAllow:
		return ActionAllow
	default:
		return ActionNone
	}
}

// tokenBoundaryHasPrefix reports whether args (already word-split) begins
// with every word of prefix, matched whole-token — "run test" is a prefix
// of ["run","test","-v"] but not of ["run","testfoo"] (ADR-091 D4/FR-024).
// An empty prefix always matches (a bare-binary rule/grant matches any
// arguments).
func tokenBoundaryHasPrefix(args []string, prefix []string) bool {
	if len(prefix) == 0 {
		return true
	}
	if len(args) < len(prefix) {
		return false
	}
	for i, w := range prefix {
		if args[i] != w {
			return false
		}
	}
	return true
}

// tokenBoundaryExact reports whether args, joined back with single spaces,
// equals full exactly — the Windows-only "exact-command" matching mode
// (ADR-091 FR-041), where an ArgPrefix has no "startswith" meaning and must
// instead equal the whole remaining argument text.
func tokenBoundaryExact(args []string, full string) bool {
	return strings.Join(args, " ") == full
}
