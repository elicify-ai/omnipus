// config_models.go: Model provider resolution - slug lookup, round-robin matching, validation and credential usability

package config

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

// IsVirtual returns true if this model was generated from multi-key expansion.
func (c *ModelConfig) IsVirtual() bool {
	return c.isVirtual
}

// APIKey returns the resolved API key for this model. After InjectFromConfig runs,
// the ref value is available as an environment variable. Returns "" if no ref is set
// or the env var is unset.
func (c *ModelConfig) APIKey() string {
	if c.APIKeyRef == "" {
		return ""
	}
	return os.Getenv(c.APIKeyRef)
}

// ModelConfig.AuthMethod closed set (ADR-068 FR-003, X-25). These mirror the
// wire enum in contracts/components/schemas/Provider.yaml (`auth_method`).
const (
	// AuthMethodAPIKey — the row authenticates with a credential-store API key.
	AuthMethodAPIKey = "api_key"
	// AuthMethodSignIn — the row authenticates through a vendor CLI sign-in
	// whose credential file Omnipus reads but never writes (FR-007).
	AuthMethodSignIn = "sign_in"
)

// Validate checks if the ModelConfig has all required fields and that
// auth_method, when set, is one of the closed set.
func (c *ModelConfig) Validate() error {
	if c.Provider == "" {
		return fmt.Errorf("provider is required")
	}
	if c.Model == "" {
		return fmt.Errorf("model is required")
	}
	switch c.AuthMethod {
	case "", AuthMethodAPIKey, AuthMethodSignIn:
		return nil
	default:
		return fmt.Errorf("auth_method %q is not supported; must be %q or %q",
			c.AuthMethod, AuthMethodAPIKey, AuthMethodSignIn)
	}
}

// normalizeProviderRows trims whitespace off the identity fields of every
// configured provider row (ADR-067 FR-036). It never changes case, never
// rewrites an id, and never drops a row: an id that is wrong after trimming
// stays wrong, and is reported as an unknown provider by whoever asks the
// catalog about it.
func normalizeProviderRows(cfg *Config) {
	if cfg == nil {
		return
	}
	for i := range cfg.Providers {
		p := cfg.Providers[i]
		if p == nil {
			continue
		}
		p.Provider = strings.TrimSpace(p.Provider)
		p.Model = strings.TrimSpace(p.Model)
		p.Protocol = strings.TrimSpace(p.Protocol)
		p.APIBase = strings.TrimSpace(p.APIBase)
	}
}

// rrCounter is a global counter for round-robin load balancing across models.
var rrCounter atomic.Uint64

// GetModelConfig returns the ModelConfig for the EXACT (provider, model)
// pair (ADR-068 D14.1 / ADR-067's exact lookup). Both halves are required —
// the model id alone is not a key, a row's user-facing model_name alias is
// not a key (that alias resolution was deleted with agents.defaults.model_name,
// CRIT-001), and no prefix stripping or cross-provider fallback happens here.
// If several providers[] rows carry the same pair (load balancing) it
// round-robins over the USABLE ones only, see findMatches.
// Returns an error if no row carries the pair.
func (c *Config) GetModelConfig(provider, model string) (*ModelConfig, error) {
	matches := c.findMatches(provider, model)
	if len(matches) == 0 {
		return nil, fmt.Errorf("model %q not found in providers for provider %q", model, provider)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}

	// Multiple configs - use round-robin for load balancing
	idx := (rrCounter.Add(1) - 1) % uint64(len(matches))
	return matches[idx], nil
}

// modelConfigCredentialUsable reports whether m's credential requirement is
// satisfied: either it names no vault ref at all (local model, CLI/OAuth
// auth, api_base-only provider — these never had a vault credential to
// begin with), or its api_key_ref resolved to a non-empty value in the
// process environment (InjectFromConfig runs at boot/reload, before any
// caller reaches GetModelConfig). Mirrors the "usable" test
// gateway.go's defaultModelCredentialBlocked applies to the default model.
func modelConfigCredentialUsable(m *ModelConfig) bool {
	if m == nil {
		return false
	}
	if strings.TrimSpace(m.APIKeyRef) == "" {
		return true
	}
	return m.APIKey() != ""
}

// findMatches finds all ModelConfig entries carrying the exact
// (provider, model) pair, preferring USABLE ones (see
// modelConfigCredentialUsable) when at least one exists. Whitespace-trimmed
// on both sides; an empty model never matches, and an empty provider matches
// only rows whose own provider is empty (exact, never "any provider").
//
// Why (2026-08-15): several providers[] entries may share one pair for
// load balancing, round-robinned by GetModelConfig above. Before this
// change, an entry whose api_key_ref never resolved (missing from the
// vault, wrong master key while it was still degradable, …) stayed in the
// candidate pool on equal footing with a working sibling — round-robin,
// being a plain counter with no key-awareness, could hand the broken entry
// back to CreateProviderFromConfig, which happily builds an *HTTPProvider
// with an empty API key (api_key OR api_base satisfies its check) and
// produces a bare upstream 401 naming neither the provider nor the
// credential — non-deterministically, since which call in the rotation gets
// the broken entry depends on the shared global rrCounter. This was
// unreachable before gateway.go's reportInjectionErrors started degrading
// (rather than aborting) on a single unresolvable provider ref, because
// boot used to abort outright on that config; the degrade-not-abort fix
// made this reachable for the first time.
//
// Filtering to the usable subset (when non-empty) removes the broken
// entries from the rotation entirely — the exact fix load-balanced
// failover already implies: an unusable sibling behaves like it isn't
// there. If NONE of the matches are usable, all of them are returned
// unfiltered so the caller still gets a ModelConfig back (and, for the
// default model, gateway.go's defaultModelCredentialBlocked reports it as
// blocked rather than silently 401ing).
func (c *Config) findMatches(provider, model string) []*ModelConfig {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	var all []*ModelConfig
	var usable []*ModelConfig
	for i := range c.Providers {
		if c.Providers[i] == nil ||
			strings.TrimSpace(c.Providers[i].Provider) != provider ||
			strings.TrimSpace(c.Providers[i].Model) != model {
			continue
		}
		all = append(all, c.Providers[i])
		if modelConfigCredentialUsable(c.Providers[i]) {
			usable = append(usable, c.Providers[i])
		}
	}
	if len(usable) > 0 {
		return usable
	}
	return all
}

// FindModelConfigBySlug resolves a bare model slug — a per-agent `model`, a
// `voice.model_name`, a composer pick — to the providers[] row that SERVES
// it, without a provider half. It is NOT the default-model lookup (that is
// the exact pair, GetModelConfig) and it applies no passthrough fallback
// (pkg/agent's ResolveModelCfg layers that on top).
//
// Order: a row whose Model equals the slug wins — and that is the ONLY
// rung. The display-alias rung is gone with ModelConfig.ModelName (ADR-067
// FR-013 / X-25). Among several hits the USABLE ones are preferred
// (modelConfigCredentialUsable), round-robinned like GetModelConfig.
func (c *Config) FindModelConfigBySlug(slug string) (*ModelConfig, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil, fmt.Errorf("model slug is required")
	}
	pick := func(match func(*ModelConfig) bool) *ModelConfig {
		var all, usable []*ModelConfig
		for i := range c.Providers {
			m := c.Providers[i]
			if m == nil || !match(m) {
				continue
			}
			all = append(all, m)
			if modelConfigCredentialUsable(m) {
				usable = append(usable, m)
			}
		}
		pool := all
		if len(usable) > 0 {
			pool = usable
		}
		switch len(pool) {
		case 0:
			return nil
		case 1:
			return pool[0]
		default:
			return pool[(rrCounter.Add(1)-1)%uint64(len(pool))]
		}
	}
	if m := pick(func(m *ModelConfig) bool { return strings.TrimSpace(m.Model) == slug }); m != nil {
		return m, nil
	}
	return nil, fmt.Errorf("model %q not found in model_list or providers", slug)
}

// ValidateProviders validates all ModelConfig entries in the providers config.
// It checks that each model config is valid.
// Note: Multiple entries with the same (provider, model) pair are allowed —
// that is how multi-key load balancing is expressed.
func (c *Config) ValidateProviders() error {
	for i := range c.Providers {
		if err := c.Providers[i].Validate(); err != nil {
			return fmt.Errorf("providers[%d]: %w", i, err)
		}
	}
	return nil
}
