// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnforceShellPermissionMode_AllowOnceDoesNotPersistPathGrant is the
// regression test for review finding #5 (MEDIUM, 2026-09-23 security fix
// lane): "'Allow once' on an Auto escalation still grants for the whole
// session." Before this fix, requestPreflightApproval's underlying
// ShellApprovalRequester returned only (bool, string), so
// enforceFSPreflight could not tell "Allow once" apart from "Allow" and
// called RecordPathGrant unconditionally on any approval — a human clicking
// "Allow once" on a D7 escalation still widened the session for every LATER
// command touching the same path.
//
// This test simulates "Allow once" (approve=true, recordGrant=false) and
// proves: (a) the FIRST command still runs (the widening applies to THIS
// call), and (b) a SECOND, otherwise-identical command re-prompts rather
// than silently reusing a grant that was never supposed to survive past the
// first call.
func TestEnforceShellPermissionMode_AllowOnceDoesNotPersistPathGrant(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)
	requester.recordGrant = false // "Allow once", not "Allow"

	outsideDir := t.TempDir()
	outsideFile := outsideDir + "/out.txt"
	cmd := "echo hi > " + outsideFile

	perm, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result, "an APPROVED escalation (even allow-once) must let THIS call run")
	require.NotNil(t, perm)
	require.Len(t, perm.pathGrants, 1, "the widening must still apply to the call that was just approved")
	assert.Equal(t, 1, requester.callCount())

	// The persistent store must hold nothing — "Allow once" recorded no
	// session grant.
	persisted := tool.approvalGrants.PathGrantsFor("session-1", "agent-1")
	assert.Empty(t, persisted, "finding #5 regression: 'Allow once' must not persist a PathGrant into the session store")

	// A second, otherwise-identical command must re-prompt — the grant did
	// not survive past the first call.
	perm2, result2 := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result2)
	require.NotNil(t, perm2)
	assert.Equal(t, 2, requester.callCount(),
		"finding #5 regression: 'Allow once' must not suppress the prompt on a later, otherwise-identical command")
}

// TestEnforceShellPermissionMode_AllowOnceDoesNotPersistNetworkGrant is
// finding #5's D8 (network) half.
func TestEnforceShellPermissionMode_AllowOnceDoesNotPersistNetworkGrant(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)
	requester.recordGrant = false // "Allow once", not "Allow"

	perm, result := tool.enforceShellPermissionMode(ctx, "curl https://example.com")
	require.Nil(t, result)
	require.NotNil(t, perm)
	assert.True(t, perm.networkGranted, "the widening must still apply to the call that was just approved")
	assert.Equal(t, 1, requester.callCount())

	assert.False(t, tool.approvalGrants.HasNetworkGrant("session-1", "agent-1"),
		"finding #5 regression: 'Allow once' must not persist a network grant into the session store")

	_, result2 := tool.enforceShellPermissionMode(ctx, "curl https://example.com/other")
	require.Nil(t, result2)
	assert.Equal(t, 2, requester.callCount(),
		"finding #5 regression: 'Allow once' must not suppress the prompt on a later network-flagged command")
}

// TestEnforceShellPermissionMode_AllowPersistsAcrossCalls is the positive
// control: "Allow" (recordGrant=true, permTestFixture's default) must keep
// working exactly as before — this is TestEnforceShellPermissionMode_
// ApprovalWidensAndSecondCommandReuses's own property, restated here next
// to its allow-once sibling for a direct side-by-side contrast.
func TestEnforceShellPermissionMode_AllowPersistsAcrossCalls(t *testing.T) {
	tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true) // recordGrant defaults to approve (true)

	outsideDir := t.TempDir()
	cmd := "echo hi > " + outsideDir + "/out.txt"

	_, result := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result)
	require.Equal(t, 1, requester.callCount())

	persisted := tool.approvalGrants.PathGrantsFor("session-1", "agent-1")
	require.Len(t, persisted, 1, "'Allow' must persist the PathGrant into the session store")

	_, result2 := tool.enforceShellPermissionMode(ctx, cmd)
	require.Nil(t, result2)
	assert.Equal(t, 1, requester.callCount(), "'Allow' must suppress the prompt on a later, identical command")
}
