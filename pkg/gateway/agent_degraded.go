// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// ADR-067 T067-09 — the `Agent.degraded_reason` derivation.
//
// Spec: docs/internal/specs/adr-067-registry-catalog-spec.md FR-016 (an agent
// is `needs_provider` iff its PRIMARY provider is unknown), FR-031 (it wins
// over ADR-068's `needs_model` in copy; the two fields are separate on the
// wire and both may be true), FR-036 (exact id comparison), US-6.AC2/AC6,
// Dataset DS-5 row 18, DS-8 rows 2/4/5.
//
// The state is DERIVED on every read, never stored: it is a function of the
// agent's provider binding and the catalog the gateway is currently serving,
// both of which change under the operator's hands. Deriving it here is what
// makes US-6.AC3 ("repair without restart") true for the list as well as for
// the turn — a PUT that re-points the agent at a real provider is reflected
// by the very next GET, with no reload of this file's state at all.

package gateway

import (
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// agentPrimaryProviderID returns the provider id the agent's PRIMARY model is
// pinned to, mirroring pkg/agent's resolveAgentPrimaryProvider exactly: the
// agent's own `model.provider` when it sets its own primary model, else the
// provider half of `agents.defaults.default_model`.
//
// An empty result means "no provider pinned" — resolved through the slug
// rungs at turn time, and ADR-068's `needs_model` territory. It is never
// `needs_provider`: that state is specifically "an id was named and it is not
// real".
func agentPrimaryProviderID(cfg *config.Config, ac *config.AgentConfig) string {
	if ac != nil && ac.Model != nil && strings.TrimSpace(ac.Model.Primary) != "" {
		return strings.TrimSpace(ac.Model.Provider)
	}
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Agents.Defaults.DefaultModel.Provider)
}

// agentDegradedReason derives `Agent.degraded_reason` (FR-016/FR-031):
// `needs_provider` when the agent's primary provider id is unknown against
// the served catalog, absent otherwise.
//
// It answers nil whenever no catalog document is loaded (E7) — the same guard
// GET /providers applies before it stamps a row `unknown-provider`. An absent
// catalog must never degrade every agent in the install at once.
//
// It shares ONE predicate (providers.IsUnknownProviderIDIn) with the agent
// runtime's own pre-turn gate, so the list can never report an agent healthy
// while its next turn is refused, or the reverse.
func agentDegradedReason(
	cat *catalog.Catalog,
	cfg *config.Config,
	ac *config.AgentConfig,
) *gen.AgentDegradedReason {
	id := agentPrimaryProviderID(cfg, ac)
	if !providers.IsUnknownProviderIDIn(cat, cfg, id) {
		return nil
	}
	reason := gen.NeedsProvider
	return &reason
}
