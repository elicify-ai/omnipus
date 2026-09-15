package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

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
