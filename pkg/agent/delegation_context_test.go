// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// resolveTestLabel is a fixed resolveLabel implementation for tests. It maps a
// small set of known IDs and returns ok=false for anything else.
func resolveTestLabel(id string) (string, bool) {
	labels := map[string]string{
		"ava": "Ava (Builder: implementation & code)",
		"ray": "Ray (Scout: research & browsing)",
	}
	l, ok := labels[id]
	return l, ok
}

func ptr(n int) *int { return &n }

// makeTarget builds a delegationTarget from the test label map. Returns a
// target with empty Label when id is unknown (buildDelegationContext will skip it).
func makeTarget(id string, modes []config.DelegationMode, depth *int) delegationTarget {
	label, _ := resolveTestLabel(id)
	return delegationTarget{ID: id, Label: label, Modes: modes, Depth: depth}
}

func TestBuildDelegationContext_NoTargets(t *testing.T) {
	got := buildDelegationContext(nil, 0)
	want := "## Delegation\nYou cannot delegate to other agents in this workspace — complete the task yourself. Do not call list_agents or search memory to look for delegation targets; there are none configured for you here."
	if got != want {
		t.Errorf("nil targets:\ngot:  %q\nwant: %q", got, want)
	}
	// Must NOT contain the authority line (only in the non-empty path).
	if strings.Contains(got, "COMPLETE, authoritative") {
		t.Errorf("cannot-delegate path must not contain authority line; got:\n%s", got)
	}
}

func TestBuildDelegationContext_EmptyTargets(t *testing.T) {
	got := buildDelegationContext([]delegationTarget{}, 0)
	want := "## Delegation\nYou cannot delegate to other agents in this workspace — complete the task yourself. Do not call list_agents or search memory to look for delegation targets; there are none configured for you here."
	if got != want {
		t.Errorf("empty targets:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestBuildDelegationContext_SingleTargetAllModes(t *testing.T) {
	targets := []delegationTarget{
		makeTarget("ava", nil, ptr(3)), // nil Modes = all three
	}
	got := buildDelegationContext(targets, 3)

	// Must contain the header.
	if !strings.Contains(got, "## Delegation") {
		t.Error("missing ## Delegation header")
	}
	// Authority line must appear immediately after the header (non-empty path).
	if !strings.Contains(got, "COMPLETE, authoritative delegation roster") {
		t.Errorf("missing authority line; got:\n%s", got)
	}
	if !strings.Contains(got, "do NOT call list_agents or search memory to determine your delegation targets") {
		t.Errorf("missing list_agents prohibition in authority line; got:\n%s", got)
	}
	// Must contain the subagent header.
	if !strings.Contains(got, "### → Ava (Builder: implementation & code)") {
		t.Errorf("missing target header; got:\n%s", got)
	}
	// Both delegate forms (background default, await async=false) must appear,
	// plus create_task.
	if !strings.Contains(got, `delegate(agent_id="ava", task="…")`) {
		t.Errorf("missing background delegate(agent_id= tool call; got:\n%s", got)
	}
	if !strings.Contains(got, `delegate(agent_id="ava", task="…", async=false)`) {
		t.Errorf("missing await delegate(agent_id=..., async=false) tool call; got:\n%s", got)
	}
	if !strings.Contains(got, `create_task(agent_id="ava"`) {
		t.Errorf("missing create_task(agent_id= tool call; got:\n%s", got)
	}
	// Exclusivity footer must appear.
	if !strings.Contains(got, "ONLY permitted delegation targets") {
		t.Errorf("missing exclusivity footer; got:\n%s", got)
	}
	if !strings.Contains(got, "delegate / create_task to any other agent WILL be denied") {
		t.Errorf("missing denial warning in exclusivity footer; got:\n%s", got)
	}
	// Depth.
	if !strings.Contains(got, "max chain depth: 3") {
		t.Errorf("missing depth; got:\n%s", got)
	}
	// Retired names must not appear.
	if strings.Contains(got, "task_create") {
		t.Errorf("retired tool name 'task_create' must not appear; got:\n%s", got)
	}
	if strings.Contains(got, "run_subagent") {
		t.Errorf("retired tool name 'run_subagent' must not appear; got:\n%s", got)
	}
	if strings.Contains(got, "check_spawn_status") {
		t.Errorf("retired tool name 'check_spawn_status' must not appear; got:\n%s", got)
	}
}

func TestBuildDelegationContext_ModesAwaitOnly(t *testing.T) {
	targets := []delegationTarget{
		makeTarget("ava", []config.DelegationMode{config.DelegationModeAwait}, nil),
	}
	got := buildDelegationContext(targets, 0)

	// Only the await (async=false) form must appear; the background form and
	// create_task must NOT appear as tool calls.
	if !strings.Contains(got, `delegate(agent_id="ava", task="…", async=false)`) {
		t.Errorf("missing await delegate call for await mode; got:\n%s", got)
	}
	// The background form's exact closing (`task="…")`, immediate paren) must
	// be absent — it is NOT a substring of the await form (which continues
	// with ", async=false)" instead of closing immediately).
	if strings.Contains(got, `delegate(agent_id="ava", task="…")`) {
		t.Errorf("background delegate call must NOT appear when Modes=[await]; got:\n%s", got)
	}
	if strings.Contains(got, `create_task(agent_id=`) {
		t.Errorf("create_task tool call must NOT appear when Modes=[await]; got:\n%s", got)
	}
	// No mode footer — the new implementation renders the global depth footer only.
	if !strings.Contains(got, "max chain depth: uncapped") {
		t.Errorf("missing uncapped depth footer; got:\n%s", got)
	}
}

func TestBuildDelegationContext_TwoTargets(t *testing.T) {
	targets := []delegationTarget{
		makeTarget("ava", nil, nil),
		makeTarget("ray", nil, nil),
	}
	got := buildDelegationContext(targets, 0)

	// Both subagent sections must be present.
	if !strings.Contains(got, "### → Ava (Builder: implementation & code)") {
		t.Errorf("missing ava section; got:\n%s", got)
	}
	if !strings.Contains(got, "### → Ray (Scout: research & browsing)") {
		t.Errorf("missing ray section; got:\n%s", got)
	}
	// Each target's ID must appear in delegate and create_task calls.
	if !strings.Contains(got, `delegate(agent_id="ava", task="…")`) {
		t.Errorf("missing delegate(agent_id=\"ava\" in tool calls; got:\n%s", got)
	}
	if !strings.Contains(got, `delegate(agent_id="ray", task="…")`) {
		t.Errorf("missing delegate(agent_id=\"ray\" in tool calls; got:\n%s", got)
	}
	if !strings.Contains(got, `create_task(agent_id="ava"`) {
		t.Errorf("missing create_task(agent_id=\"ava\" in tool calls; got:\n%s", got)
	}
	if !strings.Contains(got, `create_task(agent_id="ray"`) {
		t.Errorf("missing create_task(agent_id=\"ray\" in tool calls; got:\n%s", got)
	}
	// Depth: uncapped.
	if !strings.Contains(got, "max chain depth: uncapped") {
		t.Errorf("expected uncapped depth; got:\n%s", got)
	}
}

func TestBuildDelegationContext_UnknownTargetSkipped(t *testing.T) {
	targets := []delegationTarget{
		// Empty label = unknown/unresolvable target.
		{ID: "nonexistent-agent", Label: "", Modes: nil, Depth: nil},
	}
	got := buildDelegationContext(targets, 0)

	// All targets skipped → cannot-delegate message.
	if !strings.Contains(got, "You cannot delegate to other agents in this workspace") {
		t.Errorf("expected cannot-delegate text when all targets skipped; got:\n%s", got)
	}
	// Must contain the list_agents prohibition.
	if !strings.Contains(got, "Do not call list_agents or search memory") {
		t.Errorf("expected list_agents prohibition in cannot-delegate message; got:\n%s", got)
	}
	if strings.Contains(got, "### →") {
		t.Errorf("unknown target must be skipped but a section header appeared; got:\n%s", got)
	}
	// The cannot-delegate path returns a single-line string with no tool calls.
	if strings.Contains(got, `delegate(agent_id=`) {
		t.Errorf("no tool call lines expected when all targets skipped; got:\n%s", got)
	}
	// The authority and exclusivity lines must NOT appear in the cannot-delegate path.
	if strings.Contains(got, "COMPLETE, authoritative") {
		t.Errorf("cannot-delegate path must not contain authority line; got:\n%s", got)
	}
	if strings.Contains(got, "ONLY permitted delegation targets") {
		t.Errorf("cannot-delegate path must not contain exclusivity footer; got:\n%s", got)
	}
}

func TestBuildDelegationContext_DepthUncapped(t *testing.T) {
	targets := []delegationTarget{makeTarget("ava", nil, nil)}
	got := buildDelegationContext(targets, 0)
	if !strings.Contains(got, "max chain depth: uncapped") {
		t.Errorf("globalDepthCap=0 must render as uncapped; got:\n%s", got)
	}
}

func TestBuildDelegationContext_GlobalDepthSet(t *testing.T) {
	targets := []delegationTarget{makeTarget("ava", nil, nil)}
	got := buildDelegationContext(targets, 3)
	if !strings.Contains(got, "max chain depth: 3") {
		t.Errorf("globalDepthCap=3 must render as '3'; got:\n%s", got)
	}
}

func TestBuildDelegationContext_BackgroundModeOnly(t *testing.T) {
	targets := []delegationTarget{
		makeTarget("ava", []config.DelegationMode{config.DelegationModeBackground}, nil),
	}
	got := buildDelegationContext(targets, 0)

	// The await (async=false) form and create_task must NOT appear as calls.
	if strings.Contains(got, `delegate(agent_id="ava", task="…", async=false)`) {
		t.Errorf("await delegate call must NOT appear when Modes=[background]; got:\n%s", got)
	}
	if !strings.Contains(got, `delegate(agent_id="ava", task="…")`) {
		t.Errorf("background delegate(agent_id= must appear for background mode; got:\n%s", got)
	}
	if strings.Contains(got, `create_task(agent_id=`) {
		t.Errorf("create_task tool call must NOT appear when Modes=[background]; got:\n%s", got)
	}
}

func TestBuildDelegationContext_TaskModeOnly(t *testing.T) {
	targets := []delegationTarget{
		makeTarget("ava", []config.DelegationMode{config.DelegationModeTask}, nil),
	}
	got := buildDelegationContext(targets, 0)

	// Neither delegate form must appear as an actual call.
	if strings.Contains(got, `delegate(agent_id="ava", task="…", async=false)`) {
		t.Errorf("await delegate call must NOT appear when Modes=[task]; got:\n%s", got)
	}
	if strings.Contains(got, `delegate(agent_id="ava", task="…")`) {
		t.Errorf("background delegate call must NOT appear when Modes=[task]; got:\n%s", got)
	}
	if !strings.Contains(got, "create_task(agent_id=") {
		t.Errorf("create_task(agent_id= must appear for task mode; got:\n%s", got)
	}
}

// TestBuildDelegationContext_TwoModeSubset verifies a [background, task] subset.
func TestBuildDelegationContext_TwoModeSubset(t *testing.T) {
	targets := []delegationTarget{
		makeTarget("ava", []config.DelegationMode{
			config.DelegationModeBackground,
			config.DelegationModeTask,
		}, nil),
	}
	got := buildDelegationContext(targets, 0)

	// await must be absent.
	if strings.Contains(got, `delegate(agent_id="ava", task="…", async=false)`) {
		t.Errorf("await delegate call must NOT appear for [background,task] modes; got:\n%s", got)
	}
	// Both background and task must be present.
	if !strings.Contains(got, `delegate(agent_id="ava", task="…")`) {
		t.Errorf("background delegate(agent_id= must appear for background mode; got:\n%s", got)
	}
	if !strings.Contains(got, "create_task(agent_id=") {
		t.Errorf("create_task(agent_id= must appear for task mode; got:\n%s", got)
	}
}

// TestBuildDelegationContext_MixedTargets verifies that known targets render and
// unknown targets are skipped, leaving only the known ones' sections.
func TestBuildDelegationContext_MixedTargets(t *testing.T) {
	targets := []delegationTarget{
		makeTarget("ava", nil, nil),
		{ID: "nonexistent", Label: "", Modes: nil, Depth: nil}, // unknown, must be skipped
		makeTarget("ray", nil, nil),
	}
	got := buildDelegationContext(targets, 0)

	if !strings.Contains(got, "### → Ava (Builder: implementation & code)") {
		t.Errorf("missing ava section; got:\n%s", got)
	}
	if !strings.Contains(got, "### → Ray (Scout: research & browsing)") {
		t.Errorf("missing ray section; got:\n%s", got)
	}
	if strings.Contains(got, "nonexistent") {
		t.Errorf("nonexistent target must be skipped; got:\n%s", got)
	}
	if !strings.Contains(got, `delegate(agent_id="ava", task="…")`) {
		t.Errorf("missing delegate(agent_id=\"ava\"; got:\n%s", got)
	}
	if !strings.Contains(got, `delegate(agent_id="ray", task="…")`) {
		t.Errorf("missing delegate(agent_id=\"ray\"; got:\n%s", got)
	}
}

// TestBuildDelegationContext_AllSkipped verifies that when all entries have empty
// labels (all unknown), the result is the clean cannot-delegate text with the
// list_agents prohibition and WITHOUT the authority / exclusivity lines.
func TestBuildDelegationContext_AllSkipped(t *testing.T) {
	targets := []delegationTarget{
		{ID: "ghost1", Label: "", Modes: nil, Depth: nil},
		{ID: "ghost2", Label: "", Modes: nil, Depth: nil},
	}
	got := buildDelegationContext(targets, 0)

	if !strings.Contains(got, "You cannot delegate to other agents in this workspace") {
		t.Errorf("all-skipped must produce cannot-delegate text; got:\n%s", got)
	}
	if !strings.Contains(got, "Do not call list_agents or search memory") {
		t.Errorf("all-skipped must contain list_agents prohibition; got:\n%s", got)
	}
	if strings.Contains(got, "### →") {
		t.Errorf("no section headers expected when all skipped; got:\n%s", got)
	}
	if strings.Contains(got, "COMPLETE, authoritative") {
		t.Errorf("cannot-delegate path must not contain authority line; got:\n%s", got)
	}
	if strings.Contains(got, "ONLY permitted delegation targets") {
		t.Errorf("cannot-delegate path must not contain exclusivity footer; got:\n%s", got)
	}
}

// TestBuildDelegationContext_PerTargetOnwardForbidden verifies that when a
// target's edge Depth is <= 0, a note about no onward delegation is emitted
// for that target only, while other targets are unaffected.
func TestBuildDelegationContext_PerTargetOnwardForbidden(t *testing.T) {
	targets := []delegationTarget{
		makeTarget("ava", nil, ptr(0)), // Depth=0: cannot delegate onward
		makeTarget("ray", nil, nil),    // Depth=nil: inherits, no note
	}
	got := buildDelegationContext(targets, 0)

	// Both sections must appear.
	if !strings.Contains(got, "### → Ava") {
		t.Errorf("missing ava section; got:\n%s", got)
	}
	if !strings.Contains(got, "### → Ray") {
		t.Errorf("missing ray section; got:\n%s", got)
	}
	// Depth=0 note must appear (contains the onward delegation message).
	if !strings.Contains(got, "cannot delegate onward") {
		t.Errorf("missing onward-forbidden note for depth=0 target; got:\n%s", got)
	}
}

// TestBuildDelegationContext_PerTargetModeSubset verifies that when two targets
// have different mode subsets, each renders only its own tools.
func TestBuildDelegationContext_PerTargetModeSubset(t *testing.T) {
	targets := []delegationTarget{
		// ava: await only
		makeTarget("ava", []config.DelegationMode{config.DelegationModeAwait}, nil),
		// ray: all modes
		makeTarget("ray", nil, nil),
	}
	got := buildDelegationContext(targets, 0)

	// ava section: only the await form; background delegate and create_task
	// must NOT appear for ava.
	if strings.Contains(got, `delegate(agent_id="ava", task="…")`) {
		t.Errorf("background delegate for ava must NOT appear when ava edge is await-only; got:\n%s", got)
	}
	if strings.Contains(got, `create_task(agent_id="ava"`) {
		t.Errorf("create_task for ava must NOT appear when ava edge is await-only; got:\n%s", got)
	}
	if !strings.Contains(got, `delegate(agent_id="ava", task="…", async=false)`) {
		t.Errorf("await delegate for ava must appear; got:\n%s", got)
	}

	// ray section: all three tools.
	if !strings.Contains(got, `delegate(agent_id="ray", task="…")`) {
		t.Errorf("background delegate for ray must appear (all modes); got:\n%s", got)
	}
	if !strings.Contains(got, `create_task(agent_id="ray"`) {
		t.Errorf("create_task for ray must appear (all modes); got:\n%s", got)
	}
	if !strings.Contains(got, `delegate(agent_id="ray", task="…", async=false)`) {
		t.Errorf("await delegate for ray must appear (all modes); got:\n%s", got)
	}
}

// TestBuildDelegationContext_DelegationAuthorityAndExclusivity is a focused test
// for the two new prompt-clarity additions:
//  1. The authority line (rendered right after the ## Delegation header, before target sections).
//  2. The exclusivity footer (rendered after all target sections, before the depth footer).
//
// Both must appear in every non-empty-targets invocation and must be absent in the
// cannot-delegate path.
func TestBuildDelegationContext_DelegationAuthorityAndExclusivity(t *testing.T) {
	t.Run("non-empty targets renders authority and exclusivity", func(t *testing.T) {
		targets := []delegationTarget{makeTarget("ava", nil, nil)}
		got := buildDelegationContext(targets, 0)

		if !strings.Contains(got, "COMPLETE, authoritative delegation roster for THIS workspace") {
			t.Errorf("authority line missing; got:\n%s", got)
		}
		if !strings.Contains(got, "do NOT call list_agents or search memory to determine your delegation targets") {
			t.Errorf("list_agents prohibition missing from authority line; got:\n%s", got)
		}
		if !strings.Contains(got, "These are your ONLY permitted delegation targets") {
			t.Errorf("exclusivity footer missing; got:\n%s", got)
		}
		if !strings.Contains(got, "delegate / create_task to any other agent WILL be denied") {
			t.Errorf("denial warning missing from exclusivity footer; got:\n%s", got)
		}
		// Exclusivity footer must appear BEFORE the depth footer.
		exclusivityIdx := strings.Index(got, "ONLY permitted delegation targets")
		depthIdx := strings.Index(got, "max chain depth")
		if exclusivityIdx == -1 || depthIdx == -1 || exclusivityIdx > depthIdx {
			t.Errorf(
				"exclusivity footer must appear before depth footer; exclusivityIdx=%d depthIdx=%d; got:\n%s",
				exclusivityIdx,
				depthIdx,
				got,
			)
		}
	})

	t.Run("cannot-delegate path has no authority or exclusivity lines", func(t *testing.T) {
		got := buildDelegationContext(nil, 0)
		if strings.Contains(got, "COMPLETE, authoritative") {
			t.Errorf("cannot-delegate path must not contain authority line; got:\n%s", got)
		}
		if strings.Contains(got, "ONLY permitted delegation targets") {
			t.Errorf("cannot-delegate path must not contain exclusivity footer; got:\n%s", got)
		}
		// But the list_agents prohibition MUST appear — the agent should not go looking.
		if !strings.Contains(got, "Do not call list_agents or search memory") {
			t.Errorf("cannot-delegate path must contain list_agents prohibition; got:\n%s", got)
		}
	})

	t.Run("all-skipped path has no authority or exclusivity lines", func(t *testing.T) {
		targets := []delegationTarget{{ID: "unknown", Label: "", Modes: nil, Depth: nil}}
		got := buildDelegationContext(targets, 0)
		if strings.Contains(got, "COMPLETE, authoritative") {
			t.Errorf("all-skipped path must not contain authority line; got:\n%s", got)
		}
		if strings.Contains(got, "ONLY permitted delegation targets") {
			t.Errorf("all-skipped path must not contain exclusivity footer; got:\n%s", got)
		}
		if !strings.Contains(got, "Do not call list_agents or search memory") {
			t.Errorf("all-skipped path must contain list_agents prohibition; got:\n%s", got)
		}
	})
}
