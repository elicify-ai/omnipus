# Feature Specification: ADR-068 — Subscription login policy, provider deletion, the default model, and the provider UX at 190 providers

**Created**: 2026-08-22
**Status**: Draft — 18 ambiguities resolved 2026-08-22; adversarial review (`adr-068-providers-ux-spec-review.md`, BLOCK: 4 CRIT / 19 MAJ / 12 MIN / 5 OBS) resolved 2026-08-22; cross-spec review (`cross-spec-review-adr-066-067-068.md`, BLOCK) S68/A68 items resolved 2026-08-22 — see "Review Findings Disposition" and "Cross-Spec Findings Disposition" at the end. Brief amended: ADR-068 §2.2, §2.4, §3 (D14.1, D14.2), §4, §7 (ownership note), §8, §9.
**Input**: [`docs/internal/architecture/ADR-068-subscriptions-provider-deletion-and-provider-ux.md`](../architecture/ADR-068-subscriptions-provider-deletion-and-provider-ux.md) (Proposed, 2026-08-22) plus the §8a resolutions of [`ADR-066 …-review-pass2.md`](../architecture/ADR-066-context-budget-and-tool-result-routing-review-pass2.md) (MAJ-011, MAJ-012, MAJ-013, MAJ-015, MIN-008).
**Branch**: `feat/context-budget-and-tool-result-routing`
**Format precedent**: [`agent-form-requirements.md`](./agent-form-requirements.md) (UI spec), [`provider-validation-centralization-spec.md`](./provider-validation-centralization-spec.md) (provider REST spec)

> **Greenfield rule (operator, 2026-08-22) applies to every section below:** no backward compatibility, no migration, no aliasing of old names, no grace periods, no retired-name lists, no boot notification about removed things. Pre-existing state that does not match this design simply does not work — and fails on the generic unknown-provider path, never on a path that names what was removed.

> **Scope fence.** This spec covers ADR-068 only: D13 (subscription login policy, `antigravity` deletion, `claude-cli` descoping, the `codex-cli` / `openai-chatgpt` split), D14 (provider deletion, default model as a Settings control), §4 (UI surfaces), §5 (UX decisions and IA), §6–§7 (wire consequences), §8a (pass-2 resolutions). **The catalog's data source, schema, refresh and checksum are ADR-067** and are *referenced* here, never specified. Context-window limits and their display are ADR-066 D9, referenced only.

---

## Available Reference Patterns

`docs/reference/go-implementation/` does not exist in this repository (checked 2026-08-22: `ls docs/reference/go-implementation/` → no such directory). No reference patterns apply. In-repo precedents used instead:

| In-repo precedent | Pattern | Relevance |
|---|---|---|
| `pkg/gateway/rest.go::HandleProviders` (PUT branch) | credential-store-ready check → SSRF check → `providers.ValidateKey` → config write under `configMu` → `triggerReloadAndWaitOutcome` | DELETE and the default-model PUT follow the same write-then-wait-for-reload shape |
| `pkg/gateway/rest.go::HandleMCPServers` DELETE branch | sub-path dispatch for `DELETE /{id}` | the DELETE branch of `HandleProviders` mirrors it |
| `pkg/gateway/rest_default_agent_singleton_test.go` | "a PUT that returns 200 but changes no routing is the ADR-037 anti-pattern" | the default-model PUT must be proven to change the next turn's model |
| `src/components/ui/model-selector.tsx` | cmdk `Command` popover with typeahead | the shared provider picker is built on the same primitive |
| `src/components/settings/ReAuthDialog.tsx` | Radix `Dialog` consent primitive | extended to "session expired → re-sign-in" |

---

## Existing Codebase Context

> GitNexus: the only registered index on this machine (`~/.gitnexus/registry.json`) belongs to a **different checkout** (`wt-library-improvements`, branch `feat/library-improvements`); this worktree has no `.gitnexus/` directory and no GitNexus MCP tools were exposed to this session. Per CLAUDE.md the fallback to direct Read/Grep is correct; everything below was verified by reading the files on this branch on 2026-08-22. Impact levels are judged from direct caller counts found by grep, not from the graph.

### Symbols Involved

| Symbol | Role | Context (read on this branch) |
|---|---|---|
| `pkg/gateway/rest.go::HandleProviders` | **modifies** | Dispatches GET `/providers`, PUT `/{id}`, POST `/{id}/refresh-models`, POST `/{id}/test` (and GET `model-capabilities`, which ADR-067 deletes). No DELETE branch. Gains DELETE `/{id}`, POST `/{id}/sign-in`, GET `/{id}/sign-in/status`. **Reserved literals `catalog` and `default-model` are dispatched BEFORE the `{id}` branch**; the id validator rejects them everywhere an id is accepted (MAJ-002). |
| `pkg/gateway/rest.go` — new registration `/api/v1/providers/default-model` via `adminWrap` (`withAuth → RequireNotBypass`, the chain `/api/v1/security/sandbox-config` uses at its `RegisterHTTPHandler`) | **adds** | GET/PUT default model (CRIT-001, MAJ-007). |
| `pkg/gateway/rest.go::refreshProviderModels` | **replaced** | Live `/models` fetch at `POST /{id}/refresh-models`; ADR-067 replaces it with `POST /api/v1/providers/{id}/entitlement` — the one wire name for *Check with my account* (MAJ-003). |
| `pkg/gateway/rest.go::HandleProviders` GET branch | **modifies** | Builds `gen.Provider` rows; must emit the new `status` values, `auth_method`, `account_label`, `dependents`, `backs_default`. |
| `pkg/config/config.go::AgentDefaults.ModelName` (+ `Provider`) and `gateway.go`'s two `ModelName == ""` guards (`gateway.go::start…` L1658, `…::ReloadProvider…` L5048) | **deleted** (CRIT-001) | `model_name` was an **alias** resolved by `GetModelConfig` → `findMatches` against each `providers[]` entry's single `ModelName`/`Model`; a `(provider, model)` pair had nowhere to land. Replaced by `AgentDefaults.DefaultModel {Provider, Model}` (`json:"default_model"`, contract `DefaultModel.yaml`). |
| `pkg/config/config.go::GetModelConfig` / `findMatches` | **modifies** (CRIT-001) | Resolves the pair exactly — `(provider id, model id)` against `providers[]` entries (ADR-067's exact lookup); the alias round-robin over `ModelName` is gone. |
| `pkg/config/config.go::ModelConfig` | **modifies** (MAJ-015, X-25) | Gains `UpdatedAt *time.Time` (`json:"updated_at,omitempty"`, mirrors `AgentConfig.UpdatedAt` L1097) written on every PUT — the source of *Recent*. `AuthMethod` is **kept** with the new closed set `api_key \| sign_in` (the `oauth`/`token` values are deleted with the OAuth ladder, MAJ-005); S67 FR-013 holds the single `ModelConfig` field list naming both specs' fields (`protocol` is S67's). |
| `pkg/audit` (`audit.Logger.Log(&audit.Entry{...})`, precedent `rest.go` deny/allow entries; event names in `pkg/audit/events.go`) | **calls** (MAJ-016) | New events `provider.deleted` and `provider.default_model.changed`. |
| `pkg/config/config.go::resolveFallbackProvider` | **calls** | Defines how a fallback slug maps to a provider; used to compute dependents. |
| `pkg/config/defaults.go` | **modifies** | Seeded provider templates; `antigravity/gemini-3-flash` default replaced. |
| `pkg/providers/factory_provider.go` — **two dispatch layers** (MAJ-005) | **modifies** | Layer 1, the protocol switch: `case "antigravity"` (delete), `case "claude-cli","claudecli"` (delete), `case "codex-cli","codexcli"` → `NewCodexCliProvider` (**subprocess — kept**, alias `codexcli` dropped), new `case "openai-chatgpt"` → `NewCodexProviderWithTokenSource(CreateCodexCliTokenSource())`; the `config.go` L2719 protocol comment follows (`knownProtocols`/`IsKnownProtocol` are deleted by S67 FR-025 — X-15). Under S67's protocol dispatch the `cli` case selects its constructor by the catalog row's `cli_kind: codex | copilot` (S67 field, X-14): `codex` → `NewCodexCliProvider`, `copilot` → `NewCopilotCliProvider` (new, below); `openai-chatgpt` is protocol `openai-compatible` with the `auth.json` token source and carries no `cli_kind`. Layer 2, the id-keyed OAuth ladder: `case "openai"`+`AuthMethod oauth|token` → `createCodexAuthProvider` (store-held OpenAI OAuth, **delete**); `case "anthropic"`+same → `createClaudeAuthProvider` (store-held Anthropic OAuth, **delete** — D13 §2.3 item 2). `CreateCodexCliTokenSource` (the `auth.json` reader) has **no non-test caller today**; it becomes `openai-chatgpt`'s token source. |
| `pkg/providers/antigravity_provider.go` (+ test) | **deleted** | 105 refs to the name; 33 files across the tree. |
| `pkg/providers/claude_cli_provider.go` (+ tests) | **deleted** | Descoped (D13 §2.3 item 2). |
| `pkg/providers/codex_cli_provider.go` | **keeps, re-keyed** | Becomes what the id `codex-cli` dispatches to (subprocess). |
| `pkg/providers/copilot_cli_provider.go` (+ `_test.go`, `_integration_test.go` with a fake `copilot` on PATH) | **adds** (X-14) | Provider id `github-copilot`: the official GitHub Copilot CLI driven as a subprocess, same shape as `codex_cli_provider.go`; no Go SDK module (Constraint #1). Full contract under "GitHub Copilot subprocess provider" below. |
| `pkg/providers/codex_provider.go`, `codex_cli_credentials.go` | **keeps, re-keyed** | Become provider id `openai-chatgpt` (direct HTTP with the CLI's saved token). `codex_provider.go::createCodexTokenSource` (store OAuth + in-app refresh) is deleted; `CodexCliAuth` drops `RefreshToken` — the struct reads `tokens.access_token` and `tokens.account_id` only (MAJ-006). |
| `pkg/auth/oauth.go::GoogleAntigravityOAuthConfig` | **deleted** | Deleted with `antigravity`. |
| `pkg/auth/oauth.go::{OpenAIOAuthConfig, RequestDeviceCode, PollDeviceCodeOnce, pollDeviceCode, RefreshAccessToken, BuildAuthorizeURL, ExchangeCodeForTokens}`, `codex_provider.go::createCodexTokenSource`, `factory_provider.go::{createCodexAuthProvider, createClaudeAuthProvider, createClaudeTokenSource}` | **deleted** (resolution #8, MAJ-005) | Omnipus starts no device-code/OAuth flow of its own for OpenAI; sign-in state is read from the CLI's saved login only. What remains of `pkg/auth/oauth.go` after the greenfield delete is whatever non-provider callers still use (verified at implementation by `go build`); if nothing remains the file goes. |
| `pkg/gateway/rest.go::HandleProviders` GET branch — permanent "disconnected" template rows (and its "final fallback: the configured default model alias" models fill) | **removed** (resolution #16, MAJ-017) | `GET /providers` returns configured providers only; the `cfg.Providers` seed templates are no longer echoed as rows. **Consumers verified 2026-08-22:** `createStartupProvider` (`gateway.go` L3636) and `defaultModelCredentialBlocked` (L3704) read `cfg.Providers` directly, not the REST rows — unaffected. SPA callers of `fetchProviders()` (`api.ts` L2179): `ProvidersSection`, `MemorySection`, `CreateAgentModal`, `AgentProfile`, `composer/ModelPicker` — all filter to connected rows already or list models, none depends on a template row; `onboarding.tsx` does not call it. The template-row consumer is removed entirely. |
| `pkg/credentials/store.go::Store.Delete` | **calls** | Deletes `<providerID>_API_KEY` on confirmed provider deletion. |
| `pkg/gateway/gateway.go::triggerReloadAndWaitOutcome` | **calls** | Applies config changes to live agent instances; DELETE and default-model PUT must wait on it. |
| `pkg/providers/catalog/gen/main.go` | **deleted** | Emits `src/lib/generated/providerCatalog.ts`; the TS emission goes. |
| `src/lib/generated/providerCatalog.ts` | **deleted** | 23-entry build-time file; SPA reads the providers-catalog GET (ADR-067 §5). |
| `src/routes/onboarding.tsx` (step 3, `PRIORITY_COMPANIES`, L1 tile grid ~L1249, L2 plan/region panel ~L1305) | **modifies** | Replaced by the shared picker + auth-method control; "Model for your first agent" field. |
| `src/components/settings/ProvidersSection.tsx` (`handleClose` L377, footer L605–645) | **modifies** | Default-model card, Remove provider, draft preservation, "Check with my account". |
| `src/components/settings/ProviderPickerSheet.tsx` | **replaced** | By the shared `ProviderPicker` component. |
| `src/components/settings/ProviderRow.tsx` | **modifies** | `signed_in` / `expired` states; Edit → Manage for sign-in providers. |
| `src/components/settings/ReAuthDialog.tsx` | **extends** | Re-sign-in variant for expired vendor sessions. |
| `src/components/ui/model-selector.tsx` | **modifies** | Vendor → release-date-desc ordering, Recommended chip, virtualisation above 100 items. |
| `contracts/components/schemas/ProbeProviderRequest.yaml` (+ `pkg/gateway/inboundschemas/ProbeProviderRequest.yaml`) | **consumes; S67 writes the file** (X-04, X-26) | ONE shape, owned by S67 and listed verbatim in both specs: `{id, auth: api_key\|sign_in, api_key?, model?, api_base?, protocol?}`. This spec owns the semantics of `auth`/`api_key`/`model`; S67 owns `id`/`api_base`/`protocol`. |
| `contracts/components/schemas/Provider.yaml` (+ `pkg/gateway/inboundschemas/Provider.yaml`) | **consumes; S67 writes the file** (X-05, X-26) | S67's single edit carries all seven new fields: its `protocol`, `custom`, `company`, `locality`, `cli_kind` and this spec's `status` values `signed_in`/`expired`, `auth_method`, `account_label`, `dependents`, `backs_default`, `updated_at`; `ProviderUpdateRequest` gains `auth_method` beside S67's `protocol`. Until this spec's computation lands S67's GET emits `dependents: []`, `backs_default: false`, `auth_method: api_key`. |
| `contracts/components/schemas/ProviderCatalogEntry.yaml` | **replaced by ADR-067's catalog schema** | Describes the 23-entry build-time catalog "never served from a live HTTP endpoint" — that description is now false by decision. |
| `contracts/components/schemas/OnboardingCompleteRequest.yaml` | **modifies** | `provider.api_key` required today; gains `auth_method`. |
| `LLMError` code set — **four hand-kept copies**: `contracts/components/schemas/LLMError.yaml`, `contracts/components/schemas/LLMErrorReplay.yaml`, and the inline `components.schemas.LLMError` and `LLMErrorReplay` blocks in `contracts/asyncapi.yaml` (each with `x-user-messages` / `x-user-message-attributions`; `scripts/_gen-asyncapi-types.mjs` generates the catalogue from asyncapi's inline block); drift guard `pkg/api/generated/llm_error_codes_test.go::TestContract_LLMError_AllClassifierCodesRoundTrip` (+ `llm_error_catalogue_test.go`) | **consumes; S67 writes all four** (X-01, X-26) | This spec defines the **semantics and copy** of `model_unassigned` (attribution `config`); S67 adds it to all four copies in its coordinated contract commit together with its own `needs_provider` (X-02). Both drift tests are regression rows here. |
| `pkg/gateway/rest_onboarding.go` probe handler (L726 `"unknown provider %q …"`, SSRF check L252/L737) | **modifies** | Free-string id validated against the catalog; `api_base` (the field's name in the shared shape — the old `endpoint` is gone) requires `protocol` and passes the existing `ssrfChecker.CheckURL` gate (MIN-006, X-04). |
| SPA consumers of deleted backend surfaces — `src/lib/api.ts` `fetchModelCapabilities` and its D18 warn-and-proceed callers (L82/L313/L499), the `refreshProviderModels` wrapper (L2191), every `PROVIDER_CATALOG` importer | **deletes** (X-23) | S67 owns every backend deletion and the removal of `src/lib/generated/providerCatalog.ts`; this spec owns removing the SPA consumers and re-pointing them to the catalog GET / entitlement route. The "served GET equals embedded snapshot" test is S67's (T34/T42) — not duplicated here. |
| `scripts/check-no-removed-providers.sh` (+ `scripts/no-removed-providers.allow`) | **adds** (MAJ-009/MAJ-015) | Source no-trace exit proof, modelled on `scripts/check-no-jpeg-screencast.sh`; wired into `.github/workflows/pr.yml` and `deploy/ci-worker/runci.sh` `lint`. No allow-list lives in `pkg/`. |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents (WILL BREAK) | d=2 Dependents (LIKELY AFFECTED) |
|---|---|---|---|
| `ProbeProviderRequest.id` enum → string | **HIGH** | `pkg/api/generated/openapi_types.gen.go`, `src/lib/api/generated/{openapi-types.ts,schemas.ts}`, `pkg/gateway/inboundschemas/`, `pkg/gateway/rest_onboarding.go` probe handler, `contract_test.go` | onboarding step 3 probe call, `tests/e2e/providers.spec.ts`, `src/lib/api.ts` probe wrapper |
| `Provider.status` enum gains values | **HIGH** | every `switch`/ternary on `provider.status` in `ProviderRow.tsx`, `ProvidersSection.tsx`, `ProvidersSection.test.tsx`; Zod schema; Go `gen.ProviderStatus*` consts | `tests/e2e/providers.spec.ts`, onboarding status hints |
| `factory_provider.go` cases | **HIGH** | `factory_provider_test.go` rows; `pkg/providers/catalog/catalog_test.go` "CLI executor / non-API-key ids" allow-list; S67's catalog membership check (`IsKnownProtocol` no longer exists — X-15) | any agent entity naming `antigravity`/`claude-cli` (fails as unknown provider — intended) |
| `AgentDefaults.ModelName` deleted → `DefaultModel` pair; `GetModelConfig` exact resolution | **HIGH** | `gateway.go` L1658/L5048 guards (deleted), `createStartupProvider`, `defaultModelCredentialBlocked`, `pkg/agent/model_resolution.go::buildModelListResolver`, `config.go::resolveFallbackProvider`, every test fixture setting `model_name` | every agent whose model resolves via the default; onboarding completion |
| `src/lib/generated/providerCatalog.ts` deleted | **HIGH** | `onboarding.tsx`, `ProvidersSection.tsx`, `ProviderPickerSheet.tsx`, `catalog_test.go::TestCatalog_EmbedMatchesGeneratedTS` (#13), `src/lib/constants.ts` key-hint map keyed by id | every vitest that imports `PROVIDER_CATALOG` |
| `pkg/auth/oauth.go::GoogleAntigravityOAuthConfig` | **LOW** | `antigravity_provider.go` only (deleted together) | — |
| `pkg/auth/oauth.go` OpenAI device-code flow + factory OAuth ladder deleted | **MEDIUM** | `codex_provider.go::createCodexTokenSource`, `factory_provider.go::createCodexAuthProvider/createClaudeAuthProvider` (L95, L311 call sites), `pkg/auth` tests, `factory_provider_test.go` rows for `AuthMethod: oauth` | any config row with `auth_method: oauth\|token` (fails as unknown auth method — intended, greenfield) |
| `GET /providers` template rows removed | **MEDIUM** | `ProvidersSection.test.tsx` "template-provider filtering" describe (becomes dead — delete), `provider_credential_degraded_test.go` fixtures, `tests/e2e/providers.spec.ts` | onboarding "fallback default entry" consumers, if any |
| `HandleProviders` DELETE branch (new) | **MEDIUM** | none (new) | `credentials.Store`, config writer, reload pipeline — new call pattern, seam tests required |

**HIGH-risk warning (flagged per skill rule):** the two enum changes and the generated-catalog deletion each fan out across Go, TS and tests. They are deliberately batched into the single "antigravity deletion + contract regeneration" commit (ADR §2.4) so the tree is never half-migrated.

### Relevant Execution Flows

| Flow (by symbol, graph unavailable) | Relevance |
|---|---|
| Onboarding complete: `POST /onboarding/complete` → probe → config write → `Agents.Defaults.ModelName` set (`gateway.go` guard) | Step 3 now also carries `auth_method`; the default model is the user's explicit pick. |
| Provider PUT: validate → `storeCredential(id+"_API_KEY")` → config write → `triggerReloadAndWaitOutcome` | DELETE is its inverse and must also wait for reload. |
| Agent turn model resolution: `pkg/agent/model_resolution.go::buildModelListResolver` → `instance.go` "provider %q not found in configured providers" | A dependent left without a model must refuse the turn with a typed error here, not crash at boot (ADR-067 MAJ-010). |
| Chat error surfacing: `LLMError` WS frame (`contracts/asyncapi.yaml`) | Carries the new "no model assigned" code. |

### Cluster Placement

Spans **providers/catalog** (Go), **gateway REST** (Go), **settings SPA** and **onboarding SPA**.

### GitHub Copilot subprocess provider (`github-copilot`) — specified here in full (cross-spec X-14)

No other spec defines it (`grep -rli copilot pkg src contracts` is empty on this tree). Policy basis: ADR-068 §2.1 — the official Copilot CLI is the vendor-sanctioned path, billed to the subscription.

- **Catalog row** (S67 data, `overrides/`): `id: github-copilot`, `name: GitHub Copilot`, `company: GitHub`, `protocol: cli`, `cli_kind: copilot`, `auth_methods: [sign_in]` (no API key — the CLI holds the login), `tier: standard`, `locality: cloud`, models listed from the CLI's advertised model set in `overrides/`.
- **Driver**: `pkg/providers/copilot_cli_provider.go::CopilotCliProvider` mirrors `CodexCliProvider` — `command: "copilot"` (overridable via the provider row's `cli_path`, like `executor.cli_path`), non-interactive prompt mode, workspace = `cfg.Home`, `Chat()` builds one prompt from the messages, runs the binary with `exec.CommandContext`, parses its machine-readable output into an `LLMResponse`, surfaces non-zero exit as `copilot cli error: <stderr>`; no tool definitions are forwarded (the CLI runs its own tools — same as `codex-cli`). The exact prompt/output flags of the Copilot CLI are **[UNVERIFIED — confirm at implementation against the installed binary; the fake-binary fixture pins whatever contract is adopted]**.
- **Sign-in detection**: Omnipus never performs or stores the login. `POST /providers/github-copilot/sign-in` → `{method: "cli_login", command: "copilot login", instructions}`; `GET …/sign-in/status` runs the CLI's own auth-status command (or reads its login state file under the CLI's home directory — **[UNVERIFIED which the shipped CLI offers]**) and maps: binary missing → `Provider.status: disconnected` (+ hint *"`copilot` not found on this machine"*); not logged in → `not_signed_in`; logged in → `signed_in` with `account_label` = the GitHub login when the CLI reports one, else absent; the CLI reporting an expired/invalid session → `expired` (re-sign-in dialog: *"Run `copilot login` again, then check"*). No JWT decoding (the CLI owns the token).
- **Probe** (`auth: sign_in`, no `api_key`): one dry-run completion through the subprocess with the chosen `model`; 400 `field=auth` "not signed in" when the status check is not `signed_in`; upstream/CLI error → 200 `success:false` with the CLI's stderr as `error`.
- **Window**: subprocess CLI rows are the only exempt class for ADR-066 (`cli_kind` present ⇒ exempt — the CLI manages its own context); `openai-chatgpt` (HTTP) is **not** exempt (X-20, decided by S66).
- **Tests**: `TestCopilotCliProvider_ParsesOutput` (unit, fake binary script), `TestCopilotCliProvider_MissingBinary`, `TestSignInStatus_Copilot` (status mapping over a fake `copilot` that prints each state), `TestProbeProvider_SignIn` gains Copilot rows. Architectural note: the shared `ProviderPicker` is the one new SPA module used by two routes; it must live under `src/components/providers/` (neither `settings/` nor `routes/`) so neither surface imports the other.

---

## User Stories & Acceptance Criteria

Actors: **Operator** (the single human installing and administering Omnipus), **Agent** (an Omnipus agent running a turn), **Vendor** (the LLM provider's API or CLI).

### User Story 1 — Antigravity is gone without a trace (Priority: P0)

An operator must never be put at risk of a Google-account suspension by Omnipus. Today a fresh install's seeded default model is `antigravity/gemini-3-flash`, the very practice Google's Antigravity terms §6 name and enforce. The provider, its OAuth config, its contract enum value, its docs and its seed are removed in one commit, with no alias, shim, migration, retired-list row or boot notice naming it afterwards.

**Why this priority**: bears on the running release (ADR §9); it precedes shipping the ADR-066 branch.

**Independent Test**: on a clean checkout after the commit, the exit-proof grep returns only historical decision records, and a config naming `antigravity` fails on the generic unknown-provider path.

**Acceptance Scenarios**:

1. **Given** the deletion commit is applied, **When** `grep -ri antigravity pkg cmd src contracts config docs` runs, **Then** the only hits are the historical decision records enumerated in ADR §2.4 "Kept deliberately".
2. **Given** a `config.json` whose provider list names `antigravity`, **When** the gateway boots, **Then** the gateway starts, the provider row shows `status: unknown-provider` with the generic text parameterised by the operator's own id (`unknown provider "antigravity"` — the id is user-supplied data, not a trace), and the **source tree and binary contain no string literal naming the provider** (the no-trace property is about source, ADR-068 §2.4 as amended).
3. **Given** the deletion commit, **When** `make verify-contracts`, `CGO_ENABLED=1 go build -tags goolm,stdjson ./...` and `npm run typecheck` run, **Then** all three exit 0.
4. **Given** a fresh install (no `config.json`), **When** the seed is generated, **Then** `pkg/config/defaults.go` contains no provider template whose `Model` is prefixed `antigravity/` and no template with `AuthMethod: "oauth"` (today L197-201 seeds `antigravity/gemini-3-flash` with `AuthMethod: oauth`), and onboarding step 3 pre-selects no model.

---

### User Story 2 — Subscription sign-in only where the vendor permits it (Priority: P0)

An operator who holds a vendor subscription wants to use it in Omnipus — but only where doing so cannot cost them the account. Omnipus offers a *Sign in* alternative to the API key for exactly the vendors whose own terms or official statements permit it (GitHub Copilot via the official SDK/CLI; OpenAI via ChatGPT login as two named providers), offers API key only for Anthropic and Google, gates xAI sign-in on xAI listing Omnipus, and removes `claude-cli`.

**Why this priority**: it is the policy the deletion in US-1 executes; shipping a disallowed path is the same class of risk.

**Independent Test**: the provider catalog served to the SPA declares `auth_methods` per provider; the picker renders Sign in only where `sign_in` is declared; Anthropic and Google rows have no sign-in control in the DOM.

**Acceptance Scenarios**:

1. **Given** the catalog, **When** the operator opens Anthropic or Google in the picker (onboarding or Settings), **Then** only the API-key path is presented and no sign-in control exists.
2. **Given** the catalog, **When** the operator opens OpenAI, **Then** two subscription providers are distinguishable: `codex-cli` (official CLI driven as a subprocess; the operator signs in inside the CLI; Omnipus never touches the token) and `openai-chatgpt` (direct HTTP with the CLI's saved token), and `openai-chatgpt` is labelled *"relies on OpenAI's stated tolerance, not its written terms"*.
3. **Given** the operator picks OpenAI sign-in and makes no further choice, **When** the configuration is saved, **Then** the persisted provider id is `codex-cli` (the subprocess is the default).
4. **Given** the catalog, **When** the operator opens GitHub Copilot, **Then** Sign in (official SDK/CLI path, billed to the subscription) and API key are both offered.
5. **Given** xAI has not listed Omnipus, **When** the operator opens xAI, **Then** only the API-key path is presented — no sign-in control and **no forward-looking copy** (MIN-010).
6. **Given** the descoping commit, **When** an agent entity or config names `claude-cli`, **Then** it fails on the generic unknown-provider path (the error text is the generic template parameterised with the operator's id), and no source file outside the allow-listed historical records contains the id `claude-cli`.
7. **Given** a sign-in provider, **When** a sign-in is detected, **Then** its row reads *"Signed in"* followed by the account label **only when `auth.json` yields one** (`tokens.account_id`, opaque; there is no e-mail in the file — MAJ-006) and the row's action reads *Manage*, not *Edit*.
9. **Given** onboarding step 3 with a sign-in provider chosen, **When** the operator clicks *Check sign-in*, **Then** the probe runs with `auth: sign_in` and no `api_key`, using the CLI's saved login (`cli_kind: codex`) or the Copilot CLI session (`cli_kind: copilot`), and *Finish* enables only after that probe passes for the chosen model (CRIT-002, X-14).
8. **Given** a signed-in provider whose vendor session has expired, **When** the provider list is loaded, **Then** the row state is *Session expired* and its action opens the re-sign-in dialog.

---

### User Story 3 — Remove a provider, with a guard (Priority: P0)

An operator who added OpenRouter to try it, and now uses Anthropic, cannot remove OpenRouter today — `GET /providers` reports it forever. They need a *Remove provider* action that deletes the configuration **and** its stored key, always confirms once, lists the agents that depend on it, requires a new default model inline when the provider backs the default, and has no Undo (an Undo would retain the deleted secret).

**Why this priority**: the capability gap and the secret-retention finding (pass-2 MAJ-011) are both release-grade.

**Independent Test**: `DELETE /providers/{id}` removes the row and the credential; a second `GET` of the credential store has no `<id>_API_KEY`; dependents are reported; deletion of the default-backing provider without a replacement is refused.

**Acceptance Scenarios**:

1. **Given** a configured provider with no dependents that does not back the default, **When** the operator clicks *Remove provider* in the config sheet footer, **Then** a confirm dialog reads *"Remove `<Display name>`? Its key will be deleted."* with *Cancel* and *Remove* and nothing else.
2. **Given** that dialog, **When** the operator confirms, **Then** the provider disappears from the list, its catalog entry is again selectable in the picker, and its stored key is gone from the credential store the moment the confirmation request succeeds.
3. **Given** a provider used by agents A and B as their primary model, **When** the operator opens *Remove provider*, **Then** the dialog lists A and B by name under *"These agents will be left without a model"*.
4. **Given** the dialog in (3), **When** the operator confirms, **Then** A and B show *"needs a model"* in the agent list, and a chat turn sent to A is refused with a typed error whose code is the one reserved for "no model assigned" — nothing is re-pointed silently.
5. **Given** the provider backs the default model, **When** the operator opens *Remove provider*, **Then** the dialog contains an inline *New default model* selector restricted to **other connected** providers, and *Remove* is disabled until a new default is chosen. A provider that backs the default can never be removed while it backs it (operator decision, resolution #4); consequently the **last** connected provider can never be removed — its dialog reads *"This is your only provider and backs the default model; connect another provider and make it the default before removing this one."* with *Remove* permanently disabled.
6. **Given** the dialog in (5) with a new default chosen, **When** the operator confirms, **Then** the default model changes to the new pair and the provider is removed, in that order, and the next turn of an agent using the default runs on the new pair without a restart.
7. **Given** any successful removal, **When** the operator looks for an Undo, **Then** there is none — no toast offers to restore, and no client or server state retains the key.
8. **Given** a removal request that names a provider not configured, **When** it is sent, **Then** the response is 404 and nothing changes.
10. **Given** the dialog data came from an earlier `GET`, **When** the operator confirms, **Then** the server recomputes dependents and `backs_default` under the config lock and the **response** (not the dialog) is authoritative (MAJ-018).
11. **Given** the provider backs the default and the only other provider is in `error` state, **When** the dialog opens, **Then** the *New default model* selector still lists that provider with its status shown; choosing it is allowed (operator's risk) — there is no dead end (MAJ-011).
12. **Given** a removal whose config write failed after dependents were cleared, **When** the operator retries, **Then** the retry re-runs all steps and succeeds; at no point does a credential survive without its provider row (CRIT-004).
9. **Given** the credential store is locked, **When** a removal is attempted, **Then** the response is 503 with the existing "credential store locked" message and the provider row is unchanged.

---

### User Story 4 — The default model is a Settings control (Priority: P0)

An operator wants to change which model new and default-routed agents use without re-running onboarding or editing JSON. A *Default model* card is the first element on Settings → Providers showing `provider · model · window · source`, with *Change* opening the model selector filtered to connected providers; the same control is reachable from the provider row. It is backed by a `PUT` and takes effect on the next turn without restart.

**Why this priority**: the only writer today is onboarding, guarded by `ModelName == ""`; US-3's inline re-pick needs this control to exist.

**Independent Test**: `PUT` the default to a different connected provider/model, send one chat turn to an agent with no model of its own, observe the turn's model in the session transcript.

**Acceptance Scenarios**:

1. **Given** Settings → Providers, **When** it renders, **Then** the first card is *Default model* showing the current provider display name, model, context window and window source (window and source per ADR-066 D9).
2. **Given** the card, **When** the operator clicks *Change*, **Then** the model selector opens listing only models of **connected** providers (status `connected` or `signed_in`).
3. **Given** a selection, **When** it is saved, **Then** `agents.defaults.default_model` holds the pair, the card updates, the provider row that backs the default shows a *Default* marker, and the next turn of a default-routed agent records that exact `provider`+`model` in its session transcript with no gateway restart — the oracle is turn-time resolution, never a config read-back (CRIT-001).
4. **Given** a `PUT` naming a provider that is not configured, **When** it is sent, **Then** the response is 400 naming the field, and the default is unchanged.
5. **Given** a `PUT` naming a model the catalog does not list for that provider (and the row is neither `custom: true` nor `locality: local`), **When** it is sent, **Then** the response is 400, and the default is unchanged; for `custom: true` or `locality: local` rows any non-empty model is accepted **without a live call** (X-13, X-17, X-22).
6. **Given** a provider row, **When** the operator opens its overflow/footer actions, **Then** *Set as default model…* is present and opens the same selector pre-filtered to that provider.

---

### User Story 5 — One shared provider picker that holds 190 providers (Priority: P1)

An operator choosing a provider — in onboarding step 3 or in Settings — sees the same picker: a stable band of 8 *Popular* tiles, then *Recently used*, then one search field over company / plan / region / alias, then *All providers* collapsed until searched or expanded — a virtualised, letter-grouped list. Unsupported (cloud-IAM) providers are visible but disabled with the reason; *Custom endpoint* is the permanent last row. Built on cmdk so arrow keys and typeahead work.

**Why this priority**: the UX review verdict is FAIL at 190 with the current flat grids; without it US-2/US-4 land on an unusable surface.

**Independent Test**: render the picker with a 190-entry catalog fixture; assert 8 Popular tiles, collapsed *All providers (N)*, that typing expands the list, that Bedrock is `aria-disabled` with a reason, and that the rendered row count in the expanded list is bounded (virtualised).

**Acceptance Scenarios**:

1. **Given** the catalog has ≥9 providers, **When** the picker opens, **Then** exactly 8 Popular tiles render in a fixed order, and below them the section *All providers (`<count>`)* is collapsed.
2. **Given** the picker is open and the search field is empty, **When** the operator types a query, **Then** the *All providers* list expands and shows only providers whose `company`, `name`, plan, region or alias matches, case-insensitively (the catalog's `company` field — S67 adds it, X-10 — is the grouping key; one tile/row per company, its plan × region variants are the catalog providers sharing that `company`); with no match an empty state *"No provider matches `<query>`"* shows with *Custom endpoint* still available.
3. **Given** the picker is open, **When** the operator expands *All providers* without a query, **Then** rows are grouped under letter headers (A–Z, then `#`) and the DOM contains at most the visible window of rows plus overscan, not all ~190.
4. **Given** an unsupported provider (e.g. Amazon Bedrock), **When** it appears in the list, **Then** it is rendered `aria-disabled="true"` with the copy mapped from S67's `unsupported_reason` enum (`cloud-iam` → *"needs request signing"*; the enum value is never shown raw — X-10), is not selectable, and is never hidden by default.
5. **Given** any state of the list, **When** the operator scrolls to the end, **Then** *Custom endpoint* is the last row; choosing it opens a panel with `id` (required; same rule as the probe `id`, must not be a catalog id or a reserved literal), `api_base` (required, SSRF-checked), `protocol` (required select: `openai-compatible | anthropic`), and the key — the saved row is recognised by S67's `Provider.custom: true`, never by a literal id (X-13).
6. **Given** the operator has previously configured Z.ai Coding Plan, **When** the picker opens, **Then** a *Recent* row for it appears between Popular and the search field.
7. **Given** focus is in the search field, **When** the operator presses ArrowDown / Enter, **Then** focus moves through tiles and rows and Enter selects, with no mouse; Home/End jump by **index** into the virtual list (the virtualiser scrolls the target row into the window, then it receives focus) — cmdk runs with `shouldFilter={false}` and the spec-owned filter (MAJ-013).
8. **Given** a provider with plans and regions, **When** it is selected, **Then** the second-level panel shows plan and region as `aria-pressed` groups, region pre-selected from the browser locale with copy *"Detected: `<Region>` — change"*.
9. **Given** a provider with `sign_in` in its auth methods, **When** its second-level panel opens, **Then** a segmented control `[ Sign in with <Vendor> ] [ API key ]` appears with *Sign in* pre-selected; for providers without `sign_in` the control is absent.
10. **Given** onboarding, **When** step 3 renders, **Then** the progress tracker still shows three steps.

---

### User Story 6 — Model selection: no pre-selection, ordered, recommended, virtualised (Priority: P1)

An operator picking a model sees the catalog list ordered by vendor group then release date descending, with at most 3 models per provider carrying a *Recommended for chat* chip (tool-calling, ≥128k window) as a hint, not a selection. In onboarding the field is labelled *"Model for your first agent"* and nothing is pre-selected. Lists above 100 items are virtualised.

**Why this priority**: operator decision (no pre-selection); OpenRouter's 359 models make the un-virtualised list a measurable jank.

**Independent Test**: render the selector with a 359-model fixture; assert order, ≤3 chips per provider, bounded DOM rows, and that the submit button is disabled until a model is chosen.

**Acceptance Scenarios**:

1. **Given** a provider with models from several vendors, **When** the selector opens, **Then** models are grouped by vendor and within each group ordered by release date, newest first; models with no release date sort last within their group, alphabetically.
2. **Given** a provider's models, **When** the list renders, **Then** at most 3 models per provider carry a *Recommended for chat* chip, each of which supports tool calling and has a context window ≥128,000 tokens.
3. **Given** onboarding step 3 after the key/sign-in is validated, **When** the model field renders, **Then** its label is *"Model for your first agent"*, its value is empty, and *Finish* is disabled.
4. **Given** a provider with more than 100 models, **When** the selector opens, **Then** rendered option rows are bounded to the visible window plus overscan.
5. **Given** a provider with ≤100 models, **When** the selector opens, **Then** all rows are rendered (no virtualisation) and keyboard navigation is unchanged from today.

---

### User Story 7 — "Check with my account" and row-level limits (Priority: P2)

An operator wants to know which catalog models *their* key can actually use. The *Refresh models* action becomes *Check with my account*: it intersects the live `/models` response with the catalog, greys out models the key cannot reach, flags catalog-unknown models as *limits unknown*, and the result is cached per key. Each configured row shows, on expand, effective limits per model and the window's source (ADR-066 D9).

**Why this priority**: it preserves the one legitimate use of the live call after the selector moves to the catalog (ADR-067 §4.3); it is not blocking.

**Independent Test**: stub `/models` to return a subset plus one unknown id; assert the greyed set and the *limits unknown* flag.

**Acceptance Scenarios**:

1. **Given** a connected provider, **When** the operator clicks *Check with my account*, **Then** `POST /api/v1/providers/{id}/entitlement` (ADR-067's route, the only name) makes one live call with that provider's key, and models in the catalog but absent from the response are shown greyed with *"not available on this key"*; the result is cached for the gateway process keyed by `SHA-256(providerID + ":" + credentialRefName)`, never the secret (MIN-007, X-03).
2. **Given** the response includes a model id the catalog lacks, **When** the result renders, **Then** that model is listed with the flag *limits unknown*.
3. **Given** the live call fails, **When** the result renders, **Then** the catalog list is unchanged and an inline warning shows the upstream error; nothing is greyed.
4. **Given** a checked provider, **When** the row is expanded, **Then** each model shows window · output · image · PDF and the window's source label from `ContextWindowSource.yaml` (`operator | live | catalog | floor`, S66-owned); a model whose projection carries `window_unknown: true` shows *"No context length"* with the link *Settings → Models → Model overrides* instead of a number (X-08).

---

### User Story 8 — A typed key is never lost on sheet close (Priority: P1)

An operator who pastes a key and accidentally presses Esc or clicks the overlay loses the key today (`handleClose` clears the draft). The draft is kept until explicit Cancel; Esc/overlay with a dirty draft keeps the sheet open and asks *"Discard key?"*.

**Why this priority**: data-loss in a form is a hard UX defect; it is one of ADR §5's eight decisions.

**Independent Test**: vitest — type a key, fire Esc, assert the sheet is open and the inline prompt is present; click *Keep editing*, assert the value survives.

**Acceptance Scenarios**:

1. **Given** the config sheet with a non-empty, unsaved key, **When** the operator presses Esc or clicks the overlay, **Then** the sheet stays open and an inline prompt *"Discard key?"* with *Discard* and *Keep editing* appears.
2. **Given** the prompt, **When** *Keep editing* is chosen, **Then** the prompt closes and the key value is unchanged.
3. **Given** the prompt, **When** *Discard* is chosen, **Then** the sheet closes and the draft is cleared from memory.
4. **Given** the sheet with an empty or already-saved key, **When** Esc is pressed, **Then** the sheet closes immediately (no prompt).
5. **Given** the sheet, **When** *Cancel* is clicked, **Then** the sheet closes and the draft is cleared without a prompt.

---

### User Story 9 — Wire contract changes, contract-first (Priority: P0)

Every byte crossing the gateway/SPA boundary for this feature is defined in `contracts/` before any Go/TS code, and the SPA and gateway use only generated types (Constraint #8). The probe request's provider enum becomes a validated free string; the build-time TS catalog file is deleted and the SPA reads the providers-catalog GET.

**Why this priority**: Constraint #8 is non-negotiable and lint-enforced; a hand-written wire type fails CI.

**Independent Test**: `make verify-contracts` exits 0 on the feature branch; `grep -rn 'providerCatalog' src` is empty; `ProbeProviderRequest.id` has no `enum` key.

**Acceptance Scenarios**:

1. **Given** the feature branch, **When** `make verify-contracts` runs, **Then** it exits 0 and `git status` shows no generated-file drift.
2. **Given** a probe request whose `id` is not in the served catalog and carries no `endpoint`, **When** it is sent, **Then** the response is 400 with an error naming the field `id` and no list of accepted values is echoed.
3. **Given** a probe request whose `id` is in the catalog, **When** it is sent, **Then** it is processed exactly as today.
4. **Given** the SPA, **When** any picker or selector needs the provider list, **Then** it reads the providers-catalog GET (cached by the server's ETag/validator) and imports nothing from `src/lib/generated/providerCatalog.ts`.

---

## Behavioral Contract

Primary flows:
- When the operator opens a provider picker, the system shows 8 Popular tiles, Recent, search, a collapsed *All providers (N)* and *Custom endpoint* last.
- When a provider's catalog entry lists `sign_in`, the system offers Sign in (pre-selected) and API key; otherwise API key only.
- When the operator confirms removal of a provider, the system deletes the configuration and its stored key, lists and leaves dependents without a model, and offers no Undo.
- When the operator changes the default model, the system persists it and the next default-routed turn uses it without restart.
- When onboarding step 3 completes, the system has a probe-validated (provider, model) pair chosen explicitly by the operator.

Error flows:
- When a removal would orphan the default model and no replacement is supplied, the system refuses with 409 and changes nothing.
- When a removal names an unconfigured provider, the system returns 404.
- When the credential store is locked, the system refuses removal with 503.
- When an agent left without a model receives a turn, the system refuses it with the typed "no model assigned" error and does not fall back to another model.
- When a probe names an unknown provider id without an endpoint override, the system returns 400.
- When "Check with my account" cannot reach the vendor, the system keeps the catalog list and shows the error inline.

Boundary conditions:
- When the catalog has fewer than 8 `tier: popular` providers, the system renders those present, never pads.
- When a removal fails mid-way, the system leaves a retryable state (a configured row, possibly with its key) and never an orphaned secret; a startup sweep removes any `<id>_API_KEY` whose provider row is gone.
- When exactly one provider is connected, the system never allows its removal (it backs the default); an install always keeps ≥1 connected provider and a default.
- When a model list has exactly 100 items, the system renders all rows; at 101 it virtualises.
- When a provider has exactly 3 recommendable models, all three carry the chip; a fourth qualifying model does not.
- When the search query is whitespace only, the system treats it as empty (list stays collapsed).
- When the browser locale cannot be mapped to a region, the system pre-selects the provider's first region and the copy reads *"Region — change"* without "Detected".

---

## Edge Cases

- Removing the **only** connected provider: **refused** (resolution #4). It necessarily backs the default, the dialog cannot offer another connected provider, so it reads *"This is your only provider and backs the default model; connect another provider and make it the default before removing this one."* and *Remove* is disabled. The server enforces the same rule: `DELETE` without a valid `new_default` → 409. There is no "No default model" state after onboarding.
- Provider that backs the default but other providers are connected: removal proceeds only with the inline new default; the server applies the new default first, then removes the provider.
- Two providers configured from the **same company** (e.g. Z.ai Coding Plan and Pay-as-you-go): removal targets one variant id; the other row and its key are untouched.
- A provider whose key is referenced by `api_key_ref` but the credential is already missing from the store: removal succeeds — the handler MUST treat `credentials.NotFoundError` from `Store.Delete` (`pkg/credentials/store.go` returns it for a missing name) as success (MAJ-019).
- Concurrent removal and PUT on the same id: both take `configMu`; the second observes the first's result; a PUT after DELETE re-creates the provider as new (api_key required). Concurrent DELETE ×2 with `new_default`: the second recomputes under the lock, finds no row, and returns 404.
- Agents that never named OpenRouter but whose slug resolved to it through `resolveFallbackProvider` rule 3 (passthrough inference — `openrouter`/`vivgrid`): they are dependents too and are listed under *"resolved through OpenRouter"* (MAJ-010).
- A dependent agent that is **locked core** (Mia/Jim/Ava/Ray): listed like any other; it, too, is left needing a model — core status does not exempt it.
- An agent whose **fallback** model points at the removed provider: the fallback entry is removed and the agent is listed under *"uses as fallback"* in the dialog (resolution #3).
- An agent that is both `needs_model` (this spec) and `degraded_reason: needs_provider` (ADR-067 MAJ-010): both flags may be true; the agent list renders the `needs_provider` copy (resolution #5).
- `openai-chatgpt` expiry source (resolution #13 refined by MAJ-006): `auth.json` carries `tokens.{access_token, refresh_token, account_id}` only — no `exp`, no e-mail. The "claim" is the `exp` of the access-token JWT decoded **without verification, for status display only** (never for an authorization decision); when the token is not a JWT or has no `exp`, the rule is `auth.json` mtime + 1 h (`ReadCodexCliCredentials`). `refresh_token` is never read.
- Onboarding: the operator changes the model after a successful probe → the probe re-runs with the new `model`; *Finish* is disabled until the re-probe passes (resolution #12).
- Sign-in provider whose CLI binary is not on PATH: the sign-in panel shows *"`codex` not found on this machine"* with the install hint; status stays `disconnected`.
- Sign-in with the vendor CLI logged in under an account the operator cannot identify: `account_label` is absent and the row reads *"Signed in"*.
- `openai-chatgpt` token past its expiry: status `expired`, row routes to re-sign-in, which instructs *"Run `codex login` again"*. Omnipus never refreshes the token (the in-app refresh path is deleted) — a session ends at expiry and needs `codex login` (MAJ-006).
- Search query containing regex metacharacters (`(`, `[`, `*`): treated literally.
- Search query in CJK for a Chinese vendor's alias (e.g. 智谱): matches via the catalog's alias list when present.
- Locale `zh-CN` / `zh-SG` → region `china`; other `zh-*` (`zh-TW`, `zh-HK`) → `intl` (different endpoint/legal entity for some vendors — MIN-003); `en-US` → `us` where the provider has a US region, else `intl`; any other → `intl`.
- Catalog GET unavailable (offline first boot, no embedded snapshot served): picker shows an error state with *Retry* and *Custom endpoint*; onboarding is not blocked from the custom path.
- Virtualised list and screen readers: the listbox exposes `aria-setsize`/`aria-posinset` per row so position is announced despite partial rendering.
- Deleting a provider while "Check with my account" for it is in flight: the in-flight result is discarded on arrival (row no longer exists).

---

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not offer a subscription sign-in for Anthropic or Google in any surface, because both vendors prohibit it in their terms and Google enforces with account suspension.
- The system must not collect, store, proxy or refresh a vendor's consumer credential where the vendor prohibits it (D13 rule 4).
- The system must not ship xAI sign-in until xAI lists Omnipus, because the AUP bans unlisted bots; no tolerance-based fallback flow.
- The system must not provide an Undo for provider removal, because an Undo retains the deleted secret for its duration.
- The system must not re-point a dependent agent to another model on provider removal, because silent re-pointing hides a change the operator did not make.
- The system must not pre-select a model in onboarding, because the operator decided the user picks.
- The system must not hide unsupported providers, because hidden options generate "where is X?" tickets; they are shown disabled with the reason.
- The system must not keep any alias, shim, migration, retired-name list or boot notice for `antigravity` or `claude-cli`, because greenfield means no trace.
- The system must not drive OpenAI's OAuth/device-code client in-app (resolution #8), because that is the token handling the policy avoids; the `pkg/auth` OpenAI device-code flow is deleted.
- The system must not echo unconfigured template providers as "disconnected" rows in `GET /providers` (resolution #16), because a row the operator never created is not theirs to manage.
- The system must not allow removal of the provider that backs the default model, nor of the last connected provider (resolution #4), because an install must always keep a default.
- The system must not keep a retired-name allow-list inside `pkg/`; the no-trace check is a CI script with a data file (MAJ-015).
- The system must not decode the `auth.json` JWT for anything but a display-only expiry estimate, because the claim is unverified (MAJ-006).
- The system must not let `DELETE /providers/{id}` or `PUT /providers/default-model` be reached unauthenticated or under dev-mode bypass (MAJ-007).
- The system must not bake the provider catalog into the SPA bundle, because the catalog refreshes daily.
- The system must not hand-write any wire-format struct/interface for the new routes (Constraint #8, lint-caught).
- The system must not poll any vendor's `/models` on a timer; the live call is on explicit operator action only.
- The system must not introduce a new Go runtime dependency for sign-in (Constraint #1); the vendor CLI is an external binary the operator installs, like `codex` today.
- The system must not render emoji in any UI chrome introduced here (Phosphor icons only).
- The system must not show raw cron or any scheduling surface here (unrelated retired surfaces stay retired).

### Machine-Verifiable Constraints

**Error Codes / Messages (REST)**:
- `DELETE /api/v1/providers/{id}` on an unconfigured id → HTTP **404**, body `{"error":"provider not configured"}`.
- `DELETE /api/v1/providers/{id}` where `backs_default=true` and the body carries no valid `new_default` → HTTP **409**, body `{"error":"provider backs the default model; supply new_default"}`; no change persisted.
- `DELETE /api/v1/providers/{id}` with `new_default.provider == id` → HTTP **400**, body `{"error":"new_default must name a different, connected provider"}`.
- `DELETE` while the credential store is locked → HTTP **503**, body `{"error":"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets"}` (existing message, verbatim).
- `DELETE` also removes `ContextSettings.model_overrides[]` rows whose `provider` equals the deleted id (S66's file; no dead rows — cross-spec Q3) and evicts the provider's entitlement cache entry (X-21).
- `DELETE` with dev-mode bypass active → HTTP **503** via `RequireNotBypass`, applied as the `requireAdminAuthz` inline wrapper inside `HandleProviders` (the shared `/api/v1/providers/` dispatcher is registered `withOptionalAuth`, so the verb is gated inline exactly as `/api/v1/channels` does); `DELETE` with no authenticated user → HTTP **401** (`withAuth` semantics, always — no pre-onboarding exception, unlike the provider PUT).
- `GET/PUT /api/v1/providers/default-model` is registered as its **own route with `adminWrap`** (`withAuth → RequireNotBypass`, precedent: `/api/v1/security/sandbox-config`), dispatched before the `/providers/` prefix handler; unauthenticated → 401; bypass → 503. Neither route requires the Spec-6 FR-12.2 re-auth token — a recorded exception (operator decision, resolution #18).
- Reserved literals `catalog`, `default-model` (and `model-capabilities` until ADR-067 removes it) are **invalid provider ids** everywhere an id is accepted: `PUT /providers/default-model` with a `ProviderUpdateRequest` body → handled by the default-model route (400 on that body's shape), `DELETE /providers/catalog` → 404, probe/onboarding-complete with such an id → 400 `field=id` (MAJ-002).
- `PUT /api/v1/providers/default-model` with an unconfigured provider → HTTP **400**, body `{"error":"provider not configured","field":"provider"}`.
- `PUT /api/v1/providers/default-model` with a model not in the served catalog for that provider (row neither `custom: true` nor `locality: local` — S67's single predicate, X-17) → HTTP **400**, `{"error":"model not in catalog for provider","field":"model"}`; for custom/local rows any non-empty `model` (≤ 256) is accepted with **no network call** (X-22).
- `PUT /api/v1/providers/default-model` when reload does not confirm → HTTP **500**, `{"error":"default model saved but config reload failed: <reason>"}`.
- `POST /onboarding/probe-provider` with `id` not in the catalog and without **both** `api_base` and `protocol` → HTTP **400**, `{"error":"unknown provider \"<id>\"","field":"id"}` (S67 US-10.AC3 vocabulary; the id is echoed, never a list of accepted ids) (X-04).
- `POST /onboarding/probe-provider` with `api_base` → the same `ssrfChecker.CheckURL` gate as PUT; internal-CIDR base → HTTP **422** `provider endpoint not allowed (SSRF guard)` (MIN-006).
- `POST /onboarding/probe-provider` with `auth: sign_in` and an `api_key` → **400**; with `auth: api_key` and no `api_key` → **400** naming `api_key`; with `auth: sign_in` and no saved CLI login / CLI session → **400** `{"error":"not signed in","field":"auth"}`; with `auth: sign_in` and a saved login → the probe runs one completion through that path (CRIT-002).
- `POST /onboarding/complete` with `auth_method: "api_key"` and no `api_key` → HTTP **400** naming `api_key`; with `auth_method: "sign_in"` and an `api_key` present → HTTP **400** (`"api_key not allowed with sign_in"`).
- `POST /api/v1/providers/{id}/sign-in` on a provider whose catalog `auth_methods` lacks `sign_in` → HTTP **400**, `{"error":"provider does not support sign-in"}`.

**WS / LLMError**:
- A turn sent to an agent with no model assigned emits `LLMError` with `code: "model_unassigned"`, `attribution: "config"`, message `"This agent has no model. Pick one in the agent's settings."` and the turn ends with no provider call. **Precedence (MAJ-008, X-02, X-09):** this is an agent-level gate evaluated before any turn starts, in the order `needs_provider` (S67's code, attribution `config`, added by S67 to all four `LLMError` copies) → `model_unassigned` (this spec's code, attribution `config`, copy above; S67 writes it into the same four copies in its coordinated commit — X-01) → S66's context-window refusal (third, S66 owns its code); it therefore never competes with turn-time codes (`model_unavailable`, `provider_*`). `agent_not_configured` is not reused because its copy and attribution describe workspace membership. The end-to-end gate-order test lives here: `TestTurn_PreTurnGateOrder` (cross-spec Q6).

**Performance Bounds**:
- Picker open → first paint with a 190-entry catalog is **recorded** via `performance.mark` as a metric (p95 target 100 ms), not a pass/fail gate (MIN-004); the gate is the DOM-count bound below.
- Expanded *All providers* list: with the test container fixed at 480 px and 40 px rows (12 visible), overscan 5 → rendered row elements ≤ **22** (MIN-012); Playwright asserts the same bound with its real viewport computed as `floor(height/40) + 10`.
- Model selector with 359 models: same fixture geometry → rendered option elements ≤ **22** in vitest; frame time is a recorded metric in Playwright, not a gate.
- `DELETE /providers/{id}` p95 ≤ reload-wait bound already used by PUT (the poll window of `triggerReloadAndWaitOutcome`) + 200 ms.
- Providers-catalog GET: the SPA follows ADR-067 A-1 — re-validates with `If-None-Match` on Settings open and every 15 min; the assertion is **at most one 200 per ETag value** (304s are expected requests) (MAJ-004). Entitlement results are cached per key for the gateway process.

**Data Constraints**:
- `ProbeProviderRequest` — **ONE shape, file owned by S67, listed verbatim in both specs (X-04):** `id` (string `1..64`, no enum, no pattern — valid iff in the served catalog or accompanied by `api_base` + `protocol`), `auth` (required, `api_key | sign_in`), `api_key` (optional; required iff `auth = api_key`, runtime rule in the description), `model` (optional `1..256`; probe uses it verbatim, else the provider's first Recommended catalog model), `api_base` (optional), `protocol` (optional, `openai-compatible | anthropic`; required with `api_base`). This spec's custom-row probe sends `api_base` + `protocol`; its sign-in probe sends `auth: sign_in`; the inbound copy `pkg/gateway/inboundschemas/ProbeProviderRequest.yaml` is edited in the same S67 commit.
- `ProbeProviderResponse` gains `probed_model: string` (the model actually exercised) so the SPA can tie the result to the pick (MAJ-014).
- `GET /providers` returns **configured providers only** — no template/"disconnected" rows for unconfigured catalog entries (resolution #16).
- `Provider.status` enum — **one enumeration shared verbatim with the ADR-067 spec** (MAJ-001): `connected | disconnected | error | unknown-provider | signed_in | expired` (ADR-067 defines `unknown-provider`). It stays **exactly six**: the per-model "no context length" state is `window_unknown` on the model projection (X-08), never a seventh status.
- `Provider.auth_method` enum: `api_key | sign_in`; `Provider.account_label`: string, `maxLength 128`, present only when `status = signed_in | expired`.
- `Provider.dependents`: array of `ProviderDependent` (named schema `ProviderDependent.yaml`: `{id: string, name: string, role: "primary" | "fallback" | "passthrough" | "recap" | "image" | "voice"}`), always present (empty array when none); `Provider.backs_default`: boolean, always present. Both are **advisory** on `GET`; the server recomputes them under the config lock on `DELETE` (MAJ-018).
- `Provider.updated_at`: RFC 3339, set on every PUT (MAJ-015) — the *Recent* ordering key. `Provider.custom: true` (S67) marks an operator-defined row; `Provider.locality: local | cloud` (S67) is the only local/cloud distinction the UI uses (X-13, X-17); `Provider.company` (S67) is the picker's grouping key (X-10); `Provider.cli_kind` (S67) selects the subprocess driver (X-14).
- `ProviderDeleteRequest.new_default`: optional object `{provider: string(1..64), model: string(1..256)}`.
- `ProviderDeleteResponse`: `{deleted: true, dependents: [...], default_changed: boolean, new_default?: {provider, model}}`.
- `DefaultModel.yaml` (GET body; also the persisted shape of `agents.defaults.default_model`): `{provider: string, model: string, context_window?: integer ≥ 0, window_source?: $ref ContextWindowSource.yaml, window_unknown?: boolean}` — `ContextWindowSource.yaml` is defined once by S66 (X-06) and `$ref`'d here, never an inline enum; `context_window`/`window_source` are produced by S66's exported `ResolveWindow(provider, model)` (rungs without the per-agent override, X-07) which the default-model GET calls; `window_unknown: true` is S66's per-model "endpoint reported no context length" projection (X-08), rendered as *"No context length — set it under Settings → Models → Model overrides → `<provider>` / `<model>`"*; exempt subprocess-CLI rows return `context_window: 0` with `window_source` absent (S66 semantics); `DefaultModelUpdateRequest.yaml` (PUT body): exactly `{provider, model}` (`additionalProperties: false`). `agents.defaults.model_name` does not exist in the config schema after this change (CRIT-001).
- `OnboardingCompleteRequest.provider`: becomes an **inline `oneOf` + `discriminator: auth_method`** in `openapi.yaml` over `#/components/schemas/OnboardingProviderApiKey` (`api_key` required) and `OnboardingProviderSignIn` (no `api_key` allowed) — the ADR-034 mechanism for conditional requirements (MAJ-014).
- `SignInStartResponse.yaml`: `{method: "cli_login", instructions: string, command: string}` — no device-code fields; there is no producer for them (MIN-005).
- `SignInStatus.yaml`: `{state: "not_signed_in" | "signed_in" | "expired", account_label?: string, expires_at?: date-time}` — no `pending` (MIN-005); `account_label` is `tokens.account_id` when present (MAJ-006).
- `EntitlementResponse.yaml` (ADR-067's `POST /providers/{id}/entitlement`): `{models: [{id, entitled: boolean, limits: "known" | "unknown"}], checked_at: date-time, cached: boolean}` (MAJ-014).
- Popular set: the providers whose catalog `tier` is `popular`, rendered in **catalog order** (ADR-067 owns membership and order; pass-2 MIN-008 pins the count at 8). **Never a SPA constant** (resolution #1); the SPA reads `tier` from the catalog GET. No expected order is asserted here (MIN-001).
- Recommended chip: ≤ 3 per provider; eligibility = catalog `tool_call = true AND context_window ≥ 128000 AND status = active` (ADR-067 enum `active | retired`; MIN-002); tie-break = `release_date` desc, then id asc.
- `Agent.needs_model`: boolean, always present, derived = primary `model` empty OR its `provider` not configured. When ADR-067's `degraded_reason: needs_provider` is also set, the list copy shows *needs a provider* (resolution #5).
- Sign-in `expired` rule: JWT `exp` of `tokens.access_token`, decoded unverified for display only, when present; else `auth.json` mtime > 1 h (resolution #13, MAJ-006).
- Dependents definition (MAJ-010): every reference that would stop resolving after removal — agent primary `model`/`provider`; agent `fallback_models[].provider`; agents whose primary or fallback slug resolves to the provider only through `resolveFallbackProvider` rule 3 (passthrough: `openrouter`/`vivgrid`); `agents.defaults.default_model`; `agents.defaults.image_model`; `agents.defaults.recap_model` and `recap_fallback_models[]`; `voice.model_name`. Heartbeats and calendar triggers do not pin a model (they run as their agent). The enumeration is a test fixture (`TestProviderDependents_EnumeratesEveryModelField`) that fails if a new `Model`-named config field appears without a row.
- Region inference map (MIN-003): `zh-CN`, `zh-SG` → `china`; other `zh-*` → `intl`; `en-US` → `us` when offered, else `intl`; everything else → `intl`.
- Region inference map: `zh-*` → `china`; `en-US` → `us` if offered else `intl`; everything else → `intl`.
- Persisted provider ids for the OpenAI subscription paths: exactly `codex-cli` (subprocess) and `openai-chatgpt` (HTTP); no other spelling is accepted anywhere (`codexcli` removed).
- Credential name deleted on removal: exactly `<providerID>_API_KEY` (the name `storeCredential` writes today).

**Accessibility (WCAG 2.2 AA, machine-checked by `tests/e2e/accessibility.spec.ts` axe run + targeted assertions)**:
- Every interactive element in the picker, dialog and sheet is reachable and operable by keyboard (2.1.1); the cmdk list supports ArrowUp/Down/Home/End/Enter/Esc.
- Focus is visible on every tile, row and button with a ≥ 3:1 contrast focus ring (2.4.7, 2.4.11) — **Playwright assertion**, not axe: for each element in a fixed selector list, `getComputedStyle(el).outlineColor`/`boxShadow` after `el.focus()` contrasts ≥ 3:1 against its background (computed with the same relative-luminance helper the SPA's contrast test uses).
- Target size ≥ 24 × 24 CSS px for every tile, row, segmented-control option and footer button (2.5.8) — **Playwright assertion**: `getBoundingClientRect()` on a fixed selector list (axe's `target-size` is best-practice only and is not relied on).
- Text contrast ≥ 4.5:1 for body text and ≥ 3:1 for large text/UI components in dark and light themes (1.4.3, 1.4.11) — includes the greyed "not available on this key" rows (greyed text still ≥ 4.5:1; disabled rows are exempt under 1.4.3 but carry the reason in an adjacent non-disabled text node).
- Confirm dialog: `role="alertdialog"`, `aria-labelledby` the title, `aria-describedby` the consequence sentence (axe-checkable); focus lands on *Cancel* on open and Esc = Cancel (2.4.3, 3.2.2) — **Playwright assertions**: `document.activeElement` equals the Cancel button after open; `page.keyboard.press('Escape')` closes the dialog with no `DELETE` request observed.
- Probe/finish/deletion errors render in a `role="alert"` region with `aria-live="assertive"` (4.1.3).
- Segmented control and plan/region groups use `role="group"` + `aria-label` and `aria-pressed` on options (4.1.2).
- Virtualised listboxes set `aria-setsize` and `aria-posinset` on each rendered option (4.1.2).
- No information conveyed by colour alone: row states carry an icon + text, the Recommended chip has text (1.4.1).
- The "Discard key?" prompt does not move focus out of the sheet (3.2.1) — **Playwright assertion**: `document.activeElement` stays inside the sheet element after Esc.
- Keyboard operability (2.1.1): **Playwright** — from the search field, `ArrowDown`×3 + `Enter` selects the third Popular tile; `End` focuses the *Custom endpoint* row; `Home` returns to the first tile; `Escape` closes the picker.
- axe-core run on onboarding step 3, Settings → Providers (with sheet and dialog open) reports **0** violations of impact `serious` or `critical`.

### Conservative Type Design

No new nominal Go types beyond the generated wire types. Provider ids, model ids and credential names stay `string`; auth method and status stay the generated enum types. The SPA adds one TS discriminated union for picker items (`tile | recent | row | custom`) because rendering differs per kind; nothing else.

---

## Prerequisites

- **Hardware / OS**: any platform Omnipus builds for; sign-in providers additionally need the vendor CLI binary for that OS.
- **Required runtimes**: Go 1.26.4 (go.mod), Node 20+; `codex` CLI on PATH for `codex-cli`/`openai-chatgpt`; GitHub Copilot CLI on PATH for `github-copilot` (resolution #7).
- **Required services**: none beyond the gateway.
- **Network assumptions**: outbound HTTPS to the chosen vendor for probe/"Check with my account"; the providers-catalog GET is served by the gateway from ADR-067's embedded/pulled snapshot, so the picker works offline.
- **Accounts / credentials**: an API key or a vendor subscription the operator already holds; credential store unlocked (ADR-004).

## Development Setup

1. `cd /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-context-budget`
2. `export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH && npm ci`
3. `make gen-contracts` after every `contracts/` edit; commit spec + generated artefacts atomically.
4. `npm run build && rm -rf pkg/gateway/spa && cp -r dist/spa/* pkg/gateway/spa/ && CGO_ENABLED=0 go build -tags goolm,stdjson -o /tmp/omnipus ./cmd/omnipus/`
5. `export OMNIPUS_HOME=/tmp/omnipus-e2e-test && rm -rf "$OMNIPUS_HOME" && mkdir -p "$OMNIPUS_HOME" && OMNIPUS_BEARER_TOKEN="" /tmp/omnipus gateway --allow-empty`

**Expected first-run behaviour**: onboarding step 3 shows the shared picker with 8 Popular tiles and no pre-selected model.

**Common first-run failures**: port 5000 in use (set `gateway.port`); building without `-tags goolm,stdjson` fails on `pkg/channels/matrix` (missing tag, not a bug); stale `pkg/gateway/spa/` serves the old picker.

## Tech Stack

| Category | Choice | Version / Pin | Source |
|---|---|---|---|
| Language | Go | 1.26.4 (go.mod) | CLAUDE.md Tech Stack |
| Frontend | TypeScript, React 19, Vite 6, shadcn/Radix, Tailwind v4 | package.json | CLAUDE.md |
| Picker primitive | cmdk `Command` | `^1.1.1` | package.json L90 |
| Virtualisation | `@tanstack/react-virtual` | `^3.14.6` | package.json L85 (already a dependency) |
| Server state | TanStack Query | package.json | CLAUDE.md |
| Icons | Phosphor | package.json | CLAUDE.md UI rules |
| Datastore | `config.json` + `credentials.json` (AES-256-GCM) | — | ADR-004 |
| Contracts | OpenAPI 3 + AsyncAPI, oapi-codegen, openapi-typescript, openapi-zod-client | `scripts/gen-contracts.sh` | Constraint #8 |
| Tests | Go `go test -tags goolm,stdjson`, vitest, Playwright `^1.61.1`, axe-core | package.json | Quality Gates |

## Deployment / Runtime

- **Target environment**: single Omnipus binary, operator's machine or VPS.
- **Online / offline**: picker and selector are offline-capable (catalog served locally); probe, sign-in check and "Check with my account" need the vendor reachable.
- **Resource limits**: no new background goroutines; no timer-driven vendor calls.
- **Start / stop**: unchanged (`omnipus gateway`).
- **Health check**: `GET /api/v1/providers` lists rows with the new status vocabulary; `GET /api/v1/providers/default-model` returns the current pair.
- **Logs**: deletion logs one INFO line `rest: provider removed` with `provider_id`, `dependents` count and `default_changed`; never the key.

---

## Integration Boundaries

### Vendor API (API-key providers)

- **Data in**: probe request (one small completion), `/models` on "Check with my account".
- **Data out**: probe outcome (`valid | invalid_key | no_credit | unreachable | restricted`), model id list.
- **Contract**: OpenAI-compatible HTTP or Anthropic Messages, per the catalog protocol (ADR-067 §4.1).
- **On failure**: probe → existing `ProviderValidation` outcomes; "Check with my account" → inline warning, catalog list unchanged.
- **Development**: mocked with `httptest` servers in Go tests and MSW/`vi.mock` in vitest; Playwright uses the existing stub provider fixtures.

### Vendor CLI (`codex`, Copilot CLI)

- **Data in**: subprocess invocation with the prompt (`codex-cli`, `github-copilot`), or a read of the CLI's saved credential file (`openai-chatgpt`: `tokens.access_token`, `tokens.account_id` only). For the sign-in probe: one dry-run completion through the same path (the fake-`codex`-on-PATH fixture of `codex_cli_provider_integration_test.go` supports it).
- **Data out**: completion text / JSONL events; for status, presence and age of the saved login.
- **Contract**: the vendor's own CLI interface; Omnipus never writes the credential file.
- **On failure**: binary missing → `disconnected` with hint; login missing/expired → `not_signed_in`/`expired`; subprocess error → existing `codex cli error: …` path surfaced as `LLMError provider_rejected`.
- **Development**: a fake `codex` shell script on PATH in Go integration tests (the pattern `codex_cli_provider_integration_test.go` already uses); SPA mocks the sign-in REST routes.

### Providers catalog (ADR-067)

- **Data in**: none from this feature.
- **Data out**: the catalog document via the providers-catalog GET.
- **Contract**: ADR-067 §5 schema 2.0.0, which (resolution #1, instructed into the ADR-067 spec) carries exactly the fields this spec consumes: per provider `id, name, tier (popular|standard|unsupported), unsupported_reason, auth_methods[] (api_key|sign_in), aliases[]` (search only); per model `id, name, release_date, tool_call, context_window, max_output_tokens, input_modalities, status`. Plans/regions/protocol are ADR-067 provider-table data already present. This spec adds no field anywhere else.
- **On failure**: SPA shows picker error state with Retry and Custom endpoint; server already falls back to the embedded snapshot.
- **Development**: a 190-entry JSON fixture under `src/test/fixtures/providers-catalog.json` generated once from the real snapshot.

### Credential store (`pkg/credentials`)

- **Data in**: `Delete("<id>_API_KEY")` on confirmed removal.
- **Data out**: nil or locked-store error.
- **On failure**: locked → 503 before any change; `Store.Delete` returning `credentials.NotFoundError` → the handler treats it as success (MAJ-019); any other error → 500 with `deleted: false`, state retryable.
- **Development**: real store with a temp `OMNIPUS_HOME` and `OMNIPUS_MASTER_KEY` in Go tests.

---

## BDD Scenarios

### Feature: ADR-068 — providers policy, deletion, default model, picker

#### Background

- **Given** the gateway is running with an unlocked credential store
- **And** the providers-catalog GET serves the 190-entry fixture with the 8 Popular ids pinned

---

#### Scenario: Antigravity leaves no trace in the source tree

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the deletion commit is checked out
- **When** `grep -ril antigravity pkg cmd src contracts config docs` is run
- **Then** every path returned is one of the historical decision records listed in ADR-068 §2.4 "Kept deliberately"
- **And** `docs/ANTIGRAVITY_USAGE.md` does not exist

#### Scenario: Config naming a removed provider fails generically

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Error Path

- **Given** `config.json` contains a provider entry with `provider: "antigravity"`
- **When** the gateway boots
- **Then** the gateway reaches the listening state
- **And** `GET /api/v1/providers` shows that row with `status: unknown-provider` and the generic text `unknown provider "antigravity"` (the id is the operator's own data)
- **And** `strings $(which omnipus) | grep -c antigravity` is 0 — the binary carries no literal naming the provider (CRIT-003)

#### Scenario: Build and contract gates pass with the files gone

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the deletion commit
- **When** `make verify-contracts`, the tagged Go build and `npm run typecheck` are run in sequence with exit codes captured without a pipe
- **Then** each reports exit 0

#### Scenario: Fresh install seeds no default model

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Edge Case

- **Given** the seed source `pkg/config/defaults.go`
- **When** it is parsed by the test
- **Then** no `ModelConfig` template has a `Model` prefixed `antigravity/` or `claude-cli/` and none has `AuthMethod` set at all (the field is deleted)
- **And** `Agents.Defaults.DefaultModel` is the zero value
- **And** after onboarding step 2 on an empty `OMNIPUS_HOME`, `GET /api/v1/providers/default-model` returns 404 `{"error":"no default model"}` and step 3 renders the model field empty

---

#### Scenario Outline: Auth methods offered per provider

**Traces to**: User Story 2, Acceptance Scenarios 1, 4, 5
**Category**: Happy Path

- **Given** the picker is open
- **When** the operator selects `<provider>`
- **Then** the second-level panel shows `<controls>`
- **And** the copy `<copy>` is `<present>`

**Examples**:

| provider | controls | copy | present |
|---|---|---|---|
| anthropic | API key field only | Sign in | absent |
| google | API key field only | Sign in | absent |
| openai | segmented `[Sign in with OpenAI][API key]`, Sign in selected | relies on OpenAI's stated tolerance | present on the `openai-chatgpt` option only |
| github-copilot | segmented `[Sign in with GitHub][API key]`, Sign in selected | billed to your Copilot subscription | present |
| xai | API key field only | Sign in with xAI arrives once xAI lists Omnipus | present |

#### Scenario: OpenAI sign-in offers two named providers with the subprocess as default

**Traces to**: User Story 2, Acceptance Scenarios 2, 3
**Category**: Happy Path

- **Given** the operator selected OpenAI and the *Sign in* segment
- **When** the panel renders
- **Then** two radio options are shown: *Codex CLI (official CLI, signs in on this machine)* with value `codex-cli`, pre-selected, and *ChatGPT login (direct)* with value `openai-chatgpt` carrying the label *"relies on OpenAI's stated tolerance, not its written terms"*
- **And** saving without changing the radio persists provider id `codex-cli`

#### Scenario: `claude-cli` is an unknown provider

**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Error Path

- **Given** an agent entity with `provider: "claude-cli"`
- **When** a turn is sent to that agent
- **Then** the turn is refused with ADR-067's `needs_provider` typed error, whose text is the generic template parameterised with the operator's id
- **And** `CreateProviderFromConfig({provider: "claude-cli"})` returns `errors.Is(err, catalog.ErrUnknownProvider)` with no hint text (S67's surface; `knownProtocols`/`IsKnownProtocol` no longer exist — X-15); the "no vendor case in the switch" assertion is S67 T24's AST scan, not duplicated here (X-33)

#### Scenario: GET /providers returns configured providers only

**Traces to**: User Story 3, Acceptance Scenario 8
**Category**: Edge Case

- **Given** the seed templates in `config.json` name ten providers and the operator has configured only `openrouter`
- **When** `GET /api/v1/providers` is called
- **Then** the response contains exactly one row, `openrouter`
- **And** no row with `status: disconnected` exists for any unconfigured catalog entry
- **And** `DELETE /api/v1/providers/groq` returns 404

#### Scenario: Signed-in row shows account and Manage

**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Happy Path

- **Given** `GET /api/v1/providers` returns `codex-cli` with `status: signed_in` and `account_label: "acct_7f3a"` (the opaque `tokens.account_id`)
- **When** Settings → Providers renders
- **Then** the row reads *Signed in · acct_7f3a*; with `account_label` absent it reads *Signed in*
- **And** the row action button text is *Manage*
- **And** after the status call, `auth.json`'s mtime and bytes are unchanged (FR-007: read-only)

#### Scenario: Expired session routes to re-sign-in

**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Error Path

- **Given** `GET /api/v1/providers` returns `openai-chatgpt` with `status: expired`
- **When** the operator clicks the row action
- **Then** the re-sign-in dialog opens with the instruction *"Run `codex login` again, then check"*
- **And** pressing *Check* calls `GET /api/v1/providers/openai-chatgpt/sign-in/status`
- **And** no refresh request leaves the gateway — Omnipus never refreshes the token (MAJ-006)

#### Scenario: Sign-in refused for a key-only provider

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Error Path

- **Given** the catalog lists `anthropic` with `auth_methods: [api_key]`
- **When** `POST /api/v1/providers/anthropic/sign-in` is sent
- **Then** the response is HTTP 400 with `{"error":"provider does not support sign-in"}`

---

#### Scenario: Remove an unused provider after one confirmation

**Traces to**: User Story 3, Acceptance Scenarios 1, 2
**Category**: Happy Path

- **Given** `openrouter` is configured, has `dependents: []` and `backs_default: false`
- **When** the operator clicks *Remove provider* and then *Remove* in the dialog titled *"Remove OpenRouter? Its key will be deleted."*
- **Then** `DELETE /api/v1/providers/openrouter` returns HTTP 200 with `{"deleted":true,"dependents":[],"default_changed":false}`
- **And** the row is gone from the list
- **And** the credential store has no entry `openrouter_API_KEY` (also when the key was pre-deleted before the request — `NotFoundError` is success)
- **And** an audit entry `provider.deleted` exists with `details.credential_ref = "openrouter_API_KEY"` and no key material
- **And** OpenRouter is selectable again in the picker

#### Scenario: Dependents are listed and left without a model

**Traces to**: User Story 3, Acceptance Scenarios 3, 4
**Category**: Alternate Path

- **Given** agents "Ava" and "Scout" have `provider: "openrouter"` as primary
- **When** the operator opens *Remove provider* for OpenRouter
- **Then** the dialog lists *Ava* and *Scout* under *"These agents will be left without a model"*
- **And** after confirming, `GET /api/v1/agents` shows both with `needs_model: true` and empty `model`/`provider`
- **And** the agent list renders *needs a model* on both rows (or *needs a provider* when ADR-067's `degraded_reason: needs_provider` is also set)

#### Scenario: Turn to an agent without a model is refused, not re-pointed

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Error Path

- **Given** agent "Ava" has `needs_model: true`
- **When** a chat message is sent to Ava
- **Then** an `LLMError` frame with `code: "model_unassigned"` and `attribution: "config"` is emitted
- **And** no request reaches any provider
- **And** Ava's stored model is unchanged (still empty)

#### Scenario: Default-backing provider requires an inline new default

**Traces to**: User Story 3, Acceptance Scenarios 5, 6
**Category**: Alternate Path

- **Given** the default model is `openrouter · z-ai/glm-5.2` and `anthropic` is also connected
- **When** the operator opens *Remove provider* for OpenRouter
- **Then** the dialog contains a *New default model* selector listing only Anthropic models
- **And** *Remove* is disabled until a model is chosen
- **And** after choosing `anthropic · claude-sonnet-4.6` and confirming, `GET /api/v1/providers/default-model` returns that pair and OpenRouter is gone

#### Scenario: DELETE without replacement on the default-backing provider is refused

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path

- **Given** `openrouter` has `backs_default: true`
- **When** `DELETE /api/v1/providers/openrouter` is sent with an empty body
- **Then** the response is HTTP 409 `{"error":"provider backs the default model; supply new_default"}`
- **And** the provider, its key and the default are unchanged

#### Scenario: No Undo exists after removal

**Traces to**: User Story 3, Acceptance Scenario 7
**Category**: Edge Case

- **Given** a provider was just removed
- **When** the SPA state and the DOM are inspected for 10 seconds
- **Then** no element with text *Undo* is rendered and no toast with an action button appears
- **And** no network request other than the `DELETE` and the follow-up `GET /providers` is observed for 10 s (no restore `PUT`)

#### Scenario: DELETE partial failure leaves no orphaned secret and a retry succeeds

**Traces to**: User Story 3, Acceptance Scenario 12
**Category**: Error Path

- **Given** `openrouter` is configured with agents "Ava" and "Scout" as dependents, and the config write (step 2) is injected to fail once
- **When** `DELETE /api/v1/providers/openrouter` is sent
- **Then** the response is HTTP 500 `{"deleted":false,"error":"…"}`
- **And** the provider row and `openrouter_API_KEY` both still exist (step 3 never ran)
- **And** Ava and Scout are already `needs_model: true` (step 1 ran; it is idempotent)
- **And** a second identical `DELETE` returns HTTP 200, the row and the key are gone, and the audit entry exists exactly once per completed run

#### Scenario: Startup sweep removes an orphaned credential

**Traces to**: User Story 3, Acceptance Scenario 12
**Category**: Edge Case

- **Given** `credentials.json` holds `groq_API_KEY` and `config.json` has no `groq` provider row
- **When** the gateway boots
- **Then** `groq_API_KEY` is deleted from the store with one INFO line and an audit entry `provider.credential_swept`
- **And** a `<name>` that does not match the `<id>_API_KEY` pattern is left untouched

#### Scenario: Server recomputes dependents under the lock; the response is authoritative

**Traces to**: User Story 3, Acceptance Scenario 10
**Category**: Edge Case

- **Given** the dialog was opened from a `GET` showing `dependents: []`
- **And** meanwhile `PUT /api/v1/agents/ava` re-pointed Ava to OpenRouter
- **When** the operator confirms
- **Then** the `DELETE` response lists Ava under `dependents` and Ava is `needs_model: true`

#### Scenario: Other provider in error state still offered as new default

**Traces to**: User Story 3, Acceptance Scenario 11
**Category**: Edge Case

- **Given** `openrouter` backs the default and the only other provider `anthropic` has `status: error`
- **When** the operator opens *Remove provider* for OpenRouter
- **Then** the *New default model* selector lists Anthropic's models with the row state *Error* shown beside the provider name
- **And** choosing one enables *Remove*; confirming succeeds and `DELETE` accepts `new_default.provider = anthropic`

#### Scenario: Reserved literals are never provider ids

**Traces to**: User Story 9, Acceptance Scenario 2
**Category**: Error Path

- **Given** the gateway
- **When** `DELETE /api/v1/providers/catalog`, `DELETE /api/v1/providers/default-model`, `PUT /api/v1/providers/catalog` with a `ProviderUpdateRequest`, and `POST /onboarding/probe-provider` with `id: "default-model"` are sent
- **Then** the responses are 404, 405 (the default-model route accepts GET/PUT only), 400 `field=id`, and 400 `field=id` respectively
- **And** `PUT /api/v1/providers/default-model` with a `ProviderUpdateRequest` body returns 400 on the body shape, never creates a provider named `default-model`

#### Scenario: DELETE and default PUT require an authenticated admin

**Traces to**: User Story 3, Acceptance Scenario 9
**Category**: Error Path

- **Given** onboarding is complete
- **When** `DELETE /api/v1/providers/openrouter` and `PUT /api/v1/providers/default-model` are sent without a Bearer token
- **Then** both return 401
- **And** with `gateway.dev_mode_bypass = true` both return 503

#### Scenario: Removing an unconfigured provider returns 404

**Traces to**: User Story 3, Acceptance Scenario 8
**Category**: Error Path

- **Given** no provider `groq` is configured
- **When** `DELETE /api/v1/providers/groq` is sent
- **Then** the response is HTTP 404 `{"error":"provider not configured"}`

#### Scenario: Removal refused while the credential store is locked

**Traces to**: User Story 3, Acceptance Scenario 9
**Category**: Error Path

- **Given** the credential store is locked
- **When** `DELETE /api/v1/providers/openrouter` is sent
- **Then** the response is HTTP 503 with the existing "credential store locked" message
- **And** `config.json` is byte-identical to before

#### Scenario: Removing the only provider is refused

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Edge Case

- **Given** exactly one provider is connected and it backs the default
- **When** the operator opens *Remove provider*
- **Then** the dialog reads *"This is your only provider and backs the default model; connect another provider and make it the default before removing this one."*
- **And** *Remove* is disabled and no *New default model* selector is offered (there is no other connected provider)
- **And** a direct `DELETE /api/v1/providers/{id}` with no body returns HTTP 409 and the provider, its key and the default are unchanged

#### Scenario: `claude-cli` leaves no trace in the source tree

**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Happy Path

- **Given** the descoping commit is checked out
- **When** `grep -ril 'claude-cli' pkg cmd src contracts config docs` is run
- **Then** every path returned is a historical decision record (the same allow-list rule as `antigravity`); `pkg/providers/claude_cli_provider.go` and its tests do not exist
- **And** the grep target is the id `claude-cli`, never the word "claude" (a model family name)

#### Scenario: OpenAI device-code flow leaves no trace

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the feature branch
- **When** `grep -rn 'RequestDeviceCode\|PollDeviceCodeOnce\|OpenAIOAuthConfig\|createCodexTokenSource' pkg cmd` is run
- **Then** it returns nothing (and likewise for `createCodexAuthProvider`, `createClaudeAuthProvider`, `createClaudeTokenSource`)
- **And** `POST /api/v1/providers/openai-chatgpt/sign-in` returns `{method: "cli_login", command: "codex login", instructions: …}` — the schema has no other `method` value
- **And** a config row with `auth_method: "oauth"` fails on the generic unknown-auth-method path

#### Scenario: Fallback references are removed and listed

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Edge Case

- **Given** agent "Jim" lists an OpenRouter model only in `fallback_models`
- **When** the operator opens *Remove provider* for OpenRouter
- **Then** Jim is listed under *"uses as fallback"*
- **And** after confirming, Jim's `fallback_models` no longer contains that entry and `needs_model` is false

#### Scenario: Passthrough-resolved agents are dependents

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Edge Case

- **Given** agent "Ray" has `model: "google/gemini-2.5-flash"` with empty `provider`, which today resolves to OpenRouter only through `resolveFallbackProvider` rule 3
- **And** `agents.defaults.recap_fallback_models` names an OpenRouter model
- **When** the operator opens *Remove provider* for OpenRouter
- **Then** Ray is listed under *"resolved through OpenRouter"* with role `passthrough`, and the recap fallback is listed with role `recap`
- **And** after confirming, Ray is `needs_model: true` and the recap fallback entry is removed

---

#### Scenario: Default model card shows provider, model, window, source

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `GET /api/v1/providers/default-model` returns `{provider:"openrouter", model:"z-ai/glm-5.2", context_window:1048576, window_source:"catalog"}`
- **When** Settings → Providers renders
- **Then** the first card is titled *Default model* and reads *OpenRouter · z-ai/glm-5.2* and *1,048,576 · catalog*
- **And** the OpenRouter row carries a *Default* marker

#### Scenario: Change default model takes effect on the next turn

**Traces to**: User Story 4, Acceptance Scenarios 2, 3
**Category**: Happy Path

- **Given** `anthropic` and `openrouter` are connected and the default is OpenRouter
- **When** the operator clicks *Change*, picks `anthropic · claude-sonnet-4.6` and saves
- **Then** `PUT /api/v1/providers/default-model` returns HTTP 200 with the new pair, `config.json` holds `agents.defaults.default_model = {provider:"anthropic", model:"claude-sonnet-4.6"}` and no `model_name` key
- **And** the selector that opened listed only connected providers' models
- **And** a turn sent to a default-routed agent without restarting the gateway records `anthropic`/`claude-sonnet-4.6` in its session transcript (`pkg/session` day-partition line) — the test asserts the transcript, never the config read-back
- **And** an audit entry `provider.default_model.changed` exists with the old and new pairs

#### Scenario Outline: Default-model PUT validation

**Traces to**: User Story 4, Acceptance Scenarios 4, 5
**Category**: Error Path

- **Given** providers `openrouter` (connected) and `my-proxy` (connected, `custom: true`, `api_base` + `protocol` set, user slug list `["my/llama"]`)
- **When** `PUT /api/v1/providers/default-model` is sent with `<body>`
- **Then** the response is `<status>` and the default is `<default_after>`

**Examples**:

| body | status | default_after |
|---|---|---|
| `{"provider":"groq","model":"llama-3.3-70b"}` | 400 field=provider | unchanged |
| `{"provider":"openrouter","model":"not/a-model"}` | 400 field=model | unchanged |
| `{"provider":"my-proxy","model":"my/llama"}` | 200 | my-proxy · my/llama (custom row, no live call) |
| `{"provider":"","model":"x"}` | 400 field=provider | unchanged |
| `{"provider":"openrouter","model":""}` | 400 field=model | unchanged |
| `{"provider":"openrouter","model":"z-ai/glm-5.2","extra":1}` | 400 | unchanged |

#### Scenario: Set as default from the provider row

**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** the Anthropic row in Settings → Providers
- **When** the operator opens the row's actions and chooses *Set as default model…*
- **Then** the selector opens pre-filtered to Anthropic models
- **And** confirming performs the same `PUT` as the card

---

#### Scenario: Picker opens with 8 Popular tiles and a collapsed list

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the picker is opened from onboarding step 3
- **When** it renders
- **Then** exactly 8 elements with `data-testid^="picker-popular-"` exist — one per catalog provider with `tier: popular`, in catalog order (the fixture's: openai, anthropic, openrouter, google, xai, groq, mistral, deepseek)
- **And** an element `data-testid="picker-all-toggle"` reads *All providers (182)* and `aria-expanded="false"`
- **And** changing the fixture so `groq` is `tier: standard` and `cerebras` is `tier: popular` re-renders the band accordingly with no SPA code change

#### Scenario: Search expands and filters the full list

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the picker is open with the list collapsed
- **When** the operator types `coding plan`
- **Then** the list expands and every rendered row's company, plan, region or alias contains `coding plan` case-insensitively
- **And** typing `zzzz` instead shows *No provider matches zzzz* with the *Custom endpoint* row still present

#### Scenario: Expanded list is letter-grouped and virtualised

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the picker is open
- **When** the operator activates *All providers (182)*
- **Then** letter headers appear in A→Z order followed by `#`
- **And** with the 480 px / 40 px-row fixture the count of elements `[role="option"]` in the DOM is ≤ 22
- **And** each rendered option has `aria-setsize="182"` and a distinct `aria-posinset`
- **And** cmdk is mounted with `shouldFilter={false}`; pressing `End` calls the virtualiser's scroll-to-index for the last row and then focuses it

#### Scenario: Unsupported provider is visible, disabled, with reason

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Edge Case

- **Given** the expanded list
- **When** the row for Amazon Bedrock renders
- **Then** it has `aria-disabled="true"` and the text *needs request signing*
- **And** activating it changes nothing

#### Scenario: Custom endpoint is last

**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Edge Case

- **Given** the expanded list, any query
- **When** the operator presses End
- **Then** the focused/last row is *Custom endpoint*

#### Scenario: Recently used row appears

**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** `zai-coding-plan` is already configured
- **When** the Settings picker opens
- **Then** a section *Recent* lists *Z.ai Coding Plan* between the Popular band and the search field

#### Scenario: Keyboard-only selection

**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Happy Path

- **Given** focus is in the search field
- **When** the operator presses ArrowDown three times and Enter
- **Then** the third Popular tile (OpenRouter) is selected and the second-level panel opens
- **And** no pointer event was dispatched

#### Scenario Outline: Region inferred from locale

**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Alternate Path

- **Given** `navigator.language` is `<locale>` and the operator selects a provider with regions `<regions>`
- **When** the second-level panel opens
- **Then** region `<selected>` is `aria-pressed="true"` and the copy reads `<copy>`

**Examples**:

| locale | regions | selected | copy |
|---|---|---|---|
| zh-CN | intl, china | china | Detected: China — change |
| zh-SG | intl, china | china | Detected: China — change |
| zh-TW | intl, china | intl | Detected: International — change |
| zh-HK | intl, china | intl | Detected: International — change |
| en-GB | intl, us | intl | Detected: International — change |
| en-US | intl, us | us | Detected: US — change |
| en-US | intl, china | intl | Detected: International — change |
| de-DE | intl, china | intl | Detected: International — change |
| (empty) | intl, china | intl | Region — change |

#### Scenario: Auth-method control keeps onboarding at three steps

**Traces to**: User Story 5, Acceptance Scenarios 9, 10
**Category**: Happy Path

- **Given** onboarding step 3 with OpenAI selected
- **When** the segmented control renders
- **Then** the step tracker still shows exactly 3 steps
- **And** switching the segment to *API key* reveals the key field and hides the sign-in radios, without navigation

#### Scenario: Catalog unavailable in the picker

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Error Path

- **Given** the providers-catalog GET returns HTTP 500
- **When** the picker opens
- **Then** an error state with *Retry* is shown and the *Custom endpoint* row is still selectable
- **And** onboarding can proceed via Custom endpoint

---

#### Scenario: Models ordered by vendor then release date

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** OpenRouter's catalog models include `anthropic/claude-sonnet-4.6` (2026-02), `anthropic/claude-3.5-haiku` (2024-10), `openai/gpt-5.4` (2026-03), `x/nodate` (no date)
- **When** the selector opens
- **Then** the group order is by vendor group and within Anthropic the order is claude-sonnet-4.6 then claude-3.5-haiku
- **And** `x/nodate` is the last row of its group

#### Scenario: At most three Recommended chips per provider

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a provider with 5 models that support tool calling with windows 200k, 200k, 128k, 128k, 127,999
- **When** the selector renders
- **Then** exactly 3 rows carry *Recommended for chat*
- **And** the 127,999-window model does not

#### Scenario: Onboarding model field is empty and labelled

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Happy Path

- **Given** onboarding step 3 after a valid probe
- **When** the model field renders
- **Then** its accessible label is *Model for your first agent*
- **And** it has no value and *Finish* is disabled
- **And** a chip row may show Recommended models but none is selected
- **And** choosing a model sends `POST /onboarding/probe-provider` with that `model`; changing the model afterwards re-sends the probe and disables *Finish* until it passes

#### Scenario Outline: Model selector virtualisation threshold

**Traces to**: User Story 6, Acceptance Scenarios 4, 5
**Category**: Edge Case

- **Given** a provider with `<n>` models
- **When** the selector opens
- **Then** the number of rendered option rows is `<rendered>`

**Examples**:

| n | rendered |
|---|---|
| 0 | 0 (empty state "No models") |
| 1 | 1 |
| 100 | 100 |
| 101 | ≤ visible + 10 |
| 359 | ≤ visible + 10 |

---

#### Scenario: Check with my account greys unavailable models

**Traces to**: User Story 7, Acceptance Scenarios 1, 2
**Category**: Happy Path

- **Given** the catalog lists models A, B, C for `openai` and the stubbed `/models` returns A, C, Z
- **When** the operator clicks *Check with my account*
- **Then** `POST /api/v1/providers/openai/entitlement` is called and returns `{models:[{id:"A",entitled:true,limits:"known"},{id:"B",entitled:false,…},{id:"C",…},{id:"Z",entitled:true,limits:"unknown"}], cached:false}`
- **And** B is greyed with *not available on this key*
- **And** Z is listed with *limits unknown*
- **And** exactly one upstream request was made; a second click returns `cached: true` with no upstream request

#### Scenario: Check with my account upstream failure

**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Error Path

- **Given** the stubbed `/models` returns HTTP 429
- **When** the operator clicks *Check with my account*
- **Then** an inline warning shows *could not fetch upstream model list: status 429*
- **And** no model is greyed

#### Scenario: Row expand shows limits and window source

**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a connected provider row, before ADR-066 D9 lands
- **When** it is expanded
- **Then** each model line shows catalog `context_window` · `max_output_tokens` · image · PDF (from `input_modalities`) and the window-source cell renders `—`
- **And** once S66's `ResolveWindow(provider, model)` supplies a source the same cell renders one value of `ContextWindowSource.yaml`
- **And** a local model whose projection has `window_unknown: true` renders *No context length* with a link whose href is the Settings → Models → Model overrides route pre-filled with `<provider>`/`<model>` (X-08)

---

#### Scenario: Esc with a dirty key keeps the sheet open

**Traces to**: User Story 8, Acceptance Scenarios 1, 2
**Category**: Happy Path

- **Given** the config sheet with `sk-test-123` typed and unsaved
- **When** Esc is pressed
- **Then** the sheet remains open and *Discard key?* with *Discard* / *Keep editing* is shown
- **And** choosing *Keep editing* leaves the field value `sk-test-123`

#### Scenario: Discard clears the draft

**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** the *Discard key?* prompt
- **When** *Discard* is chosen
- **Then** the sheet closes
- **And** reopening it shows an empty key field

#### Scenario Outline: Close behaviour by draft state

**Traces to**: User Story 8, Acceptance Scenarios 4, 5
**Category**: Edge Case

- **Given** the sheet with key field `<value>` and saved state `<saved>`
- **When** `<action>`
- **Then** `<result>`

**Examples**:

| value | saved | action | result |
|---|---|---|---|
| "" | n/a | Esc | closes, no prompt |
| "   " | n/a | Esc | closes, no prompt (whitespace = empty) |
| "sk-x" | saved | Esc | closes, no prompt |
| "sk-x" | unsaved | overlay click | stays open, prompt |
| "sk-x" | unsaved | Cancel button | closes, no prompt, draft cleared |

---

#### Scenario: Contracts regenerate cleanly

**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the feature branch
- **When** `make verify-contracts` runs
- **Then** it exits 0
- **And** `ProbeProviderRequest.yaml` has no `enum` under `id`

#### Scenario Outline: Probe provider id validation

**Traces to**: User Story 9, Acceptance Scenarios 2, 3
**Category**: Error Path

- **Given** onboarding is not complete
- **When** `POST /onboarding/probe-provider` is sent with id `<id>`, endpoint `<endpoint>` and model `<model>`
- **Then** the response is `<status>`

**Examples**:

| id | endpoint | model | status |
|---|---|---|---|
| openrouter | — | — | 200 (probe runs with the first Recommended model) |
| openrouter | — | z-ai/glm-5.2 | 200 (probe runs with exactly that model) |
| openrouter | — | not/a-model | 200 `success:false` with the upstream error (the probe is the validation) |
| openrouter | http://10.0.0.5/v1 | — | 422 SSRF guard (MIN-006) |
| catalog | — | — | 400 field=id (reserved literal) |
| codex-cli (`auth: sign_in`, no key, fake `codex` on PATH logged in) | — | gpt-5.4 | 200 `success:true`, `probed_model: gpt-5.4` |
| codex-cli (`auth: sign_in`, no saved login) | — | — | 400 field=auth "not signed in" |
| openai-chatgpt (`auth: sign_in`, fresh `auth.json`) | — | — | 200 (one completion with the saved token) |
| openrouter (`auth: sign_in`) | — | — | 400 "provider does not support sign-in" |
| openrouter (`auth: api_key`, no `api_key`) | — | — | 400 field=api_key |
| not-a-provider | — | — | 400 field=id, body lacks any id list |
| not-a-provider | https://gw.example/v1 | — | 200 (custom path) |
| "" | — | — | 400 field=id |
| OPENROUTER | — | — | 400 field=id (pattern) |
| a×65 | — | — | 400 field=id (maxLength) |
| antigravity | — | — | 400 `unknown provider "antigravity"` — the echo is the operator's input, not a trace |
| claude-cli | — | — | 400 `unknown provider "claude-cli"` |
| openrouter | — | 257 chars | 400 field=model (maxLength) |

#### Scenario: SPA reads the catalog from the GET, not a bundle

**Traces to**: User Story 9, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the built SPA
- **When** `grep -rn "generated/providerCatalog" src` runs and Settings → Providers is opened in Playwright
- **Then** the grep is empty
- **And** for the session, at most one `200` response from the providers-catalog GET is observed per distinct `ETag`; conditional re-validations (`304`) on Settings open and every 15 min are expected (ADR-067 A-1)

#### Scenario: Onboarding complete with sign-in

**Traces to**: User Story 9, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** step 3 with `codex-cli` signed in
- **When** `POST /onboarding/complete` is sent with the `OnboardingProviderSignIn` variant (`auth_method: "sign_in"`, no `api_key`)
- **Then** the response is 200, the persisted provider row has `auth_method: sign_in`, and `agents.defaults.default_model` holds the chosen pair
- **And** sending the `sign_in` variant with an `api_key` fails schema validation with 400
- **And** a later reload (`ReloadProviderAndConfig`) does not overwrite `default_model` — the old `ModelName == ""` guard no longer exists (FR-020, MIN-009)

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | Go: dependents computation, id validation, delete ordering; TS: picker data model (tiers, search, letter groups), region inference, model ordering/chips, draft guard | Logic in isolation |
| Integration | Go `httptest` against `HandleProviders`/onboarding with a temp `OMNIPUS_HOME`; vitest component tests with mocked REST | Components together |
| E2E | Playwright against the embedded-SPA binary (`tests/e2e/providers.spec.ts`, `onboarding`, `accessibility.spec.ts`) | Operator-visible flows, a11y |

Conventions: Go tests run as `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/...` one at a time locally; the full suite runs only in CI. Vitest files sit beside the component. Playwright specs extend `tests/e2e/providers.spec.ts`.

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `scripts/check-no-removed-providers.sh` (CI lint gate; allow-list in `scripts/no-removed-providers.allow`) | CI script | Antigravity leaves no trace…; `claude-cli` leaves no trace…; OpenAI device-code flow leaves no trace | Greps `pkg cmd src contracts config docs` for `antigravity`, the id `claude-cli`, and the deleted OAuth symbols; fails on any hit outside the allow-list; the allow-list is a data file, not Go source (MAJ-009/MAJ-015). Self-check: the script fails when a fixture file containing the word is added. |
| 1a | `TestBinaryHasNoRemovedProviderLiteral` | E2E (CI, after `embed-build`) | Config naming a removed provider fails generically | `strings` on the built binary contains no `antigravity`/`claude-cli` |
| 2 | `TestProbeProviderID_Validation` | Unit (Go) | Probe provider id validation; Reserved literals are never provider ids | Table over the outline rows incl. reserved literals, SSRF row, sign-in rows; asserts status, field, `probed_model`; optional `model` passed verbatim, absent → first Recommended |
| 3 | `TestProviderDependents` + `TestProviderDependents_EnumeratesEveryModelField` | Unit (Go, `pkg/gateway`) | Dependents are listed…; Fallback references…; Passthrough-resolved agents are dependents | Computes `{id,name,role}` over every reference site (primary, fallback, passthrough rule 3, default_model, image, recap, voice); the second test reflects over `AgentDefaults`/`VoiceConfig` fields named `*Model*` and fails on an unlisted one |
| 4 | removed-id rows folded into S67 T23's `ErrUnknownProvider` outline (X-33) + `TestFactory_CliKindDispatch` (this spec) | Unit (Go) | `claude-cli` is an unknown provider; OpenAI device-code flow leaves no trace | `CreateProviderFromConfig` for `antigravity`, `claude-cli`, `claudecli`, `codexcli` → `errors.Is(err, catalog.ErrUnknownProvider)`, no hint; `cli_kind: codex` → `*CodexCliProvider`; `cli_kind: copilot` → `*CopilotCliProvider`; `openai-chatgpt` (protocol `openai-compatible`) → `*CodexProvider` whose token source reads a temp `auth.json`; `AuthMethod: oauth` → rejected at config validation (closed set `api_key\|sign_in`, X-25). The source-level "no vendor case" scan is S67 T24 only. |
| 5 | `TestRecommendedChipSelection` (`model-ordering.test.ts`) | Unit (TS) | At most three Recommended chips per provider | eligibility + tie-break |
| 6 | `TestModelOrdering` (`model-ordering.test.ts`) | Unit (TS) | Models ordered by vendor then release date | group + date desc + undated last |
| 7 | `TestRegionFromLocale` (`region-inference.test.ts`) | Unit (TS) | Region inferred from locale | outline rows incl. `zh-TW`, `zh-HK`, `zh-SG`, `en-GB` |
| 8 | `TestPickerModel` (`provider-picker-model.test.ts`) | Unit (TS) | Picker opens with 8 Popular…; Search expands…; Custom endpoint is last | pure data transform: tiers, recent, search over company/plan/region/alias, letter groups, custom last |
| 9 | `TestDraftGuard` (`use-draft-guard.test.ts`) | Unit (TS) | Close behaviour by draft state | whitespace = empty; saved = clean |
| 10 | `TestDeleteProvider_Unused200` | Integration (Go) | Remove an unused provider after one confirmation | 200 body, config row gone, `<id>_API_KEY` gone; variant with the key pre-deleted (NotFoundError tolerated); audit entry `provider.deleted` with ref name only |
| 10a | `TestDeleteProvider_PartialFailureNoOrphanSecret` | Integration (Go) | DELETE partial failure leaves no orphaned secret and a retry succeeds | inject config-write failure → 500, key present, dependents cleared; retry → 200; also inject an entity-update failure on agent 2 of 3 → 500, retry succeeds |
| 10b | `TestCredentialSweep_RemovesOrphans` | Integration (Go) | Startup sweep removes an orphaned credential | orphan `<id>_API_KEY` removed at boot; non-matching names untouched |
| 10c | `TestDeleteProvider_RecomputesUnderLock` | Integration (Go) | Server recomputes dependents under the lock | stale GET, concurrent agent PUT, DELETE lists the new dependent; concurrent DELETE×2 → 200 then 404 |
| 10d | `TestDeleteProvider_AuthPosture` | Integration (Go) | DELETE and default PUT require an authenticated admin | 401 without user; 503 under bypass via `requireAdminAuthz`; default-model route registered with `adminWrap` |
| 11 | `TestDeleteProvider_DependentsLeftWithoutModel` | Integration (Go) | Dependents are listed and left without a model | agents' model/provider cleared, `needs_model` true in GET /agents |
| 12 | `TestDeleteProvider_DefaultRequiresReplacement409` | Integration (Go) | DELETE without replacement… is refused | 409, nothing changed |
| 13 | `TestDeleteProvider_WithNewDefault` | Integration (Go) | Default-backing provider requires an inline new default; Other provider in error state still offered as new default | order: default changed then provider removed; reload waited; `new_default` naming an `error`-state provider accepted |
| 14 | `TestDeleteProvider_404_503_Bypass503` | Integration (Go) | Removing an unconfigured…; Removal refused while locked | 404 / 503 / RequireNotBypass 503 |
| 15 | `TestDeleteProvider_OnlyProviderRefused409` | Integration (Go) | Removing the only provider is refused | 409, provider/key/default unchanged; RemoveProviderDialog renders the only-provider copy with Remove disabled |
| 15a | `TestListProviders_ConfiguredOnly` | Integration (Go) | GET /providers returns configured providers only | seed templates not echoed; one row; DELETE on template id → 404 |
| 15b | `TestSignInStart_CLILoginOnly` | Integration (Go) | OpenAI device-code flow leaves no trace | `method: cli_login`, `command: "codex login"`; no device_code branch exists |
| 16 | `TestDeleteProvider_FallbackRemoved` | Integration (Go) | Fallback references are removed and listed | fallback entry dropped, `needs_model` false |
| 17 | `TestDefaultModel_PutResolvesAtTurnTime` | Integration (Go) | Change default model takes effect on the next turn | PUT → run one turn against a stub provider → the session transcript line carries the new `provider`+`model`; `GetModelConfig(pair)` resolves exactly; `config.json` has no `model_name` key; audit `provider.default_model.changed` (mirrors `rest_default_agent_singleton_test.go`, oracle = transcript) |
| 18 | `TestDefaultModel_PutValidation` | Integration (Go) | Default-model PUT validation | outline rows |
| 19 | `TestTurn_ModelUnassignedTypedError` | Integration (Go, `pkg/agent`) | Turn to an agent without a model is refused | `LLMError.code == model_unassigned`, zero provider calls |
| 20 | `TestSignIn_RefusedForKeyOnly400` | Integration (Go) | Sign-in refused for a key-only provider | 400 |
| 21 | `TestSignInStatus_CodexCLI` | Integration (Go) | Signed-in row…; Expired session… | fake `auth.json` fresh → `signed_in` + `account_label = account_id`; JWT `exp` past → `expired`; no-JWT stale mtime → `expired`; missing binary → `disconnected`; **file bytes and mtime unchanged after the call** (FR-007); no outbound refresh request |
| 22 | `TestOnboardingComplete_AuthMethod` | Integration (Go) | Onboarding complete with sign-in | `oneOf` variants: sign_in without key 200 + `default_model` pair written; sign_in with key 400; api_key without key 400; a subsequent `ReloadProviderAndConfig` leaves `default_model` unchanged (FR-020) |
| 22a | `TestProbeProvider_SignIn` | Integration (Go) | Probe provider id validation (sign-in rows) | fake `codex` on PATH: logged-in → 200 `probed_model`; not logged in → 400 `field=auth`; key-only provider with `auth: sign_in` → 400 |
| 23 | `TestEntitlement_IntersectsAndCaches` | Integration (Go) | Check with my account greys…; upstream failure | `POST /{id}/entitlement`: `entitled`/`limits` annotations, one upstream call, second call `cached: true`, cache key `SHA-256(providerID+":"+credentialRefName)` (X-03), eviction on DELETE and on key-changing PUT (X-21); upstream 429 → S67's 502 body |
| 23a | `TestTurn_PreTurnGateOrder` | Integration (Go, `pkg/agent`) | Turn to an agent without a model is refused | agent with unknown provider AND empty model → `needs_provider`; configured provider + empty model → `model_unassigned`; both set + S66 window refusal → S66's code only after the first two pass (cross-spec Q6) |
| 23b | `TestCopilotCliProvider_*`, `TestSignInStatus_Copilot` | Unit/Integration (Go) | Signed-in row…; Expired session… (Copilot) | fake `copilot` binary: output parsing, missing binary → disconnected, status states, probe dry-run (X-14) |
| 24 | `TestContractsRegenerateClean` (CI `make verify-contracts`) | Integration | Contracts regenerate cleanly | no drift; `contract_test.go` passes with new schemas |
| 25 | `ProviderPicker.test.tsx` | Integration (vitest) | Picker opens…; Search…; Expanded list…; Unsupported…; Recently used…; Keyboard-only…; Catalog unavailable | renders against the 190 fixture in a 480 px container; asserts testids, aria, ≤ 22 rows, `shouldFilter={false}`, End/Home → `scrollToIndex` spy then focus, error state; Recent ordered by `updated_at` desc, max 3 |
| 25a | `provider-picker-search.test.ts` | Integration (vitest, over the rendered component) | Search expands and filters the full list | FR-024 at component level (MIN-009) |
| 26 | `AuthMethodControl.test.tsx` | Integration (vitest) | Auth methods offered per provider; OpenAI sign-in offers two named providers | outline rows; default radio `codex-cli` |
| 27 | `ProvidersSection.test.tsx` (extended) | Integration (vitest) | Default model card…; Set as default from row; Signed-in row…; Expired session…; Esc with a dirty key…; Discard clears…; No Undo exists; Row expand shows limits (renders `—` for source) | existing file gains describes per scenario; No-Undo asserts no toast action and no restore request via the mocked fetch |
| 28 | `RemoveProviderDialog.test.tsx` | Integration (vitest) | Remove an unused…; Dependents…; Default-backing…; Removing the only provider | dialog content, disabled state, `role="alertdialog"`, focus on Cancel |
| 29 | `model-selector.test.tsx` (extended) | Integration (vitest) | Model selector virtualisation threshold; Onboarding model field is empty and labelled | 100 vs 101 rows; label; no value |
| 30 | `providers.spec.ts` (Playwright, extended) | E2E | Remove…; Change default model takes effect…; Check with my account…; SPA reads the catalog from the GET | real binary, stub vendor |
| 31 | `onboarding.spec.ts` (Playwright) | E2E | Fresh install seeds no default model; Auth-method control keeps three steps; Onboarding model field empty | three steps, empty model, Finish disabled |
| 32 | `accessibility.spec.ts` (Playwright, extended) | E2E | Picker opens…; Expanded list…; Keyboard-only selection; Remove an unused provider…; Esc with a dirty key… | axe 0 serious/critical on the three states; `getBoundingClientRect ≥ 24` over the selector list; focus-ring contrast ≥ 3:1 via `getComputedStyle`; `activeElement` = Cancel on dialog open; Esc closes with no DELETE; `activeElement` stays in sheet after Esc; ArrowDown×3+Enter, End, Home, Escape key rows (MAJ-012) |
| 34 | `TestDefaultsSeed_NoRemovedProvider` | Unit (Go, `pkg/config`) | Fresh install seeds no default model | `DefaultConfig()` templates: no `antigravity/`/`claude-cli/` model, no `AuthMethod` field; `Agents.Defaults.DefaultModel` zero |
| 33 | CI gates `make verify-contracts`, tagged `go build`, `npm run typecheck` (not a test function — OBS-005) | CI | Build and contract gates pass with the files gone | exit codes captured without a pipe in `runci.sh`; no Go test named for it |

### Test Datasets

#### Dataset: Provider id (probe and DELETE path)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `""` | Empty | 400 field=id | Probe provider id validation | |
| 2 | `"a"` | Min | 200 if in catalog else 400 | Probe provider id validation | single char passes pattern |
| 3 | 64×`a` | Max | pattern ok; 400 unknown | Probe provider id validation | |
| 4 | 65×`a` | Max+1 | 400 maxLength | Probe provider id validation | |
| 5 | `"OPENROUTER"` | Case | 400 pattern | Probe provider id validation | lowercase only |
| 6 | `"open router"` | Whitespace | 400 pattern | Probe provider id validation | |
| 7 | `"../etc"` | Special | 400 pattern | Probe provider id validation | path traversal guard |
| 8 | `"zai-coding-plan"` | Valid | 200 | Probe provider id validation | hyphen allowed |
| 9 | `"antigravity"` | Removed id | 400, body has no "antigravity" | Probe provider id validation | generic message |
| 10 | `"codexcli"` | Removed alias | 400 | Probe provider id validation | no alias |
| 11 | `"openai-chatgpt"` with `auth: api_key` | New id, wrong auth | 400 "provider does not support api_key" | Probe provider id validation | sign-in-only provider |
| 11a | `"openai-chatgpt"` with `auth: sign_in`, fresh `auth.json` | New id | 200 | Probe provider id validation | one completion with saved token |
| 11b | `"catalog"` / `"default-model"` | Reserved | 400 field=id | Reserved literals are never provider ids | |
| 12 | `"智谱"` | Unicode | 400 pattern | Probe provider id validation | aliases are search terms, not ids |

#### Dataset: DELETE /providers/{id} bodies

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | no body, not default | Happy | 200 `default_changed:false` | Remove an unused provider after one confirmation | |
| 2 | no body, backs default | Error | 409 | DELETE without replacement… is refused | |
| 3 | `{"new_default":{"provider":"anthropic","model":"claude-sonnet-4.6"}}` | Happy | 200 `default_changed:true` | Default-backing provider requires an inline new default | |
| 4 | `{"new_default":{"provider":"openrouter",...}}` (same id) | Error | 400 | DELETE without replacement… is refused | self-reference |
| 5 | `{"new_default":{"provider":"groq","model":"x"}}` (unconfigured) | Error | 400 | Default-model PUT validation | |
| 6 | `{"new_default":{}}` | Empty object | 400 | Default-model PUT validation | required fields |
| 7 | malformed JSON | Corrupt | 400 | Default-model PUT validation | |
| 8 | no body, only provider, backs default | Edge | 409, nothing changed | Removing the only provider is refused | no other connected provider; never deletable |
| 8a | `{"new_default":{"provider":"anthropic",...}}` but anthropic is `unknown-provider` | Error | 400 | DELETE without replacement… is refused | new default must be a configured, known provider; `error`/`expired` rows are allowed (MAJ-011) |
| 9 | concurrent DELETE ×2 same id | Concurrency | first 200, second 404 | Server recomputes dependents under the lock | both recompute under `configMu` |
| 10 | DELETE, config write injected to fail | Partial failure | 500 `deleted:false`; key + row remain; retry 200 | DELETE partial failure leaves no orphaned secret | |
| 11 | DELETE, entity update fails on agent 2/3 | Partial failure | 500; retry 200; each agent cleared once | DELETE partial failure leaves no orphaned secret | idempotent step 1 |
| 12 | DELETE without Bearer (post-onboarding) | Auth | 401 | DELETE and default PUT require an authenticated admin | |
| 13 | DELETE with `dev_mode_bypass` | Auth | 503 | DELETE and default PUT require an authenticated admin | |
| 14 | `new_default.provider` in `error` state | Edge | 200 (allowed, operator's risk) | Other provider in error state still offered as new default | |

#### Dataset: Dependents computation

| # | Input (agents) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | none reference id | Empty | `[]` | Remove an unused provider after one confirmation | |
| 2 | one primary | Single | `[{role:primary}]` | Dependents are listed and left without a model | |
| 3 | one primary + same agent fallback | Duplicate | one entry, role primary | Dependents are listed and left without a model | de-dup |
| 4 | fallback only | Alt | `[{role:fallback}]` | Fallback references are removed and listed | |
| 5 | locked core agent primary | Edge | listed | Dependents are listed and left without a model | no exemption |
| 6 | 50 agents primary | Large | 50 entries, names sorted | Dependents are listed and left without a model | dialog scroll |
| 7 | agent with `provider:""` and model slug matching the provider's `Model` | Inferred | listed as primary | Dependents are listed and left without a model | exact lookup (CRIT-001; `ModelName` alias gone) |
| 8 | agent with `provider:""`, slug unmatched, OpenRouter configured | Passthrough | listed role `passthrough` | Passthrough-resolved agents are dependents | `resolveFallbackProvider` rule 3 |
| 9 | `agents.defaults.recap_fallback_models` names the provider | Recap | listed role `recap` | Passthrough-resolved agents are dependents | |
| 10 | `agents.defaults.image_model` on the provider | Image | listed role `image` | Passthrough-resolved agents are dependents | |
| 11 | `voice.model_name` on the provider | Voice | listed role `voice` | Passthrough-resolved agents are dependents | |
| 12 | provider is the `default_model` provider | Default | `backs_default: true` | Default-backing provider requires an inline new default | |

#### Dataset: Default-model PUT

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | connected provider + catalog model | Happy | 200 | Change default model takes effect on the next turn | |
| 2 | unconfigured provider | Error | 400 provider | Default-model PUT validation | |
| 3 | connected + unknown model | Error | 400 model | Default-model PUT validation | |
| 4 | `my-proxy` (`custom: true`) + user slug | Alt | 200 | Default-model PUT validation | catalog bypass keyed on the flag, never the id (X-13) |
| 5 | `ollama` (`locality: local`) + any non-empty model | Alt | 200, **no network call** | Default-model PUT validation | X-17, X-22 |
| 6 | empty provider | Empty | 400 | Default-model PUT validation | |
| 7 | model 256 chars | Max | 200 if listed | Default-model PUT validation | |
| 8 | model 257 chars | Max+1 | 400 | Default-model PUT validation | |
| 9 | extra property | Schema | 400 | Default-model PUT validation | `additionalProperties:false` |
| 10 | `signed_in` provider + model | Alt | 200 | Change default model takes effect on the next turn | sign-in counts as connected |
| 11 | `expired` provider | Error | 400 provider | Default-model PUT validation | not connected |
| 12 | `PUT` then one turn | Turn-time | transcript `provider`+`model` = body | Change default model takes effect on the next turn | the only valid oracle (CRIT-001) |
| 13 | `PUT` without Bearer | Auth | 401 | DELETE and default PUT require an authenticated admin | |

#### Dataset: Picker search

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `""` | Empty | collapsed | Picker opens with 8 Popular tiles… | |
| 2 | `"   "` | Whitespace | collapsed | Search expands and filters… | trimmed |
| 3 | `"z"` | Single char | expands; all with z | Search expands and filters… | |
| 4 | `"Coding Plan"` | Case | plan matches | Search expands and filters… | |
| 5 | `"china"` | Region | region matches | Search expands and filters… | |
| 6 | `"glm-coding"` | Alias | Z.ai row | Search expands and filters… | alias list |
| 7 | `"bedrock"` | Unsupported | disabled row | Unsupported provider is visible… | still shown |
| 8 | `"(*["` | Regex chars | no crash, no match | Search expands and filters… | literal |
| 9 | 200-char string | Very long | no match state | Search expands and filters… | |
| 10 | `"智谱"` | Unicode alias | Z.ai row if alias present | Search expands and filters… | |

#### Dataset: Model ordering and chips

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 0 models | Empty | "No models" | Model selector virtualisation threshold | |
| 2 | 1 model, tool+200k | Single | 1 chip | At most three Recommended chips | |
| 3 | 3 eligible | Max | 3 chips | At most three Recommended chips | |
| 4 | 4 eligible | Max+1 | 3 chips (oldest dropped) | At most three Recommended chips | |
| 5 | window 127,999 | Min-1 | no chip | At most three Recommended chips | |
| 6 | window 128,000 | Min | chip | At most three Recommended chips | |
| 7 | no tool calling, 1M window | Disqualified | no chip | At most three Recommended chips | |
| 8 | same date two models | Tie | id asc | Models ordered by vendor… | |
| 9 | no date | Null | last in group | Models ordered by vendor… | |
| 10 | 100 / 101 / 359 | Threshold | all / virtual / virtual | Model selector virtualisation threshold | |

#### Dataset: Sign-in status (`codex-cli` / `openai-chatgpt`)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | binary missing | Missing dep | `disconnected` + hint | Expired session routes to re-sign-in | |
| 2 | binary present, no `auth.json` | Missing file | `not_signed_in` | Signed-in row shows account | |
| 3 | `auth.json` mtime 10 min | Fresh | `signed_in` + label | Signed-in row shows account | |
| 4 | `auth.json` mtime 61 min, `access_token` not a JWT | Expired | `expired` | Expired session routes to re-sign-in | 1 h rule only without a decodable `exp` (resolution #13, MAJ-006) |
| 4a | `auth.json` mtime 61 min, JWT `exp` 2 h ahead | Claim wins | `signed_in`, `expires_at` = `exp` | Signed-in row shows account | unverified decode, display only |
| 4b | `auth.json` mtime 5 min, JWT `exp` 1 min ago | Claim wins | `expired` | Expired session routes to re-sign-in | fresh file, stale token; no refresh attempted |
| 4c | JWT `exp` exactly now | Boundary | `expired` | Expired session routes to re-sign-in | `exp <= now` is expired |
| 4d | JWT with `exp` but bad signature | Unverified | `signed_in` (display only) | Signed-in row shows account | the claim is never an authorization input |
| 5 | `auth.json` unreadable (perm) | Permission | `not_signed_in` + warning | Expired session routes to re-sign-in | no crash |
| 6 | `auth.json` malformed | Corrupt | `not_signed_in` + warning | Expired session routes to re-sign-in | |
| 7 | `tokens.account_id` absent | Null | `account_label` absent; row reads "Signed in" | Signed-in row shows account | no e-mail exists in `auth.json` |
| 8 | `auth.json` contains `refresh_token` | Ignored | not read, not stored, not on the wire | Signed-in row shows account | struct field removed |

#### Regression Dataset: existing provider behaviour preserved

| # | Input | Previous Behaviour | Must Still Produce | Traces to |
|---|---|---|---|---|
| 1 | `PUT /providers/openrouter` with invalid key | 422, nothing persisted (`TestPutProvider_InvalidKey422NotPersisted`) | same | Regression: provider validation |
| 2 | `PUT` with no-credit key | 200 persisted + validation warning (`TestPutProvider_NoCredit200Persisted`) | same | Regression: provider validation |
| 3 | `PUT` without `api_key` on existing | no probe (`TestPutProvider_KeyUnchangedNoProbe`) | same | Regression: provider validation |
| 4 | `GET /providers` with locked vault | `providerCredErrors` message on row | same for **configured** rows (template rows no longer exist — resolution #16) | Regression: credential degraded (`provider_credential_degraded_test.go`) |
| 5 | WS provider refusal | `websocket_provider_refusal_test.go` | same | Regression: LLMError surfacing |
| 6 | Agent PUT with `delegation_policy` | 400 (ADR-037) | same | Regression: agent handler |
| 7 | Onboarding complete with `api_key` (auth_method api_key) | 200, default set once | same, now into `default_model` | Regression: onboarding |
| 8 | Company group headers only when ≥2 configured variants | `ProvidersSection.test.tsx` FIX-2 | same | Regression: configured list |

### Regression Test Requirements

This feature **modifies existing functionality**.

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| PUT provider validation/persistence | `pkg/gateway/provider_validation_test.go` (3 tests) | No | must pass unchanged |
| Credential-degraded rows | `provider_credential_degraded_test.go` | No | GET branch gains fields; existing assertions hold |
| Agent↔provider resolution | `rest_agent_provider_test.go` | Yes — `TestAgentProvider_NeedsModelDerived` | `needs_model` must be false for every existing fixture |
| Default agent singleton | `rest_default_agent_singleton_test.go` | No | pattern reused for default model |
| LLMError copy drift | `pkg/api/generated/llm_error_codes_test.go::TestContract_LLMError_AllClassifierCodesRoundTrip`, `llm_error_catalogue_test.go` | No — must pass after S67's four-copy edit | X-01 |
| `AgentDefaults` / `defaults.go` edited by all three specs | S66 `TestConfig_NoContextWindowDefaultKey`, S67 `TestSeeds_CanonicalProviderIDs`, this spec's `TestDefaultsSeed_NoRemovedProvider` | All three must pass after merge; landing order S67 → S68 → S66 | X-29 |
| Credential-degraded fixtures | `provider_credential_degraded_test.go` | Yes — fixtures updated **by this spec** (GET-branch fields, template rows gone); S67 lists it as "updated by S68" | X-32 |
| `agents.defaults.model_name` alias resolution | every fixture/test setting `model_name` (`gateway.go` guards, `config_test.go`, onboarding tests) | **Delete/rewrite** to `default_model` — greenfield (CRIT-001) | a test still passing `model_name` is a false green |
| Contract round-trip | `pkg/api/generated/contract_test.go` | No (auto-covers new schemas) | |
| Configured list rendering | `ProvidersSection.test.tsx` (5 describes) | Yes — update fixtures for new required fields `dependents`, `backs_default`; **delete** the "template-provider filtering (realistic GET /providers shape)" describe — the shape it filters no longer exists (resolution #16) | zod will reject old fixtures |
| Catalog embed parity | `catalog_test.go::TestCatalog_EmbedMatchesGeneratedTS` | **Delete** (the TS file is gone) — replace with a test that the served GET equals the embedded snapshot | ADR-067 owns the snapshot |
| Onboarding probe | `tests/e2e/providers.spec.ts` | Yes — extend for free-string id | |

---

## Functional Requirements

**D13 — policy and removals**
- **FR-001**: No file under `pkg cmd src contracts config docs` MUST contain `antigravity` outside the historical decision records enumerated in ADR-068 §2.4, enforced by `scripts/check-no-removed-providers.sh` in CI with its allow-list `scripts/no-removed-providers.allow` as a data file (never in `pkg/`). **The allow-list is enumerated here (X-19)** — exactly: the historical decision records ADR-068 §2.4 names; `docs/internal/architecture/ADR-066-*.md`, `ADR-067-*.md`, `ADR-068-*.md` and their `*-review*.md`; `docs/internal/specs/adr-066-context-overflow-spec*.md`, `adr-067-registry-catalog-spec*.md`, `adr-068-providers-ux-spec*.md`; `docs/internal/specs/cross-spec-review-adr-066-067-068.md`; and the allow-list file itself. Everything else: zero hits for `antigravity`, the id `claude-cli`, and the deleted OpenAI device-code symbols. Grep gates are evaluated on the **merged** branch (X-34). "No trace" is a SOURCE property: runtime echo of a user-supplied id through the generic unknown-provider path is not a trace (CRIT-003).
- **FR-002**: The provider factory MUST construct nothing for `antigravity`, `claude-cli`, `claudecli`, `codexcli`; an agent or config naming them MUST fail on the generic unknown-provider path with no mention of the name. `claude-cli` MUST be deleted entirely (files, factory cases, enum values, catalog allow-list row, docs) under the same no-trace rule as `antigravity`; the exit-proof grep targets the id `claude-cli`, not the word "claude" (resolution #11).
- **FR-002a**: The OpenAI device-code/OAuth flow in `pkg/auth/oauth.go` and `codex_provider.go::createCodexTokenSource` MUST be deleted; no code path in Omnipus MUST start a vendor OAuth or device-code login for OpenAI (resolution #8).
- **FR-003**: In the protocol switch the id `codex-cli` MUST dispatch to `NewCodexCliProvider` (the existing mapping, kept); the new id `openai-chatgpt` MUST dispatch to `NewCodexProviderWithTokenSource(CreateCodexCliTokenSource())`; the id-keyed OAuth ladder (`createCodexAuthProvider`, `createClaudeAuthProvider`) and the `ModelConfig.AuthMethod` values `oauth|token` MUST be deleted (`AuthMethod` itself is kept with the closed set `api_key | sign_in` — X-25). Under S67's protocol dispatch the `cli` case MUST select the constructor by the catalog row's `cli_kind` (`codex` → `NewCodexCliProvider`, `copilot` → `NewCopilotCliProvider`); `openai-chatgpt` is protocol `openai-compatible` (X-14, X-41). `knownProtocols`/`IsKnownProtocol` are deleted by S67 and MUST NOT be referenced (X-15).
- **FR-004**: The catalog served to the SPA (ADR-067 schema 2.0.0) MUST declare `auth_methods` per provider; `sign_in` MUST be absent for `anthropic`, `google` and (until listing) `xai`, and present for `codex-cli`, `openai-chatgpt`, and `github-copilot` (resolution #7; the provider is specified in full under "GitHub Copilot subprocess provider" — X-14).
- **FR-005**: The UI MUST render a sign-in control only where `auth_methods` contains `sign_in`, pre-selected where present.
- **FR-006**: The `openai-chatgpt` option MUST carry the label *"relies on OpenAI's stated tolerance, not its written terms"*; `codex-cli` MUST be the default of the pair.
- **FR-007**: Omnipus MUST NOT write, refresh or proxy the vendor credential file for `codex-cli`; for `openai-chatgpt` it MUST only read `tokens.access_token` and `tokens.account_id` (never `refresh_token`), MUST NOT refresh the token (a session ends at expiry and needs `codex login`), and a status call MUST leave the file's bytes and mtime unchanged (MAJ-006).
- **FR-008**: `POST /providers/{id}/sign-in` MUST return 400 for providers without `sign_in`.
- **FR-009**: `GET /providers/{id}/sign-in/status` MUST return one of `not_signed_in | signed_in | expired` with `account_label = tokens.account_id` when present; `expired` MUST follow the access token's JWT `exp` (decoded unverified, display only) when present and the 1 h `auth.json` age rule otherwise (resolution #13, MAJ-006). `POST /providers/{id}/sign-in` MUST return `{method: "cli_login", command, instructions}` only (resolution #8, MIN-005).

**D14 — deletion and default model**
- **FR-010**: `DELETE /api/v1/providers/{id}` MUST, under the config lock and after recomputing dependents/`backs_default`, run these idempotent steps in order: (1) clear/re-point dependents in the agent entity store; (2) remove the provider row from `config.json`; (2b) remove `ContextSettings.model_overrides[]` rows for the provider (cross-spec Q3); (3) delete `<id>_API_KEY`, treating `credentials.NotFoundError` as success; (3b) evict the provider's entitlement cache entry (X-21); (4) emit audit `provider.deleted` with the credential ref name (never the value), dependents count and any default change; (5) `TriggerReload` and wait; then respond 200 with `ProviderDeleteResponse`. On failure at any step it MUST respond 500 `{deleted:false}` leaving a retryable state; a retry re-runs all steps. A startup sweep MUST delete any `<id>_API_KEY` whose provider row is gone (greenfield housekeeping) (CRIT-004, MAJ-016, MAJ-019).
- **FR-011**: `DELETE` MUST return 404 for an unconfigured id, 503 when the credential store is locked (before any change), 503 under dev-mode bypass, 409 when `backs_default` and no valid `new_default`, 400 when `new_default` names the same id or a provider that is not `connected | signed_in`. A provider that backs the default MUST never be deleted while it backs it; hence the last connected provider MUST never be deletable (resolution #4). No dry-run mode exists (resolution #2). No password re-type is required (resolution #18).
- **FR-011a**: `GET /api/v1/providers` MUST return configured providers only — no "disconnected" template rows for unconfigured catalog entries (resolution #16).
- **FR-012**: `GET /api/v1/providers` rows MUST carry `dependents[]` (`ProviderDependent {id,name,role}`) and `backs_default` so the dialog needs no second request (resolution #2); these are advisory — the server recomputes both under the config lock on `DELETE` and the response is authoritative (MAJ-018). "Dependent" MUST cover every reference enumerated under Data Constraints (primary, fallback, passthrough rule 3, default/image/recap/voice) (MAJ-010).
- **FR-013**: On deletion, each dependent agent's primary `model`/`provider` MUST be cleared (never re-pointed); fallback entries naming the provider MUST be removed; the response MUST list both.
- **FR-014**: `GET /api/v1/agents` MUST expose `needs_model: boolean` (true when the primary model is empty or its provider is not configured); the agent list MUST render *needs a model* for such agents, except that when ADR-067's `degraded_reason: needs_provider` is also set the *needs a provider* copy wins (resolution #5).
- **FR-015**: A turn to an agent with `needs_model` MUST be refused with `LLMError.code = model_unassigned` and no provider call.
- **FR-016**: The SPA MUST always show the confirm dialog before deletion, with the sentence *"Remove `<Display name>`? Its key will be deleted."*, dependents grouped by role, and — when `backs_default` — an inline new-default selector listing every **other configured** provider with its status shown (`error`/`expired` rows included and selectable — operator's risk, MAJ-011; `unknown-provider` rows excluded) with *Remove* disabled until chosen. When no other connected provider exists the dialog MUST read *"This is your only provider and backs the default model; connect another provider and make it the default before removing this one."* with *Remove* permanently disabled (resolution #4). The dialog is the only gate — no ReAuth password prompt (resolution #18).
- **FR-017**: The SPA MUST NOT render an Undo affordance after deletion, and MUST NOT retain the key client-side at any time.
- **FR-018**: `GET/PUT /api/v1/providers/default-model` MUST exist as its own `adminWrap` route dispatched before the `/providers/` prefix handler; `PUT` MUST validate provider configured and `connected | signed_in` and model in catalog (except rows with `custom: true` or `locality: local` — S67's single predicate, X-13/X-17 — where any non-empty model is accepted with no live call, X-22), persist **`agents.defaults.default_model {provider, model}`** (contract `DefaultModel.yaml`) under the config lock, emit audit `provider.default_model.changed`, call `TriggerReload` and wait, and take effect on the next turn — proven by the session transcript. `agents.defaults.model_name` and its alias semantics MUST be deleted; `GetModelConfig` MUST resolve the pair exactly (CRIT-001).
- **FR-019**: Settings → Providers MUST render the *Default model* card first (the control lives **only** there — ADR-068 §4 as amended, X-37), showing `provider · model · window · source`, with *Change* opening the selector filtered to connected providers; the backing row MUST show a *Default* marker; each row MUST offer *Set as default model…*. Window and source come from S66's exported `ResolveWindow(provider, model)` via the default-model GET (X-07) and render as `—` until it lands; `window_unknown: true` renders the *No context length* copy with the *Settings → Models → Model overrides* pointer (X-08); the card ships without waiting (resolution #15).
- **FR-020**: Both `ModelName == ""` guards (`gateway.go` L1658, L5048) MUST be deleted with the field; onboarding completion MUST write `default_model` once, and no boot/reload path MUST overwrite a `default_model` written by the PUT (CRIT-001, MIN-009).

**§4/§5 — surfaces**
- **FR-021**: One `ProviderPicker` component MUST be used by onboarding step 3 and the Settings sheet.
- **FR-022**: The picker MUST render, in order: the Popular tiles (one per `company` whose variants include a `tier: popular` provider, in catalog order — never a SPA constant, resolution #1; no order asserted here, MIN-001; grouping key = S67's `company`, X-10), *Recent* (configured providers ordered by `Provider.updated_at` desc, max 3 — resolution #14, MAJ-015), one search field, *All providers (N)* collapsed until query non-empty (trimmed) or expanded, *Custom endpoint* last.
- **FR-023**: The expanded list MUST be letter-grouped by `company` (A–Z, `#`; X-10) — one row per company with its plan × region variants as the second-level panel — and virtualised with `aria-setsize`/`aria-posinset`; rendered options MUST be ≤ visible + 10.
- **FR-024**: Search MUST match `company`, `name`, plan, region and alias, case-insensitively, treating the query literally (X-10). The *Custom endpoint* panel MUST collect `id`, `api_base`, `protocol` and the key; the saved row MUST be recognised everywhere by `Provider.custom: true`, never by a literal id (X-13).
- **FR-025**: Unsupported providers MUST render `aria-disabled="true"` with their reason and MUST NOT be hidden by default.
- **FR-026**: The picker MUST be built on cmdk `Command` with `shouldFilter={false}` and the spec-owned filter (FR-024); the virtualised section MUST implement Home/End/typeahead by index — `@tanstack/react-virtual` `scrollToIndex` then focus the mounted row — so unmounted rows are reachable (MAJ-013); ArrowUp/Down/Home/End/Enter/Esc are Playwright-asserted.
- **FR-027**: The second-level panel MUST present plan and region as `aria-pressed` groups; region MUST default from locale per the inference map (`zh-CN`/`zh-SG` → china; other `zh-*` → intl; `en-US` → us when offered; everything else → intl — resolution #17, MIN-003) with the copy *"Detected: `<Region>` — change"* (or *"Region — change"* when not inferred).
- **FR-028**: The auth-method segmented control MUST live in the second-level panel; onboarding MUST stay three steps.
- **FR-029**: Onboarding MUST NOT pre-select a model; the field label MUST be *"Model for your first agent"*; *Finish* MUST be disabled until a model is chosen and a probe **of the chosen auth method** has passed for that exact model (`auth: api_key` with the key, or `auth: sign_in` through the CLI's saved login / Copilot session); changing the model MUST re-run the probe (resolution #12, CRIT-002).
- **FR-030**: The model selector MUST order by vendor group then `release_date` desc (undated last, id asc), mark ≤3 *Recommended for chat* per provider (catalog `tool_call` AND `context_window` ≥128,000 AND `status = active` — MIN-002), and virtualise above 100 items (≤ 22 rendered rows in the 480 px fixture).
- **FR-031**: *Refresh models* MUST be replaced by *Check with my account*, wired to `POST /api/v1/providers/{id}/entitlement` (ADR-067's route; the backend removal of `refresh-models`/`model-capabilities` is S67's deliverable — this spec removes their SPA consumers, X-23) whose `EntitlementResponse` annotates each catalog model `entitled`/`limits`; the SPA greys `entitled:false` with *not available on this key*, flags `limits: unknown`, and leaves the list unchanged on failure with an inline warning; the gateway caches for the process keyed by `SHA-256(providerID + ":" + credentialRefName)` — never the secret value (X-03, coordinator; S67 FR-021 carries the same key) — and evicts the entry on `DELETE` of the provider and on any `PUT` that changes its key (X-21; a PUT that only bumps `updated_at` with no key does not evict).
- **FR-032**: Each configured row MUST show on expand per-model window · output · image · PDF and the window source label, one of `operator | live | catalog | floor` (ADR-066 D9; `learned` does not exist — D8 dropped), or `—` until D9 lands.
- **FR-033**: Closing the config sheet via Esc/overlay with a non-empty unsaved key MUST keep it open and show *"Discard key?"* with *Discard* / *Keep editing*; explicit *Cancel* and clean states close without prompt.
- **FR-034**: Row states MUST include `signed_in` (*Signed in as …*, action *Manage*) and `expired` (*Session expired*, action opens re-sign-in).

**§6/§7 — wire**
- **FR-035**: Every new or changed wire shape MUST be defined in `contracts/` first and consumed only via generated types; `make verify-contracts` MUST pass. **Ownership (X-26): S67 owns and commits the contract FILE edit for every shared schema** — `Provider.yaml`, `Agent.yaml`, `ProbeProviderRequest.yaml`, `DefaultModel.yaml`, `EntitlementResponse.yaml`, the `LLMError` set in all four copies, and the `pkg/gateway/inboundschemas/` copies (cross-spec Q1) — including the values this spec defines (`signed_in`, `expired`, `needs_model`, `model_unassigned`); this spec owns their semantics and copy and consumes generated types only; handlers that cannot yet compute a required field emit its zero value. **S67 lands first, then this spec.** This spec's own (unshared) schemas it still writes: `ProviderDeleteRequest.yaml`, `ProviderDeleteResponse.yaml`, `ProviderDependent.yaml`, `DefaultModelUpdateRequest.yaml`, `SignInStartResponse.yaml`, `SignInStatus.yaml`, `OnboardingProviderApiKey.yaml` + `OnboardingProviderSignIn.yaml`, and the `openapi.yaml` paths it adds. The complete list (MAJ-014): `ProviderDeleteRequest.yaml`, `ProviderDeleteResponse.yaml`, `ProviderDependent.yaml`, `DefaultModel.yaml`, `DefaultModelUpdateRequest.yaml`, `SignInStartResponse.yaml`, `SignInStatus.yaml`, `EntitlementResponse.yaml` (shared with ADR-067), `Provider.yaml` (`status` six values, `auth_method`, `account_label`, `dependents`, `backs_default`, `updated_at`), `Agent.yaml` (`needs_model`, coordinated with ADR-067's `degraded_reason` in one edit), `OnboardingProviderApiKey.yaml` + `OnboardingProviderSignIn.yaml` with the inline `oneOf`+`discriminator` in `openapi.yaml` (ADR-034), `ProbeProviderRequest.yaml` (one shape `{id, auth, api_key?, model?, api_base?, protocol?}` — X-04), `ProbeProviderResponse.yaml` (`probed_model`), `LLMError` `model_unassigned` in `LLMError.yaml` + `LLMErrorReplay.yaml` + asyncapi inline `LLMError` + `LLMErrorReplay` (X-01), `DefaultModel.window_source` → `$ref ContextWindowSource.yaml` (S66-owned, X-06), and the `openapi.yaml` paths `DELETE /providers/{id}`, `GET/PUT /providers/default-model`, `POST /providers/{id}/sign-in`, `GET /providers/{id}/sign-in/status`.
- **FR-036**: `ProbeProviderRequest.id` MUST be a free string (`1..64`, no hand pattern — MIN-011) validated at runtime against the served catalog, with the reserved literals rejected; unknown without both `api_base` and `protocol` → 400 `unknown provider "<id>"` (S67 vocabulary, X-04), never an id list; any `api_base` MUST pass the SSRF gate (MIN-006). `auth` MUST be required; `api_key` MUST be required iff `auth = api_key`; for `auth = sign_in` the probe MUST use the CLI's saved login / Copilot session and 400 only when neither is present (CRIT-002). `model` (optional, `1..256`) MUST be used verbatim when present; absent → the provider's first Recommended catalog model; the response MUST carry `probed_model`.
- **FR-037**: `src/lib/generated/providerCatalog.ts` and the `gen/main.go` TS emission MUST be deleted; the SPA MUST read `GET /api/v1/providers/catalog` following ADR-067 A-1 (re-validate with `If-None-Match` on Settings open and every 15 min); at most one `200` per ETag value (MAJ-004).
- **FR-038**: `Provider.status` enum MUST be exactly `connected | disconnected | error | unknown-provider | signed_in | expired` — one enumeration, listed verbatim in this spec and the ADR-067 spec (MAJ-001).
- **FR-039**: `ProviderCatalogEntry.yaml`'s "never served from a live HTTP endpoint" description MUST be removed (the schema is superseded by ADR-067's catalog schema).
- **FR-040**: Fresh-install seed (`pkg/config/defaults.go`) MUST name no removed provider and MUST leave `agents.defaults.default_model` at its **zero value** (X-30; `model_name` no longer exists); onboarding's explicit pick is the only writer on a fresh install. The "Popular-tier API-key model" replacement of ADR §2.2 applies only to `config/config.example.json` (resolution #10).

**Accessibility**
- **FR-041**: All constraints under "Accessibility (WCAG 2.2 AA)" MUST hold: axe MUST report 0 serious/critical on onboarding step 3 and Settings → Providers with sheet and dialog open, and every non-axe row (focus ring contrast, target size, focus placement, Esc handling, focus containment, key navigation) MUST have its named Playwright assertion in `accessibility.spec.ts` (MAJ-012).
- **FR-042**: `DELETE /providers/{id}` and `GET/PUT /providers/default-model` MUST require an authenticated user (401 otherwise, no pre-onboarding exception) and MUST be gated by `RequireNotBypass` (503 under bypass) — the default-model route via `adminWrap` at registration, the DELETE verb via `requireAdminAuthz` inline in the shared dispatcher; neither requires the Spec-6 FR-12.2 re-auth token (recorded exception, resolution #18) (MAJ-007).
- **FR-043**: `GET /providers` MUST show a configured row whose provider the catalog does not know as `status: unknown-provider` (ADR-067's state), with the generic text parameterised by the operator's id (CRIT-003, MAJ-001).

---

## Success Criteria

- **SC-001**: `scripts/check-no-removed-providers.sh` exits 0 on the branch and exits 1 when a fixture file containing `antigravity` or `claude-cli` is added outside the allow-list; `grep -rn 'RequestDeviceCode\|PollDeviceCodeOnce\|OpenAIOAuthConfig\|createCodexTokenSource\|createCodexAuthProvider\|createClaudeAuthProvider' pkg cmd` prints nothing; `strings` on the built binary contains neither id.
- **SC-002**: `make verify-contracts`, `gofmt -l . | wc -l` = 0, `golangci-lint run --build-tags=goolm,stdjson`, `CGO_ENABLED=1 go build -tags goolm,stdjson ./...`, `npm run typecheck`, `npx vitest run` all exit 0 on CI (exit codes captured without a pipe).
- **SC-003**: After `DELETE /providers/{id}` returns 200, `credentials.Store.Get("<id>_API_KEY")` returns `NotFoundError` synchronously; after an injected step-2 failure the key still exists and one retry returns 200; a boot with an orphaned `<id>_API_KEY` removes it; an audit entry `provider.deleted` exists per completed run.
- **SC-004**: Default-model PUT → one chat turn: the session transcript's model/provider equals the PUT body in 10/10 runs without restart; `config.json` contains `agents.defaults.default_model` and no `model_name` key.
- **SC-005**: Picker with the 190 fixture: rendered `[role="option"]` count ≤ `floor(viewportHeight/40) + 10` in 100% of Playwright runs (≤ 22 in the vitest fixture); first paint is recorded, not gated.
- **SC-006**: Model selector with 359 models: rendered rows ≤ visible + 10; with 100 models: exactly 100.
- **SC-007**: axe-core: 0 `serious`/`critical` violations on the three audited states; every listed target ≥ 24×24 CSS px (measured via `getBoundingClientRect`).
- **SC-008**: Onboarding e2e: step tracker has exactly 3 steps; *Finish* disabled until model chosen; completion request carries `auth_method`.
- **SC-009**: 0 occurrences of `Undo` in `RemoveProviderDialog.tsx` and its tests' DOM snapshots.
- **SC-010**: `grep -rn "generated/providerCatalog" src pkg` is empty; `ls src/lib/generated/providerCatalog.ts` fails.
- **SC-011**: `ProbeProviderRequest.yaml` contains no `enum:` under `id` and lists `api_key` as optional with `auth` required; the 400 body for an unknown id is `unknown provider "<id>"` with no list.
- **SC-012**: `Provider.status` generated Go consts are exactly six and equal the ADR-067 spec's list.
- **SC-013**: A turn to a `needs_model` agent produces an `LLMError` with `code == "model_unassigned"` and the provider mock records 0 calls; an agent that is both `needs_provider` and `needs_model` produces ADR-067's `needs_provider` code, never `model_unassigned`.
- **SC-014**: With one connected provider, `DELETE` on it returns 409 in 10/10 runs and the dialog's Remove button has `disabled` set; `GET /providers` with ten seed templates and one configured provider returns exactly one row.
- **SC-015**: Unauthenticated `DELETE /providers/{id}` and `PUT /providers/default-model` return 401; under bypass 503; `DELETE /providers/catalog` returns 404 and `PUT /providers/default-model` with a `ProviderUpdateRequest` body returns 400 with no provider created.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-1 | Antigravity leaves no trace in the source tree; Config naming a removed provider fails generically | scripts/check-no-removed-providers.sh; TestBinaryHasNoRemovedProviderLiteral |
| FR-002 | US-1, US-2 | `claude-cli` is an unknown provider; `claude-cli` leaves no trace in the source tree | TestFactory_NoVendorCaseForRemovedIDs; TestProbeProviderID_Validation; scripts/check-no-removed-providers.sh |
| FR-002a | US-2 | OpenAI device-code flow leaves no trace | scripts/check-no-removed-providers.sh; TestFactory_NoVendorCaseForRemovedIDs; TestSignInStart_CLILoginOnly |
| FR-003 | US-2 | OpenAI sign-in offers two named providers…; OpenAI device-code flow leaves no trace | TestFactory_NoVendorCaseForRemovedIDs; AuthMethodControl.test.tsx |
| FR-004 | US-2 | Auth methods offered per provider; Sign-in refused for a key-only provider | AuthMethodControl.test.tsx; TestSignIn_RefusedForKeyOnly400 |
| FR-005 | US-2, US-5 | Auth methods offered per provider; Auth-method control keeps onboarding at three steps | AuthMethodControl.test.tsx; onboarding.spec.ts |
| FR-006 | US-2 | OpenAI sign-in offers two named providers… | AuthMethodControl.test.tsx |
| FR-007 | US-2 | Signed-in row shows account and Manage (file unchanged); Expired session routes to re-sign-in (no refresh) | TestSignInStatus_CodexCLI (mtime/bytes + no outbound refresh assertions) |
| FR-008 | US-2 | Sign-in refused for a key-only provider | TestSignIn_RefusedForKeyOnly400 |
| FR-009 | US-2 | Signed-in row…; Expired session… | TestSignInStatus_CodexCLI; ProvidersSection.test.tsx |
| FR-010 | US-3 | Remove an unused provider after one confirmation; DELETE partial failure leaves no orphaned secret…; Startup sweep removes an orphaned credential | TestDeleteProvider_Unused200; TestDeleteProvider_PartialFailureNoOrphanSecret; TestCredentialSweep_RemovesOrphans; providers.spec.ts |
| FR-011 | US-3 | DELETE without replacement…; Removing an unconfigured…; Removal refused while locked; Removing the only provider is refused | TestDeleteProvider_DefaultRequiresReplacement409; TestDeleteProvider_404_503_Bypass503; TestDeleteProvider_OnlyProviderRefused409 |
| FR-011a | US-3 | GET /providers returns configured providers only | TestListProviders_ConfiguredOnly |
| FR-012 | US-3 | Dependents are listed…; Default-backing provider requires…; Server recomputes dependents under the lock; Passthrough-resolved agents are dependents | TestProviderDependents; TestProviderDependents_EnumeratesEveryModelField; TestDeleteProvider_RecomputesUnderLock; RemoveProviderDialog.test.tsx |
| FR-013 | US-3 | Dependents are listed and left without a model; Fallback references are removed and listed | TestDeleteProvider_DependentsLeftWithoutModel; TestDeleteProvider_FallbackRemoved |
| FR-014 | US-3 | Dependents are listed and left without a model | TestDeleteProvider_DependentsLeftWithoutModel; TestAgentProvider_NeedsModelDerived |
| FR-015 | US-3 | Turn to an agent without a model is refused | TestTurn_ModelUnassignedTypedError |
| FR-016 | US-3 | Remove an unused…; Dependents…; Default-backing…; Removing the only provider is refused; Other provider in error state still offered as new default | RemoveProviderDialog.test.tsx; TestDeleteProvider_OnlyProviderRefused409; TestDeleteProvider_WithNewDefault |
| FR-017 | US-3 | No Undo exists after removal | ProvidersSection.test.tsx (no toast action, no restore request); providers.spec.ts |
| FR-018 | US-4 | Change default model takes effect…; Default-model PUT validation | TestDefaultModel_PutResolvesAtTurnTime; TestDefaultModel_PutValidation |
| FR-019 | US-4 | Default model card shows…; Set as default from the provider row | ProvidersSection.test.tsx; providers.spec.ts |
| FR-020 | US-4, US-1 | Fresh install seeds no default model; Onboarding complete with sign-in (reload guard); Change default model takes effect… | TestOnboardingComplete_AuthMethod; TestDefaultModel_PutResolvesAtTurnTime |
| FR-021 | US-5 | Picker opens with 8 Popular tiles…; Recently used row appears | ProviderPicker.test.tsx; onboarding.spec.ts |
| FR-022 | US-5 | Picker opens with 8 Popular tiles…; Search expands…; Custom endpoint is last | TestPickerModel; ProviderPicker.test.tsx |
| FR-023 | US-5 | Expanded list is letter-grouped and virtualised | ProviderPicker.test.tsx; accessibility.spec.ts |
| FR-024 | US-5 | Search expands and filters the full list | TestPickerModel; provider-picker-search.test.ts |
| FR-025 | US-5 | Unsupported provider is visible, disabled, with reason | ProviderPicker.test.tsx |
| FR-026 | US-5 | Keyboard-only selection; Expanded list is letter-grouped and virtualised (End → scrollToIndex) | ProviderPicker.test.tsx; accessibility.spec.ts |
| FR-027 | US-5 | Region inferred from locale | TestRegionFromLocale; ProviderPicker.test.tsx |
| FR-028 | US-5 | Auth-method control keeps onboarding at three steps | onboarding.spec.ts |
| FR-029 | US-6, US-1, US-2 | Onboarding model field is empty and labelled; Fresh install seeds no default model; Probe provider id validation (sign-in rows) | model-selector.test.tsx; onboarding.spec.ts; TestProbeProvider_SignIn |
| FR-030 | US-6 | Models ordered…; At most three Recommended chips…; Model selector virtualisation threshold | TestModelOrdering; TestRecommendedChipSelection; model-selector.test.tsx |
| FR-031 | US-7 | Check with my account greys…; Check with my account upstream failure | TestEntitlement_IntersectsAndCaches; providers.spec.ts |
| FR-032 | US-7 | Row expand shows limits and window source (`—` until D9) | ProvidersSection.test.tsx |
| FR-033 | US-8 | Esc with a dirty key…; Discard clears the draft; Close behaviour by draft state | TestDraftGuard; ProvidersSection.test.tsx |
| FR-034 | US-2 | Signed-in row…; Expired session… | ProvidersSection.test.tsx |
| FR-035 | US-9 | Contracts regenerate cleanly; Onboarding complete with sign-in | TestContractsRegenerateClean; TestOnboardingComplete_AuthMethod |
| FR-036 | US-9, US-2 | Probe provider id validation; Reserved literals are never provider ids | TestProbeProviderID_Validation; TestProbeProvider_SignIn |
| FR-037 | US-9 | SPA reads the catalog from the GET, not a bundle; Catalog unavailable in the picker | providers.spec.ts; ProviderPicker.test.tsx |
| FR-038 | US-2, US-9 | Signed-in row…; Contracts regenerate cleanly | TestContractsRegenerateClean; ProvidersSection.test.tsx |
| FR-039 | US-9 | Contracts regenerate cleanly | TestContractsRegenerateClean |
| FR-040 | US-1 | Fresh install seeds no default model; Build and contract gates pass with the files gone | `TestDefaultsSeed_NoRemovedProvider` (parses `defaults.go` seed); CI gates (`runci.sh`) |
| FR-041 | US-5, US-3, US-8 | Picker opens…; Expanded list…; Keyboard-only selection; Remove an unused provider…; Esc with a dirty key… | accessibility.spec.ts (named assertion rows) |
| FR-042 | US-3, US-4 | DELETE and default PUT require an authenticated admin | TestDeleteProvider_AuthPosture |
| FR-043 | US-1 | Config naming a removed provider fails generically | TestListProviders_ConfiguredOnly (unknown-provider row case); TestBinaryHasNoRemovedProviderLiteral |

**Completeness check**: every FR has ≥1 scenario and ≥1 test; every BDD scenario appears in at least one row. Re-traces after the review: US-3 AS8 ↔ *Removing an unconfigured provider returns 404*; *GET /providers returns configured providers only* is re-traced to US-3 AS8 **and** FR-011a/FR-043; *Config naming a removed provider fails generically* → FR-001/FR-043; *Build and contract gates* → FR-040 (CI, not a test function). US-2 AS6's two scenarios are now consistent (source no-trace vs generic runtime path).

---

## Ambiguity Warnings

All 18 items were resolved by the operator/coordinator on 2026-08-22 and the decisions are applied throughout the body. The table is kept as the record; no open ambiguity remains.

| # | What was ambiguous | Status | Decision (applied) |
|---|---|---|---|
| 1 | Catalog fields the picker/selector need | **RESOLVED** | ADR-067 schema 2.0.0 carries provider `tier (popular\|standard\|unsupported)`, `unsupported_reason`, `auth_methods[] (api_key\|sign_in)`, `aliases[]` (search only), `name`; model `name`, `release_date`, `tool_call`, `context_window`, `max_output_tokens`, `input_modalities`, `status`. Popular = `tier: popular` in catalog data, never a SPA constant. |
| 2 | Source of the dialog's dependents data | **RESOLVED (accept)** | `GET /providers` rows carry `dependents[]` and `backs_default`; `DELETE` takes optional `new_default`; no dry-run. |
| 3 | Agents referencing the provider only as a fallback | **RESOLVED (accept)** | Fallback entry removed; agent listed under *"uses as fallback"*; primary unaffected. |
| 4 | Removing the only configured provider | **RESOLVED (operator decision, supersedes assumption)** | A provider that backs the default CANNOT be deleted while it backs it; the user must first choose a new default inline from other connected providers. The last connected provider can therefore never be deleted; its dialog reads *"This is your only provider and backs the default model; connect another provider and make it the default before removing this one."* The "default cleared / No default model" path is removed. |
| 5 | How a dependent is "left without a model" | **RESOLVED (accept)** | Server clears the agent's stored primary model/provider; `Agent.needs_model` derived. Coordinated with ADR-067 `degraded_reason: needs_provider` — both may apply; `needs_provider` wins in copy. |
| 6 | LLMError code for "agent has no model" | **RESOLVED (accept)** | New code `model_unassigned`, attribution `config`, via contracts. |
| 7 | GitHub Copilot id and mechanism | **RESOLVED (accept)** | Id `github-copilot`; official Copilot CLI as subprocess; no Go SDK module (Constraint #1). |
| 8 | What *Sign in* does for OpenAI | **RESOLVED (accept)** | *"Run `codex login` on this machine"* + Check (status reads the CLI's saved login). Omnipus starts no device-code flow; the `pkg/auth` OpenAI device-code flow is unreferenced and deleted under greenfield. |
| 9 | Route path for the default-model control | **RESOLVED (accept)** | `GET/PUT /api/v1/providers/default-model`. |
| 10 | Fresh-install seed vs "no pre-selected model" | **RESOLVED (accept)** | Seed `agents.defaults` EMPTY; onboarding's explicit pick is the only writer; Popular-tier replacement applies only to `config.example.json`. |
| 11 | `claude-cli` deleted or merely unreachable | **RESOLVED (accept)** | Deleted entirely (files, factory cases, enum values, allow-list row, docs); grep exit proof on the id `claude-cli`, not the word "claude". |
| 12 | Probe model vs the operator's explicit pick | **RESOLVED (accept)** | `ProbeProviderRequest` gains optional `model`; the probe validates the exact pick; re-probe on model change in onboarding. |
| 13 | `expired` semantics for `openai-chatgpt` | **RESOLVED (refined)** | Follows the token's own expiry claim when present; the 1 h `auth.json` rule only when no claim exists. |
| 14 | Recent section source | **RESOLVED (accept)** | Configured providers, most recently saved first, max 3. |
| 15 | Window/source dependency on ADR-066 D9 | **RESOLVED (accept)** | Ship the card with `—` until ADR-066 D9 lands; no sequencing dependency. Addendum: ADR-066 D8 (learned limits) is dropped — the source vocabulary is `operator \| live \| catalog \| floor`, no `learned`. |
| 16 | Template "disconnected" rows in `GET /providers` | **RESOLVED (superseded)** | The permanent template rows are REMOVED — `GET /providers` returns configured providers only (greenfield, coordinated with ADR-067). DELETE on an unknown id → 404. |
| 17 | Region inference for other English locales | **RESOLVED (accept)** | `en-US` → us (when offered), else intl; `zh-*` → china. |
| 18 | ReAuth for deletion | **RESOLVED (operator decision)** | Confirm dialog only; no password re-type; `RequireNotBypass` applies. |

---

## Evaluation Scenarios (Holdout)

> **Note**: For post-implementation evaluation only. Not referenced in the TDD plan or traceability matrix. To be run by the operator or a separate evaluator against the built binary.

### Scenario: Cold install to first reply without ever seeing a pre-chosen model
- **Setup**: empty `OMNIPUS_HOME`, fresh binary, a real OpenRouter key.
- **Action**: complete onboarding choosing OpenRouter from the Popular band, pick a model from the list, send "hello".
- **Expected outcome**: at no point was a model pre-filled; the reply arrives from the chosen model (visible in Verbose chat); Settings → Providers shows that pair on the Default model card.
- **Category**: Happy Path

### Scenario: Swap the default while a chat is open
- **Setup**: two connected providers; a chat tab open on a default-routed agent.
- **Action**: in a second tab change the default model; return to the chat and send a message.
- **Expected outcome**: the new reply's model is the new default; no restart, no reload of the chat tab needed.
- **Category**: Happy Path

### Scenario: Find an obscure provider by typing
- **Setup**: Settings → Providers → Connect a provider.
- **Action**: type "moonshot" then "kimi".
- **Expected outcome**: both queries surface Moonshot; arrow keys reach it; Enter opens its panel with plan/region groups.
- **Category**: Happy Path

### Scenario: Delete the provider behind three agents
- **Setup**: three agents on Anthropic; Anthropic also backs the default; OpenAI connected.
- **Action**: Remove Anthropic, pick an OpenAI default inline, confirm.
- **Expected outcome**: the three agents show "needs a model"; a message to one of them is refused with a clear config error; `credentials.json` no longer decrypts an Anthropic key (`omnipus credentials list`).
- **Category**: Error

### Scenario: Try the forbidden path
- **Setup**: any install.
- **Action**: search the whole UI (onboarding, Settings, agent form) for any way to sign in to Anthropic or Google with an account.
- **Expected outcome**: none exists; only API-key fields.
- **Category**: Error

### Scenario: Keyboard-only operator
- **Setup**: unplug the mouse.
- **Action**: connect a provider, set it as default, remove another provider — keyboard only.
- **Expected outcome**: every step completes; focus is always visible; the confirm dialog opens with focus on Cancel.
- **Category**: Edge Case

### Scenario: Offline picker
- **Setup**: disconnect the network after boot (the embedded catalog snapshot is the norm; the "Catalog unavailable" BDD scenario covers the abnormal 5xx case).
- **Action**: open the picker and the model selector.
- **Expected outcome**: both populate fully from the local catalog; only Test / Check with my account fail, with a clear message.
- **Category**: Edge Case

---

## Review Findings Disposition (2026-08-22)

Every finding of `adr-068-providers-ux-spec-review.md` was verified against the code on this branch before acting. "Applied" means the body above now carries the change; ADR amendments are in commit `docs(adr): ADR-068 amendments from spec review`.

| Finding | Verified | Disposition |
|---|---|---|
| CRIT-001 | Confirmed: `gateway.go` L1658/L5048 guards; `config.go::GetModelConfig` → `findMatches` over `ModelName` | **Applied (coordinator):** `agents.defaults.default_model {provider, model}` (`DefaultModel.yaml`), `model_name` deleted, exact pair resolution, `TriggerReload`; oracle = session transcript. ADR §3 D14.1. |
| CRIT-002 | Confirmed: `ProbeProviderRequest.yaml` `required: [id, api_key]` | **Applied:** `api_key` optional, `auth: api_key\|sign_in`, sign-in probe via CLI login / Copilot session, 400 only when neither present; Finish needs a passing probe of the chosen method. ADR §7, §9.4. |
| CRIT-003 | Confirmed: `rest_onboarding.go` L726 echoes the id; ADR-067 fixes the wire text | **Applied:** no-trace is a SOURCE property; scenarios assert source/binary, not response bodies. ADR §2.4, §9.1. |
| CRIT-004 | Confirmed: `Store.Delete` returns `NotFoundError` (store.go L266-280); entity store under its own lock | **Applied (coordinator):** five idempotent steps, NotFoundError tolerated, audit `provider.deleted` with ref name, startup sweep, retry semantics, partial-failure tests. ADR §3 D14.2. |
| MAJ-001 | Confirmed: ADR-067 spec A-16/FR-016 `unknown-provider` | **Applied:** six-value enum, verbatim in both specs; SC-012 updated. |
| MAJ-002 | Confirmed: `HandleProviders` PUT branch `sub != "" && !HasSuffix("/test")` (rest.go L6017) | **Applied:** `catalog`/`default-model` reserved, dispatched before `{id}`, invalid as ids everywhere; scenario + dataset rows. |
| MAJ-003 | Confirmed: ADR-067 spec `/entitlement` replaces `refresh-models`, removes `model-capabilities` | **Applied:** one route name, `EntitlementResponse.yaml`; `model-capabilities` dropped from symbols. |
| MAJ-004 | Confirmed: ADR-067 A-1 (15 min + Settings open, ETag) | **Applied:** assertion is ≤ one 200 per ETag; entitlement cached per key for the process. |
| MAJ-005 | Confirmed **with a correction to the review's grounding**: L394 `codex-cli` → `NewCodexCliProvider` (subprocess) already; the HTTP path is `createCodexAuthProvider` (store OAuth) under `case "openai"` + `AuthMethod oauth\|token` (L93-95), and `CreateCodexCliTokenSource` has **zero non-test callers**; `createClaudeAuthProvider` at L311 is a store-held Anthropic OAuth path | **Applied:** both layers named; L394 kept; `openai-chatgpt` wires the `auth.json` reader; store-OAuth ladder (OpenAI and Anthropic) deleted; `knownProtocols`, `AuthMethod`, protocol comment in the inventory. ADR §2.2 rewritten. |
| MAJ-006 | Confirmed: `CodexCliAuth` = `tokens.{access_token, refresh_token, account_id}`; expiry = mtime + 1 h; no e-mail | **Applied:** status fields limited to `account_id`; `exp` = unverified JWT decode, display only; no refresh (path deleted); `refresh_token` not read; scenario label fixed. |
| MAJ-007 | Confirmed: `/api/v1/providers` registered `withOptionalAuth` (L5218); PUT inlines 401 + `requireReAuth`; `adminWrap` = `withAuth → RequireNotBypass` used by `sandbox-config` (L5285); `requireAdminAuthz` is the inline form (L392-402) | **Applied (coordinator):** admin-only, no re-auth (recorded Spec-6 exception), `RequireNotBypass` as route middleware for default-model, inline for DELETE; FR-042; auth test rows. |
| MAJ-008 | Confirmed: `agent_not_configured`/`model_unavailable` exist in asyncapi | **Applied (coordinator):** `model_unassigned` is an agent-level pre-turn gate; precedence `needs_provider` → `model_unassigned`; reuse rejected (copy/attribution mismatch). |
| MAJ-009 | Confirmed: `defaults.go` L29-35 `ModelName` empty; antigravity is a template at L197-201 | **Applied:** seed scenario re-pointed at the template; exit proof moved to `scripts/` with a data allow-list; vacuous Undo clause replaced; `auth.json` mtime/bytes assertion; cache test. |
| MAJ-010 | Confirmed: `resolveFallbackProvider` rule 3; `RecapFallbackModels` L1592; `ImageModel` L1547 | **Applied:** dependents enumerated (primary, fallback, passthrough, default, image, recap, voice) + reflection test; roles extended. |
| MAJ-011 | Confirmed by reading the spec | **Applied (coordinator):** `error`/`expired` providers listed with status and selectable; no dead end. |
| MAJ-012 | Confirmed (axe has no focus-contrast / focus-placement rules) | **Applied:** each non-axe row carries its Playwright assertion; FR-041 rewritten. |
| MAJ-013 | Confirmed (cmdk navigates mounted items) | **Applied:** `shouldFilter={false}`, spec-owned filter, `scrollToIndex` then focus; test rows. |
| MAJ-014 | Confirmed | **Applied:** full schema list in FR-035 incl. `ProviderDependent`, `EntitlementResponse`, `probed_model`, `oneOf`+discriminator for onboarding (ADR-034), coordinated `Agent.yaml` edit. |
| MAJ-015 | Confirmed (no `updated_at` on `ModelConfig`; `AgentConfig.UpdatedAt` L1097 is the precedent) | **Applied:** allow-list out of `pkg/`; `Provider.updated_at` / `ModelConfig.UpdatedAt` added. |
| MAJ-016 | Confirmed: `audit.Logger.Log(&audit.Entry{...})` precedent in rest.go | **Applied:** `provider.deleted`, `provider.default_model.changed`, `provider.credential_swept`; tests assert emission. |
| MAJ-017 | Verified: `createStartupProvider` L3636 / `defaultModelCredentialBlocked` L3704 read `cfg.Providers` directly; SPA `fetchProviders()` callers enumerated; `onboarding.tsx` does not call it | **Applied:** consumer list stated; template-row consumer removed entirely. |
| MAJ-018 | Confirmed | **Applied:** server recomputes under the lock; GET advisory; scenario + test. |
| MAJ-019 | Confirmed (store.go L266-280) | **Applied:** handler treats `NotFoundError` as success; test variant. |
| MIN-001 | Confirmed (ADR-067 spec order differs) | **Applied:** no order asserted; catalog order only. |
| MIN-002 | Confirmed (`active \| retired`) | **Applied:** `status = active`. |
| MIN-003 | Accepted | **Applied:** `zh-CN`/`zh-SG` → china; other `zh-*` → intl; rows added. |
| MIN-004 | Accepted | **Applied:** timing is a recorded metric; DOM bound is the gate. |
| MIN-005 | Confirmed (no producer after FR-002a) | **Applied:** device-code fields and `pending` removed. |
| MIN-006 | Confirmed: probe runs `ssrfChecker.CheckURL` (rest_onboarding.go L737) — the gate exists; the spec did not say so | **Applied:** stated; 422 row added. |
| MIN-007 | Accepted | **Applied**, then superseded by cross-spec X-03: key = `SHA-256(providerID + ":" + credentialRefName)`. |
| MIN-008 | Accepted | **Applied** via FR-042 (401 always; no optional-auth exposure). |
| MIN-009 | Accepted | **Applied:** SC renumbered (SC-014/015 at the end); FR-024 integration row; FR-020 reload-guard assertion; FR-041 scenarios named. |
| MIN-010 | Accepted | **Applied:** xAI row is API-key only, no forward copy. |
| MIN-011 | Accepted | **Applied:** hand pattern dropped; catalog membership is the rule. |
| MIN-012 | Accepted | **Applied:** 480 px / 40 px fixture → ≤ 22; Playwright formula stated. |
| OBS-001 | — | **Refuted:** ADR-068 §5 item 1 mandates the *Recently used* band (operator-ratified IA); kept, max 3, ordered by `updated_at`. |
| OBS-002 | — | **Applied:** holdout notes the embedded snapshot is the norm. |
| OBS-003 | — | **Not adopted:** `degraded_reason` is ADR-067's field and `needs_model` is derived here; one enum would couple the two specs' schedules. Precedence is stated in both. |
| OBS-004 | — | **Kept as a hint:** the chip is non-blocking; onboarding copy says "Recommended for chat", and the operator's own pick is always allowed. |
| OBS-005 | — | **Applied:** CI gates are listed as gates, not as a test function. |

---

## Cross-Spec Findings Disposition (2026-08-22)

Findings of `cross-spec-review-adr-066-067-068.md` assigned to S68 / A68 (plus the seams the coordinator named). Verified against the tree; "Applied" = in the body above or in ADR-068 (commit `docs(adr): ADR-068 seam fixes from cross-spec review`).

| Finding | Verified | Disposition |
|---|---|---|
| X-01 | Confirmed: four copies (`LLMError.yaml`, `LLMErrorReplay.yaml`, asyncapi inline `LLMError` L1512 / `LLMErrorReplay` L1632); `_gen-asyncapi-types.mjs` L637 | **Applied:** all four named for `model_unassigned`; S67 writes the files, this spec owns semantics/copy; drift tests in the regression table. |
| X-02 | Confirmed: `needs_provider` absent from every copy | **Applied:** referenced as S67's code (attribution `config`); pre-turn order `needs_provider → model_unassigned → S66 refusal`; `TestTurn_PreTurnGateOrder` added (Q6). |
| X-03 | Confirmed (two keys) | **Applied (coordinator):** `SHA-256(providerID + ":" + credentialRefName)`; FR-031, T23, constraints. |
| X-04 | Confirmed: `endpoint` vs `api_base`/`protocol`; `auth` missing from S67 rows | **Applied (coordinator):** one shape `{id, auth, api_key?, model?, api_base?, protocol?}` listed verbatim; custom probe sends `api_base`+`protocol`; sign-in probe sends `auth: sign_in`; `OPENROUTER` row is "unknown provider", not "pattern"; inbound copy named. |
| X-05 | Confirmed | **Applied:** S67 writes `Provider.yaml` with all seven fields; `ProviderUpdateRequest.auth_method`; zero-value placeholders; inbound copy named. |
| X-06 | Confirmed | **Applied:** `DefaultModel.window_source` `$ref ContextWindowSource.yaml` (S66-owned); no inline enum. |
| X-07 | Confirmed | **Applied:** FR-019 cites S66's `ResolveWindow(provider, model)` as the GET's producer; exempt rows `context_window: 0`, source absent. |
| X-08 | Confirmed | **Applied (coordinator):** per-model `window_unknown: true` rendered on card and row expand with the Settings → Models → Model overrides pointer; status stays six. |
| X-10 | Confirmed (no `company` in S67 shape today) | **Applied:** tiles, letter groups and search key on S67's `company`; `unsupported_reason` enum → copy mapping stated. |
| X-13 | Confirmed | **Applied:** Custom endpoint panel fields (`id`, `api_base`, `protocol`, key); `custom` literals replaced by `my-proxy`; recognition by `Provider.custom: true` only. |
| X-14 | Confirmed: `grep -rli copilot pkg src contracts` empty | **Applied:** `github-copilot` specified in full (catalog row, driver, sign-in detection, status states, probe, window exemption, tests); dispatch by S67's `cli_kind: codex\|copilot`; two CLI details marked [UNVERIFIED] for implementation. ADR §8 task 3 amended. |
| X-15 | Confirmed: S67 FR-025 deletes `knownProtocols`/`IsKnownProtocol` | **Applied:** FR-003, scenario and test 4 rewritten against `catalog.ErrUnknownProvider`; source scan is S67 T24 only. |
| X-17 | Confirmed (three local sets) | **Applied:** `Provider.locality: local\|cloud` is the only distinction (FR-018, US-4.AC5, datasets). |
| X-19 | Confirmed | **Applied:** allow-list enumerated in FR-001 (three specs, three ADRs, their reviews, the cross-spec review, the allow file); zero elsewhere; merged-branch rule (X-34). |
| X-21 | Confirmed | **Applied:** DELETE step 3b evicts the entitlement entry; key-changing PUT evicts; `updated_at`-only PUT does not (Q5). |
| X-22 | Confirmed | **Applied:** local/custom rows accept any non-empty model with no live call. |
| X-23 | Confirmed | **Applied:** S67 owns backend deletions + TS file; this spec owns SPA consumers (`fetchModelCapabilities` L82/313/499, `refreshProviderModels` L2191, `PROVIDER_CATALOG` importers); duplicate snapshot test dropped. |
| X-25 | Confirmed | **Applied:** `AuthMethod` kept with closed set `api_key\|sign_in`; S67 FR-013 holds the field list. |
| X-26 | Confirmed (cycle) | **Applied:** ownership division stated in FR-035 and Assumptions; S67 first, then S68. ADR §7 ownership note. |
| X-29 | Confirmed | **Applied:** regression rows cite S66/S67 tests; landing order S67 → S68 → S66. |
| X-30 | Confirmed | **Applied:** FR-040 → `default_model` zero value. |
| X-33 | — | **Applied:** removed-id rows folded into S67 T23; one AST scan (T24). |
| X-34 | — | **Applied:** "grep gates run on the merged branch" line in FR-001. |
| X-37 (A68) | Confirmed | **Applied in ADR-068 §4/§9:** "Settings → Models" canonical; default-model control on Settings → Providers only. |
| X-38 (A68) | Confirmed | **Applied in ADR-068 §4:** `operator \| live \| catalog \| floor`, `learned` removed. |
| X-39 (A68) | Confirmed | **Applied in ADR-068 §2.2, §2.4 table, §8:** seed is EMPTY; Popular-tier wording confined to `config.example.json`. |
| X-41 (A67/S67) | — | **Consumed:** `openai-chatgpt` is protocol `openai-compatible` with the `auth.json` token source; only `cli_kind` rows are subprocess drivers. |
| Q1 | — | S67 owns the `inboundschemas/` copies (stated in FR-035). |
| Q3 | — | DELETE step 2b removes the provider's `model_overrides[]` rows. |
| Q5 | — | Key-changing PUT evicts; `updated_at`-only PUT does not. |
| Q6 | — | `TestTurn_PreTurnGateOrder` (this spec) asserts the three-stage order end to end. |

---

## Assumptions

- ADR-068 is the confirmed brief (Phase 1 gate); every silence is recorded in Ambiguity Warnings, not resolved by invention.
- S67 (ADR-067's spec) lands first and owns the coordinated contract commit for every shared schema, including the values this spec defines; this spec then lands consuming generated types only (X-26). S66's backend merges after S67 in parallel with this spec; S66's row/picker refusal state and the card's window/source are the last slice (X-07, X-08, X-28).
- ADR-066 D9 supplies window/source values; until it lands the card shows `—`. Source vocabulary is `operator | live | catalog | floor` (D8 "learned" dropped).
- ADR-067 owns `GET /providers/catalog`, `POST /providers/{id}/entitlement`, `Provider.status: unknown-provider`, `Agent.degraded_reason`, and the catalog id rule; this spec consumes them and lists them verbatim where both specs must agree.
- `DefaultModel.yaml` is the persisted shape of `agents.defaults.default_model` and the wire shape of the default-model GET; `agents.defaults.model_name` no longer exists.
- The Popular set is the 8 ids in ADR-067 §4.2 and pass-2 MIN-008.
- The vendor CLIs (`codex`, Copilot CLI) are installed by the operator; Omnipus never installs them.
- No Windows-specific behaviour: file locking caveats in CLAUDE.md apply to `config.json` writes as they do to every store today.
- Out of scope: xAI sign-in implementation (gated), the catalog assembly job, context-window resolution, any agent-form change beyond rendering *needs a model*.

## Clarifications

### 2026-08-22

- Q: Can the operator be asked during this spec? -> A: No (Phase 1 gate taken as the ADR); the Ambiguity Warnings table is the operator's resolution sheet.
- Q: Is GitNexus available for this worktree? -> A: No index for this checkout and no MCP tools exposed; direct Read/Grep used, recorded under Existing Codebase Context.
- Q: Does `docs/reference/go-implementation/` exist? -> A: No; in-repo precedents substituted.
- Q: Undo after delete? -> A: None (operator decision 2026-08-22 via pass-2 MAJ-011).
- Q: `codex-cli` dispatch? -> A: Subprocess; token-reuse path renamed `openai-chatgpt` (pass-2 MAJ-013).
- Q: xAI? -> A: API-key only until xAI lists Omnipus (pass-2 MAJ-012).
- Q: The 18 ambiguities? -> A: All resolved by operator/coordinator (see the Ambiguity Warnings table); notable operator decisions: the default-backing / last connected provider is never deletable (#4); no ReAuth on delete (#18); template rows removed from `GET /providers` (#16); `pkg/auth` OpenAI device-code flow deleted (#8).
- Q: Window-source vocabulary? -> A: ADR-066 D8 (learning limits from provider errors) dropped by the operator; sources are `operator | live | catalog | floor` everywhere in this spec — no `learned`.
- Q: Spec review (BLOCK, 40 findings)? -> A: All verified against code and dispositioned in "Review Findings Disposition"; four coordinator decisions (CRIT-001 pair storage, CRIT-004 DELETE ordering, MAJ-007 auth posture, MAJ-011 error-state default) applied; ADR-068 §2.2/§2.4/§3/§7/§9 amended.
