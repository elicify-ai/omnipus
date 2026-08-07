# UAT Plan v2: Delegation, Monitoring & Communication Tools

**Goal:** Systematically test every delegation, monitoring, steering, communication, control, plan/task-lifecycle, and bash tool available to the Orchestrator (Jim), including edge cases and failure modes, to verify correct behavior and surface bugs.

**Tester:** Jim (Orchestrator) — first user / UAT user
**Date:** 2026-07-31
**Revision:** v2 — incorporates findings from two independent critical reviews (Ray + Worker)

---

## Revision History

| Version | Date | Changes |
|---------|------|---------|
| v1 | 2026-07-31 | Initial draft |
| v2 | 2026-07-31 | Added: plan/task lifecycle suite (create_plan, execute_plan, run_task, update_task, stop_plan, message_parent), foreground bash, cwd/persistent params, snapshot/critical/allow_blocking_question params, since_cursor/max on inbox, list_tasks role filters, combined list_jobs filters, race-condition suite, resource-limit suite, invalid-input suite, boundary-value suite, security-boundary suite, setup/teardown protocol, deterministic subagent prompts, bug-severity rubric. Sharpened all pass/fail criteria to binary assertions. |

---

## Scope

### Tools in scope

| Category | Tools / Actions |
|----------|----------------|
| Dispatch | `delegate(action="run", async=true)`, `delegate(action="run", async=false)`, `delegate(action="run", launch_profile="utility")`, `delegate(action="run", launch_profile="specialist")` |
| Monitor | `delegate(action="status")` with and without task_id/session_id, `delegate(action="peek")`, `list_jobs` (all filters), `list_tasks` (role/status filters) |
| Communicate | `delegate(action="steer")`, `delegate(action="respond")`, `delegate(action="inbox")` with since_cursor/max, `delegate(action="inbox_ack")`, `message_parent` (child→parent push) |
| Control | `delegate(action="cancel", hard=false)`, `delegate(action="cancel", hard=true)`, `delegate(action="follow_up")` |
| Delegate params | `snapshot`, `critical`, `allow_blocking_question`, `timeout_seconds` |
| Plan/Task lifecycle | `create_plan`, `execute_plan`, `stop_plan`, `create_task` (rich params), `update_task`, `run_task` |
| Background Bash | `bash(run_in_background=true)`, `bash(run_in_background=false)` (foreground), `bash(action="poll")`, `bash(action="read")`, `bash(action="kill")`, `bash(cwd=...)`, `bash(persistent=true)`, `bash(timeout_seconds=...)` |

---

## Setup & Teardown Protocol

### Pre-suite setup
1. **Environment probe:** Record configured agents (Ava, Ray, Worker), chain-depth limit (3), default timeout (5 min / 300s), bash timeout range (1-3600).
2. **Baseline snapshot:** Call `list_jobs()` and `list_tasks(role="assignee")` — record the count of existing jobs/tasks. All later count-based assertions use this baseline.
3. **Controlled subagent prompts:** All "long-running" tests use a task prompt that explicitly sleeps: `"Run this bash command: sleep 30 && echo done. Return the output."` This guarantees a minimum 30-second runtime.
4. **Question-asking prompt:** For TC-C06 (respond), use a specialist-profile task: `"Ask the parent agent what number to return, then return that number."` This deterministically triggers a question message.
5. **Message-producing prompt:** For TC-C02 (inbox), use a specialist-profile task: `"Send a progress message saying 'step 1', then a checkpoint message saying 'step 2', then return 'done'."`

### Per-test teardown
- After each test: cancel any running subagents, kill any background bash sessions, delete any created tasks/plans.
- Teardown is mandatory even on test failure.
- After teardown, verify `list_jobs()` returns to baseline count.

### Post-suite cleanup
- Delete all tasks and plans created during the suite.
- Kill all background bash sessions.
- Verify `list_jobs()` and `list_tasks(role="assignee")` match the pre-suite baseline.

---

## Bug Severity Rubric

| Severity | Definition | Examples |
|----------|------------|----------|
| P0 — Blocker | Tool is non-functional or causes data loss | `delegate` crashes; `cancel` doesn't stop the subagent; task data is corrupted |
| P1 — Critical | Core functionality broken with no workaround | `steer` never reaches the child; `inbox` always returns empty; `status` shows wrong state |
| P2 — Major | Functionality broken but workaround exists | `list_jobs` missing terminal rows; `follow_up` errors but re-dispatch works |
| P3 — Minor | Cosmetic or edge-case issue | Status label mismatch; error message unclear; boundary value not enforced |
| P4 — Trivial | Cosmetic only | Typo in error message; inconsistent field naming |

---

## Test Suites

Tests are organized into independent suites. Within each suite, tests may depend on prior tests (explicitly noted). Suites are independent of each other given setup/teardown.

---

### Suite A: Dispatch

#### TC-A01: Async Dispatch — Happy Path
- **Action:** `delegate(agent_id="worker", task="Return the string 'hello'", async=true)`
- **Expected:** Returns a `task_id` and `session_id` immediately. Does not block.
- **Pass:** `task_id` is a non-empty string AND the call returns within 5 seconds.

#### TC-A02: Sync Dispatch — Happy Path
- **Action:** `delegate(agent_id="worker", task="Return the string 'hello'", async=false)`
- **Expected:** Blocks until completion, returns result inline.
- **Pass:** Result contains the literal substring `"hello"`.

#### TC-A03: create_task — Durable Task with Rich Params
- **Action:** `create_task(agent_id="worker", title="UAT durable task", prompt="Return 'created'", criteria=[{"kind":"prose","text":"Result contains 'created'"}], priority=2, due="2026-08-01T00:00:00Z")`
- **Expected:** Task created with all fields.
- **Pass:** `list_tasks(role="delegator")` returns a task with title "UAT durable task", priority=2, due date matching input. (Then delete it in teardown.)

#### TC-A04: Delegate with snapshot Parameter
- **Action:** `delegate(agent_id="worker", task="What notes were provided to you? Return them verbatim.", async=false, snapshot={"notes":"SECRET_CONTEXT_12345","references":[]})`
- **Expected:** Child receives the curated context.
- **Pass:** Result contains the literal substring `"SECRET_CONTEXT_12345"`.

#### TC-A05: Delegate with critical Flag
- **Action:** `delegate(agent_id="worker", task="sleep 5 && echo done", async=true, critical=true)` — then immediately end the parent turn.
- **Expected:** Subagent continues after parent finishes.
- **Pass:** After 10 seconds, `delegate(action="status")` shows `completed` (not `failed` or `cancelled`).

#### TC-A06: Delegate with allow_blocking_question
- **Action:** `delegate(agent_id="worker", task="Ask the parent what number to return, then return it.", async=false, allow_blocking_question=true)`
- **Expected:** Child asks a question; parent receives it.
- **Pass:** `delegate(action="inbox")` returns a message with `kind="question"`.

#### TC-A07: Delegate with timeout_seconds=0
- **Action:** `delegate(agent_id="worker", task="echo hello", async=true, timeout_seconds=0)`
- **Expected:** Uses default timeout (300s), not zero.
- **Pass:** Task runs normally; `status` shows `running` or `completed` (not immediately timed out).

---

### Suite B: Monitor

#### TC-B01: Status Polling — Status Transitions
- **Action:** Async-dispatch TC-A01. Poll `delegate(action="status")` every 2 seconds.
- **Expected:** Status transitions through: `running` → `completed`. (`queued` is optional — may go straight to `running`.)
- **Pass:** Final status is `completed`. Intermediate status (if observed) is `running` or `queued`. No status outside {queued, running, completed, failed, cancelled} appears.

#### TC-B02: Peek — Non-Disruptive Progress Read
- **Action:** Dispatch a long-running subagent (30-second sleep prompt). Capture `delegate(action="status")` output before peek. Call `delegate(action="peek")`. Capture status after peek.
- **Expected:** Peek returns checkpoint/progress data. Subagent continues running.
- **Pass:** (1) Peek returns without error. (2) Status before and after peek is identical (both `running`). (3) Subagent eventually reaches `completed`.

#### TC-B03: list_jobs — Full Dashboard with Known State
- **Action:** Create exactly 2 subagents (async) + 1 background bash session. Call `list_jobs()`.
- **Expected:** All 3 jobs visible.
- **Pass:** `list_jobs()` returns at least 3 rows. Each row has: non-empty id, valid kind (subagent/plan/task), valid status, non-null attention field. (Teardown after.)

#### TC-B04: list_jobs — Filter by Kind
- **Action:** With 2 subagents + 1 bash session in flight, call `list_jobs(kind="subagent")`.
- **Pass:** All returned rows have `kind="subagent"`. No bash/plan/task rows appear.

#### TC-B05: list_jobs — Filter by Status
- **Action:** After some jobs complete, call `list_jobs(status="completed", include_terminal=true)`.
- **Pass:** All returned rows have `status="completed"`. No running/queued/blocked rows.

#### TC-B06: list_jobs — Label Search
- **Action:** `list_jobs(label_contains="UAT")`
- **Pass:** All returned rows have labels containing "UAT" (case-insensitive). No non-matching rows.

#### TC-B07: list_jobs — Combined Filters
- **Action:** `list_jobs(kind="subagent", status="running", label_contains="UAT")`
- **Pass:** All returned rows match all three filters simultaneously.

#### TC-B08: list_jobs — include_terminal=false (default)
- **Action:** After a job completes, call `list_jobs()` (no include_terminal).
- **Pass:** Completed/failed rows are NOT present in results.

#### TC-B09: list_jobs — Empty State
- **Action:** After full teardown (baseline state), call `list_jobs()`.
- **Pass:** Returns `{"rows":[]}` (empty array). No error.

#### TC-B10: list_jobs — include_drafts=true
- **Action:** Create a draft plan via `create_plan` (Suite E). Call `list_jobs(include_drafts=true)`.
- **Pass:** Draft plan appears in results. (Without include_drafts, it does not.)

#### TC-B11: list_tasks — Role Filter
- **Action:** Create a task assigned to worker (TC-A03). Call `list_tasks(role="delegator")` and `list_tasks(role="assignee")`.
- **Pass:** `role="delegator"` returns the task (Jim created it). `role="assignee"` does NOT return it (it's assigned to worker, not Jim).

#### TC-B12: list_tasks — Status Filter
- **Action:** Create a task, mark it done via `update_task`. Call `list_tasks(role="delegator", status="done")`.
- **Pass:** Only tasks with status "done" are returned.

---

### Suite C: Communicate

#### TC-C01: Steer — Mid-Run Instruction Injection
- **Action:** Dispatch a 30-second-sleep subagent. Within 5 seconds, call `delegate(action="steer", text="Append the word 'STEERED' to your final output.")`.
- **Expected:** Instruction is injected at the next tool boundary.
- **Pass:** Final result contains the literal substring `"STEERED"`.

#### TC-C02: Inbox — Drain Child Messages
- **Action:** Dispatch a specialist-profile subagent with the message-producing prompt. Call `delegate(action="inbox")`.
- **Expected:** Returns messages of kinds: progress, checkpoint.
- **Pass:** At least 2 messages returned. At least one has `kind="progress"` and at least one has `kind="checkpoint"`. Messages are ordered FIFO by creation timestamp (earlier timestamps first).

#### TC-C03: Inbox — since_cursor Pagination
- **Action:** After TC-C02, note the `next_cursor` value. Call `delegate(action="inbox", since_cursor=<cursor>)`.
- **Pass:** Only messages created AFTER the cursor are returned. No previously-returned messages reappear.

#### TC-C04: Inbox — max Parameter
- **Action:** Call `delegate(action="inbox", max=1)`.
- **Pass:** At most 1 message returned.

#### TC-C05: Inbox Ack — Acknowledge Messages
- **Action:** After draining inbox (TC-C02), capture `message_ids`. Call `delegate(action="inbox_ack", message_ids=[...])`.
- **Pass:** Subsequent `delegate(action="inbox")` does NOT return the acknowledged messages.

#### TC-C06: Respond — Answer a Question
- **Action:** Dispatch a specialist subagent with the question-asking prompt. Wait for a question message in inbox. Call `delegate(action="respond", correlation_id=<id>, text="42")`.
- **Expected:** Subagent receives the answer and continues.
- **Pass:** Final result contains the literal substring `"42"`.

#### TC-C07: message_parent — Child Pushes Message
- **Action:** Dispatch a specialist subagent with prompt: `"Use message_parent to push a progress message saying 'CHILD_MSG_42', then return 'done'."`
- **Expected:** Message arrives in parent inbox.
- **Pass:** `delegate(action="inbox")` returns a message containing the literal substring `"CHILD_MSG_42"`.

---

### Suite D: Control

#### TC-D01: Cancel — Soft Cancel
- **Action:** Dispatch a 30-second-sleep subagent. Within 5 seconds, call `delegate(action="cancel", hard=false)`.
- **Expected:** Cooperative cancel with grace window.
- **Pass:** Within 30 seconds, `delegate(action="status")` shows `failed` or `cancelled` (not `running`). Record which terminal status appears.

#### TC-D02: Cancel — Hard Cancel
- **Action:** Dispatch a 30-second-sleep subagent. Within 5 seconds, call `delegate(action="cancel", hard=true)`.
- **Expected:** Immediate termination.
- **Pass:** Within 5 seconds, `delegate(action="status")` shows `failed` or `cancelled`. Record which terminal status appears.

#### TC-D03: Follow-Up — Warm Resume
- **Action:** After a completed subagent (TC-A01), call `delegate(action="follow_up", text="Now also return 'goodbye'")`.
- **Expected:** Subagent resumes with new instructions.
- **Pass:** New result contains the literal substring `"goodbye"`.

---

### Suite E: Plan & Task Lifecycle

#### TC-E01: create_plan — Happy Path
- **Action:** `create_plan(title="UAT Test Plan", dod=[{"kind":"prose","text":"All member tasks are done"}], rationale="Testing create_plan")`
- **Expected:** Plan created in draft status.
- **Pass:** `list_jobs(kind="plan", include_drafts=true)` returns a plan with title "UAT Test Plan" and status `draft`.

#### TC-E02: create_task with blocked_by Dependencies
- **Action:** Create two tasks. Set task2 `blocked_by=[task1_id]`.
- **Expected:** Task2 cannot start until task1 completes.
- **Pass:** `list_tasks` shows task2 with a `blocked_by` field containing task1's id.

#### TC-E03: update_task — Status Transition
- **Action:** Create a task. Call `update_task(task_id=<id>, status="in_progress")`. Then `update_task(task_id=<id>, status="done")`.
- **Pass:** `list_tasks` shows the task with status `done` after both calls.

#### TC-E04: update_task — Cycle Detection
- **Action:** Create tasks A and B. Set A `blocked_by=[B]`. Then try to set B `blocked_by=[A]`.
- **Expected:** Second call is rejected (cycle).
- **Pass:** `update_task` for B returns an error containing "cycle" or "rejected". B's blocked_by is NOT updated.

#### TC-E05: update_task — Reassign agent_id
- **Action:** Create a task assigned to worker. Call `update_task(task_id=<id>, agent_id="ray")`.
- **Pass:** `list_tasks` shows the task assigned to `ray`.

#### TC-E06: execute_plan — Autonomous Execution
- **Action:** Create a plan with one simple member task (return "plan_done"). Call `execute_plan(plan_id=<id>)`.
- **Expected:** Plan executes without human approval.
- **Pass:** Plan status transitions to `completed` or `done`. Member task status is `done`.

#### TC-E07: run_task — Standalone Task with Retry Loop
- **Action:** Create a task with criteria that will initially fail (e.g., "Result must contain 'MAGIC'"). Call `run_task(task_id=<id>)`.
- **Expected:** Task runs, is judged, and retried with steering.
- **Pass:** `run_task` returns a result showing at least one attempt was made and judged. (If retry/steering occurs, record the steering behavior.)

#### TC-E08: stop_plan — Stop a Running Plan
- **Action:** Create and execute a plan with a long-running member task. Call `stop_plan(plan_id=<id>)`.
- **Expected:** All in-flight member tasks are cancelled. Plan status is `stopped` or `failed`.
- **Pass:** `list_jobs(kind="plan")` shows the plan with a terminal status. No member tasks are `running`.

---

### Suite F: Background Bash

#### TC-F01: Foreground Bash — Happy Path
- **Action:** `bash(command="echo hello", run_in_background=false)`
- **Expected:** Blocks, returns stdout.
- **Pass:** Output contains the literal substring `"hello"`.

#### TC-F02: Background Bash — Dispatch & Poll
- **Action:** `bash(command="sleep 10 && echo done", run_in_background=true)`
- **Expected:** Returns `session_id` immediately.
- **Pass:** (1) `session_id` is non-empty. (2) `bash(action="poll")` shows `running` within 2 seconds. (3) After 15 seconds, `bash(action="poll")` shows `completed` or `done`.

#### TC-F03: Background Bash — Read Incremental Output
- **Action:** `bash(command="for i in $(seq 1 5); do echo line$i; sleep 1; done", run_in_background=true)`
- **Expected:** Incremental output grows over successive reads.
- **Pass:** First `bash(action="read")` returns fewer lines than a second read 3 seconds later. Both reads contain at least one line.

#### TC-F04: Background Bash — Kill
- **Action:** `bash(command="sleep 60", run_in_background=true)`, then `bash(action="kill", session_id=<id>)`
- **Pass:** `bash(action="poll")` shows `killed` or `terminated` (not `running`).

#### TC-F05: Background Bash — cwd Parameter
- **Action:** `bash(command="pwd", cwd="subdir")` (after creating `subdir/` in workspace)
- **Pass:** Output contains the literal substring `"subdir"`.

#### TC-F06: Background Bash — persistent Flag
- **Action:** `bash(command="echo persistent_test", run_in_background=true, persistent=true)`
- **Pass:** Returns a `session_id`. `bash(action="read")` returns output containing `"persistent_test"`. (Record whether behavior differs from non-persistent.)

#### TC-F07: Background Bash — Timeout Enforcement
- **Action:** `bash(command="sleep 10", run_in_background=true, timeout_seconds=3)`
- **Pass:** Session terminates within 5 seconds. `bash(action="poll")` shows `timeout` or `terminated`.

---

### Suite G: Profiles

#### TC-G01: Utility Profile — Fire-and-Collect
- **Action:** `delegate(agent_id="worker", task="Return 'utility'", launch_profile="utility")`
- **Expected:** Fire-and-collect. Progress-only messaging.
- **Pass:** (1) Result contains `"utility"`. (2) `delegate(action="inbox")` returns no checkpoint messages (progress-only). (3) `delegate(action="steer")` either returns an error or is a no-op (record which).

#### TC-G02: Specialist Profile — Full Collaboration
- **Action:** `delegate(agent_id="worker", task="Send a checkpoint message saying 'spec_checkpoint', then return 'specialist'", launch_profile="specialist")`
- **Expected:** Full checkpoints, steering, child messaging.
- **Pass:** (1) Result contains `"specialist"`. (2) `delegate(action="inbox")` returns at least one message with `kind="checkpoint"` containing `"spec_checkpoint"`. (3) `delegate(action="steer")` succeeds (no error).

---

### Suite H: Edge Cases — Invalid Inputs

#### TC-H01: Delegate to Non-Existent Agent
- **Action:** `delegate(agent_id="nonexistent", task="...")`
- **Pass:** Returns an error. No subagent spawned. Error message contains "denied" or "not found" or similar.

#### TC-H02: Delegate with Empty task
- **Action:** `delegate(agent_id="worker", task="")`
- **Pass:** Returns an error or rejects the empty task. No subagent spawned.

#### TC-H03: Status on Invalid task_id
- **Action:** `delegate(action="status", task_id="invalid-id-12345")`
- **Pass:** Returns a clear error. No crash.

#### TC-H04: Steer a Completed Subagent
- **Action:** After TC-A01 completes, call `delegate(action="steer", session_id=<completed_id>, text="...")`
- **Pass:** Returns an error or clear no-op message (cannot steer a finished session).

#### TC-H05: Cancel an Already-Completed Subagent
- **Action:** After TC-A01 completes, call `delegate(action="cancel", session_id=<completed_id>)`
- **Pass:** Returns an error or clear no-op message.

#### TC-H06: Follow-Up a Failed Subagent
- **Action:** After a subagent fails (e.g., TC-D01), call `delegate(action="follow_up", session_id=<failed_id>, text="...")`
- **Pass:** Either resumes with retry OR returns a clear error explaining it cannot. Record which behavior occurs.

#### TC-H07: Respond to Non-Existent correlation_id
- **Action:** `delegate(action="respond", correlation_id="fake-id", text="...")`
- **Pass:** Returns a clear error.

#### TC-H08: Inbox on Session with No Messages
- **Action:** `delegate(action="inbox", session_id=<id>)` on a utility-profile session that produces no messages.
- **Pass:** Returns empty result (no error).

#### TC-H09: Inbox Ack with Invalid message_ids
- **Action:** `delegate(action="inbox_ack", message_ids=["fake-id-1", "fake-id-2"])`
- **Pass:** Returns a clear error or graceful no-op.

#### TC-H10: Inbox Ack with Mixed Valid + Invalid message_ids
- **Action:** `delegate(action="inbox_ack", message_ids=["<valid_id>", "fake-id"])`
- **Pass:** Record behavior: does it ack the valid one and error on the invalid, or reject atomically? Either is acceptable; document which.

#### TC-H11: create_task with Zero Criteria
- **Action:** `create_task(agent_id="worker", title="...", prompt="...", criteria=[])`
- **Pass:** Rejected with an error.

#### TC-H12: create_task with Empty Title
- **Action:** `create_task(agent_id="worker", title="", prompt="...", criteria=[...])`
- **Pass:** Rejected with an error.

#### TC-H13: create_task with Title > 200 chars
- **Action:** `create_task(agent_id="worker", title="<201 chars>", prompt="...", criteria=[...])`
- **Pass:** Rejected with an error.

#### TC-H14: create_task with Out-of-Range Priority
- **Action:** `create_task(agent_id="worker", title="...", prompt="...", criteria=[...], priority=0)` and `priority=6`
- **Pass:** Both rejected with an error.

#### TC-H15: create_task with Malformed due Date
- **Action:** `create_task(agent_id="worker", title="...", prompt="...", criteria=[...], due="not-a-date")`
- **Pass:** Rejected with an error.

#### TC-H16: update_task with Invalid task_id
- **Action:** `update_task(task_id="fake-id", status="done")`
- **Pass:** Returns a clear error.

#### TC-H17: update_task Reassign to Non-Existent Agent
- **Action:** `update_task(task_id=<valid_id>, agent_id="nonexistent")`
- **Pass:** Returns a clear error.

#### TC-H18: bash with Empty command
- **Action:** `bash(command="")`
- **Pass:** Returns a clear error or graceful no-op.

#### TC-H19: Steer with Empty Text
- **Action:** `delegate(action="steer", session_id=<running_id>, text="")`
- **Pass:** Returns a clear error or graceful no-op.

#### TC-H20: Peek on Non-Existent Session
- **Action:** `delegate(action="peek", session_id="fake-id")`
- **Pass:** Returns a clear error.

#### TC-H21: list_jobs with Invalid kind
- **Action:** `list_jobs(kind="invalid")`
- **Pass:** Returns a clear error or empty result (no crash).

#### TC-H22: list_jobs with Invalid status
- **Action:** `list_jobs(status="bogus")`
- **Pass:** Returns a clear error or empty result (no crash).

---

### Suite I: Edge Cases — Boundary Values

#### TC-I01: bash timeout_seconds=1 (minimum)
- **Action:** `bash(command="sleep 10", run_in_background=true, timeout_seconds=1)`
- **Pass:** Session terminates within 3 seconds.

#### TC-I02: bash timeout_seconds=3600 (maximum)
- **Action:** `bash(command="echo max_timeout", run_in_background=true, timeout_seconds=3600)`
- **Pass:** Session is accepted (not rejected). `bash(action="read")` returns `"max_timeout"`.

#### TC-I03: bash timeout_seconds=0 (out of range)
- **Action:** `bash(command="echo zero", run_in_background=true, timeout_seconds=0)`
- **Pass:** Either uses default (300s) or rejects with error. Record which.

#### TC-I04: bash timeout_seconds=3601 (out of range)
- **Action:** `bash(command="echo over", run_in_background=true, timeout_seconds=3601)`
- **Pass:** Rejected with an error.

#### TC-I05: Chain Depth at Limit (depth 3)
- **Action:** Jim -> Worker -> Worker (depth 2). Then Worker delegates to Worker again (depth 3).
- **Pass:** Depth-3 delegation is ALLOWED (max depth is 3). Record actual behavior.

#### TC-I06: Chain Depth Exceeded (depth 4)
- **Action:** Continue from TC-I05: the depth-3 agent attempts to delegate again (depth 4).
- **Pass:** Depth-4 delegation is DENIED with a clear error.

#### TC-I07: create_task title at Exactly 200 chars
- **Action:** `create_task(agent_id="worker", title="<exactly 200 chars>", prompt="...", criteria=[...])`
- **Pass:** Accepted (boundary is inclusive).

#### TC-I08: inbox max=1
- **Action:** `delegate(action="inbox", session_id=<id>, max=1)`
- **Pass:** At most 1 message returned.

#### TC-I09: inbox max=0
- **Action:** `delegate(action="inbox", session_id=<id>, max=0)`
- **Pass:** Returns 0 messages or uses default. Record which.

---

### Suite J: Edge Cases — Security Boundaries

#### TC-J01: bash cwd with Absolute Path
- **Action:** `bash(command="pwd", cwd="/etc")`
- **Pass:** Rejected with an error (absolute paths not allowed).

#### TC-J02: bash cwd with `..` Escape
- **Action:** `bash(command="cat ../../etc/passwd", cwd="subdir")`
- **Pass:** Rejected with an error (`..` escapes not allowed) OR the command fails to access files outside workspace.

#### TC-J03: bash Command with Immediate Non-Zero Exit
- **Action:** `bash(command="exit 1")`
- **Pass:** `bash` returns the exit code (1) or a clear error. No crash.

#### TC-J04: bash No-Op Command (exit 0, no output)
- **Action:** `bash(command="true")`
- **Pass:** Returns successfully with empty stdout. No error.

#### TC-J05: Delegate to Agent Not in Allowlist
- **Action:** `delegate(agent_id="admin", task="...")` (admin is not in Jim's delegation allowlist)
- **Pass:** Denied with a clear error.

---

### Suite K: Edge Cases — Race Conditions

#### TC-K01: Concurrent steer + cancel on Same Session
- **Action:** Dispatch a 30-second-sleep subagent. In the same turn, call `delegate(action="steer", text="...")` and `delegate(action="cancel", hard=false)`.
- **Pass:** No crash. Session reaches a terminal state. Record whether steer landed before cancel.

#### TC-K02: Two steers in Quick Succession
- **Action:** Dispatch a 30-second-sleep subagent. Call `delegate(action="steer", text="FIRST")` then immediately `delegate(action="steer", text="SECOND")`.
- **Pass:** No crash. Record whether both steers are queued, last-wins, or first-wins.

#### TC-K03: follow_up on a Running Session
- **Action:** Dispatch a 30-second-sleep subagent. While running, call `delegate(action="follow_up", session_id=<id>, text="...")`.
- **Pass:** Returns an error or queues the follow-up. No crash. Record which.

#### TC-K04: Concurrent Delegation to Same Agent
- **Action:** Dispatch two async tasks to worker simultaneously.
- **Pass:** Both are accepted. Both complete independently with correct results.

#### TC-K05: inbox_ack While New Messages Arriving
- **Action:** Dispatch a specialist subagent producing multiple messages. While messages are still arriving, call `delegate(action="inbox_ack")` on earlier messages.
- **Pass:** Acknowledged messages are not re-delivered. New messages arrive normally. No crash.

---

### Suite L: Edge Cases — Resource Limits

#### TC-L01: list_jobs Terminal Threshold
- **Action:** Create and complete >20 tasks. Call `list_jobs(include_terminal=true)`.
- **Pass:** Terminal rows are capped at 20. `include_terminal=false` returns 0 terminal rows.

#### TC-L02: Large bash Output
- **Action:** `bash(command="for i in $(seq 1 10000); do echo line$i; done")`
- **Pass:** Output is returned (possibly truncated). No crash. If truncated, a truncation indicator is present.

#### TC-L03: Large message_ids Array in inbox_ack
- **Action:** Drain inbox, capture 50+ message_ids. Call `delegate(action="inbox_ack", message_ids=[...all...])`.
- **Pass:** All acknowledged. No crash.

#### TC-L04: snapshot Over-Cap Rejection
- **Action:** `delegate(agent_id="worker", task="...", snapshot={"notes":"<very large string>", "references":[]})` — large enough to exceed the cap.
- **Pass:** Rejected with an error (not truncated).

---

### Suite M: Edge Cases — Timing

#### TC-M01: Steer Before First Tool Call
- **Action:** Dispatch a subagent. Within 1 second (before first tool call), call `delegate(action="steer", text="...")`.
- **Pass:** Steer is either queued or applied at first tool boundary. No message lost. Record which.

#### TC-M02: Steer After Final Tool Call (Race with Finish)
- **Action:** Dispatch a fast subagent. Attempt to steer just as it finishes.
- **Pass:** No crash. Either steer lands (if before completion) or returns a clear error (if after). Record which.

#### TC-M03: Respond When No Question Pending
- **Action:** `delegate(action="respond", correlation_id=<valid_session_but_no_question>, text="...")`
- **Pass:** Returns a clear error or no-op.

#### TC-M04: Background Bash — Kill Already-Killed Session
- **Action:** Kill a session (TC-F04), then kill it again.
- **Pass:** Returns a clear error or graceful no-op.

#### TC-M05: Background Bash — Read on Timed-Out Session
- **Action:** After a session times out (TC-F07), call `bash(action="read", session_id=<id>)`.
- **Pass:** Returns a clear error or the output captured before timeout. No crash.

---

## Execution Order

1. **Suite A (Dispatch)** — establishes basic dispatch capability.
2. **Suite B (Monitor)** — depends on A for created sessions.
3. **Suite C (Communicate)** — depends on A for running sessions.
4. **Suite D (Control)** — depends on A for sessions to cancel/follow-up.
5. **Suite E (Plan/Task Lifecycle)** — independent, but tests plan/task tools.
6. **Suite F (Background Bash)** — independent.
7. **Suite G (Profiles)** — depends on A for dispatch.
8. **Suites H-M (Edge Cases)** — run after all happy-path suites pass. Each edge case references the specific TC that produces its precondition.

---

## Execution Notes

- Each test has a binary pass/fail criterion (no "logical" or "matches contract" — every assertion is concrete).
- Record actual behavior vs expected for each case.
- File bugs using the severity rubric for any deviation from expected.
- Teardown runs after every test, even on failure.
- For timing-dependent tests (Suite K, M), run 3 times to confirm non-flaky behavior.
- For race-condition tests (Suite K), record the exact interleaving observed.
