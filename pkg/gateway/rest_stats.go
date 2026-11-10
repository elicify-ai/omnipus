// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"net/http"
	"sort"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func nonZeroPtr(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

func toByModelCell(mt session.ModelTokens) gen.ModelTokens {
	return gen.ModelTokens{
		CacheHitRatio: session.CacheHitRatio(mt.In, mt.CacheRead, mt.CacheWrite),
		CacheRead:     nonZeroPtr(mt.CacheRead), CacheWrite: nonZeroPtr(mt.CacheWrite),
		In: nonZeroPtr(mt.In), Out: nonZeroPtr(mt.Out), Total: mt.Total,
	}
}

func initGeneratedSlice[T any](dst *[]T) { *dst = make([]T, 0) }

// initOptionalMap allocates the exact generated anonymous map type inferred
// from its destination. No copied wire struct or shadow response is needed.
func initOptionalMap[K comparable, V any](dst **map[K]V) {
	m := make(map[K]V)
	*dst = &m
}

// HandleTokenStats serves the existing usage route using generated types only.
func (a *restAPI) HandleTokenStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	period, ok := session.ParseUsagePeriod(r.URL.Query().Get("period"))
	if !ok {
		jsonErr(w, http.StatusBadRequest, "period must be one of: day, week, month, all")
		return
	}
	page, errs := a.agentLoop.ListAllSessions(0, 0, "", true)
	for _, err := range errs {
		slog.Warn("rest: token stats: partial session list error", "error", err)
	}
	registry := a.agentLoop.GetRegistry()
	cfg := a.agentLoop.GetConfig()
	exclude := func(id string) bool { return cfg.IsExternalCLIWorkerID(id) }
	opts := session.UsageOptions{
		Period: period, Now: time.Now().UTC(), Dimension: session.UsageDimensionAgent,
		NameResolver: func(id string) string { name, _ := registry.GetAgentName(id); return name },
		Exclude:      exclude,
	}
	report := session.AggregateUsage(page.Sessions, opts)
	out := gen.TokenUsageSummary{
		PeriodStart: report.PeriodStart, PeriodEnd: report.PeriodEnd,
		TokensCacheRead: nonZeroPtr(report.Total.CacheRead), TokensCacheWrite: nonZeroPtr(report.Total.CacheWrite),
		UnpricedTokens: nonZeroPtr(report.UnpricedTokens), SubscriptionTokens: nonZeroPtr(report.SubscriptionTokens),
	}
	initGeneratedSlice(&out.Agents)
	for _, bucket := range report.Buckets {
		entry := gen.AgentTokenEntry{
			AgentId: bucket.Key, AgentName: bucket.Label,
			TokensIn: bucket.In, TokensOut: bucket.Out, TokensTotal: bucket.Total,
			TokensCacheRead: nonZeroPtr(bucket.CacheRead), TokensCacheWrite: nonZeroPtr(bucket.CacheWrite),
			CacheHitRatio: bucket.CacheHitRatio,
		}
		opts.AgentID, opts.Dimension = bucket.Key, session.UsageDimensionModel
		models := session.AggregateUsage(page.Sessions, opts)
		if len(models.Buckets) > 0 {
			initOptionalMap(&entry.ByModel)
			for _, m := range models.Buckets {
				// Preserve the handler's existing by_model rule: only stored
				// model ids, not the aggregator's unmodeled remainder.
				if m.Key == "(unknown)" {
					continue
				}
				(*entry.ByModel)[m.Key] = toByModelCell(session.ModelTokens{In: m.In, Out: m.Out, CacheRead: m.CacheRead, CacheWrite: m.CacheWrite, Total: m.Total})
			}
		}
		out.Agents = append(out.Agents, entry)
	}
	sort.Slice(out.Agents, func(i, j int) bool { return out.Agents[i].AgentId < out.Agents[j].AgentId })
	opts.AgentID, opts.Dimension = "", session.UsageDimensionModel
	models := session.AggregateUsage(page.Sessions, opts)
	if len(models.Buckets) > 0 {
		initOptionalMap(&out.ByModel)
		for _, m := range models.Buckets {
			if m.Key == "(unknown)" {
				continue
			}
			(*out.ByModel)[m.Key] = toByModelCell(session.ModelTokens{In: m.In, Out: m.Out, CacheRead: m.CacheRead, CacheWrite: m.CacheWrite, Total: m.Total})
		}
	}
	if len(report.ByProvider) > 0 {
		initOptionalMap(&out.ByProvider)
		for id, cell := range report.ByProvider {
			(*out.ByProvider)[id] = toByModelCell(session.ModelTokens{In: cell.In, Out: cell.Out, CacheRead: cell.CacheRead, CacheWrite: cell.CacheWrite, Total: cell.Total})
		}
	}
	if len(errs) > 0 {
		partial := true
		out.Partial = &partial
		out.PartialErrorCount = nonZeroPtr(len(errs))
	}
	jsonOK(w, out)
}
