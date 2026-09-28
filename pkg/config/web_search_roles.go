// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Web-search provider roles: the ONE usability test (ADR-096 D4a/D15) and
// the roles migration (ADR-096 D11).
//
// Spec: docs/internal/specs/web-search-provider-model-spec.md — sections
// "Config shape", "Shipped defaults (new install)", "Resolution", "Migration".
// ADR-096 D4, D11, D12, D15.
//
// WHY THE GATE READS THE DISK, NOT MEMORY: loadConfig starts every load from
// DefaultConfig() and unmarshals the file OVER the defaults, so an old
// install's in-memory roles_migrated_at is the SEEDED default timestamp even
// though its file has no such key. A gate on the in-memory marker would skip
// the migration on every upgraded install, permanently. The spec fixes the
// meaning: "a missing key means 'this file has not been migrated'" — the
// FILE's state — so both the gate and the depth-default steps read the raw
// file, not the overlaid struct.

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Provider catalogue ids (ADR-096 D4/D15). The config objects for GLM and
// Baidu stay glm_search / baidu_search — the mapping applySearchIntegration
// already uses — while the role keys carry the catalogue ids glm / baidu.
const (
	SearchProviderPerplexity = "perplexity"
	SearchProviderBrave      = "brave"
	SearchProviderTavily     = "tavily"
	SearchProviderDuckDuckGo = "duckduckgo"
	SearchProviderSearXNG    = "searxng"
	SearchProviderBaidu      = "baidu"
	SearchProviderGLM        = "glm"
	SearchProviderExa        = "exa"

	// SearchProviderNone is the literal fallback value meaning "no
	// fallback". It is a VALUE of FallbackProvider, not a third setting.
	SearchProviderNone = "none"
)

// UsableSearchProvider reports whether provider id is usable RIGHT NOW —
// the one test from the spec's Resolution table, in one place, for every
// consumer (tool resolver, settings screen, migration). Derived from the
// provider catalogue (ADR-096 D15) — one rule per provider shape, never a
// per-id switch:
//
//   - keyed providers (Brave, Tavily, Perplexity, GLM, Baidu, Exa): usable
//     when switched on, and APIKey() is non-empty.
//   - base-URL providers (SearXNG): usable when switched on, and base_url is
//     non-empty after trimming — a whitespace-only base URL is not usable.
//   - plain keyless providers (DuckDuckGo): usable when switched on.
//   - An unknown id is never usable (R9) — and never a boot failure.
//
// APIKey() reads the process environment, so callers must be aware that the
// answer is only meaningful after credentials.InjectFromConfig has run.
func (w *WebToolsConfig) UsableSearchProvider(id string) bool {
	def, ok := SearchProviderDefByID(id)
	if !ok {
		// Unknown ids (including an empty one) are never usable. R9: the
		// default is treated as not usable; never a boot failure.
		return false
	}
	switch {
	case def.Keyed:
		return def.Enabled(w) && def.APIKey(w) != ""
	case def.RequiresBaseURL:
		return def.Enabled(w) && strings.TrimSpace(def.BaseURL(w)) != ""
	default:
		return def.Enabled(w)
	}
}

// webRolesKeyedChainIDs derives the keyed subset of the PRE-ADR selection
// chain — the tie-break order migration step 9 uses when more than one keyed
// provider is switched off with a resolving ref. Derived from the catalogue's
// PreADRChainPos values (D15), so the migration and the catalogue cannot
// drift: a provider absent from the pre-ADR chain (Exa, PreADRChainPos 0)
// never enters it, and DuckDuckGo is absent (keyless, never part of the
// keyed tie-break).
func webRolesKeyedChainIDs() []string {
	var ids []string
	for _, def := range preADRChainDefs() {
		if def.Keyed {
			ids = append(ids, def.ID)
		}
	}
	return ids
}

// preADRChainIDs returns the FULL PRE-ADR selection chain — the order
// pkg/tools/web.go::NewWebSearchTool evaluated before ADR-096 roles
// (Perplexity > Brave > SearXNG > Tavily > DuckDuckGo > Baidu > GLM). The
// migration mirrors it verbatim (D11); Exa is correctly absent (it did not
// exist pre-ADR, so a test-appended catalogue entry must not enter it
// either).
func (w *WebToolsConfig) preADRChainIDs() []string {
	ids := make([]string, 0, len(SearchProviderCatalogue))
	for _, def := range preADRChainDefs() {
		ids = append(ids, def.ID)
	}
	return ids
}

// migrationWinnerID returns the catalogue id of the provider
// pkg/tools/web.go::NewWebSearchTool would construct at this moment — the
// spec's step 1: "including the final DuckDuckGo branch that runs even when
// duckduckgo.enabled is false".
//
// Derived from the catalogue's PreADRChainPos order (D15), evaluated with the
// same UsableSearchProvider test every other consumer uses. The final
// fallback stays a literal DuckDuckGo: it mirrors the live constructor's
// unconditional fallback branch, which is constructor logic — not a
// catalogue property.
func (w *WebToolsConfig) migrationWinnerID() string {
	for _, def := range preADRChainDefs() {
		if w.UsableSearchProvider(def.ID) {
			return def.ID
		}
	}
	// Final branch: DuckDuckGo even when switched off — matching the live
	// constructor's unconditional fallback.
	return SearchProviderDuckDuckGo
}

// isAmbiguousInstall reports whether any keyed provider is switched ON with
// a non-empty api_key_ref whose key does not resolve — the state
// pkg/tools/web.go::enabledButKeylessSearchProviders already detects and
// warns about. The migration DEFERS on this install (spec step 2): recording
// duckduckgo would be wrong for that operator whether the cause is a missing
// key or a broken one, and deferring is safe because the pre-migration path
// still works.
func (w *WebToolsConfig) isAmbiguousInstall() bool {
	for _, id := range webRolesKeyedChainIDs() {
		if w.UsableSearchProvider(id) {
			continue // resolves fine — not ambiguous
		}
		if enabledKeyedWithRef(w, id) {
			return true
		}
	}
	return false
}

// enabledKeyedWithRef reports whether the provider's config object carries
// enabled=true with a non-empty api_key_ref. Derived from the catalogue —
// non-keyed ids (DuckDuckGo, SearXNG) and unknown ids are false.
func enabledKeyedWithRef(w *WebToolsConfig, id string) bool {
	def, ok := SearchProviderDefByID(id)
	return ok && def.Keyed && def.Enabled(w) && def.APIKeyRef(w) != ""
}

// offKeyedWithResolvingRef reports whether the provider is switched OFF with
// a non-empty api_key_ref whose key RESOLVES — the exact predicate migration
// step 9 reads as the operator's choice. It is the MIRROR of the defer
// predicate (enabled ON, key not resolving): reusing one helper for both
// silently inverts the flag condition, which the (e)-install test caught.
// Derived from the catalogue (D15).
func offKeyedWithResolvingRef(w *WebToolsConfig, id string) bool {
	def, ok := SearchProviderDefByID(id)
	return ok && def.Keyed && !def.Enabled(w) && def.APIKeyRef(w) != "" && def.APIKey(w) != ""
}

// MigrateWebSearchRoles runs the ADR-096 D11 roles migration ONCE — when the
// FILE's tools.web section carries no roles_migrated_at — writing
// default_provider / fallback_provider / roles_migrated_at (plus the D12
// depth defaults and the two flag corrections steps 8/9 name) to both the
// in-memory config and config.json on disk.
//
// cfg MUST be the post-injection config (each provider's APIKey() must see
// the process environment), and cfgPath MUST be the config.json path.
// onSelfHeal, when non-nil, receives the exact bytes written — the reload
// path (gateway_reload.go::executeReload) uses it to register the write with
// its configSelfWriteRegistry so the watcher does not reload on our own
// write. The boot path passes nil: the watcher does not exist yet at boot.
//
// Best-effort like its precedent pkg/config/cli_token_migration.go::
// migrateCLITokenOutOfUsers: on a disk-write failure the in-memory config is
// still corrected (runtime behavior is right for this process), the failure
// is logged, and the next boot retries while the marker is still absent on
// disk.
func MigrateWebSearchRoles(cfg *Config, cfgPath string, onSelfHeal SelfHealWriteHook) {
	if cfg == nil || cfgPath == "" {
		return
	}
	web := &cfg.Tools.Web

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// A fresh install has no config.json yet: the defaults already
			// ship the roles and the marker ("the install timestamp"), so
			// there is nothing to migrate and nothing to write.
			return
		}
		logger.WarnF("web-search roles migration could not read config.json; retrying next boot", map[string]any{
			"path":  cfgPath,
			"error": err.Error(),
		})
		return
	}
	var m map[string]any
	if err = json.Unmarshal(raw, &m); err != nil {
		logger.WarnF("web-search roles migration could not parse config.json; retrying next boot", map[string]any{
			"path":  cfgPath,
			"error": err.Error(),
		})
		return
	}
	tools, _ := m["tools"].(map[string]any)
	if tools == nil {
		tools = map[string]any{}
		m["tools"] = tools
	}
	w, _ := tools["web"].(map[string]any)
	if w == nil {
		w = map[string]any{}
		tools["web"] = w
	}

	// THE GATE — on the FILE, not the overlaid struct (see the header
	// comment). An explicit empty marker still counts as unmigrated: only a
	// non-empty string is a completed migration.
	if marker, present := w["roles_migrated_at"].(string); present && marker != "" {
		return
	}

	// Step 2: DEFER on the ambiguous install, evaluated BEFORE any mutation.
	if web.isAmbiguousInstall() {
		logger.WarnCF("config", "web-search roles migration deferred: a keyed search provider is enabled but its key does not resolve; "+
			"recording roles now could cement a broken key; retrying on the next boot", map[string]any{
			"hint": "store the credential or disable the provider; the pre-migration selection path keeps working meanwhile",
		})
		return
	}

	winner := web.migrationWinnerID()

	// Step 9 detection: a keyed provider switched OFF with a key reference
	// that resolves is the operator's choice — applySearchIntegration never
	// set enabled:true for keyed providers, so "ref set, switched off, key
	// resolves" is exactly what the UI's "Set active" button produced.
	// Reading that as a choice of DuckDuckGo would cement a UI bug as
	// operator intent. Where more than one provider is in this state, the
	// old keyed chain order breaks the tie, and the log says which and why.
	step9 := ""
	for _, id := range webRolesKeyedChainIDs() {
		if web.UsableSearchProvider(id) {
			continue
		}
		if offKeyedWithResolvingRef(web, id) {
			step9 = id
			break // first in the old chain order wins the tie
		}
	}

	// In-memory mutations (applied even if the disk write then fails, per
	// the spec's "correct the in-memory config even if the disk write
	// fails").
	web.DefaultProvider = winner
	if step9 != "" {
		web.DefaultProvider = step9
	}
	web.FallbackProvider = SearchProviderNone
	web.RolesMigratedAt = time.Now().UTC().Format(time.RFC3339)

	// Step 8: the winner is the final DuckDuckGo branch and the flag is
	// false — turn it back on and say why.
	step8 := false
	if winner == SearchProviderDuckDuckGo && !web.DuckDuckGo.Enabled {
		web.DuckDuckGo.Enabled = true
		step8 = true
		logger.WarnCF("config", "web-search roles migration: a switched-off DuckDuckGo was the live provider (final fallback branch); turning the flag back on", map[string]any{
			"default_provider": web.DefaultProvider,
		})
	}

	// Step 9 execution: switch the chosen provider on, and say which.
	if step9 != "" {
		setKeyedEnabled(web, step9, true)
		logger.InfoCF("config", "web-search roles migration: a keyed provider with a stored, resolving key was switched off; treating it as the operator's default (the UI's 'Set active' never set enabled:true)", map[string]any{
			"default_provider": step9,
		})
	}

	var tavilyDepth, glmSize string
	written, err := writeMigratedConfig(cfgPath, m, w, web, step9, step8, &tavilyDepth, &glmSize)
	if err != nil {
		logger.WarnF("failed to persist web-search roles migration to config.json; runtime behavior is still correct "+
			"(in-memory state is migrated), but the next boot will retry", map[string]any{
			"path":  cfgPath,
			"error": err.Error(),
		})
		return
	}
	if onSelfHeal != nil {
		onSelfHeal(written)
	}

	// Step 7 logging — after the write, with the values actually written.
	if tavilyDepth != "" {
		logger.InfoCF("config", "web-search roles migration wrote tavily.search_depth", map[string]any{
			"value":  tavilyDepth,
			"reason": "TavilySearchProvider hardcodes search_depth=advanced today; recording advanced preserves today's bill exactly",
		})
	}
	if glmSize != "" {
		logger.InfoCF("config", "web-search roles migration wrote glm_search.content_size", map[string]any{
			"value":  glmSize,
			"reason": "GLMSearchProvider hardcodes content_size=medium today; recording medium preserves today's behavior",
		})
	}
}

// setKeyedEnabled flips one keyed provider's Enabled flag in memory.
// Derived from the catalogue — non-keyed and unknown ids are a silent no-op,
// exactly as the previous per-id switch was.
func setKeyedEnabled(web *WebToolsConfig, id string, on bool) {
	if def, ok := SearchProviderDefByID(id); ok && def.Keyed {
		def.SetEnabled(web, on)
	}
}

// writeMigratedConfig patches the raw-JSON map with the migration's output
// and rewrites config.json atomically — cli_token_migration.go's technique:
// unmarshal into map[string]any, mutate, re-emit with json.MarshalIndent.
// What it preserves is UNMODELLED keys; whitespace and key order are NOT
// preserved (this is not a byte patch).
//
// Depth defaults (step 7) are written only onto objects that exist in the
// FILE — "if there is no tavily object, leave it absent" — and the same
// values are synced into the in-memory struct so memory and disk cannot
// disagree after a successful write. The out-params carry what was written
// so the caller can log with its reason.
func writeMigratedConfig(
	path string,
	m map[string]any,
	w map[string]any,
	web *WebToolsConfig,
	step9ID string,
	step8 bool,
	tavilyDepthOut *string,
	glmSizeOut *string,
) ([]byte, error) {
	w["default_provider"] = web.DefaultProvider
	w["fallback_provider"] = web.FallbackProvider
	w["roles_migrated_at"] = web.RolesMigratedAt

	// Step 7, and its prohibition, stated where a reader looks for it:
	// search_context_size is deliberately NOT written onto perplexity — the
	// field is not sent today and writing one would change the bill.
	if tv, ok := w["tavily"].(map[string]any); ok {
		if _, present := tv["search_depth"]; !present {
			tv["search_depth"] = "advanced"
			web.Tavily.SearchDepth = "advanced"
			*tavilyDepthOut = "advanced"
		}
	}
	if glm, ok := w["glm_search"].(map[string]any); ok {
		if _, present := glm["content_size"]; !present {
			glm["content_size"] = "medium"
			web.GLMSearch.ContentSize = "medium"
			*glmSizeOut = "medium"
		}
	}

	// Steps 8/9: the flag corrections reach the disk too, or the next boot
	// would diverge from this process's in-memory state.
	if step8 {
		ddg, ok := w["duckduckgo"].(map[string]any)
		if !ok {
			ddg = map[string]any{}
			w["duckduckgo"] = ddg
		}
		ddg["enabled"] = true
	}
	if step9ID != "" {
		// The provider's config section comes from the catalogue — the same
		// id→section mapping the Settings save path uses (glm and baidu ids
		// map to glm_search / baidu_search sections).
		def, ok := SearchProviderDefByID(step9ID)
		if ok && def.Keyed && def.Section != "" {
			obj, exists := w[def.Section].(map[string]any)
			if !exists {
				obj = map[string]any{}
				w[def.Section] = obj
			}
			obj["enabled"] = true
		}
	}

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serialize config for web-roles migration: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, out, 0o600); err != nil {
		return nil, fmt.Errorf("write config for web-roles migration: %w", err)
	}
	return out, nil
}
