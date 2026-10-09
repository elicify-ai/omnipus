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
// BDD-05.3: a self request from "external CLI worker, Admin outside expanded
// native scope or fixed-tool system agent" must create no helper.
//
// SEMANTIC CONTRACT THESE TESTS PIN (supersedes the retired ADR-091 rule
// "self-delegation is ALWAYS denied"):
//
//   - "Eligible native MAIN/WORKER" is a property of the LIVE CONFIG (a native
//     agent that is a chat-target main or a worker, and not an external-CLI
//     worker or a system agent), NOT a hardcoded id allowlist. See the seam
//     note below.
//   - An eligible caller's self-delegation (explicit self target, OR an
//     omitted target normalized to the caller) is PERMITTED with NO self-edge
//     in the workspace graph — subject only to the ordinary global depth cap
//     (default 3), never to a per-edge mode/depth gate (there is no edge).
//   - An INELIGIBLE caller's self-delegation is refused.
//   - A named NON-self target is still fully graph-gated (trust set + mode +
//     depth) — that behaviour is untouched and is each test's control.
//
// SEAM (agreed in the U5A-RED-FIX report, to be implemented by GREEN): an
// eligibility resolver, wired by production from the live config at the same
// site that builds the delegation gate (pkg/agent/loop_wire.go
// registerSharedTools), consulted by buildDelegationDenyChecker for the SELF
// branch. These tests exercise that path through the REAL production wiring —
// the caller's own `delegate` tool built by registerSharedTools — so the RED
// calls exactly what production calls and no test names a not-yet-existing
// symbol (which would not compile against the pre-change tree).
//
// ORACLE: every expected value below derives from FR-014/016 and BDD-05.1/05.3,
// never from the current (retired-semantics) code.

package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// u5aDelegateTool builds a REAL AgentLoop for callerID (from the seeded config,
// so callerID is whatever kind of agent the seed makes it — "mia" is a native
// chat-target main), seeds the default workspace graph with the given edges,
// and returns the caller's own `delegate` tool — the one registerSharedTools
// constructed and wired with the production delegation gate — with its session
// launcher replaced by a spy so an ALLOWED self-delegation is observable
// without spawning a real child. Fetching the tool from the registry (rather
// than calling a checker constructor directly) is what makes these tests
// exercise the config-derived eligibility path production will use.
func u5aDelegateTool(t *testing.T, callerID string, edges []graphEdge) (*tools.DelegateTool, *spySessionLauncher) {
	t.Helper()
	// seedWorkspaceGraph points OMNIPUS_HOME at a fresh temp dir and writes the
	// default workspace + its delegation-store record; wireTestLoopWithGraph
	// then builds the loop against that home.
	seedWorkspaceGraph(t, testWS, true, edges)
	al, _ := wireTestLoopWithGraph(t, callerID)

	inst, ok := al.GetRegistry().GetAgent(callerID)
	if !ok || inst == nil {
		t.Fatalf("agent %q missing from registry after loop init", callerID)
	}
	raw, ok := inst.Tools.Get("delegate")
	if !ok {
		t.Fatalf("delegate tool not registered for %q", callerID)
	}
	dt, ok := raw.(*tools.DelegateTool)
	if !ok {
		t.Fatalf("delegate tool type = %T, want *tools.DelegateTool", raw)
	}
	spy := &spySessionLauncher{}
	dt.SetSessionLauncher(spy)
	return dt, spy
}

// u5aCallerCtx binds the caller id and the seeded workspace on the turn, the
// two things the production gate resolves (ToolAgentID / ToolWorkspaceID).
func u5aCallerCtx(callerID string, depth int) context.Context {
	return tools.WithWorkspaceID(tools.WithAgentID(ctxAtDepth(depth), callerID), testWS)
}

// FR-014/015/BDD-05.1: an eligible native agent delegating to ITSELF must be
// permitted WITHOUT any self-edge in the workspace graph. A graph exists here
// (mia→ray) but contains deliberately NO mia→mia edge. Today the production
// gate denies this with trust_set ("self-delegation is never permitted"), so
// this fails.
func TestSessionCoreU5a_NativeSelfDelegationNeedsNoSelfEdge(t *testing.T) {
	dt, spy := u5aDelegateTool(t, "mia", []graphEdge{
		edge("mia", "ray", []string{"background"}, nil),
	})

	res := dt.Execute(u5aCallerCtx("mia", 0), map[string]any{
		"task":     "self helper",
		"agent_id": "mia", // the caller's OWN configured id
	})
	if res == nil {
		t.Fatal("nil result from delegate")
	}
	if res.IsError {
		t.Fatalf("FR-014/015/BDD-05.1: an eligible native agent's self-delegation must be "+
			"permitted WITHOUT a self-edge; got: %s", res.ForLLM)
	}
	if !spy.called {
		t.Fatal("FR-014/015/BDD-05.1: an eligible self-delegation must actually reach the " +
			"launcher and start a helper")
	}

	// Control (instrument check): a NAMED, un-edged NON-self target stays
	// refused, so a blanket-allow implementation cannot pass vacuously.
	ctl := dt.Execute(u5aCallerCtx("mia", 0), map[string]any{
		"task":     "cross",
		"agent_id": "ava", // real agent, but no mia→ava edge
	})
	if ctl == nil || !ctl.IsError {
		t.Fatalf("control: an un-edged non-self target must still be denied, got: %+v", ctl)
	}
	if !strings.Contains(ctl.ForLLM, "delegation_denied") {
		t.Fatalf("control: expected a delegation_denied result, got: %s", ctl.ForLLM)
	}
}

// FR-014/BDD-05.1: an OMITTED target must normalize to the CALLER before
// authorization — i.e. it is self-delegation and needs no self-edge. With NO
// outgoing edges at all, today the gate treats "" as untargeted (requires ANY
// outgoing edge) and denies ("no permitted delegation target"), so this fails.
func TestSessionCoreU5a_OmittedTargetNormalizesToCaller(t *testing.T) {
	// No edges at all: under FR-014 an omitted target is the caller (self),
	// which needs no self-edge.
	dt, spy := u5aDelegateTool(t, "mia", nil)

	res := dt.Execute(u5aCallerCtx("mia", 0), map[string]any{
		"task": "omitted-target helper", // agent_id deliberately absent
	})
	if res == nil {
		t.Fatal("nil result from delegate")
	}
	if res.IsError {
		t.Fatalf("FR-014/BDD-05.1: an omitted agent_id must normalize to the caller "+
			"(self-delegation) before authorization; got: %s", res.ForLLM)
	}
	if !spy.called {
		t.Fatal("FR-014/BDD-05.1: the normalized self-delegation must reach the launcher")
	}

	// Control: a NAMED, un-edged target is real delegation and stays refused.
	ctl := dt.Execute(u5aCallerCtx("mia", 0), map[string]any{
		"task":     "named",
		"agent_id": "ray", // no mia→ray edge in this empty graph
	})
	if ctl == nil || !ctl.IsError {
		t.Fatalf("control: a named un-edged target must still be denied, got: %+v", ctl)
	}
}

// FR-016/BDD-05.3: an external CLI worker MUST never create Omnipus helpers.
// A helper is any Omnipus-native child session, so the prohibition is BLANKET:
// it applies whether the worker targets itself or another agent. The worker's
// own `delegate` tool must either be absent (not registered) or refuse the
// request before the launcher runs.
//
// The named target with a REAL allowing edge is deliberate: it makes the RED
// meaningful. Today `delegate` is registered for every agent
// (loop_wire.go::registerSharedTools) and no delegation path consults the
// caller's external-CLI status, so this legitimate-edged request launches a
// native helper — the exact outcome FR-016 forbids. (A self-target would NOT
// be red today: the retired "self is always denied" rule already refuses an
// external-CLI worker's self-delegation, so it would pass for the wrong
// reason.)
func TestSessionCoreU5a_ExternalCLIWorkerCannotCreateHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(home, "agents"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{
				{
					ID:   "codex-worker",
					Name: "Codex Worker",
					Type: config.AgentTypeWorker,
					Home: filepath.Join(home, "agents", "codex-worker"),
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{
							Kind:    config.ExecutorKindExternalCLI,
							CLI:     "codex",
							CLIPath: "/usr/local/bin/codex",
						},
					},
				},
				{
					ID:   "ray",
					Name: "Ray",
					Type: config.AgentTypeCustom,
					Home: filepath.Join(home, "agents", "ray"),
				},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	// Seed a REAL allowing edge codex-worker→ray in the harness workspace so the
	// request is graph-authorized — otherwise today's gate (not the FR-016
	// guard) would be the thing refusing it, and the test would prove nothing.
	writeGraphFiles(t, home, testHarnessWorkspaceMembershipID, false, []graphEdge{
		edge("codex-worker", "ray", nil, nil), // empty modes = all modes allowed
	})

	inst, ok := al.GetRegistry().GetAgent("codex-worker")
	if !ok || inst == nil {
		t.Fatal("external-CLI worker not registered")
	}

	raw, hasDelegate := inst.Tools.Get("delegate")
	if !hasDelegate {
		// FR-016 satisfied by non-registration: an external-CLI worker with no
		// delegate tool cannot create a helper. This is an accepted mechanism.
		return
	}
	dt, ok := raw.(*tools.DelegateTool)
	if !ok {
		t.Fatalf("delegate tool type = %T, want *tools.DelegateTool", raw)
	}
	spy := &spySessionLauncher{}
	dt.SetSessionLauncher(spy)

	ctx := tools.WithAgentID(
		tools.WithWorkspaceID(context.Background(), testHarnessWorkspaceMembershipID),
		"codex-worker",
	)

	// (a) named non-self target with a real allowing edge — a native helper
	//     would be created today.
	res := dt.Execute(ctx, map[string]any{"task": "helper", "agent_id": "ray"})
	if res == nil || !res.IsError {
		t.Fatalf("FR-016: an external-CLI worker must not create a helper for %q, got: %+v", "ray", res)
	}
	if spy.called {
		t.Fatal("FR-016: an external-CLI worker's delegate must not launch a child session")
	}

	// (b) self target — likewise refused (BDD-05.3).
	res = dt.Execute(ctx, map[string]any{"task": "helper", "agent_id": "codex-worker"})
	if res == nil || !res.IsError {
		t.Fatalf("FR-016/BDD-05.3: an external-CLI worker must not create a helper for itself, got: %+v", res)
	}
	if spy.called {
		t.Fatal("FR-016/BDD-05.3: an external-CLI worker's self-delegation must not launch a child")
	}
}
