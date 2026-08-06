# ADR-054: Model Capability Catalog Identity Restructure

**Status:** Proposed
**Date:** 2026-07-28
**Deciders:** architect (recommendation); operator decision pending
**Evidence level:** 1 — every code claim below is cited to `file:line` or a command run in
this session (GitNexus `impact`, grep, direct reads). Two claims are flagged `[INFERRED]` /
`[UNVERIFIED]` because they rely on external knowledge (OpenRouter's own vendor-slug
spelling) this repo cannot confirm — see RD2.

## Context

`pkg/providers/capabilities` (catalog.go, seed `pkg/providers/capabilities/data/providers_capabilities_seed.json`)
is the FR-024/025/026/027 source of truth for "which models accept which input
modalities" (vision, PDF, audio, resize budget). `Catalog.Resolve` is an **exact map
lookup keyed by a single flat string**, falling back to the FR-026 optimistic default
(`[text, image]`) on any miss:

```go
func (c *Catalog) Resolve(modelID string) *resolvedModel {
    ...
    if m, ok := c.models[modelID]; ok {
        return c.resolve(m)
    }
    return c.optimistic(modelID)
}
```
`catalog.go:635-642`. The map key comes straight from the seed's `id` field
(`catalog.go:617`: `c.models[m.ID] = model{...}`).

**The seed already carries a second field that the map ignores.** Every entry is
`{id, provider, input_modalities, ...}` — e.g.
`{"id": "glm-5.2", "provider": "z-ai", "input_modalities": ["text"]}`
(`data/providers_capabilities_seed.json:67`). `provider` here is not a routing
protocol, it is the **model's vendor/company** — the seed's 9 distinct `provider`
values are `anthropic, deepseek, google, minimax, mistral, moonshot, openai, xai, z-ai`
(verified: `grep -o '"provider": "[^"]*"' … | sort -u`), i.e. companies, not routes.
`Resolve` never reads this field for lookup — only `Model.Provider`/`resolvedModel.Provider()`
expose it as metadata.

**Why one company has several slugs.** The same model is addressed differently
depending on route:

- **Via OpenRouter** (an aggregator, not a company): the slug is `<vendor>/<model>`,
  e.g. `z-ai/glm-5.2` — confirmed live convention: `pkg/config/defaults.go:127,132`
  seeds `openrouter/auto` and `openrouter/openai/gpt-5.4`; `pkg/agent/instance.go:648`
  comments the exact pattern `mc.Model = "z-ai/glm-5.2"`.
- **Via Z.ai direct**: the slug is bare — `glm-5.2` — because the provider itself IS
  the company.
- **Bedrock** (not yet in the seed) uses a third scheme entirely — AWS `ModelId`
  strings such as region-qualified inference-profile IDs
  (`bedrock/provider_bedrock.go:160`, `aws.String(model)`) — out of scope for this
  ADR's seed coverage but relevant to RD1's identity shape (a third slug dialect
  must not force a redesign later).

**The exact live bug.** `runTurn` passes `ts.agent.Model` — the O3 two-field model's
bare `Primary`/`Model` string, e.g. `"z-ai/glm-5.2"` for an OpenRouter-routed
agent — directly into `resolveMediaRefsWithOffload(…, ts.agent.Model, …)`
(`pkg/agent/loop.go:6612-6616`), which calls `modelSupportsImage`/`modelSupportsPDF`
(`pkg/agent/media_present.go:91-107`), both of which call `catalog.Resolve(model)`
verbatim. The seed's entry is keyed `"glm-5.2"` (bare). `"z-ai/glm-5.2"` (composite)
**misses** the map and falls through to `optimistic()` — which happens to *also*
report `[text, image]`. Two independent defects are currently canceling out:

1. The **keying is wrong** (composite slug vs. bare key — a structural bug).
2. The **seed entry is wrong** (`glm-5.2` says `["text"]`; the operator uses it for
   vision regularly — a data bug, being fixed separately per the task brief).

Fixing only #2 (as another agent is doing) leaves #1 live: the corrected seed data
is *still unreachable* for any agent routed through OpenRouter, because the
composite slug still misses the flat map. This ADR fixes #1 — the identity
structure — so #2's corrected data actually reaches production traffic.

**Same defect exists client-side.** `modelLacksImageCapability` (`src/lib/api.ts:1783-1788`)
does `entries.find((c) => c.id === modelId)` against `agentModel` sourced from
`Agent.model` on the wire (`attachment-adapter.ts:214`, `browserAnnotate.ts:91`) — the
identical bare-vs-composite mismatch, currently masked the same way.

**BRD/spec grounding:** FR-024 (global compiled seed, keyed by `input_modalities`),
FR-025 (7-day pull, GitHub Release + raw fallback, non-fatal), FR-026 (optimistic
default for unknown models), FR-027 (global-scope-only overrides) —
`docs/internal/specs/workspace-media-library-and-presentation-layer-spec.md:1090-1093`.
None of the four FRs specifies the key *shape* — this ADR fills that gap.

**Blast radius (GitNexus `impact`, `Catalog.Resolve`, upstream, depth 3): risk LOW,
7 impacted symbols, 4 direct callers, 1 affected process (`runTurn`), 2 affected
modules (Capabilities, Agent).** Manually confirmed direct callers: `modelSupportsImage`,
`modelSupportsPDF` (`media_present.go:91,102`), `resizeBudgetForModel`
(`loop_media.go:1178,1180`), and `HasModal` (`catalog.go:667-669`, used by no
current caller outside the package but exported). The REST handler
(`pkg/gateway/rest.go:5557-5622`) consumes `Catalog.Models()`, not `Resolve`
directly, but its output feeds the SPA's parallel, equally-broken client-side path.
**Total surface: 2 Go call sites' worth of signature change, 1 wire schema, 1 TS
consumer function — genuinely small.**

## Decision

**Restructure the catalog's identity from a single flat string to the tuple already
sitting unused in the seed: (company, bare-model-id).** Concretely:

1. Key the in-memory map by a normalized `(company, id)` pair instead of `id` alone,
   using the seed's *existing* `provider` field as `company` — **no seed schema
   change**.
2. Give `Resolve` a second parameter — the caller's configured route/provider
   string — and have it: split a composite slug on its first `/` when present,
   or use the passed route when the slug is bare; normalize whichever
   company-signal it has through a small, explicit alias table; look up the
   tuple; fall through to the unchanged FR-026 optimistic default on any miss.
3. Add a `provider` (company) field to the wire `ModelCapabilities` type,
   populated straight from the seed's existing value, and give the SPA's
   `modelLacksImageCapability` the same two-part resolution using `Agent.model`
   + `Agent.provider` (both already on the wire).
4. Do **not** touch the Puller/Store/7-day-refresh pipeline (FR-025) — it is
   sound engineering solving a different problem (freshness, not identity).

### RD1 — Identity shape: (company, bare-model-id) tuple, sourced from the seed's existing fields. **Confidence: High.**

The canonical identity is **not** a single glued string (`"z-ai/glm-5.2"`) but a
two-part tuple, because company names and model names are independent axes that
may each contain characters (theoretically even `/`) that make single-string
splitting ambiguous. The seed already expresses this tuple as `(provider, id)` —
`provider` IS `company` under a different name. Concretely:

- **No seed JSON restructuring.** `{"id": "glm-5.2", "provider": "z-ai", ...}`
  needs zero edits. The change is entirely in `Catalog.applySeed` (map-key
  construction) and `Resolve` (lookup construction) — `catalog.go:612-629,
  635-642`.
- **Relax `seedFile.validate`'s uniqueness check** (`catalog.go:408-416`,
  `seen[m.ID]`) from "ID globally unique across all providers" to "ID unique
  within its provider" — purely permissive (nothing today violates the
  stricter rule, since IDs are already globally unique; this only future-proofs
  against two companies independently naming a model the same bare string,
  e.g. a hypothetical `"flash"`).
- **Bedrock's third dialect is why the tuple beats a single normalized string.**
  A future Bedrock seed entry's `id` would be an AWS `ModelId`
  (`bedrock/provider_bedrock.go:160`) — visually nothing like `company/model` —
  but `provider: "bedrock"` (or `"anthropic"`, since Bedrock hosts multiple
  vendors' models) still slots into the same `(company, id)` shape without a
  third code path. A design that instead tried to normalize everything into
  one canonical string would need Bedrock-specific string surgery; the tuple
  does not.

**Why not alternatives:**
- *Per-slug aliasing (one canonical entry + an explicit list of every known
  slug variant, e.g. `aliases: ["z-ai/glm-5.2", "openrouter/z-ai/glm-5.2"]`
  per model)* — rejected. This is `pkg/providers/catalog`'s own pattern
  (`catalog.go:38-42`, "Alias convention" — used there for provider *routing*
  identity, 23 entries) and works there because the entry count is small and
  centrally curated by the same people who write `entry()` calls. For 70+
  models across 9 companies it means hand-enumerating every aggregator's
  spelling of every model, forever, growing without bound as new aggregators
  (Bedrock, Azure, Vertex, self-hosted) are added — exactly the maintenance
  burden RD4 below rejects. The tuple approach needs only ONE alias table
  entry per **route/company**, not per **model**.
- *Per-provider normalization rules only (strip known prefixes down to a bare
  id, matching `normalizeModel`'s pattern)* — rejected as the *sole*
  mechanism, though its "strip a redundant self-prefix" sub-rule is reused
  defensively in RD2. Normalizing everything down to a bare id throws away
  the company signal that disambiguates cross-company collisions and loses
  exactly the information needed to keep `zhipu`/`z-ai`-direct and
  `openrouter`-routed traffic pointed at the *same* catalog entry without
  requiring the model ID itself to be globally unique forever.

### RD2 — Resolution semantics: split-or-route, normalize, tuple lookup, optimistic on any miss. **Confidence: High** (mechanism); **Medium** (exact alias-table contents — see flagged gap below.)

`Resolve(modelID, routeProvider string) *resolvedModel`:

1. **Defensive de-prefix** (reused verbatim from `normalizeModel`'s proven rule,
   `openai_compat/provider.go:424-451`): if `modelID` starts with
   `"openrouter/"` **and** the remainder still contains a `/`, strip the
   `"openrouter/"` prefix before proceeding. This guards the rare passthrough-fallback
   edge case (`instance.go:641-650`, `findPassthroughForModel`) that can produce a
   triple-segment slug; it is not the primary fix. `"openrouter/auto"` needs **no**
   special case here (unlike in `normalizeModel`) — see the emergent-correctness
   note below.
2. **If the (possibly de-prefixed) slug contains `/`:** split on the **first** `/`
   → `(vendorSlug, bareModel)`. Normalize `vendorSlug` through a small
   capabilities-local alias table (RD2a). Look up `(company, bareModel)`.
   Found → return it. **Not found → go straight to step 4 (optimistic). Do not
   retry with the raw, un-normalized vendor slug, and do not fall back to a
   bare-`bareModel`-only index.** A bare-id-only fallback is exactly the silent
   partial-match hazard the operator is worried about: two companies could
   ship same-named models, and guessing which one's capabilities apply is a
   worse failure mode than the honest, bounded optimistic default.
3. **If the slug has no `/`:** it is a bare model id from a direct (non-aggregator)
   route. Normalize the caller-supplied `routeProvider` through the *same*
   alias table → company. Look up `(company, modelID)`.
4. **Any miss → the unchanged FR-026 optimistic default** (`catalog.go:646-654`).
   No behavior change here — see RD3.

**RD2a — the alias table (capabilities-package-local, not imported from
`pkg/providers/catalog`).** Deliberately a **new, small table**, not a reuse of
`pkg/providers/catalog`'s `Company` field or `catalog.go`'s `aliases` — that
package's own doc comment states the two packages are "orthogonal concerns"
(`pkg/providers/capabilities/catalog.go:3-8`), and `catalog.Company` is a
**display string** (`"Zhipu / GLM"`), not a slug — joining on it would couple a
lookup key to prose. The new table only needs to fold known company-route
spellings onto the seed's 9 canonical values, e.g. (illustrative, to be
completed by whoever implements this against the real seed + `knownProviderProtocols`,
`pkg/config/config.go:3487-3499`): `zhipu → z-ai`, `z-ai-coding → z-ai`,
`zhipu-coding → z-ai`, `minimax-cn → minimax`, `moonshot-cn → moonshot`.

**Flagged gap `[UNVERIFIED]`:** OpenRouter's own vendor-slug spelling for xAI
models may be `x-ai` (hyphenated) while the seed's canonical company is `xai`
(no hyphen, `data/providers_capabilities_seed.json:40-43`). I could not verify
this in-repo — no `x-ai` or `grok` OpenRouter-slug reference exists anywhere in
this codebase today (checked: `grep -rn "x-ai/" pkg src` → no hits) — this is
recalled from general OpenRouter API knowledge, not confirmed against this
project's data. **Before implementing, verify OpenRouter's actual vendor
namespace for every seeded company against a live `/models` response or current
docs; do not assume the seed's `provider` spelling matches the aggregator's
spelling.** This is exactly the class of mismatch RD2's alias table exists to
absorb — but the table is only correct if its contents are checked, not
assumed.

**Emergent correctness — `"openrouter/auto"` needs no special case.** Under
step 2, `"openrouter/auto"` splits to vendor=`"openrouter"`, model=`"auto"`.
`"openrouter"` is an aggregator, not a company — it has (and should have) no
entry in the alias table, so the lookup misses cleanly and falls to the
optimistic default. That default (`text, image`) is a reasonable stand-in for a
dynamic meta-router nobody can characterize statically. No code needs to name
`"auto"` specially — a property worth stating because `normalizeModel` *does*
need that special case (for a different reason: it must preserve the literal
string OpenRouter's API expects), and it would be a mistake to assume the
capability resolver needs the identical carve-out.

**Signature-change cost:** `Resolve`'s 4 direct callers all live in
`pkg/agent` (confirmed above); the second parameter is already available at
every call site without new plumbing — `ts.agent.Candidates[0].Provider`
(`pkg/agent/instance.go:75`, `providers.FallbackCandidate.Provider`) is the
live route string, kept in sync with `ts.agent.Model` under the same mutex per
the documented Model/Provider/Candidates/ProviderPool atomicity invariant
(CLAUDE.md, "Delegation identity" section). This is an in-repo, unexported-surface
signature change (not a released public API) — change it directly rather than
adding a parallel method.

### RD3 — Unknown-model default: optimistic survives, unchanged. **Confidence: High.**

FR-026's asymmetry argument holds *more* strongly after this restructure, not
less: the tuple-based resolver is **strictly more permissive** than today's flat
lookup — it can only turn former misses (composite slugs against a bare-keyed
map) into hits; it introduces no new way to miss a slug that used to hit. There
is therefore no scenario where this restructure makes the optimistic default
fire *more often* than today. The operator's stated asymmetry — "a wrong guess
costs one outcome-based retry, never a dead turn" vs. "pessimistic-wrong silently
degrades a working capability" — is unchanged data and still favors optimism.
**Reject** any pessimistic-default or partial-match-with-a-guess alternative for
the reason given in RD2 step 2: guessing across an ambiguous partial match is
worse than an honest, bounded-cost optimistic default.

### RD4 — Versioning/freshness: keep the Puller/Store/7-day pipeline; add a coverage test that would have caught this exact bug. **Confidence: High** (keep pipeline); **Medium** (new test, `[INFERRED]` recommendation not requested by the task but directly responsive to "how do we keep it current").

The existing `Puller`/`GHReleasePuller`/`Store`/7-day-refresh machinery
(`puller.go`, `catalog.go:755-854`) is well-engineered for a different problem —
*data* freshness — and is untouched by this ADR. The task brief is explicit that
data correctness (the wrong `glm-5.2` entry) is being fixed separately; this
ADR's freshness recommendation is purely structural: **add a regression test
that resolves every model actually reachable by a live configuration (the
seeded default agents' models, plus any documented reference/E2E model) through
the tuple resolver and asserts the result is non-optimistic** (i.e., that it hit
a real catalog entry, not the FR-026 fallback). This is the test that would have
caught the production bug at CI time — today, `TestCatalog_Resolve_KnownModel`
(`catalog_test.go:635`) and `TestCatalog_Resolve_UnknownModel_Optimistic`
(`catalog_test.go:658`) exist, but nothing asserts that a *composite,
route-realistic* slug (`"z-ai/glm-5.2"` with route `"openrouter"`) resolves
non-optimistically — that gap is precisely what let the keying bug ship
unnoticed. I did not enumerate the exact default-agent models to bake into this
test (`pkg/config/defaults.go`'s seeded model_list entries are `qwen-plus`,
`moonshot-v1-8k`, `llama-3.3-70b`, `openrouter/auto`, `openrouter/openai/gpt-5.4`,
`nvidia/...` — verified lines 100-135 — none of which is `glm-5.2`; that model
appears to be an operator-specific live-deployment choice, not a shipped
default, per session memory, and I have **not** independently verified it
against a currently-running config). Whoever implements RD1-RD3 should pick the
concrete model set for this test from the actual default seed plus whatever the
project's documented E2E/reference model is at implementation time.

### RD5 — Migration / wire contract: additive `provider` field on `ModelCapabilities`; `id` semantics unchanged. **Confidence: Medium-High.**

Constraint #8 requires this to go through `contracts/` first. Recommended
change to `contracts/components/schemas/ModelCapabilities.yaml`:

```yaml
required:
  - id
  - provider
  - modalities
properties:
  id:
    type: string
    description: >
      Bare model identifier as recorded in the capability catalog seed
      (no provider/vendor prefix) — e.g. "glm-5.2", "gemini-2.5-flash".
      Combined with `provider` this is the catalog's canonical identity;
      `id` alone is NOT guaranteed unique across the whole array (two
      different providers/companies may in principle use the same bare id).
  provider:
    type: string
    description: >
      Canonical company/vendor identifier for this model, as recorded in
      the capability catalog seed (e.g. "z-ai", "openai", "google") — NOT
      a routing/aggregator name. Distinct from Agent.provider, which names
      the configured route (may be "openrouter", an aggregator with no
      catalog entry of its own).
  modalities: # unchanged
```

This is **additive at the JSON level** (new required field; `id`'s existing
values and meaning do not change) but is a **behavior-contract break** for the
one existing consumer: `modelLacksImageCapability` must change from a
single-field `id` match to a two-field `(provider, id)` match, mirroring RD2's
Go algorithm, sourced from `Agent.model` + `Agent.provider` — both already on
the wire (`contracts/components/schemas/Agent.yaml:65-85`, confirmed both
fields exist today). Because `required` grows, this is a schema version bump
under `make gen-contracts`/`make verify-contracts`, committed atomically with
the TS consumer change — the standard 5-step process in CLAUDE.md's "Contract
regeneration" section, not a novel one.

Precedent for the "keep two runtimes in sync on shared *logic*, not just wire
*types*" requirement: `IsKnownModel` (`pkg/agent/model_resolution.go:401-429`)
already documents "the TS twin lives in `src/lib/agents/model-validation.ts`
and MUST stay in sync." RD2's alias table + tuple-resolution algorithm should
follow the identical pattern — a shared, explicitly cross-referenced comment
block in both languages, not a generated artifact (Constraint #8's codegen
requirement governs wire *shape*, not resolution *behavior*).

**Rejected alternative — composite string id (`"z-ai/glm-5.2"`) on the wire**,
i.e. gluing the tuple into one field instead of adding `provider`. Rejected
because: (a) it re-introduces exactly the single-string ambiguity RD1 argues
against, now at the wire boundary; (b) it changes `id`'s existing *values*
for every current entry (not additive — every existing consumer's exact-match
against a bare id breaks, not just the buggy one); (c) it gives the frontend
nothing it doesn't already have — `Agent.provider` is on the wire today, so
splitting `Agent.model` client-side to reconstruct a company is strictly more
work than reading a `provider` field already provided.

### RD6 — Blast radius: confirmed LOW via GitNexus, contained to 2 Go files + 1 schema + 1 TS function. **Confidence: High** (this is a direct tool measurement, not an estimate).

GitNexus `impact(target: "Resolve", direction: "upstream", file_path:
"pkg/providers/capabilities/catalog.go", kind: "Method", maxDepth: 3)` →
`risk: "LOW"`, `impactedCount: 7`, `direct: 4`, one affected process (`runTurn`,
`pkg/agent/loop.go`), two affected modules (Capabilities, Agent). Manually
enumerated direct callers (all confirmed above): `modelSupportsImage`,
`modelSupportsPDF`, `resizeBudgetForModel`, `HasModal`. The REST handler
(`rest.go:5557-5622`) is a `Models()` consumer, not a `Resolve()` caller, and
needs only a one-field addition to its output construction
(`gen.ModelCapabilities{Id: snap.ID, Provider: snap.Handle.Provider(), ...}`).
No channel, session, memory, sandbox, or cross-variant (OSS/Desktop/SaaS)
surface touches this package — confirmed via the same GitNexus query's
`affected_modules` (only Capabilities + Agent) and by this package's own doc
comment naming its only consumer as "the Layer-1 presentation orchestrator
(`pkg/agent/media_present.go`)" (`catalog.go:6-8`).

## Consequences

### Positive
- Closes the exact production defect: an OpenRouter-routed `z-ai/glm-5.2` agent
  will resolve to its real (once-corrected) seed entry instead of the
  optimistic default, with no dependence on prefix-stripping happening to
  produce the right bare string.
- Removes the "two bugs canceling out" trap entirely — the seed-data fix (being
  done separately) and the keying fix (this ADR) are now independent and each
  individually safe to ship; today they are coupled by an accident.
- Strictly more permissive than the current resolver (RD3) — cannot regress a
  slug that currently resolves correctly.
- Near-zero seed/data migration: the seed's `(id, provider)` shape already IS
  the target tuple.
- Extends cleanly to Bedrock's incompatible third slug dialect (RD1) without a
  future redesign.

### Negative
- `Resolve`'s signature changes (adds a `routeProvider` parameter) — 4 call
  sites to update, all internal (RD6).
- The wire `ModelCapabilities` schema gains a required field — a coordinated
  backend+frontend change under Constraint #8, not a pure additive-and-forget
  change for the one existing consumer (`modelLacksImageCapability`).
- A second alias table now exists in the codebase (capabilities-local), adding
  to an already-crowded set of provider-name-normalization tables
  (`knownProviderProtocols` in `config.go`, `NormalizeProvider` in
  `model_ref.go`, `pkg/providers/catalog`'s own `aliases`). This is a real,
  acknowledged cost — see Alternatives Considered for why a shared table was
  rejected anyway.
- The alias table's correctness for aggregator vendor-slug spellings (e.g. the
  flagged `xai`/`x-ai` question) is unverified and must be checked against live
  aggregator data before shipping, not assumed from the seed's own spelling.

### Neutral
- The Puller/Store/7-day-refresh pipeline (FR-025) is untouched — this ADR is
  purely about the identity/keying layer sitting in front of it.
- `seedFile.validate`'s uniqueness relaxation (global → per-provider) has zero
  observable effect on the current seed (already globally unique) — it only
  changes what's *permitted* going forward.

## Alternatives Considered

### Per-slug aliasing (one canonical entry + explicit slug-variant list per model)
- Pros: matches `pkg/providers/catalog`'s existing pattern; no lookup-time
  normalization logic.
- Cons: O(models × aggregators) maintenance — every new aggregator (Bedrock,
  Azure, Vertex, a future self-hosted proxy) requires touching every existing
  model's alias list, forever.
- Why rejected: the operator's own framing ("aliasing... versus normalisation
  rules per provider... versus both") anticipated this trade-off; the tuple
  approach gets the aliasing benefit (multiple slug shapes → one entry) at
  O(companies × aggregators) cost instead, by aliasing the company axis only.

### Per-provider normalization only (strip to bare id, no company dimension)
- Pros: simplest possible change; mirrors `normalizeModel` exactly.
- Cons: discards the company signal, which is the only thing that lets a
  direct-Z.ai bare slug and an OpenRouter composite slug for the same
  underlying model share one catalog entry without requiring every model name
  to be globally unique forever; reintroduces the cross-company collision risk
  RD1 explicitly guards against.
- Why rejected: solves today's specific bug but not the general problem the
  operator described (up to three conceptual parts, provider/company/model).

### Composite string id on the wire (`"z-ai/glm-5.2"`) instead of an additive `provider` field
- Pros: single field, no schema growth.
- Cons: breaks every existing entry's `id` value (not additive); re-glues the
  tuple RD1 deliberately keeps apart; makes the frontend redo work
  (`Agent.provider`) it already has for free.
- Why rejected: strictly worse on both migration cost and design cleanliness
  than the additive-field approach (RD5).

### Pessimistic default for unknown models
- Not seriously considered — directly contradicts FR-026 and the operator's
  own stated asymmetry (a wrong-optimistic guess costs one retry; a
  wrong-pessimistic guess silently and permanently degrades a working
  capability, which is the exact failure mode the operator hit and does not
  want repeated).

## Affected Components

- **Backend:** `pkg/providers/capabilities/catalog.go` (map keying, `Resolve`
  signature, `seedFile.validate` uniqueness scope), a new small alias-table
  file in the same package, `pkg/agent/media_present.go` (2 call sites),
  `pkg/agent/loop_media.go` (1 call site), `pkg/gateway/rest.go:5557-5622`
  (add `Provider` to the constructed wire struct).
- **Frontend:** `src/lib/api.ts:1783-1788` (`modelLacksImageCapability` —
  two-field match), a new TS mirror of the alias table (co-located per the
  `IsKnownModel`/`model-validation.ts` precedent), `pkg/api/generated` +
  `src/lib/api/generated` regeneration.
- **Contracts:** `contracts/components/schemas/ModelCapabilities.yaml` (add
  required `provider` field).
- **Variants:** all three (Open Source/Desktop/SaaS) — this is pure backend
  logic plus one SPA function; no variant-specific behavior. go:embed
  unaffected (seed file unchanged).

## Integration Contract

```yaml
# contracts/components/schemas/ModelCapabilities.yaml (diff)
required:
  - id
  - provider   # NEW
  - modalities
properties:
  id: {type: string}        # unchanged meaning: bare model id
  provider: {type: string}  # NEW: canonical company id, e.g. "z-ai", "openai"
  modalities: {...}          # unchanged
```

```go
// pkg/providers/capabilities/catalog.go (signature change)
func (c *Catalog) Resolve(modelID, routeProvider string) *resolvedModel
```

Both call-site families must pass the route/company signal that is already
available today (`Candidates[0].Provider` backend-side, `Agent.provider`
wire-side) — no new plumbing to source it, only new plumbing to thread it
through.
