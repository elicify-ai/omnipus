# Feature Specification: Centralized Provider-Key Validation

**Created**: 2026-06-29
**Status**: Draft — **Revision 1** (post-grill; all 4 CRITICAL + 7 MAJOR findings resolved, see *Revision 1 — Authoritative Resolutions*)
**Input**: [ADR-025 — Centralized Provider Validation](../architecture/ADR-025-centralized-provider-validation.md) (with Revision 2, post-grill); grill report [ADR-025-…-review.md](../architecture/ADR-025-centralized-provider-validation-review.md) (verdict REVISE — all findings resolved). Four open questions resolved with the operator on 2026-06-29 (see *Resolved Decisions*).

---

## Summary

Today, provider API-key validation is **duplicated and inconsistent** across the gateway: two private functions (`fetchUpstreamModels`, `testProviderAuth` in `pkg/gateway/rest.go`) are wired into the browser onboarding probe and the Settings **Test** button, while the Settings **Save** (`PUT /providers/{id}`) and the **CLI onboard** flow perform **no real auth check at all**. The result: a user can save an invalid OpenRouter key during onboarding and only discover it when the first chat returns `401 "User not found"` (the original bug that motivated this work).

This feature **centralizes** all provider-key validation into a single, classified validator in `pkg/providers` (`ValidateKey` + `FetchModels`), wires it into **all four** flows, and replaces the old binary pass/fail with a **body-driven, per-provider, classified outcome**: `Valid · InvalidKey · NoCredit · Unreachable · Restricted`. Only a **confirmed-wrong key** (`InvalidKey`) blocks; the other three proceed with a hand-crafted, plain-English, per-type warning. Raw provider error bodies never reach the user (SEC-16) — only the curated message.

---

## Resolved Decisions (operator, 2026-06-29)

| # | Question | Decision |
|---|----------|----------|
| D1 | **PUT-rejection wire shape** | **Hybrid.** `InvalidKey` → HTTP **422** with `{error}` (SPA `request<Provider>` throws → blocking toast, key NOT persisted). Warning outcomes → HTTP **200** `Provider` carrying a new optional `validation: {outcome, message}` object (key persisted, non-blocking warning). |
| D2 | **Matrix breadth / extensibility** | **Data-driven + known set.** A rules table with explicit entries for the 6 menu providers (OpenRouter, OpenAI, Anthropic-compat, Gemini-compat, Groq, DeepSeek) **plus** a generic status+marker fallback for `Other`/custom. New providers added as data, not code branches. |
| D3 | **SPA warning UI distinctness** | **Distinct copy + per-type icon, one banner style.** Each non-blocking outcome shows its own catalog message and icon (NoCredit → wallet, Unreachable → wifi-slash, Restricted → lock), all sharing one amber warning-banner treatment. |
| D4 | **Audit** | **Log both.** Audit-log the warning-proceeds (`NoCredit`/`Unreachable`/`Restricted`, with provider + outcome) **and** CLI `--skip-verify` overrides. Info-level; never the key or raw body. |

These four are **locked**. The rest of the design is inherited from ADR-025 Revision 2 (validator home `pkg/providers`, `URLChecker` interface, only-`InvalidKey`-blocks policy, FR-7 message catalog, FR-8 `pickProbeModel`).

---

## Revision 1 — Authoritative Resolutions (2026-06-29, post-grill)

> The spec was grilled against the live codebase ([…-spec-review.md](provider-validation-centralization-spec-review.md), verdict REVISE: 4 CRITICAL, 7 MAJOR). **This section is the single source of truth and supersedes any conflicting wording later in the document.** Each resolution is grounded in verified code.

### R-A — Classifier marker sets + precedence (resolves C1, M2, M5, M6, m5)

The classifier is **not** "status only" and **not** "reuse the legacy `authRejectionMarkers` list as-is." The legacy list (`pkg/gateway/rest.go:1225` `authRejectionMarkers`) lumps `permission_denied` and `unauthenticated` in with real key-invalid markers — correct for a *400-upgrade* heuristic, but **wrong for 403**, where those two mean *region/permission*, not *wrong key*. Using the legacy list verbatim would classify a Gemini `403 PERMISSION_DENIED` as `InvalidKey` and **block** — re-introducing the F7 lockout. The new classifier therefore defines **two narrow, explicit sets** and a fixed precedence.

**Credential-marker set** (lowercased substring match → candidate `InvalidKey`). Deliberately **excludes** `permission_denied` and `unauthenticated`:
```
api_key_invalid · api key not valid · invalid api key · invalid_api_key ·
incorrect api key · incorrect_api_key · no auth credentials · invalid x-api-key ·
revoked · authentication_error · authentication fails
```

**Credit-marker set** (lowercased substring match → `NoCredit`). Reuse `error_classifier.go`'s `billingPatterns` + `matchesAny` primitive (m5 — do NOT build a second billing list); extend it with these if absent:
```
insufficient_quota · insufficient quota · insufficient credits · insufficient balance ·
credit balance is too low · exceeded your current quota · out of credits ·
billing · payment required
```

**Precedence — first match wins** (the closed algorithm Dataset A tests against):
```
classify(transportErr, status, body):
  1. transportErr (timeout / DNS / conn refused / TLS)        -> Unreachable
  2. parse body -> (msg, embeddedErrPresent, embeddedCode)
  3. if status == 200:
        if !embeddedErrPresent                                -> Valid
        else: status = embeddedCode (numeric) or 0; continue  # 200-with-error is NEVER a clean pass
  4. m := lower(msg)
     if creditMarker(m)                                       -> NoCredit      # e.g. Anthropic 400 "credit balance too low"
     if credentialMarker(m)                                   -> InvalidKey     # narrow set; key-invalid keys off the MESSAGE, not a status string
  5. switch status:
        401            -> InvalidKey
        402            -> NoCredit
        403            -> Restricted      # permission/region; NOT a wrong key (markers above already caught revoked keys)
        400            -> Valid           # auth reached; a request-level error (bad model) is not a key problem
        404            -> Unreachable     # wrong base / missing endpoint; can't confirm
        429            -> Unreachable     # rate-limited; transient (credit-429 already caught at step 4)
        5xx            -> Unreachable
        2xx (non-200)  -> Valid
        0 / other      -> Unreachable     # 200-with-unrecognised-error, or anything unmapped -> proceed w/ warning
```

Consequences that fix the grill findings:
- **C1**: `403 PERMISSION_DENIED` → step 4 no marker → step 5 `403` → **Restricted** (proceed). A `403 "revoked api key"` → step 4 credential-marker → **InvalidKey**. Both hold; A9 is now reachable.
- **M6**: the Gemini key-invalid discriminator is the **message** substring `api key not valid` (step 4), NOT the `INVALID_ARGUMENT` status string. A Gemini `400 INVALID_ARGUMENT "model not found"` → no marker → `400` → **Valid**.
- **M5**: a `429` with no credit marker (empty/opaque body, or pre-auth rate-limit) → **Unreachable**. "Pre-auth vs post-auth" is **not** detected — only "has a credit marker or not."
- **M2/SC-001 oracle**: the two sets above + this algorithm ARE the spec of FR-003/FR-004. Dataset A is the closed conformance test of *this* algorithm; "0 misclassifications" now has a definite oracle.

### R-B — Contract breadth: `validation` on three response types (resolves C2)

`validation` is an optional object `{outcome, message}` (new `ProviderValidation` schema). It MUST be added to **all three** response types the four flows return — not just `Provider`:
- `Provider` (PUT save 200) — already in FR-018.
- `ProbeProviderResponse` (onboarding probe) — currently `{success, models, error}`, `required:[success]`. Add optional `validation`.
- `OperationResult` (Settings Test) — currently `{success, error}`, `additionalProperties:false`. Add `validation` as a **defined** optional property (additive; `additionalProperties:false` forbids only *undeclared* keys, so a declared optional field is legal).

The 422 InvalidKey block body is the gateway's standard error envelope `{error: string}` emitted by `jsonErr(w, status, msg)` and parsed by the SPA's `ApiError.fromResponse` (`src/lib/api-error.ts`) — no new error schema (m1). FR-018 and the contract step expand accordingly; `make verify-contracts` must pass with all three schemas regenerated.

### R-C — "Key changed" = a non-empty `api_key` in the request body (resolves C3)

"Validate only when the key changed" means exactly: **`req.ApiKey != nil && *req.ApiKey != ""`** — the request body carries a new, non-empty key. This is byte-checkable at the handler with **no decryption** (the stored key is a credential-*ref*; comparing plaintext to the decrypted stored value is explicitly NOT done). Therefore:
- A PUT that omits `api_key` (or sends empty) → **no probe** (model/label-only edits stay free). This is what makes SC-005 hold.
- Re-submitting the **same** key value → **does** re-probe (it's still a key submission; acceptable cost, billing is one `max_tokens:1` completion).
- The SPA **MUST NOT** resend the masked/unchanged key on an unrelated edit (asserted by a vitest, m4). `configureProvider` already only sets `body.api_key` when its arg is `!== undefined` — the UI must pass `undefined` unless the user typed a new key.

### R-D — PUT handler ordering + re-auth interaction (resolves M3, M4)

The PUT save handler runs in this fixed order:
```
1. re-auth gate: requireReAuth(...)            # existing single-use consent token (rest.go:4789)
2. detect key-changed (R-C)                     # if not changed -> skip 3-6, persist non-key fields, 200 no validation
3. resolve persisted api_base + SSRF check      # a.ssrfChecker.CheckURL(persisted api_base)  (NOT a request field)
4. ValidateKey(ctx, in, a.ssrfChecker)
5. if InvalidKey      -> 422 {error}, persist NOTHING (key not stored)
   if store locked     -> 503, persist nothing (existing behaviour, rest.go:4858)
6. else storeCredential + TriggerReload
      if reload fails  -> 500 (existing behaviour, rest.go:4936); key IS persisted (document: validation already passed)
7. attach validation (warning outcomes) to the 200 Provider
```
**Re-auth/422 trap (M4):** the consent token is single-use and consumed at step 1, *before* validation. A 422 at step 5 therefore **burns the token** — on retry of a corrected key the user must re-authenticate. This is **accepted and documented**: the SPA MUST handle "422 → prompt for re-auth on the next save attempt." (Validating *before* consuming consent was considered and rejected — it would issue a billable upstream probe before step-up consent.)

### R-E — `pickProbeModel`: no OpenRouter "account-filtered" list (resolves M1)

**There is no account-filtered model API.** `GET /models` returns the full public catalog for OpenRouter (the very reason a completion probe is needed). `pickProbeModel(catalog, providerID)` selects a **known-good default chat slug** from the rules-table per provider (filtering the catalog to exclude obvious non-chat entries — names containing `embed`, `whisper`, `tts`, `dall-e`, `image`, `rerank`, `moderation`), and falls back to the provider's rules-table default if the catalog is empty. A model the *account* cannot access degrades to `Unreachable`/`Valid` (per R-A), **never** a false `InvalidKey` — that safety property is what matters, not account-accurate model selection. The phrase "account-filtered" is struck from FR-009/US5 AS3/Test #6 and moved to Out of Scope.

### R-F — CLI audit is best-effort, gateway audit is the hard requirement (resolves C4)

The CLI onboard path (`applyInput`) has **no `audit.Logger`** (only the gateway has `a.auditor`). Resolution:
- **Gateway flows** (PUT save warning-proceed): hard requirement — write `provider_key_validated` via `a.auditor` (FR-017).
- **CLI flows** (`--skip-verify`, CLI warning-proceed): **best-effort** — the CLI emits a structured `slog` line to its log (`provider_key_validation_skipped` / `provider_key_validated`, provider + outcome/flag, never the key). It MAY construct an `audit.Logger` against `$OMNIPUS_HOME/logs/audit` with the gateway's config **if cheaply available**, but onboard MUST NOT fail if audit is unavailable. Tests #18/#20 assert the **CLI log line** (not a gateway audit entry); a new gateway audit test asserts the `a.auditor` entry.

Per O2, audit only the **persisting** flows (PUT save, CLI persist) — NOT the informational onboarding-probe / Settings-Test (which don't persist), to avoid audit spam on retry.

### R-G — SC-003 is verified by Test #10 alone, not the holdout (resolves M7)

SC-003 ("the original bug no longer reproduces") is gated **only** by automated Test #10: an `httptest` server that serves a 200 public `/models` (no auth) AND a 401 on `/chat/completions` — exactly the original-bug shape (public catalog + bad key). The holdout (H1–H7) is confidence-only and is NOT a success-criterion oracle.

### R-H — Minor fixes (m1, m2, m3, m4, m5, O4)
- **m1**: 422 body = `jsonErr(w, 422, msg)` → `{error: msg}`; parsed by `ApiError.fromResponse`.
- **m2**: the onboarding provider step and the Settings Test result **reuse the same per-type warning banner** component as the PUT path (US8 / D3).
- **m3**: the PUT handler is the `case r.Method == http.MethodPut && sub != "" && !strings.HasSuffix(sub, "/test")` branch (`rest.go:4774`) — match the `case`, not a line offset.
- **m4**: a vitest asserts `configureProvider` omits `api_key` when only `model`/`models` changed (so SC-005's premise holds).
- **m5**: the new `classify` reuses `error_classifier.go`'s `matchesAny` + `extractHTTPStatus` + `billingPatterns` primitives; it adds only the **credential-marker** set (the failover classifier has no "invalid key" notion). No second billing list.
- **O4**: native Anthropic (`/messages`, not `/chat/completions`) probes as **Unreachable** (proceeds with warning) — accepted. "Anthropic-compat" rows (A13/A14) assume the OpenAI-shaped `…/anthropic/v1` endpoints. Documented, not a defect.

---

## Available Reference Patterns

> Existing in-repo implementations this spec migrates from or reuses. No `docs/reference/` library applies.

| Reference | Pattern | Relevance to This Feature |
|-----------|---------|---------------------------|
| `pkg/gateway/rest.go:1157` `fetchUpstreamModels` | Catalog fetch (`GET {base}/models`, SSRF-checked, returns `[]string`) | **Move** verbatim to `providers.FetchModels` (behaviour-preserving). 3 catalog-only callers re-point. |
| `pkg/gateway/rest.go:1281` `testProviderAuth` | Auth probe (`POST {base}/chat/completions`, status+marker discrimination, SEC-16 message hygiene) | **Absorb** into `providers.ValidateKey` and **upgrade** binary pass/fail → 5-way classified outcome. Delete the gateway copy. |
| `pkg/gateway/rest.go:1249` `authRejectionMarkers` | Credential-marker substring list (`API_KEY_INVALID`, `api key not valid`, …) | Reuse + extend into the data-driven classifier's marker sets (credential markers + credit markers). |
| `pkg/security/ssrf.go:245,326` `SSRFChecker.CheckURL` / `SafeClient` | SSRF guard (blocks RFC-1918/loopback/`169.254.169.254`, redirect-safe client) | Already satisfies the new `URLChecker` interface — gateway passes `a.ssrfChecker`; CLI passes `nil`. |
| `pkg/providers/passthrough.go` `IsPassthroughProvider` | Flags providers whose `/models` is public/keyless (openrouter, vivgrid) | Explains **why** the catalog fetch alone is insufficient and a real completion probe is required. |
| `pkg/providers/factory_provider.go` `GetDefaultAPIBase` | Resolve a provider's default base URL | Used by `pickProbeModel` defaults and CLI onboard when no api_base persisted. |
| ADR-004 credential boot contract | Secrets in `credentials.json`, `<field>_ref` in config | Save/CLI paths resolve the key from the store, never log it. |

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `providers.ValidateKey` (NEW, `pkg/providers/validate.go`) | create | The single validator. Signature `func ValidateKey(ctx, in ValidateInput, checker URLChecker) ValidationResult`. |
| `providers.FetchModels` (NEW) | create (moved) | Catalog-only fetch, ex-`fetchUpstreamModels`. |
| `providers.URLChecker` (NEW interface) | create | `CheckURL(ctx, url) error` + `SafeClient() *http.Client`. `*security.SSRFChecker` satisfies it; `nil` = no SSRF guard (CLI). |
| `providers.pickProbeModel` (NEW) | create | Picks a confirmed chat-capable model from the catalog (NOT `models[0]`). |
| `providers.classify` (NEW, internal) | create | Data-driven body classifier → `Outcome`. |
| `pkg/gateway/rest.go:1157` `fetchUpstreamModels` | delete | Replaced by `providers.FetchModels`. |
| `pkg/gateway/rest.go:1281` `testProviderAuth` | delete | Replaced by `providers.ValidateKey`. |
| `HandleOnboardingProbeProvider` (`rest_onboarding.go:392`) | modify | Re-point to `providers.FetchModels` + `providers.ValidateKey`; emit `validation` in probe response. |
| `PUT /providers/{id}` handler (`rest.go:~4775`) | modify | **NEW** validation-on-save (key-changed only); 422 on `InvalidKey`, 200+`validation` on warning; SSRF the persisted api_base. |
| `POST /providers/{id}/test` handler (`rest.go:5093`) | modify | Re-point to `providers.ValidateKey`; carry `validation` in `OperationResult`. |
| `rest.go:4717`, `rest.go:5195` (catalog callers) | modify | Re-point `fetchUpstreamModels` → `providers.FetchModels` (behaviour-preserving). |
| `cmd/omnipus/internal/onboard/onboard.go` | modify | **NEW** validation before persist; `--skip-verify` flag; `--non-interactive` abort-on-`InvalidKey`. |
| `src/lib/api.ts:1440` `configureProvider` | modify | Return `validation` from 200; let 422 throw. New rejection/warning branch in callers. |
| Settings provider panel + onboarding provider step (SPA) | modify | Render per-type warning banner (D3) / blocking error. |
| `contracts/components/schemas/ProviderValidation.yaml` (NEW) | create | `{outcome, message}` reusable schema. |
| `contracts/components/schemas/Provider.yaml` | modify | Add optional `validation` ref (PUT 200). |
| `contracts/components/schemas/ProbeProviderResponse.yaml` | modify | **C2** — add optional `validation` ref (onboarding probe). |
| `contracts/components/schemas/OperationResult.yaml` | modify | **C2** — add optional `validation` ref (Settings Test); declared optional, legal under `additionalProperties:false`. |
| `pkg/providers/error_classifier.go` | reuse | **m5** — `classify` reuses `matchesAny`/`extractHTTPStatus`/`billingPatterns`; no second billing list. |
| `pkg/audit` entry emit | modify | New `provider_key_validated` / `provider_key_validation_skipped` events (D4). |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents (WILL break / must update) | d=2 Dependents (SHOULD test) |
|-----------------|------------|-------------------------------------------|------------------------------|
| `fetchUpstreamModels` (delete) | **MEDIUM** | `rest.go:4717,5195`, `rest_onboarding.go:454` | onboarding probe response; provider list refresh |
| `testProviderAuth` (delete) | **HIGH** | `rest_onboarding.go:473`, `rest.go:5093`; tests `rest_onboarding_test.go:969-1060`, `uat_fixes_test.go` | onboarding gate; Settings Test button |
| `PUT /providers/{id}` (behaviour) | **HIGH** | SPA `configureProvider` (`api.ts:1440`) + every caller of it (Settings, onboarding) | onboarding completion; provider edit |
| `Provider.yaml` (+`validation`) | **MEDIUM** | generated Go (`pkg/api/generated`) + TS (`src/lib/api/generated`); `make verify-contracts` | every Provider consumer (must tolerate optional field) |
| CLI `onboard.go` | **MEDIUM** | `cmd/omnipus` onboard path; `realgateway_integration_test.go` | CI onboarding scripts using `--non-interactive` |
| audit events | **LOW** | audit log readers | none |

> **Flagged HIGH:** the `PUT` behaviour change and `testProviderAuth` deletion are the blast-radius hotspots. The `403 → Restricted/proceed` reclassification is a **deliberate behaviour change** (onboarding that previously *failed* on a 403 now *succeeds with a warning*) — see *Regression Test Requirements*.

### Relevant Execution Flows

| Flow | Relevance |
|------|-----------|
| Browser onboarding → provider step → probe | Calls `ValidateKey`; gate proceeds unless `InvalidKey`. The original 401-bug flow. |
| Settings → Providers → **Test** | Informational probe; surfaces classified outcome. |
| Settings → Providers → **Save** (PUT) | NEW gate: blocks save on `InvalidKey`, warns otherwise. |
| CLI `omnipus onboard` (interactive + `--non-interactive`) | NEW gate before persisting the key; `--skip-verify` bypass. |
| Provider catalog/model refresh | Uses `FetchModels` only — unchanged behaviour. |

### Cluster Placement

This feature belongs to the **provider/credentials** cluster, spanning `pkg/providers` (new home), `pkg/gateway` (4 handlers), `cmd/omnipus` (CLI), `pkg/security` (SSRF reuse), `pkg/audit`, `contracts/`, and `src/` (SPA). It is **cross-cutting** — the whole point is to collapse N divergent validators into one.

---

## User Stories & Acceptance Criteria

### User Story 1 — One validator, four flows (Priority: P0)

A developer maintaining Omnipus wants **one** place that decides "is this provider key usable?", so that the browser onboarding, Settings Test, Settings Save, and CLI onboard all behave **identically** and a fix lands once. Today the logic is forked: two flows use the gateway's private functions, two do no real check. This story extracts `ValidateKey` + `FetchModels` into `pkg/providers`, deletes the gateway copies, and re-points all callers.

**Why this priority**: P0 — every other story depends on the centralized validator existing. It is also the root-cause fix for the "saved a bad key, discovered it at first chat" bug.

**Independent Test**: Unit-test `providers.ValidateKey` against an `httptest` server in isolation (no gateway, no CLI); assert the 5-way classification. The package compiles and is importable by both `pkg/gateway` and `cmd/omnipus` (verified: CLI already imports `pkg/providers`).

**Acceptance Scenarios**:

1. **Given** `pkg/gateway` and `cmd/omnipus`, **When** the build runs, **Then** both import `providers.ValidateKey`/`FetchModels` and no `fetchUpstreamModels`/`testProviderAuth` symbol remains in `pkg/gateway` (deleted).
2. **Given** the 3 catalog-only call sites (`rest.go:4717`, `rest.go:5195`, `rest_onboarding.go:454`), **When** they fetch the model list, **Then** they call `providers.FetchModels` and return the **same** catalog they returned before (behaviour-preserving).
3. **Given** the 2 validating call sites (onboarding probe, Settings Test), **When** they validate a key, **Then** they call `providers.ValidateKey` and receive a classified `ValidationResult`.

---

### User Story 2 — Classified, body-driven outcomes (Priority: P0)

An operator entering a provider key wants the system to tell them **specifically what's wrong** — a rejected key vs. an empty balance vs. a network blip vs. a regional block — so they take the right corrective action instead of guessing. The current binary "auth failed / auth ok" conflates a genuinely-wrong key (must fix) with a no-credit or unreachable condition (key is fine, proceed).

**Why this priority**: P0 — the classification is the core deliverable and drives the block/proceed policy and every user-facing message.

**Independent Test**: Feed `classify` the per-provider body fixtures (see *Test Datasets*) and assert each maps to the correct `Outcome`, independent of any HTTP transport.

**Acceptance Scenarios**:

1. **Given** a `401` (or `400`+credential-marker, e.g. Gemini `API_KEY_INVALID`), **When** classified, **Then** outcome = `InvalidKey` and `Blocks=true`.
2. **Given** a `403` region/permission block (no credential marker), **When** classified, **Then** outcome = `Restricted` and `Blocks=false`.
3. **Given** OpenRouter/DeepSeek `402` or OpenAI `429 insufficient_quota`, **When** classified, **Then** outcome = `NoCredit` and `Blocks=false`.
4. **Given** a transport error, timeout, `5xx`, or a pre-auth generic `429`, **When** classified, **Then** outcome = `Unreachable` and `Blocks=false`.
5. **Given** a `200` with no embedded error, **When** classified, **Then** outcome = `Valid`.
6. **Given** a `200` whose body embeds an `error` object, **When** classified, **Then** the embedded error is classified by the same marker rules (NOT treated as a clean pass).
7. **Given** an unknown/custom provider, **When** classified, **Then** the generic status+marker fallback applies (no per-provider rule required).

---

### User Story 3 — Only a confirmed-wrong key blocks (Priority: P0)

A user with a valid-but-empty-balance key, or a key that can't be checked because the network is down, wants to **proceed** with a clear heads-up rather than be locked out of onboarding. Conversely, a user who pasted a wrong key wants to be **stopped** before it's saved. The policy: **only `InvalidKey` blocks**; `NoCredit`/`Unreachable`/`Restricted` proceed with a type-specific warning.

**Why this priority**: P0 — this is the safety/UX contract. Getting it wrong either locks people out (over-blocking, the grill's F7 regression) or silently saves bad keys (under-blocking, the original bug).

**Independent Test**: Map each `Outcome` → `(blocks bool, message string)` and assert the policy table holds with no transport involved.

**Acceptance Scenarios**:

1. **Given** outcome `InvalidKey`, **When** the policy is applied, **Then** the flow is **blocked** (onboarding probe fails; PUT returns 422 and does NOT persist; CLI re-prompts/aborts).
2. **Given** outcome `NoCredit`, `Unreachable`, or `Restricted`, **When** the policy is applied, **Then** the flow **proceeds** and surfaces the matching warning.
3. **Given** outcome `Valid`, **When** the policy is applied, **Then** the flow proceeds with no warning.

---

### User Story 4 — Plain-English, safe messages (Priority: P0)

A non-technical user wants the failure message in **plain English** they can act on, never a raw provider error blob (which may be cryptic, or worse, reflect the key). Each outcome has one hand-crafted message, parameterized by provider name; the raw upstream detail goes only to the server debug log (SEC-16).

**Why this priority**: P0 — directly requested by the operator ("a well crafted message so that normal user understand it… not… the technical provider message").

**Independent Test**: For each `(Outcome, providerName)`, assert `ValidationResult.Message` equals the catalog template with `{Provider}` filled, and assert `Message` never contains the raw body, the API key, or provider-internal codes.

**Acceptance Scenarios** (catalog is verbatim from ADR-025 FR-7):

1. **Given** `InvalidKey` for provider "OpenRouter", **When** the message is built, **Then** it reads *"The API key was rejected by OpenRouter. Check you copied the whole key and that it's still active in your OpenRouter account."*
2. **Given** `NoCredit`, **Then** *"Your OpenRouter key works, but the account has no credit. Add funds in your OpenRouter dashboard to use it."*
3. **Given** `Unreachable`, **Then** *"Couldn't reach OpenRouter to check the key — check your internet connection. Continuing for now; the key will be used as entered."*
4. **Given** `Restricted`, **Then** *"Your OpenRouter key works, but OpenRouter blocked this request (it may be restricted in your region, or the selected model isn't available to your account)."*
5. **Given** any outcome, **When** the message is built, **Then** it contains neither the API key, the raw upstream body, nor provider-internal status codes (e.g. `INVALID_ARGUMENT`); those appear only in the server debug log.

---

### User Story 5 — Probe the right model (Priority: P1)

The validator wants to probe a model it knows is **chat-capable**, so a `400 "model not found"` (from probing an embedding/audio/image entry, or an account-unavailable slug) is never mistaken for a key problem. Today the code probes `models[0]`, which the catalog does not guarantee is a chat model (grill F3).

**Why this priority**: P1 — without it, `pickProbeModel` correctness undermines the classification (false `Restricted`/`Valid`), but the validator is still wired and testable with a forced model.

**Independent Test**: Call `pickProbeModel(catalog, providerID)` with catalogs that interleave embedding/audio/image entries and assert it returns a chat model; for OpenRouter, assert it returns a known-good default chat slug from the catalog/rules-table (no account-filtered list exists — R-E).

**Acceptance Scenarios**:

1. **Given** a catalog `["text-embedding-3-small", "gpt-4o-mini", "dall-e-3"]` for OpenAI, **When** `pickProbeModel` runs, **Then** it returns `gpt-4o-mini` (a chat model), not `models[0]`.
2. **Given** an empty catalog, **When** `pickProbeModel` runs, **Then** it returns the provider's known default chat model (via `GetDefaultAPIBase`/provider defaults) or, if none, signals that validation must fall back to catalog-fetch-only (no false `InvalidKey`).
3. **Given** OpenRouter, **When** `pickProbeModel` runs, **Then** the probe model is a known-good default chat slug from the catalog/rules-table (there is no account-filtered list — R-E); a model the account can't access degrades to `Unreachable`/`Valid`, never false `InvalidKey`.

---

### User Story 6 — Settings Save now validates (Priority: P0)

An operator changing a provider key in **Settings → Providers → Save** wants the same protection as onboarding: a wrong key is rejected before it's persisted; a working-but-flagged key saves with a warning. Today `PUT /providers/{id}` performs no auth check and always returns `200 connected`.

**Why this priority**: P0 — the second-largest gap (alongside CLI); closing it is the operator's explicit ask ("also when a new provider is configured under settings").

**Independent Test**: Drive `PUT /providers/{id}` with a stubbed upstream returning each status; assert `InvalidKey` → 422 + key not persisted, warning → 200 + `validation` populated + key persisted, and that an edit **not** touching the key field runs **no** completion probe.

**Acceptance Scenarios**:

1. **Given** a PUT whose `api_key` field changed to a wrong key, **When** the handler runs, **Then** it returns **HTTP 422** with `{error: <InvalidKey message>}` and the key is **not** persisted.
2. **Given** a PUT whose `api_key` changed to a working-but-no-credit key, **When** the handler runs, **Then** it returns **HTTP 200** `Provider` with `validation: {outcome: "no_credit", message: …}` and the key **is** persisted.
3. **Given** a PUT whose body omits `api_key` (or sends it empty) while changing only `model`/`models`, **When** the handler runs, **Then** **no** completion probe fires (no billable call) and the response carries no `validation`. (R-C: "changed" = a non-empty `api_key` in the body; re-sending the *same* key value WOULD re-probe — the SPA must omit `api_key` unless the user typed a new one.)
4. **Given** a PUT with a persisted `api_base`, **When** the handler validates, **Then** the **persisted** `api_base` is SSRF-checked (request body carries no `api_base` — contract unchanged).

---

### User Story 7 — CLI onboard validates, with an escape hatch (Priority: P0)

An operator running `omnipus onboard` (interactively or `--non-interactive` in CI) wants the key validated before it's written, with a `--skip-verify` flag to bypass the check entirely (air-gapped/offline setup, known-good key, faster CI). Today CLI onboard writes the key with no check.

**Why this priority**: P0 — the operator explicitly asked to "close that gap as well in the CLI onboarding."

**Independent Test**: Run the onboard apply path with a stub upstream: interactive `InvalidKey` re-prompts; `--non-interactive` `InvalidKey` exits non-zero; `--skip-verify` writes the key with **no** probe; warnings print and proceed.

**Acceptance Scenarios**:

1. **Given** interactive onboard and a wrong key, **When** validation runs, **Then** the CLI prints the `InvalidKey` message and re-prompts for the key (does not persist).
2. **Given** `--non-interactive` and a wrong key (no `--skip-verify`), **When** validation runs, **Then** the CLI exits non-zero with the `InvalidKey` message and persists nothing.
3. **Given** `--skip-verify` (with or without `--non-interactive`), **When** onboard runs, **Then** **no** completion probe fires, the key is persisted, and a skip is audit-logged.
4. **Given** a `NoCredit`/`Unreachable`/`Restricted` outcome, **When** onboard runs, **Then** the CLI prints the warning and proceeds to persist.

---

### User Story 8 — SPA surfaces blocks and warnings (Priority: P0)

A user in the browser wants to **see** the blocking error or the warning. `configureProvider` (`api.ts:1440`) must gain a rejection/warning branch: a 422 throws (blocking toast, save aborted); a 200 with `validation` renders a per-type warning banner (D3: wallet/wifi-slash/lock icon, amber, distinct copy).

**Why this priority**: P0 — without the SPA branch, the new server behaviour is invisible; the user still can't tell why a save failed.

**Independent Test**: Vitest the `configureProvider` wrapper + the warning-banner component against mocked 422 and 200+`validation` responses; assert throw-vs-banner and correct icon per outcome.

**Acceptance Scenarios**:

1. **Given** PUT returns 422, **When** `configureProvider` resolves, **Then** it throws and the caller shows a blocking error toast with the message; the form stays open.
2. **Given** PUT returns 200 with `validation.outcome = no_credit`, **When** `configureProvider` resolves, **Then** the save is treated as succeeded and an amber warning banner with the **wallet** icon and the NoCredit copy is shown.
3. **Given** `validation.outcome` of `unreachable` / `restricted`, **Then** the banner shows the **wifi-slash** / **lock** icon respectively with the matching copy.
4. **Given** a 200 with no `validation` (or `outcome=valid`), **Then** no banner is shown.

---

### User Story 9 — Audit trail for non-clean acceptances (Priority: P2)

A security-minded operator wants an audit record whenever a key was accepted **without** a clean pass — a warning-proceed (`NoCredit`/`Unreachable`/`Restricted`) or a CLI `--skip-verify` override — so there's a trail of "this key was used despite not fully verifying." No key or raw body in the entry (D4).

**Why this priority**: P2 — valuable for traceability but not on the critical path; the feature is safe without it.

**Independent Test**: Drive a warning-proceed and a `--skip-verify` and assert the audit log gains `provider_key_validated` (with `outcome`, `action=proceeded`) and `provider_key_validation_skipped` (with `source`, `flag`) respectively, neither containing the key.

**Acceptance Scenarios**:

1. **Given** a warning-proceed outcome, **When** the flow proceeds, **Then** an audit entry `provider_key_validated{provider, outcome, action=proceeded}` is written.
2. **Given** CLI `--skip-verify`, **When** onboard persists, **Then** an audit entry `provider_key_validation_skipped{provider, source=cli, flag=--skip-verify}` is written.
3. **Given** any audit entry from this feature, **When** inspected, **Then** it contains neither the API key nor the raw upstream body.

---

### Edge Cases

- **Empty / whitespace-only key** → short-circuit to `InvalidKey` *without* a network call (no point probing an empty credential). Message: the `InvalidKey` catalog line.
- **Passthrough `/models` is public (OpenRouter, vivgrid)** → catalog fetch can succeed with a bad key; the completion probe is the authority. (The original bug.)
- **`200 OK` with an embedded `{"error": …}` body** → classify the embedded error, do NOT treat as `Valid` (grill F8).
- **Pre-auth `429`** (rate-limited before the key is even checked) → `Unreachable` (transient), not `NoCredit`.
- **`404`** (wrong base URL or missing endpoint, key not rejected) → `Unreachable` (can't confirm; proceed with warning) — NOT `Valid`, NOT `InvalidKey`.
- **`403` with a credential marker** (rare; some providers use 403 for revoked keys) → `InvalidKey` (marker beats status).
- **Generic `400` "model not found"** (no credential marker) → `Valid` (the key authenticated; the request-level error is not a key problem). With `pickProbeModel` this should be rare.
- **Empty catalog + no known default model** → fall back to catalog-fetch-only; never emit a false `InvalidKey` (US5 AS2).
- **PUT edits an unrelated field with key unchanged** → no probe (US6 AS3).
- **CLI offline + `--skip-verify`** → no probe, persisted, audited.
- **CLI offline without `--skip-verify`** → `Unreachable` warning, proceeds (does NOT block).
- **SSRF: persisted `api_base` is a private/loopback address** → `CheckURL` rejects before any probe; surfaced as a save error (not a provider outcome). CLI (`nil` checker) does **not** SSRF-guard — accepted (operator-run, US story note).
- **Concurrent PUTs to the same provider** → last-write-wins on the persisted key; each validates its own candidate key independently.

---

## Behavioral Contract

- When `ValidateKey` receives an empty/whitespace key, the system returns `InvalidKey` without a network call.
- When the probe returns `401`, or any status with a credential marker, the system returns `InvalidKey` and blocks.
- When the probe returns `403` without a credential marker, the system returns `Restricted` and proceeds with a warning.
- When the probe returns `402`, or `429` with a credit marker, the system returns `NoCredit` and proceeds with a warning.
- When the probe times out, errors at transport, returns `5xx`, `404`, or a pre-auth `429`, the system returns `Unreachable` and proceeds with a warning.
- When the probe returns `200` with no embedded error, the system returns `Valid` and proceeds silently.
- When the probe returns `200` with an embedded error body, the system classifies that error (never a silent `Valid`).
- When the outcome is anything but `InvalidKey`, the flow proceeds; only `InvalidKey` blocks.
- When a user-facing message is produced, it is the curated catalog line for that outcome+provider — never the raw provider body or the key.
- When `PUT` validates and the key is wrong, the save returns 422 and persists nothing.
- When `PUT` validates and the key only warns, the save returns 200 with `validation` and persists.
- When `PUT` changes no key field, no completion probe fires.
- When the CLI runs with `--skip-verify`, no probe fires and a skip is audited.
- When a key is accepted on a warning or skipped, the system writes an audit entry without the key.

## Explicit Non-Behaviors

- The system must **not** block onboarding/save on `NoCredit`, `Unreachable`, or `Restricted`, because a valid key with an empty balance, a transient network failure, or a regional block is not a wrong key (re-introducing a lockout was the grill's F7 regression).
- The system must **not** surface the raw provider error body, status string, or the API key to the user, because it is cryptic and may reflect the secret (SEC-16).
- The system must **not** add an `api_base` field to `ProviderUpdateRequest`, because that schema is `additionalProperties: false` and adding it is a contract change beyond scope; validation uses the **persisted** api_base.
- The system must **not** probe a completion on every PUT, because an unrelated edit (model/label) must not incur a billable upstream call.
- The system must **not** keep `fetchUpstreamModels`/`testProviderAuth` in `pkg/gateway` after migration, because leaving a second copy re-creates the divergence this feature exists to remove.
- The system must **not** treat `models[0]` as the probe model, because the catalog mixes non-chat entries (grill F3).
- The system must **not** SSRF-guard the CLI path by inventing a checker — `nil` is intentional (operator-run, no localhost-pivot threat); it must **not** silently skip the SSRF check on the **gateway** path.
- The system must **not** add embeddings/image/audio model probing, retry/backoff loops, provider health dashboards, or background re-validation — out of scope.

## Integration Boundaries

### Upstream LLM provider (`{api_base}/chat/completions`, `{api_base}/models`)
- **Data out**: `GET /models` (auth header, no body); `POST /chat/completions` (auth header, minimal body: probe model, single short user message, `max_tokens: 1`).
- **Data in**: model list JSON (catalog); completion JSON or error JSON (status + `error.{code,type,status,message}`).
- **Failure behavior**: transport error/timeout/`5xx`/`404`/pre-auth `429` → `Unreachable` (proceed). `401`/marker → `InvalidKey` (block). `402`/`429`-quota → `NoCredit`. `403` → `Restricted`. `200`+embedded-error → classified, not pass.
- **Dev approach**: `httptest.Server` returning canned status+body per the classification dataset. No live provider call in unit/integration tests. Live providers only in the manual holdout pass.

### `pkg/security.SSRFChecker` (gateway only)
- **Data out**: candidate base URL. **Data in**: nil (ok) or error (blocked).
- **Failure**: blocked URL → save/probe error before any upstream call. CLI passes `nil` → no guard (accepted).
- **Dev approach**: real `SSRFChecker` (it's pure-Go, fast); a `nil` checker exercises the CLI branch.

### `pkg/audit`
- **Data out**: `provider_key_validated` / `provider_key_validation_skipped` entries (provider id, outcome/flag, action). **Never** key or raw body.
- **Failure**: audit write failure must not fail the validation flow (best-effort, logged).
- **Dev approach**: in-memory audit sink; assert entries + assert no-secret.

### Contracts (`contracts/`)
- **Data**: new `ProviderValidation` schema; `Provider.validation` optional ref.
- **Failure**: `make verify-contracts` fails on drift → regenerate `pkg/api/generated` + `src/lib/api/generated` in the same commit.
- **Dev approach**: `scripts/gen-contracts.sh`; `pkg/api/generated/contract_test.go` proves the Go struct emits schema-valid JSON.

---

## BDD Scenarios

> Format/rules per `bdd-template.md`. Every scenario carries a `Traces to:` (User Story . Acceptance Scenario).

### Classification (the core matrix)

```gherkin
Scenario Outline: Classify a provider response into an outcome
  Given an httptest upstream for provider "<provider>"
  And the completion probe returns HTTP <status> with body <body>
  When providers.ValidateKey runs against it
  Then the outcome is "<outcome>"
  And blocks is <blocks>

  Examples:
    | provider   | status | body                                                              | outcome      | blocks |
    | openrouter | 401    | {"error":{"message":"No auth credentials found"}}                 | invalid_key  | true   |
    | openrouter | 402    | {"error":{"message":"Insufficient credits"}}                      | no_credit    | false  |
    | openrouter | 403    | {"error":{"message":"Region not supported"}}                      | restricted   | false  |
    | openai     | 401    | {"error":{"code":"invalid_api_key"}}                              | invalid_key  | true   |
    | openai     | 429    | {"error":{"type":"insufficient_quota"}}                           | no_credit    | false  |
    | openai     | 429    | {"error":{"type":"rate_limit_exceeded"}}                          | unreachable  | false  |
    | gemini     | 400    | {"error":{"status":"INVALID_ARGUMENT","message":"API key not valid"}} | invalid_key | true |
    | gemini     | 403    | {"error":{"status":"PERMISSION_DENIED"}}                          | restricted   | false  |
    | deepseek   | 401    | {"error":{"message":"Authentication Fails"}}                      | invalid_key  | true   |
    | deepseek   | 402    | {"error":{"message":"Insufficient Balance"}}                      | no_credit    | false  |
    | groq       | 401    | {"error":{"code":"invalid_api_key"}}                              | invalid_key  | true   |
    | anthropic  | 401    | {"error":{"type":"authentication_error"}}                         | invalid_key  | true   |
    | anthropic  | 400    | {"error":{"message":"credit balance is too low"}}                 | no_credit    | false  |
    | other      | 200    | {"choices":[]}                                                    | valid        | false  |
    | other      | 200    | {"error":{"message":"bad key"}}                                   | invalid_key  | true   |
    | other      | 500    | {"error":"server error"}                                          | unreachable  | false  |
    | other      | 404    | {"error":"not found"}                                             | unreachable  | false  |
    | other      | 400    | {"error":{"message":"model not found"}}                           | valid        | false  |
```
**Category**: Happy/Alt/Error mix. **Traces to**: US2.1–US2.7, US3.1–US3.3.

```gherkin
Scenario: Empty key short-circuits without a network call
  Given an api key that is empty or whitespace
  When providers.ValidateKey runs
  Then the outcome is "invalid_key"
  And no HTTP request was made to the upstream
```
**Category**: Edge Case. **Traces to**: US2.1, US3.1.

```gherkin
Scenario: Transport failure is Unreachable, not InvalidKey
  Given an upstream that closes the connection (transport error)
  When providers.ValidateKey runs
  Then the outcome is "unreachable"
  And blocks is false
```
**Category**: Error Path. **Traces to**: US2.4, US3.2.

### Messages (FR-7)

```gherkin
Scenario Outline: Each outcome maps to its plain-English message
  Given outcome "<outcome>" for provider name "OpenRouter"
  When the user-facing message is built
  Then it equals the catalog line for "<outcome>" with provider "OpenRouter"
  And it does not contain the api key
  And it does not contain the raw upstream body or an internal status code

  Examples:
    | outcome     |
    | invalid_key |
    | no_credit   |
    | unreachable |
    | restricted  |
```
**Category**: Happy Path. **Traces to**: US4.1–US4.5.

### Probe model (FR-8)

```gherkin
Scenario: pickProbeModel skips non-chat catalog entries
  Given an OpenAI catalog ["text-embedding-3-small","gpt-4o-mini","dall-e-3"]
  When pickProbeModel runs
  Then it returns "gpt-4o-mini"
```
**Category**: Alternate Path. **Traces to**: US5.1.

```gherkin
Scenario: Empty catalog falls back to a known default, never a false reject
  Given an empty catalog for OpenAI
  When pickProbeModel runs
  Then it returns a known default chat model or signals catalog-only fallback
  And ValidateKey never returns invalid_key purely because no probe model was found
```
**Category**: Edge Case. **Traces to**: US5.2.

### Flow A — Onboarding probe

```gherkin
Scenario: Onboarding probe blocks a wrong OpenRouter key (the original bug)
  Given an onboarding probe for OpenRouter whose /models is public but the key is wrong
  When HandleOnboardingProbeProvider runs
  Then the completion probe returns 401
  And the probe response has success=false with the InvalidKey message
  And validation.outcome is "invalid_key"
```
**Category**: Error Path. **Traces to**: US1.3, US3.1, US6 (parallel), US8.1.

```gherkin
Scenario: Onboarding probe proceeds with a warning on no-credit
  Given an onboarding probe whose key is valid but has no credit
  When HandleOnboardingProbeProvider runs
  Then the probe response has success=true
  And validation.outcome is "no_credit" with the NoCredit message
```
**Category**: Alternate Path. **Traces to**: US3.2, US4.2.

### Flow C — Settings Save (PUT)

```gherkin
Scenario: PUT rejects a wrong key with 422 and persists nothing
  Given a stored provider and a PUT changing api_key to a wrong key
  When the PUT handler validates
  Then the response status is 422
  And the body error is the InvalidKey message
  And the stored credential is unchanged
```
**Category**: Error Path. **Traces to**: US6.1, US8.1.

```gherkin
Scenario: PUT saves a no-credit key with 200 and a warning
  Given a PUT changing api_key to a valid-but-no-credit key
  When the PUT handler validates
  Then the response status is 200
  And the Provider body has validation.outcome "no_credit"
  And the new key is persisted
```
**Category**: Alternate Path. **Traces to**: US6.2, US8.2.

```gherkin
Scenario: PUT that changes only the model does not probe
  Given a PUT changing only "model", api_key unchanged
  When the PUT handler runs
  Then no completion probe is made
  And the response has no validation object
```
**Category**: Happy Path. **Traces to**: US6.3.

```gherkin
Scenario: PUT SSRF-checks the persisted api_base
  Given a stored provider whose persisted api_base is a loopback address
  When the PUT handler validates a changed key
  Then the SSRF checker rejects the api_base before any upstream call
  And the save fails with a non-provider error
```
**Category**: Error Path. **Traces to**: US6.4.

### Revision 1 — added scenarios (post-grill)

```gherkin
Scenario: 403 permission/region proceeds, 403 revoked-key blocks (C1)
  Given a probe that returns 403 with body "<body>"
  When ValidateKey classifies it
  Then the outcome is "<outcome>"

  Examples:
    | body                                  | outcome     |
    | {"error":{"status":"PERMISSION_DENIED"}} | restricted  |
    | {"error":{"message":"revoked api key"}}  | invalid_key |
```
**Category**: Error Path. **Traces to**: US2.1, US2.2, US3.1, US3.2 (SC-010).

```gherkin
Scenario: Gemini bad-key keys off the message, not the status (M6)
  Given a Gemini probe returning 400 with status "INVALID_ARGUMENT"
  When the body message is "model not found" (no key marker)
  Then the outcome is "valid"
  And when the body message is "API key not valid"
  Then the outcome is "invalid_key"
```
**Category**: Edge Case. **Traces to**: US2.1, US2.5.

```gherkin
Scenario: PUT re-sending the same key value re-probes; omitting the key does not (R-C)
  Given a stored provider with a persisted key
  When a PUT sends api_key equal to the existing key value
  Then exactly one completion probe fires
  And when a PUT omits api_key while changing only the model
  Then zero completion probes fire
```
**Category**: Edge Case. **Traces to**: US6.3 (SC-012).

```gherkin
Scenario: A rejected save burns the single-use re-auth token (M4)
  Given an authenticated PUT that passes the re-auth gate
  When validation returns InvalidKey and the handler responds 422
  Then the consent token is consumed
  And a retry with a corrected key but no fresh token is rejected at the re-auth gate
```
**Category**: Error Path. **Traces to**: US6.1 (R-D/M4).

```gherkin
Scenario Outline: PUT save ordering under store failures (M3)
  Given a PUT with a changed key that validates as "<outcome>"
  And the credential store is "<store>"
  When the handler runs
  Then the HTTP status is <status>
  And the key is persisted = <persisted>

  Examples:
    | outcome    | store    | status | persisted |
    | invalid_key| ok       | 422    | false     |
    | valid      | locked   | 503    | false     |
    | no_credit  | ok       | 200    | true      |
```
**Category**: Error Path. **Traces to**: US6.1, US6.2 (R-D/M3).

### Flow D — CLI onboard

```gherkin
Scenario Outline: CLI onboard validation matrix
  Given CLI onboard in "<mode>" mode with "<flag>" and a "<keykind>" key
  When the apply path runs
  Then the result is "<result>"
  And probe_made is <probe>

  Examples:
    | mode            | flag          | keykind     | result                    | probe |
    | interactive     |               | wrong       | reprompt, not persisted   | true  |
    | non-interactive |               | wrong       | exit non-zero, not persisted | true |
    | interactive     | --skip-verify | wrong       | persisted, skip audited   | false |
    | non-interactive | --skip-verify | wrong       | persisted, skip audited   | false |
    | interactive     |               | no_credit   | warn, persisted           | true  |
    | non-interactive |               | valid       | persisted, no warning     | true  |
    | non-interactive |               | unreachable | warn, persisted           | true  |
```
**Category**: Happy/Alt/Error mix. **Traces to**: US7.1–US7.4, US3.1–US3.2, US9.2.

### Flow B — Settings Test

```gherkin
Scenario: Test button reports a classified outcome
  Given a stored provider with a no-credit key
  When POST /providers/{id}/test runs
  Then the OperationResult success is true
  And it carries validation.outcome "no_credit" with the NoCredit message
```
**Category**: Alternate Path. **Traces to**: US1.3, US3.2.

### SPA (US8)

```gherkin
Scenario: configureProvider throws on 422 and shows a blocking toast
  Given the PUT endpoint returns 422 with an InvalidKey message
  When configureProvider is called
  Then it rejects (throws)
  And the caller renders a blocking error toast with the message
  And the provider form stays open
```
**Category**: Error Path. **Traces to**: US8.1.

```gherkin
Scenario Outline: Warning banner shows the right icon per outcome
  Given the PUT endpoint returns 200 with validation.outcome "<outcome>"
  When configureProvider resolves
  Then an amber warning banner is shown with icon "<icon>" and the "<outcome>" copy

  Examples:
    | outcome     | icon       |
    | no_credit   | wallet     |
    | unreachable | wifi-slash |
    | restricted  | lock       |
```
**Category**: Alternate Path. **Traces to**: US8.2, US8.3.

### Audit (US9)

```gherkin
Scenario: Warning-proceed is audited without the key
  Given a flow that proceeds on a no_credit outcome
  When the flow completes
  Then an audit entry "provider_key_validated" with outcome "no_credit" and action "proceeded" exists
  And no audit entry contains the api key or the raw upstream body
```
**Category**: Alternate Path. **Traces to**: US9.1, US9.3.

```gherkin
Scenario: --skip-verify is audited
  Given CLI onboard with --skip-verify
  When the key is persisted
  Then an audit entry "provider_key_validation_skipped" with source "cli" and flag "--skip-verify" exists
```
**Category**: Alternate Path. **Traces to**: US9.2.

---

## Test-Driven Development Plan

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestClassify_Matrix` | Unit | Classify Outline | Table of `(provider,status,body)→(outcome,blocks)` over the full matrix dataset. |
| 2 | `TestClassify_EmbeddedErrorIn200` | Unit | 200+embedded-error | `200` with `error` body classifies, never `Valid`. |
| 3 | `TestValidateKey_EmptyKeyNoNetwork` | Unit | Empty key short-circuit | Empty key → `InvalidKey`, asserts zero HTTP calls. |
| 4 | `TestValidateKey_TransportError` | Unit | Transport failure | Closed-conn upstream → `Unreachable`. |
| 5 | `TestBuildMessage_Catalog` | Unit | Messages Outline | Each `(outcome,provider)` → exact catalog line; SEC-16 negative asserts (no key, no raw body, no internal code). |
| 6 | `TestPickProbeModel_SkipsNonChat` | Unit | pickProbeModel skips | Interleaved catalog → chat model; OpenRouter → rules-table default chat slug (no account-filter — R-E). |
| 7 | `TestPickProbeModel_EmptyCatalogFallback` | Unit | Empty catalog fallback | Empty catalog → default or catalog-only; never false `InvalidKey`. |
| 8 | `TestFetchModels_BehaviourPreserved` | Unit | (US1.2) | `providers.FetchModels` returns the same list the gateway copy did (golden). |
| 9 | `TestValidateKey_SSRF_nilChecker` | Unit | (CLI nil checker) | `nil` checker → no SSRF guard, still validates. |
| 10 | `TestOnboardingProbe_BadKeyBlocks` | Integration | Onboarding probe blocks | The original-bug regression: public `/models` + wrong key → `success=false`. |
| 11 | `TestOnboardingProbe_NoCreditWarns` | Integration | Onboarding probe no-credit | Probe proceeds with `validation.outcome=no_credit`. |
| 12 | `TestPutProvider_InvalidKey422NotPersisted` | Integration | PUT rejects 422 | 422 + credential store unchanged. |
| 13 | `TestPutProvider_NoCredit200Persisted` | Integration | PUT saves 200 + warning | 200 + `validation` + key persisted. |
| 14 | `TestPutProvider_KeyUnchangedNoProbe` | Integration | PUT no-probe on unrelated edit | Spy upstream receives zero `/chat/completions`. |
| 15 | `TestPutProvider_SSRFPersistedApiBase` | Integration | PUT SSRF persisted api_base | Loopback persisted base → rejected pre-probe. |
| 16 | `TestProviderTest_ClassifiedOutcome` | Integration | Test button classified | `OperationResult` carries `validation`. |
| 17 | `TestCliOnboard_ValidationMatrix` | Integration | CLI matrix Outline | Drives interactive/non-interactive × flag × keykind. |
| 18 | `TestCliOnboard_SkipVerifyNoProbe` | Integration | --skip-verify | `--skip-verify` → zero probe + skip audited. |
| 19 | `TestAudit_WarningProceedNoSecret` | Integration | Warning-proceed audited | Audit entry exists; asserts no key/body. |
| 20 | `TestAudit_SkipVerify` | Integration | --skip-verify audited | `provider_key_validation_skipped` entry. |
| 21 | `contract_test` (Provider+validation) | Unit | (contracts) | Generated Go struct emits schema-valid JSON incl. optional `validation`. |
| 22 | `configureProvider.test.ts` | Unit (vitest) | configureProvider 422 / 200 | Throws on 422; returns `validation` on 200. |
| 23 | `ProviderWarningBanner.test.tsx` | Unit (vitest) | Banner Outline | Correct icon+copy per outcome; no banner when valid/absent. |
| 24 | `provider-validation.spec.ts` | E2E (Playwright, holdout-adjacent) | Onboarding/Settings flows | Bad key blocked at onboarding; no-credit warns at Save. (Stub upstream.) |
| 25 | `TestClassify_403_MarkerVsPermission` | Unit | C1 discrimination | A20 (`403 revoked`→invalid_key) AND A23 (`403 PERMISSION_DENIED`→restricted) under one rule (SC-010). |
| 26 | `TestClassify_Gemini400_MessageNotStatus` | Unit | M6 | A21 (`400 INVALID_ARGUMENT "model not found"`→valid) — discriminator is the message, not the status. |
| 27 | `TestClassify_429And200Defaults` | Unit | M5 / R-A step3 | A22 (`429` empty→unreachable), A24 (`200`+unrecognised-error→unreachable). |
| 28 | `TestPutProvider_SameKeyReprobes` | Integration | R-C / SC-012 | Re-sending the same key value → 1 probe; omitting `api_key` → 0 probes. |
| 29 | `TestPutProvider_StoreLockedAndReloadFail` | Integration | R-D / M3 | Validation passes but store locked → 503 persist-nothing; warn + reload-fail → 500 (key persisted). |
| 30 | `TestPutProvider_ReauthTokenBurnedOn422` | Integration | R-D / M4 | A 422 consumes the single-use re-auth token; a retry without a fresh token is rejected at the gate. |
| 31 | `TestCliOnboard_SkipVerifyAuditUnavailable` | Integration | R-F / SC-013 | `--skip-verify` completes when the log sink is unavailable; emits 1 skip line when available. |
| 32 | `configureProvider.omitsKey.test.ts` | Unit (vitest) | m4 / R-C | `configureProvider` omits `api_key` when only `model`/`models` changed (SC-005 premise). |
| 33 | `contract_test` (validation on 3 schemas) | Unit | C2 / SC-011 | `Provider`, `ProbeProviderResponse`, `OperationResult` all emit schema-valid `validation`. |

**Order rationale**: unit classifier/messages/probe-model first (1–9), then the four flow wirings as integration (10–20), then contract + SPA unit (21–23), then a thin E2E (24). Within units, the classifier is the foundation everything else asserts against.

### Test Datasets

> Format per `test-dataset-template.md`. The **classification matrix** is the primary dataset; every row traces to the *Classify* outline.

#### Dataset A — Per-provider classification (drives `TestClassify_Matrix`)

> Authoritative classifier = **R-A** (marker sets + precedence). Every row below is a conformance test of that algorithm.

| ID | Provider | HTTP Status | Body (abbrev) | Expected Outcome | Blocks | Category | Traces to |
|----|----------|-------------|---------------|------------------|--------|----------|-----------|
| A1 | openrouter | 401 | `No auth credentials` | invalid_key | true | Error | Classify / US2.1 |
| A2 | openrouter | 402 | `Insufficient credits` | no_credit | false | Error | Classify / US2.3 |
| A3 | openrouter | 403 | `Region not supported` | restricted | false | Error | Classify / US2.2 |
| A4 | openrouter | 200 | `{"choices":[]}` | valid | false | Happy | Classify / US2.5 |
| A5 | openai | 401 | `invalid_api_key` | invalid_key | true | Error | Classify / US2.1 |
| A6 | openai | 429 | `insufficient_quota` | no_credit | false | Error | Classify / US2.3 |
| A7 | openai | 429 | `rate_limit_exceeded` | unreachable | false | Error | Classify / US2.4 |
| A8 | gemini | 400 | `INVALID_ARGUMENT / API key not valid` | invalid_key | true | Error | Classify / US2.1 |
| A9 | gemini | 403 | `PERMISSION_DENIED` | restricted | false | Error | Classify / US2.2 |
| A10 | deepseek | 401 | `Authentication Fails` | invalid_key | true | Error | Classify / US2.1 |
| A11 | deepseek | 402 | `Insufficient Balance` | no_credit | false | Error | Classify / US2.3 |
| A12 | groq | 401 | `invalid_api_key` | invalid_key | true | Error | Classify / US2.1 |
| A13 | anthropic | 401 | `authentication_error` | invalid_key | true | Error | Classify / US2.1 |
| A14 | anthropic | 400 | `credit balance is too low` | no_credit | false | Error | Classify / US2.3 |
| A15 | other | 200 | `{"choices":[]}` | valid | false | Happy | Classify / US2.7 |
| A16 | other | 200 | `{"error":{"code":"invalid_api_key"}}` | invalid_key | true | Edge | Classify / US2.6 (200+marker, R-A step3) |
| A17 | other | 500 | `server error` | unreachable | false | Error | Classify / US2.4 |
| A18 | other | 404 | `not found` | unreachable | false | Edge | Classify / US2.4 |
| A19 | other | 400 | `model not found` (no marker) | valid | false | Edge | Classify / US2.5 |
| A20 | other | 403 | `revoked api key` (marker) | invalid_key | true | Edge | Classify / Edge-403-marker (R-A) |
| A21 | gemini | 400 | `INVALID_ARGUMENT` / `model not found` (no key marker) | valid | false | Edge | Classify / M6 — discriminator is the message, not the status |
| A22 | other | 429 | `` (empty/opaque body, no marker) | unreachable | false | Edge | Classify / M5 — 429 default |
| A23 | gemini | 403 | `PERMISSION_DENIED` (no credential marker) | restricted | false | Error | Classify / C1 — permission_denied does NOT block |
| A24 | other | 200 | `{"error":{"message":"overloaded"}}` (no marker, no code) | unreachable | false | Edge | Classify / R-A step3 — 200+unrecognised-error ≠ Valid |

#### Dataset B — Boundary / input edges (drives `TestValidateKey_*`)

| ID | Input | Expected | Category | Traces to |
|----|-------|----------|----------|-----------|
| B1 | key = `""` | invalid_key, 0 network calls | Boundary (empty) | Empty-key scenario |
| B2 | key = `"   "` (whitespace) | invalid_key, 0 network calls | Boundary | Empty-key scenario |
| B3 | transport reset | unreachable | Error | Transport scenario |
| B4 | timeout (ctx deadline) | unreachable | Error | Transport scenario |
| B5 | very long key (8 KB) | classified by status only (no panic) | Edge (large) | Classify |
| B6 | unicode in body message | classified by markers (no panic) | Edge (unicode) | Classify |
| B7 | empty catalog, known provider | pickProbeModel default or catalog-only | Edge (empty) | US5.2 |

#### Dataset C — Message catalog (drives `TestBuildMessage_Catalog`)

| ID | Outcome | Provider | Must contain | Must NOT contain | Traces to |
|----|---------|----------|--------------|------------------|-----------|
| C1 | invalid_key | OpenRouter | "rejected by OpenRouter" | key, raw body, `INVALID_ARGUMENT` | US4.1, US4.5 |
| C2 | no_credit | OpenAI | "no credit" | key, raw body | US4.2, US4.5 |
| C3 | unreachable | Gemini | "Couldn't reach Gemini" | key, raw body | US4.3, US4.5 |
| C4 | restricted | DeepSeek | "blocked this request" | key, raw body | US4.4, US4.5 |

#### Dataset D — CLI matrix (drives `TestCliOnboard_ValidationMatrix`) — see the CLI Scenario Outline Examples table (7 rows).

### Regression Test Requirements

This feature **modifies existing functionality** (the onboarding probe + Test button) and **deletes** two functions.

**Behaviours that MUST be preserved**:
1. Catalog fetch result for the 3 catalog-only callers — `providers.FetchModels` returns the identical list (Test #8, golden).
2. SEC-16 message hygiene — client-facing errors never embed the key or raw body (existing assertions in `rest_onboarding_test.go:1018-1026` carry over to the new message tests).
3. `200`/`401`/`400`+marker outcomes — these map the same as the old `testProviderAuth` (`200`→pass/`Valid`; `401`→reject/`InvalidKey`; `400`+marker→reject/`InvalidKey`; generic `400`→pass/`Valid`).
4. Empty-key and transport-error handling remain non-`InvalidKey`-for-transport.

**DELIBERATE behaviour changes** (NOT preserved — must be re-asserted with the new expectation, and called out in the PR):
- **`403` was `reject` (blocked onboarding) → now `Restricted` (proceeds with warning).** The old `TestTestProviderAuth` row `{"auth rejected 403", …, wantErr:true}` is **replaced** by `{403 → restricted, blocks:false}`. *This means onboarding that previously failed on a 403 now succeeds with a warning.*
- **`429`/`5xx` were silent pass (`nil`) → now `Unreachable`/`NoCredit` proceed WITH a visible warning.** Net policy (proceed) is unchanged; the new behaviour adds a surfaced warning where there was silence.

**Existing tests to migrate** (move + rewrite to the new outcome enum, do not leave duplicated in `pkg/gateway`):
- `pkg/gateway/rest_onboarding_test.go:969-1060` (`TestTestProviderAuth`, `TestTestProviderAuth_NetworkError`) → `pkg/providers/validate_test.go` as the classification matrix (Dataset A/B).
- `pkg/gateway/uat_fixes_test.go` (any `testProviderAuth`/`fetchUpstreamModels` references) → re-point to `providers.*`.
- `TestHandleOnboardingProbeProvider_PublicModelsBadKey` (the original-bug regression, `rest_onboarding_test.go:~1060+`) → keep as integration Test #10, re-pointed.

**Regression dataset**: Dataset A rows A4/A5/A8/A19 (the four preserved mappings) double as the OLD-behaviour confirmation; A3/A9 (the `403` change) are explicitly marked as changed.

---

## Functional Requirements

- **FR-001**: The system MUST expose a single `providers.ValidateKey(ctx, ValidateInput, URLChecker) ValidationResult` and `providers.FetchModels(...)` in `pkg/providers`, importable by both `pkg/gateway` and `cmd/omnipus`.
- **FR-002**: `pkg/gateway` MUST NOT retain `fetchUpstreamModels` or `testProviderAuth` after migration; all 5 call sites MUST use the `providers` functions.
- **FR-003**: `ValidateKey` MUST return one of `Valid | InvalidKey | NoCredit | Unreachable | Restricted`, determined by the **R-A algorithm** (credential-marker set + credit-marker set + fixed precedence). The marker key-off is the body **message/code**, never an ambiguous status string (e.g. `INVALID_ARGUMENT`). `permission_denied`/`unauthenticated` are NOT credential markers.
- **FR-004**: The classifier MUST cover OpenRouter, OpenAI, Anthropic-compat, Gemini-compat, Groq, DeepSeek via the shared R-A marker sets, with the same algorithm as the generic fallback for any other/custom provider (the per-provider "rules" are data — default probe slug + any provider-specific marker — not divergent code paths). It MUST reuse `error_classifier.go`'s `matchesAny`/`extractHTTPStatus`/`billingPatterns` primitives (m5) and add only the credential-marker set.
- **FR-005**: Only `InvalidKey` MUST block; `NoCredit`/`Unreachable`/`Restricted`/`Valid` MUST proceed.
- **FR-006**: A `200` with an embedded `error` body MUST be classified (never silently `Valid`); a `200`+error with no recognised marker → `Unreachable` (R-A step 3). A `429` with a credit marker MUST be `NoCredit`; any other `429` (empty body, pre-auth rate-limit — NOT detected as such) MUST default to `Unreachable`.
- **FR-007**: An empty/whitespace key MUST short-circuit to `InvalidKey` with no network call.
- **FR-008**: Every outcome MUST map to a curated, plain-English message parameterized by provider name (FR-7 catalog, verbatim). The message MUST NOT contain the key, the raw upstream body, or provider-internal codes; raw detail MAY go to the server debug log only.
- **FR-009**: `pickProbeModel(catalog, providerID)` MUST select a confirmed chat-capable model (not `models[0]`) by filtering out non-chat entries (`embed`/`whisper`/`tts`/`dall-e`/`image`/`rerank`/`moderation`) and preferring the provider's rules-table default chat slug; on empty catalog it MUST fall back to that default (never a false `InvalidKey`). **No account-filtered model API exists** (R-E) — a model the account can't access degrades to `Unreachable`/`Valid`, never `InvalidKey`.
- **FR-010**: The gateway MUST pass `a.ssrfChecker` to `ValidateKey`/`FetchModels`; the CLI MUST pass `nil` (no SSRF guard, accepted).
- **FR-011**: `PUT /providers/{id}` MUST validate **only when `req.ApiKey != nil && *req.ApiKey != ""`** (R-C — a new non-empty key in the body; no decryption, no plaintext-vs-stored compare). The handler order is **R-D**: re-auth gate → key-changed check → SSRF(persisted api_base) → `ValidateKey` → `InvalidKey`⇒**422** `{error}` persist-nothing → else `storeCredential`+reload → attach `validation` to the **200** `Provider`. `Valid` ⇒ 200 with no `validation`. A 422 burns the single-use re-auth token (R-D/M4) — the SPA MUST re-auth on retry.
- **FR-012**: `PUT` MUST SSRF-check the **persisted** `api_base`; the request body MUST NOT gain an `api_base` field (no contract change to `ProviderUpdateRequest`).
- **FR-013**: The onboarding probe (`ProbeProviderResponse`) and Settings **Test** (`OperationResult`) responses MUST carry the classified `validation` object — which requires those two schemas to gain the optional field (R-B), not just `Provider`.
- **FR-014**: CLI `omnipus onboard` MUST validate before persisting: interactive `InvalidKey` re-prompts; `--non-interactive` `InvalidKey` exits non-zero; warnings print and proceed.
- **FR-015**: CLI MUST accept `--skip-verify` which skips the probe entirely (compatible with `--non-interactive`), persists the key, and records the skip via the R-F best-effort `slog` line (never failing onboard on audit unavailability).
- **FR-016**: The SPA `configureProvider` MUST throw on 422 (blocking error) and return `validation` on 200; callers MUST render a per-type amber warning banner (NoCredit→wallet, Unreachable→wifi-slash, Restricted→lock) and no banner on valid/absent.
- **FR-017**: **Gateway** persisting flows (PUT save warning-proceed) MUST audit-log `provider_key_validated{provider, outcome, action=proceeded}` via `a.auditor` (hard requirement). **CLI** flows (`--skip-verify`, CLI warning-proceed) MUST emit a structured `slog` line (`provider_key_validation_skipped`/`provider_key_validated`, provider + outcome/flag) — best-effort, onboard MUST NOT fail on audit unavailability (R-F). Only **persisting** flows are recorded (not the informational probe/Test, O2). No entry/line MUST contain the key or raw body.
- **FR-018**: A new `ProviderValidation` contract schema (`{outcome: enum, message}`) MUST be added and referenced (optional) from **`Provider`, `ProbeProviderResponse`, AND `OperationResult`** (R-B). The 422 block reuses the existing `{error}` envelope (`jsonErr`/`ApiError.fromResponse`) — no new error schema. Generated Go/TS MUST be regenerated and `make verify-contracts` MUST pass.

## Success Criteria

- **SC-001**: 100% of the 24 Dataset-A rows + 7 Dataset-B rows pass `TestClassify_Matrix`/`TestValidateKey_*` (0 misclassifications) against the R-A algorithm (the defined oracle).
- **SC-002**: 0 occurrences of `fetchUpstreamModels` or `testProviderAuth` in `pkg/gateway` after the change (`rg` count = 0); all 5 call sites compile against `providers.*`.
- **SC-003**: A wrong OpenRouter key entered at browser onboarding is blocked at the probe step (the original bug no longer reproduces) — verified by **Test #10 alone** (httptest server: 200 public `/models` + 401 `/chat/completions`); the holdout is confidence-only (R-G).
- **SC-004**: A wrong key on `PUT /providers/{id}` returns 422 and leaves the stored credential byte-identical (Test #12).
- **SC-005**: A PUT changing only a non-key field issues **0** `/chat/completions` requests (Test #14 spy assertion).
- **SC-006**: For all 4 outcome messages, the rendered string contains 0 instances of the api key, the raw body, or an internal status code (Test #5 negative asserts).
- **SC-007**: `--skip-verify` issues 0 probes and writes exactly 1 `provider_key_validation_skipped` audit entry (Tests #18, #20).
- **SC-008**: All quality gates green: `gofmt -l` = 0, `golangci-lint` exit 0, `go test -tags goolm,stdjson` exit 0 (via CI worker), `npm run typecheck` exit 0, `npx vitest run` exit 0, `make verify-contracts` exit 0.
- **SC-009**: The `403 → Restricted/proceed` change is asserted by ≥1 test and explicitly noted in the PR description as a deliberate behaviour change.
- **SC-010**: Both `403 PERMISSION_DENIED → restricted` (A23) and `403 "revoked api key" → invalid_key` (A20) pass under the single R-A rule (proves C1 resolved — no lockout, no false-pass).
- **SC-011**: `validation` is present and schema-valid on all three response types (`Provider`, `ProbeProviderResponse`, `OperationResult`); `make verify-contracts` and the contract test pass (proves C2 resolved).
- **SC-012**: A PUT re-sending the **same** key value issues exactly 1 probe; a PUT omitting `api_key` issues 0 (proves R-C "changed" definition; complements SC-005).
- **SC-013**: `--skip-verify` onboard completes successfully even when the audit/log sink is unavailable (best-effort, R-F) and emits exactly one skip log line when it is.

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|------------------|--------------|
| FR-001 | US1 | (build/import) | #1, #8 (pkg location) |
| FR-002 | US1 | US1.1–1.3 | #8, SC-002 grep |
| FR-003 | US2 | Classify Outline | #1 |
| FR-004 | US2 | Classify Outline (per-provider + other) | #1 |
| FR-005 | US3 | Classify Outline (blocks col) | #1 |
| FR-006 | US2 | 200+embedded-error; pre-auth 429 | #2, #1 (A7,A16) |
| FR-007 | US2/US3 | Empty key short-circuit | #3 |
| FR-008 | US4 | Messages Outline | #5 |
| FR-009 | US5 | pickProbeModel skips; empty-catalog fallback | #6, #7 |
| FR-010 | US1/US6 | PUT SSRF; CLI nil checker | #9, #15 |
| FR-011 | US6 | PUT 422; PUT 200+warning; PUT no-probe | #12, #13, #14 |
| FR-012 | US6 | PUT SSRF persisted api_base | #15 |
| FR-013 | US1/US8 | Onboarding probe; Test classified | #10, #11, #16 |
| FR-014 | US7 | CLI matrix Outline | #17 |
| FR-015 | US7/US9 | --skip-verify; --skip-verify audited | #18, #20 |
| FR-016 | US8 | configureProvider throws; Banner Outline | #22, #23 |
| FR-017 | US9 | Warning-proceed audited; --skip-verify audited | #19, #20 |
| FR-018 | US1/US6 | (contracts) | #21 |

> Every FR appears above; every BDD scenario traces to ≥1 FR via its `Traces to:` line and the test column. No gaps.

---

## Ambiguity Self-Audit

| # | What's ambiguous | Likely agent assumption | Resolution |
|---|------------------|-------------------------|------------|
| 1 | The 422 error envelope shape (does the gateway have an `ErrorResponse` schema or `{error}` literal?) | Use the existing non-2xx error shape the SPA `request` helper already parses (`{error: string}` or `ErrorResponse`). | **Accepted assumption** — implementer confirms the existing gateway error helper and reuses it; no new error schema unless one is already standard. Documented in *Assumptions*. |
| 2 | Exact icon component names (Phosphor) for wallet/wifi-slash/lock | Use Phosphor `Wallet`, `WifiSlash`, `Lock`. | **Accepted** — Phosphor is the project icon set; names confirmed at implementation. |
| 3 | Whether the Settings **Test** button (200, informational) should also block-style on `InvalidKey` | Test is informational: show the InvalidKey message as an error state but it's a "test", not a save — no persistence either way. | **Accepted** — Test never persists; it just reports the outcome. |
| 4 | `pickProbeModel` per-provider default models (exact slugs) | gpt-4o-mini / gemini-2.0-flash / deepseek-chat / a Groq llama / an OpenRouter default chat slug (NOT "account-filtered" — R-E). | **Resolved (R-E)** — slugs are data in the rules table; verified live in the holdout pass; wrong-but-present slug degrades to `Unreachable`/`Valid`, never false `InvalidKey`. |
| 5 | Does `validation` belong on `Provider` or a wrapper? | Optional `validation` on `Provider` (PUT returns `Provider`); reused by probe/Test responses. | **Accepted** — D1 implies it; keeps PUT returning `Provider`. |
| 6 | Audit event naming convention | `provider_key_validated` / `provider_key_validation_skipped` (snake_case, matching existing audit verbs). | **Accepted** — align to existing `pkg/audit` naming at implementation. |

All six are low-risk and resolved as documented assumptions; none blocks implementation.

---

## Assumptions

- The 422 rejection body reuses the gateway's existing non-2xx error shape that `src/lib/api.ts request()` already throws on (no new error schema).
- `validation` is an **optional** field on the `Provider` schema; older clients ignore it (additive, non-breaking).
- The Settings **Test** endpoint remains informational (never persists) and simply reports the classified outcome.
- `pickProbeModel` default slugs live in the rules table (data), refined against live providers in the holdout pass.
- Audit naming follows existing `pkg/audit` snake_case verbs.
- CLI `nil` SSRF checker is an accepted operator-run trade-off (no localhost-pivot threat in a single-operator CLI).

## Dependencies

- `pkg/providers` already importable by `cmd/omnipus` (confirmed: `onboard.go` imports it) and `pkg/gateway`.
- `*security.SSRFChecker` satisfies `URLChecker` (`CheckURL(ctx,url)error` + `SafeClient()*http.Client`) — no change to `pkg/security`.
- Contract regen toolchain (`scripts/gen-contracts.sh`, `make verify-contracts`) for FR-018.
- CI worker (`ci-omnipus`) for the Go suite (never run the full `pkg/gateway` suite locally).

---

## Holdout Evaluation Scenarios

> **HOLDOUT — for post-implementation verification only. NOT in the TDD plan or traceability matrix. Evaluate manually / externally against LIVE providers.**

**Happy path**
- H1: With a **valid, funded** OpenRouter key, complete browser onboarding end-to-end → chat with Mia succeeds (no warning banner, no 401 at first chat).
- H2: With a valid key, edit the provider in Settings, change the label only, Save → succeeds instantly with **no** warning and **no** observable upstream completion call (check provider dashboard usage is unchanged).
- H3: `omnipus onboard --non-interactive` with a valid key in a fresh `$OMNIPUS_HOME` → exits 0, key persisted, first CLI run succeeds.

**Error path**
- H4: Enter a **deliberately wrong** OpenRouter key at onboarding → onboarding **blocks** at the provider step with the plain-English InvalidKey message; the raw provider text never appears on screen.
- H5: `omnipus onboard --non-interactive` with a wrong key (no `--skip-verify`) → **exits non-zero**, prints the InvalidKey message, persists nothing; `$OMNIPUS_HOME/credentials.json` has no provider key.

**Edge case**
- H6: With a valid key on an account with **zero balance** (or a freshly-created unfunded key), Save in Settings → **succeeds** with the amber NoCredit (wallet) banner; the key is persisted.
- H7: Disconnect the network (or point api_base at an unroutable host), run onboarding with any key → proceeds with the **Unreachable** (wifi-slash) warning, does NOT block; reconnecting and using the key works.

> A correct implementation passes H1–H7 by observable behaviour. H4/H6/H7 specifically prove the block-vs-warn discrimination that unit tests can only simulate.

---

## Out of Scope

- Retry/backoff or rate-limit handling beyond a single probe.
- Background/periodic re-validation or a provider health dashboard.
- Embeddings/image/audio model validation.
- Adding `api_base` to `ProviderUpdateRequest` (contract change).
- SSRF-guarding the CLI path.
- Multi-key (key-pool) validation per provider.
- **Account-filtered / account-available model lists** (no such provider API exists — R-E). Probe-model selection is catalog + rules-table default only.
- **Decrypt-and-compare key-change detection** — "changed" is a body-level non-empty check (R-C); the handler never decrypts the stored key to diff it.
- Validating a key *before* consuming the re-auth consent token (R-D/M4 — rejected to avoid a pre-consent upstream probe; the 422 token-burn is accepted).
