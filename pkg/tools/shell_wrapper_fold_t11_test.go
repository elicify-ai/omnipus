// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/stretchr/testify/require"
)

// Q31 A: the real shell-tool evaluator retains the inner deny rule for known
// wrapper case folds. These fixtures are command text and metadata only.
func TestExecTool_CommandRulesFoldedWrapperDeny(t *testing.T) {
	tool, _, _ := t11ExecWrapperFixture(t)
	rule := shellrule.Rule{Action: shellrule.ActionDeny, Binary: "printf"}
	for _, c := range t11ExecWrapperCases(t) {
		t.Run(c.name, func(t *testing.T) {
			got := tool.EvaluateCommandRules(c.command)
			record, err := json.Marshal(map[string]any{
				"name": c.name, "command": c.command, "rule": rule, "want_action": c.want, "verdict": got,
			})
			require.NoError(t, err, "fixture: record the full real-tool verdict")
			t.Logf("TOOL_WRAPPER_RULE_CASE %s", record)
			require.Len(t, got.Segments, 1, "Q31: each command is one segment")
			require.Equal(t, c.want, got.Action, "Q31 A: a known folded wrapper retains the inner operator denial")
			segment := got.Segments[0]
			require.Equal(t, c.want, segment.Action, "Q31: the segment has the specified policy action")
			require.Equal(t, c.want == shellrule.ActionAsk, segment.Blind, "Q31: deny is not a blind Ask")
			if c.want == shellrule.ActionNone {
				require.Nil(t, segment.MatchedRule, "Q31: a non-wrapper script argument is not unwrapped")
				wantPath, err := filepath.EvalSymlinks(filepath.Join(os.Getenv("PATH"), strings.Fields(c.command)[0]))
				require.NoError(t, err, "fixture: canonicalize the non-wrapper metadata path")
				require.Equal(t, wantPath, segment.ResolvedPath, "Q31: a non-wrapper retains its ordinary resolved path")
			} else {
				require.Equal(t, &rule, segment.MatchedRule, "Q31: the exact configured rule drives deny or the residual Ask")
			}
			require.False(t, got.FullyAllowed(), "Q31: no allow rule is configured")
		})
	}
}

// Calling only the real pre-execution enforcement boundary proves the God Mode
// disposition without ever dispatching a fixture process or shell script.
func TestEnforceShellPermissionMode_FoldedWrapperDenyInGodMode(t *testing.T) {
	tool, ctx, requester := t11ExecWrapperFixture(t)
	for _, c := range t11ExecWrapperCases(t) {
		t.Run(c.name, func(t *testing.T) {
			verdict := tool.EvaluateCommandRules(c.command)
			perm, result := tool.enforceShellPermissionMode(ctx, c.command)
			var mode ShellMode
			if perm != nil {
				mode = perm.mode
			}
			wantRefusal := c.want == shellrule.ActionDeny
			record, err := json.Marshal(map[string]any{
				"name": c.name, "command": c.command, "want_action": c.want, "verdict": verdict,
				"want_refusal": wantRefusal, "permission_present": perm != nil, "permission_mode": mode,
				"refusal": result, "approval_calls": requester.callCount(), "configured_mode": ShellModeGod,
			})
			require.NoError(t, err, "fixture: record verdict and actual God Mode enforcement before asserting")
			t.Logf("GOD_WRAPPER_RULE_CASE %s", record)
			require.Equal(t, 0, requester.callCount(), "Q31: God Mode never requests approval")
			if wantRefusal {
				require.NotNil(t, result, "Q31 A: expected an operator-deny refusal in God Mode; Ask or no action is not denial")
				require.Nil(t, perm, "Q31: a denied command cannot receive a usable permission result")
				require.True(t, result.IsError, "Q31: the refusal is an error, not a successful tool result")
				require.Contains(t, result.ForLLM, "operator rule", "Q31: the refusal names the operator-rule cause")
				require.Contains(t, result.ForLLM, "printf", "Q31: the refusal identifies the configured inner rule")
			} else {
				require.Nil(t, result, "Q31 control: an unknown-wrapper Ask or no-rule result does not deny in God Mode")
				require.NotNil(t, perm, "Q31 control: ordinary God Mode policy handling remains reachable")
				require.Equal(t, ShellModeGod, perm.mode, "Q31 control: the actual returned permission is God Mode")
			}
			require.Equal(t, c.want, verdict.Action, "Q31: the underlying evaluator also retains the specified action")
		})
	}
}

type t11ExecWrapperCase struct {
	name, command string
	want          shellrule.Action
}

func t11ExecWrapperCases(t *testing.T) []t11ExecWrapperCase {
	t.Helper()
	cases := make([]t11ExecWrapperCase, 0, 7*3+4)
	for _, spelling := range []string{"sudo", "ſudo", "SUDO", "setsid", "ſetsid", "setſid", "SETSID"} {
		cases = append(cases,
			t11ExecWrapperCase{"direct/" + spelling, spelling + " printf q31-control", shellrule.ActionDeny},
			t11ExecWrapperCase{"inline/" + spelling, spelling + ` bash -c "printf q31-control"`, shellrule.ActionDeny},
			t11ExecWrapperCase{"recursive/" + spelling, "env " + spelling + " printf q31-control", shellrule.ActionDeny},
		)
	}
	for _, spelling := range []string{"sudoish", "ſudoish"} {
		cases = append(cases,
			t11ExecWrapperCase{"direct/" + spelling, spelling + " printf q31-control", shellrule.ActionAsk},
			t11ExecWrapperCase{"inline/" + spelling, spelling + ` bash -c "printf q31-control"`, shellrule.ActionNone},
		)
	}
	// Seven known spellings in three forms, plus four non-wrapper controls.
	require.Len(t, cases, 25, "fixture: retain all 7*3+4 wrapper rows")
	return cases
}

func t11ExecWrapperFixture(t *testing.T) (*ExecTool, context.Context, *fakeShellApprovalRequester) {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"sudo", "ſudo", "SUDO", "setsid", "ſetsid", "setſid", "SETSID", "env", "bash", "printf", "sudoish", "ſudoish"} {
		err := os.WriteFile(filepath.Join(bin, name), []byte("metadata fixture only; never executed\n"), 0o700)
		require.NoError(t, err, "fixture: create inert executable metadata for %q", name)
	}
	t.Setenv("PATH", bin)
	for _, name := range []string{"sudo", "ſudo", "SUDO", "setsid", "ſetsid", "setſid", "SETSID", "env", "bash", "printf", "sudoish", "ſudoish"} {
		resolved, err := shellrule.ResolveBinary(name, bin)
		require.NoError(t, err, "fixture: %q must resolve before evaluating a wrapper denial", name)
		t.Logf("WRAPPER_RESOLVE name=%q path=%q", name, resolved)
	}
	requester := &fakeShellApprovalRequester{approve: true}
	tool, err := NewExecToolWithDeps(t.TempDir(), true, nil, ExecToolDeps{
		GodMode: true, ShellMode: fakeShellModeResolver{mode: ShellModeGod},
		ApprovalRequester: requester, ApprovalGrants: security.NewApprovalGrantStore(),
		CommandRules: []shellrule.Rule{{Action: shellrule.ActionDeny, Binary: "printf"}},
	})
	require.NoError(t, err, "fixture: construct the real exec tool with the configured rule")
	ctx := WithTurnID(WithTranscriptSessionID(WithAgentID(context.Background(), "t11-wrapper"), "t11-wrapper-session"), "t11-wrapper-turn")
	require.Equal(t, ShellModeGod, tool.resolveShellMode(ctx), "fixture: enforce the actual God Mode branch")
	return tool, ctx, requester
}
