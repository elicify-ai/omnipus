// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// providerFromModelRef extracts the provider prefix from a "provider/model" reference.
// Returns "" if the reference has no slash separator.
func providerFromModelRef(ref string) string {
	for i, ch := range ref {
		if ch == '/' {
			return ref[:i]
		}
	}
	return ""
}

// catalogProviderLocality looks name up in the canonical provider catalog
// (pkg/providers/catalog/data/providers_catalog.json, 211 providers) and
// reports whether it is a known id and, if so, whether it is local.
//
// This replaces two hand-maintained ~10-name allow-lists (cloudProviders,
// localProviders) that both omitted ~200 real cloud providers — xai,
// togetherai, cerebras, fireworks-ai, perplexity, deepinfra, nvidia,
// google-vertex, moonshotai, zhipuai, minimax, baseten, databricks,
// huggingface, and many more — and accepted two ids that are not real
// catalog ids at all: "bedrock" and "gemini" (the real ids are
// "amazon-bedrock" and "google"; "llamacpp" is likewise not a catalog id).
// providers.IsCatalogProvider's own doc comment names itself "the ONE
// membership test every gate uses" — this mirrors that, and reads the
// row's Locality field, which the catalog parser derives at load time via
// catalog.DeriveLocality (FR-039) rather than re-deriving it here.
func catalogProviderLocality(name string) (row catalog.Provider, known, isLocal bool) {
	row, known = providers.CatalogProvider(name)
	if !known {
		return row, false, false
	}
	return row, true, row.Locality == catalog.LocalityLocal
}

// ---- configure_provider ----

type ProviderConfigureTool struct{ deps *Deps }

func NewProviderConfigureTool(d *Deps) *ProviderConfigureTool {
	return &ProviderConfigureTool{deps: d}
}
func (t *ProviderConfigureTool) Name() string           { return "configure_provider" }
func (t *ProviderConfigureTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ProviderConfigureTool) Description() string {
	return "Store an LLM provider's API key in the encrypted credential store and wire it into config.json " +
		"so the provider becomes usable. Does NOT contact the provider — the key is stored and referenced, " +
		"never validated against the live API, so a wrong or revoked key is accepted here without complaint; " +
		"call list_models afterwards to confirm the key actually works. name must be a catalog provider id " +
		"(list_models or the Settings → Providers screen show the valid ids; note the ids are " +
		"'amazon-bedrock' and 'google', not 'bedrock' or 'gemini'). api_key is required for every cloud " +
		"provider and optional only for local ones (ollama, lmstudio, vllm). api_base is optional and " +
		"overrides the catalog's default endpoint; it must be a full http:// or https:// URL with a host.\n" +
		"Parameters: name (required), api_key, api_base."
}

func (t *ProviderConfigureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":     map[string]any{"type": "string"},
			"api_key":  map[string]any{"type": "string"},
			"api_base": map[string]any{"type": "string"},
		},
		"required": []string{"name"},
	}
}

func (t *ProviderConfigureTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	row, known, isLocal := catalogProviderLocality(name)
	if !known {
		return tools.ErrorResult(errorJSON("UNKNOWN_PROVIDER",
			fmt.Sprintf("%q is not a known provider id", name),
			"Use the catalog id (e.g. 'amazon-bedrock', not 'bedrock'; 'google', not 'gemini')",
		))
	}
	apiKey, _ := args["api_key"].(string)
	apiBase, _ := args["api_base"].(string)
	if apiBase != "" {
		if err := validateAPIBase(apiBase); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(),
				`Provide a full URL with scheme, e.g. "https://api.openai.com/v1"`))
		}
	}
	if !isLocal && apiKey == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("api_key is required for cloud provider %q", name),
			"Provide your API key for this provider",
		))
	}

	// The credential store is the single source of truth; a provider's key is
	// resolved through its config entry's APIKeyRef (looked up in credentials.json
	// and injected to the environment at boot/reload). The ref name follows the
	// onboarding convention "<provider>_API_KEY". If the provider already has a
	// config entry carrying a ref, we update the key under that same ref so we
	// don't strand the existing reference.
	ref := name + "_API_KEY"
	for _, p := range t.deps.GetCfg().Providers {
		if p == nil {
			continue
		}
		if providerNameOf(p) == name && p.APIKeyRef != "" {
			ref = p.APIKeyRef
			break
		}
	}

	if apiKey != "" {
		if err := t.deps.CredStore.Set(ref, apiKey); err != nil {
			return tools.ErrorResult(errorJSON("CREDENTIAL_SAVE_FAILED",
				"Failed to store API key: "+err.Error(),
				"Check that the credential store is unlocked",
			))
		}
	}

	// Wire the config so the stored key is actually referenced (not orphaned):
	// set APIKeyRef on the provider's entries and persist. Without this the key
	// sat in the store unreferenced — never injected, never resolved.
	if err := t.deps.WithConfig(func(cfg *config.Config) error {
		wired := false
		for _, p := range cfg.Providers {
			if p == nil || providerNameOf(p) != name {
				continue
			}
			if apiKey != "" && p.APIKeyRef == "" {
				p.APIKeyRef = ref
			}
			if apiBase != "" {
				p.APIBase = apiBase
			}
			wired = true
		}
		if !wired {
			np := &config.ModelConfig{Provider: name, Model: name}
			if apiKey != "" {
				np.APIKeyRef = ref
			}
			if apiBase != "" {
				np.APIBase = apiBase
			}
			cfg.Providers = append(cfg.Providers, np)
		}
		return nil
	}); err != nil {
		return tools.ErrorResult(errorJSON("CONFIG_SAVE_FAILED",
			"Stored the key but failed to wire it into config: "+err.Error(),
			"The provider key is saved; retry to finish configuring it",
		))
	}

	// Apply immediately: re-injects provider credentials into the environment and
	// rebuilds the agent loop so the provider is usable without a restart. A
	// reload failure does NOT undo the store/config writes above (the key really
	// is saved), so this is still reported as a success — but with a
	// publish_warning naming the failure, mirroring the pattern adopted for
	// create_agent/update_agent/delete_agent: an unqualified success response
	// would tell the caller the provider is live when it is not yet usable.
	var publishWarning string
	if t.deps.ReloadFunc != nil {
		if err := t.deps.ReloadFunc(); err != nil {
			slog.Warn("sysagent: configure_provider: reload after provider configure failed — provider available after restart",
				"provider", name, "error", err)
			publishWarning = fmt.Sprintf(
				"provider %q was configured but is not yet live: reload failed (%s); it will become usable "+
					"after the next config reload or gateway restart", name, err.Error())
		}
	}

	// api_key_stored reflects whether a key now resolves under ref, not merely
	// whether this call was passed one — a repeat call with only api_base set
	// still has a previously-stored key.
	apiKeyStored := apiKey != ""
	if !apiKeyStored {
		if _, err := t.deps.CredStore.Get(ref); err == nil {
			apiKeyStored = true
		}
	}
	resolvedAPIBase := apiBase
	if resolvedAPIBase == "" {
		resolvedAPIBase = row.API
	}

	result := map[string]any{
		"name":           name,
		"api_key_stored": apiKeyStored,
		"api_base":       resolvedAPIBase,
	}
	if publishWarning != "" {
		result["publish_warning"] = publishWarning
	}
	return tools.NewToolResult(successJSON(result))
}

// providerNameOf returns a provider entry's canonical name — the explicit
// Provider field, or the prefix of its "provider/model" reference.
func providerNameOf(p *config.ModelConfig) string {
	if p.Provider != "" {
		return p.Provider
	}
	return providerFromModelRef(p.Model)
}

// ---- list_providers ----

// ProviderListTool implements list_providers. API keys are NEVER returned.
type ProviderListTool struct{ deps *Deps }

func NewProviderListTool(d *Deps) *ProviderListTool { return &ProviderListTool{deps: d} }
func (t *ProviderListTool) Name() string            { return "list_providers" }
func (t *ProviderListTool) Scope() tools.ToolScope  { return tools.ScopeCore }
func (t *ProviderListTool) Description() string {
	return "List the providers configured in config.json and whether each one has an API key present in " +
		"the credential store (key_present / no_credentials). API keys are never returned, and no network " +
		"call is made — key_present means a key is stored, not that it works. Use list_models to exercise " +
		"a provider's live API. No parameters required."
}

func (t *ProviderListTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *ProviderListTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	type providerSummary struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	// Enumerate providers from config's model_list, redacting credentials.
	// providerNameOf (not the bare providerFromModelRef fallback) is used so
	// an entry configured via configure_provider — which sets the explicit
	// Provider field rather than a "provider/model" Model slug — is not
	// silently skipped here (F4/F3(b)).
	var list []providerSummary
	seen := map[string]bool{}
	for _, m := range t.deps.GetCfg().Providers {
		if m == nil {
			continue
		}
		p := providerNameOf(m)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		status := "no_credentials"
		if m.APIKeyRef != "" && t.deps.CredStore != nil {
			if _, err := t.deps.CredStore.Get(m.APIKeyRef); err == nil {
				status = "key_present"
			}
		}
		list = append(list, providerSummary{Name: p, Status: status})
	}
	return tools.NewToolResult(successJSON(map[string]any{"providers": list}))
}

// ---- test_provider ----

type ProviderTestTool struct{ deps *Deps }

func NewProviderTestTool(d *Deps) *ProviderTestTool { return &ProviderTestTool{deps: d} }
func (t *ProviderTestTool) Name() string            { return "test_provider" }
func (t *ProviderTestTool) Scope() tools.ToolScope  { return tools.ScopeCore }

// Description is deliberately explicit about what this tool does NOT do:
// it makes zero network calls and never contacts the provider, so a
// revoked, expired, or wrong API key can still report "ok" here. It was
// previously described as "Test a provider connection", which reads as a
// live connectivity check but never made one — a caller had no way to know
// the check was credential-presence-only short of reading the source.
func (t *ProviderTestTool) Description() string {
	return "Check that this provider's API key resolves in the credential store.\n" +
		"Does NOT contact the provider — this makes zero network calls, so a revoked, expired, or " +
		"wrong API key can still report status=\"ok\" here. Local providers (ollama, lmstudio, vllm) " +
		"always report ok — they need no key. A failed check comes back as a normal result with " +
		"status=\"error\", not as a tool error. Parameters: name (required). Use " +
		"list_models to actually exercise the connection against the provider's live API."
}

func (t *ProviderTestTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"name": map[string]any{"type": "string"}},
		"required":   []string{"name"},
	}
}

func (t *ProviderTestTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	// This is a credential-presence check only — see Description(). It makes
	// no network call, so there is no latency to report; the response
	// previously carried a hardcoded "latency_ms": 0 that read as a real
	// measurement of a connection that was never made. Report ok/error based
	// on whether the key resolves the canonical way: the provider's config
	// entry APIKeyRef, falling back to the conventional <provider>_API_KEY
	// ref (used by both onboarding and provider.configure).
	ref := name + "_API_KEY"
	for _, p := range t.deps.GetCfg().Providers {
		if p != nil && providerNameOf(p) == name && p.APIKeyRef != "" {
			ref = p.APIKeyRef
			break
		}
	}
	_, _, isLocal := catalogProviderLocality(name)
	_, err := t.deps.CredStore.Get(ref)
	if err != nil && !isLocal {
		return tools.NewToolResult(successJSON(map[string]any{
			"name":          name,
			"status":        "error",
			"error_message": "No API key configured for this provider",
		}))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"name":   name,
		"status": "ok",
		"note":   "credential presence only — no network call was made to the provider",
	}))
}

// ---- list_models ----

// ModelsListTool lists available models from configured providers.
// Used by Ava (Agent Builder) to help users select a model for new agents.
type ModelsListTool struct{ deps *Deps }

func NewModelsListTool(d *Deps) *ModelsListTool  { return &ModelsListTool{deps: d} }
func (t *ModelsListTool) Name() string           { return "list_models" }
func (t *ModelsListTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ModelsListTool) Description() string {
	return "List models actually available from your configured providers. This queries each provider's " +
		"live /models API (up to 15s per provider), so it doubles as a real connectivity and credential " +
		"check — it is what test_provider points you to. Returns each model's slug, its provider, and " +
		"whether it is the system default, plus the configured default_model. Providers that have no API " +
		"key, no known base URL, or that fail to respond are omitted from the list and reported in " +
		"`warnings` — check that field before treating the list as complete.\n" +
		"Parameters: provider (optional, filter to one provider)."
}

func (t *ModelsListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"provider": map[string]any{
				"type":        "string",
				"description": "Filter by provider name (e.g. 'openrouter'). Omit to show all.",
			},
		},
	}
}

func (t *ModelsListTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	filterProvider, _ := args["provider"].(string)
	cfg := t.deps.GetCfg()

	type modelEntry struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
		Default  bool   `json:"default,omitempty"`
	}

	defaultModel := cfg.Agents.Defaults.DefaultModel

	// Collect unique providers and resolve their API keys.
	type providerInfo struct {
		name   string
		apiKey string
	}
	providersSeen := map[string]*providerInfo{}
	for _, p := range cfg.Providers {
		if p == nil {
			continue
		}
		name := p.Provider
		if name == "" {
			name = providerFromModelRef(p.Model)
		}
		if name == "" {
			continue
		}
		if filterProvider != "" && name != filterProvider {
			continue
		}
		// The credential store (referenced by APIKeyRef) is the single source of
		// truth for a provider's key; APIKey() returns the value boot-injected
		// into the environment from that same ref. A provider name can appear in
		// config multiple times — seed scaffolding entries carry an empty
		// APIKeyRef while the onboarded/configured entry carries the ref. We must
		// therefore select the entry that actually resolves a key rather than the
		// first one encountered (the previous bug: the keyless seed entry won and
		// models.list reported "no API key configured").
		apiKey := p.APIKey()
		if apiKey == "" && p.APIKeyRef != "" && t.deps.CredStore != nil {
			if v, err := t.deps.CredStore.Get(p.APIKeyRef); err == nil {
				apiKey = v
			}
		}
		if existing := providersSeen[name]; existing != nil && (existing.apiKey != "" || apiKey == "") {
			continue
		}
		providersSeen[name] = &providerInfo{name: name, apiKey: apiKey}
	}

	// Fetch models from each provider's upstream API.
	var models []modelEntry
	seen := map[string]bool{}
	var warnings []string
	for _, pi := range providersSeen {
		// ADR-067 FR-012: the base URL is the catalog row's, never a
		// hand-typed per-vendor switch.
		baseURL := providers.APIBaseFor(pi.name)
		if baseURL == "" {
			warnings = append(warnings, fmt.Sprintf("%s: no API base URL configured", pi.name))
			continue
		}
		if pi.apiKey == "" {
			warnings = append(warnings, fmt.Sprintf("%s: no API key configured", pi.name))
			continue
		}
		upstream, err := fetchProviderModels(baseURL, pi.apiKey)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: could not fetch model list", pi.name))
			slog.Warn("list_models: failed to fetch models", "provider", pi.name, "error", err)
			continue
		}
		for _, m := range upstream {
			if seen[m] {
				continue
			}
			seen[m] = true
			models = append(models, modelEntry{
				Model:    m,
				Provider: pi.name,
				Default:  pi.name == defaultModel.Provider && m == defaultModel.Model,
			})
		}
	}

	result := map[string]any{
		"models":        models,
		"default_model": map[string]string{"provider": defaultModel.Provider, "model": defaultModel.Model},
		"total":         len(models),
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return tools.NewToolResult(successJSON(result))
}

// validateAPIBase checks that s is a valid http:// or https:// URL with a non-empty host.
// Empty s is accepted (means "use provider default").
func validateAPIBase(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("api_base %q is not a valid URL: %w", s, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("api_base %q must use http:// or https:// scheme (got %q)", s, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("api_base %q has no host — provide a full URL, e.g. https://api.example.com/v1", s)
	}
	return nil
}

// fetchProviderModels fetches models from an OpenAI-compatible /models endpoint.
func fetchProviderModels(baseURL, apiKey string) ([]string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", strings.TrimSuffix(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("X-Api-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	sort.Strings(models)
	return models, nil
}
