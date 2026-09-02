// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// FR-075 / SC-027: ONE predicate, TWO deliberately different responses.
//
// The browser pool and agent admission read the same accessor and the same
// threshold, and then do different things with the answer:
//
//   - the POOL refuses to grow. On an unmeasurable host it runs ONE browser
//     per host and refuses the second.
//   - AGENT ADMISSION holds at a floor of TWO and refuses the third.
//
// Both are "refuse to grow, never refuse to run", at different floors, because
// the things being counted cost different amounts. One Chrome is ~182 MB
// measured; one agent turn is a goroutine and some context.
//
// THE ASSERTIONS MUST BE IN ONE TEST BODY, OFF ONE STUB. Split into two tests,
// each passes alone against a build that COLLAPSED the two responses into one —
// which is the specific regression this guards, and the only one that matters,
// because a build where both consumers refuse identically looks completely
// correct from either test's point of view.

// TestUnmeasurable_PoolRefusesWhileAgentsHoldAtFloor drives both consumers off
// one ok=false stub and asserts they respond DIFFERENTLY.
func TestUnmeasurable_PoolRefusesWhileAgentsHoldAtFloor(t *testing.T) {
	// ONE stub. Both halves below read through it; neither gets its own.
	restore := config.SetMemoryProviderForTest(
		func() (bool, bool) { return false, false },
		func() (uint64, bool) { return 0, false },
	)
	defer restore()
	resetMemoryAdmissionRefusalLogForTest()
	defer resetMemoryAdmissionRefusalLogForTest()

	// --- Half 1: the POOL's input. -------------------------------------
	//
	// The pool's launch gate is a BYTES question — "is there room for one more
	// Chrome" — and its answer on an unmeasurable host is that it cannot tell,
	// which it must treat as "no". Assert the accessor it reads, through the
	// same stub, reports exactly that.
	if bytes, ok := config.AvailableMemoryBytes(); ok {
		t.Fatalf("config.AvailableMemoryBytes() = (%d, true) off an ok=false stub — the pool's launch gate would admit a second Chrome on a host whose memory nothing has measured", bytes)
	}
	if _, ok := config.MemoryPressureHigh(); ok {
		t.Fatal("config.MemoryPressureHigh() reported ok=true off an ok=false stub — the tab-open gate would admit on a host whose memory nothing has measured")
	}

	// --- Half 2: AGENT ADMISSION's response. ---------------------------
	//
	// Same stub, same threshold, DIFFERENT response: two admitted, the third
	// refused naming memory.
	const configuredCapWellAboveTheFloor = 50
	a := newAdmissionController(configuredCapWellAboveTheFloor)

	for i := 1; i <= unmeasurableHostAgentFloor; i++ {
		admitted, reason, release := a.TryAdmitWithReason(scopeName(i))
		if !admitted {
			t.Fatalf("agent admission %d of %d was REFUSED (reason %q) off the same ok=false stub.\n"+
				"THE TWO RESPONSES HAVE COLLAPSED: agent admission has adopted the pool's refuse-to-grow-from-zero behaviour. "+
				"An agent turn is the product; a host that cannot report its memory must still run one.",
				i, unmeasurableHostAgentFloor, reason)
		}
		defer release()
	}

	third := unmeasurableHostAgentFloor + 1
	admitted, reason, _ := a.TryAdmitWithReason(scopeName(third))
	if admitted {
		t.Fatalf("agent admission %d SUCCEEDED off the same ok=false stub with a configured cap of %d.\n"+
			"THE TWO RESPONSES HAVE COLLAPSED THE OTHER WAY: agent admission is ignoring the shared predicate the pool obeys.",
			third, configuredCapWellAboveTheFloor)
	}
	if reason != config.ReasonMemoryPressure {
		t.Fatalf("agent admission %d was refused with reason %q, want %q — both consumers must emit the same code, or an operator grepping their log finds half the memory refusals in the process",
			third, reason, config.ReasonMemoryPressure)
	}

	// --- The difference itself, asserted explicitly. --------------------
	//
	// Half 1 says the pool cannot grow AT ALL on this host. Half 2 says agent
	// admission runs two. If a future change makes the agent floor 1, the two
	// responses have become one and this fails.
	if unmeasurableHostAgentFloor < 2 {
		t.Fatalf("unmeasurableHostAgentFloor = %d. The pool's unmeasurable-host floor is ONE browser per host; agent admission's is deliberately HIGHER, because an agent turn costs a goroutine and one Chrome costs ~182 MB. A floor of 1 here makes the two responses identical, which is the collapse FR-075 exists to prevent.",
			unmeasurableHostAgentFloor)
	}
}

// TestUnmeasurable_PairTestIsWiredToTheRealPoolOnceItExists is a
// SELF-INVALIDATING PLACEHOLDER GUARD, and it is here because the honest thing
// to do is say what this test cannot yet do.
//
// The browser pool (pkg/tools/browser/pool.go) does not exist at this commit —
// it lands in a later, gated wave. So half 1 above asserts the pool's INPUT
// (the shared accessor's answer through the shared stub) rather than the pool's
// own Acquire refusing. That is a real assertion — it is the thing the pool
// will read, and if it were wrong the pool would be wrong — but it is not the
// pool.
//
// This test FAILS the moment pool.go appears. That is deliberate: without it,
// the placeholder half quietly stays a placeholder forever, the suite stays
// green, and SC-027's "one stub, both consumers, one body" is satisfied on
// paper by a test that only ever drove one consumer. A failing test is the only
// reliable way to make a later change do something.
//
// TO FIX IT when the pool lands: replace half 1 of the test above with a real
// pool.Acquire call proving the SECOND browser is refused while the FIRST is
// admitted, off the same stub, then delete this guard.
func TestUnmeasurable_PairTestIsWiredToTheRealPoolOnceItExists(t *testing.T) {
	poolPath := filepath.Join("..", "tools", "browser", "pool.go")
	if _, err := os.Stat(poolPath); err == nil {
		t.Fatalf("%s now exists, so TestUnmeasurable_PoolRefusesWhileAgentsHoldAtFloor must stop asserting the pool's INPUT and start driving the pool itself.\n\n"+
			"Replace its half 1 with a real pool.Acquire proving the second browser on an unmeasurable host is refused while the first is admitted — off the SAME stub, in the SAME test body — then delete this guard.\n\n"+
			"Until that happens the pair test drives one consumer, not two, and SC-027 is satisfied only on paper.", poolPath)
	}
}
