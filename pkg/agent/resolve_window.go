// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"strings"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ADR-066 D2/D3 — the ONE context-window resolver.
//
// Every consumer of "how big is this model's window" — NewAgentInstance, the
// model-switch re-window, the gateway's agent projection and S68's default-
// model card — asks ResolveWindow and nothing else. There is no other source
// of a window anywhere in pkg/agent: the max_tokens×4 heuristic, the two
// flat 128k fallbacks and agents.defaults.context_window are all deleted
// (FR-004). The ladder, top rung wins:
//
//  1. per-agent override        AgentConfig.ContextWindowOverride  (only with agentID)
//  2. per-(provider, model)     ContextSettings.ModelOverrides[]
//  3. global default            ContextSettings.DefaultContextWindow
//  4. live provider query       on demand, cached 24 h (LiveLimits, live_limits.go; never blocks)
//  5. catalog                   catalog.Resolve(provider, model).Window()
//  6. floor                     cloudWindowFloor, cloud rows only, one WARN
//
// Rungs 1–3 are operator overrides and can only LOWER the window: the
// effective value is min(override, capability) where capability is the
// live-or-catalog value — or the floor when neither knows it, cloud only —
// recomputed on every resolution (FR-002). A clamp logs one WARN naming the
// agent, the override and the clamped value.
//
// D3: a `locality: local` row (ADR-067's single predicate — ollama/vllm/
// lmstudio, or a custom row at a loopback/private host) is never floored.
// With no override and no live-or-catalog value it is UNKNOWN: the instance
// is built with window 0 and runTurn refuses the turn with
// context_window_unknown until the operator sets an override (FR-007/008).
//
// Exempt: a row whose cli_kind names a subprocess driver manages its own
// context. Window 0, no source, every budget check skipped (FR-005). The
// decision is by the field, never by a provider id.
//
// D8 is NOT adopted: nothing here learns a window from provider error text.

// cloudWindowFloor is the conservative window for a cloud model nobody can
// size (catalog miss, no live value, no override). It is the only 128k
// literal in pkg/agent (SC-009). Local endpoints never see it — see D3.
const cloudWindowFloor = 128000

// WindowSource names the ladder rung that produced an effective window. The
// values are the contract's ContextWindowSource enum (operator | live |
// catalog | floor); the generated per-parent Go copies are converted at the
// gateway boundary. Empty for an exempt or unknown window.
type WindowSource string

const (
	WindowSourceOperator WindowSource = "operator"
	WindowSourceLive     WindowSource = "live"
	WindowSourceCatalog  WindowSource = "catalog"
	WindowSourceFloor    WindowSource = "floor"
)

// WindowResolution is ResolveWindow's answer.
type WindowResolution struct {
	// Window is the effective context window in tokens. 0 when Exempt or
	// Unknown.
	Window int
	// Source is the rung that produced Window; "" when Exempt or Unknown.
	Source WindowSource
	// Clamped is true when an operator override exceeded the capability and
	// Window is the capability instead (FR-002).
	Clamped bool
	// Exempt is true for a subprocess-CLI row: the provider manages its own
	// context and every budget check is skipped (FR-005).
	Exempt bool
	// Unknown is true for a local endpoint with no override and no reported
	// window: the turn must be refused with context_window_unknown (FR-008).
	Unknown bool
}

// LiveWindowLookup is rung 4: the on-demand, cached provider limits query
// (T066-10). It returns (window, true) when the provider reported one. The
// key is (provider id, base URL, model) — a catalog `api` change therefore
// yields a new key. nil means the rung is skipped.
type LiveWindowLookup func(provider, baseURL, model string) (int, bool)

var (
	// windowCatalogPtr is the catalog the resolver reads. The gateway installs
	// the served catalog at boot (SetWindowCatalog); until then, and in tests
	// that never install one, an empty catalog misses everywhere and the
	// ladder continues past rung 5.
	windowCatalogPtr atomic.Pointer[catalog.Catalog]
	// liveWindowLookupPtr holds rung 4's lookup; nil = skipped.
	liveWindowLookupPtr atomic.Pointer[LiveWindowLookup]
)

// SetWindowCatalog installs the catalog ResolveWindow resolves against. The
// gateway calls it once the served document is loaded; nil resets to an
// empty catalog (every lookup misses).
func SetWindowCatalog(c *catalog.Catalog) {
	if c == nil {
		c = catalog.New()
	}
	windowCatalogPtr.Store(c)
}

// windowCatalog returns the installed catalog, never nil.
func windowCatalog() *catalog.Catalog {
	if c := windowCatalogPtr.Load(); c != nil {
		return c
	}
	return catalog.New()
}

// SetLiveWindowLookup installs rung 4's lookup (T066-10). nil skips the rung.
func SetLiveWindowLookup(fn LiveWindowLookup) {
	if fn == nil {
		liveWindowLookupPtr.Store(nil)
		return
	}
	liveWindowLookupPtr.Store(&fn)
}

// liveWindowLookup returns the installed rung-4 lookup, or nil.
func liveWindowLookup() LiveWindowLookup {
	if p := liveWindowLookupPtr.Load(); p != nil {
		return *p
	}
	return nil
}

// windowRow is what the resolver needs to know about the provider behind a
// (provider, model) pair: whether it exists at all, how ADR-067 classifies
// it, whether it is a subprocess driver, and its base URL for the live key.
type windowRow struct {
	known    bool
	exempt   bool
	locality catalog.Locality
	baseURL  string
}

// lookupWindowRow classifies provider from the catalog first, then from the
// operator's configured providers (a custom row: not in the catalog, but
// present in cfg.Providers with a base URL — classified by ADR-067's
// DeriveLocality with custom=true). A provider in neither place is unknown:
// an override naming it is dead and ignored (US-1.AC10).
func lookupWindowRow(cfg *config.Config, provider string) windowRow {
	if p, ok := windowCatalog().Provider(provider); ok {
		return windowRow{
			known:    true,
			exempt:   p.CLIKind != "", // a cli_kind is present iff the row is a subprocess driver (X-14)
			locality: p.Locality,
			baseURL:  p.API,
		}
	}
	if cfg != nil {
		for _, mc := range cfg.Providers {
			if mc == nil || strings.TrimSpace(mc.Provider) != provider {
				continue
			}
			return windowRow{
				known:    true,
				locality: catalog.DeriveLocality(provider, "", true, strings.TrimSpace(mc.APIBase)),
				baseURL:  strings.TrimSpace(mc.APIBase),
			}
		}
	}
	return windowRow{}
}

// ResolveWindow resolves the effective context window for (provider, model)
// through the D2 ladder. agentID selects rung 1 (the per-agent override);
// pass "" to resolve without an agent — S68's default-model card and the
// catalog projection do — in which case rungs 2–6 apply.
//
// cfg may be nil (rungs 1–3 are then absent). The resolver never mutates
// cfg: pruning a dead model_overrides[] entry is the settings write's job.
func ResolveWindow(cfg *config.Config, provider, model, agentID string) WindowResolution {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	row := lookupWindowRow(cfg, provider)

	if row.exempt {
		return WindowResolution{Exempt: true}
	}

	// Capability: the live-or-catalog value. 0 = nobody knows. Rung 4's
	// Lookup never blocks: a cold cache answers (0, false) now and the live
	// value applies at the next resolution (T066-10, FR-003).
	capability, capSource := 0, WindowSource("")
	if fn := liveWindowLookup(); fn != nil && row.known {
		if w, ok := fn(provider, row.baseURL, model); ok && w > 0 {
			capability, capSource = w, WindowSourceLive
		}
	}
	if cw := windowCatalog().Resolve(provider, model).Window(); cw > 0 {
		switch {
		case capability == 0:
			capability, capSource = cw, WindowSourceCatalog
		case capability > cw:
			// E17: the live cache lives in $OMNIPUS_HOME and can be hand-
			// edited; it can only LOWER the window. A live value above the
			// catalog's capability is clamped to the catalog.
			logger.DebugCF("agent", "Live context window exceeds the catalog's; clamped to the catalog",
				map[string]any{"provider": provider, "model": model, "live": capability, "catalog": cw})
			capability, capSource = cw, WindowSourceCatalog
		}
	}
	local := row.locality == catalog.LocalityLocal

	// Operator override, first present rung wins; clamped to the capability
	// (or to the floor for a cloud row nobody can size).
	if chosen, rung, ok := operatorWindow(cfg, provider, model, agentID, row.known); ok {
		ceiling := capability
		if ceiling == 0 && !local {
			ceiling = cloudWindowFloor
		}
		if ceiling > 0 && chosen > ceiling {
			logger.WarnCF("agent", "Context window override exceeds the model's capability; clamped",
				map[string]any{
					"agent_id":       agentID,
					"provider":       provider,
					"model":          model,
					"override_rung":  rung,
					"override":       chosen,
					"capability":     ceiling,
					"clamped_window": ceiling,
				})
			return WindowResolution{Window: ceiling, Source: WindowSourceOperator, Clamped: true}
		}
		return WindowResolution{Window: chosen, Source: WindowSourceOperator}
	}

	if capability > 0 {
		return WindowResolution{Window: capability, Source: capSource}
	}

	if local {
		// D3: never guess for a local endpoint. runTurn refuses with
		// context_window_unknown until an override is set.
		return WindowResolution{Unknown: true}
	}

	logger.WarnCF("agent", "Context window unknown for cloud model; using the conservative floor",
		map[string]any{
			"agent_id": agentID,
			"provider": provider,
			"model":    model,
			"floor":    cloudWindowFloor,
		})
	return WindowResolution{Window: cloudWindowFloor, Source: WindowSourceFloor}
}

// operatorWindow walks rungs 1–3 and returns the first present override
// with the rung's name. A model_overrides[] entry is honoured only when its
// provider is known (catalog or configured) — an entry for a deleted
// provider is dead and ignored (US-1.AC10).
func operatorWindow(cfg *config.Config, provider, model, agentID string, providerKnown bool) (int, string, bool) {
	if cfg == nil {
		return 0, "", false
	}
	if agentID != "" {
		for i := range cfg.Agents.List {
			ac := &cfg.Agents.List[i]
			if ac.ID != agentID {
				continue
			}
			if ac.ContextWindowOverride != nil && *ac.ContextWindowOverride > 0 {
				return *ac.ContextWindowOverride, "agent", true
			}
			break
		}
	}
	if providerKnown {
		for _, o := range cfg.Context.ModelOverrides {
			if o.Provider == provider && o.Model == model && o.ContextWindow > 0 {
				return o.ContextWindow, "model", true
			}
		}
	}
	if cfg.Context.DefaultContextWindow != nil && *cfg.Context.DefaultContextWindow > 0 {
		return *cfg.Context.DefaultContextWindow, "global_default", true
	}
	return 0, "", false
}

// clampMaxTokensForWindow applies FR-005b (A-18): when max_tokens would
// leave the budget B ≤ 0 for window w, it is replaced by floor(w/4) and one
// WARN names the model and both values. The pinned-core term is the
// breadcrumb cap — the system prompt is not built yet at construction time,
// and the check is a floor on B, not the exact turn-time value. Exempt and
// unknown windows (w ≤ 0) are left alone: there is no budget to protect.
func clampMaxTokensForWindow(w, maxTokens int, model string) int {
	if w <= 0 || contextBudget(w, maxTokens, breadcrumbTokenCap) > 0 {
		return maxTokens
	}
	clamped := w / 4
	logger.WarnCF("agent", "max_tokens leaves no context budget for this model's window; clamped to window/4",
		map[string]any{
			"model":              model,
			"context_window":     w,
			"max_tokens":         maxTokens,
			"clamped_max_tokens": clamped,
		})
	return clamped
}
