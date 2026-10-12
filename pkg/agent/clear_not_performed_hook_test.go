// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// S3-02 server half (ARCHITECT-RULINGS): a /clear that is refused or not
// performed is reported to the gateway as a terminal fact about the person's
// /clear row, so the SPA needs neither the reply text nor a timer. ORACLE: the
// ruling - "any path: busy-turn failure, helper-session refusal, discard"
// reports failed for the /clear's own row; a clear that happens reports nothing.

type clearOutcomeRecorder struct {
	mu   sync.Mutex
	seen [][2]string
}

func (r *clearOutcomeRecorder) hook(sessionID, entryID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, [2]string{sessionID, entryID})
}

func (r *clearOutcomeRecorder) snapshot() [][2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][2]string(nil), r.seen...)
}

func TestClearNotPerformed_UnsettledStopReportsTheClearRow(t *testing.T) {
	old := clearStopSettleTimeout
	clearStopSettleTimeout = 300 * time.Millisecond
	t.Cleanup(func() { clearStopSettleTimeout = old })
	prov := newStuckProvider()
	f := newBusyClearFixture(t, prov)
	t.Cleanup(func() { close(prov.release) })
	rec := &clearOutcomeRecorder{}
	f.al.SetClearNotPerformedHook(rec.hook)

	require.True(t, f.w.enqueue(f.human("first question")))
	<-prov.started
	require.True(t, f.w.enqueue(f.persistClearRow(t)))

	waitUntilClear(t, "the not-performed report", 10*time.Second, func() bool { return len(rec.snapshot()) == 1 })
	require.Equal(t, [][2]string{{f.sid, "clear-row-1"}}, rec.snapshot())
}

func TestClearNotPerformed_UnauthenticatedSenderReportsTheClearRow(t *testing.T) {
	prov := newStoppableProvider()
	f := newBusyClearFixture(t, prov)
	rec := &clearOutcomeRecorder{}
	f.al.SetClearNotPerformedHook(rec.hook)

	require.True(t, f.w.enqueue(f.human("first question")))
	<-prov.started
	m := f.persistClearRow(t)
	m.Sender.CanonicalID = ""
	require.True(t, f.w.enqueue(m))

	waitUntilClear(t, "the not-performed report", 10*time.Second, func() bool { return len(rec.snapshot()) == 1 })
	require.Equal(t, [][2]string{{f.sid, "clear-row-1"}}, rec.snapshot())
}

func TestClearNotPerformed_HelperSessionRefusalReportsTheClearRow(t *testing.T) {
	prov := newStoppableProvider()
	f := newBusyClearFixture(t, prov)
	rec := &clearOutcomeRecorder{}
	f.al.SetClearNotPerformedHook(rec.hook)
	parent := "some-parent-session"
	require.NoError(t, f.store.SetMeta(f.sid, session.MetaPatch{ParentSessionID: &parent}))

	require.True(t, f.w.enqueue(f.persistClearRow(t)))

	waitUntilClear(t, "the not-performed report", 15*time.Second, func() bool { return len(rec.snapshot()) == 1 })
	require.Equal(t, [][2]string{{f.sid, "clear-row-1"}}, rec.snapshot())
	require.Zero(t, f.markerCount(t), "instrument check: a refused clear appends no marker")
}

func TestClearNotPerformed_ASuccessfulClearReportsNothing(t *testing.T) {
	prov := newStoppableProvider()
	f := newBusyClearFixture(t, prov)
	rec := &clearOutcomeRecorder{}
	f.al.SetClearNotPerformedHook(rec.hook)

	require.True(t, f.w.enqueue(f.persistClearRow(t)))
	waitUntilClear(t, "the /clear marker", 15*time.Second, func() bool { return f.markerCount(t) == 1 })
	time.Sleep(200 * time.Millisecond)
	require.Empty(t, rec.snapshot(), "a clear that happened is signalled by its marker row, not by a failure")
}
