// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1725 round-3 requirement 2: a live engine may admit plans while the real
// reload replaces the /loop scheduler. Its boot-installed counter must be
// race-free. Run this exact test with CGO_ENABLED=1 and -race; the detector,
// not a synthetic test-side read of services.LoopScheduler, supplies RED.
func TestPlanEngine_LoopCounterRaceFreeDuringReload(t *testing.T) {
	h := newPlanReload1725Harness(t, false, false)
	require.NotNil(t, h.rs.LoopScheduler, "BLOCKED: boot did not install the loop scheduler — required by #1725 round 3")
	// One enabled job is the exact count oracle. It is due a day later so this
	// counter fixture never launches a scheduled model turn or needs a session.
	jobID, err := h.rs.LoopScheduler.AddInterval(planReload1725Owner, "counter-reload-1725", (24 * time.Hour).Milliseconds())
	require.NoError(t, err)
	require.NotEmpty(t, jobID, "setup: persist a real enabled loop job")
	admitted, active, capOut := h.engine.Admit("plan")
	require.True(t, admitted, "instrument: a single loop must leave capacity for a plan")
	require.Equal(t, 1, active, "instrument: real admission must invoke the boot-installed loop counter")
	require.Greater(t, capOut, active, "instrument: admission must have a usable global cap")

	stopReads := make(chan struct{})
	readsDone := make(chan struct{})
	readsStarted := make(chan struct{})
	var stopOnce sync.Once
	var admissions atomic.Uint64
	stopReader := func() {
		stopOnce.Do(func() { close(stopReads) })
		waitPlanReload1725(t, readsDone, "admission reader shutdown")
	}
	go func() {
		defer close(readsDone)
		for {
			select {
			case <-stopReads:
				return
			default:
			}
			// This traverses Admit -> computeActiveLocked -> the original
			// gateway boot closures, including the actual "loop" counter.
			h.engine.Admit("plan")
			if admissions.Add(1) == 1 {
				close(readsStarted)
			}
		}
	}()
	// This runs before harness cleanup: no reader may outlive real service
	// teardown or temporary storage, including on a failed reload assertion.
	t.Cleanup(stopReader)
	waitPlanReload1725(t, readsStarted, "first real concurrent admission")

	// Keep reads continuous through multiple actual pointer replacements,
	// rather than gambling on one short write window. There is no per-read
	// test lock/channel pairing with the reload's write. Eight is a fixture
	// stress count, not a product limit; external serial runs measure how
	// reliably the detector reports the existing race.
	const reloads = 8
	for i := range reloads {
		before := admissions.Load()
		result := h.beginReload()
		waitPlanReload1725(t, result.done, "real configuration reload during admission")
		require.NoError(t, result.err, "real reload %d must succeed", i+1)
		h.assertSameEngine(t)
		after := admissions.Load()
		t.Logf("real reload %d/%d completed with continuous admissions: before=%d after=%d", i+1, reloads, before, after)
	}
	stopReader()
	// Read final state only after the original reload and the admission
	// goroutine have both joined. These exact counts positively exercise the
	// counter; absence of a race report alone is not the fixture's only check.
	assert.Greater(t, admissions.Load(), uint64(1), "instrument: the real admission path must be exercised repeatedly")
	admitted, active, capOut = h.engine.Admit("plan")
	assert.True(t, admitted, "one surviving loop must still leave plan capacity after reload")
	assert.Equal(t, 1, active, "the real loop counter must still count the persisted job after reload")
	assert.Greater(t, capOut, active)
	t.Logf("completed %d real reloads and %d production admissions; exact loop count remained one", reloads, admissions.Load())
}
