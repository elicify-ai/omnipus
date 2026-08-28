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

// newTestSwitchAgentTool builds a SwitchAgentTool with a fixed registry,
// session store, and default-agent resolver. getDefaultAgent defaults to a
// func returning "" (no default configured) when nil, so named-target-only
// tests do not need to supply one.
func newTestSwitchAgentTool(
	reg AgentRegistryReader,
	store HandoffSessionStore,
	getDefaultAgent func() string,
	onHandoff HandoffFunc,
) *SwitchAgentTool {
	if getDefaultAgent == nil {
		getDefaultAgent = func() string { return "" }
	}
	return NewSwitchAgentTool(
		func() AgentRegistryReader { return reg },
		store,
		func(string) int { return 8192 },
		getDefaultAgent,
		onHandoff,
	)
}

// --- SwitchAgentTool: named-target ("hand off") branch ---

func TestSwitchAgentTool_RejectsUnknownAgent(t *testing.T) {
	store := &stubSessionStore{}
	tool := newTestSwitchAgentTool(&stubRegistry{agents: map[string]string{}}, store, nil, nil)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"target": "ray",
		"note":   "test",
	})
	if !result.IsError {
		t.Fatal("expected error for unknown agent")
	}
}

func TestSwitchAgentTool_BlocksUnregisteredSystemAgent(t *testing.T) {
	store := &stubSessionStore{}
	tool := newTestSwitchAgentTool(&stubRegistry{}, store, nil, nil)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"target": "omnipus-system",
		"note":   "test",
	})
	if !result.IsError {
		t.Fatal("expected error for an agent id absent from the registry, got success")
	}
}

func TestSwitchAgentTool_RejectsWorkerTarget(t *testing.T) {
	store := &stubSessionStore{}
	reg := &stubRegistry{
		agents:  map[string]string{"hans": "Hans"},
		workers: map[string]bool{"hans": true},
	}
	tool := newTestSwitchAgentTool(reg, store, nil, nil)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"target": "hans",
		"note":   "do the work",
	})
	if !result.IsError {
		t.Fatal("expected error when switching to a worker")
	}
	if !strings.Contains(result.ForLLM, "worker") {
		t.Errorf("expected worker-rejection message, got %q", result.ForLLM)
	}
	// The session must NOT have been switched — no transcript entry appended.
	if len(store.appendedEvents) != 0 {
		t.Errorf(
			"expected no session switch on rejected worker target, got %d appended events",
			len(store.appendedEvents),
		)
	}
}

func TestSwitchAgentTool_RejectsEmptySessionKey(t *testing.T) {
	store := &stubSessionStore{}
	reg := &stubRegistry{agents: map[string]string{"ray": "Ray"}}
	tool := newTestSwitchAgentTool(reg, store, nil, nil)
	// No session key in context.
	ctx := WithToolContext(context.Background(), "webchat", "chat_1")
	result := tool.Execute(ctx, map[string]any{
		"target": "ray",
		"note":   "help with billing",
	})
	if !result.IsError {
		t.Fatal("expected error when no session key")
	}
}

func TestSwitchAgentTool_IdempotentAlreadyActive(t *testing.T) {
	store := &stubSessionStore{switchErr: ErrAlreadyActive}
	reg := &stubRegistry{agents: map[string]string{"ray": "Ray"}}
	tool := newTestSwitchAgentTool(reg, store, nil, nil)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"target": "ray",
		"note":   "continue",
	})
	if result.IsError {
		t.Fatalf("expected success for ErrAlreadyActive, got error: %s", result.ForLLM)
	}
	if result.ForLLM == "" {
		t.Fatal("expected non-empty ForLLM for already-active switch")
	}
}

func TestSwitchAgentTool_NamedTargetSuccessPath(t *testing.T) {
	var notifiedAgentID string
	store := &stubSessionStore{
		transcript: []session.TranscriptEntry{
			{Role: "user", Content: "hello", AgentID: "mia", Timestamp: time.Now()},
			{Role: "assistant", Content: "hi there", AgentID: "mia", Timestamp: time.Now()},
		},
	}
	reg := &stubRegistry{agents: map[string]string{"ray": "Ray"}}
	tool := newTestSwitchAgentTool(reg, store, nil, func(evt HandoffEvent) { notifiedAgentID = evt.AgentID })
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"target": "ray",
		"note":   "user needs billing help",
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
	// Audit trail: one system entry appended, stamped with the TARGET agent
	// (ADR-071 §5.1.2 — asymmetric by design, so hydration surfaces the
	// brief under the incoming agent's own history).
	if len(store.appendedEvents) != 1 {
		t.Fatalf("expected 1 appended system entry, got %d", len(store.appendedEvents))
	}
	entry := store.appendedEvents[0]
	if entry.Type != session.EntryTypeSystem {
		t.Errorf("expected system entry type, got %q", entry.Type)
	}
	if entry.AgentID != "ray" {
		t.Errorf("expected audit entry AgentID stamped with target agent %q, got %q", "ray", entry.AgentID)
	}
	if !strings.HasPrefix(entry.Content, "Handoff: ") {
		t.Errorf(
			"expected audit content to start with the FROZEN \"Handoff: \" prefix (ADR-071 §5.2.2a, replay.go depends on it), got %q",
			entry.Content,
		)
	}
}

func TestSwitchAgentTool_SwitchAgentError(t *testing.T) {
	store := &stubSessionStore{switchErr: errors.New("disk full")}
	reg := &stubRegistry{agents: map[string]string{"ray": "Ray"}}
	tool := newTestSwitchAgentTool(reg, store, nil, nil)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{
		"target": "ray",
		"note":   "test",
	})
	if !result.IsError {
		t.Fatal("expected error when SwitchAgent fails")
	}
}

func TestSwitchAgentTool_MissingTarget(t *testing.T) {
	store := &stubSessionStore{}
	tool := newTestSwitchAgentTool(&stubRegistry{}, store, nil, nil)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{})
	if !result.IsError {
		t.Fatal("expected error when target is missing")
	}
}

func TestSwitchAgentTool_NoteOmitted_NotAnError(t *testing.T) {
	// ADR-071 §5.1.1: note is declared optional. hand_off's equivalent
	// "context" param was schema-required but never enforced in Execute, so
	// omitting note must not fail the call.
	store := &stubSessionStore{}
	reg := &stubRegistry{agents: map[string]string{"ray": "Ray"}}
	tool := newTestSwitchAgentTool(reg, store, nil, nil)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{"target": "ray"})
	if result.IsError {
		t.Fatalf("expected success with note omitted, got error: %s", result.ForLLM)
	}
}

// --- SwitchAgentTool: target:"default" ("return to default") branch ---

func TestSwitchAgentTool_Default_NoSessionKey(t *testing.T) {
	store := &stubSessionStore{}
	tool := newTestSwitchAgentTool(&stubRegistry{}, store, func() string { return "mia" }, nil)
	ctx := context.Background()
	result := tool.Execute(ctx, map[string]any{"target": "default"})
	if !result.IsError {
		t.Fatal("expected error when no session key")
	}
}

func TestSwitchAgentTool_Default_NoDefaultAgentConfigured(t *testing.T) {
	store := &stubSessionStore{}
	tool := newTestSwitchAgentTool(&stubRegistry{}, store, func() string { return "" }, nil)
	ctx := makeCtx("session_abc", "chat_1", "ray")
	result := tool.Execute(ctx, map[string]any{"target": "default"})
	if !result.IsError {
		t.Fatal("expected error when no default agent configured")
	}
}

func TestSwitchAgentTool_Default_RejectsWorkerDefault(t *testing.T) {
	// ADR-071 §5.1.2: worker rejection is unconditional, including on the
	// default branch — a hand-edited config can point the default-agent
	// singleton at a worker. return_to_default never had this check; this
	// is a deliberate small improvement, not a regression.
	store := &stubSessionStore{}
	reg := &stubRegistry{
		agents:  map[string]string{"worker-1": "Worker"},
		workers: map[string]bool{"worker-1": true},
	}
	tool := newTestSwitchAgentTool(reg, store, func() string { return "worker-1" }, nil)
	ctx := makeCtx("session_abc", "chat_1", "ray")
	result := tool.Execute(ctx, map[string]any{"target": "default"})
	if !result.IsError {
		t.Fatal("expected error when the configured default agent is a worker")
	}
}

func TestSwitchAgentTool_Default_Success(t *testing.T) {
	var notifiedAgentID string
	store := &stubSessionStore{}
	tool := newTestSwitchAgentTool(
		&stubRegistry{},
		store,
		func() string { return "mia" },
		func(evt HandoffEvent) { notifiedAgentID = evt.AgentID },
	)
	ctx := makeCtx("session_abc", "chat_1", "ray")
	result := tool.Execute(ctx, map[string]any{
		"target": "default",
		"note":   "resolved the billing question",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if notifiedAgentID != "mia" {
		t.Errorf("expected notification for mia, got %q", notifiedAgentID)
	}
	if len(store.appendedEvents) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(store.appendedEvents))
	}
	entry := store.appendedEvents[0]
	// ADR-071 §5.1.2: the default branch stamps the CURRENT (outgoing)
	// agent, not the target — a record of what the outgoing agent did.
	if entry.AgentID != "ray" {
		t.Errorf("expected audit entry AgentID stamped with the outgoing agent %q, got %q", "ray", entry.AgentID)
	}
	if strings.HasPrefix(entry.Content, "Handoff: ") {
		t.Errorf("default-return content must NOT share the frozen \"Handoff: \" prefix (ADR-071 §5.2.2a), got %q", entry.Content)
	}
}

func TestSwitchAgentTool_Default_AlreadyActive(t *testing.T) {
	store := &stubSessionStore{switchErr: ErrAlreadyActive}
	tool := newTestSwitchAgentTool(&stubRegistry{}, store, func() string { return "mia" }, nil)
	ctx := makeCtx("session_abc", "chat_1", "mia")
	result := tool.Execute(ctx, map[string]any{"target": "default"})
	if result.IsError {
		t.Fatalf("expected success when already on default, got error: %s", result.ForLLM)
	}
}

func TestSwitchAgentTool_Default_SkipsTokenBudgetTransfer(t *testing.T) {
	// ADR-071 §5.1.2: the token-budget transcript transfer must be skipped
	// entirely for target:"default" — the default agent isn't cold, so no
	// "Recent context:" block should appear in ForLLM even with a populated
	// transcript in the store.
	store := &stubSessionStore{
		transcript: []session.TranscriptEntry{
			{Role: "user", Content: "hello", AgentID: "ray", Timestamp: time.Now()},
		},
	}
	tool := newTestSwitchAgentTool(&stubRegistry{}, store, func() string { return "mia" }, nil)
	ctx := makeCtx("session_abc", "chat_1", "ray")
	result := tool.Execute(ctx, map[string]any{"target": "default"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "Recent context:") {
		t.Errorf("expected no transcript transfer on the default branch, got ForLLM %q", result.ForLLM)
	}
}

// --- SwitchAgentTool: the "default" sentinel always wins (ADR-071 §5.1.3) ---

func TestSwitchAgentTool_DefaultSentinel_WinsOverRealAgentNamedDefault(t *testing.T) {
	// A real agent literally id'd "default" must never shadow the sentinel —
	// target:"default" always resolves via getDefaultAgent, exact-match,
	// case-sensitive, no fallback to a registry lookup.
	var notifiedAgentID string
	store := &stubSessionStore{}
	reg := &stubRegistry{agents: map[string]string{"default": "A Real Agent Named default"}}
	tool := newTestSwitchAgentTool(
		reg,
		store,
		func() string { return "mia" }, // the CONFIGURED default agent, distinct from the id "default"
		func(evt HandoffEvent) { notifiedAgentID = evt.AgentID },
	)
	ctx := makeCtx("session_abc", "chat_1", "ray")
	result := tool.Execute(ctx, map[string]any{"target": "default"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	if notifiedAgentID != "mia" {
		t.Errorf("expected the sentinel to resolve to the configured default agent %q, got %q", "mia", notifiedAgentID)
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
