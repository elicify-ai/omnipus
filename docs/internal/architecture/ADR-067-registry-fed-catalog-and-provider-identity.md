# ADR-067: One registry-fed catalog — model limits and provider identity from public registries

- **Status:** Accepted (operator approval 2026-08-23 — implementation plan approved). Proposed 2026-08-22; split out of ADR-066 after its second adversarial review. **Amended 2026-08-22 (§8b)** from the plan-spec review (`docs/internal/specs/adr-067-registry-catalog-spec-review.md`).
- **Date:** 2026-08-22
- **Related:** [ADR-066](ADR-066-context-budget-and-tool-result-routing.md) (the incident fix; its D2 ladder consumes this catalog); [ADR-068](ADR-068-subscriptions-provider-deletion-and-provider-ux.md) (subscription policy, provider deletion, default model, provider UX — consumes the provider table defined here); CLAUDE.md **Constraint #1** (single binary, no new runtime deps), **Constraint #8** (contract-first wire formats).
- **Deciders:** Operator (Daniel Piatkowski)
- **Evidence level:** 1 for everything cited as read. Incident facts were read on the build tree the failing binary came from (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/build-v0.1.1` @ `6acd378`); design facts on this branch @ `4684e8c7`. Cited as `file::symbol` per CLAUDE.md except where a line number is itself the claim. Absences are cited as searches. Items marked **[UNVERIFIED]** are collected in §8.

> **Greenfield rule (operator, 2026-08-22), applies to the entire scope:** no backward compatibility, no migration, no aliasing of old names, no grace periods, no mixed-version tolerance, no retired-name lists, no boot notifications about removed things. Pre-existing Omnipus state that does not match this design simply does not work. Runtime fallbacks that are part of the design (embedded snapshot when offline) are not compatibility mechanisms and stay.

---

## 1. Context

Omnipus learns what a model can do from a file a person types. The seed (`pkg/providers/capabilities/data/providers_capabilities_seed.json`, *"freeze-gate re-validation 2026-07-28"*) carries modalities and resize limits for 78 models and **no context window at all**; the window was derived from the output limit (`maxTokens * 4`), which is how the agent in ADR-066 §1.1 assumed one eighth of its model's real capacity. A second, unrelated catalog (`pkg/providers/catalog`, 23 hand-authored provider entries) feeds the picker. Neither talks to a registry. This ADR replaces both with one document assembled from public registries, and makes the registry's provider identities Omnipus's own.

---

## 2. D1 — The catalog is assembled from public registries by a daily job

**D1 — the catalog is assembled from public registries by a daily job in a dedicated repository, not typed by hand.** *(Operator decision 2026-08-22, replacing the earlier "add two fields to the hand-curated seed".)*

*One catalog, not two (operator question 2026-08-22, resolved).* Omnipus today has **two** catalog packages by accident of history — `pkg/providers/capabilities` (per-model modalities + resize limits, built for the media pipeline) and `pkg/providers/catalog` (23 provider entries, built for the picker). Greenfield removes the reason to keep both. **Decision: one package, one embedded file, one schema — providers with their models nested**, exactly the registry's own shape:

```
provider  { id, api, protocol, env, region, plan, tier, subscription_policy, resize_limits }
  └ model { id, context_window, max_output_tokens, input_modalities, tool_call, status }
```

The provider+model key is the nesting; a model's limits live under the route that serves it. The assembly job publishes **one** checksummed file (no signature — see *Signing — not adopted* below). `pkg/providers/capabilities` is folded into `pkg/providers/catalog`; `Resolve(provider, model)` is the only lookup; the media pipeline, the agent loop, Settings and the picker all read the same in-memory document. Two files kept in step would have been the same mistake as thirty-six provider ids kept in step.

*What exists.* `pkg/providers/capabilities` already has every piece of the runtime machinery: an embedded seed (`embed.go`), a checksum-verified `GHReleasePuller` that fetches the release asset `providers_capabilities.json` + `.sha256` from GitHub with a raw fallback (`pkg/gateway/gateway.go` wires it to `elicify-ai/omnipus`, interval `capabilityCatalogRefreshInterval = 7 * 24 * time.Hour`, timeout 30 s), a semver-aware refresh that cannot downgrade, a persisted store, a validated DTO → domain parse, and `Catalog.Resolve`. **Its `resolveStrippedPrefix` fallback — which maps `z-ai/glm-5.2` to a bare `glm-5.2` by dropping the provider — is removed:** under the provider+model key it is an alias that returns the *wrong route's* limits (1,000,000 for a request that goes via OpenRouter's 1,048,576). Lookup is exact on (provider, model); a miss falls to the ADR-066 D2 ladder. **Nothing else in that machinery changes.** What changes is where the file comes from: today a human reads provider docs and writes the JSON (`source: "freeze-gate re-validation 2026-07-28 …"`; no generator exists in `scripts/`).

*Why.* Validated live on 2026-08-22 against all 78 seeded models: **models.dev** (MIT; 193 providers, 7,246 models; regenerated hourly; correctable by PR) carries `limit.context`, `limit.output` and `modalities.input` incl. `image` and `pdf` on **every** entry. Cross-checked against **LiteLLM's** `model_prices_and_context_window.json` (MIT; 3,111 entries; independently maintained): where the hand-curated seed and models.dev disagreed (19 models on PDF, 4 on image) and LiteLLM could adjudicate, **it sided with models.dev every time** — `gpt-4o`/`gpt-4.1`/`o3` accept PDFs (seed said no), `o3-mini` does not accept images (seed said yes). The hand-typed catalog is the stale one. The field agrees: OpenCode, Kilo, Hermes, Cline and Goose consume models.dev; Crush and OpenClaw run their own published feeds (`catwalk.charm.land`, `catalog.openclaw.ai`) — the shape adopted here.

*The assembly repository* (open source, separate from Omnipus) runs a daily job that:

1. pulls models.dev `api.json` and LiteLLM's JSON, recording the upstream commits in a manifest;
2. merges into the Omnipus schema — `context_window`, `max_output_tokens`, `input_modalities`, tool-calling, deprecation status — **keyed by provider + model**, because limits differ by route (`z-ai/glm-5.2`: 1,048,576 via OpenRouter, 1,000,000 direct);
3. applies `overrides/` (local corrections that win over both registries — e.g. `gemini-3-pro` PDF, where the registries disagree and the provider accepts PDFs in practice — and legacy models the registries have retired) and `resize_limits.json`;
4. **opens an issue on any disagreement between the two registries rather than silently choosing** — the discipline that exposed the stale seed; **Amendment 2026-08-23 (B2 first run):** the job opens **one issue per run** listing every dispute (label `registry-dispute`), not one issue per disagreement — the first real run produced 194 disputes between models.dev and LiteLLM (63 context_window, 98 max_output_tokens, 9 tool_call, 30 modality), and 194 issues would be noise, not signal. Last-known-good publication per dispute is unchanged.
5. publishes **one** file — `providers_catalog.json` + `.sha256` — as a GitHub Release, `schema_version` 2.0.0 (new shape, greenfield; the old `providers_capabilities.json` is not produced).

*Closing the resize gap.* `resize_budget` (`long_edge_px`, `max_bytes`) is in **neither** registry (searched both). It is not a model fact but a provider's upload limit, documented once per vendor — the 78-model seed uses exactly **four distinct values**, one per vendor. It lives in the assembly repo as a small per-provider table, hand-maintained and PR-reviewed; the job joins it onto every model of that provider.

*Omnipus-side changes* — four, all small: the catalog schema becomes 2.0.0 (providers with nested models, *One catalog, not two* above); the puller's owner/repo points at the assembly repo (asset name and sidecar unchanged); the refresh interval drops from 7 days to **24 hours, plus one background pull at startup** (never blocking boot; the existing 30 s timeout applies); and the `go:embed` snapshot is a **committed file** (`pkg/providers/catalog/data/providers_catalog.json`) refreshed by a scheduled pull request from the assembly repo — never fetched at build (§8a MAJ-005) — so offline boot and online refresh agree on schema.

*Feed acceptance rules (amended 2026-08-22, §8b F-01/F-02/F-03/F-05).* The sidecar is **mandatory**: a release or raw asset with no `.sha256` is rejected (today's puller trusts a 404 sidecar — that behaviour is reversed; with no signature the checksum is the only integrity check). On the release path the sidecar is taken from the release's own asset list, not a derived URL. The asset read cap rises from 2 MB to **16 MB** (named constant); a body that hits the cap is rejected as *too large*, never reported as a checksum failure. `version` is **`vYYYY.M.D[.N]`** with the leading `v`, because the existing comparator only parses numerically with that prefix and would otherwise order `2026.9.30` above `2026.10.1` lexically. Every non-empty `api` URL is validated on load — `https` only, no userinfo/query/fragment, host not loopback/link-local/private/metadata — except rows whose protocol is `ollama` or whose id is `vllm`, `lmstudio`, or a custom row, which may use `http` and local hosts; a document that violates this is rejected whole and the snapshot serves. **Disagreement rule:** numeric fields that differ by ≤ 5 % or ≤ 4,096 tokens (whichever is larger) are not disputes — the lower value is published and both recorded; larger deltas open an issue and publish last-known-good (§8a MAJ-014); a closed issue writes the adjudicated value into `overrides/`, which then wins.

*Signing — not adopted (operator decision 2026-08-22).* Releases carry the existing `.sha256` checksum sidecar and nothing more: no release signature, no new dependency, no key to rotate. **Accepted risk, stated plainly:** a checksum proves integrity in transit, not authorship, so a hijacked download — a compromised publishing account or a tampered mirror — would be accepted by every install; a fake catalog could set wrong limits (dead turns) or, more seriously, wrong provider endpoints, sending API keys to a server the attacker controls. **Mitigations that remain:** the release is fetched only from the pinned owner/repo (`elicify-ai/omnipus-provider-catalog`) over HTTPS; the raw fallback is pinned to `main` in code (no config override) and, because asset and sidecar then come from the same branch, its checksum proves transport integrity only; the puller never downgrades versions; every endpoint URL is validated on load (above); the embedded snapshot is the fallback when the fetch fails, the checksum is missing or wrong, or the document is invalid.

*Not adopted.* **OpenRouter as a generation source**: its terms forbid automated copying of Service data; it remains a live-query source only (ADR-066 D2, live-provider rung). **Hand-curation of the main table**: demonstrated stale. **A boot-time fetch with no bundled fallback**: violates the single-binary offline-boot requirement; every surveyed harness ships a snapshot.

---

## 3. D11 — Provider identity comes from the registry too

*(Operator decision 2026-08-22.)* models.dev is a **provider** catalog as much as a model catalog. Every one of its 193 providers carries `api` (base URL), `npm` (wire protocol: `@ai-sdk/openai-compatible`, `@ai-sdk/anthropic`, …), `env` (key variable) and `doc`; and **region, plan and protocol variants are separate providers** with their own URL, protocol and model list — `zai` / `zhipuai` (international / China), `zai-coding-plan` / `zhipuai-coding-plan`, `moonshotai` / `moonshotai-cn`, `minimax` / `minimax-cn`, `alibaba` / `alibaba-cn` / `alibaba-coding-plan` / `alibaba-token-plan`, `kimi-for-coding`, and so on — **24 of 193** are such variants (read live 2026-08-22). The coding-plan variants expose a *subset* of models (`zai-coding-plan`: 5 of `zai`'s 14), so plan availability is a catalog lookup as well.

### 3.1 Where provider identity lives in Omnipus today

**[CORRECTED 2026-08-22]** An earlier version of this section said the factory switch was the only registry. It is not. **`pkg/providers/catalog` already exists** and describes itself as *"the backend-owned single source of truth for the 23 user-facing LLM provider variants available in the Omnipus picker"* — hand-authored `Entries`, one per billable endpoint keyed by **company × plan × region**, with the Anthropic-compatible sibling folded into `AnthropicId` rather than a separate row (FIX-5, `provider-ux-fixes-plan.md`). Entry fields: `id, company, label, plan, wire, endpointHint, subtitle, logoSlug` (+ `anthropicId`). `gen/main.go` emits `data/providers_catalog.json` **and** a generated TypeScript file for the SPA picker from that Go slice (*"Source of truth: pkg/providers/catalog/catalog.go → Entries"*). So the picker is already data-driven — from a hand-typed Go slice of 23. **D11 therefore replaces the data source of an existing catalog; it does not introduce one.** The 23 ids today: `openai anthropic google openrouter groq mistral nvidia cerebras ollama azure z-ai zhipu z-ai-coding zhipu-coding moonshot moonshot-cn minimax minimax-cn deepseek qwen qwen-intl qwen-us coding-plan`.

- Transport dispatch is one `switch` in `pkg/providers/factory_provider.go` (~40 `case` labels) is the only registry; aliases are ad hoc (`"z-ai", "z.ai", "zai"`). **1,241 string literals across 36 distinct ids** in non-test Go (searched), including three spellings of one thing — `qwen-intl`, `qwen-us`, `qwen-international`.
- Wire protocol is encoded as a *suffix on the provider id* (`z-ai-anthropic`, `moonshot-cn-anthropic`, `alibaba-coding-anthropic`), so every provider that offers two protocols exists twice.
- The wire `provider` field is a **free string** (`contracts/components/schemas/Agent.yaml` gives `"openrouter"` as an *example*, not an enum) — so renaming ids needs no contract enum change.
- Credential refs are independent of provider id: `api_key_ref` in `config.json` (`openrouter_API_KEY`, `z-ai-coding_API_KEY`) is the key name, so changing provider ids does not invalidate stored secrets.

### 3.2 Decision

1. **Canonical provider ids are models.dev's.** `zai`, `zhipuai`, `zai-coding-plan`, `moonshotai`, `moonshotai-cn`, `alibaba`, `alibaba-cn`, `alibaba-coding-plan`, `google`, … One vocabulary shared with OpenCode, Cline, Hermes and Goose; new plans and regions appear without anyone in Omnipus typing anything.
2. **Protocol becomes a field, not a suffix.** The provider table carries `protocol` from the catalog (`npm`); the `-anthropic` ids collapse into `(id, protocol=anthropic)`. Where models.dev records one protocol but the vendor also serves the other (Z.ai, Moonshot, DeepSeek all expose Anthropic-compatible endpoints alongside OpenAI-compatible ones), the override file in the assembly repo adds the second endpoint — the registry is the default, not the ceiling.
3. **The factory switch dispatches on protocol, not on provider name.** ~40 cases become ~5 (`openai-compatible`, `anthropic`, `google`, `ollama`, `cli`) plus one **`custom`** case; base URL and defaults come from the table (`env` is carried for the picker's hint text only — Omnipus reads keys from the credential store, never from environment variables). `google` is a distinct value but the same HTTP transport: the row's URL is Gemini's OpenAI-compatible endpoint and the key goes as a Bearer token, exactly what today's `google` case does. **`custom` is new work, not a catalog row** (amended §8b F-07/F-08/F-09): a provider config whose id is not in the table is accepted **only** when it carries an explicit `api_base` **and** a `protocol` ∈ {`openai-compatible`, `anthropic`}; it is then a custom row keyed by the operator's chosen id, so several custom endpoints coexist. An unknown id without both fails as unknown-provider. **Model ids are bare**: the `provider/` prefix on `model` and `ExtractProtocol`'s silent `openai` default are deleted; `ModelConfig.provider` is the only identity and the catalog key is exactly `(provider, model)`.
4. **The assembly repo publishes one document** — providers with nested models (§2, D1). **`pkg/providers/catalog` becomes the single catalog package**: `capabilities` folds into it, `Entries` stops being a hand-typed Go slice and is loaded from the feed (embedded snapshot + refreshed copy). `gen/main.go` and `src/lib/generated/providerCatalog.ts` are **deleted** (amended §8b F-10; agrees with ADR-068 §6): a daily-refreshed catalog cannot be baked into the SPA bundle, so the SPA reads the providers-catalog `GET` endpoint (§5) and caches it by ETag. Providers with no registry entry stay in a local file shipped with the feed: `ollama`, `vllm`, `litellm`, `codex-cli`, `openai-chatgpt` (ADR-068), `shengsuanyun`, `volcengine`, `avian`, `mimo`. (`custom` is not a row — item 3. `novita` is `novita-ai`, `longcat` and `modelscope` are registry ids unchanged, `lmstudio` is a registry id — §3.3.)

**Aggregators are in the registry as providers in their own right** — `openrouter` (359 models, 60 upstream vendors), `vercel`, `requesty`, `amazon-bedrock`, `nvidia`, `novita-ai`, `kilo`, and ~100 more hosts and gateways; 102 of the 193 providers are aggregators or hosts rather than first-party vendors (read live 2026-08-22). Their models are keyed with the vendor prefix (`openrouter` → `z-ai/glm-5.2`), and their **limits are the aggregator's, not the vendor's** — `z-ai/glm-5.2` is 1,048,576 via `openrouter` and 1,000,000 via `zai`. That is exactly the provider+model key this ADR requires, and why the key cannot be model-only.
5. **Settings lists providers from the table**, grouped by vendor → region → plan, with protocol shown. That is a new read-only wire surface (`GET` providers catalog) and goes through Constraint #8.

### 3.3 Canonical-id reference — documentation only, not a code artefact

Old Omnipus id → canonical registry id, for an operator hand-editing their own `config.json` or agent entities. **Nothing in the binary reads this table.** Every target was verified present in the live registry on 2026-08-22.

| Old Omnipus id | Canonical | Old Omnipus id | Canonical |
|---|---|---|---|
| `z-ai`, `z.ai` | `zai` | `moonshot`, `moonshot-cn` | `moonshotai`, `moonshotai-cn` |
| `zhipu` | `zhipuai` | `moonshot-anthropic` (+`-cn`) | `moonshotai` (+`-cn`), protocol=anthropic |
| `z-ai-coding`, `glm-coding` | `zai-coding-plan` | `qwen` | `alibaba-cn` |
| `zhipu-coding` | `zhipuai-coding-plan` | `qwen-intl`, `qwen-international`, `dashscope-intl` | `alibaba` |
| `z-ai-anthropic`, `zhipu-anthropic` | `zai` / `zhipuai`, protocol=anthropic | `qwen-us`, `dashscope-us` | `alibaba`, region=us |
| `minimax-anthropic` (+`-cn`) | `minimax` (+`-cn`) — registry already records anthropic protocol | `coding-plan`, `alibaba-coding`, `qwen-coding` | `alibaba-coding-plan` |
| `deepseek-anthropic` | `deepseek`, protocol=anthropic | `…-coding-anthropic` | `alibaba-coding-plan`, protocol=anthropic |
| `gemini`, `anthropic-messages` | `google`, `anthropic` | `novita` | `novita-ai` |
| `azure`, `azure-openai` | `azure` — **`unsupported: deployment-url`** (per-deployment URL shape has no catalog `api`; revisit in its own ADR) | `bedrock` | `amazon-bedrock` — `unsupported: cloud-iam` |
| `longcat`, `modelscope`, `vivgrid`, `nvidia`, `cerebras`, `groq`, `mistral`, `deepseek`, `openrouter`, `openai`, `anthropic`, `ollama`, `vllm`, `litellm`, `minimax`, `minimax-cn`, `shengsuanyun`, `volcengine`, `avian`, `mimo` | unchanged (registry id or local-file id) | `claude-cli`, `claudecli`, `codexcli`, `antigravity` | dropped — `claude-cli` descoped and `antigravity` deleted by ADR-068; `codex-cli` / `openai-chatgpt` per ADR-068 MAJ-013 |

- **No code rewrites existing config or agent entities.** A `provider` value that is not a canonical id (and not `custom` with an endpoint) is an unknown provider and fails on the generic unknown-provider path.
- **The factory's ad-hoc alias strings** (`"z-ai", "z.ai", "zai"`, the three `qwen-*` spellings, the `-anthropic` suffix ids) **are deleted** with the switch collapse (§3.2 item 3); only canonical ids resolve. No deprecation WARN names an old id.
- Note for operators re-keying their own config: `api_key_ref` is the credential name, not the provider id, so secrets do not need re-entering.
- The fresh-install seed (`pkg/config/defaults.go`, `config/config.example.json`) is written in canonical ids.

### 3.4 Not adopted

- Keeping Omnipus's own names and maintaining any mapping to the registry in code: two vocabularies for no gain.
- Treating protocol as a suffix in the canonical ids: models.dev does not, and it is what produced the duplicate-provider sprawl.

## 4. D12 — Provider tiers: every registry provider, a pinned Popular set

*(Operator direction 2026-08-22: review what ships today and what OpenCode and Hermes ship.)*

**Today, undeclared.** The factory switch accepts **36 distinct ids**; **~10** have a display name and a validation probe (`pkg/providers/displayname.go::knownDisplayNames`, `validate.go::probeModelDefaults`: `openrouter openai anthropic google/gemini groq deepseek zhipu/z-ai moonshot azure`); **4** have onboarding key-format hints (`src/lib/constants.ts`: `anthropic openai groq openrouter`). `azure` has a name but no factory case; `xai`, `mistral`, `ollama`, `minimax` have factory cases but no name or probe. The tiers exist; nobody decided them.

**The field** (research 2026-08-22, pinned commits): OpenCode exposes all 193 models.dev providers but pins **6** as "Popular" in its picker, documents 50, bundles code for 23. Hermes ships 37 plugins split into "First-Class API-Key Providers" (~13) and "Other Compatible Providers". Goose: 34 coded + 42 declarative. Crush/Catwalk: 41. Roo: 28, and it retired 9 in one PR as "low-usage". **Typical tier 1 is 5–15; the tail is 40 to unbounded.** Every harness but Gemini CLI offers a custom OpenAI-compatible endpoint.

**Decision (revised 2026-08-22 — operator: follow the OpenCode pattern exactly).** Every provider in the registry is selectable; a small "Popular" set is pinned in the picker; the rest sit behind "show all". No subscription login for Anthropic or Google (ADR-068 D13). **No new SDKs** — validated below.

### 4.1 Validation: can the existing transports reach all 193?

Omnipus speaks exactly two wire protocols today — **OpenAI-compatible HTTP** (`pkg/providers/http_provider.go`; the `google` case already uses Gemini's OpenAI-compatible endpoint `generativelanguage.googleapis.com/v1beta/openai`) and **Anthropic Messages** (`claude_provider.go`) — plus the CLI/OAuth specials. Checked against every registry provider's declared protocol (`npm`) on 2026-08-22:

| Registry protocol | Providers | Reachable with existing infra? |
|---|---|---|
| `@ai-sdk/openai-compatible` | **154** | **Yes, directly** — base URL, key variable and models all in the registry; **0 of the 154 lack a URL** |
| `@ai-sdk/anthropic` | **9** (minimax, minimax-cn, kimi-for-coding, …) | **Yes** — `claude_provider.go` with the registry's URL |
| `@ai-sdk/openai` | 4 (openai, meta, perplexity-agent, vivgrid) | Yes — same wire as openai-compatible |
| `@ai-sdk/google` | 1 (google) | Yes — via the OpenAI-compatible Gemini endpoint already in use; API key only |
| Dedicated SDKs that are OpenAI-compatible on the wire | ~20 (groq, mistral, xai, deepseek, cerebras, togetherai, deepinfra, perplexity, openrouter, cohere, azure, …) | **Yes, with a base URL from the override file** — the registry records no `api` for them (26 providers lack one; all are dedicated-SDK entries). Omnipus already hardcodes the OpenAI-compatible URLs for groq, mistral, deepseek and cerebras in `factory_provider.go`, which is the proof of shape. |
| **Cloud-IAM auth, not a key** | **~5**: `amazon-bedrock` (SigV4 request signing), `google-vertex`, `google-vertex-anthropic` (GCP service-account OAuth), `watsonx` (IBM IAM), `sap-ai-core` | **No** — these need request-signing or cloud-credential code Omnipus does not have. **Excluded**, listed in the provider table as `unsupported: cloud-iam`, revisitable per provider. |

**Result: 163 providers reachable from the registry alone, ~20 more with a URL row in the override file, ~5 excluded.** No new SDK, no new runtime dependency (Constraint #1 holds). The override file's URL rows are the one piece of hand-maintained data this adds, and it is ~20 lines.

*Caveat:* the "OpenAI-compatible on the wire" claim for the ~20 dedicated-SDK providers is established for the four Omnipus already ships and is my assessment for the rest from their public API docs; each is confirmed by the tier-1 probe at the time its URL row is added.

### 4.2 The tiers

- **Popular (pinned, ~6–8):** the OpenCode shape — `openai`, `openrouter`, `anthropic` (API key), `google` (API key), `xai`, `groq`, `mistral`, `deepseek`. Named, probed, guided, tested.
- **Everything else (~155):** selectable behind "show all providers", reachable through D11's protocol dispatch (§3) with URL, key variable and limits from the table. Best-effort; no probe, no onboarding hint, no test matrix.
- **Unsupported (~5):** the cloud-IAM set above, shown with the reason.
- **Custom endpoint stays** (any OpenAI- or Anthropic-compatible URL).
- A config naming a provider that is not in the table fails on the generic unknown-provider path (`rest_onboarding.go`). **There is no retired-provider list.**

### 4.3 The model selector reads the catalog — live `/models` is for entitlement and local endpoints only

*(Operator question 2026-08-22.)* Today the selector is filled by a live call to the provider's `/models` with the user's key (`pkg/providers/validate.go::FetchModels`, OpenAI-compatible only, via the gateway's `refreshProviderModels`) — because there was nothing else to read from. With the catalog there is.

**Decision.** The selector lists **the catalog** — instantly, offline, for every provider, with limits and modalities attached. Live `/models` is **not** used to populate it. Live calls remain for exactly **four** things the catalog cannot know (amended 2026-08-22, cross-spec X-18/X-36):

1. **Entitlement — what *this key* can use.** The catalog says what the provider offers, not what a given account, plan or org is allowed. An explicit *"Check with my account"* action calls `/models` with the key and **intersects** the result with the catalog: models the key cannot reach are shown greyed with the reason; models the provider returns that the catalog lacks (brand-new, ahead of the daily file) are shown with *limits unknown → floor* (ADR-066 D2/D3). The result is cached per `(provider id, SHA-256(key))` for the process lifetime, evicted on provider PUT/DELETE and on catalog refresh; it is never a boot-time or hot-path call. **Entitlement is per protocol** (amended §8b F-12): `openai-compatible` and `google` → `GET {api}/models`; `anthropic` → `GET {api}/v1/models` with Anthropic headers; `ollama` → `/api/tags`; `cli` and custom rows → not supported (409; the action is hidden).
2. **Local and self-hosted endpoints** — `ollama` (`/api/tags`), `vllm`, LM Studio, `custom`: the catalog cannot know what is installed, so live is the only source there, and limits come from `/api/show` / `max_model_len` — mandatory for local endpoints, never a floor (ADR-066 D3).
3. **Key validation** — the existing probe still POSTs one request to prove the key works, but picks its probe model **from the catalog** instead of fetching the list first.

4. **The ADR-066 D2 rung-3 limit query** — for cloud providers that publish per-model limits on their model endpoints (Anthropic, Google, OpenRouter, Mistral, Groq, xAI), queried **on demand** when an agent's window is first resolved, cached on disk for 24 h, **never on a timer and never at boot**; the catalog is the rung below it.

**Not done:** polling OpenRouter or anyone else on a timer to refresh the selector or the limits. The daily catalog pull is the refresh; entitlement and the limit query are on demand.

## 5. Contract impact (Constraint #8)

- One catalog file, `schema_version` 2.0.0 (providers with nested models); the binary reads 2.0.0 only — a persisted catalog at any other version is ignored in favour of the embedded snapshot (the same path as a checksum mismatch).
- D11's (§3) read-only providers-catalog endpoint (Settings picker) is a new wire surface: schema in `contracts/components/schemas/`, generated types, SPA consumes the generated type only. The `provider` field itself stays a free string — no enum — because the provider set is data (registry table + custom rows), not a compiled enum.
- **Full contract list (amended §8b F-11):** `GET /api/v1/providers/catalog` (`ProvidersCatalog`, `CatalogProvider`, `CatalogModel`; strong quoted ETag, 304); `POST /api/v1/providers/{id}/entitlement` (`EntitlementResponse.yaml`, shape shared with ADR-068; replaces `refresh-models`); `ProbeProviderRequest.id` enum → free string with `maxLength`; `Provider.protocol` and `ProviderUpdateRequest.protocol`; `Provider.status` enum shared verbatim with ADR-068: `connected | disconnected | error | unknown-provider | signed_in | expired`; `Agent.degraded_reason` enum `[needs_provider]` beside ADR-068's `needs_model: boolean` and ADR-066's three optional read-only window fields in one coordinated `Agent.yaml` edit (`needs_provider` wins in copy); `LLMError` code `needs_provider` in `LLMError.yaml`, `LLMErrorReplay.yaml` and the two inline `asyncapi.yaml` blocks; `ProbeProviderRequest` `{id, auth, api_key?, model?, api_base?, protocol?}`; `Provider.custom`, `Provider.protocol`, plus ADR-068's `auth_method`/`account_label`/`dependents`/`backs_default`/`updated_at`; `DefaultModel.yaml`/`DefaultModelUpdateRequest.yaml`; the `pkg/gateway/inboundschemas/` copies — all in ONE contract commit owned here (X-26); the default model is ADR-068's `DefaultModel.yaml` pair — no `model_name`; per-model window overrides are ADR-066's `ContextSettings.yaml`, not catalog fields; `GET /providers/model-capabilities` and `ModelCapabilities` deleted; `gen/main.go` and the generated TS constant deleted. `GET /api/v1/providers` returns configured providers only (no template rows).

## 6. Retired and not adopted

1. **A dedicated context-window registry** beside the capability catalog: duplicated embedding, refresh, checksum, versioning and persistence that `pkg/providers/capabilities` already implemented. Folded into one catalog instead (§2).
2. **A boot-time catalog fetch with no bundled fallback**: violates the single-binary offline-boot requirement; every surveyed harness ships a snapshot.
3. **OpenRouter as a generation source**: its terms forbid automated copying; live-query only.
4. **Hand-curation of the main table**: demonstrated stale (§2, *Why*).
5. **Keeping Omnipus's own provider names** with any mapping in code; **protocol as an id suffix** (§3.4).
6. **`resolveStrippedPrefix`**: an alias that returns the wrong route's limits under a provider+model key (§2).
7. **Release signing** (a built-in Ed25519 key, or sigstore/cosign) — not adopted 2026-08-22, checksum only; the accepted risk and remaining mitigations are recorded in §2 so this can be reopened without re-deriving the reasoning.

## 7. Implementation tasks

1. The assembly repository and its daily job (§2), publishing the single providers-with-models document; the Omnipus-side puller retarget, 24 h + startup refresh, the committed snapshot plus its weekly refresh PR, and checksum verification (no signature — §6 item 7).
2. `pkg/providers/capabilities` folded into `pkg/providers/catalog`; `catalog.Entries` loaded from the feed; factory switch collapsed to protocol dispatch; all ad-hoc alias strings and `-anthropic` suffix ids deleted; SPA key-format hint map re-keyed by canonical id; fresh-install seed written in canonical ids; `resolveStrippedPrefix` removed (§2).
3. The ~20 override URL rows for dedicated-SDK providers that are OpenAI-compatible on the wire (§4.1), each confirmed by its probe when added; the per-provider `resize_limits.json`.
4. The providers-catalog `GET` endpoint (§5) and the selector reading the catalog, with *Check with my account* (§4.3).

## 8. Unverified

- **[UNVERIFIED]** OpenAI / Anthropic native model-list endpoints publishing context length (needs the operator's keys; immaterial to D1 — the seed is generated from models.dev).
- **[UNVERIFIED]** The "OpenAI-compatible on the wire" claim for ~16 of the ~20 dedicated-SDK providers (§4.1 caveat); established for the four Omnipus already ships.
- ~~Whether the assembly feed can be consumed by the existing `GHReleasePuller` without change beyond owner/repo~~ — **verified 2026-08-22: it cannot** (2 MB cap, optional sidecar, `v`-prefixed version parser); the required changes are in §2 *Feed acceptance rules* and §8b.

## 8a. Pass-2 review resolutions (2026-08-22)

- **MAJ-005 — hermetic builds.** The embedded snapshot is **not** fetched at build time. It is a committed file in the Omnipus repo, refreshed by a scheduled pull request opened by the assembly repo's job (reviewed and merged like any change). `go build` reads only the tree.
- **MAJ-006 — signing** — decided: not adopted; see D1.
- **MAJ-010 — a provider id the binary does not know must not abort boot.** A config or agent entity naming an unknown provider degrades **per provider / per agent**: the provider row shows *"unknown provider — not in the catalog"*, agents bound to it show *"needs a provider"* and refuse to run with a typed error; the gateway and every other agent start normally. The current `instance.go` *"provider %q not found in configured providers"* path must be confirmed per-agent rather than registry-fatal at implementation (task).
- **MAJ-014 — disagreement handling.** While an issue is open for a field, the job publishes the **previously published value** for that field (last known good); if none exists, models.dev's value (the primary). A disagreement never blocks a release and never silently adopts the newer number.
- **MAJ-004, MAJ-015** — schema 2.0.0 everywhere; own exit proof below.

## 8b. Spec-review amendments (2026-08-22)

Applied from `docs/internal/specs/adr-067-registry-catalog-spec-review.md`; coordinator decisions 2026-08-22.

- **F-01** version `vYYYY.M.D[.N]` (§2). **F-02** missing sidecar rejected; sidecar from the release asset list (§2). **F-03** `api` URL validation on load with the local-endpoint exception (§2). **F-04** 5 % / 4,096-token tolerance; closed issues feed `overrides/` (§2). **F-05** 16 MB asset cap, named constant (§2).
- **F-06** catalog key is `(ModelConfig.provider, bare model id)`; `provider/` prefix and `ExtractProtocol` deleted (§3.2 item 3). **F-07/08/09** `custom` is a factory case, not a row; unknown id + `api_base` + `protocol` = custom row keyed by that id (§3.2 item 3). **F-10** `gen/main.go` and the TS constant deleted; SPA reads the `GET` (§3.2 item 4). **F-11** contract list (§5). **F-12** entitlement per protocol (§4.3). **F-13** `google` = HTTP transport at the OpenAI-compatible Gemini URL with a Bearer key (§3.2 item 3).
- **F-14 — `needs_provider` rule.** An agent is degraded (`degraded_reason: needs_provider`, turns refused with a typed error, nothing sent upstream) **iff its primary provider is unknown**. An unknown provider referenced only by a fallback candidate is dropped from the pool with one WARN naming the agent and the provider; the agent runs on the remaining pool. `needs_provider` takes precedence over ADR-068's derived `needs_model` in user-facing copy.
- **Cross-spec seams (2026-08-22, `docs/internal/specs/cross-spec-review-adr-066-067-068.md`).** (X-13) A provider config row whose id is not in the catalog carries `custom: true` on disk and on the wire; every check is on the flag, never on a literal id. (X-14/X-41) The `cli` protocol dispatches on an explicit row field `cli_kind ∈ {codex, copilot}` from the local-providers file — never by id matching; `github-copilot` joins that file (ADR-068 specifies the Copilot subprocess provider); `openai-chatgpt` is **not** `cli` — it is an HTTP transport (`protocol: openai-compatible`) with `token_source: codex-auth-json`, and is a cloud provider for ADR-066's window purposes. (X-16/X-17) **One definition of "local endpoint"**, exported from `pkg/providers/catalog` as `locality ∈ {local, cloud}`: local iff protocol ∈ {`ollama`, `vllm`} or id = `lmstudio` or (custom row and `api` host is loopback/private); `lmstudio` is a catalog id in the local-providers file, not a factory case; ADR-066 and ADR-068 reference `locality`, nothing else classifies. (X-10) The provider shape gains `company` (grouping key, from `overrides/`, default = `name`); (X-11) `subscription_policy` is dropped from the shape. (X-03) The entitlement cache key is `SHA-256(providerID + ":" + credentialRefName)` — the ref **name**, never the secret — evicted on provider DELETE and on a PUT that changes the key. (X-02) The typed refusal is `LLMError.code = needs_provider`, attribution `config`, copy *"This agent's provider isn't configured. Open Settings → Providers to connect one."*, added to all four hand-kept LLMError copies. (X-26) **ADR-067's implementation owns the single coordinated contract commit** for every shared schema file, including the values ADR-068 defines; ADR-068 owns their semantics.
- **F-15** every provider-id comparison in `pkg/agent`, `pkg/gateway`, `pkg/providers` is exact after `TrimSpace` — `instance.go::findModelConfigForProvider`'s `EqualFold` is replaced. **F-16** disposition of every current factory case is in §3.3 (`azure` → `unsupported: deployment-url`). **F-18** no code reads, deletes, or mentions the old `capabilities_catalog.json`.
- **Refresh and staleness.** A successful refresh logs one INFO with the new version; when `updated_at` is older than 14 days the `GET` carries `stale: true` and `/health` reports the catalog degraded with the last refresh error. The startup pull is skipped when the persisted document is less than 1 h old (GitHub's unauthenticated rate limit). There is no manual refresh endpoint. A catalog refresh does **not** rebuild already-constructed provider instances; a changed `api` takes effect at the next agent reload.
- **Rows that vanish upstream.** A provider or model that disappears from models.dev is carried forward from the last published document with `status: retired` (models) or `tier: unsupported`, `unsupported_reason: withdrawn` (providers) — never silently dropped.

## 9. Exit proof

1. **Exact resolution** — `Resolve(provider, model)` returns the route's own limits: `(openrouter, z-ai/glm-5.2)` → 1,048,576 and `(zai, glm-5.2)` → 1,000,000; `(openrouter, glm-5.2)` is a miss, not a prefix-stripped hit.
2. **Feed integrity** — a release whose `.sha256` does not match, **or has no `.sha256`**, or exceeds 16 MB, or carries a non-`https` / private-host `api` for a hosted provider, is rejected and the embedded snapshot serves, with one WARN naming the reason; a release at any `schema_version` other than 2.0.0 is ignored the same way. `v2026.8.9 < v2026.8.10` and `v2026.9.30 < v2026.10.1` order correctly.
3. **Offline selector** — with the network down, the model picker lists every catalog provider and model with limits and modalities attached; no live call is made to populate it.
4. **Protocol dispatch** — `factory_provider.go` has no `case` on a vendor name; the ~5 protocol cases construct every reachable provider from the table's URL, key variable and protocol.
5. **Greenfield** — `grep -rnE '_migrated|alias|deprecat' pkg/providers pkg/config` returns nothing provider-related (the catalog's `status: retired` token and the search-only `aliases[]` field are the two allowed exceptions); a config with `provider: "z-ai"` or `"moonshot-cn-anthropic"` fails as unknown-provider with no rename and no WARN naming a canonical id. The proof asserts the **absence of a canonical id** in the error, log, and API bodies — it does not assert on the echoed user-supplied id, whose wording (`unknown provider %q`) ADR-068 shares.
6. **No antigravity** — `grep -ri antigravity pkg cmd src contracts config docs` returns only historical decision records (the deletion itself is ADR-068 §2.4).
