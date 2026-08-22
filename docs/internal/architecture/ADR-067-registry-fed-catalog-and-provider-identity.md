# ADR-067: One registry-fed catalog — model limits and provider identity from public registries

- **Status:** Proposed (2026-08-22). Split out of ADR-066 after its second adversarial review; awaiting operator ratification.
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

The provider+model key is the nesting; a model's limits live under the route that serves it. The assembly job publishes **one** signed file. `pkg/providers/capabilities` is folded into `pkg/providers/catalog`; `Resolve(provider, model)` is the only lookup; the media pipeline, the agent loop, Settings and the picker all read the same in-memory document. Two files kept in step would have been the same mistake as thirty-six provider ids kept in step.

*What exists.* `pkg/providers/capabilities` already has every piece of the runtime machinery: an embedded seed (`embed.go`), a checksum-verified `GHReleasePuller` that fetches the release asset `providers_capabilities.json` + `.sha256` from GitHub with a raw fallback (`pkg/gateway/gateway.go` wires it to `elicify-ai/omnipus`, interval `capabilityCatalogRefreshInterval = 7 * 24 * time.Hour`, timeout 30 s), a semver-aware refresh that cannot downgrade, a persisted store, a validated DTO → domain parse, and `Catalog.Resolve`. **Its `resolveStrippedPrefix` fallback — which maps `z-ai/glm-5.2` to a bare `glm-5.2` by dropping the provider — is removed:** under the provider+model key it is an alias that returns the *wrong route's* limits (1,000,000 for a request that goes via OpenRouter's 1,048,576). Lookup is exact on (provider, model); a miss falls to the ADR-066 D2 ladder. **Nothing else in that machinery changes.** What changes is where the file comes from: today a human reads provider docs and writes the JSON (`source: "freeze-gate re-validation 2026-07-28 …"`; no generator exists in `scripts/`).

*Why.* Validated live on 2026-08-22 against all 78 seeded models: **models.dev** (MIT; 193 providers, 7,246 models; regenerated hourly; correctable by PR) carries `limit.context`, `limit.output` and `modalities.input` incl. `image` and `pdf` on **every** entry. Cross-checked against **LiteLLM's** `model_prices_and_context_window.json` (MIT; 3,111 entries; independently maintained): where the hand-curated seed and models.dev disagreed (19 models on PDF, 4 on image) and LiteLLM could adjudicate, **it sided with models.dev every time** — `gpt-4o`/`gpt-4.1`/`o3` accept PDFs (seed said no), `o3-mini` does not accept images (seed said yes). The hand-typed catalog is the stale one. The field agrees: OpenCode, Kilo, Hermes, Cline and Goose consume models.dev; Crush and OpenClaw run their own published feeds (`catwalk.charm.land`, `catalog.openclaw.ai`) — the shape adopted here.

*The assembly repository* (open source, separate from Omnipus) runs a daily job that:

1. pulls models.dev `api.json` and LiteLLM's JSON, recording the upstream commits in a manifest;
2. merges into the Omnipus schema — `context_window`, `max_output_tokens`, `input_modalities`, tool-calling, deprecation status — **keyed by provider + model**, because limits differ by route (`z-ai/glm-5.2`: 1,048,576 via OpenRouter, 1,000,000 direct);
3. applies `overrides/` (local corrections that win over both registries — e.g. `gemini-3-pro` PDF, where the registries disagree and the provider accepts PDFs in practice — and legacy models the registries have retired) and `resize_limits.json`;
4. **opens an issue on any disagreement between the two registries rather than silently choosing** — the discipline that exposed the stale seed;
5. publishes **one** file — `providers_catalog.json` + `.sha256` — as a GitHub Release, `schema_version` 2.0.0 (new shape, greenfield; the old `providers_capabilities.json` is not produced).

*Closing the resize gap.* `resize_budget` (`long_edge_px`, `max_bytes`) is in **neither** registry (searched both). It is not a model fact but a provider's upload limit, documented once per vendor — the 78-model seed uses exactly **four distinct values**, one per vendor. It lives in the assembly repo as a small per-provider table, hand-maintained and PR-reviewed; the job joins it onto every model of that provider.

*Omnipus-side changes* — four, all small: the catalog schema becomes 2.0.0 (providers with nested models, *One catalog, not two* above); the puller's owner/repo points at the assembly repo (asset name and sidecar unchanged); the refresh interval drops from 7 days to **24 hours, plus one background pull at startup** (never blocking boot; the existing 30 s timeout applies); and the `go:embed` snapshot is **generated from the same feed at build time**, so offline boot and online refresh agree on schema.

*Signing — not adopted (operator decision 2026-08-22).* Releases carry the existing `.sha256` checksum sidecar and nothing more: no release signature, no new dependency, no key to rotate. **Accepted risk, stated plainly:** a checksum proves integrity in transit, not authorship, so a hijacked download — a compromised publishing account or a tampered mirror — would be accepted by every install; a fake catalog could set wrong limits (dead turns) or, more seriously, wrong provider endpoints, sending API keys to a server the attacker controls. **Mitigations that remain:** the release is fetched only from the pinned owner/repo over HTTPS; the puller never downgrades versions; the embedded snapshot is the fallback when the fetch fails or the checksum does not match.

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
3. **The factory switch dispatches on protocol, not on provider name.** ~40 cases become ~5 (`openai-compatible`, `anthropic`, `google`, `ollama`, `cli`); base URL, key variable and defaults come from the table. A provider unknown to the table but with an explicit endpoint is still accepted as `custom` (the existing `rest_onboarding.go` path).
4. **The assembly repo publishes one document** — providers with nested models (§2, D1). **`pkg/providers/catalog` becomes the single catalog package**: `capabilities` folds into it, `Entries` stops being a hand-typed Go slice and is loaded from the feed (embedded snapshot + refreshed copy). `gen/main.go` inverts: it generates the SPA file *from the feed*, not from Go. Providers with no registry entry stay in a local file shipped with the feed: `ollama`, `vllm`, `litellm`, `custom`, `codex-cli`, `shengsuanyun`, `volcengine`, `avian`, `mimo`. (`novita` is `novita-ai` in the registry; listed in the §3.3 reference table.)

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

**Decision.** The selector lists **the catalog** — instantly, offline, for every provider, with limits and modalities attached. Live `/models` is **not** used to populate it. Live calls remain for exactly three things the catalog cannot know:

1. **Entitlement — what *this key* can use.** The catalog says what the provider offers, not what a given account, plan or org is allowed. An explicit *"Check with my account"* action calls `/models` with the key and **intersects** the result with the catalog: models the key cannot reach are shown greyed with the reason; models the provider returns that the catalog lacks (brand-new, ahead of the daily file) are shown with *limits unknown → floor* (ADR-066 D2/D3). The result is cached per key; it is never a boot-time or hot-path call.
2. **Local and self-hosted endpoints** — `ollama` (`/api/tags`), `vllm`, LM Studio, `custom`: the catalog cannot know what is installed, so live is the only source there, and limits come from `/api/show` / `max_model_len` — mandatory for local endpoints, never a floor (ADR-066 D3).
3. **Key validation** — the existing probe still POSTs one request to prove the key works, but picks its probe model **from the catalog** instead of fetching the list first.

**Not done:** polling OpenRouter or anyone else on a timer to refresh the selector. The daily catalog pull is the refresh; per-key entitlement is on demand.

## 5. Contract impact (Constraint #8)

- One catalog file, `schema_version` 2.0.0 (providers with nested models); the binary reads 2.0.0 only — a persisted catalog at any other version is ignored in favour of the embedded snapshot (the same path as a checksum mismatch).
- D11's (§3) read-only providers-catalog endpoint (Settings picker) is a new wire surface: schema in `contracts/components/schemas/`, generated types, SPA consumes the generated type only. The `provider` field itself stays a free string — no enum — because the provider set is data (registry table + `custom`), not a compiled enum.

## 6. Retired and not adopted

1. **A dedicated context-window registry** beside the capability catalog: duplicated embedding, refresh, checksum, versioning and persistence that `pkg/providers/capabilities` already implemented. Folded into one catalog instead (§2).
2. **A boot-time catalog fetch with no bundled fallback**: violates the single-binary offline-boot requirement; every surveyed harness ships a snapshot.
3. **OpenRouter as a generation source**: its terms forbid automated copying; live-query only.
4. **Hand-curation of the main table**: demonstrated stale (§2, *Why*).
5. **Keeping Omnipus's own provider names** with any mapping in code; **protocol as an id suffix** (§3.4).
6. **`resolveStrippedPrefix`**: an alias that returns the wrong route's limits under a provider+model key (§2).
7. **Release signing** (a built-in Ed25519 key, or sigstore/cosign) — not adopted 2026-08-22, checksum only; the accepted risk and remaining mitigations are recorded in §2 so this can be reopened without re-deriving the reasoning.

## 7. Implementation tasks

1. The assembly repository and its daily job (§2), publishing the single providers-with-models document; the Omnipus-side puller retarget, 24 h + startup refresh, build-time snapshot generation, and checksum verification (no signature — §6 item 7).
2. `pkg/providers/capabilities` folded into `pkg/providers/catalog`; `catalog.Entries` loaded from the feed; factory switch collapsed to protocol dispatch; all ad-hoc alias strings and `-anthropic` suffix ids deleted; SPA key-format hint map re-keyed by canonical id; fresh-install seed written in canonical ids; `resolveStrippedPrefix` removed (§2).
3. The ~20 override URL rows for dedicated-SDK providers that are OpenAI-compatible on the wire (§4.1), each confirmed by its probe when added; the per-provider `resize_limits.json`.
4. The providers-catalog `GET` endpoint (§5) and the selector reading the catalog, with *Check with my account* (§4.3).

## 8. Unverified

- **[UNVERIFIED]** OpenAI / Anthropic native model-list endpoints publishing context length (needs the operator's keys; immaterial to D1 — the seed is generated from models.dev).
- **[UNVERIFIED]** The "OpenAI-compatible on the wire" claim for ~16 of the ~20 dedicated-SDK providers (§4.1 caveat); established for the four Omnipus already ships.
- **[UNVERIFIED]** Whether the assembly feed can be consumed by the existing `GHReleasePuller` without change beyond owner/repo — asset name and `.sha256` sidecar are unchanged by design.

## 9. Exit proof

1. **Exact resolution** — `Resolve(provider, model)` returns the route's own limits: `(openrouter, z-ai/glm-5.2)` → 1,048,576 and `(zai, glm-5.2)` → 1,000,000; `(openrouter, glm-5.2)` is a miss, not a prefix-stripped hit.
2. **Feed integrity** — a release whose `.sha256` does not match is rejected and the embedded snapshot serves, with one WARN; a release at any `schema_version` other than 2.0.0 is ignored the same way.
3. **Offline selector** — with the network down, the model picker lists every catalog provider and model with limits and modalities attached; no live call is made to populate it.
4. **Protocol dispatch** — `factory_provider.go` has no `case` on a vendor name; the ~5 protocol cases construct every reachable provider from the table's URL, key variable and protocol.
5. **Greenfield** — `grep -rnE '_migrated|alias|deprecat|retired' pkg/providers pkg/config` returns nothing provider-related; a config with `provider: "z-ai"` or `"moonshot-cn-anthropic"` fails as unknown-provider with no rename and no WARN naming a canonical id.
6. **No antigravity** — `grep -ri antigravity pkg cmd src contracts config docs` returns only historical decision records (the deletion itself is ADR-068 §2.4).
