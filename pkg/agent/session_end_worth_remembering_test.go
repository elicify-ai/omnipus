// session_end_worth_remembering_test.go — the recap's "worth_remembering" items
// must reach the workspace's carry-forward summary (WU-21 re-aim #1197).
//
// The recap prompt asks the model for {"recap", "went_well", "needs_improvement",
// "worth_remembering"}. A model may state a user-given fact (a codename, a
// number) in worth_remembering and leave it out of the free-text recap; if only
// "recap" is persisted that fact never reaches the next session. Measured
// against the CI recap model (z-ai/glm-5.2): the nonce was missing from "recap"
// in 35 of 180 calls and present in "worth_remembering" in 80 of 80.

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

func TestRunRecap_WorthRemembering_ReachesLastSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = "claude-haiku-3"

	const nonce = "recap-nonce-1791860657706"
	script := &scriptedProvider{
		// The free-text recap omits the fact; only worth_remembering carries it.
		responseBody: `{"recap":"The user gave a launch codename to remember.","went_well":[],"needs_improvement":[],"worth_remembering":["The launch codename is ` + nonce + `"]}`,
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), script)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: "wr-agent", Name: "WR"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, script)
	ag.Home = filepath.Join(home, "agents", "wr-agent")
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo("wr-agent", "WR")
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", "wr-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	wu21ReaimStampWorkspace(t, store, meta.ID) // WU-21 re-aim: carry-forward target.
	if appendErr := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role:      "user",
		Content:   "Remember for later: the launch codename is " + nonce + ".",
		AgentID:   "wr-agent",
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		t.Fatalf("AppendTranscript: %v", appendErr)
	}

	al.CloseSession(meta.ID, "idle")

	path := wu21ReaimPairPath(home, "wr-agent")
	deadline := time.Now().Add(5 * time.Second)
	var content []byte
	for time.Now().Before(deadline) {
		if b, readErr := os.ReadFile(path); readErr == nil && len(b) > 0 {
			content = b
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(content) == 0 {
		t.Fatal("carry-forward summary not produced within 5s")
	}
	got := string(content)
	if !strings.Contains(got, "The user gave a launch codename to remember.") {
		t.Errorf("carry-forward summary lost the recap text; got:\n%s", got)
	}
	if !strings.Contains(got, nonce) {
		t.Errorf("carry-forward summary must carry the worth_remembering item %q; got:\n%s", nonce, got)
	}
	if strings.Contains(got, "Fallback reason:") {
		t.Errorf("this is the success path, not the fallback; got:\n%s", got)
	}
}

func TestFormatLastSession(t *testing.T) {
	tests := []struct {
		name  string
		recap string
		items []string
		want  string
	}{
		{"no items keeps the recap exactly", "we shipped", nil, "we shipped"},
		{"empty and blank items are dropped", "we shipped", []string{"", "  "}, "we shipped"},
		{"items follow the recap as a list", "we shipped", []string{"use atomic writes", " second "},
			"we shipped\n\nWorth remembering:\n- use atomic writes\n- second"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatLastSession(tt.recap, tt.items); got != tt.want {
				t.Errorf("formatLastSession(%q, %q) = %q, want %q", tt.recap, tt.items, got, tt.want)
			}
		})
	}
}
