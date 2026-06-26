// Omnipus — Session Usage Aggregator Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Production-shaped data convention (matches pkg/session/daypartition.go AppendMessage):
//
//   - An assistant turn with Tokens=T, CacheReadTokens=CR, CacheWriteTokens=CW contributes:
//       TokensOut   += T  (full turn total — uncached + cache_read + cache_write + completion)
//       TokensCacheRead  += CR  (subset of the T above, not additive)
//       TokensCacheWrite += CW  (subset of the T above, not additive)
//       TokensTotal += T
//       ByModel[m].CacheRead += CR; ByModel[m].CacheWrite += CW; ByModel[m].Total += T
//       (ByModel[m].In == 0; ByModel[m].Out == 0 on the AppendMessage path)
//   - A user/system turn with Tokens=T contributes:
//       TokensIn  += T
//       TokensTotal += T
//   - Therefore TokensIn + TokensOut == TokensTotal ALWAYS.
//   - Cache tokens are a SUBSET of TokensOut; they MUST NOT be added to produce Total.
//
// The UpdateStats path (delta merge) DOES populate ByModel.In and ByModel.Out:
//   - delta.TokensTotal is ignored; UpdateStats computes += delta.TokensIn + delta.TokensOut.
//   - ByModel all five fields merge-add.

package session

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// --- Period boundary tests ---

// TestAggregateUsage_PeriodDay verifies that only sessions in the current UTC calendar
// day are included, and yesterday's sessions are excluded.
//
// BDD:
//
//	Given sessions with UpdatedAt today and yesterday,
//	When AggregateUsage is called with period=day,
//	Then only today's session appears, with its tokens.
func TestAggregateUsage_PeriodDay(t *testing.T) {
	// Production-shaped data:
	// s1: user(100) → In=100, Out=0, Total=100
	// s2: user(200) → In=200, Out=0, Total=200 (yesterday — should be excluded)
	today := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	yesterday := time.Date(2026, 6, 25, 23, 0, 0, 0, time.UTC)

	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", today, SessionStats{TokensIn: 100, TokensOut: 0, TokensTotal: 100}),
		buildMeta("s2", "agent-a", yesterday, SessionStats{TokensIn: 200, TokensOut: 0, TokensTotal: 200}),
	}

	report := AggregateUsage(metas, UsageOptions{Period: UsagePeriodDay, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.In != 100 {
		t.Errorf("day period: want Total.In=100, got %d", report.Total.In)
	}
	if report.Total.Total != 100 {
		t.Errorf("day period: want Total.Total=100, got %d", report.Total.Total)
	}
	if len(report.Buckets) != 1 {
		t.Errorf("day period: want 1 bucket, got %d", len(report.Buckets))
	}
	if len(report.Buckets) == 1 && report.Buckets[0].Total != 100 {
		t.Errorf("day period: bucket Total want 100, got %d", report.Buckets[0].Total)
	}
	// Verify period bounds are anchored correctly.
	wantStart := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)
	if !report.PeriodStart.Equal(wantStart) {
		t.Errorf("day period: want PeriodStart=%v, got %v", wantStart, report.PeriodStart)
	}
}

// TestAggregateUsage_PeriodWeek verifies Monday-anchored week filtering.
//
// BDD:
//
//	Given sessions on Monday (this week), Friday (this week), and last Sunday,
//	When AggregateUsage is called with period=week,
//	Then only this week's sessions appear.
func TestAggregateUsage_PeriodWeek(t *testing.T) {
	// now is Thursday 2026-06-26; ISO week starts Monday 2026-06-22.
	// Production-shaped: all user turns so In == Total.
	monday := time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)
	friday := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	lastWeek := time.Date(2026, 6, 21, 23, 59, 59, 0, time.UTC) // Sunday before

	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", monday, SessionStats{TokensIn: 10, TokensOut: 0, TokensTotal: 10}),
		buildMeta("s2", "agent-a", friday, SessionStats{TokensIn: 20, TokensOut: 0, TokensTotal: 20}),
		buildMeta("s3", "agent-a", lastWeek, SessionStats{TokensIn: 999, TokensOut: 0, TokensTotal: 999}),
	}

	report := AggregateUsage(metas, UsageOptions{Period: UsagePeriodWeek, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.In != 30 {
		t.Errorf("week period: want Total.In=30, got %d", report.Total.In)
	}
	if report.Total.Total != 30 {
		t.Errorf("week period: want Total.Total=30, got %d", report.Total.Total)
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

// TestAggregateUsage_PeriodMonth verifies current UTC calendar month filtering.
//
// BDD:
//
//	Given sessions in June and May,
//	When AggregateUsage is called with period=month (now=2026-06-26),
//	Then only June sessions appear.
func TestAggregateUsage_PeriodMonth(t *testing.T) {
	// Production-shaped data: mixed turns. Assistant turn: Out=50, Total=50 (cache inside Out).
	// User turn: In=100, Total=100. Combined session: In=100, Out=50, Total=150.
	inMonth := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	lastMonth := time.Date(2026, 5, 31, 23, 0, 0, 0, time.UTC)

	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", inMonth, SessionStats{TokensIn: 100, TokensOut: 50, TokensTotal: 150}),
		buildMeta("s2", "agent-a", lastMonth, SessionStats{TokensIn: 999, TokensOut: 0, TokensTotal: 999}),
	}

	report := AggregateUsage(metas, UsageOptions{Period: UsagePeriodMonth, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.In != 100 {
		t.Errorf("month period: want Total.In=100, got %d", report.Total.In)
	}
	if report.Total.Total != 150 {
		t.Errorf("month period: want Total.Total=150, got %d", report.Total.Total)
	}
	wantStart := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if !report.PeriodStart.Equal(wantStart) {
		t.Errorf("month period: want PeriodStart=%v, got %v", wantStart, report.PeriodStart)
	}
}

// TestAggregateUsage_PeriodAll verifies all sessions are included regardless of age.
//
// BDD:
//
//	Given sessions from 2025 and 2026,
//	When AggregateUsage is called with period=all,
//	Then both sessions appear and PeriodStart is zero.
func TestAggregateUsage_PeriodAll(t *testing.T) {
	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// Production-shaped: user turns only, so In == Total.
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", t1, SessionStats{TokensIn: 50, TokensOut: 0, TokensTotal: 50}),
		buildMeta("s2", "agent-a", t2, SessionStats{TokensIn: 100, TokensOut: 0, TokensTotal: 100}),
	}

	report := AggregateUsage(metas, UsageOptions{Period: UsagePeriodAll, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.In != 150 {
		t.Errorf("all period: want Total.In=150, got %d", report.Total.In)
	}
	if report.Total.Total != 150 {
		t.Errorf("all period: want Total.Total=150, got %d", report.Total.Total)
	}
	if !report.PeriodStart.IsZero() {
		t.Errorf("all period: want zero PeriodStart, got %v", report.PeriodStart)
	}
	if !report.PeriodEnd.Equal(now) {
		t.Errorf("all period: want PeriodEnd=%v, got %v", now, report.PeriodEnd)
	}
}

// TestAggregateUsage_PeriodBoundsExclusive verifies that a session updated AT the
// exact period end is excluded, while one nanosecond before is included.
//
// BDD:
//
//	Given a session at exactly dayEnd (exclusive bound) and one at dayEnd-1ns,
//	When AggregateUsage is called with period=day,
//	Then only the dayEnd-1ns session is included.
func TestAggregateUsage_PeriodBoundsExclusive(t *testing.T) {
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
	if report.Total.Total != 5 {
		t.Errorf("exclusive bound: want Total.Total=5, got %d", report.Total.Total)
	}
}

// --- Dimension tests ---

// TestAggregateUsage_DimensionAgent verifies agent-grouped buckets with name resolution.
//
// BDD:
//
//	Given two sessions for agent-a and one for agent-b,
//	When AggregateUsage is called with dimension=agent and a name resolver,
//	Then two buckets appear sorted by Total descending, with resolved labels.
func TestAggregateUsage_DimensionAgent(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	// Production-shaped: user turns (TokensIn == TokensTotal, TokensOut == 0).
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{TokensIn: 100, TokensOut: 0, TokensTotal: 100}),
		buildMeta("s2", "agent-a", ts, SessionStats{TokensIn: 200, TokensOut: 0, TokensTotal: 200}),
		buildMeta("s3", "agent-b", ts, SessionStats{TokensIn: 10, TokensOut: 0, TokensTotal: 10}),
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
	// First bucket should be agent-a (highest total = 300).
	if report.Buckets[0].Key != "agent-a" {
		t.Errorf("agent dimension: want first bucket key=agent-a, got %s", report.Buckets[0].Key)
	}
	if report.Buckets[0].In != 300 {
		t.Errorf("agent dimension: want agent-a In=300, got %d", report.Buckets[0].In)
	}
	if report.Buckets[0].Total != 300 {
		t.Errorf("agent dimension: want agent-a Total=300, got %d", report.Buckets[0].Total)
	}
	if report.Buckets[0].Label != "Agent A" {
		t.Errorf("agent dimension: want label=Agent A, got %s", report.Buckets[0].Label)
	}
	// Second bucket is agent-b.
	if report.Buckets[1].Key != "agent-b" {
		t.Errorf("agent dimension: want second bucket key=agent-b, got %s", report.Buckets[1].Key)
	}
	if report.Buckets[1].Total != 10 {
		t.Errorf("agent dimension: want agent-b Total=10, got %d", report.Buckets[1].Total)
	}
}

// TestAggregateUsage_DimensionModel verifies model-grouped buckets using the
// AppendMessage write-path convention (ByModel only has CacheRead/CacheWrite/Total;
// In and Out stay 0 at the model level).
//
// BDD:
//
//	Given a session with ByModel for two models (AppendMessage path),
//	When AggregateUsage is called with dimension=model,
//	Then one bucket per model appears with correct Total and CacheRead,
//	     and an (unknown) bucket carries the unmodeled remainder.
func TestAggregateUsage_DimensionModel(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	// Production AppendMessage convention:
	// - assistant turn: Tokens=200, CacheRead=50 → TokensOut=200, TokensCacheRead=50, TokensTotal=200
	//   ByModel["glm-5.2"] = {In:0, Out:0, CacheRead:50, CacheWrite:0, Total:200}
	// - user turn: Tokens=30 → TokensIn=30, TokensTotal=30
	// Total session: TokensIn=30, TokensOut=200, TokensTotal=230
	// modeled total: 200; remainder: 30 → goes to (unknown)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{
			TokensIn:        30,
			TokensOut:       200,
			TokensCacheRead: 50,
			TokensTotal:     230,
			ByModel: map[string]ModelTokens{
				"glm-5.2": {In: 0, Out: 0, CacheRead: 50, CacheWrite: 0, Total: 200},
			},
		}),
		// Second session: two models (AppendMessage path)
		buildMeta("s2", "agent-a", ts, SessionStats{
			TokensIn:    20,
			TokensOut:   100,
			TokensTotal: 120,
			ByModel: map[string]ModelTokens{
				"glm-5.2":           {In: 0, Out: 0, CacheRead: 0, CacheWrite: 0, Total: 60},
				"claude-sonnet-4-6": {In: 0, Out: 0, CacheRead: 10, CacheWrite: 5, Total: 40},
			},
		}),
	}

	report := AggregateUsage(metas, UsageOptions{
		Period:    UsagePeriodMonth,
		Now:       now,
		Dimension: UsageDimensionModel,
	})

	// Expected buckets:
	// "glm-5.2": Total = 200 + 60 = 260, CacheRead = 50 + 0 = 50
	// "claude-sonnet-4-6": Total = 40, CacheRead = 10, CacheWrite = 5
	// "(unknown)": remainder = (30) from s1 + (20) from s2 = 50
	// Total: 260 + 40 + 50 = 350 == report.Total.Total (30+200+20+100=350)

	if report.Total.Total != 350 {
		t.Errorf("model dimension: want Total.Total=350, got %d", report.Total.Total)
	}

	var glm, claude, unknown *UsageBucket
	for i := range report.Buckets {
		switch report.Buckets[i].Key {
		case "glm-5.2":
			b := report.Buckets[i]
			glm = &b
		case "claude-sonnet-4-6":
			b := report.Buckets[i]
			claude = &b
		case "(unknown)":
			b := report.Buckets[i]
			unknown = &b
		}
	}

	if glm == nil {
		t.Fatal("model dimension: glm-5.2 bucket not found")
	}
	if glm.Total != 260 {
		t.Errorf("model dimension: glm-5.2 Total want 260, got %d", glm.Total)
	}
	if glm.CacheRead != 50 {
		t.Errorf("model dimension: glm-5.2 CacheRead want 50, got %d", glm.CacheRead)
	}

	if claude == nil {
		t.Fatal("model dimension: claude-sonnet-4-6 bucket not found")
	}
	if claude.Total != 40 {
		t.Errorf("model dimension: claude-sonnet-4-6 Total want 40, got %d", claude.Total)
	}
	if claude.CacheRead != 10 {
		t.Errorf("model dimension: claude-sonnet-4-6 CacheRead want 10, got %d", claude.CacheRead)
	}
	if claude.CacheWrite != 5 {
		t.Errorf("model dimension: claude-sonnet-4-6 CacheWrite want 5, got %d", claude.CacheWrite)
	}

	if unknown == nil {
		t.Fatal("model dimension: (unknown) bucket not found")
	}
	if unknown.Total != 50 {
		t.Errorf("model dimension: (unknown) Total want 50, got %d", unknown.Total)
	}
}

// TestAggregateUsage_DimensionSession verifies session-grouped buckets and label fallback.
//
// BDD:
//
//	Given two sessions, one with a custom title and one without,
//	When AggregateUsage is called with dimension=session,
//	Then each session gets its own bucket; the titled one uses the title as Label.
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
			if b.Total != 150 {
				t.Errorf("session dimension: sess-1 Total want 150, got %d", b.Total)
			}
		}
	}
	if !found {
		t.Error("session dimension: sess-1 bucket not found")
	}
}

// --- Cache split test ---

// TestAggregateUsage_CacheSplit verifies cache tokens are tracked as a subset of Out,
// not added on top, and that Total == In + Out (the AUTHORITATIVE convention).
//
// BDD:
//
//	Given a session with TokensIn=30, TokensOut=200, CacheRead=50, CacheWrite=20,
//	      TokensTotal=230 (cache inside Out),
//	When AggregateUsage is called with dimension=agent,
//	Then the bucket Total==230 (not 300), and CacheRead==50, CacheWrite==20.
//
// This is the C1 fix: cache MUST NOT be double-counted.
func TestAggregateUsage_CacheSplit(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	// Production convention:
	// user turn: Tokens=30 → TokensIn=30, TokensTotal+=30
	// assistant turn: Tokens=200, CacheRead=50, CacheWrite=20
	//   → TokensOut=200, TokensCacheRead=50, TokensCacheWrite=20, TokensTotal+=200
	// Session totals: In=30, Out=200, CacheRead=50, CacheWrite=20, Total=230
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{
			TokensIn:         30,
			TokensOut:        200,
			TokensCacheRead:  50,
			TokensCacheWrite: 20,
			TokensTotal:      230,
		}),
	}

	report := AggregateUsage(metas, UsageOptions{Period: UsagePeriodMonth, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.CacheRead != 50 {
		t.Errorf("cache split: want CacheRead=50, got %d", report.Total.CacheRead)
	}
	if report.Total.CacheWrite != 20 {
		t.Errorf("cache split: want CacheWrite=20, got %d", report.Total.CacheWrite)
	}
	// AUTHORITATIVE: Total must be 230, not In+Out+CacheRead+CacheWrite=300.
	if report.Total.Total != 230 {
		t.Errorf("cache split: want Total.Total=230 (authoritative stored value), got %d — cache must NOT be double-counted", report.Total.Total)
	}
	if len(report.Buckets) != 1 {
		t.Fatalf("cache split: want 1 bucket, got %d", len(report.Buckets))
	}
	if report.Buckets[0].CacheRead != 50 {
		t.Errorf("cache split bucket: want CacheRead=50, got %d", report.Buckets[0].CacheRead)
	}
	if report.Buckets[0].Total != 230 {
		t.Errorf("cache split bucket: want Total=230, got %d — cache must NOT be double-counted", report.Buckets[0].Total)
	}
}

// --- Exclude and filter tests ---

// TestAggregateUsage_ExcludeSubagent3p verifies that the Exclude predicate suppresses
// sessions from external CLI agents.
//
// BDD:
//
//	Given a native agent session and an external agent session,
//	When AggregateUsage is called with Exclude targeting the external agent,
//	Then only the native agent's bucket appears.
func TestAggregateUsage_ExcludeSubagent3p(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-native", ts, SessionStats{TokensIn: 100, TokensOut: 0, TokensTotal: 100}),
		buildMeta("s2", "agent-external", ts, SessionStats{TokensIn: 999, TokensOut: 0, TokensTotal: 999}),
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
	if report.Total.Total != 100 {
		t.Errorf("exclude: want Total.Total=100, got %d", report.Total.Total)
	}
	if len(report.Buckets) != 1 {
		t.Errorf("exclude: want 1 bucket, got %d", len(report.Buckets))
	}
	if len(report.Buckets) == 1 && report.Buckets[0].Key == "agent-external" {
		t.Error("exclude: external agent must not appear in buckets")
	}
}

// TestAggregateUsage_AgentIDFilter verifies that AgentID restricts the report to one agent.
//
// BDD:
//
//	Given sessions for two agents,
//	When AggregateUsage is called with AgentID=agent-a,
//	Then only agent-a's session appears.
func TestAggregateUsage_AgentIDFilter(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{TokensIn: 100, TokensOut: 0, TokensTotal: 100}),
		buildMeta("s2", "agent-b", ts, SessionStats{TokensIn: 200, TokensOut: 0, TokensTotal: 200}),
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
	if report.Total.Total != 100 {
		t.Errorf("agent filter: want Total.Total=100, got %d", report.Total.Total)
	}
	if len(report.Buckets) != 1 {
		t.Errorf("agent filter: want 1 bucket, got %d", len(report.Buckets))
	}
}

// TestAggregateUsage_SessionIDFilter verifies that SessionID restricts the report
// to the single matching session.
//
// BDD:
//
//	Given two sessions, one targeted,
//	When AggregateUsage is called with SessionID set to the target,
//	Then only the target session appears.
func TestAggregateUsage_SessionIDFilter(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("sess-target", "agent-a", ts, SessionStats{TokensIn: 50, TokensOut: 25, TokensTotal: 75}),
		buildMeta("sess-other", "agent-a", ts, SessionStats{TokensIn: 999, TokensOut: 0, TokensTotal: 999}),
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
	if report.Total.Total != 75 {
		t.Errorf("session filter: want Total.Total=75, got %d", report.Total.Total)
	}
	if len(report.Buckets) != 1 {
		t.Errorf("session filter: want 1 bucket, got %d", len(report.Buckets))
	}
}

// --- Edge-case / nil-safety tests ---

// TestAggregateUsage_EmptyInput verifies that a nil slice returns a zero report.
func TestAggregateUsage_EmptyInput(t *testing.T) {
	report := AggregateUsage(nil, UsageOptions{Period: UsagePeriodMonth, Now: now, Dimension: UsageDimensionAgent})

	if report.Total.In != 0 || report.Total.Out != 0 || report.Total.Total != 0 {
		t.Errorf("empty input: want zero Total, got %+v", report.Total)
	}
	if len(report.Buckets) != 0 {
		t.Errorf("empty input: want 0 buckets, got %d", len(report.Buckets))
	}
}

// TestAggregateUsage_NameResolverNilSafe verifies that a nil NameResolver falls back
// to using the agent ID as the label.
func TestAggregateUsage_NameResolverNilSafe(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-x", ts, SessionStats{TokensIn: 100, TokensOut: 0, TokensTotal: 100}),
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

// TestAggregateUsage_DefaultPeriodAndDimension verifies that zero values produce
// period=month and dimension=agent defaults.
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
	if report.Buckets[0].Total != 15 {
		t.Errorf("defaults: want bucket Total=15, got %d", report.Buckets[0].Total)
	}
}

// TestAggregateUsage_ModelDimensionUnknownFallback verifies that a session with no
// ByModel data produces a single "(unknown)" bucket carrying Total only (In/Out==0).
//
// BDD:
//
//	Given a session with no ByModel (all tokens unmodeled),
//	When AggregateUsage is called with dimension=model,
//	Then a single (unknown) bucket appears with Total==TokensTotal, In==0, Out==0.
//
// NOTE: In the model dimension, (unknown) buckets are Total-only (In/Out not tracked
// at the model level for input tokens on the AppendMessage path).
func TestAggregateUsage_ModelDimensionUnknownFallback(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	// Session with no ByModel — all 150 tokens go to (unknown).
	// Production convention: user(100)+assistant(50) → In=100, Out=50, Total=150
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
	// (unknown) bucket carries Total only; In/Out stay 0.
	if report.Buckets[0].Total != 150 {
		t.Errorf("model unknown fallback: want Total=150, got %d", report.Buckets[0].Total)
	}
	if report.Buckets[0].In != 0 {
		t.Errorf("model unknown fallback: (unknown) bucket In must be 0 (Total-only convention), got %d", report.Buckets[0].In)
	}
	// Total reconciles: the single (unknown) bucket covers the full session total.
	if report.Total.Total != report.Buckets[0].Total {
		t.Errorf("model unknown fallback: Total.Total=%d != Bucket.Total=%d", report.Total.Total, report.Buckets[0].Total)
	}
}

// --- Reconciliation tests (headline guarantee) ---

// TestAggregateUsage_TotalReconciles_AllDimensions asserts that for each of the three
// dimensions (agent, model, session), the sum of bucket.Total equals report.Total.Total.
//
// This is the headline reconciliation guarantee: no tokens lost, no tokens double-counted.
// Would have FAILED before the Total semantics change (old code: sum buckets via In+Out+cache).
//
// Traces to: token-usage-tracking-2026-06.md §AggregateUsage guarantees
func TestAggregateUsage_TotalReconciles_AllDimensions(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	// Two sessions:
	// s1: user(30)+assistant(200, CacheRead=50) → In=30, Out=200, CR=50, Total=230
	//     ByModel["glm-5.2"] = {Total:200, CacheRead:50}
	// s2: user(20)+assistant(80) → In=20, Out=80, Total=100
	//     ByModel["claude-3-haiku"] = {Total:80}
	// Grand total: In=50, Out=280, Total=330
	sessions := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{
			TokensIn:        30,
			TokensOut:       200,
			TokensCacheRead: 50,
			TokensTotal:     230,
			ByModel: map[string]ModelTokens{
				"glm-5.2": {In: 0, Out: 0, CacheRead: 50, Total: 200},
			},
		}),
		buildMeta("s2", "agent-b", ts, SessionStats{
			TokensIn:    20,
			TokensOut:   80,
			TokensTotal: 100,
			ByModel: map[string]ModelTokens{
				"claude-3-haiku": {In: 0, Out: 0, Total: 80},
			},
		}),
	}

	dimensions := []UsageDimension{
		UsageDimensionAgent,
		UsageDimensionModel,
		UsageDimensionSession,
	}

	for _, dim := range dimensions {
		t.Run(string(dim), func(t *testing.T) {
			report := AggregateUsage(sessions, UsageOptions{
				Period:    UsagePeriodMonth,
				Now:       now,
				Dimension: dim,
			})

			var bucketTotalSum int
			for _, b := range report.Buckets {
				bucketTotalSum += b.Total
			}

			if bucketTotalSum != report.Total.Total {
				t.Errorf("dimension %s: sum(bucket.Total)=%d != report.Total.Total=%d — reconciliation FAILED",
					dim, bucketTotalSum, report.Total.Total)
			}

			// Grand total must be 330 regardless of dimension.
			if report.Total.Total != 330 {
				t.Errorf("dimension %s: want Total.Total=330, got %d", dim, report.Total.Total)
			}
		})
	}
}

// TestAggregateUsage_TotalReconciles_ModelDimension_UnmodeledRemainder verifies that
// when ΣByModel.Total < TokensTotal, the difference is folded into "(unknown)" so the
// model dimension's bucket sum still reconciles with report.Total.Total.
//
// Traces to: token-usage-tracking-2026-06.md §AggregateUsage model dimension
func TestAggregateUsage_TotalReconciles_ModelDimension_UnmodeledRemainder(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	// Session: user(30)+assistant(200) → Total=230, but only ByModel total is 200.
	// Unmodeled remainder = 230 - 200 = 30 → (unknown) bucket.
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{
			TokensIn:    30,
			TokensOut:   200,
			TokensTotal: 230,
			ByModel: map[string]ModelTokens{
				"glm-5.2": {Total: 200},
			},
		}),
	}

	report := AggregateUsage(metas, UsageOptions{
		Period:    UsagePeriodMonth,
		Now:       now,
		Dimension: UsageDimensionModel,
	})

	// Should have two buckets: "glm-5.2" (200) and "(unknown)" (30).
	if len(report.Buckets) != 2 {
		t.Fatalf("unmodeled remainder: want 2 buckets (glm-5.2 + (unknown)), got %d: %v",
			len(report.Buckets), report.Buckets)
	}

	var unknownBucket *UsageBucket
	var glmBucket *UsageBucket
	for i := range report.Buckets {
		switch report.Buckets[i].Key {
		case "(unknown)":
			b := report.Buckets[i]
			unknownBucket = &b
		case "glm-5.2":
			b := report.Buckets[i]
			glmBucket = &b
		}
	}

	if unknownBucket == nil {
		t.Fatal("unmodeled remainder: (unknown) bucket not found")
	}
	if unknownBucket.Total != 30 {
		t.Errorf("unmodeled remainder: (unknown) Total want 30, got %d", unknownBucket.Total)
	}
	if glmBucket == nil {
		t.Fatal("unmodeled remainder: glm-5.2 bucket not found")
	}
	if glmBucket.Total != 200 {
		t.Errorf("unmodeled remainder: glm-5.2 Total want 200, got %d", glmBucket.Total)
	}

	// Reconciliation: 200 + 30 = 230 == report.Total.Total.
	var bucketSum int
	for _, b := range report.Buckets {
		bucketSum += b.Total
	}
	if bucketSum != report.Total.Total {
		t.Errorf("unmodeled remainder: sum(bucket.Total)=%d != report.Total.Total=%d", bucketSum, report.Total.Total)
	}
}

// TestAggregateUsage_ModelDimension_CarriesFullTokens verifies that the model bucket
// Total is the FULL turn total (mt.Total from ByModel), not just the cache-read portion.
//
// This is the C2 bug fix test: before the fix, a bucket might show Total==CacheRead only
// instead of the full turn total. Would have FAILED against old code that used In+Out+cache.
//
// BDD:
//
//	Given a session with ByModel["m1"]{Total:500, CacheRead:100},
//	When AggregateUsage is called with dimension=model,
//	Then the m1 bucket Total==500, not 100.
//
// Traces to: token-usage-tracking-2026-06.md §C2 ModelTokens.Total as authoritative
func TestAggregateUsage_ModelDimension_CarriesFullTokens(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{
			TokensIn:    0,
			TokensOut:   500,
			TokensTotal: 500,
			ByModel: map[string]ModelTokens{
				"m1": {In: 0, Out: 0, CacheRead: 100, CacheWrite: 0, Total: 500},
			},
		}),
	}

	report := AggregateUsage(metas, UsageOptions{
		Period:    UsagePeriodMonth,
		Now:       now,
		Dimension: UsageDimensionModel,
	})

	if len(report.Buckets) != 1 {
		t.Fatalf("C2 fix: want 1 bucket (m1), got %d: %v", len(report.Buckets), report.Buckets)
	}
	b := report.Buckets[0]
	if b.Key != "m1" {
		t.Fatalf("C2 fix: want key=m1, got %s", b.Key)
	}
	if b.Total != 500 {
		t.Errorf("C2 fix: m1 bucket Total must be 500 (full turn total), got %d — NOT the cache-read-only value (100)", b.Total)
	}
	if b.CacheRead != 100 {
		t.Errorf("C2 fix: m1 CacheRead want 100, got %d", b.CacheRead)
	}
}

// TestAggregateUsage_CacheNotDoubleCounted verifies that cache tokens (which are already
// a SUBSET of TokensOut) do not inflate the Total.
//
// This is the C1 bug fix test: before the fix, Total was computed as In+Out+CacheRead+CacheWrite.
// Would have FAILED against old code that summed cache on top.
//
// BDD:
//
//	Given a session with TokensOut=200, TokensTotal=200, CacheRead=50, CacheWrite=20
//	  (cache tokens are a SUBSET of Out — not additive),
//	When AggregateUsage is called with dimension=agent,
//	Then the bucket Total==200 (NOT 270 = 200+50+20).
//
// Traces to: token-usage-tracking-2026-06.md §C1 cache-as-subset-of-Out
func TestAggregateUsage_CacheNotDoubleCounted(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{
			TokensIn:         0,
			TokensOut:        200,
			TokensCacheRead:  50,
			TokensCacheWrite: 20,
			TokensTotal:      200, // cache is INSIDE the 200, not added
		}),
	}

	report := AggregateUsage(metas, UsageOptions{
		Period:    UsagePeriodMonth,
		Now:       now,
		Dimension: UsageDimensionAgent,
	})

	if len(report.Buckets) != 1 {
		t.Fatalf("C1 fix: want 1 bucket, got %d", len(report.Buckets))
	}
	b := report.Buckets[0]
	if b.Total != 200 {
		t.Errorf("C1 fix: bucket Total must be 200 (authoritative stored total), got %d — cache must NOT be double-counted (200+50+20=270 is WRONG)", b.Total)
	}
	if report.Total.Total != 200 {
		t.Errorf("C1 fix: report.Total.Total must be 200, got %d", report.Total.Total)
	}
}

// --- ParseUsagePeriod tests ---

// TestParseUsagePeriod validates the full set of valid and invalid period strings.
//
// BDD:
//
//	Given a raw period string,
//	When ParseUsagePeriod is called,
//	Then valid strings return the typed period and ok=true;
//	     invalid strings return ("", false);
//	     empty string defaults to UsagePeriodMonth.
func TestParseUsagePeriod(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		want   UsagePeriod
		wantOK bool
	}{
		{name: "empty defaults to month", raw: "", want: UsagePeriodMonth, wantOK: true},
		{name: "day valid", raw: "day", want: UsagePeriodDay, wantOK: true},
		{name: "week valid", raw: "week", want: UsagePeriodWeek, wantOK: true},
		{name: "month valid", raw: "month", want: UsagePeriodMonth, wantOK: true},
		{name: "all valid", raw: "all", want: UsagePeriodAll, wantOK: true},
		{name: "garbage rejected", raw: "garbage", want: "", wantOK: false},
		{name: "DAY (uppercase) rejected", raw: "DAY", want: "", wantOK: false},
		{name: "weekly rejected", raw: "weekly", want: "", wantOK: false},
		{name: "monthly rejected", raw: "monthly", want: "", wantOK: false},
		{name: "space-padded rejected", raw: " day", want: "", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseUsagePeriod(tc.raw)
			if ok != tc.wantOK {
				t.Errorf("ParseUsagePeriod(%q): got ok=%v, want ok=%v", tc.raw, ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("ParseUsagePeriod(%q): got period=%q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// --- ParseUsageDimension tests ---

// TestParseUsageDimension validates the full set of valid and invalid dimension strings.
//
// BDD:
//
//	Given a raw dimension string,
//	When ParseUsageDimension is called,
//	Then valid strings return the typed dimension and ok=true;
//	     invalid strings return ("", false);
//	     empty string defaults to UsageDimensionAgent.
func TestParseUsageDimension(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		want   UsageDimension
		wantOK bool
	}{
		{name: "empty defaults to agent", raw: "", want: UsageDimensionAgent, wantOK: true},
		{name: "agent valid", raw: "agent", want: UsageDimensionAgent, wantOK: true},
		{name: "model valid", raw: "model", want: UsageDimensionModel, wantOK: true},
		{name: "session valid", raw: "session", want: UsageDimensionSession, wantOK: true},
		{name: "garbage rejected", raw: "garbage", want: "", wantOK: false},
		{name: "AGENT (uppercase) rejected", raw: "AGENT", want: "", wantOK: false},
		{name: "agents (plural) rejected", raw: "agents", want: "", wantOK: false},
		{name: "space-padded rejected", raw: " model", want: "", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseUsageDimension(tc.raw)
			if ok != tc.wantOK {
				t.Errorf("ParseUsageDimension(%q): got ok=%v, want ok=%v", tc.raw, ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("ParseUsageDimension(%q): got dimension=%q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// --- G3: Phantom bucket (zero-token external CLI model) ---

// TestAggregateUsage_G3_NoPhantoModelBucket verifies that a zero-token assistant entry
// from an external CLI (model="claude-code", Tokens=0) does NOT produce a non-zero
// "claude-code" bucket in the model dimension.
//
// BDD:
//
//	Given a session containing only a zero-token external CLI entry (Model="claude-code"),
//	When AggregateUsage is called with dimension=model,
//	Then either no "claude-code" bucket appears, or if it appears, its Total==0.
//	     The (unknown) bucket remainder is also 0 (TokensTotal==0).
//
// Guards against: phantom model buckets inflating the model view with zero-value noise.
// Traces to: token-usage-tracking-2026-06.md §Wave1 item 3 (scope guard G3)
func TestAggregateUsage_G3_NoPhantoModelBucket(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "g3-agent")

	meta, err := ps.NewSession("cli", "test-model", "anthropic")
	require.NoError(t, err)

	// External CLI turn: Tokens=0, CacheRead=0, CacheWrite=0, Model="claude-code"
	extEntry := TranscriptEntry{
		ID:               "ext-001",
		Role:             "assistant",
		Content:          "Result from external CLI",
		Model:            "claude-code",
		Tokens:           0,
		CacheReadTokens:  0,
		CacheWriteTokens: 0,
		Timestamp:        time.Now().UTC(),
	}
	require.NoError(t, ps.AppendMessage(meta.ID, extEntry))

	// Read back the meta and confirm zero stats.
	updatedMeta, err := ps.GetMeta(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, updatedMeta.Stats.TokensTotal,
		"external CLI turn must not inflate TokensTotal")

	// Build a UnifiedMeta for AggregateUsage.
	um := &UnifiedMeta{
		SessionMeta: *updatedMeta,
		Type:        SessionTypeChat,
	}
	um.ActiveAgentID = "g3-agent"

	report := AggregateUsage([]*UnifiedMeta{um}, UsageOptions{
		Period:    UsagePeriodAll,
		Now:       time.Now().UTC(),
		Dimension: UsageDimensionModel,
	})

	// Either no bucket, or every bucket has Total==0 (zero-Total entries are noise).
	for _, b := range report.Buckets {
		if b.Total > 0 {
			t.Errorf("G3: phantom bucket %q has non-zero Total=%d — external CLI must not create real usage buckets",
				b.Key, b.Total)
		}
	}
	// The grand total must also be zero.
	if report.Total.Total != 0 {
		t.Errorf("G3: report.Total.Total must be 0 for zero-token session, got %d", report.Total.Total)
	}
}

// --- G4: UpdateStats delta merge ---

// TestUpdateStats_G4_ByModelMerge verifies that UpdateStats correctly merge-adds all
// five ByModel fields (In, Out, CacheRead, CacheWrite, Total) and that
// TokensCacheRead/Write also delta-add correctly.
//
// BDD:
//
//	Given a fresh session with no ByModel data,
//	When UpdateStats is called with a delta carrying ByModel and cache fields,
//	Then all five ByModel fields merge-add, TokensCacheRead/Write accumulate,
//	     and the second call sums on top of the first.
//
// Guards against: UpdateStats silently dropping ByModel entries or resetting cache fields.
// Traces to: token-usage-tracking-2026-06.md §UpdateStats G4
func TestUpdateStats_G4_ByModelMerge(t *testing.T) {
	home := t.TempDir()
	ps := NewPartitionStore(home, "g4-agent")

	meta, err := ps.NewSession("cli", "model-a", "provider")
	require.NoError(t, err)
	sessionID := meta.ID

	delta1 := SessionStats{
		TokensIn:         30,
		TokensOut:        100,
		TokensCacheRead:  20,
		TokensCacheWrite: 5,
		ByModel: map[string]ModelTokens{
			"gpt-4o": {In: 30, Out: 100, CacheRead: 20, CacheWrite: 5, Total: 130},
		},
	}
	require.NoError(t, ps.UpdateStats(sessionID, delta1))

	got, err := ps.GetMeta(sessionID)
	require.NoError(t, err)

	assert.Equal(t, 30, got.Stats.TokensIn, "after delta1: TokensIn must be 30")
	assert.Equal(t, 100, got.Stats.TokensOut, "after delta1: TokensOut must be 100")
	// UpdateStats computes TokensTotal += In+Out (ignores delta.TokensTotal).
	assert.Equal(t, 130, got.Stats.TokensTotal, "after delta1: TokensTotal must be In+Out=130")
	assert.Equal(t, 20, got.Stats.TokensCacheRead, "after delta1: TokensCacheRead must be 20")
	assert.Equal(t, 5, got.Stats.TokensCacheWrite, "after delta1: TokensCacheWrite must be 5")

	require.NotNil(t, got.Stats.ByModel, "ByModel must be non-nil after UpdateStats with ByModel delta")
	require.Contains(t, got.Stats.ByModel, "gpt-4o")
	gpt := got.Stats.ByModel["gpt-4o"]
	assert.Equal(t, 30, gpt.In, "gpt-4o.In must be 30")
	assert.Equal(t, 100, gpt.Out, "gpt-4o.Out must be 100")
	assert.Equal(t, 20, gpt.CacheRead, "gpt-4o.CacheRead must be 20")
	assert.Equal(t, 5, gpt.CacheWrite, "gpt-4o.CacheWrite must be 5")
	assert.Equal(t, 130, gpt.Total, "gpt-4o.Total must be 130")

	// Second delta — must accumulate on top, not replace.
	delta2 := SessionStats{
		TokensIn:         10,
		TokensOut:        50,
		TokensCacheRead:  8,
		TokensCacheWrite: 2,
		ByModel: map[string]ModelTokens{
			"gpt-4o":   {In: 10, Out: 50, CacheRead: 8, CacheWrite: 2, Total: 60},
			"claude-3": {In: 0, Out: 40, CacheRead: 0, CacheWrite: 0, Total: 40},
		},
	}
	require.NoError(t, ps.UpdateStats(sessionID, delta2))

	got2, err := ps.GetMeta(sessionID)
	require.NoError(t, err)

	assert.Equal(t, 40, got2.Stats.TokensIn, "after delta2: TokensIn must sum to 30+10=40")
	assert.Equal(t, 150, got2.Stats.TokensOut, "after delta2: TokensOut must sum to 100+50=150")
	assert.Equal(t, 190, got2.Stats.TokensTotal, "after delta2: TokensTotal must be 40+150=190")
	assert.Equal(t, 28, got2.Stats.TokensCacheRead, "after delta2: TokensCacheRead must be 20+8=28")
	assert.Equal(t, 7, got2.Stats.TokensCacheWrite, "after delta2: TokensCacheWrite must be 5+2=7")

	gpt2 := got2.Stats.ByModel["gpt-4o"]
	assert.Equal(t, 40, gpt2.In, "gpt-4o.In must accumulate 30+10=40")
	assert.Equal(t, 150, gpt2.Out, "gpt-4o.Out must accumulate 100+50=150")
	assert.Equal(t, 28, gpt2.CacheRead, "gpt-4o.CacheRead must accumulate 20+8=28")
	assert.Equal(t, 7, gpt2.CacheWrite, "gpt-4o.CacheWrite must accumulate 5+2=7")
	assert.Equal(t, 190, gpt2.Total, "gpt-4o.Total must accumulate 130+60=190")

	claude := got2.Stats.ByModel["claude-3"]
	assert.Equal(t, 0, claude.In, "claude-3.In must be 0")
	assert.Equal(t, 40, claude.Out, "claude-3.Out must be 40")
	assert.Equal(t, 40, claude.Total, "claude-3.Total must be 40")
}

// --- G2: External CLI sub-turn scope guard ---

// TestAppendTranscript_G2_ExternalCLITurn_ZeroTokens verifies that an assistant entry
// written for an external CLI sub-turn (Tokens=0, all cache fields=0) contributes
// exactly zero to all SessionStats token fields.
//
// This is a simulation test: the real production gate (asserting that external-cli
// dispatch never calls AddTurnStats) cannot be written as a narrow unit test without
// linking pkg/agent (OOM risk). This test instead verifies the behavioral invariant
// from the session side: a zero-token entry MUST NOT inflate any stats field.
//
// The production scope guard is: external CLI dispatch (pkg/agent/external_dispatch.go)
// calls AppendTranscript with Tokens=0 and NEVER calls AddTurnStats/AddTurnCacheStats.
// This test verifies the session-side consequence of that contract.
//
// NOTE: A full production-gate test asserting that external_dispatch.go does NOT call
// AddTurnStats is deferred — it would require linking pkg/agent which risks OOM in CI.
// See token-usage-tracking-2026-06.md §G2 deferral note.
//
// Traces to: token-usage-tracking-2026-06.md §Wave1 item 3 (scope guard G2)
func TestAppendTranscript_G2_ExternalCLITurn_ZeroTokens(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "native-agent")
	require.NoError(t, err)

	// External CLI sub-turn: produced text, but no native engine was used.
	// Tokens=0 because AddTurnStats is never called for this path.
	extEntry := TranscriptEntry{
		ID:               "ext-g2-001",
		Role:             "assistant",
		Content:          "Result from external claude-code CLI",
		Model:            "claude-code",
		Tokens:           0,
		CacheReadTokens:  0,
		CacheWriteTokens: 0,
		Timestamp:        time.Now().UTC(),
	}
	require.NoError(t, store.AppendTranscript(meta.ID, extEntry))

	got, err := store.GetMeta(meta.ID)
	require.NoError(t, err)

	assert.Equal(t, 0, got.Stats.TokensTotal,
		"G2: external CLI turn must contribute zero to TokensTotal")
	assert.Equal(t, 0, got.Stats.TokensOut,
		"G2: external CLI turn must contribute zero to TokensOut")
	assert.Equal(t, 0, got.Stats.TokensCacheRead,
		"G2: external CLI turn must contribute zero to TokensCacheRead")
	assert.Equal(t, 0, got.Stats.TokensCacheWrite,
		"G2: external CLI turn must contribute zero to TokensCacheWrite")

	// Even if ByModel is populated (zero-valued entry), the Total must be 0.
	if got.Stats.ByModel != nil {
		if mt, ok := got.Stats.ByModel["claude-code"]; ok {
			assert.Equal(t, 0, mt.Total,
				"G2: claude-code ByModel entry must have Total=0 for zero-token external turn")
		}
	}
}

// --- Reconciliation: in/out sub-fields for agent/session dimensions ---

// TestAggregateUsage_InOutReconciliation verifies that for agent and session dimensions,
// the sum of bucket.In/Out/CacheRead/CacheWrite across all buckets equals report.Total
// sub-fields. (For model dimension, In/Out are 0 in most buckets — not checked here.)
func TestAggregateUsage_InOutReconciliation(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	// Production data: two sessions with mixed user+assistant turns.
	// s1: In=100, Out=50 (CacheRead=20, CacheWrite=10), Total=150
	// s2: In=200, Out=80, Total=280
	metas := []*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{
			TokensIn: 100, TokensOut: 50, TokensCacheRead: 20, TokensCacheWrite: 10, TokensTotal: 150,
		}),
		buildMeta("s2", "agent-b", ts, SessionStats{
			TokensIn: 200, TokensOut: 80, TokensTotal: 280,
		}),
	}

	for _, dim := range []UsageDimension{UsageDimensionAgent, UsageDimensionSession} {
		t.Run(string(dim), func(t *testing.T) {
			report := AggregateUsage(metas, UsageOptions{
				Period:    UsagePeriodMonth,
				Now:       now,
				Dimension: dim,
			})

			var sumIn, sumOut, sumCR, sumCW, sumTotal int
			for _, b := range report.Buckets {
				sumIn += b.In
				sumOut += b.Out
				sumCR += b.CacheRead
				sumCW += b.CacheWrite
				sumTotal += b.Total
			}

			if sumIn != report.Total.In {
				t.Errorf("%s: bucket sum In=%d != Total.In=%d", dim, sumIn, report.Total.In)
			}
			if sumOut != report.Total.Out {
				t.Errorf("%s: bucket sum Out=%d != Total.Out=%d", dim, sumOut, report.Total.Out)
			}
			if sumCR != report.Total.CacheRead {
				t.Errorf("%s: bucket sum CacheRead=%d != Total.CacheRead=%d", dim, sumCR, report.Total.CacheRead)
			}
			if sumCW != report.Total.CacheWrite {
				t.Errorf("%s: bucket sum CacheWrite=%d != Total.CacheWrite=%d", dim, sumCW, report.Total.CacheWrite)
			}
			if sumTotal != report.Total.Total {
				t.Errorf("%s: bucket sum Total=%d != Total.Total=%d", dim, sumTotal, report.Total.Total)
			}
		})
	}
}

// --- Differentiation test (hardcoded-response guard) ---

// TestAggregateUsage_DifferentInputsDifferentOutputs asserts that two different
// sessions produce two different Total.Total values — catching hardcoded responses.
//
// If a function always returns the same report regardless of input, this test fails.
func TestAggregateUsage_DifferentInputsDifferentOutputs(t *testing.T) {
	ts := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

	opts := UsageOptions{Period: UsagePeriodMonth, Now: now, Dimension: UsageDimensionAgent}

	report1 := AggregateUsage([]*UnifiedMeta{
		buildMeta("s1", "agent-a", ts, SessionStats{TokensIn: 100, TokensTotal: 100}),
	}, opts)

	report2 := AggregateUsage([]*UnifiedMeta{
		buildMeta("s2", "agent-a", ts, SessionStats{TokensIn: 500, TokensTotal: 500}),
	}, opts)

	if report1.Total.Total == report2.Total.Total {
		t.Errorf("differentiation: two different inputs produced the same Total.Total=%d — function may be hardcoded",
			report1.Total.Total)
	}
	if report1.Total.Total != 100 {
		t.Errorf("differentiation: first report Total.Total want 100, got %d", report1.Total.Total)
	}
	if report2.Total.Total != 500 {
		t.Errorf("differentiation: second report Total.Total want 500, got %d", report2.Total.Total)
	}
}
