package browser

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression coverage for the live-measured 2026-08-03 defect: switching tabs
// updated ONLY this manager's se.activeIdx and never told Chrome, so the
// WebRTC capture path — whose encoder resolves its target with
// chrome.tabs.query({active: true, ...}) (captureext/embedded/encoder.js
// findActiveTargetTab) — kept streaming the tab the user had just switched
// AWAY from.
//
// Measured on UAT v36 before the fix: the tab strip said "Google", the URL bar
// said "en.wikipedia.org/wiki/Octopus", and the pixels showed Wikipedia — a
// three-way desync, with the video track still readyState:"live", muted:false
// and ZERO console errors (only a downstream stalled-RTP watchdog warning).
// That silence is why these tests assert on the ACTIVATION CALL itself rather
// than on any error signal: there was none to observe.
//
// activateTabInChrome (manager.go's SwitchTab) is the ONLY BringToFront call
// left on the tab-switch path (ADR-061: the JPEG screencast path, which used
// to call page.BringToFront() itself before every StartScreencast, is gone —
// video is carried exclusively by WebRTC now). These tests pin the guarantee
// that SwitchTab still tells Chrome itself which tab is active, so the
// WebRTC capture's chrome.tabs.query({active:true}) resolution can't desync
// from it.

// recordingActivator is a test double for the activateTabFn seam that records
// every context it was asked to activate, in order.
type recordingActivator struct {
	mu   sync.Mutex
	ctxs []context.Context
	err  error
}

func (r *recordingActivator) fn(tabCtx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ctxs = append(r.ctxs, tabCtx)
	return r.err
}

func (r *recordingActivator) calls() []context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]context.Context, len(r.ctxs))
	copy(out, r.ctxs)
	return out
}

// newManagerWithRecordedActivation builds a fake-tab manager whose tab
// activation is recorded rather than dispatched to real CDP.
func newManagerWithRecordedActivation(t *testing.T, maxTabs int) (*BrowserManager, *recordingActivator) {
	t.Helper()
	m := newTestManagerWithFakeTabs(t, maxTabs)
	rec := &recordingActivator{}
	m.activateTabFn = rec.fn
	return m, rec
}

// TestSwitchTab_ActivatesNewTabInChrome is THE regression test for the
// three-way desync. Without SwitchTab's activateTabInChrome call it fails:
// zero activations are recorded, which is exactly the state that let the
// encoder's chrome.tabs.query keep resolving the previous tab.
func TestSwitchTab_ActivatesNewTabInChrome(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t, 5)

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID)
	require.NoError(t, err)

	// Resolve the context of the tab we are about to switch to, so the
	// assertion below proves the RIGHT tab was activated — not merely that
	// some activation happened.
	m.mu.Lock()
	wantCtx := m.sessions[DefaultSessionID].tabs[1].ctx
	m.mu.Unlock()

	before := len(rec.calls())
	_, err = m.SwitchTab(DefaultSessionID, 1)
	require.NoError(t, err)

	calls := rec.calls()
	require.Greater(t, len(calls), before,
		"SwitchTab must activate the newly-active tab in Chrome; without it the WebRTC "+
			"encoder's chrome.tabs.query({active:true}) keeps resolving the PREVIOUS tab "+
			"and the stream silently never moves (live-measured 2026-08-03)")
	assert.Same(t, wantCtx, calls[len(calls)-1],
		"the activation must target the tab that was just switched TO")
}

// TestSwitchTab_ActivatesBeforeNotifyingTabsChanged pins the ORDERING that
// makes the fix work. The tabs-changed callback is what triggers the WebRTC
// recapture; if activation ran after it, the recapture would still race a
// stale active-tab notion in Chrome and could re-bind to the old tab — the
// original bug, merely narrowed to a race window.
func TestSwitchTab_ActivatesBeforeNotifyingTabsChanged(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)

	var mu sync.Mutex
	var order []string
	m.activateTabFn = func(context.Context) error {
		mu.Lock()
		order = append(order, "activate")
		mu.Unlock()
		return nil
	}
	m.SetTabsChangedFunc(func(string, []Tab, int) {
		mu.Lock()
		order = append(order, "tabsChanged")
		mu.Unlock()
	})

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID)
	require.NoError(t, err)

	mu.Lock()
	order = nil // ignore setup-time callbacks; only the switch matters
	mu.Unlock()

	_, err = m.SwitchTab(DefaultSessionID, 0)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"activate", "tabsChanged"}, order,
		"Chrome must already agree about the active tab BEFORE the tabs-changed "+
			"callback fires the WebRTC recapture, or the recapture races a stale "+
			"active-tab notion and can re-bind to the old tab")
}

// TestSwitchTab_ActivationFailureIsNonFatal — activation is best-effort. The
// switch is already recorded in se.activeIdx, so every server-side consumer
// (Session(), tool calls, the JPEG path) still follows the new tab correctly;
// only the WebRTC capture's own tab resolution degrades. A failure here must
// never turn a successful switch into an error the user sees.
func TestSwitchTab_ActivationFailureIsNonFatal(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t, 5)
	rec.err = errors.New("bringToFront exploded")

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID)
	require.NoError(t, err)

	tab, err := m.SwitchTab(DefaultSessionID, 1)
	require.NoError(t, err, "a failed tab activation must not fail the switch itself")
	assert.Equal(t, 1, tab.Index)
	assert.True(t, tab.Active)

	// The authoritative server-side state must still reflect the switch.
	tabs, activeIdx, err := m.ListTabs(DefaultSessionID)
	require.NoError(t, err)
	assert.Equal(t, 1, activeIdx)
	assert.True(t, tabs[1].Active)
}

// TestSwitchTab_DoesNotActivateOnLookupFailure — an out-of-range or unknown
// switch must not touch Chrome at all. Activating on a rejected switch would
// steal focus toward a tab the caller never successfully selected.
func TestSwitchTab_DoesNotActivateOnLookupFailure(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t, 5)

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	before := len(rec.calls())

	_, err = m.SwitchTab(DefaultSessionID, 99)
	require.Error(t, err)
	_, err = m.SwitchTab("never-opened", 0)
	require.Error(t, err)

	assert.Len(t, rec.calls(), before,
		"a rejected switch must never activate a tab in Chrome")
}

// TestSwitchTab_ActivatesUnderNoManagerLock guards the ADR-038 rule every CDP
// call in this file follows. activateTabInChrome issues a real, blocking
// chromedp.Run in production; holding m.mu across it would deadlock any
// concurrent manager call. Calling back into the manager from inside the
// activation hook is the same trick TestSetTabsChangedFunc uses — if the lock
// were held, this test would hang rather than fail.
func TestSwitchTab_ActivatesUnderNoManagerLock(t *testing.T) {
	m := newTestManagerWithFakeTabs(t, 5)

	var reentered atomic.Bool
	m.activateTabFn = func(context.Context) error {
		// Would deadlock if SwitchTab held m.mu across the activation.
		if _, _, err := m.ListTabs(DefaultSessionID); err == nil {
			reentered.Store(true)
		}
		return nil
	}

	_, err := m.Session(DefaultSessionID)
	require.NoError(t, err)
	_, err = m.OpenTab(DefaultSessionID)
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = m.SwitchTab(DefaultSessionID, 1)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SwitchTab deadlocked — activateTabInChrome must run with NO BrowserManager lock held (ADR-038)")
	}
	assert.True(t, reentered.Load(), "the activation hook should have been able to re-enter the manager")
}

// TestSwitchTab_SkipsActivationForDeadContext — a tab whose context already
// died (browser crash, tab closed out from under us) must not be handed to
// chromedp: in production that is a guaranteed PageTimeout stall for a tab
// that cannot be brought to front anyway.
func TestSwitchTab_SkipsActivationForDeadContext(t *testing.T) {
	m, rec := newManagerWithRecordedActivation(t, 5)

	dead, cancel := context.WithCancel(context.Background())
	cancel()

	before := len(rec.calls())
	m.activateTabInChrome(dead, DefaultSessionID, 0)
	assert.Len(t, rec.calls(), before, "a canceled tab context must be skipped, not dispatched to CDP")

	// A nil context must be equally inert.
	m.activateTabInChrome(nil, DefaultSessionID, 0)
	assert.Len(t, rec.calls(), before, "a nil tab context must be skipped")
}
