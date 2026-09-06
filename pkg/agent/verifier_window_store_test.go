// verifier_window_store_test.go — regression for the 2026-09-06 UAT defect
// (judgment-first holdout H-7 class): the FR-032 verifier window feeds read
// ONLY the legacy per-agent session store while live chat/task sessions are
// written to the SHARED store, and ReadTranscript's missing-session shape is
// empty-with-no-error — so the goal Judge was silently starved of the very
// transcript that held the reply, fail-closing every criterion with "no
// evidence" on the first real end-to-end /goal run. The feeds now consult
// the shared store first, keeping the per-agent store as an old-install
// fallback.
package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestGoalSessionWindowText_ReadsSharedStore is the exact live failure: the
// goal session exists ONLY in the shared store (as every live chat session
// does), the goal-bearing agent's legacy per-agent store is empty, and the
// window feed must still surface the transcript.
func TestGoalSessionWindowText_ReadsSharedStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	shared, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = shared

	const agentID = "mia"
	meta, err := shared.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := shared.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role: "assistant", Content: "Hello! The smoke check is done -- sovereign and sound.",
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	got := al.goalSessionWindowText(meta.ID, agentID)
	if got == "" {
		t.Fatal("goalSessionWindowText returned empty for a session that exists in the shared store — " +
			"the Judge would be silently starved of its transcript window (the 2026-09-06 UAT defect)")
	}
	if !strings.Contains(got, "sovereign and sound") {
		t.Fatalf("window text does not contain the session's reply; got: %q", got)
	}
}

// TestGoalSessionWindowText_LegacyPerAgentFallbackStillWorks pins the
// old-install path: a session that lives only in the agent's own legacy
// store must still feed the window after the shared-store-first change.
func TestGoalSessionWindowText_LegacyPerAgentFallbackStillWorks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	// Shared store exists but does NOT hold the session.
	shared, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore(shared): %v", err)
	}
	al.sharedSessionStore = shared

	const agentID = "legacy-agent"
	agentCfg := &config.AgentConfig{ID: agentID, Name: "Legacy"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
	legacy, ok := ag.Sessions.(*session.UnifiedStore)
	if !ok {
		t.Fatalf("agent Sessions is not a UnifiedStore: %T", ag.Sessions)
	}
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	meta, err := legacy.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatalf("NewSession(legacy): %v", err)
	}
	if err := legacy.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role: "assistant", Content: "answer from the legacy store",
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	got := al.goalSessionWindowText(meta.ID, agentID)
	if !strings.Contains(got, "legacy store") {
		t.Fatalf("legacy per-agent fallback broken; got: %q", got)
	}
}
