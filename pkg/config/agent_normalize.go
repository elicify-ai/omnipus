// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// NormalizeAgentRoster applies loadConfigInternal's agent-roster
// normalization pass to cfg.Agents.List:
//
//  1. FallbackModel provider resolution (NormalizeFallbacks, FR-006/FR-007) —
//     resolves a legacy string/empty-provider fallback entry against
//     cfg.Providers using the same passthrough lookup the chat-side model
//     resolver uses.
//
// A second pass — the primary-model "provider/model" prefix split — used to
// run here too (migrateAgentPrimaryProvider, O3). It is DELETED (C1 fix,
// ADR-067 FR-034): it silently rerouted any bare primary slug whose leading
// segment collided with a live provider id to that provider, at a real,
// measured wrong-vendor rate — see config.go's C1 fix comment beside the
// deleted knownProviderProtocols table for the full rationale. A `/` inside
// Model.Primary is DATA now, never split; an entity-loaded agent with a
// combined legacy slug and an empty Provider keeps that slug verbatim and
// legitimately trips the pre-turn needs_provider gate (ADR-067 T067-09).
//
// loadConfigInternal (config.go, immediately after loadConfig(data)) already
// runs NormalizeFallbacks against whatever cfg.Agents.List holds AT LOAD
// TIME — but config.json's agents.list is unconditionally stripped to empty
// before that point on every load (legacy_agents_list.go, ADR-054): agents
// are per-entity records under entities/agents/<id>.json now, never
// config.json entries. Every call site that REPOPULATES cfg.Agents.List from
// the entity store AFTER LoadConfig*/LoadConfigWithStore* returns
// (pkg/gateway's populateAgentsListFromEntityStore[Strict] bridge, and the
// equivalent loaders in cmd/omnipus) is therefore a second, LATER mutation
// point that loadConfigInternal's own normalization loop never sees — an
// entity record persisted with a legacy no-provider fallback entry (e.g.
// pkg/gateway/rest.go's configureChannel-adjacent writers, which can write
// config.FallbackModel{Provider: ""} relying on "the next load" to resolve
// it) stayed silently unnormalized in every in-memory roster this process
// ever built, not just until the next restart — the entity store never runs
// loadConfigInternal's loop at all.
//
// This is that second normalization point. Call it once, immediately after
// cfg.Agents.List has been (re)populated with the real roster, and before
// anything reads FallbackModels off of it (routing, provider resolution, the
// REST API's own response shaping). Idempotent — already-normalized entries
// pass through unchanged on every subsequent call, matching
// NormalizeFallbacks' own idempotence. A nil cfg is a no-op.
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
}
