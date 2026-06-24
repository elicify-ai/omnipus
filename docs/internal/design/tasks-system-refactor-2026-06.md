# Task System Refactor — Assessment & Plan

**Status:** Pre-ADR assessment (discussion stage) · supersedes the task-tool-granularity &
scheduling portions of `tasks-redesign-2026-05.md`
**Date:** 2026-06-24
**Phase:** v0.3 (tasks redesign — no back-compat, per the locked release strategy)
**Branch context:** `feat/0.1.0-uat-fixes` (assessment done against the live tool registry + code)

Five problems surfaced from a user-perspective review of the task + scheduling tools:

1. **The `cron` tool is redundant** — scheduled tasks + the per-agent heartbeat already cover
   scheduling; cron is a third, parallel, user-invisible system.
2. **The general task tools are over-granular** — `task_update` is misnamed (status-only, not
   edit), `task_add_dependency` is a standalone tool that should be part of edit, and there is no
   general "edit a task's fields' tool for agents.
3. **`task_add_todo` is an append-only dead-end and there is no agent scratchpad** — agents need an
   in-session scratchpad for lightweight planning, but the current todo tool can only *add* (no
   update/toggle/read) and is the wrong primitive. Replaced by a `todos` tool that rides the task
   substrate (§3).
4. **The `system.task.*` surface is mis-prefixed and divergent** — it overlaps the general `task_*`
   on create/update/list/delete, but the real distinction is workspace scope (current vs explicit /
   cross-workspace), not admin-vs-agent. Renamed to `task_*_in_workspace` and unified in behavior
   (§4).
5. **`pins` are a backend-only feature with no UI** — `system.pin.*` bookmark chat responses but
   nothing in the app shows them. Retire (§5).

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
4. **`task_add_todo` is an append-only dead-end and is the wrong primitive for an in-session
   scratchpad.** It only *adds* (`task_id` + `text` → `AppendTodo`, `pkg/task/store.go:647`); there
   is no toggle/update/remove, `Todo` has no ID (`{Text, Done}`, `pkg/task/task.go`), and there is
   no read path (no `task_get`/`task_read`; `task_list` returns summaries). So even as a task
   checklist it can't track progress — and it can't serve as the agent's in-session scratchpad (you
   can add a plan but never tick it off, edit it, or read it back). Retire it → replaced by `todos`
   (see §3).

### Decision (proposed, v0.3) — simplify the general side 6 → 5

1. **Fold `task_add_dependency` into `task_update`.** `task_update` accepts `blocked_by`
   (add/remove edges); the cycle-detection + cross-workspace guard move into the update path.
   Eliminates a tool.
2. **Broaden `task_update` from status-only to a real edit** — title, priority, due, assignee,
   `blocked_by`, status (+ result/artifacts when reporting completion). One "modify a task" tool;
   status-reporting becomes a subset. Closes the no-general-edit gap.
3. **Let `task_create` accept `blocked_by` at creation** — parity with the admin tool (set
   dependencies when you create, not as a second call).
4. **Retire `task_add_todo` → replaced by `todos`** (§3). Not kept — append-only with no update/read
   is a dead-end; the `todos` tool (replace-semantics, read-on-write, re-injected) supersedes it
   entirely.

**Result:** `todos` (§3), `task_create` (+`blocked_by`), `task_update` (real edit, incl. deps),
`task_list`, `task_delete`. Drops `task_add_dependency` (folded) and `task_add_todo` (→ `todos`).

The admin `system.task.*` set is already correctly consolidated — no change needed there beyond
keeping parity with the broadened general `task_update`.

---

## 3. The `todos` tool — agent scratchpad as a visible board task

### Goal

Every agent gets an in-session **scratchpad** for lightweight planning ("read this, summarize it,
email it" — a goal with a few steps). It must be editable (not append-only), readable across turns,
and — critically — **ride the task substrate, not a parallel store**. One record type, one store,
one lifecycle.

### The tool: `todos(goal, [{text, status}])`

- **`goal`** — the title of the task these todos serve (the unit of work the agent is currently
  focused on).
- **`[{text, status}]`** — the **full** checklist, **replace-semantics** (the agent rewrites the
  whole list, so no todo IDs are needed — this is the fix for append-only). `status` ∈
  `pending` / `in_progress` / `completed`.
- **Read-on-write:** returns the full updated list, so the agent sees its plan after every write
  (no separate read tool for the happy path).
- **Agent-unaware task creation (facade):** behind the tool, the call **creates-or-updates a
  board-visible task titled `goal`** with those todos as its checklist. The agent never calls
  `task_create`, never sees a `task_id` — it sets its goal + steps, and a task appears on the board.
  There is **no hidden `scratchpad` flag and no promotion step** — the "scratchpad" is just a
  lightweight board task created through a friendlier facade. It is visible from the start, which is
  the intent (the operator sees what each agent is working on and its progress).

### Re-injection (important)

At the start of each turn, the agent loop **re-injects the acting agent's current `goal` + todos**
into the turn preamble. This means:

- The agent always has its list, every turn, **without calling a read tool**.
- It **survives context compression** (single-layer compression drops ~50% oldest turns + a
  summary; the scratchpad is re-injected from the backing task, not from turn history — so the
  agent doesn't lose its plan when older turns compress away).
- The scratchpad becomes ambient working memory — which is what a scratchpad should be.

(If re-injection is ever deemed too much loop coupling, the fallback is a separate `todo_read`
tool — but re-injection is preferred: it keeps the surface to one tool and doesn't rely on the
agent remembering to re-read post-compression.)

### The scratchpad-vs-plan boundary is structural, not just naming

Both `todos` and multi-wave planning create **visible board tasks**, but of different *shapes*:

| | `todos` (flat checklist) | multi-wave plan (`task_create` + deps) |
|---|---|---|
| Shape | **one** task with a flat `[{text, status}]` checklist | **many** tasks linked by `blocked_by` (a DAG) |
| Dependencies | none (flat list) | yes (the DAG) |
| Use | "read this, summarize, email it" — one goal, a few steps | "migrate the auth system" — waves of dependent work |

The name **`todos`** (not `plan`) is deliberate: **"plan" is reserved for the dependency-linked
multi-wave case** (the task DAG). `todos` is the flat-checklist case. Boundary statement for the
seed prompt: *"Use `todos` for the throwaway checklist of what you're doing this turn; use
`task_create` + dependencies for a durable multi-wave plan."*

### Substrate changes

Validated against current code (`pkg/task/task.go:157`, `pkg/task/store.go:647`): `Task.Todos` is
already a **real JSON array** (`[]Todo`, `json:"todos,omitempty"`), not a text blob; `AppendTodo`
does array-append + `validateTodos` + atomic write. So the storage is correct; the changes are:

1. **`Todo {Text, Status}`** — upgrade `Done bool` → `Status` (`pending`/`in_progress`/`completed`).
2. **`SetTodos(taskID, []Todo)`** — full-replace, idempotent (the operation `todos` calls under the
   hood; replaces the append-only `AppendTodo` as the primary path).
3. **No new task flag** — the `scratchpad`/promotion machinery is **not** needed (dissolved by
   making the task visible from the start).

### Open choices for the ADR

1. **Board pollution / lifecycle.** Since every `todos` call makes a visible board task, what
   happens to completed/abandoned ones? Lean: **one active goal-task per agent** — calling `todos`
   with a new `goal` archives the previous, keeping the board to "what each agent is doing right
   now" + history. (Alternatives: auto-archive completed; or accumulate with board filtering.)
2. **Goal identity.** Does calling `todos` with the same `goal` update that task (replace), and a
   new `goal` create a new one? Lean: **goal-string identity within the session** (the agent just
   re-states its goal; the facade tracks the goal→task mapping). Alternative: an explicit id once
   created.
3. **Retention.** Clean up the (archived) goal-tasks at session end, or retain-as-audit (replay
   value: see how the agent planned). Lean: retain-as-audit behind a flag.

---

## 4. Cross-workspace task tools — rename + unify (keep both surfaces)

### Finding

The general `task_*` and the `system.task.*` surfaces overlap on create/update/list/delete, which
looked like pure redundancy (§2). It is not — there is one real, distinct capability: **creating
tasks in *other* workspaces** (the Orchestrator/Jim operating across workspaces). The two tools
differ in **workspace source**:

| Tool | Workspace | Scope |
|------|-----------|-------|
| `task_create` etc. | **auto-resolved from context** (current workspace; `resolveWorkspaceID`, `pkg/tools/task.go:182`) | open (any agent) |
| `system.task.create` etc. | **explicit `workspace_id`, required** (`"workspace_id is required"`, `pkg/sysagent/tools/task.go:92`) — can target *other* workspaces | privileged (core/admin) |

So both are needed: the plain tool works in the agent's current workspace; the explicit-workspace
tool lets the Orchestrator target other workspaces. The earlier "collapse `system.task.*`" idea
(once floated in discussion) is **rejected** — the cross-workspace capability justifies keeping a
second surface.

### Decision (proposed, v0.3)

1. **Rename** `system.task.*` → `task_create_in_workspace` / `task_update_in_workspace` /
   `task_list_in_workspace` / `task_delete_in_workspace`. Drop the misleading `system.` prefix:
   tasks are **agent-work**, not platform-administration (see the prefix rule below).
2. **Behavioral parity** — the cross-workspace variant is **identical to the plain `task_*` tool
   except for the workspace parameter**: same fields (incl. `blocked_by` — the §2 broadening
   applies to both), same validation, and the **same delegation checks** (`delegationDeny` /
   `delegateCheck`) the plain tool has today. Shared core implementation with a workspace-resolution
   seam: plain auto-resolves from context; `_in_workspace` uses the explicit `workspace_id`
   (required). Two thin wrappers, one behavior.
3. **Scope stays split** — `task_*` open (current workspace, any agent); `task_*_in_workspace`
   privileged (Orchestrator / explicit allow). Cross-workspace task creation is a distinct
   privilege worth gating independently — which is **why two tools beats one tool with an optional
   `workspace_id`**: policy can allow `task_create` (current workspace, default) while denying
   `task_create_in_workspace` (cross-workspace) for most agents, granting both only to the
   Orchestrator. Gating a parameter inside one tool is messier than gating a separate tool.

### The `system.` prefix rule (now consistent)

- **No prefix** = agent working tools (`task_create`, `task_create_in_workspace`, `read_file`,
  `browser.*`, `message`, `todos`, …).
- **`system.` prefix** = platform-admin only — operations with no agent parallel
  (`system.workspace.*`, `system.agent.*`, `system.channel.*`, `system.provider.*`,
  `system.mcp.*`, `system.config.*`, `system.skill.*`, `system.pin.*`, `system.cost.*`,
  `system.backup.*`, `system.doctor.*`, `system.navigate.*`, `system.models.*`). Genuinely
  operator/system-agent surface.

Tasks were the one family that broke the rule (a working op misfiled under `system.`); the rename
fixes it. The line is "manage the platform" (`system.`) vs "do work, possibly across workspaces"
(no prefix). Note: `system.workspace.*` (create/delete a workspace itself) stays admin-only;
targeting a workspace *for a task* (`task_create_in_workspace`) is working-tool territory — no
conflict.

### Net task tool surface (v0.3 end-state)

- **Current-workspace (open):** `todos`, `task_create`, `task_update`, `task_list`, `task_delete`.
- **Cross-workspace (privileged):** `task_create_in_workspace`, `task_update_in_workspace`,
  `task_list_in_workspace`, `task_delete_in_workspace`.

Was 6 general + 4 `system.task.*` = 10; becomes 5 + 4 = 9 (retire `task_add_todo` +
`task_add_dependency` = −2; add `todos` = +1; rename `system.task.*` → `task_*_in_workspace` = net
0).

---

## 5. Retire `pins` — backend-only, no UI

### Finding

`system.pin.create` / `system.pin.list` / `system.pin.delete` (`pkg/sysagent/tools/pin.go`) are a
**message-bookmarking** feature: a `pin` is a saved reference to a chat response (`{ID, Title,
AgentName, SessionID, MessageID, WorkspaceID, Tags, ContentPreview, CreatedAt}`), persisted as JSON
under `~/.omnipus/pins/`. An agent can pin one of its chat responses with a title + tags and later
list/search them.

**There is no UI for pins.** Verified in `src/`: zero pin components (no PinCard / PinList / `/pins`
route / create-pin control). The only "pin" in the UI is the `pinned` / `pin_order` field on
**workspaces** (pin a workspace to the sidebar top) — a different concept entirely. So `system.pin.*`
is a **half-built feature**: tools + store exist, but nothing in the app reads or shows them. A user
cannot see, create, browse, or manage pins.

### Decision (proposed, v0.3)

**Retire the 3 pin tools** (`system.pin.create/list/delete`) and remove the `~/.omnipus/pins/` store.
Same profile as the other dead-end / no-surface retirements this assessment makes (`cron`,
`task_add_todo`, `task_add_dependency`) — tool surface with no user-facing view.

**Reversal clause:** if v0.3 commits to a saved-messages / bookmarks feature, pins could be revived
behind a real UI (a Pins panel + a create-pin control on chat messages). Until then they are a
phantom and should go.

### Todo

- [ ] v0.3: remove `pkg/sysagent/tools/pin.go` (PinCreate/PinList/PinDelete) + their registration in
  `pkg/sysagent/tools/registry.go`; remove the `pins/` dir handling. Confirm no references in seed
  prompts or the SPA.
- [ ] Decide (ADR): saved-messages/bookmarks feature in v0.3? If yes → keep pins behind a new UI;
  if no → full removal as above.

---

## Scope & next steps

- **Phase:** v0.3 (tasks redesign). No back-compat (fresh-build per the release strategy).
- **Pre-requisite:** this assessment feeds the v0.3 tasks-redesign **ADR** (`/albert`), then a
  `/plan-spec`. Do not implement before the ADR ratifies the retire-cron + fold-dependency
  decisions.
- **Decided (this assessment):** retire `cron` (§1); fold `task_add_dependency` into `task_update`
  + broaden `task_update` to a real edit + `task_create` accepts `blocked_by` (§2); retire
  `task_add_todo` → `todos` scratchpad-as-board-task with re-injection (§3); rename
  `system.task.*` → `task_*_in_workspace` with behavioral parity (§4); retire `pins`
  (create/list/delete — backend-only, no UI) (§5).
- **Concept doc:** the `.preview-doc/tools-catalog.html` Tool Catalog page and `.preview-doc/time.html`
  are updated to reflect the intended end-state — `cron`, `task_add_dependency`, and `task_add_todo`
  retired; `task_update` shown as the broadened edit tool; the new `todos` tool (scratchpad-as-board-task)
  added; `system.task.*` renamed to `task_*_in_workspace` — with notes pointing here.
- **Open questions for the ADR:**
  - Migration of existing `cron/jobs.json` → scheduled tasks (auto-convert at boot vs. document +
    drop).
  - `todos` lifecycle: one active goal-task per agent (lean) vs. auto-archive-completed vs.
    accumulate-with-filtering (§3 open choice 1).
  - `todos` goal identity: goal-string-within-session (lean) vs. explicit id (§3 open choice 2).
  - `todos` retention: clean-up-at-session-end vs. retain-as-audit (§3 open choice 3).
  - Whether the broadened `task_update` should be `privileged` (mutating) or stay `open` for
    status-only with `privileged` for field edits — i.e. split the risk within one tool.
  - Cross-workspace parity detail: confirm the `task_*_in_workspace` variants gain the full
    delegation-policy checks (`delegationDeny`/`delegateCheck`) the plain `task_*` has today, so an
    Orchestrator creating a task in another workspace still goes through delegation policy (§4).
