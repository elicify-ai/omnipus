package agent

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// R3 follow-up (founder, option A): a /clear is HELD only while the server is
// stopping the running reply before it clears (deferClearUntilStopped). The
// receiver learns it through SetClearHeldHook, before the stop starts, so the
// held report always precedes a later not-performed (failed) report. A /clear to
// an idle agent, or to a stopped one (a person's message continues a landed
// stop), is applied at once and is never reported held.

type clearEventLog struct {
	mu  sync.Mutex
	log []string
}

func (c *clearEventLog) add(kind string) func(sessionID, entryID string) {
	return func(sessionID, entryID string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.log = append(c.log, kind+":"+sessionID+":"+entryID)
	}
}

func (c *clearEventLog) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.log...)
}

func (c *clearEventLog) kinds() string {
	snap := c.snapshot()
	out := make([]string, 0, len(snap))
	for _, e := range snap {
		out = append(out, strings.SplitN(e, ":", 2)[0])
	}
	return strings.Join(out, ",")
}

func TestClearHeld_BusyTurn_ReportsHeldOnceBeforeTheClearRuns(t *testing.T) {
	prov := newStoppableProvider()
	f := newBusyClearFixture(t, prov)
	ev := &clearEventLog{}
	f.al.SetClearHeldHook(ev.add("held"))
	f.al.SetClearNotPerformedHook(ev.add("failed"))

	require.True(t, f.w.enqueue(f.human("first question")))
	select {
	case <-prov.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first turn never reached the model")
	}
	require.True(t, f.w.enqueue(f.persistClearRow(t)))

	require.Equal(t, []string{"held:" + f.sid + ":clear-row-1"}, ev.snapshot(),
		"the held report is made as the server takes the /clear over, naming the session and the /clear's own row")
	waitUntilClear(t, "the /clear to run after the stop", 15*time.Second, func() bool { return f.markerCount(t) == 1 })
	require.Equal(t, "held", ev.kinds(), "an applied /clear is never followed by a failed report")
}

func TestClearHeld_IdleAgent_ReportsNothing(t *testing.T) {
	prov := newStoppableProvider()
	f := newBusyClearFixture(t, prov)
	ev := &clearEventLog{}
	f.al.SetClearHeldHook(ev.add("held"))
	f.al.SetClearNotPerformedHook(ev.add("failed"))

	require.True(t, f.w.enqueue(f.persistClearRow(t)), "no turn is running")
	waitUntilClear(t, "the /clear to run at once", 15*time.Second, func() bool { return f.markerCount(t) == 1 })
	require.Empty(t, ev.snapshot(), "an idle agent applies the /clear immediately: nothing is held")
}

func TestClearHeld_NeverFollowsFailed(t *testing.T) {
	old := clearStopSettleTimeout
	clearStopSettleTimeout = 300 * time.Millisecond
	t.Cleanup(func() { clearStopSettleTimeout = old })
	prov := newStuckProvider()
	f := newBusyClearFixture(t, prov)
	t.Cleanup(func() { close(prov.release) })
	ev := &clearEventLog{}
	f.al.SetClearHeldHook(ev.add("held"))
	f.al.SetClearNotPerformedHook(ev.add("failed"))

	require.True(t, f.w.enqueue(f.human("first question")))
	select {
	case <-prov.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the first turn never reached the model")
	}
	require.True(t, f.w.enqueue(f.persistClearRow(t)))

	waitUntilClear(t, "the refusal of the unsettled /clear", 10*time.Second, func() bool { return strings.Contains(ev.kinds(), "failed") })
	prov.release <- struct{}{}
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, "held,failed", ev.kinds(), "held first, then failed, and nothing after failed")
}
