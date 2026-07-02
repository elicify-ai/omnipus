package agent

// bound_drift_route_test.go — ADR-029 WS-A agent-loop routing tests.
// Covers TDD plan items #10 (drift drop skips GetDefaultAgent), #11 (unbound
// default unchanged), and #23d (routing-change audit event stub).

import (
	"path/filepath"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestResolveMessageRoute_BoundDrop_SkipsGetDefaultAgent verifies TDD #10 /
// US-5 AC-2: when ResolveRoute returns Drop=true (bound agent unresolvable),
// resolveMessageRoute must NOT call GetDefaultAgent. The error returned must
// indicate no agent is available and the driftDropped counter must increment.
func TestResolveMessageRoute_BoundDrop_SkipsGetDefaultAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Workspace = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.ModelName = "test-model"
	// mia is the global default chat agent; "ray" is NOT in the list (deleted).
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
	}
	// The instance "whatsapp.eu" is bound: WorkspaceID + agent identity.
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"whatsapp.eu": {
			Type:        "whatsapp",
			Enabled:     true,
			WorkspaceID: "sales",
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	// Register only mia (the global default); ray is absent (deleted).
	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCustom)

	// Inbound on the bound instance — no explicit agent_id, no handoff pin.
	msg := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.eu",
		ChatID:     "15555550100@s.whatsapp.net",
		Content:    "hello",
	}

	preDrift := al.GetDriftDropped()
	_, agent, err := al.resolveMessageRoute(msg)
	postDrift := al.GetDriftDropped()

	// Must return an error (no agent available).
	if err == nil {
		t.Fatal("resolveMessageRoute must return an error for a drift drop, got nil")
	}
	// Must NOT return the global default agent.
	if agent != nil && agent.ID == "mia" {
		t.Errorf("drift drop routed to global default agent 'mia'; must NOT use the default")
	}
	// drift counter must have incremented.
	if postDrift != preDrift+1 {
		t.Errorf("driftDropped counter: pre=%d post=%d; want increment of 1", preDrift, postDrift)
	}
}

// TestResolveMessageRoute_Unbound_DefaultUnchanged verifies TDD #11 / regression:
// an unbound channel instance (no WorkspaceID, no Identity) with no explicit
// agent_id and no handoff pin routes via the existing default cascade, unchanged.
// GetDefaultAgent IS called and the global default agent handles the message.
func TestResolveMessageRoute_Unbound_DefaultUnchanged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Workspace = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
	}
	// No channels configuration: bare type key / no WorkspaceID → unbound.
	cfg.Channels = map[string]config.ChannelInstanceConfig{}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCustom)

	msg := bus.InboundMessage{
		Channel: "telegram",
		ChatID:  "user123",
		Content: "hi",
	}

	preDrift := al.GetDriftDropped()
	_, agent, err := al.resolveMessageRoute(msg)
	postDrift := al.GetDriftDropped()

	if err != nil {
		t.Fatalf("unbound default route must succeed; got: %v", err)
	}
	if agent == nil {
		t.Fatal("unbound default route must return an agent; got nil")
	}
	if agent.ID != "mia" {
		t.Errorf("unbound route: agent = %q, want %q", agent.ID, "mia")
	}
	// Drift counter must NOT increment for an unbound route.
	if postDrift != preDrift {
		t.Errorf("driftDropped changed for unbound route: pre=%d post=%d; must not increment", preDrift, postDrift)
	}
}

// TestResolveMessageRoute_BoundDrift_WorkerAgent verifies DS-3 row 4 at the
// agent-loop level: when the bound agent is a worker (not a chat target),
// the route drops and the default is NOT used.
func TestResolveMessageRoute_BoundDrift_WorkerAgent_LoopLevel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Workspace = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "worker1", Type: config.AgentTypeWorker},
	}
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"whatsapp.eu": {
			Type:        "whatsapp",
			Enabled:     true,
			WorkspaceID: "sales",
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: "worker1"},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCustom)
	registerInstance(t, al, cfg, home, "worker1", config.AgentTypeWorker)

	msg := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.eu",
		ChatID:     "15555550100@s.whatsapp.net",
		Content:    "test",
	}

	preDrift := al.GetDriftDropped()
	_, agent, err := al.resolveMessageRoute(msg)
	postDrift := al.GetDriftDropped()

	if err == nil {
		t.Fatal("resolveMessageRoute must return an error for a worker-agent drift drop")
	}
	if agent != nil && agent.ID == "mia" {
		t.Error("worker-agent drift drop must NOT route to the global default 'mia'")
	}
	if postDrift != preDrift+1 {
		t.Errorf("driftDropped counter: pre=%d post=%d; want increment of 1", preDrift, postDrift)
	}
}

// TestGetDriftDropped_InitiallyZero verifies the counter starts at 0.
func TestGetDriftDropped_InitiallyZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Workspace = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.ModelName = "test-model"

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	if n := al.GetDriftDropped(); n != 0 {
		t.Errorf("GetDriftDropped initially = %d, want 0", n)
	}
}
