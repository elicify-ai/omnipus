//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// When CGO is enabled, pkg/gateway imports pkg/channels/matrix which requires
// the libolm system library (olm/olm.h). If that library is installed,
// remove this build constraint and run tests normally.
//
// NOTE (ADR-036 §3.4): this file used to hold the wsApprovalHook /
// wsApprovalRegistry / autoApproveSafeTool / handleApprovalResponse test suite
// for the legacy exec_approval_request/response WS-frame gate ("gate 1"). That
// gate was retired — it ran BEFORE the TOCTOU "ask" branch in runTurn and
// unconditionally denied every ask-policy tool call after a 90s timeout once
// its answering frontend UI (ExecApprovalBlock/ExecApprovalTool) was removed,
// making the tool-approval REST path (the only path that was ever actually
// reachable) unreachable in practice. See pkg/agent/loop.go's
// CheckGrantOrRequestApproval doc comment for the fix, and
// pkg/gateway/ws_approval_grants_test.go for the rewritten grant-consultation
// regression tests. resolveApprovalToolPolicy (tested below) is a SEPARATE,
// still-live policy-resolution helper unrelated to the retired gate; its test
// survives unchanged.

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestResolveApprovalToolPolicy_InfraForceAllow proves the fix for the two-gate
// infra bug: the tool-approval exec gate must force-allow infra tools (load_tool) for a
// deny-by-default agent (e.g. Ava), or every lazy/load-on-demand tool becomes
// unreachable at execution time. The force-allow must stay NARROW — it must not
// widen any non-infra tool the agent's policy denies.
func TestResolveApprovalToolPolicy_InfraForceAllow(t *testing.T) {
	// Ava: deny-by-default; create_agent/list_models allowed; load_tool NOT listed.
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{
					ID: "ava",
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							DefaultPolicy: config.ToolPolicyDeny,
							Policies: map[string]config.ToolPolicy{
								"create_agent": config.ToolPolicyAllow,
								"list_models":  config.ToolPolicyAllow,
							},
						},
					},
				},
			},
		},
	}

	// THE FIX: load_tool (infra) is always "allow" even for a deny-default agent.
	assert.Equal(t, "allow", resolveApprovalToolPolicy(cfg, "load_tool", "ava"),
		"load_tool (infra) must be force-allowed for a deny-default agent")
	// An allowed lazy tool stays allowed.
	assert.Equal(t, "allow", resolveApprovalToolPolicy(cfg, "create_agent", "ava"))
	// A tool the agent denies is STILL denied — the force-allow must not widen.
	assert.Equal(t, "deny", resolveApprovalToolPolicy(cfg, "exec", "ava"))
	// Infra force-allow holds even with a nil config; non-infra falls back to "ask".
	assert.Equal(t, "allow", resolveApprovalToolPolicy(nil, "load_tool", "ava"))
	assert.Equal(t, "ask", resolveApprovalToolPolicy(nil, "exec", "ava"))
}
