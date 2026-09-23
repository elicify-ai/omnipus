// shell_permission_wiring_test.go: ADR-092 Definition-of-Done reachability.
// Every test here builds the agent loop through NewAgentLoop (the production
// constructor) and inspects the bash tool the real wiring path
// (wireExecToolDepsOn) registered — never a hand-built ExecToolDeps. If the
// wiring in loop_wire.go is removed, these fail.

package agent

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// bashToolFields returns the unexported fields of the bash tool the given
// agent's registry holds. Reflection is read-only here: IsNil/Pointer/Len
// are permitted on unexported fields.
func bashToolFields(t *testing.T, al *AgentLoop, agentID string) reflect.Value {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "agent %q must be registered", agentID)
	tool, ok := inst.Tools.Get("bash")
	require.True(t, ok, "bash must be registered for %q", agentID)
	exec, ok := tool.(*tools.ExecTool)
	require.True(t, ok, "bash must be the *tools.ExecTool built by wireExecToolDepsOn, got %T", tool)
	return reflect.ValueOf(exec).Elem()
}

func ruleBinaries(v reflect.Value) []string {
	out := make([]string, v.Len())
	for i := range out {
		out[i] = v.Index(i).FieldByName("Binary").String()
	}
	return out
}

func TestShellPermissionWiring_ExecToolGetsAllFourDeps(t *testing.T) {
	al := newGateTestLoopWithRules(t, []shellrule.Rule{
		{Action: shellrule.ActionDeny, Binary: "rm"},
		{Action: shellrule.ActionAsk, Binary: "git", ArgPrefix: "push"},
	})
	f := bashToolFields(t, al, testDefaultAgentID)

	assert.False(t, f.FieldByName("shellMode").IsNil(), "ExecToolDeps.ShellMode must be wired")
	assert.False(t, f.FieldByName("approvalRequester").IsNil(), "ExecToolDeps.ApprovalRequester must be wired")
	grants := f.FieldByName("approvalGrants")
	require.False(t, grants.IsNil(), "ExecToolDeps.ApprovalGrants must be wired")
	assert.Equal(t, reflect.ValueOf(al.ApprovalGrants()).Pointer(), grants.Pointer(),
		"the bash tool must share the loop's own grant store, not a private copy")
	assert.Equal(t, []string{"rm", "git"}, ruleBinaries(f.FieldByName("commandRules")))
}

// TestShellPermissionWiring_ConfigReloadReachesCommandRules pins when a
// command_rules edit takes effect: a config reload (the path an external
// config.json edit takes via the gateway's file poller ->
// executeReload -> ReloadProviderAndConfig) rebuilds the bash tool with the
// new rules.
func TestShellPermissionWiring_ConfigReloadReachesCommandRules(t *testing.T) {
	al := newGateTestLoopWithRules(t, []shellrule.Rule{{Action: shellrule.ActionDeny, Binary: "rm"}})
	next, err := al.GetConfig().Clone()
	require.NoError(t, err)
	next.Sandbox.CommandRules = []shellrule.Rule{{Action: shellrule.ActionDeny, Binary: "curl"}}
	require.NoError(t, al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, next))

	f := bashToolFields(t, al, testDefaultAgentID)
	assert.Equal(t, []string{"curl"}, ruleBinaries(f.FieldByName("commandRules")))
}

type countingApprover struct{ calls atomic.Int32 }

func (c *countingApprover) RequestApproval(_ context.Context, _ PolicyApprovalReq) (bool, string, bool) {
	c.calls.Add(1)
	return false, "user", false
}

// TestShellPermissionWiring_AutoAskRuleReachesApprover is the behavioural
// proof: under Auto, a D3 "ask" rule makes the real bash tool resolve its
// mode through the wired gate and escalate through the wired approval
// requester to the loop's approver. With either dep unwired the tool falls
// back to Ask, treats the ask rule as a no-op, and the approver is never
// called.
func TestShellPermissionWiring_AutoAskRuleReachesApprover(t *testing.T) {
	withKernelSandbox(t)
	al := newGateTestLoopWithRules(t, []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: "true"}})
	approver := &countingApprover{}
	al.SetToolApprover(approver)

	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	tool, ok := inst.Tools.Get("bash")
	require.True(t, ok)
	ctx := tools.WithAgentID(context.Background(), testDefaultAgentID)
	ctx = tools.WithTranscriptSessionID(ctx, "wiring-sess")
	ctx = tools.WithToolCallID(ctx, "call-1")

	res := tool.Execute(ctx, map[string]any{"command": "true"})
	require.NotNil(t, res)
	assert.Equal(t, int32(1), approver.calls.Load(), "the ask rule must reach the loop's approver")
	assert.True(t, res.IsError, "a denied escalation must refuse the command")
	assert.True(t, strings.Contains(res.ForLLM, "not approved"), "got: %s", res.ForLLM)
}

func newGateTestLoopWithRules(t *testing.T, rules []shellrule.Rule) *AgentLoop {
	t.Helper()
	return newGateTestLoopCfg(t, func(c *config.Config) {
		c.Sandbox.ToolPolicies = map[string]string{"bash": "ask"}
		c.Sandbox.AutoApprove = true
		c.Sandbox.CommandRules = rules
	})
}

// TestShellPermissionWiring_ExecToolGetsTheAuditLogger: the ADR-092 audit
// events the bash tool and its grant store write (pre-flight escalation,
// grant recorded, approval decision) need the loop's audit logger on the
// tool the production wiring registered.
func TestShellPermissionWiring_ExecToolGetsTheAuditLogger(t *testing.T) {
	al := newGateTestLoopCfg(t, func(c *config.Config) {
		c.Sandbox.ToolPolicies = map[string]string{"bash": "ask"}
		c.Sandbox.AuditLog = true
	})
	require.NotNil(t, al.AuditLogger())
	f := bashToolFields(t, al, testDefaultAgentID)
	logger := f.FieldByName("auditLogger")
	require.False(t, logger.IsNil(), "the bash tool must receive the audit logger")
	assert.Equal(t, reflect.ValueOf(al.AuditLogger()).Pointer(), logger.Pointer())
}
