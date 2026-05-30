// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for UnifiedStore.DeleteSession — Milestone 2.
//
// BDD scenarios:
//   Scenario: Delete existing session — verify directory removal
//   Scenario: Delete non-existent session — verify "not found" error
//   Scenario: Path traversal rejected — "../evil" returns validation error
//   Scenario: Empty session ID rejected — returns validation error
//   Scenario: ".." session ID rejected — returns validation error
//
// Traces to: pkg/session/unified.go — DeleteSession method (Milestone 2)

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestStore creates a UnifiedStore rooted at t.TempDir() and returns it.
// Callers are responsible for closing it if needed.
func newTestStore(t *testing.T) *UnifiedStore {
	t.Helper()
	store, err := NewUnifiedStore(t.TempDir())
	require.NoError(t, err, "NewUnifiedStore must succeed")
	return store
}

// TestDeleteSession_Success creates a session, verifies the directory exists,
// deletes it, and asserts the directory is gone.
//
// BDD: Given a session has been created,
// When DeleteSession is called with its ID,
// Then no error is returned AND the session directory no longer exists.
//
// Traces to: pkg/session/unified.go DeleteSession (Milestone 2)
func TestDeleteSession_Success(t *testing.T) {
	store := newTestStore(t)

	// Given — create a real session.
	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err, "NewSession must succeed")
	sessionID := meta.ID

	// Verify the directory was actually created (precondition for a meaningful test).
	sessionDir := filepath.Join(store.baseDir, sessionID)
	_, statErr := os.Stat(sessionDir)
	require.NoError(t, statErr, "session directory must exist before deletion")

	// When — delete the session.
	err = store.DeleteSession(sessionID)

	// Then — no error and directory is gone.
	require.NoError(t, err, "DeleteSession must succeed for an existing session")
	_, statErr = os.Stat(sessionDir)
	assert.True(t, os.IsNotExist(statErr),
		"session directory must be removed after DeleteSession; stat error: %v", statErr)
}

// TestDeleteSession_DifferentSessions verifies that deleting one session does not
// affect another — proving DeleteSession operates on the targeted directory only.
//
// This is the differentiation test: two sessions, delete one, verify the other survives.
//
// Traces to: pkg/session/unified.go DeleteSession (Milestone 2)
func TestDeleteSession_DifferentSessions(t *testing.T) {
	store := newTestStore(t)

	// Create two sessions.
	meta1, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	meta2, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	id1 := meta1.ID
	id2 := meta2.ID
	require.NotEqual(t, id1, id2, "two sessions must have distinct IDs")

	dir2 := filepath.Join(store.baseDir, id2)

	// Delete session 1.
	require.NoError(t, store.DeleteSession(id1), "DeleteSession(id1) must succeed")

	// Session 1's dir must be gone.
	_, statErr := os.Stat(filepath.Join(store.baseDir, id1))
	assert.True(t, os.IsNotExist(statErr), "deleted session directory must not exist")

	// Session 2's dir must still be present.
	_, statErr = os.Stat(dir2)
	assert.NoError(t, statErr, "non-deleted session directory must still exist")
}

// TestDeleteSession_NotFound verifies that deleting a non-existent session ID
// returns an error containing "not found".
//
// BDD: Given no session with ID "does-not-exist" exists,
// When DeleteSession("does-not-exist") is called,
// Then an error is returned containing "not found".
//
// Traces to: pkg/session/unified.go DeleteSession (Milestone 2)
func TestDeleteSession_NotFound(t *testing.T) {
	store := newTestStore(t)

	err := store.DeleteSession("does-not-exist-session-id")

	require.Error(t, err, "DeleteSession must return an error for a non-existent session")
	assert.True(t, strings.Contains(err.Error(), "not found"),
		"error must contain 'not found', got: %q", err.Error())
}

// TestDeleteSession_PathTraversal verifies that attempting to delete "../evil"
// is rejected with a validation error before any filesystem operation.
//
// BDD: Given a malicious session ID "../evil",
// When DeleteSession("../evil") is called,
// Then an error is returned about invalid session ID (validateSessionID rejects it).
//
// Traces to: pkg/session/unified.go validateSessionID (Milestone 2)
func TestDeleteSession_PathTraversal(t *testing.T) {
	store := newTestStore(t)

	err := store.DeleteSession("../evil")

	require.Error(t, err, "DeleteSession must reject path-traversal session IDs")
	// The error must come from validateSessionID, not from a filesystem operation
	// that traversed out of the base directory.
	assert.Contains(t, err.Error(), "invalid session ID",
		"error must mention 'invalid session ID', got: %q", err.Error())
}

// TestDeleteSession_EmptyID verifies that an empty string session ID is rejected.
//
// BDD: Given an empty session ID "",
// When DeleteSession("") is called,
// Then an error is returned about invalid session ID.
//
// Traces to: pkg/session/unified.go validateSessionID (Milestone 2)
func TestDeleteSession_EmptyID(t *testing.T) {
	store := newTestStore(t)

	err := store.DeleteSession("")

	require.Error(t, err, "DeleteSession must reject empty session ID")
	assert.Contains(t, err.Error(), "invalid session ID",
		"error must mention 'invalid session ID', got: %q", err.Error())
}

// TestDeleteSession_DoubleDot verifies that ".." as a session ID is rejected.
//
// BDD: Given session ID "..",
// When DeleteSession("..") is called,
// Then an error is returned about invalid session ID.
//
// Traces to: pkg/session/unified.go validateSessionID (Milestone 2)
func TestDeleteSession_DoubleDot(t *testing.T) {
	store := newTestStore(t)

	err := store.DeleteSession("..")

	require.Error(t, err, "DeleteSession must reject '..' as session ID")
	assert.Contains(t, err.Error(), "invalid session ID",
		"error must mention 'invalid session ID', got: %q", err.Error())
}

// --- SwitchAgent tests ---

// TestSwitchAgent_UpdatesActiveAgentID verifies that SwitchAgent updates the
// ActiveAgentID field and adds the new agent to AgentIDs.
//
// BDD: Given a session created with "agent-a",
// When SwitchAgent is called with "agent-b",
// Then ActiveAgentID == "agent-b" and AgentIDs contains "agent-b".
//
// Traces to: pkg/session/unified.go SwitchAgent
func TestSwitchAgent_UpdatesActiveAgentID(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	sessionID := meta.ID

	err = store.SwitchAgent(sessionID, "agent-b")
	require.NoError(t, err, "SwitchAgent must succeed")

	updated, err := store.GetMeta(sessionID)
	require.NoError(t, err)

	assert.Equal(t, "agent-b", updated.ActiveAgentID, "ActiveAgentID must be updated to agent-b")

	found := false
	for _, id := range updated.AgentIDs {
		if id == "agent-b" {
			found = true
			break
		}
	}
	assert.True(t, found, "AgentIDs must contain the new agent-b")
}

// TestSwitchAgent_SameAgent_ReturnsErrAlreadyActive verifies that switching to
// the already-active agent returns ErrAlreadyActive (idempotent guard).
//
// BDD: Given a session where ActiveAgentID == "agent-a",
// When SwitchAgent("agent-a") is called,
// Then ErrAlreadyActive is returned.
//
// Traces to: pkg/session/unified.go SwitchAgent — ErrAlreadyActive guard
func TestSwitchAgent_SameAgent_ReturnsErrAlreadyActive(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)

	err = store.SwitchAgent(meta.ID, "agent-a")

	assert.ErrorIs(t, err, ErrAlreadyActive,
		"switching to the already-active agent must return ErrAlreadyActive")
}

// TestSwitchAgent_NonExistentSession_ReturnsError verifies that SwitchAgent
// returns an error when the session does not exist.
//
// BDD: Given no session with ID "nonexistent-session",
// When SwitchAgent is called,
// Then an error is returned.
//
// Traces to: pkg/session/unified.go SwitchAgent — readMetaLocked error path
func TestSwitchAgent_NonExistentSession_ReturnsError(t *testing.T) {
	store := newTestStore(t)

	err := store.SwitchAgent("nonexistent-session-id-xyz", "agent-b")

	assert.Error(t, err, "SwitchAgent on a nonexistent session must return an error")
	// Must NOT be ErrAlreadyActive — this is a different error class.
	assert.NotErrorIs(t, err, ErrAlreadyActive,
		"error for nonexistent session must not be ErrAlreadyActive")
}

// TestSwitchAgent_AgentIDs_NoDuplicates verifies that switching back and forth
// between agents does not create duplicate entries in AgentIDs.
//
// BDD: Given a session with AgentIDs ["agent-a"],
// When SwitchAgent("agent-b") then SwitchAgent("agent-a") are called,
// Then AgentIDs contains exactly ["agent-a", "agent-b"] (no duplicates).
//
// Traces to: pkg/session/unified.go SwitchAgent — deduplication guard
func TestSwitchAgent_AgentIDs_NoDuplicates(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	sessionID := meta.ID

	// Switch a → b
	require.NoError(t, store.SwitchAgent(sessionID, "agent-b"))
	// Switch b → a (agent-a already in AgentIDs)
	require.NoError(t, store.SwitchAgent(sessionID, "agent-a"))

	updated, err := store.GetMeta(sessionID)
	require.NoError(t, err)

	// Count occurrences of each agent ID.
	counts := make(map[string]int)
	for _, id := range updated.AgentIDs {
		counts[id]++
	}
	if counts["agent-a"] != 1 {
		t.Errorf("agent-a appears %d times in AgentIDs, want exactly 1", counts["agent-a"])
	}
	if counts["agent-b"] != 1 {
		t.Errorf("agent-b appears %d times in AgentIDs, want exactly 1", counts["agent-b"])
	}
}

// TestDeleteSession_PersistenceCheck verifies the read-back contract:
// after deletion, GetMeta must fail.
//
// This is the persistence test: ensures deletion is durable and not superficial.
//
// Traces to: pkg/session/unified.go DeleteSession + GetMeta (Milestone 2)
func TestDeleteSession_PersistenceCheck(t *testing.T) {
	store := newTestStore(t)

	// Create, verify readable, delete, verify unreadable.
	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)

	// Read before deletion — must succeed.
	_, err = store.GetMeta(meta.ID)
	require.NoError(t, err, "GetMeta must succeed before deletion")

	// Delete.
	require.NoError(t, store.DeleteSession(meta.ID))

	// Read after deletion — must fail.
	_, err = store.GetMeta(meta.ID)
	assert.Error(t, err, "GetMeta must return error after session is deleted")
}

// --- MarkLastEntryTruncated tests ---

// TestMarkLastEntryTruncated_FlagsLastAssistantEntry verifies the core invariant of FR-14:
// after calling MarkLastEntryTruncated, the last assistant transcript entry has
// Truncated==true while all other fields are preserved unchanged.
//
// BDD: Given a session with one assistant transcript entry with a known turnID,
// When MarkLastEntryTruncated is called with that session's ID and turnID,
// Then ReadTranscript returns the entry with Truncated==true and all other fields intact.
//
// Traces to: pkg/session/unified.go MarkLastEntryTruncated (FR-14, H2)
func TestMarkLastEntryTruncated_FlagsLastAssistantEntry(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	// Append an assistant entry with a known turn ID.
	entry := TranscriptEntry{
		ID:      "entry-001",
		Type:    EntryTypeMessage,
		Role:    "assistant",
		Content: "Hello from the assistant",
		AgentID: "test-agent",
		TurnID:  "turn-001",
	}
	require.NoError(t, store.AppendTranscript(sessionID, entry))

	// Call MarkLastEntryTruncated with the turn ID.
	require.NoError(t, store.MarkLastEntryTruncated(sessionID, "turn-001"))

	// Read back and assert Truncated==true and other fields preserved.
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1, "must have exactly one entry")

	got := entries[0]
	assert.True(t, got.Truncated, "Truncated must be true after MarkLastEntryTruncated")
	assert.Equal(t, "entry-001", got.ID, "ID must be preserved")
	assert.Equal(t, EntryTypeMessage, got.Type, "Type must be preserved")
	assert.Equal(t, "assistant", got.Role, "Role must be preserved")
	assert.Equal(t, "Hello from the assistant", got.Content, "Content must be preserved")
	assert.Equal(t, "test-agent", got.AgentID, "AgentID must be preserved")
}

// TestMarkLastEntryTruncated_NoAssistantEntryIsNoOp verifies that calling
// MarkLastEntryTruncated on a session with no assistant entries (only user
// entries or an empty transcript) is a no-op — nil error, no file mutation.
//
// BDD: Given a session with only user transcript entries,
// When MarkLastEntryTruncated is called,
// Then nil is returned and entries are unchanged (Truncated remains false).
//
// Traces to: pkg/session/unified.go MarkLastEntryTruncated — no-assistant-entry path (FR-14)
func TestMarkLastEntryTruncated_NoAssistantEntryIsNoOp(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	// Append a user entry (no assistant entries).
	userEntry := TranscriptEntry{
		ID:      "user-001",
		Type:    EntryTypeMessage,
		Role:    "user",
		Content: "A user message",
		AgentID: "test-agent",
	}
	require.NoError(t, store.AppendTranscript(sessionID, userEntry))

	// MarkLastEntryTruncated must return nil.
	require.NoError(t, store.MarkLastEntryTruncated(sessionID, ""))

	// Entries must be unchanged.
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.False(t, entries[0].Truncated, "user entry must not have Truncated set")

	// Also verify the empty-transcript case (fresh session with no appended entries).
	metaEmpty, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	require.NoError(t, store.MarkLastEntryTruncated(metaEmpty.ID, ""),
		"MarkLastEntryTruncated on empty transcript must be a no-op")
}

// TestMarkLastEntryTruncated_DoesNotTouchContextStore verifies the FR-14a invariant:
// MarkLastEntryTruncated only mutates transcript.jsonl; context.jsonl is never
// touched. This is T9's key invariant from the cancel spec.
//
// BDD: Given a session whose context.jsonl contains an assistant message,
// When MarkLastEntryTruncated is called,
// Then transcript.jsonl's last assistant entry has Truncated==true,
// AND context.jsonl is byte-for-byte identical to before the call.
//
// Traces to: pkg/session/unified.go MarkLastEntryTruncated (FR-14a / T9)
func TestMarkLastEntryTruncated_DoesNotTouchContextStore(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	// Write an assistant entry to transcript.jsonl via AppendTranscript.
	transcriptEntry := TranscriptEntry{
		ID:      "transcript-001",
		Type:    EntryTypeMessage,
		Role:    "assistant",
		Content: "assistant partial content",
		AgentID: "test-agent",
	}
	require.NoError(t, store.AppendTranscript(sessionID, transcriptEntry))

	// Write a message to context.jsonl via the SessionStore interface.
	// AddMessage appends role/content to context.jsonl through the JSONL backend.
	store.AddMessage(sessionID, "assistant", "context store assistant content")

	// Snapshot context.jsonl before the call.
	contextPath := filepath.Join(store.BaseDir(), ".context", sessionID+".jsonl")
	contextBefore, readErr := os.ReadFile(contextPath)
	require.NoError(t, readErr, "context.jsonl must exist after AddMessage")

	// Call MarkLastEntryTruncated (empty turnID = backward-compat path).
	require.NoError(t, store.MarkLastEntryTruncated(sessionID, ""))

	// Assert transcript.jsonl has Truncated==true on the assistant entry.
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, entries[0].Truncated, "transcript.jsonl assistant entry must have Truncated==true")

	// Assert context.jsonl is byte-for-byte unchanged.
	contextAfter, readErr := os.ReadFile(contextPath)
	require.NoError(t, readErr, "context.jsonl must still be readable after MarkLastEntryTruncated")
	assert.Equal(t, string(contextBefore), string(contextAfter),
		"context.jsonl must not be mutated by MarkLastEntryTruncated (FR-14a / T9)")
}

// TestMarkLastEntryTruncated_DoesNotMutatePreviousTurnEntry verifies the H2 invariant:
// MarkLastEntryTruncated with a specific turnID must only flag entries belonging
// to that turn and must NOT touch assistant entries from other turns.
//
// BDD: Given a session with two assistant entries with different turnIDs (T1 and T2),
// When MarkLastEntryTruncated is called with turnID="T1",
// Then only the T1 entry has Truncated==true; the T2 entry is unchanged.
//
// Traces to: pkg/session/unified.go MarkLastEntryTruncated (H2 / turn-scoped truncation)
func TestMarkLastEntryTruncated_DoesNotMutatePreviousTurnEntry(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sid := meta.ID

	// Write assistant entry for turn T1.
	require.NoError(t, store.AppendTranscript(sid, TranscriptEntry{
		ID:      "asst-T1",
		Type:    EntryTypeMessage,
		Role:    "assistant",
		Content: "Response from turn T1",
		AgentID: "test-agent",
		TurnID:  "T1",
	}))
	// Write assistant entry for turn T2 (the "current" turn at cancel time).
	require.NoError(t, store.AppendTranscript(sid, TranscriptEntry{
		ID:      "asst-T2",
		Type:    EntryTypeMessage,
		Role:    "assistant",
		Content: "Partial response from turn T2",
		AgentID: "test-agent",
		TurnID:  "T2",
	}))

	// Cancel arrives for T1 only (e.g., a delayed cancel for a previous turn).
	require.NoError(t, store.MarkLastEntryTruncated(sid, "T1"))

	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	// Find by TurnID.
	var t1, t2 *TranscriptEntry
	for i := range entries {
		switch entries[i].TurnID {
		case "T1":
			t1 = &entries[i]
		case "T2":
			t2 = &entries[i]
		}
	}
	require.NotNil(t, t1, "T1 entry must exist")
	require.NotNil(t, t2, "T2 entry must exist")
	assert.True(t, t1.Truncated, "T1 entry must be marked truncated")
	assert.False(t, t2.Truncated, "T2 entry must NOT be marked truncated by a T1 cancel")
}
