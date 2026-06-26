// Omnipus — Session Usage Aggregator Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"testing"
	"time"
)

// buildMeta is a test helper that creates a *UnifiedMeta with the given
// agent ID, UpdatedAt, and SessionStats.
func buildMeta(id, agentID string, updatedAt time.Time, stats SessionStats) *UnifiedMeta {
	return &UnifiedMeta{
		SessionMeta: SessionMeta{
			ID:            id,
			AgentID:       agentID,
			AgentIDs:      []string{agentID},
			ActiveAgentID: agentID,
			UpdatedAt:     updatedAt,
			Stats:         stats,
			Title:         "Session " + id,
		},
		Type: SessionTypeChat,
	}
}

// now is a fixed reference time used throughout the tests.
var now = time.Date(2026, 6, 26, 15, 0, 0, 0, time.UTC) // Thursday, week 26

func TestAggregateUsage_PeriodDay(t *testing.T) {
	today := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)     // same day
	yesterday := time.Date(2026, 6, 25, 23, 0, 0, 0, time.UTC) // previous day

	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", today, SessionStats{TokensIn: 100, TokensOut: 50, TokensTotal: 150}),
		buildMeta("s2", "agent-a", yesterday, SessionStats{TokensIn: 200, TokensOut: 100, TokensTotal: 300}),
	}

	report := AggregateUsage(metas, UsageOptions{Period: UsagePeriodDay, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.In != 100 {
		t.Errorf("day period: want Total.In=100, got %d", report.Total.In)
	}
	if report.Total.Out != 50 {
		t.Errorf("day period: want Total.Out=50, got %d", report.Total.Out)
	}
	if len(report.Buckets) != 1 {
		t.Errorf("day period: want 1 bucket, got %d", len(report.Buckets))
	}
	// Verify period bounds are anchored correctly.
	wantStart := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)
	if !report.PeriodStart.Equal(wantStart) {
		t.Errorf("day period: want PeriodStart=%v, got %v", wantStart, report.PeriodStart)
	}
}

func TestAggregateUsage_PeriodWeek(t *testing.T) {
	// now is Thursday 2026-06-26; ISO week starts Monday 2026-06-22.
	monday := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	friday := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)     // within same week
	lastWeek := time.Date(2026, 6, 21, 23, 59, 59, 0, time.UTC) // Sunday before

	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", monday, SessionStats{TokensIn: 10, TokensOut: 5, TokensTotal: 15}),
		buildMeta("s2", "agent-a", friday, SessionStats{TokensIn: 20, TokensOut: 10, TokensTotal: 30}),
		buildMeta("s3", "agent-a", lastWeek, SessionStats{TokensIn: 999, TokensOut: 999, TokensTotal: 1998}),
	}

	report := AggregateUsage(metas, UsageOptions{Period: UsagePeriodWeek, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.In != 30 {
		t.Errorf("week period: want Total.In=30, got %d", report.Total.In)
	}
	wantStart := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC) // Monday
	if !report.PeriodStart.Equal(wantStart) {
		t.Errorf("week period: want PeriodStart=%v, got %v", wantStart, report.PeriodStart)
	}
	wantEnd := wantStart.Add(7 * 24 * time.Hour)
	if !report.PeriodEnd.Equal(wantEnd) {
		t.Errorf("week period: want PeriodEnd=%v, got %v", wantEnd, report.PeriodEnd)
	}
}

func TestAggregateUsage_PeriodMonth(t *testing.T) {
	inMonth := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	lastMonth := time.Date(2026, 5, 31, 23, 0, 0, 0, time.UTC)

	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", inMonth, SessionStats{TokensIn: 100, TokensOut: 50, TokensTotal: 150}),
		buildMeta("s2", "agent-a", lastMonth, SessionStats{TokensIn: 999, TokensOut: 999, TokensTotal: 1998}),
	}

	report := AggregateUsage(metas, UsageOptions{Period: UsagePeriodMonth, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.In != 100 {
		t.Errorf("month period: want Total.In=100, got %d", report.Total.In)
	}
	wantStart := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if !report.PeriodStart.Equal(wantStart) {
		t.Errorf("month period: want PeriodStart=%v, got %v", wantStart, report.PeriodStart)
	}
}

func TestAggregateUsage_PeriodAll(t *testing.T) {
	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", t1, SessionStats{TokensIn: 50, TokensOut: 25, TokensTotal: 75}),
		buildMeta("s2", "agent-a", t2, SessionStats{TokensIn: 100, TokensOut: 50, TokensTotal: 150}),
	}

	report := AggregateUsage(metas, UsageOptions{Period: UsagePeriodAll, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.In != 150 {
		t.Errorf("all period: want Total.In=150, got %d", report.Total.In)
	}
	if !report.PeriodStart.IsZero() {
		t.Errorf("all period: want zero PeriodStart, got %v", report.PeriodStart)
	}
	if !report.PeriodEnd.Equal(now) {
		t.Errorf("all period: want PeriodEnd=%v, got %v", now, report.PeriodEnd)
	}
}

func TestAggregateUsage_DimensionAgent(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{TokensIn: 100, TokensOut: 50, TokensTotal: 150}),
		buildMeta("s2", "agent-a", ts, SessionStats{TokensIn: 200, TokensOut: 100, TokensTotal: 300}),
		buildMeta("s3", "agent-b", ts, SessionStats{TokensIn: 10, TokensOut: 5, TokensTotal: 15}),
	}

	resolver := func(id string) string {
		if id == "agent-a" {
			return "Agent A"
		}
		return "Agent B"
	}

	report := AggregateUsage(metas, UsageOptions{
		Period:       UsagePeriodMonth,
		Now:          now,
		Dimension:    UsageDimensionAgent,
		NameResolver: resolver,
	})

	if len(report.Buckets) != 2 {
		t.Fatalf("agent dimension: want 2 buckets, got %d", len(report.Buckets))
	}
	// First bucket should be agent-a (highest total).
	if report.Buckets[0].Key != "agent-a" {
		t.Errorf("agent dimension: want first bucket key=agent-a, got %s", report.Buckets[0].Key)
	}
	if report.Buckets[0].In != 300 {
		t.Errorf("agent dimension: want agent-a In=300, got %d", report.Buckets[0].In)
	}
	if report.Buckets[0].Label != "Agent A" {
		t.Errorf("agent dimension: want label=Agent A, got %s", report.Buckets[0].Label)
	}
}

func TestAggregateUsage_DimensionModel(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{
			TokensIn:    100,
			TokensOut:   50,
			TokensTotal: 150,
			ByModel: map[string]ModelTokens{
				"claude-sonnet-4-6": {In: 80, Out: 40, CacheRead: 10, Total: 130},
				"gpt-4":             {In: 20, Out: 10, Total: 30},
			},
		}),
		buildMeta("s2", "agent-a", ts, SessionStats{
			TokensIn:    50,
			TokensOut:   25,
			TokensTotal: 75,
			ByModel: map[string]ModelTokens{
				"claude-sonnet-4-6": {In: 50, Out: 25, CacheRead: 5, Total: 80},
			},
		}),
	}

	report := AggregateUsage(metas, UsageOptions{
		Period:    UsagePeriodMonth,
		Now:       now,
		Dimension: UsageDimensionModel,
	})

	if len(report.Buckets) != 2 {
		t.Fatalf("model dimension: want 2 buckets, got %d", len(report.Buckets))
	}
	// claude-sonnet-4-6 should be first (highest total).
	found := false
	for _, b := range report.Buckets {
		if b.Key == "claude-sonnet-4-6" {
			found = true
			if b.In != 130 {
				t.Errorf("model dimension: claude-sonnet-4-6 In want 130, got %d", b.In)
			}
			if b.CacheRead != 15 {
				t.Errorf("model dimension: claude-sonnet-4-6 CacheRead want 15, got %d", b.CacheRead)
			}
		}
	}
	if !found {
		t.Errorf("model dimension: claude-sonnet-4-6 bucket not found")
	}
}

func TestAggregateUsage_DimensionSession(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("sess-1", "agent-a", ts, SessionStats{TokensIn: 100, TokensOut: 50, TokensTotal: 150}),
		buildMeta("sess-2", "agent-a", ts, SessionStats{TokensIn: 200, TokensOut: 100, TokensTotal: 300}),
	}
	// Override title for one session.
	metas[0].Title = "My chat session"

	report := AggregateUsage(metas, UsageOptions{
		Period:    UsagePeriodMonth,
		Now:       now,
		Dimension: UsageDimensionSession,
	})

	if len(report.Buckets) != 2 {
		t.Fatalf("session dimension: want 2 buckets, got %d", len(report.Buckets))
	}
	found := false
	for _, b := range report.Buckets {
		if b.Key == "sess-1" {
			found = true
			if b.Label != "My chat session" {
				t.Errorf("session dimension: want label=My chat session, got %s", b.Label)
			}
		}
	}
	if !found {
		t.Error("session dimension: sess-1 bucket not found")
	}
}

func TestAggregateUsage_CacheSplit(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{
			TokensIn:         100,
			TokensOut:        50,
			TokensCacheRead:  20,
			TokensCacheWrite: 10,
			TokensTotal:      180,
		}),
	}

	report := AggregateUsage(metas, UsageOptions{Period: UsagePeriodMonth, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.CacheRead != 20 {
		t.Errorf("cache split: want CacheRead=20, got %d", report.Total.CacheRead)
	}
	if report.Total.CacheWrite != 10 {
		t.Errorf("cache split: want CacheWrite=10, got %d", report.Total.CacheWrite)
	}
	if len(report.Buckets) != 1 {
		t.Fatalf("cache split: want 1 bucket, got %d", len(report.Buckets))
	}
	if report.Buckets[0].CacheRead != 20 {
		t.Errorf("cache split bucket: want CacheRead=20, got %d", report.Buckets[0].CacheRead)
	}
}

func TestAggregateUsage_ExcludeSubagent3p(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-native", ts, SessionStats{TokensIn: 100, TokensOut: 50, TokensTotal: 150}),
		buildMeta("s2", "agent-external", ts, SessionStats{TokensIn: 999, TokensOut: 999, TokensTotal: 1998}),
	}

	exclude := func(id string) bool {
		return id == "agent-external"
	}

	report := AggregateUsage(metas, UsageOptions{
		Period:    UsagePeriodMonth,
		Now:       now,
		Dimension: UsageDimensionAgent,
		Exclude:   exclude,
	})

	if report.Total.In != 100 {
		t.Errorf("exclude: want Total.In=100, got %d (external agent not excluded)", report.Total.In)
	}
	if len(report.Buckets) != 1 {
		t.Errorf("exclude: want 1 bucket, got %d", len(report.Buckets))
	}
	if report.Buckets[0].Key == "agent-external" {
		t.Error("exclude: external agent must not appear in buckets")
	}
}

func TestAggregateUsage_AgentIDFilter(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{TokensIn: 100, TokensOut: 50, TokensTotal: 150}),
		buildMeta("s2", "agent-b", ts, SessionStats{TokensIn: 200, TokensOut: 100, TokensTotal: 300}),
	}

	report := AggregateUsage(metas, UsageOptions{
		Period:    UsagePeriodMonth,
		Now:       now,
		Dimension: UsageDimensionAgent,
		AgentID:   "agent-a",
	})

	if report.Total.In != 100 {
		t.Errorf("agent filter: want Total.In=100, got %d", report.Total.In)
	}
	if len(report.Buckets) != 1 {
		t.Errorf("agent filter: want 1 bucket, got %d", len(report.Buckets))
	}
}

func TestAggregateUsage_SessionIDFilter(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("sess-target", "agent-a", ts, SessionStats{TokensIn: 50, TokensOut: 25, TokensTotal: 75}),
		buildMeta("sess-other", "agent-a", ts, SessionStats{TokensIn: 999, TokensOut: 999, TokensTotal: 1998}),
	}

	report := AggregateUsage(metas, UsageOptions{
		Period:    UsagePeriodMonth,
		Now:       now,
		Dimension: UsageDimensionSession,
		SessionID: "sess-target",
	})

	if report.Total.In != 50 {
		t.Errorf("session filter: want Total.In=50, got %d", report.Total.In)
	}
	if len(report.Buckets) != 1 {
		t.Errorf("session filter: want 1 bucket, got %d", len(report.Buckets))
	}
}

func TestAggregateUsage_TotalReconciliation(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{
			TokensIn: 100, TokensOut: 50, TokensCacheRead: 20, TokensCacheWrite: 10, TokensTotal: 180,
		}),
		buildMeta("s2", "agent-b", ts, SessionStats{
			TokensIn: 200, TokensOut: 100, TokensCacheRead: 5, TokensCacheWrite: 2, TokensTotal: 307,
		}),
	}

	report := AggregateUsage(metas, UsageOptions{Period: UsagePeriodMonth, Now: now, Dimension: UsageDimensionAgent})

	var sumIn, sumOut, sumCR, sumCW int
	for _, b := range report.Buckets {
		sumIn += b.In
		sumOut += b.Out
		sumCR += b.CacheRead
		sumCW += b.CacheWrite
	}

	if sumIn != report.Total.In {
		t.Errorf("reconciliation: bucket sum In=%d != Total.In=%d", sumIn, report.Total.In)
	}
	if sumOut != report.Total.Out {
		t.Errorf("reconciliation: bucket sum Out=%d != Total.Out=%d", sumOut, report.Total.Out)
	}
	if sumCR != report.Total.CacheRead {
		t.Errorf("reconciliation: bucket sum CacheRead=%d != Total.CacheRead=%d", sumCR, report.Total.CacheRead)
	}
	if sumCW != report.Total.CacheWrite {
		t.Errorf("reconciliation: bucket sum CacheWrite=%d != Total.CacheWrite=%d", sumCW, report.Total.CacheWrite)
	}
}

func TestAggregateUsage_EmptyInput(t *testing.T) {
	report := AggregateUsage(nil, UsageOptions{Period: UsagePeriodMonth, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.In != 0 || report.Total.Out != 0 || report.Total.Total != 0 {
		t.Errorf("empty input: want zero Total, got %+v", report.Total)
	}
	if len(report.Buckets) != 0 {
		t.Errorf("empty input: want 0 buckets, got %d", len(report.Buckets))
	}
}

func TestAggregateUsage_NameResolverNilSafe(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-x", ts, SessionStats{TokensIn: 100, TokensOut: 50, TokensTotal: 150}),
	}

	// Must not panic when NameResolver is nil.
	report := AggregateUsage(metas, UsageOptions{
		Period:       UsagePeriodMonth,
		Now:          now,
		Dimension:    UsageDimensionAgent,
		NameResolver: nil,
	})

	if len(report.Buckets) != 1 {
		t.Fatalf("nil resolver: want 1 bucket, got %d", len(report.Buckets))
	}
	// Label should fall back to the key.
	if report.Buckets[0].Label != "agent-x" {
		t.Errorf("nil resolver: want label=agent-x (fallback), got %s", report.Buckets[0].Label)
	}
}

func TestAggregateUsage_DefaultPeriodAndDimension(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{TokensIn: 10, TokensOut: 5, TokensTotal: 15}),
	}

	// Default period = month, default dimension = agent.
	report := AggregateUsage(metas, UsageOptions{Now: now})

	wantStart := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if !report.PeriodStart.Equal(wantStart) {
		t.Errorf("defaults: want PeriodStart=%v (month), got %v", wantStart, report.PeriodStart)
	}
	if len(report.Buckets) != 1 {
		t.Fatalf("defaults: want 1 bucket (agent dimension), got %d", len(report.Buckets))
	}
}

func TestAggregateUsage_ModelDimensionUnknownFallback(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	// Session with no ByModel data — should attribute to "(unknown)".
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{TokensIn: 100, TokensOut: 50, TokensTotal: 150}),
	}

	report := AggregateUsage(metas, UsageOptions{
		Period:    UsagePeriodMonth,
		Now:       now,
		Dimension: UsageDimensionModel,
	})

	if len(report.Buckets) != 1 {
		t.Fatalf("model unknown fallback: want 1 bucket, got %d", len(report.Buckets))
	}
	if report.Buckets[0].Key != "(unknown)" {
		t.Errorf("model unknown fallback: want key=(unknown), got %s", report.Buckets[0].Key)
	}
	if report.Buckets[0].In != 100 {
		t.Errorf("model unknown fallback: want In=100, got %d", report.Buckets[0].In)
	}

	// Total should reconcile.
	if report.Total.In != report.Buckets[0].In {
		t.Errorf("model unknown fallback: Total.In=%d != Bucket.In=%d", report.Total.In, report.Buckets[0].In)
	}
}

func TestAggregateUsage_PeriodBoundsExclusive(t *testing.T) {
	// A session updated AT the exact period end should be excluded.
	dayStart := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)
	dayEnd := dayStart.Add(24 * time.Hour) // exclusive upper bound

	metas := []*UnifiedMeta{
		// Exactly at dayEnd — must be excluded (UpdatedAt.Before(end) is false).
		buildMeta("s1", "agent-a", dayEnd, SessionStats{TokensIn: 999, TokensTotal: 999}),
		// One nanosecond before dayEnd — must be included.
		buildMeta("s2", "agent-a", dayEnd.Add(-1), SessionStats{TokensIn: 5, TokensTotal: 5}),
	}

	report := AggregateUsage(metas, UsageOptions{Period: UsagePeriodDay, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.In != 5 {
		t.Errorf("exclusive bound: want Total.In=5, got %d", report.Total.In)
	}
}
