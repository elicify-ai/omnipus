# Feature Specification: Security settings — UI/config parity + score-display correction

**Created**: 2026-04-22
**Revision**: 2 (adversarial-review pass — resolves all 4 CRITICAL + 9 MAJOR findings from `sprint-k-security-ui-parity-spec-review.md`)
**Status**: Draft — revised after grill-spec
**Input**: Public-IP testing on 2026-04-21/22 surfaced four defects in the Settings → Security screen; PR #137 landed three of them but introduced a regression in Bug 1 (security-score inversion). The operator also flagged that several security-critical settings remain only editable via `config.json`. This spec plans a single sprint covering (a) the Bug 1 correction, (b) completion of the already-in-flight UI edits, and (c) the full UI/config-parity closure for nine remaining gaps including admin user CRUD.

## Revision History

### Revision 2 — 2026-04-22 (post-grill-spec)

Adversarial review (`docs/internal/plan/sprint-k-security-ui-parity-spec-review.md`) flagged 4 CRITICAL + 9 MAJOR findings rooted in specific code disagreements. All addressed:

- **CRIT-001** (SSRF shape): removed the invented `allow_internal` bool and `allow_internal_cidrs` list. US-7 now uses the existing `OmnipusSSRFConfig.AllowInternal []string` field unchanged; the UI presents presets + advanced list that both write to that one field.
- **CRIT-002** (Retention semantics): REFUSED to flip `session_days <= 0` from "default 90" to "keep forever". Added a NEW `OmnipusRetentionConfig.Disabled bool` field for the explicit "keep forever" intent. Regression test `TestRetention_ZeroSessionDaysStillMeansDefault90` guards the existing semantics.
- **CRIT-003** (User creation + tokens): removed the one-time-token modal entirely. User creation now sets password only; new user logs in via existing `/auth/login` to mint a token (matches today's login-time token issuance). No coexisting token lifecycles.
- **CRIT-004** (AllowedPaths R/W): stated read-only explicitly. Removed "write stripped" language. Every row gets a read-only badge.
- **MAJ-001** (issue #103 → #138): fixed BDD scenario regex. Added FR-021 requiring a single source-of-truth constant in Go.
- **MAJ-002** (DMScope values): expanded to all four canonical values (`main`, `per-peer`, `per-channel-peer`, `per-account-channel-peer`). Removed `global`.
- **MAJ-003** (reset-password zeroes TokenHash): made the mechanism explicit in FR-009 — same `safeUpdateConfigJSON` transaction zeroes TokenHash and sets PasswordHash; added BDD asserting pre-reset token returns 401.
- **MAJ-004** (blockedKeys nested path support): added FR-018 replacing flat `blockedKeys map[string]bool` with `blockedPaths []string` and a dotted-path walker. New regression test covers three attack shapes.
- **MAJ-005** (last-admin race): moved the guard INSIDE `safeUpdateConfigJSON` callback. Added BDD and integration test for concurrent-demotion race.
- **MAJ-006** (dev_mode_bypass elevation): added FR-019 requiring all new endpoints to return 503 under bypass; UI hides the Access tab and banner.
- **MAJ-007** (change-password collision): removed `PUT /users/self/password`. Reuse existing `POST /api/v1/auth/change-password`.
- **MAJ-008** (revert-before-restart): specified `pending-restart` as a diff (persisted vs applied), not history. Added BDD: set-then-revert clears banner.
- **MAJ-009** (abi_version null vs omitted): restated UI handling as `typeof !== 'number'` since the field is int-omitempty (absent on non-Linux/failed-probe), not null.
- **MIN-003** (audit logging): added FR-020 requiring all state-changing endpoints to emit audit entries with redacted secrets.

Other MINOR and OBSERVATION findings from the review (MIN-001 skill-trust casing, MIN-002 sweep goroutine lifecycle, MIN-004 rate-limit coverage, MIN-005 clipboard fallback, MIN-006 self-change-password tests, MIN-007 PromptGuardSection in symbols table, OBS-001..004 diagrams/accessibility/i18n) are addressed inline in the affected sections or left as implementation-time concerns.

### Revision 1 — 2026-04-22 (initial draft via `/plan-spec`)

Initial spec covering Bug 1 correction + 9 UI-parity gaps + admin user CRUD + pending-restart banner + ABI v4 warning.

---

---

## Context & Intent

- Omnipus OSS is plain-HTTP-deployable behind an operator-managed proxy. Every security knob MUST be reachable from the Settings UI so operators don't ship hand-edited `config.json` into production.
- PR #137 already shipped:
  - Bug 2: conditional sandbox status note (`DisabledBy` branches)
  - Bug 3: CSRF Bearer bypass + plain-HTTP cookie downgrade
  - Bug 4 (partial): `GET/PUT /api/v1/security/sandbox-config` + `SandboxSection` mode radio group
- Bug 1 was shipped INVERTED — backend `score` is already a security-goodness value (higher=better), but the UI applied `100 − score` on top, making healthy deployments look critical. **This spec treats Bug 1 as the first item to fix.**

**Intended outcome**: one sprint PR (branch `sprint-k-security-ui-parity`, base `main` after #137 merges) that lands the Bug 1 correction, completes the UI editors for every operator-controllable security setting, and surfaces a consistent "restart-required" UX for settings that don't hot-reload.

---

## Available Reference Patterns

No `docs/reference/` directory in this repo — no external reference library applies. Internal patterns reused below:

| Internal file | Pattern | Relevance |
|---|---|---|
| `pkg/gateway/rest_security_wave5.go` (PR #137) | `HandleSandboxConfig` with GET/PUT merge + partial updates via pointer-null | Template for every new config editor endpoint in this sprint |
| `pkg/gateway/middleware/csrf.go` | Bearer bypass + `__Host-csrf`/`csrf` fallback | All new state-changing endpoints inherit this automatically |
| `src/components/settings/SandboxSection.tsx` (PR #137) | Admin-gated edit toggle + restart-required banner | Template for every new settings editor component |
| `pkg/gateway/rest.go::HandleToolPolicies` (`security/tool-policies`) | Admin-only PUT that persists via `safeUpdateConfigJSON` | Same persistence flow; differs only in schema |

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|---|---|---|
| `src/components/settings/DiagnosticsSection.tsx` (`toSecurityScore`, `getSecurityLabel`, `getSecurityColor`) | **modifies** | Bug 1 fix removes the `100 − risk` inversion; relabels thresholds so higher = better |
| `src/components/settings/DiagnosticsSection.test.tsx` | **modifies** | Assertions re-aligned after the inversion removal |
| `pkg/gateway/rest.go::runDiagnosticChecks` | **calls (read-only)** | Authoritative score producer. Shape: `score := 100; score -= penalty` with penalties 20/10/5 per high/medium/low issue |
| `pkg/gateway/rest_security_wave5.go::HandleSandboxConfig` | **extends** | Gains `allowed_paths` and `ssrf.allow_internal` list editors (schema already persisted, just missing in the UI form) |
| `src/components/settings/SandboxSection.tsx` | **extends** | Adds paths list + SSRF allow-internal editor + ABI v4 warning + restart-required banner |
| `pkg/gateway/rest_security_wave5.go` (new handlers) | **extends** | Adds `HandleSandboxAuditLogConfig`, `HandleSandboxSkillTrust`, `HandleSandboxPromptInjection`, `HandleSandboxRateLimits` — all admin-only PUT/GET operating on sub-keys of `sandbox.*` via `safeUpdateConfigJSON` |
| `pkg/gateway/rest.go` (new handlers) | **extends** | `HandleUsersList`, `HandleUserCreate`, `HandleUserUpdate`, `HandleUserDelete`, `HandleUserResetPassword` (admin-only), `HandleSelfChangePassword` (any authenticated user, requires current password) |
| `pkg/gateway/rest.go::blockedKeys` (line ~1622) | **modifies (narrow)** | Keep "sandbox" blocked from generic PUT; add "gateway.users" to the block list because the dedicated user-admin endpoints handle it |
| `src/components/settings/UsersSection.tsx` (new) | **new** | List / create / delete / admin-reset-password / change role (no self-service token rotation) |
| `src/components/settings/RateLimitsSection.tsx` (new) | **new** | Daily cost cap + per-agent LLM/tool call limits (read-only field exists today as comment in `SecuritySection` — promote to editable) |
| `src/components/settings/RetentionSection.tsx` (new) | **new** | `storage.retention.session_days` slider |
| `src/components/settings/SessionRoutingSection.tsx` (new) | **new** | `session.dm_scope` selector (if the feature is still in-use — flagged as ambiguity #3 below) |
| `src/components/settings/RestartBanner.tsx` (new) | **new** | Global persistent banner when any restart-required change is queued |
| `src/lib/api.ts` | **extends** | New client helpers for each endpoint + types |
| `src/store/restart.ts` (new zustand store) | **new** | Tracks queued restart-required changes across sections; feeds the banner |

### Impact Assessment

| Symbol Modified | Risk | d=1 Dependents | d=2 Dependents |
|---|---|---|---|
| `DiagnosticsSection.tsx` (Bug 1) | **LOW** | `SecuritySection.tsx` (renders it) | Settings route |
| `runDiagnosticChecks` | — (read-only) | N/A | N/A |
| `HandleSandboxConfig` extension | **LOW** | SandboxSection, sandbox_apply_test | None |
| `safeUpdateConfigJSON` (reused, not modified) | **NONE** | All config mutators | Config-snapshot reload |
| `blockedKeys` add `gateway.users` | **MEDIUM** | HandleConfig PUT callers | SPA settings screens (all) |
| Zustand `restart` store (new) | **NONE** | RestartBanner, every edit Section | None |

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| **Config write-path**: `UI → PUT /api/v1/security/<sub-endpoint> → safeUpdateConfigJSON → atomic-rename config.json → hot-reload signal or defer-until-restart` | Every new editor participates in this flow |
| **Doctor evaluation**: boot config → `runDiagnosticChecks` → persisted to `state.json` → GET /doctor → UI card | Bug 1 only touches the UI rendering end; data pipeline stays identical |
| **CSRF gate**: `middleware.CSRFMiddleware → Bearer bypass | cookie check → handler` (PR #137) | All new admin endpoints benefit automatically |
| **Auth gate**: `withAuth → middleware.RequireAdmin → handler` | All new admin endpoints follow this chain |
| **State-reload signal**: gateway config watcher (2s polling) + explicit `/reload` | Hot-reload-eligible settings go live on next poll cycle; restart-required settings do not |

---

## Decisions (user-confirmed 2026-04-22)

| Decision | Choice |
|---|---|
| Sprint scope | **Bug 1 fix + full UI-parity for all 9 gaps** in one PR |
| User management UI | **Admin-only CRUD** — list, create (password, no token), delete (last-admin guard), admin-reset-password (zeroes TokenHash), change role. Self-change-password reuses existing endpoint. No self-service token rotation. |
| Non-hot-reloadable settings UX | **Per-field "restart required" indicator + global banner** listing queued changes |
| Landlock ABI v4 incompatibility | **Warn at save, allow override** — operator sees the known-incompatible warning but can still save |

---

## User Stories & Acceptance Criteria

### US-1 — Correct the security-score display (Bug 1 regression) (Priority: P0)

An operator opens Settings → Security → Diagnostics and sees a security score that matches the real posture of their deployment. Today, PR #137 inverts the backend score, so a healthy gateway (backend `score`=90) displays as "Security Score: 10 — Critical." The operator loses trust in the indicator immediately.

**Why this priority**: shipped-regression in the flagship security screen — undermines the entire indicator until fixed.

**Independent test**: set `last_doctor_score=90` in `state.json`, render the section, assert the UI shows `90 / 100 — Excellent` (green).

**Acceptance Scenarios**:

1. **Given** backend doctor stored `score=90` and `issues=[sandbox-disabled medium]`, **When** the Diagnostics section renders, **Then** the card shows `Security Score: 90 / 100 — Excellent` with a green indicator.
2. **Given** backend doctor stored `score=65` and three issues firing, **When** the Diagnostics section renders, **Then** the card shows `Security Score: 65 / 100 — Good`.
3. **Given** backend doctor stored `score=20`, **When** the Diagnostics section renders, **Then** the card shows `Security Score: 20 / 100 — Critical` with a red indicator.
4. **Given** a successful doctor run returning `score=85`, **When** the success toast fires, **Then** the toast reads `Diagnostics complete — security score: 85/100`.

---

### US-2 — Audit-log toggle (Priority: P0)

An admin on Settings → Security → Audit Log sees the current audit-log state and can toggle it on/off without editing `config.json`. Today the control doesn't exist.

**Why this priority**: audit log is SEC-15 evidence — operators who want accountability should not have to edit JSON.

**Independent test**: PUT `/api/v1/security/audit-log` with `{enabled: true}`, GET back `{enabled: true, persisted: true, requires_restart: true}`; `config.json` has `sandbox.audit_log = true`.

**Acceptance Scenarios**:
1. **Given** admin on Settings → Security → Audit Log, **When** they toggle the switch to ON, **Then** the UI PUTs the change, shows a save confirmation, and displays a "Restart required to activate" indicator on that row.
2. **Given** a non-admin user, **When** they open the same section, **Then** the toggle is disabled (read-only) with a lock icon and tooltip "Admin only".

---

### US-3 — Skill trust level (Priority: P0)

An admin can pick skill-install trust policy (`block_unverified` / `warn_unverified` / `allow_all`) from a dropdown.

**Why this priority**: SEC-09 dictates skill verification behavior. Operators shouldn't ship `allow_all` accidentally by default config drift.

**Independent test**: PUT `/api/v1/security/skill-trust` with `{level: "block_unverified"}`; GET reports the new value; `config.json` updated.

**Acceptance Scenarios**:
1. **Given** admin on Settings → Security → Skills, **When** they change the trust level from `warn_unverified` to `block_unverified`, **Then** future skill-install attempts against unhashed skills fail without prompting.
2. **Given** admin selects `allow_all`, **When** the save succeeds, **Then** a warning banner appears: "Skill trust is allow_all — the gateway will install ANY skill without hash verification. This is dangerous in production."

---

### US-4 — Prompt injection level (Priority: P0)

An admin picks the prompt-guard strictness (`low` / `medium` / `high`) from a dropdown that writes `sandbox.prompt_injection_level`. A read-only `PromptGuardSection` already exists — promote it to editable.

**Why this priority**: SEC-25 baseline defence. Operators need to tune per their risk appetite without a JSON rebuild.

**Independent test**: PUT `/api/v1/security/prompt-guard` with `{level: "high"}`; GET reports `{level: "high"}`; next `read_file` / `web_fetch` result is sanitized at the "high" strictness.

**Acceptance Scenarios**:
1. **Given** admin on Settings → Security → Prompt Injection Defense, **When** they select `high`, **Then** the save succeeds and the section shows `Applied: high` with no restart-required indicator (this is hot-reloadable).
2. **Given** invalid level submitted via direct API call, **When** server validates, **Then** it returns 400 with message "invalid strictness — must be one of: low, medium, high".

---

### US-5 — Rate limits editor (Priority: P0)

An admin edits the three rate-limit knobs: `daily_cost_cap_usd`, `max_agent_llm_calls_per_hour`, `max_agent_tool_calls_per_minute` via numeric inputs with 0 meaning "no limit".

**Why this priority**: SEC-26 cost-cap enforcement is what prevents a runaway agent from burning the operator's OpenAI bill. Unreachable via UI today.

**Independent test**: PUT `/api/v1/security/rate-limits` with `{daily_cost_cap_usd: 50.0, max_agent_llm_calls_per_hour: 200}`; a subsequent test agent exceeding 200 calls/hour gets rate-limited.

**Acceptance Scenarios**:
1. **Given** admin enters `daily_cost_cap_usd: 25.5`, **When** they save, **Then** the value persists and hot-reloads within 2 seconds (config poller); the agent loop sees the new cap on its next cost check.
2. **Given** admin enters a negative value for any field, **When** the UI validates, **Then** the save button stays disabled with helper text "must be zero or positive".

---

### US-6 — Sandbox allowed-paths list editor (Priority: P0)

An admin can add, edit, and remove entries in `sandbox.allowed_paths` via a list editor (add row / delete row / inline-edit path).

**Why this priority**: without this, expanding the sandbox's allowed filesystem surface requires a JSON edit + restart — a support burden that blocks real use.

**Independent test**: add `/var/mycorp/data` to the list, save, restart the gateway, confirm agents can `read_file` from that path.

**Access model (EXPLICIT, CRIT-004 resolution)**: `AllowedPaths` entries grant **READ-ONLY** filesystem access to the sandbox. This matches the existing doc comment at `pkg/config/sandbox.go:47–49` ("lists additional filesystem paths the sandbox may read"). Sprint K MUST NOT change this semantic. Every row in the UI displays a "read-only" badge. There is no "grant write access to an arbitrary path" affordance. Write-capable paths (agent workspace, task scratch dirs) are sandbox policy, not settings — they do not appear in this editor. The Sprint J FR-J-013 "user-read wins, write stripped" rule concerns agent-workspace paths that happen to overlap `/etc`, not admin-added `AllowedPaths` entries; this story does NOT re-specify FR-J-013 behavior.

**Acceptance Scenarios**:
1. **Given** admin on Process Sandbox → Edit, **When** they add `/var/data/shared` and click Save, **Then** the persisted list includes the new entry; the row shows a "read-only" badge; the row is marked restart-required.
2. **Given** admin enters a relative path `./foo`, **When** they save, **Then** the API returns 400 "allowed_paths entries must be absolute — `./foo` is relative" at SAVE TIME (no deferred failure).
3. **Given** admin enters a path containing `..` (e.g., `/var/data/../etc`), **When** they save, **Then** the API returns 400 "allowed_paths entries must not contain `..` segments".
4. **Given** admin enters a path whose final component is a symlink (server `lstat` detects `S_IFLNK`), **When** they save, **Then** the API returns 400 "allowed_paths entries must not end in a symlink".
5. **Given** admin hovers the "read-only" badge on any row, **When** the tooltip appears, **Then** it reads "AllowedPaths entries grant read-only access. Write access is never available via this editor."
6. **Given** admin adds `/etc`, **When** they save, **Then** the API accepts the entry (it is read-only by the access model); no "write stripped" note is shown because no write access was ever implied.

---

### US-7 — SSRF allow_internal editor (Priority: P0)

**[REVISED — CRIT-001 resolution.]** The storage shape is unchanged from today's code. `OmnipusSSRFConfig.AllowInternal []string` (per `pkg/config/sandbox.go:90–101`) already accepts a heterogeneous list of hostnames, exact IPs, and CIDR ranges. This story ADDS a UI editor on top of that one existing field — no new config key, no bool, no migration.

The UI offers two presentations that both write to the same `allow_internal []string`:

- **Simple presets** (default for non-experts): three buttons that write an agreed preset list:
  - *Block all* → `[]` (empty)
  - *Allow loopback only* → `["127.0.0.1","::1"]`
  - *Allow RFC1918 + loopback* → `["127.0.0.1","::1","10.0.0.0/8","172.16.0.0/12","192.168.0.0/16","fc00::/7"]`
- **Advanced list editor** (expand-to-show): operator sees and edits the raw `[]string` — one row per entry. Entries may be hostname (`internal.corp`), exact IP (`10.0.0.5`), or CIDR (`10.0.0.0/8`). The existing `SSRFChecker` resolves all three shapes; the UI just surfaces the list.

On GET, the UI tries to match the current list against a preset; if no match, it opens Advanced mode by default showing the raw list.

**Why this priority**: operators legitimately need to let the sandbox reach internal services (e.g., internal search, a private LLM). Today they hand-edit JSON. Non-experts shouldn't need to learn CIDR syntax just to say "yes allow internal".

**Independent test**: (a) click "Allow RFC1918 + loopback", save — agent can reach `10.0.0.5`. (b) Advanced mode, set list to `["10.0.0.0/24"]`, save — agent can reach `10.0.0.5` but not `10.0.1.5`.

**Acceptance Scenarios**:
1. **Given** admin clicks "Allow RFC1918 + loopback" preset, **When** save succeeds, **Then** `allow_internal` on disk is exactly the preset list; the SSRFChecker allows all RFC1918 addresses within 2s (hot-reload).
2. **Given** admin adds `10.0.0.0/8` to the Advanced list, **When** they save, **Then** the SSRFChecker picks up the new entry within the 2s config poll; no restart needed.
3. **Given** admin types a malformed CIDR `10.0.0/8` in Advanced mode, **When** they save, **Then** the UI shows inline error "invalid entry — expected hostname, IP, or CIDR" and the server-side PUT returns 400 as a backstop (server validates each entry: must parse as CIDR via `net.ParseCIDR`, OR as IP via `net.ParseIP`, OR be a non-empty hostname matching `^[A-Za-z0-9][A-Za-z0-9.-]*$`).
4. **Given** the stored list matches the "Allow RFC1918 + loopback" preset exactly (same entries, order-insensitive), **When** the admin opens the section, **Then** that preset is highlighted as active.
5. **Given** the stored list does not match any preset (e.g., contains `internal.corp`), **When** the admin opens the section, **Then** Advanced mode opens by default showing the raw list.
6. **Given** admin includes `0.0.0.0/0` as an entry, **When** they save, **Then** the UI shows a confirmation modal "This would disable SSRF protection entirely — continue?" and the PUT proceeds only on confirm (server accepts the value but logs `slog.Warn` with event `ssrf_wildcard_accepted`).
7. **Given** admin includes `fe80::/10` (IPv6 link-local), **When** they save, **Then** it is accepted (the SSRFChecker already supports IPv6 CIDRs).

---

### US-8 — Session DM scope selector (Priority: P1)

**[REVISED — MAJ-002 resolution.]** An admin can change `session.dm_scope` via a radio group with **all four** values defined in `pkg/routing/session_key.go:12–15`: `main`, `per-peer`, `per-channel-peer` (default), `per-account-channel-peer`. The value `global` is NOT a valid scope and MUST NOT appear in the UI or FR-007 body.

**Why this priority**: affects DM routing; rarely changed after initial setup, but the only escape today is editing JSON. Lower priority because production default (`per-channel-peer`) is what most operators want.

**Independent test**: switch to `main`, send a Telegram DM from two different peers to the same agent — both land in the same session rather than separate ones.

**Acceptance Scenarios**:
1. **Given** admin picks `main`, **When** save succeeds, **Then** restart-required indicator fires (session routing is cached at boot); banner entry reads `session.dm_scope: per-channel-peer → main`.
2. **Given** the stored value is `per-channel-peer`, **When** the section opens, **Then** `per-channel-peer` radio is pre-selected and the other three are shown unselected with human-readable subtitles ("main: one session per agent across all DMs", "per-peer: separate per sender identity", "per-channel-peer: separate per (channel, sender)", "per-account-channel-peer: separate per (bot-account, channel, sender)").
3. **Given** the stored value is an unknown string (corrupted config), **When** the section opens, **Then** the UI shows a warning "unknown scope value — defaulting display to per-channel-peer" and none of the radios is pre-selected; saving any valid value overwrites the corrupt one.

---

### US-9 — Storage retention editor + backend sweep (Priority: P0)

**[REVISED — CRIT-002 resolution.]** The backend retention sweep does **not exist today** (confirmed by the skipped test at `pkg/session/orphan_cleanup_test.go:33`). This story ships BOTH the new `UnifiedStore.RetentionSweep(days int) (removed int, err error)` method AND the UI editor. Without this, the setting would be cosmetic.

**Existing semantics preserved** — `pkg/config/config.go:169–175` defines:

```go
func (r OmnipusRetentionConfig) RetentionSessionDays() int {
    if r.SessionDays <= 0 {
        return 90
    }
    return r.SessionDays
}
```

`session_days <= 0` today means **"use default 90"**. We MUST NOT flip this to "keep forever" — that would silently disable retention on every existing deployment with an unset or zero `session_days`. Instead we add a NEW sibling field `retention_disabled bool` (JSON: `storage.retention.disabled`) for the explicit "keep forever" intent:

| `disabled` | `session_days` | Effective behavior |
|---|---|---|
| false (default) | unset / 0 / ≤ 0 | 90 days (existing default) |
| false | ≥ 1 | N days (explicit value) |
| true | any | Retention disabled; sweep is a no-op |

The UI presents three modes that round-trip 1:1 to this shape:

- **"Default (90 days)"** → `{disabled: false, session_days: 0}` (or unset)
- **"Custom"** → `{disabled: false, session_days: N}` with N ≥ 1 (slider/input 1–365)
- **"Disabled (keep forever)"** → `{disabled: true, session_days: 0}` — requires explicit confirmation modal

The new `RetentionSessionDays()` receives an updated docstring AND an `IsDisabled()` helper is added; callers gate sweep on `!cfg.Storage.Retention.IsDisabled()`.

**Backend deliverables**:
- New field `OmnipusRetentionConfig.Disabled bool` with tag `json:"disabled,omitempty"`.
- New method `(OmnipusRetentionConfig).IsDisabled() bool` returning `r.Disabled`.
- New method `(*UnifiedStore).RetentionSweep(retentionDays int) (removed int, err error)` that walks `sessions/<id>/<YYYY-MM-DD>.jsonl`, deletes files older than `now - retentionDays`, and returns the count removed. When `disabled==true` OR `retentionDays<=0 && disabled==false` path is reached, the caller — not the sweep — short-circuits (see Nightly Goroutine).
- **Nightly goroutine** launched in gateway boot: `*time.Ticker(24h)` + a `context.Context` for graceful shutdown. On each tick: if `retention.IsDisabled()`, skip; else call `RetentionSweep(retention.RetentionSessionDays())` (which applies the 90-day default when `session_days<=0`). Wrap in `sync.Mutex` shared with the on-demand endpoint so at most one sweep runs at a time. Log `{"event":"retention_sweep","removed":N,"duration_ms":...}` on completion; `{"event":"retention_sweep_failed","error":...}` on error; the ticker continues on error (no panic, no goroutine exit).
- On gateway shutdown: cancel the context; the goroutine exits before `Close()` returns.
- Admin-only endpoint `POST /api/v1/security/retention/sweep` takes the mutex, runs the sweep, returns `{removed: N}` — or 409 `{error:"sweep in progress"}` if the mutex is held.
- Hot-reload: the nightly goroutine reads `Storage.Retention` fresh on each tick, so config changes take effect at the next midnight tick (acceptable because retention is a 24h-scale setting).
- **Upgrade path** (explicit, no migration): existing deployments with `session_days: 0` continue to mean "90 days" exactly as today. No silent re-interpretation. Only setting `disabled: true` changes behavior. Add a regression test `TestRetention_ZeroSessionDaysStillMeansDefault90` asserting `(OmnipusRetentionConfig{SessionDays:0}).RetentionSessionDays() == 90` survives this sprint.

**Why this priority** (raised to P0): disk-fill prevention. Without the sweep, the UI slider would silently lie about its behavior. Bugging this is worse than not shipping it.

**Independent test**: set Custom=7 via UI → create a session file backdated 10 days → POST `/retention/sweep` → confirm the old file is deleted and response says `{removed: 1}`. Also: `TestOrphanSessionsParticipateInRetention` (previously skipped) now passes.

**Acceptance Scenarios**:
1. **Given** admin picks "Custom" with N=30, **When** save succeeds, **Then** persisted config is `{disabled:false, session_days:30}`; the next nightly sweep uses 30; on-demand sweep endpoint also honors 30.
2. **Given** admin picks "Disabled (keep forever)", **When** they see the confirmation modal "This will let sessions accumulate indefinitely. Continue?" and confirm, **Then** persisted config is `{disabled:true, session_days:0}`; a persistent yellow warning banner appears in Settings: "Retention disabled — sessions will accumulate indefinitely." Nightly sweep becomes a no-op.
3. **Given** admin picks "Default (90 days)", **When** save succeeds, **Then** persisted config is `{disabled:false, session_days:0}` (or fields cleared) and `RetentionSessionDays()` returns 90.
4. **Given** retention Custom=7 and three session files aged 3/10/30 days, **When** admin clicks "Run sweep now", **Then** the 10-day and 30-day files are deleted; the 3-day file stays; response is `{removed: 2}`.
5. **Given** an orphan session (agent deleted), **When** the sweep runs, **Then** the orphan file is subject to the same retention cutoff as non-orphan files.
6. **Given** a pre-sprint deployment with `storage.retention.session_days: 0` and no `disabled` field, **When** the gateway starts under Sprint K code, **Then** the sweep uses 90 days (existing semantics); the UI renders "Default (90 days)" as the active mode. No silent flip.
7. **Given** an on-demand sweep is already running, **When** admin (or another process) POSTs `/retention/sweep`, **Then** the second request returns 409 `{error:"sweep in progress"}` without starting a second sweep.

---

### US-10 — Gateway user management (CRUD, admin-only) (Priority: P0)

**[REVISED — CRIT-003, MAJ-003, MAJ-005, MAJ-006, MAJ-007 resolution.]**

**Token-issuance model (the only one we ship)**: user creation does NOT return a token. Admin sets a password; the new user logs in via the existing `POST /api/v1/auth/login` to mint their bearer token. This matches today's behavior (login overwrites `TokenHash` on each successful auth per `rest_auth.go:332–337`) so we don't introduce two coexisting token lifecycles. No "one-time token modal" — that concept is removed from the story.

**Why this model** (over "create with token"): keeps the `TokenHash` field single-writer (login), preserves the existing bcrypt rotation per login, and avoids the trap where a user-creation-minted token gets overwritten the first time the user logs in.

**Capabilities**:
- List users (admin-only): `GET /api/v1/users` returns `[{username, role, created_at, has_password, has_active_token}]`. No hashes in response.
- Create user (admin-only): `POST /api/v1/users` with body `{username, role, password}`. Server validates: username must match `^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$` (no spaces, no `/`, no unicode to preserve path-component compatibility with session dirs); password ≥ 8 chars; role ∈ {`admin`,`user`}. Response: `{username, role}` — **no token**. Admin hands the password to the user through an out-of-band channel.
- Admin-reset-password (admin-only): `PUT /api/v1/users/{username}/password` body `{password}`. Sets `PasswordHash` via bcrypt AND zeroes `TokenHash` in the **same** `safeUpdateConfigJSON` transaction (MAJ-003). Response: `{username, password_reset: true}`. The target's prior bearer token returns 401 on the next request (middleware compares against empty `TokenHash` which no token matches).
- Delete user (admin-only): `DELETE /api/v1/users/{username}`. Last-admin guard evaluated INSIDE the `safeUpdateConfigJSON` callback against the just-read JSON (MAJ-005): count admins after proposed removal, return 409 `cannot leave the deployment with zero administrators` if count == 0.
- Change role (admin-only): `PATCH /api/v1/users/{username}/role` body `{role}`. Same last-admin guard inside the callback. Also prevents self-demotion when the actor is the only admin.
- Self-change-password: **REUSE the existing `POST /api/v1/auth/change-password`** (MAJ-007). Do NOT add `PUT /users/self/password`. The existing endpoint already requires current-password verification, operates on the authenticated user, and is wired to `ProfileSection`. No new endpoint for this capability.

**Dev-mode-bypass interaction (MAJ-006)**: when `gateway.dev_mode_bypass == true`, ALL six new endpoints (`GET/POST /users`, `PUT /users/:u/password`, `DELETE /users/:u`, `PATCH /users/:u/role`, and `POST /api/v1/security/retention/sweep`) return HTTP 503 `{error:"user management disabled in dev-mode-bypass"}` without touching config. The Access tab is hidden from the Settings nav in the same condition — the `/api/v1/config` response already reveals `dev_mode_bypass` to admins.

**blockedKeys interaction (MAJ-004)**: Sprint K replaces the flat `blockedKeys map[string]bool` with a new `blockedPaths []string` (dotted paths) plus a walker that refuses any PUT whose body contains a blocked path at ANY depth. Initial list: `sandbox`, `credentials`, `security`, `gateway.users`, `gateway.dev_mode_bypass`. The walker returns 403 with the specific path echoed. Regression test `TestConfigPUT_CannotSetGatewayUsers_Returns403` covers three shapes: whole-gateway `{"gateway":{"users":[...]}}`, just-users `{"gateway.users":[...]}` (dot-path literal), and nested-beside-others `{"gateway":{"port":5000,"users":[...]}}`.

**Username format**: server MUST reject `alice bob` (spaces), `alice/bob` (slash), `ALICE` is accepted and stored as-is (case-preserving; username compares are case-sensitive to match existing `HandleLogin`). Role case-variants: `ADMIN` returns 400 — server does NOT normalize role casing.

**Independent test**:
- POST `/api/v1/users` with `{username:'alice', role:'user', password:'correct-horse-battery'}` returns 201 `{username:'alice', role:'user'}`; subsequently POST `/auth/login` with those credentials returns a bearer token.
- PUT `/api/v1/users/alice/password` with `{password:'new-pwd'}` zeroes alice's `TokenHash`; an old bearer token sent to any `withAuth` endpoint returns 401.
- DELETE `/api/v1/users/{username}` removes a user; last-admin case returns 409; guard evaluated inside the write lock.
- Concurrent demotion test: two parallel goroutines PATCH each other's role from admin to user when there are only two admins → exactly one succeeds (200), the other receives 409.

**Acceptance Scenarios**:
1. **Given** admin on Settings → Access → Users, **When** they click "Add user", fill `{username:'alice', role:'user', password:'correct-horse-battery'}`, and Create, **Then** POST returns `{username:'alice', role:'user'}`; the UI shows "User created. They can now log in at the login screen with the password you set." No token is displayed.
2. **Given** admin is the only admin and clicks Delete on their own row, **When** the confirmation dialog appears, **Then** the dialog is replaced with "Cannot delete yourself while you are the only admin" and the delete button is hidden; if the admin bypasses the UI and calls DELETE directly, the server returns 409 because the guard evaluates inside the write lock.
3. **Given** admin clicks "Reset password" on another user's row and enters a new password, **When** save succeeds, **Then** the target user's stored `TokenHash` is empty-string (zeroed in the same transaction) and `PasswordHash` is the bcrypt of the new password; subsequent requests bearing the target's prior token return 401; the target can log in with the new password to mint a fresh token.
4. **Given** admin changes another user's role from `user` to `admin`, **When** save succeeds, **Then** the target user's next API call (after `a.awaitReload()`) carries admin privileges.
5. **Given** a non-admin user, **When** they load Settings, **Then** the Access tab is hidden.
6. **Given** an admin viewing their own row, **When** they open the per-row menu, **Then** they see "Change my password" (which opens `ProfileSection`-style form using `POST /api/v1/auth/change-password`) and NOT "Reset password" (which is admin-resetting-others).
7. **Given** admin submits `{username:'alice bob', role:'user', password:'...'}`, **When** the server validates, **Then** it returns 400 `{error:"username must match ^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$"}`.
8. **Given** two admins A and B exist (only admins), **When** A and B simultaneously PATCH each other's role to `user`, **Then** one transaction commits (the loser receives 409 `cannot leave the deployment with zero administrators`). Guard MUST run inside the `safeUpdateConfigJSON` callback, not before.
9. **Given** `gateway.dev_mode_bypass == true`, **When** anyone (including network-anonymous) calls any of the new user-management endpoints, **Then** the server returns 503 and the Access tab is hidden from the UI.

---

### US-11 — Per-field restart indicator + global banner (Priority: P0)

**[REVISED — MAJ-008 resolution.]** The pending-restart banner is a **DIFF** of persisted-config vs boot-time applied-config, not an accumulated change history. Saving X→Y→X (revert before restart) MUST result in an empty diff and an empty banner — no "queued revert" row. `GET /api/v1/config/pending-restart` computes the diff fresh on every call; the SPA does not store banner entries in local state, it re-fetches.

When an admin saves a setting that requires a gateway restart, a persistent banner appears at the top of Settings listing the queued changes. Each affected row also shows a local "Restart required" tag.

**Why this priority**: without this, admins don't know whether their change is live. Prevents a confusing "I saved it but it still shows the old value" experience.

**Independent test**: change `sandbox.mode` from off → enforce; banner appears showing "Sandbox mode: off → enforce (pending restart)". Change it back to off before restarting; banner clears without requiring restart.

**Acceptance Scenarios**:
1. **Given** no pending restart-required changes (persisted == applied for all restart-gated keys), **When** admin opens Settings, **Then** no banner is visible.
2. **Given** admin saves a restart-required setting changing persisted value from X to Y (while applied is still X), **When** the save succeeds, **Then** the global banner appears listing `{key, applied:X, persisted:Y}` and "restart the gateway to apply".
3. **Given** the banner lists `{sandbox.mode, applied:off, persisted:enforce}`, **When** admin saves `sandbox.mode=off` (reverting before restart), **Then** the server's next `/pending-restart` response returns an empty list; the SPA's banner poll clears the row without requiring restart.
4. **Given** pending changes exist and admin saves a hot-reloadable setting, **When** the save succeeds, **Then** the banner stays visible (unaffected) and the hot-reloaded setting does NOT appear in it (hot-reload keys are not part of the diff).
5. **Given** the gateway restarts (or the user reloads the page), **When** the banner re-mounts, **Then** it consults GET `/api/v1/config/pending-restart`; queued items whose persisted value now equals applied (because the restart applied them) are dropped.
6. **Given** a non-admin user, **When** they load Settings, **Then** the banner is hidden; `GET /api/v1/config/pending-restart` returns 403 for non-admins.

---

### US-12 — Landlock ABI v4 banner + boot-time log + save-time warning (Priority: P1)

**[REVISED — MAJ-001, MAJ-009 resolution.]** All UI copy, BDD scenarios, and log messages reference **issue #138** (the canonical tracker filed 2026-04-22). Any earlier reference to `#103` MUST be removed. The implementation MUST define a single source-of-truth constant (e.g., `pkg/sandbox.LandlockABI4IssueRef = "#138"`) and reference it from both backend log strings and the JSON surfaced to the UI; the UI pulls the ref from `/sandbox-status` rather than hard-coding.

**`abi_version` field semantics**: `pkg/sandbox/sandbox.go:523` defines `ABIVersion int json:"abi_version,omitempty"`. On non-Linux systems and failed probes the field is **absent** from the response JSON (not `null`). The UI MUST therefore treat `typeof response.abi_version !== 'number'` as "no probe available" and suppress the ABI v4 warnings. The boot-log rule fires only when `abi_version >= 4` AND the field is present.

**Three surfaces, one root cause** — kernel 6.8+ advertises Landlock ABI v4 which our current code can't negotiate (tracked in issue #138 for the actual upgrade). This story ensures no operator is blindsided by the exit-code-78 boot loop.

**Surface 1 — Boot-time log** (new). On gateway start, after Landlock probe, if `abi_version >= 4`:
- `slog.Warn("sandbox: landlock ABI v4 detected; enforce mode is known to fail on this kernel (issue #138). Use --sandbox=permissive or --sandbox=off until landlock support is upgraded.")`
- Logged once, not every request. Structured field: `{"event":"landlock_abi_warning","abi_version":4,"kernel":"6.8.0-...","issue":"#138"}`.

**Surface 2 — UI banner** (new). `SandboxSection.tsx` calls `GET /api/v1/security/sandbox-status`; if `abi_version >= 4`, renders a persistent yellow banner at the top of the section:
- Text: *"Your Linux kernel uses Landlock v4, which is not yet supported (issue #138). Enforce mode will exit with code 78 at boot. Use 'permissive' or 'off' until Landlock support is upgraded."*
- Dismiss-for-session button (localStorage, not server-persisted — banner must reappear on next session so operators don't forget).

**Surface 3 — Save-time confirmation modal** (from the original story). When the admin saves `sandbox.mode = enforce` on an ABI-v4 kernel, a modal fires: *"Your kernel reports Landlock ABI v4 (issue #138). Enforce mode will cause the gateway to exit with code 78 at next boot. Save anyway?"* with "Save anyway" / "Cancel" buttons.

**Why this priority**: prevents an operator on kernel 6.8 from saving "enforce", restarting, and finding the gateway in a boot-loop (exit 78). Three surfaces because operators enter the system at different points: via logs (ops), via Settings (admins opening the UI), via the save action (admins actively changing sandbox).

**Independent test**: (a) start gateway on kernel 6.8 — `gateway.log` contains the warning line with `"issue":"#138"`. (b) open Settings → Sandbox — yellow banner visible. (c) save mode=enforce — confirmation modal fires.

**Acceptance Scenarios**:
1. **Given** gateway starts with ABI v4 detected, **When** boot completes, **Then** `slog.Warn` emits the warning line with issue ref and kernel version; subsequent requests do NOT re-log.
2. **Given** ABI v4 detected, **When** admin opens Sandbox section, **Then** yellow banner shows the unsupported-version message; clicking "Dismiss for session" hides it until next browser session.
3. **Given** ABI v4 detected, **When** admin saves mode=enforce, **Then** confirmation modal appears with the issue #138 link; clicking "Save anyway" proceeds; clicking "Cancel" aborts without a PUT.
4. **Given** a kernel without the ABI v4 gap (ABI v1/v2/v3), **When** admin saves mode=enforce, **Then** the save goes straight through; no banner, no modal, no boot warning.

---

## Behavioral Contract

Primary flows:
- When a healthy deployment renders the Diagnostics card, the system shows a score ≥ 90 labeled "Excellent" with a green indicator.
- When an admin edits any security setting in Settings, the system persists the change via a dedicated endpoint and indicates whether the change is live or needs a restart.
- When an admin creates a new user, the system returns the bearer token exactly once and persists the hash only.
- When an admin saves a restart-required setting, the system surfaces a global banner until the next restart applies the change.

Error flows:
- When a non-admin attempts to edit any security setting, the server returns 403 and the UI control is disabled with a "admin only" tooltip.
- When the server rejects a malformed value (e.g. invalid CIDR, negative rate limit), the UI shows the error inline next to the offending field and keeps the save button disabled.
- When the admin attempts to delete the last admin user, the server returns 409 and the UI surfaces the reason.

Boundary conditions:
- When the doctor backend returns `score: 0`, the UI displays "0 / 100 — Critical" without dividing-by-zero in gauge math.
- When `sandbox.allowed_paths` is empty, the UI shows an empty-state row and allows the first add.
- When `gateway.users` would contain zero admins, the server rejects the deletion/role-change with 409.
- When `storage.retention.session_days` is set to 0, the UI displays the irreversibility warning and persists the value.

---

## Edge Cases

- **Concurrent admin edits on unrelated settings**: two admins modify different settings in different tabs → both commit (atomic rename); no conflict because `safeUpdateConfigJSON` serialises at the file level.
- **Concurrent admin edits on the same setting**: last-write-wins at the file level. Lost-update is accepted because Sprint K targets a small admin set and rare changes.
- **Concurrent last-admin demotion**: two admins simultaneously demote each other → exactly one commits; the other receives 409 from the guard evaluated INSIDE the `safeUpdateConfigJSON` callback. See US-10 AC-8.
- **Restart banner after restart**: gateway restarts, SPA reloads, banner queries the pending-restart endpoint → server returns empty (persisted == applied) → banner auto-dismisses. Operators don't see a stale "restart required" after restarting.
- **Restart banner after revert-before-restart**: admin saves X→Y then Y→X before restarting → next `/pending-restart` call returns empty; banner clears. The endpoint is a diff, not a history (US-11 AC-3).
- **Deleting oneself**: admin can delete themselves only if another admin exists. Protected by the last-admin guard inside the write lock.
- **Admin-triggered password reset on a signed-in user**: admin resets alice's password → alice's `TokenHash` is zeroed in the same transaction → alice's next request bearing the prior token receives 401 from the `withAuth` middleware (it compares against empty TokenHash). Alice must log in with the new password to mint a fresh token.
- **Self-change-password**: uses existing `POST /api/v1/auth/change-password`. No new endpoint. Leaves the user's current token intact (changing password without rotating token is an explicit feature of that endpoint today).
- **Dev-mode-bypass and new endpoints**: when `gateway.dev_mode_bypass==true`, all six new user-management endpoints and `POST /retention/sweep` return 503. The UI's Access tab is hidden. This prevents anonymous elevation on dev deployments.
- **ABI v4 warning kernel-less host**: on macOS / non-Linux, `/sandbox-status` omits `abi_version` (int omitempty → absent, not null). UI suppresses all three ABI v4 surfaces. Boot-log rule also skips.
- **Rate limits with 0**: explicitly treated as "no limit" (matches today's backend semantics).
- **Allowed-paths are read-only**: user adds `/proc/self` — server accepts but the sandbox only grants READ access. There is no "write stripped" scenario because no write is ever granted via this editor.
- **SSRF allow_internal**: the single field `sandbox.ssrf.allow_internal []string` accepts hostnames, exact IPs, and CIDR ranges interchangeably (existing SSRFChecker behavior). UI presents presets + advanced list that both write this one field.
- **Retention `session_days == 0`**: continues to mean "use default 90 days" exactly as today. "Keep forever" is expressed by the NEW `retention_disabled: true` field.

---

## Explicit Non-Behaviors

- The system must NOT expose `gateway.dev_mode_bypass` as a UI toggle, because flipping it removes all authentication.
- The system must NOT expose channel bot tokens or other raw credentials in the UI, because they are stored encrypted in `credentials.json` and should only be rotated via the credentials CLI.
- The system must NOT add a "restart gateway" button to the UI in this sprint, because a robust implementation requires process-supervisor coordination that is deployment-specific.
- The system must NOT auto-apply restart-required changes by forcing a restart, because operators may batch changes and restart deliberately during a maintenance window.
- The system must NOT rename the backend doctor `score` field, because it is part of the published API contract and renaming would break external monitoring.
- The system must NOT invert the doctor `score` semantics again — it is and has always been a security-goodness score (higher = better).
- The system must NOT silently hot-reload sandbox.mode changes, because FR-J-015 explicitly locks that out and the operator needs unambiguous "restart required" signaling.
- The system must NOT allow a non-admin user to view or edit any security setting, even read-only, unless that setting is generally public (e.g. doctor score).

---

## Integration Boundaries

### config.json on disk

- **Data in**: admin edits via UI → server merges into the JSON map.
- **Data out**: server loads on boot and every hot-reload cycle.
- **Contract**: JSON schema per `pkg/config/config.go`; atomic rename (temp + rename) write pattern.
- **On failure**: write error → 500 + the UI save button reports failure; no partial update.
- **Development**: real file via `safeUpdateConfigJSON`.

### credentials.json on disk

- Not touched directly in this sprint — user management rotates bearer tokens which live in `config.json.gateway.users[].token_hash` (bcrypt), not in the encrypted credentials store.

### `state.json` (onboarding + doctor)

- **Data in**: doctor run appends `last_doctor_score`.
- **Data out**: UI reads on settings open.
- **Contract**: JSON per `pkg/onboarding/onboarding.go`.
- **On failure**: read error → UI shows "no diagnostics run yet" placeholder (same as today).

### Landlock kernel ABI probe

- **Data in**: kernel version + `/proc/self/status` seccomp mode.
- **Data out**: `/api/v1/security/sandbox-status` surfaces `abi_version int json:"abi_version,omitempty"` (see `pkg/sandbox/sandbox.go:523`).
- **Contract**: read-only probe; no-op on non-Linux.
- **On failure or non-Linux**: the `abi_version` field is **absent** from the JSON response (int omitempty + zero value = omitted). The UI MUST check `typeof response.abi_version === 'number' && response.abi_version >= 4` to fire the warning. The boot-log warning MUST gate on the same condition (field present AND ≥ 4). A missing field does NOT trigger warnings. An added BDD scenario asserts: *Given `/sandbox-status` response does not include `abi_version`, When SandboxSection renders, Then the ABI v4 banner is not visible.*

---

## BDD Scenarios

### Feature: Diagnostics score display

#### Scenario: Healthy deployment shows Excellent
**Traces to**: US-1 AC-1
**Category**: Happy Path
- **Given** the backend doctor state holds `score: 90`
- **When** the Diagnostics section renders
- **Then** the score "90" is visible
- **And** the label "Excellent" is visible
- **And** the indicator color is success (green)
- **And** the progress bar fill-width is 90%

#### Scenario Outline: Security label by score bucket
**Traces to**: US-1 AC-1..3
**Category**: Happy Path
- **Given** the backend doctor state holds `score: <score>`
- **When** the Diagnostics section renders
- **Then** the label "<label>" is visible
- **And** the color variable resolves to `<color>`

**Examples**:

| score | label | color |
|---|---|---|
| 100 | Excellent | var(--color-success) |
| 90  | Excellent | var(--color-success) |
| 67  | Good      | var(--color-success) |
| 66  | At risk   | var(--color-warning) |
| 34  | At risk   | var(--color-warning) |
| 33  | Critical  | var(--color-error) |
| 0   | Critical  | var(--color-error) |

#### Scenario: Toast reports security score after run
**Traces to**: US-1 AC-4
**Category**: Happy Path
- **Given** the admin clicks "Run diagnostics"
- **And** the doctor returns `score: 85`
- **When** the mutation resolves
- **Then** a toast fires with text matching `Diagnostics complete — security score: 85/100`
- **And** the toast variant is "success"

---

### Feature: Audit log toggle

#### Scenario: Admin enables audit log
**Traces to**: US-2 AC-1
**Category**: Happy Path
- **Given** admin is logged in
- **And** `sandbox.audit_log` is currently `false`
- **When** admin toggles the audit-log switch to ON and clicks Save
- **Then** the UI fires `PUT /api/v1/security/audit-log` with `{enabled: true}`
- **And** the response carries `requires_restart: true`
- **And** the row shows a "Restart required" tag
- **And** the global restart banner now lists this change

#### Scenario: Non-admin sees read-only
**Traces to**: US-2 AC-2
**Category**: Error Path
- **Given** a user-role session
- **When** they navigate to Settings → Security → Audit Log
- **Then** the toggle is visible but disabled
- **And** a tooltip reads "Admin only"

---

### Feature: Skill trust level

#### Scenario Outline: Set skill trust
**Traces to**: US-3 AC-1..2
**Category**: Happy Path (first two rows), Error Path (last row)
- **Given** admin on Settings → Security → Skills
- **When** they pick "<level>" and click Save
- **Then** the server responds with status `<status>`
- **And** the UI shows message matching "<message>"

**Examples**:

| level | status | message |
|---|---|---|
| block_unverified | 200 | saved |
| warn_unverified  | 200 | saved |
| allow_all        | 200 | saved.*dangerous |
| ridiculous       | 400 | invalid.*must be one of |

---

### Feature: Prompt injection level

#### Scenario: Hot-reload on save
**Traces to**: US-4 AC-1
**Category**: Happy Path
- **Given** admin picks "high" and clicks Save
- **When** the save succeeds
- **Then** the card shows "Applied: high"
- **And** no "Restart required" tag appears (because this setting hot-reloads)
- **And** within 2 seconds a subsequent web_fetch result reflects high-strictness sanitization

#### Scenario: Invalid value rejected
**Traces to**: US-4 AC-2
**Category**: Error Path
- **Given** an API caller POSTs `{level: "paranoid"}`
- **When** the handler validates
- **Then** the response is 400
- **And** the error message contains "must be one of: low, medium, high"

---

### Feature: Rate limits editor

#### Scenario: Set daily cost cap
**Traces to**: US-5 AC-1
**Category**: Happy Path
- **Given** admin on Settings → Security → Rate Limits
- **When** they enter `daily_cost_cap_usd: 25.5` and click Save
- **Then** the PUT succeeds with `{saved: true, requires_restart: false}`
- **And** the agent loop cost tracker uses the new cap on the next check

#### Scenario: Negative value blocked client-side
**Traces to**: US-5 AC-2
**Category**: Edge Case
- **Given** admin types `-5` in the daily-cost-cap field
- **When** the onChange fires
- **Then** the Save button is disabled
- **And** helper text reads "must be zero or positive"

---

### Feature: Sandbox allowed paths

#### Scenario: Add an absolute path
**Traces to**: US-6 AC-1
**Category**: Happy Path
- **Given** admin on Process Sandbox → Edit
- **When** they type `/var/data/shared` and click "Add path"
- **Then** the row appears in the list
- **When** they click Save
- **Then** the PUT succeeds
- **And** the row displays a "restart required" badge (sandbox config is restart-gated per FR-J-015)

#### Scenario: Relative path rejected
**Traces to**: US-6 AC-2
**Category**: Error Path
- **Given** admin types `./foo` and clicks Save
- **When** the request reaches the server
- **Then** the response is 400
- **And** the error message contains "absolute or ~/-prefixed"

#### Scenario: Read-only badge is always visible
**Traces to**: US-6 AC-1, US-6 AC-5 (CRIT-004 resolution)
**Category**: Happy Path
- **Given** admin adds `/var/data/shared`
- **When** the row renders
- **Then** a "read-only" badge is visible on the row
- **And** the tooltip reads "AllowedPaths entries grant read-only access. Write access is never available via this editor."
- **And** no "write stripped" note is shown for any entry regardless of path

#### Scenario: Dot-dot segments rejected at save time
**Traces to**: US-6 AC-3
**Category**: Error Path
- **Given** admin types `/var/data/../etc`
- **When** they save
- **Then** a 400 is returned
- **And** the error message contains "must not contain `..` segments"

#### Scenario: Symlink final component rejected
**Traces to**: US-6 AC-4
**Category**: Error Path
- **Given** `/tmp/slink` is a symlink (`lstat` returns S_IFLNK)
- **Given** admin submits `/tmp/slink`
- **When** the PUT reaches the server
- **Then** a 400 is returned
- **And** the error message contains "must not end in a symlink"

---

### Feature: SSRF allow_internal

#### Scenario: Add a CIDR entry hot-reloads
**Traces to**: US-7 AC-2
**Category**: Happy Path
- **Given** admin adds `10.0.0.0/8` to the advanced list
- **When** they click Save
- **Then** the PUT succeeds with `requires_restart: false`
- **And** within 2 seconds an agent tool attempting `http://10.0.0.5/` succeeds (no SSRF block)

#### Scenario: Preset round-trip
**Traces to**: US-7 AC-1, US-7 AC-4 (CRIT-001 resolution)
**Category**: Happy Path
- **Given** admin clicks "Allow RFC1918 + loopback" preset
- **When** they click Save
- **Then** `allow_internal` on disk is exactly `["127.0.0.1","::1","10.0.0.0/8","172.16.0.0/12","192.168.0.0/16","fc00::/7"]`
- **When** admin reopens the section
- **Then** the preset button is highlighted as active

#### Scenario: Malformed entry rejected
**Traces to**: US-7 AC-3
**Category**: Error Path
- **Given** admin types `10.0.0/8` (malformed CIDR)
- **When** they attempt to save
- **Then** a 400 is returned
- **And** the UI shows "invalid entry — expected hostname, IP, or CIDR"

#### Scenario: Wildcard confirms
**Traces to**: US-7 AC-6
**Category**: Edge Case
- **Given** admin adds `0.0.0.0/0` to the list
- **When** they click Save
- **Then** a confirmation modal appears: "This would disable SSRF protection entirely — continue?"
- **When** admin confirms
- **Then** the PUT succeeds
- **And** a `slog.Warn` is emitted with `event=ssrf_wildcard_accepted`

---

### Feature: Session DM scope

#### Scenario: Change scope to `main` (restart required)
**Traces to**: US-8 AC-1 (MAJ-002 resolution)
**Category**: Happy Path
- **Given** admin picks the `main` radio (one of the four canonical scopes)
- **When** they click Save
- **Then** the PUT succeeds with `requires_restart: true`
- **And** the banner reflects the queued change `session.dm_scope: per-channel-peer → main`

#### Scenario: All four scopes accepted by the server
**Traces to**: US-8 AC-1, FR-007
**Category**: Happy Path (outline)
- **Given** the admin POSTs `{dm_scope: "<scope>"}` where `<scope>` ∈ {`main`, `per-peer`, `per-channel-peer`, `per-account-channel-peer`}
- **When** the server processes the request
- **Then** the PUT succeeds for each value

#### Scenario: Legacy `global` value rejected
**Traces to**: US-8 FR-007 (MAJ-002)
**Category**: Error Path
- **Given** admin (or a legacy client) POSTs `{dm_scope: "global"}`
- **When** the server validates
- **Then** a 400 is returned
- **And** the error message lists the four canonical values

---

### Feature: Storage retention (CRIT-002 resolution)

#### Scenario: Disabled (keep forever) requires confirmation
**Traces to**: US-9 AC-2
**Category**: Edge Case
- **Given** admin picks "Disabled (keep forever)" mode
- **When** they click Save
- **Then** a confirmation modal fires: "This will let sessions accumulate indefinitely. Continue?"
- **When** admin clicks Continue
- **Then** the PUT persists `{disabled: true, session_days: 0}`
- **And** a yellow warning banner appears in Settings: "Retention disabled — sessions will accumulate indefinitely."
- **And** the nightly sweep goroutine skips its next tick (no files removed)

#### Scenario: Pre-sprint `session_days: 0` still means 90 days
**Traces to**: US-9 AC-6, FR-008 regression
**Category**: Happy Path (regression)
- **Given** an upgraded deployment where `storage.retention` persists `{session_days: 0}` from before Sprint K and no `disabled` field is present
- **When** the gateway boots on Sprint K code
- **Then** `RetentionSessionDays()` returns 90
- **And** the nightly sweep uses 90 days
- **And** the UI renders "Default (90 days)" as the active mode (no silent flip)

#### Scenario: On-demand sweep deletes aged files
**Traces to**: US-9 AC-4
**Category**: Happy Path
- **Given** retention mode is Custom with N=7
- **Given** three session files aged 3, 10, and 30 days exist
- **When** admin POSTs `/api/v1/security/retention/sweep`
- **Then** the response is `{removed: 2}`
- **And** the 10-day and 30-day files are deleted
- **And** the 3-day file remains

#### Scenario: Concurrent sweep returns 409
**Traces to**: US-9 AC-7
**Category**: Error Path
- **Given** the nightly sweep goroutine is mid-run (holds the sweep mutex)
- **When** admin POSTs `/api/v1/security/retention/sweep`
- **Then** the response is 409 `{error: "sweep in progress"}` without starting a second sweep

---

### Feature: User management (CRIT-003 + MAJ-003 + MAJ-005 + MAJ-007 resolution)

#### Scenario: Create returns user+role only — no token
**Traces to**: US-10 AC-1 (CRIT-003)
**Category**: Happy Path
- **Given** admin on Settings → Access → Users
- **When** they click "Add user" and submit `{username: 'alice', role: 'user', password: 'correct-horse-battery'}`
- **Then** the POST `/api/v1/users` returns 201 with `{username: 'alice', role: 'user'}` — NO token field
- **And** the UI shows "User created. They can now log in with the password you set."
- **When** alice POSTs `/api/v1/auth/login` with `{username: 'alice', password: 'correct-horse-battery'}`
- **Then** the response is 200 with a bearer token matching `^omnipus_[0-9a-f]{64}$` (existing format per `generateUserToken`)

#### Scenario: Admin reset-password zeroes TokenHash in same transaction
**Traces to**: US-10 AC-3 (MAJ-003)
**Category**: Happy Path
- **Given** alice has an active bearer token (logged in previously)
- **When** admin PUTs `/api/v1/users/alice/password` with `{password: 'new-pwd'}`
- **Then** the on-disk `config.json.gateway.users[alice].token_hash` is empty string
- **And** the on-disk `config.json.gateway.users[alice].password_hash` is the bcrypt of `new-pwd`
- **When** alice's pre-reset token is sent to ANY withAuth-gated endpoint
- **Then** the response is 401 (the middleware finds no matching TokenHash)
- **When** alice logs in with `new-pwd`
- **Then** she receives a fresh bearer token

#### Scenario: Last-admin guard evaluates inside write-lock callback
**Traces to**: US-10 AC-2, US-10 AC-8 (MAJ-005)
**Category**: Error Path
- **Given** admin is the only account with role=admin
- **When** they call DELETE `/api/v1/users/{self}` (bypassing the UI confirmation)
- **Then** `safeUpdateConfigJSON` reads the current users array
- **And** the proposed post-delete admin count is zero
- **And** the callback returns 409 `{error: "cannot leave the deployment with zero administrators"}`
- **And** the config on disk is unchanged

#### Scenario: Concurrent admin demotion — one wins, one 409
**Traces to**: US-10 AC-8 (MAJ-005)
**Category**: Edge Case (race)
- **Given** A and B are the only admins
- **When** A and B simultaneously PATCH each other's role from admin to user (two goroutines)
- **Then** exactly one PATCH returns 200
- **And** the other PATCH returns 409 `{error: "cannot leave the deployment with zero administrators"}`
- **And** the deployment still has at least one admin

#### Scenario: Self-change-password uses EXISTING endpoint (no new one)
**Traces to**: US-10 AC-6 (MAJ-007)
**Category**: Happy Path
- **Given** alice is logged in
- **When** she opens her own row's "Change my password" affordance
- **Then** the form posts to `POST /api/v1/auth/change-password` (the existing endpoint per `rest_auth.go:561`)
- **And** NO request is made to any `/users/self/password` URL (the new spec introduces no such endpoint)

#### Scenario: Dev-mode-bypass blocks elevation
**Traces to**: US-10 AC-9 (MAJ-006, FR-019)
**Category**: Error Path
- **Given** `gateway.dev_mode_bypass == true`
- **When** ANY caller (including unauthenticated) issues POST `/api/v1/users`, DELETE `/api/v1/users/...`, PUT `/api/v1/users/.../password`, PATCH `/api/v1/users/.../role`, or POST `/api/v1/security/retention/sweep`
- **Then** the response is 503 `{error: "user management disabled in dev-mode-bypass"}`
- **And** the SPA's Access tab is hidden (based on `/api/v1/config` reply containing `dev_mode_bypass: true`)

#### Scenario: blockedPaths rejects nested gateway.users
**Traces to**: US-10 FR-018 (MAJ-004)
**Category**: Error Path (security regression guard)
- **Given** admin is authenticated
- **When** admin PUTs `/api/v1/config` with body `{"gateway":{"users":[{...fabricated admin...}]}}`
- **Then** the response is 403 `{error: "gateway.users is a blocked path — use /api/v1/users"}`
- **And** the config on disk is unchanged
- **When** the same body is sent as `{"gateway.users":[...]}` (dot-path key literal)
- **Then** the response is 403
- **When** the same body is sent mixed with benign keys `{"gateway":{"port":5000,"users":[...]}}`
- **Then** the response is 403 and NO fields are persisted (atomic reject)

#### Scenario: Non-admin hides the Access tab
**Traces to**: US-10 AC-5
**Category**: Alternate Path
- **Given** a user-role session opens Settings
- **When** the tabs render
- **Then** the "Access" tab is not present in the DOM
- **And** navigating directly to `/settings/access` returns the user to `/settings` with a 403 toast

---

### Feature: Per-field restart indicator + global banner (MAJ-008 resolution)

#### Scenario: Banner appears after restart-required save
**Traces to**: US-11 AC-2
**Category**: Happy Path
- **Given** no banner is currently visible
- **When** admin saves `sandbox.mode = enforce` (restart-required)
- **Then** the top of Settings renders a banner listing `{sandbox.mode, applied: "off", persisted: "enforce"}`

#### Scenario: Set-then-revert clears banner without restart (MAJ-008)
**Traces to**: US-11 AC-3
**Category**: Edge Case
- **Given** the banner lists `{sandbox.mode, applied: "off", persisted: "enforce"}`
- **When** admin saves `sandbox.mode = off` (reverting before restart)
- **Then** the server's next `GET /api/v1/config/pending-restart` returns `[]`
- **And** the SPA's banner clears on its next poll
- **And** the gateway is NOT restarted

#### Scenario: Hot-reload save does not add to banner
**Traces to**: US-11 AC-4
**Category**: Alternate Path
- **Given** the banner already lists one restart-required change
- **When** admin saves `prompt_injection_level: medium → high` (hot-reload)
- **Then** the banner does NOT add a row for the hot-reloaded change

#### Scenario: Banner auto-dismisses after restart
**Traces to**: US-11 AC-5
**Category**: Happy Path
- **Given** the banner lists one queued change
- **And** the gateway restarts so applied == persisted
- **When** the SPA reloads and queries GET `/api/v1/config/pending-restart`
- **Then** the response is `[]`
- **And** the banner is not rendered

#### Scenario: pending-restart is admin-only
**Traces to**: US-11 AC-6
**Category**: Error Path
- **Given** a non-admin session
- **When** the SPA calls GET `/api/v1/config/pending-restart`
- **Then** the response is 403
- **And** the banner is hidden

---

### Feature: Landlock ABI v4 warning

#### Scenario: Kernel 6.8 enforce save gets warning
**Traces to**: US-12 AC-1
**Category**: Edge Case
- **Given** `/api/v1/security/sandbox-status` reports `kernel_version: "6.8.0"` and `abi_version: 4`
- **When** admin picks mode=enforce and clicks Save
- **Then** a confirmation modal fires with text matching "known incompatibility.*issue #138"
- **And** the modal offers "Save anyway" and "Cancel"

#### Scenario: Compatible kernel skips warning
**Traces to**: US-12 AC-3
**Category**: Happy Path
- **Given** `abi_version: 3` (no known gap)
- **When** admin picks mode=enforce and clicks Save
- **Then** the save fires directly with no extra confirmation modal

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | New handler functions + UI components in isolation | Validate schema handling, partial updates, render branches |
| Integration | HTTP round-trip: UI → middleware → handler → config.json | Validate CSRF, auth, rate-limit, persistence |
| E2E | Real Playwright click-through over plain HTTP + HTTPS | Validate the operator flow end-to-end incl. banner + restart-required UX |

### Test Implementation Order (write BEFORE the code)

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `TestDiagnostics_ScoreDisplay_HigherIsBetter` | Unit | Healthy deployment shows Excellent | Render section with `last_doctor_score=90`, assert `Security Score: 90 / 100 — Excellent` |
| 2 | `TestDiagnostics_ScoreLabel_ByBucket` | Unit | Security label by score bucket (table) | Parameterized across the 7 rows in the Examples table |
| 3 | `TestDiagnostics_ToastAfterRun` | Unit | Toast reports security score after run | Vitest — triggers the run mutation, asserts toast copy |
| 4 | `TestHandleSandboxAuditLog_PUTPersists` | Unit | Admin enables audit log | POST `/security/audit-log` with `{enabled:true}`, readback |
| 5 | `TestHandleSandboxAuditLog_NonAdmin403` | Unit | Non-admin sees read-only | Auth mock returns user role, expect 403 |
| 6 | `TestHandleSkillTrust_ValidLevels` | Unit | Set skill trust (table) | Reuse the Examples table |
| 7 | `TestHandlePromptInjection_HotReload` | Unit | Hot-reload on save | Persist "high", assert `requires_restart: false` |
| 8 | `TestHandleRateLimits_PersistsAllThreeFields` | Unit | Set daily cost cap | Verify partial-update semantics |
| 9 | `TestHandleSandboxConfig_AllowedPaths_AddAbsolute` | Unit | Add an absolute path | Already have `HandleSandboxConfig` — extend test file |
| 10 | `TestHandleSandboxConfig_AllowedPaths_RejectsRelative` | Unit | Relative path rejected | 400 + error message |
| 11 | `TestHandleSandboxConfig_SSRFAllowInternal_AddCIDR` | Unit | Add a CIDR entry hot-reloads | Match PUT semantics |
| 12 | `TestHandleSandboxConfig_SSRFAllowInternal_MalformedCIDR` | Unit | Malformed CIDR | 400 with actionable error |
| 13 | `TestHandleSessionDMScope_RestartRequired` | Unit | Change scope | `requires_restart: true` |
| 13a | `TestHandleSessionDMScope_AllFourValuesAccepted` | Unit | All four scopes accepted | Parametrize main, per-peer, per-channel-peer, per-account-channel-peer |
| 13b | `TestHandleSessionDMScope_GlobalRejected` | Unit | Legacy `global` rejected | Expect 400 with canonical list |
| 14 | `TestRetention_ZeroSessionDaysStillMeansDefault90` | Unit | CRIT-002 regression | Assert `OmnipusRetentionConfig{SessionDays:0}.RetentionSessionDays() == 90` |
| 14a | `TestRetention_DisabledFlagMeansKeepForever` | Unit | New `Disabled` flag skips sweep | IsDisabled true → sweep goroutine skips |
| 14b | `TestHandleStorageRetention_PUT_ValidShape` | Unit | Accept `{session_days, disabled}` | Partial updates |
| 14c | `TestRetentionSweep_DeletesAgedFiles` | Unit | On-demand sweep removes aged | Backdate files; assert removed count |
| 14d | `TestRetentionSweep_ConcurrentReturns409` | Integration | Second sweep while one running | Mutex held; expect 409 |
| 14e | `TestRetentionSweep_NightlyGoroutineTicks` | Integration | Ticker fires on 24h boundary (simulated) | Inject fake clock; verify sweep fires |
| 14f | `TestRetentionSweep_GracefulShutdown` | Integration | Context cancel exits goroutine | Start gateway, shutdown, verify goroutine returns |
| 14g | `TestOrphanSessionsParticipateInRetention` | Unit (un-skipped) | Existing skipped test passes | Remove `t.Skip`; add real assertions |
| 15 | `TestHandleUserCreate_ReturnsUserAndRoleOnly` | Unit | CRIT-003 resolution — POST returns no token | POST returns `{username, role}` only |
| 15a | `TestHandleUserCreate_ThenLoginWithPassword_IssuesToken` | Integration | Create + login flow | POST `/users`, then `/auth/login`, expect `omnipus_<64hex>` |
| 15b | `TestHandleUserCreate_RejectsInvalidUsername` | Unit | Username format | Spaces / slash / empty → 400 |
| 15c | `TestHandleUserCreate_RejectsUppercaseRole` | Unit | Role case-sensitive | `ADMIN` → 400 |
| 16 | `TestHandleUserDelete_LastAdmin409` | Unit | Cannot delete last admin | Precondition: 1 admin; expect 409 |
| 16a | `TestHandleUserDelete_LastAdminGuardInsideWriteLock` | Integration | Guard inside `safeUpdateConfigJSON` callback | Mock callback invocation; assert guard runs against just-read JSON |
| 17 | `TestHandleUserResetPassword_ZeroesTokenHash` | Unit | MAJ-003 — TokenHash empty after reset | Inspect config on disk |
| 17a | `TestHandleUserResetPassword_PriorTokenReturns401` | Integration | Pre-reset token → 401 via middleware | Hit `withAuth` endpoint with old token |
| 18 | `TestHandleUserChangeRole_AdminToUser` | Unit | Change role | Cascade: subsequent admin-only endpoints succeed |
| 18a | `TestHandleUserChangeRole_ConcurrentDemotion_OneWins` | Integration | MAJ-005 race | Two goroutines PATCH simultaneously; exactly one 200, one 409 |
| 18b | `TestHandleSelfChangePassword_UsesExistingEndpoint` | Integration | MAJ-007 — no new endpoint | Assert `PUT /users/self/password` returns 404; `POST /auth/change-password` still works |
| 19 | `TestHandlePendingRestart_EmptyAfterApply` | Unit | Banner auto-dismisses after restart | Simulate applied == persisted; response `[]` |
| 19a | `TestHandlePendingRestart_SetThenRevertClearsDiff` | Unit | MAJ-008 | Save X→Y, then Y→X; response `[]` without restart |
| 19b | `TestHandlePendingRestart_NonAdmin403` | Unit | Admin-only | Non-admin session → 403 |
| 20 | `TestHandlePendingRestart_ListsQueuedChanges` | Unit | Banner appears after restart-required save | applied ≠ persisted → response carries diff list |
| 21 | `TestCSRF_AllNewEndpointsSubjectToGate` | Integration | All new endpoints enforce CSRF cookie-or-bearer | Table-driven across: audit-log, skill-trust, prompt-guard, rate-limits, sandbox-config, session-dm-scope, retention, retention/sweep, users, users/password, config/pending-restart |
| 22 | `TestAdminOnly_AllNewPUTs` | Integration | Non-admin 403 on every new PUT | Same table as #21 |
| 23 | `TestRestartBanner_ShowsAfterSandboxModeChange` | Integration | Banner appears after restart-required save | Full SPA round-trip via DOM assertion |
| 24 | `TestRestartBanner_HiddenAfterApply` | Integration | Banner auto-dismisses after restart | Simulate restart; banner gone |
| 25 | `TestABIv4Warning_Kernel68EnforcePrompt` | Integration | Kernel 6.8 enforce save gets warning | Mock `/sandbox-status` abi_version=4; expect confirmation modal |
| 25a | `TestLandlockABIv4_BootLogOnce` | Unit | FR-014(a) boot-time log fires once | Capture slog output; assert `event=landlock_abi_warning` exactly once |
| 25b | `TestSandboxSection_ABIv4Banner` | Unit (Vitest) | FR-014(b) banner renders when abi_version>=4 | Mock fetch; assert yellow banner visible |
| 25c | `TestSandboxSection_ABIv3NoBanner` | Unit (Vitest) | No banner on supported kernels | abi_version=3 → banner hidden |
| 25d | `TestSandboxSection_ABIVersionAbsentNoBanner` | Unit (Vitest) | MAJ-009 — field absent → no banner | Mock response without abi_version field; banner hidden |
| 25e | `TestLandlockABI4IssueRef_SingleConstant` | Unit | FR-021 — issue ref pinned | Assert `LandlockABI4IssueRef == "#138"` and surfaced to UI |
| 26 | `TestE2E_AdminCreatesSecondAdmin` | E2E | Complete lifecycle via Playwright | Login → Access → Add user (password) → second admin logs in via `/auth/login` → admin works |
| 27 | `TestE2E_RateLimitTakesEffect` | E2E | Save rate limit → next agent turn respects it | Needs real LLM gate; softSkip if no API key |
| 28 | `TestE2E_PromptInjectionHotReload` | E2E | High strictness kicks in without restart | Before/after tool result |
| 29 | `TestConfigPUT_CannotSetGatewayUsers_Nested` | Integration | MAJ-004 nested walker | `{"gateway":{"users":[...]}}` → 403 |
| 30 | `TestConfigPUT_CannotSetGatewayUsers_DotPathLiteral` | Integration | MAJ-004 | `{"gateway.users":[...]}` → 403 |
| 31 | `TestConfigPUT_CannotSetGatewayUsers_Mixed` | Integration | MAJ-004 atomic reject | `{"gateway":{"port":5000,"users":[...]}}` → 403, no fields persisted |
| 32 | `TestConfigPUT_CannotSetDevModeBypass` | Integration | FR-018 | `{"gateway":{"dev_mode_bypass":true}}` → 403 |
| 33 | `TestDevModeBypass_UserEndpointsReturn503` | Integration | FR-019 | All 6 new user-mgmt endpoints → 503 |
| 34 | `TestDevModeBypass_RetentionSweepReturns503` | Integration | FR-019 | `/retention/sweep` → 503 |
| 35 | `TestDevModeBypass_PendingRestartReturns503` | Integration | FR-019 | `/config/pending-restart` → 503 |
| 36 | `TestSettingsNav_HidesAccessTabUnderBypass` | Unit (Vitest) | FR-019 SPA side | Mock `/config` with `dev_mode_bypass:true`; Access tab hidden |
| 37 | `TestAuditLog_SandboxConfigPUT_Emits` | Integration | FR-020 | Enable audit log; PUT sandbox-config; assert entry with `event=security_setting_change` |
| 38 | `TestAuditLog_UserCreate_RedactsPassword` | Integration | FR-020 | Password hash → `"***redacted***"` in log |
| 39 | `TestAuditLog_PasswordReset_RedactsNewPassword` | Integration | FR-020 | Same redaction on reset |
| 40 | `TestAuditLog_UserDelete_OmitsHashes` | Integration | FR-020 | Delete entry shows `{username,role}` only |
| 41 | `TestSSRFConfig_AllowInternalRemainsStringList` | Unit | CRIT-001 regression | Assert field is `[]string`; reject any code introducing bool |
| 42 | `TestAllowedPaths_ReadOnlySemanticDocumented` | Unit | CRIT-004 regression | Doc-comment check + sandbox behavior test ensuring no write grant leaks through |
| 43 | `TestHandleLogin_StoresBcryptedTokenHash` | Unit | FR-016 — token persisted only as bcrypt | Assert `token_hash` on disk is bcrypt, never plaintext |
| 44 | `TestGenerateUserToken_EntropyAndFormat` | Unit | FR-016 — token format | 32 bytes entropy; `omnipus_<64hex>` format |

### Test Datasets

#### Dataset: Security-score label buckets (US-1)

| # | Input (backend score) | Boundary Type | Expected label | Expected color | Traces to |
|---|---|---|---|---|---|
| 1 | 100 | max | Excellent | success | BDD: Security label by score bucket |
| 2 | 90  | threshold-hit | Excellent | success | same |
| 3 | 89  | threshold-below | Good | success | same |
| 4 | 67  | threshold-hit | Good | success | same |
| 5 | 66  | threshold-below | At risk | warning | same |
| 6 | 34  | threshold-hit | At risk | warning | same |
| 7 | 33  | threshold-below | Critical | error | same |
| 8 | 0   | min | Critical | error | same |

#### Dataset: Skill trust (US-3)

| # | Input | Boundary Type | Expected status | Expected body | Traces to |
|---|---|---|---|---|---|
| 1 | `block_unverified` | valid | 200 | `{level: "block_unverified"}` | BDD: Set skill trust |
| 2 | `warn_unverified` | valid | 200 | same shape | same |
| 3 | `allow_all` | valid | 200 | same shape + `warning: "dangerous…"` | same |
| 4 | `BLOCK_UNVERIFIED` | case-variant | 400 | error "must be one of: block_unverified, warn_unverified, allow_all (case-sensitive)" | MIN-001 resolution — reject non-canonical casing; no silent normalization |
| 5 | `ridiculous` | invalid | 400 | error message contains "must be one of" | same |
| 6 | `` (empty) | invalid | 400 | error message | same |

#### Dataset: Rate limits (US-5) — globals only (no per-user, MAJ-007 / #7 resolution)

| # | Input | Boundary | Expected status | Traces to |
|---|---|---|---|---|
| 1 | `{daily_cost_cap_usd: 0}` | zero = unlimited | 200 | BDD: Set daily cost cap |
| 2 | `{daily_cost_cap_usd: 25.5}` | positive | 200 | same |
| 3 | `{daily_cost_cap_usd: -5}` | negative | 400 | BDD: Negative value blocked client-side |
| 4 | `{max_agent_llm_calls_per_hour: 0}` | zero | 200 | same |
| 5 | `{max_agent_llm_calls_per_hour: 1000000}` | very large | 200 | no overflow |
| 6 | `{max_agent_tool_calls_per_minute: 0}` | zero | 200 | same |
| 7 | `{}` | empty object | 200 (no-op) | partial update semantics |
| 8 | `{max_agent_llm_calls_per_hour: "50"}` | JSON string-number in int field | 400 | MIN-004 — strict decoder |
| 9 | `{max_agent_llm_calls_per_hour: 10.5}` | float in int field | 400 | MIN-004 |
| 10 | `{max_agent_llm_calls_per_hour: NaN}` | NaN (non-JSON — send literal `"NaN"`) | 400 | MIN-004 |
| 11 | `{max_agent_llm_calls_per_hour: 9223372036854775807}` | MaxInt64 | 200 (int64 field) | boundary |
| 12 | `{max_agent_llm_calls_per_hour: 9223372036854775808}` | MaxInt64+1 | 400 (overflow) | MIN-004 |

#### Dataset: Allowed paths (US-6) — read-only, CRIT-004 resolution

| # | Input | Boundary | Expected status | Traces to |
|---|---|---|---|---|
| 1 | `/absolute/path` | valid | 200 | BDD: Add an absolute path |
| 2 | `~/home-prefix` | valid | 200 | home-dir prefix |
| 3 | `./relative` | invalid | 400 | BDD: Relative path rejected |
| 4 | `../parent` | invalid | 400 | path traversal |
| 5 | `/var/data/../etc` | contains `..` segment | 400 | BDD: `..` rejected |
| 6 | `/etc` | valid absolute | 200 (read-only; no "write stripped" note) | CRIT-004 resolution |
| 7 | `/tmp/symlink-to-etc` (symlink) | `lstat` detects S_IFLNK | 400 | BDD: symlink rejected |
| 8 | `` (empty) | invalid | 400 | empty rejected |

#### Dataset: SSRF allow_internal (US-7) — single []string field, CRIT-001 resolution

| # | Input (list entry) | Boundary | Expected status | Traces to |
|---|---|---|---|---|
| 1 | `10.0.0.0/8` | valid CIDR | 200 | BDD: Add a CIDR entry hot-reloads |
| 2 | `localhost` | valid hostname | 200 | same |
| 3 | `127.0.0.1` | valid exact IP | 200 | same |
| 4 | `10.0.0/8` | malformed CIDR | 400 | BDD: Malformed entry |
| 5 | `::1` | valid IPv6 | 200 | same |
| 6 | `not a host` | invalid hostname (has space) | 400 | same |
| 7 | `fe80::/10` | IPv6 link-local CIDR | 200 | accepted — SSRFChecker supports IPv6 CIDRs |
| 8 | `0.0.0.0/0` | wildcard | 200 + `slog.Warn event=ssrf_wildcard_accepted` | BDD: Wildcard confirms |
| 9 | `"`` (empty string) | empty | 400 | each entry must be non-empty |
| 10 | preset "Allow RFC1918 + loopback" applied via UI | valid preset | 200; on-disk list matches preset exactly | BDD: Preset round-trip |

#### Dataset: User management (US-10) — CRIT-003 + MAJ-003 + MAJ-005 + MAJ-007 resolution

| # | Input | Boundary | Expected status | Traces to |
|---|---|---|---|---|
| 1 | `POST /users {username:'alice', role:'user', password:'correct-horse-battery'}` | valid | 201 `{username:'alice', role:'user'}` — NO token | BDD: Create returns user+role only |
| 2 | `POST /users {username:'alice', role:'admin', password:'...'}` | valid elevate | 201 | same |
| 3 | `POST /users {username:'', role:'user', password:'...'}` | empty username | 400 | username format |
| 4 | `POST /users {username:'alice bob', role:'user', password:'...'}` | space in username | 400 | MIN username regex |
| 5 | `POST /users {username:'alice/bob', role:'user', password:'...'}` | slash in username | 400 | MIN username regex |
| 6 | `POST /users {username:'ALICE', role:'user', password:'...'}` | uppercase (allowed) | 201 | case-preserving storage |
| 7 | `POST /users {username:'alice', role:'superuser', password:'...'}` | invalid role | 400 | role validation |
| 8 | `POST /users {username:'alice', role:'ADMIN', password:'...'}` | role case-variant | 400 | MIN-001 — case-sensitive role |
| 9 | `POST /users {username:'alice', role:'user', password:'short'}` | short password | 400 | password ≥ 8 |
| 10 | `POST /users` with duplicate username | conflict | 409 | idempotency |
| 11 | `POST /auth/login` after step 1 | valid credentials | 200 + bearer token | verifies login mints token |
| 12 | `PUT /users/alice/password {password:'new-pwd'}` | admin resets user | 200; alice's TokenHash is empty in config; any old bearer → 401 | BDD: Reset password zeroes TokenHash |
| 13 | `PUT /users/alice/password` | non-existent user | 404 | not found |
| 14 | `PUT /users/alice/password` as non-admin | any | 403 | admin guard |
| 15 | `POST /auth/change-password {current_password, new_password}` | valid current (existing endpoint) | 200; token stays active | self-change-password via EXISTING endpoint (MAJ-007) |
| 16 | `POST /auth/change-password {current_password:wrong, new_password}` | invalid current | 401 | same |
| 17 | `DELETE /users/alice` | precondition: 1 admin, alice is admin | 409 last-admin | BDD: Cannot delete last admin |
| 18 | `DELETE /users/alice` | precondition: 2 admins | 204 | happy path |
| 19 | Concurrent `PATCH /users/A/role {role:'user'}` + `PATCH /users/B/role {role:'user'}` when A and B are the only admins | race | exactly one 200, one 409 | BDD: Concurrent demotion |
| 20 | Any new endpoint called when `dev_mode_bypass == true` | bypass active | 503 `{error:"user management disabled in dev-mode-bypass"}` | BDD: Dev-mode-bypass blocks elevation |
| 21 | `PUT /config {"gateway":{"users":[{...new admin...}]}}` | privileged-path bypass attempt | 403 blocked by blockedPaths walker | BDD: blockedPaths rejects nested gateway.users |

#### Dataset: Retention (US-9) — CRIT-002 resolution

| # | Input | Boundary | Expected status | Effective behavior | Traces to |
|---|---|---|---|---|---|
| 1 | `{session_days: 0, disabled: false}` | default | 200 | `RetentionSessionDays() == 90` | regression `TestRetention_ZeroSessionDaysStillMeansDefault90` |
| 2 | `{session_days: 0}` (missing disabled) | default | 200 | same 90-day default | preserves pre-sprint meaning |
| 3 | `{session_days: 30}` | explicit days | 200 | sweep uses 30 | BDD: Custom N days |
| 4 | `{session_days: 1}` | min explicit | 200 | sweep uses 1 | boundary |
| 5 | `{disabled: true, session_days: 0}` | keep forever | 200 + warning banner | sweep is no-op | BDD: Disabled keep forever |
| 6 | `{session_days: -1}` | negative | 400 | rejected | validation |
| 7 | `{session_days: 366}` | beyond 1 year | 200 | accepted; no upper bound | allowance |
| 8 | Concurrent `POST /retention/sweep` while nightly goroutine holds mutex | contention | 409 `{error:"sweep in progress"}` | BDD: Concurrent sweep 409 |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Doctor `score` backend contract (higher=better) | `pkg/gateway/rest_extra_test.go::TestHandleDoctorPOST` | No | Backend untouched |
| `HandleSandboxConfig` GET/PUT mode partial update | `pkg/gateway/rest_security_wave5_test.go::TestHandleSandboxConfig_PUTPartialUpdate` | No | Extending, not replacing |
| CSRF Bearer bypass | `pkg/gateway/middleware/csrf_test.go::TestCSRFMiddleware_BearerBypass` | No | New endpoints inherit the middleware |
| `safeUpdateConfigJSON` atomic rename | `pkg/gateway/rest_exec_test.go` coverage | No | No change to the helper |
| Tool-policies endpoint still works | `pkg/gateway/rest_tool_policies_test.go` | No | Tangential |
| Onboarding complete issues CSRF cookie | `pkg/gateway/rest_onboarding_test.go::TestHandleCompleteOnboarding_Success` | No | Untouched |
| `DiagnosticsSection` current label logic (PR #137 broken version) | `src/components/settings/DiagnosticsSection.test.tsx` | **YES** — tests must be rewritten per US-1 datasets since the current assertions codify the inverted behaviour this spec removes |
| `RetentionSessionDays()` returns 90 when `SessionDays <= 0` (existing semantics per `pkg/config/config.go:169–175`) | None today | **YES** — add `TestRetention_ZeroSessionDaysStillMeansDefault90` to protect CRIT-002 resolution | Sprint K MUST NOT flip this semantic |
| `OmnipusSSRFConfig.AllowInternal` is `[]string` accepting hostnames / IPs / CIDRs | `pkg/security/ssrf.go` tests | **YES** — add `TestSSRFConfig_AllowInternalRemainsStringList` asserting the field shape is unchanged | Prevents CRIT-001 regression if anyone re-attempts the bool split |
| `HandleLogin` overwrites `TokenHash` on each successful login | `pkg/gateway/rest_auth_test.go` | **YES** — add `TestHandleLogin_OverwritesTokenHash_AfterCreate` to prove the create-then-login path works | Confirms CRIT-003 resolution in practice |
| `AllowedPaths` grants read-only access | `pkg/config/sandbox.go` doc comment | **YES** — add `TestAllowedPaths_ReadOnlySemanticDocumented` asserting no accidental write grant introduced | Pins CRIT-004 |
| `POST /api/v1/auth/change-password` still wired and untouched | `pkg/gateway/rest_auth_test.go` change-password coverage | No | MAJ-007 — spec explicitly reuses |
| `HandleUpdateConfig` flat `blockedKeys` matcher today | `pkg/gateway/rest_test.go` PUT-config coverage | **YES** — add `TestConfigPUT_CannotSetGatewayUsers_*` to cover new nested walker (MAJ-004) | Prevents privilege-escalation regression |

---

## Functional Requirements

- **FR-001**: The Diagnostics card MUST display the backend `score` field directly without inversion; the label MUST treat higher scores as better.
- **FR-002**: The server MUST expose `PUT /api/v1/security/audit-log` accepting `{enabled: bool}` and MUST persist to `config.sandbox.audit_log`, returning `requires_restart: true`.
- **FR-003** (MIN-001 resolution): The server MUST expose `PUT /api/v1/security/skill-trust` accepting `{level: "block_unverified" | "warn_unverified" | "allow_all"}` exactly (case-sensitive). Any other value — including case-variants like `BLOCK_UNVERIFIED` — MUST be rejected with HTTP 400 and an error message naming the three canonical values. No silent normalization.
- **FR-004**: The server MUST expose `PUT /api/v1/security/prompt-guard` accepting `{level: "low" | "medium" | "high"}` and MUST hot-reload without restart (`requires_restart: false`).
- **FR-005**: The server MUST expose `PUT /api/v1/security/rate-limits` accepting partial updates across `daily_cost_cap_usd`, `max_agent_llm_calls_per_hour`, `max_agent_tool_calls_per_minute`; all zero means unlimited; negative values rejected with 400.
- **FR-006**: The server MUST extend `PUT /api/v1/security/sandbox-config` to accept `allowed_paths` (read-only filesystem access per `pkg/config/sandbox.go:47–49`) and `ssrf.allow_internal` (the existing single `[]string` field per `pkg/config/sandbox.go:90–101` — NO new `allow_internal_cidrs` field is introduced) list mutations via partial update. The server MUST validate `allowed_paths` entries at save time and reject any entry that is (a) not absolute, (b) contains `..` segments, or (c) has a symlink as its final path component (detected via `lstat` on `S_IFLNK`), returning HTTP 400 with the offending entry named. The server MUST validate each `ssrf.allow_internal` entry as either parseable CIDR (via `net.ParseCIDR`), parseable IP (via `net.ParseIP`), OR a hostname matching `^[A-Za-z0-9][A-Za-z0-9.-]*$`; invalid entries return HTTP 400. The server MUST log `slog.Warn` with `event=ssrf_wildcard_accepted` when an entry equals `0.0.0.0/0` or `::/0`.
- **FR-007**: The server MUST expose `PUT /api/v1/security/session-scope` accepting `{dm_scope: "main" | "per-peer" | "per-channel-peer" | "per-account-channel-peer"}` and MUST mark restart-required. (Accepts all four values defined in `pkg/routing/session_key.go`.)
- **FR-008**: The server MUST expose `PUT /api/v1/security/retention` accepting `{session_days: int, disabled: bool}` where `session_days >= 0` (preserving existing `<= 0 means default 90` semantics) and `disabled` is the NEW explicit "keep forever" flag. The existing `OmnipusRetentionConfig` struct gains a new `Disabled bool json:"disabled,omitempty"` field; `RetentionSessionDays()` semantics are unchanged. The server MUST implement `(*UnifiedStore).RetentionSweep(retentionDays int) (removed int, err error)` and MUST launch a daily goroutine at gateway boot (driven by `*time.Ticker(24h)` and a shutdown `context.Context`). The goroutine MUST observe context cancellation and exit cleanly on gateway shutdown. The nightly goroutine AND the on-demand endpoint MUST share a `sync.Mutex` so at most one sweep runs at a time; concurrent callers receive 409 `{error:"sweep in progress"}`. Sweep errors MUST be logged via `slog.Error` with `event=retention_sweep_failed` without exiting the goroutine. The server MUST expose `POST /api/v1/security/retention/sweep` for on-demand admin-triggered sweeps returning `{removed: int}`. The `TestOrphanSessionsParticipateInRetention` test in `pkg/session/orphan_cleanup_test.go` MUST be un-skipped and passing. A regression test `TestRetention_ZeroSessionDaysStillMeansDefault90` MUST assert that existing deployments' semantics are preserved.
- **FR-009**: The server MUST expose `GET /api/v1/users` (list users — no hashes, no plaintext), `POST /api/v1/users` (create user: body `{username, role, password}`, response `{username, role}` — NO token is returned at creation; user obtains a bearer token by logging in via the existing `POST /api/v1/auth/login`), `DELETE /api/v1/users/:username` (delete user; last-admin guard evaluated INSIDE the `safeUpdateConfigJSON` callback returns 409 when removal would leave zero admins), `PUT /api/v1/users/:username/password` (admin-only password reset: sets `PasswordHash` AND zeroes `TokenHash` in the same transaction — MAJ-003), and `PATCH /api/v1/users/:username/role` (change role; same last-admin guard inside the callback). Self-change-password MUST reuse the existing `POST /api/v1/auth/change-password` (MAJ-007) — NO new `PUT /users/self/password` endpoint is introduced. Username validation: `^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$` (case-preserving, case-sensitive). Role validation: exact match to `admin` or `user` (case-sensitive; `ADMIN` returns 400). There MUST NOT be a self-service "rotate my bearer token" endpoint.
- **FR-010**: The server MUST expose `GET /api/v1/config/pending-restart` returning a list of queued changes: `[{key, persisted_value, applied_value}]`. The endpoint MUST compare config-on-disk vs the in-memory snapshot the process booted with.
- **FR-011**: The SPA MUST render a global `RestartBanner` at the top of the Settings route whenever `/config/pending-restart` returns a non-empty list.
- **FR-012**: Every new admin endpoint MUST enforce admin-role via `middleware.RequireAdmin` in the handler chain.
- **FR-013**: Every new state-changing endpoint MUST be subject to the CSRF middleware (no new exempt paths).
- **FR-014**: ABI v4 visibility has three enforcement points: (a) on gateway boot when Landlock probe reports `abi_version >= 4`, the server MUST emit a structured `slog.Warn` with `{event:"landlock_abi_warning", abi_version, kernel, issue:"#138"}` exactly once; (b) the SPA MUST render a persistent yellow banner in `SandboxSection` when `/sandbox-status.abi_version >= 4`, with a session-scoped dismiss button (localStorage); (c) the SPA MUST show a confirmation modal before saving `sandbox.mode = enforce` when `abi_version >= 4`, showing "issue #138" and offering Save/Cancel.
- **FR-015**: The server MUST reject role-change or delete requests that would leave zero admin accounts with HTTP 409 and message "cannot leave the deployment with zero administrators".
- **FR-016**: Bearer tokens for all users MUST be cryptographically random (≥32 bytes of entropy via `crypto/rand.Read`) and persisted only as bcrypt hashes. Tokens are minted at login time by the existing `generateUserToken` (`rest_auth.go:643–649`) which returns `omnipus_<64-hex>` — user creation does NOT mint a token. The Sprint K contract is: user creation sets the password; the user's first login via `POST /api/v1/auth/login` mints their first token.
- **FR-017**: Non-admin users MUST NOT see the Access / Users tab in the Settings navigation.
- **FR-018** (MAJ-004): The generic `PUT /api/v1/config` handler MUST reject any body that sets — at any nesting depth — a key in the new `blockedPaths []string` list: `sandbox`, `credentials`, `security`, `gateway.users`, `gateway.dev_mode_bypass`. The existing flat `blockedKeys map[string]bool` (`rest.go:1622`) is replaced by a dotted-path walker that recursively inspects the submitted map. Rejected requests return HTTP 403 with the blocked path name echoed. Regression test `TestConfigPUT_CannotSetGatewayUsers_Returns403` MUST cover three shapes: `{"gateway":{"users":[...]}}`, `{"gateway.users":[...]}` (dot-path key literal), and a nested `{"gateway":{"port":5000,"users":[...]}}`.
- **FR-019** (MAJ-006): When `gateway.dev_mode_bypass == true`, ALL new endpoints introduced in this sprint (`GET/POST /users`, `PUT /users/:u/password`, `DELETE /users/:u`, `PATCH /users/:u/role`, `POST /api/v1/security/retention/sweep`, and all state-changing variants of the security settings endpoints in FR-002..FR-008) MUST return HTTP 503 with body `{error:"user management disabled in dev-mode-bypass"}`. The `GET /api/v1/config/pending-restart` endpoint MUST also return 503 under the same condition. The SPA MUST hide the Access tab and the RestartBanner when `/api/v1/config` reveals `dev_mode_bypass == true`.
- **FR-020** (MIN-003): Every state-changing admin endpoint introduced in this sprint (FR-002..FR-009 mutations plus `/retention/sweep`) MUST emit an audit-log entry via the existing `sandbox.audit_log` facility (when enabled) with `event=security_setting_change`, `actor` = username from auth context, `resource` = canonical config key being changed, `old_value`, `new_value`, `timestamp`. Password hashes and bearer tokens MUST be redacted (logged as `"***redacted***"`). User-deletion audits include only `{username, role}` of the removed user, not any hash data.
- **FR-021** (MAJ-001 ABI v4 single-source): The issue reference for the Landlock ABI v4 tracker MUST live at a single source of truth in the Go codebase (constant `LandlockABI4IssueRef = "#138"` in `pkg/sandbox`). All three surfaces (boot log, UI banner, save-time modal) MUST reference that constant — no hard-coded issue numbers in UI copy.

---

## Success Criteria

- **SC-001**: On a deployment with zero issues, the Diagnostics card displays `Security Score: 100 / 100 — Excellent` in green within 200ms of a `/doctor` response.
- **SC-002**: An admin completes the happy-path sequence "open Settings → edit any of the 9 new editors → save → see result" in ≤ 3 clicks per setting.
- **SC-003**: 100% of the 9 UI-parity gaps identified in the 2026-04-22 survey are editable through the UI (no setting requires touching `config.json` for ordinary configuration).
- **SC-004**: Restart-required changes appear in the `RestartBanner` within 500ms of the successful save response.
- **SC-005**: Hot-reloadable changes (prompt-injection-level, rate-limits, tool-policies, ssrf.allow_internal) take effect within 2 seconds of save without a process restart.
- **SC-006**: User-management operations complete in ≤ 1 second for deployments with ≤ 100 users.
- **SC-007**: The last-admin guard prevents every deletion/role-change attempt that would leave zero admins; integration test suite verifies all permutations.
- **SC-008**: Non-admin users receive HTTP 403 from every new state-changing endpoint (integration test coverage for all 10 endpoints in FR-002..009,010 matrix).
- **SC-009**: 0 golangci-lint issues, 100% of new BDD scenarios have a corresponding test row, Playwright E2E suite passes on plain HTTP (via PR #137 CSRF downgrade).

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-1 | Healthy deployment shows Excellent; Security label by score bucket; Toast reports security score | `TestDiagnostics_ScoreDisplay_HigherIsBetter`, `TestDiagnostics_ScoreLabel_ByBucket`, `TestDiagnostics_ToastAfterRun` |
| FR-002 | US-2 | Admin enables audit log; Non-admin sees read-only | `TestHandleSandboxAuditLog_PUTPersists`, `TestHandleSandboxAuditLog_NonAdmin403` |
| FR-003 | US-3 | Set skill trust (outline) | `TestHandleSkillTrust_ValidLevels` |
| FR-004 | US-4 | Hot-reload on save; Invalid value rejected | `TestHandlePromptInjection_HotReload`; part of admin-only matrix |
| FR-005 | US-5 | Set daily cost cap; Negative value blocked | `TestHandleRateLimits_PersistsAllThreeFields`; rate-limits dataset rows 3 |
| FR-006 | US-6, US-7 | Add absolute path; Relative path rejected; `..` rejected; Symlink rejected; Read-only badge shown; Preset round-trip; Advanced list; Malformed entry rejected; Wildcard confirms | `TestHandleSandboxConfig_AllowedPaths_AbsoluteAccepted`, `TestHandleSandboxConfig_AllowedPaths_RelativeRejected`, `TestHandleSandboxConfig_AllowedPaths_DotDotRejected`, `TestHandleSandboxConfig_AllowedPaths_SymlinkRejected`, `TestHandleSandboxConfig_SSRFAllowInternal_PresetRoundTrip`, `TestHandleSandboxConfig_SSRFAllowInternal_CIDRAccepted`, `TestHandleSandboxConfig_SSRFAllowInternal_HostnameAccepted`, `TestHandleSandboxConfig_SSRFAllowInternal_MalformedRejected`, `TestHandleSandboxConfig_SSRFAllowInternal_WildcardLogged` |
| FR-007 | US-8 | Change scope restart required (all four values); Unknown scope falls back | `TestHandleSessionDMScope_RestartRequired`, `TestHandleSessionDMScope_AllFourValuesAccepted`, `TestHandleSessionDMScope_GlobalRejected`, `TestSessionRoutingSection_UnknownScopeFallback` |
| FR-008 | US-9 | Default (session_days=0 means 90); Custom N days; Disabled keep-forever; On-demand sweep deletes aged files; Orphan sessions participate; Concurrent sweep 409; Pre-sprint zero preserved | `TestRetention_ZeroSessionDaysStillMeansDefault90`, `TestRetention_DisabledFlagMeansKeepForever`, `TestRetentionSweep_DeletesAgedFiles`, `TestRetentionSweep_NightlyGoroutineTicks`, `TestRetentionSweep_ConcurrentReturns409`, `TestRetentionSweep_GracefulShutdown`, `TestOrphanSessionsParticipateInRetention` (un-skipped), `TestHandleStorageRetention_PUT_ValidShape` |
| FR-009 | US-10 | Create returns no token; Last-admin guard inside write lock; Admin reset-password zeroes TokenHash; Change role; Username format; Role case-sensitive; Concurrent demotion race | `TestHandleUserCreate_ReturnsUserAndRoleOnly`, `TestHandleUserCreate_ThenLoginWithPassword_IssuesToken`, `TestHandleUserDelete_LastAdmin409`, `TestHandleUserDelete_LastAdminGuardInsideWriteLock`, `TestHandleUserResetPassword_ZeroesTokenHash`, `TestHandleUserResetPassword_PriorTokenReturns401`, `TestHandleUserChangeRole_AdminToUser`, `TestHandleUserChangeRole_ConcurrentDemotion_OneWins`, `TestHandleUserCreate_RejectsInvalidUsername`, `TestHandleUserCreate_RejectsUppercaseRole` |
| FR-010 | US-11 | Banner appears after restart-required save; Revert before restart clears banner; Banner auto-dismisses after restart; Non-admin 403 | `TestHandlePendingRestart_ListsQueuedChanges`, `TestHandlePendingRestart_SetThenRevertClearsDiff`, `TestHandlePendingRestart_EmptyAfterApply`, `TestHandlePendingRestart_NonAdmin403` |
| FR-011 | US-11 | Banner appears after restart-required save; Hot-reload save leaves banner untouched; Banner auto-dismisses | `TestRestartBanner_ShowsAfterSandboxModeChange`, `TestRestartBanner_HiddenAfterApply` |
| FR-012 | US-2..10 | Non-admin sees read-only (US-2); Non-admin hides Access tab (US-10) | `TestAdminOnly_AllNewPUTs` |
| FR-013 | all | (every state-changing scenario) | `TestCSRF_AllNewEndpointsSubjectToGate` |
| FR-014 | US-12 | Boot log on ABI v4; Banner in SandboxSection; Save-enforce modal; Compatible kernel skips all three | `TestLandlockABIv4_BootLogOnce`, `TestSandboxSection_ABIv4Banner`, `TestABIv4Warning_Kernel68EnforcePrompt`, `TestSandboxSection_ABIv3NoBanner` |
| FR-015 | US-10 | Cannot delete last admin; Concurrent demotion race | `TestHandleUserDelete_LastAdmin409`, `TestHandleUserDelete_LastAdminGuardInsideWriteLock`, `TestHandleUserChangeRole_ConcurrentDemotion_OneWins` |
| FR-016 | US-10 | Tokens are random (≥32 bytes) and bcrypt-persisted (minted at login only, not create) | `TestGenerateUserToken_EntropyAndFormat`, `TestHandleLogin_StoresBcryptedTokenHash` |
| FR-017 | US-10 | Non-admin hides the Access tab | `TestE2E_AdminCreatesSecondAdmin` (negative-control subtest) |
| FR-018 | MAJ-004 resolution | Nested `gateway.users` rejected; dot-path literal rejected; mixed body rejected | `TestConfigPUT_CannotSetGatewayUsers_Nested`, `TestConfigPUT_CannotSetGatewayUsers_DotPathLiteral`, `TestConfigPUT_CannotSetGatewayUsers_Mixed`, `TestConfigPUT_CannotSetDevModeBypass` |
| FR-019 | MAJ-006 resolution | All new endpoints return 503 under dev_mode_bypass; Access tab hidden | `TestDevModeBypass_UserEndpointsReturn503`, `TestDevModeBypass_RetentionSweepReturns503`, `TestDevModeBypass_PendingRestartReturns503`, `TestSettingsNav_HidesAccessTabUnderBypass` |
| FR-020 | MIN-003 resolution | Every state-changing endpoint writes audit entry with redacted secrets | `TestAuditLog_SandboxConfigPUT_Emits`, `TestAuditLog_UserCreate_RedactsPassword`, `TestAuditLog_PasswordReset_RedactsNewPassword`, `TestAuditLog_UserDelete_OmitsHashes` |
| FR-021 | MAJ-001 resolution | Single source of truth for issue #138 | `TestLandlockABI4IssueRef_SingleConstant`, `TestSandboxStatus_SurfacesIssueRef` |

**Completeness check**: all 21 FRs covered by both a BDD scenario and at least one test row. All TDD rows link back to a BDD scenario.

---

## Ambiguity Warnings — RESOLVED 2026-04-22

**Original clarifications**:

| # | Item | Resolution |
|---|---|---|
| 1 | `session.dm_scope` still used? | **KEEP US-8.** Investigation confirmed `pkg/routing/route.go:48` actively uses `cfg.Session.DMScope` to build per-channel session keys. |
| 2 | Password-reset flow | **Admin-resets-password only. NO self-service token rotation.** (Decision 2026-04-22.) |
| 3 | Self-rotate-token UX | **Not applicable** — self-service token rotation removed. |
| 4 | Self-role-change guard | Admin may demote self to `user` IFF another admin exists. Same last-admin guard as self-delete. |
| 5 | `pending-restart` auth scope | Admin-only; 503 under `dev_mode_bypass` (FR-019). |
| 6 | `allowed_paths` validation timing | **Reject invalid paths at save time.** PUT validates: absolute path, no symlinks in the final component, no `..` segments. Returns 400 with specific path name. READ-ONLY access only (CRIT-004). |
| 7 | SSRF `allow_internal` shape | **Uses the existing `[]string` field unchanged** (CRIT-001). UI offers presets + advanced list that both write the one field. No new bool, no new `allow_internal_cidrs`. |
| 8 | Storage retention sweep existence | **Does NOT exist yet.** US-9 ships both the backend sweep and the UI editor. New `Disabled bool` field for "keep forever" — existing `SessionDays<=0` semantics preserved (CRIT-002). |
| 9 | Landlock ABI v4 detection | **Log at boot + UI banner + save-time modal.** All reference issue #138 via a single Go constant (FR-021). UI suppresses all surfaces when the field is absent (MAJ-009). |
| 10 | Daily-cost-cap below already-spent | No special handling — next spend triggers cap check and blocks. |

**Adversarial-review-driven additions (Revision 2)**:

| # | Item | Resolution |
|---|---|---|
| 11 | Retention `session_days=0` semantic flip | REFUSED. Existing "0 means default 90" semantics preserved. New `disabled: bool` field for "keep forever". Regression test pins the old meaning. (CRIT-002) |
| 12 | User-creation token issuance | No token returned at creation; user logs in via existing endpoint. (CRIT-003) |
| 13 | AllowedPaths grant write? | No. Read-only. Explicit. (CRIT-004) |
| 14 | Reset-password mechanism | Zeroes `TokenHash` in the same `safeUpdateConfigJSON` transaction. (MAJ-003) |
| 15 | blockedKeys nested path handling | New `blockedPaths []string` walker; `gateway.users` blocked at any depth. (MAJ-004) |
| 16 | Last-admin TOCTOU race | Guard evaluated INSIDE the write-lock callback. (MAJ-005) |
| 17 | dev_mode_bypass elevation | All new endpoints return 503; Access tab hidden. (MAJ-006, FR-019) |
| 18 | Self-change-password endpoint collision | Reuse existing `POST /auth/change-password`. No new endpoint. (MAJ-007) |
| 19 | Restart banner after revert | Diff-based; set-then-revert clears banner. (MAJ-008) |
| 20 | abi_version null vs omitted | UI checks `typeof !== 'number'`. (MAJ-009) |

---

## Evaluation Scenarios (Holdout)

> **Note**: These scenarios are for post-implementation evaluation only. They must NOT be visible to the implementing agent during development. Not in the traceability matrix.

### Scenario: Operator onboards and exercises every editor in under 15 minutes
- **Setup**: Fresh OSS install on a new Linux VM, plain HTTP via `--sandbox=off`.
- **Action**: Admin completes onboarding, then clicks through every setting in Settings → Security and Settings → Access, saves at least one change per section.
- **Expected outcome**: All saves succeed; banner accurately lists the 3-4 restart-required changes.
- **Category**: Happy Path

### Scenario: Security Score reflects a real misconfiguration
- **Setup**: Gateway running with `sandbox.mode=off` and no providers configured.
- **Action**: Run doctor via the button.
- **Expected outcome**: Score < 90 (high severity deduction), label "Good" or worse; issues list enumerates the real problems.
- **Category**: Happy Path

### Scenario: Second admin can log in and administer
- **Setup**: First admin created at onboarding.
- **Action**: Create a second admin via the UI, log out, log in as the new admin, delete the first admin.
- **Expected outcome**: Second admin fully functional; first admin's session 401s on next request.
- **Category**: Happy Path

### Scenario: Last-admin guard blocks footgun
- **Setup**: One admin only.
- **Action**: Admin tries to (a) delete themselves, (b) change their role to `user`, (c) rotate their own token while locked out of saving the new one.
- **Expected outcome**: (a) and (b) blocked with a clear error; (c) proceeds but the UI enforces a "copy your token" modal that can't be dismissed without acknowledgement.
- **Category**: Error Path

### Scenario: ABI v4 boot-loop warning is true to its word
- **Setup**: Kernel 6.8 machine; `/sandbox-status.abi_version == 4`.
- **Action**: Admin saves mode=enforce anyway (uses "Save anyway" override), restarts the gateway.
- **Expected outcome**: Gateway exits with code 78; `gateway_panic.log` contains the landlock create_ruleset error; the pre-save modal's warning proved accurate.
- **Category**: Error Path

### Scenario: SSRF allow_internal hot-reload
- **Setup**: Agent configured with a web_search tool; internal HTTP server running on `10.0.0.5:8080`.
- **Action**: Admin adds `10.0.0.0/8` to SSRF allow_internal, saves, waits 3 seconds, asks the agent to search internal.
- **Expected outcome**: The search succeeds without a gateway restart.
- **Category**: Edge Case

### Scenario: Restart banner survives reload
- **Setup**: Admin saves a restart-required setting.
- **Action**: Admin hard-reloads the SPA (Cmd+Shift+R) without restarting the gateway.
- **Expected outcome**: Banner reappears after reload (server endpoint still reports the pending diff).
- **Category**: Edge Case

---

## Assumptions

- PR #137 is merged (or is a direct dependency of this branch) — the Bug 2/Bug 3/Bug 4-partial work is already on main before Sprint K begins.
- `safeUpdateConfigJSON` remains the write path for all config mutations.
- The existing 2-second config-poll reload catches hot-reload-eligible changes without additional plumbing.
- `middleware.RequireAdmin` already writes role into the request context via `withAuth` — the new endpoints don't need to re-implement role checks.
- The backend already has a `configSnapshot` capability (boot-time snapshot + live disk) accessible to the new `pending-restart` endpoint. If not, one small helper is added in this sprint alongside the endpoint.

## Clarifications

### 2026-04-22

- **Q**: Should the sprint include all 9 UI gaps in one PR, or split into a hotfix + follow-up? **A**: One sprint, all 9 gaps.
- **Q**: Should user management include token rotation / role change or only list? **A**: Admin-only CRUD + admin-only password reset + role change. **No self-service token rotation.**
- **Q**: Should the UI detect and warn about Landlock ABI v4? **A**: Log at boot AND show a persistent UI banner in SandboxSection. Separately, filed issue #138 to upgrade Landlock support so the banner becomes obsolete.
- **Q**: Should there be an in-UI restart button? **A**: No — operator uses their process supervisor. Document in README.
- **Q**: Should channel secrets be editable in the UI? **A**: No — they live encrypted in `credentials.json` and rotate via CLI. Call out as an explicit non-behavior.
- **Q**: What about the Bug 1 regression? **A**: Part of US-1. Remove the UI-side `100 − risk` inversion and fix thresholds to treat higher as better.
- **Q**: `allowed_paths` validation? **A**: Reject bad paths at save time (absolute only, no symlinks, no `..`). Returns 400.
- **Q**: SSRF `allow_internal` shape? **A** (Revision 1): Toggle + optional CIDR list. **A** (Revision 2, CRIT-001): REVISED — use existing `[]string` field unchanged. UI offers presets + advanced list that both write the one field. No new bool, no new `allow_internal_cidrs`.
- **Q**: Retention sweep already exists? **A**: No. Sprint K implements the sweep AND the UI toggle.
