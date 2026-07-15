//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

var usernameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$`)

const usernameInvalidMsg = `username must start with an alphanumeric and contain only letters, digits, dots, dashes, and underscores (length 2-63)`

// reservedUsernames blocks registration of usernames that collide with a
// synthetic auth principal the gateway constructs internally rather than
// reading from a Gateway.Users row. "cli" is the identity checkBearerAuth /
// authenticateWS / withOptionalAuth synthesize for a caller presenting the
// machine-only Gateway.CLIToken (see GatewayConfig.CLIToken and
// CLITokenContextKey doc comments). If a human were allowed to register as
// "cli", their real Gateway.Users row would share a username with that
// synthetic identity, and a username-keyed lookup elsewhere could silently
// match the wrong account.
//
// "admin" and "system" are deliberately NOT reserved here despite being
// leftover principal names from the pre-single-user RBAC model: unlike
// "cli", nothing in this codebase synthesizes a UserConfig{Username:"admin"}
// or "system" identity, so there is no collision hazard to guard against —
// and "admin" is in practice the single most common username a solo
// operator picks for their own account (it's also what onboarding's own
// test fixtures use throughout). An earlier version of this map reserved
// both, which 400'd real onboarding for anyone naming their account
// "admin" — a regression caught by CI (TestHandleCompleteOnboarding_*
// failing with 400 instead of 200), not by review.
var reservedUsernames = map[string]struct{}{
	"cli": {},
}

// reservedUsernameMsg is returned when an otherwise-valid username collides
// with a name reservedUsernames blocks.
const reservedUsernameMsg = "this username is reserved and cannot be registered"

// issueSessionCookieFn is FR-011's session-cookie issuer, indirected through a
// package-level var (matching middleware.IssueSessionCookie's signature) so
// tests can force a failure (e.g. simulating a disk fault mid-onboarding)
// without needing real filesystem fault injection between the two
// safeUpdateConfigJSON calls in HandleCompleteOnboarding. Production always
// resolves to middleware.IssueSessionCookie.
var issueSessionCookieFn = middleware.IssueSessionCookie

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
	// committed-guard: release the reservation on any early-return path so
	// callers can retry. Set committed=true only after commitOnboarding()
	// succeeds (phase-2 write) to prevent a bricked-onboarding state.
	committed := false
	defer func() {
		if !committed {
			a.onboardingMgr.ReleaseReservation()
		}
	}()

	var body gen.OnboardingCompleteRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "OnboardingCompleteRequest", &body, validateEnabled) {
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
	// Enforce username constraints regardless of ValidateInbound schema validation.
	if !usernameRE.MatchString(body.Admin.Username) {
		jsonErr(w, http.StatusBadRequest, usernameInvalidMsg)
		return
	}
	if _, reserved := reservedUsernames[strings.ToLower(body.Admin.Username)]; reserved {
		jsonErr(w, http.StatusBadRequest, reservedUsernameMsg)
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
	providerModel := ""
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
	// Persist a custom endpoint as api_base when supplied (required for providers
	// with no fixed default base, e.g. azure; also a regional-host override). The
	// runtime factory reads api_base before falling back to GetDefaultAPIBase.
	if body.Provider.Endpoint != nil {
		if ep := strings.TrimSpace(*body.Provider.Endpoint); ep != "" {
			newProviderEntry["api_base"] = ep
		}
	}

	// Pre-compute all expensive crypto operations outside the config lock to
	// avoid holding configMu for ~300ms across three bcrypt operations.
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(body.Admin.Password), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("onboarding: bcrypt password hash failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "onboarding failed")
		return
	}
	token, err := generateUserToken(body.Admin.Username)
	if err != nil {
		slog.Error("onboarding: generate token failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "onboarding failed")
		return
	}
	// SEC-1: bcrypt only the secret body so the ID-tagged token (81 bytes)
	// stays under bcrypt's 72-byte input ceiling; the issued token is stored in
	// the bearer-token SET so later logins append rather than evict.
	tokenHash, err := bcrypt.GenerateFromPassword([]byte(config.TokenSecret(token)), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("onboarding: bcrypt token hash failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "onboarding failed")
		return
	}
	tokenEntry := []any{map[string]any{
		"id":         config.TokenIDFromRaw(token),
		"hash":       string(tokenHash),
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}}

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
				if body.Provider.Endpoint != nil {
					if ep := strings.TrimSpace(*body.Provider.Endpoint); ep != "" {
						entryMap["api_base"] = ep
					}
				}
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
			"tokens":        tokenEntry,
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
				um["tokens"] = tokenEntry
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
		// config.json write failed — defer will release the reservation so a retry is possible.
		slog.Error("onboarding: complete transaction failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "onboarding failed")
		return
	}

	// FR-011: issue the omnipus-session cookie bound to the new admin's
	// username, now that gateway.users has been persisted above (
	// IssueSessionCookie's configMutator locates the user by username and
	// requires it to already exist — see session_cookie.go). Deliberately
	// BEFORE commitOnboarding()/committed=true below: if this fails, we
	// return 500 without ever marking onboarding complete in state.json, and
	// the still-false `committed` lets the deferred ReleaseReservation() run
	// so the client can retry (the mutate closure above is idempotent on a
	// matching username — see the duplicate-username branch). This is the
	// r4 MAJ-003 fix — never return 200 without the cookie.
	if _, err := issueSessionCookieFn(w, r, body.Admin.Username, a.safeUpdateConfigJSON); err != nil {
		slog.Error("onboarding: issue session cookie failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "session init failed")
		return
	}

	// Issue the __Host-csrf cookie here too — BEFORE committed=true — so a
	// cookie-issuance failure fails closed consistently with the session cookie
	// above (never mark onboarding complete; the still-false `committed` lets the
	// deferred ReleaseReservation run so the client can retry cleanly). This used
	// to run AFTER commitOnboarding, so an RNG failure returned 500 for an
	// onboarding that had actually already committed. The onboarding client has
	// had no cookie up to this point (/api/v1/onboarding/complete is CSRF-exempt
	// for exactly that reason — see pkg/gateway/middleware/csrf.go), so it needs
	// this to make subsequent state-changing requests without a 403. Issue #97.
	if err := middleware.IssueCSRFCookie(w, r); err != nil {
		slog.Error("onboarding: issue CSRF cookie failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "session init failed")
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
	// Phase-2 complete: config.json is committed. Mark committed so the defer does NOT release the reservation.
	// Note: state.json may have failed above (logged as non-fatal) — the process-level reservation correctly
	// stays held since config.json represents the canonical commit.
	committed = true

	// Trigger a reload so the in-memory config picks up the new user.
	// Reload failure is non-fatal — token is on disk and active after next config poll.
	if err := a.triggerReloadAndWait(); err != nil {
		slog.Warn("onboarding: hot-reload after complete failed; token active after next restart", "error", err)
	}

	slog.Info("onboarding: completed", "username", body.Admin.Username)
	resp := gen.OnboardingCompleteResponse{
		Token:    token,
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
//	{
//	  "success": true,
//	  "models":  ["gpt-4","gpt-4-turbo",...],
//	  "validation": {"outcome":"no_credit","message":"..."}   // present for non-valid outcomes only
//	}
//	{"success":false,"error":"401 unauthorized"}               on upstream reject (outcome==invalid_key)
//	(HTTP 409)                                                 after onboarding complete
//	(HTTP 400)                                                 on malformed body / unknown id
//
// success is the complement of ValidationResult.Blocks() — only invalid_key yields success=false.
// The validation field is present for non-valid probed outcomes (no_credit, unreachable, restricted)
// and absent for valid outcomes and for error responses that short-circuit before the probe.
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

	baseURL := ""
	if body.Endpoint != nil {
		baseURL = *body.Endpoint
	}
	if baseURL == "" {
		baseURL = providers.GetDefaultAPIBase(string(body.Id))
	}
	if baseURL == "" {
		// Unknown provider and caller didn't supply an endpoint — the probe
		// cannot proceed without one.
		jsonErr(w, http.StatusBadRequest,
			fmt.Sprintf("unknown provider %q and no endpoint override supplied", body.Id))
		return
	}

	// SEC-24: this endpoint is CSRF-exempt and reachable pre-onboarding WITHOUT
	// authentication, and baseURL can come from a caller-supplied `endpoint`
	// override. Without this gate an attacker could make the server POST/GET to
	// http://169.254.169.254/... (cloud metadata), http://10.x, or localhost:<port>.
	// Resolve + check the host (with DNS-rebinding protection) before ANY outbound
	// call. Redirect-to-internal is additionally caught by the SSRF-safe client
	// passed into FetchModels / ValidateKey below.
	if a.ssrfChecker != nil {
		if err := a.ssrfChecker.CheckURL(r.Context(), baseURL); err != nil {
			slog.Warn("rest: probe-provider: SSRF blocked endpoint", "error", err)
			jsonOK(w, gen.ProbeProviderResponse{Success: false, Error: ptr("endpoint not allowed")})
			return
		}
	}

	// Fetch the model catalog (behavior-preserving: same as the former fetchUpstreamModels call).
	models, fetchErr := providers.FetchModels(r.Context(), baseURL, body.ApiKey, a.ssrfChk())
	if fetchErr != nil {
		// Upstream catalog fetch failure is a 200 with success=false — symmetrical
		// with POST /providers/{id}/test, so the SPA's error-handling branch
		// is identical for both flows.
		errMsg := fetchErr.Error()
		jsonOK(w, gen.ProbeProviderResponse{Success: false, Error: &errMsg})
		return
	}

	// An empty catalog is not a hard failure (the key is still validated below via a
	// default chat model), but it IS observable: the operator gets no model list to
	// pick from. Surface it as a WARN so an empty-catalog provider doesn't look like a
	// silent success.
	if len(models) == 0 {
		slog.Warn("rest: probe-provider: provider returned no models",
			"provider", body.Id)
	}

	// Auth-validation step: use the centralized providers.ValidateKey to probe the key.
	// Some providers (notably OpenRouter) serve GET /models without authentication, so
	// a 200 from /models does NOT prove the key is valid. The classified outcome is
	// returned in the validation field (R-B). Only InvalidKey blocks (Blocks=true).
	// SEC-16: result.RawDetail is server-debug-only; never sent to the client.
	result := providers.ValidateKey(r.Context(), providers.ValidateInput{
		ProviderID:   string(body.Id),
		ProviderName: providers.DisplayName(string(body.Id)),
		BaseURL:      baseURL,
		APIKey:       body.ApiKey,
		Catalog:      models,
	}, a.ssrfChk())
	slog.Debug("rest: probe-provider: key validation result",
		"provider", body.Id, "outcome", result.Outcome, "detail", result.RawDetail)

	// Build the probe response (R-B / FR-013).
	// validation is present for non-valid probed outcomes only (no_credit, unreachable,
	// restricted) — absent for valid (symmetric with Provider/OperationResult).
	probeResp := gen.ProbeProviderResponse{
		Success: !result.Blocks(),
		Models:  &models,
	}
	if result.Outcome != providers.OutcomeValid {
		outcomeStr := gen.ProbeProviderResponseValidationOutcome(result.Outcome)
		// Guard: only assign the validation object when the cast is a known wire value.
		if !outcomeStr.Valid() {
			slog.Warn("rest: probe-provider: unrecognized validation outcome; omitting validation field",
				"provider", body.Id, "outcome", result.Outcome)
		} else {
			// Assign the generated anonymous field struct directly (matching the two
			// rest.go sites) — binding it to a local first trips the hand-written
			// wire-type linter (Constraint #8).
			probeResp.Validation = &struct {
				Message *string                                    `json:"message,omitempty"`
				Outcome gen.ProbeProviderResponseValidationOutcome `json:"outcome"`
			}{Outcome: outcomeStr}
			if result.Message != "" {
				probeResp.Validation.Message = &result.Message
			}
		}
	}

	if result.Blocks() {
		// InvalidKey — report failure. The curated message is safe to surface (SEC-16).
		probeResp.Models = nil
		probeResp.Error = &result.Message
	}
	jsonOK(w, probeResp)
}
