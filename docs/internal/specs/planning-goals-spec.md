# Feature Specification: Planning & Goals

**Created**: 2026-07-19
**Status**: Draft
**Input**: ADR-049 (`docs/internal/architecture/ADR-049-planning-goals-system-agents.md`, grill-PASSED r3) — ratified operator decisions D1–D8. This spec resolves ADR §3 gaps 1–8 as spec decisions and covers the §6 contract-surface table (Constraint #8).
**Branch**: `feature/planning-goals` (from `release/v0.1.1`; ships IN v0.1.1 per ADR §2 Constraints).
**Pipeline position**: albert ADR (PASS) → **this plan-spec** → grill-spec (≤3 rounds, operator cap) → parallel-wave implementation → 2× 7-reviewer + grill-code rounds → delivery. No `/taskify` (operator direction).

---

## Structure & Reading Order

This spec is organized into three self-contained domain **Parts**, each carrying its own user stories, BDD scenarios, TDD plan, test datasets, functional requirements, success criteria, and spec decisions, preceded by cross-cutting sections and followed by a consolidated index:

- **Part A — Data Model, Contracts & Milestone Migration** (US-1..4, FR-001..039, SC-001..015, SD-A*): the `pkg/plan` entity, `Task` tags/plan_id/criteria, AcceptanceCriterion/EvidenceRecord/JudgeVerdict, the 15-row contract-surface table, Milestone removal + migration, Judge seeding + config bounds.
- **Part B — Runtime Engine, Judge, Goal Loops & Commands** (US-5..9, FR-040..079, SC-020..039, SD-B*): TaskExecutor goal-loop rework, evidence-ladder judge execution, the hybrid plan engine, `/goal` and `/loop` commands, and the cross-cutting enforcement (origin gating, SEC-26 narrowing, global loop cap).
- **Part C — SPA / UX Surfaces** (US-10..13, FR-080..099, SC-040..049, SD-C*): board plan/tag filters, plan lifecycle UI, criteria/evidence UX, `/goal`+`/loop` chat surfaces, Agents-screen System section, ActivityPanel judge transparency.

Numbering ranges are disjoint across parts by design, so the parts compose without collision. The **Consolidated Cross-Part Index** at the end reconciles traceability, resolves cross-part dependencies, and merges the ambiguity/spec-decision registers.

The per-part "Behavioral Contract", "Edge Cases", "Explicit Non-Behaviors", and "Integration Boundaries" sections are **domain-scoped**. The cross-cutting versions immediately below govern the epic as a whole; on any apparent conflict, the cross-cutting statement and the ADR win.

---

## Cross-Cutting Behavioral Contract

Primary flows:
- When an agent creates tasks with `blocked_by` edges and a `plan_id`, the plan names that DAG and the board can filter to exactly its task chain (D1).
- When a plan is approved with a DoD and every member task has ≥1 criterion, the engine dispatches ready tasks as dependencies clear, with no agent-coordinator in the happy path (D4).
- When a task attempt ends, machine-check criteria run through the assignee's `bash` machinery and prose criteria are judged by the Judge System Agent; all met ⇒ task done; any unmet ⇒ re-dispatch with per-criterion reasons as steering (D2).
- When attempts are exhausted (default 3), the plan owner agent is woken with failure context to decide replan/split/give-up (D4/D7).
- When all plan tasks are done, the plan-level judge evaluates the plan DoD; met ⇒ plan done + owner synthesis; unmet ⇒ owner woken with reasons (D4).
- When a user sends `/goal <condition>`, a session goal loop starts: after each round the judge evaluates; unmet reason steers the next round; met clears the goal (D6).
- When a user sends `/loop 15m <prompt>`, a cron `every` continue-session job re-fires the prompt; `/loop <prompt>` self-paces via one-shot `at` jobs with stated delays (D6).
- When any brake fires (attempts/rounds/runs/idle-expiry/global-cap), the loop finishes its current step and writes a handover (progress, remaining, blockers) to the record and owning session (D7/NFR-3).

Error flows:
- When a worker emits no parseable completion marker, the attempt is judged on criteria alone; absence of evidence for a criterion ⇒ unmet (NFR-2, fail-closed, extends ADR-043).
- When the judge call itself is unavailable (throttled/capped/provider-error/timeout), the loop pauses with cron backoff (60/120/300s); the attempt is NOT consumed; status shows the pause (D7 r2).
- When a machine check times out (60s) or the assignee `bash` policy resolves deny (incl. `ask` unattended), the criterion fails closed (D2).
- When an agent tool call creates/updates a task without ≥1 criterion (or reduces below 1), it is rejected with a listed error (D5).
- When an agent tool call sets all-machine criteria for an assignee whose `bash` policy is deny or ask, it is rejected as structurally unsatisfiable; the UI warns instead (D2 rule 5).
- When a plan-approve request finds member tasks without criteria, it returns 400 listing each offending task (D5).
- When deletion is requested for an agent owning active plans/loops, it is rejected 400 until reassigned/stopped; disable pauses loops and surfaces a blocked state (D4 r2).

Boundary conditions:
- When the 16th concurrent loop is active, starting a 17th is rejected with `active loops: 16/16`; stopping one frees the slot (D7 r2).
- When a loop is idle 7 days, the sweeper expires it with a handover; activity resets the clock; judge-unavailability pauses do NOT reset it (D7).
- When a cron- or async-injected message begins with `/goal` or `/loop`, it is inert text (origin gating); only user-originated messages start loops (Gap #8 r2).
- When migration encounters milestone names colliding post-normalization, tags are disambiguated deterministically by milestone-ID order and re-checked ≤64 chars (D1 r2/r3).

---

## Cross-Cutting Explicit Non-Behaviors

- The system must not enforce token/money budgets on any loop — count and calendar brakes only; spend is displayed, never enforcing (NFR-1, operator explicit).
- The system must not run machine checks through any path other than the assignee agent's existing `bash` tool machinery — no judge-owned exec path, ever (D2 rule 1).
- The system must not let the worker's own `TASK_STATUS: success` marker complete a task without the judge pass — the marker is a claim, not a verdict (D2).
- The system must not treat judge unavailability as a failed verdict — no attempt consumption on judge-infrastructure failure (D7 r2).
- The system must not allow type-`system` agents to be created via REST/tools, deleted, disabled, set as default, targeted by routing bindings, or enumerated in delegation/team pickers (D3 r1–r3).
- The system must not grant type-`system` agents SEC-26 rate-limit/cost-cap exemptions — `IsPrivilegedAgent` is `core`-only after this epic (D3/CRIT-002).
- The system must not auto-approve plans — approval is an explicit act recorded on the plan (D4).
- The system must not start goals/loops from non-user-originated messages (cron/system/async/delegated origins) (Gap #8).
- The system must not preserve Milestone entities, endpoints, or wire schemas after migration — no back-compat layer (D1, ADR-035/037 precedent).
- The system must not persist unredacted evidence — registered sensitive values are redacted before write (MAJ-003).
- The system must not add a workspace-level bounds layer in v1 — global + per-entity only (FR-9 r1/MIN-004).
- The system must not block or modify Scratchpad (`set_todos`) tasks with criteria requirements — they are exempt from goal-loop execution entirely (D5).

---

## Cross-Cutting Integration Boundaries

### LLM providers (judge calls)
- **Data in**: judge rubric (Judge System Agent system prompt), criteria, evidence records, file diffs, worker summary (last).
- **Data out**: structured per-criterion `{criterion_id, met, reason}` + overall verdict.
- **Contract**: same provider client stack as agent turns; no-tools structured call under `agent_id = judge` with plan/task/goal correlation IDs; subject to SEC-26.
- **On failure**: pause + backoff 60/120/300s; attempt not consumed; surfaced in status (D7 r2); repeated failure ends via idle expiry.
- **Development**: mock provider in Go tests; real provider in E2E.

### Cron service (`pkg/cron`)
- **Data in**: `/loop` interval (`every`, continue-session) + self-paced one-shot `at` jobs; idle-expiry sweeper; judge-retry wakeups.
- **Data out**: `ScheduledRunner.RunScheduled` fires into the agent loop with origin metadata marking runs non-user (origin gating).
- **On failure**: cron retry/backoff + `ConsecutiveFailures` alerting; loops survive restart via persisted jobs + boot reconciliation.
- **Development**: fake-clock harness (`pkg/cron/autonomy_test.go`).

### Sandbox / tool-policy engine
- **Data in**: machine-check commands dispatched as assignee `bash` tool calls (policy, Landlock/seccomp, env allowlist, audit — by construction).
- **Data out**: exit code, stdout/stderr (capped, redacted) as EvidenceRecord.
- **On failure**: deny/ask ⇒ criterion fails closed; 60s timeout ⇒ fails closed; audit entry always written.
- **Development**: sandbox fallback backend in tests; security tests assert in-sandbox execution + fail-closed paths.

### MessageBus / WS gateway
- **Data in**: `/goal`, `/loop` as ordinary `message` frames (server-side parse; palette is a web nicety).
- **Data out**: `goal_status`/`loop_status`/`plan_status` frames + extended `task_status_changed` (SPA zod-validates at edge, invalidates queries).
- **On failure**: WS drop ⇒ SPA reconnect + query refetch (existing pattern); loops are server-side and unaffected.
- **Development**: embedded-SPA E2E against the Go binary (never the Vite proxy).

---

---

# Part A — Data Model, Contracts & Milestone Migration

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Location |
|--------|------|----------|
| `task.Task` | Unified on-disk task entity; gains `PlanID` + `Tags` + `Criteria`; loses `MilestoneID` | `pkg/task/task.go:191-255` (`MilestoneID` at `:220`) |
| `task.Store` | Per-entity JSON store, striped-lock + atomic-write pattern the Plan store mirrors | `pkg/task/store.go:54-121` (`New` `:64`, `write` `:109`, `Create` `:393`, `Update` `:501`, `normalize` `:248`) |
| `task.Filter` | List filter; `MilestoneID` field removed (`:137`,`:163`), `PlanID`/`Tag` added | `pkg/task/store.go:132-179` |
| `task.Patch` | Update patch; `MilestoneID` removed (`:441`,`:623-625`), `PlanID`/`Tags`/`Criteria` added | `pkg/task/store.go:429-456` |
| blocked_by DAG | Cycle/self/depth validation + auto-advance; **unchanged** (regression) | `pkg/task/blocked_by.go` (`validateBlockedByLocked` `:55`, `AdvanceBlockedDependents` `:181`, `cascadeDeleteEdges` `:282`, `DropOrphanEdges` `:389`, `maxBlockedByDepth=50` `:34`) |
| `milestone` + handlers | Entire inline Milestone store + REST surface — **removed** | `pkg/gateway/rest_milestones.go:32-751` (`//go:build !cgo` `:1`; `HandleMilestones` `:699`; `computeMilestoneCounts` `:153`; `clearMilestoneOnTasks` `:189`) |
| Milestone route dispatch | `/milestones` sub-route — **removed** | `pkg/gateway/rest_workspaces.go:499-501` |
| `coreagent.SeedConfig` | Seeds core agents; gains System-Agent (Judge) seeding | `pkg/coreagent/core.go:888-1057`; `coreAgentSeed` `:367`; `All()` `:97`; `CoreAgentID` `:27-52` |
| `security.IsPrivilegedAgent` | Currently `core`||`system`; **narrow to `core` only** (ADR D3) | `pkg/security/ratelimit.go:17-22` |
| `config.ValidateToolPolicyCoverage` | Boot/write agent×tool coverage gate; Judge must be covered | `pkg/config/validate.go:491` |
| `config.Config` | Root config; gains `Planning PlanningConfig` section | `pkg/config/config.go:126-178`; defaults `pkg/config/defaults.go:15` |
| `config.OmnipusSandboxConfig` | Precedent for a bounded config block with boot-validated defaults | `pkg/config/sandbox.go:167` |
| raw-body-sniff 400 | ADR-035/037 precedent for rejecting retired/forbidden fields | `pkg/gateway/rest.go:2775` (`sandbox_profile`), `:2780` (`delegation_policy`) |
| same-workspace guard | Precedent for `PlanID`/blocker workspace validation | `pkg/tools/task.go` `validateBlockersWorkspace` `:218-232` |
| `config.RegisterSensitiveValues` | Evidence redaction source before persistence | `pkg/config/security.go:69` |
| `session.EntryType` | Transcript entry classification; gains `judge_verdict` | `pkg/session/daypartition.go:26-40` |
| `UnifiedMeta` / `MetaPatch.TaskID` | Precedent for goal/loop session-state fields | `pkg/session/unified.go` |

### Cluster Placement

New **`pkg/plan`** package (Plan entity + store), mirroring `pkg/task`. Touches the **task**, **config**, **coreagent**, **gateway/contracts**, and **session** clusters. Milestone removal deletes the milestone slice of the **gateway** cluster.

---

## User Stories & Acceptance Criteria

### User Story 1 — Plan container groups an executable task DAG (Priority: P0)

An operator (and the Orchestrator agent) wants a first-class **Plan** that names and groups a `blocked_by` task DAG with a goal, a Definition-of-Done, a state machine (`draft → approved → running → done/failed`), and an owner agent, so the board can show and run "the task chain belonging to plan X" instead of only a flat Milestone date-bucket (`pkg/task/task.go:220`). Today no Plan struct/schema/store/REST path exists (ADR §1.1).

**Why this priority**: The Plan entity is the home for the plan-level judge, DoD, owner, and state machine (ADR D1). Nothing else in the epic — plan engine, plan judge, board-by-plan — can exist without it. It is the load-bearing schema.

**Independent Test**: Create a Plan via REST, attach two tasks by `plan_id`, list the plan's members, drive it through `draft→approved→running→done`, and confirm persistence at `~/.omnipus/plans/<id>.json` — deliverable value even before the engine wraps executions.

**Acceptance Scenarios**:
1. **Given** an existing workspace, **When** a Plan is created with a title, goal, owner agent, and ≥1 DoD criterion, **Then** it persists to `~/.omnipus/plans/<id>.json` in state `draft` with server-set `created_at`/`updated_at`/`owner`/`created_by`.
2. **Given** a `draft` Plan, **When** a task in the same workspace is created/updated with that `plan_id`, **Then** the write succeeds and the task appears in the plan's member list.
3. **Given** a task whose workspace differs from the Plan's, **When** it is written with that `plan_id`, **Then** the write is rejected 400 "plan is in a different workspace" (mirrors `pkg/tools/task.go:218-232`).
4. **Given** a `running` Plan, **When** a `running → done` transition is requested, **Then** it succeeds; a `draft → running` transition is rejected 400 "illegal plan transition".
5. **Given** a Plan with member tasks in `running` state, **When** a client DELETE is issued, **Then** it is rejected 409/400 (must stop first); deleting a non-running Plan clears `plan_id` on member tasks (SD-A5).

### User Story 2 — Milestones replaced by workspace-scoped multi-tags, migrated losslessly (Priority: P0)

An operator wants generic **multi-tags** on tasks (releases/milestones/themes) instead of the single flat `MilestoneID`, and wants existing Milestones migrated with **no data loss** (name → `milestone:<name>` tag, `due_date` preserved onto member tasks), so the shipped Milestone feature can be removed cleanly (ADR D1, FR-2). Milestones are a shipped one-way-door removal (`pkg/gateway/rest_milestones.go`; ADR §7).

**Why this priority**: Milestone removal is a hard prerequisite for the Plan container (they occupy the same "grouping" slot) and is a one-way door that must be right the first time. Migration correctness is release-blocking (ADR §9.4).

**Independent Test**: Seed `~/.omnipus/milestones/*.json` + tasks referencing them, boot, and assert every task gained the right `milestone:<name>` tag, `due_date` copied onto empty `Due`, empty milestones logged, and a second boot is a no-op.

**Acceptance Scenarios**:
1. **Given** a milestone "Q3 Release" with two member tasks, **When** the migration runs at task-store load, **Then** both tasks gain tag `milestone:q3 release` (normalized lowercase+trim) and the milestone's `due_date` is copied into each task's `Due` only where `Due` was empty.
2. **Given** two milestones normalizing to the same tag ("Q3"/"q3"), **When** migration runs, **Then** the first (by milestone-ID order) yields `milestone:q3` and the second `milestone:q3-2`, uniqueness re-checked after suffixing (ADR D1 r2/r3).
3. **Given** a milestone with no member tasks, **When** migration runs, **Then** a migration-log entry records its name + `due_date` (a tag cannot exist unattached) and no task is touched.
4. **Given** a completed migration, **When** the gateway boots again, **Then** migration is a no-op (guarded by a completion sentinel, SD-A6) and no tag is duplicated.
5. **Given** the process crashes mid-migration, **When** it re-runs, **Then** the result is identical to a single clean run (idempotent tag-add + `Due`-copy-only-if-empty + deterministic suffixing).

### User Story 3 — Tasks carry acceptance criteria with recorded evidence (Priority: P0)

An operator/agent wants each task to carry **acceptance criteria** — machine-checkable (`bash` command + expected exit code, evidence recorded) and prose (LLM-judged) — each recording its **author identity** and per-run status, so completion is judged on unfakeable evidence rather than self-certification (ADR D2, FR-3/FR-6).

**Why this priority**: The evidence ladder is the ADR's #1 correctness mechanism (workers claiming done, §4). Criteria + evidence + verdict are the schema the judge consumes; without them the judge has nothing to read.

**Independent Test**: Create a task with one `check` and one `prose` criterion, run a check attempt, and assert an `EvidenceRecord` is persisted under `$OMNIPUS_HOME` (redacted, size-capped) and a `JudgeVerdict` carries per-criterion `{met, reason}`.

**Acceptance Scenarios**:
1. **Given** an agent-tool `create_task`, **When** the request carries zero criteria, **Then** it is rejected 400 "≥1 acceptance criterion required" (ADR D5; human/UI creation is soft, SD-A7).
2. **Given** a `check` criterion whose command emits a registered secret, **When** its evidence is persisted, **Then** the secret is redacted via `RegisterSensitiveValues` **before** the record is written (`pkg/config/security.go:69`; ADR D2 evidence rules).
3. **Given** a check that exceeds the per-attempt output-size cap, **When** evidence is stored, **Then** it is truncated with a truncation marker.
4. **Given** the owning task is deleted, **When** deletion completes, **Then** all its `EvidenceRecord`s are deleted with it (retention follows 90-day session default until then).
5. **Given** a criterion of `kind:check` with no command, **When** validated, **Then** it is rejected 400 "check criterion requires command".

### User Story 4 — Judge System Agent seeded, non-privileged, coverage-total (Priority: P1)

An operator wants a **System Agents** category with a seeded, **locked**, non-deletable, non-disableable **Judge** whose model + rubric are editable but which is not a chat target, not a delegation target, and not privileged (subject to SEC-26 like any non-core agent), so out-of-turn judge LLM calls are visible, metered, and rate-limited (ADR D3, FR-7).

**Why this priority**: P1, not P0 — the schema (US-1..3) can land and be tested with the judge stubbed; the seeded System-Agent identity, privilege narrowing, and enumeration exclusions are the transparency layer around it. Still release-scoped (the Judge ships this epic).

**Independent Test**: Boot a fresh install, assert exactly one `type:system` agent (`judge`) exists, locked, non-default, with an all-deny tool policy that keeps `ValidateToolPolicyCoverage` gap-free; assert `POST /api/v1/agents {"type":"system"}` → 400 and `DELETE /agents/judge` → 400.

**Acceptance Scenarios**:
1. **Given** a fresh install, **When** `SeedConfig` runs, **Then** a `judge` agent exists with `type:system`, `locked:true`, `default:false`, and an explicit all-deny `tools.builtin.policies` covering every static builtin tool.
2. **Given** the seeded Judge, **When** boot-time `ValidateToolPolicyCoverage` runs, **Then** it reports **zero** gaps for the Judge (Constraint #6 boot matrix stays total).
3. **Given** any client, **When** `POST /api/v1/agents` or the `create_agent` tool sends `type:"system"`, **Then** it is rejected 400 via raw-body sniff (mirrors `pkg/gateway/rest.go:2775`).
4. **Given** the seeded Judge, **When** `DELETE /agents/judge` or `PUT` with `enabled:false` is issued, **Then** it is rejected 400 (non-deletable, non-disableable); only model/provider + rubric edits are accepted.
5. **Given** the narrowed `IsPrivilegedAgent`, **When** a `type:system` agent makes LLM calls, **Then** it is subject to per-agent rate limits and the daily cost cap (SEC-26).

---

## Data Model

### A. Plan entity (`pkg/plan`)

New package `pkg/plan` mirroring `pkg/task` (dedicated Store with atomic write + striped lock — stronger than the inline gateway milestone helpers; SD-A1). On-disk `~/.omnipus/plans/<id>.json`.

```go
// pkg/plan/plan.go
type State string
const (
    StateDraft    State = "draft"    // being authored; not yet runnable
    StateApproved State = "approved" // DoD/owner locked in; ready to run
    StateRunning  State = "running"  // engine dispatching member tasks under plan judge
    StateDone     State = "done"     // terminal success (plan judge PASS)
    StateFailed   State = "failed"   // terminal failure (brake fired / judge rounds exhausted)
)

// not-wire-format: internal disk struct; mapped to gen.Plan at the REST layer.
type Plan struct {
    ID          string   `json:"id"`                   // ULID (mirrors milestone id gen)
    WorkspaceID string   `json:"workspace_id"`         // required; same-workspace FK gate
    Title       string   `json:"title"`                // 1..200
    Goal        string   `json:"goal,omitempty"`       // <=2000; plain-prose objective
    Description string   `json:"description,omitempty"`// <=2000
    State       State    `json:"state"`                // default draft
    OwnerAgentID string  `json:"owner_agent_id"`       // required; woken at decision points (ADR D4)
    // DoD is the plan-level acceptance-criteria set the plan judge evaluates.
    DoD         []AcceptanceCriterion `json:"dod,omitempty"`
    // Bounds overrides (ADR D7/FR-9): nil = inherit global PlanningConfig.
    Bounds      *PlanBounds `json:"bounds,omitempty"`
    // --- persisted counters/timestamps (ADR D4 MAJ-004: durable, boot-reconciled) ---
    JudgeRounds   int    `json:"judge_rounds,omitempty"`   // plan-judge rounds consumed
    ActiveLoop    bool   `json:"active_loop,omitempty"`    // counts toward global_active_loop_cap while running
    PausedReason  string `json:"paused_reason,omitempty"` // non-empty ⇒ paused (owner disabled / judge unavailable)
    LastActivityAt string `json:"last_activity_at,omitempty"` // idle-expiry clock (7d)
    PlanPhase    string `json:"plan_phase,omitempty"`    // R1/C19: dispatching|judging|synthesizing|idle — runtime-only, NOT a PlanState
    FailedReason string `json:"failed_reason,omitempty"` // R1/C19: judge_rounds_exhausted|stopped_by_user|idle_expired — set only when State==failed
    Progress     float64 `json:"progress,omitempty"`     // R4/C19/M7: done/total over members, server-computed read-time (0..1)
    // --- attribution + lifecycle timestamps (RFC 3339 UTC) ---
    Owner       string `json:"owner,omitempty"`       // creating username; read-only
    CreatedBy   string `json:"created_by,omitempty"`  // read-only
    CreatedAt   string `json:"created_at"`
    UpdatedAt   string `json:"updated_at"`
    ApprovedAt  string `json:"approved_at,omitempty"`
    StartedAt   string `json:"started_at,omitempty"`
    CompletedAt string `json:"completed_at,omitempty"`
}

type PlanBounds struct {
    PlanJudgeMaxRounds *int `json:"plan_judge_max_rounds,omitempty"`
    IdleExpiryDays     *int `json:"idle_expiry_days,omitempty"`
    // NO token/money fields (NFR-1).
}
```

**Membership** is via the reverse index `Task.PlanID` (like `MilestoneID` before it), computed read-time by scanning tasks with `plan_id == <id>` — never stored on the Plan (mirrors `computeMilestoneCounts` `pkg/gateway/rest_milestones.go:153`). Plan progress = done/total over member tasks, read-time only.

**Store layout & concurrency** (mirror `pkg/task/store.go`): `New(dir)` binds a `plan.StripedLock` pool; per-plan RMW holds `lock.Get(id)` across load→mutate→write; `write` = `fileutil.WithFlock` + `fileutil.WriteFileAtomic(path, data, 0o600)`; `MkdirAll(dir, 0o700)`; `validateID` rejects `/`,`\`,`..`,NUL. `List(Filter{WorkspaceID})` scans `*.json`, skips corrupt with WARN.

**State machine** (SD-A2 — legal transitions; illegal → `ErrIllegalPlanTransition` wrapping a `plan.ErrValidation`, 400):

| from \ to | draft | approved | running | done | failed |
|---|---|---|---|---|---|
| **draft** | ✓(no-op) | ✓ | ✗ | ✗ | ✗ |
| **approved** | ✓(revoke) | ✓ | ✓ | ✗ | ✗ |
| **running** | ✗ | ✗ | ✓ | ✓ | ✓ |
| **done** | ✗ | ✗ | ✗ | ✓ | ✗ | (frozen — terminal, mirrors task `done` `pkg/task/store.go:483`) |
| **failed** | ✗ | ✗ | ✗ | ✗ | ✓(no-op) | (terminal/frozen — F1 r2: a failed plan is NOT retried; author a new plan. Reconciles Part B `stopped`/`expired`/`judge_rounds_exhausted` = terminal) |

`draft` requires ≥1 DoD criterion before `approved` (SD-A7 tier: agent path strict, human/UI soft — plan judged against title+goal when DoD empty). Transition INTO `running` stamps `StartedAt`+`LastActivityAt`+`ActiveLoop=true`; INTO `done`/`failed` stamps `CompletedAt`+`ActiveLoop=false`.

### B. Tags on Task

Add to `task.Task` (`pkg/task/task.go`):
```go
Tags []string `json:"tags,omitempty"` // workspace-scoped, normalized, <=16, each <=64
PlanID string `json:"plan_id,omitempty"` // FK → plan; same-workspace validated
Criteria []AcceptanceCriterion `json:"criteria,omitempty"` // ADR D2/D5
AttemptCount int  `json:"attempt_count,omitempty"` // R4/C17: current run's attempt index; read-only, server-set; renders "attempt N/M"
MaxAttempts  *int `json:"max_attempts,omitempty"`  // R4/C18: per-task attempts override; nil ⇒ inherit PlanningConfig.TaskMaxAttempts
```
**Tag validation** (in `task.normalize`, `pkg/task/store.go:248`, and in `Patch` apply), applied to each tag in order:
1. **Normalize**: `strings.ToLower(strings.TrimSpace(tag))`.
2. Reject empty-after-trim → `verr("tags[%d]: tag must not be empty")`.
3. Reject `len([]rune) > 64` → `verr("tags[%d]: tag must be 64 characters or fewer")` (rune count, not bytes — Unicode-safe, matching Title's `len([]rune)` `pkg/task/store.go:252`).
4. Reject `> 16` tags per task → `verr("tags: at most 16 tags per task")`.
5. **Dedup** after normalization (case-fold collision "Q3"/"q3" → single "q3"); preserve first-seen order.
6. `prefix:value` (e.g. `milestone:`, `release:`) is **convention only**, not schema (ADR D1 tag rules). "Workspace-scoped namespace" = tags are interpreted within a workspace; there is **no** global tag registry, and identical tag strings in different workspaces are unrelated (SD-A8).

### C. AcceptanceCriterion / EvidenceRecord / JudgeVerdict

```go
// pkg/task/criterion.go (shared by Task.Criteria and Plan.DoD)
type CriterionKind string
const ( KindCheck CriterionKind = "check"; KindProse CriterionKind = "prose" )

type CriterionStatus string
const ( CritPending CriterionStatus = "pending"; CritMet CriterionStatus = "met"; CritUnmet CriterionStatus = "unmet" )

type AcceptanceCriterion struct {
    ID     string          `json:"id"`     // UUID, server-set
    Kind   CriterionKind   `json:"kind"`   // check | prose
    Text   string          `json:"text"`   // 1..1000; prose statement OR check description
    Check  *CriterionCheck `json:"check,omitempty"` // required iff kind==check
    Author CriterionAuthor `json:"author"` // recorded identity (ADR D2 rule 3)
    Status CriterionStatus `json:"status"` // per-run; default pending
}
type CriterionCheck struct {
    Command          string `json:"command"`            // dispatched via assignee's bash tool (ADR D2 rule 1)
    ExpectedExitCode int    `json:"expected_exit_code"` // 0..255
}
type CriterionAuthor struct {
    Kind string `json:"kind"` // "agent" | "user"
    ID   string `json:"id"`   // agent_id or username
}
```
**Criterion validation** (SD-A9):
- `Kind` ∈ {check, prose} else 400.
- `Text` non-empty, `len([]rune) ≤ 1000` else 400.
- `Kind==check` ⇒ `Check != nil`, `Check.Command` non-empty, `0 ≤ ExpectedExitCode ≤ 255` else 400. `Kind==prose` ⇒ `Check` must be absent (400 if present — no mixed shape).
- `Author.Kind` ∈ {agent, user} and `Author.ID` non-empty else 400 (author is mandatory, ADR r1 FR-3).
- Agent tool paths: reject a create/update whose criteria are **all** `check` when the assignee's effective `bash` policy is `deny` **or `ask`** (ask→deny unattended ⇒ structurally unsatisfiable; ADR D2 rule 5). UI warns instead.

```go
// pkg/task/evidence.go — stored under $OMNIPUS_HOME/tasks_evidence/<task_id>/<criterion_id>-<attempt>.json (0600), dir 0700
type EvidenceRecord struct {
    ID          string `json:"id"`
    TaskID      string `json:"task_id"`
    CriterionID string `json:"criterion_id"`
    Attempt     int    `json:"attempt"`      // per-run attempt index
    Command     string `json:"command"`      // redacted
    ExitCode    int    `json:"exit_code"`    // actual; timeout/deny sentinels below
    Output      string `json:"output"`       // redacted, size-capped
    Truncated   bool   `json:"truncated"`    // true ⇒ Output cut, marker appended
    TimedOut    bool   `json:"timed_out"`    // 60s (D2 rule 4) ⇒ criterion failed closed
    PolicyDenied bool  `json:"policy_denied"`// bash deny / ask→deny ⇒ failed closed (D2 rule 2)
    RecordedAt  string `json:"recorded_at"`  // RFC 3339 UTC
}
```
**Evidence rules** (ADR D2, verified against `pkg/config/security.go:69`):
- Redaction: pass `Command` + `Output` through the registered sensitive-value scrubber **before** the record is marshalled/written (NEVER write raw then scrub).
- Per-attempt size cap (default = `PlanningConfig`-adjacent constant, e.g. 64 KiB) with a `"...[truncated N bytes]"` marker and `Truncated=true`.
- Storage posture = sessions posture: file 0600, dir 0700, under `$OMNIPUS_HOME`.
- Retention: 90-day session default; **deleted with the task** (task delete cascades evidence dir removal — new step in `Store.Delete`, `pkg/task/store.go:670`, SD-A10).
- Timeout ⇒ `TimedOut=true`, criterion `unmet` (a hung check cannot hold the idle clock, D2/D7).

```go
// pkg/task/verdict.go — persisted alongside the run; also emitted as a transcript entry
type JudgeVerdict struct {
    ID          string `json:"id"`
    Scope       string `json:"scope"`        // "task" | "plan"
    TaskID      string `json:"task_id,omitempty"`
    PlanID      string `json:"plan_id,omitempty"`
    Round       int    `json:"round"`        // attempt/round index (ADR D7: round = worker turn + judge eval)
    Met         bool   `json:"met"`          // overall PASS/FAIL (fail-closed default false)
    PerCriterion []CriterionVerdict `json:"per_criterion"`
    Model       string `json:"model"`        // judge model used (transparency/metering)
    JudgedAt    string `json:"judged_at"`
    // correlation IDs (NFR-5): the System Agent's agent_id + goal/plan/task ids feed metering
    JudgeAgentID string `json:"judge_agent_id"`
}
type CriterionVerdict struct {
    CriterionID string `json:"criterion_id"`
    Met         bool   `json:"met"`
    Reason      string `json:"reason"` // feeds forward as steering (evaluator-optimizer, ADR D2)
}
```
**Session-transcript entry type**: add `EntryTypeJudgeVerdict EntryType = "judge_verdict"` to `pkg/session/daypartition.go:29-40` (alongside `message`/`compaction`/`system`/`tool_call`/`turn_canceled`). It is written alongside the worker's ADR-043 completion marker so the two cannot silently disagree (ADR §6, review Q3). Absence of a verdict never defaults to success (NFR-2).

### F. System Agent seeding (Judge)

New seeding path in `SeedConfig` (`pkg/coreagent/core.go:888`), separate from the core-agent loop:
- Add `IDJudge CoreAgentID = "judge"` (`pkg/coreagent/core.go:29-52`) and a `SystemAgents()` roster (parallel to `BaseAgents()` `:112`).
- On fresh seed AND idempotent re-enforcement: create/repair a `config.AgentConfig{ID:"judge", Type: config.AgentTypeSystem ("system", pkg/config/config.go:823), Locked:true, Default:false, Tools:&{Builtin:{Policies: allDeny}}}`. `allDeny` enumerates **every** static builtin tool as `deny` (the Judge executes as a no-tools structured call, ADR D3) so `ValidateToolPolicyCoverage` (`pkg/config/validate.go:491`) stays gap-free — same uniform `Agents.List` iteration.
- **Editable-only fields**: model/provider (`AgentConfig.Model`/`Provider`, `pkg/config/config.go:514-561`) + the **rubric** (stored as the agent's soul/prompt-override field; the rubric IS the judge's system prompt). All identity/type/locked/tool-policy/enabled fields are re-enforced on every boot (tamper protection, mirrors `:914-963`).
- **Lifecycle guards** (ADR D3 r2/r3, mirroring the ADR-035/037 raw-body-sniff at `pkg/gateway/rest.go:2775`):
  - `POST /api/v1/agents` and `create_agent` tool: raw-body sniff `"type":"system"` → 400 "system agents are not creatable" (SD-A11). Seeding is the only creation path.
  - `DELETE /agents/{id}` where target `type:system` → 400 "system agents are not deletable".
  - `PUT /agents/{id}` where target is the Judge with `enabled:false` (or `disabled:true`) → 400 "the Judge cannot be disabled" (disabling would stall every goal loop via D7 unavailability). Other System Agents (future) may be disable-able; the Judge specifically is not.
- **Privilege narrowing**: `IsPrivilegedAgent` (`pkg/security/ratelimit.go:17-22`) changes from `agentType == "core" || agentType == "system"` to `agentType == "core"` only. SEC-26 tests extended to assert a `type:system` agent is rate-limited + cost-capped (ADR D3, §9.4). No seeded-install impact today (`SeedConfig` seeds no `system` agent yet).

### G. Config bounds (`PlanningConfig`)

Add `Planning PlanningConfig` to `config.Config` (`pkg/config/config.go:126-159`, next to `Sandbox`), populated by `DefaultConfig` (`pkg/config/defaults.go:15`) and range-validated by the boot validator (mirrors `OmnipusSandboxConfig` `pkg/config/sandbox.go:167` + `PortRange.Validate` `:136`). **No token/money fields (NFR-1).**

```go
type PlanningConfig struct {
    TaskMaxAttempts     int `json:"task_max_attempts,omitempty"`      // default 3  → wake owner
    GoalMaxRounds       int `json:"goal_max_rounds,omitempty"`        // default 20 (/goal)
    PlanJudgeMaxRounds  int `json:"plan_judge_max_rounds,omitempty"`  // default 20 (symmetric with /goal)
    LoopMaxRuns         int `json:"loop_max_runs,omitempty"`          // default 100 (/loop)
    IdleExpiryDays      int `json:"idle_expiry_days,omitempty"`       // default 7  (everywhere)
    GlobalActiveLoopCap int `json:"global_active_loop_cap,omitempty"` // default 16 (simultaneous active loops)
    CheckTimeoutSeconds int `json:"check_timeout_seconds,omitempty"`  // default 60 (per machine-check)
}
```
**Per-entity overrides** (FR-9 — global + per-entity that runs a loop; no workspace layer): `Plan.Bounds` (`plan_judge_max_rounds`, `idle_expiry_days`); a `Task`-level attempts override (`Task.MaxAttempts *int`, nil = inherit `TaskMaxAttempts`); `/goal` and `/loop` bounds carried in session state (`UnifiedMeta` extension, `pkg/session/unified.go`, the runtime agent's scope). Resolution: per-entity override wins; else global `PlanningConfig`. Boot validator: each field, when non-zero, must be ≥1; `GlobalActiveLoopCap` ≥1; `CheckTimeoutSeconds` in [1, 3600]; zero ⇒ apply the documented default (mirrors `PortRange.IsZero` default-apply `pkg/config/sandbox.go:121`).

---

## Contract surface (Constraint #8)

**5-step spec-first ordering, applied to EVERY row below, in order** (CLAUDE.md "Add a new wire type"): (1) add `contracts/components/schemas/<TypeName>.yaml`; (2) reference it from `openapi.yaml` and/or `asyncapi.yaml`; (3) run `scripts/gen-contracts.sh`; (4) commit the generated diff (`pkg/api/generated/`, `src/lib/api/generated/`) atomically with the spec change; (5) write handlers/consumers against the generated type only. `make verify-contracts` must be green. The `AgentCreateRequest`-style discriminated-union exception (ADR-034) does **not** apply here (no new `oneOf`).

### New / changed wire types

| # | Wire type | Schema file | Referenced from | Generated artifacts |
|---|---|---|---|---|
| C1 | `Plan` | `contracts/components/schemas/Plan.yaml` | `openapi.yaml` `components.schemas` + GET/POST/PUT `/plans` + `/plans/{id}` responses | `pkg/api/generated/`, `src/lib/api/generated/{openapi-types.ts,schemas.ts}` |
| C2 | `PlanState` (string enum: draft/approved/running/done/failed) | inline `enum` on `Plan.state` (no separate file needed; enum is a property) | via `Plan.yaml` | generated with `Plan` |
| C3 | `PlanCreateRequest` (title,goal,description,owner_agent_id,dod[],bounds?; `additionalProperties:false`) | `PlanCreateRequest.yaml` | `POST /plans` requestBody | both |
| C4 | `PlanUpdateRequest` (partial: title/goal/description/state/owner_agent_id/dod/bounds) | `PlanUpdateRequest.yaml` | `PUT /plans/{id}` requestBody | both |
| C5 | `PlanListResponse` (`plans[]`,`total`) | `PlanListResponse.yaml` | `GET /workspaces/{id}/plans` (mirrors `MilestoneListResponse`) | both |
| C6 | `Task.tags` (array of string; each 1..64, maxItems 16) | edit `Task.yaml` | already referenced | regenerate |
| C7 | `Task.plan_id` (string) + `TaskCreateRequest.plan_id` + `TaskUpdateRequest.plan_id` | edit `Task.yaml`, `TaskCreateRequest.yaml`, `TaskUpdateRequest.yaml` | already referenced | regenerate |
| C8 | `Task.criteria` + `TaskCreateRequest.criteria` + `TaskUpdateRequest.criteria` (array of `AcceptanceCriterion`) | edit those + new `AcceptanceCriterion.yaml` | already referenced | both |
| C9 | `AcceptanceCriterion` (id, kind[check/prose], text, check{command,expected_exit_code}, author{kind,id}, status[pending/met/unmet]) | `AcceptanceCriterion.yaml` | `Task.yaml`, `Plan.yaml` (`dod`), request bodies | both |
| C10 | `EvidenceRecord` | `EvidenceRecord.yaml` | `GET /tasks/{id}/evidence` response (read-only surface) | both |
| C11 | `JudgeVerdict` (+ `CriterionVerdict`) | `JudgeVerdict.yaml`, `CriterionVerdict.yaml` | `GET /tasks/{id}/verdicts`, embedded in transcript | both |
| C12 | `Message.type` gains `judge_verdict`; add optional `verdict` object | edit `Message.yaml` `type` enum (`:16-30`) | already referenced | regenerate |
| C13 | `Agent` System-section additions: `type` enum already has `system` (`Agent.yaml:33`); add `rubric` (string, editable-on-system) + document `system` semantics | edit `Agent.yaml`, `AgentUpdateRequest.yaml` | already referenced | regenerate |
| C14 | goal/loop AsyncAPI status frames — **named here; runtime semantics by sibling agent B**: `GoalStatusFrame` (`goal_status`), `LoopStatusFrame` (`loop_status`), `PlanStatusFrame` (`plan_status`) | `GoalStatusFrame.yaml`, `LoopStatusFrame.yaml`, `PlanStatusFrame.yaml` | `asyncapi.yaml` `components.messages` + a `receiveGoalStatus`/`receiveLoopStatus`/`receivePlanStatus` operation (mirrors `receiveTaskStatusChanged` `asyncapi.yaml:368-375`) | `pkg/api/generated/`, `src/lib/api/generated/schemas.ts` (Zod) |
| C15 | Command registry entries `goal`, `loop` (+ aliases) surfaced by `GET /api/v1/commands` as `SlashCommand` | no new schema (existing `SlashCommand.yaml`); backing defs in `pkg/commands` | n/a (data, not type) | n/a |

### Removal diffs (Milestone*)

| # | Remove | Location |
|---|---|---|
| R1 | schema `Milestone.yaml`, `MilestoneCreateRequest.yaml`, `MilestoneUpdateRequest.yaml`, `MilestoneListResponse.yaml` | `contracts/components/schemas/` |
| R2 | `components.schemas` refs `Milestone`/`MilestoneCreateRequest`/`MilestoneUpdateRequest`/`MilestoneListResponse` | `openapi.yaml:533-552` |
| R3 | tag `Milestones` | `openapi.yaml:59-60` |
| R4 | paths `/workspaces/{id}/milestones` and `/workspaces/{id}/milestones/{milestoneId}` (all 5 operations: list/create/get/update/delete) | `openapi.yaml:5424-5574` |
| R5 | `Task.milestone_id` property + `TaskCreateRequest.milestone_id` property + task `milestone_id` query param | `Task.yaml`, `TaskCreateRequest.yaml`, `openapi.yaml:4309-4314` |
| R6 | generated types `Milestone*` (Go + TS) | `pkg/api/generated/openapi_types.gen.go`, `src/lib/api/generated/{schemas.ts (L783),openapi-types.ts}` — regenerate, do not hand-edit |
| R7 | SPA `Milestone` interface + query hooks + `MilestoneFilterPills.tsx`, `CreateMilestoneSlideOver.tsx`, `MilestoneProgressBar.tsx` (+ their `.test.tsx`) | `src/lib/api.ts`, `src/components/workspaces/` (sibling SPA agent executes; enumerated here) |

---

## Milestone REMOVAL + MIGRATION

### Removal list (Go side)

1. **Struct**: `Task.MilestoneID` (`pkg/task/task.go:220`).
2. **Filter**: `Filter.MilestoneID` field + `matches` clause (`pkg/task/store.go:137,163-165`).
3. **Patch**: `Patch.MilestoneID` field + apply block (`pkg/task/store.go:441,623-625`).
4. **Whole file**: `pkg/gateway/rest_milestones.go` (`:1-751`) — `milestone` struct, `readMilestoneFile`/`writeMilestoneFile`/`listMilestoneFiles`, `milestoneToWireWithProgress`, `computeMilestoneCounts`, `milestoneProgress`, `clearMilestoneOnTasks`, all 5 handlers, `HandleMilestones`, `milestoneFileLock`, `dueDatePattern`, `milestoneUpdateRequest`.
5. **Route dispatch**: the `/milestones` branch (`pkg/gateway/rest_workspaces.go:499-501`).
6. **Tests**: `pkg/gateway/rest_milestones_test.go`, `pkg/gateway/session_milestone2_test.go` — deleted or replaced by Plan/tag tests.

### Migration algorithm (ADR D1, ratified r2/r3)

Runs at **task-store load** (boot), before the gateway serves. Guarded by a completion sentinel `$OMNIPUS_HOME/.milestones_migrated` (SD-A6). If the sentinel exists → skip (opportunistically remove any leftover `~/.omnipus/milestones/` dir) → done. Else:

```
migrateMilestonesToTags(home):
  files := readDir(home/milestones/*.json)            # sorted ASCENDING by milestone ID (stable order)
  if len(files)==0: writeSentinel(); return
  # PHASE 1 — build the full milestone→tag mapping from ALL surviving files (deterministic).
  assigned := {}          # tag -> milestoneID (uniqueness ledger)
  mapping  := {}          # milestoneID -> {tag, dueDate, name}
  for m in files (by ID asc):
      base := lower(trim(m.Name))
      # reserve headroom: final tag = "milestone:" + base + optional "-<N>", must be <=64.
      # Reserve len("milestone:")=10 and worst-case suffix (e.g. "-999")=4 → truncate base to 64-10-4=50 runes.
      base = truncateRunes(base, 64-10-MAX_SUFFIX_LEN)
      tag := "milestone:" + base
      n := 1
      while assigned[tag] exists && assigned[tag] != m.ID:   # re-check AFTER suffixing (D1 r3)
          n++; tag = "milestone:" + base + "-" + n
      assigned[tag] = m.ID
      mapping[m.ID] = {tag, m.DueDate, m.Name}
  # PHASE 2 — apply to member tasks (idempotent). Empty milestones logged.
  # CRITICAL (C2 fix): the legacy milestone_id key is read from RAW JSON, NOT the
  # Task struct — FR-032 removes Task.MilestoneID in this same epic, and json.Unmarshal
  # silently drops keys with no struct field (pkg/task/store.go:97). The migration MUST
  # parse each task file into a legacy-shaped view (map[string]json.RawMessage, or a
  # dedicated legacyTask{MilestoneID,Tags,Due} struct) so the milestone linkage survives
  # the field removal. Running the migration at store-load BEFORE any struct read is not
  # sufficient on its own — the field is gone from the type, so raw-key parsing is required.
  emptyMilestones := mapping.keys()   # start "all empty", remove as members found
  for each task file t (under home/tasks):
      raw := parseLegacy(t)                                       # map[string]json.RawMessage
      mid := raw["milestone_id"]                                  # legacy key, struct-independent
      if mid in mapping:
          entry := mapping[mid]
          tags := raw["tags"]  (or [])
          if entry.tag not in tags:  tags.append(entry.tag)       # dedup ⇒ idempotent
          if raw["due"] == "" && entry.dueDate != "":
              raw["due"] = entry.dueDate + "T00:00:00Z"           # YYYY-MM-DD → RFC3339 (SD-A12)
          delete raw["milestone_id"]                              # drop legacy key on rewrite
          raw["tags"] = tags
          atomicWriteUnderStripedLock(t, raw)                     # write the merged raw doc
          delete emptyMilestones[mid]                             # capture mid BEFORE any clear (m2 fix)
  for id in emptyMilestones:
      log.Info("milestone migration: empty milestone preserved as log entry",
               name=mapping[id].name, due_date=mapping[id].dueDate)   # a tag cannot exist unattached (D1 r2)
  # PHASE 3 — finalize (only after ALL task writes succeeded).
  writeSentinel($OMNIPUS_HOME/.milestones_migrated)               # LAST durable act before dir removal
  removeDir(home/milestones)
```

**Properties (ADR D1 + SD-A6):**
- **Idempotent**: tag-add is dedup-guarded; `Due` copied only when empty; re-run after completion is sentinel-skipped.
- **Deterministic suffixing under crash**: files are **not** deleted individually — Phase 1 recomputes the identical mapping from all surviving files on any re-run, because deletion happens only in Phase 3 *after* the sentinel. A crash before the sentinel leaves ALL milestone files present → identical recompute. A crash after the sentinel → boot sees sentinel → skips + removes leftover dir. This closes the "surviving-subset reorders suffixes" hazard.
- **Crash-safe**: each task write is `WriteFileAtomic` under the striped lock; a partially-migrated task simply re-migrates idempotently.
- **Lossless**: `due_date` preserved onto empty `Due`; tasks with their own `Due` keep it; empty milestones logged.
- **Logged**: every tag assignment, every empty-milestone preservation, and the final sweep are logged at Info.

---

## Behavioral Contract

Primary flows:
- When a task write includes `tags`, the system normalizes (lowercase+trim), dedups, and rejects if >16 or any >64 runes.
- When a task write includes `plan_id`, the system validates the referenced plan exists and is in the SAME workspace, else 400.
- When the task store loads and no migration sentinel exists, the system migrates all milestones to `milestone:` tags and copies `due_date` losslessly, then writes the sentinel.
- When `SeedConfig` runs, the system ensures exactly one locked `type:system` Judge with a coverage-total all-deny policy.

Error flows:
- When a criterion of kind `check` omits its command, the system rejects 400.
- When an agent-tool `create_task` carries zero criteria, the system rejects 400.
- When `POST /agents` carries `type:"system"`, the system rejects 400 (raw-body sniff).
- When a plan transition is illegal (e.g. `draft→running`), the system rejects 400.

Boundary conditions:
- When two milestones normalize to the same tag, the second (by ID order) gets a `-N` suffix, uniqueness re-checked.
- When a check exceeds the output-size cap, evidence is truncated with a marker.
- When a check exceeds 60s, it is marked timed-out and the criterion is `unmet` (fail-closed).

---

## Edge Cases

- Tag exactly 64 runes → accepted; 65 → 400. Emoji/combining tag → counted by rune (`len([]rune)`), not byte.
- A milestone name of 64 chars → truncated to leave prefix+suffix headroom so the final tag is ≤64.
- A plan deleted while `running` with member tasks → rejected (SD-A5); non-running plan delete → `plan_id` cleared on member tasks (best-effort, striped-lock RMW, mirrors `clearMilestoneOnTasks`).
- Evidence emitting a secret that is ALSO the truncation boundary → redaction runs before truncation? No — redact first, then cap (SD-A13: redaction MUST precede truncation so a secret split across the cap boundary is still fully scrubbed).
- A criterion authored by a different agent than the assignee (Gap #2) → default requires assignee-owner confirmation (data model records `Author`; the confirmation flow is a sibling-agent runtime concern, but the `Author` field is the enabling schema).

---

## Explicit Non-Behaviors

- The system must NOT enforce any token/money budget on plans, goals, loops, or the judge (NFR-1) — no such field exists in `PlanBounds` or `PlanningConfig`.
- The system must NOT resolve a `plan_id`/tag/criterion default from a code branch — Plan-membership and tags are explicit stored data; the Judge tool policy is explicit seeded data (Constraint #6).
- The system must NOT keep any Milestone back-compat alias, endpoint, or field after migration (ADR §7, precedent ADR-035/037).
- The system must NOT treat absence of a judge verdict as success (NFR-2).
- The system must NOT let `create_agent`/REST create a `type:system` agent, nor delete/disable the seeded Judge.

---

## BDD Scenarios

### Feature: Plan entity

#### Scenario: Operator creates a plan with a DoD criterion
**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path
- **Given** an existing workspace `ws-1`
- **When** a client POSTs a Plan with title, goal, `owner_agent_id: jim`, and one prose DoD criterion
- **Then** the response is 201 with `state: draft`, server-set `owner`/`created_by`/`created_at`
- **And** `~/.omnipus/plans/<id>.json` exists with mode 0600

#### Scenario: Task in a foreign workspace cannot reference a plan
**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Error Path
- **Given** a Plan `p-1` in workspace `ws-1`
- **And** a task in workspace `ws-2`
- **When** the task is written with `plan_id: p-1`
- **Then** the write is rejected 400 "plan is in a different workspace"

#### Scenario Outline: Plan state transitions
**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Edge Case
- **Given** a Plan in state `<from>`
- **When** a transition to `<to>` is requested
- **Then** the result is `<result>`

**Examples**:
| from | to | result |
|------|------|--------|
| draft | approved | accepted |
| draft | running | rejected 400 |
| draft | done | rejected 400 |
| approved | running | accepted |
| approved | draft | accepted |
| approved | done | rejected 400 |
| running | done | accepted |
| running | failed | accepted |
| running | draft | rejected 400 |
| done | inbox/draft/approved/running/failed | rejected 400 (frozen) |
| failed | approved | rejected 400 (terminal/frozen — F1 r2) |
| failed | running | rejected 400 (terminal/frozen — F1 r2) |

#### Scenario: Deleting a running plan is rejected
**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Error Path
- **Given** a Plan `p-1` in state `running` with two member tasks
- **When** a client DELETEs `p-1`
- **Then** the delete is rejected (must stop the plan first)
- **But** the member tasks retain their `plan_id`

#### Scenario: Deleting a draft plan clears plan_id on members
**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Alternate Path
- **Given** a Plan `p-2` in state `draft` with two member tasks
- **When** a client DELETEs `p-2`
- **Then** the delete succeeds
- **And** both member tasks have empty `plan_id`

### Feature: Task tags

#### Scenario Outline: Tag normalization and validation
**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Edge Case
- **Given** a task write with tag input `<input>`
- **When** the store normalizes and validates it
- **Then** the result is `<result>`

**Examples**:
| input | result |
|-------|--------|
| `["Release"]` | stored as `["release"]` |
| `["  spaced  "]` | stored as `["spaced"]` (trimmed) |
| `[""]` | rejected 400 (empty after trim) |
| `["   "]` | rejected 400 (whitespace-only) |
| `["a"]` | stored `["a"]` (1-char ok) |
| 64-rune tag | stored (max) |
| 65-rune tag | rejected 400 (over max) |
| 17 distinct tags | rejected 400 (max 16) |
| `["Q3","q3"]` | stored `["q3"]` (case-fold dedup) |
| `["café"]` (combining) | stored, counted by rune |
| `["milestone:q3"]` | stored (prefix is convention, not schema) |

### Feature: Milestone migration

#### Scenario Outline: Milestone name → tag with collision disambiguation
**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Edge Case
- **Given** milestones `<names>` (ascending ID order) each with a member task
- **When** the migration runs at task-store load
- **Then** the tags assigned are `<tags>`

**Examples**:
| names | tags |
|-------|------|
| `["Q3 Release"]` | `["milestone:q3 release"]` |
| `["Q3","q3"]` | `["milestone:q3","milestone:q3-2"]` |
| `["Q3","Q3","q3"]` | `["milestone:q3","milestone:q3-2","milestone:q3-3"]` |
| 64-char name | truncated so `milestone:<trunc>` ≤64 |
| two 64-char names normalizing equal | `milestone:<trunc>`, `milestone:<trunc>-2` (headroom reserved) |

#### Scenario: due_date copied only onto empty Due
**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path
- **Given** milestone "Q3" (`due_date 2026-09-30`) with task A (`Due` empty) and task B (`Due` 2026-08-01T00:00:00Z)
- **When** migration runs
- **Then** task A gains `Due: 2026-09-30T00:00:00Z`
- **And** task B keeps `Due: 2026-08-01T00:00:00Z`

#### Scenario: Empty milestone preserved as a log entry
**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Edge Case
- **Given** milestone "Retired" with no member tasks
- **When** migration runs
- **Then** an Info log records name "Retired" and its due_date
- **And** no task is modified for it

#### Scenario: Re-run after completion is a no-op
**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Edge Case
- **Given** a completed migration (sentinel present, milestones dir removed)
- **When** the gateway boots again
- **Then** no task tags change and no milestone processing occurs

#### Scenario: Crash mid-migration re-runs identically
**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case
- **Given** migration applied tags to some tasks but crashed before writing the sentinel (all milestone files still present)
- **When** the process restarts and re-runs migration
- **Then** the final tag set and Due values are identical to a single clean run

### Feature: Acceptance criteria + evidence

#### Scenario: Agent create_task without criteria is rejected
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Error Path
- **Given** an agent invoking the `create_task` tool
- **When** the task has zero criteria
- **Then** the tool rejects it 400 "≥1 acceptance criterion required"

#### Scenario Outline: Criterion validation
**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path
- **Given** a criterion `<criterion>`
- **When** it is validated
- **Then** the result is `<result>`

**Examples**:
| criterion | result |
|-----------|--------|
| `{kind:prose, text:"looks good", author:{kind:user,id:alice}}` | accepted |
| `{kind:check, text:"tests pass", check:{command:"go test", expected_exit_code:0}, author:{kind:agent,id:jim}}` | accepted |
| `{kind:bogus, ...}` | rejected 400 (invalid kind) |
| `{kind:prose, text:"", author:...}` | rejected 400 (empty text) |
| `{kind:check, text:"x", author:...}` (no check) | rejected 400 (check requires command) |
| `{kind:check, check:{command:"x", expected_exit_code:256}, ...}` | rejected 400 (exit code out of 0..255) |
| `{kind:check, check:{command:"x", expected_exit_code:-1}, ...}` | rejected 400 (exit code below 0) |
| `{kind:prose, text:"x", author:{kind:user,id:""}}` | rejected 400 (author id missing) |
| `{kind:prose, text:"x"}` (no author) | rejected 400 (author required) |

#### Scenario: Evidence is redacted before persistence
**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Edge Case
- **Given** a registered sensitive value `SECRET123` and a check command whose output contains it
- **When** the EvidenceRecord is written
- **Then** the on-disk record contains a redaction placeholder, never `SECRET123`

#### Scenario: Evidence deleted with the task
**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Happy Path
- **Given** a task with two persisted EvidenceRecords
- **When** the task is deleted
- **Then** the task's evidence directory is removed

### Feature: Judge System Agent

#### Scenario: Fresh install seeds a locked non-privileged Judge
**Traces to**: User Story 4, Acceptance Scenario 1 & 2
**Category**: Happy Path
- **Given** a fresh install
- **When** `SeedConfig` runs and boot coverage validation follows
- **Then** exactly one `type:system` agent `judge` exists, `locked:true`, `default:false`
- **And** `ValidateToolPolicyCoverage` reports zero gaps for it

#### Scenario Outline: System-agent lifecycle guards
**Traces to**: User Story 4, Acceptance Scenario 3 & 4
**Category**: Error Path
- **Given** a client `<action>`
- **When** it is submitted
- **Then** the result is `<result>`

**Examples**:
| action | result |
|--------|--------|
| `POST /agents {"type":"system"}` | rejected 400 (raw-body sniff) |
| `create_agent` tool with type system | rejected 400 |
| `DELETE /agents/judge` | rejected 400 (non-deletable) |
| `PUT /agents/judge {"enabled":false}` | rejected 400 (non-disableable) |
| `PUT /agents/judge {"model":"..."}` | accepted (model editable) |
| `PUT /agents/judge {"rubric":"..."}` | accepted (rubric editable) |

#### Scenario: type:system agent is rate-limited
**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Edge Case
- **Given** `IsPrivilegedAgent` narrowed to `core` only
- **When** a `type:system` agent's LLM call is checked against SEC-26
- **Then** it is subject to per-agent rate limits and the daily cost cap

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------|-------|---------|
| Unit | `pkg/plan` store, tag/criterion validators, migration function, config validation, `SeedConfig` | logic in isolation |
| Integration | Plan REST handlers, task `plan_id`/`tags`/`criteria` REST + tool paths, migration at boot, coverage validation with seeded Judge, agent lifecycle guards | components together |
| Contract | `pkg/api/generated/contract_test.go` (Go structs → schema-valid JSON), `make verify-contracts` | wire-format integrity |

### Test Implementation Order

| Order | Test Name | Level | Traces to | Description |
|-------|-----------|-------|-----------|-------------|
| 1 | `TestPlan_Normalize_RequiredFields` | Unit | Scenario: create plan | title/workspace_id/owner required; defaults `draft` |
| 2 | `TestPlan_StateTransitions_LegalAndIllegal` | Unit | Scenario Outline: Plan state transitions | full transition matrix incl. frozen `done` |
| 3 | `TestPlanStore_CreateGetUpdateDelete_Atomic` | Unit | Scenario: create plan | round-trip persistence, 0600, striped lock |
| 4 | `TestPlanStore_List_ByWorkspace` | Unit | Scenario: create plan | workspace filter, corrupt-file skip |
| 5 | `TestTask_TagValidation_NormalizeDedupBounds` | Unit | Scenario Outline: Tag normalization | lowercase/trim/rune-64/max-16/dedup |
| 6 | `TestTask_PlanIDSameWorkspace_Rejected` | Unit | Scenario: foreign workspace | cross-workspace plan_id → ErrValidation |
| 7 | `TestCriterion_Validate_KindTextCheckAuthorExit` | Unit | Scenario Outline: Criterion validation | all criterion validation rows |
| 8 | `TestCriterion_AllCheckWithBashDenyOrAsk_Rejected` | Unit | US-3 (D2 rule 5) | all-check + bash deny/ask unsatisfiable |
| 9 | `TestEvidence_RedactBeforePersist` | Unit | Scenario: evidence redacted | scrubber runs before marshal |
| 10 | `TestEvidence_SizeCapTruncationMarker` | Unit | Scenario: cap truncation | over-cap output truncated with marker |
| 11 | `TestEvidence_RedactPrecedesTruncation` | Unit | Edge case SD-A13 | secret at cap boundary still scrubbed |
| 12 | `TestJudgeVerdict_PerCriterionShape` | Unit | US-3 | per-criterion {met,reason}; fail-closed default |
| 13 | `TestMigration_NameToTag_NormalizeTruncate` | Unit | Scenario Outline: name→tag | normalize + headroom truncation |
| 14 | `TestMigration_CollisionDisambiguation_ReCheck` | Unit | Scenario Outline (Q3/q3) | suffix + uniqueness re-check |
| 15 | `TestMigration_DueDate_CopyOnlyIfEmpty` | Unit | Scenario: due_date copy | empty Due filled; own Due kept; YYYY-MM-DD→RFC3339 |
| 16 | `TestMigration_EmptyMilestone_Logged` | Unit | Scenario: empty milestone | log entry, no task touched |
| 17 | `TestMigration_Idempotent_ReRunNoOp` | Unit | Scenario: re-run no-op | sentinel skip |
| 18 | `TestMigration_CrashMidway_ReRunIdentical` | Unit | Scenario: crash mid-migration | partial → identical final state |
| 19 | `TestPlanningConfig_Defaults_And_BoundsValidate` | Unit | US-1/G | defaults 3/20/20/100/7/16/60; range checks; no token fields |
| 20 | `TestSeedConfig_SeedsJudge_SystemLockedAllDeny` | Unit | Scenario: fresh install judge | type/system, locked, all-deny |
| 21 | `TestValidateToolPolicyCoverage_WithSeededJudge_NoGaps` | Unit | Scenario: coverage | Judge covered, matrix total |
| 22 | `TestIsPrivilegedAgent_SystemNotPrivileged` | Unit | Scenario: rate-limited | narrowed to core only |
| 23 | `TestPlanREST_CreateListGetUpdateDelete` | Integration | US-1 scenarios | handler happy paths |
| 24 | `TestPlanREST_DeleteRunning_Rejected` | Integration | Scenario: delete running | 409/400; members keep plan_id |
| 25 | `TestPlanREST_DeleteDraft_ClearsMemberPlanID` | Integration | Scenario: delete draft | cascade clear |
| 26 | `TestTaskREST_CreateWithTagsPlanCriteria` | Integration | US-2/US-3 | wire round-trip of new fields |
| 27 | `TestTaskTool_CreateWithoutCriteria_Rejected` | Integration | Scenario: agent no criteria | agent path strict |
| 28 | `TestMigration_RunsAtBoot_TagsApplied` | Integration | Scenario: due_date copy | boot path migrates on real dirs |
| 29 | `TestAgentREST_CreateSystemType_Rejected400` | Integration | Scenario Outline: lifecycle | raw-body sniff |
| 30 | `TestAgentREST_DeleteOrDisableJudge_Rejected400` | Integration | Scenario Outline: lifecycle | non-deletable/disableable |
| 31 | `TestAgentREST_EditJudgeModelRubric_Accepted` | Integration | Scenario Outline: lifecycle | editable fields only |
| 32 | `TestEvidence_DeletedWithTask` | Integration | Scenario: evidence deleted | cascade on task delete |
| 33 | `TestContract_PlanCriterionEvidenceVerdict_SchemaValid` | Contract | C1-C12 | generated structs → valid JSON |
| 34 | `TestVerifyContracts_NoMilestoneRefs` | Contract | R1-R7 | milestone schemas/paths gone; specs lint clean |

### Test Datasets

#### Dataset: Tag boundary validation
| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `[]` (no tags) | Empty | accepted, no tags | Scenario Outline: Tag normalization | tags optional |
| 2 | `["a"]` | Min (1 char) | stored `["a"]` | same | minimum meaningful |
| 3 | 64-rune tag | Max | stored | same | upper bound |
| 4 | 65-rune tag | Max+1 | 400 "64 characters or fewer" | same | over limit |
| 5 | 16 distinct tags | Max collection | accepted | same | max per task |
| 6 | 17 distinct tags | Max+1 collection | 400 "at most 16 tags" | same | over collection limit |
| 7 | `[""]` | Empty string | 400 (empty after trim) | same | zero-length |
| 8 | `["   "]`,`["\t"]` | Whitespace only | 400 | same | invisible non-empty |
| 9 | `[" x "]` | Leading/trailing | stored `["x"]` | same | trim |
| 10 | `["RELEASE"]` | Uppercase | stored `["release"]` | same | lowercased |
| 11 | `["Q3","q3"]` | Duplicate (case-fold) | stored `["q3"]` | same | dedup |
| 12 | `["café"]` (e+combining) | Unicode/combining | stored, rune-counted | same | multi-byte |
| 13 | `["milestone:q3"]` | Prefix convention | stored verbatim | same | prefix not schema |

#### Dataset: Milestone migration collisions
| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `["Q3"]` | Single | `["milestone:q3"]` | Scenario Outline: name→tag | normalize |
| 2 | `["Q3","q3"]` | Collision | `["milestone:q3","milestone:q3-2"]` | same | post-normalize collision (r2) |
| 3 | `["Q3","Q3","q3"]` | Triple collision | `...q3`, `...q3-2`, `...q3-3` | same | re-check after suffix (r3) |
| 4 | 64-char name | Max | `milestone:<truncated>` ≤64 | same | headroom reserved |
| 5 | two equal 64-char names | Max + collision | `<trunc>`, `<trunc>-2` both ≤64 | same | prefix+suffix overflow guarded |
| 6 | milestone, no members | Empty collection | log entry only | Scenario: empty milestone | unattached tag impossible |
| 7 | milestone due=2026-09-30, task Due="" | Date copy | task Due=2026-09-30T00:00:00Z | Scenario: due_date copy | date→datetime |
| 8 | milestone due=2026-09-30, task Due=own | Date conflict | task keeps own Due | same | own wins |
| 9 | completed migration + reboot | Idempotency | no-op | Scenario: re-run no-op | sentinel skip |
| 10 | tags applied, sentinel absent, reboot | Crash recovery | identical final state | Scenario: crash mid-migration | deterministic re-run |

#### Dataset: Criterion validation
| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | kind=prose, text="ok", author=user:alice | Valid | accepted | Scenario Outline: Criterion | happy prose |
| 2 | kind=check, cmd="go test", exit=0, author=agent:jim | Valid | accepted | same | happy check |
| 3 | kind="bogus" | Invalid enum | 400 invalid kind | same | wrong type |
| 4 | kind=prose, text="" | Empty | 400 empty text | same | required |
| 5 | kind=prose, text=1001 runes | Max+1 | 400 over 1000 | same | text cap |
| 6 | kind=check, no check obj | Missing field | 400 check requires command | same | check needs command |
| 7 | kind=check, exit=0 (min) | Min | accepted | same | lower bound |
| 8 | kind=check, exit=255 (max) | Max | accepted | same | upper bound |
| 9 | kind=check, exit=256 | Max+1 | 400 out of 0..255 | same | over |
| 10 | kind=check, exit=-1 | Min-1 | 400 out of 0..255 | same | under |
| 11 | kind=prose + check obj present | Mixed shape | 400 prose has no check | same | no mixed |
| 12 | author absent | Missing | 400 author required | same | mandatory (r1) |
| 13 | author.id="" | Empty | 400 author id required | same | identity required |
| 14 | all-check criteria + bash policy=deny | Unsatisfiable | 400 (agent path) | Scenario: bash deny | D2 rule 5 |
| 15 | all-check criteria + bash policy=ask | Unsatisfiable | 400 (ask→deny) | same | ask resolves deny |

#### Dataset: Plan FK & lifecycle
| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | task plan_id → missing plan | Missing FK | 400 plan not found | Scenario: foreign workspace | dangling ref |
| 2 | task plan_id → plan in other workspace | Cross-workspace | 400 different workspace | same | mirrors blocker guard |
| 3 | task plan_id → same-workspace plan | Valid FK | accepted, appears in members | US-1 AS2 | happy |
| 4 | delete plan in `running` w/ members | State guard | rejected | Scenario: delete running | stop first |
| 5 | delete plan in `draft` w/ members | Cascade | plan_id cleared on members | Scenario: delete draft | best-effort clear |
| 6 | delete plan with zero members | Empty | plan removed cleanly | US-1 | no cascade needed |

#### Dataset: Config bounds
| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | all fields zero (fresh) | Default apply | 3/20/20/100/7/16/60 | Test 19 | defaults on empty |
| 2 | task_max_attempts = 0 explicit | Zero → default | 3 | same | zero means default |
| 3 | check_timeout_seconds = 0 | Zero → default | 60 | same | default |
| 4 | check_timeout_seconds = 3601 | Max+1 | validation error | same | [1,3600] bound |
| 5 | global_active_loop_cap = -1 | Negative | validation error | same | must ≥1 |
| 6 | any `token_budget` field | Forbidden | not present in schema (compile/marshal ignores) | Non-Behaviors | NFR-1 |

### Regression Test Requirements

**Modifying existing functionality (task store + gateway):**

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|--------------------|---------------|----------------------------|-------|
| blocked_by cycle/self/depth validation | `pkg/task/store_test.go`, `pkg/task/coverage_test.go` | No — must keep passing unchanged | `validateBlockedByLocked` `pkg/task/blocked_by.go:55` untouched by tag/plan_id/criteria additions |
| `AdvanceBlockedDependents` auto-advance | `pkg/task/store_test.go` | No — keep passing | `blocked_by.go:181`; adding fields must not alter advance semantics |
| `cascadeDeleteEdges` / `DropOrphanEdges` | `pkg/task/store_test.go` | Yes: `TestDelete_CascadesEdges_AND_Evidence` | `Store.Delete` gains an evidence-dir removal step (`store.go:670`); must not regress edge cleanup |
| Atomic write + striped lock | `pkg/task/store_test.go` | No — Plan store reuses the identical pattern | `store.go:109-121` |
| Task normalize field caps (title 200, desc 2000, prompt 10000) | `pkg/task/store_test.go` | Yes: extend to assert tags/criteria caps added, existing caps unchanged | `normalize` `store.go:248` |
| Milestone endpoints/handlers | `pkg/gateway/rest_milestones_test.go`, `pkg/gateway/session_milestone2_test.go` | These tests are DELETED (feature removed); add `TestVerifyContracts_NoMilestoneRefs` + a boot test asserting `/milestones` → 404 | one-way-door removal |
| `SeedConfig` core-agent seeding + Locked re-enforcement | `pkg/coreagent/core_test.go`, `pkg/coreagent/worker_seed_test.go` | Yes: `TestSeedConfig_ExistingCoreAgentsUnaffected_ByJudgeSeed` | Judge seeding must not perturb Mia/Jim/Ava/Ray seeds or default flag |
| Tool-policy coverage boot gate | existing coverage tests (`pkg/config`, `pkg/agent/tool_manifest_test.go`) | Yes: Test 21 (`...WithSeededJudge_NoGaps`) | Judge all-deny must keep the matrix total |
| SEC-26 privilege exemption for `system` | existing SEC-26 tests (`pkg/security`) | Yes: Test 22 asserts `system` NOT privileged | privilege narrowing is an intentional behavior change (ADR §7) |

**New functionality** (Plan store, tags, criteria/evidence/verdict, PlanningConfig): no back-compat regression surface; integration seams protected by the existing task-store tests (`pkg/task/store_test.go`, `pkg/task/coverage_test.go`) and gateway REST harness (`pkg/gateway/rest_test.go`).

---

## Functional Requirements

- **FR-001**: The system MUST provide a `Plan` entity persisted at `~/.omnipus/plans/<id>.json` with atomic write (`WriteFileAtomic` + `WithFlock`) under a per-plan striped lock, mirroring `pkg/task/store.go:109-121`.
- **FR-002**: A Plan MUST carry `id, workspace_id, title, state, owner_agent_id, created_at, updated_at` as required fields; `goal, description, dod, bounds` optional.
- **FR-003**: `Plan.state` MUST be one of `draft, approved, running, done, failed`, defaulting to `draft` on create.
- **FR-004**: The system MUST reject illegal Plan state transitions (per the SD-A2 matrix) with a 400 wrapping `plan.ErrValidation`; `done` MUST be frozen (terminal).
- **FR-005**: A Plan MUST persist loop/judge counters and timestamps (`judge_rounds, active_loop, paused_reason, last_activity_at, approved_at, started_at, completed_at`) as durable fields (ADR D4 MAJ-004).
- **FR-006**: `Task` MUST gain a `plan_id` field; a write with a `plan_id` MUST be rejected 400 unless the referenced Plan exists and shares the task's workspace (mirrors `pkg/tools/task.go:218-232`).
- **FR-007**: Plan membership MUST be computed read-time by scanning `Task.plan_id`; the Plan record MUST NOT store a member list.
- **FR-008**: Deleting a Plan in `running` state MUST be rejected; deleting a non-running Plan MUST clear `plan_id` on member tasks best-effort under the striped lock (SD-A5).
- **FR-009**: `Task` MUST gain a `tags` string array; each tag MUST be normalized (lowercase, trim) on write.
- **FR-010**: The system MUST reject a task write whose normalized tag is empty, exceeds 64 runes, or whose tag count exceeds 16, with a field-naming 400.
- **FR-011**: The system MUST dedup normalized tags (case-fold collisions collapse to one), preserving first-seen order.
- **FR-012**: Tags MUST be workspace-scoped in interpretation only; the system MUST NOT maintain a global tag registry (SD-A8).
- **FR-013**: `Task` MUST gain a `criteria` array of `AcceptanceCriterion`; `Plan` MUST gain a `dod` array of the same type.
- **FR-014**: `AcceptanceCriterion` MUST validate `kind ∈ {check, prose}`, non-empty `text` ≤1000 runes, and a mandatory `author {kind ∈ {agent,user}, id non-empty}`.
- **FR-015**: A `check` criterion MUST require `check.command` non-empty and `check.expected_exit_code ∈ [0,255]`; a `prose` criterion MUST NOT carry a `check` object.
- **FR-016**: Agent tool paths (`create_task`/`update_task`) MUST reject a task with zero criteria and MUST reject an edit reducing criteria below 1 (ADR D5/FR-6); human/UI creation MUST be soft (SD-A7).
- **FR-017**: Agent tool paths MUST reject a create/update whose criteria are all `check` when the assignee's effective `bash` policy is `deny` or `ask` (ADR D2 rule 5).
- **FR-018**: The system MUST record every criterion's author identity at authorship time (ADR D2 rule 3).
- **FR-019**: The system MUST persist an `EvidenceRecord` per check attempt under `$OMNIPUS_HOME` with file mode 0600 and dir 0700 (sessions posture).
- **FR-020**: The system MUST redact `EvidenceRecord.command` and `.output` via `RegisterSensitiveValues` (`pkg/config/security.go:69`) BEFORE the record is written; redaction MUST precede truncation (SD-A13).
- **FR-021**: The system MUST cap per-attempt evidence output size and append a truncation marker with `truncated:true` when exceeded.
- **FR-022**: A check timeout (default 60s) MUST set `timed_out:true` and fail the criterion closed; a `bash` deny / ask→deny MUST set `policy_denied:true` and fail the criterion closed (ADR D2 rules 2,4).
- **FR-023**: The system MUST delete a task's evidence directory when the task is deleted (`Store.Delete`, `pkg/task/store.go:670`).
- **FR-024**: The system MUST produce a `JudgeVerdict` carrying overall `met` plus per-criterion `{criterion_id, met, reason}`, the judge model, and the judge `agent_id` for metering (NFR-5); absence of a verdict MUST NOT default to success (NFR-2).
- **FR-025**: The system MUST add a `judge_verdict` transcript entry type (`pkg/session/daypartition.go:29-40`) written alongside the ADR-043 completion marker.
- **FR-026**: The system MUST run the milestone→tag migration at task-store load, guarded by a `$OMNIPUS_HOME/.milestones_migrated` sentinel (SD-A6); a completed migration MUST be a no-op on reboot.
- **FR-027**: Migration MUST normalize each milestone name (lowercase, trim) THEN truncate reserving prefix (`milestone:`) + `-N` suffix headroom so the final tag never exceeds 64 runes.
- **FR-028**: Migration MUST disambiguate colliding tags with `-2, -3, …` keyed by ascending milestone-ID order, re-checking uniqueness AFTER suffixing (ADR D1 r3).
- **FR-029**: Migration MUST copy a milestone's `due_date` into each member task's `Due` only when `Due` is empty, converting `YYYY-MM-DD` → `YYYY-MM-DDT00:00:00Z` (SD-A12); tasks with their own `Due` MUST keep it.
- **FR-030**: Migration MUST preserve empty milestones (no members) as Info log entries (name + due_date) and MUST NOT create an unattached tag.
- **FR-031**: Migration MUST be crash-safe and idempotent — a re-run after a mid-migration crash MUST produce an identical final state (deterministic mapping from surviving files; delete-dir only after sentinel).
- **FR-032**: The system MUST remove `Task.milestone_id`, `Filter.MilestoneID`, `Patch.MilestoneID`, the entire `pkg/gateway/rest_milestones.go`, the `/milestones` route (`rest_workspaces.go:499-501`), and all `Milestone*` schemas/paths/generated types with no back-compat alias.
- **FR-033**: `SeedConfig` MUST seed exactly one `type:system` Judge agent, `locked:true`, `default:false`, with an explicit all-deny `tools.builtin.policies` covering every static builtin tool.
- **FR-034**: The seeded Judge MUST keep `ValidateToolPolicyCoverage` (`pkg/config/validate.go:491`) gap-free at boot (Constraint #6 total matrix).
- **FR-035**: The system MUST reject creating a `type:system` agent via REST or the `create_agent` tool with a 400 raw-body sniff (mirrors `pkg/gateway/rest.go:2775`).
- **FR-036**: The system MUST reject deleting any `type:system` agent and disabling the seeded Judge (400); only model/provider and rubric edits are accepted.
- **FR-037**: The system MUST narrow `IsPrivilegedAgent` (`pkg/security/ratelimit.go:17-22`) to `core` only, so `type:system` agents are subject to SEC-26 rate limits and cost caps.
- **FR-038**: The system MUST add a `PlanningConfig` section with global defaults `task_max_attempts=3, goal_max_rounds=20, plan_judge_max_rounds=20, loop_max_runs=100, idle_expiry_days=7, global_active_loop_cap=16, check_timeout_seconds=60`, boot-validated, and MUST NOT include any token/money budget field (NFR-1).
- **FR-039**: The system MUST support per-entity bounds overrides on Plan (`plan_judge_max_rounds`, `idle_expiry_days`) and Task (`max_attempts`), resolving per-entity-then-global; every new wire type MUST follow the 5-step spec-first process and pass `make verify-contracts`.

---

## Success Criteria

- **SC-001**: A Plan created via REST round-trips through `~/.omnipus/plans/<id>.json` and re-reads byte-identical after restart (0 data loss).
- **SC-002**: 100% of the SD-A2 transition matrix cells behave as tabulated (accepted/rejected) in `TestPlan_StateTransitions_LegalAndIllegal`.
- **SC-003**: Tag validation rejects all 4 invalid boundary rows (empty, 65-rune, 17-count, whitespace) with a field-naming 400 and accepts all 9 valid rows.
- **SC-004**: A cross-workspace `plan_id` write is rejected 400 in 100% of attempts (race-free — `workspace_id` immutable, mirrors `pkg/tools/task.go:218-232`).
- **SC-005**: Every criterion-validation dataset row (15 rows) yields its tabulated accept/400 result.
- **SC-006**: No `EvidenceRecord` on disk contains a registered sensitive value (grep of the evidence dir after a redaction test yields 0 hits).
- **SC-007**: Evidence output over the size cap is truncated with a marker; on-disk size ≤ cap + marker length.
- **SC-008**: Deleting a task removes 100% of its evidence files.
- **SC-009**: After migration, every previously-milestoned task carries exactly one `milestone:<name>` tag and no `milestone_id`; `jq` finds zero `milestone_id` keys across `~/.omnipus/tasks/*.json`.
- **SC-010**: Migration of a `["Q3","q3"]` fixture yields exactly `milestone:q3` and `milestone:q3-2` (deterministic).
- **SC-011**: A milestone `due_date` is preserved on 100% of member tasks with empty `Due`; 0% of tasks with their own `Due` are overwritten.
- **SC-012**: A second boot after migration modifies 0 task files (sentinel no-op); a simulated mid-migration crash re-run produces a byte-identical task set to a clean run.
- **SC-013**: A fresh install boots with exactly one `type:system` agent and `ValidateToolPolicyCoverage` reporting 0 gaps.
- **SC-014**: `POST /agents{type:system}`, `DELETE /agents/judge`, and `PUT /agents/judge{enabled:false}` each return 400 in 100% of attempts; `make verify-contracts` exits 0 with zero `Milestone*` references remaining.
- **SC-015**: `PlanningConfig` contains zero token/money fields (schema grep) and boot-applies all seven documented defaults when the section is absent.

---

## Spec decisions

- **SD-A1**: **Plan gets a dedicated `pkg/plan` package** (Store mirroring `pkg/task/store.go`), not inline gateway helpers like the milestone store (`rest_milestones.go`). Rationale: the ADR calls Plan "first-class" (D1) with a state machine, DoD, and judge — richer than a milestone; the task-store pattern (striped lock + atomic write + normalize/validate) is the ratified mirror target ("mirroring task store", scope A). Inside ADR bounds ("mirrors the proven milestone store/REST pattern" — a stronger mirror of the same shape).
- **SD-A2**: **Plan transition matrix** as tabulated (draft→approved→running→done/failed; done frozen; failed retryable). The ADR names the five states and their happy order but not every illegal pair; this matrix mirrors the task lifecycle policy (`done` frozen `pkg/task/store.go:483`, `failed` re-queuable) which is the ratified precedent.
- **SD-A5**: **Plan deletion**: reject while `running` (mirrors ADR D4's "deleting an owner with active loops is rejected"); non-running delete clears `plan_id` on members (mirrors `clearMilestoneOnTasks`). The ADR doesn't state plan-delete cascade explicitly; this is the least-surprising, precedent-following resolution.
- **SD-A6**: **Migration idempotency guard = a completion sentinel file** `$OMNIPUS_HOME/.milestones_migrated`, written LAST (before dir removal). Chosen over per-file deletion to keep suffix disambiguation deterministic under partial-crash re-runs (the "surviving subset reorders suffixes" hazard). Config-version bump was the alternative but milestones are separate files, not config. Satisfies ADR D1 "idempotent, crash-safe re-run".
- **SD-A7**: **Tiered DoD** (ADR D5): agent tool path strict (≥1 criterion, cannot edit below 1); human/UI soft (Plan judged against title+goal, Task against title+description when criteria absent). Directly ratified — recorded here for the data model (criteria may be empty on human-authored records; the judge falls back).
- **SD-A8**: **"Workspace-scoped namespace" for tags = interpretation scope only**, no global registry; identical tag strings across workspaces are unrelated. The ADR says "workspace-scoped namespace" without a registry; a registry would be over-engineering for free-form strings (ADR D1: "free-form strings … convention only").
- **SD-A9**: **Criterion `prose` MUST NOT carry a `check` object** (400 on mixed shape). The ADR defines two kinds; forbidding the mixed shape keeps the discriminator clean and the judge path unambiguous.
- **SD-A10**: **Evidence stored under `$OMNIPUS_HOME/tasks_evidence/<task_id>/`** (dir per task) so task-delete cascade is a single `RemoveAll`. ADR says "stored under $OMNIPUS_HOME … deleted with the task"; the per-task subdir is the mechanical realization.
- **SD-A11**: **System-agent create rejection uses the raw-body sniff** (`"type":"system"`) exactly as ADR-035/037 reject `sandbox_profile`/`delegation_policy` (`pkg/gateway/rest.go:2775`). ADR D3 r2 cites that precedent explicitly.
- **SD-A12**: **`due_date` (YYYY-MM-DD) → `Due` (RFC3339)** conversion appends `T00:00:00Z` (UTC midnight). `Task.Due` is `format: date-time` (`Task.yaml`); the milestone `due_date` is a date; midnight-UTC is the lossless canonical lift.
- **SD-A13**: **Redaction precedes truncation** for evidence, so a secret straddling the size-cap boundary is still fully scrubbed. ADR D2 lists both rules but not their order; scrubbing-first is the only safe order.
- **SD-A14**: **`Message.type` gains `judge_verdict` + an optional embedded `verdict` object** rather than a wholly separate transcript stream, so the verdict sits inline next to the worker's ADR-043 marker (ADR §6 "written alongside … so the two cannot silently disagree"). Backend `EntryType` const is the source of truth (`pkg/session/daypartition.go`).

---

## Assumptions

- The runtime plan engine, TaskExecutor goal-loop wrapping, judge invocation, `/goal`+`/loop` command execution, ActivityPanel span rendering, and SPA board/plan screens are specified by sibling agents; this document supplies only the schemas, stores, validation, config, seeding, and migration those consume.
- `modernc.org/sqlite`/CGo posture is unaffected — Plan/tag/criteria storage is pure file-based JSON like tasks.
- The existing `pkg/task` striped-lock (`TaskFileLock`) and `fileutil` primitives are reused verbatim; a parallel `plan.StripedLock` pool is instantiated for plans (same type).

## Clarifications

### 2026-07-19

- Q: Does Plan store membership? → A: No — membership is the read-time reverse index over `Task.plan_id`, mirroring the milestone `MilestoneID` counting pattern (`pkg/gateway/rest_milestones.go:153`).
- Q: Are there token/money bounds anywhere? → A: No — `PlanBounds` and `PlanningConfig` carry count + calendar bounds only (NFR-1).
- Q: Is the `system` wire enum value new? → A: No — it already exists in `Agent.yaml:33` and `config.AgentTypeSystem` (`pkg/config/config.go:823`); this epic revives it as a live seeded category and narrows its privilege (ADR D3).

---

# Part B — Runtime Engine, Judge, Goal Loops & Commands

## Grounding Facts (verified code seams — cited throughout)

| # | Seam | Location | What it does today | How Section B extends it |
|---|------|----------|--------------------|--------------------------|
| G1 | `TaskExecutor.finishTaskRun` | `pkg/agent/task_executor.go:250` | Single-shot: parses ADR-043 marker, calls `completeTaskWithResult` → terminal `done`/`failed`. No retry. | Marker becomes a **claim**; judge adjudicates before terminal; unmet → re-dispatch (new attempt). |
| G2 | `completeTaskWithResult` | `task_executor.go:361` | Writes one of two terminal statuses, archives session, runs `onTaskComplete`. | Gated behind judge verdict; only fires on judge-`met` or attempts-exhausted-fail. |
| G3 | `buildPrompt` | `task_executor.go:462` | Builds task prompt + ADR-043 marker instruction (dispatch-aware). | Appends per-criterion steering (unmet reasons) on attempts ≥2. |
| G4 | `parseTaskCompletionSignal` | `pkg/agent/task_completion_signal.go:274` | Tri-state verdict (`verdictNotFound`/`Success`/`Failure`), fail-closed on absence. | **EXTEND not fork** — verdict feeds the judge as the worker's *claim*, never the terminal decision. |
| G5 | `ExecuteTask` | `task_executor.go:89` | Claims `next→in_progress`, dispatch-sema gated, launches `runTask` goroutine. | Plan engine + attempt loop re-enter this path per attempt/per-ready-task. |
| G6 | `onTaskComplete` → `AdvanceBlockedDependents` → `advanceBlockedTasks` | `task_executor.go:528`, `pkg/task/blocked_by.go:181`, `task_executor.go:642` | DAG auto-advance: `done` dep unblocks dependents, dispatches ready ones. | Plan engine consumes the same advance to dispatch ready plan tasks server-side (D4 hybrid). |
| G7 | `IsPrivilegedAgent` | `pkg/security/ratelimit.go:21-23` | Returns true for `core` **or** `system`. | **Narrow to `core` only** (D3/CRIT-002); `system` becomes rate-limited + cost-capped. |
| G8 | SEC-26 gates | `loop.go:6289` (LLM/hr), `:6321` (daily cost cap), `:7880` (tool/min); `RecordSpend` post-call `:7334` | All four pass `ts.agent.AgentType` to `security.IsPrivilegedAgent` / `RecordSpend` (`AgentType` field `config.go:821`, resolved via `ResolveType() :895`). Test `TestIsPrivilegedAgent` `loop_wave4_test.go:109`. | After G7 narrowing, type-`system` (Judge) passes the `!IsPrivileged` test → is throttled/capped AND its spend recorded. |
| G9 | `resolveDefaultAgentID` / `pickAgentID` | `pkg/routing/route.go:339`, `:298` | Default = first `Default && IsChatTarget()`; workers skipped via `IsChatTarget()`. | Type-`system` must be `IsChatTarget()==false` ⇒ excluded from fallback + binding targets. |
| G10 | `UnifiedMeta` / `MetaPatch` / `TaskID` | `pkg/session/unified.go:73`, `:59`, `:62` | Session meta carries `TaskID *string` via `MetaPatch`. | `/goal` condition + round state stored the same way (new `MetaPatch` fields, agent A schema). |
| G11 | Cron `ScheduledRunner` / `SessionMode` / retry backoff / overlap guard | `pkg/cron/service.go:100`, `:32-40`, `defaultRetryBackoffMs :71` = `[60000,120000,300000]`, `CronJobState.Running :140` | Fires owned jobs; `continue`/`isolated`/`main` modes; transient backoff; overlap guard skips a fire while `Running`. | `/loop` interval → `every`+`continue`; self-paced → one-shot `at`; idle-sweeper + plan overlap guard reuse these. |
| G12 | `async_notifier` → `PublishInbound` | `pkg/agent/async_notifier.go:258` | Synthesises inbound `Channel:"system"` message; comment anticipates "a Goals feature" (`:292`). | Owner wake at decision points publishes through this exact seam. |
| G13 | `turnLoop` / hard ceiling | `loop.go:6257`, `:6272` (`2 * ts.agent.MaxIterations`) | Per-turn iteration ceiling. | Loop **round** hard-ceiling mirrors `2×` the configured round bound (D7). |
| G14 | `pendingResults` drain / `followUps` re-publish | `loop.go:6372`, `:5784` | Sub-turn results injected; follow-ups re-published post-turn. | Attempt/round re-dispatch reuses the follow-up + steering-injection path. |
| G15 | `handleCommand` / `applyExplicitSkillCommand` / `applyMemoryCommandPrompt` | `loop.go:9582`, `:9693`, `:9770` | Slash-command dispatch + rewrite-hook precedent; runtime via `buildCommandsRuntime :9794`. | `/goal` + `/loop` register as `pkg/commands.Definition` (struct `pkg/commands/definition.go:26`; `Delivery DeliveryMode :40`, `DeliveryAgent` `surface.go:43`; `Handler` `request.go:8`). The **agent-delivery / Handler-less** precedent is `rememberCommand` (`cmd_memory.go:29`, `Delivery:DeliveryAgent :35`, `Handler:nil :36`) — the loop rewrites the turn instead of replying inline. Server-side parse works on any channel (Gap #4). |
| G16 | Machine check exec path | assignee agent's `bash` tool machinery (registry/policy/sandbox/audit) | Existing `bash` tool with per-agent policy resolution. | Judge machine checks dispatch **exclusively** through this path (D2/CRIT-003 rule 1). Parallel exec path forbidden. |

---

## User Stories & Acceptance Criteria

### User Story 5 — Task execution runs as a judged attempt-loop (Priority: P0)

A workspace **owner** dispatches a task with acceptance criteria and expects the system to keep the worker honest: the worker's "I'm done" is only a **claim**, and the task is not marked `done` until an independent judge confirms the criteria are actually met against real evidence. When the worker's claim is unmet, the system re-dispatches the SAME task with the judge's per-criterion reasons fed forward as steering, up to a bounded number of attempts (default 3), then wakes the owner rather than silently succeeding or looping forever. Today `TaskExecutor` is single-shot (`task_executor.go:250` `finishTaskRun` → `completeTaskWithResult` → terminal) and the ADR-043 marker (`task_completion_signal.go:274`) is the terminal decision — a worker that emits `TASK_STATUS: success` is trusted outright. This story converts that marker into a claim adjudicated by the judge (US-6).

**Why this priority**: This is the #1 documented failure mode in the ADR decision criteria (§4: "workers claiming done"). Without it, acceptance criteria on the task data model (Section A) have no runtime that enforces them; the whole feature is inert. P0.

**Independent Test**: Dispatch a task with one always-failing machine criterion and a worker stub that emits `TASK_STATUS: success`; assert the task is re-dispatched exactly `attempts-1` times, then lands `failed` with an owner-wake system message — never `done`.

**Acceptance Scenarios**:
1. **Given** a task with ≥1 acceptance criterion and `attempt_count=0`, **When** the worker turn ends with a success claim and the judge returns `met`, **Then** the task is marked `done` (via the existing `completeTaskWithResult` terminal path, `task_executor.go:361`) and the DAG auto-advance (`onTaskComplete`, `:528`) fires exactly once.
2. **Given** a task on attempt N (`N < max_attempts`), **When** the judge returns `unmet` with per-criterion reasons, **Then** `attempt_count` is incremented and persisted, the task is re-dispatched, and the next `buildPrompt` (`:462`) carries the unmet reasons as steering.
3. **Given** a task that has consumed `max_attempts` (default 3) attempts all `unmet`, **When** the final judge verdict lands, **Then** the task is marked `failed`, a handover summary is written to the task record AND the owning session transcript (NFR-3), and the owner agent is woken via the async-notifier system message (`async_notifier.go:258`).
4. **Given** a worker that emits NO parseable `TASK_STATUS` line, **When** `finishTaskRun` reaches the no-signal branch (`:322`), **Then** the run is treated as an `unmet` claim (attempt consumed, re-dispatch or owner-wake) — NOT terminally failed on the spot (behaviour change from today's `:339` `completeTaskWithResult(false)`).
5. **Given** a native agent that calls `update_task(status:"done")` explicitly, **When** `finishTaskRun` observes the terminal status (`:300` `task.IsTerminal`), **Then** that self-certification is ALSO routed to the judge as a claim, not accepted as the terminal decision (SD-B2).
6. **Given** the judge call itself is unavailable (throttled / cost-capped / provider error / timeout), **When** the attempt's judge cycle runs, **Then** the loop pauses and retries with cron backoff `60/120/300s` (`service.go:71`), the attempt is **NOT** consumed, no verdict is recorded, and the idle-expiry clock keeps running (D7 unavailability paragraph).

---

### User Story 6 — Evidence-ladder judge adjudicates claims fail-closed (Priority: P0)

The **judge** (a seeded, non-privileged System Agent — see US-cross) evaluates each completion claim on an evidence ladder: machine-checkable criteria run as real commands whose exit codes are unfakeable evidence; prose criteria get a per-criterion `{met, reason}` from a no-tools structured LLM call whose primary inputs are the machine-check evidence and workspace file diffs, with the worker's own summary provided LAST and unevidenced claims scored unmet. Machine checks dispatch EXCLUSIVELY through the assignee agent's existing `bash` tool machinery (same registry/policy/sandbox/audit) — building a parallel judge-owned exec path is forbidden. Everything fails closed: `ask`-policy resolves to deny (no interactive approver mid-loop), `deny` fails the criterion, a check timeout (default 60s) fails the criterion, and absent evidence never defaults to success. The offline scorer at `evals/judge/scorer.go` is the reusable prior art (eval-only today).

**Why this priority**: The judge is the enforcement organ US-5 depends on; a judge that can be fooled or that runs checks on a bypass path re-opens the self-certification hole and the check-command attack surface. P0.

**Independent Test**: Give the judge one machine criterion `exit 1` and one prose criterion the worker only *asserts* satisfying; assert both score `unmet`, the machine check ran through the assignee's `bash` policy (audit event present), and no judge-owned exec occurred.

**Acceptance Scenarios**:
1. **Given** a machine criterion `command + expected exit code` and the assignee's effective `bash` policy is `allow`, **When** the judge evaluates, **Then** the command runs through the assignee's `bash` tool machinery (G16), the real exit code is compared to expected, and an evidence record is persisted (redacted via `RegisterSensitiveValues` before write — MAJ-003).
2. **Given** a machine criterion and the assignee's effective `bash` policy is `ask`, **When** the judge evaluates unattended, **Then** the criterion resolves to **deny → failed** (fail-closed; `ask`==deny, D2 rule 2) with no prompt raised.
3. **Given** a machine check whose process hangs, **When** 60s (default, configurable) elapse, **Then** the check is killed, the criterion is marked **failed**, its output is capped with a truncation marker, and the loop's idle clock is NOT held (D7: "a hung check cannot hold the clock").
4. **Given** a prose criterion, **When** the judge runs, **Then** it is a no-tools structured System-Agent call whose input ordering is evidence-records + file-diffs FIRST and the worker summary LAST, and any claim not backed by evidence scores `unmet` (OBS-003).
5. **Given** a task whose criteria are ALL machine-type and the assignee's effective `bash` policy is `deny` OR `ask`, **When** an agent tool path tries to create/update it, **Then** the write is rejected (structurally unsatisfiable, D2 rule 5); the human UI path warns instead.
6. **Given** a machine check authored by a DIFFERENT agent than the assignee, **When** it would run, **Then** it requires assignee-owner confirmation unless a workspace setting waives it (Gap #2 / SD-B7).

---

### User Story 7 — A Plan runs as a server-coordinated judged loop (Priority: P1)

A plan **owner** groups a task DAG under a Plan with a goal, DoD, and plan-level judge. A hybrid coordinator dispatches ready tasks server-side as the `blocked_by` DAG clears (extending the existing `AdvanceBlockedDependents`+`TaskExecutor` path, G6) and only wakes the owner agent at decision points — attempts exhausted, plan judge failed, or plan complete → synthesis. The plan-level judge is the SAME Judge System Agent with a plan rubric, bounded at 20 rounds. Plan state, all counters, and timestamps are persisted; on boot the engine reconciles from the task store (statuses authoritative, events an optimization). Exactly one engine instance runs, guarded cron-style; a 7-day idle-expiry sweeper rides the cron service. Deleting an owner with active loops is rejected (400); disabling it pauses and later resumes them — no silent week-long stall.

**Why this priority**: Plans are the container FR-1 requires and the multi-task coordination layer; they depend on US-5/US-6 existing first, so P1 (ships in the same epic, after the task loop is proven).

**Independent Test**: Build a 3-task linear DAG under a plan, kill the process mid-run, restart; assert boot reconciliation resumes dispatch from the task-store statuses (no double-dispatch of an already-`in_progress` task, no stuck-`blocked` task whose deps are `done`).

**Acceptance Scenarios**:
1. **Given** a plan with tasks A→B→C and A just reached judged-`done`, **When** the coordinator advances, **Then** B is dispatched server-side via the extended `advanceBlockedTasks` path (`:642`) without waking the owner.
2. **Given** a plan whose last task reached judged-`done`, **When** the DAG is clear, **Then** the plan-level judge evaluates the plan DoD (same Judge agent, plan rubric), and on `met` the owner is woken for synthesis.
3. **Given** the plan judge returns `unmet` for ≤20 rounds, **When** a round completes, **Then** the owner is woken at that decision point (attempts exhausted / plan judge failed).
4. **Given** the process is killed mid-plan, **When** the gateway boots, **Then** the plan engine reconciles from the task store: in-flight `in_progress` tasks are not re-dispatched blindly, `blocked` tasks whose deps are all `done` are advanced, and plan counters are restored from persisted fields (D4/MAJ-004).
5. **Given** two engine-start attempts race (hot reload), **When** the second tries to start, **Then** the single-instance overlap guard (cron-style, mirroring `CronJobState.Running`) makes it a no-op.
6. **Given** an owner agent owns ≥1 active plan/goal-loop, **When** a delete is requested, **Then** it is rejected 400; **When** a disable is requested, **Then** the owned loops pause (plan surfaces a blocked state) and resume on re-enable (D4/R2-MIN-005).
7. **Given** a plan with no state transition, attempt, or user interaction for 7 days, **When** the idle sweeper runs, **Then** the plan is wound down gracefully (handover written) and marked expired.

---

### User Story 8 — `/goal <condition>` drives a proof-driven session loop (Priority: P1)

A user in any chat session types `/goal <condition>` to make the agent iterate until a plain-prose condition is judged satisfied. Each **round** = one worker turn + its judge evaluation (D7/MIN-001); the judge's reason feeds forward as steering into the next round. Default bound 20 rounds; 7-day idle expiry; one active `/goal` per session, replace-on-set. `/goal status` shows condition, elapsed, rounds used, token spend (visible, never enforcing — NFR-1), latest judge reason, and `active loops: N/cap`. `/goal clear` (plus aliases) stops it. Condition + round state persist in `UnifiedMeta` following the `TaskID` precedent (`unified.go:62`). Server-side parse in `handleCommand` (`loop.go:9582`) makes it work on any text channel (Gap #4); the SPA palette is web-only.

**Why this priority**: The headline user-facing command; depends on the judge (US-6) and the round/brake machinery. P1.

**Independent Test**: Set `/goal make tests pass` against a worker that satisfies the condition on round 3; assert exactly 3 rounds ran, each non-final round injected the prior judge reason as steering, and the goal cleared itself on `met`.

**Acceptance Scenarios**:
1. **Given** a session with no active goal, **When** a USER types `/goal <condition>`, **Then** the condition + `rounds_used=0` are stored on the session meta (via extended `MetaPatch`, agent A schema) and the loop begins.
2. **Given** an active goal, **When** a round's judge returns `unmet`, **Then** `rounds_used` increments and the judge reason is injected as steering into the next worker turn (reusing the steering-injection path around `loop.go:6386`).
3. **Given** an active goal that reaches 20 rounds unmet, **When** the 20th round's judge lands, **Then** the loop stops, writes a handover to the session transcript, and the goal is cleared with a bound-reached status.
4. **Given** an active goal, **When** the user types `/goal status`, **Then** the reply shows condition, elapsed wall-clock, `rounds_used/bound`, cumulative token spend (visible-only), latest judge reason, and `active loops: N/cap`.
5. **Given** an active goal, **When** the user types `/goal clear` (or an alias) or the task/plan card Clear button, **Then** the loop stops immediately (finishing the current step) and the session meta goal fields are cleared.
6. **Given** an active goal, **When** the user types `/goal <new condition>`, **Then** the existing goal is replaced (one per session) and rounds reset.

---

### User Story 9 — `/loop` runs a time-driven recurring session (Priority: P2)

A user types `/loop` to run a session on a cadence. **Interval mode** (`/loop every 5m …`) schedules a cron `every` job in `continue` session mode (`service.go`); **self-paced mode** (`/loop` with no interval) lets the agent pick its own next delay plus a stated reason and schedules a one-shot `at` job each time. Default bound 100 runs; 7-day expiry; `/loop status` and `/loop stop` verbs. Loops obey existing session ownership/auth (Gap #5); a channel-originated loop runs under the routed agent and that session's brakes.

**Why this priority**: Time-driven iteration is valuable but orthogonal to the proof-driven core; it rides existing cron infra with the least new machinery. P2.

**Independent Test**: `/loop every 1m` under a fake clock; advance 100 intervals; assert exactly 100 runs fired in `continue` mode on one session, then the loop auto-expired on the run bound.

**Acceptance Scenarios**:
1. **Given** no active loop on a session, **When** a USER types `/loop every <interval> <prompt>`, **Then** a cron job is created with `EveryMS` + `SessionMode=continue` + owner = the session's agent, and `run_count=0`.
2. **Given** an active self-paced loop, **When** a run completes, **Then** the agent's chosen next delay + reason schedule a one-shot `at` job (`AtMS`), and `run_count` increments.
3. **Given** an active loop at `run_count=99`, **When** the 100th run fires, **Then** it executes and the loop then stops (bound reached) with a wind-down handover.
4. **Given** an active loop untouched for 7 days, **When** the idle sweeper runs, **Then** the loop is expired and its cron job removed.
5. **Given** an active loop, **When** the user types `/loop status`, **Then** the reply shows mode, interval/next-delay, `run_count/bound`, elapsed, and `active loops: N/cap`; **When** `/loop stop`, **Then** the cron job is removed and the loop cleared.

---

### User Story (Cross-cutting) — Autonomy is origin-gated, capped, and privilege-narrowed (Priority: P0)

*(Enforcement threaded across US-5..US-9; captured as its own story for traceability of the security-critical rules.)*

**Why this priority**: These rules bound the blast radius the ADR §7 flags as one-way doors (privilege narrowing) and runaway risk (loops spawning loops, global cap). P0 — they must land WITH the features, not after.

**Independent Test**: Feed a cron-injected inbound message whose `Content` begins `/goal …`; assert it is inert (no goal started); feed the identical text from a user origin; assert a goal starts.

**Acceptance Scenarios**:
1. **Given** an inbound message whose origin is system / cron / async-injected, **When** its `Content` begins `/goal` or `/loop`, **Then** the command is inert (discriminates on origin, not surface — Gap #8/r2), enforced at `handleCommand`.
2. **Given** a task run or delegated sub-turn, **When** its worker emits `/goal`/`/loop` text, **Then** it cannot start a goal or loop (loops-spawning-loops prohibition, Gap #8).
3. **Given** 16 active loops (goal + loop + running plans; task attempt-loops inside a running plan are bounded by the plan, not counted individually), **When** a 17th start is attempted, **Then** it is rejected with a cap message; status output shows `active loops: 16/16`.
4. **Given** a type-`system` agent (the Judge), **When** it makes LLM/tool calls, **Then** it is subject to per-agent rate limits and the daily cost cap (SEC-26 gates at `loop.go:6289/:6321/:7880` after narrowing `IsPrivilegedAgent` to `core`-only, `ratelimit.go:21-23`).
5. **Given** a type-`system` agent, **When** default-agent fallback, routing-binding writes, delegation pickers, `list_agents`, or team rosters enumerate agents, **Then** the System Agent is excluded (`IsChatTarget()==false`, `route.go:339`; binding-target write → 400).
6. **Given** a fresh install choosing the Judge model at onboarding fails to record a choice, **When** the Judge resolves its model, **Then** it falls back to the default agent's model (Gap #1 — no "cheapest" heuristic).

---

## Behavioral Contract

**Primary flows**:
- When a worker turn ends with a completion **claim** (ADR-043 marker success OR explicit `update_task(done)`), the runtime routes the claim to the judge (US-6) rather than terminating the task directly.
- When the judge returns `met`, the task/goal/plan reaches its terminal `done`/satisfied state via the existing `completeTaskWithResult` path (`task_executor.go:361`).
- When the judge returns `unmet`, the unit's attempt/round counter increments (persisted), the unmet per-criterion reasons are fed forward as steering, and the unit re-dispatches.
- When a plan's `blocked_by` DAG clears a dependency, the server-side coordinator dispatches the newly-ready tasks (extending `AdvanceBlockedDependents`→`advanceBlockedTasks`, `blocked_by.go:181`/`task_executor.go:642`) without waking the owner.
- When a decision point is reached (attempts exhausted / plan judge failed / plan complete → synthesis), the owner agent is woken via the async-notifier system inbound message (`async_notifier.go:258`).

**Error flows**:
- When the judge call itself is unavailable (SEC-26 throttled / cost-capped / provider error / timeout), the loop pauses and retries on cron backoff `60/120/300s` (`service.go:71`); the attempt/round is NOT consumed; the pause surfaces in status; the idle clock keeps running.
- When a machine check's policy is `ask` or `deny`, or the check times out (60s default) or exceeds the output cap, the criterion is scored **failed** (fail-closed).
- When no parseable completion signal is produced, the run is an `unmet` claim (attempt consumed) — not an immediate terminal failure (behaviour change vs `task_executor.go:339`).
- When a delete targets an owner with active loops, the request is rejected 400.

**Boundary conditions**:
- Attempts: task default **3**; rounds: `/goal` **20**, plan judge **20**; `/loop` **100** runs; **7-day** idle expiry everywhere; hard ceiling mirrors `2×` the configured bound (`loop.go:6272`).
- Global concurrency: default **16** simultaneously-active loops across `/goal` + `/loop` + running plans.
- A round = one worker turn + its judge evaluation (D7/MIN-001).

---

## Edge Cases

- Judge returns `met` on the SAME attempt the idle sweeper would expire the loop → completion wins (terminal state is checked before the sweep acts on the unit).
- A machine check that exits with the expected NON-zero code (e.g. a criterion "command must fail") → compared against the criterion's declared expected exit code, not hardcoded 0.
- Worker calls `update_task(done)` then ALSO emits `TASK_STATUS: failure` → the explicit terminal write is the claim; the marker is not re-read (mirrors today's `:300` precedence), and the claim is `done` → judged (SD-B2).
- Owner disabled mid-round while the judge is mid-call → the in-flight round finishes and writes its verdict; the loop then enters paused, not consuming a fresh attempt.
- Two plan tasks become ready simultaneously → both dispatched (each gated by the existing dispatch semaphore, `task_executor.go:108`); neither wakes the owner.
- Recurring `Trigger` task fires while its previous run's attempt-loop is still active → each fire is a fresh run with its own attempt loop; criteria are immutable per run (Gap #7). Overlap of runs bounded by the same dispatch/concurrency guards.
- `/goal` set on a session that is itself a task-run session → rejected (origin/surface gate: task runs cannot start goals, Gap #8).
- 15th, 16th, 17th loop start → 15 succeeds, 16 succeeds (fills cap), 17 rejected.
- Idle-expiry at exactly 7d vs 6d23h59m vs 7d+1s → 6d23h ok (still active), ≥7d expires.

---

## Explicit Non-Behaviors

- The system MUST NOT mark a task/goal/plan satisfied on a worker claim without a judge `met` verdict — self-certification is the failure mode being closed.
- The system MUST NOT run machine checks on any exec path other than the assignee agent's `bash` tool machinery — no parallel judge-owned executor (D2 rule 1).
- The system MUST NOT prompt an interactive approver for an `ask`-policy check mid-loop — `ask` resolves to deny (D2 rule 2).
- The system MUST NOT enforce any token or money brake — spend is visible only (NFR-1); bounds are count + calendar only.
- The system MUST NOT consume an attempt/round when the judge call itself was unavailable (D7 unavailability rule).
- The system MUST NOT let a system/cron/async-injected message start a `/goal` or `/loop`, nor let a task run or delegated sub-turn start one (Gap #8).
- The system MUST NOT treat type-`system` agents as privileged — they are rate-limited and cost-capped (D3/CRIT-002).
- The system MUST NOT allow a type-`system` agent to be a chat target, default-fallback, routing-binding target, delegation target, or team member (D3/MAJ-001).
- The Judge MUST NOT be disable-able or deletable; the operator escape hatch is stopping loops (D8), not switching off the judge (r3).

---

## Integration Boundaries

### Judge System Agent (internal LLM)
- **Data in**: acceptance criteria (machine evidence records + prose criteria), workspace file diffs, worker summary (last), rubric prompt.
- **Data out**: per-criterion `{met, reason}` + overall verdict, as a structured (JSON) result; written to the session transcript as a dedicated **judge-verdict entry type** alongside the worker's ADR-043 marker so the two cannot silently disagree (§6/review Q3).
- **Contract**: no-tools structured call under the System-Agent identity; metered/rate-limited/cost-capped like any non-core agent; usage attributed to the System Agent's `agent_id` with plan/task/goal correlation IDs (NFR-5).
- **On failure**: unavailable (throttle/cap/500/timeout) ⇒ pause + backoff, attempt not consumed (D7). Ran-but-empty/negative ⇒ fail-closed unmet (NFR-2).
- **Development**: real internal agent. Reuse the eval judge's proven shape — the structured JSON contract + 5-dimension rubric live in `evals/judge/prompt.go:15` (`JudgeTemplate`, "Return ONLY valid JSON") / `RenderPrompt` (`prompt.go:83`); parsing/validation is `evals/judge/scorer.go` (`Scores` struct `:32-39`, `Parse` `:115`, `Score` scalar `:13`, clamps FP noise but errors outside [0,1]). NOTE: `scorer.go` is **parse-only** — it does NOT call an LLM; the provider call in the eval path is in `evals/cmd/eval-runner`. The runtime judge adapts this to a per-criterion `{met, reason}` rubric, not the eval's 5 dimensions. Model resolved at onboarding; fallback = default agent's model (Gap #1).

### Assignee agent `bash` tool machinery (machine checks)
- **Data in**: check command + expected exit code + per-check timeout (default 60s) + output cap.
- **Data out**: real exit code + captured (redacted, capped) output → evidence record.
- **Contract**: dispatched through the SAME tool registry/policy/sandbox/audit as a normal `bash` call (G16). Policy `allow` runs; `ask`→deny; `deny` fails closed.
- **On failure**: timeout/oversize/denied ⇒ criterion failed closed; a hung check cannot hold the idle clock.

### Cron service (schedules, sweeper, `/loop`)
- **Data in**: `/loop` interval (`EveryMS`, `continue` mode) or self-paced one-shot (`AtMS`); idle-sweeper tick.
- **Data out**: `RunScheduled` fire outcome (string, error) recorded to `CronJobState`.
- **Contract**: `ScheduledRunner` seam (`service.go:100`); overlap guard (`CronJobState.Running :140`); transient backoff `60/120/300` (`:71`).
- **On failure**: transient ⇒ backoff retry; owner-missing ⇒ skip (`onSkip` seam `:221`).

---

## BDD Scenarios

### Feature: Task goal-loop (US-5)

#### Scenario: Worker success claim confirmed by judge marks task done
**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a task with one machine criterion `exit 0` and `attempt_count=0`
- **And** a worker that emits `TASK_STATUS: success`
- **When** the worker turn ends and the judge evaluates the claim
- **Then** the judge returns `met`
- **And** the task is marked `done` via `completeTaskWithResult` (`task_executor.go:361`)
- **And** `onTaskComplete` DAG auto-advance fires exactly once
- **But** the task is never marked `done` on the marker alone

#### Scenario: Unmet claim re-dispatches with reasons fed forward
**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** a task on attempt 1 with a prose criterion the worker only asserts
- **When** the judge returns `unmet` with reason "no evidence for X"
- **Then** `attempt_count` becomes 2 and is persisted
- **And** the task re-dispatches
- **And** the next `buildPrompt` output contains the reason "no evidence for X" as steering

#### Scenario: No completion signal is an unmet claim, not a terminal fail
**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Edge Case
- **Given** a task on attempt 1
- **When** the worker produces output with no parseable `TASK_STATUS` line
- **Then** the run is treated as `unmet` (attempt consumed, re-dispatch)
- **But** the task is NOT immediately marked `failed` (contrast `task_executor.go:339` today)

#### Scenario: Explicit update_task(done) is still judged
**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Edge Case
- **Given** a native agent that calls `update_task(status:"done")`
- **When** `finishTaskRun` observes the terminal status (`:300`)
- **Then** the completion is routed to the judge as a claim
- **But** it is not accepted as the terminal decision without a `met` verdict

#### Scenario Outline: Attempt-count boundary and hard ceiling
**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Edge Case
- **Given** a task with `max_attempts=<max>` and every attempt judged `unmet`
- **When** the loop runs to exhaustion
- **Then** the task ends `<terminal>` after exactly `<dispatches>` worker dispatches
- **And** the owner is woken `<owner_wake>`

**Examples**:

| max | dispatches | terminal | owner_wake |
|-----|-----------|----------|------------|
| 0   | 1 (floor-clamped to ≥1) | failed | yes |
| 1   | 1 | failed | yes |
| 2   | 2 | failed | yes |
| 3   | 3 | failed | yes |
| 4   | 4 | failed | yes |
| 3 (hard-ceiling probe: injects extra pending re-dispatch) | ≤ 2×3=6 then unconditional stop | failed | yes |

#### Scenario Outline: Judge-unavailability does not consume an attempt
**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Error Path
- **Given** a task on attempt 1 and the judge call fails with `<cause>`
- **When** the attempt's judge cycle runs
- **Then** the loop pauses and schedules a retry after `<backoff_ms>` ms
- **And** `attempt_count` stays 1
- **And** no judge verdict is recorded
- **And** the idle-expiry clock keeps running

**Examples**:

| cause | backoff_ms |
|-------|-----------|
| SEC-26 rate throttle (1st) | 60000 |
| cost-cap denial (2nd) | 120000 |
| provider 500 (3rd) | 300000 |
| judge call timeout (4th, schedule exhausted) | normal cadence, terminal-retry recorded, still no attempt consumed |

---

### Feature: Evidence-ladder judge (US-6)

#### Scenario Outline: Machine-check policy triad under unattended judge
**Traces to**: User Story 6, Acceptance Scenarios 1–2
**Category**: Error Path / Edge Case
- **Given** a machine criterion and the assignee's effective `bash` policy is `<policy>`
- **When** the judge evaluates unattended
- **Then** the check `<runs>` and the criterion is `<result>`
- **And** no interactive approver is prompted

**Examples**:

| policy | runs | result |
|--------|------|--------|
| allow  | yes, via assignee bash machinery | met if exit==expected, else unmet |
| ask    | no (ask→deny) | failed (fail-closed) |
| deny   | no | failed (fail-closed) |

#### Scenario Outline: Per-check timeout boundary
**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Edge Case
- **Given** a machine check configured with a 60s timeout
- **When** the check runs for `<duration>`
- **Then** the criterion result is `<result>` and the idle clock is `<clock_held>`

**Examples**:

| duration | result | clock_held |
|----------|--------|-----------|
| 59s (completes) | met/unmet by exit code | no |
| 60s (killed at deadline) | failed (timeout) | no |
| hung indefinitely | failed (killed at 60s) | no |

#### Scenario: Prose judge scores unevidenced claim unmet
**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Happy Path
- **Given** a prose criterion and a worker summary asserting success with no supporting evidence
- **When** the judge runs as a no-tools structured call
- **And** the input ordering is evidence-records + file-diffs first, worker summary last
- **Then** the criterion scores `unmet` (OBS-003)

#### Scenario: All-machine criteria with unsatisfiable bash policy rejected at write
**Traces to**: User Story 6, Acceptance Scenario 5
**Category**: Error Path
- **Given** a task whose criteria are all machine-type
- **And** the assignee's effective `bash` policy is `ask`
- **When** an agent tool path attempts to create/update the task
- **Then** the write is rejected (structurally unsatisfiable, D2 rule 5)
- **But** the same shape via the human UI path is accepted with a warning

#### Scenario: Cross-agent machine check requires confirmation
**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Edge Case
- **Given** a machine check authored by agent B on a task assigned to agent A
- **And** no workspace waiver is set
- **When** the check would run
- **Then** it requires assignee-owner (A) confirmation before executing (Gap #2)
- **And** with a workspace waiver set it runs without confirmation

#### Scenario: Evidence is redacted before persistence
**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Edge Case
- **Given** a machine check whose output contains a registered sensitive value
- **When** the evidence record is persisted
- **Then** the sensitive value is redacted (`RegisterSensitiveValues`, ADR-004 flow) before write (MAJ-003)
- **And** the record is size-capped with a truncation marker and deleted with the task

---

### Feature: Plan engine (US-7)

#### Scenario: Server-side advance dispatches ready task without owner wake
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a plan with A→B→C and A just reached judged-`done`
- **When** the coordinator advances the DAG
- **Then** B is dispatched via `advanceBlockedTasks` (`task_executor.go:642`)
- **But** the owner agent is not woken

#### Scenario: Boot reconciliation resumes from task store
**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Error Path
- **Given** a plan with A(`done`) → B(`in_progress`) → C(`blocked`) when the process is killed
- **When** the gateway boots
- **Then** B is not re-dispatched blindly (already `in_progress`)
- **And** C stays `blocked` until B reaches judged-`done`
- **And** plan counters are restored from persisted fields (D4/MAJ-004)

#### Scenario: Single-instance overlap guard on hot reload
**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Edge Case
- **Given** a running plan engine instance
- **When** a config hot-reload attempts to start a second engine
- **Then** the overlap guard (mirroring `CronJobState.Running`) makes the second start a no-op

#### Scenario Outline: Owner lifecycle during active loops
**Traces to**: User Story 7, Acceptance Scenario 6
**Category**: Error Path
- **Given** an owner agent owning `<active>` active plans/goal-loops
- **When** the operator requests `<op>`
- **Then** the result is `<result>`

**Examples**:

| active | op | result |
|--------|-----|--------|
| 1 | delete | 400 rejected (reassign or stop first) |
| 1 | disable | owned loops pause; plan surfaces blocked; resume on re-enable |
| 0 | delete | allowed |

#### Scenario Outline: Plan-state illegal transitions rejected
**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Edge Case
- **Given** a plan in state `<from>`
- **When** a transition to `<to>` is attempted
- **Then** it is `<verdict>`

**Examples**:

| from | to | verdict |
|------|-----|---------|
| draft | active | allowed |
| active | judging | allowed |
| judging | synthesizing | allowed (judge met) |
| judging | active | allowed (judge unmet, rounds remain) |
| done | active | rejected (terminal) |
| expired | active | rejected (terminal) |
| stopped | judging | rejected (terminal) |
| synthesizing | draft | rejected (no backward edge) |

#### Scenario: Idle-expiry sweeps a stalled plan
**Traces to**: User Story 7, Acceptance Scenario 7
**Category**: Edge Case
- **Given** a plan with no state transition/attempt/interaction for 7 days
- **When** the cron idle sweeper runs
- **Then** the plan is wound down (handover written to plan record + owning session) and marked expired

---

### Feature: /goal command (US-8)

#### Scenario: User sets a goal and it iterates to met
**Traces to**: User Story 8, Acceptance Scenario 1–2
**Category**: Happy Path
- **Given** a session with no active goal
- **When** a user types `/goal make the tests pass`
- **Then** the condition + `rounds_used=0` are stored on session meta (extended `MetaPatch`)
- **And** each unmet round injects the prior judge reason as steering into the next worker turn

#### Scenario Outline: Round boundary for /goal
**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Edge Case
- **Given** an active goal with bound 20 and every round judged `unmet`
- **When** round `<round>` completes
- **Then** the loop `<state>`

**Examples**:

| round | state |
|-------|-------|
| 19 | continues (under bound) |
| 20 | stops, handover written, goal cleared (bound reached) |
| 21 | unreachable (hard-ceiling 2×20=40 caps any pending-message runaway) |

#### Scenario: /goal status shows visible-only spend
**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Happy Path
- **Given** an active goal that has spent tokens across 3 rounds
- **When** the user types `/goal status`
- **Then** the reply shows condition, elapsed, `rounds_used/20`, cumulative token spend, latest judge reason, and `active loops: N/16`
- **But** the spend is never used to stop the loop (NFR-1)

#### Scenario: Replace-on-set enforces one goal per session
**Traces to**: User Story 8, Acceptance Scenario 6
**Category**: Alternate Path
- **Given** an active goal "A"
- **When** the user types `/goal B`
- **Then** goal "A" is replaced by "B" and rounds reset to 0

#### Scenario Outline: /goal clear aliases
**Traces to**: User Story 8, Acceptance Scenario 5
**Category**: Alternate Path
- **Given** an active goal
- **When** the user types `<verb>`
- **Then** the loop stops after the current step and goal meta is cleared

**Examples**:

| verb |
|------|
| /goal clear |
| /goal stop |
| /goal cancel |
| (task/plan card Clear button) |

---

### Feature: /loop command (US-9)

#### Scenario: Interval-mode loop uses continue session
**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path
- **Given** no active loop on the session
- **When** a user types `/loop every 5m summarize new emails`
- **Then** a cron job is created with `EveryMS=300000`, `SessionMode=continue`, owner = the session's agent, `run_count=0`

#### Scenario: Self-paced loop schedules one-shot at-jobs
**Traces to**: User Story 9, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** an active self-paced loop
- **When** a run completes and the agent states next delay 10m + reason
- **Then** a one-shot cron job with `AtMS = now+600000` is scheduled and `run_count` increments

#### Scenario Outline: /loop run-count boundary
**Traces to**: User Story 9, Acceptance Scenario 3
**Category**: Edge Case
- **Given** an active loop with bound 100
- **When** run `<run>` fires
- **Then** the loop `<state>`

**Examples**:

| run | state |
|-----|-------|
| 99 | executes, continues |
| 100 | executes, then stops (bound reached), handover written |
| 101 | never scheduled |

#### Scenario: /loop stop removes the cron job
**Traces to**: User Story 9, Acceptance Scenario 5
**Category**: Alternate Path
- **Given** an active loop
- **When** the user types `/loop stop`
- **Then** the cron job is removed and the loop cleared

---

### Feature: Cross-cutting enforcement (US-cross)

#### Scenario Outline: Origin gating for /goal and /loop
**Traces to**: US-cross, Acceptance Scenario 1–2
**Category**: Error Path
- **Given** an inbound message with origin `<origin>` whose content begins `/goal`
- **When** `handleCommand` processes it
- **Then** the command is `<outcome>`

**Examples**:

| origin | outcome |
|--------|---------|
| user (real inbound) | starts a goal |
| system / async-injected (`Channel:"system"`, `async_notifier.go:258`) | inert |
| cron-injected | inert |
| delegated sub-turn worker | inert |
| task-run worker | inert |

#### Scenario Outline: Global active-loop cap boundary
**Traces to**: US-cross, Acceptance Scenario 3
**Category**: Edge Case
- **Given** `<active>` active loops (goal + loop + running plans)
- **When** one more loop start is attempted
- **Then** it is `<outcome>`

**Examples**:

| active | outcome |
|--------|---------|
| 15 | starts (now 16) |
| 16 | rejected (cap reached); status shows `16/16` |
| 17 (impossible steady-state; concurrent-start race) | rejected under the single-writer cap guard |

#### Scenario: Type-system agent is rate-limited (SEC-26 narrowing)
**Traces to**: US-cross, Acceptance Scenario 4
**Category**: Error Path
- **Given** the Judge (type `system`) making its Nth LLM call this hour above `MaxAgentLLMCallsPerHour`
- **When** the SEC-26 gate at `loop.go:6289` evaluates `!IsPrivilegedAgent("system")`
- **Then** the gate is active (since `IsPrivilegedAgent` is narrowed to `core`-only, `ratelimit.go:21-23`)
- **And** the call is rate-limited exactly like a non-core agent

#### Scenario Outline: System Agent enumeration exclusions
**Traces to**: US-cross, Acceptance Scenario 5
**Category**: Error Path
- **Given** a seeded type-`system` agent
- **When** surface `<surface>` enumerates agents
- **Then** the System Agent is `<result>`

**Examples**:

| surface | result |
|---------|--------|
| default-agent fallback (`route.go:339`) | excluded (`IsChatTarget()==false`) |
| routing-binding target write | 400 rejected |
| delegation picker / `list_agents` for delegation | excluded |
| workspace team roster | excluded |
| Agents screen "System" section | shown (only here) |
| REST/agent-tool create with type `system` | 400 (raw-body sniff, ADR-035/037 precedent) |
| delete seeded System Agent / disable the Judge | rejected |

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------|-------|---------|
| Unit | attempt/round counters, verdict routing, brake boundaries, origin discrimination, policy triad, cap arithmetic | Validate logic in isolation with fakes/stubs |
| Integration | TaskExecutor↔judge↔store, plan engine↔task store↔cron, command↔handleCommand↔session meta | Validate components together under the fake-clock harness |
| E2E | `/goal` end-to-end on the embedded binary; plan crash-recovery | Full-feature from user view |

### Test Implementation Order

Write BEFORE implementation. Unit → Integration → E2E; within a level, by dependency.

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestGoalLoop_SuccessClaim_JudgeMet_MarksDone` | Unit | Worker success claim confirmed | Judge `met` → `completeTaskWithResult(done)` fires once |
| 2 | `TestGoalLoop_UnmetClaim_IncrementsAttempt_FeedsReasons` | Unit | Unmet claim re-dispatches | attempt++ persisted; `buildPrompt` carries reason |
| 3 | `TestGoalLoop_NoSignal_IsUnmetNotTerminalFail` | Unit | No completion signal | contrast `task_executor.go:339`; regression-sensitive |
| 4 | `TestGoalLoop_ExplicitUpdateTaskDone_StillJudged` | Unit | Explicit update_task(done) judged | `:300` terminal claim routed to judge |
| 5 | `TestGoalLoop_AttemptBoundary` (table 0,1,2,3,4) | Unit | Attempt-count boundary outline | dispatch count vs max; floor-clamp at 1 |
| 6 | `TestGoalLoop_HardCeiling_2xBound` | Unit | Hard ceiling probe | mirrors `loop.go:6272` unconditional stop |
| 7 | `TestJudgeUnavailable_PauseBackoff_NoAttemptConsumed` (table causes) | Unit | Judge-unavailability outline | 60/120/300 backoff; attempt unchanged; idle clock runs |
| 8 | `TestJudge_MachineCheck_PolicyTriad` (allow/ask/deny) | Unit | Policy triad outline | ask→deny, deny→failed, allow→exit-compare; **extend SEC-26/policy tests** |
| 9 | `TestJudge_MachineCheck_TimeoutBoundary` (59/60/hung) | Unit | Per-check timeout outline | kill at 60s; criterion failed; clock not held |
| 10 | `TestJudge_MachineCheck_DispatchesThroughAssigneeBash` | Integration | (D2 rule 1) | asserts audit event on assignee bash path; **no parallel exec** |
| 11 | `TestJudge_Prose_UnevidencedClaimUnmet` | Unit | Prose unevidenced unmet | evidence-first ordering; worker summary last |
| 12 | `TestJudge_AllMachineCriteria_UnsatisfiableBashPolicy_RejectedAtWrite` | Integration | All-machine unsatisfiable | agent tool path 400; UI warns |
| 13 | `TestJudge_Evidence_RedactedAndCappedBeforePersist` | Unit | Evidence redacted | `RegisterSensitiveValues` before write; truncation marker |
| 14 | `TestJudge_CrossAgentCheck_RequiresConfirmation` | Unit | Cross-agent confirmation | waiver waives; default confirms |
| 15 | `TestJudgeVerdict_WrittenAsTranscriptEntryType` | Unit | (§6 Q3) | judge-verdict entry type sits beside ADR-043 marker |
| 16 | `TestPlan_ServerAdvance_DispatchesReady_NoOwnerWake` | Integration | Server-side advance | extends `advanceBlockedTasks` |
| 17 | `TestPlan_BootReconcile_FromTaskStore` (crash matrix) | Integration | Boot reconciliation | no double-dispatch; blocked-with-done-deps advanced |
| 18 | `TestPlan_SingleInstance_OverlapGuard` | Unit | Overlap guard | second start no-op (mirror `CronJobState.Running`) |
| 19 | `TestPlan_OwnerLifecycle` (delete 400 / disable pause-resume) | Integration | Owner lifecycle outline | 400 on delete; pause+resume on disable |
| 20 | `TestPlan_State_IllegalTransitions` (table) | Unit | Illegal transitions outline | terminal states reject; no backward edges |
| 21 | `TestPlan_IdleExpiry_Sweep` (6d23h/7d/7d+1s) | Integration (fake clock) | Idle-expiry sweep | cron sweeper winds down at ≥7d |
| 22 | `TestPlanJudge_20RoundBound` | Unit | (D7 symmetric bound) | plan judge rounds symmetric with /goal |
| 23 | `TestGoal_Set_StoresConditionOnMeta` | Unit | User sets a goal | extended `MetaPatch` write |
| 24 | `TestGoal_RoundBoundary` (19/20/21) | Unit | Round boundary outline | stops at 20; 21 unreachable |
| 25 | `TestGoal_Status_ShowsVisibleSpend` | Unit | /goal status | condition/elapsed/rounds/spend/reason/active-loops |
| 26 | `TestGoal_ReplaceOnSet_OnePerSession` | Unit | Replace-on-set | rounds reset |
| 27 | `TestGoal_ClearAliases` (table) | Unit | /goal clear aliases | clear/stop/cancel + card button |
| 28 | `TestLoop_IntervalMode_ContinueSession` | Integration (fake clock) | Interval-mode loop | `EveryMS` + `continue` |
| 29 | `TestLoop_SelfPaced_OneShotAtJobs` | Integration (fake clock) | Self-paced at-jobs | `AtMS` per run |
| 30 | `TestLoop_RunCountBoundary` (99/100/101) | Integration (fake clock) | Run-count boundary | stop at 100 |
| 31 | `TestLoop_IdleExpiry_7d` | Integration (fake clock) | (D7) | expire + remove cron job |
| 32 | `TestLoop_Stop_RemovesCronJob` | Unit | /loop stop | cron job removed |
| 33 | `TestOriginGating_GoalLoop` (user/system/cron/async/delegated/task) | Unit | Origin gating outline | discriminate on origin, not surface |
| 34 | `TestGlobalCap_ActiveLoopBoundary` (15/16/17) | Unit | Global cap outline | reject 17th; status `16/16` |
| 35 | `TestRoleGating_SessionOwnership` | Unit | (role gating, FR-074) | session ownership/auth gates who may start /goal /loop (Gap #5) |
| 36 | `TestTaskExecutor_ScratchpadExemptFromGoalLoop` | Unit | Scratchpad exemption (R8/FR-048) | set_todos task never enters an attempt loop, never judged |
| 35 | `TestSEC26_TypeSystemAgent_IsRateLimited` | Unit | Type-system rate-limited | **extend `runturn_rate_limit_test.go` / `wave5b_sysagent_ratelimit_test.go`** |
| 36 | `TestSEC26_IsPrivilegedAgent_CoreOnly` | Unit | (D3/CRIT-002) | **extend `pkg/security/ratelimit_test.go`**: `system`→false, `core`→true |
| 37 | `TestSystemAgent_ExcludedFromEnumeration` (surfaces table) | Integration | Enumeration exclusions | fallback/binding/delegation/team/create/delete |
| 38 | `TestSystemAgent_Constraint6_BootCoverage` | Integration | (NFR-4) | seeded all-deny policies keep agent×tool matrix total |
| 39 | `TestJudge_DefaultModel_FallbackToDefaultAgent` | Unit | Judge default model | onboarding choice; fallback = default agent model |
| 40 | `TestRecurringTrigger_EachRun_GetsAttemptLoop_ImmutableCriteria` | Integration | (Gap #7) | per-run attempt loop; criteria immutable per run |
| 41 | `TestGoalLoop_E2E_MakeTestsPass` | E2E | US-8 independent test | embedded binary, 3-round convergence |
| 42 | `TestPlan_E2E_CrashRecovery` | E2E | US-7 independent test | kill mid-plan → boot resume |

### Fake-clock harness pattern

Reuse the deterministic cron harness in `pkg/cron/autonomy_test.go`: `fakeClock` (`:16`) with `Now()` (`:23`) and `Advance(d)` (`:29`); build the service via `newAutonomyService(t, clk)` (`:66`, which calls `cs.SetClock(clk)` `:70` against the `Clock` interface `service.go:24`); seed jobs with `addDueJob` (`:75`). The canonical shape is `TestRunDueJobs_FiresDueJob` (`:90`): setup fakeClock + `newAutonomyService` + inject a recording runner (`:36`) via `cs.SetRunner` + `cs.startNoLoop()` (`:96`, no real ticker); then `clk.Advance(...)` (`:104`) → `cs.RunDueJobs(clk.Now())` (`:105`) → `cs.WaitForLane()` (`:106`) to drain the lane; assert on `runner.calls()` + `jobState(...)`. Drive `RunDueJobs(now)` + `WaitForLane()` directly — NOT `executeJobByID` (`service.go:576`) — no wall-clock sleeps. Idle-expiry, `/loop` run-count, interval firing, and judge-backoff timing tests all mount on this harness. The `SetOnSkip` seam (`service.go:221`) gives a deterministic overlap/owner-missing skip observer for plan-overlap and owner-missing assertions.

### Test Datasets

#### Dataset: Attempt boundaries (task loop)

| # | Input (max_attempts, per-attempt verdict) | Boundary Type | Expected Output | Traces to | Notes |
|---|-------------------------------------------|---------------|-----------------|-----------|-------|
| 1 | max=0 | Zero / floor | 1 dispatch, `failed`, owner woken | Attempt boundary outline | clamp to ≥1; never zero-dispatch |
| 2 | max=1, unmet | Min | 1 dispatch, `failed`, owner woken | Attempt boundary outline | single-shot equivalence |
| 3 | max=2, unmet×2 | Mid | 2 dispatches, `failed` | Attempt boundary outline | |
| 4 | max=3 (default), unmet×3 | Default | 3 dispatches, `failed` | Attempt boundary outline | ADR default |
| 5 | max=3, met on attempt 2 | Happy | 2 dispatches, `done` | Worker success scenario | early success |
| 6 | max=4, unmet×4 | Max+1 vs default | 4 dispatches, `failed` | Attempt boundary outline | above default still honored |
| 7 | hard-ceiling probe (pending re-dispatch injected each attempt) | Overflow | ≤ 2×max then unconditional stop | Hard ceiling scenario | mirrors `loop.go:6272` |

#### Dataset: Round boundaries (/goal, plan judge)

| # | Input (bound, round reached, all unmet) | Boundary Type | Expected Output | Traces to | Notes |
|---|------------------------------------------|---------------|-----------------|-----------|-------|
| 1 | bound=20, round 19 | Max-1 | continues | Round boundary outline | under bound |
| 2 | bound=20, round 20 | Max | stops, handover, cleared | Round boundary outline | bound reached |
| 3 | bound=20, round 21 | Max+1 | unreachable (hard ceiling 40) | Round boundary outline | runaway guard |
| 4 | bound=20, met on round 5 | Happy | stops at 5, satisfied | /goal iterate scenario | early met |

#### Dataset: /loop run counts

| # | Input (bound, run) | Boundary Type | Expected Output | Traces to | Notes |
|---|--------------------|---------------|-----------------|-----------|-------|
| 1 | bound=100, run 99 | Max-1 | executes, continues | Run-count boundary | |
| 2 | bound=100, run 100 | Max | executes, then stops | Run-count boundary | bound reached |
| 3 | bound=100, run 101 | Max+1 | never scheduled | Run-count boundary | |

#### Dataset: Idle expiry (all loop kinds)

| # | Input (idle duration) | Boundary Type | Expected Output | Traces to | Notes |
|---|-----------------------|---------------|-----------------|-----------|-------|
| 1 | 6d23h | Max-ε | still active | Idle-expiry sweep | under 7d |
| 2 | 7d exactly | Max | expired, wound down | Idle-expiry sweep | inclusive at 7d |
| 3 | 7d+1s | Max+ε | expired | Idle-expiry sweep | past bound |
| 4 | idle reset by a state transition at 6d23h | Reset | clock resets, not expired | Idle definition | attempt/transition/interaction resets |

#### Dataset: Machine-check timeout

| # | Input (check duration) | Boundary Type | Expected Output | Traces to | Notes |
|---|------------------------|---------------|-----------------|-----------|-------|
| 1 | 59s, exit 0 | Max-1 | met (exit==expected) | Timeout boundary | completes in time |
| 2 | 60s | Max | failed (timeout) | Timeout boundary | killed at deadline |
| 3 | hung (never exits) | Overflow | failed (killed at 60s) | Timeout boundary | idle clock not held |
| 4 | 2s, exit 1 vs expected 1 | Happy (nonzero expected) | met | Policy-triad allow | expected-code compare, not hardcoded 0 |
| 5 | output > cap | Resource | failed/capped w/ truncation marker | Evidence-cap scenario | output-size cap |

#### Dataset: Policy triad × unattended

| # | Input (bash policy) | Boundary Type | Expected Output | Traces to | Notes |
|---|---------------------|---------------|-----------------|-----------|-------|
| 1 | allow | Valid | runs via assignee bash; exit compared | Policy triad | audit event present |
| 2 | ask | Fail-closed | deny → criterion failed | Policy triad | no interactive prompt (D2 rule 2) |
| 3 | deny | Fail-closed | criterion failed | Policy triad | |
| 4 | all-machine + ask/deny at create | Write reject | 400 (agent tool) / warn (UI) | Unsatisfiable-write scenario | D2 rule 5 |

#### Dataset: Judge unavailability (× attempt-not-consumed)

| # | Input (cause, attempt index) | Boundary Type | Expected Output | Traces to | Notes |
|---|------------------------------|---------------|-----------------|-----------|-------|
| 1 | SEC-26 throttle, 1st | Dependency failure | pause, retry +60000ms, attempt unchanged | Judge-unavailability outline | shared non-privileged bucket (D3) |
| 2 | cost-cap denial, 2nd | Dependency failure | pause, retry +120000ms, attempt unchanged | Judge-unavailability outline | |
| 3 | provider 500, 3rd | Dependency failure | pause, retry +300000ms, attempt unchanged | Judge-unavailability outline | |
| 4 | call timeout, backoff exhausted | Dependency failure | normal cadence, terminal-retry record, still no attempt consumed; idle clock ends it eventually | Judge-unavailability outline | calendar brake, not attempt-burn |

#### Dataset: Origin gating

| # | Input (origin, content) | Boundary Type | Expected Output | Traces to | Notes |
|---|-------------------------|---------------|-----------------|-----------|-------|
| 1 | user inbound, `/goal X` | Valid | goal starts | Origin gating outline | |
| 2 | system (`Channel:"system"`), `/goal X` | Injection | inert | Origin gating outline | `async_notifier.go:258-264` (`CanonicalID="async:<kind>"` at `:261`) |
| 3 | cron-injected, `/goal X` | Injection | inert | Origin gating outline | Gap #8 |
| 4 | async-injected (`async:<kind>` sender), `/goal X` | Injection | inert | Origin gating outline | |
| 5 | delegated sub-turn worker, `/goal X` | Injection | inert | Loops-spawning-loops | |
| 6 | task-run worker, `/loop …` | Injection | inert | Loops-spawning-loops | |

#### Dataset: Global active-loop cap

| # | Input (active count) | Boundary Type | Expected Output | Traces to | Notes |
|---|----------------------|---------------|-----------------|-----------|-------|
| 1 | 15 → start | Max-1 | starts (16) | Global cap outline | |
| 2 | 16 → start | Max | rejected; `16/16` | Global cap outline | goal+loop+running plans |
| 3 | 17 → concurrent start race | Max+1 | rejected under single-writer guard | Global cap outline | task attempt-loops-in-plan NOT counted |

#### Dataset: Crash recovery (plan boot reconciliation)

| # | Input (task-store state at kill) | Boundary Type | Expected Output | Traces to | Notes |
|---|----------------------------------|---------------|-----------------|-----------|-------|
| 1 | A done, B in_progress, C blocked | Partial | B not re-dispatched; C stays blocked | Boot reconciliation | statuses authoritative |
| 2 | A done, B blocked (deps done) | Stuck-advance | B advanced blocked→next→dispatched | Boot reconciliation | AdvanceBlockedDependents on boot |
| 3 | all done | Complete | plan judge evaluates DoD | Boot reconciliation | plan-complete path |
| 4 | engine started twice (race) | Concurrency | second start no-op | Overlap guard | |

#### Dataset: Plan-state illegal transitions

| # | Input (from→to) | Boundary Type | Expected Output | Traces to | Notes |
|---|-----------------|---------------|-----------------|-----------|-------|
| 1 | done→active | Terminal | rejected | Illegal transitions | one-way terminal |
| 2 | expired→active | Terminal | rejected | Illegal transitions | |
| 3 | stopped→judging | Terminal | rejected | Illegal transitions | |
| 4 | synthesizing→draft | Backward | rejected | Illegal transitions | no backward edge |
| 5 | draft→active | Valid | allowed | Illegal transitions | baseline |

#### Dataset: Recurring Trigger task runs (Gap #7)

| # | Input (trigger fires, prior run state) | Boundary Type | Expected Output | Traces to | Notes |
|---|----------------------------------------|---------------|-----------------|-----------|-------|
| 1 | fire #1, no prior run | First-use | fresh run, own attempt loop (max 3) | `TestRecurringTrigger_...` | each fire = a run (`SpawnTriggeredRun`→`ExecuteTask`, `task_executor.go:900`) |
| 2 | fire #2 while run #1's attempt loop still active | Concurrency | second run bounded by dispatch sema; independent attempt loop | `TestRecurringTrigger_...` | overlap guarded by existing sema/concurrency (`:108`,`:132`) |
| 3 | fire #2 attempts to edit criteria count of run #1 | Immutability | rejected — criteria immutable per run (D5) | `TestRecurringTrigger_...` | per-run snapshot |
| 4 | fire with judge `met` on attempt 1 | Happy | run `done`; next fire starts a new run | `TestRecurringTrigger_...` | no carry-over of attempt count across runs |

### Regression Test Requirements

**Modifying existing functionality:**

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|--------------------|---------------|----------------------------|-------|
| Single-shot task completion: marker success → immediate terminal `done` | `pkg/agent/task_completion_contract_test.go`, `task_completion_signal_test.go` | Yes — `TestGoalLoop_SuccessClaim_JudgeMet_MarksDone`, `TestGoalLoop_NoSignal_IsUnmetNotTerminalFail` | **New contract**: marker is a CLAIM; terminal only after judge `met`. The ADR-043 parser is EXTENDED, not forked — all existing `parseTaskCompletionSignal` tests (fence-awareness, echo-safety `TestBuildPrompt_InstructionEchoNeverResolvesToSuccess`, last-occurrence-wins, truncation) MUST keep passing unchanged. |
| No-signal → `completeTaskWithResult(false)` terminal fail (`task_executor.go:339`) | `task_completion_contract_test.go` | Yes — assert no-signal now maps to `unmet` (attempt consumed) not terminal fail while attempts remain | Behaviour change; document new contract |
| Explicit `update_task(done)` terminal precedence (`:300`) | `task_status_emit_test.go` | Yes — assert terminal claim is judged, not auto-accepted | precedence over marker preserved; adjudication added |
| DAG auto-advance on `done` (`onTaskComplete`→`AdvanceBlockedDependents`) | `pkg/agent/orchestrator_advance_test.go`, `pkg/task/blocked_by` tests | Yes — assert advance still fires exactly once on judged-`done`, never on an unmet claim | Plan engine must not double-advance |
| `IsPrivilegedAgent` returns true for `system` | `pkg/security/ratelimit_test.go`, `pkg/agent/wave5b_sysagent_ratelimit_test.go`, `runturn_rate_limit_test.go` | Yes — `TestSEC26_IsPrivilegedAgent_CoreOnly`, `TestSEC26_TypeSystemAgent_IsRateLimited` | Intentional behaviour change (D3): any hand-crafted legacy type-`system` agent loses exemption |
| Cron overlap guard / retry backoff / SessionMode | `pkg/cron/autonomy_test.go`, `service_test.go`, `zz_overlap_reschedule_test.go`, `jobspec_session_test.go` | No new behaviour — reused as-is | `/loop` + sweeper + plan overlap guard mount on the SAME guarantees; add coverage, don't change |
| Workspace heartbeat rides cron (`pkg/gateway/heartbeat_schedule.go`) | `pkg/gateway/heartbeat_schedule_test.go` | No — preserved | Idle sweeper + `/loop` must not perturb heartbeat jobs |
| Delegation identity from `execSource` (ADR-032) | `pkg/agent/subturn_target_identity_test.go` | No — preserved | Judge runs as its OWN System-Agent identity, never inheriting the caller's |

**New functionality** (no regression impact): Plan entity engine, `/goal`/`/loop` commands, judge execution. Integration seams protected by: `task_completion_contract_test.go` (marker grammar), `orchestrator_advance_test.go` (DAG advance), `autonomy_test.go` (cron fake-clock), `ratelimit_test.go` (SEC-26), `subturn_target_identity_test.go` (delegation identity).

---

## Functional Requirements

**Task goal-loop (US-5)**
- **FR-040**: The system MUST treat a worker turn's ADR-043 completion marker (`task_completion_signal.go:274`) as a **claim** submitted to the judge, never as the terminal task decision.
- **FR-041**: The system MUST route an explicit `update_task(status:"done")` terminal write (observed at `task_executor.go:300`) to the judge as a claim, preserving its precedence over the marker but not its finality (SD-B2).
- **FR-042**: On a judge `unmet` verdict, the system MUST increment and persist the task's attempt counter and re-dispatch the same task (reusing `ExecuteTask`, `:89`).
- **FR-043**: On re-dispatch (attempt ≥2), the system MUST feed the judge's per-criterion unmet reasons forward as steering in the next `buildPrompt` (`:462`).
- **FR-044**: When the attempt counter reaches `max_attempts` (default **3**, configurable globally and per task — FR-9) with no `met` verdict, the system MUST mark the task `failed`, write a graceful wind-down handover to the task record AND the owning session transcript (NFR-3), and wake the owner agent via the async-notifier (`async_notifier.go:258`).
- **FR-045**: A worker turn producing no parseable completion signal MUST be treated as an `unmet` claim (attempt consumed) while attempts remain — NOT an immediate terminal failure (behaviour change vs `:339`).
- **FR-046**: On a judge `met` verdict, the system MUST mark the task terminal `done` via `completeTaskWithResult` (`:361`) and run the existing post-completion hooks (`onTaskComplete`, `:528`) exactly once.
- **FR-047**: The per-task attempt loop MUST enforce a hard ceiling mirroring `2×` the configured attempt bound (pattern from `loop.go:6272`), stopping unconditionally regardless of pending re-dispatch or interrupt state.
- **FR-048**: Scratchpad / `set_todos` tasks MUST be exempt from goal-loop execution entirely (FR-4 exemption).

**Evidence-ladder judge (US-6)**
- **FR-049**: The judge MUST dispatch every machine-checkable criterion EXCLUSIVELY through the assignee agent's existing `bash` tool machinery (same registry/policy/sandbox/audit, G16); a parallel judge-owned exec path is forbidden (D2 rule 1).
- **FR-050**: In the unattended judge context, machine-check policy resolution MUST be: `allow`→run; `ask`→deny (fail-closed, no interactive approver); `deny`→criterion failed (D2 rule 2).
- **FR-051**: Each machine check MUST enforce a per-check timeout (default **60s**, configurable) and an output-size cap; a timeout ⇒ criterion failed (closed), and MUST NOT hold the loop's idle-expiry clock (D7).
- **FR-052**: The system MUST reject (agent tool paths) creating/updating a task whose criteria are ALL machine-type when the assignee's effective `bash` policy is `deny` OR `ask`; the human UI path MUST warn instead (D2 rule 5).
- **FR-053**: Prose criteria MUST be evaluated by a no-tools structured System-Agent call whose input ordering is machine-check evidence records + workspace file diffs FIRST and the worker's own summary LAST, with unevidenced claims scored `unmet` (OBS-003, NFR-2).
- **FR-054**: Machine-check evidence MUST pass registered-sensitive-value redaction (`RegisterSensitiveValues`, ADR-004 flow) before persistence, be per-attempt size-capped with a truncation marker, stored under `$OMNIPUS_HOME` with the sessions permission posture, retained per the 90-day session default, and deleted with the task (MAJ-003).
- **FR-055**: A machine check authored by a different agent than the assignee MUST require assignee-owner confirmation unless a workspace setting waives it (Gap #2 / SD-B7).
- **FR-056**: Each judge verdict MUST be written to the session transcript as a dedicated judge-verdict entry type alongside the worker's ADR-043 marker (§6/Q3), and metered as out-of-turn usage attributed to the System Agent's `agent_id` with plan/task/goal correlation IDs (NFR-5). The `EntryType` set (`pkg/session/daypartition.go:27`, values message/compaction/system/tool_call/turn_canceled at `:31`/`:33`/`:35`/`:37`/`:40`; `TranscriptEntry.Type` at `:143`) has **no** judge/verdict member today — a new additive `EntryTypeJudgeVerdict` value (wire type owned by agent A per §6) is the write target; the runtime is its sole producer.
- **FR-057**: Absence of evidence or verdict MUST never default to success (extends ADR-043 fail-closed, NFR-2).

**Plan engine (US-7)**
- **FR-058**: A server-side plan coordinator MUST dispatch ready plan tasks as the `blocked_by` DAG clears, extending `AdvanceBlockedDependents` (`blocked_by.go:181`) + `advanceBlockedTasks` (`task_executor.go:642`), WITHOUT waking the owner (D4 hybrid).
- **FR-059**: The coordinator MUST wake the owner agent (via async-notifier) only at decision points: attempts exhausted, plan judge failed, or plan complete → synthesis.
- **FR-060**: The plan-level judge MUST be the SAME Judge System Agent invoked with a plan rubric, bounded at **20 rounds** (symmetric with `/goal`, D7).
- **FR-061**: All plan/loop/goal counters and timestamps MUST be persisted fields — plan counters on the Plan entity; `/goal`/`/loop` session state on the `UnifiedMeta` extension (`TaskID` precedent `unified.go:62`); the **per-task attempt counter on `Task.attempt_count`** (wire field C17, R4), NOT `UnifiedMeta` (F5 r2).
- **FR-062**: On boot the engine MUST reconcile from the task store — task statuses authoritative, events an optimization — restoring counters from persisted fields and never blindly re-dispatching an already-`in_progress` task (D4/MAJ-004). The idempotent create/update/remove reconcile shape mirrors `ReconcileHeartbeatSchedules` (`pkg/gateway/heartbeat_schedule.go:154`) and the boot orphan-edge sweep `DropOrphanEdges` (`pkg/task/blocked_by.go:389`).
- **FR-063**: Exactly ONE plan engine instance MUST run, guarded by a cron-style overlap guard (hot-reload safe; mirrors `CronJobState.Running` `service.go:140`).
- **FR-064**: A 7-day idle-expiry sweeper MUST run on the existing cron service and wind down (handover + expired state) any plan/goal/loop idle for ≥7 days; "idle" = no attempt, state transition, or user interaction on the unit.
- **FR-065**: Deleting an agent that owns active plans/goal-loops MUST be rejected (400) until they are reassigned or stopped; disabling MUST pause them (plan surfaces a blocked state) and resume on re-enable (D4/R2-MIN-005).
- **FR-066**: The Plan state machine MUST reject illegal transitions (terminal states are one-way; no backward edges) — enforced server-side (SD-B5 enumerates the legal set; wire enum owned by agent A).

**/goal command (US-8)**
- **FR-067**: `/goal <condition>` MUST store the condition + round state on the session's `UnifiedMeta` (extended `MetaPatch`), begin a proof-driven loop where a **round** = one worker turn + its judge evaluation, default bound **20 rounds**, feeding each unmet judge reason forward as steering.
- **FR-068**: Only ONE active `/goal` per session MUST be permitted; setting a new one replaces the existing (rounds reset).
- **FR-069**: `/goal status` MUST report condition, elapsed wall-clock, `rounds_used/bound`, cumulative token spend (visible only, NFR-1), latest judge reason, and `active loops: N/cap`.
- **FR-070**: `/goal clear` plus aliases (`stop`, `cancel`) and the task/plan card Clear button MUST stop the loop after the current step and clear the session goal fields (D8).

**/loop command (US-9)**
- **FR-071**: `/loop every <interval> <prompt>` MUST create a cron job with `EveryMS` in `continue` session mode owned by the session's agent, fired through the existing `scheduledRunner` (`pkg/gateway/schedules.go:120` `RunScheduled`; owner = `job.AgentID :121`; session resolved by `pickSession :517`; message injected via `exec.ProcessScheduled :233`), reusing `cron.CronSchedule{Kind:"every", EveryMS:…}` + `SessionMode=continue` exactly as heartbeat jobs do (`heartbeat_schedule.go:213` `AddJobFull`).
- **FR-072**: Self-paced `/loop` MUST let the agent choose its next delay + stated reason and schedule a one-shot `at` job (`AtMS`) each run, default bound **100 runs**, **7-day** expiry.
- **FR-073**: `/loop status` MUST report mode, interval/next-delay, `run_count/bound`, elapsed, and `active loops: N/cap`; `/loop stop` MUST remove the cron job and clear the loop.
- **FR-074**: Loops MUST obey existing session ownership/auth; a channel-originated loop MUST run under the routed agent and that session's brakes (Gap #5, no extra role gate in v1).

**Cross-cutting enforcement (US-cross)**
- **FR-075**: `/goal` and `/loop` MUST be inert when the turn does not originate from a genuine user-initiated inbound message. Enforced at `handleCommand` (`loop.go:9582`) via a **user-origin predicate** on the turn, NOT the command surface (Gap #8/r2). Because `bus.InboundMessage` (`pkg/bus/types.go:38`) has **no first-class origin/source-kind field**, the predicate composes the existing structural signals: async-injected turns are `Channel:"system"` with `Sender.CanonicalID = "async:<kind>"` (`async_notifier.go:261`) and non-empty `AsyncOriginAgentID` (`types.go:87`); genuine gateway-user turns set `GatewayUserID` (`types.go:75`); scheduled/cron runs bypass the inbound bus entirely and enter via `exec.ProcessScheduled` (`schedules.go:233`); task runs enter via `processTaskDirect` and delegated work via `spawnSubTurn` (`subturn.go:392`). All four non-user entry paths MUST be marked non-user-origin so `/goal`/`/loop` pass through inert. Given the absence of a first-class field, a small net-new turn-scoped `UserInitiated` flag is the recommended enforcement carrier (SD-B6 / Ambiguity #5).
- **FR-076**: A configurable global cap (default **16**) MUST bound simultaneously-active loops across `/goal` + `/loop` + running plans; task attempt-loops inside a running plan are bounded by the plan and NOT counted individually.
- **FR-077**: `security.IsPrivilegedAgent` (`ratelimit.go:21-23`) MUST be narrowed to `core` only; type-`system` agents MUST be subject to per-agent LLM/tool rate limits and the daily cost cap at the SEC-26 gates (`loop.go:6289`, `:6321`, `:7880`) and MUST have their spend recorded (`RecordSpend`, `loop.go:7334`; `ratelimit.go:269` currently exempts privileged). All four sites pass `ts.agent.AgentType`, so the narrowing alone activates enforcement for the Judge with no call-site change.
- **FR-078**: Type-`system` agents MUST be excluded from default-agent fallback (`IsChatTarget()==false`, `route.go:339`), rejected (400) as routing-binding targets, excluded from delegation-target enumeration / `list_agents` delegation pickers / workspace team rosters, rendered only in the Agents "System" section, non-creatable via REST/agent tools (400, raw-body sniff), with seeded System Agents non-deletable and the Judge non-disable-able (D3/MAJ-001/r3); and MUST satisfy Constraint #6 via seeded explicit all-deny tool policies keeping the boot agent×tool matrix total (NFR-4).
- **FR-079**: The Judge default model MUST be chosen at onboarding; on absence the fallback MUST be the default agent's model — no "cheapest" heuristic (Gap #1). Each recurring `Trigger` task fire MUST create a run that gets its own attempt loop with criteria immutable per run (Gap #7).

---

## Success Criteria

- **SC-020**: In an attempt-loop test with a permanently-failing criterion and `max_attempts=3`, the worker is dispatched exactly 3 times and the task ends `failed` — never `done` — in 100% of runs.
- **SC-021**: A worker emitting `TASK_STATUS: success` against an unmet criterion NEVER yields a `done` task (0 false-success across the judge test matrix).
- **SC-022**: On a judge `met` verdict, `completeTaskWithResult` and `onTaskComplete` DAG-advance each fire exactly once (no double-advance).
- **SC-023**: Judge unavailability (throttle/cap/500/timeout) consumes 0 attempts and records 0 verdicts across all four cause cases; the loop resumes after the `60/120/300s` backoff.
- **SC-024**: Machine checks with `ask` or `deny` policy execute 0 commands and score the criterion `failed` in 100% of policy-triad runs; `allow` checks all appear in the assignee's `bash` audit trail.
- **SC-025**: A machine check hung past 60s is killed within a bounded margin and the criterion is `failed`; the unit's idle clock advances normally (not held).
- **SC-026**: No machine check executes on any path other than the assignee's `bash` tool machinery (asserted by the absence of a judge-owned exec audit signature).
- **SC-027**: Persisted evidence contains no registered sensitive value (redaction 100%) and never exceeds the per-attempt size cap.
- **SC-028**: A prose criterion asserted without evidence scores `unmet` in 100% of unevidenced-claim runs.
- **SC-029**: `/goal` stops at exactly 20 rounds when never met; round 21 is never executed (hard ceiling 40 caps any runaway).
- **SC-030**: `/loop` fires exactly 100 runs under the fake clock, then auto-expires; run 101 is never scheduled.
- **SC-031**: Idle-expiry triggers at ≥7 days and not at 6d23h, across all loop kinds.
- **SC-032**: A cron/system/async-injected message beginning `/goal` or `/loop` starts 0 loops; the identical text from a user origin starts 1.
- **SC-033**: The 17th concurrent loop start is rejected while ≤16 are active; status output reports `N/16` accurately.
- **SC-034**: `IsPrivilegedAgent("system")` returns false and `IsPrivilegedAgent("core")` returns true; a type-`system` agent hitting `MaxAgentLLMCallsPerHour` is rate-limited (SEC-26 gate active).
- **SC-035**: A type-`system` agent is absent from default-agent fallback, delegation pickers, team rosters, and binding-target writes (400); a REST create with type `system` returns 400; a seeded System Agent delete and a Judge disable are both rejected.
- **SC-036**: Boot reconciliation after a mid-plan kill produces zero double-dispatches and zero stuck-`blocked`-with-all-deps-`done` tasks across the crash-recovery matrix.
- **SC-037**: A second plan-engine start (hot reload) performs zero work (overlap guard no-op).
- **SC-038**: Deleting an owner with ≥1 active loop returns 400; disabling pauses then resumes the owned loops with no lost state.
- **SC-039**: Boot with a seeded System Agent keeps `ValidateToolPolicyCoverage` green (agent×tool matrix total; Constraint #6), and the Judge's model resolves to the onboarding choice or, absent it, the default agent's model.

---

## Traceability Matrix (Section B)

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|-----------------|--------------|
| FR-040 | US-5 | Worker success claim confirmed; Explicit update_task judged | 1, 4 |
| FR-041 | US-5 | Explicit update_task(done) is still judged | 4 |
| FR-042/043 | US-5 | Unmet claim re-dispatches with reasons | 2 |
| FR-044 | US-5 | Attempt-count boundary and hard ceiling | 5, 6 |
| FR-045 | US-5 | No completion signal is an unmet claim | 3 |
| FR-046 | US-5 | Worker success claim confirmed | 1 |
| FR-047 | US-5 | Attempt-count boundary and hard ceiling | 6 |
| FR-049/050 | US-6 | Machine-check policy triad | 8, 10 |
| FR-051 | US-6 | Per-check timeout boundary | 9 |
| FR-052 | US-6 | All-machine unsatisfiable policy rejected | 12 |
| FR-053 | US-6 | Prose judge unevidenced unmet | 11 |
| FR-054 | US-6 | Evidence redacted before persistence | 13 |
| FR-055 | US-6 | Cross-agent check requires confirmation | 14 |
| FR-056 | US-6 | Judge-verdict transcript entry type | 15 |
| FR-057 | US-5/6 | No completion signal; policy triad | 3, 8 |
| FR-058/059 | US-7 | Server-side advance; decision-point wake | 16 |
| FR-060 | US-7 | Plan judge 20-round bound | 22 |
| FR-061/062 | US-7 | Boot reconciliation | 17 |
| FR-063 | US-7 | Single-instance overlap guard | 18 |
| FR-064 | US-7/8/9 | Idle-expiry sweep | 21, 31 |
| FR-065 | US-7 | Owner lifecycle | 19 |
| FR-066 | US-7 | Plan-state illegal transitions | 20 |
| FR-067/068 | US-8 | User sets a goal; Round boundary; Replace-on-set | 23, 24, 26 |
| FR-069 | US-8 | /goal status visible-only spend | 25 |
| FR-070 | US-8 | /goal clear aliases | 27 |
| FR-071 | US-9 | Interval-mode loop | 28 |
| FR-072 | US-9 | Self-paced at-jobs; run-count boundary | 29, 30 |
| FR-073 | US-9 | /loop stop | 32 |
| FR-074 | US-9 | (role gating) | 35 (`TestRoleGating_SessionOwnership`) |
| FR-048 | US-5 | Scratchpad set_todos task never dispatched into a goal loop | 36 (`TestTaskExecutor_ScratchpadExemptFromGoalLoop`) |
| FR-075 | US-cross | Origin gating outline | 33 |
| FR-076 | US-cross | Global active-loop cap boundary | 34 |
| FR-077 | US-cross | Type-system rate-limited | 35, 36 |
| FR-078 | US-cross | System Agent enumeration exclusions; Constraint #6 boot | 37, 38 |
| FR-079 | US-cross | Judge default model; recurring Trigger runs | 39, 40 |

---

## Spec Decisions (SD-B*) — ambiguities resolved within ADR bounds

- **SD-B1 — Claim vs terminal boundary.** A "completion claim" is any worker-turn outcome that today would terminate the task `done`: (a) an ADR-043 `TASK_STATUS: success` marker, or (b) an explicit `update_task(status:"done")`. A `failed` marker / `update_task(failed)` is an accepted give-up that counts as an `unmet` outcome for the attempt loop (consume attempt, re-dispatch or wake owner). This keeps the ADR-043 parser EXTENDED (verdict feeds the judge), never forked. *(Within D2/D5/FR-4.)*
- **SD-B2 — Self-certification is judged.** The `task.IsTerminal(current.Status)` precedence at `task_executor.go:300` is preserved (explicit tool call wins over the marker for WHICH claim was made), but the resulting `done` claim is routed to the judge rather than accepted as terminal. This is the only ADR-compliant reading of "every task execution runs as a goal loop with a judge" (FR-4).
- **SD-B3 — Attempt-loop lives in TaskExecutor, wrapping finishTaskRun.** The attempt loop is implemented at the `finishTaskRun` (`:250`) seam: it intercepts the claim, invokes the judge, and either completes (`completeTaskWithResult`) or re-dispatches (`ExecuteTask`) with a persisted attempt increment. Re-dispatch reuses the existing goroutine/dispatch-sema machinery, not a new scheduler. *(Reuse criterion, §4.)*
- **SD-B4 — "Active loop" definition for the global cap.** A unit counts toward the 16-cap iff it is a `/goal`, a `/loop`, or a plan in a non-terminal running/judging state. A task attempt-loop that is a MEMBER of a running plan is bounded by that plan and NOT counted separately; a standalone task attempt-loop (not under a plan) counts as… **it does not** — per D7 only /goal+/loop+running plans are counted; standalone task attempt loops are bounded by their own 3-attempt brake and the dispatch semaphore, consistent with "task attempt loops inside a running plan are bounded by the plan, not counted individually." *(Within D7/MAJ-009.)*
- **SD-B5 — Plan states (canonicalized by R1, F1 r2).** The runtime literals below map onto the **five** canonical `PlanState` values via the R1 table — `active`/`judging`/`synthesizing` ⇒ `running` + `plan_phase`; `paused` ⇒ `running` + `paused_reason`; `stopped`/`expired`/`judge-exhausted` ⇒ `failed` + `failed_reason`. Runtime legal flow: `draft → approved → running(dispatching → judging → {synthesizing → done | dispatching(judge unmet, rounds remain)})`; plus `running → paused` (owner disabled / judge-unavailable) `→ running`. **All `failed` terminals are one-way/frozen** (F1 r2 — a failed plan is not retried), matching Part A's canonical table; `done` is likewise frozen; `draft` is entry-only. The wire `PlanState` enum (5 values) is owned by Part A (C2); this SD enumerates the runtime-enforced transitions in canonical terms. `TestPlan_State_IllegalTransitions` (Test 20) and `TestPlan_StateTransitions` (R7) assert the SAME table (no `failed→running`/`stopped→judging` retry edge on either side).
- **SD-B6 — Origin discrimination point (net-new signal recommended).** Origin gating is enforced in `handleCommand` (`loop.go:9582`). `bus.InboundMessage` (`pkg/bus/types.go:38`) carries NO first-class origin discriminator — origin is only indirectly inferable today (async ⇒ `Channel:"system"` + `Sender.CanonicalID="async:<kind>"` `async_notifier.go:261` + `AsyncOriginAgentID` set `types.go:87`; gateway-user ⇒ `GatewayUserID` set `types.go:75`; cron/scheduled ⇒ never reaches the inbound bus at all, it calls `exec.ProcessScheduled` directly `schedules.go:233`). Relying on that fragile inference across four entry paths is brittle; the resolved approach is a single explicit turn-scoped `UserInitiated` boolean set true ONLY on the genuine gateway/channel user-inbound path and left false on the async-notifier, scheduled (`ProcessScheduled`), task (`processTaskDirect`), and sub-turn (`spawnSubTurn`) paths. `/goal` and `/loop` command definitions action only when `UserInitiated` is true; otherwise they pass through inert as ordinary text. This is a small additive field, consistent with the ADR's "discriminate on origin, not surface" and with the fact that these paths are already structurally distinct producers. *(Within Gap #8/r2; confirms the plan-spec enforcement point the ADR requested.)*
- **SD-B7 — Cross-agent machine-check confirmation.** Default: a machine check whose `author` (recorded per FR-3) differs from the task assignee requires an explicit assignee-owner confirmation before its first execution; a workspace-level setting (`waive_cross_agent_check_confirmation`) waives it. Confirmation state persists on the criterion so it is a one-time gate, not per-attempt. *(Gap #2.)*
- **SD-B8 — Judge as plan-judge reuse.** The plan-level judge is the same seeded Judge System Agent invoked with a distinct plan rubric prompt (no second seeded agent), matching the ADR §6 decision (review Q5). Round semantics and the 20-round bound are symmetric with `/goal`.
- **SD-B9 — Handover destination.** On any brake fire (attempts/rounds exhausted, idle expiry, `stop`/`clear`, owner disable), the wind-down writes a handover summary to BOTH the plan/task record AND the owning session transcript (NFR-3), then wakes the owner via the async-notifier for the owner-facing decision points (attempts exhausted / plan judge failed / plan complete → synthesis).
- **SD-B10 — Bounds configurability.** Every count/calendar bound (task attempts, /goal rounds, plan judge rounds, /loop runs, idle-expiry days, global cap, per-check timeout) is configurable globally AND per entity that runs a loop (plan, task, /goal, /loop) — FR-9. No workspace-level default layer in v1 (MIN-004). Defaults are the ADR D7 numbers.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|------------------|-------------------------|---------------------|
| 1 | Exact persisted shape of the task attempt counter + per-criterion status (owned by agent A) | New `Task.attempt_count` int + `AcceptanceCriterion.status` + `EvidenceRecord[]` per §6 contract table | Confirm agent A's schema names so the runtime reads/writes the right fields |
| 2 | Whether a self-reported `failed` claim should still consume an attempt and re-dispatch, or wake the owner immediately | Consume an attempt + re-dispatch (treat as unmet) until attempts exhausted | Confirm with operator; SD-B1 assumes re-dispatch |
| 3 | `/goal` round steering vs the session's normal turn injection — one combined turn or a distinct judged turn after the worker turn | Round = worker turn THEN a separate judge evaluation (D7/MIN-001); reason injected as steering into the next round's worker turn | Confirm the round is worker-turn + judge, not judge-inline |
| 4 | Where the global active-loop counter lives (single-writer authority) | A single engine-owned in-memory + persisted counter, decremented on terminal, reconciled on boot from persisted loop states | Confirm the cap authority co-locates with the single plan-engine instance |
| 5 | No first-class origin field on `bus.InboundMessage` (`types.go:38`); cron/scheduled bypasses the inbound bus (`ProcessScheduled`, `schedules.go:233`) | Add a small turn-scoped `UserInitiated` boolean set only on the genuine user-inbound path (SD-B6) rather than inferring from `Channel`/`GatewayUserID`/`AsyncOriginAgentID` | Confirm agent A/security lead accept a net-new `UserInitiated` turn flag as the origin-gating carrier |

---

## Assumptions

- Agent A delivers the `Plan`, `PlanState`, `AcceptanceCriterion`, `EvidenceRecord`, `JudgeVerdict` wire types and the `Task.attempt_count` / criterion-status / goal-loop `UnifiedMeta` fields BEFORE this runtime lands (Constraint #8 five-step process); this section consumes only generated types.
- The judge reuses the offline judge's proven shape — structured-JSON contract + rubric template (`evals/judge/prompt.go:15` / `RenderPrompt :83`) and the validating parser (`evals/judge/scorer.go` `Parse :115`, `Scores :32`) — adapted to a per-criterion `{met, reason}` no-tools structured System-Agent call. `scorer.go` is parse-only (no LLM call); the LLM invocation is added new under the System-Agent identity. No new provider dependency.
- The cron fake-clock harness (`pkg/cron/autonomy_test.go`) is the deterministic time source for every calendar/interval test; no wall-clock sleeps (per CLAUDE.md build discipline — full Go suite runs in CI, not the dev pod).
- Not in scope here: the Plan/AcceptanceCriterion/Evidence wire schemas (agent A), the board/Plan SPA surfaces and Agents "System" section UI (agent C), Milestone removal + tag migration (agent A). This section defines the RUNTIME behaviour those depend on.

## Clarifications

### 2026-07-19

- Q: Does the ADR-043 marker still terminate a task? -> A: No — it becomes a claim adjudicated by the judge (FR-040). The parser is EXTENDED, not forked; all existing `task_completion_signal_test.go` invariants hold.
- Q: Is the Judge privileged? -> A: No (D3/CRIT-002). `IsPrivilegedAgent` narrows to `core`-only; the Judge is rate-limited and cost-capped like any non-core agent (FR-077).
- Q: What happens when the judge itself is down? -> A: Pause + cron backoff `60/120/300s`; attempt NOT consumed; no verdict recorded; idle clock keeps running (FR-051-adjacent, D7 unavailability paragraph).
- Q: Can a cron-injected `/goal` start a loop? -> A: No — origin gating makes it inert (FR-075/SD-B6).

---

# Part C — SPA / UX Surfaces

## Available Reference Patterns

| Codebase Anchor | Pattern | Relevance |
|---|---|---|
| `src/lib/statusColors.ts:39-58` | Single-source-of-truth status palette + labels (`STATUS_COLORS`, `STATUS_LABELS`, `statusColor()`, `statusLabel()`) | New `PLAN_STATE_COLORS`/`PLAN_STATE_LABELS` module mirrors this exactly (SD-C6) |
| `src/components/workspaces/MilestoneFilterPills.tsx` (whole) | Pill filter bar with sentinel values (`MILESTONE_FILTER_ALL=null` L6, `MILESTONE_FILTER_UNSCHEDULED` L7) | `PlanFilterBar` replaces it 1:1 with plan pills + tag chips + All/Untagged sentinels |
| `src/components/workspaces/MilestoneProgressBar.tsx:25-80` | `progress ?? 0 → pct`, tri-state bar colour (done/overdue/accent) | Plan progress bar reuses the bar math + colour ramp |
| `src/components/command-center/TaskDetailPanel.tsx:743-812` | Popover multi-select with `aria-pressed` row buttons + removable chips | Acceptance-criteria editor + tag input reuse this exact interaction grammar |
| `src/components/chat/RateLimitIndicator` render slot (`ChatScreen.tsx:2748-2762`) | Transient above-composer status banner, dismissable, session-scoped | `GoalIndicator` renders in the same slot, driven by `goal_status` frames |
| `src/components/chat/ActivityBar.tsx` + `ActivityPanel.tsx` + `useRunningActivity.ts` | Span-aggregation hook + slide-out; `RECENTLY_FINISHED_CAP=8` (`useRunningActivity.ts:95`) | Judge-verdict spans join the same `ActivityItem` union + panel rows (§G) |
| `src/lib/toolVisibility.ts:89-243` | Three render policies (thread / span / panel), verbose-chat override | Judge-verdict thread visibility decided here (SD-C10) |
| `src/hooks/useSlashMenu.ts:657-707,861-862` | `executeSlashCommand`, `completeSkillName`, ghost-text | `/goal` `/loop` palette entries + argument-hint ghost (SD-C7) |
| `src/store/chat.ts:2637,3698-3712,1034-1040` | WS frame `switch`, `task_status_changed`→invalidate `['tasks']`, `SESSION_SCOPED_FRAME_TYPES` | New status frames consumed here (§H) |
| `src/lib/ws.ts:254,179-190,233` | `WsFrameSchema.safeParse` edge validation, `_recordDropped`, dev-mode toast | New frames validated at edge; drop+counter on failure (§H) |

---

## Existing Codebase Context

### Symbols Involved (SPA)

| Symbol | Role (current) | Change |
|---|---|---|
| `WorkspaceTasksTab` (`WorkspaceTasksTab.tsx:37`) | Owns the milestone filter + create slide-overs; renders Board/List | Milestone query/filter → plan query/filter; add Plans list surface |
| `MilestoneFilterPills` (`MilestoneFilterPills.tsx:17`) | Board milestone filter | **DELETED**, replaced by `PlanFilterBar` |
| `useWorkspacesStore` (`workspacesStore.ts:26-33`) | `activeMilestoneId`/`setActiveMilestoneId`, `boardAltitude` | `activeMilestoneId`→`activePlanId`; add `activeTagFilter` |
| `BoardView.filterByMilestone` (`BoardView.tsx:241-247`) | Milestone filtering | → `filterByPlan` + tag filter; altitude/nesting untouched |
| `TaskCard` (`TaskCard.tsx:67`) | Card body; milestone tag chip L191-195 | Milestone chip → tag chips; add criteria/attempt/goal-loop status |
| `ListView` (`ListView.tsx:25`) | Table + Milestone `FilterSelect` L75-86 | Milestone filter → plan/tag filter; Milestone column → Tags column |
| `TaskDetailPanel` (`TaskDetailPanel.tsx:120`) | Milestone `SmartSelect` L641-655 | **Remove** milestone dropdown; add criteria editor, evidence viewer, tags, Clear/Stop |
| `CreateTaskSlideOver` (`CreateTaskSlideOver.tsx:97`) | Milestone `Select` L392-419 | Milestone select → tags input + criteria editor |
| `useSlashMenu` (`useSlashMenu.ts:226`) | Palette; `argumentHint` skills-only | Renders `/goal`,`/loop` from `GET /commands`; ghost via SD-C7 |
| `useRunningActivity` (`useRunningActivity.ts:349`) | `ActivityItem` aggregation | New `JudgeActivityItem` member (§G) |
| `chat.ts` frame `switch` (`chat.ts:2637`) | WS consumption | New `plan_status`/`goal_status`/`loop_status`/`judge_verdict` cases (§H) |
| `AgentsLibraryView` grouping (`AgentListScreen.tsx:161-163`) | `mainAgents`/`workerAgents`/`builtInAgents` type-branch | Add `systemAgents` filter + fourth locked section; exclude `system` from `mainAgents` |
| `useChatAgents` (`useChatAgents.ts:78-84`) | Chat-target scoping (`!isWorker` + status + team) | Add `a.type !== 'system'` at L81 — the single chat-exclusion point (AgentPicker + `@`-mention inherit) |
| `isWorker` (`api.ts:860-862`) | `Subagent`/`subagent_3p`/`worker` predicate | System is NOT a worker — needs its own `isSystem` guard (SD-C16); note: no `isChatTarget`/`isCore`/`isSystem` helper exists today |
| `agentKindFlags` (`agentKind.ts:52-67`) | `isLocked`/`isWorker`/`isExternal` flags | Add `isSystem` flag |
| `AddAgentPicker` (`team/AddAgentPicker.tsx:34-36`) | Team-add candidates; already `a.type !== 'system'` | Keep (already excludes system from team membership → transitively from delegation edges) |
| `validateConnection` (`workspaces/team/teamGraphModel.ts:343-352`) | Delegation-edge validity (self/member/dup only, no type gate) | Add defensive `system` target exclusion (SD-C17) |

### Impact Assessment

| Symbol Modified | Risk | d=1 Dependents |
|---|---|---|
| `useWorkspacesStore` field rename | MEDIUM | `WorkspaceTasksTab`, `BoardView`, `CreateTaskSlideOver` |
| `MilestoneFilterPills` deletion | MEDIUM | `WorkspaceTasksTab` (only importer); tests `MilestoneProgressBar.test.tsx`, `CreateMilestoneSlideOver.test.tsx` |
| `SESSION_SCOPED_FRAME_TYPES` add | LOW | `chat.ts` routing (`chat.ts:2596`) |
| `ActivityItem` union extension | MEDIUM | `ActivityBar`, `ActivityPanel`, `ActivityAvatar`, `useRunningActivity.test.ts` |
| `TaskCard` body | MEDIUM | `BoardView`, `ExecutionView`, `TaskCard.test.tsx` |

### Cluster Placement

Spans **workspace-tasks** cluster (community 46: Board/List/Card/statusColors) and **chat** cluster (community 0/14: composer, ActivityBar/Panel, chat store). Cross-cluster seam is the WS frame layer (`chat.ts` ↔ query invalidation into the tasks cluster).

---

## User Stories & Acceptance Criteria

### User Story 10 — Plans as the board's container & lifecycle (Priority: P1)

An operator opens a workspace's Board and needs to see, filter by, and manage **Plans** — named goal-directed task chains — instead of date-bucket Milestones. They create a Plan (goal + Definition-of-Done criteria + owner agent + bounds), approve it, watch it run, and can Stop/Clear it. The board filters to a Plan's task chain and shows the Plan's progress and dependency-chain statuses.

**Why this priority**: FR-1/FR-2 — the Plan container is the epic's headline user-visible surface and the entry point for every other flow (criteria, goal loops, judge). Milestone removal (a shipped feature) leaves a hole that must be filled in the same release; without it the board has no grouping at all.

**Independent Test**: With Agent A's `Plan` contract + Agent B's plan engine stubbed to return static plans, load the Board — the `PlanFilterBar` renders plan pills, selecting one filters tasks to that plan's chain, the Plans list shows state badges and progress, and Create/Approve/Stop drive the REST endpoints. Verifiable with the plan endpoints alone; no goal-loop execution required.

**Acceptance Scenarios**:
1. **Given** a workspace with ≥1 plan, **When** the Board loads, **Then** the toolbar renders a `PlanFilterBar` (All + one pill per plan + tag chips + Untagged) where `MilestoneFilterPills` used to be, and no milestone UI is present anywhere.
2. **Given** the "All" pill is active, **When** the operator clicks a plan pill, **Then** the board shows only tasks belonging to that plan's dependency chain, each in its lifecycle column, and the plan's progress (0–100%) is shown.
3. **Given** a selected plan, **When** the operator opens the Plans list, **Then** each plan card shows name, goal (truncated), state badge (draft/approved/running/done/failed), owner-agent avatar, progress, and bounds (rounds/calendar).
4. **Given** the Create Plan slide-over, **When** the operator enters a goal, ≥0 DoD criteria, owner, and bound overrides and submits, **Then** a draft plan is created (`POST /plans`) and appears with a `draft` badge.
5. **Given** a draft plan whose member tasks lack acceptance criteria, **When** the operator clicks Approve, **Then** the backend `400` payload's per-task validation errors are listed inline in the slide-over (no optimistic transition), and the plan stays `draft`.
6. **Given** a `running` plan, **When** the operator clicks Stop/Clear (D8), **Then** the plan loop is stopped (`POST /plans/{id}/stop` or equivalent), the badge optimistically flips, and a graceful wind-down surfaces.
7. **Given** a plan whose owner agent is disabled mid-run (D4), **When** the board renders, **Then** the plan card and its board tasks surface a **paused / blocked (owner disabled)** state, resuming when the owner is re-enabled.
8. **Given** `altitude = show-all`, **When** a plan filter is active, **Then** nested subtask rows still render under parent cards exactly as today (altitude/nesting behaviour preserved).

---

### User Story 11 — Task acceptance criteria, evidence & tags (Priority: P1)

When creating or inspecting a task, the operator defines **acceptance criteria** (machine `check` = command + expected exit code, or `prose`), sees per-attempt criterion verdicts (met/unmet + judge reason) with an evidence viewer, watches the goal-loop attempt counter, and can Clear/Stop the loop. Tasks carry **tags** (replacing milestones) with validation feedback, chips on cards, and board filtering; post-migration `milestone:<name>` tags appear automatically.

**Why this priority**: FR-3/FR-6 — criteria are the substrate the judge evaluates; without the editor the whole goal-loop is unusable from the UI. Tags (FR-2) are the milestone replacement and must ship together so no task loses its grouping.

**Independent Test**: Open the Task detail with a task carrying criteria + a prior attempt's `EvidenceRecord`/`AcceptanceCriterion.status`; the editor renders both kinds, the evidence viewer shows redaction + truncation markers, the attempt counter reads "attempt 2/3", and the tag input rejects an uppercase/65-char/17th tag with the correct message. No live loop needed — all fed from static task JSON.

**Acceptance Scenarios**:
1. **Given** the Create Task slide-over, **When** the operator adds a `check` criterion, **Then** fields for command + expected exit code appear; adding a `prose` criterion shows a single text field; each saved criterion shows its author identity.
2. **Given** an agent-tool-path task with zero criteria, **When** the backend `400`s (FR-6 strict agent path), **Then** the SPA surfaces the error; **but** a human/UI-created task with zero criteria is allowed (D5 soft) with an inline hint that it will be judged against title+description/prompt.
3. **Given** a task that has run ≥1 attempt, **When** the detail opens, **Then** each criterion shows a met/unmet indicator + the judge's reason, and the attempt counter reads "attempt N/M".
4. **Given** a criterion with recorded evidence, **When** the operator expands it, **Then** the evidence viewer shows the (redacted) command output with a truncation marker when capped, and never renders a raw secret.
5. **Given** a running goal-loop task, **When** the card renders, **Then** it shows a goal-loop status affordance (round N/M or paused), and the detail exposes a Clear/Stop button.
6. **Given** the tag input, **When** the operator types `Q3 Release` (uppercase + space) / a 65-char tag / a 17th tag, **Then** the input shows the exact validation message (lowercased-on-normalise / max 64 / max 16 per task) and the offending entry is rejected or normalised per rule.
7. **Given** a task migrated from a milestone, **When** the detail/card renders, **Then** a `milestone:<name>` tag chip appears (post-migration), and the **milestone dropdown is gone** from `TaskDetailPanel`.
8. **Given** tag chips on cards, **When** the operator clicks a board tag chip, **Then** the board filters to tasks carrying that tag (interoperating with the plan filter).

---

### User Story 12 — `/goal` and `/loop` in chat (Priority: P2)

From any chat, the operator types `/goal <condition>` (proof-driven) or `/loop` (time-driven) — the palette offers them automatically, a persistent indicator shows the active goal (condition + round N/20 + latest judge reason), and `/goal status`, `/goal clear`, `/loop stop` render readable status/confirmation output. The commands work on any channel; the palette is web-only.

**Why this priority**: FR-5/FR-8 — the chat command surface is the operator's fastest path to start/stop a goal, but it depends on US-10/US-11 primitives (a goal loop needs criteria/judge). P2 because plans (US-10) can run without the chat commands.

**Independent Test**: With `GET /api/v1/commands` returning `/goal` + `/loop` (`delivery: agent`), the palette lists them with argument-hint ghost text; sending `/goal <cond>` forwards it; a `goal_status` frame renders the persistent indicator; `/goal clear` clears it. Palette + indicator verifiable with a stubbed command list + injected frames.

**Acceptance Scenarios**:
1. **Given** the composer, **When** the operator types `/go`, **Then** `/goal` and `/loop` appear in the palette automatically (sourced from `GET /commands`, `delivery: agent`), each with its argument-hint.
2. **Given** `/goal ` selected, **When** the ghost-text overlay shows, **Then** it displays the command's argument hint (e.g. `<condition>`), not the generic `<message>` (SD-C7).
3. **Given** an active goal, **When** the chat renders, **Then** a persistent `GoalIndicator` shows the goal condition, `round N/20`, and the latest judge reason, above the composer.
4. **Given** an active goal that pauses because the judge is unavailable (D7 R2-MAJ-001), **When** the indicator updates, **Then** it shows a **paused — waiting on judge** state (attempt not consumed), distinct from active.
5. **Given** a brake fires (rounds/calendar, D7), **When** the indicator updates, **Then** it shows a **brake-fired / winding down** state and then clears, with the handover summarised in the thread.
6. **Given** `/goal status`, **When** sent, **Then** the agent's status output (condition, round, active loops N/cap) renders in the thread; `/goal clear` and `/loop stop` render a confirmation and clear the indicator.
7. **Given** a non-web channel (Telegram), **When** the operator sends `/goal <cond>`, **Then** it is parsed server-side and works (origin note: palette is web-only; the command itself is channel-agnostic).

---

### User Story 13 — System Agents & judge transparency (Priority: P2)

The Agents screen gains a locked **System** section rendering the Judge (model/provider + rubric prompt editable; no delete, no disable, not ★-eligible, excluded from delegation pickers and team rosters). Every judge call is transparent: an ActivityPanel span shows the verdict summary per criterion + spend, subject to the existing cap + verbose-chat rules.

**Why this priority**: FR-7/NFR-5 — transparency parity is an Omnipus principle, but the Judge functions correctly even if its UI surfaces are minimal, so P2. Depends on the judge running (US-10/US-11).

**Independent Test**: With `fetchAgents()` returning a `type: system`, `locked: true` Judge, the Agents screen renders it in a System section with editable model/rubric and no delete/disable/★; it is absent from the delegation picker, team roster, and default-agent star. Injecting a `judge_verdict` span shows it in the ActivityPanel. Verifiable with a stubbed agents list + injected frame.

**Acceptance Scenarios**:
1. **Given** a seeded Judge (`type: system`, `locked: true`), **When** the Agents screen loads, **Then** it renders in a dedicated **System** section, visually distinct from Core/user agents.
2. **Given** the Judge detail, **When** opened, **Then** model/provider and the rubric prompt are editable and persist via `PUT /agents/{id}`; **but** there is no Delete, no Disable toggle, and no ★ default control.
3. **Given** a delegation-target picker (workspace Team tab / delegation edge UI), **When** it enumerates agents, **Then** the Judge (`type: system`) is excluded.
4. **Given** the workspace Team roster add-picker, **When** it lists eligible agents, **Then** System agents are excluded.
5. **Given** a judge evaluation ran, **When** the operator opens the ActivityPanel, **Then** a judge-verdict row shows a per-criterion verdict summary and the token/cost spend attributed to the Judge's `agent_id`.
6. **Given** verbose-chat off, **When** a judge verdict is produced, **Then** it is **not** rendered as a standalone thread card (panel-only, per SD-C10); with verbose-chat on, it renders in the thread.
7. **Given** the retained-failure rule (`ActivityBar.tsx:49-61`), **When** a judge call fails, **Then** the ActivityBar/panel stays reachable at idle while that failure is retained in the capped list.

---

## Behavioral Contract

Primary flows:
- When the Board loads with ≥1 plan, the toolbar renders `PlanFilterBar` (All + plan pills + tag chips + Untagged) in place of `MilestoneFilterPills`.
- When a plan pill is selected, `BoardView` filters `rootTasks` to that plan's chain (replacing `filterByMilestone`, `BoardView.tsx:241-247`) and the altitude/DnD behaviour is unchanged.
- When a criterion or evidence record is present on a task, the detail panel renders the editor + evidence viewer with redaction + truncation markers.
- When `GET /commands` includes `/goal`/`/loop`, the palette lists them automatically (no client-side hardcoding — `ChatScreen.no-hardcoded-commands.test.ts` guard holds).
- When a `goal_status` frame arrives, the `GoalIndicator` renders/updates; when `plan_status`/`task_status_changed` arrive, the tasks/plans queries invalidate.
- When `fetchAgents()` returns a `type: system` agent, it renders only in the Agents-screen System section and is excluded from every agent-enumerating picker.

Error flows:
- When Approve `400`s with per-task criteria-missing errors, the slide-over lists them and does not transition the plan.
- When a WS status frame fails zod validation at the edge (`ws.ts:254`), it is dropped, `_droppedFrameCount++`, and a dev-mode toast fires — no prod crash.
- When the tag input violates a rule, the specific message renders inline and the entry is rejected/normalised.
- When the plans/commands query errors, the surface degrades (empty PlanFilterBar / "Commands unavailable" row `useSlashMenu.ts` L544) rather than crashing.

Boundary conditions:
- When a plan has zero member tasks, the plan pill still renders (progress 0%, empty chain state).
- When a task has zero criteria and is human-created, it is allowed with a soft hint; agent-path zero-criteria is a backend `400` surfaced by the UI.
- When evidence output exceeds the size cap, a truncation marker renders; when redaction applied, redacted spans render as the backend-provided redaction marker.
- When `recentlyFinished` exceeds `RECENTLY_FINISHED_CAP=8`, the oldest judge/agent/bash items drop from the panel (shared cap).

---

## Edge Cases

- **Milestone→plan store rename mid-session**: `useWorkspacesStore.activeMilestoneId` persisted value is gone; `activePlanId` starts `null` (All). No migration needed (Zustand non-persisted per `workspacesStore.ts:26`). Expected: board shows All.
- **A task belonging to a plan AND carrying tags**: plan filter + tag filter compose (AND). Expected: intersection shown.
- **Judge span with zero steps but a verdict**: must stay expandable (mirrors `ActivityPanel.tsx:52` `canExpand` for zero-step-but-finalResult spans).
- **`goal_status` frame missing `session_id`**: it is session-scoped; add to `SESSION_SCOPED_FRAME_TYPES` (`chat.ts:1034`) so a missing id drops-in-prod (not routed to active) unless it is a global variant.
- **Plan state `failed` vs task `failed`**: distinct palettes/labels — plan uses `PLAN_STATE_COLORS`, task uses `STATUS_COLORS`; never share a module symbol to avoid the pre-`statusColors.ts` divergence this codebase already fixed.
- **`/goal` typed as first char of a multi-line draft** (Shift+Enter): menu closes on embedded newline (existing `useSlashMenu.ts:833-855` behaviour) — unchanged.
- **Owner agent deleted while it owns active plans**: backend `400` (D4); the delete UI must surface it (Agents screen). Disable → paused/blocked surfacing (US-10 AC-7).
- **Tag `milestone:q3` collides with a user-typed `milestone:q3`**: migration disambiguation is backend (`-2` suffix, D1); the SPA just renders whatever tags arrive — no client uniqueing.
- **Very long goal condition (4000 chars)**: indicator truncates with title tooltip; the thread output wraps.
- **Judge disabled attempt**: there is no disable control for the Judge (backend rejects); the UI never renders one (US-13 AC-2).

---

## Explicit Non-Behaviors

- The SPA must **not** render any token/money brake or budget UI — bounds are count + calendar only (NFR-1); spend is shown as **information**, never as an enforced limit.
- The SPA must **not** offer a Delete or Disable control for a `type: system` agent, and must **not** show a ★ default-agent control for it (ADR-049 D3 enumeration exclusions).
- The SPA must **not** show System agents in delegation pickers, `list_agents`-fed delegation menus, team rosters, or as chat targets (`isChatTarget` = `!isWorker`; system excluded).
- The SPA must **not** re-introduce any milestone UI: no `MilestoneFilterPills`, no milestone dropdown in `TaskDetailPanel`, no milestone `FilterSelect` in `ListView`, no `CreateMilestoneSlideOver` entry point.
- The SPA must **not** client-side-uniquify, re-prefix, or truncate migrated `milestone:` tags — normalisation/disambiguation is backend (D1).
- The SPA must **not** render a raw judge verdict as a standalone thread card by default — panel-only unless verbose chat is on (SD-C10), mirroring `shouldRenderSubagentSpan` (`toolVisibility.ts:218-223`).
- The SPA must **not** hardcode `/goal`/`/loop` in the palette — they come from `GET /commands` (`ChatScreen.no-hardcoded-commands.test.ts` guard).
- The SPA must **not** optimistically transition a plan to `approved` when Approve may `400` on missing criteria — approve is confirm-on-success (SD-C4).

---

## Integration Boundaries

### REST — Plans / Tasks / Agents (gateway)

- **Data in**: `Plan`, `PlanState`, `PlanCreateRequest`/`PlanUpdateRequest`, `Task` (with `tags`, `criteria`/`AcceptanceCriterion`, attempt counters), `EvidenceRecord`, `Agent` (with `type: system`, `locked`), all from `src/lib/api/generated/` (Agent A).
- **Data out**: create/update/approve/stop plan bodies; task criteria/tags PATCH bodies; agent model/rubric PUT.
- **Contract**: OpenAPI-generated types + zod schemas; `request()` (`api.ts:634`) validates responses via the generated schema; new query keys `plansQueryKeys` mirror `tasksQueryKeys` (`api.ts:1925-1935`).
- **On failure**: Approve `400` → inline per-task error list; list query error → degraded empty state; mutation error → `addToast` (existing `WorkspaceTasksTab.tsx:53-56` pattern).
- **Development**: real gateway (Agent B) behind the generated contract; component tests use stubbed fetchers + injected frames.

### WebSocket — status frames (AsyncAPI)

- **Data in**: `plan_status`, `goal_status`, `loop_status`, `judge_verdict`, extended `task_status_changed` (Agent A AsyncAPI).
- **Data out**: none (server→client).
- **Contract**: `WsFrameSchema` discriminated union (`ws.ts:13,254`); each new frame is a generated zod variant + a `SESSION_SCOPED_FRAME_TYPES` member (`chat.ts:1034`).
- **On failure**: schema-invalid → dropped + `_droppedFrameCount++` + dev toast (`ws.ts:179-190,233`); unknown type → `_unknownFrameTypeCount++` (`ws.ts:285`); missing `session_id` on a session-scoped frame → dropped-in-prod (`chat.ts:2603`).
- **Development**: injected frames via `window.__omnipus_test_hooks` (`ws.ts:155`) in Playwright; unit tests push frames through the store reducer directly (existing `chat.notification-frame.test.ts` pattern).

---

## BDD Scenarios

### Feature: Plan container & lifecycle (US-10)

#### Scenario: Board renders the plan filter bar in place of milestone pills
**Traces to**: US-10, AS-1
**Category**: Happy Path
- **Given** a workspace with two plans "Launch" and "Hardening"
- **When** the Board tab mounts
- **Then** the toolbar renders a filter bar with an "All" pill and one pill per plan
- **And** no milestone pill, milestone dropdown, or "New milestone" button appears anywhere

#### Scenario: Selecting a plan filters the board to its dependency chain
**Traces to**: US-10, AS-2
**Category**: Happy Path
- **Given** the "All" pill is active and 6 tasks exist across two plans
- **When** the operator clicks the "Launch" plan pill
- **Then** only Launch's chain tasks render, each in its lifecycle column
- **And** Launch's progress percentage is displayed

#### Scenario: Plan card shows the full state summary
**Traces to**: US-10, AS-3
**Category**: Happy Path
- **Given** a plan in `running` state owned by "Ray"
- **When** the Plans list renders
- **Then** the card shows name, truncated goal, a Forge-Gold `running` badge, Ray's avatar, progress, and bounds

#### Scenario Outline: Plan state badge matrix
**Traces to**: US-10, AS-3
**Category**: Edge Case
- **Given** a plan with state `<state>`
- **When** its card renders
- **Then** the badge label is `<label>` and its accent colour is `<hex>`

**Examples**:
| state | label | hex |
|---|---|---|
| draft | Draft | #9ca3af |
| approved | Approved | #3B82F6 |
| running | Running | #D4AF37 |
| done | Done | #10b981 |
| failed | Failed | #ef4444 |

#### Scenario: Approve is blocked when member tasks lack criteria
**Traces to**: US-10, AS-5
**Category**: Error Path
- **Given** a draft plan whose two member tasks have no acceptance criteria
- **When** the operator clicks Approve and the backend returns `400` with a per-task error list
- **Then** the slide-over lists each offending task + reason inline
- **But** the plan badge stays `draft` (no optimistic transition)

#### Scenario: Stop/Clear a running plan
**Traces to**: US-10, AS-6
**Category**: Happy Path
- **Given** a `running` plan
- **When** the operator clicks Stop/Clear on the plan card
- **Then** the stop endpoint is called and the badge optimistically flips out of `running`
- **And** a graceful wind-down state is surfaced until the server confirms

#### Scenario: Owner disabled surfaces a paused/blocked plan
**Traces to**: US-10, AS-7
**Category**: Error Path
- **Given** a `running` plan whose owner agent is then disabled
- **When** the board re-renders on the next `plan_status` frame
- **Then** the plan card and its board tasks show a "paused — owner disabled" state
- **And** re-enabling the owner clears it

#### Scenario: Altitude nesting preserved under a plan filter
**Traces to**: US-10, AS-8
**Category**: Edge Case
- **Given** `altitude = show-all` and a plan filter active
- **When** the board renders
- **Then** subtask rows render nested under parent cards exactly as before the plan change

### Feature: Task criteria, evidence & tags (US-11)

#### Scenario: Add a machine-check criterion in Create Task
**Traces to**: US-11, AS-1
**Category**: Happy Path
- **Given** the Create Task slide-over is open
- **When** the operator adds a criterion of kind `check`
- **Then** command + expected-exit-code fields appear
- **And** the saved criterion records the operator's author identity

#### Scenario: Human-created task with zero criteria is allowed with a hint
**Traces to**: US-11, AS-2
**Category**: Alternate Path
- **Given** the Create Task slide-over with no criteria added
- **When** the operator submits via the UI
- **Then** the task is created (D5 soft path)
- **And** an inline hint states it will be judged against its title + description/prompt

#### Scenario: Agent-path zero-criteria task rejection is surfaced
**Traces to**: US-11, AS-2
**Category**: Error Path
- **Given** a task-create attempt on the agent tool path with zero criteria
- **When** the backend returns `400` (FR-6 strict)
- **Then** the SPA surfaces the validation error text

#### Scenario: Per-attempt criterion verdicts + attempt counter
**Traces to**: US-11, AS-3
**Category**: Happy Path
- **Given** a task that has completed attempt 2 of a max of 3
- **When** the detail panel opens
- **Then** each criterion shows met/unmet + the judge's reason
- **And** the attempt counter reads "attempt 2/3"

#### Scenario: Evidence viewer shows redaction + truncation markers
**Traces to**: US-11, AS-4
**Category**: Edge Case
- **Given** a criterion whose evidence output was redacted and truncated
- **When** the operator expands the evidence viewer
- **Then** the redaction marker replaces the secret span
- **And** a truncation marker indicates the output was capped
- **But** no raw secret is ever rendered

#### Scenario Outline: Tag input validation feedback
**Traces to**: US-11, AS-6
**Category**: Error Path
- **Given** the tag input on a task
- **When** the operator enters `<input>`
- **Then** the result is `<result>` with message `<message>`

**Examples**:
| input | result | message |
|---|---|---|
| `Q3 Release` | normalised to `q3 release`? rejected space | "Tags are lowercased" / "no spaces" per SD-C8 |
| `a`×65 | rejected | "Max 64 characters" |
| 17th tag | rejected | "Max 16 tags per task" |
| `  spaced  ` | trimmed to `spaced` | (no error) |
| `Release` | normalised to `release` | (no error, lowercased) |

#### Scenario: Migrated milestone appears as a tag; dropdown gone
**Traces to**: US-11, AS-7
**Category**: Happy Path
- **Given** a task migrated from milestone "Q3"
- **When** the detail panel renders
- **Then** a `milestone:q3` tag chip appears
- **And** no Milestone `SmartSelect` is present (was `TaskDetailPanel.tsx:641-655`)

#### Scenario: Board tag chip filters the board
**Traces to**: US-11, AS-8
**Category**: Alternate Path
- **Given** several tasks tagged `release`
- **When** the operator clicks the `release` tag chip in the filter bar
- **Then** the board filters to `release`-tagged tasks
- **And** the plan filter (if any) composes with it (AND)

### Feature: `/goal` and `/loop` in chat (US-12)

#### Scenario: Palette offers /goal and /loop automatically
**Traces to**: US-12, AS-1
**Category**: Happy Path
- **Given** `GET /commands` returns `/goal` and `/loop` (`delivery: agent`)
- **When** the operator types `/go` in the composer
- **Then** `/goal` and `/loop` appear in the palette Commands section
- **And** neither is hardcoded client-side

#### Scenario: Argument-hint ghost text for /goal
**Traces to**: US-12, AS-2
**Category**: Happy Path
- **Given** `/goal` carries an `argument_hint` of `<condition>`
- **When** the operator selects it from the palette
- **Then** the composer shows `/goal ` with ghost text `<condition>` (not `<message>`)

#### Scenario Outline: Goal indicator states
**Traces to**: US-12, AS-3, AS-4, AS-5, AS-6
**Category**: Edge Case
- **Given** a `goal_status` frame with state `<state>`
- **When** the `GoalIndicator` renders
- **Then** it shows `<render>`

**Examples**:
| state | render |
|---|---|
| active | condition + "round N/20" + latest judge reason |
| paused_judge_unavailable | "paused — waiting on judge" (no round increment) |
| brake_fired | "winding down (bound reached)" then clears |
| cleared | indicator removed |

#### Scenario: /goal clear removes the indicator
**Traces to**: US-12, AS-6
**Category**: Happy Path
- **Given** an active goal indicator
- **When** the operator sends `/goal clear`
- **Then** a confirmation renders in the thread
- **And** the indicator is removed

#### Scenario: /goal works on a non-web channel
**Traces to**: US-12, AS-7
**Category**: Alternate Path
- **Given** a Telegram-routed session
- **When** the operator sends `/goal ship the release`
- **Then** the command is parsed server-side and starts a goal
- **But** no palette UI is involved (palette is web-only)

### Feature: System Agents & judge transparency (US-13)

#### Scenario: Judge renders in a locked System section
**Traces to**: US-13, AS-1, AS-2
**Category**: Happy Path
- **Given** `fetchAgents()` returns a `type: system`, `locked: true` Judge
- **When** the Agents screen loads
- **Then** the Judge renders in a dedicated System section
- **And** its model/provider + rubric prompt are editable
- **But** no Delete, Disable, or ★ control is shown for it

#### Scenario: Judge excluded from delegation + team pickers
**Traces to**: US-13, AS-3, AS-4
**Category**: Edge Case
- **Given** a Judge exists (`type: system`)
- **When** a delegation-target picker or the Team roster add-picker enumerates agents
- **Then** the Judge is absent from both lists

#### Scenario: Judge verdict span in the ActivityPanel
**Traces to**: US-13, AS-5
**Category**: Happy Path
- **Given** a judge evaluation produced a `judge_verdict` frame with three criterion verdicts + spend
- **When** the operator opens the ActivityPanel
- **Then** a judge row shows a per-criterion verdict summary
- **And** the token/cost spend attributed to the Judge's `agent_id` is shown

#### Scenario: Judge verdict is panel-only by default
**Traces to**: US-13, AS-6
**Category**: Edge Case
- **Given** verbose chat is off
- **When** a judge verdict is produced
- **Then** no standalone judge card renders in the thread
- **But** with verbose chat on, the verdict renders inline in the thread

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | `PLAN_STATE_COLORS`/`LABELS`, tag validation fn, plan/tag filter fns, frame reducers | Pure logic in isolation |
| Component (vitest + RTL) | `PlanFilterBar`, plan card, Create/Edit Plan, criteria editor, evidence viewer, tag input, `GoalIndicator`, Agents System section, judge `ActivityRow` | Rendered behaviour + a11y |
| E2E (Playwright, embedded SPA) | create plan→approve→run→judge→done; `/goal` set→rounds→clear; migration visible | Full flows against the Go binary |

### Test Implementation Order

Write BEFORE implementation; unit → component → E2E; within a level, by dependency.

| Order | Test Name | Level | Traces to BDD | Description |
|---|---|---|---|---|
| 1 | `planStateColors.test.ts` | Unit | Plan state badge matrix | Every `PlanState` maps to a hex+label; unknown→draft fallback (mirrors `statusColors` test convention) |
| 2 | `tagValidation.test.ts` | Unit | Tag input validation | lowercase/trim/64/16 rules per SD-C8 boundary table |
| 3 | `planFilter.test.ts` | Unit | Selecting a plan filters | `filterByPlan` + `filterByTag` compose; All/Untagged sentinels |
| 4 | `chat.plan-status-frame.test.ts` | Unit | Owner disabled surfaces paused | `plan_status` reducer invalidates `['plans']`/`['tasks']`, updates paused state (pattern: `chat.notification-frame.test.ts`) |
| 5 | `chat.goal-status-frame.test.ts` | Unit | Goal indicator states | `goal_status` reducer stores per-session goal state incl. paused/brake/cleared |
| 6 | `chat.judge-verdict-frame.test.ts` | Unit | Judge verdict span | `judge_verdict` frame appends a `JudgeActivityItem`; zod-validated at edge |
| 7 | `ws.new-frames-validation.test.ts` | Unit | Frame edge validation | New frames: valid→parsed, missing field→drop+counter, missing session_id→drop (pattern: existing `ws` drop tests) |
| 8 | `PlanFilterBar.test.tsx` | Component | Board renders plan filter | Plan pills + tag chips + All/Untagged; no milestone UI; `aria-pressed`/`role=group` a11y (parity with `MilestoneFilterPills`) |
| 9 | `PlanCard.test.tsx` | Component | Plan card summary + matrix | name/goal/badge/owner/progress/bounds; badge matrix |
| 10 | `CreatePlanSlideOver.test.tsx` | Component | Create + Approve blocked | DoD criteria editor; Approve `400` lists per-task errors; no optimistic transition |
| 11 | `AcceptanceCriteriaEditor.test.tsx` | Component | Add check/prose; author shown | check→command+exit-code fields; prose→text; author identity; zero-criteria soft hint |
| 12 | `CriteriaVerdictList.test.tsx` | Component | Per-attempt verdicts + counter | met/unmet + reason; "attempt N/M" |
| 13 | `EvidenceViewer.test.tsx` | Component | Redaction + truncation | truncation marker; redaction marker; never renders raw secret |
| 14 | `TagInput.test.tsx` | Component | Tag validation feedback | boundary table; chips; remove; keyboard add (Enter) parity with todo input |
| 15 | `TaskCard.tags.test.tsx` | Component | Board tag chip + migrated tag | tag chips replace milestone chip (`TaskCard.tsx:191-195`); `milestone:` chip renders |
| 15a | `TaskCard.goalLoopStatus.test.tsx` | Component | Goal-loop status on card (FR-090) | "attempt N/M" + paused chip render |
| 16 | `TaskDetailPanel.no-milestone.test.tsx` | Component | Milestone dropdown gone | asserts absence of Milestone `SmartSelect`; presence of tags + criteria + Clear/Stop |
| 17 | `GoalIndicator.test.tsx` | Component | Goal indicator states | active/paused/brake/cleared rendering; 4000-char truncation |
| 18 | `ChatScreen.goal-loop-palette.test.tsx` | Component | Palette offers /goal /loop | commands from `GET /commands`; no hardcoding; argument-hint ghost (SD-C7) |
| 19 | `AgentsSystemSection.test.tsx` | Component | System section + exclusions | System section render (`system-agents-section`); editable model/rubric; no delete/disable/★; + `useChatAgents.test.ts` extension asserts `system` excluded from chat/`@`-mention; + `AgentDelegatePicker`/`AddAgentPicker` exclusion |
| 20 | `ActivityPanel.judge-row.test.tsx` | Component | Judge verdict span | per-criterion summary + spend; expandable with zero steps; panel-only default |
| 21 | `e2e/plan-lifecycle.spec.ts` | E2E | create→approve→run→verdicts→done | full plan flow against embedded binary |
| 22 | `e2e/goal-command.spec.ts` | E2E | /goal set→rounds→clear | indicator lifecycle over injected `goal_status` frames |
| 23 | `e2e/milestone-migration.spec.ts` | E2E | Migration visible | seed a legacy milestone, boot, assert `milestone:` tags + no milestone UI |

### Test Datasets

#### Dataset: Tag input boundaries (UI-side, SD-C8)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `""` | Empty | rejected, no chip | Tag validation | empty add is a no-op (mirrors todo `!text` guard) |
| 2 | `"a"` | Min | accepted `a` | Tag validation | single char valid |
| 3 | `"Release"` | Case | normalised `release` | Migrated milestone / validation | lowercase-on-normalise |
| 4 | `"  spaced  "` | Whitespace | trimmed `spaced` | Tag validation | trim |
| 5 | `"a"×64` | Max | accepted | Tag validation | at cap |
| 6 | `"a"×65` | Max+1 | rejected, "Max 64 characters" | Tag validation | over cap |
| 7 | 16 tags | Max collection | accepted | Tag validation | at per-task cap |
| 8 | 17th tag | Max+1 collection | rejected, "Max 16 tags per task" | Tag validation | over cap |
| 9 | `"Q3 Release"` | Special (space) | per SD-C8 (normalise vs reject) | Tag validation | space policy decided in SD-C8 |
| 10 | `"milestone:q3"` | Prefix convention | accepted verbatim | Migrated milestone | client never re-prefixes |
| 11 | `"<script>"` | Injection | rendered as inert text | Tag validation | React escapes; no exec |
| 12 | `"café"` (combining) | Unicode | accepted, grapheme-safe length | Tag validation | length by grapheme (mirror `truncateLabel` `SubagentBlock.tsx:29`) |

#### Dataset: Criteria editor validation

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | kind=check, empty command | Missing required | inline "command required" | Add machine-check | check needs a command |
| 2 | kind=check, exit code non-integer | Wrong type | inline "exit code must be an integer" | Add machine-check | numeric field |
| 3 | kind=check, exit code negative | Range | accept or reject per Agent A schema | Add machine-check | mirror trigger-interval min guard |
| 4 | kind=prose, empty text | Missing required | inline "criterion text required" | Add machine-check | prose needs text |
| 5 | zero criteria, human path | Empty collection | allowed + soft hint | Human zero-criteria | D5 |
| 6 | all-check criteria, assignee `bash: deny/ask` | State | UI warns (D2 rule 5) | (Behavioral) | warn, not block, in UI |
| 7 | criterion authored by other agent | State | author identity shown | Add machine-check | FR-3 author display |

#### Dataset: Plan-state badge matrix

| # | State | Boundary Type | Label | Hex | Traces to |
|---|---|---|---|---|---|
| 1 | `draft` | valid | Draft | #9ca3af | Badge matrix |
| 2 | `approved` | valid | Approved | #3B82F6 | Badge matrix |
| 3 | `running` | valid | Running | #D4AF37 | Badge matrix |
| 4 | `done` | valid | Done | #10b981 | Badge matrix |
| 5 | `failed` | valid | Failed | #ef4444 | Badge matrix |
| 6 | `""`/unknown | invalid | Draft (fallback) | #9ca3af | Badge matrix (tolerate unknown, mirror `statusColor` `statusColors.ts:83`) |

#### Dataset: Goal indicator states

| # | Frame state | Boundary Type | Render | Traces to |
|---|---|---|---|---|
| 1 | active, round 3/20, reason "tests fail" | valid | condition + "round 3/20" + reason | Goal indicator states |
| 2 | paused_judge_unavailable | valid | "paused — waiting on judge"; round unchanged | Goal indicator states |
| 3 | brake_fired (rounds) | boundary | "winding down (20/20 rounds)" then clears | Goal indicator states |
| 4 | brake_fired (calendar/7-day idle) | boundary | "winding down (idle expiry)" then clears | Goal indicator states |
| 5 | cleared | valid | indicator removed | /goal clear |
| 6 | condition length 4000 | Max string | truncated + title tooltip | Long content |

#### Dataset: Empty states

| # | Context | Expected | Traces to |
|---|---|---|---|
| 1 | Workspace with no plans | `PlanFilterBar` shows only "All"; optional "New plan" affordance; board shows all tasks | US-10 boundary |
| 2 | Task with no tags | no tag chips; tag input placeholder | US-11 empty |
| 3 | Task with no criteria (human) | soft hint, no verdict list | US-11 AS-2 |
| 4 | Plan with zero member tasks | pill renders; progress 0%; empty-chain message | US-10 boundary |
| 5 | No active goal | no `GoalIndicator` rendered (slot collapses like `RateLimitIndicator`) | US-12 |
| 6 | No judge activity | ActivityBar unmounts unless a retained failure/running (`ActivityBar.tsx:61`) | US-13 AS-7 |

#### Dataset: Long content / truncation

| # | Input | Expected | Traces to |
|---|---|---|---|
| 1 | 64-char tag chip | chip truncates with title tooltip (mirror `TaskCard` chip) | US-11 |
| 2 | 4000-char goal condition | indicator truncates; thread output wraps | US-12 |
| 3 | evidence output > size cap | truncation marker at cap | US-11 AS-4 |
| 4 | judge reason multi-line | wraps in verdict list; `whitespace-pre-wrap` | US-11 AS-3 |
| 5 | plan goal > card width | `line-clamp` truncation with tooltip | US-10 AS-3 |

#### Dataset: a11y / keyboard (new interactive elements)

| # | Element | Requirement | Traces to |
|---|---|---|---|
| 1 | Plan pill | `role=group`+`aria-pressed` per pill (parity with `MilestoneFilterPills.tsx:32,91`); single tab stop per pill | US-10 AS-1 |
| 2 | Tag filter chip | `aria-pressed`; Enter/Space toggles | US-11 AS-8 |
| 3 | Tag input | `aria-label`; Enter adds; removable chip has `aria-label="Remove tag X"` (mirror dep-chip `CreateTaskSlideOver.tsx:565-573`) | US-11 AS-6 |
| 4 | Criterion kind toggle / add | labelled controls; add via button + Enter (mirror todo add `CreateTaskSlideOver.tsx:601-621`) | US-11 AS-1 |
| 5 | Evidence expand | `aria-expanded`; disabled when nothing to expand (mirror `ActivityPanel.tsx:66-70`) | US-11 AS-4 |
| 6 | `GoalIndicator` update | `aria-live="polite"` announcement of state change (mirror mention announce `ChatScreen.tsx:2802`) | US-12 AS-3 |
| 7 | Clear/Stop buttons | destructive confirm for irreversible stop where appropriate (mirror delete `AlertDialog` `TaskDetailPanel.tsx:1036`) | US-10 AS-6 / US-11 AS-5 |
| 8 | Judge `ActivityRow` | `aria-expanded`; per-criterion summary reachable by keyboard | US-13 AS-5 |
| 9 | System agent card | no focusable delete/disable/★ (they are absent, not merely disabled) | US-13 AS-2 |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Board DnD transitions (`canDropTransition` `BoardView.tsx:60-72`) | `BoardViewDnd.test.tsx` | No — must keep passing unchanged | Plan/tag filter must not alter DnD, columns, or transition guards |
| Delegation roll-up on parent cards | `BoardViewRollup.test.tsx` | No — keep passing | Roll-up badge coexists with new tag chips on the card |
| Altitude nesting (`show-all`) | `TaskChildren.test.tsx`, `BoardViewRollup.test.tsx` | Yes — `PlanFilter.altitude.test.tsx` | Assert nesting preserved under a plan filter |
| Task card render (priority/todos/agent) | `TaskCard.test.tsx` | Update in place | Milestone chip removed; tag chips added; assertions on milestone chip must be migrated to tags |
| Create Task form | `CreateTaskSlideOver.test.tsx`, `CreateTaskSlideOver.initialDue.test.tsx` | Update in place | Milestone `Select` removed; tags + criteria added; `milestoneId` prop path removed |
| List filters | (none dedicated) | Yes — `ListView.filters.test.tsx` | Milestone `FilterSelect` → plan/tag filter; Milestone column → Tags column |
| Milestone progress bar | `MilestoneProgressBar.test.tsx` | **Delete** with the component | Component removed; test removed |
| Create milestone slide-over | `CreateMilestoneSlideOver.test.tsx` | **Delete** with the component | Removed |
| Slash palette (partitioned menu, ghost, no-hardcode) | `ChatScreen.partitioned-menu.test.tsx`, `ChatScreen.ghost-text.test.tsx`, `ChatScreen.no-hardcoded-commands.test.ts` | No — must keep passing | `/goal`/`/loop` flow through the same palette; SD-C7 ghost extension must not break skill ghost |
| Slash command error row | `ChatScreen.slash-commands-error.test.tsx` | No — keep passing | Unchanged |
| Activity aggregation + cap | `useRunningActivity.test.ts`, `ActivityBar.test.tsx`, `ActivityPanel.test.tsx` | Update in place | `JudgeActivityItem` joins the union; `RECENTLY_FINISHED_CAP` shared; failed-retain rule holds |
| Subagent span thread visibility | `ChatScreen.delegation-thread-visibility.test.tsx`, `SubagentBlock.test.tsx` | No — keep passing | Judge-verdict thread policy mirrors, does not alter, span policy |
| WS frame routing / drop counters | `chat.notification-frame.test.ts`, `ws` drop tests | No — keep passing | New frames extend the union; existing drop behaviour unchanged |
| Chat verbose preference | `chatPreferences.ts` store + `ChatSection` | Update copy | Verbose-chat help text may add "judge verdicts" to its list |
| Agents Library grouping (Main/Worker/Built-in) | `AgentListScreen.test.tsx` | Yes — extend for a 4th System section | Assert `system` renders in the new section, NOT `base-agents-section`; existing three sections unchanged |
| Agent card badges / default control | `AgentCard.test.tsx`, `WorkerCard.test.tsx` | Yes — `AgentCard.system.test.tsx` | `system` badge variant already `default`; assert no ★ for `system` |
| Agent profile locked behaviour | `AgentProfile.test.tsx` | Yes — `AgentProfile.system.test.tsx` | model/provider + SOUL editable; no delete/disable/default for `system` |
| Chat-agent scoping (status/worker/team) | `useChatAgents.test.ts`, `ChatScreen.agent-picker-scoping.test.tsx`, `AgentPicker.test.tsx`, `ChatScreen.agent-mention.test.tsx` | Yes — extend each | Assert a `type:system` agent is excluded from chat targets + `@`-mention |
| Team roster + delegation pickers | `WorkspaceTeamTab.test.tsx`, `team/AgentDelegatePicker.test.tsx` | Yes — extend | Assert `system` absent from add-picker (covers `AddAgentPicker.tsx:36`, currently only indirectly tested) and rejected as an edge target (SD-C17) |
| Agent kind helper | `agentKind.test.ts` | Yes — add `isSystem` cases | New predicate + flag |

#### MilestoneFilterPills removal — impact list

1. `WorkspaceTasksTab.tsx:4,92-98,159-174` — remove `MilestoneFilterPills`/`CreateMilestoneSlideOver` imports + render; wire `PlanFilterBar`.
2. `BoardView.tsx:23,135-136,241-247` — remove `MILESTONE_FILTER_UNSCHEDULED` import + `filterByMilestone`; add `filterByPlan`/tag.
3. `ListView.tsx:28,38,75-86,116-117,174,221-229` — remove milestone filter state, `FilterSelect`, column, cell.
4. `CreateTaskSlideOver.tsx:38,57-58,109,120,163-167,392-419` — remove `milestoneId` prop + Milestone `Select` + `buildBody` `milestone_id`.
5. `TaskDetailPanel.tsx:196-201,641-655` — remove Milestone `SmartSelect` + `fetchMilestones` query.
6. `TaskCard.tsx:3,78,191-195` — remove `milestone`/`milestones` prop usage + milestone chip.
7. `workspacesStore.ts:14-16,29-30` — `activeMilestoneId`→`activePlanId`; add `activeTagFilter`.
8. `api.ts:1926,3310` — `milestonesQueryKeys` + `fetchMilestones` removed (Agent A); `Milestone` type + endpoints removed.
9. Delete files: `MilestoneFilterPills.tsx`, `CreateMilestoneSlideOver.tsx`, `MilestoneProgressBar.tsx` (+ their `.test.tsx`).

#### System Agents — insertion points (additive, no deletions)

1. `agentKind.ts:13-15,52-67` — add `isSystem()` + `isSystem` flag on `agentKindFlags`.
2. `AgentListScreen.tsx:161` — exclude `system` from `mainAgents`; **:163+** — add `systemAgents` filter + a fourth `<section data-testid="system-agents-section">` cloned from Built-in (`:445-478`), no `onSetDefault`.
3. `useChatAgents.ts:81` — add `&& a.type !== 'system'` (single chat-exclusion point; AgentPicker + `@`-mention inherit).
4. `teamGraphModel.ts:343-352` — add `system` target rejection in `validateConnection` (defence-in-depth).
5. `AgentProfile.tsx:1126,1193-1223,2278-2292` — branch `isSystem` to suppress default-toggle + relabel the locked banner; keep model/provider/SOUL editable.
6. `AddAgentPicker.tsx:36` — **no change** (already `a.type !== 'system'`); add a direct test.
7. No contract change needed for the enum (`openapi-types.ts:2961` already has `system`); Agent A supplies the seeded Judge + any new `Agent` fields (rubric prompt reuses `soul`).

---

## Functional Requirements

- **FR-080**: The Board toolbar MUST render a `PlanFilterBar` (All pill + one pill per plan + tag filter chips + Untagged sentinel) in place of `MilestoneFilterPills`, reusing its `role=group`/`aria-pressed` a11y grammar.
- **FR-081**: Selecting a plan pill MUST filter the board to that plan's dependency-chain tasks (replacing `filterByMilestone`) while leaving DnD, columns, transition guards, and altitude/nesting unchanged.
- **FR-082**: The SPA MUST render a per-workspace Plans list where each plan card shows name, truncated goal, a `PlanState` badge, owner-agent avatar, progress (0–100%), and bounds (rounds/calendar).
- **FR-083**: The Create/Edit Plan slide-over MUST edit goal, DoD criteria, owner, and per-entity bound overrides, and MUST create/update via the `Plan` REST contract.
- **FR-084**: The Approve action MUST, on backend `400`, list the per-task criteria-missing validation errors inline and MUST NOT optimistically transition the plan state.
- **FR-085**: The SPA MUST provide Stop/Clear affordances (D8) on plan cards and task detail, calling the stop endpoint and optimistically flipping the plan/loop state pending server confirmation.
- **FR-086**: The SPA MUST surface a paused/blocked state on plan cards + their board tasks when the owner agent is disabled mid-loop, clearing on re-enable.
- **FR-087**: The Task create/detail surface MUST provide an acceptance-criteria editor supporting `check` (command + expected exit code) and `prose` kinds, showing each criterion's author identity.
- **FR-088**: For each attempt, the detail MUST show per-criterion met/unmet + judge reason and an attempt counter "attempt N/M".
- **FR-089**: The evidence viewer MUST render redaction markers and a truncation marker, and MUST NEVER render a raw secret value.
- **FR-090**: The Task card MUST show a goal-loop status affordance (round N/M or paused) when a task is running as a goal loop.
- **FR-091**: The SPA MUST provide a tag input with client-side validation feedback (lowercase-normalise, trim, max 64 chars, max 16 per task) and MUST NOT client-side re-prefix/uniquify migrated `milestone:` tags.
- **FR-092**: Tag chips MUST render on cards/rows/detail (replacing the milestone chip) and MUST be filterable from the `PlanFilterBar`; the Milestone dropdown MUST be removed from `TaskDetailPanel` and the Milestone filter/column from `ListView`.
- **FR-093**: `/goal` and `/loop` MUST appear in the web palette automatically from `GET /api/v1/commands` (`delivery: agent`), never hardcoded client-side.
- **FR-094**: The composer MUST show the command's argument-hint as ghost text when a `delivery: agent` command with an `argument_hint` is selected (SD-C7), and MUST render a persistent `GoalIndicator` (condition + round N/20 + latest judge reason) driven by `goal_status` frames, with distinct active / paused-judge-unavailable / brake-fired / cleared states.
- **FR-095**: The `AgentsLibraryView` MUST render `type: system` agents in a new dedicated locked "System" `<section>` (a fourth section alongside Main/Sub-agent/Built-in, mirroring the Built-in accordion `AgentListScreen.tsx:445-478`), and MUST exclude `system` from the `mainAgents` filter (`AgentListScreen.tsx:161`) so a Judge never renders as a Main chat colleague. In `AgentProfile`, model/provider (`ModelSelector`, `AgentProfile.tsx:1308-1312`) and the rubric prompt (SOUL textarea via `BehaviorFields`, `AgentFormFields.tsx:179-192`) MUST be editable for a System agent; the Delete action (`deleteAgent`, backend rejects locked — `api.ts:940-941`), the Disable/status control, and the ★ default-toggle (`AgentProfile.tsx:1212-1223`; `AgentCard` ★ `AgentCard.tsx:120-130`) MUST NOT be rendered for `type: system`.
- **FR-096**: `type: system` agents MUST be excluded from: (a) chat-target enumeration — add `a.type !== 'system'` to `useChatAgents` (`useChatAgents.ts:81`), which both `AgentPicker` and the `@`-mention menu inherit; (b) the Team-add picker — already enforced (`AddAgentPicker.tsx:36`), keep; (c) delegation-edge targets — defensively gated in `validateConnection` (`teamGraphModel.ts:343-352`) even though (b) already prevents a System agent from joining a team; (d) default-agent fallback — excluded by (a) since `isChatTarget = !isWorker` must also drop `system` (Agent B narrows `IsChatTarget`; SPA mirrors via the `useChatAgents` guard).
- **FR-097**: The ActivityPanel MUST render judge calls as a new `ActivityItem` variant showing a per-criterion verdict summary + spend (attributed to the Judge `agent_id`), obeying `RECENTLY_FINISHED_CAP=8` and the retained-failure reachability rule.
- **FR-098**: Judge verdicts MUST be panel-only in the thread by default and render inline only under verbose chat (mirroring `shouldRenderSubagentSpan`).
- **FR-099**: The SPA MUST consume the new WS frames (`plan_status`, `goal_status`, `loop_status`, `judge_verdict`, extended `task_status_changed`) by (a) zod-validating each at the edge (`ws.ts`), dropping + counting invalid frames; (b) registering session-scoped frames in `SESSION_SCOPED_FRAME_TYPES`; (c) invalidating `['tasks']`/`['plans']` queries and updating goal/loop/judge store state; (d) applying optimistic updates for operator-driven plan-state transitions.

---

## Success Criteria

- **SC-040**: After board load in a workspace with plans, zero milestone UI elements are present (0 `MilestoneFilterPills`, 0 milestone `Select`/`FilterSelect`, 0 milestone column) — asserted by grep-style DOM query in E2E.
- **SC-041**: Selecting a plan pill filters the board to that plan's chain with p95 render < 100ms for ≤200 tasks (client-side filter, no refetch), and DnD/altitude tests pass unchanged.
- **SC-042**: The plan-state badge renders the correct label+hex for all 5 states + unknown-fallback (6/6 dataset rows pass).
- **SC-043**: The tag input rejects/normalises 12/12 boundary rows with the exact message per SD-C8.
- **SC-044**: The evidence viewer renders a truncation marker for capped output and a redaction marker for redacted spans, with 0 raw secrets in the DOM (asserted against a secret-bearing fixture).
- **SC-045**: `/goal` and `/loop` appear in the palette with argument-hint ghost text, with 0 hardcoded command literals (asserted by `ChatScreen.no-hardcoded-commands.test.ts` extension).
- **SC-046**: The `GoalIndicator` renders all 4 lifecycle states (active/paused/brake/cleared) from injected frames, and clears within one frame of `/goal clear`.
- **SC-047**: The Judge renders in the System section with editable model/rubric and 0 delete/disable/★ controls; it is absent from delegation picker, team roster, and default-star (asserted by role/label queries).
- **SC-048**: A `judge_verdict` frame produces exactly one panel row (0 thread cards with verbose off; 1 thread entry with verbose on), with per-criterion summary + spend visible.
- **SC-049**: All new WS frames pass edge validation for valid payloads and increment `_droppedFrameCount` for malformed ones (valid→parsed, invalid→dropped), with 0 production crashes on a malformed frame.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-080 | US-10 | Board renders plan filter bar | `PlanFilterBar.test.tsx` |
| FR-081 | US-10 | Selecting a plan filters; Altitude preserved | `planFilter.test.ts`, `PlanFilter.altitude.test.tsx`, `BoardViewDnd.test.tsx` |
| FR-082 | US-10 | Plan card summary; badge matrix | `PlanCard.test.tsx`, `planStateColors.test.ts` |
| FR-083 | US-10 | (Create Plan) | `CreatePlanSlideOver.test.tsx` |
| FR-084 | US-10 | Approve blocked | `CreatePlanSlideOver.test.tsx` |
| FR-085 | US-10, US-11 | Stop/Clear a running plan | `PlanCard.test.tsx`, `TaskDetailPanel.no-milestone.test.tsx` |
| FR-086 | US-10 | Owner disabled surfaces paused | `chat.plan-status-frame.test.ts`, `PlanCard.test.tsx` |
| FR-087 | US-11 | Add machine-check; zero-criteria hint | `AcceptanceCriteriaEditor.test.tsx` |
| FR-088 | US-11 | Per-attempt verdicts + counter | `CriteriaVerdictList.test.tsx` |
| FR-089 | US-11 | Evidence redaction + truncation | `EvidenceViewer.test.tsx` |
| FR-090 | US-11 | (goal-loop status on card) | `TaskCard.goalLoopStatus.test.tsx` |
| FR-091 | US-11 | Tag input validation | `tagValidation.test.ts`, `TagInput.test.tsx` |
| FR-092 | US-11 | Migrated milestone→tag; dropdown gone; board tag filter | `TaskDetailPanel.no-milestone.test.tsx`, `TaskCard.tags.test.tsx`, `ListView.filters.test.tsx` |
| FR-093 | US-12 | Palette offers /goal /loop | `ChatScreen.goal-loop-palette.test.tsx` |
| FR-094 | US-12 | Argument-hint ghost; goal indicator states | `ChatScreen.goal-loop-palette.test.tsx`, `GoalIndicator.test.tsx`, `chat.goal-status-frame.test.ts` |
| FR-095 | US-13 | Judge in locked System section | `AgentsSystemSection.test.tsx` |
| FR-096 | US-13 | Judge excluded from pickers | `AgentsSystemSection.test.tsx` |
| FR-097 | US-13 | Judge verdict span | `ActivityPanel.judge-row.test.tsx`, `chat.judge-verdict-frame.test.ts` |
| FR-098 | US-13 | Judge verdict panel-only | `ActivityPanel.judge-row.test.tsx` |
| FR-099 | US-10..13 | (all frame scenarios) | `ws.new-frames-validation.test.ts`, `chat.{plan-status,goal-status,judge-verdict}-frame.test.ts` |

**Completeness**: every FR-08x/09x has ≥1 BDD + ≥1 test; every BDD scenario above appears in ≥1 row.

---

## Spec Decisions (SD-Cx) — SPA/UX ambiguities resolved within ADR-049 bounds; CLAUDE.md UI rules win

- **SD-C1 — Plans surface placement**: The Plans list is a **panel within the workspace Tasks tab** (a collapsible section above/beside the board), NOT a new top-level route — mirrors how `WorkspaceTasksTab` already owns milestone+board in one surface (`WorkspaceTasksTab.tsx:37`). Rationale: keeps plan↔board filter coupling in one component; avoids a new route + nav entry for a v0.1.1 slice. (In-ADR-bounds: ADR-049 §6 says board filters by plan; it does not mandate a route.)
- **SD-C2 — Filter-bar composition**: `PlanFilterBar` shows plan pills AND tag chips in one bar with two sentinels: `PLAN_FILTER_ALL = null` and `PLAN_FILTER_UNTAGGED` (analogue of `MILESTONE_FILTER_UNSCHEDULED` `MilestoneFilterPills.tsx:7`). Plan selection and tag selection compose (AND). Store keys: `activePlanId: string | null`, `activeTagFilter: string | null` on `useWorkspacesStore`.
- **SD-C3 — Plan progress source**: Progress is a **read-time computed** field on the `Plan` wire type (mirrors milestone progress being computed server-side, `rest_milestones.go:121`); the SPA renders `plan.progress ?? 0` — it does not compute progress client-side.
- **SD-C4 — Approve is confirm-on-success, not optimistic**: because Approve can `400` on missing criteria (FR-6 strict for agent-created tasks in the chain), the badge transitions only on `2xx`; the `400` payload's per-task list renders inline. Stop/Clear (SD-C5) may be optimistic because it cannot validation-fail.
- **SD-C5 — Optimistic scope**: Only Stop/Clear and drag-to-column (existing) use optimistic UI; Create/Approve/criteria/tag edits are confirm-on-success + query invalidation (consistent with current `WorkspaceTasksTab` `moveMutation` invalidate-only pattern, extended with an `onMutate` optimistic snapshot for Stop/Clear).
- **SD-C6 — Plan-state palette is its own module**: add `src/lib/planStateColors.ts` (`PLAN_STATE_COLORS`/`PLAN_STATE_LABELS`/`planStateColor()`/`planStateLabel()`) mirroring `statusColors.ts` exactly, with the mapping draft=`#9ca3af`, approved=`#3B82F6`, running=`#D4AF37` (Forge Gold marquee "live"), done=`#10b981`, failed=`#ef4444`, unknown→draft fallback. Rationale: never share a symbol with task `STATUS_COLORS` (the two domains diverge; the codebase already paid for that divergence once — `statusColors.ts:1-9`). Dark-first, no emoji (CLAUDE.md UI rules).
- **SD-C7 — Argument-hint ghost text for commands**: **Finding** — `SlashCommand` (`contracts/components/schemas/SlashCommand.yaml`) has NO `argument_hint`; ghost text is currently skills-only (`useSlashMenu.ts:475,700,861`), and a `delivery: agent` command just inserts `/name ` text with no ghost (`useSlashMenu.ts:677-682`). **Decision**: Agent A adds an optional `argument_hint` to `SlashCommand`; this section extends `executeSlashCommand`'s `delivery: agent` branch to set `ghostSkillId`/`ghostArgumentHint` (reusing the `completeSkillName` ghost path, `useSlashMenu.ts:700-707`) so `/goal ` shows `<condition>`. If Agent A declines the contract change, fallback is the palette-row `description`/`usage` only, no inline ghost (still satisfies "appears automatically" but not the ghost sub-goal).
- **SD-C8 — Tag normalisation vs rejection**: lowercase + trim are applied silently on commit (normalise, no error); length > 64 and count > 16 are hard rejections with an inline message; **spaces/`prefix:value`** are permitted verbatim (D1 tag rules: "free-form strings, lowercase, trimmed, max 64, max 16, `prefix:value` is convention only"). Therefore `Q3 Release` normalises to `q3 release` (space kept, lowercased) — NOT rejected. (Update the dataset row 9 to "normalised".) Validation lives in a pure `validateTag()`/`normalizeTag()` for unit testing.
- **SD-C9 — Goal indicator lives in the RateLimitIndicator slot**: `GoalIndicator` renders above the composer in the same non-scrolling slot as `RateLimitIndicator` (`ChatScreen.tsx:2748-2762`), session-scoped, driven by `goal_status` store state. It is persistent (not dismissable) while a goal is active; it collapses when cleared (like `ActivityBar`'s `empty:hidden`). `aria-live="polite"` announces state transitions.
- **SD-C10 — Judge verdict thread visibility = panel-only by default**: consistent with `shouldRenderSubagentSpan` (`toolVisibility.ts:218-223`, verbose-only) and NFR-5's "auditable/visible via named mechanisms" (transcript entry type + ActivityPanel span). The judge verdict IS persisted as a transcript entry type (Agent A), but rendering it as a standalone thread card by default would be exactly the noisy internal-LLM-call surface the tool-visibility rules suppress. Decision: **panel-only by default; inline in the thread only under verbose chat**. Justification vs verbose-chat precedent: judge calls are out-of-turn internal LLM actions with no standalone chat meaning to a reader (same class as `delegate`/background-`bash`), so they follow the established hide-by-default-with-verbose-override rule rather than inventing a new always-visible surface. The `ChatSection` verbose help copy (`ChatSection.tsx:38-43`) is updated to name judge verdicts alongside delegation cards.
- **SD-C11 — Judge span shape in `useRunningActivity`**: add a `JudgeActivityItem` member to the `ActivityItem` union (`useRunningActivity.ts:86`) carrying `criterionVerdicts: {text, met, reason}[]`, `spend: {tokens, costUsd}`, and correlation ids (plan/task/goal), rendered by a judge-specific `ActivityRow` branch (`ActivityPanel.tsx:37`). It shares `RECENTLY_FINISHED_CAP=8` and the failed-retain reachability rule (`ActivityBar.tsx:49-61`) with agent/bash items. Populated from `judge_verdict` frames (Agent A), keyed by verdict id.
- **SD-C12 — Attempt counter display**: "attempt N/M" where M is the effective per-task bound (default 3, D7), sourced from the task's **`attempt_count` wire field (contract C17, Part A)** — NOT `UnifiedMeta`, which carries only `/goal`/`/loop` session state (F5 r2); shown on the card as a compact affordance and in the detail as a labelled row. Paused (judge-unavailable) shows "attempt N/M · paused" without incrementing N.
- **SD-C13 — Criteria editor placement**: the criteria editor appears in BOTH Create Task (`CreateTaskSlideOver`) and Task detail (`TaskDetailPanel`), reusing the removable-row + add-button grammar of the existing todos/dependencies editors (`CreateTaskSlideOver.tsx:593-643`, `TaskDetailPanel.tsx:829-900`). `check` criteria reveal command + integer exit-code fields; `prose` reveals one text field; author identity is a read-only label per saved criterion.
- **SD-C14 — Tag chip replaces milestone chip on cards**: `TaskCard.tsx:191-195` milestone chip is replaced by a wrapping row of tag chips (Forge-Gold-tinted, same chip styling), capped visually with a "+N" overflow when tags exceed the card width; full list in the detail.
- **SD-C15 — `/goal status` / `/loop status` render as thread system messages**: their output is agent/command-delivered text rendered as normal thread content (not a bespoke card); the persistent live state is the `GoalIndicator`. `/goal clear` + `/loop stop` render a confirmation system message and clear the indicator.
- **SD-C16 — `isSystem` predicate + Library section (grounded in the confirmed anchors)**: the wire enum already carries `system` (`openapi-types.ts:2961`; `SlashConfig`/`SeedConfig` seed none today). Add `isSystem(a) = a.type === 'system'` to `agentKind.ts` (alongside `isWorkerType`, `agentKind.ts:13-15`) and an `isSystem` flag to `agentKindFlags` (`agentKind.ts:52-67`). In `AgentListScreen.tsx:161-163` add `const systemAgents = filteredAgents.filter((a) => a.type === 'system')` and change `mainAgents` (L161) to also exclude `system` (`&& a.type !== 'system'`) — otherwise a Judge silently renders as a Main agent (confirmed current behaviour). Render `systemAgents` in a fourth `<section data-testid="system-agents-section">` cloned from the Built-in accordion (L445-478), subtitle e.g. "System agents — locked, run out-of-turn (Judge)." Do NOT pass `onSetDefault` into its cards (mirrors workers, `AgentListScreen.tsx:438`), so no ★ is offered. The type badge already renders distinctly (`AgentCard.tsx:16-19`, `system: 'default'`).
- **SD-C17 — Delegation-edge defence-in-depth**: `AddAgentPicker.tsx:36` already blocks System agents from team membership, and delegation targets must be team members — so a System agent cannot become a delegation target through the supported flow. Still, `validateConnection` (`teamGraphModel.ts:343-352`) has no type gate; add a `target.type === 'system'` rejection there so a hand-constructed/legacy edge to a System agent is inert (defence-in-depth, mirrors the ADR-049 "invalid as routing/delegation target" requirement). `teamGraphModel.ts:214-215` already returns the `'System'` role label, so no label work is needed.
- **SD-C18 — AgentProfile locked-control suppression for System**: reuse the existing `agentKindFlags` `isLocked` path (`AgentProfile.tsx:1126`) but branch a new `isSystem` so: name/description stay read-only like core (`AgentProfile.tsx:1167-1182`), the locked banner copy (`AgentProfile.tsx:2278-2292`) reads "System agent — model, provider and rubric are editable; identity is locked", the default-toggle block (`AgentProfile.tsx:1193-1223`) is suppressed for `system` (unlike core, which keeps it — L1188-1190), and model/provider + SOUL(rubric) remain editable. The server already rejects identity/prompt-lock violations (`AgentProfile.tsx:721-736`); the SPA must not offer the reject-guaranteed controls.

---

## Assumptions

- Agent A delivers the `Plan`, `PlanState`, `AcceptanceCriterion`, `EvidenceRecord`, `JudgeVerdict`, `Task.tags`, `Agent.type: system`/`locked`, and the four WS status frames as generated types + zod schemas before SPA work merges (Constraint #8, 5-step process).
- Agent B's plan engine + Judge emit the frames named here (final spelling may differ; §H wiring is spelling-agnostic).
- The `Task` wire type gains `tags`, `criteria`, and loop-counter fields; `milestone_id` and `Milestone`/`fetchMilestones`/`milestonesQueryKeys` are removed (no back-compat, ADR-035/037 precedent).
- No new top-level route is added for Plans (SD-C1); the Agents screen gains a section, not a route.
- Playwright E2E runs against the embedded Go binary (CLAUDE.md SPA embed pipeline), with injected WS frames via `window.__omnipus_test_hooks`.

## Clarifications

### 2026-07-19
- Q: Does `SlashCommand` carry an argument hint for `/goal` ghost text? -> A: No — it is skills-only today; SD-C7 adds it to the contract (Agent A) or falls back to palette description.
- Q: Is the judge verdict shown inline in the chat thread? -> A: Panel-only by default; verbose-chat inline (SD-C10), per the tool-visibility precedent.
- Q: Do plan and task states share a colour module? -> A: No — separate `planStateColors.ts` (SD-C6) to avoid cross-domain symbol coupling.
- Q: Is `Q3 Release` a valid tag? -> A: Yes, normalised to `q3 release` (lowercase+trim; spaces permitted, D1) (SD-C8).

---

# Consolidated Cross-Part Index

## Numbering Reconciliation

Ranges are disjoint by construction; no collisions:

| Part | User Stories | Functional Reqs | Success Criteria | Spec Decisions |
|------|--------------|-----------------|------------------|----------------|
| A — Data/Contracts/Migration | US-1..4 | FR-001..039 | SC-001..015 | SD-A1..A14 |
| B — Engine/Judge/Loops/Commands | US-5..9 (+US-cross) | FR-040..079 | SC-020..039 | SD-B1..B10 |
| C — SPA/UX | US-10..13 | FR-080..099 | SC-040..049 | SD-C1..C18 |

Each Part contains its own Traceability Matrix (FR → US → BDD → Test). The completeness rule (every FR traces to ≥1 BDD and ≥1 test; every BDD appears in a matrix row) is enforced **within each Part**; the grill should verify per-Part matrices rather than a single merged one, since the domains are independently testable.

## Cross-Part Dependencies (resolved)

These are the seams where one Part produces a type/field another Part consumes. All are resolved here so no implementation wave is blocked on an unowned artifact:

1. **`SlashCommand.argument_hint`** — Part C's `/goal`,`/loop` ghost-text UX (SD-C7) requires an `argument_hint` field on `SlashCommand`, which does not exist today (`contracts/components/schemas/SlashCommand.yaml`; ghost text is skills-only, `useSlashMenu.ts`). **Resolution:** add `argument_hint` (optional string) to `SlashCommand.yaml` as a **Part A contract task** (append to the contract-surface table as row C16), regenerate. Part C's agent-delivery branch reuses the existing skill ghost path. If descoped, the documented fallback is palette-description-only (no ghost) — non-blocking.
2. **`judge_verdict` transcript entry + `Message.type` enum + `verdict` object** — owned by **Part A** (contract rows C11/C12: `JudgeVerdict`, `CriterionVerdict`, `Message.type += judge_verdict`). Part B writes verdict entries alongside the ADR-043 marker; Part C renders them (panel-only by default, SD-C10). No `EntryTypeJudgeVerdict` exists today (`pkg/session/daypartition.go`) — additive, A owns.
3. **`Plan`, `AcceptanceCriterion`, `EvidenceRecord`, `JudgeVerdict`, `Task.tags`, `Task.plan_id`** wire types — owned by **Part A** (rows C1–C11). Parts B (engine reads/writes) and C (SPA renders) consume them spelling-agnostically; the generated types are the only legal cross-boundary shape (Constraint #8).
4. **`goal_status` / `loop_status` / `plan_status` AsyncAPI frames** — named and schema-owned by **Part A** (row C14); runtime emission semantics specified by **Part B**; SPA consumption (zod-validate at edge, query invalidation) by **Part C** (FR-099).
5. **`UserInitiated` turn-scoped origin flag** — Part B's origin gating (SD-B6) requires a first-class origin signal because `bus.InboundMessage` has none and scheduled runs bypass the inbound bus (`exec.ProcessScheduled`). This is a **Part B** net-new internal field (not wire); it threads through the four entry paths (user WS message = true; cron/scheduled/async-notifier/delegated = false). Not a cross-boundary type; no contract impact.
6. **`IsPrivilegedAgent` narrowing** — owned jointly: Part A specifies the one-line change (`pkg/security/ratelimit.go`, `core`-only) and the seeding; Part B specifies the SEC-26 test extensions asserting a `type:system` agent is rate-limited + cost-capped. Verified: all four SEC-26 seams already pass `AgentType`, so no call-site edits (SD-B/A concur).
7. **Judge default-model resolution** (ADR Gap #1) — Part B's runtime resolution (chosen at onboarding; fallback = default agent's model) reads the model field Part A seeds on the Judge `AgentConfig`. No "cheapest model" heuristic (not machine-derivable).

## Master Ambiguity & Spec-Decision Register

The per-Part registers (SD-A1..A14, SD-B1..B10, SD-C1..C18, plus each Part's Ambiguity Warnings) are the authoritative resolution record; all were resolved **within ADR-049 bounds**. Items the grill should specifically pressure-test (flagged by the drafting agents as the least-settled):

- **SD-A6 (migration completion sentinel):** `.milestones_migrated` sentinel + no-per-file-deletion chosen over per-file deletion, because a partial-crash re-run over a surviving subset would reorder the ID-keyed collision suffixes. ADR required "idempotent, crash-safe"; the mechanism is a spec choice. → Grill: is the sentinel + Phase-3-only-dir-removal genuinely crash-safe against a crash *between* the sentinel write and the dir removal? (Spec says yes: next boot sees sentinel → skips + sweeps leftover dir.)
- **SD-B6 (origin gating carrier):** net-new `UserInitiated` flag vs inferring origin across four entry paths. ADR Gap #8 left the enforcement point open. → Grill: are all four non-user entry paths (cron, scheduled, async-notifier, delegated sub-turn) provably covered, with a fail-closed default (unknown origin ⇒ not user-initiated ⇒ cannot start a loop)?
- **SD-A13 (redact-before-truncate ordering):** redaction must precede truncation so a secret straddling the size cap is still fully scrubbed. → Grill: confirm no code path truncates first.
- **Regression contract change (Part B):** marker-as-claim means (a) no-signal is now *unmet/re-dispatch* not terminal-fail, and (b) explicit `update_task(done)` by an agent is now judged. These change two shipped behaviors; the ADR-043 parser is **extended, not forked**. → Grill: verify the existing `task_completion_contract_test.go` expectations are updated coherently, not deleted.

No ambiguity remains OPEN/undecided; every item is either resolved in-spec or listed above for grill pressure.

---

# Round-1 Grill Reconciliation (AUTHORITATIVE — overrides per-Part text on conflict)

> This section resolves the cross-part contradictions surfaced by grill round 1. Where it
> conflicts with any per-Part statement, **this section wins**; implementers follow it. It is
> the single source of truth for the shared schema seams the three Parts touch.

## R1 — Canonical `PlanState` (resolves C1, M9)

The wire `PlanState` enum has **exactly five values**, defined once in `Plan.yaml` (contract C2):
`draft`, `approved`, `running`, `done`, `failed`. Part B's nine runtime states are re-expressed against these five:

| Part B runtime state | Canonical representation |
|---|---|
| `draft` | `PlanState=draft` |
| `active` | `PlanState=running`, `plan_phase=dispatching` |
| `judging` | `PlanState=running`, `plan_phase=judging` |
| `synthesizing` | `PlanState=running`, `plan_phase=synthesizing` |
| `paused` | `PlanState=running`, `paused_reason` non-empty |
| `done` | `PlanState=done` |
| `failed` | `PlanState=failed`, `failed_reason=judge_rounds_exhausted` |
| `stopped` | `PlanState=failed`, `failed_reason=stopped_by_user` |
| `expired` | `PlanState=failed`, `failed_reason=idle_expired` |

- New non-badged fields on `Plan` (Part A owns; add to `Plan.yaml`/struct): `plan_phase` (enum `dispatching|judging|synthesizing|idle`, default `idle`, runtime-only, **not** a `PlanState`) and `failed_reason` (enum `judge_rounds_exhausted|stopped_by_user|idle_expired`, set only when `PlanState=failed`). `paused_reason` and `active_loop` already exist in Part A's struct — reuse them; Part B does not introduce a parallel state field.
- **Part C** badges only the five `PlanState` values (SD-C6 matrix is correct and complete); when `PlanState=running` it MAY show a secondary phase/paused chip from `plan_phase`/`paused_reason`, and when `PlanState=failed` it renders a secondary chip from `failed_reason` (O1 r2: distinguishes user-stopped vs judge-exhausted vs idle-expired — otherwise all three collapse to "Failed"), but never treats any of these secondary fields as states. The SD-C6 "unknown → draft" fallback is removed (the enum is now closed and total).
- **Approve gating (M9, F2 r2):** the `draft→approved` transition runs the approval check with a **tiered DoD rule matching ADR D5**: for **agent-authored plans (SD-A7 strict tier)** a non-empty DoD is required; for **human/UI-authored plans** the DoD MAY be empty (soft tier — the plan judge then evaluates against title+goal, SD-A7). In **all** cases, every member task MUST carry ≥1 criterion or approval is rejected 400 listing the offenders (FR-084; this member-task gate is unconditional). **Approval is the only explicit act (O2 r2):** approving transitions `draft→approved`, and the single plan-engine instance then auto-transitions `approved→running` on its next tick and begins dispatch — there is no separate "Start" action; `approved` is a brief transitional state. Part B's transition table is amended to include `approved` between `draft` and `running`; the transition matrix in Part A §"State machine" is canonical, with **`failed` terminal/frozen** (F1 r2).
- The global-active-loop count (R5) treats a plan as active iff `PlanState=running` (equivalently `active_loop=true`), independent of `plan_phase`.

## R2 — Migration reads legacy `milestone_id` from raw JSON (resolves C2, m1, m2)

Resolved inline in Part A migration pseudocode (Phase 2): the migration parses each task file into a legacy raw view (`map[string]json.RawMessage`) and reads the `milestone_id` key **struct-independently**, because FR-032 removes `Task.MilestoneID` and `json.Unmarshal` silently drops unknown keys (`pkg/task/store.go:97`). New mandatory test (supersedes the false-passing Test 28): **`TestMigrateMilestones_LegacyJSONAfterFieldRemoved`** — a task JSON literal containing `"milestone_id":"m1"` (with NO such struct field compiled in) still gains `milestone:<name>` after migration. m2 pseudocode bug (capture `mid` before any clear) is fixed in the same block. m1 (empty-milestone log accuracy after a partial-crash rerun) is an accepted best-effort limitation: **data integrity is unaffected** (SC-012 holds; tags/Due are byte-identical), only the empty-vs-had-members log line may differ on a crash rerun — documented, not blocking.

## R3 — Canonical WS frames + `type` literals (resolves M1, M6)

Four new AsyncAPI frames, each a one-file schema referenced from `asyncapi.yaml` with a matching `receive*` operation (mirroring `TaskStatusChangedFrame`/`receiveTaskStatusChanged`, `asyncapi.yaml:368-375`). The `type` discriminator literal is **canonical and used verbatim** in the Part A schema, Part B emission, and Part C consumer + tests (the `WsFrameSchema` union keys on the exact literal — no "spelling-agnostic" latitude):

| Frame schema | canonical `type` literal | operation | payload |
|---|---|---|---|
| `GoalStatusFrame` | `goal_status` | `receiveGoalStatus` | condition, round, max_rounds, latest_reason, active_loops, cap, state |
| `LoopStatusFrame` | `loop_status` | `receiveLoopStatus` | mode, run, max_runs, next_delay?, state |
| `PlanStatusFrame` | `plan_status` | `receivePlanStatus` | plan_id, state, plan_phase, progress, paused_reason? |
| `JudgeVerdictFrame` | `judge_verdict` | `receiveJudgeVerdict` | the `JudgeVerdict` payload (per-criterion met/reason) |

- **Part C correction:** every `plan_status` reference (FR-099, SD-C11, `chat.plan-status-frame.test.ts`) uses `plan_status`. Frame `type`s are `goal_status`/`loop_status`/`plan_status`/`judge_verdict` — no `_changed` suffix (that suffix belongs only to the pre-existing `task_status_changed`).
- **`judge_verdict` has two carriers (M1):** the live **`JudgeVerdictFrame`** WS push (this table) AND the persisted transcript **`Message.type: judge_verdict`** (contract C12). Both carry the same `JudgeVerdict` shape (contract C11). ActivityPanel + optional thread rendering consume the frame live; session replay reads the transcript entry. Add `JudgeVerdictFrame` as **contract row C-new-1** to the C14 frame set.

## R4 — Contract-surface additions (resolves M2, M5, M7; completes Constraint #8)

Append these rows to the Part A contract-surface table (each follows the 5-step spec-first process):

| # | Wire type / field | Schema file | Referenced from | Notes |
|---|---|---|---|---|
| C16 | `SlashCommand.argument_hint` (optional string) | edit `SlashCommand.yaml` | existing `/commands` response | enables `/goal`,`/loop` ghost text (SD-C7); Part C agent-delivery branch reuses the skill ghost path |
| C17 | `Task.attempt_count` (int, read-only, server-set) | edit `Task.yaml` | already referenced | current run's attempt index; renders "attempt N/M" (FR-088) |
| C18 | `Task.max_attempts` (optional int) + `TaskCreateRequest`/`TaskUpdateRequest` | edit those three | already referenced | per-task attempts override; nil ⇒ inherit `PlanningConfig.TaskMaxAttempts` |
| C19 | `Plan.progress` (number 0..1, read-only) + `Plan.plan_phase` + `Plan.failed_reason` | edit `Plan.yaml` | `/plans` responses | `progress` server-computed at REST layer (like milestone counts); `plan_phase`/`failed_reason` per R1 |
| C20 | `JudgeVerdictFrame` | `JudgeVerdictFrame.yaml` | `asyncapi.yaml` + `receiveJudgeVerdict` | live judge-verdict push (R3) |

The standalone-task attempt counter lives on `Task` (C17), NOT in `UnifiedMeta` — it crosses the wire for the SPA. `UnifiedMeta` carries only the `/goal`/`/loop` **session** loop state (condition, round, mode, run, bounds), which is session-scoped and already Part B's domain.

## R5 — Global active-loop cap authority (resolves M8, m5)

The cap authority **co-locates with the single plan-engine instance** (the singleton with the cron-style overlap guard, ADR D4). There is no separately-mutated counter (avoids drift): at each loop-admission decision the engine computes the active count from persisted state — plans with `PlanState=running`, sessions with an active `/goal`, and enabled `/loop` cron jobs — reconciled at boot. The counted set is **exactly those three sources** (F3 r2: `PlanState=running` plans + active `/goal` sessions + enabled `/loop` jobs). A **standalone task attempt-loop does NOT count** (m5/F3: SD-B4's garbled clause is superseded) — it is bounded by its own attempt ceiling (default 3) and self-terminates; task attempt-loops **inside** a running plan likewise do not count individually (the running plan counts as one). This is consistent with FR-076, US-cross AS-3, SC-033, and the cap datasets — none of which are changed. Admission is serialized by the engine's single-writer overlap guard, so the 15th/16th/17th-start race resolves deterministically (17th → rejected `active loops: 16/16`).

## R6 — Origin gating positive predicate for channels (resolves M3, m7)

`UserInitiated` is a turn-scoped boolean set explicitly at **each** message-origination point, fail-closed (any path not listed ⇒ false):

| Origination point | `UserInitiated` |
|---|---|
| Web WS `message` handler (authenticated gateway user) | **true** |
| Channel adapter inbound (Telegram/Discord/Slack/… real human `Sender`) | **true** |
| Cron/scheduled runner (`exec.ProcessScheduled`) | **false** (regardless of surface — a cron job targeting a channel is still false) |
| Async-notifier synthesized system message | **false** |
| Delegated sub-turn (`spawnSubTurn`) | **false** |

This makes `/goal`/`/loop` work for a genuine channel end-user (US-12 AS-7) while a cron-injected `/goal` stays inert (H7), because the discriminator is the **origination path**, not the surface or a web-only `GatewayUserID`. A channel message carries a real `Sender` but no `GatewayUserID`; the channel-inbound path sets `UserInitiated=true` directly. **m7:** "author differs from assignee" (cross-agent machine-check confirmation, Gap #2) is decided by author **identity ≠ assignee agent id**, regardless of author kind — a user-authored machine check on an agent-assigned task DOES require confirmation unless waived by the workspace setting (threat-model closure: no unattended check authored by a different principal runs under the assignee's `bash: allow` without an approval gate).

## R7 — Part A Traceability Matrix (resolves M4)

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 Plan entity persists | US-1 | Operator creates a plan with a DoD criterion | `TestPlanStore_CreatePersists` |
| FR-002 same-workspace plan_id FK | US-1 | Task in a foreign workspace cannot reference a plan | `TestTask_PlanIDWorkspaceGuard` |
| FR-003/004 Plan state machine | US-1 | Plan state transitions (outline) | `TestPlan_StateTransitions` |
| FR-005 persisted plan counters | US-1 | (added) Plan counters survive reload | `TestPlan_CountersPersistAndReload` |
| FR-006 delete running plan rejected | US-1 | Deleting a running plan is rejected | `TestPlan_DeleteRunningRejected` |
| FR-007 membership computed read-time | US-1 | Deleting a draft plan clears plan_id | `TestPlan_MembershipComputed` |
| FR-008..011 tags normalize/validate/dedup/cap | US-2 | Tag normalization and validation (outline) | `TestTask_TagValidation` |
| FR-012 no global tag registry | US-2 | (added) same tag in two workspaces unrelated | `TestTask_TagWorkspaceScoped` |
| FR-013..017 migration name→tag/collision/due/empty/idempotent | US-2 | Milestone migration scenarios (5) | `TestMigrateMilestones_*` incl. `TestMigrateMilestones_LegacyJSONAfterFieldRemoved` (R2) |
| FR-018 criterion author recorded | US-3 | (added) author recorded at authorship time | `TestCriterion_AuthorRecorded` |
| FR-019..024 criterion/evidence validation + redaction + retention | US-3 | Criterion validation (outline); evidence redaction/truncation | `TestCriterion_Validate`, `TestEvidence_RedactBeforeTruncate`, `TestEvidence_DeletedWithTask` |
| FR-025..031 migration properties | US-2 | Re-run no-op; crash re-runs identically; empty preserved | `TestMigrateMilestones_Idempotent`, `_CrashSafe`, `_EmptyLogged` |
| FR-032 MilestoneID removed | US-2 | (regression) removal list | `TestTask_NoMilestoneField` (compile-time + JSON) |
| FR-033..038 Judge seeding/lifecycle/privilege | US-4 | Judge seed; system-agent create/delete/disable rejected; SEC-26 applies | `TestSeed_JudgeSystemAgent`, `TestAgents_SystemTypeUncreatable`, `TestAgents_JudgeUndeletable`, `TestRatelimit_SystemNotPrivileged` |
| FR-039 per-entity bounds resolution | US-1/US-4 | (added) Plan.Bounds/Task.MaxAttempts precedence over global | `TestBounds_PerEntityOverridesGlobal` |

SC-001..015 map onto these tests; SC-012 (migration data integrity) is covered by `TestMigrateMilestones_LegacyJSONAfterFieldRemoved` + `_CrashSafe`.

## R8 — set_todos exemption gets coverage (resolves M10)

New BDD (Part B feature "Task goal-loop"):
**Scenario: Scratchpad set_todos task is never dispatched into a goal loop** — *Traces to*: US-5, AS (D5 exemption); *Category*: Edge Case — **Given** a task created via `set_todos` (`Scratchpad=true`), **When** the plan engine / task drain scans for dispatchable work, **Then** the scratchpad task is skipped (never enters an attempt loop, never judged) **And** no criteria are required of it. New test **`TestTaskExecutor_ScratchpadExemptFromGoalLoop`**; add the FR-048 row to Part B's traceability matrix.

## R9 — Minor closures

- **m3 (evidence ExitCode sentinel):** on timeout or policy-deny the authoritative signals are the `TimedOut`/`PolicyDenied` bools; `ExitCode` is set to **-1** (sentinel) in those cases; consumers MUST check the bools before interpreting `ExitCode`.
- **m4 (post-backoff cadence):** after the 3-step judge backoff (60/120/300s) is exhausted, retries continue at **300s** intervals until the loop's idle-expiry (7d) fires — a permanently-unavailable judge ends the loop via calendar brake, never via a false verdict.
- **m6 (FR→test mismaps):** FR-074 (session-ownership/role gating) traces to a real new test **`TestRoleGating_SessionOwnership`** (not the origin-gating test); FR-090 (goal-loop status affordance) traces to **`TaskCard.goalLoopStatus.test.tsx`** (not the tags test). Both tests are added.
- **o1 (SC-025 margin):** a check exceeding its 60s deadline is killed within **≤5s** of the deadline (bounded-margin quantified).

## Reconciliation → new/changed test obligations (carried into the wave plan)

`TestMigrateMilestones_LegacyJSONAfterFieldRemoved`, `TestPlan_CountersPersistAndReload`, `TestTask_TagWorkspaceScoped`, `TestCriterion_AuthorRecorded`, `TestBounds_PerEntityOverridesGlobal`, `TestTaskExecutor_ScratchpadExemptFromGoalLoop`, `TestRoleGating_SessionOwnership`, `TaskCard.goalLoopStatus.test.tsx`, plus the `JudgeVerdictFrame` zod-edge validation test and the `plan_status` (not `_changed`) frame test.

---

# Evaluation Scenarios (Holdout)

> **Note**: Post-implementation evaluation only. NOT for the implementing agents; NOT referenced in any Part's TDD plan or traceability matrix.

### Scenario H1: Fresh-install plan end-to-end
- **Setup**: Fresh `$OMNIPUS_HOME`, onboarded, one workspace, default seeded agents + the seeded Judge, a tool-capable model configured.
- **Action**: In chat, ask Jim to "plan and execute: create `docs/CHANGELOG-TEST.md` with three dated entries, then verify it exists" as a plan with two dependent tasks; approve the plan in the UI.
- **Expected**: Plan appears on the board with its two tasks filterable by plan; tasks run in dependency order; each completion shows judge verdicts with evidence (file-exists check exit 0); plan reaches done; owner synthesis arrives in the session. No milestone UI anywhere.
- **Category**: Happy Path

### Scenario H2: /goal drives a session to a provable condition
- **Setup**: Running gateway, chat session with default agent, workspace with a writable filesystem.
- **Action**: Send `/goal a file named goal-proof.txt exists in the workspace and contains the word done`.
- **Expected**: Agent works toward it; after each round a judge evaluation is visible (round counter increments); the goal clears itself once the file provably exists; status before completion shows condition, rounds N/20, latest judge reason; `/goal` after completion reports no active goal.
- **Category**: Happy Path

### Scenario H3: Task with a failing machine check exhausts attempts and wakes the owner
- **Setup**: Plan with one task whose only criterion is a check command that always exits 1; assignee has `bash: allow`; plan owner is a different agent.
- **Action**: Approve the plan; observe.
- **Expected**: Exactly 3 attempts (visible counter), each with an unmet verdict + reason; then the owner agent receives a wake with failure context (visible in its session); the task is not marked done; the plan does not advance past it.
- **Category**: Error

### Scenario H4: Brake + handover on a runaway /loop
- **Setup**: `/loop`-capable session; `loop_max_runs` configured to 3 for the test.
- **Action**: Start `/loop 1m note the current time in the session`; wait.
- **Expected**: After 3 runs the loop stops itself; a handover summary (progress + next step) appears in the session; `/loop` status shows the loop ended by run-cap, not error.
- **Category**: Edge Case

### Scenario H5: Judge outage does not burn attempts
- **Setup**: Task loop mid-flight; make the Judge model unreachable (invalid provider key for the Judge's provider only).
- **Action**: Let an attempt complete its worker turn.
- **Expected**: Status shows a judge-unavailable pause with backoff; the attempt counter does NOT increment; restoring the provider resumes judging the same attempt; no false failure recorded.
- **Category**: Error

### Scenario H6: Milestone upgrade preserves groupings and deadlines
- **Setup**: Pre-upgrade data dir with milestones "Q3", "q3" (distinct), an empty milestone "Q4" with a due date, tasks with and without their own `Due`.
- **Action**: Boot the new binary once; boot again (idempotency).
- **Expected**: Tasks show tags `milestone:q3` and `milestone:q3-2` per original grouping; tasks with no `Due` inherit their milestone's date; "Q4" name+date appear in the migration log; second boot changes nothing; milestone endpoints 404/410 and the SPA shows tag filters instead of milestone pills.
- **Category**: Edge Case

### Scenario H7: A cron message cannot start a goal
- **Setup**: A schedule whose message text is exactly `/goal delete all files` targeting a chat-capable agent.
- **Action**: Let it fire.
- **Expected**: No goal is created (status shows none); the text is handled as inert content; no loop appears in active-loop counts.
- **Category**: Edge Case

---

# Assumptions

- The four grounding research reports (2026-07-19) and the seams re-verified across grill rounds r1–r3 remain accurate; each Part re-cited its load-bearing seams at file:line.
- Single-operator deployments are the v1 audience for role gating (Gap #5); multi-user role tiers for loop creation are future work.
- `evals/judge/scorer.go`/`prompt.go` stay offline; the runtime Judge is a new call path sharing rubric shape, not code.
- Plan/Task/Evidence JSON documents stay small enough for whole-file atomic writes (same envelope as tasks).
- Issue closure for this epic is manual at the release→main merge (non-main base; ADR §2).

# Clarifications

### 2026-07-19 (operator interview + follow-ups)
- Plan container → new Plan entity; **remove Milestones**, replace with generic multi-tags.
- Judge → evidence ladder, fail-closed.
- Coordinator → hybrid engine + owner-agent decision points.
- DoD → strict on agent tool paths; soft on human/UI paths.
- Judge identity → System Agents category (revived `system` type); **Judge only this epic** (Summarizer premise was false — `forceCompression` deleted; Memory System Agent lands with v0.3 dreaming/scheduled-retros).
- /loop → both modes (interval + self-paced), separate from /goal.
- Budgets → **no money/token brakes**; counts + calendar, all configurable; graceful wind-down; clear affordances at every level.
- Release placement → ships **in release v0.1.1**.
- Pipeline → full pipeline **without taskify**; grill-spec capped at 3 rounds.
