// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Tests for SIGKILL recovery — RecoverOrphanedToolCalls (FR-069, FR-088).
//
// PRT T1 reconciliation: crash-recovery DESIGN Decisions 2/5 supersede
// synthetic Deny and whole-group omission. Preserve the original declaration,
// completed results and archive addresses; append only engine-owned missing-
// occurrence observations. Unknown has audit decision=error, never policy Deny.
// Legacy test names remain so existing exact regression selectors still run.

package agent

import (
	"context"
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
func newTestSessionStoreRecovery(t *testing.T) *session.UnifiedStore {
	t.Helper()
	return recoveryStore(t, t.TempDir())
}

// recoveryStore opens the one shared session store (session-core DEL-10:
// SessionManager is deleted) over dir.
func recoveryStore(t *testing.T, dir string) *session.UnifiedStore {
	t.Helper()
	store, err := session.NewUnifiedStore(dir)
	require.NoError(t, err, "NewUnifiedStore")
	t.Cleanup(func() { _ = store.Close() })
	return store
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
	p1Mark, err := buildRecallMark("emptied", "exec", "p1", 2, big, turnNumberForArchiveLine(denseArchive(archive), 2))
	require.NoError(t, err)
	p2Mark, err := buildRecallMark("emptied", "exec", "p2", 3, big, turnNumberForArchiveLine(denseArchive(archive), 3))
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
		policy: capPolicyFor(config.DefaultContextSettings(), 100_000), archive: denseArchive(archive),
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
	store := recoveryStore(t, storage)
	const sessionKey = "test-public-orphaned-turn"
	history := buildOrphanedHistory("tc-001", "exec")
	// Name is an in-memory convenience field; seed the canonical persisted form.
	history[1].ToolCalls[0] = providers.ToolCall{ID: "tc-001", Type: "function",
		Function: &providers.FunctionCall{Name: "exec", Arguments: `{}`}}
	for _, msg := range history {
		store.AddFullMessage(sessionKey, msg)
	}
	require.NoError(t, store.Save(sessionKey), "seed the real persisted session")

	before, err := store.WindowView(context.Background(), sessionKey)
	require.NoError(t, err)
	issuer := r2Issuer(t, before, 1)
	cleaned := RecoverOrphanedToolCalls(store, sessionKey, nil)

	// DESIGN 2/5: user + original declaration + truthful missing observation.
	require.Len(t, cleaned, 3, "preserve the original declaration instead of omitting its group")
	assert.Equal(t, "user", cleaned[0].Role)
	assert.Equal(t, history, cleaned[:len(history)], "recovery preserves every complete original message and field")
	assert.Equal(t, r2ExpectedObservation("tc-001"), r2CheckedObservation(t, cleaned[2], "tc-001", issuer))
	require.NoError(t, validateWindowGroups(cleaned), "retained declaration is paired with its own recovery observation")

	// Reopen: the observation must be durable; original addresses stay intact.
	reloaded := recoveryStore(t, storage)
	persisted := reloaded.GetHistory(sessionKey)
	require.Len(t, persisted, len(history)+1, "one durable observation, not an invented original result")
	assert.Equal(t, history, persisted[:len(history)], "the orphaned assistant remains archived, unchanged")
	assert.Equal(t, "tool", persisted[len(history)].Role, "engine observation is a provider-valid result")
	assert.Equal(t, r2ExpectedObservation("tc-001"), r2CheckedObservation(t, persisted[len(history)], "tc-001", issuer))
	after, err := reloaded.WindowView(context.Background(), sessionKey)
	require.NoError(t, err)
	assert.Equal(t, before.Live, after.Live[:len(before.Live)], "all original archive addresses and source metadata survive")
	assert.Equal(t, cleaned, RecoverOrphanedToolCalls(reloaded, sessionKey, nil), "repeat recovery is idempotent")
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
			store := recoveryStore(t, storage)
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
			assert.Equal(t, history, recoveryStore(t, storage).GetHistory(sessionKey), "no-op recovery changes no persisted message")
		})
	}
}

// TestRecovery_RecoverOrphaned_ExistingMarkerNoNewOrphan_CleanViewNoNewRecord
// closes independent-CHECK finding F1: no leaf exercised "history already
// contains a supported turn_canceled_restart marker from an earlier recovery
// AND no current orphan exists".
//
// Every expected value derives from the contract, never from the
// implementation:
//   - loop_run_turn.go::assembleInitialContext — recovery is idempotent on
//     sessions where the synthetic turn_canceled_restart entry already
//     exists, the on-disk transcript is preserved, and the returned
//     LLM-context slice is cleaned so the provider never sees dangling
//     unanswered tool_call entries (FR-088);
//   - architect Option A — exclude only the locally bound canceled
//     group/results/records; preserve the entire remaining sequence;
//   - tests/plans/orphan-restart-recovery-acceptance.md — "repeated recovery
//     does not duplicate the record" (position-scoped idempotency ruling).
//
// The store is deliberately checkpoint-less (*SessionManager implements no
// session.ContextWindowStore): on that fallback path the returned slice is
// authoritative downstream (assembleMessages uses it as-is), so a wrong
// return reaches the provider.
func TestRecovery_RecoverOrphaned_ExistingMarkerNoNewOrphan_CleanViewNoNewRecord(t *testing.T) {
	storage := t.TempDir()
	store := recoveryStore(t, storage)
	// session-core DEL-10: the one shared store is always a ContextWindowStore,
	// so the old checkpoint-less SessionManager fixture cannot be built; the
	// assertions below (idempotency, marker preservation, archive retention)
	// still hold on the checkpointed store.

	const sessionKey = "test-premarked-no-new-orphan"
	// Session recovered once earlier (marker persisted), one clean turn after.
	seed := []providers.Message{
		{Role: "user", Content: "first question"}, // 0: unrelated valid context
		{Role: "assistant", Content: "", ToolCalls: []providers.ToolCall{ // 1: canceled incomplete group
			{ID: "call_old", Type: "function",
				Function: &providers.FunctionCall{Name: "exec", Arguments: `{}`}},
		}},
		{Role: "system", Content: `{"type":"turn_canceled_restart","tool_call_id":"call_old","reason":"ungraceful_shutdown_recovery"}`}, // 2: marker from the FIRST recovery
		{Role: "user", Content: "second question"},                    // 3: closes the segment after the marker
		{Role: "assistant", Content: "second answer", ToolCalls: nil}, // 4: clean last assistant ⇒ no current orphan
	}
	for _, msg := range seed {
		store.AddFullMessage(sessionKey, msg)
	}
	require.NoError(t, store.Save(sessionKey), "seed the real persisted session")
	before := store.GetHistory(sessionKey)
	require.Len(t, before, 5, "fixture precondition: pre-marked five-message transcript")
	require.JSONEq(t, seed[2].Content, before[2].Content, "fixture precondition: the persisted marker from the earlier recovery")

	view, err := store.WindowView(context.Background(), sessionKey)
	require.NoError(t, err)
	issuer := r2Issuer(t, view, 1)
	cleaned := RecoverOrphanedToolCalls(store, sessionKey, nil)

	// DESIGN 2/5: a retained legacy marker is not a recovered historical
	// return. Add one truthful observation without erasing the declaration or
	// either later clean message. The old marker remains archived once.
	require.Len(t, cleaned, 5)
	checked := append([]providers.Message(nil), cleaned...)
	checked[2] = r2CheckedObservation(t, cleaned[2], "call_old", issuer)
	require.Equal(t, []providers.Message{seed[0], seed[1], r2ExpectedObservation("call_old"), seed[3], seed[4]}, checked,
		"original declaration plus occurrence-bound Unknown; unrelated messages retain order and fields")
	require.NoError(t, validateWindowGroups(cleaned))
	persisted := recoveryStore(t, storage).GetHistory(sessionKey)
	require.Len(t, persisted, len(before)+1, "only the missing occurrence gains one durable observation")
	require.Equal(t, before, persisted[:len(before)], "all five old archive records, including the legacy marker, remain unchanged")
	require.Equal(t, r2ExpectedObservation("call_old"), r2CheckedObservation(t, persisted[len(before)], "call_old", issuer))
	require.Equal(t, cleaned, RecoverOrphanedToolCalls(store, sessionKey, nil), "repeat repair adds nothing")
	require.Equal(t, persisted, store.GetHistory(sessionKey), "no duplicate observation or cancellation")
	markers := 0
	for _, msg := range persisted {
		if msg.Role == "system" && strings.Contains(msg.Content, "turn_canceled_restart") {
			markers++
			require.JSONEq(t, seed[2].Content, msg.Content, "the one cancellation record keeps its exact canonical schema")
		}
	}
	require.Equal(t, 1, markers,
		"repeated recovery must not duplicate the cancellation record (position-scoped idempotency ruling)")
}

// --- RecoverOrphanedToolCalls integration tests ---

// TestRecovery_RecoverOrphaned_AppendsSyntheticEntry verifies the full recovery,
// reconciled to crash-recovery DESIGN Decisions 2/5 (the synthetic-deny and
// whole-group-omission oracles are superseded):
//  1. The original declaration is retained; one durable engine observation
//     covers the missing occurrence — no invented original return.
//  2. The audit entry is the typed Unknown recovery-interrupted event with
//     decision=error — never tool.policy.ask.denied.
//  3. The returned view is provider-valid: declaration plus observation.
//
// Legacy test name retained so existing exact regression selectors still run.
// Traces to: tool-registry-redesign-spec.md FR-069 / FR-088; DESIGN Decisions 2/5
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
	view, err := store.WindowView(context.Background(), sessionKey)
	require.NoError(t, err)
	issuer := r2Issuer(t, view, 1)
	cleanedHistory := RecoverOrphanedToolCalls(store, sessionKey, auditLogger)

	// 1. DESIGN 2/5: the declaration stays; one observation covers the orphan.
	require.Len(t, cleanedHistory, 3, "declaration retained, one engine observation for the missing occurrence")
	assert.Equal(t, buildOrphanedHistory(toolCallID, toolName), cleanedHistory[:2], "original user and declaration survive field-for-field")
	assert.Equal(t, r2ExpectedObservation(toolCallID), r2CheckedObservation(t, cleanedHistory[2], toolCallID, issuer))
	require.NoError(t, validateWindowGroups(cleanedHistory), "declaration and observation form a provider-valid group")

	// 2. Durable transcript: originals unchanged, observation appended.
	fullHistory := store.GetHistory(sessionKey)
	require.Len(t, fullHistory, 3, "one durable record, no invented original result")
	assert.Equal(t, r2ExpectedObservation(toolCallID), r2CheckedObservation(t, fullHistory[2], toolCallID, issuer))

	// 3. DESIGN 5 audit: typed Unknown with decision=error, never ask.denied.
	// Close the logger to flush pending writes before reading the file.
	require.NoError(t, auditLogger.Close())
	auditData, err := readAuditFileRecovery(auditPath)
	require.NoError(t, err, "audit file must be readable")

	foundUnknown := false
	for _, entry := range auditData {
		assert.NotEqual(t, "tool.policy.ask.denied", entry["event"],
			"DESIGN 5: a missing result is never a policy denial")
		if entry["event"] == string(audit.EventToolRecoveryInterrupted) && entry["decision"] == string(audit.DecisionError) {
			if details, ok := entry["details"].(map[string]any); ok {
				if tc, _ := details["tool_call_id"].(string); tc == toolCallID {
					if disposition, _ := details["disposition"].(string); disposition == "unknown" {
						foundUnknown = true
					}
				}
			}
		}
	}
	assert.True(t, foundUnknown,
		"audit must carry the typed recovery-interrupted Unknown for the exact occurrence (DESIGN 5); entries: %v", auditData)
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
// different durable observation records, each bound to its own issuing declaration
// (DESIGN 2/5 reconciliation of the superseded synthetic-entry oracle).
//
// Traces to: tool-registry-redesign-spec.md FR-069; crash-recovery DESIGN Decisions 2/5
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
	require.Len(t, hist1, 3, "declaration retained, one observation appended")
	require.Len(t, hist2, 3, "declaration retained, one observation appended")
	viewA, err := store.WindowView(context.Background(), "session-a")
	require.NoError(t, err)
	viewB, err := store.WindowView(context.Background(), "session-b")
	require.NoError(t, err)

	// DESIGN 2/5: occurrences bind to their own issuing declaration and
	// session, never to a global call-ID cancellation set.
	obsA := r2CheckedObservation(t, hist1[2], "tc-AAA", r2Issuer(t, viewA, 1))
	obsB := r2CheckedObservation(t, hist2[2], "tc-BBB", r2Issuer(t, viewB, 1))
	assert.NotEqual(t, obsA, obsB, "two different sessions must produce different observation records")
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
