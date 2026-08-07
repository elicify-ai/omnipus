package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// --- test doubles ---

type stubRegistry struct {
	agents  map[string]string // id → name
	workers map[string]bool   // id → isWorker
}

func (r *stubRegistry) GetAgentName(id string) (string, bool) {
	name, ok := r.agents[id]
	return name, ok
}

func (r *stubRegistry) IsWorker(id string) bool {
	return r.workers[id]
}

type stubSessionStore struct {
	switchErr      error
	appendedEvents []session.TranscriptEntry
	transcript     []session.TranscriptEntry
}

func (s *stubSessionStore) SwitchAgent(sessionID, newAgentID string) error {
	return s.switchErr
}

func (s *stubSessionStore) ReadTranscript(sessionID string) ([]session.TranscriptEntry, error) {
	return s.transcript, nil
}

// AppendTranscriptStrict implements HandoffSessionStore's strict entry point
// (ADR-057 U22, W3c: HandoffSessionStore.AppendTranscript was renamed to
// AppendTranscriptStrict in handoff.go; this is the same test double,
// mechanically renamed to keep satisfying the interface — no assertions in
// this file depend on the old lenient name).
func (s *stubSessionStore) AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error {
	s.appendedEvents = append(s.appendedEvents, entry)
	return nil
}

// --- helpers ---

func makeCtx(sessionKey, chatID, agentID string) context.Context {
	ctx := context.Background()
	ctx = WithSessionKey(ctx, sessionKey)
	ctx = WithToolContext(ctx, "webchat", chatID)
	ctx = WithAgentID(ctx, agentID)
	return ctx
}

// --- HandoffTool tests ---

func TestHandoffTool_BlocksSystemAgent(t *testing.T) {
	store := &stubSessionStore{}
	tool := NewHandoffTool(
		func() AgentRegistryReader { return &stubRegistry{} },
		store,
		func(string) int { return 8192 },
		nil,
	)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"agent_id": "omnipus-system",
		"context":  "test",
	})
	if !result.IsError {
		t.Fatal("expected error for system agent, got success")
	}
}

func TestHandoffTool_RejectsUnknownAgent(t *testing.T) {
	store := &stubSessionStore{}
	tool := NewHandoffTool(
		func() AgentRegistryReader { return &stubRegistry{agents: map[string]string{}} },
		store,
		func(string) int { return 8192 },
		nil,
	)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"agent_id": "ray",
		"context":  "test",
	})
	if !result.IsError {
		t.Fatal("expected error for unknown agent")
	}
}

func TestHandoffTool_RejectsWorkerTarget(t *testing.T) {
	store := &stubSessionStore{}
	reg := &stubRegistry{
		agents:  map[string]string{"hans": "Hans"},
		workers: map[string]bool{"hans": true},
	}
	tool := NewHandoffTool(
		func() AgentRegistryReader { return reg },
		store,
		func(string) int { return 8192 },
		nil,
	)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"agent_id": "hans",
		"context":  "do the work",
	})
	if !result.IsError {
		t.Fatal("expected error when handing off to a worker")
	}
	if !strings.Contains(result.ForLLM, "worker") {
		t.Errorf("expected worker-rejection message, got %q", result.ForLLM)
	}
	// The session must NOT have been switched — no transcript entry appended.
	if len(store.appendedEvents) != 0 {
		t.Errorf(
			"expected no session switch on rejected worker handoff, got %d appended events",
			len(store.appendedEvents),
		)
	}
}

func TestHandoffTool_RejectsEmptySessionKey(t *testing.T) {
	store := &stubSessionStore{}
	reg := &stubRegistry{agents: map[string]string{"ray": "Ray"}}
	tool := NewHandoffTool(
		func() AgentRegistryReader { return reg },
		store,
		func(string) int { return 8192 },
		nil,
	)
	// No session key in context.
	ctx := WithToolContext(context.Background(), "webchat", "chat_1")
	result := tool.Execute(ctx, map[string]any{
		"agent_id": "ray",
		"context":  "help with billing",
	})
	if !result.IsError {
		t.Fatal("expected error when no session key")
	}
}

func TestHandoffTool_IdempotentAlreadyActive(t *testing.T) {
	store := &stubSessionStore{switchErr: ErrAlreadyActive}
	reg := &stubRegistry{agents: map[string]string{"ray": "Ray"}}
	tool := NewHandoffTool(
		func() AgentRegistryReader { return reg },
		store,
		func(string) int { return 8192 },
		nil,
	)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"agent_id": "ray",
		"context":  "continue",
	})
	if result.IsError {
		t.Fatalf("expected success for ErrAlreadyActive, got error: %s", result.ForLLM)
	}
	if result.ForLLM == "" {
		t.Fatal("expected non-empty ForLLM for already-active handoff")
	}
}

func TestHandoffTool_SuccessPath(t *testing.T) {
	var notifiedAgentID string
	store := &stubSessionStore{
		transcript: []session.TranscriptEntry{
			{Role: "user", Content: "hello", AgentID: "mia", Timestamp: time.Now()},
			{Role: "assistant", Content: "hi there", AgentID: "mia", Timestamp: time.Now()},
		},
	}
	reg := &stubRegistry{agents: map[string]string{"ray": "Ray"}}
	tool := NewHandoffTool(
		func() AgentRegistryReader { return reg },
		store,
		func(string) int { return 8192 },
		func(evt HandoffEvent) { notifiedAgentID = evt.AgentID },
	)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"agent_id": "ray",
		"context":  "user needs billing help",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if result.ForUser == "" {
		t.Error("ForUser should not be empty on success")
	}
	if result.ForLLM == "" {
		t.Error("ForLLM should not be empty on success")
	}
	if notifiedAgentID != "ray" {
		t.Errorf("expected onHandoff called with ray, got %q", notifiedAgentID)
	}
	// Audit trail: one system entry appended.
	if len(store.appendedEvents) != 1 {
		t.Errorf("expected 1 appended system entry, got %d", len(store.appendedEvents))
	}
	if store.appendedEvents[0].Type != session.EntryTypeSystem {
		t.Errorf("expected system entry type, got %q", store.appendedEvents[0].Type)
	}
}

func TestHandoffTool_SwitchAgentError(t *testing.T) {
	store := &stubSessionStore{switchErr: errors.New("disk full")}
	reg := &stubRegistry{agents: map[string]string{"ray": "Ray"}}
	tool := NewHandoffTool(
		func() AgentRegistryReader { return reg },
		store,
		func(string) int { return 8192 },
		nil,
	)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"agent_id": "ray",
		"context":  "test",
	})
	if !result.IsError {
		t.Fatal("expected error when SwitchAgent fails")
	}
}

// --- splitByTokenBudget tests ---

func TestSplitByTokenBudget_Empty(t *testing.T) {
	recent, older := splitByTokenBudget(nil, 1000)
	if len(recent) != 0 || len(older) != 0 {
		t.Errorf("expected empty slices for nil input, got recent=%d older=%d", len(recent), len(older))
	}
}

func TestSplitByTokenBudget_AllFit(t *testing.T) {
	entries := []session.TranscriptEntry{
		{Content: "hello"}, // ~3 tokens
		{Content: "world"}, // ~3 tokens
	}
	recent, older := splitByTokenBudget(entries, 1000)
	if len(recent) != 2 {
		t.Errorf("expected all 2 in recent, got %d", len(recent))
	}
	if len(older) != 0 {
		t.Errorf("expected 0 in older, got %d", len(older))
	}
}

func TestSplitByTokenBudget_OverBudget(t *testing.T) {
	// Each entry has content "x" * 200 = ~101 tokens each.
	makeEntry := func(size int) session.TranscriptEntry {
		return session.TranscriptEntry{
			Content: string(make([]byte, size)),
		}
	}
	entries := []session.TranscriptEntry{
		makeEntry(200), // ~101 tokens
		makeEntry(200), // ~101 tokens
		makeEntry(200), // ~101 tokens
	}
	// Budget of 150 tokens — only the last entry (the most recent) should fit.
	recent, older := splitByTokenBudget(entries, 150)
	if len(recent) != 1 {
		t.Errorf("expected 1 in recent (last entry only), got %d", len(recent))
	}
	if len(older) != 2 {
		t.Errorf("expected 2 in older, got %d", len(older))
	}
}

func TestSplitByTokenBudget_UsesStoredTokens(t *testing.T) {
	// If entry.Tokens is set, that value is used directly.
	entries := []session.TranscriptEntry{
		{Content: "short", Tokens: 500}, // stored: 500 tokens
		{Content: "also short", Tokens: 50},
	}
	// Budget 100 — only the last entry fits (50 tokens).
	recent, older := splitByTokenBudget(entries, 100)
	if len(recent) != 1 {
		t.Errorf("expected 1 in recent, got %d", len(recent))
	}
	if recent[0].Tokens != 50 {
		t.Errorf("expected recent entry to have 50 tokens, got %d", recent[0].Tokens)
	}
	if len(older) != 1 {
		t.Errorf("expected 1 in older, got %d", len(older))
	}
}

// --- ReturnToDefaultTool tests ---

func TestReturnToDefaultTool_NoSessionKey(t *testing.T) {
	store := &stubSessionStore{}
	tool := NewReturnToDefaultTool(store, func() string { return "mia" }, nil)
	ctx := context.Background()
	result := tool.Execute(ctx, map[string]any{})
	if !result.IsError {
		t.Fatal("expected error when no session key")
	}
}

func TestReturnToDefaultTool_NoDefaultAgent(t *testing.T) {
	store := &stubSessionStore{}
	tool := NewReturnToDefaultTool(store, func() string { return "" }, nil)
	ctx := makeCtx("session_abc", "chat_1", "ray")
	result := tool.Execute(ctx, map[string]any{})
	if !result.IsError {
		t.Fatal("expected error when no default agent configured")
	}
}

func TestReturnToDefaultTool_Success(t *testing.T) {
	var notifiedAgentID string
	store := &stubSessionStore{}
	tool := NewReturnToDefaultTool(
		store,
		func() string { return "mia" },
		func(evt HandoffEvent) { notifiedAgentID = evt.AgentID },
	)
	ctx := makeCtx("session_abc", "chat_1", "ray")
	result := tool.Execute(ctx, map[string]any{
		"summary": "resolved the billing question",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if notifiedAgentID != "mia" {
		t.Errorf("expected notification for mia, got %q", notifiedAgentID)
	}
	if len(store.appendedEvents) != 1 {
		t.Errorf("expected 1 audit entry, got %d", len(store.appendedEvents))
	}
}

func TestReturnToDefaultTool_AlreadyActive(t *testing.T) {
	store := &stubSessionStore{switchErr: ErrAlreadyActive}
	tool := NewReturnToDefaultTool(store, func() string { return "mia" }, nil)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{})
	if result.IsError {
		t.Fatalf("expected success when already on default, got error: %s", result.ForLLM)
	}
}
