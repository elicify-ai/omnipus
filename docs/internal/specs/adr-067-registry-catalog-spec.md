# Spec — ADR-067: registry-fed catalog and provider identity (D1 · D11 · D12)

- **Source ADR:** `docs/internal/architecture/ADR-067-registry-fed-catalog-and-provider-identity.md` (Proposed 2026-08-22; §8a pass-2 resolutions MAJ-005/006/010/014 folded in). Companion review: `docs/internal/architecture/ADR-066-context-budget-and-tool-result-routing-review-pass2.md` (MAJ-004/005/006/010/014/015, MIN-003/004, open questions 7–8).
- **Status:** Draft (plan-spec) · Phase 1 gate: the ADR treated as the confirmed brief · Phase 5.5 gate PASSED (§9) · **Grilled 2026-08-22 (`adr-067-registry-catalog-spec-review.md`, verdict BLOCK) — all findings resolved in §1.1 (BINDING); cross-spec seams (`cross-spec-review-adr-066-067-068.md`) resolved in §1.2 (BINDING); ADR-067 amended in its §8b.** `[A-n]` labels in the body now point at the *resolved* decision in §9, not an open assumption.
- **Branch:** `feat/context-budget-and-tool-result-routing` (worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-context-budget`). Facts read @ this tree on 2026-08-22.
- **Tech:** Go (`pkg/providers`, `pkg/gateway`, `pkg/agent`, `pkg/media`) · React 19 + Vite (SPA, consumer only) · contract-first (`contracts/*` → `pkg/api/generated`, `src/lib/api/generated`) · Go tests run with `-tags goolm,stdjson`; never the full gateway suite locally.
- **Scope:** ADR-067 only — D1 (one catalog, schema 2.0.0, assembled daily elsewhere, checksum-only, committed embedded snapshot, retargeted puller at 24 h + startup, last-known-good on registry disagreement), D11 (models.dev provider ids, protocol as a field, protocol dispatch, exact `(provider, model)` lookup, `resolveStrippedPrefix` removed, unknown provider degrades per provider/agent, `capabilities` folded into `catalog`), D12 (every registry provider selectable, Popular pinned, cloud-IAM visible-disabled, custom endpoint, selector reads the catalog; live `/models` only for entitlement / local endpoints / probe).
- **Out of scope (referenced, not specified):** picker/selector UI behaviour, shared provider picker, provider deletion, default-model card, subscription sign-in, `antigravity` deletion, `codex-cli`/`openai-chatgpt` split → **ADR-068**. Window resolution ladder, floor, learned limits, Settings window display → **ADR-066 D2–D3/D8/D9** (this catalog is one rung there). The assembly repository's own code → separate codebase; only its **contract** is specified here (§5 Integration Boundaries, US-2).
- **Greenfield rule (ADR header, binding):** no backward compatibility, migration, aliasing, grace periods, retired-name lists, or boot notices about removed things. The embedded-snapshot fallback is design, not compatibility.

---

## 1. Overview

Omnipus currently learns what a model can do from two hand-typed files: `pkg/providers/capabilities/data/providers_capabilities_seed.json` (78 models, modalities + resize limits, **no context window**) and `pkg/providers/catalog/catalog.go::Entries` (23 picker rows typed in Go, emitted to JSON + a TypeScript constant). Both are stale in ways ADR-067 §2 demonstrates, and neither knows a context window, which is how ADR-066's incident agent ran at one eighth of its real capacity.

This spec replaces both with **one document** — providers with nested models, `schema_version` `2.0.0` — produced daily by a separate open-source assembly repository from models.dev + LiteLLM + a reviewed `overrides/` directory + a per-provider resize table, and published as a single GitHub Release asset `providers_catalog.json` with a `.sha256` sidecar (no signature, by decision). Omnipus ships a **committed** snapshot of that file (refreshed by a scheduled PR, never fetched at build), pulls a fresh copy in the background at startup and every 24 h through the existing checksum-verifying `GHReleasePuller`, and serves the in-memory document to every consumer: the media pipeline (modalities, resize), the agent loop (context window and output limit, as ADR-066 D2's catalog rung), the validation probe (probe model), and the SPA (a new read-only `GET` endpoint). Provider identity becomes the registry's (`zai`, `moonshotai-cn`, `alibaba-coding-plan`, …); wire protocol becomes a field; the ~40-case factory switch becomes ~5 protocol cases fed by the table; `resolveStrippedPrefix` is deleted so `(provider, model)` lookup is exact. An unknown provider id never aborts boot — it degrades the provider row and the agents bound to it.

**What the operator observes when this is done:** the model picker lists every catalog provider and model with limits and modalities attached, offline, with no live call; a checksum-mismatched or wrong-schema feed is rejected with one WARN and the snapshot keeps serving; a config naming `z-ai` fails as an unknown provider with no rename and no hint; `(openrouter, z-ai/glm-5.2)` resolves to 1,048,576 and `(zai, glm-5.2)` to 1,000,000 while `(openrouter, glm-5.2)` is a miss.

---

## 1.1 Amendment A1 — post spec-grill (BINDING; overrides any conflict below)

Each finding was verified against the code before disposition. Coordinator decisions 2026-08-22; ADR-067 §8b carries the ADR-side amendments.

| ID | Verified? | Disposition (applied where) |
|---|---|---|
| F-01 | Yes — `pkg/providers/capabilities/version.go::parseSemverPrefix` parses numerically only with a leading `v` | **Apply.** `version` is `vYYYY.M.D[.N]`; FR-002 rejects a `version` without the `v` prefix; T7 gains the rows `v2026.8.9 < v2026.8.10`, `v2026.9.30 < v2026.10.1`, `v2026.12.31 < v2027.1.1`, `v2026.8.22 < v2026.8.22.1` (DS-7). |
| F-02 | Yes — `puller.go::verify` returns nil on a 404 sidecar; `TestGHReleasePuller_Pull_NoSidecar` asserts success | **Apply (behaviour change).** FR-032: no sidecar → reject, `reason=checksum`; release path reads the sidecar from the release's asset list; `Pull_NoSidecar` rewritten to assert rejection and removed from the "unchanged" list (§7.2). |
| F-03 | Yes — no URL validation on load anywhere; `api` is new | **Apply.** FR-033: every non-empty `api`/`protocols[].api` must be absolute `https`, no userinfo/query/fragment, host not loopback/link-local/RFC1918/ULA/metadata — except rows with protocol `ollama`, or id `vllm`/`lmstudio`, or custom rows, which may use `http` and local hosts. Violation rejects the document (snapshot serves). The LLM transport built from a validated catalog URL uses today's HTTP/Claude providers unchanged (honouring `ModelConfig.Proxy`); custom rows are the operator's own choice and are not URL-validated beyond parseability. DS-1 rows 18–22. |
| F-04 | Yes (assembly-side; the example itself was a rounding case) | **Apply.** Numeric fields within 5 % or 4,096 tokens (whichever larger) are not disputes: publish the lower, record both; larger deltas open an issue and publish last-known-good; a closed issue writes the adjudicated value into `overrides/`, which wins thereafter. Override-sourced values never raise an issue. US-2.AC2 rewritten. | **Amendment 2026-08-23 (B2 first run):** the job opens **one issue per run** listing every dispute (label `registry-dispute`), not one issue per disagreement — the first real run produced 194 disputes between models.dev and LiteLLM (63 context_window, 98 max_output_tokens, 9 tool_call, 30 modality), and 194 issues would be noise, not signal. Last-known-good publication per dispute is unchanged.
| F-05 | Yes — `puller.go::fetchReleaseAsset` and `fetchRaw` cap at `2<<20` | **Apply.** Named constant `maxCatalogAssetBytes = 16 << 20`; read limit is cap + 1 byte so the boundary distinguishes `too_large` from `checksum`; the 4 MB Releases-API response cap is kept and named `maxReleaseAPIBytes`. T12 asserts a truncated body yields `too_large`, never `checksum`. |
| F-06 | Yes — `factory_provider.go::ExtractProtocol` splits `Model` on `/` and defaults to `openai` | **Apply.** FR-034: the catalog key is `(ModelConfig.Provider, ModelConfig.Model)` exactly; `Model` is a bare catalog model id (a `/` inside it is data, e.g. `z-ai/glm-5.2` under `openrouter`); `ExtractProtocol` is deleted; `pkg/agent/model_resolution.go` and `pkg/voice/transcriber.go` (its callers) are listed as modified. DS-2 rows 9–10. |
| F-07 / F-08 / F-09 | Yes — no `case "custom"` in `factory_provider.go`; `custom` appears only in a `rest.go` comment | **Apply.** FR-035: `custom` is a factory **case**, not a catalog row (removed from US-2.AC4 / T18 / FR-026). A provider config whose id is not in the catalog is accepted iff it has `api_base` **and** `protocol ∈ {openai-compatible, anthropic}`; it is then a custom row keyed by that id, so several custom endpoints coexist. Unknown id without both → `ErrUnknownProvider`. `PUT /providers/{id}`, the onboarding probe and CLI onboard gate identically. DS-3 rows 13–15. |
| F-10 | Yes — ADR §3.2 item 4 said the opposite | **ADR amended** (§3.2 item 4): `gen/main.go` and `providerCatalog.ts` deleted; SPA reads the `GET`. Spec unchanged. |
| F-11 | Yes — `Agent.yaml` has no `degraded` field | **Apply.** FR-024 now lists `Agent.degraded_reason` (enum `[needs_provider]`, beside ADR-068's `needs_model: boolean` — same `Agent.yaml` edit, one PR), `Provider.protocol`, `ProviderUpdateRequest.protocol`, the six-value `Provider.status` enum shared verbatim with ADR-068, `EntitlementResponse.yaml`, `ProbeProviderRequest.id` `maxLength: 64`. DS-5 row 18. |
| F-12 | Yes — `validate.go::FetchModels` is OpenAI-compatible only | **Apply.** FR-021 per protocol: `openai-compatible`/`google` → `GET {api}/models`; `anthropic` → `GET {api}/v1/models` with `x-api-key` + `anthropic-version`; `ollama` → `/api/tags`; `cli` and custom rows → 409 `entitlement not supported for this protocol` (button hidden). DS-5 rows 10–11 re-keyed per protocol; row 11b (409). |
| F-13 | Yes — and refined: today's `google` case falls into the generic HTTP branch (`factory_provider.go::CreateProviderFromConfig`) and sends only a Bearer key; no Gemini-specific header exists | **Apply (as the code is).** `case google` constructs the HTTP provider at the row's URL (Gemini's OpenAI-compatible base, from `overrides/`) with Bearer auth — one implementation, distinct enum value kept so the URL rule stays explicit (FR-012). |
| F-14 | Yes — `buildProviderPool` skips unknown candidates with WARN | **Apply.** FR-016 rewritten: `needs_provider` iff the **primary** provider is unknown; an unknown fallback-only provider is dropped with one WARN naming agent and provider and the agent runs on the rest. Scenarios + DS-8. |
| F-15 | Yes — `instance.go::findModelConfigForProvider` uses `strings.EqualFold` (line ~702) | **Apply.** FR-036: exact comparison after `TrimSpace` across `pkg/agent`, `pkg/gateway`, `pkg/providers`; `findModelConfigForProvider` and `resolveAgentPrimaryProvider` listed as modified; DS-8 row 4 (`ZAI` entity vs `zai` config → `needs_provider`). |
| F-16 | Checked live against models.dev 2026-08-22: `longcat`, `modelscope`, `vivgrid`, `nvidia`, `azure`, `lmstudio` are registry providers; `azure` has no `api` | **Apply.** Disposition table for every current factory case in §5 (Integration Boundaries → "Factory case disposition"); `azure`/`azure-openai` → `azure`, `tier: unsupported`, `unsupported_reason: deployment-url`. FR-026 asserts "every unsupported row has a reason" (count dropped — F-35). |
| F-17 | Yes — `go build` needs the module cache | **Apply.** T48 replaced: assert `pkg/providers/catalog/data/providers_catalog.json` is git-tracked, `go:embed` references only that path, no `//go:generate` in `pkg/providers/catalog` invokes a network tool, and `go build` runs with `GOFLAGS=-mod=mod GOPROXY=off` after a warm-cache step. |
| F-18 | Yes — a WARN needs code that reads the old file | **Apply.** US-3.AC7 scenario deleted; replaced by "new-path file with `schema_version: 1.0.0` → ignored, one WARN `reason=schema_version`"; the old file is invisible (zero log lines). |
| F-19 | — | **Apply.** FR-002: `protocols[]` MAY be omitted; when present it MUST contain the primary with the same `api`; entries unique; `protocol` MAY be empty only when `tier: unsupported`. DS-1 rows 23–25. |
| F-20 | Yes — keys come from `api_key_ref`; nothing reads env | **Apply.** `env` is opaque (picker hint only); "key variable" removed from FR-012 and the Behavioral Contract. |
| F-21 | — | **Apply.** SC-005 / US-3.AC1 replaced by a structural assertion: the refresh goroutine starts after the listener is bound and a recording stub sees zero hits before listen. |
| F-22 | — | **Apply.** SC-011: `Resolve` mean < 1,000 ns/op with 0 allocs (`-benchmem`); the `GET` serves a pre-serialised byte slice + ETag cached at apply time, swapped atomically as one pair. |
| F-23 | — | **Apply.** Entitlement cache keyed `(provider id, SHA-256(key))`; evicted on provider PUT/DELETE and on catalog refresh. |
| F-24 | — | **Apply.** FR-037: `stale: true` on the `GET` when `updated_at` > 14 days; `/health` reports catalog degraded with the last refresh error; one INFO per successful refresh. |
| F-25 | — | **Apply.** FR-022: rule kept (A-20), plus fall-through to the next candidate on `model_not_found`, bounded to 3 attempts. |
| F-26 | — | **Apply.** Catalog refresh invalidates the entitlement cache; running agents unaffected; SPA re-fetches on ETag change (E10). |
| F-27 | — | **Apply.** No manual refresh endpoint; H-EP1 uses a test seam; stated in FR-008. |
| F-28 | — | **Apply.** Raw fallback `Ref` pinned to `main` in code (no override); accepted-risk text in §5. |
| F-29 | Yes — no ETag handling in `pkg/gateway` | **Apply.** FR-017: `ETag: "<sha256>"` (quoted, strong), `Cache-Control: private, max-age=0, must-revalidate`, no content negotiation, weak `W/` rejected; served bytes and ETag are one atomically-swapped pair. |
| F-30 | — | **Keep `aliases[]`** — operator-decided (A-9); the prohibitions stay. |
| F-31 | — | **Apply.** CI check on release tags fails when the embedded `updated_at` is > 14 days old (T49). |
| F-32 | — | Follow-up noted for ADR-068 (`?providers_only=1` projection); not in this spec. |
| F-33 | — | **Apply.** `seed_parity.json` gains `correction_source` (models.dev commit). |
| F-34 | — | **Apply.** Startup pull skipped when the persisted document is < 1 h old (FR-008). |
| F-35 | — | **Apply.** FR-026 asserts every `unsupported` row has a reason; only the `popular` set is pinned by name. |
| Structural | — | T33c (repair without restart), scenario for the entitlement 422, T6b/E6 (persisted newer than embedded wins), T20b/E8 (retired model still constructs), `ProbeProviderRequest.id` `maxLength: 64`, atomic bytes+ETag swap test (T34c), regression rows for `pkg/voice/audio_model_transcriber.go`, `pkg/providers/legacy_provider.go`, `cmd/omnipus/internal/onboard/validate_integration_test.go` (uses `GetDefaultAPIBase` ×4 — rewritten). Atomic landing is enforced by the compiler: `GetDefaultAPIBase`/`IsKnownProtocol`/`knownProtocols` are deleted in the same change as the table-backed factory, so a partial state cannot build; T24 prevents re-adding a vendor case. |
| Unasked Q3 | — | Rows that vanish upstream are carried forward with `status: retired` / `unsupported_reason: withdrawn` (ADR §8b). |
| Unasked Q4 | — | Override re-clamp on refresh is ADR-066 D2's rule and the override itself lives in ADR-066's `ContextSettings.yaml` `model_overrides[{provider, model, context_window}]`; this spec defines no override and only guarantees `Window()` reflects the new catalog value immediately. |
| Unasked Q8 | — | Refresh does not rebuild provider instances; a changed `api` takes effect at the next agent reload (ADR §8b). |
| Unasked Q9 | — | `GET /providers` returns configured rows **with** `models[]` filled from the catalog (US-9.AC1); the SPA does not join. The default model shown against a row is ADR-068's `DefaultModel.yaml` pair (`agents.defaults.default_model`), not a `model_name` alias. |
| Unasked Q10 | — | A persisted file rejected under FR-033 logs `reason=invalid` with the path; the `GET` shows `source: embedded` so the condition is visible. |
| Cross-doc | — | `Provider.status` six-value enum cited verbatim in FR-024; the entitlement route is `POST /api/v1/providers/{id}/entitlement` → `EntitlementResponse.yaml` (single name/shape); `Agent.yaml` carries `degraded_reason` (here) and `needs_model` (ADR-068) with `needs_provider`-wins precedence in both specs; the default model is the `(provider, model)` pair in `agents.defaults.default_model` (`DefaultModel.yaml`, ADR-068 CRIT-001) — this spec reads and writes no `model_name` (verified by grep); per-model window overrides are owned by ADR-066's `ContextSettings.yaml` `model_overrides[{provider, model, context_window}]` — this spec defines no competing override; the no-trace proof (SC-010) asserts only the absence of a canonical id, never the echoed user id. |

## 1.2 Amendment A2 — post cross-spec review (BINDING; overrides §1.1 and anything below on conflict)

Coordinator decisions 2026-08-22. This spec **owns the single coordinated contract commit** (X-26): it edits `Provider.yaml`, `ProviderUpdateRequest.yaml`, `Agent.yaml`, `ProbeProviderRequest.yaml`, `DefaultModel.yaml`, `DefaultModelUpdateRequest.yaml`, `EntitlementResponse.yaml`, the four hand-kept `LLMError` copies, and their `pkg/gateway/inboundschemas/` twins in ONE commit — including the values ADR-068 *defines* (`signed_in`, `expired`, `auth_method`, `account_label`, `dependents`, `backs_default`, `updated_at`, `needs_model`, `model_unassigned`) and the three optional read-only window fields ADR-066 defines. ADR-068/ADR-066 own the semantics of their fields; handlers here emit zero values (`dependents: []`, `backs_default: false`, `auth_method: api_key`, `needs_model` as derived by ADR-068's rule once it lands, window fields absent) until those specs' computations land.

| ID | Verified | Disposition |
|---|---|---|
| X-01 / X-02 | Yes — `contracts/asyncapi.yaml` L1512 `LLMError` and L1632 `LLMErrorReplay` inline, plus `LLMError.yaml` / `LLMErrorReplay.yaml`; `pkg/api/generated/llm_error_codes_test.go`, `llm_error_catalogue_test.go`, `llm_error_no_hardcopy_test.go` exist; `config` is in `x-user-message-attributions` | **Apply.** New code **`needs_provider`**, attribution `config`, copy *"This agent's provider isn't configured. Open Settings → Providers to connect one."*, added to **all four** copies; T33 asserts `LLMError.code == needs_provider`; regression rows for the three `llm_error_*_test.go`. Pre-turn gate order, stated once: `needs_provider` (this spec) → `model_unassigned` (ADR-068) → ADR-066's context-window refusal (third). FR-038. |
| X-03 | — | **Apply.** Cache key = `SHA-256(providerID + ":" + credentialRefName)` — the ref **name**, never the secret value; evicted on provider DELETE (ADR-068 FR-010 step 3b) and on a PUT that changes `api_key`/`api_key_ref`; a PUT that only bumps `updated_at` is **not** an eviction (unasked Q5). Catalog refresh still evicts. FR-021, T37. |
| X-04 | Yes — `pkg/gateway/inboundschemas/ProbeProviderRequest.yaml` exists | **Apply.** ONE `ProbeProviderRequest` shape: `{id (1..64, no enum, no pattern), auth (required: api_key \| sign_in), api_key?, model? (1..256), api_base?, protocol? (openai-compatible \| anthropic)}`; `api_key` required iff `auth = api_key`; `api_base` + `protocol` required iff `id` is a custom row. `endpoint` is renamed `api_base` throughout. Every probe row in DS-5 sends `auth: api_key`. This spec owns `id`/`api_base`/`protocol`; ADR-068 owns `auth`/`api_key`/`model`. FR-023. |
| X-05 / X-06 / X-26 | Yes — `inboundschemas/Provider.yaml`, `Agent.yaml`, `ProviderUpdateRequest.yaml` exist | **Apply.** Single `Provider.yaml` edit carrying `protocol`, `custom` (X-13) **and** ADR-068's `auth_method`, `account_label`, `dependents`, `backs_default`, `updated_at`; `ProviderUpdateRequest` gains `protocol` and `auth_method`; `Agent.yaml` gains `degraded_reason`, ADR-068's `needs_model`, and ADR-066's `context_window_effective` / `context_window_source` (`$ref ContextWindowSource.yaml`, owner ADR-066) / `context_window_clamped` as **optional**; `DefaultModel.yaml` + `DefaultModelUpdateRequest.yaml` written here. T31b asserts the **full** enums. FR-024 rewritten. |
| X-10 | — | **Apply.** Provider shape gains `company` (string; grouping key from `overrides/` via the models.dev name family; default = `name`). ADR-068's tile/letter grouping keys on it. Source-per-field list updated. |
| X-11 | — | **Apply.** `subscription_policy` dropped from the shape. |
| X-12 | — | **Apply.** Entitlement upstream non-2xx → HTTP 502 `{"error":"could not fetch upstream model list: status <n>"}`, nothing cached. FR-021, DS-5 row 11c. |
| X-13 | — | **Apply.** Provider config rows gain `custom: true` (persisted `ModelConfig.Custom`, wire `Provider.custom`, true iff the id is not in the catalog); every check and test is on the flag, never on the literal id `custom`. All literal `custom` ids in scenarios/datasets replaced by `my-proxy`. FR-035. |
| X-14 / X-41 | Yes — `grep -rli copilot pkg src contracts` is empty | **Apply.** `case "cli"` dispatches on the row field **`cli_kind ∈ {codex, copilot}`** (local-providers file), never by id; `github-copilot` added to the local-providers file (ADR-068 specifies the Copilot subprocess provider). `openai-chatgpt` is **not** `cli`: `protocol: openai-compatible`, `token_source: codex-auth-json`, `auth_methods: [sign_in]` — cloud for ADR-066. Disposition table, FR-012, T24 (the `cli` case may switch on `cli_kind`; still no vendor-id case). |
| X-16 / X-17 | — | **Apply.** ONE definition of "local endpoint", exported from `pkg/providers/catalog`: provider `locality ∈ {local, cloud}`, derived `local ⇔ protocol ∈ {ollama, vllm} ∨ id = lmstudio ∨ (custom row ∧ api host loopback/private)`. `lmstudio` is a catalog id in the local-providers file, not a factory case. FR-033's exception list and FR-020's local set both become "`locality = local`"; no other classification exists on this side. ADR-066/068 reference `locality`. FR-039. |
| X-18 / X-36 | — | **Apply (ADR §4.3 amended).** Four sanctioned live calls: entitlement, local endpoints, validation probe, **and** ADR-066's rung-3 limit query (cloud providers that publish limits; on demand, 24 h on-disk cache, never on a timer, never at boot). Prohibition narrowed to "never to populate the selector, never on a timer, never at boot". |
| X-23 | — | **Apply.** This spec owns every backend deletion and the removal of `src/lib/generated/providerCatalog.ts`; ADR-068 owns the SPA consumers (`fetchModelCapabilities`, the D18 callers, `refreshProviderModels`, `PROVIDER_CATALOG` importers). |
| X-24 | Yes — `pkg/agent/model_resolution.go` header L18–22 and `buildModelListResolver` L49/L64 use `GetModelConfig` (ModelName) and `ExtractProtocol` | **Apply.** Residual `buildModelListResolver` rule set (FR-040): (1) a reference is the pair `(provider, model)`; exact match on `cfg.Providers[i].Provider == provider && Model == model` wins; (2) a bare model id with no provider matches iff exactly one configured, non-degraded provider lists it (catalog models for cloud rows; the row's manual `models[]` for `locality = local` rows); (3) otherwise unresolved → ADR-068's `needs_model`. No alias, no `ModelName`, no prefix split, no passthrough. |
| X-25 | — | **Apply.** One `ModelConfig` field list (FR-013): kept — `Provider`, `Model` (bare), `Protocol` (S67), `Custom` (S67), `APIBase`, `APIKeyRef`, `AuthMethod` (ADR-068: closed set `api_key \| sign_in`; `oauth`/`token` removed), `UpdatedAt` (ADR-068), `Models` (manual list, local rows), `Proxy`, `MaxTokensField`, `RequestTimeout`, `ThinkingLevel`, `ExtraBody`, `RPM`, `Home`, `Fallbacks`, `Name`; deleted — `ModelName` alias (ADR-068 CRIT-001). |
| X-27 / X-29 | — | **Apply.** Sequencing section below; regression table cites ADR-066's `TestConfig_NoContextWindowDefaultKey` and ADR-068's `TestDefaultsSeed_NoRemovedProvider` as "must pass after merge"; landing order S67 → S68 → S66. |
| X-31 | Yes — `subturn_target_identity_test.go` uses `Provider: "mock"` | **Apply.** File moved to "re-keyed fixtures": `mock` becomes a custom row (`provider: mock, custom: true, api_base: <stub>, protocol: openai-compatible`); ADR-066 adds assertions only after that re-key. |
| X-32 | — | **Apply.** `provider_credential_degraded_test.go` listed as "updated by ADR-068" (template-row shape gone). |
| X-34 | — | **Apply.** Grep gates (T29, SC-009) are evaluated on the **merged** branch; allow-lists are the ones ADR-068 enumerates in `scripts/no-removed-providers.allow` (which must include this spec, the review files and ADR-067 for the historical `claude-cli` literal in the disposition table). |
| X-40 | Yes — ADR §2/§7 contradicted §8a | **ADR amended** (`afcfe834`). |
| Unasked Q1 / Q4 / Q5 | — | Q1: the `inboundschemas/` copies are edited in this spec's contract commit. Q4: `Provider.models` is `[]` for an `unknown-provider` row; a local model lacking a window carries ADR-066's per-model field, not a status. Q5: see X-03. |

### Sequencing (X-27)
1. **S67 lands first** — including the whole coordinated contract commit above; nothing in S67 depends on S66 or S68 at build time.
2. **S68** lands next (consumes the catalog `GET`, `unknown-provider`, `needs_provider`, entitlement, free-string probe id, canonical ids, `locality`, `cli_kind`, `custom`).
3. **S66 backend** may be developed in parallel behind this spec's package API (`catalog.Resolve(provider, model).Window()`, `locality`) but **merges after S67** — its catalog rung does not compile before the contract commit and the `pkg/providers/catalog` fold; its row/picker UI slice lands after S68.

## 2. Available Reference Patterns

`docs/reference/go-implementation/00-overview.md` does not exist in this repository (checked 2026-08-22). **N/A.** The reusable in-repo patterns are the ones the spec builds on directly: `pkg/providers/capabilities/puller.go` (Release-API fetch → raw fallback → `.sha256` verify, `ErrChecksumMismatch`), `capabilities/catalog.go` (two-stage DTO→validated parse, `refreshLocked` transaction with semver anti-downgrade and persisted last-known-good, `degradedTransportReporter`), `capabilities/version.go` (semver-aware `Version.Compare`), and `pkg/gateway/gateway.go::runCapabilityCatalogRefreshLoop` ("refresh once immediately, then tick").

## 3. Existing Codebase Context

> GitNexus MCP tools are not exposed in this session, and `~/.gitnexus/registry.json` registers the `omnipus` index against a **different checkout** (`wt-library-improvements`, branch `feat/library-improvements`), not this worktree. Per CLAUDE.md the fallback to direct Read/Grep is correct; the tables below were built that way. Risk levels are hand-assessed from caller counts (non-test Go, this tree).

### Symbols Involved

| Symbol | Role | Context (read 2026-08-22) |
|---|---|---|
| `pkg/providers/capabilities/catalog.go::Catalog` (+ `Resolve`, `resolveStrippedPrefix`, `optimistic`, `Models`, `HasModal`, `Refresh`/`refreshLocked`, `seedFile.validate`, `ParseSeed`) | **modifies / moves** into `pkg/providers/catalog` | Model-only key; `Resolve(modelID)` falls back to prefix stripping, then an optimistic default (text+image). Consumers: `pkg/agent/loop_media.go` (`catalog.Resolve(model).Budget()`), `pkg/agent/media_present.go` (`Supports(ModalityImage/PDF)`, `SetCapabilityCatalog`), `pkg/media/resize/resize.go`, `pkg/gateway/rest.go` (`/providers/model-capabilities` uses `catalog.Models()`), `pkg/agent/context.go`, `pkg/config/sandbox.go` (type refs). |
| `capabilities/puller.go::GHReleasePuller` (`Pull`, `fetchReleaseAsset`, `fetchRaw`, `verify`, `LastPullDegraded`, `ErrChecksumMismatch`) | **calls / retargets** | Wired in `pkg/gateway/gateway.go` as `NewGHReleasePuller("elicify-ai", "omnipus", "providers_capabilities.json")`; 30 s client; release API first, raw fallback (flagged degraded). **Kept; only owner/repo/asset change** (ADR §2 "Omnipus-side changes"). |
| `capabilities/version.go::Version` | calls | Semver-aware compare used by the anti-downgrade check. Kept. |
| `capabilities/embed.go::EmbeddedSeed` | **replaced** | Embeds the 1.0.0 seed; becomes the 2.0.0 committed snapshot in `pkg/providers/catalog/data/`. |
| `pkg/gateway/gateway.go::capabilityCatalogRefreshInterval` (7 d), `capabilityCatalogRefreshTimeout` (30 s), `runCapabilityCatalogRefreshLoop`, `capFileStore` (`$OMNIPUS_HOME/capabilities_catalog.json`), `capabilityCatalogLogAdapter` | **modifies** | Interval → 24 h; loop already does "refresh once, then tick" (startup pull exists in shape). |
| `pkg/providers/catalog/catalog.go::Entries`, `LoadCatalog`, `DeriveWire`, `validateCatalog`, `validateDisjointIDs`, `init`; `catalog/gen/main.go` | **replaced** | 23 hand-typed `gen.ProviderCatalogEntry` rows → loaded from the feed. `gen/main.go` emits `src/lib/generated/providerCatalog.ts`, which **goes** (ADR-068 §6 item 1; SPA reads the new `GET`). |
| `pkg/providers/factory_provider.go::CreateProviderFromConfig`, `ExtractProtocol`, `knownProtocols`, `IsKnownProtocol`, `AllKnownProtocols`, `GetDefaultAPIBase` | **modifies (collapse)** | `switch protocol` with ~40 vendor cases + ~45-entry `GetDefaultAPIBase` switch + `knownProtocols` map. Callers of `CreateProviderFromConfig`: `pkg/agent/instance.go` (×3, incl. `buildProviderPool`), `pkg/agent/loop.go`, `pkg/providers/legacy_provider.go`, `pkg/voice/audio_model_transcriber.go`. `GetDefaultAPIBase`: `rest.go` ×7, `rest_onboarding.go` ×3, `cmd/omnipus/internal/onboard` ×1, `pkg/sysagent/tools/provider.go` ×1, `catalog.go` ×1. `IsKnownProtocol`: `rest_onboarding.go`, `onboard.go` ×2, `catalog.go`. `ExtractProtocol`: `pkg/agent/model_resolution.go` ×2, `pkg/voice/transcriber.go`. |
| `pkg/providers/displayname.go::knownDisplayNames`, `DisplayName` | **replaced** | 11-entry map, title-case fallback. Callers: `rest.go` ×2, `rest_onboarding.go` ×2. Display name comes from the catalog `[A-14]`. |
| `pkg/providers/validate.go::probeModelDefaults`, `pickProbeModel`, `FetchModels`, `ValidateKey` | **modifies** | Probe model picked from a live `/models` list or a 10-entry slug map; `FetchModels` is OpenAI-compatible only, 10 s, 2 MB. Callers: `rest_onboarding.go` (×2 `FetchModels`, ×2 `ValidateKey`), `rest.go` (`FetchModels` in `HandleProviders` + `refreshProviderModels`; `ValidateKey` ×2), `onboard.go`. |
| `pkg/gateway/rest.go::refreshProviderModels` (`POST /providers/{id}/refresh-models`), `HandleProviders` (live `/models` fill of `Provider.models`), `inferProviderName` | **modifies** | Today populates the selector from live `/models`. Becomes catalog-fed; live call survives only as the entitlement check `[A-13]`. |
| `pkg/gateway/rest_onboarding.go` probe path (`IsKnownProtocol` gate at ::126, `FetchModels`+`ValidateKey` at ::295–302) | **modifies** | Gate → catalog membership; probe model → catalog. |
| `pkg/agent/instance.go::findModelConfigForProvider` (`EqualFold` → exact, F-15), `buildProviderPool`, `resolveAgentPrimaryProvider`; `pkg/agent/registry.go` (`NewAgentInstance` loop at ::88); `pkg/agent/model_resolution.go` and `pkg/voice/transcriber.go` (callers of the deleted `ExtractProtocol`, F-06) | **modifies** | `buildProviderPool` already **skips with WARN** on a missing ModelConfig or a `CreateProviderFromConfig` error (non-fatal). MAJ-010 requires confirming the whole boot path is per-agent non-fatal and adding the typed refusal at turn time. |
| `contracts/components/schemas/ProbeProviderRequest.yaml` (`id` enum, 61 values), `ProviderCatalogEntry.yaml`, `ModelCapabilities.yaml`, `Provider.yaml`, `openapi.yaml` paths `/providers`, `/providers/{id}`, `/providers/{id}/test`, `/providers/model-capabilities`, `/onboarding/probe-provider` | **modifies** | Constraint #8 surface. |
| `src/lib/constants.ts` key-format hints (`anthropic`, `openai`, `groq`, `openrouter`) | modifies (re-key) | All four ids are already canonical in models.dev; no change in value, but the map's key domain becomes "catalog id". |
| `src/lib/agents/providerCatalog.ts`, `src/lib/providerMigration.ts`, `src/components/settings/ProvidersSection.tsx`, `ProviderRow.tsx`, `src/routes/onboarding.tsx` | consumers (ADR-068 owns behaviour) | `providerMigration.ts` is an alias resolver → deleted under greenfield (its input no longer exists). |

### Impact Assessment

| Symbol Modified | Risk | d=1 (WILL BREAK — must update/test) | d=2 (LIKELY AFFECTED — should test) |
|---|---|---|---|
| `CreateProviderFromConfig` (protocol dispatch) | **HIGH** — every LLM call path constructs through it | `instance.go` (`NewAgentInstance`, `buildProviderPool`), `loop.go` (provider-pool rebuild), `session_end.go` (recap pool), `legacy_provider.go`, `voice/audio_model_transcriber.go` | every agent turn, fallback chain, recap, voice transcription; `rest.go` rewire-after-save |
| `GetDefaultAPIBase` / `IsKnownProtocol` / `knownProtocols` (deleted or table-backed) | **HIGH** — 13 call sites across REST/CLI/tools | `rest.go` (`HandleProviders`, `refreshProviderModels`, `/test`, PUT), `rest_onboarding.go`, `onboard.go`, `sysagent/tools/provider.go`, `catalog.go` | onboarding two-phase commit, provider test, `system.provider` tool |
| `capabilities.Catalog` → `catalog.Catalog` with `Resolve(provider, model)` | MEDIUM | `loop_media.go`, `media_present.go`, `resize.go`, `rest.go` model-capabilities, `context.go`, `config/sandbox.go` | media ingest/present path, vision warning toast |
| `catalog.Entries` / `LoadCatalog` / `gen/main.go` | MEDIUM | `catalog_test.go` drift guards, `src/lib/generated/providerCatalog.ts` and its 10 SPA importers | Settings Providers list, onboarding step 3 (ADR-068) |
| `pickProbeModel` / `probeModelDefaults` / `FetchModels` | MEDIUM | `ValidateKey` → `/test`, PUT, onboarding probe, CLI onboard | false-Unreachable vs false-Valid probe outcomes |
| `ProbeProviderRequest.id` enum → string | MEDIUM | generated Go/TS/zod; `TestCatalog_DriftGuard_IdInProbeEnum` (deleted) | onboarding SPA form validation |
| `DisplayName` | LOW | `rest.go`, `rest_onboarding.go` | provider labels |
| `refreshProviderModels` route | MEDIUM | SPA `ProvidersSection`/`ProviderRow` callers | entitlement UX (ADR-068) |

**HIGH-risk warning (recorded per skill rule):** the factory collapse and the `GetDefaultAPIBase` removal touch the construction path of every provider instance. They must land with the table-backed replacement in the same change and with the regression set in §7 green; there is no partial state in which some providers dispatch by name and others by protocol.

### Relevant Execution Flows (hand-traced)

| Flow | Relevance |
|---|---|
| Gateway boot → `capabilities.NewCatalog(EmbeddedSeed, GHReleasePuller, capFileStore)` → `agentLoop.SetCapabilityCatalog` → `go runCapabilityCatalogRefreshLoop(…, 7d, 30s)` | Becomes the 2.0.0 catalog boot; interval 24 h; first pull is already immediate-in-background. |
| `refreshLocked`: Pull → degraded check → `ParseSeed` → version anti-downgrade → `applySeed` → `store.Write` | Unchanged shape; parse becomes 2.0.0, plus a **schema-version gate** (non-2.0.0 ignored like a checksum mismatch). |
| Media ingest: `loop_media.go` → `catalog.Resolve(model).Budget()`; present: `media_present.go` → `Supports(modality)` | Key becomes `(provider, model)`; the loop already knows the agent's provider. |
| Agent construction: `NewAgentInstance` → `resolveAgentPrimaryProvider` → `buildProviderPool` → `findModelConfigForProvider` → `CreateProviderFromConfig` | Unknown provider: pool entry skipped with WARN today; MAJ-010 adds a typed, per-agent "needs a provider" state and turn-time refusal. |
| Onboarding probe: `IsKnownProtocol` gate → `GetDefaultAPIBase` → `FetchModels` → `ValidateKey(pickProbeModel)` | Gate → catalog membership; base URL → catalog; probe model → catalog; `FetchModels` pre-fetch dropped from this path. |
| Settings providers: `HandleProviders` → live `FetchModels` fills `Provider.models`; `POST /providers/{id}/refresh-models` | Selector fill → catalog; live call survives as explicit entitlement check only. |
| ADR-066 D2 ladder (`pkg/agent/loop.go`, symbol to be named by that spec) → catalog rung | This catalog supplies `context_window`/`max_output_tokens`; a miss is a miss (no prefix strip) and falls to the next rung. |

### Cluster Placement

Providers / LLM transport (`pkg/providers/*`), with spokes into gateway REST, agent media path, and contracts. Spans three clusters; the architectural consequence is that the catalog type must live in `pkg/providers/catalog` with **no import of `pkg/gateway` or `pkg/agent`** (same direction as today's `capabilities`), and `pkg/providers/catalog` must not import `pkg/providers` (today it does, for `IsKnownProtocol` — that edge is removed with the fold so the factory can import the catalog instead).

---

## 4. User Stories & Acceptance Criteria

### US-1 — One catalog document, schema 2.0.0, validated on load — **P0**
The operator and every Omnipus consumer read one document: providers with nested models. The system accepts a document only when it is well-formed under the 2.0.0 shape and its invariants hold; otherwise it keeps what it had. *Why P0:* everything else in this spec reads this document. *Independent test:* feed documents to the parser and observe accept/reject and the resulting lookups.

1. **Given** a document with `schema_version` `2.0.0`, a non-empty `version`, `updated_at`, `source`, a default resize limit `[A-10]`, and ≥1 provider with ≥1 model, **When** it is loaded, **Then** the load succeeds and each `(provider id, model id)` pair is resolvable with its own `context_window`, `max_output_tokens`, `input_modalities`, `tool_call`, `status` and the provider's resize limits.
2. **Given** a document with any `schema_version` other than `2.0.0`, **When** it is loaded, **Then** the load is rejected with a message naming the schema version, and the previously loaded document is retained.
3. **Given** a document where a provider id is duplicated, a model id is duplicated within one provider, a provider or model id is empty, a `protocol` value is outside the protocol set, or a model's `input_modalities` lacks `text`, **When** it is loaded, **Then** the load is rejected with a message naming the offending path and the previous document is retained.
4. **Given** two providers each carrying a model with the same model id, **When** loaded, **Then** both are accepted and resolve independently (the key is the pair, not the model id).
5. **Given** a model entry without `max_output_tokens` or with `context_window` `0`, **When** loaded, **Then** the entry is accepted and its window/output are reported as *unknown* to consumers `[A-11]` (ADR-066's ladder decides what to do).

### US-2 — Assembly repository contract (integration boundary) — **P0**
The assembly job (separate codebase) publishes what Omnipus expects; Omnipus specifies the boundary so both sides can test against it. *Why P0:* the consumer is only as correct as the contract. *Independent test:* a fixture that conforms to the contract loads; each violation in §5 Integration Boundaries is rejected.

1. **Given** the published release, **When** Omnipus fetches it, **Then** exactly one asset `providers_catalog.json` and one sidecar `providers_catalog.json.sha256` exist, the sidecar is the hex SHA-256 of the asset bytes, and the asset is a 2.0.0 document.
2. **Given** the two registries differ on a numeric field by ≤ 5 % or ≤ 4,096 tokens (whichever is larger), **When** the job publishes, **Then** the lower value is published with both recorded and no issue is opened; **Given** a larger delta (or any boolean/enum difference), **Then** the previously published value (last known good; models.dev's when none) is published with `disputed: true` and one issue is opened; **Given** that issue is closed with an adjudicated value, **Then** it lands in `overrides/` and wins on the next run. The release is never blocked.
3. **Given** a provider present in models.dev with no `api` URL but OpenAI-compatible on the wire, **When** the job publishes, **Then** the provider carries the URL from `overrides/` and is marked reachable; a cloud-IAM provider carries `unsupported: cloud-iam` and no URL requirement.
4. **Given** a provider absent from models.dev but shipped by Omnipus (`ollama`, `vllm`, `litellm`, `lmstudio`, `codex-cli`, `openai-chatgpt`, `github-copilot`, `shengsuanyun`, `volcengine`, `avian`, `mimo`), **When** the job publishes, **Then** it appears in the document from the local provider file with the same shape as registry providers. Custom rows are **not** in the document (F-07/F-08, X-13).
5. **Given** the document is published, **When** an Omnipus release is cut, **Then** the committed snapshot in the Omnipus repo is the document from a scheduled pull request, and `go build` reads nothing from the network.

### US-3 — Refresh: retargeted puller, 24 h + startup, checksum and schema gates, last known good — **P0**
The running gateway keeps its catalog fresh without ever blocking boot or trusting a bad file. *Why P0:* the offline/online agreement and integrity behaviour are the ADR's exit proofs 2 and 3. *Independent test:* stub the release host; observe what the in-memory catalog serves after each outcome.

1. **Given** the gateway starts with no network, **When** boot completes, **Then** the embedded snapshot serves every lookup, the refresh goroutine starts only after the listener is bound, and a recording stub host sees zero hits before listen (F-21).
2. **Given** the gateway starts with network, **When** the background startup pull succeeds, **Then** the pulled document replaces the snapshot in memory and is persisted as last known good, within the 30 s pull timeout.
3. **Given** a running gateway, **When** 24 h elapse since the last attempt, **Then** another pull is attempted; no pull is attempted on any request path.
4. **Given** a pulled asset whose checksum does not match its sidecar, **When** refresh runs, **Then** the asset is rejected, exactly one WARN is logged naming the mismatch, and the current document continues to serve.
5. **Given** a pulled document whose `schema_version` ≠ `2.0.0`, **When** refresh runs, **Then** it is ignored the same way (one WARN, current document retained).
6. **Given** a pulled document whose `version` is lower than the current one, **When** refresh runs, **Then** it is rejected (no downgrade) and the current document is retained.
7. **Given** a persisted `providers_catalog.json` whose schema is not 2.0.0, whose envelope is unreadable, or which fails FR-033, **When** the gateway boots, **Then** it is ignored with one WARN naming the reason and the embedded snapshot serves; the legacy `capabilities_catalog.json` is never read and produces zero log lines (F-18).
8. **Given** the release API is rate-limited or absent but the raw path serves a verifiable asset, **When** refresh runs, **Then** the raw path result is accepted and the catalog records the degraded transport (existing behaviour retained).

### US-4 — Exact `(provider, model)` resolution in one package — **P0**
Every consumer asks one function for one route's facts and never receives another route's. *Why P0:* exit proof 1; the wrong-route alias is the defect being removed. *Independent test:* load the fixture with `zai`/`openrouter` and assert the three lookups.

1. **Given** the catalog contains `(openrouter, z-ai/glm-5.2)` at 1,048,576 and `(zai, glm-5.2)` at 1,000,000, **When** each pair is resolved, **Then** each returns its own number.
2. **Given** the same catalog, **When** `(openrouter, glm-5.2)` is resolved, **Then** the result is a miss — not a prefix-stripped hit.
3. **Given** a miss, **When** the media pipeline asks for modalities, **Then** it receives the optimistic modality default (text+image) with the catalog default resize limit, exactly as today; **and when** the agent loop asks for the window, **Then** it receives *unknown* (the ADR-066 ladder continues).
4. **Given** `pkg/providers/capabilities` no longer exists, **When** the tree is built, **Then** every former consumer compiles against `pkg/providers/catalog` and there is exactly one embedded catalog file in the binary.

### US-5 — Provider identity from the registry; protocol is a field; factory dispatches on protocol — **P0**
An operator configures a provider by its registry id; Omnipus builds the transport from the table, not from a name switch. *Why P0:* exit proofs 4 and 5. *Independent test:* construct providers from table rows and inspect base URL/protocol; grep the factory for vendor cases.

1. **Given** a configured provider `zai` with a key, **When** a transport is constructed, **Then** it is the OpenAI-compatible transport with `zai`'s base URL from the catalog and the key from the credential ref.
2. **Given** `minimax` (registry protocol anthropic), **When** constructed, **Then** it is the Anthropic-Messages transport with `minimax`'s URL.
3. **Given** a provider that offers two protocols (`zai`: openai-compatible + anthropic from overrides) and a config choosing `protocol: anthropic` `[A-8]`, **When** constructed, **Then** the Anthropic transport with the anthropic endpoint is used; with no choice the provider's primary protocol is used.
4. **Given** a config whose provider id is not in the catalog but carries `api_base` **and** `protocol ∈ {openai-compatible, anthropic}`, **When** constructed, **Then** it is accepted through the `custom` factory case as a custom row keyed by that id (F-07/F-09); two such rows with different ids coexist.
5. **Given** the factory source, **When** inspected, **Then** there is no `case` on a vendor name; the cases are exactly the protocol set (`openai-compatible`, `anthropic`, `google`, `ollama`, `cli`) `[A-7]`.
6. **Given** a config with `provider: z-ai`, `moonshot-cn-anthropic`, `qwen-intl`, or any non-canonical spelling, and no (`api_base` + `protocol`) pair, **When** constructed, **Then** it fails as *unknown provider* with a typed error that does **not** name a canonical id.
8. **Given** `ModelConfig.Model` `z-ai/glm-5.2` under `provider: openrouter`, **When** the catalog is consulted, **Then** the key is exactly `(openrouter, z-ai/glm-5.2)`; no prefix is split off and there is no `openai` default (F-06).
7. **Given** the fresh-install seed and `config/config.example.json`, **When** inspected, **Then** every provider id is a catalog id.

### US-6 — Unknown provider degrades per provider and per agent; boot never aborts — **P0**
An operator whose config names an unknown provider still reaches Settings to fix it. *Why P0:* MAJ-010; the alternative is an install with no UI path to repair. *Independent test:* boot with one unknown provider and two agents (one bound to it).

1. **Given** config with providers `openai` and `nope`, and agents A (openai) and B (nope), **When** the gateway boots, **Then** boot succeeds, A runs turns normally, and the providers list shows `nope` in an *unknown provider* state.
2. **Given** the same, **When** B is asked to run a turn, **Then** the turn is refused with a typed error stating the agent needs a provider, nothing is sent upstream, and the agent list marks B as needing a provider `[A-16]` (`degraded_reason: "needs_provider"`; when ADR-068's derived `needs_model` is also true, `needs_provider` takes precedence in any copy — a provider must exist before a model can).
5. **Given** agent C whose primary is `openai` and whose fallback names `nope`, **When** constructed, **Then** C is **not** degraded; `nope` is dropped from its pool with one WARN naming C and `nope`; C's turns run (F-14).
6. **Given** agent D whose entity says `provider: "ZAI"` and config has `zai`, **When** constructed, **Then** D is `needs_provider` (exact comparison, F-15).
3. **Given** the operator re-points B to `openai` through the existing agent update path, **When** saved, **Then** B runs without a restart beyond the existing reload mechanism.
4. **Given** an unknown provider, **When** the providers list is read, **Then** no rename, alias, or suggestion of a canonical id is produced anywhere (log, API, UI text).

### US-7 — Read-only providers-catalog endpoint — **P0**
The SPA and any client read the same document the gateway uses, through a contract-defined surface. *Why P0:* Constraint #8; ADR-068's picker cannot be built without it. *Independent test:* `GET` with and without auth; compare to the in-memory catalog; ETag round trip.

1. **Given** an authenticated request, **When** `GET /api/v1/providers/catalog` is called, **Then** the response is the full 2.0.0 document (providers with nested models, tier, protocol(s), unsupported reason, resize limits) plus `version`, `updated_at`, a `source` marker (`embedded` / `pulled`) and `stale: true` when `updated_at` is older than 14 days `[A-1]` (F-24).
2. **Given** an unauthenticated request, **When** called, **Then** 401.
3. **Given** a client sending `If-None-Match` with the current document's quoted strong ETag, **When** called, **Then** 304 with no body; a weak `W/` value or an unquoted value is treated as no match (200) `[A-1]` (F-29).
4. **Given** the catalog failed to construct at boot, **When** called, **Then** 503 with a typed error (never an empty 200 that looks like "no providers") `[A-12]`.
5. **Given** the response, **When** validated against the generated schema, **Then** it validates; the SPA consumes only the generated type.
6. **Given** a config with two configured providers, **When** `GET /api/v1/providers` is called, **Then** exactly those two rows are returned — no template/"disconnected" rows for unconfigured catalog providers (the picker lists the catalog; the providers list lists configurations; coordination note for ADR-068 D14).

### US-8 — Tiers are data: every provider selectable, Popular pinned, cloud-IAM visible-disabled, custom endpoint — **P1**
The operator can pick any registry provider; the picker knows which are popular and which are unsupported from the document, not from code. *Why P1:* the data must exist for ADR-068's UI; the UI itself is out of scope. *Independent test:* inspect the document and the endpoint output.

1. **Given** the document, **When** providers are listed, **Then** each carries `tier ∈ {popular, standard, unsupported}` `[A-9]`; the popular set is `openai, openrouter, anthropic, google, xai, groq, mistral, deepseek` (ADR §4.2, ~8 pinned).
2. **Given** a cloud-IAM provider (`amazon-bedrock`, `google-vertex`, `google-vertex-anthropic`, `watsonx`, `sap-ai-core`), **When** listed, **Then** `tier: unsupported` with `unsupported_reason: cloud-iam`, and **When** configured via PUT, **Then** 400 with the reason.
3. **Given** a standard-tier provider, **When** configured with a key, **Then** it is accepted and reachable through protocol dispatch with no probe requirement.
4. **Given** an operator-named custom row (`my-proxy`, `custom: true`), **When** configured with `api_base` and a protocol, **Then** it is accepted; `api_base` or `protocol` missing → 400.

### US-9 — The selector reads the catalog; live `/models` only for entitlement, local endpoints and the probe — **P1**
The operator sees the model list instantly and offline; a live call happens only when they ask what *their key* can use, when the endpoint is local, or when a key is being validated. *Why P1:* exit proof 3 depends on the data path; the surface's look is ADR-068. *Independent test:* network down, list models for `anthropic`; then trigger the entitlement action with a stub.

1. **Given** no network, **When** the provider's models are requested through the providers API, **Then** the catalog's models for that provider are returned with limits and modalities, and no outbound request is made.
2. **Given** an explicit entitlement check for provider P with key K `[A-13]`, **When** invoked, **Then** the protocol's listing call is made once with K (`openai-compatible`/`google` → `GET {api}/models`; `anthropic` → `GET {api}/v1/models` with Anthropic headers; `ollama` → `/api/tags`); the result is `EntitlementResponse` — the catalog list annotated `entitled: true/false` with `limits: "known"`, plus models the provider returned that the catalog lacks with `limits: "unknown"`, `checked_at`, and `cached`; cached by `SHA-256(providerID + ":" + credentialRefName)` for the process lifetime, evicted on provider DELETE, on a key-changing PUT, and on catalog refresh; never fetched at boot or on a turn. **Given** the upstream returns non-2xx, **Then** 502 with `could not fetch upstream model list: status <n>` and nothing cached. **Given** P's protocol is `cli` or P is a custom row, **Then** 409 `entitlement not supported for this protocol`. **Given** P has no resolvable key, **Then** 422 (F-12, F-23).
3. **Given** a provider with `locality = local` (X-16), **When** models are requested, **Then** the live listing is the source (`/api/tags` for ollama; `/v1/models` otherwise) and the catalog is not consulted for the list.
4. **Given** a key validation (`/providers/{id}/test`, onboarding probe), **When** it runs, **Then** the probe model is chosen from the catalog (first `status: active`, tool-calling, text-modality model of that provider `[A-20]`), falling through to the next candidate on a `model_not_found` response, at most 3 attempts (F-25); no `/models` pre-fetch is made for catalog providers.

### US-10 — `ProbeProviderRequest.id` becomes a free string validated against the catalog — **P0**
Onboarding can probe any of ~190 providers without a 61-value enum. *Why P0:* contract change that blocks US-8/US-9 and ADR-068. *Independent test:* probe `zai` (not in today's enum) and `z-ai` (in today's enum).

1. **Given** `id: "zai"`, **When** probed, **Then** the probe runs against `zai`'s catalog URL.
2. **Given** `id: "z-ai"`, **When** probed, **Then** 400 "unknown provider" (no hint).
3. **Given** `{id: my-proxy, auth: api_key, api_key, api_base, protocol}`, **When** probed, **Then** the probe runs against `api_base` as a custom row; without `api_base` **and** `protocol`, 400 (F-07, X-04). **Given** an `id` longer than 64 characters, **Then** 400 by schema (`maxLength`). **Given** `auth: api_key` without `api_key`, **Then** 400.
4. **Given** `id: "amazon-bedrock"`, **When** probed, **Then** 400 with `unsupported: cloud-iam`.

### US-11 — Greenfield enforcement — **P0**
Nothing in the binary maps old names, and nothing refers to them. *Why P0:* exit proof 5. *Independent test:* the greps in §8 SC-009/SC-010.

1. **Given** `pkg/providers` and `pkg/config`, **When** grepped for alias/migration/retired machinery, **Then** nothing provider-related matches (the catalog's own `status: retired` value is the single allowed token `[A-3]`).
2. **Given** `src/lib/providerMigration.ts` and `src/lib/generated/providerCatalog.ts`, **When** the tree is inspected, **Then** neither exists.

### Edge Cases

| # | Condition | Expected |
|---|---|---|
| E1 | Document > 16 MB `[A-18]` | Pull rejected ("catalog too large"), current document retained, one WARN. |
| E2 | Document contains a provider with zero models (e.g. a coding-plan variant whose models were all retired) | Accepted; provider listed; selector shows no models; not selectable as a model source (UI per ADR-068). |
| E3 | Model id containing `/` under a non-aggregator (e.g. `(zai, glm-5.2)` vs `(openrouter, z-ai/glm-5.2)`) | Exact match on the full string; `/` has no meaning to the lookup. |
| E4 | Provider id case/whitespace variance in config (`" ZAI "`) | Unknown provider (ids are exact, lowercase, trimmed only at the config boundary — trimming yes, case-folding no `[A-19]`). |
| E5 | Two refreshes race (ticker fires during a slow startup pull) | Serialized by the refresh mutex; second waits; no torn state. |
| E6 | Persisted last-known-good is newer than the embedded snapshot | Persisted wins at boot (existing behaviour; T6b). |
| E7 | Embedded snapshot fails its own validation (bad commit) | Gateway boots with **no** catalog; every catalog consumer degrades (media optimistic; window → ladder; `GET /providers/catalog` → 503); one ERROR at boot. |
| E8 | Provider `status: retired` on every model the agent uses | Agent still constructs (T20b); model selection UI flags it (ADR-068); no refusal at turn time. |
| E9 | Unicode in provider `name` / model `name` | Preserved byte-for-byte through load and `GET`. |
| E10 | Catalog refresh lands mid-session | `GET /providers/catalog` ETag changes; entitlement cache invalidated; running agents unaffected; SPA re-fetches on ETag change; no WS push (F-26). |
| E11 | Release exists but has no `.sha256` sidecar | **Rejected** with `reason=checksum`, current retained — a behaviour change from today's puller, which trusts a 404 sidecar (F-02, FR-032). |
| E13 | Hosted provider row with `api: http://…` or a loopback/private/metadata host | Document rejected whole (`reason=invalid`, path named); snapshot serves (F-03, FR-033). |
| E14 | Pulled body exactly 16 MB vs 16 MB + 1 | Accepted vs rejected `reason=too_large` — never `checksum` (F-05). |
| E12 | custom row (`my-proxy`) with `protocol: google` or `ollama` | 400 — custom rows accept `openai-compatible` or `anthropic` only (ADR §4.2 "any OpenAI- or Anthropic-compatible URL"). |

---

## 5. Behavioral Contract & Boundaries

### Behavioral Contract

- When a 2.0.0 document loads, the system serves exact `(provider, model)` facts to every consumer from one in-memory copy.
- When a document fails checksum, schema-version, invariant, size, or anti-downgrade checks, the system logs one WARN and keeps serving the previous document.
- When the gateway boots, the embedded snapshot (or newer persisted last-known-good) serves immediately; one background pull starts; boot is never delayed or aborted by it.
- When 24 h pass, the system pulls again; it never pulls on a request or turn.
- When a lookup misses, the system says *miss*; it never strips a prefix to find a neighbouring route.
- When a provider id is not in the catalog and not a custom row (`api_base` + `protocol`), the system marks that provider unknown and the agents bound to it as needing a provider; boot and other agents proceed; no rename or hint is produced.
- When a transport is constructed, the system dispatches on the provider's protocol and takes the URL from the catalog (or from the custom row's `api_base`); keys come from the credential store only.
- When a release has no `.sha256`, fails its checksum, exceeds 16 MB, or carries a non-`https`/private-host `api` for a hosted provider, the system rejects it and keeps serving the current document.
- When the SPA needs providers or models, the system serves the catalog via `GET /api/v1/providers/catalog`; a live `/models` call occurs only on an explicit entitlement check, for local endpoints, or inside a key validation.
- When a probe request names a provider, the system validates the id against the catalog at runtime (no enum).
- When a cloud-IAM provider is configured or probed, the system returns 400 with the `cloud-iam` reason.

### Explicit Non-Behaviors & Safeguards

#### Qualitative Prohibitions
- The system must not map, alias, or suggest canonical ids for old provider ids, because the greenfield rule forbids compatibility machinery and the §3.3 table is documentation only.
- The system must not fetch the catalog at build time, because builds must be hermetic (MAJ-005).
- The system must not verify a release signature or add a signing dependency, because signing was explicitly not adopted (ADR §2, §6 item 7); accepted risk is recorded there.
- The system must not call any provider's `/models` **to populate the selector**, nor on a timer, nor at boot; the four sanctioned live calls are entitlement, local endpoints, the validation probe, and ADR-066's on-demand rung-3 limit query with its 24 h cache (ADR §4.3 as amended, X-18/X-36).
- The system must not populate the selector from live `/models` for catalog providers, because the catalog is the source and offline listing is an exit proof.
- The system must not abort boot, refuse to start the gateway, or hide Settings because a provider id is unknown (MAJ-010).
- The system must not keep a second catalog file, package, or Go slice of providers, because one document is the decision ("One catalog, not two").
- The system must not strip provider prefixes during lookup, because that returns another route's limits.
- The system must not trust a release without a `.sha256` sidecar, because with signing not adopted the checksum is the only integrity check (F-02).
- The system must not send a key to a catalog URL that is not `https` on a public host unless the row is a local endpoint or a custom row the operator typed, because a tampered document would otherwise exfiltrate keys (F-03).
- The system must not resolve, validate, or construct anything from a provider's `aliases[]`, because that field exists only for the picker's search box (A-9); treating it as identity would reintroduce the alias table greenfield forbids.
- The system must not silently adopt a newer registry value during a disagreement (assembly side), because last-known-good is the decision (MAJ-014).
- The system must not implement cloud-IAM (SigV4/GCP OAuth/IBM IAM) transports, because they are excluded pending per-provider ADRs and Constraint #1.
- The system must not specify or change picker layout, grouping, "show all" affordances, or the entitlement button's UX — that is ADR-068.

#### Machine-Verifiable Constraints
- **HTTP:** `GET /api/v1/providers/catalog` → 200 + document; no auth → 401; `If-None-Match` match → 304 empty body; catalog unavailable → 503 `{"error":"provider catalog unavailable"}`. `PUT /api/v1/providers/{id}` with unknown id → 400 `{"error":"unknown provider \"<id>\""}`; with cloud-IAM id → 400 `{"error":"provider \"<id>\" is unsupported: cloud-iam"}`; `custom` without `api_base` → 400. `POST /api/v1/onboarding/probe-provider` same 400 vocabulary. Entitlement check `[A-13]` → 200 annotated list; provider has no key → 422 (existing `describeCredentialResolutionError` vocabulary).
- **Performance:** `GET /providers/catalog` serves from memory; p95 ≤ 50 ms for a ≤ 8 MB document on the CI worker; catalog lookup `Resolve(provider, model)` is O(1) map access, ≤ 1 µs p99 in a benchmark; startup pull bounded by 30 s and never on the boot critical path (boot-to-listen time unchanged ± 5 %).
- **Size:** pulled document > `maxCatalogAssetBytes` (16 MB) `[A-18]` rejected before parse with `reason=too_large` (read limit is cap + 1 byte); embedded snapshot ≤ 8 MB `[A-2]` enforced by a test.
- **HTTP caching:** `ETag: "<sha256 hex>"` (quoted, strong), `Cache-Control: private, max-age=0, must-revalidate`, no content negotiation; `W/` or unquoted `If-None-Match` → 200.
- **Logging:** rejections log exactly one line at WARN with keys `reason ∈ {checksum, schema_version, invalid, regressed, too_large}`; embedded-snapshot validation failure logs ERROR once.
- **Source:** `grep -rnE 'case "(z-ai|zhipu|moonshot|qwen|deepseek|groq|mistral|openrouter|gemini|minimax|nvidia|cerebras)' pkg/providers/factory_provider.go` → 0 lines. `grep -rn resolveStrippedPrefix pkg` → 0. `ls pkg/providers/capabilities` → not found. Exactly one `//go:embed` of a catalog JSON in `pkg/providers`.
- **Contract:** `make verify-contracts` exit 0; `ProbeProviderRequest.id` has no `enum`; schemas `ProvidersCatalog`, `CatalogProvider`, `CatalogModel`, `EntitlementResponse` `[A-13]` exist under `contracts/components/schemas/`.

### Integration Boundaries

**Assembly repository → Omnipus (the feed).**
- *Data in:* one GitHub Release per day on the assembly repo **`elicify-ai/omnipus-provider-catalog`** (owner/repo pinned in Go `[A-5]`; puller order unchanged: release API → raw fallback), assets `providers_catalog.json` and `providers_catalog.json.sha256` (format: `<64 hex>` or `<64 hex>  providers_catalog.json`); **the sidecar is mandatory** and on the release path is located from the release's asset list (F-02). Also reachable at the raw URL of `main` — `Ref` pinned in code, no override (F-28); on that path asset and sidecar come from the same branch, so the checksum proves transport integrity only (accepted risk, ADR §2).
- *Document shape (2.0.0):* top level `schema_version` (`"2.0.0"`), `version` (monotonic, **`vYYYY.M.D[.N]`** with the leading `v` so `Version.Compare` orders numerically `[A-6]`, F-01), `updated_at` (RFC 3339), `source` (free text with upstream commit ids), `default_resize_limits {long_edge_px, max_bytes}` `[A-10]`, `providers[]`. Provider: `id` (models.dev id or local-file id), `name` `[A-14]`, `api` (base URL; empty only when `unsupported`), `protocol` (primary, one of `openai-compatible|anthropic|google|ollama|cli`), `protocols[]` (optional; when present MUST include the primary with the same `api`; entries unique; `protocol` MAY be empty only when `tier: unsupported` — F-19) `[A-8]`, `env` (opaque; picker hint text only — never consumed by the factory, F-20), `region` (optional), `plan` (optional), `tier` (`popular|standard|unsupported`) `[A-9]`, `unsupported_reason` (`cloud-iam` | `deployment-url` | `withdrawn`; required when tier unsupported), `auth_methods[]` (`api_key|sign_in`, ≥1; ADR-068 picker) `[A-9]`, `aliases[]` (**search-only** strings for the picker's filter — never consulted by resolution, the factory, or config validation) `[A-9]`, `company` (grouping key; from `overrides/`, default = `name`; X-10), `locality` (`local|cloud`, **derived** on load — X-16), `cli_kind` (`codex|copilot`; required iff protocol `cli`; X-14), `token_source` (optional; `codex-auth-json` for `openai-chatgpt`; X-41), `resize_limits {long_edge_px, max_bytes}`, `models[]`. Model: `id`, `name` `[A-14]`, `release_date` (`YYYY-MM-DD`, optional) `[A-9]`, `context_window` (int, 0 = unknown), `max_output_tokens` (int, 0 = unknown), `input_modalities[]` (must include `text`), `tool_call` (bool), `status` (`active|retired`) `[A-3]`, `disputed` (bool, optional) `[A-22]`. **Source per field:** everything from models.dev (LiteLLM adjudicating, `overrides/` winning) except `tier`, `unsupported_reason`, `auth_methods`, `aliases`, `company`, `cli_kind`, `token_source`, which come only from `overrides/` or the local-providers file; `resize_limits` from `resize_limits.json`; `locality` is derived by the consumer, never published.
- *Inputs to the job (for the record, not enforced by Omnipus):* models.dev `api.json`, LiteLLM `model_prices_and_context_window.json`, `overrides/` (wins over both), `resize_limits.json` (per provider, joined onto every model), a local-provider file for ids absent from models.dev, a manifest of upstream commits.
- *Failure behaviour:* unreachable / 404 / rate-limited → raw fallback → otherwise retain current, WARN; checksum mismatch → reject, WARN; wrong schema → ignore, WARN; oversize → reject, WARN. Omnipus never blocks on the feed.
- *Factory case disposition (F-16; every `case` in today's `factory_provider.go::CreateProviderFromConfig` / `knownProtocols`):*

| Today's id(s) | Disposition |
|---|---|
| `openai`, `anthropic`, `openrouter`, `groq`, `mistral`, `deepseek`, `cerebras`, `nvidia`, `vivgrid`, `longcat`, `modelscope`, `minimax`, `minimax-cn`, `google`, `ollama`, `vllm`, `litellm` | registry / local-file id, unchanged |
| `gemini` → `google`; `anthropic-messages` → `anthropic`; `novita` → `novita-ai`; `zhipu` → `zhipuai`; `z-ai`/`z.ai`/`zai` → `zai`; `z-ai-coding`/`glm-coding` → `zai-coding-plan`; `zhipu-coding` → `zhipuai-coding-plan`; `moonshot`(-`cn`) → `moonshotai`(-`cn`); `qwen` → `alibaba-cn`; `qwen-intl`/`qwen-international`/`dashscope-intl` → `alibaba`; `qwen-us`/`dashscope-us` → `alibaba` (region us); `coding-plan`/`alibaba-coding`/`qwen-coding` → `alibaba-coding-plan` | canonical id (ADR §3.3 — documentation only; old ids are unknown) |
| every `*-anthropic` id | `(canonical id, protocol=anthropic)` |
| `shengsuanyun`, `volcengine`, `avian`, `mimo` | local-file id, unchanged |
| `azure`, `azure-openai` | `azure`, `tier: unsupported`, `unsupported_reason: deployment-url` |
| `bedrock` | `amazon-bedrock`, `tier: unsupported`, `unsupported_reason: cloud-iam` |
| `antigravity`, `claude-cli`, `claudecli` | dropped (ADR-068 §2.3/§2.4) |
| `codex-cli`, `codexcli` | `codex-cli` (protocol `cli`, `cli_kind: codex`) and `openai-chatgpt` (protocol `openai-compatible`, `token_source: codex-auth-json`, cloud) per ADR-068 MAJ-013 / X-14 / X-41; new local-file row `github-copilot` (protocol `cli`, `cli_kind: copilot`) |
| (new) custom rows (`custom: true`, operator-named ids such as `my-proxy`) | factory case, not a row (FR-035); tested on the flag, never on a literal id |
| `lmstudio` | catalog id in the local-providers file, `locality: local` (X-16) |

- *Development approach:* simulated twin — `httptest` servers replaying fixture releases (existing `puller_test.go` pattern), plus one conformance fixture `testdata/providers_catalog_2.0.0_fixture.json` shared by Go tests and, by copy, the assembly repo's own tests.

**Omnipus → LLM providers (live calls that remain).**
- `/models` (OpenAI-compatible) only on entitlement check and inside key validation for non-catalog providers; `/api/tags` and `/api/show` for ollama; `/v1/models` `max_model_len` for vLLM (ADR-066 D3 owns the window semantics). Failure → annotated "unknown", never a selector wipe. SSRF-safe client retained (SEC-24).

**Omnipus → SPA.**
- `GET /api/v1/providers/catalog` (new), `POST /api/v1/providers/{id}/entitlement` → `EntitlementResponse.yaml` `[A-13]` (replaces `refresh-models`), `POST /onboarding/probe-provider` (id now free string), `GET /api/v1/providers` (configured rows only — FR-029). Removed: `GET /providers/model-capabilities` `[A-12]`, `src/lib/generated/providerCatalog.ts`.

---

## 6. BDD Scenarios

> Categories: HP=Happy Path, AP=Alternate, EP=Error, EC=Edge. Every scenario `Traces to:` US#.AC#.

**Scenario (HP): load a conforming 2.0.0 document** — Traces to US-1.AC1
Given the conformance fixture
When it is loaded
Then `(zai, glm-5.2)` resolves with `context_window` 1000000, `max_output_tokens` > 0, modalities including `text`, `tool_call` true, `status` active, and `zai`'s resize limits.

**Scenario Outline (EP): reject a malformed document and retain the previous one** — Traces to US-1.AC2, US-1.AC3
Given a loaded catalog at version V
When a document with `<defect>` is loaded
Then the load fails with a message containing `<path>` and the catalog still reports version V.

| defect | path |
|---|---|
| `schema_version: "1.0.0"` | `schema_version` |
| `schema_version: "2.1.0"` | `schema_version` |
| duplicate provider id `zai` | `providers[1].id` |
| duplicate model id within `zai` | `providers[0].models[1].id` |
| empty provider id | `providers[0].id` |
| `protocol: "grpc"` | `providers[0].protocol` |
| model `input_modalities: ["image"]` | `providers[0].models[0].input_modalities` |
| `version: ""` | `version` |
| `default_resize_limits.max_bytes: 0` | `default_resize_limits.max_bytes` |

**Scenario (EC): same model id under two providers** — Traces to US-1.AC4
Given `openrouter` carries `z-ai/glm-5.2` at 1048576 and `zai` carries `glm-5.2` at 1000000
When both are resolved
Then each returns its own window.

**Scenario (EC): unknown limits are reported as unknown** — Traces to US-1.AC5
Given a model with `context_window: 0` and no `max_output_tokens`
When resolved
Then the window and output are *unknown* (zero) and the entry is otherwise usable.

**Scenario (HP): release layout conforms** — Traces to US-2.AC1
Given a stub release host serving the fixture and its sidecar
When the puller pulls
Then the bytes are returned and the sidecar hex equals SHA-256 of the bytes.

**Scenario (AP): disagreement publishes last known good** — Traces to US-2.AC2
Given the assembly job's previous output has `gpt-4o.context_window = 128000` and the registries now disagree (models.dev 128000, LiteLLM 131072)
When the job publishes
Then the document carries 128000 and `disputed: true` `[A-22]`, and an issue is opened.
*(Assembly-side; executed in that repo's tests against the shared fixture. Recorded here as the contract.)*

**Scenario (AP): override supplies a URL for a dedicated-SDK provider** — Traces to US-2.AC3
Given models.dev has `groq` with no `api`
When the job publishes
Then `groq.api` is the override's URL and `groq.tier` is `popular`.

**Scenario (HP): local-file providers appear with the registry shape** — Traces to US-2.AC4
Given the document
When `ollama`, `vllm`, `litellm`, `lmstudio`, `codex-cli`, `openai-chatgpt`, `github-copilot`, `shengsuanyun`, `volcengine`, `avian`, `mimo` are looked up
Then each is present with `protocol` set and validates like any other provider.

**Scenario (HP): hermetic build** — Traces to US-2.AC5
Given a warm module cache
When `GOFLAGS=-mod=mod GOPROXY=off make build` runs
Then it succeeds; `git ls-files pkg/providers/catalog/data/providers_catalog.json` lists the file; the only `go:embed` in `pkg/providers/catalog` names that path; no `//go:generate` in the package invokes a network tool; the binary's embedded catalog equals the committed file byte-for-byte.

**Scenario (HP): offline boot serves the snapshot** — Traces to US-3.AC1
Given a recording stub host that records the wall-clock of each hit
When the gateway boots
Then `GET /providers/catalog` returns the snapshot's `version` with `source: embedded`, and the stub's first hit (if any) is after the listener bound.


**Scenario (HP): startup pull replaces the snapshot** — Traces to US-3.AC2
Given a stub host serving version V+1
When the gateway boots and the background pull completes
Then the catalog reports V+1 with `source: pulled` and the persisted file holds V+1.

**Scenario (HP): 24 h ticker** — Traces to US-3.AC3
Given a refresh loop with a 24 h interval and a fake clock
When 24 h elapse
Then exactly one more pull is attempted; **and** when 1,000 REST requests and 10 turns run in between, no pull is attempted.

**Scenario (EP): checksum mismatch is rejected** — Traces to US-3.AC4
Given a stub host whose sidecar does not match the asset
When refresh runs
Then the result is `ErrChecksumMismatch`, one WARN with `reason=checksum`, and the catalog version is unchanged.

**Scenario (EP): wrong schema version is ignored** — Traces to US-3.AC5
Given a stub host serving a valid 1.0.0 seed with a correct sidecar
When refresh runs
Then one WARN with `reason=schema_version`, version unchanged.

**Scenario (EP): downgrade refused** — Traces to US-3.AC6
Given the catalog at `2026.8.22` and a host serving `2026.8.21`
When refresh runs
Then WARN `reason=regressed`, version unchanged.

**Scenario (EC): invalid new-path persisted file ignored; legacy file invisible** — Traces to US-3.AC7
Given `$OMNIPUS_HOME/providers_catalog.json` holds a `schema_version: 1.0.0` envelope and `capabilities_catalog.json` also exists
When the gateway boots
Then the embedded snapshot serves, exactly one WARN `reason=schema_version` names `providers_catalog.json`, and zero log lines mention `capabilities_catalog.json`.

**Scenario (EC): persisted newer than embedded wins** — Traces to E6
Given the embedded snapshot at `v2026.8.20` and a valid persisted `v2026.8.21`
When the gateway boots
Then `Version()` is `v2026.8.21` and `source` is `pulled`.

**Scenario (EP): missing sidecar is rejected** — Traces to E11, US-2.AC1
Given a stub release whose asset list has `providers_catalog.json` but no `.sha256`
When refresh runs
Then `ErrChecksumMismatch`, one WARN `reason=checksum`, version unchanged — and the same on the raw path.

**Scenario Outline (EP): hosted `api` URL validation** — Traces to E13
Given a document whose provider `<id>` (protocol `<protocol>`) has `api: <url>`
When loaded
Then the outcome is `<outcome>`.

| id | protocol | url | outcome |
|---|---|---|---|
| zai | openai-compatible | `http://api.z.ai/api/paas/v4` | reject `providers[0].api` (scheme) |
| zai | openai-compatible | `https://169.254.169.254/v1` | reject (metadata host) |
| zai | openai-compatible | `https://10.0.0.5/v1` | reject (private) |
| zai | openai-compatible | `https://user:pw@api.z.ai/v1` | reject (userinfo) |
| zai | openai-compatible | `https://api.z.ai/v1?x=1` | reject (query) |
| ollama | ollama | `http://127.0.0.1:11434` | accept |
| lmstudio | openai-compatible | `http://127.0.0.1:1234/v1` | accept (id exception) |
| vllm | openai-compatible | `http://10.0.0.5:8000/v1` | accept (id exception) |

**Scenario (EP): truncated body is too_large, never checksum** — Traces to E14
Given a stub serving `maxCatalogAssetBytes + 1` bytes with a correct sidecar
When refresh runs
Then WARN `reason=too_large`; and at exactly `maxCatalogAssetBytes` bytes, the document is accepted.

**Scenario Outline (HP): version ordering across boundaries** — Traces to US-3.AC6
Given the catalog at `<current>`
When a document at `<pulled>` is pulled
Then it is `<outcome>`.

| current | pulled | outcome |
|---|---|---|
| v2026.8.9 | v2026.8.10 | applied |
| v2026.9.30 | v2026.10.1 | applied |
| v2026.12.31 | v2027.1.1 | applied |
| v2026.8.22 | v2026.8.22.1 | applied |
| v2026.8.10 | v2026.8.9 | regressed |
| v2026.8.22 | 2026.8.23 (no `v`) | rejected `version` |

**Scenario (AP): raw fallback accepted and flagged degraded** — Traces to US-3.AC8
Given the release API returns 403 and the raw URL serves asset + sidecar
When refresh runs
Then the document is applied and `Degraded()` reports true with the release error.

**Scenario (HP): exact route resolution** — Traces to US-4.AC1
Given the fixture
When `(openrouter, z-ai/glm-5.2)` and `(zai, glm-5.2)` are resolved
Then 1048576 and 1000000 respectively.

**Scenario (EP): no prefix stripping** — Traces to US-4.AC2
Given the fixture
When `(openrouter, glm-5.2)` is resolved
Then the result is a miss.

**Scenario (AP): miss semantics per consumer** — Traces to US-4.AC3
Given a miss
When the media path asks `Supports(image)` and `Budget()`
Then true and the default resize limits; **and** when the loop asks `Window()`
Then unknown.

**Scenario (HP): single package** — Traces to US-4.AC4
Given the tree
When `go build -tags goolm,stdjson ./...` runs
Then it succeeds, `pkg/providers/capabilities` does not exist, and exactly one catalog `go:embed` exists under `pkg/providers`.

**Scenario Outline (HP): construct a transport from the table** — Traces to US-5.AC1, US-5.AC2
Given a ModelConfig `{provider: <id>, api_key: k}` and the fixture
When a provider is constructed
Then its type is `<transport>` and its base URL is `<url>`.

| id | transport | url |
|---|---|---|
| zai | HTTPProvider | `https://api.z.ai/api/paas/v4` |
| minimax | ClaudeProvider | `https://api.minimax.io/anthropic` |
| google | HTTPProvider | `https://generativelanguage.googleapis.com/v1beta/openai` |
| openrouter | HTTPProvider | `https://openrouter.ai/api/v1` |

**Scenario (AP): protocol choice on a dual-protocol provider** — Traces to US-5.AC3
Given `zai` offers openai-compatible (primary) and anthropic
When a ModelConfig with `protocol: anthropic` is constructed
Then a ClaudeProvider with `zai`'s anthropic endpoint; and with no `protocol`, an HTTPProvider.

**Scenario (AP): custom endpoint is a factory case, not a row** — Traces to US-5.AC4
Given `{provider: my-proxy, api_base: https://llm.example/v1, protocol: openai-compatible, api_key: k}` and `{provider: my-proxy-2, api_base: https://llm2.example, protocol: anthropic, api_key: k2}`
When both are constructed
Then an HTTPProvider at the first base and a ClaudeProvider at the second; both rows coexist.

**Scenario (EP): unknown id with only an api_base** — Traces to US-5.AC6
Given `{provider: nope, api_base: https://x/v1}` (no `protocol`)
When constructed
Then `ErrUnknownProvider`.

**Scenario (HP): catalog key is (Provider, bare Model)** — Traces to US-5.AC8
Given `{provider: openrouter, model: z-ai/glm-5.2}`
When the loop asks the catalog
Then the key is `(openrouter, z-ai/glm-5.2)` → 1048576; no `ExtractProtocol` symbol exists in `pkg/providers`.

**Scenario (HP): factory has no vendor cases** — Traces to US-5.AC5
Given `pkg/providers/factory_provider.go`
When the `case` labels of the dispatch switch are listed
Then the set is exactly `{openai-compatible, anthropic, google, ollama, cli}`.

**Scenario Outline (EP): non-canonical ids fail as unknown with no hint** — Traces to US-5.AC6
Given `{provider: <old>}`
When constructed
Then an error of type `ErrUnknownProvider` whose text contains `<old>` and does not contain `<canonical>`.

| old | canonical |
|---|---|
| z-ai | zai |
| moonshot-cn-anthropic | moonshotai-cn |
| qwen-intl | alibaba |
| coding-plan | alibaba-coding-plan |
| gemini | google |

**Scenario (HP): seeds are canonical** — Traces to US-5.AC7
Given `pkg/config/defaults.go` and `config/config.example.json`
When every `provider` value is checked against the embedded snapshot
Then all are present.

**Scenario (HP): boot survives an unknown provider** — Traces to US-6.AC1
Given config providers `openai`, `nope`; agents A→openai, B→nope
When the gateway boots
Then listen succeeds, A completes a turn against a stub, and `GET /providers` shows `nope` with `status: unknown-provider` `[A-16]`.

**Scenario (EP): bound agent refuses to run, typed** — Traces to US-6.AC2
Given the same
When B receives a message
Then the turn ends with error kind `needs_provider`, zero upstream requests, and `GET /agents` shows B `degraded: needs provider`.

**Scenario (AP): repair without restart** — Traces to US-6.AC3
Given B degraded
When `PUT /agents/B` sets provider `openai`
Then B's next turn succeeds after the existing reload.

**Scenario (EP): no hint anywhere** — Traces to US-6.AC4
Given provider `z-ai` configured
When boot log, `GET /providers`, and `GET /agents` are captured
Then none contains the string `zai` as a suggestion (assert absence of `did you mean` and of the canonical id in the `nope`/`z-ai` rows).

**Scenario (AP): unknown fallback is dropped, agent runs** — Traces to US-6.AC5
Given agent C primary `openai`, fallbacks `[nope]`
When constructed and given a message
Then one WARN names `C` and `nope`, C's pool has `openai` only, the turn completes, and `GET /agents/C` has no `degraded_reason`.

**Scenario (EP): case-different entity id is unknown** — Traces to US-6.AC6
Given agent D entity `provider: "ZAI"`, config `zai`
When constructed
Then D is `degraded_reason: needs_provider`.

**Scenario (HP): repair without restart** — Traces to US-6.AC3
Given B degraded
When `PUT /agents/B {provider: openai}` then a message
Then the turn succeeds and `GET /agents/B` has no `degraded_reason`.

**Scenario (HP): catalog endpoint** — Traces to US-7.AC1, US-7.AC5
Given an authenticated client
When `GET /api/v1/providers/catalog`
Then 200, body validates against the generated zod schema, `providers[].models[]` present, `version`/`updated_at`/`source` present.

**Scenario (EP): unauthenticated** — Traces to US-7.AC2
When `GET /api/v1/providers/catalog` without a bearer
Then 401.

**Scenario (AP): ETag round trip** — Traces to US-7.AC3
Given a prior 200 with `ETag: E`
When `GET` with `If-None-Match: E`
Then 304, empty body, `Cache-Control: private, max-age=0, must-revalidate`; with `If-None-Match: W/E` or an unquoted value → 200; after a refresh to a new version, 200 with a different ETag; the body bytes hash to the ETag.

**Scenario (EC): stale flag** — Traces to US-7.AC1
Given the served document's `updated_at` is 15 days old
When `GET /providers/catalog`
Then `stale: true`, and `/health` reports the catalog degraded with the last refresh error.

**Scenario (HP): providers list is configurations only** — Traces to US-7.AC6
Given config with providers `openai` and `zai` and an embedded snapshot of 190+ providers
When `GET /api/v1/providers`
Then the response has exactly 2 rows (`openai`, `zai`) and no row with a synthetic `disconnected` status for an unconfigured catalog provider.

**Scenario (EP): catalog unavailable** — Traces to US-7.AC4
Given the catalog failed construction
When `GET /api/v1/providers/catalog`
Then 503 `{"error":"provider catalog unavailable"}`.

**Scenario (HP): tiers are in the data** — Traces to US-8.AC1
Given the snapshot
When providers are filtered `tier: popular`
Then the set is exactly `{openai, openrouter, anthropic, google, xai, groq, mistral, deepseek}`.

**Scenario (EP): cloud-IAM is visible but not configurable** — Traces to US-8.AC2
Given `amazon-bedrock`
When listed → `tier: unsupported`, `unsupported_reason: cloud-iam`; when `PUT /providers/amazon-bedrock` → 400 containing `cloud-iam`.

**Scenario (HP): standard-tier provider accepted** — Traces to US-8.AC3
Given `togetherai` (standard)
When `PUT /providers/togetherai` with a key
Then 200 and a subsequent construction succeeds with the catalog URL.

**Scenario (EP/HP): custom requires a base** — Traces to US-8.AC4
When `PUT /providers/my-proxy` without `api_base` → 400; with `api_base` and `protocol: anthropic` → 200 and the row carries `custom: true`.

**Scenario (HP): offline model list** — Traces to US-9.AC1
Given no network and `anthropic` configured
When `GET /providers/anthropic`
Then `models[]` equals the catalog's `anthropic` models with limits, and the outbound request counter is 0.

**Scenario Outline (AP): entitlement per protocol** — Traces to US-9.AC2
Given provider `<id>` (protocol `<protocol>`) with a key and a recording stub
When `POST /providers/<id>/entitlement`
Then the stub receives `<call>` once and the response is `<outcome>`.

| id | protocol | call | outcome |
|---|---|---|---|
| openai | openai-compatible | `GET /models` Bearer | 200 annotated list |
| google | google | `GET /models` Bearer | 200 annotated list |
| anthropic | anthropic | `GET /v1/models` with `x-api-key` + `anthropic-version` | 200 annotated list |
| ollama | ollama | `GET /api/tags` | 200 annotated list |
| codex-cli | cli | (none) | 409 |
| my-proxy (custom) | openai-compatible | (none) | 409 |

**Scenario (AP): entitlement intersects and caches** — Traces to US-9.AC2
Given a stub returning `[claude-x, brand-new-model]` and a catalog with `[claude-x, claude-y]`
When the entitlement check runs twice with the same key
Then `claude-x: entitled, limits known`, `claude-y: not entitled`, `brand-new-model: limits "unknown"`; the first response has `cached: false`, the second `cached: true` with the same `checked_at`; stub counter stays 1; after a catalog refresh the counter becomes 2.

**Scenario (EP): entitlement without a key** — Traces to US-9.AC2
Given `anthropic` configured with an unresolvable `api_key_ref`
When `POST /providers/anthropic/entitlement`
Then 422.

**Scenario (AP): local endpoints are live** — Traces to US-9.AC3
Given `ollama` with a stub `/api/tags`
When models are requested
Then the stub's list is returned and the catalog's `ollama` entry contributes no model ids.

**Scenario (HP): probe model from the catalog** — Traces to US-9.AC4
Given `zai` and a recording stub
When `/providers/zai/test` runs
Then exactly one POST (the completion probe) and zero GET `/models`, and the probe model is the catalog's first active tool-calling text model for `zai`.

**Scenario (AP): probe falls through on model_not_found** — Traces to US-9.AC4
Given the stub answers 404 `model_not_found` for the first two candidates and 200 for the third
When `/providers/zai/test` runs
Then exactly 3 POSTs and outcome Valid; with four 404s, outcome Unreachable after 3 POSTs.

**Scenario (EC): retired model still constructs** — Traces to E8
Given agent E on `(zai, glm-4)` with `status: retired`
When constructed and given a message
Then the provider constructs, the turn is sent, and no `needs_provider` is set.

**Scenario (HP): probe a registry id** — Traces to US-10.AC1
When `POST /onboarding/probe-provider {id: zai, auth: api_key, api_key: k}` against a stub → the stub at `zai`'s URL receives the probe.

**Scenario (EP): probe an old id** — Traces to US-10.AC2
When `{id: z-ai, auth: api_key, api_key: k}` → 400 `unknown provider "z-ai"`, and the body does not contain `zai`.

**Scenario (AP/EP): probe custom** — Traces to US-10.AC3
When `{id: my-proxy, auth: api_key, api_key: k, api_base: …, protocol: openai-compatible}` → probe runs there; without `api_base` or `protocol` → 400; `auth: api_key` without `api_key` → 400; a 65-character `id` → 400 by schema.

**Scenario (EP): probe cloud-IAM** — Traces to US-10.AC4
When `{id: amazon-bedrock, auth: api_key, api_key: k}` → 400 containing `cloud-iam`.

**Scenario (HP): greenfield grep** — Traces to US-11.AC1
Given the tree
When `grep -rnE '_migrated|alias|deprecat|retired' pkg/providers pkg/config` runs
Then the only matches are the `status` enum value `retired` in the catalog package and its tests `[A-3]`.

**Scenario (HP): SPA artefacts gone** — Traces to US-11.AC2
When `ls src/lib/providerMigration.ts src/lib/generated/providerCatalog.ts` → both absent; `npm run typecheck` exit 0.

**Scenario (EP): oversize document** — Traces to E1
Given a stub serving 17 MB
When refresh runs → WARN `reason=too_large`, version unchanged.

**Scenario (EC): provider with zero models** — Traces to E2
Given `zai-coding-plan` with `models: []`
When loaded → accepted; `GET /providers/catalog` lists it with an empty `models`.

**Scenario (EC): whitespace/case in config** — Traces to E4
Given `provider: " ZAI "` → unknown provider (after trim, `ZAI` ≠ `zai`).

**Scenario (EC): concurrent refresh** — Traces to E5
Given a startup pull blocked on a slow stub
When the ticker fires → the second pull waits; after both, version is the latest served and the race detector is clean.

**Scenario (EC): bad embedded snapshot** — Traces to E7
Given the embedded bytes replaced by an invalid document (test seam)
When the gateway boots → listen succeeds, ERROR logged once, `GET /providers/catalog` → 503, media path optimistic.

**Scenario (EC): unicode names** — Traces to E9
Given a provider `name: "智谱 AI"` → `GET` returns it unchanged.

**Scenario (EP): custom with disallowed protocol** — Traces to E12
When `PUT /providers/my-proxy {api_base, protocol: ollama}` → 400.

---

## 7. TDD Plan

| Order | Test | Level | Traces to BDD | Description |
|---|---|---|---|---|
| 1 | `TestParseDocument_Conforming` | Unit | load conforming | fixture → `Document`; spot-check `(zai, glm-5.2)` |
| 2 | `TestParseDocument_Rejects` (table) | Unit | reject malformed outline (9 rows) | each defect → error containing path; previous retained via `Catalog` |
| 3 | `TestResolve_SameModelIDTwoProviders` | Unit | same model id / exact route | key is the pair |
| 4 | `TestResolve_UnknownLimitsAreZero` | Unit | unknown limits | `Window()==0`, usable |
| 5 | `TestResolve_NoPrefixStripping` | Unit | no prefix stripping | `(openrouter, glm-5.2)` miss |
| 6 | `TestResolve_MissSemantics` | Unit | miss per consumer | optimistic modality + default resize; window unknown |
| 7 | `TestVersion_DateSemver` | Unit | version ordering outline (6 rows) | `v2026.8.9<v2026.8.10`, `v2026.9.30<v2026.10.1`, `v2026.12.31<v2027.1.1`, `v2026.8.22<v2026.8.22.1`; no-`v` rejected `[A-6]` (DS-7) |
| 6b | `TestBoot_PersistedNewerThanEmbedded` | Unit | persisted newer wins | E6 |
| 8 | `TestGHReleasePuller_Pull_RetargetedAsset` | Unit | release layout | owner/repo/asset = assembly repo; sidecar verify |
| 9 | `TestRefresh_ChecksumMismatch_Retains` | Unit | checksum mismatch | one WARN `reason=checksum` |
| 9b | `TestGHReleasePuller_Pull_NoSidecar_Rejected` | Unit | missing sidecar rejected | rewrites today's `Pull_NoSidecar`; release path reads the asset list; raw path too |
| 9c | `TestParseDocument_APIURLValidation` (table) | Unit | URL validation outline (8 rows) | scheme/host/userinfo/query; ollama/lmstudio/vllm exceptions (DS-1.18–25) |
| 10 | `TestRefresh_WrongSchemaVersion_Ignored` | Unit | wrong schema | WARN `reason=schema_version` |
| 11 | `TestRefresh_Downgrade_Refused` | Unit | downgrade refused | WARN `reason=regressed` |
| 12 | `TestRefresh_TooLarge_Rejected` | Unit | oversize; truncated body | `maxCatalogAssetBytes`+1 → `too_large`, never `checksum`; exactly cap → accepted `[A-18]` |
| 13 | `TestRefresh_RawFallback_Degraded` | Unit | raw fallback | existing behaviour retained |
| 14 | `TestRefresh_Concurrent_Serialized` | Unit (-race) | concurrent refresh | mutex serialization |
| 15 | `TestStore_InvalidPersisted_Ignored_LegacyInvisible` | Unit | invalid new-path persisted | one WARN for `providers_catalog.json`; zero lines for `capabilities_catalog.json` `[A-4]` |
| 16 | `TestEmbeddedSnapshot_Valid_And_Bounded` | Unit | hermetic build; bad snapshot; E7 | snapshot parses, ≤ 8 MB `[A-2]`, tier set exact |
| 17 | `TestEmbeddedSnapshot_PopularTier` | Unit | tiers in data | popular set exact |
| 18 | `TestEmbeddedSnapshot_LocalProvidersPresent` | Unit | local-file providers | 11 ids present; no row with `custom: true`; `cli_kind` set on every `cli` row |
| 19 | `TestEmbeddedSnapshot_UnsupportedHaveReason` | Unit | cloud-IAM listed | every `tier: unsupported` row has a reason; `amazon-bedrock`=cloud-iam, `azure`=deployment-url |
| 20 | `TestCreateProviderFromConfig_ProtocolDispatch` (table) | Unit | construct from table outline | transport type + URL per row |
| 21 | `TestCreateProviderFromConfig_ProtocolChoice` | Unit | dual-protocol | `protocol: anthropic` → ClaudeProvider `[A-8]` |
| 20b | `TestCreateProviderFromConfig_RetiredModelConstructs` | Unit | retired model constructs | E8 |
| 22 | `TestCreateProviderFromConfig_Custom` | Unit | custom case; unknown+base only; E12 | two custom rows coexist; `nope`+base → unknown; disallowed protocol → error |
| 22b | `TestCatalogKey_ProviderAndBareModel` | Unit | catalog key | `(openrouter, z-ai/glm-5.2)`; `ExtractProtocol` absent (source scan) |
| 23 | `TestCreateProviderFromConfig_UnknownProvider_NoHint` (table) | Unit | non-canonical outline | `errors.Is(err, ErrUnknownProvider)`; text lacks canonical |
| 24 | `TestFactory_NoVendorCases` | Unit (source scan) | no vendor cases | parse `factory_provider.go` AST; protocol case set exact; inner `cli` switch has only `cli_kind` literals; no `"custom"` string literal |
| 24b | `TestCatalog_LocalityPredicate` (table) | Unit | (FR-039) | ollama/vllm/lmstudio/custom-loopback → local; zai/openai-chatgpt/custom-public → cloud |
| 24c | `TestModelListResolver_PairExactThenUnique` | Unit (agent) | (FR-040) | pair match; unique bare id; ambiguous → unresolved; no ModelName path |
| 25 | `TestSeeds_CanonicalProviderIDs` | Unit | seeds canonical | defaults.go + config.example.json vs snapshot |
| 26 | `TestPickProbeModel_FromCatalog` | Unit | probe model from catalog | first active tool-calling text model `[A-20]` |
| 27 | `TestDisplayName_FromCatalog` | Unit | (US-7.AC1 name field) | `name` from catalog; unknown → id `[A-14]` |
| 28 | `TestConfig_ProviderID_TrimNotFold` | Unit | whitespace/case; case-different entity | `" ZAI "` unknown; `findModelConfigForProvider("ZAI")` misses `zai` `[A-19]` |
| 29 | `TestGreenfield_NoAliasMachinery` | Unit (source scan) | greenfield grep | regex over `pkg/providers`, `pkg/config`; allow `retired` token `[A-3]` |
| 30 | `TestContract_ProvidersCatalog_Shape` | Unit | catalog endpoint validates | Go struct → JSON → schema validation (pattern: `pkg/api/generated/contract_test.go`) |
| 31 | `TestContract_ProbeProviderRequest_NoEnum` | Unit | probe old id; 65-char id | generated type is `string`; YAML has no `enum`, `maxLength: 64` |
| 31b | `TestContract_SharedSchemas_FullEnums` | Unit | (schema) | `Agent.degraded_reason` enum, `Agent.needs_model`, optional window fields; `Provider.status` exactly `connected,disconnected,error,unknown-provider,signed_in,expired`; `Provider.protocol`/`custom`/`auth_method`/`account_label`/`dependents`/`backs_default`/`updated_at`; `ProviderUpdateRequest.protocol`/`auth_method`; `ProbeProviderRequest` = FR-023 shape; `DefaultModel`/`DefaultModelUpdateRequest`; `EntitlementResponse`; `LLMError` enum includes `needs_provider` **and** `model_unassigned` in all four copies; `inboundschemas/` twins identical |
| 32 | `TestBuildProviderPool_UnknownProvider_Skips` | Unit | boot survives | existing WARN+skip retained; pool lacks `nope` |
| 33 | `TestAgentTurn_NeedsProvider_TypedRefusal` | Unit (agent) | bound agent refuses | `LLMError.code == needs_provider`, attribution `config`, 0 upstream; gate runs before `model_unassigned` `[A-16]` |
| 33b | `TestBuildProviderPool_UnknownFallback_DroppedWithWarn` | Unit (agent) | unknown fallback dropped | pool = primary only; WARN names agent+provider; no degraded_reason (DS-8) |
| 33c | `TestAgentRepair_PUTProvider_NoRestart` | Integration | repair without restart | PUT → reload → turn ok |
| 34 | `TestRestProvidersCatalog_GET` | Integration (scoped gateway test, `-run`, `-p 1`) | catalog endpoint; 401; ETag; 503 | all four statuses |
| 34b | `TestRestProviders_GET_ConfiguredOnly` | Integration | providers list is configurations only | 2 configured → 2 rows with `models[]` from the catalog; 0 template rows |
| 34c | `TestRestProvidersCatalog_ETagAtomicAndStale` | Integration | ETag details; stale flag | quoted strong ETag; `W/`/unquoted → 200; bytes+ETag swapped as one pair under a concurrent apply (-race); `stale: true` at 15 days; `/health` degraded |
| 35 | `TestRestProviders_PUT_Unknown_CloudIAM_Custom` | Integration | cloud-IAM 400; custom 400/200; standard 200 | error vocabulary exact |
| 36 | `TestRestProviders_OfflineModelList` | Integration | offline model list | outbound counter 0 |
| 37 | `TestRestProviders_Entitlement_PerProtocol` | Integration | entitlement outline (6 rows); intersects; 422; 502 | per-protocol call; 409 for cli/custom; 502 on upstream 429; cache key `SHA-256(id:refName)`, evicted on DELETE, key-changing PUT, and refresh; not evicted on an `updated_at`-only PUT `[A-13]` |
| 38 | `TestRestProviders_Ollama_Live` | Integration | local endpoints | stub `/api/tags` |
| 39 | `TestRestProviders_Test_ProbeFromCatalog` | Integration | probe from catalog; fall-through | 1 POST, 0 GET `/models`; 3-attempt bound on `model_not_found` |
| 40 | `TestOnboarding_Probe_FreeStringID` | Integration | probe zai / z-ai / custom / bedrock | 4 outcomes |
| 41 | `TestGatewayBoot_UnknownProvider_NonFatal` | Integration | boot survives; no hint | listen ok; A runs; rows/logs lack hint |
| 42 | `TestGatewayBoot_OfflineSnapshot_Then_StartupPull` | Integration | offline boot; startup pull | `source` flips embedded→pulled; stub's first hit is after listen; startup pull skipped when persisted < 1 h old |
| 43 | `TestRefreshLoop_24h_NoRequestPathPulls` | Integration (fake clock) | 24 h ticker | exactly 1 extra pull; 0 during traffic |
| 44 | `TestEmbeddedSnapshot_Corrupt_BootDegrades` | Integration | bad embedded snapshot | 503 + ERROR once (test seam injects bytes) |
| 45 | SPA `providersCatalog.test.ts` | Unit (vitest) | catalog endpoint validates | zod parse of fixture; `ETag` cache rule `[A-1]` |
| 46 | SPA `onboarding probe id` test | Unit (vitest) | probe old id | free-string id submitted; 400 rendered |
| 47 | Contracts gate | E2E (CI) | all contract scenarios | `make verify-contracts`; `npm run typecheck` |
| 48 | Hermetic build gate | E2E (CI) | hermetic build | `GOFLAGS=-mod=mod GOPROXY=off make build` after warm cache; snapshot git-tracked; single `go:embed`; no network `go:generate` |
| 49 | Snapshot-age gate | E2E (CI, release tags) | (F-31) | fails when embedded `updated_at` > 14 days |

**Order:** 1–31 unit (catalog → version → puller/refresh → embed → factory → probe → greenfield → contract), 32–33 agent unit, 34–44 integration (one scoped gateway test at a time, never the suite), 45–46 vitest, 47–48 CI.

### Test Datasets

**DS-1 document validation** (Traces to US-1 scenarios)
| # | schema_version | version | providers | defect | expect | Traces |
|---|---|---|---|---|---|---|
| 1 | 2.0.0 | v2026.8.22 | fixture (3 providers, 6 models) | none | accept | US-1.AC1 HP |
| 2 | 1.0.0 | 2026.8.22 | fixture | schema | reject `schema_version` | US-1.AC2 |
| 3 | 2.1.0 | 2026.8.22 | fixture | schema | reject `schema_version` | US-1.AC2 |
| 4 | 2.0.0 | "" | fixture | version | reject `version` | US-1.AC3 |
| 5 | 2.0.0 | 2026.8.22 | dup provider `zai` | dup | reject `providers[1].id` | US-1.AC3 |
| 6 | 2.0.0 | 2026.8.22 | dup model in `zai` | dup | reject `providers[0].models[1].id` | US-1.AC3 |
| 7 | 2.0.0 | 2026.8.22 | `protocol: grpc` | enum | reject | US-1.AC3 |
| 8 | 2.0.0 | 2026.8.22 | modalities `[image]` | invariant | reject | US-1.AC3 |
| 9 | 2.0.0 | 2026.8.22 | `context_window: 0` | none | accept, unknown | US-1.AC5 EC |
| 10 | 2.0.0 | 2026.8.22 | provider with `models: []` | none | accept | E2 |
| 11 | 2.0.0 | 2026.8.22 | 0 providers | empty | reject `providers` | US-1.AC1 (min-1) |
| 12 | 2.0.0 | 2026.8.22 | name `"智谱 AI"` | unicode | accept, preserved | E9 |
| 13 | 2.0.0 | 2026.8.22 | 16 MB + 1 byte | size | reject `too_large` | E1 |
| 14 | 2.0.0 | 2026.8.22 | 16 MB exactly | size | accept | E1 (max) |
| 15 | 2.0.0 | 2026.8.22 | `default_resize_limits.max_bytes: 0` | invariant | reject | US-1.AC3 |
| 16 | 2.0.0 | 2026.8.22 | `auth_methods: []` | invariant | reject `providers[0].auth_methods` | US-1.AC3 |
| 17 | 2.0.0 | 2026.8.22 | `aliases: ["z-ai"]` on `zai`, config `provider: z-ai` | none | accept doc; `z-ai` still unknown | FR-030 |
| 18 | 2.0.0 | v2026.8.22 | `zai.api: http://…` | url | reject `providers[0].api` | E13 |
| 19 | 2.0.0 | v2026.8.22 | `zai.api: https://169.254.169.254/v1` | url | reject | E13 |
| 20 | 2.0.0 | v2026.8.22 | `zai.api: https://10.0.0.5/v1` | url | reject | E13 |
| 21 | 2.0.0 | v2026.8.22 | `zai.api: https://u:p@api.z.ai` | url | reject | E13 |
| 22 | 2.0.0 | v2026.8.22 | `lmstudio.api: http://127.0.0.1:1234/v1` | none | accept | E13 |
| 23 | 2.0.0 | v2026.8.22 | `protocols[]` lacks the primary | invariant | reject `providers[0].protocols` | F-19 |
| 24 | 2.0.0 | v2026.8.22 | `protocols[]` omitted | none | accept | F-19 |
| 25 | 2.0.0 | v2026.8.22 | `amazon-bedrock.protocol: ""`, tier unsupported | none | accept | F-19 |
| 26 | 2.0.0 | `2026.8.22` (no `v`) | fixture | version | reject `version` | F-01 |

**DS-2 resolution** (Traces to US-4)
| # | provider | model | expect window | Traces |
|---|---|---|---|---|
| 1 | openrouter | z-ai/glm-5.2 | 1048576 | US-4.AC1 |
| 2 | zai | glm-5.2 | 1000000 | US-4.AC1 |
| 3 | openrouter | glm-5.2 | miss | US-4.AC2 |
| 4 | zai | z-ai/glm-5.2 | miss | US-4.AC2 |
| 5 | "" | glm-5.2 | miss | US-4.AC2 (empty) |
| 6 | zai | "" | miss | US-4.AC2 (empty) |
| 7 | ZAI | glm-5.2 | miss | E4 |
| 8 | zai | glm-5.2 (status retired) | 1000000, status retired | E8 |
| 9 | openrouter | z-ai/glm-5.2 (from `ModelConfig.Model` verbatim) | 1048576 — no split | US-5.AC8 |
| 10 | zai | zai/glm-5.2 (stale prefix typed by operator) | miss — `Model` is bare | US-5.AC8 |

**DS-3 factory dispatch** (Traces to US-5)
| # | provider | protocol (cfg) | api_base | expect | Traces |
|---|---|---|---|---|---|
| 1 | zai | — | — | HTTP @ catalog url | US-5.AC1 |
| 2 | minimax | — | — | Claude @ catalog url | US-5.AC2 |
| 3 | zai | anthropic | — | Claude @ zai anthropic url | US-5.AC3 |
| 4 | zai | google | — | error: protocol not offered | US-5.AC3 (EP) |
| 5 | custom | — | https://x/v1 | HTTP @ x | US-5.AC4 |
| 6 | custom | — | — | error: api_base required | US-8.AC4 |
| 7 | custom | ollama | https://x | error | E12 |
| 8 | z-ai | — | — | ErrUnknownProvider, no "zai" | US-5.AC6 |
| 9 | amazon-bedrock | — | — | error: unsupported cloud-iam | US-8.AC2 |
| 10 | google | — | — | HTTP @ gemini openai endpoint | US-5.AC1 |
| 11 | zai | — | https://proxy/v1 | HTTP @ proxy (explicit base wins) | US-5.AC1 (AP) |
| 12 | (empty) | — | — | error: provider required | US-5.AC6 (empty) |
| 13 | my-proxy | openai-compatible | https://llm.example/v1 | HTTP @ base (custom row) | US-5.AC4 |
| 14 | my-proxy-2 | anthropic | https://llm2.example | Claude @ base (coexists with 13) | US-5.AC4 |
| 15 | nope | — | https://x/v1 | ErrUnknownProvider (no protocol) | US-5.AC6 |

**DS-4 refresh outcomes** (Traces to US-3)
| # | release API | raw | sidecar | doc | expect | Traces |
|---|---|---|---|---|---|---|
| 1 | 200 | — | match | 2.0.0 V+1 | applied, persisted | US-3.AC2 |
| 2 | 200 | — | mismatch | 2.0.0 | WARN checksum, retained | US-3.AC4 |
| 3 | 200 | — | absent | 2.0.0 | rejected, retained | E11 |
| 4 | 403 | 200 | match | 2.0.0 V+1 | applied, degraded=true | US-3.AC8 |
| 5 | 403 | 404 | — | — | retained, WARN | US-3.AC1 |
| 6 | 200 | — | match | 1.0.0 | WARN schema_version | US-3.AC5 |
| 7 | 200 | — | match | 2.0.0 V-1 | WARN regressed | US-3.AC6 |
| 8 | 200 | — | match | 2.0.0 V (equal) | applied (no-op allowed) | US-3.AC2 (boundary) |
| 9 | timeout (>30 s) | timeout | — | — | retained, WARN | US-3.AC2 (timeout) |

**DS-5 REST** (Traces to US-7/8/9/10)
| # | call | auth | expect | Traces |
|---|---|---|---|---|
| 1 | GET /providers/catalog | yes | 200 doc | US-7.AC1 |
| 2 | GET /providers/catalog | no | 401 | US-7.AC2 |
| 3 | GET /providers/catalog If-None-Match=E | yes | 304 | US-7.AC3 |
| 4 | GET /providers/catalog (catalog nil) | yes | 503 | US-7.AC4 |
| 4b | GET /providers (2 configured) | yes | 200, exactly 2 rows | US-7.AC6 |
| 4c | GET /providers (0 configured) | yes | 200, `[]` | US-7.AC6 (empty) |
| 5 | PUT /providers/amazon-bedrock | yes | 400 cloud-iam | US-8.AC2 |
| 6 | PUT /providers/togetherai + key | yes | 200 | US-8.AC3 |
| 7 | PUT /providers/my-proxy no base | yes | 400 | US-8.AC4 |
| 8 | PUT /providers/nope | yes | 400 unknown | US-6.AC4 |
| 9 | GET /providers/anthropic (offline) | yes | 200 catalog models, 0 outbound | US-9.AC1 |
| 10 | POST /providers/{openai,google,anthropic,ollama}/entitlement | yes | 200 annotated via the protocol's call; 2nd call cached; refresh evicts | US-9.AC2 |
| 11 | POST /providers/anthropic/entitlement (no key) | yes | 422 | US-9.AC2 (EP) |
| 11b | POST /providers/{codex-cli,my-proxy}/entitlement | yes | 409 | US-9.AC2 (EP) |
| 11c | POST /providers/openai/entitlement (stub 429) | yes | 502 `could not fetch upstream model list: status 429`; nothing cached | US-9.AC2 (EP) |
| 12 | POST /onboarding/probe-provider {id: zai, auth: api_key, api_key} | n/a | probe runs | US-10.AC1 |
| 13 | … {id: z-ai, auth: api_key, api_key} | n/a | 400 no hint | US-10.AC2 |
| 14 | … {id: my-proxy, auth: api_key, api_key, api_base, protocol} / same without protocol | n/a | runs / 400 | US-10.AC3 |
| 14b | … {id: zai, auth: api_key} (no api_key) | n/a | 400 | US-10.AC3 |
| 15 | … {id: amazon-bedrock, auth: api_key, api_key} | n/a | 400 cloud-iam | US-10.AC4 |
| 16 | … {id: ""} | n/a | 400 | US-10.AC2 (empty) |
| 17 | … {id: 65-char string} | n/a | 400 by schema (`maxLength: 64`) | US-10.AC3 |
| 18 | GET /agents/B (B bound to `nope`) | yes | 200, `degraded_reason: needs_provider` | US-6.AC2 |

**DS-7 version ordering** (Traces to US-3.AC6)
| # | current | pulled | expect |
|---|---|---|---|
| 1 | v2026.8.9 | v2026.8.10 | applied |
| 2 | v2026.9.30 | v2026.10.1 | applied |
| 3 | v2026.12.31 | v2027.1.1 | applied |
| 4 | v2026.8.22 | v2026.8.22.1 | applied |
| 5 | v2026.8.10 | v2026.8.9 | regressed |
| 6 | v2026.8.22 | 2026.8.23 | rejected (no `v`) |

**DS-8 provider pool composition** (Traces to US-6)
| # | primary | fallbacks | expect |
|---|---|---|---|
| 1 | openai | — | runs |
| 2 | nope | — | `needs_provider` |
| 3 | openai | [nope] | runs; pool={openai}; 1 WARN |
| 4 | ZAI (entity) vs zai (config) | — | `needs_provider` |
| 5 | nope | [openai] | `needs_provider` (primary rules) |
| 6 | openai | [nope, zai] | runs; pool={openai, zai}; 1 WARN |

### Regression Requirements

This feature **modifies existing functionality**.

1. **Behaviours preserved:** media modality resolution and resize budgets for every model in today's seed (the 78 models must resolve under their canonical provider to the same or registry-corrected modalities — corrections are listed in ADR §2 *Why*); the checksum/raw-fallback/degraded-transport semantics of `GHReleasePuller`; the refresh transaction's "retain last known good on any failure"; `buildProviderPool`'s skip-with-WARN; SSRF-safe clients on every live provider call; the onboarding two-phase commit (probe → write → complete) and its rate limit; `/providers/{id}/test` outcome classification (`classify`, `BuildMessage`).
2. **Existing tests that must pass unchanged** (grep gates and these lists are evaluated on the **merged** branch, X-34; after merge ADR-066's `TestConfig_NoContextWindowDefaultKey` and ADR-068's `TestDefaultsSeed_NoRemovedProvider` must also pass — X-29): `pkg/providers/capabilities/puller_test.go` (11 of 12 — moved to `pkg/providers/catalog`; **`TestGHReleasePuller_Pull_NoSidecar` is rewritten to assert rejection (F-02)** and the 2 MB-cap fixtures are re-sized for `maxCatalogAssetBytes` (F-05)); `capabilities/catalog_test.go` tests for `validate`, version compare, `refreshLocked` retention, degraded transport (moved; key arguments become pairs); `pkg/gateway/provider_validation_test.go`, `rest_agent_provider_test.go`, `websocket_provider_refusal_test.go` (ids in fixtures re-keyed to canonical — assertion logic unchanged); `provider_credential_degraded_test.go` is **updated by ADR-068** (template-row shape gone, X-32); `pkg/agent/subturn_target_identity_test.go` is **re-keyed here** — its `Provider: "mock"` becomes a custom row (`custom: true`, stub `api_base`, `protocol: openai-compatible`) so its agents are not `needs_provider`; ADR-066 adds assertions only afterwards (X-31); `pkg/api/generated/contract_test.go`, `llm_error_codes_test.go`, `llm_error_catalogue_test.go`, `llm_error_no_hardcopy_test.go` (X-01).
3. **Existing tests deleted with their subject:** `TestCatalog_DriftGuard_IdIsKnownProtocol`, `TestCatalog_DriftGuard_IdInProbeEnum`, `TestCatalog_DriftGuard_BaseNonEmptyOrExempt`, `TestCatalog_DriftGuard_NewProtocolUntriagedFails`, `TestWireDerivation_Table` (the wire-suffix rule is gone), `TestContract_ProviderCatalogEntry_Shape` (schema replaced), SPA `providerMigration` tests, `catalog-consistency.test.ts` (rewritten against the `GET`); `cmd/omnipus/internal/onboard/validate_integration_test.go` (depends on `GetDefaultAPIBase` ×4 — rewritten against the embedded snapshot).
4. **New regression tests:** T6 (miss semantics identical to today's optimistic path), T13 (raw fallback), T32 (pool skip), `TestMediaResize_BudgetsUnchangedForSeedModels` (DS-6), `TestValidateKey_OutcomeClassificationUnchanged` (probe model source changed; classification must not); `TestVoiceTranscriber_ConstructsViaProtocolDispatch` and `TestLegacyProvider_ConstructsViaProtocolDispatch` for the two impact-table callers with no existing coverage.
5. **DS-6 regression — seed parity** (Traces to US-4.AC3): for each of the 78 `(canonical provider, model)` pairs from the old seed: expected `input_modalities` (seed value, or the ADR-listed registry correction) and `resize_budget` (vendor value). 78 rows, generated from the old seed file at test-authoring time and committed as `testdata/seed_parity.json` with a `correction_source` column naming the models.dev commit for each corrected row (F-33).

---

## 8. Requirements & Success Criteria

### Functional Requirements

- **FR-001** The system MUST load provider catalogs only at `schema_version` `2.0.0`; any other value is rejected and the previously loaded document retained.
- **FR-002** The system MUST validate on load: non-empty `version` matching `^v\d{4}\.\d{1,2}\.\d{1,2}(\.\d+)?$`, non-empty `updated_at`/`source`; `protocols[]` optional but, when present, containing the primary with the same `api`, entries unique; `protocol` empty only when `tier: unsupported`; positive default resize limits; ≥1 provider; unique non-empty provider ids; per-provider unique non-empty model ids; `protocol ∈ {openai-compatible, anthropic, google, ollama, cli}`; every model's `input_modalities` includes `text`; `tier ∈ {popular, standard, unsupported}`; `auth_methods` non-empty ⊆ `{api_key, sign_in}`; `status ∈ {active, retired}`; `release_date`, when present, parses as `YYYY-MM-DD`.
- **FR-003** The system MUST key every lookup on the exact `(provider id, model id)` pair and MUST NOT strip or add prefixes.
- **FR-004** A lookup miss MUST yield the optimistic modality default and catalog default resize limits to the media path, and *unknown* window/output to the agent loop. The catalog is read-only for windows: any per-model override is ADR-066's `ContextSettings.yaml` `model_overrides[]`, never a catalog field.
- **FR-005** `pkg/providers/capabilities` MUST be removed; its machinery lives in `pkg/providers/catalog`; exactly one embedded catalog file exists.
- **FR-006** The embedded snapshot MUST be a committed file in `pkg/providers/catalog/data/providers_catalog.json`, refreshed only by pull request; the build MUST NOT fetch.
- **FR-007** The puller MUST target the assembly repository's release asset `providers_catalog.json` with sidecar `.sha256`, verifying SHA-256 on both the release and raw paths (existing behaviour).
- **FR-008** The gateway MUST start one background pull after the listener is bound (30 s timeout; skipped when the persisted document is < 1 h old) and one every 24 h; no pull on any request or turn path; no manual refresh endpoint exists; boot MUST NOT wait for it. A successful refresh logs one INFO with the new version.
- **FR-009** Checksum mismatch or **missing sidecar** (`checksum`), wrong schema (`schema_version`), invalid document incl. URL violations (`invalid`), version regression (`regressed`), or body exceeding `maxCatalogAssetBytes` = 16 MB (`too_large`, read limit cap + 1) `[A-18]` MUST each log exactly one WARN with that `reason` key and retain the current document.
- **FR-010** The persisted last-known-good MUST be written after a successful apply and read at boot; a persisted file that fails FR-001/FR-002 MUST be ignored with one WARN `[A-4]`.
- **FR-011** Provider ids in config, agent entities, seeds and the probe request MUST be catalog ids or operator-named custom rows (`custom: true`); no alias table exists in code.
- **FR-012** The factory MUST dispatch on protocol only (`openai-compatible`, `anthropic`, `google`, `ollama`, `cli`, plus the custom-row case selected by `Custom: true`); inside `case "cli"` the driver is chosen by the row's `cli_kind ∈ {codex, copilot}`, never by id (X-14); the base URL comes from the catalog row (explicit `api_base` in config wins); keys come from the credential store, never from the row's `env`. `case google` constructs the HTTP provider at the row's Gemini OpenAI-compatible URL with Bearer auth — the same transport as today's `google` case `[A-7]`.
- **FR-013** A config MAY select a secondary protocol a provider offers via `protocol`; absent → primary; a protocol not offered → error `[A-8]`. The persisted `ModelConfig` field list is the one in §1.2 X-25 (kept: `Provider`, `Model`, `Protocol`, `Custom`, `APIBase`, `APIKeyRef`, `AuthMethod` (`api_key|sign_in`, ADR-068), `UpdatedAt` (ADR-068), `Models`, `Proxy`, `MaxTokensField`, `RequestTimeout`, `ThinkingLevel`, `ExtraBody`, `RPM`, `Home`, `Fallbacks`, `Name`; deleted: `ModelName`).
- **FR-014** A custom row MUST require `api_base` and accept `protocol ∈ {openai-compatible, anthropic}` only; it carries `custom: true` on disk (`ModelConfig.Custom`) and on the wire (`Provider.custom`); every check is on the flag (X-13).
- **FR-015** An unknown provider id MUST produce `ErrUnknownProvider` whose message names the id and never a canonical alternative.
- **FR-016** Boot MUST succeed with unknown providers; the provider row reports `unknown-provider` `[A-16]`. An agent is `degraded_reason: needs_provider` **iff its primary provider is unknown** (or fails exact-match, FR-036); it then refuses turns with error kind `needs_provider` (logged at WARN) and zero upstream requests. An unknown provider referenced only by a fallback is dropped from the pool with one WARN naming the agent and the provider; the agent runs on the remaining pool.
- **FR-017** `GET /api/v1/providers/catalog` MUST return a pre-serialised byte slice cached at apply time with `version`, `updated_at`, `source ∈ {embedded, pulled}`, `stale` (true when `updated_at` > 14 days); `ETag: "<sha256>"` quoted strong, `Cache-Control: private, max-age=0, must-revalidate`, no content negotiation; 304 on an exactly matching `If-None-Match` (weak/unquoted → 200); bytes and ETag swapped atomically as one pair; 401 unauthenticated; 503 when no catalog `[A-1]`.
- **FR-018** Tier and unsupported reason MUST be data in the document; the popular set is `{openai, openrouter, anthropic, google, xai, groq, mistral, deepseek}` `[A-9]`.
- **FR-019** Configuring or probing a `tier: unsupported` provider MUST return 400 with the reason.
- **FR-020** The providers API MUST list models for `locality = cloud` providers from the catalog with no outbound call; for `locality = local` providers (FR-039) from the live endpoint.
- **FR-021** `POST /api/v1/providers/{id}/entitlement` MUST call the protocol's listing once (`openai-compatible`/`google` → `GET {api}/models`; `anthropic` → `GET {api}/v1/models` with `x-api-key` + `anthropic-version`; `ollama` → `/api/tags`), intersect with the catalog, annotate entitlement, surface extra models as limits-unknown, and cache by `SHA-256(providerID + ":" + credentialRefName)` — the ref name, never the secret — for the process lifetime, evicting on provider DELETE, on a PUT that changes `api_key`/`api_key_ref` (not on a PUT that only bumps `updated_at`), and on catalog refresh; `cli` and custom rows → 409; no resolvable key → 422; upstream non-2xx → 502 `{"error":"could not fetch upstream model list: status <n>"}` with nothing cached; rate-limited like `/test` `[A-13]` (X-03, X-12).
- **FR-022** Key validation MUST pick its probe model from the catalog for catalog providers (first active tool-calling text model in document order), fall through to the next candidate on `model_not_found` at most 3 times, and MUST NOT pre-fetch `/models` for them `[A-20]`.
- **FR-023** `ProbeProviderRequest` MUST be exactly `{id (1..64, no enum/pattern), auth (required: api_key|sign_in), api_key?, model? (1..256), api_base?, protocol? (openai-compatible|anthropic)}` with `api_key` required iff `auth = api_key` and `api_base` + `protocol` required iff `id` is not a catalog id (custom row); `id` is validated at runtime against the catalog. This spec owns `id`/`api_base`/`protocol`; ADR-068 owns `auth`/`api_key`/`model`; the `pkg/gateway/inboundschemas/` copy is edited in the same commit (X-04).
- **FR-024** This spec's implementation owns the ONE coordinated contract commit for every shared schema file and its `pkg/gateway/inboundschemas/` twin (X-26); ADR-068 and ADR-066 own the semantics of their fields and consume generated types only. Every new or changed wire type MUST be defined in `contracts/` first and consumed only via generated types: `ProvidersCatalog`, `CatalogProvider`, `CatalogModel`, `EntitlementResponse`; `ProbeProviderRequest` rewritten to the one shape in FR-023 (id/auth/api_key/model/api_base/protocol); `Provider.protocol`, `Provider.custom`, and ADR-068's `Provider.auth_method` / `account_label` / `dependents` / `backs_default` / `updated_at` (this spec's `GET /providers` emits `dependents: []`, `backs_default: false`, `auth_method: api_key` until ADR-068's computation lands); `ProviderUpdateRequest.protocol` and `.auth_method`; `DefaultModel.yaml` and `DefaultModelUpdateRequest.yaml` (semantics ADR-068); `LLMError` code `needs_provider` in all four copies (`LLMError.yaml`, `LLMErrorReplay.yaml`, inline `asyncapi.yaml` `LLMError` L1512 and `LLMErrorReplay` L1632) — X-01/X-02; `Provider.status` — one enumeration shared verbatim with the ADR-068 spec: `connected | disconnected | error | unknown-provider | signed_in | expired` (this spec defines `unknown-provider`; ADR-068 defines `signed_in`/`expired`); `Agent.yaml` coordinated edit — this spec adds `degraded_reason` (enum, currently the single value `needs_provider`, optional) beside ADR-068's `needs_model: boolean` (always present, derived) and ADR-066's **optional** `context_window_effective` / `context_window_source` (`$ref ContextWindowSource.yaml`, owned by ADR-066) / `context_window_clamped`; both flags may be true and `needs_provider` wins in copy (FR-031, ADR-068 FR-014); `ModelCapabilities` deleted.
- **FR-025** `GET /providers/model-capabilities`, `src/lib/generated/providerCatalog.ts`, `src/lib/providerMigration.ts`, `knownDisplayNames`, `probeModelDefaults`, `knownProtocols`, `GetDefaultAPIBase`, `resolveStrippedPrefix` MUST be deleted `[A-12]`.
- **FR-026** The embedded snapshot MUST be ≤ 8 MB `[A-2]`, MUST contain the nine local-file providers and the popular set, MUST NOT contain a `custom` row, and every `tier: unsupported` row MUST carry an `unsupported_reason` (`amazon-bedrock` = `cloud-iam`, `azure` = `deployment-url`).
- **FR-027** The assembly contract (§5) MUST be captured in a conformance fixture committed under `pkg/providers/catalog/testdata/` and used by the Go tests.
- **FR-028** The refresh loop MUST serialize concurrent pulls and be race-free under `-race`.
- **FR-029** `GET /api/v1/providers` MUST return configured providers only; the handler MUST NOT emit template rows for unconfigured catalog providers (today's ~25 permanent `disconnected` rows are removed). The catalog is served by FR-017 alone.
- **FR-030** The provider shape MUST carry `auth_methods[]`, `aliases[]` (search-only), `name`, and per model `name`, `release_date`, for ADR-068's picker; `aliases[]` MUST NOT participate in resolution, validation, or construction `[A-9]`.
- **FR-031** When both `degraded_reason: needs_provider` (this spec) and ADR-068's derived `needs_model: true` apply to an agent, `needs_provider` MUST take precedence in user-facing copy `[A-16]`; the two fields are separate (`degraded_reason` enum, `needs_model` boolean) and land in one `Agent.yaml` edit.
- **FR-032** A release or raw asset with no `.sha256` sidecar MUST be rejected with `reason=checksum`; on the release path the sidecar MUST be located from the release's own asset list; the raw fallback `Ref` MUST be pinned to `main` in code with no configuration override (F-02, F-28).
- **FR-033** Every non-empty `api` and `protocols[].api` MUST be an absolute `https` URL with a non-empty host, no userinfo, no query, no fragment, and a host that is not an IP literal in loopback, link-local, RFC 1918, ULA or metadata (`169.254.169.254`) ranges — except rows with `locality = local` (FR-039), which MAY use `http` and local hosts. A violating document MUST be rejected whole (`reason=invalid`, path named). Custom rows' `api_base` is validated for parseability only (F-03).
- **FR-034** The catalog key MUST be exactly `(ModelConfig.Provider, ModelConfig.Model)`; `Model` is the bare catalog model id; `ExtractProtocol` and every `provider/` prefix convention on `Model` MUST be deleted (F-06).
- **FR-035** Custom rows MUST be a factory case, not a catalog row: a provider config whose id is not in the catalog is accepted iff it carries `api_base` and `protocol ∈ {openai-compatible, anthropic}`, becoming a custom row keyed by that id with `custom: true` (several coexist); otherwise `ErrUnknownProvider`. No code path and no test MAY test the literal id `custom` (X-13). `PUT /providers/{id}`, the onboarding probe and CLI onboard MUST apply the same rule (F-07/F-08/F-09).
- **FR-036** Every provider-id comparison in `pkg/agent`, `pkg/gateway`, `pkg/providers` MUST be exact after `TrimSpace` (no case folding); `instance.go::findModelConfigForProvider` and `resolveAgentPrimaryProvider` are modified accordingly (F-15).
- **FR-038** `LLMError.code` **`needs_provider`** (attribution `config`, copy *"This agent's provider isn't configured. Open Settings → Providers to connect one."*) MUST be added to all four hand-kept copies in the contract commit; `TestContract_LLMError_AllClassifierCodesRoundTrip` and the catalogue/no-hardcopy tests MUST pass; the pre-turn gate order is `needs_provider` → `model_unassigned` (ADR-068) → ADR-066's window refusal (X-01/X-02).
- **FR-039** `pkg/providers/catalog` MUST export the single locality predicate: `locality = local ⇔ protocol ∈ {ollama, vllm} ∨ id = lmstudio ∨ (custom ∧ api host loopback/private)`, else `cloud`; derived on load, carried on every provider handle, and the ONLY classification ADR-066/ADR-068 may consume (X-16/X-17).
- **FR-040** `pkg/agent/model_resolution.go::buildModelListResolver` MUST resolve by (1) exact `(provider, model)` pair, (2) bare model id listed by exactly one configured non-degraded provider (catalog models for cloud rows, manual `models[]` for local rows), else (3) unresolved → ADR-068 `needs_model`; no `ModelName` alias, no prefix split, no passthrough (X-24).
- **FR-037** `/health` MUST report the catalog degraded with the last refresh error whenever the served document is stale (> 14 days) or the last refresh failed; a catalog refresh MUST invalidate the entitlement cache and MUST NOT rebuild constructed provider instances (F-24, F-26).

### Success Criteria

- **SC-001** `Resolve("openrouter","z-ai/glm-5.2").Window()==1048576`, `Resolve("zai","glm-5.2").Window()==1000000`, `Resolve("openrouter","glm-5.2")` is a miss — asserted by T3/T5.
- **SC-002** Checksum-mismatched and non-2.0.0 releases produce exactly 1 WARN each and leave `Version()` unchanged — T9/T10.
- **SC-003** With the network stubbed closed, `GET /providers/anthropic` returns the catalog's model count and the outbound request counter is 0 — T36.
- **SC-004** `TestFactory_NoVendorCases` finds case set exactly `{openai-compatible, anthropic, google, ollama, cli}` plus the `Custom`-flag branch; the `cli` case switches only on `cli_kind` values — T24.
- **SC-005** Boot with an unknown provider reaches listen; a recording stub sees its first catalog hit only after listen; the bound agent's turn returns `needs_provider` with 0 upstream requests; an agent with only an unknown fallback runs — T41/T33/T33b/T42.
- **SC-006** `make verify-contracts`, `npm run typecheck`, `npx vitest run`, `golangci-lint run --build-tags=goolm,stdjson`, `gofmt -l . | wc -l == 0`, `govulncheck` all exit 0 in CI.
- **SC-007** `make build` succeeds in a CI job with egress disabled — T48.
- **SC-008** `ls pkg/providers/capabilities` fails; `grep -rn resolveStrippedPrefix pkg | wc -l == 0`.
- **SC-009** `grep -rnE '_migrated|alias|deprecat|retired' pkg/providers pkg/config` matches only the `retired` status token in `pkg/providers/catalog` — T29.
- **SC-010** A config with `provider: "z-ai"` fails as unknown and the boot log, `GET /providers`, and `GET /agents` bodies contain no canonical id (`zai`) anywhere — the assertion is on the **absence of the canonical id**, never on the echoed user-supplied id (whose `unknown provider %q` wording ADR-068 shares) — T41.
- **SC-011** `GET /providers/catalog` serves a pre-serialised slice (the handler does no per-request marshal — asserted by a benchmark showing 0 allocs beyond the response write); `BenchmarkResolve` mean < 1,000 ns/op with 0 allocs (`-benchmem`). Wall-clock figures are recorded in perf-nightly only.
- **SC-014** A release without a sidecar, and a hosted row with `http://`/private `api`, are each rejected with the named `reason` and the served version is unchanged — T9b/T9c.
- **SC-015** `v2026.9.30 → v2026.10.1` and `v2026.8.9 → v2026.8.10` are applied, not regressed — T7.
- **SC-012** Seed parity: all 78 DS-6 rows pass — modalities equal the seed or the ADR-listed registry correction; resize limits equal the vendor value.
- **SC-013** Embedded snapshot size ≤ 8 MB and passes FR-002 — T16.

### Traceability Matrix

| FR | US | BDD Scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 | US-1, US-3 | reject malformed (schema rows); wrong schema ignored | T2, T10, DS-1.2/3, DS-4.6 |
| FR-002 | US-1 | reject malformed (7 rows); zero models; unicode; conforming | T1, T2, DS-1.1,4–12,15 |
| FR-003 | US-4 | exact route; no prefix stripping; same model id | T3, T5, DS-2.1–7 |
| FR-004 | US-4 | miss per consumer; unknown limits | T4, T6, DS-2.3 |
| FR-005 | US-4 | single package | T16 (build), SC-008 |
| FR-006 | US-2 | hermetic build | T48, T16 |
| FR-007 | US-2, US-3 | release layout; raw fallback; checksum mismatch | T8, T9, T13, DS-4.1–4 |
| FR-008 | US-3 | offline boot; startup pull; 24 h ticker | T42, T43 |
| FR-009 | US-3, E1 | checksum; wrong schema; downgrade; oversize | T9–T12, DS-4.2,6,7, DS-1.13/14 |
| FR-010 | US-3 | startup pull (persist); stale persisted ignored | T42, T15 |
| FR-011 | US-5, US-10, US-11 | seeds canonical; old id fails; probe old id | T25, T23, T40, DS-3.8 |
| FR-012 | US-5 | construct from table; no vendor cases | T20, T24, DS-3.1/2/10/11 |
| FR-013 | US-5 | protocol choice | T21, DS-3.3/4 |
| FR-014 | US-5, US-8, E12 | custom endpoint; custom requires base; disallowed protocol | T22, T35, DS-3.5–7 |
| FR-015 | US-5, US-6 | non-canonical outline; no hint anywhere | T23, T41, DS-3.8/12 |
| FR-016 | US-6 | boot survives; refuses typed; repair | T32, T33, T41 |
| FR-017 | US-7 | endpoint; 401; ETag; 503 | T34, T30, T45, DS-5.1–4 |
| FR-018 | US-8 | tiers in data | T17, T19 |
| FR-019 | US-8, US-10 | cloud-IAM 400; probe cloud-IAM | T35, T40, DS-5.5/15, DS-3.9 |
| FR-020 | US-9 | offline model list; local endpoints live | T36, T38, DS-5.9 |
| FR-021 | US-9 | entitlement intersects | T37, DS-5.10/11 |
| FR-022 | US-9 | probe model from catalog | T26, T39 |
| FR-023 | US-10 | probe zai / old / custom / bedrock | T31, T40, T46, DS-5.12–17 |
| FR-024 | US-7, US-10 | endpoint validates; probe no enum | T30, T31, T47 |
| FR-025 | US-11 | SPA artefacts gone; greenfield grep | T29, SC-008 |
| FR-026 | US-2, US-8 | local providers present; popular; cloud-IAM | T16–T19 |
| FR-027 | US-2 | load conforming; release layout | T1, T8 |
| FR-028 | E5 | concurrent refresh | T14 |
| FR-029 | US-7 | providers list is configurations only | T34b, DS-5.4b/4c |
| FR-030 | US-2, US-11 | load conforming; reject malformed (`auth_methods`) | T1, T2, T29 (aliases never resolved) |
| FR-031 | US-6 | bound agent refuses, typed | T33 |
| FR-032 | US-2, E11 | missing sidecar rejected | T9b, DS-4.3 |
| FR-033 | E13 | URL validation outline | T9c, DS-1.18–22 |
| FR-034 | US-5 | catalog key is (Provider, bare Model) | T22b, DS-2.9/10 |
| FR-035 | US-5, US-8, US-10 | custom is a factory case; unknown id with base only; probe custom | T22, T35, T40, DS-3.13–15 |
| FR-036 | US-6 | case-different entity id | T28, T33b, DS-8.4 |
| FR-037 | US-7, E10 | stale flag; entitlement caches (refresh evicts) | T34c, T37 |
| FR-038 | US-6 | bound agent refuses, typed | T33, T31b |
| FR-039 | US-9, E13 | local endpoints are live; URL validation outline | T24b, T38, T9c |
| FR-040 | US-5 | catalog key is (Provider, bare Model) | T24c, T22b |
| FR-016 (mixed) | US-6 | unknown fallback dropped; repair without restart | T33b, T33c, DS-8 |
| E6 / E8 | — | persisted newer wins; retired model constructs | T6b, T20b |

Every BDD scenario above maps to ≥1 FR via its US; assembly-side scenario US-2.AC2 traces to FR-027 (fixture carries `disputed`) and is executed in the assembly repo.

---

## 9. Ambiguity Self-Audit

**GATE PASSED — all 22 resolved by operator/coordinator 2026-08-22.** Decisions are applied throughout; `[A-n]` in the body refers to the row below.

| # | Ambiguity | RESOLVED decision |
|---|---|---|
| A-1 | Providers-catalog `GET` shape/caching | **ACCEPT** — full document; strong ETag = SHA-256 of served bytes; `If-None-Match` → 304; SPA re-validates on Settings open and every 15 min; no WS push (FR-017). |
| A-2 | Embedded snapshot extent | **ACCEPT** — full document; 8 MB test bound; assembly job trims `status: retired` models first if exceeded (FR-026). |
| A-3 | Deprecation status value | **ACCEPT** — `status ∈ {active, retired}`; greenfield grep whitelists that token in `pkg/providers/catalog` (SC-009). |
| A-4 | Persisted last-known-good file | **ACCEPT** — `$OMNIPUS_HOME/providers_catalog.json`; old `capabilities_catalog.json` neither read nor deleted (no code handles it) (FR-010). |
| A-5 | Assembly repo / puller order | **OPERATOR DECISION** — `elicify-ai/omnipus-provider-catalog`; puller order unchanged (release API → raw fallback) (FR-007). |
| A-6 | `version` format | **ACCEPT, corrected by F-01** — `vYYYY.M.D[.N]` with the leading `v` (T7, DS-7). |
| A-7 | Protocol enum | **ACCEPT** — `{openai-compatible, anthropic, google, ollama, cli}` + the `custom` case; `google` distinct, same HTTP transport with Bearer auth at the Gemini OpenAI-compatible URL (FR-012, F-13). |
| A-8 | Protocol choice location | **ACCEPT** — `protocol` on `ModelConfig` and on `Provider`/`ProviderUpdateRequest` wire types; absent → catalog primary (ADR-067 D11) (FR-013, FR-024). |
| A-9 | Tier mechanism + picker fields | **RESOLVED by ADR-067 D12** — tier is catalog data from `overrides/`; no popular list in Go or SPA. **Shape extended** for ADR-068's picker: provider `tier`, `unsupported_reason`, `auth_methods[]`, `aliases[]` (search-only, never resolution), `name`; model `name`, `release_date`, `tool_call`, `context_window`, `max_output_tokens`, `input_modalities`, `status`. All from models.dev except tier/unsupported_reason/auth_methods/aliases (overrides only) (FR-018, FR-030). |
| A-10 | Catalog-level default resize | **ACCEPT** — `default_resize_limits` (FR-002, FR-004). |
| A-11 | Missing limits | **ACCEPT** — `0` = unknown; ADR-066 ladder decides (FR-004). |
| A-12 | `GET /providers/model-capabilities` | **ACCEPT** — deleted; SPA derives modalities from the catalog `GET` by `(provider, model)` (FR-025). |
| A-13 | Entitlement surface | **ACCEPT** — `POST /api/v1/providers/{id}/entitlement` → `EntitlementResponse.yaml` `{models: [{id, entitled: boolean, limits: "known" \| "unknown"}], checked_at: date-time, cached: boolean}` (shared verbatim with the ADR-068 spec, MAJ-014); `refresh-models` removed; cache per key for process lifetime (FR-021). |
| A-14 | Display names | **ACCEPT** — `name` on provider and model; `knownDisplayNames` deleted; unknown → id verbatim (FR-025, FR-030). |
| A-15 | Snapshot refresh cadence | **ACCEPT** — weekly bot PR; release checklist requires snapshot ≤ 14 days old (FR-006). |
| A-16 | Degraded-state wire vocabulary | **ACCEPT** — `Provider.status` enum is `connected \| disconnected \| error \| unknown-provider \| signed_in \| expired` (shared verbatim with ADR-068); `Agent.degraded_reason: "needs_provider"` sits beside ADR-068's `needs_model: boolean` in one coordinated `Agent.yaml` edit: both may be true; **`needs_provider` wins in copy** (FR-016, FR-031). |
| A-17 | Catalog Go type | **ACCEPT** — single `catalog.Catalog` with `Resolve(provider, model) Handle` (FR-005). |
| A-18 | Pull size limit | **ACCEPT** — `maxCatalogAssetBytes` = 16 MB, read limit cap + 1; the puller's 2 MB cap is raised (FR-009, F-05). |
| A-19 | Id normalisation | **ACCEPT** — trim-only, exact ids (T28). |
| A-20 | Probe-model rule | **ACCEPT** — first active tool-calling text model in document order; no `probe_model` field; fall-through on `model_not_found`, 3 attempts (FR-022, F-25). |
| A-21 | CLI onboarding source | **ACCEPT** — validates against the embedded snapshot (FR-011). |
| A-22 | Disputed marker | **ACCEPT** — carry `disputed: true`; surfaced only via ADR-066 D9's source label (FR-027). |
| + | Coordinator addition | `GET /providers` returns **configured providers only**; the ~25 template `disconnected` rows are removed (greenfield; ADR-068 D14 context). Picker = catalog; providers list = configurations (US-7.AC6, FR-029). |

---

## 10. Holdout Evaluation Scenarios *(post-implementation; NOT in traceability)*

- **H-HP1:** On a laptop with Wi-Fi off, start the gateway fresh, open Settings → Providers, add Anthropic with any key, open the model selector: the list appears immediately with context-window numbers next to models, and the gateway log shows no outbound `/models` attempt.
- **H-HP2:** Configure Z.ai as `zai` and OpenRouter; set two agents to `glm-5.2` via each; in each agent's Settings window display (ADR-066 D9) read the catalog window: 1,000,000 for the direct route and 1,048,576 via OpenRouter.
- **H-HP3:** Leave a gateway running across a day with network; the next morning `GET /api/v1/providers/catalog` reports a newer `version` and `source: pulled`, and exactly one refresh line per attempt appears in the log.
- **H-EP1:** Point the puller (test build flag) at a host serving a tampered asset with the real sidecar: the gateway logs one WARN naming a checksum mismatch and Settings keeps showing the previous `version`.
- **H-EP2:** Hand-edit `config.json` to `provider: "z-ai"` for one agent and restart: the gateway comes up, other agents chat normally, the provider row says unknown provider, the agent says it needs a provider, and nowhere — log, API, UI — is the word `zai` offered as the fix.
- **H-EC1:** Pick a provider from the long tail (e.g. a Chinese coding-plan variant) that Omnipus never shipped before, enter a key, and send a message: the request goes to the URL the catalog lists for it, with no code change.
- **H-EC2:** Select `amazon-bedrock`: it is visible in the list, cannot be configured, and the reason shown is cloud-IAM; the API returns 400 with the same reason.

---

### Summary
- **Gate status:** Phase 1 treated as confirmed (ADR = brief); Phase 5.5 PASSED; **grill findings 3C/14M/11m/6O all dispositioned in §1.1** (33 applied, 1 kept by operator decision (F-30), 1 follow-up noted (F-32); ADR-067 §8b amended).
- **User stories:** 11 (P0: US-1,2,3,4,5,6,7,10,11 · P1: US-8,9)
- **BDD scenarios:** 72 (HP 25 · AP 13 · EP 21 · EC 11 · mixed 2), including 6 scenario outlines (9 + 4 + 5 + 8 + 6 + 6 example rows)
- **Test datasets:** 8 (DS-1: 26 · DS-2: 10 · DS-3: 15 · DS-4: 9 · DS-5: 24 · DS-6: 78 generated · DS-7: 6 · DS-8: 6) = 174 data rows
- **Functional requirements:** 40 · **Success criteria:** 15
- **Tests planned:** 64 (40 unit · 4 agent-unit · 15 integration · 2 vitest · 3 CI gates)
- **Risk flagged:** factory collapse + `GetDefaultAPIBase` removal are HIGH-impact (every provider construction path); must land atomically with the table-backed replacement.
- **Follow-ups outside this spec:** picker/entitlement UX, `Provider.status` additions for sign-in, provider deletion (ADR-068); window ladder/floor/learned consumption of `Window()` (ADR-066 D2–D3/D8/D9); the assembly repository's own implementation against §5's contract and the shared fixture.
