// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-092 D1/D3: bash Allow bypasses the upstream tool-policy prompt, not an
// operator Ask. The actual rule-approval request must still settle consent.
func TestEnforceShellPermissionMode_WrapperAskOnAllowTool(t *testing.T) {
	t23ToolMetadata(t)
	for _, c := range t23IncompleteToolCases(t) {
		for _, approve := range []bool{false, true} {
			decision := "refused"
			if approve {
				decision = "approved"
			}
			t.Run(c.name+"/"+decision, func(t *testing.T) {
				tool, ctx, requester := t23AllowTool(t, ShellModeAsk, approve, c.rules)
				verdict := tool.EvaluateCommandRules(c.command)
				perm, refusal := tool.enforceShellPermissionMode(ctx, c.command)
				t23RecordTool(t, c, ShellModeAsk, approve, verdict, perm, refusal, requester)
				t23AssertToolRule(t, c, verdict, false)
				t23AssertPermission(t, c, ShellModeAsk, approve, perm, refusal, requester)
			})
		}
		t.Run(c.name+"/no_rule", func(t *testing.T) {
			c.rules, c.match = nil, nil
			tool, ctx, requester := t23AllowTool(t, ShellModeAsk, false, nil)
			verdict := tool.EvaluateCommandRules(c.command)
			perm, refusal := tool.enforceShellPermissionMode(ctx, c.command)
			t23RecordTool(t, c, ShellModeAsk, false, verdict, perm, refusal, requester)
			t23AssertToolRule(t, c, verdict, false)
			t23AssertPermission(t, c, ShellModeAsk, false, perm, refusal, requester)
		})
	}
}

// CHECK M7 guard: ordinary own Ask prompts outside God Mode, inner Allow
// cannot erase it, and inner Deny still refuses in either mode.
func TestEnforceShellPermissionMode_WrapperAskPrecedence(t *testing.T) {
	t23ToolMetadata(t)
	for _, c := range t23ResolvedToolCases(t, false) {
		for _, mode := range []ShellMode{ShellModeAsk, ShellModeGod} {
			for _, approve := range []bool{false, true} {
				decision := "refused"
				if approve {
					decision = "approved"
				}
				t.Run(c.name+"/"+string(mode)+"/"+decision, func(t *testing.T) {
					tool, ctx, requester := t23AllowTool(t, mode, approve, c.rules)
					verdict := tool.EvaluateCommandRules(c.command)
					perm, refusal := tool.enforceShellPermissionMode(ctx, c.command)
					t23RecordTool(t, c, mode, approve, verdict, perm, refusal, requester)
					t23AssertToolRule(t, c, verdict, true)
					t23AssertPermission(t, c, mode, approve, perm, refusal, requester)
				})
			}
		}
	}
}

// CHECK M10 guard at the real tool evaluator: a transparent wrapper does not
// erase a covering inner Allow, or turn a wrapper-only Allow into inner consent.
func TestExecTool_TransparentWrapperAllowScope(t *testing.T) {
	t23ToolMetadata(t)
	for _, c := range t23ResolvedToolCases(t, true) {
		t.Run(c.name, func(t *testing.T) {
			tool, _, _ := t23AllowTool(t, ShellModeAsk, false, c.rules)
			verdict := tool.EvaluateCommandRules(c.command)
			t23RecordTool(t, c, ShellModeAsk, false, verdict, nil, nil, nil)
			t23AssertToolRule(t, c, verdict, true)
		})
	}
}

type t23ToolCase struct {
	name, command string
	rules         []shellrule.Rule
	want          shellrule.Action
	match         *shellrule.Rule
	fullyAllowed  bool
}

func t23IncompleteToolCases(t *testing.T) []t23ToolCase {
	t.Helper()
	cases := make([]t23ToolCase, 0, 2*2*3)
	for _, spelling := range []string{"sudo", "ſudo"} {
		for _, depth := range []string{"direct", "nested"} {
			prefix := ""
			if depth == "nested" {
				prefix = "env "
			}
			for _, form := range []struct{ name, suffix string }{
				{"standalone", ""}, {"flag_only", " --version"}, {"unresolved_inner", " t23-unavailable q45-control"},
			} {
				rule := shellrule.Rule{Action: shellrule.ActionAsk, Binary: spelling}
				cases = append(cases, t23ToolCase{
					name: depth + "/" + spelling + "/" + form.name, command: prefix + spelling + form.suffix,
					rules: []shellrule.Rule{rule}, want: shellrule.ActionAsk, match: &rule,
				})
			}
		}
	}
	require.Len(t, cases, 2*2*3, "fixture: retain both spellings, depths and all three incomplete forms")
	return cases
}

func t23ResolvedToolCases(t *testing.T, allowScope bool) []t23ToolCase {
	t.Helper()
	width := 3
	if allowScope {
		width = 2
	}
	cases := make([]t23ToolCase, 0, 2*2*width)
	for _, spelling := range []string{"sudo", "ſudo"} {
		for _, depth := range []string{"direct", "nested"} {
			prefix := ""
			if depth == "nested" {
				prefix = "env "
			}
			command := prefix + spelling + " printf q45-control"
			name := depth + "/" + spelling + "/"
			ask := shellrule.Rule{Action: shellrule.ActionAsk, Binary: spelling}
			innerAllow := shellrule.Rule{Action: shellrule.ActionAllow, Binary: "printf", ArgPrefix: "q45-control"}
			innerDeny := shellrule.Rule{Action: shellrule.ActionDeny, Binary: "printf"}
			wrapperAllow := shellrule.Rule{Action: shellrule.ActionAllow, Binary: spelling}
			if allowScope {
				cases = append(cases,
					t23ToolCase{name + "inner_allow", command, []shellrule.Rule{innerAllow}, shellrule.ActionAllow, &innerAllow, true},
					t23ToolCase{name + "wrapper_only_allow", command, []shellrule.Rule{wrapperAllow}, shellrule.ActionNone, nil, false},
				)
			} else {
				cases = append(cases,
					t23ToolCase{name + "own_ask", command, []shellrule.Rule{ask}, shellrule.ActionAsk, &ask, false},
					t23ToolCase{name + "inner_allow", command, []shellrule.Rule{ask, innerAllow}, shellrule.ActionAsk, &ask, false},
					t23ToolCase{name + "inner_deny", command, []shellrule.Rule{ask, innerDeny}, shellrule.ActionDeny, &innerDeny, false},
				)
			}
		}
	}
	require.Len(t, cases, 2*2*width, "fixture: retain the complete precedence or allow-scope matrix")
	return cases
}

func t23ToolMetadata(t *testing.T) {
	t.Helper()
	bin := t11PolicyMetadata(t)
	_, err := shellrule.ResolveBinary("t23-unavailable", bin)
	require.Error(t, err, "fixture: the inner program is absent from the positively checked isolated PATH")
}

func t23AllowTool(t *testing.T, mode ShellMode, approve bool, rules []shellrule.Rule) (*ExecTool, context.Context, *fakeShellApprovalRequester) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	requester := &fakeShellApprovalRequester{approve: approve}
	tool, err := NewExecToolWithDeps(t.TempDir(), true, nil, ExecToolDeps{
		GodMode: mode == ShellModeGod, ShellMode: fakeShellModeResolver{mode: mode},
		ApprovalRequester: requester, ApprovalGrants: security.NewApprovalGrantStore(), CommandRules: rules,
	})
	require.NoError(t, err, "fixture: construct the real shell tool")
	policy := &ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"bash": config.ToolPolicyAllow}, GodMode: mode == ShellModeGod}
	filtered, policies := FilterToolsByPolicy([]Tool{tool}, "native", policy)
	require.Len(t, filtered, 1, "fixture: the actual shell tool passes the Allow policy gate")
	require.Same(t, tool, filtered[0], "fixture: enforce the same tool selected by the real policy filter")
	require.Equal(t, map[string]string{"bash": "allow"}, policies, "fixture: this is not an upstream Ask tool-policy case")
	ctx := WithToolCallID(WithTurnID(WithTranscriptSessionID(WithAgentID(context.Background(), "t23-agent"), "t23-session"), "t23-turn"), "t23-call")
	require.Equal(t, mode, tool.resolveShellMode(ctx), "fixture: Allow uses internal Ask, or the explicit God Mode control")
	require.False(t, RuleAskSettled(ctx), "fixture: no earlier approval has settled this rule")
	return tool, ctx, requester
}

func t23RecordTool(t *testing.T, c t23ToolCase, mode ShellMode, approve bool, v shellrule.CommandVerdict, perm *shellPermissionResult, refusal *ToolResult, requester *fakeShellApprovalRequester) {
	t.Helper()
	calls := 0
	var args map[string]any
	if requester != nil {
		calls, args = requester.callCount(), requester.lastArg
	}
	var actualMode ShellMode
	if perm != nil {
		actualMode = perm.mode
	}
	record, err := json.Marshal(map[string]any{
		"name": c.name, "command": c.command, "rules": c.rules, "want_action": c.want,
		"want_rule": c.match, "want_fully_allowed": c.fullyAllowed, "verdict": v, "fully_allowed": v.FullyAllowed(),
		"tool_policy": "allow", "mode": mode, "approve": approve, "genuine_ask": VerdictHasGenuineAskRuleMatch(v),
		"permission_present": perm != nil, "permission_mode": actualMode, "refusal": refusal, "approval_calls": calls, "approval_args": args,
	})
	require.NoError(t, err, "fixture: record actual evaluation and approval disposition before assertions")
	t.Logf("WRAPPER_ASK_TOOL %s", record)
}

func t23AssertToolRule(t *testing.T, c t23ToolCase, v shellrule.CommandVerdict, plain bool) {
	t.Helper()
	require.Len(t, v.Segments, 1, "wrapper rules: exactly one segment")
	assert.Equal(t, c.want, v.Action, "wrapper rules: the specified precedence determines the overall action")
	seg := v.Segments[0]
	assert.Equal(t, c.want, seg.Action, "wrapper rules: retain the winning segment action")
	assert.Equal(t, c.match, seg.MatchedRule, "wrapper rules: retain the exact configured rule, not a generic Ask")
	assert.Equal(t, c.want == shellrule.ActionAsk && c.match != nil, VerdictHasGenuineAskRuleMatch(v), "wrapper rules: only a matched operator Ask forces rule approval")
	assert.Equal(t, c.fullyAllowed, v.FullyAllowed(), "wrapper rules: only an explicit covering inner Allow can fully approve")
	if plain {
		assert.False(t, seg.Blind, "plain wrapper: the inner command is resolvable")
		assert.Empty(t, seg.BlindReason, "plain wrapper: no generic blind reason")
		assert.True(t, seg.Simple, "plain wrapper: no assignments, redirects or substitutions")
	}
}

func t23AssertPermission(t *testing.T, c t23ToolCase, mode ShellMode, approve bool, perm *shellPermissionResult, refusal *ToolResult, requester *fakeShellApprovalRequester) {
	t.Helper()
	prompt := mode != ShellModeGod && c.want == shellrule.ActionAsk && c.match != nil
	if prompt {
		assert.Equal(t, 1, requester.callCount(), "operator Ask: reach the real rule-approval request exactly once")
		assert.Equal(t, map[string]any{"command": c.command, "adr092_kind": "rule_ask"}, requester.lastArg, "operator Ask: exact approval arguments identify this command and rule cause")
	} else {
		assert.Zero(t, requester.callCount(), "no-prompt control: God Ask, Deny and unmatched Ask do not prompt here")
		assert.Nil(t, requester.lastArg, "no-prompt control: no approval arguments were sent")
	}
	if c.want == shellrule.ActionDeny || (prompt && !approve) {
		assert.Nil(t, perm, "consent: no usable permission after an operator Deny or refused approval")
		require.NotNil(t, refusal, "consent: refused commands return an error result")
		assert.True(t, refusal.IsError, "consent: refusal is an error")
		if c.want == shellrule.ActionDeny {
			assert.Contains(t, refusal.ForLLM, "operator rule", "Deny wins without prompting")
			assert.Contains(t, refusal.ForLLM, "binary=\"printf\"", "Deny identifies the winning inner rule")
		} else {
			assert.Contains(t, refusal.ForLLM, "operator rule requires approval", "refusal identifies the explicit Ask requirement")
			assert.Contains(t, refusal.ForLLM, "denied by test fixture", "refusal retains the actual approver's reason")
		}
	} else {
		assert.Nil(t, refusal, "consent: approved calls and no-prompt controls are not refused")
		require.NotNil(t, perm, "consent: usable permission exists after the required decision")
		assert.Equal(t, mode, perm.mode, "consent: preserve the selected enforcement mode")
	}
}
