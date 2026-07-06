// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session_end_transient_retry_test.go — unit tests for the transient-stream-reset
// retry logic in runRecap (session_end.go).
//
// Root cause: recap LLM calls against z-ai/glm-5.2 fail with
// "streaming read error: http2: response body closed" — a transient mid-stream
// HTTP/2 drop. Before the fix, runRecap classified this as an unclassified
// "llm_error" and immediately wrote a stub fallback retro with no content.
//
// Fix: runRecap retries each candidate up to maxTransientRetries times on
// transient stream-reset errors (using isTransientStreamError) before falling
// through to the next fallback candidate or the stub. Non-transient errors
// (4xx, auth, context-overflow) are not retried — they fall through immediately.
//
// These tests exercise:
//  1. Happy path after retries: provider fails N times with transient error,
//     then succeeds → recap persists real content, NOT the llm_error stub.
//  2. Stub after exhaustion: provider always fails transiently → stub is written
//     after all retries and fallback candidates are tried.
//  3. Non-transient no-retry: auth error → single attempt, no retry, stub written.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// recapTransientProvider is a test provider that returns transient stream-reset
// errors for the first `failCount` calls, then returns `successBody` on the
// next call. All subsequent calls also return successBody.
type recapTransientProvider struct {
	mu          sync.Mutex
	failCount   int
	callCount   int
	successBody string
	failErr     error
}

func (p *recapTransientProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount++
	if p.callCount <= p.failCount {
		return nil, p.failErr
	}
	return &providers.LLMResponse{Content: p.successBody}, nil
}

func (p *recapTransientProvider) GetDefaultModel() string { return "recap-test-model" }

// buildRecapTestLoop builds an AgentLoop + session store + agent instance
// for recap integration tests. It returns the loop, the session ID, and the
// agent instance. The session is pre-populated with one user transcript entry.
func buildRecapTestLoop(
	t *testing.T,
	provider providers.LLMProvider,
) (al *AgentLoop, sessionID string, ag *AgentInstance) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = "recap-test-model"

	msgBus := bus.NewMessageBus()
	al = mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: "retry-test-agent", Name: "RetryTest"}
	defaults := &cfg.Agents.Defaults
	ag = NewAgentInstance(agentCfg, defaults, cfg, provider)
	if ag == nil {
		t.Fatal("NewAgentInstance returned nil")
	}
	ag.Workspace = filepath.Join(home, "agents", "retry-test-agent")
	ag.ContextBuilder = NewContextBuilder(ag.Workspace).WithAgentInfo("retry-test-agent", "RetryTest")
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", "retry-test-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	sessionID = meta.ID
	if appendErr := store.AppendTranscript(sessionID, session.TranscriptEntry{
		Role:      "user",
		Content:   "transient retry test conversation",
		Timestamp: time.Now().UTC(),
		AgentID:   "retry-test-agent",
	}); appendErr != nil {
		t.Fatalf("AppendTranscript: %v", appendErr)
	}

	return al, sessionID, ag
}

// pollLastSession polls for last-session.md up to 5 seconds, returning its
// content or failing the test if the deadline is exceeded.
func pollLastSession(t *testing.T, ag *AgentInstance, wantAbsent bool) []byte {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	lastSessionPath := filepath.Join(ag.Workspace, ".omnipus", "last-session.md")
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(lastSessionPath)
		if err == nil {
			if wantAbsent {
				// Caller wanted to confirm the file doesn't contain real content —
				// return whatever is there so the caller can assert.
				return data
			}
			return data
		}
		time.Sleep(20 * time.Millisecond)
	}
	if wantAbsent {
		return nil // file never appeared, which is also a valid outcome for wantAbsent
	}
	t.Fatalf("last-session.md not produced within deadline; agent workspace=%s", ag.Workspace)
	return nil
}

// ---------------------------------------------------------------------------
// Test 1: transient error N times then success → real recap content written
// ---------------------------------------------------------------------------

// TestRunRecap_TransientRetry_SucceedsAfterRetries is the primary regression test.
//
// BDD: Given a provider that returns "http2: response body closed" (transient) for
// the first 2 calls, then returns a valid JSON recap on the 3rd call,
// When runRecap is invoked via CloseSession,
// Then last-session.md contains the summarized content (NOT the llm_error stub),
// And the provider was called exactly 3 times (2 transient failures + 1 success).
func TestRunRecap_TransientRetry_SucceedsAfterRetries(t *testing.T) {
	// The canonical transient error that triggered the bug in e2e.
	transientErr := errors.New("streaming read error: http2: response body closed")
	successBody := `{"recap":"shipped the feature","went_well":["ci green"],"needs_improvement":["docs"],"worth_remembering":["use atomic writes"]}`

	provider := &recapTransientProvider{
		failCount:   2,
		failErr:     transientErr,
		successBody: successBody,
	}

	al, sessionID, ag := buildRecapTestLoop(t, provider)
	al.CloseSession(sessionID, "explicit")

	data := pollLastSession(t, ag, false)
	content := string(data)

	// The real recap must appear, not the stub fallback.
	if strings.Contains(content, "Fallback reason:") {
		t.Errorf(
			"last-session.md contains fallback stub — transient retry must have produced real recap; content:\n%s",
			content,
		)
	}
	if !strings.Contains(content, "shipped the feature") {
		t.Errorf("last-session.md missing real recap content; content:\n%s", content)
	}

	// Verify the provider was called more than once (retried).
	provider.mu.Lock()
	calls := provider.callCount
	provider.mu.Unlock()
	if calls < 3 {
		t.Errorf("provider.callCount = %d, want >= 3 (2 transient failures + 1 success)", calls)
	}
}

// TestRunRecap_TransientRetry_SucceedsAfterOneRetry exercises the single-retry path.
//
// BDD: Given a provider that fails once transiently then succeeds,
// When runRecap is invoked,
// Then last-session.md contains the real content,
// And the provider was called exactly 2 times.
func TestRunRecap_TransientRetry_SucceedsAfterOneRetry(t *testing.T) {
	transientErr := errors.New("http2: response body closed")
	successBody := `{"recap":"one retry worked","went_well":[],"needs_improvement":[],"worth_remembering":[]}`

	provider := &recapTransientProvider{
		failCount:   1,
		failErr:     transientErr,
		successBody: successBody,
	}

	al, sessionID, ag := buildRecapTestLoop(t, provider)
	al.CloseSession(sessionID, "explicit")

	data := pollLastSession(t, ag, false)
	if strings.Contains(string(data), "Fallback reason:") {
		t.Errorf("single transient retry must succeed; got fallback stub:\n%s", data)
	}
	if !strings.Contains(string(data), "one retry worked") {
		t.Errorf("expected real recap content; got:\n%s", data)
	}

	provider.mu.Lock()
	calls := provider.callCount
	provider.mu.Unlock()
	if calls != 2 {
		t.Errorf("provider.callCount = %d, want 2", calls)
	}
}

// ---------------------------------------------------------------------------
// Test 2: transient errors exhaust all retries → stub written, no panic
// ---------------------------------------------------------------------------

// TestRunRecap_TransientRetry_StubAfterExhaustion verifies graceful degradation.
//
// BDD: Given a provider that ALWAYS returns "http2: response body closed",
// When runRecap is invoked and exhausts all transient retries across all candidates,
// Then last-session.md is written with the stub fallback format
// ("Session ... Fallback reason: llm_error"),
// And the goroutine does not panic or hang.
func TestRunRecap_TransientRetry_StubAfterExhaustion(t *testing.T) {
	// Provider always fails with a transient error.
	alwaysTransient := &recapTransientProvider{
		failCount:   999, // effectively never succeeds
		failErr:     errors.New("streaming read error: http2: response body closed"),
		successBody: "unreachable",
	}

	al, sessionID, ag := buildRecapTestLoop(t, alwaysTransient)
	al.CloseSession(sessionID, "explicit")

	data := pollLastSession(t, ag, true)
	if data == nil {
		// If the file was never written, the recap ran but wrote nothing — that
		// would be a separate bug. The stub MUST be written.
		t.Fatalf("last-session.md was never written after transient exhaustion")
	}
	content := string(data)
	if !strings.Contains(content, "Fallback reason:") {
		t.Errorf("expected stub fallback after exhaustion; content:\n%s", content)
	}
	// Confirm the stub does NOT contain real recap JSON fragments.
	if strings.Contains(content, `"recap"`) {
		t.Errorf("stub must not contain recap JSON content; content:\n%s", content)
	}
}

// ---------------------------------------------------------------------------
// Test 3: non-transient error → single attempt, fallback immediately
// ---------------------------------------------------------------------------

// TestRunRecap_NonTransientError_NoRetry verifies that auth/4xx errors are NOT
// retried by the transient-reset path.
//
// BDD: Given a provider that returns an unauthorized (401) error,
// When runRecap is invoked,
// Then last-session.md contains the stub fallback,
// And the provider is called exactly ONCE per candidate (no retry).
func TestRunRecap_NonTransientError_NoRetry(t *testing.T) {
	// Non-transient: this must not trigger transient retry.
	authErr := &recapTransientProvider{
		failCount:   999,
		failErr:     errors.New("provider returned 401 unauthorized: invalid api key"),
		successBody: "unreachable",
	}

	al, sessionID, ag := buildRecapTestLoop(t, authErr)
	al.CloseSession(sessionID, "explicit")

	data := pollLastSession(t, ag, true)
	if data == nil {
		t.Fatalf("last-session.md was never written after auth error")
	}
	if !strings.Contains(string(data), "Fallback reason:") {
		t.Errorf("expected stub after non-transient auth error; content:\n%s", data)
	}

	// For a single candidate, a non-transient error must only call the provider
	// once (no retry). With N fallback candidates the cap is N×1 calls.
	authErr.mu.Lock()
	calls := authErr.callCount
	authErr.mu.Unlock()
	// The recap loop has 1 primary candidate (recap model) + 0 fallback models
	// in the default config, so exactly 1 call is expected.
	if calls > 1 {
		t.Errorf("non-transient auth error was retried: provider.callCount = %d, want 1", calls)
	}
}

// ---------------------------------------------------------------------------
// Test 4: GOAWAY variant (the second canonical transient trigger)
// ---------------------------------------------------------------------------

// TestRunRecap_GOAWAYTransient_SucceedsAfterRetry verifies the HTTP/2 GOAWAY
// variant (the second canonical transient trigger from the e2e logs).
//
// BDD: Given a provider that returns a GOAWAY error once then succeeds,
// When runRecap is invoked,
// Then last-session.md contains real recap content (not the stub).
func TestRunRecap_GOAWAYTransient_SucceedsAfterRetry(t *testing.T) {
	goawayErr := errors.New(
		`http2: server sent GOAWAY and closed the connection; LastStreamID=5, ErrCode=INTERNAL_ERROR, debug=""`,
	)
	successBody := `{"recap":"goaway retry worked","went_well":[],"needs_improvement":[],"worth_remembering":[]}`

	provider := &recapTransientProvider{
		failCount:   1,
		failErr:     goawayErr,
		successBody: successBody,
	}

	al, sessionID, ag := buildRecapTestLoop(t, provider)
	al.CloseSession(sessionID, "explicit")

	data := pollLastSession(t, ag, false)
	if strings.Contains(string(data), "Fallback reason:") {
		t.Errorf("GOAWAY retry must succeed; got fallback stub:\n%s", data)
	}
	if !strings.Contains(string(data), "goaway retry worked") {
		t.Errorf("expected real recap after GOAWAY retry; got:\n%s", data)
	}
}
