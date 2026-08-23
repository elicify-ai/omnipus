// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
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

// onboardingSignInNotImplementedMsg is the typed 400 returned when a
// completion body selects the `sign_in` variant. The sign-in completion path
// (vendor-CLI login, no stored credential) is T068-16; until it lands the
// contract is honoured at the schema level only. T068-16 replaces this
// rejection with the real path and flips the pinning test in
// rest_onboarding_authmethod_test.go.
const onboardingSignInNotImplementedMsg = "sign-in onboarding not implemented — T068-16"

// onboardingAuthMethodErrMsg is the 400 for a missing or unrecognized
// provider.auth_method discriminator.
const onboardingAuthMethodErrMsg = "provider.auth_method is required and must be one of api_key, sign_in"

// decodeOnboardingCompleteBody reads the POST /onboarding/complete body,
// peeks provider.auth_method, and — for the api_key variant — returns the
// decoded wrapper (for `admin`) plus the strictly-decoded
// OnboardingProviderApiKey. Any other discriminator value has already been
// answered with a 400 when ok is false.
//
// Behaviour for api_key bodies is byte-for-byte the pre-ADR-068 one apart
// from the now-required auth_method field: the same 1 MB limit, the same
// "request body is required" / "invalid JSON body" messages, and schema
// validation only when validateEnabled.
func decodeOnboardingCompleteBody(w http.ResponseWriter, r *http.Request, validateEnabled bool) (gen.OnboardingCompleteRequest, gen.OnboardingProviderApiKey, bool) {
	var body gen.OnboardingCompleteRequest
	var provider gen.OnboardingProviderApiKey

	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return body, provider, false
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		jsonErr(w, http.StatusBadRequest, "request body is required")
		return body, provider, false
	}

	var peek struct { // not-wire-format: decode-only local peek at the discriminator and the raw provider member, never serialized
		Provider struct {
			AuthMethod *string `json:"auth_method"`
		} `json:"provider"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return body, provider, false
	}
	if peek.Provider.AuthMethod == nil {
		jsonErr(w, http.StatusBadRequest, onboardingAuthMethodErrMsg)
		return body, provider, false
	}
	switch *peek.Provider.AuthMethod {
	case string(gen.OnboardingProviderApiKeyAuthMethodApiKey):
		// fallthrough to the api_key path below
	case string(gen.OnboardingProviderSignInAuthMethodSignIn):
		// T068-16 wires the sign-in completion path; stubbed honestly until then.
		jsonErr(w, http.StatusBadRequest, onboardingSignInNotImplementedMsg)
		return body, provider, false
	default:
		jsonErr(w, http.StatusBadRequest, onboardingAuthMethodErrMsg)
		return body, provider, false
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
			return body, provider, false
		}
	}

	if err := json.Unmarshal(raw, &body); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return body, provider, false
	}
	// Strict decode of the provider member into the named variant: a field
	// the api_key variant does not carry is rejected 400 unconditionally,
	// independent of ValidateInbound (ADR-034 decodeAgentCreateVariant rule).
	var providerRaw struct { // not-wire-format: decode-only local carrier for the raw provider member
		Provider json.RawMessage `json:"provider"`
	}
	if err := json.Unmarshal(raw, &providerRaw); err != nil || len(providerRaw.Provider) == 0 {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return body, provider, false
	}
	dec := json.NewDecoder(bytes.NewReader(providerRaw.Provider))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&provider); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf(
				"field not allowed on provider auth_method %q: %v — see the OnboardingProviderApiKey schema",
				*peek.Provider.AuthMethod, err))
		} else {
			jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		}
		return body, provider, false
	}
	return body, provider, true
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
	if provider.Id == "" {
		jsonErr(w, http.StatusBadRequest, "provider.id is required")
		return
	}
	// Reject unknown protocols at the boundary so the gateway does not persist
	// a config that will fail the post-save rewire and flip to degraded.
	if !providers.IsKnownProtocol(provider.Id) {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("provider.id %q is not a known protocol", provider.Id))
		return
	}
	if provider.ApiKey == "" {
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
		return
	}

	// Resolve what the probe will talk to: the operator's endpoint override if
	// they supplied one, else the vendor default for this protocol.
	probeBase := ""
	if provider.Endpoint != nil {
		probeBase = strings.TrimSpace(*provider.Endpoint)
	}
	if probeBase == "" {
		probeBase = providers.GetDefaultAPIBase(provider.Id)
	}

	// keyWarning carries a non-blocking validation outcome to the response's
	// existing `warning` field. Empty means "nothing to say".
	keyWarning := ""
	// probeSkipReason is empty when the probe actually ran; otherwise it names
	// why it could not, for the audit trail.
	probeSkipReason := ""
	providerDisplayName := providers.DisplayName(provider.Id)
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
				"provider", provider.Id, "error", err)
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
		// D6: fetch the live model catalog first, exactly as
		// HandleOnboardingProbeProvider does (see its Catalog: models call
		// below), so ValidateKey's pickProbeModel can prefer a probe model that
		// is actually present in the provider's current catalog. Without this,
		// pickProbeModel returns providers.probeModelDefaults' hardcoded slug
		// immediately for any of the 10 providers in that table WITHOUT ever
		// calling FetchModels (see pickProbeModel: it only reaches the
		// catalog-fetch fallback when the filtered catalog is empty AND no
		// rules-table default exists) — so a retired default slug would silently
		// degrade the check to a false Unreachable, or a wrong-model 400 to a
		// false Valid, defeating the whole point of this fix for exactly the
		// providers it's most likely to matter for. A fetch failure here is not
		// fatal: ValidateKey falls back to the rules-table default exactly as it
		// did before this fetch existed.
		catalog, catalogErr := providers.FetchModels(r.Context(), probeBase, provider.ApiKey, a.ssrfChk())
		if catalogErr != nil {
			slog.Debug("onboarding: catalog fetch before key validation failed; falling back to rules-table probe model",
				"provider", provider.Id, "error", catalogErr)
			catalog = nil
		}
		// SEC-16: result.RawDetail is server-debug-only and never leaves this process.
		result := providers.ValidateKey(r.Context(), providers.ValidateInput{
			ProviderID:   provider.Id,
			ProviderName: providerDisplayName,
			BaseURL:      probeBase,
			APIKey:       provider.ApiKey,
			Catalog:      catalog,
		}, a.ssrfChk())
		slog.Debug("onboarding: provider key validation result",
			"provider", provider.Id, "outcome", result.Outcome, "detail", result.RawDetail)

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
			return
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
						"provider": provider.Id,
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
				"provider": provider.Id,
				"reason":   probeSkipReason,
				"source":   "onboarding",
			},
		}); err != nil {
			slog.Warn("audit write failed", "event", "provider_key_validation_skipped", "error", err)
		}
	}

	// Store the API key in the encrypted credentials store (AES-256-GCM).
	// Refuses the operation if the store is locked (SEC-23: no plaintext fallback).
	credRefName, credErr := a.storeCredential(provider.Id+"_API_KEY", provider.ApiKey)
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
	if provider.Model != nil {
		providerModel = *provider.Model
	}
	if providerModel == "" {
		switch provider.Id {
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
		"provider":    provider.Id,
		"model":       providerModel,
		"api_key_ref": credRefName,
	}
	// Persist a custom endpoint as api_base when supplied (required for providers
	// with no fixed default base, e.g. azure; also a regional-host override). The
	// runtime factory reads api_base before falling back to GetDefaultAPIBase.
	if provider.Endpoint != nil {
		if ep := strings.TrimSpace(*provider.Endpoint); ep != "" {
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
			if entryMap["provider"] == provider.Id && entryMap["model"] == providerModel {
				// Update existing entry.
				if credRefName != "" {
					entryMap["api_key_ref"] = credRefName
					delete(entryMap, "api_key")
					delete(entryMap, "api_keys")
				} else {
					entryMap["api_key"] = provider.ApiKey
				}
				entryMap["model"] = providerModel
				entryMap["model_name"] = providerModel
				entryMap["provider"] = provider.Id
				if provider.Endpoint != nil {
					if ep := strings.TrimSpace(*provider.Endpoint); ep != "" {
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
	case credRefName == "":
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
	// ADR-067 FR-023 / ADR-068 FR-036: ONE ProbeProviderRequest shape. Only the
	// api_key path is wired here; the sign_in path (CLI saved login / Copilot
	// session) lands with ADR-068's provider constructors (B3), and catalog
	// validation of `id` + the custom-row (api_base + protocol) rule land with
	// ADR-067 T067-12. Until then: auth=sign_in → 400, and api_key is required.
	if body.Auth != gen.ProbeProviderRequestAuthApiKey {
		jsonErr(w, http.StatusBadRequest, "auth sign_in is not supported yet; use auth api_key")
		return
	}
	apiKey := ""
	if body.ApiKey != nil {
		apiKey = *body.ApiKey
	}
	if apiKey == "" {
		jsonErr(w, http.StatusBadRequest, "api_key is required")
		return
	}

	baseURL := ""
	if body.ApiBase != nil {
		baseURL = *body.ApiBase
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
	models, fetchErr := providers.FetchModels(r.Context(), baseURL, apiKey, a.ssrfChk())
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
		ProviderID:   body.Id,
		ProviderName: providers.DisplayName(body.Id),
		BaseURL:      baseURL,
		APIKey:       apiKey,
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
