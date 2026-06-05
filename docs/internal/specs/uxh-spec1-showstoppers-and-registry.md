# Spec-1 — v0.1 Showstopper Fixes + Tool-Registry Metadata Correction

**Status:** Draft — **revised after `/grill-spec` round 1** (BLOCK → resolves F-01…F-04 CRITICAL + folds F-05…F-22) · **Date:** 2026-06-03
**Source:** `docs/internal/architecture/ADR-018-tools-system-and-v01-showstopper-resolution.md` (grill-revised, owner-ratified)
**Ships:** FIRST — **unblocks PR #344** (the v0.1 UX-hardening epic, currently held off `main`). Spec-2 (tools categories/dangerous/config) and Spec-3 (ClawHub) ship independently and MUST NOT gate this spec.
**Surfaces:** SPA (React 19) + Go gateway/registry. Contract-first for any wire change (`contracts/openapi.yaml`).
**Tests:** vitest + go test (deterministic, no wall-clock) **and Playwright e2e** (`tests/e2e/`) for every user-facing behavior.

> Phase-1 discovery was satisfied by ADR-018's interview + grill rounds; requirements below are the ratified decisions, not new assumptions.

---

## 1. Problem & Scope

The epic shipped CI-green but manual testing found showstoppers. This spec fixes the ones blocking the merge, **plus** the root cause of the worst one (the Security tool editor shows only system tools, double-listed, no allow/deny) — which is that `/api/v1/tools` exposes only the 41 `system.*` tools, not the general builtins.

**In scope:** D-A1/A2 (register general-builtin **metadata** centrally; fix the `gateway.go:689` deps-registry drop; flatten `ToolPolicyEditor`), D-B1 (provider-green), D-B2+D-B8 (restart banner + hot-reload-always-on + post-onboarding re-baseline), D-B5 (font-size), D-B6 (About), D-B7 (MCP→Sheet), D-B8 (remove no-auth from UI), and the **time-boxed** D-B3 (WhatsApp QR) + D-B4 (token TTL).
**Out of scope (Spec-2/3):** the `dangerous` flag, purpose-category re-taxonomy, per-tool config datamodel, ClawHub. **Hard invariant:** per-agent tool **execution instances stay per-agent** (workspace-bound) — this spec only changes **metadata** exposure.

---

## 2. Existing Codebase Context

### Symbols Involved
| Symbol | Role | Context |
|---|---|---|
| `BuiltinRegistry` (`pkg/tools/builtin_registry.go`) | **extend** | Add general-builtin metadata; today populated system-only (`gateway.go:614`) |
| `systools.AllTools(nil,nil)` | **call/extend** | Already builds a deps-free metadata catalog → mirror for general builtins |
| `gateway.go:689` central-registry re-population | **fix (bug)** | Re-created live-deps registry is dropped (passed by value at :630); `restAPI.builtinRegistry` keeps the nil-deps copy |
| `HandleToolsRegistry` (`rest_tool_registry.go:122`) | **unchanged** | Already reads `builtinRegistry.All()`; will return more once metadata lands |
| `ToolPolicyEditor.tsx` | **modify** | Remove `category==='system'`→Advanced split + double-listing; render flat allow/ask/deny over all tools |
| `SecuritySection.tsx`, `ToolsAndPermissions.tsx` | **modify** | Consume the corrected registry |
| `HandleProviders` (`rest.go:3387`, status hard-coded `Connected` @:3451) | **modify** | Status = key resolves (+ optional cached test) |
| `rest_pending_restart.go` (`RestartGatedKeys` @:36, `normalizeUsersForDiff`), `appliedConfig` (`rest.go:84-90`, once-at-boot) | **modify** | Re-baseline post-onboarding; reconcile `gateway.users`; restart-key set |
| `GatewaySection.tsx` (`auth_mode` @:67, `hot_reload` toggle @:296) | **modify** | Remove no-auth UI; force hot-reload, remove toggle |
| `defaults.go:363` (`HotReload:false`), `gateway.go:779` (reload watcher gate) | **modify** | Hot-reload default on |
| `ProfileSection.tsx:128` (`--user-font-size` set, unconsumed) | **modify** + CSS | Wire root font-size |
| About panel (`SettingsScreen`/About), `src/assets/logo/omnipus-logo.svg` | **modify** | Logo, version, GitHub URL |
| `McpServerModal.tsx` (`Dialog`) | **modify** | → `Sheet` (slide-out) |
| WhatsApp pairing (`ChannelConfigPanel.tsx:260-360`, `pkg/channels/whatsapp_native`, `websocket.go` forwarding) | **investigate/fix** | QR frame flow |
| Token expiry (location TBD — NOT in `auth.go`) | **investigate/fix** | ~2-3 min expiry source |

### Impact Assessment
| Symbol Modified | Risk | Direct dependents |
|---|---|---|
| `BuiltinRegistry` metadata population | **MEDIUM** | `/tools` consumers (both tool editors); MUST NOT alter per-agent execution instancing |
| `gateway.go:689` deps-registry fix | **MEDIUM** | tools whose `Execute` reads live deps via the registry handler path |
| `appliedConfig` re-baseline | **MEDIUM** | every pending-restart consumer; new post-onboarding code path |
| `ToolPolicyEditor` flatten | **MEDIUM** | global Security + per-agent (two call-sites) |
| `HandleProviders` status | LOW | Providers tab |
| `hot_reload` default flip | LOW-MED | the reload watcher; restart-banner semantics |

### Relevant Execution Flows
| Flow | Relevance |
|---|---|
| Boot registry population (`gateway.go:614,689`) | Where general-builtin metadata must register + the deps-registry bug |
| Per-LLM-call tool assembly + `FilterToolsByPolicy` | Must stay per-agent + unchanged (invariant) |
| Onboarding → config write → pending-restart diff | D-B2 re-baseline |
| WS `whatsapp_pairing` subscribe→frame→render | D-B3 |

---

## 3. User Stories & Acceptance Criteria

### US-1 — Global tool editor shows ALL tools with allow/ask/deny (P0) `[D-A1/A2]`
A non-technical operator opens Settings → Security → Tool Access and sees **every** builtin tool (general + system), grouped, each settable to Allow/Ask/Deny — not an empty grid with system tools listed twice.
- **Why P0:** the reported showstopper; the global policy is useless if it can't see the tools it governs.
- **Independent test:** against the REAL `/api/v1/tools` (not a mock), the editor's primary grid is non-empty and contains general tools (`exec`, `read_file`, `web_search`).
- **AC1. Given** a default install, **When** the Tool Access editor renders, **Then** general builtins appear with allow/ask/deny controls and **no tool is listed twice**, and **no raw category key (`core`/`system`) is shown as a user-facing label** (F-02).
- **AC2. Given** the corrected registry, **When** `GET /api/v1/tools` is called, **Then** it returns both general and `system.*` builtin metadata (count > 41).
- **AC3 (metadata-only construction — F-05).** General-builtin metadata is obtained WITHOUT execution dependencies: each general builtin is constructed once with a **dummy workspace (`""`), `restrict=false`, deps-nil**, purely to read `Name()/Description()/Category()`; **constructor errors are logged-and-skipped, never fatal**; these metadata instances are **never `Execute()`d** via the central registry (F-17 invariant — the per-agent registry remains the demand-side execution source). The spec enumerates the exact general-builtin set + asserts the name set/count in a unit test.
- **AC4 (basic categories so the editor is usable — F-02).** Spec-1 assigns a **basic purpose category** to each general builtin via `Category()` overrides (`exec`→Code; `read_file`/`write_file`/`list_dir`/`edit_file`/`append_file`→File; `web_search`→Search; `web_fetch`→Web) so the grid groups sensibly; any tool still reporting `core` renders under a human-readable **"General"** label (add a `core`→"General" entry to `CATEGORY_LABELS`). The finer `system.*` split + the `dangerous` flag are **Spec-2**.
- **AC5 (concrete editor change — F-07).** The double-list is `ToolPolicyEditor.tsx` §3 (system disclosure) + §5 (the raw `tools.map` grid that re-lists ALL tools incl. system). Fix = **remove the system-hidden disclosure (§3) and de-duplicate so each tool renders once** in its category section; the raw per-tool grid is the per-category controls, not a second full list.
- **AC6 (per-agent scope — F-07).** `ToolPolicyEditor` is shared by SecuritySection (global), ToolsAndPermissions + CreateAgentModal (per-agent). The flatten applies to **all** call-sites (the per-agent editor had the same bug); the per-agent UX intentionally becomes the same flat list. A component test runs **per call-site**, not one shared mock.
- **AC7 (per-agent execution isolation — regression).** Two agents with different workspaces each `Execute()` `exec` only in their own workspace (per-agent instancing preserved — no shared instance; metadata instances are never executed).

### US-2 — Provider shows "Connected" only when actually configured (P0) `[D-B1]`
- **Why P0:** false "Connected" misleads the operator about what works.
- **AC1. Given** a provider listed in config with **no resolvable API key**, **When** the Providers tab renders, **Then** it shows a not-connected state (not green/Connected).
- **AC2. Given** a provider whose `api_key_ref` resolves to a non-empty credential, **Then** it shows Connected.

### US-3 — No spurious restart banner on a fresh install (P0) `[D-B2 + D-B8]` *(revised — grill F-01/F-03/F-08/F-18/F-20)*
- **Why P0:** every fresh install shows a false "restart to apply" — undermines trust.
- **Root cause + chosen fix (F-03/F-20):** `gateway.users` is **hot** — auth reads `GetConfig()` live per request (`auth.go:133` → `configSnapshotMiddleware`) and `register-admin` already calls `refreshConfigAndRewireServices`. So **remove `config.GatewayUsers` from `RestartGatedKeys`** (`rest_pending_restart.go:36`). Verify the onboarding-written **provider** entry is likewise hot (the gateway rewires providers on config change); if so it is not gated. **With no genuinely-gated key written during onboarding, the `appliedConfig` re-baseline is DROPPED entirely** — eliminating the F-01 data race (no new mutable `appliedConfig`, no `-race` exposure). `appliedConfig` stays the once-at-boot immutable snapshot.
- **AC1. Given** a just-completed onboarding, **When** Settings loads, **Then** no pending-restart banner shows (no onboarding-written key is in `RestartGatedKeys`).
- **AC2. Given** hot-reload is **always on** (default flipped in `defaults.go`), **When** a hot-reloadable key changes, **Then** it applies without a restart banner.
- **AC3. Given** a genuine restart-required key changes — one that is actually in `RestartGatedKeys` (`GatewayPort`, sandbox `mode`, preview listener host/port; **NOT** `bind_address`, which has no main-listener key today, F-18) — **Then** the banner shows for that key only.
- **AC4 (F-08).** If verification finds *any* genuinely-gated key onboarding writes, the spec re-opens the re-baseline question with a `sync.RWMutex`/`atomic.Pointer[config.Config]` guard and a `-race` test (F-01) — but the **default plan is the simpler no-re-baseline fix.**

### US-4 — Gateway settings: no "connect without authentication" option (P1) `[D-B8]`
- **AC1.** The Gateway tab does **not** offer `auth_mode: none`; token auth is the only UI option. (Backend config-file capability retained.)
- **AC2.** The hot-reload toggle is removed (always on).

### US-5 — Profile font-size actually scales the UI (P1) `[D-B5]`
- **AC1. Given** the operator changes the font-size slider, **Then** the app's base font-size changes (root consumes `var(--user-font-size)`), bounded min/max, and persists across reload.

### US-6 — About page is correct (P1) `[D-B6]`
- **AC1.** The real octopus logo (`omnipus-logo.svg`) renders (no placeholder "O"). **AC2.** GitHub link = `https://github.com/elicify-ai/omnipus`. **AC3.** Version shows the real build version (not `dev`).

### US-7 — MCP "Add server" uses the slide-out, like channels (P1) `[D-B7]`
- **AC1.** Adding/configuring an MCP server opens a `Sheet` (slide-out), consistent with `ChannelConfigPanel`; behavior unchanged.

### US-8 — WhatsApp QR appears on enable (P1, time-boxed) `[D-B3]`
- **Pre-task:** reproduce + root-cause the enable→`whatsapp_pairing`-frame flow on a controlled instance. **If the root cause is shallow, fix in this spec; if deep, drop to a tracked follow-up so it doesn't hold PR #344.**
- **AC1. Given** a default (native) build with WhatsApp enabled+saved, **When** the Configure panel is open, **Then** the QR renders within the pairing timeout (no perpetual spinner, no silence).

### US-9 — Session does not expire within minutes (P1, time-boxed) `[D-B4]`
- **Pre-task:** locate the actual expiry mechanism (JWT exp / session sweep / client refresh gap — NOT found in `auth.go`). **AC1.** A logged-in admin session remains valid for a sane duration (≥ the configured session length) without spurious 401s on `/config`,`/agents`.

**Edge cases:** registry with 0 MCP tools (US-1 still shows builtins); a provider in config with an *inline* key vs a `_ref` (US-2 both → resolved); onboarding that writes only a provider (no admin) — banner still clean; font-size at min/max bounds; About when version ldflag is absent (fallback string, not `dev`); WhatsApp on a `lite`/excluded-arch build (`native_available:false` → capability hint, not a broken QR).

---

## 4. Behavioral Contract
- When the Tool Access editor renders, the system shows **all** builtin tools once each with Allow/Ask/Deny.
- When a provider has no resolvable key, the system shows it as not-connected.
- When only onboarding/hot keys changed, the system shows **no** restart banner; when a restart-required key changed, it shows the banner for that key.
- When font-size changes, the base UI font scales and persists.
- When WhatsApp pairing is pending/ready/linked/failed, the system shows the matching state (never silence) — subject to the D-B3 time-box.

## 5. Explicit Non-Behaviors
- The system must **NOT** move tool **execution** to a shared central instance — per-agent workspace-bound instancing is preserved (sandbox isolation).
- The system must **NOT** render the `scope` field (`system/core/general`) as a user-facing grouping (would resurface the "system" labels; Spec-2 owns scope's fate — here just don't show it).
- The system must **NOT** remove the backend no-auth capability — only the UI option.
- The system must **NOT** introduce the `dangerous` flag / categories / per-tool config (Spec-2) or ClawHub browse (Spec-3).
- The system must **NOT** edit `generated/` by hand; wire changes go through `contracts/`.

## 6. BDD Scenarios (representative; full set in the traceability matrix)
```
Scenario: General builtins appear in the global tool editor (Happy Path) — Traces to: US-1 / AC1
  Given a default install with the corrected builtin registry
  When the operator opens Settings → Security → Tool Access
  Then the primary grid contains "exec", "read_file" and "web_search"
  And no tool name appears more than once

Scenario: /tools returns general + system metadata (Happy Path) — Traces to: US-1 / AC2
  Given the gateway booted with the metadata correction
  When a client GETs /api/v1/tools
  Then the response contains general-builtin entries AND system.* entries
  And the total count is greater than 41

Scenario: Per-agent exec stays workspace-isolated (Edge/Regression) — Traces to: US-1 / AC3
  Given agent A (workspace /a) and agent B (workspace /b)
  When A executes exec and B executes exec
  Then A's exec is bound to /a and B's to /b (distinct instances)

Scenario: Unconfigured provider is not green (Happy Path) — Traces to: US-2 / AC1
  Given a provider in config whose api_key_ref resolves empty
  When the Providers tab renders
  Then that provider is shown not-connected

Scenario: Fresh install shows no restart banner (Happy Path) — Traces to: US-3 / AC1
  Given onboarding just completed (wrote gateway.users + a provider)
  When Settings loads
  Then no pending-restart banner is shown

Scenario: Restart-required key shows the banner (Alternate) — Traces to: US-3 / AC3
  Given hot-reload is on
  When the gateway port (a key actually in RestartGatedKeys) is changed
  Then the restart banner shows for that key only

Scenario: No no-auth option in the UI (Happy) — Traces to: US-4 / AC1
  Given the Gateway settings tab
  When it renders
  Then there is no "connect without authentication" / auth_mode:none control and no hot-reload toggle

Scenario: Unauthenticated-mode warning banner (Error/Security) — Traces to: US-4 / FR-107b
  Given the backend booted with auth_mode:none (set via config.json)
  When the SPA loads
  Then a persistent "unauthenticated access is enabled" banner is shown

Scenario: Font-size scales the UI (Happy) — Traces to: US-5 / AC1
  Given the operator sets the font-size slider to 18px
  When the value is applied
  Then getComputedStyle(html).fontSize is 18px and persists across reload

Scenario: About page is correct (Happy) — Traces to: US-6 / AC1-3
  Given the About tab
  When it renders
  Then the octopus logo image is shown, the GitHub link points at the canonical repo, and the version is the build version (not "dev")

Scenario: MCP add server opens a slide-out (Happy) — Traces to: US-7 / AC1
  Given the MCP servers view
  When the operator clicks Add server
  Then a slide-out Sheet opens (focus-trapped, ESC dismisses, focus restored on close)

Scenario: Provider with inline key is connected (Alternate) — Traces to: US-2 / AC2 (F-15)
  Given a provider configured with an inline API key (not a _ref)
  When the Providers tab renders
  Then it shows Connected

Scenario: WhatsApp unavailable on a lite build (Error/Edge) — Traces to: US-8 / edge (F-22)
  Given a lite/excluded-arch build (native_available:false)
  When the operator opens Configure WhatsApp
  Then a "not available on this build" capability hint shows (no hung QR)
```

### Test datasets (boundary/edge/error)
| Dataset | Inputs | Expected | Traces |
|---|---|---|---|
| Font-size bounds | 11 (min-1), 12 (min), 18, 20 (max), 21 (max+1) | clamps to 12 / 12 / 18 / 20 / 20; layout intact at 12 & 20 | US-5 |
| Provider status | `_ref` resolves non-empty / `_ref` resolves empty / inline key set / no key | Connected / Disconnected / Connected / Disconnected | US-2 |
| Tool count | default install / +1 MCP server | `/tools` > 41 incl. general; +MCP entries | US-1 |
| Restart keys | `gateway.users` change / `port` change / sandbox `mode` change | no banner / banner / banner | US-3 |
| WhatsApp build | native_available:true / false | QR flow / capability hint | US-8 |

## 7. TDD Plan (unit-first, then integration, then E2E)

| Order | Test | Level | Traces | Notes |
|---|---|---|---|---|
| 1 | `builtin_registry_metadata_test.go` — general builtins registered as metadata; count>41; per-agent instancing unaffected | Unit (go) | US-1/AC2,AC3 | assert `systools`-style catalog incl. general tools |
| 2 | `gateway_deps_registry_test.go` — the re-populated live-deps registry reaches `restAPI.builtinRegistry` (the :689 fix) | Unit (go) | US-1 | guards the dropped-registry bug |
| 3 | `HandleToolsRegistry` returns general+system | Integration (go) | US-1/AC2 | real registry, not mock |
| 4 | `HandleProviders` status = key-resolves | Unit/Integration (go) | US-2 | unconfigured→Disconnected; configured→Connected |
| 5 | `pending_restart_test.go` — onboarding keys not flagged; appliedConfig re-baselined post-onboarding; restart-key set | Unit/Integration (go) | US-3 | deterministic, injected config |
| 6 | `ToolPolicyEditor.test.tsx` — flat list, all tools once, allow/ask/deny, no system-hidden/double-list, **no raw `core`/`system` label**; **mock uses the REAL category values the backend emits (`core` for un-recategorized) — not `file` (F-21)**; run **per call-site** (global + per-agent, F-07) | Component (vitest) | US-1/AC1,AC4,AC5,AC6 | |
| 7 | `ProvidersSection.test.tsx` — not-green when unconfigured | Component | US-2 | |
| 8 | `RestartBanner / Settings` — no banner on fresh-install fixture | Component | US-3 | |
| 9 | `GatewaySection.test.tsx` — no `auth_mode:none` option; no hot-reload toggle | Component | US-4 | |
| 10 | `ProfileSection.test.tsx` — font-size sets root; bounds; persists | Component | US-5 | |
| 11 | `About.test.tsx` — logo `<img>`, github=elicify-ai, version≠dev | Component | US-6 | |
| 12 | `McpServerModal.test.tsx` — renders in a `Sheet` | Component | US-7 | |
| **E2E-1** | `tests/e2e/security-tools.spec.ts` — **boots the real gateway; asserts the Tool Access grid is NON-EMPTY, contains `exec`/`web_search`, no tool double-listed, AND no user-facing category label equals a raw key (`core`/`system`)** (F-11 strengthening — the bare non-empty assertion would pass on the ugly `core`-bucket). **Runs only after Wave 0 integrates** (depends on the corrected `/tools`). | **E2E (Playwright)** | US-1/AC1,AC2,AC4 | **the test that would have caught the regression** |
| **E2E-2** | `tests/e2e/providers.spec.ts` — unconfigured provider not green | E2E | US-2 | |
| **E2E-3** | `tests/e2e/fresh-install.spec.ts` — onboard → Settings shows no restart banner | E2E | US-3 | |
| **E2E-4** | `tests/e2e/settings-gateway.spec.ts` — no no-auth option; no hot-reload toggle | E2E | US-4 | |
| **E2E-5** | `tests/e2e/profile-fontsize.spec.ts` — slider changes root font-size | E2E | US-5 | |
| **E2E-6** | `tests/e2e/about.spec.ts` — logo renders, github link, version | E2E | US-6 | |
| **E2E-7** | `tests/e2e/mcp-add.spec.ts` — Add-server opens a slide-out Sheet | E2E | US-7 | |
| **E2E-8** | `tests/e2e/whatsapp-qr.spec.ts` (extend) — QR renders on enable | E2E | US-8 | conditional on D-B3 time-box |

**Regression:** all current `*.test.tsx`/`*_test.go`/`tests/e2e/*` for touched areas MUST stay green (update only for intended label/behavior changes). **New regression guard:** E2E-1 (real-data tool grid) is mandatory — the unit tests passed on mocked tools while production was broken; E2E-1 closes that gap.

## 8. Functional Requirements
- **FR-101** The central builtin registry MUST expose general-builtin **metadata** (name/description/category) via `/api/v1/tools`, in addition to `system.*`, via deps-free metadata-only construction (US-1/AC3); execution instances remain per-agent. The general builtins MUST carry a basic purpose `Category()` (US-1/AC4).
- **FR-102 (F-06 — hygiene, not the showstopper fix)** The `gateway.go:689` live-deps registry re-population MUST reach `restAPI.builtinRegistry` (today it is dropped — passed by value at :630 before the :689 reassign). **Note:** this has **no user-visible effect on `/tools`** (both registries hold the same 41 system tools today); US-1 is fixed solely by FR-101. Confirm whether anything executes via the central registry (`:686` "if ever routed"); if nothing does, this is latent-bug hardening — fix it but do not claim it fixes the regression.
- **FR-103** `ToolPolicyEditor` MUST render every builtin tool **exactly once** with Allow/Ask/Deny, grouped by a **human-readable category label** (no raw `core`/`system` keys); the system-hidden disclosure (§3) and the duplicate raw-grid listing (§5) MUST be removed/de-duplicated (US-1/AC5). The change applies to **all call-sites** (global + per-agent), tested per call-site (US-1/AC6).
- **FR-104** `/api/v1/providers` MUST report `Connected` only when the provider's API key resolves to a non-empty credential.
- **FR-105** A fresh onboarded install MUST show no pending-restart banner; `appliedConfig` MUST be re-baselined after onboarding completes.
- **FR-106** `hot_reload` MUST default on and have no UI toggle; the restart banner MUST surface only the authoritative restart-required key set.
- **FR-107** The Gateway UI MUST NOT offer `auth_mode: none`.
- **FR-107b (F-13 — security)** When the backend boots with `auth_mode: none` OR `dev_mode_bypass: true`, the SPA MUST show a **loud, persistent banner** (consistent with the existing one-time stderr WARN). Hiding the toggle MUST NOT hide the risk that the unauthenticated state can still be set via `config.json`.
- **FR-108 (F-12)** The font-size preference MUST scale the app's root font-size (`html { font-size: var(--user-font-size) }`) and persist, **bounded 12–20px**. The spec MUST state that this rescales the whole rem scale (Tailwind v4 — spacing/sizing included), not only text, and a visual check MUST confirm layout does not break at the 12px and 20px bounds.
- **FR-109 (F-10 reconciled)** The About page MUST show the real logo (`omnipus-logo.svg`), the GitHub URL **`https://github.com/elicify-ai/omnipus`** (owner-chosen public repo; the current code's `omnipus-ai/omnipus` is wrong), and the build-injected version. **Note:** the Go module path stays `github.com/dapicom-ai/omnipus` (go.mod) — that is the import path, unrelated to the public-repo *link*; do NOT change go.mod. Owner to confirm `elicify-ai` resolves to a live public repo before ship (else hide the link).
- **FR-110** MCP "Add server" MUST use a slide-out `Sheet`.
- **FR-111 (time-boxed)** WhatsApp QR MUST render on enable for a native build, or the work is deferred with a tracked issue. **Security ACs (F-14, ride to the follow-up if deferred):** the `whatsapp_pairing` WS frame is delivered ONLY to authenticated admin sessions; a consumed/expired QR is not re-served; pairing events are audit-logged (the QR is a linked-device credential).
- **FR-112 (time-boxed; likely DEFERRED out of Spec-1 — F-09)** `[FACT]` the session cookie MaxAge is **24h** (`SessionCookieMaxAge=86400`, `middleware/session_cookie.go:78`) — the ~2-3 min expiry is **NOT** the session cookie; it is the CSRF token TTL or a client-side refresh gap (`expires_at` in `src/lib/api.ts:725,752`). The pre-task MUST **locate the actual mechanism** before any fix; if it is client-side it belongs in a FE task with a deterministic test, NOT a backend AC in this merge-blocker spec. **Default: time-box it OUT of Spec-1** (it is P1) unless the mechanism is found cheaply.

## 9. Success Criteria
- **SC-101** `GET /api/v1/tools` on a default install returns > 41 entries including `exec`, `read_file`, `web_search`. (measurable)
- **SC-102** E2E-1 passes against the embedded SPA + real gateway (non-empty category grid).
- **SC-103** A provider with no key renders not-connected in the Providers tab (E2E-2).
- **SC-104** A fresh onboarded install shows zero restart banners (E2E-3).
- **SC-105** Font-size slider changes `getComputedStyle(html).fontSize` (E2E-5).
- **SC-106** No `auth_mode:none` control and no hot-reload toggle in the DOM (E2E-4).
- **SC-107** All quality gates green (gofmt, golangci-lint, **go test `-race`**, vitest, verify-contracts, full Playwright) on the Spec-1 branch.
- **SC-108 (F-19 observability)** Boot logs the general-builtin metadata count AND the total `/tools` count (today only the system count is logged at `gateway.go:696`), so a future registry regression is diagnosable from logs alone.

## 10. Traceability Matrix
| FR | US | BDD | Tests |
|---|---|---|---|
| FR-101/102 | US-1 | /tools returns general+system; per-agent exec | T1,T2,T3,E2E-1 |
| FR-103 | US-1 | general builtins in editor | T6,E2E-1 |
| FR-104 | US-2 | unconfigured not green | T4,T7,E2E-2 |
| FR-105/106 | US-3 | fresh install clean; restart key | T5,T8,E2E-3 |
| FR-107 | US-4 | no no-auth | T9,E2E-4 |
| FR-108 | US-5 | font-size scales | T10,E2E-5 |
| FR-109 | US-6 | about correct | T11,E2E-6 |
| FR-110 | US-7 | MCP sheet | T12,E2E-7 |
| FR-111 | US-8 | whatsapp QR | E2E-8 |
| FR-112 | US-9 | session TTL | (loc-then-test) |

---

## 11. Multi-agent parallel delivery plan

**Wave 0 — backend registry foundation (CRITICAL PATH, blocks US-1 frontend):**
- `backend-lead`: FR-101/102 — register general-builtin metadata centrally + fix `gateway.go:689` (with `qa-lead` for T1-T3).

**Wave 1 — three file-disjoint teams in parallel (start at minute 0; only the US-1 frontend waits on Wave 0):**
- **Team BE-fixes** `backend-lead`: FR-104 (providers status), FR-105/106 (remove `gateway.users` from `RestartGatedKeys`; flip `hot_reload` default — **no `appliedConfig` re-baseline**, F-01/F-20). Files: `pkg/gateway/rest.go` (providers handler), `rest_pending_restart.go`, `defaults.go`. **Does NOT touch `gateway.go`** — Wave 0 solely owns it (F-04 ownership fix).
- **Team FE-settings** `frontend-lead`: FR-107 (GatewaySection no-auth + toggle removal), FR-107b (auth-none/dev-bypass banner), FR-108 (ProfileSection font-size + CSS), FR-109 (About), FR-110 (McpServerModal→Sheet, with the F-16 a11y AC). Files: `settings/GatewaySection.tsx`, `settings/ProfileSection.tsx`, About panel, `skills/McpServerModal.tsx`, CSS. **Hot-reload default flip (FR-106) is done in `defaults.go:363` by this team's backend counterpart — NOT in `gateway.go`** (F-04 ownership fix).
- **Team FE-tools** `frontend-lead`: FR-103 (flatten `ToolPolicyEditor` + SecuritySection/ToolsAndPermissions consumers). **Depends on Wave 0** (needs the corrected `/tools` to verify) — can build against the contract immediately, integrate/verify after Wave 0 merges.
- **Team Investigate** `backend-lead` (+ `frontend-lead` for repro): FR-111 (WhatsApp QR repro+root-cause), FR-112 (locate token-expiry). **Time-boxed** — report root cause before committing scope; if deep, file tracked issue and drop from Spec-1.

**Wave 2 — QA + E2E (after each feature lands):**
- `qa-lead`: unit tests per team (T1-T12) authored alongside; then the **Playwright e2e suite** (E2E-1…E2E-7, E2E-8 conditional) against the embedded SPA.

**7-reviewer gate (MANDATORY):** run after EACH feature (before its branch merges) AND on the whole Spec-1 diff before the Spec-1 → `hotfix/v0.1.0` (PR #344) integration: the 6 `pr-review-toolkit` agents (parallel) + the `/grill-code` architect pass. Resolve every finding (fix or defer-with-issue) before merge.

**Parallelism summary (F-04 corrected):** Wave 0 + Team BE-fixes + Team FE-settings + Team Investigate run **fully in parallel from minute 0**; Team FE-tools + E2E-1 integrate **after Wave 0** (they need the corrected `/tools`). File ownership is now **genuinely disjoint**: `gateway.go` is **Wave-0-only** (registry); BE-fixes owns `rest.go` providers handler + `rest_pending_restart.go` + `defaults.go` (the hot-reload default flip — NOT `gateway.go`); the FE teams own different `settings/`, `shared/`, and `skills/` files. No two parallel branches share a file.

## 12. Holdout Evaluation (post-implementation; NOT in the matrix)
- H1: a non-technical tester opens Security → Tool Access and can set `exec` to "Ask" and `read_file` to "Allow" without seeing any tool twice or any empty section.
- H2: the tester finishes onboarding and sees no "restart to apply" message anywhere.
- H3: the tester sees only the providers they actually configured marked green.
- H4 (error): on a `lite` build the tester enabling WhatsApp sees a clear "not available on this build" hint, never a hung QR.
- H5 (error): changing `bind_address` shows a restart prompt; changing a user does not.
- H6 (edge): the font-size slider visibly changes text size and survives a reload.
- H7 (edge): two agents running `exec` cannot read each other's workspace files.

## 13. Ambiguities (for `/grill-spec`)
| Item | Likely assumption | To resolve |
|---|---|---|
| Authoritative restart-required key set | `bind_address`, `port`, sandbox `mode`, listener toggles | confirm against what the gateway actually re-binds (D-B2) |
| `gateway.users` hot vs restart | hot (remove from gated keys) | verify auth state isn't boot-cached |
| WhatsApp QR root cause | channel starts on enable; frame forwarded | repro decides scope (time-box) |
| Token-expiry source | JWT exp or client refresh gap | locate before fix |
| `scope` field rendering | not rendered (Spec-2 owns its fate) | confirm SPA hides it now |

**Handoff:** `/grill-spec docs/internal/specs/uxh-spec1-showstoppers-and-registry.md` → `/taskify`. Spec-2 + Spec-3 are separate files (next).
