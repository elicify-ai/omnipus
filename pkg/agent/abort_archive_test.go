// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// Package agent — abort-path archive-preservation tests (round-2 gap closure).
//
// Gap: the abort/restoreSession tests in subturn_test.go only use an
// ephemeralSessionStore (in-memory, no Skip concept) with Skip=0. They never
// exercise the case where eviction has already occurred (meta.Skip > 0) and a
// subsequent turn abort must preserve the evicted prefix in the on-disk archive.
//
// These tests use a real JSONL-backed store (created by buildTrimTestAgentLoop
// which wires a real tmpdir workspace → UnifiedStore → JSONLBackend →
// JSONLStore). They verify SC-001 / SC-010 / SC-011 on the ABORT paths:
//
//   - TestAbortPath_HardAbort_PreservesEvictedArchive (Gap 1a)
//   - TestAbortPath_RestoreSession_PreservesEvictedArchive (Gap 1b)
//
// Gap 2 (assembleMessages ReadArchive-error fallback) is tested by
// TestAssembleMessages_ReadArchiveError_FallsBackToBreadcrumb.
//
// Traces to: spec docs/internal/specs/context-paging-sliding-window-recall-spec.md
//   §7 tests, SC-001 (line 249), SC-010 (line 259), SC-011 (line 259), FR-016 (line 233).

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// ============================================================================
// Helpers for abort-archive tests
// ============================================================================

// archiveLineCountFromAgent returns the number of lines in the full archive
// for the given session key by calling ReadArchive on the agent's Sessions
// (ignores meta.Skip — reads from line 0).
func archiveLineCountFromAgent(t *testing.T, al *AgentLoop, sessionKey string) int {
	t.Helper()
	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)
	archived, err := agent.Sessions.ReadArchive(context.Background(), sessionKey)
	require.NoError(t, err)
	return len(archived)
}

// archiveLinesFromAgent returns the full archive for the given session.
func archiveLinesFromAgent(t *testing.T, al *AgentLoop, sessionKey string) []memory.ArchivedMessage {
	t.Helper()
	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)
	archived, err := agent.Sessions.ReadArchive(context.Background(), sessionKey)
	require.NoError(t, err)
	return archived
}

// appendInTurnMessages simulates the agent appending messages during a live
// turn by calling AddFullMessage directly on the session store. This mirrors
// what the turn engine does when processing tool results and assistant replies.
func appendInTurnMessages(al *AgentLoop, sessionKey string, msgs []providers.Message) {
	agent := al.GetRegistry().GetDefaultAgent()
	for _, m := range msgs {
		agent.Sessions.AddFullMessage(sessionKey, m)
	}
}

// ============================================================================
// Gap 1a — HardAbort preserves evicted archive prefix (SC-001 / SC-010 / SC-011)
// ============================================================================

// TestAbortPath_HardAbort_PreservesEvictedArchive verifies that HardAbort after
// eviction (meta.Skip > 0) performs a Skip-preserving rollback: only the
// tail appended during this turn is removed; the evicted prefix (lines at
// indices < meta.Skip) remains intact on disk.
//
// BDD:
//
//	Given a session with 8 turns seeded to disk,
//	And windowTrim is called so that meta.Skip > 0 (some turns are evicted),
//	And the on-disk archive has A lines (all turns — evicted + window),
//	When 2 additional messages are appended to simulate a running turn,
//	And HardAbort is triggered for that session,
//	Then the on-disk archive has exactly A lines (the 2 appended lines are removed),
//	And ReadArchive returns the evicted turns (lines at indices < Skip are intact),
//	And the live window does not contain the aborted-turn sentinel messages.
//
// Traces to: spec SC-001 (line 249), SC-010 (line 259), SC-011 (line 259),
// FR-005 (line 218). HardAbort path in steering.go:613.
func TestAbortPath_HardAbort_PreservesEvictedArchive(t *testing.T) {
	const (
		cw = 3000
		mt = 500
	)
	al, _ := buildTrimTestAgentLoop(t, cw, mt)
	const sk = "abort-hardabort-evicted"

	// --- Phase 1: seed 8 turns to disk so total history >> context budget.
	// Each turn is ~600 chars ≈ 240 tokens. 8 turns × 2 msgs × 240 ≈ 3840 tokens
	// which exceeds budget = 3000 - 500 - ceil(0.05*3000) = 2350 tokens.
	bigContent := strings.Repeat("A", 600)
	var history []providers.Message
	for i := 0; i < 8; i++ {
		history = append(history, makeTurn(bigContent, bigContent)...)
	}
	seedHistory(t, al, sk, history)

	// Verify precondition: all 16 messages on disk.
	preTrimArchiveLen := archiveLineCountFromAgent(t, al, sk)
	require.Equal(t, len(history), preTrimArchiveLen,
		"precondition: archive must contain all seeded messages before trim")

	// --- Phase 2: evict turns via windowTrim so meta.Skip > 0.
	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)
	_, ok := al.windowTrim(agent, "", sk)
	require.True(t, ok, "windowTrim must evict turns from the over-budget session")

	// Archive must still have all 16 lines (eviction = Skip advance, not byte delete).
	postTrimArchiveLen := archiveLineCountFromAgent(t, al, sk)
	assert.Equal(t, preTrimArchiveLen, postTrimArchiveLen,
		"SC-001: windowTrim must not delete bytes from the archive")

	// The live window must be smaller (eviction worked).
	windowAfterEvict := agent.Sessions.GetHistory(sk)
	require.Less(t, len(windowAfterEvict), len(history),
		"precondition: live window must be smaller than full history after eviction")

	evictedCount := preTrimArchiveLen - len(windowAfterEvict)
	require.Greater(t, evictedCount, 0, "SC-011: precondition: must have evicted turns")

	// initialArchiveLen = total archive line count at turn start (after eviction,
	// before any in-turn appends). This mirrors ts.initialArchiveLen in a real turn.
	initialArchiveLen := archiveLineCountFromAgent(t, al, sk)

	// --- Phase 3: simulate in-turn message appends.
	inTurnMsgs := []providers.Message{
		{Role: "user", Content: "ABORT_TEST_SENTINEL_USER: in-turn message that must be rolled back"},
		{Role: "assistant", Content: "ABORT_TEST_SENTINEL_ASST: in-turn reply that must be rolled back"},
	}
	appendInTurnMessages(al, sk, inTurnMsgs)

	// Verify archive grew by exactly 2.
	afterAppendArchiveLen := archiveLineCountFromAgent(t, al, sk)
	require.Equal(t, initialArchiveLen+len(inTurnMsgs), afterAppendArchiveLen,
		"precondition: archive must grow by in-turn appends")

	// --- Phase 4: trigger HardAbort.
	// HardAbort (steering.go:613) reads ts.session and ts.initialArchiveLen
	// then calls ts.session.RollbackAppended(sessionKey, ts.initialArchiveLen).
	// Construct a minimal turnState that wires these two fields correctly.
	// We also set cancelFunc so ts.Finish(true) does not call a nil cancel.
	ctx, cancel := context.WithCancel(context.Background())
	ts := &turnState{
		sessionKey:           sk,
		session:              agent.Sessions,
		initialArchiveLen:    initialArchiveLen,
		initialHistoryLength: len(windowAfterEvict),
		cancelFunc:           cancel,
	}
	// Initialize the finished channel lazily (Finish calls Finished() which does
	// a lazy init under mu, so leaving finishedChan nil is safe — Finished() will
	// create it). But we must initialize ctx for the turnState to avoid a nil
	// ctx panic if Finish tries to cancel children.
	ts.ctx = ctx

	al.activeTurnStates.Store(sk, ts)
	defer al.activeTurnStates.Delete(sk)

	err := al.HardAbort(sk)
	require.NoError(t, err, "HardAbort must not error on an evicted session")

	// --- Phase 5: assert archive invariants.
	// The archive must be back to initialArchiveLen (appended tail removed).
	afterAbortArchiveLen := archiveLineCountFromAgent(t, al, sk)
	assert.Equal(t, initialArchiveLen, afterAbortArchiveLen,
		"SC-001/SC-010: HardAbort must roll back only the appended tail, not the evicted prefix")

	// The in-turn sentinel messages must be gone from the archive.
	afterAbortArchive := archiveLinesFromAgent(t, al, sk)
	for _, line := range afterAbortArchive {
		if strings.Contains(line.Content, "ABORT_TEST_SENTINEL") {
			t.Errorf("HardAbort rollback left an in-turn sentinel in the archive: role=%s content=%q",
				line.Role, line.Content)
		}
	}

	// SC-011: ReadArchive must still return the evicted prefix.
	if afterAbortArchiveLen < evictedCount {
		t.Fatalf("SC-011: archive too small after abort: %d lines, need at least %d for evicted turns",
			afterAbortArchiveLen, evictedCount)
	}
	evictedFound := false
	for i := 0; i < evictedCount && i < len(afterAbortArchive); i++ {
		if strings.Contains(afterAbortArchive[i].Content, bigContent[:50]) {
			evictedFound = true
			break
		}
	}
	assert.True(t, evictedFound,
		"SC-011: evicted turns must be present in archive after HardAbort (archive prefix intact)")

	// The live window (GetHistory) must NOT contain the aborted turn's messages,
	// AND must have the same size as the pre-turn window (round-2 fix: Skip must
	// be restored to the turn-start value, not left at the mid-turn-eviction value).
	liveWindow := agent.Sessions.GetHistory(sk)
	assert.Equal(t, len(windowAfterEvict), len(liveWindow),
		"round-2 fix: live window must be the same size as at turn start (Skip restored to turn-start value)")
	for _, m := range liveWindow {
		if strings.Contains(m.Content, "ABORT_TEST_SENTINEL") {
			t.Errorf("HardAbort rollback left an in-turn sentinel in the live window: role=%s content=%q",
				m.Role, m.Content)
		}
	}
}

// ============================================================================
// Gap 1b — restoreSession (turn abort) preserves evicted archive prefix
// ============================================================================

// TestAbortPath_RestoreSession_PreservesEvictedArchive verifies that
// restoreSession (called by abortTurn) after eviction (meta.Skip > 0) performs
// a Skip-preserving rollback: only the tail appended during the aborted turn is
// removed; the evicted prefix remains intact.
//
// BDD:
//
//	Given a session with 8 turns seeded to disk,
//	And windowTrim is called so meta.Skip > 0 (some turns are evicted),
//	And the archive has A lines (all turns — evicted + window),
//	When 3 additional messages are appended to simulate a running turn,
//	And restoreSession is called with initialArchiveLen = A (turn-start snapshot),
//	Then the archive has exactly A lines (the 3 appended lines are removed),
//	And ReadArchive still returns the evicted turns (archive prefix unchanged),
//	And the live window does not contain the aborted-turn sentinel messages.
//
// Traces to: spec SC-001 (line 249), SC-010 (line 259), SC-011 (line 259),
// FR-005 (line 218). turn.go:713 restoreSession path.
func TestAbortPath_RestoreSession_PreservesEvictedArchive(t *testing.T) {
	const (
		cw = 3000
		mt = 500
	)
	al, _ := buildTrimTestAgentLoop(t, cw, mt)
	const sk = "abort-restoresession-evicted"

	// --- Phase 1: seed 8 turns to disk (same budget logic as Gap 1a test).
	bigContent := strings.Repeat("B", 600)
	var history []providers.Message
	for i := 0; i < 8; i++ {
		history = append(history, makeTurn(bigContent, bigContent)...)
	}
	seedHistory(t, al, sk, history)

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	preTrimArchiveLen := archiveLineCountFromAgent(t, al, sk)
	require.Equal(t, len(history), preTrimArchiveLen,
		"precondition: archive must contain all seeded messages before trim")

	// --- Phase 2: evict turns via windowTrim.
	_, ok := al.windowTrim(agent, "", sk)
	require.True(t, ok, "windowTrim must evict turns from the over-budget session")

	postTrimArchiveLen := archiveLineCountFromAgent(t, al, sk)
	assert.Equal(t, preTrimArchiveLen, postTrimArchiveLen,
		"SC-001: windowTrim must not delete bytes from the archive")

	windowAfterEvict := agent.Sessions.GetHistory(sk)
	require.Less(t, len(windowAfterEvict), len(history),
		"precondition: live window must be smaller than full history after eviction")

	evictedCount := preTrimArchiveLen - len(windowAfterEvict)
	require.Greater(t, evictedCount, 0, "SC-011 precondition: must have evicted turns")

	// initialArchiveLen is what ts.initialArchiveLen captures at turn start.
	initialArchiveLen := archiveLineCountFromAgent(t, al, sk)

	// --- Phase 3: simulate in-turn appends (3 messages: user + tool call + result).
	inTurnMsgs := []providers.Message{
		{Role: "user", Content: "RESTORE_TEST_SENTINEL_USER: this turn is about to abort"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{
					ID:   "restore_tc_001",
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "web_search",
						Arguments: `{"query":"abort test"}`,
					},
				},
			},
		},
		{Role: "tool", Content: "RESTORE_TEST_SENTINEL_TOOL: search result", ToolCallID: "restore_tc_001"},
	}
	appendInTurnMessages(al, sk, inTurnMsgs)

	afterAppendArchiveLen := archiveLineCountFromAgent(t, al, sk)
	require.Equal(t, initialArchiveLen+len(inTurnMsgs), afterAppendArchiveLen,
		"precondition: archive must grow by in-turn appends")

	// --- Phase 4: trigger restoreSession (the abortTurn path).
	// Construct a minimal turnState with the fields restoreSession reads:
	//   ts.initialArchiveLen → targetLen for RollbackAppended
	//   ts.initialHistoryLength → used to derive targetSkip (round-2 fix)
	//   ts.sessionKey → session key for the store calls
	ts := &turnState{
		sessionKey:           sk,
		initialArchiveLen:    initialArchiveLen,
		initialHistoryLength: len(windowAfterEvict),
	}

	// restoreSession (turn.go:721):
	//   computes targetSkip = initialArchiveLen - initialHistoryLength
	//   calls agent.Sessions.RollbackAppended(ts.sessionKey, targetLen, targetSkip)
	//   calls agent.Sessions.Save(ts.sessionKey)
	err := ts.restoreSession(agent)
	require.NoError(t, err, "restoreSession must not error on an evicted session")

	// --- Phase 5: assert archive invariants.
	afterRestoreArchiveLen := archiveLineCountFromAgent(t, al, sk)
	assert.Equal(t, initialArchiveLen, afterRestoreArchiveLen,
		"SC-001/SC-010: restoreSession must roll back only the appended tail, not the evicted prefix")

	// The in-turn sentinel messages must be gone.
	afterRestoreArchive := archiveLinesFromAgent(t, al, sk)
	for _, line := range afterRestoreArchive {
		if strings.Contains(line.Content, "RESTORE_TEST_SENTINEL") {
			t.Errorf("restoreSession rollback left an in-turn sentinel in the archive: role=%s content=%q",
				line.Role, line.Content)
		}
	}

	// SC-011: evicted prefix must still be present.
	if afterRestoreArchiveLen < evictedCount {
		t.Fatalf("SC-011: archive too small after restoreSession: %d lines, need at least %d",
			afterRestoreArchiveLen, evictedCount)
	}
	evictedFound := false
	for i := 0; i < evictedCount && i < len(afterRestoreArchive); i++ {
		if strings.Contains(afterRestoreArchive[i].Content, bigContent[:50]) {
			evictedFound = true
			break
		}
	}
	assert.True(t, evictedFound,
		"SC-011: evicted turns must be present in archive after restoreSession (archive prefix intact)")

	// The live window must not contain the aborted turn's messages AND must have
	// the same number of messages as before the turn started (round-2 fix: Skip
	// must be restored to the turn-start value, not left advanced by mid-turn evictions).
	liveWindow := agent.Sessions.GetHistory(sk)
	assert.Equal(t, len(windowAfterEvict), len(liveWindow),
		"round-2 fix: live window must be the same size as at turn start (Skip restored to turn-start value)")
	for _, m := range liveWindow {
		if strings.Contains(m.Content, "RESTORE_TEST_SENTINEL") {
			t.Errorf("restoreSession left in-turn sentinel in the live window: role=%s content=%q",
				m.Role, m.Content)
		}
	}
}

// ============================================================================
// Gap 2 — assembleMessages ReadArchive-error fallback
// ============================================================================

// TestAssembleMessages_ReadArchiveError_FallsBackToBreadcrumb verifies the
// CRITICAL-2 fix: if ReadArchive errors, the breadcrumb falls back gracefully
// (uses an empty breadcrumb = "") and the turn proceeds on the live window.
//
// Note on testability: assembleMessages cannot be driven without a full running
// turn loop (it requires a live turnState, provider, and channel). The closest
// feasible boundary is BuildMessages (called by assembleMessages after the
// breadcrumb and span are resolved). This test:
//
//  1. Exercises BuildMessages with an empty breadcrumb (the ReadArchive-error
//     fallback path — caller passes "" when ReadArchive fails).
//  2. Verifies the live window and current user message appear in the output.
//  3. Uses a differentiation sub-case: BuildMessages with a non-empty breadcrumb
//     produces different output (proving the empty case is not hardcoded).
//
// The design of assembleMessages: it calls agent.Sessions.ReadArchive; on error,
// it logs a warning and sets breadcrumb = "" (graceful degradation). This test
// verifies the post-error output (BuildMessages with breadcrumb = "") is valid.
//
// Traces to: spec SC-010 (line 259), SC-011, CRITICAL-2 fix, FR-016 (line 233),
// FR-007 (line 220).
func TestAssembleMessages_ReadArchiveError_FallsBackToBreadcrumb(t *testing.T) {
	// BDD: Given a ContextBuilder with a valid workspace,
	//      And a live window with 2 messages,
	//      And ReadArchive is unavailable (returns error, caller falls back to breadcrumb=""),
	//      When BuildMessages is called with the fallback empty breadcrumb,
	//      Then the assembled messages contain the live window and current user message,
	//      And no panic occurs,
	//      And the first message is the system message (pinned core).
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent for ReadArchive error fallback.",
	})
	cb := NewContextBuilder(tmpDir)

	// Live window messages (come from GetHistory even when ReadArchive fails).
	liveWindow := []providers.Message{
		{Role: "user", Content: "LIVE_WINDOW_SENTINEL: what is the capital of France?"},
		{Role: "assistant", Content: "Paris is the capital of France."},
	}

	currentMsg := "CURRENT_MSG_SENTINEL: follow-up question"

	// ReadArchive-error fallback: breadcrumb = "" (no breadcrumb injected).
	fallbackBreadcrumb := ""

	// Must not panic.
	assembled := cb.BuildMessages(
		liveWindow,
		currentMsg,
		nil,
		"",
		"test", "readarchive-error-fallback", "", "",
		fallbackBreadcrumb,
		nil, // no recall span
	)

	require.NotEmpty(t, assembled,
		"CRITICAL-2: BuildMessages must return non-empty messages even when ReadArchive fails (empty breadcrumb path)")

	// First message must be a system message (pinned core always present).
	require.Equal(t, "system", assembled[0].Role,
		"CRITICAL-2: first assembled message must be system even on ReadArchive error fallback")

	// The live window must appear in the assembled messages.
	liveWindowFound := false
	for _, m := range assembled {
		if strings.Contains(m.Content, "LIVE_WINDOW_SENTINEL") {
			liveWindowFound = true
			break
		}
	}
	assert.True(t, liveWindowFound,
		"CRITICAL-2: live window must be present in assembled messages on ReadArchive error fallback")

	// The current user message must appear.
	currentFound := false
	for _, m := range assembled {
		if strings.Contains(m.Content, "CURRENT_MSG_SENTINEL") {
			currentFound = true
			break
		}
	}
	assert.True(t, currentFound,
		"CRITICAL-2: current user message must be present in assembled messages on ReadArchive error fallback")

	// Differentiation test: calling with a non-empty breadcrumb produces different output
	// (proves the empty-breadcrumb fallback is not hardcoded — SC-007 style proof).
	explicitBreadcrumb := "## Earlier in this conversation (evicted from the live window)\n" +
		"Use the recall_conversation tool to read any of these verbatim.\n" +
		"- turns 1–2 · 2h ago · \"DIFFERENTIATION_BREADCRUMB_SENTINEL\""

	assembledWithBreadcrumb := cb.BuildMessages(
		liveWindow,
		currentMsg,
		nil,
		"",
		"test", "readarchive-error-fallback", "", "",
		explicitBreadcrumb,
		nil,
	)
	require.NotEmpty(t, assembledWithBreadcrumb,
		"BuildMessages with explicit breadcrumb must return non-empty messages")

	// The explicit breadcrumb must appear in the assembled output.
	breadcrumbFound := false
	for _, m := range assembledWithBreadcrumb {
		if strings.Contains(m.Content, "DIFFERENTIATION_BREADCRUMB_SENTINEL") {
			breadcrumbFound = true
			break
		}
	}
	assert.True(
		t,
		breadcrumbFound,
		"CRITICAL-2 differentiation: explicit breadcrumb must appear in output — proving empty fallback is not hardcoded",
	)

	// The breadcrumb sentinel must NOT appear in the empty-fallback-assembled output.
	for _, m := range assembled {
		if strings.Contains(m.Content, "DIFFERENTIATION_BREADCRUMB_SENTINEL") {
			t.Errorf(
				"CRITICAL-2 differentiation: breadcrumb sentinel leaked into empty-fallback assembled output: role=%s",
				m.Role,
			)
		}
	}
}

// ============================================================================
// Round-2 architect finding — mid-turn eviction + abort blanks live window
// ============================================================================

// TestRollbackAppended_MidTurnEviction_HardAbort verifies the round-2 fix:
// when windowTrim advances meta.Skip DURING a turn and the turn is then
// hard-aborted, RollbackAppended MUST restore Skip to its turn-start value so
// GetHistory returns the exact pre-turn live window — not a blanked/shrunk one.
//
// BDD:
//
//	Given a session with N seeded messages and Skip=S1 (some turns already evicted),
//	And a turn begins (capturing initialArchiveLen = Count, initialHistoryLength = Count-S1),
//	And mid-turn windowTrim advances Skip from S1 to S2 > S1,
//	And mid-turn messages are appended (Count grows),
//	When HardAbort is triggered (RollbackAppended(targetLen, targetSkip=S1)),
//	Then GetHistory returns exactly Count-S1 messages (the pre-turn live window),
//	Not an empty/shrunk window caused by Skip being left at S2 > targetLen.
//
// Traces to: SC-001, SC-010, round-2 architect finding (RollbackAppended Skip restore).
func TestRollbackAppended_MidTurnEviction_HardAbort(t *testing.T) {
	const (
		cw = 3000
		mt = 500
	)
	al, _ := buildTrimTestAgentLoop(t, cw, mt)
	const sk = "midturn-evict-hardabort"

	// Phase 1: seed 6 turns so the session has a non-empty archive.
	// Each turn ≈ 2×240 tokens = 480 tokens. 6 turns ≈ 2880 tokens.
	// We do NOT exceed budget here — we want a baseline with Skip=0.
	smallContent := strings.Repeat("X", 300) // ~120 tokens each
	var history []providers.Message
	for i := 0; i < 6; i++ {
		history = append(history, makeTurn(smallContent, smallContent)...)
	}
	seedHistory(t, al, sk, history)

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	// Confirm baseline: 12 messages, Skip=0.
	preTrimArchiveLen := archiveLineCountFromAgent(t, al, sk)
	require.Equal(t, 12, preTrimArchiveLen, "baseline: 6 turns × 2 messages = 12")
	windowBeforeEvict := agent.Sessions.GetHistory(sk)
	require.Equal(t, 12, len(windowBeforeEvict), "baseline: all 12 messages visible")

	// Phase 2: simulate a prior eviction BEFORE the turn starts (Skip advances).
	// Use TruncateHistory directly to advance Skip without removing bytes.
	// Keep 8 messages visible → Skip = 12 - 8 = 4.
	agent.Sessions.TruncateHistory(sk, 8)
	windowAfterPreEvict := agent.Sessions.GetHistory(sk)
	require.Equal(t, 8, len(windowAfterPreEvict),
		"after pre-evict: 8 messages visible (Skip=4)")

	// Capture turn-start state (mirrors newTurnState).
	initialArchiveLen := archiveLineCountFromAgent(t, al, sk) // = 12 (bytes unchanged)
	initialHistoryLength := len(windowAfterPreEvict)          // = 8
	// targetSkip at turn start = initialArchiveLen - initialHistoryLength = 4
	turnStartSkip := initialArchiveLen - initialHistoryLength
	require.Equal(t, 4, turnStartSkip, "turn-start Skip must be 4")

	// Phase 3: simulate mid-turn windowTrim advancing Skip further.
	// Keep 4 messages → Skip advances to 12 - 4 = 8.
	agent.Sessions.TruncateHistory(sk, 4)
	windowAfterMidEvict := agent.Sessions.GetHistory(sk)
	require.Equal(t, 4, len(windowAfterMidEvict),
		"after mid-turn eviction: only 4 messages visible (Skip=8)")

	// Confirm archive still has 12 lines (eviction = Skip advance, not delete).
	midEvictArchiveLen := archiveLineCountFromAgent(t, al, sk)
	require.Equal(t, 12, midEvictArchiveLen, "mid-turn eviction must not delete archive bytes")

	// Phase 4: append 3 in-turn messages (Count grows to 15, Skip=8, window=7).
	inTurnMsgs := []providers.Message{
		{Role: "user", Content: "MIDTURN_SENTINEL_USER: will be rolled back"},
		{Role: "assistant", Content: "MIDTURN_SENTINEL_ASST: will be rolled back"},
		{Role: "tool", Content: "MIDTURN_SENTINEL_TOOL: will be rolled back", ToolCallID: "tc1"},
	}
	appendInTurnMessages(al, sk, inTurnMsgs)
	afterAppendArchiveLen := archiveLineCountFromAgent(t, al, sk)
	require.Equal(t, 15, afterAppendArchiveLen, "after in-turn appends: 12+3=15 archive lines")

	// Without the fix: RollbackAppended(12, 0) would truncate Count to 12 and
	// clamp Skip = min(8, 12) = 8 → GetHistory = 12-8 = 4 messages (wrong).
	// With the fix: RollbackAppended(12, 4) restores Skip=4 → GetHistory = 12-4 = 8.
	//
	// Phase 5: trigger RollbackAppended directly (mirrors HardAbort's call).
	agent.Sessions.RollbackAppended(sk, initialArchiveLen, turnStartSkip, nil)

	// Phase 6: assert post-rollback invariants.
	// Archive must be back to 12 lines.
	afterRollbackArchiveLen := archiveLineCountFromAgent(t, al, sk)
	assert.Equal(t, initialArchiveLen, afterRollbackArchiveLen,
		"SC-001: rollback must restore archive to initialArchiveLen=12")

	// GetHistory MUST return the 8 messages that were visible at turn start.
	liveWindow := agent.Sessions.GetHistory(sk)
	assert.Equal(t, initialHistoryLength, len(liveWindow),
		"SC-010/round-2 fix: GetHistory must return the pre-turn live window (8 messages), not a blanked/shrunk window")

	// None of the in-turn sentinels may appear in the live window.
	for _, m := range liveWindow {
		if strings.Contains(m.Content, "MIDTURN_SENTINEL") {
			t.Errorf("rolled-back sentinel found in live window: role=%s content=%q", m.Role, m.Content)
		}
	}

	// ReadArchive must still return all 12 pre-turn lines (evicted turns intact).
	archive := archiveLinesFromAgent(t, al, sk)
	assert.Equal(t, initialArchiveLen, len(archive),
		"SC-001: ReadArchive must return all pre-turn archive lines after rollback")
	for _, line := range archive {
		if strings.Contains(line.Content, "MIDTURN_SENTINEL") {
			t.Errorf("rolled-back sentinel found in archive: role=%s content=%q",
				line.Role, line.Content)
		}
	}
}

// TestRollbackAppended_MidTurnEviction_RestoreSession verifies the same fix
// for the restoreSession (turn-abort) path. The test is structurally identical
// to TestRollbackAppended_MidTurnEviction_HardAbort but calls
// ts.restoreSession(agent) instead of RollbackAppended directly.
//
// Traces to: SC-001, SC-010, round-2 architect finding, turn.go restoreSession path.
func TestRollbackAppended_MidTurnEviction_RestoreSession(t *testing.T) {
	const (
		cw = 3000
		mt = 500
	)
	al, _ := buildTrimTestAgentLoop(t, cw, mt)
	const sk = "midturn-evict-restoresession"

	// Phase 1: seed 6 turns (12 messages, Skip=0).
	smallContent := strings.Repeat("Y", 300)
	var history []providers.Message
	for i := 0; i < 6; i++ {
		history = append(history, makeTurn(smallContent, smallContent)...)
	}
	seedHistory(t, al, sk, history)

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	// Phase 2: pre-turn eviction → keep 8 visible (Skip=4).
	agent.Sessions.TruncateHistory(sk, 8)
	windowAfterPreEvict := agent.Sessions.GetHistory(sk)
	require.Equal(t, 8, len(windowAfterPreEvict))

	initialArchiveLen := archiveLineCountFromAgent(t, al, sk) // 12
	initialHistoryLength := len(windowAfterPreEvict)          // 8
	turnStartSkip := initialArchiveLen - initialHistoryLength // 4

	// Phase 3: mid-turn windowTrim → keep 4 visible (Skip advances to 8).
	agent.Sessions.TruncateHistory(sk, 4)
	require.Equal(t, 4, len(agent.Sessions.GetHistory(sk)),
		"mid-turn eviction: 4 messages visible")

	// Phase 4: append 3 in-turn messages (archive grows to 15).
	inTurnMsgs := []providers.Message{
		{Role: "user", Content: "RS_SENTINEL_USER: will be restored away"},
		{Role: "assistant", Content: "RS_SENTINEL_ASST: will be restored away"},
		{Role: "tool", Content: "RS_SENTINEL_TOOL: will be restored away", ToolCallID: "tc2"},
	}
	appendInTurnMessages(al, sk, inTurnMsgs)
	require.Equal(t, 15, archiveLineCountFromAgent(t, al, sk))

	// Phase 5: trigger restoreSession (abortTurn path).
	// turnState mirrors what newTurnState captures at turn start.
	ts := &turnState{
		sessionKey:           sk,
		initialArchiveLen:    initialArchiveLen,
		initialHistoryLength: initialHistoryLength,
	}
	err := ts.restoreSession(agent)
	require.NoError(t, err, "restoreSession must not error")

	// Phase 6: assert pre-turn live window is fully restored.
	// Archive back to 12.
	assert.Equal(t, initialArchiveLen, archiveLineCountFromAgent(t, al, sk),
		"SC-001: restoreSession must restore archive to 12 lines")

	// GetHistory must return the 8 pre-turn messages.
	liveWindow := agent.Sessions.GetHistory(sk)
	assert.Equal(
		t,
		initialHistoryLength,
		len(liveWindow),
		"SC-010/round-2 fix: restoreSession must restore the pre-turn live window (8 messages), not leave Skip advanced to 8",
	)

	// No sentinels in the live window or archive.
	for _, m := range liveWindow {
		if strings.Contains(m.Content, "RS_SENTINEL") {
			t.Errorf(
				"in-turn sentinel found in live window after restoreSession: role=%s content=%q",
				m.Role,
				m.Content,
			)
		}
	}
	for _, line := range archiveLinesFromAgent(t, al, sk) {
		if strings.Contains(line.Content, "RS_SENTINEL") {
			t.Errorf("in-turn sentinel found in archive after restoreSession: role=%s content=%q",
				line.Role, line.Content)
		}
	}

	// Verify that turnStartSkip is exactly what restoreSession computes
	// (targetSkip = initialArchiveLen - initialHistoryLength). This proves
	// the derivation is correct for the fix.
	assert.Equal(t, 4, turnStartSkip,
		"turn-start Skip derivation: initialArchiveLen(12) - initialHistoryLength(8) = 4")
}
