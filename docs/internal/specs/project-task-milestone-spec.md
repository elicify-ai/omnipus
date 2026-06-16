# Feature Specification: Unified Project + Task + Milestone Management

**Created**: 2026-06-09
**Branch**: `feat/level1-project-task-mgmt`
**Status**: Revised — all MAJOR and MINOR findings from grill-spec addressed (2026-06-09). Ready for implementation.
**Builds on**: `docs/internal/specs/project-task-management-level1-spec.md` (Level 1 REST + board)

---

## Background and Scope

### What Level 1 delivered (already implemented)

| Already done | Location |
|---|---|
| Projects REST: `GET/POST /api/v1/projects`, `GET/PUT/DELETE /api/v1/projects/{id}`, `GET /api/v1/projects/{id}/sessions` | `pkg/gateway/rest_projects.go` |
| Board tasks REST: `GET/POST /api/v1/board/tasks`, `GET/PUT/DELETE /api/v1/board/tasks/{id}` | `pkg/gateway/rest_board.go` |
| `boardTask` on-disk struct: id, name, description, status (inbox/next/active/waiting/done), project_id, agent_id, created_at, updated_at | `pkg/gateway/rest_board.go:29-38` |
| `project` on-disk struct: id, name, description, status (active/archived), pinned, pin_order, core_team, repository, created_at, updated_at | `pkg/gateway/rest_projects.go:34-45` |
| Session auto-link via `project_session_links.jsonl` | `pkg/sysagent/tools/project_session_links.go` |
| Sidebar: Projects section with pinning, overflow collapse to "▸ N more…", inline "/" search, archive section | `src/components/layout/Sidebar.tsx` |
| `/tasks` route: Kanban board with Inbox / Next / Active / Waiting / Done columns | `src/components/screens/TasksScreen.tsx` |
| `NewProjectSlideOver` | `src/components/projects/NewProjectSlideOver.tsx` |
| GTD status enum in contracts: inbox / next / active / waiting / done | `contracts/components/schemas/GTDBoardTaskStatus.yaml` |

### What this spec adds (Level 1.5)

This spec extends the Level 1 foundation with:

1. **Extended task status** — adds `failed` as a sixth status value (system-set only; surfaces for human retry).
2. **Extended BoardTask fields** — adds `prompt`, `priority`, `milestone_id`, `session_id`, `result` to the existing `boardTask` struct and wire contract.
3. **Milestones** — new first-class entity scoped to a project. New REST endpoints. Filter pills on board and list view. Progress bars in project header.
4. **Default "Inbox" project** — auto-created on first use (gateway boot or first API call that touches project storage). Not deletable.
5. **Navigation change** — remove "Tasks" from top-level sidebar nav. Projects list becomes the sole entry point to task work. Clicking a project opens ProjectDetailScreen.
6. **ProjectDetailScreen** — two view modes: Board (6-column Kanban) and List/Backlog (filterable table). Replaces TasksScreen as the task-work surface.
7. **Enhanced Task creation slide-over** — adds Prompt, Priority (P1–P5), Milestone selector, "Create & Start" button.
8. **Restored TaskDetailPanel** — adapts the existing `TaskDetailPanel.tsx` (currently wired to workflow tasks) to the GTD BoardTask model with the new fields.
9. **Agent execution lifecycle** — specification of how an agent polls `next` tasks, transitions to `active`, and resolves to `done`/`failed`.

### Explicitly out of scope

- **Subtasks / dependency graph** — deferred to v0.3. `TaskDetailPanel` MUST NOT render a sub-tasks section for GTD tasks. The `subtasks` query in the existing `TaskDetailPanel.tsx` is wired to workflow tasks and MUST NOT be ported.
- **Room topology (v0.3 sandbox redesign)** — no filesystem directories per project, no per-room `TaskStore`. Level 1 flat storage is preserved.
- **Cross-project tasks** — a task belongs to exactly one project (or none).
- **Memory scoping to projects** — v0.3 only.
- **Multi-user / tenant RBAC per project** — v0.3 only.
- **Subtask drag-and-drop reassignment** — v0.3 only.

---

## Available Reference Patterns

No `docs/reference/` directory exists. The following existing patterns serve as implementation references:

| Pattern | Location | Applies to |
|---|---|---|
| GTD BoardTask on-disk struct | `pkg/gateway/rest_board.go:29-38` | Extend with new fields |
| boardTask REST handler pattern | `pkg/gateway/rest_board.go:182–582` | Mirror for milestone handlers |
| projectToWire conversion pattern | `pkg/gateway/rest_projects.go:192-228` | Mirror for milestoneToWire |
| `fileutil.WriteFileAtomic` + `fileutil.WithFlock` | `pkg/gateway/rest_projects.go:176-189` | Milestone file writes |
| `scanGTDTasks` pattern (single-pass enumeration) | `pkg/gateway/rest_projects.go:108-141` | Milestone progress computation |
| `TaskDetailPanel.tsx` (existing, wired to workflow tasks) | `src/components/command-center/TaskDetailPanel.tsx` | Restore/adapt for GTD BoardTask |
| `SmartSelect` usage in TaskDetailPanel | `src/components/command-center/TaskDetailPanel.tsx:212-232` | Priority/Status/Agent/Milestone selectors |
| `useProjectsStore` (Zustand, activeProjectId) | `src/store/projectsStore.ts` | Extend with activeMilestoneId |
| Project slide-over form pattern | `src/components/projects/NewProjectSlideOver.tsx` | Task creation slide-over |

---

## Existing Codebase Context

### Symbols Involved

| Symbol | File | Role |
|---|---|---|
| `boardTask` struct | `pkg/gateway/rest_board.go:29` | modifies — add prompt, priority, milestone_id, session_id, result fields |
| `toWireBoardTask` | `pkg/gateway/rest_board.go:142` | modifies — map new fields to wire type |
| `handleBoardTaskPost` | `pkg/gateway/rest_board.go:307` | modifies — accept new fields in create request |
| `handleBoardTaskPut` | `pkg/gateway/rest_board.go:407` | modifies — accept new fields in update request; enforce status transition rules |
| `HandleBoardTasks` | `pkg/gateway/rest_board.go:549` | extends — add sub-path routing for new per-task endpoints if needed |
| `GTDBoardTaskStatus.yaml` | `contracts/components/schemas/GTDBoardTaskStatus.yaml` | modifies — add `failed` value |
| `BoardTask.yaml` | `contracts/components/schemas/BoardTask.yaml` | modifies — add prompt, priority, milestone_id, session_id, result fields |
| `BoardTaskCreateRequest.yaml` | `contracts/components/schemas/BoardTaskCreateRequest.yaml` | modifies — add prompt, priority, milestone_id |
| `BoardTaskUpdateRequest.yaml` | `contracts/components/schemas/BoardTaskUpdateRequest.yaml` | modifies — add prompt, priority, milestone_id, session_id, result |
| `task` struct (sysagent) | `pkg/sysagent/tools/task.go:17` | modifies — add prompt, priority, milestone_id fields |
| `TaskCreateTool.Execute` | `pkg/sysagent/tools/task.go:61` | extends — accept prompt, priority, milestone_id |
| `TaskUpdateTool.Execute` | `pkg/sysagent/tools/task.go:128` | extends — accept prompt, priority, milestone_id; handle failed/active status |
| `gtdStatusSet` | `pkg/sysagent/tools/task.go:32` | modifies — add "failed" |
| `isGTDTask` / `gtdStatuses` | `pkg/gateway/rest_board.go:42` | modifies — add "failed" |
| `HandleProjects` | `pkg/gateway/rest_projects.go:268` | extends — add milestone sub-path routing |
| `useProjectsStore` | `src/store/projectsStore.ts` | extends — add activeMilestoneId |
| `TasksScreen` | `src/components/screens/TasksScreen.tsx` | replaces — this screen is superseded by ProjectDetailScreen; the `/tasks` route now redirects to the Inbox project or shows a project picker |
| `TaskDetailPanel.tsx` | `src/components/command-center/TaskDetailPanel.tsx` | adapts — re-wire from workflow Task to GTD BoardTask; add new fields |
| `Sidebar.tsx` | `src/components/layout/Sidebar.tsx` | modifies — remove "Tasks" nav item; project click → ProjectDetailScreen |
| `NAV_ITEMS` | `src/components/layout/Sidebar.tsx:33` | modifies — remove Tasks entry |
| `src/routes/_app/tasks.tsx` | route file | modifies — redirect to Inbox project detail |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|---|---|---|---|
| `GTDBoardTaskStatus.yaml` (add `failed`) | MEDIUM | All consumers of generated `GTDBoardTaskStatus` enum in Go + TS | Any switch statement exhaustiveness checks |
| `BoardTask.yaml` (add fields) | HIGH | All generated Go + TS types; all API consumers | `TaskCard`, `TaskList`, `TaskDetailPanel`, all board task tests |
| `boardTask` on-disk struct | MEDIUM | `readBoardTask`, `listBoardTasks`, `toWireBoardTask`, `writeBoardTask` | Existing board task tests; migration of existing task files |
| `gtdStatusSet` / `isGTDTask` (add `failed`) | MEDIUM | `handleBoardTaskPost`, `handleBoardTaskPut`, `scanGTDTasks` | `computeProjectTaskCounts`, cascade delete |
| `Sidebar.tsx` NAV_ITEMS | LOW | Sidebar test files | E2E navigation tests |
| `TasksScreen.tsx` (replaced) | MEDIUM | `/tasks` route; sidebar click handler | E2E tests that navigate to /tasks |
| `task` struct in sysagent | LOW | `TaskCreateTool`, `TaskUpdateTool`, `TaskListTool` | Agent loop tool execution |

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| `handleBoardTaskPost` → `writeBoardTask` → disk | Add new fields; validate milestone_id FK |
| `handleBoardTaskPut` → validate status transition → `writeBoardTask` | Status transition guards (active→failed only by system-context header; human can set all values except `active` directly) |
| `scanGTDTasks` → `computeProjectTaskCounts` | Milestone progress computation reuses this pattern |
| `ProjectSessionLinker` hook → `project_session_links.jsonl` | Unchanged; session auto-link still fires on task create/update with project_id |
| Sidebar project click → `setActiveProjectId` → navigate → ProjectDetailScreen | New; replaces navigate-to-tasks pattern |
| Agent execution loop → `system.task.update(status=active)` | Agent sets active; system may set done/failed |

---

## Data Model

### Extended `boardTask` on-disk struct (`pkg/gateway/rest_board.go`)

The following new fields are added. Existing fields are preserved unchanged.

```go
type boardTask struct { // not-wire-format
    ID          string `json:"id"`
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
    // --- existing fields above; new fields below ---
    Prompt      string `json:"prompt,omitempty"`       // Agent execution instruction (multiline markdown)
    Priority    int    `json:"priority,omitempty"`     // 1 (critical) – 5 (low); 0 = unset (treated as 3 on read)
    MilestoneID string `json:"milestone_id,omitempty"` // optional FK to milestone in same project
    SessionID   string `json:"session_id,omitempty"`   // set by system when agent starts; links to chat session
    Result      string `json:"result,omitempty"`       // execution output; set on done/failed
    Status      string `json:"status"`                 // inbox|next|active|waiting|done|failed
    ProjectID   string `json:"project_id,omitempty"`
    AgentID     string `json:"agent_id,omitempty"`
    CreatedAt   string `json:"created_at"`
    UpdatedAt   string `json:"updated_at"`
}
```

**Field notes:**

- `prompt`: stored as-is (multiline markdown accepted); max 10,000 characters.
- `priority`: integer 1–5. Default 3. Values outside 1–5 are rejected with 400 on create/update. On read, `priority=0` (legacy files without field) is returned as `3`.
- `milestone_id`: FK to a Milestone in the same project. Validated at write time: if non-empty, the milestone file MUST exist AND `milestone.project_id` MUST equal `boardTask.project_id`. Returns 400 if FK invalid.
- `session_id`: set only by the agent execution system (via a system-context header on PUT, or directly by the loop). Human PUT requests MUST NOT be able to set `session_id` arbitrarily; the handler ignores `session_id` in normal PUT requests and only accepts it from agent-context requests (identified by a trusted `X-Omnipus-Agent-Context: true` header, validated server-side against the auth token).
- `result`: read-only from the human perspective; set by the agent system on completion.
- `status`: extended from 5 to 6 values. `failed` is added. Human workflow: inbox → next → (system) → active → done/failed. Humans MAY move tasks between any statuses except they MUST NOT directly set `active` (that is reserved for the agent system, enforced server-side when agent-context header is absent).

**Status transitions:**

| From | To (human) | To (system/agent) |
|---|---|---|
| inbox | next, waiting | — |
| next | inbox, waiting | active |
| active | waiting | done, failed |
| waiting | inbox, next | active |
| done | inbox (retry) | — |
| failed | inbox (retry), next | — |

Enforcement: `PUT /api/v1/board/tasks/{id}` with `{"status":"active"}` from a non-agent-context caller returns `400 "status 'active' can only be set by an agent"`.

> **Enforcement note**: The table above is informational — it describes the intended GTD workflow. Server-side enforcement applies ONLY to the `→ active` transition (requires `X-Omnipus-Agent-Context` header). All other transitions including `done → next`, `done → waiting`, `failed → done` are accepted by the server without error. The frontend Status selector MUST show all 6 values for all task states (FR-L2-023). `active → done` without agent-context IS permitted — this is the human "force-close" path for stuck tasks.

### Milestone on-disk struct (new entity)

Storage: `~/.omnipus/milestones/{id}.json`

```go
type milestone struct { // not-wire-format
    ID          string `json:"id"`
    ProjectID   string `json:"project_id"`
    Name        string `json:"name"`
    DueDate     string `json:"due_date,omitempty"`     // YYYY-MM-DD; optional
    Description string `json:"description,omitempty"`  // optional; max 2000 chars
    CreatedAt   string `json:"created_at"`
    UpdatedAt   string `json:"updated_at"`             // set on create and every PUT
}
```

**Field notes:**

- `id`: ULID string, same pattern as projects and tasks.
- `project_id`: required FK. The milestone belongs to exactly one project.
- `name`: required; 1–200 chars.
- `due_date`: optional date string in `YYYY-MM-DD` format; not validated for future-only; stored as-is.
- `description`: optional.
- `updated_at`: Add `updated_at` unconditionally to the milestone struct and `Milestone.yaml` schema. Set on create and on every PUT.
- **Milestone progress** is computed at read time: `done_count / total_count` where counts are tasks with `milestone_id = X`. Not stored.

### Updated wire contracts (OpenAPI schemas to add/modify)

All changes follow the 5-step contract-first process (Constraint #8).

#### Modify `GTDBoardTaskStatus.yaml`
Add `failed` to the enum: `[inbox, next, active, waiting, done, failed]`

#### Modify `BoardTask.yaml`
Add:
```yaml
prompt:
  type: string
  maxLength: 10000
  description: Agent execution instruction (multiline markdown). Set on create/update.
priority:
  type: integer
  minimum: 1
  maximum: 5
  default: 3
  description: P1 (critical) through P5 (low). Defaults to P3 if omitted.
milestone_id:
  type: string
  description: Optional FK to a milestone in the same project. Validated at write time.
session_id:
  type: string
  description: Set by the agent system when execution begins. Links to a chat session.
result:
  type: string
  maxLength: 50000
  description: Execution output. Set by the agent system on done or failed. Read-only for humans.
```

#### Modify `BoardTaskCreateRequest.yaml`
Add: `prompt`, `priority` (1–5, optional), `milestone_id` (optional).

#### Modify `BoardTaskUpdateRequest.yaml`
Add: `prompt`, `priority`, `milestone_id`, `result`. Do NOT expose `session_id` as a settable field in the request schema (handled via agent-context only).

#### New `Milestone.yaml`
```yaml
type: object
required: [id, project_id, name, created_at, updated_at]
properties:
  id: {type: string}
  project_id: {type: string}
  name: {type: string, minLength: 1, maxLength: 200}
  due_date: {type: string, pattern: '^\d{4}-\d{2}-\d{2}$', description: Optional YYYY-MM-DD}
  description: {type: string, maxLength: 2000}
  created_at: {type: string, format: date-time}
  updated_at: {type: string, format: date-time, description: Set on create and every PUT}
  progress:
    type: number
    format: float
    minimum: 0
    maximum: 1
    description: done_tasks / total_tasks. Computed at read time. 0 when no tasks.
```

#### New `MilestoneCreateRequest.yaml`
```yaml
type: object
required: [name]
properties:
  name: {type: string, minLength: 1, maxLength: 200}
  due_date: {type: string, pattern: '^\d{4}-\d{2}-\d{2}$'}
  description: {type: string, maxLength: 2000}
```

#### New `MilestoneUpdateRequest.yaml`
```yaml
type: object
properties:
  name: {type: string, minLength: 1, maxLength: 200}
  due_date: {type: string, nullable: true}
  description: {type: string, maxLength: 2000}
```

To clear an existing `due_date`, set it to `null` (JSON null) in the request body. The handler MUST accept `null` for `due_date` and write the milestone without the field. Omitting `due_date` leaves the existing value unchanged.

---

## REST API Changes

### Existing endpoints — extended

| Endpoint | Change |
|---|---|
| `POST /api/v1/board/tasks` | Accept `prompt`, `priority`, `milestone_id` in request body |
| `PUT /api/v1/board/tasks/{id}` | Accept `prompt`, `priority`, `milestone_id`, `result`; enforce status transition rules |
| `GET /api/v1/board/tasks` | Return new fields in response; accept `?milestone_id=` filter (FR-L2-030), `?agent_id=` filter (FR-L2-029) |

**FR-L2-029**: `GET /api/v1/board/tasks` MUST accept an `?agent_id=` query parameter and return only tasks where `agent_id` equals the value.

**FR-L2-030**: `GET /api/v1/board/tasks` MUST accept a `?milestone_id=` query parameter and return only tasks where `milestone_id` equals the value.

### New endpoints — milestones

All milestone endpoints require authentication (same `withAuth` middleware). Rate-limited under the existing `configLimiter`. All mutating operations emit an audit log entry.

```
GET    /api/v1/projects/{project_id}/milestones
POST   /api/v1/projects/{project_id}/milestones
GET    /api/v1/projects/{project_id}/milestones/{id}
PUT    /api/v1/projects/{project_id}/milestones/{id}
DELETE /api/v1/projects/{project_id}/milestones/{id}
```

**GET /api/v1/projects/{project_id}/milestones**
- Returns `200` with response body:
  ```json
  { "milestones": [...], "total": N }
  ```
  ordered by `due_date ASC` (null due_dates last), then by `created_at ASC`.
- Returns `404` if the project does not exist.
- Includes computed `progress` on each milestone (single-pass scan across tasks).
- Milestones per project are bounded by human cognition. The list endpoint returns all milestones for the project (no pagination). Projects with more than 200 milestones are not a supported use case in this release.

**POST /api/v1/projects/{project_id}/milestones**
- Request body: `MilestoneCreateRequest`.
- Returns `201` with created `Milestone`.
- Returns `404` if project does not exist.
- Returns `400` if name missing or empty.

**GET /api/v1/projects/{project_id}/milestones/{id}**
- Returns `200` with `Milestone`.
- Returns `404` if project or milestone not found or `milestone.project_id != project_id`.

**PUT /api/v1/projects/{project_id}/milestones/{id}**
- Partial update (merge semantics).
- Returns `200` with updated `Milestone`.
- Returns `404` if not found.

**DELETE /api/v1/projects/{project_id}/milestones/{id}**
- Deletes the milestone file.
- Does NOT cascade-delete tasks. Instead, tasks with `milestone_id = deleted_id` have their `milestone_id` cleared (set to empty string) at delete time — single-pass scan + rewrite affected task files.
- Returns `204`.
- Returns `404` if not found.

### Inbox project auto-creation endpoint behavior

**FR-INX-1**: On gateway boot, after the projects directory is initialized, the system MUST check whether a project named "Inbox" with `is_default: true` (new field, see below) exists. If none does, the gateway MUST create it automatically with:
- `name: "Inbox"`
- `status: "active"`
- `is_default: true`
- `pinned: false`
- `pin_order: 0`

The Inbox project MUST always appear first in the sidebar Projects list, above all pinned and unpinned projects, regardless of creation date. It MUST NOT be reorderable by the user.

**FR-INX-2**: `DELETE /api/v1/projects/{id}` MUST return `409 Conflict` (with body `{"error":"cannot delete the default Inbox project"}`) when the target project has `is_default: true`.

**FR-INX-3**: The `is_default` field is added to the `Project` wire schema. It is set to `true` only for the Inbox project. It is read-only via the API — `PUT /api/v1/projects/{id}` MUST ignore `is_default` if provided in the request body.

**FR-INX-4**: The `storedProject` struct gets a new field:
```go
IsDefault bool `json:"is_default,omitempty"` // true only for the auto-created Inbox project
```

---

## User Stories and Acceptance Criteria

### User Story L2-1 — Inbox Default Project (P0)

A user who has never used the project system opens Omnipus and immediately sees an "Inbox" project in the sidebar, ready to receive tasks. They did not have to create it. They cannot delete it.

**Why this priority**: Every task captured goes somewhere. Without a default project, the first-run experience forces a creation step before any task can be assigned.

**Independent Test**: On a fresh `~/.omnipus/` directory, start the gateway and call `GET /api/v1/projects` — the response contains exactly one project with `name: "Inbox"` and `is_default: true`. `DELETE /api/v1/projects/<inbox-id>` returns `409`.

**Acceptance Scenarios**:

1. **Given** a fresh install with no projects, **When** the gateway starts, **Then** `GET /api/v1/projects` returns a project `{name: "Inbox", is_default: true, status: "active"}` before any user action.
2. **Given** the Inbox project exists, **When** `DELETE /api/v1/projects/<inbox-id>` is called, **Then** `409` with message `"cannot delete the default Inbox project"`.
3. **Given** the Inbox project exists, **When** it already exists and the gateway restarts, **Then** no duplicate Inbox is created; `GET /api/v1/projects` still returns exactly one project with `name: "Inbox"`.
4. **Given** a user creates additional projects, **When** the sidebar loads, **Then** Inbox appears in the Projects list (pinned or newest-first per existing sort logic).
5. **Given** `PUT /api/v1/projects/<inbox-id>` with `{"is_default": false}`, **When** processed, **Then** the field is silently ignored and `GET /api/v1/projects/<inbox-id>` still returns `is_default: true`.

---

### User Story L2-2 — Extended Task Fields (P0)

A user creating a task can supply a `prompt` (the instruction an agent executes), a `priority` (P1–P5), and optionally assign it to a `milestone`. These fields persist through the API and are displayed in the task board and detail panel.

**Why this priority**: Prompt and priority are the core input for agent-driven task execution. Without them, the "next → active" handoff to agents has no content to execute.

**Independent Test**: `POST /api/v1/board/tasks` with `{name:"test", prompt:"do X", priority:2, milestone_id:"<valid>"}` returns `201` with all fields echoed. `GET /api/v1/board/tasks/<id>` returns the same fields.

**Acceptance Scenarios**:

1. **Given** a task is created with `prompt: "Run tests and report failures"` and `priority: 1`, **When** `GET /api/v1/board/tasks/<id>`, **Then** response contains `prompt: "Run tests and report failures"` and `priority: 1`.
2. **Given** a task created without `priority`, **When** retrieved, **Then** `priority` is `3` (default).
3. **Given** `POST /api/v1/board/tasks` with `priority: 6`, **When** processed, **Then** `400` with validation error.
4. **Given** a valid milestone M in project P, **When** `POST /api/v1/board/tasks` with `{project_id: P, milestone_id: M}`, **Then** `201` with `milestone_id: M`.
5. **Given** a milestone in project Q, **When** `POST /api/v1/board/tasks` with `{project_id: P, milestone_id: M_from_Q}`, **Then** `400` "milestone does not belong to this project".
6. **Given** a task with `milestone_id` set, **When** the milestone is deleted, **Then** the task's `milestone_id` is cleared (empty string); the task is not deleted.
7. **Given** a task with `status: "done"` and `result: "output text"`, **When** `GET /api/v1/board/tasks/<id>`, **Then** `result` is present in response.

---

### User Story L2-3 — `failed` Status and Human Retry (P0)

When an agent fails to complete a task, the task's status is set to `failed` automatically. The task surfaces prominently in the Failed column of the board. A human can retry by moving it back to `next`.

**Why this priority**: Without a `failed` status, failed executions vanish silently. Operators cannot distinguish "not yet run" from "ran and crashed."

**Independent Test**: `PUT /api/v1/board/tasks/<id>` with `{"status":"failed","result":"error: timeout"}` from agent-context succeeds with `200`. `PUT /api/v1/board/tasks/<id>` with `{"status":"failed"}` from a non-agent-context returns `400`.

**Acceptance Scenarios**:

1. **Given** a task in `active` status and an agent-context request, **When** `PUT /api/v1/board/tasks/<id>` with `{status: "failed", result: "error text"}`, **Then** `200`; task has `status: "failed"` and `result: "error text"`.
2. **Given** a human (non-agent-context) sending `PUT /api/v1/board/tasks/<id>` with `{status: "active"}`, **Then** `400` "status 'active' can only be set by an agent".
3. **Given** a task with `status: "failed"`, **When** human sends `PUT` with `{status: "next"}`, **Then** `200`; task moves to `next`; `result` is preserved (not cleared by status change).
4. **Given** a task with `status: "failed"`, **When** human sends `PUT` with `{status: "inbox"}`, **Then** `200`; this is a valid retry-reset path.
5. **Given** the Board view for a project with one failed task, **When** the board renders, **Then** the Failed column shows that task with priority badge and result preview.

---

### User Story L2-4 — Agent Picks Up a "next" Task (P0)

An agent polls for `next` tasks assigned to it, moves the task to `active` when it begins, and moves to `done` or `failed` on completion. The agent sets `session_id` so the task links back to the conversation.

**Why this priority**: This is the primary execution path. Without this lifecycle, tasks are static records, not executable work items.

**Independent Test**: Create a task with `{status:"next", agent_id:"mia", prompt:"…"}`. Agent calls `PUT /api/v1/board/tasks/<id>` with `{status:"active", session_id:"<session>"}` via agent-context header. Status becomes `active`. Session_id is set. Subsequent `PUT` with `{status:"done", result:"…"}` completes the task.

**Acceptance Scenarios**:

1. **Given** a task with `status: "next"` and `agent_id: "mia"`, **When** `GET /api/v1/board/tasks?status=next&agent_id=mia`, **Then** the task appears in the response.
2. **Given** an agent-context `PUT /api/v1/board/tasks/<id>` with `{status: "active", session_id: "<id>"}`, **Then** `200`; task has `status: "active"` and `session_id` set.
3. **Given** a task in `active` status, **When** agent-context `PUT` with `{status: "done", result: "output"}`, **Then** `200`; task has `status: "done"`, `result: "output"`.
4. **Given** a task with `session_id` set, **When** human opens task detail, **Then** "Open in Chat" button is visible and navigates to `/sessions/<session_id>`.
5. **Given** `GET /api/v1/board/tasks?agent_id=mia&status=next`, **Then** only tasks matching both filters are returned.

---

### User Story L2-5 — Milestone Creation and Task Assignment (P1)

A user can create milestones within a project, assign tasks to them, and see milestone progress in the project header.

**Why this priority**: Milestones provide time-bounded grouping of tasks. Without them, all tasks are in a single unscheduled backlog regardless of deadline.

**Independent Test**: `POST /api/v1/projects/<id>/milestones` with `{name:"v1.0", due_date:"2026-08-01"}` returns `201`. Then `PUT /api/v1/board/tasks/<task_id>` with `{milestone_id: "<milestone_id>"}` returns `200`. `GET /api/v1/projects/<id>/milestones/<milestone_id>` returns `progress: 0`.

**Acceptance Scenarios**:

1. **Given** a project P, **When** `POST /api/v1/projects/P/milestones` with `{name:"v1.0", due_date:"2026-08-01"}`, **Then** `201` with `{id, project_id: P, name:"v1.0", due_date:"2026-08-01", progress: 0}`.
2. **Given** a milestone M in project P and a task T in project P, **When** `PUT /api/v1/board/tasks/T` with `{milestone_id: M}`, **Then** `200`; `GET /api/v1/board/tasks/T` returns `milestone_id: M`.
3. **Given** a milestone M with 3 tasks (1 done, 2 inbox), **When** `GET /api/v1/projects/P/milestones/M`, **Then** `progress: 0.333…` (1/3).
4. **Given** a milestone M with all tasks done, **When** `GET /api/v1/projects/P/milestones/M`, **Then** `progress: 1.0`.
5. **Given** a milestone M with no tasks, **When** `GET /api/v1/projects/P/milestones/M`, **Then** `progress: 0`.
6. **Given** `DELETE /api/v1/projects/P/milestones/M`, **Then** `204`; all tasks that had `milestone_id: M` now have `milestone_id: ""`.
7. **Given** `POST /api/v1/projects/P/milestones` with missing `name`, **When** processed, **Then** `400`.
8. **Given** `GET /api/v1/projects/nonexistent/milestones`, **Then** `404`.

---

### User Story L2-6 — Navigation: Projects as Entry Point (P0)

The "Tasks" top-level sidebar nav item is removed. Clicking a project in the sidebar opens a `ProjectDetailScreen` for that project. The old `/tasks` route redirects to the Inbox project's detail view.

**Why this priority**: Navigation is the frame that everything else hangs on. If the nav does not change, the new board/list views are unreachable by design.

**Independent Test**: The sidebar does NOT contain a "Tasks" link in `NAV_ITEMS`. Clicking a project in the sidebar navigates to `/projects/<id>`. `GET /tasks` redirects to `/projects/<inbox-id>`.

**Acceptance Scenarios**:

1. **Given** the sidebar renders, **When** inspected, **Then** no nav item with label "Tasks" is present.
2. **Given** the sidebar Projects section, **When** user clicks project P, **Then** the browser URL changes to `/projects/P` and ProjectDetailScreen renders.
3. **Given** the user navigates directly to `/tasks`, **When** the route resolves, **Then** they are redirected to `/projects/<inbox_id>` (the Inbox project's detail).
4. **Given** no projects exist except Inbox, **When** the sidebar loads, **Then** "Inbox" is visible in the Projects list.
5. **Given** a project with `is_default: true`, **When** the sidebar renders, **Then** Inbox has a distinct visual indicator (e.g. inbox icon) distinguishing it from user-created projects.

---

### User Story L2-7 — ProjectDetailScreen: Board View (P0)

A user viewing a project sees a 6-column Kanban board (Inbox / Next / Active / Waiting / Done / Failed) with a milestone filter pill row at the top. Task cards show name, priority badge (P1–P5), agent badge, and milestone tag.

**Why this priority**: This is the primary task-work surface for human review and GTD processing.

**Independent Test**: Navigate to `/projects/<id>`. Six columns are visible. Milestone filter row shows "All" plus one pill per milestone. Selecting a milestone pill filters cards to only tasks in that milestone.

**Acceptance Scenarios**:

1. **Given** a project with tasks in all 6 statuses, **When** Board view renders, **Then** all 6 columns are visible: Inbox, Next, Active, Waiting, Done, Failed.
2. **Given** a project with milestones M1 and M2, **When** Board view renders, **Then** milestone filter pills show: "All" | "M1" | "M2" | "Unscheduled".
3. **Given** milestone filter "M1" is active, **When** the board renders, **Then** only tasks with `milestone_id = M1` appear across all columns.
4. **Given** milestone filter "Unscheduled" is active, **When** the board renders, **Then** only tasks with no `milestone_id` appear.
5. **Given** milestone filter "All" is active, **When** the board renders, **Then** all tasks for the project appear.
6. **Given** a task card with `priority: 1`, `agent_id: "mia"`, and `milestone_id: M1`, **When** rendered, **Then** card shows a P1 badge (red), "mia" badge, and M1 tag.
7. **Given** a task card is clicked, **When** click fires, **Then** the task detail slide-over opens for that task.
8. **Given** the board header, **When** user clicks "+ New task", **Then** the task creation slide-over opens with the current project pre-filled and the currently active milestone pre-filled (if a milestone filter is active).

---

### User Story L2-8 — ProjectDetailScreen: List/Backlog View (P1)

A user can toggle to a List view showing tasks as a filterable table with columns: Priority | Name | Status | Milestone | Agent | Updated. Filters: Status, Priority, Milestone, Agent.

**Why this priority**: Kanban is good for workflow; list/backlog is better for prioritization across many tasks.

**Independent Test**: Toggle to List view. Table shows all tasks for the project. Filter by `Status: done` — only done tasks remain. Click a row — task detail slide-over opens.

**Acceptance Scenarios**:

1. **Given** the project header view-toggle, **When** user clicks "List", **Then** a table renders with columns: Priority | Name | Status | Milestone | Agent | Updated.
2. **Given** the List view, **When** user filters by `Status: done`, **Then** only tasks with `status: "done"` are shown.
3. **Given** the List view, **When** user filters by `Priority: P1`, **Then** only tasks with `priority: 1` are shown.
4. **Given** the List view, **When** user filters by milestone M1, **Then** only tasks with `milestone_id: M1` are shown.
5. **Given** the List view, **When** user clicks a row, **Then** the task detail slide-over opens for that task.
6. **Given** the List view with no filters, **When** "Updated" column header is clicked, **Then** rows sort by `updated_at` descending (newest first); second click reverses to ascending.

---

### User Story L2-9 — Task Creation Slide-Over (Enhanced) (P0)

A user creating a task can fill in Name, Prompt, Priority (P1–P5 default P3), Project (pre-filled), Milestone (optional, filtered to current project), and Agent. "Create" creates with `status: inbox`. "Create & Start" creates with `status: next` and triggers agent assignment.

**Why this priority**: Task creation is the start of every work item. Without prompt and priority, agents have nothing to execute and work cannot be triaged.

**Independent Test**: Click "+ New task" in a project context. Fill Name="test", Prompt="do X", Priority=P2. Click "Create & Start". Task appears in the Next column with `status: next`, `prompt: "do X"`, `priority: 2`.

**Acceptance Scenarios**:

1. **Given** the task creation slide-over opened from project P, **When** rendered, **Then** Project field is pre-filled with P; Prompt textarea, Priority selector (default P3), Milestone selector (filtered to P's milestones), Agent selector are all present.
2. **Given** the slide-over with Name="test" and Prompt="do X", **When** "Create" is clicked, **Then** task created with `status: "inbox"`, `prompt: "do X"`, `priority: 3`; slide-over closes; Inbox column updates.
3. **Given** the slide-over with Name="deploy" and Agent="mia", **When** "Create & Start" is clicked, **Then** task created with `status: "next"` (not inbox); task appears in Next column.
4. **Given** the slide-over, **When** Name is empty and "Create" is clicked, **Then** name field shows "Name is required"; no API call made.
5. **Given** an active milestone filter pill (M1) on the board, **When** "+ New task" is clicked, **Then** the Milestone field in the slide-over pre-selects M1.
6. **Given** Priority selector, **When** P1 is selected, **Then** a red "P1" badge preview appears next to the selector label.
7. **Given** a project with no milestones, **When** the Milestone field is rendered, **Then** it shows "No milestones — create one first" and the field is disabled.

---

### User Story L2-10 — Task Detail Slide-Over (Restored and Extended) (P0)

When a user clicks a task card or list row, a slide-over shows all task fields as editable selectors. It includes "Start Task" (move to `next`) and "Open in Chat" (navigate to linked session) buttons. Result is displayed read-only for done/failed tasks.

**Why this priority**: Task detail is the primary edit surface. Without it, status changes, prompt edits, and milestone reassignment require direct API calls.

**Independent Test**: Open task detail for a task with `session_id` set. "Open in Chat" button is visible. Click it — browser navigates to `/sessions/<session_id>`. Edit the prompt and click "Save" — `GET /api/v1/board/tasks/<id>` returns updated prompt.

**Acceptance Scenarios**:

1. **Given** task detail is open, **When** rendered, **Then** all fields are editable: Name, Prompt (textarea), Priority (selector), Status (selector, all 6 values), Project (selector), Milestone (selector filtered to selected project), Agent (selector).
2. **Given** task with `status: "inbox"`, **When** "Start Task" button is clicked, **Then** task moves to `status: "next"` and a success toast is shown.
3. **Given** task with `session_id` set, **When** task detail renders, **Then** "Open in Chat" button is visible; click navigates to `/sessions/<session_id>` and slide-over closes.
4. **Given** task with `session_id` NOT set, **When** task detail renders, **Then** "Open in Chat" button is absent.
5. **Given** task with `status: "done"` and `result: "output text"`, **When** task detail renders, **Then** a read-only "Result" section displays "output text".
6. **Given** task with `status: "failed"` and `result: "error: timeout"`, **When** task detail renders, **Then** Result section shows with a red border/badge; "Retry" button shown (equivalent to "Start Task" — sets status to `next`).
7. **Given** the Prompt field in edit mode, **When** user edits text and clicks "Save", **Then** `PUT /api/v1/board/tasks/<id>` is called with updated prompt; field returns to read-only display.
8. **Given** user changes the Project selector to a different project, **When** saved, **Then** task's `project_id` changes; Milestone selector clears and reloads milestones for the new project.
9. **Given** task detail open, **When** user presses Escape or clicks ✕, **Then** slide-over closes; any unsaved edits are discarded without confirmation — no browser confirmation dialog is shown.

---

### User Story L2-11 — Project Header with Milestone Progress (P1)

The project detail page shows a header with project name, description, core team, repository link, task count, and a row of milestone progress bars. Each progress bar shows `done/total` and due date.

**Why this priority**: Operators need a project-health summary at a glance before diving into the board.

**Independent Test**: Navigate to a project with 2 milestones (M1: 2/5 done, M2: 5/5 done). Project header shows two progress bars: M1 at 40%, M2 at 100%.

**Acceptance Scenarios**:

1. **Given** a project with name, description, and 2 milestones, **When** ProjectDetailScreen renders, **Then** header shows name, description, and two progress bars with percentage and due date.
2. **Given** a project with no milestones, **When** header renders, **Then** milestone progress section is absent (not empty placeholder).
3. **Given** a milestone with `due_date` in the past and `progress < 1`, **When** rendered, **Then** the due date is shown in red as overdue.
4. **Given** a project header ✎ (edit) button, **When** clicked, **Then** name and description fields become inline-editable; save calls `PUT /api/v1/projects/<id>`.

---

## Behavioral Contract

**Primary flows:**

- When the gateway starts with an empty projects directory, the system creates an Inbox project with `is_default: true` before serving requests.
- When a user clicks a project in the sidebar, the browser navigates to `/projects/<id>` and ProjectDetailScreen renders.
- When a task is created via "Create & Start", its `status` is set to `next` (not `inbox`).
- When an agent begins executing a task, it sets `status: active` and `session_id` via an agent-context PUT.
- When an agent completes a task, it sets `status: done` and `result` via an agent-context PUT.
- When an agent fails, it sets `status: failed` and `result` (error text) via an agent-context PUT.
- When a human calls `PUT /api/v1/board/tasks/{id}` with `status: active` without an agent-context header, the server returns `400`.
- When a milestone is deleted, all tasks with `milestone_id = M` have that field cleared; the tasks themselves are not deleted.
- When `DELETE /api/v1/projects/<inbox-id>` is called, the server returns `409`.
- When `DELETE /api/v1/projects/{id}` succeeds, the cascade order is: (1) delete all milestone files for the project, (2) clear `milestone_id` on tasks whose milestone belonged to the project, (3) cascade-delete tasks and session links, (4) delete the project file itself.
- When a task detail slide-over shows a task with `session_id`, the "Open in Chat" button navigates to `/sessions/<session_id>`.

**Error flows:**

- When `POST /api/v1/board/tasks` supplies `priority: 0` or `priority: 6`, the server returns `400`.
- When `POST /api/v1/board/tasks` supplies `milestone_id` for a milestone in a different project, the server returns `400`.
- When `POST /api/v1/projects/{pid}/milestones` is called for a non-existent project, the server returns `404`.
- When `GET /api/v1/projects/{pid}/milestones` fails to read individual milestone files, those files are skipped with WARN; remaining milestones are returned.

**Boundary conditions:**

- A task with `milestone_id` whose milestone has since been deleted has `milestone_id: ""` (cleared at milestone delete time). The task is never orphaned with a dangling FK.
- A task's `prompt` may be empty string (no instruction). The agent will receive no execution prompt but can still be manually started.
- A project's milestone list may be empty. The milestone filter row on the board is hidden in that case.
- `priority` defaults to `3` when absent in create requests and when reading legacy task files that lack the field.

---

## Explicit Non-Behaviors

- The system MUST NOT allow humans to set `status: "active"` via the REST API without an agent-context header.
- The system MUST NOT delete tasks when a milestone is deleted — only clear the `milestone_id` FK.
- The system MUST NOT implement subtasks, parent_task_id, or task dependency graphs — these are v0.3. The task detail panel MUST NOT show a sub-tasks section for GTD tasks.
- The system MUST NOT create filesystem directories per project (no room topology). Milestones are JSON files in `~/.omnipus/milestones/`, not per-project subdirectories.
- The system MUST NOT allow cross-project milestone assignments (`milestone.project_id != task.project_id` is rejected).
- The system MUST NOT expose `session_id` as a settable field in the `BoardTaskUpdateRequest` schema (it is set exclusively through the agent-context path).
- The system MUST NOT delete the Inbox project. The `DELETE /api/v1/projects/{id}` handler MUST check `is_default` before proceeding.
- The system MUST NOT duplicate the Inbox project on restart. The boot-time check MUST be idempotent.
- The system MUST NOT render a "Tasks" nav item in the sidebar after this feature ships.

---

## Integration Boundaries

### Extended GTD Task Storage (`~/.omnipus/tasks/*.json`)
- **Data in**: boardTask struct with new fields (prompt, priority, milestone_id, session_id, result)
- **Data out**: Same struct as JSON; `priority` defaults to 3 on read when absent
- **Contract**: Same atomic write pattern (`fileutil.WriteFileAtomic` + flock)
- **On failure**: Same as Level 1 — file errors logged, item skipped in list response
- **Development**: Real filesystem with temp dir in tests; no migration of existing task files required (new fields are optional)

### Milestone Storage (`~/.omnipus/milestones/*.json`)
- **Data in**: milestone struct (id, project_id, name, due_date, description, created_at)
- **Data out**: Same struct + computed `progress`
- **Contract**: Same file-per-entity pattern as projects and tasks; `fileutil.WriteFileAtomic` + flock
- **On failure**: File read error → log WARN + skip in list; handler continues
- **Development**: Real filesystem with temp dir in tests

### Inbox Project Auto-creation (gateway boot)
- **Trigger**: `gateway.Start()` → project initialization step (before HTTP listener opens)
- **Contract**: Read project directory; if no project file has `is_default: true`, write one. Idempotent.
- **On failure**: Log ERROR; gateway continues (Inbox creation failure is non-fatal)
- **Development**: Covered by unit test with temp dir

### Agent-Context Header (`X-Omnipus-Agent-Context: true`)
- **Purpose**: Distinguishes agent-originated status transitions (active, session_id) from human REST calls
- **Contract**: Header presence alone is insufficient — it is accepted only when the request also carries a valid bearer token. The token is validated as normal; the header merely unlocks the `active` status transition.
- **On failure**: Missing header → 400 for `status: active` writes; no other impact
- **Development**: Integration tests pass the header explicitly; unit tests can inject directly

### OpenAPI Contract (extended)
- **Data in**: New/modified schemas: GTDBoardTaskStatus, BoardTask, BoardTaskCreateRequest, BoardTaskUpdateRequest, Milestone, MilestoneCreateRequest, MilestoneUpdateRequest, Project (add is_default)
- **Data out**: Updated generated types in `pkg/api/generated/` and `src/lib/api/generated/`
- **Contract**: Constraint #8 — schema-first, `make gen-contracts` before any Go/TS code
- **On failure**: `make verify-contracts` fails CI
- **Development**: Run `make gen-contracts` after each schema change

---

## BDD Scenarios

### Feature: Inbox Default Project Auto-Creation

#### Scenario: Inbox created on fresh install
**Traces to**: L2-1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `~/.omnipus/projects/` directory contains no project files
- **When** the gateway starts
- **Then** `GET /api/v1/projects` returns an array containing exactly one project with `name: "Inbox"`, `is_default: true`, `status: "active"`

#### Scenario: Inbox not duplicated on restart
**Traces to**: L2-1, Acceptance Scenario 3
**Category**: Edge Case

- **Given** the Inbox project already exists on disk
- **When** the gateway restarts
- **Then** `GET /api/v1/projects` still returns exactly one project with `name: "Inbox"` and `is_default: true`
- **And** no duplicate Inbox project is created

#### Scenario: Inbox deletion returns 409
**Traces to**: L2-1, Acceptance Scenario 2
**Category**: Error Path

- **Given** the Inbox project exists with `is_default: true`
- **When** `DELETE /api/v1/projects/<inbox-id>` is called
- **Then** response status is `409`
- **And** response body contains `"cannot delete the default Inbox project"`
- **And** the Inbox project still exists in `GET /api/v1/projects`

#### Scenario: is_default cannot be unset via PUT
**Traces to**: L2-1, Acceptance Scenario 5
**Category**: Edge Case

- **Given** the Inbox project with `is_default: true`
- **When** `PUT /api/v1/projects/<inbox-id>` with `{"is_default": false}`
- **Then** response is `200`
- **And** `GET /api/v1/projects/<inbox-id>` still returns `is_default: true`

---

### Feature: Extended Task Fields

#### Scenario: Create task with prompt and priority
**Traces to**: L2-2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a valid project P exists
- **When** `POST /api/v1/board/tasks` with `{name:"deploy", project_id:"P", prompt:"Run deploy script", priority:2}`
- **Then** `201` with `{prompt:"Run deploy script", priority:2, status:"inbox"}`
- **And** `GET /api/v1/board/tasks/<id>` returns the same prompt and priority

#### Scenario: Default priority is 3 when omitted
**Traces to**: L2-2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a valid project P exists
- **When** `POST /api/v1/board/tasks` with `{name:"test", project_id:"P"}` (no priority field)
- **Then** `201` with `priority: 3`

#### Scenario Outline: Invalid priority values rejected
**Traces to**: L2-2, Acceptance Scenario 3
**Category**: Error Path

- **Given** a valid project P
- **When** `POST /api/v1/board/tasks` with `{name:"t", project_id:"P", priority: <value>}`
- **Then** response status is `<expected>`

**Examples**:

| value | expected | note |
|---|---|---|
| 0 | 400 | below minimum |
| 6 | 400 | above maximum |
| 1 | 201 | valid |
| 5 | 201 | valid |

#### Scenario: Cross-project milestone_id rejected
**Traces to**: L2-2, Acceptance Scenario 5
**Category**: Error Path

- **Given** project P and project Q, both exist
- **And** milestone M belongs to project Q
- **When** `POST /api/v1/board/tasks` with `{name:"t", project_id:"P", milestone_id:"M"}`
- **Then** `400` with error "milestone does not belong to this project"

#### Scenario: milestone_id cleared when milestone deleted
**Traces to**: L2-2, Acceptance Scenario 6; L2-5, Acceptance Scenario 6
**Category**: Happy Path

- **Given** project P, milestone M, and task T with `milestone_id: M`
- **When** `DELETE /api/v1/projects/P/milestones/M`
- **Then** `204`
- **And** `GET /api/v1/board/tasks/T` returns `milestone_id: ""` (empty or absent)
- **And** task T still exists with all other fields intact

---

### Feature: Failed Status and Human Retry

#### Scenario: Agent sets task to failed
**Traces to**: L2-3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** task T with `status: "active"`
- **And** the request carries a valid bearer token and `X-Omnipus-Agent-Context: true` header
- **When** `PUT /api/v1/board/tasks/T` with `{status:"failed", result:"error: connection refused"}`
- **Then** `200` with `status: "failed"` and `result: "error: connection refused"`

#### Scenario: Human cannot directly set active status
**Traces to**: L2-3, Acceptance Scenario 2
**Category**: Error Path

- **Given** task T in `status: "next"`
- **And** the request carries a valid bearer token but NO `X-Omnipus-Agent-Context` header
- **When** `PUT /api/v1/board/tasks/T` with `{status:"active"}`
- **Then** `400` with error "status 'active' can only be set by an agent"

#### Scenario: Human retries failed task by setting status to next
**Traces to**: L2-3, Acceptance Scenario 3
**Category**: Happy Path

- **Given** task T with `status: "failed"` and `result: "error text"`
- **When** (non-agent-context) `PUT /api/v1/board/tasks/T` with `{status:"next"}`
- **Then** `200` with `status: "next"`
- **And** `result` field is preserved unchanged ("error text" still present)

---

### Feature: Agent Task Execution Lifecycle

#### Scenario: Agent marks task active and sets session_id
**Traces to**: L2-4, Acceptance Scenario 2
**Category**: Happy Path

- **Given** task T with `status: "next"` and `agent_id: "mia"`
- **And** agent-context request (valid bearer token + `X-Omnipus-Agent-Context: true`)
- **When** `PUT /api/v1/board/tasks/T` with `{status:"active", session_id:"sess-abc"}`
- **Then** `200` with `status: "active"` and `session_id: "sess-abc"`

#### Scenario: Agent completes task
**Traces to**: L2-4, Acceptance Scenario 3
**Category**: Happy Path

- **Given** task T with `status: "active"` and `session_id: "sess-abc"`
- **And** agent-context request
- **When** `PUT /api/v1/board/tasks/T` with `{status:"done", result:"All tests passed"}`
- **Then** `200` with `status: "done"` and `result: "All tests passed"`

#### Scenario: Filter next tasks by agent
**Traces to**: L2-4, Acceptance Scenario 5
**Category**: Happy Path

- **Given** task T1 with `status: "next"`, `agent_id: "mia"` and task T2 with `status: "next"`, `agent_id: "jim"`
- **When** `GET /api/v1/board/tasks?status=next&agent_id=mia`
- **Then** only T1 is returned; T2 is absent

#### Scenario: Open in Chat button present when session_id set
**Traces to**: L2-4, Acceptance Scenario 4; L2-10, Acceptance Scenario 3
**Category**: Happy Path

- **Given** task T with `session_id: "sess-xyz"` is open in task detail slide-over
- **When** the slide-over renders
- **Then** "Open in Chat" button is visible
- **And** clicking it navigates to `/sessions/sess-xyz`
- **And** the slide-over closes

---

### Feature: Milestone Management

#### Scenario: Create milestone with due date
**Traces to**: L2-5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** project P exists
- **When** `POST /api/v1/projects/P/milestones` with `{name:"v1.0", due_date:"2026-08-01", description:"First release"}`
- **Then** `201` with `{id, project_id:"P", name:"v1.0", due_date:"2026-08-01", progress:0}`

#### Scenario: Milestone progress computed correctly
**Traces to**: L2-5, Acceptance Scenario 3
**Category**: Happy Path

- **Given** milestone M in project P
- **And** tasks T1 (status: done), T2 (status: inbox), T3 (status: next) all have `milestone_id: M`
- **When** `GET /api/v1/projects/P/milestones/M`
- **Then** `progress` is approximately `0.333` (1/3)

#### Scenario: Failed task counts as not-done in milestone progress
**Traces to**: FR-L2-010
**Category**: Edge Case

- **Given** milestone M in project P
- **And** task T1 (status: done) and T2 (status: failed) both have `milestone_id: M`
- **When** `GET /api/v1/projects/P/milestones/M`
- **Then** `progress` is `0.5` (1 done + 1 failed → 1/2)

#### Scenario: Milestone for non-existent project returns 404
**Traces to**: L2-5, Acceptance Scenario 8
**Category**: Error Path

- **Given** no project with ID "ghost"
- **When** `GET /api/v1/projects/ghost/milestones`
- **Then** `404`

#### Scenario: Milestone without due date is valid
**Traces to**: L2-5, Acceptance Scenario 1 (no due_date variant)
**Category**: Alternate Path

- **Given** project P exists
- **When** `POST /api/v1/projects/P/milestones` with `{name:"Backlog cleanup"}`
- **Then** `201` with `due_date` absent from response (or null)

---

### Feature: Navigation Change

#### Scenario: Tasks nav item absent from sidebar
**Traces to**: L2-6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the user is authenticated and the sidebar renders
- **When** the sidebar is inspected
- **Then** no nav link with text "Tasks" is present in `NAV_ITEMS`

#### Scenario: Sidebar project click navigates to project detail
**Traces to**: L2-6, Acceptance Scenario 2
**Category**: Happy Path

- **Given** projects P1 and P2 exist in the sidebar
- **When** user clicks P1
- **Then** the URL changes to `/projects/P1`
- **And** `ProjectDetailScreen` renders showing P1's tasks

#### Scenario: /tasks route redirects to Inbox project
**Traces to**: L2-6, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** the Inbox project exists with `id: "inbox-id"`
- **When** user navigates directly to `/tasks`
- **Then** they are redirected to `/projects/inbox-id`
- **And** `ProjectDetailScreen` renders for the Inbox project

---

### Feature: Board View with Milestone Filter

#### Scenario: Board shows 6 columns
**Traces to**: L2-7, Acceptance Scenario 1
**Category**: Happy Path

- **Given** project P has tasks in all 6 statuses (inbox, next, active, waiting, done, failed)
- **When** Board view renders for project P
- **Then** 6 columns are visible in order: Inbox | Next | Active | Waiting | Done | Failed
- **And** each column contains the correct task(s)

#### Scenario: Milestone filter pill filters board
**Traces to**: L2-7, Acceptance Scenario 3
**Category**: Happy Path

- **Given** project P has milestones M1 and M2
- **And** task T1 has `milestone_id: M1`, task T2 has `milestone_id: M2`, task T3 has no milestone
- **When** milestone filter pill "M1" is clicked
- **Then** only T1 appears on the board
- **And** T2 and T3 are hidden

#### Scenario: "Unscheduled" filter shows tasks without milestone
**Traces to**: L2-7, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** project P has task T1 (milestone_id: M1) and task T2 (no milestone)
- **When** "Unscheduled" pill is clicked
- **Then** only T2 appears on the board

#### Scenario: Task card shows priority badge and agent badge
**Traces to**: L2-7, Acceptance Scenario 6
**Category**: Happy Path

- **Given** a task with `priority: 1`, `agent_id: "mia"`, `milestone_id: M1`
- **When** the task card renders on the board
- **Then** a red "P1" badge is visible on the card
- **And** "mia" is shown as an agent badge
- **And** the milestone name is shown as a tag on the card

---

### Feature: List View with Filters

#### Scenario: List view shows all tasks for project
**Traces to**: L2-8, Acceptance Scenario 1
**Category**: Happy Path

- **Given** project P has 5 tasks across different statuses
- **When** user clicks the "List" view toggle
- **Then** a table renders with 5 rows
- **And** columns visible: Priority | Name | Status | Milestone | Agent | Updated

#### Scenario: Filter list by status
**Traces to**: L2-8, Acceptance Scenario 2
**Category**: Happy Path

- **Given** project P has 3 tasks: 2 done, 1 inbox
- **When** user selects `Status: done` filter
- **Then** table shows 2 rows; the inbox task is hidden

#### Scenario: Click list row opens task detail
**Traces to**: L2-8, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the List view shows task T in a row
- **When** user clicks the row
- **Then** task detail slide-over opens for task T

---

### Feature: Task Creation Slide-Over (Enhanced)

#### Scenario: Create task with Create & Start sets status next
**Traces to**: L2-9, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the task creation slide-over is open for project P
- **And** user fills Name="deploy infra" and Prompt="run terraform apply"
- **And** user selects Agent="jim"
- **When** "Create & Start" button is clicked
- **Then** task is created with `status: "next"`, `prompt: "run terraform apply"`, `agent_id: "jim"`
- **And** the task appears in the Next column of the board
- **And** the slide-over closes

#### Scenario: Active milestone filter pre-fills Milestone field
**Traces to**: L2-9, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** board view with milestone filter "M1" active
- **When** user clicks "+ New task"
- **Then** the task creation slide-over opens with Milestone field pre-selected to M1

#### Scenario: Empty name blocks submission
**Traces to**: L2-9, Acceptance Scenario 4
**Category**: Error Path

- **Given** the task creation slide-over is open
- **When** user clicks "Create" with Name field empty
- **Then** Name field shows "Name is required"
- **And** no API call is made

---

### Feature: Task Detail Panel (Edit Prompt, Change Status, Open in Chat)

#### Scenario: Edit prompt and save
**Traces to**: L2-10, Acceptance Scenario 7
**Category**: Happy Path

- **Given** task detail slide-over is open for task T with `prompt: "old prompt"`
- **When** user clicks the edit icon on the Prompt field
- **And** edits the text to "new prompt"
- **And** clicks "Save"
- **Then** `PUT /api/v1/board/tasks/T` is called with `{prompt: "new prompt"}`
- **And** the Prompt field displays "new prompt" in read-only mode

#### Scenario: Change status via selector
**Traces to**: L2-10, Acceptance Scenario 1
**Category**: Happy Path

- **Given** task detail for task T with `status: "inbox"`
- **When** user selects "Next" from the Status dropdown
- **Then** `PUT /api/v1/board/tasks/T` is called with `{status: "next"}`
- **And** the board column for T updates accordingly

#### Scenario: Reassign task to different project clears milestone
**Traces to**: L2-10, Acceptance Scenario 8
**Category**: Alternate Path

- **Given** task T in project P with `milestone_id: M` (milestone in P)
- **When** user selects project Q from the Project selector
- **Then** Milestone selector clears and reloads milestones for Q
- **And** if user saves without selecting a new milestone, `PUT` is sent with `{project_id: "Q", milestone_id: ""}` (milestone cleared)

#### Scenario: Result displayed for done task
**Traces to**: L2-10, Acceptance Scenario 5
**Category**: Happy Path

- **Given** task T with `status: "done"` and `result: "All 42 tests passed"`
- **When** task detail renders
- **Then** a read-only "Result" section shows "All 42 tests passed"
- **And** a "Copy" button allows clipboard copy of the result

#### Scenario: Retry button shown for failed task
**Traces to**: L2-10, Acceptance Scenario 6
**Category**: Happy Path

- **Given** task T with `status: "failed"` and `result: "error: OOM"`
- **When** task detail renders
- **Then** Result section shows "error: OOM" with a red visual indicator
- **And** a "Retry" button is visible
- **When** "Retry" is clicked
- **Then** `PUT /api/v1/board/tasks/T` is called with `{status:"next"}`
- **And** task moves to Next column

---

## Frontend Component Map

### Components to restore / adapt

| Component | Source | Action | Notes |
|---|---|---|---|
| `TaskDetailPanel.tsx` | `src/components/command-center/TaskDetailPanel.tsx` | Adapt in-place | Add required `taskMode: 'gtd' \| 'workflow'` prop. When `taskMode === 'gtd'`: re-wire from workflow `Task` type to GTD `BoardTask`; add prompt, priority, milestone, result, retry; MUST NOT render subtasks section or call `fetchSubtasks`. When `taskMode === 'workflow'`: behavior unchanged. `ProjectDetailScreen` passes `taskMode='gtd'`; `CommandCenterScreen` passes `taskMode='workflow'`. Keep "Open in Chat" button logic (already correct). |

### Components to create new

| Component | Location | Description |
|---|---|---|
| `ProjectDetailScreen` | `src/components/screens/ProjectDetailScreen.tsx` | Top-level screen for `/projects/:id`. Contains view toggle (Board / List), milestone filter pills, project header, `BoardView` or `ListView`. |
| `ProjectHeader` | `src/components/projects/ProjectHeader.tsx` | Project name, description, core team, repository link, task count, milestone progress bars. Inline-editable via ✎ button. |
| `BoardView` | `src/components/projects/BoardView.tsx` | 6-column Kanban (Inbox/Next/Active/Waiting/Done/Failed). Accepts `tasks`, `milestones`, `activeMilestoneId`. Renders `Column` and `TaskCard`. |
| `Column` (extended) | Inline in `BoardView.tsx` or extracted | Existing Column component updated to support "Failed" column and `failed` status styling. |
| `TaskCard` (extended) | `src/components/projects/TaskCard.tsx` | Renders name, priority badge (P1–P5 with color coding), agent badge, milestone tag. Clicking card opens `TaskDetailSlideOver`. |
| `ListView` | `src/components/projects/ListView.tsx` | Filterable table: Priority, Name, Status, Milestone, Agent, Updated. Sortable by Updated. Filter UI: Status, Priority, Milestone, Agent dropdowns. |
| `MilestoneFilterPills` | `src/components/projects/MilestoneFilterPills.tsx` | "All" + per-milestone pills + "Unscheduled". Controlled component: receives `milestones`, `activeMilestoneId`, `onSelect`. |
| `TaskDetailSlideOver` | `src/components/projects/TaskDetailSlideOver.tsx` | Wraps adapted `TaskDetailPanel` in a `Sheet`. Replaces the old `TaskDetailPanel` usage in CommandCenterScreen. |
| `CreateTaskSlideOver` (extended) | `src/components/projects/CreateTaskSlideOver.tsx` | Extended version of the existing form in `TasksScreen.tsx`: adds Prompt textarea, Priority selector (P1–P5), Milestone selector, "Create & Start" button. |
| `MilestoneProgressBar` | `src/components/projects/MilestoneProgressBar.tsx` | Single milestone: name, progress bar, due date (red if overdue). Used by `ProjectHeader`. |

### Routes to add / modify

| Route file | Action | Notes |
|---|---|---|
| `src/routes/_app/projects.$projectId.tsx` | Create new | Lazy-loads `ProjectDetailScreen`. |
| `src/routes/_app/tasks.tsx` | Modify | Replace `TasksScreen` render with a redirect to the Inbox project ID. Requires reading `projects` store or a static lookup. |
| `src/routes/_app/command-center.tsx` | Existing (already redirects to /tasks per FR-030) | Update redirect target to `/projects/<inbox-id>` or `/tasks` (which itself redirects). |

### Store changes

| Store | Change |
|---|---|
| `src/store/projectsStore.ts` | Add `activeMilestoneId: string \| null` and `setActiveMilestoneId` action. |

### API client additions (`src/lib/api.ts`)

| Addition | Description |
|---|---|
| `fetchMilestones(projectId)` | `GET /api/v1/projects/{id}/milestones` |
| `getMilestone(projectId, milestoneId)` | `GET /api/v1/projects/{project_id}/milestones/{id}` — can be satisfied from the list cache when milestones are loaded at project open |
| `createMilestone(projectId, body)` | `POST /api/v1/projects/{id}/milestones` |
| `updateMilestone(projectId, milestoneId, body)` | `PUT /api/v1/projects/{id}/milestones/{id}` |
| `deleteMilestone(projectId, milestoneId)` | `DELETE /api/v1/projects/{id}/milestones/{id}` |
| `milestonesQueryKeys` | TanStack Query key factory |
| Updated `fetchBoardTasks` params | Add `milestone_id?: string` and `agent_id?: string` filter parameters |
| Updated `createBoardTask` / `updateBoardTask` | Add `prompt`, `priority`, `milestone_id` to request types |

---

## Test-Driven Development Plan

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario |
|---|---|---|---|
| 1 | `TestInboxAutoCreate_OnFreshInstall` | Unit (Go) | Inbox created on fresh install |
| 2 | `TestInboxAutoCreate_Idempotent` | Unit (Go) | Inbox not duplicated on restart |
| 3 | `TestHandleProjects_DeleteInbox_Returns409` | Integration (Go) | Inbox deletion returns 409 |
| 4 | `TestHandleProjects_IsDefaultNotUnsettable` | Integration (Go) | is_default cannot be unset via PUT |
| 5 | `TestBoardTask_ExtendedFields_CreateAndRead` | Unit (Go) | Create task with prompt and priority |
| 6 | `TestBoardTask_DefaultPriority_Three` | Unit (Go) | Default priority is 3 when omitted |
| 7 | `TestBoardTask_InvalidPriority_Rejected` | Unit (Go) | Invalid priority values rejected (outline) |
| 8 | `TestBoardTask_CrossProjectMilestone_Rejected` | Unit (Go) | Cross-project milestone_id rejected |
| 9 | `TestMilestoneDelete_ClearsMilestoneIDOnTasks` | Integration (Go) | milestone_id cleared when milestone deleted |
| 10 | `TestBoardTask_FailedStatus_AgentContextRequired` | Integration (Go) | Human cannot directly set active status |
| 11 | `TestBoardTask_AgentSetsActive_SessionID` | Integration (Go) | Agent marks task active and sets session_id |
| 12 | `TestBoardTask_AgentCompletes_Done` | Integration (Go) | Agent completes task |
| 13 | `TestBoardTask_HumanRetry_FailedToNext` | Integration (Go) | Human retries failed task by setting status to next |
| 14 | `TestBoardTask_FilterByAgentAndStatus` | Unit (Go) | Filter next tasks by agent |
| 15 | `TestHandleMilestones_CRUD` | Integration (Go) | Milestone CRUD scenarios |
| 16 | `TestMilestoneProgress_Computed` | Unit (Go) | Milestone progress computed correctly |
| 17 | `TestMilestoneProgress_ZeroWhenNoTasks` | Unit (Go) | Milestone with no tasks returns progress 0 |
| 18 | `TestHandleMilestones_ProjectNotFound_404` | Integration (Go) | Milestone for non-existent project returns 404 |
| 19 | `TestMilestoneCreate_MissingName_400` | Unit (Go) | Milestone without name rejected |
| 20 | `sidebar-navigation.test.tsx` — no Tasks nav item | Unit (Vitest) | Tasks nav item absent from sidebar |
| 21 | `sidebar-navigation.test.tsx` — project click navigates | Unit (Vitest) | Sidebar project click navigates to project detail |
| 22 | `project-detail-screen.test.tsx` — board 6 columns | Unit (Vitest) | Board shows 6 columns |
| 23 | `project-detail-screen.test.tsx` — milestone filter pills | Unit (Vitest) | Board shows milestone filter pills |
| 24 | `project-detail-screen.test.tsx` — milestone pill filters board | Unit (Vitest) | Milestone filter pill filters board |
| 25 | `project-detail-screen.test.tsx` — unscheduled filter | Unit (Vitest) | Unscheduled filter shows tasks without milestone |
| 26 | `project-detail-screen.test.tsx` — list view renders | Unit (Vitest) | List view shows all tasks for project |
| 27 | `project-detail-screen.test.tsx` — list filter by status | Unit (Vitest) | Filter list by status |
| 28 | `task-card.test.tsx` — priority badge rendered | Unit (Vitest) | Task card shows priority badge and agent badge |
| 29 | `create-task-slide-over.test.tsx` — create & start sets next | Unit (Vitest) | Create task with Create & Start sets status next |
| 30 | `create-task-slide-over.test.tsx` — active milestone prefills | Unit (Vitest) | Active milestone filter pre-fills Milestone field |
| 31 | `create-task-slide-over.test.tsx` — empty name blocked | Unit (Vitest) | Empty name blocks submission |
| 32 | `task-detail-panel.test.tsx` — edit prompt | Unit (Vitest) | Edit prompt and save |
| 33 | `task-detail-panel.test.tsx` — status change via selector | Unit (Vitest) | Change status via selector |
| 34 | `task-detail-panel.test.tsx` — result shown for done | Unit (Vitest) | Result displayed for done task |
| 35 | `task-detail-panel.test.tsx` — retry button for failed | Unit (Vitest) | Retry button shown for failed task |
| 36 | `task-detail-panel.test.tsx` — open in chat visible with session_id | Unit (Vitest) | Open in Chat button present when session_id set |
| 37 | `task-detail-panel.test.tsx` — open in chat absent without session_id | Unit (Vitest) | Open in Chat absent when session_id unset |
| 38 | `task-detail-panel.test.tsx` — project change clears milestone | Unit (Vitest) | Reassign task to different project clears milestone |
| 39 | E2E: full task lifecycle on board | E2E (Playwright) | Create task → Next column → task detail → edit prompt → status change |
| 40 | E2E: milestone filter on board | E2E (Playwright) | Create milestone, assign task, filter by milestone pill |
| 41 | E2E: open in chat navigates to session | E2E (Playwright) | Task with session_id → Open in Chat → session route |
| 42 | E2E: inbox project exists on fresh install | E2E (Playwright) | Navigate to app, sidebar shows Inbox |

### Test Datasets

#### Dataset: Priority Validation

| # | Input | Boundary Type | Expected | Traces to |
|---|---|---|---|---|
| 1 | `priority: 1` | min valid | 201 | L2-2 |
| 2 | `priority: 5` | max valid | 201 | L2-2 |
| 3 | `priority: 0` | below min | 400 | L2-2 AC3 |
| 4 | `priority: 6` | above max | 400 | L2-2 AC3 |
| 5 | no priority field | missing | 201, priority=3 | L2-2 AC2 |
| 6 | `priority: 3` | midpoint valid | 201 | L2-2 |
| 7 | `priority: -1` | negative | 400 | L2-2 AC3 |
| 8 | `priority: 2.5` (float) | non-integer | 400 | L2-2 AC3 |

#### Dataset: Extended GTD Status Transitions (human context)

| # | From | To (requested) | Agent-context | Expected | Traces to |
|---|---|---|---|---|---|
| 1 | inbox | next | false | 200 | L2-3 |
| 2 | inbox | active | false | 400 | L2-3 AC2 |
| 3 | next | active | true | 200 | L2-4 AC2 |
| 4 | next | active | false | 400 | L2-3 AC2 |
| 5 | active | done | true | 200 | L2-4 AC3 |
| 6 | active | failed | true | 200 | L2-3 AC1 |
| 7 | failed | next | false | 200 | L2-3 AC3 |
| 8 | failed | inbox | false | 200 | L2-3 AC4 |
| 9 | done | inbox | false | 200 | valid retry-reset |
| 10 | waiting | next | false | 200 | valid transition |
| 11 | active | done | false | 200 | human force-close is permitted |

#### Dataset: Milestone Due Date Validation

| # | Input | Boundary | Expected | Traces to |
|---|---|---|---|---|
| 1 | `due_date: "2026-08-01"` | valid YYYY-MM-DD | 201 | L2-5 AC1 |
| 2 | no due_date field | omitted | 201, no due_date in response | L2-5 AC alternate |
| 3 | `due_date: "2020-01-01"` | past date | 201 (not validated) | L2-5 — no future-only constraint |
| 4 | `due_date: "not-a-date"` | invalid format | 400 | L2-5 |
| 5 | `due_date: "08/01/2026"` | wrong format | 400 | L2-5 |
| 6 | `due_date: ""` | empty string | 400 | L2-5 |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test |
|---|---|---|
| Existing 5-status GTD tasks still read/write correctly | `TestHandleBoardTasks_CRUD` | Extend to confirm `failed` is now also accepted; `inbox/next/active/waiting/done` continue working |
| Project cascade delete | `TestHandleProjects_Delete_Cascades` | Extend: cascade must also delete milestone files and clear `milestone_id` on tasks whose milestone was in the deleted project; add `TestHandleProjects_Delete_CascadesMilestones` |
| Session auto-link (`project_session_links.jsonl`) | `TestSessionAutoLink_OnTaskCreate` | No change; ensure extended task fields do not break hook inspection of tool result |
| `GET /api/v1/board/tasks` pagination | `TestHandleBoardTasks_CRUD` | Confirm new fields appear in paginated response items |
| `TaskDetailPanel` subtasks query | existing `TaskDetailPanel.test.tsx` | Confirm that the adapted GTD-mode `TaskDetailPanel` does NOT call `fetchSubtasks` — the subtasks section must be absent |
| `/tasks` route renders | `tasks.tsx` route test | Update: route now redirects to Inbox project; old Kanban render is removed |

---

## Functional Requirements

- **FR-L2-001**: System MUST auto-create an "Inbox" project with `is_default: true` on gateway boot when no project with `is_default: true` exists. This check MUST be idempotent.
- **FR-L2-002**: `DELETE /api/v1/projects/{id}` MUST return `409` when the target project has `is_default: true`.
- **FR-L2-003**: `PUT /api/v1/projects/{id}` MUST silently ignore `is_default` in the request body; the field is read-only via the API.
- **FR-L2-004**: `BoardTask` wire schema MUST be extended with fields: `prompt` (string, max 10,000), `priority` (int 1–5, default 3), `milestone_id` (string, optional), `session_id` (string, optional, agent-set only), `result` (string, optional).
- **FR-L2-005**: `GTDBoardTaskStatus` enum MUST be extended with `"failed"` as a valid value.
- **FR-L2-006**: `PUT /api/v1/board/tasks/{id}` with `status: "active"` MUST be rejected with `400` unless the request includes `X-Omnipus-Agent-Context: true` header (validated alongside bearer auth).
- **FR-L2-007**: `priority` MUST default to `3` when absent in create requests and when reading legacy task files without the field.
- **FR-L2-008**: `milestone_id` on a board task MUST be validated at write time: if non-empty, the milestone file MUST exist AND `milestone.project_id` MUST equal the task's `project_id`. Invalid FK returns `400`.
- **FR-L2-009**: System MUST expose `GET/POST /api/v1/projects/{project_id}/milestones` and `GET/PUT/DELETE /api/v1/projects/{project_id}/milestones/{id}` as authenticated REST endpoints.
- **FR-L2-010**: Milestone progress MUST be computed as `count(tasks where milestone_id=X AND status='done') / count(tasks where milestone_id=X)`. ALL tasks referencing the milestone are counted in the denominator regardless of status (inbox, next, active, waiting, done, failed). A `failed` task reduces the ratio identically to an `inbox` task. Result is `0` when no tasks exist for the milestone.
- **FR-L2-011**: `DELETE /api/v1/projects/{project_id}/milestones/{id}` MUST clear `milestone_id` on all tasks that referenced the deleted milestone (set to empty string). Tasks MUST NOT be deleted.
- **FR-L2-012**: Milestones MUST be stored in `~/.omnipus/milestones/{id}.json`. The `id` MUST be a ULID. The filename MUST be `{id}.json`.
- **FR-L2-013**: `GET /api/v1/projects/{project_id}/milestones` MUST return milestones sorted by `due_date ASC` (null last), then `created_at ASC`. When sorting, a milestone with an absent or empty `due_date` MUST be placed last. In Go: check `a.DueDate == ""` before lexicographic comparison.
- **FR-L2-014**: The "Tasks" top-level nav item MUST be removed from `NAV_ITEMS` in `Sidebar.tsx`.
- **FR-L2-015**: The `/projects/:projectId` TanStack Router route MUST be created, lazy-loading `ProjectDetailScreen`.
- **FR-L2-016**: The `/tasks` route MUST redirect to `/projects/<inbox-project-id>` (the project with `is_default: true`).
- **FR-L2-017**: `ProjectDetailScreen` MUST support two view modes: Board (6-column Kanban) and List (filterable table), toggled by a view-mode selector in the screen header.
- **FR-L2-018**: The Board view MUST render 6 columns: Inbox | Next | Active | Waiting | Done | Failed.
- **FR-L2-019**: The Board view MUST show milestone filter pills ("All", one per milestone, "Unscheduled") above the columns.
- **FR-L2-020**: Task cards MUST display: name, priority badge (P1–P5 with color: P1 red, P2 orange, P3 yellow, P4 blue, P5 muted), agent badge, milestone tag.
- **FR-L2-021**: The task creation slide-over MUST include: Name (required), Prompt (multiline textarea), Priority (P1–P5, default P3), Project (pre-filled), Milestone (optional, filtered to current project), Agent (optional).
- **FR-L2-022**: The task creation slide-over MUST have two submit buttons: "Create" (status=inbox) and "Create & Start" (status=next).
- **FR-L2-023**: The task detail slide-over MUST display and allow editing of: Name, Prompt, Priority, Status (all 6 values), Project, Milestone (filtered to selected project), Agent. It MUST include "Start Task" (→ next), "Open in Chat" (when session_id set), Result (read-only when done/failed), "Retry" (when failed). The `TaskDetailPanel` component MUST accept a `taskMode: 'gtd' | 'workflow'` prop (required); this behavior applies when `taskMode === 'gtd'`.
- **FR-L2-024**: The task detail slide-over MUST NOT display a subtasks section for GTD board tasks (`taskMode === 'gtd'`). The `fetchSubtasks` query MUST NOT be called in GTD mode.
- **FR-L2-025**: All new milestone REST endpoints MUST be authenticated (same `withAuth` middleware) and rate-limited under the existing `configLimiter`. All mutating milestone operations MUST emit audit log entries.
- **FR-L2-026**: All new milestone and updated board task schemas MUST be defined in `contracts/openapi.yaml` and generated via `scripts/gen-contracts.sh` before implementation (Constraint #8).
- **FR-L2-027**: `project` wire schema MUST add `is_default: boolean` field. The `Project.yaml` schema MUST be updated accordingly.
- **FR-L2-028**: `DELETE /api/v1/projects/{id}` MUST also delete all milestone files whose `project_id` equals the deleted project ID. This cascade MUST run before the project file itself is deleted, using the same best-effort pattern as task cascade (log WARN on individual file failure, do not abort). The `milestone_id` field on all tasks referencing those milestones MUST also be cleared (same pattern as FR-L2-011). Add `TestHandleProjects_Delete_CascadesMilestones` to the TDD plan.

---

## Success Criteria

- **SC-L2-001**: `GET /api/v1/projects` on a fresh install returns a project `{name:"Inbox", is_default:true}` within one gateway boot cycle, before any user action.
- **SC-L2-002**: `DELETE /api/v1/projects/<inbox-id>` consistently returns `409` across 100 sequential calls.
- **SC-L2-003**: `POST /api/v1/board/tasks` with valid `priority`, `prompt`, and `milestone_id` returns `201` with all fields echoed; `GET /api/v1/board/tasks/<id>` returns identical values.
- **SC-L2-004**: `PUT /api/v1/board/tasks/<id>` with `{status:"active"}` returns `400` without agent-context header; returns `200` with it.
- **SC-L2-005**: `DELETE /api/v1/projects/{pid}/milestones/{mid}` clears `milestone_id` on all affected tasks and returns `204`; subsequent `GET /api/v1/board/tasks?milestone_id=<mid>` returns `[]`.
- **SC-L2-006**: Milestone progress computed via `GET /api/v1/projects/{pid}/milestones/{mid}` matches expected value within floating-point tolerance (±0.001) for N=0, N=1 (all done), N=3 (1 done, 2 pending) cases.
- **SC-L2-007**: Sidebar renders with no "Tasks" nav item; clicking a project navigates to `/projects/<id>` within one React render cycle.
- **SC-L2-008**: `ProjectDetailScreen` Board view renders 6 columns for a project with tasks in all 6 statuses.
- **SC-L2-009**: Milestone filter pill selection filters the board to only tasks in that milestone within one render cycle (no network request required if tasks are already loaded).
- **SC-L2-010**: Task detail slide-over for a task with `session_id` set shows "Open in Chat" button; clicking it navigates to `/sessions/<session_id>` (verified by Playwright E2E test 41).
- **SC-L2-011**: All backend unit/integration tests (Tests 1–19) pass in CI: `CGO_ENABLED=0 go test -tags goolm,stdjson -run 'TestInbox|TestBoardTask|TestMilestone|TestHandleMilestones|TestHandleProjects_Delete' -p 1 ./pkg/...`
- **SC-L2-012**: All Vitest frontend tests (Tests 20–38) pass: `npx vitest run` exits 0 with no regressions in existing test suite.
- **SC-L2-013**: `make verify-contracts` exits 0 after all schema changes and codegen.
- **SC-L2-014**: `golangci-lint run --build-tags=goolm,stdjson` exits 0 on all new and modified files.
- **SC-L2-015**: No `subtasks` TanStack Query call appears in the network tab when a GTD task detail slide-over is opened.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-L2-001 | L2-1 | Inbox created on fresh install; Inbox not duplicated on restart | TestInboxAutoCreate_OnFreshInstall; TestInboxAutoCreate_Idempotent |
| FR-L2-002 | L2-1 | Inbox deletion returns 409 | TestHandleProjects_DeleteInbox_Returns409 |
| FR-L2-003 | L2-1 | is_default cannot be unset via PUT | TestHandleProjects_IsDefaultNotUnsettable |
| FR-L2-004 | L2-2 | Create task with prompt and priority; Default priority is 3 | TestBoardTask_ExtendedFields_CreateAndRead; TestBoardTask_DefaultPriority_Three |
| FR-L2-005 | L2-3 | Agent sets task to failed; failed status in status outline | TestBoardTask_FailedStatus_AgentContextRequired |
| FR-L2-006 | L2-3 | Human cannot directly set active status | TestBoardTask_FailedStatus_AgentContextRequired |
| FR-L2-007 | L2-2 | Default priority is 3 when omitted | TestBoardTask_DefaultPriority_Three |
| FR-L2-008 | L2-2 | Cross-project milestone_id rejected | TestBoardTask_CrossProjectMilestone_Rejected |
| FR-L2-009 | L2-5 | Milestone CRUD scenarios | TestHandleMilestones_CRUD |
| FR-L2-010 | L2-5 | Milestone progress computed correctly | TestMilestoneProgress_Computed |
| FR-L2-011 | L2-2, L2-5 | milestone_id cleared when milestone deleted | TestMilestoneDelete_ClearsMilestoneIDOnTasks |
| FR-L2-012 | L2-5 | Milestone CRUD scenarios | TestHandleMilestones_CRUD |
| FR-L2-013 | L2-5 | Milestone CRUD scenarios (ordering) | TestHandleMilestones_CRUD |
| FR-L2-014 | L2-6 | Tasks nav item absent from sidebar | sidebar-navigation.test.tsx |
| FR-L2-015 | L2-6 | Sidebar project click navigates | sidebar-navigation.test.tsx; E2E test 39 |
| FR-L2-016 | L2-6 | /tasks route redirects to Inbox project | sidebar-navigation.test.tsx |
| FR-L2-017 | L2-7, L2-8 | Board shows 6 columns; List view renders | project-detail-screen.test.tsx |
| FR-L2-018 | L2-7 | Board shows 6 columns | project-detail-screen.test.tsx |
| FR-L2-019 | L2-7 | Board shows milestone filter pills; Milestone filter pill filters board | project-detail-screen.test.tsx; E2E test 40 |
| FR-L2-020 | L2-7 | Task card shows priority badge and agent badge | task-card.test.tsx |
| FR-L2-021 | L2-9 | Create task with Create & Start; Active milestone prefills | create-task-slide-over.test.tsx |
| FR-L2-022 | L2-9 | Create task with Create & Start sets status next | create-task-slide-over.test.tsx |
| FR-L2-023 | L2-10 | Edit prompt; Change status; Open in Chat; Result displayed; Retry button | task-detail-panel.test.tsx |
| FR-L2-024 | L2-10 | Subtasks section absent for GTD tasks | task-detail-panel.test.tsx (regression: no fetchSubtasks call) |
| FR-L2-025 | L2-5 | Milestone CRUD scenarios (auth) | TestHandleMilestones_CRUD; make verify-contracts |
| FR-L2-026 | all | (contract gate) | make verify-contracts; make gen-contracts |
| FR-L2-027 | L2-1 | is_default field in project response | TestHandleProjects_IsDefaultNotUnsettable; TestInboxAutoCreate_OnFreshInstall |
| FR-L2-028 | L2-1, L2-5 | Project cascade delete (milestones) | TestHandleProjects_Delete_CascadesMilestones |
| FR-L2-029 | L2-4 | Filter next tasks by agent | TestBoardTask_FilterByAgentAndStatus |
| FR-L2-030 | L2-5, L2-7 | Milestone filter pill filters board | TestBoardTask_FilterByMilestoneId |

---

## Ambiguity Warnings

All items below are either resolved or acknowledged. Implementers MUST NOT make silent assumptions.

| # | Item | Resolution |
|---|---|---|
| AW-1 | How does the gateway obtain the Inbox project ID to use in the `/tasks` redirect? | The SPA reads `GET /api/v1/projects` (already loaded), filters for `is_default: true`, and uses that project's `id`. The TanStack Router redirect component performs this lookup. |
| AW-2 | Should `active` status be settable by humans who provide the agent-context header manually? | The header is accepted only alongside a valid bearer token. Any authenticated user can provide the header. MVP does not distinguish "user" from "agent" at the token level. Implementers should document this and flag for a follow-up: agent tokens vs human tokens is a v0.3 concern. |
| AW-3 | Does `PUT /api/v1/board/tasks/{id}` with `{session_id: "x"}` from a human (no agent-context) fail, succeed, or silently ignore? | The `session_id` field is NOT included in `BoardTaskUpdateRequest` schema. The OpenAPI schema excludes it. The handler has no path to set it from a human request. It can only be set via the agent-context path. |
| AW-4 | Where are milestones stored — per-project directory or flat global directory? | Flat global directory: `~/.omnipus/milestones/{id}.json`. The `project_id` field on the milestone is the FK. This matches the flat storage model of Level 1. No per-project subdirectories. |
| AW-5 | What happens to a task's `milestone_id` when the task is moved to a different project? | The `milestone_id` must be cleared (set to empty string) if the new project differs from the milestone's `project_id`. The handler enforces this: if `project_id` and `milestone_id` are both present in the PUT request, FK validation runs. If project changes but milestone is not cleared in the request, the server checks and returns 400 if the old milestone does not belong to the new project. |
| AW-6 | How does the Inbox project ID reach the SPA router for the `/tasks` redirect? | The redirect component queries the projects list (from TanStack Query cache or fresh fetch), finds `is_default: true`, and navigates. A loading state (spinner) MUST be shown while the projects list loads. If the fetch fails, redirect falls back to `/` (root chat). |

---

## Out of Scope

The following items are explicitly deferred and MUST NOT be implemented in this feature:

- **Subtasks and dependency graphs**: Deferred to v0.3. `TaskDetailPanel` for GTD tasks MUST NOT include any subtask UI. The `fetchSubtasks` query from the original `TaskDetailPanel.tsx` MUST NOT be ported.
- **Room topology and per-project filesystem roots**: This is the v0.3 redesign described in `docs/internal/design/tasks-redesign-2026-05.md` and `docs/internal/design/sandbox-redesign-2026-05.md`. All milestone and task storage is flat (`~/.omnipus/milestones/`, `~/.omnipus/tasks/`).
- **Cross-project task dependencies**: A task belongs to exactly one project. Cross-project promotion is a v0.3 concept.
- **Memory scoping to projects**: The v0.3 memory redesign.
- **Git-aware project directories**: v0.3 only.
- **Multi-user RBAC per project**: v0.3 only.
- **Drag-and-drop Kanban reassignment**: Not specified. If implemented, it is a bonus — not a requirement of this spec.
- **Session deletion cleanup of project_session_links.jsonl**: Sessions are never deleted in this feature. If session deletion is added later, it must clean up link entries at that time (see UQ-3 in Level 1 spec).
- **Gantt chart or timeline view**: Not in scope.

---

## Evaluation Scenarios (Holdout)

> **Note**: For post-implementation evaluation only. Do NOT reference in the TDD plan or traceability matrix.

### Scenario EV-1: Fresh install Inbox project
- **Setup**: Remove `~/.omnipus/projects/` entirely.
- **Action**: Start gateway, open SPA, observe sidebar.
- **Expected outcome**: Sidebar shows "Inbox" in the Projects list with no user action. Navigate to it — Board view renders (possibly empty). Attempting to delete it via the project header menu either shows "Delete" as disabled or produces an error toast "Cannot delete the Inbox project."
- **Category**: Happy Path

### Scenario EV-2: Full task lifecycle with agent execution
- **Setup**: Fresh install. Inbox project exists. Agent "mia" configured.
- **Action**: Click Inbox → Create Task → Name="run tests", Prompt="npx vitest run", Priority=P2, Agent=mia → "Create & Start".
- **Expected outcome**: Task appears in Next column. Task card shows P2 badge and "mia" badge. Open task detail — Status shows "Next", Prompt shows "npx vitest run". Start the agent (simulate or use "Start Task" button) — task moves to Active, `session_id` is set. After completion — task moves to Done, Result section shows output. "Open in Chat" button visible.
- **Category**: Happy Path

### Scenario EV-3: Milestone progress bar reflects reality
- **Setup**: Project P with milestone "v1.0". Create 4 tasks assigned to v1.0. Complete 2 of them (set to `done`).
- **Action**: Navigate to project P header.
- **Expected outcome**: Milestone "v1.0" progress bar shows 50%. After completing a third task (total 3/4), bar updates to 75%.
- **Category**: Happy Path

### Scenario EV-4: Failed task surfaces and can be retried
- **Setup**: Task T with `status: "active"`.
- **Action**: Set T to `failed` with `result: "error: OOM"` via agent-context PUT. Navigate to project board.
- **Expected outcome**: Task T appears in the Failed column. Click it — task detail shows result "error: OOM" in red. Click "Retry" — task moves to Next column. Task card is no longer in Failed column.
- **Category**: Happy Path

### Scenario EV-5: Milestone deletion does not orphan tasks
- **Setup**: Project P, milestone M, 3 tasks assigned to M.
- **Action**: Delete milestone M via the milestone management UI (or `DELETE /api/v1/projects/P/milestones/M`).
- **Expected outcome**: 204 returned. All 3 tasks still exist. Navigate to List view — tasks show "—" in the Milestone column. Filter by "Unscheduled" — all 3 tasks appear.
- **Category**: Edge Case

### Scenario EV-6: Board milestone filter with "Unscheduled"
- **Setup**: Project P, milestone M1, 2 tasks with `milestone_id: M1`, 3 tasks with no milestone.
- **Action**: Click "Unscheduled" filter pill on the board.
- **Expected outcome**: Only the 3 unscheduled tasks appear on the board. The 2 M1 tasks are hidden. Click "All" — all 5 appear.
- **Category**: Happy Path

### Scenario EV-7: agent_id + status filter combination
- **Setup**: 3 tasks — T1 (agent=mia, status=next), T2 (agent=jim, status=next), T3 (agent=mia, status=active).
- **Action**: `GET /api/v1/board/tasks?agent_id=mia&status=next`
- **Expected outcome**: Only T1 is returned. T2 (wrong agent) and T3 (wrong status) are absent.
- **Category**: Edge Case

### Scenario EV-8: Inbox cannot be deleted via sidebar context menu
- **Setup**: Inbox project in sidebar.
- **Action**: Right-click (or "..." menu) Inbox project in sidebar → select "Delete".
- **Expected outcome**: Either the Delete option is greyed out / absent for the Inbox project, OR an error toast is shown: "Cannot delete the Inbox project." The project remains in the sidebar.
- **Category**: Error Path

---

## Assumptions

- `X-Omnipus-Agent-Context: true` header is the sole mechanism for distinguishing agent-originated status transitions in this feature. Agent-specific tokens are a v0.3 concern.
- Milestone storage is flat (`~/.omnipus/milestones/*.json`), consistent with the Level 1 flat-storage model. No per-project subdirectory.
- `priority: 0` in a stored legacy task file (without the field) is normalized to `3` at read time without a file rewrite. The file is only rewritten if the task is explicitly updated.
- The Inbox auto-creation check happens in `gateway.Start()`, before the HTTP listener opens. It is synchronous and blocking — if it fails, a WARN is logged but the gateway continues.
- Milestone progress is computed on every read of a single milestone. For list responses, a single-pass scan of all task files builds a `map[milestoneID]counts` (mirrors the `computeProjectTaskCounts` pattern for projects).
- The `TaskDetailPanel.tsx` component MUST accept a `taskMode: 'gtd' | 'workflow'` prop (required). When `taskMode === 'gtd'`, the component renders using the `BoardTask` type, shows prompt/priority/milestone/result/retry fields, and MUST NOT render the subtasks section or call `fetchSubtasks`. When `taskMode === 'workflow'`, behavior is unchanged from today. The new `ProjectDetailScreen` passes `taskMode='gtd'`. The existing `CommandCenterScreen` usage passes `taskMode='workflow'`.
- The SPA project store (`useProjectsStore`) is extended minimally — only `activeMilestoneId` is added. Milestone data is fetched server-side per project load and is not globally cached in Zustand.
```

---

### Critical Files for Implementation

- `/home/dev/omnipus/pkg/gateway/rest_board.go` — extend `boardTask` struct and handlers with new fields; add `failed` status; enforce agent-context status transition rules
- `/home/dev/omnipus/contracts/components/schemas/BoardTask.yaml` — add `prompt`, `priority`, `milestone_id`, `session_id`, `result` fields (contract-first gate for all code)
- `/home/dev/omnipus/pkg/gateway/rest_projects.go` — add Inbox auto-creation, `is_default` field, 409 guard on delete, milestone sub-path routing
- `/home/dev/omnipus/src/components/screens/ProjectDetailScreen.tsx` — new file; primary frontend surface for this feature (board/list, milestone pills, task creation, task detail)
- `/home/dev/omnipus/src/components/command-center/TaskDetailPanel.tsx` — adapt from workflow Task to GTD BoardTask; add new fields; remove subtasks; keep "Open in Chat"
