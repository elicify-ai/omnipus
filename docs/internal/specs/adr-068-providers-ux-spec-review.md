# Adversarial Review: ADR-068 — Subscription login policy, provider deletion, the default model, and the provider UX at 190 providers

**Spec reviewed**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-context-budget/docs/internal/specs/adr-068-providers-ux-spec.md`
**Ratified brief**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-context-budget/docs/internal/architecture/ADR-068-subscriptions-provider-deletion-and-provider-ux.md`
**Sibling spec cross-checked**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-context-budget/docs/internal/specs/adr-067-registry-catalog-spec.md`
**Review date**: 2026-08-22
**Branch / commit read**: `feat/context-budget-and-tool-result-routing` @ `fd9f1574`
**Input mode**: plan-spec (full structural checks applied)
**Verdict**: **BLOCK**

## Executive Summary

The spec is thorough on UI behaviour and on the deletion dialog, but four of its load-bearing claims do not survive contact with the code on this branch: the default-model `PUT` writes a `(provider, model)` pair into a field (`agents.defaults.model_name`) that the runtime treats as an *alias* looked up against each provider entry's single configured model, so the round-trip test passes while the next turn does not change; sign-in providers cannot finish onboarding because the probe the spec makes mandatory requires an API key; the "no trace" acceptance scenario demands that no response body contain the word `antigravity` while the very path it relies on (and ADR-067's contract) echoes the operator's own provider id; and `DELETE` spans four stores under three different locks with no ordering or partial-failure rule, which can leave a deleted provider's secret orphaned in `credentials.json` with no remaining way to remove it — the exact defect class this ADR exists to fix. Nineteen further findings are MAJOR, most of them contradictions with the ADR-067 spec's wire shapes (`Provider.status`, the entitlement route, catalog caching), gaps in Constraint #8 coverage, and acceptance scenarios that either cannot fail today or cannot be asserted by the named test.

| Severity | Count |
|----------|-------|
| CRITICAL | 4 |
| MAJOR | 19 |
| MINOR | 12 |
| OBSERVATION | 5 |
| **Total** | **40** |

Every code citation below was read on this branch on 2026-08-22; line numbers are given only where the line itself is the evidence.

---

## Findings

### CRITICAL Findings

#### [CRIT-001] The default-model `PUT` persists into a field the runtime does not read the way the spec assumes

- **Lens**: Incorrectness
- **Affected section**: US-4; FR-018 ("persist `agents.defaults.{provider,model_name}` … take effect on the next turn"); Data Constraints `DefaultModel {provider, model}`; scenario "Change default model takes effect on the next turn"; test 17 `TestDefaultModel_PutGet`.
- **Description**: `agents.defaults.model_name` is **not** a model id. `pkg/gateway/gateway.go` (the `ModelName == ""` guard, ~L1654) documents it as an *alias* — "Don't overwrite an alias (e.g. `openrouter-auto`) with the raw model slug … the alias is what `GetModelConfig` looks up by" — and `pkg/config/config.go::GetModelConfig` resolves it through `findMatches` against each `cfg.Providers[]` entry's `ModelName`/`Model`. Each provider entry carries exactly **one** `Model` slug. The spec's `PUT {provider:"anthropic", model:"claude-sonnet-4.6"}` therefore has nowhere to land when `claude-sonnet-4.6` is not the Anthropic entry's configured `Model`: writing the raw id into `model_name` produces an alias that matches nothing, and `instance.go`'s "provider %q not found" / `GetModelConfig`'s "model %q not found in model_list or providers" fires on the next turn. The spec never says that the PUT must also rewrite the provider entry's `Model` (and `ModelName`), nor what happens to that entry's existing alias that other agents may reference by name.
- **Impact**: `TestDefaultModel_PutGet` (a PUT → GET round-trip on the config field) goes green; the operator's next message fails with a config error. This is precisely the ADR-037 anti-pattern the spec itself cites as the thing to avoid (`rest_default_agent_singleton_test.go` precedent). The `DELETE … new_default` path (US-3 AS6) inherits the same defect: "the default model changes to the new pair … then the provider is removed" changes nothing resolvable.
- **Recommendation**: Define the persistence model explicitly. Either (a) the default is `(provider entry id, model id)` and the PUT rewrites that provider entry's `Model` (and its `ModelName` alias) under `configMu`, with a stated rule for agents that referenced the old alias; or (b) introduce a per-provider model list (ADR-067's catalog already supplies it) and make `agents.defaults.model_name` carry `"<provider>/<model>"` with `GetModelConfig`/`findMatches` changed to resolve that form — which is a runtime change the spec must list under Symbols Involved. In both cases make `TestDefaultModel_PutGet` assert **turn-time resolution** (`resolveModelCandidatesForAgent` / the session transcript's `provider`+`model`, which `pkg/session/daypartition.go` does record), not a config read-back.

---

#### [CRIT-002] Sign-in providers cannot complete onboarding as specified

- **Lens**: Inconsistency / Infeasibility
- **Affected section**: FR-029 ("*Finish* MUST be disabled until a model is chosen and the probe has passed for that exact model"); Dataset "Provider id" row 11 (`openai-chatgpt` → "400 for probe (sign-in provider has no key)"); scenario "Onboarding complete with sign-in" (asserts 200); US-2 AS3; ADR §9 item 4 ("ends with a probe-validated (provider, model) pair").
- **Description**: `contracts/components/schemas/ProbeProviderRequest.yaml` has `required: [id, api_key]`. The spec changes `id` to a free string and adds `model`, but leaves `api_key` required and, in its own dataset, says a probe for a sign-in provider returns 400. FR-029 then forbids enabling *Finish* until that probe passes. For `codex-cli`, `openai-chatgpt` and `github-copilot` the flow is therefore unfinishable, and the "Onboarding complete with sign-in" scenario contradicts FR-029.
- **Impact**: The headline onboarding promise of D13 ("Sign in *or* API key per provider, three steps") is unreachable for every sign-in provider.
- **Recommendation**: Specify the probe for sign-in: make `api_key` optional (conditional on `auth_method`), and define what "probe" means per path — for `codex-cli`, a subprocess dry-run with the chosen model (the fake-`codex`-on-PATH fixture in `codex_cli_provider_integration_test.go` already supports this); for `openai-chatgpt`, one completion with the saved token. Add the request/response shape to `contracts/` (Constraint #8), add outline rows for sign-in probes to "Probe provider id validation", and update dataset row 11.

---

#### [CRIT-003] The "no trace" acceptance scenario is infeasible and contradicts both the ADR and ADR-067

- **Lens**: Infeasibility / Inconsistency
- **Affected section**: US-1 AS2; scenario "Config naming a removed provider fails generically" ("no log line and no response body contains the string `antigravity`"); scenario "`claude-cli` is an unknown provider" ("neither the error message nor any log line contains `claude-cli`"); Probe outline row "antigravity … body does not contain 'antigravity' beyond echoing nothing".
- **Description**: Three facts collide. (1) ADR-068 §2.4 says a config naming `antigravity` "takes the generic unknown-provider path that already exists … That path is **not touched**". (2) That path echoes the operator's id: `pkg/gateway/rest_onboarding.go` L726 `"unknown provider %q and no endpoint override supplied"`, and `pkg/providers/factory_provider.go`'s default case `"unknown protocol %q in model %q"`. (3) The ADR-067 spec's machine-verifiable section fixes the wire text as `{"error":"unknown provider \"<id>\""}` — the id is *part of the contract*. On top of that, the `GET /providers` row for the operator's own `provider: "antigravity"` entry necessarily carries `"id":"antigravity"` in the body. A test written to the spec's wording cannot pass without rewriting the generic path (which the ADR forbids) and breaking ADR-067's contract.
- **Impact**: Either the implementer silently weakens the test (the false-green pattern `docs/internal/false-green-patterns.md` warns about) or ships a generic path that hides operator input from error messages, which is a worse UX for every genuine typo.
- **Recommendation**: Redefine "no trace" as a *source* property: no literal `antigravity` / `claude-cli` in code, contracts, seeds, docs, and no branch keyed on them. Rewrite AS2 as: "the row/error text is the generic unknown-provider text parameterised with the operator's id, and the binary contains no string literal naming the provider" (assert via `go tool nm`/`strings` on the binary or the grep exit proof, not on response bodies). Delete the contradictory clauses in the two scenarios and the probe outline row, and reconcile with ADR-067's `unknown provider "<id>"` wording.

---

#### [CRIT-004] `DELETE /providers/{id}` has no ordering, atomicity or partial-failure rule across four stores — and can orphan the secret it exists to delete

- **Lens**: Incompleteness / Insecurity
- **Affected section**: FR-010 ("remove every config entry … and the credential … then wait for reload, then respond"); FR-013; Edge Cases "Concurrent removal and PUT … under `configMu`"; Integration Boundaries → Credential store.
- **Description**: A removal must (a) delete `<id>_API_KEY` from `pkg/credentials` (its own mutex + file), (b) rewrite `config.json` under `configMu`, (c) update every dependent agent entity through `agentstore.New(a.homePath).Update` (the `pkg/entity` striped lock — **not** `configMu`, see `pkg/gateway/rest.go` ~L7577), and (d) wait on `triggerReloadAndWaitOutcome`. The spec states only "under `configMu`", which does not cover (a) or (c), and gives no order and no failure semantics. Concretely unspecified: key deleted, then config write fails → provider row persists with a dangling `api_key_ref`; config written, then entity update fails on agent 37 of 50 → response `dependents` is wrong; reload fails → 500 after the deletion already happened (the spec defines that 500 only for the PUT); a retry of the `DELETE` then returns **404** (FR-011) because the config row is gone — while the credential may still be in `credentials.json` with no remaining UI or REST path to remove it. Note also that `Store.Delete` returns `NotFoundError` on a missing name (`pkg/credentials/store.go` L266-280), contrary to the Edge Case that says it "is not an error" — so the naïve implementation fails the happy path when the key was already missing.
- **Impact**: The secret-retention risk the ADR's MAJ-011 set out to eliminate (no Undo, "retained nowhere") re-enters through the failure path: an orphaned key is retained *forever*. Dataset row 9 ("concurrent DELETE ×2 … first 200, second 404") is asserted without the lock that would make it true.
- **Recommendation**: Specify the sequence and make each step idempotent: (1) take `configMu`; (2) compute dependents from the entity store *inside* the lock; (3) write config with the row removed and the default updated; (4) clear dependents (entity updates, treating `ErrNotFound` as done); (5) delete the credential, treating `NotFoundError` as success; (6) reload-wait; on reload failure respond 500 with a `deleted:true` body and the reason. State that the credential delete happens *after* the config write so a failure leaves a configured row the operator can retry, never an orphan. Add `TestDeleteProvider_PartialFailureNoOrphanSecret` (inject a config-write failure; assert the key still exists and a retry succeeds) and a test for a failing entity update.

---

### MAJOR Findings

#### [MAJ-001] `Provider.status` enum contradicts the ADR-067 spec

- **Lens**: Inconsistency
- **Affected section**: FR-038 ("MUST be exactly `connected | disconnected | error | signed_in | expired`"); SC-012 ("generated Go consts are exactly five"); scenario "Config naming a removed provider … shows the generic unknown-provider state".
- **Description**: The ADR-067 spec (A-16, FR-016, FR-031) adds `Provider.status: unknown-provider`. This spec's "exactly five" excludes it, yet its own scenario needs a state to show for an unknown provider and names none of the five.
- **Impact**: Whichever spec lands second fails `contract_test.go` or SC-012.
- **Recommendation**: Make the enum six values (`unknown-provider` included), update SC-012, and have the scenario assert `status: unknown-provider`.

#### [MAJ-002] Reserved sub-paths collide with `{id}` on the `/providers/` prefix

- **Lens**: Incorrectness
- **Affected section**: Symbols Involved (`HandleProviders` gains `DELETE /{id}`, `GET/PUT default-model`, `POST /{id}/sign-in`); FR-018; ADR-067's `GET /providers/catalog`.
- **Description**: `HandleProviders` strips `/api/v1/providers/` and treats the remainder as the id (`PUT … sub != "" && !HasSuffix("/test")`, `pkg/gateway/rest.go` L6017). `default-model` and `catalog` both satisfy the new id pattern `^[a-z0-9][a-z0-9._-]*$`. Nothing in the spec reserves them, orders the dispatch, or says what `PUT /providers/default-model` with a `ProviderUpdateRequest` body, or `DELETE /providers/catalog`, does.
- **Recommendation**: List reserved literals (`catalog`, `default-model`, `model-capabilities` if kept) as invalid provider ids everywhere an id is accepted (PUT, DELETE, probe, onboarding-complete), and specify dispatch precedence with a test row per reserved literal.

#### [MAJ-003] The entitlement route is named two ways and one removed route is listed as live

- **Lens**: Inconsistency
- **Affected section**: Symbols Involved (`refreshProviderModels` "becomes the Check with my account intersection"; `HandleProviders` "dispatches … GET model-capabilities"); FR-031.
- **Description**: The ADR-067 spec defines `POST /api/v1/providers/{id}/entitlement` as *replacing* `refresh-models`, and removes `GET /providers/model-capabilities` (A-12). This spec keeps the `refresh-models` name, never states the wire route for "Check with my account", and lists `model-capabilities` as still dispatched.
- **Recommendation**: Name the route once (`/entitlement`, per ADR-067), define its response shape in `contracts/` (see MAJ-014), and drop `model-capabilities` from the symbol table.

#### [MAJ-004] Catalog caching rule contradicts ADR-067's

- **Lens**: Inconsistency
- **Affected section**: Performance Bounds ("at most **one** network fetch per SPA session unless the validator changes … `staleTime` = session"); FR-037; scenario "SPA reads the catalog from the GET" ("exactly one request … for the session").
- **Description**: ADR-067 spec A-1: "SPA re-validates on Settings open and every 15 min" — a conditional GET with `If-None-Match` is still a request. The Playwright assertion here counts requests and will fail against the ADR-067 behaviour.
- **Recommendation**: Adopt one rule (ADR-067 owns the endpoint) and assert "at most one **200** per validator value" rather than one request.

#### [MAJ-005] The factory has two dispatch layers; the spec and the ADR describe only one, and describe it wrongly

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: Symbols Involved (`factory_provider.go` cases); FR-003; test 4 `TestFactory_NoVendorCaseForRemovedIDs`; ADR §2.2 ("`codex-cli` — despite the name, not a subprocess").
- **Description**: On this branch the *protocol* switch at `pkg/providers/factory_provider.go` L394 maps `"codex-cli", "codexcli"` → `NewCodexCliProvider` (the **subprocess**). The HTTP path is built by the separate, id-keyed `createCodexAuthProvider` (L36-50), which reads an OAuth credential from the **credential store** (`getCredential("openai")`) and wraps `createCodexTokenSource()` — it does not read `auth.json` at all. So (1) the ADR's grounding claim for the rename is not what the code does; (2) there are two distinct token sources (`createCodexTokenSource` = store-held OAuth, `CreateCodexCliTokenSource` = `auth.json`) and the spec deletes one without saying which provider keeps the other; (3) the parallel `createClaudeAuthProvider`/`createClaudeTokenSource` path (L20-33, "no credentials for anthropic") is a store-held Anthropic token path — a *subscription-shaped* path the D13 descoping never mentions; (4) `knownProtocols` (L404+), `config.go` L2719's protocol comment, and the id-keyed `createXxxAuthProvider` ladder are not in the deletion inventory.
- **Recommendation**: Re-ground §2.2 on the two layers; specify the end state per layer for `codex-cli`, `openai-chatgpt`, `claude-cli`, and the Anthropic store-OAuth path; extend test 4 to cover both switches and `IsKnownProtocol`.

#### [MAJ-006] `openai-chatgpt` expiry claim and account label have no source in the data the code reads

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: FR-009; Edge Cases (`openai-chatgpt` "token carrying its own expiry claim"); scenario "Signed-in row shows account" (`account_label: "dev@example.com"`); Dataset "Sign-in status" rows 4a-4c, 7.
- **Description**: `pkg/providers/codex_cli_credentials.go` parses only `tokens.{access_token,refresh_token,account_id}`; expiry is `mtime + 1h` (L26, L49-51). There is no `exp` field and no e-mail. An "expiry claim" can only come from decoding the access-token JWT unverified — not specified, and a design choice with security implications (trusting unverified claims for a status display is fine; for anything else it is not). `account_id` is opaque, so `"dev@example.com"` is invented. Separately, FR-002a deletes `RefreshAccessToken`, which `codex_provider.go` L269-270 uses today to refresh store-held OAuth tokens; the spec does not state the behaviour change (sessions now end at token expiry and require `codex login`). FR-007 says "only read" but the struct still reads `refresh_token`; say it must not be read or stored.
- **Recommendation**: State the claim source (JWT `exp`/`email` decoded without verification, display-only), the fallback when absent, and the behaviour change; fix the scenario's label to what `auth.json` can yield.

#### [MAJ-007] Authentication posture of `DELETE` and the default-model `PUT` is unspecified and weaker than the existing key-mutation gate

- **Lens**: Insecurity (Spoofing / Elevation of Privilege)
- **Affected section**: FR-011 (503 under bypass; "No password re-type is required (resolution #18)"); Machine-Verifiable Constraints; resolution #18.
- **Description**: `/api/v1/providers` is registered with `withOptionalAuth` (`rest.go` L5218-5219). The PUT branch inlines two gates: 401 once onboarding is complete and no user is in context, and `requireReAuth` (Spec-6 FR-12.2: an API-key mutation needs the single-use re-auth consent token). The spec (a) drops re-auth for a *credential deletion*, which is the same class of mutation, by operator decision — acceptable only if recorded as a conscious exception to Spec-6 FR-12.2; (b) says nothing about whether `DELETE` and `PUT default-model` are reachable unauthenticated pre-onboarding as PUT is; (c) cites `RequireNotBypass`, which is a registration-time wrapper (`adminWrap`) that this handler does not have — it must be an inline check, and the spec should say so.
- **Recommendation**: Add an explicit auth row per new route (pre-onboarding allowed? user required? admin required? re-auth?), cite the Spec-6 exception, and put `TestDeleteProvider_404_503_Bypass503` on the inline bypass check plus a 401 row.

#### [MAJ-008] A third adjacent `LLMError` code, with undefined precedence against ADR-067's

- **Lens**: Overcomplexity / Inconsistency
- **Affected section**: FR-015; WS constraint (`code: "model_unassigned"`); Edge Cases (`needs_model` + `needs_provider`).
- **Description**: `contracts/asyncapi.yaml` already has `agent_not_configured` and `model_unavailable`; the ADR-067 spec adds the error kind `needs_provider`; this spec adds `model_unassigned`. Three codes for "this agent cannot resolve a model" with no stated reason not to reuse `agent_not_configured`. The copy precedence (`needs_provider` wins) is defined for the agent **list** only; at turn time an agent can have both flags and the spec does not say which frame is emitted.
- **Recommendation**: Either reuse `agent_not_configured` with a `detail`, or justify the new code and define turn-time precedence with a test row.

#### [MAJ-009] Acceptance scenarios that cannot fail before implementation, or whose named test cannot assert them

- **Lens**: Incorrectness (false-green)
- **Affected section**: scenario "Fresh install seeds no default model"; test 1 `TestNoRemovedProviderInTree`; scenario "No Undo exists after removal" ("the SPA holds no variable containing the removed key"); FR-007 → `TestSignInStatus_CodexCLI`; FR-031 "caches per key" (no test).
- **Description**: (1) `pkg/config/defaults.go` L29-35 already leaves `Agents.Defaults.ModelName` empty; the "antigravity default" the ADR cites is a provider **template** (`defaults.go` L197-201) — the scenario is green today and guards nothing. (2) `TestNoRemovedProviderInTree` is a Go test whose allow-list must itself spell `antigravity`, so it matches its own source file; it also walks `docs`/`src` from a package working directory. (3) "holds no variable containing the key" is vacuous (keys are never returned) and untestable. (4) FR-007 ("MUST NOT write the credential file") is traced to a status test that does not assert non-writing. (5) Per-key caching has no test.
- **Recommendation**: Re-point scenario (1) at the provider template (`defaults.go` contains no `antigravity` template and no `AuthMethod: "oauth"` entry); make the exit proof a script under `scripts/` (like `check-no-jpeg-screencast.sh`, wired into CI) with the allow-list as a data file; delete the vacuous Undo clause; add an mtime/content assertion on `auth.json` after a sign-in status call; add a cache test.

#### [MAJ-010] Dependents computation ignores resolution side effects and other model references

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: FR-012/FR-013; Dataset "Dependents computation" row 7; Edge Cases.
- **Description**: `pkg/config/config.go::resolveFallbackProvider` rule 3 resolves any unmatched slug on an agent with empty `provider` to the configured `openrouter`/`vivgrid` entry. Deleting OpenRouter therefore changes resolution for agents that never named it — they are not "dependents" by the spec's definition but break anyway. Also uncovered: `RecapFallbackModels` (`config.go` L1592), `agents.defaults.provider`, voice model, and heartbeats/schedules that pin a model.
- **Recommendation**: Define "dependent" as "any reference that would stop resolving after removal", enumerate the reference sites, and add rows for passthrough inference and recap fallbacks.

#### [MAJ-011] The undeletable-default rule creates a dead end the spec does not acknowledge

- **Lens**: Incompleteness
- **Affected section**: US-3 AS5; FR-011/FR-016; Boundary conditions ("an install always keeps ≥1 connected provider").
- **Description**: The inline selector is restricted to `connected | signed_in`. If the default-backing provider is itself `error`/`expired` and every other provider is `error`, the operator can neither remove the default nor choose a new one. The spec also never says whether a `backs_default` provider in `expired` state is treated differently.
- **Recommendation**: State the dead end and its escape (repair or connect another provider first) in the dialog copy; add a row "default-backing provider `error`, other provider `error` → dialog explains, Remove disabled".

#### [MAJ-012] Several WCAG constraints are not machine-checkable by the tests named

- **Lens**: Infeasibility
- **Affected section**: Accessibility block (11 constraints); FR-041; test 32 (`accessibility.spec.ts`: "axe 0 serious/critical; target size; focus ring; keyboard").
- **Description**: axe-core has no rule for focus-indicator contrast (2.4.7/2.4.11) or for "focus lands on Cancel", "Esc = Cancel", "prompt does not move focus" (3.2.1); its `target-size` rule is best-practice-tagged, not `serious`/`critical`. Those constraints need explicit Playwright assertions that the test table does not list. Testable as written: keyboard operability, `role`/`aria-*` attributes, text contrast, the axe run itself.
- **Recommendation**: Per constraint, name the assertion (e.g. `document.activeElement` after open; computed `outline`/`box-shadow` colour contrast via `getComputedStyle`; `getBoundingClientRect` ≥ 24 for a fixed selector list) and add them as rows to test 32.

#### [MAJ-013] cmdk + virtualisation + "End reaches Custom endpoint" is not feasible without a stated approach

- **Lens**: Infeasibility
- **Affected section**: FR-023, FR-026; scenario "Custom endpoint is last" (End key); scenario "Expanded list is letter-grouped and virtualised".
- **Description**: cmdk filters and navigates over the `Command.Item`s present in the DOM. With `@tanstack/react-virtual`, rows outside the window are not mounted, so End/Home/typeahead cannot reach them, and cmdk's built-in filter would fight the spec's own search. The spec mandates both without reconciling them.
- **Recommendation**: Specify `shouldFilter={false}`, spec-owned filtering, and a virtualiser-driven focus model (scroll-to-index then focus) — or drop cmdk for the virtualised section and keep it for the tiles.

#### [MAJ-014] Constraint #8 gaps: shapes consumed but never defined

- **Lens**: Incompleteness (contract-first)
- **Affected section**: FR-035; Integration Boundaries; scenarios for US-7 and US-2.
- **Description**: Not defined in `contracts/` by this spec: the "Check with my account" response (greyed / `limits unknown` annotations); the sign-in probe request/response (CRIT-002); `ProviderDependent` as a named schema; the `ProbeProviderResponse` change when `model` is supplied; `OnboardingCompleteRequest.provider.api_key` as conditionally required (OpenAPI cannot express "required iff"; ADR-034 requires an inline `oneOf`+discriminator in `openapi.yaml` or a stated runtime rule); the `Agent.yaml` edits that both this spec (`needs_model`) and ADR-067 (`degraded_reason`) make to the same file.
- **Recommendation**: Add each to FR-035 with its schema file name; state the conditional-required mechanism.

#### [MAJ-015] Greenfield self-contradictions

- **Lens**: Inconsistency
- **Affected section**: Qualitative Prohibitions ("must not keep any … retired-name list"); test 1 allow-list; FR-022 "Recent (most recently saved first)".
- **Description**: (1) `TestNoRemovedProviderInTree`'s allow-list of historical files is a retired-name list living in `pkg/` — the thing the spec forbids. (2) "Most recently saved" requires a saved-at timestamp on provider entries; none exists and none is specified (a config schema addition).
- **Recommendation**: Move the exit proof out of Go (MAJ-009) and specify the timestamp field or change Recent to "configured, in config order".

#### [MAJ-016] No audit record for deleting a credential or changing the default model

- **Lens**: Insecurity (Repudiation) / Inoperability
- **Affected section**: Deployment / Runtime → Logs ("one INFO line"); FR-010; FR-018.
- **Description**: Credential deletion and default-model change are security-relevant operator actions; the spec specifies a log line, not an audit event (`pkg/audit` is the existing, HMAC-chained trail per CLAUDE.md v0.2 scope).
- **Recommendation**: Require an audit entry per action (actor, provider id, dependents count, default change) and a test that it is emitted.

#### [MAJ-017] `GET /providers` configured-only: the onboarding "fallback default entry" consumer is left as "if any"

- **Lens**: Incompleteness
- **Affected section**: Impact Assessment row "GET /providers template rows removed" (d=2: "onboarding 'fallback default entry' consumers, if any"); FR-011a.
- **Description**: `rest.go` ~L5898 builds a "final fallback: the configured default model alias" row; `createStartupProvider`/`defaultModelCredentialBlocked` read `cfg.Providers` templates. Whether the SPA onboarding or the limited-mode banner depends on the template row is left unverified in a spec that otherwise claims evidence level 1.
- **Recommendation**: Verify and state the consumer list; add the removal of the fallback row to the scenario.

#### [MAJ-018] "Removing the only provider" boundary conflicts with the concurrency row

- **Lens**: Inconsistency
- **Affected section**: Dataset "DELETE bodies" row 9 ("concurrent DELETE ×2 … first 200, second 404"); FR-011 (409 when `backs_default`).
- **Description**: Two concurrent `DELETE`s on the same id with a `new_default` body: the first succeeds and re-points the default; the second must 404 — but only if the config read happens under `configMu`; the spec's `GET`-supplied `backs_default` is stale by then, and the dialog's `dependents` list is advisory. The spec never states that the server recomputes everything and that the response, not the dialog, is authoritative.
- **Recommendation**: Add "server recomputes `backs_default` and dependents under `configMu`; the `GET` values are advisory" to FR-011/FR-012.

#### [MAJ-019] `Store.Delete` on a missing name *is* an error today

- **Lens**: Incorrectness
- **Affected section**: Edge Cases ("`Delete` on a missing name is not an error"); Integration Boundaries → Credential store ("missing name → treated as success").
- **Description**: `pkg/credentials/store.go` L266-280 returns `&NotFoundError{}` for a missing name. The handler must special-case it; the spec presents it as existing behaviour.
- **Recommendation**: Rewrite as "the handler MUST treat `credentials.NotFoundError` as success" and add it to `TestDeleteProvider_Unused200` (pre-delete the key, then DELETE).

---

### MINOR Findings

#### [MIN-001] Popular order differs between the two specs
- **Lens**: Inconsistency. **Affected**: Data Constraints ("expected to be exactly `openai, anthropic, openrouter, google, …`"); ADR-067 spec US-8.AC1 lists `openai, openrouter, anthropic, google, …`. The scenario asserts "catalog order". **Fix**: drop the expected order here; assert "catalog order" only.

#### [MIN-002] `status ≠ deprecated/retired` names a value ADR-067 does not have
- **Lens**: Inconsistency. **Affected**: Recommended chip rule; FR-030. ADR-067 status enum is `active | retired`. **Fix**: `status = active`.

#### [MIN-003] Region inference maps `zh-TW`/`zh-HK` to `china`
- **Lens**: Incorrectness. **Affected**: FR-027 (`zh-*` → china). Taiwanese/Hong Kong operators get the mainland endpoint and, for some vendors, a different legal entity. **Fix**: `zh-CN`/`zh-SG` → china; other `zh-*` → intl; add rows.

#### [MIN-004] First-paint ≤ 100 ms p95 on a shared CI runner is not a stable gate
- **Lens**: Infeasibility. **Affected**: Performance Bounds; SC-005. **Fix**: keep the DOM-count bound as the gate; make the timing a recorded metric, not a pass/fail.

#### [MIN-005] Speculative wire fields with no producer
- **Lens**: Overcomplexity. **Affected**: `SignInStartResponse.method: "device_code"`, `user_code`, `verification_uri`, `expires_at`; `SignInStatus.state: pending`. FR-002a deletes the only device-code producer; `pending` has no producer for CLI login. **Fix**: remove them (greenfield forbids future-proofing).

#### [MIN-006] Probe with an arbitrary id plus `endpoint` accepts any URL — SSRF gate not mentioned
- **Lens**: Insecurity. **Affected**: Probe outline row "not-a-provider + https://gw.example/v1 → 200 (custom path)". PUT runs an SSRF check (precedent table); the spec does not say the probe does. **Fix**: state the same check applies and add an internal-CIDR row.

#### [MIN-007] Per-key cache key derivation unspecified
- **Lens**: Insecurity. **Affected**: FR-031 "caches per key". **Fix**: key the cache by provider id + a hash of the credential ref, never the secret.

#### [MIN-008] `dependents` names leak to any `withOptionalAuth` caller pre-onboarding
- **Lens**: Insecurity (Information Disclosure). **Affected**: FR-012. Minor because pre-onboarding exposure is bounded. **Fix**: covered by MAJ-007's auth table.

#### [MIN-009] Traceability hygiene
- **Lens**: Inconsistency. SC-014 sits between SC-001 and SC-002; FR-024 has only a unit test; FR-020's test (`TestOnboardingComplete_AuthMethod`) does not exercise the reload-path guard at `gateway.go` L5048; FR-041 maps to "all picker/dialog scenarios" without naming them. **Fix**: renumber; add an integration row for FR-024 and a reload-guard test for FR-020.

#### [MIN-010] `xAI` copy promises a future feature inside the product
- **Lens**: Ambiguity. **Affected**: US-2 AS5 (*"Sign in with xAI arrives once xAI lists Omnipus"*). Ships a commitment that may never be met. **Fix**: "API key only" with no forward promise.

#### [MIN-011] `ProbeProviderRequest.id` pattern excludes ids ADR-067 may serve
- **Lens**: Incorrectness. Pattern `^[a-z0-9][a-z0-9._-]*$` — models.dev ids are lower-case today, but the rule should be "validated against the served catalog", not a hand pattern that can drift. **Fix**: keep length bounds; drop the pattern or derive it from the catalog loader's id rule (ADR-067 FR-002).

#### [MIN-012] Virtualisation threshold is asserted at "visible + 10" in one place and "visible rows + 2 × overscan (=10)" in another
- **Lens**: Ambiguity. Same number, two definitions; "visible" is undefined for a jsdom test. **Fix**: fix the container height in the fixture and state the resulting integer.

---

### Observations

#### [OBS-001] Recent section adds a fourth band for a list the ADR says stays short
- **Lens**: Overcomplexity. Popular + Recent + search + letter-grouped list; the ADR notes "the configured list stays short at 190 — the picker is the problem, not the rows". Consider dropping Recent from v1.

#### [OBS-002] Evaluation holdout "Offline picker" contradicts the "Catalog unavailable" scenario's premise
- Both fine if the embedded snapshot is the norm; say so in the holdout.

#### [OBS-003] `Agent.needs_model` derivation duplicates `degraded_reason`
- One derived field with an enum (`none | needs_provider | needs_model`) would avoid two booleans with a precedence rule across two specs.

#### [OBS-004] The Recommended chip's ≥128k window rule excludes every small fast model a first-time user might sensibly pick
- A hint, not a block, but "Recommended for chat" reads as authoritative in onboarding.

#### [OBS-005] `TestBuildGates_FilesGone` as "E2E"
- It is the CI pipeline itself; listing it as a test name invites a stub that always passes.

---

## Structural Integrity Results

| Check | Result | Notes |
|---|---|---|
| Every user story has ≥1 acceptance scenario | PASS | |
| Every acceptance scenario has ≥1 BDD scenario | **FAIL** | US-4 AS6 ("Set as default from row") and US-7 AS4 are present; US-3 AS8 is traced to "GET /providers returns configured providers only", which is a different behaviour; US-2 AS6 is covered by two scenarios with contradictory feasibility (CRIT-003). |
| Every BDD scenario has a `Traces to:` | PASS | |
| Every BDD scenario has a test in the TDD plan | **FAIL** | "No Undo exists after removal" → `ProvidersSection.test.tsx` cannot assert "holds no variable" (MAJ-009); "Config naming a removed provider fails generically" has no test row at all; "Row expand shows limits and window source" has no test that can run while values are `—` (FR-019/FR-032 resolution #15). |
| Every FR in the traceability matrix | PASS | FR-001…FR-041 present. |
| Every BDD scenario in the matrix | **FAIL** | "Config naming a removed provider fails generically" and "Build and contract gates pass" appear only in the prose completeness note, not as rows. |
| Test datasets cover boundary/edge/error | PARTIAL | Good on ids, bodies, chips; missing: partial-failure rows for DELETE (CRIT-004), passthrough-inference dependents (MAJ-010), `error`/`expired` default-backing provider (MAJ-011), sign-in probe (CRIT-002). |
| Regression impact addressed | PASS | Regression dataset and table present; note `TestCatalog_EmbedMatchesGeneratedTS` replacement is ADR-067's to own. |
| Success criteria measurable | PARTIAL | SC-005 timing (MIN-004); SC-001 depends on an allow-list that is itself a hit (MAJ-009). |

## Test Coverage Assessment

- **Missing negative tests**: DELETE partial failure and retry (CRIT-004); sign-in probe failure paths (CRIT-002); reserved-literal ids (MAJ-002); unauthenticated/pre-onboarding DELETE and default PUT (MAJ-007); concurrent DELETE vs agent PUT that re-points to the provider mid-delete (MAJ-018).
- **Missing boundary tests**: default-backing provider in `error`/`expired` (MAJ-011); `zh-TW` locale (MIN-003); `new_default` naming a `custom`/`ollama` provider with a non-catalog model (FR-018 exception applied to DELETE?).
- **Missing concurrency tests**: only dataset row 9, asserted without the lock that makes it true.
- **Missing idempotency tests**: second `DELETE` after a failed first (CRIT-004).
- **Wrong level**: `TestNoRemovedProviderInTree` as a Go unit test (MAJ-009); `TestBuildGates_FilesGone` (OBS-005).
- **Regression blind spot**: turn-time resolution after default change is never asserted — the round-trip test is the wrong oracle (CRIT-001).

## STRIDE Threat Summary

| Component / flow | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| `DELETE /providers/{id}` | **open** (MAJ-007) | partial-failure orphan (CRIT-004) | **no audit** (MAJ-016) | dependents names (MIN-008) | 50-agent serial entity writes under lock — bounded, acceptable | re-auth dropped (MAJ-007) | |
| `PUT /providers/default-model` | **open** (MAJ-007) | alias mismatch silently changes nothing (CRIT-001) | **no audit** (MAJ-016) | — | — | — | |
| Probe with `endpoint` | — | — | — | — | — | SSRF gate unstated (MIN-006) | |
| Sign-in status (`auth.json` read) | unverified JWT claims for display (MAJ-006) | reads `refresh_token` it should not (MAJ-006) | — | `account_label` on the wire | — | `CODEX_HOME` path is operator-controlled — acceptable | |
| "Check with my account" cache | — | — | — | cache keyed by secret? (MIN-007) | one call per click — fine | — | |
| Picker / catalog GET | covered by ADR-067 | — | — | — | 190-entry DOM bounded | — | |

## Unasked Questions

1. Where does a `(provider, model)` default actually live, given `model_name` is an alias and each provider entry holds one `Model`? (CRIT-001)
2. What does "probe" mean for a provider that has no API key? (CRIT-002)
3. If the key delete succeeds and the config write fails, who deletes the key next time? (CRIT-004)
4. Which `Provider.status` does the operator's stale `antigravity` row show, with only five values and ADR-067 adding a sixth? (MAJ-001)
5. What does `PUT /providers/default-model` with a `ProviderUpdateRequest` body do? (MAJ-002)
6. Does the Anthropic store-held OAuth path (`createClaudeAuthProvider`) survive D13? (MAJ-005)
7. What is the `exp`/e-mail source for `openai-chatgpt`, and is it verified? (MAJ-006)
8. Is the default-model `PUT` reachable before onboarding, as the provider `PUT` is? (MAJ-007)
9. When an agent is both `needs_provider` and `needs_model`, which `LLMError` code is emitted? (MAJ-008)
10. What happens to agents whose slug resolved through the passthrough rule when OpenRouter is removed? (MAJ-010)
11. How does End reach a row the virtualiser has not mounted? (MAJ-013)
12. Which audit event records that an operator deleted a credential? (MAJ-016)
