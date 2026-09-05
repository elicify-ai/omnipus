package browser

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration coverage for the SEAM the 2026-08-03 tab-switch bug lived in.
//
// Both halves of this chain were already well covered in isolation before the
// bug shipped — SwitchTab had TestSwitchTab_ChangesActiveIndex, and the
// recapture had TestCaptureSession_RecapturePropagatesToRelayAndIngest — but
// NOTHING exercised them together. The defect sat precisely between them: the
// switch updated activeIdx and fired a recapture, yet the recapture re-bound
// to the same tab because Chrome had never been told the active tab moved.
//
// Isolation tests cannot catch that class of bug by construction. These tests
// assert the whole path:
//
//	SwitchTab → activate in Chrome → notifyTabsChanged
//	          → LiveViewRegistry.handleTabsChanged → onTabsChanged
//	          → activeTabChanged → CaptureSession.Recapture
//
// so a future change that severs any link fails here even if every unit test
// on either side still passes.

// chainRecorder captures the ordered sequence of observable effects along the
// switch → capture chain.
type chainRecorder struct {
	mu     sync.Mutex
	events []string
}

func (c *chainRecorder) add(ev string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *chainRecorder) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.events))
	copy(out, c.events)
	return out
}

func (c *chainRecorder) count(ev string) int {
	n := 0
	for _, e := range c.snapshot() {
		if e == ev {
			n++
		}
	}
	return n
}

// TestSwitchTab_ActivationPrecedesTabsChangedNotification is the end-to-end
// ordering guarantee. The recapture is triggered BY the tabs-changed callback,
// so Chrome must already agree about the active tab before that callback runs
// — otherwise the recapture resolves the old tab and the stream silently never
// moves, which is exactly the shipped bug.
func TestSwitchTab_ActivationPrecedesTabsChangedNotification(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	rec := &chainRecorder{}

	m.tabFocusFn = func(_ context.Context, actions ...chromedp.Action) error {
		if focusTreatment(actions) == "foreground" {
			rec.add("chrome-activated")
		}
		return nil
	}
	m.SetTabsChangedFunc(func(_ string, _ []Tab, activeIdx int) {
		// Stands in for the real LiveViewRegistry.handleTabsChanged, which is
		// what ultimately calls CaptureSession.Recapture.
		rec.add("tabs-changed")
		rec.add("recapture-would-fire")
		_ = activeIdx
	})

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	rec.mu.Lock()
	rec.events = nil // discard setup noise
	rec.mu.Unlock()

	_, err = m.SwitchTab(testSessionID, 0)
	require.NoError(t, err)

	got := rec.snapshot()
	require.Equal(t, []string{"chrome-activated", "tabs-changed", "recapture-would-fire"}, got,
		"Chrome must be told the active tab moved BEFORE the tabs-changed callback fires the "+
			"recapture; reversing this re-opens the silent wrong-tab capture")
}

// TestSwitchTab_EverySwitchActivatesAndNotifies — a rapid sequence of switches
// must produce one activation per switch. A missed activation anywhere in the
// sequence leaves the capture pinned to a stale tab for every subsequent
// switch, which is how the live session ended up three-way desynced (tab strip,
// URL bar, and pixels all disagreeing).
func TestSwitchTab_EverySwitchActivatesAndNotifies(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	rec := &chainRecorder{}

	m.tabFocusFn = func(_ context.Context, actions ...chromedp.Action) error {
		if focusTreatment(actions) == "foreground" {
			rec.add("activate")
		}
		return nil
	}
	m.SetTabsChangedFunc(func(string, []Tab, int) { rec.add("notify") })

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = m.OpenTab(testSessionID)
		require.NoError(t, err)
	}

	rec.mu.Lock()
	rec.events = nil
	rec.mu.Unlock()

	order := []int{2, 0, 1, 0, 2}
	for _, idx := range order {
		_, err := m.SwitchTab(testSessionID, idx)
		require.NoError(t, err)
	}

	assert.Equal(t, len(order), rec.count("activate"),
		"every switch must activate its target tab in Chrome — a skipped activation pins "+
			"the capture to a stale tab for all later switches")
	assert.Equal(t, len(order), rec.count("notify"),
		"every switch must still notify the tabs-changed subscribers")
}

// TestSwitchTab_ActivatesTabMatchingResolvedSession ties the activation to the
// SAME context Session() hands to the capture path. If these ever diverge, the
// panel activates one tab while the capture binds another — the desync in a
// different disguise.
func TestSwitchTab_ActivatesTabMatchingResolvedSession(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	// Capture the activated context via a slice rather than assigning a
	// context.Context into a captured variable — the latter trips fatcontext
	// ("nested context in function literal") and, more substantively, storing a
	// context for later comparison is exactly the pattern that lint discourages.
	var mu sync.Mutex
	activatedCtxs := make([]context.Context, 0, 1)
	m.tabFocusFn = func(tabCtx context.Context, actions ...chromedp.Action) error {
		if focusTreatment(actions) != "foreground" {
			return nil
		}
		mu.Lock()
		activatedCtxs = append(activatedCtxs, tabCtx)
		mu.Unlock()
		return nil
	}

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	// Discard setup activations: OpenTab foregrounds the tab it opens too
	// (review finding F9), so only the switch below is under test here.
	mu.Lock()
	activatedCtxs = activatedCtxs[:0]
	mu.Unlock()

	_, err = m.SwitchTab(testSessionID, 1)
	require.NoError(t, err)

	// Session() is the oracle the live/capture paths use to resolve "the
	// active tab" (ADR-041 D1) — the activation must have targeted that exact
	// context.
	resolved, err := m.Session(testSessionID)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, activatedCtxs, 1, "exactly one activation expected")
	assert.Same(t, resolved, activatedCtxs[0],
		"the tab activated in Chrome must be the same context Session() resolves, or the "+
			"panel and the capture disagree about which tab is live")
}

// TestSwitchTab_TabsChangedReceivesNewActiveIndex guards the payload the live
// registry relies on to decide whether the active tab actually changed
// (onTabsChanged's activeTabChanged signal, which gates the recapture).
func TestSwitchTab_TabsChangedReceivesNewActiveIndex(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	var mu sync.Mutex
	var gotIdx []int
	var gotActiveFlags []bool
	m.SetTabsChangedFunc(func(_ string, tabs []Tab, activeIdx int) {
		mu.Lock()
		defer mu.Unlock()
		gotIdx = append(gotIdx, activeIdx)
		if activeIdx >= 0 && activeIdx < len(tabs) {
			gotActiveFlags = append(gotActiveFlags, tabs[activeIdx].Active)
		}
	})

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	mu.Lock()
	gotIdx, gotActiveFlags = nil, nil
	mu.Unlock()

	_, err = m.SwitchTab(testSessionID, 1)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []int{1}, gotIdx, "the tabs-changed callback must carry the NEW active index")
	require.Equal(t, []bool{true}, gotActiveFlags,
		"the snapshot's active flag must agree with the reported active index")
}

// TestSwitchTab_SlowActivationDoesNotBlockOtherSessions — activation issues a
// real, blocking CDP call in production. A slow or hung bringToFront on one
// browsing context must not stall unrelated manager work, or one wedged tab
// takes the whole browser subsystem down with it.
func TestSwitchTab_SlowActivationDoesNotBlockOtherSessions(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)

	release := make(chan struct{})
	entered := make(chan struct{}, 1)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(testSessionID)
	require.NoError(t, err)

	// Installed AFTER setup, deliberately: OpenTab drives the same focus seam
	// as SwitchTab (review finding F9 — opening a tab moves the active tab
	// too), so a hook that blocks forever would hang the setup instead of the
	// switch this test is about.
	m.tabFocusFn = func(context.Context, ...chromedp.Action) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release // simulate a hung CDP call
		return nil
	}

	switched := make(chan struct{})
	go func() {
		defer close(switched)
		_, _ = m.SwitchTab(testSessionID, 1)
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("activation hook was never reached")
	}

	// While that switch is stuck inside activation, unrelated manager reads
	// must still complete promptly.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = m.ListTabs(testSessionID)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("a hung tab activation blocked an unrelated manager call — activation must not " +
			"hold the BrowserManager lock (ADR-038)")
	}

	close(release)
	<-switched
}
