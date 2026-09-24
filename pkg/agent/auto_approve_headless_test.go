// auto_approve_headless_test.go: ADR-092 D9 tests T8 (unattended runs follow
// the same Auto rule, ruling J1) and T18 (one dialog for a bash call that
// matches an operator D3 ask rule, §5.7).

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

// runScheduled runs one headless scheduled turn for "mia".
func runScheduled(t *testing.T, al *AgentLoop) {
	t.Helper()
	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)
	_, err = al.ProcessScheduled(context.Background(), "mia", meta.ID, "run the job", "scheduled", meta.ID)
	require.NoError(t, err)
}

// T8: in an unattended run with Auto on and a kernel sandbox, a RUNS tool
// executes, an ASKS tool is auto-denied with the headless reason and its
// audit rows, and bash under Auto executes. The approver is never consulted.
func TestAutoApprove_T8_HeadlessFollowsAutoRule(t *testing.T) {
	withKernelSandbox(t)
	provider := testutil.NewScenario().WithToolCalls([]providers.ToolCall{
		autoToolCall("t8-runs", "get_config", `{}`),
		autoToolCall("t8-asks", "delete_task", `{}`),
		autoToolCall("t8-bash", "bash", `{"command":"echo t8-headless-bash-ran"}`),
	}).WithText("done")
	al := newAutoTestLoop(t, provider, true, nil)
	stubs := installAutoStubs(t, al, "mia", []string{"get_config", "delete_task"}, "bash")
	approver := &autoRecordingApprover{approve: true}
	al.SetToolApprover(approver)
	readAudit := swapAuditLogger(t, al)

	runScheduled(t, al)

	assert.Empty(t, approver.requests(), "an unattended run never reaches the approver")
	assert.Equal(t, int32(1), stubs["get_config"].calls.Load(), "a RUNS tool executes headless")
	assert.Zero(t, stubs["delete_task"].calls.Load(), "an ASKS tool is auto-denied headless")
	assert.Contains(t, toolResultText(t, provider, "t8-asks"), "auto-denied")
	bashResult := toolResultText(t, provider, "t8-bash")
	assert.Contains(t, bashResult, "t8-headless-bash-ran", "bash under Auto runs in an unattended run (J1)")
	assert.NotContains(t, bashResult, "auto-denied")

	rows := readAudit()
	denied := auditRowsFor(rows, audit.EventToolPolicyAskDenied, "delete_task")
	require.Len(t, denied, 1, "the headless auto-deny writes its tool.policy.ask.denied row")
	assert.Len(t, auditRowsFor(rows, audit.EventToolPolicyDenyAttempted, "delete_task"), 1)
	assert.Len(t, auditRowsFor(rows, audit.EventToolAutoApproved, "get_config"), 1)
	assert.Empty(t, auditRowsFor(rows, audit.EventToolPolicyAskDenied, "bash"))
}

// ruleAskLoop builds a loop where bash is on plain "ask", Auto is off (so the
// shell mode is Ask), and the operator rules are rules.
func ruleAskLoop(t *testing.T, provider providers.LLMProvider, rules []shellrule.Rule, approve bool) (*AgentLoop, *autoRecordingApprover) {
	t.Helper()
	al := newAutoTestLoop(t, provider, false, func(c *config.Config) {
		c.Sandbox.ToolPolicies = map[string]string{"bash": "ask"}
		c.Sandbox.CommandRules = rules
	})
	approver := &autoRecordingApprover{approve: approve}
	al.SetToolApprover(approver)
	return al, approver
}

// T18: one dialog (§5.7). bash policy "ask", mode Ask, a command matching an
// operator {action: ask} rule.
func TestAutoApprove_T18_OneDialogForRuleAsk(t *testing.T) {
	askEcho := []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: "echo"}}
	// The output "t18-42" never appears in the command text, so a refusal
	// that quotes the command cannot pass for a run.
	const cmd = `{"command":"echo t18-$((20+22))"}`

	t.Run("approve: one request carrying rule_ask, command runs", func(t *testing.T) {
		provider := testutil.NewScenario().WithToolCall("bash", cmd).WithText("done")
		al, approver := ruleAskLoop(t, provider, askEcho, true)
		_, err := al.ProcessDirect(context.Background(), "run it", "t18-approve")
		require.NoError(t, err)
		reqs := approver.requests()
		require.Len(t, reqs, 1, "exactly one dialog for one call")
		assert.Equal(t, "rule_ask", reqs[0].Args["adr092_kind"], "the one dialog carries the D3 context")
		assert.Equal(t, "echo t18-$((20+22))", reqs[0].Args["command"])
		assert.Contains(t, toolResultText(t, provider, "bash-0"), "t18-42")
	})
	t.Run("deny: one request, nothing runs", func(t *testing.T) {
		provider := testutil.NewScenario().WithToolCall("bash", cmd).WithText("done")
		al, approver := ruleAskLoop(t, provider, askEcho, false)
		_, err := al.ProcessDirect(context.Background(), "run it", "t18-deny")
		require.NoError(t, err)
		assert.Len(t, approver.requests(), 1)
		result := toolResultText(t, provider, "bash-0")
		assert.Contains(t, result, "permission_denied")
		assert.NotContains(t, result, "t18-42")
	})
	t.Run("deny rule: refused with zero requests", func(t *testing.T) {
		provider := testutil.NewScenario().WithToolCall("bash", cmd).WithText("done")
		al, approver := ruleAskLoop(t, provider, []shellrule.Rule{{Action: shellrule.ActionDeny, Binary: "echo"}}, true)
		_, err := al.ProcessDirect(context.Background(), "run it", "t18-denyrule")
		require.NoError(t, err)
		assert.Empty(t, approver.requests())
		result := toolResultText(t, provider, "bash-0")
		assert.Contains(t, result, "operator rule")
		assert.NotContains(t, result, "t18-42")
	})
	t.Run("headless: auto-denied once with zero requests", func(t *testing.T) {
		provider := testutil.NewScenario().WithToolCall("bash", cmd).WithText("done")
		al, approver := ruleAskLoop(t, provider, askEcho, true)
		readAudit := swapAuditLogger(t, al)
		runScheduled(t, al)
		assert.Empty(t, approver.requests())
		result := toolResultText(t, provider, "bash-0")
		assert.Contains(t, result, "auto-denied")
		assert.NotContains(t, result, "t18-42")
		assert.Len(t, auditRowsFor(readAudit(), audit.EventToolPolicyAskDenied, "bash"), 1, "denied once")
	})
}
