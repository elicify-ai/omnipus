// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U12 (idle recap triggers), pkg/agent slice.
// Spec: docs/internal/specs/session-core-spec.md FR-036, BDD-11.1/11.4, DEL-08.
// ADR D9 (ADR-20261006-session-core-with-an-agent-address-book.md).
//
// FR-036: "Default-on recap MUST use 30-minute actual inactivity ... Keep
// idle/bootstrap/joined and ordinary helper memory; delete lazy/explicit/
// session_close/ack."
// DEL-08 removes the explicit session-close recap trigger.
//
// Oracle: an "explicit" trigger must NOT produce a recap; an "idle" trigger
// MUST (the control proves the instrument can observe a recap at all). Today
// CloseSession runs the recap for ANY trigger string, so the explicit half
// fails.

package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// u12WireAgent registers one minimal agent with its own home and returns the
// agent home so a test can watch that agent's memory files.
func u12WireAgent(t *testing.T, al *AgentLoop, cfg *config.Config, script *scriptedProvider, agentID string, home string) string {
	t.Helper()
	agentCfg := &config.AgentConfig{ID: agentID, Name: agentID}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, script)
	if ag == nil {
		t.Fatal("NewAgentInstance returned nil")
	}
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, agentID)
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()
	return ag.Home
}

// u12NewSession creates a chat session for agentID with one transcript entry.
func u12NewSession(t *testing.T, store *session.UnifiedStore, agentID string) string {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role:      "user",
		Content:   "hello",
		Timestamp: time.Now().UTC(),
		AgentID:   agentID,
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}
	return meta.ID
}

// u12WaitForLastSession polls for an agent's last-session.md within budget.
func u12WaitForLastSession(agentHome string, budget time.Duration) []byte {
	path := filepath.Join(agentHome, ".omnipus", "last-session.md")
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			return data
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

// FR-036/DEL-08: an explicit session-close must NOT recap; the idle trigger
// (control) MUST — proving the instrument can see a recap at all.
func TestSessionCoreU12_ExplicitSessionCloseDoesNotRecap(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.Routing = &config.RoutingConfig{LightModel: "claude-haiku-3"}

	script := &scriptedProvider{
		responseBody: `{"recap":"session ended","went_well":["x"],"needs_improvement":[],"worth_remembering":[]}`,
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, script)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	explicitHome := u12WireAgent(t, al, cfg, script, "explicit-agent", home)
	idleHome := u12WireAgent(t, al, cfg, script, "idle-agent", home)

	explicitSID := u12NewSession(t, store, "explicit-agent")
	idleSID := u12NewSession(t, store, "idle-agent")

	// Idle control: this MUST recap, else the negative assertion below would be
	// vacuous (the instrument could not observe a recap at all).
	al.resetIdleTicker(idleSID)
	al.fireIdleTimeout(idleSID)
	if u12WaitForLastSession(idleHome, 5*time.Second) == nil {
		t.Fatal("instrument: idle recap produced no last-session.md — the negative assertion below would be vacuous")
	}

	// Explicit: the deleted trigger path. It must produce NO recap.
	al.CloseSession(explicitSID, "explicit")
	if got := u12WaitForLastSession(explicitHome, 3*time.Second); got != nil {
		t.Fatalf("FR-036/DEL-08: an explicit session-close must NOT recap; last-session.md was written:\n%s", got)
	}
}
