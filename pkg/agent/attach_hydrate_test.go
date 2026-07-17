package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestHydrateAgentHistoryFromTranscript_RestoresPriorTurns verifies the bridge
// between the shared transcript store and the per-agent SessionStore. Without
// this hydration, "open past session" only repopulates the SPA UI; the agent's
// in-memory turn buffer stays empty for the new WS chatID and the next LLM
// turn answers as if the session just started.
func TestHydrateAgentHistoryFromTranscript_RestoresPriorTurns(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	const agentID = "hydrate-agent"
	agentCfg := &config.AgentConfig{ID: agentID, Name: "Hydrate"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
	if ag == nil {
		t.Fatal("NewAgentInstance returned nil")
	}
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, "Hydrate")
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	transcriptID := meta.ID

	now := time.Now().UTC()
	for i, e := range []session.TranscriptEntry{
		{Role: "user", Content: "deploy host?", AgentID: agentID, Timestamp: now},
		{Role: "assistant", Content: "prod-east-1.example.com", AgentID: agentID, Timestamp: now.Add(time.Second)},
		{Role: "user", Content: "and the staging one?", AgentID: agentID, Timestamp: now.Add(2 * time.Second)},
		{Role: "assistant", Content: "stage.example.com", AgentID: agentID, Timestamp: now.Add(3 * time.Second)},
	} {
		if err := store.AppendTranscript(transcriptID, e); err != nil {
			t.Fatalf("AppendTranscript[%d]: %v", i, err)
		}
	}

	// Hydrate using only sessionID — the per-agent key is now "agent:<id>:session:<sessionID>".
	if err := al.HydrateAgentHistoryFromTranscript(transcriptID); err != nil {
		t.Fatalf("HydrateAgentHistoryFromTranscript: %v", err)
	}

	wantKey := fmt.Sprintf("agent:%s:session:%s", agentID, transcriptID)
	got := ag.Sessions.GetHistory(wantKey)
	if len(got) != 4 {
		t.Fatalf("hydrated history len = %d, want 4; messages=%+v", len(got), got)
	}
	if got[0].Role != "user" || got[0].Content != "deploy host?" {
		t.Errorf("got[0] = %+v, want user/deploy host?", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "prod-east-1.example.com" {
		t.Errorf("got[1] = %+v, want assistant/prod-east-1.example.com", got[1])
	}
	if got[2].Role != "user" || got[2].Content != "and the staging one?" {
		t.Errorf("got[2] = %+v, want user/and the staging one?", got[2])
	}
	if got[3].Role != "assistant" || got[3].Content != "stage.example.com" {
		t.Errorf("got[3] = %+v, want assistant/stage.example.com", got[3])
	}
}

// TestHydrateAgentHistoryFromTranscript_HandoffBriefReachesTarget confirms
// that a Handoff system entry written by HandoffTool is surfaced to the
// target agent's reconstructed history, so a freshly-handed-off agent sees
// the original brief on its first turn instead of starting blind.
func TestHydrateAgentHistoryFromTranscript_HandoffBriefReachesTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	for _, id := range []string{"mia", "ray"} {
		ag := NewAgentInstance(&config.AgentConfig{ID: id, Name: id},
			&cfg.Agents.Defaults, cfg, &mockProvider{})
		ag.Home = filepath.Join(home, "agents", id)
		ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(id, id)
		al.registry.mu.Lock()
		al.registry.agents[id] = ag
		al.registry.mu.Unlock()
	}

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	now := time.Now().UTC()
	for i, e := range []session.TranscriptEntry{
		{Role: "user", Content: "research agentic ai", AgentID: "mia", Timestamp: now},
		{Role: "assistant", Content: "Connecting you with Ray...", AgentID: "mia", Timestamp: now.Add(time.Second)},
		// Handoff system entry, written by HandoffTool with AgentID = target.
		{
			Type:      session.EntryTypeSystem,
			Role:      "system",
			AgentID:   "ray",
			Content:   "Handoff: mia → Ray. Context: comprehensive overview of agentic AI",
			Timestamp: now.Add(2 * time.Second),
		},
	} {
		if err := store.AppendTranscript(meta.ID, e); err != nil {
			t.Fatalf("AppendTranscript[%d]: %v", i, err)
		}
	}

	if err := al.HydrateAgentHistoryFromTranscript(meta.ID); err != nil {
		t.Fatalf("HydrateAgentHistoryFromTranscript: %v", err)
	}

	rayKey := fmt.Sprintf("agent:ray:session:%s", meta.ID)
	ray, ok := al.GetRegistry().GetAgent("ray")
	if !ok {
		t.Fatal("ray not in registry after setup")
	}
	rayHistory := ray.Sessions.GetHistory(rayKey)
	if len(rayHistory) == 0 {
		t.Fatal("Ray's hydrated history should contain at least the handoff brief")
	}
	hasHandoff := false
	for _, m := range rayHistory {
		if strings.Contains(m.Content, "comprehensive overview of agentic AI") {
			hasHandoff = true
			break
		}
	}
	if !hasHandoff {
		t.Fatalf("Ray did not receive the handoff brief; messages=%+v", rayHistory)
	}
}

// TestHydrateAgentHistoryFromTranscript_EmptyTranscriptIsNoOp confirms the
// helper is safe to call on a brand-new (empty) session — for example when
// the SPA reconnects on a session that hasn't yet exchanged a turn.
func TestHydrateAgentHistoryFromTranscript_EmptyTranscriptIsNoOp(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if err := al.HydrateAgentHistoryFromTranscript(meta.ID); err != nil {
		t.Fatalf("HydrateAgentHistoryFromTranscript on empty transcript should not error: %v", err)
	}
}
