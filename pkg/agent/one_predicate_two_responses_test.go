// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// FR-075 / SC-027: ONE predicate, TWO deliberately different responses.
//
// The browser pool and agent admission read the same accessor and the same
// threshold, and then do different things with the answer:
//
//   - the POOL refuses to grow. It will not start a browser it has no
//     measured room for — not even the FIRST one, from zero.
//   - AGENT ADMISSION holds at a floor of TWO and refuses the third, off the
//     same reading.
//
// Both are "refuse to grow, never refuse to run", at different floors, because
// the things being counted cost different amounts. One Chrome is ~182 MB
// assumed; one agent turn is a goroutine and some context.
//
// THE ASSERTIONS MUST BE IN ONE TEST BODY, OFF ONE STUB, AND BOTH HALVES MUST
// DRIVE THE REAL CONSUMER. Split into two tests, each passes alone against a
// build that COLLAPSED the two responses into one — which is the specific
// regression this guards, and the only one that matters, because a build where
// both consumers refuse identically looks completely correct from either
// test's point of view.

// gatePoolForTest returns a REAL *browser.BrowserPool whose launch gate reads
// the SHARED accessor — pool.availableMemory is left nil, so
// readAvailableMemory falls through to config.AvailableMemoryBytes and the
// stub installed by config.SetMemoryProviderForTest drives it.
//
// Its profile root is deliberately UNCREATABLE: cfg.ProfileDir sits under a
// path that is a regular FILE, so os.MkdirAll in the pool's launch step fails
// with ENOTDIR before a coordinator is ever built. Nothing here can spawn
// Chrome, resolve a binary or reach the network — which is what makes the
// admit branch observable in a unit test at all:
//
//	gate REFUSED   -> ErrBrowserMemoryRefused, and no profile directory is
//	                  ever attempted (the refusal happens before configFor).
//	gate ADMITTED  -> "cannot create profile directory", which is only
//	                  reachable PAST the gate.
//
// Two distinguishable errors, one real Acquire, no Chrome.
func gatePoolForTest(t *testing.T) *browser.BrowserPool {
	t.Helper()
	home := t.TempDir()
	blocker := filepath.Join(home, "not-a-directory")
	require.NoError(t, os.WriteFile(blocker, []byte("this is a file, so MkdirAll beneath it fails"), 0o600))
	return browser.NewBrowserPool(home, browser.BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: time.Second,
		ProfileDir:  filepath.Join(blocker, "profiles", "default"),
	})
}

// TestUnmeasurable_PoolRefusesWhileAgentsHoldAtFloor drives BOTH consumers —
// the real browser pool and the real agent admission controller — off one stub
// and asserts they respond DIFFERENTLY.
func TestUnmeasurable_PoolRefusesWhileAgentsHoldAtFloor(t *testing.T) {
	pool := gatePoolForTest(t)
	defer pool.Shutdown()
	key := browserTestKey(t, "one-predicate")

	// ================================================================
	// Scenario 1 — the host declines to report its memory at all.
	// ================================================================
	//
	// ONE stub. Both halves below read through it; neither gets its own.
	restore := config.SetMemoryProviderForTest(
		func() (bool, bool) { return false, false },
		func() (uint64, bool) { return 0, false },
	)
	resetMemoryAdmissionRefusalLogForTest()

	// --- Half 1: the REAL POOL, on an unmeasurable host. ---------------
	//
	// FR-082's floor is ONE BROWSER PER HOST: refuse to GROW, never refuse to
	// RUN. From zero browsers the gate must ADMIT, because a floor of zero
	// removes browsing entirely from the /proc-less deployments this project
	// supports (gVisor, GKE Sandbox) on the strength of a reading the host
	// declined to give.
	_, firstErr := pool.Acquire(context.Background(), key)
	require.Error(t, firstErr, "the fixture's profile root is uncreatable, so a launch that got past the gate must still fail")
	require.False(t, errors.Is(firstErr, browser.ErrBrowserMemoryRefused),
		"the pool REFUSED its first browser on an unmeasurable host (%v).\n"+
			"THE TWO RESPONSES HAVE COLLAPSED: the pool has adopted refuse-to-RUN. FR-082's floor is one browser "+
			"per host — an install that cannot read /proc must still be able to browse.", firstErr)
	require.Contains(t, firstErr.Error(), "profile directory",
		"expected the past-the-gate failure (%v) — if this is some other error the admit branch is no longer what is being observed", firstErr)

	// --- Half 2: AGENT ADMISSION, same stub. ---------------------------
	const configuredCapWellAboveTheFloor = 50
	assertAdmissionHoldsAtItsFloor(t, newAdmissionController(configuredCapWellAboveTheFloor),
		"on an unmeasurable host")

	restore()

	// ================================================================
	// Scenario 2 — the host IS measurable, and it is short. THE DIFFERENCE.
	// ================================================================
	//
	// Same one stub mechanism, one reading, and now the two consumers must
	// visibly disagree: from ZERO the pool refuses outright, while agent
	// admission still runs two turns.
	restore2 := config.SetMemoryProviderForTest(
		func() (bool, bool) { return true, true },
		func() (uint64, bool) { return browser.PerBrowserCostBytes - 1, true },
	)
	defer restore2()
	resetMemoryAdmissionRefusalLogForTest()
	defer resetMemoryAdmissionRefusalLogForTest()

	// --- Half 1: the REAL POOL refuses from ZERO. ----------------------
	//
	// One byte short of the launch headroom is a refusal, and the refusal is
	// the NAMED one (FR-053) so a caller can branch on it without parsing
	// prose. If this admits, the gate is a no-op or it has adopted agent
	// admission's floor-of-two — the collapse, in the other direction.
	_, refusedErr := pool.Acquire(context.Background(), key)
	require.Error(t, refusedErr)
	require.ErrorIs(t, refusedErr, browser.ErrBrowserMemoryRefused,
		"the pool ADMITTED a browser one byte below its own launch headroom (err=%v).\n"+
			"THE TWO RESPONSES HAVE COLLAPSED: the pool is no longer refusing to grow at zero, "+
			"which is the only admission control at this level — every tab counter was deleted (ADR-072 D1.5a).", refusedErr)
	require.NotContains(t, refusedErr.Error(), "profile directory",
		"the refusal must happen BEFORE the launch is attempted, not after")
	require.Empty(t, pool.LiveKeys(), "a refused acquire must leave no live browser behind")

	// --- Half 2: AGENT ADMISSION, off that SAME short reading. ---------
	//
	// Not a refusal. Two turns run. An agent turn is the product; the pool's
	// answer to "no room" is zero browsers and admission's is two turns, and
	// that difference is the whole of FR-075.
	assertAdmissionHoldsAtItsFloor(t, newAdmissionController(configuredCapWellAboveTheFloor),
		"on a measured, memory-short host")

	// --- The difference itself, asserted explicitly. --------------------
	//
	// Half 1 says the pool cannot grow AT ALL on this host. Half 2 says agent
	// admission runs two. If a future change makes the agent floor 1, the two
	// responses have become one and this fails.
	if unmeasurableHostAgentFloor < 2 {
		t.Fatalf("unmeasurableHostAgentFloor = %d. The pool's floor on a host with no room is ZERO browsers; agent admission's is deliberately HIGHER, because an agent turn costs a goroutine and one Chrome costs ~182 MB. A floor of 1 here makes the two responses identical, which is the collapse FR-075 exists to prevent.",
			unmeasurableHostAgentFloor)
	}
}

// assertAdmissionHoldsAtItsFloor drives the REAL admission controller through
// its floor and one past it, off whatever memory stub is currently installed.
func assertAdmissionHoldsAtItsFloor(t *testing.T, a *AdmissionController, situation string) {
	t.Helper()

	for i := 1; i <= unmeasurableHostAgentFloor; i++ {
		admitted, reason, release := a.TryAdmitWithReason(scopeName(i))
		if !admitted {
			t.Fatalf("agent admission %d of %d was REFUSED (reason %q) %s.\n"+
				"THE TWO RESPONSES HAVE COLLAPSED: agent admission has adopted the pool's refuse-to-grow-from-zero behaviour. "+
				"An agent turn is the product; a host short of memory must still run one.",
				i, unmeasurableHostAgentFloor, reason, situation)
		}
		defer release()
	}

	third := unmeasurableHostAgentFloor + 1
	admitted, reason, _ := a.TryAdmitWithReason(scopeName(third))
	if admitted {
		t.Fatalf("agent admission %d SUCCEEDED %s with a configured cap well above the floor.\n"+
			"THE TWO RESPONSES HAVE COLLAPSED THE OTHER WAY: agent admission is ignoring the shared predicate the pool obeys.",
			third, situation)
	}
	if reason != config.ReasonMemoryPressure {
		t.Fatalf("agent admission %d was refused with reason %q, want %q — both consumers must emit the same code, or an operator grepping their log finds half the memory refusals in the process",
			third, reason, config.ReasonMemoryPressure)
	}
	if !strings.EqualFold(config.ReasonMemoryPressure, browser.ReasonMemoryPressure) {
		t.Fatalf("agent admission refuses with %q and the browser pool with %q — one constraint must carry one code",
			config.ReasonMemoryPressure, browser.ReasonMemoryPressure)
	}
}

// ---------------------------------------------------------------------------
// The self-invalidating placeholder guard that used to live here is GONE, and
// this note is what replaces it so the deletion is not mistaken for the
// shortcut it was designed to catch.
//
// Its condition is DISCHARGED. It demanded that half 1 above "stop asserting
// the pool's INPUT and start driving the pool itself", and half 1 now calls
// browser.NewBrowserPool and makes real pool.Acquire calls off the same
// config memory stub agent admission reads.
//
// Its literal wording additionally demanded a single body proving the SECOND
// browser is refused while the FIRST is admitted. That assertion cannot be
// written from pkg/agent at all: seeding a live instance without launching
// Chrome needs BrowserPool.newCoordinator, which is unexported, and package
// browser cannot host the test instead because agent -> browser is a real
// import cycle. It IS covered, in the package where it can be —
// TestPool_UnmeasurableHostRefusesToGrow in pkg/tools/browser.
//
// What half 1 proves and that test cannot is the thing SC-027 is actually
// about: both consumers read ONE accessor.
//
// A guard whose condition is met, and whose residual wording is unsatisfiable
// by construction, is no longer a guard — it is a permanently red test for a
// condition that is no longer true.
// ---------------------------------------------------------------------------
