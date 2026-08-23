package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// TestRootDelegationAdmission_UnsetInheritsCentralValueLive is the
// 2026-08-04 concurrency-gate consolidation replacement for the old
// "FailsClosedOnUnresolvableCap" test, which pinned SubTurn.MaxConcurrent's
// ZERO value as the FR-095 error branch. It no longer is: 0 (unset — the
// shipped default, see DefaultConfig/defaults.go, the seeded 16 was removed)
// now means "inherit the central Performance.EffectiveMaxParallelAgents()
// authority" — the same value session admission and TaskExecutor's dispatch
// semaphore resolve to. This test proves that inheritance AND proves it is
// resolved LIVE, not frozen at AgentLoop construction (the "boot-frozen
// Cap()" defect this fix repairs): changing cfg.Performance.MaxParallelAgents
// after construction must move al.rootDelegationAdmission.Cap() with it, with
// no restart and no explicit resize call.
func TestRootDelegationAdmission_UnsetInheritsCentralValueLive(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "") // keep the unrelated env override inert

	// SubTurn is left at its zero value (MaxConcurrent == 0, "unset"); a
	// distinct, explicit Performance.MaxParallelAgents makes the assertion
	// deterministic regardless of this machine's hardware-autodetected value.
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 40},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	require.NotNil(t, al.rootDelegationAdmission, "the gate must always be constructed, never nil")
	require.Equal(t, 40, al.rootDelegationAdmission.Cap(),
		"an unset subturn.max_concurrent must inherit the CENTRAL Performance.EffectiveMaxParallelAgents() value")

	// Liveness: raise the central value after construction and confirm
	// Cap() picks it up on the very next call, with no restart and no
	// explicit resize call — proving this is genuinely live-resolved
	// (newRootDelegationAdmissionWithResolver), not the old fixed cap
	// frozen once at NewAgentLoop time.
	al.cfg.Performance.MaxParallelAgents = 90
	require.Equal(t, 90, al.rootDelegationAdmission.Cap(),
		"Cap() must re-resolve the central value live — a boot-frozen cap would still read 40 here")
	al.cfg.Performance.MaxParallelAgents = 40 // restore for the rest of this test

	// Positive lower bound (Binding Rule 4): a non-nil gate at the right Cap()
	// still proves nothing unless it actually REFUSES. Saturate it and assert
	// the next admit is denied — without this, a gate that admits everything
	// would satisfy the assertions above.
	const want = 40
	releases := make([]func(), 0, want)
	for i := 0; i < want; i++ {
		admitted, release := al.rootDelegationAdmission.TryAdmit()
		require.Truef(t, admitted,
			"admit %d of %d must succeed while the gate has capacity", i+1, want)
		releases = append(releases, release)
	}
	saturated, _ := al.rootDelegationAdmission.TryAdmit()
	require.False(t, saturated,
		"the gate must refuse once saturated — otherwise it is present but inert")

	// And it must admit again once a slot frees, so the refusal above is the
	// cap doing its job rather than the gate being stuck closed.
	releases[0]()
	readmitted, release := al.rootDelegationAdmission.TryAdmit()
	require.True(t, readmitted, "the gate must admit again after a slot is released")
	release()
	for _, r := range releases[1:] {
		r()
	}
}

// TestRootDelegationAdmission_NegativeConfigFailsClosedToCentralValue pins
// the ACTUAL remaining FR-095 fail-closed contract post-consolidation: only a
// NEGATIVE agents.defaults.subturn.max_concurrent is a genuine configuration
// error (ResolveRootDelegationCap), and the gate must still be constructed —
// never nil — falling back to the SAME central Performance.
// EffectiveMaxParallelAgents() authority every other branch uses, not a
// second, independent hardcoded number.
//
// Why this exists: an earlier wiring left al.rootDelegationAdmission NIL when
// the cap could not be resolved, and rootDelegationAdmittingSpawner's nil-gate
// check then degrades to pass-through — i.e. UNLIMITED root-level delegate()
// fan-out. That is the "silently reinterpreted as no gate" outcome FR-095
// names as the banned ADR-037 anti-pattern, and it is invisible at runtime:
// nothing refuses, nothing logs at the dispatch site, and the operator sees a
// working system right up until a wide fan-out saturates their provider.
func TestRootDelegationAdmission_NegativeConfigFailsClosedToCentralValue(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
				SubTurn:           config.SubTurnConfig{MaxConcurrent: -1}, // genuine misconfiguration
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 40},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	require.NotNil(t, al.rootDelegationAdmission,
		"an unresolvable (negative) root-delegation cap MUST still construct a gate — a nil gate "+
			"is pass-through, i.e. unlimited root fan-out (FR-095's banned 'no gate' outcome)")
	require.Equal(t, 40, al.rootDelegationAdmission.Cap(),
		"the failure branch must fall back to the SAME central Performance.EffectiveMaxParallelAgents() value, not a separate hardcoded default")

	// Positive lower bound (Binding Rule 4), same shape as the sibling test.
	releases := make([]func(), 0, 40)
	for i := 0; i < 40; i++ {
		admitted, release := al.rootDelegationAdmission.TryAdmit()
		require.Truef(t, admitted, "admit %d of 40 must succeed while the gate has capacity", i+1)
		releases = append(releases, release)
	}
	saturated, _ := al.rootDelegationAdmission.TryAdmit()
	require.False(t, saturated, "the gate must refuse once saturated — otherwise it is present but inert")

	releases[0]()
	readmitted, release := al.rootDelegationAdmission.TryAdmit()
	require.True(t, readmitted, "the gate must admit again after a slot is released")
	release()
	for _, r := range releases[1:] {
		r()
	}
}

// TestRootDelegationAdmission_ValidOverrideIsNotCoerced is the companion guard
// on the OTHER direction: an explicit, positive subturn.max_concurrent is a
// deliberate per-delegation override and is honored exactly as configured —
// it may differ from the central Performance.EffectiveMaxParallelAgents()
// value in either direction, never silently coerced towards it.
func TestRootDelegationAdmission_ValidOverrideIsNotCoerced(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")

	const operatorCap = 24 // the explicit per-delegation override
	const centralValue = 5 // deliberately DIFFERENT, so the two cannot agree by coincidence
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
				SubTurn:           config.SubTurnConfig{MaxConcurrent: operatorCap},
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: centralValue},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	require.NotNil(t, al.rootDelegationAdmission)
	require.Equal(t, operatorCap, al.rootDelegationAdmission.Cap(),
		"an explicit operator override must survive unclamped and unmerged with the central value")
}
