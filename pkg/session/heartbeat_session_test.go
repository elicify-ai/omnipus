// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the heartbeat session primitives (FR-010, A1/F-02, A2/F-11):
//   - SessionTypeHeartbeat constant + IsValidSessionType
//   - NewHeartbeatSession: stamps type/workspace_id/agent + round-trips
//
// Traces to: pkg/session/unified.go — FR-010, FR-024, A2/F-11.

package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionTypeHeartbeat_IsValid asserts that the new heartbeat type passes
// IsValidSessionType and that its string value equals "heartbeat" (FR-024 —
// the generated zod enum must match this exact value).
func TestSessionTypeHeartbeat_IsValid(t *testing.T) {
	assert.True(t, IsValidSessionType(SessionTypeHeartbeat), "heartbeat must be valid")
	assert.Equal(t, "heartbeat", string(SessionTypeHeartbeat))
	// Regression: existing types must remain valid.
	assert.True(t, IsValidSessionType(SessionTypeChat))
	assert.True(t, IsValidSessionType(SessionTypeTask))
	assert.True(t, IsValidSessionType(SessionTypeChannel))
	assert.True(t, IsValidSessionType(SessionTypeScheduled))
	// Bogus type must still be rejected.
	assert.False(t, IsValidSessionType(UnifiedSessionType("bogus")))
}

// TestNewHeartbeatSession_StampsTypeWorkspaceAgent verifies that a freshly
// created heartbeat session is stamped with all three required fields:
//   - Type = "heartbeat"
//   - WorkspaceID = the supplied workspaceID
//   - AgentID / ActiveAgentID = the supplied agentID
//
// Traces to: FR-010, A1/F-02.
func TestNewHeartbeatSession_StampsTypeWorkspaceAgent(t *testing.T) {
	store := newTestStore(t)

	const wsID = "ws-alpha"
	const agentID = "mia"

	meta, err := store.NewHeartbeatSession(wsID, agentID)
	require.NoError(t, err)
	require.NotNil(t, meta)

	assert.Equal(t, SessionTypeHeartbeat, meta.Type, "type must be heartbeat")
	assert.Equal(t, wsID, meta.WorkspaceID, "WorkspaceID must be stamped")
	assert.Equal(t, agentID, meta.AgentID, "AgentID must be set")
	assert.Equal(t, agentID, meta.ActiveAgentID, "ActiveAgentID must be set")
	assert.Equal(t, []string{agentID}, meta.AgentIDs, "AgentIDs must be [agentID]")
	assert.NotEmpty(t, meta.ID, "session must have a non-empty ID")
}

// TestNewHeartbeatSession_RoundTrips verifies that the session created by
// NewHeartbeatSession survives a GetMeta round-trip with all fields intact.
//
// Traces to: FR-024, A2/F-11 — the heartbeat type must be persisted and
// readable without the SPA receiving an unknown type value.
func TestNewHeartbeatSession_RoundTrips(t *testing.T) {
	store := newTestStore(t)

	const wsID = "ws-beta"
	const agentID = "jim"

	created, err := store.NewHeartbeatSession(wsID, agentID)
	require.NoError(t, err)

	got, err := store.GetMeta(created.ID)
	require.NoError(t, err)

	assert.Equal(t, created.ID, got.ID, "ID must survive round-trip")
	assert.Equal(t, SessionTypeHeartbeat, got.Type, "type must be heartbeat after round-trip")
	assert.Equal(t, wsID, got.WorkspaceID, "WorkspaceID must survive round-trip")
	assert.Equal(t, agentID, got.AgentID, "AgentID must survive round-trip")
}

// TestNewHeartbeatSession_DistinctIDs verifies that two calls to
// NewHeartbeatSession produce two distinct sessions (isolated minting).
func TestNewHeartbeatSession_DistinctIDs(t *testing.T) {
	store := newTestStore(t)

	a, err := store.NewHeartbeatSession("ws1", "mia")
	require.NoError(t, err)

	b, err := store.NewHeartbeatSession("ws2", "mia")
	require.NoError(t, err)

	assert.NotEqual(t, a.ID, b.ID, "each heartbeat session gets a distinct id")
	assert.Equal(t, "ws1", a.WorkspaceID)
	assert.Equal(t, "ws2", b.WorkspaceID)
}

// TestNewHeartbeatSession_ListSessions verifies that a heartbeat session
// appears in ListSessions with its type intact.
func TestNewHeartbeatSession_ListSessions(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewHeartbeatSession("ws-gamma", "ray")
	require.NoError(t, err)

	all, err := store.ListSessions()
	require.NoError(t, err)
	require.Len(t, all, 1)

	assert.Equal(t, meta.ID, all[0].ID)
	assert.Equal(t, SessionTypeHeartbeat, all[0].Type)
	assert.Equal(t, "ws-gamma", all[0].WorkspaceID)
}
