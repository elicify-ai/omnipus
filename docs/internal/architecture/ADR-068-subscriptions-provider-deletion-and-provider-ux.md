# ADR-068: Subscription login policy, provider deletion, the default model, and the provider UX at 190 providers

- **Status:** Accepted (operator approval 2026-08-23 — implementation plan approved). Proposed 2026-08-22; split out of ADR-066 after its second adversarial review.
- **Date:** 2026-08-22
- **Related:** [ADR-066](ADR-066-context-budget-and-tool-result-routing.md) (the incident fix; D9 there defines the Settings controls for caps and window that §4 places on screen); [ADR-067](ADR-067-registry-fed-catalog-and-provider-identity.md) (the catalog and provider table every screen here reads; D12's tiers and selector rule); [ADR-060](ADR-060-structured-tool-failure-family.md) (structured refusals); CLAUDE.md **Constraint #8** (contract-first wire formats).
- **Deciders:** Operator (Daniel Piatkowski)
- **Evidence level:** 1 for everything cited as read. Incident facts were read on the build tree the failing binary came from (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/build-v0.1.1` @ `6acd378`); design facts on this branch @ `4684e8c7`. Cited as `file::symbol` per CLAUDE.md except where a line number is itself the claim. Absences are cited as searches. Items marked **[UNVERIFIED]** are collected in §8 of ADR-067 and the notes inline here.

> **Greenfield rule (operator, 2026-08-22), applies to the entire scope:** no backward compatibility, no migration, no aliasing of old names, no grace periods, no retired-name lists, no boot notifications about removed things. Pre-existing Omnipus state that does not match this design simply does not work.

---

## 1. Context

With every registry provider selectable (ADR-067 D12), three questions the old 23-provider picker never had to answer become decisions: which vendors may be used through a *subscription* login rather than an API key — checked against each vendor's own terms, with one shipped provider failing that test; how a provider is removed and how the default model is changed, neither of which is possible today; and whether the onboarding and Settings screens can carry ~190 providers without losing the user. The code facts below were verified on this branch on 2026-08-22.

---

## 2. D13 — Subscription login: only where the vendor permits it, verified from the vendor's own terms

*(Operator direction 2026-08-22: "only where the vendor does not forbid it". The operator's recollection — Anthropic and Google forbid, others tolerate — was checked against each vendor's own published terms on 2026-08-22 and is confirmed, with one material consequence for code Omnipus ships today.)*

### 2.1 Vendor by vendor — primary sources

| Vendor | Borrowing the subscription token in a third-party tool | Driving the vendor's own CLI as a subprocess | Source |
|---|---|---|---|
| **Anthropic** | **Prohibited.** *"Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, or Max plan credentials on behalf of their users. Moreover, developers may not collect, store, or intermediate Claude.ai credentials or session tokens."* Since 2026-04-04 a Claude login in a third-party tool no longer draws on subscription limits at all (Boris Cherny, Head of Claude Code). | **Permitted only if** the `claude` binary is unmodified and the end user signs in inside it themselves: *"Nor does it prevent an end user from signing in to the unmodified Claude Code binary with their own Claude subscription, including where a platform hosts Claude Code."* Whether a harness-driven subprocess is "ordinary use" after 2026-04-04 is **unclear**. | code.claude.com/docs/en/legal-and-compliance; anthropic.com/legal/consumer-terms §3(7) |
| **Google** | **Prohibited, explicitly, naming the practice.** Antigravity Additional Terms §6: *"Using third party software, tools, or services to access the Service (e.g. using OpenClaw with Antigravity OAuth) is a breach of this Agreement"* and *"may be grounds for suspension or termination of your account."* Gemini CLI ToS: *"Directly accessing the services powering Gemini CLI … using third-party software … is a violation."* **Enforced:** Antigravity accounts of OpenClaw-OAuth users suspended, Feb 2026; Google staff cite §6 on the official forum. | Not addressed by the text — **unclear**. | antigravity.google/terms §6; geminicli.com/docs/resources/tos-privacy; discuss.ai.google.dev thread 126426 |
| **OpenAI** | **Permitted in practice, not in text.** Sam Altman, 2026-05-01: *"you can sign in to openclaw with your chatgpt account now and use your subscription there!"* No enforcement found. The ToS still prohibits *"Automatically or programmatically extract data or Output"* with no carve-out. | Fits the Help Center's supported-client list (Codex CLI / app-server). Lowest risk. | help.openai.com "Using Codex with your ChatGPT plan"; openai.com/policies/terms-of-use; x.com/sama/status/2050357911915028689 |
| **xAI** | **Permitted and vendor-sanctioned** for named agents: xAI published first-party OAuth integrations for Hermes (2026-05-18), OpenClaw (05-19), OpenCode (05-21), Kilo (05-29), Warp (06-15) — *"Use your SuperGrok or X Premium subscription inside OpenCode … More open-source agents and integrations are coming soon."* The AUP still bans unauthorised bots, so an unlisted harness is **medium** confidence. | Not addressed. | x.ai/news/grok-opencode, grok-openclaw, grok-hermes; x.ai/legal/acceptable-use-policy |
| **GitHub Copilot** | Raw token to `api.githubcopilot.com`: **not prohibited, not sanctioned** — unclear. | **Permitted via the official Copilot SDK / CLI**, billed to the subscription: *"A GitHub Copilot subscription is required to use the GitHub Copilot SDK … each prompt being counted towards your usage allowance."* | github.com/github/copilot-sdk; GitHub changelog 2026-06-02 |
| Kilo | Permitted (Gateway API offered for third-party apps). | — | kilo.ai/terms |
| Mistral (consumer) | Prohibited without written authorisation; the API is a separate business product. | — | legal.mistral.ai EU consumer terms |
| Cursor, Windsurf | No sanctioned consumer-credential path — skip. | — | — |

### 2.2 What Omnipus ships today, against that table

Verified in `pkg/providers` on this branch:

- **`claude-cli`** — `exec.CommandContext(ctx, "claude", …)`; the file handles no token, credential or keychain (searched). This is the shape Anthropic permits — but it is a *subscription* path, and the operator descoped all Anthropic subscription paths (§2.3 item 2). **Descoped.**
- **`codex-cli`** — *(re-grounded 2026-08-22 after the spec review, MAJ-005; the earlier sentence here was wrong.)* The factory has **two dispatch layers**. (1) The protocol switch, `factory_provider.go` `case "codex-cli", "codexcli"` → `NewCodexCliProvider` — the **subprocess**; that mapping is correct and is **kept**. (2) An id-keyed OAuth ladder in front of it: `case "openai"` with `cfg.AuthMethod == "oauth"|"token"` → `createCodexAuthProvider()`, which reads a **store-held** OAuth credential (`auth.GetCredential("openai")`) and refreshes it in-app via `createCodexTokenSource` → `auth.RefreshAccessToken`; and `case "anthropic"` with the same auth methods → `createClaudeAuthProvider()` — a store-held **Anthropic** OAuth token path. The `auth.json` reader (`codex_cli_credentials.go::CreateCodexCliTokenSource`) exists but has **no non-test caller** on this branch. End state per layer: `codex-cli` → subprocess (unchanged); a new id **`openai-chatgpt`** → `NewCodexProviderWithTokenSource(CreateCodexCliTokenSource())`, reading the Codex CLI's saved `auth.json` **only** (never written, never refreshed by Omnipus; `refresh_token` is not read); the store-held OpenAI OAuth path (`createCodexAuthProvider`, `createCodexTokenSource`, and `pkg/auth/oauth.go`'s `OpenAIOAuthConfig`/`RequestDeviceCode`/`PollDeviceCodeOnce`/`RefreshAccessToken`/`BuildAuthorizeURL`/`ExchangeCodeForTokens`) is **deleted** — Omnipus starts no OpenAI login flow of its own; the store-held **Anthropic** OAuth path (`createClaudeAuthProvider`, `createClaudeTokenSource`, `NewClaudeProviderWithTokenSource`) is **descoped and deleted** with the rest of the Anthropic subscription paths (§2.3 item 2). `ModelConfig.AuthMethod` values `oauth`/`token`, `knownProtocols`, and the `config.go` protocol comment are part of the deletion inventory. OpenAI tolerates and publicly encourages the `auth.json` reuse, but the ToS text does not. **`openai-chatgpt` is documented as resting on practice; `codex-cli` is the default of the pair.**
- **`antigravity`** — Google OAuth (`auth.GoogleAntigravityOAuthConfig`, `RefreshAccessToken`) against the Antigravity backend. **Deleted outright, greenfield — §2.4.** **This is the practice Google's §6 names and suspends accounts for — and it is the seeded default model on a fresh install (`pkg/config/defaults.go` → `antigravity/gemini-3-flash`).** Hermes removed the equivalent (PR #50492: *"Google now actively bans accounts … a ban can extend to the entire Google account"*); Goose deprecated it.

**Decision: delete the `antigravity` OAuth provider entirely (§2.4); the fresh-install seed carries NO default model** — `agents.defaults.default_model` is the zero value until onboarding writes the operator's explicit pick (§4 "no default model is pre-selected"; spec resolution #10; cross-spec X-39). The "Popular-tier API-key model" wording that used to stand here applies only to the illustrative `config/config.example.json`. Google's sanctioned route for third-party tools is the Gemini API or Vertex key, which stays. This is the one finding in this ADR that bears on the running release rather than the design, and it is flagged as such in ADR-066 §13 and §9 here.

### 2.3 The policy — as decided

*(Operator decisions 2026-08-22.)*

1. **API keys stay for every vendor, Anthropic and Google included.** That is the route both vendors name as the sanctioned one for third-party tools (`anthropic` via the Console key; `google` via the Gemini API key through the OpenAI-compatible endpoint already in use).
2. **Every Anthropic and Google *subscription* path is descoped.** Google: the `antigravity` OAuth provider — deleted, §2.4. Anthropic: no OAuth path ever existed; **`claude-cli` is descoped with the rest** — it exists to use a Claude subscription through the official binary, and since 2026-04-04 that login no longer draws on the subscription for third-party tools, so its reason to exist is gone. (It can return later as a plain "drive the vendor CLI" integration if there is a non-subscription case for it; that would be a new decision.)
3. **Subscription login is offered only where the vendor's own terms or an official vendor statement permit it**, cited in §2.1: **GitHub Copilot** via the official SDK/CLI; **xAI** via the published OAuth flow (ask xAI to list Omnipus, as the five named agents are); **OpenAI** via ChatGPT login, documented as practice-based.
4. **Never collect, store, proxy or refresh a vendor's consumer credential where the vendor prohibits it.**
5. **Prefer driving the vendor's own CLI as a subprocess over borrowing its token** wherever both exist.
6. The table in §2.1 is re-verified each release; a vendor that changes position moves tier, or is removed outright the way §2.4 removes `antigravity`.

### 2.4 `antigravity` — deleted, no trace, no backward compatibility

**Greenfield removal (operator direction 2026-08-22).** Inventory on this branch: 33 files reference it. Everything below is removed in one commit. **No code deals with antigravity afterwards in any form** — no alias, no shim, no migration, no retired-list row, no boot notification, **no error string in the source that names it**. After the commit the word does not occur in `pkg/`, `cmd/`, `src/`, `contracts/`, `config/`, or `docs/` outside historical decision records. **"No trace" is a property of the SOURCE, not of runtime output** (spec review CRIT-003, 2026-08-22): a stale `config.json` row whose id is `antigravity` is user-supplied data, and the generic unknown-provider path echoes that id back (`rest_onboarding.go`: *"unknown provider %q …"*; ADR-067 fixes `{"error":"unknown provider \"<id>\""}` as the wire text) — that echo is not a trace and must not be asserted against.

| Area | What goes |
|---|---|
| **Provider code** | `pkg/providers/antigravity_provider.go` (105 refs) + `_test.go`; the `case "antigravity"` in `factory_provider.go` and its test rows; `AntigravityModelInfo`, `FetchAntigravityModels` |
| **OAuth config** | `pkg/auth/oauth.go::GoogleAntigravityOAuthConfig` and the `OMNIPUS_GOOGLE_CLIENT_ID` / `OMNIPUS_GOOGLE_CLIENT_SECRET` env reads. **The file stays** — `OpenAIOAuthConfig`, `RequestDeviceCode`, `RefreshAccessToken` are used by `codex_provider.go` (verified). |
| **Default model** | `pkg/config/defaults.go` → the `antigravity/gemini-3-flash` provider template is removed and `agents.defaults.default_model` stays at its zero value (no replacement seed — X-39); `config.go` protocol comment; `config/config.example.json` shows a Popular-tier API-key example |
| **Wire contract (Constraint #8)** | the `antigravity` enum value in `contracts/components/schemas/ProbeProviderRequest.yaml` and its inbound copy `pkg/gateway/inboundschemas/ProbeProviderRequest.yaml`; regenerate `pkg/api/generated/openapi_types.gen.go`, `src/lib/api/generated/openapi-types.ts`, `schemas.ts`; commit spec + generated artifacts atomically |
| **Catalog allow-list** | `pkg/providers/catalog/catalog_test.go` "CLI executor / non-API-key ids" entry |
| **Docs** | `docs/ANTIGRAVITY_USAGE.md` deleted; mentions removed from `docs/providers.md`, `docs/configuration.md`, `docs/README.md`, `docs/migration/model-list-migration.md`, `docs/internal/provider-endpoint-audit-2026-06.md`, `docs/internal/design/provider-refactoring*.md` |
| **Kept deliberately** | historical decision records that mention it as history (ADR-031 and its review, ADR-059 spec reviews, the cli-minimization and workspace-rename specs, the turn-truncation root-cause note, ADR-066 and its reviews, and this ADR). Rewriting a past decision's text to erase a name is falsifying the record, not removing a trace. |

**Backward compatibility: none, and nothing antigravity-specific to provide it.** A `config.json` or agent entity that still names `antigravity` is simply an unknown provider id and takes the generic unknown-provider path that already exists (`rest_onboarding.go`: *"unknown provider %q and no endpoint override supplied"*). That path is not touched and never mentions antigravity.

**Exit proof:** no file under `pkg cmd src contracts config docs` contains the string `antigravity` except the historical decision records listed above (checked by a CI script under `scripts/` with its allow-list as a data file — not a Go test in `pkg/`, which would itself have to spell the name); `make verify-contracts` passes after regeneration; `go build` and `npm run typecheck` pass with the files gone. The same rule and script cover the id `claude-cli` (not the word "claude").

### 2.5 Not adopted

- **"Support everything that technically works, user's risk."** Google's remedy is account termination that can extend to the whole Google account; Omnipus would be the tool that caused it.
- **"API keys only."** Removes three shipped paths, two of which are sanctioned or tolerated.

## 3. D14 — Providers can be deleted; the default model is a Settings control

**Grounding** (this branch, 2026-08-22):
- **No provider can be deleted.** The REST surface is `GET /providers`, `PUT /providers/{id}`, `POST …/test`, `POST …/refresh-models`, `GET …/model-capabilities` — **no `DELETE`** (searched routes and `contracts/openapi.yaml`). `ProvidersSection.tsx` documents that `GET /providers` *"reports ALL of them forever as status: 'disconnected'"*; the only "Remove" control (`ProvidersSection.tsx` ~L550) removes a model slug from a manual list, not the provider.
- **The default model cannot be switched after onboarding.** `agents.defaults.model_name` is written at onboarding completion and only if empty (`pkg/gateway/gateway.go::…` at the two `ModelName == ""` guards); `agents.defaults.provider` is empty on the operator's install. No Settings control exists (searched `src/components/settings`, `src/lib/api.ts`). Removing the provider that default points at would fail at `instance.go` — *"provider %q not found in configured providers"* — with no UI path to fix it.

**Decision (D14).** A provider configuration **can be deleted** (`DELETE /providers/{id}`, Constraint #8), which clears its key and removes its row — the catalog entry remains available in the picker. **The default model is a first-class Settings control** (Settings → Providers, and reachable from the provider row), switchable at any time, with the current default shown on its provider's row. Removing a provider **always asks once** (operator decision 2026-08-22, after the second review flagged that an Undo toast would retain the deleted secret in memory): *"Remove OpenRouter? Its key will be deleted."* — listing dependent agents and, if it backs the default model, requiring a new default to be chosen inline before removal proceeds. **There is no Undo**: the secret is deleted the moment the user confirms and is retained nowhere. Agents that pointed at the removed provider show *no provider* in the agent list and are unusable until re-pointed — visible, not silent. The affordances are §5.

**D14.1 — the default model is stored as a PAIR, greenfield** *(spec review CRIT-001, coordinator decision 2026-08-22).* `agents.defaults.model_name` was an **alias** — `GetModelConfig` resolves it through `findMatches` against each `providers[]` entry's single `ModelName`/`Model` (`pkg/config/config.go`; `gateway.go`'s `ModelName == ""` guard documents "the alias is what `GetModelConfig` looks up by") — so a `(provider, model)` PUT had nowhere to land. Greenfield: a new config field **`agents.defaults.default_model: {provider, model}`** (contract `DefaultModel.yaml`) replaces it; `agents.defaults.model_name` and its alias semantics are **deleted**; `GetModelConfig` resolves the pair **exactly** (ADR-067's `(provider, model)` lookup). `PUT /api/v1/providers/default-model` writes the pair under the config lock and calls `TriggerReload` — the same precedent as the default-agent singleton (`rest_default_agent_singleton_test.go`). Proof is turn-time resolution (the session transcript's `provider`+`model`), never a config read-back.

**D14.2 — `DELETE` ordering, every step idempotent** *(spec review CRIT-004 / MAJ-016 / MAJ-019, coordinator decision 2026-08-22).* Under the config lock, the server **recomputes** dependents and `backs_default` (the `GET` values are advisory), then: (1) clear/re-point dependents in the agent entity store, listing them; (2) remove the provider row from `config.json`; (3) delete the credential `<id>_API_KEY` — `credentials.Store.Delete`'s `NotFoundError` is tolerated, absence is success; (4) emit the audit event `provider.deleted` carrying the credential **ref name** (never the value), the dependents count and any default change; (5) `TriggerReload`. A retry after a partial failure re-runs all five steps and succeeds; after a completed run a leftover credential with no provider row is impossible, and a **startup sweep** removes any orphaned `<id>_API_KEY` ref whose provider row is gone (greenfield housekeeping, not migration). The default-model PUT emits `provider.default_model.changed` the same way. Both routes are **admin-only** (`withAuth` → `RequireNotBypass`, the `adminWrap` chain `sandbox-config` uses), with **no** re-auth password re-type — a recorded exception to Spec-6 FR-12.2 (operator decision 2026-08-22).

---

## 4. UI surfaces, walked through

*Grounding.* Onboarding is three steps — name → password → **model key** (`src/routes/onboarding.tsx`, Spec-6 FR-12.3). Its provider picker already groups by **company → plan → region** from `src/lib/generated/providerCatalog.ts` (23 entries, emitted by `pkg/providers/catalog/gen`). Settings → Providers (`ProvidersSection.tsx`, `ProviderPickerSheet.tsx`, `ProviderRow.tsx`) holds exactly one config per catalog entry, a search-and-group picker sheet, a test-validation banner per row, an "Anthropic endpoint" chip for dual-protocol entries, and a *refresh models* action that calls the provider live. **The screens are the right shape already; what changes is what feeds them and five specific behaviours.**

**Onboarding — step 3.**
- The picker shows the **Popular** set first; *"Show all providers"* expands to the full registry (~190), grouped by company, searchable. The cloud-IAM exclusions are listed but not selectable, with the reason.
- Plan and region selectors stay; they now map to registry variants (`zai` / `zai-coding-plan` / `zhipuai` …). The endpoint-format toggle stays only for providers the override file marks dual-protocol.
- **Subscription sign-in is offered in onboarding too** (operator decision 2026-08-22, reversing the first draft of this section): for the providers whose vendor permits it — xAI, OpenAI, GitHub Copilot — step 3 presents *Sign in* and *Use an API key* as a choice **per provider**, not as an extra step, so the flow stays three steps. Anthropic and Google present the key path only.
- The step ends with a *working* (provider, model) pair: model picked from the catalog (no live list), validated by the probe, which now chooses its probe model from the catalog. **No default model is pre-selected** — the old `antigravity/gemini-3-flash` seed is gone and nothing replaces it silently; the user picks.

**Settings → Providers.**
- *Refresh models* becomes **"Check with my account"** (ADR-067 §4.3): intersects the live `/models` with the catalog, greys out models this key cannot use, and flags catalog-unknown models with *limits unknown*.
- Rows for Anthropic and Google show **API key only** — no sign-in option exists for them (§2.3). Copilot, xAI and OpenAI rows gain a *Sign in* alternative to the key, each with the vendor's own flow.
- Each configured provider row shows its **effective limits per model** on expand (window · output · image · PDF), with the source of the window (`operator | live | catalog | floor` — ADR-066 D8 "learned" was not adopted; `ContextWindowSource.yaml` is owned by ADR-066's spec) — the ADR-066 D9 visibility requirement, placed where the operator already looks.

**Settings → Models** (ADR-066 D9; the canonical user-facing name — ADR-066's spec adopts it and bakes the path *Settings → Models → Model overrides* into its refusal copy; cross-spec X-37): the global default context window with its source, and the override. The **default-model control lives on Settings → Providers only** (§5 item 5) — not duplicated here. **Agent form**: the per-agent override field, clamped to the model's capability (ADR-066 D2), shown with the clamp when it bites.

**Chat**: the emptied-result mark renders only with Verbose chat on (ADR-066 §12) — no other change.

## 5. UX review at 190 providers — verdict FAIL as-is; what changes

*Review run 2026-08-22 with the `elicify-ui-ux-design` checklist (cognitive load, hierarchy, accessibility, happy-path friction, onboarding, forms, settings, empty/error states) against `onboarding.tsx`, `ProvidersSection.tsx`, `ProviderPickerSheet.tsx`, `ProviderRow.tsx`, `ReAuthDialog.tsx`, `model-selector.tsx`. Findings carry the threshold or law they violate; file:line in the review transcript.*

**Why it fails at 190.** Three assumptions built for 23: (1) the onboarding company picker is a **flat tile grid of every company** (`onboarding.tsx` ~L1249), ordered only by a three-name priority list — at ~200 companies that is 60+ rows of tiles before search (Hick's law; happy-path rule: >25 options → searchable, not visible selectors); (2) the Settings picker sheet renders **every entry as one button in one scroll** (`ProviderPickerSheet.tsx` ~L89), no virtualisation, no Popular, no letter jump — ~215 buttons in a `max-w-lg` sheet; (3) **no concept of Popular vs everything** anywhere. Plus the two capability gaps: no delete (config sheet footer is Refresh / Test / Cancel / Save), no default-model control.

**Decisions.**

1. **One shared provider picker** (onboarding step 3 and the Settings sheet use the same component): a stable band of **12 Popular tiles** (amended 2026-08-25, catalog repo commit `b50f5a6`: twelve, usage-backed — `openai, anthropic, google, openrouter, deepseek, zai, minimax, moonshotai, alibaba, xai, mistral, ollama`; groq demoted, ollama promoted), then *Recently used*, then one search field over company / plan / region / alias, then *All providers* **collapsed until search has text or the user expands it** — a virtualised, letter-grouped list (row = company → variant subtitle → protocol chip). **Unsupported providers are shown disabled with the reason**, never hidden (hidden options generate "where is Bedrock?" tickets). *Custom endpoint* is the permanent last row (serial-position). Built on cmdk `Command` like the existing `ModelSelector`, so typeahead and arrow keys come free (WCAG 2.1.1).
2. **Auth method is a per-provider segmented control inside the existing second-level panel** (where plan and region already are): `[ Sign in with xAI ] [ API key ]`, defaulting to *Sign in* where the vendor sanctions it, absent for Anthropic and Google. No fourth step; the three-step tracker stays. The probe runs identically after either.
3. **Plan / region selectors stay** (they are correct `aria-pressed` groups); region **defaults to the value inferred from browser locale**, stated in copy (*"Detected: International — change"*), so a Chinese vendor's 3 plans × 3 regions does not present nine equal buttons cold.
4. **Model selection** (no pre-selection, per operator): the catalog list is ordered **by vendor group, then release date descending**; ≤3 models per provider carry a *Recommended for chat* chip (tool-calling, ≥128k window) — a hint, not a selection; `ModelSelector` gains virtualisation above 100 items (OpenRouter lists 359). The field is labelled *"Model for your first agent"* — "default model" is a system concept the user has not met yet.
5. **Default model card** is the first thing on Settings → Providers: `provider · model · window · source`, with *Change* opening the selector filtered to **connected** providers. Also reachable from the provider row. (Backed by a `PUT` on the default — Constraint #8.)
6. **Remove provider**: a text-tier destructive button at the config sheet's footer-left, backed by `DELETE /providers/{id}`. **Always confirms once** — *"Remove OpenRouter? Its key will be deleted."* — listing dependent agents when there are any; when the provider backs the default model, the dialog **requires picking a new default inline** before removal proceeds (D14, §3). **No Undo toast**: an Undo would keep the deleted key in memory for its duration; the secret is gone the moment the user confirms. One extra click, no secret retained.
7. **Sheet close must not discard a typed key** (`handleClose` clears the draft on Esc/overlay today): keep the draft until explicit Cancel; on Esc/overlay with a dirty draft, stay open with inline *"Discard key?"*.
8. **Row states** gain `signed-in as …` and `session expired` alongside Connected / Error / Not configured; the row's *Edit* reads *Manage* for sign-in providers; `ReAuthDialog` extends to OAuth expiry.

Already correct and kept: probe and finish errors as `role="alert" aria-live="assertive"` with the raw upstream error shown under the friendly one; key input autofocus; Radix `Dialog`/`Sheet` focus trap and Esc; company group headers only when a company has ≥2 configured variants (the configured list stays short at 190 — the picker is the problem, not the rows).

**Proposed information architecture.**

```
Provider picker (shared)                 Settings → Providers
┌ Connect a provider ──────────────┐     ┌ Providers          [+ Connect a provider] ┐
│ POPULAR                           │     │ ┌ Default model ───────────────────────┐ │
│ [OpenAI][Anthropic][OpenRouter]   │     │ │ OpenRouter · z-ai/glm-5.2            │ │
│ [Google][xAI][Groq][Mistral][…]   │     │ │ 1,048,576 · catalog         [Change] │ │
│ RECENT · Z.ai Coding Plan         │     │ └──────────────────────────────────────┘ │
│ [🔍 Search 190 providers…      ]  │     │ OpenRouter  ● Connected · live   [Edit] │
│ ▸ All providers (182)             │     │ xAI         ● Signed in as …   [Manage] │
│   A  Alibaba · Coding Plan · CN   │     │ Z.ai ── Coding Plan · Intl       [Edit] │
│      Amazon Bedrock — needs       │     │      ── Pay-as-you-go · China    [Edit] │
│      request signing ⓘ (disabled) │     │ sheet footer:                           │
│   …                               │     │ [Remove provider]      [Cancel] [Save]  │
│   Custom endpoint             →   │     └─────────────────────────────────────────┘
└───────────────────────────────────┘
```

**Top three by impact:** the Popular-first / search-everything picker (one component, both surfaces); the default-model card plus Remove provider with its confirm-and-repoint dialog; the auth-method control inside the onboarding panel. `ModelSelector` is shared with the agent form, so its virtualisation benefits both.

## 6. Wire consequences (Constraint #8), corrected

1. `src/lib/generated/providerCatalog.ts` — a build-time file — **goes**. A catalog refreshed daily cannot be baked into the SPA bundle; the SPA reads the **`GET` providers-catalog endpoint** (ADR-067 §5) and caches it. The `gen/main.go` TS emission is deleted with it.
2. **`ProbeProviderRequest.yaml` carries a provider *enum* today** (it is where `antigravity` appears as a value). ADR-067 §5 says the provider field "stays a free string — no enum"; that was true of `Agent.yaml` but not of the probe request. With ~190 providers the enum cannot stand: **it becomes a free string validated at runtime against the catalog**, and the schema + generated Go/TS are regenerated in the same commit that deletes the `antigravity` value.

## 7. Contract impact (Constraint #8)

> **Ownership and landing order (cross-spec X-26, 2026-08-22).** ADR-067's spec **owns and commits the contract FILE edit for every shared schema** — `Provider.yaml` (incl. this ADR's `signed_in`/`expired`, `auth_method`, `account_label`, `dependents`, `backs_default`, `updated_at`), `Agent.yaml` (incl. `needs_model`), `ProbeProviderRequest.yaml` (incl. `auth`/`api_key`/`model`), `DefaultModel.yaml`, `EntitlementResponse.yaml`, and the `LLMError` code set in all **four** copies (`LLMError.yaml`, `LLMErrorReplay.yaml`, the inline `LLMError`/`LLMErrorReplay` blocks in `contracts/asyncapi.yaml`, incl. this ADR's `model_unassigned`), plus the `pkg/gateway/inboundschemas/` copies. This ADR owns the **semantics and copy** of the values it defines; handlers that cannot yet compute a field emit its zero value. **ADR-067 lands first, then this ADR** — there is no cycle.

- `DELETE /api/v1/providers/{id}` (D14) — new route; the response carries the dependent agents and whether the default model is affected, so the dialog in §5 item 6 is driven by data, not a second request.
- `GET`/`PUT /api/v1/providers/default-model` (D14.1) — new reserved route dispatched **before** `{id}`; body `DefaultModel.yaml` `{provider, model}`; `agents.defaults.model_name` deleted from the config schema; takes effect without restart.
- `POST /api/v1/providers/{id}/entitlement` (ADR-067; replaces `refresh-models`) is the wire name of *Check with my account*; its annotated-list response is contract-defined. `GET /providers/model-capabilities` is removed (ADR-067).
- Audit events `provider.deleted` and `provider.default_model.changed` (D14.2).
- `ProbeProviderRequest.yaml`: the provider enum becomes a free string validated against the catalog (§6); `api_key` becomes **optional** and `auth: api_key|sign_in` is added — for `sign_in` the probe uses the CLI's saved login (`codex`) / the Copilot CLI session and 400s only when neither is present; optional `model` makes the probe validate the operator's exact pick (spec review CRIT-002).
- `src/lib/generated/providerCatalog.ts` removed; the SPA reads ADR-067's providers-catalog `GET` endpoint (§6).
- Row state vocabulary is **one enumeration**: ADR-067's set plus `signed_in` / `expired` — `connected | disconnected | error | unknown-provider | signed_in | expired` (`Provider.yaml`), listed verbatim in both specs (§5 item 8).
- `Agent.yaml` gains `needs_model` (this ADR) beside ADR-067's `degraded_reason`; the `model_unassigned` `LLMError` code is evaluated at agent level before any turn starts, so it never competes with turn-time codes; `needs_provider` wins when both apply.
- The `antigravity` enum value and every generated artefact carrying it are removed in the deletion commit (§2.4).

## 8. Implementation tasks

1. Delete `antigravity` per the §2.4 checklist, including contract regeneration; the fresh-install seed carries no default model (§2.2, X-39).
2. Descope `claude-cli` (§2.3 item 2).
3. Subscription sign-in flows for OpenAI (`codex-cli` subprocess, `openai-chatgpt` saved-token HTTP) and GitHub Copilot (`github-copilot`: the official Copilot CLI driven as a subprocess, sign-in detected from the CLI's own login — specified in full by this ADR's spec, cross-spec X-14; no Go SDK module, Constraint #1), each citing §2.1; xAI stays key-only until xAI lists Omnipus (request filed). The catalog row's `cli_kind: codex | copilot` (ADR-067 field) selects the subprocess driver; `openai-chatgpt` is an HTTP transport and carries no `cli_kind`.
4. `DELETE /providers/{id}` and the default-model `PUT` (§7), with the confirm-and-repoint dialog (§3, §5 item 6).
5. The shared provider picker (§5 item 1) for onboarding step 3 and the Settings sheet; the auth-method control in the second-level panel (§5 item 2); locale-inferred region (§5 item 3).
6. Model selector ordering, *Recommended for chat* chip, virtualisation, and the "Model for your first agent" label (§5 item 4); the default-model card (§5 item 5).
7. Draft-key preservation on sheet close (§5 item 7); `signed-in` / `expired` row states and `ReAuthDialog` for OAuth expiry (§5 item 8).
8. Per-model effective limits and window source on the provider row (§4).

## 8a. Pass-2 review resolutions (2026-08-22)

- **MAJ-011 — dependents of a deleted provider.** Agents whose model used the deleted provider are left **without a model**: listed in the confirm dialog, shown as *"needs a model"* in the agent list, and refusing to run with a typed error until reassigned. Nothing is re-pointed silently.
- **MAJ-012 — D13 rules 3 and 4 reconciled; xAI gated.** Rule 4 reads *"never collect, store, proxy or refresh a consumer credential **where the vendor prohibits it**"* — OpenAI does not, so rule 3's ChatGPT login is consistent with it. **xAI sign-in ships only once xAI lists Omnipus** as it has the five named agents (request filed as a task); until then the xAI row is API-key only. No fallback flow that depends on tolerance.
- **MAJ-013 — `codex-cli`: both paths kept (operator decision 2026-08-22).** Two distinct providers, named for what they do: **`codex-cli`** = the official `codex` CLI driven as a subprocess (`codex_cli_provider.go`; the user signs in inside it; Omnipus never touches the token) and **`openai-chatgpt`** = the direct HTTP path that reads the CLI's `auth.json` token and calls `chatgpt.com/backend-api/codex` (`codex_cli_credentials.go`, `codex_provider.go`). The misnomer — today the id `codex-cli` dispatches to the HTTP path — is fixed by the rename. The picker labels the HTTP path *"relies on OpenAI's stated tolerance, not its written terms"*; the subprocess is the default when the user has not chosen. This makes "prefer subprocess" a default, not an enforced rule — accepted.
- **MAJ-015** — own exit proof below.

## 8b. Amendment 2026-08-23 — in-app OpenAI sign-in restored; xAI UI built ready-to-enable; Google re-verified (operator decision)

**Trigger.** The operator asked why Omnipus cannot offer a direct ChatGPT / xAI login the way the peer agents do. Re-research on 2026-08-23 against the peers' own documentation changed one decision and confirmed three.

**Evidence (all verified 2026-08-23 from primary docs).**

| Vendor | OpenClaw | OpenCode | Hermes | Vendor stance today |
|---|---|---|---|---|
| **OpenAI** | in-app OAuth (`--provider openai`, browser **and** `--device-code`); its docs state "OpenAI explicitly supports subscription OAuth usage in external tools" | "ChatGPT Plus/Pro" option opens the browser to authenticate | device-code flow, tokens in `~/.hermes/auth.json`; can import `~/.codex/auth.json` | **endorsed** (Altman 2026-05-01; OpenClaw docs) — the shared Codex login is the sanctioned shape; nobody holds a per-agent client id |
| **xAI** | native OAuth since v2026.5.16, xAI announced 2026-05-19; browser + device code; SuperGrok / X Premium | "SuperGrok via device-code OAuth or API key" | OAuth against `accounts.x.ai`, shipped with xAI 2026-05-15/16; SuperGrok / X Premium+ | **endorsed per named agent** — Omnipus needs its own registration (vault task filed 2026-08-23) |
| **Anthropic** | residual `setup-token` / reuse-Claude-CLI routes, own docs warn against them | **removed** in v1.3.0: "Anthropic explicitly prohibits this" | OAuth only for Max + purchased extra credits (burns the credits); Pro: no | **prohibited** (terms 2026-02-20) and **server-side blocked** since 2026-04-04 |
| **Google** | none: "Google ended consumer Gemini CLI Login with Google access on June 18, 2026"; Antigravity terms prohibit third-party access | API key / Vertex only | "no way to sign in with a consumer Gemini subscription" | **prohibited**; Feb-2026 mass suspensions incl. paying Ultra users |

**Decisions.**

1. **OpenAI: the in-app sign-in comes back, as a device-code flow Omnipus runs itself.** §2.3's earlier reading ("borrowing the Codex client id is an unsanctioned trick") is withdrawn — the shared Codex login is the form OpenAI itself endorses and every peer ships. `openai-chatgpt` therefore becomes: *Sign in* → Omnipus requests a device code, shows the verification link + short code, polls until approved, stores the resulting access + refresh token **in its own encrypted credential store** (never the vendor file), and refreshes it itself. Reading `~/.codex/auth.json` stays only as a read-only **import** ("Use my existing Codex login") — it is no longer the sole source. `codex-cli` (subprocess) is unchanged. The restored code is the `pkg/auth` device-code / PKCE / refresh flow deleted by T068-03 (`60ee2275`), re-introduced from `60ee2275^` and re-grounded on the new FRs; T068-01's grep gate drops those symbols from its removed-symbol list (the `antigravity` / `claude-cli` halves are untouched).
2. **Default of the OpenAI pair flips to `openai-chatgpt`** — it needs no installed CLI and is the one-click path the operator asked for; `codex-cli` remains selectable. FR-006's "stated tolerance" caveat is replaced by a neutral "uses your ChatGPT plan's included usage" line — the caveat described a situation that no longer holds.
3. **xAI: the UI is built now, gated on registration.** The same sign-in dialog (link + code, polling, signed-in/expired states, *Sign out*) is implemented for xAI behind the catalog's `auth_methods`; the `xai` row gains `sign_in` the day the client id exists (configuration, not a release). Until then the row is key-only and shows no forward-looking copy (Qualitative prohibition kept).
4. **Anthropic and Google: API key only, confirmed.** No sign-in control, no import of Claude Code / Gemini CLI logins — the peers' residual Anthropic routes are exactly what Anthropic's April enforcement blocks.

**Rule 4 of D13 is unchanged** — "never collect, store, proxy or refresh a consumer credential *where the vendor prohibits it*". OpenAI and xAI do not; Anthropic and Google do.

**Headless rule.** Both flows use the **device-code** grant only (verification link + code entered on any device) — never a localhost browser callback — because Omnipus routinely runs on a server the operator reaches through the SPA. The browser that opens the vendor page is the operator's, not the gateway host's.

**Security.** Tokens live in `credentials.json` under `<provider>_OAUTH` (AES-256-GCM); refresh tokens never cross the wire; `GET …/sign-in/status` and `GET /providers` expose only `state`, `account_label`, `expires_at`. `DELETE /providers/{id}` step 3 deletes the OAuth entry alongside `<id>_API_KEY`. A `POST …/sign-in` start is rate-limited like the auth endpoints (`withRateLimit`).

**Spec changes:** FR-006, FR-007, FR-008, FR-009 rewritten; FR-044–FR-049 added; T068-14 and T068-21 re-scoped; T068-32 (backend) and T068-33 (SPA sign-in dialog) added. Contract: `SignInStartResponse` gains `method: device_code` with `verification_url`, `user_code`, `device_auth_id`, `expires_at`, `interval_seconds`; `SignInStatus` gains `pending`; new `POST /providers/{id}/sign-in/poll` and `DELETE /providers/{id}/sign-in` (sign out).

## 9. Release flag and exit proof

**Bears on the running release:** the shipped `antigravity` OAuth provider is the practice Google's Antigravity terms §6 name and enforce with account suspension, and it is the fresh-install default model. Its removal (§2.4) precedes shipping the branch that carries ADR-066.

1. **No antigravity, no claude-cli** *(the OpenAI OAuth client is restored — §8b)* — a CI script under `scripts/` finds no file under `pkg cmd src contracts config docs` containing `antigravity` or the id `claude-cli` outside the allow-listed historical records, and `grep -rn 'RequestDeviceCode\|PollDeviceCodeOnce\|OpenAIOAuthConfig\|createCodexTokenSource\|createClaudeAuthProvider' pkg cmd` is empty; `make verify-contracts`, `go build` and `npm run typecheck` pass with the files gone. Runtime echo of a user-supplied id is out of scope (§2.4).
2. **Delete with guard** — `DELETE /providers/{id}` runs D14.2's five idempotent steps; with dependent agents the dialog lists them; when the provider backs the default model the request is refused (409) until a new default is supplied, so the last connected provider is never deletable; no secret survives the confirm; an injected failure after step 1 or 2 leaves a retryable state and a retry succeeds; an audit `provider.deleted` entry exists.
3. **Default model control** — present on Settings → Providers only (X-37); `PUT /providers/default-model` writes `agents.defaults.default_model {provider, model}`; the **next turn's session transcript** records that pair without restart; the provider row shows the current default; `agents.defaults.model_name` no longer exists in the config schema.
4. **Onboarding stays three steps** — step 3 presents Sign in / API key per provider for OpenAI and Copilot (xAI key-only until listed), key-only for Anthropic and Google, ends with a probe-validated (provider, model) pair **for the chosen auth method** (a sign-in probe uses the CLI's saved login) and no pre-selected model.
5. **Picker at 190** — Popular tiles first, then search, then *All providers* collapsed until searched or expanded, virtualised; unsupported providers visible and disabled with their reason; *Custom endpoint* last; arrow-key navigation works.
6. **Draft preserved** — closing the config sheet with a typed, unsaved key keeps the draft and asks before discarding.
7. **Subscription states** — a sign-in provider row shows *signed in as …*, and *session expired* routes to re-auth.
