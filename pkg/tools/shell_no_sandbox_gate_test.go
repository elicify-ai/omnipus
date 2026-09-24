// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Founder decision A (2026-09-24, "like Claude Code default"): with NO
// kernel sandbox enforcing, Auto must not auto-run an arbitrary command.
// These tests are written to FAIL against the pre-decision code (which ran
// any command under Auto with no sandbox unless D7/D8 happened to flag it —
// neither classifier sees a relative-path write or a bare `cd`/`bash -c`)
// and PASS once shell_no_sandbox_gate.go's new escalation is wired in.
package tools

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

// TestEnforceShellPermissionMode_NoSandbox_ReadOnlyCommandsRunWithZeroCalls
// is founder decision A(1)(a): ls, cat <relative path>, and git status (one
// of the four named read-only git subcommands) must run with NO approver
// call at all — not even D7/D8's own, which this read-only path bypasses
// entirely (see commandIsNoSandboxReadOnly's own doc comment on why both
// are structurally inapplicable to these commands).
func TestEnforceShellPermissionMode_NoSandbox_ReadOnlyCommandsRunWithZeroCalls(t *testing.T) {
	cases := []string{"ls", "cat notes/x", "git status"}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			tool, ctx, requester, _ := permTestFixtureNoSandbox(t, ShellModeAuto, true)

			perm, result := tool.enforceShellPermissionMode(ctx, cmd)
			require.Nil(t, result, "a read-only command must never be refused: %q", cmd)
			require.NotNil(t, perm)
			assert.Equal(t, 0, requester.callCount(), "%q: a read-only command must run with zero approver calls, no sandbox", cmd)
		})
	}
}

// TestEnforceShellPermissionMode_NoSandbox_NonReadOnlyCommandsAskExactlyOnce
// is founder decision A(2): a command not on the read-only allowlist and
// not covered by an operator allow rule must ask — exactly once, not
// stacked with a separate D7/D8 prompt for the same call (mutual
// exclusivity — see commandTriggersExistingPreflight's own doc comment).
// Each of these is a shape NEITHER D7 (absolute-path-only classifier) NOR
// D8 (no filesystem/network signature at all) would flag on its own.
func TestEnforceShellPermissionMode_NoSandbox_NonReadOnlyCommandsAskExactlyOnce(t *testing.T) {
	cases := []string{
		"touch notes/x",
		"cd .. && rm -rf x",
		`bash -c "echo"`,
		"echo hi > notes/y",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			tool, ctx, requester, _ := permTestFixtureNoSandbox(t, ShellModeAuto, true)

			perm, result := tool.enforceShellPermissionMode(ctx, cmd)
			require.Nil(t, result, "an APPROVED no-sandbox escalation must not refuse the command: %q", cmd)
			require.NotNil(t, perm)
			assert.Equal(t, 1, requester.callCount(), "%q: must ask exactly once with no sandbox", cmd)
		})
	}
}

// TestEnforceShellPermissionMode_NoSandbox_AsksCarriesRealTurnID proves the
// escalation reaches the approver with the D-03 fix's real, non-empty turn
// ID — reusing shell_permission_mode_turnid_test.go's own
// turnIDCapturingApprovalRequester rather than a third copy of the same
// fake.
func TestEnforceShellPermissionMode_NoSandbox_AsksCarriesRealTurnID(t *testing.T) {
	tool, ctx, _, _ := permTestFixtureNoSandbox(t, ShellModeAuto, true)
	capture := &turnIDCapturingApprovalRequester{approve: true}
	tool.approvalRequester = capture

	perm, result := tool.enforceShellPermissionMode(ctx, "touch notes/x")
	require.Nil(t, result)
	require.NotNil(t, perm)
	require.Equal(t, 1, capture.calls)
	assert.NotEmpty(t, capture.lastTurnID, turnIDAssertionMsg)
}

// TestEnforceShellPermissionMode_NoSandbox_AsksCarriesKindAndNote proves the
// approval request itself carries the "no_sandbox_ask" adr092_kind and the
// exact operator-facing note the founder decision names.
func TestEnforceShellPermissionMode_NoSandbox_AsksCarriesKindAndNote(t *testing.T) {
	tool, ctx, requester, _ := permTestFixtureNoSandbox(t, ShellModeAuto, true)

	perm, result := tool.enforceShellPermissionMode(ctx, "touch notes/x")
	require.Nil(t, result)
	require.NotNil(t, perm)
	require.NotNil(t, requester.lastArg)
	assert.Equal(t, "no_sandbox_ask", requester.lastArg["adr092_kind"])
	assert.Equal(t, noSandboxAskNote, requester.lastArg["note"])
}

// TestEnforceShellPermissionMode_NoSandbox_DeniedEscalationRefusesOutright
// mirrors the existing D7/D8 no-sandbox refusal proofs: a denied no_sandbox_
// ask escalation must refuse the command outright, never run it un-widened.
func TestEnforceShellPermissionMode_NoSandbox_DeniedEscalationRefusesOutright(t *testing.T) {
	tool, ctx, requester, _ := permTestFixtureNoSandbox(t, ShellModeAuto, false)

	perm, result := tool.enforceShellPermissionMode(ctx, "touch notes/x")
	require.Nil(t, perm, "a denied escalation must not return a usable permission result")
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Equal(t, 1, requester.callCount())
}

// TestEnforceShellPermissionMode_NoSandbox_AllowRuleRuns is founder
// decision A(1)(b): an operator allow rule that fully covers the command
// lets it run — this gate's own prompt is skipped, though (unlike the
// read-only allowlist path) D7/D8 still apply on top, matching the existing
// TestAllowRule_DoesNotSuppressFSPreflightUnderAuto precedent.
func TestEnforceShellPermissionMode_NoSandbox_AllowRuleRuns(t *testing.T) {
	tool, ctx, _, _ := permTestFixtureNoSandbox(t, ShellModeAuto, true)
	tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionAllow, Binary: "npm", ArgPrefix: "test"}}

	perm, result := tool.enforceShellPermissionMode(ctx, "npm test")
	require.Nil(t, result, "npm test must run when an allow rule fully covers it")
	require.NotNil(t, perm)
}

// TestEnforceShellPermissionMode_WithSandbox_NoSandboxGateNeverFires proves
// founder decision A(3): with a kernel sandbox enforcing, behaviour is
// completely unchanged — the new gate must never fire even for a command
// that would trigger it with no sandbox.
func TestEnforceShellPermissionMode_WithSandbox_NoSandboxGateNeverFires(t *testing.T) {
	tool, ctx, requester, workDir := permTestFixture(t, ShellModeAuto, true)

	perm, result := tool.enforceShellPermissionMode(ctx, "touch "+filepath.Join(workDir, "x"))
	require.Nil(t, result)
	require.NotNil(t, perm)
	assert.Equal(t, 0, requester.callCount(), "an in-workspace write must run silently with a kernel sandbox enforcing, unchanged by founder decision A")
}
