# Feature Spec — Connectors & Providers UI/UX Redesign (ADR-031)

**Created**: 2026-07-02
**Status**: Draft
**Input**: [ADR-031](../architecture/ADR-031-connectors-providers-redesign.md) (Accepted, revised R1+R2+R3) + its three grill reports (`-review*.md`). This spec covers the **complete** ADR-031 scope: Providers redesign + Channels redesign + onboarding consistency + login cleanup, structured as two separately-shippable tracks.

---

## 1. Overview

### Problem
Two settings surfaces — **Connectors (Channels)** and **Providers** — present *the whole catalog of what's possible* rather than *what the operator configured*, and hide the one attribute that matters (a channel's workspace→agent binding; a provider's plan/endpoint). The provider **plan/region/variant** UX (needed to hit the right endpoint for the multi-endpoint Chinese providers) exists **only in onboarding**, mislabels a wire protocol ("Anthropic API") as a billing plan, has no logos, and is inconsistent with Settings. A real operator concluded a channel could bind to only one workspace.

### Solution (one line)
A shared information architecture — **configured-only list → empty-state roster → `Sheet` slide-out config** — for both surfaces, driven by one build-time-embedded **provider catalog** (single source of truth, `label`/`subtitle` baked in), rendered with one shared **`<BrandIcon>`**, made consistent across onboarding and Settings, with the redundant login re-onboard button removed.

### Actors
- **Operator / admin** — configures providers and channels; the primary user of both surfaces and onboarding.
- **Onboarding flow** — the first-run wizard (`ModelKeyStep`) that must share the catalog + terminology + logos with Settings.
- **SPA settings screens** — `ProvidersSection`, `ConnectorsScreen`.
- **Backend provider registry** — `knownProtocols` / `GetDefaultAPIBase`; the functional truth the catalog is seeded from.

### Locked decisions (from ADR-031; do NOT re-litigate)
- **G-1=B** — real recolored brand marks everywhere (+ mandatory disclaimer, swappable assets, SVG sanitization).
- **G-2=B** — one backend-owned catalog SoT. **Delivery = build-time `go:embed` artifact, NOT a live endpoint** (plan-spec decision 2026-07-02). **`label`/`subtitle` carried as catalog fields** ("on the wire"), NOT derived frontend-side (plan-spec decision 2026-07-02) — this keeps G-3=C's "duplicated pickers can't drift the words" guarantee unconditional.
- **G-3=C** — onboarding and Settings share the catalog + `<BrandIcon>` only; each builds its own picker layout (safe because all words are catalog data).
- **Rollback** = git-revert-and-redeploy (no SPA feature-flag infra exists; plan-spec decision 2026-07-02).

### In scope
1. **Shared foundation** (both tracks): the embedded provider catalog SoT + 3-property drift-guard; the `<BrandIcon>` component + vendored/sanitized SVGs + disclaimer.
2. **Track 1 — Providers** (no ADR-029 dependency; ships first): configured-only list + empty roster; `Sheet` config; company-grouped binding-first variant rows; corrected terminology; onboarding↔Settings consistency; migration of existing configs.
3. **Track 2 — Channels** (hard-gated on ADR-029 reaching `Accepted`): type-grouped binding-first rows; empty roster; `Sheet` create + config (no modal `Dialog`).
4. **Login cleanup**: remove the re-onboard button + `Rocket` import; retain the post-login redirect.

### Out of scope
- Provider **routing/probe** behavior (`probe-provider`, `ValidateKey`, `FetchModels`, SSRF) — unchanged.
- Channel **binding semantics / routing** — owned by ADR-029; this spec renders them, does not change them.
- Adding **new** providers/protocols to `knownProtocols` or the `ProbeProviderRequest` enum.
- Any i18n/localization capability (the SPA has none today).
- A live `GET /providers/catalog` endpoint (explicitly rejected in favor of the embed).

### Constraints
- Single Go binary, embedded SPA, strict CSP, no new runtime deps, no telemetry (CLAUDE.md).
- **Contract-first (Constraint #8):** the `ProviderCatalogEntry` **type** is defined in `contracts/components/schemas/` and generated to Go + TS; hand-written cross-boundary types are forbidden. (The catalog *data* is embedded, not served — see §7 Integration Boundaries for how this honors #8 without an endpoint.)
- Build tags `goolm,stdjson`; CI on the ci-omnipus worker is authoritative for Go.
- Reuse existing components: `Sheet`, `Tabs`, `Badge`, `Button`, `SmartSelect`, `Switch`, `Input`, `DOMPurify` (already a dep).

---

## 2. Available Reference Patterns

| Reference | Pattern | Relevance |
|---|---|---|
| `pkg/gateway/embed.go` (`//go:embed all:spa`) + `pkg/gateway/inboundschemas/schemas.go` (`//go:embed *.yaml`) | build-time asset embed | The catalog JSON is embedded the same way (`//go:embed data/providers_catalog.json` in `pkg/providers/catalog`). |
| `pkg/api/generated/contract_test.go` (`mustPassComponent`, the `ToolApprovalRequiredFrame_NilArgsRejected` regression guard) | contract drift-guard test | Template for the 3-property catalog drift-guard. |
| `src/components/skills/ChannelConfigPanel.tsx` (`Sheet` + `doSaveRoutingDebounced` auto-persist) | slide-over config with auto-persist | The canonical `Sheet` config pattern both surfaces adopt. |
| `src/routes/proto.tsx` (`BrandIcon`, `LettermarkChip`, `BRAND_SVG_PATHS`) + `src/assets/brand-logos/*.svg` (20 vendored) | prototype logo component | Promote to a production `<BrandIcon>`; the SVGs are already vendored (uncommitted). |
| `src/routes/onboarding.tsx` (`ProviderVariant`, `AVAILABLE_PROVIDERS`, `resolveVariantId`, `PLAN_LABELS`) | existing variant model | The catalog seed; `AVAILABLE_PROVIDERS` is ported into the backend catalog then deleted. |

---

## 3. Existing Codebase Context

### Symbols Involved

| Symbol | File:line | Role | Notes |
|---|---|---|---|
| `knownProtocols` | `pkg/providers/factory_provider.go:424` | **read** | `map[string]bool`, **61** entries; ~30 user-facing, ~31 alias/CLI/infra. |
| `IsKnownProtocol(p)` | `factory_provider.go:492` | **call** | Membership check for drift-guard property (a). |
| `GetDefaultAPIBase(p)` | `factory_provider.go:497` | **read/reference** | Returns base URL; `""` only for `azure`/`azure-openai`/`bedrock`. `litellm/ollama/vllm` return non-empty localhost (508/550/578) — the R2-01 fact. |
| `AVAILABLE_PROVIDERS` | `src/routes/onboarding.tsx:74` | **modify (delete)** | 30 variants / 15 companies; ported to backend catalog seed, then deleted. |
| `ProviderVariant.endpointHint` | `onboarding.tsx:67` | **extend** | Curated per-variant host — becomes catalog `endpointHint` (R2-01/R2-03). |
| `PLAN_LABELS` | `onboarding.tsx:331` | **modify** | `{api:'Pay-as-you-go API', coding:'Coding Plan', anthropic:'Anthropic API'}` → relabel per FR-006. |
| `ProbeProviderRequest` (`.id` enum) | `contracts/components/schemas/ProbeProviderRequest.yaml:14-74` | **read (invariant)** | 74 ids; the catalog must stay ⊆ this enum (drift-guard property c). |
| `-onboarding.test.tsx` invariant | `src/routes/-onboarding.test.tsx:1029-1074` | **modify (re-home)** | `AVAILABLE_PROVIDERS ⊆ ProbeProviderRequest` — migrates to the backend drift-guard. |
| `ProvidersSection` | `src/components/settings/ProvidersSection.tsx:33` | **modify** | Configured-only list; inline expand (`expandedProvider`) → `Sheet`; add empty-state, grouping, logos. |
| `ModelKeyStep` | `src/routes/onboarding.tsx:1249` | **modify** | Consume shared catalog + `<BrandIcon>`; corrected labels. |
| `ConnectorsScreen` | `src/components/screens/ConnectorsScreen.tsx:352` | **modify** | Flat list (503-589) → type-grouped binding-first rows; empty-state at 494-500 reused. |
| `AddInstanceDialog` | `ConnectorsScreen.tsx:76-250` | **modify (replace)** | Modal `Dialog` → `Sheet` create flow (FR-002). |
| `ChannelConfigPanel` | `src/components/skills/ChannelConfigPanel.tsx:292` | **reuse** | Already `Sheet`-based; the pattern Track 1 adopts. |
| `Sheet*` | `src/components/ui/sheet.tsx:6-140` | **call** | `side`, `widthClass` props; `SheetContent/Header/Title/Description`. |
| `providerCatalogMode` | `src/lib/agents/providerCatalog.ts:71` | **avoid collision** | Existing file = live/manual model-catalogue mode; NEW catalog must not reuse this name. |
| `login.tsx` button + `Rocket` + post-login redirect | `src/routes/login.tsx:4,32-38,179-190` | **modify** | Remove button+import (FR-009); **retain** 32-38 redirect (MIN-002). |
| `_app.tsx` beforeLoad | `src/routes/_app.tsx:13-41` | **read (safety)** | Onboarding-before-auth; `/state`-throw falls through to `/login` (R2-06). |
| `HandleProviders` / `withOptionalAuth` | `rest.go:4493` / `rest_auth.go:235` | **reference** | Auth pattern (not used — no new endpoint). |

### Impact Assessment

| Symbol Modified | Risk | d=1 (WILL BREAK) | d=2 (test) |
|---|---|---|---|
| Delete `AVAILABLE_PROVIDERS` | **HIGH** | `-onboarding.test.tsx:1029-1074` invariant; `ModelKeyStep` consumers; `sortProvidersByPriority`/`resolveVariantId` | any import of `AVAILABLE_PROVIDERS`/`PLAN_LABELS` across `src/` |
| `PLAN_LABELS` relabel (`api→Standard API`) | **MEDIUM** | `ModelKeyStep` render; `-onboarding.test.tsx` label assertions | Settings variant rows |
| `ProvidersSection` inline-expand → `Sheet` | **MEDIUM** | `ProvidersSection.test.tsx` (expand assertions) | Settings screen e2e |
| `ConnectorsScreen` flat list → grouped rows | **MEDIUM** (Track 2, ADR-029-gated) | `-channels.test.tsx`; `channel-*.spec.ts` e2e | ConnectorsScreen render tests |
| Add `ProviderCatalogEntry` schema + generated types | **MEDIUM** | `make verify-contracts` drift check | `contract_test.go` |
| Remove login button | **LOW** | `login.test.tsx` (if it asserts the button) | login e2e |

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| Onboarding probe (`POST /onboarding/probe-provider` → `GetDefaultAPIBase` → `FetchModels`/`ValidateKey`) | Unchanged; the catalog only supplies the *picker* metadata, not the probe. Catalog `id` must remain a valid probe id (drift-guard c). |
| Provider save (`PUT /providers/{id}` → encrypt key → `ModelConfig`) | Unchanged; the `Sheet` config calls the same `configureProvider`. |
| `_app.tsx` beforeLoad (onboarding-before-auth) | The login-cleanup safety proof; a regression test pins fresh-install still reaches `/onboarding`. |
| ADR-029 channel binding (`ResolveRoute` Priority-0 identity) | Track 2 renders the binding; does not alter routing. Hard prerequisite. |

### Cluster placement
Spans the **frontend settings/onboarding** cluster and the **backend providers** cluster (the catalog SoT). No security cluster involvement beyond the SVG-sanitization control.

---

## 4. User Stories & Acceptance Criteria

> Track tags: **[F]** = shared foundation, **[T1]** = Providers, **[T2]** = Channels (ADR-029-gated), **[L]** = login.

### US-1 — Provider catalog single source of truth [F] (P0)
A developer wants ONE backend-owned catalog of the ~30 user-facing providers (company/plan/region/wire/endpointHint/logoSlug/label/subtitle), embedded at build time and consumed identically by onboarding and Settings, so terminology and logos can never diverge and the frontend/backend duplication (`AVAILABLE_PROVIDERS`) ends.

**Why this priority**: Both tracks and onboarding depend on it; nothing else can be consistent without it.
**Independent Test**: Build the binary; assert the embedded `providers_catalog.json` parses to `[]ProviderCatalogEntry`, has ~30 entries, and the generated TS catalog imported by the SPA is byte-identical to the embedded JSON.

**Acceptance Scenarios**:
1. **Given** the Go catalog SoT with 30 entries, **When** `go generate` + build runs, **Then** `providers_catalog.json` is embedded and a generated TS catalog is produced from the same data.
2. **Given** a catalog entry, **When** the drift-guard test runs, **Then** its `id` satisfies `IsKnownProtocol(id)==true`, is a member of the `ProbeProviderRequest` enum, and (unless `azure`/`azure-openai`/`bedrock`) `GetDefaultAPIBase(id)` is non-empty.
3. **Given** a new user-facing protocol added to `knownProtocols` without a catalog entry, **When** the drift-guard runs, **Then** CI fails until a human adds it to the catalog or marks it `catalogVisible=false`.
4. **Given** the catalog JSON, **When** it is inspected, **Then** it contains no secrets (no api_key/credential fields).

### US-2 — Shared `<BrandIcon>` with real marks, fallback, disclaimer, sanitized SVGs [F] (P0)
An operator wants recognizable brand logos on every provider/channel across onboarding and Settings, rendered from bundled offline SVGs themed to the UI, with a graceful lettermark fallback and a visible trademark disclaimer — so the surfaces look polished and legally safe.

**Why this priority**: Foundation reused by every screen; the disclaimer + sanitization are the G-1=B legal/security controls.
**Independent Test**: Render `<BrandIcon slug="openai"/>` → real mark; `<BrandIcon slug="__missing__"/>` → lettermark, no throw; grep vendored SVGs for disallowed tokens → none.

**Acceptance Scenarios**:
1. **Given** a `logoSlug` with a vendored SVG, **When** `<BrandIcon>` renders, **Then** it shows the inline SVG themed via `currentColor`.
2. **Given** a `logoSlug` with no vendored asset, **When** `<BrandIcon>` renders, **Then** it shows a lettermark chip and does not throw.
3. **Given** any screen that renders a `<BrandIcon>`, **When** it loads, **Then** the "Logos are trademarks of their respective owners, used for identification only — no affiliation or endorsement implied" notice is present in the DOM (not tooltip-only).
4. **Given** the vendored SVG set, **When** the sanitization check runs, **Then** no SVG contains `<script>`, `<style>`, `<foreignObject>`, `<use>`, `on*=`, `javascript:`, or external `href`/`xlink:href`.

### US-3 — Providers list: configured-only + empty-state roster [T1] (P0)
An operator wants the Providers screen to list only providers they've actually configured, and — when none are — a roster of connectable providers to set up their first, so the screen reflects reality instead of a wall of possibilities.

**Why this priority**: The core mental-model fix for Track 1.
**Independent Test**: With 0 configured providers → empty-state roster renders the catalog; with ≥1 → only configured rows render.

**Acceptance Scenarios**:
1. **Given** no configured providers, **When** the screen loads, **Then** an empty-state roster of connectable providers (from the catalog) renders with "Connect" affordances.
2. **Given** ≥1 configured provider, **When** the screen loads, **Then** only configured providers render (grouped — US-5), not the full catalog.
3. **Given** a configured provider whose key is invalid/expired, **When** the screen loads, **Then** it **stays** in the list with a distinct status badge (never vanishes).
4. **Given** a configured provider with zero model slugs, **When** the screen loads, **Then** it stays listed with its status; the zero-model state is a per-provider detail, not a reason to hide it.

### US-4 — Provider config via `Sheet` slide-out [T1] (P1)
An operator wants to configure a provider in the app's standard slide-out panel (like channels), not an inline card expansion, so the interaction is consistent across the app.

**Why this priority**: Consistency + the ADR's explicit "Sheet for both config and connect" rule.
**Independent Test**: Clicking Configure on a provider row opens a `Sheet` (not an inline `expandedProvider` section); the key + model-slug editing works inside it.

**Acceptance Scenarios**:
1. **Given** a configured provider row, **When** the operator clicks Configure, **Then** a right-side `Sheet` opens with the provider's editable API key and (for manual providers) model-slug list.
2. **Given** the Connect flow from the empty-state roster, **When** the operator picks a provider, **Then** the same `Sheet` opens for first-time setup — no modal `Dialog`.
3. **Given** an open provider `Sheet`, **When** the operator saves a key change, **Then** it persists via the existing `configureProvider` path (re-auth confirmation preserved) and the sheet reflects the validation outcome.

### US-5 — Company-grouped, binding-first variant rows [T1] (P1)
An operator wants configured providers grouped by company with the company logo + name in the group header, and each row showing just the variant identity (`Access Type · Region`) without repeating the company — so a Zhipu Coding-Plan and a Zhipu Pay-as-you-go read as two endpoint-rows under one Zhipu header.

**Why this priority**: The variant-clarity core of Track 1.
**Independent Test**: Two configured Zhipu variants render under one "Zhipu / GLM" header; each row title is `Coding Plan · China` etc., never "Zhipu — Coding Plan".

**Acceptance Scenarios**:
1. **Given** ≥1 configured variant for a company, **When** the list renders, **Then** a group header shows the company logo + name and an in-group "Add another…".
2. **Given** a variant row inside its group, **When** it renders, **Then** the title is `<Access Type> · <Region>` (region omitted if none), with the wire shown as a derived badge — never the redundant company prefix.
3. **Given** a variant row, **When** the operator opens it, **Then** Plan/Region/Wire/Endpoint are **view-only** and only the API key is editable.

### US-6 — Corrected variant terminology (opencode-style, wire-as-badge) [T1] (P1)
An operator wants intuitive plan labels — "Standard API" / "Coding Plan" — with the OpenAI-/Anthropic-compatible **wire** shown as a derived badge (not a fake "plan"), and a subtitle naming the billing model + endpoint, so they select the right variant and stop hitting "insufficient balance".

**Why this priority**: Fixes the concrete mislabel bug and the intuitiveness gap.
**Independent Test**: The plan control offers {Standard API, Coding Plan}; "Anthropic API" is absent; a wire badge (openai-compatible / anthropic) shows per variant.

**Acceptance Scenarios**:
1. **Given** the plan control, **When** it renders, **Then** it shows "Standard API" (was `api:'Pay-as-you-go API'`) and "Coding Plan" (unchanged); "Anthropic API" is **not** a plan option.
2. **Given** a variant whose id matches `/-anthropic$/` or ∈ {anthropic, anthropic-messages, bedrock}, **When** its row renders, **Then** a wire badge reads "Anthropic-compatible"; otherwise "OpenAI-compatible".
3. **Given** a variant, **When** its subtitle renders, **Then** it names the billing model (e.g. "Pay-as-you-go, per token") and the `endpointHint`.
4. **Given** the shipped plan enum, **When** inspected, **Then** it does NOT contain "Token Plan" (reserved, not shipped).

### US-7 — Onboarding ModelKeyStep consumes the shared catalog + `<BrandIcon>` [T1] (P0)
An operator going through onboarding wants the exact same logos and plan/region/wire words they'll later see in Settings, so the product feels coherent from first run.

**Why this priority**: NFR-1 consistency is the whole driver of the redesign.
**Independent Test**: For the same provider id, onboarding and Settings render byte-identical `label`/`subtitle`/logo (asserted by a shared-catalog consistency test).

**Acceptance Scenarios**:
1. **Given** the onboarding provider step, **When** it renders a provider, **Then** its logo comes from `<BrandIcon>` and its label/subtitle come from the shared catalog — the same source Settings uses.
2. **Given** a provider present in both onboarding and Settings, **When** both render it, **Then** the `label`, `subtitle`, and `logoSlug` are identical (they read the same catalog entry).
3. **Given** the onboarding step, **When** it needs the plan/region vocabulary, **Then** it uses the catalog's terminology, not a local `PLAN_LABELS` copy.

### US-8 — Migration of existing configured providers [T1] (P0)
An operator who already configured providers (possibly under alias ids like `z.ai`, or self-hosted `ollama`) wants them to keep working and display correctly after the redesign, so nothing they set up breaks.

**Why this priority**: Breaking existing configs is unacceptable (Constraint #7).
**Independent Test**: A config with `z.ai` renders under the canonical Zhipu group; a config with `ollama` renders under a "Self-hosted / Custom" group; an unrecognized id renders generically — none crash.

**Acceptance Scenarios**:
1. **Given** a configured provider stored under an **alias** id (`z.ai`/`zai`/`glm-coding`), **When** the list renders, **Then** the alias is normalized to its canonical catalog entry and shown in the right company group.
2. **Given** a configured **self-hosted** provider (`litellm`/`ollama`/`vllm`), which is `catalogVisible=false` (excluded from the roster), **When** the list renders, **Then** it appears in a "Self-hosted / Custom" group with its `endpointHint`, fully functional — NOT demoted to unknown.
3. **Given** a configured provider with a **truly unrecognized** id, **When** the list renders, **Then** it appears under a generic group with the raw id and does not crash.

### US-9 — Channels list: type-grouped binding-first rows + empty roster [T2] (P1, ADR-029-gated)
An operator wants channel instances grouped under their type (with logo), each row showing `Workspace → Agent`, and an empty-state roster when none exist — so the multi-instance-per-workspace model is obvious at a glance.

**Why this priority**: The mental-model fix for Track 2; gated on ADR-029.
**Independent Test**: Three `whatsapp.*` instances render under one WhatsApp header, each row reading `Sales → Mia` etc.; empty state shows the channel roster.

**Acceptance Scenarios**:
1. **Given** ≥1 configured instance of a type, **When** the list renders, **Then** a type group header (logo + name + in-group "Add another…") groups its instances.
2. **Given** an instance row, **When** it renders, **Then** the title is `<Workspace> → <Agent>` (the ADR-029 binding), without the redundant channel-type prefix.
3. **Given** no configured channels, **When** the screen loads, **Then** an empty-state roster of connectable channel types renders.

### US-10 — Channel create + config via `Sheet` (no modal) [T2] (P2, ADR-029-gated)
An operator wants to create a new channel instance and configure it in the standard slide-out, not a modal dialog, so channel setup matches the rest of the app.

**Why this priority**: Consistency; lower priority as it follows US-9 and is ADR-029-gated.
**Independent Test**: "Add another…" opens a `Sheet` (not `AddInstanceDialog` modal) whose steps pick type→slug→workspace→agent.

**Acceptance Scenarios**:
1. **Given** the Channels screen, **When** the operator starts a create flow, **Then** it opens in a `Sheet`, not the modal `AddInstanceDialog`.
2. **Given** the create `Sheet`, **When** the operator completes type→slug→workspace→agent, **Then** the instance is created via the ADR-029 path and appears grouped under its type.

### US-11 — Remove login re-onboard button, retain post-login redirect [L] (P1)
An operator on the login screen should not see a redundant "Set up Omnipus for the first time" button (fresh installs reach onboarding automatically), but a logged-in-not-onboarded admin must still be routed to onboarding after login.

**Why this priority**: Small, independent cleanup; ships anytime.
**Independent Test**: The button + `Rocket` import are gone; a fresh (not-onboarded) install still lands on `/onboarding`; a post-login not-onboarded admin still redirects to `/onboarding`.

**Acceptance Scenarios**:
1. **Given** the login screen, **When** it renders, **Then** the "Set up Omnipus for the first time" button and the `Rocket` import are absent.
2. **Given** a fresh (not-onboarded) install, **When** the operator navigates to any `_app` route, **Then** `_app.tsx` beforeLoad redirects to `/onboarding` (button not needed).
3. **Given** a not-onboarded admin who logs in successfully, **When** login completes, **Then** `login.tsx:32-38` still redirects them to `/onboarding` (retained).

### Edge Cases
- **Catalog id in `knownProtocols` but not in the probe enum** → drift-guard property (c) fails CI (US-1/AS-2). *This is the MAJ-001 protection.*
- **`GetDefaultAPIBase` returns `""` for a catalog id** → allowed only for `azure`/`azure-openai`/`bedrock`; drift-guard exempts exactly those; any other empty base fails.
- **A vendored SVG carrying an inline `<script>`** → sanitization check fails the build (US-2/AS-4).
- **`/api/v1/state` unreachable on a fresh install** → `_app.tsx` sends the user to `/login`; with the button removed there is no onboarding affordance there (accepted — a dead `/state` is a broader failure; R2-06).
- **A provider configured with an alias whose canonical entry was later removed from the catalog** → falls through to the generic group with the raw id (US-8/AS-3).
- **Two variants of the same company both configured** → both render as separate endpoint-rows under one header (US-5/AS-1).
- **ADR-029 still `Proposed` at implementation time** → Track 2 (US-9/US-10) MUST NOT ship; Track 1 + foundation + login proceed.

---

## 5. Behavioral Contract & Boundaries

### Behavioral Contract
**Primary flows**
- When the binary builds, the system embeds `providers_catalog.json` and generates a byte-identical TS catalog for the SPA.
- When a provider/channel screen has ≥1 configured entry, the system renders only configured entries, grouped; when it has none, it renders the catalog roster.
- When the operator configures or connects a provider/channel, the system opens a `Sheet` slide-out (never a modal `Dialog`).
- When any screen renders a brand mark, the system renders it from `<BrandIcon>` (shared catalog `logoSlug`) and shows the trademark disclaimer.
- When onboarding and Settings render the same provider, the system shows identical label/subtitle/logo (same catalog entry).

**Error flows**
- When a catalog id is not in the `ProbeProviderRequest` enum / not a known protocol, the system fails the drift-guard test (build/CI blocked).
- When a `logoSlug` has no asset, the system renders a lettermark fallback without throwing.
- When a configured provider's key is invalid, the system keeps it listed with a distinct status badge.

**Boundary conditions**
- When a configured id is an alias, the system normalizes it to the canonical catalog entry before display.
- When a configured id is a known self-hosted (`catalogVisible=false`) provider, the system shows it under "Self-hosted / Custom".
- When a configured id is unrecognized, the system shows it generically without crashing.

### Explicit Non-Behaviors
- The system MUST NOT serve the catalog from a live HTTP endpoint (embed only) — because it adds a pre-auth fingerprinting surface and a network dependency in onboarding for static data.
- The system MUST NOT derive `label`/`subtitle` frontend-side — because that would reintroduce the G-3=C terminology-drift risk the wire-carried strings eliminate.
- The system MUST NOT change provider probe/validation/routing or channel routing behavior — those are out of scope (ADR-029 owns channel routing).
- The system MUST NOT add "Token Plan" to the shipped plan enum — no variant maps to it (avoid dead branches).
- The system MUST NOT hide a configured self-hosted provider just because it's excluded from the roster.
- The system MUST NOT remove the post-login `!onboarding_complete → /onboarding` redirect (`login.tsx:32-38`).
- The system MUST NOT introduce a runtime i18n dependency to justify the label split (there is no i18n; the split is not taken).
- The system MUST NOT ship Track 2 (Channels) UI while ADR-029 is `Proposed`.

### Integration Boundaries

#### Provider catalog artifact (backend SoT → SPA)
- **Data in**: none at runtime — the catalog is authored as a Go SoT (`pkg/providers/catalog/catalog.go`).
- **Data out**: `providers_catalog.json` (`//go:embed`) for the backend; a generated `src/lib/generated/providerCatalog.ts` for the SPA, both derived from the same SoT via `go generate` + a gen-contracts step.
- **Contract**: the `ProviderCatalogEntry` **type** is defined in `contracts/components/schemas/ProviderCatalogEntry.yaml` and generated to Go + TS (Constraint #8 satisfied for the *type*); the *data* never crosses the gateway/SPA HTTP boundary, so no path/endpoint is added.
- **On failure**: a malformed SoT fails `go generate`/build; a drift between the embedded JSON and the generated TS fails `make verify-contracts`; a catalog↔protocol/probe-enum drift fails `contract_test.go`.
- **Development**: real — the SoT is the source; no mock.

#### `ProbeProviderRequest` enum (existing contract — read-only invariant)
- **Data in / out**: unchanged; the probe flow is untouched.
- **Contract**: the catalog's id-set MUST remain ⊆ this enum (drift-guard property c). Adding a catalog id that isn't in the enum requires updating `ProbeProviderRequest.yaml` + regen first.
- **On failure**: drift-guard test fails.
- **Development**: real.

#### ADR-029 channel binding (Track 2 only)
- **Data in / out**: Track 2 reads instance→workspace→agent bindings; it does not write routing.
- **Contract**: ADR-029 must be `Accepted`; the `ChannelInstanceConfig.WorkspaceID` + identity routing must exist.
- **On failure**: Track 2 is not implemented until the dependency lands.
- **Development**: gated — Track 2 BDD/tests are written but not executed until ADR-029 merges.

---

## 6. BDD Scenarios

### Feature: Provider catalog SoT + drift-guard (US-1)

#### Scenario: Build embeds the catalog and generates a matching TS copy
**Traces to**: US-1, AS-1 · **Category**: Happy Path
- **Given** the Go catalog SoT with 30 `ProviderCatalogEntry` values
- **When** `go generate ./pkg/providers/catalog/...` and the build run
- **Then** `pkg/providers/catalog/data/providers_catalog.json` exists and is embedded
- **And** `src/lib/generated/providerCatalog.ts` exports the same 30 entries, byte-identical in field values.

#### Scenario Outline: Drift-guard validates every catalog id
**Traces to**: US-1, AS-2 · **Category**: Happy Path
- **Given** a catalog entry with id `<id>`
- **When** the drift-guard `contract_test.go` runs
- **Then** `IsKnownProtocol(<id>)` is true
- **And** `<id>` is a member of the `ProbeProviderRequest` id enum
- **And** `GetDefaultAPIBase(<id>)` is non-empty unless `<id>` is deployment-configured.

**Examples**:
| id | known | in probe enum | base non-empty |
|---|---|---|---|
| openai | true | true | true |
| z-ai-coding | true | true | true |
| ollama | true | true | true (localhost) |
| azure | true | true | false (deployment-configured, exempt) |
| bedrock | true | true | false (exempt) |

#### Scenario: New user-facing protocol without a catalog entry fails CI
**Traces to**: US-1, AS-3 · **Category**: Error Path
- **Given** a protocol added to `knownProtocols` and marked user-facing (`catalogVisible=true`)
- **And** no corresponding catalog entry
- **When** the drift-guard runs
- **Then** the test fails with a message naming the untriaged id.

#### Scenario: Catalog carries no secrets
**Traces to**: US-1, AS-4 · **Category**: Edge Case
- **Given** the embedded `providers_catalog.json`
- **When** the secret-free assertion runs
- **Then** no entry contains an `api_key`/credential field.

### Feature: Shared `<BrandIcon>` (US-2)

#### Scenario: Known slug renders the real mark
**Traces to**: US-2, AS-1 · **Category**: Happy Path
- **Given** a `logoSlug` "openai" with a vendored SVG
- **When** `<BrandIcon slug="openai"/>` renders
- **Then** an inline SVG themed with `currentColor` appears.

#### Scenario: Missing slug falls back to a lettermark
**Traces to**: US-2, AS-2 · **Category**: Error Path
- **Given** a `logoSlug` "__missing__" with no vendored asset
- **When** `<BrandIcon slug="__missing__"/>` renders
- **Then** a lettermark chip renders
- **And** no exception is thrown.

#### Scenario: Trademark disclaimer is present wherever marks appear
**Traces to**: US-2, AS-3 · **Category**: Happy Path
- **Given** a screen rendering ≥1 `<BrandIcon>`
- **When** it loads
- **Then** the "identification only, no endorsement" notice is in the DOM and visible (not tooltip-only).

#### Scenario: Vendored SVGs pass sanitization
**Traces to**: US-2, AS-4 · **Category**: Edge Case
- **Given** the vendored SVG set
- **When** the build-time sanitizer/allow-list check runs
- **Then** no SVG contains `<script>`, `<style>`, `<foreignObject>`, `<use>`, `on*=`, `javascript:`, or external `href`/`xlink:href`.

### Feature: Providers configured-only list + roster (US-3)

#### Scenario: Empty state shows the connectable roster
**Traces to**: US-3, AS-1 · **Category**: Happy Path
- **Given** zero configured providers
- **When** the Providers screen loads
- **Then** an empty-state roster from the catalog renders with "Connect" affordances.

#### Scenario: Populated list shows only configured providers
**Traces to**: US-3, AS-2 · **Category**: Happy Path
- **Given** two configured providers out of the full catalog
- **When** the screen loads
- **Then** exactly those two (grouped) render — not the full catalog.

#### Scenario: Invalid-key provider stays listed with a status badge
**Traces to**: US-3, AS-3 · **Category**: Error Path
- **Given** a configured provider whose key now fails validation (`status: error`)
- **When** the screen loads
- **Then** the provider remains in the list with an error status badge.

#### Scenario: Zero-model provider stays listed
**Traces to**: US-3, AS-4 · **Category**: Edge Case
- **Given** a configured manual provider with zero model slugs
- **When** the screen loads
- **Then** it stays listed with its status; it is not hidden.

### Feature: Provider `Sheet` config (US-4)

#### Scenario: Configure opens a Sheet, not an inline expand
**Traces to**: US-4, AS-1 · **Category**: Happy Path
- **Given** a configured provider row
- **When** the operator clicks Configure
- **Then** a right-side `Sheet` opens with the editable API key (+ manual model-slug list)
- **And** no inline `expandedProvider` section is used.

#### Scenario: Connect from the roster uses the same Sheet
**Traces to**: US-4, AS-2 · **Category**: Alternate Path
- **Given** the empty-state roster
- **When** the operator clicks Connect on a catalog provider
- **Then** the same `Sheet` opens for first-time setup (no modal `Dialog`).

#### Scenario: Saving a key persists via the existing path
**Traces to**: US-4, AS-3 · **Category**: Happy Path
- **Given** an open provider `Sheet`
- **When** the operator changes the key and saves (confirming re-auth)
- **Then** `configureProvider` persists it and the `Sheet` shows the validation outcome banner.

### Feature: Company-grouped variant rows + terminology (US-5, US-6)

#### Scenario: Two variants group under one company header
**Traces to**: US-5, AS-1 · **Category**: Happy Path
- **Given** two configured Zhipu variants (Coding Plan, Standard API)
- **When** the list renders
- **Then** one "Zhipu / GLM" header (logo + name + "Add another…") groups both rows.

#### Scenario: Row title omits the redundant company prefix
**Traces to**: US-5, AS-2 · **Category**: Happy Path
- **Given** a Zhipu Coding-Plan variant row inside its group
- **When** it renders
- **Then** the title is `Coding Plan · China` (region if any), with a wire badge — not "Zhipu — Coding Plan".

#### Scenario: Variant fields are view-only except the key
**Traces to**: US-5, AS-3 · **Category**: Happy Path
- **Given** a variant `Sheet`
- **When** it opens
- **Then** Plan/Region/Wire/Endpoint are read-only and only the API key is editable.

#### Scenario: Plan control drops "Anthropic API"; renames "Standard API"
**Traces to**: US-6, AS-1 · **Category**: Happy Path
- **Given** the plan control
- **When** it renders
- **Then** it offers "Standard API" and "Coding Plan"
- **And** "Anthropic API" is not an option.

#### Scenario Outline: Wire badge is derived, not chosen
**Traces to**: US-6, AS-2 · **Category**: Happy Path
- **Given** a variant with id `<id>`
- **When** its row renders
- **Then** the wire badge reads `<wire>`.

**Examples**:
| id | wire |
|---|---|
| openai | OpenAI-compatible |
| z-ai-coding | OpenAI-compatible |
| z-ai-anthropic | Anthropic-compatible |
| anthropic | Anthropic-compatible |
| bedrock | Anthropic-compatible |

#### Scenario: Token Plan is not in the shipped enum
**Traces to**: US-6, AS-4 · **Category**: Edge Case
- **Given** the shipped plan enum
- **When** inspected
- **Then** it contains no "Token Plan" value.

### Feature: Onboarding↔Settings consistency (US-7)

#### Scenario: Onboarding renders logos + labels from the shared catalog
**Traces to**: US-7, AS-1 · **Category**: Happy Path
- **Given** the onboarding provider step
- **When** it renders a provider
- **Then** its logo is `<BrandIcon>` and its label/subtitle come from the shared catalog entry.

#### Scenario: Same provider renders identically in both surfaces
**Traces to**: US-7, AS-2 · **Category**: Happy Path
- **Given** a provider present in onboarding and Settings
- **When** both render it
- **Then** `label`, `subtitle`, and `logoSlug` are identical (the blocking consistency test).

### Feature: Migration of existing configs (US-8)

#### Scenario Outline: Configured ids resolve to a display home
**Traces to**: US-8, AS-1/AS-2/AS-3 · **Category**: Edge Case
- **Given** a configured provider stored under id `<stored>`
- **When** the list renders
- **Then** it appears under group `<group>` without crashing.

**Examples**:
| stored | group |
|---|---|
| z.ai | Zhipu / GLM (canonical) |
| zai | Zhipu / GLM (canonical) |
| glm-coding | Zhipu / GLM (canonical) |
| ollama | Self-hosted / Custom |
| litellm | Self-hosted / Custom |
| some-unknown-x | Generic (raw id) |

### Feature: Channels grouped rows + Sheet create (US-9, US-10 — ADR-029-gated)

#### Scenario: Instances group under their channel type
**Traces to**: US-9, AS-1 · **Category**: Happy Path
- **Given** three configured `whatsapp.*` instances
- **When** the Channels list renders
- **Then** one WhatsApp header groups all three.

#### Scenario: Instance row shows the workspace→agent binding
**Traces to**: US-9, AS-2 · **Category**: Happy Path
- **Given** an instance `whatsapp.sales` bound to workspace "Sales" / agent "Mia"
- **When** its row renders
- **Then** the title reads "Sales → Mia" without a redundant "WhatsApp" prefix.

#### Scenario: Channels empty state shows the type roster
**Traces to**: US-9, AS-3 · **Category**: Happy Path
- **Given** no configured channels
- **When** the screen loads
- **Then** an empty-state roster of channel types renders.

#### Scenario: Create flow opens in a Sheet, not a modal
**Traces to**: US-10, AS-1 · **Category**: Happy Path
- **Given** the Channels screen
- **When** the operator starts "Add another…"
- **Then** a `Sheet` opens (not the modal `AddInstanceDialog`).

#### Scenario: Completing the create Sheet creates the instance
**Traces to**: US-10, AS-2 · **Category**: Happy Path
- **Given** the create `Sheet`
- **When** the operator completes type→slug→workspace→agent
- **Then** the instance is created (ADR-029 path) and appears grouped under its type.

### Feature: Login cleanup (US-11)

#### Scenario: Login screen has no re-onboard button
**Traces to**: US-11, AS-1 · **Category**: Happy Path
- **Given** the login screen
- **When** it renders
- **Then** the "Set up Omnipus for the first time" button and the `Rocket` import are absent.

#### Scenario: Fresh install still reaches onboarding
**Traces to**: US-11, AS-2 · **Category**: Happy Path
- **Given** a not-onboarded install (`/state` reachable)
- **When** the operator navigates to any `_app` route
- **Then** `_app.tsx` beforeLoad redirects to `/onboarding`.

#### Scenario: Post-login redirect retained for not-onboarded admin
**Traces to**: US-11, AS-3 · **Category**: Alternate Path
- **Given** a not-onboarded admin
- **When** they log in successfully
- **Then** `login.tsx:32-38` redirects them to `/onboarding`.

---

## 7. Test-Driven Development Plan

### Test Hierarchy
| Level | Scope | Purpose |
|---|---|---|
| Unit | Go: drift-guard, wire-derivation, catalog parse; TS: `<BrandIcon>`, migration mapper, catalog consumers | Logic in isolation |
| Integration | SPA screen renders (vitest + RTL) over the embedded catalog; onboarding≡Settings consistency | Components + catalog together |
| E2E | Playwright: providers configure-in-Sheet, empty roster, login cleanup; (Track 2 gated) channels grouping | Full user flows |

### Test Implementation Order
| # | Test Name | Level | Traces to BDD | Description |
|---|---|---|---|---|
| 1 | `TestCatalog_ParsesAndCountIsExpected` | Unit(Go) | Build embeds catalog | Embedded JSON parses to `[]ProviderCatalogEntry`, ~30 entries. |
| 2 | `TestCatalog_DriftGuard_IdIsKnownProtocol` | Unit(Go) | Drift-guard outline | Every catalog id `IsKnownProtocol`. |
| 3 | `TestCatalog_DriftGuard_IdInProbeEnum` | Unit(Go) | Drift-guard outline | Every catalog id ∈ `ProbeProviderRequest` enum (re-homes `-onboarding.test.tsx:1029`). |
| 4 | `TestCatalog_DriftGuard_BaseNonEmptyOrExempt` | Unit(Go) | Drift-guard outline | `GetDefaultAPIBase` non-empty unless azure/azure-openai/bedrock. |
| 5 | `TestCatalog_DriftGuard_NewProtocolUntriagedFails` | Unit(Go) | New protocol fails CI | A `catalogVisible` protocol missing from catalog → fail. |
| 6 | `TestCatalog_NoSecretsInPayload` | Unit(Go) | Catalog carries no secrets | No credential fields in the JSON. |
| 7 | `TestWireDerivation_Table` | Unit(Go) | Wire badge outline | `wire(id)` matches the anthropic-vs-openai rule for all ids. |
| 8 | `TestContract_ProviderCatalogEntry_Shape` | Unit(Go) | (contract) | Go type marshals to schema-valid JSON. |
| 9 | `brandicon.knownSlug.test` | Unit(TS) | Known slug renders mark | Real SVG for a vendored slug. |
| 10 | `brandicon.missingSlug.test` | Unit(TS) | Missing slug fallback | Lettermark, no throw. |
| 11 | `svgSanitize.test` | Unit(TS/build) | SVGs pass sanitization | Allow-list check via DOMPurify; deny tokens absent. |
| 12 | `migration.resolveDisplay.test` | Unit(TS) | Migration outline | alias→canonical / self-hosted / unknown mapping. |
| 13 | `catalogGeneratedTs.matchesEmbed.test` | Unit(TS) | Build embeds + TS copy | Generated TS catalog equals the embedded JSON. |
| 14 | `providers.emptyRoster.test` | Integration | Empty roster | 0 configured → catalog roster. |
| 15 | `providers.configuredOnly.test` | Integration | Populated list | Only configured render. |
| 16 | `providers.invalidKeyStaysListed.test` | Integration | Invalid-key stays | Error-status provider remains. |
| 17 | `providers.zeroModelStaysListed.test` | Integration | Zero-model stays | Listed with status. |
| 18 | `providers.configureOpensSheet.test` | Integration | Configure opens Sheet | `Sheet`, not inline expand. |
| 19 | `providers.connectUsesSheet.test` | Integration | Connect uses Sheet | Roster Connect → Sheet. |
| 20 | `providers.groupedVariantRows.test` | Integration | Grouped rows | Two Zhipu variants under one header. |
| 21 | `providers.rowTitleNoPrefix.test` | Integration | Row title no prefix | `Coding Plan · China`. |
| 22 | `providers.variantViewOnlyKeyEditable.test` | Integration | View-only variant | Only key editable. |
| 23 | `providers.planControlLabels.test` | Integration | Plan labels | "Standard API"/"Coding Plan"; no "Anthropic API"/"Token Plan". |
| 24 | `onboardingSettings.consistency.test` (**blocking**) | Integration | Same provider identical | label/subtitle/logo identical across surfaces. |
| 25 | `login.noReonboardButton.test` | Integration | No re-onboard button | Button + `Rocket` gone. |
| 26 | `login.postLoginRedirectRetained.test` | Integration | Post-login redirect | Not-onboarded admin → `/onboarding`. |
| 27 | `app.freshInstallReachesOnboarding.test` | Integration | Fresh install onboarding | `_app` beforeLoad redirect. |
| 28 | `e2e/providers-configure-sheet.spec.ts` | E2E | Configure/Connect Sheet | Real gateway + embedded catalog. |
| 29 | `e2e/providers-empty-roster.spec.ts` | E2E | Empty roster | Fresh provider state. |
| 30 | `e2e/login-cleanup.spec.ts` | E2E | Login cleanup | Button absent; onboarding reachable. |
| 31 | `e2e/channels-grouping.spec.ts` (**ADR-029-gated**) | E2E | Channels grouping | Only run once ADR-029 merged. |

### Test Datasets

#### Dataset: Wire derivation (`wire(id)`)
| # | id | Boundary | Expected | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | openai | happy | openai-compatible | Wire badge outline | default branch |
| 2 | z-ai-coding | happy | openai-compatible | Wire badge outline | coding plan is OpenAI-wire |
| 3 | z-ai-anthropic | suffix match | anthropic | Wire badge outline | `/-anthropic$/` |
| 4 | anthropic | explicit set | anthropic | Wire badge outline | id ∈ set |
| 5 | anthropic-messages | explicit set | anthropic | Wire badge outline | id ∈ set |
| 6 | bedrock | explicit set | anthropic | Wire badge outline | id ∈ set |
| 7 | coding-plan-anthropic | suffix match | anthropic | Wire badge outline | compound suffix |

#### Dataset: Migration display resolution
| # | stored id | Boundary | Expected group | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | z-ai | canonical | Zhipu/GLM | Migration outline | canonical already |
| 2 | z.ai | alias | Zhipu/GLM | Migration outline | normalize dot |
| 3 | zai | alias | Zhipu/GLM | Migration outline | normalize |
| 4 | glm-coding | alias | Zhipu/GLM | Migration outline | alias→z-ai-coding |
| 5 | ollama | known-excluded | Self-hosted/Custom | Migration outline | catalogVisible=false |
| 6 | vllm | known-excluded | Self-hosted/Custom | Migration outline | localhost:8000 |
| 7 | litellm | known-excluded | Self-hosted/Custom | Migration outline | localhost:4000 |
| 8 | "" (empty) | null/edge | Generic (no crash) | Migration outline | defensive |
| 9 | zzz-unknown | unknown | Generic (raw id) | Migration outline | fallback |

#### Dataset: Drift-guard base-URL exemption
| # | id | Boundary | `GetDefaultAPIBase` | Guard verdict | Traces to |
|---|---|---|---|---|---|
| 1 | openai | happy | non-empty | pass | Drift outline |
| 2 | ollama | localhost | non-empty | pass | Drift outline |
| 3 | azure | exempt | "" | pass (exempt) | Drift outline |
| 4 | azure-openai | exempt | "" | pass (exempt) | Drift outline |
| 5 | bedrock | exempt | "" | pass (exempt) | Drift outline |
| 6 | (hypothetical non-exempt with "") | error | "" | **fail** | Drift outline |

#### Dataset: SVG sanitization tokens (deny)
| # | SVG snippet | Boundary | Expected | Traces to |
|---|---|---|---|---|
| 1 | clean `<path>` only | happy | pass | Sanitize |
| 2 | contains `<script>` | error | fail | Sanitize |
| 3 | contains `<style>@import` | error | fail | Sanitize |
| 4 | `onload=` attr | error | fail | Sanitize |
| 5 | `href="javascript:…"` | error | fail | Sanitize |
| 6 | `<use href="data:…">` | error | fail | Sanitize |
| 7 | external `xlink:href` | error | fail | Sanitize |

### Regression Test Requirements
**Modifying existing functionality:**
| Existing behaviour | Existing test | New regression test | Notes |
|---|---|---|---|
| `AVAILABLE_PROVIDERS ⊆ ProbeProviderRequest` | `-onboarding.test.tsx:1029-1074` | `TestCatalog_DriftGuard_IdInProbeEnum` (#3) | Invariant re-homed to backend before the const is deleted. |
| Per-company variant counts (Zhipu=6…) | `-onboarding.test.tsx:1046-1068` | catalog-seed count test (or drop w/ justification) | Guarded `AVAILABLE_PROVIDERS` completeness; now the catalog's job. |
| Provider save/probe (`configureProvider`, `probe-provider`) | existing provider tests | none new — unchanged | Sheet reuses the same calls. |
| Onboarding probe flow | `-onboarding.test.tsx` probe tests | none new — unchanged | Catalog only supplies picker metadata. |
| Login post-login redirect | `login.test.tsx` (if present) | #26 | Retain the redirect while removing the button. |
| Channel routing (ADR-029) | `-channels.test.tsx`, `channel-*.spec.ts` | Track-2 render tests | UI-only; routing unchanged. |

---

## 8. Requirements & Success Criteria

### Functional Requirements
- **FR-001**: The system MUST author the provider catalog as a single backend Go SoT and emit both an embedded `providers_catalog.json` and a generated SPA TS catalog from it. *(US-1; ADR G-2=B, embed decision)*
- **FR-002**: The catalog id-set MUST be the curated ~30 user-facing subset governed by a hand-authored `catalogVisible` allow-list; alias/CLI/self-hosted-infra ids are excluded from the roster but remain instantiable. *(US-1/US-8; ADR §6 G-2 pt 1)*
- **FR-003**: A drift-guard contract test MUST assert (a) every catalog id `IsKnownProtocol`, (b) every `catalogVisible` protocol is in the catalog, and (c) every catalog id ∈ the `ProbeProviderRequest` enum. *(US-1; ADR §6 G-2 pt 2, MAJ-001)*
- **FR-004**: The catalog MUST carry `endpointHint` as the curated display host (reused from `ProviderVariant.endpointHint`), NOT a `GetDefaultAPIBase`-derived value. *(US-1; ADR R2-01/R2-03)*
- **FR-005**: `wire` MUST be a closed enum `{openai-compatible, anthropic}` derived by `id matches /-anthropic$/ OR id ∈ {anthropic, anthropic-messages, bedrock} → anthropic else openai-compatible`. *(US-6; ADR §6 G-2 pt 3)*
- **FR-006**: The plan labels MUST be `api → "Standard API"` and `coding → "Coding Plan"`; `anthropic` MUST be dropped from plans (→ wire badge); "Token Plan" MUST NOT be in the shipped enum. *(US-6; ADR FR-6)*
- **FR-007**: The catalog MUST carry `label` and `subtitle` as fields (not frontend-derived), consumed identically by onboarding and Settings. *(US-6/US-7; label-on-wire decision)*
- **FR-008**: Both Providers and Channels lists MUST show only configured entries; empty → a catalog/type roster with Connect affordances. "Configured" = has a persisted config entry, independent of key validity or enabled state. *(US-3/US-9; ADR FR-1, MAJ-002)*
- **FR-009**: Configuring AND connecting MUST use a `Sheet` slide-out; no modal `Dialog`. *(US-4/US-10; ADR FR-2)*
- **FR-010**: Rows MUST be binding-first with the redundant type/company prefix dropped; grouping is adaptive (grouped when ≥1 entry, roster when empty — mutually exclusive). *(US-5/US-9; ADR FR-3/FR-4)*
- **FR-011**: A provider variant's Plan/Region/Wire/Endpoint MUST be view-only; only the API key editable. *(US-5; ADR FR-5)*
- **FR-012**: Migration MUST resolve a stored id in three cases — alias→canonical, known-excluded self-hosted→"Self-hosted/Custom" group, unknown→generic — never crashing or hiding a configured provider. *(US-8; ADR §7 G-4/MAJ-004)*
- **FR-013**: A shared `<BrandIcon>` MUST render bundled offline SVGs themed via `currentColor`, with a lettermark fallback for an unknown `logoSlug` (no throw). *(US-2; ADR FR-8/FR-11)*
- **FR-014**: A trademark disclaimer MUST render in the DOM on every screen showing a `<BrandIcon>` (not tooltip-only). *(US-2; ADR FR-10)*
- **FR-015**: Vendored SVGs MUST be sanitized (allow-list via DOMPurify at build time); a check MUST fail on any disallowed element/attribute. *(US-2; ADR FR-12/R2-09)*
- **FR-016**: The catalog MUST NOT be served from a live HTTP endpoint (embed only). *(US-1; embed decision)*
- **FR-017**: The login "Set up Omnipus…" button + `Rocket` import MUST be removed; the `login.tsx:32-38` post-login redirect MUST be retained. *(US-11; ADR FR-9, MIN-002)*
- **FR-018**: Track 2 (Channels UI: US-9/US-10) MUST NOT ship until ADR-029 is `Accepted`; Track 1 + foundation + login proceed independently. *(sequencing; ADR §7/R2-11)*
- **FR-019**: The onboarding↔Settings consistency test MUST be a blocking acceptance criterion. *(US-7; ADR R3/MAJ-003)*
- **FR-020**: The `ProviderCatalogEntry` type MUST be contract-defined (`contracts/components/schemas/`) and generated to Go + TS; no hand-written cross-boundary type. *(Constraint #8)*

### Success Criteria
- **SC-001**: `make verify-contracts` and the drift-guard `contract_test.go` pass; adding a `catalogVisible` protocol without a catalog entry makes CI red (demonstrable). *(FR-003)*
- **SC-002**: For all ~30 catalog providers, an automated test shows onboarding and Settings render identical `label`/`subtitle`/`logoSlug` (0 diffs). *(FR-007/FR-019)*
- **SC-003**: With 0 configured providers the Providers screen shows a roster; with N≥1 it shows exactly N grouped (no full-catalog leakage) — asserted in tests. *(FR-008)*
- **SC-004**: 100% of provider config/connect entry points open a `Sheet`; 0 modal `Dialog`s remain for provider config (grep + test). *(FR-009)*
- **SC-005**: All 9 migration dataset rows resolve to the correct group with 0 crashes. *(FR-012)*
- **SC-006**: `<BrandIcon>` renders a lettermark for an unknown slug with 0 thrown errors; the disclaimer string is present on every mark-bearing screen. *(FR-013/FR-014)*
- **SC-007**: The SVG sanitization check fails the build on any of the 6 deny-token fixtures and passes on the clean set. *(FR-015)*
- **SC-008**: The login screen renders no re-onboard button; a not-onboarded fresh install still reaches `/onboarding` (both via `_app` and post-login) — asserted in tests. *(FR-017)*
- **SC-009**: No live `/providers/catalog` route is registered (route-table assertion). *(FR-016)*
- **SC-010**: All applicable Quality Gates pass (gofmt, golangci-lint, go test `goolm,stdjson`, govulncheck, typecheck, vitest, verify-contracts) on the ci-omnipus worker. *(Constraint #7)*

### Traceability Matrix
| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-1 | Build embeds catalog | #1, #13 |
| FR-002 | US-1, US-8 | New protocol fails CI; Migration outline | #5, #12 |
| FR-003 | US-1 | Drift-guard outline; New protocol fails CI | #2, #3, #4, #5 |
| FR-004 | US-1 | Drift-guard outline | #4 (base), catalog parse #1 |
| FR-005 | US-6 | Wire badge outline | #7 |
| FR-006 | US-6 | Plan labels; Token Plan | #23 |
| FR-007 | US-6, US-7 | Same provider identical | #24 |
| FR-008 | US-3, US-9 | Empty roster; configured-only; invalid-key; zero-model; Channels empty | #14, #15, #16, #17, (#31) |
| FR-009 | US-4, US-10 | Configure opens Sheet; Connect uses Sheet; create Sheet | #18, #19, (#31), #28 |
| FR-010 | US-5, US-9 | Grouped rows; row title no prefix; instances group; binding title | #20, #21, (#31) |
| FR-011 | US-5 | View-only variant | #22 |
| FR-012 | US-8 | Migration outline | #12 |
| FR-013 | US-2 | Known slug; missing slug fallback | #9, #10 |
| FR-014 | US-2 | Disclaimer present | brandicon disclaimer assertion (in #9/#14) |
| FR-015 | US-2 | SVGs pass sanitization | #11 |
| FR-016 | US-1 | (non-behavior) no endpoint | #29 route assertion, SC-009 |
| FR-017 | US-11 | No re-onboard button; fresh install; post-login | #25, #26, #27, #30 |
| FR-018 | US-9, US-10 | (gating) | #31 gated; CI ordering |
| FR-019 | US-7 | Same provider identical (blocking) | #24 |
| FR-020 | US-1 | (contract) shape | #8 |

**Completeness check**: every FR maps to ≥1 BDD scenario + ≥1 test; every BDD scenario in §6 appears in the TDD plan (§7) and traces here.

---

## 9. Ambiguity Warnings (self-audit)

| # | What's ambiguous | Likely agent assumption | Resolution |
|---|---|---|---|
| 1 | Exact SPA TS catalog delivery (generated file location + how gen-contracts emits it) | Emit `src/lib/generated/providerCatalog.ts` from the same SoT during `make gen-contracts` | **Accepted assumption** — documented in §7 Integration Boundaries; implementer wires it into the gen pipeline; `catalogGeneratedTs.matchesEmbed.test` (#13) enforces parity. |
| 2 | Whether `contracts/` needs a *path* for the catalog type or just a component schema | Component schema only (type generation), no path/endpoint | **Resolved** — FR-016/FR-020: component schema for the type; embed for the data; no endpoint. |
| 3 | The "Self-hosted / Custom" group's exact label + whether it also appears in the empty-roster | Show self-hosted only when *configured*; do not advertise in the roster (they need operator endpoint) | **Accepted assumption** — documented; plan-spec BDD US-8/AS-2 covers the configured case; roster is catalogVisible-only. |
| 4 | Whether the provider `Sheet` reuses `ChannelConfigPanel`'s shell or a new `ProviderConfigSheet` | New `ProviderConfigSheet` reusing `Sheet` primitives (contexts differ) | **Accepted assumption** — G-3=C spirit (share primitives, not whole components). |
| 5 | Subtitle exact copy per plan (billing wording) | "Pay-as-you-go, per token" / "Subscription (Coding Plan)" + endpointHint | **Accepted assumption** — copy is catalog data; refine during implementation; consistency test guarantees parity regardless of wording. |
| 6 | Track-2 create `Sheet` step model (reuse ADR-029 slug rules) | Reuse ADR-029 `<type>.<slug>` grammar + workspace→agent mandatory | **Resolved** — inherits ADR-029; Track 2 gated anyway. |

All items are either resolved by the ADR/decisions or accepted as low-risk assumptions (recorded in §11). No blocking ambiguity remains.

---

## 10. Holdout Evaluation Scenarios

> Post-implementation only. NOT referenced in the TDD plan or traceability matrix.

### HO-1 (Happy): First-run consistency
- **Setup**: Fresh install, complete onboarding picking "Zhipu / GLM — Coding Plan (China)".
- **Action**: After onboarding, open Settings → Providers.
- **Expected**: The Zhipu logo, "Coding Plan" wording, and endpoint hint shown in Settings are visually identical to what onboarding showed.
- **Category**: Happy Path.

### HO-2 (Happy): Multi-variant grouping
- **Setup**: Configure two Zhipu variants (Coding Plan + Standard API).
- **Action**: Open Providers.
- **Expected**: One Zhipu header, two endpoint-rows, neither repeating "Zhipu" in the row title.
- **Category**: Happy Path.

### HO-3 (Happy): Empty-state roster
- **Setup**: Remove all configured providers.
- **Action**: Open Providers.
- **Expected**: A roster of connectable providers with logos + Connect; clicking Connect opens a slide-out.
- **Category**: Happy Path.

### HO-4 (Error): Invalid key visibility
- **Setup**: Configure a provider, then invalidate its key upstream.
- **Action**: Open Providers.
- **Expected**: The provider is still listed with an error status — not hidden.
- **Category**: Error.

### HO-5 (Error): Self-hosted survival
- **Setup**: Configure an `ollama` provider on an older build; upgrade to the redesign.
- **Action**: Open Providers.
- **Expected**: The ollama provider appears under "Self-hosted / Custom" with its localhost endpoint, still usable.
- **Category**: Error.

### HO-6 (Edge): Logo takedown resilience
- **Setup**: Delete one vendored SVG + clear its `logoSlug` in the catalog; rebuild.
- **Action**: Open any screen showing that provider.
- **Expected**: A lettermark renders in its place; no crash; disclaimer still present.
- **Category**: Edge.

### HO-7 (Edge): Login cleanup safety
- **Setup**: Fresh, not-onboarded install.
- **Action**: Navigate directly to `/` and to `/login`.
- **Expected**: `/` redirects to `/onboarding`; `/login` shows no re-onboard button; logging in (if creds exist) still routes a not-onboarded admin to `/onboarding`.
- **Category**: Edge.

---

## 11. Assumptions
- The SPA build can emit a generated TS catalog from the Go SoT during `make gen-contracts` (or an adjacent `go generate` + copy step). If not, an equivalent single-SoT mechanism is used; parity is test-enforced (#13).
- ADR-029 will reach `Accepted` before Track 2 is implemented; until then Track 2 is spec-complete but not built.
- The 20 vendored brand SVGs in `src/assets/brand-logos/` (currently uncommitted) are the logo source; missing brands use lettermarks.
- No i18n exists or is added; all catalog display strings are English.
- Provider probe/validation/routing and channel routing are unchanged (out of scope).
- The operator controls deploys; onboarding rollback is git-revert-and-redeploy (no in-place kill switch).

## 12. Clarifications

### 2026-07-02
- Q: How are catalog `label`/`subtitle` handled? → A: **Carried as catalog fields** (not frontend-derived), keeping G-3=C's no-drift guarantee unconditional.
- Q: How is the catalog delivered to the SPA? → A: **Build-time `go:embed` artifact + generated TS**, no live endpoint (removes pre-auth surface + network dep).
- Q: Rollback for the onboarding step swap? → A: **Git-revert-and-redeploy**; no SPA feature-flag infra exists and none is built.
- Q: Scope? → A: **Complete** ADR-031 (both tracks + onboarding + login), structured as Track 1 (Providers, ships first) and Track 2 (Channels, ADR-029-gated).

## Notes for `/taskify`
- **Two sprints/epics**: Track 1 (foundation + Providers + onboarding consistency + login) and Track 2 (Channels, gated on ADR-029).
- **Wave order for Track 1**: (W1) catalog SoT + contract type + drift-guard [backend-lead + qa-lead]; (W1‖) `<BrandIcon>` + SVG sanitize + disclaimer [frontend-lead]; (W2) Providers list/roster/Sheet/grouping/terminology [frontend-lead] + migration [frontend-lead]; (W2‖) onboarding consumes catalog [frontend-lead]; (W3) login cleanup [frontend-lead]; then 7-reviewer gate + fixes + qa.
- **Blocking gate**: the onboarding≡Settings consistency test (#24) must be green before Track 1 merges.
- **Do not** delete `AVAILABLE_PROVIDERS` until the drift-guard (#3) is green (invariant re-homed first).
