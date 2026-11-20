Status: In review

ADR: none — no open design decision. The founder settled the design in the wireframe answers (S-Q1 to S-Q14, #1096 Q1 to Q4). The contract shapes this spec needs, including the B4 choice, are architect decisions recorded in "Contract Changes" below. One existing ADR needs a dated correction because of S-Q11 B: [ADR-022 — Re-auth Consent Gate: Credential-Vault Scope Expansion to v0.1.0](../architecture/ADR-022-credential-vault-reauth.md) (see FQ-2 and DOC-012).

# Feature Specification: Settings redesign (page G)

**Created**: 2026-11-20
**Input**: founder-approved wireframe round 4 and design notes (`/workspace/omnipus-coordination/handoffs/ui-redesign/settings-wireframe/design-notes.md`, `questions.md`, `review-response.md`, `index.html`, `app.js`); Settings inventory (`/workspace/logs/ui-1519/settings/inventory.md`); founder answers S-Q1 to S-Q14 and #1096 Q1 to Q4 (`/workspace/omnipus-coordination/handoffs/founder-questions-20261119/team-b-open-questions.md`). No interview-me output file exists for this feature; the wireframe answers take its place (team-lead's brief).
**Code baseline**: release `c2f8f8e735` (after #1919, PR #1927, removed Backup and Restore). Every `file::symbol` below was read at that commit.
**Epic**: #1519 (Stage: Spec; Horizon: Now — founder 2026-11-19, "UI redesign is now P0").
**Separable block**: everything tagged **[#1096]** (signed-in browsers). The chief ruled at 04:38Z on 2026-11-20 that #1096 is in this package. The block stays tagged so it can be cut without rework.
**Riders** (open hardening issues on the same files; to be confirmed with the chief before the design is final — see "Riders" below): #1154, #1153, #1265, #1596, #346, #1560, #1145, #1742, #1940, #1641, #853, #936, #1917, #995 (Settings-section endpoints only), #1595 [#1096], #1146 [#1096], #598 [#1096]. Closed by this package: #689 [#1096] (the work itself).
**Agent tool field table**: no agent-managed object changed. Settings values are not agent-managed objects (plan, task, goal, scheduled task, agent). For completeness: the system agent's `set_config` tool cannot write `storage.*` (not in its writable sections), `sandbox.*` or `gateway.users` (refused subtrees) — `pkg/sysagent/tools/config.go::ConfigSetTool.Description` and `::validateConfigKey`. This spec adds no agent write path and removes none; the new retention switch and the browser-session set are therefore user-only, as their neighbours already are.

---

## Overview

Settings today is 13 tabs in one long row, with six different ways of saving, six different ways of saying "restart needed", the same retention value editable in two places with different rules, a device-pairing tab that promises things that do not exist, and only one web sign-in per user: signing in on a phone signs the laptop out.

This feature rebuilds Settings as **12 sections in 3 labelled groups** (AI & agents, Security, System) listed down the left side, one section at a time on a phone. It applies **one save rule** everywhere (values wait for **Save changes**; buttons act at once), **one password prompt per Save**, and **one restart pattern** (a "Needs restart" badge, a "Restart now?" window right after such a Save, then one banner). It splits retention into two settings in one place — chat history and memory, with "Keep forever" for memory — and moves God Mode, voice input, verbose chat and the old Models and Performance values to where people look for them. It removes what no longer exists (backup and restore) and hides what does not work (device pairing).

The **[#1096]** block lets one person stay signed in on up to 10 browsers at once. Settings → Sign-in lists them with their last use, marks this browser, and lets the person end any other browser or all other browsers at once. Signing in on an 11th browser ends the one used longest ago. "Sign out" in the account menu ends this browser only.

The server changes are small and named: **B2** (one complete list of saved changes that wait for a restart), **B3** (one write path per retention setting), **B4** (one password prompt per Save — decided below, no server change), **B6** ("Keep forever" for memory), **B7** (deleting a stored key needs a confirm, not a password — S-Q11 B; not in the design notes, found in the code), and the **[#1096]** session set and its three endpoints.

---

## Existing Codebase Context

GitNexus MCP tools are not connected in this session, so every fact below comes from Grep and Read at `c2f8f8e735` (the fallback the repo rules allow). Risk levels are therefore **Inferred** from a Grep caller sweep, not from a GitNexus `impact` run; backend-lead runs `impact` before editing.

### Symbols Involved

| Symbol | Role | Context |
|---|---|---|
| `src/routes/_app/settings.tsx::SETTINGS_TABS`, `::settingsSearchSchema` | modifies | 13-value `?tab=` enum; unknown values fall back to Providers; `focus` accepts only `god-mode` |
| `src/components/screens/SettingsScreen.tsx::SettingsScreen` | replaces | Tab row; Devices only when pairing is enabled |
| `src/components/settings/*Section.tsx` | modifies / splits | One component per old tab (inventory §1–13) |
| `src/components/settings/useStepUp.tsx` | modifies | Password-prompt flow used by Security, Sandbox, Performance, God Mode, Providers, Integrations |
| `src/components/settings/GatewayRestartModal.tsx` | reuses | Restart window; also the "Restart now?" window after a Save |
| `src/lib/api/config.ts` (`frontendToRawConfig` mapping at lines 321–327) | modifies | Today writes `data.session_retention_days` → `storage.retention.session_days` through `PUT /config` |
| `pkg/gateway/rest_pending_restart.go::RestartGatedKeys`, `::HandlePendingRestart` | modifies (B2) | The only aggregated restart list; lacks the filesystem model and the shell network proxy |
| `pkg/gateway/rest_memory_settings.go` (validation at 117–122, `setRetentionFields`) | modifies (B3) | Writes `session_days` and `memory_retros_days`; accepts any value ≥ 0 |
| `pkg/gateway/rest_retention.go` | deletes GET/PUT (B3) | `/security/retention`; no SPA caller (`src/lib/api/config.ts:349` comment) |
| `pkg/gateway/blocked_paths.go::blockedPaths` | modifies (B3) | One-write-path precedent (#904) |
| `pkg/config/config.go::OmnipusRetentionConfig`, `pkg/config/config_retention.go` | modifies (B6) | `SessionDays`, `Disabled`, `MemoryRetrosDays`; no memory "forever" |
| `pkg/gateway/retention_goroutine.go` (`IsDisabled` checks at 114 and 267; `RetentionMemoryRetrosDays` at 271) | modifies (B6) | `disabled` stops BOTH sweeps today |
| `pkg/agent/memory_adapter.go::SearchRetros`, `pkg/agent/memory.go::MemoryStore.ReadRetros` | modifies only if FQ-1 = A | Recall reads at most 365 days back (hard-coded `ReadRetros(365)`, clamp in `ReadRetros`) |
| `pkg/gateway/rest_settings.go::deleteCredential` | modifies (B7) | Calls `requireReAuth` today |
| `pkg/gateway/rest_integrations_auth.go::reauthStore`, `::requireReAuth` | unchanged (B4) | Single-use 5-minute consent token |
| `pkg/gateway/rest_auth.go::HandleLogin`, `::HandleLogout`, `::HandleChangePassword` | modifies [#1096] | Single `session_token_hash` slot: overwritten on login, cleared on logout and password change |
| `pkg/gateway/middleware/session_cookie.go::ResolveUserFromCookie`, `::IssueSessionCookie`, `::MintSessionToken`, `::SessionCookieMaxAge` | modifies [#1096] | One bcrypt compare per user; 24-hour cookie |
| `pkg/gateway/middleware/planted_cookie.go::genuineSessionResend` | modifies [#1096] | ADR-094 FR-015 guard reads `SessionTokenHash` and `SessionIssuedAt` |
| `pkg/config/config.go::UserConfig` (`SessionTokenHash`, `SessionIssuedAt`, `Tokens`, `MaxUserTokens = 10`) | modifies [#1096] | Bearer tokens are already a capped set; the cookie is the single-slot outlier |
| `pkg/gateway/websocket.go::authenticateWS`, `pkg/gateway/browser_ws.go` (cookie read at 916) | modifies [#1096] | Authenticate once at upgrade; nothing closes a socket when its sign-in ends |
| `pkg/gateway/signin_provider.go::restAPIHost.IssueSession`, `::WriteSessionToken` | modifies [#1096] | Platform-mode sign-in and the desktop claim route mint or re-send the session |
| `pkg/gateway/middleware/person_session.go::RequirePersonSession` | reuses [#1096] | Refuses CLI token, dev bypass and non-account identities with 403 |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|---|---|---|---|
| `middleware.ResolveUserFromCookie` [#1096] | **HIGH** (Inferred) | `auth.go` cookie path, `rest_auth.go::withOptionalAuth`, `websocket.go::authenticateWS`, `browser_ws.go`, `signin_provider.go::ResolveSessionUser`, `session_cookie.go:610` | every authenticated REST route, both WebSockets, platform sign-in |
| `middleware.genuineSessionResend` [#1096] | **HIGH** (Inferred) | `PlantedCookieGuard` | every request that carries a duplicated reserved cookie (ADR-094 FR-015) |
| `HandleLogin` / `IssueSessionCookie` [#1096] | MEDIUM | `/auth/login`, `/onboarding/complete`, platform callback | onboarding and sign-in flows |
| `RestartGatedKeys` (B2) | MEDIUM | `HandlePendingRestart`; mirrored by `pkg/sysagent/tools/config.go::isRestartRequired` | Gateway restart banner, `set_config` result |
| `blockedPaths` (B3) | MEDIUM | `rest_config.go::updateConfig` | every SPA `PUT /config` caller (`src/lib/api/config.ts`) |
| `retention_goroutine.go` sweep (B6) | MEDIUM | nightly sweeps | data deletion — a wrong condition deletes memory |
| `MemoryStore.ReadRetros` (FQ-1) | MEDIUM | `SearchRetros` → `recall_memory` | agent recall; touches `pkg/agent`, so hands-on UAT on the cut commit is mandatory |
| `deleteCredential` (B7) | LOW | `DELETE /credentials/{key}` | Keys & secrets; security-lead review (focus area `pkg/credentials` neighbour) |

The two **HIGH** rows need security-lead sign-off on this spec before GREEN starts. They are the [#1096] cookie path, which every authenticated request crosses.

---

## Contract Changes (contract-first — Hard Constraint #8)

The [#1096] contract comes first (chief's instruction, 2026-11-20). Every new or changed shape below goes into `contracts/` before any Go or TypeScript is written; backend-lead edits the files and regenerates with `scripts/gen-contracts.sh`. This spec proposes the shapes; it does not hand-write generated code.

### C-1 [#1096] Signed-in browsers

**Decision.** The browser sign-in becomes a capped set, mirroring the bearer-token set (`UserConfig.Tokens`, `MaxUserTokens = 10`), exactly as #689 proposes. Three endpoints under `/auth/browser-sessions` expose it. All three are behind `withAuth` + `middleware.RequirePersonSession` (a signed-in person on a configured account; CLI token, dev bypass and the legacy env token get 403), are registered in both local and platform auth modes, and the two state-changing ones are CSRF-protected like every other state-changing route.

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `BrowserSession` | new | `contracts/components/schemas/BrowserSession.yaml` | One signed-in browser. Never the secret or its hash |
| `BrowserSessionList` | new | `contracts/components/schemas/BrowserSessionList.yaml` | Response of `GET /auth/browser-sessions` |
| `BrowserSessionsSignOutOthersResponse` | new | `contracts/components/schemas/BrowserSessionsSignOutOthersResponse.yaml` | Response of `POST /auth/browser-sessions/sign-out-others` |
| `/auth/logout` description | changed | `contracts/openapi.yaml` | Ends this browser's sign-in only (Q2 A); the description today says it clears `session_token_hash` |
| `/auth/change-password` description | changed | `contracts/openapi.yaml` | States that every browser sign-in ends (unchanged behaviour, now stated) |

Proposed `BrowserSession.yaml`:

```yaml
title: BrowserSession
type: object
description: >
  One browser signed in to the caller's account (#1096). Carries no secret:
  the id is the non-secret routing prefix of the session cookie, never the
  cookie value or its hash. label and device_kind are derived from the
  User-Agent header at sign-in and are informational only — a client can send
  any User-Agent.
additionalProperties: false
required: [id, label, device_kind, signed_in_at, last_used_at, expires_at, current]
properties:
  id:
    type: string
    pattern: '^[A-Za-z0-9_-]{16}$'
    description: Non-secret session id (96 random bits, base64url). Stable for the life of the sign-in.
  label:
    type: string
    minLength: 1
    maxLength: 64
    description: Browser and operating system, e.g. "Chrome on macOS". "Unknown browser" when not recognised.
    example: Chrome on macOS
  device_kind:
    type: string
    enum: [desktop, phone, tablet, unknown]
    description: Picks the row icon only.
  signed_in_at:
    type: string
    format: date-time
  last_used_at:
    type: string
    format: date-time
    description: >
      Last authenticated request made with this sign-in. Kept in memory; after a
      gateway restart it starts again from signed_in_at until the browser is next used.
  expires_at:
    type: string
    format: date-time
    description: signed_in_at plus the sign-in lifetime (24 hours today, SessionCookieMaxAge).
  current:
    type: boolean
    description: True only for the sign-in the request itself was made with.
```

Proposed `BrowserSessionList.yaml`:

```yaml
title: BrowserSessionList
type: object
additionalProperties: false
required: [sessions, max_sessions]
properties:
  sessions:
    type: array
    maxItems: 10
    items: { $ref: './BrowserSession.yaml' }
    description: >
      Live sign-ins only (expired ones are never listed). Order: the current
      sign-in first, then by last_used_at, newest first; ties by signed_in_at,
      newest first.
  max_sessions:
    type: integer
    minimum: 10
    maximum: 10
    description: How many browsers may be signed in at once (founder #1096 Q1 A).
```

Proposed `BrowserSessionsSignOutOthersResponse.yaml`:

```yaml
title: BrowserSessionsSignOutOthersResponse
type: object
additionalProperties: false
required: [ended]
properties:
  ended:
    type: integer
    minimum: 0
    maximum: 9
    description: Number of other browser sign-ins that were ended.
```

Proposed `openapi.yaml` paths (abridged to the decision-bearing parts; `security: [BearerAuth: []]` like the other authenticated auth routes):

```yaml
/auth/browser-sessions:
  get:
    operationId: listBrowserSessions
    summary: List the browsers signed in to this account (#1096)
    responses:
      '200': { content: { application/json: { schema: { $ref: '#/components/schemas/BrowserSessionList' } } } }
      '401': { $ref: '#/components/responses/401Unauthorized' }
      '403': { description: Not a signed-in person (CLI token, dev bypass, legacy env token). }
/auth/browser-sessions/{id}:
  delete:
    operationId: endBrowserSession
    summary: End one other browser's sign-in (#1096)
    parameters: [{ name: id, in: path, required: true, schema: { type: string } }]
    responses:
      '204': { description: Ended. That browser's open live connections are closed. }
      '401': { $ref: '#/components/responses/401Unauthorized' }
      '403': { description: Not a signed-in person. }
      '404': { description: No live sign-in with this id (unknown, malformed, already ended or expired — one indistinguishable answer). }
      '409': { description: The id is the caller's own current sign-in; use POST /auth/logout. }
/auth/browser-sessions/sign-out-others:
  post:
    operationId: signOutOtherBrowsers
    summary: End every browser sign-in except the caller's own (#1096)
    responses:
      '200': { content: { application/json: { schema: { $ref: '#/components/schemas/BrowserSessionsSignOutOthersResponse' } } } }
      '401': { $ref: '#/components/responses/401Unauthorized' }
      '403': { description: Not a signed-in person. }
      '409': { description: The request was not made with a browser sign-in (for example a bearer token), so "other" has no meaning. }
```

**Storage (not on the wire, stated so the contract has one meaning).** Each account's sign-ins live in `config.json` as `gateway.users[].sessions[]`, each entry `{id, hash, issued_at, label, device_kind}`, where `hash` is bcrypt over the secret part only. The cookie value becomes `<id>.<secret>` (the 43-character secret `MintSessionToken` already makes), so one request costs one bcrypt compare, not up to ten. Writes happen only at sign-in, sign-out, End, sign-out-others, eviction and password change — never per request — so the config file and its reload are not churned. Last-used times are kept in memory only (decision: see Clarifications 2026-11-20 #4). `gateway.users` is already refused by `PUT /config` (`blocked_paths.go::blockedPaths`) and by `set_config`. `GET /config` must never serve `sessions` (FR-089), because its key-name redaction (`rest_config.go::redactSensitiveFields`) would not catch a field called `hash`.

**Migration (Q4 A).** On the first load after the update, an existing `session_token_hash` (with `session_issued_at`) becomes the first entry of the set, marked legacy, with label "Unknown browser" until that browser's next request supplies a User-Agent. A legacy cookie (no `<id>.` prefix) is checked only against that one legacy entry. Nobody is signed out by the update. A legacy entry with no issue time gets the migration time as its issue time.

**Threat model [#1096] (for the security-lead pass, gpt-6-astra).** Realistic risks get a requirement; exotic ones are parked.

| # | Risk | Realistic? | Requirement |
|---|---|---|---|
| T1 | A stolen or forgotten sign-in stays valid after the person ends it, because the browser's open live connection keeps working | Yes | FR-084: ending a sign-in (End, sign-out-others, Sign out, password change, eviction, expiry) closes that browser's WebSocket connections within 5 s |
| T2 | A replayed cookie value works after 24 h: today the server never checks the issue time; only the browser drops the cookie | Yes | FR-079: the server refuses a sign-in older than its lifetime, whatever the browser sends |
| T3 | Planted duplicate cookie (ADR-094 FR-015) stops working or re-sends the wrong session once there are several | Yes | FR-086: the planted-cookie guard keeps its rules over the set; resend picks the entry by id |
| T4 | CPU exhaustion: up to 10 bcrypt compares per request if cookies carried no id | Yes | FR-077: id-prefixed cookie, one compare per request; a legacy cookie is compared with the legacy entry only |
| T5 | Cross-site request ends sign-ins | Yes | FR-081: the state-changing routes keep the double-submit CSRF check and SameSite=Strict |
| T6 | An attacker holding one stolen sign-in ends the owner's other browsers | Accepted | The owner signs in again and changes the password, which ends every sign-in (FR-083); every End and sign-out-others is audited (FR-088). docs say what to do (DOC-009) |
| T7 | Probing ids to learn which sign-ins exist | Yes, low | FR-082: unknown, malformed, ended and expired ids get one indistinguishable 404 |
| T8 | The list leaks where and how the person works | Yes, low | FR-076: only a label and a device kind are stored and served; no IP address, no raw User-Agent; only to a signed-in person |
| T9 | Concurrent sign-ins push the set past 10 | Yes | FR-078: the cap is enforced inside the one serialised config write |
| T10 | No record of who ended what | Yes | FR-088: sign-in, End, sign-out-others and eviction write audit entries |

Parked (exotic, listed for completeness): timing differences between "unknown id" and "known id, wrong secret"; a forged User-Agent mislabelling a row (the label is informational, documented); binding a sign-in to an IP address or device fingerprint; a clock that jumps backwards; more than one human account (the product is single-user: `GatewayConfig.Users` comment "holds at most one entry").

### C-2 B2 — one combined pending-restart list

**Decision:** extend the existing `GET /config/pending-restart` (`operationId: getPendingRestart`); no new endpoint. Its entries already carry the saved value (`persisted_value`) and the running value (`applied_value`) — `contracts/components/schemas/PendingRestartEntry.yaml`.

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `PendingRestartEntry` | changed (description only) | `contracts/components/schemas/PendingRestartEntry.yaml` | `key` description lists the restart-gated keys, adding `sandbox.filesystem_model` and `tools.exec.enable_proxy` |
| `/config/pending-restart` | changed (description only) | `contracts/openapi.yaml` | Names the full key list; states that God Mode and hot-reloaded keys never appear |

Code facts that correct the design notes (code wins):

| Design-notes claim (§5 B2, wireframe badges) | Code at `c2f8f8e735` | This spec |
|---|---|---|
| The Gateway list misses sandbox mode | `sandbox.mode` is already in `RestartGatedKeys` (`rest_pending_restart.go:35`) | No change for sandbox mode |
| The list misses the filesystem model | Not in `RestartGatedKeys`; `PUT /security/sandbox-config` does not set `requires_restart` for it (`rest_sandbox_config.go::respond`, only mode, allowed paths and workspace guard); the policy is built at boot (`sandbox_apply.go`, `DefaultPolicyForModel`) | Add `sandbox.filesystem_model` (Inferred restart-gated; RED proves the running value does not change on a reload) |
| The list misses the network proxy | `tools.exec.enable_proxy` is read when the agent loop is built (`pkg/agent/loop_construct.go:354`) and is not in the list | Add `tools.exec.enable_proxy` (Inferred; RED proves it) |
| Prompt injection defense needs a restart (wireframe "Needs restart" badge) | Hot-reloaded: `PUT` answers `requires_restart: false` unless the reload fails (`rest_prompt_guard.go`), and the key is deliberately excluded (`rest_pending_restart.go:20`) | **No "Needs restart" badge** on Defense level. A failed reload still says so through the write response (FR-034) |
| — | God Mode is deliberately excluded (`rest_pending_restart.go:39-50`, O14) | Stays excluded; God Mode keeps its own restart window |

### C-3 B3 + B6 — retention: one write path per setting, and "Keep forever" for memory

**Decision.** One dedicated endpoint owns all three retention values. The design notes keep chat history on the generic `PUT /config` (no server range check) and memory on `PUT /settings/memory`. Instead, both move to a new **`/settings/retention`**, and `storage.retention` joins `blockedPaths`, the same one-write-path pattern #904 used for the tool-call limit. Reasons: (1) the server then enforces 1–365 and 1–730 itself (today `PUT /config` checks no range, and `PUT /settings/memory` accepts any value ≥ 0, `rest_memory_settings.go:117-122`); (2) the Data & retention Save is one request; (3) "one write path per setting" becomes true at the server, not only in the screen. The unused `GET/PUT /security/retention` is deleted, as the design notes say. `POST /security/retention/sweep` stays (out of scope, unchanged).

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `RetentionSettings` | new | `contracts/components/schemas/RetentionSettings.yaml` | GET response and PUT body of `/settings/retention` |
| `MemorySettings` | changed | `contracts/components/schemas/MemorySettings.yaml` | Remove `session_days` and `memory_retros_days` (they move to `RetentionSettings`). Because the schema has `additionalProperties: false`, a body still carrying either field gets 400 (FR-045) |
| `RetentionConfig`, `RetentionUpdateRequest`, `RetentionUpdateResponse` | deleted | `contracts/components/schemas/` + `openapi.yaml` refs at lines 1512, 1797, 1995 | Only `/security/retention` GET/PUT used them (verified by Grep over `contracts/`) |
| `/security/retention` GET and PUT | deleted | `contracts/openapi.yaml` | `rest_retention.go` handlers removed |

Proposed `RetentionSettings.yaml`:

```yaml
title: RetentionSettings
type: object
description: >
  How long chat history and memory are kept (Settings → Data & retention).
  The only write path for storage.retention.session_days,
  storage.retention.memory_retros_days and storage.retention.memory_retros_forever;
  PUT /config refuses storage.retention. PUT is a partial update.
additionalProperties: false
properties:
  session_days:
    type: integer
    minimum: 1
    maximum: 365
    description: Days chat transcripts are kept. Default 90. Maps to storage.retention.session_days.
    example: 90
  memory_retros_days:
    type: integer
    minimum: 1
    maximum: 730
    description: >
      Days the agents' retrospectives (reflections on past chats) are kept.
      Default 180. Maps to storage.retention.memory_retros_days. Ignored while
      memory_retros_forever is true, but still validated when sent.
    example: 180
  memory_retros_forever:
    type: boolean
    description: >
      Keep memory forever: the nightly sweep deletes no retrospective. Default
      false. Maps to storage.retention.memory_retros_forever (new).
    example: false
  keep_everything_forever:
    type: boolean
    readOnly: true
    description: >
      True when config.json sets storage.retention.disabled, an older switch
      that stops BOTH sweeps. Config-file only: PUT refuses it. While true,
      neither day count is applied.
```

Endpoint: `GET /settings/retention` (`getRetentionSettings`) returns every field with defaults applied; `PUT /settings/retention` (`updateRetentionSettings`) is a partial update, 200 echoes the stored values, 400 names the field and its range, an empty body is a 400 ("at least one field required" — unlike the deleted route, which accepted `{}`). Auth: `withAuth` like `/settings/memory` (writable by any authenticated user, the existing operator decision for memory settings), plus `RequireNotBypass` because it deletes data on a schedule. No password (not marked "Password" in the wireframe). Hot-reloaded; no restart.

Server meaning of the stored values (B6):

| Stored state | Chat sweep | Memory sweep | Source |
|---|---|---|---|
| `disabled: true` (legacy, config-file only) | off | off | unchanged — `retention_goroutine.go:114,267` |
| `memory_retros_forever: true` | by `session_days` | **off** | new |
| `session_days` 0 or absent | 90 days | — | unchanged default (`config_retention.go::RetentionSessionDays`) |
| `memory_retros_days` 0 or absent | — | 180 days | unchanged default (`::RetentionMemoryRetrosDays`) |
| a stored day count outside the new range (hand-edited) | applied as stored | applied as stored | GET returns it as stored; the screen shows it with the range error and blocks Save until fixed (FR-046) |

Recall window: see **FQ-1**. Today recall reads at most 365 days of retrospectives (`memory_adapter.go::SearchRetros` calls `ReadRetros(365)`; `ReadRetros` clamps to 365). The design notes (§5 B6) say the switch must be honoured by "the sweep and the recall window". FR-050 is written for the recommended answer and is marked pending.

### C-4 B4 — one password prompt per Save: **no server change** (option 2 of design-notes §5)

**Decision: the screen asks the server for one consent token per Save, from the one typed password, and every Save needs exactly one.** The single-use 5-minute token (`rest_integrations_auth.go::reauthStore.consume`) stays as it is.

**Reason.** The design notes assumed one Save could touch two password-protected endpoints ("Auto-approve and Performance limits"). In the approved wireframe a Save covers **one section** (leaving a section with unsaved changes asks first — `app.js::go` → `guard`), and in the code every section has **at most one** password-protected endpoint:

| Section | Password-protected write (code) | Other writes in the same Save (no password) |
|---|---|---|
| Providers | `PUT /integrations/providers/{id}` for **Active transcriber** (`rest_integrations_roles.go:365` calls `requireReAuth`) | `PUT /providers/default-model` (no `requireReAuth` in `rest_default_model.go`) |
| Limits | `PUT /performance` (`rest_performance.go:203`) | `PUT /settings/context` |
| Protection | `PUT /security/sandbox-config` for Auto-approve (`rest_sandbox_config.go:367`) | prompt guard, rate limits |
| Sandbox & network | `PUT /security/sandbox-config` (mode, filesystem model, workspace limit, allowed folders, internal addresses) | `PUT /config` for the network proxy |
| Every other section | none | — |

So the Save sends all of a section's password-protected values in **one** request to that one endpoint, and one token covers it. A multi-use token (option 1) would weaken the one-consent-one-write property every gated endpoint has today, for a case that does not occur. Option 2's weakness — keeping the typed password in browser memory to mint a second token — does not arise either: the password is used for one `POST /auth/reauth` and dropped (FR-028). A guard test pins the "one password-protected endpoint per section" fact (FR-030), so a later section that needs two fails loudly and reopens this decision. The reauth rate limit (10 per minute per IP, `rest_auth.go:222`) is never stressed by a Save.

Wireframe correction (code wins): **Active transcriber** needs the password (`rest_integrations_roles.go:365`); the wireframe row (`app.js:122`) has no Password badge. It gets one.

One edge stays: the max-tool-calls lowering can still answer 409 after the token is used (`rest_performance_max_tool_iterations.go::freshDriftError` runs inside the locked write). The drift check before the token (`preCheckMaxToolIterationsDrift`) catches the usual case. When the late 409 happens, the screen shows the fresh list of affected agents and asks for the password again. A changed list is a new consent, so a second prompt is correct (FR-031).

No contract change.

### C-5 B7 — deleting a stored key asks for a confirm, not a password (S-Q11 B)

**Code fact the design notes miss:** `DELETE /credentials/{key}` calls `requireReAuth` (`rest_settings.go::deleteCredential`), under an accepted ADR ([ADR-022](../architecture/ADR-022-credential-vault-reauth.md), whose reason is "revoke a live provider/channel key mid-session"). S-Q11 B (confirm only) therefore needs a **server change**, not just a screen change.

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `DELETE /credentials/{key}` description | changed | `contracts/openapi.yaml` | No `X-Reauth-Token`; still `withAuth` + the existing admin and bypass guards; still audited |

Set (`POST /credentials`) and rotate (`POST /credentials/rotate`) keep the password. ADR-022 gets a dated correction at landing (DOC-012). FQ-2 asks the founder to confirm S-Q11 B now that this server-side effect is known; the spec is written for B.

### Collisions checked

New names `BrowserSession`, `BrowserSessionList`, `BrowserSessionsSignOutOthersResponse` and `RetentionSettings` match no existing file in `contracts/components/schemas/` (562 files listed). `AgentSession` and `SessionState*` are chat sessions, a different concept. On screen, "Clear all sessions" (Data & retention) means chat sessions, so the Sign-in section says "browser" and "sign-in", never "session" alone (FR-074).

## API and Data

| Endpoint | Change | Contract |
|---|---|---|
| `GET /auth/browser-sessions` [#1096] | new | `BrowserSessionList` |
| `DELETE /auth/browser-sessions/{id}` [#1096] | new | 204 / 404 / 409 |
| `POST /auth/browser-sessions/sign-out-others` [#1096] | new | `BrowserSessionsSignOutOthersResponse` |
| `POST /auth/login`, `/onboarding/complete`, platform sign-in callback [#1096] | behaviour | add a sign-in to the set instead of overwriting the one slot; cap 10, ending the one used longest ago |
| `POST /auth/logout` [#1096] | behaviour | ends the caller's own browser sign-in only |
| `POST /auth/change-password` [#1096] | behaviour (stated) | ends every browser sign-in |
| `GET /config/pending-restart` | behaviour (B2) | two more keys can appear |
| `GET/PUT /settings/retention` | new (B3, B6) | `RetentionSettings` |
| `GET/PUT /settings/memory` | changed (B3) | no retention fields |
| `GET/PUT /security/retention` | deleted (B3) | — |
| `PUT /config` | behaviour (B3) | refuses `storage.retention` at any depth |
| `DELETE /credentials/{key}` | behaviour (B7) | no password token |

Stored data, behavioural level: the browser sign-in set per account (in `config.json`); last-used times (memory only); one new retention switch (`storage.retention.memory_retros_forever`, default false). Nothing else new is stored. The screen keeps unsaved drafts in page memory only; nothing is written to browser storage except what exists today (verbose chat, now shown in Profile).

---

## User Stories & Acceptance Criteria

### User Story 1 — Find any setting quickly, and keep old links working (Priority: P1)

A person running Omnipus wants to find a setting by the job it does ("my models", "what agents may do", "the server") instead of scanning 13 tabs in a row. Today the tabs give no hint of what belongs together, a reload jumps back to the first tab (#1154), and links in other screens point at tab names that will no longer exist.

**Why this priority**: every other story happens inside this structure; without it nothing else is reachable.

**Independent Test**: open Settings with no address parameters, with each old `?tab=` value, with the God Mode link and on a 390 px wide screen; compare what opens against the tables in FR-001 and FR-003.

**Acceptance Scenarios**:

1. **Given** a signed-in person, **When** they open Settings with no section named, **Then** the list shows 12 sections in three groups (AI & agents: Providers, Memory & recaps, System agents, Third-party agents, Limits; Security: Protection, Sandbox & network, Keys & secrets, Sign-in; System: Gateway, Data & retention, About) and Providers is open.
2. **Given** Sandbox & network is open, **When** the page is reloaded, **Then** Sandbox & network is open again.
3. **Given** a link made for the old tabs (for example `?tab=performance`), **When** it is opened, **Then** the section that now holds those settings opens (Limits).
4. **Given** the "No context length" link for a provider and model, **When** it is followed, **Then** Limits opens with Model overrides expanded and a new row filled with that provider and model.
5. **Given** God Mode is on and the sidebar pill shows, **When** the pill is clicked, **Then** Protection opens with the God Mode button focused.
6. **Given** an old backup or restore link, **When** it is opened, **Then** Data & retention opens with the notice "Backup and restore were removed."
7. **Given** an old link to the Chat tab, **When** it is opened, **Then** Profile opens at Chat display.
8. **Given** the search box, **When** the person types "web search", **Then** the result says it moved to Skills & Tools and links there.
9. **Given** a 390 px wide screen, **When** the person taps Gateway in the list, **Then** only Gateway shows, with a "Settings" back button.
10. **Given** the search box, **When** the person types a word no setting contains, **Then** "No settings match" shows with the typed word.
11. **Given** the person moved from Gateway to Limits, **When** they press the browser's Back button, **Then** Gateway opens.

### User Story 2 — Change several values and save them together (Priority: P0)

A person wants to know whether a change is saved. Today six save models coexist: silent autosave, a Save button, a delayed confirm, instant clicks, a restart dialog and password dialogs. With this story every value waits for **Save changes**, and every button acts at once.

**Why this priority**: it is the fix for the inventory's problem 2 and the rule every section depends on.

**Independent Test**: change values in one section, then Save, Discard, leave, fix an invalid value and force one write to fail; check the Save bar text and the stored values after each step.

**Acceptance Scenarios**:

1. **Given** Gateway shows saved values, **When** the person changes Port, **Then** a Save bar appears saying "1 unsaved change" with Discard and Save changes, and nothing is stored yet.
2. **Given** one unsaved change, **When** the person presses Save changes, **Then** the value is stored, the bar says "Saved at" and the time, and the value is still there after opening another section and coming back.
3. **Given** two unsaved changes, **When** the person presses Discard, **Then** both fields show their last saved values and the bar disappears.
4. **Given** Port holds 80, **When** the person looks at the Save bar, **Then** Save changes is unavailable, the bar says "Fix 1 value before saving", and the field says "Enter a whole number from 1,024 to 65,535."
5. **Given** the person changed Port and then typed the old value back, **When** they look at the page, **Then** the Save bar is gone.
6. **Given** one unsaved change in Limits, **When** the person clicks another section, **Then** they are asked whether to leave without saving; choosing to stay keeps them in Limits with the change.
7. **Given** two changes going to two different places on the server, **When** one is refused, **Then** the other is shown as saved, the refused one keeps its value and shows the server's own message under its field, and the bar says "1 change could not be saved".
8. **Given** Memory & recaps, **When** the person picks a different recap model, **Then** nothing is stored until Save changes.
9. **Given** the person lowered Max tool calls per turn, **When** they press Save changes, **Then** the list of affected agents is shown before anything is stored.
10. **Given** the person chose All interfaces for Bind address, **When** they press Save changes, **Then** they are asked to confirm before anything is stored.

### User Story 3 — Type the password once per Save (Priority: P1)

A person saving a protected value wants to type the password once, not once per value.

**Why this priority**: without it the single save rule doubles the friction it was meant to remove.

**Independent Test**: in Sandbox & network change two password-marked values and Save; count password prompts and server writes.

**Acceptance Scenarios**:

1. **Given** two password-marked changes in Sandbox & network, **When** the person presses Save changes, **Then** one password prompt lists both settings by name, and after the right password both are stored.
2. **Given** the password prompt, **When** a wrong password is entered, **Then** the prompt says the password is incorrect, stays open, and the changes stay unsaved.
3. **Given** the password prompt, **When** the person cancels, **Then** nothing is stored and every change, including a typed key in a side panel, is still on the page.
4. **Given** a hosted or desktop install (sign-in through omnipus.ai), **When** a password-marked change is saved, **Then** a plain confirmation appears instead of a password prompt.
5. **Given** Providers, **When** the person changes Active transcriber and saves, **Then** the password is asked once.
6. **Given** a lowering of Max tool calls per turn whose affected agents changed after the password was given, **When** the server reports the new list, **Then** the new list is shown and the password is asked again before saving.
7. **Given** changes that carry no Password badge, **When** they are saved, **Then** no password prompt appears.

### User Story 4 — Know which saved changes wait for a restart (Priority: P1)

A person wants one clear signal when a saved change only takes effect after a gateway restart, and one place to restart.

**Why this priority**: today the signal appears in six shapes and misses some settings, so people believe a security setting is in force when it is not.

**Independent Test**: save each restart-marked setting in turn; check the window, the banner, its list, and that a restart clears it.

**Acceptance Scenarios**:

1. **Given** the person changed Port, **When** they save, **Then** a "Restart now?" window lists Port and offers Later and Restart now.
2. **Given** that window, **When** the person chooses Later, **Then** a banner on every section says "1 saved change waits for a gateway restart" and focus moves to its Restart now button.
3. **Given** the banner, **When** the person chooses Show changes, **Then** each waiting setting is listed with its running value and its saved value.
4. **Given** the person saved a new Filesystem model, **When** the banner is shown, **Then** Filesystem model is in its list.
5. **Given** the person turned the network proxy for shell commands on or off, **When** the banner is shown, **Then** that setting is in its list.
6. **Given** Protection, **When** the person changes Defense level and saves, **Then** no restart badge, window or banner appears, because the change applies at once.
7. **Given** the banner, **When** the restart finishes, **Then** the banner disappears and focus moves to the section heading.
8. **Given** a saved Port change waiting for a restart, **When** the person saves the old Port again, **Then** the banner disappears without a restart.

### User Story 5 — Decide how long chats and memory are kept (Priority: P1)

A person wants chat history cleaned up sooner than memory, set in one place, with the option to keep memory forever (S-Q5 B, S-Q13 A). Today the same chat value can be edited in two tabs with different rules.

**Why this priority**: these values delete data on a schedule; a wrong value or a second writer deletes what the person wanted kept.

**Independent Test**: set each value at and beyond its limits through the screen and through the server; run the nightly sweep and check what was deleted.

**Acceptance Scenarios**:

1. **Given** a fresh install, **When** Data & retention opens, **Then** "Keep chat history for" shows 90 days, "Keep memory for" shows 180 days and Keep forever is off.
2. **Given** Data & retention, **When** the person saves 30 days of chat history and 365 days of memory, **Then** both are stored and shown after a reload.
3. **Given** the person turned Keep forever on and saved, **When** the nightly sweep runs, **Then** no memory is deleted, and the memory day field stays switched off.
4. **Given** "Keep chat history for", **When** the person types 366, **Then** the field says "Enter a whole number from 1 to 365." and Save changes is unavailable.
5. **Given** Memory & recaps, **When** it opens, **Then** it shows no retention fields, only a link row to Data & retention.
6. **Given** an install whose config file says to keep everything forever, **When** Data & retention opens, **Then** a notice says chat history and memory are both kept forever because of that setting, and that the day counts apply once it is removed.
7. **Given** a script, **When** it tries to change retention through the general configuration write, **Then** it is refused with a message naming the retention address, and nothing changes.
8. **Given** a script, **When** it sends a retention value to the memory settings, **Then** it is refused, and nothing changes.
9. **[Pending FQ-1]** **Given** memory kept for 730 days and a reflection 400 days old, **When** an agent recalls something that reflection holds, **Then** the reflection can be found.

### User Story 6 — Find moved controls, and see nothing that does not work (Priority: P1)

A person who knew the old tabs wants every control in a predictable new place: God Mode beside Auto-approve, voice input beside the model providers, verbose chat in Profile. They should see no control that does nothing (device pairing) or no longer exists (backup and restore).

**Why this priority**: an unmapped control is a lost feature; a dead control is a false promise.

**Independent Test**: walk the 88-row map (design-notes §2) and confirm each control is where its row says.

**Acceptance Scenarios**:

1. **Given** Protection, **When** it opens, **Then** God Mode shows as a "Turn on God Mode…" button next to Auto-approve, and Gateway has no God Mode control.
2. **Given** the God Mode button, **When** the person presses it, **Then** it asks for the password and acts at once, and the Save bar is not involved.
3. **Given** Providers, **When** it opens, **Then** a Voice input group shows Active transcriber and the key for each voice service.
4. **Given** Profile, **When** it opens, **Then** Chat display holds the Verbose chat switch, and Settings has no Chat section.
5. **Given** Protection, **When** it opens, **Then** a notice says tool permissions and skill trust are in Skills & Tools, with a link there.
6. **Given** an install with device pairing switched on in its config file, **When** Settings opens, **Then** no device pairing control appears anywhere.
7. **Given** a stored key in Keys & secrets, **When** the person deletes it, **Then** they confirm once and the key is gone; no password is asked.
8. **Given** Gateway, **When** the person presses Rotate for the gateway token, **Then** they are asked to confirm first, because every app and program using the token is signed out.
9. **Given** Limits, **When** it opens, **Then** it holds three groups: Context and tool results, Model overrides, and Agents and goals.
10. **Given** the 88-row map, **When** each row's control is looked for, **Then** it is in the place the row names, or absent for the rows marked Removed, Hidden or Moved out.

### User Story 7 — [#1096] Stay signed in on several browsers (Priority: P1)

A person wants to use Omnipus on a laptop and a phone at the same time. Today signing in on one signs the other out (#689, #1595). Up to 10 browsers may be signed in at once; an 11th sign-in ends the one used longest ago (Q1 A, S-Q14 A). Sign out ends this browser only (Q2 A). Changing the password ends every sign-in. Nobody is signed out by the update (Q4 A).

**Why this priority**: founder-set P1 (#1096 epic); it removes a daily annoyance and a test-script hazard.

**Independent Test**: sign in from up to 11 separate browsers or cookie jars and check which remain signed in after each step.

**Acceptance Scenarios**:

1. **Given** browser A is signed in, **When** the same person signs in on browser B, **Then** both A and B stay signed in.
2. **Given** 10 browsers are signed in, **When** an 11th signs in, **Then** the one used longest ago is signed out and the other nine stay signed in.
3. **Given** browsers A and B are signed in, **When** the person chooses Sign out on A, **Then** A is signed out and B stays signed in.
4. **Given** browsers A and B are signed in, **When** the person changes the password on A, **Then** both are signed out.
5. **Given** a person signed in before the update, **When** the update is installed and they continue working, **Then** they stay signed in.
6. **Given** a sign-in made 24 hours ago or earlier, **When** a request is made with it, **Then** it is refused.
7. **Given** browser B has a chat open with a live connection, **When** its sign-in is ended from browser A, **Then** B's live connection closes within 5 seconds and its next action leads to the sign-in screen.
8. **Given** a page planted a second cookie with the sign-in cookie's name, **When** the next request arrives, **Then** the sign-in protection still answers as it does today (clears the planted cookie, re-sends the genuine one, refuses changes).

### User Story 8 — [#1096] See and end signed-in browsers (Priority: P1)

A person who lost a phone, or used a shared computer, wants to see where they are signed in and end the ones they no longer use (Q3 A, S-Q9 A).

**Why this priority**: the list and the End button are what make multi-browser sign-in safe.

**Independent Test**: with three browsers signed in, open Settings → Sign-in on one of them; end one, then sign out the rest.

**Acceptance Scenarios**:

1. **Given** three browsers are signed in, **When** Sign-in opens on one of them, **Then** three rows show browser and system, and last use; this browser is marked "This browser" and has no End button.
2. **Given** that list, **When** the person ends another browser and confirms, **Then** that row disappears and that browser is signed out.
3. **Given** that list, **When** the person chooses Sign out other browsers and confirms, **Then** only this browser remains, and the confirmation said "You stay signed in here".
4. **Given** only this browser is signed in, **When** Sign-in opens, **Then** this browser's row shows and below it "No other browsers are signed in".
5. **Given** a row whose browser was already signed out elsewhere, **When** the person ends it, **Then** the list refreshes and says "That browser was already signed out."
6. **Given** the list cannot be loaded, **When** Sign-in opens, **Then** an error notice names what failed and offers Retry.
7. **Given** a script using the command-line token, **When** it asks for the list, **Then** it is refused, because only a signed-in person may see or end sign-ins.

### User Story 9 — Every section loads, fails and reads the same way (Priority: P2)

A person, including one using only a keyboard, a screen reader or a phone, wants every section to behave the same way when it loads, fails or is empty.

**Why this priority**: consistency is most of the redesign's value, but each item is small.

**Independent Test**: force loading, error, empty and offline on every section; drive the page by keyboard only and at 390 px.

**Acceptance Scenarios**:

1. **Given** a section is still loading, **When** it is shown, **Then** it says "Loading" with the section name and shows placeholder cards.
2. **Given** a section's data cannot be loaded, **When** it is shown, **Then** an error notice says what failed, the observed cause and one thing to do, offers Retry, and Save is unavailable.
3. **Given** the gateway does not answer, **When** Settings opens, **Then** one error notice above the sections says values may be out of date.
4. **Given** keyboard only, **When** the person presses Tab once on Settings, **Then** "Skip to settings content" is focused.
5. **Given** a 390 px wide screen, **When** any section is shown, **Then** every control is at least 44 by 44 px and nothing scrolls sideways.
6. **Given** a screen reader, **When** the Save bar appears or a save finishes, **Then** the change is announced.
7. **Given** any section with grouped lists, **When** it opens, **Then** every group starts collapsed.

### Edge Cases

- **Two browser tabs editing the same section.** Each tab saves only the fields it changed. The later save wins per field; neither tab's unchanged fields overwrite the other's.
- **The gateway restarts while the Save bar shows.** Drafts stay on the page; the next Save goes to the restarted gateway.
- **A Save during a running restart.** The write fails with a connection error; drafts stay, the bar says the change could not be saved, and the error says the gateway is not answering.
- **Keep forever turned off again.** The memory day count applies again from the next nightly sweep, and reflections older than it are then deleted. The switch's description says so before saving.
- **A day count stored outside the new range by hand-editing the config file.** It is shown as stored, marked invalid, and Save is unavailable until it is fixed.
- **[#1096] The browser marked "This browser" is ended by another browser.** Its next request is refused and the page goes to the sign-in screen (#1146 rider: an unsent message must not be lost silently).
- **[#1096] The gateway restarts.** Every live sign-in stays valid. Last use shows the sign-in time until the browser is used again.
- **[#1096] Two sign-ins arrive at the same moment with 10 already live.** One sign-in is ended per new sign-in, and the set never holds more than 10.
- **[#1096] The desktop app's claim step** gives the app the same sign-in as the system browser that completed it; the list shows it once.
- **A side panel (Configure provider, Add key) is open with a typed key when the password prompt is cancelled.** The panel and the typed key stay (#1153).
- **Device pairing switched on in the config file.** Still hidden in Settings (#1916); the old `?tab=devices` link opens Sign-in.
- **Skills & Tools has not landed when this package is cut.** Settings keeps the tool grid, web search and skill trust in their old places until it lands (S-Q3 A); see FR-060.

---

## UI Screens and States

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Settings shell (list + search) | List shows at once; open section shows its loading state | — (12 sections always exist) | Gateway offline: one error Callout above the sections (on a phone, above the open section) | Some sections failed: only those show their error | Section open, address names it |
| Providers | Spinner "Loading Providers…" + 3 Skeleton cards | Kit Empty "No providers connected" with Connect a provider | Callout: cause, Fix, Retry; Save off | Provider list loaded but voice list failed: voice group shows its own error + Retry | Default model, providers, Voice input |
| Memory & recaps | Spinner + Skeleton | — (always has values) | Callout + Retry | Providers list failed: model picker allows a typed model (today's behaviour) | Recaps, models, Recap limits (collapsed), link row to Data & retention |
| System agents | Spinner + Skeleton rows | — | Callout + Retry | One agent missing: "Not found on this install." row | Three rows with Edit |
| Third-party agents | Spinner + Skeleton | — | Callout + Retry | A refused field: inline error under it | Three programs |
| Limits | Spinner + Skeleton | — | Callout + Retry | "Some saved values are not in use yet" info Callout (pending apply) | Three groups |
| Protection | Spinner + Skeleton | — | Callout + Retry | God Mode status failed: God Mode notice with Retry; rest works | Health, Approvals, God Mode, Prompt injection defense, Rate limits, Audit log, moved-items Callout |
| Sandbox & network | Spinner + Skeleton | — | Callout + Retry | Proxy failed to start / shell limit set on the server / no kernel sandbox: warning Callouts | Sandbox, Allowed folders, Internal network addresses, Network proxy |
| Keys & secrets | Spinner + Skeleton | Kit Empty "No keys stored" with Add key | Callout + Retry | — | Master key, key rows |
| Sign-in [#1096] | Spinner + Skeleton rows | This browser's row + Empty "No other browsers are signed in" | Callout + Retry | — | Rows, limit line, Sign out other browsers |
| Gateway | Spinner + Skeleton | — | Callout + Retry; Save off | Unauthenticated access on: error Callout with the fix | Network, token, Other, Restart, Remote access (collapsed) |
| Data & retention | Spinner + Skeleton | — | Callout + Retry | Storage counts failed: counts show the error, retention still editable | Counts, How long data is kept, Danger zone |
| About | Spinner | — | "System info could not be loaded" Callout + Retry | — | Version, build, system info + Copy, repository link |
| Save bar | — | hidden with no changes | "N changes could not be saved" | "Fix N values before saving" | "Saved at HH:MM" |
| Restart banner | — | hidden with nothing waiting | list could not be loaded: banner says so with Retry | — | "N saved changes wait for a gateway restart" |

Wording follows the wireframe (`app.js`) and `omnipus-voice`. Where this table and the wireframe differ, the wireframe wins on wording and this table wins on states.

**Badge corrections from the code** (code wins; the wireframe is otherwise followed):

| Row | Wireframe | Code fact | Spec |
|---|---|---|---|
| Active transcriber (Providers) | no badge (`app.js:122`) | password-protected (`rest_integrations_roles.go:365`) | **Password** badge |
| Defense level (Protection) | Needs restart (`app.js:230`) | hot-reloaded (`rest_prompt_guard.go`; excluded in `rest_pending_restart.go:20`) | no badge |
| Shell workspace limit (Sandbox & network) | Password only (`app.js:248`) | `sandbox.workspace_path_guard` is restart-gated (`rest_pending_restart.go:51`) | **Needs restart** + Password |
| Add a folder / allowed folders (Sandbox & network) | Password only (`app.js:254`) | `sandbox.allowed_paths` is restart-gated (`rest_pending_restart.go:37`) | **Needs restart** + Password |
| Delete key (Keys & secrets) | confirm only | password today (`rest_settings.go::deleteCredential`) | confirm only, after B7 |
| Sample browser row "Last used 3 days ago" (`app.js:286`) | multi-day sign-ins | a sign-in lasts 24 h (`session_cookie.go::SessionCookieMaxAge = 86400`) | rows can only be under 24 h old; FQ-3 |

## User Journey

1. Entry: account menu → Settings, the header gear on the Omnipus chat, a link from another screen, or an old bookmark (US-1 AS1–AS7).
2. The person finds the section in the grouped list or by search (US-1 AS8, AS10). On a phone the list comes first (AS9).
3. They change values; the Save bar counts them and says which need a restart or the password (US-2 AS1, AS5).
4. They press Save changes. Pre-save confirms run first (US-2 AS9, AS10), then one password prompt if needed (US-3), then the writes.
5. If something needs a restart: "Restart now?" (US-4 AS1); Later leaves the banner (AS2).
6. Buttons act at once with their own confirm (US-6 AS2, AS7, AS8; US-8 AS2, AS3).
7. Leaving with unsaved changes asks first (US-2 AS6).

## Accessibility and Keyboard

- One focus ring everywhere: the product's single `:focus-visible` rule (1 px gold outline, 2 px offset); no component draws its own (`omnipus-design-system` skill, focus-visible ownership).
- "Skip to settings content" is the first Tab stop; it moves focus without changing the address (R2-O1).
- The section list is a list of links with `aria-current` on the open one. Radio groups have one Tab stop and arrow keys. Switches are `role="switch"` with `aria-checked`. Collapsible groups use `aria-expanded` and `aria-controls`.
- Dialogs stack: Escape or Cancel closes only the top one and returns focus to the control that opened it. After "Restart now?" closes with Later or Escape, focus goes to the banner's Restart now button. After a restart completes, focus goes to the section heading. When an opener no longer exists, focus goes to the section title, never the page body.
- Every control is named by its visible label. Number fields state range and unit in their description. Program fields name the program ("Codex program path"); effort pickers name their model ("Effort for the default model"); icon buttons name their target ("Remove /home/you/projects"); End buttons name the browser ("End Safari on iPad").
- The Save bar and the saved signal are live regions; errors use `role="alert"`; search results update a polite live region.
- At 390 px every control is at least 44 × 44 px (`--target-touch-minimum`; a switch keeps its 36 × 20 track inside a 44 × 44 tap area); nothing scrolls sideways.
- Colour is never the only signal: Needs restart, Password and This browser badges carry an icon and a word.
- Skeleton pulse, spinner and row highlight stop under reduced motion.

## Design-System Components

Every element uses a catalogued component, checked in `design-system/catalog.json` at `c2f8f8e735`: PageHeader, SearchField, Button, IconButton, Tooltip, Field, Card, Input, Select, ModelSelector, ModelEffortPicker, Switch, RadioGroup, SegmentedControl, Badge, Callout, ListGroup, DisclosureRow, Empty, Skeleton, Spinner, CopyButton, AutoSaveIndicator, ConfirmDialog, Dialog, Sheet, DropdownMenu, FilterMenu, Table, BrandIcon, BrandDisclaimer, FormError — each found. The mapping per job is design-notes §4. No new kit component is needed.

- `Item` (R41) is **not** in the catalog (Grep count 0). Rows for providers, keys and browsers use a `Card` row layout until `Item` exists.
- The Save bar is a screen layout (a floating `Card` with two `Button`s and the `AutoSaveIndicator`), composed once in the Settings screen. It is not catalogued; if another page later needs it, it becomes a catalogued composite then.
- The restart banner is a `Callout` (warning) with two `Button`s. The "Restart now?" window is the existing `GatewayRestartModal`. The password prompt is the existing `ReAuthDialog`.
- Inline styles in the wireframe files are drawing shortcuts and must not be copied.

## Security and User Promises

- **Focus areas touched (security-lead on-demand review regardless of size):** gateway auth and sign-ins [#1096] (`pkg/gateway/rest_auth.go`, `pkg/gateway/middleware/session_cookie.go`, `planted_cookie.go`, WebSocket authentication); the credential vault's delete gate (B7); the reauth consent rule (B4: unchanged, pinned by a guard test); retention deletion (B6).
- **B7 removes a password gate set by an accepted ADR** (ADR-022). The founder answered S-Q11 B; FQ-2 asks for confirmation now that the server effect is known. Delete stays behind sign-in, the existing admin and bypass guards, a confirm, and the audit log.
- **God Mode** keeps its own password prompt and restart signal and never travels in a Save batch (FR-051).
- **Auto-approve** keeps its password; the help text says each chat can still turn it off with its Auto button (ADR-092).
- **Tool permissions and skill trust** leave Settings only when Skills & Tools has landed (S-Q3 A, FR-060); the two-layer policy (Hard Constraint #6) is not changed by this package.
- **Retention deletes data.** The server enforces the ranges (FR-042); the screen never sends a day count while Keep forever is on (FR-040); the legacy keep-everything switch is honoured, never silently dropped (FR-048).
- **[#1096] threat model:** Contract Changes C-1.
- **User promises in `docs/security.md`** about sign-in: updated by DOC-009.

---

## Behavioral Contract

Primary flows:
- When Settings opens with no section named, the system shows Providers.
- When Settings opens with a section named (new or old name), the system shows the section that holds those settings now.
- When a value is changed, the system shows the Save bar with the number of changed values, how many need a restart and how many need the password.
- When Save changes is pressed with valid values, the system runs the pre-save confirms, then at most one password prompt, then stores every changed value.
- When a stored value needs a restart, the system offers "Restart now?" at once and keeps a banner on every section until the restart, or until the value is saved back to its running value.
- When a button is pressed (Test, Check, Rotate, God Mode, Delete, End, Sign out other browsers), the system acts at once, after a confirm when it destroys or signs out something.
- When Keep forever is on for memory, the system deletes no reflection.
- [#1096] When a person signs in, the system keeps their other browser sign-ins, up to 10 in total.
- [#1096] When an 11th browser signs in, the system ends the sign-in used longest ago.
- [#1096] When a sign-in is ended, the system refuses that browser's next request and closes its live connections within 5 seconds.

Error flows:
- When a value is outside its range, the system names the range under the field and keeps Save unavailable until it is fixed.
- When one of several writes fails, the system keeps the failed value as a change, shows the server's message under it, and marks the others saved.
- When the password is wrong, the system says so in the prompt and keeps every change.
- When the password prompt is cancelled, the system stores nothing and keeps every change, including keys typed in side panels.
- When a section cannot load, the system shows what failed with Retry and does not allow saving that section.
- When a script writes retention through the general configuration write or the memory settings, the system refuses it and changes nothing.
- [#1096] When a sign-in id is unknown, malformed, already ended or expired, the system gives one "not found" answer.
- [#1096] When someone asks to end the sign-in they are using, the system refuses and points to Sign out.

Boundary conditions:
- When chat history is set to 1 or 365 days, the system accepts it; 0 or 366 is refused.
- When memory is set to 1 or 730 days, the system accepts it; 0 or 731 is refused.
- [#1096] When exactly 10 browsers are signed in, the system ends none; the 11th sign-in ends exactly one.
- [#1096] When a sign-in is exactly 24 hours old, the system refuses it.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not save any value without Save changes being pressed, because the single save rule is the feature (S-Q2 A). This includes recap model slots, Gateway fields, rate limits and the old delayed-confirm Performance fields.
- The system must not put God Mode in a Save batch, because it is the highest-blast-radius switch and always acts alone with its own password.
- The system must not show device pairing in Settings, even when the config file switches it on, because it does not work end to end (#1916).
- The system must not show issue numbers, requirement tags (R30, S-Q8) or "contact support" in any on-screen text (design-notes §10 wording rule; omnipus-voice).
- The system must not remove the tool grid, web search or skill trust from Settings before Skills & Tools has landed, because the controls would become unreachable (S-Q3 A).
- The system must not keep the typed password after the one consent request of a Save, because a password in page memory outlives its purpose (B4).
- The system must not offer "Sign out everywhere"; Sign out ends this browser only (#1096 Q2 A).
- The system must not store or show a browser's IP address or raw User-Agent [#1096], because the list needs only a label to be useful (T8).
- The system must not end app (bearer) tokens when browsers are ended or signed out [#1096], because scripts use those separately (founder question text: "App tokens (used by scripts) already allow 10 at once").
- The system must not delete memory while Keep forever is on, nor chat history or memory while the legacy keep-everything switch is on.
- The system must not add a new kit component; the catalog already covers every job.
- The system must not show a "Needs restart" badge on a setting that applies without a restart (Defense level).

### Machine-Verifiable Constraints

**Error Codes / Messages**:
- `PUT /settings/retention` with `session_days` outside 1–365 → 400 `ErrorResponse`, message `session_days must be a whole number from 1 to 365`; `memory_retros_days` outside 1–730 → 400, `memory_retros_days must be a whole number from 1 to 730`. A body with `keep_everything_forever` or an unknown field → 400. `{}` → 400 `at least one field required`.
- `PUT /config` whose body reaches `storage.retention` at any depth → 400, message naming `PUT /api/v1/settings/retention`; config file unchanged.
- `PUT /settings/memory` carrying `session_days` or `memory_retros_days` → 400; config file unchanged.
- `GET` or `PUT /security/retention` → 404 (route removed). `POST /security/retention/sweep` unchanged.
- `DELETE /credentials/{key}` with no `X-Reauth-Token`, by a signed-in admin → today's success status; the key is gone.
- [#1096] `GET /auth/browser-sessions` → 200 `BrowserSessionList`; with the CLI token, dev bypass or the legacy env token → 403.
- [#1096] `DELETE /auth/browser-sessions/{id}` → 204 for another live sign-in; 404 for unknown, malformed, ended or expired; 409 for the caller's own.
- [#1096] `POST /auth/browser-sessions/sign-out-others` → 200 `{ended: n}`; 409 when the request carries no browser sign-in.
- [#1096] A request whose sign-in was issued 24 h ago or earlier → 401 on authenticated routes; treated as anonymous on optional-auth routes, exactly as an unknown cookie today.

**Performance Bounds**:
- [#1096] Resolving a browser sign-in MUST cost at most 1 bcrypt comparison per request, for an id-prefixed cookie and for a legacy cookie alike.
- [#1096] Ending a sign-in MUST close its WebSocket connections within 5 s.
- The Save bar MUST appear within 100 ms of the change that triggers it (no server round trip).

**Scope Boundaries**:
- The system MUST NOT add an agent tool or a `set_config` path for any value in this spec.
- The system MUST NOT change Skills & Tools, Tasks and Team, or device pairing (out of scope).
- The system MUST NOT change the sign-in lifetime of 24 hours (FQ-3 asks whether a later package should).

## Integration Boundaries

### Gateway REST API (SPA ↔ gateway)
- **Data in / out**: section reads and partial writes; the endpoints in "API and Data".
- **Contract**: `contracts/openapi.yaml` and the schemas named in Contract Changes.
- **On failure**: section error with Retry; per-field error on a refused write; gateway-offline Callout when nothing answers.
- **Development**: real gateway (`./omnipus gateway` on a test home).

### Gateway WebSockets (chat, live browser) [#1096]
- **Data in / out**: no new frames; the gateway closes a socket whose sign-in ended.
- **Contract**: `contracts/asyncapi.yaml` unchanged; closing uses the existing close behaviour.
- **On failure**: the SPA's existing reconnect then gets 401 and goes to sign-in.
- **Development**: real gateway.

### Config file and nightly sweeps (B3, B6)
- **Data in / out**: `storage.retention.*`, read by the sweeps on each tick.
- **Contract**: `RetentionSettings` on the wire; the `config.json` keys named there.
- **On failure**: a failed write is a 500 from `/settings/retention` with the cause; the sweep keeps using its last good config.
- **Development**: real sweep functions with a controlled clock in tests.

### Skills & Tools page (dependency, S-Q3 A)
- **Data in / out**: links only.
- **Contract**: the Skills & Tools route (owned by that package).
- **On failure**: if the page has not landed, the moved controls stay in Settings (FR-060).
- **Development**: real route once landed.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|---|---|---|
| 1 | Exact text of each error and notice not drawn in the wireframe (for example the legacy keep-everything notice, "That browser was already signed out.") | Invent wording | Use this spec's text; frontend-lead applies omnipus-voice; reviewers check the pattern |
| 2 | Which browsers and systems the label recognises | Pull in a User-Agent parsing library | No new dependency: a small fixed table (Chrome, Edge, Firefox, Safari, Opera; Windows, macOS, Linux, iOS, iPadOS, Android, ChromeOS); anything else "Unknown browser" (Hard Constraint #1) |
| 3 | How search ranks and which words match moved items | A fuzzy-search library | Plain case-insensitive substring match over label, description and the moved-items list; no new dependency |
| 4 | Recall window if FQ-1 is answered B | Leave FR-050 in | FR-050 and US-5 AS9 are dropped if the founder picks B |
| 5 | Whether drafts survive switching sections | Keep drafts across sections | No: the wireframe asks before leaving; drafts are per section (this is also what makes B4 hold) |

---

## BDD Scenarios

Expected values below come from this spec, the cited contract schemas, the wireframe and the founder answers — never from today's implementation. "the gateway" is a real gateway on a test home; "signed in" means a local-mode account unless the scenario says platform mode.

### Feature: Settings structure and links (User Story 1)

#### Scenario: Person opens Settings and sees 12 sections in three groups
**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a signed-in person on a fresh install
- **When** they open `/settings` with no parameters
- **Then** the section list shows, in order, the group "AI & agents" with Providers, Memory & recaps, System agents, Third-party agents, Limits; the group "Security" with Protection, Sandbox & network, Keys & secrets, Sign-in; the group "System" with Gateway, Data & retention, About
- **And** Providers is open and marked current in the list
- **But** no Devices, Chat, Integrations, Models, Performance or Security entry exists

#### Scenario: Reload keeps the open section
**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path
- **Given** Sandbox & network is open and the address carries `tab=sandbox`
- **When** the page is reloaded
- **Then** Sandbox & network is open

#### Scenario Outline: Old and new section links land in the right place
**Traces to**: User Story 1, Acceptance Scenarios 3, 6, 7
**Category**: Alternate Path
- **Given** a signed-in person
- **When** they open `/settings?tab=<tab>`
- **Then** `<lands>` is shown, with `<extra>`

**Examples**: Dataset D-3 (all rows).

#### Scenario: Unknown section name falls back to Providers
**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Edge Case
- **Given** a signed-in person
- **When** they open `/settings?tab=notasection`
- **Then** Providers is open and no error is shown

#### Scenario: The "No context length" link pre-fills a model override
**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Alternate Path
- **Given** a provider `openrouter` whose model `acme/m1` reports no context length
- **When** the person follows `/settings?tab=models&provider=openrouter&model=acme/m1`
- **Then** Limits is open, the Model overrides group is expanded, and a new unsaved row shows provider `openrouter` and model `acme/m1` with an empty context length
- **And** the Save bar shows "1 unsaved change"

#### Scenario: The God Mode pill lands on the God Mode button
**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Alternate Path
- **Given** God Mode is on and the sidebar pill is visible
- **When** the person clicks the pill
- **Then** Protection is open and the God Mode button has keyboard focus
- **And** the address no longer carries `focus=god-mode` after focus moved

#### Scenario: Searching for a moved setting says where it went
**Traces to**: User Story 1, Acceptance Scenario 8
**Category**: Happy Path
- **Given** Settings is open
- **When** the person types "web search" in Search settings
- **Then** a result reads that web search moved to Skills & Tools, with a link that opens Skills & Tools
- **And** the result count is announced in a polite live region

#### Scenario Outline: Search results
**Traces to**: User Story 1, Acceptance Scenarios 8, 10
**Category**: Alternate Path
- **Given** Settings is open
- **When** the person types `<query>` in Search settings
- **Then** the results show `<result>`

**Examples**: Dataset D-13.

#### Scenario: Search with no match shows an empty state
**Traces to**: User Story 1, Acceptance Scenario 10
**Category**: Error Path
- **Given** Settings is open
- **When** the person types "zzqx" in Search settings
- **Then** the kit Empty state says no settings match "zzqx"

#### Scenario: On a phone the list comes first
**Traces to**: User Story 1, Acceptance Scenario 9
**Category**: Alternate Path
- **Given** a 390 px wide window showing the Settings list
- **When** the person taps Gateway
- **Then** only the Gateway section shows, with a "Settings" back button at the top
- **And** tapping that button shows the list again

#### Scenario: Browser Back moves to the previous section
**Traces to**: User Story 1, Acceptance Scenario 11
**Category**: Alternate Path
- **Given** the person opened Gateway, then Limits, with no unsaved changes
- **When** they press the browser's Back button
- **Then** Gateway is open; Forward opens Limits again

#### Scenario Outline: Links from other screens keep working
**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Alternate Path
- **Given** the person is on `<screen>`
- **When** they activate `<control>`
- **Then** `<lands>` is open

**Examples**:

| screen | control | lands |
|---|---|---|
| Omnipus chat header | Omnipus settings gear | System agents |
| Agent profile of a third-party worker | Change in Settings | Third-party agents |
| Skills screen | permissions link (old `tab=security`) | Protection |
| Default model card | No context length → Set it in Limits | Limits, Model overrides expanded |
| `/app/policies` | (redirect) | Settings, Providers |

### Feature: One save rule (User Story 2)

#### Scenario: A changed value shows the Save bar and stores nothing
**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path
- **Given** Gateway shows Port 8080 as saved
- **When** the person types 8081 in Port
- **Then** the Save bar says "1 unsaved change", "1 needs a gateway restart", with Discard and Save changes
- **And** `GET /config` still returns port 8080

#### Scenario: Save stores the value and it stays after leaving the section
**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path
- **Given** Port changed from 8080 to 8081 and not saved
- **When** the person presses Save changes (and Later in the restart window)
- **Then** `GET /config` returns port 8081, and the Save bar says "Saved at" followed by the time as HH:MM
- **And** after opening Limits and coming back, Port shows 8081

#### Scenario: Discard returns every change to its saved value
**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Alternate Path
- **Given** Port changed to 8081 and Log level changed to Debug, unsaved
- **When** the person presses Discard
- **Then** Port shows 8080, Log level shows Info, the Save bar is hidden, and nothing was written

#### Scenario Outline: An invalid value blocks saving and names the range
**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Error Path
- **Given** `<section>` is open
- **When** the person enters `<input>` in `<field>`
- **Then** the field shows `<message>`, Save changes is marked unavailable, and the bar says "Fix 1 value before saving"
- **And** pressing Save changes moves focus to `<field>` and sends nothing

**Examples**: Dataset D-10.

#### Scenario: A value changed back by hand is no longer counted
**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case
- **Given** Port was changed from 8080 to 8081
- **When** the person types 8080 back into Port
- **Then** the Save bar is hidden

#### Scenario: Typing in an Add field is not a change until Add is pressed
**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case
- **Given** Sandbox & network with no unsaved changes
- **When** the person types `/srv/data` in "Add a folder" without pressing Add
- **Then** the Save bar is hidden; after pressing Add it says "1 unsaved change"

#### Scenario: Leaving with unsaved changes asks first, and Stay keeps them
**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Alternate Path
- **Given** Limits with 1 unsaved change
- **When** the person clicks Gateway in the section list
- **Then** a confirm asks whether to leave without saving 1 change
- **And** choosing Stay keeps Limits open with the change, and the address still names Limits

#### Scenario Outline: Every way out asks first
**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Edge Case
- **Given** a section with 1 unsaved change
- **When** the person uses `<exit>`
- **Then** `<asks>`

**Examples**:

| exit | asks |
|---|---|
| another section in the list | the leave confirm |
| a search result | the leave confirm; Stay returns focus to the search field |
| the Profile link | the leave confirm |
| an app sidebar item | the leave confirm |
| browser Back | the leave confirm; Stay undoes the history step (no extra entry) |
| a typed address | the leave confirm |
| closing the tab | the browser's own leave prompt |

#### Scenario: Leave without saving drops the change
**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Alternate Path
- **Given** Limits with 1 unsaved change and the leave confirm open
- **When** the person chooses Leave
- **Then** Gateway opens and returning to Limits shows the saved value

#### Scenario: One of two writes fails
**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Error Path
- **Given** Limits with a changed Default context window and a changed Tries per goal, and the server refuses the context write with "default_context_window must be at least 1024"
- **When** the person saves (with the right password)
- **Then** Tries per goal shows as saved, Default context window keeps the typed value with `default_context_window must be at least 1024` under it, and the bar says "1 change could not be saved"

#### Scenario: A Save while the gateway is down keeps every change
**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Error Path
- **Given** Gateway with 1 unsaved change and the gateway process stopped
- **When** the person presses Save changes
- **Then** the change stays, the bar says "1 change could not be saved", and the error says the gateway is not answering and to check that it is running

#### Scenario: A recap model choice waits for Save
**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Happy Path
- **Given** Memory & recaps with a saved primary recap model
- **When** the person picks a different primary model
- **Then** `GET /settings/memory` still returns the old model and the Save bar says "1 unsaved change"

#### Scenario: Lowering the tool-call limit previews affected agents before saving
**Traces to**: User Story 2, Acceptance Scenario 9
**Category**: Alternate Path
- **Given** Max tool calls per turn saved as 50 and one agent with its own value 40
- **When** the person enters 30 and presses Save changes
- **Then** a confirm lists that agent before the password prompt and before any write

#### Scenario: Choosing All interfaces asks before saving
**Traces to**: User Story 2, Acceptance Scenario 10
**Category**: Alternate Path
- **Given** Bind address saved as Localhost only
- **When** the person selects All interfaces and presses Save changes
- **Then** "Listen on all interfaces?" opens before any write; Cancel stores nothing and keeps the change

#### Scenario: Only one gold button while the Save bar shows
**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Edge Case
- **Given** Providers with no changes shows Connect a provider as the gold button
- **When** the person changes the default model
- **Then** Save changes is the only gold button and Connect a provider is outlined

### Feature: One password prompt per Save (User Story 3)

#### Scenario: Two password-marked changes need one password
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path
- **Given** local mode, Sandbox & network with Filesystem model and Shell workspace limit changed
- **When** the person presses Save changes and enters the right password
- **Then** exactly one password prompt opened, listing "Filesystem model" and "Shell workspace limit" and nothing else
- **And** exactly one `POST /auth/reauth` and one `PUT /security/sandbox-config` were sent, and both values are stored

#### Scenario: The typed password is not kept
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Edge Case
- **Given** a Save whose password prompt was answered correctly
- **When** the prompt closes
- **Then** the password field's value is empty and no later request carries the password

#### Scenario: Wrong password
**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Error Path
- **Given** the password prompt for a Save
- **When** the person enters a wrong password
- **Then** the prompt stays open with "The password is incorrect." under the field, focus is in the field, and nothing was stored

#### Scenario: Too many password tries
**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Error Path
- **Given** the reauth limit (10 per minute) is used up
- **When** the person submits the password
- **Then** the prompt says too many tries were made and to wait a minute, and every change stays

#### Scenario: Cancelling the password prompt keeps every change and any typed key
**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Alternate Path
- **Given** Providers → Configure with a typed API key and the password prompt open over the panel
- **When** the person presses Cancel
- **Then** only the prompt closes; the panel stays open with the typed key, focus returns to its Save button, and nothing was stored

#### Scenario: Platform mode asks for a confirmation instead
**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Alternate Path
- **Given** a platform-mode install (omnipus.ai sign-in)
- **When** the person saves a change to Auto-approve
- **Then** a confirmation listing "Auto-approve" opens with no password field, and no `POST /auth/reauth` is sent

#### Scenario: Active transcriber needs the password
**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Happy Path
- **Given** Providers, Voice input, Active transcriber "OpenAI Whisper" saved
- **When** the person picks "Groq", presses Save changes and enters the right password
- **Then** the row carried a Password badge, the prompt listed "Active transcriber", and the new transcriber is stored

#### Scenario: A late change in the affected agents asks again
**Traces to**: User Story 3, Acceptance Scenario 6
**Category**: Edge Case
- **Given** a lowering of Max tool calls per turn was confirmed and the password given, and the server answers 409 with a different list of affected agents
- **When** the 409 arrives
- **Then** the new list is shown for confirmation, the password is asked again, and nothing was stored before that

#### Scenario: A Save with no password-marked value has no prompt
**Traces to**: User Story 3, Acceptance Scenario 7
**Category**: Happy Path
- **Given** Gateway with Log level changed
- **When** the person presses Save changes
- **Then** no password prompt opens and no `POST /auth/reauth` is sent

#### Scenario Outline: Each section sends its password-marked values to at most one protected endpoint
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Edge Case
- **Given** `<section>` with every password-marked value changed at once
- **When** the person saves with the right password
- **Then** exactly `<n>` protected request(s) are sent, to `<endpoint>`

**Examples**: Dataset D-8.

### Feature: One restart pattern (User Story 4)

#### Scenario: A Save that needs a restart offers it at once
**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path
- **Given** Port changed from 8080 to 8081
- **When** the person saves
- **Then** the "Restart now?" window lists "Port" and offers Later and Restart now

#### Scenario: Later leaves a banner on every section
**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** the "Restart now?" window after saving Port
- **When** the person chooses Later
- **Then** the banner "1 saved change waits for a gateway restart" shows with Restart now and Show changes, focus is on its Restart now button, and the banner also shows on Providers and About

#### Scenario: Show changes lists running and saved values
**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Happy Path
- **Given** Port saved as 8081 while the gateway runs on 8080
- **When** the person chooses Show changes
- **Then** the list reads Port, running 8080, saved 8081

#### Scenario Outline: The server lists every restart-gated setting that was saved
**Traces to**: User Story 4, Acceptance Scenarios 4, 5
**Category**: Happy Path
- **Given** a running gateway
- **When** `<key>` is saved as `<saved>` while it runs with `<running>`
- **Then** `GET /config/pending-restart` `<appears>`

**Examples**: Dataset D-4.

#### Scenario: Defense level applies at once
**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Edge Case
- **Given** Protection with Defense level Medium
- **When** the person selects High and saves
- **Then** the row carries no Needs restart badge, no "Restart now?" window opens, and no banner appears

#### Scenario: A failed reload still says a restart is needed
**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Error Path
- **Given** a write whose response says `requires_restart: true` for a setting not marked Needs restart (for example Defense level when the reload failed)
- **When** the response arrives
- **Then** the "Restart now?" window opens and names that setting

#### Scenario: The restart clears the banner
**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Happy Path
- **Given** the banner for Port
- **When** the person chooses Restart now and the gateway comes back
- **Then** the banner is gone and focus is on the open section's heading

#### Scenario: Saving the running value again clears the banner
**Traces to**: User Story 4, Acceptance Scenario 8
**Category**: Edge Case
- **Given** Port saved as 8081 while running on 8080
- **When** the person saves 8080
- **Then** `GET /config/pending-restart` returns an empty list and the banner is gone

#### Scenario: The restart list cannot be loaded
**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Error Path
- **Given** `GET /config/pending-restart` answers 500
- **When** a section opens
- **Then** the banner area says the list of waiting changes could not be loaded, with Retry

### Feature: Retention (User Story 5)

#### Scenario: Fresh-install defaults
**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a fresh install
- **When** Data & retention opens
- **Then** "Keep chat history for" shows 90, "Keep memory for" shows 180, and Keep forever is off
- **And** `GET /settings/retention` returns `session_days` 90, `memory_retros_days` 180, `memory_retros_forever` false, `keep_everything_forever` false

#### Scenario: Saving both day counts
**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Happy Path
- **Given** Data & retention with defaults
- **When** the person sets 30 and 365 and saves
- **Then** one `PUT /settings/retention` carrying both values is sent, and after a reload the fields show 30 and 365

#### Scenario Outline: The server enforces the ranges
**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Edge Case
- **Given** a signed-in script
- **When** it sends `PUT /settings/retention` with `<body>`
- **Then** the answer is `<status>` `<message>` and the stored values are `<stored>`

**Examples**: Datasets D-1 and D-2.

#### Scenario: Keep forever stops memory deletion
**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path
- **Given** `memory_retros_forever` true, `memory_retros_days` 180, and a reflection dated 200 days ago
- **When** the nightly retention sweep runs
- **Then** the reflection still exists
- **And** chat transcripts older than `session_days` were still swept

#### Scenario: With Keep forever on the day field is off and not sent
**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Alternate Path
- **Given** "Keep memory for" shows 999 (invalid)
- **When** the person turns Keep forever on and saves
- **Then** the day field is disabled with no error, Save was available, and the `PUT /settings/retention` body is `{"memory_retros_forever": true}` with no `memory_retros_days`

#### Scenario: Turning Keep forever off again resumes deletion
**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Edge Case
- **Given** Keep forever on, `memory_retros_days` 180, and a reflection dated 200 days ago
- **When** Keep forever is turned off and saved, and the nightly sweep runs
- **Then** that reflection is deleted, and the switch's description had said older reflections are deleted at the next sweep

#### Scenario: Memory & recaps holds only a link to retention
**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Alternate Path
- **Given** Memory & recaps
- **When** it opens
- **Then** it shows no day-count field, and a row "How long memory is kept" opens Data & retention when activated

#### Scenario: The legacy keep-everything switch is shown, not hidden
**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Edge Case
- **Given** `config.json` has `storage.retention.disabled: true`
- **When** Data & retention opens
- **Then** an info notice says chat history and memory are both kept forever because of a setting in the configuration file, and that the day counts below apply once it is removed
- **And** both day fields stay editable, and the nightly sweep deletes neither chats nor reflections

#### Scenario: The general configuration write refuses retention
**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Error Path
- **Given** a signed-in script
- **When** it sends `<body>` to `PUT /config`
- **Then** the answer is `<status>` and `config.json` is byte-identical to before

**Examples**: Dataset D-12.

#### Scenario: The memory settings refuse retention fields
**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Error Path
- **Given** a signed-in script
- **When** it sends `PUT /settings/memory` with `{"session_days": 30}`
- **Then** the answer is 400 and `config.json` is unchanged
- **And** the same holds for `{"memory_retros_days": 365}`

#### Scenario: The old retention route is gone
**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Error Path
- **Given** a signed-in script
- **When** it sends `GET /security/retention` and `PUT /security/retention`
- **Then** both answer 404, and `POST /security/retention/sweep` still answers as before

#### Scenario: Saving Gateway never sends retention
**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Edge Case
- **Given** Gateway with Port changed
- **When** the person saves
- **Then** the `PUT /config` body contains no `storage` key and the answer is 200

#### Scenario: A hand-edited value outside the range is shown and blocks Save
**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Edge Case
- **Given** `config.json` has `storage.retention.session_days: 500`
- **When** Data & retention opens
- **Then** "Keep chat history for" shows 500 with "Enter a whole number from 1 to 365." and Save changes stays unavailable until it is fixed

#### Scenario: [Pending FQ-1] An old reflection kept by the setting can be recalled
**Traces to**: User Story 5, Acceptance Scenario 9
**Category**: Edge Case
- **Given** `memory_retros_days` 730 and a reflection 400 days old containing the word "zeppelin"
- **When** an agent calls `recall_memory` with "zeppelin"
- **Then** the result includes that reflection

### Feature: Moved, removed and hidden controls (User Story 6)

#### Scenario: God Mode sits in Protection
**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path
- **Given** God Mode is available and off
- **When** Protection opens
- **Then** a "Turn on God Mode…" button shows after Approvals, and Gateway contains no God Mode control

#### Scenario: The God Mode button acts alone
**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Happy Path
- **Given** Protection with 1 unsaved Auto-approve change
- **When** the person presses "Turn on God Mode…" and enters the right password
- **Then** God Mode is turned on, the Auto-approve change is still unsaved, and the Save bar still says "1 unsaved change"

#### Scenario: Voice input lives in Providers
**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Happy Path
- **Given** voice services with and without keys
- **When** Providers opens
- **Then** a Voice input group shows Active transcriber and a key row per service; a key row opens a side panel with the key and show/hide only (no Test, no Remove)

#### Scenario: Verbose chat lives in Profile
**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Happy Path
- **Given** Verbose chat was on in this browser before the update
- **When** Profile opens
- **Then** Chat display shows Verbose chat on, and Settings has no Chat section

#### Scenario: Moved items are announced in Protection
**Traces to**: User Story 6, Acceptance Scenario 5
**Category**: Happy Path
- **Given** Skills & Tools has landed
- **When** Protection opens
- **Then** an info notice says tool permissions and skill trust are set in Skills & Tools, with a link that opens it

#### Scenario: Device pairing stays hidden when switched on
**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Edge Case
- **Given** `sandbox.experimental.device_pairing_enabled: true`
- **When** each of the 12 sections opens, and search runs for "pair"
- **Then** no pairing control appears; search says device pairing is not available in Settings at the moment

#### Scenario: Deleting a stored key needs one confirm
**Traces to**: User Story 6, Acceptance Scenario 7
**Category**: Happy Path
- **Given** Keys & secrets with the key `GITHUB_TOKEN`
- **When** the person presses Delete on that row and confirms
- **Then** no password prompt opened, the row is gone, and `GET /credentials` no longer lists it

#### Scenario: The server deletes a key without a password token
**Traces to**: User Story 6, Acceptance Scenario 7
**Category**: Happy Path
- **Given** a signed-in admin and a stored key `GITHUB_TOKEN`
- **When** `DELETE /credentials/GITHUB_TOKEN` is sent with no `X-Reauth-Token`
- **Then** the answer is today's success status, the key is gone, and an audit entry records the deletion

#### Scenario: Adding a key still needs the password
**Traces to**: User Story 6, Acceptance Scenario 7
**Category**: Error Path
- **Given** a signed-in admin
- **When** `POST /credentials` is sent with no `X-Reauth-Token`
- **Then** the answer is 403 and nothing is stored

#### Scenario: Rotating the gateway token asks first
**Traces to**: User Story 6, Acceptance Scenario 8
**Category**: Alternate Path
- **Given** Gateway
- **When** the person presses Rotate
- **Then** a confirm says every app and program using the token is signed out; Cancel keeps the old token

#### Scenario: Limits holds three groups
**Traces to**: User Story 6, Acceptance Scenario 9
**Category**: Happy Path
- **Given** Limits
- **When** it opens
- **Then** it shows the groups Context and tool results, Model overrides, and Agents and goals, each collapsed

#### Scenario Outline: Every inventory control is where the map says
**Traces to**: User Story 6, Acceptance Scenario 10
**Category**: Happy Path
- **Given** the 88-row map of design-notes §2 with the corrections in "Badge corrections from the code"
- **When** the control of row `<row>` is looked for
- **Then** it is `<place>`

**Examples**: Dataset D-9 (the qa-lead imports all 88 rows; D-9 lists the rows whose place changes behaviour).

#### Scenario: Controls whose new home has not landed stay put
**Traces to**: User Story 6, Acceptance Scenario 10
**Category**: Edge Case
- **Given** a build in which Skills & Tools has not landed
- **When** Protection opens
- **Then** the tool permission grid and skill trust are present in Settings and no "moved" notice is shown

### Feature: [#1096] Several browsers signed in (User Story 7)

#### Scenario: [#1096] Signing in on a second browser keeps the first
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path
- **Given** browser A is signed in
- **When** the same person signs in on browser B
- **Then** an authenticated request from A answers 200 and one from B answers 200

#### Scenario Outline: [#1096] The 11th sign-in ends the one used longest ago
**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Edge Case
- **Given** `<live>` live sign-ins with the last-use times in `<state>`
- **When** a new browser signs in
- **Then** `<ended>` is signed out and the set holds `<after>` sign-ins

**Examples**: Dataset D-5.

#### Scenario: [#1096] Sign out ends only this browser
**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Happy Path
- **Given** browsers A and B signed in
- **When** A sends `POST /auth/logout`
- **Then** A's next authenticated request answers 401 and B's answers 200

#### Scenario: [#1096] A password change ends every sign-in
**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Happy Path
- **Given** browsers A and B signed in
- **When** A changes the password
- **Then** authenticated requests from A and B both answer 401

#### Scenario: [#1096] The sign-in from before the update keeps working
**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Happy Path
- **Given** a `config.json` with one `session_token_hash` and `session_issued_at` 2 hours ago, and a browser holding the matching cookie (no id prefix)
- **When** the updated gateway starts and the browser makes a request
- **Then** the request answers 200, the list shows one sign-in marked current with the browser's label, and `config.json` has one `sessions` entry and no `session_token_hash`

#### Scenario Outline: [#1096] A sign-in older than its lifetime is refused
**Traces to**: User Story 7, Acceptance Scenario 6
**Category**: Edge Case
- **Given** a sign-in issued `<age>` ago
- **When** a request is made with its cookie
- **Then** an authenticated route answers `<status>`

**Examples**: Dataset D-6.

#### Scenario: [#1096] Ending a sign-in closes its live connection
**Traces to**: User Story 7, Acceptance Scenario 7
**Category**: Happy Path
- **Given** browser B with an open chat WebSocket authenticated by its cookie
- **When** browser A ends B's sign-in
- **Then** B's WebSocket is closed within 5 s, and B's reconnect attempt is refused

#### Scenario Outline: [#1096] Every way a sign-in ends closes its live connections
**Traces to**: User Story 7, Acceptance Scenario 7
**Category**: Edge Case
- **Given** browser B with an open chat WebSocket and an open live-browser WebSocket
- **When** B's sign-in ends by `<cause>`
- **Then** both sockets close within 5 s

**Examples**:

| cause |
|---|
| End from browser A |
| Sign out other browsers from A |
| Sign out on B |
| password change on A |
| eviction by an 11th sign-in |
| expiry at 24 h |

#### Scenario: [#1096] The planted-cookie protection still works with several sign-ins
**Traces to**: User Story 7, Acceptance Scenario 8
**Category**: Edge Case
- **Given** browsers A and B signed in, and a request from A carrying A's cookie plus a planted second `omnipus-session` cookie
- **When** the request reaches the gateway
- **Then** the clear set is sent, A's genuine cookie is re-sent with A's original expiry, a state-changing request answers `planted_cookie_cleared`, and B is unaffected

#### Scenario: [#1096] One password-strength check per request
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Edge Case
- **Given** 10 live sign-ins and a request carrying the 10th's cookie
- **When** the request is authenticated
- **Then** exactly 1 bcrypt comparison ran (counted through the compare hook `config.SetCompareHashAndPasswordForTest`)

#### Scenario: [#1096] Simultaneous sign-ins never exceed the cap
**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Edge Case
- **Given** 10 live sign-ins
- **When** 5 sign-ins arrive at the same time
- **Then** the set holds exactly 10, the 5 newest are among them, and exactly 5 older ones were ended

#### Scenario: [#1096] A platform-mode sign-in joins the set
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Alternate Path
- **Given** a platform-mode install with browser A signed in
- **When** browser B completes the omnipus.ai sign-in
- **Then** both A and B stay signed in, and the desktop claim of B's sign-in adds no third entry

#### Scenario: [#1096] App tokens are not touched
**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Edge Case
- **Given** a script holding a bearer token from a REST sign-in, and browsers A and B
- **When** A chooses Sign out other browsers
- **Then** the script's bearer request still answers 200

#### Scenario: [#1096] The configuration read never shows sign-ins
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Error Path
- **Given** two live sign-ins
- **When** `GET /config` is called
- **Then** the response contains no `sessions` key under any user, and no bcrypt hash string

#### Scenario: [#1096] Sign-in events are audited
**Traces to**: User Story 7, Acceptance Scenarios 2, 3
**Category**: Alternate Path
- **Given** an empty audit log
- **When** a person signs in, ends another browser, signs out other browsers, and an 11th sign-in evicts one
- **Then** the audit log holds one entry for each, naming the event, the account and the session id, and none holds a cookie value or hash

#### Scenario: [#1096] A gateway restart keeps every sign-in
**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Edge Case
- **Given** browsers A and B signed in, B last used 1 minute ago
- **When** the gateway restarts
- **Then** A and B both answer 200, and the list shows B's last use as its sign-in time until B makes a request

### Feature: [#1096] Signed-in browsers list (User Story 8)

#### Scenario: [#1096] The list shows every signed-in browser
**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Happy Path
- **Given** sign-ins from "Chrome on macOS" (this browser), "Safari on iPadOS" (used 2 hours ago) and "Firefox on Linux" (used 5 hours ago)
- **When** Sign-in opens in Chrome
- **Then** three rows show in that order; the first carries "This browser" and no End button; the others carry End
- **And** the line "Up to 10 browsers stay signed in at once. Signing in on an 11th ends the one used longest ago." shows

#### Scenario: [#1096] Ending another browser
**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Happy Path
- **Given** the list from the previous scenario
- **When** the person presses "End Safari on iPadOS" and confirms "End session"
- **Then** that row is gone, and Safari's next request answers 401

#### Scenario: [#1096] Signing out every other browser
**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Happy Path
- **Given** three sign-ins with Chrome current
- **When** the person chooses "Sign out other browsers…" and confirms
- **Then** the confirm said "You stay signed in here", the answer was `{"ended": 2}`, only Chrome's row remains, and Chrome stays signed in

#### Scenario: [#1096] Only this browser is signed in
**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Alternate Path
- **Given** one sign-in
- **When** Sign-in opens
- **Then** this browser's row shows and below it the Empty state "No other browsers are signed in"

#### Scenario: [#1096] Ending a browser that is already gone
**Traces to**: User Story 8, Acceptance Scenario 5
**Category**: Error Path
- **Given** the list shows Safari, which was meanwhile ended from another browser
- **When** the person ends Safari
- **Then** the server answered 404, the list refreshes without Safari, and the page says "That browser was already signed out."

#### Scenario: [#1096] The list cannot be loaded
**Traces to**: User Story 8, Acceptance Scenario 6
**Category**: Error Path
- **Given** `GET /auth/browser-sessions` answers 500
- **When** Sign-in opens
- **Then** an error Callout says the signed-in browsers could not be loaded, with the cause and Retry

#### Scenario Outline: [#1096] Only a signed-in person may use the browser list
**Traces to**: User Story 8, Acceptance Scenario 7
**Category**: Error Path
- **Given** a caller authenticated by `<credential>`
- **When** it calls `GET /auth/browser-sessions`
- **Then** the answer is `<status>`

**Examples**:

| credential | status |
|---|---|
| browser sign-in of the account | 200 |
| bearer token from `POST /auth/login` | 200 (no row marked current) |
| CLI token | 403 |
| dev bypass on | 403 |
| legacy `OMNIPUS_BEARER_TOKEN` with no account | 403 |
| none | 401 |

#### Scenario Outline: [#1096] Ending one sign-in by id
**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Error Path
- **Given** a signed-in browser with live sign-ins as in `<state>`
- **When** it sends `DELETE /auth/browser-sessions/<id>`
- **Then** the answer is `<status>` and `<effect>`

**Examples**: Dataset D-7.

#### Scenario: [#1096] Sign out other browsers needs a browser sign-in
**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Error Path
- **Given** a caller authenticated only by a bearer token from `POST /auth/login`
- **When** it sends `POST /auth/browser-sessions/sign-out-others`
- **Then** the answer is 409 and no sign-in ended

#### Scenario: [#1096] Ending a sign-in without the CSRF header is refused
**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Edge Case
- **Given** a signed-in browser
- **When** it sends `DELETE /auth/browser-sessions/{id}` without `X-CSRF-Token`
- **Then** the answer is 403 and the sign-in is still live

#### Scenario: [#1096] End buttons are named for their browser
**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Edge Case
- **Given** the list with "Safari on iPadOS"
- **When** a screen reader reads the row's button
- **Then** its accessible name is "End Safari on iPadOS"

### Feature: States and accessibility (User Story 9)

#### Scenario Outline: Every section has a loading state
**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path
- **Given** the reads of `<section>` have not answered yet
- **When** `<section>` opens
- **Then** it shows "Loading `<section>`…" with a spinner and skeleton cards

**Examples**: the 12 section names in US-1 AS1.

#### Scenario: A section that cannot load
**Traces to**: User Story 9, Acceptance Scenario 2
**Category**: Error Path
- **Given** `GET /settings/context` answers 500 with "context settings unreadable"
- **When** Limits opens
- **Then** an error Callout says the Limits settings could not be loaded, quotes "context settings unreadable", names one thing to do, offers Retry, and no value in Limits can be saved

#### Scenario: The gateway does not answer
**Traces to**: User Story 9, Acceptance Scenario 3
**Category**: Error Path
- **Given** `GET /about` fails with connection refused
- **When** Settings opens
- **Then** one error Callout between the page title and the sections says the gateway information could not be loaded, that values may be out of date, with Retry; on a phone it stays above the open section

#### Scenario: The skip link is the first Tab stop
**Traces to**: User Story 9, Acceptance Scenario 4
**Category**: Happy Path
- **Given** Settings just opened
- **When** the person presses Tab
- **Then** "Skip to settings content" has focus; Enter moves focus to the section content without changing the address

#### Scenario: Stacked dialogs return focus to their opener
**Traces to**: User Story 9, Acceptance Scenario 4
**Category**: Edge Case
- **Given** the Configure provider panel with the "Discard key?" confirm open over it
- **When** the person presses Escape
- **Then** only the confirm closes and focus returns to the control inside the panel that opened it

#### Scenario Outline: Touch targets and width on a phone
**Traces to**: User Story 9, Acceptance Scenario 5
**Category**: Edge Case
- **Given** a 390 px wide window
- **When** `<section>` opens
- **Then** every visible control measures at least 44 × 44 px and the page has no horizontal scroll

**Examples**: the 12 section names in US-1 AS1.

#### Scenario: Saving is announced
**Traces to**: User Story 9, Acceptance Scenario 6
**Category**: Happy Path
- **Given** a screen reader and 1 unsaved change
- **When** the person saves
- **Then** the live region announces "Changes saved." (and "Some need a gateway restart." when one does)

#### Scenario: Grouped lists start collapsed
**Traces to**: User Story 9, Acceptance Scenario 7
**Category**: Happy Path
- **Given** any section with a grouped list (Recap limits, Model overrides, Remote access, Always asks list, provider picker groups)
- **When** it opens
- **Then** each group's header has `aria-expanded="false"`

#### Scenario: Reduced motion stops animations
**Traces to**: User Story 9, Acceptance Scenario 5
**Category**: Edge Case
- **Given** the system asks for reduced motion
- **When** a section loads
- **Then** the skeleton does not pulse and the spinner does not rotate

#### Scenario: On-screen text carries no tags or support line
**Traces to**: User Story 9, Acceptance Scenario 2
**Category**: Edge Case
- **Given** every section in each of its states
- **When** its visible text is collected
- **Then** it contains no `#` followed by digits, no "R30", "S-Q" or similar tag, and no "contact support"

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit (Go) | session set (add, cap, eviction, expiry, migration, id lookup), retention validation and sweep conditions, `RestartGatedKeys`, `blockedPaths`, `deleteCredential` gate, label table | rules in isolation, with a controlled clock and the bcrypt compare counter |
| Integration (Go, `httptest` against the gateway's handlers) | the three `/auth/browser-sessions` routes, login/logout/change-password, `/settings/retention`, `/settings/memory`, `/config` refusal, `/config/pending-restart`, WebSocket close on end, planted-cookie guard | the server contract end to end inside one process |
| Unit (Vitest + Testing Library) | Save bar counting, validation, leave guard, password flow, restart banner, legacy link mapping, search, Sign-in section | screen rules with a mocked API (the API layer is the boundary; the components stay real) |
| E2E (Playwright, targeted spec files only) | the hands-on rows marked "stable" below | the whole journey in a real browser against a real gateway |

### Hands-on UAT rows (written at spec time)

| Row | Acceptance criterion | Steps | Expected result | Affected area | Validator critical path | Automation candidate |
|---|---|---|---|---|---|---|
| G-01 | US-1 AS1, AS2 | Open Settings; open Sandbox & network; reload | 12 sections in 3 groups; Sandbox & network still open after reload | shell, routing | open `/settings?tab=sandbox` directly | stable |
| G-02 | US-1 AS3–AS7 | Open each D-3 link | each lands per D-3 | routing | spot-check 4 D-3 rows | stable |
| G-03 | US-1 AS8–AS10 | Search "web search", "backup", "pair", "zzqx" | results per D-13 | search | repeat 2 queries | stable |
| G-04 | US-1 AS9, US-9 AS5 | 390 px: open each section | list first; back button; no sideways scroll; controls ≥ 44 px | layout | 3 sections | stable |
| G-05 | US-2 AS1–AS5 | Gateway: change Port, Discard; change, type back; type 80 | bar counts follow the values; 80 blocks Save with the range text | Save bar | Port 80 only | stable |
| G-06 | US-2 AS6 | Change a value; try every exit in the "Every way out asks first" table | each asks; Stay keeps the change | leave guard | section switch + Back | stable |
| G-07 | US-2 AS7 | Limits: change context window to an invalid server value and Tries per goal; save | partial-failure display per scenario | Save, Limits | same | reason not yet: needs a server-refused value; stable once D-10 row 9 is fixed |
| G-08 | US-3 AS1–AS3 | Sandbox & network: change two password rows; save with wrong, then right password; repeat with Cancel | one prompt listing both; wrong keeps it open; Cancel keeps drafts | password flow | right-password path | stable |
| G-09 | US-3 AS5, US-6 AS3 | Providers: change Active transcriber; save | Password badge; one prompt | Providers voice | same | stable |
| G-10 | US-4 AS1–AS3, AS7 | Gateway: change Port; save; Later; open About; Show changes; Restart now | window; banner everywhere; running/saved values; banner gone after restart | restart | Later + banner on About | stable |
| G-11 | US-4 AS4, AS5 | Save Filesystem model; then the network proxy | both appear in Show changes | B2 | Filesystem model | stable |
| G-12 | US-4 AS6 | Protection: change Defense level; save | no badge, no window, no banner | restart | same | stable |
| G-13 | US-5 AS1–AS4 | Data & retention: check defaults; set 30/365; save; reload; type 366; Keep forever on | defaults 90/180/off; values persist; 366 blocked; day field off | retention UI | 366 + Keep forever | stable |
| G-14 | US-5 AS3 (server) | Keep forever on; plant a 200-day-old reflection; trigger the sweep (`POST /security/retention/sweep` covers sessions; the retro sweep runs on its tick) | reflection kept | B6 | same | reason not yet: needs a sweep trigger for reflections in the test harness |
| G-15 | US-5 AS5, AS6 | Open Memory & recaps; then set `storage.retention.disabled: true` and reload Data & retention | link row only; legacy notice | retention UI | notice | stable |
| G-16 | US-6 AS1, AS2 | Protection: God Mode button with an unsaved Auto-approve change | God Mode acts alone; draft stays | Protection | same | stable |
| G-17 | US-6 AS4–AS8 | Profile Chat display; Protection moved notice; pairing flag on; delete a key; rotate token | per scenarios | moved items | delete key | stable |
| G-18 | US-6 AS10 | Walk all 88 map rows | each in its mapped place | whole page | 15 random rows | reason not yet: one-off walk; D-9 rows become stable checks |
| G-19 | US-7 AS1–AS3 [#1096] | Sign in on browsers A and B; sign out on A | A and B both work; after Sign out only B works | sign-in | A + B | stable |
| G-20 | US-7 AS2 [#1096] | Sign in 11 times (separate cookie jars), using jar 3 last-but-one | jar used longest ago is signed out; 10 remain | eviction | 11 jars by script | stable (scripted jars) |
| G-21 | US-7 AS5 [#1096] | Sign in on the old release; update the binary; continue | still signed in; list shows one row | migration | same | reason not yet: needs two builds; keep manual |
| G-22 | US-7 AS7 [#1096] | B in a chat; end B from A | B's socket closes ≤ 5 s; next action → sign-in screen; unsent text handling per #1146 | live connections | same | stable |
| G-23 | US-8 AS1–AS5 [#1096] | Three browsers; open Sign-in; End one; Sign out others; End an already-ended row (from a stale tab) | per scenarios | Sign-in section | End + Sign out others | stable |
| G-24 | US-7 AS4 [#1096] | Two browsers; change password on A | both signed out | password change | same | stable |
| G-25 | US-9 AS1–AS4, AS6 | Throttle network; stop gateway; keyboard-only and screen reader pass | loading, error, offline states; skip link; announcements | states, a11y | offline + skip link | partly: states stable; screen-reader pass manual |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | TestBrowserSessions_SecondSignInKeepsFirst | Integration (Go) | [#1096] Signing in on a second browser keeps the first | contract-first: the set exists |
| 2 | TestBrowserSessions_EvictsLeastRecentlyUsed | Unit (Go) | [#1096] The 11th sign-in ends the one used longest ago | D-5 |
| 3 | TestBrowserSessions_ExpiryEnforcedServerSide | Unit (Go) | [#1096] A sign-in older than its lifetime is refused | D-6 |
| 4 | TestBrowserSessions_OneBcryptPerRequest | Unit (Go) | [#1096] One password-strength check per request | compare counter |
| 5 | TestBrowserSessions_LegacyMigrationKeepsSignIn | Integration (Go) | [#1096] The sign-in from before the update keeps working | Q4 A |
| 6 | TestBrowserSessions_ListEndSignOutOthers | Integration (Go) | list / End / sign-out-others scenarios, D-7 | three routes |
| 7 | TestBrowserSessions_PersonOnly | Integration (Go) | [#1096] Only a signed-in person may use the browser list | 403 matrix |
| 8 | TestBrowserSessions_EndClosesWebSockets | Integration (Go) | [#1096] Every way a sign-in ends closes its live connections | both sockets |
| 9 | TestPlantedCookieGuard_WithSessionSet | Integration (Go) | [#1096] The planted-cookie protection still works with several sign-ins | ADR-094 FR-015 |
| 10 | TestLogout_EndsOnlyThisBrowser / TestChangePassword_EndsAllBrowsers | Integration (Go) | Sign out / password change scenarios | Q2 A |
| 11 | TestGetConfig_NeverServesSessions | Integration (Go) | [#1096] The configuration read never shows sign-ins | T8 |
| 12 | TestBrowserSessions_Audit | Integration (Go) | [#1096] Sign-in events are audited | T10 |
| 13 | TestPendingRestart_IncludesFilesystemModelAndProxy | Integration (Go) | The server lists every restart-gated setting that was saved | D-4 |
| 14 | TestRetentionSettings_Ranges | Integration (Go) | The server enforces the ranges | D-1, D-2 |
| 15 | TestRetentionSweep_MemoryForeverKeepsRetros / _ForeverOffResumes / _LegacyDisabledKeepsAll | Unit (Go) | Keep forever scenarios, legacy switch | B6 |
| 16 | TestUpdateConfig_RefusesStorageRetention | Integration (Go) | The general configuration write refuses retention | D-12 |
| 17 | TestMemorySettings_RefusesRetentionFields | Integration (Go) | The memory settings refuse retention fields | B3 |
| 18 | TestSecurityRetentionRoute_Removed | Integration (Go) | The old retention route is gone | B3 |
| 19 | TestDeleteCredential_NoReauthNeeded / TestSetCredential_StillNeedsReauth | Integration (Go) | B7 scenarios | replaces ADR-022's `TestDeleteCredential_RequiresReAuth` (named in ADR-022) |
| 20 | TestSearchRetros_FollowsRetentionWindow [pending FQ-1] | Unit (Go) | An old reflection kept by the setting can be recalled | FQ-1 A |
| 21 | settingsRoute.legacyTabs.test | Unit (Vitest) | Old and new section links land in the right place | D-3 |
| 22 | saveBar.counting.test | Unit (Vitest) | Save bar scenarios | D-10 |
| 23 | saveBar.leaveGuard.test | Unit (Vitest) | Every way out asks first | exits table |
| 24 | saveBar.partialFailure.test | Unit (Vitest) | One of two writes fails | — |
| 25 | passwordOncePerSave.test | Unit (Vitest) | Password scenarios | D-11 |
| 26 | sectionPasswordEndpoints.guard.test | Unit (Vitest) | Each section sends its password-marked values to at most one protected endpoint | D-8 |
| 27 | restartBanner.test | Unit (Vitest) | Restart scenarios | — |
| 28 | dataRetention.section.test | Unit (Vitest) | Retention UI scenarios | D-1, D-2 |
| 29 | signInSection.test | Unit (Vitest) | [#1096] list scenarios | — |
| 30 | settingsSearch.test | Unit (Vitest) | Search scenarios | D-13 |
| 31 | settingsMap.test | Unit (Vitest) | Every inventory control is where the map says | D-9 |
| 32 | settings-redesign.spec.ts | E2E (Playwright) | rows marked stable | targeted file only |

### Test Datasets

#### Dataset D-1: Keep chat history for (screen field and `session_days`)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | (empty) | Empty | Screen: "This cannot be empty." then the range; Save off | BDD Scenario: An invalid value blocks saving and names the range | required field (design-notes §6) |
| 2 | 0 | Min − 1 | Screen: "Enter a whole number from 1 to 365."; server 400 `session_days must be a whole number from 1 to 365` | BDD Scenario: The server enforces the ranges | 0 meant "default 90" before; no longer accepted on write |
| 3 | 1 | Min | stored 1 | BDD Scenario: The server enforces the ranges | |
| 4 | 90 | Default | stored 90 | BDD Scenario: Fresh-install defaults | S-Q13 A |
| 5 | 365 | Max | stored 365 | BDD Scenario: The server enforces the ranges | |
| 6 | 366 | Max + 1 | screen range text; server 400 | BDD Scenario: The server enforces the ranges | |
| 7 | -1 | Negative | screen range text; server 400 | BDD Scenario: The server enforces the ranges | |
| 8 | 30.5 | Float | screen "Enter a whole number…"; server 400 | BDD Scenario: The server enforces the ranges | |
| 9 | "30" (JSON string) | Wrong type | server 400 | BDD Scenario: The server enforces the ranges | |
| 10 | 500 stored by hand | Out of range at rest | shown as 500 with range error; Save off | BDD Scenario: A hand-edited value outside the range is shown and blocks Save | FR-046 |

#### Dataset D-2: Keep memory for and Keep forever

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | days 0 | Min − 1 | server 400 `memory_retros_days must be a whole number from 1 to 730` | BDD Scenario: The server enforces the ranges | |
| 2 | days 1 | Min | stored 1 | BDD Scenario: The server enforces the ranges | |
| 3 | days 180 | Default | stored 180 | BDD Scenario: Fresh-install defaults | S-Q13 A |
| 4 | days 730 | Max | stored 730 | BDD Scenario: The server enforces the ranges | new bound |
| 5 | days 731 | Max + 1 | server 400 | BDD Scenario: The server enforces the ranges | |
| 6 | forever true, no days | Alternate | stored forever true, days unchanged | BDD Scenario: With Keep forever on the day field is off and not sent | |
| 7 | forever true, days 999 | Invalid while forever | server 400 (days are validated whenever sent) | BDD Scenario: The server enforces the ranges | the screen never sends this (FR-040) |
| 8 | forever "true" (string) | Wrong type | server 400 | BDD Scenario: The server enforces the ranges | |
| 9 | `keep_everything_forever: true` | Read-only field | server 400 | BDD Scenario: The server enforces the ranges | config-file only |
| 10 | `{}` | Empty body | server 400 `at least one field required` | BDD Scenario: The server enforces the ranges | |
| 11 | `{"extra": 1}` | Unknown field | server 400 | BDD Scenario: The server enforces the ranges | `additionalProperties: false` |

#### Dataset D-3: Section links (`/settings?tab=…`)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | (none) | Default | Providers | BDD Scenario: Person opens Settings and sees 12 sections in three groups | |
| 2 | providers, memory, system-agents, third-party-agents, limits, protection, sandbox, keys, signin, gateway, data, about | New ids | that section | BDD Scenario: Old and new section links land in the right place | ids per wireframe `app.js::SECTIONS` |
| 3 | models | Legacy | Limits | BDD Scenario: Old and new section links land in the right place | |
| 4 | performance | Legacy | Limits | same | |
| 5 | integrations | Legacy | Providers (Voice input) | same | web search is in Skills & Tools |
| 6 | security | Legacy | Protection | same | |
| 7 | memory | Same id | Memory & recaps | same | |
| 8 | data | Same id | Data & retention | same | |
| 9 | devices | Legacy | Sign-in | same | pairing hidden, #1916 |
| 10 | chat | Legacy | Profile → Chat display | same | also the chat leave-search link `/#/settings?tab=chat` |
| 11 | backup, backups, restore | Removed | Data & retention + "Backup and restore were removed." | same | #1919 |
| 12 | gateway&focus=god-mode | Legacy focus | Protection, God Mode button focused | BDD Scenario: The God Mode pill lands on the God Mode button | any tab with `focus=god-mode` |
| 13 | models&provider=openrouter&model=acme/m1 | Pre-fill | Limits, Model overrides expanded, filled row | BDD Scenario: The "No context length" link pre-fills a model override | |
| 14 | provider=openrouter&model=acme/m1 (no tab) | Pre-fill only | same as row 13 | same | today's rule (`settings.tsx::SettingsRoute`) kept |
| 15 | notasection | Unknown | Providers | BDD Scenario: Unknown section name falls back to Providers | |
| 16 | PROTECTION | Case | Providers | BDD Scenario: Unknown section name falls back to Providers | ids are lower-case exact |

#### Dataset D-4: Restart-gated keys in `GET /config/pending-restart`

| # | Input (key saved ≠ running) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `sandbox.mode` | Existing | listed | BDD Scenario: The server lists every restart-gated setting that was saved | already gated |
| 2 | `sandbox.filesystem_model` | New (B2) | listed with running and saved values | same | Inferred restart-gated; RED proves |
| 3 | `tools.exec.enable_proxy` | New (B2) | listed | same | Inferred; RED proves |
| 4 | `sandbox.workspace_path_guard` | Existing | listed | same | badge correction |
| 5 | `sandbox.allowed_paths` | Existing | listed | same | badge correction |
| 6 | `gateway.port` | Existing | listed | same | |
| 7 | `gateway.host` | Existing | listed | same | Bind address |
| 8 | `sandbox.prompt_injection_level` | Hot-reload | **not** listed | BDD Scenario: Defense level applies at once | |
| 9 | `sandbox.auto_approve` | Hot-reload | not listed | same | |
| 10 | `sandbox.god_mode` | Excluded by design | not listed | same | O14 |
| 11 | `gateway.port` saved back to running | Revert | not listed (empty list) | BDD Scenario: Saving the running value again clears the banner | |

#### Dataset D-5: [#1096] Cap and eviction

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 0 live, new sign-in | Empty | 1 live, none ended | BDD Scenario: [#1096] The 11th sign-in ends the one used longest ago | |
| 2 | 9 live, new sign-in | Max − 1 | 10 live, none ended | same | |
| 3 | 10 live, last uses t1 < t2 < … < t10, new sign-in | Max + 1 | the t1 sign-in ended; 10 live | same | S-Q14 A |
| 4 | 10 live, t3 oldest though signed in latest | Order by use, not by age | the t3 sign-in ended | same | "used longest ago", not "oldest" |
| 5 | 10 live, two share the oldest last use | Tie | of those two, the one signed in earlier ended | same | tie rule FR-078 |
| 6 | 10 live, 2 of them expired | Expired present | the 2 expired pruned, none live ended; 9 live | same | expiry before eviction |
| 7 | 10 live, current browser is the least used | Self | that sign-in is ended (a new sign-in on another browser) | same | no exemption |

#### Dataset D-6: [#1096] Sign-in lifetime (24 h)

| # | Input (age at request) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 0 s | Min | 200 | BDD Scenario: [#1096] A sign-in older than its lifetime is refused | |
| 2 | 23 h 59 m 59 s | Max − 1 s | 200 | same | |
| 3 | 24 h 0 m 0 s | Max | 401 | same | "exactly 24 hours old is refused" |
| 4 | 3 days | Far past | 401 | same | wireframe sample row impossible (FQ-3) |
| 5 | migrated legacy, no issue time | Missing field | treated as issued at migration; 200 until 24 h later | BDD Scenario: [#1096] The sign-in from before the update keeps working | Q4 A |
| 6 | issued 1 h in the future (clock went back) | Clock skew | 200; expires at issued + 24 h | same | parked T-risk, behaviour stated |

#### Dataset D-7: [#1096] `DELETE /auth/browser-sessions/{id}`

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | id of another live sign-in | Happy | 204; that sign-in refused next | BDD Scenario: [#1096] Ending one sign-in by id | |
| 2 | id of the caller's own sign-in | Self | 409; nothing ended | same | use Sign out |
| 3 | well-formed unknown id | Unknown | 404 | same | |
| 4 | id ended a moment ago | Already ended | 404 | same | same body as row 3 |
| 5 | id of an expired sign-in | Expired | 404 | same | same body as row 3 |
| 6 | `abc` (fails the pattern) | Malformed | 404 | same | same body as row 3 (T7) |
| 7 | 16 chars with `/` encoded | Special chars | 404 | same | no path traversal |
| 8 | id of another live sign-in, caller is a bearer token | Bearer caller | 204 | same | no current sign-in to protect |

#### Dataset D-8: Sections and their one password-protected endpoint (B4 guard)

| # | Input (section, all password rows changed) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Providers (Active transcriber) | One endpoint | 1 × `PUT /integrations/providers/{id}` | BDD Scenario: Each section sends its password-marked values to at most one protected endpoint | |
| 2 | Limits (six Agents-and-goals rows) | One endpoint | 1 × `PUT /performance` | same | |
| 3 | Protection (Auto-approve) | One endpoint | 1 × `PUT /security/sandbox-config` | same | |
| 4 | Sandbox & network (mode, filesystem model, workspace limit, allowed folders, internal addresses) | One endpoint | 1 × `PUT /security/sandbox-config` | same | |
| 5 | Memory & recaps, System agents, Third-party agents, Keys & secrets, Sign-in, Gateway, Data & retention, About | No password values | 0 | same | Keys' buttons act alone |

#### Dataset D-9: Inventory map rows whose place changes behaviour

| # | Input (design-notes §2 row) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 14–19 Models caps and window | Moved | Limits → Context and tool results | BDD Scenario: Every inventory control is where the map says | |
| 2 | 20 Model overrides | Moved | Limits → Model overrides (collapsed, S-Q12 A) | same | design notes row text says "open by default"; S-Q12 A wins |
| 3 | 21, 26, 74 old Save buttons | Merged | Save bar | same | |
| 4 | 27–31 Web search | Moved out | absent; search points to Skills & Tools | same | only after Skills & Tools lands |
| 5 | 32–33 Voice | Moved | Providers → Voice input | same | Password badge on row 32 |
| 6 | 38, 39 Skill trust, tool grid | Moved out | absent; Protection notice | same | only after Skills & Tools lands |
| 7 | 51 Delete credential | Kept | confirm only | same | B7 |
| 8 | 59 God Mode | Moved | Protection, button | same | |
| 9 | 61, 72, 73 retention | Kept / Removed / Moved | Data & retention, two settings; Memory link row | same | |
| 10 | 62, 63 Backup, Restore | Removed | absent; notice | same | #1919 |
| 11 | 75–77 Devices | Hidden | absent | same | #1916 |
| 12 | 79 Tool loading switch | Removed later | stays in Limits → Agents and goals until R31 ships | same | S-Q3 |
| 13 | 85 Per-control hidden Save + debounce | Removed | absent | same | |
| 14 | 86 Verbose chat | Moved out | Profile → Chat display | same | |
| 15 | 55 Rotate gateway token | Kept | confirm first | same | |

#### Dataset D-10: Field validation messages

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Gateway → Port 80 | Min − | "Enter a whole number from 1,024 to 65,535." | BDD Scenario: An invalid value blocks saving and names the range | |
| 2 | Port 65536 | Max + 1 | same message | same | |
| 3 | Port 1024 / 65535 | Min / Max | accepted | same | |
| 4 | Limits → Tries per goal 0 | Min − 1 | "Enter a whole number of at least 1." | same | no upper bound |
| 5 | Idle reminder quiet window 29 / 3601 | Out of range | "Enter a whole number from 30 to 3,600." | same | |
| 6 | Check-in interval 4 / 1441 | Out of range | "Enter a whole number from 5 to 1,440." | same | validated even while check-ins are off |
| 7 | Max tool calls per turn 0 / 1001 | Out of range | "Enter a whole number from 1 to 1,000." | same | |
| 8 | Max parallel agents (empty) | Empty allowed | accepted (no fixed limit) | same | empty means automatic |
| 9 | Default context window 512 | Server-refused | server message under the field | BDD Scenario: One of two writes fails | server owns the bound |
| 10 | Keep chat history for (empty) | Required | "This cannot be empty." | same | |

#### Dataset D-11: Password prompt outcomes

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | right password | Happy | one reauth, one protected write, stored | BDD Scenario: Two password-marked changes need one password | |
| 2 | wrong password | Error | 401 → "The password is incorrect."; prompt open | BDD Scenario: Wrong password | |
| 3 | empty password, press Confirm | Empty | "Enter your password to continue."; no request | BDD Scenario: Wrong password | wireframe `reauth-ok` |
| 4 | 11th try within a minute | Rate limit | 429 → wait message | BDD Scenario: Too many password tries | limit 10/min |
| 5 | Cancel | Alternate | nothing sent; drafts kept | BDD Scenario: Cancelling the password prompt keeps every change and any typed key | #1153 |
| 6 | platform mode | Alternate | confirm only, no reauth | BDD Scenario: Platform mode asks for a confirmation instead | |
| 7 | 73-character password | Max + 1 | 400 from `ReAuthRequest` (maxLength 72) shown as the server's message | BDD Scenario: Wrong password | bcrypt limit |

#### Dataset D-12: `PUT /config` refusals for retention

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `{"storage":{"retention":{"session_days":30}}}` | Nested | 400 naming `PUT /api/v1/settings/retention`; file unchanged | BDD Scenario: The general configuration write refuses retention | |
| 2 | `{"storage":{"retention":{}}}` | Empty nested | 400 | same | |
| 3 | `{"storage":{"retention":{"disabled":true}}}` | Legacy switch | 400 | same | config-file only |
| 4 | `{"storage":{"other":1}}` | Sibling | not refused by this rule | same | only `storage.retention` is blocked |
| 5 | `{"gateway":{"port":8081},"storage":{"retention":{"session_days":30}}}` | Mixed | 400; port **not** written either | same | refusal before any mutation (blockedPaths behaviour) |

#### Dataset D-13: Search queries

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | "port" | Label | Gateway → Port | BDD Scenario: Search results | |
| 2 | "PORT" | Case | same as row 1 | same | case-insensitive |
| 3 | "web search" | Moved out | moved to Skills & Tools, with link | BDD Scenario: Searching for a moved setting says where it went | |
| 4 | "backup" / "restore" | Removed | "Backup and restore were removed." | same | |
| 5 | "pair" | Hidden | device pairing is not available in Settings at the moment | BDD Scenario: Device pairing stays hidden when switched on | |
| 6 | "retention" | Description | Data & retention → both settings | BDD Scenario: Search results | |
| 7 | "zzqx" | No match | Empty "No settings match" | BDD Scenario: Search with no match shows an empty state | |
| 8 | "   " | Whitespace | full section list, no results panel | BDD Scenario: Search results | |
| 9 | `<script>` | Special chars | treated as text; no match | BDD Scenario: Search results | |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Login rate limit 5 failures / 15 min | existing `rest_auth` limiter tests | No | unchanged path |
| Bearer tokens capped at 10, logout revokes the presented one | existing SEC-1 tests (`appendUserToken`, `revokeUserToken`) | No | must stay green |
| Onboarding issues a session cookie | `rest_onboarding_cookie_test.go` | Yes — update to assert a `sessions` entry instead of `session_token_hash` | the oracle changes by design |
| Platform sign-in callback and claim | `rest_platform_auth` tests | Yes — the claim re-sends the same sign-in, no new entry | |
| Planted-cookie guard | ADR-094 FR-015 tests in `planted_cookie_test.go` | Yes — `TestPlantedCookieGuard_WithSessionSet` | HIGH-risk row |
| Delete credential requires reauth | `TestDeleteCredential_RequiresReAuth` (ADR-022) | Replaced by `TestDeleteCredential_NoReauthNeeded` | B7, founder S-Q11 B; FQ-2 |
| Pending-restart excludes hot keys and God Mode | `rest_pending_restart_test.go` | No — keep; add D-4 rows 2–3 | |
| Memory settings round trip | `rest_memory_settings` tests | Yes — retention fields removed | |
| `/security/retention` GET/PUT | `rest_retention_test.go`, `api_e2e_test.go`, `routes_admin_test.go` | Yes — those cases now expect 404; sweep cases stay | |
| Max tool calls lowering flow (#904) | `PerformanceSection.maxToolIterations.test.tsx`, Go #904 tests | No — must stay green under the Save bar | |
| Chat leave-search link `/#/settings?tab=chat` | `-workspaces.$workspaceId.chat.leaveSearch.test.tsx` | Yes — it now lands on Profile → Chat display | |

---

## Functional Requirements

Numbers are grouped by area; gaps are left on purpose so an area can grow without renumbering. **[#1096]** marks the separable signed-in-browsers block. "MUST" is the requirement; the wording of on-screen text follows the wireframe (`app.js`) and `omnipus-voice`.

### Structure, links and search (User Story 1)

- **FR-001**: The Settings screen MUST show 12 sections in three groups, in this order: AI & agents (Providers, Memory & recaps, System agents, Third-party agents, Limits); Security (Protection, Sandbox & network, Keys & secrets, Sign-in); System (Gateway, Data & retention, About). With no section named in the address it MUST open Providers. The old tabs Models, Performance, Integrations, Security, Context, Credential vault, Devices, Chat and Backup MUST NOT appear.
- **FR-002**: The open section MUST be named in the address as `tab=<id>` with the ids of D-3 row 2; a reload MUST reopen it, and each section switch MUST add one browser history entry so Back and Forward move between sections (#1154).
- **FR-003**: Every old `tab=` value MUST open the section listed in Dataset D-3 (rows 3–11); an unknown or differently cased value MUST open Providers without an error; `backup`, `backups` and `restore` MUST show the notice "Backup and restore were removed." in Data & retention; `chat` MUST open Profile at Chat display.
- **FR-004**: `provider=<p>&model=<m>`, with or without `tab=models` or `tab=limits`, MUST open Limits with Model overrides expanded and one new unsaved row filled with that provider and model (today's pre-fill rule in `src/routes/_app/settings.tsx::SettingsRoute`, kept).
- **FR-005**: `focus=god-mode` with any `tab` MUST open Protection and move keyboard focus to the God Mode button, then remove `focus` from the address without a new history entry.
- **FR-006**: Search settings MUST match the typed text case-insensitively as a plain substring over each setting's label and description (no new dependency), list results with their section, announce the count in a polite live region, show the kit Empty state "No settings match" with the typed text when nothing matches, and treat typed markup as text.
- **FR-007**: Search MUST also answer for moved, removed and hidden items: web search, tool permissions and skill trust → "moved to Skills & Tools" with a link (only once Skills & Tools has landed, FR-060); backup and restore → "Backup and restore were removed."; device pairing → "Device pairing is not available in Settings at the moment."
- **FR-008**: Below 768 px wide, the list MUST show first; choosing a section MUST show only that section with a "Settings" back button that returns to the list.
- **FR-009**: Every in-app link into Settings MUST land per the table in "Links from other screens keep working": Omnipus chat gear → System agents; third-party agent profile → Third-party agents; Skills permissions link → Protection (until Skills & Tools lands); default model card "No context length" → Limits per FR-004; `/app/policies` → Settings.

### One save rule (User Story 2)

- **FR-010**: A changed value MUST NOT be stored until Save changes is pressed (S-Q2 A) — including recap model slots, Gateway fields, rate limits and the Limits fields that today save after a delay. Buttons (Test, Check, Rotate, Restart, God Mode, Delete, End, Sign out other browsers, Add key, Connect a provider) MUST act at once and MUST NOT enter the Save batch.
- **FR-011**: The Save bar MUST appear within 100 ms of a change and show "N unsaved change(s)", "N need(s) a gateway restart" and "N need(s) your password" for the changed values, with Discard and Save changes. A value typed back to its saved value MUST stop counting; text in an "Add …" field MUST count only after Add is pressed. With zero changes the bar MUST be hidden.
- **FR-012**: Discard MUST return every changed value of the section to its last saved value and store nothing.
- **FR-013**: The screen MUST validate each field with the range and message of Dataset D-10 (and D-1, D-2 for retention); while any value is invalid, Save changes MUST be unavailable, the bar MUST say "Fix N value(s) before saving", and pressing Save changes MUST move focus to the first invalid field and send nothing.
- **FR-014**: With unsaved changes, every way out of the section (section list, search result, Profile link, app sidebar, browser Back, typed address) MUST ask "Leave without saving N change(s)?" with Stay and Leave; closing the tab MUST trigger the browser's own leave prompt. Stay MUST keep the section, its changes and the address; Back plus Stay MUST not add a history entry.
- **FR-015**: Save MUST send each changed value to its own endpoint as a partial update carrying only the changed fields. When some writes fail, the stored ones MUST show as saved, each failed one MUST keep its typed value with the server's message under its field, and the bar MUST say "N change(s) could not be saved". A connection failure MUST keep every change and say the gateway is not answering and to check that it is running.
- **FR-016**: Pre-save confirms MUST run before the password prompt and before any write: the affected-agents list when Max tool calls per turn is lowered (#904 flow, kept), and "Listen on all interfaces?" when Bind address becomes All interfaces. Cancel MUST store nothing and keep the changes.
- **FR-017**: While the Save bar shows, Save changes MUST be the only gold (primary) button on the screen; other primary buttons MUST render outlined.
- **FR-018**: After a successful Save the bar MUST say "Saved at HH:MM" (local time, 24-hour) and the live region MUST announce "Changes saved." plus "Some need a gateway restart." when one does.
- **FR-019**: Drafts MUST belong to one section; leaving it resolves them (FR-014). Two tabs editing the same section MUST each send only their own changed fields, so neither overwrites the other's untouched fields.

### Password once per Save (User Story 3, B4)

- **FR-025**: The Password badge MUST be on exactly these rows: Active transcriber (Providers); the six Agents-and-goals rows (Limits); Auto-approve (Protection); Sandbox mode, Filesystem model, Shell workspace limit, Allowed folders and Internal network addresses (Sandbox & network); the God Mode button. Each badge carries an icon and the word.
- **FR-026**: A Save containing any password-marked change MUST open exactly one password prompt that lists, by visible label, every password-marked setting in the Save and nothing else.
- **FR-027**: After the right password, the screen MUST send exactly one `POST /auth/reauth` and then all password-marked values of the section in exactly one request to the section's one protected endpoint (Dataset D-8), carrying the token in `X-Reauth-Token`. No server change (C-4).
- **FR-028**: The typed password MUST be cleared from the prompt and from page state as soon as the one `POST /auth/reauth` returns, whatever its result; no later request may carry it.
- **FR-029**: Wrong password (401) MUST keep the prompt open with "The password is incorrect." under the field and focus in it; an empty submit MUST say "Enter your password to continue." without a request; rate-limited (429) MUST say too many tries were made and to wait a minute; Cancel MUST store nothing, close only the prompt, keep every change and keep any side panel with its typed key open (#1153), returning focus to the control that opened the prompt.
- **FR-030**: A guard test MUST fail when any section's save plan maps password-marked values to more than one protected endpoint, so a future field added to a second protected endpoint in the same section cannot silently need a second token.
- **FR-031**: If the protected write answers 409 with a changed affected-agents list (max tool calls lowering), the screen MUST show the new list for confirmation and ask for the password again; nothing may be stored before that.
- **FR-032**: In platform mode (sign-in through omnipus.ai) a Save with password-marked changes MUST show a confirmation listing them, with no password field and no `POST /auth/reauth`.

### Restart (User Story 4, B2)

- **FR-033**: After a Save that stored at least one restart-gated value, the existing "Restart now?" window MUST open listing those settings by label, with Later and Restart now.
- **FR-034**: A write response with `requires_restart: true` MUST open the same window naming that setting, even when the row has no Needs restart badge (for example a failed prompt-guard reload).
- **FR-035**: While `GET /config/pending-restart` lists entries, every section MUST show the banner "N saved change(s) wait(s) for a gateway restart" with Restart now and Show changes; Show changes MUST list each entry's label, running value (`applied_value`) and saved value (`persisted_value`). After Later or Escape, focus MUST move to the banner's Restart now; after a finished restart, to the open section's heading.
- **FR-036**: `pkg/gateway/rest_pending_restart.go::RestartGatedKeys` MUST add `sandbox.filesystem_model` and `tools.exec.enable_proxy`, and the `PendingRestartEntry` and `/config/pending-restart` descriptions MUST list the full key set (C-2). God Mode and hot-reloaded keys MUST stay excluded.
- **FR-037**: The Needs restart badge MUST be on exactly the rows whose keys are in `RestartGatedKeys` after FR-036 (including Shell workspace limit and Allowed folders), and MUST NOT be on Defense level or any hot-reloaded row.
- **FR-038**: The banner MUST disappear when the list becomes empty — after a restart, or after a key is saved back to its running value.
- **FR-039**: When `GET /config/pending-restart` fails, the banner area MUST say the list of waiting changes could not be loaded, with Retry.

### Retention (User Story 5, B3 and B6)

- **FR-040**: While Keep forever is on, "Keep memory for" MUST be disabled, not validated by the screen, and not sent; the Save body MUST carry `memory_retros_forever` and omit `memory_retros_days`.
- **FR-041**: `GET /settings/retention` MUST return `RetentionSettings` with every field and defaults applied: `session_days` 90, `memory_retros_days` 180, `memory_retros_forever` false, `keep_everything_forever` mirroring `storage.retention.disabled`.
- **FR-042**: `PUT /settings/retention` MUST accept `session_days` only as an integer 1–365 and `memory_retros_days` only as an integer 1–730 (validated whenever sent, also while Keep forever is on) and `memory_retros_forever` only as a boolean; it MUST answer 400 naming the field and range for anything else, for `keep_everything_forever`, for an unknown field, and for an empty body ("at least one field required"). Messages: Machine-Verifiable Constraints.
- **FR-043**: `PUT /settings/retention` MUST be a partial update written in one atomic config save, answer 200 with the stored values, and use `withAuth` plus `RequireNotBypass` with no password (C-3).
- **FR-044**: `storage.retention` MUST be added to `pkg/gateway/blocked_paths.go::blockedPaths`, so `PUT /config` refuses any body reaching it at any depth with 400 naming `PUT /api/v1/settings/retention`, before any part of the body is applied (Dataset D-12).
- **FR-045**: `MemorySettings` MUST drop `session_days` and `memory_retros_days`; `PUT /settings/memory` carrying either MUST answer 400 and change nothing.
- **FR-046**: A stored day count outside the new range (hand-edited) MUST be returned as stored, applied as stored by the sweeps, and shown by the screen with the range error, with Save unavailable until fixed.
- **FR-047**: `GET` and `PUT /security/retention`, their handlers and the schemas `RetentionConfig`, `RetentionUpdateRequest`, `RetentionUpdateResponse` MUST be removed; `POST /security/retention/sweep` MUST keep working unchanged.
- **FR-048**: The legacy `storage.retention.disabled: true` MUST keep stopping both sweeps, MUST be readable only (config-file only), and Data & retention MUST show an info notice that chat history and memory are both kept forever because of a setting in the configuration file and that the day counts apply once it is removed; the day fields stay editable.
- **FR-049**: `storage.retention.memory_retros_forever: true` (new config key) MUST stop the memory sweep and only the memory sweep; turned off again, the next nightly sweep MUST apply `memory_retros_days`. The switch's description MUST say that older reflections are deleted at the next nightly clean-up when it is turned off.
- **FR-050** *(pending FQ-1, recommended answer A)*: Memory recall (`pkg/agent/memory_adapter.go::SearchRetros`) MUST read retrospectives back as far as the memory retention keeps them: `memory_retros_days` (up to 730), or with no age limit while Keep forever or the legacy switch is on. Dropped if FQ-1 is answered B.
- **FR-052**: Memory & recaps MUST show no retention field, only a row "How long memory is kept" that opens Data & retention.

### Moved, removed and hidden controls (User Story 6)

- **FR-051**: God Mode MUST be a "Turn on God Mode…" / "Turn off God Mode…" button in Protection after Approvals; it MUST act alone with its own password prompt and its own restart window, never enter or clear the Save batch, and Gateway MUST have no God Mode control.
- **FR-053**: Providers MUST hold a Voice input group: Active transcriber (a Save-batch value, Password badge) and one key row per voice service; a key row MUST open a side panel with the key field and show/hide only.
- **FR-054**: Verbose chat MUST move to Profile → Chat display, reading and writing the same per-browser preference it uses today, so the setting survives the update; Settings MUST have no Chat section.
- **FR-055**: No device pairing control MUST appear anywhere in Settings, even when `sandbox.experimental.device_pairing_enabled` is true (#1916).
- **FR-056**: `DELETE /credentials/{key}` MUST no longer call `requireReAuth` (S-Q11 B, C-5); it keeps `withAuth`, the existing admin and bypass guards and the audit entry. `POST /credentials` and `POST /credentials/rotate` MUST keep requiring the token. The screen MUST ask one confirm before deleting.
- **FR-057**: Rotate (gateway token) MUST ask a confirm saying every app and program using the token is signed out; Cancel keeps the token.
- **FR-058**: Limits MUST hold three collapsible groups: Context and tool results, Model overrides, Agents and goals.
- **FR-059**: Every row of the 88-row map (design-notes §2), as corrected by "Badge corrections from the code", MUST be in the place its row names, or absent for rows marked Removed, Hidden or Moved out (Dataset D-9).
- **FR-060**: The tool permission grid, web search and skill trust MUST stay in Settings in their current places until the Skills & Tools page has landed; once it has, they MUST leave Settings and Protection MUST show an info notice linking to Skills & Tools (S-Q3 A, S-Q8).
- **FR-061**: No backup or restore control MUST appear (#1919); old links show the FR-003 notice.

### States, accessibility and wording (User Story 9)

- **FR-065**: Each section MUST show "Loading <section>…" with a spinner and Skeleton cards until its reads answer.
- **FR-066**: A section whose read fails MUST show an error Callout naming what failed, quoting the server's cause, giving one thing to do and offering Retry; nothing in that section can be saved.
- **FR-067**: When the gateway does not answer, one error Callout MUST show between the page title and the sections (above the open section on a phone) saying values may be out of date, with Retry.
- **FR-068**: "Skip to settings content" MUST be the first Tab stop and move focus to the section content without changing the address.
- **FR-069**: Stacked dialogs MUST close one at a time on Escape or Cancel, returning focus to the control that opened each; when that control no longer exists, focus goes to the section heading.
- **FR-070**: At 390 px every visible control MUST measure at least 44 × 44 px and the page MUST NOT scroll sideways.
- **FR-071**: The Save bar and saved signal MUST be live regions; errors MUST use `role="alert"`.
- **FR-072**: Every grouped list MUST start collapsed (`aria-expanded="false"`), including Model overrides (S-Q12 A).
- **FR-073**: No on-screen text MUST contain an issue number, a requirement tag (R30, S-Q8 and similar) or "contact support".
- **FR-074**: The Sign-in section and its dialogs MUST say "browser" and "sign-in", never "session" alone, because "Clear all sessions" in Data & retention means chat sessions.
- **FR-075**: Under reduced motion, Skeleton pulse, spinner rotation and row highlight MUST stop.

### [#1096] Several browsers signed in (User Stories 7 and 8)

- **FR-076** [#1096]: Each sign-in MUST store only `{id, hash, issued_at, label, device_kind}`; the label MUST come from a fixed browser and system table (Ambiguity Warning 2), "Unknown browser" otherwise; no IP address and no raw User-Agent may be stored, logged in the list or served.
- **FR-077** [#1096]: The session cookie value MUST be `<id>.<secret>`, `id` matching `^[A-Za-z0-9_-]{16}$`; resolving it MUST cost at most one bcrypt comparison (by id lookup); a legacy cookie (no id prefix) MUST be compared only with the migrated legacy entry.
- **FR-078** [#1096]: At most 10 sign-ins per account. A new sign-in MUST first prune expired entries, then, if 10 remain, end the one with the oldest last use (tie: the one signed in earlier), all inside the one serialised config write so concurrent sign-ins never exceed 10 (Dataset D-5).
- **FR-079** [#1096]: The server MUST refuse a sign-in whose age is 24 hours or more, whatever the browser sends (Dataset D-6); refused means 401 on authenticated routes and anonymous on optional-auth routes.
- **FR-080** [#1096]: `GET /auth/browser-sessions`, `DELETE /auth/browser-sessions/{id}` and `POST /auth/browser-sessions/sign-out-others` MUST exist as in C-1, behind `withAuth` and `middleware.RequirePersonSession` (CLI token, dev bypass and legacy env token → 403; none → 401). The list MUST order the current sign-in first, then by last use newest first, and mark `current` only for the caller's own browser sign-in.
- **FR-081** [#1096]: DELETE and POST MUST require the existing double-submit CSRF header; the cookie MUST stay `HttpOnly`, `SameSite=Strict` and `Secure` when served over HTTPS, as today.
- **FR-082** [#1096]: `DELETE /auth/browser-sessions/{id}` MUST answer the same 404 body for unknown, malformed, ended and expired ids; 409 for the caller's own sign-in; 204 otherwise (Dataset D-7). `sign-out-others` MUST answer 409 when the request has no browser sign-in, else 200 `{ended: n}`.
- **FR-083** [#1096]: A password change MUST end every browser sign-in of the account, the changing browser included. What it does to app (bearer) tokens stays as today.
- **FR-084** [#1096]: Every way a sign-in ends (End, sign-out-others, Sign out, password change, eviction, expiry) MUST close that sign-in's WebSocket connections (chat and live browser) within 5 seconds; their reconnect MUST be refused.
- **FR-085** [#1096]: On the first config load after the update, an existing `session_token_hash` with `session_issued_at` MUST become the first `sessions` entry (issue time kept; missing issue time → migration time), and the legacy fields MUST be removed on the next save. Nobody may be signed out by the update (Q4 A).
- **FR-086** [#1096]: The planted-cookie guard (`middleware/planted_cookie.go::genuineSessionResend`, ADR-094 FR-015) MUST keep its behaviour over the set: it identifies the genuine cookie by id, re-sends it with its own original expiry, and leaves other sign-ins untouched.
- **FR-087** [#1096]: `POST /auth/logout` MUST end only the caller's sign-in. Ending browsers MUST NOT touch app (bearer) tokens. A platform-mode sign-in MUST join the set like a local one; the desktop app's claim step MUST re-send the same sign-in and add no entry.
- **FR-088** [#1096]: Sign-in, End, sign-out-others and eviction MUST each write one audit entry naming the event, the account and the session id; no entry may contain a cookie value or hash.
- **FR-089** [#1096]: `GET /config` MUST never serve `sessions` (nor any bcrypt hash) under any user; `gateway.users` stays in `blockedPaths` for writes.
- **FR-090** [#1096]: The Sign-in section MUST show one row per live sign-in (label, device icon, "Signed in" and "Last used" as relative times), "This browser" on the current one with no End button, an "End <label>" button on the others with the confirm "End session"/Cancel, "Sign out other browsers…" with a confirm that says "You stay signed in here", the line "Up to 10 browsers stay signed in at once. Signing in on an 11th ends the one used longest ago.", the Empty state "No other browsers are signed in", "That browser was already signed out." after a 404, and an error Callout with Retry when the list fails.
- **FR-091** [#1096]: Last use MUST be kept in memory only and updated on each authenticated request of that sign-in; after a gateway restart it MUST equal the issue time until the browser's next request. It is not written to `config.json` (Clarification 3).

---

## Riders

Open hardening issues on the same files (titles and state read with `gh issue view` on 2026-11-20; all OPEN). The chief confirms the list before the design is final.

| Issue | Title (short) | How this package handles it |
|---|---|---|
| #1154 | Reload reopens Providers, not the tab you were on | Fixed by FR-002 |
| #1153 | Cancelling the password prompt leaves the typed key | Fixed by FR-029 (panel and key stay on purpose; the bug's wrong state is the old dialog flow) |
| #1265 | "Clear sessions": admin-only, re-confirm, honest wording | Rider: Data & retention Danger zone keeps one confirm with honest wording ("chat sessions") |
| #1596 | Ask-list text names retired skill tools | Rider: fix the text where the list moves (Protection) |
| #346 | `useAutoSave` unmount flush swallows save errors | Closed by FR-010/FR-015: the autosave path is removed from Settings |
| #1560 | About shows no build sha | Rider: About shows the build sha |
| #1145 | About and version API show 0.0.0-dev | Rider on the same About read (build stamping is outside Settings; owner confirms) |
| #1742 | Audit log viewer reads the wrong folder | Rider: the Protection audit row links to the viewer; fix the path there |
| #1940 | Audit viewer cannot open `external_cli.run` entries | Rider (same viewer) |
| #1641 | Agent edit overlay stays on top of Settings | Rider: System agents' Edit opens the panel; FR-069 focus rules apply |
| #853 | Security health says "Sandbox is disabled" on a fresh install | Rider: Protection health row |
| #936 | Performance settings writable through `PUT /config` and `set_config` without step-up | Rider: same `blockedPaths` change as FR-044 extended to the performance keys |
| #1917 | Saving rate limits re-applies the whole live config | Rider: Protection rate-limit Save |
| #995 | Saves report success when the internal rebuild fails | Rider, Settings-section endpoints only: FR-034/FR-015 show the failure |
| #1595 [#1096] | Browser sign-in seems to end an API session | Closed by FR-087 (verify with the test) |
| #1146 [#1096] | An ended sign-in loses the unsent message | Rider: required for FR-084's "next action leads to sign-in" |
| #598 [#1096] | Auth rate limiters are package globals | Rider: the new routes add no new global limiter; tests isolate the existing ones |
| #689 [#1096] | Web login is single-slot | Closed by this package (FR-076 to FR-091) |

---

## Success Criteria

- **SC-001**: 100 % of Dataset D-3 links open the listed target (automated, Vitest + Playwright).
- **SC-002**: 0 values stored without Save changes across every section (integration test counting writes per change).
- **SC-003**: Every Save with password-marked changes makes exactly 1 password prompt, 1 `POST /auth/reauth` and 1 protected write (Dataset D-8, guard test FR-030).
- **SC-004**: Every key of D-4 rows 1–7 appears in `/config/pending-restart` after a save that differs from the running value; rows 8–10 never appear.
- **SC-005**: 100 % of Datasets D-1, D-2 and D-12 rows produce the stated status and leave `config.json` byte-identical on refusal.
- **SC-006**: 88 of 88 map rows found in their mapped place (hands-on G-18, D-9 automated).
- **SC-007** [#1096]: With 10 browsers signed in, all 10 answer 200; the 11th sign-in ends exactly the D-5 target; 1 bcrypt comparison per request; ended sockets close within 5 s in 6 of 6 end causes.
- **SC-008** [#1096]: 0 people signed out by the update in the migration test (G-21).
- **SC-009**: 0 serious or critical axe violations on each of the 12 sections; every control ≥ 44 × 44 px at 390 px.

---

## Reachability

| Deliverable | How a person or agent reaches it | Check |
|---|---|---|
| Settings screen (12 sections) | Route `/settings` (`src/routes/_app/settings.tsx`), from the account menu, the Omnipus chat gear and the in-app links of FR-009 | Playwright opens it from the account menu, not only by URL |
| Profile → Chat display | Route `/profile` (`src/routes/_app/profile.tsx`) | G-17 |
| `/settings/retention`, `/auth/browser-sessions*` | Called only by the Settings screen; registered in the gateway route table next to `/settings/memory` and `/auth/*` | route-table test asserts each path is registered with its middleware |
| `RestartGatedKeys` additions | Read by `GET /config/pending-restart`, shown by the restart banner | G-11 |
| Agent tools | None. No tool added or changed; no `sandbox.tool_policies` entry needed (Hard Constraint #6 untouched). `set_config` still cannot write `storage.*` or `gateway.users` | `grep -rl` for the new keys in `pkg/sysagent/tools/` returns 0 by design |

**Where tests run.** RED (qa-lead): the Go tests of "Test Implementation Order" rows 1–20 in `pkg/gateway`, `pkg/gateway/middleware`, `pkg/config` and (FQ-1 A only) `pkg/agent`; Vitest rows 21–31 next to the Settings components; Playwright row 32 as one targeted spec file. GREEN (backend-lead, frontend-lead) runs only those targeted tests under the own-tests rule. CHECK (a different qa-lead instance) audits them. Full suites run once, at the cut.

**Hands-on UAT on the exact cut commit is required.** #1096 changes the gateway's WebSocket authentication path (FR-084) and FQ-1 A changes `pkg/agent`; both fall under the root CLAUDE.md agent-engine rule. Rows G-01 to G-25 are written above.

---

## User-facing documentation

| ID | Page | TODO |
|---|---|---|
| DOC-001 | `docs/settings.md` | Rewrite the overview: 12 sections in three groups, what each holds, the search box |
| DOC-002 | `docs/settings.md` | Rewrite the old-tab → new-place map table (today's table near the end of the page) from FR-003 and the 88-row map |
| DOC-003 | `docs/settings.md` | Explain the save rule (Save bar, Discard, leave prompt), the password once per Save, the Needs restart badge and the restart banner |
| DOC-004 | `docs/settings.md` | Data & retention: two settings with ranges and defaults, Keep forever for memory, the legacy config-file switch and its notice, that `/settings/retention` is the only write path |
| DOC-005 | `docs/memory.md` | Replace the retention lines (today around lines 121–122) with a pointer to Data & retention, the 1–730 range, Keep forever, and the recall window per FQ-1 |
| DOC-006 | `docs/settings.md`, `docs/providers-and-models.md` | Limits groups (context, model overrides, agents and goals), Voice input under Providers, Active transcriber needs the password |
| DOC-007 | `docs/security.md` | God Mode now in Settings → Protection; Auto-approve and its password; moved-items note for tool permissions and skill trust (once Skills & Tools lands) |
| DOC-008 | `docs/using-omnipus-ui.md` | Verbose chat in Profile → Chat display; device pairing not in Settings |
| DOC-009 | `docs/security.md` | [#1096] Signing in on several browsers: up to 10, the 11th ends the one used longest ago, 24-hour sign-in, Sign out ends this browser only, password change ends all, how to end a lost device's sign-in, and that a stolen sign-in is ended by changing the password |
| DOC-010 | `docs/settings.md` | Keys & secrets: deleting a key asks a confirm, not the password; adding and rotating still ask the password |
| DOC-011 | the 19 user pages that name a Settings path (`grep -rcE 'Settings (→|>) \|tab=' docs` outside `docs/internal`: connectors, custom-endpoint, security, tutorial, goals, tools, using-omnipus-cli, settings, reference/built-in-tools, troubleshooting, agents, using-omnipus-ui, memory, providers-and-models, operations/sandbox-config, migration/model-list-migration, operations/sandbox-limitations, connectors/whatsapp_native, connectors/discord) | Update every "Settings → <old tab>" path and `?tab=` link to the new section name |
| DOC-012 | `docs/internal/architecture/ADR-022-credential-vault-reauth.md` | Dated correction: delete no longer needs the password (S-Q11 B, FQ-2), set and rotate still do |

docs-verifier audits DOC-001 to DOC-011 against the landed behaviour; DOC-012 is an internal record the architect writes at landing.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) | DOC |
|---|---|---|---|---|
| FR-001 | US-1 | Person opens Settings and sees 12 sections in three groups | settingsRoute.legacyTabs.test; G-01 | DOC-001 |
| FR-002 | US-1 | Reload keeps the open section; Browser Back moves to the previous section | settingsRoute.legacyTabs.test | DOC-001 |
| FR-003 | US-1 | Old and new section links land in the right place; Unknown section name falls back to Providers | settingsRoute.legacyTabs.test (D-3) | DOC-002, DOC-011 |
| FR-004 | US-1 | The "No context length" link pre-fills a model override | settingsRoute.legacyTabs.test (D-3 rows 13–14) | DOC-006 |
| FR-005 | US-1 | The God Mode pill lands on the God Mode button | settingsRoute.legacyTabs.test (D-3 row 12) | DOC-007 |
| FR-006 | US-1 | Search results; Search with no match shows an empty state | settingsSearch.test (D-13) | DOC-001 |
| FR-007 | US-1, US-6 | Searching for a moved setting says where it went; Device pairing stays hidden when switched on | settingsSearch.test | DOC-001 |
| FR-008 | US-1 | On a phone the list comes first | settings-redesign.spec.ts; G-04 | DOC-001 |
| FR-009 | US-1 | Links from other screens keep working | settingsRoute.legacyTabs.test; chat leaveSearch test (regression) | DOC-011 |
| FR-010 | US-2 | A changed value shows the Save bar and stores nothing; A recap model choice waits for Save | saveBar.counting.test | DOC-003 |
| FR-011 | US-2 | A value changed back by hand is no longer counted; Typing in an Add field is not a change until Add is pressed | saveBar.counting.test | DOC-003 |
| FR-012 | US-2 | Discard returns every change to its saved value | saveBar.counting.test | DOC-003 |
| FR-013 | US-2 | An invalid value blocks saving and names the range | saveBar.counting.test (D-10) | DOC-003 |
| FR-014 | US-2 | Leaving with unsaved changes asks first; Every way out asks first; Leave without saving drops the change | saveBar.leaveGuard.test | DOC-003 |
| FR-015 | US-2 | One of two writes fails; A Save while the gateway is down keeps every change | saveBar.partialFailure.test | DOC-003 |
| FR-016 | US-2 | Lowering the tool-call limit previews affected agents; Choosing All interfaces asks before saving | saveBar.counting.test; PerformanceSection.maxToolIterations.test (regression) | DOC-003 |
| FR-017 | US-2 | Only one gold button while the Save bar shows | saveBar.counting.test | — (visual only; no doc change needed) |
| FR-018 | US-2, US-9 | Save stores the value…; Saving is announced | saveBar.counting.test | DOC-003 |
| FR-019 | US-2 | Edge case "two browser tabs" (US-2 Edge Cases) | saveBar.partialFailure.test | DOC-003 |
| FR-025 | US-3 | Active transcriber needs the password | passwordOncePerSave.test | DOC-006 |
| FR-026 | US-3 | Two password-marked changes need one password | passwordOncePerSave.test (D-11) | DOC-003 |
| FR-027 | US-3 | Each section sends its password-marked values to at most one protected endpoint | sectionPasswordEndpoints.guard.test (D-8) | DOC-003 |
| FR-028 | US-3 | The typed password is not kept | passwordOncePerSave.test | DOC-003 |
| FR-029 | US-3 | Wrong password; Too many password tries; Cancelling the password prompt keeps every change | passwordOncePerSave.test (D-11) | DOC-003 |
| FR-030 | US-3 | Each section sends its password-marked values to at most one protected endpoint | sectionPasswordEndpoints.guard.test | — (internal guard) |
| FR-031 | US-3 | A late change in the affected agents asks again | passwordOncePerSave.test | DOC-003 |
| FR-032 | US-3 | Platform mode asks for a confirmation instead | passwordOncePerSave.test | DOC-003 |
| FR-033 | US-4 | A Save that needs a restart offers it at once | restartBanner.test | DOC-003 |
| FR-034 | US-4 | A failed reload still says a restart is needed | restartBanner.test | DOC-003 |
| FR-035 | US-4 | Later leaves a banner on every section; Show changes lists running and saved values | restartBanner.test | DOC-003 |
| FR-036 | US-4 | The server lists every restart-gated setting that was saved | TestPendingRestart_IncludesFilesystemModelAndProxy (D-4) | DOC-003 |
| FR-037 | US-4 | Defense level applies at once | restartBanner.test; TestPendingRestart_IncludesFilesystemModelAndProxy (D-4 rows 8–10) | DOC-003 |
| FR-038 | US-4 | The restart clears the banner; Saving the running value again clears the banner | restartBanner.test; TestPendingRestart_IncludesFilesystemModelAndProxy (D-4 row 11) | DOC-003 |
| FR-039 | US-4 | The restart list cannot be loaded | restartBanner.test | DOC-003 |
| FR-040 | US-5 | With Keep forever on the day field is off and not sent | dataRetention.section.test | DOC-004 |
| FR-041 | US-5 | Fresh-install defaults | TestRetentionSettings_Ranges; dataRetention.section.test | DOC-004 |
| FR-042 | US-5 | The server enforces the ranges | TestRetentionSettings_Ranges (D-1, D-2) | DOC-004 |
| FR-043 | US-5 | Saving both day counts | TestRetentionSettings_Ranges | DOC-004 |
| FR-044 | US-5 | The general configuration write refuses retention; Saving Gateway never sends retention | TestUpdateConfig_RefusesStorageRetention (D-12) | DOC-004 |
| FR-045 | US-5 | The memory settings refuse retention fields | TestMemorySettings_RefusesRetentionFields | DOC-005 |
| FR-046 | US-5 | A hand-edited value outside the range is shown and blocks Save | dataRetention.section.test (D-1 row 10) | DOC-004 |
| FR-047 | US-5 | The old retention route is gone | TestSecurityRetentionRoute_Removed | DOC-004 |
| FR-048 | US-5 | The legacy keep-everything switch is shown, not hidden | TestRetentionSweep_LegacyDisabledKeepsAll; dataRetention.section.test | DOC-004 |
| FR-049 | US-5 | Keep forever stops memory deletion; Turning Keep forever off again resumes deletion | TestRetentionSweep_MemoryForeverKeepsRetros; _ForeverOffResumes | DOC-004, DOC-005 |
| FR-050 | US-5 | [Pending FQ-1] An old reflection kept by the setting can be recalled | TestSearchRetros_FollowsRetentionWindow | DOC-005 |
| FR-051 | US-6 | God Mode sits in Protection; The God Mode button acts alone | settingsMap.test; G-16 | DOC-007 |
| FR-052 | US-5 | Memory & recaps holds only a link to retention | settingsMap.test | DOC-005 |
| FR-053 | US-6 | Voice input lives in Providers | settingsMap.test | DOC-006 |
| FR-054 | US-6 | Verbose chat lives in Profile | settingsMap.test | DOC-008 |
| FR-055 | US-6 | Device pairing stays hidden when switched on | settingsMap.test | DOC-008 |
| FR-056 | US-6 | Deleting a stored key needs one confirm; The server deletes a key without a password token; Adding a key still needs the password | TestDeleteCredential_NoReauthNeeded; TestSetCredential_StillNeedsReauth | DOC-010, DOC-012 |
| FR-057 | US-6 | Rotating the gateway token asks first | settingsMap.test | DOC-001 |
| FR-058 | US-6 | Limits holds three groups | settingsMap.test | DOC-006 |
| FR-059 | US-6 | Every inventory control is where the map says | settingsMap.test (D-9); G-18 | DOC-002 |
| FR-060 | US-6 | Moved items are announced in Protection; Controls whose new home has not landed stay put | settingsMap.test | DOC-007 |
| FR-061 | US-6 | Old and new section links land in the right place (D-3 row 11) | settingsRoute.legacyTabs.test | DOC-002 |
| FR-065 | US-9 | Every section has a loading state | settings-redesign.spec.ts; G-25 | — (states; no doc change needed) |
| FR-066 | US-9 | A section that cannot load | settings-redesign.spec.ts | DOC-001 |
| FR-067 | US-9 | The gateway does not answer | settings-redesign.spec.ts | DOC-001 |
| FR-068 | US-9 | The skip link is the first Tab stop | settings-redesign.spec.ts | — (a11y; no doc change needed) |
| FR-069 | US-9 | Stacked dialogs return focus to their opener | settings-redesign.spec.ts | — (a11y) |
| FR-070 | US-9 | Touch targets and width on a phone | settings-redesign.spec.ts; G-04 | — (a11y) |
| FR-071 | US-9 | Saving is announced | saveBar.counting.test | — (a11y) |
| FR-072 | US-9 | Grouped lists start collapsed | settingsMap.test | — (a11y) |
| FR-073 | US-9 | On-screen text carries no tags or support line | settingsMap.test (text sweep) | — (wording rule) |
| FR-074 | US-8 | [#1096] The list shows every signed-in browser | signInSection.test | DOC-009 |
| FR-075 | US-9 | Reduced motion stops animations | settings-redesign.spec.ts | — (a11y) |
| FR-076 | US-7 | [#1096] The configuration read never shows sign-ins; [#1096] The list shows every signed-in browser | TestBrowserSessions_ListEndSignOutOthers | DOC-009 |
| FR-077 | US-7 | [#1096] One password-strength check per request | TestBrowserSessions_OneBcryptPerRequest | DOC-009 |
| FR-078 | US-7 | [#1096] The 11th sign-in ends the one used longest ago; [#1096] Simultaneous sign-ins never exceed the cap | TestBrowserSessions_EvictsLeastRecentlyUsed (D-5) | DOC-009 |
| FR-079 | US-7 | [#1096] A sign-in older than its lifetime is refused | TestBrowserSessions_ExpiryEnforcedServerSide (D-6) | DOC-009 |
| FR-080 | US-8 | [#1096] Only a signed-in person may use the browser list; [#1096] The list shows every signed-in browser | TestBrowserSessions_PersonOnly; TestBrowserSessions_ListEndSignOutOthers | DOC-009 |
| FR-081 | US-8 | [#1096] Ending a sign-in without the CSRF header is refused | TestBrowserSessions_ListEndSignOutOthers | — (no user-visible effect) |
| FR-082 | US-8 | [#1096] Ending one sign-in by id; [#1096] Sign out other browsers needs a browser sign-in | TestBrowserSessions_ListEndSignOutOthers (D-7) | DOC-009 |
| FR-083 | US-7 | [#1096] A password change ends every sign-in | TestChangePassword_EndsAllBrowsers | DOC-009 |
| FR-084 | US-7 | [#1096] Ending a sign-in closes its live connection; [#1096] Every way a sign-in ends closes its live connections | TestBrowserSessions_EndClosesWebSockets; G-22 | DOC-009 |
| FR-085 | US-7 | [#1096] The sign-in from before the update keeps working | TestBrowserSessions_LegacyMigrationKeepsSignIn; G-21 | DOC-009 |
| FR-086 | US-7 | [#1096] The planted-cookie protection still works with several sign-ins | TestPlantedCookieGuard_WithSessionSet | — (no user-visible change) |
| FR-087 | US-7 | [#1096] Sign out ends only this browser; [#1096] App tokens are not touched; [#1096] A platform-mode sign-in joins the set | TestLogout_EndsOnlyThisBrowser; TestBrowserSessions_SecondSignInKeepsFirst | DOC-009 |
| FR-088 | US-7 | [#1096] Sign-in events are audited | TestBrowserSessions_Audit | DOC-009 |
| FR-089 | US-7 | [#1096] The configuration read never shows sign-ins | TestGetConfig_NeverServesSessions | — (no user-visible change) |
| FR-090 | US-8 | [#1096] list, End, Sign out other browsers, only-this-browser, already-gone, cannot-load scenarios; End buttons are named for their browser | signInSection.test; G-23 | DOC-009 |
| FR-091 | US-7 | [#1096] A gateway restart keeps every sign-in | TestBrowserSessions_ListEndSignOutOthers | DOC-009 |

Check: 83 FRs, each with a story, at least one scenario and one named test; every acceptance scenario of US-1 to US-9 is the "Traces to" of at least one BDD scenario.

---

## Assumptions

- GitNexus MCP tools were not available in this session; every code fact was read with Grep and Read at `c2f8f8e735` (the fallback the root CLAUDE.md allows). The two HIGH-impact rows in "Existing Codebase Context" are therefore **Inferred**, not graph-verified; backend-lead runs `impact` on `ResolveUserFromCookie` and `genuineSessionResend` before editing.
- No interview-me output exists for this feature; the founder's wireframe answers (S-Q1 to S-Q14, #1096 Q1 to Q4) take its place (team-lead's brief).
- `sandbox.filesystem_model` and `tools.exec.enable_proxy` need a restart to take effect (**Inferred**, medium confidence, from where they are read); RED proves it before FR-036 is implemented. If either turns out hot-reloaded, it is not added and its badge is dropped.
- The product holds at most one human account (`GatewayConfig.Users` comment); the session set is per account anyway.
- Skills & Tools may land before or after this package; FR-060 covers both orders.
- Today's password change behaviour for app (bearer) tokens is kept as is (**Unknown** whether it revokes them; not changed by this spec).

## Clarifications

### 2026-11-20

- Q: Where do chat-history and memory retention get written? → A: one new endpoint `/settings/retention`; `storage.retention` is blocked on `PUT /config` and removed from `/settings/memory`; `/security/retention` GET/PUT are deleted (architect decision, C-3).
- Q: B4 — multi-use token or one token per Save? → A: one token per Save, no server change, because a Save covers one section and each section has at most one protected endpoint (architect decision, C-4; guard FR-030).
- Q: Where is a browser's last use kept? → A: in memory only, so authenticated requests cause no config writes; after a restart it shows the sign-in time until next use (architect decision, FR-091).
- Q: How does a request find its sign-in among up to 10? → A: the cookie carries a public id before the secret, so one bcrypt compare per request (architect decision, FR-077; threat T4).
- Q: Is #1096 in this package? → A: yes, fully in scope, kept tagged [#1096] (chief, 2026-11-20 04:38Z).

## Questions for the founder

- **FQ-1 — Recall window for kept memory.** Today agents can recall at most 365 days of reflections, whatever is kept. With memory kept up to 730 days or forever, older reflections would be stored but never found. **A (recommended)**: recall reaches as far back as memory is kept (touches `pkg/agent`, needs hands-on testing). **B**: keep the 365-day recall limit and say so on screen. Answer "FQ-1 A" or "FQ-1 B".
- **FQ-2 — Deleting a stored key without the password.** S-Q11 B (confirm only) removes a password check an accepted decision record (ADR-022) added on purpose: someone at an unlocked, signed-in screen could then delete a provider key. **A (recommended, as answered)**: confirm only, audited. **B**: keep the password for delete. Answer "FQ-2 A" or "FQ-2 B".
- **FQ-3 — How long a browser stays signed in.** A sign-in lasts 24 hours today; the wireframe shows a row "last used 3 days ago", which cannot happen. **A (recommended)**: keep 24 hours in this package; the sample row changes. **B**: open a follow-up to lengthen it (for example 7 days with the list as the safety net). Answer "FQ-3 A" or "FQ-3 B".
