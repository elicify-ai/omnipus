# Grill Review — ADR-025 Centralized provider-key validation

- **Reviewed:** `docs/internal/architecture/ADR-025-centralized-provider-validation.md`
- **Mode:** generic-markdown (ADR red-team)
- **Date:** 2026-06-29
- **Reviewer:** grill-spec (adversarial, read-only)
- **Grounding:** claims checked against the actual codebase (`pkg/gateway/rest.go`, `pkg/gateway/rest_onboarding.go`, `pkg/providers/*`, `pkg/security/ssrf.go`, `contracts/components/schemas/*`, `src/lib/api.ts`, `cmd/omnipus/internal/onboard/onboard.go`) plus empirical provider-behaviour research (cited inline).

---

## Executive Summary

The ADR's **direction is sound and most of its structural claims verify against the code** — `pkg/providers` is genuinely CLI-importable with no `pkg/gateway`/channels/sandbox pull-in, `*security.SSRFChecker` literally already satisfies the proposed `URLChecker` interface, and the 5 call sites are exactly where the ADR says they are. **But the riskiest call (G3, the three-way status mapping) is empirically wrong in ways that will mis-bucket real keys for real menu providers**, and two of the ADR's load-bearing claims (R3 "PUT carries api_base" and NFR-4 "no contract change") are factually inaccurate against the current contract and SPA.

**Findings:** 3 CRITICAL, 5 MAJOR, 4 MINOR, 3 OBSERVATION.

**Verdict: REVISE** (no findings rise to a security-data-loss BLOCK, but the three CRITICALs must be resolved in `/plan-spec` before implementation — G3 as written will both false-accept bad keys and false-reject valid ones, which directly reintroduces the bug this ADR exists to kill).

---

## Findings Table

| ID | Severity | Lens | Section | Description | Fix / Decision |
|----|----------|------|---------|-------------|----------------|
| F1 | CRITICAL | Incorrectness | G3 / D2 / R2 | "Non-auth 4xx proves auth passed" is empirically false for the menu providers. Gemini's *primary* invalid-key error is **HTTP 400** (`API_KEY_INVALID`) — caught only by the marker list, so the marker list is load-bearing, not a heuristic backstop. Groq returns **400 `model_decommissioned` / 404 `model_not_found`** for a stale probe model → bad key mapped **Valid** (confirmed in the wild, Dify #25524). | Make the mapping **body-driven, not status-driven**: parse `error.code`/`error.type`/`status`/`reason`. Treat the marker list as authoritative for Rejected, and 400/404 *without* a positive "authenticated" signal as **Inconclusive**, not Valid. Build the per-provider matrix in plan-spec (G3 says "confirm" — it must be *designed*, the default is unsafe). |
| F2 | CRITICAL | Incorrectness | G3 / D2 | 403 ≠ Rejected for OpenAI (region block `unsupported_country_region_territory`, project-model-access) and Anthropic (`permission_error` for tier-gated model). A **valid** key from a cloud egress IP or probing a gated first-model gets **403 → wrongly Rejected → blocked**. The current code (`rest.go:1322`) already maps 403→reject; the ADR preserves it. | Distinguish 403-auth from 403-authz/region/moderation via the body (`error.type`/`code`/`metadata.reasons`). A 403 that is *not* a credential rejection must be **Inconclusive** (or Valid), never Rejected. Decide per-provider in plan-spec. |
| F3 | CRITICAL | Incorrectness / Infeasibility | R2 / D5 | "First model from the catalog" (`models[0]`, `rest_onboarding.go:473`) is unsafe. `/models` mixes non-chat models (OpenAI embeddings/audio/image; Gemini embeddings/Imagen/PaLM; Groq Whisper/TTS) in **undocumented order**; OpenRouter's plain `/models` is **not** the account-filtered list (`/models/user` is) → 404 "no endpoints matching your data policy" regardless of key. A non-chat/unavailable `models[0]` 404s irrespective of key validity → false **Valid** (or false block under a stricter mapping). | Probe with a model **confirmed chat-capable**, not `data[0]`. For OpenRouter use `/models/user`. Define the model-selection rule in plan-spec; it is currently "first element", which is wrong. |
| F4 | MAJOR | Incorrectness | NFR-4 / R3 | **R3's premise is false.** `ProviderUpdateRequest` (`contracts/components/schemas/ProviderUpdateRequest.yaml`) has only `api_key`/`model`/`models` with `additionalProperties: false` — **PUT /providers does NOT carry `api_base`**. The handler (`rest.go:4775-4954`) never reads/persists an endpoint. The `api_base` validated by `/test` comes from the *persisted* config entry (`rest.go:5003`), seeded via `OnboardingCompleteRequest.api_base`. So Save-path SSRF is against a stored value, not a fresh request value. | Reword R3: the Save path SSRF-checks the *persisted* `api_base` (still required — a malicious persisted value is possible), not a request-supplied one. **If** the spec wants PUT to accept an api_base override, that is a **contract change** and contradicts NFR-4 — decide explicitly. Note: `src/lib/api.ts:1454` already sends a stray `endpoint` field on PUT that the schema forbids and the server silently drops. |
| F5 | MAJOR | Inconsistency | NFR-4 ("no contract change") | "No contract change" is true for the *schema* but false for *behaviour*. PUT /providers currently **always** returns `gen.Provider{Status: Connected}` HTTP-200 (`rest.go:4948`). Adding strict validation means PUT can now reject. The SPA `configureProvider` (`src/lib/api.ts:1440`) types the response as `Promise<Provider>` validated by `ProviderSchema` — it has **no branch** for "saved but key rejected/inconclusive". Whether you return a non-2xx (throws in `request<Provider>`) or a `Provider` with a non-Connected status, the SPA must change. | State this as a **behavioural contract change**: define the rejection wire shape (reuse `OperationResult`? non-2xx error? a `Provider.status` value?) and the SPA handling, in plan-spec. NFR-4 should read "no *schema* change; SPA error-handling change required." |
| F6 | MAJOR | Incompleteness | G3 / R2 | **Credit-exhausted valid keys** map inconsistently across providers and the ADR addresses none: DeepSeek **402**, OpenRouter **402** (ADR's table omits 402 entirely → falls into "other 4xx → Valid"), OpenAI **429 `insufficient_quota`**, Anthropic **400 "credit balance too low"**, Gemini **429/400**. The onboarding error message even says "has credit" (`rest_onboarding.go:474`) but nothing checks it. | Decide the intended semantics: is "valid key, no credit" **Valid** (it authenticates) or a distinct surfaced state? Add 402 to the mapping explicitly. Pick one and document per-provider; "other 4xx → Valid" silently green-lights an unusable OpenRouter/DeepSeek key. |
| F7 | MAJOR | Incorrectness | G1 / FR-3 / R1 / G2 | The Q1→Q3 reconciliation (Inconclusive **blocks**) is a **behaviour regression** vs. today and is under-mitigated. Current `testProviderAuth` treats 5xx/timeout/transport as **PASS** (`rest.go:1343-1351`, returns nil → onboarding proceeds). The ADR flips these to **Inconclusive → block**. So a provider 5xx blip or corporate-proxy interference now blocks **all** onboarding/save, where today it passed. The CLI gets `--skip-verify`; **the SPA gets nothing** (G2 is "Open (recommended)"). | **G2 is a Blocker-for-the-decision, not a recommendation.** Browser onboarding and Settings-Save must have an escape hatch equivalent to `--skip-verify` ("save anyway / mark unverified"), or a flaky provider locks browser users out with no recourse. Decide G2 *in this ADR's resolution*, not deferred — it is the difference between "strict" and "unusable behind a proxy". |
| F8 | MAJOR | Incorrectness | G3 | OpenRouter (and Groq/DeepSeek, Cloudflare-fronted) can return **HTTP 200 with an error body** (`finish_reason:"error"`, documented) and **edge 429 before auth** (rate limits are account-scoped, pre-origin). A status-only mapping calls the 200-with-error **Valid** and the pre-auth 429 **Valid** even for a bad key. | The probe must (a) confirm a 200 actually contains a completion (not an embedded `error`), and (b) treat 429 as **Inconclusive** unless the provider JSON envelope confirms an auth signal. Specify in plan-spec. |
| F9 | MINOR | Inconsistency | FR-6 / D4 | FR-6 says model-refresh uses catalog-only `FetchModels`. `rest.go:5195` (refresh-models) and `rest.go:4717` (state listing) are indeed catalog-only today and don't auth-probe — so D4 is already the behaviour. Good, but the ADR frames it as new; clarify it's *preserving* current behaviour while moving the function, so a reviewer doesn't expect a behaviour change there. | Note "FetchModels migration is behaviour-preserving for the 3 catalog callers (4717/4717-listing/5195); only the 2 validate callers (onboarding 473, test 5093) gain/keep the completion probe." |
| F10 | MINOR | Insecurity | NFR-3 / Constraints | The `URLChecker` seam keeps `pkg/providers` light **only if the CLI passes nil** (verified: `pkg/security` imports `pkg/policy` via `execapproval.go`/`promptguard.go`, so constructing a real `*security.SSRFChecker` *would* drag `pkg/policy`→`pkg/audit` into the CLI). With a nil checker the CLI does **no SSRF check** on a user-supplied endpoint. For a local user-run CLI that's defensible, but the ADR should state the residual: CLI `onboard` with a custom endpoint makes an unchecked outbound call. | Document explicitly that CLI nil-checker = no SSRF guard, and that this is accepted because the CLI is locally-run by the operator. If the CLI ever accepts an endpoint from a less-trusted source, it must inject a checker. |
| F11 | MINOR | Ambiguity | FR-1 / §6 | `ValidateOptions.Model` "optional hint; else first from the fetched catalog" — combined with F3, "first from catalog" is the unsafe default baked into the type. The signature invites the bug. | Make the catalog→probe-model selection an explicit, testable function (`pickProbeModel(catalog, providerID)`), not an implicit "[0]". Reflect in the type/contract in plan-spec. |
| F12 | MINOR | Overcomplexity | D5 / Out-of-scope | The "per-provider probe strategy registry (future)" in D5 is speculative generality flagged as future work — fine to defer, but the *current* design's correctness (F1/F2) actually **needs** per-provider body interpretation now, not "future". The registry isn't over-engineering; it's the minimum for F1/F2. | Don't defer the per-provider *interpretation* (needed for correctness); only defer non-OpenAI-compat *transports* (Anthropic `/messages`, Gemini `generateContent`). Separate "interpretation matrix" (now) from "native transports" (later). |
| F13 | OBSERVATION | — | §5 Option B | Option B (`pkg/providervalidation`) is correctly rejected, but the stated reason ("duplicates provider knowledge") is the right call — Option A reuses `GetDefaultAPIBase`/`IsPassthroughProvider` already in `pkg/providers`. No change needed; noting the analysis holds. | — |
| F14 | OBSERVATION | — | NFR-5 | "One /models GET + one 1-token completion" cost claim is fine, but note the completion is billable on metered providers (a real token charge per validate, ×4 flows + every Settings-Save). Negligible per-call, but a user repeatedly editing settings pays per save. | Consider debouncing validate-on-save or only validating when the key field actually changed. |
| F15 | OBSERVATION | — | D3 / CLI | `--non-interactive` exits non-zero on Inconclusive; ensure `--skip-verify` is honoured in `--non-interactive` too (CI air-gapped path). The ADR implies it but doesn't state the combination. | Confirm `--skip-verify --non-interactive` is the supported CI path in plan-spec. |

---

## Structural Integrity (generic-markdown narrative)

- **Scope clarity:** Strong. Four flows enumerated with a verified status table (§1). Out-of-scope explicit (§8).
- **Actors:** Covered (operator, maintainers, security). Missing: the **SPA as a consumer** of the new PUT-rejection behaviour (F5) — an unlisted actor that must change.
- **Success criteria:** FR-1..FR-6 are mostly testable, but FR-3's "Valid" hinges on G3, which is not yet a decision (F1).
- **Failure modes:** Partially addressed (R1-R4). The *behaviour change* for 5xx/transport (pass→block) is under-stated (F7); credit-exhaustion failure mode absent (F6).
- **Implementation detail:** Sufficient for the *refactor* (move 2 funcs, migrate 5 callers — all verified). Insufficient for the *mapping* (F1-F3, F6, F8) — the hardest part is the least specified.
- **Assumptions:** "Non-auth 4xx == auth passed" (F1) and "PUT carries api_base" (F4) are stated as fact but are false. "No contract change" (F5) is half-true.
- **Constraints:** Pure-Go/single-binary/tags verified. Import-cycle claim verified (no `providers↔security` cycle; `URLChecker` seam real).

**Verified-correct ADR claims** (grounding held): CLI imports `pkg/providers` not `pkg/gateway` (`onboard.go:50`); `pkg/providers` has no gateway/channels/sandbox/security import; `*security.SSRFChecker` satisfies `CheckURL(ctx,url)error`+`SafeClient()*http.Client` exactly (`ssrf.go:245,326`); 5 call sites at the cited lines; CLI does zero validation today; PUT does zero validation today; `fetchUpstreamModels`/`testProviderAuth`/`authRejectionMarkers` are gateway-local.

---

## STRIDE Summary

| Component | Threat | Status in ADR |
|-----------|--------|---------------|
| Validator outbound (onboarding, caller-supplied endpoint) | **Info Disclosure / SSRF** — POST to metadata/loopback | Handled: SSRF pre-check + safe client (verified `rest_onboarding.go:446`, `rest.go:5072`). |
| Validator outbound (Save path) | SSRF via persisted `api_base` | Mislabelled (F4): it's the *persisted* value, still needs the check; ADR's "request carries api_base" is wrong. |
| CLI validator (nil checker) | SSRF unchecked on user endpoint | Accepted but **undocumented** (F10). |
| Error surfacing | Info Disclosure — key/raw body in client message | Already handled (SEC-16, fixed message, `rest.go:1327`); ADR preserves it. Verify the moved code keeps the fixed-message + server-only-debug split. |
| Validate-on-save | DoS / cost — per-save billable completion | Unaddressed (F14). |
| Rejection on Save | Repudiation/UX — no audit of "saved unverified" override | If G2 override added, log it (audit who bypassed). Unaddressed. |

---

## Test Coverage Assessment

- **Existing coverage** (`rest_onboarding_test.go:969-1060`) is a clean status-matrix table test for `testProviderAuth` — **this is the test the migration must carry over and extend**, not rewrite.
- **Critical gap:** the existing test uses a single `httptest` server returning one status — it does **not** test the body-driven discrimination per provider (F1/F2) beyond the Gemini marker. The plan-spec TDD must add a **per-provider body matrix** (Gemini 400-API_KEY_INVALID, Groq 400-model_decommissioned, OpenAI 403-region, Anthropic 403-permission, OpenRouter 200-with-error-body, 402 credit, pre-auth 429).
- **Missing:** Save-path rejection test (F5), Inconclusive-blocks-and-escape-hatch test (F7), CLI `--skip-verify`/`--non-interactive` matrix (F15).
- **Regression:** the 5xx/transport pass→block flip (F7) needs an explicit test asserting the *new* blocking behaviour, plus a test that the escape hatch unblocks.

---

## Unasked Questions

1. **What is the actual per-provider status/body → outcome matrix?** G3 says "confirm in plan-spec"; the empirical evidence (F1-F3, F6, F8) shows the default mapping is wrong. This is *the* deliverable, not a confirmation step.
2. **What wire shape does a rejected PUT return,** and what does the SPA render? (F5) — undecided, blocks SPA work.
3. **Is G2 (browser/Save escape hatch) in or out?** (F7) — "recommended" is not a decision; strictness without it locks out proxy/outage users.
4. **Does "Valid" include "valid key, no credit"?** (F6) — affects whether a broke OpenRouter/DeepSeek key onboards.
5. **Which model does the probe use** if not `models[0]`? (F3/F11)
6. **Does adding validation to Settings-Save change the meaning of an existing saved-but-now-invalid key** when a user re-saves unrelated fields (e.g. just editing the model list)? Re-validation on every PUT could block edits to a provider whose key later expired — is that intended?

---

## Verdict

**REVISE**

Review written to: `docs/internal/architecture/ADR-025-centralized-provider-validation-review.md`

The centralization refactor (Option A, `pkg/providers` home, `URLChecker` seam, 5-caller migration) is well-grounded and verified — implement that part with confidence. The blockers are all in the *decision content*, not the structure: G3's status mapping (F1-F3, F6, F8) must be redesigned body-driven and per-provider before any code, the PUT behavioural-contract change must be specified for the SPA (F4/F5), and G2 must be *decided* not deferred (F7).

Address the findings above, then re-run:
  `/grill-spec docs/internal/architecture/ADR-025-centralized-provider-validation.md`
(or take the resolved G2/G3 decisions straight into `/plan-spec`).
