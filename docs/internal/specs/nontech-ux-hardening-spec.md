# Spec — Non-Technical-User UX Hardening of Configuration Surfaces

**Status:** Revised after `/grill-spec` + live as-is verification (§0) — ready for `/taskify`
**Date:** 2026-06-03
**Driver:** 6-reviewer non-technical-user study (Schedules, Settings/Security, Channels) + a 7th agent-config review + an 8th tools/MCP/skills review, with **18 design decisions approved by the product owner as ASCII wireframes**. Stress-tested via two `/grill-spec` passes and re-verified against the live runtime (see §0); the schedule session model follows a Hermes-vs-OpenClaw study (D18).
**Surfaces:** SPA only (React 19 / Vite / Tailwind v4 / shadcn/ui). No REST/WS wire-contract change; one additive change to a hand-written SPA descriptor (`src/lib/channel-fields.ts`, already `not-wire-format`).
**Tests:** vitest + @testing-library, deterministic (no wall-clock). Each workstream ships as its own 7-reviewer-gated PR.

> Out of band: the study also surfaced a **gateway crash on WhatsApp reload-enable** (fixed in PR #313, merged to hotfix) and the agent-config bugs B-1/B-2 (in Workstream D below). The WhatsApp-QR "didn't render" finding was that crash, not a UI gap.

---

## 0. As-Is Verification Log (2026-06-03 — live runtime + code reads)

After `/grill-spec`, every load-bearing claim was re-verified against the running system and the source. **This section is authoritative and overrides any inline statement it contradicts.**

**Runtime evidence** — gateway booted from the real-SPA build, onboarded headless (openrouter / `z-ai/glm-5v-turbo`) on port 6400:
- `GET /api/v1/tools` → **41 tools, EVERY one `scope:"core"`, `category:"system"`, `source:"builtin"`**. **0 MCP tools** (none configured, so MCP-tool scope is verifiable only once a server exists — see E5).

**Disposition of grill findings (and corrections to the grill itself):**

| Grill ID | Verdict after as-is verification | Action in this revision |
|---|---|---|
| **F-01** (B-1 root cause) | **Spec was RIGHT; the grill was WRONG.** Live data: `system.*` tools carry `category:"system"` + `scope:"core"` — **not** `scope:"system"`. So `ToolsAndPermissions.tsx:217` (`scope==='system'`) **and** `SecuritySection.tsx:156` (`scope!=='system'`) BOTH fail to exclude → all 41 leak in **both** surfaces. (The spec's "global filters correctly" half was the only wrong part.) | US-D4 + §12 rewritten: the fix filters on **`category==='system'`** (reliable in the payload) in BOTH grids; test against the real payload, not a mock. |
| **F-02** (channels) | **Confirmed.** 13 descriptor keys incl `google-chat` (already has 6 fields, all with helpText). The six CJK channels (feishu/line/dingtalk/qq/wecom/weixin) are real with **partial** helpText (40–83%). `signal` is **absent**. | US-C1 enumerates the exact set + the CJK gap; US-C2 reframed (GChat descriptors already exist — only the `authGroup` picker is new). |
| **F-03** (helper field) | **Confirmed.** `ChannelField.helpText` already exists, used 48×. A new `helper` would duplicate it. | §2.2: **reuse `helpText`**; only `helpLink`/`advanced`/`authGroup` are genuinely new. |
| **F-04** (QR states) | **Confirmed.** Wire frame = **5** states `waiting\|code\|linked\|timeout\|error`. | US-C3 + §10 row 7 map the UX onto all 5 wire states. |
| **F-05** (policyMode) | **Confirmed.** Default is `'deny'` (safe) at `SecuritySection.tsx:277`; the risky `allow` toggle is at :453. | US-B2 line ref + framing fixed. |
| **F-06** (E6 skills) | **RESOLVED in our favor.** `loop.go:6102-6107`: when `SkillsFilter`+`ForcedSkills` is empty it `return nil` → **empty `Skills` == ZERO skills today**. "Opt-in, default none" is ALREADY the runtime semantics → E6 is genuinely **frontend-only, no migration**. | US-E6 caveat removed. |
| **F-07** (consolidation) | **Confirmed + located.** Local copies in `SecuritySection.tsx`: `CATEGORY_LABELS`(:45), `PolicyBadge`(:58), `groupByCategory`(:91). Shared canonicals: `@/lib/toolCategories` + `@/components/shared/PolicyBadge`. `ToolsAndPermissions` already uses the shared ones. | Preset-consolidation note corrected to the real N→1 collapse. |
| **F-08** (preset ids) | **Confirmed.** Real ids: `exec`, `browser.navigate/click/type/evaluate`, `read_file`/`write_file`/`list_dir`. **No `delete_file` tool exists.** | §2.1 overrides fixed (drop `delete`; use `write_file`). |
| **F-11** (trigger encoding) | **Confirmed constraint.** `ScheduleTrigger` kinds = `at` / `every` (`every_ms`, interval-only, **no time-of-day**) / `cron`. `ScheduleFormSheet.tsx` `every` UI = minutes/hours/days, N≥1, **no weekly/monthly, no time-of-day**. | US-A2 redefined: "Repeat … at HH:MM", weekly, monthly MUST emit a generated **`cron`** string; pure-interval Repeat uses `every`. |
| **F-13** (scheduled auto-deny) | **Valid.** Headless runs auto-DENY `ask` tools, so a Balanced-role scheduled task using `exec` silently no-ops. | §2.1 + US-A2: warn at schedule-create when the owner's policy would auto-deny a needed tool; holdout H8 added. |
| **F-16** (main session) | **Confirmed built** (`schedules.go:461`, id `sched-main-<owner>`, creates if missing). Concurrent `main` runs **share one transcript with no mutex**. | **RESOLVED by D18** — `main` is dropped (Hermes isolated-only); hazard eliminated at the source. Backend path deprecated. |
| **E5** (MCP in editors) | **CORRECTED twice:** `fetchBuiltinTools` is an alias for `fetchRegistryTools`, which calls `GET /tools` (`api.ts:1951-1956`) — the MCP-inclusive registry. The `tools-builtin` string is only a React-Query **cache key**, NOT the endpoint. So **both** editors already fetch MCP tools; there is **no query to swap**. | US-E5 reduced to presentation only: add per-server grouping + source badge in the global editor (per-agent already badges) + the M-6 persistence rule. |

**Owner decision (RESOLVED 2026-06-03) — `system.*` visibility = Advanced disclosure.** Both editors were *built to hide* `system.*` via a filter that is broken (keys on `scope==='system'`, but the tools are `scope:"core"`). The owner chose to **show `system.*` under a collapsed "Advanced / system tools" disclosure** (default `deny`, danger note) in BOTH the per-agent grid and global Security — NOT in the primary category list, and NOT fully hidden. The B-1 fix therefore **separates** system tools (filter `category==='system'`) into that disclosure rather than dropping them. MCP + general tools remain in the primary categories (E5).

---

## 1. Problem & Actors

**Actor:** a non-technical operator — small-business owner / office manager — self-hosting Omnipus. No knowledge of cron, "session", Landlock/seccomp, SSRF, RFC1918, regex, `system.*` tool ids, SOUL.md/HEARTBEAT.md, bearer tokens, or `0.0.0.0`.

**Problem:** every *configuration* surface exposes the developer's mental model and jargon directly — no plain-language layer, no progressive disclosure, no "where do I get this", and (in Security) no safe-default-with-warning. Four Security settings can silently weaken protection. The result: the app is functionally complete but **not operable by its target user**.

**In scope:** Schedules form + Command Center entry (A); Settings/Security IA + the 4 risky controls + tool-policy presets (B); Channel Configure panels + WhatsApp QR states (C); Agent config consistency + the shared preset model + 2 bugs (D).
**Out of scope:** backend behavior of channels/agents/scheduler beyond the 2 named bugs (B-1, B-2) and any field-label text the backend emits in errors (D10 maps them client-side); the Rooms/v0.3 redesign; new channels; chat UX.

---

## 2. Shared primitives (build once; used across A–D)

| Primitive | Purpose | Build on |
|---|---|---|
| `<AdvancedDisclosure>` | Reusable collapsible "Advanced" section with a safe-defaults summary | existing collapsed pattern `CreateAgentModal.tsx:278-307` |
| `<RiskySettingControl>` | Safe option + "Recommended" pill; selecting a risky value opens a consequence `AlertDialog` (safe = default button); a STANDING amber "This lowers your protection" badge persists while risky. **Props (F-G09): `copy` / `safeValue` / `onConfirm`** — no internal per-site branching; the stdio MCP gate reuses the *pattern*, not necessarily the component | `AlertDialog` (`src/components/ui/alert-dialog.tsx`); `SandboxProfileSelector.tsx:184-233` confirm pattern |
| `<ToolPolicyEditor>` | THE shared role-preset + collapsed-category tool editor (Cautious/Balanced/Full access), raw per-tool grid behind "Customize per tool (advanced)" | replaces the duplicated logic in `ToolsAndPermissions.tsx` (per-agent) + `GlobalToolPoliciesSection` in `SecuritySection.tsx` (global); uses `@/lib/toolCategories` |
| `HelperLink` *(LOCAL, not a shared primitive — M-11)* | One-line `helpText` + clickable `helpLink` under a field | single consumer (ChannelConfigPanel) → a local component, not abstracted |
| `NextRunPreview` *(LOCAL, not a shared primitive — M-11)* | Live human-readable "Next run: …" | single consumer (ScheduleFormSheet) → local; reuses `triggerSummary()` (`SchedulesList.tsx:42`) |

### 2.1 Role-preset → policy mapping (the single source of truth, used by B6 + D12)

`system.*` admin tools are **separated** (bug B-1) by filtering on **`category==='system'`** (verified reliable; `scope` is `"core"` for these tools, so the existing `scope`-based filter is the bug) into a collapsed **"Advanced / system tools"** disclosure (default `deny`, danger note) — per the owner decision in §0. Presets apply to the **user-facing categories**, enumerated from `CATEGORY_LABELS` (`toolCategories.ts:4`) minus `system` (M-3):

> **File & Code · Code Execution · Web & Search · Browser Automation · Communication · Task Management · Automation · Search & Discovery · Skills · Hardware (IoT)** — plus an **"Other"** bucket for any tool whose `category` is unset (`groupByCategory` → `'other'`). A preset always sets `default_policy`, which governs the Other bucket and any future uncategorised tool, so nothing is ungoverned. **Precisely (F-G06):** the "Advanced / system tools" disclosure renders the `groupByCategory` result whose key is exactly `'system'`; the primary grid renders all OTHER keys, including `'other'`.

**Collapsed-category summary for mixed policies (M-9):** when a category's tools resolve to different policies under a preset (e.g. Browser Automation under Balanced = `navigate`/`click`/`type` ask + `evaluate` deny), the category header shows a **"Mixed"** pill; expanding reveals the per-tool policies. A uniform category shows a single Allow/Ask/Deny pill.

Override keys use the **real registered tool ids** (verified via live `GET /api/v1/tools`):

| Role | Default policy | Overrides |
|---|---|---|
| **Cautious** | `ask` | (none — nothing runs without asking) |
| **Balanced** *(default)* | `allow` | `exec` = ask · `browser.navigate`/`browser.click`/`browser.type` = ask · `browser.evaluate` = deny · `write_file` = ask |
| **Full access** | `allow` | (none — allows everything unconditionally) |

> There is **no `delete_file` tool** in the registry — earlier "file write/delete" wording is dropped; `write_file` is the only mutating file tool to gate.

`Ask` semantics for a headless/scheduled run (no human): auto-DENY (unchanged existing behavior). **Surface this at TWO points:** (a) in the preset description, and (b) **at schedule-create time** — if the chosen owner agent's effective policy would auto-deny a tool the schedule needs, warn ("This agent will be asked to approve `exec`, but scheduled runs can't approve — it will be skipped"). Without (b), a novice on the default Balanced role schedules an `exec` task that silently no-ops every run (F-13).

### 2.2 `channel-fields.ts` metadata delta (additive; `ChannelField` is `not-wire-format`)

```ts
export interface ChannelField {
  key: string; label: string; type: ...; required: boolean; placeholder?: string;
  helpText?: string;                         // EXISTS already (used 48×) — REUSE for the one-line explanation
  helpLink?: { label: string; url: string }; // NEW: "where to get it" source link
  advanced?: boolean;                        // NEW: render under "Advanced"
  authGroup?: string;                        // NEW: mutually-exclusive auth method group (GChat)
}
```
**Do NOT add a `helper` field** — `helpText` already carries the plain explanation for 48 fields. The only new metadata is `helpLink`, `advanced`, `authGroup`. No generated/wire types change; no `make verify-contracts` impact.

**`helpLink.url` is security-constrained (M-4):** it MUST be a compile-time constant literal authored in `channel-fields.ts` (never user/runtime input), **`https://`-only**, and rendered `<a target="_blank" rel="noopener noreferrer">`. Add a unit assertion that every descriptor `helpLink.url` matches `^https://`. This forecloses `javascript:`/tabnabbing/stored-XSS on the new outbound-link surface.

---

## 3. Workstream A — Schedules  *(P0; self-contained; no shared-primitive dependency)*

**Components:** `src/components/command-center/ScheduleFormSheet.tsx`, `SchedulesList.tsx`.

### US-A1 — Simple-first schedule form (D1)
A novice sees only **Name, Message, When, Which agent**; Delivery/Timeout/Enabled — plus the optional "keep the full thread" toggle (the only remnant of session-mode after D18 drops `main`) — live under `<AdvancedDisclosure>` "Advanced options".
- **Why P0:** the form is the primary "automate something" surface and currently blocks novices with 8 flat fields.
- **Independent test:** render the sheet; assert the four primary fields are visible and the four advanced fields are NOT visible until "Advanced options" is expanded.
- **AC1. Given** a fresh form, **When** it renders, **Then** Name, Message, When, Which-agent are visible and Memory/Delivery/Timeout/Active are hidden.
- **AC2. Given** the form, **When** the user expands "Advanced options", **Then** Memory/Delivery/Timeout/Active appear.

### US-A2 — Friendly "When?" with live preview (D2)
Once / **Repeat** / **Custom** (raw cron, relabeled, with examples). A live `NextRunPreview` renders under the trigger. The backend `ScheduleTrigger` has three kinds — `at` (one-shot), `every` (`every_ms`, a pure interval, **no** time-of-day), `cron`.
- **Finite friendly grammar (M-1) — the friendly UI ONLY ever emits these shapes:**

  | Friendly selection | Emitted trigger |
  |---|---|
  | Once, at date/time | `{kind:'at', at:<ISO>}` |
  | Every N minutes/hours (no time) | `{kind:'every', every_ms:N×unit}` |
  | Every N days (no time) | `{kind:'every', every_ms:N×86400000}` |
  | Every day **at HH:MM** | `{kind:'cron', cron:'M H * * *'}` |
  | Every **week** on \<weekday\> at HH:MM | `{kind:'cron', cron:'M H * * D'}` |
  | Every **month** on day \<d\> at HH:MM | `{kind:'cron', cron:'M H d * *'}` |

  Anything outside this table (e.g. "every 3 days **at** 18:00", multiple weekdays, step ranges) is authorable **only** via **Custom**. The weekday / day-of-month widget shows only for the week / month selections.
- **Reverse parse on edit (M-1):** opening an existing schedule re-hydrates the friendly controls **only if** its trigger matches a grammar row (an `every` with a round unit, or a cron matching the three templates). **Any other/complex cron opens the Custom view** with the raw string shown verbatim — never silently rewritten.
- **Timezone (M-1, verified F-G05):** the scheduler clock is `realClock.Now() = time.Now()` (`pkg/cron/service.go:30`), i.e. the **gateway's local zone** — gronx evaluates against that local time, so the preview's "server time" label is accurate (caveat: a host configured to UTC shows UTC — the label means "the server's clock," not the user's browser zone). The form emits `at` as a **local-zone ISO string** for consistency with cron; the preview says "server time" for both.
- **Validation (M-8 — no new library, per §9):** friendly shapes are valid by construction. **Custom** runs a lightweight client-side **5-field structural check** (regex on field count/charset, not a cron library) to gate Save + hide the preview on malformed input; the **authoritative** check stays server-side (the scheduler's existing `gronx` parse), whose 4xx maps to the same inline error.
- **Why P0:** cron is the second hard wall; novices cannot author a recurrence.
- **AC1.** Repeat with a time-of-day or week/month unit → `{kind:'cron', cron:<generated>}`; preview shows the human recurrence + "server time".
- **AC2.** Repeat = every N minutes/hours/days with no time → `{kind:'every', every_ms:…}`.
- **AC3.** Custom `0 18 * * *` → preview renders the human equivalent + examples; a malformed string gates Save with an inline error.
- **AC3b (F-G10).** A structurally-valid-but-semantically-invalid cron (e.g. `99 99 * * *`) passes the client 5-field check but fails server validation: the preview shows "checking…" (not a misleading "Next run"), and the server 4xx maps to the same inline error.
- **AC4 (default).** A brand-new form defaults the trigger to **Once**.
- **AC5 (edit round-trip).** A schedule saved `{cron:'0 18 * * *'}` re-hydrates to "Every day at 6:00 PM"; a schedule saved `{cron:'*/5 9-17 * * 1-5'}` (outside the grammar) opens the Custom view with the raw string, and re-saving untouched leaves the stored trigger byte-identical.
- **O-3 note:** a scheduled run that auto-denies an `ask`-gated tool (F-13) should also emit a **run-time audit line** so an operator can later see the skip — confirm the cron fire path audits this; if not, file a backend follow-up (out of this UI sprint's code, tracked in its issue list).

### US-A3 — Plain Advanced wording + reassurance (D3, **D18**)
**Owner decision D18 — Hermes isolated-only model (resolves M-5).** Every scheduled run uses a **fresh isolated session** (its own transcript → no concurrent-write hazard), and the agent's **memory is shared across runs** — verified already true: `MemoryStore` is **workspace-scoped** (`context.go:129`, `NewMemoryStore(workspace)`), so an isolated scheduled session still reads/writes the agent's `memory/` via `remember`/`recall_memory`. So "remember across runs" is provided by **shared memory**, not a shared transcript. There is no `skip_memory` in Omnipus, so **no backend memory change is needed**.
- **"Use my main chat" is REMOVED** from the form; no UI path emits `session_mode:'main'`. The backend `SessionModeMain` path (`schedules.go:461`) becomes unreachable from the UI and is marked **deprecated** (removal tracked separately — out of this UI sprint).
- The primary form needs **no session-mode question at all** — the single behavior is "each run is fresh; your agent keeps its memory," stated in one reassurance line. An **Advanced** opt-in "Also keep the full conversation thread across runs" maps to `session_mode:'continue'` (kept because it is SAFE — a per-job named session + the existing skip-if-running overlap policy prevents self-overlap; it never cross-shares like `main` did).
- "Delivery" → labeled either/or with helper lines → `deliver` false/true. Plain Timeout. Owner pre-filled with the default agent (Mia). Card no longer shows the raw `session_mode` enum badge.
- **AC1.** The form does NOT offer "Use my main chat"; no submit path produces `session_mode:'main'`.
- **AC2.** Default submit → `session_mode:'isolated'`; the run reads/writes the agent's shared memory store.
- **AC3.** The Advanced "keep the full thread" opt-in → `session_mode:'continue'`.
- **AC4.** Owner defaults to the agent with `default:true`.
- **AC5.** A created schedule's card does not render the raw `session_mode` string.

**Edge cases:** Custom cron invalid → inline "couldn't read that schedule" + preview hidden, save gated; the time-of-day picker appears only when the Repeat shape will emit `cron`; switching Repeat↔Custom clears the other input (no silent carry-over). **`main` mode is dropped (D18)** — the concurrent-shared-transcript hazard (F-16) is eliminated at the source: with no `main` option, no two schedules share a transcript. The optional `continue` (Advanced) is per-job and guarded by skip-if-running, so it cannot interleave either.

---

## 4. Workstream B — Settings / Security  *(P0; depends on `<ToolPolicyEditor>` + `<RiskySettingControl>`)*

**Components:** `SecuritySection.tsx`, `DiagnosticsSection.tsx`, `GatewaySection.tsx`, `RestartBanner.tsx`.

### US-B1 — Two-layer IA: Health over Advanced (D4)
Promote the Security Score (`DiagnosticsSection`) to a permanent "Security health" header with 3-4 plain outcome-toggles; everything jargon-heavy (sandbox internals, SSRF, deny-regex, the tool grid, audit log) moves under ONE collapsed "Advanced / technical details" labeled safe-to-skip.
- **Independent test:** render Security tab; the Score + the 3-4 toggles are visible; Landlock/SSRF/regex strings are NOT in the document until "Advanced" is expanded.

### US-B2 — Safe-default + confirm-to-weaken + standing badge (D5)  **[highest-risk]**
The FOUR controls — `auth_mode` None (`GatewaySection.tsx:~183`), `bind_address` 0.0.0.0 (`~151`), Process-sandbox Off, and the Tool-Access `policyMode` toggle (`SecuritySection.tsx:453`; default is the **safe** `'deny'` at :277) — use `<RiskySettingControl>`.
- **Why P0:** a *user-initiated* switch to the unsafe value silently weakens protection; a non-expert cannot tell safe from dangerous. (Note: `policyMode` is safe-by-default today, so its standing badge fires only after a deliberate switch to `allow` — the wrap gates that switch.)
- **AC1. Given** auth=token (safe), **When** the user selects "None", **Then** an `AlertDialog` warns in plain language with "Keep login on" as the default button; selecting it cancels.
- **AC2. Given** the dialog, **When** the user confirms the weakening, **Then** the value changes AND a persistent "This lowers your protection" badge remains on the control.
- **AC3.** The safe option carries a "Recommended" pill in each of the four controls.
- **AC4 (badge source, F-09/F-10/F-G04).** The standing badge is a pure function of the **persisted** setting value, read from existing client-readable config queries — **named per control:** `auth_mode` ← `config.gateway.auth_mode` and `bind_address` ← `config.gateway.bind_address` (both from the `['config']` query / `fetchConfig`, `GatewaySection.tsx:50-52`); sandbox profile ← the same `['config']` query; `policyMode` ← the `['global-tool-policies']` query (`SecuritySection.tsx`). All four are already client-readable saved values → **no new wire type needed**. The badge therefore survives reload and fires for any setting already unsafe on load (incl. config-import).
- **AC5 (revert path, M-2).** Given a control showing the standing badge, when the user reselects the safe value, the badge **clears on save** (when the safe value is persisted) — not on mere local re-render. For the restart-gated settings (`auth_mode`, `bind_address`, sandbox) a separate neutral "applies after restart" note may show, but the protection badge tracks the **saved** value, not the pending-restart state — so it never claims you're still weakened after you've saved the fix.

### US-B3 — Global Tool Access via `<ToolPolicyEditor>` (D6)
Global tool policy uses the shared 3-role preset + collapsed categories; `system.*` tools appear only inside the **"Advanced / system tools"** disclosure (§0 decision; default deny). Mapping per §2.1.
- **AC1.** Selecting "Balanced" writes default `allow` with the §2.1 overrides; "Cautious" → default `ask`; "Full access" → default `allow`, no overrides.
- **AC2 (M-9).** A category whose tools have mixed resolved policies shows a "Mixed" pill; a uniform category shows a single policy pill.

### US-B4 — Score as control surface + plain restart banner (D4 cont.)
Each Score deduction (`IssueCard` `action_link`/`action_label`) links to its in-place fix; the populated state reassures ("You're protected. 1 thing could be stronger"). Restart banner → plain summary ("1 change waits; applies on next restart"); raw config diff + systemd/docker jargon behind "Technical details". Credential Vault header gains "Your keys are encrypted and stored only on this server — never sent anywhere."

**Edge cases:** all four risky settings already in their unsafe state on load → show the standing badge immediately, no retroactive dialog; Score = 100 → "You're protected" with no deduction list.

---

## 5. Workstream C — Channels  *(P1; independent)*

**Components:** `ChannelConfigPanel.tsx`, `src/lib/channel-fields.ts`.

### US-C1 — Per-field helper + source links + advanced collapse (D7)
Each credential field renders `<HelperLink>` (the existing `helpText` + a new clickable `helpLink`); a one-line "How to connect" intro per channel; `advanced:true` fields (Allow From, proxy, custom API, mention-only) collapse under "Advanced". Metadata per §2.2.
- **Exact channel set (13, verified):** `telegram, discord, slack, whatsapp, feishu, matrix, line, dingtalk, qq, wecom, irc, weixin, google-chat`. `signal` is NOT in the descriptor (skip). **`whatsapp`** has only 2 non-secret fields (`allow_from`, `group_trigger.mention_only`) and is shadowed by the native QR notice (US-C3) — give those 2 helpText, do not invent token fields.
- **helpText is partial today** (telegram/slack/matrix/google-chat = 100%; the six CJK channels 40–83%): the work is (a) ADD missing `helpText` on the CJK channels, (b) ADD `helpLink` where a real source URL exists, (c) mark `advanced` fields — without duplicating existing `helpText`.
- **AC1.** Telegram Bot Token already shows "Get from @BotFather" (`helpText`); ADD the clickable `helpLink`; "Allow From" moves under "Advanced".
- **AC2.** A field with no `helpLink` renders `helpText` only (no broken link).
- **AC3.** Every required field on the six CJK channels has `helpText` after this pass.

### US-C2 — Google Chat "pick one" (D8)
The `google-chat` descriptor already exists with all 6 fields + helpText (verified) — the **only new work** is the `authGroup` picker so the three auth fields aren't one confusing block: a "How do you want to connect?" radio (Webhook URL [simplest] / Service account); only the selected method's field(s) render; matches the backend OR-group.
- **AC1.** Webhook selected → only `webhook_url` shows; Service account selected → `service_account_json` + `service_account_file` show; the other group's fields are not submitted.
- **AC2 (switch rule, M-13/F-G08).** Switching method clears the deselected group's field **`useState` value** (mirrors Repeat↔Custom) — the test asserts the abandoned field's state is empty after switching (not merely excluded from the payload), so a switch-back cannot resurrect a stale secret.

### US-C3 — WhatsApp QR state machine (D9)
The wire frame (`asyncapi-types.ts:334`) carries **5** states `waiting | code | linked | timeout | error`. Map them to the UI by their **real wire names** (do not invent "ready"):
- `waiting` → spinner "Generating your QR code…"
- `code` → QR image + "WhatsApp → Settings → Linked Devices → Link a Device" + "refreshes every 20s"
- `linked` → success
- `timeout` → "QR expired — tap to get a fresh one" (Retry)
- `error` → "Pairing failed — tap to retry" (Retry)
- **Pre-task:** the crash that prevented the QR is fixed in PR #313 (merged) — confirm the live frame arrives on a native build; spec the states regardless.
- **AC1.** No frame → waiting. **AC2.** `code` → QR + steps. **AC3.** `linked` → success. **AC4.** `timeout` and `error` each render their own distinct copy, both offering Retry.

### US-C4 — Human-label errors + button clarity + a11y (D10)
Test/validation errors map raw `snake_case` → the field's human `label` ("Please fill in: Bot Token"). Keep Test/Save/Save&Enable; add "Test = check without saving" hint + a pass/fail Test result line. Fix missing `aria-describedby` on Configure dialogs.
- **AC1.** Test with an empty required field shows "Please fill in: <Label>", never `bot_token`.
- **AC2.** Configure dialog has an `aria-describedby` target.

---

## 6. Workstream D — Agent config  *(P1; depends on `<ToolPolicyEditor>`; do AFTER B's shared component)*

**Components:** `CreateAgentModal.tsx`, `AgentProfile.tsx`, `ToolsAndPermissions.tsx`, `SandboxProfileSelector.tsx`, `ShellDenyPatternsEditor.tsx`.

### US-D1 — Create-Agent consistency (D11)
Keep the blank form (no template gallery) but apply `<ToolPolicyEditor>` (role-preset), plain labels, and keep the existing temperature-in-Advanced collapse. New-agent default = **Balanced** (not `default_policy:'allow'`, `CreateAgentModal.tsx:~84`).

### US-D2 — One shared preset model (D12)
Replace the agent's four ad-hoc presets (`Unrestricted/Cautious/Standard/Minimal`, `ToolsAndPermissions.tsx:47-83`) with `<ToolPolicyEditor>` (Cautious/Balanced/Full access, §2.1). Preset-first; raw grid behind "Customize per tool (advanced)". The same component renders identically in global Security and every agent.
- **AC1.** Global and per-agent surfaces render the same three role labels + same category rollups.

### US-D3 — Sandbox + behavior plain-language
SandboxProfileSelector → 2-3 plain options ("Use recommended security (inherit global)" [Recommended pill] / "Stricter (workspace only)") with Host/Workspace+Net/Off + Landlock/kernel wording under "Advanced security"; KEEP the existing type-the-name confirm-to-weaken for Off; ADD the missing **standing warning badge** on the accordion header when a **weakened profile** (Workspace+Net / Off) is active. (F-G14: a shell-deny *pattern* HARDENS — shrinks the allow surface — so do NOT badge its mere presence as "weakened"; badge only an actual allow-carve-out that *widens* the surface, or drop the shell-deny clause from the badge trigger entirely.) ShellDenyPatterns (regex) stays under Advanced. Labels: "SOUL.md" → "Personality & instructions", "HEARTBEAT.md" → "Background tasks / periodic instructions" (.md framing demoted to Advanced); plain captions on temperature/top-p/steering/iterations/timeout.

### US-D4 — BUG B-1: `system.*` tools mis-placed in BOTH tool grids  **[P0 bug]**
**Verified root cause (live `GET /api/v1/tools`):** all 41 `system.*` tools carry `category:"system"` + `scope:"core"` — NOT `scope:"system"`. So `ToolsAndPermissions.tsx:217` (`if t.scope==='system' return false`) AND `SecuritySection.tsx:156` (`filter(t => t.scope!=='system')`) BOTH fail to exclude → all 41 admin tools land in the primary grid in BOTH the per-agent editor and global Security. (The earlier "global filters correctly" framing was wrong — both leak.) **Fix in the SPA: filter on `category==='system'`** (reliable in the payload) to MOVE these tools out of the primary categories into the collapsed **"Advanced / system tools"** disclosure (owner decision §0; default `deny` + danger note) in both grids. Verify against the real payload, not a mocked tool list.
- **AC1.** Opening Tools for a new custom agent shows only user-facing categories in the primary grid; the 41 `system.*` rows appear only inside the collapsed "Advanced / system tools" disclosure (default deny), not mixed into the main categories.
- **AC2.** The global Security tool editor behaves identically.
- **AC3.** The Advanced/system disclosure carries a danger note; expanding it is a deliberate action.

### US-D5 — BUG B-2: locked-agent tools panel silent 403  **[P1 bug]**
The per-agent Tools panel renders editable Allow/Ask/Deny + presets for LOCKED core agents; autosave then 403s. **Render the panel read-only** for locked agents (M-12: do NOT instead "suppress autosave" — that leaves controls interactive but silently drops edits, a new silent failure).
- **AC1.** Opening Tools on a locked core agent shows read-only controls and fires no write.

---

## 6.5 Workstream E — Tools / MCP / Skills  *(P1; depends on `<ToolPolicyEditor>`)*

**Components (full paths, F-G01):** `src/components/skills/McpServerModal.tsx`, `src/components/agents/MCPServerPicker.tsx`, `src/components/skills/SkillBrowser.tsx`, `src/components/settings/SkillTrustSection.tsx`, `src/components/skills/SkillsScreen.tsx`, `src/components/settings/SecuritySection.tsx`; **delete** `src/lib/agentToolPresets.ts` + `src/components/agents/ToolPickerPreset.tsx` + `src/components/agents/ToolGroupList.tsx`.

> **Preset glossary (F-G12) — two different "preset" systems:** **selection-presets** (`agentToolPresets.ts` etc.) are dead code, DELETED by US-E3. **Policy-presets** (`POLICY_PRESETS`, `ToolsAndPermissions.tsx:47-83`) are the live Cautious/Balanced/Full roles, REPLACED by `<ToolPolicyEditor>` (US-D2) — not deleted. Don't conflate them.

### US-E1 — MCP "Add server" fix + simple/advanced + stdio safety gate (D13)  **[contains a correctness BUG]**
- **BUG:** `McpServerModal.tsx` (`canSubmit = name && command`, `:55`) has **no URL field** — switching transport to SSE/HTTP never reveals one, so network MCP servers **cannot be added**. Replace the transport dropdown with "Connect via: A local program / A network address": the local path shows Command/Args/Env, the network path shows a **URL** field; relax the command-required gate for the URL path.
- Plain "what is this / Learn more" intro + helper; `command`/`args`/`env`/transport names under "Advanced".
- **Safety gate (non-negotiable):** stdio spawns an arbitrary local program → a confirm dialog ("this runs a program on your server") + a STANDING warning badge on the saved server (the `<RiskySettingControl>` pattern). `MCPServerPicker.tsx:43` hides the raw `transport` string.
- **Network URL validity (M-10):** "valid" = scheme `https://` (or `http://` only for `localhost`/loopback); other schemes rejected. A URL whose host is a **literal** internal/RFC1918/link-local address gets the same SSRF-class **caution** the spec flags in §1 — surfaced inline (visible warning, not a hard block). **F-G07:** the SPA check is a literal-IP/hostname heuristic (`10.` / `192.168.` / `172.16–31.` / `169.254.` / `localhost` / `*.local`) — the browser cannot resolve DNS, so a hostname that resolves privately at connect-time is NOT caught here; the **authoritative** SSRF guard is the backend connect path, not this hint.
- **AC1.** Network address → URL field shows + Add is enabled with a valid (https, or http-localhost) URL; an internal-address URL shows an SSRF caution. **AC2.** Local program → Command/Args/Env; Add opens the confirm. **AC3.** A saved stdio server shows a standing "runs a local program" badge.

### US-E2 — One tool editor in Security; Tools tab = read-only overview (D14)
- Security Tool Access uses `<ToolPolicyEditor>` (shared; presets + plain categories). The Skills→Tools tab becomes a plain-language, by-category "what your agents can do" summary (no raw `system.*`) with a "Manage permissions → Security" link.
- **AC1.** The Tools tab shows category outcomes + a link, not the raw 41-row grid; editing happens only in Security.

### US-E3 — Delete dead selection-preset code (D15)
- Delete `src/lib/agentToolPresets.ts`, `src/components/agents/ToolPickerPreset.tsx`, `src/components/agents/ToolGroupList.tsx` (unwired; a divergent third vocabulary). The live model has no per-agent tool-*selection* step.
- **AC1.** Build + `tsc -b` pass after removal; no dangling imports.

### US-E4 — Skills trust mounted + surfaced at install + error fix (D16)
- **The component ALREADY EXISTS (F-G02):** `src/components/settings/SkillTrustSection.tsx` (+ `SkillTrustSection.test.tsx`), imported nowhere today. The task is to **MOUNT it**, not build it: first read it and **confirm its existing tri-state + persistence match the Block / Warn-unverified [default] / Allow-all contract** (adapt if it diverges), then mount it in Settings→Security and wire trust-mode persistence. At install (`SkillBrowser`), show the skill's capabilities + an "unverified" notice + confirm. Replace the silent non-hash install-error swallow (`SkillBrowser.tsx:52`) with a toast.
- **AC1.** `SkillTrustSection` renders in Security. **AC2.** A failed (non-hash) install shows a toast, not silence. **AC3.** Installing surfaces a trust/confirm step.

### US-E5 — MCP tools governed in the shared editor, global + per-agent (D13 cont.)
- MCP tools appear in `GET /tools` with `source:'mcp'` (`rest.go:122-126`). **CORRECTED:** both editors already fetch `/tools` — `SecuritySection`'s `fetchBuiltinTools` is an alias of `fetchRegistryTools` → `GET /tools` (`api.ts:1951-1956`); the `tools-builtin` string is only a React-Query cache key. The per-agent editor already renders MCP with a source badge (`ToolsAndPermissions.tsx:364`). So **E5 is presentation only**: add **per-server grouping + source badge** in the global editor; group MCP tools by `source:'mcp'` + server name, NOT by `category`.
- **Guard:** ensure the B-1 `category==='system'` separation does NOT catch an MCP tool with an unset category (MCP tools stay in their per-server group, never the Advanced/system disclosure). MCP-tool `scope`/`category` could not be observed live (no server at audit time) — test with a synthetic `source:'mcp'` tool in the payload.
- **Policy persistence + orphans (M-6):** global tool policy persists a per-tool-id entry for ANY tool set away from default, **including MCP tools** (the save payload is keyed by tool id). When an MCP server is removed, its tools vanish from `/tools` and thus both editors; any persisted Deny/Ask for those ids is **kept-but-inert** (not pruned, not rendered) — harmless because `resolvePolicy` only consults it if the id reappears (same server re-added). No pruning job; document this so the orphan entry isn't mistaken for a bug.
- **AC1.** With an MCP server configured, its tools appear as a per-server group + source badge in BOTH global Security and an agent's Tools. **AC2.** A global Deny on an MCP tool greys it out per-agent. **AC3.** Removing the server removes its tools from both editors; a previously-saved Deny persists inertly and re-applies if the server is re-added.

### US-E6 — Per-agent skill assignment, opt-in (D17)
- The backend already supports per-agent skills: `AgentConfig.Skills []string` (`config.go:449`) → `skillsFilter = agentCfg.Skills` (`instance.go:141`). **Verified semantics (`loop.go:6102-6107`): an empty `Skills` list yields ZERO skills today** (`len(combined)==0 → return nil`). So "opt-in, default none" is ALREADY the runtime behavior — **E6 is purely frontend: no migration, no §9 violation.** E6 adds a per-agent **Skills** picker (agent profile + Create-Agent advanced) that edits this list. Source = `GET /api/v1/skills`.
- **AC1.** A new agent grants no skills until added (matches the current backend default). **AC2.** Granting a skill to agent A does not grant it to agent B. **AC3.** A skill not granted to an agent is unavailable in that agent's runs.

### Preset-consolidation WIDENING (amends §2.1 + US-B3 + US-D2)
`<ToolPolicyEditor>` MUST replace BOTH the per-agent `POLICY_PRESETS` AND the global `GlobalToolPoliciesSection` (`SecuritySection.tsx:101-252`). Collapse SecuritySection's **local duplicates** — `CATEGORY_LABELS` (`:45`), `PolicyBadge` (`:58`), `groupByCategory` (`:91`) — onto the shared canonicals (`@/lib/toolCategories` for labels + grouping; `@/components/shared/PolicyBadge` for the badge), which `ToolsAndPermissions` already imports. Today there are 4-5 tool systems (2 dead, deleted in US-E3); after this there is **one** policy editor + **one** category source + **one** `PolicyBadge`.

---

## 7. Existing Codebase Context

| Symbol | Role | Context |
|---|---|---|
| `ToolsAndPermissions.tsx` (`POLICY_PRESETS` 47-83, grid, confirm 431-472) | **replace** | per-agent tool editor → `<ToolPolicyEditor>` |
| `GlobalToolPoliciesSection` in `SecuritySection.tsx` (101-252) | **replace** | global tool editor → `<ToolPolicyEditor>` |
| `@/lib/toolCategories` (`CATEGORY_LABELS`, `groupByCategory`, `resolvePolicy`) | **call** | category model for the shared editor |
| `ScheduleFormSheet.tsx` (TriggerKind/everyValue/cronExpr/deliver/session_mode) | **modify** | A1-A3 |
| `triggerSummary()` (`SchedulesList.tsx:42`) | **call** | live preview |
| `DiagnosticsSection.tsx` (`IssueCard` action_link) | **extend** | score-as-control-surface |
| `GatewaySection.tsx` (auth_mode ~183, bind ~151), `SecuritySection.tsx` (policyMode ~451), `SandboxProfileSelector.tsx` | **wrap** | `<RiskySettingControl>` |
| `channel-fields.ts` (`ChannelField`) | **extend (additive)** | C1/C2 metadata |
| `ChannelConfigPanel.tsx` | **modify** | C1-C4 |
| tool registry scope/category (backend) | **fix** | B-1 root cause |

### Impact Assessment
| Symbol Modified | Risk | Direct dependents |
|---|---|---|
| `<ToolPolicyEditor>` (new, replaces 2 grids) | **HIGH** | global Security + every Agent profile + Create-Agent; one component, two call-sites, both must keep their policy round-trip |
| `channel-fields.ts ChannelField` | LOW | additive optional fields; `ChannelConfigPanel` only |
| `RiskySettingControl` wrapping 4 controls | MEDIUM | each control's save path must be preserved |
| tool registry scope/category (B-1) | MEDIUM | both tool grids' filters depend on it; verify global grid still hides system.* after the data fix |

---

## 8. Behavioral contract (quick reference)

- When a novice opens any config form, the system shows only fields they can understand; advanced/jargon fields are collapsed with safe defaults.
- When a user selects a protection-weakening value, the system confirms with a plain consequence and leaves a standing warning.
- When a user picks a tool-policy role, the system applies the §2.1 mapping identically in global and per-agent surfaces.
- When a schedule trigger is edited, the system shows a live human-readable "next run".
- When a channel field needs an external value, the system tells the user where to get it.
- When the WhatsApp QR is pending/ready/linked/failed, the system shows the matching state (never silence).
- When a validation/Test error occurs, the system names the human field label, not the API key.

## 9. Explicit non-behaviors
- The system must NOT remove any existing capability — every advanced field/policy/cron remains reachable (under "Advanced"), because power users still rely on them.
- The system must NOT change runtime channel/agent/scheduler behavior except the two named bugs (B-1, B-2) and the merged crash fix — this is a UI-comprehension change.
- The system must NOT auto-pick a weakened security value or skip the confirm — safety regressions must require deliberate user action.
- The system must NOT introduce a new wire/generated type or a form library — `channel-fields.ts` is `not-wire-format`; forms stay `useState`.
- The system must NOT edit `src/lib/api/generated/` or `pkg/api/generated/` — in particular the `session_mode` wire enum **retains `"main"`** (F-G03). D18 removes only the UI control that *emits* `main`; the value stays in the generated zod/Go types and the (deprecated) backend path, so existing `main` schedules still deserialize. **Reverse-parse on edit:** a pre-existing `main` schedule loads in read-only/"legacy" form (its mode can no longer be re-selected) — it is not silently rewritten to `isolated`.
- The system must NOT show internal identifiers (`system.*`, `SEC-28`, `--allow-god-mode`, file-format `.md` names, raw config diffs) at the primary altitude.
- Channels ADDED after this spec inherit the C1–C4 helper-metadata contract by default (F-G15). `signal` is currently absent from the descriptor and out of scope; if it lands mid-sprint, either apply the helper treatment in its own PR or explicitly defer it there — it must not ship a bare Configure panel by omission.

**Deferred (tracked, not prose-only) — F-G11:** the run-time audit line for a scheduled run that auto-denies an `ask`-gated tool (O-3/F-13) is a **backend follow-up issue whose acceptance criterion is "the cron fire path emits an audit entry on auto-deny."** It is named here as a tracked deferral, not left as "confirm; if not, file a follow-up."

## 10. TDD plan (per workstream; vitest unit-first, then component, then the existing E2E specs)

| Order | Test | Level | Traces |
|---|---|---|---|
| 1 | `toolPolicyPresets.test` — Cautious/Balanced/Full-access → policy map (§2.1) | Unit | US-B3, US-D2 |
| 2 | `ToolPolicyEditor.test` — preset apply, category rollup, system.* in the Advanced/system disclosure (NOT the primary grid), Mixed-pill for a heterogeneous category, raw-grid behind Advanced, policy round-trip | Component | US-B3/US-D2/US-D4 |
| 3 | `RiskySettingControl.test` — recommended pill, confirm-to-weaken (default=safe), standing badge, no-retroactive-dialog | Component | US-B2 |
| 4 | `ScheduleFormSheet.test` — simple/advanced split; Repeat↔cron equivalence + live preview; **no "main" option / default `isolated` (D18)**; "keep full thread"→`continue`; Once default; owner prefill | Component | US-A1/A2/A3 |
| 5 | `SecuritySection.test` — two-layer (jargon hidden until Advanced); score deduction → in-place fix | Component | US-B1/B4 |
| 6 | `ChannelConfigPanel.test` — helper+link render; advanced collapse; GChat authGroup mutual-exclusion; human-label errors; aria-describedby | Component | US-C1/C2/C4 |
| 7 | `whatsappQrState.test` — all 5 wire states `waiting/code/linked/timeout/error`; timeout vs error render distinct copy | Component | US-C3 |
| 8 | `AgentTools.test` — locked-agent read-only/no-write (B-2); new-agent default=Balanced | Component | US-D5/D1 |
| 9 | existing E2E (`settings.spec`, `channels-routing.spec`, `whatsapp-qr.spec`) updated to the new labels/states | E2E | regression |
| 10 | `cronRoundTrip.test` — friendly→cron generation + reverse parse; complex cron → Custom view; re-save untouched is byte-identical (M-1) | Unit/Component | US-A2 |
| 11 | `RiskySettingControl.revert.test` — badge derives from persisted value; clears on save of the safe value; no premature clear for restart-gated (M-2) | Component | US-B2 |
| 12 | `helpLinkScheme.test` — every descriptor `helpLink.url` matches `^https://` (M-4) | Unit | US-C1/§2.2 |
| 13 | `mcpPolicyOrphan.test` — synthetic `source:'mcp'` tool renders per-server; removing the server leaves a persisted Deny inert; re-add re-applies (M-6) | Component | US-E5 |
| 14 | `oldPresetCompat.test` — an agent saved under a removed preset (Unrestricted/Standard/Minimal, `ToolsAndPermissions.tsx:47-83`) loads + saves under Cautious/Balanced/Full **without mutating policy on mere render** (O-4). *F-G13:* verify whether config ever persisted a preset **name** or only `default_policy`+overrides — if only the latter, this simplifies to "an arbitrary saved policy loads/saves unchanged." | Component | US-D2 |

**Regression:** this modifies existing UI — all current `*.test.tsx` for the touched components must keep passing (or be updated for renamed labels, justified per change). The two tool-grid surfaces share one new component: add a round-trip test proving an existing per-tool override loaded from config still renders + saves unchanged.

## 11. Sequencing / dependency plan
1. **Shared primitives first:** `<ToolPolicyEditor>` (+ §2.1 map; must render MCP tools per-server, per US-E5), `<RiskySettingControl>`, `<AdvancedDisclosure>`, `<HelperLink>`. (Gated PR, mostly pure components + tests.)
2. **A — Schedules** (independent; can parallelize with #1).
3. **B — Security** (consumes `<ToolPolicyEditor>` + `<RiskySettingControl>`; includes the global-policy absorption + MCP tools in the editor).
4. **D — Agent config** (consumes `<ToolPolicyEditor>`; includes B-1/B-2 bugs + the per-agent MCP-tool surface).
5. **E — Tools / MCP / Skills** (consumes `<ToolPolicyEditor>` + `<RiskySettingControl>`; MCP modal fix + stdio gate, Tools-tab overview, delete dead code, Skills trust mount + per-agent skills E6 — resolve the empty-`Skills` semantics first).
6. **C — Channels** (independent; consumes `<HelperLink>`).
Each as its own 7-reviewer-gated PR into the active release branch.

## 12. Ambiguities (resolved / accepted)
| Item | Resolution |
|---|---|
| Balanced strictness | **Resolved** — §2.1 (allow, gate exec/browser-write/file-write=ask, browser.evaluate=deny). |
| "Full access" naming | **Resolved** — renamed from "Trusted"; allow-all, no overrides. |
| WhatsApp QR real-bug-vs-env | **Resolved** — it was the reload crash (PR #313); D9 is the UX state machine. |
| B-1 root cause + fix | **Verified (live `/tools`)** — `system.*` tools = `category:"system"` + `scope:"core"`; both grids filter on `scope==='system'` and so leak. Fix = SPA filter on `category==='system'` in both grids. No backend change required. |
| `system.*` visibility | **RESOLVED — Advanced disclosure** (owner, 2026-06-03). Shown under a collapsed "Advanced / system tools" group (default deny) in both editors; B-1 fix separates via `category==='system'`. |
| `'main'` schedule mode (M-5) | **RESOLVED — D18: Hermes isolated-only** (owner, 2026-06-03). Drop `main`; every run isolated + shared (workspace-scoped) memory; `continue` kept as a safe Advanced opt-in. Eliminates the F-16 hazard at the source; no backend memory change (verified `MemoryStore` is per-workspace). Backend `SessionModeMain` deprecated, removal tracked separately. Researched against Hermes (isolated-only, no shared session) + OpenClaw (run-lane); chose Hermes' model. |
| MCP / skills as frontend-only | **Verified** — per-agent MCP already rendered; global Security needs the `/tools` query switch (E5). Empty `Skills` already == none, so E6 is frontend-only (F-06). |
| Category set for the user-facing grid | **Accepted assumption** — use existing `@/lib/toolCategories` groups minus the hidden system category; product may rename labels during build. |

## 13. Holdout evaluation scenarios *(post-implementation; NOT in the TDD matrix)*
- H1 (happy): a non-technical tester creates a "daily 6pm summary" schedule without help and without opening "Advanced".
- H2 (happy): the same tester connects Telegram using only the on-screen guidance + link.
- H3 (happy): the tester reads the Security tab and can state, in plain words, whether they're protected.
- H4 (error): turning off login shows a clear consequence and a standing warning afterward.
- H5 (error): a wrong Google Chat method shows which one field is needed, not three boxes.
- H6 (edge): enabling WhatsApp shows a QR with phone steps (and never crashes the gateway).
- H7 (edge): switching an agent's role to "Cautious" makes it ask before acting; "Full access" never asks.
- H8 (edge): a tester schedules a task whose agent role would auto-deny the needed tool, and the form warns them *before* they save (F-13) — the schedule does not silently no-op.
- H9 (edge): a daily schedule whose agent saved a fact via `remember` on run 1 can `recall_memory` it on run 2, even though each run is an isolated session (D18: isolated transcript + shared workspace memory) — and the form never offers a "main chat" option.
