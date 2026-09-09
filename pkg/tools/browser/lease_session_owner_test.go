package browser

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// lease_session_owner_test.go — FR-081.
//
// FR-021 originally said the lease is reached ONLY for TabOwnerWorkspace(), on
// the reasoning that "no second writer can reach an agent's own tab set". That
// is true ACROSS sessions and false WITHIN one: /loop, async system-notify
// (pkg/agent/loop.go's own comment files it as #505) and cron SessionModeMain
// each start a second turn on an already-live session id. Three shipped paths.
//
// So the lease is taken on the RESOLVED owner, whichever it is — and never
// across owners, because a lease scoped to the browsing key alone would make
// two unrelated chats on one workspace block each other on a tab neither can
// see. That mistake is invisible under load: it looks like contention.

// TestLease_TwoTurnsOneSessionSerialise is the case FR-081 exists for: two turns
// on ONE transcriptSessionID both navigate. Exactly one holds the lease at any
// instant, BOTH complete, and neither is a Go error.
//
// Run with -race, this also catches the failure the lease prevents: two turns
// mutating one tab set concurrently.
func TestLease_TwoTurnsOneSessionSerialise(t *testing.T) {
	m := newTestManagerWithFakeTabs(t)
	m.cfg.LeaseWait = 2 * time.Second

	owner, err := TabOwnerSession("one-live-chat")
	require.NoError(t, err)

	var inFlight int64
	var maxConcurrent int64
	var completed int64
	acquisitions := int64(0)

	turn := func(agentID string) {
		release, ok, _ := m.acquireWrite(context.Background(), testKey, owner, agentID)
		require.True(t, ok, "both turns must eventually acquire — the bound is generous here")
		atomic.AddInt64(&acquisitions, 1)
		defer release()

		now := atomic.AddInt64(&inFlight, 1)
		for {
			prev := atomic.LoadInt64(&maxConcurrent)
			if now <= prev || atomic.CompareAndSwapInt64(&maxConcurrent, prev, now) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond) // stand in for a CDP round trip
		atomic.AddInt64(&inFlight, -1)
		atomic.AddInt64(&completed, 1)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	// Same agent, two turns — FR-081's own case. The agent id does not
	// distinguish them, which is exactly why the lease is keyed on the tab set
	// and not on the caller.
	go func() { defer wg.Done(); turn("jim") }()
	go func() { defer wg.Done(); turn("jim") }()
	wg.Wait()

	require.Equal(t, int64(1), maxConcurrent,
		"two turns in ONE session must serialise — %d were inside the critical section at once, "+
			"which is two turns interleaving CDP commands on one page", maxConcurrent)
	require.Equal(t, int64(2), completed, "BOTH turns must complete, not one")
	require.Equal(t, int64(2), acquisitions, "acquireWrite must have been called by both turns")
}

// TestLease_TwoSessionsNeverBlockEachOther is the direction a per-BROWSER lease
// gets wrong.
//
// A BrowsingKey is "ws:<workspaceID>" — ONE key for every session on the
// workspace. A lease scoped to the key alone would make two unrelated chats
// block each other on a tab neither can see, and it would look like contention
// rather than like a bug. So this asserts that NEITHER writer waits.
func TestLease_TwoSessionsNeverBlockEachOther(t *testing.T) {
	clock := installFakeLeaseClock(t)
	m := newTestManagerWithFakeTabs(t)
	m.cfg.LeaseWait = time.Second

	chatA, err := TabOwnerSession("chat-A")
	require.NoError(t, err)
	chatB, err := TabOwnerSession("chat-B")
	require.NoError(t, err)

	releaseA, okA, _ := m.acquireWrite(context.Background(), testKey, chatA, "jim")
	require.True(t, okA)
	defer releaseA()

	waitsBefore := clock.waitCount()
	releaseB, okB, holder := m.acquireWrite(context.Background(), testKey, chatB, "mia")
	require.True(t, okB,
		"a lease held on chat A's tabs must NOT block chat B — they are different tab sets, and "+
			"blocking here is a lease scoped to the browsing key instead of the (key, owner) pair")
	require.Empty(t, holder)
	defer releaseB()

	require.Equal(t, waitsBefore, clock.waitCount(),
		"chat B must acquire WITHOUT waiting at all; any wait means the two sessions are contending")

	// Both are held simultaneously, which is the whole point.
	require.True(t, m.writeLeases().isHeld(sessionKey(testKey, chatA)))
	require.True(t, m.writeLeases().isHeld(sessionKey(testKey, chatB)))
}

// TestLease_TakenOnSessionOwnerNotOnlyWorkspace is the direct guard on the
// SUPERSEDED trigger, and it is the test SC-028's mutation receipt is taken
// against: restore FR-021's TabOwnerWorkspace()-only trigger and this must go
// RED.
//
// It asserts at the seam — acquireWrite is observably CALLED for a
// TabOwnerSession() write — rather than inferring it from an outcome, because
// the outcome of an uncontended lease is indistinguishable from no lease at all.
func TestLease_TakenOnSessionOwnerNotOnlyWorkspace(t *testing.T) {
	registry := tools.NewToolRegistry()
	owner, err := TabOwnerSession("a-live-chat")
	require.NoError(t, err)

	mgr := registerToolsForTestAs(t, registry, controlTestCfg(t),
		security.NewSSRFChecker([]string{"127.0.0.1"}),
		func(m *BrowserManager) ManagerResolver {
			return &fixedResolver{mgr: m, key: testKey, owner: owner}
		})
	t.Cleanup(mgr.Shutdown)
	mgr.cfg.LeaseWait = 20 * time.Millisecond

	sessionSet := sessionKey(testKey, owner)

	// Hold the SESSION's lease from "another turn". If the tool takes the lease
	// on the resolved owner (FR-081) it must defer. If the trigger is
	// TabOwnerWorkspace()-only, it sails past and returns a CDP/session error
	// instead — which is what makes this test RED against that build.
	release, ok, _ := mgr.acquireWrite(context.Background(), testKey, owner, "the-other-turn")
	require.True(t, ok)
	t.Cleanup(release)
	require.True(t, mgr.writeLeases().isHeld(sessionSet))

	navigate, found := registry.Get("browser_navigate")
	require.True(t, found)
	result := navigate.Execute(context.Background(), map[string]any{"url": "http://127.0.0.1/whatever"})
	require.NotNil(t, result)

	require.Contains(t, result.ForLLM, leaseDeferralMarker,
		"browser_navigate on a SESSION-owned tab set must take the write lease and defer behind "+
			"another turn holding it (FR-081). A non-deferral here means the lease is still "+
			"triggered only for TabOwnerWorkspace() — the SUPERSEDED FR-021 rule — and two turns in "+
			"one chat can interleave CDP commands on one page.")
	require.Contains(t, result.ForLLM, "the-other-turn", "the deferral must name the holder")

	// The workspace-owned set is genuinely untouched by all of this: the lease
	// is per (key, owner) pair, so holding one never holds the other.
	require.False(t, mgr.writeLeases().isHeld(sessionKey(testKey, TabOwnerWorkspace())),
		"a lease on a chat's tabs must not also hold the operator's")
}
