// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-092 D1/D3: an explicit wrapper Ask is not replaced by an unmatched Ask
// when the wrapper has no following program or the inner head cannot resolve.
func TestEvaluateCommand_WrapperAskKeepsMatchedRule(t *testing.T) {
	bin := t23RuleMetadata(t)
	for _, c := range t23IncompleteRuleCases(t) {
		t.Run(c.name+"/operator_rule", func(t *testing.T) {
			got := EvaluateCommand(c.command, c.rules, baseOptions(bin))
			t23RecordRule(t, c, got)
			t23AssertRule(t, c, got, false)
		})
		t.Run(c.name+"/no_rule", func(t *testing.T) {
			c.rules, c.match = nil, nil
			got := EvaluateCommand(c.command, nil, baseOptions(bin))
			t23RecordRule(t, c, got)
			t23AssertRule(t, c, got, false)
		})
	}
}

// CHECK M7 guard: Ask outranks inner Allow, but inner Deny wins in turn.
func TestEvaluateCommand_WrapperAskPrecedence(t *testing.T) {
	bin := t23RuleMetadata(t)
	for _, c := range t23ResolvedRuleCases(t, false) {
		t.Run(c.name, func(t *testing.T) {
			got := EvaluateCommand(c.command, c.rules, baseOptions(bin))
			t23RecordRule(t, c, got)
			t23AssertRule(t, c, got, true)
		})
	}
}

// ADR-092 D3/D4 and CHECK M10: inner-command Allow is distinct from granting
// the wrapper permission to authorize whatever follows it.
func TestEvaluateCommand_TransparentWrapperAllowScope(t *testing.T) {
	bin := t23RuleMetadata(t)
	for _, c := range t23ResolvedRuleCases(t, true) {
		t.Run(c.name, func(t *testing.T) {
			got := EvaluateCommand(c.command, c.rules, baseOptions(bin))
			t23RecordRule(t, c, got)
			t23AssertRule(t, c, got, true)
		})
	}
}

type t23RuleCase struct {
	name, command string
	rules         []Rule
	want          Action
	match         *Rule
	fullyAllowed  bool
}

func t23IncompleteRuleCases(t *testing.T) []t23RuleCase {
	t.Helper()
	cases := make([]t23RuleCase, 0, 2*2*3)
	for _, spelling := range []string{"sudo", "ſudo"} {
		for _, depth := range []string{"direct", "nested"} {
			prefix := ""
			if depth == "nested" {
				prefix = "env "
			}
			for _, form := range []struct{ name, suffix string }{
				{"standalone", ""}, {"flag_only", " --version"}, {"unresolved_inner", " t23-unavailable q45-control"},
			} {
				rule := Rule{Action: ActionAsk, Binary: spelling}
				cases = append(cases, t23RuleCase{
					name: depth + "/" + spelling + "/" + form.name, command: prefix + spelling + form.suffix,
					rules: []Rule{rule}, want: ActionAsk, match: &rule,
				})
			}
		}
	}
	require.Len(t, cases, 2*2*3, "fixture: retain both spellings, depths and all three incomplete forms")
	return cases
}

func t23ResolvedRuleCases(t *testing.T, allowScope bool) []t23RuleCase {
	t.Helper()
	width := 3
	if allowScope {
		width = 2
	}
	cases := make([]t23RuleCase, 0, 2*2*width)
	for _, spelling := range []string{"sudo", "ſudo"} {
		for _, depth := range []string{"direct", "nested"} {
			prefix := ""
			if depth == "nested" {
				prefix = "env "
			}
			command := prefix + spelling + " printf q45-control"
			name := depth + "/" + spelling + "/"
			ask := Rule{Action: ActionAsk, Binary: spelling}
			innerAllow := Rule{Action: ActionAllow, Binary: "printf", ArgPrefix: "q45-control"}
			innerDeny := Rule{Action: ActionDeny, Binary: "printf"}
			wrapperAllow := Rule{Action: ActionAllow, Binary: spelling}
			if allowScope {
				cases = append(cases,
					t23RuleCase{name + "inner_allow", command, []Rule{innerAllow}, ActionAllow, &innerAllow, true},
					t23RuleCase{name + "wrapper_only_allow", command, []Rule{wrapperAllow}, ActionNone, nil, false},
				)
			} else {
				cases = append(cases,
					t23RuleCase{name + "own_ask", command, []Rule{ask}, ActionAsk, &ask, false},
					t23RuleCase{name + "inner_allow", command, []Rule{ask, innerAllow}, ActionAsk, &ask, false},
					t23RuleCase{name + "inner_deny", command, []Rule{ask, innerDeny}, ActionDeny, &innerDeny, false},
				)
			}
		}
	}
	require.Len(t, cases, 2*2*width, "fixture: retain the complete precedence or allow-scope matrix")
	return cases
}

func t23RuleMetadata(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"sudo", "ſudo", "env", "printf"} {
		writeFakeExecutable(t, bin, name)
		_, err := ResolveBinary(name, bin)
		require.NoError(t, err, "fixture: %q resolves before rule assertions", name)
	}
	_, err := ResolveBinary("t23-unavailable", bin)
	require.Error(t, err, "fixture: the inner program is absent from the same positively checked resolver path")
	return bin
}

func t23RecordRule(t *testing.T, c t23RuleCase, got CommandVerdict) {
	t.Helper()
	record, err := json.Marshal(map[string]any{
		"name": c.name, "command": c.command, "rules": c.rules, "want_action": c.want,
		"want_rule": c.match, "want_fully_allowed": c.fullyAllowed, "verdict": got, "fully_allowed": got.FullyAllowed(),
	})
	require.NoError(t, err, "fixture: record the full verdict before assertions")
	t.Logf("WRAPPER_ASK_UNIT %s", record)
}

func t23AssertRule(t *testing.T, c t23RuleCase, got CommandVerdict, plain bool) {
	t.Helper()
	require.Len(t, got.Segments, 1, "wrapper rules: exactly one segment")
	assert.Equal(t, c.want, got.Action, "wrapper rules: the specified precedence determines the overall action")
	seg := got.Segments[0]
	assert.Equal(t, c.want, seg.Action, "wrapper rules: retain the winning segment action")
	assert.Equal(t, c.match, seg.MatchedRule, "wrapper rules: retain the exact configured rule, not a generic Ask")
	assert.Equal(t, c.fullyAllowed, got.FullyAllowed(), "wrapper rules: only an explicit covering inner Allow can fully approve")
	if plain {
		assert.False(t, seg.Blind, "plain wrapper: the inner command is resolvable")
		assert.Empty(t, seg.BlindReason, "plain wrapper: no generic blind reason")
		assert.True(t, seg.Simple, "plain wrapper: no assignments, redirects or substitutions")
	}
}
