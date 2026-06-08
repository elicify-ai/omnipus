//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"net/http"
	"time"
)

// HandleTokenStats handles GET /api/v1/stats/tokens.
// It returns per-agent token usage aggregated from SessionMeta.Stats across
// all sessions. The ?period query parameter must be "month" or absent (defaults
// to "month"). Unrecognised period values are rejected with 400.
//
// Token attribution: tokens are charged to sm.ActiveAgentID (the most-recent
// agent active in the session). For sessions that pre-date the multi-agent
// model, PostLoad backfills ActiveAgentID from the legacy single AgentID.
// Attributing to each sm.AgentIDs entry would double-count on handoffs.
func (a *restAPI) HandleTokenStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Validate and normalise the period query parameter.
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "month"
	}
	if period != "month" {
		jsonErr(w, http.StatusBadRequest, "period must be 'month'")
		return
	}

	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)

	// agentAccum accumulates per-agent token counts during session traversal.
	type agentAccum struct { // not-wire-format: internal accumulator only, never serialised over the wire
		name         string
		inputTokens  int
		outputTokens int
	}
	byAgent := make(map[string]*agentAccum)

	// ListAllSessions merges the shared store (new sessions) with all per-agent
	// legacy stores, deduplicates, and returns a slice of *UnifiedMeta.
	allSessions, errs := a.agentLoop.ListAllSessions()
	for _, e := range errs {
		slog.Warn("rest: token stats: partial session list error", "error", e)
	}

	registry := a.agentLoop.GetRegistry()

	for _, sm := range allSessions {
		// Apply month filter: keep sessions whose UpdatedAt falls within [periodStart, periodEnd).
		if sm.UpdatedAt.Before(periodStart) || !sm.UpdatedAt.Before(periodEnd) {
			continue
		}

		// PostLoad backfills ActiveAgentID from legacy AgentID for sessions that
		// pre-date the multi-agent model.
		sm.PostLoad()

		// Attribute tokens to ActiveAgentID only (the last-active agent in the
		// session). Using AgentIDs would double-count on agent handoffs — each
		// entry in that slice would receive the full session token total.
		agentID := sm.ActiveAgentID
		if agentID == "" && len(sm.AgentIDs) > 0 {
			agentID = sm.AgentIDs[0] // fallback to first agent
		}
		if agentID == "" {
			continue
		}

		acc, ok := byAgent[agentID]
		if !ok {
			name, _ := registry.GetAgentName(agentID)
			if name == "" {
				name = agentID
			}
			acc = &agentAccum{name: name}
			byAgent[agentID] = acc
		}
		acc.inputTokens += sm.Stats.TokensIn
		acc.outputTokens += sm.Stats.TokensOut
	}

	// Build the wire response using local structs whose JSON tags match the
	// generated gen.TokenUsageSummary shape exactly. The generated type uses
	// an inline anonymous struct for the Agents element, which cannot be
	// constructed directly from outside the package.
	type agentEntry struct { // not-wire-format: local shim matching anonymous generated struct item in TokenUsageSummary.Agents
		AgentId     string `json:"agent_id"`
		AgentName   string `json:"agent_name"`
		TokensIn    int    `json:"tokens_in"`
		TokensOut   int    `json:"tokens_out"`
		TokensTotal int    `json:"tokens_total"`
	}
	type tokenUsageResp struct { // not-wire-format: wrapper shim for anonymous inline generated struct, mirrors TokenUsageSummary
		Agents      []agentEntry `json:"agents"`
		PeriodEnd   time.Time    `json:"period_end"`
		PeriodStart time.Time    `json:"period_start"`
	}

	entries := make([]agentEntry, 0, len(byAgent))
	for agentID, acc := range byAgent {
		entries = append(entries, agentEntry{
			AgentId:     agentID,
			AgentName:   acc.name,
			TokensIn:    acc.inputTokens,
			TokensOut:   acc.outputTokens,
			TokensTotal: acc.inputTokens + acc.outputTokens,
		})
	}

	// Insertion sort for a stable, deterministic response order by agent_id.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].AgentId < entries[j-1].AgentId; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}

	jsonOK(w, tokenUsageResp{
		Agents:      entries,
		PeriodEnd:   periodEnd,
		PeriodStart: periodStart,
	})
}
