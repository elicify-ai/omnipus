// idle_timeout_seam_test.go — deterministic coverage for the idle-timeout →
// CloseSession pipeline using the fireIdleTimeout test seam added in loop.go.
//
// Without this seam the only way to reach CloseSession("idle") is to wait the
// full idle period (≥1 minute, controlled by GetIdleTimeoutMinutes). These
// tests exercise the same code path in <100 ms.

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

// TestIdleTimeout_FireSeam_TriggersCloseSession verifies that fireIdleTimeout
// calls CloseSession("idle") which, when AutoRecapEnabled is true, enqueues the
// recap goroutine and ultimately produces LAST_SESSION.md + a retro file.
//
// This closes the 0%-behavioral idle-fire gap: previously there was no sub-
// minute test exercising the timer→close path.
func TestIdleTimeout_FireSeam_TriggersCloseSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	// LightModel must be in the recap allow-list (claude-haiku-3 is permitted).
	cfg.Agents.Defaults.Routing = &config.RoutingConfig{LightModel: "claude-haiku-3"}

	// Scripted provider returns valid JSON so runRecap takes the happy path.
	script := &scriptedProvider{
		responseBody: `{"recap":"idle session ended","went_well":["idle"],"needs_improvement":[],"worth_remembering":[]}`,
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, script)
	t.Cleanup(func() { al.Close() })

	// Wire a real shared session store.
	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	// Register a minimal agent.
	const agentID = "idle-agent"
	agentCfg := &config.AgentConfig{ID: agentID, Name: "Idle"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, script)
	if ag == nil {
		t.Fatal("NewAgentInstance returned nil")
	}
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, "Idle")
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	// Create a session and append a transcript entry so runRecap has something
	// to summarize and can resolve the agent.
	meta, err := store.NewSession(session.SessionTypeChat, "web", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sessionID := meta.ID
	if err := store.AppendTranscript(sessionID, session.TranscriptEntry{
		Role:      "user",
		Content:   "hello from idle test",
		Timestamp: time.Now().UTC(),
		AgentID:   agentID,
	}); err != nil {
		t.Fatalf("AppendTranscript: %v", err)
	}

	// Arm the idle ticker (as runTurn would) then immediately fire it via the
	// test seam — no real wait, deterministic, same code path as the timer.
	al.resetIdleTicker(sessionID)
	al.fireIdleTimeout(sessionID)

	// After firing, the idle ticker entry must be gone (cancelIdleTicker ran).
	if _, ok := al.idleTickers.Load(sessionID); ok {
		t.Error("idle ticker must be removed after fireIdleTimeout")
	}

	// After firing, the session must be claimed by CloseSession.
	if _, ok := al.claimedCloseSessions.Load(sessionID); !ok {
		t.Error("claimedCloseSessions must contain sessionID after fireIdleTimeout")
	}

	// Wait for the recap goroutine to produce LAST_SESSION.md (up to 5 s;
	// mirrors the polling pattern in session_end_behavioral_test.go).
	lastSessionPath := filepath.Join(ag.Home, ".omnipus", "last-session.md")
	deadline := time.Now().Add(5 * time.Second)
	var lastSessionBytes []byte
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(lastSessionPath)
		if readErr == nil {
			lastSessionBytes = data
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastSessionBytes == nil {
		t.Fatalf("LAST_SESSION.md not produced within 5s after fireIdleTimeout; Chat calls=%d", script.callCount)
	}
	if !strings.Contains(string(lastSessionBytes), "idle session ended") {
		t.Errorf("LAST_SESSION.md missing recap text; got:\n%s", lastSessionBytes)
	}

	// A retro file must also exist with trigger=idle.
	//
	// AppendRetro (memory.go) is NOT atomic from a concurrent reader's point
	// of view: it os.OpenFile(O_CREATE|O_APPEND)s the path, THEN writes the
	// content in a separate syscall. os.ReadDir can observe the just-created
	// (still-empty) file in the sliver between those two steps. The original
	// form of this loop treated "the filename showed up in ReadDir" as proof
	// the write was complete, read it exactly ONCE, and — on catching that
	// sliver — failed permanently on an empty/partial read with no retry.
	// That is a genuine intra-test race, not a load simulation: it is more
	// likely to fire under scheduling contention (which delays the writer
	// between its two syscalls, or delays this loop's iteration to land in
	// that widened window), but it can fire at any load. The fix is to keep
	// polling — re-reading the file — until its CONTENT is actually there,
	// not just its directory entry, using the same 5s budget as before.
	retrosDir := filepath.Join(ag.Home, ".omnipus", "retros")
	var retroBytes []byte
	var sawRetroFile bool
	retroDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(retroDeadline) {
		dateDirs, rerr := os.ReadDir(retrosDir)
		if rerr != nil {
			if !os.IsNotExist(rerr) {
				t.Fatalf("read retros dir (unexpected): %v", rerr)
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}
		var content []byte
		var found bool
		for _, d := range dateDirs {
			if !d.IsDir() {
				continue
			}
			files, _ := os.ReadDir(filepath.Join(retrosDir, d.Name()))
			for _, f := range files {
				if strings.HasSuffix(f.Name(), "_retro.md") {
					found = true
					data, readErr := os.ReadFile(filepath.Join(retrosDir, d.Name(), f.Name()))
					if readErr == nil {
						content = data
					}
				}
			}
		}
		if found {
			sawRetroFile = true
			retroBytes = content
		}
		if found && strings.Contains(string(content), "trigger=idle") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sawRetroFile {
		t.Fatal("no _retro.md produced within 5s after fireIdleTimeout")
	}
	if !strings.Contains(string(retroBytes), "trigger=idle") {
		t.Errorf("retro must record trigger=idle within 5s; last observed content:\n%s", retroBytes)
	}

	// Ensure al.Close() drains recapWG cleanly (no goroutine leak).
	// t.Cleanup above calls al.Close(); if the test reaches here the drain
	// either already completed or will complete within the 30s budget in Close().
}

// TestIdleTimeout_FireSeam_Idempotent verifies that a second fireIdleTimeout
// call for the same session is a no-op (FR-027 idempotency): CloseSession was
// already claimed, so a duplicate call must not enqueue a second recap.
func TestIdleTimeout_FireSeam_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.Routing = &config.RoutingConfig{LightModel: "claude-haiku-3"}

	script := &scriptedProvider{
		responseBody: `{"recap":"ok","went_well":[],"needs_improvement":[],"worth_remembering":[]}`,
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, script)
	t.Cleanup(func() { al.Close() })

	store, _ := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	al.sharedSessionStore = store

	const agentID = "idem-agent"
	agentCfg := &config.AgentConfig{ID: agentID, Name: "Idem"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, script)
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, "Idem")
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sessionID := meta.ID
	_ = store.AppendTranscript(sessionID, session.TranscriptEntry{
		Role: "user", Content: "hi", Timestamp: time.Now().UTC(), AgentID: agentID,
	})

	// Fire twice — second call must be a no-op per FR-027.
	al.fireIdleTimeout(sessionID)
	al.fireIdleTimeout(sessionID)

	// Wait for exactly one Chat call (the first recap).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		script.mu.Lock()
		n := script.callCount
		script.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Give any hypothetical second recap goroutine time to start (it mustn't).
	time.Sleep(100 * time.Millisecond)

	script.mu.Lock()
	calls := script.callCount
	script.mu.Unlock()
	if calls != 1 {
		t.Errorf("Chat called %d times after two fireIdleTimeout calls, want exactly 1 (FR-027 idempotency)", calls)
	}
}
