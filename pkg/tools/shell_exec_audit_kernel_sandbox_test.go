// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Functional proof for the bash tool's own EventExec audit row carrying
// "kernel_sandbox" and "mode" [2026-09-24, founder decision]: since Auto no
// longer requires an enforcing kernel sandbox, this is how an operator finds
// every bash call that ran with or without kernel confinement, alongside the
// tool.auto_approved proof in pkg/agent/auto_approve_gate_test.go and
// pkg/audit/tool_auto_approve_events_test.go.

package tools

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// runBenignCommandAudited runs `echo hi` through the real ExecTool.Execute
// path (not enforceShellPermissionMode directly), with a real audit logger
// wired, and returns the decoded EventExec allow-decision row.
func runBenignCommandAudited(t *testing.T) auditRow {
	t.Helper()
	workspace := t.TempDir()
	tool, err := NewExecTool(workspace, true)
	require.NoError(t, err)

	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 1})
	require.NoError(t, err)
	tool.SetAuditLogger(logger)

	ctx := WithToolContext(context.Background(), "cli", "")
	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "echo hi",
	})
	require.False(t, result.IsError, "a benign command must not error: %s", result.ForLLM)

	rows := readAuditRows(t, logger, dir)
	var execRows []auditRow
	for _, r := range rows {
		if r.Event == string(audit.EventExec) && r.Decision == string(audit.DecisionAllow) {
			execRows = append(execRows, r)
		}
	}
	require.Len(t, execRows, 1, "exactly one allow-decision exec row for one command")
	return execRows[0]
}

// TestExecTool_AuditRecordsKernelSandboxState_NoSandbox proves the
// no-sandbox half: with no kernel policy base registered, the exec audit row
// must record kernel_sandbox=false — not omit the field, not default it
// true.
func TestExecTool_AuditRecordsKernelSandboxState_NoSandbox(t *testing.T) {
	sandbox.RegisterTurnPolicyBase(nil)
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	require.False(t, sandbox.TurnPolicyBaseInstalled(), "setup: no kernel policy base must be registered")

	row := runBenignCommandAudited(t)
	assert.Equal(t, false, row.Details["kernel_sandbox"],
		"the exec audit row must record kernel_sandbox=false when no kernel sandbox is enforcing")
}

// TestExecTool_AuditRecordsKernelSandboxState_WithSandbox proves the
// sandbox-enforcing half, so the two tests together prove the field tracks
// real state rather than being hardcoded either way.
func TestExecTool_AuditRecordsKernelSandboxState_WithSandbox(t *testing.T) {
	home, err := os.MkdirTemp("", "kernel-sandbox-audit-*")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: home})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	require.True(t, sandbox.TurnPolicyBaseInstalled(), "setup: a kernel policy base must be registered")

	row := runBenignCommandAudited(t)
	assert.Equal(t, true, row.Details["kernel_sandbox"],
		"the exec audit row must record kernel_sandbox=true when a kernel sandbox is enforcing")
}
