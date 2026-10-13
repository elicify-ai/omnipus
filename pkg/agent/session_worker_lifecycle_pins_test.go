// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Round-1 S1-07 (3 of 3), the pins the idle-timer test never had:
//   - the idle exit decision is made under inboxMu and only when nothing waits;
//   - a /clear whose worker is leaving is handed to a fresh worker, not lost.

// An idle timeout that fires while a message is waiting must NOT retire the
// worker; once nothing waits it must retire it AND refuse later pushes. Removing
// either half (the waiting check, or the exiting store) turns this red.
func TestSessionWorker_IdleExit_OnlyWhenNothingWaits(t *testing.T) {
	al, _ := newConcurrentTestAgentLoop(t)
	defer al.Close()
	w := newSessionWorker("agent:"+testDefaultAgentID+":session:idle-exit", al, func() {})
	t.Cleanup(w.cancel)

	require.True(t, w.pushInbox(makeSessionMsg("idle-exit", "waiting")), "instrument: a live worker accepts a push")
	require.False(t, w.retireIfIdle(), "S1-07: a waiting message must keep the worker alive")
	require.False(t, w.exiting.Load(), "S1-07: a worker that kept a waiting message is not exiting")
	require.Equal(t, 1, w.waitingInboxCount())

	<-w.inbox // the message is taken
	require.True(t, w.retireIfIdle(), "S1-07: nothing waiting, so the idle timeout retires the worker")
	require.True(t, w.exiting.Load())
	require.False(t, w.pushInbox(makeSessionMsg("idle-exit", "late")),
		"S1-07: after the idle exit decision a push is refused, never stranded")
}

// Through the real loop: with an empty inbox the worker leaves after the idle
// timeout, marked exiting.
func TestSessionWorker_RunLoop_IdleTimeoutRetiresAnEmptyWorker(t *testing.T) {
	old := workerIdleTimeout
	workerIdleTimeout = 50 * time.Millisecond
	t.Cleanup(func() { workerIdleTimeout = old })

	al, _ := newConcurrentTestAgentLoop(t)
	defer al.Close()
	w := newSessionWorker("agent:"+testDefaultAgentID+":session:idle-loop", al, func() {})
	t.Cleanup(w.cancel)
	go w.runLoop()

	select {
	case <-w.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the idle worker never retired")
	}
	require.True(t, w.exiting.Load(), "an idle-timeout exit marks the worker exiting")
}

// stopThenClear fallback: the worker the /clear arrived on starts leaving while
// the stopped turn settles, so pushInbox refuses it. The /clear must reach a
// fresh worker and run, not vanish.
func TestClearWhileBusy_ExitingWorkerHandsTheClearToAFreshWorker(t *testing.T) {
	prov := newStoppableProvider()
	f := newBusyClearFixture(t, prov)

	require.True(t, f.w.enqueue(f.human("first question")))
	select {
	case <-prov.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first turn never reached the model")
	}
	f.w.exiting.Store(true) // this worker will refuse the /clear when the Stop settles
	require.True(t, f.w.enqueue(f.persistClearRow(t)))

	waitUntilClear(t, "the /clear marker written by the replacement worker", 15*time.Second,
		func() bool { return f.markerCount(t) == 1 })
}
