// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// NormalizeAgentRoster applies loadConfigInternal's two agent-roster
// normalization passes to cfg.Agents.List:
//
//  1. FallbackModel provider resolution (NormalizeFallbacks, FR-006/FR-007) —
//     resolves a legacy string/empty-provider fallback entry against
//     cfg.Providers using the same passthrough lookup the chat-side model
//     resolver uses.
//  2. The primary-model provider split (migrateAgentPrimaryProvider, O3) —
//     splits a combined "provider/model" primary slug (e.g.
//     "openrouter/google/gemini-2.5-flash") into Primary + Provider so
//     resolution never has to infer a provider from the slug.
//
// loadConfigInternal (config.go, immediately after loadConfig(data)) already
// runs both against whatever cfg.Agents.List holds AT LOAD TIME — but
// config.json's agents.list is unconditionally stripped to empty before that
// point on every load (legacy_agents_list.go, ADR-054): agents are per-entity
// records under entities/agents/<id>.json now, never config.json entries.
// Every call site that REPOPULATES cfg.Agents.List from the entity store
// AFTER LoadConfig*/LoadConfigWithStore* returns (pkg/gateway's
// populateAgentsListFromEntityStore[Strict] bridge, and the equivalent
// loaders in cmd/omnipus) is therefore a second, LATER mutation point that
// loadConfigInternal's own normalization loop never sees — an entity record
// persisted with a pre-split combined primary slug (e.g.
// pkg/gateway/rest.go's configureChannel-adjacent writers, which can write
// config.FallbackModel{Provider: ""} relying on "the next load" to resolve
// it) stayed silently unnormalized in every in-memory roster this process
// ever built, not just until the next restart — the entity store never runs
// loadConfigInternal's loop at all.
//
// This is that second normalization point. Call it once, immediately after
// cfg.Agents.List has been (re)populated with the real roster, and before
// anything reads Model.Provider/Primary or FallbackModels off of it (routing,
// provider resolution, the REST API's own response shaping). Idempotent —
// already-normalized entries pass through unchanged on every subsequent
// call, matching both underlying passes' own idempotence. A nil cfg is a
// no-op.
//
// migrateAgentPrimaryProvider is unexported but lives in this same package
// (config.go) — this file calls it directly rather than duplicating its
// logic, and adds no new exported surface to config.go itself.
func NormalizeAgentRoster(cfg *Config) {
	if cfg == nil {
		return
	}
	for i := range cfg.Agents.List {
		if len(cfg.Agents.List[i].FallbackModels) == 0 {
			continue
		}
		cfg.Agents.List[i].FallbackModels = NormalizeFallbacks(cfg, cfg.Agents.List[i].FallbackModels)
	}
	migrateAgentPrimaryProvider(cfg)
}
