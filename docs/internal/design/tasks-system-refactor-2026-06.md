# Task System Refactor — Assessment & Plan

**Status:** Pre-ADR assessment (discussion stage) · supersedes the task-tool-granularity &
scheduling portions of `tasks-redesign-2026-05.md`
**Date:** 2026-06-24
**Phase:** v0.3 (tasks redesign — no back-compat, per the locked release strategy)
**Branch context:** `feat/0.1.0-uat-fixes` (assessment done against the live tool registry + code)

Two problems surfaced from a user-perspective review of the task + scheduling tools:

1. **The `cron` tool is redundant** — scheduled tasks + the per-agent heartbeat already cover
   scheduling; cron is a third, parallel, user-invisible system.
2. **The general task tools are over-granular** — `task_update` is misnamed (status-only, not
   edit), `task_add_dependency` is a standalone tool that should be part of edit, and there is no
   general "edit a task's fields" tool for agents.

---

## 1. Scheduling: retire `cron` → scheduled tasks + heartbeat

### Finding

Tasks already carry a first-class **`Trigger`** (`pkg/task/task.go:87-126`) with four kinds, fired
by the **task trigger scheduler** (`pkg/gateway/gateway.go` `TaskTrigger`):

| Trigger | Meaning |
|---------|---------|
| `manual` | no auto-fire (drag to in_progress / Run) |
| `once` | fire once at `at_ms` |
| `every` | fire every `every_ms` (≥1000 ms) |
| `recurring` | fire on a `cron_expr` (5/6-field cron) |

A scheduled task's prompt is executed by its assigned agent — through the agent's policy + sandbox,
and the task is **visible on the board/Calendar**. So one-shot, interval, and cron-expr scheduling
are all already expressible as scheduled tasks.

That makes `cron` (`pkg/tools/cron.go`, backed by a separate `cron/jobs.json` store +
`pkg/cron.CronService`) a **third, parallel scheduling system** that fires reminders/messages and
(optionally) raw shell commands via `at_seconds` / `every_seconds` / `cron_expr`. Three mechanisms
for one job:

1. **Scheduled tasks** (`task.Trigger`) — visible on the board/Calendar, policy-gated, agent-executed.
2. **Heartbeat** — per-agent periodic autonomous tick (`HEARTBEAT.md`).
3. **Cron** — invisible parallel store firing messages/commands.

### Why it feels redundant to users

Cron jobs are **not surfaced in the user-facing board/Calendar** — only scheduled tasks are. From
the operator's seat, cron jobs don't exist as a concept ("there are no schedules, only scheduled
tasks + the heartbeat"). That perception is accurate.

### The one distinct cron capability — and why it's the wrong abstraction

Cron can run a **raw shell `command`** directly (e.g. `df -h`). But:

- It is gated behind `tools.cron.allow_command` (off by default).
- A raw shell fire **bypasses the agent's policy/sandbox**, whereas a scheduled task with an agent
  prompt ("run `df -h` and report") achieves the same outcome **safely** — through the agent.

So cron's distinguishing feature is arguably the *wrong* abstraction (direct shell execution
outside the agent), not a reason to keep it.

### Decision (proposed, v0.3)

**Retire the `cron` tool.** Migrate its use cases to scheduled tasks (`task.Trigger` + prompt).
One-shot → `once`; interval → `every`; cron-expr → `recurring`. Raw-command fires become scheduled
tasks with an agent prompt (safer, policy-gated).

**Removes:** one tool, one parallel store (`cron/jobs.json`), one parallel scheduler
(`CronService`), and the hidden-command-execution footgun. Scheduled tasks already cover every
scheduling shape cron offered.

**Migration note:** any existing `cron/jobs.json` entries should be converted to scheduled tasks
(`Trigger` + the job's message as the prompt) at v0.3 boot, then `cron/` removed.

---

## 2. Task tools: over-granular — simplify 6 → 4

### Finding — two inconsistent surfaces

| | General (agent-facing, 6 tools) | System/admin (4 tools) |
|---|---|---|
| create | `task_create` — title/prompt/agent/priority/parent **(no `blocked_by`)** | `system.task.create` — all fields incl. `blocked_by` |
| edit | `task_update` — **status only** (in_progress/done/failed + result/artifacts) | `system.task.update` — all fields incl. `blocked_by` (replaces list) |
| dependency | `task_add_dependency` — standalone (cycle-detect + cross-workspace guard) | *(folded into update)* |
| todo | `task_add_todo` — checklist {text, done} | — |
| list/delete | `task_list`, `task_delete` | `system.task.list`, `system.task.delete` |

### The core problems

1. **`task_update` is misnamed.** It only sets *status* (`in_progress`/`done`/`failed`) + result +
   artifacts (`pkg/tools/task.go:315`). It is really "report task completion," **not** an edit. There
   is **no general 'edit a task's fields' tool** for an agent — an agent cannot rename, reprioritize,
   reassign, re-due, or re-link a task today. That is a real capability gap, not just naming.
2. **`task_add_dependency` should be part of edit, not standalone.** The admin side already proves
   the right shape: `system.task.update` accepts `blocked_by` (replaces the list) and
   `system.task.create` accepts it at creation (`pkg/sysagent/tools/task.go:141-239`). The general
   side split dependency-adding into a separate tool for no benefit. The cycle-detection +
   cross-workspace guard (`pkg/tools/task.go:533-548`) move into the update path cleanly.
3. **`task_create` (general) can't set `blocked_by` at creation** — a parity gap with the admin
   tool. Setting a dependency today requires create + a second `task_add_dependency` call.
4. **`task_add_todo` vs subtasks.** This is a *deliberate* three-tier model (`Todo` < `Task` <
   `Subtask`; `pkg/task/task.go`: "A todo is NOT a task: no agent, no status, no trigger, no ID").
   Todos (lightweight checklist lines) vs subtasks (nested real tasks) is intentional, not
   accidental redundancy — so this is a product call, not a clear cut.

### Decision (proposed, v0.3) — simplify the general side 6 → 4

1. **Fold `task_add_dependency` into `task_update`.** `task_update` accepts `blocked_by`
   (add/remove edges); the cycle-detection + cross-workspace guard move into the update path.
   Eliminates a tool.
2. **Broaden `task_update` from status-only to a real edit** — title, priority, due, assignee,
   `blocked_by`, status (+ result/artifacts when reporting completion). One "modify a task" tool;
   status-reporting becomes a subset. Closes the no-general-edit gap.
3. **Let `task_create` accept `blocked_by` at creation** — parity with the admin tool (set
   dependencies when you create, not as a second call).
4. **`task_add_todo`: keep** (recommendation). The three-tier model is deliberate and todos are
   cheaper than spawning sub-agent subtasks. Open to revisiting, but no strong reason to remove.

**Result:** `task_list`, `task_create` (+`blocked_by`), `task_update` (real edit, incl. deps),
`task_delete`. Drops `task_add_dependency` (folded). `task_add_todo` retained.

The admin `system.task.*` set is already correctly consolidated — no change needed there beyond
keeping parity with the broadened general `task_update`.

---

## Scope & next steps

- **Phase:** v0.3 (tasks redesign). No back-compat (fresh-build per the release strategy).
- **Pre-requisite:** this assessment feeds the v0.3 tasks-redesign **ADR** (`/albert`), then a
  `/plan-spec`. Do not implement before the ADR ratifies the retire-cron + fold-dependency
  decisions.
- **Concept doc:** the `.preview-doc/tools-catalog.html` Tool Catalog page is updated to reflect
  the intended end-state — `cron` and `task_add_dependency` removed, `task_update` shown as the
  broadened edit tool — with a note pointing here.
- **Open questions for the ADR:**
  - Migration of existing `cron/jobs.json` → scheduled tasks (auto-convert at boot vs. document +
    drop).
  - Whether `task_add_todo` stays (recommendation: yes) or the three-tier model collapses to
    tasks/subtasks only.
  - Whether the broadened `task_update` should be `privileged` (mutating) or stay `open` for
    status-only with `privileged` for field edits — i.e. split the risk within one tool.
