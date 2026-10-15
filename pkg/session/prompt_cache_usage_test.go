package session

import (
	"fmt"
	"testing"
	"time"
)

// TestUsageReport_HitRatioAndProvider is TDD row 18.
// 800 / (100 + 800 + 100) = 0.8. A zero denominator is null, not 0.
func TestUsageReport_HitRatioAndProvider(t *testing.T) {
	ratio := CacheHitRatio(100, 800, 100)
	if ratio == nil || *ratio != 0.8 {
		t.Fatalf("cache hit ratio = %v, want 0.8", ratioText(ratio))
	}
	if empty := CacheHitRatio(0, 0, 0); empty != nil {
		t.Fatalf("zero-denominator ratio = %v, want null", *empty)
	}

	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	report := AggregateUsage([]*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{
			TokensIn:         100,
			TokensCacheRead:  800,
			TokensCacheWrite: 100,
			TokensTotal:      1000,
			ByProvider: map[string]ModelTokens{
				"anthropic": {In: 50, CacheRead: 400, CacheWrite: 50, Total: 500},
				"openai":    {In: 50, CacheRead: 400, CacheWrite: 60, Total: 510},
			},
		}),
	}, UsageOptions{Period: UsagePeriodAll, Now: ts, Dimension: UsageDimensionAgent})

	if len(report.Buckets) != 1 || report.Buckets[0].CacheHitRatio == nil || *report.Buckets[0].CacheHitRatio != 0.8 {
		var got any
		if len(report.Buckets) == 1 {
			got = ratioText(report.Buckets[0].CacheHitRatio)
		}
		t.Fatalf("agent ratio = %v, want 0.8", got)
	}
	if report.ByProvider == nil {
		t.Fatal("by_provider is nil, want one cell per catalog provider")
	}
	wants := map[string]float64{
		"anthropic": 400.0 / float64(50+400+50),
		"openai":    400.0 / float64(50+400+60),
	}
	for id, want := range wants {
		cell, ok := report.ByProvider[id]
		if !ok || cell.CacheHitRatio == nil || *cell.CacheHitRatio != want {
			t.Errorf("provider %s ratio = %v, want %v", id, ratioText(cell.CacheHitRatio), want)
		}
	}

	zero := AggregateUsage([]*UnifiedMeta{
		buildMeta("s0", "agent-z", ts, SessionStats{}),
	}, UsageOptions{Period: UsagePeriodAll, Now: ts})
	if len(zero.Buckets) == 1 && zero.Buckets[0].CacheHitRatio != nil {
		t.Fatalf("empty agent ratio = %v, want null", *zero.Buckets[0].CacheHitRatio)
	}
}

func ratioText(v *float64) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprint(*v)
}

// TestProviderId_SurvivesReload is TDD row 25. The same model id on two
// providers stays split. Subscription tokens are separate from cost. A mixed
// call keeps its stored unpriced portion after reload.
func TestProviderId_SurvivesReload(t *testing.T) {
	store := newTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	now := time.Now().UTC()
	entries := []TranscriptEntry{
		{
			ID: "priced-openai", Role: "assistant", Model: "shared-model", ProviderID: "openai",
			BillingClass: "priced", PromptTokens: 10, CompletionTokens: 5, Tokens: 15, Cost: 0.01,
			Timestamp: now,
		},
		{
			ID: "priced-anthropic", Role: "assistant", Model: "shared-model", ProviderID: "anthropic",
			BillingClass: "priced", PromptTokens: 20, CompletionTokens: 5, Tokens: 25, Cost: 0.02,
			Timestamp: now,
		},
		{
			ID: "subscription", Role: "assistant", Model: "gpt-4o", ProviderID: "openai",
			BillingClass: "subscription", PromptTokens: 30, CacheReadTokens: 40, CompletionTokens: 10, Tokens: 80,
			Timestamp: now,
		},
		{
			ID: "mixed", Role: "assistant", Model: "shared-model", ProviderID: "anthropic",
			BillingClass: "priced", UnpricedTokens: 7, PromptTokens: 4, CompletionTokens: 1, Tokens: 12, Cost: 0.004,
			Timestamp: now,
		},
	}
	for _, entry := range entries {
		if err := store.AppendTranscript(meta.ID, entry); err != nil {
			t.Fatalf("append %s: %v", entry.ID, err)
		}
	}
	base := store.BaseDir()
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := NewUnifiedStore(base)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	got, err := reopened.GetMeta(meta.ID)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	// FR-002, FR-026/D14 (chief ruling 2026-10-15): subscription calls
	// contribute tokens to their provider, but no dollars. OpenAI's two calls
	// give in 10+30, read 0+40, out 5+10, and authoritative total 15+80.
	openai := got.Stats.ByProvider["openai"]
	if openai.In != 40 || openai.CacheRead != 40 || openai.CacheWrite != 0 || openai.Out != 15 || openai.Total != 95 {
		t.Errorf("openai after reload = in %d out %d read %d write %d total %d, want 40 / 15 / 40 / 0 / 95 (priced + subscription)",
			openai.In, openai.Out, openai.CacheRead, openai.CacheWrite, openai.Total)
	}
	if got.Stats.ByProvider["anthropic"].In != 24 {
		t.Errorf("anthropic input after reload = %d, want 24 (20+4), separate from openai", got.Stats.ByProvider["anthropic"].In)
	}
	if len(got.Stats.ByProvider) != 2 {
		t.Errorf("by_provider after reload = %#v, want exactly openai and anthropic", got.Stats.ByProvider)
	}

	// FR-002: all four calls count, including the mixed call's unpriced portion.
	// In=10+20+30+4, out=5+5+10+1, read=40, write=0, total=15+25+80+12.
	stats := got.Stats
	sessionTotals := ModelTokens{
		In: stats.TokensIn, Out: stats.TokensOut, CacheRead: stats.TokensCacheRead,
		CacheWrite: stats.TokensCacheWrite, Total: stats.TokensTotal,
	}
	wantTotals := ModelTokens{In: 64, Out: 21, CacheRead: 40, CacheWrite: 0, Total: 132}
	if sessionTotals != wantTotals {
		t.Errorf("session tokens after reload = %+v, want %+v (all stored calls)", sessionTotals, wantTotals)
	}
	var providerTotals ModelTokens
	for _, cell := range stats.ByProvider {
		providerTotals.In += cell.In
		providerTotals.Out += cell.Out
		providerTotals.CacheRead += cell.CacheRead
		providerTotals.CacheWrite += cell.CacheWrite
		providerTotals.Total += cell.Total
	}
	if providerTotals != sessionTotals {
		t.Errorf("by_provider token sum after reload = %+v, want session totals %+v", providerTotals, sessionTotals)
	}
	if got.Stats.ByModel["shared-model"].In == 0 {
		t.Fatal("shared model lost its tokens")
	}
	if got.Stats.SubscriptionTokens != 80 {
		t.Fatalf("subscription tokens = %d, want 80, separate from cost", got.Stats.SubscriptionTokens)
	}
	if got.Stats.Cost != 0.034 {
		t.Fatalf("cost = %v, want the priced sum 0.034 and no dollars from the subscription call", got.Stats.Cost)
	}
	if got.Stats.UnpricedTokens != 7 {
		t.Fatalf("unpriced tokens = %d, want the stored mixed-call portion 7 (reload must not reprice)", got.Stats.UnpricedTokens)
	}
}

// TestCacheOnlyZeroOutput_ReconcilesThroughReload is TDD row 31.
func TestCacheOnlyZeroOutput_ReconcilesThroughReload(t *testing.T) {
	store := newTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	now := time.Now().UTC()
	if err := store.AppendTranscript(meta.ID, TranscriptEntry{
		ID: "cache-only", Role: "assistant", Model: "claude", ProviderID: "anthropic",
		PromptTokens: 0, CompletionTokens: 0, CacheReadTokens: 800, CacheWriteTokens: 200, Tokens: 1000,
		Timestamp: now,
	}); err != nil {
		t.Fatalf("append cache-only: %v", err)
	}
	if err := store.AppendTranscript(meta.ID, TranscriptEntry{
		ID: "all-read", Role: "assistant", Model: "claude", ProviderID: "anthropic",
		PromptTokens: 0, CompletionTokens: 0, CacheReadTokens: 1000, CacheWriteTokens: 0, Tokens: 1000,
		Timestamp: now,
	}); err != nil {
		t.Fatalf("append all-read: %v", err)
	}

	base := store.BaseDir()
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := NewUnifiedStore(base)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	got, err := reopened.GetMeta(meta.ID)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	s := got.Stats
	if s.TokensIn != 0 || s.TokensOut != 0 || s.TokensCacheRead != 1800 || s.TokensCacheWrite != 200 || s.TokensTotal != 2000 {
		t.Fatalf("session stats = in %d out %d read %d write %d total %d, want 0 / 0 / 1800 / 200 / 2000",
			s.TokensIn, s.TokensOut, s.TokensCacheRead, s.TokensCacheWrite, s.TokensTotal)
	}
	model := s.ByModel["claude"]
	if model.In != 0 || model.Out != 0 || model.CacheRead != 1800 || model.CacheWrite != 200 || model.Total != 2000 {
		t.Fatalf("model stats = %+v, want 0 / 0 / 1800 / 200 / 2000", model)
	}
	prov := s.ByProvider["anthropic"]
	if prov.In != 0 || prov.Out != 0 || prov.CacheRead != 1800 || prov.CacheWrite != 200 || prov.Total != 2000 {
		t.Fatalf("provider stats = %+v, want the same known split", prov)
	}

	report := AggregateUsage([]*UnifiedMeta{got}, UsageOptions{Period: UsagePeriodAll, Now: now.Add(time.Hour)})
	if report.Total.In != 0 || report.Total.Out != 0 || report.Total.CacheRead != 1800 || report.Total.CacheWrite != 200 || report.Total.Total != 2000 {
		t.Fatalf("report total = %+v, want the known split kept", report.Total)
	}
}

// TestCacheOnly_MissingUsageIsNotTheKnownSplit is the control for row 31.
// A missing usage object is not backfilled into 0 / 0 / 800 / 200.
func TestCacheOnly_MissingUsageIsNotTheKnownSplit(t *testing.T) {
	store := newTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := store.AppendTranscript(meta.ID, TranscriptEntry{
		ID: "no-usage", Role: "assistant", Tokens: 1000, Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := store.GetMeta(meta.ID)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	s := got.Stats
	if s.TokensIn == 0 && s.TokensOut == 0 && s.TokensCacheRead == 800 && s.TokensCacheWrite == 200 {
		t.Fatal("a missing usage object was treated as the known 0/0/800/200 split")
	}
	if s.TokensTotal != 1000 {
		t.Fatalf("total = %d, want the entry total 1000 kept without inventing a cache split", s.TokensTotal)
	}
}
