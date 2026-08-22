# Spec — ADR-067: registry-fed catalog and provider identity (D1 · D11 · D12)

- **Source ADR:** `docs/internal/architecture/ADR-067-registry-fed-catalog-and-provider-identity.md` (Proposed 2026-08-22; §8a pass-2 resolutions MAJ-005/006/010/014 folded in). Companion review: `docs/internal/architecture/ADR-066-context-budget-and-tool-result-routing-review-pass2.md` (MAJ-004/005/006/010/014/015, MIN-003/004, open questions 7–8).
- **Status:** Draft (plan-spec) · **Phase 1 gate: the ADR is treated as the confirmed brief** (operator unavailable for this pass). Where the ADR is silent the spec does NOT invent: each gap is in §9 with the assumption the spec proceeds under, labelled `[A-n]` wherever it is used. The operator resolves §9 afterwards.
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
| `pkg/agent/instance.go::findModelConfigForProvider`, `buildProviderPool`, `resolveAgentPrimaryProvider`; `pkg/agent/registry.go` (`NewAgentInstance` loop at ::88) | **verifies / modifies** | `buildProviderPool` already **skips with WARN** on a missing ModelConfig or a `CreateProviderFromConfig` error (non-fatal). MAJ-010 requires confirming the whole boot path is per-agent non-fatal and adding the typed refusal at turn time. |
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
2. **Given** the two registries disagree on a field, **When** the job publishes, **Then** the published value is the previously published value for that field (last known good), or models.dev's value when none exists, and an issue is opened; the release is never blocked and the newer number is never silently adopted.
3. **Given** a provider present in models.dev with no `api` URL but OpenAI-compatible on the wire, **When** the job publishes, **Then** the provider carries the URL from `overrides/` and is marked reachable; a cloud-IAM provider carries `unsupported: cloud-iam` and no URL requirement.
4. **Given** a provider absent from models.dev but shipped by Omnipus (`ollama`, `vllm`, `litellm`, `custom`, `codex-cli`, `shengsuanyun`, `volcengine`, `avian`, `mimo`), **When** the job publishes, **Then** it appears in the document from the local provider file with the same shape as registry providers.
5. **Given** the document is published, **When** an Omnipus release is cut, **Then** the committed snapshot in the Omnipus repo is the document from a scheduled pull request, and `go build` reads nothing from the network.

### US-3 — Refresh: retargeted puller, 24 h + startup, checksum and schema gates, last known good — **P0**
The running gateway keeps its catalog fresh without ever blocking boot or trusting a bad file. *Why P0:* the offline/online agreement and integrity behaviour are the ADR's exit proofs 2 and 3. *Independent test:* stub the release host; observe what the in-memory catalog serves after each outcome.

1. **Given** the gateway starts with no network, **When** boot completes, **Then** the embedded snapshot serves every lookup and boot time is not extended by the refresh attempt.
2. **Given** the gateway starts with network, **When** the background startup pull succeeds, **Then** the pulled document replaces the snapshot in memory and is persisted as last known good, within the 30 s pull timeout.
3. **Given** a running gateway, **When** 24 h elapse since the last attempt, **Then** another pull is attempted; no pull is attempted on any request path.
4. **Given** a pulled asset whose checksum does not match its sidecar, **When** refresh runs, **Then** the asset is rejected, exactly one WARN is logged naming the mismatch, and the current document continues to serve.
5. **Given** a pulled document whose `schema_version` ≠ `2.0.0`, **When** refresh runs, **Then** it is ignored the same way (one WARN, current document retained).
6. **Given** a pulled document whose `version` is lower than the current one, **When** refresh runs, **Then** it is rejected (no downgrade) and the current document is retained.
7. **Given** a persisted last-known-good file whose schema is not 2.0.0 or whose checksum envelope is unreadable, **When** the gateway boots, **Then** it is ignored and the embedded snapshot serves (no notice about the old file beyond one WARN).
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
4. **Given** a config with `provider: custom` and an explicit `api_base`, **When** constructed, **Then** it is accepted without a catalog row (protocol from config, default openai-compatible).
5. **Given** the factory source, **When** inspected, **Then** there is no `case` on a vendor name; the cases are exactly the protocol set (`openai-compatible`, `anthropic`, `google`, `ollama`, `cli`) `[A-7]`.
6. **Given** a config with `provider: z-ai`, `moonshot-cn-anthropic`, `qwen-intl`, or any non-canonical spelling, **When** constructed, **Then** it fails as *unknown provider* with a typed error that does **not** name a canonical id.
7. **Given** the fresh-install seed and `config/config.example.json`, **When** inspected, **Then** every provider id is a catalog id.

### US-6 — Unknown provider degrades per provider and per agent; boot never aborts — **P0**
An operator whose config names an unknown provider still reaches Settings to fix it. *Why P0:* MAJ-010; the alternative is an install with no UI path to repair. *Independent test:* boot with one unknown provider and two agents (one bound to it).

1. **Given** config with providers `openai` and `nope`, and agents A (openai) and B (nope), **When** the gateway boots, **Then** boot succeeds, A runs turns normally, and the providers list shows `nope` in an *unknown provider* state.
2. **Given** the same, **When** B is asked to run a turn, **Then** the turn is refused with a typed error stating the agent needs a provider, nothing is sent upstream, and the agent list marks B as needing a provider `[A-16]`.
3. **Given** the operator re-points B to `openai` through the existing agent update path, **When** saved, **Then** B runs without a restart beyond the existing reload mechanism.
4. **Given** an unknown provider, **When** the providers list is read, **Then** no rename, alias, or suggestion of a canonical id is produced anywhere (log, API, UI text).

### US-7 — Read-only providers-catalog endpoint — **P0**
The SPA and any client read the same document the gateway uses, through a contract-defined surface. *Why P0:* Constraint #8; ADR-068's picker cannot be built without it. *Independent test:* `GET` with and without auth; compare to the in-memory catalog; ETag round trip.

1. **Given** an authenticated request, **When** `GET /api/v1/providers/catalog` is called, **Then** the response is the full 2.0.0 document (providers with nested models, tier, protocol(s), unsupported reason, resize limits) plus `version`, `updated_at` and a `source` marker (`embedded` / `pulled`) `[A-1]`.
2. **Given** an unauthenticated request, **When** called, **Then** 401.
3. **Given** a client sending `If-None-Match` with the current document's ETag, **When** called, **Then** 304 with no body `[A-1]`.
4. **Given** the catalog failed to construct at boot, **When** called, **Then** 503 with a typed error (never an empty 200 that looks like "no providers") `[A-12]`.
5. **Given** the response, **When** validated against the generated schema, **Then** it validates; the SPA consumes only the generated type.

### US-8 — Tiers are data: every provider selectable, Popular pinned, cloud-IAM visible-disabled, custom endpoint — **P1**
The operator can pick any registry provider; the picker knows which are popular and which are unsupported from the document, not from code. *Why P1:* the data must exist for ADR-068's UI; the UI itself is out of scope. *Independent test:* inspect the document and the endpoint output.

1. **Given** the document, **When** providers are listed, **Then** each carries `tier ∈ {popular, standard, unsupported}` `[A-9]`; the popular set is `openai, openrouter, anthropic, google, xai, groq, mistral, deepseek` (ADR §4.2, ~8 pinned).
2. **Given** a cloud-IAM provider (`amazon-bedrock`, `google-vertex`, `google-vertex-anthropic`, `watsonx`, `sap-ai-core`), **When** listed, **Then** `tier: unsupported` with `unsupported_reason: cloud-iam`, and **When** configured via PUT, **Then** 400 with the reason.
3. **Given** a standard-tier provider, **When** configured with a key, **Then** it is accepted and reachable through protocol dispatch with no probe requirement.
4. **Given** `custom`, **When** configured with `api_base` and a protocol, **Then** it is accepted; `api_base` missing → 400.

### US-9 — The selector reads the catalog; live `/models` only for entitlement, local endpoints and the probe — **P1**
The operator sees the model list instantly and offline; a live call happens only when they ask what *their key* can use, when the endpoint is local, or when a key is being validated. *Why P1:* exit proof 3 depends on the data path; the surface's look is ADR-068. *Independent test:* network down, list models for `anthropic`; then trigger the entitlement action with a stub.

1. **Given** no network, **When** the provider's models are requested through the providers API, **Then** the catalog's models for that provider are returned with limits and modalities, and no outbound request is made.
2. **Given** an explicit entitlement check for provider P with key K `[A-13]`, **When** invoked, **Then** `/models` is called once with K; the result is the catalog list annotated `entitled: true/false`, plus models the provider returned that the catalog lacks marked `limits: unknown`; the result is cached per key and never fetched at boot or on a turn.
3. **Given** `ollama`/`vllm`/`custom`, **When** models are requested, **Then** the live listing is the source (`/api/tags` for ollama; `/v1/models` otherwise) and the catalog is not consulted for the list.
4. **Given** a key validation (`/providers/{id}/test`, onboarding probe), **When** it runs, **Then** the probe model is chosen from the catalog (first `status: active`, tool-calling, text-modality model of that provider `[A-20]`) and no `/models` pre-fetch is made for catalog providers.

### US-10 — `ProbeProviderRequest.id` becomes a free string validated against the catalog — **P0**
Onboarding can probe any of ~190 providers without a 61-value enum. *Why P0:* contract change that blocks US-8/US-9 and ADR-068. *Independent test:* probe `zai` (not in today's enum) and `z-ai` (in today's enum).

1. **Given** `id: "zai"`, **When** probed, **Then** the probe runs against `zai`'s catalog URL.
2. **Given** `id: "z-ai"`, **When** probed, **Then** 400 "unknown provider" (no hint).
3. **Given** `id: "custom"` with `endpoint`, **When** probed, **Then** the probe runs against `endpoint`; without `endpoint`, 400.
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
| E6 | Persisted last-known-good is newer than the embedded snapshot | Persisted wins at boot (existing behaviour). |
| E7 | Embedded snapshot fails its own validation (bad commit) | Gateway boots with **no** catalog; every catalog consumer degrades (media optimistic; window → ladder; `GET /providers/catalog` → 503); one ERROR at boot. |
| E8 | Provider `status: retired` on every model the agent uses | Agent still constructs; model selection UI flags it (ADR-068); no refusal at turn time. |
| E9 | Unicode in provider `name` / model `name` | Preserved byte-for-byte through load and `GET`. |
| E10 | Catalog refresh lands mid-session | `GET /providers/catalog` ETag changes; SPA cache rule is `[A-1]`; no WS push (pass-2 Q8 → ambiguity). |
| E11 | Release exists but has no `.sha256` sidecar | Rejected (existing `Pull_NoSidecar` behaviour), current retained. |
| E12 | `custom` provider with `protocol: google` or `ollama` | 400 — `custom` accepts `openai-compatible` or `anthropic` only (ADR §4.2 "any OpenAI- or Anthropic-compatible URL"). |

---

## 5. Behavioral Contract & Boundaries

### Behavioral Contract

- When a 2.0.0 document loads, the system serves exact `(provider, model)` facts to every consumer from one in-memory copy.
- When a document fails checksum, schema-version, invariant, size, or anti-downgrade checks, the system logs one WARN and keeps serving the previous document.
- When the gateway boots, the embedded snapshot (or newer persisted last-known-good) serves immediately; one background pull starts; boot is never delayed or aborted by it.
- When 24 h pass, the system pulls again; it never pulls on a request or turn.
- When a lookup misses, the system says *miss*; it never strips a prefix to find a neighbouring route.
- When a provider id is not in the catalog and not `custom`-with-endpoint, the system marks that provider unknown and the agents bound to it as needing a provider; boot and other agents proceed; no rename or hint is produced.
- When a transport is constructed, the system dispatches on the provider's protocol and takes URL and key variable from the catalog.
- When the SPA needs providers or models, the system serves the catalog via `GET /api/v1/providers/catalog`; a live `/models` call occurs only on an explicit entitlement check, for local endpoints, or inside a key validation.
- When a probe request names a provider, the system validates the id against the catalog at runtime (no enum).
- When a cloud-IAM provider is configured or probed, the system returns 400 with the `cloud-iam` reason.

### Explicit Non-Behaviors & Safeguards

#### Qualitative Prohibitions
- The system must not map, alias, or suggest canonical ids for old provider ids, because the greenfield rule forbids compatibility machinery and the §3.3 table is documentation only.
- The system must not fetch the catalog at build time, because builds must be hermetic (MAJ-005).
- The system must not verify a release signature or add a signing dependency, because signing was explicitly not adopted (ADR §2, §6 item 7); accepted risk is recorded there.
- The system must not poll any provider's `/models` on a timer or at boot, because the daily catalog pull is the refresh (ADR §4.3 "Not done").
- The system must not populate the selector from live `/models` for catalog providers, because the catalog is the source and offline listing is an exit proof.
- The system must not abort boot, refuse to start the gateway, or hide Settings because a provider id is unknown (MAJ-010).
- The system must not keep a second catalog file, package, or Go slice of providers, because one document is the decision ("One catalog, not two").
- The system must not strip provider prefixes during lookup, because that returns another route's limits.
- The system must not silently adopt a newer registry value during a disagreement (assembly side), because last-known-good is the decision (MAJ-014).
- The system must not implement cloud-IAM (SigV4/GCP OAuth/IBM IAM) transports, because they are excluded pending per-provider ADRs and Constraint #1.
- The system must not specify or change picker layout, grouping, "show all" affordances, or the entitlement button's UX — that is ADR-068.

#### Machine-Verifiable Constraints
- **HTTP:** `GET /api/v1/providers/catalog` → 200 + document; no auth → 401; `If-None-Match` match → 304 empty body; catalog unavailable → 503 `{"error":"provider catalog unavailable"}`. `PUT /api/v1/providers/{id}` with unknown id → 400 `{"error":"unknown provider \"<id>\""}`; with cloud-IAM id → 400 `{"error":"provider \"<id>\" is unsupported: cloud-iam"}`; `custom` without `api_base` → 400. `POST /api/v1/onboarding/probe-provider` same 400 vocabulary. Entitlement check `[A-13]` → 200 annotated list; provider has no key → 422 (existing `describeCredentialResolutionError` vocabulary).
- **Performance:** `GET /providers/catalog` serves from memory; p95 ≤ 50 ms for a ≤ 8 MB document on the CI worker; catalog lookup `Resolve(provider, model)` is O(1) map access, ≤ 1 µs p99 in a benchmark; startup pull bounded by 30 s and never on the boot critical path (boot-to-listen time unchanged ± 5 %).
- **Size:** pulled document > 16 MB `[A-18]` rejected before parse; embedded snapshot ≤ 8 MB `[A-2]` enforced by a test.
- **Logging:** rejections log exactly one line at WARN with keys `reason ∈ {checksum, schema_version, invalid, regressed, too_large}`; embedded-snapshot validation failure logs ERROR once.
- **Source:** `grep -rnE 'case "(z-ai|zhipu|moonshot|qwen|deepseek|groq|mistral|openrouter|gemini|minimax|nvidia|cerebras)' pkg/providers/factory_provider.go` → 0 lines. `grep -rn resolveStrippedPrefix pkg` → 0. `ls pkg/providers/capabilities` → not found. Exactly one `//go:embed` of a catalog JSON in `pkg/providers`.
- **Contract:** `make verify-contracts` exit 0; `ProbeProviderRequest.id` has no `enum`; schemas `ProvidersCatalog`, `CatalogProvider`, `CatalogModel`, `ProviderEntitlement` `[A-13]` exist under `contracts/components/schemas/`.

### Integration Boundaries

**Assembly repository → Omnipus (the feed).**
- *Data in:* one GitHub Release per day on the assembly repo (owner/repo pinned in Go `[A-5]`), assets `providers_catalog.json` and `providers_catalog.json.sha256` (format: `<64 hex>` or `<64 hex>  providers_catalog.json`). Also reachable at the raw URL of the default branch (existing fallback path).
- *Document shape (2.0.0):* top level `schema_version` (`"2.0.0"`), `version` (monotonic, semver-comparable — `YYYY.M.D` `[A-6]`), `updated_at` (RFC 3339), `source` (free text with upstream commit ids), `default_resize_limits {long_edge_px, max_bytes}` `[A-10]`, `providers[]`. Provider: `id` (models.dev id or local-file id), `name` `[A-14]`, `api` (base URL; empty only when `unsupported`), `protocol` (primary, one of `openai-compatible|anthropic|google|ollama|cli`), `protocols[]` (all offered, each with its own `api`) `[A-8]`, `env` (key variable name), `region` (optional), `plan` (optional), `tier` (`popular|standard|unsupported`) `[A-9]`, `unsupported_reason` (`cloud-iam` when tier unsupported), `subscription_policy` (opaque to this spec; ADR-068 consumes), `resize_limits {long_edge_px, max_bytes}`, `models[]`. Model: `id`, `name` `[A-14]`, `context_window` (int, 0 = unknown), `max_output_tokens` (int, 0 = unknown), `input_modalities[]` (must include `text`), `tool_call` (bool), `status` (`active|retired`) `[A-3]`, `disputed` (bool, optional) `[A-22]`.
- *Inputs to the job (for the record, not enforced by Omnipus):* models.dev `api.json`, LiteLLM `model_prices_and_context_window.json`, `overrides/` (wins over both), `resize_limits.json` (per provider, joined onto every model), a local-provider file for ids absent from models.dev, a manifest of upstream commits.
- *Failure behaviour:* unreachable / 404 / rate-limited → raw fallback → otherwise retain current, WARN; checksum mismatch → reject, WARN; wrong schema → ignore, WARN; oversize → reject, WARN. Omnipus never blocks on the feed.
- *Development approach:* simulated twin — `httptest` servers replaying fixture releases (existing `puller_test.go` pattern), plus one conformance fixture `testdata/providers_catalog_2.0.0_fixture.json` shared by Go tests and, by copy, the assembly repo's own tests.

**Omnipus → LLM providers (live calls that remain).**
- `/models` (OpenAI-compatible) only on entitlement check and inside key validation for non-catalog providers; `/api/tags` and `/api/show` for ollama; `/v1/models` `max_model_len` for vLLM (ADR-066 D3 owns the window semantics). Failure → annotated "unknown", never a selector wipe. SSRF-safe client retained (SEC-24).

**Omnipus → SPA.**
- `GET /api/v1/providers/catalog` (new), `POST /api/v1/providers/{id}/entitlement` `[A-13]` (replaces `refresh-models`), `POST /onboarding/probe-provider` (id now free string). Removed: `GET /providers/model-capabilities` `[A-12]`, `src/lib/generated/providerCatalog.ts`.

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
When `ollama`, `vllm`, `litellm`, `custom`, `codex-cli`, `shengsuanyun`, `volcengine`, `avian`, `mimo` are looked up
Then each is present with `protocol` set and validates like any other provider.

**Scenario (HP): hermetic build** — Traces to US-2.AC5
Given the network is unavailable
When `make build` runs
Then it succeeds and the binary's embedded catalog equals the committed file byte-for-byte.

**Scenario (HP): offline boot serves the snapshot** — Traces to US-3.AC1
Given the release host is unreachable
When the gateway boots
Then `GET /providers/catalog` returns the snapshot's `version` with `source: embedded`, and boot-to-listen is within the baseline.

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

**Scenario (EC): stale persisted file ignored at boot** — Traces to US-3.AC7
Given `$OMNIPUS_HOME` contains the old `capabilities_catalog.json` (1.0.0) and no `providers_catalog.json` `[A-4]`
When the gateway boots
Then the embedded snapshot serves and the old file is neither read into state nor mentioned beyond one WARN.

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

**Scenario (AP): custom endpoint without a catalog row** — Traces to US-5.AC4
Given `{provider: custom, api_base: https://llm.example/v1, api_key: k}`
When constructed
Then an HTTPProvider at that base.

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
Then 304, empty body; and after a refresh to a new version, 200 with a different ETag.

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
When `PUT /providers/custom` without `api_base` → 400; with `api_base` and `protocol: anthropic` → 200.

**Scenario (HP): offline model list** — Traces to US-9.AC1
Given no network and `anthropic` configured
When `GET /providers/anthropic`
Then `models[]` equals the catalog's `anthropic` models with limits, and the outbound request counter is 0.

**Scenario (AP): entitlement check intersects** — Traces to US-9.AC2
Given a stub `/models` returning `[claude-x, brand-new-model]` and a catalog with `[claude-x, claude-y]`
When the entitlement check runs
Then `claude-x: entitled`, `claude-y: not entitled`, `brand-new-model: limits unknown`; a second call with the same key hits the cache (stub counter stays 1).

**Scenario (AP): local endpoints are live** — Traces to US-9.AC3
Given `ollama` with a stub `/api/tags`
When models are requested
Then the stub's list is returned and the catalog's `ollama` entry contributes no model ids.

**Scenario (HP): probe model from the catalog** — Traces to US-9.AC4
Given `zai` and a recording stub
When `/providers/zai/test` runs
Then exactly one POST (the completion probe) and zero GET `/models`, and the probe model is the catalog's first active tool-calling text model for `zai`.

**Scenario (HP): probe a registry id** — Traces to US-10.AC1
When `POST /onboarding/probe-provider {id: zai, api_key: k}` against a stub → the stub at `zai`'s URL receives the probe.

**Scenario (EP): probe an old id** — Traces to US-10.AC2
When `{id: z-ai}` → 400 `unknown provider "z-ai"`, and the body does not contain `zai`.

**Scenario (AP/EP): probe custom** — Traces to US-10.AC3
When `{id: custom, endpoint: …}` → probe runs there; without `endpoint` → 400.

**Scenario (EP): probe cloud-IAM** — Traces to US-10.AC4
When `{id: amazon-bedrock}` → 400 containing `cloud-iam`.

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
When `PUT /providers/custom {api_base, protocol: ollama}` → 400.

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
| 7 | `TestVersion_DateSemver` | Unit | downgrade refused | `2026.8.22` > `2026.8.21` `[A-6]` |
| 8 | `TestGHReleasePuller_Pull_RetargetedAsset` | Unit | release layout | owner/repo/asset = assembly repo; sidecar verify |
| 9 | `TestRefresh_ChecksumMismatch_Retains` | Unit | checksum mismatch | one WARN `reason=checksum` |
| 10 | `TestRefresh_WrongSchemaVersion_Ignored` | Unit | wrong schema | WARN `reason=schema_version` |
| 11 | `TestRefresh_Downgrade_Refused` | Unit | downgrade refused | WARN `reason=regressed` |
| 12 | `TestRefresh_TooLarge_Rejected` | Unit | oversize | 16 MB + 1 → `reason=too_large` `[A-18]` |
| 13 | `TestRefresh_RawFallback_Degraded` | Unit | raw fallback | existing behaviour retained |
| 14 | `TestRefresh_Concurrent_Serialized` | Unit (-race) | concurrent refresh | mutex serialization |
| 15 | `TestStore_StalePersisted_Ignored` | Unit | stale persisted | 1.0.0 envelope ignored, snapshot serves `[A-4]` |
| 16 | `TestEmbeddedSnapshot_Valid_And_Bounded` | Unit | hermetic build; bad snapshot; E7 | snapshot parses, ≤ 8 MB `[A-2]`, tier set exact |
| 17 | `TestEmbeddedSnapshot_PopularTier` | Unit | tiers in data | popular set exact |
| 18 | `TestEmbeddedSnapshot_LocalProvidersPresent` | Unit | local-file providers | 9 ids present |
| 19 | `TestEmbeddedSnapshot_CloudIAMUnsupported` | Unit | cloud-IAM listed | 5 ids `unsupported_reason=cloud-iam` |
| 20 | `TestCreateProviderFromConfig_ProtocolDispatch` (table) | Unit | construct from table outline | transport type + URL per row |
| 21 | `TestCreateProviderFromConfig_ProtocolChoice` | Unit | dual-protocol | `protocol: anthropic` → ClaudeProvider `[A-8]` |
| 22 | `TestCreateProviderFromConfig_Custom` | Unit | custom endpoint; E12 | accepted; disallowed protocol → error |
| 23 | `TestCreateProviderFromConfig_UnknownProvider_NoHint` (table) | Unit | non-canonical outline | `errors.Is(err, ErrUnknownProvider)`; text lacks canonical |
| 24 | `TestFactory_NoVendorCases` | Unit (source scan) | no vendor cases | parse `factory_provider.go` AST; case set exact |
| 25 | `TestSeeds_CanonicalProviderIDs` | Unit | seeds canonical | defaults.go + config.example.json vs snapshot |
| 26 | `TestPickProbeModel_FromCatalog` | Unit | probe model from catalog | first active tool-calling text model `[A-20]` |
| 27 | `TestDisplayName_FromCatalog` | Unit | (US-7.AC1 name field) | `name` from catalog; unknown → id `[A-14]` |
| 28 | `TestConfig_ProviderID_TrimNotFold` | Unit | whitespace/case | `" ZAI "` unknown `[A-19]` |
| 29 | `TestGreenfield_NoAliasMachinery` | Unit (source scan) | greenfield grep | regex over `pkg/providers`, `pkg/config`; allow `retired` token `[A-3]` |
| 30 | `TestContract_ProvidersCatalog_Shape` | Unit | catalog endpoint validates | Go struct → JSON → schema validation (pattern: `pkg/api/generated/contract_test.go`) |
| 31 | `TestContract_ProbeProviderRequest_NoEnum` | Unit | probe old id | generated type is `string`; YAML has no `enum` |
| 32 | `TestBuildProviderPool_UnknownProvider_Skips` | Unit | boot survives | existing WARN+skip retained; pool lacks `nope` |
| 33 | `TestAgentTurn_NeedsProvider_TypedRefusal` | Unit (agent) | bound agent refuses | error kind `needs_provider`, 0 upstream `[A-16]` |
| 34 | `TestRestProvidersCatalog_GET` | Integration (scoped gateway test, `-run`, `-p 1`) | catalog endpoint; 401; ETag; 503 | all four statuses |
| 35 | `TestRestProviders_PUT_Unknown_CloudIAM_Custom` | Integration | cloud-IAM 400; custom 400/200; standard 200 | error vocabulary exact |
| 36 | `TestRestProviders_OfflineModelList` | Integration | offline model list | outbound counter 0 |
| 37 | `TestRestProviders_Entitlement_Intersects_Caches` | Integration | entitlement | annotations + cache `[A-13]` |
| 38 | `TestRestProviders_Ollama_Live` | Integration | local endpoints | stub `/api/tags` |
| 39 | `TestRestProviders_Test_ProbeFromCatalog` | Integration | probe from catalog | 1 POST, 0 GET `/models` |
| 40 | `TestOnboarding_Probe_FreeStringID` | Integration | probe zai / z-ai / custom / bedrock | 4 outcomes |
| 41 | `TestGatewayBoot_UnknownProvider_NonFatal` | Integration | boot survives; no hint | listen ok; A runs; rows/logs lack hint |
| 42 | `TestGatewayBoot_OfflineSnapshot_Then_StartupPull` | Integration | offline boot; startup pull | `source` flips embedded→pulled; boot time within baseline |
| 43 | `TestRefreshLoop_24h_NoRequestPathPulls` | Integration (fake clock) | 24 h ticker | exactly 1 extra pull; 0 during traffic |
| 44 | `TestEmbeddedSnapshot_Corrupt_BootDegrades` | Integration | bad embedded snapshot | 503 + ERROR once (test seam injects bytes) |
| 45 | SPA `providersCatalog.test.ts` | Unit (vitest) | catalog endpoint validates | zod parse of fixture; `ETag` cache rule `[A-1]` |
| 46 | SPA `onboarding probe id` test | Unit (vitest) | probe old id | free-string id submitted; 400 rendered |
| 47 | Contracts gate | E2E (CI) | all contract scenarios | `make verify-contracts`; `npm run typecheck` |
| 48 | Hermetic build gate | E2E (CI) | hermetic build | `make build` with network disabled in the job |

**Order:** 1–31 unit (catalog → version → puller/refresh → embed → factory → probe → greenfield → contract), 32–33 agent unit, 34–44 integration (one scoped gateway test at a time, never the suite), 45–46 vitest, 47–48 CI.

### Test Datasets

**DS-1 document validation** (Traces to US-1 scenarios)
| # | schema_version | version | providers | defect | expect | Traces |
|---|---|---|---|---|---|---|
| 1 | 2.0.0 | 2026.8.22 | fixture (3 providers, 6 models) | none | accept | US-1.AC1 HP |
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
| 5 | PUT /providers/amazon-bedrock | yes | 400 cloud-iam | US-8.AC2 |
| 6 | PUT /providers/togetherai + key | yes | 200 | US-8.AC3 |
| 7 | PUT /providers/custom no base | yes | 400 | US-8.AC4 |
| 8 | PUT /providers/nope | yes | 400 unknown | US-6.AC4 |
| 9 | GET /providers/anthropic (offline) | yes | 200 catalog models, 0 outbound | US-9.AC1 |
| 10 | POST /providers/anthropic/entitlement | yes | 200 annotated; 2nd call cached | US-9.AC2 |
| 11 | POST /providers/anthropic/entitlement (no key) | yes | 422 | US-9.AC2 (EP) |
| 12 | POST /onboarding/probe-provider {zai} | n/a | probe runs | US-10.AC1 |
| 13 | … {z-ai} | n/a | 400 no hint | US-10.AC2 |
| 14 | … {custom, endpoint} / {custom} | n/a | runs / 400 | US-10.AC3 |
| 15 | … {amazon-bedrock} | n/a | 400 cloud-iam | US-10.AC4 |
| 16 | … {id: ""} | n/a | 400 | US-10.AC2 (empty) |
| 17 | … {id: 300-char string} | n/a | 400 unknown | US-10.AC2 (large) |

### Regression Requirements

This feature **modifies existing functionality**.

1. **Behaviours preserved:** media modality resolution and resize budgets for every model in today's seed (the 78 models must resolve under their canonical provider to the same or registry-corrected modalities — corrections are listed in ADR §2 *Why*); the checksum/raw-fallback/degraded-transport semantics of `GHReleasePuller`; the refresh transaction's "retain last known good on any failure"; `buildProviderPool`'s skip-with-WARN; SSRF-safe clients on every live provider call; the onboarding two-phase commit (probe → write → complete) and its rate limit; `/providers/{id}/test` outcome classification (`classify`, `BuildMessage`).
2. **Existing tests that must pass unchanged:** `pkg/providers/capabilities/puller_test.go` (all 12 — moved to `pkg/providers/catalog`, package rename only); `capabilities/catalog_test.go` tests for `validate`, version compare, `refreshLocked` retention, degraded transport (moved; key arguments become pairs); `pkg/gateway/provider_validation_test.go`, `provider_credential_degraded_test.go`, `rest_agent_provider_test.go`, `websocket_provider_refusal_test.go` (ids in fixtures re-keyed to canonical — assertion logic unchanged); `pkg/agent/subturn_target_identity_test.go` (provider pool identity); `pkg/api/generated/contract_test.go`.
3. **Existing tests deleted with their subject:** `TestCatalog_DriftGuard_IdIsKnownProtocol`, `TestCatalog_DriftGuard_IdInProbeEnum`, `TestCatalog_DriftGuard_BaseNonEmptyOrExempt`, `TestCatalog_DriftGuard_NewProtocolUntriagedFails`, `TestWireDerivation_Table` (the wire-suffix rule is gone), `TestContract_ProviderCatalogEntry_Shape` (schema replaced), SPA `providerMigration` tests, `catalog-consistency.test.ts` (rewritten against the `GET`).
4. **New regression tests:** T6 (miss semantics identical to today's optimistic path), T13 (raw fallback), T32 (pool skip), `TestMediaResize_BudgetsUnchangedForSeedModels` (DS-6), `TestValidateKey_OutcomeClassificationUnchanged` (probe model source changed; classification must not).
5. **DS-6 regression — seed parity** (Traces to US-4.AC3): for each of the 78 `(canonical provider, model)` pairs from the old seed: expected `input_modalities` (seed value, or the ADR-listed registry correction) and `resize_budget` (vendor value). 78 rows, generated from the old seed file at test-authoring time and committed as `testdata/seed_parity.json`.

---

## 8. Requirements & Success Criteria

### Functional Requirements

- **FR-001** The system MUST load provider catalogs only at `schema_version` `2.0.0`; any other value is rejected and the previously loaded document retained.
- **FR-002** The system MUST validate on load: non-empty `version`/`updated_at`/`source`; positive default resize limits; ≥1 provider; unique non-empty provider ids; per-provider unique non-empty model ids; `protocol ∈ {openai-compatible, anthropic, google, ollama, cli}`; every model's `input_modalities` includes `text`; `tier ∈ {popular, standard, unsupported}`; `status ∈ {active, retired}`.
- **FR-003** The system MUST key every lookup on the exact `(provider id, model id)` pair and MUST NOT strip or add prefixes.
- **FR-004** A lookup miss MUST yield the optimistic modality default and catalog default resize limits to the media path, and *unknown* window/output to the agent loop.
- **FR-005** `pkg/providers/capabilities` MUST be removed; its machinery lives in `pkg/providers/catalog`; exactly one embedded catalog file exists.
- **FR-006** The embedded snapshot MUST be a committed file in `pkg/providers/catalog/data/providers_catalog.json`, refreshed only by pull request; the build MUST NOT fetch.
- **FR-007** The puller MUST target the assembly repository's release asset `providers_catalog.json` with sidecar `.sha256`, verifying SHA-256 on both the release and raw paths (existing behaviour).
- **FR-008** The gateway MUST start one background pull at startup (30 s timeout) and one every 24 h; no pull on any request or turn path; boot MUST NOT wait for it.
- **FR-009** Checksum mismatch, wrong schema, invalid document, version regression, or size > 16 MB `[A-18]` MUST each log exactly one WARN with a `reason` key and retain the current document.
- **FR-010** The persisted last-known-good MUST be written after a successful apply and read at boot; a persisted file that fails FR-001/FR-002 MUST be ignored with one WARN `[A-4]`.
- **FR-011** Provider ids in config, agent entities, seeds and the probe request MUST be catalog ids or `custom`; no alias table exists in code.
- **FR-012** The factory MUST dispatch on protocol only; base URL and key variable come from the catalog row (explicit `api_base` in config wins) `[A-7]`.
- **FR-013** A config MAY select a secondary protocol a provider offers via `protocol`; absent → primary; a protocol not offered → error `[A-8]`.
- **FR-014** `custom` MUST require `api_base` and accept `protocol ∈ {openai-compatible, anthropic}` only.
- **FR-015** An unknown provider id MUST produce `ErrUnknownProvider` whose message names the id and never a canonical alternative.
- **FR-016** Boot MUST succeed with unknown providers; the provider row reports `unknown-provider` `[A-16]`; agents bound to it are marked needing a provider and refuse turns with error kind `needs_provider` and zero upstream requests.
- **FR-017** `GET /api/v1/providers/catalog` MUST return the in-memory document with `version`, `updated_at`, `source ∈ {embedded, pulled}`; 401 unauthenticated; 304 on matching `If-None-Match`; 503 when no catalog `[A-1]`.
- **FR-018** Tier and unsupported reason MUST be data in the document; the popular set is `{openai, openrouter, anthropic, google, xai, groq, mistral, deepseek}` `[A-9]`.
- **FR-019** Configuring or probing a `tier: unsupported` provider MUST return 400 with the reason.
- **FR-020** The providers API MUST list models for catalog providers from the catalog with no outbound call; for `ollama`/`vllm`/`custom` from the live endpoint.
- **FR-021** An explicit entitlement check MUST call `/models` once per (provider, key), intersect with the catalog, annotate entitlement, surface extra models as limits-unknown, and cache per key `[A-13]`.
- **FR-022** Key validation MUST pick its probe model from the catalog for catalog providers and MUST NOT pre-fetch `/models` for them `[A-20]`.
- **FR-023** `ProbeProviderRequest.id` MUST be a free string validated at runtime against the catalog (or `custom` + `endpoint`).
- **FR-024** Every new or changed wire type (`ProvidersCatalog`, `CatalogProvider`, `CatalogModel`, `ProviderEntitlement`, `ProbeProviderRequest`, `Provider.status` value, `ModelConfig.protocol` on the wire) MUST be defined in `contracts/` first and consumed only via generated types.
- **FR-025** `GET /providers/model-capabilities`, `src/lib/generated/providerCatalog.ts`, `src/lib/providerMigration.ts`, `knownDisplayNames`, `probeModelDefaults`, `knownProtocols`, `GetDefaultAPIBase`, `resolveStrippedPrefix` MUST be deleted `[A-12]`.
- **FR-026** The embedded snapshot MUST be ≤ 8 MB `[A-2]` and MUST contain the nine local-file providers, the popular set, and the five cloud-IAM providers as unsupported.
- **FR-027** The assembly contract (§5) MUST be captured in a conformance fixture committed under `pkg/providers/catalog/testdata/` and used by the Go tests.
- **FR-028** The refresh loop MUST serialize concurrent pulls and be race-free under `-race`.

### Success Criteria

- **SC-001** `Resolve("openrouter","z-ai/glm-5.2").Window()==1048576`, `Resolve("zai","glm-5.2").Window()==1000000`, `Resolve("openrouter","glm-5.2")` is a miss — asserted by T3/T5.
- **SC-002** Checksum-mismatched and non-2.0.0 releases produce exactly 1 WARN each and leave `Version()` unchanged — T9/T10.
- **SC-003** With the network stubbed closed, `GET /providers/anthropic` returns the catalog's model count and the outbound request counter is 0 — T36.
- **SC-004** `TestFactory_NoVendorCases` finds case set exactly `{openai-compatible, anthropic, google, ollama, cli}` — T24.
- **SC-005** Boot with an unknown provider reaches listen in ≤ baseline + 5 %; the bound agent's turn returns `needs_provider` with 0 upstream requests — T41/T33.
- **SC-006** `make verify-contracts`, `npm run typecheck`, `npx vitest run`, `golangci-lint run --build-tags=goolm,stdjson`, `gofmt -l . | wc -l == 0`, `govulncheck` all exit 0 in CI.
- **SC-007** `make build` succeeds in a CI job with egress disabled — T48.
- **SC-008** `ls pkg/providers/capabilities` fails; `grep -rn resolveStrippedPrefix pkg | wc -l == 0`.
- **SC-009** `grep -rnE '_migrated|alias|deprecat|retired' pkg/providers pkg/config` matches only the `retired` status token in `pkg/providers/catalog` — T29.
- **SC-010** A config with `provider: "z-ai"` fails as unknown and the boot log, `GET /providers`, and `GET /agents` bodies do not contain the substring `zai` in any hint position — T41.
- **SC-011** `GET /providers/catalog` p95 ≤ 50 ms on the CI worker for the embedded snapshot; `Resolve` benchmark ≤ 1 µs p99.
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

Every BDD scenario above maps to ≥1 FR via its US; assembly-side scenario US-2.AC2 traces to FR-027 (fixture carries `disputed`) and is executed in the assembly repo.

---

## 9. Ambiguity Self-Audit

**GATE NOT YET PASSED — operator unavailable for this pass.** The spec proceeds under the *Likely assumption* column, labelled `[A-n]` at each use. Each row needs an operator answer, an accepted assumption, or an explicit deferral.

| # | What is ambiguous (ADR silent) | Likely assumption (spec proceeds under this) | Question to resolve |
|---|---|---|---|
| A-1 | Response shape and caching of the providers-catalog `GET` (MIN-004, pass-2 Q8): full document vs. paged/filtered; ETag; how the SPA learns of a mid-session refresh | Full document in one response; strong ETag = SHA-256 of the served bytes; `If-None-Match` → 304; SPA re-validates on Settings open and every 15 min; no WS push | Full document OK at ~7k models? Is an ETag/304 rule enough, or is a WS `catalog_updated` frame wanted? |
| A-2 | Size of the embedded snapshot — full registry (193 providers / ~7,246 models) or a filtered subset | Full document; hard test bound 8 MB; if the full document exceeds it the assembly job trims `status: retired` models first | Embed everything, or only popular+standard active models? |
| A-3 | Name of the model deprecation status value (MIN-003: `deprecated` collides with the greenfield grep) | `status ∈ {active, retired}`; the greenfield grep whitelists that one token in `pkg/providers/catalog` | Accept `retired`? |
| A-4 | Persisted last-known-good filename/location | `$OMNIPUS_HOME/providers_catalog.json` (new name); the old `capabilities_catalog.json` is never read and never deleted | New filename OK? Delete the old file or leave it? |
| A-5 | The assembly repository's GitHub owner/repo (the puller pin) and release cadence vs. the "raw first" suggestion in MIN-004 | `elicify-ai/omnipus-provider-catalog`; puller order unchanged (release API → raw fallback) | Repo name? Keep release-API-first? |
| A-6 | `version` format in 2.0.0 (must be comparable by the existing semver-aware anti-downgrade) | Date-derived semver `YYYY.M.D` (e.g. `2026.8.22`), with `.N` patch for same-day republishes | Accept date-semver? |
| A-7 | The exact protocol enum and how `google` and `cli` dispatch (§3.2 item 3 lists ~5; `google` is reached via the OpenAI-compatible Gemini endpoint today) | `{openai-compatible, anthropic, google, ollama, cli}`; `google` constructs the HTTP provider at the Gemini OpenAI-compatible base (today's behaviour); `cli` covers `claude-cli`/`codex-cli`/`openai-chatgpt` per ADR-068 MAJ-013 | Keep `google` as a distinct protocol value, or fold into `openai-compatible`? |
| A-8 | Where an operator's protocol choice lives for dual-protocol providers (`zai` openai-compatible + anthropic) now that `-anthropic` ids are gone | New `ModelConfig.protocol` (`json:"protocol,omitempty"`) and the same field on `Provider`/`ProviderUpdateRequest` wire types; absent → catalog primary | Accept a `protocol` field on the provider config and wire? |
| A-9 | Mechanism that marks Popular / unsupported (ADR says "pinned" but not where) | A `tier` field in the document, set by `overrides/` in the assembly repo; Go has no hardcoded popular list | Tier as catalog data (assembly-owned) rather than code? |
| A-10 | Whether 2.0.0 carries a top-level default resize limit (today's DTO requires `default_resize_budget`) | Yes: `default_resize_limits`, used for providers with no row in `resize_limits.json` and for lookup misses | Keep a catalog-level default? |
| A-11 | Models whose registry entry lacks `max_output_tokens` or `context_window` | Accepted with `0` = unknown; ADR-066's ladder decides (floor/WARN) | Accept, or should the assembly job refuse to publish such a model? |
| A-12 | Fate of `GET /providers/model-capabilities` (flat, bare-model-id keyed; the SPA vision toast reads it) | Deleted; the SPA derives modalities from the providers-catalog `GET` using the agent's `(provider, model)` | Delete, or keep as a projection? |
| A-13 | Wire shape of "Check with my account" and of `POST /providers/{id}/refresh-models` | `refresh-models` removed; new `POST /api/v1/providers/{id}/entitlement` → `ProviderEntitlement` (`models[] {id, entitled: true/false/unknown, in_catalog}`), cached per key in memory for the process lifetime | Endpoint name/shape? Cache TTL? |
| A-14 | Display names: ADR's provider shape has no `name`; models.dev has one | Provider and model carry `name`; `knownDisplayNames` deleted; unknown → id verbatim | Add `name` to the shape? |
| A-15 | Cadence/owner of the scheduled PR that refreshes the committed snapshot, and the staleness bound at release | Weekly bot PR from the assembly repo; release checklist requires snapshot ≤ 14 days old | Cadence and bound? |
| A-16 | Wire vocabulary for the degraded states (MAJ-010): provider row and agent row | `Provider.status` gains `unknown-provider`; `Agent` gains `degraded_reason: "needs_provider"` (string, optional) | Accept these two values (ADR-068 also extends `Provider.status`)? |
| A-17 | Which Go type holds the loaded document and whether the media/agent consumers get one object or two views | One `catalog.Catalog` with `Resolve(provider, model) Handle` exposing `Window()`, `MaxOutput()`, `Supports()`, `Budget()`, `Status()`; no separate capabilities view | Single handle type OK? |
| A-18 | Maximum accepted size of a pulled document | 16 MB read limit before parse | Accept 16 MB? |
| A-19 | Normalisation of provider ids at the config boundary | Trim whitespace only; no case folding (ids are exact) | Trim-only? |
| A-20 | Probe-model selection rule from the catalog | First `status: active` model with `tool_call: true` and `text` modality, in document order; provider with no such model → probe skipped with the existing "no endpoint to probe" warning | Accept the rule? Should the assembly document carry an explicit `probe_model` per provider instead? |
| A-21 | CLI onboarding (`cmd/omnipus/internal/onboard`) validation source | Validates against the embedded snapshot (no gateway running) | OK for the CLI to use the snapshot only? |
| A-22 | Whether a disagreement under adjudication is visible in the document (pass-2 suggested `disputed: true`; ADR §8a chose last-known-good without a marker) | Model carries optional `disputed: true`; Omnipus surfaces it only through ADR-066 D9's "source" label | Carry the marker? |

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
- **Gate status:** Phase 1 treated as confirmed (ADR = brief); Phase 5.5 **open** — 22 ambiguities proceed under labelled assumptions for operator resolution.
- **User stories:** 11 (P0: US-1,2,3,4,5,6,7,10,11 · P1: US-8,9)
- **BDD scenarios:** 53 (HP 22 · AP 11 · EP 14 · EC 6) including 4 scenario outlines (9 + 4 + 5 + 4 example rows)
- **Test datasets:** 6 (DS-1: 15 · DS-2: 8 · DS-3: 12 · DS-4: 9 · DS-5: 17 · DS-6: 78 generated rows) = 139 data rows
- **Functional requirements:** 28 · **Success criteria:** 13
- **Tests planned:** 48 (31 unit · 2 agent-unit · 11 integration · 2 vitest · 2 CI gates)
- **Risk flagged:** factory collapse + `GetDefaultAPIBase` removal are HIGH-impact (every provider construction path); must land atomically with the table-backed replacement.
- **Follow-ups outside this spec:** picker/entitlement UX, `Provider.status` additions for sign-in, provider deletion (ADR-068); window ladder/floor/learned consumption of `Window()` (ADR-066 D2–D3/D8/D9); the assembly repository's own implementation against §5's contract and the shared fixture.
