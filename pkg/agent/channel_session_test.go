// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the channel session routing feature in AgentLoop.
//
// BDD scenarios:
//
//	Scenario: processMessage creates a shared session for a non-webchat channel message
//	Scenario: rebuildChannelSessionIndex restores session IDs after restart
//	Scenario: resolveOrCreateChannelSession returns same ID for same channel/chatID (dedup)
//	Scenario: resolveOrCreateChannelSession returns different IDs for different peers
//	Scenario: resolveOrCreateChannelSession with empty chatID returns "" (no-op guard)
//
// Traces to: pkg/agent/loop.go — rebuildChannelSessionIndex, resolveOrCreateChannelSession,
//
//	processMessage channel-session block (channel session routing feature)

package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeLoopWithSharedStore creates an AgentLoop backed by a fresh temp directory
// and returns both the loop and its sharedSessionStore for direct inspection.
// The shared store is always non-nil when NewAgentLoop succeeds because NewAgentLoop
// initializes it from <parent of Workspace>/sessions.
func makeLoopWithSharedStore(t *testing.T) (*AgentLoop, *session.UnifiedStore) {
	t.Helper()

	// Create a workspace two levels deep so filepath.Dir gives a stable parent
	// that is still a temp dir: <tmp>/home/workspace → parent = <tmp>/home.
	tmpRoot := t.TempDir()
	workspace := filepath.Join(tmpRoot, "home", "workspace")
	require.NoError(t, os.MkdirAll(workspace, 0o700))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	require.NotNil(t, al.sharedSessionStore, "sharedSessionStore must be initialized by NewAgentLoop")
	return al, al.sharedSessionStore
}

// TestProcessMessage_ChannelSessionCreated verifies that a non-webchat channel message
// with an empty SessionID causes processMessage to create a shared channel session in
// the shared store.
//
// BDD: Given an AgentLoop with an initialized sharedSessionStore,
// When processMessage is called with Channel="discord", ChatID="chat-1", SessionID="",
// Then sharedSessionStore.ListSessions() returns exactly 1 session with
//   Channel=="discord", Type=="channel", PeerID=="chat-1".
//
// Traces to: pkg/agent/loop.go processMessage channel-session block (channel session routing feature)
func TestProcessMessage_ChannelSessionCreated(t *testing.T) {
	al, store := makeLoopWithSharedStore(t)

	// Precondition: store is empty.
	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, sessions, "precondition: shared store must be empty before processMessage")

	// When — send a non-webchat message with no SessionID.
	_, _, err = al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "discord",
		SenderID: "discord:user-7",
		Sender: bus.SenderInfo{
			DisplayName: "Alice",
		},
		ChatID:  "chat-1",
		Content: "hello",
	})
	require.NoError(t, err, "processMessage must succeed")

	// Then — shared store must contain exactly one session.
	sessions, err = store.ListSessions()
	require.NoError(t, err)
	require.Len(t, sessions, 1, "processMessage must create exactly 1 shared channel session")

	s := sessions[0]
	assert.Equal(t, "discord", s.Channel, "session Channel must be 'discord'")
	assert.Equal(t, string(session.SessionTypeChannel), string(s.Type),
		"session Type must be SessionTypeChannel")
	assert.Equal(t, "chat-1", s.PeerID, "session PeerID must match the ChatID from the message")
}

// TestProcessMessage_WebchatDoesNotCreateChannelSession verifies that webchat messages
// are excluded from the channel-session creation path.
//
// BDD: Given an AgentLoop with an initialized sharedSessionStore,
// When processMessage is called with Channel="webchat", SessionID="",
// Then sharedSessionStore.ListSessions() returns 0 sessions.
//
// Traces to: pkg/agent/loop.go processMessage channel-session guard (channel session routing feature)
func TestProcessMessage_WebchatDoesNotCreateChannelSession(t *testing.T) {
	al, store := makeLoopWithSharedStore(t)

	_, _, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel:  "webchat",
		SenderID: "user-1",
		ChatID:   "chat-web-1",
		Content:  "hello from webchat",
	})
	require.NoError(t, err, "processMessage for webchat must succeed")

	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, sessions, "webchat messages must NOT create a channel session in the shared store")
}

// TestRebuildChannelSessionIndex_RestoresSessionIDs verifies that after a restart,
// rebuildChannelSessionIndex re-populates channelSessionIdx from disk so that
// resolveOrCreateChannelSession returns the SAME session ID, not a new one.
//
// BDD: Given a shared session for (telegram, user-1) already exists on disk,
// When a new AgentLoop is created (simulating restart) and rebuildChannelSessionIndex is called,
// Then resolveOrCreateChannelSession("telegram", "user-1", ...) returns the existing session ID,
// AND ListSessions returns exactly 1 session (no duplicate created).
//
// Traces to: pkg/agent/loop.go rebuildChannelSessionIndex + resolveOrCreateChannelSession
//
//	(channel session routing feature)
func TestRebuildChannelSessionIndex_RestoresSessionIDs(t *testing.T) {
	al, store := makeLoopWithSharedStore(t)

	// Pre-create a channel session in the shared store (simulates a previous run).
	meta, err := store.NewChannelSession("telegram", "user-1", "agent-1", "Alice")
	require.NoError(t, err)
	sessionID1 := meta.ID

	// Simulate a restart: clear the in-memory index and rebuild from disk.
	al.channelSessionIdx = sync.Map{}
	al.rebuildChannelSessionIndex()

	// resolveOrCreateChannelSession must return the existing session ID (not create a new one).
	resolved := al.resolveOrCreateChannelSession("telegram", "user-1", "agent-1", "Alice")
	assert.Equal(t, sessionID1, resolved,
		"after rebuildChannelSessionIndex, resolveOrCreateChannelSession must return the existing session ID")

	// ListSessions must still return exactly 1 session (no duplicate).
	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1, "rebuildChannelSessionIndex must not create a duplicate session")
}

// TestResolveOrCreateChannelSession_DeduplicatesSamePeer verifies that two calls
// with the same channel/chatID return the same session ID (no duplicate sessions).
//
// BDD: Given an empty sharedSessionStore,
// When resolveOrCreateChannelSession("discord", "peer-9", ...) is called twice,
// Then both calls return the same session ID,
// AND ListSessions returns exactly 1 session.
//
// Traces to: pkg/agent/loop.go resolveOrCreateChannelSession (channel session routing feature)
func TestResolveOrCreateChannelSession_DeduplicatesSamePeer(t *testing.T) {
	al, store := makeLoopWithSharedStore(t)

	id1 := al.resolveOrCreateChannelSession("discord", "peer-9", "agent-1", "Bob")
	require.NotEmpty(t, id1, "first call must return a non-empty session ID")

	id2 := al.resolveOrCreateChannelSession("discord", "peer-9", "agent-1", "Bob")
	assert.Equal(t, id1, id2,
		"second call with same channel/chatID must return the same session ID (dedup)")

	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 1, "two calls for the same peer must result in exactly 1 session")
}

// TestResolveOrCreateChannelSession_DifferentPeersGetDifferentSessions verifies that
// different chatIDs on the same channel produce different session IDs.
//
// This is the differentiation test: different inputs → different outputs.
//
// BDD: Given an empty sharedSessionStore,
// When resolveOrCreateChannelSession is called with peer-A and then with peer-B,
// Then the two returned session IDs are different.
//
// Traces to: pkg/agent/loop.go resolveOrCreateChannelSession (channel session routing feature)
func TestResolveOrCreateChannelSession_DifferentPeersGetDifferentSessions(t *testing.T) {
	al, store := makeLoopWithSharedStore(t)

	idA := al.resolveOrCreateChannelSession("telegram", "peer-A", "agent-1", "Alice")
	idB := al.resolveOrCreateChannelSession("telegram", "peer-B", "agent-1", "Bob")

	require.NotEmpty(t, idA, "peer-A must get a session ID")
	require.NotEmpty(t, idB, "peer-B must get a session ID")
	assert.NotEqual(t, idA, idB, "different peers must get different session IDs")

	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Len(t, sessions, 2, "two different peers must produce exactly 2 sessions")
}

// TestResolveOrCreateChannelSession_EmptyChatIDReturnsEmpty verifies the guard:
// when chatID is empty, resolveOrCreateChannelSession returns "" and creates no session.
//
// BDD: Given an empty sharedSessionStore,
// When resolveOrCreateChannelSession("discord", "", ...) is called,
// Then "" is returned and no session is created.
//
// Traces to: pkg/agent/loop.go resolveOrCreateChannelSession early-return guard
//
//	(channel session routing feature)
func TestResolveOrCreateChannelSession_EmptyChatIDReturnsEmpty(t *testing.T) {
	al, store := makeLoopWithSharedStore(t)

	result := al.resolveOrCreateChannelSession("discord", "", "agent-1", "NoName")
	assert.Equal(t, "", result, "empty chatID must cause resolveOrCreateChannelSession to return ''")

	sessions, err := store.ListSessions()
	require.NoError(t, err)
	assert.Empty(t, sessions, "empty chatID must not create any session")
}

// TestResolveOrCreateChannelSession_TitleFallbackToChatID verifies that when
// displayName is empty, the session title falls back to chatID.
//
// BDD: Given displayName is "",
// When resolveOrCreateChannelSession("slack", "room-99", "agent-1", "") is called,
// Then the session title stored in the shared store equals "room-99".
//
// Traces to: pkg/agent/loop.go resolveOrCreateChannelSession title fallback
//
//	(channel session routing feature)
func TestResolveOrCreateChannelSession_TitleFallbackToChatID(t *testing.T) {
	al, store := makeLoopWithSharedStore(t)

	sessionID := al.resolveOrCreateChannelSession("slack", "room-99", "agent-1", "")
	require.NotEmpty(t, sessionID, "must return a session ID even when displayName is empty")

	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, "room-99", meta.Title,
		"when displayName is empty, title must fall back to chatID ('room-99')")
}
