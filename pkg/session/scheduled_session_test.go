// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the scheduled-session primitives added for issue #264 (W-2):
//   - SessionTypeScheduled + IsValidSessionType
//   - GetOrCreateScheduledSession (get-or-create by exact id)
//   - NewScheduledSession (fresh isolated scheduled session)
//
// Traces to: pkg/session/unified.go — FR-005, W-2.

package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionTypeScheduled_IsValid asserts the new type is accepted by the
// shared validator and that bogus types are rejected. Existing types must keep
// validating (regression: adding `scheduled` must not break grouping/listing).
func TestSessionTypeScheduled_IsValid(t *testing.T) {
	assert.True(t, IsValidSessionType(SessionTypeScheduled), "scheduled must be valid")
	assert.True(t, IsValidSessionType(SessionTypeChat))
	assert.True(t, IsValidSessionType(SessionTypeTask))
	assert.True(t, IsValidSessionType(SessionTypeChannel))
	assert.False(t, IsValidSessionType(UnifiedSessionType("bogus")))
	assert.Equal(t, "scheduled", string(SessionTypeScheduled))
}

// TestGetOrCreateScheduledSession_Creates verifies that the first call with a
// fresh id creates a session with the EXACT supplied id, Type=scheduled, and
// ActiveAgentID == AgentID == ownerAgentID.
func TestGetOrCreateScheduledSession_Creates(t *testing.T) {
	store := newTestStore(t)

	const id = "sched-main-mia"
	meta, err := store.GetOrCreateScheduledSession(id, "mia")
	require.NoError(t, err)
	require.NotNil(t, meta)

	assert.Equal(t, id, meta.ID, "must use the exact supplied id")
	assert.Equal(t, SessionTypeScheduled, meta.Type)
	assert.Equal(t, "mia", meta.AgentID)
	assert.Equal(t, "mia", meta.ActiveAgentID)
	assert.Equal(t, []string{"mia"}, meta.AgentIDs)

	// The meta is readable back through the public reader.
	got, err := store.GetMeta(id)
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
	assert.Equal(t, SessionTypeScheduled, got.Type)
}

// TestGetOrCreateScheduledSession_GetsExisting verifies the get path: a second
// call with the same id returns the existing session and does NOT clobber its
// owner (a human may have touched a `continue` session in the meantime).
func TestGetOrCreateScheduledSession_GetsExisting(t *testing.T) {
	store := newTestStore(t)

	const id = "sched-continue-1"
	first, err := store.GetOrCreateScheduledSession(id, "mia")
	require.NoError(t, err)

	// Simulate a human switching the active agent on this session.
	require.NoError(t, store.SwitchAgent(id, "max"))

	// Second get-or-create with a DIFFERENT requested owner must NOT recreate
	// or reset the session — it returns what is on disk.
	second, err := store.GetOrCreateScheduledSession(id, "mia")
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, "max", second.ActiveAgentID, "existing session must not be clobbered")
	assert.Equal(t, SessionTypeScheduled, second.Type)

	// Only one session directory should exist.
	metas, err := store.ListSessions()
	require.NoError(t, err)
	assert.Len(t, metas, 1)
}

// TestGetOrCreateScheduledSession_RejectsBadID asserts path-escape / empty ids
// are rejected (validateSessionID) so a malformed reserved id cannot escape the
// store root.
func TestGetOrCreateScheduledSession_RejectsBadID(t *testing.T) {
	store := newTestStore(t)

	for _, bad := range []string{"", "../evil", "a/b", ".."} {
		_, err := store.GetOrCreateScheduledSession(bad, "mia")
		assert.Error(t, err, "id %q must be rejected", bad)
	}
}

// TestNewScheduledSession_FreshIsolated verifies the isolated-mode primitive
// mints a fresh, distinct scheduled session on each call.
func TestNewScheduledSession_FreshIsolated(t *testing.T) {
	store := newTestStore(t)

	a, err := store.NewScheduledSession("mia")
	require.NoError(t, err)
	b, err := store.NewScheduledSession("mia")
	require.NoError(t, err)

	assert.NotEqual(t, a.ID, b.ID, "each isolated run gets a distinct session id")
	assert.Equal(t, SessionTypeScheduled, a.Type)
	assert.Equal(t, SessionTypeScheduled, b.Type)
	assert.Equal(t, "mia", a.ActiveAgentID)
	assert.Equal(t, "mia", b.ActiveAgentID)
}
