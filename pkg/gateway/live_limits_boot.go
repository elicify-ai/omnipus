// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
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
func newLiveLimitsForBoot(homePath string, store *credentials.Store, src configSource) *agent.LiveLimits {
	return agent.NewLiveLimits(agent.LiveLimitsOptions{
		CachePath:  filepath.Join(homePath, filepath.FromSlash(liveLimitsCacheFile)),
		Credential: func(provider string) string { return providerCredential(src, store, provider) },
	})
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
