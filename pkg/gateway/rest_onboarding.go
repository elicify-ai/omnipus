//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"golang.org/x/crypto/bcrypt"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/middleware"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// HandleCompleteOnboarding handles POST /api/v1/onboarding/complete.
//
// Two-phase commit invariant:
//
//	Phase 1 — reservation: ReserveComplete() is called BEFORE safeUpdateConfigJSON.
//	  If onboarding is already complete (or concurrently reserved), it returns
//	  ErrAlreadyComplete and this handler responds with 409 immediately.
//	  The reservation sets an in-memory flag that blocks concurrent callers.
//
//	Phase 2 — commit: After safeUpdateConfigJSON writes config.json successfully,
//	  commit() is called to persist state.json (marking onboarding complete) and
//	  clear the reservation. If safeUpdateConfigJSON fails, ReleaseReservation()
//	  clears the flag so a retry is possible.
//
// This ordering guarantees state.json is NEVER written before config.json,
// preventing the "bricked instance" scenario where state says complete but
// config has no admin user (e.g., disk-full mid-write).
func (a *restAPI) HandleCompleteOnboarding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Phase 1: Reserve the completion slot BEFORE touching config.json.
	// This closes the TOCTOU window: concurrent callers racing through the
	// IsComplete() check all see "already complete" once the first caller
	// holds the reservation, without needing to wait for disk I/O.
	commitOnboarding, reserveErr := a.onboardingMgr.ReserveComplete()
	if reserveErr != nil {
		if errors.Is(reserveErr, onboarding.ErrAlreadyComplete) {
			jsonErr(w, http.StatusConflict, "onboarding already complete")
			return
		}
		slog.Error("onboarding: reserve failed unexpectedly", "error", reserveErr)
		jsonErr(w, http.StatusInternalServerError, "onboarding failed")
		return
	}

	var body gen.OnboardingCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Validate provider.
	if body.Provider.Id == "" {
		jsonErr(w, http.StatusBadRequest, "provider.id is required")
		return
	}
	// Reject unknown protocols at the boundary so the gateway does not persist
	// a config that will fail the post-save rewire and flip to degraded.
	if !providers.IsKnownProtocol(body.Provider.Id) {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("provider.id %q is not a known protocol", body.Provider.Id))
		return
	}
	if body.Provider.ApiKey == "" {
		jsonErr(w, http.StatusBadRequest, "provider.api_key is required")
		return
	}

	// Validate admin.
	if body.Admin.Username == "" {
		jsonErr(w, http.StatusBadRequest, "admin.username is required")
		return
	}
	if body.Admin.Password == "" {
		jsonErr(w, http.StatusBadRequest, "admin.password is required")
		return
	}
	if len(body.Admin.Password) < 8 {
		jsonErr(w, http.StatusBadRequest, "admin.password must be at least 8 characters")
		return
	}

	// Store the API key in the encrypted credentials store (AES-256-GCM).
	// Refuses the operation if the store is locked (SEC-23: no plaintext fallback).
	credRefName, credErr := a.storeCredential(body.Provider.Id+"_API_KEY", body.Provider.ApiKey)
	if credErr != nil {
		a.onboardingMgr.ReleaseReservation()
		slog.Error("rest: credential store unavailable during onboarding", "error", credErr)
		jsonErr(
			w,
			http.StatusServiceUnavailable,
			"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets",
		)
		return
	}

	// Build the provider entry as a JSON object to inject into providers array.
	// model defaults per provider when not specified in the onboarding request.
	var providerModel string
	if body.Provider.Model != nil {
		providerModel = *body.Provider.Model
	}
	if providerModel == "" {
		switch body.Provider.Id {
		case "anthropic":
			providerModel = "claude-sonnet-4-6"
		case "gemini", "google":
			providerModel = "gemini-2.0-flash"
		case "openrouter":
			providerModel = "openai/gpt-4o"
		default: // openai and any other provider
			providerModel = "gpt-4o"
		}
	}
	// model_name is the user-facing alias that agents.defaults.model_name
	// references to resolve a provider entry. It is also what the Agent Profile
	// UI shows as the agent's model. Using the provider ID here (e.g.
	// "openrouter") would display as the agent's model — non-descriptive and
	// inconsistent with seeded entries, which set model_name == model.
	// Use the actual model string so the alias matches what the user picked.
	newProviderEntry := map[string]any{
		"model_name":  providerModel,
		"provider":    body.Provider.Id,
		"model":       providerModel,
		"api_key_ref": credRefName,
	}

	// Pre-compute all expensive crypto operations outside the config lock to
	// avoid holding configMu for ~300ms across three bcrypt operations.
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Admin.Password), bcrypt.DefaultCost)
	if err != nil {
		a.onboardingMgr.ReleaseReservation()
		slog.Error("onboarding: bcrypt password hash failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "onboarding failed")
		return
	}
	token, err := generateUserToken(body.Admin.Username)
	if err != nil {
		a.onboardingMgr.ReleaseReservation()
		slog.Error("onboarding: generate token failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "onboarding failed")
		return
	}
	tokenHash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		a.onboardingMgr.ReleaseReservation()
		slog.Error("onboarding: bcrypt token hash failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "onboarding failed")
		return
	}

	// Phase 2: Write config.json only (no state.json write inside the callback).
	// The commit() closure writes state.json after safeUpdateConfigJSON returns.
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		// The TOCTOU window is now closed by ReserveComplete() above — no need
		// to re-check IsComplete() here. The reserved flag blocks concurrent
		// callers before they can reach this callback.

		// --- Provider ---
		providerList, ok := m["providers"].([]any)
		if !ok {
			if m["providers"] != nil {
				return fmt.Errorf("providers field is not an array: %T", m["providers"])
			}
			providerList = []any{}
		}

		// Check if provider already exists; update or append.
		// Dedup key is the (provider, model) pair. Running onboarding twice with
		// the same model is idempotent; running with a different model from the
		// same provider creates a new entry sharing the api_key_ref.
		found := false
		for i, entry := range providerList {
			entryMap, isMap := entry.(map[string]any)
			if !isMap {
				continue
			}
			if entryMap["provider"] == body.Provider.Id && entryMap["model"] == providerModel {
				// Update existing entry.
				if credRefName != "" {
					entryMap["api_key_ref"] = credRefName
					delete(entryMap, "api_key")
					delete(entryMap, "api_keys")
				} else {
					entryMap["api_key"] = body.Provider.ApiKey
				}
				entryMap["model"] = providerModel
				entryMap["model_name"] = providerModel
				entryMap["provider"] = body.Provider.Id
				providerList[i] = entryMap
				found = true
				break
			}
		}
		if !found {
			providerList = append(providerList, newProviderEntry)
		}
		m["providers"] = providerList

		// --- Set default model ---
		// The actual model the user selected becomes the default agent model.
		// This matches the model_name on the provider entry created above, so
		// the Agent Profile UI and LLM routing both show the model the user
		// picked (not a generic provider alias).
		agentsMap, ok := m["agents"].(map[string]any)
		if !ok {
			agentsMap = map[string]any{}
		}
		defaultsMap, ok := agentsMap["defaults"].(map[string]any)
		if !ok {
			defaultsMap = map[string]any{}
		}
		defaultsMap["model_name"] = providerModel
		agentsMap["defaults"] = defaultsMap
		m["agents"] = agentsMap

		// --- Admin user ---
		// Build the user entry using pre-computed hashes.
		newUser := map[string]any{
			"username":      body.Admin.Username,
			"password_hash": string(passwordHash),
			"token_hash":    string(tokenHash),
			"role":          "admin",
		}

		// Ensure gateway object exists in m.
		if m["gateway"] == nil {
			m["gateway"] = map[string]any{}
		}
		gatewayMap, ok := m["gateway"].(map[string]any)
		if !ok {
			return fmt.Errorf("gateway config is not a map")
		}
		users := make([]any, 0, 1)
		if raw, exists := gatewayMap["users"]; exists {
			var ok bool
			users, ok = raw.([]any)
			if !ok {
				return fmt.Errorf("gateway.users is not an array")
			}
		}
		// Check for duplicate username. If the same admin already exists (e.g.,
		// from a partial commit where config was saved but state.json wasn't),
		// treat as idempotent success: overwrite the hashes so the caller gets
		// a working session with the newly generated token.
		for _, u := range users {
			um, ok := u.(map[string]any)
			if !ok {
				continue
			}
			if um["username"] == body.Admin.Username {
				um["password_hash"] = string(passwordHash)
				um["token_hash"] = string(tokenHash)
				return nil
			}
		}
		users = append(users, newUser)
		gatewayMap["users"] = users
		m["gateway"] = gatewayMap

		// Config mutation only — state.json is written AFTER config.json
		// succeeds (two-phase commit). Do NOT call CompleteOnboarding() here.
		return nil
	}); err != nil {
		// config.json write failed — release the reservation so a retry is possible.
		a.onboardingMgr.ReleaseReservation()
		slog.Error("onboarding: complete transaction failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "onboarding failed")
		return
	}

	// config.json written successfully. Now commit state.json (phase 2).
	// If this fails, the instance is in a recoverable state: next boot
	// will re-enter onboarding, detect the admin user exists, and succeed.
	if err := commitOnboarding(); err != nil {
		slog.Error(
			"onboarding: state.json commit failed (config.json already written — retry will recover)",
			"error", err,
		)
		// Do NOT return an error to the caller — config is committed.
		// The admin user exists and the token is valid.
	}

	// Trigger a reload so the in-memory config picks up the new user.
	// Reload failure is non-fatal — token is on disk and active after next config poll.
	if err := a.awaitReload(); err != nil {
		slog.Warn("onboarding: hot-reload after complete failed; token active after next restart", "error", err)
	}

	// Issue a __Host-csrf cookie so the onboarding client (which up to this
	// point had no cookie — /api/v1/onboarding/complete is exempt from the
	// CSRF gate for exactly that reason, see pkg/gateway/middleware/csrf.go)
	// can make subsequent state-changing requests without a 403. Issue #97.
	if err := middleware.IssueCSRFCookie(w, r); err != nil {
		slog.Error("onboarding: issue CSRF cookie failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "session init failed")
		return
	}

	slog.Info("onboarding: completed", "username", body.Admin.Username)
	resp := gen.OnboardingCompleteResponse{
		Token:    token,
		Role:     gen.OnboardingCompleteResponseRole(config.UserRoleAdmin),
		Username: body.Admin.Username,
	}
	if credRefName == "" {
		warningMsg := "API key stored in plaintext — set OMNIPUS_MASTER_KEY for encrypted storage"
		resp.Warning = &warningMsg
	}
	jsonOK(w, resp)
}

// HandleOnboardingProbeProvider handles POST /api/v1/onboarding/probe-provider.
//
// Purpose: during onboarding the SPA needs to test an API key AND fetch the
// available model list so the user can pick a model — BEFORE onboarding
// completes and BEFORE a __Host-csrf cookie can be issued (the Secure cookie
// cannot install over plain HTTP on non-localhost origins).
//
// The endpoint is CSRF-exempt (see defaultExemptPaths) and non-persistent:
// it accepts the api_key in the request body, uses it to fetch the upstream
// model list, and returns the result. Nothing is written to disk, credentials
// store, or in-memory config. After onboarding completes, this endpoint
// returns 409 — post-onboarding admins use the normal PUT /providers/{id}
// + GET /providers flow (which works because their browser has the cookie
// by then).
//
// Request body:
//
//	{"id":"openrouter","api_key":"sk-or-...","endpoint":"https://openrouter.ai/api/v1"}
//
// `endpoint` is optional; when omitted, the server uses
// providers.GetDefaultAPIBase(id).
//
// Response shape:
//
//	{"success":true,"models":["gpt-4","gpt-4-turbo",...]}     on OK
//	{"success":false,"error":"401 unauthorized"}               on upstream reject
//	(HTTP 409)                                                 after onboarding complete
//	(HTTP 400)                                                 on malformed body / unknown id
func (a *restAPI) HandleOnboardingProbeProvider(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Gate: only usable during bootstrap. Once onboarding is complete the
	// endpoint still exists (CSRF-exempt path can't be removed dynamically)
	// but it refuses to serve — admins with a cookie use the standard
	// /providers/{id} PUT + GET /providers flow instead.
	if a.onboardingMgr != nil && a.onboardingMgr.IsComplete() {
		jsonErr(w, http.StatusConflict,
			"onboarding already complete — use PUT /api/v1/providers/{id} and GET /api/v1/providers to add providers")
		return
	}

	var body gen.ProbeProviderRequest
	var validateEnabled bool
	if a.agentLoop != nil {
		validateEnabled = a.agentLoop.GetConfig().Gateway.ValidateInbound
	}
	if !decodeAndValidate(w, r, "ProbeProviderRequest", &body, validateEnabled) {
		return
	}
	if body.Id == "" {
		jsonErr(w, http.StatusBadRequest, "id is required")
		return
	}
	if body.ApiKey == "" {
		jsonErr(w, http.StatusBadRequest, "api_key is required")
		return
	}

	var baseURL string
	if body.Endpoint != nil {
		baseURL = *body.Endpoint
	}
	if baseURL == "" {
		baseURL = providers.GetDefaultAPIBase(body.Id)
	}
	if baseURL == "" {
		// Unknown provider and caller didn't supply an endpoint — the probe
		// cannot proceed without one.
		jsonErr(w, http.StatusBadRequest,
			fmt.Sprintf("unknown provider %q and no endpoint override supplied", body.Id))
		return
	}

	models, fetchErr := fetchUpstreamModels(baseURL, body.ApiKey)
	if fetchErr != nil {
		// Upstream probe failure is a 200 with success=false — symmetrical
		// with POST /providers/{id}/test, so the SPA's error-handling branch
		// is identical for both flows.
		errMsg := fetchErr.Error()
		jsonOK(w, gen.ProbeProviderResponse{Success: false, Error: &errMsg})
		return
	}

	jsonOK(w, gen.ProbeProviderResponse{Success: true, Models: &models})
}
