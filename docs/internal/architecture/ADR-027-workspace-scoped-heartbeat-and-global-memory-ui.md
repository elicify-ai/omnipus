# ADR-027 — Workspace-scoped heartbeat config + global memory settings UI

- **Status:** Proposed · ADR-grill (C-1…C-4) + **spec-grill (F-01/02/03/04/07/10) addressed** — see **Amendment A1**
- **Reviews:** `ADR-027-…-review.md` (ADR grill) · `../specs/workspace-heartbeat-memory-config-spec-review.md` (spec grill, verdict BLOCK)
- **Date:** 2026-06-30
- **Deciders:** Operator (Daniel) · Albert (architecture)
- **Supersedes scope of:** the per-agent heartbeat placement in `agent-form-requirements.md` §2/§4.9
- **Related:** the continuous-agent / heartbeat-model exploration (preview docs), the memory "sliding window + recall" decision (separate ADR pending)

---

## Amendment A1 — post spec-grill resolutions (2026-06-30)

The plan-spec was grilled (`../specs/workspace-heartbeat-memory-config-spec-review.md`, verdict BLOCK). Four findings were architectural; the operator resolved them. These **amend** the decisions below — where they conflict, **A1 wins**.

- **F-01 — Authorization (supersedes the D2 "owner/admin auth" bullet).** There is **no permission gate** on `member_configs` writes — they go through the existing workspace-edit path (#406: owner is attribution only, not an access gate). **Validation stays** (key ∈ `CoreTeam`, `interval_minutes ≥ 5`, `body ≤ 16 KB`, body-required-when-enabled); only the *auth gate* is dropped.
- **F-02 + F-04 — Session lifecycle & delete-protection (supersedes D4's cron-ref guard and the fail-open A6).** The standing session is created **eagerly, when the heartbeat is enabled in the form** (not lazily by the first cron tick). At creation it is stamped `workspace_id` + agent + `type="heartbeat"`, and its id is stored on `member_configs[agentId].heartbeat.session_id`. **Delete-protection follows the persisted `enabled` flag** (not a live cron read): `enabled=true` ⇒ 409 + trash hidden; `enabled=false` ⇒ deletable + trash returns; re-enable ⇒ protected again (recreate the session if it was deleted while disabled). This removes the fail-open/closed question entirely and gives F-02 its missing `workspace_id` mechanism. The cron job simply **continues** the pre-created session (`SessionModeContinue` using the stored `session_id`).
- **F-03 — Memory settings transport.** The Settings → Memory tab uses a **dedicated `GET/PUT /api/v1/settings/memory`** that reads/writes only the recap/retention fields — **not** the generic `PUT /config` (whose GET redacts secrets, so a read-modify-write would clobber provider keys).
- **F-10 — Agent contract.** The heartbeat fields are **removed immediately** from `Agent.yaml` + `AgentCreateRequest.yaml` (contract-first regen) — not deprecated.
- **F-07 (consequence of no-migration, D5).** Existing `agents/<id>/HEARTBEAT.md` **bodies are not carried forward**; operators re-enter the heartbeat body per workspace. Explicitly accepted.
- **F-08 / F-05 / F-09 (spec detail).** ~~Add an `IsMain()` predicate~~ → **A2:** use `!IsWorker()` (Mia is `type=core`); wire member-removal GC into the workspace-edit handler; the Heartbeat tab's save is a separate **workspace** mutation, the slide-over's other tabs save the **agent**.

### Amendment A2 — post re-grill #2 (2026-06-30)
The re-grill confirmed A1 closed 3/4 round-1 criticals; the remaining two were **mechanism** gaps (not decisions), resolved in the spec (§1.2):
- **Job↔session link:** add `cron.JobSpec.SessionID` so reconcile injects the eager session id and the run *continues* it (today `JobSpec` has no such field).
- **Delete-guard lookup:** the eager session carries `workspace_id`, so the guard loads that one workspace → the member whose `heartbeat.session_id == id` → 409 if enabled (bounded, no scan).
- **`protected` is computed live** from `member_configs.enabled` (single source of truth — not a stored flag) and exposed on `GET /sessions` for the SPA's pin + delete-disable.
- **`/settings/memory`** is writable by **any authenticated user** (operator decision).
- Saving member_configs **triggers reconcile**; **workspace delete** also releases members' cron jobs + standing sessions; the `Session.type+="heartbeat"` regen ships **atomically** with the stamping code.
No further ADR-level decisions; the rest is implementation in plan-spec.

## 1. Context

Today, heartbeat and memory configuration are scoped wrongly for where the product is going (shared agents across workspaces, v0.3). Grounded current state:

- `[FACT]` **Heartbeat is per-agent.** `AgentConfig.HeartbeatEnabled *bool` + `HeartbeatInterval int` (minutes) in `pkg/config/config.go`; edited in the agent wizard **Step 2 (Personality)** (`src/components/agents/wizard/Step2Personality.tsx`); on the wire in `contracts/components/schemas/Agent.yaml` and `AgentCreateRequest.yaml` (`heartbeat`, `heartbeat_enabled`, `heartbeat_interval` seconds).
- `[FACT]` **The agent dialog has no workspace context.** It is opened only from the global Agents screen (`AgentListScreen.tsx`); `openCreateAgentModal` (`src/store/ui.ts`) carries no workspace id.
- `[FACT]` **Memory/recap settings are global-only and have no UI.** `AgentDefaults.{AutoRecapEnabled, IdleTimeoutMinutes, BootstrapRecap*, RecapModelAllowList}` and `OmnipusRetentionConfig.{SessionDays, MemoryRetrosDays}` (`pkg/config/config.go`). Confirmed dormant on the running instance (`auto_recap_enabled=false`, `idle_timeout_minutes=0` → zero recaps/retros ever produced).
- `[FACT]` **Workspace ↔ agent is a flat list.** `Workspace.CoreTeam []string` (`pkg/workspace/workspace.go`); an agent carries a single `Workspace` string. **No per-(workspace,agent) config object exists.**
- `[FACT]` **Heartbeat already resolves the agent's workspace** (`computeDesiredHeartbeats → agentWorkspace(ac.ID)`, `pkg/gateway/heartbeat_schedule.go`) and runs in a `SessionModeContinue` scheduled session (`pickSession`, `pkg/gateway/schedules.go`). **No protected/undeletable-session concept exists**; `deleteSession` (`pkg/gateway/rest.go`) is unguarded.

## 2. Decision Drivers

1. **Agents are shared across workspaces** (operator-confirmed; matches v0.3 shared-agents / marketplace direction). The same agent must run a *different* heartbeat in workspace A vs B.
2. Heartbeat is conceptually a **workspace activity** ("heartbeat is always in a workspace context").
3. Memory/recap settings are **global concerns** (operator-confirmed: "no per-agent setting, only global under settings") — they govern cost and retention, not per-workspace behaviour.
4. The agent-creation form must stop being the home of heartbeat config.
5. An active heartbeat must have a **stable, continued, undeletable session**.
6. **Constraints:** single Go binary; contract-first wire formats (`contracts/*` → generated types, Constraint #8); backward-compatible migration of existing per-agent heartbeats; deny-by-default / cost-safety.

## 3. Considered Options

### Config scope (resolved by operator)
- **(A) Heartbeat per-(workspace,agent) on the Workspace; memory stays global with a Settings UI** — **CHOSEN.**
- (B) Combined per-(workspace,agent) "Memory & Heartbeat" tab holding *both* heartbeat and memory — *original brief; rejected by operator* (memory is a global concern, not per-workspace).
- (C) Keep heartbeat per-agent, add per-workspace override on the Agent (`Agent.workspace_configs`) — *rejected*: scatters workspace data onto agents, contradicts "heartbeat lives on the workspace".
- (D) New dedicated `/workspaces/{id}/agents/{agentId}/config` resource — *rejected for now*: more API surface than warranted; the data is small and workspace-owned.

### Undeletable session mechanism
- **(A) Cron-job-reference guard** — `deleteSession` checks whether an *enabled* heartbeat job references this `SessionID` — **CHOSEN.** Source-of-truth, no new persisted state.
- (B) `protected: bool` flag on session metadata — *rejected*: a second source of truth that can drift from the job's enabled state.

## 4. Decision

A **split** model. Heartbeat becomes workspace-owned and per-member; memory stays global and finally gets a UI.

### D0 — Terminology (resolves grill C-1)
"Workspace" is overloaded in the code and the un-revised draft conflated two things:
- **The agent home dir** `OMNIPUS_HOME/agents/<id>/` — where heartbeat reads `HEARTBEAT.md` **today** via `agentWorkspacePath()` (`pkg/gateway/rest.go:1191`). This is per-agent, not per-workspace.
- **The Workspace record** `pkg/workspace.Workspace` (metadata JSON, `CoreTeam`, etc.) — the real "workspace".

**This ADR puts ALL heartbeat config — enabled, interval, AND the HEARTBEAT.md body — on the Workspace record**, keyed per member. There is **no per-(workspace,agent) filesystem `HEARTBEAT.md`**; the body is a string field in `member_configs[agentId].heartbeat.body`. The legacy `agents/<id>/HEARTBEAT.md` read path is decommissioned (D5).

**Operator decision (gap #1 closed):** the HEARTBEAT.md body **is per-(workspace, agent)** — the same shared agent runs a *different* heartbeat prompt in each workspace. It lives in that workspace's member config, not in the agent.

### D1 — Config scope (split)
- **Heartbeat config is per-(workspace, agent).** `[Confidence: High · operator-confirmed]`
- **Memory/recap config stays global** (unchanged scope), surfaced in a new global **Settings → Memory** tab. **Not** per-agent, **not** per-workspace. `[Confidence: High · operator-confirmed; supersedes the original brief]`

### D2 — Storage & wire contract
- **Heartbeat → on the Workspace record** (see D0). Extend it with a per-member config map:
  `Workspace.member_configs: { "<agentId>": WorkspaceMemberConfig }`, where
  `WorkspaceMemberConfig.heartbeat = { enabled: bool, interval_minutes: int(≥5), body: string }`.
  The **`body` is the per-(workspace,agent) HEARTBEAT.md** (operator-decided). `[Confidence: High]`
- **Bounds (resolves grill C-3):** map keys MUST be ∈ `Workspace.CoreTeam` (reject unknown / removed agentIds; GC entries on member removal); `interval_minutes ≥ 5`; `body` ≤ 16 KB; body-required-when-enabled. Writes go through the **existing workspace-edit path — no owner gate** (Amendment A1/F-01). Plus the eager-created session's id is stored at `member_configs[agentId].heartbeat.session_id`. `[Confidence: High]`
- **Contract-first (Constraint #8):** add `contracts/components/schemas/WorkspaceMemberConfig.yaml`; reference it as the `additionalProperties` value of `member_configs` in `Workspace.yaml`; regenerate. No hand-written map/value type. `[Confidence: High]`
- **Memory → unchanged storage** (`agents.defaults.*`, `storage.retention`). The Settings → Memory tab reads/writes the **existing global config** surface, not a new scope. `[Confidence: High]`

### D3 — UI: conditional agent **edit** form + global memory tab
- **Remove heartbeat from both agent forms' default path:** the create wizard Step 2 (`Step2Personality.tsx`) and the edit form's **Personality** tab (`AgentProfile.tsx` — "Enable heartbeat" lives there today, ~L1075). `[Confidence: High]`
- **The entry point already exists** `[FACT · operator-confirmed + grounded]`. The workspace **Team tab** (`WorkspaceTeamTab.tsx`, route `/workspaces/$workspaceId/team`) already opens the agent **edit slide-over** `AgentProfile` via `openEditAgentSlideOver(agentId)` (`store/ui.ts`). That slide-over currently carries **only `agentId`, no workspace**. The single change: make it workspace-aware — thread `workspaceId` into `openEditAgentSlideOver` (or derive it from the active `/workspaces/$workspaceId/*` route). `[Confidence: High]`
- **Conditional Heartbeat tab.** `AgentProfile` is already tabbed (Basics · Personality · Tools · Runtime · Advanced). Add a **6th "Heartbeat" tab rendered only when a workspace context is present** (i.e. opened from the Team tab). It edits `member_configs[agentId].heartbeat` for that (workspace, agent). Opened from the global Agents screen (no workspace) → the tab is absent. `[Confidence: High]`
- **Add a global Settings → Memory tab** for the existing recap/retention/memory settings (their first UI). `[Confidence: High]`
- Naming: the workspace tab is **"Heartbeat"** (memory has moved to global Settings). `[Confidence: Medium]`

### D4 — The default heartbeat session (per-(ws,agent), undeletable while active)
> **Superseded by Amendment A1 (F-02+F-04):** the session is created **eagerly on enable**, stamped `workspace_id`+agent+`type="heartbeat"`, linked via `member_configs[agentId].heartbeat.session_id`; **protection follows the persisted `enabled` flag** (config-driven), not a live cron-job reference. The bullets below are retained for the reasoning trail.
- Each enabled (workspace, agent) heartbeat owns **one standing session**, workspace-scoped (carries `workspace_id` + the agent), continued across runs (already `SessionModeContinue`). `[Confidence: High]`
- ~~**Undeletable while active:** guard `deleteSession` — if an *enabled* heartbeat cron job references this `SessionID`, return **409**. The cron job is the source of truth.~~ → **A1:** guard checks the persisted `member_configs…enabled` flag (always readable; no fail-open/closed question).
- Disabling the heartbeat releases the protection, after which the session is deletable. `[Confidence: High]`

### D5 — Resolution, NO-migration, decommission (revised after grill)
- **Effective heartbeat** = `workspace.member_configs[agentId].heartbeat`; absent ⇒ OFF (deny-by-default). `[Confidence: High]`
- **No automatic migration (resolves grill C-2).** "The agent's current workspace" is undefined: agents are **many-to-many** with workspaces, and `AgentConfig.Workspace` is a free-text *path*, not a `Workspace.id` FK. So existing per-agent heartbeats are **NOT auto-migrated**; in the new model every (workspace, agent) heartbeat starts OFF and the operator opts in per workspace. (The only live heartbeat — Mia's — is effectively dormant and maps to no `Workspace.id`; nothing of value is lost.) `[Confidence: Medium-High]`
- **Decommission the agent-level path (resolves grill C-4).** Redirect **all** readers of `AgentConfig.HeartbeatEnabled/Interval` (~15+ sites in `pkg/gateway/rest.go` + reconcile) to `member_configs`, and **stop `coreagent.SeedConfig` seeding agent-level heartbeat** (`pkg/coreagent/core.go:776,835`) — else Mia's seed keeps writing fields nothing reads and her heartbeat silently dies post-cutover. Agent-level heartbeat fields are removed from the forms now and from the read path at cutover (dead config tolerated one release, never read). `[Confidence: Medium-High]`
- Memory/recap: no migration — same global config, new UI. `[Confidence: High]`

### D6 — Heartbeat reconcile becomes workspace-aware
- `computeDesiredHeartbeats` / `ReconcileHeartbeatSchedules` (`pkg/gateway/heartbeat_schedule.go`) iterate **(workspace × member)** from `Workspace.member_configs`, not the flat `cfg.Agents.List`. The heartbeat **prompt body is read from `member_configs[agentId].heartbeat.body`** (per D0/C-1), **not** from `agentWorkspacePath()/HEARTBEAT.md` (`rest.go:1191`). Job key = `heartbeat:<workspaceId>:<agentId>`; the standing session carries `workspace_id`. `[Confidence: Medium-High]`
- **No in-place rekey (resolves grill M-2).** Because there is no migration (D5), heartbeats are created **fresh** in the new keyspace on first opt-in — there is no legacy `heartbeat:<agent>` job whose `SessionID` must survive a rename. At cutover, legacy per-agent heartbeat jobs are **deleted**, not renamed; the new standing session is created on first enable. `[Confidence: Medium]`

### D7 — The heartbeat session is pinned to the top of the Session panel
[2026-07-16: SessionPanel.tsx was removed; session-list UI now lives in SearchModal + the sidebar accordion — retarget accordingly]
- In the Session panel (`src/components/chat/SessionPanel.tsx` — the workspace → agent nested view), the active workspace's heartbeat **standing session is rendered pinned at the very top**, above the normal groups, visually distinct (a heartbeat badge/icon), and reflecting D4: its **delete affordance is disabled while the heartbeat is active** (the server already returns 409; the SPA hides/disables the trash to match). `[Confidence: Medium]`
- **Identification.** The SPA must know which session is the heartbeat session for a (workspace, agent). Recommend marking it on the wire — a `kind: "heartbeat"` / `origin: "heartbeat"` field on the session list item, resolved from the heartbeat cron job's `SessionID` — rather than the SPA inferring from `type=scheduled`. `[Confidence: Medium]`
- Depends on D4: the heartbeat session must carry the workspace's `workspace_id` so it groups under the workspace (not today's "No workspace" bucket) before it floats to the top. `[Confidence: Medium-High]`

## 5. Consequences

**Positive**
- Correct scope for shared agents: one agent, distinct heartbeat per workspace.
- Heartbeat config and its session are co-located on the workspace — coherent lifecycle, and the undeletable session has an obvious owner.
- Memory/recap finally has a UI (today it's headless and dormant), without over-scoping cost/retention to per-workspace.
- The agent form gets simpler; heartbeat stops being a per-agent property that ignores workspace.

**Negative / cost**
- Net-new: a per-member workspace config object (storage + contract + generated types), a workspace-context entry point for the agent dialog, two UI surfaces, a migration, and a delete-guard.
- A deprecation window for the agent-level heartbeat wire fields.
- Job key/identity change for heartbeats (reconcile + any references) — must migrate cleanly to avoid orphan/duplicate jobs.

## 6. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Legacy per-agent heartbeat jobs orphaned / double-firing at cutover | No migration (D5): **delete** every legacy `heartbeat:<agent>` job at cutover; new jobs only on per-(ws,agent) opt-in |
| `SeedConfig` keeps writing agent-level heartbeat → Mia's heartbeat silently dies (C-4) | Stop seeding heartbeat in `coreagent.SeedConfig`; redirect all ~15+ agent-level readers to `member_configs` in the **same** change |
| Delete-guard: O(n) job scan, TOCTOU, no audit (grill M-3) | N is small (few Main heartbeats); re-check under the cron read; **emit an audit entry on the 409**; add a `SessionID→job` index only if N grows |
| Shared agent in many workspaces multiplies heartbeat sessions/cost | Opt-in per (ws,agent), OFF by default; cost/safety guards stay **global** (rate-limits, budgets, recap allow-list) |
| Unbounded `member_configs` writes (grill C-3) | Keys ∈ `CoreTeam`; interval ≥ 5m; body ≤ 16 KB; body-required-when-enabled; GC on member removal. **No auth gate** (A1/F-01) |
| Contract drift (Constraint #8) | `WorkspaceMemberConfig.yaml` first → regenerate → `make verify-contracts`; no hand-written wire structs |

## 7. Gaps & Ambiguities

1. ~~**Heartbeat prompt body scope.**~~ **RESOLVED (operator):** the HEARTBEAT.md body is **per-(workspace, agent)**, stored as `member_configs[agentId].heartbeat.body` on the Workspace record (D0/D2). No agent-level body; no filesystem `HEARTBEAT.md` for the pairing.
2. ~~**Workspace-context entry point UX.**~~ **RESOLVED (operator + grounded):** the workspace **Team tab** already opens the agent edit slide-over (`AgentProfile` via `openEditAgentSlideOver`). Only work: thread `workspaceId` into that slide-over (param or route-derived) so the Heartbeat tab renders. No new entry point.
3. **Membership source of truth.** `Workspace.CoreTeam` lists agent IDs; is membership edited there, and does `member_configs` key strictly to `CoreTeam`? Need to define add/remove-member semantics (config GC on removal).
4. **Edit vs create.** Heartbeat is removed from *create*; confirm the per-(ws,agent) heartbeat is only editable *after* the agent is a workspace member (create agent → add to workspace → configure heartbeat).
5. **Continuous-agent execution model.** This ADR fixes *config scope + session lifecycle*, not the heartbeat *execution* model (Mission Loop / Resident / ledger). Those remain a separate decision; this ADR's per-(ws,agent) standing session is the substrate they'd build on.

## 8. Decision Confidence (summary)

| Decision | Confidence | Basis | Missing evidence |
|---|---|---|---|
| D1 scope split | High | Operator-confirmed | — |
| D2 storage on Workspace | High | Operator-confirmed | exact endpoint shape (PUT vs sub-path) |
| D3 conditional edit-form tab + Settings→Memory | High | Operator-confirmed + grounded (entry point exists) | — |
| D4 undeletable session (cron-ref guard) | Medium | `[EXPERT REASONING]` + grounded cron facts | race-window validation |
| D5 migration | Medium-High | grounded config facts | deprecation timeline |
| D6 workspace-aware reconcile | Medium-High | grounded heartbeat_schedule facts | job-key migration test |
| D7 heartbeat session pinned in Session panel | Medium | operator requirement + grounded SessionPanel | session `kind` wire field |

## 9. Next Steps & Handoff

1. **Confirm the 5 Gaps** (esp. #1 body scope, #2 entry-point UX).
2. **Red-team this ADR:** `/grill-spec docs/internal/architecture/ADR-027-workspace-scoped-heartbeat-and-global-memory-ui.md` (generic-markdown mode) — high-value since this changes wire contracts and session-deletion semantics.
3. **Then spec it:** `/plan-spec docs/internal/architecture/ADR-027-workspace-scoped-heartbeat-and-global-memory-ui.md` — produce the contract changes (`WorkspaceMemberConfig.yaml`), the migration, the delete-guard, and the two UI surfaces as testable user stories.
4. Sequence note: land the **config/scope/session** change (this ADR) before the heartbeat *execution-model* rework, since the per-(ws,agent) standing session is its substrate.
