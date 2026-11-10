package gateway

// Backend-authored supplemental persistence proof; CHECK must audit this file.
// The real wsStreamer and UnifiedStore run. No gateway provider transport or
// transcript writer is replaced with a mock.

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestBackendStreamer_PersistsPerCallNotTurnTotals(t *testing.T) {
	store, openErr := session.NewUnifiedStore(t.TempDir())
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = store.Close() })
	meta, createErr := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	if createErr != nil {
		t.Fatal(createErr)
	}
	stream := &wsStreamer{agentStore: store, sessionID: meta.ID, agentID: "mia"}
	stream.SetMessageID("visible-final")
	stream.SetTurnID("turn-accounting")
	stream.SetTurnStats(2000, 0.03, time.Second) // UI summary, NOT a third accounting debit.
	stream.SetTurnIOStats(200, 20, 1600, 180)
	calls := []session.TranscriptEntry{
		{ID: "call-1", Role: "assistant", AgentID: "mia", TurnID: "turn-accounting", Timestamp: time.Now().UTC(), ProviderID: "anthropic", Model: "shared", ModelID: "shared", BillingClass: "priced", Tokens: 1000, PromptTokens: 100, CompletionTokens: 10, CacheReadTokens: 800, CacheWriteTokens: 90, CacheWrite5mTokens: 30, CacheWrite1hTokens: 60, Cost: 0.01},
		{ID: "call-2", Role: "assistant", AgentID: "mia", TurnID: "turn-accounting", Timestamp: time.Now().UTC(), ProviderID: "cohere", Model: "shared", ModelID: "shared", BillingClass: "priced", UnpricedTokens: 60, Tokens: 1000, PromptTokens: 100, CompletionTokens: 10, CacheReadTokens: 800, CacheWriteTokens: 90, CacheWrite5mTokens: 30, CacheWrite1hTokens: 60, Cost: 0.02},
	}
	if setter, ok := any(stream).(interface {
		SetCallAccounting(entries []session.TranscriptEntry)
	}); ok {
		setter.SetCallAccounting(calls)
	}
	if finalizeErr := stream.Finalize(t.Context(), "FINAL-NARRATION"); finalizeErr != nil {
		t.Fatal(finalizeErr)
	}
	entries, readErr := store.ReadTranscript(meta.ID)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 2 {
		t.Fatalf("assistant entries=%d, want exactly two completed-call entries", len(entries))
	}
	for i, entry := range entries {
		want := calls[i]
		if entry.ProviderID != want.ProviderID || entry.ModelID != want.ModelID || entry.BillingClass != want.BillingClass || entry.UnpricedTokens != want.UnpricedTokens || entry.CacheWrite5mTokens != 30 || entry.CacheWrite1hTokens != 60 || entry.Tokens != 1000 || entry.Cost != want.Cost {
			t.Errorf("entry %d accounting=%+v, want call %+v", i, entry, want)
		}
	}
	if entries[0].Content != "" || entries[1].Content != "FINAL-NARRATION" || entries[1].ID != "visible-final" {
		t.Errorf("narration/identity changed: %+v", entries)
	}
	stats, statsErr := store.GetMeta(meta.ID)
	if statsErr != nil {
		t.Fatal(statsErr)
	}
	if stats.Stats.TokensTotal != 2000 || stats.Stats.ByProvider["anthropic"].Total != 1000 || stats.Stats.ByProvider["cohere"].Total != 1000 || stats.Stats.UnpricedTokens != 60 {
		t.Errorf("stats=%+v, want each call counted exactly once", stats.Stats)
	}
}
