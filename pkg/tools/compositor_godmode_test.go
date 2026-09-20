// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// compositor_godmode_test.go — god mode (O14) as it interacts with the
// global×agent policy merge (compositor.go::resolveEffectivePolicyWith).
//
// Oracle discipline: every expected value below is derived from god mode's
// stated contract — "god mode floors the GLOBAL layer at allow and then runs
// the normal merge unchanged" — combined with the merge's own documented rule
// (deny > ask > allow; a side with no entry does not vote). None of it is read
// back off the implementation. Concretely that contract forces:
//
//	global allow × agent deny   → deny   (the agent's own ceiling survives)
//	global allow × agent ask    → ask    (an agent-level ask still prompts)
//	global allow × agent allow  → allow
//	global allow × agent absent → allow  (the global side alone decides)
//
// The first row is the whole point: god mode lifts the OPERATOR's restrictions,
// never an agent's own. Before this was fixed, god mode short-circuited ahead
// of the merge and returned "allow" for everything, silently erasing the
// deliberately narrow tool ceilings that system agents (the Judge,
// PlanSupervisor) depend on.
package tools

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestResolveEffectivePolicy_GodMode_AgentCeilingSurvives is the key property:
// with god mode ON, a tool the AGENT denies still resolves "deny". An agent-side
// "ask" likewise still resolves "ask" — god mode removes global prompting, not
// an agent's own.
func TestResolveEffectivePolicy_GodMode_AgentCeilingSurvives(t *testing.T) {
	// The global side is deliberately hostile (a blanket deny wildcard) to prove
	// god mode really does floor the GLOBAL layer at allow: without that floor,
	// "fetch_url" and "anything.else" below would come back deny.
	cfg := &ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{
			"system.exec": config.ToolPolicyDeny,  // agent deny  → deny
			"fetch_url":   config.ToolPolicyAsk,   // agent ask   → ask
			"read_file":   config.ToolPolicyAllow, // agent allow → allow
		},
		GlobalPolicies: map[string]config.ToolPolicy{"system.*": config.ToolPolicyDeny},
		GodMode:        true,
	}
	for _, tc := range []struct {
		tool string
		want string
		why  string
	}{
		{"system.exec", "deny", "an agent's own deny is its tool ceiling; god mode must not lift it"},
		{"fetch_url", "ask", "an agent-level ask still prompts under god mode"},
		{"read_file", "allow", "agent allow × god-mode global allow is allow"},
		{"anything.else", "allow", "no agent entry: the god-mode global allow alone decides"},
	} {
		if got := ResolveEffectivePolicy(cfg, tc.tool); got != tc.want {
			t.Errorf("god mode: ResolveEffectivePolicy(%q) = %q, want %q — %s", tc.tool, got, tc.want, tc.why)
		}
	}
}

// TestResolveEffectivePolicy_GodMode_LiftsGlobalCeiling is the other half of the
// contract and the differentiator for the test above: the SAME global deny that
// is overridden here is what would otherwise decide these tools. Without this
// case, the test above could pass for a degenerate implementation that ignored
// god mode entirely.
func TestResolveEffectivePolicy_GodMode_LiftsGlobalCeiling(t *testing.T) {
	policies := func(god bool) *ToolPolicyCfg {
		return &ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{},
			GlobalPolicies: map[string]config.ToolPolicy{
				"exec":      config.ToolPolicyDeny,
				"fetch_url": config.ToolPolicyAsk,
			},
			GodMode: god,
		}
	}
	// Negative control: god mode OFF, the global ceiling decides.
	if got := ResolveEffectivePolicy(policies(false), "exec"); got != "deny" {
		t.Fatalf("god mode off: exec = %q, want deny (negative control)", got)
	}
	if got := ResolveEffectivePolicy(policies(false), "fetch_url"); got != "ask" {
		t.Fatalf("god mode off: fetch_url = %q, want ask (negative control)", got)
	}
	// God mode ON: the global layer is floored at allow, and with no agent
	// entry on either tool the global side alone decides.
	if got := ResolveEffectivePolicy(policies(true), "exec"); got != "allow" {
		t.Errorf("god mode on: exec = %q, want allow (global deny must be lifted)", got)
	}
	if got := ResolveEffectivePolicy(policies(true), "fetch_url"); got != "allow" {
		t.Errorf("god mode on: fetch_url = %q, want allow (global prompting must be removed)", got)
	}
}

// TestResolveEffectivePolicy_GodMode_NoCoverageResolvesAllowWithoutErrorLog
// pins the fail-closed branch's behaviour under god mode. A tool with no entry
// on EITHER side is a coverage bug and is logged at Error + denied — but under
// god mode the global side is never empty, so that branch must NOT fire: the
// tool resolves "allow" and nothing is logged at Error.
func TestResolveEffectivePolicy_GodMode_NoCoverageResolvesAllowWithoutErrorLog(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg := &ToolPolicyCfg{
		Policies:       map[string]config.ToolPolicy{},
		GlobalPolicies: map[string]config.ToolPolicy{},
		GodMode:        true,
	}
	if got := ResolveEffectivePolicy(cfg, "totally_uncovered_tool"); got != "allow" {
		t.Errorf("god mode, no coverage on either side: got %q, want allow", got)
	}
	if strings.Contains(buf.String(), "level=ERROR") {
		t.Errorf("god mode must not fire the no-coverage Error log; captured: %s", buf.String())
	}

	// Differentiation: the SAME uncovered tool with god mode off must still hit
	// the fail-closed branch — proving the assertion above is about god mode and
	// not about a logger that never emits.
	buf.Reset()
	cfg.GodMode = false
	if got := ResolveEffectivePolicy(cfg, "totally_uncovered_tool"); got != "deny" {
		t.Errorf("god mode off, no coverage on either side: got %q, want deny (fail-closed)", got)
	}
	if !strings.Contains(buf.String(), "level=ERROR") {
		t.Errorf("god mode off: expected the no-coverage Error log, captured: %s", buf.String())
	}
}

// TestResolveEffectivePolicy_GodModeOff_RestoresDecisions verifies the override
// is non-destructive: clearing GodMode restores the original deny/ask decisions.
func TestResolveEffectivePolicy_GodModeOff_RestoresDecisions(t *testing.T) {
	cfg := &ToolPolicyCfg{
		Policies:       map[string]config.ToolPolicy{"system.exec": "deny", "fetch_url": "ask"},
		GlobalPolicies: map[string]config.ToolPolicy{},
		GodMode:        false,
	}
	if got := ResolveEffectivePolicy(cfg, "system.exec"); got != "deny" {
		t.Fatalf("god-mode off: system.exec = %q, want deny", got)
	}
	if got := ResolveEffectivePolicy(cfg, "fetch_url"); got != "ask" {
		t.Fatalf("god-mode off: web_fetch = %q, want ask", got)
	}
}

// TestResolveEffectivePolicy_GodMode_DoesNotMutatePolicyMaps pins the
// non-destructive property structurally: resolving under god mode must leave
// both policy maps byte-for-byte as they were, so flipping GodMode back off
// restores the prior decision exactly.
func TestResolveEffectivePolicy_GodMode_DoesNotMutatePolicyMaps(t *testing.T) {
	cfg := &ToolPolicyCfg{
		Policies:       map[string]config.ToolPolicy{"exec": config.ToolPolicyDeny},
		GlobalPolicies: map[string]config.ToolPolicy{"exec": config.ToolPolicyAsk, "read_file": config.ToolPolicyAsk},
		GodMode:        true,
	}
	for _, name := range []string{"exec", "read_file", "never_mentioned"} {
		ResolveEffectivePolicy(cfg, name)
	}
	if got, want := cfg.Policies["exec"], config.ToolPolicyDeny; got != want {
		t.Errorf("cfg.Policies[exec] mutated: %q, want %q", got, want)
	}
	if got, want := cfg.GlobalPolicies["exec"], config.ToolPolicyAsk; got != want {
		t.Errorf("cfg.GlobalPolicies[exec] mutated: %q, want %q", got, want)
	}
	if got, want := cfg.GlobalPolicies["read_file"], config.ToolPolicyAsk; got != want {
		t.Errorf("cfg.GlobalPolicies[read_file] mutated: %q, want %q", got, want)
	}
	if len(cfg.Policies) != 1 || len(cfg.GlobalPolicies) != 2 {
		t.Errorf("policy maps gained entries: Policies=%v GlobalPolicies=%v", cfg.Policies, cfg.GlobalPolicies)
	}
	// Clearing god mode restores the pre-god-mode decisions exactly.
	cfg.GodMode = false
	if got := ResolveEffectivePolicy(cfg, "exec"); got != "deny" {
		t.Errorf("after clearing god mode: exec = %q, want deny", got)
	}
	if got := ResolveEffectivePolicy(cfg, "read_file"); got != "ask" {
		t.Errorf("after clearing god mode: read_file = %q, want ask", got)
	}
}
