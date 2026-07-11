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
	"fmt"
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

// --- UpdateToolCallStatus tests (Wave 3 fix 5b) ---

// TestUpdateToolCallStatus_RewritesMatchingToolCall verifies the core invariant
// of fix 5b: after calling UpdateToolCallStatus with a ToolCall.ID that exists in
// the transcript, ReadTranscript returns that ToolCall with the new Status and
// DurationMS while every other field (Tool, Parameters, Result, ...) is preserved.
//
// This simulates the ASYNC delegation scenario: the spawning "delegate" tool call
// is first persisted with a placeholder ack (Status="success", DurationMS=0, per
// tools.AsyncResult), then corrected once the real sub-turn finishes.
//
// BDD: Given a transcript entry carrying a ToolCall with ID "c1" and a placeholder
//
//	Status/DurationMS,
//	When UpdateToolCallStatus is called for "c1" with the real status/duration,
//	Then ReadTranscript returns "c1" with the updated Status/DurationMS and all
//	other fields unchanged.
//
// Traces to: pkg/session/unified.go UpdateToolCallStatus (Wave 3 fix 5b)
func TestUpdateToolCallStatus_RewritesMatchingToolCall(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	// Simulate the async delegation "ack" record loop.go's standard tool-completion
	// path writes immediately after DelegateTool.executeAsync returns AsyncResult.
	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{
		ID:      "c1",
		Type:    EntryTypeToolCall,
		AgentID: "jim",
		ToolCalls: []ToolCall{
			{
				ID:         "c1",
				Tool:       "delegate",
				Status:     "success",
				DurationMS: 0,
				Parameters: map[string]any{"task": "audit go files"},
			},
		},
	}))

	// The sub-turn actually finishes later with a real status/duration.
	require.NoError(t, store.UpdateToolCallStatus(sessionID, "c1", "success", 4210))

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1, "must have exactly one entry")

	require.Len(t, entries[0].ToolCalls, 1)
	got := entries[0].ToolCalls[0]
	assert.Equal(t, ToolCallID("c1"), got.ID, "ID must be preserved")
	assert.Equal(t, "delegate", got.Tool, "Tool must be preserved")
	assert.Equal(t, "success", got.Status, "Status must be updated to the real terminal status")
	assert.EqualValues(t, 4210, got.DurationMS, "DurationMS must be updated to the real wall-clock duration")
	assert.Equal(t, "audit go files", got.Parameters["task"], "Parameters must be preserved")
}

// TestUpdateToolCallStatus_FlipsToError verifies UpdateToolCallStatus can move a
// ToolCall from a placeholder "success" to a real "error" status — the mirror
// case of the success-preserved test, proving the function does not hardcode a
// bias toward either terminal value.
func TestUpdateToolCallStatus_FlipsToError(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{
		ID:      "c2",
		Type:    EntryTypeToolCall,
		AgentID: "jim",
		ToolCalls: []ToolCall{
			{ID: "c2", Tool: "delegate", Status: "success", DurationMS: 0},
		},
	}))

	require.NoError(t, store.UpdateToolCallStatus(sessionID, "c2", "error", 987))

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Len(t, entries[0].ToolCalls, 1)
	got := entries[0].ToolCalls[0]
	assert.Equal(t, "error", got.Status)
	assert.EqualValues(t, 987, got.DurationMS)
}

// TestUpdateToolCallStatus_NoMatchIsNoOp verifies that calling UpdateToolCallStatus
// with a ToolCall.ID that does not exist in the transcript is a no-op: nil error,
// and every existing entry is byte-for-byte unchanged.
//
// This is the expected outcome for SYNCHRONOUS delegation (DelegateTool.
// executeSync): spawnSubTurn blocks until the child finishes, so at the moment
// EventKindSubTurnEnd fires the spawning tool call's own record has not been
// appended to the transcript yet.
//
// Traces to: pkg/session/unified.go UpdateToolCallStatus doc comment (Wave 3 fix 5b)
func TestUpdateToolCallStatus_NoMatchIsNoOp(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	require.NoError(t, store.AppendTranscript(sessionID, TranscriptEntry{
		ID:      "c3",
		Type:    EntryTypeToolCall,
		AgentID: "jim",
		ToolCalls: []ToolCall{
			{ID: "c3", Tool: "read_file", Status: "success", DurationMS: 12},
		},
	}))

	err = store.UpdateToolCallStatus(sessionID, "does-not-exist", "success", 999)
	require.NoError(t, err, "no matching ToolCall.ID must be a no-op, not an error")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Len(t, entries[0].ToolCalls, 1)
	got := entries[0].ToolCalls[0]
	assert.Equal(t, "success", got.Status, "unrelated entry must be unchanged")
	assert.EqualValues(t, 12, got.DurationMS, "unrelated entry must be unchanged")
}

// TestUpdateToolCallStatus_EmptyTranscriptIsNoOp verifies that calling
// UpdateToolCallStatus on a session with no transcript file yet is a no-op
// (nil error), mirroring MarkLastEntryTruncated's os.IsNotExist handling.
func TestUpdateToolCallStatus_EmptyTranscriptIsNoOp(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	err = store.UpdateToolCallStatus(sessionID, "c1", "error", 100)
	assert.NoError(t, err, "no transcript file yet must be a no-op, not an error")
}

// --- Cascade-delete uploads tests (N-B fix) ---

// TestDeleteSession_CascadeDeletesUploads_SharedStore verifies that DeleteSession
// on the shared store (baseDir = <home>/sessions) removes the session's uploads
// directory at <home>/uploads/<sessionID>.
//
// BDD: Given a shared store at <home>/sessions with a session whose uploads exist
//
//	at <home>/uploads/<sessionID>/,
//	When DeleteSession is called,
//	Then the uploads directory is removed.
//
// Traces to: ADR-017 D5, N-B fix — uploads are home-rooted.
func TestDeleteSession_CascadeDeletesUploads_SharedStore(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")

	store, err := NewUnifiedStoreWithHome(sessionsDir, home)
	require.NoError(t, err)

	meta, err := store.NewSession(SessionTypeChat, "", "agent-test")
	require.NoError(t, err)
	sessionID := meta.ID

	// Simulate an upload at <home>/uploads/<sessionID>/file.txt.
	uploadsDir := filepath.Join(home, "uploads", sessionID)
	require.NoError(t, os.MkdirAll(uploadsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(uploadsDir, "file.txt"), []byte("data"), 0o600))

	// Delete the session.
	require.NoError(t, store.DeleteSession(sessionID))

	// The session directory must be gone.
	_, statErr := os.Stat(filepath.Join(sessionsDir, sessionID))
	assert.True(t, os.IsNotExist(statErr), "session dir must be removed after DeleteSession")

	// The uploads directory must also be gone (cascade-delete, N-B fix).
	_, uploadsStatErr := os.Stat(uploadsDir)
	assert.True(t, os.IsNotExist(uploadsStatErr),
		"uploads dir at <home>/uploads/<sessionID> must be removed by cascade-delete (N-B fix)")
}

// TestDeleteSession_CascadeDeletesUploads_PerAgentStore verifies that DeleteSession
// on a per-agent store (baseDir = <home>/agents/<id>/sessions) still correctly
// removes uploads at <home>/uploads/<sessionID> — not at the wrong
// <home>/agents/<id>/uploads/<sessionID> path that the old filepath.Dir logic
// would have computed.
//
// BDD: Given a per-agent store at <home>/agents/my-agent/sessions
//
//	with uploads at <home>/uploads/<sessionID>/,
//	When DeleteSession is called,
//	Then the uploads dir at <home>/uploads/<sessionID> is removed,
//	And NO directory is created or removed under <home>/agents/my-agent/uploads/.
//
// Traces to: ADR-017 D5, N-B fix — per-agent stores must use home-rooted uploads path.
func TestDeleteSession_CascadeDeletesUploads_PerAgentStore(t *testing.T) {
	home := t.TempDir()
	agentSessionsDir := filepath.Join(home, "agents", "my-agent", "sessions")

	store, err := NewUnifiedStoreWithHome(agentSessionsDir, home)
	require.NoError(t, err)

	meta, err := store.NewSession(SessionTypeChat, "", "my-agent")
	require.NoError(t, err)
	sessionID := meta.ID

	// Uploads at the correct home-rooted path.
	correctUploadsDir := filepath.Join(home, "uploads", sessionID)
	require.NoError(t, os.MkdirAll(correctUploadsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(correctUploadsDir, "upload.txt"), []byte("data"), 0o600))

	// The WRONG path (what the old code would have used).
	wrongUploadsDir := filepath.Join(home, "agents", "my-agent", "uploads", sessionID)

	require.NoError(t, store.DeleteSession(sessionID))

	// The correct uploads directory must be removed (N-B fix).
	_, correctStatErr := os.Stat(correctUploadsDir)
	assert.True(t, os.IsNotExist(correctStatErr),
		"uploads at <home>/uploads/<sessionID> must be removed by cascade-delete")

	// The wrong path must never have been created or touched.
	_, wrongStatErr := os.Stat(wrongUploadsDir)
	assert.True(t, os.IsNotExist(wrongStatErr),
		"<home>/agents/<id>/uploads/<sessionID> must not be touched — wrong path for uploads")
}

// --- ClearAll tests ---
//
// ClearAll is a destructive, irreversible, bulk operation (DELETE
// /api/v1/sessions/all → pkg/gateway/rest_settings.go:567 HandleClearSessions,
// which calls it once per registered agent's store). Before this file it had
// zero test coverage anywhere in the repo (repo-wide grep for "ClearAll" in
// *_test.go returned no hits).
//
// TODO: BDD scenario missing in spec — no wave-spec/BRD section documents
// ClearAll's Given/When/Then explicitly; scenarios below are inferred from
// the ClearAll doc comment and code at pkg/session/unified.go:833-869, plus
// the DeleteSession cascade-delete precedent it mirrors. [INFERRED]

// newTestStoreWithHome creates a UnifiedStore rooted at <home>/sessions with
// an explicit home directory, so cascade-deleted uploads (ClearAll, like
// DeleteSession) land at a path the test controls: <home>/uploads/<sessionID>.
func newTestStoreWithHome(t *testing.T) (store *UnifiedStore, home string) {
	t.Helper()
	home = t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	store, err := NewUnifiedStoreWithHome(sessionsDir, home)
	require.NoError(t, err, "NewUnifiedStoreWithHome must succeed")
	return store, home
}

// TestClearAll_RemovesSessionsContextAndUploads verifies the core destructive
// contract: every top-level session directory (plus its fake message file),
// its matching .context/<id>.jsonl file, and its matching uploads/<id>/
// directory are all removed, and the returned count equals the number of
// session directories removed.
//
// BDD: Given 3 session directories — one with a fake message file plus a
// matching .context/<id>.jsonl entry, another with a matching uploads/<id>/
// directory — When ClearAll is called, Then all 3 session directories, the
// matching context file, and the matching uploads directory are removed, and
// ClearAll returns (3, nil).
//
// Traces to: pkg/session/unified.go ClearAll (lines 835-869)
func TestClearAll_RemovesSessionsContextAndUploads(t *testing.T) {
	store, home := newTestStoreWithHome(t)

	meta1, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	meta2, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	meta3, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	allMetas := []*UnifiedMeta{meta1, meta2, meta3}

	// Fake message files inside each session directory (beyond meta.json).
	for _, m := range allMetas {
		dir := filepath.Join(store.BaseDir(), m.ID)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "transcript.jsonl"),
			[]byte(`{"role":"user","content":"hi"}`+"\n"), 0o600))
	}

	// meta1 gets a matching .context/<id>.jsonl file via the SessionStore
	// interface (AddMessage writes through to the JSONL backend).
	store.AddMessage(meta1.ID, "user", "hello from context store")
	contextFile := filepath.Join(store.BaseDir(), ".context", meta1.ID+".jsonl")
	_, statErr := os.Stat(contextFile)
	require.NoError(t, statErr, "precondition: context file for meta1 must exist")

	// meta2 gets a matching uploads/<id>/ directory.
	uploadsDir := filepath.Join(home, "uploads", meta2.ID)
	require.NoError(t, os.MkdirAll(uploadsDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(uploadsDir, "photo.png"), []byte("fake-bytes"), 0o600))

	// Preconditions: all 3 session dirs exist.
	for _, m := range allMetas {
		_, precondStatErr := os.Stat(filepath.Join(store.BaseDir(), m.ID))
		require.NoError(t, precondStatErr, "precondition: session dir for %s must exist", m.ID)
	}

	// When
	removed, err := store.ClearAll()

	// Then
	require.NoError(t, err, "ClearAll must not return an error in the happy path")
	assert.Equal(t, 3, removed, "ClearAll must report exactly 3 removed sessions")

	for _, m := range allMetas {
		_, statErr = os.Stat(filepath.Join(store.BaseDir(), m.ID))
		assert.True(t, os.IsNotExist(statErr), "session dir for %s must be removed", m.ID)
	}

	_, statErr = os.Stat(contextFile)
	assert.True(t, os.IsNotExist(statErr), "meta1's .context/<id>.jsonl must be removed")

	_, statErr = os.Stat(uploadsDir)
	assert.True(t, os.IsNotExist(statErr), "meta2's uploads/<id>/ dir must be removed")
}

// TestClearAll_PreservesContextDirItself verifies the exact skip condition in
// ClearAll's loop (`!entry.IsDir() || entry.Name() == ".context"`): the
// .context directory ITSELF must survive ClearAll even though every session's
// individual .context/<id>.jsonl file underneath it is removed, and any
// unrelated file left inside .context (not matching a removed session ID)
// must be left untouched.
//
// BDD: Given a store whose .context directory contains both a file matching
// a live session ID and an unrelated stray file, When ClearAll is called,
// Then the .context directory still exists, the matching file is gone, and
// the stray unrelated file is untouched.
//
// Traces to: pkg/session/unified.go ClearAll — `entry.Name() == ".context"`
// skip condition (line ~850)
func TestClearAll_PreservesContextDirItself(t *testing.T) {
	store, _ := newTestStoreWithHome(t)

	meta1, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)

	store.AddMessage(meta1.ID, "user", "hi")
	contextDir := filepath.Join(store.BaseDir(), ".context")

	// An unrelated stray file inside .context that does not match any
	// session ID being removed.
	strayFile := filepath.Join(contextDir, "unrelated-leftover.jsonl")
	require.NoError(t, os.WriteFile(strayFile, []byte(`{"stray":"data"}`+"\n"), 0o600))

	// Precondition: .context exists as a directory.
	info, statErr := os.Stat(contextDir)
	require.NoError(t, statErr, "precondition: .context dir must exist")
	require.True(t, info.IsDir(), "precondition: .context must be a directory")

	removed, err := store.ClearAll()
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	// .context directory itself must still exist.
	info, statErr = os.Stat(contextDir)
	require.NoError(t, statErr, ".context directory must survive ClearAll")
	assert.True(t, info.IsDir(), ".context must still be a directory after ClearAll")

	// The matching context file for the removed session must be gone.
	matchingFile := filepath.Join(contextDir, meta1.ID+".jsonl")
	_, statErr = os.Stat(matchingFile)
	assert.True(t, os.IsNotExist(statErr), "matching context file for removed session must be gone")

	// The unrelated stray file must be untouched.
	strayData, readErr := os.ReadFile(strayFile)
	require.NoError(t, readErr, "unrelated stray file inside .context must not be removed")
	assert.Equal(t, `{"stray":"data"}`+"\n", string(strayData),
		"unrelated stray file content must be untouched by ClearAll")
}

// TestClearAll_EmptyOrAlreadyClearedStore_NoOp verifies ClearAll is a safe
// no-op — returning (0, nil), never an error — both on a brand-new store that
// never had any sessions, and on a store that has already been cleared once.
//
// BDD: Given a store with no session directories (fresh, or already cleared),
// When ClearAll is called, Then it returns (0, nil).
//
// Traces to: pkg/session/unified.go ClearAll (lines 835-869)
func TestClearAll_EmptyOrAlreadyClearedStore_NoOp(t *testing.T) {
	t.Run("fresh store with no sessions", func(t *testing.T) {
		store, _ := newTestStoreWithHome(t)

		removed, err := store.ClearAll()
		require.NoError(t, err, "ClearAll on an empty store must not error")
		assert.Equal(t, 0, removed, "ClearAll on an empty store must report 0 removed")
	})

	t.Run("already-cleared store returns zero on second call", func(t *testing.T) {
		store, _ := newTestStoreWithHome(t)

		_, err := store.NewSession(SessionTypeChat, "", "agent-a")
		require.NoError(t, err)
		_, err = store.NewSession(SessionTypeChat, "", "agent-a")
		require.NoError(t, err)

		firstRemoved, err := store.ClearAll()
		require.NoError(t, err)
		require.Equal(t, 2, firstRemoved, "precondition: first ClearAll must remove the 2 seeded sessions")

		secondRemoved, err := store.ClearAll()
		require.NoError(t, err, "calling ClearAll again on an already-cleared store must not error")
		assert.Equal(t, 0, secondRemoved, "calling ClearAll again must report 0 removed, not re-count or fail")
	})
}

// TestClearAll_BaseDirMissing_ReturnsZeroNoError exercises the
// os.IsNotExist(err) branch: if the store's base directory has been removed
// out from under it (e.g., manual cleanup, or a prior ClearAll-adjacent
// operation), ClearAll must treat "nothing to clear" as success, not an error.
//
// BDD: Given the store's base directory does not exist on disk,
// When ClearAll is called,
// Then it returns (0, nil) rather than propagating the ReadDir error.
//
// Traces to: pkg/session/unified.go ClearAll — `os.IsNotExist(err)` branch
// (lines ~840-843)
func TestClearAll_BaseDirMissing_ReturnsZeroNoError(t *testing.T) {
	store, _ := newTestStoreWithHome(t)

	require.NoError(t, os.RemoveAll(store.BaseDir()), "test setup: remove the base dir entirely")

	removed, err := store.ClearAll()
	require.NoError(t, err, "ClearAll must not error when the base directory does not exist")
	assert.Equal(t, 0, removed)
}

// TestClearAll_ContinuesPastRemovalFailure documents ClearAll's behavior when
// one session directory's os.RemoveAll fails: per the code in
// pkg/session/unified.go's ClearAll, the error is logged via slog.Warn and the
// loop `continue`s to the next entry — it does NOT abort the whole operation,
// and the failed entry is NOT counted in the returned removed total. The
// per-entry failure IS surfaced to the caller: ClearAll aggregates every
// per-entry error (via errors.Join) and returns it alongside the partial
// removed count, so a "clear all sessions" request cannot silently
// under-deliver on this privacy-sensitive, destructive action.
//
// The real removal failure is forced by chmod'ing one session directory to
// 0500 (r-x, no write) so the OS refuses to unlink meta.json/transcript.jsonl
// inside it — a genuine os.RemoveAll error, not a simulated one.
//
// BDD: Given 3 session directories where one cannot be removed due to
// filesystem permissions, When ClearAll is called, Then the two removable
// directories are removed and counted, the unremovable directory is left
// fully intact, and ClearAll returns a non-nil aggregate error describing the
// failure alongside the correct partial count.
//
// Traces to: pkg/session/unified.go ClearAll, the
// `if err := os.RemoveAll(dir); err != nil { slog.Warn(...); continue }` path
// and its final `return removed, errors.Join(errs...)`.
func TestClearAll_ContinuesPastRemovalFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip(
			"test forces a real os.RemoveAll failure via directory permissions (chmod 0500) — root bypasses Unix permission checks (CAP_DAC_OVERRIDE), so this doesn't reproduce a failure when the test process runs as root (e.g. inside a CI container)",
		)
	}

	store, _ := newTestStoreWithHome(t)

	metaBad, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	metaGood1, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	metaGood2, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)

	badDir := filepath.Join(store.BaseDir(), metaBad.ID)

	// Remove write permission on the bad session's directory so the OS refuses
	// to unlink its contents (meta.json etc.) — a real os.RemoveAll failure.
	require.NoError(t, os.Chmod(badDir, 0o500))
	// Restore permissions before t.TempDir()'s own cleanup runs (t.Cleanup
	// callbacks run LIFO; this is registered after newTestStoreWithHome's
	// internal t.TempDir(), so it runs first).
	t.Cleanup(func() { _ = os.Chmod(badDir, 0o700) })

	removed, err := store.ClearAll()

	// Current behavior: the per-entry RemoveAll failure is logged via
	// slog.Warn AND surfaced as a non-nil aggregate error from ClearAll.
	require.Error(t, err,
		"ClearAll must surface a non-nil aggregate error when at least one session dir failed to remove")
	assert.Contains(t, err.Error(), metaBad.ID,
		"the aggregate error should mention the session whose removal failed")
	assert.Equal(t, 2, removed,
		"only the 2 removable session dirs must be counted; the dir whose RemoveAll failed must not be")

	// The bad directory must still exist, fully intact (RemoveAll could not
	// remove any of its children, so nothing partial happened to it either).
	_, statErr := os.Stat(badDir)
	assert.NoError(t, statErr, "the session dir whose RemoveAll failed must still exist on disk")
	_, statErr = os.Stat(filepath.Join(badDir, "meta.json"))
	assert.NoError(t, statErr, "meta.json inside the unremovable dir must still be present")

	// The two good directories must be gone.
	for _, m := range []*UnifiedMeta{metaGood1, metaGood2} {
		_, statErr := os.Stat(filepath.Join(store.BaseDir(), m.ID))
		assert.True(t, os.IsNotExist(statErr), "removable session dir for %s must be gone", m.ID)
	}
}

// TestClearAll_ContinuesPastRemovalFailure_InjectedError asserts the exact
// same aggregate-error behavior as TestClearAll_ContinuesPastRemovalFailure
// above, but via the removeAllFn package-level test seam instead of chmod'ing
// a directory to 0500. The chmod-based test is skipped when the test process
// runs as root (CAP_DAC_OVERRIDE bypasses the permission check) — which is
// exactly how CI runs it — so that test provides zero coverage of this
// behavior in CI. This test forces a deterministic os.RemoveAll-shaped error
// for one specific session directory regardless of privilege level, so it
// runs identically under root or non-root and actually exercises the
// aggregation path in CI.
//
// BDD: Given 3 session directories where removeAllFn is stubbed to fail for
// exactly one of them, When ClearAll is called, Then the two unaffected
// directories are removed and counted, and ClearAll returns a non-nil
// aggregate error mentioning the failed session's ID alongside the correct
// partial count.
//
// Traces to: pkg/session/unified.go ClearAll's
// `if err := removeAllFn(dir); err != nil { slog.Warn(...); continue }` path
// and its final `return removed, errors.Join(errs...)`.
func TestClearAll_ContinuesPastRemovalFailure_InjectedError(t *testing.T) {
	store, _ := newTestStoreWithHome(t)

	metaBad, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	metaGood1, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	metaGood2, err := store.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)

	badDir := filepath.Join(store.BaseDir(), metaBad.ID)
	injectedErr := fmt.Errorf("injected removeAllFn failure for %s", metaBad.ID)

	// Override the package-level seam for the duration of this test, save/
	// restore via t.Cleanup so other tests are unaffected.
	origRemoveAllFn := removeAllFn
	t.Cleanup(func() { removeAllFn = origRemoveAllFn })
	removeAllFn = func(path string) error {
		if path == badDir {
			return injectedErr
		}
		return origRemoveAllFn(path)
	}

	removed, err := store.ClearAll()

	require.Error(t, err,
		"ClearAll must surface a non-nil aggregate error when removeAllFn failed for one session dir")
	assert.Contains(t, err.Error(), metaBad.ID,
		"the aggregate error should mention the session whose removal failed")
	assert.Contains(t, err.Error(), injectedErr.Error(),
		"the aggregate error should wrap the injected removeAllFn error")
	assert.Equal(t, 2, removed,
		"only the 2 unaffected session dirs must be counted; the one whose removeAllFn failed must not be")

	// The bad directory was never actually touched by the real os.RemoveAll
	// (the stub short-circuited it), so it must still exist, fully intact.
	_, statErr := os.Stat(badDir)
	assert.NoError(t, statErr, "the session dir whose removeAllFn failed must still exist on disk")
	_, statErr = os.Stat(filepath.Join(badDir, "meta.json"))
	assert.NoError(t, statErr, "meta.json inside the unremoved dir must still be present")

	// The two unaffected directories must be gone (removed via the real
	// os.RemoveAll passthrough in the stub).
	for _, m := range []*UnifiedMeta{metaGood1, metaGood2} {
		_, statErr := os.Stat(filepath.Join(store.BaseDir(), m.ID))
		assert.True(t, os.IsNotExist(statErr), "unaffected session dir for %s must be gone", m.ID)
	}
}
