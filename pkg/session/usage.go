// Omnipus — Session Usage Aggregator
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package session — this file provides AggregateUsage, a pure function (no I/O)
// that aggregates a slice of *UnifiedMeta into a structured usage report.
// It supports four time periods (day, week, month, all) and three grouping
// dimensions (agent, model, session).  Both the REST handler
// (pkg/gateway/rest_stats.go) and the get_usage sysagent tool
// (pkg/sysagent/tools/diag.go) import this file.

package session

import (
	"sort"
	"strings"
	"time"
)

// UsagePeriod is the time window over which usage is aggregated.
type UsagePeriod string

const (
	// UsagePeriodDay covers the current UTC calendar day (00:00 – 24:00).
	UsagePeriodDay UsagePeriod = "day"
	// UsagePeriodWeek covers the current ISO week (Monday 00:00 UTC – +7d).
	UsagePeriodWeek UsagePeriod = "week"
	// UsagePeriodMonth covers the current UTC calendar month (1st – last day).
	UsagePeriodMonth UsagePeriod = "month"
	// UsagePeriodAll includes every session regardless of UpdatedAt.
	// PeriodStart is the zero time; PeriodEnd is opts.Now.
	UsagePeriodAll UsagePeriod = "all"
)

// UsageDimension controls how sessions are grouped in UsageReport.Buckets.
type UsageDimension string

const (
	// UsageDimensionAgent groups by the session's ActiveAgentID.
	UsageDimensionAgent UsageDimension = "agent"
	// UsageDimensionModel groups by model name (iterates sm.Stats.ByModel).
	UsageDimensionModel UsageDimension = "model"
	// UsageDimensionSession groups by session ID.
	UsageDimensionSession UsageDimension = "session"
)

// UsageBucket holds aggregated token counts for one grouping key.
type UsageBucket struct {
	// Key is the grouping key: agent_id, model name, or session ID.
	Key string
	// Label is the human-readable name (agent display name, model name, or session title).
	// Falls back to Key when the label cannot be resolved.
	Label string
	// In is uncached input tokens.
	In int
	// Out is output (completion) tokens.
	Out int
	// CacheRead is tokens served from the provider KV cache (not re-computed).
	CacheRead int
	// CacheWrite is tokens written into a new cache entry (Anthropic only).
	CacheWrite int
	// Total is In + Out + CacheRead + CacheWrite.
	Total int
}

// UsageOptions controls the behaviour of AggregateUsage.
type UsageOptions struct {
	// Period selects the time window.  Defaults to UsagePeriodMonth when zero.
	Period UsagePeriod
	// Now is the reference point for period calculations.  Pass time.Now().UTC().
	// Must be in UTC.
	Now time.Time
	// Dimension controls the grouping of Buckets.  Defaults to UsageDimensionAgent.
	Dimension UsageDimension
	// AgentID, when non-empty, restricts the report to sessions whose
	// ActiveAgentID matches this value.
	AgentID string
	// SessionID, when non-empty, restricts the report to the single session
	// with this ID.
	SessionID string
	// NameResolver maps an agentID to a human-readable display name.
	// Nil-safe — the aggregator skips the call when nil.
	NameResolver func(agentID string) string
	// Exclude, when non-nil, is called once per session's ActiveAgentID.
	// Sessions whose agent returns true are skipped entirely (used to exclude
	// subagent_3p workers that run on a separate engine).
	Exclude func(agentID string) bool
}

// UsageReport is the return value of AggregateUsage.
type UsageReport struct {
	// PeriodStart is the inclusive start of the aggregation window.
	// Zero for UsagePeriodAll.
	PeriodStart time.Time
	// PeriodEnd is the exclusive end of the aggregation window.
	// Equals opts.Now for UsagePeriodAll.
	PeriodEnd time.Time
	// Total is the grand total across all included sessions.  It always
	// reconciles with the sum of Buckets (In/Out/CacheRead/CacheWrite).
	Total UsageBucket
	// Buckets contains one entry per grouping key, sorted descending by Total
	// then ascending by Key.
	Buckets []UsageBucket
}

// periodBounds computes the [start, end) bounds for the requested period,
// anchored on now (UTC).  Returns a zero start for UsagePeriodAll.
func periodBounds(period UsagePeriod, now time.Time) (start, end time.Time) {
	if period == "" {
		period = UsagePeriodMonth
	}
	switch period {
	case UsagePeriodDay:
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		end = start.Add(24 * time.Hour)
	case UsagePeriodWeek:
		// ISO week: Monday is day 1.
		weekday := int(now.Weekday()) // Sunday=0 … Saturday=6
		if weekday == 0 {
			weekday = 7 // treat Sunday as day 7 so Monday=1
		}
		start = time.Date(now.Year(), now.Month(), now.Day()-weekday+1, 0, 0, 0, 0, time.UTC)
		end = start.Add(7 * 24 * time.Hour)
	case UsagePeriodMonth:
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		end = start.AddDate(0, 1, 0)
	case UsagePeriodAll:
		// Zero start signals "no time filter"; PeriodEnd == now.
		end = now
	}
	return
}

// resolveLabel returns the display name for an agent using opts.NameResolver.
// Falls back to agentID when the resolver is nil or returns an empty string.
func resolveLabel(agentID string, opts UsageOptions) string {
	if opts.NameResolver != nil {
		if name := opts.NameResolver(agentID); name != "" {
			return name
		}
	}
	return agentID
}

// AggregateUsage aggregates a slice of *UnifiedMeta into a UsageReport.
//
// The function is pure (no I/O, no goroutines, no side effects). Callers are
// responsible for:
//   - Calling sm.PostLoad() before passing metas (backfills ActiveAgentID from
//     legacy AgentID; readUnifiedMeta already does this, so ListSessions output
//     is safe to pass directly).
//   - Passing time.Now().UTC() as opts.Now.
//
// Token attribution: tokens are charged to sm.ActiveAgentID (fallback
// sm.AgentIDs[0]).  Using every entry in AgentIDs would double-count on
// agent handoffs — the existing HandleTokenStats logic is preserved exactly.
func AggregateUsage(metas []*UnifiedMeta, opts UsageOptions) UsageReport {
	if opts.Period == "" {
		opts.Period = UsagePeriodMonth
	}
	if opts.Dimension == "" {
		opts.Dimension = UsageDimensionAgent
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}

	start, end := periodBounds(opts.Period, opts.Now)

	report := UsageReport{
		PeriodStart: start,
		PeriodEnd:   end,
	}

	// accumByKey holds running totals keyed by the grouping key.
	type accumEntry struct {
		label      string
		in         int
		out        int
		cacheRead  int
		cacheWrite int
	}
	accumByKey := make(map[string]*accumEntry)

	// addToKey upserts token counts into accumByKey under key.
	addToKey := func(key, label string, in, out, cr, cw int) {
		e, ok := accumByKey[key]
		if !ok {
			e = &accumEntry{label: label}
			accumByKey[key] = e
		}
		// Prefer a non-empty label seen later (defensive: first wins if already set).
		if e.label == "" && label != "" {
			e.label = label
		}
		e.in += in
		e.out += out
		e.cacheRead += cr
		e.cacheWrite += cw
	}

	for _, sm := range metas {
		// PostLoad is idempotent; call it defensively in case the caller forgot.
		sm.PostLoad()

		// Resolve the agent responsible for this session.
		agentID := sm.ActiveAgentID
		if agentID == "" && len(sm.AgentIDs) > 0 {
			agentID = sm.AgentIDs[0]
		}
		// Skip sessions with no owning agent.
		if agentID == "" {
			continue
		}

		// Apply optional agent filter.
		if opts.AgentID != "" && agentID != opts.AgentID {
			continue
		}

		// Apply optional session filter.
		if opts.SessionID != "" && sm.ID != opts.SessionID {
			continue
		}

		// Apply Exclude predicate (skip subagent_3p).
		if opts.Exclude != nil && opts.Exclude(agentID) {
			continue
		}

		// Apply period filter (using UpdatedAt, consistent with the existing handler).
		// For UsagePeriodAll, start is zero — include everything.
		if !start.IsZero() {
			if sm.UpdatedAt.Before(start) || !sm.UpdatedAt.Before(end) {
				continue
			}
		}

		s := sm.Stats // local copy for brevity

		// Accumulate grand total.
		report.Total.In += s.TokensIn
		report.Total.Out += s.TokensOut
		report.Total.CacheRead += s.TokensCacheRead
		report.Total.CacheWrite += s.TokensCacheWrite
		report.Total.Total += s.TokensTotal

		// Accumulate into the requested dimension.
		switch opts.Dimension {
		case UsageDimensionAgent:
			label := resolveLabel(agentID, opts)
			addToKey(agentID, label, s.TokensIn, s.TokensOut, s.TokensCacheRead, s.TokensCacheWrite)

		case UsageDimensionModel:
			if len(s.ByModel) == 0 {
				// No per-model data: attribute this session's totals to "(unknown)".
				// This ensures the dimension-level sum always reconciles with Total.
				const unknownKey = "(unknown)"
				addToKey(unknownKey, unknownKey, s.TokensIn, s.TokensOut, s.TokensCacheRead, s.TokensCacheWrite)
			} else {
				for model, mt := range s.ByModel {
					// mt.In and mt.Out may be 0 for sessions that only accumulated
					// per-model CacheRead/CacheWrite (pre-Wave-1 stats accumulation path
					// in unified.go).  We use the mt values directly as recorded.
					// For model dimension, session-level TokensIn/Out are NOT added
					// separately since ByModel is meant to be the authoritative split.
					key := model
					if key == "" {
						key = "(unknown)"
					}
					addToKey(key, key, mt.In, mt.Out, mt.CacheRead, mt.CacheWrite)
				}
			}

		case UsageDimensionSession:
			label := sm.Title
			if label == "" {
				label = sm.ID
			}
			addToKey(sm.ID, label, s.TokensIn, s.TokensOut, s.TokensCacheRead, s.TokensCacheWrite)
		}
	}

	// Build and sort the Buckets slice.
	buckets := make([]UsageBucket, 0, len(accumByKey))
	for key, e := range accumByKey {
		label := e.label
		if label == "" {
			label = key
		}
		total := e.in + e.out + e.cacheRead + e.cacheWrite
		buckets = append(buckets, UsageBucket{
			Key:        key,
			Label:      label,
			In:         e.in,
			Out:        e.out,
			CacheRead:  e.cacheRead,
			CacheWrite: e.cacheWrite,
			Total:      total,
		})
	}

	// Sort descending by Total, then ascending by Key for stability.
	sort.Slice(buckets, func(i, j int) bool {
		if buckets[i].Total != buckets[j].Total {
			return buckets[i].Total > buckets[j].Total
		}
		return strings.Compare(buckets[i].Key, buckets[j].Key) < 0
	})

	report.Buckets = buckets
	return report
}
