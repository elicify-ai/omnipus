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
