# Spec — Workspace-scoped heartbeat config + global memory settings UI

- **Source ADR:** `docs/internal/architecture/ADR-027-workspace-scoped-heartbeat-and-global-memory-ui.md` (revised, criticals C-1…C-4 addressed)
- **Status:** Draft (plan-spec) · gate-confirmed by operator 2026-06-30
- **Branch target:** TBD (new branch off `main`)
- **Tech:** Go (gateway/config/session/workspace) · React 19 + Vite (SPA) · contract-first (`contracts/*`)

---

## 1. Overview

Move **heartbeat** configuration from per-agent to **per-(workspace, agent)**, stored on the Workspace record as `member_configs[agentId].heartbeat = { enabled, interval_minutes, body }`. Give the existing **global** memory/recap settings their first UI (a Settings → Memory tab) — their scope does **not** change. Make the heartbeat's standing session **workspace-scoped and undeletable while the heartbeat is active**, and **pin it on top** of the Session panel. Decommission the agent-level heartbeat path with **no migration** (deny-by-default).

Out of scope: the heartbeat *execution model* (Mission Loop / Resident) and the sliding-window/recall *memory rework* — both separate decisions.

## 1.1 Amendment A1 — post spec-grill (BINDING; overrides any conflict below)

This spec was grilled (`workspace-heartbeat-memory-config-spec-review.md`, verdict BLOCK). Operator resolutions (also in ADR-027 Amendment A1):

- **F-01 — no auth gate.** Member_config writes go through the existing workspace-edit path with **no owner/admin gate** (#406). **Drop the 403** (US-2.AC4, FR-006, DS-1.8). Validation (FR-003/004/005/005b) stays.
- **F-02 + F-04 — session lifecycle.** The standing session is created **eagerly when the heartbeat is enabled** (during the workspace save), stamped `workspace_id` + agent + `type="heartbeat"`, with its id stored at `member_configs[agentId].heartbeat.session_id`. **Delete-protection follows the persisted `enabled` flag:** enabled ⇒ 409 + trash hidden; disabled ⇒ deletable + trash returns; re-enable ⇒ protected (recreate the session if it was deleted). **No fail-open/closed** (FR-014). The cron job *continues* the pre-created session.
- **F-03 — memory transport.** The Memory tab uses a dedicated **`GET/PUT /api/v1/settings/memory`** (recap/retention only, never secrets) — not `PUT /config` (FR-019).
- **F-10 — agent contract.** Remove heartbeat fields from `Agent.yaml` + `AgentCreateRequest.yaml` **now** (FR-027).
- **F-07 — no body migration.** Existing `HEARTBEAT.md` bodies are NOT carried forward; operators re-enter per workspace.
- **F-05 / F-08 / F-09 — detail** *(see A2 §1.2 for the corrections)*: member-removal GC wired into the workspace handler (FR-022); predicate is **`!IsWorker()`** (A2 — not `IsMain()`; FR-025); the Heartbeat tab saves to the **workspace** mutation as a separate save (FR-016).

## 1.2 Amendment A2 — post re-grill #2 (BINDING; resolves the 2 mechanism criticals + majors)

Re-grill #2 confirmed A1 closed 3/4 round-1 criticals; the remaining two were mechanism gaps. Operator-confirmed resolutions:

- **F-02 — job↔session link (mechanism).** Add a `SessionID` field to the cron `JobSpec`; reconcile injects the eager session's id so the cron job **continues** it (not a fresh `NewScheduledSession`). A **workspace-aware** session creation stamps `workspace_id` + agent + `type="heartbeat"` at eager-create time. (FR-007b, FR-010)
- **G-01 — delete-guard lookup + computed `protected`.** The eager session carries `workspace_id`, so the guard loads **that one workspace** → finds the member whose `heartbeat.session_id == id` → 409 if `enabled` (bounded; no scan). `GET /sessions` returns a **computed `protected: bool`** derived from `member_configs.enabled` (single source of truth — **NOT** a stored flag); the SPA reads it to pin + hide the trash. (FR-014, FR-021, FR-028)
- **F-08 — predicate fix.** "Main agent" = **`!IsWorker()`**, NOT a new `IsMain()` (Mia is `type=core`, which "Main" would wrongly exclude). (FR-025)
- **F-06 — reconcile trigger.** Saving `member_configs` MUST trigger heartbeat reconcile (workspace-PUT doesn't today). (FR-007c)
- **F-13 — delete cascade.** `handleWorkspaceDelete` MUST also release members' heartbeat jobs + standing sessions (it cascades tasks/files only today). (FR-023)
- **F-11 — atomic contract.** The `Session.type += "heartbeat"` regen MUST ship in the **same change** as the stamping code (the strict generated zod enum rejects the whole payload on an unknown `type`; `rawToSession`'s fallback is dead). (FR-024)
- **F-09 — save scopes.** The Heartbeat tab's save MUST be a **separate workspace mutation**, NOT the agent autosave flow (`AgentProfile` is single-flow autosave today). (FR-016)
- **G-02 — memory endpoint.** `/settings/memory` is writable by **any authenticated user** (operator decision) and reads/writes ONLY the memory fields (no merge of sibling config / secrets). (FR-019)

## 2. Available Reference Patterns

`docs/reference/` was checked — no directory present. **N/A** (no reference library to map from). Internal patterns reused instead: the existing config-resolution helpers (`AgentConfig.HeartbeatIsEnabled()`-style effective getters), the contract-first add-a-wire-type process (CLAUDE.md §Contract regeneration), and the `withAuth`/owner-admin gate pattern in `pkg/gateway/rest_auth.go`.

## 3. Existing Codebase Context

### Symbols Involved
| Symbol | Role | Context |
|--------|------|---------|
| `pkg/workspace.Workspace` (`workspace.go`) | **extend** | add `member_configs map[string]WorkspaceMemberConfig`; new `WorkspaceMemberConfig` type |
| `contracts/components/schemas/Workspace.yaml` | **extend** | add `member_configs`; new `WorkspaceMemberConfig.yaml` |
| `contracts/components/schemas/Session.yaml` | **extend** | add `"heartbeat"` to the `type` enum |
| `Agent.yaml` + `AgentCreateRequest.yaml` | **remove** | delete heartbeat fields now (A1/F-10) |
| `MemorySettings.yaml` (new) + `GET/PUT /api/v1/settings/memory` | **add** | dedicated memory endpoint (A1/F-03) |
| `pkg/config.AgentConfig.{HeartbeatEnabled,HeartbeatInterval}` | **remove from read path** | decommission; forms + readers redirected |
| `pkg/coreagent.SeedConfig` (`core.go:776,835`) | **modify** | stop seeding agent-level heartbeat |
| `pkg/gateway/heartbeat_schedule.go` (`computeDesiredHeartbeats`, `ReconcileHeartbeatSchedules`) | **modify** | iterate (workspace × member) from `member_configs`; key `heartbeat:<ws>:<agent>`; read body from config |
| `pkg/cron` `JobSpec` | **extend** | add `SessionID` so reconcile injects the eager session id (A2/F-02) |
| `pkg/gateway/schedules.go::pickSession` / `NewScheduledSession` | **modify** | continue the eager session via `JobSpec.SessionID`; add a workspace-aware creator stamping `workspace_id`+agent+`type="heartbeat"` (A2) |
| `pkg/gateway/rest.go::deleteSession` | **modify** | 409 guard when an enabled heartbeat references the session; audit on block |
| `pkg/gateway/rest.go` (workspace PUT handler) | **modify** | accept/validate `member_configs` (bounds + auth) |
| `src/components/agents/AgentProfile.tsx` | **modify** | remove heartbeat from Personality; add conditional **Heartbeat** tab that saves to the **workspace** (A3), separate from the agent save |
| `src/components/agents/wizard/Step2Personality.tsx` | **modify** | remove heartbeat fields; add soul `.md` upload (parity, US-10) |
| `src/store/ui.ts::openEditAgentSlideOver` | **modify** | add explicit `workspaceId` param (A5); update all callers |
| `src/components/workspaces/WorkspaceTeamTab.tsx` | **modify** | pass `workspaceId` when opening the edit slide-over |
| `src/components/screens/SettingsScreen.tsx` (+ a new `MemorySection.tsx`) | **extend** | add Memory tab over global recap/retention settings |
| `src/components/chat/SessionPanel.tsx` | **modify** | pin/badge heartbeat session; disable its delete while active |

### Impact Assessment
| Symbol Modified | Risk | Direct dependents (d=1) | Indirect (d=2) |
|---|---|---|---|
| `heartbeat_schedule.go` reconcile | **HIGH** | cron service job set; gateway boot reconcile | every Main agent's heartbeat firing |
| `AgentConfig.Heartbeat*` read path (~15+ sites in `rest.go`) | **HIGH** | Agent GET/PUT serialization; `SeedConfig`; reconcile | Mia's live heartbeat (silent-death risk if partial) |
| `deleteSession` | **MEDIUM** | DELETE `/sessions/{id}`; SessionPanel delete | media-ref release (already wired) |
| `Workspace.yaml` / `Session.yaml` | **MEDIUM** | generated Go + TS types; `make verify-contracts` | all Workspace/Session consumers |
| `AgentProfile.tsx` tabs | **MEDIUM** | edit slide-over; Team tab; Agents screen | `AgentProfile.test.tsx` |
| `SessionPanel.tsx` | **MEDIUM** | session list render; delete flow | `SessionPanel.test.tsx` (35 tests) |

### Relevant Execution Flows
| Flow | Relevance |
|---|---|
| Gateway boot → `ReconcileHeartbeatSchedules` | now reads `member_configs`; legacy `heartbeat:<agent>` jobs deleted |
| Heartbeat fire → `pickSession` (SessionModeContinue) | standing session stamped `type=heartbeat` + `workspace_id` |
| Team tab → `openEditAgentSlideOver(agentId)` → `AgentProfile` | gains `workspaceId` → renders Heartbeat tab |
| DELETE `/sessions/{id}` | guarded against an active heartbeat's session |
| `CoreTeam` member removal | GCs `member_configs` entry + heartbeat job + session protection |

---

## 4. User Stories & Acceptance Criteria

### US-1 — Per-(workspace,agent) heartbeat storage & contract — **P0**
As a workspace owner, I want each member agent's heartbeat (enabled, interval, body) stored on the workspace, so a shared agent runs a different heartbeat per workspace.
- **Why P0:** the foundational data model; everything else reads it.
- **Independent test:** PUT a workspace with `member_configs[a].heartbeat`, GET it back identical; generated Go/TS types compile; `make verify-contracts` passes.
- **Acceptance:**
  1. **Given** a workspace with agent `a` in `CoreTeam`, **When** I PUT `member_configs.a.heartbeat = {enabled:true, interval_minutes:30, body:"…"}`, **Then** GET returns exactly that.
  2. **Given** the same agent in two workspaces, **When** each sets a different heartbeat, **Then** the two configs are independent.
  3. **Given** a fresh workspace, **When** I GET it, **Then** `member_configs` is empty and every (ws,agent) heartbeat is effectively OFF.

### US-2 — Bounds & authorization on member_configs writes — **P0**
As the platform, I must reject unbounded/unauthorized member_config writes, to prevent storage abuse and orphan configs.
- **Why P0:** security/cost guard (grill C-3).
- **Independent test:** writes with a non-CoreTeam key, `interval_minutes<5`, or oversized body are rejected (validation only; no auth gate — A1/F-01).
- **Acceptance:**
  1. **Given** agent `x` NOT in `CoreTeam`, **When** I PUT `member_configs.x`, **Then** 422 (unknown member).
  2. **Given** `interval_minutes:4`, **When** I PUT, **Then** 422 (min 5).
  3. **Given** a `body` over the cap, **When** I PUT, **Then** 422 (body too large).
  4. *(A1/F-01: dropped — NO auth gate; member_config writes use the existing workspace-edit path. Validation-only.)*

### US-3 — Workspace-aware heartbeat reconcile — **P0**
As an operator, I want enabled per-(ws,agent) heartbeats to actually run from the workspace config.
- **Why P0:** makes the config do anything.
- **Independent test:** with one enabled member heartbeat, reconcile produces exactly one job `heartbeat:<ws>:<agent>` whose run uses the config `body` and creates a session carrying `workspace_id`.
- **Acceptance:**
  1. **Given** `member_configs.a.heartbeat.enabled=true, interval_minutes=30`, **When** reconcile runs, **Then** a job `heartbeat:<ws>:a` exists at 30-min cadence.
  2. **Given** that job fires, **When** the agent runs, **Then** the prompt body equals `member_configs.a.heartbeat.body` (NOT `agents/a/HEARTBEAT.md`).
  3. **Given** a heartbeat is disabled, **When** reconcile runs, **Then** its job is removed.
  4. **Given** the same agent enabled in two workspaces, **When** reconcile runs, **Then** two distinct jobs exist.

### US-4 — Decommission the agent-level heartbeat path (no migration) — **P0**
As a maintainer, I want the old per-agent heartbeat path fully retired so nothing reads stale fields or silently dies.
- **Why P0:** grill C-4 — partial cutover kills Mia's heartbeat silently.
- **Independent test:** no production reader consults `AgentConfig.Heartbeat*`; `SeedConfig` writes no agent-level heartbeat; existing per-agent heartbeats do not auto-appear in any workspace.
- **Acceptance:**
  1. **Given** cutover, **When** the gateway boots, **Then** no `heartbeat:<agent>` (legacy-keyed) job exists.
  2. **Given** `SeedConfig` runs on a fresh install, **Then** no agent-level `heartbeat_enabled/interval` is written and no heartbeat is active until a workspace opts in.
  3. **Given** a config with legacy `agents[].heartbeat_enabled=true`, **When** the gateway boots, **Then** that value is ignored (no heartbeat) and logged once.

### US-5 — Conditional Heartbeat tab on the edit form — **P1**
As a workspace owner, when I open an agent's edit form **from the Team tab**, I want a Heartbeat tab to configure that agent's heartbeat for *this* workspace; opened globally, no such tab.
- **Why P1:** the primary editing surface; depends on US-1.
- **Independent test:** `AgentProfile` shows the Heartbeat tab iff a `workspaceId` context is present; the create wizard and Personality tab no longer show heartbeat.
- **Acceptance:**
  1. **Given** the Team tab, **When** I open agent `a`'s editor, **Then** a **Heartbeat** tab is present and edits `member_configs.a.heartbeat`.
  2. **Given** the global Agents screen, **When** I open `a`'s editor, **Then** there is **no** Heartbeat tab.
  3. **Given** the create wizard, **Then** Step 2 has **no** heartbeat fields.
  4. **Given** the edit form's Personality tab, **Then** it no longer contains "Enable heartbeat".

### US-6 — Global Settings → Memory tab — **P1**
As an admin, I want a Memory tab in global Settings to see and change the recap/retention settings that today have no UI.
- **Why P1:** usability; no scope change (settings stay global).
- **Independent test:** the tab reads/saves the global values via the dedicated `GET/PUT /api/v1/settings/memory` (A1/F-03), not `PUT /config`.
- **Acceptance:**
  1. **Given** Settings, **Then** a **Memory** tab lists: `auto_recap_enabled`, `idle_timeout_minutes`, `bootstrap_recap_enabled`, `bootstrap_recap_max_per_minute`, `bootstrap_recap_daily_budget_usd`, `recap_model_allow_list`, retention `session_days`, `memory_retros_days`.
  2. **Given** I toggle `auto_recap_enabled` and save, **When** I reload, **Then** the new value persists.
  3. **Given** these are global, **Then** the workspace Heartbeat tab does **not** show any memory/recap setting.

### US-7 — Heartbeat session: eager-created, undeletable while enabled — **P0** *(amended A1/F-02+F-04)*
As an operator, the heartbeat's session should appear when I turn the heartbeat on and be undeletable while it stays on — driven by the toggle, not a background read.
- **Why P0:** integrity of the continued session (the ADR's core promise).
- **Independent test:** enabling the heartbeat creates `S` (stamped `type=heartbeat`+`workspace_id`, id stored on `member_configs`); DELETE `S` → 409 while `enabled`; → 200 once disabled.
- **Acceptance:**
  1. **Given** I enable a heartbeat and save, **Then** a standing session `S` exists, stamped `type="heartbeat"` + `workspace_id`, with its id at `member_configs[a].heartbeat.session_id`.
  2. **Given** `enabled=true`, **When** I DELETE `S`, **Then** 409, `S` survives, and an audit entry is written.
  3. **Given** I set `enabled=false` and save, **When** I DELETE `S`, **Then** 200 (trash control returns).
  4. **Given** `S` was deleted while disabled, **When** I re-enable the heartbeat, **Then** the standing session is recreated and protected again.
  5. **Given** a normal (non-heartbeat) session, **When** I DELETE it, **Then** 200 (unchanged).

### US-8 — Heartbeat session pinned in the Session panel — **P1**
As a user, I want the heartbeat session pinned at the top of the Session panel, badged, with delete disabled while active.
- **Why P1:** discoverability + matches the server-side lock.
- **Independent test:** a session with `type="heartbeat"` renders first, badged, with its delete control absent/disabled.
- **Acceptance:**
  1. **Given** the active workspace has a heartbeat session, **When** I open the panel, **Then** it appears **pinned above** the normal workspace→agent groups with a heartbeat badge.
  2. **Given** that session, **Then** its delete (trash) control is disabled/hidden while the heartbeat is active.
  3. **Given** the session carries `workspace_id`, **Then** it is associated with the workspace (not "No workspace").

### US-9 — Membership GC on CoreTeam removal — **P1**
As a workspace owner, when I remove an agent from the team, its heartbeat config/job/session-protection should be cleaned up.
- **Why P1:** prevents orphan configs/jobs (grill C-3 lifecycle).
- **Independent test:** removing agent `a` from `CoreTeam` deletes `member_configs.a`, removes its heartbeat job, and unprotects its standing session.
- **Acceptance:**
  1. **Given** agent `a` has an enabled heartbeat, **When** I remove `a` from `CoreTeam`, **Then** `member_configs.a` is deleted and the `heartbeat:<ws>:a` job is removed.
  2. **Given** the removal, **When** reconcile next runs, **Then** no job is recreated for `a` in that workspace.
  3. **Given** the removal, **Then** the previously-protected standing session becomes deletable.

### US-10 — Soul markdown upload on the create wizard *(scope addition — parity, beyond ADR-027)* — **P2**
As someone creating an agent, I want to upload a `SOUL.md` file into the soul field on the **add** screen — matching the edit form — instead of pasting it.
- **Why P2:** pure parity/convenience; the edit form already has it (`AgentProfile.UploadButton`). Bundled because this work already modifies `Step2Personality.tsx`. Not blocking the heartbeat/memory work.
- **Independent test:** the create wizard's soul field shows an "Upload .md" control that reads a markdown file into `payload.soul`.
- **Acceptance:**
  1. **Given** the create wizard Step 2, **Then** the soul field offers an **Upload .md** control (accepts `.md/.markdown/.txt`).
  2. **Given** I pick a markdown file, **When** it loads, **Then** its contents fill the soul field.
  3. **Given** the edit form, **Then** its existing soul/heartbeat upload behaviour is unchanged.

### Edge Cases
- E1: same agent enabled in N workspaces → N independent jobs/sessions.
- E2: workspace deleted while a member heartbeat is active → its jobs + standing sessions are released (cascade).
- E3: `body` empty but `enabled=true` → reject (422: body required when enabled) **[ambiguity — see §10]**.
- E4: `interval_minutes` exactly 5 (min) and a very large value → accepted; clamp/validate boundaries.
- E5: concurrent PUT member_configs + reconcile → last-write-wins on config; reconcile reads the committed state.
- E6: gateway restart mid-heartbeat → standing session reused via SessionModeContinue (already), still protected.
- E7: legacy session with `type=scheduled` (not `type=heartbeat`) → treated as a normal session (not pinned).
- E8: *(A1/F-04)* no TOCTOU — the delete-guard reads the persisted `enabled` flag, not a live cron state; the check is deterministic.
- E9: non-Main agent (worker) in CoreTeam → no Heartbeat tab, no heartbeat config accepted.

## 5. Behavioral Contract & Boundaries

### Behavioral Contract
- When a workspace owner sets `member_configs[a].heartbeat.enabled=true` with a valid interval+body, the system runs agent `a`'s heartbeat in that workspace at that cadence using that body.
- When `enabled` is false/absent, the system runs no heartbeat for that (workspace, agent).
- When the same agent is configured in two workspaces, the system runs two independent heartbeats.
- When a heartbeat is active, the system rejects deletion of its standing session (409) and pins it atop the Session panel.
- When an agent is removed from a workspace's team, the system GCs its heartbeat config, job, and session protection.
- When a member_config write is out of bounds, the system rejects it (422) and persists nothing. (No auth gate — A1/F-01.)
- When the global Memory settings are changed via the Settings tab, the system persists them globally (no per-workspace effect).

### Explicit Non-Behaviors
- The system must **not** read `agents/<id>/HEARTBEAT.md` for heartbeat content, because the body now lives in `member_configs` (avoids C-1 ambiguity).
- The system must **not** auto-migrate existing per-agent heartbeats into any workspace, because "the agent's current workspace" is undefined (C-2).
- `SeedConfig` must **not** write agent-level heartbeat fields, because nothing reads them post-cutover (C-4).
- The system must **not** expose memory/recap settings per-agent or per-workspace, because they remain global by decision.
- The system must **not** allow a non-CoreTeam agentId as a member_config key (orphan/abuse prevention).
- The system must **not** allow per-workspace overrides of the global cost/safety guards (budget, rate-limit, model allow-list).

### Integration Boundaries
| System | In/Out | Contract | Failure behavior | Dev approach |
|---|---|---|---|---|
| Cron service (`pkg/cron`) | reconcile writes/removes `heartbeat:<ws>:<agent>` jobs; reads job SessionID for the delete-guard | in-process Go API | if cron unavailable, reconcile no-ops + logs; delete-guard fails **open** only if cron state is unreadable (logged) | real, in-process |
| Session store | standing session stamped `type=heartbeat`+`workspace_id`; delete-guard reads it | Go interface | store error on delete → 500 (unchanged) | real |
| SPA ↔ gateway | `member_configs` on Workspace; `type="heartbeat"` on Session; global config PUT for Memory | generated types (Constraint #8) | zod-validate inbound; drop+counter on mismatch | generated + zod |

## 6. BDD Scenarios

> Categories: HP=Happy Path, AP=Alternate, EP=Error, EC=Edge. Every scenario `Traces to:` US#.AC#.

**Scenario (HP): store & read a member heartbeat** — Traces to US-1.AC1
Given a workspace `w` with `CoreTeam=[a]`
When I `PUT /workspaces/w` with `member_configs.a.heartbeat={enabled:true,interval_minutes:30,body:"x"}`
Then `GET /workspaces/w` returns `member_configs.a.heartbeat` equal to that object.

**Scenario (HP): independent per-workspace config** — Traces to US-1.AC2
Given agent `a` ∈ `CoreTeam` of `w1` and `w2`
When `w1` sets interval 30 and `w2` sets enabled=false
Then the two member_configs are independent and neither read affects the other.

**Scenario (EC): empty member_configs ⇒ heartbeat off** — Traces to US-1.AC3
Given a fresh workspace
When reconcile runs
Then no heartbeat job exists for any member.

**Scenario (EP): unknown member key rejected** — Traces to US-2.AC1
Given `x ∉ CoreTeam`
When I `PUT member_configs.x`
Then the response is 422 and nothing is persisted.

**Scenario Outline (EP): bounds validation** — Traces to US-2.AC2,AC3
Given a member_config write with `<field>=<value>`
When I PUT it
Then the response is 422 with `<reason>`.
| field | value | reason |
|---|---|---|
| interval_minutes | 4 | below min 5 |
| interval_minutes | 0 | below min 5 |
| body | >cap bytes | body too large |

**Scenario (REMOVED A1/F-01): non-owner write rejected** — dropped; no auth gate (§1.1). member_config writes are validation-only on the existing workspace-edit path.

**Scenario (HP): reconcile creates a workspace-keyed job** — Traces to US-3.AC1
Given `member_configs.a.heartbeat={enabled:true,interval_minutes:30}`
When reconcile runs
Then exactly one job `heartbeat:w:a` exists at 30-min cadence.

**Scenario (HP): heartbeat uses the config body** — Traces to US-3.AC2
Given the job `heartbeat:w:a` fires
When the agent runs
Then the prompt body equals `member_configs.a.heartbeat.body` and `agents/a/HEARTBEAT.md` is not read.

**Scenario (AP): disabling removes the job** — Traces to US-3.AC3
Given an enabled member heartbeat with a live job
When I set `enabled=false` and reconcile runs
Then the job is removed.

**Scenario (EC): same agent two workspaces ⇒ two jobs** — Traces to US-3.AC4 / US-1.AC2 / E1
Given `a` enabled in `w1` and `w2`
When reconcile runs
Then jobs `heartbeat:w1:a` and `heartbeat:w2:a` both exist.

**Scenario (HP): no legacy job after cutover** — Traces to US-4.AC1
Given cutover
When the gateway boots
Then no job keyed `heartbeat:<agent>` (without a workspace segment) exists.

**Scenario (HP): seed writes no agent-level heartbeat** — Traces to US-4.AC2
Given a fresh install
When `SeedConfig` runs
Then no `agents[].heartbeat_enabled` is set and no heartbeat is active.

**Scenario (AP): legacy agent-level heartbeat ignored** — Traces to US-4.AC3
Given a config with `agents[a].heartbeat_enabled=true`
When the gateway boots
Then no heartbeat runs for `a` and a single warning is logged.

**Scenario (HP): Heartbeat tab in workspace context** — Traces to US-5.AC1
Given the Team tab
When I open agent `a`'s editor
Then a Heartbeat tab is shown and edits `member_configs.a.heartbeat`.

**Scenario (AP): no Heartbeat tab globally** — Traces to US-5.AC2
Given the global Agents screen
When I open `a`'s editor
Then no Heartbeat tab is shown.

**Scenario (HP): heartbeat removed from create + personality** — Traces to US-5.AC3,AC4
Given the create wizard Step 2 and the edit Personality tab
Then neither contains heartbeat fields.

**Scenario (HP): soul markdown upload on the add screen** — Traces to US-10.AC1,AC2
Given the create wizard Step 2
When I upload a `SOUL.md` file via the Upload .md control
Then its contents populate the soul field.

**Scenario (HP): Memory settings tab persists** — Traces to US-6.AC1,AC2
Given the Settings → Memory tab
When I toggle `auto_recap_enabled` and save
Then a reload shows the new value.

**Scenario (AP): memory absent from workspace tab** — Traces to US-6.AC3
Given the workspace Heartbeat tab
Then no memory/recap setting is present.

**Scenario (EP): delete blocked while active** — Traces to US-7.AC1
Given an active heartbeat with standing session `S`
When I `DELETE /sessions/S`
Then the response is 409, `S` survives, and an audit entry is written.

**Scenario (HP): delete allowed once disabled** — Traces to US-7.AC2
Given the heartbeat is disabled and its job removed
When I `DELETE /sessions/S`
Then the response is 200.

**Scenario (HP): normal session still deletable** — Traces to US-7.AC3
Given a non-heartbeat session
When I delete it
Then the response is 200.

**Scenario (HP): heartbeat session pinned** — Traces to US-8.AC1,AC2
Given a session with `type="heartbeat"` in the active workspace
When I open the Session panel
Then it is rendered first with a heartbeat badge and its delete control is disabled.

**Scenario (HP): GC on member removal** — Traces to US-9.AC1,AC2
Given agent `a` with an enabled heartbeat in `w`
When I remove `a` from `w.CoreTeam`
Then `member_configs.a` is deleted, the job is removed, and reconcile does not recreate it.

**Scenario (EC): workspace delete cascades** — Traces to E2
Given a workspace with an active member heartbeat
When the workspace is deleted
Then its heartbeat jobs and standing sessions are released.

## 7. TDD Plan

| Order | Test | Level | Traces to BDD | Description |
|---|---|---|---|---|
| 1 | `TestWorkspaceMemberConfig_RoundTrip` | Unit | store member heartbeat | marshal/unmarshal `WorkspaceMemberConfig`; zero-value = off |
| 2 | `TestMemberConfig_Validation` | Unit | bounds validation; unknown member; body cap | interval≥5, key∈CoreTeam, body cap |
| 3 | `TestEffectiveHeartbeat_Resolution` | Unit | empty ⇒ off; enabled ⇒ on | resolve from member_configs; deny-by-default |
| 4 | `TestComputeDesiredHeartbeats_WorkspaceKeyed` | Unit | reconcile creates job; two workspaces | desired set keyed `heartbeat:<ws>:<agent>` from member_configs |
| 5 | `TestReconcile_DisableRemovesJob` | Unit | disabling removes job | |
| 6 | `TestSeedConfig_NoAgentHeartbeat` | Unit | seed writes no heartbeat | `SeedConfig` emits no agent-level heartbeat |
| 7 | `TestDeleteGuard_Predicate` | Unit | delete blocked while active | predicate: enabled heartbeat job references SessionID |
| 8 | `TestSessionStamp_TypeHeartbeat` | Unit | pinned (type set) | standing session stamped `type=heartbeat`+`workspace_id` |
| 9 | `Contract: WorkspaceMemberConfig + Session.type+="heartbeat"` | Unit | (schema) | `make verify-contracts` green; generated types compile |
| 10 | `TestWorkspacePUT_MemberConfig_Bounds` | Integration | unknown/bounds | 422 paths; happy 200 (no auth gate — A1/F-01) |
| 11 | `TestReconcile_Integration_BodyFromConfig` | Integration | heartbeat uses config body | fired job's prompt = config body; not HEARTBEAT.md |
| 12 | `TestDeleteSession_409_WhileActive` | Integration | delete blocked / allowed | 409 active, 200 disabled, 200 normal, audit on 409 |
| 13 | `TestSessionsList_OriginField` | Integration | heartbeat session pinned (data) | GET `/sessions` returns `type=heartbeat` |
| 14 | `TestMemberRemoval_GC` | Integration | GC on member removal; cascade | config+job removed; not recreated; workspace-delete cascade |
| 15 | `TestBoot_NoLegacyHeartbeatJobs` | Integration | no legacy job; legacy ignored | cutover removes `heartbeat:<agent>`; warns once |
| 16 | `AgentProfile.heartbeatTab.test.tsx` | Unit (vitest) | tab in ws context / not global; removed from personality | conditional tab on `workspaceId` |
| 17 | `Step2Personality.test.tsx` (update) | Unit (vitest) | heartbeat removed from create | no heartbeat fields |
| 18 | `MemorySection.test.tsx` | Unit (vitest) | memory tab persists | reads/saves global settings |
| 19 | `SessionPanel.heartbeatPin.test.tsx` | Unit (vitest) | pinned + delete disabled | `type=heartbeat` first + badge + delete off |
| 20 | `e2e: workspace-heartbeat.spec.ts` | E2E | Team→tab→save→fires; panel pin; delete blocked | full flow on the seeded gateway |
| 21 | `e2e: settings-memory.spec.ts` | E2E | memory tab persists | toggle + reload |
| 22 | `Step2Personality.soulUpload.test.tsx` | Unit (vitest) | soul md upload on the add screen | upload `.md` → fills `payload.soul`; edit-form upload unchanged |

**Order:** unit (1–9, 16–19) → integration (10–15) → E2E (20–21).

### Test Datasets

**DS-1 member_config validation** (Traces to US-2 scenarios)
| # | interval_minutes | key∈CoreTeam | body | auth | expect | Traces |
|---|---|---|---|---|---|---|
| 1 | 30 | yes | "ok" | owner | 200 | US-2 HP |
| 2 | 5 | yes | "ok" | owner | 200 (min boundary) | US-2 EC |
| 3 | 4 | yes | "ok" | owner | 422 below-min | US-2.AC2 |
| 4 | 0 | yes | "ok" | owner | 422 below-min | US-2.AC2 |
| 5 | 30 | **no** | "ok" | owner | 422 unknown-member | US-2.AC1 |
| 6 | 30 | yes | (>cap) | owner | 422 body-too-large | US-2.AC3 |
| 7 | 30 | yes | "" + enabled | owner | 422 body-required *[E3]* | E3 |
| 8 | 100000 | yes | "ok" | owner | 200 (large interval accepted) | US-2 EC |
| 9 | 30 | yes | unicode/emoji | owner | 200 | edge |

**DS-2 delete-guard** (Traces to US-7)
| # | session type | heartbeat enabled | expect | Traces |
|---|---|---|---|---|
| 1 | heartbeat | true | 409 + audit | US-7.AC2 |
| 2 | heartbeat | false | 200 | US-7.AC3 |
| 3 | chat | n/a | 200 | US-7.AC5 |
| 4 | heartbeat | re-enabled after delete | session recreated + protected | US-7.AC4 |

**DS-3 reconcile keying** (Traces to US-3, E1)
| # | enabled-in | expect jobs | Traces |
|---|---|---|---|
| 1 | w1 | {heartbeat:w1:a} | US-3.AC1 |
| 2 | w1,w2 | {heartbeat:w1:a, heartbeat:w2:a} | US-3.AC4 / E1 |
| 3 | none | {} | US-1.AC3 |
| 4 | w1 then disabled | {} | US-3.AC3 |

### Regression Requirements
This **modifies existing functionality** (heartbeat, deleteSession, AgentProfile, SessionPanel, Workspace/Session contracts).
- **Preserve:** non-heartbeat session deletion (200); `deleteSession` media-ref release (recent fix); `SessionPanel` workspace→agent nesting + hover card (35 existing tests); `AgentProfile` Basics/Personality/Tools/Runtime/Advanced tabs; Workspace CRUD; cron reconcile idempotency.
- **Tests that MUST stay green unchanged:** `SessionPanel.test.tsx`, `SessionPanel.token.test.tsx`, `layout/SessionPanel.test.tsx`; `AgentProfile.test.tsx` (minus the moved heartbeat assertions); existing workspace + cron + session Go tests.
- **New regression tests:** `TestDeleteSession_NonHeartbeat_Unchanged`; `TestReconcile_Idempotent_NewKeying`; a SessionPanel test asserting non-heartbeat sessions render in their groups unchanged while only `type=heartbeat` is pinned.
- **Regression dataset:** DS-2 row 3 (normal delete), DS-3 row 3 (no heartbeat ⇒ no jobs).

## 8. Requirements & Success Criteria

### Functional Requirements
- **FR-001** — The Workspace record MUST carry `member_configs: map[agentId]WorkspaceMemberConfig` with `heartbeat={enabled,interval_minutes,body}`.
- **FR-002** — `member_configs` and `WorkspaceMemberConfig` MUST be defined contract-first (`Workspace.yaml` + new `WorkspaceMemberConfig.yaml`) with generated Go/TS types; `make verify-contracts` MUST pass.
- **FR-003** — The system MUST reject member_config writes whose key ∉ `CoreTeam` (422).
- **FR-004** — The system MUST enforce `interval_minutes ≥ 5` (422 otherwise).
- **FR-005** — The system MUST enforce a `body` size cap of **16 KB** (422 otherwise).
- **FR-005b** — The system MUST reject `enabled=true` with an empty `body` (422 "body required when enabled").
- **FR-006** *(amended A1/F-01)* — member_config writes use the **existing workspace-edit path with NO owner/admin gate** (#406); only validation (FR-003/004/005/005b) applies. (The earlier 403 requirement is dropped.)
- **FR-007** — Heartbeat reconcile MUST iterate `(workspace × member)` from `member_configs`, keyed `heartbeat:<ws>:<agent>`.
- **FR-007b** *(A2/F-02)* — The cron `JobSpec` MUST gain a `SessionID` field; reconcile MUST set it to the eager session's id so the run **continues** that session (not `NewScheduledSession`).
- **FR-007c** *(A2/F-06)* — Saving `member_configs` (the workspace PUT) MUST trigger a heartbeat reconcile in the same request path (it does not today).
- **FR-008** — A heartbeat run MUST use `member_configs[a].heartbeat.body` as the prompt body and MUST NOT read `agents/<a>/HEARTBEAT.md`.
- **FR-009** — Disabling a member heartbeat MUST remove its job on the next reconcile.
- **FR-010** *(amended A1/F-02, A2)* — When a heartbeat is **enabled** (during the workspace save), the system MUST **eagerly create** the standing session via a **workspace-aware** creation that stamps `type="heartbeat"` + `workspace_id` + agent (today's `NewScheduledSession(owner)` takes no workspace — add a variant), and store its id at `member_configs[agentId].heartbeat.session_id`. The cron job MUST continue that session via `JobSpec.SessionID` (FR-007b), not create its own.
- **FR-010b** *(A1/F-02)* — Re-enabling a heartbeat whose stored session was deleted (while disabled) MUST recreate the standing session.
- **FR-011** — The system MUST NOT auto-migrate per-agent heartbeats; effective heartbeat is OFF unless set per (ws,agent).
- **FR-012** — `SeedConfig` MUST NOT write agent-level heartbeat fields; legacy agent-level values MUST be ignored (logged once) at boot.
- **FR-013** — No production reader MUST consult `AgentConfig.HeartbeatEnabled/Interval` after cutover.
- **FR-014** *(amended A1/F-04, A2/G-01)* — `DELETE /sessions/{id}` MUST return 409 + audit when the session is a heartbeat standing session whose member is enabled. **Lookup:** load the session meta → if `type="heartbeat"`, take its `workspace_id` → load **that one workspace** → find the member whose `heartbeat.session_id == id` → 409 if `enabled` (bounded; no scan). Reads the config flag (always available) → **no fail-open/closed**. Deletable when `enabled` is false.
- **FR-015** — `DELETE` MUST succeed (200) for the standing session once its heartbeat is disabled, and for all non-heartbeat sessions (unchanged).
- **FR-016** *(amended A2/F-09)* — `AgentProfile` MUST render a Heartbeat tab **iff** opened with a workspace context, editing `member_configs[a].heartbeat`. The Heartbeat tab's save MUST be a **separate workspace mutation** (`PUT /workspaces/{id}`), NOT the agent autosave flow (`AgentProfile` is single-flow autosave today — the tab must opt out of it); the rest of `AgentProfile` is unchanged.
- **FR-017** — Heartbeat fields MUST be removed from the create wizard (Step 2) and the edit Personality tab.
- **FR-018** — `openEditAgentSlideOver` MUST take `workspaceId` as an **explicit parameter** (A5); the Team tab MUST pass it, and the global Agents screen MUST pass none (→ no Heartbeat tab).
- **FR-019** *(amended A1/F-03)* — A global **Settings → Memory** tab MUST read/write the global recap/retention settings (`auto_recap_enabled, idle_timeout_minutes, bootstrap_recap_enabled, bootstrap_recap_max_per_minute, bootstrap_recap_daily_budget_usd, recap_model_allow_list, session_days, memory_retros_days`) via a **dedicated `GET/PUT /api/v1/settings/memory`** that touches ONLY those fields (no merge of sibling config / secrets) — NOT `PUT /config`. Writable by **any authenticated user** (A2/G-02). New `MemorySettings` schema (contract-first).
- **FR-020** — Memory/recap settings MUST NOT appear on the workspace Heartbeat tab nor be scoped per-agent/per-workspace.
- **FR-021** *(amended A2/G-01)* — The Session panel MUST render a `type="heartbeat"` session pinned above all groups, badged, with its delete control disabled when the session's computed **`protected`** is true (FR-028).
- **FR-022** *(amended A1/F-05)* — Removing an agent from `CoreTeam` (in the workspace-edit handler) MUST, in the same operation, delete its `member_config`, remove its heartbeat job, and unprotect its standing session.
- **FR-023** *(amended A2/F-13)* — Deleting a workspace MUST release its members' heartbeat **cron jobs and standing sessions** (`handleWorkspaceDelete` cascades tasks/files only today — add cron + session release).
- **FR-024** *(amended A2/F-11)* — The `Session` contract `type` enum MUST add `"heartbeat"`; consumers MUST handle it. The regen MUST ship in the **same change** as the stamping code — the strict generated zod enum rejects the whole session payload on an unknown `type` (`rawToSession`'s fallback is dead).
- **FR-025** *(amended A2/F-08)* — Only **non-worker** agents MAY have a heartbeat config (predicate **`!IsWorker()`**, NOT `IsMain()` — Mia is `type=core`); worker member_configs heartbeat MUST be rejected/ignored.
- **FR-028** *(A2/G-01)* — `GET /sessions` MUST include a **computed `protected: bool`** per session, derived server-side from `member_configs.enabled` (single source of truth; not a stored field). The SPA uses it for pin + delete-disable.
- **FR-026** *(scope addition)* — The create wizard's soul field MUST offer a markdown-file upload (`.md/.markdown/.txt` → `payload.soul`), reusing the `UploadButton` pattern from `AgentProfile`.
- **FR-027** *(A1/F-10)* — Heartbeat fields (`heartbeat`, `heartbeat_enabled`, `heartbeat_interval`) MUST be **removed now** from `Agent.yaml` + `AgentCreateRequest.yaml` (contract-first regen); no deprecation window.

### Success Criteria
- **SC-001** — `make verify-contracts`, `gofmt -l`=0, `npm run typecheck`, the full Go gate, and vitest all pass on the branch.
- **SC-002** — A shared agent enabled in 2 workspaces produces exactly 2 jobs and 2 standing sessions (0 cross-talk).
- **SC-003** — 100% of DS-1 rows return their expected status; 0 invalid writes persisted.
- **SC-004** — DELETE on an active heartbeat session returns 409 in 100% of attempts; succeeds in 100% after disable.
- **SC-005** — After cutover boot, count of legacy `heartbeat:<agent>` jobs = 0.
- **SC-006** — In a fresh install, count of active heartbeats = 0 until a workspace opts in.
- **SC-007** — The Session panel shows the heartbeat session as the first item with a badge and no enabled delete control (verified in a real browser).
- **SC-008** — The Settings → Memory tab round-trips all 8 settings (write → reload → equal).
- **SC-009** — Existing SessionPanel/AgentProfile/workspace/cron/session test suites remain green (regression).

### Traceability Matrix
| FR | US | BDD Scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 | US-1 | store & read; independent config | T1, T9 |
| FR-002 | US-1 | (schema) | T9 |
| FR-003 | US-2 | unknown member rejected | T2, T10, DS-1.5 |
| FR-004 | US-2 | bounds validation | T2, T10, DS-1.3/4 |
| FR-005 | US-2 | bounds validation | T2, T10, DS-1.6 |
| FR-005b | US-2 | empty body rejected (E3) | T2, T10, DS-1.7 |
| FR-006 | US-2 | (no auth gate — validation only, A1/F-01) | T10 |
| FR-007 | US-3 | reconcile workspace-keyed; two jobs | T4, T11, DS-3 |
| FR-007b | US-7 | heartbeat uses config body (continues eager session) | T8, T11 |
| FR-007c | US-3 | reconcile on save | T11 |
| FR-008 | US-3 | heartbeat uses config body | T11 |
| FR-009 | US-3 | disabling removes job | T5, DS-3.4 |
| FR-010 | US-7,US-8 | eager session on enable; pinned | T8, T13 |
| FR-010b | US-7 | re-enable recreates session | T12 |
| FR-011 | US-4 | legacy ignored | T3, T15 |
| FR-012 | US-4 | seed writes none; legacy ignored | T6, T15 |
| FR-013 | US-4 | no legacy job | T15 |
| FR-014 | US-7 | delete blocked while active | T7, T12, DS-2.1 |
| FR-015 | US-7 | delete allowed; normal deletable | T12, DS-2.2/3 |
| FR-016 | US-5 | tab in ws context | T16 |
| FR-017 | US-5 | removed from create+personality | T16, T17 |
| FR-018 | US-5 | tab in ws context | T16, e2e T20 |
| FR-019 | US-6 | memory tab persists | T18, e2e T21 |
| FR-020 | US-6 | memory absent from ws tab | T16, T18 |
| FR-021 | US-8 | pinned | T19, e2e T20 |
| FR-022 | US-9 | GC on member removal | T14 |
| FR-023 | US-9 | workspace delete cascade | T14 |
| FR-024 | US-8 | pinned (data) | T13 |
| FR-028 | US-8 | computed protected on GET /sessions | T13, T19 |
| FR-025 | US-5 | (worker no tab) | T16, E9 |
| FR-026 | US-10 | soul md upload on the add screen | T22 |
| FR-027 | US-4 | heartbeat removed from Agent contract | T9, T15 |

## 9. Ambiguity Self-Audit

**GATE PASSED — all six resolved by the operator (2026-06-30):**

| # | Ambiguity | Resolved decision |
|---|---|---|
| A1 | `enabled=true` with empty `body` | **Reject** — 422 "body required when enabled" (FR-005b) |
| A2 | `body` size cap | **16 KB** (FR-005) |
| A3 | member_config write surface | **Extend `PUT /workspaces/{id}`**, triggered from the **Heartbeat tab inside the existing `AgentProfile` slide-over** — the tab is its own save unit (→ workspace mutation); the other tabs keep saving to the agent. Keep the original form (FR-016, FR-018) |
| A4 | session marker | **Reuse `Session.type`** — add `"heartbeat"` as a new value (NOT a separate `origin`). All `type`-branching code MUST handle it (FR-010, FR-024) |
| A5 | `workspaceId` source | **Pass explicitly** — `openEditAgentSlideOver(agentId, workspaceId)`; Team tab passes it, global Agents screen passes none → no tab (FR-018) |
| A6 | delete-guard when cron state unreadable | **Fail-open + WARN** — allow the delete; the session is recreated on the next reconcile (FR-014) |

## 10. Holdout Evaluation Scenarios *(post-implementation; NOT in traceability)*
- **H-HP1:** As an owner, enable a 5-minute heartbeat for Mia in workspace Alpha with body "summarise overnight CI"; observe a heartbeat session appear pinned in Alpha's Session panel and a run occur within ~5 min.
- **H-HP2:** Add the same agent to workspace Beta with the heartbeat OFF; confirm Alpha keeps firing and Beta never does.
- **H-HP3:** Open Settings → Memory, turn on auto-recap + set idle timeout 15; have a chat go idle; observe a `LAST_SESSION.md`/retro appear.
- **H-EP1:** Try to delete Alpha's pinned heartbeat session via the API while enabled → 409; via the UI → no delete control.
- **H-EP2:** PUT a member_config for an agent not on the team → rejected; nothing stored.
- **H-EC1:** Remove the agent from Alpha's team → its heartbeat stops, its session becomes deletable, and reconcile doesn't resurrect it.
- **H-EC2:** Configure the same agent in 3 workspaces with 3 different bodies → 3 distinct heartbeat sessions, each using its own body.

---

### Summary
- **Amendments A1 (§1.1) + A2 (§1.2) applied.** A1: no-auth-gate · eager session · dedicated memory endpoint · Agent-contract removal. A2 (post re-grill #2): `JobSpec.SessionID` job↔session link · bounded delete-guard lookup + **computed `protected`** · `!IsWorker()` predicate · reconcile-on-save · workspace-delete cron/session cascade · atomic `type` regen · separate Heartbeat save · `/settings/memory` any-auth.
- **User stories:** 10 (P0: US-1,2,3,4,7 · P1: US-5,6,8,9 · P2: US-10 soul-upload parity)
- **BDD scenarios:** 23 (HP 13 · AP 4 · EP 3 · EC 3) + 2 scenario outlines
- **Test datasets:** 3 (DS-1: 9 rows · DS-2: 4 · DS-3: 4) = 17 data rows
- **Functional requirements:** 32 · **Success criteria:** 9 · all in the traceability matrix
- **Regression:** addressed (SessionPanel/AgentProfile/workspace/cron/session suites must stay green; all `type`-branches must handle `"heartbeat"`)
- **Open items:** none — Ambiguity A1–A6 all resolved by the operator (see §9)
- **Holdout evals:** 7 (excluded from traceability)
