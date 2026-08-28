package agent

// instance_id_priority0_test.go — ADR-029 TDD #23b: O-2 false-green guard.
//
// The O-2 class of false-green tests exists because earlier tests built configs
// where the instance key ("whatsapp.eu") and the bare-type fallback ("whatsapp")
// both resolve identically — so a regression to the type-key fallback would still
// pass.  This test constructs a config where the two cases are DISTINCT:
//
//   - cfg.Channels["whatsapp.eu"] is bound to agent A ("ray")
//   - cfg.Channels["whatsapp"]   is ABSENT (or bound to a DIFFERENT agent B)
//
// It then:
//  1. Sends an inbound with InstanceID:"whatsapp.eu"  → must route to A (ray).
//  2. Sends an inbound with InstanceID:""             → InstanceID is empty, so
//     inboundInstanceID falls back to msg.Channel="whatsapp" → key "whatsapp" is
//     absent → BoundInstance=false → should NOT route to ray.
//
// Traces to: channel-instance-workspace-binding-spec.md §7 TDD #23b,
//            OBS-002, MAJ-002, FR-020.

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestInstanceID_DrivesPriority0_EndToEnd_StampedVsTypeKey is the O-2 false-green
// guard (TDD #23b).  It proves that Priority-0 identity resolution fires on the
// STAMPED InstanceID ("whatsapp.eu"), not the bare type-key fallback ("whatsapp").
//
// Config:
//   - cfg.Channels["whatsapp.eu"] → WorkspaceID="sales", Identity.ID="ray"
//   - "whatsapp" key is ABSENT → no binding for the bare-type fallback
//   - Registered agents: mia (default), ray
//
// Case A — stamped InstanceID:
//   - msg.InstanceID = "whatsapp.eu" → inboundInstanceID returns "whatsapp.eu"
//     → cfg.Channels["whatsapp.eu"].WorkspaceID != "" → BoundInstance=true
//     → ResolveRoute uses Priority-0 identity → routes to "ray"
//
// Case B — empty InstanceID (type-key fallback):
//   - msg.InstanceID = ""  (adapter did NOT stamp)
//   - msg.Channel    = "whatsapp" → inboundInstanceID returns "whatsapp"
//   - cfg.Channels["whatsapp"] does NOT exist → identity=nil, BoundInstance=false
//   - ResolveRoute falls through to default cascade → routes to "mia"
//
// A regression to the type-key fallback would cause Case A to return "mia"
// (same as Case B), which this test catches.
func TestInstanceID_DrivesPriority0_EndToEnd_StampedVsTypeKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Default: true},
		{ID: "ray"},
	}
	// Only the namespaced key is bound.  The bare "whatsapp" type key is
	// intentionally absent so the two resolution paths are distinguishable.
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"whatsapp.eu": {
			Type:        "whatsapp",
			Enabled:     true,
			WorkspaceID: "sales",
			Identity:    &config.ChannelIdentity{Kind: "agent", ID: "ray"},
		},
		// "whatsapp" key is absent — the bare-type fallback has no binding.
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	registerInstance(t, al, cfg, home, "mia", config.AgentTypeCustom)
	registerInstance(t, al, cfg, home, "ray", config.AgentTypeCustom)

	// ── Case A: stamped InstanceID drives Priority-0 to "ray" ───────────────
	stampedMsg := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "whatsapp.eu", // trusted stamp from the adapter
		ChatID:     "15555550100@s.whatsapp.net",
		Content:    "hello",
	}
	routeA, agentA, errA := al.resolveMessageRoute(stampedMsg)
	if errA != nil {
		t.Fatalf("Case A (stamped): resolveMessageRoute returned error: %v", errA)
	}
	if agentA == nil || agentA.ID != "ray" {
		t.Errorf("Case A (stamped): agent = %v, want ray — Priority-0 must fire on InstanceID 'whatsapp.eu'",
			agentA)
	}
	if routeA.AgentID != "ray" {
		t.Errorf("Case A (stamped): route.AgentID = %q, want %q", routeA.AgentID, "ray")
	}
	if routeA.MatchedBy != "identity.agent" {
		t.Errorf("Case A (stamped): route.MatchedBy = %q, want %q", routeA.MatchedBy, "identity.agent")
	}
	if routeA.Drop {
		t.Error("Case A (stamped): route.Drop is true; must be false for a valid bound agent")
	}

	// ── Case B: empty InstanceID falls back to the bare type key → default ──
	// cfg.Channels["whatsapp"] is absent → no bound instance → normal cascade.
	unstampedMsg := bus.InboundMessage{
		Channel:    "whatsapp",
		InstanceID: "", // adapter did NOT stamp — inboundInstanceID falls back to Channel
		ChatID:     "15555550100@s.whatsapp.net",
		Content:    "hello",
	}
	routeB, agentB, errB := al.resolveMessageRoute(unstampedMsg)
	if errB != nil {
		t.Fatalf("Case B (type fallback): resolveMessageRoute returned error: %v", errB)
	}
	if agentB == nil {
		t.Fatal("Case B (type fallback): resolveMessageRoute returned nil agent; want the default 'mia'")
	}
	// The bare-type fallback has no bound config → must NOT route to "ray".
	if agentB.ID == "ray" {
		t.Errorf(
			"Case B (type fallback): routed to 'ray' — this would be a false-green regression; the bare 'whatsapp' key has no binding so the default (mia) should be used",
		)
	}
	if agentB.ID != "mia" {
		t.Errorf("Case B (type fallback): agent = %q, want %q (default cascade)", agentB.ID, "mia")
	}
	if routeB.AgentID != "mia" {
		t.Errorf("Case B (type fallback): route.AgentID = %q, want %q", routeB.AgentID, "mia")
	}
	if routeB.Drop {
		t.Error("Case B (type fallback): route.Drop is true for an unbound default route; must be false")
	}

	// ── Differentiation guard: the two cases must resolve to DIFFERENT agents ─
	// This is the core of the O-2 guard: if a regression collapsed both to the
	// type-key fallback, both cases would route to the same agent and this check
	// would catch it.
	if routeA.AgentID == routeB.AgentID {
		t.Errorf(
			"Differentiation FAILED: both stamped and unstamped inbounds route to the same agent %q — "+
				"this is the O-2 false-green class; the stamped path must use 'whatsapp.eu' (→ray) "+
				"and the unstamped path must use 'whatsapp' fallback (→mia)",
			routeA.AgentID,
		)
	}
}
