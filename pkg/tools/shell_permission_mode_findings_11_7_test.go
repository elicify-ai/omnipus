// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/shellrule"
)

// TestEnforceShellPermissionMode_AskModeD3AskRuleDenialRefuses is the
// regression test for review finding #11 (HIGH, 2026-09-23 security fix
// lane): "rule {action: ask, binary: npm, arg_prefix: publish} with bash =
// allow, and `npm publish` runs unprompted." A bash tool policy resolved
// directly to "allow" pins ShellModeAsk here (the classic ask-policy gate
// never ran, since it only fires when the ceiling resolves to literally
// "ask") — before this fix, enforceShellPermissionMode's D3 ask-rule branch
// only fired under mode==ShellModeAuto, so this scenario produced NO prompt
// ANYWHERE and the command ran straight through.
//
// This test proves the fixed property end-to-end: a denied D3 ask rule in
// ShellModeAsk now actually REFUSES the command (not merely "the requester
// was called" — a refusal is the only proof that matters for a security
// finding).
func TestEnforceShellPermissionMode_AskModeD3AskRuleDenialRefuses(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAsk, false) // approve=false: the human denies

	resolvedTrue, err := shellrule.ResolveBinary("true", os.Getenv("PATH"))
	require.NoError(t, err)
	tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: resolvedTrue}}

	perm, result := tool.enforceShellPermissionMode(ctx, "true")
	require.Nil(t, perm, "a denied D3 ask rule must refuse the command outright")
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Equal(t, 1, requester.callCount(),
		"finding #11 regression: a D3 ask rule under an 'allow'-ceiling-collapsed ShellModeAsk must reach the approval requester")
}

// TestEnforceShellPermissionMode_HeadlessAutoDeniesD3AskRule is the
// regression test for review finding #7 (MEDIUM): the lead decision that
// "anything that would need a human prompt (a plain Ask with Auto off, a D3
// ask rule, a D7/D8 escalation) is auto-DENIED headless, with a clear error
// in the result" — applied to the NEW D3 ask-rule call site this ADR-092
// lane added. Before this fix, requestRuleApproval had no AutoDenyAsk
// check at all and would have gone straight to the (possibly
// indefinitely-blocking, or nop-fallback-with-a-generic-reason) interactive
// requester on a headless run.
func TestEnforceShellPermissionMode_HeadlessAutoDeniesD3AskRule(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true) // approve=true: would approve if ever asked
	ctx = WithAutoDenyAsk(ctx, true)

	resolvedTrue, err := shellrule.ResolveBinary("true", os.Getenv("PATH"))
	require.NoError(t, err)
	tool.commandRules = []shellrule.Rule{{Action: shellrule.ActionAsk, Binary: resolvedTrue}}

	perm, result := tool.enforceShellPermissionMode(ctx, "true")
	require.Nil(t, perm)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Equal(t, 0, requester.callCount(),
		"a headless run must never reach the interactive requester — it stalls otherwise")
	assert.Contains(t, result.ForLLM, "headless", "the refusal must name why, not just that it was refused")
}

// TestEnforceShellPermissionMode_HeadlessAutoDeniesFSPreflightEscalation is
// finding #7's D7 half: a filesystem pre-flight escalation under Auto must
// also auto-deny headless rather than block.
func TestEnforceShellPermissionMode_HeadlessAutoDeniesFSPreflightEscalation(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)
	ctx = WithAutoDenyAsk(ctx, true)

	outsideDir, err := os.MkdirTemp("", "headless-fs-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(outsideDir) })
	cmd := "echo hi > " + outsideDir + "/out.txt"

	perm, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, perm)
	require.NotNil(t, result)
	assert.True(t, result.IsError)
	assert.Equal(t, 0, requester.callCount(), "headless D7 escalation must not reach the interactive requester")
}
