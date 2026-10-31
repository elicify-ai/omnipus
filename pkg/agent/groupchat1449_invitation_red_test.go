// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Invitation input oracles: DS-5, FR-039, FR-040, FR-046, TD-2, TD-9, T-21.
// DS-5 row 9 is deliberately not asserted: "the password is hunter2" in plain
// prose is an accepted gap (D18, #1467). A test that required it to be removed
// would be a false green of a requirement the spec refuses.

func TestGroupChat1449_InvitationInputKeepsTheNewestTail(t *testing.T) {
	// DS-5 rows 1–4. 40 entries are all used. The 41st drops the oldest.
	// 16,384 bytes are used whole. 16,385 bytes are cut at an entry boundary,
	// keeping the newest entry.
	t.Run("40 entries are all used", func(t *testing.T) {
		entries := groupChatTail(40)
		got := invitationInput(entries, 40, 16384)
		if !strings.Contains(got, "E00") || !strings.Contains(got, "E39") {
			t.Fatalf("40-entry tail dropped an entry: %q", got)
		}
	})
	t.Run("41 entries keep the newest 40", func(t *testing.T) {
		entries := groupChatTail(41)
		got := invitationInput(entries, 40, 16384)
		if strings.Contains(got, "E00") || !strings.Contains(got, "E01") || !strings.Contains(got, "E40") {
			t.Fatalf("41-entry tail = %q, want E01 through E40 and not E00", got)
		}
	})
	t.Run("16384 bytes are used whole", func(t *testing.T) {
		body := strings.Repeat("a", 16384)
		got := invitationInput([]session.TranscriptEntry{groupChatUser(body)}, 40, 16384)
		if !strings.Contains(got, body) {
			t.Fatalf("a 16384-byte entry was cut or dropped: length %d", len(got))
		}
	})
	t.Run("16385 bytes cut at an entry boundary", func(t *testing.T) {
		body := strings.Repeat("a", 16384)
		got := invitationInput([]session.TranscriptEntry{groupChatUser(body), groupChatUser("B")}, 40, 16384)
		if strings.Contains(got, body) || !strings.Contains(got, "B") {
			t.Fatalf("16385-byte tail = %q, want the newest entry only, not a split of the older one", got)
		}
		if len(got) > 16384 {
			t.Fatalf("cut input is %d bytes, want at most 16384", len(got))
		}
	})
}

func TestGroupChat1449_InvitationInputDropsSecretsAndScrubsTheRest(t *testing.T) {
	// DS-5 rows 5–8 and FR-040. Row 9 (plain prose "hunter2") is not asserted.
	t.Cleanup(logger.SetSensitiveValueReplacer(strings.NewReplacer("sk-REG-123", "[FILTERED]")))
	const apiKey = "sk-ABCDEFGHIJ1234567890"
	entries := []session.TranscriptEntry{
		groupChatUser("the registered value is sk-REG-123"),
		groupChatUser("the key is " + apiKey),
		{Type: session.EntryTypeMessage, Role: "assistant", Content: "SECRET-CARD", ToolCalls: []session.ToolCall{{ID: "c1", Tool: "secret_request", Status: "success"}}},
		{Type: session.EntryTypeMessage, Role: "assistant", Content: "VAULT-CONTENT", ToolCalls: []session.ToolCall{{ID: "c2", Tool: "vault_read", Status: "success"}}},
		{Type: session.EntryTypeSystem, Role: "system", Content: "SYSTEM-NOTE"},
		groupChatUser("the plan is Friday"),
	}
	got := invitationInput(entries, 40, 16384)
	for _, banned := range []string{"sk-REG-123", apiKey, "SECRET-CARD", "VAULT-CONTENT", "SYSTEM-NOTE"} {
		if strings.Contains(got, banned) {
			t.Errorf("summary input still contains %q", banned)
		}
	}
	if !strings.Contains(got, "the plan is Friday") {
		t.Fatalf("a usable entry was dropped: %q", got)
	}
	stored := scrubInvitation("summary mentions sk-REG-123 and " + apiKey)
	if strings.Contains(stored, "sk-REG-123") || strings.Contains(stored, apiKey) {
		t.Fatalf("stored summary was not scrubbed: %q", stored)
	}
}

func TestGroupChat1449_EmptyOriginChatDoesNotCallTheModel(t *testing.T) {
	// TD-2, DS-5 row 10. No usable entry: no model call and no error.
	al, provider, store, sessionID, cleanup := groupChatInvitationLoop(t)
	defer cleanup()
	if err := store.AppendTranscript(sessionID, session.TranscriptEntry{
		ID: "tool-only", Type: session.EntryTypeMessage, Role: "assistant", Content: "VAULT-CONTENT",
		ToolCalls: []session.ToolCall{{ID: "c1", Tool: "vault_read", Status: "success"}},
	}); err != nil {
		t.Fatal(err)
	}
	before := groupChatChatSnapshot(t, store, sessionID)
	result, err := al.SummarizeRoomInvitation(context.Background(), RoomInvitationRequest{
		WorkspaceID: "sales", OriginSessionID: sessionID, AgentID: testDefaultAgentID, MaxEntries: 40, MaxBytes: 16384,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Called || result.Text != "" || result.CostUSD != 0 {
		t.Fatalf("empty origin = %+v, want no call and no cost", result)
	}
	if provider.calls != 0 {
		t.Fatalf("model calls = %d, want 0", provider.calls)
	}
	after := groupChatChatSnapshot(t, store, sessionID)
	if before != after {
		t.Fatalf("origin chat changed:\n%s\n%s", before, after)
	}
}

func TestGroupChat1449_InvitationCostIsLoggedOnceAndLeavesTheOriginChat(t *testing.T) {
	// T-21, TD-9, FR-046. One call-accounting record for the origin agent.
	// The origin chat's transcript and stored usage stay unchanged.
	al, provider, store, sessionID, cleanup := groupChatInvitationLoop(t)
	defer cleanup()
	if err := store.AppendTranscript(sessionID, groupChatUser("the launch is Friday")); err != nil {
		t.Fatal(err)
	}
	before := groupChatChatSnapshot(t, store, sessionID)
	logPath := t.TempDir() + "/calls.log"
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(logger.DisableFileLogging)
	result, err := al.SummarizeRoomInvitation(context.Background(), RoomInvitationRequest{
		WorkspaceID: "sales", OriginSessionID: sessionID, AgentID: testDefaultAgentID, MaxEntries: 40, MaxBytes: 16384,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Called || provider.calls != 1 {
		t.Fatalf("result %+v calls %d, want one model call", result, provider.calls)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	records := 0
	for _, line := range strings.Split(string(logged), "\n") {
		if strings.Contains(line, "llm_usage") && strings.Contains(line, `agent="`+testDefaultAgentID+`"`) && strings.Contains(line, `session="`+sessionID+`"`) {
			records++
		}
	}
	if records != 1 {
		t.Fatalf("call-accounting records for this chat = %d, want 1; log:\n%s", records, logged)
	}
	after := groupChatChatSnapshot(t, store, sessionID)
	if before != after {
		t.Fatalf("origin chat changed:\n%s\n%s", before, after)
	}
}

func groupChatUser(content string) session.TranscriptEntry {
	return session.TranscriptEntry{Type: session.EntryTypeMessage, Role: "user", Content: content}
}

func groupChatTail(n int) []session.TranscriptEntry {
	out := make([]session.TranscriptEntry, n)
	for i := range out {
		out[i] = groupChatUser(fmt.Sprintf("E%02d", i))
	}
	return out
}

type groupChatProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *groupChatProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return &providers.LLMResponse{
		Content: "Friday launch",
		Usage:   &providers.UsageInfo{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}, nil
}

func (p *groupChatProvider) GetDefaultModel() string { return "test-model" }

func groupChatInvitationLoop(t *testing.T) (*AgentLoop, *groupChatProvider, *session.UnifiedStore, string, func()) {
	t.Helper()
	al, _, _, _, cleanup := newTestAgentLoop(t)
	provider := &groupChatProvider{}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok || inst == nil {
		t.Fatal("test agent is not registered")
	}
	inst.mu.Lock()
	inst.Provider = provider
	inst.Model = "test-model"
	inst.mu.Unlock()
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("session store is nil")
	}
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
	if err != nil {
		t.Fatal(err)
	}
	return al, provider, store, meta.ID, cleanup
}

func groupChatChatSnapshot(t *testing.T, store *session.UnifiedStore, sessionID string) string {
	t.Helper()
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cost=%v in=%d out=%d total=%d messages=%d\n", meta.Stats.Cost, meta.Stats.TokensIn, meta.Stats.TokensOut, meta.Stats.TokensTotal, meta.Stats.MessageCount)
	for _, e := range entries {
		fmt.Fprintf(&b, "%s\t%s\n", e.Role, e.Content)
	}
	return b.String()
}
