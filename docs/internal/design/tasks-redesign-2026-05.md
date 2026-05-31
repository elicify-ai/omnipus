# Tasks redesign — 2026-05

## Status

Draft, decisions logged 2026-05-03. Ports the existing gateway-global task storage onto the two-room (private agent + shared project) workspace topology established in `sandbox-redesign-2026-05.md` and reused by `memory-redesign-2026-05.md`.

Companion documents:

- `docs/internal/design/sandbox-redesign-2026-05.md` — workspace topology.
- `docs/internal/design/memory-redesign-2026-05.md` — same scoping rules applied to memory.
- `docs/internal/design/projects-ui-2026-05.md` — Command Center task board surface.

## Problem

Task storage today is **flat and gateway-global**:

- One directory: `<OMNIPUS_HOME>/tasks/<id>.json` (`pkg/taskstore/store.go:101,118`).
- Every agent's tasks in the same place. Per-agent filtering done at read time via `agent_id` field.
- No notion of project; no cross-agent collaboration concept; no scoping.

Once projects exist as a first-class concept, tasks need the same scoping as memories: private to an agent (the default for personal sessions) or shared in a project room (for collaborative work).

## Decisions log

| # | Decision | Rationale |
|---|---|---|
| T1 | Tasks live under the room: `<room>/.omnipus/tasks/<id>.json` | Mirrors memory-redesign room model; same mental model for operators |
| T2 | Default scope follows session context (project-bound → project room; private → agent room); explicit `scope=` override | Matches memory-redesign D18 |
| T3 | `TaskEntity` shape (`pkg/taskstore/store.go:25-43`) is reused as the per-room file format; only directory layout changes | Existing `agent_id` (assignee) and `created_by` (creator) fields suffice for the new model — no schema rewrite needed |
| T4 | Project tasks are reassignable across project members; private tasks are not reassignable | Project tasks model collaborative work; private tasks model individual work |
| T5 | `parent_task_id` must reference a task in the same room | Hierarchies don't cross rooms; cross-room promotion is explicit |
| T6 | Promotion verb: `task_update(id, scope=project, [project_id=…])` moves a private task into a project | Same pattern as memory promotion |
| T7 | Project room deletion sweeps its task directory after operator confirmation | Tasks belong to the project lifecycle |

## Storage layout

```
agents/jim/.omnipus/
├── memories/
├── learnings/
├── sessions/
└── tasks/                          ← Jim's private tasks
    ├── tsk-7f3a.json
    └── tsk-2b1c.json

<project_root>/.omnipus/
├── memories/
├── learnings/
└── tasks/                          ← project shared tasks
    ├── tsk-9c2b.json               # created_by: jim, agent_id: jim
    ├── tsk-4d18.json               # created_by: jim, agent_id: mia
    └── tsk-1a8e.json               # created_by: mia, agent_id: ""   (unassigned)
```

`TaskStore` instances are now per-room rather than gateway-global. The gateway holds:

- One `TaskStore` per agent (rooted at `agents/<id>/.omnipus/tasks/`), constructed at agent boot.
- One `TaskStore` per project room (rooted at `<project>/.omnipus/tasks/`), constructed when the project is loaded.

## Tool surface

Existing task tools gain a `scope` parameter and route to the correct store based on session context. Names unchanged for continuity.

```
task_create(title, prompt, [agent_id=current], [scope=private|project], [parent_task_id=?])
  → writes to <room>/.omnipus/tasks/<id>.json
  → scope defaults from session.project_id (T2)

task_list([scope=private|project|both], [status], [agent_id])
  → defaults: scope=both when in project context, scope=private otherwise
  → flattens results across both stores when scope=both

task_update(id, ...)
  → finds task by id across both stores in current context
  → can update fields including scope (move private → project)

task_delete(id)
  → finds task by id across both stores
  → operator-gated when deleting another agent's task in a project room
```

`task_get(id)`, `task_dependencies(id)`, and other read paths follow the same "search both stores, current context first" rule.

## Cross-room rules

- **Hierarchies stay in-room.** A private task's `parent_task_id` must reference another private task. Same for project tasks. Cross-room dependencies are intentionally not modeled — promote first, then link.
- **Promotion (private → project).** Explicit `task_update(id, scope=project, project_id=foo)`. Moves the file from `agents/<id>/.omnipus/tasks/` to `<project_root>/.omnipus/tasks/`, preserves task id, appends a system event to `<project>/.omnipus/.system.jsonl` recording who promoted what when.
- **Demotion (project → private).** Not supported. Once a task is shared, it stays shared. Operator can delete it if needed.
- **Reassignment.** `task_update(id, agent_id=mia)` works only on project tasks (T4). Private tasks reject the field change.
- **Authorship.** `created_by` is set on creation and never mutated. `agent_id` (assignee) is mutable on project tasks.

## UI integration

Command Center task board pivots on rooms (see `projects-ui-2026-05.md`). Three filter modes:

- **All** — flattened across personal + every project the operator has access to. Default for the "what's on my plate" view.
- **Personal** — private tasks only, grouped by agent.
- **Per project** — that project's tasks, grouped by assignee.

Drag-and-drop reassignment works only inside a project (T4). Promotion of a private task to a project is a context-menu action ("Move to project…") with a project picker.

## Fresh install — no backward compatibility

This is a fresh-build redesign. The legacy `<OMNIPUS_HOME>/tasks/` flat directory is **not preserved or migrated** — existing data there is ignored. The new task subsystem starts empty.

At first boot of an agent, the gateway creates the agent's `.omnipus/tasks/` directory empty. At project creation, the project room's `tasks/` directory is created empty. Tasks accumulate organically as agents create them.

The `TaskStore` factory function is rewritten to take a room directory; the singleton-store-at-`<OMNIPUS_HOME>/tasks/` pattern is removed entirely.

## Pros and cons

### Pros

1. Same room model as memory — one mental model for operators.
2. Project tasks model collaborative work without inventing a new concept.
3. Tasks travel with the project repo when the operator commits `<project>/.omnipus/tasks/`.
4. `TaskEntity` shape reused — no data-format rewrite, just directory layout change.
5. Per-room `TaskStore` removes single-directory contention as agent/project counts grow.

### Cons

1. **More `TaskStore` instances** — one per agent + one per project. Memory overhead is small (each store is a directory pointer + mutex), but boot wiring needs care.
2. **Cross-room queries become multi-store reads.** `task_list(scope=both)` reads two stores and merges. Slightly more code than the singleton walk.
3. **Promotion is destructive in the file layout sense** — task file moves directories, breaking absolute-path references the SPA might cache. Mitigation: SPA refers to tasks by id, not path.
4. **Project deletion ambiguity.** When a project is deleted, its tasks go too. If the operator wanted to keep them, they must promote each individually first — but demotion to private isn't supported. Documented as a deliberate trade-off; complete tasks before deleting a project if you care about them.

## Sequencing

After memory-redesign and sandbox-redesign room topology lands. Within the task work itself:

1. Rewrite `TaskStore` factory to take a per-room directory. Remove the singleton pattern.
2. Wire per-agent `TaskStore` at agent boot.
3. Wire per-project `TaskStore` at project load. Update task tools with `scope` parameter and routing.
4. SPA Command Center task board: project filter + reassignment + promotion menu.

## Locked decisions (resolving prior open questions)

| Was | Decision | Source |
|---|---|---|
| Demotion (project → private) | **Not supported.** Once shared, stays shared. To "kill" a project task without deleting the whole project, mark it complete or delete it individually | T6 |
| Project deletion + task survival | **Cascade.** Project tasks die with the project room. No "move to private" option at delete time — operator types project name in the danger-zone dialog (Q10) and accepts the loss | Q10 |
| Cross-project task dependencies | **Out of scope.** No `external_dependency` field. If two projects' work is genuinely interdependent, promote both into a single shared meta-project | T5 |
| Unassigned project tasks (`agent_id = ""`) | **Stay in the schema.** Intended for "available, to be picked up." Operator or any project member can claim by reassigning to themselves | T4 |
| Task reassignment audit | **Yes, in v1.** Every `agent_id` change on a project task appends `{event: "task.reassigned", task_id, from_agent, to_agent, by_operator, ts}` to `<project>/.omnipus/.system.jsonl` | Q15 follow-on |
| Agent removal from project — task handling | Tasks assigned to the removed agent become **unassigned** at removal time. Operator can reassign later from the task board. No forced choice at the removal dialog | Q15 |

## References

- `pkg/taskstore/store.go` — current `TaskStore` and `TaskEntity` shapes; flat-directory implementation.
- `pkg/tools/task.go` — `task_create`, `task_list`, `task_update`, `task_delete` tool implementations.
- `pkg/agent/task_executor.go` — task execution path; consumes `TaskStore`.
- `pkg/sysagent/tools/task.go` — additional task-related tools (system scope).
- `docs/internal/design/sandbox-redesign-2026-05.md` — room topology this design reuses.
- `docs/internal/design/memory-redesign-2026-05.md` — same-shape scoping rules.
- `docs/internal/design/projects-ui-2026-05.md` — Command Center task board surface.
