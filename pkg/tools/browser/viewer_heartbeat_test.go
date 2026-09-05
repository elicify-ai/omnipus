// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package browser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-072 FR-052 — a viewer must keep PROVING it is there.
//
// BrowserManager.Viewers() used to return the raw attach count, so a
// live-panel WebSocket whose cleanup never ran (SIGKILL, a half-open socket
// behind a NAT that dropped state, a panic past readLoop's defer) left the
// count at 1 with nobody behind it. That phantom pinned its workspace's
// Chrome against BOTH pool controls — eviction (pool.go `pinned`) and idle
// close (`idle`) — forever. It is a deadlock, not a leak: nothing could ever
// reclaim that browser, and under memory pressure the pool refused to launch
// others while it was held.
//
// The two tests that matter are the pair, and they pull in opposite
// directions:
//
//   - the stale half proves a viewer that stops heart-beating is eventually
//     not counted (the leak is fixed);
//   - the idle-but-alive half proves a viewer that is merely quiet — a person
//     reading a page, touching nothing — still pins (the fix did not become a
//     worse bug).
//
// A build that reaps every viewer immediately passes the first alone. A build
// that reaps none passes the second alone. Only both together pin the
// behaviour, so neither may be deleted without the other.

// heartbeatWindow mirrors viewerLivenessWindow so the tests below read in
// terms of the contract rather than a bare literal, while still failing if the
// constant is redefined out from under them (see TestViewerLivenessWindow_IsTwiceTheReadDeadline).
const heartbeatWindow = viewerLivenessWindow

// TestViewers_StaleViewerStopsBeingCounted — the leak. A viewer attaches,
// heartbeats for a while like a healthy panel, and then its socket dies
// without any detach ever running. Past the liveness window it must stop
// counting, so the pool may finally evict and idle-close the browser it was
// pinning.
func TestViewers_StaleViewerStopsBeingCounted(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	m.ViewerAttached(testSessionID)
	require.Equal(t, 1, m.Viewers(), "a freshly attached viewer counts")

	// A healthy panel: pongs land every 30s in production. Two windows' worth
	// of them, so the test cannot pass merely because too little time passed.
	for elapsed := time.Duration(0); elapsed < 2*heartbeatWindow; elapsed += 30 * time.Second {
		*clock = clock.Add(30 * time.Second)
		m.ViewerHeartbeat(testSessionID)
		require.Equal(t, 1, m.Viewers(),
			"a heart-beating viewer must keep counting at +%s", elapsed+30*time.Second)
	}

	// The socket dies here. Nothing detaches: this is the whole point — the
	// raw count stays at 1 forever.
	m.mu.Lock()
	rawCount := m.sessions[testSessionID].viewers
	m.mu.Unlock()
	require.Equal(t, 1, rawCount,
		"the phantom must still be attached as far as the raw count knows — "+
			"a test where the viewer actually detached would prove nothing")

	// Right up to the window, the benefit of the doubt holds.
	*clock = clock.Add(heartbeatWindow)
	assert.Equal(t, 1, m.Viewers(),
		"a viewer exactly at the liveness window must still count — the window is inclusive, "+
			"and shortening it risks reaping a browser a real person is watching")

	// One tick past it, the phantom is gone.
	*clock = clock.Add(time.Nanosecond)
	assert.Equal(t, 0, m.Viewers(),
		"a viewer that stopped heart-beating longer ago than the liveness window must not be counted; "+
			"a raw count here pins the workspace's Chrome against eviction AND idle close forever")

	// And the raw count is untouched — Viewers() reports liveness, it does not
	// mutate the bookkeeping. A real detach must still be able to arrive.
	m.mu.Lock()
	rawAfter := m.sessions[testSessionID].viewers
	m.mu.Unlock()
	assert.Equal(t, 1, rawAfter,
		"liveness is a judgement, not a detach — Viewers() must not silently decrement the count")
}

// TestViewers_IdleButAliveViewerStillPins — the half that protects real
// people. Somebody is reading a page for two hours. They click nothing, type
// nothing, and no agent tool call touches the browser. Their browser still
// answers the server's keep-alive ping, and that alone must keep the browser
// pinned.
//
// Getting this wrong closes a window out from under someone who is looking at
// it, which is a worse failure than holding a phantom's browser too long.
func TestViewers_IdleButAliveViewerStillPins(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	m.ViewerAttached(testSessionID)

	// Two hours of watching without a single interaction. The only thing
	// happening is the 30s ping/pong, which is protocol-level and owes nothing
	// to the user.
	for elapsed := time.Duration(0); elapsed < 2*time.Hour; elapsed += 30 * time.Second {
		*clock = clock.Add(30 * time.Second)
		m.ViewerHeartbeat(testSessionID)
	}

	assert.Equal(t, 1, m.Viewers(),
		"a viewer that has answered every keep-alive must still count after two hours of no interaction — "+
			"a person reading a page touches nothing, and treating that as detached closes the window "+
			"they are looking at")
	assert.Empty(t, m.ReapIdleSessions(),
		"and the reaper must not touch a context somebody is still watching, however idle its tabs")
	assert.Contains(t, m.sessions, testSessionID)
}

// TestViewerHeartbeat_SurvivesMissedPongsWithinTheWindow — the reason the
// window is 2x the 60s read deadline rather than 1x. A single lost pong (a GC
// pause, a scheduling hiccup, half a second of packet loss) must not make a
// watching human look detached.
func TestViewerHeartbeat_SurvivesMissedPongsWithinTheWindow(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	m.ViewerAttached(testSessionID)

	// Three consecutive 30s pings go unanswered — 90s of silence, more than
	// one read deadline, less than the liveness window.
	*clock = clock.Add(90 * time.Second)
	require.Equal(t, 1, m.Viewers(),
		"three missed pongs must not unseat a viewer; at a 1x window this is already dead")

	// The fourth pong arrives and the clock resets in full.
	m.ViewerHeartbeat(testSessionID)
	*clock = clock.Add(heartbeatWindow)
	assert.Equal(t, 1, m.Viewers(), "a late heartbeat must restore the full window")

	*clock = clock.Add(time.Nanosecond)
	assert.Equal(t, 0, m.Viewers(), "and silence past the window still ends the pin")
}

// TestReapIdleSessions_StaleViewerNoLongerPinsTabs — the second consumer.
// Eviction reads Viewers(); the per-tab reaper reads the same liveness
// directly. Both must stop honouring a phantom, because a context whose tabs
// are never reaped keeps its Chrome permanently non-idle (pool.go's `idle`
// requires zero tabs) even once eviction has released it.
func TestReapIdleSessions_StaleViewerNoLongerPinsTabs(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	m.ViewerAttached(testSessionID)

	// Well past the idle TTL, but still inside the liveness window: the viewer
	// has not had time to prove itself absent, so the pin holds.
	*clock = clock.Add(31 * time.Minute)
	m.ViewerHeartbeat(testSessionID)
	require.Empty(t, m.ReapIdleSessions(),
		"a heart-beating viewer pins the whole context regardless of tab idleness")

	// Now it goes quiet for good.
	*clock = clock.Add(heartbeatWindow + time.Nanosecond)
	assert.Equal(t, []string{testSessionID}, m.ReapIdleSessions(),
		"once the viewer has stopped proving it is there, its tabs are reapable again — "+
			"otherwise the tabs live forever and the instance is never idle-closable")
	assert.NotContains(t, m.sessions, testSessionID)
}

// TestViewerHeartbeat_DoesNotResurrectDetachedOrUnknownSessions — a heartbeat
// must never conjure a pin out of nothing. A pong that outlives its detach, or
// arrives for a session the manager has already torn down and recreated, must
// be inert rather than re-pinning a browser nobody is watching.
func TestViewerHeartbeat_DoesNotResurrectDetachedOrUnknownSessions(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)

	// No viewer has ever attached. A stray heartbeat must not create one.
	m.ViewerHeartbeat(testSessionID)
	assert.Equal(t, 0, m.Viewers(), "a heartbeat with no attached viewer must not manufacture a pin")

	// Attach then detach; late pongs from the dead connection keep arriving.
	m.ViewerAttached(testSessionID)
	m.ViewerDetached(testSessionID)
	m.ViewerHeartbeat(testSessionID)
	m.ViewerHeartbeat(testSessionID)
	assert.Equal(t, 0, m.Viewers(), "heartbeats after the last detach must not re-pin the context")

	*clock = clock.Add(31 * time.Minute)
	assert.Equal(t, []string{testSessionID}, m.ReapIdleSessions(),
		"and the context must still reap on schedule despite the late heartbeats")

	assert.NotPanics(t, func() { m.ViewerHeartbeat("never-opened") },
		"a heartbeat for an unknown session is a no-op, not a panic")
}

// TestViewerAttached_StampsItsOwnFirstProofOfLife — the narrow case a
// heartbeat-only design would miss: a panel that attaches and whose socket
// wedges before the very first pong can come back. Attach is itself proof of
// life, so such a viewer gets exactly one window's grace and then ages out,
// rather than pinning forever on a zero timestamp.
func TestViewerAttached_StampsItsOwnFirstProofOfLife(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session(testSessionID)
	require.NoError(t, err)
	m.ViewerAttached(testSessionID) // and never a single heartbeat after it

	*clock = clock.Add(heartbeatWindow)
	assert.Equal(t, 1, m.Viewers(), "the attach itself buys one full window")

	*clock = clock.Add(time.Nanosecond)
	assert.Equal(t, 0, m.Viewers(),
		"a viewer that never heartbeats at all must still age out — treating a never-stamped viewer "+
			"as alive leaves the FR-052 hole open for a socket that wedges before its first pong")
}

// TestViewers_LiveViewerOnOneContextDoesNotRescueAnother — liveness is
// per-browsing-context. A live panel on one workspace must not keep a
// phantom's context on another workspace pinned, or the fix would only work
// for managers owning a single context.
func TestViewers_LiveViewerOnOneContextDoesNotRescueAnother(t *testing.T) {
	m, clock := newReapableManager(t, 30*time.Minute)

	_, err := m.Session("session-live")
	require.NoError(t, err)
	_, err = m.Session("session-phantom")
	require.NoError(t, err)

	m.ViewerAttached("session-live")
	m.ViewerAttached("session-phantom")
	require.Equal(t, 2, m.Viewers())

	// Only the live one keeps answering.
	for elapsed := time.Duration(0); elapsed < 2*heartbeatWindow; elapsed += 30 * time.Second {
		*clock = clock.Add(30 * time.Second)
		m.ViewerHeartbeat("session-live")
	}

	assert.Equal(t, 1, m.Viewers(),
		"the live context still counts and the phantom's does not; a manager-wide 'most recent beat' "+
			"check would report 2 and keep the phantom pinned")
}

// TestViewerLivenessWindow_IsTwiceTheReadDeadline pins the constant itself.
// The doubling is the safety argument, not an arbitrary number: at 1x, one
// pong lost to a GC pause makes a watching human look detached. This fails
// loudly if somebody "tidies" the window down to match the read deadline.
func TestViewerLivenessWindow_IsTwiceTheReadDeadline(t *testing.T) {
	// The live-panel WebSocket's read deadline (pkg/gateway/browser_ws.go).
	// Restated here rather than imported because pkg/tools/browser must not
	// depend on pkg/gateway.
	const browserWSReadDeadline = 60 * time.Second

	assert.Equal(t, 2*browserWSReadDeadline, viewerLivenessWindow,
		"the liveness window must stay at 2x the live-panel read deadline — four chances to speak "+
			"before anything reclaims a browser somebody may be watching")
}
