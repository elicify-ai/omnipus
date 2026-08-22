# Feature Specification: ADR-068 — Subscription login policy, provider deletion, the default model, and the provider UX at 190 providers

**Created**: 2026-08-22
**Status**: Draft (plan-spec output; Phase 1 gate taken as the ADR itself — see "Ambiguity Warnings" for everything the ADR is silent on)
**Input**: [`docs/internal/architecture/ADR-068-subscriptions-provider-deletion-and-provider-ux.md`](../architecture/ADR-068-subscriptions-provider-deletion-and-provider-ux.md) (Proposed, 2026-08-22) plus the §8a resolutions of [`ADR-066 …-review-pass2.md`](../architecture/ADR-066-context-budget-and-tool-result-routing-review-pass2.md) (MAJ-011, MAJ-012, MAJ-013, MAJ-015, MIN-008).
**Branch**: `feat/context-budget-and-tool-result-routing`
**Format precedent**: [`agent-form-requirements.md`](./agent-form-requirements.md) (UI spec), [`provider-validation-centralization-spec.md`](./provider-validation-centralization-spec.md) (provider REST spec)

> **Greenfield rule (operator, 2026-08-22) applies to every section below:** no backward compatibility, no migration, no aliasing of old names, no grace periods, no retired-name lists, no boot notification about removed things. Pre-existing state that does not match this design simply does not work — and fails on the generic unknown-provider path, never on a path that names what was removed.

> **Scope fence.** This spec covers ADR-068 only: D13 (subscription login policy, `antigravity` deletion, `claude-cli` descoping, the `codex-cli` / `openai-chatgpt` split), D14 (provider deletion, default model as a Settings control), §4 (UI surfaces), §5 (UX decisions and IA), §6–§7 (wire consequences), §8a (pass-2 resolutions). **The catalog's data source, schema, refresh and checksum are ADR-067** and are *referenced* here, never specified. Context-window limits and their display are ADR-066 D9, referenced only.

---

## Available Reference Patterns

`docs/reference/go-implementation/` does not exist in this repository (checked 2026-08-22: `ls docs/reference/go-implementation/` → no such directory). No reference patterns apply. In-repo precedents used instead:

| In-repo precedent | Pattern | Relevance |
|---|---|---|
| `pkg/gateway/rest.go::HandleProviders` (PUT branch) | credential-store-ready check → SSRF check → `providers.ValidateKey` → config write under `configMu` → `triggerReloadAndWaitOutcome` | DELETE and the default-model PUT follow the same write-then-wait-for-reload shape |
| `pkg/gateway/rest.go::HandleMCPServers` DELETE branch | sub-path dispatch for `DELETE /{id}` | the DELETE branch of `HandleProviders` mirrors it |
| `pkg/gateway/rest_default_agent_singleton_test.go` | "a PUT that returns 200 but changes no routing is the ADR-037 anti-pattern" | the default-model PUT must be proven to change the next turn's model |
| `src/components/ui/model-selector.tsx` | cmdk `Command` popover with typeahead | the shared provider picker is built on the same primitive |
| `src/components/settings/ReAuthDialog.tsx` | Radix `Dialog` consent primitive | extended to "session expired → re-sign-in" |

---

## Existing Codebase Context

> GitNexus: the only registered index on this machine (`~/.gitnexus/registry.json`) belongs to a **different checkout** (`wt-library-improvements`, branch `feat/library-improvements`); this worktree has no `.gitnexus/` directory and no GitNexus MCP tools were exposed to this session. Per CLAUDE.md the fallback to direct Read/Grep is correct; everything below was verified by reading the files on this branch on 2026-08-22. Impact levels are judged from direct caller counts found by grep, not from the graph.

### Symbols Involved

| Symbol | Role | Context (read on this branch) |
|---|---|---|
| `pkg/gateway/rest.go::HandleProviders` | **modifies** | Dispatches GET `/providers`, GET `model-capabilities`, PUT `/{id}`, POST `/{id}/refresh-models`, POST `/{id}/test`. No DELETE branch. Gains DELETE `/{id}`, GET/PUT `default-model`, POST `/{id}/sign-in`, GET `/{id}/sign-in/status`. |
| `pkg/gateway/rest.go::refreshProviderModels` | **modifies** | Live `/models` fetch; becomes the "Check with my account" entitlement intersection against the catalog (ADR-067 §4.3). |
| `pkg/gateway/rest.go::HandleProviders` GET branch | **modifies** | Builds `gen.Provider` rows; must emit the new `status` values, `auth_method`, `account_label`, `dependents`, `backs_default`. |
| `pkg/config/config.go::AgentDefaults.{Provider,ModelName}` | **modifies (writer added)** | Today written only at onboarding completion behind `ModelName == ""` guards (`gateway.go` two sites). The default-model PUT becomes the second writer. |
| `pkg/config/config.go::resolveFallbackProvider` | **calls** | Defines how a fallback slug maps to a provider; used to compute dependents. |
| `pkg/config/defaults.go` | **modifies** | Seeded provider templates; `antigravity/gemini-3-flash` default replaced. |
| `pkg/providers/factory_provider.go` | **modifies** | `case "antigravity"`, `case "claude-cli","claudecli"`, `case "codex-cli","codexcli"` (→ token-reuse HTTP path today). |
| `pkg/providers/antigravity_provider.go` (+ test) | **deleted** | 105 refs to the name; 33 files across the tree. |
| `pkg/providers/claude_cli_provider.go` (+ tests) | **deleted** | Descoped (D13 §2.3 item 2). |
| `pkg/providers/codex_cli_provider.go` | **keeps, re-keyed** | Becomes what the id `codex-cli` dispatches to (subprocess). |
| `pkg/providers/codex_provider.go`, `codex_cli_credentials.go` | **keeps, re-keyed** | Become provider id `openai-chatgpt` (direct HTTP with the CLI's saved token). |
| `pkg/auth/oauth.go::GoogleAntigravityOAuthConfig` | **deleted** | File stays; `OpenAIOAuthConfig`, `RequestDeviceCode`, `RefreshAccessToken` are used by `codex_provider.go`. |
| `pkg/credentials/store.go::Store.Delete` | **calls** | Deletes `<providerID>_API_KEY` on confirmed provider deletion. |
| `pkg/gateway/gateway.go::triggerReloadAndWaitOutcome` | **calls** | Applies config changes to live agent instances; DELETE and default-model PUT must wait on it. |
| `pkg/providers/catalog/gen/main.go` | **deleted** | Emits `src/lib/generated/providerCatalog.ts`; the TS emission goes. |
| `src/lib/generated/providerCatalog.ts` | **deleted** | 23-entry build-time file; SPA reads the providers-catalog GET (ADR-067 §5). |
| `src/routes/onboarding.tsx` (step 3, `PRIORITY_COMPANIES`, L1 tile grid ~L1249, L2 plan/region panel ~L1305) | **modifies** | Replaced by the shared picker + auth-method control; "Model for your first agent" field. |
| `src/components/settings/ProvidersSection.tsx` (`handleClose` L377, footer L605–645) | **modifies** | Default-model card, Remove provider, draft preservation, "Check with my account". |
| `src/components/settings/ProviderPickerSheet.tsx` | **replaced** | By the shared `ProviderPicker` component. |
| `src/components/settings/ProviderRow.tsx` | **modifies** | `signed_in` / `expired` states; Edit → Manage for sign-in providers. |
| `src/components/settings/ReAuthDialog.tsx` | **extends** | Re-sign-in variant for expired vendor sessions. |
| `src/components/ui/model-selector.tsx` | **modifies** | Vendor → release-date-desc ordering, Recommended chip, virtualisation above 100 items. |
| `contracts/components/schemas/ProbeProviderRequest.yaml` (+ `pkg/gateway/inboundschemas/` copy) | **modifies** | `id` enum (61 values, incl. `antigravity`, `claude-cli`, `codexcli`) → free string. |
| `contracts/components/schemas/Provider.yaml` | **modifies** | `status` enum + new fields. |
| `contracts/components/schemas/ProviderCatalogEntry.yaml` | **replaced by ADR-067's catalog schema** | Describes the 23-entry build-time catalog "never served from a live HTTP endpoint" — that description is now false by decision. |
| `contracts/components/schemas/OnboardingCompleteRequest.yaml` | **modifies** | `provider.api_key` required today; gains `auth_method`. |
| `contracts/asyncapi.yaml` LLMError code enum | **modifies** | New code for "agent has no model". |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents (WILL BREAK) | d=2 Dependents (LIKELY AFFECTED) |
|---|---|---|---|
| `ProbeProviderRequest.id` enum → string | **HIGH** | `pkg/api/generated/openapi_types.gen.go`, `src/lib/api/generated/{openapi-types.ts,schemas.ts}`, `pkg/gateway/inboundschemas/`, `pkg/gateway/rest_onboarding.go` probe handler, `contract_test.go` | onboarding step 3 probe call, `tests/e2e/providers.spec.ts`, `src/lib/api.ts` probe wrapper |
| `Provider.status` enum gains values | **HIGH** | every `switch`/ternary on `provider.status` in `ProviderRow.tsx`, `ProvidersSection.tsx`, `ProvidersSection.test.tsx`; Zod schema; Go `gen.ProviderStatus*` consts | `tests/e2e/providers.spec.ts`, onboarding status hints |
| `factory_provider.go` cases | **HIGH** | `factory_provider_test.go` rows; `pkg/providers/catalog/catalog_test.go` "CLI executor / non-API-key ids" allow-list; `IsKnownProtocol` | any agent entity naming `antigravity`/`claude-cli` (fails as unknown provider — intended) |
| `AgentDefaults.ModelName/Provider` second writer | **MEDIUM** | `gateway.go` onboarding guards (must not fight the PUT); `agent.Registry` model resolution | every agent whose model resolves via the default provider |
| `src/lib/generated/providerCatalog.ts` deleted | **HIGH** | `onboarding.tsx`, `ProvidersSection.tsx`, `ProviderPickerSheet.tsx`, `catalog_test.go::TestCatalog_EmbedMatchesGeneratedTS` (#13), `src/lib/constants.ts` key-hint map keyed by id | every vitest that imports `PROVIDER_CATALOG` |
| `pkg/auth/oauth.go::GoogleAntigravityOAuthConfig` | **LOW** | `antigravity_provider.go` only (deleted together) | — |
| `HandleProviders` DELETE branch (new) | **MEDIUM** | none (new) | `credentials.Store`, config writer, reload pipeline — new call pattern, seam tests required |

**HIGH-risk warning (flagged per skill rule):** the two enum changes and the generated-catalog deletion each fan out across Go, TS and tests. They are deliberately batched into the single "antigravity deletion + contract regeneration" commit (ADR §2.4) so the tree is never half-migrated.

### Relevant Execution Flows

| Flow (by symbol, graph unavailable) | Relevance |
|---|---|
| Onboarding complete: `POST /onboarding/complete` → probe → config write → `Agents.Defaults.ModelName` set (`gateway.go` guard) | Step 3 now also carries `auth_method`; the default model is the user's explicit pick. |
| Provider PUT: validate → `storeCredential(id+"_API_KEY")` → config write → `triggerReloadAndWaitOutcome` | DELETE is its inverse and must also wait for reload. |
| Agent turn model resolution: `pkg/agent/model_resolution.go::buildModelListResolver` → `instance.go` "provider %q not found in configured providers" | A dependent left without a model must refuse the turn with a typed error here, not crash at boot (ADR-067 MAJ-010). |
| Chat error surfacing: `LLMError` WS frame (`contracts/asyncapi.yaml`) | Carries the new "no model assigned" code. |

### Cluster Placement

Spans **providers/catalog** (Go), **gateway REST** (Go), **settings SPA** and **onboarding SPA**. Architectural note: the shared `ProviderPicker` is the one new SPA module used by two routes; it must live under `src/components/providers/` (neither `settings/` nor `routes/`) so neither surface imports the other.

---

## User Stories & Acceptance Criteria

Actors: **Operator** (the single human installing and administering Omnipus), **Agent** (an Omnipus agent running a turn), **Vendor** (the LLM provider's API or CLI).

### User Story 1 — Antigravity is gone without a trace (Priority: P0)

An operator must never be put at risk of a Google-account suspension by Omnipus. Today a fresh install's seeded default model is `antigravity/gemini-3-flash`, the very practice Google's Antigravity terms §6 name and enforce. The provider, its OAuth config, its contract enum value, its docs and its seed are removed in one commit, with no alias, shim, migration, retired-list row or boot notice naming it afterwards.

**Why this priority**: bears on the running release (ADR §9); it precedes shipping the ADR-066 branch.

**Independent Test**: on a clean checkout after the commit, the exit-proof grep returns only historical decision records, and a config naming `antigravity` fails on the generic unknown-provider path.

**Acceptance Scenarios**:

1. **Given** the deletion commit is applied, **When** `grep -ri antigravity pkg cmd src contracts config docs` runs, **Then** the only hits are the historical decision records enumerated in ADR §2.4 "Kept deliberately".
2. **Given** a `config.json` whose provider list names `antigravity`, **When** the gateway boots, **Then** the gateway starts, the provider row shows the generic unknown-provider state, and no log line, error string or UI text contains the word "antigravity".
3. **Given** the deletion commit, **When** `make verify-contracts`, `CGO_ENABLED=1 go build -tags goolm,stdjson ./...` and `npm run typecheck` run, **Then** all three exit 0.
4. **Given** a fresh install (no `config.json`), **When** onboarding reaches step 3, **Then** no model is pre-selected and no seeded default names a removed provider.

---

### User Story 2 — Subscription sign-in only where the vendor permits it (Priority: P0)

An operator who holds a vendor subscription wants to use it in Omnipus — but only where doing so cannot cost them the account. Omnipus offers a *Sign in* alternative to the API key for exactly the vendors whose own terms or official statements permit it (GitHub Copilot via the official SDK/CLI; OpenAI via ChatGPT login as two named providers), offers API key only for Anthropic and Google, gates xAI sign-in on xAI listing Omnipus, and removes `claude-cli`.

**Why this priority**: it is the policy the deletion in US-1 executes; shipping a disallowed path is the same class of risk.

**Independent Test**: the provider catalog served to the SPA declares `auth_methods` per provider; the picker renders Sign in only where `sign_in` is declared; Anthropic and Google rows have no sign-in control in the DOM.

**Acceptance Scenarios**:

1. **Given** the catalog, **When** the operator opens Anthropic or Google in the picker (onboarding or Settings), **Then** only the API-key path is presented and no sign-in control exists.
2. **Given** the catalog, **When** the operator opens OpenAI, **Then** two subscription providers are distinguishable: `codex-cli` (official CLI driven as a subprocess; the operator signs in inside the CLI; Omnipus never touches the token) and `openai-chatgpt` (direct HTTP with the CLI's saved token), and `openai-chatgpt` is labelled *"relies on OpenAI's stated tolerance, not its written terms"*.
3. **Given** the operator picks OpenAI sign-in and makes no further choice, **When** the configuration is saved, **Then** the persisted provider id is `codex-cli` (the subprocess is the default).
4. **Given** the catalog, **When** the operator opens GitHub Copilot, **Then** Sign in (official SDK/CLI path, billed to the subscription) and API key are both offered.
5. **Given** xAI has not listed Omnipus, **When** the operator opens xAI, **Then** only the API-key path is presented, with the copy *"Sign in with xAI arrives once xAI lists Omnipus"*.
6. **Given** the descoping commit, **When** an agent entity or config names `claude-cli`, **Then** it fails as an unknown provider with no mention of the name in the error.
7. **Given** a sign-in provider, **When** a sign-in completes, **Then** its row reads *"Signed in as `<account label>`"* and the row's action reads *Manage*, not *Edit*.
8. **Given** a signed-in provider whose vendor session has expired, **When** the provider list is loaded, **Then** the row state is *Session expired* and its action opens the re-sign-in dialog.

---

### User Story 3 — Remove a provider, with a guard (Priority: P0)

An operator who added OpenRouter to try it, and now uses Anthropic, cannot remove OpenRouter today — `GET /providers` reports it forever. They need a *Remove provider* action that deletes the configuration **and** its stored key, always confirms once, lists the agents that depend on it, requires a new default model inline when the provider backs the default, and has no Undo (an Undo would retain the deleted secret).

**Why this priority**: the capability gap and the secret-retention finding (pass-2 MAJ-011) are both release-grade.

**Independent Test**: `DELETE /providers/{id}` removes the row and the credential; a second `GET` of the credential store has no `<id>_API_KEY`; dependents are reported; deletion of the default-backing provider without a replacement is refused.

**Acceptance Scenarios**:

1. **Given** a configured provider with no dependents that does not back the default, **When** the operator clicks *Remove provider* in the config sheet footer, **Then** a confirm dialog reads *"Remove `<Display name>`? Its key will be deleted."* with *Cancel* and *Remove* and nothing else.
2. **Given** that dialog, **When** the operator confirms, **Then** the provider disappears from the list, its catalog entry is again selectable in the picker, and its stored key is gone from the credential store the moment the confirmation request succeeds.
3. **Given** a provider used by agents A and B as their primary model, **When** the operator opens *Remove provider*, **Then** the dialog lists A and B by name under *"These agents will be left without a model"*.
4. **Given** the dialog in (3), **When** the operator confirms, **Then** A and B show *"needs a model"* in the agent list, and a chat turn sent to A is refused with a typed error whose code is the one reserved for "no model assigned" — nothing is re-pointed silently.
5. **Given** the provider backs the default model, **When** the operator opens *Remove provider*, **Then** the dialog contains an inline *New default model* selector restricted to **other connected** providers, and *Remove* is disabled until a new default is chosen.
6. **Given** the dialog in (5) with a new default chosen, **When** the operator confirms, **Then** the default model changes to the new pair and the provider is removed, in that order, and the next turn of an agent using the default runs on the new pair without a restart.
7. **Given** any successful removal, **When** the operator looks for an Undo, **Then** there is none — no toast offers to restore, and no client or server state retains the key.
8. **Given** a removal request that names a provider not configured, **When** it is sent, **Then** the response is 404 and nothing changes.
9. **Given** the credential store is locked, **When** a removal is attempted, **Then** the response is 503 with the existing "credential store locked" message and the provider row is unchanged.

---

### User Story 4 — The default model is a Settings control (Priority: P0)

An operator wants to change which model new and default-routed agents use without re-running onboarding or editing JSON. A *Default model* card is the first element on Settings → Providers showing `provider · model · window · source`, with *Change* opening the model selector filtered to connected providers; the same control is reachable from the provider row. It is backed by a `PUT` and takes effect on the next turn without restart.

**Why this priority**: the only writer today is onboarding, guarded by `ModelName == ""`; US-3's inline re-pick needs this control to exist.

**Independent Test**: `PUT` the default to a different connected provider/model, send one chat turn to an agent with no model of its own, observe the turn's model in the session transcript.

**Acceptance Scenarios**:

1. **Given** Settings → Providers, **When** it renders, **Then** the first card is *Default model* showing the current provider display name, model, context window and window source (window and source per ADR-066 D9).
2. **Given** the card, **When** the operator clicks *Change*, **Then** the model selector opens listing only models of **connected** providers (status `connected` or `signed_in`).
3. **Given** a selection, **When** it is saved, **Then** the card updates, the provider row that backs the default shows a *Default* marker, and the next turn of a default-routed agent uses the new pair with no gateway restart.
4. **Given** a `PUT` naming a provider that is not configured, **When** it is sent, **Then** the response is 400 naming the field, and the default is unchanged.
5. **Given** a `PUT` naming a model the catalog does not list for that provider (and the provider is not `custom`/local), **When** it is sent, **Then** the response is 400, and the default is unchanged.
6. **Given** a provider row, **When** the operator opens its overflow/footer actions, **Then** *Set as default model…* is present and opens the same selector pre-filtered to that provider.

---

### User Story 5 — One shared provider picker that holds 190 providers (Priority: P1)

An operator choosing a provider — in onboarding step 3 or in Settings — sees the same picker: a stable band of 8 *Popular* tiles, then *Recently used*, then one search field over company / plan / region / alias, then *All providers* collapsed until searched or expanded — a virtualised, letter-grouped list. Unsupported (cloud-IAM) providers are visible but disabled with the reason; *Custom endpoint* is the permanent last row. Built on cmdk so arrow keys and typeahead work.

**Why this priority**: the UX review verdict is FAIL at 190 with the current flat grids; without it US-2/US-4 land on an unusable surface.

**Independent Test**: render the picker with a 190-entry catalog fixture; assert 8 Popular tiles, collapsed *All providers (N)*, that typing expands the list, that Bedrock is `aria-disabled` with a reason, and that the rendered row count in the expanded list is bounded (virtualised).

**Acceptance Scenarios**:

1. **Given** the catalog has ≥9 providers, **When** the picker opens, **Then** exactly 8 Popular tiles render in a fixed order, and below them the section *All providers (`<count>`)* is collapsed.
2. **Given** the picker is open and the search field is empty, **When** the operator types a query, **Then** the *All providers* list expands and shows only providers whose company, plan, region or alias matches, case-insensitively; with no match an empty state *"No provider matches `<query>`"* shows with *Custom endpoint* still available.
3. **Given** the picker is open, **When** the operator expands *All providers* without a query, **Then** rows are grouped under letter headers (A–Z, then `#`) and the DOM contains at most the visible window of rows plus overscan, not all ~190.
4. **Given** an unsupported provider (e.g. Amazon Bedrock), **When** it appears in the list, **Then** it is rendered `aria-disabled="true"` with its reason text (e.g. *"needs request signing"*), is not selectable, and is never hidden by default.
5. **Given** any state of the list, **When** the operator scrolls to the end, **Then** *Custom endpoint* is the last row.
6. **Given** the operator has previously configured Z.ai Coding Plan, **When** the picker opens, **Then** a *Recent* row for it appears between Popular and the search field.
7. **Given** focus is in the search field, **When** the operator presses ArrowDown / Enter, **Then** focus moves through tiles and rows and Enter selects, with no mouse.
8. **Given** a provider with plans and regions, **When** it is selected, **Then** the second-level panel shows plan and region as `aria-pressed` groups, region pre-selected from the browser locale with copy *"Detected: `<Region>` — change"*.
9. **Given** a provider with `sign_in` in its auth methods, **When** its second-level panel opens, **Then** a segmented control `[ Sign in with <Vendor> ] [ API key ]` appears with *Sign in* pre-selected; for providers without `sign_in` the control is absent.
10. **Given** onboarding, **When** step 3 renders, **Then** the progress tracker still shows three steps.

---

### User Story 6 — Model selection: no pre-selection, ordered, recommended, virtualised (Priority: P1)

An operator picking a model sees the catalog list ordered by vendor group then release date descending, with at most 3 models per provider carrying a *Recommended for chat* chip (tool-calling, ≥128k window) as a hint, not a selection. In onboarding the field is labelled *"Model for your first agent"* and nothing is pre-selected. Lists above 100 items are virtualised.

**Why this priority**: operator decision (no pre-selection); OpenRouter's 359 models make the un-virtualised list a measurable jank.

**Independent Test**: render the selector with a 359-model fixture; assert order, ≤3 chips per provider, bounded DOM rows, and that the submit button is disabled until a model is chosen.

**Acceptance Scenarios**:

1. **Given** a provider with models from several vendors, **When** the selector opens, **Then** models are grouped by vendor and within each group ordered by release date, newest first; models with no release date sort last within their group, alphabetically.
2. **Given** a provider's models, **When** the list renders, **Then** at most 3 models per provider carry a *Recommended for chat* chip, each of which supports tool calling and has a context window ≥128,000 tokens.
3. **Given** onboarding step 3 after the key/sign-in is validated, **When** the model field renders, **Then** its label is *"Model for your first agent"*, its value is empty, and *Finish* is disabled.
4. **Given** a provider with more than 100 models, **When** the selector opens, **Then** rendered option rows are bounded to the visible window plus overscan.
5. **Given** a provider with ≤100 models, **When** the selector opens, **Then** all rows are rendered (no virtualisation) and keyboard navigation is unchanged from today.

---

### User Story 7 — "Check with my account" and row-level limits (Priority: P2)

An operator wants to know which catalog models *their* key can actually use. The *Refresh models* action becomes *Check with my account*: it intersects the live `/models` response with the catalog, greys out models the key cannot reach, flags catalog-unknown models as *limits unknown*, and the result is cached per key. Each configured row shows, on expand, effective limits per model and the window's source (ADR-066 D9).

**Why this priority**: it preserves the one legitimate use of the live call after the selector moves to the catalog (ADR-067 §4.3); it is not blocking.

**Independent Test**: stub `/models` to return a subset plus one unknown id; assert the greyed set and the *limits unknown* flag.

**Acceptance Scenarios**:

1. **Given** a connected provider, **When** the operator clicks *Check with my account*, **Then** one live call is made with that provider's key, and models in the catalog but absent from the response are shown greyed with *"not available on this key"*.
2. **Given** the response includes a model id the catalog lacks, **When** the result renders, **Then** that model is listed with the flag *limits unknown*.
3. **Given** the live call fails, **When** the result renders, **Then** the catalog list is unchanged and an inline warning shows the upstream error; nothing is greyed.
4. **Given** a checked provider, **When** the row is expanded, **Then** each model shows window · output · image · PDF and the window's source label (one of override / live / catalog / learned / floor, per ADR-066 D9).

---

### User Story 8 — A typed key is never lost on sheet close (Priority: P1)

An operator who pastes a key and accidentally presses Esc or clicks the overlay loses the key today (`handleClose` clears the draft). The draft is kept until explicit Cancel; Esc/overlay with a dirty draft keeps the sheet open and asks *"Discard key?"*.

**Why this priority**: data-loss in a form is a hard UX defect; it is one of ADR §5's eight decisions.

**Independent Test**: vitest — type a key, fire Esc, assert the sheet is open and the inline prompt is present; click *Keep editing*, assert the value survives.

**Acceptance Scenarios**:

1. **Given** the config sheet with a non-empty, unsaved key, **When** the operator presses Esc or clicks the overlay, **Then** the sheet stays open and an inline prompt *"Discard key?"* with *Discard* and *Keep editing* appears.
2. **Given** the prompt, **When** *Keep editing* is chosen, **Then** the prompt closes and the key value is unchanged.
3. **Given** the prompt, **When** *Discard* is chosen, **Then** the sheet closes and the draft is cleared from memory.
4. **Given** the sheet with an empty or already-saved key, **When** Esc is pressed, **Then** the sheet closes immediately (no prompt).
5. **Given** the sheet, **When** *Cancel* is clicked, **Then** the sheet closes and the draft is cleared without a prompt.

---

### User Story 9 — Wire contract changes, contract-first (Priority: P0)

Every byte crossing the gateway/SPA boundary for this feature is defined in `contracts/` before any Go/TS code, and the SPA and gateway use only generated types (Constraint #8). The probe request's provider enum becomes a validated free string; the build-time TS catalog file is deleted and the SPA reads the providers-catalog GET.

**Why this priority**: Constraint #8 is non-negotiable and lint-enforced; a hand-written wire type fails CI.

**Independent Test**: `make verify-contracts` exits 0 on the feature branch; `grep -rn 'providerCatalog' src` is empty; `ProbeProviderRequest.id` has no `enum` key.

**Acceptance Scenarios**:

1. **Given** the feature branch, **When** `make verify-contracts` runs, **Then** it exits 0 and `git status` shows no generated-file drift.
2. **Given** a probe request whose `id` is not in the served catalog and carries no `endpoint`, **When** it is sent, **Then** the response is 400 with an error naming the field `id` and no list of accepted values is echoed.
3. **Given** a probe request whose `id` is in the catalog, **When** it is sent, **Then** it is processed exactly as today.
4. **Given** the SPA, **When** any picker or selector needs the provider list, **Then** it reads the providers-catalog GET (cached by the server's ETag/validator) and imports nothing from `src/lib/generated/providerCatalog.ts`.

---

## Behavioral Contract

Primary flows:
- When the operator opens a provider picker, the system shows 8 Popular tiles, Recent, search, a collapsed *All providers (N)* and *Custom endpoint* last.
- When a provider's catalog entry lists `sign_in`, the system offers Sign in (pre-selected) and API key; otherwise API key only.
- When the operator confirms removal of a provider, the system deletes the configuration and its stored key, lists and leaves dependents without a model, and offers no Undo.
- When the operator changes the default model, the system persists it and the next default-routed turn uses it without restart.
- When onboarding step 3 completes, the system has a probe-validated (provider, model) pair chosen explicitly by the operator.

Error flows:
- When a removal would orphan the default model and no replacement is supplied, the system refuses with 409 and changes nothing.
- When a removal names an unconfigured provider, the system returns 404.
- When the credential store is locked, the system refuses removal with 503.
- When an agent left without a model receives a turn, the system refuses it with the typed "no model assigned" error and does not fall back to another model.
- When a probe names an unknown provider id without an endpoint override, the system returns 400.
- When "Check with my account" cannot reach the vendor, the system keeps the catalog list and shows the error inline.

Boundary conditions:
- When the catalog has fewer than 8 Popular providers, the system renders those present, never pads.
- When a model list has exactly 100 items, the system renders all rows; at 101 it virtualises.
- When a provider has exactly 3 recommendable models, all three carry the chip; a fourth qualifying model does not.
- When the search query is whitespace only, the system treats it as empty (list stays collapsed).
- When the browser locale cannot be mapped to a region, the system pre-selects the provider's first region and the copy reads *"Region — change"* without "Detected".

---

## Edge Cases

- Removing the **only** configured provider: allowed; the default is necessarily backed by it, so the dialog cannot offer another connected provider — the dialog says *"This is your only provider. Removing it leaves every agent without a model."* and *Remove* is enabled (no inline re-pick possible); afterwards the Default model card reads *"No default model"* with *Choose* opening the picker. *(Assumption — see Ambiguity #4.)*
- Two providers configured from the **same company** (e.g. Z.ai Coding Plan and Pay-as-you-go): removal targets one variant id; the other row and its key are untouched.
- A provider whose key is referenced by `api_key_ref` but the credential is already missing from the store: removal succeeds (config row removed, `Delete` on a missing name is not an error).
- Concurrent removal and PUT on the same id: the second request observes the first's result under `configMu`; a PUT after DELETE re-creates the provider as new (api_key required).
- A dependent agent that is **locked core** (Mia/Jim/Ava/Ray): listed like any other; it, too, is left needing a model — core status does not exempt it.
- An agent whose **fallback** model points at the removed provider: the fallback entry is removed and the agent is listed under *"uses as fallback"* in the dialog *(Assumption — Ambiguity #3)*.
- Sign-in provider whose CLI binary is not on PATH: the sign-in panel shows *"`codex` not found on this machine"* with the install hint; status stays `disconnected`.
- Sign-in with the vendor CLI logged in under an account the operator cannot identify: `account_label` falls back to the vendor name (*"Signed in"*).
- `openai-chatgpt` token file older than the vendor's refresh window: status `expired`, row routes to re-sign-in, which instructs *"Run `codex login` again"*.
- Search query containing regex metacharacters (`(`, `[`, `*`): treated literally.
- Search query in CJK for a Chinese vendor's alias (e.g. 智谱): matches via the catalog's alias list when present.
- Locale `zh-CN` → region `china`; `en-US` → `us` where the provider has a US region, else `intl`; any other → `intl`.
- Catalog GET unavailable (offline first boot, no embedded snapshot served): picker shows an error state with *Retry* and *Custom endpoint*; onboarding is not blocked from the custom path.
- Virtualised list and screen readers: the listbox exposes `aria-setsize`/`aria-posinset` per row so position is announced despite partial rendering.
- Deleting a provider while "Check with my account" for it is in flight: the in-flight result is discarded on arrival (row no longer exists).

---

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not offer a subscription sign-in for Anthropic or Google in any surface, because both vendors prohibit it in their terms and Google enforces with account suspension.
- The system must not collect, store, proxy or refresh a vendor's consumer credential where the vendor prohibits it (D13 rule 4).
- The system must not ship xAI sign-in until xAI lists Omnipus, because the AUP bans unlisted bots; no tolerance-based fallback flow.
- The system must not provide an Undo for provider removal, because an Undo retains the deleted secret for its duration.
- The system must not re-point a dependent agent to another model on provider removal, because silent re-pointing hides a change the operator did not make.
- The system must not pre-select a model in onboarding, because the operator decided the user picks.
- The system must not hide unsupported providers, because hidden options generate "where is X?" tickets; they are shown disabled with the reason.
- The system must not keep any alias, shim, migration, retired-name list or boot notice for `antigravity` or `claude-cli`, because greenfield means no trace.
- The system must not bake the provider catalog into the SPA bundle, because the catalog refreshes daily.
- The system must not hand-write any wire-format struct/interface for the new routes (Constraint #8, lint-caught).
- The system must not poll any vendor's `/models` on a timer; the live call is on explicit operator action only.
- The system must not introduce a new Go runtime dependency for sign-in (Constraint #1); the vendor CLI is an external binary the operator installs, like `codex` today.
- The system must not render emoji in any UI chrome introduced here (Phosphor icons only).
- The system must not show raw cron or any scheduling surface here (unrelated retired surfaces stay retired).

### Machine-Verifiable Constraints

**Error Codes / Messages (REST)**:
- `DELETE /api/v1/providers/{id}` on an unconfigured id → HTTP **404**, body `{"error":"provider not configured"}`.
- `DELETE /api/v1/providers/{id}` where `backs_default=true` and the body carries no valid `new_default` → HTTP **409**, body `{"error":"provider backs the default model; supply new_default"}`; no change persisted.
- `DELETE /api/v1/providers/{id}` with `new_default.provider == id` → HTTP **400**, body `{"error":"new_default must name a different, connected provider"}`.
- `DELETE` while the credential store is locked → HTTP **503**, body `{"error":"credential store locked: set OMNIPUS_MASTER_KEY or unlock before saving secrets"}` (existing message, verbatim).
- `DELETE` with dev-mode bypass active → HTTP **503** via `RequireNotBypass` (high-blast-radius admin route).
- `PUT /api/v1/providers/default-model` with an unconfigured provider → HTTP **400**, body `{"error":"provider not configured","field":"provider"}`.
- `PUT /api/v1/providers/default-model` with a model not in the served catalog for that provider (provider not `custom`/`ollama`/`vllm`) → HTTP **400**, `{"error":"model not in catalog for provider","field":"model"}`.
- `PUT /api/v1/providers/default-model` when reload does not confirm → HTTP **500**, `{"error":"default model saved but config reload failed: <reason>"}`.
- `POST /onboarding/probe-provider` with `id` not in the catalog and no `endpoint` → HTTP **400**, `{"error":"unknown provider","field":"id"}`; the body must not enumerate accepted ids.
- `POST /onboarding/complete` with `auth_method: "api_key"` and no `api_key` → HTTP **400** naming `api_key`; with `auth_method: "sign_in"` and an `api_key` present → HTTP **400** (`"api_key not allowed with sign_in"`).
- `POST /api/v1/providers/{id}/sign-in` on a provider whose catalog `auth_methods` lacks `sign_in` → HTTP **400**, `{"error":"provider does not support sign-in"}`.

**WS / LLMError**:
- A turn sent to an agent with no model assigned emits `LLMError` with `code: "model_unassigned"`, `attribution: "config"`, message `"This agent has no model. Pick one in the agent's settings."` and the turn ends with no provider call.

**Performance Bounds**:
- Picker open → first paint ≤ 100 ms at p95 with a 190-entry catalog on the Playwright CI runner (measured via `performance.mark`).
- Expanded *All providers* list: rendered row elements ≤ (visible rows + 2 × overscan); overscan = 5.
- Model selector with 359 models: rendered option elements ≤ (visible + 10); scroll frame time ≤ 16.7 ms at p95 in the vitest jsdom benchmark is **not** asserted (jsdom cannot); asserted in Playwright via `requestAnimationFrame` deltas ≤ 33 ms at p95.
- `DELETE /providers/{id}` p95 ≤ reload-wait bound already used by PUT (the poll window of `triggerReloadAndWaitOutcome`) + 200 ms.
- Providers-catalog GET cached client-side; at most **one** network fetch per SPA session unless the validator changes (TanStack Query `staleTime` = session).

**Data Constraints**:
- `ProbeProviderRequest.id`: string, `minLength 1`, `maxLength 64`, pattern `^[a-z0-9][a-z0-9._-]*$`, no `enum`.
- `Provider.status` enum: exactly `connected | disconnected | error | signed_in | expired`.
- `Provider.auth_method` enum: `api_key | sign_in`; `Provider.account_label`: string, `maxLength 128`, present only when `status = signed_in | expired`.
- `Provider.dependents`: array of `{id: string, name: string, role: "primary" | "fallback"}`, always present (empty array when none); `Provider.backs_default`: boolean, always present.
- `ProviderDeleteRequest.new_default`: optional object `{provider: string(1..64), model: string(1..256)}`.
- `ProviderDeleteResponse`: `{deleted: true, dependents: [...], default_changed: boolean, new_default?: {provider, model}}`.
- `DefaultModel` (GET/PUT body): `{provider: string, model: string, context_window?: integer ≥ 0, window_source?: "override"|"live"|"catalog"|"learned"|"floor"}`; `PUT` accepts only `{provider, model}` (`additionalProperties: false`).
- `OnboardingCompleteRequest.provider.auth_method`: enum `api_key | sign_in`, **required**; `api_key` required iff `auth_method = api_key`.
- `SignInStartResponse`: `{method: "cli_login" | "device_code", instructions: string, command?: string, user_code?: string, verification_uri?: string, expires_at?: date-time}`.
- `SignInStatus`: `{state: "not_signed_in" | "pending" | "signed_in" | "expired", account_label?: string, expires_at?: date-time}`.
- Popular set: exactly these 8 ids in this order: `openai, anthropic, openrouter, google, xai, groq, mistral, deepseek` (ADR-067 §4.2; pass-2 MIN-008 pins it at 8).
- Recommended chip: ≤ 3 per provider; eligibility = `tool_calling = true AND context_window ≥ 128000`; tie-break = release date desc, then id asc.
- Region inference map: `zh-*` → `china`; `en-US` → `us` if offered else `intl`; everything else → `intl`.
- Persisted provider ids for the OpenAI subscription paths: exactly `codex-cli` (subprocess) and `openai-chatgpt` (HTTP); no other spelling is accepted anywhere (`codexcli` removed).
- Credential name deleted on removal: exactly `<providerID>_API_KEY` (the name `storeCredential` writes today).

**Accessibility (WCAG 2.2 AA, machine-checked by `tests/e2e/accessibility.spec.ts` axe run + targeted assertions)**:
- Every interactive element in the picker, dialog and sheet is reachable and operable by keyboard (2.1.1); the cmdk list supports ArrowUp/Down/Home/End/Enter/Esc.
- Focus is visible on every tile, row and button with a ≥ 3:1 contrast focus ring (2.4.7, 2.4.11 non-obscured).
- Target size ≥ 24 × 24 CSS px for every tile, row, segmented-control option and footer button (2.5.8).
- Text contrast ≥ 4.5:1 for body text and ≥ 3:1 for large text/UI components in dark and light themes (1.4.3, 1.4.11) — includes the greyed "not available on this key" rows (greyed text still ≥ 4.5:1; disabled rows are exempt under 1.4.3 but carry the reason in an adjacent non-disabled text node).
- Confirm dialog: `role="alertdialog"`, `aria-labelledby` the title, `aria-describedby` the consequence sentence; focus lands on *Cancel* on open; Esc = Cancel (2.4.3, 3.2.2).
- Probe/finish/deletion errors render in a `role="alert"` region with `aria-live="assertive"` (4.1.3).
- Segmented control and plan/region groups use `role="group"` + `aria-label` and `aria-pressed` on options (4.1.2).
- Virtualised listboxes set `aria-setsize` and `aria-posinset` on each rendered option (4.1.2).
- No information conveyed by colour alone: row states carry an icon + text, the Recommended chip has text (1.4.1).
- The "Discard key?" prompt does not move focus out of the sheet (3.2.1).
- axe-core run on onboarding step 3, Settings → Providers (with sheet and dialog open) reports **0** violations of impact `serious` or `critical`.

### Conservative Type Design

No new nominal Go types beyond the generated wire types. Provider ids, model ids and credential names stay `string`; auth method and status stay the generated enum types. The SPA adds one TS discriminated union for picker items (`tile | recent | row | custom`) because rendering differs per kind; nothing else.

---

## Prerequisites

- **Hardware / OS**: any platform Omnipus builds for; sign-in providers additionally need the vendor CLI binary for that OS.
- **Required runtimes**: Go 1.26.4 (go.mod), Node 20+; `codex` CLI on PATH for `codex-cli`/`openai-chatgpt`; GitHub Copilot CLI on PATH for `github-copilot` *(id per Ambiguity #7)*.
- **Required services**: none beyond the gateway.
- **Network assumptions**: outbound HTTPS to the chosen vendor for probe/"Check with my account"; the providers-catalog GET is served by the gateway from ADR-067's embedded/pulled snapshot, so the picker works offline.
- **Accounts / credentials**: an API key or a vendor subscription the operator already holds; credential store unlocked (ADR-004).

## Development Setup

1. `cd /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-context-budget`
2. `export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH && npm ci`
3. `make gen-contracts` after every `contracts/` edit; commit spec + generated artefacts atomically.
4. `npm run build && rm -rf pkg/gateway/spa && cp -r dist/spa/* pkg/gateway/spa/ && CGO_ENABLED=0 go build -tags goolm,stdjson -o /tmp/omnipus ./cmd/omnipus/`
5. `export OMNIPUS_HOME=/tmp/omnipus-e2e-test && rm -rf "$OMNIPUS_HOME" && mkdir -p "$OMNIPUS_HOME" && OMNIPUS_BEARER_TOKEN="" /tmp/omnipus gateway --allow-empty`

**Expected first-run behaviour**: onboarding step 3 shows the shared picker with 8 Popular tiles and no pre-selected model.

**Common first-run failures**: port 5000 in use (set `gateway.port`); building without `-tags goolm,stdjson` fails on `pkg/channels/matrix` (missing tag, not a bug); stale `pkg/gateway/spa/` serves the old picker.

## Tech Stack

| Category | Choice | Version / Pin | Source |
|---|---|---|---|
| Language | Go | 1.26.4 (go.mod) | CLAUDE.md Tech Stack |
| Frontend | TypeScript, React 19, Vite 6, shadcn/Radix, Tailwind v4 | package.json | CLAUDE.md |
| Picker primitive | cmdk `Command` | `^1.1.1` | package.json L90 |
| Virtualisation | `@tanstack/react-virtual` | `^3.14.6` | package.json L85 (already a dependency) |
| Server state | TanStack Query | package.json | CLAUDE.md |
| Icons | Phosphor | package.json | CLAUDE.md UI rules |
| Datastore | `config.json` + `credentials.json` (AES-256-GCM) | — | ADR-004 |
| Contracts | OpenAPI 3 + AsyncAPI, oapi-codegen, openapi-typescript, openapi-zod-client | `scripts/gen-contracts.sh` | Constraint #8 |
| Tests | Go `go test -tags goolm,stdjson`, vitest, Playwright `^1.61.1`, axe-core | package.json | Quality Gates |

## Deployment / Runtime

- **Target environment**: single Omnipus binary, operator's machine or VPS.
- **Online / offline**: picker and selector are offline-capable (catalog served locally); probe, sign-in check and "Check with my account" need the vendor reachable.
- **Resource limits**: no new background goroutines; no timer-driven vendor calls.
- **Start / stop**: unchanged (`omnipus gateway`).
- **Health check**: `GET /api/v1/providers` lists rows with the new status vocabulary; `GET /api/v1/providers/default-model` returns the current pair.
- **Logs**: deletion logs one INFO line `rest: provider removed` with `provider_id`, `dependents` count and `default_changed`; never the key.

---

## Integration Boundaries

### Vendor API (API-key providers)

- **Data in**: probe request (one small completion), `/models` on "Check with my account".
- **Data out**: probe outcome (`valid | invalid_key | no_credit | unreachable | restricted`), model id list.
- **Contract**: OpenAI-compatible HTTP or Anthropic Messages, per the catalog protocol (ADR-067 §4.1).
- **On failure**: probe → existing `ProviderValidation` outcomes; "Check with my account" → inline warning, catalog list unchanged.
- **Development**: mocked with `httptest` servers in Go tests and MSW/`vi.mock` in vitest; Playwright uses the existing stub provider fixtures.

### Vendor CLI (`codex`, Copilot CLI)

- **Data in**: subprocess invocation with the prompt (`codex-cli`), or a read of the CLI's saved credential file (`openai-chatgpt`).
- **Data out**: completion text / JSONL events; for status, presence and age of the saved login.
- **Contract**: the vendor's own CLI interface; Omnipus never writes the credential file.
- **On failure**: binary missing → `disconnected` with hint; login missing/expired → `not_signed_in`/`expired`; subprocess error → existing `codex cli error: …` path surfaced as `LLMError provider_rejected`.
- **Development**: a fake `codex` shell script on PATH in Go integration tests (the pattern `codex_cli_provider_integration_test.go` already uses); SPA mocks the sign-in REST routes.

### Providers catalog (ADR-067)

- **Data in**: none from this feature.
- **Data out**: the catalog document via the providers-catalog GET.
- **Contract**: ADR-067 §5 schema. **Fields this spec consumes and therefore requires** (to be confirmed against ADR-067's schema — Ambiguity #1): per provider `id, display_name, company, tier (popular|standard|unsupported), unsupported_reason?, auth_methods[], plans[], regions[], protocol, aliases[]`; per model `id, display_name, release_date?, tool_calling, context_window, modalities`.
- **On failure**: SPA shows picker error state with Retry and Custom endpoint; server already falls back to the embedded snapshot.
- **Development**: a 190-entry JSON fixture under `src/test/fixtures/providers-catalog.json` generated once from the real snapshot.

### Credential store (`pkg/credentials`)

- **Data in**: `Delete("<id>_API_KEY")` on confirmed removal.
- **Data out**: nil or locked-store error.
- **On failure**: locked → 503 before any config change; missing name → treated as success.
- **Development**: real store with a temp `OMNIPUS_HOME` and `OMNIPUS_MASTER_KEY` in Go tests.

---

## BDD Scenarios

### Feature: ADR-068 — providers policy, deletion, default model, picker

#### Background

- **Given** the gateway is running with an unlocked credential store
- **And** the providers-catalog GET serves the 190-entry fixture with the 8 Popular ids pinned

---

#### Scenario: Antigravity leaves no trace in the source tree

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the deletion commit is checked out
- **When** `grep -ril antigravity pkg cmd src contracts config docs` is run
- **Then** every path returned is one of the historical decision records listed in ADR-068 §2.4 "Kept deliberately"
- **And** `docs/ANTIGRAVITY_USAGE.md` does not exist

#### Scenario: Config naming a removed provider fails generically

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Error Path

- **Given** `config.json` contains a provider entry with `provider: "antigravity"`
- **When** the gateway boots
- **Then** the gateway reaches the listening state
- **And** `GET /api/v1/providers` shows that row with the generic unknown-provider state
- **And** no log line and no response body contains the string `antigravity`

#### Scenario: Build and contract gates pass with the files gone

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the deletion commit
- **When** `make verify-contracts`, the tagged Go build and `npm run typecheck` are run in sequence with exit codes captured without a pipe
- **Then** each reports exit 0

#### Scenario: Fresh install seeds no default model

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Edge Case

- **Given** an empty `OMNIPUS_HOME`
- **When** the gateway boots and `GET /api/v1/providers/default-model` is called after onboarding step 2
- **Then** the response has empty `provider` and `model`
- **And** onboarding step 3 renders the model field empty

---

#### Scenario Outline: Auth methods offered per provider

**Traces to**: User Story 2, Acceptance Scenarios 1, 4, 5
**Category**: Happy Path

- **Given** the picker is open
- **When** the operator selects `<provider>`
- **Then** the second-level panel shows `<controls>`
- **And** the copy `<copy>` is `<present>`

**Examples**:

| provider | controls | copy | present |
|---|---|---|---|
| anthropic | API key field only | Sign in | absent |
| google | API key field only | Sign in | absent |
| openai | segmented `[Sign in with OpenAI][API key]`, Sign in selected | relies on OpenAI's stated tolerance | present on the `openai-chatgpt` option only |
| github-copilot | segmented `[Sign in with GitHub][API key]`, Sign in selected | billed to your Copilot subscription | present |
| xai | API key field only | Sign in with xAI arrives once xAI lists Omnipus | present |

#### Scenario: OpenAI sign-in offers two named providers with the subprocess as default

**Traces to**: User Story 2, Acceptance Scenarios 2, 3
**Category**: Happy Path

- **Given** the operator selected OpenAI and the *Sign in* segment
- **When** the panel renders
- **Then** two radio options are shown: *Codex CLI (official CLI, signs in on this machine)* with value `codex-cli`, pre-selected, and *ChatGPT login (direct)* with value `openai-chatgpt` carrying the label *"relies on OpenAI's stated tolerance, not its written terms"*
- **And** saving without changing the radio persists provider id `codex-cli`

#### Scenario: `claude-cli` is an unknown provider

**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Error Path

- **Given** an agent entity with `provider: "claude-cli"`
- **When** a turn is sent to that agent
- **Then** the turn is refused with a typed config error
- **And** neither the error message nor any log line contains `claude-cli` or `claude`

#### Scenario: Signed-in row shows account and Manage

**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Happy Path

- **Given** `GET /api/v1/providers` returns `codex-cli` with `status: signed_in` and `account_label: "dev@example.com"`
- **When** Settings → Providers renders
- **Then** the row reads *Signed in as dev@example.com*
- **And** the row action button text is *Manage*

#### Scenario: Expired session routes to re-sign-in

**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Error Path

- **Given** `GET /api/v1/providers` returns `openai-chatgpt` with `status: expired`
- **When** the operator clicks the row action
- **Then** the re-sign-in dialog opens with the instruction *"Run `codex login` again, then check"*
- **And** pressing *Check* calls `GET /api/v1/providers/openai-chatgpt/sign-in/status`

#### Scenario: Sign-in refused for a key-only provider

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Error Path

- **Given** the catalog lists `anthropic` with `auth_methods: [api_key]`
- **When** `POST /api/v1/providers/anthropic/sign-in` is sent
- **Then** the response is HTTP 400 with `{"error":"provider does not support sign-in"}`

---

#### Scenario: Remove an unused provider after one confirmation

**Traces to**: User Story 3, Acceptance Scenarios 1, 2
**Category**: Happy Path

- **Given** `openrouter` is configured, has `dependents: []` and `backs_default: false`
- **When** the operator clicks *Remove provider* and then *Remove* in the dialog titled *"Remove OpenRouter? Its key will be deleted."*
- **Then** `DELETE /api/v1/providers/openrouter` returns HTTP 200 with `{"deleted":true,"dependents":[],"default_changed":false}`
- **And** the row is gone from the list
- **And** the credential store has no entry `openrouter_API_KEY`
- **And** OpenRouter is selectable again in the picker

#### Scenario: Dependents are listed and left without a model

**Traces to**: User Story 3, Acceptance Scenarios 3, 4
**Category**: Alternate Path

- **Given** agents "Ava" and "Scout" have `provider: "openrouter"` as primary
- **When** the operator opens *Remove provider* for OpenRouter
- **Then** the dialog lists *Ava* and *Scout* under *"These agents will be left without a model"*
- **And** after confirming, `GET /api/v1/agents` shows both with `needs_model: true`
- **And** the agent list renders *needs a model* on both rows

#### Scenario: Turn to an agent without a model is refused, not re-pointed

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Error Path

- **Given** agent "Ava" has `needs_model: true`
- **When** a chat message is sent to Ava
- **Then** an `LLMError` frame with `code: "model_unassigned"` and `attribution: "config"` is emitted
- **And** no request reaches any provider
- **And** Ava's stored model is unchanged (still empty)

#### Scenario: Default-backing provider requires an inline new default

**Traces to**: User Story 3, Acceptance Scenarios 5, 6
**Category**: Alternate Path

- **Given** the default model is `openrouter · z-ai/glm-5.2` and `anthropic` is also connected
- **When** the operator opens *Remove provider* for OpenRouter
- **Then** the dialog contains a *New default model* selector listing only Anthropic models
- **And** *Remove* is disabled until a model is chosen
- **And** after choosing `anthropic · claude-sonnet-4.6` and confirming, `GET /api/v1/providers/default-model` returns that pair and OpenRouter is gone

#### Scenario: DELETE without replacement on the default-backing provider is refused

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path

- **Given** `openrouter` has `backs_default: true`
- **When** `DELETE /api/v1/providers/openrouter` is sent with an empty body
- **Then** the response is HTTP 409 `{"error":"provider backs the default model; supply new_default"}`
- **And** the provider, its key and the default are unchanged

#### Scenario: No Undo exists after removal

**Traces to**: User Story 3, Acceptance Scenario 7
**Category**: Edge Case

- **Given** a provider was just removed
- **When** the SPA state and the DOM are inspected for 10 seconds
- **Then** no element with text *Undo* is rendered
- **And** the SPA holds no variable containing the removed key (keys are write-only and were never returned)

#### Scenario: Removing an unconfigured provider returns 404

**Traces to**: User Story 3, Acceptance Scenario 8
**Category**: Error Path

- **Given** no provider `groq` is configured
- **When** `DELETE /api/v1/providers/groq` is sent
- **Then** the response is HTTP 404 `{"error":"provider not configured"}`

#### Scenario: Removal refused while the credential store is locked

**Traces to**: User Story 3, Acceptance Scenario 9
**Category**: Error Path

- **Given** the credential store is locked
- **When** `DELETE /api/v1/providers/openrouter` is sent
- **Then** the response is HTTP 503 with the existing "credential store locked" message
- **And** `config.json` is byte-identical to before

#### Scenario: Removing the only provider

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Edge Case

- **Given** exactly one provider is configured and it backs the default
- **When** the operator opens *Remove provider*
- **Then** the dialog reads *"This is your only provider. Removing it leaves every agent without a model."* and *Remove* is enabled
- **And** after confirming, the Default model card reads *No default model* with a *Choose* action

#### Scenario: Fallback references are removed and listed

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Edge Case

- **Given** agent "Jim" lists an OpenRouter model only in `fallback_models`
- **When** the operator opens *Remove provider* for OpenRouter
- **Then** Jim is listed under *"uses as fallback"*
- **And** after confirming, Jim's `fallback_models` no longer contains that entry and `needs_model` is false

---

#### Scenario: Default model card shows provider, model, window, source

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `GET /api/v1/providers/default-model` returns `{provider:"openrouter", model:"z-ai/glm-5.2", context_window:1048576, window_source:"catalog"}`
- **When** Settings → Providers renders
- **Then** the first card is titled *Default model* and reads *OpenRouter · z-ai/glm-5.2* and *1,048,576 · catalog*
- **And** the OpenRouter row carries a *Default* marker

#### Scenario: Change default model takes effect on the next turn

**Traces to**: User Story 4, Acceptance Scenarios 2, 3
**Category**: Happy Path

- **Given** `anthropic` and `openrouter` are connected and the default is OpenRouter
- **When** the operator clicks *Change*, picks `anthropic · claude-sonnet-4.6` and saves
- **Then** `PUT /api/v1/providers/default-model` returns HTTP 200 with the new pair
- **And** the selector that opened listed only connected providers' models
- **And** a turn sent to a default-routed agent without restarting the gateway records `anthropic`/`claude-sonnet-4.6` in its session transcript

#### Scenario Outline: Default-model PUT validation

**Traces to**: User Story 4, Acceptance Scenarios 4, 5
**Category**: Error Path

- **Given** providers `openrouter` (connected) and `custom` (connected, user slug list `["my/llama"]`)
- **When** `PUT /api/v1/providers/default-model` is sent with `<body>`
- **Then** the response is `<status>` and the default is `<default_after>`

**Examples**:

| body | status | default_after |
|---|---|---|
| `{"provider":"groq","model":"llama-3.3-70b"}` | 400 field=provider | unchanged |
| `{"provider":"openrouter","model":"not/a-model"}` | 400 field=model | unchanged |
| `{"provider":"custom","model":"my/llama"}` | 200 | custom · my/llama |
| `{"provider":"","model":"x"}` | 400 field=provider | unchanged |
| `{"provider":"openrouter","model":""}` | 400 field=model | unchanged |
| `{"provider":"openrouter","model":"z-ai/glm-5.2","extra":1}` | 400 | unchanged |

#### Scenario: Set as default from the provider row

**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** the Anthropic row in Settings → Providers
- **When** the operator opens the row's actions and chooses *Set as default model…*
- **Then** the selector opens pre-filtered to Anthropic models
- **And** confirming performs the same `PUT` as the card

---

#### Scenario: Picker opens with 8 Popular tiles and a collapsed list

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the picker is opened from onboarding step 3
- **When** it renders
- **Then** exactly 8 elements with `data-testid^="picker-popular-"` exist in the order openai, anthropic, openrouter, google, xai, groq, mistral, deepseek
- **And** an element `data-testid="picker-all-toggle"` reads *All providers (182)* and `aria-expanded="false"`

#### Scenario: Search expands and filters the full list

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the picker is open with the list collapsed
- **When** the operator types `coding plan`
- **Then** the list expands and every rendered row's company, plan, region or alias contains `coding plan` case-insensitively
- **And** typing `zzzz` instead shows *No provider matches zzzz* with the *Custom endpoint* row still present

#### Scenario: Expanded list is letter-grouped and virtualised

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the picker is open
- **When** the operator activates *All providers (182)*
- **Then** letter headers appear in A→Z order followed by `#`
- **And** the count of elements `[role="option"]` in the DOM is ≤ visible rows + 10
- **And** each rendered option has `aria-setsize="182"` and a distinct `aria-posinset`

#### Scenario: Unsupported provider is visible, disabled, with reason

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Edge Case

- **Given** the expanded list
- **When** the row for Amazon Bedrock renders
- **Then** it has `aria-disabled="true"` and the text *needs request signing*
- **And** activating it changes nothing

#### Scenario: Custom endpoint is last

**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Edge Case

- **Given** the expanded list, any query
- **When** the operator presses End
- **Then** the focused/last row is *Custom endpoint*

#### Scenario: Recently used row appears

**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** `zai-coding-plan` is already configured
- **When** the Settings picker opens
- **Then** a section *Recent* lists *Z.ai Coding Plan* between the Popular band and the search field

#### Scenario: Keyboard-only selection

**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Happy Path

- **Given** focus is in the search field
- **When** the operator presses ArrowDown three times and Enter
- **Then** the third Popular tile (OpenRouter) is selected and the second-level panel opens
- **And** no pointer event was dispatched

#### Scenario Outline: Region inferred from locale

**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Alternate Path

- **Given** `navigator.language` is `<locale>` and the operator selects a provider with regions `<regions>`
- **When** the second-level panel opens
- **Then** region `<selected>` is `aria-pressed="true"` and the copy reads `<copy>`

**Examples**:

| locale | regions | selected | copy |
|---|---|---|---|
| zh-CN | intl, china | china | Detected: China — change |
| en-US | intl, us | us | Detected: US — change |
| en-US | intl, china | intl | Detected: International — change |
| de-DE | intl, china | intl | Detected: International — change |
| (empty) | intl, china | intl | Region — change |

#### Scenario: Auth-method control keeps onboarding at three steps

**Traces to**: User Story 5, Acceptance Scenarios 9, 10
**Category**: Happy Path

- **Given** onboarding step 3 with OpenAI selected
- **When** the segmented control renders
- **Then** the step tracker still shows exactly 3 steps
- **And** switching the segment to *API key* reveals the key field and hides the sign-in radios, without navigation

#### Scenario: Catalog unavailable in the picker

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Error Path

- **Given** the providers-catalog GET returns HTTP 500
- **When** the picker opens
- **Then** an error state with *Retry* is shown and the *Custom endpoint* row is still selectable
- **And** onboarding can proceed via Custom endpoint

---

#### Scenario: Models ordered by vendor then release date

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** OpenRouter's catalog models include `anthropic/claude-sonnet-4.6` (2026-02), `anthropic/claude-3.5-haiku` (2024-10), `openai/gpt-5.4` (2026-03), `x/nodate` (no date)
- **When** the selector opens
- **Then** the group order is by vendor group and within Anthropic the order is claude-sonnet-4.6 then claude-3.5-haiku
- **And** `x/nodate` is the last row of its group

#### Scenario: At most three Recommended chips per provider

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a provider with 5 models that support tool calling with windows 200k, 200k, 128k, 128k, 127,999
- **When** the selector renders
- **Then** exactly 3 rows carry *Recommended for chat*
- **And** the 127,999-window model does not

#### Scenario: Onboarding model field is empty and labelled

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Happy Path

- **Given** onboarding step 3 after a valid probe
- **When** the model field renders
- **Then** its accessible label is *Model for your first agent*
- **And** it has no value and *Finish* is disabled
- **And** a chip row may show Recommended models but none is selected

#### Scenario Outline: Model selector virtualisation threshold

**Traces to**: User Story 6, Acceptance Scenarios 4, 5
**Category**: Edge Case

- **Given** a provider with `<n>` models
- **When** the selector opens
- **Then** the number of rendered option rows is `<rendered>`

**Examples**:

| n | rendered |
|---|---|
| 0 | 0 (empty state "No models") |
| 1 | 1 |
| 100 | 100 |
| 101 | ≤ visible + 10 |
| 359 | ≤ visible + 10 |

---

#### Scenario: Check with my account greys unavailable models

**Traces to**: User Story 7, Acceptance Scenarios 1, 2
**Category**: Happy Path

- **Given** the catalog lists models A, B, C for `openai` and the stubbed `/models` returns A, C, Z
- **When** the operator clicks *Check with my account*
- **Then** B is greyed with *not available on this key*
- **And** Z is listed with *limits unknown*
- **And** exactly one upstream request was made

#### Scenario: Check with my account upstream failure

**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Error Path

- **Given** the stubbed `/models` returns HTTP 429
- **When** the operator clicks *Check with my account*
- **Then** an inline warning shows *could not fetch upstream model list: status 429*
- **And** no model is greyed

#### Scenario: Row expand shows limits and window source

**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a connected provider row
- **When** it is expanded
- **Then** each model line shows window · output · image · PDF values and a source label from {override, live, catalog, learned, floor}

---

#### Scenario: Esc with a dirty key keeps the sheet open

**Traces to**: User Story 8, Acceptance Scenarios 1, 2
**Category**: Happy Path

- **Given** the config sheet with `sk-test-123` typed and unsaved
- **When** Esc is pressed
- **Then** the sheet remains open and *Discard key?* with *Discard* / *Keep editing* is shown
- **And** choosing *Keep editing* leaves the field value `sk-test-123`

#### Scenario: Discard clears the draft

**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** the *Discard key?* prompt
- **When** *Discard* is chosen
- **Then** the sheet closes
- **And** reopening it shows an empty key field

#### Scenario Outline: Close behaviour by draft state

**Traces to**: User Story 8, Acceptance Scenarios 4, 5
**Category**: Edge Case

- **Given** the sheet with key field `<value>` and saved state `<saved>`
- **When** `<action>`
- **Then** `<result>`

**Examples**:

| value | saved | action | result |
|---|---|---|---|
| "" | n/a | Esc | closes, no prompt |
| "   " | n/a | Esc | closes, no prompt (whitespace = empty) |
| "sk-x" | saved | Esc | closes, no prompt |
| "sk-x" | unsaved | overlay click | stays open, prompt |
| "sk-x" | unsaved | Cancel button | closes, no prompt, draft cleared |

---

#### Scenario: Contracts regenerate cleanly

**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the feature branch
- **When** `make verify-contracts` runs
- **Then** it exits 0
- **And** `ProbeProviderRequest.yaml` has no `enum` under `id`

#### Scenario Outline: Probe provider id validation

**Traces to**: User Story 9, Acceptance Scenarios 2, 3
**Category**: Error Path

- **Given** onboarding is not complete
- **When** `POST /onboarding/probe-provider` is sent with id `<id>` and endpoint `<endpoint>`
- **Then** the response is `<status>`

**Examples**:

| id | endpoint | status |
|---|---|---|
| openrouter | — | 200 (probe runs) |
| not-a-provider | — | 400 field=id, body lacks any id list |
| not-a-provider | https://gw.example/v1 | 200 (custom path) |
| "" | — | 400 field=id |
| OPENROUTER | — | 400 field=id (pattern) |
| a×65 | — | 400 field=id (maxLength) |
| antigravity | — | 400 field=id, body does not contain "antigravity" beyond echoing nothing |

#### Scenario: SPA reads the catalog from the GET, not a bundle

**Traces to**: User Story 9, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the built SPA
- **When** `grep -rn "generated/providerCatalog" src` runs and Settings → Providers is opened in Playwright
- **Then** the grep is empty
- **And** exactly one request to the providers-catalog GET is observed for the session

#### Scenario: Onboarding complete with sign-in

**Traces to**: User Story 9, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** step 3 with `codex-cli` signed in
- **When** `POST /onboarding/complete` is sent with `provider.auth_method: "sign_in"` and no `api_key`
- **Then** the response is 200 and the persisted provider row has `auth_method: sign_in`
- **And** sending it with an `api_key` as well returns 400

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | Go: dependents computation, id validation, delete ordering; TS: picker data model (tiers, search, letter groups), region inference, model ordering/chips, draft guard | Logic in isolation |
| Integration | Go `httptest` against `HandleProviders`/onboarding with a temp `OMNIPUS_HOME`; vitest component tests with mocked REST | Components together |
| E2E | Playwright against the embedded-SPA binary (`tests/e2e/providers.spec.ts`, `onboarding`, `accessibility.spec.ts`) | Operator-visible flows, a11y |

Conventions: Go tests run as `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/...` one at a time locally; the full suite runs only in CI. Vitest files sit beside the component. Playwright specs extend `tests/e2e/providers.spec.ts`.

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `TestNoAntigravityInTree` | Unit (Go, `pkg/providers`) | Antigravity leaves no trace in the source tree | Walks `pkg cmd src contracts config docs`, asserts only the allow-listed historical files contain the word |
| 2 | `TestProbeProviderID_Validation` | Unit (Go) | Probe provider id validation | Table over the outline rows; asserts status, field, no id list echoed |
| 3 | `TestProviderDependents` | Unit (Go, `pkg/gateway`) | Dependents are listed…; Fallback references are removed and listed | Computes `{id,name,role}` from agent entities incl. fallback role |
| 4 | `TestFactory_NoVendorCaseForRemovedIDs` | Unit (Go) | `claude-cli` is an unknown provider | `antigravity`, `claude-cli`, `claudecli`, `codexcli` construct nothing; `codex-cli` → `*CodexCliProvider`; `openai-chatgpt` → `*CodexProvider` |
| 5 | `TestRecommendedChipSelection` (`model-ordering.test.ts`) | Unit (TS) | At most three Recommended chips per provider | eligibility + tie-break |
| 6 | `TestModelOrdering` (`model-ordering.test.ts`) | Unit (TS) | Models ordered by vendor then release date | group + date desc + undated last |
| 7 | `TestRegionFromLocale` (`region-inference.test.ts`) | Unit (TS) | Region inferred from locale | outline rows |
| 8 | `TestPickerModel` (`provider-picker-model.test.ts`) | Unit (TS) | Picker opens with 8 Popular…; Search expands…; Custom endpoint is last | pure data transform: tiers, recent, search over company/plan/region/alias, letter groups, custom last |
| 9 | `TestDraftGuard` (`use-draft-guard.test.ts`) | Unit (TS) | Close behaviour by draft state | whitespace = empty; saved = clean |
| 10 | `TestDeleteProvider_Unused200` | Integration (Go) | Remove an unused provider after one confirmation | 200 body, config row gone, `<id>_API_KEY` gone |
| 11 | `TestDeleteProvider_DependentsLeftWithoutModel` | Integration (Go) | Dependents are listed and left without a model | agents' model/provider cleared, `needs_model` true in GET /agents |
| 12 | `TestDeleteProvider_DefaultRequiresReplacement409` | Integration (Go) | DELETE without replacement… is refused | 409, nothing changed |
| 13 | `TestDeleteProvider_WithNewDefault` | Integration (Go) | Default-backing provider requires an inline new default | order: default changed then provider removed; reload waited |
| 14 | `TestDeleteProvider_404_503_Bypass503` | Integration (Go) | Removing an unconfigured…; Removal refused while locked | 404 / 503 / RequireNotBypass 503 |
| 15 | `TestDeleteProvider_OnlyProvider` | Integration (Go) | Removing the only provider | default cleared, GET default-model empty |
| 16 | `TestDeleteProvider_FallbackRemoved` | Integration (Go) | Fallback references are removed and listed | fallback entry dropped, `needs_model` false |
| 17 | `TestDefaultModel_PutGet` | Integration (Go) | Change default model takes effect on the next turn | PUT → GET round-trip; registry resolves new pair (mirrors `rest_default_agent_singleton_test.go`) |
| 18 | `TestDefaultModel_PutValidation` | Integration (Go) | Default-model PUT validation | outline rows |
| 19 | `TestTurn_ModelUnassignedTypedError` | Integration (Go, `pkg/agent`) | Turn to an agent without a model is refused | `LLMError.code == model_unassigned`, zero provider calls |
| 20 | `TestSignIn_RefusedForKeyOnly400` | Integration (Go) | Sign-in refused for a key-only provider | 400 |
| 21 | `TestSignInStatus_CodexCLI` | Integration (Go) | Signed-in row…; Expired session… | fake `auth.json` fresh → `signed_in` + label; stale → `expired`; missing binary → `disconnected` |
| 22 | `TestOnboardingComplete_AuthMethod` | Integration (Go) | Onboarding complete with sign-in | sign_in without key 200; with key 400; api_key without key 400 |
| 23 | `TestRefreshModels_Intersection` | Integration (Go) | Check with my account greys…; upstream failure | greyed set, unknown flag, one upstream call; 429 path |
| 24 | `TestContractsRegenerateClean` (CI `make verify-contracts`) | Integration | Contracts regenerate cleanly | no drift; `contract_test.go` passes with new schemas |
| 25 | `ProviderPicker.test.tsx` | Integration (vitest) | Picker opens…; Search…; Expanded list…; Unsupported…; Recently used…; Keyboard-only…; Catalog unavailable | renders against the 190 fixture; asserts testids, aria, bounded rows, error state |
| 26 | `AuthMethodControl.test.tsx` | Integration (vitest) | Auth methods offered per provider; OpenAI sign-in offers two named providers | outline rows; default radio `codex-cli` |
| 27 | `ProvidersSection.test.tsx` (extended) | Integration (vitest) | Default model card…; Set as default from row; Signed-in row…; Expired session…; Esc with a dirty key…; Discard clears…; No Undo exists | existing file gains describes per scenario |
| 28 | `RemoveProviderDialog.test.tsx` | Integration (vitest) | Remove an unused…; Dependents…; Default-backing…; Removing the only provider | dialog content, disabled state, `role="alertdialog"`, focus on Cancel |
| 29 | `model-selector.test.tsx` (extended) | Integration (vitest) | Model selector virtualisation threshold; Onboarding model field is empty and labelled | 100 vs 101 rows; label; no value |
| 30 | `providers.spec.ts` (Playwright, extended) | E2E | Remove…; Change default model takes effect…; Check with my account…; SPA reads the catalog from the GET | real binary, stub vendor |
| 31 | `onboarding.spec.ts` (Playwright) | E2E | Fresh install seeds no default model; Auth-method control keeps three steps; Onboarding model field empty | three steps, empty model, Finish disabled |
| 32 | `accessibility.spec.ts` (Playwright, extended) | E2E | all picker/dialog scenarios (a11y constraints) | axe 0 serious/critical; target size; focus ring; keyboard |
| 33 | `TestBuildGates_FilesGone` (CI) | E2E | Build and contract gates pass with the files gone | exit codes captured without a pipe |

### Test Datasets

#### Dataset: Provider id (probe and DELETE path)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `""` | Empty | 400 field=id | Probe provider id validation | |
| 2 | `"a"` | Min | 200 if in catalog else 400 | Probe provider id validation | single char passes pattern |
| 3 | 64×`a` | Max | pattern ok; 400 unknown | Probe provider id validation | |
| 4 | 65×`a` | Max+1 | 400 maxLength | Probe provider id validation | |
| 5 | `"OPENROUTER"` | Case | 400 pattern | Probe provider id validation | lowercase only |
| 6 | `"open router"` | Whitespace | 400 pattern | Probe provider id validation | |
| 7 | `"../etc"` | Special | 400 pattern | Probe provider id validation | path traversal guard |
| 8 | `"zai-coding-plan"` | Valid | 200 | Probe provider id validation | hyphen allowed |
| 9 | `"antigravity"` | Removed id | 400, body has no "antigravity" | Probe provider id validation | generic message |
| 10 | `"codexcli"` | Removed alias | 400 | Probe provider id validation | no alias |
| 11 | `"openai-chatgpt"` | New id | 400 for probe (sign-in provider has no key) / valid for sign-in | Sign-in refused for a key-only provider | probe is key-only |
| 12 | `"智谱"` | Unicode | 400 pattern | Probe provider id validation | aliases are search terms, not ids |

#### Dataset: DELETE /providers/{id} bodies

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | no body, not default | Happy | 200 `default_changed:false` | Remove an unused provider after one confirmation | |
| 2 | no body, backs default | Error | 409 | DELETE without replacement… is refused | |
| 3 | `{"new_default":{"provider":"anthropic","model":"claude-sonnet-4.6"}}` | Happy | 200 `default_changed:true` | Default-backing provider requires an inline new default | |
| 4 | `{"new_default":{"provider":"openrouter",...}}` (same id) | Error | 400 | DELETE without replacement… is refused | self-reference |
| 5 | `{"new_default":{"provider":"groq","model":"x"}}` (unconfigured) | Error | 400 | Default-model PUT validation | |
| 6 | `{"new_default":{}}` | Empty object | 400 | Default-model PUT validation | required fields |
| 7 | malformed JSON | Corrupt | 400 | Default-model PUT validation | |
| 8 | no body, only provider, backs default | Edge | 200, default cleared | Removing the only provider | no other connected provider |
| 9 | concurrent DELETE ×2 same id | Concurrency | first 200, second 404 | Removing an unconfigured provider returns 404 | under `configMu` |

#### Dataset: Dependents computation

| # | Input (agents) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | none reference id | Empty | `[]` | Remove an unused provider after one confirmation | |
| 2 | one primary | Single | `[{role:primary}]` | Dependents are listed and left without a model | |
| 3 | one primary + same agent fallback | Duplicate | one entry, role primary | Dependents are listed and left without a model | de-dup |
| 4 | fallback only | Alt | `[{role:fallback}]` | Fallback references are removed and listed | |
| 5 | locked core agent primary | Edge | listed | Dependents are listed and left without a model | no exemption |
| 6 | 50 agents primary | Large | 50 entries, names sorted | Dependents are listed and left without a model | dialog scroll |
| 7 | agent with `provider:""` and model slug matching the provider's ModelName | Inferred | listed as primary | Dependents are listed and left without a model | `resolveFallbackProvider` rule 1 |

#### Dataset: Default-model PUT

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | connected provider + catalog model | Happy | 200 | Change default model takes effect on the next turn | |
| 2 | unconfigured provider | Error | 400 provider | Default-model PUT validation | |
| 3 | connected + unknown model | Error | 400 model | Default-model PUT validation | |
| 4 | `custom` + user slug | Alt | 200 | Default-model PUT validation | catalog bypass for custom/local |
| 5 | `ollama` + live-listed model | Alt | 200 | Default-model PUT validation | local endpoint |
| 6 | empty provider | Empty | 400 | Default-model PUT validation | |
| 7 | model 256 chars | Max | 200 if listed | Default-model PUT validation | |
| 8 | model 257 chars | Max+1 | 400 | Default-model PUT validation | |
| 9 | extra property | Schema | 400 | Default-model PUT validation | `additionalProperties:false` |
| 10 | `signed_in` provider + model | Alt | 200 | Change default model takes effect on the next turn | sign-in counts as connected |
| 11 | `expired` provider | Error | 400 provider | Default-model PUT validation | not connected |

#### Dataset: Picker search

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `""` | Empty | collapsed | Picker opens with 8 Popular tiles… | |
| 2 | `"   "` | Whitespace | collapsed | Search expands and filters… | trimmed |
| 3 | `"z"` | Single char | expands; all with z | Search expands and filters… | |
| 4 | `"Coding Plan"` | Case | plan matches | Search expands and filters… | |
| 5 | `"china"` | Region | region matches | Search expands and filters… | |
| 6 | `"glm-coding"` | Alias | Z.ai row | Search expands and filters… | alias list |
| 7 | `"bedrock"` | Unsupported | disabled row | Unsupported provider is visible… | still shown |
| 8 | `"(*["` | Regex chars | no crash, no match | Search expands and filters… | literal |
| 9 | 200-char string | Very long | no match state | Search expands and filters… | |
| 10 | `"智谱"` | Unicode alias | Z.ai row if alias present | Search expands and filters… | |

#### Dataset: Model ordering and chips

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 0 models | Empty | "No models" | Model selector virtualisation threshold | |
| 2 | 1 model, tool+200k | Single | 1 chip | At most three Recommended chips | |
| 3 | 3 eligible | Max | 3 chips | At most three Recommended chips | |
| 4 | 4 eligible | Max+1 | 3 chips (oldest dropped) | At most three Recommended chips | |
| 5 | window 127,999 | Min-1 | no chip | At most three Recommended chips | |
| 6 | window 128,000 | Min | chip | At most three Recommended chips | |
| 7 | no tool calling, 1M window | Disqualified | no chip | At most three Recommended chips | |
| 8 | same date two models | Tie | id asc | Models ordered by vendor… | |
| 9 | no date | Null | last in group | Models ordered by vendor… | |
| 10 | 100 / 101 / 359 | Threshold | all / virtual / virtual | Model selector virtualisation threshold | |

#### Dataset: Sign-in status (`codex-cli` / `openai-chatgpt`)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | binary missing | Missing dep | `disconnected` + hint | Expired session routes to re-sign-in | |
| 2 | binary present, no `auth.json` | Missing file | `not_signed_in` | Signed-in row shows account | |
| 3 | `auth.json` mtime 10 min | Fresh | `signed_in` + label | Signed-in row shows account | |
| 4 | `auth.json` mtime 61 min (`openai-chatgpt`) | Expired | `expired` | Expired session routes to re-sign-in | mirrors `codex_cli_credentials.go` 1h rule |
| 5 | `auth.json` unreadable (perm) | Permission | `not_signed_in` + warning | Expired session routes to re-sign-in | no crash |
| 6 | `auth.json` malformed | Corrupt | `not_signed_in` + warning | Expired session routes to re-sign-in | |
| 7 | account field absent | Null | label = "Signed in" | Signed-in row shows account | fallback |

#### Regression Dataset: existing provider behaviour preserved

| # | Input | Previous Behaviour | Must Still Produce | Traces to |
|---|---|---|---|---|
| 1 | `PUT /providers/openrouter` with invalid key | 422, nothing persisted (`TestPutProvider_InvalidKey422NotPersisted`) | same | Regression: provider validation |
| 2 | `PUT` with no-credit key | 200 persisted + validation warning (`TestPutProvider_NoCredit200Persisted`) | same | Regression: provider validation |
| 3 | `PUT` without `api_key` on existing | no probe (`TestPutProvider_KeyUnchangedNoProbe`) | same | Regression: provider validation |
| 4 | `GET /providers` with locked vault | `providerCredErrors` message on row | same (now alongside new fields) | Regression: credential degraded (`provider_credential_degraded_test.go`) |
| 5 | WS provider refusal | `websocket_provider_refusal_test.go` | same | Regression: LLMError surfacing |
| 6 | Agent PUT with `delegation_policy` | 400 (ADR-037) | same | Regression: agent handler |
| 7 | Onboarding complete with `api_key` (auth_method api_key) | 200, default set once | same | Regression: onboarding |
| 8 | Company group headers only when ≥2 configured variants | `ProvidersSection.test.tsx` FIX-2 | same | Regression: configured list |

### Regression Test Requirements

This feature **modifies existing functionality**.

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| PUT provider validation/persistence | `pkg/gateway/provider_validation_test.go` (3 tests) | No | must pass unchanged |
| Credential-degraded rows | `provider_credential_degraded_test.go` | No | GET branch gains fields; existing assertions hold |
| Agent↔provider resolution | `rest_agent_provider_test.go` | Yes — `TestAgentProvider_NeedsModelDerived` | `needs_model` must be false for every existing fixture |
| Default agent singleton | `rest_default_agent_singleton_test.go` | No | pattern reused for default model |
| Contract round-trip | `pkg/api/generated/contract_test.go` | No (auto-covers new schemas) | |
| Configured list rendering | `ProvidersSection.test.tsx` (5 describes) | Yes — update fixtures for new required fields `dependents`, `backs_default` | zod will reject old fixtures |
| Catalog embed parity | `catalog_test.go::TestCatalog_EmbedMatchesGeneratedTS` | **Delete** (the TS file is gone) — replace with a test that the served GET equals the embedded snapshot | ADR-067 owns the snapshot |
| Onboarding probe | `tests/e2e/providers.spec.ts` | Yes — extend for free-string id | |

---

## Functional Requirements

**D13 — policy and removals**
- **FR-001**: The tree MUST contain no occurrence of `antigravity` outside the historical decision records enumerated in ADR-068 §2.4.
- **FR-002**: The provider factory MUST construct nothing for `antigravity`, `claude-cli`, `claudecli`, `codexcli`; an agent or config naming them MUST fail on the generic unknown-provider path with no mention of the name.
- **FR-003**: The id `codex-cli` MUST dispatch to the subprocess provider; the id `openai-chatgpt` MUST dispatch to the direct-HTTP provider reading the CLI's saved token.
- **FR-004**: The catalog served to the SPA MUST declare `auth_methods` per provider; `sign_in` MUST be absent for `anthropic`, `google` and (until listing) `xai`, and present for `codex-cli`, `openai-chatgpt`, and the Copilot provider.
- **FR-005**: The UI MUST render a sign-in control only where `auth_methods` contains `sign_in`, pre-selected where present.
- **FR-006**: The `openai-chatgpt` option MUST carry the label *"relies on OpenAI's stated tolerance, not its written terms"*; `codex-cli` MUST be the default of the pair.
- **FR-007**: Omnipus MUST NOT write, refresh or proxy the vendor credential file for `codex-cli`; for `openai-chatgpt` it MUST only read it.
- **FR-008**: `POST /providers/{id}/sign-in` MUST return 400 for providers without `sign_in`.
- **FR-009**: `GET /providers/{id}/sign-in/status` MUST return one of `not_signed_in | pending | signed_in | expired` with `account_label` when known.

**D14 — deletion and default model**
- **FR-010**: `DELETE /api/v1/providers/{id}` MUST remove every config entry for the provider and the credential `<id>_API_KEY`, then wait for reload, then respond 200 with `ProviderDeleteResponse`.
- **FR-011**: `DELETE` MUST return 404 for an unconfigured id, 503 when the credential store is locked (before any change), 503 under dev-mode bypass, 409 when `backs_default` and no valid `new_default`, 400 when `new_default` names the same id or an unconnected provider.
- **FR-012**: `GET /api/v1/providers` rows MUST carry `dependents[]` (`{id,name,role}`) and `backs_default` so the dialog needs no second request.
- **FR-013**: On deletion, each dependent agent's primary `model`/`provider` MUST be cleared (never re-pointed); fallback entries naming the provider MUST be removed; the response MUST list both.
- **FR-014**: `GET /api/v1/agents` MUST expose `needs_model: boolean` (true when the primary model is empty or its provider is not configured); the agent list MUST render *needs a model* for such agents.
- **FR-015**: A turn to an agent with `needs_model` MUST be refused with `LLMError.code = model_unassigned` and no provider call.
- **FR-016**: The SPA MUST always show the confirm dialog before deletion, with the sentence *"Remove `<Display name>`? Its key will be deleted."*, dependents grouped by role, and — when `backs_default` — an inline new-default selector with *Remove* disabled until chosen.
- **FR-017**: The SPA MUST NOT render an Undo affordance after deletion, and MUST NOT retain the key client-side at any time.
- **FR-018**: `GET/PUT /api/v1/providers/default-model` MUST exist; `PUT` MUST validate provider connected (`connected | signed_in`) and model in catalog (except `custom`, `ollama`, `vllm`, where the provider's own list applies), persist `agents.defaults.{provider,model_name}` under the config lock, wait for reload, and take effect on the next turn.
- **FR-019**: Settings → Providers MUST render the *Default model* card first, showing `provider · model · window · source`, with *Change* opening the selector filtered to connected providers; the backing row MUST show a *Default* marker; each row MUST offer *Set as default model…*.
- **FR-020**: The onboarding completion guard (`ModelName == ""`) MUST NOT overwrite a default written by the PUT.

**§4/§5 — surfaces**
- **FR-021**: One `ProviderPicker` component MUST be used by onboarding step 3 and the Settings sheet.
- **FR-022**: The picker MUST render, in order: exactly the 8 Popular tiles (FR constants list), *Recent* (when any), one search field, *All providers (N)* collapsed until query non-empty (trimmed) or expanded, *Custom endpoint* last.
- **FR-023**: The expanded list MUST be letter-grouped (A–Z, `#`) and virtualised with `aria-setsize`/`aria-posinset`; rendered options MUST be ≤ visible + 10.
- **FR-024**: Search MUST match company, plan, region and alias, case-insensitively, treating the query literally.
- **FR-025**: Unsupported providers MUST render `aria-disabled="true"` with their reason and MUST NOT be hidden by default.
- **FR-026**: The picker MUST be built on cmdk `Command` and support ArrowUp/Down/Home/End/Enter/Esc.
- **FR-027**: The second-level panel MUST present plan and region as `aria-pressed` groups; region MUST default from locale per the inference map with the copy *"Detected: `<Region>` — change"* (or *"Region — change"* when not inferred).
- **FR-028**: The auth-method segmented control MUST live in the second-level panel; onboarding MUST stay three steps.
- **FR-029**: Onboarding MUST NOT pre-select a model; the field label MUST be *"Model for your first agent"*; *Finish* MUST be disabled until a model is chosen and the probe has passed.
- **FR-030**: The model selector MUST order by vendor group then release date desc (undated last, id asc), mark ≤3 *Recommended for chat* per provider (tool calling AND window ≥128,000), and virtualise above 100 items.
- **FR-031**: *Refresh models* MUST be replaced by *Check with my account*, which makes one live call, intersects with the catalog, greys absent models with *not available on this key*, flags catalog-unknown with *limits unknown*, caches per key, and leaves the list unchanged on failure with an inline warning.
- **FR-032**: Each configured row MUST show on expand per-model window · output · image · PDF and the window source label (values per ADR-066 D9).
- **FR-033**: Closing the config sheet via Esc/overlay with a non-empty unsaved key MUST keep it open and show *"Discard key?"* with *Discard* / *Keep editing*; explicit *Cancel* and clean states close without prompt.
- **FR-034**: Row states MUST include `signed_in` (*Signed in as …*, action *Manage*) and `expired` (*Session expired*, action opens re-sign-in).

**§6/§7 — wire**
- **FR-035**: Every new or changed wire shape (`ProviderDeleteRequest`, `ProviderDeleteResponse`, `DefaultModel`, `DefaultModelUpdateRequest`, `SignInStartResponse`, `SignInStatus`, `Provider` additions, `Agent.needs_model`, `OnboardingCompleteRequest.provider.auth_method`, `ProbeProviderRequest.id`, LLMError `model_unassigned`) MUST be defined in `contracts/` first and consumed only via generated types; `make verify-contracts` MUST pass.
- **FR-036**: `ProbeProviderRequest.id` MUST be a free string (`1..64`, pattern `^[a-z0-9][a-z0-9._-]*$`) validated at runtime against the served catalog; unknown without `endpoint` → 400 naming `id`, never echoing an id list.
- **FR-037**: `src/lib/generated/providerCatalog.ts` and the `gen/main.go` TS emission MUST be deleted; the SPA MUST read the providers-catalog GET (ADR-067 §5) with at most one fetch per session unless the validator changes.
- **FR-038**: `Provider.status` enum MUST be exactly `connected | disconnected | error | signed_in | expired`.
- **FR-039**: `ProviderCatalogEntry.yaml`'s "never served from a live HTTP endpoint" description MUST be removed (the schema is superseded by ADR-067's catalog schema).
- **FR-040**: Fresh-install seed (`pkg/config/defaults.go`, `config/config.example.json`) MUST name no removed provider and MUST leave `agents.defaults.{provider,model_name}` empty until onboarding writes them.

**Accessibility**
- **FR-041**: All constraints under "Accessibility (WCAG 2.2 AA)" MUST hold; axe MUST report 0 serious/critical on onboarding step 3 and Settings → Providers with sheet and dialog open.

---

## Success Criteria

- **SC-001**: `grep -ril antigravity pkg cmd src contracts config docs | grep -v -F -f <allowlist>` prints nothing (exit 1).
- **SC-002**: `make verify-contracts`, `gofmt -l . | wc -l` = 0, `golangci-lint run --build-tags=goolm,stdjson`, `CGO_ENABLED=1 go build -tags goolm,stdjson ./...`, `npm run typecheck`, `npx vitest run` all exit 0 on CI (exit codes captured without a pipe).
- **SC-003**: After `DELETE /providers/{id}` returns 200, `credentials.Store.Get("<id>_API_KEY")` returns `NotFoundError` in the same process within 0 ms of the response (synchronous).
- **SC-004**: Default-model PUT → one chat turn: the session transcript's model/provider equals the PUT body in 10/10 runs without restart.
- **SC-005**: Picker with the 190 fixture: rendered `[role="option"]` count ≤ visible + 10 in 100% of Playwright runs; first paint ≤ 100 ms p95.
- **SC-006**: Model selector with 359 models: rendered rows ≤ visible + 10; with 100 models: exactly 100.
- **SC-007**: axe-core: 0 `serious`/`critical` violations on the three audited states; every listed target ≥ 24×24 CSS px (measured via `getBoundingClientRect`).
- **SC-008**: Onboarding e2e: step tracker has exactly 3 steps; *Finish* disabled until model chosen; completion request carries `auth_method`.
- **SC-009**: 0 occurrences of `Undo` in `RemoveProviderDialog.tsx` and its tests' DOM snapshots.
- **SC-010**: `grep -rn "generated/providerCatalog" src pkg` is empty; `ls src/lib/generated/providerCatalog.ts` fails.
- **SC-011**: `ProbeProviderRequest.yaml` contains no `enum:` under `id`; `rest_onboarding.go` validates against the catalog, and the 400 body for an unknown id has no `enum`/list.
- **SC-012**: `Provider.status` generated Go consts are exactly five.
- **SC-013**: A turn to a `needs_model` agent produces an `LLMError` with `code == "model_unassigned"` and the provider mock records 0 calls.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-1 | Antigravity leaves no trace in the source tree | TestNoAntigravityInTree |
| FR-002 | US-1, US-2 | Config naming a removed provider fails generically; `claude-cli` is an unknown provider | TestFactory_NoVendorCaseForRemovedIDs; TestProbeProviderID_Validation |
| FR-003 | US-2 | OpenAI sign-in offers two named providers… | TestFactory_NoVendorCaseForRemovedIDs; AuthMethodControl.test.tsx |
| FR-004 | US-2 | Auth methods offered per provider; Sign-in refused for a key-only provider | AuthMethodControl.test.tsx; TestSignIn_RefusedForKeyOnly400 |
| FR-005 | US-2, US-5 | Auth methods offered per provider; Auth-method control keeps onboarding at three steps | AuthMethodControl.test.tsx; onboarding.spec.ts |
| FR-006 | US-2 | OpenAI sign-in offers two named providers… | AuthMethodControl.test.tsx |
| FR-007 | US-2 | Signed-in row shows account and Manage; Expired session routes to re-sign-in | TestSignInStatus_CodexCLI |
| FR-008 | US-2 | Sign-in refused for a key-only provider | TestSignIn_RefusedForKeyOnly400 |
| FR-009 | US-2 | Signed-in row…; Expired session… | TestSignInStatus_CodexCLI; ProvidersSection.test.tsx |
| FR-010 | US-3 | Remove an unused provider after one confirmation | TestDeleteProvider_Unused200; providers.spec.ts |
| FR-011 | US-3 | DELETE without replacement…; Removing an unconfigured…; Removal refused while locked | TestDeleteProvider_DefaultRequiresReplacement409; TestDeleteProvider_404_503_Bypass503 |
| FR-012 | US-3 | Dependents are listed…; Default-backing provider requires… | TestProviderDependents; RemoveProviderDialog.test.tsx |
| FR-013 | US-3 | Dependents are listed and left without a model; Fallback references are removed and listed | TestDeleteProvider_DependentsLeftWithoutModel; TestDeleteProvider_FallbackRemoved |
| FR-014 | US-3 | Dependents are listed and left without a model | TestDeleteProvider_DependentsLeftWithoutModel; TestAgentProvider_NeedsModelDerived |
| FR-015 | US-3 | Turn to an agent without a model is refused | TestTurn_ModelUnassignedTypedError |
| FR-016 | US-3 | Remove an unused…; Dependents…; Default-backing…; Removing the only provider | RemoveProviderDialog.test.tsx; TestDeleteProvider_OnlyProvider |
| FR-017 | US-3 | No Undo exists after removal | ProvidersSection.test.tsx; providers.spec.ts |
| FR-018 | US-4 | Change default model takes effect…; Default-model PUT validation | TestDefaultModel_PutGet; TestDefaultModel_PutValidation |
| FR-019 | US-4 | Default model card shows…; Set as default from the provider row | ProvidersSection.test.tsx; providers.spec.ts |
| FR-020 | US-4, US-1 | Fresh install seeds no default model; Change default model takes effect… | TestOnboardingComplete_AuthMethod; TestDefaultModel_PutGet |
| FR-021 | US-5 | Picker opens with 8 Popular tiles…; Recently used row appears | ProviderPicker.test.tsx; onboarding.spec.ts |
| FR-022 | US-5 | Picker opens with 8 Popular tiles…; Search expands…; Custom endpoint is last | TestPickerModel; ProviderPicker.test.tsx |
| FR-023 | US-5 | Expanded list is letter-grouped and virtualised | ProviderPicker.test.tsx; accessibility.spec.ts |
| FR-024 | US-5 | Search expands and filters the full list | TestPickerModel |
| FR-025 | US-5 | Unsupported provider is visible, disabled, with reason | ProviderPicker.test.tsx |
| FR-026 | US-5 | Keyboard-only selection | ProviderPicker.test.tsx; accessibility.spec.ts |
| FR-027 | US-5 | Region inferred from locale | TestRegionFromLocale; ProviderPicker.test.tsx |
| FR-028 | US-5 | Auth-method control keeps onboarding at three steps | onboarding.spec.ts |
| FR-029 | US-6, US-1 | Onboarding model field is empty and labelled; Fresh install seeds no default model | model-selector.test.tsx; onboarding.spec.ts |
| FR-030 | US-6 | Models ordered…; At most three Recommended chips…; Model selector virtualisation threshold | TestModelOrdering; TestRecommendedChipSelection; model-selector.test.tsx |
| FR-031 | US-7 | Check with my account greys…; Check with my account upstream failure | TestRefreshModels_Intersection; providers.spec.ts |
| FR-032 | US-7 | Row expand shows limits and window source | ProvidersSection.test.tsx |
| FR-033 | US-8 | Esc with a dirty key…; Discard clears the draft; Close behaviour by draft state | TestDraftGuard; ProvidersSection.test.tsx |
| FR-034 | US-2 | Signed-in row…; Expired session… | ProvidersSection.test.tsx |
| FR-035 | US-9 | Contracts regenerate cleanly; Onboarding complete with sign-in | TestContractsRegenerateClean; TestOnboardingComplete_AuthMethod |
| FR-036 | US-9 | Probe provider id validation | TestProbeProviderID_Validation |
| FR-037 | US-9 | SPA reads the catalog from the GET, not a bundle; Catalog unavailable in the picker | providers.spec.ts; ProviderPicker.test.tsx |
| FR-038 | US-2, US-9 | Signed-in row…; Contracts regenerate cleanly | TestContractsRegenerateClean; ProvidersSection.test.tsx |
| FR-039 | US-9 | Contracts regenerate cleanly | TestContractsRegenerateClean |
| FR-040 | US-1 | Fresh install seeds no default model | onboarding.spec.ts; TestBuildGates_FilesGone |
| FR-041 | US-5, US-3 | Expanded list…; Keyboard-only…; Remove an unused provider… (dialog a11y) | accessibility.spec.ts |

**Completeness check**: every FR has ≥1 scenario and ≥1 test; every BDD scenario above appears in at least one row (Build and contract gates → FR-040/SC-002 via TestBuildGates_FilesGone; Catalog unavailable → FR-037; Removing the only provider → FR-016; No Undo → FR-017).

---

## Ambiguity Warnings

Items the ADR is silent on. Each is implemented under the **Likely Agent Assumption** (labelled as such in the text above) until the operator resolves it.

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|---|---|---|
| 1 | Which catalog fields the picker/selector need (`tier`, `unsupported_reason`, `auth_methods`, `aliases`, per-model `release_date`, `tool_calling`, `context_window`) — ADR-067 §5 names the endpoint but not these fields | ADR-067's catalog schema is extended to carry exactly the fields listed under "Providers catalog" above; this spec consumes them and adds none elsewhere | Does ADR-067's schema already carry these, or must its implementer add them? Is the 8-id Popular set data in the catalog (`tier: popular`) or a constant in the SPA? |
| 2 | Where the "dependents" data for the dialog comes from ("driven by data, not a second request") | `GET /providers` rows carry `dependents[]` and `backs_default`; `DELETE` takes an optional `new_default` body and returns the same shape | Accept `GET`-carried dependents, or prefer a `DELETE ?dry_run=true` round trip? |
| 3 | Agents that reference the provider only as a **fallback** model | The fallback entry is removed and the agent is listed in the dialog under *"uses as fallback"*; primary `needs_model` unaffected | Remove the fallback entry, or leave it and skip it at runtime with a WARN? |
| 4 | Removing the **only** configured provider (no other connected provider to re-point the default to) | Allowed; dialog states the consequence; default cleared; card shows *No default model* | Allow, or block with *"Connect another provider first"*? |
| 5 | How a dependent is "left without a model" on the wire | Server clears the agent entity's primary `model`/`provider`; `Agent.needs_model` is a derived boolean (also true for ADR-067 MAJ-010's unknown-provider case) | Clear the stored fields, or leave them and derive `needs_model` only from provider absence? |
| 6 | The LLMError code for "agent has no model" | New code `model_unassigned` (attribution `config`), not a reuse of `model_unavailable` (whose copy says "this reply used the previous model" — false here) | New code, or reuse with new copy? |
| 7 | GitHub Copilot provider id and mechanism ("official SDK/CLI") | Provider id `github-copilot`; mechanism = the official Copilot CLI driven as a subprocess (same shape as `codex-cli`), no new Go module (Constraint #1) | Subprocess of the CLI, or the Go SDK package (a new module dependency)? Exact id? |
| 8 | What *Sign in* does for OpenAI, given `codex login` is interactive and `pkg/auth` already has an OpenAI device-code flow | Sign in = instruct *"Run `codex login` on this machine"* + *Check* (status endpoint reads the CLI's saved login); Omnipus starts no device-code flow of its own | May Omnipus drive an in-app device-code login (it exists in `pkg/auth/oauth.go`), or only detect the CLI's login? |
| 9 | Route path for the default-model control ("a PUT on the default") | `GET/PUT /api/v1/providers/default-model` (under the providers handler; `default-model` is reserved, cannot collide with an id because ids are validated against the catalog) | This path, or `/api/v1/settings/default-model`? |
| 10 | "Replace the fresh-install default model with a Popular-tier API-key model" (§2.2) vs "no default model is pre-selected" (§4) | Seed `agents.defaults` **empty**; the Popular-tier replacement applies only to `config.example.json` and any seeded provider template row that named `antigravity` | Confirm the seed is empty and onboarding's explicit pick is the only writer on fresh install. |
| 11 | Whether `claude-cli` code is **deleted** or merely unreachable | Deleted (files, factory cases, enum values, catalog allow-list row, docs mentions) with the same "no trace" rule as `antigravity`, minus the grep exit proof on the word "claude" (it is a model family name) | Delete, or keep the file unreferenced? |
| 12 | Probe model for onboarding ("picks its probe model from the catalog") vs the operator's explicit pick | `ProbeProviderRequest` gains optional `model`; when present the probe uses it, otherwise the provider's first Recommended model | Should the probe validate the user's exact pick (extra probe on model change) or only the key? |
| 13 | `expired` semantics for `openai-chatgpt` | Mirrors `codex_cli_credentials.go`: `auth.json` older than 1 h → `expired` | Is the 1 h rule correct for the split provider, or should it follow the token's own expiry claim? |
| 14 | Recent section source | Recent = providers currently configured, most recently saved first, max 3 | Configured-only, or also previously-selected-but-not-saved? |
| 15 | Window/source on the Default model card and row expand depend on ADR-066 D9 | `DefaultModel` carries optional `context_window`/`window_source` populated by ADR-066's resolver; absent until that lands, card shows `—` | Ship the card before ADR-066 D9 (with `—`), or sequence after? |
| 16 | `Provider.dependents`/`backs_default` on the always-present "fallback default entry" rows that `GET /providers` emits today | Those template rows report `dependents: []`, `backs_default: false`, and `DELETE` on them returns 404 (not configured) | Confirm that template rows are not deletable. |
| 17 | Region inference for `en-GB`, `en-AU`, etc. | `intl` (only `en-US` maps to `us`) | Confirm the map. |
| 18 | Whether deletion requires ReAuth (password re-type, Spec-6 FR-12.2) | No — the confirm dialog is the single gate; `RequireNotBypass` applies | Require re-auth for deletion? |

---

## Evaluation Scenarios (Holdout)

> **Note**: For post-implementation evaluation only. Not referenced in the TDD plan or traceability matrix. To be run by the operator or a separate evaluator against the built binary.

### Scenario: Cold install to first reply without ever seeing a pre-chosen model
- **Setup**: empty `OMNIPUS_HOME`, fresh binary, a real OpenRouter key.
- **Action**: complete onboarding choosing OpenRouter from the Popular band, pick a model from the list, send "hello".
- **Expected outcome**: at no point was a model pre-filled; the reply arrives from the chosen model (visible in Verbose chat); Settings → Providers shows that pair on the Default model card.
- **Category**: Happy Path

### Scenario: Swap the default while a chat is open
- **Setup**: two connected providers; a chat tab open on a default-routed agent.
- **Action**: in a second tab change the default model; return to the chat and send a message.
- **Expected outcome**: the new reply's model is the new default; no restart, no reload of the chat tab needed.
- **Category**: Happy Path

### Scenario: Find an obscure provider by typing
- **Setup**: Settings → Providers → Connect a provider.
- **Action**: type "moonshot" then "kimi".
- **Expected outcome**: both queries surface Moonshot; arrow keys reach it; Enter opens its panel with plan/region groups.
- **Category**: Happy Path

### Scenario: Delete the provider behind three agents
- **Setup**: three agents on Anthropic; Anthropic also backs the default; OpenAI connected.
- **Action**: Remove Anthropic, pick an OpenAI default inline, confirm.
- **Expected outcome**: the three agents show "needs a model"; a message to one of them is refused with a clear config error; `credentials.json` no longer decrypts an Anthropic key (`omnipus credentials list`).
- **Category**: Error

### Scenario: Try the forbidden path
- **Setup**: any install.
- **Action**: search the whole UI (onboarding, Settings, agent form) for any way to sign in to Anthropic or Google with an account.
- **Expected outcome**: none exists; only API-key fields.
- **Category**: Error

### Scenario: Keyboard-only operator
- **Setup**: unplug the mouse.
- **Action**: connect a provider, set it as default, remove another provider — keyboard only.
- **Expected outcome**: every step completes; focus is always visible; the confirm dialog opens with focus on Cancel.
- **Category**: Edge Case

### Scenario: Offline picker
- **Setup**: disconnect the network after boot.
- **Action**: open the picker and the model selector.
- **Expected outcome**: both populate fully from the local catalog; only Test / Check with my account fail, with a clear message.
- **Category**: Edge Case

---

## Assumptions

- ADR-068 is the confirmed brief (Phase 1 gate); every silence is recorded in Ambiguity Warnings, not resolved by invention.
- ADR-067 lands first (catalog GET, protocol dispatch, canonical ids); this spec's picker reads that endpoint and never defines its data.
- ADR-066 D9 supplies window/source values; until it lands the card shows `—`.
- The Popular set is the 8 ids in ADR-067 §4.2 and pass-2 MIN-008.
- The vendor CLIs (`codex`, Copilot CLI) are installed by the operator; Omnipus never installs them.
- No Windows-specific behaviour: file locking caveats in CLAUDE.md apply to `config.json` writes as they do to every store today.
- Out of scope: xAI sign-in implementation (gated), the catalog assembly job, context-window resolution, any agent-form change beyond rendering *needs a model*.

## Clarifications

### 2026-08-22

- Q: Can the operator be asked during this spec? -> A: No (Phase 1 gate taken as the ADR); the Ambiguity Warnings table is the operator's resolution sheet.
- Q: Is GitNexus available for this worktree? -> A: No index for this checkout and no MCP tools exposed; direct Read/Grep used, recorded under Existing Codebase Context.
- Q: Does `docs/reference/go-implementation/` exist? -> A: No; in-repo precedents substituted.
- Q: Undo after delete? -> A: None (operator decision 2026-08-22 via pass-2 MAJ-011).
- Q: `codex-cli` dispatch? -> A: Subprocess; token-reuse path renamed `openai-chatgpt` (pass-2 MAJ-013).
- Q: xAI? -> A: API-key only until xAI lists Omnipus (pass-2 MAJ-012).
