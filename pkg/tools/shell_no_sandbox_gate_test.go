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
	cases := []string{
		"ls", "cat notes/x", "git status",
		// 2026-09-24 fix: the POSIX no-op/condition-check builtins — none of
		// them can write a file or reach the network by themselves — plus
		// the exact `true; echo $?` idiom
		// conformance-design-chat-e2e.spec.ts's t0 goal-claim steer asks a
		// worker to run (a genuine CI hang before this fix: `echo $?`
		// tripped the D7 FR-020 blind spot below, which fired even with a
		// kernel sandbox enforcing — see the WithSandbox test in this file).
		"true", "false", ":", "exit 0", "true; echo $?",
	}
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
		// 2026-09-24 fix: a chain with ONE read-only-eligible segment
		// (`true`) and one that is not (`touch`, a write, not on either
		// read-only list) must still ask — every segment must clear the
		// allowlist, not just one (commandIsNoSandboxReadOnly's own
		// contract).
		"true && touch x",
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

// TestEnforceShellPermissionMode_WithSandbox_BareExitStatusDoesNotBlindEscalate
// reproduces the genuine CI hang this file's own gate turned out NOT to be
// the cause of (2026-09-24): with a kernel sandbox enforcing —
// sandbox.TurnPolicyBaseInstalled() true, exactly the CI condition
// (gateway health showed landlock-v7/mode=enforce) — founder decision A's
// gate above is a correct no-op (proven by the test above), but D7's
// filesystem pre-flight (enforceFSPreflight, shell_permission_mode.go) ran
// regardless and asked anyway: tokenizeShellWords (preflight.go) treated the
// bare `$?` in `true; echo $?` — the EXACT command
// conformance-design-chat-e2e.spec.ts's t0 goal-claim steer asks a worker to
// run — as an unparseable FR-020 blind spot (the same "any $ byte
// disqualifies" rule meant for `$(...)`/backtick substitution), opening a
// live "could not be classified" approval card that CI's headless-less chat
// session never answers. Proves the fix: this exact idiom now runs with
// zero approver calls even though the sandbox IS enforcing.
func TestEnforceShellPermissionMode_WithSandbox_BareExitStatusDoesNotBlindEscalate(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)

	perm, result := tool.enforceShellPermissionMode(ctx, "true; echo $?")
	require.Nil(t, result, "true; echo $? must not be refused, kernel sandbox enforcing or not")
	require.NotNil(t, perm)
	assert.Equal(t, 0, requester.callCount(), "a bare $? exit-status read must never blind-escalate D7, sandbox or no sandbox")
}

// TestEnforceShellPermissionMode_WithSandbox_ExitStatusGluedToPathStillBlindEscalates
// pins the narrow shape of the fix above: `$?` glued onto a larger token
// (not a bare, standalone word) still disqualifies tokenizeShellWords and
// still asks. This matters because a classifier that returned literal text
// containing `$?` as a path/host to check would be checking a FAKE string —
// the real shell resolves a different path once `$?` actually expands at
// run time — so this shape must keep failing closed, not silently pass a
// mismatched classification through.
//
// callCount is 2, not 1: with a kernel sandbox enforcing, D7's and D8's
// blind-spot postures are independent and both fire for one unparseable
// command — TestEnforceShellPermissionMode_NoKernelSandbox_BlindCommandEscalates's
// own doc comment names this exact "doubled blind-spot posture" as the
// documented with-sandbox behaviour (only the no-sandbox gate collapses the
// two into one ask; this test is the with-sandbox path, so that collapse
// does not apply). This test's job is narrower: proving `$?` glued to a
// path still counts as unparseable at all, not the ask-count contract.
func TestEnforceShellPermissionMode_WithSandbox_ExitStatusGluedToPathStillBlindEscalates(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)

	perm, result := tool.enforceShellPermissionMode(ctx, "cat /etc/passwd$?")
	require.Nil(t, result, "an APPROVED escalation must not refuse the command")
	require.NotNil(t, perm)
	assert.Equal(t, 2, requester.callCount(), "$? glued onto a larger token must still ask on both D7 and D8 — only a bare, standalone $? is exempt")
}
