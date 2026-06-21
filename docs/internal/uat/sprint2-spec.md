# Sprint 2 — Unified Task model + trigger engine (the no-back-compat big-bang)

**Spec of record:** `remediation-decisions.md` (D3, D4, Tier-2 Details #1–#8). **Migration: NONE** (Detail #7) —
no users, no back-compat. **Delete** `pkg/boardtask` + `pkg/taskstore` and their REST endpoints; the new
unified `Task` wire schema **replaces** `BoardTask` + `Task` outright. This is a coordinated cross-layer
change that lands as ONE reviewed feature branch (contract → store → tools → REST/WS → FE views), not
interleaved with other sprints.

> **Sequencing note:** this depends on Sprint 1 landing. It is the largest single change in 0.1.0 and a
> destructive store rewrite — open its own branch `feat/0.1.0-s2-unified-tasks` and checkpoint with the
> human before merge.

## 1. The unified Task entity (Detail #2)
One entity merging `boardtask.Task` + `taskstore.TaskEntity`. Wire schema `contracts/components/schemas/Task.yaml`
(replaces the existing `Task` and `BoardTask`). Fields:

| field | type | notes |
|---|---|---|
| `id` | string (UUID) | |
| `title` | string (1..200) | the name |
| `description?` | string (≤2000) | |
| `prompt?` | string (≤10000) | agent prompt for `llm` action |
| `action` | enum | **`llm` only now**; reserves `human`/`tool`/`notify`/`sub_workflow` |
| `status` | enum (7-state, §2) | |
| `agent_id?` | string | optional — human-only tasks have none |
| `agent_name?` | string | display, read-time |
| `priority` | int 1..5 | default 3 |
| `blocked_by` | string[] | DAG ordering; write-time cycle validator (keep boardtask's) |
| `todos` | Todo[] | `{text, done}` — lightweight checklist, NOT subtasks (§3) |
| `parent_task_id?` | string | delegation / sub-task link |
| `workspace_id` | string | **required-scoped**; every task belongs to a workspace |
| `milestone_id?` | string | |
| `trigger?` | TaskTrigger | §4 — folds board `start`/`recurrence` + workflow `trigger_type` |
| `due?` | date-time | deadline, separate from trigger |
| `surface` | enum | `user` (default, shows on all views) / `heartbeat` (hidden from general views) — Detail #5 |
| `source_channel?` / `source_chat_id?` | string | delegated-task result delivery (Detail #6) |
| `session_id?` | string | run session |
| `result?` | string (≤50000) | |
| `artifacts?` | string[] | output paths |
| `owner` / `created_by` | string | server-set attribution |
| `created_at`/`updated_at`/`started_at?`/`completed_at?` | date-time | |

`Todo` schema: `{ text: string(1..500), done: bool }`. `rollup` (read-time, Detail #6): derived list of live
child sub-agent runs for board roll-up badges — `[{agent_id, label, status}]`; computed, never stored.

## 2. Status lifecycle (Detail #1) — 7 states
`inbox → next → planning → in_progress → done` (+ `failed`), with `blocked` an auto side-state.
- **inbox** captured/untriaged · **next** ready · **planning** agent decomposing (light in Tier 2) ·
  **in_progress** worked by a human OR agent · **blocked** unmet dependency (auto-set; clears to `next` when
  deps `done`) · **done** · **failed**.
- `in_progress` is **decoupled from `/start`** — a human drags a card there, or assign+Run hands it to an agent.
- No separate manual "waiting" — `blocked` (dependency-driven) covers it.

## 3. Todos vs subtasks vs tasks (three tiers — LOCKED)
- **todo** = a lightweight `{text,done}` checklist item on a task (`task.todos[]`). Not a task; no agent, no status.
- **subtask** = a full child Task with `parent_task_id` (own status/agent/trigger).
- **task** = top-level (no parent). Board/List/Graph/Calendar show top-level tasks; subtasks nest.

## 4. Trigger model (Detail #3) — extensible `{type,config}`, time-only in Tier 2
`TaskTrigger` schema designed for the v0.3 multi-trigger/boolean future but Tier-2-restricted:
- `kind`: `manual` (no trigger — starts by drag-to-in_progress or Run) · `once` (`at_ms`) · `every`
  (`every_ms`) · `recurring` (`cron_expr`).
- `every`/`recurring` **spawn a fresh run each fire** (fresh session + run history + pause) — reuse the
  existing per-agent Schedules engine (`pkg/cron`) as the **trigger executor** (a schedule = a task with a
  recurring trigger). Heartbeat = a recurring-trigger task with `surface:heartbeat`, Main-only.
- Design the schema as `{ type, config }` so v0.3 adds `on_task`/`on_agent`/`on_message`/`webhook`/`on_condition`
  + AND/OR composition **additively**.

## 5. Store + engine (BE)
- New `pkg/task` (or evolve one of the two) = the single store. Per-entity JSON files, atomic writes, the
  64-shard mutex pattern. Keep boardtask's DAG cycle validator + auto-advance (`blocked` → `next` on dep-done).
- Fold `pkg/cron` in as the trigger executor; a recurring/every task drives a cron job; one-shot `once` fires `at_ms`.
- **Delete** `pkg/boardtask`, `pkg/taskstore`, their REST handlers, the `task_list(scope=both)` merge hack, the
  title-field disambiguator, dual status enums.
- One task tool surface (`pkg/sysagent/tools/task.go`): create/update/list/get/add-todo/add-dependency over the
  one store. Delegated `task`-mode creates a Task with `parent_task_id` (Detail #6) → visible card + roll-up.
- **Workspace→turn binding** (M4): the active workspace scopes task reads/writes + tags the session
  (`SessionMeta.WorkspaceID` already exists). Connections carry a workspace tag.

## 6. REST/WS contract
- `GET/POST /tasks`, `GET/PATCH/DELETE /tasks/{id}`, `GET /tasks/{id}/subtasks`, todo + dependency endpoints,
  all `workspace_id`-scoped. One `TaskStatusChangedFrame` over the existing WS for realtime board/graph updates
  (subsumes WS-E board realtime). `TaskCreateRequest`/`TaskUpdateRequest` unified (Detail #8 one create form).
- Follow the 5-step add-a-wire-type process; regenerate Go+TS+Zod; `make verify-contracts` clean.

## 7. Views (Detail #4) — FE (overlaps Sprint 4)
Four views over the one store: **Board** (kanban by status, one-shot tasks, roll-up badges) · **List** (flat,
filterable) · **Graph** (horizontal dependency DAG — renders `blocked_by`; the big new view) · **Calendar**
(recurring tasks by fire time; **replaces** the old Execution "second board"). Remove the Automations route.
Delegation appearance per Detail #6 (board roll-ups + depth/altitude toggle; graph = live tree).

## 8. Out of scope (v0.3 ADR) — design the shapes to grow into, don't build
Conditional/branching edges, retries, fan-out/in, non-LLM action types, event triggers + chaining, the
first-class Workflow container, LLM-plan-as-workflow, the GH-Actions-shaped DSL + `create_workflow` planning tool.

## Wave plan
contract (1 agent) → regen → BE store+engine+tools (1–2 agents) ‖ FE views (per-view agents, Sprint 4) →
integrate (one compiling tree) → 7-reviewer gate → CI on ci-omnipus → checkpoint with human before merge.
