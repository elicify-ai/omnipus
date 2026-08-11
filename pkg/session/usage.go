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
	// UsagePeriodWeek covers the current Monday-anchored week (Monday 00:00 UTC – +7d).
	UsagePeriodWeek UsagePeriod = "week"
	// UsagePeriodMonth covers the current UTC calendar month (1st – last day).
	UsagePeriodMonth UsagePeriod = "month"
	// UsagePeriodAll includes every session regardless of UpdatedAt.
	// PeriodStart is the zero time; PeriodEnd is opts.Now.
	UsagePeriodAll UsagePeriod = "all"
)

// ParseUsagePeriod validates a raw period string and returns the typed value.
// An empty string defaults to UsagePeriodMonth. Any unrecognized value returns
// ok=false so callers (the REST handler and the get_usage tool) can reject it
// instead of silently coercing — the single source of truth for the valid set.
func ParseUsagePeriod(raw string) (UsagePeriod, bool) {
	switch raw {
	case "":
		return UsagePeriodMonth, true
	case string(UsagePeriodDay), string(UsagePeriodWeek), string(UsagePeriodMonth), string(UsagePeriodAll):
		return UsagePeriod(raw), true
	default:
		return "", false
	}
}

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

// ParseUsageDimension validates a raw dimension string and returns the typed
// value. An empty string defaults to UsageDimensionAgent. Any unrecognized
// value returns ok=false so callers can reject it.
func ParseUsageDimension(raw string) (UsageDimension, bool) {
	switch raw {
	case "":
		return UsageDimensionAgent, true
	case string(UsageDimensionAgent), string(UsageDimensionModel), string(UsageDimensionSession):
		return UsageDimension(raw), true
	default:
		return "", false
	}
}

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
	// On entries written via the provider input/output split (pkg/session/
	// entry_stats.go's accumulateEntryStats), this is an ADDITIVE component
	// of Total alongside In and Out — Total == In + Out + CacheRead +
	// CacheWrite. On legacy pre-split entries Out instead carries the WHOLE
	// turn total (cache included), so summing In+Out+CacheRead+CacheWrite
	// for those can exceed Total. See Total's comment below — always read
	// Total directly rather than deriving it from these components.
	CacheRead int
	// CacheWrite is tokens written into a new cache entry (Anthropic only).
	// See CacheRead's comment for its relationship to Total.
	CacheWrite int
	// Total is the authoritative recorded token total for this bucket
	// (SessionStats.TokensTotal for agent/session dimensions, ModelTokens.Total
	// for the model dimension), accumulated independently of In/Out/CacheRead/
	// CacheWrite and NEVER derived from them. Whether Total reconciles exactly
	// with In+Out+CacheRead+CacheWrite depends on which write-path convention
	// produced the underlying entry (see CacheRead's comment) — Total itself
	// is correct either way; the components are the ones that vary.
	Total int
}

// UsageOptions controls the behavior of AggregateUsage.
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
	// Total is the grand total across all included sessions. The grand
	// Total.Total always reconciles with the sum of the buckets' Total across
	// every dimension. The In/Out/CacheRead/CacheWrite sub-fields reconcile
	// exactly for the agent and session dimensions; for the model dimension only
	// Total reconciles (per-model In/Out are not recorded by the write path, so
	// the unmodeled remainder is folded into a "(unknown)" bucket carrying Total
	// only).
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
		// Monday-anchored week (Monday 00:00 UTC start, +7d). Not ISO week
		// numbering — just the current week's Monday as the window start.
		weekday := int(now.Weekday()) // Sunday=0 … Saturday=6
		if weekday == 0 {
			weekday = 7 // treat Sunday as day 7 so Monday=1
		}
		start = time.Date(now.Year(), now.Month(), now.Day()-weekday+1, 0, 0, 0, 0, time.UTC)
		end = start.Add(7 * 24 * time.Hour)
	case UsagePeriodAll:
		// Zero start signals "no time filter"; PeriodEnd == now.
		end = now
	default:
		// UsagePeriodMonth and any unexpected value fall back to the current
		// calendar month (mirrors the empty-period default above), so an
		// invalid period can never silently produce a zero [start,end) window.
		start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		end = start.AddDate(0, 1, 0)
	}
	return start, end
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
	// Enforce the documented UTC precondition defensively so a caller passing
	// local time can't silently shift every period boundary.
	opts.Now = opts.Now.UTC()

	start, end := periodBounds(opts.Period, opts.Now)

	report := UsageReport{
		PeriodStart: start,
		PeriodEnd:   end,
	}

	// accumByKey holds running totals keyed by the grouping key.
	//
	// total is the AUTHORITATIVE recorded token total (SessionStats.TokensTotal
	// or ModelTokens.Total), accumulated independently of in/out/cache. We never
	// recompute Total from in+out+cache. Entries written via the provider
	// input/output split (pkg/session/entry_stats.go's accumulateEntryStats)
	// DO reconcile additively — total == in + out + cache_read + cache_write —
	// but legacy pre-split entries do not: their whole turn count (uncached
	// input + cache_read + cache_write + completion) was added to out, and the
	// cache split was ALSO tracked additionally on top of that, so naively
	// summing in+out+cache for a legacy entry over-counts relative to total.
	// Reading total directly, as this aggregator does, is correct for both
	// conventions at once. See pkg/session/entry_stats.go's accumulateEntryStats.
	type accumEntry struct {
		label      string
		in         int
		out        int
		cacheRead  int
		cacheWrite int
		total      int
	}
	accumByKey := make(map[string]*accumEntry)

	// addToKey upserts token counts into accumByKey under key. total is the
	// authoritative recorded total (NOT derived from in/out/cache).
	addToKey := func(key, label string, in, out, cr, cw, total int) {
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
		e.total += total
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
			addToKey(agentID, label, s.TokensIn, s.TokensOut, s.TokensCacheRead, s.TokensCacheWrite, s.TokensTotal)

		case UsageDimensionModel:
			// ByModel now records In/Out per model too (the provider
			// input/output split, pkg/session/entry_stats.go's
			// accumulateEntryStats), alongside CacheRead/CacheWrite/Total —
			// In/Out are 0 only for entries written before that split existed
			// or for a provider that never reports one (see ModelTokens.In's
			// doc comment). We still key the bucket Total off mt.Total (the
			// authoritative per-model token count), never a derived sum, since
			// that stays correct regardless of which write-path convention
			// produced a given entry. Any portion
			// of the session total NOT attributed to a model (input-side tokens,
			// or assistant turns with an empty Model) is attributed to "(unknown)"
			// so the model dimension's bucket-Total sum always reconciles exactly
			// with report.Total.Total.
			const unknownKey = "(unknown)"
			modeled := 0
			for model, mt := range s.ByModel {
				key := model
				if key == "" {
					key = unknownKey
				}
				addToKey(key, key, mt.In, mt.Out, mt.CacheRead, mt.CacheWrite, mt.Total)
				modeled += mt.Total
			}
			if rem := s.TokensTotal - modeled; rem > 0 {
				// Unmodeled remainder (typically session input tokens). Carries
				// Total only; In/Out/cache stay 0 (informational fields).
				addToKey(unknownKey, unknownKey, 0, 0, 0, 0, rem)
			}

		case UsageDimensionSession:
			label := sm.Title
			if label == "" {
				label = sm.ID
			}
			addToKey(sm.ID, label, s.TokensIn, s.TokensOut, s.TokensCacheRead, s.TokensCacheWrite, s.TokensTotal)
		}
	}

	// Build and sort the Buckets slice.
	buckets := make([]UsageBucket, 0, len(accumByKey))
	for key, e := range accumByKey {
		label := e.label
		if label == "" {
			label = key
		}
		buckets = append(buckets, UsageBucket{
			Key:        key,
			Label:      label,
			In:         e.in,
			Out:        e.out,
			CacheRead:  e.cacheRead,
			CacheWrite: e.cacheWrite,
			Total:      e.total,
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
