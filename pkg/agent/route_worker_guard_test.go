package agent

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// registerInstance is a small test helper that wires a fully-formed AgentInstance
// (with workspace + context builder) into the loop's registry under id.
func registerInstance(t *testing.T, al *AgentLoop, cfg *config.Config, home, id string, typ config.AgentType) {
	t.Helper()
	ag := NewAgentInstance(&config.AgentConfig{ID: id, Name: id, Type: typ},
		&cfg.Agents.Defaults, cfg, &mockProvider{})
	ag.Home = filepath.Join(home, "agents", id)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(id, id)
	al.registry.mu.Lock()
	al.registry.agents[id] = ag
	al.registry.mu.Unlock()
}

// TestResolveMessageRoute_ExplicitWorkerAgentIDDegradesToDefault verifies RESIDUAL
// PATH 1: a message that explicitly addresses a worker via agent_id metadata must
// NOT let the worker answer as a persona. It degrades to the normal ResolveRoute
// cascade, which resolves the chat-target default ("mia"). The worker is invocable
// only via delegation, never as a chat target.
func TestResolveMessageRoute_ExplicitWorkerAgentIDDegradesToDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	// The route cascade reads cfg.Agents.List to resolve the default — give it a
	// chat-target default and a worker.
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "hans", Type: config.AgentTypeWorker},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCore)
	registerInstance(t, al, cfg, home, "hans", config.AgentTypeWorker)

	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: "sess-worker-explicit",
		Content:   "answer me directly",
		Metadata:  map[string]string{"agent_id": "hans"},
	}
	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID == "hans" || (agent != nil && agent.ID == "hans") {
		t.Fatalf(
			"explicit agent_id=worker resolved to the worker (route=%q) — workers are not chat targets",
			route.AgentID,
		)
	}
	if route.AgentID != "mia" {
		t.Errorf("AgentID = %q, want %q (worker degrades to base default)", route.AgentID, "mia")
	}
}

// TestResolveMessageRoute_WorkerHandoffPinClearedAndFallsBack verifies RESIDUAL
// PATH 2: a stale session handoff pin that points at a worker must be cleared and
// routing must fall back to the chat-target default. A worker pin is illegitimate
// (handoff now rejects worker targets), so resolution drops it.
func TestResolveMessageRoute_WorkerHandoffPinClearedAndFallsBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "hans", Type: config.AgentTypeWorker},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCore)
	registerInstance(t, al, cfg, home, "hans", config.AgentTypeWorker)

	const sessionID = "sess-worker-pin"
	// Simulate a stale pin to a worker (no explicit agent_id on the message).
	al.sessionActiveAgent.Store("session:"+sessionID, "hans")

	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: sessionID,
		Content:   "follow up",
	}
	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID == "hans" || (agent != nil && agent.ID == "hans") {
		t.Fatalf("worker pin resolved to the worker (route=%q) — workers are not chat targets", route.AgentID)
	}
	if route.AgentID != "mia" {
		t.Errorf("AgentID = %q, want %q (worker pin degrades to base default)", route.AgentID, "mia")
	}
	// The stale worker pin must have been deleted.
	if _, stale := al.sessionActiveAgent.Load("session:" + sessionID); stale {
		t.Error("stale worker handoff pin should have been cleared")
	}
}

// TestResolveMessageRoute_ExplicitBaseAgentIDStillResolves is the control: an
// explicit agent_id pointing at a NON-worker base agent still resolves to it,
// proving the worker guard does not break normal explicit routing.
func TestResolveMessageRoute_ExplicitBaseAgentIDStillResolves(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "jim"},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCore)
	registerInstance(t, al, cfg, home, "jim", config.AgentTypeCore)

	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: "sess-base-explicit",
		Content:   "hello jim",
		Metadata:  map[string]string{"agent_id": "jim"},
	}
	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID != "jim" || agent == nil || agent.ID != "jim" {
		t.Fatalf("explicit agent_id=jim (base agent) should resolve to jim, got route=%q", route.AgentID)
	}
}
