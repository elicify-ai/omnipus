// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// FR-017 audit-coverage regression test: an approval HOOK that denies a tool
// (e.g. the gateway's wsApprovalHook returning VerdictDeny for a policy-denied
// tool on a WS/CLI turn) must produce an attributed audit entry.
//
// ## The gap this closes
//
// Before the fix, the loop's approval-deny branch (loop.go, the
// `if !approval.IsApproved()` block after al.hooks.ApproveTool) emitted a
// ToolExecSkipped event and injected a synthetic deny message into history,
// then `continue`d — WITHOUT calling emitPolicyDenyAudit. So a tool denied by
// the hook wrote NO audit.Entry, and the FR-017 User attribution (e.g.
// "cli") was never observable for the most common security event: a denied
// tool on a WS-originated turn. The hook returns Deny BEFORE the loop reaches
// the TOCTOU re-check where emitPolicyDenyAudit fires, so the other deny
// branches did not cover it.
//
// These tests drive the REAL runTurn path (via runAgentLoop) with:
//   - a ScenarioProvider that emits a tool call (so the loop reaches the
//     approval branch), then a text recovery reply,
//   - a mounted ToolApprover hook that returns VerdictDeny (simulating the
//     wsApprovalHook policy-deny), and
//   - processOptions.UserID threaded as the gateway principal,
//
// then assert the resulting audit.jsonl carries a tool.policy.deny.attempted
// entry with the correct User attribution. The companion test pins the
// channel/non-gateway invariant: an empty UserID leaves audit User empty.
//
// Traces to: cli-minimization-spec.md FR-017 / SC-004 / §TDD Plan #12.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// denyApproverHook is a test ToolApprover that always denies, mirroring the
// gateway wsApprovalHook returning VerdictDeny for a policy-denied tool. It
// records that it was actually consulted so the test can prove the hook branch
// (not some other deny path) produced the audit entry.
type denyApproverHook struct {
	reason     string
	wasCalled  bool
	lastToolID string
}

func (h *denyApproverHook) ApproveTool(
	_ context.Context,
	req *ToolApprovalRequest,
) (ApprovalDecision, error) {
	h.wasCalled = true
	h.lastToolID = req.Tool
	return ApprovalDecision{Verdict: VerdictDeny, Reason: h.reason}, nil
}

// newHookDenyFixture builds an AgentLoop with audit enabled, a custom agent,
// and a scripted provider that emits a single tool call (to dangerous_tool)
// then a text reply. It mounts the deny-only approver hook. The tool's policy
// stays "allow" so the deny is produced by the HOOK, not the TOCTOU re-check —
// this is what isolates the branch under test.
//
// Returns the loop, the custom agent instance, the deny hook, and the audit
// dir so callers can read back audit.jsonl.
func newHookDenyFixture(t *testing.T) (al *AgentLoop, agentID, auditDir string, hook *denyApproverHook) {
	t.Helper()

	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	provider := testutil.NewScenario().
		WithToolCall("dangerous_tool", `{}`).
		WithText("I cannot proceed without that tool.")

	const customAgentID = "hookdeny-agent"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: customAgentID, Name: "Hook Deny Agent", Type: config.AgentTypeCustom},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			// Enable audit logging so emitPolicyDenyAudit writes to audit.jsonl.
			AuditLog: true,
		},
	}

	msgBus := bus.NewMessageBus()
	al = mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })

	// Register the stub tool (reused from scenario_runturn_test.go). Its policy
	// stays at the agent default ("allow") so it passes the TOCTOU re-check; the
	// HOOK is what denies it. This is the branch under test.
	al.RegisterTool(&dangerousStubTool{})

	hook = &denyApproverHook{reason: "tool denied by agent policy"}
	require.NoError(t, al.MountHook(HookRegistration{
		Name:   "test-deny-approver",
		Source: HookSourceInProcess,
		Hook:   hook,
	}), "mounting the deny approver hook must succeed")

	_, ok := al.GetRegistry().GetAgent(customAgentID)
	require.True(t, ok, "SETUP: custom agent %q must be registered", customAgentID)

	return al, customAgentID, filepath.Join(tmpHome, "system"), hook
}

// findDenyEntries returns the tool.policy.deny.attempted / decision=deny entries
// in the audit file at dir/audit.jsonl.
func findDenyEntries(t *testing.T, auditDir string) []map[string]any {
	t.Helper()
	auditPath := filepath.Join(auditDir, "audit.jsonl")
	require.FileExists(t, auditPath, "audit.jsonl must exist — AuditLog=true and a deny event was expected")
	entries, err := readAuditEntries(auditPath)
	require.NoError(t, err, "audit.jsonl must be readable and valid JSONL")
	var deny []map[string]any
	for _, e := range entries {
		if e["event"] == audit.EventToolPolicyDenyAttempted && e["decision"] == audit.DecisionDeny {
			deny = append(deny, e)
		}
	}
	return deny
}

// TestRunTurn_HookDeny_AuditedWithUser is the core regression proof: a WS/CLI
// turn (UserID="cli") whose tool call is denied by the approval HOOK produces a
// tool.policy.deny.attempted audit entry attributed to User="cli".
//
// Before the fix this entry did not exist at all (the deny branch `continue`d
// without auditing). The dangerousStubTool must never execute, and the deny
// entry must name the tool and carry the gateway principal.
//
// Traces to: cli-minimization-spec.md FR-017 / SC-004 / §TDD Plan #12.
func TestRunTurn_HookDeny_AuditedWithUser(t *testing.T) {
	al, agentID, auditDir, hook := newHookDenyFixture(t)

	customAgent, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok)

	ctx := context.Background()
	_, err := al.runAgentLoop(ctx, customAgent, processOptions{
		SessionKey:      "hookdeny-session-cli",
		Channel:         "webchat",
		ChatID:          "direct",
		UserMessage:     "please run dangerous_tool",
		UserID:          "cli", // the WS-authenticated gateway principal (FR-017)
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err, "policy deny is a loop-level action, not an agent error")

	// The hook must actually have been consulted for the tool — proves the deny
	// came from the hook branch (not a different deny path).
	require.True(t, hook.wasCalled, "the approval hook must have been consulted")
	assert.Equal(t, "dangerous_tool", hook.lastToolID)

	deny := findDenyEntries(t, auditDir)
	require.NotEmpty(t, deny,
		"the hook-deny branch must now emit a tool.policy.deny.attempted audit entry")

	// At least one deny entry must name the tool AND carry User="cli".
	var attributed map[string]any
	for _, e := range deny {
		if e["tool"] == "dangerous_tool" && e["user"] == "cli" {
			attributed = e
			break
		}
	}
	require.NotNilf(t, attributed,
		"a hook-deny audit entry for dangerous_tool with user=\"cli\" must exist; deny entries: %v", deny)

	// Sanity: the entry carries real values, not blanks.
	assert.Equal(t, string(audit.EventToolPolicyDenyAttempted), attributed["event"])
	assert.Equal(t, string(audit.DecisionDeny), attributed["decision"])
	assert.Equal(t, agentID, attributed["agent_id"])
}

// TestRunTurn_HookDeny_NoUser_WhenUnauthenticated pins the channel/non-gateway
// invariant: when no gateway principal is present (UserID empty — channel,
// env-token, or dev-bypass turns), the hook-deny audit entry is still written
// (the security event must always be recorded) but its User field is omitted —
// never guessed. This proves the fix did not change behavior for non-gateway
// principals.
//
// Traces to: cli-minimization-spec.md FR-017 (channel-originated turns leave
// User empty).
func TestRunTurn_HookDeny_NoUser_WhenUnauthenticated(t *testing.T) {
	al, agentID, auditDir, hook := newHookDenyFixture(t)

	customAgent, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok)

	ctx := context.Background()
	_, err := al.runAgentLoop(ctx, customAgent, processOptions{
		SessionKey:      "hookdeny-session-anon",
		Channel:         "telegram", // a channel turn — no gateway principal
		ChatID:          "direct",
		UserMessage:     "please run dangerous_tool",
		UserID:          "", // no authenticated gateway principal
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err)
	require.True(t, hook.wasCalled, "the approval hook must have been consulted")

	deny := findDenyEntries(t, auditDir)
	require.NotEmpty(t, deny,
		"the deny must still be audited even when no gateway principal is present")

	var forTool map[string]any
	for _, e := range deny {
		if e["tool"] == "dangerous_tool" {
			forTool = e
			break
		}
	}
	require.NotNilf(t, forTool, "a deny entry for dangerous_tool must exist; deny entries: %v", deny)

	_, present := forTool["user"]
	assert.False(t, present,
		"user key must be omitted when no gateway principal is authenticated; entry: %v", forTool)

	// The deny was still recorded with real values (fail-open empty, not fail-to-emit).
	assert.Equal(t, agentID, forTool["agent_id"])
}
