// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Functional proof for ADR-092's FR-032(b)/FR-046 shell.preflight_escalation
// and FR-032(d)/FR-046 shell.approval_decision audit events — the two
// decision points this file (shell_permission_mode.go) emits directly,
// distinct from FR-032(c) shell.grant_recorded (proven in
// pkg/security/approvalgrants_audit_test.go, colocated with the actual
// grant-store mutation).
//
// Reuses permTestFixture/fakeShellApprovalRequester from
// shell_permission_mode_test.go (same package) and wires a real
// *audit.Logger via ExecTool.SetAuditLogger so these tests read back real
// JSONL rows, not a mock — proving the events are reachable end to end, not
// just constructible.

package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

// wireAuditedFixture builds on permTestFixture, additionally wiring a real
// *audit.Logger onto the tool so the FR-032(b)/(c)/(d) emitters have
// somewhere to write.
func wireAuditedFixture(t *testing.T, mode ShellMode, approve bool) (tool *ExecTool, ctx context.Context, requester *fakeShellApprovalRequester, dir string) {
	t.Helper()
	tool, ctx, requester, _ = permTestFixture(t, mode, approve)

	dir = t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 1})
	require.NoError(t, err)
	tool.SetAuditLogger(logger)
	return tool, ctx, requester, dir
}

func rowsForEventType(rows []auditRow, event string) []auditRow {
	var out []auditRow
	for _, r := range rows {
		if r.Event == event {
			out = append(out, r)
		}
	}
	return out
}

// --- FR-032(b)/(d): filesystem pre-flight, approved ---

func TestEnforceShellPermissionMode_FSPreflightEmitsEscalationAndAllowWithGrantDecision(t *testing.T) {
	tool, ctx, requester, dir := wireAuditedFixture(t, ShellModeAuto, true)

	outsideDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	outsideFile := filepath.Join(outsideDir, "out.txt")
	cmd := "echo hi > " + outsideFile

	perm, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result)
	require.NotNil(t, perm)
	require.Equal(t, 1, requester.callCount())

	rows := readAuditRows(t, tool.auditLogger, dir)

	escalations := rowsForEventType(rows, audit.EventShellPreflightEscalation)
	require.Len(t, escalations, 1, "an fs escalation must emit exactly one shell.preflight_escalation row")
	assert.Equal(t, "bash", escalations[0].Tool)
	assert.Equal(t, "agent-1", escalations[0].AgentID)
	assert.Equal(t, "session-1", escalations[0].SessionID)
	assert.Equal(t, "filesystem", escalations[0].detail("kind"))
	assert.NotEmpty(t, escalations[0].detail("matched"), "must name the rule/path that tripped (SEC-17)")
	assert.Contains(t, escalations[0].detail("requested"), "write")

	decisions := rowsForEventType(rows, audit.EventShellApprovalDecision)
	require.Len(t, decisions, 1, "the approval must emit exactly one shell.approval_decision row")
	assert.Equal(t, audit.DecisionAllow, decisions[0].Decision)
	assert.Equal(t, "fs_preflight", decisions[0].detail("kind"))
	assert.Equal(t, string(audit.ShellApprovalAllowWithGrant), decisions[0].detail("outcome"),
		"an approved fs_preflight escalation always records a grant right after — must be allow_with_grant, not allow_once")

	grants := rowsForEventType(rows, audit.EventShellGrantRecorded)
	require.Len(t, grants, 1, "the recorded PathGrant must emit exactly one shell.grant_recorded row")
	assert.Equal(t, "path_widening", grants[0].detail("scope"))
	assert.Equal(t, outsideFile, grants[0].detail("path"))
}

// --- FR-032(b)/(d): filesystem pre-flight, denied ---

func TestEnforceShellPermissionMode_FSPreflightDeniedEmitsDenyDecisionAndNoGrant(t *testing.T) {
	tool, ctx, requester, dir := wireAuditedFixture(t, ShellModeAuto, false)

	outsideDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	cmd := "echo hi > " + filepath.Join(outsideDir, "out.txt")

	perm, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, perm)
	require.NotNil(t, result)
	require.Equal(t, 1, requester.callCount())

	rows := readAuditRows(t, tool.auditLogger, dir)

	require.Len(t, rowsForEventType(rows, audit.EventShellPreflightEscalation), 1)

	decisions := rowsForEventType(rows, audit.EventShellApprovalDecision)
	require.Len(t, decisions, 1)
	assert.Equal(t, audit.DecisionDeny, decisions[0].Decision)
	assert.Equal(t, string(audit.ShellApprovalDeny), decisions[0].detail("outcome"))
	assert.NotEmpty(t, decisions[0].detail("reason"))

	assert.Empty(t, rowsForEventType(rows, audit.EventShellGrantRecorded), "a denied escalation must never record a grant")
}

// --- FR-032(b)/(d): network pre-flight, approved ---

func TestEnforceShellPermissionMode_NetworkPreflightEmitsEscalationAndAllowWithGrantDecision(t *testing.T) {
	tool, ctx, requester, dir := wireAuditedFixture(t, ShellModeAuto, true)

	perm, result := tool.enforceShellPermissionMode(ctx, "curl https://example.com")
	require.Nil(t, result)
	require.True(t, perm.networkGranted)
	require.Equal(t, 1, requester.callCount())

	rows := readAuditRows(t, tool.auditLogger, dir)

	escalations := rowsForEventType(rows, audit.EventShellPreflightEscalation)
	require.Len(t, escalations, 1)
	assert.Equal(t, "network", escalations[0].detail("kind"))

	decisions := rowsForEventType(rows, audit.EventShellApprovalDecision)
	require.Len(t, decisions, 1)
	assert.Equal(t, "network_preflight", decisions[0].detail("kind"))
	assert.Equal(t, string(audit.ShellApprovalAllowWithGrant), decisions[0].detail("outcome"))

	grants := rowsForEventType(rows, audit.EventShellGrantRecorded)
	require.Len(t, grants, 1)
	assert.Equal(t, "network_widening", grants[0].detail("scope"))
}

// Reuse (grant already recorded) must not re-emit any of the three events.
func TestEnforceShellPermissionMode_NetworkPreflightReuseDoesNotReEmit(t *testing.T) {
	tool, ctx, _, dir := wireAuditedFixture(t, ShellModeAuto, true)

	_, result := tool.enforceShellPermissionMode(ctx, "curl https://example.com")
	require.Nil(t, result)
	_, result2 := tool.enforceShellPermissionMode(ctx, "curl https://example.com/other")
	require.Nil(t, result2)

	rows := readAuditRows(t, tool.auditLogger, dir)
	assert.Len(t, rowsForEventType(rows, audit.EventShellPreflightEscalation), 1, "the second call reuses the grant — no second escalation")
	assert.Len(t, rowsForEventType(rows, audit.EventShellApprovalDecision), 1)
	assert.Len(t, rowsForEventType(rows, audit.EventShellGrantRecorded), 1)
}

// --- FR-032(d): D3 ask-rule branch — allow_once (no grant recorded today) ---

func TestEnforceShellPermissionMode_RuleAskApprovedEmitsAllowOnceDecision(t *testing.T) {
	tool, ctx, requester, dir := wireAuditedFixture(t, ShellModeAuto, true)

	resolvedTrue, err := shellrule.ResolveBinary("true", os.Getenv("PATH"))
	require.NoError(t, err)
	tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: resolvedTrue}}

	perm, result := tool.enforceShellPermissionMode(ctx, "true")
	require.Nil(t, result)
	require.NotNil(t, perm)
	require.Equal(t, 1, requester.callCount())

	rows := readAuditRows(t, tool.auditLogger, dir)

	assert.Empty(t, rowsForEventType(rows, audit.EventShellPreflightEscalation),
		"a D3 rule-ask verdict is not a D7/D8 pre-flight escalation — FR-032(b) is scoped to FR-009/FR-042")

	decisions := rowsForEventType(rows, audit.EventShellApprovalDecision)
	require.Len(t, decisions, 1)
	assert.Equal(t, "rule_ask", decisions[0].detail("kind"))
	assert.Equal(t, string(audit.ShellApprovalAllowOnce), decisions[0].detail("outcome"),
		"requestRuleApproval records no grant today — must be allow_once, not allow_with_grant")

	assert.Empty(t, rowsForEventType(rows, audit.EventShellGrantRecorded))
}

func TestEnforceShellPermissionMode_RuleAskDeniedEmitsDenyDecision(t *testing.T) {
	tool, ctx, requester, dir := wireAuditedFixture(t, ShellModeAuto, false)

	resolvedTrue, err := shellrule.ResolveBinary("true", os.Getenv("PATH"))
	require.NoError(t, err)
	tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: resolvedTrue}}

	_, result := tool.enforceShellPermissionMode(ctx, "true")
	require.NotNil(t, result)
	require.Equal(t, 1, requester.callCount())

	rows := readAuditRows(t, tool.auditLogger, dir)
	decisions := rowsForEventType(rows, audit.EventShellApprovalDecision)
	require.Len(t, decisions, 1)
	assert.Equal(t, audit.DecisionDeny, decisions[0].Decision)
	assert.Equal(t, string(audit.ShellApprovalDeny), decisions[0].detail("outcome"))
}

// --- Nil audit logger: pure no-op, never blocks enforcement ---

func TestEnforceShellPermissionMode_NilAuditLoggerDoesNotBlockEnforcement(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true) // no SetAuditLogger call

	outsideDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	cmd := "echo hi > " + filepath.Join(outsideDir, "out.txt")

	perm, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result)
	require.NotNil(t, perm)
	assert.Equal(t, 1, requester.callCount())
}
