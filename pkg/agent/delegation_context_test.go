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
	want := "## Delegation\nYou cannot delegate to other agents — complete the task yourself."
	if got != want {
		t.Errorf("nil targets:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestBuildDelegationContext_EmptyTargets(t *testing.T) {
	got := buildDelegationContext([]delegationTarget{}, 0)
	want := "## Delegation\nYou cannot delegate to other agents — complete the task yourself."
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
	// Must contain the subagent header.
	if !strings.Contains(got, "### → Ava (Builder: implementation & code)") {
		t.Errorf("missing target header; got:\n%s", got)
	}
	// All three tools must appear with correct names.
	if !strings.Contains(got, "run_subagent") {
		t.Error("missing run_subagent tool")
	}
	if !strings.Contains(got, `spawn(agent_id=`) {
		t.Errorf("missing spawn(agent_id= tool call; got:\n%s", got)
	}
	if !strings.Contains(got, `create_task(agent_id=`) {
		t.Errorf("missing create_task(agent_id= tool call; got:\n%s", got)
	}
	// Correct agent ID in spawn and create_task tool calls.
	if !strings.Contains(got, `"ava"`) {
		t.Errorf("missing concrete agent id 'ava' in tool calls; got:\n%s", got)
	}
	// run_subagent targets a named agent by id (await mode).
	if !strings.Contains(got, `run_subagent(agent_id="ava"`) {
		t.Errorf("run_subagent must target the named agent via agent_id; got:\n%s", got)
	}
	// Depth.
	if !strings.Contains(got, "max chain depth: 3") {
		t.Errorf("missing depth; got:\n%s", got)
	}
	// Retired name must not appear.
	if strings.Contains(got, "task_create") {
		t.Errorf("retired tool name 'task_create' must not appear; got:\n%s", got)
	}
}

func TestBuildDelegationContext_ModesAwaitOnly(t *testing.T) {
	targets := []delegationTarget{
		makeTarget("ava", []config.DelegationMode{config.DelegationModeAwait}, nil),
	}
	got := buildDelegationContext(targets, 0)

	// Only run_subagent must appear; spawn and create_task must NOT.
	if !strings.Contains(got, "run_subagent") {
		t.Error("missing run_subagent for await mode")
	}
	// "spawn(" as a distinctive prefix — avoids substring-match with check_spawn_status.
	if strings.Contains(got, "spawn(") {
		t.Errorf("spawn must NOT appear when Modes=[await]; got:\n%s", got)
	}
	if strings.Contains(got, "create_task") {
		t.Errorf("create_task must NOT appear when Modes=[await]; got:\n%s", got)
	}
	// run_subagent targets the named agent by id (await mode).
	if !strings.Contains(got, `run_subagent(agent_id="ava"`) {
		t.Errorf("run_subagent must target the named agent via agent_id; got:\n%s", got)
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
	// Each target's ID must appear in spawn and create_task calls.
	if !strings.Contains(got, `spawn(agent_id="ava"`) {
		t.Errorf("missing spawn(agent_id=\"ava\" in tool calls; got:\n%s", got)
	}
	if !strings.Contains(got, `spawn(agent_id="ray"`) {
		t.Errorf("missing spawn(agent_id=\"ray\" in tool calls; got:\n%s", got)
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
	if !strings.Contains(got, "You cannot delegate to other agents") {
		t.Errorf("expected cannot-delegate text when all targets skipped; got:\n%s", got)
	}
	if strings.Contains(got, "### →") {
		t.Errorf("unknown target must be skipped but a section header appeared; got:\n%s", got)
	}
	if strings.Contains(got, "run_subagent") {
		t.Errorf("no tool lines expected when all targets skipped; got:\n%s", got)
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

	if strings.Contains(got, "run_subagent") {
		t.Errorf("run_subagent must NOT appear when Modes=[background]; got:\n%s", got)
	}
	if !strings.Contains(got, "spawn(agent_id=") {
		t.Errorf("spawn(agent_id= must appear for background mode; got:\n%s", got)
	}
	if strings.Contains(got, "create_task") {
		t.Errorf("create_task must NOT appear when Modes=[background]; got:\n%s", got)
	}
}

func TestBuildDelegationContext_TaskModeOnly(t *testing.T) {
	targets := []delegationTarget{
		makeTarget("ava", []config.DelegationMode{config.DelegationModeTask}, nil),
	}
	got := buildDelegationContext(targets, 0)

	if strings.Contains(got, "run_subagent") {
		t.Errorf("run_subagent must NOT appear when Modes=[task]; got:\n%s", got)
	}
	if strings.Contains(got, "spawn(") {
		t.Errorf("spawn must NOT appear when Modes=[task]; got:\n%s", got)
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
	if strings.Contains(got, "run_subagent") {
		t.Errorf("run_subagent must NOT appear for [background,task] modes; got:\n%s", got)
	}
	// Both background and task must be present.
	if !strings.Contains(got, "spawn(agent_id=") {
		t.Errorf("spawn(agent_id= must appear for background mode; got:\n%s", got)
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
	if !strings.Contains(got, `spawn(agent_id="ava"`) {
		t.Errorf("missing spawn(agent_id=\"ava\"; got:\n%s", got)
	}
	if !strings.Contains(got, `spawn(agent_id="ray"`) {
		t.Errorf("missing spawn(agent_id=\"ray\"; got:\n%s", got)
	}
}

// TestBuildDelegationContext_AllSkipped verifies that when all entries have empty
// labels (all unknown), the result is the clean cannot-delegate text.
func TestBuildDelegationContext_AllSkipped(t *testing.T) {
	targets := []delegationTarget{
		{ID: "ghost1", Label: "", Modes: nil, Depth: nil},
		{ID: "ghost2", Label: "", Modes: nil, Depth: nil},
	}
	got := buildDelegationContext(targets, 0)

	if !strings.Contains(got, "You cannot delegate to other agents") {
		t.Errorf("all-skipped must produce cannot-delegate text; got:\n%s", got)
	}
	if strings.Contains(got, "### →") {
		t.Errorf("no section headers expected when all skipped; got:\n%s", got)
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

	// ava section: only run_subagent; spawn and create_task must NOT appear for ava.
	// (ray also has run_subagent so we can't check absence globally — just check
	// the ava section specifically by looking for spawn with ava's id.)
	if strings.Contains(got, `spawn(agent_id="ava"`) {
		t.Errorf("spawn for ava must NOT appear when ava edge is await-only; got:\n%s", got)
	}
	if strings.Contains(got, `create_task(agent_id="ava"`) {
		t.Errorf("create_task for ava must NOT appear when ava edge is await-only; got:\n%s", got)
	}
	if !strings.Contains(got, `run_subagent(agent_id="ava"`) {
		t.Errorf("run_subagent for ava must appear; got:\n%s", got)
	}

	// ray section: all three tools.
	if !strings.Contains(got, `spawn(agent_id="ray"`) {
		t.Errorf("spawn for ray must appear (all modes); got:\n%s", got)
	}
	if !strings.Contains(got, `create_task(agent_id="ray"`) {
		t.Errorf("create_task for ray must appear (all modes); got:\n%s", got)
	}
	if !strings.Contains(got, `run_subagent(agent_id="ray"`) {
		t.Errorf("run_subagent for ray must appear (all modes); got:\n%s", got)
	}
}
