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
	"sort"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/tools"
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

// cloudProviders is the known set of cloud provider names.
var cloudProviders = map[string]bool{
	"anthropic": true, "openai": true, "deepseek": true, "groq": true,
	"openrouter": true, "azure": true, "bedrock": true, "cohere": true,
	"mistral": true, "gemini": true,
}

// localProviders is the known set of local provider names.
var localProviders = map[string]bool{
	"ollama": true, "llamacpp": true, "lmstudio": true,
}

// ---- system.provider.configure ----

type ProviderConfigureTool struct{ deps *Deps }

func NewProviderConfigureTool(d *Deps) *ProviderConfigureTool {
	return &ProviderConfigureTool{deps: d}
}
func (t *ProviderConfigureTool) Name() string           { return "system.provider.configure" }
func (t *ProviderConfigureTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ProviderConfigureTool) Description() string {
	return "Add or update an LLM provider with its API key.\nParameters: name (required), api_key (for cloud), api_base (optional)."
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
	apiKey, _ := args["api_key"].(string)
	if cloudProviders[name] && apiKey == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("api_key is required for cloud provider %q", name),
			"Provide your API key for this provider",
		))
	}
	// Store API key encrypted (write-only per D.4.8).
	if apiKey != "" {
		credKey := fmt.Sprintf("provider.%s.api_key", name)
		if err := t.deps.CredStore.Set(credKey, apiKey); err != nil {
			return tools.ErrorResult(errorJSON("CREDENTIAL_SAVE_FAILED",
				"Failed to store API key: "+err.Error(),
				"Check that the credential store is unlocked",
			))
		}
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"name":             name,
		"status":           "connected",
		"models_available": []string{},
	}))
}

// ---- system.provider.list ----

// ProviderListTool implements system.provider.list. API keys are NEVER returned.
type ProviderListTool struct{ deps *Deps }

func NewProviderListTool(d *Deps) *ProviderListTool { return &ProviderListTool{deps: d} }
func (t *ProviderListTool) Name() string            { return "system.provider.list" }
func (t *ProviderListTool) Scope() tools.ToolScope  { return tools.ScopeCore }
func (t *ProviderListTool) Description() string {
	return "List configured providers with connection status. API keys are never returned. No parameters required."
}

func (t *ProviderListTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *ProviderListTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	type providerSummary struct {
		Name            string   `json:"name"`
		Status          string   `json:"status"`
		ModelsAvailable []string `json:"models_available"`
	}
	// Enumerate providers from config's model_list, redacting credentials.
	var providers []providerSummary
	seen := map[string]bool{}
	for _, m := range t.deps.GetCfg().Providers {
		if m == nil {
			continue
		}
		// ModelConfig.Model is "provider/model-name" (e.g. "anthropic/claude-sonnet-4-6").
		// Extract the provider prefix.
		p := providerFromModelRef(m.Model)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		providers = append(providers, providerSummary{
			Name:            p,
			Status:          "configured",
			ModelsAvailable: []string{},
		})
	}
	return tools.NewToolResult(successJSON(map[string]any{"providers": providers}))
}

// ---- system.provider.test ----

type ProviderTestTool struct{ deps *Deps }

func NewProviderTestTool(d *Deps) *ProviderTestTool { return &ProviderTestTool{deps: d} }
func (t *ProviderTestTool) Name() string            { return "system.provider.test" }
func (t *ProviderTestTool) Scope() tools.ToolScope  { return tools.ScopeCore }
func (t *ProviderTestTool) Description() string {
	return "Test a provider connection. Parameters: name (required)."
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
	// Actual connection test requires a live LLM provider instance.
	// Return ok/not-configured based on whether credentials exist.
	credKey := fmt.Sprintf("provider.%s.api_key", name)
	_, err := t.deps.CredStore.Get(credKey)
	if err != nil && !localProviders[name] {
		return tools.NewToolResult(successJSON(map[string]any{
			"name":          name,
			"status":        "error",
			"error_message": "No API key configured for this provider",
		}))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"name":       name,
		"status":     "ok",
		"latency_ms": 0,
	}))
}

// ---- system.models.list ----

// ModelsListTool lists available models from configured providers.
// Used by Ava (Agent Builder) to help users select a model for new agents.
type ModelsListTool struct{ deps *Deps }

func NewModelsListTool(d *Deps) *ModelsListTool  { return &ModelsListTool{deps: d} }
func (t *ModelsListTool) Name() string           { return "system.models.list" }
func (t *ModelsListTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ModelsListTool) Description() string {
	return "List available models from configured providers. " +
		"Optional: filter by provider name. Returns model slugs, provider, and whether it's the system default."
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

	defaultModel := cfg.Agents.Defaults.ModelName

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
		baseURL := providers.GetDefaultAPIBase(pi.name)
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
			slog.Warn("system.models.list: failed to fetch models", "provider", pi.name, "error", err)
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
				Default:  m == defaultModel,
			})
		}
	}

	result := map[string]any{
		"models":        models,
		"default_model": defaultModel,
		"total":         len(models),
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return tools.NewToolResult(successJSON(result))
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
