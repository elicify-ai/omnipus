package routing

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestResolveRoute_NeverPicksWorkerAsDefault verifies that a worker is never
// resolved as the routing default. Even when a (tampered) config marks a worker
// Default=true, resolveDefaultAgentID skips it and falls back to the first
// enabled chat-target agent — a worker must never receive inbound messages.
//
// BDD: Given a worker marked Default=true and an enabled base agent,
//
//	When ResolveRoute is called with no bindings,
//	Then the resolved agent is the base agent, never the worker.
func TestResolveRoute_NeverPicksWorkerAsDefault(t *testing.T) {
	enabled := true
	agents := []config.AgentConfig{
		// A worker that has somehow been marked default (config tampering).
		{ID: "worker", Type: config.AgentTypeWorker, Default: true, Enabled: &enabled},
		// A normal chat-target base agent.
		{ID: "mia", Enabled: &enabled},
	}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{Channel: "telegram"})
	if route.AgentID == "worker" {
		t.Fatalf("routing resolved to a worker (%q) — workers must never be the default", route.AgentID)
	}
	if route.AgentID != "mia" {
		t.Errorf("AgentID = %q, want %q (worker skipped, first enabled chat target)", route.AgentID, "mia")
	}
}

// TestResolveRoute_WorkerSortsFirstButSkipped verifies the first-enabled-agent
// fallback (no agent marked default) skips a worker even when it sorts/appears
// before the base agents.
func TestResolveRoute_WorkerSortsFirstButSkipped(t *testing.T) {
	enabled := true
	agents := []config.AgentConfig{
		// Worker appears first in the list and no agent is marked default.
		{ID: "aworker", Type: config.AgentTypeWorker, Enabled: &enabled},
		{ID: "jim", Enabled: &enabled},
	}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{Channel: "telegram"})
	if route.AgentID == "aworker" {
		t.Fatalf("first-enabled fallback picked a worker (%q) — workers are not chat targets", route.AgentID)
	}
	if route.AgentID != "jim" {
		t.Errorf("AgentID = %q, want %q (worker skipped in fallback)", route.AgentID, "jim")
	}
}

// TestResolveRoute_AllWorkersFallsBackToBuiltin verifies the last-resort path:
// when EVERY agent is a worker (degenerate config), routing returns the built-in
// default sentinel rather than a worker.
func TestResolveRoute_AllWorkersFallsBackToBuiltin(t *testing.T) {
	enabled := true
	agents := []config.AgentConfig{
		{ID: "w1", Type: config.AgentTypeWorker, Enabled: &enabled},
		{ID: "w2", Type: config.AgentTypeWorker, Enabled: &enabled},
	}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{Channel: "telegram"})
	if route.AgentID == "w1" || route.AgentID == "w2" {
		t.Fatalf("routing resolved to a worker (%q) when all agents are workers", route.AgentID)
	}
	if route.AgentID != DefaultAgentID {
		t.Errorf("AgentID = %q, want built-in default %q", route.AgentID, DefaultAgentID)
	}
}

// TestResolveRoute_ExplicitBindingToWorkerFallsBack verifies M1: an explicit
// channel-wildcard binding that targets a worker must NOT make the worker a live
// chat target. pickAgentID detects the worker and degrades to the base default.
//
// BDD: Given a base default agent and a worker, and a binding telegram → worker,
//
//	When ResolveRoute is called for telegram,
//	Then the resolved agent is the base default ("mia"), never the worker.
func TestResolveRoute_ExplicitBindingToWorkerFallsBack(t *testing.T) {
	enabled := true
	agents := []config.AgentConfig{
		{ID: "mia", Default: true, Enabled: &enabled},
		{ID: "worker", Type: config.AgentTypeWorker, Enabled: &enabled},
	}
	bindings := []config.AgentBinding{
		{AgentID: "worker", Match: config.BindingMatch{Channel: "telegram", AccountID: "*"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{Channel: "telegram"})
	if route.AgentID == "worker" {
		t.Fatalf("explicit binding resolved to a worker (%q) — workers are not chat targets (M1)", route.AgentID)
	}
	if route.AgentID != "mia" {
		t.Errorf("AgentID = %q, want %q (worker binding degrades to base default)", route.AgentID, "mia")
	}
}

// TestResolveRoute_IdentityAgentWorkerFallsBack verifies M1 on the identity
// override path (Priority 0): identity{kind:agent,id:worker} must not let the
// worker answer as a persona; it degrades to the base default.
//
// BDD: Given a base default agent and a worker, and identity{agent, worker},
//
//	When ResolveRoute is called,
//	Then the resolved agent is the base default ("mia"), never the worker.
func TestResolveRoute_IdentityAgentWorkerFallsBack(t *testing.T) {
	enabled := true
	agents := []config.AgentConfig{
		{ID: "mia", Default: true, Enabled: &enabled},
		{ID: "worker", Type: config.AgentTypeWorker, Enabled: &enabled},
	}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:  "telegram",
		Identity: &config.ChannelIdentity{Kind: "agent", ID: "worker"},
	})
	if route.AgentID == "worker" {
		t.Fatalf("identity override resolved to a worker (%q) — workers are not chat targets (M1)", route.AgentID)
	}
	if route.AgentID != "mia" {
		t.Errorf("AgentID = %q, want %q (worker identity degrades to base default)", route.AgentID, "mia")
	}
}

// TestResolveRoute_ExplicitBindingToBaseAgentResolves is the control for M1: an
// explicit binding to a normal (non-worker) base agent still resolves to it.
//
// BDD: Given two base agents and a binding telegram → "support",
//
//	When ResolveRoute is called for telegram,
//	Then the resolved agent is "support" (not the default), matched by binding.channel.
func TestResolveRoute_ExplicitBindingToBaseAgentResolves(t *testing.T) {
	enabled := true
	agents := []config.AgentConfig{
		{ID: "mia", Default: true, Enabled: &enabled},
		{ID: "support", Enabled: &enabled},
	}
	bindings := []config.AgentBinding{
		{AgentID: "support", Match: config.BindingMatch{Channel: "telegram", AccountID: "*"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{Channel: "telegram"})
	if route.AgentID != "support" {
		t.Errorf("AgentID = %q, want %q (binding to a base agent must resolve to it)", route.AgentID, "support")
	}
	if route.MatchedBy != "binding.channel" {
		t.Errorf("MatchedBy = %q, want %q", route.MatchedBy, "binding.channel")
	}
}
