package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// TestRootDelegationAdmission_FailsClosedOnUnresolvableCap pins the FR-095
// fail-closed contract for the branch where ResolveRootDelegationCap returns
// an error.
//
// Why this exists: an earlier wiring left al.rootDelegationAdmission NIL when
// the cap could not be resolved, and rootDelegationAdmittingSpawner's nil-gate
// check then degrades to pass-through — i.e. UNLIMITED root-level delegate()
// fan-out. That is the "silently reinterpreted as no gate" outcome FR-095
// names as the banned ADR-037 anti-pattern, and it is invisible at runtime:
// nothing refuses, nothing logs at the dispatch site, and the operator sees a
// working system right up until a wide fan-out saturates their provider.
//
// The existing happy-path coverage
// (TestWiring_RootDelegationAdmission_RefusesPastCapThenAdmitsOnRelease) sets
// a valid MaxConcurrent, so it cannot catch a regression on this branch.
func TestRootDelegationAdmission_FailsClosedOnUnresolvableCap(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "") // keep the unrelated env override inert

	// SubTurn is left at its zero value, so MaxConcurrent is 0 and
	// ResolveRootDelegationCap returns ErrRootDelegationCapMisconfigured —
	// exactly the branch under test.
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	require.NotNil(t, al.rootDelegationAdmission,
		"an unresolvable root-delegation cap MUST still construct a gate — a nil gate "+
			"is pass-through, i.e. unlimited root fan-out (FR-095's banned 'no gate' outcome)")
	require.Equal(t, config.DefaultSubTurnMaxConcurrent, al.rootDelegationAdmission.Cap(),
		"the failure branch must fall back to the seeded default cap")

	// Positive lower bound (Binding Rule 4): a non-nil gate at the right Cap()
	// still proves nothing unless it actually REFUSES. Saturate it and assert
	// the next admit is denied — without this, a gate that admits everything
	// would satisfy the assertions above.
	releases := make([]func(), 0, config.DefaultSubTurnMaxConcurrent)
	for i := 0; i < config.DefaultSubTurnMaxConcurrent; i++ {
		admitted, release := al.rootDelegationAdmission.TryAdmit()
		require.Truef(t, admitted,
			"admit %d of %d must succeed while the gate has capacity", i+1, config.DefaultSubTurnMaxConcurrent)
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

// TestRootDelegationAdmission_ValidOverrideIsNotCoerced is the companion guard
// on the OTHER direction: the fail-closed fallback above must fire ONLY on the
// error branch. ResolveRootDelegationCap's contract is that an explicit
// operator value passes through unclamped (FR-095 forbids routing it through
// the paths that hard-clamp to 16), so a fallback applied too eagerly would
// silently cap an operator who deliberately raised the limit.
func TestRootDelegationAdmission_ValidOverrideIsNotCoerced(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")

	const operatorCap = 24 // deliberately above DefaultSubTurnMaxConcurrent (16)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
				SubTurn:           config.SubTurnConfig{MaxConcurrent: operatorCap},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	require.NotNil(t, al.rootDelegationAdmission)
	require.Equal(t, operatorCap, al.rootDelegationAdmission.Cap(),
		"an explicit operator cap above the default must survive unclamped — the "+
			"fail-closed fallback applies only when resolution errors")
}
