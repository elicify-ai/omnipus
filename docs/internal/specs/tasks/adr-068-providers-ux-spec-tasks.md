# Task breakdown — ADR-068 spec (`adr-068-providers-ux-spec.md`)

- **Spec:** `docs/internal/specs/adr-068-providers-ux-spec.md` (Draft, all reviews resolved). ADR: `docs/internal/architecture/ADR-068-subscriptions-provider-deletion-and-provider-ux.md`.
- **Plan:** `docs/internal/specs/implementation-plan-adr-066-067-068.md` — this list is streams **B3 (providers backend)** and **B5 (SPA)**. B5 also carries **ADR-066 D9's Settings → Models screen** (tasks T068-29 / T068-30, traced to ADR-066 FR-036 / FR-037, US-11, B-44 / B-45).
- **Created:** 2026-08-23. Branch `feat/context-budget-and-tool-result-routing`.

## Landing-order dependencies (read first)

1. **Wave A contract commit (`A-CONTRACT`) lands before anything here.** S67 owns the FILE edit of every shared schema this spec consumes: `Provider.yaml` (six-value `status`, `auth_method`, `account_label`, `dependents`, `backs_default`, `updated_at`, plus S67's `protocol`/`custom`/`company`/`locality`/`cli_kind`), `ProviderUpdateRequest.yaml`, `Agent.yaml` (`needs_model`, `degraded_reason`), `ProbeProviderRequest.yaml` (`{id, auth, api_key?, model?, api_base?, protocol?}` — `antigravity` enum value gone), `DefaultModel.yaml`, `EntitlementResponse.yaml`, `ContextWindowSource.yaml` (S66), the four `LLMError` copies (`needs_provider`, `model_unassigned`, …), and the `inboundschemas/` twins (spec FR-035, X-26). Every task below compiles against those generated types.
2. **Greenfield deletions come first** (T068-01 … T068-05) — they shrink the surface everything else touches (plan §2, B3 row).
3. **Commit-level order across specs is 067 → 068 → 066** (X-29). Within this list, tasks are numbered in landing order; every `depends-on` precedes its dependents.
4. **Cross-spec dependency ids.** No `T067-xx` / `T066-xx` ids exist in the tree yet (`grep -rn "T067-" docs/internal/specs/` is empty on 2026-08-23; the sibling task lists are being produced in parallel). They are referenced here by **named placeholders**; the integrator maps each to the concrete id when the sibling lists land:
   | Placeholder | Meaning (S67 / S66 deliverable) |
   |---|---|
   | `A-CONTRACT` | Wave A coordinated contract commit (S67) |
   | `T067-FACTORY` | S67's atomic protocol-dispatch factory collapse (deletes `GetDefaultAPIBase`/`IsKnownProtocol`/`knownProtocols` and every vendor case; `cli` case selects by `cli_kind`) |
   | `T067-RESOLVER` | S67's exact `(provider, model)` catalog resolve (`catalog.Resolve`, `ErrUnknownProvider`) |
   | `T067-CATALOG-GET` | S67's `GET /api/v1/providers/catalog` (ETag, 2.0.0 projection incl. `tier`, `auth_methods`, `company`, `unsupported_reason`, `aliases`) |
   | `T067-ENTITLEMENT` | S67's `POST /api/v1/providers/{id}/entitlement` (replaces `refresh-models`; removes `model-capabilities`) |
   | `T067-NEEDS-PROVIDER` | S67's per-agent `degraded_reason: needs_provider` pre-turn gate |
   | `B2-RELEASE` | first real release of the assembly repo (`overrides/` rows incl. `github-copilot`); a fixture stands in until then |
   | `T066-SETTINGS-CONTEXT` | S66's `GET/PUT /api/v1/settings/context` + `ContextSettings` config (ADR-066 FR-036) |
   | `T066-RESOLVE-WINDOW` | S66's exported `ResolveWindow(provider, model)` (ADR-066 FR-001/FR-037) |
5. **Fly gates** are `runci.sh <ref> <gate>` with `gate ∈ quick | lint | go-test | contracts | spa`. Playwright/a11y (T068-31) and the binary-`strings` test run in the plan's Wave D via the `embed-build` + `e2e` gates; they are listed under `spa` here because that is the closest gate in the allowed set.
6. **Never run the full Go suite locally** (CLAUDE.md). One narrowly scoped `-tags goolm,stdjson -run '^TestName$' -p 1` test at most; everything else through Fly/CI.

Legend: **P0** = release-blocking per the spec's user-story priorities (US-1, 2, 3, 4, 9). Size S ≈ ≤ ½ day, M ≈ 1 day, L ≈ 2+ days for one agent.

---

## Tasks

### T068-01 — No-removed-providers CI gate (script + allow-list + wiring) — **P0**
- **Files:** create `scripts/check-no-removed-providers.sh`, `scripts/no-removed-providers.allow`; modify `.github/workflows/pr.yml`, `deploy/ci-worker/runci.sh` (`lint` gate), `Makefile` (`lint-no-removed-providers`, folded into `lint`). Model on `scripts/check-no-jpeg-screencast.sh`.
- **FRs:** FR-001 (allow-list enumerated there: ADR-066/067/068 + reviews, the three specs + reviews, cross-spec review, the allow file itself), FR-002 (id `claude-cli`, not the word "claude"), FR-002a (symbols `RequestDeviceCode|PollDeviceCodeOnce|OpenAIOAuthConfig|createCodexTokenSource|createCodexAuthProvider|createClaudeAuthProvider|createClaudeTokenSource`).
- **BDD:** Antigravity leaves no trace in the source tree; `claude-cli` leaves no trace in the source tree; OpenAI device-code flow leaves no trace.
- **Tests first:** TDD row 1 — the script itself plus its self-check (adding a fixture file containing `antigravity` outside the allow-list must make it exit 1; SC-001).
- **Gate:** `lint` (RED until T068-02/03 land — expected; that is the test-first state).
- **Depends-on:** none.
- **Size:** S.
- **DoD:** script exits 1 on the current tree, exits 0 after T068-02/03, self-check proves it can fail; no allow-list inside `pkg/`.

### T068-02 — Delete `antigravity` in one commit (code, OAuth config, seed, docs, catalog allow-list) — **P0**
- **Files:** delete `pkg/providers/antigravity_provider.go` + `_test.go`, `docs/ANTIGRAVITY_USAGE.md`; modify `pkg/providers/factory_provider.go` (the `case "antigravity"` + `AntigravityModelInfo`/`FetchAntigravityModels`), `pkg/providers/factory_test.go` rows, `pkg/auth/oauth.go` (`GoogleAntigravityOAuthConfig`, `OMNIPUS_GOOGLE_CLIENT_ID/SECRET` reads), `pkg/config/defaults.go` (remove the `antigravity/gemini-3-flash` template; `agents.defaults` default model stays zero), `pkg/config/config.go` protocol comment, `config/config.example.json` (Popular-tier API-key example), `pkg/providers/catalog/catalog_test.go` ("CLI executor / non-API-key ids" row), docs `docs/providers.md`, `docs/configuration.md`, `docs/README.md`, `docs/migration/model-list-migration.md`, `docs/internal/provider-endpoint-audit-2026-06.md`, `docs/internal/design/provider-refactoring*.md`.
- **FRs:** FR-001, FR-002, FR-040 (seed names no removed provider; default model zero value).
- **BDD:** Antigravity leaves no trace in the source tree; Config naming a removed provider fails generically (source half); Fresh install seeds no default model (seed half); Build and contract gates pass with the files gone.
- **Tests first:** TDD row 34 `TestDefaultsSeed_NoRemovedProvider` (`pkg/config`); removed-id rows folded into S67 T23's `ErrUnknownProvider` outline (row 4, X-33); T068-01's script.
- **Gate:** `quick` + `lint`.
- **Depends-on:** `A-CONTRACT` (the `antigravity` enum value leaves `ProbeProviderRequest.yaml` there), T068-01.
- **Size:** M.
- **Merge hazard:** the factory `case` removal overlaps `T067-FACTORY`'s rewrite of the same switch. The integrator lands this BEFORE the collapse commit; if the collapse lands first, this task shrinks to file/doc/seed removals (the cases are already gone). Never resolve by re-adding a case.
- **DoD:** `grep -ril antigravity pkg cmd src contracts config docs` returns only allow-listed records; tagged `go build`, `npm run typecheck`, `make verify-contracts` exit 0.

### T068-03 — Delete `claude-cli`, the store-OAuth ladders and the OpenAI device-code flow — **P0**
- **Files:** delete `pkg/providers/claude_cli_provider.go` (+ tests); modify `pkg/providers/factory_provider.go` (remove `case "claude-cli","claudecli"`, the `codexcli` alias, the id-keyed ladder `case "openai"`/`case "anthropic"` with `AuthMethod oauth|token` → `createCodexAuthProvider`/`createClaudeAuthProvider`/`createClaudeTokenSource`, `NewClaudeProviderWithTokenSource`), `pkg/providers/codex_provider.go` (delete `createCodexTokenSource`; `CodexCliAuth` drops `RefreshToken`), `pkg/providers/codex_cli_credentials.go` (reads `tokens.access_token` + `tokens.account_id` only), `pkg/auth/oauth.go` (`OpenAIOAuthConfig`, `RequestDeviceCode`, `PollDeviceCodeOnce`, `pollDeviceCode`, `RefreshAccessToken`, `BuildAuthorizeURL`, `ExchangeCodeForTokens` — delete the file if nothing remains; `go build` decides), `pkg/auth` tests, `pkg/config/config.go` (`ModelConfig.AuthMethod` closed set `api_key | sign_in`; validation rejects `oauth`/`token`), `pkg/providers/catalog/catalog_test.go` allow-list row, docs mentioning `claude-cli`.
- **FRs:** FR-002, FR-002a, FR-003 (deletion half; `knownProtocols`/`IsKnownProtocol` must not be referenced — X-15), FR-007 (struct no longer carries `refresh_token`).
- **BDD:** `claude-cli` is an unknown provider; `claude-cli` leaves no trace in the source tree; OpenAI device-code flow leaves no trace (grep half); Sign-in status dataset row 8 (`refresh_token` ignored).
- **Tests first:** T068-01's script; TDD row 4 rejection rows (`claude-cli`, `claudecli`, `codexcli` → `errors.Is(err, catalog.ErrUnknownProvider)`, no hint; `AuthMethod: oauth` → config validation error); the `pkg/auth` tests that named the deleted symbols are deleted, not skipped.
- **Gate:** `go-test` (scope `./pkg/providers/... ./pkg/auth/... ./pkg/config/...`).
- **Depends-on:** `A-CONTRACT`, T068-01. Same factory merge hazard as T068-02 (`T067-FACTORY`).
- **Size:** M.
- **DoD:** SC-001 grep for the seven symbols prints nothing under `pkg cmd`; `codex-cli` still dispatches to `NewCodexCliProvider`; build green.

### T068-04 — `GET /providers` returns configured rows only (template rows removed) — **P0**
- **Files:** modify `pkg/gateway/rest.go` (`HandleProviders` GET branch: drop the permanent "disconnected" template rows and the "final fallback: configured default model alias" fill), `pkg/gateway/provider_credential_degraded_test.go` fixtures (X-32).
- **FRs:** FR-011a, FR-043 (configured row the catalog does not know → `status: unknown-provider` with the generic text).
- **BDD:** GET /providers returns configured providers only; Config naming a removed provider fails generically (runtime half).
- **Tests first:** TDD row 15a `TestListProviders_ConfiguredOnly` (ten seed templates + one configured → one row; `DELETE` on a template id → 404 once T068-09 exists — assert 404/405 from the missing branch until then and tighten in T068-09); regression row 4 (`providerCredErrors` on configured rows only).
- **Gate:** `go-test` (scope `./pkg/gateway/ -run 'TestListProviders_ConfiguredOnly|TestProviderCredentialDegraded'`).
- **Depends-on:** `A-CONTRACT`, T068-02 (seed templates).
- **Size:** S.
- **DoD:** SC-014 second clause (exactly one row) passes; `createStartupProvider`/`defaultModelCredentialBlocked` untouched (they read `cfg.Providers` directly — verified in the spec).

### T068-05 — SPA: remove consumers of `model-capabilities`, `refresh-models` and the bundled catalog; delete `providerCatalog.ts` + TS emitter — **P0**
- **Files:** delete `src/lib/generated/providerCatalog.ts`, `pkg/providers/catalog/gen/main.go` TS emission (coordinate with S67 — whichever lands first deletes; never re-add), `catalog_test.go::TestCatalog_EmbedMatchesGeneratedTS`, `src/lib/__tests__/catalog-consistency.test.ts`; modify `src/lib/api.ts` (`fetchModelCapabilities`, `refreshProviderModels`), `src/lib/browserAnnotate.ts` + `.test.ts`, `src/lib/attachment-adapter.ts` + `.test.ts` (the D18 warn-and-proceed callers), `src/lib/providerMigration.ts`, `src/lib/constants.ts` (key-hint map keyed by id), `src/routes/onboarding.tsx`, `src/routes/-onboarding.test.tsx`, `src/lib/__tests__/onboarding-settings-parity.test.tsx`, `src/components/settings/ProvidersSection.tsx` + `.test.tsx` (delete the "template-provider filtering (realistic GET /providers shape)" describe; update fixtures for required `dependents`/`backs_default`).
- **FRs:** FR-037 (file + emitter deleted), FR-039 (`ProviderCatalogEntry.yaml` description — already rewritten by A-CONTRACT `36801b44`; the file itself is deleted by ADR-067 T067-02/T067-13), FR-031 (SPA half of the `refresh-models` → entitlement move; wiring lands in T068-27), FR-011a (SPA fixtures).
- **BDD:** SPA reads the catalog from the GET, not a bundle (grep half); Build and contract gates pass with the files gone.
- **Tests first:** SC-010 grep (`grep -rn "generated/providerCatalog" src pkg` empty); `npm run typecheck` + `npx vitest run` exit 0 with the importers re-pointed to a temporary `fetchProvidersCatalog` stub that T068-18 makes real.
- **Gate:** `spa`.
- **Depends-on:** `A-CONTRACT` (generated TS types), T068-02.
- **Size:** M.
- **DoD:** `ls src/lib/generated/providerCatalog.ts` fails; no SPA file references `fetchModelCapabilities`/`refreshProviderModels`/`PROVIDER_CATALOG`; vitest green.

### T068-06 — This spec's own contract files (unshared schemas + new paths) — **P0**
- **Amendment 2026-08-23 (A-CONTRACT):** `ProviderDeleteRequest.yaml`, `ProviderDeleteResponse.yaml`, `ProviderDependent.yaml`, `DefaultModelUpdateRequest.yaml` and the `DELETE /providers/{id}` + `GET/PUT /providers/default-model` paths **already exist** from commit `36801b44` — do not recreate; verify against `contracts/openapi.yaml` and create only what is missing.
- **Files:** create (unless already present per the amendment above) `contracts/components/schemas/ProviderDeleteRequest.yaml`, `ProviderDeleteResponse.yaml`, `ProviderDependent.yaml`, `DefaultModelUpdateRequest.yaml` (`additionalProperties: false`), `SignInStartResponse.yaml`, `SignInStatus.yaml`, `OnboardingProviderApiKey.yaml`, `OnboardingProviderSignIn.yaml`; modify `contracts/openapi.yaml` (paths `DELETE /providers/{id}`, `GET/PUT /providers/default-model`, `POST /providers/{id}/sign-in`, `GET /providers/{id}/sign-in/status`; `OnboardingCompleteRequest.provider` as inline `oneOf` + `discriminator: auth_method` per ADR-034), `contracts/components/schemas/ProbeProviderResponse.yaml` (`probed_model`), `OnboardingCompleteRequest.yaml` (`auth_method`), `pkg/gateway/inboundschemas/` twins of the inbound ones; regenerate `pkg/api/generated/`, `src/lib/api/generated/` via `scripts/gen-contracts.sh`; commit spec + artefacts atomically.
- **FRs:** FR-035 (the "this spec still writes" list), FR-036 (`probed_model`), FR-038 (assert the six `status` consts generated by `A-CONTRACT`).
- **BDD:** Contracts regenerate cleanly.
- **Tests first:** TDD row 24 `TestContractsRegenerateClean` (`make verify-contracts`); `pkg/api/generated/contract_test.go` round-trip for the new schemas; SC-012 (six `gen.ProviderStatus*` consts).
- **Gate:** `contracts`.
- **Depends-on:** `A-CONTRACT`.
- **Size:** M.
- **DoD:** `make verify-contracts` exits 0 with zero drift; `ProbeProviderRequest.yaml` has no `enum` under `id`; no hand-written wire struct anywhere (Constraint #8 lint).

### T068-07 — Default model as a pair: `agents.defaults.default_model`, delete `model_name` + guards, exact `GetModelConfig` — **P0**
- **Files:** modify `pkg/config/config.go` (`AgentDefaults.DefaultModel {Provider, Model}` `json:"default_model"`; delete `ModelName` and its alias semantics; `GetModelConfig`/`findMatches` resolve the pair exactly; `ModelConfig.UpdatedAt *time.Time` `json:"updated_at,omitempty"`), `pkg/config/config_test.go` and every fixture that set `model_name` (rewrite, greenfield — a test still passing `model_name` is a false green), `pkg/gateway/gateway.go` (delete both `ModelName == ""` guards; `createStartupProvider`, `defaultModelCredentialBlocked` read the pair), `pkg/agent/model_resolution.go::buildModelListResolver`, `pkg/config/config.go::resolveFallbackProvider`, `pkg/gateway/rest_onboarding.go` completion (writes `default_model` once), `pkg/gateway/rest.go` PUT branch (stamps `UpdatedAt`).
- **FRs:** FR-018 (persistence + exact resolution), FR-020, FR-040 (zero value on fresh install), Data Constraints (`Provider.updated_at`).
- **BDD:** Fresh install seeds no default model (`DefaultModel` zero; `GET …/default-model` 404 `{"error":"no default model"}` once T068-11 exists); Onboarding complete with sign-in (the reload-guard `And` — api_key variant here, sign-in variant in T068-16); regression dataset row 7.
- **Tests first:** `TestDefaultsSeed_NoRemovedProvider` (extend: `Agents.Defaults.DefaultModel` zero), the `config_test.go` rewrites, TDD row 22's `ReloadProviderAndConfig` does-not-overwrite assertion (FR-020), S67 `TestSeeds_CanonicalProviderIDs` + S66 `TestConfig_NoContextWindowDefaultKey` must still pass (X-29).
- **Gate:** `go-test` (scope `./pkg/config/... ./pkg/agent/... ./pkg/gateway/ -run 'Onboarding|Seed|DefaultModel|Config'`).
- **Depends-on:** `A-CONTRACT` (`DefaultModel.yaml`), T068-02, T068-06.
- **Size:** L.
- **Impact (spec table, HIGH):** d=1 — `gateway.go` L1658/L5048 guards, `createStartupProvider`, `defaultModelCredentialBlocked`, `buildModelListResolver`, `resolveFallbackProvider`, every `model_name` fixture; d=2 — every agent whose model resolves via the default; onboarding completion. Run GitNexus `impact` on `GetModelConfig` and `AgentDefaults` before editing (plan 0.1 re-index).
- **DoD:** `config.json` schema has no `model_name`; `GetModelConfig(pair)` resolves exactly; onboarding completion writes the pair once and reload never overwrites it.

### T068-08 — Dependents computation, `Agent.needs_model`, and the new `GET /providers` row fields — **P0**
- **Files:** create `pkg/gateway/provider_dependents.go` (+ `_test.go`); modify `pkg/gateway/rest.go` (GET branch emits `dependents[]`, `backs_default`, `updated_at`, `auth_method`, `account_label` — zero values where T068-14 has not yet landed), agent handlers in `pkg/gateway/rest.go` (`listAgents`/`getAgent`/`updateAgent` derive `needs_model` = primary `model` empty OR its `provider` not configured), `pkg/gateway/rest_agent_provider_test.go`.
- **FRs:** FR-012 (every reference: primary, fallback, passthrough rule 3 via `resolveFallbackProvider`, `default_model`, `image_model`, `recap_model`/`recap_fallback_models[]`, `voice.model_name`), FR-014 (backend), FR-038 (rows emit the six-value enum).
- **BDD:** Dependents are listed and left without a model (list half); Fallback references are removed and listed (list half); Passthrough-resolved agents are dependents; Dataset "Dependents computation" rows 1–12.
- **Tests first:** TDD row 3 `TestProviderDependents` + `TestProviderDependents_EnumeratesEveryModelField` (reflects over `AgentDefaults`/`VoiceConfig` `*Model*` fields and fails on an unlisted one); regression `TestAgentProvider_NeedsModelDerived` (`needs_model` false for every existing fixture).
- **Gate:** `go-test` (scope `./pkg/gateway/ -run 'TestProviderDependents|TestAgentProvider'`).
- **Depends-on:** T068-04, T068-07.
- **Size:** M.
- **DoD:** both dependents tests green; `GET /providers` and `GET /agents` validate against the generated schemas (`contract_test.go`).

### T068-09 — `DELETE /api/v1/providers/{id}`: five idempotent steps, guards, audit, reload — **P0**
- **Files:** modify `pkg/gateway/rest.go` (`HandleProviders` DELETE branch dispatched after the reserved literals; `requireAdminAuthz` inline; recompute dependents/`backs_default` under `configMu`; steps (1) clear dependents in the entity store + drop fallback entries, (2) remove the row, (2b) prune `ContextSettings.model_overrides[]` for the id — gated on `T066-SETTINGS-CONTEXT` existing, wired by T068-17 otherwise, (3) `credentials.Store.Delete("<id>_API_KEY")` with `NotFoundError` = success, (3b) entitlement-cache eviction hook (T068-17), (4) audit `provider.deleted` with the ref NAME, dependents count, default change, (5) `triggerReloadAndWaitOutcome`), `pkg/audit/events.go` (`provider.deleted`, `provider.default_model.changed`, `provider.credential_swept`), `pkg/gateway/rest_providers_delete_test.go` (new).
- **FRs:** FR-010, FR-011 (404 / 503 locked / 503 bypass / 409 backs-default / 400 same-id or non-connected `new_default`; last connected provider never deletable), FR-013, FR-042 (DELETE verb inline gate: 401 always, no pre-onboarding exception).
- **BDD:** Remove an unused provider after one confirmation; Dependents are listed and left without a model; Default-backing provider requires an inline new default (server half); DELETE without replacement on the default-backing provider is refused; DELETE partial failure leaves no orphaned secret and a retry succeeds; Server recomputes dependents under the lock; Other provider in error state still offered as new default (server accepts); Reserved literals are never provider ids (DELETE rows); DELETE and default PUT require an authenticated admin (DELETE half); Removing an unconfigured provider returns 404; Removal refused while the credential store is locked; Removing the only provider is refused (server half); Fallback references are removed and listed; Dataset "DELETE bodies" rows 1–14.
- **Tests first:** TDD rows 10 `TestDeleteProvider_Unused200`, 10a `TestDeleteProvider_PartialFailureNoOrphanSecret` (inject config-write failure; inject entity-update failure on agent 2/3), 10c `TestDeleteProvider_RecomputesUnderLock` (concurrent agent PUT; DELETE×2 → 200 then 404), 10d `TestDeleteProvider_AuthPosture` (DELETE half), 11 `TestDeleteProvider_DependentsLeftWithoutModel`, 12 `TestDeleteProvider_DefaultRequiresReplacement409`, 13 `TestDeleteProvider_WithNewDefault` (default changed THEN row removed; reload waited; `error`-state `new_default` accepted), 14 `TestDeleteProvider_404_503_Bypass503`, 15 `TestDeleteProvider_OnlyProviderRefused409`, 16 `TestDeleteProvider_FallbackRemoved`.
- **Gate:** `go-test` (scope `./pkg/gateway/ -run 'TestDeleteProvider'`).
- **Depends-on:** T068-06, T068-08; the `new_default` path reuses T068-11's validator — land T068-11 first or share the helper in this task and have T068-11 consume it (integrator's call; the spec puts the default change before row removal either way).
- **Size:** L.
- **DoD:** SC-003 (key gone synchronously; injected step-2 failure → retry 200; one audit entry per completed run) and SC-014 first clause (409 in 10/10 runs) pass; log line `rest: provider removed` never carries the key.

### T068-10 — Startup sweep of orphaned `<id>_API_KEY` credentials — **P0**
- **Files:** modify `pkg/gateway/gateway.go` (boot step after config + store unlock: delete any `<id>_API_KEY` whose provider row is gone; one INFO line; audit `provider.credential_swept`; names not matching the pattern untouched), `pkg/gateway/credential_sweep_test.go` (new).
- **FRs:** FR-010 (last clause).
- **BDD:** Startup sweep removes an orphaned credential.
- **Tests first:** TDD row 10b `TestCredentialSweep_RemovesOrphans`.
- **Gate:** `go-test` (scope `./pkg/gateway/ -run 'TestCredentialSweep'`).
- **Depends-on:** T068-07 (pair-based provider rows), T068-09 (shares the credential-name rule and the audit event).
- **Size:** S.
- **DoD:** boot with an orphan removes it and emits the audit entry; a non-matching name survives; no effect on a clean store.

### T068-11 — `GET/PUT /api/v1/providers/default-model` (`adminWrap`), validation, audit, turn-time oracle; reserved-literal id validation — **P0**
- **Files:** modify `pkg/gateway/rest.go` (own route registered with `adminWrap` BEFORE the `/api/v1/providers/` prefix handler; GET returns `DefaultModel` — `context_window`/`window_source`/`window_unknown` from `T066-RESOLVE-WINDOW` when present, otherwise omitted; 404 `{"error":"no default model"}` when zero; PUT validates provider configured and `connected | signed_in`, model in catalog via `T067-RESOLVER` unless `custom: true` or `locality: local` (any non-empty model ≤ 256, no live call), writes the pair under `configMu`, audit `provider.default_model.changed` with old/new pairs, `TriggerReload` + wait, 500 on reload failure), shared provider-id validator rejecting `catalog`, `default-model` (and `model-capabilities` until S67 removes it) everywhere an id is accepted (PUT `{id}`, DELETE, probe, onboarding complete), `pkg/gateway/rest_default_model_test.go` (new; mirrors `rest_default_agent_singleton_test.go`).
- **FRs:** FR-018, FR-019 (data for the card — window/source `—` until S66), FR-042 (route via `adminWrap`: 401 / 503), Machine-Verifiable Constraints (reserved literals, MAJ-002).
- **BDD:** Change default model takes effect on the next turn (server half); Default-model PUT validation (outline, all rows); Reserved literals are never provider ids (`PUT /providers/default-model` with a `ProviderUpdateRequest` body → 400, nothing created; `DELETE /providers/catalog` → 404; 405 on other verbs); DELETE and default PUT require an authenticated admin (PUT half); Dataset "Default-model PUT" rows 1–13.
- **Tests first:** TDD rows 17 `TestDefaultModel_PutResolvesAtTurnTime` (PUT → one turn against a stub provider → the session transcript line carries the new pair; `config.json` has no `model_name`; audit entry), 18 `TestDefaultModel_PutValidation`, 10d auth rows for the route, reserved-literal rows of row 2.
- **Gate:** `go-test` (scope `./pkg/gateway/ -run 'TestDefaultModel|TestProbeProviderID'`).
- **Depends-on:** T068-06, T068-07, `T067-RESOLVER`; `T066-RESOLVE-WINDOW` is a soft dependency (fields omitted until it lands — resolution #15).
- **Size:** L.
- **DoD:** SC-004 (transcript equals the PUT body 10/10 without restart), SC-015 (401/503/404/400 rows) pass.

### T068-12 — Pre-turn gate: `model_unassigned` typed refusal and the three-stage gate order — **P0**
- **Files:** modify `pkg/agent/turn.go` / `pkg/agent/loop.go` (pre-turn check ordered `needs_provider` → `model_unassigned` → S66's `context_window_unknown`; message *"This agent has no model. Pick one in the agent's settings."*, attribution `config`; zero provider calls), `pkg/agent/turn_pre_gate_test.go` (new); SPA copy catalogue is generated from the asyncapi block (`A-CONTRACT`), nothing hand-written.
- **FRs:** FR-015, WS/LLMError constraint (precedence, MAJ-008/X-02/X-09).
- **BDD:** Turn to an agent without a model is refused, not re-pointed.
- **Tests first:** TDD rows 19 `TestTurn_ModelUnassignedTypedError`, 23a `TestTurn_PreTurnGateOrder` (unknown provider AND empty model → `needs_provider`; configured provider + empty model → `model_unassigned`; S66's code only after both pass — the S66 stage is asserted once `T066` lands, skipped not faked until then, X-27 pattern); regression `websocket_provider_refusal_test.go`, `llm_error_codes_test.go::TestContract_LLMError_AllClassifierCodesRoundTrip`, `llm_error_catalogue_test.go` still green.
- **Gate:** `go-test` (scope `./pkg/agent/ -run 'TestTurn_ModelUnassigned|TestTurn_PreTurnGateOrder'` and `./pkg/api/generated/`).
- **Depends-on:** `A-CONTRACT` (the code in all four `LLMError` copies), T068-08 (`needs_model` derivation), `T067-NEEDS-PROVIDER` (gate stage 1).
- **Size:** M.
- **DoD:** SC-013 (0 provider calls; `needs_provider` wins when both apply).

### T068-13 — Probe handler: free-string id validated against the catalog, `auth`, optional `api_key`/`model`, `probed_model`, SSRF — **P0**
- **Files:** modify `pkg/gateway/rest_onboarding.go` (id valid iff in the served catalog or `api_base` + `protocol` both present; 400 `{"error":"unknown provider \"<id>\"","field":"id"}` with no id list; `api_base` through `ssrfChecker.CheckURL` → 422; `auth` required; `api_key` required iff `auth = api_key`; `model` verbatim else the provider's first Recommended catalog model; response `probed_model`; `openai-chatgpt` with `auth: api_key` → 400 "provider does not support api_key"), `pkg/gateway/rest_onboarding_probe_test.go`.
- **FRs:** FR-036, FR-029 (backend half: the probe validates the exact pick).
- **BDD:** Probe provider id validation (all outline rows except the sign-in rows, which T068-16 adds); Reserved literals are never provider ids (probe row); Dataset "Provider id" rows 1–12.
- **Tests first:** TDD row 2 `TestProbeProviderID_Validation` (table over the outline incl. reserved literals, SSRF row, `antigravity`/`claude-cli` rows asserting the generic echo and no list, maxLength 64/256).
- **Gate:** `go-test` (scope `./pkg/gateway/ -run 'TestProbeProviderID_Validation'`).
- **Depends-on:** `A-CONTRACT` (`ProbeProviderRequest` shape), T068-06 (`probed_model`), T068-11 (shared id validator), `T067-RESOLVER` / `T067-CATALOG-GET` (membership + first Recommended model).
- **Size:** M.
- **DoD:** SC-011 (no `enum` under `id`; `api_key` optional, `auth` required; 400 body is `unknown provider "<id>"` with no list); regression rows 1–3 of `provider_validation_test.go` unchanged.

### T068-14 — `codex-cli` (subprocess) and `openai-chatgpt` (store-backed OAuth): factory dispatch, sign-in start/status/poll/import/sign-out routes — **P0** *(re-scoped 2026-08-23, ADR-068 §8b)*
- **Files:** modify `pkg/providers/factory_provider.go` (under S67's protocol dispatch: `cli` + `cli_kind: codex` → `NewCodexCliProvider`; `openai-chatgpt` = `openai-compatible` with `NewCodexProviderWithTokenSource(NewStoreOAuthTokenSource("openai", …))` from T068-32), `pkg/providers/codex_cli_credentials.go` (read-only import source only), `pkg/gateway/rest.go` + new `pkg/gateway/rest_sign_in.go` (`POST /providers/{id}/sign-in` → `cli_login` or `device_code` per FR-008, `withRateLimit`; `POST …/sign-in/poll` FR-044; `GET …/sign-in/status` FR-009 incl. `pending`; `POST /providers/openai-chatgpt/sign-in/import` FR-047; `DELETE …/sign-in` FR-048; `GET /providers` rows use status), `pkg/gateway/rest_sign_in_test.go` (new; vendor endpoints faked with `httptest`).
- **FRs:** FR-003 (construction half), FR-004, FR-007, FR-008, FR-009, FR-044, FR-047, FR-048, FR-049 (server half: routes work for any `sign_in` row; xAI config-gated).
- **Tests first:** `TestFactory_CliKindDispatch`; `TestSignInStart_CLILogin` (codex-cli/copilot shape); `TestSignInStart_DeviceCode` (openai-chatgpt → link + code, device code never in the body); `TestSignIn_RefusedForKeyOnly400`; `TestSignInPoll_PendingThenSignedIn` (store written before 200); `TestSignInPoll_Expired404`; `TestSignInStatus_Store` (fresh/expired/pending); `TestSignInImport_ReadOnly` (bytes + mtime unchanged); `TestSignOut_DeletesOAuth`; `TestSignInStart_XAI_NoClientID400`.
- **Gate:** `go-test` (scope `./pkg/providers/ -run 'TestFactory_CliKindDispatch'` then `./pkg/gateway/ -run 'TestSignIn|TestSignOut'`).
- **Depends-on:** `T067-FACTORY`, T068-03, T068-06, T068-08, **T068-32**, T068-34 (contract).
- **Size:** L.
- **DoD:** both ids construct the right type; refresh tokens never on the wire; `grep -rn refresh_token contracts/ src/` empty; import never writes `auth.json`.

### T068-15 — `github-copilot` subprocess provider + Copilot sign-in status — P1
- **Files:** create `pkg/providers/copilot_cli_provider.go`, `_test.go`, `_integration_test.go` (fake `copilot` on PATH, pattern of `codex_cli_provider_integration_test.go`); modify `pkg/providers/factory_provider.go` (`cli_kind: copilot` → `NewCopilotCliProvider`), `pkg/gateway/rest.go` sign-in start/status (`copilot login`; binary missing → `disconnected` + hint; not logged in / logged in / expired mapping; `account_label` = GitHub login when reported).
- **FRs:** FR-003 (`copilot` constructor), FR-004 (`sign_in` present for `github-copilot`), FR-009 (states), Existing Codebase Context "GitHub Copilot subprocess provider" contract (X-14). ADR-066 window exemption for `cli_kind` rows is S66's rule — not implemented here.
- **BDD:** Auth methods offered per provider (`github-copilot` row — data half); Signed-in row / Expired session (Copilot variants).
- **Tests first:** TDD row 23b `TestCopilotCliProvider_ParsesOutput`, `TestCopilotCliProvider_MissingBinary`, `TestSignInStatus_Copilot`; Copilot rows added to row 22a `TestProbeProvider_SignIn` (in T068-16).
- **Gate:** `go-test` (scope `./pkg/providers/ -run 'TestCopilotCli'` and `./pkg/gateway/ -run 'TestSignInStatus_Copilot'`).
- **Depends-on:** T068-14, `B2-RELEASE` (the `github-copilot` catalog row in `overrides/` — a fixture row stands in until the release).
- **Size:** M.
- **UNVERIFIED (spec-flagged, confirm against the installed binary BEFORE writing the fixture):** the Copilot CLI's non-interactive prompt/output flags, and whether it offers an auth-status command or a login state file. The fake binary pins whatever contract is adopted; record the finding in the task's commit message.
- **DoD:** fake-binary tests green; missing binary → `disconnected`; no Go SDK module added (Constraint #1: `go.mod` diff empty).

### T068-16 — Sign-in probe (`auth: sign_in`) and onboarding completion with `auth_method` (`oneOf`) — **P0**
- **Files:** modify `pkg/gateway/rest_onboarding.go` (probe: `auth: sign_in` + `api_key` → 400; no saved CLI login / Copilot session → 400 `{"error":"not signed in","field":"auth"}`; else one completion through the subprocess / saved-token path with the chosen `model`; completion handler validates the discriminated `OnboardingProviderApiKey | OnboardingProviderSignIn` body, persists `auth_method`, writes `default_model`), `pkg/gateway/rest_onboarding_probe_test.go`, `pkg/gateway/rest_onboarding_complete_test.go`.
- **FRs:** FR-036 (sign-in clauses, CRIT-002), FR-029 (backend: probe of the chosen auth method), FR-035 (`oneOf` consumption), FR-020 (completion writes the pair once).
- **BDD:** Probe provider id validation (sign-in rows: `codex-cli` logged in → 200 `probed_model`; not signed in → 400 `field=auth`; `openai-chatgpt` fresh `auth.json` → 200; `openrouter` + `sign_in` → 400; `openrouter` + `api_key` without key → 400 `field=api_key`); Onboarding complete with sign-in.
- **Tests first:** TDD rows 22 `TestOnboardingComplete_AuthMethod`, 22a `TestProbeProvider_SignIn` (fake `codex` on PATH; Copilot rows once T068-15 lands).
- **Gate:** `go-test` (scope `./pkg/gateway/ -run 'TestOnboardingComplete_AuthMethod|TestProbeProvider_SignIn'`).
- **Depends-on:** T068-13, T068-14, T068-07; T068-15 for the Copilot rows only.
- **Size:** M.
- **DoD:** sign-in without key 200 and pair written; sign-in with key 400; api_key without key 400; `ReloadProviderAndConfig` leaves `default_model` unchanged.

### T068-17 — Entitlement cache key + eviction hooks (DELETE 3b, key-changing PUT) and `model_overrides` pruning (DELETE 2b) — P2
- **Files:** modify `pkg/gateway/rest.go` (cache keyed `SHA-256(providerID + ":" + credentialRefName)` — never the secret; evict on DELETE and on a PUT that changes the key; a PUT that only bumps `updated_at` does not evict; DELETE step 2b removes `ContextSettings.model_overrides[]` rows for the id), `pkg/gateway/rest_entitlement_test.go`.
- **FRs:** FR-031 (gateway cache + eviction, X-03/X-21), FR-010 steps 2b (cross-spec Q3) and 3b.
- **BDD:** Check with my account greys unavailable models (server half: one upstream call, second call `cached: true`); Check with my account upstream failure (429 → S67's 502 body).
- **Tests first:** TDD row 23 `TestEntitlement_IntersectsAndCaches` (annotations, one upstream call, `cached: true`, key derivation, eviction on DELETE and key-changing PUT; no eviction on `updated_at`-only PUT).
- **Gate:** `go-test` (scope `./pkg/gateway/ -run 'TestEntitlement'`).
- **Depends-on:** `T067-ENTITLEMENT` (the route and its intersection logic are S67's), T068-09, `T066-SETTINGS-CONTEXT` for step 2b (land 2b as a follow-up edit in this task when S66's struct exists; the DELETE task leaves a named hook).
- **Size:** S.
- **DoD:** second click returns `cached: true` with no upstream request; a DELETE leaves no cache entry and no `model_overrides` row for the id.

### T068-18 — SPA catalog client (`GET /providers/catalog`, ETag re-validation) + 190-entry test fixture — P1
- **Files:** modify `src/lib/api.ts` (`fetchProvidersCatalog` using generated types; `If-None-Match` re-validate on Settings open and every 15 min; TanStack Query cache), create `src/test/fixtures/providers-catalog.json` (190 entries; 8 `tier: popular`; `bedrock` `tier: unsupported` + `unsupported_reason: cloud-iam`; `zai`/`zai-coding-plan`/`zhipuai` variants sharing `company`; aliases incl. `glm-coding`, `智谱`; `github-copilot`, `codex-cli`, `openai-chatgpt` with `sign_in`; models with `release_date`, `tool_call`, `context_window`, `max_output_tokens`, `input_modalities`, `status`), replace T068-05's stub.
- **FRs:** FR-037 (SPA half: reads the GET, A-1 cadence, ≤ one `200` per ETag), FR-022/FR-025 (data the fixture must carry), Integration Boundaries "Providers catalog".
- **BDD:** SPA reads the catalog from the GET, not a bundle (network half, asserted in T068-31); Catalog unavailable in the picker (error propagation).
- **Tests first:** `src/lib/api.test.ts` cases: `If-None-Match` sent on re-validation, 304 keeps the cached document, 500 surfaces an error the picker renders (no spec-named test — traced to FR-037's MAJ-004 assertion and the "Catalog unavailable" scenario).
- **Gate:** `spa`.
- **Depends-on:** `A-CONTRACT` (catalog TS types), T068-05; `T067-CATALOG-GET` for real data (mock until then — plan B5 row).
- **Size:** M.
- **DoD:** fixture committed and generated once from the real snapshot when `B2-RELEASE` exists (regenerate then); typecheck + vitest green.

### T068-19 — Pure TS models: picker data model, region inference, model ordering + Recommended chips, draft guard — P1
- **Files:** create `src/components/providers/provider-picker-model.ts` + `provider-picker-model.test.ts`, `src/components/providers/region-inference.ts` + `.test.ts`, `src/components/ui/model-ordering.ts` + `model-ordering.test.ts`, `src/hooks/use-draft-guard.ts` + `use-draft-guard.test.ts`.
- **FRs:** FR-022 (Popular by `company` with a `tier: popular` variant, catalog order; Recent by `updated_at` desc max 3; collapsed until trimmed query non-empty; Custom last), FR-023 (letter groups A–Z then `#`), FR-024 (search over `company`, `name`, plan, region, alias; literal; case-insensitive), FR-027 (inference map), FR-030 (vendor group → `release_date` desc, undated last id asc; chips: `tool_call && context_window ≥ 128000 && status = active`, ≤ 3, tie `release_date` desc then id asc), FR-033 (whitespace = empty; saved = clean).
- **BDD:** Picker opens with 8 Popular tiles (model half incl. the fixture-change re-render); Search expands and filters the full list; Custom endpoint is last; Region inferred from locale (all 9 rows); Models ordered by vendor then release date; At most three Recommended chips per provider; Close behaviour by draft state; Datasets "Picker search" 1–10 and "Model ordering and chips" 1–10.
- **Tests first:** TDD rows 5 `TestRecommendedChipSelection`, 6 `TestModelOrdering`, 7 `TestRegionFromLocale`, 8 `TestPickerModel`, 9 `TestDraftGuard`.
- **Gate:** `spa`.
- **Depends-on:** T068-18 (fixture + types).
- **Size:** M.
- **DoD:** all five unit files green; no React in these modules (pure functions).

### T068-20 — Shared `ProviderPicker` component (cmdk, virtualised, unsupported-disabled, Custom endpoint, error state) — P1
- **Files:** create `src/components/providers/ProviderPicker.tsx`, `ProviderPicker.test.tsx`, `provider-picker-search.test.ts`, `CustomEndpointPanel.tsx` (fields `id`, `api_base`, `protocol` select, key; saved row recognised by `custom: true`); TS discriminated union `tile | recent | row | custom`; `@tanstack/react-virtual` + cmdk `Command` with `shouldFilter={false}`.
- **FRs:** FR-021 (one component, under `src/components/providers/`), FR-022, FR-023 (`aria-setsize`/`aria-posinset`; ≤ visible + 10 rows), FR-024 (Custom endpoint panel), FR-025, FR-026 (Home/End by index via `scrollToIndex` then focus), FR-037 (error state with Retry; Custom still selectable), Accessibility rows (keyboard, `role="group"`, no colour-only state; Phosphor only — no emoji).
- **BDD:** Picker opens with 12 Popular tiles and a collapsed list (amended 2026-08-25, catalog repo commit `b50f5a6`: `groq` demoted to standard, `ollama` promoted); Search expands and filters the full list; Expanded list is letter-grouped and virtualised (≤ 22 in the 480 px / 40 px fixture); Unsupported provider is visible, disabled, with reason (`cloud-iam` → "needs request signing", never the raw enum); Custom endpoint is last; Recently used row appears; Keyboard-only selection; Catalog unavailable in the picker.
- **Tests first:** TDD rows 25 `ProviderPicker.test.tsx`, 25a `provider-picker-search.test.ts`.
- **Gate:** `spa`.
- **Depends-on:** T068-19.
- **Size:** L.
- **DoD:** SC-005 vitest bound (≤ 22) holds; `data-testid="picker-popular-*"` × 12 and `picker-all-toggle` with `aria-expanded` present; `performance.mark` recorded on open.

### T068-21 — Second-level panel: plan/region `aria-pressed` groups with locale default, and `AuthMethodControl` (segmented + OpenAI radio pair) — P1
- **Files:** create `src/components/providers/ProviderDetailPanel.tsx`, `AuthMethodControl.tsx`, `AuthMethodControl.test.tsx`; modify `ProviderPicker.tsx` (opens the panel on select).
- **FRs:** FR-005 (sign-in control only where `auth_methods` has `sign_in`, pre-selected), FR-006 *(amended)* (`openai-chatgpt` **default** radio with *"Uses your ChatGPT plan's included usage"*; `codex-cli` second; the *Sign in* button opens T068-33's dialog), FR-027 (copy *"Detected: <Region> — change"* / *"Region — change"*), FR-028 (control inside the panel; no extra step), Qualitative prohibitions (no Anthropic/Google sign-in anywhere; xAI key-only, no forward-looking copy).
- **BDD:** Auth methods offered per provider (all 5 rows); OpenAI sign-in offers two named providers with the subprocess as default; Region inferred from locale (render half); Auth-method control keeps onboarding at three steps (segment switch reveals key field without navigation).
- **Tests first:** TDD row 26 `AuthMethodControl.test.tsx`; `ProviderPicker.test.tsx` region rows.
- **Gate:** `spa`.
- **Depends-on:** T068-20.
- **Size:** M.
- **DoD:** Anthropic/Google panels contain no sign-in control in the DOM; xAI shows one only when its catalog row carries `sign_in`; saving with OpenAI sign-in untouched persists `openai-chatgpt`.

### T068-22 — `ModelSelector`: vendor/date ordering, Recommended chip, virtualisation above 100, "Model for your first agent" label — P1
- **Files:** modify `src/components/ui/model-selector.tsx`, `src/components/ui/model-selector.test.tsx` (extend); `@tanstack/react-virtual` above 100 items; `aria-setsize`/`aria-posinset`; optional `filterProviders` prop (connected-only / single provider) consumed by T068-25.
- **FRs:** FR-030, FR-029 (label + empty value + Recommended row unselected), FR-019 (selector filtered to connected providers — prop only here).
- **BDD:** Onboarding model field is empty and labelled (component half); Model selector virtualisation threshold (0 / 1 / 100 / 101 / 359); Models ordered…; At most three Recommended chips… (render half).
- **Tests first:** TDD row 29 `model-selector.test.tsx` (100 vs 101 rows; label; no value; "No models" empty state).
- **Gate:** `spa`.
- **Depends-on:** T068-19.
- **Size:** M.
- **DoD:** SC-006 (359 → ≤ visible + 10; 100 → exactly 100); agent form (the other consumer) unchanged in behaviour for ≤ 100 models.

### T068-23 — Draft-key preservation in the provider config sheet — P1
- **Files:** modify `src/components/settings/ProvidersSection.tsx` (`handleClose`: Esc/overlay with a dirty unsaved key → stay open + inline *"Discard key?"* `Discard` / `Keep editing`; explicit Cancel clears without prompt; focus stays inside the sheet), `ProvidersSection.test.tsx` (new describes), uses `use-draft-guard`.
- **FRs:** FR-033, Accessibility (focus containment 3.2.1).
- **BDD:** Esc with a dirty key keeps the sheet open; Discard clears the draft; Close behaviour by draft state (5 rows).
- **Tests first:** TDD row 27 (`ProvidersSection.test.tsx` draft describes), row 9 `TestDraftGuard` (T068-19).
- **Gate:** `spa`.
- **Depends-on:** T068-19, T068-05 (fixtures already migrated).
- **Size:** S.
- **DoD:** the five outline rows pass; no key value is logged or persisted client-side beyond component state.

### T068-24 — Onboarding step 3 on the shared picker: auth control, no pre-selected model, probe of the chosen method, re-probe on change — P1
- **Files:** modify `src/routes/onboarding.tsx` (replace `PRIORITY_COMPANIES` + tile grid + L2 panel with `ProviderPicker` + `ProviderDetailPanel`; *Check sign-in* for sign-in providers → probe `{auth: sign_in, model}`; key path → probe `{auth: api_key, api_key, model}`; model field via `ModelSelector` labelled *"Model for your first agent"*, empty; *Finish* disabled until the probe for the exact model passes; model change re-probes; completion body is the `OnboardingProviderApiKey | OnboardingProviderSignIn` variant; three-step tracker unchanged; CLI-missing hint *"`codex` not found on this machine"*), `src/routes/-onboarding.test.tsx`, `src/lib/__tests__/onboarding-settings-parity.test.tsx`.
- **FRs:** FR-021, FR-028, FR-029, FR-005, FR-036 (client side of `auth`/`model`/`probed_model`), FR-035 (generated `oneOf` types only).
- **BDD:** Auth-method control keeps onboarding at three steps; Onboarding model field is empty and labelled (route half: probe sent with `model`, re-probe disables Finish); Fresh install seeds no default model (step 3 renders the model field empty); Onboarding complete with sign-in (client body); Catalog unavailable in the picker (onboarding proceeds via Custom endpoint).
- **Tests first:** `-onboarding.test.tsx` cases per the scenarios above (spec names only the Playwright `onboarding.spec.ts`, row 31, which T068-31 adds; the vitest cases are its component-level counterparts).
- **Gate:** `spa`.
- **Depends-on:** T068-20, T068-21, T068-22, T068-13, T068-16 (server contract for the probe/complete bodies — mock responses from the generated zod schemas until the backend lands).
- **Size:** L.
- **DoD:** SC-008 (3 steps; Finish disabled until a model is chosen; completion request carries `auth_method`); no `PRIORITY_COMPANIES` constant remains.

### T068-25 — Settings → Providers: Default-model card, Set-as-default row action, `RemoveProviderDialog`, shared picker replaces `ProviderPickerSheet` — **P0**
- **Files:** create `src/components/settings/DefaultModelCard.tsx`, `RemoveProviderDialog.tsx`, `RemoveProviderDialog.test.tsx`; delete `src/components/settings/ProviderPickerSheet.tsx`; modify `ProvidersSection.tsx` (card first: `provider · model · window · source` from `GET /providers/default-model`, `—` when fields absent, *No context length* copy + link to Settings → Models → Model overrides pre-filled with `<provider>/<model>` when `window_unknown`; *Change* → `ModelSelector` filtered to `connected | signed_in`; row *Default* marker; row action *Set as default model…* pre-filtered to that provider; footer-left text-tier *Remove provider*; dialog: `role="alertdialog"`, `aria-labelledby`/`aria-describedby`, focus on *Cancel*, Esc = Cancel, title *"Remove <Display name>? Its key will be deleted."*, dependents grouped by role (*"These agents will be left without a model"*, *"uses as fallback"*, *"resolved through <Name>"*, recap), inline *New default model* selector over other configured providers with status shown (`error`/`expired` included, `unknown-provider` excluded), *Remove* disabled until chosen, only-provider copy with *Remove* permanently disabled; no Undo, no toast action, no client-side key retention; DELETE response is authoritative for the post-dialog state), `ProvidersSection.test.tsx`, `src/lib/api.ts` (`deleteProvider`, `getDefaultModel`, `putDefaultModel` via generated types).
- **FRs:** FR-016, FR-017, FR-019, FR-021 (Settings surface uses the shared picker), FR-012 (consumes `dependents`/`backs_default`), Accessibility (alertdialog rows).
- **BDD:** Remove an unused provider after one confirmation; Dependents are listed and left without a model (dialog half); Default-backing provider requires an inline new default (dialog half); No Undo exists after removal; Other provider in error state still offered as new default (dialog half); Removing the only provider is refused (dialog half); Default model card shows provider, model, window, source; Change default model takes effect on the next turn (client half: selector lists connected only); Set as default from the provider row; Row expand shows limits and window source (the `window_unknown` link — shared with T068-27).
- **Tests first:** TDD rows 28 `RemoveProviderDialog.test.tsx`, 27 `ProvidersSection.test.tsx` (card, Set-as-default, No-Undo via mocked fetch: no toast action, no restore `PUT` within 10 s), 15 (dialog half of `TestDeleteProvider_OnlyProviderRefused409`: only-provider copy + disabled Remove).
- **Gate:** `spa`.
- **Depends-on:** T068-20, T068-22, T068-23, T068-09, T068-11 (contracts and behaviours the client relies on; mock until integrated).
- **Size:** L.
- **DoD:** SC-009 (0 occurrences of `Undo` in `RemoveProviderDialog.tsx` and its snapshots); `ProviderPickerSheet.tsx` gone; card renders `—` for absent window/source.

### T068-26 — Row states `signed_in` / `expired`, *Manage* action, re-sign-in dialog, sign-in panel — P1
- **Files:** modify `src/components/settings/ProviderRow.tsx` (six-value status switch; *Signed in · <account_label>* / *Signed in*; *Session expired*; icon + text, never colour alone; action reads *Manage* for sign-in providers), `ReAuthDialog.tsx` + `ReAuthDialog.test.tsx` (re-sign-in variant: *"Run `codex login` again, then check"* / `copilot login`; *Check* calls `GET /providers/{id}/sign-in/status`; no refresh request), `ProvidersSection.tsx` (sign-in panel on *Manage*: `POST …/sign-in` instructions + *Check*; CLI-missing hint), `ProvidersSection.test.tsx`, `src/lib/api.ts` (`startSignIn`, `getSignInStatus`).
- **FRs:** FR-034, FR-009 (client), FR-005 (Settings surface), FR-038 (every `switch` on `status` handles six values — impact table HIGH).
- **BDD:** Signed-in row shows account and Manage (client half); Expired session routes to re-sign-in (client half: only the status GET leaves the SPA).
- **Tests first:** TDD row 27 (`ProvidersSection.test.tsx` signed-in / expired describes), `ReAuthDialog.test.tsx` re-sign-in case.
- **Gate:** `spa`.
- **Depends-on:** T068-14 (route shapes), T068-21, T068-25 (row/section scaffold).
- **Size:** M.
- **DoD:** zod accepts `signed_in`/`expired` rows; no ternary on `status` leaves a value unhandled (typecheck exhaustive switch).

### T068-27 — *Check with my account* (entitlement) and per-model limits + window source on row expand — P2
- **Files:** modify `src/components/settings/ProvidersSection.tsx` / `ProviderRow.tsx` (button *Check with my account* → `POST /providers/{id}/entitlement`; grey `entitled:false` with *not available on this key* (≥ 4.5:1 text) ; *limits unknown* flag; inline warning on failure, list unchanged; discard an in-flight result if the row was deleted; expand shows `context_window · max_output_tokens · image · PDF` from `input_modalities` and the window-source cell — `—` until S66 supplies `window_source`; `window_unknown` → *No context length* + link), `ProvidersSection.test.tsx`, `src/lib/api.ts` (`checkEntitlement`).
- **FRs:** FR-031 (SPA half), FR-032, Edge case "Deleting a provider while Check is in flight".
- **BDD:** Check with my account greys unavailable models (client half); Check with my account upstream failure; Row expand shows limits and window source.
- **Tests first:** TDD row 27 (`ProvidersSection.test.tsx` entitlement + row-expand describes, `—` for source).
- **Gate:** `spa`.
- **Depends-on:** `T067-ENTITLEMENT` (shape via `A-CONTRACT`; mock until the route lands), T068-18, T068-25; `T066-RESOLVE-WINDOW` soft (non-`—` value is S66's test 46b tail).
- **Size:** M.
- **DoD:** second click shows `cached` result with no new request in the mocked fetch; 429 → inline warning, nothing greyed.

### T068-28 — Agent list: *needs a model* / *needs a provider* copy — P1
- **Files:** modify the agent list row component under `src/components/agents/` (render *needs a model* when `needs_model`; *needs a provider* wins when `degraded_reason: needs_provider` — resolution #5) and its vitest beside it.
- **FRs:** FR-014 (SPA half).
- **BDD:** Dependents are listed and left without a model (final `And`: the agent list renders the copy on both rows).
- **Tests first:** vitest case on the agent list component asserting both copies and the precedence (no spec-named vitest — the scenario's `And` clause is the oracle; backend coverage is `TestDeleteProvider_DependentsLeftWithoutModel`).
- **Gate:** `spa`.
- **Depends-on:** `A-CONTRACT` (`Agent.needs_model`, `degraded_reason`), T068-08.
- **Size:** S.
- **DoD:** both copies render from generated types; no colour-only state.

### T068-29 — Settings → Models screen (ADR-066 D9): caps, trigger, ingest bound, global default window + source, model overrides — P1 *(ADR-066 scope carried by stream B5)*
- **Files:** create `src/components/settings/ContextSection.tsx`, `ContextSection.test.tsx`; modify `src/routes/_app/settings.tsx` (tab *Models*; route accepts `?provider=&model=` to pre-fill a new override row — the X-08 link target), `src/lib/api.ts` (`getContextSettings`, `putContextSettings` partial update via `ContextSettingsUpdate`).
- **FRs (ADR-066 spec):** FR-036 (`GET/PUT /api/v1/settings/context`: `builtin_success`/tool caps, `absolute_trigger_chars`, `ingest_bound_bytes`, `default_context_window`, `model_overrides[{provider, model, context_window}]`; 400 naming field + limit on cap > 150,000 or < 1, trigger < 1, ingest bound ≥ 8,388,608, `context_window < 1`; every write reloads), FR-037 (location *Settings → Models*, X-37). ADR-068 FR-019/FR-032 link to *Settings → Models → Model overrides*.
- **BDD (ADR-066):** B-44 read defaults / partial write round-trip / reload triggered (US-11.AC1–2); B-14 cap ceiling rows surfaced as field errors.
- **Tests first:** ADR-066 TDD row 46 `ContextSection.test.tsx` (defaults 62,500 / 64,000 / 10,000 / 400,000 / 8,000,000, `default_context_window` unset, `model_overrides: []`; partial PUT sends only changed fields; 400 renders the field/limit; pre-fill from query params).
- **Gate:** `spa`.
- **Depends-on:** `A-CONTRACT` (`ContextSettings.yaml`, `ContextSettingsUpdate.yaml`, `ContextWindowSource.yaml`), `T066-SETTINGS-CONTEXT` (mock the endpoint from the generated zod schema until it lands — the plan's "independent of S68" note on test 46 applies in reverse here).
- **Size:** M.
- **DoD:** the section renders from generated types only; the X-08 link from T068-25/27 lands on a pre-filled override row; no raw cron or scheduling surface introduced.

### T068-30 — Agent form: per-agent `context_window_override` with effective window, source and clamp indicator (ADR-066 D9) — P1 *(ADR-066 scope carried by stream B5)*
- **Files:** modify the agent form component under `src/components/agents/` (field *Context window override* → `AgentUpdateRequest.context_window_override`; read-only *Effective window · source* from `Agent.context_window_effective` / `context_window_source`; clamp note when `context_window_clamped`; all three optional — hidden until present), its vitest.
- **FRs (ADR-066 spec):** FR-037 (agent fields optional on `Agent.yaml`; `AgentUpdateRequest.context_window_override`; write → reload), FR-002 (clamp shown when it bites); ADR-068 §4 "Agent form" sentence.
- **BDD (ADR-066):** B-45 agent fields derived; override write reloads (US-11.AC3–4) — client half.
- **Tests first:** agent-form vitest cases (field present, optional fields absent → hidden, clamp note) — ADR-066's named tests for B-45 are backend (row 42 `TestGateway_AgentWindowFieldsAndOverrideReload`); the SPA assertions are their component-level counterparts.
- **Gate:** `spa`.
- **Depends-on:** `A-CONTRACT`, T068-29 (shared source-label copy), `T066-RESOLVE-WINDOW` soft (fields absent until it lands).
- **Size:** S.
- **DoD:** override round-trips through `PUT /agents/{id}` with generated types; no field shown when the wire omits it.

### T068-31 — Playwright e2e + accessibility rows + binary no-literal test — P1
- **Files:** modify `tests/e2e/providers.spec.ts` (remove; change default takes effect on the next turn; Check with my account; catalog GET ≤ one 200 per ETag; free-string probe id), create `tests/e2e/onboarding.spec.ts` (3 steps; empty model; Finish disabled; completion carries `auth_method`), modify `tests/e2e/accessibility.spec.ts` (axe 0 serious/critical on onboarding step 3 and Settings → Providers with sheet + dialog open; `getBoundingClientRect ≥ 24`; focus-ring contrast ≥ 3:1 via `getComputedStyle`; `activeElement` = Cancel on dialog open; Esc closes with no DELETE observed; `activeElement` stays in the sheet after Esc; ArrowDown×3 + Enter, End, Home, Escape rows; rendered rows ≤ `floor(height/40) + 10`), create `tests/e2e/binary_no_removed_literal_test.go` or a `runci.sh` step (`strings` on the built binary, 0 hits for `antigravity`/`claude-cli`).
- **FRs:** FR-041 (every non-axe row has its named assertion), FR-001 (1a), FR-017, FR-019, FR-026, FR-031, FR-037, SC-005, SC-007, SC-008.
- **BDD:** Config naming a removed provider fails generically (binary half); SPA reads the catalog from the GET (network half); Picker opens…; Expanded list…; Keyboard-only selection; Remove an unused provider…; Esc with a dirty key…; Change default model takes effect…; Check with my account…; Fresh install seeds no default model; Auth-method control keeps three steps; Onboarding model field is empty.
- **Tests first:** TDD rows 1a `TestBinaryHasNoRemovedProviderLiteral`, 30 `providers.spec.ts`, 31 `onboarding.spec.ts`, 32 `accessibility.spec.ts`.
- **Gate:** `spa` for authoring; executed in Wave D through `embed-build` + `e2e` (SPA sync `npm run build → pkg/gateway/spa/` before the binary, or the e2e runs against the stale SPA).
- **Depends-on:** every task above (T068-01 … T068-30) integrated on the branch.
- **Size:** L.
- **DoD:** SC-005/007/008 green on the Fly `e2e` gate; exit codes read without a pipe (`false-green-patterns.md`).

---

## Dependency summary

**Critical path (serial):** `A-CONTRACT` → T068-02/03 (deletions) → T068-06 (own contracts) → T068-07 (default-model pair, HIGH) → T068-08 (dependents) → T068-09 (DELETE) → T068-11 (default-model route) → T068-25 (Providers screen) → T068-31 (e2e). T068-14 → T068-16 → T068-24 is the second serial chain (sign-in backend → onboarding).

**Parallel groups (after `A-CONTRACT`):**
- *Deletions:* T068-01 first (it is the red test), then T068-02 ∥ T068-03 ∥ T068-05; T068-04 after T068-02. All four deletion tasks are independent of `T067-FACTORY` only if they land before it — see the merge hazard on T068-02/03.
- *Backend, after T068-07/08:* T068-09 ∥ T068-11 ∥ T068-12 ∥ T068-13 (T068-09 and T068-11 share the `new_default` validator — one owner, named at integration); T068-10 after T068-09; T068-34 → T068-32 → T068-14 ∥ T068-13; T068-33 after T068-21 + T068-34 once `T067-FACTORY` is in; T068-15 and T068-16 after T068-14; T068-17 last (needs `T067-ENTITLEMENT`).
- *SPA, from day one with mocks:* T068-18 → T068-19 → { T068-20, T068-22, T068-23 } in parallel → T068-21 → { T068-24, T068-25, T068-26, T068-27, T068-28 }; T068-29 ∥ T068-30 are independent of the provider SPA chain (they need only `A-CONTRACT` types) and can start immediately.
- *Cross-stream gates:* T068-12 waits on `T067-NEEDS-PROVIDER`; T068-13 on `T067-RESOLVER`; T068-15 on `B2-RELEASE` (fixture until then); T068-17/27 on `T067-ENTITLEMENT`; T068-29 on `T066-SETTINGS-CONTEXT` (mockable); the window/source values in T068-11/25/27/30 on `T066-RESOLVE-WINDOW` (soft — `—` / omitted until then, resolution #15).

**Riskiest single task: T068-07 (default model as a pair).** The spec's impact table rates it HIGH with the widest fan-out in this list: deleting `AgentDefaults.ModelName` breaks both `gateway.go` `ModelName == ""` guards, `createStartupProvider`, `defaultModelCredentialBlocked`, `pkg/agent/model_resolution.go::buildModelListResolver`, `config.go::resolveFallbackProvider` and every fixture that sets `model_name` (d=1), and at d=2 every agent whose model resolves via the default plus onboarding completion. Its oracle is turn-time resolution in the session transcript, never a config read-back (CRIT-001 / the ADR-037 anti-pattern); a test that still passes `model_name` is a false green. It also sits under all three specs' edits to `AgentDefaults`/`defaults.go` (X-29: S66 `TestConfig_NoContextWindowDefaultKey`, S67 `TestSeeds_CanonicalProviderIDs`, S68 `TestDefaultsSeed_NoRemovedProvider` must all pass after merge). Mitigation per the plan: one agent, one commit, GitNexus `impact` on `GetModelConfig`/`AgentDefaults` attached, Fly `go-test` before integration. Runner-up: T068-09 (DELETE) for its partial-failure/idempotency semantics and the merge-time overlap of T068-02/03's factory edits with `T067-FACTORY`'s atomic collapse.

### T068-32 — Restore the device-code OAuth flow in `pkg/auth` and add the store-backed OAuth token source — **P0** *(added 2026-08-23, ADR-068 §8b)*
- **Files:** restore from `60ee2275^` into `pkg/auth/oauth.go` + `pkg/auth/pkce.go` (+ tests): `OAuthProviderConfig`, `RequestDeviceCode`, `PollDeviceCodeOnce` (+ `slow_down` handling), PKCE helpers, `RefreshAccessToken` (kept); make the config **data**: `OpenAIOAuthConfig()` keeps the Codex endpoints/client id, new `XAIOAuthConfig()` reads `OMNIPUS_XAI_CLIENT_ID` (+ endpoint env) and returns `ErrNotConfigured` when unset. New `pkg/providers/oauth_token_source.go`: `NewStoreOAuthTokenSource(providerID, store, cfg)` — reads `<id>_OAUTH` (JSON `{access_token, refresh_token, account_id, expires_at}`) from the encrypted credential store, refreshes within 5 min of `exp` or on 401 (single-flight, persists result), maps failure to `LLMError needs_provider` / attribution `user` (FR-046). `pkg/credentials`: helper for the `<id>_OAUTH` entry name. `scripts/check-no-removed-providers.sh`: drop the OAuth/device-code symbols from `SYMS` (keep `antigravity`, `claude-cli`, `createClaude*`); update the self-check.
- **FRs:** FR-007, FR-046, FR-049 (config-gated xAI).
- **Tests first:** `TestRequestDeviceCode_Parses` / `TestPollDeviceCodeOnce_SlowDown` (from the restored tests); `TestStoreOAuthTokenSource_RefreshesNearExpiry`; `TestStoreOAuthTokenSource_RefreshFailureIsNeedsProvider`; `TestStoreOAuthTokenSource_SingleFlight`; `TestXAIOAuthConfig_Unset`; grep script self-check green.
- **Gate:** `go-test` (`./pkg/auth/ ./pkg/providers/ -run 'OAuth|DeviceCode|TokenSource'`), `lint`.
- **Depends-on:** T068-03 (done). **Size:** M.
- **DoD:** no code constant holds an xAI client id; refresh token never logged (`RegisterSensitiveValues`).

### T068-33 — SPA sign-in dialog (device code: link + code + polling) shared by Settings → Providers and onboarding step 3; xAI-ready — **P0** *(added 2026-08-23)*
- **Files:** create `src/components/providers/SignInDialog.tsx` (+ `.test.tsx`), `src/lib/api.ts` wrappers over the generated types (`startSignIn`, `pollSignIn`, `fetchSignInStatus`, `importCodexLogin`, `signOutProvider`); modify `AuthMethodControl.tsx` (T068-21: *Sign in* opens the dialog; `cli_login` method renders the existing command + *Check sign-in* path), `ProvidersSection.tsx` (row states `signed_in` with account label + *Sign out*, `expired` → *Sign in again*, `pending`), onboarding step 3 (same dialog; completion requires `signed_in`).
- **FRs:** FR-005, FR-006, FR-045, FR-047 (import link), FR-048 (sign out), FR-049 (works for any `sign_in` row; xAI appears only when the catalog says so — no forward-looking copy).
- **Tests first:** `SignInDialog.test.tsx`: renders link (new tab, `noopener`) + code + copy; polls at `interval_seconds` with fake timers, never faster, backs off on `slow_down`; stops on close; end states `signed_in | expired | denied` with *Try again*; `aria-live` announcement; focus trap + Escape. `ProvidersSection.test.tsx`: the three row states; sign-out round trip. Playwright row added to T068-31 (axe clean on the dialog).
- **Gate:** `spa`. **Depends-on:** T068-21, T068-34 (generated types). **Size:** L.
- **DoD:** `npm run typecheck` + `vitest` green; no `refresh_token` string anywhere under `src/`.

### T068-34 — Contract: device-code sign-in wire shapes — **P0** *(added 2026-08-23; one atomic commit with regenerated artefacts)*
- **Files:** `contracts/components/schemas/SignInStartResponse.yaml` (`method` enum `cli_login | device_code`; `device_code` adds `verification_url` (uri), `user_code` (≤ 16), `device_auth_id` (opaque ≤ 64), `expires_at` (date-time), `interval_seconds` (int 1–30) — as an inline `oneOf` + discriminator in `openapi.yaml` per ADR-034), `SignInStatus.yaml` (`state` gains `pending`), new `SignInPollRequest.yaml` / `SignInPollResponse.yaml` (`state: pending | signed_in | expired | denied`), `openapi.yaml` paths `POST /providers/{id}/sign-in/poll`, `POST /providers/{id}/sign-in/import`, `DELETE /providers/{id}/sign-in`; `make gen-contracts`; commit spec + `pkg/api/generated/` + `src/lib/api/generated/` together.
- **Gate:** `contracts`. **Depends-on:** nothing (lands first). **Size:** S.
- **DoD:** `make verify-contracts` exit 0; `refresh_token` absent from every schema.
