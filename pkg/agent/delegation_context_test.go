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

func TestBuildDelegationContext_NilPolicy(t *testing.T) {
	got := buildDelegationContext(nil, resolveTestLabel)
	want := "## Delegation\nYou cannot delegate to other agents — complete the task yourself."
	if got != want {
		t.Errorf("nil policy:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestBuildDelegationContext_EmptyTo(t *testing.T) {
	policy := &config.DelegationPolicy{
		To: []config.AgentRef{}, // explicit empty slice
	}
	got := buildDelegationContext(policy, resolveTestLabel)
	want := "## Delegation\nYou cannot delegate to other agents — complete the task yourself."
	if got != want {
		t.Errorf("empty To:\ngot:  %q\nwant: %q", got, want)
	}
}

func TestBuildDelegationContext_SingleTargetAllModes(t *testing.T) {
	policy := &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ava"}},
		Modes: nil, // nil = all three modes
		Depth: ptr(3),
	}
	got := buildDelegationContext(policy, resolveTestLabel)

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
	// spawn must use agent_id= (NOT agent=). Anchor on distinctive prefix.
	if !strings.Contains(got, `spawn(agent_id=`) {
		t.Errorf("missing spawn(agent_id= tool call; got:\n%s", got)
	}
	// create_task (NOT task_create — the retired name). Must have agent_id param.
	if !strings.Contains(got, `create_task(agent_id=`) {
		t.Errorf("missing create_task(agent_id= tool call; got:\n%s", got)
	}
	// Correct agent ID in spawn and create_task tool calls.
	if !strings.Contains(got, `"ava"`) {
		t.Errorf("missing concrete agent id 'ava' in tool calls; got:\n%s", got)
	}
	// run_subagent now targets a named agent by id (await mode).
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
	policy := &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ava"}},
		Modes: []config.DelegationMode{config.DelegationModeAwait},
	}
	got := buildDelegationContext(policy, resolveTestLabel)

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
	// Footer must note the mode restriction.
	if !strings.Contains(got, "allowed modes: await") {
		t.Errorf("missing mode footer; got:\n%s", got)
	}
	// run_subagent targets the named agent by id (await mode).
	if !strings.Contains(got, `run_subagent(agent_id="ava"`) {
		t.Errorf("run_subagent must target the named agent via agent_id; got:\n%s", got)
	}
}

func TestBuildDelegationContext_TwoTargets(t *testing.T) {
	policy := &config.DelegationPolicy{
		To: []config.AgentRef{
			{Kind: "local", ID: "ava"},
			{Kind: "local", ID: "ray"},
		},
		Modes: nil, // all modes
		Depth: nil, // uncapped
	}
	got := buildDelegationContext(policy, resolveTestLabel)

	// Both subagent sections must be present.
	if !strings.Contains(got, "### → Ava (Builder: implementation & code)") {
		t.Errorf("missing ava section; got:\n%s", got)
	}
	if !strings.Contains(got, "### → Ray (Scout: research & browsing)") {
		t.Errorf("missing ray section; got:\n%s", got)
	}
	// Each target's ID must appear in spawn(agent_id= and create_task(agent_id= calls.
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
	policy := &config.DelegationPolicy{
		To: []config.AgentRef{
			{Kind: "local", ID: "nonexistent-agent"},
		},
		Modes: nil,
	}
	got := buildDelegationContext(policy, resolveTestLabel)

	// All targets skipped → cannot-delegate message.
	if !strings.Contains(got, "You cannot delegate to other agents") {
		t.Errorf("expected cannot-delegate text when all targets skipped; got:\n%s", got)
	}
	// No section header should appear.
	if strings.Contains(got, "### →") {
		t.Errorf("unknown target must be skipped but a section header appeared; got:\n%s", got)
	}
	// No tool lines expected.
	if strings.Contains(got, "run_subagent") {
		t.Errorf("no tool lines expected when all targets skipped; got:\n%s", got)
	}
}

func TestBuildDelegationContext_WildcardTarget(t *testing.T) {
	policy := &config.DelegationPolicy{
		To: []config.AgentRef{
			{Kind: "local", ID: "*"},
		},
		Modes: nil,
		Depth: nil,
	}
	// resolveLabel must NOT be called for "*"; pass a function that panics to verify.
	panicResolver := func(id string) (string, bool) {
		t.Fatalf("resolveLabel must not be called for wildcard; called with id=%q", id)
		return "", false
	}
	got := buildDelegationContext(policy, panicResolver)

	if !strings.Contains(got, "### → any available agent") {
		t.Errorf("wildcard label wrong; got:\n%s", got)
	}
	// The wildcard section must instruct the agent to replace <id> — NOT emit "agent_id=\"*\"".
	if strings.Contains(got, `agent_id="*"`) {
		t.Errorf("must not emit uncallable agent_id=\"*\"; got:\n%s", got)
	}
	// Must hint at using a concrete id.
	if !strings.Contains(got, "<id>") {
		t.Errorf("wildcard section must include <id> placeholder hint; got:\n%s", got)
	}
}

func TestBuildDelegationContext_DepthUncapped(t *testing.T) {
	policy := &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ava"}},
		Depth: nil,
	}
	got := buildDelegationContext(policy, resolveTestLabel)
	if !strings.Contains(got, "max chain depth: uncapped") {
		t.Errorf("nil Depth must render as uncapped; got:\n%s", got)
	}
}

// TestBuildDelegationContext_DepthZero verifies that Depth=ptr(0) is treated as
// uncapped (ResolveDelegationDepth returns 0 for <=0 Depth), matching enforcement.
func TestBuildDelegationContext_DepthZero(t *testing.T) {
	policy := &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ava"}},
		Depth: ptr(0),
	}
	got := buildDelegationContext(policy, resolveTestLabel)
	if !strings.Contains(got, "max chain depth: uncapped") {
		t.Errorf("Depth=ptr(0) must render as uncapped (enforcement treats it as uncapped); got:\n%s", got)
	}
}

func TestBuildDelegationContext_DepthSet(t *testing.T) {
	policy := &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ava"}},
		Depth: ptr(3),
	}
	got := buildDelegationContext(policy, resolveTestLabel)
	if !strings.Contains(got, "max chain depth: 3") {
		t.Errorf("Depth=3 must render as '3'; got:\n%s", got)
	}
}

func TestBuildDelegationContext_BackgroundModeOnly(t *testing.T) {
	policy := &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ava"}},
		Modes: []config.DelegationMode{config.DelegationModeBackground},
	}
	got := buildDelegationContext(policy, resolveTestLabel)

	if strings.Contains(got, "run_subagent") {
		t.Errorf("run_subagent must NOT appear when Modes=[background]; got:\n%s", got)
	}
	// Anchor on spawn( to avoid substring match with check_spawn_status.
	if !strings.Contains(got, "spawn(agent_id=") {
		t.Errorf("spawn(agent_id= must appear for background mode; got:\n%s", got)
	}
	if strings.Contains(got, "create_task") {
		t.Errorf("create_task must NOT appear when Modes=[background]; got:\n%s", got)
	}
}

func TestBuildDelegationContext_TaskModeOnly(t *testing.T) {
	policy := &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ava"}},
		Modes: []config.DelegationMode{config.DelegationModeTask},
	}
	got := buildDelegationContext(policy, resolveTestLabel)

	if strings.Contains(got, "run_subagent") {
		t.Errorf("run_subagent must NOT appear when Modes=[task]; got:\n%s", got)
	}
	// Anchor on spawn( to avoid substring match with check_spawn_status.
	if strings.Contains(got, "spawn(") {
		t.Errorf("spawn must NOT appear when Modes=[task]; got:\n%s", got)
	}
	if !strings.Contains(got, "create_task(agent_id=") {
		t.Errorf("create_task(agent_id= must appear for task mode; got:\n%s", got)
	}
}

// TestBuildDelegationContext_TwoModeSubset verifies a [background, task] subset.
func TestBuildDelegationContext_TwoModeSubset(t *testing.T) {
	policy := &config.DelegationPolicy{
		To: []config.AgentRef{{Kind: "local", ID: "ava"}},
		Modes: []config.DelegationMode{
			config.DelegationModeBackground,
			config.DelegationModeTask,
		},
	}
	got := buildDelegationContext(policy, resolveTestLabel)

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
	// Footer must list both modes.
	if !strings.Contains(got, "allowed modes:") {
		t.Errorf("missing allowed modes footer; got:\n%s", got)
	}
	if !strings.Contains(got, "background") {
		t.Errorf("footer must include 'background'; got:\n%s", got)
	}
	if !strings.Contains(got, "task") {
		t.Errorf("footer must include 'task'; got:\n%s", got)
	}
}

// TestBuildDelegationContext_MixedTargets verifies that known targets render and
// unknown targets are skipped, leaving only the known ones' sections.
func TestBuildDelegationContext_MixedTargets(t *testing.T) {
	policy := &config.DelegationPolicy{
		To: []config.AgentRef{
			{Kind: "local", ID: "ava"},
			{Kind: "local", ID: "nonexistent"},
			{Kind: "local", ID: "ray"},
		},
		Modes: nil,
		Depth: nil,
	}
	got := buildDelegationContext(policy, resolveTestLabel)

	// ava and ray must render.
	if !strings.Contains(got, "### → Ava (Builder: implementation & code)") {
		t.Errorf("missing ava section; got:\n%s", got)
	}
	if !strings.Contains(got, "### → Ray (Scout: research & browsing)") {
		t.Errorf("missing ray section; got:\n%s", got)
	}
	// nonexistent must be silently dropped (no section for it).
	if strings.Contains(got, "nonexistent") {
		t.Errorf("nonexistent target must be skipped; got:\n%s", got)
	}
	// Tool calls for each real target.
	if !strings.Contains(got, `spawn(agent_id="ava"`) {
		t.Errorf("missing spawn(agent_id=\"ava\"; got:\n%s", got)
	}
	if !strings.Contains(got, `spawn(agent_id="ray"`) {
		t.Errorf("missing spawn(agent_id=\"ray\"; got:\n%s", got)
	}
}

// TestBuildDelegationContext_AllSkipped verifies that when all To entries are
// skipped (all unknown), the result is the clean cannot-delegate text.
func TestBuildDelegationContext_AllSkipped(t *testing.T) {
	policy := &config.DelegationPolicy{
		To: []config.AgentRef{
			{Kind: "local", ID: "ghost1"},
			{Kind: "local", ID: "ghost2"},
		},
		Modes: nil,
	}
	got := buildDelegationContext(policy, resolveTestLabel)

	if !strings.Contains(got, "You cannot delegate to other agents") {
		t.Errorf("all-skipped must produce cannot-delegate text; got:\n%s", got)
	}
	if strings.Contains(got, "### →") {
		t.Errorf("no section headers expected when all skipped; got:\n%s", got)
	}
}

// TestBuildDelegationContext_NonLocalKindSkipped verifies that refs with a
// non-local Kind (e.g. "remote-a2a") are skipped, mirroring CanSpawnSubagent.
func TestBuildDelegationContext_NonLocalKindSkipped(t *testing.T) {
	policy := &config.DelegationPolicy{
		To: []config.AgentRef{
			{Kind: "remote-a2a", ID: "ava"},
		},
		Modes: nil,
	}
	got := buildDelegationContext(policy, resolveTestLabel)

	// remote-a2a refs must be skipped → cannot-delegate text.
	if !strings.Contains(got, "You cannot delegate to other agents") {
		t.Errorf("remote-a2a ref must be skipped; got:\n%s", got)
	}
	if strings.Contains(got, "### →") {
		t.Errorf("no section headers expected for remote-a2a; got:\n%s", got)
	}
}

// TestBuildDelegationContext_UnknownModeFooter verifies that an unrecognized
// mode value is omitted from the footer (no tool line was emitted for it).
func TestBuildDelegationContext_UnknownModeFooter(t *testing.T) {
	policy := &config.DelegationPolicy{
		To: []config.AgentRef{{Kind: "local", ID: "ava"}},
		// Mix a known mode with a bogus one.
		Modes: []config.DelegationMode{
			config.DelegationModeBackground,
			config.DelegationMode("unicorn"), // unrecognized
		},
	}
	got := buildDelegationContext(policy, resolveTestLabel)

	// spawn should appear (background is active).
	if !strings.Contains(got, "spawn(agent_id=") {
		t.Errorf("spawn(agent_id= must appear for background mode; got:\n%s", got)
	}
	// "unicorn" must NOT appear in the footer.
	if strings.Contains(got, "unicorn") {
		t.Errorf("unrecognized mode 'unicorn' must not appear in footer; got:\n%s", got)
	}
	// footer must still list "background".
	if !strings.Contains(got, "background") {
		t.Errorf("footer must list 'background'; got:\n%s", got)
	}
}
