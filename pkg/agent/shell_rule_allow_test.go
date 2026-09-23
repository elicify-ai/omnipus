// shell_rule_allow_test.go: ADR-092 D3 allow rules at the "ask" prompt.
// An allow rule on every segment replaces the retired exec allowlist: the
// classic Ask prompt is skipped. It never bypasses the D7/D8 pre-flights.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func ruleTurnState(t *testing.T, rules []shellrule.Rule) (*AgentLoop, *turnState) {
	t.Helper()
	al := newGateTestLoopWithRules(t, rules)
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	return al, &turnState{agent: inst, agentID: testDefaultAgentID}
}

func bashArgs(cmd string) map[string]any { return map[string]any{"command": cmd} }

func TestBashRulesSettlePrompt_AllSegmentsAllowedSkipsPrompt(t *testing.T) {
	_, ts := ruleTurnState(t, []shellrule.Rule{
		{Action: shellrule.ActionAllow, Binary: "echo"},
		{Action: shellrule.ActionAllow, Binary: "true"},
	})
	assert.True(t, bashRulesSettlePrompt(ts, "bash", bashArgs("echo hi && true")))
	assert.False(t, bashRulesSettlePrompt(ts, "not_bash", bashArgs("echo hi")), "only bash consults D3")
}

func TestBashRulesSettlePrompt_UnmatchedSegmentStillPrompts(t *testing.T) {
	_, ts := ruleTurnState(t, []shellrule.Rule{{Action: shellrule.ActionAllow, Binary: "echo"}})
	assert.False(t, bashRulesSettlePrompt(ts, "bash", bashArgs("echo hi && true")))
	assert.False(t, bashRulesSettlePrompt(ts, "bash", bashArgs("true")))
}

func TestBashRulesSettlePrompt_AllowPlusDenyIsRefused(t *testing.T) {
	_, ts := ruleTurnState(t, []shellrule.Rule{
		{Action: shellrule.ActionAllow, Binary: "echo"},
		{Action: shellrule.ActionDeny, Binary: "true"},
	})
	require.True(t, bashRulesSettlePrompt(ts, "bash", bashArgs("echo hi; true")),
		"a deny segment settles the call without a prompt: the tool refuses it")

	tool, ok := ts.agent.Tools.Get("bash")
	require.True(t, ok)
	ctx := tools.WithAgentID(context.Background(), testDefaultAgentID)
	res := tool.Execute(tools.WithTranscriptSessionID(ctx, "s"), bashArgs("echo hi; true"))
	require.NotNil(t, res)
	assert.True(t, res.IsError)
	assert.Contains(t, res.ForLLM, "operator rule")
}

// TestAllowRule_DoesNotSuppressFSPreflightUnderAuto: an allow rule on the
// binary settles the upfront prompt, but under Auto the tool's D7
// pre-flight still escalates a write outside the workspace.
func TestAllowRule_DoesNotSuppressFSPreflightUnderAuto(t *testing.T) {
	withKernelSandbox(t)
	al := newGateTestLoopCfg(t, func(c *config.Config) {
		c.Agents.Defaults.RestrictToWorkspace = true // the shipped default (workspace_path_guard)
		c.Sandbox.ToolPolicies = map[string]string{"bash": "ask"}
		c.Sandbox.AutoApprove = true
		c.Sandbox.CommandRules = []shellrule.Rule{{Action: shellrule.ActionAllow, Binary: "echo"}}
	})
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	ts := &turnState{agent: inst, agentID: testDefaultAgentID}
	approver := &countingApprover{}
	al.SetToolApprover(approver)
	cmd := "echo probe > /etc/omnipus-adr092-allow-probe"
	require.True(t, bashRulesSettlePrompt(ts, "bash", bashArgs(cmd)))

	tool, ok := ts.agent.Tools.Get("bash")
	require.True(t, ok)
	ctx := tools.WithAgentID(context.Background(), testDefaultAgentID)
	ctx = tools.WithTranscriptSessionID(ctx, "allow-probe")
	res := tool.Execute(withPinnedShellMode(ctx, tools.ShellModeAuto), bashArgs(cmd))
	require.NotNil(t, res)
	assert.GreaterOrEqual(t, approver.calls.Load(), int32(1), "the D7 escalation must still reach the approver")
	assert.True(t, res.IsError, "a denied escalation refuses the command")
	assert.True(t, strings.Contains(res.ForLLM, "pre-flight"), "got: %s", res.ForLLM)
}

// TestRunTurn_AllowRuleSkipsAskPrompt drives a real turn: bash policied ask,
// Auto-approve off, and an allow rule for the command. The loop's approver
// is never consulted; without the rule it is consulted once.
func TestRunTurn_AllowRuleSkipsAskPrompt(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rules     []shellrule.Rule
		wantCalls int
	}{
		{"allow rule", []shellrule.Rule{{Action: shellrule.ActionAllow, Binary: "echo"}}, 0},
		{"no rule", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := baseLoopDenialTestConfig(t)
			cfg.Sandbox.ToolPolicies = map[string]string{"bash": "ask"}
			cfg.Sandbox.AutoApprove = false
			cfg.Sandbox.CommandRules = tc.rules
			provider := testutil.NewScenario().WithToolCall("bash", `{"command":"echo hi"}`).WithText("done")
			al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
			defer al.Close()
			setAskPolicyForAllAgents(t, al, "bash", config.ToolPolicyAsk)
			approver := &countingDenyApprover{reason: "user"}
			al.SetToolApprover(approver)

			_, err := al.ProcessDirect(context.Background(), "run echo", "allow-rule-turn")
			require.NoError(t, err)
			assert.Equal(t, tc.wantCalls, approver.callCount())
		})
	}
}
