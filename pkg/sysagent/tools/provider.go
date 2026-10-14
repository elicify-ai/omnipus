// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
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

// credentialPresent reports whether ref names a credential that currently
// resolves in store. Nil-safe: store is nil in some test/scaffolding
// contexts (a Deps built without a CredStore), and a naked store.Get(ref)
// on a nil *credentials.Store nil-derefs. configure_provider, list_providers,
// and test_provider all need the identical "does this ref resolve" check —
// this is the one place that answers it so each of the three doesn't
// re-derive its own nil-guard.
func credentialPresent(store *credentials.Store, ref string) bool {
	if store == nil || ref == "" {
		return false
	}
	_, err := store.Get(ref)
	return err == nil
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
		"so the provider becomes usable, then check the SAVED entry: the configured endpoint is asked for its " +
		"model list over the provider's protocol with the saved credential (local providers need no key). " +
		"The result's outcome says whether that check passed; it does not prove that a chosen model runs. " +
		"name must be a catalog provider id " +
		"(list_models or the Settings → Providers screen show the valid ids; note the ids are " +
		"'amazon-bedrock' and 'google', not 'bedrock' or 'gemini'). api_key is required for every cloud " +
		"provider and optional only for local ones (ollama, lmstudio, vllm). api_base is optional and " +
		"overrides the catalog's default endpoint; it must be a full http:// or https:// URL with a host. " +
		"expected_revision is the provider's revision from list_providers (\"absent\" when it is not configured yet); " +
		"the write is refused if the provider changed since. dry_run checks the input shape only and saves nothing.\n" +
		"Parameters: name (required), api_key, api_base, expected_revision (required unless dry_run), dry_run."
}

func (t *ProviderConfigureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":     map[string]any{"type": "string"},
			"api_key":  map[string]any{"type": "string"},
			"api_base": map[string]any{"type": "string"},
			expectedRevisionParam: map[string]any{
				"type":        "string",
				"description": "The provider's revision from list_providers; \"absent\" for a provider that is not configured yet.",
			},
			"dry_run": map[string]any{
				"type":        "boolean",
				"description": "Validate the provider id, key shape and api_base shape only; save nothing and make no network call.",
			},
		},
		"required": []string{"name"},
	}
}

// providerCheckTimeout bounds configure_provider's model-listing check.
const providerCheckTimeout = 15 * time.Second

// providerConfigureInput is configure_provider's validated input.
type providerConfigureInput struct {
	name    string
	row     catalog.Provider
	isLocal bool
	apiKey  string
	apiBase string
}

func (t *ProviderConfigureTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	in, refusal := validateProviderConfigureInput(args)
	if refusal != nil {
		return refusal
	}
	if dry, _ := args["dry_run"].(bool); dry {
		return providerDryRunResult(in)
	}
	expected, missing := expectedRevisionArg(args, "list_providers")
	if missing != nil {
		return missing
	}

	// The credential store is the single source of truth; a provider's key is
	// resolved through its config entry's APIKeyRef (looked up in credentials.json
	// and injected to the environment at boot/reload). The ref name follows the
	// onboarding convention "<provider>_API_KEY". If the provider already has a
	// config entry carrying a ref, we update the key under that same ref so we
	// don't strand the existing reference.
	ref := in.name + "_API_KEY"
	keyStored := false
	writeErr := t.deps.withSharedResourceWrite(sharedProvider, in.name, expected, func() error {
		if rep := representativeProviderRow(t.deps.GetCfg(), in.name); rep != nil && rep.APIKeyRef != "" {
			ref = rep.APIKeyRef
		}
		if in.apiKey != "" {
			if t.deps.CredStore == nil {
				return &providerStepError{step: "credential", err: fmt.Errorf("no credential store is configured")}
			}
			if err := t.deps.CredStore.Set(ref, in.apiKey); err != nil {
				return &providerStepError{step: "credential", err: err}
			}
			keyStored = true
		}
		// Wire the config so the stored key is actually referenced (not orphaned).
		if err := t.deps.WithConfig(func(cfg *config.Config) error {
			wireProviderEntry(cfg, in, ref)
			return nil
		}); err != nil {
			return &providerStepError{step: "config", err: err}
		}
		return nil
	})
	if r, ok := revisionConflictResult(writeErr, "list_providers"); ok {
		return r
	}
	if writeErr != nil {
		return providerWriteFailureResult(in.name, ref, keyStored, writeErr)
	}

	// Apply immediately: re-injects provider credentials into the environment and
	// rebuilds the agent loop so the provider is usable without a restart. A
	// reload failure does NOT undo the store/config writes above.
	var publishWarning string
	if t.deps.ReloadFunc != nil {
		if err := t.deps.ReloadFunc(); err != nil {
			slog.Warn("sysagent: configure_provider: reload after provider configure failed — provider available after restart",
				"provider", in.name, "error", err)
			publishWarning = fmt.Sprintf(
				"provider %q was configured but is not yet live: reload failed (%s); it will become usable "+
					"after the next config reload or gateway restart", in.name, err.Error())
		}
	}

	cfg := t.deps.GetCfg()
	rep := representativeProviderRow(cfg, in.name)
	apiKeyStored := keyStored || (rep != nil && credentialPresent(t.deps.CredStore, rep.APIKeyRef))
	result := map[string]any{
		"name":           in.name,
		"api_key_stored": apiKeyStored,
		"api_base":       in.row.API,
		"revision":       providerRevision(cfg, in.name),
		"changed_fields": providerChangedFields(in),
	}
	verdict := t.checkSavedProvider(ctx, rep)
	if ep, err := effectiveProviderEndpoint(rep); err == nil {
		result["api_base"], result["protocol"] = ep.API, string(ep.Protocol)
	}
	if publishWarning != "" {
		result["publish_warning"] = publishWarning
		verdict = partlySaved(publishWarning, verdict.CheckScope, []string{"live reload"}, verdict.NotTested...)
	}
	return changeSuccess(result, verdict)
}

// validateProviderConfigureInput checks configure_provider's arguments before
// any write or network call. It is the whole of a dry run's check.
func validateProviderConfigureInput(args map[string]any) (providerConfigureInput, *tools.ToolResult) {
	var in providerConfigureInput
	in.name, _ = args["name"].(string)
	if in.name == "" {
		return in, tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", "Provide a catalog provider id"))
	}
	var known bool
	in.row, known, in.isLocal = catalogProviderLocality(in.name)
	if !known {
		return in, tools.ErrorResult(errorJSON("UNKNOWN_PROVIDER",
			fmt.Sprintf("%q is not a known provider id", in.name),
			"Use the catalog id (e.g. 'amazon-bedrock', not 'bedrock'; 'google', not 'gemini')",
		))
	}
	in.apiKey, _ = args["api_key"].(string)
	in.apiBase, _ = args["api_base"].(string)
	if in.apiBase != "" {
		if err := validateAPIBase(in.apiBase); err != nil {
			return in, tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(),
				`Provide a full URL with scheme, e.g. "https://api.openai.com/v1"`))
		}
	}
	if !in.isLocal && in.apiKey == "" {
		return in, tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("api_key is required for cloud provider %q", in.name),
			"Provide your API key for this provider",
		))
	}
	if in.apiKey != "" {
		if err := validateAPIKeyShape(in.apiKey); err != nil {
			return in, tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(),
				"Paste the whole key exactly as the provider shows it"))
		}
	}
	return in, nil
}

// validateAPIKeyShape refuses a key no provider issues: one containing
// whitespace or control characters (a paste that picked up a newline or a
// label). It does not judge length or prefix — formats differ per provider.
func validateAPIKeyShape(key string) error {
	for _, r := range key {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("api_key contains whitespace or a control character")
		}
	}
	return nil
}

// providerDryRunResult is configure_provider's validate-only answer (#1060):
// the input passed the shape checks; nothing was saved and nothing was called.
func providerDryRunResult(in providerConfigureInput) *tools.ToolResult {
	return tools.NewToolResult(successJSON(map[string]any{
		"name":        in.name,
		"dry_run":     true,
		"valid":       true,
		"saved":       false,
		"check_scope": "shape only; no network",
		"not_tested": []string{
			"the API key against the provider's live API",
			"the endpoint answering a model listing",
		},
		"missing_parts": []string{},
	}))
}

// providerStepError names the configure_provider write step that failed.
type providerStepError struct {
	step string // "credential" or "config"
	err  error
}

func (e *providerStepError) Error() string { return e.step + ": " + e.err.Error() }
func (e *providerStepError) Unwrap() error { return e.err }

// providerWriteFailureResult reports a failed configure_provider write
// truthfully: a key already stored when the config write failed is a partly
// saved change (BDD-9.6), never "nothing saved".
func providerWriteFailureResult(name, ref string, keyStored bool, err error) *tools.ToolResult {
	var se *providerStepError
	if !errors.As(err, &se) {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	if se.step == "credential" {
		return tools.ErrorResult(errorJSON("CREDENTIAL_SAVE_FAILED",
			"Failed to store API key: "+se.err.Error(), "Check that the credential store is unlocked"))
	}
	if !keyStored {
		return tools.ErrorResult(errorJSON("CONFIG_SAVE_FAILED",
			"Failed to save the provider configuration: "+se.err.Error(), "Read list_providers and retry"))
	}
	return changeErrorWithOutcome(map[string]any{
		"success": false,
		"error": map[string]any{
			"code":       "CONFIG_SAVE_FAILED",
			"message":    "Stored the key but failed to wire it into config: " + se.err.Error(),
			"suggestion": "The key is saved under " + ref + "; read list_providers, then retry to finish configuring it",
		},
		"name":           name,
		"changed_fields": []string{"api_key"},
	}, partlySaved("the API key was stored under "+ref+" but the provider entry was not written: "+se.err.Error(),
		"", []string{"provider configuration entry"}))
}

// providerChangedFields lists the parts this call wrote.
func providerChangedFields(in providerConfigureInput) []string {
	fields := []string{"provider_entry"}
	if in.apiKey != "" {
		fields = append(fields, "api_key")
	}
	if in.apiBase != "" {
		fields = append(fields, "api_base")
	}
	return fields
}

// wireProviderEntry sets the credential reference and api_base on every row
// of the provider, or appends a row when none exists.
func wireProviderEntry(cfg *config.Config, in providerConfigureInput, ref string) {
	wired := false
	for _, p := range cfg.Providers {
		if p == nil || providerNameOf(p) != in.name {
			continue
		}
		if in.apiKey != "" && p.APIKeyRef == "" {
			p.APIKeyRef = ref
		}
		if in.apiBase != "" {
			p.APIBase = in.apiBase
		}
		wired = true
	}
	if !wired {
		np := &config.ModelConfig{Provider: in.name, Model: in.name}
		if in.apiKey != "" {
			np.APIKeyRef = ref
		}
		if in.apiBase != "" {
			np.APIBase = in.apiBase
		}
		cfg.Providers = append(cfg.Providers, np)
	}
}

// providerCheckNotTested is what a passing model listing does not prove.
var providerCheckNotTested = []string{"model execution", "quota", "tool support"}

// checkSavedProvider is the FR-031 check: it resolves the SAVED effective entry
// (configured api_base, credential, protocol) exactly as runtime does and asks
// the endpoint for its model list through the protocol-aware listing. A local
// provider is checked without a key.
func (t *ProviderConfigureTool) checkSavedProvider(ctx context.Context, rep *config.ModelConfig) changeOutcome {
	if rep == nil {
		return savedNotWorking("the saved provider entry could not be read back", "")
	}
	ep, err := effectiveProviderEndpoint(rep)
	if err != nil {
		return savedNotWorking("the saved entry does not resolve to an endpoint: "+err.Error(), "")
	}
	scope := fmt.Sprintf("model listing: the saved endpoint %s was asked for its model list over the %s protocol "+
		"with the saved credential", ep.API, ep.Protocol)
	if !providerListable(ep.Protocol) {
		return savedNotWorking(fmt.Sprintf("not checked: the %s protocol has no model listing", ep.Protocol), "",
			append([]string{"the endpoint", "the credential"}, providerCheckNotTested...)...)
	}
	key := rep.APIKey()
	if key == "" && rep.APIKeyRef != "" && t.deps.CredStore != nil {
		if v, gerr := t.deps.CredStore.Get(rep.APIKeyRef); gerr == nil {
			key = v
		}
	}
	local := ep.Locality == catalog.LocalityLocal
	if key == "" && !local && ep.Protocol != catalog.ProtocolOllama {
		return savedNotWorking("no credential resolves for this provider", scope, providerCheckNotTested...)
	}
	var checker providers.URLChecker = providers.NoopChecker{}
	if !local && t.deps.URLChecker != nil {
		checker = t.deps.URLChecker
		if cerr := checker.CheckURL(ctx, ep.API); cerr != nil {
			return savedNotWorking("the endpoint is not allowed by the outbound-request guard: "+cerr.Error(),
				scope, providerCheckNotTested...)
		}
	}
	cctx, cancel := context.WithTimeout(ctx, providerCheckTimeout)
	defer cancel()
	list := providers.ListModels
	if t.deps.ListProviderModels != nil {
		list = t.deps.ListProviderModels
	}
	models, lerr := list(cctx, ep.Protocol, ep.API, key, checker)
	if lerr != nil {
		var se *providers.UpstreamStatusError
		reason := "the endpoint could not be reached"
		if errors.As(lerr, &se) {
			reason = fmt.Sprintf("the endpoint answered the model listing with status %d", se.Status)
		}
		slog.Warn("configure_provider: saved-entry check failed", "endpoint", ep.API, "error", lerr)
		return savedNotWorking(reason, scope, providerCheckNotTested...)
	}
	return working(fmt.Sprintf("%s; it listed %d model(s)", scope, len(models)), providerCheckNotTested...)
}

// providerListable reports whether a protocol has an HTTP model listing.
func providerListable(p catalog.Protocol) bool {
	switch p {
	case catalog.ProtocolOpenAICompatible, catalog.ProtocolGoogle, catalog.ProtocolAnthropic, catalog.ProtocolOllama:
		return true
	}
	return false
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
		// Endpoint and Protocol are the saved entry's EFFECTIVE base URL and
		// wire protocol, resolved exactly as runtime does (catalog row,
		// protocol selection, api_base override). Empty when the row cannot
		// be resolved; Unresolved then carries the reason.
		Endpoint   string `json:"endpoint"`
		Protocol   string `json:"protocol"`
		Unresolved string `json:"unresolved,omitempty"`
		// APIKeyRef is the credential REFERENCE NAME (never the value).
		APIKeyRef string `json:"api_key_ref"`
		// Revision is the FR-088 token configure_provider's expected_revision
		// must carry; it changes whenever the endpoint, protocol or reference does.
		Revision string `json:"revision"`
	}
	// Enumerate providers from config's model_list, redacting credentials.
	// providerNameOf (not the bare providerFromModelRef fallback) is used so
	// an entry configured via configure_provider — which sets the explicit
	// Provider field rather than a "provider/model" Model slug — is not
	// silently skipped here.
	cfg := t.deps.GetCfg()
	list := []providerSummary{}
	seen := map[string]bool{}
	for _, m := range cfg.Providers {
		if m == nil {
			continue
		}
		p := providerNameOf(m)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		rep := representativeProviderRow(cfg, p)
		status := "no_credentials"
		if credentialPresent(t.deps.CredStore, rep.APIKeyRef) {
			status = "key_present"
		}
		summary := providerSummary{Name: p, Status: status, APIKeyRef: rep.APIKeyRef, Revision: providerRevision(cfg, p)}
		if ep, err := effectiveProviderEndpoint(rep); err == nil {
			summary.Endpoint, summary.Protocol = ep.API, string(ep.Protocol)
		} else {
			summary.Unresolved = err.Error()
		}
		list = append(list, summary)
	}
	return tools.NewToolResult(successJSON(map[string]any{"providers": list}))
}

// representativeProviderRow is the row runtime and the tools treat as a
// provider's entry: the first row carrying a credential reference, else the
// first row. Seed scaffolding rows carry no reference, so the configured row
// wins when both exist. Returns nil when no row names the provider.
func representativeProviderRow(cfg *config.Config, id string) *config.ModelConfig {
	rows := providerRows(cfg, id)
	for _, r := range rows {
		if r.APIKeyRef != "" {
			return r
		}
	}
	if len(rows) > 0 {
		return rows[0]
	}
	return nil
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
	_, known, isLocal := catalogProviderLocality(name)
	if !known {
		return tools.ErrorResult(errorJSON("UNKNOWN_PROVIDER",
			fmt.Sprintf("%q is not a known provider id", name),
			"Use the catalog id (e.g. 'amazon-bedrock', not 'bedrock'; 'google', not 'gemini')",
		))
	}
	if !isLocal && !credentialPresent(t.deps.CredStore, ref) {
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
