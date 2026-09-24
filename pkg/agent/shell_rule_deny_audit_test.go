// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// D-12 security-review fix (MEDIUM, 2026-09-24): emitShellRuleSettledAudit
// (loop_policy.go) was called for BOTH of bashRuleVerdict.settlesPrompt()'s
// cases — a D3 ALLOW verdict (every segment allowed) AND a D3 DENY verdict
// — but it hardcodes ShellApprovalAllowOnce/"rule_fully_allowed", so a
// command a deny rule was about to refuse produced a FALSE
// shell.approval_decision "allow" audit row. This test drives a real turn
// end to end and is written to FAIL against the pre-fix call site (which
// gates on the broader settlesPrompt(), true for both cases) and PASS once
// it gates on fullyAllowedSettlesPrompt() instead.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

// TestRunTurn_DenyRule_WritesNoFalseAllowDecision is the D-12 regression: a
// bash call an operator D3 DENY rule refuses must never produce a
// shell.approval_decision row claiming "allow" — exactly one truthful
// decision row (the exec-tool's own EventExec/deny row) must exist for it.
func TestRunTurn_DenyRule_WritesNoFalseAllowDecision(t *testing.T) {
	cfg, _ := baseLoopDenialTestConfig(t)
	cfg.Sandbox.ToolPolicies = map[string]string{"bash": "ask"}
	cfg.Sandbox.AutoApprove = false
	cfg.Sandbox.CommandRules = []shellrule.Rule{{Action: shellrule.ActionDeny, Binary: "rm"}}

	provider := testutil.NewScenario().WithToolCall("bash", `{"command":"rm -rf /tmp/x"}`).WithText("done")
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	setAskPolicyForAllAgents(t, al, "bash", config.ToolPolicyAsk)
	approver := &countingDenyApprover{reason: "user"}
	al.SetToolApprover(approver)

	_, err := al.ProcessDirect(context.Background(), "run rm", "deny-rule-turn")
	require.NoError(t, err)
	assert.Equal(t, 0, approver.callCount(), "a D3 deny rule settles the call without ever consulting a human")

	var auditPath string
	require.NoError(t, filepath.WalkDir(al.homePath, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() == "audit.jsonl" {
			auditPath = path
		}
		return nil
	}))
	require.NotEmpty(t, auditPath, "audit.jsonl must exist under %s — audit_log=true was requested", al.homePath)

	entries := readAuditEvents(t, auditPath)
	sawExecDeny := false
	for _, e := range entries {
		if e["event"] == "shell.approval_decision" {
			require.NotEqual(t, "allow", e["decision"],
				"D-12 regression: a deny-rule command must never write an \"allow\" shell.approval_decision row: %+v", e)
		}
		if e["event"] == "exec" && e["decision"] == "deny" {
			if cmd, _ := e["command"].(string); cmd == "rm -rf /tmp/x" {
				sawExecDeny = true
			}
		}
	}
	assert.True(t, sawExecDeny, "expected the exec-tool's own deny row (EventExec/DecisionDeny) for the refused command; entries=%+v", entries)
}
