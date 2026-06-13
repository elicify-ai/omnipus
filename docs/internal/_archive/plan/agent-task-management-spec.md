# Feature Specification: Agent Task Management System

**Created**: 2026-04-01
**Status**: Draft (pending grill)

---

## Context

Omnipus currently has a GTD-style task board (inbox/next/active/waiting/done) designed for human use. Agents cannot interact with tasks — they have no tools to list, create, or update tasks. The task board has almost no adoption (2 test tasks).

This spec replaces the GTD-style task board (implemented in Wave 5a) with an execution-focused **agent work queue** — a delegation system where humans create tasks, assign them to agents, and agents execute them autonomously. Agents can also create sub-tasks and delegate to other agents, forming a delegation chain. The GTD statuses (inbox/next/active/waiting/done) are replaced by execution statuses (queued/assigned/running/completed/failed). Existing GTD tasks are lazily migrated on first access via the TaskStore migration layer (see Phase 1).

**Relationship to Wave 5a**: Wave 5a implements the Command Center with a GTD kanban board as the initial task UI. This spec supersedes that model. The component structure introduced in Wave 5a (TaskList.tsx, TaskDetailPanel.tsx, command-center route) is preserved and extended — the GTD column configuration and drag-drop behavior are replaced with the execution-focused model described here.

---

## Impact on Wave 5a

Wave 5a's `TaskList.tsx` and `TaskDetailPanel.tsx` are the implementation targets for the UI changes described in this spec (US-7). The GTD column configuration (5 columns: Inbox, Next, Active, Waiting, Done) and drag-drop behavior between those columns are replaced with an execution-focused 4-column layout (Queued, Running, Completed, Failed). The component structure (TaskList, TaskDetailPanel, command-center route) is preserved and the components are updated in-place rather than rebuilt from scratch.

Task status changes are broadcast as WebSocket frames (type: `task_status_changed`) so the UI can update in real-time without polling. See `docs/protocol/websocket-protocol.md` for the frame schema. The Wave 5a frontend must handle this frame type in its WebSocket message router.

---

## User Stories & Acceptance Criteria

### US-1 — Human Creates and Assigns a Task (P0)

A user wants to create a task with instructions and assign it to a specific agent for autonomous execution.

**Why P0**: This is the entry point — without task creation, nothing else works.

**Independent Test**: Create a task via UI, assign to an agent, verify it appears as "queued" in the task board.

1. **Given** a user on the command center, **When** they create a task with title, prompt, agent, and priority, **Then** a task file is created at `~/.omnipus/tasks/<id>.json` with status "queued".
2. **Given** a queued task, **When** the user clicks "Start", **Then** the task status changes to "running" and the agent begins execution in a dedicated session.
3. **Given** a running task, **When** the agent completes, **Then** the task status is "completed" with a result summary and artifact list.
4. **Given** a running task, **When** the agent encounters an error, **Then** the task status is "failed" with an error description.

### US-2 — Agent Fetches Its Task Backlog (P0)

An agent needs to see what tasks are assigned to it so it can work on them.

**Why P0**: Agents must be able to discover their work.

**Independent Test**: Assign 3 tasks to an agent, call `task_list` with `role=assignee`, verify all 3 are returned sorted by priority.

1. **Given** an agent with 3 queued tasks, **When** the agent calls `task_list(role="assignee")`, **Then** it receives all 3 tasks sorted by priority (1 first), then by created_at.
2. **Given** an agent with tasks in mixed statuses, **When** it calls `task_list(role="assignee", status="queued")`, **Then** only queued tasks are returned.
3. **Given** an agent with no tasks, **When** it calls `task_list(role="assignee")`, **Then** it receives an empty list.

### US-3 — Agent Creates and Delegates a Sub-Task (P0)

An agent working on a complex task needs to delegate part of the work to another agent.

**Why P0**: Multi-agent collaboration is the core value proposition.

**Independent Test**: Agent A calls `task_create` for Agent B. Verify task appears in B's backlog and A can track its status.

1. **Given** Agent A with `can_delegate_to: ["agent-b"]`, **When** A calls `task_create(agent_id="agent-b", title="Research X", prompt="...")`, **Then** a queued task is created with `created_by=agent-a`, `agent_id=agent-b`.
2. **Given** Agent A created a task for Agent B, **When** A calls `task_list(role="delegator")`, **Then** A sees the task with B's current status.
3. **Given** Agent A with `can_delegate_to: []`, **When** A calls `task_create(agent_id="agent-b")`, **Then** the tool returns an error "delegation to agent-b not allowed".
4. **Given** Agent A creates a task with `parent_task_id`, **When** the parent task doesn't exist, **Then** the tool returns an error.

### US-4 — Agent Updates Its Own Task (P0)

An agent needs to mark its task as running, completed, or failed with results.

**Why P0**: Without status updates, the system has no feedback loop.

**Independent Test**: Agent picks up a task, calls `task_update(status="running")`, then `task_update(status="completed", result="Done")`. Verify both state transitions persist.

1. **Given** a queued task assigned to Agent A, **When** A calls `task_update(task_id, status="running")`, **Then** status changes to "running" and `started_at` is set.
2. **Given** a running task, **When** the agent calls `task_update(status="completed", result="Summary", artifacts=["/path/to/file.md"])`, **Then** status is "completed", `completed_at` is set, result and artifacts are stored.
3. **Given** a task assigned to Agent B, **When** Agent A calls `task_update` on it, **Then** the tool returns an error "you can only update tasks assigned to you".
4. **Given** a completed task, **When** the agent calls `task_update(status="running")`, **Then** the tool returns an error (invalid transition).

### US-5 — Dependency Resolution (P1)

When all child tasks of a parent complete, the parent agent is notified to continue its work.

**Why P1**: Enables the delegation chain flow but not needed for basic task execution.

**Independent Test**: Create parent task for A, A creates 2 child tasks for B. B completes both. Verify A receives a notification with child results.

1. **Given** a parent task with 2 child tasks both completed, **When** the last child completes, **Then** the parent agent receives a message in its task session summarizing all child results.
2. **Given** a parent task with 2 children, one completed and one failed, **When** the last child fails, **Then** the parent agent receives a notification including the failure information.
3. **Given** a parent_task_id chain deeper than 10 levels, **When** task_create is called, **Then** the tool returns an error "maximum dependency depth exceeded".

### US-6 — Heartbeat Task Polling (P1)

Agents automatically pick up queued tasks during their heartbeat cycle.

**Why P1**: Enables autonomous task execution without manual "Start" clicks.

**Independent Test**: Assign a queued task to an agent. Wait for heartbeat interval. Verify the task transitions to "running".

1. **Given** a queued task assigned to Agent A and heartbeat enabled, **When** the heartbeat fires, **Then** the task executor picks up the highest-priority queued task and starts execution.
2. **Given** 3 queued tasks with priorities 1, 3, 5, **When** heartbeat fires, **Then** the priority-1 task is picked up first.
3. **Given** no queued tasks, **When** heartbeat fires, **Then** no execution is triggered.

### US-7 — Task Board UI (P1)

The command center shows tasks in execution-focused columns with agent info, results, and artifact links.

**Why P1**: Visual feedback for task management.

**Independent Test**: Create tasks in various states. Verify the board shows correct columns, agent names, priority badges, and result previews.

1. **Given** tasks in queued, running, completed, and failed states, **When** the user views the command center, **Then** tasks appear in 4 columns with correct status badges.
2. **Given** a completed task with result and artifacts, **When** the user clicks the task, **Then** the detail panel shows the full result text and artifact file paths.
3. **Given** a task with child tasks, **When** the user opens the detail panel, **Then** sub-tasks are listed with their own status badges.
4. **Given** the create form, **When** the user fills title, prompt, selects an agent, and clicks create, **Then** a queued task is created.

---

## Behavioral Contract

- When a task is created, it starts in status "queued".
- When a task is started (manually or via heartbeat), it transitions to "running" with a dedicated session.
- When an agent completes a task, it calls task_update with status "completed" and a result.
- When all children of a parent task complete, the parent agent is notified.
- When an agent tries to update another agent's task, the request is rejected.
- When delegation is not allowed, task_create returns an error.

## Explicit Non-Behaviors

- The system must NOT allow agents to update tasks assigned to other agents.
- The system must NOT allow delegation to agents not in the `can_delegate_to` list.
- The system must NOT create circular dependency chains (max depth 10).
- The system must NOT run more than 3 concurrent task executions per agent (configurable).
- The system must NOT include human GTD statuses — the old model is fully replaced.

---

## Data Model

### Task Entity

```go
type TaskEntity struct {
    ID            string     `json:"id"`
    Title         string     `json:"title"`
    Prompt        string     `json:"prompt"`
    AgentID       string     `json:"agent_id"`
    CreatedBy     string     `json:"created_by"`
    ParentTaskID  string     `json:"parent_task_id,omitempty"`
    Priority      int        `json:"priority"`
    Status        string     `json:"status"`
    Result        string     `json:"result,omitempty"`
    Artifacts     []string   `json:"artifacts,omitempty"`
    TriggerType   string     `json:"trigger_type"`
    CreatedAt     time.Time  `json:"created_at"`
    StartedAt     *time.Time `json:"started_at,omitempty"`
    CompletedAt   *time.Time `json:"completed_at,omitempty"`
}
```

Storage: `~/.omnipus/tasks/<id>.json` (per-entity files, atomic writes)

### Status Machine

```
queued → running → completed
                 → failed
```

### Config Addition

```go
type AgentConfig struct {
    // ... existing ...
    CanDelegateTo []string `json:"can_delegate_to,omitempty"` // ["agent-b", "agent-c"] or ["*"]
}
```

---

## Implementation Phases

### Phase 1 — TaskStore + Migration (backend-lead)
- Create `pkg/taskstore/store.go` with List/Get/Create/Update
- Lazy migration from old GTD format
- Refactor `rest.go` task handlers to use TaskStore

### Phase 2 — Config + Context (backend-lead)
- Add `can_delegate_to` to AgentConfig/AgentDefaults
- Add task tool enable flags to ToolsConfig
- Add `WithAgentID`/`ToolAgentID` to tool context

### Phase 3 — Agent Tools (backend-lead)
- Create `pkg/tools/task.go` with task_list, task_create, task_update
- Register in `registerSharedTools`

### Phase 4 — Task Executor (backend-lead)
- Create `pkg/agent/task_executor.go`
- Dedicated session per task
- Concurrency control (max 3)
- Wire into AgentLoop

### Phase 5 — Heartbeat + Dependencies (backend-lead)
- Task polling in heartbeat
- Dependency resolution on child completion

### Phase 6 — REST API Updates (backend-lead)
- New task schema in endpoints
- Add `/tasks/{id}/subtasks` and `/tasks/{id}/start`

### Phase 7 — Frontend (frontend-lead)
- New Task type in api.ts
- Redesign TaskList.tsx (execution-focused columns)
- Redesign TaskDetailPanel.tsx (result, artifacts, sub-tasks)
- Add startTask API call

---

## Success Criteria

- **SC-001**: Agent can fetch its queued tasks via `task_list` tool within 1 second.
- **SC-002**: Agent can create a sub-task for another agent via `task_create` and the sub-task appears in the target's backlog.
- **SC-003**: Task execution produces a result and artifacts stored on the task entity.
- **SC-004**: Heartbeat picks up the highest-priority queued task within one heartbeat interval.
- **SC-005**: Parent task receives child completion notification when all children finish.
- **SC-006**: Delegation is blocked when target agent is not in `can_delegate_to` list.
- **SC-007**: UI shows tasks in execution-focused columns with result previews.
