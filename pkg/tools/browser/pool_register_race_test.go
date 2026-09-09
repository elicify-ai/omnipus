package browser

// pool_register_race_test.go — a browser closed in the middle of a manager
// registering against it must make the call FAIL, not hand back a dead
// coordinator and report success. See ErrBrowserRestarting.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Defect 3: a close during registration must not hand back a dead handle -

// TestPool_RegisterRefusesAfterTheBrowserWasClosedMidRegistration pins the
// difference between a call that fails once and a workspace that cannot browse
// again until the gateway restarts.
//
// The window: Register acquires an instance, registers the manager against its
// coordinator, then re-checks that the instance is still this key's live one.
// An idle close, an eviction or a workspace deletion landing in between makes
// that re-check fail. The build this test was written against noticed, skipped
// the mgrs bookkeeping — and then returned the dead coordinator anyway with a
// nil error.
//
// Why that was permanent rather than transient, which is the part worth
// keeping: the manager adopts the dead root context and sets m.started, so
// ensureStarted returns early forever after. The recovery mechanism that
// exists for exactly this — closeInstance calling invalidateConnection on
// every manager in inst.mgrs — cannot reach it, because the bookkeeping that
// would have put it in that set is the bookkeeping that was skipped. Nothing
// else resets it. Every browser_* call for that workspace returns "context
// canceled" from then on.
//
// Mutation contract: restore the old tail of Register
// (`return inst.coord, rootCtx, nil` unconditionally) and this test goes red
// on its first assertion.
func TestPool_RegisterRefusesAfterTheBrowserWasClosedMidRegistration(t *testing.T) {
	f := newPoolFixture(t)
	key := browserTestKey("alpha")
	mgr := &BrowserManager{sessions: map[string]*sessionEntry{}}

	// The close lands in the window: after the coordinator registration has
	// returned, before the pool re-checks liveness. Once only — the retry must
	// find a healthy pool.
	var once sync.Once
	f.pool.registerRaceHook = func() {
		once.Do(func() { f.pool.Close(key) })
	}

	coord, rootCtx, err := f.pool.Register(context.Background(), key, mgr)

	require.Error(t, err,
		"the browser was closed while this call was connecting to it, so there is no working "+
			"coordinator to return — reporting success here hands the caller a dead handle it "+
			"will latch onto permanently")
	assert.True(t, errors.Is(err, ErrBrowserRestarting),
		"the refusal must be errors.Is-comparable so a caller can branch on retryability "+
			"without matching prose")
	assert.Contains(t, strings.ToLower(err.Error()), "retry",
		"the message must tell the caller the useful thing: this is transient, ask again")
	assert.Nil(t, coord, "a refused registration must not hand back a coordinator at all")
	assert.Nil(t, rootCtx, "nor the dead root context that goes with it")

	// The bookkeeping and the return value have to agree. A manager recorded
	// against an instance that was NOT returned to it is the mirror-image leak.
	f.pool.mu.Lock()
	_, stillListed := f.pool.instances[key.String()]
	f.pool.mu.Unlock()
	assert.False(t, stillListed, "the closed instance must be gone from the pool")

	// And the whole point of refusing rather than lying: the retry works. This
	// is the assertion that separates "fails once" from "broken until restart".
	coord2, rootCtx2, err2 := f.pool.Register(context.Background(), key, mgr)
	require.NoError(t, err2, "the caller's retry must resolve a freshly launched browser")
	require.NotNil(t, coord2)
	require.NotNil(t, rootCtx2)
	assert.NoError(t, rootCtx2.Err(),
		"the retry must return a LIVE root context — a second dead handle would be the same "+
			"defect one call later")

	// The retried manager is registered, so a later close can reach it.
	f.pool.mu.Lock()
	inst := f.pool.instances[key.String()]
	var registered bool
	if inst != nil {
		_, registered = inst.mgrs[mgr]
	}
	f.pool.mu.Unlock()
	assert.True(t, registered,
		"a manager handed a live coordinator MUST be in the instance's mgrs set — that set is "+
			"the only thing closeInstance can invalidate, so a manager missing from it can never "+
			"be told its browser went away")
}

// TestPool_RegisterRecordsTheManagerOnTheHappyPath is the counterweight. A
// Register that refused unconditionally would pass the test above while
// removing browsing entirely.
func TestPool_RegisterRecordsTheManagerOnTheHappyPath(t *testing.T) {
	f := newPoolFixture(t)
	key := browserTestKey("alpha")
	mgr := &BrowserManager{sessions: map[string]*sessionEntry{}}

	coord, rootCtx, err := f.pool.Register(context.Background(), key, mgr)
	require.NoError(t, err, "an undisturbed registration must succeed")
	require.NotNil(t, coord)
	require.NotNil(t, rootCtx)
	assert.NoError(t, rootCtx.Err())

	f.pool.mu.Lock()
	_, registered := f.pool.instances[key.String()].mgrs[mgr]
	f.pool.mu.Unlock()
	assert.True(t, registered)
}
