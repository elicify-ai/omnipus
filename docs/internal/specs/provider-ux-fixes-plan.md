# Provider UX Fixes — Implementation Plan (supersedes the Providers half of connectors-providers-redesign-spec where they conflict)

**Created**: 2026-07-02 · **Branch**: `feat/provider-ux-fixes` (off `hotfix/v0.1.1`)
**Source**: operator critical review of the shipped ADR-031 Track-1 Providers page. **Where this plan conflicts with ADR-031 / connectors-providers-redesign-spec.md, THIS PLAN WINS** — the spec wrongly imported the Channels (multi-instance, binding-first) model into Providers.

## Corrected domain model
A **provider** is a global, single-instance API config. There is exactly ONE config per catalog entry — no "instances", no workspace/agent binding, no "Add another". The only real variant axes are **plan** (pay-as-you-go vs Coding Plan subscription — only ~4 companies have both) and **region** (intl/china/us — only the Chinese companies). **Wire** (OpenAI- vs Anthropic-compatible) is NOT a separate provider: per Z.ai's own console, one account/key exposes BOTH base URLs; the wire is just which endpoint the client calls — an internal config detail, never a row.

## FIX-1 — Kill "+ Add another…" (HIGH, buggy)
Remove the per-company "Add another…" control entirely (`ProvidersSection.tsx` group header). It's a multi-instance concept, and its current wiring re-opens the Connect sheet for the already-configured entry.

## FIX-2 — Grouping only when it earns its place (MED)
A company group header (logo + name) renders ONLY when ≥2 variants of that company are **configured**. A single configured provider renders as a **flat row** carrying its own `<BrandIcon>` + name. No header wrapping one row.

## FIX-3 — Configured-only, always; roster becomes an on-demand picker (HIGH)
- The list shows **only configured** providers in every state. The default-visible wall of ~30 roster entries is removed.
- Empty state = compact message + one **"Connect a provider"** button (primary CTA).
- **"Connect a provider"** is ALSO always available when providers exist (top-right of the section) — currently there is no way to add a new provider after the first.
- Clicking it opens a **picker Sheet**: search box + the catalog grouped by company, **excluding already-configured entries**; selecting an entry swaps the Sheet content to the existing connect form (key + endpoint etc.). Sheet, not modal.

## FIX-4 — Real terminology (HIGH) — align to opencode/models.dev + vendor language
- **Delete "Standard API" everywhere.** models.dev: the normal provider is just "Zhipu AI"; the subscription one is "Zhipu AI Coding Plan". Z.ai console: "GLM Coding Plan" vs "Resource package / Prepaid balance".
- Plan display labels: `standard-api` → **"Pay-as-you-go API"** (restores the original, vendor-aligned wording; grill R2-04 already flagged "Standard API" as less precise), `coding-plan` → **"Coding Plan"**.
- **Catalog `label`**: single-plan companies get NO plan suffix — `"OpenAI"`, `"Anthropic"`, `"Groq"`, `"Zhipu / GLM (International)"`. Coding-plan entries: `"Zhipu / GLM — Coding Plan (International)"`. Region suffix only when the company has regional variants.
- **Row title inside a group** (only when grouped per FIX-2): `"Pay-as-you-go · International"` / `"Coding Plan · International"` — one axis language, no wire axis.
- Subtitles keep the billing wording: `"Pay-as-you-go, per token · <host>"` / `"Subscription (Coding Plan) · <host>"`.

## FIX-5 — Wire is a config detail, not a row (HIGH)
- **Merge the 7 pure anthropic-wire sibling entries into their primary entry**: `z-ai-anthropic→z-ai`, `zhipu-anthropic→zhipu`, `moonshot-anthropic→moonshot`, `moonshot-cn-anthropic→moonshot-cn`, `minimax-anthropic→minimax`, `minimax-cn-anthropic→minimax-cn`, `deepseek-anthropic→deepseek`. Catalog: 30 → **23 entries**.
- Contract: `ProviderCatalogEntry.yaml` gains optional **`anthropic_id`** (string) — the sibling protocol id exposing the Anthropic-compatible endpoint for the same account/key. Regen Go+TS.
- **Config/Connect Sheet**: when `anthropic_id` exists, show an **"Endpoint format"** toggle — `OpenAI-compatible (default)` / `Anthropic-compatible` — with one help line ("Same account and API key; choose the endpoint your tools expect. Anthropic-compatible suits Claude-Code-style clients."). The choice selects which protocol id gets configured/probed (`entry.id` vs `entry.anthropic_id`). Existing single-wire entries show no toggle.
- **Anthropic (the company)** keeps `id: anthropic`, `wire: anthropic`, no toggle. **Qwen Coding Plan**: `coding-plan` (openai-wire, valid `GetDefaultAPIBase`) is the promoted primary, with `id: coding-plan-anthropic` demoted to `anthropic_id: coding-plan-anthropic`.
- **Row display**: no wire badge by default. When a provider is configured under an `anthropic_id`, show one small muted chip `"Anthropic endpoint"` on its row. Delete `wireBadgeLabel` usage from rows otherwise.
- **Migration/resolve** (`providerMigration.ts`): a stored `-anthropic` id resolves to its merged catalog entry (treat `anthropic_id` like an alias for display), flagged so the row shows the chip. Aliases unchanged.
- **Onboarding**: picker reaches ALL 23 entries (the old 23-of-30 gap dissolves — wire is no longer entry identity). `PLAN_LABELS['standard-api']` → `'Pay-as-you-go API'`. Keep the wire toggle OUT of onboarding (quick-start uses the primary endpoint); Settings config offers the toggle.

## FIX-6 — Drift-guard + tests follow the model (required)
- Partition property: `anthropic_id` values count as catalog-covered `knownProtocols` ids.
- Probe-enum property: both `id` AND `anthropic_id` ∈ `ProbeProviderRequest` enum.
- Alias-endpoint equality unchanged. `LoadCatalog` wire validation: validate `wire(id)` for primary ids; assert each `anthropic_id` derives to `anthropic`.
- Entry-count assertions 30→23. **Delete** the "labels unique via wire descriptor" hack test (uniqueness now natural — assert plain uniqueness). Update `catalog-consistency.test.ts` label rules: label contains "Coding Plan" iff plan=coding-plan; NO label contains "Standard API" or "Anthropic-compatible".
- Update `ProvidersSection.test.tsx` + `-onboarding.test.tsx` + parity test for the new UX (picker Sheet, flat-vs-grouped, no Add-another, toggle).

## Explicit non-goals
No backend routing/probe/knownProtocols changes (only the catalog + contract type). No i18n. No changes to channels. `GET /providers` behavior unchanged.
