// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session_end_fix_test.go — regression coverage for FIX-7's defects 1, 3 and
// 4 (ADR-057 unconsidered riders in the session-end recap/cleanup pipeline).
//
// Binding rules (per the fix-wave brief): every assertion below lands on
// REAL store-backed state (a real *session.UnifiedStore rooted at a fresh
// t.TempDir(), a real *AgentLoop, a real registered AgentInstance) — never a
// spy that merely records its argument. Every negative/zero-count assertion
// is paired with a positive lower bound proving the mechanism under test is
// genuinely exercised, not vacuously satisfied by a broken fixture.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestCloseSession' -p 1 ./pkg/agent/
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestCloseSession_SkipsRecapForDelegatedChild_ButRunsForRealChatSession is
// the Defect 1 + Defect 3 red/green pair.
//
// Defect 1 (MAJOR — unbilled spend + memory corruption): ADR-057 added
// al.CloseSession(childID, "delegate_terminal") to subturn.go's cleanup
// defer with no session-type gate. Since runRecap has no gate of its own
// either, and AutoRecapEnabled is seeded true on a fresh install, every
// delegated child fired a real LLM call (scriptedProvider.Chat here stands
// in for it) that bills spend no session's Stats ever sees and can
// overwrite the owning agent's ONE last-session.md with a stale micro-task
// recap.
//
// Defect 3 (MAJOR — unbounded leak): the SAME unconditional CloseSession
// call also inserted the child's single-use session id into
// al.claimedCloseSessions (a sync.Map with no eviction anywhere in the
// tree) — one permanent entry per ever-delegated child for the process
// lifetime.
//
// Part A proves BOTH regressions are closed for a delegated child. Part B
// is the mandatory positive lower bound (per the fix-wave brief): a REAL
// chat session closing must still recap exactly as before — without it,
// this test would pass just as well if recap were broken outright.
func TestCloseSession_SkipsRecapForDelegatedChild_ButRunsForRealChatSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = "claude-haiku-3"

	const recapSentinel = "defect1 recap sentinel value QRS"
	script := &scriptedProvider{
		responseBody: `{"recap":"` + recapSentinel + `","went_well":["fix"],"needs_improvement":[],"worth_remembering":[]}`,
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, script)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	const agentID = "defect1-agent"
	agentCfg := &config.AgentConfig{ID: agentID, Name: "Defect1Agent"}
	defaults := &cfg.Agents.Defaults
	ag := NewAgentInstance(agentCfg, defaults, cfg, script)
	if ag == nil {
		t.Fatal("NewAgentInstance returned nil")
	}
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, "Defect1Agent")
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	lastSessionPath := filepath.Join(ag.Home, ".omnipus", "last-session.md")

	// ---------------------------------------------------------------
	// Part A: a DELEGATED CHILD session must NOT trigger a real recap.
	// ---------------------------------------------------------------
	delegateMeta, err := store.NewSession(session.SessionTypeDelegate, "delegate", agentID)
	if err != nil {
		t.Fatalf("NewSession(delegate): %v", err)
	}
	delegateSessionID := delegateMeta.ID

	// RED-baseline sanity: prove this is a real, non-empty delegate session
	// with a real transcript — the exact shape subturn.go produces (one
	// synthetic user turn, the task prompt) — so a skip cannot be explained
	// away as "there was nothing to recap anyway".
	if err := store.AppendTranscript(delegateSessionID, session.TranscriptEntry{
		Role:      "user",
		Content:   "[SubTurn Task] summarize the repository README",
		Timestamp: time.Now().UTC(),
		AgentID:   agentID,
	}); err != nil {
		t.Fatalf("AppendTranscript(delegate): %v", err)
	}
	preMeta, err := store.GetMeta(delegateSessionID)
	if err != nil {
		t.Fatalf("GetMeta(delegate) pre-close: %v", err)
	}
	if preMeta.Type != session.SessionTypeDelegate {
		t.Fatalf("fixture broken: session type = %q, want %q — the delegate-vs-chat distinction this test needs is not actually in place",
			preMeta.Type, session.SessionTypeDelegate)
	}

	// This is subturn.go's own real call site and trigger label
	// ("delegate_terminal"), reproduced verbatim.
	al.CloseSession(delegateSessionID, "delegate_terminal")

	// Give any (buggy) async recap goroutine a real wall-clock window to
	// fire before asserting it did not — the scriptedProvider responds
	// in-memory, so a genuine recap would complete well within this window.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		script.mu.Lock()
		calls := script.callCount
		script.mu.Unlock()
		if calls > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	script.mu.Lock()
	callsAfterDelegate := script.callCount
	script.mu.Unlock()
	if callsAfterDelegate != 0 {
		t.Fatalf("Defect 1 violated: closing a delegated child session made %d real recap LLM call(s), want 0 "+
			"— unbilled spend invisible to any session's Stats, and a race to overwrite the agent's shared last-session.md",
			callsAfterDelegate)
	}
	if _, statErr := os.Stat(lastSessionPath); statErr == nil {
		t.Fatal("Defect 1 violated: last-session.md was written after closing a delegated child session — recap ran for a non-chat session")
	}

	// Defect 3: the delegated child's session id must never even be inserted
	// into claimedCloseSessions — that map has no eviction anywhere in the
	// tree, so every entry recorded there is permanent for the process
	// lifetime.
	if _, ok := al.claimedCloseSessions.Load(delegateSessionID); ok {
		t.Error("Defect 3 violated: claimedCloseSessions contains the delegated child's session id after CloseSession " +
			"— this leaks one permanent entry per ever-delegated child")
	}

	// -----------------------------------------------------------------
	// Part B (mandatory positive lower bound): a REAL chat session
	// closing must still trigger the real recap pipeline, unchanged.
	// -----------------------------------------------------------------
	chatMeta, err := store.NewSession(session.SessionTypeChat, "web", agentID)
	if err != nil {
		t.Fatalf("NewSession(chat): %v", err)
	}
	chatSessionID := chatMeta.ID
	if err := store.AppendTranscript(chatSessionID, session.TranscriptEntry{
		Role:      "user",
		Content:   "please summarize what we did today",
		Timestamp: time.Now().UTC(),
		AgentID:   agentID,
	}); err != nil {
		t.Fatalf("AppendTranscript(chat): %v", err)
	}

	al.CloseSession(chatSessionID, "explicit")

	waitDeadline := time.Now().Add(5 * time.Second)
	var lastSessionBytes []byte
	for time.Now().Before(waitDeadline) {
		data, readErr := os.ReadFile(lastSessionPath)
		if readErr == nil {
			lastSessionBytes = data
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastSessionBytes == nil {
		script.mu.Lock()
		calls := script.callCount
		script.mu.Unlock()
		t.Fatalf("positive lower bound failed: closing a REAL chat session did not produce last-session.md within deadline "+
			"(recap LLM calls so far=%d) — if this fails, the fix broke recap outright instead of scoping it to delegate sessions",
			calls)
	}
	if !strings.Contains(string(lastSessionBytes), recapSentinel) {
		t.Errorf("last-session.md missing expected recap sentinel; got:\n%s", lastSessionBytes)
	}

	script.mu.Lock()
	finalCalls := script.callCount
	script.mu.Unlock()
	if finalCalls != 1 {
		t.Errorf("expected exactly 1 real recap LLM call (from the real chat session close), got %d", finalCalls)
	}

	// Regression guard: the pre-existing "one entry per real chat session"
	// claimedCloseSessions growth (explicitly out of scope for Defect 3 —
	// called "bounded in practice" in the fix-wave brief) must be completely
	// unaffected by the delegate-only gate above.
	if _, ok := al.claimedCloseSessions.Load(chatSessionID); !ok {
		t.Error("regression: a real chat session's id is no longer recorded in claimedCloseSessions " +
			"— the delegate-only gate must not change behavior for real chat sessions")
	}
}

// TestCloseSession_FlushFailure_DoesNotStrandRetryByEvictingCache is the
// Defect 4 red/green pair.
//
// Defect 4 (MAJOR): CloseSession's FlushSessionStats + EvictSessionMeta are
// two SEPARATE shard acquisitions. On a flush failure (a real disk write
// error here, not a mock), u6FlushDirtySessionLocked deliberately keeps the
// dirty mark for the next retry — but the OLD code called EvictSessionMeta
// unconditionally, deleting the one cache entry that retry could ever read.
// Every subsequent flush attempt then found the dirty mark set but no cached
// meta to flush, and silently no-op'd forever: the pending delta was lost
// and could never be recovered, even once the underlying disk error cleared.
//
// This test reproduces a REAL disk write failure (a read-only session
// directory — no mock/spy) deterministically, no race required, matching
// the fix-wave brief's "deterministic path (no race needed)" framing.
func TestCloseSession_FlushFailure_DoesNotStrandRetryByEvictingCache(t *testing.T) {
	al, store := u17bNewTeardownLoop(t)

	meta, err := store.NewSession(session.SessionTypeChat, "test", "test-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sessionID := meta.ID

	if err := store.AppendTranscript(sessionID, session.TranscriptEntry{
		Role:    "user",
		Content: "hello",
		Tokens:  42,
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	// --- RED baseline: prove the dirty delta is real before injecting any failure. ---
	preClose, err := store.GetMeta(sessionID)
	if err != nil {
		t.Fatalf("GetMeta (pre-close): %v", err)
	}
	if preClose.Stats.MessageCount != 1 {
		t.Fatalf("positive lower bound failed: want cached MessageCount == 1 after one AppendTranscript, got %d — fixture is broken",
			preClose.Stats.MessageCount)
	}

	// Inject a REAL disk write failure: strip write permission from the
	// session's own directory so u5WriteStatsLocked's WithFlock(statsPath, ...)
	// (which os.OpenFile(O_CREATE)s stats.json for the very first time here)
	// fails with a permission error — no mock, a genuine fileutil/os failure.
	sessionDir := filepath.Join(store.BaseDir(), sessionID)
	if err := os.Chmod(sessionDir, 0o500); err != nil {
		t.Fatalf("chmod session dir read-only: %v", err)
	}
	// Restore write permission unconditionally so t.TempDir()'s cleanup can
	// remove the directory regardless of how this test exits.
	t.Cleanup(func() { _ = os.Chmod(sessionDir, 0o700) })

	// --- Act: the teardown under test, with the flush forced to fail. ---
	al.CloseSession(sessionID, "test-trigger")

	// Confirm the injected failure genuinely blocked the write — otherwise
	// the rest of this test would be meaningless (RED precondition).
	if _, existed := u17bReadStatsMessageCount(t, store, sessionID); existed {
		t.Fatal("fixture broken: stats.json exists despite a read-only session directory — the injected disk failure did not take effect")
	}

	// Restore write permission and retry the flush — exactly what the next
	// periodic flusher tick or forced-flush point would do.
	if err := os.Chmod(sessionDir, 0o700); err != nil {
		t.Fatalf("chmod session dir writable: %v", err)
	}
	if err := store.FlushSessionStats(sessionID); err != nil {
		t.Fatalf("retry flush after restoring permissions failed: %v", err)
	}

	// --- GREEN: the retry must have found the cache entry and the dirty
	// mark still intact, and produced the correct stats.json. ---
	count, existed := u17bReadStatsMessageCount(t, store, sessionID)
	if !existed {
		t.Fatal("Defect 4 violated: CloseSession's flush-then-evict evicted the cache entry even though the flush failed, " +
			"so the retry above had nothing to read from and silently wrote nothing")
	}
	if count != 1 {
		t.Fatalf("retry flush produced wrong message_count = %d, want 1 — the dirty delta was corrupted, not just delayed", count)
	}
}
