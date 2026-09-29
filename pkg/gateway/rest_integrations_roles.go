// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — ADR-096 integration roles on the REST wire.
//
// This file carries the gateway half of the web-search provider model:
//
//   - GET resolves the stored roles through the ONE resolver
//     (pkg/tools/web_search.go::ResolveSearchRoleSnapshot, FR-035) and puts
//     them on the wire (default_search, fallback_search,
//     fallback_ignored_reason, per-row usable / fallback / fallback_automatic,
//     native_search_in_effect per FR-031).
//   - PUT writes roles through the raw-map patch (absent stays absent, "none"
//     stays "none"), stamps the migration marker on any role write so the
//     roles migration can never overwrite an operator choice, and — FR-033 —
//     makes the save live through the reload pipeline, judging a keyed
//     default's readiness AFTER the reload, from post-reload state.
//
// Oracle for this file: docs/internal/specs/web-search-provider-model-spec.md
// (Settings screen, Contract shape, Resolution R1–R9 + save rules, FR-028,
// FR-031, FR-033, BDD scenarios) and the contract schemas
// contracts/components/schemas/IntegrationProvider*.yaml.
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// GET: the resolved roles on the wire
// ---------------------------------------------------------------------------

// integrationRolesFileState is what the read path needs from config.json.
type integrationRolesFileState struct {
	// decided is true when the file carries a non-empty roles_migrated_at —
	// the roles migration ran on this install and its output (or an operator
	// write through Settings) decided the roles. A missing config.json is a
	// fresh install: the shipped defaults ARE the decision (the defaults
	// carry the marker), so it is decided there too. Only an existing file
	// without the marker is undecided (the migration has not run on it — it
	// deferred on an ambiguous install, or the file predates ADR-096).
	decided bool
	// web is the RAW tools.web map — what distinguishes absent from "none"
	// (the defaults overlay masks absence in memory, so raw-file reads are
	// the only honest source for the decided-gate and for R3's absent state).
	web map[string]any
	// rawDefault / rawFallback are the file's role strings, "" when absent.
	rawDefault  string
	rawFallback string
}

// readIntegrationRolesFileState reads config.json for the raw role state.
// The file is only ever written atomically (fileutil.WriteFileAtomic), so a
// concurrent PUT yields the pre- or post-write snapshot — never a torn file.
func (a *restAPI) readIntegrationRolesFileState() integrationRolesFileState {
	var state integrationRolesFileState
	raw, err := os.ReadFile(a.configPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Fresh install: the shipped defaults are the decision.
			state.decided = true
			return state
		}
		// Unreadable file (permissions): undecided is the safe shape — the
		// payload then claims no roles, and rows report usable only.
		slog.Warn("integrations: could not read config.json for role state",
			"path", a.configPath(), "error", err)
		return state
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		slog.Warn("integrations: could not parse config.json for role state",
			"path", a.configPath(), "error", err)
		return state
	}
	toolsMap, _ := m["tools"].(map[string]any)
	web, _ := toolsMap["web"].(map[string]any)
	// THE DECIDED-GATE — on the FILE, not the overlaid struct: in-memory
	// RolesMigratedAt is always seeded by the defaults overlay, so only the
	// file tells a migrated install from a pre-ADR-096 / deferred one.
	if web != nil {
		if marker, ok := web["roles_migrated_at"].(string); ok && marker != "" {
			state.decided = true
		}
	}
	state.web = web
	state.rawDefault = webRoleString(web, "default_provider")
	state.rawFallback = webRoleString(web, "fallback_provider")
	return state
}

// webRoleString extracts a string value from the raw web map, "" when absent
// or not a string.
func webRoleString(web map[string]any, key string) string {
	if web == nil {
		return ""
	}
	v, _ := web[key].(string)
	return v
}

// searchProviderEnabled reports only the saved on/off posture. The fallback
// save rule checks this before writing, while key/prerequisite usability is
// judged after reload per FR-033.
func searchProviderEnabled(web *config.WebToolsConfig, id string) bool {
	if web == nil {
		return false
	}
	// Derived from the one catalogue (FR-035), like every other per-provider
	// lookup in this file: a provider added there is checked here with no edit.
	def, ok := config.SearchProviderDefByID(id)
	return ok && def.Enabled(web)
}

// integrationRoleSnapshot resolves the roles through the ONE resolver
// (FR-035): the raw-file role strings (absent → "") ride on top of a fresh
// load's provider sections, so R3's absent-state survives the defaults
// overlay that would otherwise mask it as "none".
func (a *restAPI) integrationRoleSnapshot(state integrationRolesFileState) (tools.SearchRoleSnapshot, bool) {
	fresh, err := config.LoadConfig(a.configPath())
	if err != nil {
		slog.Error("integrations: could not load config for role resolution",
			"path", a.configPath(), "error", err)
		return tools.SearchRoleSnapshot{}, false
	}
	web := fresh.Tools.Web
	if state.web != nil {
		// Raw-file role strings win over the seeded overlay: an absent
		// fallback_provider must stay ABSENT ("" → R3 auto-DuckDuckGo), not
		// become the seeded "none" (R2). Provider sections (enabled flags,
		// refs, base_url) come from the fresh load, exactly as the runtime
		// ladder would see them.
		web.DefaultProvider = state.rawDefault
		web.FallbackProvider = state.rawFallback
	}
	return tools.ResolveSearchRoleSnapshot(&web), state.decided
}

// nativeSearchInEffect is the FR-031 predicate: prefer_native is set and the
// active model's provider reports native search. Any failure to determine
// the active row resolves the flag to false — the payload's conservative
// default, since the field must always be present.
func (a *restAPI) nativeSearchInEffect(cfg *config.Config) bool {
	if cfg == nil || !cfg.Tools.Web.PreferNative {
		return false
	}
	dm := cfg.Agents.Defaults.DefaultModel
	if dm.Model == "" {
		return false
	}
	row, err := cfg.FindModelConfigBySlug(dm.Model)
	if err != nil || row == nil {
		return false
	}
	if dm.Provider != "" && row.Provider != dm.Provider {
		return false
	}
	provider, _, err := providers.CreateProviderFromConfig(row)
	if err != nil {
		return false
	}
	if nc, ok := provider.(providers.NativeSearchCapable); ok {
		return nc.SupportsNativeSearch()
	}
	return false
}

// buildIntegrationResponse computes the live provider catalog: configured (a
// key is present in the store), usable (enabled + key resolves, ADR-096),
// active (the stored default, only when usable — FR-028), fallback (the
// resolved fallback), plus the top-level role fields. Voice rows keep
// today's shape: active only — usable/fallback/fallback_automatic are
// "Search rows only" per the contract.
//
// The bool return is the decided-gate (file marker), so the response writer
// can apply the contract's null-vs-absent rule for fallback_search without
// re-reading the state.
func (a *restAPI) buildIntegrationResponse(cfg *config.Config) (gen.IntegrationProvidersResponse, bool) {
	state := a.readIntegrationRolesFileState()
	snapshot, decided := a.integrationRoleSnapshot(state)
	activeVoice := a.activeVoiceProviderID(cfg)

	var resp gen.IntegrationProvidersResponse
	resp.Search = []gen.IntegrationProvider{}
	resp.Voice = []gen.IntegrationProvider{}

	for _, d := range integrationCatalogue() {
		entry := gen.IntegrationProvider{
			Id:          gen.IntegrationProviderId(d.id),
			Kind:        gen.IntegrationProviderKind(d.kind),
			DisplayName: d.displayName,
			Configured:  a.integrationConfigured(cfg, d),
			RequiresKey: d.requiresKey,
		}
		if d.kind == "search" {
			usable := snapshot.Usable[d.id]
			active := decided && snapshot.DefaultID == d.id && usable
			isFallback := decided && snapshot.FallbackID == d.id
			fallbackAuto := isFallback && snapshot.FallbackAutomatic
			entry.Usable = &usable
			entry.Active = &active
			entry.Fallback = &isFallback
			entry.FallbackAutomatic = &fallbackAuto
			// Only Tavily's depth cap is surfaced in this response. Providers
			// without a depth axis, and voice rows, omit the optional field.
			if def, ok := config.SearchProviderDefByID(d.id); ok && def.HonoursDepth && d.id == config.SearchProviderTavily {
				depth := cfg.Tools.Web.Tavily.SearchDepth
				if depth == "" {
					depth = "advanced" // The same legacy-empty cap as TavilySearchProvider.effectiveDepth.
				}
				entry.SearchDepthCap = &depth
			}
			resp.Search = append(resp.Search, entry)
		} else {
			active := d.id == activeVoice
			entry.Active = &active
			resp.Voice = append(resp.Voice, entry)
		}
	}
	if decided {
		if snapshot.DefaultID != "" {
			id := snapshot.DefaultID
			resp.DefaultSearch = &id
			// active_search mirrors default_search (contract comment on
			// ActiveSearch) — same stored id, no chain derivation.
			resp.ActiveSearch = &id
		}
		if snapshot.FallbackID != "" {
			id := snapshot.FallbackID
			resp.FallbackSearch = &id
		}
		if snapshot.IgnoredSameAsDefault {
			reason := "same_as_default"
			resp.FallbackIgnoredReason = &reason
		}
	}
	inEffect := a.nativeSearchInEffect(cfg)
	resp.NativeSearchInEffect = &inEffect
	return resp, decided
}

// writeIntegrationResponse writes the integration catalog response,
// applying the one wire rule the generated struct cannot express: with the
// roles decided and no fallback, the contract demands the key PRESENT with
// JSON null — "No fallback" is an explicit selectable state, and a stored
// absent value with no automatic fallback is the same state — while Go's
// omitempty on the generated *string can only emit the absent key.
// Undecided keeps the key absent ("Absent when the roles are not yet
// decided"). This is a documented, minimal post-marshal step; the
// generated wire struct stays untouched (Hard Constraint #8).
func (a *restAPI) writeIntegrationResponse(w http.ResponseWriter, cfg *config.Config) {
	resp, decided := a.buildIntegrationResponse(cfg)
	if !decided || resp.FallbackSearch != nil {
		jsonOK(w, resp)
		return
	}
	// Decided + no fallback: remarshal with the key present as null.
	raw, err := json.Marshal(resp)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not serialize the integration catalog")
		return
	}
	var m map[string]json.RawMessage
	if uerr := json.Unmarshal(raw, &m); uerr != nil {
		jsonErr(w, http.StatusInternalServerError, "could not serialize the integration catalog")
		return
	}
	m["fallback_search"] = json.RawMessage("null")
	raw, err = json.Marshal(m)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "could not serialize the integration catalog")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// ---------------------------------------------------------------------------
// PUT: the live, role-aware save (FR-033)
// ---------------------------------------------------------------------------

// integrationRoleWrite is what the PUT decided to persist, derived from the
// request body before any write or reload.
type integrationRoleWrite struct {
	keySet    bool
	setActive bool
	// fallbackSet is true when the body carried a fallback field.
	fallbackSet bool
	// fallbackOn is the fallback field's value when fallbackSet.
	fallbackOn bool
	// materializeFallback is the save rule's materialization: a default-role
	// save with the fallback field ABSENT, the file holding NO
	// fallback_provider key (the absent state), R3 applying
	// (default-after-write ≠ duckduckgo, DuckDuckGo usable), and the operator
	// not picking "No fallback" — writes fallback_provider=duckduckgo so the
	// file leaves the absent state.
	materializeFallback bool
}

// handleIntegrationProviderUpdate handles PUT /integrations/providers/{id}:
// the live, role-aware save (FR-033). Check order: auth → reauth → role
// validations → activation prerequisites → key store → persist → audit →
// reload → post-reload usability judgment → respond from post-reload state.
func (a *restAPI) handleIntegrationProviderUpdate(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Extract {id} from the path: /api/v1/integrations/providers/{id}.
	const prefix = "/api/v1/integrations/providers/"
	id := strings.TrimPrefix(r.URL.Path, prefix)
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") {
		jsonErr(w, http.StatusBadRequest, "provider id is required in the path")
		return
	}
	def, known := integrationDefByID(id)
	if !known {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("unknown integration provider %q", id))
		return
	}

	var body gen.IntegrationProviderUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "IntegrationProviderUpdateRequest", &body, validateEnabled) {
		return
	}
	if string(body.Kind) != def.kind {
		jsonErr(w, http.StatusBadRequest,
			fmt.Sprintf("provider %q is a %q integration, not %q", id, def.kind, body.Kind))
		return
	}

	// Sensitive change → require the re-auth consent token (FR-12.2).
	if !a.requireReAuth(w, r, user.Username) {
		return
	}

	apiKey := ""
	if body.ApiKey != nil {
		apiKey = strings.TrimSpace(*body.ApiKey)
	}
	write := integrationRoleWrite{}

	switch def.kind {
	case "search":
		// Contract (IntegrationProviderUpdateRequest.active): an explicit
		// active:false is rejected — roles move by setting another provider
		// active, they are not unset.
		if body.Active != nil && !*body.Active {
			jsonErr(w, http.StatusBadRequest,
				"active:false is not valid — set another provider active to move the default")
			return
		}
		// Save rejection (scenario "Save rejects a fallback that is the same
		// provider"): one request cannot make the provider both roles.
		if body.Fallback != nil && *body.Fallback && body.Active != nil && *body.Active {
			jsonErr(w, http.StatusBadRequest,
				fmt.Sprintf("%s cannot be the default and the fallback in the same save", def.displayName))
			return
		}
		if body.Fallback != nil {
			write.fallbackSet = true
			write.fallbackOn = *body.Fallback
			if *body.Fallback {
				// The other half of "a provider cannot fall back to itself":
				// the fallback radio on the row that is already the stored
				// default.
				state := a.readIntegrationRolesFileState()
				if state.rawDefault == id {
					jsonErr(w, http.StatusBadRequest,
						fmt.Sprintf("%s is the current default and cannot also be the fallback", def.displayName))
					return
				}
				// Save rejection: the fallback target must already be enabled.
				// Its key/prerequisite usability is intentionally checked only
				// after this request stores its key and reloads (FR-033).
				fresh, err := config.LoadConfig(a.configPath())
				if err != nil {
					jsonErr(w, http.StatusInternalServerError, "could not read the current configuration")
					return
				}
				if !searchProviderEnabled(&fresh.Tools.Web, id) {
					jsonErr(w, http.StatusBadRequest,
						fmt.Sprintf("%s is not enabled and cannot serve as the fallback — switch it on first", def.displayName))
					return
				}
			}
		}
		write.setActive = body.Active != nil && *body.Active
		if write.setActive && body.Fallback == nil {
			// A default-role save with no fallback field consults the file
			// state once, for two rules:
			//
			//   - Save-rule "Same id as default and as fallback | Rejected",
			//     across two requests: a provider may already hold the
			//     fallback role from an earlier save, and active:true on it
			//     would persist default==fallback. An explicit fallback:false
			//     is allowed here because that request removes the old
			//     fallback while assigning the default.
			//   - Materialization: the file sits in the absent state and R3
			//     would apply, so the save writes fallback_provider=
			//     duckduckgo and the file leaves the absent state.
			state := a.readIntegrationRolesFileState()
			if state.rawFallback == id {
				jsonErr(w, http.StatusBadRequest,
					fmt.Sprintf("%s is the current fallback and cannot also be the default", def.displayName))
				return
			}
			if _, present := state.web["fallback_provider"]; !present {
				ddgUsable := false
				if fresh, err := config.LoadConfig(a.configPath()); err == nil {
					ddgUsable = fresh.Tools.Web.UsableSearchProvider(config.SearchProviderDuckDuckGo)
				}
				if id != config.SearchProviderDuckDuckGo && ddgUsable {
					write.materializeFallback = true
				}
			}
		}
	case "voice":
		// Voice rows keep today's shape plus the two new rejections the
		// contract adds: no fallback field at all, and the same
		// active:false rejection as search rows.
		if body.Fallback != nil {
			jsonErr(w, http.StatusBadRequest, "fallback is not valid on a voice row")
			return
		}
		if body.Active != nil && !*body.Active {
			jsonErr(w, http.StatusBadRequest,
				"active:false is not valid — set another provider active to move the role")
			return
		}
		if def.requiresKey && apiKey == "" && body.Active != nil && *body.Active {
			// Voice activation keeps today's key pre-check: activating a
			// keyed transcriber needs a key already (or in this request).
			hasKey, err := a.credentialRefResolves(def.credRef)
			if err != nil {
				jsonErr(w, http.StatusServiceUnavailable, "credential store locked")
				return
			}
			if !hasKey {
				jsonErr(w, http.StatusBadRequest,
					fmt.Sprintf("%s requires an API key before it can be activated", def.displayName))
				return
			}
		}
	}

	// Keyless search providers with prerequisites must have them set
	// before activation.
	if def.kind == "search" && !def.requiresKey && body.Active != nil && *body.Active {
		if ok, reason := a.integrationActivationReady(a.agentLoop.GetConfig(), def); !ok {
			jsonErr(w, http.StatusBadRequest, reason)
			return
		}
	}

	// Store the key (if supplied) in the encrypted credential store BEFORE
	// writing the ref to config.json (SEC-23: no plaintext fallback).
	write.keySet = apiKey != ""
	if write.keySet {
		if def.credRef == "" {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("%s does not accept an API key", def.displayName))
			return
		}
		if _, err := a.storeCredential(def.credRef, apiKey); err != nil {
			slog.Error("integrations: credential store failed", "provider", def.id, "error", err)
			jsonErr(w, http.StatusServiceUnavailable,
				"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets")
			return
		}
	}

	// Persist the role/key writes through the raw-map patch.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		return applyIntegrationRoles(m, def, write)
	}); err != nil {
		slog.Error("integrations: config update failed", "provider", def.id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to save integration config")
		return
	}

	// Audit the change (resource names the integration; values omit secrets).
	if logger := a.agentLoop.AuditLogger(); logger != nil {
		if err := audit.EmitSecuritySettingChange(
			r.Context(), logger, "integrations.provider",
			map[string]any{"provider": def.id},
			map[string]any{
				"provider": def.id, "kind": def.kind, "key_set": write.keySet,
				"active": write.setActive, "fallback_set": write.fallbackSet,
				"fallback": write.fallbackOn,
			},
		); err != nil {
			slog.Error("rest: audit emit integration change failed", "error", err)
		}
	}

	// FR-033: make the save live through the reload pipeline and wait for
	// its outcome. (safeUpdateConfigJSON has already refreshed the gateway's
	// in-memory view; this runs the loop's reload so the tools rewire too.)
	if confirmed, reloadErr := a.triggerReloadAndWaitOutcome(); reloadErr != nil || !confirmed {
		reason := "the reload did not confirm"
		if reloadErr != nil {
			reason = reloadErr.Error()
		}
		slog.Error("integrations: config saved but reload failed", "provider", def.id, "reason", reason)
		jsonErr(w, http.StatusInternalServerError,
			fmt.Sprintf("%s integration saved but config reload failed: %s", def.displayName, reason))
		return
	}

	// FR-033 round-2 rule: a keyed default whose key still does not resolve
	// AFTER the reload is rejected — judged from post-reload state. The
	// persisted write stays; the response names the step that failed.
	postReloadCfg := a.agentLoop.GetConfig()
	if write.setActive && def.requiresKey {
		if !postReloadCfg.Tools.Web.UsableSearchProvider(id) {
			jsonErr(w, http.StatusBadRequest,
				fmt.Sprintf("%s was saved as the default but its API key does not resolve — store an API key for it, then save the default again", def.displayName))
			return
		}
	}
	// The same FR-033 post-reload judgment for the FALLBACK role (G4): a key sent
	// with this request only resolves after the reload above, so the fallback is
	// judged here, not before the write; the write is kept and the 400 names the step.
	if write.fallbackSet && write.fallbackOn && !postReloadCfg.Tools.Web.UsableSearchProvider(id) {
		if def.requiresKey {
			jsonErr(w, http.StatusBadRequest,
				fmt.Sprintf("%s was saved as the fallback but its API key does not resolve — store an API key for it, then save the fallback again", def.displayName))
		} else {
			jsonErr(w, http.StatusBadRequest,
				fmt.Sprintf("%s was saved as the fallback but its prerequisites are not available after reload", def.displayName))
		}
		return
	}

	a.writeIntegrationResponse(w, postReloadCfg)
}

// applyIntegrationRoles dispatches the raw-map mutation by kind: search rows
// take the new role writes; voice rows keep applyVoiceIntegration unchanged.
func applyIntegrationRoles(m map[string]any, def integrationDef, write integrationRoleWrite) error {
	switch def.kind {
	case "search":
		return applySearchIntegrationRoles(m, def, write)
	case "voice":
		keySet := write.keySet
		makeActive := write.setActive
		return applyVoiceIntegration(m, def, keySet, makeActive)
	default:
		return fmt.Errorf("unknown integration kind %q", def.kind)
	}
}

// applySearchIntegrationRoles patches tools.web in the raw config map:
//
//   - a stored key writes this provider's api_key_ref AND switches that
//     provider's own section on (enabled:true) — founder decision
//     2026-09-29, "Key save switches on": otherwise a keyed provider could
//     never become usable from the Settings screen, since the only other
//     place enabled was ever set true is a default-role save, which the
//     screen will not offer for a provider the catalogue does not yet
//     report usable. No other provider's ref or enabled flag is ever
//     touched (FR-005, extended by the founder decision to enabled too);
//   - a key-only save (no active, no fallback) assigns no role
//     (default_provider / fallback_provider stay untouched) and does not
//     stamp roles_migrated_at;
//   - the default role writes default_provider and switches the provider on
//     (the spec's "Setting the default sets enabled:true");
//   - the fallback role writes the id, or the literal "none" for "No
//     fallback";
//   - materializeFallback writes fallback_provider=duckduckgo (see
//     integrationRoleWrite.materializeFallback);
//   - any role write stamps roles_migrated_at so the roles migration can
//     never overwrite an operator choice (a key-only save stamps nothing).
func applySearchIntegrationRoles(m map[string]any, def integrationDef, write integrationRoleWrite) error {
	toolsMap := mapChild(m, "tools")
	web := mapChild(toolsMap, "web")

	if write.keySet && def.credRef != "" {
		if sec, ok := searchRefSectionByID(def.id); ok {
			section := mapChild(web, sec)
			section["api_key_ref"] = def.credRef
			// Founder decision 2026-09-29 ("Key save switches on"): saving a
			// key for a keyed provider also enables that provider's own
			// section. This does not assign a role and does not stamp
			// roles_migrated_at — those stay governed by write.setActive /
			// write.fallbackSet below.
			section["enabled"] = true
		}
	}

	roleWrite := false
	if write.setActive {
		roleWrite = true
		web["default_provider"] = def.id
		switch def.id {
		case "duckduckgo":
			mapChild(web, "duckduckgo")["enabled"] = true
		default:
			if sec, ok := searchRefSectionByID(def.id); ok {
				mapChild(web, sec)["enabled"] = true
			}
		}
		if write.materializeFallback {
			web["fallback_provider"] = config.SearchProviderDuckDuckGo
		}
	}
	if write.fallbackSet {
		roleWrite = true
		if write.fallbackOn {
			web["fallback_provider"] = def.id
		} else {
			web["fallback_provider"] = config.SearchProviderNone
		}
	}
	if roleWrite {
		web["roles_migrated_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	return nil
}
