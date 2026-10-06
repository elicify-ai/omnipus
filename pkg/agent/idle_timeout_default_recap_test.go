// idle_timeout_default_recap_test.go — the idle close runs the recap on a
// default config (founder decision 2026-10-06: auto recap is ON by default).
// Uses the same fireIdleTimeout seam as idle_timeout_seam_test.go; no new
// production hook.

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestIdleTimeout_DefaultConfig_RunsRecap(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	// Deliberately NOT setting AutoRecapEnabled: the shipped default decides.
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Routing = &config.RoutingConfig{LightModel: "claude-haiku-3"}

	script := &scriptedProvider{
		responseBody: `{"recap":"default recap ran","went_well":["idle"],"needs_improvement":[],"worth_remembering":[]}`,
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), script)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	const agentID = "default-recap-agent"
	ag := NewAgentInstance(&config.AgentConfig{ID: agentID, Name: "DefaultRecap"}, &cfg.Agents.Defaults, cfg, script)
	if ag == nil {
		t.Fatal("NewAgentInstance returned nil")
	}
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, "DefaultRecap")
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role: "user", Content: "hello from default-recap test", Timestamp: time.Now().UTC(), AgentID: agentID,
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	al.resetIdleTicker(meta.ID)
	al.fireIdleTimeout(meta.ID)

	lastSession := filepath.Join(ag.Home, ".omnipus", "last-session.md")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, readErr := os.ReadFile(lastSession); readErr == nil {
			if !strings.Contains(string(data), "default recap ran") {
				t.Fatalf("last-session.md missing recap text; got:\n%s", data)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no recap on idle close with a default config within 5s; Chat calls=%d", script.callCount)
}
