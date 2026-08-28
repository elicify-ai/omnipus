// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// liveLimitsCacheFile is ADR-066 FR-003's cache location under $OMNIPUS_HOME.
const liveLimitsCacheFile = "cache/model_limits.json"

// configSource is the narrow slice of *agent.AgentLoop the credential
// resolver needs: the CURRENT config (providers[] and their api_key_ref),
// re-read on every call so a provider added after boot is seen without a
// restart.
type configSource interface {
	GetConfig() *config.Config
}

// newLiveLimitsForBoot builds rung 4 for the gateway (T066-10). The
// credential resolver mirrors the providers projection in rest.go: the
// first providers[] row for the id whose api_key_ref resolves — through the
// environment InjectFromConfig populated at boot, else straight from the
// store — supplies the key; no row, no ref, or an unresolvable ref means
// "" and the rung is skipped for that provider.
func newLiveLimitsForBoot(
	homePath string,
	store *credentials.Store,
	src configSource,
	onLanded func(provider, baseURL, model string, window int),
) *agent.LiveLimits {
	return agent.NewLiveLimits(agent.LiveLimitsOptions{
		CachePath:      filepath.Join(homePath, filepath.FromSlash(liveLimitsCacheFile)),
		Credential:     func(provider string) string { return providerCredential(src, store, provider) },
		OnWindowLanded: onLanded,
	})
}

// windowReloader is the slice of *agent.AgentLoop the rung-4 notification
// needs.
type windowReloader interface {
	configSource
	GetRegistry() *agent.AgentRegistry
	TriggerReload() error
}

// reloadOnLiveWindow is what newLiveLimitsForBoot's OnWindowLanded is wired
// to: a landed live window is only real once the agents that were built
// without it are rebuilt, because an AgentInstance resolves and CACHES its
// window at construction (pkg/agent/instance.go).
//
// Without this a fresh Ollama install never runs a turn: rung 4 is installed
// after NewAgentLoop, Lookup never blocks, so the first resolution that
// reaches it answers Unknown and starts a fetch — and the answer, correct and
// cached, was applied by nothing (FR-007, US-2.AC2). It is not a timer: it
// fires only as the tail of a fetch a resolution asked for.
func reloadOnLiveWindow(al windowReloader) func(string, string, string, int) {
	return func(provider, _, model string, window int) {
		if al == nil {
			return
		}
		if err := al.TriggerReload(); err != nil {
			// ErrReloadAlreadyInProgress means another landed window is
			// already rebuilding the registry — ours rides along.
			slog.Debug("gateway: live context window landed; reload not started",
				"provider", provider, "model", model, "window", window, "error", err)
			return
		}
		slog.Info("gateway: live context window landed; agents rebuilt with it",
			"provider", provider, "model", model, "window", window)
	}
}

// primeUnknownWindows asks rung 4 once, in the background, for every agent
// whose window resolved UNKNOWN at construction — a `locality: local` row the
// catalog cannot size, whose every turn is refused with
// context_window_unknown until an operator sets an override.
//
// That population is exactly the set FR-007 makes the live query mandatory
// for, and it is the only set primed: a cloud row that resolved to a catalog
// value or the floor is left alone, so this starts no upstream request the
// ladder did not already need. Each prime that lands calls back into
// reloadOnLiveWindow, which rebuilds the agent with the endpoint's own
// reported window.
func primeUnknownWindows(al windowReloader) {
	if al == nil {
		return
	}
	cfg := al.GetConfig()
	registry := al.GetRegistry()
	if cfg == nil || registry == nil {
		return
	}
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		inst, ok := registry.GetAgent(ac.ID)
		if !ok || inst == nil || !inst.WindowUnknown {
			continue
		}
		provider := agentPrimaryProviderID(cfg, ac)
		model := agentPrimaryModelID(cfg, ac)
		if provider == "" || model == "" {
			continue
		}
		// The call itself reaches rung 4 and starts the fetch; the answer is
		// deliberately discarded (Lookup never blocks).
		_ = agent.ResolveWindow(cfg, provider, model, ac.ID)
	}
}

// providerCredential resolves provider's API key from its configured
// api_key_ref, or "" when there is none.
func providerCredential(src configSource, store *credentials.Store, provider string) string {
	if src == nil {
		return ""
	}
	cfg := src.GetConfig()
	if cfg == nil {
		return ""
	}
	for _, m := range cfg.Providers {
		if m == nil || strings.TrimSpace(m.Provider) != provider {
			continue
		}
		if key := m.APIKey(); key != "" {
			return key
		}
		ref := strings.TrimSpace(m.APIKeyRef)
		if ref == "" || store == nil {
			continue
		}
		if v, err := credentials.ResolveRef(store, ref); err == nil && v != "" {
			return v
		}
	}
	return ""
}
