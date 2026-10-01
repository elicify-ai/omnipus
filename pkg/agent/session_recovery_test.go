// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Tests for SIGKILL recovery — RecoverOrphanedToolCalls (FR-069, FR-088).
//
// BDD Scenario: "SIGKILL recovery — orphaned tool_call gets synthetic deny on next boot"
//
// Given a session JSONL transcript whose tail contains a tool_call with no matching
//   tool_result (gateway was SIGKILL'd while paused awaiting approval),
// And no in-process pending approval matches the orphaned tool_call_id,
// When the gateway restarts and the session is loaded,
// Then a synthetic entry {role: "system", type: "turn_canceled_restart",
//   tool_call_id: <orphan>, reason: "ungraceful_shutdown_recovery"} is appended,
// And an audit event tool.policy.ask.denied with reason: "restart" is emitted at session-load time,
// And the orphaned turn is not resumed; the next user message starts a fresh turn.
//
// Traces to: tool-registry-redesign-spec.md BDD "SIGKILL recovery" / FR-069 / FR-088

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// newTestSessionStoreRecovery creates a session store in a temp directory.
func newTestSessionStoreRecovery(t *testing.T) session.SessionStore {
	t.Helper()
	dir := t.TempDir()
	return session.NewSessionManager(dir)
}

// newTestAuditLoggerRecovery creates a file-based audit logger in a temp dir.
func newTestAuditLoggerRecovery(t *testing.T) (*audit.Logger, string) {
	t.Helper()
	dir := t.TempDir()
	lg, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  1024 * 1024,
		RetentionDays: 1,
	})
	require.NoError(t, err, "NewLogger must succeed")
	t.Cleanup(func() { lg.Close() })
	return lg, filepath.Join(dir, "audit.jsonl")
}

// buildOrphanedHistory returns a history slice that simulates a SIGKILL mid-turn:
// an assistant message with one tool_call but no subsequent tool_result.
func buildOrphanedHistory(toolCallID, toolName string) []providers.Message {
	return []providers.Message{
		{Role: "user", Content: "run exec for me"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []providers.ToolCall{
				{
					ID:   toolCallID,
					Name: toolName,
				},
			},
		},
		// No "tool" result message — gateway was killed here.
	}
}

// buildCleanHistory returns a history slice with a matching tool_result.
func buildCleanHistory(toolCallID, toolName string) []providers.Message {
	return []providers.Message{
		{Role: "user", Content: "run exec for me"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []providers.ToolCall{
				{
					ID:   toolCallID,
					Name: toolName,
				},
			},
		},
		{
			Role:       "tool",
			Content:    "file1.txt\nfile2.txt",
			ToolCallID: toolCallID,
		},
	}
}

// --- ADR-066 D5: emptying never creates an orphan ---

// TestProjection_NeverOrphans (spec §7 regression table, "Orphan recovery"
// row): an emptying pass and the reload projection both leave every tool
// call paired — the slot, role and tool_call_id survive, only the content
// changes — so findOrphanedToolCalls sees nothing new and the recovery path
// never fires because of D5. Also pins that a result whose call is NOT in
// the slice is left alone by the pass (recovery owns it, not D5).
func TestProjection_NeverOrphans(t *testing.T) {
	big := strings.Repeat("o", 3000)
	history := []providers.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "p1", Name: "exec", Function: &providers.FunctionCall{Name: "exec"}},
			{ID: "p2", Name: "exec", Function: &providers.FunctionCall{Name: "exec"}},
		}},
		{Role: "tool", ToolCallID: "p1", Content: big},
		{Role: "tool", ToolCallID: "p2", Content: big},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "p3", Name: "exec", Function: &providers.FunctionCall{Name: "exec"}},
		}},
		{Role: "tool", ToolCallID: "p3", Content: big},
	}
	archive := make([]memory.ArchivedMessage, len(history))
	for i, m := range history {
		archive[i] = memory.ArchivedMessage{Message: m}
	}
	lineOf := func(i int) int { return i }
	require.Empty(t, findOrphanedToolCalls(history), "precondition: clean history")

	// Live pass: p1 and p2 (not the floor set, p3) end up emptied. D5's own
	// standalone eligibility/empty pass (eligibleToolResults/emptyOldestFirst)
	// was retired after the R1 GREEN refactor moved emptying behind a new
	// call site, so the "live" emptied state is built directly here with the
	// same buildRecallMark primitive both the (new) live path and the reload
	// projection below share (empty_in_place.go's own doc comment: "the mark
	// is the same byte string projection.go produces for the persisted
	// state, by construction").
	live := append([]providers.Message(nil), history...)
	p1Mark, err := buildRecallMark("emptied", "exec", "p1", 2, big, turnNumberForArchiveLine(archive, 2))
	require.NoError(t, err)
	p2Mark, err := buildRecallMark("emptied", "exec", "p2", 3, big, turnNumberForArchiveLine(archive, 3))
	require.NoError(t, err)
	live[2].Content = p1Mark
	live[3].Content = p2Mark
	assert.Empty(t, findOrphanedToolCalls(live), "an emptied result still answers its call")
	assert.Len(t, live, len(history), "no slot removed")

	// Reload projection of the persisted state: same shape, same verdict.
	set := memory.ProjectionSet{
		{ToolCallID: "p1", ArchiveLine: 2}: memory.ProjectionEmptied,
		{ToolCallID: "p2", ArchiveLine: 3}: memory.ProjectionEmptied,
	}
	projected := projectMessages(history, lineOf, set, projectionContext{
		policy: capPolicyFor(config.DefaultContextSettings(), 100_000), archive: archive,
	})
	assert.Empty(t, findOrphanedToolCalls(projected))
	for i := range projected {
		assert.Equal(t, history[i].Role, projected[i].Role)
		assert.Equal(t, history[i].ToolCallID, projected[i].ToolCallID)
		assert.Equal(t, history[i].ToolCalls, projected[i].ToolCalls)
	}

	// A genuine orphan (call missing) is not D5's business: recovery still
	// owns it (the dead eligibility pass's own exclusion of orphans was
	// pinned only by the now-deleted TestEmptyInPlace_EligibilityAndOrder /
	// TestEligibleToolResults_FloorIsIndexKeyedNotIDKeyed; both of those
	// functions were retired with D5's old standalone pass).
	orphaned := buildOrphanedHistory("tc-orphan", "exec")
	orphaned = append(orphaned, providers.Message{Role: "tool", ToolCallID: "stray", Content: big})
	assert.Len(t, findOrphanedToolCalls(orphaned), 1, "recovery still sees the orphaned call")
}

// --- findOrphanedToolCalls unit tests ---

// TestRecovery_FindOrphans_DetectsOrphanedCall verifies that findOrphanedToolCalls
// identifies a tool call with no matching result.
//
// Traces to: tool-registry-redesign-spec.md FR-069
func TestRecovery_FindOrphans_DetectsOrphanedCall(t *testing.T) {
	history := buildOrphanedHistory("tc-001", "exec")
	orphans := findOrphanedToolCalls(history)

	require.Len(t, orphans, 1, "must find exactly one orphaned tool call")
	assert.Equal(t, "tc-001", orphans[0].ToolCallID)
	assert.Equal(t, "exec", orphans[0].ToolName)
}

// TestRecovery_FindOrphans_NoOrphansOnCleanHistory verifies that
// findOrphanedToolCalls is a no-op when every tool_call has a result.
//
// Traces to: tool-registry-redesign-spec.md FR-069
func TestRecovery_FindOrphans_NoOrphansOnCleanHistory(t *testing.T) {
	history := buildCleanHistory("tc-complete", "exec")
	orphans := findOrphanedToolCalls(history)
	assert.Len(t, orphans, 0, "clean history must have zero orphans")
}

// TestRecovery_FindOrphans_EmptyHistory verifies no-op on empty input.
//
// Traces to: tool-registry-redesign-spec.md FR-069
func TestRecovery_FindOrphans_EmptyHistory(t *testing.T) {
	assert.Len(t, findOrphanedToolCalls(nil), 0)
	assert.Len(t, findOrphanedToolCalls([]providers.Message{}), 0)
}

// TestRecovery_FindOrphans_MultipleOrphans verifies detection when the last
// assistant message has multiple tool calls with only partial results.
//
// Traces to: tool-registry-redesign-spec.md FR-069
func TestRecovery_FindOrphans_MultipleOrphans(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "do stuff"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{ID: "tc-A", Name: "exec"},
				{ID: "tc-B", Name: "read_file"},
				{ID: "tc-C", Name: "write_file"},
			},
		},
		// Only tc-B has a result (partial completion).
		{Role: "tool", Content: "ok", ToolCallID: "tc-B"},
	}

	orphans := findOrphanedToolCalls(history)
	require.Len(t, orphans, 2, "must find two orphaned tool calls")

	ids := make(map[string]bool)
	for _, o := range orphans {
		ids[o.ToolCallID] = true
	}
	assert.True(t, ids["tc-A"], "tc-A must be in orphans")
	assert.True(t, ids["tc-C"], "tc-C must be in orphans")
	assert.False(t, ids["tc-B"], "tc-B must NOT be in orphans (has a result)")
}

// --- Public recovery regressions (legacy test names retained for selectors) ---

// TestRecovery_StripOrphanedTurn_RemovesOrphanedAssistant verifies that public
// recovery excludes an unresolved assistant from context but preserves the
// original transcript and appends its restart-cancellation record.
//
// Traces to: tool-registry-redesign-spec.md FR-069 / FR-088 and architect Option A.
func TestRecovery_StripOrphanedTurn_RemovesOrphanedAssistant(t *testing.T) {
	storage := t.TempDir()
	store := session.NewSessionManager(storage)
	const sessionKey = "test-public-orphaned-turn"
	history := buildOrphanedHistory("tc-001", "exec")
	// Name is an in-memory convenience field; seed the canonical persisted form.
	history[1].ToolCalls[0] = providers.ToolCall{ID: "tc-001", Type: "function",
		Function: &providers.FunctionCall{Name: "exec", Arguments: `{}`}}
	for _, msg := range history {
		store.AddFullMessage(sessionKey, msg)
	}
	require.NoError(t, store.Save(sessionKey), "seed the real persisted session")

	cleaned := RecoverOrphanedToolCalls(store, sessionKey, nil)

	// Only the original user message should remain in the recovered view.
	require.Len(t, cleaned, 1, "cleaned history must have only the user message")
	assert.Equal(t, "user", cleaned[0].Role)
	assert.Equal(t, []providers.Message{history[0]}, cleaned, "recovery preserves the complete original user message")

	// Reopen from disk: a cleaned context is not permission to erase the archive
	// or to treat an in-memory-only cancellation as durable recovery.
	persisted := session.NewSessionManager(storage).GetHistory(sessionKey)
	require.Len(t, persisted, len(history)+1, "recovery appends exactly one cancellation to the original persisted transcript")
	assert.Equal(t, history, persisted[:len(history)], "the orphaned assistant remains archived, unchanged")
	assert.Equal(t, "system", persisted[len(history)].Role, "cancellation is a system record")
	require.JSONEq(t, `{"type":"turn_canceled_restart","tool_call_id":"tc-001","reason":"ungraceful_shutdown_recovery"}`,
		persisted[len(history)].Content, "architect Option A retains the exact one-l restart-record schema")
}

// TestRecovery_StripOrphanedTurn_NoOpOnHistoryWithNoToolCalls verifies that public
// recovery leaves ordinary assistant history unchanged. A resolved-call control
// also proves that no unresolved calls means no recovery, not unconditional stripping.
//
// Traces to: tool-registry-redesign-spec.md FR-088 and architect Option A's completed-group preservation.
func TestRecovery_StripOrphanedTurn_NoOpOnHistoryWithNoToolCalls(t *testing.T) {
	completed := buildCleanHistory("tc-complete", "exec")
	completed[1].ToolCalls[0] = providers.ToolCall{ID: "tc-complete", Type: "function",
		Function: &providers.FunctionCall{Name: "exec", Arguments: `{}`}}
	cases := []struct {
		name    string
		history []providers.Message
	}{
		{"no_tool_calls", []providers.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hello back", ToolCalls: nil},
		}},
		{"resolved_tool_call", completed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storage := t.TempDir()
			store := session.NewSessionManager(storage)
			const sessionKey = "test-public-recovery-no-op"
			history := tc.history
			for _, msg := range history {
				store.AddFullMessage(sessionKey, msg)
			}
			require.NoError(t, store.Save(sessionKey), "seed the real persisted session")

			cleaned := RecoverOrphanedToolCalls(store, sessionKey, nil)
			assert.Len(t, cleaned, len(history),
				"history with no tool_calls in last assistant message must be unchanged")
			assert.Equal(t, history, cleaned, "every message and field survives when no call is unresolved")
			assert.Equal(t, history, store.GetHistory(sessionKey), "no-op recovery appends no in-memory cancellation")
			assert.Equal(t, history, session.NewSessionManager(storage).GetHistory(sessionKey), "no-op recovery changes no persisted message")
		})
	}
}

// --- RecoverOrphanedToolCalls integration tests ---

// TestRecovery_RecoverOrphaned_AppendsSyntheticEntry verifies the full recovery:
//  1. A synthetic system message is appended to the transcript.
//  2. An audit event tool.policy.ask.denied reason=restart is emitted.
//  3. The returned history excludes the orphaned assistant turn (FR-088).
//
// BDD: "SIGKILL recovery — orphaned tool_call gets synthetic deny on next boot"
// Traces to: tool-registry-redesign-spec.md FR-069 / FR-088
func TestRecovery_RecoverOrphaned_AppendsSyntheticEntry(t *testing.T) {
	store := newTestSessionStoreRecovery(t)
	auditLogger, auditPath := newTestAuditLoggerRecovery(t)

	const sessionKey = "test-sigkill-session"
	const toolCallID = "tc-sigkill-001"
	const toolName = "exec"

	// Seed the session with an orphaned history.
	for _, msg := range buildOrphanedHistory(toolCallID, toolName) {
		store.AddFullMessage(sessionKey, msg)
	}
	require.NoError(t, store.Save(sessionKey), "Save must succeed")

	// Run recovery.
	cleanedHistory := RecoverOrphanedToolCalls(store, sessionKey, auditLogger)

	// 1. Cleaned history must not contain the orphaned assistant turn (FR-088).
	for _, msg := range cleanedHistory {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			t.Errorf("cleaned history must NOT contain assistant message with tool calls")
		}
	}

	// 2. The session transcript must have a synthetic system entry appended.
	fullHistory := store.GetHistory(sessionKey)
	foundSynthetic := false
	for _, msg := range fullHistory {
		if msg.Role == "system" && strings.Contains(msg.Content, "turn_canceled_restart") {
			foundSynthetic = true
			assert.Contains(t, msg.Content, toolCallID,
				"synthetic entry must contain the orphaned tool_call_id")
			assert.Contains(t, msg.Content, "ungraceful_shutdown_recovery",
				"synthetic entry must contain the recovery reason")
		}
	}
	assert.True(t, foundSynthetic,
		"session transcript must contain a synthetic turn_canceled_restart entry")

	// 3. Audit event must have been emitted.
	// Close the logger to flush pending writes before reading the file.
	require.NoError(t, auditLogger.Close())
	auditData, err := readAuditFileRecovery(auditPath)
	require.NoError(t, err, "audit file must be readable")

	foundAuditEvent := false
	for _, entry := range auditData {
		if entry["event"] == "tool.policy.ask.denied" {
			if details, ok := entry["details"].(map[string]any); ok {
				if reason, _ := details["reason"].(string); reason == "restart" {
					foundAuditEvent = true
					tc, _ := details["tool_call_id"].(string)
					assert.Equal(t, toolCallID, tc,
						"audit event must contain the orphaned tool_call_id")
				}
			}
		}
	}
	assert.True(t, foundAuditEvent,
		"audit event tool.policy.ask.denied with reason=restart must be emitted")
}

// TestRecovery_RecoverOrphaned_NoOpOnCleanSession verifies that the function
// is a no-op when no orphaned calls exist.
//
// Traces to: tool-registry-redesign-spec.md FR-069
func TestRecovery_RecoverOrphaned_NoOpOnCleanSession(t *testing.T) {
	store := newTestSessionStoreRecovery(t)
	const sessionKey = "test-clean-session"

	for _, msg := range buildCleanHistory("tc-ok", "read_file") {
		store.AddFullMessage(sessionKey, msg)
	}
	require.NoError(t, store.Save(sessionKey))

	initialHistory := store.GetHistory(sessionKey)
	initialLen := len(initialHistory)

	cleanedHistory := RecoverOrphanedToolCalls(store, sessionKey, nil)
	assert.Len(t, cleanedHistory, initialLen,
		"clean session history must be unchanged by recovery")
}

// TestRecovery_RecoverOrphaned_NilAuditLogger verifies no panic with nil logger.
//
// Traces to: tool-registry-redesign-spec.md FR-069
func TestRecovery_RecoverOrphaned_NilAuditLogger(t *testing.T) {
	store := newTestSessionStoreRecovery(t)
	const sessionKey = "test-no-audit"
	const toolCallID = "tc-no-audit"

	for _, msg := range buildOrphanedHistory(toolCallID, "exec") {
		store.AddFullMessage(sessionKey, msg)
	}
	require.NoError(t, store.Save(sessionKey))

	assert.NotPanics(t, func() {
		RecoverOrphanedToolCalls(store, sessionKey, nil)
	}, "RecoverOrphanedToolCalls must not panic with nil audit logger")
}

// TestRecovery_RecoverOrphaned_DifferentInputsDifferentOutputs is the differentiation
// test: two different sessions with different orphaned tool_call_ids must produce
// different synthetic entries.
//
// Traces to: tool-registry-redesign-spec.md FR-069
func TestRecovery_RecoverOrphaned_DifferentInputsDifferentOutputs(t *testing.T) {
	store := newTestSessionStoreRecovery(t)

	for _, msg := range buildOrphanedHistory("tc-AAA", "exec") {
		store.AddFullMessage("session-a", msg)
	}
	for _, msg := range buildOrphanedHistory("tc-BBB", "read_file") {
		store.AddFullMessage("session-b", msg)
	}
	require.NoError(t, store.Save("session-a"))
	require.NoError(t, store.Save("session-b"))

	RecoverOrphanedToolCalls(store, "session-a", nil)
	RecoverOrphanedToolCalls(store, "session-b", nil)

	hist1 := store.GetHistory("session-a")
	hist2 := store.GetHistory("session-b")

	var syn1, syn2 string
	for _, m := range hist1 {
		if m.Role == "system" && strings.Contains(m.Content, "turn_canceled_restart") {
			syn1 = m.Content
		}
	}
	for _, m := range hist2 {
		if m.Role == "system" && strings.Contains(m.Content, "turn_canceled_restart") {
			syn2 = m.Content
		}
	}

	require.NotEmpty(t, syn1, "session-a must have a synthetic entry")
	require.NotEmpty(t, syn2, "session-b must have a synthetic entry")
	assert.Contains(t, syn1, "tc-AAA", "session-a synthetic must reference tc-AAA")
	assert.Contains(t, syn2, "tc-BBB", "session-b synthetic must reference tc-BBB")
	assert.NotEqual(t, syn1, syn2, "two different sessions must produce different synthetic entries")
}

// readAuditFileRecovery reads a JSONL audit file and returns parsed records.
func readAuditFileRecovery(path string) ([]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue
		}
		records = append(records, r)
	}
	return records, nil
}
