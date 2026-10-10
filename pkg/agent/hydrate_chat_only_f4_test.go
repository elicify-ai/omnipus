package agent

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// U8 r2 F4 (security): a chat-only entry (view_membership "chat", e.g. the
// connector-reply mirror) is for display only and must never become model
// history during empty-archive reconstruction. "both" entries are the positive
// control.
func TestHydrate_ChatOnlyEntryStaysOutOfTheModelWindow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	al.sharedSessionStore = store

	const agentID = "f4-agent"
	ag := NewAgentInstance(&config.AgentConfig{ID: agentID, Name: "F4"}, &cfg.Agents.Defaults, cfg, &mockProvider{})
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, "F4")
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, e := range []session.TranscriptEntry{
		{Role: "user", Content: "ordinary question", AgentID: agentID, Timestamp: now},
		{Role: "assistant", Content: "CHAT-ONLY mirror text", AgentID: agentID, Timestamp: now.Add(time.Second), ViewMembership: session.ViewMembershipChat},
		{Role: "assistant", Content: "ordinary answer", AgentID: agentID, Timestamp: now.Add(2 * time.Second)},
	} {
		if err := store.AppendTranscript(meta.ID, e); err != nil {
			t.Fatalf("append[%d]: %v", i, err)
		}
	}
	if err := al.HydrateAgentHistoryFromTranscript(meta.ID); err != nil {
		t.Fatal(err)
	}
	got := ag.Sessions.GetHistory(fmt.Sprintf("agent:%s:session:%s", agentID, meta.ID))
	var sawQuestion, sawAnswer bool
	for _, m := range got {
		if m.Content == "CHAT-ONLY mirror text" {
			t.Fatalf("a chat-only entry reached the model window: %+v", got)
		}
		sawQuestion = sawQuestion || m.Content == "ordinary question"
		sawAnswer = sawAnswer || m.Content == "ordinary answer"
	}
	if !sawQuestion || !sawAnswer {
		t.Fatalf("instrument check: ordinary (both) entries must still hydrate, got %+v", got)
	}
}
