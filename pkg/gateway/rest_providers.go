// rest_providers.go: LLM provider catalog, keys, and dependents

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	providers_pkg "github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// isSeedTemplateRow reports whether a providers[] entry is a fresh-install
// TEMPLATE rather than something the operator configured (ADR-067 FR-029).
//
// The test used to be `Provider == ""`, because seed templates carried no
// provider identity at all. ADR-067 FR-011 made the provider id mandatory —
// a row IS the pair (provider, model) — so identity no longer distinguishes
// them. What does is that a template names a provider and supplies NOTHING
// with which to reach it: no credential, no endpoint, no model list, no PUT
// stamp and no auth method. The moment any of those is present, an operator
// has touched the row and it is a configuration.
// anonRedactedDependents returns the provider's real dependent list for an
// authenticated caller and an EMPTY (never nil) list for an anonymous one.
//
// C1: `dependents` enumerates every agent id and name bound to the provider —
// the operator's roster, which an unauthenticated reader has no business
// enumerating. Provider.yaml marks the field required with "always present
// (empty array when none)", so the anonymous answer is `[]`, which is
// contract-valid and indistinguishable from a provider nothing depends on.
func anonRedactedDependents(authed bool, cfg *config.Config, providerID string) []gen.ProviderDependent {
	if !authed {
		return []gen.ProviderDependent{}
	}
	return computeProviderDependents(cfg, providerID)
}

// --- Providers ---

// HandleProviders handles GET/PUT/POST /api/v1/providers and sub-paths.
func (a *restAPI) HandleProviders(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	sub := strings.TrimPrefix(path, "/api/v1/providers")
	sub = strings.TrimPrefix(sub, "/")

	switch {
	case r.Method == http.MethodGet && sub == "":
		// Return the CONFIGURED providers only (ADR-068 FR-011a, resolution
		// #16), enriched with upstream available models for OpenAI-compatible
		// providers. The seeded cfg.Providers templates (pkg/config/defaults.go
		// — model + api_base, no `provider` identity, no credential ref) are
		// NOT rows: a row the operator never created is not theirs to manage,
		// and the SPA used to have to filter the ~10 permanent "disconnected"
		// template rows out of every list. A row is configured when it
		// carries a `provider` identity — PUT /providers/{id} and onboarding
		// completion stamp it on every row they write (template or new), and
		// ADR-067 makes it the only provider identity.
		//
		// C1 — AUTHORIZATION. This branch is the one branch of HandleProviders
		// that used to carry no gate at all. The shared route is registered
		// withOptionalAuth (an anonymous caller passes straight through), and
		// every sibling verb carries its own inline gate — PUT and /test gate
		// on onboardingDone, the five sign-in routes gate on FR-050, DELETE
		// 401s unconditionally — so the list, and only the list, stayed
		// anonymous forever. On a fully onboarded production gateway an
		// unauthenticated GET returned the whole provider inventory, each
		// row's status and updated_at, the full `dependents` list (every agent
		// id and name referencing the provider) and — once T068-14 wired
		// cheapSignInRowStatus into this branch — the `account_label` of the
		// operator's live ChatGPT/xAI session. contracts/openapi.yaml has
		// always declared `security: [BearerAuth: []]` for listProviders, so
		// implementation and contract disagreed; this restores the contract.
		//
		// The gate is requireAuthOutsideOnboarding, the same FR-050 shape the
		// sibling branches use (fail-closed on unknown onboarding state — see
		// preAuthOnboardingWindowOpen, M3) rather than an unconditional 401,
		// so an onboarding client that has no admin account to authenticate
		// as is not locked out of a read the wizard may need.
		//
		// What an ANONYMOUS caller gets is deliberately reduced (see
		// `authed` below): never account_label, and never dependents. The
		// SPA needs neither pre-auth — the wizard reads the catalog
		// (GET /providers/catalog) and POSTs /onboarding/probe-provider;
		// fetchProviders (src/lib/api.ts) is called only by the Settings
		// screens, which run authenticated.
		if !a.requireAuthOutsideOnboarding(w, r) {
			return
		}
		// `authed` decides the row REDUCTION below, and must recognise the
		// same principals the gate above accepted — otherwise an
		// env-token-authenticated headless caller would pass the gate and
		// then be served the anonymous, redacted rows.
		authed := a.requestPrincipalAuthenticated(r)
		if !authed {
			// Pre-onboarding anonymous reads get a ceiling; this branch fans
			// out to a live upstream /models fetch per configured provider.
			// Authenticated callers are not limited here (see
			// providerListAnonLimiter's doc comment in rest_auth.go).
			ip := clientIP(r)
			if !providerListAnonLimiter.allow(ip) {
				retryAfter := providerListAnonLimiter.retryAfter(ip)
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				jsonErr(w, http.StatusTooManyRequests,
					fmt.Sprintf("rate limit exceeded, retry after %d seconds", retryAfter))
				return
			}
		}
		cfg := a.agentLoop.GetConfig()
		// providerUserModels holds the operator-supplied catalog slugs for
		// providers that have no live /models endpoint (UAT model-catalog fix).
		providerUserModels := make(map[string][]string)
		providerAPIKeys := make(map[string]string)
		// providerCredErrors holds an operator-facing message, keyed by provider
		// name, for the case where a.resolveCredentialRef failed for a reason
		// WORSE than "ref not found" (locked/undecryptable vault) — see
		// describeCredentialResolutionError. Without this, that case and "no key
		// configured at all" were both reported as plain status=disconnected,
		// indistinguishable to the operator viewing Settings/Providers.
		providerCredErrors := make(map[string]string)
		// providerUpdatedAt / providerAuthMethod carry the ADR-068 row fields
		// (T068-08): the latest PUT stamp across the provider's rows (MAJ-015,
		// the picker's Recent ordering key) and the row's auth method
		// (api_key unless a sign_in row exists — T068-14 wires the sign-in
		// status/account_label on top of this).
		providerUpdatedAt := make(map[string]*time.Time)
		providerAuthMethod := make(map[string]string)
		// providerFirstRow is the representative config row for each
		// provider — the first non-template one, which carries the
		// custom/protocol/api_base identity ADR-067 FR-020 and FR-039 read
		// to decide where this row's model list comes from.
		providerFirstRow := make(map[string]*config.ModelConfig)
		providerOrder := make([]string, 0)
		seen := make(map[string]struct{})
		for _, m := range cfg.Providers {
			if isSeedTemplateRow(m) {
				continue // never configured — not a row (FR-029)
			}
			providerName := m.Provider
			if _, exists := seen[providerName]; !exists {
				seen[providerName] = struct{}{}
				providerOrder = append(providerOrder, providerName)
				providerFirstRow[providerName] = m
			}
			if m.UpdatedAt != nil {
				if cur := providerUpdatedAt[providerName]; cur == nil || m.UpdatedAt.After(*cur) {
					providerUpdatedAt[providerName] = m.UpdatedAt
				}
			}
			if providerAuthMethod[providerName] == "" && m.AuthMethod != "" {
				providerAuthMethod[providerName] = m.AuthMethod
			}
			if len(m.Models) > 0 {
				providerUserModels[providerName] = append(providerUserModels[providerName], m.Models...)
			}
			// Resolve API key for upstream model fetching.
			// APIKeyRef is resolved via process environment (set by InjectFromConfig).
			if _, hasKey := providerAPIKeys[providerName]; !hasKey {
				resolved := m.APIKey()
				if resolved == "" && m.APIKeyRef != "" {
					if v, err := a.resolveCredentialRef(m.APIKeyRef); err != nil {
						slog.Warn("rest: could not resolve provider credential", "ref", m.APIKeyRef, "error", err)
						var notFound *credentials.NotFoundError
						if !errors.As(err, &notFound) {
							providerCredErrors[providerName] = describeCredentialResolutionError(err)
						}
					} else {
						resolved = v
					}
				}
				if resolved != "" {
					providerAPIKeys[providerName] = resolved
				}
			}
		}
		providers := make([]gen.Provider, 0, len(providerOrder))
		for _, name := range providerOrder {
			// ADR-067 FR-020 (T067-10): the model list's source is decided
			// by the row's locality, not by whether a vendor base URL is
			// hardcoded anywhere. A `locality = cloud` row lists the
			// CATALOG's models with no outbound call at all (US-9.AC1 —
			// the list is instant and works offline); only a
			// `locality = local` row is listed live, because nothing but
			// that machine knows what has been pulled onto it (US-9.AC3).
			src := a.resolveProviderRow(name, providerFirstRow[name])
			models, modelFetchWarning := a.providerModelList(
				r.Context(), name, src, providerAPIKeys[name], providerUserModels[name])
			// "Has a live /models endpoint" means "the gateway fills this
			// list, so the SPA must not present it as an editable slug
			// list". A custom endpoint never qualifies — its catalogue IS
			// the operator's slugs. APIBaseFor reads the process catalog,
			// which the gateway installs from the very document
			// resolveProviderRow read (providers.SetCatalog at boot), so
			// the two agree on every real installation.
			hasEndpoint := !src.custom && providers_pkg.APIBaseFor(name) != ""
			// FR-104: report Connected only when the provider's API key resolves to
			// a non-empty credential. providerAPIKeys is populated above for every
			// provider that has either a resolvable api_key_ref or an inline api_key;
			// absence from the map means no key was found.
			status := gen.ProviderStatusDisconnected
			if _, hasKey := providerAPIKeys[name]; hasKey {
				status = gen.ProviderStatusConnected
			}
			// ADR-068 FR-043 / ADR-067 FR-016: a configured row whose id the
			// served catalog does not contain is unknown-provider, with the
			// generic text parameterised by the operator's own id (the id is
			// user data, not a trace — CRIT-003) and an empty model list
			// (S67 Q4). Classified only when a catalog document is actually
			// loaded, and never for a custom row — an operator-named
			// endpoint is not in the catalog BY DESIGN (FR-035, X-13); an
			// absent catalog (E7) never turns every row unknown.
			var unknownProviderMsg string
			if src.unknownProvider() {
				status = gen.ProviderStatusUnknownProvider
				unknownProviderMsg = fmt.Sprintf("unknown provider %q", name)
				models = []string{}
			}
			// ADR-068 FR-034 (T068-14 gap fix): a configured sign_in row's
			// real status/account_label, using ONLY the cheap local check —
			// no vendor fan-out for a background list render. Skipped when
			// unknown-provider already won above (nothing to look up for an
			// id the catalog does not carry) — cheapSignInRowStatus's own
			// doc comment covers why github-copilot never pays a premium
			// request here.
			//
			// C1: skipped entirely for an ANONYMOUS caller. account_label is
			// the operator's own vendor account identifier (Provider.yaml:
			// "account identifier of the signed-in session") and must never
			// reach an unauthenticated response — not redacted downstream,
			// not fetched at all, so the anonymous path also stops touching
			// the credential store to peek stored OAuth material.
			var signInAccountLabel *string
			if authed && unknownProviderMsg == "" && providerAuthMethod[name] == config.AuthMethodSignIn {
				if signInState, label, known := a.cheapSignInRowStatus(name); known {
					status = signInState
					if label != "" {
						labelCopy := label
						signInAccountLabel = &labelCopy
					}
				}
			}
			// ADR-068 FR-009 (T068-15): a `github-copilot` row is backed by
			// the vendor CLI, not an API key, so the key-derived status above
			// says nothing useful about it. When the CLI is absent from this
			// machine the row stays `disconnected` and carries the operator
			// hint. Whether the operator is SIGNED IN is never computed by
			// running the CLI here — that check costs a premium request, so
			// it stays the explicit Check sign-in action's alone. The row
			// CAN still say signed_in/expired for github-copilot — from the
			// cached result of the operator's last explicit Check
			// (copilotRowSignInStatus, never a probe), codex-cli from its
			// saved login file.
			copilotHint := copilotRowHint(name)
			hasEndpointCopy := hasEndpoint
			// ADR-068 T068-08: the row's auth method comes from the config row
			// (closed set api_key | sign_in — Validate rejects anything else);
			// api_key when unset. account_label is populated above when the
			// cheap sign-in status check found a signed_in/expired session.
			authMethod := gen.ProviderAuthMethodApiKey
			if providerAuthMethod[name] == config.AuthMethodSignIn {
				authMethod = gen.ProviderAuthMethodSignIn
			}
			p := gen.Provider{
				Id:                name,
				Name:              name,
				Status:            status,
				Models:            models,
				HasModelsEndpoint: &hasEndpointCopy,
				// ADR-068 FR-012 (T068-08): dependents/backs_default are
				// advisory here; T068-09's DELETE recomputes them under
				// configMu and its response is authoritative (MAJ-018).
				AuthMethod:   authMethod,
				AccountLabel: signInAccountLabel,
				// C1: `dependents` names every agent that references this
				// provider — the operator's roster. Provider.yaml requires
				// the field, so an anonymous caller gets the empty array
				// rather than a missing key: contract-valid, and it tells an
				// unauthenticated reader nothing about who runs here.
				Dependents:   anonRedactedDependents(authed, cfg, name),
				BacksDefault: providerBacksDefault(cfg, name),
				UpdatedAt:    providerUpdatedAt[name],
				// ADR-067 identity fields (T067-10): the wire row now
				// carries what the catalog says about it, so the SPA never
				// has to re-derive protocol, locality or grouping.
				Protocol: providerWireProtocol(src.protocol),
				Locality: providerWireLocality(src.locality),
			}
			if src.custom {
				customCopy := true
				p.Custom = &customCopy
			}
			if src.known {
				if company := src.row.Company; company != "" {
					companyCopy := company
					p.Company = &companyCopy
				}
				if display := src.row.Name; display != "" {
					displayCopy := display
					p.DisplayName = &displayCopy
				}
				p.CliKind = providerWireCLIKind(src.row.CLIKind)
			}
			if modelFetchWarning != "" {
				p.Warning = &modelFetchWarning
			}
			switch {
			case unknownProviderMsg != "":
				// unknown-provider wins over the credential-derived states: a
				// key for a provider that does not exist is not "connected".
				p.Error = &unknownProviderMsg
			case status != gen.ProviderStatusConnected:
				// A credential-resolution failure worse than "not configured"
				// (locked/undecryptable vault) is reported as status=error with
				// the classified remediation message, instead of a plain
				// "disconnected" indistinguishable from a provider whose key was
				// never entered (Task 3 fix).
				if credErrMsg, ok := providerCredErrors[name]; ok {
					p.Status = gen.ProviderStatusError
					p.Error = &credErrMsg
				} else if copilotHint != "" {
					p.Error = &copilotHint
				}
			}
			providers = append(providers, p)
		}
		// No configured provider → `[]`, never a synthetic "default" filler
		// row (a fresh install has nothing to manage yet; the onboarding wizard
		// creates the first row).
		jsonOK(w, providers)

	case r.Method == http.MethodDelete && isReservedProviderPathSegment(sub):
		// DELETE on a reserved path segment ("catalog", "model-capabilities";
		// "default-model" normally dispatches to its own route first) — the
		// reserved literals are never provider ids, so there is nothing to
		// delete: 404, per the MAJ-002 scenario rows.
		jsonErr(w, http.StatusNotFound, "provider not found")

	case r.Method == http.MethodDelete && sub != "" && !strings.Contains(sub, "/"):
		// DELETE /api/v1/providers/{id} (T068-09, ADR-068 FR-010/FR-011,
		// rest_providers_delete.go). The shared dispatcher is registered
		// withOptionalAuth, so the verb carries its own authorization gate
		// inline (FR-042/MAJ-007): requireAdminAuthz (RequireNotBypass →
		// 503 under dev-mode bypass) here, and the unconditional 401 for an
		// unauthenticated caller inside deleteProvider — no pre-onboarding
		// exception.
		providerID := sub
		a.requireAdminAuthz(func(w http.ResponseWriter, r *http.Request) {
			a.deleteProvider(w, r, providerID)
		})(w, r)

	case r.Method == http.MethodPut && sub != "" && !strings.HasSuffix(sub, "/test"):
		// O5: rate limit FIRST, before any other work on this branch. FR-050
		// keeps this route reachable with no credential while onboarding is
		// incomplete (see requireAuthOutsideOnboarding below), and it is one
		// of the most expensive requests the gateway serves — an outbound
		// ValidateKey call, a config.json rewrite, and then
		// triggerReloadAndWaitOutcome, which rebuilds the ENTIRE agent
		// registry synchronously and holds the response until it confirms.
		// With no ceiling at all, one anonymous client could keep a fresh
		// install in permanent rebuild churn. The limiter is the only bound
		// on that caller, so nothing may run ahead of it.
		if !rateLimitAllows(w, r, providerConfigWriteLimiter) {
			return
		}
		// PUT /api/v1/providers/{id} — update or insert a provider entry.
		// Reserved path segments are never provider ids (MAJ-002): reject
		// BEFORE auth-gating or decoding so no request shape can upsert a
		// provider named "catalog" / "default-model" / "model-capabilities".
		if isReservedProviderPathSegment(sub) {
			jsonErrField(w, http.StatusBadRequest,
				fmt.Sprintf("unknown provider %q", sub), "id")
			return
		}
		// O16: reject an id containing a path separator BEFORE it can ever
		// be written. DELETE /api/v1/providers/{id} only matches
		// `!strings.Contains(sub, "/")` (see the MethodDelete case above),
		// so a row this PUT let through with a "/" in its id — e.g.
		// PUT /api/v1/providers/a/b, where sub == "a/b" — could be created
		// but could never be routed to a DELETE again; the row became
		// permanently stuck in config.json. validateEntityID also rejects
		// ".." and NUL, both equally unwelcome in a value that ends up as a
		// providers[].provider string and a "<id>_API_KEY" credential ref.
		if err := validateEntityID(sub); err != nil || len(sub) > maxProviderIDLen {
			jsonErrField(w, http.StatusBadRequest, "invalid provider id", "id")
			return
		}
		// Allow unauthenticated access during onboarding so the wizard can
		// configure the provider before the admin user exists. M3: the window
		// closes on an unknown onboarding state as well as a complete one —
		// see preAuthOnboardingWindowOpen (rest_auth.go).
		if !a.requireAuthOutsideOnboarding(w, r) {
			return
		}
		// Re-auth gate (Spec-6 FR-12.2 / FR-6.6): a model/provider API-key mutation
		// is a sensitive HTTP-layer settings change and requires the single-use
		// re-auth consent token — the same gate the Integrations PUT enforces.
		// Skipped only during onboarding (no authenticated user yet), where the
		// provider is configured before any password exists. When a user IS in
		// context (post-onboarding edits), the token is mandatory.
		if reauthUser, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); ok && reauthUser != nil {
			if !a.requireReAuth(w, r, reauthUser.Username) {
				return
			}
		}
		providerID := sub
		var req gen.ProviderUpdateRequest
		validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
		if !decodeAndValidate(w, r, "ProviderUpdateRequest", &req, validateEnabled) {
			return
		}
		// ADR-068 (T068-14 gap fix; contracts/components/schemas/
		// ProviderUpdateRequest.yaml `auth_method`): sign_in is accepted only
		// for a provider whose catalog row declares it, and must not be
		// combined with api_key. Reuses signInMethodFor — the same helper
		// the five sign-in routes gate on — so this PUT and POST
		// .../sign-in never disagree about which providers support sign-in.
		wantsSignIn := req.AuthMethod != nil && *req.AuthMethod == gen.ProviderUpdateRequestAuthMethodSignIn
		if wantsSignIn {
			if req.ApiKey != nil && *req.ApiKey != "" {
				jsonErrField(w, http.StatusBadRequest,
					"auth_method sign_in must not be combined with api_key", "auth_method")
				return
			}
			if _, ok := a.signInMethodFor(providerID); !ok {
				jsonErrField(w, http.StatusBadRequest,
					"provider does not support sign-in", "auth_method")
				return
			}
		}
		// Bounds enforcement (M-slug): cap the model list inline so the limits
		// hold even when schema validation is skipped (validate_inbound=false is
		// the default). Mirrors the inline name/description caps in
		// rest_workspaces.go. dedupeNonEmpty applies no maxItems / length cap.
		if req.Models != nil {
			const maxModels = 500
			const maxSlugLen = 256
			if len(*req.Models) > maxModels {
				jsonErr(w, http.StatusBadRequest, fmt.Sprintf("models exceeds %d entries", maxModels))
				return
			}
			for _, slug := range *req.Models {
				if len(slug) > maxSlugLen {
					jsonErr(w, http.StatusBadRequest, fmt.Sprintf("model slug exceeds %d characters", maxSlugLen))
					return
				}
			}
		}
		// ADR-067 FR-019 / FR-035 (T067-10): catalog admission. The id the
		// operator typed is either a catalog row (accepted unless the
		// catalog itself marks it unsupported, with the catalog's own
		// reason), or an operator-named CUSTOM endpoint — accepted only
		// when it carries both halves of what it takes to reach one, an
		// api_base and one of the two protocols a base URL fully
		// describes. Anything else is an unknown provider, and saying so
		// here is the difference between an obvious 400 and a row that
		// looks saved and never resolves a model.
		reqAPIBase := derefStr(req.ApiBase)
		reqProtocol := derefStr((*string)(req.Protocol))
		isCustomRow, admitErr := providerAdmission(a.providerCatalog, providerID, reqAPIBase, reqProtocol)
		if admitErr != nil {
			field := "id"
			if errors.Is(admitErr, providers_pkg.ErrUnknownProvider) && reqAPIBase != "" {
				// The id is unknown AND a base was supplied: what is
				// missing is the protocol, so point the SPA at that field.
				field = "protocol"
			}
			jsonErrField(w, http.StatusBadRequest, admitErr.Error(), field)
			return
		}

		// Check if the provider already exists.
		cfg := a.agentLoop.GetConfig()
		found := false
		for _, m := range cfg.Providers {
			if m.IsVirtual() {
				continue
			}
			if strings.TrimSpace(m.Provider) == providerID {
				found = true
				break
			}
		}
		if !found {
			// New provider — api_key is required, UNLESS the row
			// authenticates via sign_in (T068-14 gap fix): a sign_in row has
			// no key to give — its credential lives in the encrypted OAuth
			// entry the sign-in handlers write, never here.
			if !wantsSignIn && (req.ApiKey == nil || *req.ApiKey == "") {
				jsonErr(w, http.StatusUnprocessableEntity, "api_key is required")
				return
			}
			if req.Model == nil || *req.Model == "" {
				defaultModel := "default"
				req.Model = &defaultModel
			}
		}
		// R-D fixed order (spec FR-011 / R-D):
		// Step 2 — key-changed check (R-C): validate ONLY when api_key is present and non-empty.
		// Re-sending the same key value DOES re-probe (accepted cost). A PUT omitting
		// api_key (model/label-only edit) skips the probe entirely — no billable upstream call.
		// Step 3 — resolve persisted api_base + SSRF check (NOT a request field; ProviderUpdateRequest
		// has no api_base field). Step 4 — ValidateKey. Step 5 — InvalidKey → 422, persist nothing.
		// Note: the re-auth consent token (step 1) is consumed BEFORE reaching here (see requireReAuth
		// above). A 422 at step 5 therefore burns the single-use token — the SPA must re-auth on retry
		// of a corrected key (R-D/M4, accepted trade-off: validating before consent would probe without step-up).
		var putValidationResult providers_pkg.ValidationResult
		keyChanged := req.ApiKey != nil && *req.ApiKey != ""
		if keyChanged {
			// Store-readiness FIRST (SEC-23): if the credential store is locked we cannot
			// persist the key, so return 503 BEFORE the SSRF check + the billable
			// validation probe. (Otherwise a locked store would be reported as an invalid
			// key — the validation probe would run and 422 before we discovered we can't
			// store anything.)
			if err := a.credentialStoreReady(); err != nil {
				slog.Error("rest: credential store unavailable for provider update",
					"provider", providerID, "error", err)
				jsonErr(w, http.StatusServiceUnavailable,
					"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets")
				return
			}
			// Resolve the base URL to probe: the api_base this very
			// request supplies wins (a custom row has no other source),
			// then the persisted one, then the catalog's.
			persistedAPIBase := reqAPIBase
			if persistedAPIBase == "" {
				for _, m := range cfg.Providers {
					if m.IsVirtual() {
						continue
					}
					if strings.TrimSpace(m.Provider) == providerID {
						persistedAPIBase = m.APIBase
						break
					}
				}
			}
			if persistedAPIBase == "" {
				persistedAPIBase = providers_pkg.APIBaseFor(providerID)
			}
			// SSRF-check the persisted api_base before any outbound probe.
			if persistedAPIBase != "" && a.ssrfChecker != nil {
				if err := a.ssrfChecker.CheckURL(r.Context(), persistedAPIBase); err != nil {
					slog.Warn("rest: PUT provider: SSRF blocked persisted api_base",
						"provider", providerID, "api_base", persistedAPIBase, "error", err)
					jsonErr(w, http.StatusUnprocessableEntity, "provider endpoint not allowed (SSRF guard)")
					return
				}
			}
			// Run the centralized key validator. SEC-16: RawDetail is server-debug only.
			putValidationResult = providers_pkg.ValidateKey(r.Context(), providers_pkg.ValidateInput{
				ProviderID:   providerID,
				ProviderName: providers_pkg.DisplayName(providerID),
				BaseURL:      persistedAPIBase,
				APIKey:       *req.ApiKey,
			}, a.ssrfChk())
			slog.Debug("rest: PUT provider: key validation result",
				"provider", providerID, "outcome", putValidationResult.Outcome,
				"detail", putValidationResult.RawDetail)
			if putValidationResult.Blocks() {
				// InvalidKey — reject the save. The key is NOT stored. SEC-16: message is curated.
				jsonErr(w, http.StatusUnprocessableEntity, putValidationResult.Message)
				return
			}
		}

		// Store API key in the encrypted credentials store (AES-256-GCM) and
		// reference it via api_key_ref in config.json. Refuses the operation if
		// the credential store is locked (SEC-23: no plaintext fallback).
		var credRefName string
		if keyChanged {
			ref, err := a.storeCredential(providerID+"_API_KEY", *req.ApiKey)
			if err != nil {
				slog.Error(
					"rest: credential store unavailable for provider update",
					"provider",
					providerID,
					"error",
					err,
				)
				jsonErr(
					w,
					http.StatusServiceUnavailable,
					"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets",
				)
				return
			}
			credRefName = ref
		}
		// Normalise the user-supplied catalog slugs (UAT model-catalog fix) into
		// a deduplicated []any for JSON persistence. nil req.Models leaves the
		// stored list unchanged; a non-nil (incl. empty) list replaces it.
		var userModelsJSON []any
		if req.Models != nil {
			for _, slug := range dedupeNonEmpty(*req.Models) {
				userModelsJSON = append(userModelsJSON, slug)
			}
			if userModelsJSON == nil {
				userModelsJSON = []any{} // explicit clear
			}
		}
		// ADR-068 MAJ-015: every PUT stamps the row's updated_at — the
		// picker's Recent ordering key (Provider.updated_at).
		putStamp := time.Now().UTC()
		putStampStr := putStamp.Format(time.RFC3339)
		if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
			providerList, _ := m["providers"].([]any)
			updated := false
			for _, entry := range providerList {
				model, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				if strings.TrimSpace(strVal(model, "provider")) == providerID {
					model["updated_at"] = putStampStr
					if req.ApiKey != nil && *req.ApiKey != "" {
						model["api_key_ref"] = credRefName
						delete(model, "api_key")
						delete(model, "api_keys")
					}
					if req.Model != nil && *req.Model != "" {
						model["model"] = *req.Model
					}
					if req.Models != nil {
						if len(userModelsJSON) > 0 {
							model["models"] = userModelsJSON
						} else {
							delete(model, "models")
						}
					}
					model["provider"] = providerID
					// ADR-068 (T068-14 gap fix): persist the requested
					// auth_method verbatim — the field this gap never wrote,
					// which is why a signed-in ChatGPT session never
					// materialized a provider row for GET /providers to show.
					if req.AuthMethod != nil {
						model["auth_method"] = string(*req.AuthMethod)
					}
					applyProviderIdentity(model, reqAPIBase, reqProtocol, isCustomRow)
					updated = true
					break
				}
			}
			if !updated {
				// Provider not found — add a new entry.
				modelVal := ""
				if req.Model != nil {
					modelVal = *req.Model
				}
				newEntry := map[string]any{
					"provider":   providerID,
					"model":      modelVal,
					"updated_at": putStampStr,
				}
				if credRefName != "" {
					newEntry["api_key_ref"] = credRefName
				}
				if req.AuthMethod != nil {
					newEntry["auth_method"] = string(*req.AuthMethod)
				}
				if len(userModelsJSON) > 0 {
					newEntry["models"] = userModelsJSON
				}
				applyProviderIdentity(newEntry, reqAPIBase, reqProtocol, isCustomRow)
				m["providers"] = append(providerList, newEntry)
			}
			return nil
		}); err != nil {
			slog.Error("rest: save config for provider update", "error", err)
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not save config: %v", err))
			return
		}
		// ADR-067 FR-021 (T067-11): a PUT that CHANGED the key invalidates
		// this provider's cached entitlement — what a different key can
		// reach is a different fact. A PUT that only bumps updated_at (or
		// edits the model list) is deliberately NOT an eviction: the key
		// behind the cached answer is still the same key.
		if keyChanged {
			a.entitlements.evictProvider(providerID)
		}
		// Trigger reload AND WAIT for it (triggerReloadAndWaitOutcome, not a bare
		// TriggerReload — mirrors createAgent/updateAgent/deleteAgent/
		// updateAgentTools): a bare TriggerReload only enqueues the reload and
		// returns before the registry actually swaps. Per updateAgent's
		// model-apply doc comment a few hundred lines up, persisting +
		// SwapConfig alone does NOT touch an already-constructed agent
		// instance's cached provider/model client — only the async
		// TriggerReload → executeReload → ReloadProviderAndConfig →
		// NewAgentRegistry rebuild does. Without waiting, a client that fixes a
		// revoked/invalid API key here and immediately sends a chat message
		// could still be served by the stale cached client (and the old,
		// possibly-compromised key) for as long as that goroutine takes to
		// run. triggerReloadAndWaitOutcome absorbs ErrReloadNotConfigured (unit tests
		// / minimal embeddings without the full reload pipeline wired)
		// internally as a no-op, so a non-nil error here is always a genuine
		// reload failure — preserving the existing 500 semantics below (the
		// key IS persisted; only the live application failed). The confirmed
		// bool additionally distinguishes a genuine timeout (agents may still
		// be served by the stale cached provider client) from a hard failure.
		if confirmed, err := a.triggerReloadAndWaitOutcome(); err != nil {
			slog.Error("config reload after provider update failed", "error", err)
			jsonErr(
				w,
				http.StatusInternalServerError,
				fmt.Sprintf("provider updated but config reload failed: %v", err),
			)
			return
		} else if !confirmed {
			slog.Warn("rest: reload after provider update did not confirm within the poll window; "+
				"agents may still be served by the stale cached provider client", "provider_id", providerID)
		}
		// The saved row is a catalog row unless admission classified it as
		// an operator-named custom endpoint (FR-035); a catalog row's list
		// is filled by the gateway, a custom row's is the operator's own.
		hasEndpoint := !isCustomRow && providers_pkg.IsCatalogProvider(providerID)
		respModels := []string{}
		if req.Models != nil {
			respModels = dedupeNonEmpty(*req.Models)
			if respModels == nil {
				respModels = []string{}
			}
		}
		// ADR-068 (T068-14 gap fix): a sign_in row was not just given an
		// api_key, so the api_key-flavoured "connected"/api_key defaults
		// below would misreport it. Reuse the same cheap local check GET
		// /providers uses (no vendor fan-out) — falls back to disconnected
		// when the operator has not completed sign-in yet.
		respStatus := gen.ProviderStatusConnected
		respAuthMethod := gen.ProviderAuthMethodApiKey
		if wantsSignIn {
			respAuthMethod = gen.ProviderAuthMethodSignIn
			respStatus = gen.ProviderStatusDisconnected
			if state, _, known := a.cheapSignInRowStatus(providerID); known {
				respStatus = state
			}
		}
		providerResp := gen.Provider{
			Id:                providerID,
			Name:              providerID,
			Status:            respStatus,
			Models:            respModels,
			HasModelsEndpoint: &hasEndpoint,
			AuthMethod:        respAuthMethod,
			Dependents:        []gen.ProviderDependent{},
			BacksDefault:      providerBacksDefault(a.agentLoop.GetConfig(), providerID),
			UpdatedAt:         &putStamp,
		}
		if isCustomRow {
			customCopy := true
			providerResp.Custom = &customCopy
		}
		if p := providerWireProtocol(catalog.Protocol(reqProtocol)); p != nil {
			providerResp.Protocol = p
		}
		// R-D step 7 / FR-011: attach validation for warning outcomes (NoCredit/Unreachable/Restricted).
		// Valid outcome and key-absent PUTs carry no validation field.
		if keyChanged && putValidationResult.Outcome != providers_pkg.OutcomeValid {
			outcomeStr := gen.ProviderValidationOutcome(putValidationResult.Outcome)
			// Guard: only assign the validation object when the cast is a known wire value.
			// An off-contract Outcome (e.g. future 6th value, wrong case) must not silently
			// produce an invalid enum value on the wire.
			if !outcomeStr.Valid() {
				slog.Warn("rest: PUT provider: unrecognized validation outcome; omitting validation field",
					"provider", providerID, "outcome", putValidationResult.Outcome)
			} else {
				msg := putValidationResult.Message
				providerResp.Validation = &struct {
					Message *string                       `json:"message,omitempty"`
					Outcome gen.ProviderValidationOutcome `json:"outcome"`
				}{
					Outcome: outcomeStr,
					Message: &msg,
				}
			}
			// R-F / FR-017: audit the warning-proceed. Best-effort — log write failures.
			// Only persisting flows are audited (not the informational probe/Test), per O2.
			if a.auditor != nil {
				if err := a.auditor.Log(&audit.Entry{
					Event:    "provider_key_validated",
					Decision: audit.DecisionAllow,
					Details: map[string]any{
						"provider": providerID,
						"outcome":  string(putValidationResult.Outcome),
						"action":   "proceeded",
					},
				}); err != nil {
					slog.Warn("audit write failed", "event", "provider_key_validated", "error", err)
				}
			}
		}
		jsonOK(w, providerResp)

	case r.Method == http.MethodPost && strings.HasSuffix(sub, "/sign-in"),
		r.Method == http.MethodGet && strings.HasSuffix(sub, "/sign-in/status"),
		r.Method == http.MethodPost && strings.HasSuffix(sub, "/sign-in/poll"),
		r.Method == http.MethodPost && sub == "openai-chatgpt/sign-in/import",
		r.Method == http.MethodDelete && strings.HasSuffix(sub, "/sign-in"):
		// ADR-068 FR-008/FR-009/FR-044/FR-047/FR-048 (§8b amendment,
		// 2026-08-23; T068-14). FR-050: these five routes are reachable
		// PRE-AUTH only while onboarding is incomplete (mirrors the /test
		// handler's onboardingDone gate above, and CLAUDE.md's documented
		// "Onboarding does NOT need bypass" list, which already covers
		// bare /providers) — onboarding step 3 needs a working sign-in
		// flow before any admin account exists to authenticate as. Once
		// onboarding is complete these revert to the contract's normal
		// adminWrap posture: 401 unauthenticated, 503 under dev-mode
		// bypass.
		//
		// M3: the gate is requireAuthOutsideOnboarding, not a bare
		// IsComplete() test. IsComplete() fails OPEN — onboarding.NewManager
		// keeps OnboardingComplete=false on any load error and resets an
		// unparseable state.json — so one corrupt file silently reopened all
		// five of these routes, DELETE (destroys the OAuth grant) and import
		// (writes the credential store) included, unauthenticated, on a
		// long-onboarded instance. See preAuthOnboardingWindowOpen.
		if !a.requireAuthOutsideOnboarding(w, r) {
			return
		}
		a.requireAdminAuthz(func(w http.ResponseWriter, r *http.Request) {
			switch {
			// github-copilot has its own CLI-status-aware handlers (T067-07/
			// T068-15): a real state via one bounded Copilot CLI invocation,
			// cost-aware about premium requests. Routed first so they win
			// over the generic cli_login path below, which would otherwise
			// also match github-copilot's routes (signInMethodFor classifies
			// it cli_login) with only a hardcoded command string and no real
			// status check.
			case r.Method == http.MethodPost && sub == copilotProviderID+"/sign-in":
				a.handleCopilotSignInStart(w, r)
			case r.Method == http.MethodGet && sub == copilotProviderID+"/sign-in/status":
				// Rate-limited for the same reason as the generic status
				// route below — this one spawns a Copilot CLI process per
				// call (see signInStatusLimiter's doc comment).
				withRateLimit(signInStatusLimiter, a.handleCopilotSignInStatus)(w, r)
			case r.Method == http.MethodPost && strings.HasSuffix(sub, "/sign-in/poll"):
				// FR-044: no explicit rate-limit requirement, but a device-code
				// dialog polls repeatedly by design — signInPollLimiter is a
				// dedicated, more generous ceiling than start's (see its own
				// doc comment in rest_auth.go).
				withRateLimit(signInPollLimiter, func(w http.ResponseWriter, r *http.Request) {
					a.handleProviderSignInPoll(w, r, strings.TrimSuffix(sub, "/sign-in/poll"))
				})(w, r)
			case r.Method == http.MethodPost && sub == "openai-chatgpt/sign-in/import":
				// M2: this route was called BARE while its four FR-050
				// siblings were all wrapped. Every call rewrites the whole
				// encrypted credentials.json and re-registers every OAuth
				// value with the sensitive-value replacer — see
				// signInImportLimiter (rest_auth.go).
				withRateLimit(signInImportLimiter, a.handleProviderSignInImport)(w, r)
			case r.Method == http.MethodPost && strings.HasSuffix(sub, "/sign-in"):
				// FR-008: "rate-limited like the auth endpoints". codex-cli,
				// openai-chatgpt, and any other sign_in row land here —
				// github-copilot is already routed above.
				withRateLimit(signInStartLimiter, func(w http.ResponseWriter, r *http.Request) {
					a.handleProviderSignInStart(w, r, strings.TrimSuffix(sub, "/sign-in"))
				})(w, r)
			case r.Method == http.MethodGet && strings.HasSuffix(sub, "/sign-in/status"):
				// NOT read-only in the way the shape suggests: for a
				// device_code provider this reaches the stored-OAuth token
				// source, which refreshes against the vendor within 5
				// minutes of expiry (FR-046). It needs a ceiling like its
				// sibling routes — see signInStatusLimiter (rest_auth.go).
				withRateLimit(signInStatusLimiter, func(w http.ResponseWriter, r *http.Request) {
					a.handleProviderSignInStatus(w, r, strings.TrimSuffix(sub, "/sign-in/status"))
				})(w, r)
			case r.Method == http.MethodDelete && strings.HasSuffix(sub, "/sign-in"):
				// FR-048 sign-out: harmless no-op (NotFound = success) for a
				// cli_login provider like github-copilot, which never has a
				// "<id>_OAUTH" entry to begin with — Omnipus never sees or
				// stores its credential.
				//
				// M2: also previously bare. Not harmless in aggregate — each
				// call nils the process-wide sensitive-data replacer cache,
				// so the next scrub pays a full reflection walk of Config
				// under a write lock. See signInSignOutLimiter (rest_auth.go).
				withRateLimit(signInSignOutLimiter, func(w http.ResponseWriter, r *http.Request) {
					a.handleProviderSignOut(w, r, strings.TrimSuffix(sub, "/sign-in"))
				})(w, r)
			}
		})(w, r)

	case r.Method == http.MethodPost && strings.HasSuffix(sub, "/entitlement"):
		// POST /api/v1/providers/{id}/entitlement (ADR-067 FR-021, T067-11)
		// — "Check with my account". One live listing call per protocol,
		// intersected with the catalog and cached for the process. The
		// retired POST /providers/{id}/refresh-models is NOT its ancestor:
		// that route is gone entirely (T067-01/T067-10) and must not return.
		//
		// O3: unlike PUT and /test, this route has NO FR-050 pre-auth window —
		// authentication is required unconditionally, and the rate limiter
		// lives inside the handler. See handleProviderEntitlement.
		a.handleProviderEntitlement(w, r, strings.TrimSuffix(sub, "/entitlement"))

	case r.Method == http.MethodPost && strings.HasSuffix(sub, "/test"):
		// O5: rate limit FIRST, for the same reason as the PUT branch — this
		// route is pre-auth reachable and had no ceiling. Unlike the probe on
		// /onboarding/probe-provider, which carries the caller's own key in
		// the body, /test resolves the provider's STORED credential and spends
		// one real upstream request with the OPERATOR's key, so an unbounded
		// anonymous caller burns quota that is not theirs.
		if !rateLimitAllows(w, r, providerTestLimiter) {
			return
		}
		// POST /api/v1/providers/{id}/test — verify the provider has a valid API key.
		// Allow unauthenticated access during onboarding (same reason as PUT
		// above, and the same M3 fail-closed window).
		if !a.requireAuthOutsideOnboarding(w, r) {
			return
		}
		// Read from disk directly to avoid stale in-memory config after async reload.
		providerID := strings.TrimSuffix(sub, "/test")
		cfgData, err := os.ReadFile(a.configPath())
		if err != nil {
			errMsg := "could not read config"
			jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
			return
		}
		var cfgRaw map[string]any
		if err := json.Unmarshal(cfgData, &cfgRaw); err != nil {
			errMsg := "could not parse config"
			jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
			return
		}
		providerList, _ := cfgRaw["providers"].([]any)
		found := false
		var resolvedAPIKey string
		var firstModel string
		var configuredAPIBase string
		// credResolveErr captures a resolveCredentialRef failure when the provider
		// entry references an api_key_ref that the credential vault could NOT
		// resolve. This is distinct from "no key configured" and must surface a
		// different, actionable message that depends on WHY resolution failed —
		// see describeCredentialResolutionError (fix #5, hardened to distinguish
		// "ref not found" from "vault locked/undecryptable").
		var credResolveErr error
		for _, entry := range providerList {
			modelMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			if strings.TrimSpace(strVal(modelMap, "provider")) == providerID {
				found = true
				// Capture the provider entry's configured api_base (config.go
				// `json:"api_base"`). Preferred over the vendor default so a
				// regional host / self-hosted gateway is probed at the SAME base
				// the runtime factory will actually use (fix #3).
				configuredAPIBase = strings.TrimSpace(strVal(modelMap, "api_base"))
				// Check if API key is set: either via api_keys array or api_key_ref
				// pointing to the encrypted credentials store.
				apiKeys, _ := modelMap["api_keys"].([]any)
				apiKeyRef, _ := modelMap["api_key_ref"].(string)
				if len(apiKeys) > 0 {
					if k, _ := apiKeys[0].(string); k != "" {
						resolvedAPIKey = k
					}
				}
				if resolvedAPIKey == "" && apiKeyRef != "" {
					if v, err := a.resolveCredentialRef(apiKeyRef); err != nil {
						// A ref is present but could not be resolved — do NOT fall
						// through to "no API key configured" (misleading). The exact
						// remediation depends on WHY it failed; see
						// describeCredentialResolutionError below.
						slog.Warn("rest: provider test: credential store error", "ref", apiKeyRef, "error", err)
						credResolveErr = err
					} else {
						resolvedAPIKey = v
					}
				}
				if resolvedAPIKey == "" {
					if credResolveErr != nil {
						errMsg := describeCredentialResolutionError(credResolveErr)
						jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
						return
					}
					errMsg := "no API key configured for this provider"
					jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
					return
				}
				// Presence gate only — firstModel is passed to pickProbeModel inside
				// ValidateKey, which selects the actual probe model from the catalog.
				// A non-empty firstModel here satisfies the "has a model configured"
				// check below; the probe model may differ.
				if configuredModels, _ := modelMap["models"].([]any); len(configuredModels) > 0 {
					firstModel, _ = configuredModels[0].(string)
				}
				if firstModel == "" {
					firstModel = strVal(modelMap, "model")
				}
				break
			}
		}
		if !found {
			errMsg := fmt.Sprintf("provider %q not configured", providerID)
			jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
			return
		}
		// Resolve the base URL to probe: prefer the entry's configured api_base
		// (regional host / self-hosted gateway), fall back to the vendor default
		// (fix #3).
		baseURL := configuredAPIBase
		if baseURL == "" {
			baseURL = providers_pkg.APIBaseFor(providerID)
		}
		if baseURL == "" {
			// Neither a configured api_base nor a known vendor default — the probe
			// genuinely cannot run. Report success WITH a note rather than a silent
			// pass, so the caller knows the key was not actually verified (fix #3).
			slog.Warn("rest: provider test: no api_base or vendor default; key NOT verified",
				"provider", providerID)
			note := "API key not verified: no endpoint is configured for this provider " +
				"(set api_base) and it has no known default."
			jsonOK(w, gen.OperationResult{Success: true, Error: &note})
			return
		}
		// SEC-24: block a probe against an internal/loopback/metadata address before
		// any outbound call (fix #1). The configured api_base is caller-influenced
		// (set via PUT /providers), so it must be SSRF-checked just like the
		// onboarding endpoint override.
		if a.ssrfChecker != nil {
			if err := a.ssrfChecker.CheckURL(r.Context(), baseURL); err != nil {
				slog.Warn("rest: provider test: SSRF blocked api_base", "provider", providerID, "error", err)
				errMsg := "endpoint not allowed"
				jsonOK(w, gen.OperationResult{Success: false, Error: &errMsg})
				return
			}
		}
		if firstModel == "" && providers_pkg.DefaultProbeModel(providerID) == "" {
			// No model to probe with ANYWHERE — neither the operator's config
			// nor the registry catalog offers one, so the auth call cannot
			// run. Make the skip observable instead of silently returning
			// success (fix #4).
			//
			// ADR-067 FR-022 (T067-12): a configured model is no longer a
			// precondition. The probe model comes from the CATALOG — the first
			// active, tool-calling text model of that row in document order —
			// so a catalog provider whose entry lists no slugs is still fully
			// verified. Requiring one here used to turn "I have not picked a
			// model yet" into "your key is fine", untested.
			slog.Warn("rest: provider test: provider has no model to probe; API key not verified",
				"provider", providerID)
			jsonOK(w, gen.OperationResult{Success: true})
			return
		}
		// Auth-validation step: use the centralized providers.ValidateKey to probe
		// the key with a minimal chat-completion call. This is an informational test
		// — it does NOT persist anything. The classified outcome is returned in the
		// validation field so the SPA can surface the right icon/message per outcome.
		// SEC-16: result.RawDetail is server-debug-only; never sent to the client.
		result := providers_pkg.ValidateKey(r.Context(), providers_pkg.ValidateInput{
			ProviderID:   providerID,
			ProviderName: providers_pkg.DisplayName(providerID),
			BaseURL:      baseURL,
			APIKey:       resolvedAPIKey,
		}, a.ssrfChk())
		slog.Debug("rest: provider test: classification complete",
			"provider", providerID, "outcome", result.Outcome, "detail", result.RawDetail)
		success := !result.Blocks()
		resp := gen.OperationResult{Success: success}
		if result.Outcome != providers_pkg.OutcomeValid {
			outcomeStr := gen.OperationResultValidationOutcome(result.Outcome)
			// Guard: only assign the validation object when the cast is a known wire value.
			if !outcomeStr.Valid() {
				slog.Warn("rest: provider test: unrecognized validation outcome; omitting validation field",
					"provider", providerID, "outcome", result.Outcome)
			} else {
				msg := result.Message
				resp.Validation = &struct {
					Message *string                              `json:"message,omitempty"`
					Outcome gen.OperationResultValidationOutcome `json:"outcome"`
				}{
					Outcome: outcomeStr,
					Message: &msg,
				}
			}
		}
		if result.Blocks() {
			resp.Error = &result.Message
		}
		jsonOK(w, resp)

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// dedupeNonEmpty trims, drops empties, and de-duplicates a slice of strings,
// preserving first-seen order. Returns nil when the result is empty so callers
// can distinguish "no entries" from "an explicit empty list" at the call site.
func dedupeNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
