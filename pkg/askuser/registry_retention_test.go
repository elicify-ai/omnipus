// Omnipus — #1329 round-2 retention/question-queue regression
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package askuser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRetentionSweepForgetsPendingQueueAndTimers traces to security F1 in
// pq2-sec-full-report.md and the 2026-10-17 team-lead dispatch. It composes the
// REAL store, observer, registry and timers. Only outbound resume/card delivery
// is recorded. No direct ForgetDeletedSession call can stand in for retention.
// GREEN, mutation probes and the proof-of-failability gate are deferred to CHECK.
func TestRetentionSweepForgetsPendingQueueAndTimers(t *testing.T) {
	store := newQueueStore(t)
	expired := newOwnerSession(t, store)
	survivor := newOwnerSession(t, store)
	probeSession := newOwnerSession(t, store)
	resume, sink := &fakeResume{}, &fakeSink{}
	// Three is the exact fixture population: expired A + queued B + survivor.
	// An hour-long physical delay prevents expiry from impersonating cleanup.
	reg := NewRegistry(store, resume, Options{MaxPending: 3, DefaultSafeDelay: time.Hour, Sink: sink})
	store.SetSessionDeletedObserver(reg.ForgetDeletedSession)
	const headText = "EXPIRED-HEAD-QUESTION-1329"
	const headContext = "EXPIRED-HEAD-CONTEXT-1329"
	const queuedText = "EXPIRED-QUEUED-QUESTION-1329"
	const queuedContext = "EXPIRED-QUEUED-CONTEXT-1329"
	head := testSet(expired, retentionDefaultSafeQuestion(headText, headContext))
	queued := testSet(expired, retentionDefaultSafeQuestion(queuedText, queuedContext))
	keep := testSet(survivor, retentionDefaultSafeQuestion("Retained question?", "Retained context"))
	ownedTimers := make([]*time.Timer, 0, 2)
	// Register before admission: even an expected RED or failed precondition
	// must stop every long timer BEFORE Quiesce. Saved handles also rescue a
	// faulty implementation that drops its timer map without stopping timers.
	t.Cleanup(func() {
		for _, timer := range ownedTimers {
			if timer.Stop() {
				reg.timerWG.Done() // teardown only: balance the slot for the stopped callback.
			}
		}
		for _, card := range []*PendingSet{head, queued, keep} {
			reg.stopTimers(card.CardID)
		}
		reg.Quiesce()
	})
	for _, card := range []*PendingSet{head, queued, keep} {
		require.NoError(t, reg.CreatePending(card))
	}
	headTimer := retentionCaptureTimer(t, reg, head.CardID)
	keepTimer := retentionCaptureTimer(t, reg, keep.CardID)
	ownedTimers = append(ownedTimers, headTimer, keepTimer)
	require.Equal(t, 3, reg.PendingCount(), "instrument: head, queue and survivor must fill the cap")
	reg.mu.Lock()
	queuedIDs := make([]string, 0, len(reg.queued[expired]))
	for _, card := range reg.queued[expired] {
		queuedIDs = append(queuedIDs, card.CardID)
	}
	queuedTimerCount := len(reg.timers[queued.CardID])
	reg.mu.Unlock()
	require.Equal(t, []string{queued.CardID}, queuedIDs, "instrument: B must really be in the FIFO")
	require.Zero(t, queuedTimerCount, "B's clock must not start before promotion")
	before, err := json.Marshal(reg.PendingAll())
	require.NoError(t, err)
	require.Contains(t, string(before), headText, "instrument: PendingAll can expose the expired head before retention")
	require.Contains(t, string(before), headContext)
	require.NotContains(t, string(before), queuedText, "queued B must not already be public")
	probe := testSet(probeSession)
	require.ErrorIs(t, reg.CreatePending(probe), ErrSaturated, "instrument: global capacity must really be exhausted")

	// Change only the real transcript mtime. Pending questions do not exempt
	// an ordinary chat from retention (security F1). 30 days is clearly older
	// than the chosen 7-day window, not an exact-cutoff timing assertion.
	dir := filepath.Join(store.BaseDir(), expired)
	old := time.Now().Add(-30 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "transcript.jsonl"), old, old))
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed, "instrument: exactly the expired transcript must be swept")
	_, err = os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist, "instrument: prove REAL directory deletion before any memory oracle")

	assert.Equal(t, 1, reg.PendingCount(), "retention must free both expired A and queued B; only the survivor remains")
	for _, card := range []*PendingSet{head, queued} {
		_, visible := reg.Get(card.CardID)
		assert.False(t, visible, "expired card %s must not remain reachable", card.CardID)
	}
	_, visible := reg.PendingForSession(expired)
	assert.False(t, visible, "retention must remove the expired session's active lookup")
	reg.mu.Lock()
	assert.NotContains(t, reg.queued, expired, "the WHOLE FIFO must be forgotten, not just its current head")
	assert.NotContains(t, reg.bySession, expired)
	assert.NotContains(t, reg.byRouting, head.RoutingSessionKey)
	assert.NotContains(t, reg.timers, head.CardID, "expired head timer must be dropped")
	assert.NotContains(t, reg.timers, queued.CardID, "expired queue must retain no timer")
	assert.Equal(t, []*time.Timer{keepTimer}, reg.timers[keep.CardID], "another session's timer must be untouched")
	reg.mu.Unlock()

	all := reg.PendingAll()
	ids := make([]string, 0, len(all))
	for _, card := range all {
		ids = append(ids, card.CardID)
	}
	assert.Equal(t, []string{keep.CardID}, ids, "reconnect snapshots must expose ONLY the retained session")
	snapshot, err := json.Marshal(all)
	require.NoError(t, err)
	for _, forbidden := range []string{expired, headText, headContext, queuedText, queuedContext} {
		assert.NotContains(t, string(snapshot), forbidden, "PendingAll must never serve expired question text/context")
	}
	assert.Equal(t, 2, sink.count(), "retention cleanup must emit no terminal card or promoted B")
	assert.Zero(t, resume.count(), "retention cleanup must dispatch no resume")
	assert.NoError(t, reg.CreatePending(probe), "both deleted cards must release their global capacity")

	// Probe the SAVED timer handles, not only the timer map: deleting a map
	// entry without stopping its real callback must also fail. Stop consumes
	// any still-live timer, so balance that callback's WaitGroup slot solely
	// for teardown; the observed result remains an explicit failure below.
	retentionAssertTimerStopped(t, reg, headTimer, true, "expired head")
	retentionAssertTimerStopped(t, reg, keepTimer, false, "unrelated survivor")
}

func retentionDefaultSafeQuestion(text, context string) Question {
	return Question{
		Header: "Scope", Question: text, Context: context,
		Options:     []Option{{Label: "Backend"}, {Label: "Full stack"}},
		Recommended: "Backend", DefaultSafe: true,
	}
}

func retentionCaptureTimer(t *testing.T, reg *Registry, cardID string) *time.Timer {
	t.Helper()
	reg.mu.Lock()
	timers := append([]*time.Timer(nil), reg.timers[cardID]...)
	reg.mu.Unlock()
	require.Len(t, timers, 1, "instrument: %s must own one REAL scheduled timer before retention", cardID)
	return timers[0]
}

func retentionAssertTimerStopped(t *testing.T, reg *Registry, timer *time.Timer, wantStopped bool, owner string) {
	t.Helper()
	wasLive := timer.Stop()
	if wasLive {
		reg.timerWG.Done() // teardown only; never changes the observed assertion value.
	}
	assert.Equal(t, wantStopped, !wasLive, "%s timer stopped state must reflect retention cleanup, not a dropped map entry", owner)
}
