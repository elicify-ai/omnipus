// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U5a (native self-delegation through the ordinary
// launcher). Spec: docs/internal/specs/session-core-spec.md FR-014/015/016,
// BDD-05.1–05.4, T07. ADR D4
// (ADR-20261006-session-core-with-an-agent-address-book.md).
//
// FR-014: "Omitted target MUST normalize to caller before common authorization.
// Eligible native MAIN/WORKER self-delegation needs no self-edge; operator Deny
// wins."
// FR-015: "Self-helpers MUST use ordinary context/memory/requested-skill/
// receiver/depth/admission rules and nesting default depth 3."
// FR-016: "External CLI workers MUST never create Omnipus helpers."
// BDD-05.1: "Native MAIN in main/extra and native WORKER; no self-edge, delegate
// permitted; ... omitted and explicit self in each role."
//
// Current code: self-delegation is gated by workspace.PermittedSelfDelegationID
// (a hardcoded jim|worker allowlist) AND STILL requires an explicit self-edge in
// the workspace graph; every other identity is denied outright. An omitted
// target is evaluated as untargeted (requires ANY outgoing edge), not normalized
// to the caller. The expected values below come from the spec, not the code.

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// FR-014/015/BDD-05.1: an eligible native agent delegating to ITSELF must be
// permitted WITHOUT any self-edge in the workspace graph. Today "mia"
// self-targets are denied with trust_set (the id is not the hardcoded
// jim/worker allowlist), so this fails.
func TestSessionCoreU5a_NativeSelfDelegationNeedsNoSelfEdge(t *testing.T) {
	// A graph EXISTS (mia→ray), but there is deliberately NO mia→mia edge.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, nil),
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.PerformanceConfig{}, config.DelegationModeBackground)

	if d := check(ctxWS(testWS, 0), "mia"); d != nil {
		t.Fatalf(
			"FR-014/015/BDD-05.1: an eligible native agent's self-delegation must be permitted WITHOUT a self-edge; got denial %q (%s)",
			d.Reason, d.Policy,
		)
	}

	// Control (instrument check): the checker CAN still deny — an un-edged,
	// non-self target stays refused, so a blanket-allow implementation cannot
	// pass this test vacuously.
	if d := check(ctxWS(testWS, 0), "outsider"); d == nil {
		t.Fatal("control: an un-edged non-self target must still be denied")
	}
}

// FR-014/BDD-05.1: an OMITTED target must normalize to the CALLER before
// authorization — i.e. it is self-delegation and needs no self-edge. With NO
// outgoing edges at all, today the checker treats "" as untargeted and denies
// (trust_set "no permitted delegation target"), so this fails.
func TestSessionCoreU5a_OmittedTargetNormalizesToCaller(t *testing.T) {
	// No edges at all: under FR-014 an omitted target is the caller (self),
	// which needs no self-edge.
	seedWorkspaceGraph(t, testWS, true, nil)
	check := buildDelegationDenyCheckerForDelegate("mia", config.PerformanceConfig{}, config.DelegationModeBackground)

	if d := check(ctxWS(testWS, 0), ""); d != nil {
		t.Fatalf(
			"FR-014/BDD-05.1: an omitted agent_id must normalize to the caller (self-delegation) before authorization; got denial %q (%s)",
			d.Reason, d.Policy,
		)
	}

	// Control: a NAMED, un-edged target is real delegation and stays refused.
	if d := check(ctxWS(testWS, 0), "ray"); d == nil {
		t.Fatal("control: a named un-edged target must still be denied")
	}
}

// FR-016/BDD-05.3: an external CLI worker MUST never create Omnipus helpers.
//
// BLOCKED: no guard exists today. registerSharedTools registers the `delegate`
// tool for EVERY agent in the registry with no external-CLI skip, and no
// delegation path consults the caller's external status. The mechanism (skip
// registration for external-CLI workers, or refuse inside the delegate path) is
// a design decision for the architect. Required by FR-016 / DEL-20-adjacent
// session-core U5a.
func TestSessionCoreU5a_ExternalCLIWorkerCannotCreateHelpers(t *testing.T) {
	t.Fatal("BLOCKED: no guard prevents an external-CLI worker from creating Omnipus helpers — " +
		"required by FR-016 / BDD-05.3. Today `delegate` is registered for every agent " +
		"(pkg/agent/loop_wire.go::registerSharedTools) and no delegation path checks the caller's " +
		"external-CLI status; the eligibility predicate and its shape are open for the architect.")
}