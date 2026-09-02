// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// admission_adr057_test.go — ADR-057 unit U19's tests for W17 (FR-069,
// FR-070, FR-095; BDD-75, BDD-76, BDD-77, BDD-108). Per binding Rule 5 this
// is a NEW file; per binding Rule 6 its unexported helpers are prefixed u19.
//
// Per binding Rule 1, every assertion below runs against the REAL
// RootDelegationAdmission/ResolveRootDelegationCap primitives (admission.go)
// and, for the nested-gating regression, a REAL *AgentLoop and a REAL
// spawnSubTurn call — never a spy that records its argument and returns
// success.
package agent

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- FR-095 / test #110: cap sourced from subturn.max_concurrent, unclamped ---

// TestRootDelegationCap_SourcedFromSubTurnMaxConcurrent is test #110
// (BDD-75): the resolved cap comes from agents.defaults.subturn.max_concurrent
// UNCLAMPED, and specifically NOT from Performance.EffectiveMaxParallelAgents()
// (which clampParallelExplicit hard-caps at 16). An operator-set 24 must
// survive resolution intact.
func TestRootDelegationCap_SourcedFromSubTurnMaxConcurrent(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "") // keep the env override inert

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				SubTurn: config.SubTurnConfig{MaxConcurrent: 24},
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
		Performance: config.PerformanceConfig{
			// Deliberately a DIFFERENT number, and one clampParallelExplicit
			// would treat differently (8, not 16), so a resolver that
			// accidentally routed through EffectiveMaxParallelAgents() would
			// be caught returning 8, not silently agreeing with 24 by
			// coincidence.
			MaxParallelAgents: 8,
		},
	}

	got, err := ResolveRootDelegationCap(cfg)
	if err != nil {
		t.Fatalf("ResolveRootDelegationCap: unexpected error: %v", err)
	}
	if got != 24 {
		t.Fatalf("ResolveRootDelegationCap = %d, want 24 (the unclamped subturn.max_concurrent value)", got)
	}
	if eff, _ := cfg.Performance.EffectiveMaxParallelAgents(); got == eff {
		t.Fatalf("resolved cap (%d) equals EffectiveMaxParallelAgents() (%d) — "+
			"this assertion is supposed to distinguish the two paths; the test fixture must set them differently", got, eff)
	}
}

// TestRootDelegationCap_NegativeIsBootError asserts the remaining half of
// FR-095 post-consolidation (2026-08-04, commit 536b7340's follow-up fix):
// only a NEGATIVE configured value is a boot-time configuration error, never
// silently reinterpreted as "no gate" (the ADR-037 anti-pattern this project
// bans). Zero is intentionally EXCLUDED from this table now — see
// TestRootDelegationCap_UnsetResolvesToCentralValue — because 0 means
// "unset", not "misconfigured".
func TestRootDelegationCap_NegativeIsBootError(t *testing.T) {
	for _, v := range []int{-1, -100} {
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					SubTurn: config.SubTurnConfig{MaxConcurrent: v},
				},
				List: []config.AgentConfig{{ID: "mia"}},
			},
		}
		_, err := ResolveRootDelegationCap(cfg)
		if err == nil {
			t.Fatalf("MaxConcurrent=%d: ResolveRootDelegationCap returned nil error, want ErrRootDelegationCapMisconfigured", v)
		}
		if !errors.Is(err, ErrRootDelegationCapMisconfigured) {
			t.Fatalf("MaxConcurrent=%d: error = %v, want errors.Is(_, ErrRootDelegationCapMisconfigured)", v, err)
		}
	}
}

// TestRootDelegationCap_UnsetResolvesToCentralValue is the 2026-08-04
// consolidation's DELIBERATE INVERSION of the old
// "MisconfiguredIsBootError"'s v==0 case: FR-095 originally treated
// MaxConcurrent<=0 (including exactly 0) as a boot-time error, seeded away
// on a fresh install by a fixed 16. That premise depended on
// Performance.EffectiveMaxParallelAgents() ALSO being hard-clamped to 16 at
// the time (clampParallelExplicit); commit 536b7340 removed that ceiling,
// so treating 0 as an error (or seeding around it) would leave a second,
// independent cap. 0 (unset) is now a VALID, common configuration meaning
// "inherit the central authority" — no error, no coercion to a fixed number.
func TestRootDelegationCap_UnsetResolvesToCentralValue(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				SubTurn: config.SubTurnConfig{MaxConcurrent: 0},
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 40},
	}
	got, err := ResolveRootDelegationCap(cfg)
	if err != nil {
		t.Fatalf("MaxConcurrent=0: ResolveRootDelegationCap returned an error, want nil (0 means 'unset', not misconfigured): %v", err)
	}
	if got != 40 {
		t.Fatalf("ResolveRootDelegationCap = %d, want 40 (cfg.Performance.EffectiveMaxParallelAgents(), the central authority)", got)
	}
}

// --- FR-095/M2-1 / test #112, AMENDED 2026-08-04: the unset (fresh-install) case now inherits the central value ---

// TestRootDelegationCap_DefaultInstallInheritsCentralValue is the
// consolidation's replacement for the retired
// TestRootDelegationCap_DefaultInstallIsGatedAt16 (test #112 / BDD-108 as
// originally written). That test asserted config.DefaultConfig() seeds the
// root-delegation cap at a fixed 16 REGARDLESS of the central
// max_parallel_agents setting — exactly the "control that moves, persists
// and governs nothing" outcome this consolidation exists to close, once
// Performance.EffectiveMaxParallelAgents() stopped being hard-clamped to 16
// (commit 536b7340). This test proves the corrected property instead: no
// operator override anywhere — config.DefaultConfig(), exactly what a fresh
// install ships — resolves to whatever the CENTRAL Performance.
// EffectiveMaxParallelAgents() value is, and the gate built from it is
// ACTIVE at that value (the N+1th concurrent root delegation is refused,
// proving the gate is a real cap and not "no gate").
func TestRootDelegationCap_DefaultInstallInheritsCentralValue(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")

	cfg := config.DefaultConfig()

	if cfg.Agents.Defaults.SubTurn.MaxConcurrent != 0 {
		t.Fatalf("config.DefaultConfig().Agents.Defaults.SubTurn.MaxConcurrent = %d, want 0 (unset — the seed was removed)",
			cfg.Agents.Defaults.SubTurn.MaxConcurrent)
	}
	// Pin the central value explicitly so this test is deterministic across
	// hardware, rather than depending on this machine's auto-detected default.
	cfg.Performance.MaxParallelAgents = 40

	rootCap, err := ResolveRootDelegationCap(cfg)
	if err != nil {
		t.Fatalf("ResolveRootDelegationCap on a fresh-install config: unexpected error: %v", err)
	}
	if want, _ := cfg.Performance.EffectiveMaxParallelAgents(); rootCap != want {
		t.Fatalf("ResolveRootDelegationCap = %d, want %d (cfg.Performance.EffectiveMaxParallelAgents(), the central authority)", rootCap, want)
	}
	if rootCap != 40 {
		t.Fatalf("ResolveRootDelegationCap = %d, want 40 (the pinned central value) — a fresh install must NOT be gated at any fixed number", rootCap)
	}

	gate := NewRootDelegationAdmission(rootCap)
	var releases []func()
	for i := 0; i < rootCap; i++ {
		ok, release := gate.TryAdmit()
		if !ok {
			t.Fatalf("admission %d/%d refused on a fresh-install config — the gate must admit up to the central-value cap", i+1, rootCap)
		}
		releases = append(releases, release)
	}
	// The (N+1)th is refused — the gate is ACTIVE at the central value on an
	// untouched default, not "no gate" (BDD-108's central assertion, now
	// amended to the central-value cap rather than a fixed 16).
	if ok, _ := gate.TryAdmit(); ok {
		t.Fatalf("concurrent root delegation #%d was admitted on a fresh-install (cap=%d) config — BDD-108 requires it refused", rootCap+1, rootCap)
	}
	for _, r := range releases {
		r()
	}
	if gate.Active() != 0 {
		t.Fatalf("Active() = %d after releasing all %d slots, want 0", gate.Active(), rootCap)
	}
}

// --- FR-069/FR-077 / test #63: refuses immediately, does not queue, operator-visible ---

// TestRootDelegationAdmission_RefusesNotQueues is test #63 (BDD-75, BDD-77):
// with N admitted, the N+1th is refused — not blocked/queued — and the
// refusal is operator-visible (an ErrorResult naming the cap, plus an
// slog.Error record naming the cap/delegating agent/target, mirroring
// pkg/tools/delegate.go:1150-1159's existing shape).
func TestRootDelegationAdmission_RefusesNotQueues(t *testing.T) {
	const rootCap = 24
	gate := NewRootDelegationAdmission(rootCap)

	for i := 0; i < rootCap; i++ {
		ok, release := gate.TryAdmit()
		if !ok {
			t.Fatalf("admission %d/%d unexpectedly refused", i+1, rootCap)
		}
		t.Cleanup(release)
	}
	if gate.Active() != rootCap {
		t.Fatalf("Active() = %d, want %d after admitting to cap", gate.Active(), rootCap)
	}

	// BDD-75's "But it is not queued behind the session-store lock": TryAdmit
	// on the 25th call must return IMMEDIATELY (non-blocking), not wait for a
	// slot. Race the call against a short timer on its own goroutine — a
	// queueing implementation would still be blocked when the timer fires.
	admitted := make(chan bool, 1)
	go func() {
		ok, _ := gate.TryAdmit()
		admitted <- ok
	}()
	select {
	case ok := <-admitted:
		if ok {
			t.Fatal("25th concurrent root delegation was admitted at cap=24 — want refused")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("TryAdmit for the 25th delegation did not return within 500ms — it appears to be QUEUEING, which BDD-75 forbids")
	}

	// Operator-visible refusal shape (BDD-77): an ErrorResult naming the rootCap,
	// returned to the calling agent.
	res := RefuseRootDelegation(rootCap, "delegating-agent-1", "target-agent-1")
	if res == nil {
		t.Fatal("RefuseRootDelegation returned nil")
	}
	if !res.IsError {
		t.Fatal("RefuseRootDelegation's ToolResult does not report IsError")
	}
	if !strings.Contains(res.ForLLM, strconv.Itoa(rootCap)) {
		t.Fatalf("RefuseRootDelegation's error content %q does not name the cap (%d)", res.ForLLM, rootCap)
	}
}

// --- FR-070 / test #64: nested (child-level) gating is unchanged ---

// TestNestedDelegationGating_Unchanged is test #64 (BDD-76): with the
// root-level gate saturated (cap fully consumed), a CHILD-level delegation's
// existing concurrencySem behaviour is unaffected — RootDelegationAdmission
// is a wholly separate counter that spawnSubTurn (subturn.go, untouched by
// this unit) never reads, writes, or is gated by. This is proven by driving
// a REAL spawnSubTurn call — the same production entry point
// fanout_cap_test.go's TestSubTurnFanOutCap_ChildSemaphoreCapacity exercises
// — while the root gate reports zero remaining capacity.
func TestNestedDelegationGating_Unchanged(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")

	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	const childCap = 4
	al.cfg.Performance.MaxParallelAgents = childCap
	al.cfg.Agents.Defaults.SubTurn.MaxConcurrent = 0 // unset -> child-level fan-out follows MaxParallelAgents (unrelated to the root gate)

	// Saturate a root-level gate completely (cap=1, one slot taken, zero
	// remaining) BEFORE driving the nested spawn below, so if spawnSubTurn
	// were (incorrectly) coupled to it, the spawn would fail.
	rootGate := NewRootDelegationAdmission(1)
	ok, release := rootGate.TryAdmit()
	if !ok {
		t.Fatal("could not saturate the root gate for this test's precondition")
	}
	defer release()
	if rootGate.Active() != rootGate.Cap() {
		t.Fatalf("root gate not fully saturated: active=%d cap=%d", rootGate.Active(), rootGate.Cap())
	}

	// spawnSubTurn's real production path (pkg/agent/subturn.go) mints the
	// child via al.GetSessionStore().CreateSessionWithID(childID,
	// parentTS.transcriptSessionID, ...) — FR-082 requires reading the
	// PARENT's own meta.json to copy its Owner field, so the parent must be
	// a REAL, store-backed session, not a bare id string. Minting one for
	// real here (rather than a placeholder like fanout_cap_test.go's, which
	// leaves transcriptSessionID unset) is what makes this test independent
	// of that file's own separate, pre-existing failure.
	sharedStore := al.GetSessionStore()
	require.NotNil(t, sharedStore, "AgentLoop has no shared session store")
	parentMeta, err := sharedStore.NewSession(session.SessionTypeChannel, "test-channel", "main")
	require.NoError(t, err, "mint a real parent session")

	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "u19-nested-parent",
		depth:               0,
		pendingResults:      make(chan *tools.ToolResult, 8),
		session:             &ephemeralSessionStore{},
		concurrencySem:      make(chan struct{}, childCap),
		transcriptSessionID: parentMeta.ID,
		routingSessionID:    session.RoutingSessionID(parentMeta.ID),
	}

	rtCfg := al.getSubTurnConfig()
	if rtCfg.maxConcurrent != childCap {
		t.Fatalf("resolved child-level cap = %d, want %d — precondition for this test is unmet", rtCfg.maxConcurrent, childCap)
	}

	// DelegateSessionID is set explicitly (rather than left empty, which
	// falls back to al.generateSubTurnID()'s "subturn-<counter>" sequence —
	// subturn.go:448-450) because al.GetSessionStore()'s shared directory is
	// derived from filepath.Dir(cfg.AgentHomeBasePath()) (loop.go:723-724),
	// and newTestAgentLoop's per-test tmpDir already collapses to the SAME
	// parent directory across every test in this package (verified: os.
	// MkdirTemp("", "agent-test-*") always lands directly under the shared
	// system temp root, so filepath.Dir(tmpDir) is identical for every
	// caller) — a pre-existing test-harness property this unit does not own
	// or touch. Any two tests that both let the counter default risk
	// colliding on literally the same "subturn-1" directory on a shared
	// disk across separate test runs. A random, test-scoped id sidesteps it
	// entirely without needing to fix (or even touch) that shared harness.
	cfg := SubTurnConfig{Model: "gpt-4o-mini", Tools: []tools.Tool{}, DelegateSessionID: "u19-nested-child-" + uuid.NewString()}
	if _, err := spawnSubTurn(context.Background(), al, parent, cfg); err != nil {
		t.Fatalf("spawnSubTurn: %v (nested delegation must succeed regardless of the SATURATED root gate)", err)
	}
	if cap(parent.concurrencySem) != childCap {
		t.Fatalf("parent semaphore capacity = %d, want %d — nested gating changed", cap(parent.concurrencySem), childCap)
	}

	// And the converse: driving the nested spawn did not touch the root
	// gate's own counter — the two are fully decoupled.
	if rootGate.Active() != 1 {
		t.Fatalf("root gate Active() = %d after a nested spawn, want 1 (unchanged) — "+
			"a nested delegation must never consume a root-level admission slot", rootGate.Active())
	}
}

// --- Concurrency safety of the primitive itself ---

// TestRootDelegationAdmission_ConcurrentAdmitNeverExceedsCap hammers TryAdmit
// from many goroutines at once and asserts the admitted count never exceeds
// cap at any observed point — the core safety property FR-069's refusal
// depends on.
//
// REGRESSION NOTE (2026-08-06): the original version of this test had every
// admitted goroutine time.Sleep(1ms) and then call its own release()
// WHILE the other 199 goroutines were still being scheduled, then asserted
// admittedCount == rootCap (exact equality, total across the whole run).
// That is not the safety property FR-069 requires (the doc comment above
// only ever promised "never exceeds cap at any observed point", i.e. an
// upper bound on concurrently-active admits — the same invariant
// TestAdmissionController_TryAdmit_NoOvercommit checks for the sibling
// AdmissionController primitive). On a CPU-constrained runner under -race
// (race-detector instrumentation plus 4 vCPUs on GitHub's ubuntu-latest,
// contending with the rest of this package's ~213s parallel suite), 200
// goroutines do not all get an OS thread inside 1ms: some of the first 8
// admits legitimately sleep, release, and free their slot before the
// remaining goroutines have even reached their first TryAdmit call, letting
// a 9th/10th/13th caller be admitted after a real release — which is a
// semaphore working exactly as designed, not an overcommit. Reproduced
// locally 3/30 runs under `GOMAXPROCS=4 -race -count=30`
// (admittedCount observed at 10 and 13 twice), while a diagnostic patch
// logging maxObservedActive alongside admittedCount showed
// maxObservedActive pinned at exactly rootCap (8) on all 40 additional
// runs — i.e. the actual safety property never broke; only the test's
// stricter, timing-dependent extra assertion did.
//
// Fixed by removing the in-goroutine sleep+release entirely: every admitted
// caller holds its slot until the whole burst has finished attempting
// admission (mirroring TestAdmissionController_TryAdmit_NoOvercommit's
// "collect releases, call them all after wg.Wait()" shape), so no slot can
// ever free up mid-burst. With that, EXACTLY rootCap of the 200 concurrent
// attempts can succeed regardless of scheduling — the equality assertion is
// now a deterministic consequence of the design, not a timing bet. A
// startGate channel additionally makes the "all 200 attempt admission at
// once" intent explicit rather than relying on goroutine-launch ordering.
func TestRootDelegationAdmission_ConcurrentAdmitNeverExceedsCap(t *testing.T) {
	const rootCap = 8
	const attempts = 200
	gate := NewRootDelegationAdmission(rootCap)

	var (
		wg                sync.WaitGroup
		startGate         = make(chan struct{})
		mu                sync.Mutex
		admittedCount     int
		maxObservedActive int
		releases          []func()
	)

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startGate
			ok, release := gate.TryAdmit()
			if !ok {
				return
			}
			mu.Lock()
			admittedCount++
			if a := gate.Active(); a > maxObservedActive {
				maxObservedActive = a
			}
			releases = append(releases, release)
			mu.Unlock()
		}()
	}
	close(startGate) // unleash all 200 attempts simultaneously
	wg.Wait()

	if admittedCount != rootCap {
		t.Fatalf("total admitted across %d concurrent attempts = %d, want exactly %d (cap)", attempts, admittedCount, rootCap)
	}
	if maxObservedActive > rootCap {
		t.Fatalf("observed Active() = %d, exceeds cap %d", maxObservedActive, rootCap)
	}

	// Only now — after every attempt has resolved — release the admitted
	// slots. Releasing earlier (e.g. from inside each goroutine) would let a
	// still-pending attempt be admitted into a freed slot, which is correct
	// semaphore behaviour but would make admittedCount's total exceed
	// rootCap for reasons unrelated to the safety property under test.
	for _, release := range releases {
		release()
	}
	if gate.Active() != 0 {
		t.Fatalf("Active() = %d after every admitted caller released, want 0", gate.Active())
	}
}
