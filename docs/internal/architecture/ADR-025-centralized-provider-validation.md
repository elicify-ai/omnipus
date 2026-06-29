# ADR-025 — Centralized provider-key validation (one validator, all flows)

- **Status:** Proposed
- **Date:** 2026-06-29
- **Deciders:** operator, Albert (architect)
- **Evidence level (highest used):** 1 (user-provided decisions) grounded against 2/3 (codebase facts)

> Ratifying ADR. The direction and the strictness model were decided with the operator
> in session on 2026-06-29; this records the design, grounds it in code, and assigns
> per-decision confidence. Hands to `/grill-spec` then `/plan-spec`.

> **Revision 2 (2026-06-29, post-grill — `ADR-025-…-review.md`; operator-decided).** The
> grill (REVISE; 3 CRITICAL) showed the status-only mapping is wrong and that the earlier
> strict "Inconclusive blocks" was a regression. Final design:
>
> **Outcomes are a CLASSIFIED type, body-driven and per-provider** (not status-only):
> `Valid` · `InvalidKey` · `NoCredit` · `Unreachable` · `Restricted`. The classifier
> inspects the response body (`error.code/type/status/message`), not just the HTTP status —
> e.g. a Gemini bad key is HTTP **400 `API_KEY_INVALID`**; a **403 region/permission** block
> is NOT a bad key (`Restricted`); OpenRouter/DeepSeek **402** and OpenAI **429
> `insufficient_quota`** are `NoCredit`; a **200 with an embedded `error`** body or a
> pre-auth edge **429** is not a clean pass. The per-provider classification matrix is the
> core `/plan-spec` deliverable (supersedes the old "non-auth 4xx → Valid" rule — fixes
> F1/F2/F3/F6/F8).
>
> **Policy (supersedes FR-3 / D3):** ONLY `InvalidKey` **blocks** (a confirmed-wrong key).
> `NoCredit`, `Unreachable`, and `Restricted` **proceed** with a clear, type-specific
> warning — so a provider outage, a corporate proxy, an unfunded account, or a region block
> never locks a user out. This resolves the F7/G2 browser-lockout without a separate "save
> anyway" escape; CLI `--skip-verify` remains (to skip the check entirely, e.g. air-gapped).
>
> **User-facing messages (NEW — FR-7):** every outcome maps to a hand-crafted, plain-English
> message parameterized by provider name — **never** the raw provider error body. The raw
> upstream detail stays in a server-side debug log only (SEC-16). Catalog:
> - `InvalidKey` (blocks): "The API key was rejected by {Provider}. Check you copied the whole key and that it's still active in your {Provider} account."
> - `NoCredit` (proceeds): "Your {Provider} key works, but the account has no credit. Add funds in your {Provider} dashboard to use it."
> - `Unreachable` (proceeds): "Couldn't reach {Provider} to check the key — check your internet connection. Continuing for now; the key will be used as entered."
> - `Restricted` (proceeds): "Your {Provider} key works, but {Provider} blocked this request (it may be restricted in your region, or the selected model isn't available to your account)."
>
> **Probe model (NEW — FR-8, fixes F3):** probe with a model confirmed **chat-capable**, not
> `models[0]` (the catalog mixes embedding/audio/image models in undocumented order); for
> OpenRouter use the account-filtered list. `pickProbeModel(catalog, providerID)` is an
> explicit, tested function.
>
> **Factual corrections from the grill:**
> - **R3 (F4):** `ProviderUpdateRequest` has NO `api_base` field — the Save path SSRF-checks
>   the *persisted* `api_base`, not a request value. (A PUT api_base override would be a
>   contract change — out of scope.)
> - **NFR-4 (F5):** no *schema* change, but a **behavioural contract change** — PUT
>   `/providers/{id}` can now return a classified rejection where it always succeeded, so the
>   SPA's `configureProvider` MUST gain a rejection/warning branch that renders the FR-7
>   message. Define the rejection wire shape in `/plan-spec`.
>
> **Notes:** `FetchModels` migration is behaviour-preserving for the 3 catalog callers; only
> the 2 validate callers do the completion (F9). CLI nil-checker = no SSRF guard on a
> CLI-supplied endpoint — accepted because the CLI is operator-run (F10). Validate-on-save
> SHOULD run only when the key field actually changed, to avoid a billable completion on
> every unrelated edit (F14).

---

## 1. Problem Understanding

A user onboarded with an invalid OpenRouter key, completed onboarding, and only hit a
cryptic `401 "User not found"` on the first chat. Root cause `[FACT]`: validation
"verified" the key by listing the provider's `/models` endpoint, which is **public/
keyless on OpenRouter** (and other passthrough providers), so a bad key passed. A
prior fix added a real authenticated `/chat/completions` probe — but the validation
logic is now **scattered and inconsistent**, and two of the four key-accepting flows
don't validate at all.

**Business objective.** One canonical provider-key validator, used by **every** flow
that accepts a provider key, with a single, consistent strictness policy — and close
the validation gap in CLI onboard and Settings-save.

**Stakeholders.** Operator/self-host users (first-run friction), maintainers (one code
path to reason about), security (the probe makes unauthenticated outbound calls).

**The four key-accepting flows** `[FACT]`:
| Flow | Endpoint | Validates today | Enforced |
|---|---|---|---|
| Browser onboarding | `POST /onboarding/probe-provider` | yes (real auth) | ✅ SPA blocks Complete |
| Settings → Test | `POST /providers/{id}/test` | yes (real auth) | manual (on click) |
| Settings → Save | `PUT /providers/{id}` | **no** | — |
| CLI `omnipus onboard` | (none) | **no** | — |

**The redundancy** `[FACT]`: `fetchUpstreamModels` (`pkg/gateway/rest.go:1157`; 3
callers) + `testProviderAuth` (`rest.go:1281`; 2 callers) live in `pkg/gateway`,
duplicating endpoint-resolution, SSRF, model-fetch, and the 401/403 + 400-marker
discrimination. The CLI has none.

## 2. Extracted Requirements

### Functional
- FR-1: A single validator MUST exist that, given (provider, key, optional endpoint,
  optional model), returns the model catalog and a **three-way outcome**:
  **Valid** / **Rejected** / **Inconclusive**. `[FACT: operator decision]`
- FR-2: All four flows MUST use it. The duplicated `fetchUpstreamModels` /
  `testProviderAuth` logic in `pkg/gateway` MUST be removed (callers migrated). `[FACT]`
- FR-3: **Strictness policy (uniform):** a flow proceeds ONLY on **Valid**. A
  **Rejected** key blocks with a key-rejection message; an **Inconclusive** result
  blocks with a "could not reach/verify the provider — try again" message. `[FACT: Q3
  block-until-reachable, which supersedes Q1's "inconclusive saves" option text]`
- FR-4: **Settings Save (PUT)** MUST validate and block on Rejected/Inconclusive (new).
  `[FACT: Q1 block-the-save]`
- FR-5: **CLI `omnipus onboard`** MUST validate (new): interactive → re-prompt for the
  key on Rejected, block on Inconclusive; `--non-interactive` → exit non-zero with a
  clear message; a **`--skip-verify`** flag bypasses validation entirely (air-gapped/
  CI/offline). `[FACT: Q2]`
- FR-6: Model-*refresh* of an already-configured provider MUST use catalog-only fetch
  (no completion call burned per refresh); only the explicit validate flows do the auth
  completion. `[FACT: implementation decision]`

### Non-Functional
- NFR-1 (Drift-resistance): one validator → no divergence across flows. `[FACT]`
- NFR-2 (Footprint): `pkg/providers` (the validator's home) MUST stay importable by the
  lightweight CLI `onboard` without pulling in `pkg/gateway`. `[FACT]`
- NFR-3 (Security/SSRF): the validator's outbound calls MUST be SSRF-guarded on the
  paths where the endpoint is caller-supplied. `[FACT: SEC-24]`
- NFR-4 (No contract change): reuse existing endpoints; no `contracts/` change. `[FACT]`
- NFR-5 (Cost): a validate = one `/models` GET + one 1-token completion; negligible.

### Constraints
- Pure-Go single binary; build tags `goolm,stdjson`. `[FACT]`
- `pkg/providers` ⇄ `pkg/security` are independent (no import cycle) — SSRF injected via
  a small interface to keep `pkg/providers` dependency-light. `[FACT]`

## 3. Gaps and Ambiguities

| # | Item | Status | Resolution |
|---|---|---|---|
| G1 | Q1 ("inconclusive saves") vs Q3 ("block until reachable") conflict | **Resolved** | Q3 governs — Inconclusive blocks uniformly (FR-3). |
| G2 | Settings-Save offline escape hatch | **Open (recommended)** | Strict block can lock out a user editing while a provider has a transient outage. Recommend a SPA "save anyway / mark unverified" override (mirrors CLI `--skip-verify`). Decide in `/plan-spec`. |
| G3 | Three-way mapping of edge statuses (404/400-generic/429) | **Resolved (proposed)** | A non-auth 4xx (404 model-not-found, 400 bad-model, 429) proves the provider *authenticated* the request → **Valid**. Only 5xx/timeout/transport/`/models`-fetch-failure → **Inconclusive**. 401/403/400-credential-marker → **Rejected**. (Confirm in plan-spec.) |
| G4 | Non-OpenAI-compat providers (Anthropic `/messages`, Gemini `generateContent`) | **Accepted limitation** | The probe is OpenAI-compat `/chat/completions` + a 400-credential-marker heuristic. Covers the menu (Anthropic compat works; Gemini 400-marker). Native per-provider probes are out of scope. |

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Single source of truth (no redundancy) | 30% | The whole point — one validator, all flows. |
| Correctness across providers | 25% | Catch bad keys incl. public-/models providers; don't false-block. |
| CLI footprint (no gateway pull-in) | 20% | The validator must be CLI-callable. |
| Security (SSRF) | 15% | Unauthenticated outbound on the onboarding path. |
| Offline/air-gapped support | 10% | Strict policy must have an escape hatch. |

## 5. Option Analysis

The genuine architectural fork is **where the validator lives** (which dictates whether
the CLI can share it).

### Option A — Validator in `pkg/providers` (chosen)
| Dimension | Assessment |
|---|---|
| Strengths | `pkg/providers` already owns provider knowledge (`GetDefaultAPIBase`, `IsPassthroughProvider`) and is **already imported by CLI `onboard`** with no `pkg/gateway` pull-in. SSRF injected via a small `URLChecker` interface → no cycle, no heavy dep. One home for `ValidateKey` + `FetchModels`. |
| Weaknesses | `pkg/providers` gains an HTTP-probe concern (it already does HTTP for chat). Minor. |
| Risks | Interface seam must be kept clean so `pkg/providers` doesn't grow a `pkg/security` dep. |
| Complexity | Medium (move 2 funcs + the marker logic; migrate 5 callers). |

### Option B — New dedicated package `pkg/providervalidation`
| Dimension | Assessment |
|---|---|
| Strengths | Crisp single-responsibility package; importable by both gateway and CLI. |
| Weaknesses | Duplicates provider knowledge already in `pkg/providers` (or must import it anyway) → another package for no real isolation gain. |
| Risks | Two provider-adjacent packages to keep coherent. |
| Complexity | Medium-high (more wiring, marginal benefit over A). |

### Option C — Keep it in `pkg/gateway`, expose for CLI
| Dimension | Assessment |
|---|---|
| Strengths | No move. |
| Weaknesses | **Fatal:** the CLI `onboard` cannot import `pkg/gateway` (pulls channels/sandbox/the whole engine into the bootstrap binary path) — violates NFR-2. The redundancy and CLI gap remain. |
| Risks | — |

## 6. Recommended Architecture

Adopt **Option A**. New `pkg/providers/validate.go`:

```go
// URLChecker is the SSRF guard. The gateway passes its *security.SSRFChecker;
// the CLI passes nil (local, user-run — lower SSRF exposure).
type URLChecker interface {
    CheckURL(ctx context.Context, rawURL string) error
    SafeClient() *http.Client
}

type Outcome int
const ( Valid Outcome = iota; Rejected; Inconclusive )

type ValidateOptions struct {
    ProviderID string
    APIKey     string
    Endpoint   string      // optional override; else GetDefaultAPIBase(ProviderID)
    Model      string      // optional hint; else first from the fetched catalog
    Checker    URLChecker  // optional SSRF guard
}
type ValidateResult struct {
    Outcome Outcome
    Models  []string  // catalog (for the UI), when reachable
    Detail  string    // server-side detail (never the key); a fixed client message is derived
}

// ValidateKey resolves+SSRF-checks the endpoint, fetches the catalog, and exercises a
// real authenticated /chat/completions. Three-way result per FR-3/G3.
func ValidateKey(ctx context.Context, o ValidateOptions) (ValidateResult, error)

// FetchModels is the catalog-only fetch (moved from gateway.fetchUpstreamModels),
// used by the model-refresh paths (FR-6).
func FetchModels(ctx context.Context, baseURL, apiKey string, c URLChecker) ([]string, error)
```

`testProviderAuth` + the credential-marker list move into `pkg/providers` (unexported,
behind `ValidateKey`). The 5 gateway callers migrate; the gateway copies are deleted.

### D1 — Validator in `pkg/providers`; SSRF via `URLChecker` interface
```
CONFIDENCE: High
  Basis    : CLI already imports pkg/providers and not pkg/gateway; no provider/security cycle (verified).
  Evidence : onboard.go imports; empty grep for providers↔security; fetchUpstreamModels/testProviderAuth call sites.
  Missing  : nothing material.
  Would improve: n/a.
```

### D2 — Three-way outcome (Valid/Rejected/Inconclusive) + the status mapping (G3)
```
CONFIDENCE: Medium-High
  Basis    : The strict policy needs to distinguish "reachable+bad" from "unreachable"; the existing binary testProviderAuth can't.
  Evidence : 401/403/400-marker already implemented; provider auth-before-routing behavior.
  Missing  : empirical confirmation that 404/400-generic/429 universally imply auth-passed for the menu providers — verify in plan-spec with a per-provider check.
  Would improve: a small live matrix test per menu provider.
```

### D3 — Uniform strict policy: proceed only on Valid; block on Rejected + Inconclusive
Applied per flow: browser onboarding (already blocks; now via the shared validator),
Settings Test (reports outcome), Settings Save (NEW block), CLI onboard (NEW;
interactive re-prompt on Rejected, block on Inconclusive, `--non-interactive` exits
non-zero, `--skip-verify` bypasses).
```
CONFIDENCE: High (policy is operator-decided)
  Basis    : Operator chose block-the-save (Q1) + block-until-reachable (Q3) + reject+escape-hatch (Q2).
  Evidence : the three answers this session.
  Missing  : the Settings-save escape hatch (G2) — recommended, not yet decided.
  Would improve: confirm G2 in plan-spec.
```

### D4 — Model-refresh uses `FetchModels` (catalog-only); only validate flows do the completion
```
CONFIDENCE: High
  Basis    : refreshing a configured provider's catalog shouldn't burn a completion each time.
  Evidence : rest.go:4717/5195 are refresh paths, not key-entry paths.
  Missing  : nothing material.
```

### D5 — OpenAI-compat completion probe + 400-credential-marker heuristic (accepted limitation)
```
CONFIDENCE: Medium
  Basis    : covers the menu providers; native per-provider probes are disproportionate now.
  Evidence : Anthropic OpenAI-compat works; Gemini returns 400 INVALID_ARGUMENT (marker-caught).
  Missing  : robustness for arbitrary non-compat providers a user adds via "Other".
  Would improve: a per-provider probe strategy registry (future).
```

## 7. Risks and Caveats

- **R1 — Strict block breaks offline/transient-outage onboarding.** Operator chose it
  deliberately. Mitigated for CLI by `--skip-verify`; **recommend an equivalent SPA
  "save anyway / mark unverified" override (G2)** so a browser user isn't locked out
  during a provider blip. One-way-door-ish UX; settle in plan-spec.
- **R2 — Heuristic false-negatives (G3/D5).** A provider that returns 200 on `/models`
  with a bad key AND doesn't 401 on a bad completion would slip through; and a provider
  that 401s a *valid* key on a weird first model would false-block. Low for the menu;
  the three-way mapping + first-model-from-catalog reduce it.
- **R3 — SSRF on the new Save path.** `PUT /providers` can carry an `api_base`; it MUST
  be SSRF-checked through the same `URLChecker` (the validator enforces it).
- **R4 — Refactor blast radius.** 5 gateway call sites + tests migrate; a missed caller
  or a behavior change in `FetchModels` could regress model-listing. Covered by the
  existing gateway tests + the migration being mechanical.

## 8. Confidence Assessment

| Decision | Confidence |
|---|---|
| D1 — pkg/providers home + URLChecker interface | High |
| D2 — three-way outcome + status mapping | Medium-High |
| D3 — uniform strict policy across flows | High (operator-decided) |
| D4 — refresh = catalog-only | High |
| D5 — OpenAI-compat probe + marker heuristic | Medium |

Overall: **High** direction. The soft spots are G3 (status mapping — confirm empirically)
and G2 (Settings-save escape hatch — recommended, undecided).

### Out of scope (explicit)
- Native per-provider probes (Anthropic `/messages`, Gemini `generateContent`) — OpenAI-compat covers the menu.
- Re-validating already-saved keys on a schedule / health dashboard.
- Provider model-catalog caching changes.

## 9. Validation / Next Steps
- **Red-team:** `/grill-spec docs/internal/architecture/ADR-025-centralized-provider-validation.md` — focus G3 (status mapping correctness), R2 (false-neg/false-block), and G2.
- **Spec it:** `/plan-spec docs/internal/architecture/ADR-025-centralized-provider-validation.md` — resolve G2 (Settings-save override) + G3 (per-provider status matrix), then BDD/TDD for the validator + the 4 flow wirings + the CLI `--skip-verify`.
- Then `/taskify` → wave implementation (one validator module → migrate callers → CLI onboard → tests) → `/grill-code`.
