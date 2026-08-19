// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for UnifiedStore.NewChannelSession — channel session routing feature.
//
// BDD scenarios:
//
//	Scenario: NewChannelSession stores all fields correctly
//	Scenario: NewChannelSession fields survive a round-trip to disk
//	Scenario: NewChannelSession with empty title stores empty title (no fallback at this layer)
//	Scenario: NewChannelSession called twice creates two independent sessions (no dedup at store layer)
//	Scenario: NewChannelSession with different channels produces different Channel fields
//
// Traces to: pkg/session/unified.go — NewChannelSession (channel session routing feature)

package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewChannelSession_StoresAllFields verifies that NewChannelSession creates a
// session with all provided fields correctly set: Channel, PeerID, Title, Type, AgentID.
//
// BDD: Given a UnifiedStore backed by a temp directory,
// When NewChannelSession("telegram", "telegram", "user-42", "agent-1", "Alice") is called,
// Then no error is returned, meta.Channel == "telegram", meta.PeerID == "user-42",
// meta.Title == "Alice", meta.Type == SessionTypeChannel, meta.AgentID == "agent-1".
//
// Traces to: pkg/session/unified.go NewChannelSession (channel session routing feature)
func TestNewChannelSession_StoresAllFields(t *testing.T) {
	store := newTestStore(t)

	// When — create the channel session.
	meta, err := store.NewChannelSession("telegram", "telegram", "user-42", "agent-1", "Alice")

	// Then — all fields must be set correctly.
	require.NoError(t, err, "NewChannelSession must succeed")
	require.NotNil(t, meta, "NewChannelSession must return a non-nil meta")

	assert.Equal(t, "telegram", meta.Channel, "Channel must be set to 'telegram'")
	assert.Equal(t, "user-42", meta.PeerID, "PeerID must be set to 'user-42'")
	assert.Equal(t, "Alice", meta.Title, "Title must be set to 'Alice'")
	assert.Equal(t, SessionTypeChannel, meta.Type, "Type must be SessionTypeChannel")
	assert.Equal(t, "agent-1", meta.AgentID, "AgentID must be set to 'agent-1'")
	assert.NotEmpty(t, meta.ID, "ID must be a non-empty session ID")
}

// TestNewChannelSession_RoundTrip verifies that the fields written by NewChannelSession
// survive a read-back via GetMeta — proving the data is durably stored on disk, not
// just held in memory.
//
// BDD: Given a session created with NewChannelSession("telegram", "telegram", "user-42", "agent-1", "Alice"),
// When GetMeta(meta.ID) is called on the same store,
// Then the returned meta has identical Channel, PeerID, Title, Type, and AgentID.
//
// Traces to: pkg/session/unified.go NewChannelSession + GetMeta (channel session routing feature)
func TestNewChannelSession_RoundTrip(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewChannelSession("telegram", "telegram", "user-42", "agent-1", "Alice")
	require.NoError(t, err)

	// Round-trip: read back from disk.
	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err, "GetMeta must succeed after NewChannelSession")

	assert.Equal(t, "telegram", got.Channel, "round-trip: Channel must match")
	assert.Equal(t, "user-42", got.PeerID, "round-trip: PeerID must match")
	assert.Equal(t, "Alice", got.Title, "round-trip: Title must match")
	assert.Equal(t, SessionTypeChannel, got.Type, "round-trip: Type must be SessionTypeChannel")
	assert.Equal(t, "agent-1", got.AgentID, "round-trip: AgentID must match")
}

// TestNewChannelSession_EmptyTitle verifies that NewChannelSession with an empty title
// stores an empty title (no fallback at this layer — title fallback lives in
// resolveOrCreateChannelSession in the agent loop).
//
// BDD: Given a call to NewChannelSession with title == "",
// When GetMeta returns the stored meta,
// Then meta.Title == "" (not the chatID or any other fallback).
//
// Traces to: pkg/session/unified.go NewChannelSession (channel session routing feature)
func TestNewChannelSession_EmptyTitle(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewChannelSession("telegram", "telegram", "user-42", "agent-1", "")
	require.NoError(t, err)

	// Title must be empty — no fallback at the store layer.
	assert.Equal(t, "", meta.Title, "empty title must be stored as empty (no fallback in store layer)")

	// Round-trip verification.
	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, "", got.Title, "round-trip: empty title must survive disk write")
}

// TestNewChannelSession_TwoCallsCreateTwoSessions verifies that calling
// NewChannelSession twice with the same inputs creates TWO distinct sessions —
// there is no de-duplication at the store layer; that is the agent loop's job.
//
// This is the differentiation test: two calls → two different session IDs.
//
// BDD: Given two calls to NewChannelSession with identical channel/peerID/agentID/title,
// When both complete without error,
// Then they produce two different session IDs.
//
// Traces to: pkg/session/unified.go NewChannelSession (channel session routing feature)
func TestNewChannelSession_TwoCallsCreateTwoSessions(t *testing.T) {
	store := newTestStore(t)

	meta1, err := store.NewChannelSession("telegram", "telegram", "user-42", "agent-1", "Alice")
	require.NoError(t, err)

	meta2, err := store.NewChannelSession("telegram", "telegram", "user-42", "agent-1", "Alice")
	require.NoError(t, err)

	// Two calls must produce two independent sessions (no store-level dedup).
	assert.NotEqual(t, meta1.ID, meta2.ID,
		"two NewChannelSession calls must produce different session IDs (dedup is the loop's job)")

	// Both sessions must be readable from disk.
	_, err = store.GetMeta(meta1.ID)
	require.NoError(t, err, "first session must be retrievable after second creation")
	_, err = store.GetMeta(meta2.ID)
	require.NoError(t, err, "second session must be retrievable")
}

// TestNewChannelSession_DifferentChannelsProduceDifferentMeta verifies that two sessions
// created with different channel values store distinct Channel fields.
//
// This is the differentiation test: same peerID/agentID, different channels → different Channel.
//
// BDD: Given calls to NewChannelSession("telegram", "telegram", ...) and NewChannelSession("discord", "discord", ...),
// When both succeed,
// Then the first meta.Channel == "telegram" and the second meta.Channel == "discord".
//
// Traces to: pkg/session/unified.go NewChannelSession (channel session routing feature)
func TestNewChannelSession_DifferentChannelsProduceDifferentMeta(t *testing.T) {
	store := newTestStore(t)

	meta1, err := store.NewChannelSession("telegram", "telegram", "user-99", "agent-1", "Bob")
	require.NoError(t, err)

	meta2, err := store.NewChannelSession("discord", "discord", "user-99", "agent-1", "Bob")
	require.NoError(t, err)

	// IDs must be different.
	assert.NotEqual(t, meta1.ID, meta2.ID, "sessions on different channels must have distinct IDs")

	// Channel fields must differ.
	assert.Equal(t, "telegram", meta1.Channel, "first session must have channel 'telegram'")
	assert.Equal(t, "discord", meta2.Channel, "second session must have channel 'discord'")

	// ListSessions must show both.
	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 2, "ListSessions must return exactly 2 sessions")
}
