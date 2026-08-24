// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-068 T068-08 — provider dependents computation, `backs_default`, and
// the `Agent.needs_model` derivation.
//
// Spec: docs/internal/specs/adr-068-providers-ux-spec.md FR-012 (dependents
// definition, MAJ-010), FR-014 (`needs_model`), Data Constraints ("Dependents
// definition"), Dataset "Dependents computation" rows 1-12.
//
// "Dependent" covers every reference that would stop resolving if the
// provider were removed: an agent's primary model (explicit provider, or a
// bare slug that exact-matches one of the provider's rows), an agent's
// fallback_models entries, agents whose slug resolves to the provider only
// through the passthrough rung (config.ResolveSlugProvider rule 3 —
// openrouter/vivgrid), and the non-agent settings references
// (agents.defaults.image_model, .recap_model / .recap_fallback_models,
// voice.model_name, plus the model_fallbacks / image_model_fallbacks chains,
// which would equally stop resolving — MAJ-010's "every reference" clause).
// agents.defaults.default_model is NOT a dependents row: the role enum has no
// value for it; it is carried by the dedicated `backs_default` boolean
// (providerBacksDefault), which gates DELETE behind an inline new_default.
//
// The computation is advisory on GET /providers; T068-09's DELETE handler
// recomputes it under configMu and its response is authoritative (MAJ-018).

package gateway

import (
	"sort"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// providerConfigured reports whether an operator-configured provider row with
// this identity exists — the same predicate the GET /providers list uses to
// decide what is a row at all (resolution #16).
//
// Since ADR-067 FR-011 every row carries a provider id, seed templates
// included, so identity alone no longer separates the two: the test is
// isSeedTemplateRow (no credential, no endpoint, no model list, no PUT stamp,
// no auth method).
func providerConfigured(cfg *config.Config, providerID string) bool {
	if cfg == nil || providerID == "" {
		return false
	}
	for _, m := range cfg.Providers {
		if m == nil || isSeedTemplateRow(m) {
			continue
		}
		if strings.TrimSpace(m.Provider) == providerID {
			return true
		}
	}
	return false
}

// agentNeedsModel derives `Agent.needs_model` (ADR-068 FR-014): true when the
// agent's effective primary model is empty, or the provider that primary
// would route through is not configured.
//
// "Effective primary" is the agent's own primary (model, provider) pair when
// the agent has one, else the global agents.defaults.default_model pair —
// mirroring what the list/get handlers already display as the agent's model.
// A provider-less slug is resolved through config.ResolveSlugProvider (the
// same rungs the turn-time resolver applies); a slug nothing configured can
// serve is "provider not configured" too. Precedence with ADR-067's
// degraded_reason (needs_provider wins in copy) is the SPA's concern
// (resolution #5) — both flags may be true on the wire.
func agentNeedsModel(cfg *config.Config, ac *config.AgentConfig) bool {
	if cfg == nil {
		return true
	}
	model, provider := "", ""
	if ac != nil && ac.Model != nil {
		model = strings.TrimSpace(ac.Model.Primary)
		provider = strings.TrimSpace(ac.Model.Provider)
	}
	if model == "" {
		model = strings.TrimSpace(cfg.Agents.Defaults.DefaultModel.Model)
		provider = strings.TrimSpace(cfg.Agents.Defaults.DefaultModel.Provider)
	}
	if model == "" {
		return true
	}
	if provider == "" {
		provider, _ = config.ResolveSlugProvider(cfg, model)
	}
	if provider == "" {
		return true
	}
	return !providerConfigured(cfg, provider)
}

// providerBacksDefault reports whether agents.defaults.default_model names
// this provider (ADR-068 FR-012; Dataset "Dependents computation" row 12).
// The pair is exact (T068-07); a legacy provider-less default slug is
// resolved through the same rungs for robustness.
func providerBacksDefault(cfg *config.Config, providerID string) bool {
	if cfg == nil || strings.TrimSpace(providerID) == "" {
		return false
	}
	dm := cfg.Agents.Defaults.DefaultModel
	if p := strings.TrimSpace(dm.Provider); p != "" {
		return p == providerID
	}
	if m := strings.TrimSpace(dm.Model); m != "" {
		p, _ := config.ResolveSlugProvider(cfg, m)
		return p == providerID
	}
	return false
}

// dependentRoleRank orders roles for per-agent de-duplication (Dataset row 3:
// one agent referencing the provider as both primary and fallback is listed
// once, role primary). Explicit primary outranks a passthrough-resolved
// primary, which outranks a fallback reference.
func dependentRoleRank(r gen.ProviderDependentRole) int {
	switch r {
	case gen.ProviderDependentRolePrimary:
		return 3
	case gen.ProviderDependentRolePassthrough:
		return 2
	default: // fallback
		return 1
	}
}

// computeProviderDependents lists every reference that would stop resolving
// if providerID were removed (ADR-068 FR-012 / MAJ-010). Always returns a
// non-nil slice (Provider.yaml requires `dependents: array`), sorted by
// display name then id (Dataset row 6: "names sorted").
func computeProviderDependents(cfg *config.Config, providerID string) []gen.ProviderDependent {
	deps := []gen.ProviderDependent{}
	providerID = strings.TrimSpace(providerID)
	if cfg == nil || providerID == "" {
		return deps
	}

	// --- Agent references, de-duplicated per agent (Dataset row 3). ---
	best := make(map[string]gen.ProviderDependent)
	consider := func(id, name string, role gen.ProviderDependentRole) {
		if cur, ok := best[id]; ok && dependentRoleRank(cur.Role) >= dependentRoleRank(role) {
			return
		}
		best[id] = gen.ProviderDependent{Id: id, Name: name, Role: role}
	}
	// resolveSlugRole maps a provider-less slug to (matches this provider?,
	// role): an exact row match (rung 1/2) keeps the site's own role; a
	// rule-3 passthrough resolution is role passthrough (Dataset rows 7, 8).
	resolveSlugRole := func(slug string, siteRole gen.ProviderDependentRole) (bool, gen.ProviderDependentRole) {
		resolved, viaPassthrough := config.ResolveSlugProvider(cfg, slug)
		if resolved != providerID {
			return false, siteRole
		}
		if viaPassthrough {
			return true, gen.ProviderDependentRolePassthrough
		}
		return true, siteRole
	}

	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		name := ac.Name
		if name == "" {
			name = ac.ID
		}
		// Primary site. A locked core agent is listed like any other — core
		// status does not exempt it (Dataset row 5).
		if ac.Model != nil && strings.TrimSpace(ac.Model.Primary) != "" {
			slug := strings.TrimSpace(ac.Model.Primary)
			switch prov := strings.TrimSpace(ac.Model.Provider); {
			case prov == providerID:
				consider(ac.ID, name, gen.ProviderDependentRolePrimary)
			case prov == "":
				if hit, role := resolveSlugRole(slug, gen.ProviderDependentRolePrimary); hit {
					consider(ac.ID, name, role)
				}
			}
		}
		// Fallback sites (BDD "Fallback references are removed and listed").
		for _, fb := range ac.FallbackModels {
			slug := strings.TrimSpace(fb.Model)
			if slug == "" {
				continue
			}
			switch prov := strings.TrimSpace(fb.Provider); {
			case prov == providerID:
				consider(ac.ID, name, gen.ProviderDependentRoleFallback)
			case prov == "":
				if hit, role := resolveSlugRole(slug, gen.ProviderDependentRoleFallback); hit {
					consider(ac.ID, name, role)
				}
			}
		}
	}
	for _, d := range best {
		deps = append(deps, d)
	}

	// --- Non-agent settings references (id = settings key). These keep their
	// semantic role regardless of how the slug resolved (BDD "Passthrough-
	// resolved agents are dependents": the recap fallback is role recap). ---
	slugHits := func(slugs ...string) bool {
		for _, s := range slugs {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if resolved, _ := config.ResolveSlugProvider(cfg, s); resolved == providerID {
				return true
			}
		}
		return false
	}
	d := cfg.Agents.Defaults
	if slugHits(d.ModelFallbacks...) {
		deps = append(deps, gen.ProviderDependent{
			Id: "agents.defaults.model_fallbacks", Name: "Default model fallbacks",
			Role: gen.ProviderDependentRoleFallback,
		})
	}
	if slugHits(d.ImageModel) {
		deps = append(deps, gen.ProviderDependent{
			Id: "agents.defaults.image_model", Name: "Image model",
			Role: gen.ProviderDependentRoleImage,
		})
	}
	if slugHits(d.ImageModelFallbacks...) {
		deps = append(deps, gen.ProviderDependent{
			Id: "agents.defaults.image_model_fallbacks", Name: "Image model fallbacks",
			Role: gen.ProviderDependentRoleImage,
		})
	}
	if slugHits(d.RecapModel) {
		deps = append(deps, gen.ProviderDependent{
			Id: "agents.defaults.recap_model", Name: "Recap model",
			Role: gen.ProviderDependentRoleRecap,
		})
	}
	recapFallbackHit := false
	for _, fb := range d.RecapFallbackModels {
		if strings.TrimSpace(fb.Provider) == providerID ||
			(strings.TrimSpace(fb.Provider) == "" && slugHits(fb.Model)) {
			recapFallbackHit = true
			break
		}
	}
	if recapFallbackHit {
		deps = append(deps, gen.ProviderDependent{
			Id: "agents.defaults.recap_fallback_models", Name: "Recap fallback models",
			Role: gen.ProviderDependentRoleRecap,
		})
	}
	if slugHits(cfg.Voice.ModelName) {
		deps = append(deps, gen.ProviderDependent{
			Id: "voice.model_name", Name: "Voice model",
			Role: gen.ProviderDependentRoleVoice,
		})
	}

	sort.Slice(deps, func(i, j int) bool {
		if deps[i].Name != deps[j].Name {
			return deps[i].Name < deps[j].Name
		}
		return deps[i].Id < deps[j].Id
	})
	return deps
}
