// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionPartitionNaming verifies YYYY-MM-DD.jsonl filename is derived from timestamp.
// Traces to: wave1-core-foundation-spec.md Scenario: New session creates partition and metadata (US-5 AC1, FR-011)
func TestSessionPartitionNaming(t *testing.T) {
	tests := []struct {
		name              string
		timestamp         time.Time
		expectedPartition string
	}{
		// Dataset: Session File Edge Cases row 1
		{
			name:              "last millisecond of day",
			timestamp:         time.Date(2026, 3, 28, 23, 59, 59, 999000000, time.UTC),
			expectedPartition: "2026-03-28.jsonl",
		},
		// Dataset: Session File Edge Cases row 2
		{
			name:              "first millisecond of new day",
			timestamp:         time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
			expectedPartition: "2026-03-29.jsonl",
		},
		// Dataset: Session File Edge Cases row 3
		{
			name:              "just after midnight",
			timestamp:         time.Date(2026, 3, 29, 0, 0, 0, 1000000, time.UTC),
			expectedPartition: "2026-03-29.jsonl",
		},
		{
			name:              "noon on a date",
			timestamp:         time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
			expectedPartition: "2026-01-15.jsonl",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The partition naming logic is: ts.UTC().Format("2006-01-02") + ".jsonl"
			partitionName := tc.timestamp.UTC().Format("2006-01-02") + ".jsonl"
			assert.Equal(t, tc.expectedPartition, partitionName,
				"partition name must be YYYY-MM-DD.jsonl for timestamp %v", tc.timestamp)
		})
	}
}

// TestSessionPartitionMidnightBoundary verifies UTC midnight triggers a new partition.
// Traces to: wave1-core-foundation-spec.md Scenario: Midnight rollover creates new partition (US-5 AC2, FR-012)
func TestSessionPartitionMidnightBoundary(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "test-agent")

	meta, err := ps.NewSession("test-channel", "claude-3", "anthropic")
	require.NoError(t, err)
	sessionID := meta.ID

	// Send a message on day 1 (23:59:59 UTC).
	day1Msg := TranscriptEntry{
		ID:        "msg-1",
		Role:      "user",
		Content:   "end of day 1",
		Timestamp: time.Date(2026, 3, 28, 23, 59, 59, 0, time.UTC),
	}
	require.NoError(t, ps.AppendMessage(sessionID, day1Msg))

	// Send a message on day 2 (00:00:00 UTC).
	day2Msg := TranscriptEntry{
		ID:        "msg-2",
		Role:      "assistant",
		Content:   "start of day 2",
		Timestamp: time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, ps.AppendMessage(sessionID, day2Msg))

	// Verify two partition files exist.
	sessionDir := filepath.Join(home, "sessions", sessionID)

	partition1 := filepath.Join(sessionDir, "2026-03-28.jsonl")
	partition2 := filepath.Join(sessionDir, "2026-03-29.jsonl")

	_, err = os.Stat(partition1)
	require.NoError(t, err, "2026-03-28.jsonl must exist for day 1 message")

	_, err = os.Stat(partition2)
	require.NoError(t, err, "2026-03-29.jsonl must exist for day 2 message")

	// Verify meta.json records both partitions.
	updatedMeta, err := ps.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Len(t, updatedMeta.Partitions, 2, "meta.json must list both partitions")
	assert.Contains(t, updatedMeta.Partitions, "2026-03-28.jsonl")
	assert.Contains(t, updatedMeta.Partitions, "2026-03-29.jsonl")
}

// TestSessionWriteIntegration verifies full session create → message append → meta update cycle.
// Traces to: wave1-core-foundation-spec.md Scenario: New session creates partition and metadata (US-5 AC1, AC4)
func TestSessionWriteIntegration(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "integration-agent")

	// Create a new session.
	now := time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC)
	meta, err := ps.NewSession("telegram", "claude-opus", "anthropic")
	require.NoError(t, err)
	require.NotEmpty(t, meta.ID, "session ID must be non-empty")
	assert.True(t, len(meta.ID) > 0, "session must have a ULID-based ID")
	assert.Equal(t, StatusActive, meta.Status)
	assert.Equal(t, "telegram", meta.Channel)

	// Verify meta.json exists.
	sessionDir := filepath.Join(home, "sessions", meta.ID)
	_, err = os.Stat(filepath.Join(sessionDir, "meta.json"))
	require.NoError(t, err, "meta.json must exist after NewSession")

	// Append a message with tool calls.
	entry := TranscriptEntry{
		ID:        "entry-1",
		Role:      "assistant",
		Content:   "I ran the command",
		Timestamp: now,
		Tokens:    42,
		Cost:      0.001,
		ToolCalls: []ToolCall{
			{
				ID:         "tc-1",
				Tool:       "shell",
				Status:     "success",
				DurationMS: 150,
				Parameters: map[string]any{"cmd": "ls -la"},
				Result:     map[string]any{"output": "file1.txt"},
			},
		},
	}
	require.NoError(t, ps.AppendMessage(meta.ID, entry))

	// Verify JSONL partition file exists.
	partitionName := now.UTC().Format("2006-01-02") + ".jsonl"
	partitionPath := filepath.Join(sessionDir, partitionName)
	_, err = os.Stat(partitionPath)
	require.NoError(t, err, "partition file must exist after AppendMessage")

	// Verify partition file contains valid JSONL.
	f, err := os.Open(partitionPath)
	require.NoError(t, err)
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineCount := 0
	for scanner.Scan() {
		line := scanner.Text()
		var e TranscriptEntry
		require.NoError(t, json.Unmarshal([]byte(line), &e),
			"each JSONL line must be valid JSON")
		assert.Equal(t, "entry-1", e.ID)
		assert.Len(t, e.ToolCalls, 1, "tool calls must be serialized")
		assert.Equal(t, "shell", e.ToolCalls[0].Tool)
		lineCount++
	}
	assert.Equal(t, 1, lineCount, "partition must have exactly 1 line")

	// Verify meta.json stats are updated.
	// Assistant messages contribute to TokensOut; user messages to TokensIn.
	updatedMeta, err := ps.GetMeta(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, updatedMeta.Stats.MessageCount)
	assert.Equal(t, 42, updatedMeta.Stats.TokensOut, "assistant message tokens go to TokensOut")
	assert.Equal(t, 42, updatedMeta.Stats.TokensTotal, "TokensTotal must include all tokens")
	assert.InDelta(t, 0.001, updatedMeta.Stats.Cost, 1e-9)
	assert.Equal(t, 1, updatedMeta.Stats.ToolCalls)
}

// TestSessionMultiPartition verifies cross-day message appending creates separate files.
// Traces to: wave1-core-foundation-spec.md Scenario: Midnight rollover creates new partition (US-5 AC2)
func TestSessionMultiPartition(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "multi-agent")

	meta, err := ps.NewSession("cli", "claude-3", "anthropic")
	require.NoError(t, err)

	days := []time.Time{
		time.Date(2026, 3, 27, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 29, 11, 0, 0, 0, time.UTC),
	}

	for i, day := range days {
		msg := TranscriptEntry{
			ID:        "msg-" + day.Format("2006-01-02"),
			Role:      "user",
			Content:   "message on day " + string(rune('A'+i)),
			Timestamp: day,
			Tokens:    10,
		}
		require.NoError(t, ps.AppendMessage(meta.ID, msg),
			"AppendMessage must succeed for day %v", day)
	}

	// Verify 3 partition files exist.
	sessionDir := filepath.Join(home, "sessions", meta.ID)
	expectedPartitions := []string{
		"2026-03-27.jsonl",
		"2026-03-28.jsonl",
		"2026-03-29.jsonl",
	}
	for _, partition := range expectedPartitions {
		_, statErr := os.Stat(filepath.Join(sessionDir, partition))
		assert.NoError(t, statErr, "partition %q must exist", partition)
	}

	// Verify meta lists all partitions.
	updatedMeta, err := ps.GetMeta(meta.ID)
	require.NoError(t, err)
	assert.Len(t, updatedMeta.Partitions, 3, "meta must list all 3 partitions")
}

// TestSessionStatsAggregation verifies stats accumulate correctly across multiple messages.
// Traces to: wave1-core-foundation-spec.md Scenario: Session stats aggregate across partitions (US-5 AC3)
func TestSessionStatsAggregation(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "stats-agent")

	meta, err := ps.NewSession("cli", "claude-3", "anthropic")
	require.NoError(t, err)

	now := time.Now().UTC()

	messages := []TranscriptEntry{
		{ID: "m1", Role: "user", Content: "msg1", Timestamp: now, Tokens: 10, Cost: 0.001},
		{
			ID: "m2", Role: "assistant", Content: "reply1", Timestamp: now, Tokens: 50, Cost: 0.005,
			ToolCalls: []ToolCall{{ID: "tc1", Tool: "shell", Status: "success"}},
		},
		{ID: "m3", Role: "user", Content: "msg2", Timestamp: now, Tokens: 20, Cost: 0.002},
	}

	for _, msg := range messages {
		require.NoError(t, ps.AppendMessage(meta.ID, msg))
	}

	updatedMeta, err := ps.GetMeta(meta.ID)
	require.NoError(t, err)

	// user(10) + user(20) = 30 → TokensIn; assistant(50) → TokensOut; total = 80
	assert.Equal(t, 3, updatedMeta.Stats.MessageCount, "message count must aggregate")
	assert.Equal(t, 30, updatedMeta.Stats.TokensIn, "user tokens: 10+20=30")
	assert.Equal(t, 50, updatedMeta.Stats.TokensOut, "assistant tokens: 50")
	assert.Equal(t, 80, updatedMeta.Stats.TokensTotal, "total tokens: 10+50+20=80")
	assert.InDelta(t, 0.008, updatedMeta.Stats.Cost, 1e-9, "cost must aggregate: 0.001+0.005+0.002")
	assert.Equal(t, 1, updatedMeta.Stats.ToolCalls, "tool call count must aggregate")
}

// TestNewSessionIDFormat verifies session IDs are ULID-based with "session_" prefix.
// Traces to: wave1-core-foundation-spec.md Ambiguity Resolution #2 (Session ID algorithm)
func TestNewSessionIDFormat(t *testing.T) {
	id, err := NewSessionID()
	require.NoError(t, err)
	assert.True(t, len(id) > len("session_"), "session ID must be non-empty after prefix")
	assert.Equal(t, "session_", id[:8], "session ID must start with 'session_'")

	// IDs must be unique.
	id2, err := NewSessionID()
	require.NoError(t, err)
	assert.NotEqual(t, id, id2, "consecutive session IDs must be unique")
}

// --- Wave 5a: ListSessions and ReadMessages ---

// TestListSessions_SortedByUpdatedAtDesc verifies sessions are returned newest-first.
// BDD: Given two sessions with different UpdatedAt timestamps,
// When ListSessions is called,
// Then sessions are sorted by UpdatedAt descending (newest first).
// Traces to: wave5a-wire-ui-spec.md — Scenario: Session list sorted by update time (US-15 AC1)
func TestListSessions_SortedByUpdatedAtDesc(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "sort-test-agent")

	// Create session A — will have an earlier UpdatedAt
	metaA, err := ps.NewSession("cli", "test-model", "anthropic")
	require.NoError(t, err)

	// Append a message to A so its UpdatedAt is set
	require.NoError(t, ps.AppendMessage(metaA.ID, TranscriptEntry{
		ID:        "msg-a",
		Role:      "user",
		Content:   "hello from A",
		Timestamp: time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC),
		Tokens:    5,
	}))

	// Small sleep to ensure UpdatedAt differs
	time.Sleep(5 * time.Millisecond)

	// Create session B — will have a later UpdatedAt
	metaB, err := ps.NewSession("cli", "test-model", "anthropic")
	require.NoError(t, err)
	require.NoError(t, ps.AppendMessage(metaB.ID, TranscriptEntry{
		ID:        "msg-b",
		Role:      "user",
		Content:   "hello from B",
		Timestamp: time.Date(2026, 3, 28, 11, 0, 0, 0, time.UTC),
		Tokens:    5,
	}))

	metas, err := ps.ListSessions()
	require.NoError(t, err)
	require.Len(t, metas, 2, "must return both sessions")

	// B was updated more recently so it should come first
	assert.Equal(t, metaB.ID, metas[0].ID, "session B (newer) must be first")
	assert.Equal(t, metaA.ID, metas[1].ID, "session A (older) must be second")
}

// TestListSessions_EmptyDirReturnsNil verifies an empty (or non-existent) sessions dir returns nil.
// BDD: Given no sessions exist,
// When ListSessions is called,
// Then the result is nil (or empty) and no error.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Session list empty state (US-15 AC2)
func TestListSessions_EmptyDirReturnsNil(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "empty-agent")
	// Don't create any sessions — baseDir doesn't even exist yet.

	metas, err := ps.ListSessions()
	require.NoError(t, err)
	assert.Nil(t, metas, "ListSessions must return nil for a non-existent sessions dir")
}

// TestListSessions_SkipsUnreadableSessions verifies corrupted session dirs are skipped gracefully.
// BDD: Given one valid session and one session dir with a missing/corrupted meta.json,
// When ListSessions is called,
// Then only the valid session is returned and no error is raised.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Session list skips corrupted sessions (US-15 AC3)
func TestListSessions_SkipsUnreadableSessions(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "skip-agent")

	// Create one valid session
	meta, err := ps.NewSession("cli", "test-model", "anthropic")
	require.NoError(t, err)

	// Create a corrupted session dir (directory exists, meta.json missing)
	corruptDir := filepath.Join(home, "sessions", "session_corrupt")
	require.NoError(t, os.MkdirAll(corruptDir, 0o755))

	metas, err := ps.ListSessions()
	require.NoError(t, err, "ListSessions must not error on corrupt dir, just skip it")
	require.Len(t, metas, 1, "only the valid session must be returned")
	assert.Equal(t, meta.ID, metas[0].ID)
}

// TestReadMessages_MergesPartitionsChronologically verifies messages from all partitions
// are returned in declaration order (partition list order in meta.json).
// BDD: Given a session with messages across two day partitions,
// When ReadMessages is called,
// Then messages from all partitions are returned in meta.Partitions order.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Session messages merged across partitions (US-15 AC4)
func TestReadMessages_MergesPartitionsChronologically(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "merge-agent")

	meta, err := ps.NewSession("cli", "test-model", "anthropic")
	require.NoError(t, err)

	day1Msg := TranscriptEntry{
		ID:        "msg-day1",
		Role:      "user",
		Content:   "day 1 message",
		Timestamp: time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC),
		Tokens:    10,
	}
	day2Msg := TranscriptEntry{
		ID:        "msg-day2",
		Role:      "assistant",
		Content:   "day 2 reply",
		Timestamp: time.Date(2026, 3, 29, 9, 0, 0, 0, time.UTC),
		Tokens:    20,
	}

	require.NoError(t, ps.AppendMessage(meta.ID, day1Msg))
	require.NoError(t, ps.AppendMessage(meta.ID, day2Msg))

	entries, err := ps.ReadMessages(meta.ID)
	require.NoError(t, err)
	require.Len(t, entries, 2, "both messages must be returned")
	assert.Equal(t, "msg-day1", entries[0].ID, "day 1 message must come first")
	assert.Equal(t, "msg-day2", entries[1].ID, "day 2 message must come second")
}

// TestReadMessages_EmptySession verifies a session with no messages returns an empty slice.
// BDD: Given a session with no appended messages,
// When ReadMessages is called,
// Then the result is an empty (non-nil) slice and no error.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Empty session returns empty messages (US-15 AC5)
func TestReadMessages_EmptySession(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "empty-msg-agent")

	meta, err := ps.NewSession("cli", "test-model", "anthropic")
	require.NoError(t, err)

	entries, err := ps.ReadMessages(meta.ID)
	require.NoError(t, err)
	// Implementation returns nil (not empty slice) when no partitions exist — both are "empty".
	assert.Empty(t, entries, "no messages should be returned for an empty session")
}

// TestReadMessages_MissingPartitionSkipped verifies that if a partition file listed in
// meta.json is missing, it is skipped (warning logged) and remaining partitions are returned.
// BDD: Given meta.json lists two partitions but one file is deleted,
// When ReadMessages is called,
// Then messages from the remaining partition are returned and no error is raised.
// Traces to: wave5a-wire-ui-spec.md — Scenario: Missing partition skipped gracefully (US-15 AC6)
func TestReadMessages_MissingPartitionSkipped(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "missing-part-agent")

	meta, err := ps.NewSession("cli", "test-model", "anthropic")
	require.NoError(t, err)

	// Append messages on two different days
	require.NoError(t, ps.AppendMessage(meta.ID, TranscriptEntry{
		ID: "msg-1", Role: "user", Content: "day 1",
		Timestamp: time.Date(2026, 3, 28, 10, 0, 0, 0, time.UTC), Tokens: 5,
	}))
	require.NoError(t, ps.AppendMessage(meta.ID, TranscriptEntry{
		ID: "msg-2", Role: "assistant", Content: "day 2",
		Timestamp: time.Date(2026, 3, 29, 9, 0, 0, 0, time.UTC), Tokens: 5,
	}))

	// Delete the first partition file to simulate corruption/missing file
	sessionDir := filepath.Join(home, "sessions", meta.ID)
	require.NoError(t, os.Remove(filepath.Join(sessionDir, "2026-03-28.jsonl")))

	entries, err := ps.ReadMessages(meta.ID)
	require.NoError(t, err, "ReadMessages must not error on missing partition")
	require.Len(t, entries, 1, "only messages from the surviving partition must be returned")
	assert.Equal(t, "msg-2", entries[0].ID)
}

// TestAppendMessage_TokensIn_UnpopulatedByRealisticCaller is the RC-7
// regression test. RC-7 defect: PartitionStore.AppendMessage folds
// non-assistant-role TranscriptEntry.Tokens into meta.Stats.TokensIn, but no
// production caller ever sets Tokens on a user/system-role entry — every
// session in production reports tokens_in: 0, including sessions with
// millions of tokens_out. This test has two halves:
//
//  1. It reproduces the REALISTIC production shape — a user-role
//     TranscriptEntry built the way every real caller builds one (ID, Role,
//     Content, Timestamp set; Tokens left at its zero value — mirroring
//     pkg/agent/loop.go:6508's userEntry literal and the equivalent
//     construction sites in pkg/gateway/sse.go and pkg/gateway/
//     websocket.go) — and asserts TokensIn stays exactly 0 after several
//     such messages plus an assistant reply. If this ever silently starts
//     returning non-zero without any caller change, something is
//     fabricating a token count from nowhere, which is worse than an
//     honest 0.
//  2. It proves the aggregation MECHANISM is not itself the bug: giving a
//     non-assistant entry a real Tokens value (the shape a fixed caller
//     WOULD produce once pkg/agent/turn.go or pkg/gateway is wired — see
//     AppendMessage's doc comment for exactly where) correctly increments
//     TokensIn. So the day a caller in one of those out-of-scope files
//     starts setting entry.Tokens, TokensIn becomes meaningful with zero
//     further changes needed in this file.
//
// Traces to: RC-7 (fix/uat-delegation-rootcauses root-cause investigation).
func TestAppendMessage_TokensIn_UnpopulatedByRealisticCaller(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "rc7-tokens-in-agent")

	meta, err := ps.NewSession("cli", "test-model", "anthropic")
	require.NoError(t, err)

	now := time.Now().UTC()

	// Realistic shape: mirrors pkg/agent/loop.go:6508's userEntry literal —
	// ID/Role/Content/Timestamp set, Tokens never touched. No production
	// caller sets Tokens on a non-assistant entry today.
	realisticUserEntry := TranscriptEntry{
		ID:        "rc7-user-1",
		Role:      "user",
		Content:   "do something",
		Timestamp: now,
	}
	require.NoError(t, ps.AppendMessage(meta.ID, realisticUserEntry))

	// A large assistant reply, matching the reported production shape
	// (millions of TokensOut, zero TokensIn).
	assistantEntry := TranscriptEntry{
		ID:        "rc7-assistant-1",
		Role:      "assistant",
		Content:   "reply",
		Timestamp: now,
		Tokens:    1_400_000,
		Model:     "test-model",
	}
	require.NoError(t, ps.AppendMessage(meta.ID, assistantEntry))

	afterRealistic, err := ps.GetMeta(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, afterRealistic.Stats.TokensIn,
		"TokensIn must stay honestly 0 when no caller populates Tokens on a non-assistant entry — "+
			"a non-zero value here would mean something is fabricating a token count")
	assert.Equal(t, 1_400_000, afterRealistic.Stats.TokensOut,
		"TokensOut must still aggregate normally — this test isolates the TokensIn gap, not a general regression")

	// Second half: prove the aggregation MECHANISM works once a caller DOES
	// populate Tokens — i.e. the only gap is the missing caller-side write,
	// not the summation in AppendMessage.
	wiredUserEntry := TranscriptEntry{
		ID:        "rc7-user-2",
		Role:      "user",
		Content:   "a second message from a hypothetically-fixed caller",
		Timestamp: now,
		Tokens:    42, // as if a caller had set this from providers.LLMResponse.Usage.PromptTokens
	}
	require.NoError(t, ps.AppendMessage(meta.ID, wiredUserEntry))

	afterWired, err := ps.GetMeta(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, 42, afterWired.Stats.TokensIn,
		"once a caller sets Tokens on a non-assistant entry, AppendMessage must correctly fold it into TokensIn — "+
			"proving the aggregation logic itself is not the defect, only the missing caller-side wiring is")
}
