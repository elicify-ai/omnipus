package agent

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// candidateProviderRows preserves configured order within the exact-model,
// explicit model-list, or provider-wide row group. Rows for a different local
// model cannot override a configured exact route.
func candidateProviderRows(cfg *config.Config, candidate providers.FallbackCandidate) []*config.ModelConfig {
	var exact, listed, other []*config.ModelConfig
	for _, row := range cfg.Providers {
		if row == nil || strings.TrimSpace(row.Provider) != strings.TrimSpace(candidate.Provider) {
			continue
		}
		if strings.TrimSpace(row.Model) == candidate.Model {
			exact = append(exact, row)
			continue
		}
		found := false
		for _, model := range row.Models {
			if strings.TrimSpace(model) == candidate.Model {
				found = true
				break
			}
		}
		if found {
			listed = append(listed, row)
		} else {
			other = append(other, row)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	if len(listed) > 0 {
		return listed
	}
	return other
}

// providerRowUsable checks explicit credential requirements and endpoint
// syntax without making a request. The factory retains protocol-specific
// local/keyless/OAuth requirements; a missing explicit ref is never usable.
func providerRowUsable(row *config.ModelConfig) error {
	if strings.TrimSpace(row.APIKeyRef) != "" && row.AuthMethod != config.AuthMethodSignIn && strings.TrimSpace(row.APIKey()) == "" {
		return fmt.Errorf("provider %q credential reference %q is unresolved", row.Provider, row.APIKeyRef)
	}
	if base := strings.TrimSpace(row.APIBase); base != "" {
		u, err := url.Parse(base)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("provider %q API endpoint must be an absolute HTTP or HTTPS URL", row.Provider)
		}
	}
	return nil
}

func buildCandidateProvider(cfg *config.Config, candidate providers.FallbackCandidate, cache map[*config.ModelConfig]providers.LLMProvider) (providers.LLMProvider, error) {
	var failures []error
	for _, row := range candidateProviderRows(cfg, candidate) {
		if err := providerRowUsable(row); err != nil {
			failures = append(failures, err)
			continue
		}
		if p := cache[row]; p != nil {
			return p, nil
		}
		clone := *row
		clone.Model = candidate.Model
		p, _, err := providers.CreateProviderFromConfig(&clone)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		cache[row] = p
		return p, nil
	}
	if len(failures) == 0 {
		return nil, fmt.Errorf("provider %q has no configured row for model %q", candidate.Provider, candidate.Model)
	}
	return nil, errors.Join(failures...)
}

// storeProviderPoolBuild publishes pair-specific clients and their constructor
// failures with the provider pool, under the caller's model/pool lock.
func (a *AgentInstance) storeProviderPoolBuild(build providerPoolBuild) {
	a.StoreProviderPool(build.pool)
	if build.routes != nil {
		routes := make(map[providers.FallbackCandidate]providers.LLMProvider, len(build.routes))
		for key, p := range build.routes {
			routes[key] = p
		}
		a.providerRoutes.Store(&routes)
	}
	if len(build.failures) > 0 {
		failures := make(map[providers.FallbackCandidate]error, len(build.failures))
		for key, err := range build.failures {
			failures[key] = err
		}
		a.providerPoolFailures.Store(&failures)
	}
}

// providerForTurnCandidate requires the caller's agent read lock. Unlike the
// legacy provider-only accessor, every named miss returns a refusal, not the
// borrowed global-default client. The light pair is a constructor-bound fact.
func (a *AgentInstance) providerForTurnCandidate(candidate providers.FallbackCandidate, light bool) (providers.LLMProvider, error) {
	candidate.Provider = strings.TrimSpace(candidate.Provider)
	if candidate.Provider == "" {
		return a.Provider, nil
	}
	if light && a.LightProvider != nil && a.lightProviderPair == candidate {
		return a.LightProvider, nil
	}
	if routes := a.providerRoutes.Load(); routes != nil {
		if p := (*routes)[candidate]; p != nil {
			return p, nil
		}
		return nil, a.unavailableModelRoute(candidate)
	}
	if pool := a.providerPool.Load(); pool != nil {
		if p := (*pool)[strings.TrimSpace(candidate.Provider)]; p != nil {
			return p, nil
		}
	}
	return nil, a.unavailableModelRoute(candidate)
}

func (a *AgentInstance) unavailableModelRoute(candidate providers.FallbackCandidate) error {
	var cause error
	if failures := a.providerPoolFailures.Load(); failures != nil {
		cause = (*failures)[candidate]
	}
	if cause == nil {
		cause = fmt.Errorf("no usable configured client for provider %q and model %q", candidate.Provider, candidate.Model)
	}
	return &providers.ModelRouteUnavailableError{Provider: candidate.Provider, Model: candidate.Model, Cause: cause}
}
