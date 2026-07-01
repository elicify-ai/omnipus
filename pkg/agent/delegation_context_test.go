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
	// All three tools must appear.
	if !strings.Contains(got, "run_subagent") {
		t.Error("missing run_subagent tool")
	}
	if !strings.Contains(got, "spawn") {
		t.Error("missing spawn tool")
	}
	if !strings.Contains(got, "task_create") {
		t.Error("missing task_create tool")
	}
	// Correct agent ID in the tool calls.
	if !strings.Contains(got, `"ava"`) {
		t.Errorf("missing concrete agent id 'ava' in tool calls; got:\n%s", got)
	}
	// Depth.
	if !strings.Contains(got, "max chain depth: 3") {
		t.Errorf("missing depth; got:\n%s", got)
	}
}

func TestBuildDelegationContext_ModesAwaitOnly(t *testing.T) {
	policy := &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ava"}},
		Modes: []config.DelegationMode{config.DelegationModeAwait},
	}
	got := buildDelegationContext(policy, resolveTestLabel)

	// Only run_subagent must appear; spawn and task_create must NOT.
	if !strings.Contains(got, "run_subagent") {
		t.Error("missing run_subagent for await mode")
	}
	if strings.Contains(got, "spawn") {
		t.Errorf("spawn must NOT appear when Modes=[await]; got:\n%s", got)
	}
	if strings.Contains(got, "task_create") {
		t.Errorf("task_create must NOT appear when Modes=[await]; got:\n%s", got)
	}
	// Footer must note the mode restriction.
	if !strings.Contains(got, "allowed modes: await") {
		t.Errorf("missing mode footer; got:\n%s", got)
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
	// Each target's ID must appear in tool calls.
	if !strings.Contains(got, `"ava"`) {
		t.Errorf("missing ava id in tool calls; got:\n%s", got)
	}
	if !strings.Contains(got, `"ray"`) {
		t.Errorf("missing ray id in tool calls; got:\n%s", got)
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

	// The unknown target must be skipped, so no ### header for it.
	if strings.Contains(got, "### →") {
		t.Errorf("unknown target must be skipped but a section header appeared; got:\n%s", got)
	}
	// Because ALL targets were skipped, there are no tool lines.
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
	// Tools should still list the wildcard id "*".
	if !strings.Contains(got, `"*"`) {
		t.Errorf("wildcard id must appear in tool calls; got:\n%s", got)
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
	if !strings.Contains(got, "spawn") {
		t.Error("spawn must appear for background mode")
	}
	if strings.Contains(got, "task_create") {
		t.Errorf("task_create must NOT appear when Modes=[background]; got:\n%s", got)
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
	if strings.Contains(got, "spawn") {
		t.Errorf("spawn must NOT appear when Modes=[task]; got:\n%s", got)
	}
	if !strings.Contains(got, "task_create") {
		t.Error("task_create must appear for task mode")
	}
}
