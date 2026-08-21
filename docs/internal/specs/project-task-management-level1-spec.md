# Feature Specification: Level 1 Project & Task Management

**Created**: 2026-06-08
**Status**: Revised post-grill-spec round 2 (2026-06-08) — 18 additional findings addressed; ready for re-grill or taskify
**Input**: Design interview (2026-06-07/08) — 7-question session establishing architecture, data model, UI layout, and scope.

---

## Background

Omnipus has two task systems that must not be conflated:

| System | Storage | Statuses | Access today |
|--------|---------|----------|--------------|
| **GTD board tasks** | `~/.omnipus/tasks/*.json` (sysagent tools) | inbox / next / active / waiting / done | Agent tool calls only — no REST, no UI |
| **Workflow tasks** | `pkg/taskstore` | queued / assigned / running / completed / failed | REST `/api/v1/tasks`, shown in current Command Center |

This spec covers **GTD board tasks and projects only**. Workflow tasks move to the Monitor screen untouched. The current Command Center is retired and replaced by a Tasks screen (GTD) + Monitor screen (ops).

---

## Available Reference Patterns

No `docs/reference/` directory exists. N/A.

---

## Existing Codebase Context

### Symbols Involved

| Symbol | File:Line | Role |
|--------|-----------|------|
| `project` struct | `pkg/sysagent/tools/project.go:17-26` | modifies — rename `agent_ids`→`core_team`, add `repository`, add `pinned`+`pin_order`, drop `color`, make `task_count` computed |
| `ProjectCreateTool.Execute` | `pkg/sysagent/tools/project.go:54-86` | modifies — update parameter schema, populate core_team + repository |
| `ProjectUpdateTool.Execute` | `pkg/sysagent/tools/project.go:113-141` | modifies — fix B2 (agent_ids ignored), add repository |
| `ProjectDeleteTool.Execute` | `pkg/sysagent/tools/project.go:165-182` | modifies — fix B3 cascade delete |
| `task` struct | `pkg/sysagent/tools/task.go:17-26` | reads — unchanged; project_id field already present |
| `TaskCreateTool.Execute` | `pkg/sysagent/tools/task.go:64-97` | extends — append link entry to `project_session_links.jsonl` after write |
| `TaskUpdateTool.Execute` | `pkg/sysagent/tools/task.go:125-161` | extends — append link entry to `project_session_links.jsonl` after write |
| `project_session_links.jsonl` | `~/.omnipus/project_session_links.jsonl` (NEW) | creates — append-only JSONL link file `{project_id, session_id, created_at}` |
| `HandleTasks` | `pkg/gateway/rest.go:2987-3043` | reads (workflow tasks) — unchanged; new handlers added alongside |
| `CommandCenterScreen` | `src/components/screens/CommandCenterScreen.tsx` | modifies — retire; replace with TasksScreen + MonitorScreen |
| `command-center.tsx` | `src/routes/_app/command-center.tsx` | modifies — route rename / replacement |
| `SchedulesList`, `ScheduleFormSheet` | `src/components/command-center/` | relocates → Monitor screen |
| `openapi.yaml` | `contracts/openapi.yaml:3381` | extends — add `/projects` and `/board/tasks` paths |
| `asyncapi.yaml` | `contracts/asyncapi.yaml` | unchanged — no new WS frames; Monitor uses polling |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|----------------|------------|----------------|----------------|
| `project` struct JSON keys | MEDIUM | `ProjectCreateTool`, `ProjectUpdateTool`, `ProjectListTool`, any stored `.json` files | File migration needed (lazy read of old `agent_ids` key) |
| `ProjectDeleteTool.Execute` | LOW | none (delete is terminal) | — |
| `loop.go` tool result handler | MEDIUM | every tool execution passes through this path | transcript writes, session store |
| `command-center.tsx` route | MEDIUM | sidebar nav link, TanStack Router config | any component importing from command-center path |
| `openapi.yaml` / `asyncapi.yaml` | HIGH | all generated types in `pkg/api/generated/` and `src/lib/api/generated/` | every consumer of generated types |

### Relevant Execution Flows

| Flow | Relevance |
|------|-----------|
| Agent tool execution → tool result → transcript write | Hook point for session auto-link (loop.go:3260-3313) |
| `system.task.create` / `system.task.update` | Trigger auto-link when project_id present in result |
| Session meta write via `UnifiedStore` | Path to persist `ProjectID` field (already in struct) |
| `HandleProjects` (new) | New REST handler mirroring `HandleTasks` pattern, reading from `~/.omnipus/projects/` |
| `HandleBoardTasks` (new) | New REST handler reading from `~/.omnipus/tasks/` (GTD) with project_id filter |
| Command Centre screen render | Replaced by TasksScreen; SchedulesList moved to MonitorScreen |

---

## Target Data Model

### `project` struct (`pkg/sysagent/tools/project.go`)

```go
type project struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    Description string   `json:"description,omitempty"`
    Status      string   `json:"status"`               // "active" | "archived"; default "active"
    Pinned      bool     `json:"pinned"`
    PinOrder    int      `json:"pin_order"`             // 0 = unset/unpinned; positive int = pin position
    CoreTeam    []string `json:"core_team,omitempty"`   // replaces deprecated "agent_ids" key
    Repository  string   `json:"repository,omitempty"`
    CreatedAt   string   `json:"created_at"`
    UpdatedAt   string   `json:"updated_at"`
    // TaskCount is computed at read time — never stored in the JSON file
}
```

**Field notes**:
- `status`: new field; legacy files that omit it are treated as `"active"` on read.
- `core_team`: reader MUST also accept `"agent_ids"` key in legacy files; writer MUST emit `"core_team"`.
- `pin_order`: uniqueness within a user's pinned set is enforced at write time (last-writer-wins; no global sequence); 0 means unordered/unpinned.
- `task_count`: never stored; computed by `listEntities[task]()` filtered by `project_id` on each read.

### `project_session_link` entry (`~/.omnipus/project_session_links.jsonl`)

```go
type projectSessionLink struct {
    ProjectID string `json:"project_id"`
    SessionID string `json:"session_id"`
    CreatedAt string `json:"created_at"` // RFC3339
}
```

Deduplication key: `(project_id, session_id)` pair — duplicates are dropped at read time; only the earliest `created_at` is kept.

Compaction: triggered automatically when the file exceeds **100,000 lines or 10 MB**. Compaction deduplicates all entries, removing links whose `project_id` no longer corresponds to an existing project file. Compaction is run in a background goroutine with a per-file mutex; no read is blocked.

---

## User Stories & Acceptance Criteria

### User Story 1 — Project REST API (Priority: P0)

A user wants to create, read, update, delete, and list projects via REST so the frontend SPA can manage projects without relying on agent tool calls. Currently, projects exist only as JSON files agents can manipulate — no human-facing API exists.

**Why this priority**: All frontend project UI (F1, F4, F6) depends on this API. Nothing else can ship without it.

**Independent Test**: `curl -X POST /api/v1/projects -d '{"name":"test"}' -H "Authorization: Bearer …"` returns `201` with a project object containing `id`, `name`, `status: "active"`, `created_at`.

**Acceptance Scenarios**:

1. **Given** a valid bearer token, **When** `POST /api/v1/projects` with `{"name":"website-api"}`, **Then** `201` with project object; project JSON written to `~/.omnipus/projects/<id>.json`.
2. **Given** an existing project, **When** `GET /api/v1/projects/<id>`, **Then** `200` with project object including computed `task_count` (count of tasks with matching `project_id`).
3. **Given** an existing project, **When** `PUT /api/v1/projects/<id>` with `{"core_team":["mia","jim"]}`, **Then** `200` and `core_team` is persisted.
4. **Given** an existing project with tasks, **When** `DELETE /api/v1/projects/<id>`, **Then** `204`; project file deleted; all tasks with matching `project_id` deleted (cascade).
5. **Given** multiple projects, **When** `GET /api/v1/projects`, **Then** `200` array sorted newest-first; each has computed `task_count`.
6. **Given** a project with `repository` set, **When** `GET /api/v1/projects/<id>`, **Then** `repository` field is present in response.
7. **Given** no name in request body, **When** `POST /api/v1/projects`, **Then** `400` with descriptive error.

---

### User Story 2 — GTD Board Task REST API (Priority: P0)

A user wants to view, create, update, and delete GTD board tasks (inbox/next/active/waiting/done) via REST. These are distinct from workflow tasks (queued/running/completed) — they are agent-managed work items linked to projects.

**Why this priority**: The Tasks screen (F2) and project task filter (F3) depend entirely on this API. Without it the frontend shows nothing.

**Independent Test**: `GET /api/v1/board/tasks?project_id=<id>` returns only tasks belonging to that project.

**Acceptance Scenarios**:

1. **Given** a valid bearer token, **When** `GET /api/v1/board/tasks`, **Then** `200` array of GTD tasks from `~/.omnipus/tasks/`, newest first.
2. **Given** tasks across multiple projects, **When** `GET /api/v1/board/tasks?project_id=<id>`, **Then** only tasks with matching `project_id` returned.
3. **Given** a valid bearer token, **When** `POST /api/v1/board/tasks` with `{"name":"Fix auth","project_id":"<id>","status":"inbox"}`, **Then** `201` with task object.
4. **Given** an existing board task, **When** `PUT /api/v1/board/tasks/<id>` with `{"status":"active"}`, **Then** `200` with updated task; status change persisted.
5. **Given** an existing board task, **When** `DELETE /api/v1/board/tasks/<id>`, **Then** `204`; task file removed.
6. **Given** `GET /api/v1/board/tasks?status=active`, **Then** only tasks with `status=active` returned.

---

### User Story 3 — Fix Backend Data Integrity (Priority: P0)

A user or agent creates/updates tasks and projects expecting the data to stay consistent. Currently: `task_count` is always 0 (B1), `core_team` updates are silently ignored (B2), and deleting a project leaves orphan tasks (B3).

**Why this priority**: These are data integrity bugs. Any UI built on top of broken data will mislead users.

**Independent Test**: Create a project, create 3 tasks linked to it, call `GET /api/v1/projects/<id>` — `task_count` equals `3`. Then delete the project — `GET /api/v1/board/tasks?project_id=<id>` returns empty array.

**Acceptance Scenarios**:

1. **Given** a project and 3 tasks with its `project_id`, **When** `GET /api/v1/projects/<id>`, **Then** `task_count` is `3` (computed, not stored).
2. **Given** a project with `core_team:[]`, **When** `PUT` with `{"core_team":["mia"]}`, **Then** `GET` returns `core_team:["mia"]` (B2 fixed).
3. **Given** a project with 2 tasks, **When** `DELETE /api/v1/projects/<id>`, **Then** both task files are deleted; `GET /api/v1/board/tasks?project_id=<id>` returns `[]` (B3 fixed).
4. **Given** a project with no tasks, **When** `GET /api/v1/projects/<id>`, **Then** `task_count` is `0`.

---

### User Story 4 — Session Auto-Link to Project (Priority: P1)

When an agent creates or updates a GTD board task with a `project_id` during a conversation, that session should automatically be recorded as having worked on that project. The link is stored in a separate many-to-many link file — `~/.omnipus/project_session_links.jsonl` — so neither the session entity nor the project entity is modified. Users do not manually create these links.

**Why this priority**: This is the only session-project link mechanism. Without it the "Linked sessions" section in task detail (F5) is always empty.

**Independent Test**: Start a session, have an agent call `system.task.create` with `project_id=X`. Then `GET /api/v1/projects/X/sessions` — the response includes this session ID. Then `GET /api/v1/projects/X` — the session list is populated.

**Acceptance Scenarios**:

1. **Given** an active session S, **When** agent calls `system.task.create` with `project_id: "P"`, **Then** a link entry `{project_id:"P", session_id:"S", created_at:…}` is appended to `project_session_links.jsonl`.
2. **Given** session S already linked to project A, **When** agent calls `system.task.update` on a task in project B, **Then** a new link entry for (B, S) is appended — (A, S) is not removed (many-to-many accumulate).
3. **Given** an active session, **When** agent calls `system.task.create` without a `project_id`, **Then** no link entry is written.
4. **Given** a project P is deleted, **When** cascade executes, **Then** all link entries with `project_id: "P"` are removed from `project_session_links.jsonl`; the sessions themselves are untouched.
5. **Given** the same (project_id, session_id) pair is triggered twice, **When** queried, **Then** only one logical link is returned (dedup at read time).

---

### User Story 5 — Projects Section in Sidebar (Priority: P1)

A user wants to see their projects listed in the sidebar so they can navigate to a specific project's task board with one click. The list must handle many projects gracefully (pinning, recency order, search, overflow collapse).

**Why this priority**: The sidebar is the primary navigation surface. Without projects here, all project UI is hidden behind a tab or screen the user must remember to visit.

**Independent Test**: Create 3 projects via the slide-over. All three appear in the sidebar Projects section, newest first.

**Acceptance Scenarios**:

1. **Given** existing projects, **When** sidebar loads, **Then** Projects section shows projects newest-first, pinned projects at top.
2. **Given** more than 5 unpinned projects, **When** sidebar renders, **Then** oldest projects are collapsed into "▸ N more…" with click-to-expand.
3. **Given** a project is right-clicked (or ··· opened), **When** user selects "Pin to top", **Then** project moves to pinned group and persists across reloads.
4. **Given** the sidebar Projects section, **When** user types "/" then a search term, **Then** project list filters inline to matching projects only.
5. **Given** an agent actively working a task in a project, **When** sidebar renders, **Then** that project shows a gold pulse indicator (◉).
6. **Given** a project in the list, **When** user clicks it, **Then** Tasks screen opens pre-filtered to that project.

---

### User Story 6 — Tasks Screen Replaces Command Center (Priority: P1)

A user wants a dedicated Tasks screen showing GTD board tasks (inbox/next/active/waiting/done), accessible from the sidebar. The current "Command Center" label is retired. Schedules and operational content move to the Monitor screen.

**Why this priority**: The entry point to all task work. Without this rename/refactor, the GTD board has no home in the nav.

**Independent Test**: Navigate to the Tasks screen — it shows GTD board tasks grouped by status. The sidebar entry reads "Tasks" not "Command Center".

**Acceptance Scenarios**:

1. **Given** the sidebar, **When** user clicks "Tasks", **Then** a screen showing GTD board tasks in status columns (Inbox / Next / Active / Waiting / Done) is displayed.
2. **Given** the Tasks screen, **When** no project is selected, **Then** all tasks across all projects are shown.
3. **Given** the Tasks screen, **When** user clicks "+ New task", **Then** a task creation form appears (name, project selector, agent selector, status).
4. **Given** existing workflow tasks in taskstore, **When** user views Tasks screen, **Then** workflow tasks (queued/running/completed) are NOT shown — those live in Monitor.

---

### User Story 7 — Project Filter on Task Board (Priority: P1)

A user wants to click a project in the sidebar and see only that project's tasks on the board, without a full page reload.

**Why this priority**: Core navigation pattern — the whole point of having projects in the sidebar.

**Independent Test**: Click project A in sidebar — task board shows only project A's tasks. Click project B — board updates to project B's tasks. Click "All" — all tasks appear.

**Acceptance Scenarios**:

1. **Given** the sidebar with a project selected, **When** user clicks that project, **Then** task board filters to show only tasks with that `project_id`.
2. **Given** a project is selected, **When** user clicks "All" (top of project list or board header), **Then** filter clears and all tasks are shown.
3. **Given** a filtered board, **When** user clicks "+ New task", **Then** the new task form pre-selects the active project.
4. **Given** a project with zero tasks, **When** selected, **Then** board shows empty-state message with "+ New task" prompt.

---

### User Story 8 — Project Header Block (Priority: P2)

When a user navigates to a project, a header block shows the project name, description, core team agents, repository link, and active task count, giving context before diving into tasks.

**Why this priority**: Contextual orientation. Useful but the task board is usable without it.

**Independent Test**: Navigate to a project — a header block appears above the task board showing name, description (if set), core team (if set), and repository link (if set).

**Acceptance Scenarios**:

1. **Given** a project with all fields set, **When** user navigates to it, **Then** header shows name, description, core team agent avatars, repository link, and task count.
2. **Given** a project with only a name, **When** user navigates to it, **Then** header shows name only; optional fields are absent (not blank placeholders).
3. **Given** a project header, **When** user clicks ✎ (edit), **Then** inline edit of name, description, core team, repository is possible.
4. **Given** a project header with a repository URL, **When** user clicks it, **Then** opens in a new tab.

---

### User Story 9 — Task Detail: Linked Sessions (Priority: P2)

A user clicking on a task wants to see which sessions have worked on its project — a read-only list of sessions that were auto-linked when any agent created or updated a task in that project, so they can navigate back to the conversation.

> **Scoping note**: The link file is keyed by `(project_id, session_id)` — there is no task-level link. Opening task T shows sessions that worked on *any* task in T's project, not sessions that modified T specifically. The UI section MUST be labeled "Sessions linked to this project" (not "Sessions that touched this task") to avoid misleading users.

**Why this priority**: Closes the "context loop" — from task back to the conversation that produced work on it.

**Independent Test**: An agent creates a task with `project_id=X` in session S. Open the task detail — session S appears in the "Linked sessions" section.

**Acceptance Scenarios**:

1. **Given** a task modified by an agent in session S, **When** user opens task detail, **Then** session S appears in "Linked sessions" with timestamp.
2. **Given** multiple sessions worked on the same project, **When** user opens task detail, **Then** all sessions linked to the task's project are shown.
3. **Given** a task with no linked sessions, **When** user opens task detail, **Then** "Linked sessions" section shows "No sessions yet."
4. **Given** a linked session in task detail, **When** user clicks it, **Then** navigates to that session in the Chat screen.

---

### User Story 10 — New Project Slide-Over (Priority: P1)

A user clicking "+ New project" in the sidebar gets a slide-over form with name (required), description (optional), core team (optional), and repository (optional), creating the project on submit.

**Why this priority**: Creation is the entry point to the entire project system.

**Independent Test**: Click "+ New project", fill in name only, click "Create project" — project appears in the sidebar immediately, newest-first.

**Acceptance Scenarios**:

1. **Given** the sidebar "+ New project" link, **When** clicked, **Then** a slide-over opens with name, description, core team, and repository fields.
2. **Given** the slide-over with only name filled, **When** "Create project" clicked, **Then** project is created; slide-over closes; project appears top of sidebar list.
3. **Given** the slide-over with all fields filled, **When** "Create project" clicked, **Then** project is created with all fields persisted.
4. **Given** the slide-over with name empty, **When** "Create project" clicked, **Then** name field shows validation error; no project created.
5. **Given** a core team field, **When** user types agent name, **Then** autocomplete shows matching agents from `GET /api/v1/agents`.
6. **Given** the slide-over open, **When** user presses Escape or clicks ✕, **Then** slide-over closes; no project created.

---

### User Story 12 — Agent Uses Project Tools Correctly (Priority: P1)

An agent receiving a natural-language instruction about projects or tasks must be able to call the right tool with the right arguments — finding the project by name, creating tasks inside it, updating task status — without requiring the user to repeat themselves or correct the agent. This tests that the tool schemas are clear enough for an LLM to use reliably.

**Why this priority**: The primary path for creating and updating tasks in Omnipus is via agent conversation, not REST API. If the tool schemas are ambiguous or the tool descriptions are wrong, agents will fail silently or produce tasks in the wrong project.

**Independent Test**: In a live chat session with Mia, type "create a task called 'write tests' in the website-api project". The task must appear on the board with `project_id` matching the website-api project ID. No follow-up correction from the user should be needed.

**Acceptance Scenarios**:

1. **Given** a project "website-api" exists, **When** user instructs an agent "create a task called 'fix login' for the website-api project", **Then** the agent calls `system.task.create` with the correct `project_id` and the task appears on the board under website-api.
2. **Given** no project name is specified, **When** user instructs "create a task called 'write docs'", **Then** the agent creates the task without a `project_id` (unassigned) rather than guessing or failing.
3. **Given** a task "fix login" in project "website-api" with status "inbox", **When** user instructs "mark the fix login task as active", **Then** the agent calls `system.task.update` with `status: "active"` and the board reflects the change.
4. **Given** multiple projects exist, **When** user instructs "show me what's in the mobile-app project", **Then** the agent calls `system.task.list` with the correct `project_id` filter and returns the task list.
5. **Given** a project "website-api" exists, **When** user instructs "create a new project called mobile-app", **Then** the agent calls `system.project.create` with `name: "mobile-app"` and the project appears in the sidebar.
6. **Given** a session where an agent creates a task with `project_id`, **When** that tool call completes, **Then** the session is auto-linked to that project (link entry written to `project_session_links.jsonl`).
7. **Given** the tool registry is loaded, **When** the descriptions for all `system.project.*` and `system.task.*` tools are read, **Then** each meets all four agent-usability criteria: when-to-call stated; chaining instruction present where needed; every parameter described with source and optionality; enum values listed with intent.

---

### User Story 11 — Monitor Screen (Priority: P2)

A user wants a Monitor screen (new SYSTEM-group sidebar item) showing: live agent activity, cost breakdown by agent, schedules management, and audit log. This moves all operational/observability content out of the Command Center (which is now Tasks-only).

**Why this priority**: Separates work management from system observability — each screen has a clear mental model.

**Independent Test**: Navigate to Monitor — schedules list is visible and matches those currently in Command Center. Cost breakdown shows per-agent token usage for the current month.

**Acceptance Scenarios**:

1. **Given** the sidebar, **When** user clicks "Monitor", **Then** Monitor screen shows four sections: Activity, Cost Breakdown, Schedules, Audit Log.
2. **Given** an agent actively running, **When** Monitor screen is open, **Then** Activity section shows that agent's name and current action (live, via WS or polling).
3. **Given** existing schedules in the system, **When** Monitor → Schedules section, **Then** all schedules are visible with enable/disable toggle; creating/editing/deleting works identically to current Command Center schedules.
4. **Given** Monitor → Cost Breakdown, **When** rendered, **Then** per-agent token counts and estimated costs for the current calendar month are shown; a [By agent ▾] and [Last 30d ▾] filter is available.
5. **Given** Monitor → Audit Log, **When** rendered, **Then** shows timestamped entries (agent, action, target) newest-first with "Load more" pagination.
6. **Given** the current Command Center with Schedules, **When** this feature ships, **Then** all existing schedule data remains intact; only the UI surface changes.

---

## Behavioral Contract

**Primary flows:**
- When a project is created via REST, it is persisted to `~/.omnipus/projects/<id>.json` and immediately visible in `GET /api/v1/projects`.
- When a project is listed, `task_count` is computed live from tasks on disk (not a stored field).
- When a project is deleted, all GTD tasks with that `project_id` are also deleted before the project file is removed.
- When an agent executes `system.task.create` or `system.task.update` with a non-empty `project_id` in a session, a link entry `{project_id, session_id, created_at}` is appended to `project_session_links.jsonl`. `SessionMeta` is NOT modified.
- When a user clicks a project in the sidebar, the task board filters to show only that project's GTD tasks.
- When a user clicks "+ New project", a slide-over with name/description/core_team/repository fields appears.

**Error flows:**
- When `POST /api/v1/projects` is called without a name, the API returns `400`.
- When `GET /api/v1/projects/<unknown-id>` is called, the API returns `404`.
- When `DELETE /api/v1/projects/<id>` fails to delete a task file during cascade, the error is logged but the project deletion continues (best-effort cascade).
- When the Monitor cost endpoint has no data, it shows zeroed totals rather than an error state.

**Boundary conditions:**
- When a session has been linked to multiple projects (worked across projects), all `project_id` values are accumulated (many-to-many).
- When the sidebar project list exceeds 5 unpinned items, oldest items collapse into "▸ N more…".
- When a project has no tasks, `task_count` is `0` and the task board shows an empty state.

---

## Edge Cases

- Project name collision: two projects with the same name are allowed (IDs differ). No uniqueness constraint.
- Deleting a project that an agent is actively working on: cascade proceeds; in-flight tool calls referencing that `project_id` may produce 404 on subsequent reads — agents must handle gracefully.
- Session linked to a deleted project: link entries in `project_session_links.jsonl` are removed during cascade (see FR-007). Sessions themselves are unaffected. `GET /api/v1/sessions` never returns a `project_id` field — session records have no project reference.
- Renaming `agent_ids` → `core_team` in JSON: existing files on disk use `"agent_ids"`. The Go struct reader must check both keys (lazy migration: read either, write `core_team`).
- Repository URL field: stored as-is, not validated for reachability. Frontend opens it in a new tab; no backend fetch.
- `core_team` agent IDs that no longer exist: stored IDs may reference deleted agents. Display gracefully (show ID, not crash).
- Very long project name (>200 chars): stored as-is; sidebar truncates with ellipsis.
- Concurrent project creation: two requests creating the same-named project simultaneously produce two distinct projects (ID is a UUID generated per request).

---

## Explicit Non-Behaviors

- The system must not use `project_id` as an access control gate — any agent can work on any project's tasks because core_team is a default roster only, not a permission system.
- The system must not require users to manually link sessions to projects — auto-link on tool result is the only mechanism.
- The system must not add file-system directories or room structures per project — that is Level 2 (v0.3 Rooms). A project is a metadata record only.
- The system must not add color customization to projects — project identity is name-only; the design system handles visual states.
- The system must not move existing workflow tasks (`pkg/taskstore`) to the GTD board system — they are separate concerns with different semantics and remain at `/api/v1/tasks`.
- The system must not remove the Schedules functionality — only relocate it from Command Center to Monitor.
- The system must not show GTD board tasks in the Monitor screen — Monitor is for operational (workflow) tasks and system observability only.
- The system must not add Level 2 memory scoping, git checkout, or per-project filesystem roots — those are v0.3 scope.

---

## Integration Boundaries

### GTD Task Storage (`~/.omnipus/tasks/*.json`)
- **Data in**: `task` struct (name, description, status, project_id, agent_id, created_at, updated_at)
- **Data out**: Same struct as JSON
- **Contract**: File per task, ID = UUID filename, read via `listEntities[task]()` pattern in sysagent tools
- **On failure**: File read error → log + skip that task in list response (no 500)
- **Development**: Real filesystem (no mock needed; test with temp dir)

### Project Storage (`~/.omnipus/projects/*.json`)
- **Data in**: `project` struct (id, name, description, core_team, repository, status, created_at, updated_at)
- **Data out**: Same struct + computed `task_count`
- **Contract**: Mirrors GTD task storage pattern exactly
- **On failure**: Same as above — file errors logged, item skipped
- **Development**: Real filesystem with temp dir in tests

### Project-Session Link File (`~/.omnipus/project_session_links.jsonl`)
- **Data in**: `{project_id: string, session_id: string, created_at: RFC3339}` — one JSON object per line
- **Data out**: Lines filtered by `project_id` or `session_id`, deduplicated by `(project_id, session_id)` pair at read time
- **Contract**: Append-only JSONL; file created with mode `0600` (not `0644` — session IDs are sensitive); `fileutil.WriteFileAtomic` not needed for append (use `os.OpenFile` with `O_APPEND|O_CREATE|O_RDWR, 0600`); a single named process-level mutex (`linkFileMu sync.Mutex`) MUST serialise ALL writes — both the linker-hook append path and the cascade-delete rewrite path and compaction; reads do not require the mutex (JSONL append is atomic at the OS level for writes < 4 KB on Linux); compaction triggered when file exceeds 100,000 lines or 10 MB (background goroutine, no read blocked)
- **On failure**: Log WARN; do not abort the tool result — auto-link is best-effort; partial writes are skipped at read time
- **Development**: Temp dir in tests; file absent = empty link set (no error)

### OpenAPI Contract (`contracts/openapi.yaml`)
- **Data in**: New `/projects` and `/board/tasks` path definitions + schemas
- **Data out**: Generated Go and TypeScript types in `pkg/api/generated/` and `src/lib/api/generated/`
- **Contract**: Constraint #8 — schema-first, codegen via `scripts/gen-contracts.sh`
- **On failure**: `make verify-contracts` fails CI — must regenerate before push
- **Development**: Run `make gen-contracts` after schema changes

---

## BDD Scenarios

### Feature: Project REST API

#### Background
- **Given** the Omnipus gateway is running with a valid master key
- **And** the caller has a valid bearer token

#### Scenario: Create a project with name only
**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** no project named "alpha" exists
- **When** `POST /api/v1/projects` is called with `{"name":"alpha"}`
- **Then** the response status is `201`
- **And** the response body contains `id`, `name: "alpha"`, `status: "active"`, `task_count: 0`, `created_at`
- **And** a file `~/.omnipus/projects/<returned-id>.json` exists on disk

#### Scenario: Create a project with all optional fields
**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** no projects exist
- **When** `POST /api/v1/projects` with `{"name":"api","description":"Main API","core_team":["mia"],"repository":"https://github.com/org/repo"}`
- **Then** `201` with all fields present in response
- **And** `core_team: ["mia"]` and `repository` persisted in the project file

#### Scenario: task_count reflects actual task count
**Traces to**: User Story 1, Acceptance Scenario 2; User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** project P exists with no tasks
- **And** three tasks are created with `project_id = P`
- **When** `GET /api/v1/projects/P`
- **Then** `task_count: 3` in the response
- **But** the project JSON file on disk does NOT contain a `task_count` field (it is computed)

#### Scenario: Retrieve project returns 404 for unknown ID
**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Error Path

- **Given** no project with ID "does-not-exist"
- **When** `GET /api/v1/projects/does-not-exist`
- **Then** response status is `404`

#### Scenario: Update core_team on existing project
**Traces to**: User Story 1, Acceptance Scenario 3; User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** project P exists with `core_team: []`
- **When** `PUT /api/v1/projects/P` with `{"core_team":["mia","jim"]}`
- **Then** `200` response with the full updated project object (same shape as the GET response, including `core_team: ["mia","jim"]`, `name`, `status`, `task_count`)
- **And** subsequent `GET /api/v1/projects/P` also returns `core_team: ["mia","jim"]`

#### Scenario: Delete project cascades to tasks
**Traces to**: User Story 1, Acceptance Scenario 4; User Story 3, Acceptance Scenario 3
**Category**: Happy Path

- **Given** project P with 2 tasks (T1, T2) having `project_id = P`
- **When** `DELETE /api/v1/projects/P`
- **Then** response status is `204`
- **And** `GET /api/v1/projects/P` returns `404`
- **And** `GET /api/v1/board/tasks/T1` returns `404`
- **And** `GET /api/v1/board/tasks/T2` returns `404`

#### Scenario: Project list sorted newest-first
**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Happy Path

- **Given** three projects created in order: A (oldest), B, C (newest)
- **When** `GET /api/v1/projects`
- **Then** response array order is [C, B, A]
- **And** each item includes computed `task_count`

#### Scenario: Archive project removes it from default list
**Traces to**: User Story 1, Acceptance Scenario 5; FR-028
**Category**: Happy Path

- **Given** project P exists and appears in `GET /api/v1/projects`
- **When** `PUT /api/v1/projects/P` with `{"status":"archived"}`
- **Then** `200` response
- **And** `GET /api/v1/projects` (default) does NOT include project P
- **And** `GET /api/v1/projects?status=archived` includes project P
- **And** `GET /api/v1/projects/P` still returns `200` with `status: "archived"`

#### Scenario: Linked sessions returned for project
**Traces to**: User Story 4, Acceptance Scenario 1; FR-027
**Category**: Happy Path

- **Given** project P has sessions S1 and S2 linked via `project_session_links.jsonl`
- **When** `GET /api/v1/projects/P/sessions`
- **Then** `200` with array `[{session_id:"S1", created_at:…}, {session_id:"S2", created_at:…}]`

#### Scenario: Linked sessions endpoint 404 for unknown project
**Traces to**: FR-027
**Category**: Error Path

- **Given** no project with ID "nonexistent"
- **When** `GET /api/v1/projects/nonexistent/sessions`
- **Then** `404`

#### Scenario Outline: Invalid project status values rejected
**Traces to**: FR-029; UQ-2
**Category**: Error Path

- **Given** project P exists
- **When** `PUT /api/v1/projects/P` with `{"status":"<bad_status>"}`
- **Then** response status is `<expected>`

**Examples**:

| bad_status | expected | note |
|------------|----------|------|
| `deleted` | 400 | unknown value |
| `` (empty) | 400 | empty string |
| `ACTIVE` | 400 | wrong case |
| `active` | 200 | valid — no change expected |
| `archived` | 200 | valid archive transition |

#### Scenario: Legacy project file with agent_ids key loads correctly
**Traces to**: User Story 3; FR-010
**Category**: Edge Case

- **Given** a project JSON file on disk contains `"agent_ids": ["mia"]` (old format, no `status` field)
- **When** `GET /api/v1/projects/<id>`
- **Then** `200` with `core_team: ["mia"]` and `status: "active"`
- **And** no error is returned

#### Scenario Outline: Invalid project create requests
**Traces to**: User Story 1, Acceptance Scenario 7
**Category**: Error Path

- **Given** the gateway is running
- **When** `POST /api/v1/projects` with body `<body>`
- **Then** response status is `<status>`

**Examples**:

| body | status | note |
|------|--------|------|
| `{}` | 400 | missing name |
| `{"name":""}` | 400 | empty name |
| `{"name":null}` | 400 | null name |

---

### Feature: GTD Board Task REST API

#### Background
- **Given** the Omnipus gateway is running with a valid bearer token

#### Scenario: List board tasks returns GTD tasks only
**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** two GTD tasks in `~/.omnipus/tasks/` and two workflow tasks in `pkg/taskstore`
- **When** `GET /api/v1/board/tasks`
- **Then** only the two GTD tasks are returned
- **And** workflow tasks do not appear in the response

#### Scenario: Filter board tasks by project
**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** task T1 with `project_id = P` and task T2 with `project_id = Q`
- **When** `GET /api/v1/board/tasks?project_id=P`
- **Then** only T1 is returned

#### Scenario: Create a board task with project
**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Happy Path

- **Given** project P exists
- **When** `POST /api/v1/board/tasks` with `{"name":"Fix auth","project_id":"P","status":"inbox"}`
- **Then** `201` with task object containing `project_id: "P"` and `status: "inbox"`

#### Scenario Outline: Update board task status
**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Happy Path

- **Given** board task T with `status: "inbox"`
- **When** `PUT /api/v1/board/tasks/T` with `{"status":"<new_status>"}`
- **Then** response is `200` with the full updated task object (same shape as the create response, including updated `status: "<new_status>"`, `name`, `project_id`, `created_at`, `updated_at`)
- **And** `GET /api/v1/board/tasks/T` also returns `status: "<new_status>"`

**Examples**:

| new_status |
|------------|
| next       |
| active     |
| waiting    |
| done       |

#### Scenario: Delete board task
**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Happy Path

- **Given** board task T exists
- **When** `DELETE /api/v1/board/tasks/T`
- **Then** `204`; `GET /api/v1/board/tasks/T` returns `404`

---

### Feature: Session Auto-Link

#### Scenario: Agent task.create with project_id writes link entry
**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** session S is active
- **And** `project_session_links.jsonl` contains no entry for (P, S)
- **When** an agent executes `system.task.create` with `project_id: "P"` during session S
- **Then** a line `{"project_id":"P","session_id":"S","created_at":"…"}` is appended to `project_session_links.jsonl`
- **And** `GET /api/v1/projects/P/sessions` returns session S in the list

#### Scenario: Auto-link is best-effort — does not block tool result
**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Error Path

- **Given** session S is active
- **And** the link file write fails (e.g. disk full)
- **When** an agent executes `system.task.create` with `project_id: "P"`
- **Then** the tool result is returned to the agent normally
- **And** a WARN is logged; no error propagated to the agent

#### Scenario: task.create without project_id writes no link entry
**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** session S is active
- **When** agent executes `system.task.create` without `project_id`
- **Then** no new line is appended to `project_session_links.jsonl`

#### Scenario: Project delete removes link entries but not sessions
**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Happy Path

- **Given** project P has link entries for sessions S1 and S2 in `project_session_links.jsonl`
- **When** `DELETE /api/v1/projects/P`
- **Then** all lines with `project_id: "P"` are removed from the link file
- **And** sessions S1 and S2 still exist in the session store
- **But** `GET /api/v1/projects/P/sessions` returns `404` (project gone)

---

### Feature: Sidebar Projects Section

#### Scenario: Projects listed newest-first with pinned at top
**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** projects A (pinned), B (pinned), C (newest, unpinned), D (older, unpinned)
- **When** the sidebar renders
- **Then** order is: A, B, C, D (pinned first, then newest-first among unpinned)

#### Scenario: More than 5 unpinned projects collapses overflow — 5 newest shown
**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Edge Case

- **Given** 7 unpinned projects P1 (newest, created_at=T7) through P7 (oldest, created_at=T1)
- **When** sidebar renders
- **Then** P1, P2, P3, P4, P5 are visible in order (newest first)
- **And** P6 and P7 are collapsed into "▸ 2 more…"
- **And** clicking "▸ 2 more…" expands the full list showing all 7

#### Scenario: Inline search filters project list (case-insensitive substring)
**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Happy Path

- **Given** projects "website-api", "mobile-app", "infra-2026" in sidebar
- **And** the sidebar Projects section has keyboard focus
- **When** user types "/" then "mob"
- **Then** only "mobile-app" is shown in the list
- **And** typing "MOB" would also match "mobile-app" (case-insensitive)
- **And** results update on every keystroke with no debounce delay

#### Scenario: Clicking project navigates to filtered task board
**Traces to**: User Story 5, Acceptance Scenario 6; User Story 7, Acceptance Scenario 1
**Category**: Happy Path

- **Given** project P in sidebar
- **When** user clicks P
- **Then** the Tasks screen opens (or updates) showing only tasks with `project_id = P`
- **And** the project header block for P is shown above the task board

---

### Feature: New Project Slide-Over

#### Scenario: Create project with name only
**Traces to**: User Story 10, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the sidebar is visible
- **When** user clicks "+ New project"
- **And** types "backend-api" in the Name field
- **And** clicks "Create project"
- **Then** the slide-over closes
- **And** "backend-api" appears at the top of the sidebar project list

#### Scenario: Empty name blocks submission
**Traces to**: User Story 10, Acceptance Scenario 4
**Category**: Error Path

- **Given** the new project slide-over is open
- **When** user clicks "Create project" with Name field empty
- **Then** the Name field shows a validation error "Name is required"
- **And** no API call is made

#### Scenario: Escape closes slide-over without creating project
**Traces to**: User Story 10, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** the new project slide-over is open with "alpha" typed in Name
- **When** user presses Escape
- **Then** the slide-over closes
- **And** `GET /api/v1/projects` does not include a project named "alpha"

#### Scenario: Core team autocomplete degrades gracefully when agent list fails
**Traces to**: User Story 10, Acceptance Scenario 5; MIN-003
**Category**: Error Path

- **Given** the new project slide-over is open
- **And** `GET /api/v1/agents` returns an error (network failure or 5xx)
- **When** user clicks the Core Team field
- **Then** the autocomplete shows a "Could not load agents" fallback message
- **And** the user can still type a raw agent ID and add it manually
- **And** the rest of the form remains functional

---

### Feature: Task Detail — Linked Sessions

#### Scenario: Task detail shows linked sessions for project
**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a task T with `project_id = P`
- **And** sessions S1 and S2 are linked to P in `project_session_links.jsonl`
- **When** user opens the task detail for T
- **Then** the "Linked sessions" section shows S1 and S2 with timestamps
- **And** clicking either session navigates to that session in the Chat screen

#### Scenario: Task detail shows empty state when no sessions linked
**Traces to**: User Story 9, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** a task T with `project_id = P`
- **And** no sessions are linked to P
- **When** user opens the task detail for T
- **Then** the "Linked sessions" section displays "No sessions yet."

---

### Feature: Agent Tool Usage — Project & Task Tools

#### Background
- **Given** the Omnipus gateway is running with a configured LLM provider
- **And** at least one agent (e.g. Mia) is active

#### Scenario: Agent creates task in named project via natural language
**Traces to**: User Story 12, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a project "website-api" exists with a known ID
- **And** a chat session is open with Mia
- **When** the user sends "create a task called 'fix login' for the website-api project"
- **Then** the agent calls `system.task.create` with `project_id` equal to website-api's ID
- **And** `GET /api/v1/board/tasks?project_id=<website-api-id>` returns a task named "fix login"
- **And** the session is linked to website-api in `project_session_links.jsonl`

#### Scenario: Agent creates task without project — no project_id assumed
**Traces to**: User Story 12, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** projects exist in the system
- **And** a chat session is open
- **When** the user sends "create a task called 'write docs'"
- **Then** the agent calls `system.task.create` without a `project_id`
- **And** `GET /api/v1/board/tasks` returns a task named "write docs" with no `project_id`

#### Scenario: Agent updates task status via natural language
**Traces to**: User Story 12, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a task "fix login" with `status: "inbox"` exists in project "website-api"
- **When** the user sends "mark the fix login task as active"
- **Then** the agent calls `system.task.update` with `status: "active"`
- **And** `GET /api/v1/board/tasks/<task-id>` returns `status: "active"`

#### Scenario: Agent lists tasks for a specific project
**Traces to**: User Story 12, Acceptance Scenario 4
**Category**: Happy Path

- **Given** project "mobile-app" has 3 tasks; project "website-api" has 2 tasks
- **When** the user sends "show me what's in the mobile-app project"
- **Then** the agent calls `system.task.list` with `project_id` equal to mobile-app's ID
- **And** the agent response lists only the 3 mobile-app tasks
- **But** the 2 website-api tasks do not appear in the agent's response

#### Scenario: Agent creates a new project
**Traces to**: User Story 12, Acceptance Scenario 5
**Category**: Happy Path

- **Given** no project named "mobile-app" exists
- **When** the user sends "create a new project called mobile-app"
- **Then** the agent calls `system.project.create` with `name: "mobile-app"`
- **And** `GET /api/v1/projects` returns a project with `name: "mobile-app"`
- **And** the project appears in the sidebar on next render

#### Scenario: Project and task tool descriptions meet agent-usability criteria
**Traces to**: User Story 12, Acceptance Scenario 1 (pre-condition)
**Category**: Edge Case

- **Given** the tool registry is loaded
- **When** the descriptions for `system.project.create`, `system.project.update`, `system.project.list`, `system.task.create`, `system.task.update`, `system.task.list` are inspected
- **Then** each tool-level description is at least one full sentence stating when the agent should call it
- **And** `system.task.create` and `system.task.update` descriptions name `system.project.list` as the source of `project_id` when a project name is mentioned
- **And** every parameter description is more than three words and does not use the phrase "the ID" or "the value" without further context
- **And** the `status` parameter description on `system.task.create` and `system.task.update` lists all valid values: inbox, next, active, waiting, done — with a one-line explanation of when to use each

---

### Feature: Monitor Screen

#### Scenario: Monitor screen has four sections
**Traces to**: User Story 11, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the user is authenticated
- **When** user clicks "Monitor" in the sidebar
- **Then** the Monitor screen renders with sections: Activity, Cost Breakdown, Schedules, Audit Log

#### Scenario: Existing schedules are visible in Monitor
**Traces to**: User Story 11, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a schedule "Daily standup" exists in the system
- **When** user navigates to Monitor → Schedules section
- **Then** "Daily standup" is listed with its cron expression, agent, and enable/disable toggle

#### Scenario: Cost breakdown shows per-agent token usage
**Traces to**: User Story 11, Acceptance Scenario 4
**Category**: Happy Path

- **Given** sessions exist for agents Mia and Jim with recorded token counts
- **When** user views Monitor → Cost Breakdown
- **Then** Mia and Jim each have a bar with token count and estimated cost

#### Scenario: Schedules are preserved after Command Center is removed
**Traces to**: User Story 11, Acceptance Scenario 6
**Category**: Edge Case

- **Given** schedules created before this feature shipped (in old Command Center)
- **When** user opens Monitor after the upgrade
- **Then** all existing schedules are present and functional

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------|-------|---------|
| Unit | Individual functions in `pkg/sysagent/tools/`, `pkg/gateway/` | Validates logic in isolation with temp dirs |
| Integration (ScenarioProvider) | Full agent loop with pre-scripted LLM responses via `testutil.ScenarioProvider` | Validates hook wiring, handler logic, and tool execution deterministically — no LLM call made |
| Integration (live LLM) | Full agent loop with real LLM provider available in CI | Validates agent usability: real model + real tool descriptions → correct tool call; these tests exist specifically because LLM is available in CI |
| E2E | Playwright: sidebar navigation, task board, project creation | Validates complete UI flows from user perspective |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestProjectCreate_WritesFile` | Unit | Create project with name only | `ProjectCreateTool.Execute` writes valid JSON to temp dir |
| 2 | `TestProjectCreate_TaskCountZeroOnCreate` | Unit | Create project with name only | `task_count` not in written file |
| 3 | `TestProjectUpdate_CoreTeamPersisted` | Unit | Update core_team on existing project | B2 fix: `core_team` value written after update |
| 4 | `TestProjectUpdate_ReadsLegacyAgentIDs` | Unit | (migration) | Reads old `agent_ids` JSON key, writes `core_team` |
| 5 | `TestProjectDelete_CascadesTasks` | Unit | Delete project cascades to tasks | Deletes all task files with matching `project_id` |
| 6 | `TestProjectList_TaskCountComputed` | Unit | task_count reflects actual count | Count from task files, not from project file |
| 7 | `TestBoardTaskCreate_WritesFile` | Unit | Create a board task with project | Task file written to `~/.omnipus/tasks/` |
| 8 | `TestBoardTaskList_FilterByProject` | Unit | Filter board tasks by project | Filter returns only tasks with matching `project_id` |
| 9 | `TestBoardTaskList_ExcludesWorkflowTasks` | Unit | List board tasks returns GTD tasks only | No cross-contamination with taskstore |
| 10 | `TestSessionAutoLink_LinkFileWritten` | Unit | Agent task.create writes link entry | `TaskCreateTool.Execute` returns result containing project_id; verify the AfterTool hook writes to link JSONL (use temp file) |
| 11 | `TestSessionAutoLink_NoProjectID_NoLinkWritten` | Unit | task.create without project_id writes no link | AfterTool hook does not append when project_id absent from result |
| 12 | `TestSessionAutoLink_BestEffort_WriteFailure` | Unit | Auto-link is best-effort | Link file write failure → WARN logged, tool result returned normally |
| 13 | `TestHandleProjects_CRUD` | Integration | Project CRUD scenarios | Full HTTP handler: create, get, update, delete, list |
| 14 | `TestHandleProjects_Delete_Cascades` | Integration | Delete project cascades to tasks | HTTP DELETE → task files removed |
| 15 | `TestHandleBoardTasks_CRUD` | Integration | Board task CRUD scenarios | Full HTTP handler for GTD tasks |
| 16 | `TestHandleBoardTasks_FilterByStatus` | Integration | Filter by status | `?status=active` returns only active tasks |
| 17 | `TestProjectRoutes_RequireAuth` | Integration | (security) | All `/api/v1/projects` routes return `401` without token |
| 18 | `TestBoardTaskRoutes_RequireAuth` | Integration | (security) | All `/api/v1/board/tasks` routes return `401` without token |
| 19 | `sidebar-projects.test.tsx` — renders newest-first | Unit (Vitest) | Projects listed newest-first | React component renders projects in correct order |
| 20 | `sidebar-projects.test.tsx` — overflow collapse | Unit (Vitest) | More than 5 projects collapses | "▸ N more…" shown; click expands |
| 21 | `sidebar-projects.test.tsx` — inline search | Unit (Vitest) | Inline search filters | "/" activates search, filters project list |
| 22 | `new-project-slide-over.test.tsx` — happy path | Unit (Vitest) | Create project with name only | Form submits, slide-over closes, project appears |
| 23 | `new-project-slide-over.test.tsx` — empty name | Unit (Vitest) | Empty name blocks submission | Validation error shown, no API call |
| 24 | `new-project-slide-over.test.tsx` — escape closes | Unit (Vitest) | Escape closes slide-over | No project created on Escape |
| 25 | `tasks-screen.test.tsx` — shows GTD tasks | Unit (Vitest) | Tasks screen shows GTD tasks | Calls `/api/v1/board/tasks`, renders status columns |
| 26 | `tasks-screen.test.tsx` — filters on project select | Unit (Vitest) | Filter task board by project | Selecting project refetches with `?project_id=` |
| 27 | `monitor-screen.test.tsx` — four sections render | Unit (Vitest) | Monitor screen has four sections | Activity, Cost, Schedules, Audit Log all render |
| 28 | `monitor-screen.test.tsx` — schedules functional | Unit (Vitest) | Existing schedules visible | SchedulesList renders in Monitor context |
| 29 | E2E: create project + task + verify task count | E2E (Playwright) | task_count reflects actual count | Full flow: create project, create 2 tasks, verify count=2 in header |
| 30 | E2E: sidebar project navigation | E2E (Playwright) | Clicking project navigates to filtered board | Click project → task board shows only project tasks |
| 31 | E2E: new project slide-over | E2E (Playwright) | Create project with name only | End-to-end create via slide-over; appears in sidebar |
| 32 | E2E: Monitor schedules preserved | E2E (Playwright) | Schedules preserved after Command Center removed | Existing schedules visible and editable in Monitor |
| 33 | `TestProjectToolDescriptions_AgentUsability` | Unit | Project and task tool descriptions meet agent-usability criteria | Asserts: tool description ≥ 1 sentence; task tools mention `system.project.list` for project_id chaining; every param description > 3 words and not just "the ID"/"the value"; status param lists all 5 valid values |
| 34 | `TestAgentLoop_TaskCreate_WithProjectID_LinksSession` | Integration (ScenarioProvider) | Agent creates task in named project | `testutil.NewScenario().WithToolCall("system.task.create", `{"name":"…","project_id":"P"}`)` — pre-scripts the tool call; full AfterTool hook path fires; verifies link JSONL written. Tests hook wiring, not LLM reasoning. |
| 35 | `TestAgentLoop_TaskUpdate_WithProjectID_LinksSession` | Integration | Agent updates task status | ScenarioProvider with `.WithToolCall("system.task.update", `{"id":"T","status":"active","project_id":"P"}`)`. Verify link accumulates (not replaced) when same session updates twice. |
| 36 | `TestAgentLoop_TaskCreate_NoProjectID_NoLink` | Integration | Agent creates task without project | ScenarioProvider with `.WithToolCall("system.task.create", `{"name":"…"}`)` (no project_id). Verify no line appended to link file. |
| 37 | `TestProjectStatus_InvalidValueRejected` | Unit | Invalid project status rejected with 400 | PUT with "deleted", "", "ACTIVE" → 400; PUT with "active", "archived" → 200 |
| 38 | `TestCoreTeam_DeduplicatesEntries` | Unit | core_team dedup | `["mia","mia","jim"]` stored as `["mia","jim"]` |
| 40 | `TestSessionAutoLink_LinkFileAbsent_CreatesFile` | Unit | Link file absent on first write | Linker hook creates `project_session_links.jsonl` when not present (O_CREATE) |
| 41 | `TestProjectDelete_ConcurrentAppend_SafeWithMutex` | Integration | Concurrent cascade + linker append | Race detector enabled; DELETE rewrites while goroutine appends; no data loss |
| 42 | `TestPinOrder_Tiebreaker_SortsByCreatedAt` | Unit | pin_order tiebreaker | Two projects with same `pin_order` → sorted by `created_at` ascending |
| 43 | `TestBoardTask_DefaultStatus_Inbox` | Unit | Board task default status | POST without `status` field → 201 with `status: "inbox"` |
| 44 | `TestMonitor_AuditLog_NonAdmin_ShowsFallback` | Unit (Vitest) | Monitor Audit Log non-admin path | When `user.admin=false`, Audit Log section shows "Audit log requires admin access" |
| 45 | `TestAgentLoop_NL_CreateTask_InNamedProject` | Integration (live LLM) | Agent creates task in named project via NL | Real LLM in CI. Send "create a task called 'fix login' in the website-api project". Assert agent calls `system.task.create` with correct `project_id`; task appears in `GET /api/v1/board/tasks?project_id=…`; session linked in JSONL. |
| 46 | `TestAgentLoop_NL_UpdateTaskStatus` | Integration (live LLM) | Agent updates task status via NL | Real LLM in CI. Send "mark the fix login task as active". Assert agent calls `system.task.update` with `status:"active"`; board reflects change. |
| 47 | `TestAgentLoop_NL_ListProjectTasks` | Integration (live LLM) | Agent lists tasks for specific project | Real LLM in CI. Send "show me tasks in the mobile-app project". Assert agent calls `system.task.list` with correct `project_id`; response contains only that project's tasks. |
| 48 | `TestAgentLoop_NL_CreateProject` | Integration (live LLM) | Agent creates new project | Real LLM in CI. Send "create a new project called mobile-app". Assert agent calls `system.project.create`; project appears in `GET /api/v1/projects`. |

### Test Datasets

#### Dataset: Project Name Validation

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `{"name":"a"}` | min valid | 201 | Create with name only | Single char accepted |
| 2 | `{"name":""}` | empty | 400 | Invalid project create | Empty string rejected |
| 3 | `{}` | missing field | 400 | Invalid project create | Name absent rejected |
| 4 | `{"name": "x".repeat(200)}` | max | 201 | Create with name only | Stored as-is, displayed truncated |
| 5 | `{"name":"café au lait"}` | unicode | 201 | Create with name only | Unicode names accepted |
| 6 | `{"name":"test","core_team":[]}` | empty array | 201 | Create with all optional fields | Empty array is valid |
| 7 | `{"name":"test","repository":"not-a-url"}` | invalid URL | 201 | (stored as-is) | No URL validation at backend |

#### Dataset: Board Task Status

| # | Input | Boundary Type | Expected | Traces to | Notes |
|---|-------|---------------|----------|-----------|-------|
| 1 | `{"name":"t","status":"inbox"}` | valid | 201 with `status:"inbox"` | Create board task | Explicit inbox |
| 2 | `{"name":"t"}` (no status) | missing | 201 with `status:"inbox"` | Create board task | Defaults to inbox |
| 3 | `{"status":"next"}` | valid update | 200 | Update task status | Accepted |
| 4 | `{"status":"active"}` | valid update | 200 | Update task status | Accepted |
| 5 | `{"status":"waiting"}` | valid update | 200 | Update task status | Accepted |
| 6 | `{"status":"done"}` | valid update | 200 | Update task status | Accepted |
| 7 | `{"status":"queued"}` | invalid (workflow status) | 400 | Update task status | Workflow status rejected |
| 8 | `{"status":""}` | empty | 400 | Update task status | Empty rejected |
| 9 | `{"status":"INBOX"}` | wrong case | 400 | Update task status | Case-sensitive |

#### Dataset: Project Status Validation

| # | Input | Boundary Type | Expected | Traces to | Notes |
|---|-------|---------------|----------|-----------|-------|
| 1 | `{"status":"active"}` | valid | 200 | Invalid project status rejected | No change |
| 2 | `{"status":"archived"}` | valid | 200 | Archive project removes from default list | Archived |
| 3 | `{"status":"deleted"}` | invalid | 400 | Invalid project status rejected | Unknown value |
| 4 | `{"status":""}` | empty | 400 | Invalid project status rejected | Empty rejected |
| 5 | `{"status":"ACTIVE"}` | wrong case | 400 | Invalid project status rejected | Case-sensitive |
| 6 | (no status field) | omitted | 200 | (partial update — other field) | Status unchanged |

#### Dataset: core_team Validation

| # | Input | Boundary Type | Expected | Traces to | Notes |
|---|-------|---------------|----------|-----------|-------|
| 1 | `{"core_team":["mia"]}` | valid | 201/200 | Update core_team | Single entry |
| 2 | `{"core_team":["mia","mia","jim"]}` | duplicates | 201 with `["mia","jim"]` | Update core_team | Dedup at write |
| 5 | `{"core_team":[]}` | empty array | 201/200 | Create project with all optional fields | Empty is valid |
| 6 | `{"core_team":["deleted-agent-id"]}` | stale ID | 201/200 | Update core_team | No write-time validation |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|--------------------|---------------|---------------------------|-------|
| Schedules create/edit/delete | `SchedulesList.test.tsx`, `ScheduleFormSheet.test.tsx` | No — existing tests must pass with new MonitorScreen wrapper | Schedules component relocated, not rewritten |
| Workflow tasks at `/api/v1/tasks` | `TaskList.test.tsx` | `TestWorkflowTasksUnchanged` — confirm `/api/v1/tasks` still returns workflow tasks | Ensure no cross-contamination with board tasks |
| Command Center route renders | `command-center.tsx` route | Route renamed; existing route test updated (not deleted) | The old URL should 404 or redirect |
| `system.project.*` agent tools | (none currently) | Tests 1-6 above cover the fixed tool behaviour | New regression coverage for fixed bugs |

---

## Functional Requirements

- **FR-001**: System MUST expose `GET /api/v1/projects`, `POST /api/v1/projects`, `GET /api/v1/projects/{id}`, `PUT /api/v1/projects/{id}`, `DELETE /api/v1/projects/{id}` as authenticated REST endpoints. All new authenticated endpoints (projects, board tasks, stats/tokens) MUST be covered by the existing per-IP authenticated rate limiter. `PUT /api/v1/projects/{id}` uses merge (partial-update) semantics: only fields present in the JSON request body are updated; fields absent from the body are left unchanged (matching the existing config-update merge pattern in the codebase).
- **FR-002**: System MUST expose `GET /api/v1/board/tasks`, `POST /api/v1/board/tasks`, `GET /api/v1/board/tasks/{id}`, `PUT /api/v1/board/tasks/{id}`, `DELETE /api/v1/board/tasks/{id}` as authenticated REST endpoints serving GTD board tasks. All new authenticated endpoints (board tasks, projects, stats/tokens) MUST be covered by the existing per-IP authenticated rate limiter. All mutating REST operations on projects and board tasks (`POST`, `PUT`, `DELETE`) MUST emit an audit log entry via `pkg/audit`.
- **FR-003**: System MUST define `Project` and `BoardTask` schemas in `contracts/openapi.yaml` before implementing Go or TypeScript code (Constraint #8).
- **FR-004**: System MUST compute `task_count` at read time by counting task files with matching `project_id`; it MUST NOT be stored as a field. When computing `task_count` for a list response (`GET /api/v1/projects`), the handler MUST call `listEntities[task]()` exactly once and build a `map[string]int` count (keyed by `project_id`) before attaching counts to each project — not once per project (O(N×M) is prohibited).
- **FR-005**: System MUST persist `core_team` (list of agent IDs) when provided on project create or update. The backend MUST deduplicate `core_team` entries (case-sensitive) before storage. The backend MUST NOT validate that IDs correspond to existing agents at write time — lazy resolution at read time is correct for deleted-agent handling.
- **FR-006**: System MUST persist `repository` (string) when provided on project create or update.
- **FR-007**: System MUST cascade-delete when `DELETE /api/v1/projects/<id>` is called, in this exact order: (1) read and delete each task file in `~/.omnipus/tasks/` with matching `project_id` (log error and continue on individual task file failure — best-effort per task); (2) remove all lines with `project_id=<id>` from `project_session_links.jsonl`; (3) delete the project file. The project file MUST be the last deletion. If the project file read fails before cascade begins, return 404. Partial cascades are self-healing: tasks referencing a non-existent project are treated as unassigned and may be cleaned up via `DELETE /api/v1/board/tasks/<id>`.
- **FR-008**: System MUST append a link entry `{project_id, session_id, created_at}` to `project_session_links.jsonl` when an agent executes `system.task.create` or `system.task.update` with a non-empty `project_id` in a tracked session. `SessionMeta` MUST NOT be modified. The write MUST be performed in the `AfterTool` hook dispatch path in `loop.go` (at `al.hooks.AfterTool`, currently line 5274), by a registered built-in hook `ProjectSessionLinker` that inspects the tool name and result map. The hook MUST obtain the session ID via `tools.ToolTranscriptSessionID(ctx)` (NOT from `ToolResultHookResponse` — that struct has no `SessionID` field; the value is stored on the `context.Context` passed to the hook via `tools.WithTranscriptSessionID` at `loop.go:3767`). If `tools.ToolTranscriptSessionID(ctx)` returns an empty string (non-tracked session), the hook MUST log WARN and MUST NOT write any link entry. Before appending, the hook MUST consult a process-scoped in-memory LRU set (capacity ≥ 1,000 entries, keyed by `project_id + ":" + session_id`) and MUST skip the append if the pair was already appended during this process lifetime. This write-time dedup is best-effort (cleared on process restart); read-time dedup via `(project_id, session_id)` pair remains the durable guarantee.
- **FR-009**: Session auto-link (FR-008) MUST be best-effort — a write failure to `project_session_links.jsonl` MUST log WARN and MUST NOT prevent the tool result from being returned to the agent.
- **FR-010**: System MUST rename the `agent_ids` JSON key to `core_team` in the project struct, with lazy backward-compat reading of the old key.
- **FR-011**: System MUST NOT require a color field on projects.
- **FR-012**: The sidebar MUST display projects ordered by: `pinned=true` ascending `pin_order` first, then `pinned=false` by `created_at` descending. `PUT /api/v1/projects/{id}` with `{"pinned":true,"pin_order":N}` sets pin state; the project record is the source of truth.
- **FR-013**: The sidebar Projects section MUST collapse unpinned projects beyond 5 into "▸ N more…" with click-to-expand.
- **FR-014**: The sidebar MUST support inline project search activated by typing "/" when keyboard focus is within the sidebar Projects section (e.g. a project row or the section header is focused). The "/" shortcut MUST NOT be intercepted for global keypresses outside the sidebar. Search MUST be case-insensitive substring match on project `name` only. Results MUST update on every keystroke with no debounce delay.
- **FR-015**: A "Tasks" sidebar item MUST replace the current "Command Center" sidebar item.
- **FR-016**: The Tasks screen MUST display GTD board tasks grouped by status columns (Inbox / Next / Active / Waiting / Done).
- **FR-017**: The Tasks screen MUST support project filtering by clicking a project in the sidebar.
- **FR-018**: The new project slide-over MUST include: name (required), description (optional), core_team (optional, agent autocomplete), repository (optional).
- **FR-019**: The new project slide-over MUST block submission when name is empty, showing an inline validation error.
- **FR-020**: A "Monitor" sidebar item MUST be added in the SYSTEM navigation group.
- **FR-021**: The Monitor screen MUST contain four sections: (1) **Activity** — NOTE: `GET /api/v1/agents` returns agent config, not runtime state. Active-agent tracking lives in `al.agentCurrentSession.Store` in-process. A pre-implementation spike MUST identify the correct activity endpoint (candidate: `GET /api/v1/status` if it exposes active sessions, or a new `GET /api/v1/agents/activity` endpoint); this endpoint definition is deferred to implementation; (2) **Token Usage** — calls `GET /api/v1/stats/tokens?period=month` (new authenticated endpoint, non-admin). Response schema: `{ "agents": [{ "agent_id": string, "agent_name": string, "tokens_in": int, "tokens_out": int, "tokens_total": int }], "period_start": string, "period_end": string }`. Aggregation logic: iterate all `SessionMeta` files in the session store, group by `agent_id`, sum `Stats.TokensIn + Stats.TokensOut`. `period=month` = current calendar month UTC. Schema MUST be added to `contracts/components/schemas/TokenUsageSummary.yaml` and referenced in `openapi.yaml` before implementation. No dollar estimates in MVP. (3) **Schedules** — relocated from Command Center, all existing schedule data preserved; (4) **Audit Log** — calls `GET /api/v1/audit-log` (existing, `RequireAdmin`-gated). When the current user is not an admin, the Audit Log section MUST display "Audit log requires admin access" and MUST NOT attempt the request. When no entries exist, display "No audit entries."
- **FR-022**: All existing schedule data MUST be accessible and manageable in the Monitor Schedules section after this feature ships.
- **FR-023**: The `/api/v1/tasks` endpoint (workflow taskstore) MUST remain unchanged.
- **FR-024**: All new wire types MUST be defined in `contracts/openapi.yaml` and generated via `scripts/gen-contracts.sh` before implementation.
- **FR-025**: Every `system.project.*` and `system.task.*` tool description MUST meet all four agent-usability criteria: (1) **When-to-call** — the tool-level description states what user intent or situation should trigger this tool, in plain language; (2) **How-to-chain** — if this tool requires data from another tool first (e.g. `system.task.create` needs a `project_id` that comes from `system.project.list`), the description says so explicitly with the name of the prerequisite tool; (3) **Parameter meaning** — every parameter description states what the value represents, how to obtain it if it is not obvious, and what to do when it is optional (omit vs leave empty vs default); (4) **Enum values explained** — any parameter with a fixed set of values (e.g. `status`: inbox / next / active / waiting / done) lists those values and states when each is appropriate. A description that is shorter than one sentence, says only "the ID" or "the value", or omits any of the four criteria MUST be rewritten before this feature ships.
- **FR-026**: The full agent loop MUST write a link entry to `project_session_links.jsonl` when a tool result from `system.task.create` or `system.task.update` contains a non-empty `project_id`, without any intermediate step being omittable.
- **FR-027**: `GET /api/v1/board/tasks` MUST return at most 200 items by default; callers MAY pass `?limit=N` (max 1000) and `?offset=M` for pagination. The response body MUST include a `total` count field alongside the `items` array.
- **FR-028**: System MUST expose `GET /api/v1/projects/{id}/sessions` as an authenticated REST endpoint that returns `200` with an array of `{session_id: string, created_at: string}` objects, read from `project_session_links.jsonl` filtered by `project_id` and deduplicated; returns `200` with empty array when the project exists but has no links; returns `404` when the project does not exist.
- **FR-029**: System MUST support project archiving: `PUT /api/v1/projects/{id}` with `{"status":"archived"}` sets the project to archived; `GET /api/v1/projects` MUST exclude archived projects by default; `GET /api/v1/projects?status=archived` MUST return only archived projects; `GET /api/v1/projects?status=all` returns all. A sidebar "▸ Archive" collapsible section (hidden by default) shows archived projects. `status` defaults to `"active"` on create; legacy project files without a `status` field are treated as `"active"`.
- **FR-030**: The `/command-center` TanStack Router route definition MUST be replaced with a redirect component: `export const Route = createFileRoute('/_app/command-center')({ component: () => <Navigate to="/tasks" replace /> })`. This handles both direct-URL navigation (SPA loads, then redirects client-side) and in-app link clicks. No server-side HTTP redirect is required or appropriate for a single-page application — the Go gateway catch-all serves `index.html` for all non-API paths regardless of path specificity, making a Go-side 301 unreliable.

---

## Success Criteria

- **SC-001**: `GET /api/v1/projects/<id>` returns correct `task_count` for a project with N tasks in ≤ 100ms at p95 (measured against up to 1000 task files).
- **SC-001b**: `GET /api/v1/projects` with 20 projects and 1,000 tasks returns in ≤ 200ms at p95 (single-pass task enumeration per FR-004).
- **SC-002**: `DELETE /api/v1/projects/<id>` with 50 linked tasks removes all 51 files in ≤ 2s (filesystem-dependent; sequential `os.Remove` calls each trigger a journal flush on ext4 — 500ms is too tight on constrained disks).
- **SC-003**: All backend unit/integration tests (Tests 1-48, excluding Vitest frontend tests) pass in CI: `CGO_ENABLED=0 go test -tags goolm,stdjson -run 'TestProject|TestBoardTask|TestSessionAutoLink|TestHandle|TestAgentLoop|TestPinOrder|TestCoreTeam|TestMonitor' -p 1 ./pkg/...`. Tests 34-36 use `testutil.ScenarioProvider` (pre-scripted tool responses, deterministic). Tests 45-48 use the live LLM provider available in CI — they are the agent-usability gate for FR-025 and must not be stubbed out.
- **SC-004**: All frontend unit tests pass: `npx vitest run` exits 0 with no regressions in existing 1957 tests.
- **SC-005**: `make verify-contracts` exits 0 after all schema changes and codegen.
- **SC-006**: `golangci-lint run --build-tags=goolm,stdjson` exits 0 on all new and modified files.
- **SC-007**: A project created via the sidebar slide-over appears in the sidebar Projects section within 1 render cycle (no page reload required).
- **SC-008**: Navigating to a project via sidebar filters the task board to show only that project's tasks (verified by Playwright E2E test 30).
- **SC-009**: All existing schedules remain accessible and functional in the Monitor screen after migration (verified by E2E test 32).
- **SC-010**: `gofmt -l . | wc -l` equals 0 on the final diff.
- **SC-011**: No `"agent_ids"` key appears in any project file written by the new code (all new writes use `"core_team"`).

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|-----------------|--------------|
| FR-001 | US-1 | Create project with name only; Update core_team; Delete project cascades; Project list sorted | TestHandleProjects_CRUD |
| FR-002 | US-2 | Create board task with project; List board tasks; Filter by project; Delete board task | TestHandleBoardTasks_CRUD |
| FR-003 | US-1, US-2 | (contract gate — all scenarios) | make verify-contracts |
| FR-004 | US-1, US-3 | task_count reflects actual count | TestProjectList_TaskCountComputed |
| FR-005 | US-1, US-3 | Update core_team on existing project | TestProjectUpdate_CoreTeamPersisted |
| FR-006 | US-1 | Create project with all optional fields | TestProjectCreate_WritesFile |
| FR-007 | US-1, US-3 | Delete project cascades to tasks | TestProjectDelete_CascadesTasks; TestHandleProjects_Delete_Cascades |
| FR-008 | US-4 | Agent task.create with project_id links session | TestSessionAutoLink_OnTaskCreate |
| FR-009 | US-4 | Auto-link is best-effort | TestSessionAutoLink_BestEffort_WriteFailure |
| FR-010 | US-1, US-3 | (migration) | TestProjectUpdate_ReadsLegacyAgentIDs |
| FR-011 | US-1 | Create project with name only (no color field in response) | TestProjectCreate_WritesFile |
| FR-012 | US-5 | Projects listed newest-first with pinned at top | sidebar-projects.test.tsx — renders newest-first |
| FR-013 | US-5 | More than 5 unpinned projects collapses overflow | sidebar-projects.test.tsx — overflow collapse |
| FR-014 | US-5 | Inline search filters project list | sidebar-projects.test.tsx — inline search |
| FR-015 | US-6 | Tasks screen shows GTD tasks | tasks-screen.test.tsx — shows GTD tasks |
| FR-016 | US-6 | Tasks screen shows GTD tasks | tasks-screen.test.tsx — shows GTD tasks |
| FR-017 | US-7 | Clicking project navigates to filtered task board | tasks-screen.test.tsx — filters on project select; E2E test 30 |
| FR-018 | US-10 | Create project with name only; Create with all fields | new-project-slide-over.test.tsx — happy path |
| FR-019 | US-10 | Empty name blocks submission | new-project-slide-over.test.tsx — empty name |
| FR-020 | US-11 | Monitor screen has four sections | monitor-screen.test.tsx — four sections render |
| FR-021 | US-11 | Monitor screen has four sections; Cost breakdown | monitor-screen.test.tsx — four sections render |
| FR-022 | US-11 | Schedules preserved after Command Center removed | monitor-screen.test.tsx — schedules functional; E2E test 32 |
| FR-023 | US-2, US-6 | List board tasks returns GTD tasks only | TestBoardTaskList_ExcludesWorkflowTasks; TestWorkflowTasksUnchanged |
| FR-024 | US-1, US-2 | (contract gate) | make verify-contracts |
| FR-025 | US-12 | Project and task tool descriptions meet agent-usability criteria | TestProjectToolDescriptions_AgentUsability (Test 33) |
| FR-026 | US-4, US-12 | Agent creates task in named project; auto-link fires | TestAgentLoop_TaskCreate_WithProjectID_LinksSession; TestAgentLoop_TaskUpdate_WithProjectID_LinksSession |
| FR-027 | US-2 | (board task list pagination) | TestHandleBoardTasks_CRUD (pagination assertions); TestBoardTask_DefaultStatus_Inbox (Test 43) |
| FR-028 | US-4, US-9 | Linked sessions returned for project; 404 for unknown project; session ID via ToolTranscriptSessionID | TestHandleProjects_CRUD (sessions sub-path); TestSessionAutoLink_LinkFileWritten (Test 10); TestSessionAutoLink_LinkFileAbsent_CreatesFile (Test 40) |
| FR-029 | US-1 | Archive project removes from default list; invalid status rejected | TestHandleProjects_CRUD (archive); TestProjectStatus_InvalidValueRejected (Test 37); sidebar-projects.test.tsx — archive section |
| FR-030 | US-6 | (command-center client-side redirect) | router.test.tsx — /command-center navigates to /tasks |

---

## Ambiguity Warnings

All ambiguities resolved 2026-06-08. See Clarifications section.

| # | Resolution |
|---|-----------|
| A1 | `/api/v1/board/tasks` — confirmed |
| A2 | Separate link file `~/.omnipus/project_session_links.jsonl`; SessionMeta unchanged |
| A3 | Removed — pins have no UI surface; cascade to pins deferred |
| A4 | `pinned: bool` + `pin_order: int` fields on the project record itself. Backend is source of truth; frontend may mirror to `localStorage` as a cache. Delete cascade is automatic — pin state lives on the record. `PUT /api/v1/projects/{id}` with `{"pinned":true,"pin_order":N}` is all that's needed. |
| A5 | Tokens only — no dollar estimates in MVP. Aggregate existing `SessionMeta.Stats`; new summary endpoint only. |
| A6 | Polling at 10s interval; no new WS frame. |
| A7 | Open in new tab — confirmed |
| A8 | Archive section as collapsible "▸ Archive" at bottom of sidebar Projects area; hidden by default |

---

## Unasked Questions — Resolved 2026-06-08

The following were identified during spec review as gaps that required resolution before implementation:

| # | Question | Decision |
|---|----------|----------|
| UQ-1 | Is `pin_order` unique within a user's pinned set? | Last-writer-wins. No global sequence. If two projects have the same `pin_order`, render is stable (sorted by `created_at` as tiebreaker). |
| UQ-2 | What is the full valid set for `status`? | `"active"` and `"archived"` only. Any other value is rejected with `400`. |
| UQ-3 | When a session is deleted, are its link entries cleaned up? | No. Sessions are never deleted in this feature. If a future feature adds session deletion, it must clean up `project_session_links.jsonl` entries for that `session_id` at that time. |
| UQ-4 | Can an archived project's tasks still be created/updated? | Yes. Archive is a UI-only visibility filter; it is not an access gate. Agents can still create tasks under an archived project via tool calls or REST. |
| UQ-5 | What does the `GET /api/v1/projects/{id}/sessions` endpoint look like when the link file doesn't exist yet? | Returns `200` with `[]`. Absence of the file equals empty link set — same as FR-028. |
| UQ-6 | How are `created_at` values formatted in `project_session_links.jsonl`? | RFC3339 UTC strings, e.g. `"2026-06-08T14:22:00Z"`. Same convention as all other timestamps in Omnipus. |
| UQ-7 | What happens to the 301 redirect if the user has TanStack Router client-side routing? | No server-side 301. The `/command-center` TanStack Router route is replaced with `<Navigate to="/tasks" replace />`. The Go gateway catch-all serves `index.html` regardless; routing happens client-side. (FR-030 updated accordingly.) |
| UQ-8 | Who can delete a project? Any user or admin-only? | Any authenticated user in MVP (no per-project ownership model at Level 1). All new endpoints use the existing authenticated rate limiter. Admin-only scoping is Level 2/v0.3. |
| UQ-9 | What does `GET /api/v1/projects/{id}/sessions` return when the link file has corrupted (non-JSON) lines? | Corrupted lines are skipped silently (same as compaction behavior) and a WARN is logged per bad line. The endpoint still returns all valid entries. |
| UQ-10 | Is there a maximum number of projects? | No hard limit in MVP. `GET /api/v1/projects` returns all projects (no pagination in this version). With very large numbers (> 10,000), task_count computation degrades — users should page results. Add pagination in a follow-up. |
| UQ-11 | How does the SPA sidebar know to refresh after an agent creates a project via `system.project.create`? | The sidebar polls `GET /api/v1/projects` every 30s (same interval as existing config polling) and invalidates on relevant WS events. New projects from agent tool calls appear within 30s without user action. |
| UQ-12 | Migration of existing stored `"task_count": 0` in project JSON files? | The Go struct drops `TaskCount int \`json:"task_count"\``. At read time, the JSON unmarshaller ignores unknown fields — existing `"task_count": 0` in files is silently discarded. At next write, the field is not re-emitted. No explicit migration step needed. |
| UQ-13 | `GET /api/v1/board/tasks?project_id=<nonexistent>` — 200 empty or 404? | `200 []`. The filter matches tasks, not projects; if no tasks exist for that project_id (which may not exist), the result is simply empty. This is consistent with the pagination contract (empty `items` array, `total: 0`). |

---

## Evaluation Scenarios (Holdout)

> **Note**: For post-implementation evaluation only. Do NOT reference in the TDD plan or traceability matrix.

### Scenario: Full project lifecycle
- **Setup**: Clean `~/.omnipus/` with no existing projects.
- **Action**: Create a project named "eval-project" via the slide-over. Add two tasks via the task board. Verify task count in project header. Delete one task. Verify task count decrements. Delete the project. Verify it disappears from sidebar.
- **Expected outcome**: All counts accurate at each step; cascade delete removes remaining task; project gone from sidebar within 1 render.
- **Category**: Happy Path

### Scenario: Agent auto-links session to project
- **Setup**: A project P exists. A fresh chat session is started.
- **Action**: Ask an agent to "create a task for project P called 'setup CI'".
- **Expected outcome**: After the agent's tool call, navigating to project P in the sidebar shows at least one linked session in the "Linked sessions" section of any task in the project.
- **Category**: Happy Path

### Scenario: Many projects sidebar management
- **Setup**: Create 8 projects via the slide-over, one by one.
- **Action**: Observe the sidebar after the 6th project is added.
- **Expected outcome**: Sidebar shows 5 projects and "▸ 3 more…"; clicking the collapse link shows all 8.
- **Category**: Edge Case

### Scenario: Monitor shows schedules after upgrade
- **Setup**: Existing installation with 2 active schedules created via the old Command Center.
- **Action**: Navigate to the Monitor screen after upgrading.
- **Expected outcome**: Both schedules appear in the Schedules section with correct cron expressions, agents, and enabled state. Enable/disable toggles work.
- **Category**: Happy Path

### Scenario: Cascade delete cleans up correctly
- **Setup**: Project P with 5 tasks (T1–T5). Two tasks (T6, T7) belong to a different project Q.
- **Action**: Delete project P via the project header's delete action.
- **Expected outcome**: T1–T5 are gone (verified by `GET /api/v1/board/tasks?project_id=P` returning `[]`). T6 and T7 still exist. Project Q untouched.
- **Category**: Edge Case

### Scenario: core_team migration
- **Setup**: An existing project JSON file on disk with `"agent_ids": ["mia"]` (old format).
- **Action**: `GET /api/v1/projects/<id>`.
- **Expected outcome**: Response returns `core_team: ["mia"]`. No error, no data loss.
- **Category**: Edge Case

### Scenario: Cost breakdown zero state
- **Setup**: Fresh installation with no sessions run.
- **Action**: Navigate to Monitor → Cost Breakdown.
- **Expected outcome**: Each agent row shows `0 tokens`. No error state or empty crash.
- **Category**: Error Path

### Scenario: Agent two-step project lookup
- **Setup**: Projects "website-api" and "mobile-app" exist. Fresh chat session with Mia.
- **Action**: Send "what tasks are open in the website-api project?"
- **Expected outcome**: Agent calls `system.project.list` or already knows the project ID, then calls `system.task.list` with the correct `project_id`. Response lists only website-api tasks. mobile-app tasks do not appear. No hallucinated task names.
- **Category**: Happy Path

### Scenario: Agent handles ambiguous project name gracefully
- **Setup**: No project named "frontend" exists. Projects "website-api" and "mobile-app" exist.
- **Action**: Send "create a task called 'update nav' in the frontend project."
- **Expected outcome**: Agent either (a) creates the project first and then the task, or (b) asks for clarification. Agent does NOT silently create a task with a random or invented `project_id`.
- **Category**: Error Path

### Scenario: Agent task status progression
- **Setup**: Project "website-api" exists. Fresh chat session.
- **Action**: Send "create a task called 'setup db' in website-api, then mark it as active once created."
- **Expected outcome**: Agent calls `system.task.create` then `system.task.update` in sequence. Task ends with `status: "active"` and `project_id` set. Both calls are in the same session, which is auto-linked to website-api.
- **Category**: Happy Path

### Scenario: Tool descriptions are clear enough to use without guessing
- **Setup**: Omnipus binary built and running.
- **Action**: Read the descriptions for `system.project.create`, `system.task.create`, `system.task.update` from the tool registry (or from the source in `pkg/sysagent/tools/`).
- **Expected outcome**: For each tool — (1) the tool description states in plain language when an agent should call it; (2) `system.task.create` and `system.task.update` tell the agent to call `system.project.list` first if a project name is given; (3) every parameter description explains where to get the value and what to do when it is optional; (4) the `status` parameter lists inbox / next / active / waiting / done with a brief note on when each applies. A person reading only the schema text — without looking at any other code — could use the tool correctly.
- **Category**: Edge Case

---

## Assumptions

- Pin-to-top state for sidebar projects is stored on the project record as `pinned: bool` and `pin_order: int` fields. Backend is source of truth; frontend may mirror to `localStorage` as a cache only. (A4 resolved 2026-06-08.)
- Repository field is stored as an arbitrary string; no URL validation performed at the backend.
- The existing `system.project.*` agent tools remain functional alongside the new REST API — agents can still create/update projects via tool calls.
- `SessionMeta` is never written by this feature. Session-project relationships exist only in `project_session_links.jsonl`. The existing `SessionMeta.ProjectID` field (present in `daypartition.go:81`) is left unmodified and unused by this feature.
- Audit log data is read from the existing audit log file (if one exists in `~/.omnipus/logs/`); if no audit log exists, the section shows "No audit entries."
- `task_count` computation iterates all task files in `~/.omnipus/tasks/` and counts those matching `project_id`. For installations with ≤ 10,000 tasks this is acceptable (< 100ms). For larger installations, consider a count cache — deferred.
- The Monitor cost breakdown reads from session stats (token usage recorded in `SessionMeta.Stats`) aggregated across sessions. If no cost model is configured, estimated cost shows $0.00.

## Clarifications

### 2026-06-08 — Interview

- Q: Should sessions and projects have a one-to-one or many-to-many relationship? → A: Many-to-many. Solved with relational links, not physical separation. A session can work on multiple projects.
- Q: Is this Level 1 (metadata) or Level 2 (filesystem/rooms)? → A: Level 1 only. Projects are lightweight metadata records. No filesystem directories, no memory scoping, no room topology.
- Q: Should projects have color customization? → A: No. Design system handles visual states; color would be noise inconsistent with Sovereign Deep.
- Q: Is agent_ids an access gate or a default roster? → A: Default roster only (renamed to core_team). Any agent can work on any project's tasks.
- Q: Who creates session-project links? → A: The system only. Auto-link fires when agent creates/updates a task with project_id. No user gesture required.
- Q: What is the project creation UX? → A: Slide-over with name (required), description/core_team/repository (all optional).
- Q: What is MVP scope? → A: All 13 items (B1–B6, F1–F7) are MVP.

### 2026-06-08 — Ambiguity resolution

- A1: GTD task API path → `/api/v1/board/tasks` with `?project_id=` and `?status=` filters.
- A2: Session-project link storage → separate append-only JSONL file `~/.omnipus/project_session_links.jsonl`; `SessionMeta` is NOT modified. Sessions are fully independent — deleting a project removes link entries only, never session data.
- A3: Cascade to pins on project delete → deferred; pins have no UI surface yet.
- A4: Pin-to-top state → stored on the project record itself as `pinned: bool` and `pin_order: int` fields. Backend is source of truth; frontend may cache in `localStorage`. Deleting a project removes its pin state automatically with no extra cascade step.
- A5: Monitor token display → tokens only (no dollar estimates) for MVP. Aggregate existing `SessionMeta.Stats`; new summary endpoint, no new data collection.
- A6: Monitor live activity → 10s polling; no new WS frame.
- A7: Repository link → opens in new tab. Level 1 only; no workspace/clone action.
- A8: Archive UX → collapsible "▸ Archive" section at the bottom of the sidebar Projects area, hidden by default. Right-click → Archive sets `status: "archived"`; project moves out of the active list.
