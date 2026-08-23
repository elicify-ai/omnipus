package agent

// bound_drift_route_test.go — ADR-029 WS-A agent-loop routing tests.
// Covers TDD plan items #10 (drift drop skips GetDefaultAgent), #11 (unbound
// default unchanged), #23d (routing-change audit event stub), and the
// no-double-emit regression guard (fix-wave finding #1).

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestResolveMessageRoute_BoundDrop_SkipsGetDefaultAgent verifies TDD #10 /
// US-5 AC-2: when ResolveRoute returns Drop=true (bound agent unresolvable),
// resolveMessageRoute must NOT call GetDefaultAgent. The error returned must
// indicate no agent is available. The driftDropped counter is NOT incremented
// here — resolveMessageRoute is a side-effect-free resolver; the counter is
// emitted exactly once by processMessage (the actual rejection site).
func TestResolveMessageRoute_BoundDrop_SkipsGetDefaultAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
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
	route, agent, err := al.resolveMessageRoute(msg)
	postDrift := al.GetDriftDropped()

	// Must return an error (no agent available).
	if err == nil {
		t.Fatal("resolveMessageRoute must return an error for a drift drop, got nil")
	}
	// Must NOT return the global default agent.
	if agent != nil && agent.ID == "mia" {
		t.Errorf("drift drop routed to global default agent 'mia'; must NOT use the default")
	}
	// route.Drop must be true so the caller (processMessage) can detect the drift
	// condition and emit the counter/audit exactly once.
	if !route.Drop {
		t.Error("resolveMessageRoute must return route.Drop=true for a drift drop")
	}
	// resolveMessageRoute is side-effect-free: it must NOT increment the counter.
	// The counter is emitted by processMessage (the actual rejection site).
	if postDrift != preDrift {
		t.Errorf(
			"driftDropped counter changed inside resolveMessageRoute: pre=%d post=%d; must stay unchanged (counter is emitted by processMessage)",
			preDrift,
			postDrift,
		)
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
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
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
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
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
	route, agent, err := al.resolveMessageRoute(msg)
	postDrift := al.GetDriftDropped()

	if err == nil {
		t.Fatal("resolveMessageRoute must return an error for a worker-agent drift drop")
	}
	if agent != nil && agent.ID == "mia" {
		t.Error("worker-agent drift drop must NOT route to the global default 'mia'")
	}
	// route.Drop must be true so processMessage can detect and emit exactly once.
	if !route.Drop {
		t.Error("resolveMessageRoute must return route.Drop=true for a worker-agent drift drop")
	}
	// Side-effect-free: counter must NOT increment here; processMessage is the emission point.
	if postDrift != preDrift {
		t.Errorf(
			"driftDropped changed inside resolveMessageRoute: pre=%d post=%d; must stay unchanged",
			preDrift,
			postDrift,
		)
	}
}

// TestGetDriftDropped_InitiallyZero verifies the counter starts at 0.
func TestGetDriftDropped_InitiallyZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	if n := al.GetDriftDropped(); n != 0 {
		t.Errorf("GetDriftDropped initially = %d, want 0", n)
	}
}

// ── No-double-emit regression guard (fix-wave finding #1) ────────────────────

// TestDriftDrop_SingleEmission_ViaProcessMessage is the regression guard for
// the no-double-emit fix.  It verifies that a single drifting inbound message
// results in EXACTLY ONE driftDropped increment when the message travels through
// the real dispatch path (resolveSteeringTarget → processMessage).
//
// Background: before the fix, resolveMessageRoute was not side-effect-free.
// Both resolveSteeringTarget (which discards the result on error) and
// processMessage (the actual rejection site) called resolveMessageRoute on the
// same message, so the counter and audit event fired TWICE per drop.
//
// The fix makes resolveMessageRoute purely a resolver (no counter/audit) and
// emits exactly once in processMessage.  This test drives both callers to prove
// the combined path counts to 1, not 2.
func TestDriftDrop_SingleEmission_ViaProcessMessage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		// "ray" is intentionally absent (deleted) — drift condition.
	}
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
	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCustom)

	msg := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.eu",
		ChatID:     "15555550100@s.whatsapp.net",
		Content:    "hello drift",
	}

	// Step 1: resolveSteeringTarget is the first call in the dispatch loop.
	// It must produce ZERO side effects for a drift-drop message.
	preSteering := al.GetDriftDropped()
	scope, _, ok := al.resolveSteeringTarget(msg)
	postSteering := al.GetDriftDropped()

	if ok {
		t.Errorf("resolveSteeringTarget returned ok=true for drift drop (scope=%q); expected false", scope)
	}
	if postSteering != preSteering {
		t.Errorf(
			"resolveSteeringTarget incremented driftDropped: pre=%d post=%d; must not emit (side-effect-free resolver)",
			preSteering,
			postSteering,
		)
	}

	// Step 2: processMessage is the single emission point.  The !ok dispatch
	// branch calls it.  Verify exactly ONE increment.
	preProcess := al.GetDriftDropped()
	_, _, err := al.processMessage(context.Background(), msg)
	postProcess := al.GetDriftDropped()

	if err == nil {
		t.Fatal("processMessage must return an error for a drift drop")
	}
	if postProcess != preProcess+1 {
		t.Errorf(
			"driftDropped after processMessage: pre=%d post=%d; want exactly +1 (single emission)",
			preProcess,
			postProcess,
		)
	}

	// Step 3: combined — simulate the full dispatch for a second message to
	// confirm idempotency: another message on the same bound instance should also
	// count as exactly one more increment (not two, not zero).
	preCombined := al.GetDriftDropped()
	scope2, _, ok2 := al.resolveSteeringTarget(msg)
	if ok2 {
		t.Errorf("resolveSteeringTarget second call returned ok=true (scope=%q); expected false", scope2)
	}
	//nolint:dogsled // only the GetDriftDropped() side effect matters here
	_, _, _ = al.processMessage(context.Background(), msg)
	postCombined := al.GetDriftDropped()

	if postCombined != preCombined+1 {
		t.Errorf(
			"second dispatch: driftDropped pre=%d post=%d; want exactly +1 per message (no double-count)",
			preCombined,
			postCombined,
		)
	}
}
