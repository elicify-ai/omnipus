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
//   - External CLI workers must never create Omnipus helpers (FR-016). The
//     delegate gate refuses an external-CLI CALLER through the ORDINARY
//     delegation checker — a caller-eligibility resolver is injected on the
//     helper-creation wiring (delegationGateDeps.CallerIsExternalCLI), not an
//     identity allowlist and not a second enforcement path. External-CLI
//     agents remain valid delegation TARGETS (founder ruling 2026-10-09).

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

// FR-016/BDD-05.3: an external CLI worker MUST never create Omnipus helpers as
// a CALLER, while remaining a perfectly valid delegation TARGET.
//
// The refusal lives in the SAME ordinary delegation gate that enforces trust
// set / mode / depth (buildDelegationDenyChecker): the delegate wiring injects
// a caller-eligibility resolver (delegationGateDeps.CallerIsExternalCLI, backed
// by the registry's dispatch kind). It is neither an identity allowlist nor a
// second enforcement path — the graph edge, mode and depth checks are untouched.
func TestSessionCoreU5a_ExternalCLIWorkerCannotCreateHelpers(t *testing.T) {
	// A graph that WOULD authorize every call below: extworker→mia, mia→extworker
	// and extworker→extworker edges. Without a valid edge a denial would prove
	// nothing about the caller guard (an un-edged target is denied anyway).
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("extworker", "mia", []string{"background"}, nil),
		edge("mia", "extworker", []string{"background"}, nil),
		edge("extworker", "extworker", []string{"background"}, nil),
	})

	// The resolver the production delegate wiring installs: "extworker" is an
	// external CLI worker; every other id is native.
	externalCLI := func(id string) bool { return id == "extworker" }

	// 1. External-CLI CALLER → refused, even though a valid caller→target edge
	//    exists and the target is an ordinary native agent.
	extCaller := buildDelegationDenyCheckerForDelegate("extworker",
		config.PerformanceConfig{}, config.DelegationModeBackground,
		delegationGateDeps{CallerIsExternalCLI: externalCLI})
	if d := extCaller(ctxWS(testWS, 0), "mia"); d == nil {
		t.Fatal("an external-CLI caller must be REFUSED (FR-016) even with a valid caller→target edge")
	}

	// 2. Native CALLER, same graph → unaffected, AND delegating TO the
	//    external-CLI agent is still allowed (the founder ruling that external
	//    CLI agents may be delegates is preserved — only the caller is barred).
	nativeCaller := buildDelegationDenyCheckerForDelegate("mia",
		config.PerformanceConfig{}, config.DelegationModeBackground,
		delegationGateDeps{CallerIsExternalCLI: externalCLI})
	if d := nativeCaller(ctxWS(testWS, 0), "extworker"); d != nil {
		t.Fatalf("a native caller must be unaffected, including delegating to an "+
			"external-CLI target; got denial %q (%s)", d.Reason, d.Policy)
	}

	// 3. Instrument check: the guard is the CALLER, not a blanket deny. A native
	//    caller→native target on a fresh valid edge is allowed. (Re-write the
	//    graph so a failure here cannot be blamed on the earlier edge set.)
	rewriteWorkspaceGraph(t, home, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, nil),
	})
	nativeToNative := buildDelegationDenyCheckerForDelegate("mia",
		config.PerformanceConfig{}, config.DelegationModeBackground,
		delegationGateDeps{CallerIsExternalCLI: externalCLI})
	if d := nativeToNative(ctxWS(testWS, 0), "ray"); d != nil {
		t.Fatalf("control: a native caller with a valid edge must be allowed; got %+v", d)
	}

	// 4. Control: the guard fires for the external-CLI caller regardless of the
	//    target shape — an explicit self-target is refused too (no helper of any
	//    kind), even though the extworker→extworker self-edge exists.
	if d := extCaller(ctxWS(testWS, 0), "extworker"); d == nil {
		t.Fatal("control: an external-CLI caller must be refused for an explicit self-target too")
	}
}
