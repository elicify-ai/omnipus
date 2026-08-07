# UAT Plan v2: Delegation, Monitoring & Communication Tools (Revised)

**Goal:** Systematically test every delegation, monitoring, steering, communication, control, plan/task-lifecycle, and bash tool available to the Orchestrator (Jim), including edge cases and failure modes, to verify correct behavior and surface bugs.

**Tester:** Jim (Orchestrator) — first user / UAT user
**Date:** 2026-07-31
**Revision:** v2 — incorporates findings from two independent critical reviews (Ray + Worker). See CHANGELOG at bottom.

---

## Scope

Tools in scope (the full orchestration lifecycle):

| Category | Tools / Actions |
|----------|----------------|
| Dispatch | `delegate(action="run", async=true)`, `delegate(action="run", async=false)`, `create_task` |
| Monitor | `delegate(action="status")`, `delegate(action="status")` with no id (list all), `delegate(action="peek")`, `list_jobs`, `list_tasks` |
| Communicate | `delegate(action="steer")`, `delegate(action="respond")`, `delegate(action="inbox")`, `delegate(action="inbox_ack")`, `message_parent` (child→parent) |
| Control | `delegate(action="cancel", hard=false)`, `delegate(action="cancel", hard=true)`, `delegate(action="follow_up")` |
| Plan/Task Lifecycle | `create_plan`, `execute_plan`, `stop_plan`, `run_task`, `update_task` |
| Background Bash | `bash(run_in_background=true)`, `bash(action="poll")`, `bash(action="read")`, `bash(action="kill")`, foreground `bash` |
| Profiles | `launch_profile="utility"`, `launch_profile="specialist"` |
| Delegate Params | `snapshot`, `critical`, `allow_blocking_question`, `timeout_seconds` |
| Inbox Params | `since_cursor`, `max` |

---

## Pre-Suite: Environment & Setup Protocol

### ENV-01: Baseline State
- Call `list_jobs()` with no filters. Record the count of existing jobs (baseline).
- All subsequent `list_jobs` assertions must account for this baseline.
- If baseline is non-zero, tests must use delta assertions (count after - count before = expected delta), not absolute counts.

### ENV-02: Controlled Long-Running Task Prompt
- Define a standard "long-running" task prompt that guarantees the subagent stays alive for at least 60 seconds:
  `task="Run this shell command and return its output: sleep 45 && echo LONG_RUN_COMPLETE"`
- This prompt is used by all tests requiring a subagent to still be running when a control action (steer, cancel, peek) is fired.

### ENV-03: Message-Producing Task Prompt (Specialist Profile)
- Define a task prompt that causes the specialist-profile subagent to emit progress/checkpoint messages:
  `task="Count from 1 to 5, sending a progress message to your parent after each number. Then return 'DONE'."`
  `launch_profile="specialist"`

### ENV-04: Question-Asking Task Prompt (Specialist Profile)
- Define a task prompt that causes the subagent to ask its parent a question:
  `task="Ask your parent 'What is the magic number?' using message_parent. Wait for the answer. Then return the answer you received."`
  `launch_profile="specialist"`

### ENV-05: Teardown Protocol
- After each test (or at minimum after each suite), clean up:
  - Cancel any still-running subagents (`delegate(action="cancel", hard=true)`).
  - Kill any background bash sessions (`bash(action="kill")`).
  - Delete test tasks/plans created during the suite (if delete is available; otherwise mark done).
- Teardown runs even if the test failed, to prevent state pollution.

### ENV-06: Bug Severity Rubric
- **P0 (Blocker):** Tool crashes, data loss, security boundary bypass, infinite loop/hang.
- **P1 (Critical):** Wrong result returned, status transition broken, message lost, cancel doesn't cancel.
- **P2 (Major):** Error message unclear, edge case not handled gracefully, flaky behavior.
- **P3 (Minor):** Cosmetic issue, latency, documentation mismatch.

---

## Suite A: Dispatch

### TC-01: Async Dispatch — Happy Path
- **Setup:** Clean state (baseline recorded).
- **Action:** `delegate(agent_id="worker", task="Return the exact string 'hello'", async=true)`
- **Expected:** Returns a `task_id` and `session_id` immediately. Does not block.
- **Pass:** `task_id` is a non-empty string. `delegate(action="status", session_id=<id>)` eventually shows `status=completed` with `result` containing "hello".
- **Teardown:** None (session is terminal).

### TC-02: Sync Dispatch — Happy Path
- **Action:** `delegate(agent_id="worker", task="Return the exact string 'hello'", async=false)`
- **Expected:** Blocks until completion, returns result inline.
- **Pass:** Result contains the literal substring "hello".

### TC-03: create_task — Durable Task with Real Criteria
- **Action:** `create_task(agent_id="worker", title="UAT durable task", prompt="Return the string 'created'", criteria=[{kind: "prose", text: "Result contains the word 'created'"}])`
- **Expected:** Task created, appears in `list_tasks(role="delegator")` and `list_jobs(kind="task")`.
- **Pass:** `list_tasks(role="delegator")` returns a row with title "UAT durable task". `list_jobs(kind="task")` includes its id. Status is `inbox` or `next` (not `done` or `failed`).
- **Teardown:** Mark task done or delete after suite.

### TC-03b: create_task — Rich Parameters
- **Action:** `create_task(agent_id="worker", title="UAT rich task", prompt="Return 'rich'", criteria=[{kind:"prose", text:"Result contains 'rich'"}], priority=2, due="2026-08-15T00:00:00Z", blocked_by=[], stream="uat-stream", write_set=["uat-output.txt"], is_join=false)`
- **Pass:** Task appears in `list_tasks` with correct priority, due date, stream, and write_set. Verify via `list_tasks` or `list_jobs`.
- **Teardown:** Clean up task.

### TC-03c: create_task with Zero Criteria — Rejection
- **Action:** `create_task(agent_id="worker", title="No criteria", prompt="...", criteria=[])`
- **Pass:** Rejected with a clear error message. No task created.

### TC-03d: create_task — Title Boundary (200 chars)
- **Action:** `create_task` with title of exactly 200 characters.
- **Pass:** Accepted. Then `create_task` with 201-char title.
- **Pass:** Rejected with clear error.

### TC-03e: create_task — Invalid Priority
- **Action:** `create_task` with `priority=0` and `priority=6`.
- **Pass:** Both rejected (valid range is 1-5).

### TC-03f: create_task — Malformed Due Date
- **Action:** `create_task` with `due="not-a-date"`.
- **Pass:** Rejected with clear error.

---

## Suite B: Monitoring

### TC-04: Status Polling — Explicit Transitions
- **Setup:** Dispatch async subagent (TC-01 pattern). Record `session_id`.
- **Action:** Poll `delegate(action="status", session_id=<id>)` at 5-second intervals.
- **Expected transitions:** `running` → `completed`. The `queued` state may or may not be observed (it may be transient). 
- **Pass:** Final status is `completed`. No backward transitions (e.g., `completed` → `running`). If `queued` is observed, it must transition to `running` before `completed`.
- **Fail:** Any backward transition, or terminal state other than `completed`.

### TC-05: Peek — Non-Disruptive with State Comparison
- **Setup:** Dispatch long-running subagent (ENV-02). Wait 3 seconds.
- **Action:** 
  1. Capture state: `delegate(action="status", session_id=<id>)` → record status and any checkpoint.
  2. `delegate(action="peek", session_id=<id>)` → record result.
  3. Capture state again: `delegate(action="status", session_id=<id>)`.
- **Pass:** 
  - Peek returns a result (not an error).
  - Status before peek == status after peek (both `running`).
  - Subagent eventually completes normally (status → `completed`).
- **Teardown:** Cancel subagent if still running.

### TC-06: list_jobs — Known-Count Dashboard
- **Setup:** Create exactly 3 subagents (async, long-running ENV-02) + 1 task (TC-03 pattern). Record all ids.
- **Action:** `list_jobs()` with no filters.
- **Pass:** All 4 ids appear in the result. Count of non-terminal rows = baseline + 4 (per ENV-01 delta assertion). Each row has `status`, `attention`, `actionable`, `notes` fields.
- **Teardown:** Cancel all 3 subagents. Delete/clean the task.

### TC-07: list_jobs — Filter by Kind
- **Action:** `list_jobs(kind="subagent")`, then `list_jobs(kind="task")`, then `list_jobs(kind="plan")`.
- **Pass:** Each call returns ONLY rows of the specified kind. No cross-contamination.

### TC-08: list_jobs — Filter by Status
- **Setup:** After TC-06 teardown, some subagents are terminal (cancelled).
- **Action:** `list_jobs(status="running")` — should exclude terminal. `list_jobs(status="failed", include_terminal=true)` — should include failed/cancelled.
- **Pass:** Terminal rows only appear when `include_terminal=true`. Running filter excludes non-running.

### TC-09: list_jobs — Label Search
- **Setup:** Create a task with title containing "UAT_LABEL_TEST".
- **Action:** `list_jobs(label_contains="UAT_LABEL")`.
- **Pass:** Only rows whose label contains "UAT_LABEL" (case-insensitive) are returned.
- **Teardown:** Clean up task.

### TC-09b: list_jobs — Combined Filters
- **Action:** `list_jobs(kind="subagent", status="running", label_contains="LONG")`.
- **Pass:** Results match ALL three filter criteria simultaneously.

### TC-09c: list_jobs — Empty State
- **Action:** In a clean workspace (or after full teardown), `list_jobs()`.
- **Pass:** Returns `rows: []` (empty array), no error.

### TC-09d: list_jobs — include_terminal=false (default)
- **Action:** After some jobs complete, `list_jobs()` without `include_terminal`.
- **Pass:** Completed/failed rows excluded from results.

### TC-09e: list_jobs — include_drafts=true
- **Setup:** Create a draft plan (see Suite E).
- **Action:** `list_jobs(include_drafts=true)`.
- **Pass:** Draft plan visible. Then `list_jobs(include_drafts=false)` — draft excluded.

### TC-10: list_tasks — Role and Status Filters
- **Action:** `list_tasks(role="assignee")` and `list_tasks(role="delegator")`.
- **Pass:** `assignee` returns tasks assigned to me. `delegator` returns tasks I created for others. No overlap (a task I created for myself appears in both, which is valid). Verify the `truncated` and `matched` fields are present.

---

## Suite C: Communication

### TC-11: Inbox — Drain Child Messages (Specialist Profile)
- **Setup:** Dispatch specialist-profile subagent with ENV-03 prompt. Wait for messages to accumulate.
- **Action:** `delegate(action="inbox", session_id=<id>)`.
- **Expected:** Returns messages with `kind` field (progress, checkpoint, artifact, blocker, question, handback).
- **Pass:** At least 1 message returned. Each message has a `kind`, a message body, and a `message_id`. Messages are in chronological order (oldest first) — verify by checking that timestamps or sequence numbers are monotonically increasing.
- **Teardown:** Cancel subagent after test.

### TC-11b: Inbox — max Parameter
- **Action:** `delegate(action="inbox", session_id=<id>, max=1)`.
- **Pass:** Returns at most 1 message regardless of how many are queued.

### TC-11c: Inbox — since_cursor (Incremental Drain)
- **Action:** 
  1. `delegate(action="inbox", session_id=<id>)` → record `next_cursor`.
  2. Wait for more messages.
  3. `delegate(action="inbox", session_id=<id>, since_cursor=<next_cursor>)`.
- **Pass:** Second drain returns only messages created after the first drain. No duplicates from first drain.

### TC-11d: Inbox — Empty (No Messages)
- **Setup:** Dispatch a utility-profile subagent (no child messaging).
- **Action:** `delegate(action="inbox", session_id=<id>)`.
- **Pass:** Returns `messages: null` or `messages: []`, no error.

### TC-12: Inbox Ack — Acknowledge Messages
- **Setup:** Drain inbox (TC-11), capture `message_id`s.
- **Action:** `delegate(action="inbox_ack", message_ids=[<id1>, <id2>])`.
- **Pass:** Acknowledged. Subsequent `delegate(action="inbox", session_id=<id>)` does NOT return the acknowledged messages.

### TC-12b: Inbox Ack — Invalid message_ids
- **Action:** `delegate(action="inbox_ack", message_ids=["fake-id-12345"])`.
- **Pass:** Clear error or graceful no-op. No crash.

### TC-12c: Inbox Ack — Mixed Valid + Invalid
- **Action:** `delegate(action="inbox_ack", message_ids=[<real_id>, "fake-id"])`.
- **Pass:** Document actual behavior (acks valid + errors on invalid, or rejects atomically). Either is acceptable if documented.

### TC-13: Respond — Answer a Question
- **Setup:** Dispatch specialist subagent with ENV-04 prompt. Wait for a question message in inbox.
- **Action:** `delegate(action="respond", correlation_id=<question_corr_id>, text="42")`.
- **Pass:** Subagent receives the answer. Subagent's final result contains "42".
- **Teardown:** None (subagent completes after receiving answer).

### TC-13b: Respond — Invalid correlation_id
- **Action:** `delegate(action="respond", correlation_id="fake-corr-id", text="...")`.
- **Pass:** Clear error, no crash.

### TC-13c: Respond — No Question Pending
- **Setup:** Dispatch a subagent that does NOT ask a question. Wait for completion.
- **Action:** `delegate(action="respond", correlation_id=<session_id>, text="...")`.
- **Pass:** Clear error ("no open question" or similar), no side effects.

---

## Suite D: Control

### TC-14: Steer — Mid-Run Instruction Injection
- **Setup:** Dispatch long-running subagent (ENV-02). Wait 3 seconds (ensure running).
- **Action:** `delegate(action="steer", session_id=<id>, text="Before finishing, also output the word 'STEERED'")`.
- **Pass:** Subagent's final result contains the literal substring "STEERED".
- **Teardown:** None (subagent completes).

### TC-14b: Steer — Empty Text
- **Action:** `delegate(action="steer", session_id=<id>, text="")`.
- **Pass:** Clear error (empty instruction rejected) or documented graceful no-op.

### TC-14c: Steer — Before First Tool Call
- **Setup:** Dispatch subagent. Immediately (within 1 second) call steer.
- **Pass:** Document actual behavior (queued and applied, or error). Either is acceptable if documented.

### TC-14d: Steer — On Completed Session
- **Setup:** Wait for subagent to complete.
- **Action:** `delegate(action="steer", session_id=<id>, text="...")`.
- **Pass:** Clear error ("cannot steer a finished session" or similar).

### TC-15: Cancel — Soft Cancel
- **Setup:** Dispatch long-running subagent (ENV-02). Wait 3 seconds.
- **Action:** `delegate(action="cancel", session_id=<id>, hard=false)`.
- **Pass:** Status transitions to `failed` or `cancelled` (document which). Grace window observed (status doesn't change instantly — may take a few seconds). Poll until terminal.
- **Teardown:** Confirm session is terminal.

### TC-15b: Cancel — Hard Cancel
- **Setup:** Dispatch long-running subagent. Wait 3 seconds.
- **Action:** `delegate(action="cancel", session_id=<id>, hard=true)`.
- **Pass:** Status transitions to `failed` or `cancelled` immediately (within 1-2 seconds). No grace window.

### TC-15c: Cancel — Already-Completed Session
- **Setup:** Wait for subagent to complete.
- **Action:** `delegate(action="cancel", session_id=<id>)`.
- **Pass:** Clear error or graceful no-op ("session already terminal").

### TC-16: Follow-Up — Warm Resume
- **Setup:** Complete a subagent (TC-01). Record `session_id`.
- **Action:** `delegate(action="follow_up", session_id=<id>, text="Now also return the string 'goodbye'")`.
- **Pass:** New result contains the literal substring "goodbye". Session was resumed, not re-spawned (same `session_id`).

### TC-16b: Follow-Up — On Failed Session
- **Setup:** Cancel a subagent (TC-15). Record `session_id`.
- **Action:** `delegate(action="follow_up", session_id=<id>, text="Try again: return 'hello'")`.
- **Pass:** Document actual behavior (resumes with retry, or clear error explaining it cannot). Either is acceptable if documented.

### TC-16c: Follow-Up — On Running Session
- **Setup:** Dispatch long-running subagent. While still running:
- **Action:** `delegate(action="follow_up", session_id=<id>, text="...")`.
- **Pass:** Document actual behavior (error "session still running", or queues). Either is acceptable if documented.

---

## Suite E: Plan/Task Lifecycle

### TC-17: create_plan — Happy Path
- **Action:** `create_plan(title="UAT Test Plan", dod=[{kind:"prose", text:"All member tasks completed"}], rationale="Testing plan creation")`
- **Pass:** Plan created with an id. `list_jobs(kind="plan", include_drafts=true)` includes it. Status is `draft`.

### TC-18: create_task with Dependencies (blocked_by)
- **Setup:** Create a plan (TC-17). Create two tasks: Task A and Task B.
- **Action:** `create_task(agent_id="worker", title="Task B", prompt="Return 'B'", criteria=[...], blocked_by=[<task_a_id>], plan_id=<plan_id>)`
- **Pass:** Task B appears in `list_jobs` with `blocked` status (or equivalent). Task B cannot be picked up until Task A completes.

### TC-19: execute_plan — Autonomous Execution
- **Setup:** Create a plan with 2 simple member tasks (no dependencies between them).
- **Action:** `execute_plan(plan_id=<plan_id>)`.
- **Pass:** Plan status transitions from `draft` to `running` (or equivalent). Member tasks are dispatched. Eventually all member tasks complete. Plan status → `completed` or `done`.

### TC-20: stop_plan — Cancel In-Flight Plan
- **Setup:** Create and execute a plan with long-running member tasks.
- **Action:** `stop_plan(plan_id=<plan_id>)` while plan is running.
- **Pass:** All in-flight member tasks cancelled. Plan status → `stopped` or `failed`. No member tasks remain `running`.

### TC-21: run_task — Standalone Task with Retry/Steering Loop
- **Setup:** Create a task assigned to worker with criteria that will initially fail (e.g., "Result must contain 'MAGIC'").
- **Action:** `run_task(task_id=<task_id>)`.
- **Expected:** Task runs, is judged, fails criteria, retries with steering.
- **Pass:** At least 1 retry attempt occurs. Task eventually reaches `done` or `failed` terminal state. The attempt loop (run → judge → retry) is observable in status history.

### TC-22: update_task — Status Mutation
- **Setup:** Create a task (TC-03 pattern).
- **Action:** `update_task(task_id=<id>, status="in_progress")` then `update_task(task_id=<id>, status="done", result="Manually completed")`.
- **Pass:** `list_tasks` or `list_jobs` reflects the updated status and result after each call.

### TC-22b: update_task — Reassign Agent
- **Action:** `update_task(task_id=<id>, agent_id="ray")`.
- **Pass:** Task's `agent_id` field updated to "ray".

### TC-22c: update_task — Invalid task_id
- **Action:** `update_task(task_id="fake-id", status="done")`.
- **Pass:** Clear error, no crash.

### TC-22d: update_task — blocked_by Cycle Detection
- **Setup:** Create tasks A, B, C where B is blocked_by A.
- **Action:** `update_task(task_id=<id_a>, blocked_by=[<id_b>])` (creates A→B→A cycle).
- **Pass:** Rejected with clear error ("cycle detected" or similar).

### TC-22e: update_task — blocked_by Replacement Semantics
- **Setup:** Task C blocked_by [A, B].
- **Action:** `update_task(task_id=<id_c>, blocked_by=[<id_d>])`.
- **Pass:** blocked_by is REPLACED (now only [D], not [A, B, D]). Verify via list_tasks.

---

## Suite F: Background Bash

### TC-23: Foreground Bash — Happy Path
- **Action:** `bash(command="echo 'hello bash'")` (no background flag).
- **Pass:** Blocks until completion. stdout contains "hello bash". Exit code 0.

### TC-24: Background Bash — Dispatch & Poll
- **Action:** `bash(command="sleep 10 && echo done", run_in_background=true)`.
- **Pass:** Returns `session_id` immediately. `bash(action="poll", session_id=<id>)` shows `running`, then after ~10s shows `completed`.

### TC-25: Background Bash — Read Incremental Output
- **Action:** `bash(command="for i in $(seq 1 5); do echo line$i; sleep 1; done", run_in_background=true)`.
- **Pass:** `bash(action="read", session_id=<id>)` called at t=2s returns "line1\nline2". Called at t=5s returns all 5 lines. Output grows monotonically.

### TC-26: Background Bash — Kill
- **Action:** `bash(command="sleep 60", run_in_background=true)`, then `bash(action="kill", session_id=<id>)`.
- **Pass:** Session terminates. `bash(action="poll", session_id=<id>)` shows killed/terminated.

### TC-26b: Background Bash — Kill Already-Killed
- **Action:** Kill a session (TC-26), then `bash(action="kill", session_id=<id>)` again.
- **Pass:** Clear error or graceful no-op.

### TC-27: Background Bash — Timeout Enforcement
- **Action:** `bash(command="sleep 300", run_in_background=true, timeout_seconds=5)`.
- **Pass:** Session times out after ~5 seconds. Poll shows timeout/terminated. Subsequent `bash(action="read")` on the timed-out session returns a clear error or empty result (document which).

### TC-28: Bash — cwd Parameter
- **Action:** `bash(command="pwd", cwd="")` (workspace root) and `bash(command="pwd", cwd="subdir")`.
- **Pass:** First returns the workspace root path. Second returns `<root>/subdir`.

### TC-28b: Bash — cwd Path Escape Rejection
- **Action:** `bash(command="echo test", cwd="../../etc")` and `bash(command="echo test", cwd="/etc")`.
- **Pass:** Both rejected with clear error (no `..` escapes, no absolute paths).

### TC-29: Bash — Timeout Boundary Values
- **Action:** `bash(command="echo ok", timeout_seconds=1)` (minimum) and attempt `bash(command="echo ok", timeout_seconds=3601)` (over max).
- **Pass:** timeout=1 accepted and executes. timeout=3601 rejected with clear error.

### TC-29b: Bash — Invalid Timeout Values
- **Action:** `bash(command="echo ok", timeout_seconds=0)` and `bash(command="echo ok", timeout_seconds=-1)`.
- **Pass:** Both rejected (valid range is 1-3600). Document whether 0 means "default" or is rejected.

### TC-30: Bash — Immediate Exit (No Output)
- **Action:** `bash(command="true", run_in_background=true)` (exits immediately, code 0, no output).
- **Pass:** Poll shows `completed` quickly. Read returns empty string or null, no error.

### TC-30b: Bash — Immediate Non-Zero Exit
- **Action:** `bash(command="false", run_in_background=true)` (exits immediately, code 1).
- **Pass:** Poll shows `completed` (or `failed`). Read returns empty output. Exit code 1 recorded.

---

## Suite G: Profiles & Delegate Parameters

### TC-31: Utility Profile — Concrete Assertions
- **Setup:** `delegate(agent_id="worker", task="Return 'util'", launch_profile="utility")`.
- **Pass:** 
  - Result returned (fire-and-collect).
  - `delegate(action="inbox", session_id=<id>)` returns no checkpoint messages (empty/null).
  - `delegate(action="steer", session_id=<id>, text="...")` returns error or no-op (steering not supported on utility profile — document actual behavior).

### TC-32: Specialist Profile — Concrete Assertions
- **Setup:** `delegate(agent_id="worker", task=ENV-03 prompt, launch_profile="specialist")`.
- **Pass:**
  - `delegate(action="inbox", session_id=<id>)` returns at least 1 checkpoint/progress message.
  - `delegate(action="steer", session_id=<id>, text="...")` succeeds (no error).
  - `delegate(action="peek", session_id=<id>)` returns checkpoint data.

### TC-33: delegate — snapshot Parameter
- **Action:** `delegate(agent_id="worker", task="What context notes were you given? Quote them.", snapshot={notes:"TEST_SNAPSHOT_NOTE", references:["uat-plan.md"]})`.
- **Pass:** Subagent's result contains "TEST_SNAPSHOT_NOTE" (confirming the snapshot was delivered).

### TC-33b: delegate — snapshot Over-Cap Rejection
- **Action:** `delegate(agent_id="worker", task="...", snapshot={notes: <very long string exceeding cap>, references:[]})`.
- **Pass:** Rejected with clear error (over-cap rejected, not truncated).

### TC-34: delegate — critical Flag
- **Action:** `delegate(agent_id="worker", task="sleep 30 && echo CRITICAL_DONE", critical=true, async=true)`.
- **Pass:** Subagent continues running even after the parent (Jim) finishes the current turn. Verify by polling status in a subsequent turn — subagent should still be `running` or `completed`, not `cancelled`.

### TC-35: delegate — allow_blocking_question
- **Action:** `delegate(agent_id="worker", task=ENV-04 prompt, async=false, allow_blocking_question=true)`.
- **Pass:** Subagent asks a question. Parent receives the question (via inbox or blocking wait). Parent responds. Subagent completes with the answer.

### TC-36: delegate — timeout_seconds=0 (Default)
- **Action:** `delegate(agent_id="worker", task="echo 'timeout test'", timeout_seconds=0, async=true)`.
- **Pass:** Uses default timeout (5 min), not zero. Subagent completes normally.

### TC-37: delegate — task_id and session_id Collision
- **Action:** `delegate(action="status", task_id=<tid>, session_id=<sid>)` where both refer to the same task.
- **Pass:** `session_id` wins (per spec). No error. Result is consistent with using session_id alone.

### TC-38: delegate(action="status") — No ID (List All)
- **Action:** `delegate(action="status")` with no `task_id` or `session_id`.
- **Pass:** Returns a list of all visible tasks (distinct from filtered status). No error.

---

## Suite H: Edge Cases & Error Handling

### EC-01: Delegate to Non-Existent Agent
- **Action:** `delegate(agent_id="nonexistent", task="...")`.
- **Pass:** Denied with clear error. No subagent spawned.

### EC-02: Delegate to Allowlist-Denied Agent
- **Action:** `delegate(agent_id="ava", task="...")` — wait, Ava IS in the allowlist. Use an agent that exists in the system but is NOT in Jim's delegation allowlist.
- **Pass:** Denied with clear error distinguishing "not in allowlist" from "does not exist" (if the system makes this distinction).

### EC-03: Status on Invalid session_id
- **Action:** `delegate(action="status", session_id="invalid-id-99999")`.
- **Pass:** Clear error, no crash.

### EC-04: Peek on Non-Existent Session
- **Action:** `delegate(action="peek", session_id="fake-id")`.
- **Pass:** Clear error.

### EC-05: Empty Task String
- **Action:** `delegate(agent_id="worker", task="")`.
- **Pass:** Clear error (empty task rejected) or documented behavior.

### EC-06: Empty agent_id
- **Action:** `delegate(agent_id="", task="...")`.
- **Pass:** Clear error.

### EC-07: Bash Empty Command
- **Action:** `bash(command="")`.
- **Pass:** Clear error or documented no-op behavior.

### EC-08: Chain Depth Limit
- **Precondition:** Verify the actual configured max chain depth (documented as 3).
- **Action:** Delegate to Worker. Worker delegates to Worker. Worker delegates to Worker (depth 3). Attempt depth 4.
- **Pass:** Depth 3 (at limit) is allowed. Depth 4 (exceeds limit) is denied with clear error.
- **Note:** This test requires Worker to have delegation capability. If Worker cannot delegate, document this as an environmental limitation and skip.

### EC-09: Concurrent Delegation to Same Agent
- **Action:** Dispatch two async tasks to `worker` simultaneously (same turn, parallel calls).
- **Pass:** Both accepted and queued. Both complete independently. No collision in results.

### EC-10: Concurrent Steer + Cancel (Race Condition)
- **Setup:** Dispatch long-running subagent. Wait 3 seconds.
- **Action:** In the same turn (parallel calls): `delegate(action="steer", session_id=<id>, text="...")` AND `delegate(action="cancel", session_id=<id>, hard=true)`.
- **Pass:** No crash. Session reaches a terminal state. Document which action "won" (steer applied before cancel, or cancel pre-empted steer). Either outcome is acceptable if documented and no panic/crash.

### EC-11: Two Steers in Quick Succession
- **Setup:** Dispatch long-running subagent. Wait 3 seconds.
- **Action:** `delegate(action="steer", session_id=<id>, text="Output 'FIRST'")` then immediately `delegate(action="steer", session_id=<id>, text="Output 'SECOND'")`.
- **Pass:** Document actual behavior (last-wins, both queued, or error on second). No crash. Subagent reaches terminal state.

### EC-12: list_jobs with Invalid kind/status
- **Action:** `list_jobs(kind="invalid")` and `list_jobs(status="bogus")`.
- **Pass:** Clear error or empty result (document which).

---

## Execution Order

Tests are grouped into independent suites. Within each suite, tests may have dependencies (declared in Setup). Suites can run in any order, but Suite A (Dispatch) should run first as it establishes basic patterns.

**Recommended order:**
1. Pre-Suite (ENV-01 through ENV-06)
2. Suite A: Dispatch (TC-01 through TC-03f)
3. Suite B: Monitoring (TC-04 through TC-10)
4. Suite C: Communication (TC-11 through TC-13c)
5. Suite D: Control (TC-14 through TC-16c)
6. Suite E: Plan/Task Lifecycle (TC-17 through TC-22e)
7. Suite F: Background Bash (TC-23 through TC-30b)
8. Suite G: Profiles & Params (TC-31 through TC-38)
9. Suite H: Edge Cases (EC-01 through EC-12)

**Teardown runs after each suite.**

---

## CHANGELOG (v1 → v2)

### Added (based on reviewer findings)

**New test suites:**
- Suite E: Plan/Task Lifecycle — `create_plan`, `execute_plan`, `stop_plan`, `run_task`, `update_task` with 10 test cases (TC-17 through TC-22e). Addresses [CRITICAL] C-01/C-02/C-03/C-04 from both reviewers.

**New test cases addressing missing tool coverage:**
- TC-03b: `create_task` rich parameters (priority, due, blocked_by, stream, write_set, is_join). Addresses [MINOR] C-13.
- TC-03c-f: `create_task` boundary/invalid inputs (zero criteria, title length, priority range, malformed due date). Addresses [MAJOR] E-03, E-04.
- TC-09b: `list_jobs` combined filters. Addresses [MINOR] C-14.
- TC-09c: `list_jobs` empty state. Addresses [MINOR] E-10.
- TC-09e: `list_jobs` include_drafts. Addresses gap in draft plan visibility.
- TC-10: `list_tasks` role/status filters. Addresses [MAJOR] C-05.
- TC-11b: `inbox` max parameter. Addresses [MAJOR] C-12.
- TC-11c: `inbox` since_cursor (incremental drain). Addresses [MAJOR] C-12.
- TC-11d: `inbox` empty (no messages). Addresses [MINOR] E-10.
- TC-12b/c: `inbox_ack` invalid and mixed message_ids. Addresses [MINOR] E-07.
- TC-13b/c: `respond` invalid correlation_id and no question pending. Addresses [MAJOR] E-06.
- TC-14b/c/d: `steer` edge cases (empty text, before first tool call, on completed session). Addresses [MAJOR] E-06.
- TC-15c: `cancel` on already-completed session. Addresses EC-04.
- TC-16b/c: `follow_up` on failed and running sessions. Addresses [MINOR] E-08.
- TC-22b/c/d/e: `update_task` reassign, invalid id, cycle detection, replacement semantics. Addresses [CRITICAL] C-01.
- TC-23: Foreground bash. Addresses [MAJOR] C-09.
- TC-28/28b: `bash` cwd parameter and path-escape rejection. Addresses [MAJOR] C-10, E-05.
- TC-29/29b: Bash timeout boundary values and invalid values. Addresses [MAJOR] E-04, E-03.
- TC-30/30b: Bash immediate exit (zero and non-zero). Addresses [MAJOR] E-05.
- TC-31/32: Profile tests with concrete observable assertions (not circular). Addresses [CRITICAL] D-06.
- TC-33/33b: `delegate` snapshot parameter and over-cap rejection. Addresses [MAJOR] C-06.
- TC-34: `delegate` critical flag. Addresses [MAJOR] C-07.
- TC-35: `delegate` allow_blocking_question. Addresses [MINOR] C-08.
- TC-36: `delegate` timeout_seconds=0. Addresses EC-17.
- TC-37: `delegate` task_id/session_id collision. Addresses [MAJOR] gap from Ray.
- TC-38: `delegate(action="status")` with no id. Addresses [MAJOR] gap from Ray.
- EC-08: Chain depth boundary (at limit AND exceeding). Addresses [MINOR] D-09.
- EC-10: Concurrent steer + cancel race condition. Addresses [CRITICAL] E-01.
- EC-11: Two steers in quick succession. Addresses [CRITICAL] E-01.
- EC-12: `list_jobs` invalid kind/status. Addresses [MAJOR] gap.

**New pre-suite infrastructure:**
- ENV-01: Baseline state recording (delta assertions). Addresses [MAJOR] O-03.
- ENV-02: Controlled long-running task prompt. Addresses [CRITICAL] R-01.
- ENV-03: Message-producing task prompt. Addresses [MAJOR] O-01 (hidden dep).
- ENV-04: Question-asking task prompt. Addresses [MAJOR] O-01 (hidden dep).
- ENV-05: Teardown protocol. Addresses [CRITICAL] O-02, R-03.
- ENV-06: Bug severity rubric. Addresses [MINOR] R-06.

### Fixed (based on reviewer findings)

- TC-03: Replaced `criteria=[...]` placeholder with real criteria. Addresses [CRITICAL] D-01.
- TC-04: Enumerated exact valid transitions and explicit fail conditions. Addresses [MAJOR] D-02.
- TC-05: Added state-comparison verification method (capture before/after). Addresses [MAJOR] D-03.
- TC-06: Changed to known-count assertion (create N jobs, verify all N appear). Addresses [MAJOR] D-07.
- TC-10 (steer): Added concrete assertion (literal substring "STEERED" in output). Addresses [MAJOR] D-04.
- TC-11: Defined ordering rule (chronological, monotonically increasing). Addresses [MINOR] D-05.
- TC-14/15 (cancel): Distinguished soft vs hard, documented acceptable terminal states. Addresses [MAJOR] D-06.
- TC-20/21 (profiles): Replaced circular "matches contract" with concrete observable assertions. Addresses [CRITICAL] D-06.
- EC-05 (follow_up on failed): Changed from "either resumes or errors" to "document actual behavior." Addresses [MINOR] D-10.
- EC-09 (chain depth): Added boundary test (at limit) in addition to exceeding. Addresses [MINOR] D-09.
- EC-13 (bash timeout): Added session cleanup verification. Addresses [MINOR] D-10.
- All timing-dependent tests: Now use ENV-02 controlled prompt with guaranteed 45s runtime. Addresses [CRITICAL] R-01.
- All tests with hidden dependencies: Now have explicit Setup steps referencing ENV prompts or prior test outputs. Addresses [MAJOR] O-01.
- Execution order: Suite-based with declared inter-suite and intra-suite dependencies. Addresses [MAJOR] O-04.

### Not yet addressed (deferred)
- `message_parent` as a direct tool call from Jim: Jim does not have `message_parent` in his toolset (it's a child-agent tool). Testing it requires a specialist-profile child that uses it, which TC-11/TC-13 now exercise indirectly. Direct testing is out of Jim's scope.
- `bash` `persistent` parameter: Reserved for future use, not testable until the feature is active.
- 3P agent profile degradation: Requires a 3P (external-CLI) agent, which is not configured in this workspace.
- Resource-limit/scale tests (concurrent session caps, large output buffers): Marked as future work — requires controlled load generation that may destabilize the workspace.
- `list_jobs` 75-row terminal threshold: Requires creating 75+ terminal jobs, which is impractical without automation and risks workspace pollution.
