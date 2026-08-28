// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_providers_delete.go — DELETE /api/v1/providers/{id} (T068-09,
// ADR-068 FR-010/FR-011/FR-013/FR-042, D14.2).
//
// The verb is dispatched from HandleProviders AFTER the reserved-literal
// case ("catalog" / "default-model" / "model-capabilities" are never
// provider ids — MAJ-002) and gated inline: requireAdminAuthz
// (RequireNotBypass → 503 under dev-mode bypass) wraps the handler, which
// then requires an authenticated user unconditionally (401, no
// pre-onboarding exception — FR-042/MIN-008) and refuses 503 while the
// credential store is locked, BEFORE any change (FR-011).
//
// Under a.configMu the server RECOMPUTES dependents and backs_default (the
// GET /providers values are advisory — MAJ-018), enforces the new_default
// guards, then runs the idempotent steps in order (D14.2): (0) apply
// new_default when the provider backs the default; (1) clear dependent
// agents in the entity store — primaries cleared, never re-pointed, fallback
// entries removed (FR-013); (2) remove the provider row from config.json and
// prune the non-agent settings references, including (2b) the
// ContextSettings.model_overrides rows for the id (cross-spec Q3); (3)
// delete BOTH credentials the row can own — the `<id>_API_KEY` entry and the
// device-code OAuth entry named
// credentials.OAuthEntryName(providers.OAuthVendorID(id)) (openai-chatgpt's
// tokens live under `openai_OAUTH`, ADR-068 FR-007) — treating
// credentials.NotFoundError as success; (4) audit provider.deleted with the
// credential REF NAMES (never the values), the dependents count and any
// default change; (5) trigger a
// reload and wait. A failure at any step answers 500 {deleted:false} leaving
// a retryable state — a second identical DELETE re-runs every step and
// succeeds, and after a completed run no orphaned secret can survive
// (SC-003; T068-10's startup sweep is the belt-and-braces for crashes).
package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// EventProviderDeleted is the audit event emitted once per COMPLETED
// provider deletion (FR-010 step 4; registered in pkg/audit.IsValidEventName).
const EventProviderDeleted = "provider.deleted"

// EventProviderCredentialSwept is the audit event the T068-10 startup sweep
// emits when it removes an orphaned provider secret whose provider row is
// gone — either an `<id>_API_KEY`, or a `<vendor>_OAUTH` device-code grant
// no configured row maps to. Declared here alongside the deletion event that
// shares the credential-name rule; registered in pkg/audit.IsValidEventName
// so the sweep's first emission never trips the unknown-event warn-once.
const EventProviderCredentialSwept = "provider.credential_swept"

// Test seams for the partial-failure contract (TDD row 10a). Nil in
// production; a test sets one to inject a failure at the corresponding step
// and MUST reset it to nil in cleanup. Package-level because the injected
// step runs deep inside the configMu critical section.
var (
	// testHookProviderDeleteConfigWrite runs at the START of step 2's
	// config mutation; a non-nil error aborts the write (nothing persisted).
	testHookProviderDeleteConfigWrite func() error
	// testHookProviderDeleteEntityUpdate runs before EACH dependent agent's
	// entity-store update in step 1; a non-nil error aborts the run.
	testHookProviderDeleteEntityUpdate func(agentID string) error
)

// deleteProvider handles DELETE /api/v1/providers/{id}. The caller has
// already routed reserved literals to 404 and wrapped this in
// requireAdminAuthz (503 under bypass); providerID is the bare path segment.
func (a *restAPI) deleteProvider(w http.ResponseWriter, r *http.Request, providerID string) {
	// FR-042: an authenticated user is required unconditionally — unlike PUT
	// /providers/{id} there is NO pre-onboarding exception (a fresh install
	// has nothing to delete; the wizard only creates).
	if r.Context().Value(UserContextKey{}) == nil {
		jsonErr(w, http.StatusUnauthorized, "authentication required")
		return
	}

	// FR-011: 503 while the credential store is locked, BEFORE any change —
	// step 3 could not complete, so nothing may start.
	if err := a.providerDeleteStoreUsable(); err != nil {
		slog.Warn("rest: provider delete refused: credential store locked",
			"provider_id", providerID, "error", err)
		jsonErr(w, http.StatusServiceUnavailable,
			"credential store locked: set OMNIPUS_MASTER_KEY or unlock before removing providers")
		return
	}

	// Optional body (ProviderDeleteRequest). Strict decode: the contract is
	// additionalProperties:false, so unknown keys are a 400 on shape.
	var req gen.ProviderDeleteRequest
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if decodeErr := dec.Decode(&req); decodeErr != nil {
			jsonErr(w, http.StatusBadRequest,
				fmt.Sprintf("request body does not match ProviderDeleteRequest: %v", decodeErr))
			return
		}
	}

	resp, status, errMsg, errField := a.runProviderDelete(r, providerID, req.NewDefault)
	switch {
	case status == http.StatusOK:
		// Step 5 — reload AND wait, outside the config lock (the same
		// posture as putDefaultModel: the removal must be live, not queued).
		confirmed, reloadErr := a.triggerReloadAndWaitOutcome()
		if reloadErr != nil || !confirmed {
			reason := "reload did not confirm within the wait deadline"
			if reloadErr != nil {
				reason = reloadErr.Error()
			}
			slog.Error("rest: provider delete: reload failed", "provider_id", providerID, "reason", reason)
			// FR-010: a failed step answers 500 {deleted:false} — the state
			// is retryable (the row is already gone; a retry reports 404,
			// which tells the operator the removal itself did land).
			resp.Deleted = false
			writeJSON(w, http.StatusInternalServerError, resp)
			return
		}
		jsonOK(w, resp)
	case resp != nil:
		// A step failed mid-run: 500 {deleted:false}, retryable (FR-010).
		writeJSON(w, status, resp)
	case errField != "":
		jsonErrField(w, status, errMsg, errField)
	default:
		jsonErr(w, status, errMsg)
	}
}

// providerDeleteStoreUsable reports whether the credential store can accept
// the step-3 delete. Unlike credentialStoreReady it also checks an INJECTED
// store's lock state — a locked injected store must refuse up front, not
// fail at step 3 after the config already changed.
func (a *restAPI) providerDeleteStoreUsable() error {
	if a.credStore != nil {
		if a.credStore.IsLocked() {
			return fmt.Errorf("credential store locked")
		}
		return nil
	}
	return a.credentialStoreReady()
}

// runProviderDelete recomputes the deletion facts and runs steps 0-4 under
// a.configMu. Returns exactly one of:
//   - resp non-nil, status 200: all steps completed (caller runs step 5);
//   - resp non-nil, status 500: a step failed after the guards passed —
//     the body is the retryable {deleted:false} contract shape;
//   - resp nil, status 4xx: a guard refused; errMsg/errField carry the body.
func (a *restAPI) runProviderDelete(
	r *http.Request, providerID string, newDefault *gen.DefaultModelUpdateRequest,
) (resp *gen.ProviderDeleteResponse, status int, errMsg, errField string) {
	a.configMu.Lock()
	defer a.configMu.Unlock()

	cfg := a.agentLoop.GetConfig()
	if !providerConfigured(cfg, providerID) {
		return nil, http.StatusNotFound, "provider not configured", ""
	}

	// MAJ-018: recompute under the lock — the response is authoritative,
	// the GET /providers values the dialog rendered are advisory.
	dependents := computeProviderDependents(cfg, providerID)
	backsDefault := providerBacksDefault(cfg, providerID)

	if backsDefault {
		if newDefault == nil {
			return nil, http.StatusConflict,
				"provider backs the default model; supply new_default", ""
		}
		if st, msg, field := a.validateProviderDeleteNewDefault(cfg, providerID, newDefault); st != 0 {
			return nil, st, msg, field
		}
	}

	oldPair := cfg.Agents.Defaults.DefaultModel
	defaultChanged := false
	failed := func() (*gen.ProviderDeleteResponse, int, string, string) {
		return &gen.ProviderDeleteResponse{
			Deleted:        false,
			Dependents:     dependents,
			DefaultChanged: defaultChanged,
		}, http.StatusInternalServerError, "", ""
	}

	// Step 0 — apply new_default BEFORE the removal (the spec orders the
	// default change ahead of the row removal so no moment exists where the
	// default names a missing provider).
	if backsDefault {
		ndProvider := strings.TrimSpace(newDefault.Provider)
		ndModel := strings.TrimSpace(newDefault.Model)
		if err := a.updateConfigJSONLocked(func(m map[string]any) error {
			defaults := ensureMap(m, "agents", "defaults")
			defaults["default_model"] = map[string]any{"provider": ndProvider, "model": ndModel}
			return nil
		}); err != nil {
			slog.Error("rest: provider delete: new_default write failed",
				"provider_id", providerID, "error", err)
			return failed()
		}
		defaultChanged = true
	}

	// Step 1 — clear dependent AGENTS in the entity store (ADR-054: agents
	// are per-entity records, not config.json rows). Primaries are CLEARED,
	// never re-pointed; fallback entries naming the provider are removed
	// (FR-013). Idempotent: a retry finds the references already gone and
	// the mutation is a no-op. Non-agent dependents (settings keys) are
	// pruned with the config write in step 2.
	agentIDs := make(map[string]struct{}, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		agentIDs[cfg.Agents.List[i].ID] = struct{}{}
	}
	store := agentstore.New(a.homePath)
	for _, dep := range dependents {
		if _, isAgent := agentIDs[dep.Id]; !isAgent {
			continue
		}
		if hook := testHookProviderDeleteEntityUpdate; hook != nil {
			if err := hook(dep.Id); err != nil {
				slog.Error("rest: provider delete: entity update failed (injected)",
					"provider_id", providerID, "agent_id", dep.Id, "error", err)
				return failed()
			}
		}
		if _, err := store.Update(dep.Id, func(rec *config.AgentConfig) error {
			clearAgentProviderRefs(cfg, rec, providerID)
			return nil
		}); err != nil {
			slog.Error("rest: provider delete: entity update failed",
				"provider_id", providerID, "agent_id", dep.Id, "error", err)
			return failed()
		}
	}

	// Step 2 (+2b) — remove the provider row and prune the settings
	// references in one atomic config write. Slug→provider resolution uses
	// the PRE-delete cfg snapshot (the row must still exist for
	// ResolveSlugProvider's rungs to identify what routed through it).
	resolvesToProvider := func(slug string) bool {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			return false
		}
		resolved, _ := config.ResolveSlugProvider(cfg, slug)
		return resolved == providerID
	}
	if err := a.updateConfigJSONLocked(func(m map[string]any) error {
		if hook := testHookProviderDeleteConfigWrite; hook != nil {
			if hookErr := hook(); hookErr != nil {
				return hookErr
			}
		}
		// The provider rows (every row carrying this provider identity).
		if list, ok := m["providers"].([]any); ok {
			kept := make([]any, 0, len(list))
			for _, item := range list {
				if row, isMap := item.(map[string]any); isMap {
					if p, _ := row["provider"].(string); strings.TrimSpace(p) == providerID {
						continue
					}
				}
				kept = append(kept, item)
			}
			m["providers"] = kept
		}
		// Non-agent settings references (the MAJ-010 "every reference"
		// clause): fallback chains are pruned entry-wise; single-value
		// slots are cleared, never re-pointed.
		if agents, ok := m["agents"].(map[string]any); ok {
			if defaults, ok := agents["defaults"].(map[string]any); ok {
				pruneSlugList(defaults, "model_fallbacks", resolvesToProvider)
				pruneSlugList(defaults, "image_model_fallbacks", resolvesToProvider)
				clearSlugValue(defaults, "image_model", resolvesToProvider)
				clearSlugValue(defaults, "recap_model", resolvesToProvider)
				pruneFallbackEntries(defaults, "recap_fallback_models", providerID, resolvesToProvider)
			}
		}
		if voice, ok := m["voice"].(map[string]any); ok {
			clearSlugValue(voice, "model_name", resolvesToProvider)
		}
		// Step 2b — ContextSettings.model_overrides rows for the id
		// (cross-spec Q3; exact pair rows, provider match is enough).
		if ctxSettings, ok := m["context"].(map[string]any); ok {
			if list, ok := ctxSettings["model_overrides"].([]any); ok {
				kept := make([]any, 0, len(list))
				for _, item := range list {
					if row, isMap := item.(map[string]any); isMap {
						if p, _ := row["provider"].(string); strings.TrimSpace(p) == providerID {
							continue
						}
					}
					kept = append(kept, item)
				}
				ctxSettings["model_overrides"] = kept
			}
		}
		return nil
	}); err != nil {
		slog.Error("rest: provider delete: config write failed",
			"provider_id", providerID, "error", err)
		return failed()
	}

	// Step 3b — evict the provider's entitlement-cache entries (ADR-067
	// FR-021, T067-11). Keyed on the provider id rather than on
	// SHA-256(providerID+":"+ref) so a row whose credential ref changed
	// during the process's lifetime still loses every stale answer.
	a.entitlements.evictProvider(providerID)

	// Step 3 — delete the credentials; absence is success
	// (credentials.NotFoundError is absorbed by removeStoredCredential).
	//
	// BOTH secrets a provider row can own must go, or the confirm does not
	// revoke anything (ADR-068 §9 exit proof #2, "no secret survives the
	// confirm"). Deleting only `<id>_API_KEY` left a signed-in
	// openai-chatgpt row's live access AND refresh token sitting in
	// credentials.json with no UI referencing it any more — an orphaned,
	// unrevokable grant on the operator's real vendor account.
	//
	// The OAuth entry is NOT `<providerID>_OAUTH`: providers.OAuthVendorID
	// maps a route/catalog id to the VENDOR identity its tokens belong to
	// (openai-chatgpt → openai, ADR-068 FR-007), and that mapping is what
	// every writer of the entry uses. Deriving the name any other way here
	// would silently miss the only row that actually has one today.
	//
	// O7: that vendor mapping is many-to-one (openai AND openai-chatgpt
	// both resolve to vendor "openai"), so it must NOT be used to decide
	// which row's DELETE gets to remove the entry — only the row that can
	// actually SOURCE a sign-in for that vendor
	// (providers_pkg.OAuthEntryOwner) may. Deleting the plain "openai"
	// api_key row must never destroy a still-configured "openai-chatgpt"
	// row's live ChatGPT grant; oauthRef stays "" (nothing to remove) when
	// providerID isn't the owner.
	credRef := providerID + "_API_KEY"
	refsToDelete := []string{credRef}
	var oauthRef string
	if providers_pkg.OAuthEntryOwner(providerID) {
		oauthRef = credentials.OAuthEntryName(providers_pkg.OAuthVendorID(providerID))
		refsToDelete = append(refsToDelete, oauthRef)
	}
	for _, ref := range refsToDelete {
		if err := a.removeStoredCredential(ref); err != nil {
			slog.Error("rest: provider delete: credential delete failed",
				"provider_id", providerID, "credential_ref", ref, "error", err)
			return failed()
		}
	}

	// Step 4 — audit with the ref NAME, never the value (MAJ-016).
	// Best-effort like every other gateway audit emission: losing the log
	// line must not strand a half-deleted retryable state that is actually
	// complete.
	//
	// O6: actor + source_ip, matching the shape auditCopilotProbe
	// (rest_signin_copilot.go) already uses. This route is reachable during
	// the FR-050 pre-auth window (like every /providers/{id}/sign-in
	// sibling), so an entry with neither was indistinguishable from an
	// authenticated admin's own deletion — the operator investigating "who
	// deleted my provider" (O7's exact scenario, where the consequence can
	// be losing a live OAuth grant) had nothing to go on. auditActor
	// returns "" for an anonymous pre-auth caller, its documented default,
	// never a guess.
	if a.auditor != nil {
		details := map[string]any{
			"provider":        providerID,
			"credential_ref":  credRef,
			"dependents":      len(dependents),
			"default_changed": defaultChanged,
			"source_ip":       a.clientIPWithLiveFallback(r),
		}
		if oauthRef != "" {
			details["oauth_credential_ref"] = oauthRef
		}
		if defaultChanged {
			details["old_default_provider"] = oldPair.Provider
			details["old_default_model"] = oldPair.Model
			details["new_default_provider"] = strings.TrimSpace(newDefault.Provider)
			details["new_default_model"] = strings.TrimSpace(newDefault.Model)
		}
		if err := a.auditor.Log(&audit.Entry{
			Event:    EventProviderDeleted,
			Decision: audit.DecisionAllow,
			User:     auditActor(r),
			Details:  details,
		}); err != nil {
			slog.Warn("audit write failed", "event", EventProviderDeleted, "error", err)
		}
	}

	// The ref name is loggable; the key value never is (SC-003's log check).
	slog.Info("rest: provider removed",
		"provider_id", providerID, "credential_ref", credRef,
		"oauth_credential_ref", oauthRef,
		"dependents", len(dependents), "default_changed", defaultChanged)

	resp = &gen.ProviderDeleteResponse{
		Deleted:        true,
		Dependents:     dependents,
		DefaultChanged: defaultChanged,
	}
	if defaultChanged {
		resp.NewDefault = &gen.DefaultModelUpdateRequest{
			Provider: strings.TrimSpace(newDefault.Provider),
			Model:    strings.TrimSpace(newDefault.Model),
		}
	}
	return resp, http.StatusOK, "", ""
}

// validateProviderDeleteNewDefault enforces the FR-011 new_default rules,
// as refined by MAJ-011: the pair must be non-empty and within the wire
// caps, name a DIFFERENT provider that is CONFIGURED, and — when a served
// catalog is loaded — a provider the catalog knows (unknown-provider rows
// are excluded, Dataset row 8a) with the model in that provider's catalog
// unless the row is custom or local (T068-11's predicate, X-13/X-17/X-22).
// A configured provider in a degraded state (error / expired — its
// credential does not currently resolve) IS accepted: the dialog shows the
// state and proceeding is the operator's risk (Dataset row 14, MAJ-011).
// Returns (0, "", "") when valid.
func (a *restAPI) validateProviderDeleteNewDefault(
	cfg *config.Config, deletingID string, nd *gen.DefaultModelUpdateRequest,
) (int, string, string) {
	provider := strings.TrimSpace(nd.Provider)
	model := strings.TrimSpace(nd.Model)
	switch {
	case provider == "":
		return http.StatusBadRequest, "new_default.provider is required", "new_default"
	case len(provider) > defaultModelProviderMaxLen:
		return http.StatusBadRequest,
			fmt.Sprintf("new_default.provider must be at most %d characters", defaultModelProviderMaxLen),
			"new_default"
	case model == "":
		return http.StatusBadRequest, "new_default.model is required", "new_default"
	case len(model) > defaultModelModelMaxLen:
		return http.StatusBadRequest,
			fmt.Sprintf("new_default.model must be at most %d characters", defaultModelModelMaxLen),
			"new_default"
	case provider == deletingID:
		return http.StatusBadRequest,
			"new_default must name a different provider", "new_default"
	}
	if !providerConfigured(cfg, provider) {
		return http.StatusBadRequest, "new_default provider not configured", "new_default"
	}
	if a.providerCatalog != nil && a.providerCatalog.Document() != nil {
		catRow, known := a.providerCatalog.Provider(provider)
		if !known {
			return http.StatusBadRequest,
				fmt.Sprintf("new_default provider %q is not a known provider", provider), "new_default"
		}
		if !catRow.Custom && catRow.Locality != catalog.LocalityLocal &&
			!a.providerCatalog.Resolve(provider, model).Found() {
			return http.StatusBadRequest,
				"new_default model not in catalog for provider", "new_default"
		}
	}
	return 0, "", ""
}

// clearAgentProviderRefs removes every reference rec holds to providerID:
// the primary (explicit provider, exact-slug, or passthrough-resolved) is
// cleared — never re-pointed — and matching fallback entries are dropped.
// cfg is the PRE-delete config snapshot used for slug resolution. Idempotent.
func clearAgentProviderRefs(cfg *config.Config, rec *config.AgentConfig, providerID string) {
	slugResolves := func(slug string) bool {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			return false
		}
		resolved, _ := config.ResolveSlugProvider(cfg, slug)
		return resolved == providerID
	}
	if rec.Model != nil {
		prov := strings.TrimSpace(rec.Model.Provider)
		slug := strings.TrimSpace(rec.Model.Primary)
		if prov == providerID || (prov == "" && slugResolves(slug)) {
			rec.Model.Primary = ""
			rec.Model.Provider = ""
		}
	}
	if len(rec.FallbackModels) > 0 {
		kept := rec.FallbackModels[:0]
		for _, fb := range rec.FallbackModels {
			prov := strings.TrimSpace(fb.Provider)
			if prov == providerID || (prov == "" && slugResolves(fb.Model)) {
				continue
			}
			kept = append(kept, fb)
		}
		rec.FallbackModels = kept
		if len(rec.FallbackModels) == 0 {
			rec.FallbackModels = nil
		}
	}
}

// pruneSlugList filters a raw JSON []string value at m[key], dropping every
// slug for which drop returns true. An emptied list is removed entirely.
func pruneSlugList(m map[string]any, key string, drop func(string) bool) {
	list, ok := m[key].([]any)
	if !ok {
		return
	}
	kept := make([]any, 0, len(list))
	for _, item := range list {
		if s, isStr := item.(string); isStr && drop(s) {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == 0 {
		delete(m, key)
		return
	}
	m[key] = kept
}

// clearSlugValue deletes m[key] when it is a string the drop predicate
// matches — a single-value model slot is cleared, never re-pointed.
func clearSlugValue(m map[string]any, key string, drop func(string) bool) {
	if s, ok := m[key].(string); ok && drop(s) {
		delete(m, key)
	}
}

// pruneFallbackEntries filters a raw JSON fallback-model list at m[key]
// whose entries are either bare slug strings or {model, provider} objects
// (config.FallbackModelSlice accepts both wire forms). An entry is dropped
// when its explicit provider is providerID, or it has no explicit provider
// and its slug resolves to providerID. An emptied list is removed.
func pruneFallbackEntries(m map[string]any, key, providerID string, drop func(string) bool) {
	list, ok := m[key].([]any)
	if !ok {
		return
	}
	kept := make([]any, 0, len(list))
	for _, item := range list {
		switch v := item.(type) {
		case string:
			if drop(v) {
				continue
			}
		case map[string]any:
			prov, _ := v["provider"].(string)
			slug, _ := v["model"].(string)
			if strings.TrimSpace(prov) == providerID ||
				(strings.TrimSpace(prov) == "" && drop(slug)) {
				continue
			}
		}
		kept = append(kept, item)
	}
	if len(kept) == 0 {
		delete(m, key)
		return
	}
	m[key] = kept
}
