package agent

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestResolveMessageRoute_ExplicitAgentIDOverridesHandoffOverride pins that
// when a message carries an explicit agent_id AND a stale "session:" override
// exists for that session from a prior handoff, the explicit selection wins and
// the override is cleared. This prevents Ray's tool calls leaking into Jim's
// session after the user switches the SPA dropdown back to Jim.
func TestResolveMessageRoute_ExplicitAgentIDOverridesHandoffOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	for _, id := range []string{"jim", "ray"} {
		ag := NewAgentInstance(&config.AgentConfig{ID: id, Name: id},
			&cfg.Agents.Defaults, cfg, &mockProvider{})
		ag.Home = filepath.Join(home, "agents", id)
		ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(id, id)
		al.registry.mu.Lock()
		al.registry.agents[id] = ag
		al.registry.mu.Unlock()
	}

	const sessionID = "sess-leak-1"
	// Simulate the state left by a prior Mia → Ray handoff, keyed by session_id.
	al.sessionActiveAgent.Store("session:"+sessionID, "ray")

	// User picks Jim in the SPA dropdown — webchat sends agent_id=jim with the session_id.
	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: sessionID,
		Content:   "what is my name",
		Metadata:  map[string]string{"agent_id": "jim"},
	}
	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID != "jim" {
		t.Fatalf("explicit agent_id=jim was overridden — routed to %q", route.AgentID)
	}
	if agent == nil || agent.ID != "jim" {
		t.Fatalf("returned agent should be jim, got %+v", agent)
	}
	wantSK := "agent:jim:session:" + sessionID
	if route.SessionKey != wantSK {
		t.Errorf("session key = %q, want %q", route.SessionKey, wantSK)
	}
	// Stale override must be gone so future no-agent-id messages route to jim.
	if _, stale := al.sessionActiveAgent.Load("session:" + sessionID); stale {
		t.Error("explicit agent_id should have cleared the stale handoff override")
	}
}

// TestResolveMessageRoute_HandoffOverrideStillAppliesWhenNoExplicitAgentID
// confirms the "session:" override still routes correctly when the message has
// no explicit agent_id, and that the session key uses the new formula.
func TestResolveMessageRoute_HandoffOverrideStillAppliesWhenNoExplicitAgentID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	ag := NewAgentInstance(&config.AgentConfig{ID: "ray", Name: "ray"},
		&cfg.Agents.Defaults, cfg, &mockProvider{})
	ag.Home = filepath.Join(home, "agents", "ray")
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo("ray", "ray")
	al.registry.mu.Lock()
	al.registry.agents["ray"] = ag
	al.registry.mu.Unlock()

	const sessionID = "sess-leak-2"
	// Set override using the new session-scoped key.
	al.sessionActiveAgent.Store("session:"+sessionID, "ray")

	msg := bus.InboundMessage{
		Channel:   "webchat",
		SessionID: sessionID,
		Content:   "follow up after handoff with no agent_id metadata",
	}
	route, _, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID != "ray" {
		t.Fatalf("handoff override should still route to ray when no explicit agent_id, got %q", route.AgentID)
	}
	wantSK := "agent:ray:session:" + sessionID
	if route.SessionKey != wantSK {
		t.Errorf("session key = %q, want %q", route.SessionKey, wantSK)
	}
}

// TestResolveMessageRoute_ChannelHandoffOverride_RoutesByChatScope pins the
// channel-handoff fix (P1). A channel (whatsapp/telegram/…) inbound message
// carries NO SessionID, so sessionScopeKey resolves its handoff override under
// the chat scope ("chat:<channel>:<chatID>"), NOT "session:<id>". The handoff
// now stores the override under that same chat-scope key, so after Mia hands off
// to Ray in a WhatsApp chat, the next WhatsApp message routes to Ray. Regression
// for the bug where the override was stored ONLY under "session:<id>" — which
// channel messages never look up — so routing fell back to ResolveRoute and the
// original agent "stayed".
func TestResolveMessageRoute_ChannelHandoffOverride_RoutesByChatScope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	for _, id := range []string{"mia", "ray"} {
		ag := NewAgentInstance(&config.AgentConfig{ID: id, Name: id},
			&cfg.Agents.Defaults, cfg, &mockProvider{})
		ag.Home = filepath.Join(home, "agents", id)
		ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(id, id)
		al.registry.mu.Lock()
		al.registry.agents[id] = ag
		al.registry.mu.Unlock()
	}

	const chatID = "15555550100@s.whatsapp.net"
	// State left by a Mia → Ray handoff on a WhatsApp chat: onHandoffFrontend
	// writes the override under the chat-scope key for non-webchat channels.
	al.sessionActiveAgent.Store("chat:whatsapp:"+chatID, "ray")

	// Next inbound WhatsApp message from the same chat — NO SessionID (channels
	// don't carry one), no explicit agent_id.
	msg := bus.InboundMessage{
		Channel: "whatsapp",
		ChatID:  chatID,
		Content: "are you still there?",
	}
	route, agent, err := al.resolveMessageRoute(msg)
	if err != nil {
		t.Fatalf("resolveMessageRoute: %v", err)
	}
	if route.AgentID != "ray" {
		t.Fatalf(
			"channel handoff override should route the whatsapp message to ray, got %q ('agent stays' regression)",
			route.AgentID,
		)
	}
	if agent == nil || agent.ID != "ray" {
		t.Fatalf("returned agent should be ray, got %+v", agent)
	}
	wantSK := "agent:ray:chat:whatsapp:" + chatID
	if route.SessionKey != wantSK {
		t.Errorf("session key = %q, want %q", route.SessionKey, wantSK)
	}
}
