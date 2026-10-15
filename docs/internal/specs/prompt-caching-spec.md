# Feature Specification: Prompt caching

**Created**: 2026-10-15
**Status**: Draft
**Input**: Founder interview decisions D1–D12 in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/prompt-caching-interview-decisions-20261015.md`, architect analysis `prompt-caching-analysis-20261015.md`, provider research `prompt-caching-research/` (top50.md, research-1.md through research-5.md), issues #750, #980, #1307, #654. Code verified on this worktree at `8ba69a673` (branch `work/prompt-caching-spec`). Catalog repo `elicify-ai/omnipus-provider-catalog` read via the GitHub API. models.dev cost shape read from `sst/models.dev` `packages/core/src/schema.ts` and a fetch of `https://models.dev/api.json` on 2026-10-15.

D1–D12 are the interview. D13–D18 close the questions this draft first left open. D19 and D20 close grill round 1. D21 and D22 close grill round 2. No requirement is blocked. There is no remaining open question. No round-1 or round-2 finding is rejected. D1–D20 are not reopened. D22 replaces only the live-proof population in D9 and D17.

The evening correction: the interview line that "the cost limit would stop protecting you" was wrong. Omnipus has no cost limit. ADR-053 D12 retired the SEC-26 daily USD cap (`pkg/security/ratelimit.go`). The 2026-09-14 update on that ADR deleted the app-level token budget. D8's token-limit sentence is void (D13).

| ID | Closes | Decision |
|---|---|---|
| D13 | Q-CAP | No new limit. Price honestly. A call with no price is `unpriced` and its tokens are still counted. |
| D14 | Q-SUB | ChatGPT sign-in calls are marked `subscription`: cached and uncached tokens only, no dollar figure. |
| D15 | Q-UI, Q-LOG | No new screen. The existing Usage screen adds a cache-hit percent beside the cache totals it already shows. The per-call line is at info. The shipped log level is warn, so that line is hidden until the level is info. Per-area levels stay #1324. |
| D16 | Q-KEY | The cache key is the session id (one conversation), not the agent id. |
| D17 | Q-LIVE | Superseded for who is called live, by D22. DeepSeek is not a live gate. |
| D18 | Q-TTLPRICE | Store 5-minute and 1-hour writes separately. Price 5-minute writes from catalog `cache_write`. Until the catalog carries `cache_write_1h`, price 1-hour writes at that provider's documented 1-hour multiplier times the catalog input price. Anthropic's published multiplier is 2. A provider with no documented 1-hour multiplier leaves that portion `unpriced`. The multiplier is a documented factor, not a dollar price list. `cache_write_1h`, when published, replaces the multiplier. |
| D21 | Fireworks | Read only `usage.prompt_tokens_details.cached_tokens`, the same field as every other OpenAI-style host. No header. No performance-metrics fallback. No metrics opt-in. |
| D22 | Live proof | Live proof uses only accounts we have: OpenRouter, the Z.AI/GLM coding plan, the MiniMax coding plan, the Moonshot coding plan, and OpenAI through the ChatGPT subscription. Each check sends the same conversation twice. The second call must report cached tokens greater than zero. A subscription route records tokens and no dollars (D14). Every other provider is a fixture taken from that provider's documented example, not a live call. |

D19: each provider uses its own documented cache rules inside the one connector. D6's 1-hour / 5-minute split applies only where that host documents those marks. Moonshot sends no mark. MiniMax uses its documented 5-minute or automatic mode and no 1-hour mark. D20: the log carries bounded numeric usage counters and the correlation ids only. It never carries an arbitrary string or the rest of the response.

## Available Reference Patterns

`docs/reference/go-implementation/00-overview.md` is not in this tree. No reference pattern applies.

---

## Existing Codebase Context

GitNexus MCP tools were not available in this session. The table is from Read and search on this worktree at `8ba69a673`. Impact is **Inferred** from caller search, not from a GitNexus impact run.

### Symbols involved

| Symbol | Role | What it does today |
|---|---|---|
| `pkg/providers/factory_provider.go::CreateProviderFromConfig` | modifies | Anthropic protocol constructs `anthropic_messages`. `google` and `ollama` share the OpenAI-compatible HTTP provider. `openai-chatgpt` constructs `CodexProvider`. Bedrock has its own branch. |
| `pkg/providers/anthropic_messages` | deletes | Hand-written Anthropic HTTP connector. Flattens system text. Parses cache usage. No streaming. |
| `pkg/providers/claude_provider.go::NewClaudeProvider` | extends | Official SDK wrapper. Test-only caller today. Maps system parts to a cache marker. Streams. Constructor takes a token only, not a base URL or timeout. |
| `pkg/providers/protocoltypes/types.go::UsageInfo` | extends | Uncached prompt, cache read, cache write, completion. Total is the sum of those four. |
| `pkg/providers/protocoltypes/types.go::CacheControl` | extends | `Type` only. No lifetime. |
| `pkg/agent/context.go::BuildMessages` | modifies | One system message: static prompt (marker), dynamic block (clock and skill reminder), active-skill text, breadcrumb. History follows. |
| `pkg/agent/loop_run_turn.go::prepareCallMessages` | modifies | Inserts manifest, web-rendering, workspace instructions, goal rubric, and scratchpad ahead of history. Sets the cache key to the agent id. |
| `pkg/agent/loop.go::estimateLLMCallCost` | replaces | Prices uncached input and output from `llmRateTable`. Ignores cache tokens. Unknown models use the fallback ($3 / $15 per million). |
| `pkg/agent/loop.go::debitLLMUsage` | modifies | The single accounting path into turn stats. |
| `pkg/session/usage.go::AggregateUsage` | extends | Usage report by agent, model, or session. Cache read and write already summed. No hit percent. No provider dimension. |
| `pkg/providers/openai_compat/provider.go::supportsPromptCacheKey` | extends | Key sent only to `api.openai.com` and `*.openai.azure.com`. Comment says other hosts reject unknown fields. |
| `pkg/providers/openai_responses_common/responses_common.go::parseResponse` | modifies | Does not read cached-token details. This is #1307. |
| `pkg/providers/catalog/document.go::Model` | extends | No price fields. Embedded snapshot `providers_catalog.json` model objects have no cost keys. |

### Impact assessment

| Symbol modified | Risk | Direct callers to update or retest | Indirect |
|---|---|---|---|
| `CreateProviderFromConfig` | High | Every model call. Factory tests. Anthropic-protocol rows (DeepSeek, MiniMax, Moonshot, Z.AI, and Anthropic itself). | Streaming, tool-call shape, third-party base URLs. |
| `BuildMessages` / `prepareCallMessages` | High | Every turn. Skill-reminder placement tests. Goal rubric and scratchpad behaviour. | Window trim, recall span, delegation depth text. |
| `estimateLLMCallCost` | Medium | `debitLLMUsage` only, then session `cost` on the wire (`SessionStats.yaml`). | Usage screen does not show dollars today (`docs/settings.md` says tokens, not money). Room allocated cost does not call this function. |
| `AggregateUsage` | Medium | `GET /api/v1/stats/tokens` (`pkg/gateway/rest_stats.go::HandleTokenStats`). Usage screen tests. | None beyond that report. |
| Tool-list lifetime | Medium | Tool manifest and MCP TTL (`ADR-071` §1.1.1: static tools already stay; MCP tools expire). | Goal-door narrowing replaces the list for one turn. |

### Relevant execution flows

| Flow | Relevance |
|---|---|
| One model call inside a turn | Where the body is built, the marker or key is attached, usage is parsed, and cost is recorded. |
| Several tool steps inside one turn | The history grows at the end. A stable prefix is what makes step 2 onward a cache read. |
| Usage report | Where an operator sees cache-read and cache-write today, and where the hit percent must appear. |

### Cluster placement

Provider adapters, the agent turn loop, the session usage report, and the catalog repository. No new screen.

---

## User Stories & Acceptance Criteria

### User Story 1 — See whether caching is working (Priority: P0)

An operator runs agents all day and cannot tell whether the provider reused the start of the request. The product must show that from the log of each call and from the usage report the product already has. There is no new screen and no new setting.

**Why this priority**: D1. Without a number, a correct marker can still be a silent miss, which is how #750 shipped.

**Independent Test**: A fake provider returns a cache read on the second call. The log line and the usage report both show that read, the uncached input, and the write, with no new page in the app.

**Acceptance Scenarios**:

1. **Given** a successful model call, **When** the call finishes, **Then** the log gains one info line naming the provider, the model, the agent, the session, the call, the numeric token counters, and either a dollar cost, the word unpriced, or the word subscription, and the line contains no raw usage object.
2. **Given** two or more calls in a period, **When** the operator opens the existing usage report, **Then** it shows a cache-hit percent for each agent and for each provider, and the percent is empty rather than zero when that row had no input tokens.
3. **Given** the app as shipped, **When** the operator looks for a new caching page or a new caching setting, **Then** none exists.

---

### User Story 2 — Ask Anthropic-protocol hosts to cache, through one connector (Priority: P0)

Anthropic does not cache unless the request carries a cache marker. Omnipus builds a marker and then sends the call through a connector that drops it. The official connector already understands the marker and can stream. There must be one Anthropic-protocol connector, the official one, and the hand-written connector must be removed.

**Why this priority**: D3 and D6. This is the only way those hosts cache at all.

**Independent Test**: A fake Anthropic server records the request body after the official connector builds it. Native Anthropic carries the marks below. Moonshot and MiniMax carry only their own documented marks. Before the hand-written connector and its tests are deleted, the official connector already passes the old guarantees: a reconstructed tool call whose name exists only in the canonical JSON is sent with that name, its arguments, its id, and its paired result; the model id is verbatim (`MiniMax-M2.7` is not rewritten); media parts are still sent; parallel results stay correlated. The factory-to-wire body is the proof.

**Acceptance Scenarios**:

1. **Given** an Anthropic-protocol provider, **When** a call is sent, **Then** the request is built by the official Anthropic connector, and the hand-written connector is gone.
2. **Given** a native Anthropic call with tools, stable instructions, and a conversation, **When** the body is sent, **Then** it marks the tool list and the stable instructions for a 1-hour cache and the conversation tail for a 5-minute cache, every 1-hour mark sits before every 5-minute mark, and there are at most four marks. The tool-list mark stays so an instruction change can still reuse the tool prefix. Moonshot sends no mark and no 1-hour option. MiniMax uses its documented 5-minute or automatic mode and no 1-hour mark. Any other Anthropic-protocol host gets only the marks it documents.
3. **Given** a streamed Anthropic-protocol call, **When** the stream ends, **Then** cache-read and cache-write tokens are still recorded.
4. **Given** a third-party Anthropic-protocol host that rejects the official body during the live check, **When** the connector is adjusted, **Then** there is still exactly one connector, not a second package kept beside it.

---

### User Story 3 — Keep the start of the request stable (Priority: P0)

A cache hit needs the start of the next request to be byte-for-byte the same as the previous one, up through the conversation that already happened. Today a clock with seconds, and several notes that change every turn, sit in front of that conversation, so the history never matches.

**Why this priority**: D4 and D5. Marker work without a stable prefix only reuses the static instructions, not the history, which is the bulk of a tool loop.

**Independent Test**: Build two consecutive requests for the same session. Advance the clock by a few seconds, change the scratchpad, and load one tool on the first request only. The bytes up through the history that existed on the first request are identical on the second request.

**Acceptance Scenarios**:

1. **Given** a session, **When** the next request leaves the normalizer and the adapter, **Then** the clock, the tool-summary note, the scratchpad note, and the goal-checklist note are one trailing user-role text block after history and after any completed tool-call group. They are not a system-role message, and they are not inserted between a tool call and its results. The history cache mark sits on the last history content before that block. The web-display note and the workspace instructions stay in front.
2. **Given** a non-core tool, including an MCP tool, loaded earlier in the session, **When** later turns are built and the tool is invoked, **Then** its definition is still advertised, it still runs, a second session of the same agent does not list it and cannot run it, and a Deny, a removed assignment, or a disconnect removes it even though the prefix then changes. The same set of tools is serialized in the same order every time. A static builtin that never expires does not satisfy this.
3. **Given** the skill list advertised at the start of the session, **When** later turns are built, **Then** that advertised list is unchanged, the per-turn skill reminder sits with the other volatile notes after the conversation, and a labelled token count or token estimate of the tool list and of the skill text is recorded for that agent. The record names the estimator. A byte or character count alone does not count. The product does not start sending every allowed tool from the first turn.
4. **Given** a turn that must narrow the tool list to the goal-setting tools, **When** that turn is sent, **Then** the narrowing still happens, and that turn is allowed to miss the cache. The following ordinary turn uses the session's tool list again.

---

### User Story 4 — Record cache tokens, and send a key only where it is accepted (Priority: P0)

Hosts do not share one usage object. A reader that understands only one field records zeros on the others. Some hosts also want a cache key, and some reject that field. Bedrock is not in this feature.

**Why this priority**: D2. The founder checked the assumption that Anthropic plus OpenAI covers the rest: true for asking to cache, false for reading the result.

**Independent Test**: For each usage shape in the provider table, a fake response with a non-zero cached-token field produces the same non-zero cache-read (and cache-write, when the shape has one) on the normalized usage, and the prompt total is uncached-only. A host documented as rejecting the cache key does not receive one.

**Acceptance Scenarios**:

1. **Given** a response in any shape listed in the provider table, **When** it is parsed, **Then** cache-read and cache-write are taken from that shape, the normalized prompt count is uncached tokens only, and the normalized total equals uncached plus cache-read plus cache-write plus output.
2. **Given** a host that documents a cache key, **When** a call is sent, **Then** that key is sent, and it is not sent to a host that documents the field as rejected.
3. **Given** a host that caches only when the prefix is stable and documents no marker and no key, **When** a call is sent, **Then** no cache marker and no cache key are added.
4. **Given** a Bedrock provider, **When** this feature ships, **Then** Bedrock requests are unchanged and Bedrock cache fields are not newly parsed.

---

### User Story 5 — Price from the catalog, and never pretend a missing price is zero (Priority: P0)

The hand-kept rate table does not contain current models and does not price cache tokens. Once caching works, a cost that ignores cache reads falls toward zero. The catalog's sources already publish prices, and the catalog build drops them. Omnipus does not keep its own price list.

**Why this priority**: D7, D8, and D13. The analysis showed that fixing the marker without fixing the price understates cost. D8's token-limit sentence is void: there is no cost cap to extend, and this feature does not add one.

**Independent Test**: A fixture catalog prices one model and omits the price on another. The priced call's cost matches the fixture arithmetic, including cache read and cache write. The unpriced call still counts tokens and is reported as unpriced, not as $0 and not as the old fallback rate.

**Acceptance Scenarios**:

1. **Given** a catalog row with input, output, and cache prices, **When** a call is accounted, **Then** the cost uses those prices and the hand-kept rate table is not consulted, because it has been removed.
2. **Given** a model with no input price, **When** the call is accounted, **Then** tokens are stored and the call is unpriced. The stored cost does not become zero in a way that looks free.
3. **Given** an input price and no cache price, **When** cache tokens are accounted, **Then** those tokens use the input price. **Given** a published cache price of zero, **When** cache tokens are accounted, **Then** the price is zero, not the input price.
4. **Given** a model whose catalog price changes with input length, **When** the call is accounted, **Then** the input size is uncached input plus cache read plus cache write, and output is not included. The boundary follows that source's own wording. Where the source says "up to N" at the base rate and "over N" at the high rate, exactly N is the base rate. A different source that includes N in the high band is priced that way. One inclusive rule is not applied to every `tier.size`.

---

### User Story 6 — Prove it with fakes, then five live runs (Priority: P0)

A unit test can prove the bytes and the arithmetic. A live call is used only where we already have an account. A live miss is not an error: providers return success with zero cached tokens.

**Why this priority**: D9, narrowed by D22. The fake tests land in CI. The five live runs are a landing gate, not a CI job. No other provider is called live.

**Independent Test**: CI runs the fake-provider suite with no network, including fixtures copied from provider documentation. The landing notes contain five saved runs, one per account in D22. Each run sent the same conversation twice, and the second call reports cached tokens greater than zero.

**Acceptance Scenarios**:

1. **Given** CI with no provider credentials, **When** the suite runs, **Then** it checks the request bytes, the stable prefix, every usage shape taken from the documented examples, and the cost arithmetic, including that a cache-heavy second call costs less than the first and does not cost nothing.
2. **Given** a landing candidate, **When** the evidence is checked, **Then** it contains one recorded run each for OpenRouter, the Z.AI/GLM coding plan, the MiniMax coding plan, the Moonshot coding plan, and OpenAI through the ChatGPT subscription. Each run sent the same conversation twice, and the second call reports cached tokens greater than zero. The ChatGPT subscription run records tokens and no dollars. No other provider was called live.
3. **Given** a live second call that returns success and zero cached tokens, **When** the proof is judged, **Then** the run fails. Success of the HTTP call is not a pass.

---

## Behavioral Contract

Primary flows:

- When a model call completes, the system records uncached input, cache read, cache write, and output as four separate counts, and writes one log line.
- When the operator opens the usage report, the system shows cache-hit percent per agent and per provider.
- When the host documents Anthropic's lifetimes, the system sends cache marks on the tool list (1 hour), the stable instructions (1 hour), and the conversation tail (5 minutes), with the tool mark kept so an instruction change can still reuse the tool prefix. Moonshot gets no mark. MiniMax gets its documented 5-minute or automatic mode and no 1-hour mark.
- When the next turn is built, the system places the clock and the volatile notes after the conversation, and keeps the web-display note and the workspace instructions before it.
- When a tool was loaded earlier in the session, the system keeps it in the tool list until the session ends.
- When the catalog has a price, the system uses it. When it does not, the system says unpriced. When the call is ChatGPT sign-in, the system says subscription and records no dollars.
- When a cache key is sent, the value is the session id.

Error flows:

- When a provider returns success with no cached tokens, the system records a zero read and does not treat that as a cache hit.
- When a usage object uses a field this spec does not list, the system still completes the call and records a zero cache read rather than failing the turn.
- When the catalog has no input price, the system does not substitute the old fallback dollar rate.

Boundary conditions:

- When the denominator of the hit percent is zero, the system leaves the percent empty.
- When a published cache price is zero, the system charges zero for those tokens.
- When a goal-setting turn narrows the tool list, the system sends the narrow list and allows a cache miss.
- When history is trimmed or the model changes, the system allows a miss and does not freeze history or pin the model.

---

## Edge Cases

- A call under the provider's minimum cacheable length returns success and zero cache tokens. Expected: recorded as a miss, not as a failed call.
- The documented counters are absent. Expected: cache read and cache write are zero, and the prompt count is the provider's prompt total. Extra text inside the usage object is not logged.
- DeepSeek reports both a hit count and a nested cached-token count for the same tokens. Expected: one cache-read figure, not the sum of both.
- Moonshot and Alibaba report a write count inside the prompt total. Expected: the normalized prompt count excludes it, and the write is stored on its own.
- A 1-hour write and a 5-minute write arrive on the same Anthropic call. Expected: both counts are stored separately. The 5-minute write uses catalog `cache_write`. The 1-hour write uses 2 times the catalog input price until the catalog publishes `cache_write_1h`, and then uses that. The log names which rule priced the 1-hour portion. A provider that documents no 1-hour multiplier leaves that portion unpriced and still stores the token count.
- Two sessions of one agent run at the same time. Expected: each request's cache key is that session's id, not the agent id.
- The skill list on disk changes while a session is open. Expected: the advertised list for that session does not change.
- An unsupported catalog row (blank protocol, or a tier the factory cannot construct) is not given a new connector by this feature.
- A host that documents `prompt_cache_key` as unsupported receives no such field, even if a shared client would otherwise add one.
- Window trimming rewrites the oldest history. Expected: the next call may miss. Later calls can hit again on the new prefix.

---

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not add a caching screen, a caching tile, or a caching setting, because D1 forbids a new UI.
- The system must not keep two Anthropic-protocol connectors, because D3 says to adjust the official one instead.
- The system must not send Bedrock cache checkpoints, because D2 postpones Bedrock (#1323).
- The system must not change how skill `allowed-tools` is parsed, because D11 puts that outside this feature.
- The system must not add per-area log levels, because that is #1324.
- The system must not send every allowed tool from the first turn. D5 says to record token counts or token estimates and decide that later. A byte count is not that record.
- The system must not turn on OpenAI's explicit breakpoint mode. Implicit caching plus a stable prefix is the OpenAI behaviour. Explicit mode adds a write charge on current models and is not in D6 (D6 is the Anthropic lifetime).
- The system must not reintroduce a USD daily cap or the deleted app-level token budget, because D13 forbids a new limit and both were removed on purpose (ADR-053 D12 and the 2026-09-14 update).
- The system must not put message text, tool arguments, credentials, or a raw usage object in the per-call log. The line carries the documented numeric counters and the correlation ids only (D20).
- The system must not freeze trimmed history or refuse a model switch in order to protect a cache.
- The system must not weaken the goal-setting tool narrowing in order to keep the tool list stable.

### Machine-Verifiable Constraints

**Usage report (`GET /api/v1/stats/tokens`):**

- The existing success response stays HTTP 200.
- An unrecognized `period` stays HTTP 400, unchanged.
- Each agent row and each provider row includes `cache_hit_ratio` as a number from 0 to 1 inclusive, or null when `tokens_in + tokens_cache_read + tokens_cache_write` is 0.
- `cache_hit_ratio` equals `tokens_cache_read / (tokens_in + tokens_cache_read + tokens_cache_write)` on rows where the denominator is positive. `tokens_in` is uncached input, which is what the assistant-turn write path already stores.
- A new `by_provider` object uses the same cell shape as `by_model`. Keys are catalog provider ids.
- `SessionStats` gains optional `unpriced_tokens` and optional `subscription_tokens` (each an integer, minimum 0). `unpriced_tokens` counts only the unpriced portion of a call, not the whole call. `cost` remains the sum of priced portions only. A session with `cost` 0 and `unpriced_tokens` greater than 0 is unpriced, not free. A session with `subscription_tokens` greater than 0 recorded no dollars for those tokens.
- Each stored assistant accounting entry gains `provider_id`, `model_id`, `billing_class` (`priced`, `unpriced`, or `subscription`), `unpriced_tokens`, and the 5-minute and 1-hour write counts when the provider reported them. `unpriced_tokens` is the unpriced portion only. A mixed call stays `priced` and stores that portion here. `subscription` stores tokens and no dollars. Reload reads these fields. It does not reclassify them from a newer catalog. `by_provider` is summed from those entries.
- A usage object that reports input 0, output 0, cache read 800, and cache write 200, with total 1,000, is a known split. Session, model, and provider totals stay 0 / 0 / 800 / 200 / 1,000 after reload. An all-read case (0 / 0 / 1,000 / 0 / 1,000) is the same kind of split. A missing usage object is not a known zero split and is not backfilled.

**Log line:**

- Exactly one line per completed model call, at info.
- Fields present: provider id, model id, uncached input, cache read, cache write, and when the provider split them, 5-minute write and 1-hour write.
- The call's money result is a dollar cost of the priced portions, or the word `subscription` for a ChatGPT sign-in call (no dollars). A portion with no price is marked `unpriced` on its own and does not erase a dollar cost on the rest of the call. A call with no priced portion and no subscription mark is `unpriced` overall.
- The line includes the documented numeric counters and the correlation ids, and no other part of the provider response.

**Scope boundaries:**

- The system does not add a fifth cache mark. Anthropic allows four.
- The system does not send a cache field that the provider table marks "do not send".
- The system does not add prices inside this repository. Prices arrive only by updating the embedded catalog snapshot from a catalog release that contains them.

**Data constraints:**

- Catalog prices are US dollars per 1 million tokens, which is the unit models.dev publishes (`claude-haiku-4-5` input `1` matches that model's published $1 per million input tokens, and its `cache_write` of `1.25` is 1.25 times that input).
- A cache price of `0` in the catalog is a real zero. An absent cache price is missing.
- Normalized total tokens equal uncached input plus cache read plus cache write plus output, for every adapter fixture.

### Conservative type design

Do not add a new nominal type for a token count or a dollar amount. A count is an integer. A dollar amount is a decimal number. A missing hit percent is null, not a sentinel string. The word `unpriced` is a log token and a boolean-or-count on the stored stats (`unpriced_tokens`), not a new currency type.

---

## Prerequisites

- **Hardware / OS**: The platforms Omnipus already supports (Linux, macOS, Windows). No new platform.
- **Required runtimes**: The repository's existing Go and Node toolchains. No new runtime.
- **Required services**: None for CI. Live runs need outbound HTTPS to the named providers and a credential the operator already uses.
- **Network assumptions**: CI tests use a local fake server. Live runs are manual and recorded.
- **Accounts / credentials**: The five D22 accounts only: OpenRouter, the Z.AI/GLM coding plan, the MiniMax coding plan, the Moonshot coding plan, and OpenAI through the ChatGPT subscription. No other provider account is required.

---

## Development Setup

No new local setup. Implementers use the existing worktree, the existing contract regeneration, and the catalog repository's existing assemble pipeline. Tests must use a temporary home directory, never the operator's real Omnipus home.

**Expected first-run behaviour**: Not a user-facing install change. After the catalog snapshot that contains prices is embedded, a priced model call records a non-fallback cost.

**Common first-run failures**: A catalog snapshot without cost fields makes every call unpriced. That is the honest result, not a fallback to the deleted rate table.

---

## Tech Stack

| Category | Choice | Version / Pin | Source |
|---|---|---|---|
| Language | Go | `go 1.26.9` in `go.mod` | This repository |
| Catalog pipeline | The existing TypeScript assemble in `elicify-ai/omnipus-provider-catalog` | schema 2.0.0, extended with cost | Catalog `src/schema.ts` |
| Price source | models.dev primary, LiteLLM cross-check | USD per 1 million tokens | D7; models.dev `Cost` object |
| Anthropic connector | Official `anthropic-sdk-go` already in `go.mod` | The version already required | D3 |
| Test framework | `go test` for the fake-provider suite; the existing Usage screen tests if the screen shows the percent | — | D9, D22 |
| Datastore | Existing session files | — | No new store |

---

## Deployment / Runtime

- **Target environment**: The same single binary as the rest of Omnipus.
- **Online / offline**: Cache markers and keys are sent only on a real provider call. Offline and local hosts (Ollama, vLLM) get no marker. Their usage is parsed when they report it.
- **Resource limits**: No new process, no new cache stored by Omnipus. The provider holds the cache. RAM overhead must stay inside the existing footprint limit.
- **Start / stop commands**: None.
- **Health check**: None new. A zero cache read is not a health failure.
- **Logs / telemetry**: One info line per call, as FR-001. No new telemetry channel.

---

## Integration Boundaries

### Catalog repository (`elicify-ai/omnipus-provider-catalog`)

- **Data in**: models.dev `cost` on each model (`input`, `output`, optional `reasoning`, `cache_read`, `cache_write`, optional `tiers[]` with `tier.type = context` and `tier.size`, optional `context_over_200k`). A tier boundary keeps the source's own meaning: Anthropic "up to N / over N" prices exactly N at the lower rate even when `tier.size` is N. A source that includes N in the higher band keeps that meaning. The catalog does not flatten every `tier.size` to one inclusive or exclusive rule. LiteLLM's per-token prices, converted to per million before compare. Optional follow-up field `cache_write_1h`, same unit, for a 1-hour write. The first publication that adds prices does not have to include `cache_write_1h`. When it is present, Omnipus uses it. When it is absent, Omnipus multiplies the catalog input price by the provider's documented 1-hour factor (Anthropic: 2) and does not store that factor as a dollar rate. A provider with no documented factor leaves the 1-hour tokens unpriced.
- **Data out**: The published catalog gains those fields on each model. The embedded snapshot in this repository is updated from that publication. This repository does not type prices in by hand.
- **Contract**: The catalog schema is strict. Adding fields means extending `Model` in the catalog schema and the Go `Model` struct together. A model with no `cost` in models.dev is published without a price, not with zeros.
- **On failure**: If models.dev and LiteLLM disagree by more than the catalog's existing 5 percent relative tolerance, the row is disputed under the catalog's existing dispute rule. The 4,096 absolute tolerance is for token counts and is not applied to dollars. If LiteLLM has no price, models.dev wins. If models.dev has no price and LiteLLM has one, that is a fill, same as a missing context window today.
- **Development**: The catalog's own tests. Omnipus tests use a fixture catalog, not a live fetch.

### Anthropic-protocol hosts

- **Data in**: Official Messages request. Cache marks as in User Story 2. Base URL and timeout of the catalog row are passed through, which the official constructor does not do today and must grow to do.
- **Data out**: `cache_read_input_tokens`, `cache_creation_input_tokens`, and when present `cache_creation.ephemeral_5m_input_tokens` and `cache_creation.ephemeral_1h_input_tokens`. `input_tokens` excludes cache tokens.
- **On failure**: HTTP errors stay errors. A 200 with zero cache fields is a miss. If a MiniMax, Moonshot, or Z.AI coding-plan call rejects the official body, this one connector is adjusted. DeepSeek is a documented fixture, not a live gate. A second connector is not added.
- **Development**: Fake HTTP server in CI. One recorded live run at landing.

### OpenAI-compatible hosts, including the ChatGPT sign-in path and the Gemini and Ollama rows

- **Data in**: The existing chat-completions or responses body, plus a cache key or header only where the provider table says so. No `cache_control` except where the table says so.
- **Data out**: Whichever of the listed usage fields the host returns. The parser accepts all of them.
- **On failure**: A host that responds 400 or 422 to a cache field is a failed live check for that allow-list entry. The field is removed for that host. The call is not retried forever inside a user turn as a silent fallback; the landing check is what removes it.
- **Development**: Fake server per usage shape.

### Bedrock

- **Data in / out**: Unchanged.
- **On failure**: Not exercised for caching.
- **Development**: Existing Bedrock tests keep passing. No new cache fixture is required for landing.

### Usage report API

- **Data in**: The existing period query.
- **Data out**: Existing token fields, plus `cache_hit_ratio`, `by_provider`, and `unpriced_tokens` as specified under machine-verifiable constraints.
- **On failure**: Existing 400 for a bad period. A store that fails to load still sets the existing `partial` flag.
- **Development**: Handler tests against the generated types. No hand-written wire struct.

---

## Provider table

Ranks and catalog ids are the fifty in the research. "What we send" is this feature's decision, not every field the vendor accepts. Where the table says a cache key or a session header, the value is the session id (D16), never the agent id, and never a secret. Prices used for accounting come from the catalog (models.dev, USD per 1 million tokens), not from the vendor page. The vendor page is the source for the mechanism and the usage field names. "Included" means the provider's own prompt total already contains the cached tokens; the normalized prompt count must still be uncached-only.

Hosts the factory cannot call today (unsupported tier, or a blank protocol) are marked "no call". This feature does not make them callable.

| Catalog id | Mechanism | What we send | Usage fields to read | Prompt total includes cache? | Minimum prefix | Lifetime | Price source |
|---|---|---|---|---|---|---|---|
| `openai` | Implicit. Explicit breakpoints exist on the newest models and are not used. | `prompt_cache_key` (already sent). | Responses: `usage.input_tokens_details.cached_tokens` and `cache_write_tokens`. Chat field names were not in OpenAI's own caching guide. | Yes, on the documented responses formula. | 1,024 tokens on the newest models; varies earlier. | About 30 minutes on the newest; shorter or up to 24 hours earlier. | Catalog |
| `anthropic` | Explicit marker. | Tool list and stable instructions at 1 hour; conversation tail at 5 minutes. | `cache_read_input_tokens`, `cache_creation_input_tokens`, `cache_creation.ephemeral_5m_input_tokens`, `cache_creation.ephemeral_1h_input_tokens`. | No. | 512, 1,024, 2,048, or 4,096 by model. Below that, success with zeros. | 5 minutes or 1 hour, as marked. | Catalog `cache_write` for 5-minute writes. 2× catalog input for 1-hour writes until `cache_write_1h` is published. |
| `azure` | Azure OpenAI caches; other families unknown. | No call. The catalog row is unsupported and the factory has no Azure branch. | If a response is ever parsed by the shared OpenAI parser: `prompt_tokens_details.cached_tokens` and `cache_write_tokens`. | The published example puts the read inside `prompt_tokens`. Writes not shown. | 1,024. | 5–10 minutes, or up to 24 hours. | Catalog, when the row is callable. |
| `google-vertex` | Implicit and explicit resource. | No call. Unsupported, blank protocol. | Native field `cachedContentTokenCount`. Not our wire. | Not stated. | 2,048 to 6,144. | Implicit up to 24 hours. | Catalog, when callable. |
| `amazon-bedrock` | Explicit checkpoint and implicit. | Nothing new. Postponed (#1323). | Not parsed by this feature. | Converse excludes. | Per model, 512 to 4,096. | 5 minutes or 1 hour. | Out of scope. |
| `google` | Implicit on Gemini 2.5 and newer. The OpenAI-compatible endpoint's field is not documented. | Nothing. The row already uses the OpenAI-compatible client. | Read `prompt_tokens_details.cached_tokens` if present, and `usage.total_cached_tokens` or `cachedContentTokenCount` if present. Do not fail if absent. | Not stated. | 2,048 or 4,096. | Not stated. | Catalog |
| `google-vertex-anthropic` | Anthropic marker on Vertex. | No call. Unsupported, blank protocol. | Claude usage object, per Anthropic's page. | Excludes, if that object is what returns. | Anthropic's table. | 5 minutes or 1 hour. | Catalog, when callable. |
| `mistral` | Automatic, key raises the chance of a hit. | `prompt_cache_key`. | `usage.prompt_tokens_details.cached_tokens`. Omitted or 0 on a miss. No write field. | Yes. | 64-token blocks. | Not stated. | Catalog |
| `cohere` | No caching page. | Nothing. | None published. | Unknown. | Unknown. | Unknown. | Catalog if present, else unpriced. |
| `xai` | Automatic. Chat uses a header. Responses uses a key. | Header `x-grok-conv-id` on the chat path this client uses. | Chat: `prompt_tokens_details.cached_tokens`. | Yes. A full hit equals the prompt total. | Not stated. | No fixed lifetime. | Catalog |
| `nvidia` | Hosted API: no contract. Self-hosted NIM is a different product. | Nothing. | None on the hosted API. | Unknown. | Not a token count. | Not stated. | Catalog if present, else unpriced. |
| `meta` | Automatic. Optional key. | `prompt_cache_key`. | Chat: `prompt_tokens_details.cached_tokens`. Responses: `input_tokens_details.cached_tokens`. | Yes, as a subset. | Not stated. | In memory, or a 24-hour hint on responses. | Catalog |
| `llama` | The caching page describes `api.meta.ai`, not the catalog host `api.llama.com`. | Nothing. | Unknown for this host. | Unknown. | Unknown. | Unknown. | Catalog if present, else unpriced. |
| `nova` | Direct host: no contract found. Bedrock Nova is a different API and is postponed. | Nothing. | Unknown on the direct host. | Unknown. | Unknown. | Unknown. | Catalog if present, else unpriced. |
| `deepseek` | Automatic disk cache. `prompt_cache_key` is ignored. | Nothing. | Chat: `prompt_cache_hit_tokens`, `prompt_cache_miss_tokens`, and `prompt_tokens_details.cached_tokens` (same number as the hit). Do not add them. | Yes. Hit plus miss equals `prompt_tokens`. | Not stated. | Hours to days, not fixed. | Catalog |
| `alibaba` | Implicit by default. Explicit marker turns implicit off and is the cheaper read. | `cache_control` `ephemeral` on content blocks, at most four. Covered by a documented fixture, not a live call (D22). | Chat: `usage.prompt_tokens_details.cached_tokens` and `usage.prompt_tokens_details.cache_creation_input_tokens`. Messages: `usage.cache_creation_input_tokens`. The callable row is the chat path. | OpenAI-compatible samples include both inside `prompt_tokens`. | 1,024. | Explicit 5 minutes. | Catalog. Do not hard-code the vendor's 10% or 20%. |
| `alibaba-cn` | Same as `alibaba`. | Same marker. | Same full paths as `alibaba`. | Same include rule. | 1,024; 512 for some partner models. | Explicit 5 minutes. | Catalog |
| `zai` | Automatic. | Nothing. | `prompt_tokens_details.cached_tokens`. | Yes. | Not stated. | Not a number. | Catalog |
| `zhipuai` | Automatic. | Nothing. | `prompt_tokens_details.cached_tokens`. | Yes in the published example. | A recommendation of about 500, not a hard floor. | Not a number. | Catalog |
| `moonshotai` | Automatic. Sends a 5-minute write even when the request has no cache field. A breakpoint inside the message is rejected. | Nothing. Do not send `prompt_cache_breakpoint`. | Chat: `cached_tokens` and `cache_write_tokens` under `prompt_tokens_details`. | Yes. Read, write, and the rest sum to the prompt total. | One full block; block size not stated. | 5 minutes unless a top-level option asks for 1 hour. We do not send that option. | Catalog |
| `moonshotai-cn` | Same as `moonshotai`. | Nothing. Same rejection rule. | Same chat fields, plus a top-level `cached_tokens` that repeats the read. | Yes. | Same. | Same. | Catalog |
| `minimax` | Anthropic protocol. Automatic on some models, explicit marker on others. | That model's documented mode only. Automatic where documented. Explicit 5-minute marks where documented. No 1-hour mark. The coding-plan account is one of the five live runs (D22). | Anthropic read, write, and `input_tokens`. | Anthropic: no. | Automatic: 512. | Explicit: 5 minutes. | Catalog |
| `minimax-cn` | Same family. The China guide's examples use a different host than the catalog base. | Same as `minimax`. | Same. | Same. | 512 for automatic. | 5 minutes for explicit. | Catalog |
| `snowflake-cortex` | OpenAI models implicit; Claude needs a marker; other families none. | No call while the row is unsupported. | Chat: `prompt_tokens_details.cached_tokens` (reads only on OpenAI, reads plus writes on Claude). | Not stated. | 1,024 for OpenAI. | Claude 5 minutes. | Catalog, when callable. |
| `databricks` | Claude needs a marker on an OpenAI-shaped body. | No call while the row is unsupported. | `usage.cache_read_input_tokens` and `usage.cache_creation_input_tokens` on the OpenAI-shaped body. | Not stated. | Not stated. | Not stated for Claude. | Catalog, when callable. |
| `watsonx` | No contract found. | Nothing. | None. | Unknown. | Unknown. | Unknown. | Unpriced unless the catalog has a price. |
| `sap-ai-core` | No contract found. | Nothing. | None. | Unknown. | Unknown. | Unknown. | Unpriced unless the catalog has a price. |
| `fireworks-ai` | Automatic. | `prompt_cache_key`. Do not send `perf_metrics_in_response`. | Only `usage.prompt_tokens_details.cached_tokens` (D21). Do not read a header and do not read `perf_metrics`. | Same include rule as other OpenAI-style hosts when that field is present. | Not stated. | Minutes to hours. | Catalog |
| `togetherai` | Automatic. | `prompt_cache_key`. | `prompt_tokens_details.cached_tokens` or top-level `usage.cached_tokens`. Read whichever is present, not both if they repeat. | Yes. | Varies by model. | About an hour, best effort. | Catalog |
| `groq` | Automatic. Cannot be turned off. Not every model. | Nothing. | `prompt_tokens_details.cached_tokens`. | Yes. | 128 to 1,024 by model. | 2 hours idle. | Catalog |
| `cerebras` | Automatic. No discount. | `prompt_cache_key`, at most 1,024 characters. | `prompt_tokens_details.cached_tokens`. | Yes. | 128-token blocks. | At least 5 minutes, up to 1 hour. | Catalog. A cached token billed at the normal input price is correct when the catalog says so. |
| `baseten` | Automatic. | Header `x-session-affinity`. | Chat: `prompt_tokens_details.cached_tokens`. Messages: `cache_read_input_tokens`, omitted on a miss. | Messages: no. Chat: not stated. | Not stated. | Not stated. | Catalog |
| `deepinfra` | Automatic, plus an opt-in retention mode. | `prompt_cache_key` only. Do not send retention options. | `prompt_tokens_details.cached_tokens`. Retention also returns `cache_write_tokens`; ignore a write unless we sent retention, which we do not. | Yes. | Not a fixed number for automatic. | Not published for automatic. | Catalog |
| `huggingface` | Router. The cache is the upstream's. | Nothing. | No completion field documented. | Unknown. | The upstream's. | The upstream's. | Catalog pass-through if present, else unpriced. |
| `openrouter` | Router. Claude and Qwen need a marker. Most others are automatic. | Body `session_id` (sticky routing) and `cache_control` ephemeral, which OpenRouter translates. | Chat: `prompt_tokens_details.cached_tokens` and `cache_write_tokens`. | The published example includes the read in `prompt_tokens`. | Depends on the upstream. | Routing stickiness 10 minutes. Cache lifetime is the upstream's. | Catalog |
| `perplexity` | The orchestrator route accepts cache fields and can reject unknown fields. The catalog base has no path. | Nothing, unless the request URL is exactly the documented orchestrator path. It is not, on the catalog row. | If that path is ever used: `prompt_tokens_details.cached_tokens` and `cache_write_tokens`. | Yes, on that path's description. | Not stated. | The published schema disagrees with itself (`30m` versus `5m` / `1h` / `24h`). | Catalog |
| `cloudflare-workers-ai` | Automatic on some models. | Header `x-session-affinity` when a call is actually made. | The caching page says the usage object and never names the key. Read the OpenAI-compatible cached-token field if present. | Not stated. | No number. | Not stated. | Catalog |
| `scaleway` | Automatic. | Nothing. `prompt_cache_key` is documented as unsupported. | `prompt_tokens_details.cached_tokens` or `input_tokens_details.cached_tokens`. | Not stated as a rule. | Not stated. | Not stated. | Catalog |
| `ovhcloud` | No cache product on the pages opened. | Nothing. Do not send `prompt_cache_key`. | None. | Unknown. | Unknown. | Unknown. | Unpriced unless the catalog has a price. |
| `nebius` | No customer cache product. The schema has a field with no description. | Nothing. | `prompt_tokens_details.cached_tokens` if present. A zero is not proof of a real miss. | Not stated. | Unknown. | Unknown. | Catalog if the price is present. A zero cached-token count is still recorded. |
| `volcengine` | Implicit, cannot be turned off. Explicit mode is a different API. | `prompt_cache_key`. Do not send the explicit caching object. | `prompt_tokens_details.cached_tokens`. | The billing formula treats input as including cached tokens. | 256 to 8,960 by model. Default 1,024. | Not fixed. | Catalog |
| `upstage` | A cache price and a responses field exist. No request marker was found. | Nothing. | Responses: `input_tokens_details.cached_tokens`. Chat reference has no cache field. | Not stated. | Unknown. | Unknown. | Catalog |
| `sarvam` | Priced cache. Optional session header. | Header `x-session-affinity`. | `prompt_tokens_details.cached_tokens`. The details object is null on a miss. | Not stated as a formula. The text does not say excluded. | Unknown. | Unknown. | Catalog |
| `siliconflow-cn` | Automatic on supported models. | Nothing. | `prompt_cache_hit_tokens`, `prompt_cache_miss_tokens`, and `prompt_tokens_details.cached_tokens`. The English schema for the other host omits them. | The China example has prompt total equal to hit plus miss. | Unknown. | Unknown. | Catalog |
| `stackit` | None documented. | Nothing. | None. | Unknown. | Unknown. | Unknown. | Unpriced unless the catalog has a price. |
| `infomaniak` | None documented. | Nothing. | None. | Unknown. | Unknown. | Unknown. | Unpriced unless the catalog has a price. |
| `poolside` | Hosted API: none. Self-managed metrics are the engine's, not a response field. | Nothing. | None on the hosted example. | Not stated. | Unknown. | Unknown. | Unpriced unless the catalog has a price. |
| `vercel` | Router. With no `caching` field the gateway passes the body through. Implicit upstreams still cache. Claude does not, unless the body carries a manual mark. | Session pin `prompt_cache_key`. Do not send `caching: auto`. For a Claude model id, also send `cache_control` `{ "type": "ephemeral" }` on the last history message before the volatile tail. Implicit upstreams on this row (OpenAI, Google, DeepSeek) get no `cache_control`. No second connector. | Whichever upstream field the gateway forwards: chat cached tokens, responses cached tokens, or Anthropic's read field. | Not stated. | The upstream's. | The pin is at most 1 hour. The manual mark is Anthropic's ephemeral mark. | Catalog, upstream rates. |
| `vllm` | Automatic prefix cache inside the process. | Nothing. Do not send `cache_salt`. | `prompt_tokens_details.cached_tokens` only when the server has details enabled. | `prompt_tokens` is the full prompt, counted separately. | One full block. | Process lifetime, LRU. | Not a bill. Unpriced unless a catalog price was published. |
| `ollama` | Automatic inside the loaded runner. | Nothing. | OpenAI-compatible `prompt_tokens_details.cached_tokens` when the runner reports a cache count. | Yes. The prompt total includes the cached count. | Unknown. | Lives in the runner. Default model keep-alive is 5 minutes and is not a cache lifetime we set. | Not a bill. Unpriced unless a catalog price was published. |

Every other catalog row that the factory already calls, and that is not in this table, is treated as OpenAI-compatible or Anthropic-protocol by the factory branch it already uses. An Anthropic-protocol row gets a mark only when that host documents one: native Anthropic gets the 1-hour tool and instruction marks and the 5-minute history mark; Moonshot gets none; MiniMax gets its documented 5-minute or automatic mode and no 1-hour mark. OpenAI-compatible rows get no extra field unless this table lists one. All of them are parsed with the full field list. A row the factory cannot construct is unchanged.

---

## Reachability

A person can see caching without a new screen.

| Surface | What they see | When it is available |
|---|---|---|
| The log, one line per model call | Provider, model, agent, session, call, numeric counters, and a dollar cost, `unpriced`, or `subscription` | Info. Hidden while the shipped level stays `warn` (D15). Not raised to warn. How to raise the level is documented in `docs/operations/observability.md`. |
| Usage screen, the screen `docs/settings.md` already describes | Cache-hit percent beside the cached and uncached totals it already shows, per agent and per provider. No new screen. Still no dollar total, matching the current sentence that usage counts tokens, not money. | Default install (D15). |
| `GET /api/v1/stats/tokens` | The same percent, the provider breakdown, `unpriced_tokens`, and `subscription_tokens` | Existing route |

The feature is not done when only the tests pass. It is done when one of the five D22 runs shows a second-call cached-token count greater than zero on the Usage screen, and the info log line exists for people who raise the log level.

The existing Usage screen (`src/components/screens/UsageScreen.tsx`, route `src/routes/_app/usage.tsx`) gains no new control. It adds a provider breakdown on the existing catalog `Tabs`, and a text label "Cache hit" beside the cache totals it already shows. `0.8` is shown as `80%`, not `0.8%`. A null ratio shows no percent, not `0%`. "Cached" stays the existing read-plus-write total. "Cache hit" is reads divided by full input. Loading, empty, error, and partial stay the screen's existing states. An error is not an empty list. Changing the period keeps focus on the selected tab. The provider tab uses the same keyboard behaviour as the current tabs. On a narrow screen the new column wraps like the existing breakdowns and does not hide the percent. Mounted tests cover `80%`, a null ratio, the provider rows, a period change, and the existing loading, empty, error, and partial states. `npm run typecheck` is the type gate. The design-system check applies because the screen reuses catalogued tabs and adds no control.

User-facing documentation in the same change, drafted by the implementing lead and checked by the docs verifier:

| Page | What the draft must add |
|---|---|
| `docs/settings.md` | The Usage paragraph: hit percent, provider breakdown, `80%` versus a missing percent, no new page, tokens not dollars. A ChatGPT sign-in call is counted in tokens and is not given a dollar cost. |
| `docs/operations/observability.md` | How to raise the existing log level from warn to info. The accounting line's fields and one example. The hit-ratio formula. What `priced`, `unpriced`, and `subscription` mean, including a mixed call. The line stays hidden at warn. |

---

## Contract changes

Constraint #8. The architect sets the shape. The backend lead edits the schema files, references them from `contracts/openapi.yaml`, runs `scripts/gen-contracts.sh`, and commits the generated diff with that schema change. Writers, the usage report, and the Usage screen use the generated types only, and only after that regeneration. No hand-written wire type. This step comes before those writers in the implementation order.

| Schema | Change |
|---|---|
| `contracts/components/schemas/ModelTokens.yaml` | Add optional `cache_hit_ratio`, number, 0 to 1, or null when the denominator is 0. |
| `contracts/components/schemas/AgentTokenEntry.yaml` | Add the same `cache_hit_ratio`. |
| `contracts/components/schemas/TokenUsageSummary.yaml` | Add optional `by_provider`, same cell shape as `by_model`, keys are catalog provider ids. Add optional `unpriced_tokens` and optional `subscription_tokens`. |
| `contracts/components/schemas/SessionStats.yaml` | Add optional `by_provider`, same cell shape as `by_model`. Add optional `unpriced_tokens` and optional `subscription_tokens`. `cost` stays required and stays the priced API sum only. Its description must say that zero cost plus a positive `unpriced_tokens` is unpriced, not free, and that `subscription_tokens` are excluded from `cost`. |

No new route. No WebSocket frame change. No AsyncAPI change, unless a session snapshot already embeds `SessionStats`, in which case regenerating both specs is required and no field is added by hand on the socket.

Persisted session stats are JSON the Usage screen reads through these schemas, so the new fields are wire fields, not a private struct.

The per-call producer is the existing assistant transcript entry (`pkg/session/daypartition.go::TranscriptEntry`). It gains `provider_id`, `model_id`, `billing_class` (`priced`, `unpriced`, or `subscription`), `unpriced_tokens`, `cache_write_5m_tokens`, and `cache_write_1h_tokens`. `unpriced_tokens` is the unpriced portion of that call, not the whole call. A mixed call is `priced` and still stores that portion. A fully unpriced call is `unpriced`. A ChatGPT sign-in call is `subscription`, with tokens and no dollars. `cache_write_tokens` stays the sum of the 5-minute and 1-hour counts when the provider reported the split. The screen does not read this entry: a search of `src/` and `contracts/` finds no `cache_write_tokens` or `prompt_tokens` consumer, so these fields are persisted accounting, not a new wire schema. `by_provider` on `SessionStats` and on the usage report is summed from them. Reload reads the stored provider and the stored `unpriced_tokens`. It does not infer the provider from the agent's later configuration, and it does not reprice the call from a newer catalog.

---

## ADR-071 amendment

[ADR-071 — Tool manifest tier redesign](../architecture/ADR-071-tool-manifest-tier-redesign.md) needs a dated amendment before the tool-list behaviour changes. This spec does not edit the ADR.

What the amendment must say:

| ADR-071 today | Amendment |
|---|---|
| §6 D5 puts a static tool catalog inside the cached system prefix and leaves a per-turn "already loaded" line in front of history. That work was deferred to #654. | Superseded for this feature. D4 of the founder decisions moves the tool-summary note behind the conversation. The catalog-inside-the-prefix split is not implemented. |
| §1.1.1: static tools stay for the session; MCP tools expire by TTL and leave the tool list. | Loaded tools, including MCP tools that would have expired, stay in the provider tool list until the session ends. Expiry may still apply to discovery bookkeeping that does not change the bytes of the tool list. |
| §6.4: tool order is already deterministic. | Unchanged. The amendment restates it as a requirement this feature locks with a test. |
| §6.6: D5 ships behind a live cache-read gate. | That gate is not this feature's gate. This feature's live gate is the five D22 runs. |

The amendment also records the non-decision: sending every allowed tool from the start of the session is not decided. The feature records a labelled token count or token estimate of the tool list and of the skill text per agent, names the estimator, and stops there. A byte count does not satisfy that record.

D5 of the founder decisions is the authority for the amendment. ADR-071 is amended; it is not silently left describing the old TTL behaviour.

---

## BDD Scenarios

### Feature: Prompt caching

#### Scenario: A completed call writes one accounting line

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a model call that finished with 100 uncached input tokens, 800 cache-read tokens, 50 cache-write tokens, and a catalog price
- **When** the call is accounted
- **Then** the log contains one info line with those three token counts, the provider id, the model id, the agent id, the session id, the call id, and a dollar cost
- **And** the line does not contain the prompt text or a raw usage object

#### Scenario: The usage report shows a hit percent per agent and per provider

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** an agent made calls through two providers in the selected period
- **And** the normalized tokens are 100 uncached, 800 cache-read, and 100 cache-write
- **When** the usage report is requested
- **Then** that agent's cache-hit ratio is 0.8
- **And** each provider row uses the same formula on its own tokens

#### Scenario: A row with no input tokens has no hit percent

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Edge Case

- **Given** an agent with no input, cache-read, or cache-write tokens in the period
- **When** the usage report is requested
- **Then** the cache-hit ratio is null
- **And** it is not 0

#### Scenario: No new caching screen exists

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** the app's navigation
- **When** a person looks for a caching page or a caching setting
- **Then** the only caching numbers are on the existing Usage screen and in the log

#### Scenario: Anthropic-protocol traffic uses the official connector

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a provider whose catalog protocol is Anthropic
- **When** a call is built
- **Then** the official connector builds the request
- **And** the hand-written Anthropic connector is not part of the build

#### Scenario: Anthropic marks tools and instructions for one hour and the tail for five minutes

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a native Anthropic request with a tool list, stable instructions, and a conversation
- **When** the Anthropic body is produced
- **Then** the tool list and the stable instructions are marked for 1 hour
- **And** the conversation tail is marked for 5 minutes
- **And** both 1-hour marks appear before the 5-minute mark
- **And** there are at most four marks
- **And** a Moonshot body has no cache mark and no 1-hour option
- **And** a MiniMax body uses its documented 5-minute or automatic mode and no 1-hour mark

#### Scenario: A streamed Anthropic call still reports cache tokens

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** a streamed Anthropic response whose opening event carries a cache read
- **When** the stream completes
- **Then** the recorded cache read equals that event's count

#### Scenario: A third-party rejection does not revive a second connector

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Error Path

- **Given** a live check where one Anthropic-protocol host rejects the official body
- **When** the connector is changed so that host accepts the call
- **Then** the tree still contains one Anthropic-protocol connector

#### Scenario: Volatile notes sit behind the conversation

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a session whose scratchpad and clock differ from the previous turn
- **When** the next request is built
- **Then** the captured body, after normalization and after adapter conversion, matches through the previous turn's history
- **And** the clock, tool-summary note, scratchpad note, and goal-checklist note are one user-role block after that history and after any completed tool-call group
- **And** they are not a system-role message
- **And** the web-display note and the workspace instructions appear before that history

#### Scenario Outline: The same tool set always serializes the same way

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a tool set presented in order `<order>`
- **When** the provider tool list is built twice
- **Then** both serializations are byte-identical
- **And** they match the serialization of `<canonical>`

**Examples**:

| order | canonical |
|---|---|
| alphabetical | alphabetical |
| reversed | alphabetical |
| shuffled | alphabetical |

#### Scenario: A loaded tool is still listed after time passes

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a tool whose definition was added to the session tool list
- **When** further turns pass that would previously have expired it
- **Then** the definition is still in the list and the tool still runs
- **And** a second open session of the same agent does not list it and cannot run it
- **And** a Deny, a removed assignment, or a disconnect removes it
- **And** the list's bytes do not change merely because time passed

#### Scenario: The advertised skill list stays fixed and the reminder moves to the tail

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a session that advertised a skill list on its first turn
- **And** the skill files on disk change before the next turn
- **When** the next request is built
- **Then** the advertised list matches the first turn
- **And** the per-turn skill reminder is after the conversation
- **And** a labelled token count or token estimate of the tool list and of the skill text was recorded for the agent, naming the estimator

#### Scenario: Goal-setting narrowing still replaces the tool list

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** a turn that must offer only the goal-setting tools
- **When** the request is built
- **Then** the tool list is that narrow list
- **And** the following ordinary turn uses the session tool list again

#### Scenario: A new session does not inherit the previous session's loaded tools

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Error Path

- **Given** a session that loaded a tool and then ended
- **When** a new session for the same agent builds its first request
- **Then** that tool is not in the tool list merely because the previous session loaded it

#### Scenario Outline: Each published usage shape normalizes to uncached prompt plus cache read plus cache write

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a fake `<shape>` response with the fixture counts
- **When** it is parsed
- **Then** cache read is `<read>`
- **And** cache write is `<write>`
- **And** uncached prompt is `<uncached>`
- **And** total equals uncached plus read plus write plus output

**Examples**:

| shape | uncached | read | write |
|---|---|---|---|
| Anthropic exclusive input | 100 | 800 | 50 |
| Anthropic 5-minute and 1-hour writes | 100 | 0 | 40 and 60 stored separately, write total 100 |
| OpenAI chat details, read inside prompt total | 200 | 800 | 0 |
| OpenAI responses details, read and write inside input total | 100 | 700 | 200 |
| DeepSeek hit and miss, nested cached tokens repeating the hit | 300 | 700 | 0 |
| Moonshot chat, read and write inside prompt total | 100 | 800 | 100 |
| Alibaba chat, nested `usage.prompt_tokens_details.cached_tokens` and `usage.prompt_tokens_details.cache_creation_input_tokens` | 100 | 800 | 100 |
| Fireworks, only `usage.prompt_tokens_details.cached_tokens` | 100 | 900 | 0 |
| Together top-level cached tokens repeating the nested field | 100 | 900 | 0 |
| Gemini total cached tokens field, include rule unknown | recorded as read, prompt left as the provider reported it when the include rule is unknown | the reported cached count | 0 |
| Ollama cached count inside prompt total | 100 | 50 | 0 |
| Missing cache fields | the prompt total | 0 | 0 |

The Gemini row is the one shape whose include rule is not stated by the documented example. The parser must not subtract the cached count from the prompt total. Gemini is not a live check (D22). Subtracting would invent a rule. The Alibaba fixture is the nested chat JSON, not a top-level `cache_creation_input_tokens`. The Fireworks fixture does not include a header or a `perf_metrics` object.

#### Scenario: A cache key is sent only to hosts that accept it

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the provider table's send column
- **When** requests are built for a host that lists a key or a header, and for Scaleway, and for OVHcloud
- **Then** the first request carries that key or header, and the value is the session id
- **And** the value is not the agent id
- **And** the Scaleway and OVHcloud requests do not carry `prompt_cache_key`

#### Scenario: An implicit host receives no marker

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** Groq, which caches without a field
- **When** a request is built
- **Then** the body contains no cache marker and no cache key

#### Scenario: An unknown usage field does not fail the call

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Error Path

- **Given** a successful response whose usage object has no field this spec names
- **When** it is parsed
- **Then** the call still completes
- **And** cache read and cache write are zero
- **And** the prompt count is the provider's prompt total

#### Scenario: Bedrock is unchanged

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** a Bedrock model
- **When** a request is built and a response with Bedrock cache fields is parsed by this feature's new parser
- **Then** the request body is the same shape as before this feature
- **And** those Bedrock fields are not required for the call to succeed

#### Scenario: A priced call uses the catalog and a cache read is not free

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a fixture price of $3 per million input, $0.30 per million cache read, $3.75 per million cache write, and $15 per million output
- **And** a call with 1,000 uncached, 8,000 cache-read, 2,000 cache-write, and 500 output tokens
- **When** the cost is computed
- **Then** the cost is $0.003 + $0.0024 + $0.0075 + $0.0075
- **And** no hand-kept rate table exists to consult

#### Scenario: A missing input price is unpriced, not the old fallback

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Error Path

- **Given** a model with no input price in the catalog
- **When** the call is accounted
- **Then** the tokens are stored
- **And** the log says unpriced
- **And** the cost is not $3 per million input
- **And** the usage stats record the tokens as unpriced rather than as a free zero

#### Scenario: A ChatGPT sign-in call is marked subscription

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** a ChatGPT sign-in call whose model has a catalog API price
- **When** the call is accounted
- **Then** cached and uncached tokens are stored
- **And** the log says subscription
- **And** no dollar cost is recorded for that call

#### Scenario: A one-hour write is not priced as a five-minute write

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a catalog input price of $1 per million, a `cache_write` of $1.25 per million, and no `cache_write_1h`
- **And** 1,000 tokens of 5-minute write and 1,000 tokens of 1-hour write on Anthropic, whose documented 1-hour write is 2× input
- **When** the cost is computed
- **Then** the 5-minute portion costs $0.00125
- **And** the 1-hour portion costs $0.002
- **And** the two write counts are stored separately
- **And** the log says the 1-hour portion used 2× the catalog input price

#### Scenario Outline: A missing cache price is not the same as a zero cache price

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Edge Case

- **Given** an input price of $2 per million and a cache-read price that is `<cache_price>`
- **And** 1,000 cache-read tokens
- **When** the cost is computed
- **Then** the cache-read portion costs `<dollars>`

**Examples**:

| cache_price | dollars |
|---|---|
| absent | 0.002 |
| 0 | 0 |
| 0.2 | 0.0002 |

#### Scenario Outline: A length band follows the source boundary

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** an Anthropic-style band whose source says "up to 100,000" at the base rate and "over 100,000" at the high rate, and a catalog `tier.size` of 100000
- **When** a call is accounted whose input size is `<tokens>`
- **Then** the price used is `<band>`

Input size is uncached input plus cache read plus cache write. Output is not included. Exactly 100,000 is the base rate. A cache-heavy split is the same total: 100 uncached plus 99,900 read is base; 100 uncached plus 99,901 read is the high rate. Canonical tiers win over a legacy `context_over_200k` alias. The alias alone means over 200,000, so exactly 200,000 stays on the base rate, and it is not evidence of a 512,000 threshold. A source that documents the higher band as including its boundary token keeps that meaning. This scenario does not apply one exclusive rule to every provider.

**Examples**:

| tokens | band |
|---|---|
| 99999 | base |
| 100000 | base |
| 100001 | the over-100,000 rate |

#### Scenario: Fake-provider tests do not need credentials

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** CI with no provider keys
- **When** the caching suite runs
- **Then** it passes on a local fake server
- **And** a second cache-heavy call costs less than the first and more than zero

#### Scenario: Landing requires five live runs

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a build proposed for landing
- **When** the landing evidence is checked
- **Then** it includes one saved run each for OpenRouter, the Z.AI/GLM coding plan, the MiniMax coding plan, the Moonshot coding plan, and OpenAI through the ChatGPT subscription
- **And** each run sent the same conversation twice
- **And** the second call reports cached tokens greater than zero
- **And** the ChatGPT subscription run records tokens and no dollars
- **And** no other provider was called live

#### Scenario: A live HTTP success with zero cached tokens fails the proof

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Error Path

- **Given** one of the five live runs whose second call returns HTTP 200 and zero cached tokens
- **When** the proof is judged
- **Then** it is not a pass

#### Scenario: A cache-only call keeps a known zero split

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Edge Case

- **Given** a stored call whose usage object says 0 uncached input, 0 output, 800 cache read, 200 cache write, and total 1,000
- **When** the entry is appended, aggregated, reported, and reloaded
- **Then** session, model, and provider totals stay 0 input, 0 output, 800 read, 200 write, and total 1,000
- **And** an all-read call of 0 input, 0 output, 1,000 read, 0 write, and total 1,000 stays that split
- **And** a missing usage object is not treated as this known zero split

#### Scenario: A Vercel Claude request carries a manual mark

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** a Vercel model id `anthropic/claude-opus-4.5`
- **When** the request is captured
- **Then** the last history message before the volatile tail carries `cache_control` with type `ephemeral`
- **And** the body does not contain `caching`
- **And** an implicit upstream on the same row does not gain that mark

#### Scenario: The log stays free of prompt text when the provider returns an error body

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Error Path

- **Given** a provider error response that echoes the prompt
- **When** the failure is logged
- **Then** the caching line is either absent or contains no prompt text
- **And** the turn still surfaces the provider error rather than a success with empty usage

---

## Test-Driven Development Plan

### Test hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | Parsers, price arithmetic, prefix comparison, key allow-list | Deterministic, no network |
| Integration | One turn against a fake provider, then the usage report | The bytes, the stored stats, and the report agree |
| E2E | Not a CI browser run | The five D22 live runs are the end-to-end proof, recorded outside CI |

### Test implementation order

Write these before the production change they lock. Order inside a level is dependency order. Names are the tests to add or rewrite. Packages follow existing suites; new code does not grow `pkg/agent/loop.go`, which is already at its size ceiling.

| Order | Test name | Level | Traces to BDD scenario | Description |
|---|---|---|---|---|
| 1 | `TestUsageShapes_Normalize` | Unit | Each published usage shape | Table-driven. Expected counts are the Examples table, written from this spec, not read off the parser. |
| 2 | `TestUsageShapes_DeepSeekHitNotDoubleCounted` | Unit | DeepSeek row | Hit field and nested cached tokens that repeat it produce one read. |
| 3 | `TestUsageShapes_GeminiDoesNotSubtractUntilEvidence` | Unit | Gemini row | A `total_cached_tokens` value is stored as cache read and is not subtracted from prompt. The documented example does not state the include rule, and there is no live Gemini check. |
| 4 | `TestPrice_CatalogArithmetic` | Unit | Priced call | The hand-computed $0.003 + $0.0024 + $0.0075 + $0.0075 case. |
| 5 | `TestPrice_MissingInputIsUnpriced` | Unit | Missing input price | Not the old $3/$15 fallback. Tokens kept. Unpriced flag set. |
| 6 | `TestPrice_MissingCacheUsesInput_ZeroCacheStaysZero` | Unit | Cache price outline | The three rows: absent, zero, and a real cache price. |
| 6a | `TestPrice_SubscriptionRecordsNoDollars` | Unit | ChatGPT sign-in call | Tokens stored, log word `subscription`, cost unchanged. |
| 6b | `TestPrice_OneHourWriteUsesDocumentedMultiplier` | Unit | One-hour write | Anthropic: 5-minute write at `cache_write`; 1-hour write at 2× catalog input when `cache_write_1h` is absent, and at `cache_write_1h` when present. A provider with no documented 1-hour factor leaves that portion unpriced. |
| 7 | `TestPrice_ContextTier` | Unit | Length band | Anthropic up-to-100,000: 99,999 and 100,000 are base; 100,001 is the high rate. |
| 8 | `TestPrice_NoRateTable` | Unit | Priced call | The hand-kept table symbol is gone. |
| 9 | `TestCacheKeyAllowList_SendsAndSuppresses` | Unit | Cache key scenario | Mistral and OpenAI receive a key whose value is the session id, not the agent id. Scaleway and OVHcloud do not. Groq receives neither key nor marker. |
| 10 | `TestToolOrder_IndependentOfInputOrder` | Unit | Tool-set outline | Three input orders, one byte string. |
| 11 | `TestAnthropicBody_MarksAndLifetimes` | Unit | Anthropic marks | Fake server captures the body. At most four marks. 1 hour before 5 minutes. |
| 12 | `TestAnthropicStream_ReadsCacheUsage` | Unit | Streamed Anthropic call | Cache read taken from the opening stream event. |
| 12a | `TestUsageShapes_UnknownFieldDoesNotFail` | Unit | Unknown usage field | Success response, zero cache counts, prompt total kept. |
| 12b | `TestLoadedTool_NotInheritedByNextSession` | Integration | New session does not inherit loaded tools | A tool loaded in a finished session is absent from the next session's first tool list. |
| 13 | `TestPrefixStable_ClockAndScratchpadMove` | Integration | Volatile notes | Two built requests. Clock advanced. Scratchpad changed. Shared history bytes match. Mutation note: putting the seconds clock back in front must fail this test. |
| 14 | `TestLoadedTool_SurvivesTime` | Integration | Loaded tool | A tool that would have expired is still in the second request's tool list. |
| 15 | `TestSkillList_FrozenForSession` | Integration | Skill list | Disk change between turns does not change the advertised list. Reminder is after history. The size record is a token count or token estimate. |
| 16 | `TestGoalNarrowing_RestoresListNextTurn` | Integration | Goal narrowing | Narrow list on the goal turn, session list on the next. |
| 17 | `TestLogLine_UsageOnly` | Integration | Accounting line, and the error-body scenario | One line, required fields, no prompt text, including when the provider errors. |
| 18 | `TestUsageReport_HitRatioAndProvider` | Integration | Hit percent, and the empty-percent scenario | 0.8 on the fixture. Null when the denominator is 0. Provider breakdown present. |
| 19 | `TestSecondCall_CostsLessNotZero` | Integration | Fake-provider suite | First call writes cache, second reads it, second cost is lower and not zero. |
| 20 | `TestConnector_OneAnthropicPackage` | Unit | Official connector, and no second connector | Factory builds the official connector. The hand-written package is absent. |
| 21 | `TestBedrockBody_Unchanged` | Unit | Bedrock unchanged | A Bedrock request fixture matches the pre-feature shape. |
| 22 | `TestPrefixStable_AfterNormalizeAndAdapter` | Integration | Volatile notes | Captures the body after normalization and adapter conversion. Tail is user-role and after history. Tool results stay grouped. |
| 23 | `TestConnector_ReconstructedCallAndVerbatimModel` | Unit | Official connector | Canonical tool call with no convenience name is sent intact. `MiniMax-M2.7` is not rewritten. |
| 24 | `TestDialectBodies_MoonshotAndMiniMax` | Unit | Anthropic marks | Moonshot body has no in-message mark and no 1-hour option. MiniMax uses its documented mode and no 1-hour mark. |
| 25 | `TestProviderId_SurvivesReload` | Integration | Hit percent | Same model id on two providers stays split after reload. Fallback and subscription are separate. A mixed call keeps its stored `unpriced_tokens` after the catalog later gains a price. |
| 26 | `TestLoadedMcpTool_ExecutesOnlyInThatSession` | Integration | Loaded tool | Non-core MCP tool survives expiry, runs, is absent from a second session, and disappears on revocation. |
| 27 | `TestPrice_TierUsesCacheHeavyInput` | Unit | Length band | 100 uncached plus 99,900 read is the base rate. 100 plus 99,901 is the high rate. A canonical 512,000 tier plus the legacy alias does not price 200,001 as the 512,000 band. Exactly 200,000 on the alias alone stays base. |
| 28 | `TestLogLine_DropsTextInsideUsage` | Unit | Accounting line | Private text and an oversized extension inside usage are absent. Counters and session id remain. |
| 29 | `TestHistoryHit_StaticOnlyFails` | Integration | Fake-provider suite | A read equal to the pre-history prefix fails. A read beyond it, from a matching captured prefix, passes. |
| 30 | `TestUsageScreen_HitPercentAndProviderTab` | E2E | Hit percent; no new screen | Mounted screen: `0.8` shows `80%`, null shows no percent, provider tab is the existing tabs control, error and partial states stay. |
| 31 | `TestCacheOnlyZeroOutput_ReconcilesThroughReload` | Integration | Cache-only known split | 0 input, 0 output, 800 read, 200 write, total 1,000 through entry, session, model, provider, report, and reload. Plus the all-read case. A missing usage object is a separate control. |
| 32 | `TestVercelClaude_ManualCacheControl` | Unit | Vercel Claude mark | Captured request for `anthropic/claude-opus-4.5` has `cache_control` type `ephemeral` before the volatile tail and no `caching` field. An implicit upstream has no mark. |
| 33 | `TestFireworks_StandardCachedTokensOnly` | Unit | Fireworks row | Reads `usage.prompt_tokens_details.cached_tokens` only. No header and no `perf_metrics` are consulted, and `perf_metrics_in_response` is not sent. |
| 34 | `TestAlibaba_NestedWritePath` | Unit | Alibaba row | The fixture JSON nests `cache_creation_input_tokens` under `usage.prompt_tokens_details`. A top-level-only parse fails this test. |
| 35 | `TestSkillAndToolSizes_AreTokenCounts` | Integration | Skill list | The recorded sizes are labelled token counts or token estimates and name the estimator. A byte-only record fails. |

The five live runs are not rows in this table. They are the landing gate in User Story 6 and are not automated in CI.

### Test datasets

#### Dataset: Usage shapes

The Examples table in "Each published usage shape" is the dataset. Each row traces to that scenario. Boundary rows already included: missing fields, a repeated DeepSeek count, a Gemini unknown include rule, and a split 5-minute / 1-hour write.

#### Dataset: Cache price

| # | Input price per million | Cache price | Cache-read tokens | Expected cache dollars | Boundary | Traces to |
|---|---|---|---|---|---|---|
| 1 | 2 | absent | 0 | 0 | zero tokens | Cache price outline |
| 2 | 2 | absent | 1000 | 0.002 | missing price | Cache price outline |
| 3 | 2 | 0 | 1000 | 0 | published zero | Cache price outline |
| 4 | 2 | 0.2 | 1000 | 0.0002 | happy path | Cache price outline |
| 5 | absent | absent | 1000 | unpriced, not 0.003 | no price at all | Missing input price |

#### Dataset: Prefix stability

| # | What changes between turns | Bytes through prior history | Traces to |
|---|---|---|---|
| 1 | Clock plus 5 seconds | identical | Volatile notes |
| 2 | Scratchpad text | identical | Volatile notes |
| 3 | One newly loaded tool | different at the tool list, identical after the tool list only if the new tool is appended and the previous prefix matches up to the insertion point | Loaded tool |
| 4 | Goal-setting narrowing | different, allowed | Goal narrowing |
| 5 | History trimmed | different, allowed | Edge case, not a failure |

Row 3's expected bytes are identical for the prefix that precedes the new tool, and different from the new tool onward. That is one miss at load time, then hits on later turns. The test fails if the clock change alone changes the prefix.

### Regression test requirements

This feature changes existing behaviour. These tests must be rewritten to the new contract or deleted with the package they belong to. Deleting a package deletes its tests. Weakening an assertion to keep a green is not allowed.

| Existing behaviour | Existing test | What happens |
|---|---|---|
| Static system block carries a cache marker | `pkg/agent/skill_reminder_test.go::TestTurnAssembly_MenuInsideCacheBoundaryReminderOutside` | Rewrite. The skill menu stays in the stable prefix. The reminder moves behind the conversation with the dynamic block. The byte budget on the reminder text stays. |
| Hand-written Anthropic parser splits cache tokens | `pkg/providers/anthropic_messages/cache_tokens_test.go::TestParseResponseBody_CacheTokens_SeparatedFromPrompt` | Deleted with the package. The official connector's parser gets the equivalent assertion. |
| OpenAI chat parser splits cached tokens | `pkg/providers/openai_compat/cache_tokens_test.go::TestChatStream_CachedTokensSeparatedFromPrompt` | Kept, and extended by the usage-shape table rather than replaced. |
| Cost uses the rate table | `pkg/agent/loop_wave4_test.go::TestEstimateLLMCallCost` | Replaced by `TestPrice_CatalogArithmetic` and `TestPrice_MissingInputIsUnpriced`. The nil-usage case still costs nothing. |
| Cache key only for OpenAI's own hosts | The `supportsPromptCacheKey` cases in `pkg/providers/openai_compat/provider_test.go` | Rewritten to the allow-list. OpenAI still receives a key. A host documented as rejecting it still does not. |
| Usage report sums cache read | `pkg/session/usage_test.go` cases that sum cache read | Kept. New assertions cover the ratio and the provider dimension. |
| Factory returns the hand-written Anthropic provider | Factory tests that type-assert that package | Updated to the official connector. |

---

## Functional Requirements

- **FR-001**: The system MUST write one info log line per completed model call with provider id, model id, acting agent id, session id, turn or call id, uncached input, cache read, cache write, and when reported the 5-minute and 1-hour write counts. The line MUST NOT contain a raw usage object, an arbitrary string from the provider, prompt text, tool arguments, or credentials (D20). Unknown fields and non-numeric values are dropped, at most 32 counters are written, and existing credential scrubbing still runs. A ChatGPT sign-in call MUST say `subscription` and MUST NOT include a dollar cost. Any other call MUST include the dollar cost of its priced portions. An unpriced portion MUST be marked `unpriced` without erasing that dollar cost. The line stays at info.
- **FR-002**: The system MUST expose cache-hit percent per agent and per provider on the existing usage report, using the formula and the null rule in the machine-verifiable constraints. Provider totals MUST be summed from the stored per-call provider id, including after reload, and MUST reconcile with the session and agent totals, including a known zero split of 0 input, 0 output, 800 read, and 200 write. The existing Usage screen MUST show the percent and a provider breakdown on the existing catalog tabs, as specified under Reachability. `0.8` is shown as `80%`. A null ratio shows no percent. No new screen and no new control.
- **FR-003**: The system MUST NOT add a caching screen, tile, or setting.
- **FR-004**: The system MUST send Anthropic-protocol calls through the official Anthropic connector, and MUST delete the hand-written Anthropic connector only after the surviving connector passes the parity checks in User Story 2: reconstructed canonical tool calls, verbatim model ids, media, and correlated parallel results. The official constructor MUST keep the catalog row's base URL and the configured timeout, and MUST send the catalog model id verbatim.
- **FR-005**: Where the host documents Anthropic's lifetimes, the system MUST mark the tool list and the stable instructions for a 1-hour cache and the conversation tail for a 5-minute cache, with every 1-hour mark before every 5-minute mark, and with at most four marks. The tool-list mark exists so an instruction change can still reuse the tool prefix. Moonshot MUST receive no cache field and no 1-hour option. MiniMax MUST use its documented 5-minute or automatic mode and MUST NOT receive a 1-hour mark. Any other Anthropic-protocol host gets only the marks it documents. The history mark sits before the volatile tail.
- **FR-006**: The system MUST NOT keep a second Anthropic-protocol connector if a third-party host rejects the official body. The adjustment stays inside the one connector.
- **FR-007**: The system MUST place the clock, the tool-summary note, the scratchpad note, and the goal-checklist note in one trailing user-role text block after history and after any completed tool-call group, on the request that leaves the normalizer and the adapter. It MUST NOT insert that block between a tool call and its results. It MUST keep the web-display note and the workspace instructions before the conversation. It MUST NOT place those four notes in a system-role message. The history cache mark sits on the last history content before the tail.
- **FR-008**: The system MUST keep a tool that has been added to a session both advertised and executable in that session until the session ends, MUST NOT expose it to another session of the same agent, MUST remove it when authorization, assignment, or the server is revoked, and MUST serialize a given tool set in one deterministic order.
- **FR-009**: The system MUST keep the advertised skill list unchanged for the session, MUST place the per-turn skill reminder after the conversation, MUST record a labelled token count or token estimate of the tool list and of the skill text per agent, and MUST name the estimator. A byte or character count alone does not satisfy this. Bytes MAY be stored separately under their own name. The system MUST NOT start sending every allowed tool from the first turn.
- **FR-010**: The system MUST still narrow the tool list on a goal-setting turn, and MUST restore the session tool list on the next ordinary turn.
- **FR-011**: The system MUST parse every usage field named in the provider table and MUST normalize to uncached prompt, cache read, and cache write without double-counting a repeated field. Fireworks MUST be read only from `usage.prompt_tokens_details.cached_tokens` (D21). Alibaba's chat path MUST be read from the nested `usage.prompt_tokens_details` paths, not from a top-level creation field. The Gemini include rule MUST NOT be invented: the documented example does not say to subtract, so the parser does not subtract. A known usage object with input 0 and output 0 and a positive cache count is a real split, distinct from a missing usage object.
- **FR-012**: The system MUST send a cache key or header only where the provider table says to send one, and MUST NOT send a field the table marks as rejected. The value MUST be the session id, not the agent id, and MUST NOT be a secret.
- **FR-013**: The system MUST NOT add Bedrock cache checkpoints or Bedrock cache-field parsing in this feature.
- **FR-014**: The catalog repository MUST publish models.dev cost fields, in USD per 1 million tokens, with a LiteLLM cross-check at the existing 5 percent relative tolerance. Omnipus MUST price from that snapshot and MUST delete the hand-kept rate table.
- **FR-015**: The system MUST record tokens for a model with no input price, MUST report the call as unpriced, and MUST NOT apply the old fallback rate.
- **FR-016**: The system MUST price cache-read tokens, and 5-minute write tokens, at the input price when that cache price is absent, and MUST price them at zero when the catalog publishes zero. This fallback MUST NOT price a 1-hour write that FR-027 marks unpriced.
- **FR-017**: The system MUST NOT add a token-limit brake or a USD spend cap. D13 voids D8's token-limit sentence. Pricing honesty does not include a new limit.
- **FR-018**: The system MUST choose the length tier from uncached input plus cache read plus cache write, excluding output. It MUST follow the source's own boundary. Where the source says "up to N" at the base rate and "over N" at the high rate, exactly N MUST use the base rate, including when the catalog `tier.size` is N. A source that includes N in the high band MUST be priced that way. The system MUST NOT apply one inclusive or exclusive rule to every `tier.size`. Canonical tiers MUST win over a legacy `context_over_200k` alias. The alias alone means over 200,000: exactly 200,000 stays on the base rate, and the alias MUST NOT be read as a higher threshold such as 512,000.
- **FR-019**: The system MUST have fake-provider CI tests for request bytes, prefix stability, every usage shape in the dataset, cost arithmetic, and the second-call cost being lower but not zero.
- **FR-020**: Landing MUST include the five recorded live runs in D22, and no others. Each run MUST send the same conversation twice, and the second call MUST report cached tokens greater than zero. The ChatGPT subscription run MUST record tokens and no dollars (D14). Every other provider MUST be covered by a fixture taken from that provider's documented example response, not by a live call. One of the five runs MUST also be checked, on that same commit, by a tester and an independent validator, that the moved notes are still followed.
- **FR-021**: A live second call that returns success with zero cached tokens MUST NOT pass the cache proof. The live proof is cached tokens greater than zero. It is not a requirement that the live read exceed the pre-history prefix. That stronger comparison stays in the fake Anthropic suite.
- **FR-022**: The system MUST NOT change skill `allowed-tools` parsing.
- **FR-023**: The system MUST NOT add per-area log levels.
- **FR-024**: The system MUST allow a cache miss when history is trimmed or the model changes, and MUST NOT freeze history or pin the model to prevent it.
- **FR-025**: The per-call log MUST NOT include prompt text, tool arguments, credentials, or any string, array, or object taken from the provider usage payload. Provider id, model id, agent id, session id, and turn or call id are correlation ids this system writes, not provider strings, and they stay. A successful usage object that contains private text MUST NOT put that text in the log.
- **FR-026**: A ChatGPT sign-in call MUST be marked `subscription`, MUST record cached and uncached tokens, and MUST NOT record a dollar cost even when the catalog has an API price for that model.
- **FR-027**: The system MUST store 5-minute and 1-hour write counts separately when the provider reports the split. 5-minute writes MUST use catalog `cache_write` when that field is present. 1-hour writes MUST use catalog `cache_write_1h` when that field is present. When it is absent, 1-hour writes MUST be priced at the provider's documented 1-hour multiplier times the catalog input price. Anthropic's documented multiplier is 2. The system MUST NOT keep a dollar price for that multiplier. A provider that documents no 1-hour multiplier MUST leave that portion `unpriced` and MUST still store its tokens. The log MUST say which rule priced the 1-hour portion. The catalog's first price publication is not required to include `cache_write_1h`; the field is the tracked extension.
- **FR-028**: The system MUST NOT enable OpenAI explicit breakpoint mode, Moonshot's 1-hour option, DeepInfra retention, or the Vercel `caching: auto` rewrite. A Vercel Claude model MUST still receive the documented manual `cache_control` mark. That mark is not a second connector and is not the automatic rewrite.
- **FR-029**: The system MUST NOT promote an unsupported catalog row to a callable provider as part of this feature.

---

## Success Criteria

- **SC-001**: On a fake Anthropic server, the second call in one session reports a cache read greater than the full pre-history prefix (tools plus front instructions), after the clock has moved and the scratchpad has changed. A read that covers only that prefix fails.
- **SC-002**: The usage report hit ratio for a fixture of 100 uncached, 800 read, and 100 write equals 0.8, and a fixture with a zero denominator returns null.
- **SC-003**: A fixture call priced as in the priced-call scenario costs $0.0204. A model with no catalog price does not cost $0.003 per 1,000 input tokens.
- **SC-004**: Searching the tree finds no hand-kept rate table and no hand-written Anthropic connector package.
- **SC-005**: Landing evidence contains the five D22 runs and no others. Each names the account, the model, the commit, and a second-call cached-token count greater than zero. The ChatGPT subscription run has tokens and no dollars. A zero cached-token count fails that run.
- **SC-006**: `docs/settings.md` describes the hit percent on the existing Usage screen and does not add a caching page. `docs/operations/observability.md` describes the info accounting line, how to raise the existing log level, and priced, unpriced, and subscription.

---

## Traceability Matrix

| Requirement | User Story | BDD scenario | Test |
|---|---|---|---|
| FR-001 | US-1 | Accounting line | `TestLogLine_UsageOnly`, `TestLogLine_DropsTextInsideUsage` |
| FR-002 | US-1 | Hit percent; empty percent | `TestUsageReport_HitRatioAndProvider`, `TestProviderId_SurvivesReload`, `TestUsageScreen_HitPercentAndProviderTab` |
| FR-003 | US-1 | No new screen | Review of routes plus the settings doc. No new page test beyond the existing navigation tests still passing. |
| FR-004 | US-2 | Official connector; streamed Anthropic call | `TestConnector_OneAnthropicPackage`, `TestAnthropicStream_ReadsCacheUsage`, `TestConnector_ReconstructedCallAndVerbatimModel` |
| FR-005 | US-2 | Marks and lifetimes | `TestAnthropicBody_MarksAndLifetimes`, `TestDialectBodies_MoonshotAndMiniMax` |
| FR-006 | US-2 | No second connector | `TestConnector_OneAnthropicPackage` |
| FR-007 | US-3 | Volatile notes | `TestPrefixStable_ClockAndScratchpadMove`, `TestPrefixStable_AfterNormalizeAndAdapter` |
| FR-008 | US-3 | Tool order; loaded tool; new session does not inherit | `TestToolOrder_IndependentOfInputOrder`, `TestLoadedTool_SurvivesTime`, `TestLoadedTool_NotInheritedByNextSession`, `TestLoadedMcpTool_ExecutesOnlyInThatSession` |
| FR-009 | US-3 | Skill list | `TestSkillList_FrozenForSession`, `TestSkillAndToolSizes_AreTokenCounts` |
| FR-010 | US-3 | Goal narrowing | `TestGoalNarrowing_RestoresListNextTurn` |
| FR-011 | US-4 | Usage shapes; unknown usage field | `TestUsageShapes_Normalize`, `TestUsageShapes_DeepSeekHitNotDoubleCounted`, `TestUsageShapes_GeminiDoesNotSubtractUntilEvidence`, `TestUsageShapes_UnknownFieldDoesNotFail`, `TestFireworks_StandardCachedTokensOnly`, `TestAlibaba_NestedWritePath`, `TestCacheOnlyZeroOutput_ReconcilesThroughReload` |
| FR-012 | US-4 | Cache key | `TestCacheKeyAllowList_SendsAndSuppresses` |
| FR-013 | US-4 | Bedrock unchanged | `TestBedrockBody_Unchanged` |
| FR-014 | US-5 | Priced call | `TestPrice_CatalogArithmetic`, `TestPrice_NoRateTable` |
| FR-015 | US-5 | Missing input price | `TestPrice_MissingInputIsUnpriced` |
| FR-016 | US-5 | Cache price outline | `TestPrice_MissingCacheUsesInput_ZeroCacheStaysZero` |
| FR-017 | US-5 | — | Absence: no new cap field and no new token-budget brake. `TestPrice_NoRateTable` does not grow a limit. |
| FR-018 | US-5 | Length band | `TestPrice_ContextTier`, `TestPrice_TierUsesCacheHeavyInput` |
| FR-019 | US-6 | Fake-provider suite | `TestSecondCall_CostsLessNotZero`, `TestHistoryHit_StaticOnlyFails` |
| FR-020 | US-6 | Recorded live read | Landing evidence, not a CI test |
| FR-021 | US-6 | Zero cached tokens fails the proof | The live-evidence check, not a CI test |
| FR-022 | — | — | Absence: skill `allowed-tools` parsing tests unchanged |
| FR-023 | — | — | Absence: no new log-level setting |
| FR-024 | US-3 | Prefix dataset row 5 | `TestPrefixStable` does not assert a hit across a trim |
| FR-025 | US-1 | Error body | `TestLogLine_UsageOnly`, `TestLogLine_DropsTextInsideUsage` |
| FR-026 | US-5 | ChatGPT sign-in call | `TestPrice_SubscriptionRecordsNoDollars` |
| FR-027 | US-4, US-5 | One-hour write; Anthropic split write row | `TestUsageShapes_Normalize`, `TestPrice_OneHourWriteUsesDocumentedMultiplier` |
| FR-028 | US-4 | Implicit host; provider table | `TestCacheKeyAllowList_SendsAndSuppresses`, `TestVercelClaude_ManualCacheControl` |
| FR-029 | US-4 | — | Factory tests: unsupported rows still fail construction as they do today |

---

## Rollout and risks

| Risk | What goes wrong | Mitigation |
|---|---|---|
| Third-party hosts reject the official body | MiniMax, Moonshot, or Z.AI coding-plan calls start failing | D3 and D22. One connector is adjusted. Those three runs are part of the five. DeepSeek is a documented fixture, not a live gate. |
| Moved notes change agent behaviour | Goal checklist or scratchpad is followed less well at the end of the prompt | One of the five live runs is checked by a tester and an independent validator. A note that fails that check is moved back in front, and the spec is corrected. It is not left behind a green test. |
| Prices land after markers | Displayed session cost falls toward zero while cache reads are real | Catalog snapshot with prices lands in the same change as the marker, or before it. The second-call test fails if the second cost is zero. |
| 1-hour writes priced at 1× input, or at the 5-minute `cache_write` | An Anthropic 1-hour write is understated. The published 1-hour write is 2× input, and `cache_write` on Haiku 4.5 is the 1.25× 5-minute rate. | Corrected D18. Until `cache_write_1h` exists, Anthropic 1-hour writes cost 2× the catalog input price. A provider with no documented 1-hour multiplier does not get a guessed factor: that portion is unpriced. The log names the rule. |
| Cache key sent to a host that rejects it | The turn fails with 400 or 422 | Allow-list. Scaleway and OVHcloud are explicit denials. A new host is not added because a neighbour accepts the field. |
| Default log level hides the line | An operator at `warn` sees nothing per call | Accepted (D15). The Usage screen is the default-install surface. The line stays at info. |
| No spend brake | Honest prices do not stop a runaway loop | Accepted (D13). This feature does not add a cap and does not pretend the deleted one still exists. |
| Gemini include rule guessed | Prompt tokens go negative or double | Parser does not subtract. The documented example does not state the rule, and Gemini is not called live. |

Rollout is one change on the feature branch: catalog publication, embedded snapshot, connector switch, prefix order, parsers, report fields, tests. No flag. A flag that can turn the marker off while leaving the prefix move on is a second behaviour to test, and D1 does not ask for one. If a live check fails, the branch does not land; it is not half-enabled in production.

User-facing docs: `docs/settings.md` and `docs/operations/observability.md`, as in Reachability.

---

## Dependency graph and implementation order

```
catalog cost fields ──► embedded snapshot ──► price from catalog ──► delete rate table
                                                          │
contract schemas ──► regenerate ──► persisted-stat writers, report, screen
        │
official connector ──► Anthropic marks and stream
        │
prefix order (notes, clock) ──► sticky tools + skill token estimates
        │
usage parsers (all shapes) ──► log line
        │
        └──────── fake-provider CI suite
                              │
                    five live runs (landing gate)
```

| Step | Work | Depends on | Landing note |
|---|---|---|---|
| 1 | Dated ADR-071 amendment, as specified above | This spec | Before the tool-list change |
| 2 | Catalog schema and assemble publish cost fields, including optional `cache_write_1h` and the source's tier-boundary meaning; dispute rule for prices | D7, D18, FR-018 | First publication may omit `cache_write_1h` |
| 3 | Embed the new snapshot; extend the Go model struct | Step 2 | — |
| 4 | Contract schemas, then `scripts/gen-contracts.sh` | This spec | Before any writer, report, or screen that uses the new fields. Five steps: schema file, OpenAPI reference, regenerate, commit the generated diff with the schema, then use only the generated types |
| 5 | Usage-shape parser and its tests, including the 5-minute / 1-hour split, the Fireworks standard field, the nested Alibaba write, and the cache-only zero split | Step 4 for anything the parser persists onto session stats | Gemini is not subtracted |
| 6 | Official Anthropic connector, delete the hand-written one, marks and lifetimes, Vercel manual Claude mark | Step 5 for the stream assertion | Parity tests pass before the old package is deleted. Not gated on a DeepSeek live call |
| 7 | Move the volatile notes and the clock | Nothing | — |
| 8 | Sticky tool list, frozen skill list, token estimates | Step 1 | — |
| 9 | Cache-key allow-list; value is the session id | Nothing | — |
| 10 | Catalog pricing, delete the rate table, unpriced stats, subscription marking, 1-hour write rule | Steps 3 and 4 | Writers use the regenerated types |
| 11 | Usage report, Usage screen hit percent, both doc pages | Steps 4 and 10 | — |
| 12 | Info log line, including `unpriced` and `subscription` | Steps 5 and 10 | Stays at info |
| 13 | Fake-provider suite green in CI | Steps 5–12 | Includes the documented-example fixtures |
| 14 | Five recorded live runs (D22) | Step 13 | No other provider is called live |

Steps 2, 5's in-memory parser, and 7 touch different trees and can run together. Step 8 must not start until the ADR amendment is written. Step 4 is finished before step 10 and step 11. Step 6 and step 7 meet at the request body and should be integrated before the prefix-stability test is claimed green.

---

## Ambiguity Warnings

No open question remains. D13–D20 closed the earlier questions. D21 and D22 closed grill round 2. D22 replaces the live-proof population in D9 and D17.

| # | Was ambiguous | Decision |
|---|---|---|
| 1 | D8's token-limit sentence | D13. No new limit. The sentence is void. |
| 2 | ChatGPT subscription dollars | D14. Mark `subscription`. Tokens only. No dollars. |
| 3 | Cache key value | D16. Session id. Not the agent id. |
| 4 | Which providers are called live | D22. Five accounts only. Everyone else is a documented fixture. D17's extra live list is not the gate. |
| 5 | Log line versus default `warn` | D15. Stay at info. The Usage screen is what the default install shows. |
| 6 | One `cache_write` versus a 1-hour lifetime | Corrected D18. Separate counts. 5-minute writes at `cache_write`. 1-hour writes at the documented multiplier times catalog input (Anthropic: 2) until `cache_write_1h` exists. No documented multiplier: that portion is unpriced. |
| 7 | "No UI" versus the usage report | D15. No new screen. The existing Usage screen shows the hit percent. |
| 8 | Gemini prompt total include rule | FR-011. Do not subtract. There is no live Gemini check. |
| 9 | Fireworks metrics versus the standard field | D21. Standard `usage.prompt_tokens_details.cached_tokens` only. |
| 10 | How strict the live cache proof is | D22. Second call, cached tokens greater than zero, on the five accounts. The fake suite still requires a history-covering read. |

---

## Evaluation Scenarios (Holdout)

These are for a person checking the landed build. They are not part of the test plan above, and implementers should not add tests that only restate them.

### Scenario: A tool loop gets cheaper on the second step

- **Setup**: One session, one Anthropic model, a prefix long enough for that model's minimum.
- **Action**: Ask for a task that takes several tool steps without changing tools after the first step.
- **Expected outcome**: On one of the five live accounts, a later step reports cached tokens greater than zero, and the usage report hit percent for that agent is above zero. The fake suite, not this check, proves the read includes history.
- **Category**: Happy Path

### Scenario: The usage screen matches the log

- **Setup**: The live run from the previous scenario.
- **Action**: Open Usage for today and compare one provider's cache-read total with the sum of the log lines for that provider.
- **Expected outcome**: The totals match, and the hit percent matches the formula.
- **Category**: Happy Path

### Scenario: A local model does not grow a fake dollar cost

- **Setup**: An Ollama model with no catalog price.
- **Action**: Run one turn.
- **Expected outcome**: Tokens are counted. The log says unpriced. The cost is not the old $3 per million fallback.
- **Category**: Happy Path

### Scenario: A host that hates the cache key still answers

- **Setup**: A Scaleway or other host this spec says must not receive `prompt_cache_key`, if one is configured.
- **Action**: Send one turn.
- **Expected outcome**: The call succeeds, and the captured request has no `prompt_cache_key`.
- **Category**: Error

### Scenario: A short prompt does not look like a failure

- **Setup**: A one-line turn, well under every provider minimum.
- **Action**: Send it twice.
- **Expected outcome**: Both calls succeed. Cache read may be zero. The product does not show an error.
- **Category**: Error

### Scenario: Loading a tool misses once

- **Setup**: A session that has already had a cache hit.
- **Action**: Load one new tool, then send two more ordinary turns without loading another.
- **Expected outcome**: The load turn may miss. A later turn can hit again. The tool is still listed on those later turns.
- **Category**: Edge Case

### Scenario: The goal note is still followed from the tail

- **Setup**: A goal-setting turn and a scratchpad with a distinctive instruction, on the model used for the behaviour check.
- **Action**: Run the existing goal and scratchpad checks.
- **Expected outcome**: The agent still follows them. If it does not, the note placement is wrong even if the cache hits.
- **Category**: Edge Case

---

## Assumptions

- D1 through D22 are binding. D13 voids D8's token-limit sentence. There is no cost cap to extend, and none is added. D19 narrows D6 to hosts that document those lifetimes. D20 replaces every "raw usage object" sentence. D21 drops the Fireworks metrics fallback. D22 replaces the live-proof population in D9 and D17 and does not reopen the rest of those decisions.
- "The two formats" means the Anthropic protocol and the OpenAI-compatible protocol the factory already speaks, including Gemini's and Ollama's rows on that client. It does not mean adding Bedrock, Vertex, or Azure.
- The embedded catalog snapshot is updated from a catalog release. Prices are not typed into Go.
- A published models.dev cost is USD per 1 million tokens. Checked against `claude-haiku-4-5` on 2026-10-15 (`input` 1, `cache_write` 1.25).
- The catalog's 5 percent relative tolerance is reused for prices. The 4,096-token absolute tolerance is not.
- Session `cost` on the wire stays a priced sum. Unpriced calls are a separate token count, so the Usage screen can keep counting tokens and not money.
- GitNexus was not available. Caller impact is inferred from search.

## Clarifications

### 2026-10-15

- Q: Is the scope Anthropic only, or every provider? -> A: D2. Both wire formats. Bedrock postponed.
- Q: Which Anthropic connector? -> A: D3. Official SDK. Delete the hand-written one. One connector if a third party needs a change.
- Q: Where do volatile notes go? -> A: D4. Clock, tool summary, scratchpad, and goal checklist behind the conversation. Web display and workspace instructions stay in front.
- Q: What happens to tools that expire? -> A: D5. They stay for the session. Order is deterministic. Skill list is checked the same way. Measure sizes. Do not yet send every tool up front. ADR-071 is amended.
- Q: What lifetimes? -> A: D6, as narrowed by D19. 1 hour on instructions and tools, and 5 minutes on the conversation tail, only where that host documents those marks. Moonshot sends no mark. MiniMax uses its documented 5-minute or automatic mode and no 1-hour mark.
- Q: Who keeps the price list? -> A: D7. The catalog's sources. Delete the hand-kept table.
- Q: What if there is no price? -> A: D8, as narrowed by D13. Unpriced, tokens still counted. A missing cache price uses the input price. No token limit is added.
- Q: Subscription calls, the cache-key identity, the Usage screen, the log level, the extra live hosts, and the 1-hour write price? -> A: D14 marks ChatGPT sign-in as `subscription` with tokens and no dollars. D16 sets the cache key to the session id. D15 puts the hit percent on the existing Usage screen and the per-call line at info. D17's extra live list is replaced by D22. D18 stores 5-minute and 1-hour writes separately and prices them as specified in FR-027.
- Q: One caching dialect for every Anthropic-protocol host, and may the log carry the raw usage object? -> A: D19 and D20. One connector, each host's documented marks. The log carries bounded numeric counters and correlation ids only. No finding from grill round 1 is rejected.
- Q: What proves it? -> A: D9, as narrowed by D22. Fake-provider CI for every provider, using documented examples where we have no account. Live proof is the five accounts only.
- Q: Fireworks performance metrics, and which live routes must prove a history-sized read? -> A: D21 and D22. Fireworks reads only `usage.prompt_tokens_details.cached_tokens`. Live proof is OpenRouter, Z.AI/GLM, MiniMax, Moonshot, and ChatGPT subscription: the same conversation twice, cached tokens greater than zero. No other live provider. No finding from grill round 2 is rejected.
- Q: What does the operator see? -> A: D10. One log line, and hit percent per agent and per provider on the existing report. No new log-level feature.
- Q: Skill allowed-tools? -> A: D11. Out of scope.
- Q: A red-team pass in the interview? -> A: D12. No. Two grill rounds on this spec instead.
