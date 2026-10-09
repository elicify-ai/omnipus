// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Session-core U5a — self-delegation, RECONCILED to the settled design
// (founder ruling 2026-10-09, DESIGN-RULING-delegation-20261009.md). The
// original U5a RED pack encoded FR-014's "an eligible native agent's
// self-delegation needs no self-edge" + "an omitted target normalizes to the
// caller"; that design was SUPERSEDED and is asserted (inverted) below.
//
// Settled design:
//   - Delegation ALWAYS names an explicit target. An omitted agent_id is
//     refused outright — no caller substitution, no generic subagent.
//   - Self-delegation is an ORDINARY edge: an agent may name itself and fork a
//     NEW session running the same agent, authorized iff a caller→caller
//     self-edge EXISTS in the workspace graph, exactly like any other target.
//     workspace.PermittedSelfDelegationID (the hardcoded jim||worker allowlist)
//     was deleted — a self-edge is authorized by existing, not by a name.
//   - External CLI workers still must never create Omnipus helpers (FR-016),
//     which remains BLOCKED (no guard exists yet).

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Self-delegation is an ORDINARY edge: the SAME caller→self delegation is
// DENIED when no caller→caller edge exists, and ALLOWED once one does. This is
// the settled design — the former "eligible native agents need no self-edge"
// path was deleted.
func TestSessionCoreU5a_SelfDelegationRequiresSelfEdge(t *testing.T) {
	// A graph with a NON-self edge only (mia→ray); deliberately NO mia→mia.
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, nil),
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.PerformanceConfig{}, config.DelegationModeBackground)

	if d := check(ctxWS(testWS, 0), "mia"); d == nil {
		t.Fatal("a self-delegation with NO self-edge must be DENIED — a self-edge is an ordinary edge")
	}
	// Control (instrument check): an un-edged non-self target is also refused,
	// so a blanket-allow implementation cannot pass this test vacuously.
	if d := check(ctxWS(testWS, 0), "outsider"); d == nil {
		t.Fatal("control: an un-edged non-self target must still be denied")
	}

	// Add the self-edge: the SAME self-delegation is now authorized.
	rewriteWorkspaceGraph(t, home, testWS, true, []graphEdge{
		edge("mia", "mia", []string{"background"}, nil),
	})
	if d := check(ctxWS(testWS, 0), "mia"); d != nil {
		t.Fatalf("an explicit mia→mia self-edge must authorize self-delegation; got denial %q (%s)", d.Reason, d.Policy)
	}
}

// Delegation ALWAYS names a target: an OMITTED agent_id is refused outright —
// it is neither normalized to the caller nor spawned as a generic subagent.
func TestSessionCoreU5a_OmittedTargetRefused(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, nil)
	check := buildDelegationDenyCheckerForDelegate("mia", config.PerformanceConfig{}, config.DelegationModeBackground)

	if d := check(ctxWS(testWS, 0), ""); d == nil {
		t.Fatal("an omitted agent_id must be REFUSED — delegation always names an explicit target")
	}
	// Control: a named, un-edged target is real delegation and stays refused.
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
