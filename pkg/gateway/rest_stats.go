//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"net/http"
	"sort"
	"time"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/session"
)

// byModelCell is the per-model token breakdown cell used in both the per-agent
// and cross-agent by_model maps. The json tags are byte-identical to the inline
// element shapes generated for AgentTokenEntry.ByModel and
// TokenUsageSummary.ByModel in pkg/api/generated/openapi_types.gen.go.
// Canonical schema: contracts/components/schemas/ModelTokens.yaml.
type byModelCell struct { // not-wire-format: mirrors the oapi-codegen-inlined by_model element shared by AgentTokenEntry and TokenUsageSummary; canonical schema ModelTokens.yaml — field order and tags must stay byte-identical to the generated inline shape
	CacheRead  *int `json:"cache_read,omitempty"`
	CacheWrite *int `json:"cache_write,omitempty"`
	In         *int `json:"in,omitempty"`
	Out        *int `json:"out,omitempty"`
	Total      int  `json:"total"`
}

// nonZeroPtr returns a pointer to v when v != 0, or nil when v == 0.
// Used to emit optional integer fields only when they carry a non-zero value.
func nonZeroPtr(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// toByModelCell converts a session.ModelTokens value to a byModelCell,
// omitting zero-valued optional fields.
func toByModelCell(mt session.ModelTokens) byModelCell {
	return byModelCell{
		CacheRead:  nonZeroPtr(mt.CacheRead),
		CacheWrite: nonZeroPtr(mt.CacheWrite),
		In:         nonZeroPtr(mt.In),
		Out:        nonZeroPtr(mt.Out),
		Total:      mt.Total,
	}
}

// addModelTokens accumulates src into dst field-by-field.
func addModelTokens(dst *session.ModelTokens, src session.ModelTokens) {
	dst.In += src.In
	dst.Out += src.Out
	dst.CacheRead += src.CacheRead
	dst.CacheWrite += src.CacheWrite
	dst.Total += src.Total
}

// HandleTokenStats handles GET /api/v1/stats/tokens.
//
// It returns per-agent token usage aggregated from SessionMeta.Stats across all
// sessions using session.AggregateUsage. The response shape matches
// gen.TokenUsageSummary (contracts/components/schemas/TokenUsageSummary.yaml).
//
// The ?period query parameter accepts "day", "week", "month" (default), or
// "all". Unrecognized values are rejected with 400.
//
// Token attribution: tokens are charged to sm.ActiveAgentID (the most-recent
// agent active in the session). For sessions that pre-date the multi-agent
// model, PostLoad backfills ActiveAgentID from the legacy single AgentID.
// Attributing to each sm.AgentIDs entry would double-count on handoffs.
//
// subagent_3p (external CLI workers) are excluded — they run on a separate
// engine and their tokens are not tracked through Omnipus's provider layer.
func (a *restAPI) HandleTokenStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Validate and normalise the period query parameter via the single source
	// of truth (session.ParseUsagePeriod).  Empty → month; unrecognized → 400.
	period, ok := session.ParseUsagePeriod(r.URL.Query().Get("period"))
	if !ok {
		jsonErr(w, http.StatusBadRequest, "period must be one of: day, week, month, all")
		return
	}

	// ListAllSessions merges the shared store (new sessions) with all per-agent
	// legacy stores, deduplicates, and returns a slice of *UnifiedMeta.
	allSessions, errs := a.agentLoop.ListAllSessions()
	for _, e := range errs {
		slog.Warn("rest: token stats: partial session list error", "error", e)
	}

	registry := a.agentLoop.GetRegistry()

	// Build NameResolver (agent display name from registry) and Exclude
	// predicate (skip subagent_3p — external CLI workers).
	nameResolver := func(agentID string) string {
		name, _ := registry.GetAgentName(agentID)
		return name
	}
	cfg := a.agentLoop.GetConfig()
	excludeFn := func(agentID string) bool {
		return cfg.IsExternalCLIWorkerID(agentID)
	}

	// Aggregate by-agent dimension for the response.
	report := session.AggregateUsage(allSessions, session.UsageOptions{
		Period:       period,
		Now:          time.Now().UTC(),
		Dimension:    session.UsageDimensionAgent,
		NameResolver: nameResolver,
		Exclude:      excludeFn,
	})

	// Build per-agent entries from report buckets.
	// We also need per-agent by-model breakdowns; compute them with a second
	// model-keyed pass limited to each agent's sessions.
	//
	// agentModelMap[agentID][model] = accumulated ModelTokens.
	agentModelMap := make(map[string]map[string]session.ModelTokens)
	for _, sm := range allSessions {
		sm.PostLoad()
		agentID := sm.ActiveAgentID
		if agentID == "" && len(sm.AgentIDs) > 0 {
			agentID = sm.AgentIDs[0]
		}
		if agentID == "" {
			continue
		}
		if excludeFn(agentID) {
			continue
		}
		// Apply same period filter the aggregator uses.
		if !report.PeriodStart.IsZero() {
			if sm.UpdatedAt.Before(report.PeriodStart) || !sm.UpdatedAt.Before(report.PeriodEnd) {
				continue
			}
		}
		for model, mt := range sm.Stats.ByModel {
			if _, ok := agentModelMap[agentID]; !ok {
				agentModelMap[agentID] = make(map[string]session.ModelTokens)
			}
			existing := agentModelMap[agentID][model]
			addModelTokens(&existing, mt)
			agentModelMap[agentID][model] = existing
		}
	}

	// Build cross-agent by_model totals.
	crossModelMap := make(map[string]session.ModelTokens)
	for _, perAgent := range agentModelMap {
		for model, mt := range perAgent {
			existing := crossModelMap[model]
			addModelTokens(&existing, mt)
			crossModelMap[model] = existing
		}
	}

	// Build the per-agent Agents slice.  We use report.Buckets (already sorted
	// and deduplicated by AggregateUsage) as the authoritative source.
	//
	// gen.AgentTokenEntry.ByModel uses an anonymous inline struct type generated
	// by oapi-codegen; our named byModelCell is byte-identical in JSON tags but
	// a distinct Go type.  We embed gen.AgentTokenEntry inside agentEntryWithByModel
	// and shadow ByModel with interface{} so json.Marshal sees byModelCell (which
	// serialises identically) while the outer struct carries all other fields from
	// the contract type without repetition.  The shadowing rule in encoding/json:
	// a promoted field is overridden by a field at the outer depth with the same name.
	type agentEntryWithByModel struct { // not-wire-format: embeds gen.AgentTokenEntry, shadows ByModel with typed byModelCell map — JSON output is contract-identical to AgentTokenEntry
		gen.AgentTokenEntry
		ByModel interface{} `json:"by_model,omitempty"`
	}
	enriched := make([]agentEntryWithByModel, 0, len(report.Buckets))
	for _, b := range report.Buckets {
		e := agentEntryWithByModel{
			AgentTokenEntry: gen.AgentTokenEntry{
				AgentId:          b.Key,
				AgentName:        b.Label,
				TokensIn:         b.In,
				TokensOut:        b.Out,
				TokensTotal:      b.Total,
				TokensCacheRead:  nonZeroPtr(b.CacheRead),
				TokensCacheWrite: nonZeroPtr(b.CacheWrite),
			},
		}
		if modelMap, hasModel := agentModelMap[b.Key]; hasModel && len(modelMap) > 0 {
			byModel := make(map[string]byModelCell, len(modelMap))
			for model, mt := range modelMap {
				byModel[model] = toByModelCell(mt)
			}
			e.ByModel = byModel
		}
		enriched = append(enriched, e)
	}

	// Sort entries by AgentId for stable output (matches prior behaviour).
	sort.Slice(enriched, func(i, j int) bool { return enriched[i].AgentId < enriched[j].AgentId })

	// Build cross-agent by_model for the top-level response field.
	var crossModelOut interface{}
	if len(crossModelMap) > 0 {
		m := make(map[string]byModelCell, len(crossModelMap))
		for model, mt := range crossModelMap {
			m[model] = toByModelCell(mt)
		}
		crossModelOut = m
	}

	// Grand-total cache fields (optional — omit when zero for wire-compat with
	// receivers that don't yet read cache fields).

	// gen.TokenUsageSummary.Agents is an inline anonymous struct generated by
	// oapi-codegen (it inlines $ref'd object items rather than aliasing them to
	// the named AgentTokenEntry component). This thin wrapper holds the named
	// gen.AgentTokenEntry items and reproduces the TokenUsageSummary JSON shape;
	// the AgentTokenEntry element type is the contract source of truth that
	// make verify-contracts tracks for drift.
	type tokenUsageResp struct { // not-wire-format: wrapper over gen.AgentTokenEntry items, mirrors TokenUsageSummary with cache+by_model extensions
		Agents           []agentEntryWithByModel `json:"agents"`
		ByModel          interface{}             `json:"by_model,omitempty"`
		PeriodEnd        time.Time               `json:"period_end"`
		PeriodStart      time.Time               `json:"period_start"`
		TokensCacheRead  *int                    `json:"tokens_cache_read,omitempty"`
		TokensCacheWrite *int                    `json:"tokens_cache_write,omitempty"`
	}

	jsonOK(w, tokenUsageResp{
		Agents:           enriched,
		ByModel:          crossModelOut,
		PeriodEnd:        report.PeriodEnd,
		PeriodStart:      report.PeriodStart,
		TokensCacheRead:  nonZeroPtr(report.Total.CacheRead),
		TokensCacheWrite: nonZeroPtr(report.Total.CacheWrite),
	})
}
