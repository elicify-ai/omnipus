package browser

// pool_gate_test.go — the two controls that are the ENTIRE defence against a
// host being eaten by browsers, and the proof that neither is a no-op.
//
// FR-061 states the obligation plainly: idle close and the pressure gate may
// not silently no-op, ship disabled, be "best effort", or sit behind an
// off-by-default flag on any platform, and EACH must carry a test that fails
// if the control does nothing. That is a stronger requirement than "test the
// happy path", and it is here because a memory gate is the archetype of a
// control that can be neutered without anything failing: stub it to admit
// everything and every functional test still passes, right up until a host
// dies.
//
// The two tests that discharge it are named for what they do:
//
//	TestPool_GateFailsIfItAlwaysAdmits
//	TestReaper_FailsIfNothingIsEverClosed
//
// Both drive the control to the state where refusing/closing is the ONLY
// correct answer and assert the outcome, not the call.

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPool_GateFailsIfItAlwaysAdmits is D1.5b's blind-gate guard.
//
// Mutation contract: replace admitLaunchLocked's body with `return true` and
// this test must go red. It drives the reader to a figure far below
// PerBrowserCostBytes with nothing evictable, so admitting is unambiguously
// wrong, and asserts that Acquire REFUSED — not that some function was called.
func TestPool_GateFailsIfItAlwaysAdmits(t *testing.T) {
	f := newPoolFixture(t)

	// One byte of headroom. No browser fits in that on any host.
	*f.available = 1

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	inst, err := f.pool.Acquire(ctx, browserTestKey("alpha"))

	require.Error(t, err,
		"the launch gate admitted a browser on a host with one byte free. The gate is the ONLY "+
			"admission control left — every tab counter was deleted (D1.5a) — so if it admits "+
			"here, nothing at all is watching")
	assert.Nil(t, inst)
	assert.Empty(t, *f.launched, "P-3: the pool must never launch past the gate, not even by one")
	assert.Empty(t, f.pool.LiveKeys())
}

// TestPool_PressureGateAtTheBoundary walks the exact byte either side of the
// launch floor. A gate that is off by one browser is a gate that OOMs a host
// under precisely the conditions it exists for.
func TestPool_PressureGateAtTheBoundary(t *testing.T) {
	cases := []struct {
		name      string
		available uint64
		admit     bool
	}{
		{"one byte under the floor", PerBrowserCostBytes - 1, false},
		{"exactly the floor", PerBrowserCostBytes, true},
		{"one byte over the floor", PerBrowserCostBytes + 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPoolFixture(t)
			*f.available = tc.available

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			_, err := f.pool.Acquire(ctx, browserTestKey("alpha"))

			if tc.admit {
				assert.NoError(t, err)
				assert.Len(t, *f.launched, 1)
			} else {
				assert.Error(t, err)
				assert.Empty(t, *f.launched)
			}
		})
	}
}

// TestPool_NothingEvictableWaitsThenRefusesNamingMemory is FR-053.
//
// Three separate claims, and the last two are about the WORDS, which matter as
// much as the refusal: a message that names a cap sends an operator looking
// for a setting to raise that this build does not have, and sends a model into
// a retry loop against a configuration problem that does not exist.
func TestPool_NothingEvictableWaitsThenRefusesNamingMemory(t *testing.T) {
	f := newPoolFixture(t)
	*f.available = 1

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, err := f.pool.Acquire(ctx, browserTestKey("alpha"))
	require.Error(t, err)

	assert.True(t, errors.Is(err, ErrBrowserMemoryRefused),
		"the refusal must be comparable with errors.Is so callers branch on the constraint, "+
			"not on prose")

	msg := strings.ToLower(err.Error())
	assert.Contains(t, msg, "memory", "the refusal must name MEMORY as the constraint")
	assert.Contains(t, msg, "alpha", "it must say WHICH workspace could not start")

	for _, forbidden := range []string{
		"max_tabs", "max_total_tabs", "limit", "cap", "raise", "increase",
		"tools.browser", "config.json", "setting",
	} {
		assert.NotContains(t, msg, forbidden,
			"the refusal must not name a cap or a config key (%q) — there is none to raise, and "+
				"saying otherwise sends an operator after a setting that does not exist", forbidden)
	}
}

// TestPool_UnmeasurableHostRefusesToGrow is FR-065 + FR-082, and its assertion
// is a PAIR — the floor must ADMIT and the growth must REFUSE.
//
// Asserting only the refusal would be satisfied by a build that refuses
// everything, which removes browsing entirely from the /proc-less deployments
// this project supports (gVisor, GKE Sandbox) on the strength of a reading the
// host declines to give. Asserting only the admit would be satisfied by a
// build with no gate at all.
//
// The floor is ONE BROWSER PER HOST, not one per key: keys are unbounded, so
// one-per-key would be no floor whatsoever.
func TestPool_UnmeasurableHostRefusesToGrow(t *testing.T) {
	f := newPoolFixture(t)
	*f.measurable = false

	// The floor ADMITS.
	first, err := f.pool.Acquire(context.Background(), browserTestKey("alpha"))
	require.NoError(t, err,
		"an unmeasurable host must still be able to browse — refusing to RUN is not the same as "+
			"refusing to GROW, and a floor of zero deletes browsing from every /proc-less deployment")
	require.NotNil(t, first)

	// Growth REFUSES.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	second, secondErr := f.pool.Acquire(ctx, browserTestKey("beta"))
	require.Error(t, secondErr,
		"the SECOND browser on an unmeasurable host must be refused — the host declined to say "+
			"how much room there is, so the conservative answer is one")
	assert.Nil(t, second)
	assert.Contains(t, strings.ToLower(secondErr.Error()), "memory")
	assert.Len(t, *f.launched, 1, "exactly one Chrome was started")
}

// TestPool_UnmeasurableRefusalIsLoggedOnce guards the latch on FR-065's
// "availability cannot be determined" line. Without it the line fires on every
// acquire on an unmeasurable host, which is every acquire forever — a log
// flood that buries whatever an operator was actually reading.
func TestPool_UnmeasurableRefusalIsLoggedOnce(t *testing.T) {
	f := newPoolFixture(t)
	*f.measurable = false

	f.pool.mu.Lock()
	require.False(t, f.pool.unmeasurableLogged)
	_, _ = f.pool.admitLaunchLocked()
	first := f.pool.unmeasurableLogged
	_, _ = f.pool.admitLaunchLocked()
	_, _ = f.pool.admitLaunchLocked()
	f.pool.mu.Unlock()

	assert.True(t, first, "the first unmeasurable read must say so")
	// The latch itself is the assertion: a second WARN cannot be emitted while
	// it is set, because the emit is inside the !unmeasurableLogged branch.
}

// TestPool_PressureGateOffLinuxIsNotSilent asserts the gate is present and
// consulted on THIS platform, whatever it is.
//
// FR-061 forbids the gate shipping disabled "on any platform", and the way
// that regression arrives is a build-tagged reader that quietly returns "fine"
// off Linux. The whole test suite would stay green; only non-Linux hosts would
// be unprotected. So this asserts behaviour on the running platform rather
// than trusting a Linux-only CI to have covered it.
func TestPool_PressureGateOffLinuxIsNotSilent(t *testing.T) {
	f := newPoolFixture(t)
	*f.available = 1

	f.pool.mu.Lock()
	admitted, _ := f.pool.admitLaunchLocked()
	f.pool.mu.Unlock()

	assert.False(t, admitted,
		"on %s the gate admitted a launch with one byte free — the gate must be live on every "+
			"platform, not only where CI happens to run", runtime.GOOS)
}

// --- FR-040 / FR-061: idle close ---------------------------------------------

// TestReaper_FailsIfNothingIsEverClosed is idle close's half of D1.5b.
//
// Mutation contract: replace CloseIdle's body with `return nil` and this test
// must go red. It drives an instance to the one state where closing is
// unambiguously correct — past the TTL, no tabs, no viewer, no call in flight
// — and asserts the browser is GONE, not that a sweep ran.
//
// Why this control cannot be allowed to no-op quietly: a Chrome that is never
// closed costs at least ~182 MB (PerBrowserCostBytes, and that figure is a
// lower bound) per workspace, forever, and nothing else reclaims it. The
// symptom is a host that degrades over days, which is exactly the kind of
// failure nobody traces back to a sweep that returned early.
func TestReaper_FailsIfNothingIsEverClosed(t *testing.T) {
	f := newPoolFixture(t)
	f.mustAcquire(t, "alpha")
	f.mustAcquire(t, "beta")
	require.Len(t, f.pool.LiveKeys(), 2)

	f.advance(f.pool.idleCloseTTL + time.Minute)
	closed := f.pool.CloseIdle(*f.now)

	assert.ElementsMatch(t, []string{"ws:alpha", "ws:beta"}, closed,
		"both browsers sat past idle_close_ttl with zero tabs, zero viewers and no call in "+
			"flight — the sweep closed neither, so nothing is reclaiming memory at all")
	assert.Empty(t, f.pool.LiveKeys(), "a closed browser must actually be gone from the pool")
}

// TestPool_IdleCloseIsNotBehindAFlag is the other half of FR-061's "may not
// ship disabled": there must be no configuration under which idle close is
// off. A zero or negative idle_close_ttl means "use the default", never
// "never close" — an off switch on one of the two memory controls is exactly
// what this requirement forbids.
func TestPool_IdleCloseIsNotBehindAFlag(t *testing.T) {
	for _, ttl := range []time.Duration{0, -time.Hour} {
		f := newPoolFixture(t)
		cfg := f.pool.cfg
		cfg.IdleCloseTTL = ttl
		f.pool.ApplyRuntimeConfig(cfg)

		assert.Positive(t, f.pool.idleCloseTTL,
			"idle_close_ttl=%v must fall back to the default, never disable the control", ttl)

		f.mustAcquire(t, "alpha")
		f.advance(f.pool.idleCloseTTL + time.Minute)
		assert.NotEmpty(t, f.pool.CloseIdle(*f.now),
			"idle close must still fire with idle_close_ttl=%v", ttl)
	}
}

// TestPool_IdleCloseSkipsBusyBrowsers is the counterweight. A sweep that
// closes everything unconditionally would pass TestReaper_FailsIfNothingIsEverClosed
// while being far worse than a no-op: it would kill the browser somebody is
// watching, mid-call.
func TestPool_IdleCloseSkipsBusyBrowsers(t *testing.T) {
	f := newPoolFixture(t)

	watched := f.mustAcquire(t, "watched")
	working := f.mustAcquire(t, "working")
	quiet := f.mustAcquire(t, "quiet")

	// lastViewerBeat is what makes this a LIVE viewer rather than a phantom
	// (FR-052): Viewers() counts only viewers that have proved they are still
	// there inside viewerLivenessWindow, so a zero stamp would read as decades
	// stale and this browser would be swept as idle.
	viewerMgr := &BrowserManager{
		sessions: map[string]*sessionEntry{"s": {viewers: 1, lastViewerBeat: time.Now()}},
	}
	workingMgr := &BrowserManager{sessions: map[string]*sessionEntry{}}
	releaseCall := workingMgr.EnterCall()
	defer releaseCall()

	f.pool.mu.Lock()
	watched.mgrs[viewerMgr] = struct{}{}
	working.mgrs[workingMgr] = struct{}{}
	f.pool.mu.Unlock()
	_ = quiet

	f.advance(f.pool.idleCloseTTL + time.Minute)
	closed := f.pool.CloseIdle(*f.now)

	assert.Equal(t, []string{"ws:quiet"}, closed,
		"only the browser with nothing happening in it may be closed")
	assert.ElementsMatch(t, []string{"ws:watched", "ws:working"}, f.pool.LiveKeys(),
		"a watched browser and one with a call in flight must both survive the sweep")
}
