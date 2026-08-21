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

// TestHydrateAgentHistoryFromTranscript_BlankOwnerFailsLoud is the regression
// guard for the removal of the "main" sentinel's silent fallback. Before the
// sentinel was retired, HydrateAgentHistoryFromTranscript defaulted
// sessionOwner to "main" whenever a transcript entry had a blank AgentID and
// session metadata itself carried no ActiveAgentID/AgentID — so those
// entries hydrated (or silently vanished, since "main" was never really a
// resolvable registry member either) with zero error and zero log signal.
// That was a genuine silent-history-loss path: perAgent[""] would never
// match a real registry.GetAgent lookup, so the affected messages simply
// disappeared from the next turn's context with nothing to indicate why.
//
// The general fallback remains: an entry with no AgentID of its own is
// attributed to the SESSION's owner, which is what lets a session whose
// original agent is gone still hydrate. What was removed is the
// agent-of-last-resort behind it — naming a specific agent when metadata
// resolves nothing would reattribute one agent's history to another.
//
// So when neither the entry nor the session names an owner, the entry is
// simply skipped: not hydrated under a blank owner, and not turned into a
// hard failure of the whole session either. This test pins that the call
// SUCCEEDS and that the unattributable entry reaches no agent.
func TestHydrateAgentHistoryFromTranscript_UnownedEntryIsSkipped(t *testing.T) {
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

	// Deliberately create the session with an EMPTY creatingAgentID, so its
	// meta carries neither ActiveAgentID nor AgentID — session metadata alone
	// cannot resolve an owner. This mirrors a real (if rare) production
	// gap: a session created via a path that never stamped an owning agent.
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if meta.ActiveAgentID != "" || meta.AgentID != "" {
		t.Fatalf("test setup invariant broken: meta must carry no owner, got ActiveAgentID=%q AgentID=%q",
			meta.ActiveAgentID, meta.AgentID)
	}

	// A transcript entry with a blank AgentID — e.g. assistant text written
	// by the wsStreamer fallback before a handoff ever ran.
	now := time.Now().UTC()
	if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role:      "assistant",
		Content:   "orphaned reply with no owning agent",
		AgentID:   "",
		Timestamp: now,
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	if err := al.HydrateAgentHistoryFromTranscript(meta.ID); err != nil {
		t.Fatalf("an unattributable entry must be skipped, not fail the session: %v", err)
	}

	// The entry belonged to nobody, so it must have reached nobody. Crucially
	// it must not have landed under a blank key or a guessed identity: the
	// point of removing the sentinel is that no agent inherits another
	// agent's history by default.
	reg := al.GetRegistry()
	for _, id := range reg.ListAgentIDs() {
		ag, ok := reg.GetAgent(id)
		if !ok || ag == nil || ag.Sessions == nil {
			continue
		}
		key := fmt.Sprintf("agent:%s:session:%s", ag.ID, meta.ID)
		for _, m := range ag.Sessions.GetHistory(key) {
			if strings.Contains(m.Content, "orphaned reply with no owning agent") {
				t.Fatalf("agent %q received an entry that named no owner", ag.ID)
			}
		}
	}
}
