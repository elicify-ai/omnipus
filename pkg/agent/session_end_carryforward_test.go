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

// TestBuildCarryForward exercises the fallback carry-forward tail builder.
// Boundary conditions: empty, budget-fits, budget-exceeded (keep newest),
// single over-budget turn (tail-truncate), chronological ordering, and the
// load-bearing property — a fact stated in the LAST turn always survives.
func TestBuildCarryForward(t *testing.T) {
	tests := []struct {
		name     string
		turns    []string
		maxRunes int
		want     string
	}{
		{
			name:     "empty input yields empty",
			turns:    nil,
			maxRunes: 1500,
			want:     "",
		},
		{
			name:     "zero budget yields empty",
			turns:    []string{"hello"},
			maxRunes: 0,
			want:     "",
		},
		{
			name:     "negative budget yields empty",
			turns:    []string{"hello"},
			maxRunes: -10,
			want:     "",
		},
		{
			name:     "single turn within budget",
			turns:    []string{"the codename is BLUEHERON"},
			maxRunes: 1500,
			want:     "the codename is BLUEHERON",
		},
		{
			name:     "multiple turns within budget stay chronological",
			turns:    []string{"first", "second", "third"},
			maxRunes: 1500,
			want:     "first\n\nsecond\n\nthird",
		},
		{
			name:     "budget exceeded keeps the newest turns only",
			turns:    []string{"aaaa", "bbbb", "cccc"}, // 4 runes each
			maxRunes: 8,                                // fits only the two newest (bbbb+cccc = 8)
			want:     "bbbb\n\ncccc",
		},
		{
			name:     "blank turns are skipped",
			turns:    []string{"real", "   ", "\n"},
			maxRunes: 1500,
			want:     "real",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildCarryForward(tt.turns, tt.maxRunes)
			if got != tt.want {
				t.Errorf("buildCarryForward(%q, %d) = %q; want %q", tt.turns, tt.maxRunes, got, tt.want)
			}
		})
	}
}

// TestBuildCarryForward_SingleOverBudgetTurn_TailKept verifies that when the
// final turn alone exceeds the whole budget, its END is kept (a nonce stated
// at the very end of a long final message must still land).
func TestBuildCarryForward_SingleOverBudgetTurn_TailKept(t *testing.T) {
	long := strings.Repeat("x", 100) + "NONCE-Z9"
	got := buildCarryForward([]string{long}, 20)
	if !strings.Contains(got, "NONCE-Z9") {
		t.Fatalf("tail of an over-budget final turn must be kept; got %q", got)
	}
	if len([]rune(got)) > 20 {
		t.Fatalf("carry-forward must respect maxRunes=20; got %d runes", len([]rune(got)))
	}
}

// TestBuildCarryForward_NonceInLastTurnAlwaysSurvives is the continuity
// property the recap-continuity e2e relies on: whatever the user stated most
// recently is preserved even when many earlier turns overflow the budget.
func TestBuildCarryForward_NonceInLastTurnAlwaysSurvives(t *testing.T) {
	turns := []string{
		strings.Repeat("earlier chatter ", 200),
		strings.Repeat("more chatter ", 200),
		"Remember for later: the launch codename is BLUEHERON-42.",
	}
	got := buildCarryForward(turns, carryForwardMaxRunes)
	if !strings.Contains(got, "BLUEHERON-42") {
		t.Fatalf("most-recent user fact must survive; carry-forward = %q", got)
	}
}

// TestRunRecap_AllCandidatesFail_CarriesRecentContextForward is the product-level
// regression for the recap-fallback content-loss gap: when every recap candidate
// fails (the degraded-under-load path), last-session.md must still carry the
// user's stated fact forward verbatim, not just a content-free status stub.
func TestRunRecap_AllCandidatesFail_CarriesRecentContextForward(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = "m1"

	prov := &alwaysErrorProvider{}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, prov)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: "cf-agent", Name: "CarryFwd"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, prov)
	ag.Workspace = filepath.Join(home, "agents", "cf-agent")
	ag.ContextBuilder = NewContextBuilder(ag.Workspace).WithAgentInfo("cf-agent", "CarryFwd")
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", "cf-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	const nonce = "the launch codename is BLUEHERON-42"
	if appendErr := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role:      "user",
		Content:   "Remember for later: " + nonce + ".",
		AgentID:   "cf-agent",
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		t.Fatalf("AppendTranscript: %v", appendErr)
	}

	al.CloseSession(meta.ID, "explicit")

	lastSessionPath := filepath.Join(ag.Workspace, ".omnipus", "last-session.md")
	deadline := time.Now().Add(5 * time.Second)
	var content []byte
	for time.Now().Before(deadline) {
		if b, readErr := os.ReadFile(lastSessionPath); readErr == nil && len(b) > 0 {
			content = b
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if len(content) == 0 {
		t.Fatalf("last-session.md not produced within 5s after all-candidates-fail recap")
	}
	if !strings.Contains(string(content), nonce) {
		t.Fatalf("fallback last-session.md must carry the recent user fact forward verbatim.\nwant substring: %q\ngot:\n%s", nonce, content)
	}
	// The status stub must still be present (we append to it, not replace it).
	if !strings.Contains(string(content), "Fallback reason:") {
		t.Errorf("fallback recap should retain the status stub; got:\n%s", content)
	}
}
