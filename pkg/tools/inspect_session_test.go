// Omnipus — inspect_session Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// fakeInspectSessionStore is an in-memory InspectSessionStore for tests.
type fakeInspectSessionStore struct {
	metas   map[string]*session.UnifiedMeta
	entries map[string][]session.TranscriptEntry
}

func newFakeInspectSessionStore() *fakeInspectSessionStore {
	return &fakeInspectSessionStore{
		metas:   map[string]*session.UnifiedMeta{},
		entries: map[string][]session.TranscriptEntry{},
	}
}

func (f *fakeInspectSessionStore) GetMeta(sessionID string) (*session.UnifiedMeta, error) {
	m, ok := f.metas[sessionID]
	if !ok {
		return nil, errors.New("session not found")
	}
	return m, nil
}

func (f *fakeInspectSessionStore) ReadTranscript(sessionID string) ([]session.TranscriptEntry, error) {
	return f.entries[sessionID], nil
}

func (f *fakeInspectSessionStore) seed(sessionID, agentID string, entries []session.TranscriptEntry) {
	f.metas[sessionID] = &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: sessionID, AgentID: agentID}}
	f.entries[sessionID] = entries
}

// TestInspectSession_ScopeLock_AllowedSessionPasses proves a session id
// inside the engine-set scope is readable (ADR-052 FR-033, US-13 Acceptance 3).
func TestInspectSession_ScopeLock_AllowedSessionPasses(t *testing.T) {
	t.Parallel()
	store := newFakeInspectSessionStore()
	store.seed("sess-1", "worker-agent", []session.TranscriptEntry{
		{ID: "e1", Role: "assistant", Content: "hello", Timestamp: time.Now()},
	})

	tool := NewInspectSessionTool(store)
	ctx := WithVerifierSessionScope(context.Background(), []string{"sess-1"})

	res := tool.Execute(ctx, map[string]any{"session_id": "sess-1"})
	if res.IsError {
		t.Fatalf("inspect_session on an in-scope session: %s", res.ForLLM)
	}

	var out struct {
		SessionID  string `json:"session_id"`
		AgentID    string `json:"agent_id"`
		EntryCount int    `json:"entry_count"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("parse result %q: %v", res.ForLLM, err)
	}
	if out.SessionID != "sess-1" || out.AgentID != "worker-agent" || out.EntryCount != 1 {
		t.Errorf("unexpected result: %+v", out)
	}
}

// TestInspectSession_ScopeLock_OtherSessionRefused proves a session id
// outside the engine-set scope is refused, even though it exists and is
// readable in the store (ADR-052 FR-033, spec Test 23).
func TestInspectSession_ScopeLock_OtherSessionRefused(t *testing.T) {
	t.Parallel()
	store := newFakeInspectSessionStore()
	store.seed("sess-1", "worker-agent", nil)
	store.seed("sess-2-secret", "other-agent", []session.TranscriptEntry{
		{ID: "e1", Role: "user", Content: "sensitive", Timestamp: time.Now()},
	})

	tool := NewInspectSessionTool(store)
	ctx := WithVerifierSessionScope(context.Background(), []string{"sess-1"})

	res := tool.Execute(ctx, map[string]any{"session_id": "sess-2-secret"})
	if !res.IsError {
		t.Fatal("expected refusal for an out-of-scope session id")
	}
	if !strings.Contains(res.ForLLM, "refused") {
		t.Errorf("unexpected rejection message: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "sensitive") {
		t.Fatal("refused call must not leak any transcript content")
	}
}

// TestInspectSession_NoScopeSet_RefusesEverything proves a turn with NO
// verifier scope set at all (i.e. not a verifier turn) refuses every session
// id — fail closed, never an implicit allow.
func TestInspectSession_NoScopeSet_RefusesEverything(t *testing.T) {
	t.Parallel()
	store := newFakeInspectSessionStore()
	store.seed("sess-1", "worker-agent", []session.TranscriptEntry{
		{ID: "e1", Role: "user", Content: "hi", Timestamp: time.Now()},
	})

	tool := NewInspectSessionTool(store)
	res := tool.Execute(context.Background(), map[string]any{"session_id": "sess-1"})
	if !res.IsError {
		t.Fatal("expected refusal when no verifier scope is set on the turn context")
	}
}

// TestInspectSession_MultiSessionScope_PlanLevel proves a plan-level scope
// (multiple member sessions) permits reading any of them (R3-11).
func TestInspectSession_MultiSessionScope_PlanLevel(t *testing.T) {
	t.Parallel()
	store := newFakeInspectSessionStore()
	store.seed("member-1", "worker-a", []session.TranscriptEntry{{ID: "e1", Timestamp: time.Now()}})
	store.seed("member-2", "worker-b", []session.TranscriptEntry{{ID: "e2", Timestamp: time.Now()}})

	tool := NewInspectSessionTool(store)
	ctx := WithVerifierSessionScope(context.Background(), []string{"member-1", "member-2"})

	for _, id := range []string{"member-1", "member-2"} {
		res := tool.Execute(ctx, map[string]any{"session_id": id})
		if res.IsError {
			t.Fatalf("inspect_session(%s): %s", id, res.ForLLM)
		}
	}
}

// TestInspectSession_ToolCallSummary proves tool calls are summarized with
// name/args/success and that the tool_name filter narrows the output.
func TestInspectSession_ToolCallSummary(t *testing.T) {
	t.Parallel()
	store := newFakeInspectSessionStore()
	store.seed("sess-1", "worker-agent", []session.TranscriptEntry{
		{
			ID: "e1", Role: "assistant", Timestamp: time.Now(),
			ToolCalls: []session.ToolCall{
				{ID: "tc1", Tool: "web_search", Status: "success", Parameters: map[string]any{"query": "foo"}},
				{ID: "tc2", Tool: "bash", Status: "error", Parameters: map[string]any{"cmd": "ls"}},
			},
		},
	})

	tool := NewInspectSessionTool(store)
	ctx := WithVerifierSessionScope(context.Background(), []string{"sess-1"})

	res := tool.Execute(ctx, map[string]any{"session_id": "sess-1"})
	if res.IsError {
		t.Fatalf("inspect_session: %s", res.ForLLM)
	}

	var out struct {
		Entries []struct {
			ToolCalls []struct {
				Name    string `json:"name"`
				Success bool   `json:"success"`
			} `json:"tool_calls"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("parse result %q: %v", res.ForLLM, err)
	}
	if len(out.Entries) != 1 || len(out.Entries[0].ToolCalls) != 2 {
		t.Fatalf("unexpected entries: %+v", out.Entries)
	}
	if out.Entries[0].ToolCalls[0].Name != "web_search" || !out.Entries[0].ToolCalls[0].Success {
		t.Errorf("unexpected first tool call: %+v", out.Entries[0].ToolCalls[0])
	}
	if out.Entries[0].ToolCalls[1].Name != "bash" || out.Entries[0].ToolCalls[1].Success {
		t.Errorf("unexpected second tool call: %+v", out.Entries[0].ToolCalls[1])
	}

	// tool_name filter narrows to just the matching call.
	res2 := tool.Execute(ctx, map[string]any{"session_id": "sess-1", "tool_name": "web_search"})
	if res2.IsError {
		t.Fatalf("inspect_session with tool_name filter: %s", res2.ForLLM)
	}
	var out2 struct {
		Entries []struct {
			ToolCalls []struct {
				Name string `json:"name"`
			} `json:"tool_calls"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(res2.ForLLM), &out2); err != nil {
		t.Fatalf("parse filtered result: %v", err)
	}
	if len(out2.Entries) != 1 || len(out2.Entries[0].ToolCalls) != 1 || out2.Entries[0].ToolCalls[0].Name != "web_search" {
		t.Fatalf("tool_name filter did not narrow correctly: %+v", out2.Entries)
	}
}

// TestInspectSession_FiltersDelegateChildEntries proves a child delegation
// sub-turn's raw entries are never surfaced.
func TestInspectSession_FiltersDelegateChildEntries(t *testing.T) {
	t.Parallel()
	store := newFakeInspectSessionStore()
	store.seed("sess-1", "worker-agent", []session.TranscriptEntry{
		{ID: "top", Role: "assistant", Content: "top-level", Timestamp: time.Now()},
		{ID: "child", Role: "assistant", Content: "child narration", Timestamp: time.Now(), ParentSpawnCallID: "call-1"},
	})

	tool := NewInspectSessionTool(store)
	ctx := WithVerifierSessionScope(context.Background(), []string{"sess-1"})

	res := tool.Execute(ctx, map[string]any{"session_id": "sess-1"})
	if res.IsError {
		t.Fatalf("inspect_session: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "child narration") {
		t.Fatal("delegate child entry content must not be surfaced")
	}
	if !strings.Contains(res.ForLLM, "top-level") {
		t.Fatal("top-level entry content must be surfaced")
	}
}

// TestInspectSession_RequiresSessionID proves session_id is a required arg.
func TestInspectSession_RequiresSessionID(t *testing.T) {
	t.Parallel()
	tool := NewInspectSessionTool(newFakeInspectSessionStore())
	ctx := WithVerifierSessionScope(context.Background(), []string{"sess-1"})
	res := tool.Execute(ctx, map[string]any{})
	if !res.IsError {
		t.Fatal("expected rejection for a missing session_id")
	}
}

// TestInspectSession_NilStore_FailsClosed proves a nil store (metadata-only
// construction) never executes.
func TestInspectSession_NilStore_FailsClosed(t *testing.T) {
	t.Parallel()
	tool := NewInspectSessionTool(nil)
	ctx := WithVerifierSessionScope(context.Background(), []string{"sess-1"})
	res := tool.Execute(ctx, map[string]any{"session_id": "sess-1"})
	if !res.IsError {
		t.Fatal("expected error with nil session store")
	}
}

// TestVerifierSessionScopeAllows_EmptySessionIDsIsNoop proves
// WithVerifierSessionScope(ctx, nil) leaves ctx carrying no scope (unset,
// not an empty-but-present allow-set) — VerifierSessionScopeAllows then
// fail-closed refuses every session id, same as a turn that never called it.
func TestVerifierSessionScopeAllows_EmptySessionIDsIsNoop(t *testing.T) {
	t.Parallel()
	ctx := WithVerifierSessionScope(context.Background(), nil)
	if VerifierSessionScopeAllows(ctx, "anything") {
		t.Fatal("an empty scope must authorize nothing")
	}
	if VerifierSessionScopeAllows(ctx, "") {
		t.Fatal("an empty session id must never be authorized")
	}
}
