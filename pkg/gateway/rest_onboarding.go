// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
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

// onboardingAuthMethodErrMsg is the 400 for a missing or unrecognized
// provider.auth_method discriminator.
const onboardingAuthMethodErrMsg = "provider.auth_method is required and must be one of api_key, sign_in"

// onboardingSignInUnsupportedMsg is the 400 for a `sign_in` completion naming
// a provider whose catalog row does not declare `sign_in` in auth_methods —
// the rule OnboardingProviderSignIn.yaml states for its `id`.
const onboardingSignInUnsupportedMsg = "provider does not support sign-in"

// onboardingSignInModelRequiredMsg is the 400 for a `sign_in` completion that
// names no model on a row the catalog has no Recommended-for-chat model for.
// Guessing a vendor default the way the api_key branch does would write a pair
// GetModelConfig cannot resolve (FR-020: the pair is written once, exactly).
const onboardingSignInModelRequiredMsg = "provider.model is required for this provider"

// onboardingProviderChoice is the decode-time normalization of the two
// OnboardingCompleteRequest.provider variants (OnboardingProviderApiKey |
// OnboardingProviderSignIn), so the handler below asks "which auth method"
// once instead of carrying two parallel shapes through 200 lines.
//
// It is NOT a wire type: it is never serialized, never crosses the gateway
// boundary, and carries no json tags. The strict decode that produces it
// happens into the GENERATED variant structs (Constraint #8).
type onboardingProviderChoice struct {
	// AuthMethod is a config.AuthMethod* constant, never the raw wire literal.
	AuthMethod string
	ID         string
	// APIKey is empty on the sign_in variant, which has no such property.
	APIKey   string
	Model    string
	Endpoint string
}

// decodeOnboardingCompleteBody reads the POST /onboarding/complete body,
// peeks provider.auth_method, and strictly decodes the provider member into
// the NAMED generated variant the discriminator selects — never through the
// union wrapper's As*() accessors (the ADR-034 pattern createAgent uses).
// It returns the decoded wrapper (for `admin`) plus the variant normalized
// into onboardingProviderChoice.
//
// Behaviour for api_key bodies is byte-for-byte the pre-ADR-068 one apart
// from the now-required auth_method field: the same 1 MB limit, the same
// "request body is required" / "invalid JSON body" messages, and schema
// validation only when validateEnabled.
//
// The strict decode is what enforces the two variants' disjointness
// unconditionally, independent of ValidateInbound: `api_key` is not a
// property of OnboardingProviderSignIn, so a sign-in body carrying one is a
// 400 naming the field and the schema it violated.
func decodeOnboardingCompleteBody(
	w http.ResponseWriter, r *http.Request, validateEnabled bool,
) (gen.OnboardingCompleteRequest, onboardingProviderChoice, bool) {
	var body gen.OnboardingCompleteRequest
	var choice onboardingProviderChoice

	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return body, choice, false
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		jsonErr(w, http.StatusBadRequest, "request body is required")
		return body, choice, false
	}

	var peek struct { // not-wire-format: decode-only local peek at the discriminator and the raw provider member, never serialized
		Provider struct {
			AuthMethod *string `json:"auth_method"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return body, choice, false
	}
	if peek.Provider.AuthMethod == nil {
		jsonErr(w, http.StatusBadRequest, onboardingAuthMethodErrMsg)
		return body, choice, false
	}
	switch *peek.Provider.AuthMethod {
	case string(gen.OnboardingProviderApiKeyAuthMethodApiKey):
		choice.AuthMethod = config.AuthMethodAPIKey
	case string(gen.OnboardingProviderSignInAuthMethodSignIn):
		choice.AuthMethod = config.AuthMethodSignIn
	default:
		jsonErr(w, http.StatusBadRequest, onboardingAuthMethodErrMsg)
		return body, choice, false
	}

	if validateEnabled {
		// The wrapper twin (inboundschemas/OnboardingCompleteRequest.yaml)
		// carries the same oneOf, so one validation covers admin AND the
		// chosen variant (additionalProperties: false on each).
		if errMsg, serverErr := validateBodyAgainstSchema("OnboardingCompleteRequest", raw); errMsg != "" {
			if serverErr {
				jsonErr(w, http.StatusInternalServerError, "inbound schema unavailable")
			} else {
				jsonErr(w, http.StatusBadRequest,
					fmt.Sprintf("request body does not match schema %s: %s", "OnboardingCompleteRequest", errMsg))
			}
			return body, choice, false
		}
	}

	if err := json.Unmarshal(raw, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return body, choice, false
	}
	var providerRaw struct { // not-wire-format: decode-only local carrier for the raw provider member
		Provider json.RawMessage `json:"provider"`
	}
	if err := json.Unmarshal(raw, &providerRaw); err != nil || len(providerRaw.Provider) == 0 {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return body, choice, false
	}

	// Strict decode of the provider member into the named variant: a field
	// the chosen variant does not carry is rejected 400 unconditionally,
	// independent of ValidateInbound (ADR-034 decodeAgentCreateVariant rule).
	dec := json.NewDecoder(bytes.NewReader(providerRaw.Provider))
	dec.DisallowUnknownFields()
	schemaName := "OnboardingProviderApiKey"
	if choice.AuthMethod == config.AuthMethodSignIn {
		schemaName = "OnboardingProviderSignIn"
	}
	decodeFailed := func(err error) bool {
		if err == nil {
			return false
		}
		if strings.Contains(err.Error(), "unknown field") {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf(
				"field not allowed on provider auth_method %q: %v — see the %s schema",
				*peek.Provider.AuthMethod, err, schemaName))
		} else {
			jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		}
		return true
	}

	if choice.AuthMethod == config.AuthMethodSignIn {
		var variant gen.OnboardingProviderSignIn
		if decodeFailed(dec.Decode(&variant)) {
			return body, choice, false
		}
		choice.ID = variant.Id
		if variant.Model != nil {
			choice.Model = *variant.Model
		}
		if variant.Endpoint != nil {
			choice.Endpoint = *variant.Endpoint
		}
		return body, choice, true
	}

	var variant gen.OnboardingProviderApiKey
	if decodeFailed(dec.Decode(&variant)) {
		return body, choice, false
	}
	choice.ID = variant.Id
	choice.APIKey = variant.ApiKey
	if variant.Model != nil {
		choice.Model = *variant.Model
	}
	if variant.Endpoint != nil {
		choice.Endpoint = *variant.Endpoint
	}
	return body, choice, true
}

// onboardingClosedMsg is the ONE refusal body POST /onboarding/complete emits
// for every closed-window reason — already complete, an authentication
// authority already exists, or the onboarding state is unknown.
//
// It is deliberately identical across all three. The divergent state this
// endpoint's authority gate exists to defend against (users in config.json,
// onboarding.completed=false in state.json) is otherwise invisible to an
// anonymous caller: GET /api/v1/state would report onboarding_complete=false,
// so a reason-specific message here would be the one oracle telling an
// attacker "this instance is in the interesting state". The reason is
// recorded where it belongs — the audit log and the server log.
const onboardingClosedMsg = "onboarding already complete"

// errOnboardingUsernameTaken is the sentinel the config mutation returns when
// the requested admin username already exists in gateway.users. See
// onboardingWindowGate for why this is defence in depth rather than the
// primary control.
var errOnboardingUsernameTaken = errors.New("onboarding: username already exists")

// onboardingWindowGate is the authority gate for POST /onboarding/complete.
// It writes the refusal response and returns false when the request must not
// proceed.
//
// THE DEFECT THIS CLOSES. Until this gate existed, the endpoint gated on the
// onboarding flag ALONE — `onboardingMgr.ReserveComplete()` and nothing else.
// That made it the only pre-auth route that never asked whether an
// authentication authority was already present, which is precisely backwards:
// it is the one route that MINTS authority. Reproduced live on a real binary:
// with a legitimate admin already in config.json and system/state.json's
// onboarding.completed forced back to false, an anonymous POST returned 200,
// appended a second admin to gateway.users and handed the caller its bearer
// token — while every ordinary route on the same instance correctly 401'd.
//
// The divergent state is not theoretical. onboarding.NewManager keeps the
// zero value (OnboardingComplete=false, i.e. "fresh install") on ANY load
// failure — an unparseable state.json is renamed aside and reset. A partial
// write, a disk error, a botched chmod or a restored backup therefore returns
// the flag to false while config.json still holds every user. ADR-068 FR-050
// already hardened the five sign-in routes against exactly this by making
// their gate fail CLOSED; this route never received that treatment.
//
// The fix reuses that same gate rather than writing a parallel check, so
// "the pre-auth window is open" has exactly ONE definition in this codebase
// (preAuthOnboardingWindowOpen, rest_auth.go). Its three signals all apply
// here unchanged:
//
//  1. onboardingStateUnknown — an existing but unreadable/unparseable
//     state.json is "unknown", never "fresh install". This route fails closed
//     on unknown, and that is deliberate: an unreadable state.json is
//     indistinguishable from the attack above, and refusing is recoverable
//     while minting an admin is not. It also cannot break a genuine first
//     run, because a MISSING state.json (what a first launch actually looks
//     like) is not "unknown". The degradation is bounded and self-healing:
//     the manager has already renamed the corrupt file aside, so the next
//     restart sees a missing file and onboarding proceeds normally.
//  2. The onboarding manager must not report completion.
//  3. The instance must have no authentication authority — no configured user
//     and no OMNIPUS_BEARER_TOKEN. This is the signal a corrupt state.json
//     cannot erase, because it lives in config.json and the environment.
//
// Status code: 409, not 401. 401 invites the caller to retry with
// credentials, and no credential makes minting a second admin through the
// first-run wizard legal — an authenticated admin gets the same refusal, and
// this is a state conflict rather than a failed authentication challenge.
// 409 is also already what this endpoint returns for "already complete" and
// already declared in contracts/openapi.yaml, so the refusal needs no new
// wire vocabulary. (A 401 would additionally be read by the SPA as session
// expiry and force a logout — see rest_providers_catalog_test.go.)
func (a *restAPI) onboardingWindowGate(w http.ResponseWriter, r *http.Request) bool {
	if a.preAuthOnboardingWindowOpen(r) {
		return true
	}
	reason := a.onboardingClosedReason(r)
	sourceIP := a.clientIPWithLiveFallback(r)
	slog.Warn("onboarding: refused — the pre-auth onboarding window is closed",
		"reason", reason, "source_ip", sourceIP)
	if a.auditor != nil {
		// No username is recorded: the gate runs before the body is read, on
		// purpose (see HandleCompleteOnboarding phase 0). What matters
		// forensically is that an admin-minting attempt reached a closed
		// window, when, and from where.
		if err := a.auditor.Log(&audit.Entry{
			Event:    audit.EventOnboardingRefused,
			Decision: audit.DecisionDeny,
			Details: map[string]any{
				"reason":    reason,
				"source_ip": sourceIP,
				"route":     "/api/v1/onboarding/complete",
			},
			PolicyRule: "onboarding.complete requires an open pre-auth window " +
				"(readable onboarding state, onboarding not complete, and no existing authentication authority)",
		}); err != nil {
			slog.Warn("audit write failed", "event", audit.EventOnboardingRefused, "error", err)
		}
	}
	jsonErr(w, http.StatusConflict, onboardingClosedMsg)
	return false
}

// onboardingClosedReason labels WHY the window is closed, for the audit log
// and the server log only. It never makes the decision — that is
// preAuthOnboardingWindowOpen's job, and duplicating it here is exactly the
// parallel-definition drift this fix set out to avoid.
func (a *restAPI) onboardingClosedReason(r *http.Request) string {
	switch {
	case a.onboardingStateUnknown:
		return "onboarding_state_unknown"
	case a.onboardingMgr != nil && a.onboardingMgr.IsComplete():
		return "onboarding_already_complete"
	case a.hasAuthenticationAuthority(r):
		return "authentication_authority_exists"
	default:
		// Unreachable while this is only called on the !open branch; a
		// non-empty label is still better than an empty audit field.
		return "window_closed"
	}
}

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
//
// Phase 0 — authority gate: before either phase, the FR-050 pre-auth window
// must be OPEN. See onboardingWindowGate below for why the onboarding flag
// alone was never a sufficient gate on the one route that mints authority.
func (a *restAPI) HandleCompleteOnboarding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Phase 0: refuse outright if this instance already has an authentication
	// authority (or its onboarding state is unknown). Deliberately BEFORE
	// ReserveComplete and before the request body is read at all — an
	// unauthenticated body is attacker-controlled input and must not be parsed
	// ahead of the authorization decision.
	if !a.onboardingWindowGate(w, r) {
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

	// ADR-068 (T068-06): `provider` is a discriminated union on `auth_method`
	// (OnboardingProviderApiKey | OnboardingProviderSignIn). Mirror the ADR-034
	// peek-discriminator pattern of createAgent (rest.go): buffer the body,
	// peek provider.auth_method from the raw JSON, validate against the chosen
	// variant's inbound schema when ValidateInbound is on, then strictly
	// decode the provider into the NAMED variant struct — never through the
	// generated union wrapper's As*() accessors.
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	body, provider, ok := decodeOnboardingCompleteBody(w, r, validateEnabled)
	if !ok {
		return
	}

	// Validate provider.
	if provider.ID == "" {
		jsonErr(w, http.StatusBadRequest, "provider.id is required")
		return
	}
	// Reserved path segments are never provider ids (ADR-068 MAJ-002).
	if isReservedProviderPathSegment(provider.ID) {
		jsonErrField(w, http.StatusBadRequest, fmt.Sprintf("unknown provider %q", provider.ID), "id")
		return
	}
	// Reject ids the catalog does not carry at the boundary, so the gateway
	// never persists a config that fails the post-save rewire and flips to
	// degraded. The message names the id the caller sent and offers NO
	// canonical alternative (ADR-067 FR-015, SC-010).
	//
	// Membership only, deliberately: FR-019's tier gate belongs to the two
	// sites the spec maps it to — PUT /providers/{id} (T067-10) and the
	// onboarding PROBE below (T067-12). This wizard step must not become the
	// third, because it would also make the "no endpoint resolved" skip
	// branch below unreachable — every supported catalog row carries a URL,
	// so the only ids that reach it are the unsupported ones.
	if !providers.IsCatalogProvider(provider.ID) {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("unknown provider %q", provider.ID))
		return
	}
	// The two variants diverge for the first time here: the api_key variant
	// must carry a key, and the sign_in variant must name a row whose catalog
	// entry actually declares sign_in (the rule OnboardingProviderSignIn.yaml
	// states for its `id`). A row with no declared auth_methods at all is not
	// refused — that is a catalog gap, not an operator error.
	if provider.AuthMethod == config.AuthMethodSignIn {
		row, known := providers.CatalogProvider(provider.ID)
		if known && len(row.AuthMethods) > 0 &&
			!catalogOffersAuth(row.AuthMethods, gen.ProbeProviderRequestAuthSignIn) {
			jsonErrField(w, http.StatusBadRequest, onboardingSignInUnsupportedMsg, "id")
			return
		}
	} else if provider.APIKey == "" {
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

	// ── Credential handling, per auth method ────────────────────────────────
	// api_key: probe the key and store it. sign_in: nothing to probe and
	// nothing to store — the vendor CLI's own login is the credential and
	// Omnipus only ever reads it (FR-007), so credRefName stays empty and no
	// key warning can arise.
	credRefName := ""
	keyWarning := ""
	if provider.AuthMethod == config.AuthMethodAPIKey {
		var ok bool
		credRefName, keyWarning, ok = a.validateAndStoreOnboardingKey(w, r, provider)
		if !ok {
			return
		}
	}

	// Build the provider entry as a JSON object to inject into providers array.
	// model defaults per provider when not specified in the onboarding request.
	providerModel := provider.Model
	if providerModel == "" && provider.AuthMethod == config.AuthMethodSignIn {
		// A sign-in row has no vendor api_key default to guess from — its
		// models are whatever the operator's subscription carries — so the
		// fallback is the row's own first Recommended-for-chat catalog model,
		// the SAME pick the probe would have exercised (FR-036). If the
		// catalog offers none, say so rather than write a pair
		// GetModelConfig cannot resolve.
		if row, known := providers.CatalogProvider(provider.ID); known {
			if rec := recommendedProbeModels(row); len(rec) > 0 {
				providerModel = rec[0]
			}
		}
		if providerModel == "" {
			jsonErrField(w, http.StatusBadRequest, onboardingSignInModelRequiredMsg, "model")
			return
		}
	}
	if providerModel == "" {
		switch provider.ID {
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
	// model_name is the row's user-facing display alias (deleted by ADR-067
	// T067-08); the default model itself is the (provider, model) PAIR written
	// below, never this alias. Use the actual model string so the alias matches
	// what the user picked.
	//
	// auth_method is stamped explicitly on both variants (ADR-068 FR-003):
	// the row records HOW it authenticates, so a sign-in row is never
	// mistaken for a key row whose credential went missing.
	newProviderEntry := map[string]any{
		"model_name":  providerModel,
		"provider":    provider.ID,
		"model":       providerModel,
		"auth_method": provider.AuthMethod,
	}
	if credRefName != "" {
		newProviderEntry["api_key_ref"] = credRefName
	}
	// Persist a custom endpoint as api_base when supplied (required for providers
	// with no fixed default base, e.g. azure; also a regional-host override). The
	// runtime factory reads an explicit api_base before falling back to the
	// catalog row's own base URL (ADR-067 FR-012).
	if ep := strings.TrimSpace(provider.Endpoint); ep != "" {
		newProviderEntry["api_base"] = ep
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
			if entryMap["provider"] == provider.ID && entryMap["model"] == providerModel {
				// Update existing entry.
				switch {
				case credRefName != "":
					entryMap["api_key_ref"] = credRefName
					delete(entryMap, "api_key")
					delete(entryMap, "api_keys")
				case provider.AuthMethod == config.AuthMethodSignIn:
					// No credential exists for a sign-in row, so leave none
					// behind — including a stale one from an earlier api_key
					// onboarding of the same pair.
					delete(entryMap, "api_key")
					delete(entryMap, "api_keys")
					delete(entryMap, "api_key_ref")
				default:
					entryMap["api_key"] = provider.APIKey
				}
				entryMap["model"] = providerModel
				entryMap["model_name"] = providerModel
				entryMap["provider"] = provider.ID
				entryMap["auth_method"] = provider.AuthMethod
				if ep := strings.TrimSpace(provider.Endpoint); ep != "" {
					entryMap["api_base"] = ep
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
		// The pair the user picked becomes agents.defaults.default_model
		// (ADR-068 D14.1 / FR-020): written ONCE here, as the exact
		// (provider, model) the provider row above carries, so GetModelConfig
		// resolves it exactly. No boot/reload path rewrites it.
		agentsMap, ok := m["agents"].(map[string]any)
		if !ok {
			agentsMap = map[string]any{}
		}
		defaultsMap, ok := agentsMap["defaults"].(map[string]any)
		if !ok {
			defaultsMap = map[string]any{}
		}
		delete(defaultsMap, "model_name")
		defaultsMap["default_model"] = map[string]any{
			"provider": provider.ID,
			"model":    providerModel,
		}
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
		// Duplicate username: REFUSE. This branch used to treat a name
		// collision as "idempotent success" and overwrite the existing row's
		// password_hash and tokens, on the theory that the only way to reach
		// it was a partial commit by the same operator retrying. It was also
		// a silent account takeover: an anonymous caller who reached this
		// handler and named an EXISTING user replaced that user's password
		// with one of their own choosing — the original password then 401'd
		// and the attacker's worked, with nothing written anywhere to say
		// why. (Confirmed in UAT.)
		//
		// The phase-0 authority gate already makes this unreachable over
		// HTTP: existing users mean existing authority, which closes the
		// window before the body is even read. This stays as defence in
		// depth — creating an admin must never mutate a different account's
		// credentials as a side effect, whatever gate ran upstream.
		//
		// The partial-commit case this branch was written for is not lost:
		// the operator's account and the password they chose are already in
		// config.json (config.json is written before the cookie is issued and
		// before state.json is committed), so they sign in at /auth/login
		// with the credentials they just typed. Only the one-shot bearer
		// token from the 200 response is forfeited, and the session cookie
		// login issues a working session anyway.
		for _, u := range users {
			um, ok := u.(map[string]any)
			if !ok {
				continue
			}
			if um["username"] == body.Admin.Username {
				return errOnboardingUsernameTaken
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
		if errors.Is(err, errOnboardingUsernameTaken) {
			// A name collision is a client-visible conflict, not a server
			// fault: report it as one rather than laundering it into a 500.
			// It shares onboardingClosedMsg for the same anti-oracle reason
			// the gate does — the response must not confirm which usernames
			// already exist on this instance.
			slog.Warn("onboarding: refused — requested admin username already exists",
				"username", body.Admin.Username, "source_ip", a.clientIPWithLiveFallback(r))
			if a.auditor != nil {
				if auditErr := a.auditor.Log(&audit.Entry{
					Event:    audit.EventOnboardingRefused,
					Decision: audit.DecisionDeny,
					Details: map[string]any{
						"reason":    "username_exists",
						"username":  body.Admin.Username,
						"source_ip": a.clientIPWithLiveFallback(r),
						"route":     "/api/v1/onboarding/complete",
					},
					PolicyRule: "onboarding.complete must never overwrite an existing account's credentials",
				}); auditErr != nil {
					slog.Warn("audit write failed", "event", audit.EventOnboardingRefused, "error", auditErr)
				}
			}
			jsonErr(w, http.StatusConflict, onboardingClosedMsg)
			return
		}
		slog.Error("onboarding: complete transaction failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "onboarding failed")
		return
	}

	// SEC-15: the admin account now exists on disk. This is the single moment
	// this product creates an authentication authority out of nothing, and
	// until now it left no audit record at all — UAT found neither the
	// creation nor the password change of the takeover variant anywhere in
	// the log. Emitted here, immediately after the config.json commit and
	// before any of the remaining best-effort steps (cookies, state.json,
	// reload), so the record exists even if one of those subsequently fails.
	// The password and the issued token are never logged; the username, the
	// source IP and the provider the account was created alongside are.
	if a.auditor != nil {
		if auditErr := a.auditor.Log(&audit.Entry{
			Event:    audit.EventOnboardingAdminCreated,
			Decision: audit.DecisionAllow,
			User:     body.Admin.Username,
			Details: map[string]any{
				"username":    body.Admin.Username,
				"source_ip":   a.clientIPWithLiveFallback(r),
				"provider":    provider.ID,
				"auth_method": provider.AuthMethod,
				"route":       "/api/v1/onboarding/complete",
			},
			PolicyRule: "onboarding.complete admitted: pre-auth onboarding window was open " +
				"(no pre-existing authentication authority)",
		}); auditErr != nil {
			slog.Warn("audit write failed", "event", audit.EventOnboardingAdminCreated, "error", auditErr)
		}
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
	if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
		slog.Warn("onboarding: hot-reload after complete failed; token active after next restart", "error", err)
	} else if !confirmed {
		slog.Warn(
			"onboarding: hot-reload after complete did not confirm within the poll window; "+
				"new admin user may not be active until next restart",
			"username", body.Admin.Username,
		)
	}

	slog.Info("onboarding: completed", "username", body.Admin.Username)
	resp := gen.OnboardingCompleteResponse{
		Token:    token,
		Username: body.Admin.Username,
	}
	switch {
	case provider.AuthMethod == config.AuthMethodAPIKey && credRefName == "":
		warningMsg := "API key stored in plaintext — set OMNIPUS_MASTER_KEY for encrypted storage"
		resp.Warning = &warningMsg
	case keyWarning != "":
		// A non-blocking key-validation outcome (no_credit / unreachable /
		// restricted). Surfaced on the existing warning field so the SPA's
		// first-run screen can tell the operator now, rather than letting them
		// discover it on their first message.
		resp.Warning = &keyWarning
	}
	jsonOK(w, resp)
}

// validateAndStoreOnboardingKey is the api_key half of onboarding completion:
// probe the submitted key, then put it in the encrypted credential store. It
// runs for the OnboardingProviderApiKey variant only — the sign_in variant has
// no key to probe and stores no credential at all (ADR-068 FR-007: the vendor
// CLI holds the login and Omnipus never copies it).
//
// Returns (credRefName, keyWarning, ok). ok is false when it has already
// written the response; the caller must return immediately.
func (a *restAPI) validateAndStoreOnboardingKey(
	w http.ResponseWriter, r *http.Request, provider onboardingProviderChoice,
) (string, string, bool) {
	// ── Provider API-key validation ──────────────────────────────────────────
	//
	// Before this block, first-run setup checked only that api_key was non-empty
	// and then stored it verbatim. That made the STRICTEST moment in the product
	// — the one where a wrong key leaves an install whose agent cannot answer a
	// single message — the ONLY place with no key check, while the far less
	// consequential provider EDIT path (PUT /api/v1/providers/{id}, rest.go's
	// keyChanged branch) has probed the key and rejected a bad one all along.
	// The operator's words: "a provider without key or other authentication
	// should not be possible to configure."
	//
	// This deliberately calls the SAME validator as that PUT path and as the CLI
	// wizard (cmd/omnipus/internal/onboard/onboard.go::validateAndResolveKey) —
	// providers.ValidateKey — rather than an onboarding-local check that would
	// drift from them the first time a provider changes its error shape.
	//
	// Placement is deliberate on both sides:
	//   * LAST of the request validations, so a malformed body (bad username,
	//     short password) is still rejected without a billable upstream call.
	//   * BEFORE storeCredential, so a rejected key never reaches the credential
	//     store or config.json.
	//   * AFTER a credential-store readiness check, for the same reason the PUT
	//     path orders it that way: there is no point probing a key we have
	//     nowhere to put, and a locked store must report itself as a locked
	//     store (503) rather than be mistaken for a bad key.
	//
	// ACCEPT/REJECT POLICY — only "the provider told us this key is wrong"
	// blocks. providers.ValidationResult.Blocks() is true for exactly one
	// outcome, invalid_key; no_credit / unreachable / restricted all proceed
	// with a warning. That asymmetry matters more here than anywhere else in
	// the product, because onboarding is the only door in: if a DNS hiccup, a
	// captive portal, a provider 5xx or a 30-second outage could block it, a
	// flaky network would make Omnipus uninstallable — strictly worse than the
	// bad-key bug this check fixes. Fail CLOSED on "we asked and the answer was
	// no"; fail OPEN on "we could not find out".
	//
	// KNOWN, ACCEPTED TRADE-OFF (review finding D8): this probe is now a real,
	// billed upstream call that can take up to ~25s (10s catalog fetch + 15s
	// completion probe) — up from ~1s before this check existed — and that
	// time is spent INSIDE the ReserveComplete()/committed-guard window above,
	// and counts against onboardingCompleteLimiter's 3-requests/minute-per-IP
	// budget (rest_auth.go). Three mistyped keys in a row therefore cost a
	// real minute of lockout, and a double-click mid-probe can surface a
	// spurious 409 "onboarding already complete" instead of a clean retry.
	// This is not fixed here: retuning onboardingCompleteLimiter or adding a
	// fast client-side debounce is a rate-limiting/UX design change with its
	// own blast radius, not a one-line fix alongside a validation-correctness
	// patch, and PUT /providers/{id} already accepts the identical latency
	// profile for the same reason (a probe cannot be both real and instant).
	// Tracked as follow-up, not a blocker for this change.
	if err := a.credentialStoreReady(); err != nil {
		slog.Error("onboarding: credential store unavailable before key validation", "error", err)
		jsonErr(
			w,
			http.StatusServiceUnavailable,
			"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets",
		)
		return "", "", false
	}

	// Resolve what the probe will talk to: the operator's endpoint override if
	// they supplied one, else the vendor default for this protocol.
	probeBase := strings.TrimSpace(provider.Endpoint)
	if probeBase == "" {
		probeBase = providers.APIBaseFor(provider.ID)
	}

	// keyWarning carries a non-blocking validation outcome to the response's
	// existing `warning` field. Empty means "nothing to say".
	keyWarning := ""
	// probeSkipReason is empty when the probe actually ran; otherwise it names
	// why it could not, for the audit trail.
	probeSkipReason := ""
	providerDisplayName := providers.DisplayName(provider.ID)
	if probeBase == "" {
		// A known protocol with no vendor default and no operator-supplied
		// endpoint (azure is the live example — it has no fixed base). There is
		// nothing to probe against, which is "we could not find out", not "the
		// key is bad".
		probeSkipReason = "no endpoint resolved"
		// D4: this is a non-blocking outcome just like `unreachable`, three
		// lines below in the other skip branch — it must be equally visible to
		// the operator, not just to the audit log. Without this, a provider
		// with no fixed default base (azure) always looks EXACTLY like a
		// verified key on the completion screen, which is indistinguishable
		// from the "warning nobody sees" failure mode this whole check exists
		// to avoid.
		keyWarning = fmt.Sprintf(
			"Couldn't verify your %s key: no endpoint is configured for this provider and none was supplied. Continuing with the key as entered.",
			providerDisplayName,
		)
	} else if a.ssrfChecker != nil {
		if err := a.ssrfChecker.CheckURL(r.Context(), probeBase); err != nil {
			// SEC-24: this endpoint is reachable pre-auth and CSRF-exempt, and
			// probeBase can come straight from the request body, so the probe
			// must never be the thing that reaches an internal address. Skipping
			// the outbound call IS the mitigation — we do not additionally reject
			// the request the way PUT /providers/{id} does. PUT can afford a 422
			// because it runs post-auth against an already-persisted api_base;
			// rejecting here would mean an operator running a local model server
			// (Ollama / LM Studio / LiteLLM on 127.0.0.1 — loopback is blocked by
			// default unless allowlisted, see security.NewSSRFChecker) could not
			// finish first-run setup at all. Same principle as the unreachable
			// rule: not being permitted to look is not evidence the key is bad.
			slog.Warn("onboarding: SSRF guard blocked the key-validation probe; proceeding without it",
				"provider", provider.ID, "error", err)
			probeSkipReason = "ssrf blocked"
			// D4: same visibility fix as the branch above. Deliberately does NOT
			// echo probeBase/err — those are already in the WARN log above for an
			// operator/support engineer, and the completion-screen message is
			// user-facing text, not a debug trace (SEC-16 posture: curated,
			// no raw detail on the wire).
			keyWarning = fmt.Sprintf(
				"Couldn't verify your %s key: the configured endpoint isn't reachable for an automatic check (e.g. a local model server). Continuing with the key as entered.",
				providerDisplayName,
			)
		}
	}

	if probeSkipReason == "" {
		// ADR-067 FR-022: NO `GET /models` pre-fetch. provider.ID is gated
		// above to a catalog id, so ValidateKey resolves its probe candidates
		// straight from the registry catalog (the first active, tool-calling
		// text models in document order) and falls through to the next one on
		// a model_not_found answer, at most three times. The live fetch this
		// used to make was a round trip whose answer the catalog already
		// carries — and on a provider whose /models is public it returned a
		// reassuring 200 that said nothing about the key.
		//
		// SEC-16: result.RawDetail is server-debug-only and never leaves this process.
		result := providers.ValidateKey(r.Context(), providers.ValidateInput{
			ProviderID:   provider.ID,
			ProviderName: providerDisplayName,
			BaseURL:      probeBase,
			APIKey:       provider.APIKey,
		}, a.ssrfChk())
		slog.Debug("onboarding: provider key validation result",
			"provider", provider.ID, "outcome", result.Outcome, "detail", result.RawDetail)

		if result.Blocks() {
			// invalid_key — the only blocking outcome. Nothing is stored: the
			// credential-store write and the config.json transaction are both
			// still below this point, and the committed-guard defer above
			// releases the onboarding reservation so the operator can retry
			// immediately with a corrected key.
			//
			// 400 rather than the PUT path's 422 purely because
			// /onboarding/complete's contract (contracts/openapi.yaml) declares
			// 400/409/429/500/503 and not 422, and a rejected api_key is a
			// rejected request field exactly like the checks above it. The
			// decision itself — which outcomes reject — is shared with PUT via
			// Blocks(); only the status code differs.
			jsonErr(w, http.StatusBadRequest, result.Message)
			return "", "", false
		}
		if result.Outcome != providers.OutcomeValid {
			// no_credit / unreachable / restricted: proceed, but do not proceed
			// silently — a first-run that quietly accepts an out-of-credit key is
			// how someone ends up debugging a "broken" install.
			keyWarning = result.Message
			if a.auditor != nil {
				if err := a.auditor.Log(&audit.Entry{
					Event:    "provider_key_validated",
					Decision: audit.DecisionAllow,
					Details: map[string]any{
						"provider": provider.ID,
						"outcome":  string(result.Outcome),
						"action":   "proceeded",
						"source":   "onboarding",
					},
				}); err != nil {
					slog.Warn("audit write failed", "event", "provider_key_validated", "error", err)
				}
			}
		}
	} else if a.auditor != nil {
		// The probe did not run at all. That is a legitimate outcome (see the
		// two branches above) but it must be visible afterwards, so it uses the
		// audit event pkg/audit/audit.go already reserves for exactly this.
		if err := a.auditor.Log(&audit.Entry{
			Event:    "provider_key_validation_skipped",
			Decision: audit.DecisionAllow,
			Details: map[string]any{
				"provider": provider.ID,
				"reason":   probeSkipReason,
				"source":   "onboarding",
			},
		}); err != nil {
			slog.Warn("audit write failed", "event", "provider_key_validation_skipped", "error", err)
		}
	}

	// Store the API key in the encrypted credentials store (AES-256-GCM).
	// Refuses the operation if the store is locked (SEC-23: no plaintext fallback).
	credRefName, credErr := a.storeCredential(provider.ID+"_API_KEY", provider.APIKey)
	if credErr != nil {
		slog.Error("rest: credential store unavailable during onboarding", "error", credErr)
		jsonErr(
			w,
			http.StatusServiceUnavailable,
			"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets",
		)
		return "", "", false
	}

	return credRefName, keyWarning, true
}

// Wire-format limits from contracts/components/schemas/ProbeProviderRequest.yaml,
// enforced in the handler as well as the schema because validate_inbound is
// off by default.
const (
	probeProviderIDMaxLen    = 64
	probeProviderModelMaxLen = 256
)

// catalogOffersAuth reports whether a catalog row's auth_methods include the
// method the probe was asked for.
func catalogOffersAuth(methods []catalog.AuthMethod, want gen.ProbeProviderRequestAuth) bool {
	for _, m := range methods {
		if string(m) == string(want) {
			return true
		}
	}
	return false
}

// probeUnsupportedAuthMsg names the method the provider does not offer, in
// the operator's vocabulary ("sign-in", not the wire literal "sign_in").
func probeUnsupportedAuthMsg(auth gen.ProbeProviderRequestAuth) string {
	if auth == gen.ProbeProviderRequestAuthSignIn {
		return "provider does not support sign-in"
	}
	return "provider does not support api_key"
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
// Request body (ProbeProviderRequest — ADR-067 FR-023 / ADR-068 FR-036):
//
//	{"id":"openrouter","auth":"api_key","api_key":"sk-or-...",
//	 "model":"z-ai/glm-5.2","api_base":"https://…/v1","protocol":"openai-compatible"}
//
// `id` is a FREE STRING (1..64) with no enum and no hand pattern — it is
// valid iff the served catalog carries it, or the caller supplied both
// `api_base` and `protocol` (an operator-named custom row). Anything else is
// 400 `unknown provider "<id>"` on field `id`, echoing the caller's own
// input and NEVER a list of accepted ids: this endpoint is unauthenticated
// pre-onboarding, and a list would hand an attacker a map of the install.
//
// `api_base` is optional for a catalog provider (an override); when omitted
// the catalog row's own base is used, falling back to
// providers.APIBaseFor(id). Whatever resolves is SSRF-checked before any
// outbound call.
//
// `model`, when present, is exercised VERBATIM — the probe is the
// validation, so a slug the catalog has never heard of is answered by the
// upstream, not pre-refused here. Absent → the provider's Recommended-for-
// chat shortlist (release_date desc, ≤3), falling through only on
// model_not_found.
//
// Response shape:
//
//	{
//	  "success": true,
//	  "models":  ["gpt-4","gpt-4-turbo",...],
//	  "probed_model": "z-ai/glm-5.2",                          // the model actually exercised
//	  "validation": {"outcome":"no_credit","message":"..."}   // present for non-valid outcomes only
//	}
//	{"success":false,"error":"401 unauthorized"}               on upstream reject (outcome==invalid_key)
//	(HTTP 409)                                                 after onboarding complete
//	(HTTP 400)                                                 malformed body, unknown id, auth/api_key/model rules
//	(HTTP 422)                                                 api_base refused by the SSRF guard
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
	// ── `id` (ADR-068 FR-036 / ADR-067 FR-023) ──────────────────────────────
	// A free string, `1..64`, with NO enum and NO hand pattern (MIN-011):
	// what makes an id valid is membership in the SERVED CATALOG, decided at
	// runtime, or the operator declaring a custom endpoint. The length cap is
	// enforced here as well as in the schema because validate_inbound is off
	// by default.
	if body.Id == "" {
		jsonErrField(w, http.StatusBadRequest, "id is required", "id")
		return
	}
	if len(body.Id) > probeProviderIDMaxLen {
		jsonErrField(w, http.StatusBadRequest,
			fmt.Sprintf("id exceeds %d characters", probeProviderIDMaxLen), "id")
		return
	}
	// Reserved path segments are never provider ids (ADR-068 MAJ-002): the
	// generic unknown-provider echo, parameterised by the caller's own id
	// with no id list (CRIT-003).
	if isReservedProviderPathSegment(body.Id) {
		jsonErrField(w, http.StatusBadRequest, fmt.Sprintf("unknown provider %q", body.Id), "id")
		return
	}

	// ── `auth` (required, closed set) ───────────────────────────────────────
	if body.Auth == "" {
		jsonErrField(w, http.StatusBadRequest, "auth is required", "auth")
		return
	}
	if !body.Auth.Valid() {
		jsonErrField(w, http.StatusBadRequest,
			fmt.Sprintf("unsupported auth %q", string(body.Auth)), "auth")
		return
	}

	// ── `model` (optional, 1..256, used verbatim) ───────────────────────────
	pickedModel := ""
	if body.Model != nil {
		pickedModel = *body.Model
		if pickedModel == "" {
			jsonErrField(w, http.StatusBadRequest, "model must not be empty", "model")
			return
		}
		if len(pickedModel) > probeProviderModelMaxLen {
			jsonErrField(w, http.StatusBadRequest,
				fmt.Sprintf("model exceeds %d characters", probeProviderModelMaxLen), "model")
			return
		}
	}

	// ── `api_key` — required iff auth is api_key, forbidden with sign_in ────
	apiKey := ""
	if body.ApiKey != nil {
		apiKey = *body.ApiKey
	}
	switch body.Auth {
	case gen.ProbeProviderRequestAuthApiKey:
		if apiKey == "" {
			jsonErrField(w, http.StatusBadRequest, "api_key is required", "api_key")
			return
		}
	default:
		if apiKey != "" {
			jsonErrField(w, http.StatusBadRequest,
				"api_key must not be sent with auth sign_in", "api_key")
			return
		}
	}

	reqAPIBase := ""
	if body.ApiBase != nil {
		reqAPIBase = strings.TrimSpace(*body.ApiBase)
	}
	reqProtocol := ""
	if body.Protocol != nil {
		reqProtocol = string(*body.Protocol)
	}
	// ADR-067 FR-019/FR-023/FR-035 (T067-12): the SAME admission gate
	// PUT /providers/{id} and the CLI wizard apply, against the catalog this
	// process serves. `id` is a free string on the wire (there is no enum any
	// more), so this is the only thing standing between a typo'd id and a
	// probe fired at a URL nobody asked for.
	if _, admitErr := providers.Admit(body.Id, reqAPIBase, reqProtocol); admitErr != nil {
		field := "id"
		if errors.Is(admitErr, providers.ErrUnknownProvider) && reqAPIBase != "" {
			// The id is unknown AND a base was supplied: what is missing is
			// the protocol, so point the SPA at that field (mirrors PUT).
			field = "protocol"
		}
		jsonErrField(w, http.StatusBadRequest, admitErr.Error(), field)
		return
	}

	// ── the provider must OFFER the requested auth method (FR-030) ──────────
	// Catalog data decides this, so it is answerable without any network
	// call: a sign-in-only row asked for an api_key, or a key-only row asked
	// for sign-in, is a 400 that names the method — not a probe that fails
	// later for a reason the operator cannot act on. A custom row is not a
	// catalog row, so it declares no auth methods and is never refused here.
	catalogRow, isCatalogRow := providers.CatalogProvider(body.Id)
	if len(catalogRow.AuthMethods) > 0 && !catalogOffersAuth(catalogRow.AuthMethods, body.Auth) {
		jsonErrField(w, http.StatusBadRequest, probeUnsupportedAuthMsg(body.Auth), "auth")
		return
	}

	baseURL := reqAPIBase
	if baseURL == "" {
		baseURL = providers.APIBaseFor(body.Id)
	}
	if baseURL == "" {
		// Admission passed, so this is a catalog row whose document carries no
		// URL at all — nothing to probe against.
		jsonErr(w, http.StatusBadRequest,
			fmt.Sprintf("unknown provider %q and no endpoint override supplied", body.Id))
		return
	}

	// SEC-24 / MIN-006: this endpoint is CSRF-exempt and reachable
	// pre-onboarding WITHOUT authentication, and baseURL can come from a
	// caller-supplied `api_base`. Without this gate an attacker could make the
	// server POST/GET to http://169.254.169.254/... (cloud metadata),
	// http://10.x, or localhost:<port>. Resolve + check the host (with
	// DNS-rebinding protection) before ANY outbound call. Redirect-to-internal
	// is additionally caught by the SSRF-safe client passed into FetchModels /
	// ValidateKey below.
	//
	// A blocked base is 422, not a 200 with success=false: the request was
	// well-formed and the server REFUSED it — reporting that as "the provider
	// rejected your key" would send the operator hunting for a credential
	// problem that does not exist.
	if a.ssrfChecker != nil {
		if err := a.ssrfChecker.CheckURL(r.Context(), baseURL); err != nil {
			slog.Warn("rest: probe-provider: SSRF blocked endpoint", "error", err)
			jsonErr(w, http.StatusUnprocessableEntity, "provider endpoint not allowed (SSRF guard)")
			return
		}
	}

	// The model list comes from the REGISTRY CATALOG, offline, with zero
	// outbound requests (ADR-067 FR-020/FR-022, US-9.AC1). The `GET /models`
	// pre-fetch that used to run here told us nothing the catalog does not
	// already know, cost a round trip on every keystroke-driven probe, and —
	// on providers whose /models is public, like OpenRouter — reported a 200
	// that proved nothing about the key. An operator-named custom row has no
	// catalog models: it lists none, and the operator types their own slug.
	var models []string
	if isCatalogRow {
		models = catalogModelIDs(catalogRow)
	}

	// An empty list is not a hard failure (the key is still validated below),
	// but it IS observable: the operator gets nothing to pick from. Surface it
	// as a WARN so it does not look like a silent success.
	if len(models) == 0 {
		slog.Warn("rest: probe-provider: provider returned no models",
			"provider", body.Id)
	}

	// ── auth: sign_in (ADR-068 FR-036, CRIT-002) ────────────────────────────
	// Everything above already decided that this provider OFFERS sign-in.
	// What remains is the same two questions the api_key path asks, answered
	// through the vendor's saved login instead of a submitted key: is there a
	// login at all, and does ONE completion with the operator's chosen model
	// succeed.
	if body.Auth == gen.ProbeProviderRequestAuthSignIn {
		result, refusal := a.probeSignIn(r.Context(), catalogRow, isCatalogRow, body.Id, pickedModel, baseURL)
		if refusal != "" {
			jsonErrField(w, http.StatusBadRequest, refusal, "auth")
			return
		}
		writeProbeResult(w, body.Id, result, models)
		return
	}

	// Auth-validation step: use the centralized providers.ValidateKey to probe the key.
	// Some providers (notably OpenRouter) serve GET /models without authentication, so
	// a 200 from /models does NOT prove the key is valid. The classified outcome is
	// returned in the validation field (R-B). Only InvalidKey blocks (Blocks=true).
	// SEC-16: result.RawDetail is server-debug-only; never sent to the client.
	//
	// FR-036 decides WHICH model is exercised, and the answer is never a
	// catalog membership check: an explicit `model` is used verbatim (the
	// probe IS the validation — if the slug is wrong the upstream is the one
	// that says so), and only an absent `model` falls back to the provider's
	// Recommended-for-chat shortlist.
	var probeModels []string
	if isCatalogRow {
		probeModels = recommendedProbeModels(catalogRow)
	}
	if pickedModel != "" {
		probeModels = []string{pickedModel}
	}
	result := providers.ValidateKey(r.Context(), providers.ValidateInput{
		ProviderID:   body.Id,
		ProviderName: providers.DisplayName(body.Id),
		BaseURL:      baseURL,
		APIKey:       apiKey,
		Catalog:      models,
		ProbeModels:  probeModels,
	}, a.ssrfChk())
	slog.Debug("rest: probe-provider: key validation result",
		"provider", body.Id, "outcome", result.Outcome,
		"probed_model", result.ProbedModel, "detail", result.RawDetail)

	writeProbeResult(w, body.Id, result, models)
}

// writeProbeResult renders one ValidationResult as the probe's 200 body
// (R-B / FR-013). Shared by the api_key and sign-in paths so BOTH keep the
// invariant this endpoint's doc comment states: `success` is the complement of
// Blocks(), `probed_model` names the model actually exercised, and `validation`
// is present for non-valid outcomes only (symmetric with Provider /
// OperationResult).
func writeProbeResult(
	w http.ResponseWriter, providerID string, result providers.ValidationResult, models []string,
) {
	probeResp := gen.ProbeProviderResponse{
		Success: !result.Blocks(),
		Models:  &models,
	}
	// FR-029/FR-036: report the model the probe actually exercised, so the
	// SPA can tie the outcome to the exact pick and refuse Finish when the
	// operator changes the model afterwards.
	if result.ProbedModel != "" {
		probeResp.ProbedModel = &result.ProbedModel
	}
	if result.Outcome != providers.OutcomeValid {
		outcomeStr := gen.ProbeProviderResponseValidationOutcome(result.Outcome)
		// Guard: only assign the validation object when the cast is a known wire value.
		if !outcomeStr.Valid() {
			slog.Warn("rest: probe-provider: unrecognized validation outcome; omitting validation field",
				"provider", providerID, "outcome", result.Outcome)
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
		// The credential was rejected — report failure. The curated message is
		// safe to surface (SEC-16).
		probeResp.Models = nil
		probeResp.Error = &result.Message
	}
	jsonOK(w, probeResp)
}

// ── The sign-in probe (ADR-068 FR-036 / FR-029, CRIT-002) ───────────────────

// probeNotSignedInMsg is the ONLY pre-probe refusal a sign-in probe may
// return: there is no saved vendor login on this machine at all. FR-036 is
// explicit that a sign-in probe "400s only when neither is present" — every
// other answer comes from the completion it then runs, exactly as the api_key
// path's does.
const probeNotSignedInMsg = "not signed in"

// probeSignInUnavailableMsg answers a row that declares `sign_in` but offers
// no mechanism this gateway can exercise: no `cli_kind` (a vendor CLI holding
// the login) and no `token_source` (a saved token Omnipus may read). Today
// that is the device-code shape (xai) — its stored-OAuth token and the chat
// client to spend it are separate, unlanded features — and any operator-named
// custom row, which is not a catalog row and so declares no mechanism at all.
//
// It is deliberately NOT "not signed in": whether the operator has an account
// is unknown here, and guessing would send them to sign in again and again
// against a probe that could never go green.
const probeSignInUnavailableMsg = "sign-in cannot be probed for this provider"

// signInProbeTimeout bounds the ONE completion a sign-in probe runs. It is
// wider than probeCompletion's 15s because the CLI mechanisms spawn a real
// vendor binary that boots a runtime before it answers.
const signInProbeTimeout = 90 * time.Second

// signInProbePrompt is the smallest prompt that still forces a real turn.
const signInProbePrompt = "hi"

// probeSignIn runs the `auth: sign_in` half of POST /onboarding/probe-provider.
//
// Which mechanism to use is CATALOG DATA, never a hardcoded id list:
//
//   - `cli_kind` (codex | copilot) — a `protocol: cli` row whose vendor binary
//     holds the login. The probe is one subprocess completion with the chosen
//     model.
//   - `token_source: codex-auth-json` — a row that reuses the Codex CLI's saved
//     access token against its OWN base URL (openai-chatgpt). The probe is one
//     ordinary completion carrying that token, classified by the same
//     providers.ValidateKey the api_key path uses.
//
// Returns (result, refusal). A non-empty refusal is a 400 on field `auth` and
// the result is meaningless; an empty refusal means a completion ran and its
// result is the answer.
func (a *restAPI) probeSignIn(
	ctx context.Context,
	row catalog.Provider, isCatalogRow bool,
	providerID, pickedModel, baseURL string,
) (providers.ValidationResult, string) {
	// FR-036 decides WHICH model is exercised identically for both auth
	// methods: the operator's pick verbatim, absent → the row's first
	// Recommended-for-chat catalog model.
	model := pickedModel
	if model == "" && isCatalogRow {
		if rec := recommendedProbeModels(row); len(rec) > 0 {
			model = rec[0]
		}
	}
	displayName := providers.DisplayName(providerID)

	ctx, cancel := context.WithTimeout(ctx, signInProbeTimeout)
	defer cancel()

	switch {
	case row.CLIKind != "":
		return a.probeSignInCLI(ctx, row, displayName, model)

	case row.TokenSource == catalog.TokenSourceCodexAuthJSON:
		// FR-007: the file is read, never written, refreshed or proxied.
		token, _, _, err := providers.ReadCodexCliCredentials()
		if err != nil {
			slog.Debug("rest: probe-provider: no saved codex login for a token-source row",
				"provider", providerID, "error", err)
			return providers.ValidationResult{}, probeNotSignedInMsg
		}
		var probeModels []string
		if model != "" {
			probeModels = []string{model}
		}
		result := providers.ValidateKey(ctx, providers.ValidateInput{
			ProviderID:   providerID,
			ProviderName: displayName,
			BaseURL:      baseURL,
			APIKey:       token,
			ProbeModels:  probeModels,
		}, a.ssrfChk())
		slog.Debug("rest: probe-provider: sign-in token probe result",
			"provider", providerID, "outcome", result.Outcome,
			"probed_model", result.ProbedModel, "detail", result.RawDetail)
		return result, ""

	default:
		return providers.ValidationResult{}, probeSignInUnavailableMsg
	}
}

// probeSignInCLI is the `cli_kind` half of probeSignIn: confirm a saved login
// exists, then spend exactly one subprocess completion on the chosen model.
//
// The presence check is per-mechanism and comes FIRST, because the two failure
// modes must not be conflated: "you have not signed in" is a 400 on field
// `auth` that tells the operator what to do, while "the vendor answered no to
// this model" is a 200 with success=false that tells them to pick another one.
func (a *restAPI) probeSignInCLI(
	ctx context.Context, row catalog.Provider, displayName, model string,
) (providers.ValidationResult, string) {
	switch row.CLIKind {
	case catalog.CLIKindCodex:
		// The Codex CLI's login IS its auth.json; no auth.json, no login.
		if _, _, _, err := providers.ReadCodexCliCredentials(); err != nil {
			slog.Debug("rest: probe-provider: no saved codex cli login",
				"provider", row.ID, "error", err)
			return providers.ValidationResult{}, probeNotSignedInMsg
		}
	case catalog.CLIKindCopilot:
		// The Copilot CLI stores its token in the system credential store and
		// exposes no status command (see CopilotSignIn's verified-behaviour
		// note), so the only thing knowable WITHOUT spending a request is
		// whether the binary exists at all. Its login state is read off the
		// completion below, via the shared classifier.
		if !providers.CopilotCLIAvailable("") {
			return providers.ValidationResult{}, providers.CopilotCLIMissingHint
		}
	default:
		slog.Warn("rest: probe-provider: catalog row carries an unknown cli_kind",
			"provider", row.ID, "cli_kind", row.CLIKind)
		return providers.ValidationResult{}, probeSignInUnavailableMsg
	}

	prov, err := providers.NewCliProviderForKind(row.CLIKind, "", "")
	if err != nil {
		slog.Warn("rest: probe-provider: no subprocess driver for cli_kind",
			"provider", row.ID, "cli_kind", row.CLIKind, "error", err)
		return providers.ValidationResult{}, probeSignInUnavailableMsg
	}

	_, chatErr := prov.Chat(ctx,
		[]providers.Message{{Role: "user", Content: signInProbePrompt}}, nil, model, nil)
	if chatErr == nil {
		return providers.ValidationResult{Outcome: providers.OutcomeValid, ProbedModel: model}, ""
	}
	slog.Debug("rest: probe-provider: sign-in cli probe failed",
		"provider", row.ID, "cli_kind", row.CLIKind, "model", model, "error", chatErr)

	// Copilot only reveals "no credential" here, after the run — so report it
	// as the sign-in refusal it is rather than as a rejected model. Only an
	// EXPLICIT marker match counts: an unrecognised failure from a CLI that is
	// installed, after a completion on the operator's chosen model, is far
	// likelier to be that model or that plan, and answering it with "not
	// signed in" would send them to re-authenticate against a problem
	// authentication cannot fix (see MatchCopilotSignInFailure).
	if row.CLIKind == catalog.CLIKindCopilot {
		if state, matched := providers.MatchCopilotSignInFailure(chatErr.Error()); matched &&
			(state == providers.CopilotNotSignedIn || state == providers.CopilotSignInExpired) {
			return providers.ValidationResult{}, probeNotSignedInMsg
		}
	}

	// A login existed and the vendor still said no. That is the sign-in
	// equivalent of a rejected key, so it maps to the one outcome that
	// BLOCKS — `success:false`, Finish stays disabled (FR-029) — carrying a
	// message written for a sign-in row rather than BuildMessage's key
	// wording. SEC-16: chatErr stays in the debug log above, never on the wire.
	return providers.ValidationResult{
		Outcome:     providers.OutcomeInvalidKey,
		Message:     signInProbeFailureMsg(displayName, model),
		RawDetail:   chatErr.Error(),
		ProbedModel: model,
	}, ""
}

// signInProbeFailureMsg is the curated, user-facing text for a completion that
// failed AFTER a saved login was confirmed (SEC-16: no raw vendor detail).
func signInProbeFailureMsg(displayName, model string) string {
	if model == "" {
		return fmt.Sprintf(
			"You're signed in to %s, but the test message failed. Try signing in again, then retry.",
			displayName)
	}
	return fmt.Sprintf(
		"You're signed in to %s, but the test message failed for %q. "+
			"Check that model is available on your plan, or sign in again.",
		displayName, model)
}
