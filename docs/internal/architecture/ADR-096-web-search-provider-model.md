# ADR-096 — Web search: a default, a fallback, and an honest tool

- **Status:** Proposed. Revised 2026-09-26 (round 2) against the adversarial review in [ADR-096 review](./ADR-096-web-search-provider-model-review.md). Founder decisions are settled except where the founder explicitly reopened them in the round-2 brief: D6, D7, D9, D11, D13/D15, D16, and the `prefer_native` question now D19. The changelog at the end of this file lists every change and every point still open.
- **Date:** 2026-09-26
- **Deciders:** Daniel Piatkowski (founder, the settled decisions); architect (drafting, plus the calls the brief left open: what a failed agent-chosen provider does, and what an unsupported site filter does).
- **Number check:** no `ADR-096-*.md` under `docs/internal/architecture/` when this file was added. ADR-093 does not exist on this tree; the previous decision record is [ADR-092 — Shell permission modes](./ADR-092-shell-permission-modes.md).
- **Build instructions:** `docs/internal/specs/web-search-provider-model-spec.md`. That spec traces every decision and every acceptance criterion below to a requirement and a test. This file is the why.

## Evidence baseline

Commit `07a75c104`, this worktree's HEAD. Its subject is *"fix(credentials): web-search and voice keys never reached the process"*.

**That commit is the credential-injection fix.** An earlier draft of this ADR said the fix was "landing separately" and treated it as a future precondition. It is the baseline. Two consequences run through the whole document:

| Claim in the earlier draft | Ground truth at `07a75c104` |
|---|---|
| Web-search key references are not injected | They are. `pkg/credentials/inject.go::InjectFromConfig` and `ResolveAll` walk one shared enumeration, `pkg/credentials/inject.go::nonChannelRefsFor`, which covers the five keyed web-search providers alongside voice keys and marketplace tokens. Its doc comment names the drift it exists to stop: *"before the shared helper existed, this loop was missing and a fully configured Tavily sat unused in the vault while search degraded to keyless DuckDuckGo."* |
| The "enabled but keyless" warning cannot fire | It fires. `pkg/tools/web.go::enabledButKeylessSearchProviders` runs unconditionally at the top of `NewWebSearchTool`, **before** the selection chain, for all five keyed providers, and logs `"search provider enabled but no resolved key; selection will skip it"` with provider names, refs and a hint. |
| There are uncommitted edits in `pkg/tools/web.go` and `pkg/credentials/inject.go` belonging to another lane | There are not. The only modified files on this tree are `pkg/tools/web_test.go` (two tests that pin the warning) and an unrelated evidence JSON. |

Every `file::symbol` below was read from this commit, and in round 2 the **content** of each citation was re-read, not only its existence. That is what failed in round 1: the symbols all existed, and two claims about what they contained were wrong.

## Context

`search_web` is built once, with one provider baked in. `pkg/tools/web.go::NewWebSearchTool` walks a fixed list and keeps the first match: Perplexity, Brave, SearXNG, Tavily, DuckDuckGo, Baidu, GLM, and then DuckDuckGo again even when DuckDuckGo is switched off. A later failure ends the search. The error names whichever provider that one object happened to be.

The Settings screen answers "who is active" with a **second** list, `pkg/gateway/rest_integrations_auth.go::activeSearchProviderID`. For the five keyed providers that list does not look at the on/off flag — a non-empty key *name* is enough. (SearXNG is the exception: its branch does check `Enabled` as well as the base URL.) DuckDuckGo sits last, after Baidu and GLM, not where the tool puts it. Choosing a provider there (`applySearchIntegration`) deletes every other search provider's key name, so two providers cannot both stay configured.

That split is how a working Tavily configuration reported "Tavily active" while DuckDuckGo ran, for about two months, with no warning.

**The incident had three causes, not two.** Round 1 found the third, and it changes what migration has to do:

| # | Cause | Where it lives | State at the baseline |
|---|---|---|---|
| 1 | The key never reached the process, so `APIKey()` read empty and the tool skipped Tavily | `pkg/credentials/inject.go::InjectFromConfig` | **Fixed** at `07a75c104` via `nonChannelRefsFor` |
| 2 | Two selection lists that could disagree, and a badge computed from a key-name string | `NewWebSearchTool` vs `activeSearchProviderID` | Open. D15 and D13 |
| 3 | "Set active" in the UI never switches a keyed provider **on** | `pkg/gateway/rest_integrations_auth.go::applySearchIntegration` writes `api_key_ref` and toggles only `searxng.enabled`; `pkg/config/defaults.go::defaultToolsConfig` ships all five keyed providers `Enabled: false` | Open. D4 fixes it forward, D11 fixes it backward |

Cause 3 means "key reference set, provider switched off" is not an exotic hand-edit — it is exactly the state the UI's own button produces. The operator pressed the button, the screen said "Active", and the config said disabled. Any migration that "follows the tool" would read that as an operator choice of DuckDuckGo.

An earlier draft of the spec treated this as "add providers" and left Exa off the done line, forbade the agent from naming a provider, and designed a Settings field for SearXNG. The founder reversed those three. The seven providers are already built. The work is making them usable and choosable, adding Exa, and stopping the tool from quietly doing something other than what was asked.

## Definitions

**Usable** is defined once, here, and every other section refers to this table rather than restating it (round 1 found four drifting copies).

| Provider | Usable when |
|---|---|
| Brave, Tavily, Perplexity, GLM, Baidu, Exa | Switched on, **and** `APIKey()` returns a non-empty string |
| SearXNG | Switched on, **and** the base URL is non-empty after trimming |
| DuckDuckGo | Switched on. Nothing else |

`APIKey()` reads `os.Getenv(<ref>)` (`pkg/config/config_defaults_apply.go::TavilyConfig.APIKey` and the Brave, Perplexity, GLM and Baidu twins). So "usable" is a statement about the **process environment**, which is populated by `credentials.InjectFromConfig`. Two separate consequences follow, and both had to be fixed in round 2: where the migration can run (D11) and what the tool must hold in order to re-check this at call time (D4a).

Two neighbouring tests exist and are **not** this one. Keep them apart:

| Test | Reads | Legitimate use |
|---|---|---|
| Usable (above) | the process environment | Who will actually answer a search |
| `pkg/gateway/rest_config.go::credentialRefResolves` | the credential **store** | Whether a secret is in the vault at all |
| `activeSearchProviderID` | a key-name **string** | Nothing. D15 deletes it |

## Decisions

### D1 — Seven providers already exist. Do not build them again

Perplexity, Brave, SearXNG, Tavily, DuckDuckGo, Baidu, and GLM each already have a provider type in `pkg/tools/web.go`, a config struct on `pkg/config/config.go::WebToolsConfig`, and a shipped on/off flag in `pkg/config/defaults.go::defaultToolsConfig`. The work is selection, honesty, and the gaps below. An earlier draft that proposed building Brave was wrong: Brave is already there (`BraveSearchProvider`).

### D2 — Exa is the eighth provider, and it is in this delivery

There is no Exa type under `pkg/tools` or `pkg/config` (a word search for Exa as its own name returned nothing). Add it. It is a POST of JSON with `Authorization: Bearer`, the same family as Tavily (a JSON search call that returns pages), not the same family as Perplexity (a chat completion). Config mirrors `pkg/config/config.go::TavilyConfig`: on/off, a key reference, a base URL, a max-results count. Default base URL `https://api.exa.ai/search`.

Exa's key reference must join `pkg/credentials/inject.go::nonChannelRefsFor` and `pkg/tools/web.go::enabledButKeylessSearchProviders`, both named explicitly so no lane has to guess which list "the injection list" means. Miss either and Exa reproduces the Tavily failure exactly: configured, never called, no warning.

What it is good for, in the one line the tool is allowed to say: **find pages like this** (semantic search, not a keyword box). That line is the whole reason the agent is told Exa exists.

**Exa honours site filters.** Round 1 verified the field names externally: `includeDomains` and `excludeDomains`, **camelCase**, not Tavily's snake_case. This is a live trap — snake_case keys would be silently ignored and the call would return unfiltered results, which is the class of lie this whole ADR exists to remove. The names are recorded in D9's mapping table.

### D3 — DuckDuckGo stays on, and leaves the priority list

DuckDuckGo is the only provider that needs no key, so it stays enabled on a new install. It is the safety net. It is not a rung on a hidden ranking anymore. The priority chain in `NewWebSearchTool` is deleted. DuckDuckGo runs when it is the default, or when it is the resolved fallback. It does not run because nothing else matched.

### D4 — The operator sets two things: a default and a fallback

Two keys on `tools.web`, always written, never `omitempty`. An explicit "no fallback" has to survive a save.

| Key | Values | Meaning |
|---|---|---|
| `default_provider` | `perplexity`, `brave`, `tavily`, `duckduckgo`, `baidu`, `glm`, `exa`. `searxng` only when an existing file already resolves to it (D10) | Who is tried first |
| `fallback_provider` | one of those ids, `none`, or absent | Who is tried second, if anyone |

Both are plain `string`, JSON `default_provider` / `fallback_provider`, `yaml:"-"`, and **`env:"-"`**. The env tag matters: `WebToolsConfig` embeds `ToolConfig` with `envPrefix:"OMNIPUS_TOOLS_WEB_"` and every sibling scalar carries an `env:` tag, so without `env:"-"` an environment variable could make `default_provider` non-empty and permanently suppress the migration on that install (D11 keys off absence).

The precedent for the write technique is `pkg/config/cli_token_migration.go::migrateCLITokenOutOfUsers`, not `VideoEmbedHosts`. Round 1 caught the mis-citation: `pkg/config/config.go::VideoEmbedHosts` is a `*[]string` **with** `omitempty` — a pointer, the opposite technique, chosen for a three-state problem we do not have. And `migrateCLITokenOnDisk` does not "patch the bytes": it unmarshals into a `map[string]any`, mutates that map, and re-emits with `json.MarshalIndent`. Whitespace, key order and number formatting are re-emitted; what the technique actually preserves is **unmodelled keys**, which is the property we need. Describe it that way.

`none` is a value of the fallback, not a third setting. It means "no second try", and the screen shows it as a real choice labelled "No fallback". Absent (the key is missing) is a different state: the file has not chosen. The catalogue ids are `glm` and `baidu`; the config objects stay `glm_search` and `baidu_search` (`searchRefSectionByID (derived from the provider catalogue)` already maps them that way).

**The rows below are evaluated in order. The first row whose condition holds wins.** Round 1 found that R1, R2 and R3 can all be true at once and nothing said which applied; this sentence is the fix, and it is as load-bearing as any row.

| Case | Condition | Who is called |
|---|---|---|
| R1 | The resolved default is usable, and no fallback resolves (`none`, absent with no automatic fallback, same-as-default, or an id that is not usable) | The default only. No second try. A failure is a failure |
| R2 | `fallback_provider` is `none` | The default only |
| R3 | Fallback key is **absent**, DuckDuckGo is usable, and the default is a usable provider other than DuckDuckGo | The default, then DuckDuckGo on a hop-class failure (D17) |
| R4 | Fallback is a different id, and that provider is usable | The default, then that provider |
| R4b | Fallback is a different, **known** id that is not currently usable | The default only. The result lists the fallback as "not called", with the reason |
| R5 | Fallback names the default | The default, once. The screen says why the fallback was ignored (`same_as_default`). Boot does not fail |
| R6 | The default is not usable, and R3 or R4 produced a usable fallback | The fallback only. The result says the default was not called, and why. DuckDuckGo is not used unless it **is** that fallback |
| R7 | The default is not usable, and there is no usable fallback | Nobody is called. The error names the default and, if a fallback was configured, the fallback, each as "not called" |
| R8 | Fallback key is absent, and DuckDuckGo is not usable | The default only. The rule does not invent Brave, Tavily, or anyone else |
| R9 | The default id is unknown | Treated as not usable (R6 or R7). No walk of the old list |

Three round-1 corrections are baked into that table:

- **R1 is stated in roles, not in a global count.** It used to read "exactly one provider is usable", which fired on the configuration `default_provider: tavily` / key gone / DuckDuckGo on — exactly one provider usable, namely DuckDuckGo — and so called DuckDuckGo **as the provider**. That is verbatim "DuckDuckGo ran because nothing else matched", which D3 abolishes and AC-2 forbids. The same bug made the spec's flagship scenario (an unusable default handing off) unreachable from the table. Fixed.
- **R4b is new.** A fallback that names a real provider which is not currently usable matched no row at all.
- **R3's "two or more providers are usable" clause is gone.** With R1 stated in roles the clause is redundant, and it was the clause that made the R1 bug reachable. R5's dead clause "or R3 would pick the default" is also gone: R3 requires the default not to be DuckDuckGo and always resolves DuckDuckGo, so it can never pick the default.

The screen shows the fallback as its own control (a radio, including "No fallback"), not as a bare key. When R3 applies, that radio shows DuckDuckGo with the label "Automatic fallback". Saving writes the id, so the file stops being "absent". The screen does **not** flip an explicit `none` to DuckDuckGo when the operator changes the default. `none` is a choice. Changing it takes a move of that radio.

Setting a default switches that provider on — this is the forward fix for cause 3. It does not delete any other provider's key reference, and it does not switch the others off. A save that names one provider as both roles is rejected.

A save that names a provider whose key does not resolve is **not** simply rejected any more; see D18, which changes the save path so that a key supplied in the same request is live before usability is judged.

A new install ships `duckduckgo.enabled: true`, `default_provider: duckduckgo`, `fallback_provider: none`, every other search provider off. One role, no fallback, so no hop (R1/R2).

**Hand-typed `searxng`** on a file that never resolved to it: treated as a known id, usable only under the SearXNG row of the Definitions table (switched on with a base URL). Otherwise R6 or R7. It is never rejected at load; a file that cannot boot is worse than a file that reports honestly.

### D4a — What the tool checks usability *against*

Usability is checked at **call** time. A key can appear after unlock. A provider that failed last time is tried again next time. The hop is not remembered.

Round 1 established that this is **not implementable against the current construction**, and the fix is a decision, not an implementation detail. `pkg/agent/loop_wire.go::registerCoreTools` evaluates `APIKey()` once, at construction, and passes resolved plaintexts **by value** (`BraveAPIKeys: braveKeys(rw.cfg.Tools.Web.Brave.APIKey())`, `GLMSearchAPIKey: rw.cfg.Tools.Web.GLMSearch.APIKey()`). `pkg/tools/web.go::WebSearchTool` holds only `{BaseTool; provider SearchProvider; maxResults int}` — no config, no accessor, no closure. There is nothing to re-check against.

**Decision.** The tool holds a **resolver**, not baked key strings. Concretely: `WebSearchToolOptions` gains one function-valued field that answers "which providers are usable right now, and with what key", and `WebSearchTool` keeps that resolver instead of the per-provider plaintext fields. `NewWebSearchTool` builds provider clients from it on demand rather than selecting one provider at construction.

Three consequences to write down rather than discover:

1. This is a change to `WebSearchToolOptions`, `WebSearchTool`, `NewWebSearchTool` and `registerCoreTools`. WP-2 owns it and Affected components lists it. No work package owned it in round 1.
2. The resolved plaintext currently lives inside the tool instance for the life of the process. Under a resolver it is read per call instead. That is a **narrowing** of exposure, not a widening, but the resolver must never log a key value and must never return one in an error; refs are labels, keys are not (the existing warning already gets this right).
3. The capability **snapshot** used to shape the tool's arguments (D7) stays a snapshot. Usability is live; the argument list is not. Those are two different things and the earlier draft blurred them.

The test that proves this is the one D4a actually claims: a key that appears **after** the tool was built makes that provider callable without re-registration.

### D5 — The agent may name a provider, only if it is usable

The call may pass `provider`. The value has to be one the tool currently considers usable. Naming one that is not usable is a refusal. The refusal lists the ids that **are** usable. It is not a silent downgrade, and it is not a raw HTTP error or a vault error.

The argument's allowed values are built from the usable set at registration (D7), so a model that honours the schema cannot name a missing one. A model that sends one anyway gets the same refusal, checked live (D4a).

### D6 — A provider the agent named hard-fails only when the call used that provider's own capability

**This decision is reversed from round 1's blanket rule.** The founder reopened it because its justification was refuted inside this same document.

The old rule was: if the agent names a provider and that call fails, the search stops, always. The stated reason was that hopping "would return a different product and hide the failure". D16 makes hiding impossible — a hop must name the provider that answered, its role, and the default's failure, in fixed text. Under this ADR a hop cannot hide anything, so the reason did not survive contact with the rest of the document. The Alternatives entry then rejected hopping by pointing back at D6, which made the argument circular.

**The rule now:**

| The call the agent made | On a hop-class failure (D17) |
|---|---|
| Named a provider **and** used a capability that is specific to it — `include_domains`, `depth`, or Perplexity's prose answer | **Hard fail.** No hop. A substitute cannot deliver what was asked for |
| Named a provider and used no such capability (a plain query) | **Hop**, with D16's honest report: the fallback answered, the named provider failed, here is its error |
| Named the provider that is already the resolved default | Treated exactly as if `provider` had been omitted. D4's table applies in full |

The last row is the second round-1 correction. The old rule made behaviour depend on whether the model filled in an argument that carried no information: `provider: tavily` when Tavily is already the default silently disabled the operator's configured fallback, while omitting it did not. Models populate optional enum arguments gratuitously, so that was not a rare path — it was a way for an agent to unilaterally turn a resilient search into a single point of failure, with no lever for the operator.

The honest trade-off, recorded instead of an appeal to the incident: hard-failing costs the user a visible failure and the agent a wasted turn; hopping costs one extra request and a result of a different shape. Where the shape was explicitly requested, the failure is the better answer. Where it was not, the answer is.

In every hard-fail case the error names the provider, the failure class, and the message, and tells the agent the two honest next steps: omit `provider` to use the operator's default and fallback, or name another usable provider. The agent chooses. The tool does not choose for it.

### D7 — The argument list is built from who is usable. The description is not

Today `pkg/tools/web.go::WebSearchTool.Description` is a fixed sentence and `WebSearchTool.Parameters` is a fixed object for `query`, `count` and `range`. `Parameters` already interpolates `t.maxResults`, so data-driven parameters have a precedent in that same method.

**Round 2 splits what round 1 lumped together.** The dynamic *enum* earns its keep. The dynamic *description* does not.

| Element | Round 1 | Round 2 | Why |
|---|---|---|---|
| `provider`'s allowed values | built from the usable ids | **unchanged** | A model that honours the schema cannot ask for a provider that is off. Cheap, high value |
| The per-provider "good for" lines | in `Description` | moved into the **`provider` argument's own description**, generated from the same usable set | The enum needs them to be useful; `Description` does not |
| `Description` | built from the usable set | **a constant sentence** | See below |
| `depth`, `include_domains`, `exclude_domains` | offered if **any** usable provider honours them | offered if the **resolved default** honours them | See below |

Why `Description` goes static. It buys nothing that D5's live refusal does not already buy, and it costs three things: a documented staleness bug, a word cap that needs its own test, and a guaranteed divergence between what the agent sees and what the API serves. That last one is concrete: `pkg/tools/general_builtin_catalog.go::GeneralBuiltinMetadata` constructs its instance with `WebSearchToolOptions{DuckDuckGoEnabled: true}` and **has no config in scope at all**, yet that instance is what exposes `Name`/`Description`/`Category` on the central registry, and the comment chain there warns that a tool missing from that catalogue "ships denied-by-default on every install". A constant `Description` is identical in both instances by construction, so the divergence disappears instead of needing a test to police it.

Why the capability arguments follow the **resolved default** rather than "any usable provider". With Tavily and Brave both usable and Brave as the operator's default, the old rule advertised `include_domains` and every use of it refused — and since the schema is unchanged next turn, the model is invited to try again. A permanent per-install turn tax, in a feature whose purpose is to stop the tool doing something other than what was asked. The refusal message still names the usable providers that *can* honour the argument, so the agent can pass one explicitly.

| Rule | Why |
|---|---|
| `provider`'s allowed values are the usable ids | A model that honours the schema cannot ask for a provider that is off |
| With exactly one usable provider, `provider` is omitted | There is nothing to choose |
| `depth` is offered only if the resolved default honours it | Do not advertise a knob whose every use refuses |
| `include_domains` / `exclude_domains` are offered only if the resolved default honours them | Same reason |
| `query`, `count`, and `range` stay | They already work across the providers, via the existing range maps |

**The size cap moves onto the thing that is actually sent, and is measured in bytes.** Round 1 found that the 80-word cap bound only `Description`, while what ships every turn is the whole tool definition — name, description, and a JSON Schema with up to seven properties, each with its own description, plus an eight-value enum. On today's tool the argument descriptions already outweigh the description. Worse, the cap was asserted only where it could not fail: the test measured the DuckDuckGo-only case, which is trivially short.

| Control | Value |
|---|---|
| What is capped | The **rendered tool definition**: description plus the serialised `Parameters` object |
| Unit | **Bytes of the serialised definition.** Not words — the cost is per token, and bytes are the deterministic proxy available without adding a tokenizer dependency (roughly four bytes per token for this text) |
| Cap | 2,400 bytes, about 600 tokens |
| Measured at | The **maximal** configuration: all eight providers usable, every capability argument present. Not the degenerate one |
| On overflow | Drop the per-provider "good for" clauses from the `provider` argument description, keeping the enum. The test fails, so this is a build-time decision, never a runtime surprise |
| Prose guidance, not a control | Each argument description is one short sentence. No credit essays, no provider manuals, no credit prices |

**Prompt caching, which round 1 found missing entirely.** The dominant economics of a per-turn tool definition is not its length but whether it sits in a stable, cacheable prefix. `pkg/agent/loop_run_turn.go::prepareLLMRequest` sends `prompt_cache_key` set to the agent's id, so a definition that changes mid-session invalidates that agent's cached prefix for the whole conversation — a cost far larger than 80 words ever was. Therefore: **the rendered definition must be stable for the lifetime of an agent instance.** Rebuilding it on credential unlock is refused, not merely "not required".

That also settles a gap the earlier draft declared unknowable. `registerCoreTools` is called only from `pkg/agent/loop_wire.go::registerSharedTools`, which has exactly three production call sites: boot (`loop_construct.go`), `AgentLoop.ReloadProviderAndConfig` (`loop_config.go`, reached from `gateway_reload.go`), and the agent-upsert path (`registry.go`). So a full reload **does** rebuild the definition. The staleness that remains is not a tolerable limit — it is the Integrations save path, which is a defect, and D18 closes it.

### D8 — A written answer from a search engine is out of scope, with one exception that is already paid for

Do not turn on Tavily `include_answer`, Brave `summary`, or GLM `search_intent`. Each of those runs that provider's own model and costs extra, and the product does not need a second written answer. Tavily keeps `include_answer: false`. GLM keeps `search_intent: false`. Brave does not gain `summary`. None of these is a tool argument.

**Exception: Perplexity.** `PerplexitySearchProvider.Search` already POSTs to `https://api.perplexity.ai/chat/completions` with `model`, `messages`, and `max_tokens`, and returns `choices[0].message.content`. That call is already a model call. We pay for it today. The response struct keeps only that prose. The `citations` array the founder named is not read, so the agent gets confident prose and no sources. This ADR requires those citations to be returned under the prose, as a source list. That is not a new written answer. It is stopping the discard of something the call already paid for. If the array is empty, the result says so. It does not invent sources.

**Temperature.** The founder asked to drop `temperature: 0.2` on Perplexity, on the grounds that the code sends it. **The committed payload does not send `temperature`.** Third-party API write-ups (retrieved 2026-09-26) give the vendor default as `0.2`. Leaving the field off leaves that default in charge. The request must send `temperature: 0`.

Round 1 corrected the claimed benefit, and the correction stands: setting `temperature: 0` removes **sampler** variance. It does not make a web search repeatable, because the dominant source of run-to-run variation is the retrieved set, not the sampler. Say that, and do not claim more.

The assertion also changes. "Does not send `0.2`" is a substring check over a serialised body: it passes vacuously today, would break if any unrelated numeric field ever contained that substring, and tests the absence of a value no code has ever written. Replace it with a positive assertion on the **decoded** body: `temperature` is present and equal to `0`.

### D9 — Site filters are a real argument. An include is a requirement; an exclude is a preference

One tool-side shape:

| Argument | Shape | Limit |
|---|---|---|
| `include_domains` | list of hostnames | at most 10 |
| `exclude_domains` | list of hostnames | at most 10 |

**Hostname is defined by the same rules, not by a shared function.** Round 1 found "a value that is not a hostname is a rejection" unbuildable and untestable as written. The gateway's `pkg/gateway/video_embed_hosts.go::validVideoEmbedHosts` already defines exactly this shape (bare hostname, normalised, de-duplicated, invalid entries rejected); `pkg/tools` cannot import `pkg/gateway`, so the search tool's site-filter validator (`pkg/tools/web_search.go::normalizeSearchDomains` and its per-entry check) MIRRORS that rule set — same validity, normalisation, per-entry and count caps — and a comment at the validator names the file it mirrors. The rules:

| Question | Answer |
|---|---|
| What is valid | A bare hostname, under the mirrored rule set (rules of `validVideoEmbedHosts`, implemented in `pkg/tools/web_search.go::normalizeSearchDomains`). Lower-cased, trailing dot stripped, duplicates collapsed |
| Rejected outright | Wildcards (`*.example.com`), ports, paths, query strings, userinfo, schemes, IP literals |
| Per-entry length | 253 characters, the DNS name limit |
| Count | 10 per list, as above. Ten entries of unbounded length was an agent-controlled payload with no ceiling |
| A domain in both lists | Excluded. The exclusion wins |

| Provider | How the lists are sent |
|---|---|
| Tavily | `include_domains` and `exclude_domains`, snake_case, the names the founder verified. This writing pass did not re-fetch Tavily's page |
| Exa | **`includeDomains` and `excludeDomains`, camelCase** (verified in round 1). Do not copy Tavily's casing — snake_case keys are ignored and the call returns unfiltered results |
| Perplexity | `search_domain_filter`. A third-party provider page (retrieved 2026-09-26) shows a list of domains, with a leading `-` meaning "exclude". Map includes straight through and excludes with that prefix. If the current official page disagrees, follow the official page; do not drop an exclude to make the call succeed |
| GLM | The field name is `search_domain_filter` (founder, and a vendor example retrieved 2026-09-26). The request shape — one string or a list, and whether exclude exists — was **not** established. Do not invent it. Until the current page is read, a GLM call with either list set is a refusal, not a guess |
| Brave, Baidu, SearXNG, DuckDuckGo | Do not honour site filters |

**The two lists are not the same kind of thing, and round 2 stops treating them as one.**

| Argument | If the provider that would run cannot honour it | Why |
|---|---|---|
| `include_domains` | **Refuse before any request.** The error names the provider, says the filter was not applied, and names the usable providers that can honour it | An include is a *requirement*: the caller restricted the answer to a set of sites, so anything else is off-target. Returning the open web with a note returns the wrong pages, and the model can miss the note |
| `exclude_domains` | **Proceed, and say so.** The result carries a `Note:` line naming the provider and stating that the exclusion was not applied | An exclude is a *preference*: the caller is removing noise. Refusing the whole search returns **zero** pages instead of mostly-right pages — including on every new install, where DuckDuckGo is the only provider |

The same split applies to `depth`: setting it against a provider with no depth axis is a refusal (a depth the caller asked for cannot be approximated), and the argument is not offered in the first place when the resolved default has no depth axis (D7).

**When the default honours the filter and the resolved fallback does not** — the case round 1 found undefined, and the one an explicit fallback makes ordinary:

| Argument the agent set | The hop |
|---|---|
| `include_domains` | The fallback is **not eligible**. The hop is skipped. The result reports the default's failure plus `fallback not eligible: cannot honour include_domains` |
| `exclude_domains` | The hop proceeds, with the `Note:` line above |

**What round 2 removed from this decision.** The old text said: "Do not hop to a provider that could honour the filter either: that would be a silent change of provider (D6)." That cross-reference was wrong. D6 governs providers the **agent named**; when the agent named none, choosing a provider is not a silent change — it is what D4's table does on every call. The citation is deleted, and the alternative it was dismissing is now argued on its own merits in Alternatives.

There is no operator allow-list or block-list of domains in this delivery. The founder asked for a tool argument, not a second policy.

### D10 — SearXNG is descoped, and its two safety gaps are named as such

No new config field, no address box, no "set up SearXNG" work in Settings. Descoped by the founder for this feature. The provider code stays. It is not deleted.

Migration still has to know about it: in today's chain SearXNG sits **above** Tavily, so an install that already resolves to SearXNG keeps SearXNG as its default. The screen may show that id as the current default. It does not gain an editor.

**Two properties of `SearXNGSearchProvider` are security fixes, not housekeeping.** The earlier draft put the ingest bound in scope as a tidy-up, which in a descoped provider is the first thing a lane drops.

| Property | Today | Required |
|---|---|---|
| Response body | Decoded straight off `resp.Body` with `json.NewDecoder`, with **no ingest bound** — the only provider that skips it | Read through the same bound as every other provider. An unbounded response from an operator-set host can exhaust memory |
| HTTP client | When no SSRF checker is present it constructs its own `&http.Client{Timeout: 10 * time.Second}`, bypassing the SSRF-safe path the others get from `makeSearchClient` via `ssrf.SafeClient()` | Use `makeSearchClient` like every other provider. The base URL is operator-controlled and the request is server-side |

Both carry an acceptance criterion and a test. Neither is optional because the provider is descoped.

**Amendment, 2026-09-29 (founder, via #1056 F-2/squad `websearch-1055-1056`): reversed — SearXNG is fully removed, not merely descoped.** The founder's original "the provider code stays, it is not deleted" instruction above is superseded: the catalogue entry, its `WebToolsConfig.SearXNG` field, its settings-surface exposure, and its documentation are deleted outright, and any persisted `tools.web.searxng` block in an existing install's `config.json` is dropped on load (silently — `pkg/config`'s loader has no `DisallowUnknownFields`, so an unrecognised key is simply ignored once the Go field no longer exists; no new migration marker is introduced for this). The one open risk this reversal creates against D11's own migration (below) — whether any not-yet-migrated install with SearXNG enabled can still be awarded SearXNG's legacy chain position 3 once the typed field is gone — is tracked and resolved in the implementation (`pkg/config` roles-migration code, raw-JSON read if needed instead of new alias/marker machinery), not reopened here as a design question.

### D11 — Migration must not silently change who answers, or what it costs — and it must run where it can tell

**The trigger.** Runs once, when `default_provider` is absent on disk. Writes both keys plus a marker. Does not run again once the marker is present, so a later hand-edit that removes only the fallback key is the unset state in R3, not a second migration. Does not change an `enabled` flag except in the two cases named below. Does not delete a key reference.

**Where it runs — this is the round-2 change, and it is the one that would have corrupted data.**

The migration must compute the winner with the usable test, which reads the process environment. The boot order is fixed by ADR-004 and pinned by `boot_order_test.go`; `pkg/gateway/gateway_boot_credentials.go::bootCredentials` documents it:

```
1. NewStore → Unlock
2. LoadConfigWithStore        ← every load-time migration runs here
3. EditionMisbuild tripwire
4. InjectFromConfig           ← os.Setenv happens here, for the first time
5. ResolveBundle
6. RegisterSensitiveValues
```

`migrateCLITokenOutOfUsers` — the sibling the earlier draft said to sit beside — is called from `loadConfigInternal`, i.e. step 2. On the **first load of the process**, which is the upgrade load, every `APIKey()` therefore returns `""`. The winner would always be the final DuckDuckGo branch, for every install, including one with a perfectly resolvable Tavily key in the vault — and because the migration is idempotent on its own output, `default_provider: duckduckgo` would be written to disk permanently, for 100% of upgrades, with no self-correction. The feature's headline safety property would have been violated by construction on every install that has it.

Reading the credential store instead is **not available at step 2**: the interface threaded into `LoadConfigWithStore` is `pkg/config/config.go::CredentialStore`, and it has exactly one method, `Set(name, value string) error`. It cannot read. Widening a write-only interface across every implementer, to answer a question the environment answers ten lines later, is the wrong trade.

**Decision.** The migration is **not** a config-load migration. It runs in the gateway's credential boot, **after step 4 and before the agent loop builds its tools**, inside `bootCredentials` (which already holds both the config path and the loaded config), and it is mirrored in `pkg/gateway/gateway_reload.go::executeReload` immediately after that function's own `InjectFromConfig` call. Consequences to state plainly:

- It is no longer "beside `migrateCLITokenOutOfUsers`". It borrows that function's *write technique* only.
- At boot the config-file watcher does not exist yet, so the write needs no suppression. On the reload path it does: register the written bytes with the `configSelfWriteRegistry` that `executeReload`'s services already carry, exactly as `safeUpdateConfigJSON` does, or the watcher will reload on our own write.
- A CLI invocation that loads config without the gateway's credential boot does not migrate. That is correct: it also does not inject, so it could not tell.

**Do not migrate on an ambiguous install.** If any keyed provider is `enabled: true` with a non-empty `api_key_ref` whose key does not resolve, leave `default_provider` absent, log it, and retry on the next boot. The earlier draft dismissed this with a non-sequitur — "there is no runtime detector that distinguishes 'hotfix not landed' from 'the key is really missing'". Distinguishing them is unnecessary: in **both** cases recording `duckduckgo` is wrong for that operator, and deferring is safe because the pre-migration path still works. The state is the same one `enabledButKeylessSearchProviders` already detects and warns about, so the detector exists and is already wired.

**The marker.** Write `tools.web.roles_migrated_at` (an RFC 3339 timestamp) and key idempotency off it, not off `default_provider`'s presence. Reason: without a marker, no later corrective migration can tell whether this one ran, or with what predicate — and the usual escape hatch does not exist here. `CurrentVersion` has been `1` since the beginning, the load switch accepts only `CurrentVersion` and hard-fails everything else with no `case 0` branch, and the code comments state that a config predating the current schema "has no migration path". A wrong-but-valid `default_provider` would be permanent and undetectable at the population level. The marker is the repair path.

**The steps.**

1. Compute the winner with the usable test (Definitions), i.e. the provider `NewWebSearchTool` would construct at this moment, including the final DuckDuckGo branch that runs even when `duckduckgo.enabled` is false.
2. Write `default_provider` to that winner's catalogue id (`glm_search` → `glm`; `baidu_search` → `baidu`).
3. Write `fallback_provider: none`.
4. Write `tools.web.roles_migrated_at`.
5. Depth defaults: if a `tavily` object exists and `search_depth` is absent, write `advanced` (see the cost note below). If a `glm_search` object exists and `content_size` is absent, write `medium`, which is the value the code hardcodes today. Do not write `search_context_size` onto Perplexity — the field is not sent today and writing one would change the bill.
6. If the winner is the final DuckDuckGo branch and `duckduckgo.enabled` is false, switch it on and log that it did so. Matching that branch is not a silent change of provider.
7. **If a keyed provider is `enabled: false` with a key reference that resolves, treat that as the operator having chosen it**: set `default_provider` to it, switch it on, and log that the flag was corrected. This is cause 3, fixed backward. `applySearchIntegration` never set `enabled: true` for a keyed provider, so "ref set, switched off, key resolves" is what the UI's own "Set active" button produced for Tavily, Brave, Perplexity, GLM and Baidu. The operator pressed the button and the screen said "Active". Reading that as a choice of DuckDuckGo would cement a UI bug as operator intent. Where more than one keyed provider is in this state, the old chain order breaks the tie, and the log says which and why.

| Install | Who runs a successful search today | After this migration, before anyone touches Settings |
|---|---|---|
| (a) Nothing configured. DuckDuckGo on, no keyed provider usable | DuckDuckGo | Default `duckduckgo`, fallback `none`. DuckDuckGo. No hop |
| (b) DuckDuckGo only | DuckDuckGo | Same as (a) |
| (c) Tavily switched on and the key resolves | Tavily. A failure is the end | Default `tavily`, fallback `none`. A Tavily failure still does not call DuckDuckGo. Depth stays `advanced` |
| (d) Tavily switched on, key reference set, key resolves at the baseline. **This is the founder's live instance** | Tavily, at `advanced` depth | Default `tavily`, fallback `none`. No hop added. Depth stays `advanced`. The screen stops calling Tavily active merely because the name is set (D13) |
| (e) Tavily key reference set and resolvable, Tavily switched **off** (the "Set active" shape) | DuckDuckGo | Default `tavily`, switched on, logged as a corrected flag (step 7) |

**The cost note on row (d), stated honestly.** Round 1 argued that writing `advanced` moves a free install to Tavily's most expensive depth. That is not right, and the reason matters: `TavilySearchProvider.Search` **hardcodes** `search_depth: "advanced"` on every call today. Once the key resolves, that install pays `advanced` whether or not this migration runs. Writing `advanced` preserves behaviour exactly; writing `basic` would be this feature silently cheapening a search the operator never asked us to cheapen. What round 1 was right about is that the earlier draft's Positive section claimed row (d) gets no cost change, and it does get one — from the key starting to work. So: keep `advanced`, correct the claim (see Consequences), log the depth written with its reason, and make sure the Settings screen shows the inherited depth so it is one click from `basic`.

**Precondition, restated.** The earlier draft read as a future release gate: "this feature ships after the injection fix, or it does not ship." The injection fix **is** the evidence baseline. The precondition is met. What replaces the gate is the ambiguous-install guard above, which is a runtime rule rather than a release rule, and which protects the same operators for a better reason.

### D12 — Depth is how hard the provider searches, and it is configurable because it changes the bill

Depth is not a written answer (D8). `TavilySearchProvider.Search` sends `search_depth: "advanced"` on every call. The founder names four Tavily levels — `basic`, `advanced`, `fast`, `ultra-fast` — with `advanced` the most expensive; round 1 verified this externally and it is settled. Perplexity has `web_search_options.search_context_size` (`low`, `medium`, `high`; not sent today). GLM sends `content_size: "medium"`, and a vendor example also shows `high`. No other GLM value was established.

The operator has a default, in the provider's own vocabulary. The agent has one override, `depth`: `low`, `medium`, or `high`. One argument, three words, so the per-turn schema stays small. Omitted means "use the operator default".

| Agent `depth` | Tavily `search_depth` | Perplexity `search_context_size` | GLM `content_size` |
|---|---|---|---|
| `low` | `fast` | `low` | Not mapped. The call is refused. Do not invent a GLM value |
| `medium` | `basic` | `medium` | `medium` |
| `high` | `advanced` | `high` | `high` (seen in a vendor example, 2026-09-26) |

`ultra-fast` is operator-only. It is not worth a fourth word in every prompt. If the operator set it and the agent omits `depth`, the call sends `ultra-fast`.

| Provider | Shipped default | Existing install with the key absent |
|---|---|---|
| Tavily | `basic` | `advanced`, so today's bill does not change (D11) |
| Perplexity | do not send the field | do not write one |
| GLM | `medium` | `medium` |

Brave, Baidu, SearXNG, DuckDuckGo and Exa have no depth axis in this delivery. If the agent sets `depth` and the call would use one of them, the call is refused (D9). If the resolved default has no depth axis, the argument is not offered at all (D7).

**The agent may ask for less depth, never more.** See D20 — the operator's configured value is a ceiling, not just a default.

### D13 — The screen reports the resolved state, not the key name

`activeSearchProviderID` treats a non-empty `api_key_ref` as "active". It does not look at `enabled`, and it does not ask whether the key resolved. A sibling check, `integrationConfigured`, asks the credential store via `credentialRefResolves` whether the secret exists. That is a third test again: the tool uses the environment, "configured" uses the store, and "active" uses the string. They disagreed, and the badge showed the string.

Search rows stop using "Active". They show **Default** and **Fallback**, and they show ready-or-not using the Definitions test. A key reference whose `APIKey()` is empty reads "key not reaching search" — not "active" and not "ready". Voice rows keep today's "Active" behaviour.

`configured` may keep meaning "the secret is in the vault". It must not be the badge that says who runs.

**One thing D13 must not do on its own.** Round 1 found that switching the badge to the environment test, without D18, replaces a false positive with a confusing false negative: a key the operator just saved successfully would read "key not reaching search" until a restart. D13 ships **with** D18 or not at all. They are one change to the operator's experience even though they are two decisions.

**And it must say when none of this is in use.** See D19: when native model search is in effect for the active model, the search group must say so and must not present any provider as the one that answers.

### D14 — The "enabled but unusable" warning: already shipped, verify and extend

At `07a75c104` this is **done**. `enabledButKeylessSearchProviders` runs before the selection chain, for all five keyed providers, and fires while DuckDuckGo is on. Two tests already sit on this tree pinning that behaviour (`TestNewWebSearchTool_EnabledButKeyless_WarnsRegardlessOfSelection`, `TestNewWebSearchTool_EnabledWithKey_NoKeylessWarn`).

The earlier draft described the warning as unreachable and dispatched a work package to move it. That was the round-1 evidence error: a lane would have "fixed" working code and regressed those tests.

What is left is one line of real work: **Exa must join `enabledButKeylessSearchProviders`** when it joins `nonChannelRefsFor` (D2). Verify the existing behaviour; do not rebuild it.

### D15 — One resolver, used by the tool and by the screen

After `default_provider` is present, neither `NewWebSearchTool`'s chain nor `activeSearchProviderID` selects a provider. Both call D4's table. Two lists that can disagree are the bug.

**One resolver is necessary and not sufficient, and round 2 says so.** The incident had three causes (Context). The two lists were cause 2. Cause 1 is fixed at the baseline. Cause 3 — that the write path never makes a saved key live — is untouched by D15 and is closed by D18. A reviewer reading D15 alone would conclude the headline defect is closed. It is not.

**And the id enumeration needs the same principle applied to it.** D15's rule is "two lists that can disagree are the bug", and the earlier draft applied it only to the two selection lists. The *id* enumeration is duplicated across at least nine places verified on this tree: `integrationCatalogue`, `searchRefKeyByID` (which omits `duckduckgo` and `searxng`), `activeSearchProviderID`'s switch, `nonChannelRefsFor`, `NewWebSearchTool`'s chain, `enabledButKeylessSearchProviders`, `defaultToolsConfig`, `WebToolsConfig`'s fields, and the contract's `id` — which is a free-form `type: string` in `contracts/components/schemas/IntegrationProvider.yaml`, not an enum. There is no shared constant; the gateway catalogue's own comment says it "mirrors the providers wired in pkg/tools/web.go and pkg/voice". **The commit this ADR is baselined on exists because two of those lists drifted.**

So WP-1 delivers **one** `searchProviderCatalogue` — id, config-section name, credential ref, keyed or keyless, capabilities — and every consumer derives from it, including the contract's `id` enum. The acceptance criterion is behavioural, not structural: adding a provider to the catalogue and changing nothing else makes it storable, injectable, selectable, warnable and listable, proven by a test that walks the catalogue instead of naming ids. Without this, Exa is a tenth hand-maintained list and a re-run of the Tavily failure is a matter of time.

### D16 — What the tool says when more than one provider was involved

The tool result stays text. `WebSearchTool.Execute` returns the same string to the agent and to the user today. Both keep seeing the same lines.

A success names the provider that answered, and whether it was the default, the fallback, or the agent's choice. A hop also includes the default's failure. A skip says "not called" and the reason. A double failure is one tool error listing both. A result must not name a provider that was neither called nor configured as a role for this call. An empty, well-formed result is a success from the provider that returned it.

Reason vocabulary for a "not called" line: `no API key`, `switched off`, `unknown id`, `same as default`, `cannot honour include_domains`, `no time budget remaining` (D17a). One of those, never free text.

Two things must be true of every provider message that reaches this text:

| Rule | Why |
|---|---|
| It passes through the registered-sensitive-values redactor before it is returned or logged | `TavilySearchProvider.Search` sends `"api_key": apiKey` in the **JSON request body**. An upstream 4xx from a provider, proxy or WAF that echoes the submitted payload therefore has a path into the model's context and the transcript. `cfg.RegisterSensitiveValues` (boot step 6) already holds every resolved plaintext; this is invoking an existing mitigation, not building one |
| It is truncated to 300 characters | An unbounded upstream string in a per-turn result is both a cost and a leak surface |

**The chat card — corrected.** The earlier draft said "the live tool name is `search_web`; the card also matches the legacy name `web_search`". That is inverted, and the difference is the whole user-visible half of this decision.

| Fact | Evidence |
|---|---|
| The live tool's name is `search_web` | `pkg/tools/web.go::WebSearchTool.Name` |
| The only production registration in the SPA is `web_search` | `src/components/chat/tools/WebSearchResult.tsx` registers `toolName: 'web_search'`, and its visibility gate calls `shouldRenderToolCall('web_search', …)` |
| `search_web` has no production registration | A repository-wide search for a production `toolName: 'search_web'` returns only test files |
| The intent was both | `src/components/chat/OmnipusRuntimeProvider.tsx` documents `search_web → WebSearchResultUI (canonical)` and `web_search → WebSearchResultUI (legacy alias)` in a comment, but mounts one component |

This is issue [#898](https://github.com/elicify-ai/omnipus/issues/898), which covers the same defect for `list_directory` / `list_dir`. **Every web search today renders as the generic JSON badge.** So the deliverable most likely to be demoed — "the operator can see who answered" — would be built, tested against a text fixture, and invisible in the product.

Therefore: `search_web` gains a production tool-UI registration in `OmnipusRuntimeProvider.tsx` and `WebSearchResult.tsx`, and the test asserts that the card renders for a tool call **named `search_web`** — the name the backend emits — not only that the parser handles a fixture. This ADR claims the search half of #898; the `list_directory` half stays on that issue.

Note also that `WebSearchResult.tsx` already declares `args.provider` and never renders it, so the card cannot show a provider today even when it does fire. It reads those facts from the result text and does not hardcode a provider name.

### D17 — Which failures hop

One extra try, then stop. Key rotation inside Brave, Tavily and Perplexity stays inside that provider; the hop happens only after that pool is exhausted. GLM, Baidu and Exa have one key; a 401 on it is `auth`.

| Class | Examples | Hop? |
|---|---|---|
| `network` | DNS, timeout, connection reset, TLS failure | Yes |
| `auth` | HTTP 401 or 403 after the key pool is exhausted | Yes. The auth line stays in the report |
| `rate_limit` | HTTP 429 after the pool is exhausted | Yes, once. No sleep, no third try |
| `upstream` | HTTP 5xx, or HTTP 408 | Yes |
| `bad_response` | Body is not the documented JSON, or DuckDuckGo's HTTP status is not 200 | Yes |
| `empty` | HTTP 200 and a well-formed body with zero results | **No** |
| `rejected` | HTTP 400 or 422, or the arguments are invalid (including D9's `include_domains` refusal) | **No** |
| `cancelled` | The turn was cancelled | **No** |
| `unusable` | Not called | Not a call. R6 or R7 |
| Ingest bound | The existing response-size bound refuses the body | **No.** A second provider must not be a way around the bound |

DuckDuckGo today does not check the HTTP status (`DuckDuckGoSearchProvider.Search` reads the body and `extractResults` returns a success string when it finds no anchors). A non-200 becomes `bad_response`. A 200 with no anchors stays `empty`, including a block page that returns 200 and no anchors. This ADR does not invent a fingerprint for that page — but it does not leave the failure invisible either; see D20's observability requirement, which counts consecutive empties.

D9's `include_domains` refusal is `rejected` and D6's hard-fail is a named failure. Neither hops.

### D17a — One search call has a wall-clock budget

Neither document stated a timeout for a search, a budget for the fallback attempt, or that the pair fits inside the turn. The existing values are `searchTimeout = 10s` (Brave, Tavily, DuckDuckGo, GLM, the final DuckDuckGo) and `perplexityTimeout = 30s` (Perplexity, Baidu), and the key-pool loop retries **per key** inside a provider before the hop even starts. A Perplexity default plus a DuckDuckGo fallback is a 40-second worst case inside one tool call, more with multiple keys.

| Control | Value |
|---|---|
| Total budget for one `search_web` call | 45 seconds of wall clock |
| The default attempt | Keeps its provider timeout, bounded by the remaining budget |
| The fallback attempt | Gets the remaining budget. If less than its provider's own timeout remains, it is **skipped** with the reason `no time budget remaining` (D16's vocabulary) |

Without this, the feature's headline behaviour is dead on the slowest providers — silently, and only under failure conditions nobody reproduces locally.

### D18 — A save in Settings must be live before it is reported

**This decision is new in round 2. Without it, D13 and D15 do not close the reported defect, and the feature's primary journey cannot be completed through the UI.**

The Integrations save path writes the vault and the config file and makes neither of them live in the running process. Verified end to end:

| Step in the operator's journey | What actually happens |
|---|---|
| "Save & activate" → `PUT /api/v1/integrations/providers/{id}` | `handleIntegrationProviderUpdate` → `storeCredential` → `safeUpdateConfigJSON` |
| `pkg/gateway/rest_config.go::storeCredential` | `store.Set(refName, apiKey)` and nothing else. **No `os.Setenv`** |
| `safeUpdateConfigJSON` → `pkg/gateway/rest_config.go::refreshConfigAndRewireServices` | Reloads config, re-resolves the secret bundle, re-arms redaction, `SwapConfig`. **No `InjectFromConfig`. No tool rebuild** |
| The file watcher that would trigger a full reload | Deliberately suppressed — the written bytes are registered with `configSelfWriteRegistry` precisely so the watcher does not reload |
| `GET /api/v1/integrations/providers` afterwards | `configured: true`, and the config holds the ref |
| `os.Getenv(ref)` in this process | Still empty |
| The live `WebSearchTool` | Still the instance built at boot, from the old config |

So after a successful save the response is truthful about config and false about the running tool — **the same class of gap as the incident, one layer out.** `07a75c104` fixed *which* refs `InjectFromConfig` enumerates, not *when* it runs.

The repository already solved this, in the neighbouring endpoint. `pkg/gateway/rest_settings.go`'s credential handler calls `triggerReloadAndWaitOutcome` after its `store.Set`, with a comment that spells out the reason and cites the UAT finding it came from: a credential value only becomes live once a reload's `InjectFromConfig` step re-runs `os.Setenv`, and without it "the OLD value stays live in-process … a stale-credential window that was previously invisible to the caller". The Integrations path does not do this.

**Decision.** The Integrations `PUT` routes through the same mechanism: after the config write succeeds, call `triggerReloadAndWaitOutcome`, which runs `executeReload` — `InjectFromConfig` **and** `ReloadProviderAndConfig`, which rebuilds the tools through `registerSharedTools`. Reuse, not a new path. Following the existing pattern, a reload failure does not undo the writes: the response still reports what was persisted and warns separately that it is not yet live.

Two rules follow, and they are what make the operator's journey possible:

1. **Usability is judged after the reload, not before it.** The save-time rule "a default pointing at a provider that is not usable yet is rejected" was unsatisfiable as written: `APIKey()` is empty at save time and stays empty, so setting Tavily as the default was impossible through the UI on a fresh install — the exact journey this feature exists to enable. The order is now: store the key, write the config, make it live, then evaluate usability. Reject only if the key still does not resolve afterwards, and say which step failed.
2. **The response is built from post-reload state.** Otherwise D13's badge reads "key not reaching search" for a key that was just saved successfully.

**The request shape has to grow, and by more than one field.** `contracts/components/schemas/IntegrationProviderUpdateRequest.yaml` has `additionalProperties: false` and exactly three properties — `kind`, `api_key`, `active` — and the handler computes `makeActive := body.Active != nil && *body.Active`, so `active: false` is accepted and silently does nothing. The SPA offers only "Set active" and "Save & activate", so there is no way to store a key without activating, no way to deactivate, and no `base_url` field.

| Required | Why |
|---|---|
| Storing a key must be separable from assigning a role | The save-rule table assumes the operator can add a key and separately choose a default |
| `active: false` gets a defined meaning, or is rejected | Silently accepting a no-op is how a UI comes to lie |
| A `fallback` boolean | "No fallback" has to be expressible |
| No `base_url` field | SearXNG stays descoped (D10) |

All of these are breaking changes under `additionalProperties: false`, so they follow the five contract steps, and the regenerated `pkg/gateway/inboundschemas/` copy is part of the change — the runtime validator reads the embedded YAML, so an un-regenerated schema edit validates against stale rules and the drift is invisible.

### D19 — When this feature applies at all

**This decision is new in round 2, promoted out of "Neutral" at the founder's instruction. It is the largest "who actually searches" switch in the product, and this ADR exists to make that question answerable honestly.**

`pkg/config/defaults.go::defaultToolsConfig` ships `PreferNative: true` — **verified, and confirmed true on the founder's live instance.** Its own doc comment says that when the active LLM supports native search "the client-side web_search tool is **hidden** to avoid duplicate search surfaces, and the provider's built-in search is used instead". `pkg/agent/loop_run_turn.go` implements exactly that: when `PreferNative` is set and the active provider reports `SupportsNativeSearch()`, it **filters `search_web` out of the tool definitions entirely** and sets `native_search: true` on the request instead.

Which installs that covers, precisely — this is narrower than "every install", and the difference matters:

| Active provider | Native search | Effect |
|---|---|---|
| OpenAI-compatible against `api.openai.com` or `*.openai.azure.com` (`pkg/providers/openai_compat/provider.go::isNativeSearchHost`) | Yes | `search_web` is not offered. **None of this feature is reachable** |
| The Codex provider with web search enabled (`pkg/providers/codex_provider.go::SupportsNativeSearch`) | Yes | Same |
| Every other provider, including OpenRouter | No | The feature applies in full |

So on a default install pointed at OpenAI or Azure, `search_web` does not exist for the model, the whole R1–R9 table is unreachable, and a Settings screen that says "Default: DuckDuckGo" is confidently naming a provider that is not being used.

**Decisions:**

1. **AC-2 is conditional and says so.** "A new install calls DuckDuckGo and nobody else" is true only where native search is not in effect. Stated unconditionally it is simply false on those installs.
2. **The Settings search group must say when it is not in use.** When `prefer_native` is in effect for the active model, the group shows that the active model searches natively and these settings are not currently deciding who answers. It must not present any provider as the one that answers. This is part of D13's honesty requirement, not a nicety.
3. **This ADR does not change `prefer_native`'s default or its behaviour.** Whether a keyless DuckDuckGo default should beat a provider's native search is a separate decision about search quality and cost, and it is listed as an open question for the founder rather than settled here.
4. **The result text still names the provider when the client-side tool does run**, so an operator can always tell which of the two paths answered.

### D20 — The agent may spend less than the operator allowed, never more, and every search is on the record

**This decision is new in round 2.** D5 and D12 together hand the model two powers it does not have today, and the earlier draft put no control on either: choose **which third party** receives the query and spends the operator's key, and choose **how hard** that provider searches. Today both are fixed by config, so neither is reachable from a prompt. `search_web` is `allow` by default in `pkg/config/defaults.go::defaultToolPoliciesGeneral` and is granted to the built-in roles, so a prompt-injected or merely looping agent could route every search to the most expensive configured provider at maximum depth — and the operator's one lever, the default, is the lever the agent bypasses.

| Control | Rule | Cost of adding it |
|---|---|---|
| Depth ceiling | The operator's configured depth for a provider is a **ceiling**, not just a default. `depth` may ask for less and is clamped, with a note in the result, if it asks for more | No new config key. A rule on values that already exist |
| Per-call record | One structured log record per search call carrying: resolved default, resolved fallback, provider that answered, role (`default` / `fallback` / `chosen`), depth sent, failure class, and whether a hop, refusal or skip occurred. Carries NO request-correlation fields (no session/turn/request id): `pkg/logger` has no context-aware variant, so the record cannot be joined to a specific agent request — a known, accepted limitation | Needed for MAJ-017's operability gap anyway |
| Consecutive-empty warning | A warning after 3 consecutive `empty` results from DuckDuckGo (`pkg/tools/web_search.go::ddgEmptyWarnThreshold`; lane decision — the spec fixes the warning, not a number) | The cheap version of the block-page fingerprint D17 declines to build. On a new install DuckDuckGo is the only provider, so "DuckDuckGo is blocking this IP" is the single most likely real-world failure and today renders as a successful empty search, forever |

Two further controls are **not** decided here and are open questions for the founder, because both add operator policy surface that was not asked for: a per-agent or operator-visible allow-set restricting which providers the agent may name, and a per-turn cap on search calls. Recorded rather than silently omitted — if the founder wants the agent able to escalate spend without either, that should be an explicit decision, not a by-product of D5 plus D12.

## Consequences

### Positive

The operator can see who answers, and who the spare is, without learning a ranking. A failed search names who was actually tried. An `include_domains` filter cannot silently widen, and an `exclude_domains` preference that cannot be honoured is stated rather than dropped. A new install no longer pays Tavily's most expensive depth. Perplexity results come with the sources the call already paid for. Setting a provider as the default from Settings now works on the first attempt, in one request, with no restart (D18) — which is the journey the feature exists for and which round 1 found impossible. Every search leaves a record naming who answered and at what depth.

### Negative

An operator who wants a spare after upgrade has to move the fallback radio; upgrade will not attach DuckDuckGo for them. An agent that names a provider **and uses that provider's own capability** gets an error instead of a spare answer, and has to call again (D6) — a worse experience on that outage and a better one the rest of the time. GLM cannot take an agent `depth` of `low`, and site filters on GLM refuse, until someone reads the current page. An install pointed at OpenAI or Azure with `prefer_native` on sees none of this until that setting or that model changes (D19). The migration now runs in the gateway's credential boot rather than in config load, so a CLI that loads config without booting credentials does not migrate — deliberate, since it could not compute the winner anyway.

### Corrected from round 1

The earlier draft's Positive section claimed that "an existing Tavily install does not get a surprise cheaper (or more expensive) depth". **That is false for row (d), the founder's own instance**, which does get a cost change — from the key starting to work at the baseline. The migration does not add to it (D11's cost note), but the claim as written was wrong and is withdrawn. The same section claimed that "the same query stops varying because a default temperature was left implicit"; `temperature: 0` removes sampler variance only, and retrieval variance remains (D8).

### Neutral

Voice integrations are unchanged. The `search_web` policy entry stays an allow in `defaultToolPoliciesGeneral`. Secrets stay in the credential store; config holds references only. Wire changes go through the contract steps in the spec. No generated file is hand-edited.

Native model search is **no longer filed here**: it decides whether this feature is reachable at all, and it is D19.

### Known narrow paths

R3 — the automatic-DuckDuckGo fallback for an absent `fallback_provider` — is reachable only through a hand-edited config file, because migration writes `none` for every existing install and a new install ships `none`. The mechanism, its Settings label, its contract field and its two tests therefore serve a state the product does not produce. It is kept because the founder settled it and it is cheap; it is recorded here so nobody mistakes it for a common path, and it is listed as an open question.

A rate-limited default means every concurrent call pays a 429 and then a fallback call, so total request volume roughly doubles across both providers at the moment one is throttling. There is deliberately no shared "provider is down" latch; this is the accepted consequence of that simplicity.

## Alternatives considered

**Keep the priority chain and add failover along it.** Rejected. The chain is why the wrong provider runs, and why the screen lies. Seven rungs also hide failures and multiply cost.

**Three stored modes (`auto`, `explicit`, `none`), as the first spec draft had.** Rejected. The founder asked for two settings. `none` is a value of the fallback, not a mode key.

**On upgrade, leave the fallback absent so DuckDuckGo becomes the spare by D4's own rule.** Rejected. For a working Tavily install that also has DuckDuckGo on, an absent fallback would start hopping on the first outage — a silent change of answers and of cost.

**Let an agent-chosen provider fall back on the same classes as the default, unconditionally.** Rejected, but the blanket opposite is rejected too — see D6, which now hops when the call used no provider-specific capability. The round-1 version of this entry pointed at D6 for its reason while D6 pointed at the incident, and D16 had already removed the property the incident turned on. Circular reasoning, now broken.

**Refuse every search whose site filter the running provider cannot honour, includes and excludes alike.** Rejected for excludes (D9). Refusing a whole search because three spam domains cannot be excluded returns zero pages instead of mostly-right pages, and on a new install DuckDuckGo is the only provider, so that is every search.

**When the agent sets a site filter and names no provider, resolve to a usable provider that honours it and say so.** Rejected, now on its own merits rather than by mis-citing D6. It would let a tool argument override the operator's choice of who answers — the operator picked a default for reasons this tool cannot see (cost, jurisdiction, quality), and an argument about *which pages* should not silently reassign *who is paid*. The refusal names the providers the agent can pass explicitly, so the capable path stays one turn away and stays the agent's decision.

**A static tool schema with the full provider enum, relying only on D5's live refusal.** Considered, and adopted in part — which is the round-2 change. The dynamic **enum** is kept: it is cheap and it stops a compliant model naming a provider that is off. The dynamic **description** is dropped: it duplicated enforcement D5 performs anyway, cost per-turn tokens forever, carried a documented staleness bug, and guaranteed a divergence between the agent's text and the `/api/v1/tools` text because `GeneralBuiltinMetadata` has no config in scope (D7).

**Read the credential store during config load to compute the migration winner.** Rejected. The interface threaded into `LoadConfigWithStore` is `config.CredentialStore`, whose only method is `Set` — it cannot read. Widening a write-only interface across every implementer, to answer a question the environment answers at the next boot step, is the wrong trade. The migration moves after injection instead (D11).

**Ship the migration inside config load and accept that the first load records the pre-injection winner.** Rejected outright. It would write `duckduckgo` for 100% of installs, permanently and idempotently, including every correctly configured one.

**Build a synthesised-answer flag (`include_answer`, Brave summary, GLM intent).** Rejected by the founder. Extra model calls, and not the product. Perplexity is the exception only because the model call already happens.

**Add a Settings address field for SearXNG.** Rejected by the founder for this feature (D10). Its ingest bound and SSRF-safe client are separate and are required (D10).

**Ship Exa later.** Rejected by the founder. It is the eighth provider in this delivery (D2).

**Map every vendor depth enum onto the tool schema.** Rejected. Four Tavily words plus three Perplexity words plus GLM's words would sit in the prompt on every turn. The agent gets `low` / `medium` / `high`; the operator keeps the vendor's own values, including Tavily `ultra-fast`.

## Acceptance criteria

- **AC-1.** The seven existing providers are still the seven. Exa can be stored, injected, warned about, and chosen as default or fallback. No eighth provider other than Exa is added.
- **AC-2.** On a new install **where native model search is not in effect** (D19), a search calls DuckDuckGo and nobody else. The differential form, which is what is actually observable: given a config where the old chain would have picked DuckDuckGo but `default_provider` names another usable provider, that named provider is called.
- **AC-3.** Settings shows a default control and a separate fallback control, including "No fallback". An absent fallback with DuckDuckGo usable and a different usable default resolves to DuckDuckGo and the screen labels it automatic. An explicit `none` is not flipped when the default changes.
- **AC-4.** With one usable role and no resolvable fallback, a network failure names that provider only. Nobody else is called.
- **AC-5.** `provider` accepts only a usable id. An unusable id is a refusal that lists the usable ids and does not call anyone. A named provider that fails **after using that provider's own capability** does not hop; a named provider that fails on a plain query does hop, with the default's failure reported. Naming the resolved default behaves exactly as omitting `provider`.
- **AC-6.** The definition registered for the agent lists the usable providers in `provider`'s enum, and omits `depth` and the domain arguments when the resolved default does not honour them. A DuckDuckGo-only install's definition has `query`, `count` and `range` only. The **rendered definition** — description plus serialised parameters — is at most 2,400 bytes **at the maximal configuration** (all eight usable, all capabilities present), and `Description` is byte-identical between the live instance and `GeneralBuiltinMetadata`'s.
- **AC-7.** Tavily still sends `include_answer: false`. GLM still sends `search_intent: false`. Brave does not send `summary`. A Perplexity result includes the citations array (or an explicit "none returned"). The **decoded** Perplexity request body has `temperature` present and equal to `0`.
- **AC-8.** `include_domains` against a provider that cannot honour it refuses before any request, names a provider that can, and does not hop. `exclude_domains` against such a provider proceeds and the result states that the exclusion was not applied. Tavily sends snake_case `include_domains` / `exclude_domains`; Exa sends camelCase `includeDomains` / `excludeDomains`.
- **AC-9.** No new SearXNG field and no SearXNG address control are added. An install that already resolves to SearXNG keeps that default across migration. SearXNG's response goes through the ingest bound and its client comes from `makeSearchClient`.
- **AC-10.** The five rows in D11's table hold. Row (c) does not gain a hop; row (d) does not gain a hop and does not drop depth to `basic`; row (e) switches the provider on and logs it.
- **AC-11.** A new Tavily install sends `basic` unless the agent sets `depth`. A migrated Tavily object sends `advanced` until the operator changes it. `depth: high` sends `advanced` for that call only. `depth` above the operator's configured ceiling is clamped, with a note.
- **AC-12.** The search badge follows the Definitions test. A key reference with an empty `APIKey()` is not shown as the provider that runs — **and a key stored in the same request reads ready, not "key not reaching search"** (D18).
- **AC-13.** The existing keyless warning is verified, not rebuilt: with DuckDuckGo enabled and Tavily enabled but keyless, a warning names Tavily. Exa is added to the same enumeration and to `nonChannelRefsFor`, proven by a test that walks the provider catalogue.
- **AC-14.** Migration runs after credential injection and before the tools are built, computes the winner with the Definitions test, defers when a keyed provider is enabled with a reference whose key does not resolve, and writes `tools.web.roles_migrated_at`. A test exercises the real boot order, not a pre-seeded environment.
- **AC-15.** One `PUT` that stores a key and sets that provider as the default results, with no restart, in a search that calls that provider, and a row that reads ready. A key can be stored **without** changing any role.
- **AC-16.** A key that appears after the tool was built makes that provider callable without re-registration.
- **AC-17.** The chat card renders for a tool call **named `search_web`** and shows the provider that answered, the role, and a hop or skip line when there was one.
- **AC-18.** When `prefer_native` is in effect for the active model, the Settings search group says the active model searches natively and names no provider as the one that answers.
- **AC-19.** Every search call emits one structured record naming the resolved default, the resolved fallback, the provider that answered, its role, the depth sent, and the failure class or hop/refusal/skip. A provider message in any result or log is redacted through the registered sensitive values and truncated to 300 characters.
- **AC-20.** Adding a provider to the single `searchProviderCatalogue` and changing nothing else makes it storable, injectable, selectable, warnable and listable, proven by a test that walks the catalogue rather than naming ids.

## Work packages

| Package | Owns | Proves |
|---|---|---|
| WP-1 | The single `searchProviderCatalogue`; config keys with their `env:"-"` tags; Exa config; shipped defaults; the migration in the credential-boot path with its marker and its defer guard | AC-1 (storage), AC-2, AC-3, AC-9 (migration), AC-10, AC-14, AC-20 |
| WP-2 | The call path: the resolver handle on the tool (D4a), D4's ordered table, at most one fallback, D17's classes, D17a's budget, D6's capability rule, D9's include/exclude split with the shared hostname validator, the result text, the per-call record | AC-4, AC-5, AC-8 ("does not hop"), AC-16, AC-19 |
| WP-3 | Provider bodies: the depth map and ceiling, Tavily domains, Exa (including camelCase domain fields), Perplexity citations and `temperature: 0`, DuckDuckGo status check, Brave's `(via …)` header, SearXNG through `makeSearchClient` and the ingest bound | AC-1 (provider), AC-7, AC-8 (wire names), AC-9 (SearXNG), AC-11 |
| WP-4 | The dynamic `provider` enum and its generated argument description; the static `Description`; the byte cap measured at the maximal configuration | AC-6 |
| WP-5 | Contract-first: the request shape in D18, the `id` enum, the role and usability fields. Settings: default and fallback controls, resolved usability, the native-search notice. Stop deleting other key references. Voice rows unchanged | AC-3, AC-12, AC-15, AC-18 |
| WP-6 | Exa in `enabledButKeylessSearchProviders`; verify the existing warning against D14 and keep its two tests green. **Small — the warning itself already shipped at the baseline** | AC-13 |
| WP-7 | `search_web`'s production tool-UI registration and the card's provider/role/hop rendering (the search half of issue #898) | AC-17 |
| WP-8 | D18's save path: route the Integrations `PUT` through `triggerReloadAndWaitOutcome`, judge usability after the reload, build the response from post-reload state | AC-15, AC-12 |

WP-5 is contract-first. The order is in the spec, and the regenerated `pkg/gateway/inboundschemas/` copy is part of the same commit. No hand-edited generated types.

## Affected components

- **Config:** `pkg/config/config.go::WebToolsConfig` and `TavilyConfig` (plus the Perplexity, GLM and new Exa structs), `pkg/config/defaults.go::defaultToolsConfig`, the single provider catalogue, and the migration — which now lives in the gateway boot path, not beside `migrateCLITokenOutOfUsers`.
- **Tool:** `pkg/tools/web.go::NewWebSearchTool`, `WebSearchTool`, `WebSearchToolOptions`, `enabledButKeylessSearchProviders`, each `Search` method named above, `pkg/agent/loop_wire.go::registerCoreTools`. `pkg/tools/general_builtin_catalog.go::GeneralBuiltinMetadata` stays a non-executed metadata instance and, with a static `Description`, no longer diverges from the live one.
- **Credentials:** `pkg/credentials/inject.go::nonChannelRefsFor` — named explicitly. Exa joins it. This ADR does not redesign injection.
- **Boot and reload:** `pkg/gateway/gateway_boot_credentials.go::bootCredentials` (the migration's new home), `pkg/gateway/gateway_reload.go::executeReload` (the mirror, plus the self-write registration).
- **Gateway:** `pkg/gateway/rest_integrations_auth.go::integrationCatalogue`, `searchRefSectionByID`; `pkg/gateway/rest_integrations_roles.go::buildIntegrationResponse`, `handleIntegrationProviderUpdate`, `applySearchIntegrationRoles` (the pre-ADR `activeSearchProviderID` and `applySearchIntegration` were removed); `pkg/gateway/rest_config.go::storeCredential`, `safeUpdateConfigJSON`, `refreshConfigAndRewireServices`; `pkg/gateway/video_embed_hosts.go::validVideoEmbedHosts` (its rule set mirrored by `pkg/tools/web_search.go::normalizeSearchDomains` for the search tool's site filters).
- **SPA:** `src/components/settings/IntegrationsSection.tsx`; `src/components/chat/tools/WebSearchResult.tsx::WebSearchResultUI`; `src/components/chat/OmnipusRuntimeProvider.tsx` (the `search_web` registration).
- **Contracts:** `contracts/components/schemas/IntegrationProvider.yaml` (search rows gain role and usability fields; the `id` becomes an enum) and `IntegrationProviderUpdateRequest.yaml` (D18's shape), plus the regenerated `pkg/gateway/inboundschemas/`.

## What this ADR could not establish

| Gap | Consequence |
|---|---|
| The JSON key of Perplexity's citations array was not re-read from the official response schema. The founder named `citations` | Implementer confirms the key before coding. Do not invent a second key |
| Perplexity's own default temperature. Third-party pages say `0.2`; the official reference was not re-read | D8 sends `0` so no default can apply. If the official default is already `0`, the explicit `0` is harmless |
| GLM `content_size` values other than `medium` (our code) and `high` (one vendor example). GLM `search_domain_filter` request shape | `low` is refused. Domain filters on GLM are refused until the page is read |
| Whether Exa has a depth axis. Exa's domain-filter names **are** established (camelCase, round 1) | No depth axis is assumed. The domain names are recorded in D9 |
| Tavily's current credit numbers | Not re-fetched. The decision does not depend on them, and the description must not quote a credit count |
| A captured body of a DuckDuckGo block page that returns HTTP 200 | 200 with no anchors stays `empty`; D20's consecutive-empty warning is the cheap detector instead of a fingerprint |
| GitNexus | Not in this session's tool list. No impact graph was run; the component lists come from direct symbol reads |

Two gaps from round 1 are now **closed**, and both changed a decision: whether `registerCoreTools` runs again after unlock (it does — three call sites — see D7), and what the tool can check usability against (nothing, today — see D4a).

## Open questions for the founder

Each of these is a real decision this ADR deliberately did not make on the founder's behalf.

| # | Question | Options | Recommendation |
|---|---|---|---|
| Q1 | `prefer_native` ships `true`, so on an OpenAI or Azure install none of this feature is reachable (D19). Should that stay? | (A) Leave the default, show the notice. (B) Flip the default to `false` so the operator's configured provider always wins. (C) Make it a visible choice in the search group | **A** for this delivery — flipping it is a search-quality and cost decision that deserves its own look — but it means the feature is invisible on a common install shape |
| Q2 | Should the agent's choice of provider be restricted by an operator allow-set, and should there be a per-turn cap on search calls (D20)? | (A) Neither now; the depth ceiling and the per-call record are enough. (B) Add an allow-set. (C) Add both | **A**, with the record in place so the exposure is measurable before policy is added |
| Q3 | R3's automatic DuckDuckGo fallback is reachable only through a hand-edited file, since migration and the shipped defaults both write `none`. Keep it? | (A) Keep — cheap, and it is a settled decision. (B) Delete R3, `fallback_automatic`, the "Automatic fallback" label and its two tests; absent means `none` | **B** on simplicity grounds, but it reverses a decision the founder settled, so it is a question and not a change |
| Q4 | A migrated Tavily object keeps `advanced` depth, which is what the code sends today (D11's cost note). Is preserving the bill the right call, or should migration take the install down to `basic` and tell the operator? | (A) Preserve `advanced`, log it, surface it in Settings. (B) Write `basic` and log a deliberate cheapening | **A** — silently changing what an operator's searches cost, in either direction, is the thing this ADR exists to stop |


## Amendment — 2026-09-28 (gate round 1/2 fixes on PR #944)

Text changed after the review gates, to match the shipped code; no decision was reversed:

- D9: hostname validation MIRRORS the `pkg/gateway/video_embed_hosts.go` rule set in `pkg/tools` (it cannot reuse the unexported function across packages).
- D20: the per-call record exists on every search path (default and named provider); it does not carry request-correlation fields (`pkg/logger` has no context-aware variant) — a known limitation. The consecutive-empty-DuckDuckGo warning fires after 3 empty results in a row (`ddgEmptyWarnThreshold`).
- D15/FR-035: `pkg/config/search_provider_catalogue.go` is the single provider catalogue; symbol references in this ADR and the spec updated to the current names.
- R3: the automatic DuckDuckGo fallback requires a usable default (the third conjunct is enforced); an unusable default with an absent fallback is R7 (nobody is called).

## Changelog — round 2 corrections

| Finding | What changed |
|---|---|
| CRIT-001 | Evidence baseline rewritten: `07a75c104` **is** the injection fix. Context ¶4 replaced with the three-cause table. D14 reduced to "already shipped; verify and add Exa". WP-6 reduced to that one line. AC-13 restated. D11's precondition changed from a release gate to a met condition |
| CRIT-002 | D11 rewritten: the migration moves out of config load into the credential-boot path after `InjectFromConfig`, mirrored in `executeReload`, with the boot-order block quoted. Store-read alternative rejected with the reason (`config.CredentialStore` is `Set`-only). Defer-on-ambiguous guard added. AC-14 rewritten |
| CRIT-003 | R1 restated in roles rather than a global count; R3's "two or more usable" clause and R5's dead clause removed |
| CRIT-004 | R4b added for a configured-but-unusable fallback; "first match wins" stated above the table |
| CRIT-005 | **New D18.** The Integrations `PUT` routes through `triggerReloadAndWaitOutcome`; usability judged after the reload; the response built from post-reload state; the "not usable yet → reject" rule replaced; the request shape enumerated. D13 marked as shipping only with D18. New AC-15 |
| CRIT-006 | **New D4a.** The tool holds a resolver rather than baked key strings, with the secret-exposure note and the ownership assigned to WP-2. New AC-16 |
| CRIT-007 | D16's tool-name claim inverted back to the truth, evidence tabled, issue #898 cited, a production `search_web` registration required. New WP-7, new AC-17 |
| CRIT-008 | **New D20.** Depth ceiling, per-call structured record, consecutive-empty warning. The allow-set and per-turn cap raised as Q2 rather than assumed |
| MAJ-001, MAJ-002 | **D6 reversed.** Capability-based hard-fail; plain queries hop; naming the resolved default is identical to omitting `provider`. Alternatives de-circularised |
| MAJ-003, MAJ-004, MAJ-005, MAJ-006 | D9 split into include-as-requirement and exclude-as-preference; the default-honours/fallback-does-not case defined for both; the D6 mis-citation deleted and the capability-directed alternative argued on its merits; capability arguments gated on the resolved default |
| MAJ-007, MAJ-008, MAJ-009 | D7: static `Description`, dynamic enum kept, the per-provider lines moved into the `provider` argument description; the cap moved onto the rendered definition in bytes, measured at the maximal configuration, with a defined overflow; prompt caching addressed via `prompt_cache_key`; the "not traced" gap closed with three call sites; a static-schema Alternatives entry added |
| MAJ-011 | **Partly disputed, not applied as recommended.** `TavilySearchProvider.Search` hardcodes `advanced`, so writing `advanced` preserves post-baseline behaviour and writing `basic` would be a silent cheapening. Kept `advanced`, corrected the false Positive claim, added logging and the Settings surface, and raised it as Q4 |
| MAJ-012 | D11 step 7: a keyed provider switched off with a resolvable key is read as the operator's choice, switched on and logged. Cause 3 added to Context |
| MAJ-013 | D15 extended to the id enumeration; WP-1 owns one `searchProviderCatalogue`; new AC-20 |
| MAJ-014 | `tools.web.roles_migrated_at` marker, with the reason and the note that `Version` cannot express one |
| MAJ-015 | **New D19**, promoted out of Neutral, with the provider scope verified precisely (OpenAI, Azure, Codex-with-web-search). AC-2 conditional, new AC-18, and Q1 |
| MAJ-016 | **New D17a**: a 45-second budget, a skipped fallback with a stated reason |
| MAJ-017 | D20's record and consecutive-empty warning; D16's fixed reason vocabulary |
| MAJ-018 | D16: redaction through the registered sensitive values and a 300-character truncation, with Tavily's in-body key as the reason. New AC-19 |
| MAJ-019 | D9: hostname rules mirroring `validVideoEmbedHosts` (implemented in pkg/tools, which cannot import pkg/gateway), with per-entry and count caps, wildcards rejected, and both-lists precedence |
| MAJ-020 | D8's temperature benefit narrowed to sampler variance; the vacuous substring assertion replaced with a decoded-body assertion |
| MAJ-021 | D10: the ingest bound and the SSRF-safe client named as security fixes with their own acceptance criterion |
| MAJ-022 | D18: the request shape enumerated, `active: false` given a meaning, `inboundschemas` regeneration named |
| MIN-001, MIN-002 | Dead R5 clause and redundant R3 clause deleted |
| MIN-003, MIN-011 | `VideoEmbedHosts` precedent corrected (it is a pointer with `omitempty`); the write technique described accurately as a `map[string]any` round-trip that preserves unmodelled keys |
| MIN-004 | `env:"-"` and `yaml:"-"` stated, with the migration-suppression reason |
| MIN-005 | AC-2 given its differential form |
| MIN-006 | "Usable" defined once, in Definitions, with the two neighbouring tests kept apart |
| MIN-007 | AC-1's storage half assigned to WP-1 |
| MIN-008 | `nonChannelRefsFor` named in D2, D14, AC-13 and Affected components |
| MIN-009 | Exa's camelCase `includeDomains` / `excludeDomains` recorded in D2 and D9; Exa placed in the honours-site-filters set |
| MIN-010 | Hand-typed `searxng` behaviour stated in D4 |
| MIN-012 | Brave's missing `(via …)` header assigned to WP-3 |
| MIN-013 | Context ¶2 corrected: SearXNG's branch does check `Enabled` |
| OBS-002 | The doubled request volume under a rate-limited default recorded in Consequences |
| Structural | Acceptance criteria added for D15 (AC-20), D16 (AC-17, AC-19) and D4a (AC-16); AC-2, AC-6 and AC-7 assertions fixed |
