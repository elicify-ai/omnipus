# Feature Specification: Web search default, fallback, and provider capabilities

**Created**: 2026-09-26
**Status:** Implemented

**Correction (2026-09-28):** Synced against the code that landed in PR #944 (merge `faaddc6e6`, ADR-096). The gate rounds' fixes are in the ADR's own "Amendment — 2026-09-28" section; this pass carries the same facts into the build instructions and closes a few citation and contract gaps the ADR amendment did not cover. See the dated correction lines below.

Revised 2026-09-26 against founder decisions, then again the same day (round 2) to follow the ADR's corrections after the adversarial review. The decision record is [ADR-096 — Web search: a default, a fallback, and an honest tool](../architecture/ADR-096-web-search-provider-model.md), whose changelog lists every round-2 change and the four questions still open for the founder. This file is the build instructions. Where an earlier draft disagreed, the later revision wins: see [What changed from the first draft](#what-changed-from-the-first-draft) and [What changed in round 2](#what-changed-in-round-2).
**Input**: Founder decisions F1–F10 for the feature that follows the web-search credential fix, which landed as commit `07a75c104` and is this spec's evidence baseline. Issue [#47](https://github.com/elicify-ai/omnipus/issues/47) is background only.

---

## Summary

Seven search providers are already built: Perplexity, Brave, SearXNG, Tavily, DuckDuckGo, Baidu, and GLM. This feature does not add them. It makes them choosable, adds Exa as the eighth, and stops the tool from quietly running a different provider than the one that was asked for.

Today `search_web` is built once, with a single provider baked in. The winner is the first configured provider in a fixed list. A later failure ends the search. Settings shows one "Active" badge, computed by a different list, and choosing a provider there deletes the other providers' key references. DuckDuckGo ships switched on, so it often becomes the provider that actually runs, and the error names DuckDuckGo even when the operator configured someone else.

The operator sets two things: a **default** and a **fallback**. DuckDuckGo stays switched on for a new install, as the keyless safety net, and it leaves the priority list. It runs when it is the default, or when it is the resolved fallback. It does not run because nothing else matched. If the fallback has not been chosen and two or more providers are usable, DuckDuckGo becomes the fallback by a visible rule. If exactly one provider is usable, there is no second try, and a failure is a failure.

The agent may name a provider on the call, but only one that is usable. Naming one that is not usable is a refusal that lists what is usable. A provider the agent named does not fall back when it fails (ADR-096 D6). Omitting the argument is the path that uses the operator's default and fallback.

The tool's description and its argument list are built from the usable set at registration, and they stay short, because that text is sent on every turn.

A written answer from a search engine is out of scope, except that Perplexity is already a model call and must stop throwing away the sources that call returns.

---

## What changed from the first draft

| First draft | This revision | Because |
|---|---|---|
| Exa specified, not required to be done | Exa is in this delivery | ADR-096 D2 |
| The agent cannot pass a provider | The agent may, if it is usable. A failure of that choice does not hop | ADR-096 D5, D6 |
| SearXNG gains an address field and can be newly chosen in Settings | No new SearXNG config surface and no new UI. It stays only so migration does not move an install that already resolves to it | ADR-096 D10 |
| Tool arguments `answer`, `topic`, `media`, and "ignore with a note" | Those arguments are not added. A site filter the provider cannot honour is a refusal, not a note | ADR-096 D8, D9 |
| Three keys: `default_provider`, `fallback_provider`, `fallback_mode` | Two keys. `none` is a value of the fallback. An absent fallback is the automatic-DuckDuckGo rule | ADR-096 D4 |
| Operator domain allow and block lists | Not in this delivery | ADR-096 D9 |

The failover classes, the empty-result rule, and the migration's "do not change who answers" rule are kept. They are ADR-096 D17 and D11.

## What changed in round 2

Driven by the adversarial review of the ADR. Each row is a requirement change, not a wording change.

| Earlier in this spec | Now | Because |
|---|---|---|
| The credential-injection fix is a pending dependency; the D14 warning cannot fire | Both are **already done** at `07a75c104`, the evidence baseline. FR-029 is verify-plus-Exa | The earlier draft's own baseline commit *is* the fix. Building it again would regress two tests already on the tree |
| Migration runs on config load | Runs in the gateway's credential boot, after `InjectFromConfig`, with a defer guard and a marker (FR-030) | On load the environment is empty, so **every** install — including a correctly configured one — would be permanently recorded as `duckduckgo` |
| R1: "exactly one provider is usable" | R1 stated in **roles**; R4b added; two dead clauses deleted; first-match-wins stated | The count form called DuckDuckGo "because nothing else matched" — the original defect — and made the flagship scenario unreachable |
| A save that names a not-yet-usable default is rejected | The save makes the key live first, then judges usability (FR-033) | `APIKey()` is empty at save time and stays empty, so setting Tavily as the default was impossible through the UI at all |
| Call-time usability, mechanism unstated | The tool holds a resolver (FR-032) | `WebSearchTool` holds no config handle, so there was nothing to re-check against |
| A named provider never falls back | Hard-fail only when the call used that provider's own capability; naming the default equals omitting it (FR-023) | The stated reason — a hop hides the failure — is refuted by this spec's own result text; and filling in a redundant argument silently disabled the operator's fallback |
| `exclude_domains` on an unsupporting provider refuses | It proceeds with a note; only `include_domains` refuses (FR-026) | Refusing returns zero pages instead of mostly-right pages, on every new install |
| `Description` is built from the usable set and capped at 80 words | `Description` is constant; the enum stays dynamic; the cap moves onto the rendered definition in bytes at the maximal configuration (FR-024) | The cap bound the smaller half of what ships each turn, was measured where it could not fail, and a dynamic description guaranteed a divergence from `/api/v1/tools` |
| `prefer_native` is unchanged and neutral | A condition on the whole feature (FR-031), with a Settings notice | It ships `true` and removes `search_web` from the tool list entirely on OpenAI, Azure and Codex installs |
| The chat card handles both tool names | It handles only `web_search` while the tool emits `search_web` (FR-034) | Issue #898. Every web search renders as the generic JSON badge today |
| No spend, latency, redaction or observability requirements | FR-036 and FR-037: redaction, a 45-second budget, a depth ceiling, a per-call record | The feature hands the model a choice of who is paid and how hard they search, with no ceiling and no audit |
| Provider ids duplicated across nine lists; Exa would be a tenth | One `searchProviderCatalogue` every consumer derives from (FR-035) | The baseline commit exists *because* two of those lists drifted |

---

## What this spec does not decide

**The credential-injection fix is the baseline, not a future lane.** Commit `07a75c104` — this worktree's HEAD, and the commit every citation below was read from — *is* that fix: its subject is "fix(credentials): web-search and voice keys never reached the process", and it makes `pkg/credentials/inject.go::InjectFromConfig` and `ResolveAll` walk one shared enumeration, `pkg/credentials/inject.go::nonChannelRefsFor`, covering the five keyed web-search providers. Before that commit the refs were resolved for redaction only and never published, every `APIKey()` read an empty variable (`pkg/config/config_defaults_apply.go::TavilyConfig.APIKey` and the Brave, Perplexity, GLM and Baidu twins read `os.Getenv`), and a configured Tavily silently lost to DuckDuckGo. That is history, not a pending dependency.

This spec does not redesign injection. It does require **Exa's reference to join `nonChannelRefsFor` and `pkg/tools/web.go::enabledButKeylessSearchProviders`**, both named so no lane has to guess which list "the injection list" means.

"Usable" means the test in [Resolution](#resolution): switched on, and a required key resolves to a non-empty `APIKey()`, and a required base URL is non-empty. If a reference does not resolve, the provider is unusable and R6 or R7 applies. That visible failure is the correct symptom.

**Where the migration runs is a requirement, not an implementation detail** (ADR-096 D11). `APIKey()` reads the process environment, and the environment is populated at boot step 4 (`credentials.InjectFromConfig`), while every config-load migration runs at step 2. A migration inside config load would therefore read an empty environment on the upgrade load and record `duckduckgo` for **every** install, permanently. It runs in the gateway's credential boot instead, after injection and before the tools are built. See [Migration](#migration).

Voice integrations on the same Settings screen stay as they are: one active transcriber.

**Native model search is not "unchanged and irrelevant" — it decides whether this feature is reachable at all** (ADR-096 D19). `pkg/config/defaults.go::defaultToolsConfig` ships `PreferNative: true`, and `pkg/agent/loop_run_turn.go` filters `search_web` out of the tool definitions entirely when the active provider reports `SupportsNativeSearch()` — true for OpenAI-compatible providers against `api.openai.com` or `*.openai.azure.com` (`pkg/providers/openai_compat/provider.go::isNativeSearchHost`) and for the Codex provider with web search enabled. On those installs the model never sees `search_web`. This spec does not change that default; it requires the Settings screen to say so (FR-031) and it scopes FR-003 and AC-2 accordingly.

SearXNG is descoped by the founder for this feature. One line, here, so it is not redesigned in a work package: no new config surface and no new UI.

---

## Existing Codebase Context

GitNexus was not available in this session (no GitNexus tools in the tool list). The impact rows are from a direct search of callers, not from a graph run. Certainty for those rows is **Inferred**.

Citations below were read from commit `07a75c104`, and in the round-2 pass their **content** was re-read, not only their existence. There are no uncommitted edits in `pkg/tools/web.go` or `pkg/credentials/inject.go` on this tree; the only modified files are `pkg/tools/web_test.go` and an unrelated evidence JSON.

**Correction (2026-09-28):** The table below cites everything as living in `pkg/tools/web.go`, because that was true at the writing baseline `07a75c104`. What actually shipped split most of ADR-096's new logic into sibling files, and this spec never names them: `pkg/tools/web_search.go` (the resolver, the R-table ladder, `normalizeSearchDomains`, the capability refusals, the per-call `searchCallRecord`, `sanitizeProviderMessage`'s redact-and-truncate, `ddgEmptyWarnThreshold`), `pkg/tools/web_search_keys.go` (per-provider key/base-URL plumbing for the dynamic path), `pkg/config/search_provider_catalogue.go` (the single catalogue FR-035 requires), `pkg/config/web_search_roles.go` (the D11/FR-030 migration, `MigrateWebSearchRoles`), and `pkg/gateway/rest_integrations_roles.go` (the new gateway handlers — `buildIntegrationResponse`, `handleIntegrationProviderUpdate`, `applySearchIntegrationRoles` — which replace the `activeSearchProviderID` / `applySearchIntegration` row below; those two symbols are confirmed removed from `pkg/gateway/rest_integrations_auth.go`, not merely superseded in place). `pkg/tools/web.go` keeps the per-provider `Search` methods and the legacy single-provider chain (used only when `Roles` is nil). Code wins; the symbol names in the table below are still correct, only their file is not, in these cases.

### Symbols involved

| Symbol | Role | What it does today |
|---|---|---|
| `pkg/tools/web.go::SearchProvider` | extends | One method: `Search(ctx, query, count, rangeCode) (string, error)`. No provider name, no options, no error class. |
| `pkg/tools/web.go::NewWebSearchTool` | replaces the selection | Picks one provider at construction. Order in code: Perplexity (enabled and keys), Brave (enabled and keys), SearXNG (enabled and base URL), Tavily (enabled and keys), DuckDuckGo (enabled), Baidu (enabled and key), GLM (enabled and key), else DuckDuckGo even when DuckDuckGo is switched off. |
| `pkg/tools/web.go::WebSearchTool` | modifies | `Name()` is `search_web`. `Description()` is a fixed sentence — and **stays** one (FR-024). `Parameters()` exposes `query`, `count` (1–10), `range` (`d`/`w`/`m`/`y`), and already interpolates `t.maxResults`. `Execute` calls the one baked-in provider and returns `search failed: …` with that error only. **The struct is `{BaseTool; provider SearchProvider; maxResults int}` — no config handle, so call-time usability is not implementable until FR-032 gives it a resolver.** |
| `pkg/tools/web.go::TavilySearchProvider` | modifies | POST JSON to `https://api.tavily.com/search` unless `base_url` is set. Sends `search_depth: "advanced"`, `include_answer: false`, `include_images: false`, `include_raw_content: false`, `max_results`, and `time_range` when `range` is set. Key is in the JSON body. |
| `pkg/tools/web.go::BraveSearchProvider` | unchanged except error class and the refusal rule | GET `https://api.search.brave.com/res/v1/web/search`, header `X-Subscription-Token`. Sends `q`, `count`, and `freshness` only. |
| `pkg/tools/web.go::DuckDuckGoSearchProvider` | modifies | GET `html.duckduckgo.com` with `q` and, when `range` is set, `df`. No key. Does not check the HTTP status. Zero parsed anchors returns a success string (`extractResults`). |
| `pkg/tools/web.go::SearXNGSearchProvider` | no new wiring | GET `{base}/search` with `q`, `format=json`, `categories=general`, and `time_range` when `range` is set. Does not send `language` or `pageno`. The body is read with `json.NewDecoder`, not the shared ingest bound. Selected only when enabled and the base URL is non-empty. Sits above Tavily in the chain. |
| `pkg/tools/web.go::GLMSearchProvider` | modifies for depth only, once the vendor page is read | POST JSON. Sends `search_query`, `search_engine`, `search_intent: false`, `count`, `content_size: "medium"`, and `search_recency_filter` (the map's default is `noLimit`, so the field is always sent). |
| `pkg/tools/web.go::BaiduSearchProvider` | unchanged in this delivery | POST JSON. `resource_type_filter` is web only, with `top_k`. Day and week both map to Baidu's week window (`mapBaiduRecencyFilter`). |
| `pkg/tools/web.go::PerplexitySearchProvider` | modifies | POST `https://api.perplexity.ai/chat/completions` with `model: sonar`, `messages`, `max_tokens: 1000`, and `search_recency_filter` when `range` is set. **Does not send `temperature`.** Returns `choices[0].message.content` only. The citations array is not unmarshalled. |
| `pkg/config/config.go::WebToolsConfig` | extends | Nested `brave`, `tavily`, `duckduckgo`, `perplexity`, `searxng`, `glm_search`, `baidu_search`. No default, no fallback, no Exa. |
| `pkg/config/config.go::TavilyConfig` | extends | `Enabled`, `APIKeyRef`, `BaseURL`, `MaxResults` only. Exa mirrors this shape. |
| `pkg/config/defaults.go::defaultToolsConfig` | extends | `DuckDuckGo.Enabled: true`. Every other search provider `Enabled: false`. |
| `pkg/config/config_defaults_apply.go::TavilyConfig.APIKey` (and the Brave, Perplexity, GLM, Baidu twins) | dependency, do not redesign | Reads `os.Getenv(APIKeyRef)`. An unset variable yields an empty key. |
| `pkg/agent/loop_wire.go::registerCoreTools` | modifies | The live constructor. Passes each provider's `Enabled` flag and `APIKey()`. |
| `pkg/tools/general_builtin_catalog.go::GeneralBuiltinMetadata` | do not treat as the live selector | Builds a catalogue entry with `DuckDuckGoEnabled: true` so the name exists in the static catalogue. The comment says that instance is never executed. |
| `pkg/gateway/rest_integrations_auth.go::activeSearchProviderID` | replaces | "Who is active" is a second chain: Perplexity if its key ref is non-empty, else Brave, else SearXNG (enabled and URL), else Tavily, else Baidu, else GLM, else DuckDuckGo. It does not look at `Enabled`. DuckDuckGo is last, not before Baidu. |
| `pkg/gateway/rest_integrations_auth.go::integrationConfigured` | do not confuse with "active" | For a keyed provider, asks `pkg/gateway/rest_config.go::credentialRefResolves`, which reads the credential **store**, not the process environment. |
| `pkg/gateway/rest_integrations_auth.go::applySearchIntegration` | replaces | "Set active" writes this provider's key ref and deletes every other search provider's `api_key_ref`. SearXNG is toggled with `enabled`. Keyed providers are not switched `enabled: true` here. |
| `pkg/credentials/inject.go::InjectFromConfig` and `nonChannelRefsFor` | dependency, already fixed | At `07a75c104` this **is** the fix: both `InjectFromConfig` and `ResolveAll` walk `nonChannelRefsFor`, which covers the five keyed web-search providers. Exa joins that enumeration (FR-021). |
| `pkg/gateway/gateway_boot_credentials.go::bootCredentials` | extends | Owns the documented boot order: Unlock → LoadConfigWithStore → EditionMisbuild → **InjectFromConfig** → ResolveBundle → RegisterSensitiveValues. The migration runs here, after injection (FR-030). |
| `pkg/config/config.go::CredentialStore` | constraint | The interface threaded into `LoadConfigWithStore` has exactly one method, `Set(name, value string) error`. It **cannot read**, so a load-time migration cannot ask the vault who the winner is. |
| `pkg/gateway/rest_config.go::storeCredential` / `refreshConfigAndRewireServices` | modifies | `storeCredential` does `store.Set` and nothing else — no `os.Setenv`. `refreshConfigAndRewireServices` reloads config, re-resolves the bundle and swaps the pointer — no `InjectFromConfig`, no tool rebuild. This is why a save is not live (FR-033). |
| `pkg/gateway/rest_settings.go` credential handler | pattern to copy | Calls `triggerReloadAndWaitOutcome` after its `store.Set`, with a comment citing UAT batch3 finding #6: a credential is live only once a reload's `InjectFromConfig` re-runs `os.Setenv`. The Integrations path does not do this. |
| `pkg/gateway/video_embed_hosts.go::validVideoEmbedHosts` | reused | The existing hostname validator: normalises, validates, de-duplicates, drops invalid entries. The domain-list arguments use its rules rather than a second definition (FR-026). |
| `pkg/agent/loop_run_turn.go` (native-search filter) | constraint | When `tools.web.prefer_native` is set and the active provider reports `SupportsNativeSearch()`, `search_web` is removed from the tool definitions entirely. FR-031. |
| `src/components/settings/IntegrationsSection.tsx` | modifies | One "Set active" button. A key field for providers that require a key. The in-file comment states SearXNG shows "Needs configuration" and cannot be given a base URL from this screen. This spec does not add that field. |
| `contracts/components/schemas/IntegrationProvider.yaml` | contract shape | `active` boolean, no fallback, no usability distinct from `configured`. |
| `src/components/chat/tools/WebSearchResult.tsx::WebSearchResultUI` | modifies | Parses the text result. **It is registered for `web_search` only** (`toolName: 'web_search'`, and `shouldRenderToolCall('web_search', …)`), while the live tool emits `search_web` — so every web search today renders as the generic JSON badge. A production `toolName: 'search_web'` exists nowhere outside test files. It also declares `args.provider` and never renders it. |
| `src/components/chat/OmnipusRuntimeProvider.tsx` | modifies | Documents `search_web → WebSearchResultUI (canonical)` and `web_search → WebSearchResultUI (legacy alias)` in a comment, but mounts one component. The missing `search_web` registration is issue [#898](https://github.com/elicify-ai/omnipus/issues/898); this spec claims its search half (FR-034). |

### Two selectors that disagree

Three tests, not two. All three are live.

| Question | Tool (`NewWebSearchTool`, via `APIKey()`) | Badge (`activeSearchProviderID`) | "Configured" flag (`credentialRefResolves`) |
|---|---|---|---|
| What does it look at? | `enabled`, and a non-empty environment variable | A non-empty key-name string. Ignores `enabled` | Whether the secret exists in the vault |
| Where is DuckDuckGo? | Before Baidu and GLM, if `duckduckgo.enabled` | Last | Always configured |
| Can two keyed providers stay configured? | Yes, but only the first in the list is used | No. Activation deletes the other refs | Yes, until activation deletes them |
| What happens when the chosen provider fails mid-search? | That error is the whole result | Not represented | Not represented |

That split is why an outage can name the wrong provider, and why the badge said Tavily while the environment variable was empty. The design deletes both chains (ADR-096 D15). One resolver decides. The tool and the screen both use it. The badge uses the tool's test, not the key-name string and not the vault check (ADR-096 D13).

### The warning that already fires

**Corrected in round 2.** At `07a75c104` this work is done. `pkg/tools/web.go::enabledButKeylessSearchProviders` runs unconditionally at the top of `NewWebSearchTool`, **before** the selection chain, for all five keyed providers, and logs `"search provider enabled but no resolved key; selection will skip it"` with provider names, refs and a hint. It fires while DuckDuckGo is on. Two tests on this tree already pin it (`TestNewWebSearchTool_EnabledButKeyless_WarnsRegardlessOfSelection`, `TestNewWebSearchTool_EnabledWithKey_NoKeylessWarn`).

The earlier draft described this warning as living in the final `else` and therefore unreachable. That was wrong, and it would have sent a lane to "fix" working code and regress those two tests. What is left is one line: **Exa joins that enumeration** (FR-029). Verify the rest; do not rebuild it.

### Impact assessment

| Symbol modified | Risk | Who breaks if the migration is wrong | Notes |
|---|---|---|---|
| `NewWebSearchTool` / `WebSearchTool.Execute` | **High** if migration attaches a fallback by itself | Every agent whose policy allows `search_web` (`pkg/config/defaults.go::defaultToolPoliciesGeneral` seeds `search_web: allow`; `pkg/coreagent/role_policies_adr090.go` grants it to the built-in roles) | Mitigation is D11: fallback `none` until the operator chooses one |
| `applySearchIntegration` | **High** | An operator who has two keys stored, if we keep deleting refs | The new save must stop deleting other refs |
| Integrations contract + `IntegrationsSection` | Medium | Settings → Integrations, search rows only | Voice rows stay on `active` |
| Tool parameter map | Medium | Models that already call `query` / `count` / `range` | New fields are optional. Fields the usable set cannot honour are absent, not ignored |
| Perplexity request body | Low | Installs that use Perplexity | Adding `temperature: 0` and returning citations. No new model call |

### Relevant flows

| Flow | What changes |
|---|---|
| Agent calls `search_web` with no `provider` | Resolve roles, call default, maybe call fallback, return a result that names who ran |
| Agent calls `search_web` with `provider` | One try of that provider, or a refusal. No hop |
| Operator opens Settings → Integrations | A default radio and a fallback radio. No SearXNG address field |
| Gateway credential boot on an old file | After injection, write the two role keys and the marker once, matching the winner the tool would pick at that moment, with fallback `none` — or defer if any enabled keyed provider's key does not resolve |

---

## Trace to the decision record

| ADR-096 | This spec | Tests |
|---|---|---|
| D1, D2, AC-1 | [Exa](#exa), FR-021 | 28, 29 |
| D3, D4, AC-2, AC-3 | [Config shape](#config-shape), [Resolution](#resolution), FR-001–FR-006 | 1–6, 20, 21 |
| D5, D6, AC-5 | [Agent-chosen provider](#agent-chosen-provider), FR-022, FR-023 | 30–32 |
| D7, AC-6 | [Dynamic description and arguments](#dynamic-description-and-arguments), FR-024 | 33, 34 |
| D8, AC-7 | [Capability matrix](#capability-matrix), FR-025 | 35, 36 |
| D9, AC-8 | [Site filters](#site-filters), FR-026 | 37, 38 |
| D10, AC-9 | [Migration](#migration) SearXNG row, FR-027 | 18b |
| D11, AC-10 | [Migration](#migration), FR-011 | 16–19 |
| D12, AC-11 | [Depth](#depth), FR-015 | 19, 39 |
| D13, D15, AC-12 | [Settings screen](#settings-screen), FR-028 | 26, 40 |
| D14, AC-13 | [The warning that already fires](#the-warning-that-already-fires), FR-029 | 41 (verify, do not rebuild) |
| D16 | [What the tool returns](#what-the-tool-returns), FR-008 | 9, 10, 27 |
| D17 | [Failover](#failover), FR-007, FR-009, FR-010 | 11–15 |
| AC-14 | [Migration](#migration), FR-030 | 42 |
| D4a, AC-16 | [Resolution](#resolution), FR-032 | 43 |
| D17a, D20, AC-11, AC-19 | [Failover](#failover), [Depth](#depth), FR-036, FR-037 | 49, 50 |
| D18, AC-15 | [Settings screen](#settings-screen), [Contract shape](#contract-shape), FR-033 | 44 |
| D19, AC-18 | [What this spec does not decide](#what-this-spec-does-not-decide), FR-031 | 52 |
| D15 (id catalogue), AC-20 | [Exa](#exa), FR-035 | 48 |
| D16 (chat card), AC-17 | [What the tool returns](#what-the-tool-returns), FR-034 | 27 |

---

## Config shape

Two new keys on `tools.web`, plus a migration marker. They are always written after migration or a new install. They must not use `omitempty`: an explicit `none` has to survive a save, and a missing key means "this file has not been migrated", which is a different state from "no fallback".

Both are plain `string` with `yaml:"-"` and **`env:"-"`**. The env tag is load-bearing: `WebToolsConfig` embeds `ToolConfig` with `envPrefix:"OMNIPUS_TOOLS_WEB_"` and every sibling scalar carries an `env:` tag, so without `env:"-"` an environment variable could make `default_provider` non-empty and permanently suppress the migration on that install.

The write technique is `pkg/config/cli_token_migration.go::migrateCLITokenOnDisk`'s, described accurately: unmarshal into a `map[string]any`, mutate the map, re-emit with `json.MarshalIndent`. It is **not** a byte patch — whitespace, key order and number formatting are re-emitted. What it preserves, and why we want it, is **unmodelled keys**. (`pkg/config/config.go::VideoEmbedHosts` is *not* the precedent: it is a `*[]string` **with** `omitempty`, a pointer for a three-state problem we do not have.)

Correct the in-memory config even if the disk write fails, and log the write failure. The next boot retries while the marker is still absent on disk.

| Key | Values | Meaning |
|---|---|---|
| `default_provider` | `perplexity`, `brave`, `tavily`, `duckduckgo`, `baidu`, `glm`, `exa`. `searxng` only when migration found that today's chain already selects it | Who is tried first |
| `fallback_provider` | one of those ids, `none`, or absent | Who is tried second. `none` is "No fallback". Absent is "not chosen" |
| `roles_migrated_at` | RFC 3339 timestamp | The migration marker. Idempotency keys off **this**, not off `default_provider`'s presence, so a later corrective pass can tell whether this migration ran. `config.CurrentVersion` cannot express it: it has always been `1`, the load switch accepts only `CurrentVersion` and hard-fails everything else, and there is no `case 0` branch |

Catalogue ids are `glm` and `baidu`. The config objects stay `glm_search` and `baidu_search`, which is the mapping the gateway derives from the provider catalogue (`pkg/gateway/rest_integrations_auth.go::searchRefSectionByID`).

Provider objects keep their current fields. New fields are only the depth defaults in [Depth](#depth) and the Exa object in [Exa](#exa). Secrets stay in the credential store. Config holds references only.

DuckDuckGo's `enabled` stays `true` in `pkg/config/defaults.go::defaultToolsConfig`. This spec does not add a second keyless provider and does not switch DuckDuckGo off.

### Shipped defaults (new install)

| Key | Value |
|---|---|
| `tools.web.duckduckgo.enabled` | `true` |
| `tools.web.default_provider` | `duckduckgo` |
| `tools.web.fallback_provider` | `none` |
| `tools.web.roles_migrated_at` | the install timestamp |
| `tools.web.tavily.search_depth` | `basic` (only once a `tavily` object exists; the shipped Tavily object is disabled) |
| every other search provider `enabled` | `false` |

A new install has one usable provider, so there is no hop.

### Resolution

`usable` is evaluated at **call** time, not only when the tool is constructed. That requires the tool to hold a live resolver rather than baked key strings — see FR-032, because the current construction cannot do it.

| Provider | Usable when |
|---|---|
| Brave, Tavily, Perplexity, GLM, Baidu, Exa | Switched on, and `APIKey()` is non-empty |
| SearXNG | Switched on, and `base_url` is non-empty after trimming |
| DuckDuckGo | Switched on |

**The rows are evaluated in order. The first row whose condition holds wins.** Without that sentence R1, R2 and R3 can all hold at once and two implementers produce two different products.

| Case | Condition | Default used | Fallback used |
|---|---|---|---|
| R1 | The resolved default is usable and no fallback resolves (`none`, absent with no automatic fallback, same-as-default, or an id that is not usable) | that provider | none. No second try |
| R2 | `fallback_provider` is `none` | the configured default | none |
| R3 | The fallback key is absent, DuckDuckGo is usable, and the default is a usable provider other than DuckDuckGo | the configured default | `duckduckgo` |
| R4 | `fallback_provider` is a different id and that provider is usable | the configured default | that id |
| R4b | `fallback_provider` is a different, **known** id that is not currently usable | the configured default | none. The result lists the fallback as "not called", with the reason |
| R5 | The fallback id equals the default | the configured default | none. The Settings response carries `fallback_ignored_reason: same_as_default`. Boot still proceeds |
| R6 | The default is not usable, and R3 or R4 produced a usable fallback | not called | the fallback is called. The result says the default was not called and why |
| R7 | The default is not usable, and there is no usable fallback | not called | nobody is called. The error names each role as "not called" and the reason. DuckDuckGo is not called unless it is the resolved fallback |
| R8 | The fallback key is absent and DuckDuckGo is not usable | the configured default | none. No other provider is invented |
| R9 | `default_provider` is not a known id | treated as not usable (R6 or R7). The error names the unknown id. No walk of the old list |

Three round-2 corrections are in that table, and each closes a defect:

- **R1 is about roles, not a global count.** It used to read "exactly one provider is usable", which fired on `default_provider: tavily` with Tavily's key gone and DuckDuckGo on — exactly one usable provider, DuckDuckGo — and called DuckDuckGo *as the provider*. That is the original defect, restored: "DuckDuckGo ran because nothing else matched". It also made the flagship scenario ("An unusable default does not pretend to have run") unreachable from the table, because R6 needs R3 or R4 and neither could fire.
- **R4b is new.** A fallback naming a real but currently unusable provider matched no row at all.
- **Two dead clauses are gone.** R3's "two or more providers are usable" (redundant once R1 is role-based, and the clause that made the R1 bug reachable) and R5's "or R3 would pick the default" (R3 requires the default not to be DuckDuckGo and always resolves DuckDuckGo, so it can never pick the default).

A hand-typed `default_provider: searxng` on a file that never resolved to it is treated as a known id, usable only under the SearXNG row above; otherwise R6 or R7. It is never a boot failure.

Save-time rules (the UI and `PUT` reject these; a hand-edited file is healed by R5–R9 at call time rather than refusing to boot):

| Save attempt | Result |
|---|---|
| Same id as default and as fallback | Rejected |
| Fallback points at a provider that is not enabled | Rejected |
| Default points at a provider that is not usable yet | **Changed in round 2.** Not rejected up front. The save stores the key, writes the config, makes both live (FR-033), and only then judges usability. Rejected only if the key still does not resolve afterwards, naming the step that failed. Rejecting before the reload made setting Tavily as the default impossible through the UI at all, because `APIKey()` is empty at save time and stays empty |
| Setting a default | Sets that provider's `enabled` to true. Does not delete any other `api_key_ref`. Does not switch other providers off. Does not change an explicit `none` |
| Operator chooses "No fallback" | Saves `fallback_provider: none`. DuckDuckGo may stay enabled. It is not called |
| Save while the fallback key is absent and R3 would apply, and the operator does not pick "No fallback" | Saves the resolved id (`duckduckgo`), so the file is no longer absent |

---

## Agent-chosen provider

Optional argument `provider`. Omitted: the D4 table and the failover table apply.

**Changed in round 2 (ADR-096 D6).** The old rule was a blanket hard-fail for any named provider. Its stated reason was that a hop "would hide the failure", which [What the tool returns](#what-the-tool-returns) makes impossible — a hop must name the provider that answered, its role, and the default's failure. The rule is now capability-based.

| Situation | Result |
|---|---|
| The id is not in the usable set | Refusal. No request. The error lists the usable ids by their catalogue names. No HTTP error and no vault error |
| The id is usable, and the call succeeds | The result names that provider. The fallback is not called |
| The id is usable, the call **used a capability specific to that provider** — `include_domains`, `depth`, or Perplexity's prose answer — and it fails | **Hard fail. No hop.** The error names the provider, the class, and the message, and says the agent can omit `provider` to use the default and the fallback, or name another usable provider. A substitute cannot deliver what was asked for |
| The id is usable, the call was a **plain query**, and it fails with a hop class | **Hop**, with the honest report: the fallback answered, the named provider failed, here is its error |
| The named id **equals the resolved default** | Treated exactly as if `provider` had been omitted. The full R-table applies, including the operator's fallback |

That last row is the second correction. The old rule made behaviour depend on whether the model filled in an argument carrying no information: `provider: tavily` when Tavily is already the default disabled the operator's configured fallback, while omitting it did not. Models populate optional enum arguments gratuitously, so an agent could unilaterally turn a resilient search into a single point of failure with no lever for the operator.

The trade-off, recorded rather than asserted: hard-failing costs a visible failure and a wasted turn; hopping costs one extra request and a result of a different shape. Where the shape was explicitly requested, the failure is the better answer. Where it was not, the answer is.

---

## Failover

Failover is per call, and only when the agent did **not** pass `provider`. One extra try. Then stop.

### Which failures hop

| Class | Examples | Try the fallback? |
|---|---|---|
| `network` | DNS failure, timeout, connection reset, TLS handshake failure | Yes |
| `auth` | HTTP 401 or 403 after this provider's own key pool is exhausted | Yes. The auth line stays in the report |
| `rate_limit` | HTTP 429 after the key pool is exhausted | Yes, once. No sleep, no third try |
| `upstream` | HTTP 5xx, or HTTP 408 | Yes |
| `bad_response` | Body is not the provider's documented JSON, or DuckDuckGo's HTTP status is not 200 | Yes |
| `empty` | HTTP 200 and a well-formed body with zero results | **No.** The success text is the empty answer, and it names the provider |
| `rejected` | HTTP 400 or 422, invalid arguments, or a refusal for an `include_domains` or `depth` the provider cannot honour | **No.** No request is sent for a refusal. An `exclude_domains` the provider cannot honour is **not** a refusal — the search proceeds with a note |
| `cancelled` | The turn was cancelled | **No** |
| `unusable` | Not called: no key, no base URL, switched off, unknown id | Not a call. R6 or R7 |
| Ingest bound | The existing response-size bound refuses the body | **No** |

Key rotation inside Brave, Tavily, and Perplexity stays inside that provider. The hop happens only after the pool is exhausted. GLM, Baidu, and Exa have a single key; a 401 on that key is `auth`.

**One call has a wall-clock budget** (ADR-096 D17a). The existing timeouts are `searchTimeout = 10s` (Brave, Tavily, DuckDuckGo, GLM, the final DuckDuckGo) and `perplexityTimeout = 30s` (Perplexity, Baidu), and the key pool retries **per key** before the hop even starts — so a Perplexity default plus a DuckDuckGo fallback is a 40-second worst case inside one tool call, more with several keys.

| Control | Value |
|---|---|
| Total budget for one `search_web` call | 45 seconds of wall clock |
| The default attempt | Its provider timeout, bounded by the remaining budget |
| The fallback attempt | The remaining budget. If less than its provider's own timeout remains, it is **skipped** with the reason `no time budget remaining` |

Without a stated budget, the headline behaviour is dead on the slowest providers — silently, and only under failure conditions nobody reproduces locally.

DuckDuckGo limit, stated so it is not "fixed" by guesswork: a **200** response with zero result anchors stays `empty`, including a block page that returns 200 and HTML without those anchors. This spec does not invent a fingerprint. A non-200 DuckDuckGo response becomes `bad_response` and does fail over, when a fallback exists and the agent did not name a provider.

### What the tool returns

The tool result is still text. `WebSearchTool.Execute` returns one string, shown to the agent and to the user. The chat card shows the same facts. It does not invent a provider name.

Success, no hop:

```
Search provider: tavily (default)
Results for: …
```

Success, default failed and the fallback answered:

```
Search provider: duckduckgo (fallback)
Note: tavily (default) failed and was not used for these results. tavily: network: request failed: …
Results for: …
```

Success, default was not called:

```
Search provider: duckduckgo (fallback)
Note: tavily (default) was not called: no API key.
Results for: …
```

Both failed (tool **error**):

```
search failed
- tavily (default): network: request failed: …
- duckduckgo (fallback): network: request failed: …
```

Default unusable and no fallback (tool error):

```
search failed
- tavily (default): not called: no API key
```

Agent named a provider that then failed (tool error, no hop):

```
search failed
- tavily (chosen): network: request failed: …
This call named tavily, so the fallback was not tried. Omit provider to use the default and the fallback, or set provider to one of: duckduckgo.
```

Agent named an unusable provider (tool error, nobody called):

```
search failed
- exa: not usable
Usable providers: tavily, duckduckgo
```

Reason vocabulary for a "not called" line — one of these, never free text: `no API key`, `switched off`, `unknown id`, `same as default`, `cannot honour include_domains`, `no time budget remaining`.

Two rules on every provider message that reaches this text:

| Rule | Why |
|---|---|
| It passes through the registered sensitive values before it is returned or logged | `TavilySearchProvider.Search` sends `"api_key": apiKey` in the **JSON request body**, so an upstream 4xx from a provider, proxy or WAF that echoes the payload has a path into the model's context and the transcript. `cfg.RegisterSensitiveValues` (boot step 6) already holds every resolved plaintext — this invokes an existing mitigation rather than building one |
| It is truncated to 300 characters | An unbounded upstream string in a per-turn result is both a cost and a leak surface |

**Correction (2026-09-28): a second, narrower redaction layer that this section never described.** `pkg/tools/web.go::redactURLUserinfo` strips a `user:password@` segment from any message that names a URL, before that message reaches `buildDynamicSearchProviders`'s constructor-failure log and the `not usable: <cause>` text a call surfaces (comment cites "gate round 2, security NEW-1"). This is a different secret class than the row above: `RegisterSensitiveValues` redacts *resolved API keys* it already knows about; a credentialed URL (for example a malformed SearXNG or Exa `base_url` with embedded basic-auth) is never registered there, and Go's own `url.Parse` error quotes the whole offending input, including the password, verbatim. Both layers now apply: registered-secret redaction plus truncation for provider *messages*, and this regex-based redaction for URL userinfo specifically, wherever a URL can appear in a constructor error. No FR named this; it is added to FR-036's evidence rather than reopened as a new requirement, since it strengthens the same "no secret reaches the transcript" property FR-036 already states.

| Rule | |
|---|---|
| Every provider that was called is listed, with its class and message | |
| A provider that was not called is listed only as "not called", with the reason, and only if it was the configured default or the resolved fallback | |
| The old priority winner is never mentioned just because it exists | |
| A single line that names only the last provider is not a legal error when more than one role was involved | |
| Empty success still names the provider that returned no rows | |

`WebSearchResultUI` shows the answering provider, and one extra line when a hop or a skip happened. It reads that from the result text. It must not hardcode a provider name.

**It must also be registered for the name the backend emits.** Today it is registered for `web_search` only while the tool is `search_web`, so every web search renders as the generic JSON badge — issue [#898](https://github.com/elicify-ai/omnipus/issues/898). FR-034 adds the `search_web` registration, and the test asserts the card renders for a call **named `search_web`**, not only that the parser handles a fixture.

---

## Dynamic description and arguments

Built when `pkg/agent/loop_wire.go::registerCoreTools` constructs the tool. `WebSearchTool` carries the usable set: id, a fixed one-line "good for", and whether that id honours `depth` and site filters. `Description()` and `Parameters()` read the set. They are not constants.

`pkg/tools/general_builtin_catalog.go::GeneralBuiltinMetadata` builds a different instance, with only DuckDuckGo switched on, and that instance is never executed. Its text is the one-provider text. The definition the agent is shown is the one `registerCoreTools` registers (`RegisterReplacing`). A test asserts the agent's registered definition, not the catalogue instance.

**`Description()` stays a constant sentence; only the argument list is built from the usable set** (ADR-096 D7, changed in round 2). The per-provider "good for" lines move into the **`provider` argument's own description**, generated from the same usable set — which is where the enum needs them anyway. A dynamic `Description` bought nothing the live refusal does not already buy, and it guaranteed a divergence between what the agent sees and what `GET /api/v1/tools` serves, because `GeneralBuiltinMetadata` constructs its instance with `WebSearchToolOptions{DuckDuckGoEnabled: true}` and has no config in scope at all. With a constant `Description` the two are byte-identical by construction, so no test has to police the gap.

The size control moves onto what is actually sent, and is measured in bytes:

| Control | Value |
|---|---|
| What is capped | The **rendered definition**: description plus the serialised `Parameters` object |
| Unit | Bytes, roughly four per token. Not words — the cost is billed per token, and bytes are deterministic without adding a tokenizer |
| Cap | 2,400 bytes (about 600 tokens) |
| Measured at | The **maximal** configuration: all eight providers usable, every capability argument present. Not the degenerate one |
| On overflow | Drop the per-provider "good for" clauses, keep the enum. The test fails, so it is a build-time decision |
| Prose guidance, not a control | One short sentence per argument. No credit prices |

**The definition must be stable for the lifetime of an agent instance.** `pkg/agent/loop_run_turn.go::prepareLLMRequest` sends `prompt_cache_key` set to the agent's id, so a definition that changes mid-session invalidates that agent's cached prompt prefix for the whole conversation — a cost far larger than the description's length. Rebuilding on credential unlock is therefore refused, not merely "not required". (A full reload does rebuild it: `registerCoreTools` is reached only through `registerSharedTools`, whose three production call sites are boot, `ReloadProviderAndConfig`, and the agent-upsert path.)

The "good for" lines are fixed:

| Id | Line | Honours depth | Honours site filters |
|---|---|---|---|
| `duckduckgo` | keyless web results | no | no |
| `brave` | web results | no | no |
| `tavily` | web results | yes | yes |
| `perplexity` | a short written answer plus sources | yes | yes, once the official filter shape is confirmed (see the matrix) |
| `glm` | web results | yes, except agent `low` | no, until the request shape is read |
| `baidu` | web results | no | no |
| `exa` | find pages like this | no | **yes** — `includeDomains` / `excludeDomains`, camelCase, verified |
| `searxng` | self-hosted results | no | no |

`searxng` appears in the argument list only when it is already usable. The screen still does not grow a way to create it.

| Degenerate case | Arguments offered | Description |
|---|---|---|
| DuckDuckGo only | `query`, `count`, `range` | Names DuckDuckGo. Does not mention depth or site filters |
| Exactly one provider, and it honours depth and site filters (Tavily only, for example) | `query`, `count`, `range`, `depth`, `include_domains`, `exclude_domains`. No `provider` | Names that provider |
| Two or more usable, mixed abilities | `provider` whose allowed values are the usable ids, plus `depth` and the domain arguments only if the **resolved default** honours them | Constant. The per-usable-id clauses live in `provider`'s own description |

`query`, `count`, and `range` are always offered. `range` already maps per provider (`mapBraveFreshness`, `mapTavilyTimeRange`, `mapPerplexityRecencyFilter`, `mapDuckDuckGoDateFilter`, `mapSearXNGTimeRange`, `mapGLMRecencyFilter`, `mapBaiduRecencyFilter`).

The set is a snapshot from registration. Call time still applies the usable test. A provider that stopped being usable is refused, not called. A provider that became usable later can still be the operator default, and will not appear in `provider`'s allowed values until the tool is built again. This spec does not require a rebuild on unlock.

**The capability arguments follow the resolved default, not "any usable provider"** (changed in round 2). With Tavily and Brave both usable and Brave as the operator's default, the old rule advertised `include_domains` and every use of it refused — and since the schema is unchanged next turn, the model was invited to try again. A permanent per-install turn tax, in a feature whose purpose is to stop the tool doing something other than what was asked. The refusal still names the usable providers that *can* honour the argument, so the capable path stays one turn away.

Combining `include_domains` or `depth` with a provider that does not honour it is still a refusal. `exclude_domains` is not — see [Site filters](#site-filters).

---

## Capability matrix

"Sent today" is what `pkg/tools/web.go` puts on the wire at `07a75c104`, read in this task. "This delivery" is in scope. "Noted, not now" is recorded so it is not slipped in. A written answer is a non-goal (ADR-096 D8), not a "later" item, except Perplexity's citations.

Four themes across the providers: depth, site filters, result type (news, image, video), and a written answer. The first two are in scope. The third is noted, not now. The fourth is a non-goal.

| Provider | Sent today | In scope | Noted, not now | Non-goal |
|---|---|---|---|---|
| Tavily | `search_depth: advanced`, `include_answer: false`, `include_images: false`, `include_raw_content: false`, `max_results`, `time_range` when `range` is set. Key in the JSON body | Depth (D12). `include_domains`, `exclude_domains` (D9) | `topic`, `chunks_per_source`, `country`, `auto_parameters`, images, raw page text | `include_answer` stays false. Not a tool argument |
| Perplexity | `model`, `messages`, `max_tokens`. `search_recency_filter` when `range` is set. No `temperature`. Citations not read | Return the citations array. Send `temperature: 0`. Depth via `web_search_options.search_context_size`. `search_domain_filter` | `return_related_questions`, `return_images` | Do not add a second mode that turns Perplexity into a link list. `answer: false` is not a thing this tool offers |
| Brave | `q`, `count`, `freshness` | Nothing new on the request, except the error class and the D9 refusal | `extra_snippets` (the founder notes extra excerpts at no extra cost), `goggles_id`, `result_filter`, `country`, `search_lang`, `safesearch`, `spellcheck`, `text_decorations` | `summary` is not added |
| GLM | `search_query`, `search_engine`, `search_intent: false`, `count`, `content_size: medium`, `search_recency_filter` always | `content_size` as the depth axis, for `medium` and `high` only | Domain filter, once the request shape is read from the current page | `search_intent` stays false. It rewrites the query and is that provider's model |
| Baidu | `messages`, `search_source`, `resource_type_filter` web with `top_k`, `search_recency_filter` when `range` is set | Nothing new. Day and week both stay "week"; the result note already in the code's comment may be surfaced as one line, and is not a new argument | Image and video resource types | — |
| SearXNG | `q`, `format=json`, `categories=general`, `time_range`. Not `language`, not `pageno`. Body not ingest-bounded | Put the body through the same ingest bound as the other providers. No other change | `categories`, `engines`, `safesearch` as operator config | No new Settings field (D10) |
| DuckDuckGo | HTML page, `q` and `df`. Status ignored | Check the HTTP status | — | No new knobs. It is a scrape |
| Exa | Nothing. Not in the tree | The provider itself. See [Exa](#exa) | Page text and highlights, off unless a later spec says otherwise | — |

### Perplexity citations and temperature

The response struct gains the citations array the founder named (`citations`). Append them under the prose as a source list of URLs. If the array is missing or empty, the result says `Sources: none returned`. Do not invent URLs from the prose.

Confirm the JSON key against the current Perplexity response before coding. This writing pass did not re-read the official schema. If the key differs, use the official key. The requirement is "the sources the call already returned are shown", not a particular spelling guessed past the founder.

Send `temperature: 0` on every Perplexity search request. Do not send `0.2`. The committed code sends neither; third-party pages retrieved 2026-09-26 describe the vendor default as `0.2`. Explicit `0` is what stops that default.

### Site filters

| Argument | Type | Default |
|---|---|---|
| `include_domains` | list of hostnames, at most 10 | omit |
| `exclude_domains` | list of hostnames, at most 10 | omit |

**Hostname is defined by reference, not by adjective.** The repository already owns the validator: `pkg/gateway/video_embed_hosts.go::validVideoEmbedHosts`, which normalises, validates and de-duplicates while dropping syntactically invalid entries. Its rules apply here, so two implementers cannot write two validators:

**Correction (2026-09-28):** `pkg/tools` cannot import `pkg/gateway`, so the search tool does not call `validVideoEmbedHosts` directly. It **mirrors** the rule set in its own `pkg/tools/web_search.go::validSearchHostname` / `normalizeSearchDomains` (same validity test, normalisation, per-entry 253-character cap and 10-entry count cap), as ADR-096's 2026-09-28 amendment already states. One behaviour is deliberately **not** mirrored: `validVideoEmbedHosts` drops an invalid entry and keeps going (it validates an operator-set config list at load time). `normalizeSearchDomains` rejects the whole call on the first invalid entry — matching this section's own rule below ("A value that fails validation is rejected. No search is attempted") — because these are agent-supplied call arguments, not a config list. The "Its rules apply here" sentence above means the hostname-validity rules, not the drop-vs-reject behaviour.

| Question | Answer |
|---|---|
| Valid | A bare hostname as that validator defines it. Lower-cased, trailing dot stripped, duplicates collapsed |
| Rejected | Wildcards (`*.example.com`), ports, paths, query strings, userinfo, schemes, IP literals |
| Per-entry length | 253 characters, the DNS name limit |
| Count | 10 per list |
| A domain in both lists | Excluded. The exclusion wins |

A value that fails validation is `rejected`. No search is attempted.

| Provider that will run | Behaviour |
|---|---|
| Tavily | Send `include_domains` and `exclude_domains`, snake_case, under those names |
| Perplexity | Send `search_domain_filter`. A third-party page retrieved 2026-09-26 shows a list, with a leading `-` on an excluded domain. Map includes as plain names and excludes with that prefix. A name in both lists is excluded. If the current official page disagrees, follow it. Do not drop an exclude to force the call through |
| GLM | Refuse while the request shape is unread. The field name `search_domain_filter` is known; whether it is a string or a list, and whether it can exclude, is not. A later one-line change may send includes once the page is read. Excludes stay a refusal if the field cannot express them |
| Exa | Send **`includeDomains` and `excludeDomains` — camelCase** (verified in round 1). Do not copy Tavily's snake_case: those keys are ignored and the call returns unfiltered results, which is the class of lie this feature exists to remove |
| Brave, Baidu, SearXNG, DuckDuckGo | Cannot honour either list |

**The two lists are not the same kind of thing** (ADR-096 D9, changed in round 2):

| Argument | When the provider that would run cannot honour it | Why |
|---|---|---|
| `include_domains` | **Refuse before any request.** The error names the provider, says the filter was not applied, and names the usable providers that can honour it | An include is a *requirement*: the caller restricted the answer to a set of sites, so anything else is off-target. A note can be missed; the wrong pages cannot be unsent |
| `exclude_domains` | **Proceed, and say so.** The result carries a `Note:` line naming the provider and stating that the exclusion was not applied | An exclude is a *preference*: the caller is removing noise. Refusing returns **zero** pages instead of mostly-right pages — on every new install, where DuckDuckGo is the only provider, that is every search |

**When the default honours the filter and the resolved fallback does not:**

| Argument the agent set | The hop |
|---|---|
| `include_domains` | The fallback is **not eligible**. The hop is skipped. The result reports the default's failure plus `fallback not eligible: cannot honour include_domains` |
| `exclude_domains` | The hop proceeds, with the `Note:` line above |

Refusal for an include, before any request, and with no hop:

```
search failed
- brave (default): rejected: site filter is not supported
Providers that support site filters: tavily, exa
```

The ids in the second line are the usable ones that honour the filter, not the whole catalogue. If none do, the line says so, and the arguments should not have been offered (the snapshot was stale, or the model ignored the schema). Still no request.

### Depth

Not a written answer. Operator default, plus an optional agent argument `depth`: `low`, `medium`, or `high`. Omitted means the operator default.

| Agent `depth` | Tavily `search_depth` | Perplexity `search_context_size` | GLM `content_size` |
|---|---|---|---|
| `low` | `fast` | `low` | Refuse. No GLM value was established for this |
| `medium` | `basic` | `medium` | `medium` |
| `high` | `advanced` | `high` | `high` |

Tavily `ultra-fast` is operator-only. It is not an agent value. If the operator set it and the agent omits `depth`, send `ultra-fast`.

**The operator's configured depth is a ceiling, not just a default** (ADR-096 D20). `depth` may ask for less and is **clamped** if it asks for more, with a note in the result. Without this, an agent — prompt-injected or merely looping — can override an operator's `basic` or `ultra-fast` and send Tavily `advanced`, the most expensive setting, on every call. No new config key: it is a rule about values that already exist.

| Key | New install | Existing object, key absent |
|---|---|---|
| `tools.web.tavily.search_depth` | `basic` | migrate to `advanced` |
| `tools.web.perplexity.search_context_size` | absent, which means do not send the field | do not write one |
| `tools.web.glm_search.content_size` | `medium` | write `medium` |

Perplexity's context size is sent only when the operator set it or the agent passed `depth`. Sending a default on an existing install would change the bill (D11).

Brave, Baidu, SearXNG, DuckDuckGo, and Exa: if the agent sets `depth` and that provider would run, refuse the same way as a site filter. Name the usable providers that honour depth. Do not hop.

```
search failed
- duckduckgo (chosen): rejected: depth is not supported
Providers that support depth: tavily
```

---

## Exa

In this delivery (ADR-096 D2, AC-1).

| Item | Value |
|---|---|
| Config | Mirrors `TavilyConfig`: `enabled`, `api_key_ref`, `base_url`, `max_results`. JSON object `tools.web.exa` |
| Shipped default | `enabled: false`. `base_url` default `https://api.exa.ai/search` when empty at call time |
| Request | POST JSON, header `Authorization: Bearer <key>` |
| Response | Read title and URL from the result list. Do not request page text |
| Key injection | `pkg/credentials/inject.go::nonChannelRefsFor`, the same enumeration Tavily is on, **and** `pkg/tools/web.go::enabledButKeylessSearchProviders`. A missing entry means Exa fails the way Tavily did, silently |
| Roles | Can be default or fallback with no further resolver change. It is a known id |
| Settings | One catalogue row, same key field as Tavily. The row's one line of help is "find pages like this" |
| Good-for line | `find pages like this` |

Wire names for domain filters are read from the current Exa page before coding. This pass did not re-fetch it. The tool argument names do not change if the JSON names differ.

Not in this delivery, named so they are not added as extras: SerpBase, Kagi, You.com, SerpAPI. Issue #47 mentioned them. No decision adds them.

---

## Migration

**Where it runs — the round-2 correction, and the one that would have corrupted every install.** Not in config load. `APIKey()` reads the process environment, and `pkg/gateway/gateway_boot_credentials.go::bootCredentials` fixes the order: Unlock (1) → `LoadConfigWithStore` (2, where every load-time migration runs) → EditionMisbuild (3) → **`InjectFromConfig` (4, the first `os.Setenv`)** → ResolveBundle (5) → RegisterSensitiveValues (6). On the upgrade load — step 2 — every `APIKey()` returns `""`, so the winner would always be the final DuckDuckGo branch, for every install, including one with a perfectly resolvable Tavily key. Because idempotency then keys off the migration's own output, `default_provider: duckduckgo` would be permanent and would never self-correct.

Reading the vault instead is not available at step 2: `pkg/config/config.go::CredentialStore`, the interface threaded into `LoadConfigWithStore`, has exactly one method — `Set(name, value string) error`.

So: the migration runs inside `bootCredentials`, **after step 4 and before the agent loop builds its tools**, and is mirrored in `pkg/gateway/gateway_reload.go::executeReload` immediately after that function's own `InjectFromConfig`. At boot the config-file watcher does not exist yet, so no write suppression is needed; on the reload path the written bytes must be registered with the `configSelfWriteRegistry` that `executeReload`'s services already carry, or the watcher reloads on our own write. A CLI that loads config without booting credentials does not migrate — correct, since it also does not inject and could not tell.

It runs once, when the marker `tools.web.roles_migrated_at` is absent. Removing only `fallback_provider` later is R3 (unset), not a second migration.

**Correction (2026-09-28):** This migration needed, and got, a founder-approved carve-out from an unrelated standing rule. `docs/internal/architecture/ADR-067-registry-fed-catalog-and-provider-identity.md`, §8c ("Amendment 2026-09-27") records a **narrow exemption** for the `roles_migrated_at` token from the 2026-09-15 greenfield ruling that otherwise bans migration/alias machinery in `pkg/config` and `pkg/providers` (`pkg/providers/greenfield_test.go::TestGreenfield_NoAliasMachinery`, `scripts/check-greenfield-providers.sh`). The exemption is scoped to the literal token `roles_migrated_at`, only in `pkg/config/config.go` and `pkg/config/web_search_roles.go`, and does not extend to provider-identity migrations generally. Neither this spec nor ADR-096 cited it; it belongs here because a reader following "why does a migration exist at all under the greenfield rule" needs this pointer.

1. Compute the winner with the usable test — the provider `NewWebSearchTool` would construct at this moment, including the final DuckDuckGo branch that runs even when `duckduckgo.enabled` is false.
2. **Defer if the install is ambiguous.** If any keyed provider is `enabled: true` with a non-empty `api_key_ref` whose key does not resolve, write nothing, log it, and retry on the next boot. Recording `duckduckgo` is wrong for that operator whether the cause is a missing key or a broken one, and deferring is safe because the pre-migration path still works. It is the same state `enabledButKeylessSearchProviders` already detects and warns about.
3. Set `default_provider` to the winner's catalogue id (`glm_search` → `glm`; `baidu_search` → `baidu`).
4. Set `fallback_provider` to `none`.
5. Write `roles_migrated_at`.
6. Do not change any `enabled` flag, except steps 8 and 9. Do not delete any `api_key_ref`.
7. If a `tavily` object exists and `search_depth` is absent, write `advanced`, and log the value with its reason. If there is no `tavily` object, leave it absent. If a `glm_search` object exists and `content_size` is absent, write `medium`. Do not write `search_context_size` onto Perplexity.
8. If the winner is the final DuckDuckGo branch and the flag is false, set `duckduckgo.enabled` to true and log a warning that a switched-off DuckDuckGo was the live provider and the flag was turned back on.
9. **If a keyed provider is `enabled: false` with a key reference that resolves, treat that as the operator's choice**: set `default_provider` to it, switch it on, and log the corrected flag. `pkg/gateway/rest_integrations_auth.go::applySearchIntegration` never sets a keyed provider's `enabled: true` — it writes `api_key_ref` and toggles only `searxng.enabled` — and defaults ship all five keyed providers off. So "ref set, switched off" is exactly what the UI's own "Set active" button produces. The operator pressed the button and the screen said "Active". Reading that as a choice of DuckDuckGo would cement a UI bug as operator intent. Where more than one provider is in this state, the old chain order breaks the tie and the log says which and why.

Persist with the technique described in [Config shape](#config-shape).

| Install | Who runs a successful search at the baseline | After migration, before Settings is touched |
|---|---|---|
| (a) Nothing configured. DuckDuckGo on, no keyed provider usable | DuckDuckGo | Default `duckduckgo`, fallback `none`. No hop |
| (b) DuckDuckGo only | DuckDuckGo | Same as (a) |
| (c) Tavily on, key resolves | Tavily, depth `advanced`. A failure is terminal | Default `tavily`, fallback `none`. A Tavily network error does not call DuckDuckGo. `search_depth` is `advanced` |
| (d) Tavily on, key reference set, key resolves. The founder's live instance | Tavily, depth `advanced` | Default `tavily`, fallback `none`. No hop added. Depth stays `advanced`, not `basic`. The badge no longer treats the key name as "who runs" |
| (e) Tavily key reference set and resolvable, Tavily switched **off** — the "Set active" shape | DuckDuckGo | Default `tavily`, switched on, logged as a corrected flag (step 9) |
| (f) Tavily on, key reference set, key does **not** resolve | DuckDuckGo, with the keyless warning already firing | **Nothing written.** The migration defers and retries next boot (step 2) |

**The cost note on row (d), stated honestly.** Keeping `advanced` is not a cost increase this migration causes: `TavilySearchProvider.Search` **hardcodes** `search_depth: "advanced"` on every call today, so that install pays `advanced` with or without the migration. Writing `basic` instead would be this feature silently cheapening a search nobody asked us to cheapen. What is true — and what the ADR's earlier Positive section wrongly denied — is that row (d) does get a cost change, from the key starting to work. So: keep `advanced`, log it, and show the inherited depth in Settings so it is one click from `basic`.

Row (e) replaces the earlier draft's rule that migration "follows the tool" for a switched-off Tavily. That rule read a UI bug as an operator decision.

SearXNG enabled, base URL set, and no earlier chain entry usable: the winner is SearXNG, because the chain places it above Tavily. Migration sets `default_provider: searxng` and `fallback_provider: none`. No new field is written. AC-9.

The operator who wants a fallback after upgrade moves the fallback radio. Upgrade does not do it for them. An explicit `none` is not flipped to DuckDuckGo when they later change the default.

---

## Settings screen

Settings → Integrations, the Web Search group (`IntegrationsSection`). Voice rows do not gain these controls. SearXNG does not gain an address field.

| Control | Behaviour |
|---|---|
| List order | Whatever the catalogue order already is. Help text: "The order of this list does not choose who is tried first." |
| Default | A radio. Exactly one row, among providers the screen lists. Exa is listed. SearXNG is not offered as a new choice |
| Current default is `searxng` | Show the id as the current default, as text. No editor. The radio can move the default to a listed provider |
| Fallback | A second radio, plus a visible "No fallback" choice. Not an unselected blank |
| Automatic label | When the stored fallback is absent and R3 resolves to DuckDuckGo, that radio shows "Automatic fallback". Saving without picking "No fallback" writes `duckduckgo` |
| Explicit `none` | Shows "No fallback". Changing the default does not move it |
| Same row | The fallback radio on the default's row is disabled. Reason: "A provider cannot fall back to itself." |
| Keys | Adding a key stores it and leaves every other key in place |
| Usable | A row is "ready" only when the tool's test passes (`enabled` and non-empty `APIKey()`, or DuckDuckGo simply on). A key name whose `APIKey()` is empty shows "key not reaching search". It is not the default badge and it is not ready. **A key stored in the same request reads ready, not "key not reaching search"** — the response is built from post-reload state (FR-033). Without that, this row replaces a false positive with a confusing false negative |
| Badge | "Active" goes away for search rows. Replaced by "Default" and "Fallback". Voice rows keep "Active" |
| Native model search in effect | The group states that the active model searches natively and that these settings are not currently deciding who answers, and names **no** provider as the one that answers (FR-031). Without this, a screen saying "Default: DuckDuckGo" is confidently naming a provider that is not being used |
| Depth | The row shows the provider's configured depth, so an operator who inherited `advanced` from migration can see it and change it |

**Correction (2026-09-28):** The Depth row above did not ship. `src/components/settings/WebSearchGroup.tsx` states this deliberately in its own header comment: "Depth and site-filter controls are deliberately absent: the landed contract carries no depth or capability field, so any control here would offer a value nothing would read — exactly what US-5 forbids." Neither `IntegrationProvider.yaml` nor `IntegrationProvidersResponse` carries a depth field, so there is nothing for a Settings row to read. An operator who inherited `advanced` from migration currently has no way to see it in Settings; they can still change it by hand-editing `tools.web.tavily.search_depth`. This is a real gap against this spec's own text, not an open design question — flagged for the founder as a follow-up, not fixed here, because adding the field is a contract change (five-step process) this pass did not make.

`configured` on the wire may still mean "the secret is in the vault". It is not the badge.

**Correction (2026-09-28):** The top-level `active_search` field (in [Contract shape](#contract-shape) below) gets the same treatment as the per-row `active` field: `pkg/gateway/rest_integrations_roles.go::buildIntegrationResponse` sets it to mirror `default_search` — the same stored id, no chain derivation — per its own comment ("active_search mirrors default_search … same stored id, no chain derivation"). The spec's Contract shape table documents this for the per-row `active` field but never mentions `active_search` by name; this is the same fact, restated for completeness.

---

## Contract shape

`backend-lead` edits `contracts/` and regenerates. Nobody else edits generated files. The five steps, in order: add the schema, reference it from `contracts/openapi.yaml`, run `scripts/gen-contracts.sh`, commit the generated diff with the schema, then write the handler against the generated type only.

`IntegrationProvider` (search rows; voice rows keep today's `active` and leave the new fields unset):

| Field | Type | Meaning |
|---|---|---|
| `active` | boolean, kept | Voice: unchanged. Search: true only when this row is the default, so an old client still has the field. It is no longer "whoever the priority list picked" |
| `fallback` | boolean | True when this row is the resolved fallback |
| `fallback_automatic` | boolean | True when this row is the fallback because the stored value is absent and R3 applied, not because the operator picked the radio |
| `usable` | boolean | The tool's test. False when the key name is set and `APIKey()` is empty |
| `id` | string → **enum** | Derived from the single provider catalogue, not free-form. Today it is `type: string` in `contracts/components/schemas/IntegrationProvider.yaml`, which is one of the nine hand-maintained id lists this feature has to collapse (FR-035) |

`IntegrationProvidersResponse` gains:

| Field | Type | Meaning |
|---|---|---|
| `default_search` | string | The default id |
| `fallback_search` | string or null | The resolved fallback, or null when there is none |
| `fallback_mode` is **not** added | — | The first draft had this. It is not a stored mode |
| `fallback_ignored_reason` | string, optional | `same_as_default` when R5 healed a file |
| `native_search_in_effect` | boolean | True when `prefer_native` is in effect for the active model (D19/FR-031). Not in the first two drafts of this table — added below |

**Correction (2026-09-28), two gaps closed:**

1. **`native_search_in_effect` was missing from this table entirely.** It shipped on `IntegrationProvidersResponse` (`contracts/openapi.yaml`, `pkg/api/generated/openapi_types.gen.go::IntegrationProvidersResponse.NativeSearchInEffect`) to satisfy FR-031/D19/test 52, and the contract's own doc comment flags the very gap this correction closes. It is always present (not optional): true when the active model's native search is in effect, false otherwise.
2. **`default_search` and `fallback_search` have a third wire state this table did not name.** Both are `*string` with `omitempty` in the generated Go type, so a nil pointer omits the key — Go's `omitempty` cannot emit a literal JSON `null`. The three states actually served are: key **absent** (roles not yet decided — migration hasn't run or deferred), key **present with a string** (a role is set), and for `fallback_search` only, key **present with JSON `null`** (roles decided, no fallback). The third state does not come from the generated struct: `pkg/gateway/rest_integrations_roles.go::writeIntegrationResponse` does a documented post-marshal step — it marshals normally, then re-injects a literal `null` for `fallback_search` specifically when the roles are decided and no fallback was resolved, because "No fallback" must be distinguishable on the wire from "not decided yet". The generated wire struct itself is never hand-edited (Hard Constraint #8); this is a narrow, commented exception layered on top of it, not a parallel type.

`IntegrationProviderUpdateRequest` needs more than one new field. Today it has `additionalProperties: false` and exactly three properties — `kind`, `api_key`, `active` — and the handler computes `makeActive := body.Active != nil && *body.Active`, so `active: false` is accepted and silently does nothing. The SPA offers only "Set active" and "Save & activate", so there is no way to store a key without activating and no way to deactivate.

| Required change | Why |
|---|---|
| Storing a key must be separable from assigning a role | The save-rule table assumes the operator can add a key and separately choose a default |
| `active: false` gets a defined meaning, or is rejected | Silently accepting a no-op is how a UI comes to lie |
| A `fallback` boolean | "No fallback" has to be expressible |
| No `base_url` field | SearXNG stays descoped |
| `active: true` deleting other search key refs is dropped | That makes a fallback impossible |

Sending `active: true` and `fallback: true` for the same id is HTTP 400. A default whose key does not resolve **after** the reload (FR-033) is HTTP 400 ("needs an API key") — not before it, which was unsatisfiable.

`additionalProperties` stays false, so each of these is a breaking change and follows the five contract steps. **The regenerated `pkg/gateway/inboundschemas/` copy is part of the same commit**: the runtime validator reads the embedded YAML, so an un-regenerated schema edit validates against stale rules and the drift is invisible. The SPA must not send the new fields until the schema and the generated types include them.

The `search_web` argument list is not an OpenAPI schema. It is the tool parameter map. Do not add a REST endpoint for a search.

---

## User stories and acceptance criteria

### US-1 — Choose a default and a fallback (P0)

An operator wants to say which search provider answers, and which one is the spare, without learning a hidden ranking. A new install searches through DuckDuckGo with no key.

**Independent test**: A config with Tavily usable, fallback `none`, runs Tavily and does not call DuckDuckGo even when Tavily returns a network error. A config with only DuckDuckGo runs DuckDuckGo and does not call anyone else.

**Acceptance scenarios**:

1. **Given** a new install, **When** an agent calls `search_web`, **Then** DuckDuckGo is called and no other provider is called.
2. **Given** Tavily is usable and is the default, and fallback is `none`, **When** Tavily returns results, **Then** the result names Tavily and DuckDuckGo is not called.
3. **Given** the operator selects only Brave and "No fallback", **When** Brave fails with a network error, **Then** no other provider is called and the error names Brave only.
4. **Given** the operator sets the default and the fallback to the same provider, **When** they save, **Then** the save is rejected. **Given** the same pair is already in a hand-edited file, **When** a search runs, **Then** that provider is called at most once.
5. **Given** the default is Tavily and Tavily has no key, and the resolved fallback is a usable DuckDuckGo, **When** a search runs, **Then** Tavily is not called, DuckDuckGo is called, and the result says Tavily was not called because it has no API key.
6. **Given** the fallback key is absent, Tavily and DuckDuckGo are both usable, and the default is Tavily, **When** a search runs and Tavily fails with a network error, **Then** DuckDuckGo is called and the Settings payload marks the fallback automatic.

### US-2 — A failed search names who was tried (P0)

**Independent test**: Force the default into a TLS error and the fallback into a TLS error. The tool error contains both names.

**Acceptance scenarios**:

1. **Given** a usable default and a usable fallback, **When** the default fails with a network error and the fallback returns results, **Then** the tool succeeds, the result names the fallback, and it includes the default's error.
2. **Given** the same pair, **When** both fail with network errors, **Then** the tool fails and the error lists both.
3. **Given** the default returns a well-formed empty result, **When** the search finishes, **Then** the fallback is not called.
4. **Given** the default returns HTTP 401 after its keys are exhausted, and a fallback exists, **When** the search runs, **Then** the fallback is tried and the report still contains the authentication failure.
5. **Given** the agent cancels the turn while the default is in flight, **When** the call returns, **Then** the fallback is not called.

### US-3 — Upgrade does not change who answers (P0)

**Independent test**: Load a config that has no `default_provider`, with Tavily enabled and a resolvable key, and DuckDuckGo enabled. After migration, a Tavily network error does not call DuckDuckGo.

**Acceptance scenarios**:

1. **Given** an old config whose tool constructor would pick Tavily, **When** it is loaded, **Then** `default_provider` is `tavily`, `fallback_provider` is `none`, and both `enabled` flags are unchanged.
2. **Given** that migrated config, **When** Tavily fails with a network error, **Then** the error names Tavily only.
3. **Given** an old config with no provider switched on, **When** it is loaded, **Then** the default is `duckduckgo` and DuckDuckGo is enabled, because that is the final branch.
4. **Given** a config that already has `default_provider`, **When** it is loaded again, **Then** the two role keys are not rewritten.
5. **Given** the founder's shape — Tavily enabled, key reference set, key resolving at the baseline — **When** it is migrated, **Then** the default is `tavily`, the fallback is `none`, and `search_depth` is `advanced`. **Given** the same shape but a key that does not resolve, **When** the gateway boots, **Then** nothing is written and the migration retries on the next boot.

### US-4 — The agent can pick a usable provider, and that pick is honest (P0)

**Acceptance scenarios**:

1. **Given** Tavily and DuckDuckGo are usable and Brave is the default, **When** the agent sets `provider` to `tavily` **with `include_domains`** and Tavily fails with a network error, **Then** DuckDuckGo is not called and the error says the fallback was not tried. **Given** the same pair, **When** the agent sets `provider` to `tavily` on a **plain query** and Tavily fails with a network error, **Then** the fallback is called and the result names both. **Given** Tavily is itself the resolved default, **When** the agent sets `provider` to `tavily`, **Then** behaviour is identical to omitting the argument.
2. **Given** the same pair, **When** the agent sets `provider` to `exa` and Exa is not usable, **Then** nobody is called and the error lists `tavily` and `duckduckgo`.
3. **Given** the agent omits `provider`, **When** the default fails with a network error and a fallback is resolved, **Then** the fallback is tried (US-2).

### US-5 — Depth and site filters are real, and they are not offered when they would be ignored (P1)

**Independent test**: A DuckDuckGo-only tool definition has no `depth` and no domain arguments. A new Tavily call without `depth` sends `basic`. The same call with `depth: high` sends `advanced`. A migrated Tavily object sends `advanced` until the operator changes it.

**Acceptance scenarios**:

1. **Given** a new Tavily config, **When** the agent searches without `depth`, **Then** the body uses `basic`.
2. **Given** a migrated Tavily config, **When** the agent searches without `depth`, **Then** the body uses `advanced`.
3. **Given** `depth: high` and a Tavily default, **When** the search runs, **Then** the body uses `advanced`.
4. **Given** the agent sets `include_domains` and the default is Brave, **When** the search runs, **Then** Brave is not called and the error says the site filter is not supported.
5. **Given** the agent sets `include_domains` and the default is Tavily, **When** the search runs, **Then** the Tavily body contains `include_domains`.

### US-6 — Exa can be the default (P0)

**Acceptance scenarios**:

1. **Given** Exa is implemented, a key resolves, and Exa is the default with fallback `none`, **When** the agent searches, **Then** the request is a POST to the Exa search URL with a Bearer token, and the result names Exa.
2. **Given** Exa is switched on and the key reference is not on `nonChannelRefsFor`, **When** the tool is built, **Then** Exa is not usable and the keyless warning names it. This is the exact failure `07a75c104` fixed for the other five providers; the test pins the enumeration by walking the provider catalogue rather than naming ids.

---

## Behavioral contract

Primary:

- When a new install searches, the system calls DuckDuckGo and names it.
- When the default returns results, the system does not call the fallback.
- When the agent omits `provider` and the default fails with a network, auth, rate-limit, upstream, or bad-response error, and a fallback is resolved and usable, the system calls the fallback once.
- When the agent names a usable provider and uses that provider's own capability, the system calls that provider once and does not hop. When the agent names a usable provider on a plain query, the system hops on a hop-class failure. When the named provider is the resolved default, the system behaves as if the argument had been omitted.
- When native model search is in effect for the active model, `search_web` is not offered to the model at all, and the Settings search group says so.
- When a key is stored through Settings, the system makes it live in the same request, and the response reflects post-reload state.
- When Settings saves a default, the system keeps every other provider's key reference.

Error:

- When both the default and the fallback fail, the system returns one tool error that contains both names and both messages.
- When the default is unusable and there is no usable fallback, the system calls nobody and names the default as not called.
- When the agent names an unusable provider, the system calls nobody and lists the usable ids.
- When the search arguments are invalid, or a site filter or depth is set on a provider that cannot honour it, the system calls nobody.

Boundary:

- When the fallback id equals the default id, the system calls that provider at most once.
- When the fallback key is absent and DuckDuckGo is disabled, the system does not substitute another provider.
- When a search is cancelled, the system does not start the fallback.
- When a provider returns a well-formed empty list, the system does not start the fallback.
- When the agent named the provider and used its own capability, the system does not start the fallback even for a network error.
- When the resolved fallback cannot honour an `include_domains` the agent set, the system does not start it and says why.
- When less than the fallback's own timeout remains of the call budget, the system does not start it and says why.
- When the agent asks for more depth than the operator configured, the system clamps to the operator's value and says so.

---

## Edge cases

| Situation | Expected |
|---|---|
| Default fails over, the next call's default is healthy again | The next call tries the default first. The hop is not remembered |
| Fallback is unusable and the default fails with a network error | One error. The default's network line, and the fallback as "not called" |
| DuckDuckGo returns HTTP 200 and no anchors | `empty`. No hop |
| DuckDuckGo returns a non-200 | `bad_response`. Hop if a fallback exists and `provider` was omitted |
| `count` outside 1–10, or `range` outside `d`/`w`/`m`/`y` | Rejected, as today. No search |
| Baidu and `range: d` | Week window, as `mapBaiduRecencyFilter` already does. No failure |
| Ingest bound trips | That provider fails with the bound error. No hop |
| Two searches at once | Each resolves and fails over on its own. No shared "provider is down" latch |
| `prefer_native` hides `search_web` | **Not an edge case — a condition on the whole feature.** The model never sees `search_web`, so nothing here runs. The Settings group must say so and must name no provider (FR-031). This spec does not change the default and does not fail over native model search |
| Schema was built before a key resolved, and the model omits `provider` | Call-time resolution still uses the now-usable default |
| Schema was built before a key resolved, and the model sends that id anyway | Call-time usable test accepts it if it now resolves. The allowed-values list is a help, not the only check |
| GLM and `depth: low` | Refusal. No request |
| The agent sets `exclude_domains` and the provider that will run cannot honour it | The search proceeds. The result carries a note naming the provider and stating the exclusion was not applied |
| `include_domains` with 11 entries, an empty string, a wildcard, a port, a path, or one 10 KB entry | Rejected by the shared hostname validator before any request |
| The same domain in both lists | Excluded |
| The agent asks `depth: high` while the operator configured `basic` | Clamped to `basic`, with a note |
| The default and the fallback are both slow, so the budget runs out | The fallback is skipped with `no time budget remaining` |
| DuckDuckGo returns HTTP 200 and no anchors repeatedly | Each call is `empty`; a warning fires after a run of consecutive empties (the cheap stand-in for a block-page fingerprint) |

---

## Explicit non-goals

| The system must not | Because |
|---|---|
| Design how credential refs are injected | Another lane. This spec says what "no key" means, and that Exa joins that list |
| Walk the old priority list, including "just in case" | That list is the bug |
| Try more than one fallback | A chain hides failures and multiplies cost |
| Fail over on an empty, well-formed result | Empty is an answer |
| Fall back when the agent named a provider **and used that provider's own capability** (`include_domains`, `depth`, Perplexity's prose) | ADR-096 D6. A plain named query does hop |
| Delete another provider's `api_key_ref` when one is chosen as default | That makes a fallback impossible |
| Add a SearXNG address field or a way to newly enable it from Settings | Descoped by the founder |
| Add another HTML-scrape provider | DuckDuckGo is already the one scrape |
| Change voice integrations, or native model search | Different features |
| Send Tavily `include_answer`, Brave `summary`, or GLM `search_intent: true` | A written answer is out of scope. Each is an extra model call |
| Add `topic`, `media`, `answer`, raw page text, or Exa highlights as tool arguments | Noted, not now, or a non-goal |
| Add an operator domain allow-list or block-list | The founder asked for a tool argument |
| Implement SerpBase, Kagi, You.com, or SerpAPI | Not chosen |
| Put API keys on the tool schema | A secret must not sit in a tool argument |
| Quote Tavily credit prices in the tool description | The rendered definition is byte-capped, and the price was not re-fetched |

---

## BDD scenarios

### Scenario: New install uses DuckDuckGo once

**Category:** Happy Path
**Traces to:** US-1 acceptance 1, AC-2

**Given** a config with shipped search defaults
**When** the agent calls `search_web` with a query
**Then** DuckDuckGo is called once
**And** the result names DuckDuckGo as the default
**And** no other search host is called

### Scenario: An explicit "no fallback" is not used when the default succeeds

**Category:** Happy Path
**Traces to:** US-1 acceptance 2

**Given** the default is Tavily and Tavily is usable
**And** `fallback_provider` is `none`
**When** Tavily returns one or more results
**Then** the result names Tavily
**And** DuckDuckGo is not called

### Scenario: One selected provider has no hop

**Category:** Happy Path
**Traces to:** US-1 acceptance 3, R1, AC-4

**Given** Brave is the only enabled provider
**And** `fallback_provider` is `none`
**When** Brave fails with a network error
**Then** the tool fails
**And** the error names Brave only

### Scenario: An absent fallback uses DuckDuckGo when two providers are usable

**Category:** Alternate Path
**Traces to:** US-1 acceptance 6, R3, AC-3

**Given** the default is Tavily and Tavily is usable
**And** the fallback key is absent
**And** DuckDuckGo is enabled
**When** Tavily fails with a network error and DuckDuckGo returns results
**Then** the result names DuckDuckGo as the fallback
**And** the Settings payload says the fallback is automatic

### Scenario: Save rejects a fallback that is the same provider

**Category:** Error Path
**Traces to:** US-1 acceptance 4

**Given** the operator is saving Settings
**When** they mark Tavily as both default and fallback
**Then** the save is rejected
**And** the stored roles are unchanged

### Scenario: A file that names the same provider twice calls it once

**Category:** Edge Case
**Traces to:** US-1 acceptance 4, R5

**Given** a hand-edited config with default `tavily` and fallback `tavily`
**And** Tavily is usable
**When** a search runs and Tavily returns results
**Then** Tavily is called once
**And** the Settings payload reports `fallback_ignored_reason` `same_as_default`

### Scenario: An unusable default does not pretend to have run

**Category:** Error Path
**Traces to:** US-1 acceptance 5, R6

**Given** the default is Tavily and Tavily's key does not resolve
**And** the resolved fallback is DuckDuckGo and DuckDuckGo is usable
**When** a search runs
**Then** Tavily's host is not called
**And** DuckDuckGo is called
**And** the result says Tavily was not called because it has no API key

### Scenario: Network failure uses the fallback and keeps both stories

**Category:** Alternate Path
**Traces to:** US-2 acceptance 1

**Given** a usable Tavily default and a usable DuckDuckGo fallback
**And** the agent did not pass `provider`
**When** Tavily fails with a TLS error and DuckDuckGo returns results
**Then** the tool succeeds
**And** the result names DuckDuckGo
**And** the result includes Tavily's TLS error

### Scenario: Both providers fail and both are named

**Category:** Error Path
**Traces to:** US-2 acceptance 2

**Given** a usable default and a usable fallback
**When** both fail with network errors
**Then** the tool fails
**And** the error text contains the default's name and message
**And** the error text contains the fallback's name and message

### Scenario Outline: Failover class

**Category:** Edge Case
**Traces to:** US-2 acceptances 3, 4, and 5, D17

**Given** a usable default and a usable fallback
**And** the agent did not pass `provider`
**When** the default ends with `<class>`
**Then** the fallback `<hop>`

| class | hop |
|---|---|
| network | is called |
| auth, after the key pool is exhausted | is called, and the auth failure remains in the report |
| rate_limit | is called once |
| upstream | is called |
| bad_response | is called |
| empty | is not called |
| rejected | is not called |
| cancelled | is not called |
| ingest bound | is not called |

### Scenario: A named provider using its own capability does not fall back

**Category:** Error Path
**Traces to:** US-4 acceptance 1, AC-5, D6

**Given** a usable Tavily and a usable DuckDuckGo fallback
**When** the agent sets `provider` to `tavily` and `include_domains` to `example.com`
**And** Tavily fails with a network error
**Then** DuckDuckGo is not called
**And** the error says the fallback was not tried
**And** the error names DuckDuckGo as a provider the agent can set

### Scenario: A named provider on a plain query does fall back

**Category:** Alternate Path
**Traces to:** US-4 acceptance 1, AC-5, D6

**Given** a usable Tavily, a default of Brave, and a usable DuckDuckGo fallback
**When** the agent sets `provider` to `tavily` with no capability argument
**And** Tavily fails with a network error
**Then** DuckDuckGo is called
**And** the result names DuckDuckGo and includes Tavily's failure

### Scenario: Naming the resolved default is the same as omitting it

**Category:** Edge Case
**Traces to:** US-4 acceptance 1, AC-5, D6

**Given** Tavily is the resolved default and DuckDuckGo is the resolved fallback
**When** the agent sets `provider` to `tavily` and Tavily fails with a network error
**Then** DuckDuckGo is called
**And** the operator's fallback was not disabled by the agent naming the default

### Scenario: An unusable named provider is a refusal

**Category:** Error Path
**Traces to:** US-4 acceptance 2, AC-5

**Given** Tavily and DuckDuckGo are usable
**And** Exa is not usable
**When** the agent sets `provider` to `exa`
**Then** no search host is called
**And** the error lists tavily and duckduckgo
**And** the error does not contain an HTTP status or a vault error

### Scenario: Upgrade keeps today's winner and does not add a hop

**Category:** Happy Path
**Traces to:** US-3 acceptances 1 and 2, AC-10

**Given** an old config with no `default_provider`, Tavily enabled, a resolvable Tavily key, and DuckDuckGo enabled
**When** the config is loaded
**Then** `default_provider` is `tavily`
**And** `fallback_provider` is `none`
**And** a later Tavily network error does not call DuckDuckGo

### Scenario: Upgrade is idempotent

**Category:** Edge Case
**Traces to:** US-3 acceptance 4

**Given** a config that already has `default_provider` `tavily` and `fallback_provider` `brave`
**When** the config is loaded again
**Then** the fallback is still Brave
**And** the default is still Tavily

### Scenario: The final constructor branch stays DuckDuckGo

**Category:** Edge Case
**Traces to:** US-3 acceptance 3

**Given** an old config where every keyed provider is unusable and DuckDuckGo is switched off
**When** the config is loaded
**Then** `default_provider` is `duckduckgo`
**And** DuckDuckGo is switched on
**And** a warning records that the flag was turned on

### Scenario: The founder's Tavily config, with a resolving key

**Category:** Edge Case
**Traces to:** US-3 acceptance 5, D11 row (d), AC-10

**Given** an old config with Tavily enabled, a Tavily key that resolves, no `default_provider`, and no `search_depth`
**When** the config is loaded
**Then** `default_provider` is `tavily`
**And** `fallback_provider` is `none`
**And** `search_depth` is `advanced`
**And** a later Tavily network error does not call DuckDuckGo

### Scenario: An install that already uses SearXNG keeps it, without a new field

**Category:** Edge Case
**Traces to:** AC-9, D10

**Given** an old config with SearXNG enabled, a non-empty base URL, and no earlier provider usable
**When** the config is loaded
**Then** `default_provider` is `searxng`
**And** `fallback_provider` is `none`
**And** no new SearXNG key was added

### Scenario: New Tavily installs are not deep by default

**Category:** Happy Path
**Traces to:** US-5 acceptances 1 and 3, AC-11

**Given** a new install with Tavily as the default
**When** the agent searches without `depth`
**Then** the Tavily body uses `search_depth` `basic`
**When** the agent searches with `depth` `high`
**Then** the Tavily body uses `search_depth` `advanced`

### Scenario: Existing Tavily installs keep deep search until changed

**Category:** Edge Case
**Traces to:** US-5 acceptance 2

**Given** an old config with a `tavily` object and no `search_depth`
**When** it is migrated
**Then** `search_depth` is `advanced`

### Scenario: A site filter on Brave is a refusal

**Category:** Error Path
**Traces to:** US-5 acceptance 4, AC-8, D9

**Given** Brave is the default and Brave is usable
**When** the agent sets `include_domains` to `example.com`
**Then** Brave's host is not called
**And** the error says the site filter is not supported
**And** the error names the usable providers that can honour it

### Scenario: An exclude Brave cannot honour is a note, not a refusal

**Category:** Alternate Path
**Traces to:** AC-8, D9

**Given** Brave is the default and Brave is usable
**When** the agent sets `exclude_domains` to `spam.example`
**Then** Brave is called
**And** the result states that the exclusion was not applied, and by which provider

### Scenario: A fallback that cannot honour an include is not eligible

**Category:** Edge Case
**Traces to:** AC-8, D9

**Given** Tavily is the default and DuckDuckGo is the resolved fallback
**When** the agent sets `include_domains` and Tavily fails with a network error
**Then** DuckDuckGo is not called
**And** the result reports Tavily's failure and `fallback not eligible: cannot honour include_domains`

### Scenario: Tavily receives the site filter

**Category:** Happy Path
**Traces to:** US-5 acceptance 5, AC-8

**Given** Tavily is the default and Tavily is usable
**When** the agent sets `include_domains` to `example.com`
**Then** the Tavily body contains `include_domains` with `example.com`

### Scenario: DuckDuckGo-only definition hides knobs that would be ignored

**Category:** Edge Case
**Traces to:** AC-6, D7

**Given** the only usable provider is DuckDuckGo
**When** the agent's registered tool definition is read
**Then** the arguments are `query`, `count`, and `range`
**And** `depth` is absent
**And** `include_domains` is absent
**And** `provider` is absent
**And** the `Description` is the same constant string as the catalogue instance's

### Scenario: The maximal definition stays inside the size cap

**Category:** Edge Case
**Traces to:** AC-6, D7

**Given** all eight providers are usable and every capability argument is offered
**When** the agent's registered tool definition is serialised
**Then** the rendered definition — description plus parameters — is at most 2,400 bytes

### Scenario: Perplexity returns sources and asks for a stable answer

**Category:** Happy Path
**Traces to:** AC-7, D8

**Given** Perplexity is the default
**When** the agent searches
**Then** the **decoded** request body has `temperature` present and equal to `0`
**And** the result text includes each URL from the response citations
**And** when that array is empty the result says `Sources: none returned`

### Scenario: A keyless enabled Tavily warns while DuckDuckGo is on

**Category:** Error Path
**Traces to:** AC-13, D14

**Given** DuckDuckGo is enabled
**And** Tavily is enabled and its resolved key is empty
**When** the search tool is constructed
**Then** a warning is logged that names Tavily
**And** the warning does not contain the secret

---

## Test-driven development plan

Tests are named for the behaviour. Unit tests do not need a live network: a fake provider returns a class, or an `httptest` server returns a status.

| Order | Test | Level | Traces to |
|---|---|---|---|
| 1 | Resolve: fresh install is DuckDuckGo and fallback `none` | Unit | New-install scenario |
| 2 | Resolve: absent fallback, Tavily default, DuckDuckGo on, yields DuckDuckGo | Unit | Absent-fallback scenario, R3 |
| 3 | Resolve: `none` yields no fallback | Unit | One-provider scenario, R2 |
| 4 | Resolve: same id yields no fallback and the ignored reason | Unit | Same-provider scenario, R5 |
| 5 | Resolve: absent fallback with DuckDuckGo disabled yields no fallback | Unit | R8 |
| 6 | Resolve: unknown default id is unusable and does not scan other providers | Unit | R9 |
| 7 | Call: unusable default, usable fallback, default host sees no request | Unit | Unusable-default scenario, R6 |
| 8 | Call: unusable default, no fallback, zero hosts called, error says not called | Unit | R7 |
| 9 | Call: network then success names both | Unit | Network-hop scenario |
| 10 | Call: network then network lists both in one error | Unit | Both-fail scenario |
| 11 | Call: empty does not call the fallback | Unit | Outline row `empty` |
| 12 | Call: 401 after one key calls the fallback and keeps the auth line | Unit | Outline row `auth` |
| 13 | Call: cancel does not call the fallback | Unit | Outline row `cancelled` |
| 14 | Call: ingest-bound error does not call the fallback | Unit | Outline row |
| 15 | Call: DuckDuckGo non-200 is `bad_response`, not a success string | Unit | DuckDuckGo status rule |
| 16 | Migration: Tavily-enabled old file becomes default Tavily, fallback `none`, flags unchanged | Unit | Upgrade scenario, AC-10 |
| 17 | Migration: second load does not rewrite an explicit fallback | Unit | Idempotent scenario |
| 18 | Migration: all-off old file enables DuckDuckGo and warns | Unit | Final-branch scenario |
| 18b | Migration: SearXNG-enabled old file with a base URL becomes default `searxng`, no new key | Unit | SearXNG-keeps scenario, AC-9 |
| 19 | Migration: old Tavily object gains `search_depth` `advanced`; a brand-new Tavily object is `basic` | Unit | Depth scenarios, AC-11 |
| 20 | Save: same-provider roles return 400 and do not write | Integration | Save-reject scenario |
| 21 | Save: setting a default does not remove another provider's `api_key_ref` | Integration | US-1 |
| 22 | Save: setting a default does not change fallback `none` to DuckDuckGo | Integration | D4 |
| 23 | Save: absent fallback, operator saves without "No fallback", writes `duckduckgo` | Integration | R3 save rule |
| 24 | Domain: Brave with `include_domains` sends no request | Unit | Refusal scenario, AC-8 |
| 25 | Domain: Tavily body contains `include_domains` | Unit | Tavily filter scenario |
| 26 | Settings payload: `usable` is false when the key name is set and `APIKey()` is empty; `active` is not that row | Integration | AC-12 |
| 27 | Chat card **renders for a tool call named `search_web`** and shows the answering provider, its role, and the hop line. A text fixture alone cannot catch the registration defect | Unit (frontend) | US-2, FR-034 |
| 28 | Exa: POST bearer, result names Exa, page text not requested | Unit | US-6 acceptance 1, AC-1 |
| 29 | Exa's reference is in `nonChannelRefsFor` and in `enabledButKeylessSearchProviders` — asserted by walking the provider catalogue, not by naming ids | Unit | US-6 acceptance 2, AC-13, FR-035 |
| 30 | Chosen provider with `include_domains` fails: no fallback. Chosen provider on a plain query fails: fallback called. Chosen provider equals the resolved default: identical to omitting it | Unit | D6 scenarios, AC-5 |
| 31 | Unusable `provider` calls nobody and lists usable ids | Unit | Refusal scenario |
| 32 | Omitting `provider` still hops | Unit | US-4 acceptance 3 |
| 33 | DuckDuckGo-only registered definition has no `depth`, no domains, no `provider`; `Description` is byte-identical to the catalogue instance's; and the **maximal** configuration's rendered definition is ≤ 2,400 bytes | Unit | AC-6 |
| 34 | Two usable providers: `provider` allowed values equal that set | Unit | D7 |
| 35 | The **decoded** Perplexity request body has `temperature == 0`; result includes citations; empty citations say none. (The old "does not contain `0.2`" was a substring check that passed vacuously and tested the absence of a value no code has ever written) | Unit | AC-7 |
| 36 | Tavily body still has `include_answer` false; GLM body still has `search_intent` false | Unit | AC-7 |
| 37 | `depth: high` on Tavily sends `advanced`; omitted depth on a new install sends `basic` | Unit | AC-11 |
| 38 | `depth: low` on GLM sends no request | Unit | Depth table |
| 39 | Migrated Tavily without `search_depth` sends `advanced` | Unit | AC-11 |
| 40 | Search row badge uses the env-key test, not the ref string | Integration | AC-12 |
| 41 | Warning names Tavily when Tavily is enabled, keyless, and DuckDuckGo is enabled | Unit | AC-13 |
| 42 | Migration runs **after** injection in the real boot order: a fixture that boots through `bootCredentials` with a Tavily ref in the vault ends with `default_provider: tavily`, and a fixture whose ref is absent from the vault ends with the migration **deferred** and no keys written. A pre-seeded-environment unit test would pass while the defect shipped, because the defect is *when* the migration runs | Integration | AC-14, FR-030 |
| 43 | A key that appears after the tool was built makes that provider callable without re-registration | Unit | AC-16, FR-032 |
| 44 | One `PUT` that stores a key and sets that provider as the default results in a search calling that provider with no restart, and a row that reads ready. A second `PUT` stores a key without changing any role | Integration | AC-15, FR-033 |
| 45 | Total-coverage resolver test: the cross product of (default usable / unusable / unknown) × (fallback absent / `none` / same / other-usable / other-unusable) × (DuckDuckGo usable / not) maps to exactly one row, and no row calls a third provider | Unit | SC-001 |
| 46 | `exclude_domains` on Brave proceeds with a note; `include_domains` on Brave refuses; a fallback that cannot honour an include is not eligible | Unit | AC-8, FR-026 |
| 47 | Domain-list boundaries: 11 entries, empty string, wildcard, port, path, userinfo, IP literal, 254-character name, 10 KB entry, and the same domain in both lists | Unit | FR-026 |
| 48 | Adding a provider to the single catalogue and nothing else makes it storable, injectable, selectable, warnable and listable — the test walks the catalogue instead of naming ids | Unit | AC-20, FR-035 |
| 49 | A provider error that echoes the request body is redacted and truncated before it reaches the tool result or the log | Unit | AC-19, FR-036 |
| 50 | A fallback is skipped with `no time budget remaining` when less than its provider's timeout is left; `depth` above the operator's ceiling is clamped with a note; each search emits one structured record naming provider, role and depth | Unit | AC-11, AC-19, FR-037 |
| 51 | An oversized SearXNG body is refused by the ingest bound, and SearXNG's client comes from `makeSearchClient` | Unit | AC-9, FR-020 |
| 52 | With native model search in effect, `search_web` is absent from the tool definitions and the Settings payload says the active model searches natively and names no provider | Integration | AC-18, FR-031 |

No end-to-end browser suite is required for P0. Settings is covered by the integration save tests plus one frontend test on the result text.

### Regression

| Behaviour | Where it lives |
|---|---|
| `count` outside 1–10 is rejected, not clamped | `WebSearchTool.Execute` |
| `range` outside `d`/`w`/`m`/`y` is rejected | `normalizeSearchRange` |
| Brave / Tavily / Perplexity try the next key in the pool before giving up | those `Search` methods |
| Voice "Set active" still clears the other voice key ref | `applyVoiceIntegration` |
| `search_web` remains the registered tool name and stays on the default allow list | `defaultToolPoliciesGeneral`, `WebSearchTool.Name` |

The old "first match in the priority list wins" tests, if any encode that list as desired behaviour, are updated to the role table. That list is not a regression to preserve.

---

## Functional requirements

- **FR-001**: The system MUST resolve `search_web`, when `provider` is omitted, to a default and at most one fallback using the R1–R9 table, at call time.
- **FR-002**: The system MUST NOT select a provider by the `NewWebSearchTool` priority chain or by `activeSearchProviderID` once `default_provider` is present.
- **FR-003**: A new install MUST set `duckduckgo.enabled` true, `default_provider` `duckduckgo`, and `fallback_provider` `none`.
- **FR-004**: The system MUST persist `default_provider` and `fallback_provider` even when the fallback is `none`. The fields MUST NOT use `omitempty`.
- **FR-005**: Saving a default MUST NOT delete another search provider's `api_key_ref` and MUST set the chosen provider's `enabled` to true. It MUST NOT change an explicit `none`.
- **FR-006**: The system MUST reject a save that marks one provider as both default and fallback.
- **FR-007**: When `provider` is omitted, the system MUST call the fallback only for the classes marked "Yes" in the failover table, and at most once.
- **FR-008**: A tool error after a hop MUST include both providers' names and messages. A skip MUST say "not called" and the reason. The text MUST NOT name a provider that was neither called nor a role for this call.
- **FR-009**: A well-formed empty result MUST be a success from the provider that returned it, with no hop.
- **FR-010**: DuckDuckGo MUST treat a non-200 HTTP status as `bad_response`, not as an empty success.
- **FR-011**: Migration MUST follow the [Migration](#migration) steps, including the four install rows, and MUST be idempotent once `default_provider` is present.
- **FR-012**: The Settings search group MUST show a default radio and a separate fallback radio including "No fallback", and MUST state that list order is not priority.
- **FR-013**: Removed. SearXNG does not gain a base-URL field. See FR-027.
- **FR-014**: The system MUST NOT ship a default SearXNG base URL, and MUST NOT add a Settings control that sets one.
- **FR-015**: New Tavily configs MUST default `search_depth` to `basic`. Migrated existing Tavily objects without the key MUST get `advanced`. Agent `depth: high` MUST send `advanced` for that call only. Agent `depth: low` on GLM MUST refuse.
- **FR-016**: Removed as written in the first draft (`topic`, `answer`, `media`, operator domain ceiling). Replaced by FR-026.
- **FR-017**: Tavily `include_answer` MUST stay false. It MUST NOT be a tool argument. GLM `search_intent` MUST stay false. Brave MUST NOT gain `summary`.
- **FR-018**: Removed. The description MUST NOT quote a credit price. The cap is FR-024.
- **FR-019**: Voice provider activation and `prefer_native` MUST keep their current behaviour.
- **FR-020**: Every provider body read, including SearXNG, MUST stay inside the existing ingest bound. An ingest-bound failure MUST NOT hop.
- **FR-021**: Exa MUST be implemented as in [Exa](#exa), and MUST be selectable as default or fallback.
- **FR-022**: `provider` MUST be accepted only for a usable id. An unusable id MUST be a refusal that lists the usable ids and MUST NOT call a host.
- **FR-023**: When the agent passed `provider` **and the call used a capability specific to that provider** (`include_domains`, `depth`, or Perplexity's prose answer) and it fails, the system MUST NOT call the fallback, and the error MUST say the fallback was not tried. When the call was a plain query, the system MUST hop on a hop class. When the named id equals the resolved default, the system MUST behave exactly as if `provider` had been omitted.
- **FR-024**: `Parameters` on the tool `registerCoreTools` registers MUST be built from the usable set; `Description` MUST be a constant and MUST be byte-identical to the catalogue instance's. A DuckDuckGo-only definition MUST omit `provider`, `depth`, and the domain arguments. `depth` and the domain arguments MUST be offered only when the **resolved default** honours them. The **rendered definition** (description plus serialised parameters) MUST be at most 2,400 bytes at the maximal configuration, and MUST be stable for the lifetime of an agent instance.
- **FR-025**: Perplexity's result MUST include the citations array, or the words that none were returned. The request MUST send `temperature` `0` and MUST NOT send `0.2`.
- **FR-026**: `include_domains` and `exclude_domains` MUST be optional tool arguments, offered only when the resolved default honours them, validated by `pkg/gateway/video_embed_hosts.go::validVideoEmbedHosts`'s rules with at most 10 entries of at most 253 characters, no wildcards, ports, paths, userinfo, schemes or IP literals, and a domain in both lists excluded. Tavily MUST send snake_case `include_domains` / `exclude_domains`; Exa MUST send camelCase `includeDomains` / `excludeDomains`. A provider that cannot honour `include_domains` MUST refuse before any request and MUST NOT hop; a provider that cannot honour `exclude_domains` MUST proceed and the result MUST state that the exclusion was not applied. A resolved fallback that cannot honour a set `include_domains` MUST NOT be called, and the result MUST say why.
- **FR-027**: The system MUST NOT add a SearXNG settings field. Migration MUST still record `searxng` when that is the tool's winner.
- **FR-028**: The search badge MUST use the tool's usable test. A non-empty key name with an empty `APIKey()` MUST NOT be presented as the provider that runs. A key stored in the same request MUST read ready, because the response is built from post-reload state (FR-033).
- **FR-029**: A keyed provider that is enabled with no resolved key MUST produce a warning that names it, including when DuckDuckGo is enabled. The warning MUST NOT contain the secret. **This already holds at `07a75c104` via `enabledButKeylessSearchProviders`; the work is to verify it, keep its two existing tests green, and add Exa to that enumeration.**
- **FR-030**: Migration MUST decide the winner with `APIKey()` (the environment) and MUST therefore run **after** `credentials.InjectFromConfig` — inside `pkg/gateway/gateway_boot_credentials.go::bootCredentials` and mirrored in `pkg/gateway/gateway_reload.go::executeReload` — and before the agent loop builds its tools. It MUST NOT run inside `config.loadConfigInternal`, where the environment is still empty and every install would be recorded as `duckduckgo`, permanently. It MUST NOT read the credential store during load instead: `pkg/config/config.go::CredentialStore` exposes only `Set`. It MUST defer, writing nothing and retrying on the next boot, whenever any keyed provider is `enabled: true` with a non-empty `api_key_ref` whose key does not resolve. It MUST key idempotency off `tools.web.roles_migrated_at`, not off `default_provider`'s presence. It MUST NOT grow a "hotfix detected" branch. Exa's key reference MUST be on `nonChannelRefsFor`.
- **FR-031**: When `tools.web.prefer_native` is in effect for the active model, the Settings search group MUST state that the active model searches natively and MUST NOT present any provider as the one that answers. FR-003 and AC-2 apply only where native search is not in effect. This spec MUST NOT change `prefer_native`'s default or behaviour.
- **FR-032**: `WebSearchTool` MUST hold a live resolver rather than resolved key strings, so usability can be evaluated at call time. `WebSearchToolOptions`, `WebSearchTool`, `NewWebSearchTool` and `registerCoreTools` all change. The resolver MUST NOT log or return a key value.
- **FR-033**: `PUT /api/v1/integrations/providers/{id}` MUST make its writes live in the same request by routing through `triggerReloadAndWaitOutcome`, which re-runs `InjectFromConfig` and rebuilds the tools. Usability MUST be judged after that reload, never before it. The response MUST be built from post-reload state. A reload failure MUST NOT undo the persisted writes and MUST be reported separately.
- **FR-034**: `search_web` MUST have a production tool-UI registration, and the card MUST render for a tool call named `search_web` — the name the backend emits. The `list_directory` half of issue #898 is out of scope.
- **FR-035**: One `searchProviderCatalogue` MUST be the single source of provider ids, config-section names, credential refs, keyed-or-keyless and capabilities, and every consumer MUST derive from it, including the contract's `id` enum. Adding a provider to it and nothing else MUST make that provider storable, injectable, selectable, warnable and listable.
- **FR-036**: Every provider message included in a tool result or a log MUST pass through the registered sensitive values and MUST be truncated to 300 characters. **Correction (2026-09-28):** a URL appearing in such a message MUST also have any `user:password@` userinfo redacted (`pkg/tools/web.go::redactURLUserinfo`), independent of whether that credential is a registered sensitive value — see [What the tool returns](#what-the-tool-returns).
- **FR-037**: One `search_web` call MUST fit a 45-second wall-clock budget; a fallback that cannot fit MUST be skipped with the stated reason. The operator's configured depth MUST be a ceiling, and an agent `depth` above it MUST be clamped with a note. Every search call MUST emit one structured record naming the resolved default, the resolved fallback, the provider that answered, its role, the depth sent, and the failure class or hop/refusal/skip. A run of consecutive `empty` results from DuckDuckGo MUST produce a warning.

## Success criteria

- **SC-001**: Every combination of (default usable / unusable / unknown) × (fallback absent / `none` / same / other-usable / other-unusable) × (DuckDuckGo usable / not) maps to **exactly one** row of R1–R9, and resolves to that row's default and fallback. Fail if any combination matches zero rows or more than one, or calls a third provider. A per-row fixture alone presumes the uniqueness this criterion exists to prove.
- **SC-002**: A double network failure fixture produces one error string containing both provider ids.
- **SC-003**: An old Tavily-enabled fixture, after one load, still calls only Tavily when Tavily returns a network error. `search_depth` is `advanced`.
- **SC-004**: A Settings save that sets a second provider's key leaves the first provider's `api_key_ref` in the saved JSON.
- **SC-005**: A new Tavily request body contains `"search_depth":"basic"` unless `depth` is `high` or the migrated value is `advanced`.
- **SC-006**: A named-provider network failure **with a capability argument** does not call the fallback; the same failure on a plain query does.
- **SC-007**: A DuckDuckGo-only parameter map has exactly the keys `query`, `count`, and `range`, and the maximal configuration's rendered definition is at most 2,400 bytes.
- **SC-008**: A Perplexity fixture request **decodes** to `temperature == 0` and the rendered result contains a citation URL supplied by the fake response.
- **SC-009**: A fixture that boots through the real credential path with a Tavily ref in the vault ends with `default_provider: tavily`; the same fixture with the ref absent ends with the migration deferred and no keys written.
- **SC-010**: One `PUT` storing a key and setting a default is followed, with no restart, by a search that reaches that provider's host.

## Traceability

| Requirement | Story | Scenarios | Tests | ADR |
|---|---|---|---|---|
| FR-001, FR-002, FR-003 | US-1 | New install, no fallback, one provider, absent fallback | 1–6 | D3, D4, AC-2, AC-3 |
| FR-004, FR-005, FR-006 | US-1 | Save rejects same provider | 20, 21, 22 | D4, AC-3 |
| FR-007, FR-008, FR-009, FR-010 | US-2 | Hop, both fail, outline | 7–15 | D16, D17 |
| FR-011 | US-3 | Upgrade, idempotent, final branch, founder row | 16–19 | D11, AC-10 |
| FR-012, FR-014, FR-027 | — | SearXNG-keeps, settings | 18b, 22, 23 | D10, AC-9 |
| FR-015 | US-5 | Depth | 19, 37, 38, 39 | D12, AC-11 |
| FR-017, FR-025 | — | Perplexity scenario | 35, 36 | D8, AC-7 |
| FR-019 | — | Regression row for voice | existing voice tests | D neutral |
| FR-020 | US-2 | Outline ingest-bound row | 14 | D17 |
| FR-021, FR-030 | US-6 | Exa | 28, 29, 42 | D2, AC-1, AC-14 |
| FR-022, FR-023 | US-4 | Named provider, refusal | 30–32 | D5, D6, AC-5 |
| FR-024 | US-5 | DuckDuckGo-only definition | 33, 34 | D7, AC-6 |
| FR-026 | US-5 | Brave refusal, Tavily filter | 24, 25 | D9, AC-8 |
| FR-028 | — | Badge | 26, 40 | D13, AC-12 |
| FR-029 | — | Warning | 41 | D14, AC-13 |
| FR-031 | — | `prefer_native` condition | 52 | D19, AC-18 |
| FR-032 | US-4 | Key appears after build | 43 | D4a, AC-16 |
| FR-033 | US-1 | Save is live | 44 | D18, AC-15 |
| FR-034 | US-2 | Card renders for `search_web` | 27 | D16, AC-17 |
| FR-035 | US-6 | Catalogue walk | 48 | D15, AC-20 |
| FR-036 | US-2 | Redaction | 49 | D16, AC-19 |
| FR-037 | US-5 | Budget, ceiling, record | 50 | D17a, D20, AC-19 |

---

## Integration boundaries

| System | In | Out | When it fails | During development |
|---|---|---|---|---|
| Tavily `POST /search` | Query, depth, domains | Result list | Classes in the failover table | `httptest` or a fake `SearchProvider` |
| Brave GET search | Query, count, freshness | Result list | Same. Site filter refuses first | Fake |
| DuckDuckGo HTML | Query | HTML | TLS and non-200 hop; 200 with no anchors does not | Fake HTML bodies |
| SearXNG | Query, the existing base URL | JSON results | Same, plus unusable when the URL is empty. No new settings | Fake |
| GLM | As today, plus `content_size` when depth maps | As today | `depth: low` refuses | Fake |
| Perplexity | As today, plus `temperature: 0`, optional context size, optional domain filter | Prose plus citations | Same classes | Fake JSON with a citations array |
| Baidu | As today | As today | Same | Fake |
| Exa `POST /search` | Query, Bearer key, optional domains once confirmed | Title and URL | Same classes | Fake |
| Credential store and environment | A ref name | A key in the environment, or "does not resolve" | Unusable. Not this spec's injector | Fake resolver |
| The gateway credential boot (`bootCredentials`) | The unlocked store and the loaded config | An injected environment, then the one-shot migration | A store fault defers the migration; it does not write a wrong winner | A temporary home directory with a real store and a real config file |
| The Integrations `PUT` and the reload it triggers | A key and a role | A live key, rebuilt tools, a post-reload response | A reload failure keeps the persisted writes and warns | `httptest` against the real handler with a real store |

---

## Evaluation scenarios (holdout)

Not for the implementer's unit suite. For a person checking the finished product.

| # | What you do | What you should see |
|---|---|---|
| H1 | Fresh install, ask an agent to search | A result that says DuckDuckGo. No key was required |
| H2 | Set Tavily as the default, leave "No fallback", search | The result says Tavily. A forced Tavily failure names Tavily only |
| H3 | Move the fallback radio to DuckDuckGo, then fail Tavily only | The result says DuckDuckGo answered and includes Tavily's failure |
| H4 | Ask the agent to use Exa for "pages like this" when Exa has a key | The result names Exa |
| H5 | On a copy of an old config that used Tavily, upgrade and fail Tavily | The error names Tavily, not DuckDuckGo, until you opt in to a fallback |
| H6 | On a copy of the founder's config (Tavily key reference set and resolvable), upgrade | Search uses Tavily. Settings does not say Tavily is active merely because the name is stored, if the key does not resolve |
| H7 | On a fresh install, store a Tavily key in Settings and set Tavily as the default, then search — **without restarting** | The search reaches Tavily, and the row reads ready. This is the journey that was impossible before FR-033 |
| H8 | Point the install at an OpenAI model with `prefer_native` left at its default, then open Settings | The search group says the active model searches natively, and names no provider as the one that answers |
| H9 | Ask an agent to search and look at the chat, not the logs | A search card showing the provider that answered — not a generic JSON badge |

## Assumptions

| Assumption | Basis |
|---|---|
| The credential-injection fix is already in place | It **is** commit `07a75c104`, the evidence baseline. Not an assumption any more. What remains an assumption is that no later change narrows `nonChannelRefsFor` again — test 48 pins it |
| `search_web` is the tool agents call. `web_search` is a legacy card name | `WebSearchTool.Name`. **The runtime provider's comment claims both names are registered; only `web_search` is** — issue #898, closed for search by FR-034 |
| The migration can compute the winner once credentials are injected | `bootCredentials`' documented order. It cannot do so during config load, and it cannot read the vault there (`config.CredentialStore` is `Set`-only) |
| This feature is reachable on the operator's install | Only where native model search is not in effect. `PreferNative` ships `true` and `pkg/agent/loop_run_turn.go` removes `search_web` for OpenAI, Azure and Codex-with-web-search providers (FR-031) |
| Catalogue ids `glm` and `baidu` stay, while config objects stay `glm_search` and `baidu_search` | `pkg/gateway/rest_integrations_auth.go::searchRefSectionByID` (catalogue-derived) |
| The agent's prompt is given the tool `registerCoreTools` registers, not the catalogue instance | `RegisterReplacing` and the catalogue comment that the instance is never executed. A test locks this (test 33) |

## What this task could not establish

| Gap | Consequence |
|---|---|
| GitNexus impact was not run | The impact table is inferred |
| Perplexity's official response key for citations, and Perplexity's own default for temperature | The code does not send temperature. Third-party pages say the default is 0.2. Send 0. Confirm the citations key; do not invent a second one |
| GLM `content_size` other than `medium` (our code) and `high` (one vendor example on 2026-09-26). GLM `search_domain_filter` request shape | `low` refuses. Domain filters on GLM refuse until the page is read |
| Exa's domain-filter JSON names | **Closed in round 1**: `includeDomains` / `excludeDomains`, camelCase. Exa is in the honours-site-filters set. Tool argument names stay |
| Whether Exa has a depth axis | Still open. No depth axis is assumed |
| Tavily credit numbers | Not re-fetched. Do not put them in the description |
| Whether the tool is rebuilt when the credential store unlocks | **Closed**: `registerCoreTools` is reached only through `registerSharedTools`, whose three production call sites are boot (`loop_construct.go`), `ReloadProviderAndConfig` (`loop_config.go`) and the agent-upsert path (`registry.go`). A full reload rebuilds it; nothing rebuilds it on unlock alone, and FR-024 refuses to add that because of prompt-prefix caching |
| A captured body of a 200 block page | 200 with no anchors stays empty |
| An exact token count for the tool definition | Bytes are used as the deterministic proxy (FR-024). Adding a tokenizer for this is not worth it |
| Brave's `extra_snippets` and Baidu's image and video request shapes | Noted, not now. Do not add the parameters from memory |

---

## Definition of done

Code correct and tested: the R1–R9 table, the failover classes, the named-provider hard-fail, the migration fixtures including row (d), the DuckDuckGo-only argument list, and the Perplexity citations fixture are covered by the tests in the plan, and those tests were run.

Reachable by a user or an agent, with three claims that are checked separately and never merged:

1. An operator can set the default and the fallback from Settings → Integrations, in one request, with no restart, on a fresh install — the journey FR-033 exists to make possible.
2. An agent can call `search_web` and see, in the tool result, the name of the provider that actually answered.
3. **A person can see it in the chat.** The card renders for a tool call named `search_web` (FR-034). Before that registration exists, every web search shows the generic JSON badge, so a green test suite on the parser proves nothing about what the user sees.

## Follow-up (out of scope, tracked)

| Item | Where it already lives |
|---|---|
| GLM domain-filter request shape, and a GLM value for depth `low` | Capability matrix. Wire them in a small change after the page is read. Do not guess here |
| Brave `extra_snippets`, result type (news, image, video) | Capability matrix, noted not now |
| A fingerprint for a 200 block page | Failover section |
| Operator domain allow and block lists | Explicitly not this delivery |
| Per-agent default and fallback | Issue #47. Not this spec |
| An operator allow-set for which providers the agent may name, and a per-turn cap on search calls | ADR-096 D20 and its open question Q2. The depth ceiling and the per-call record ship now; the policy surface is a founder decision |
| Whether `prefer_native` should keep its `true` default | ADR-096 Q1 |
| Whether R3's automatic DuckDuckGo fallback is worth keeping, given that nothing but a hand-edit produces an absent `fallback_provider` | ADR-096 Q3 |
| The `list_directory` half of issue #898 | That issue |
| **(2026-09-28, found in spec-sync)** A Settings depth-display row, so an operator who inherited `advanced` from migration can see and change it without a hand-edit | [Settings screen](#settings-screen) Depth row correction. Not shipped: the contract carries no depth field. Flagged for the founder rather than fixed here, because adding it is a contract change |
