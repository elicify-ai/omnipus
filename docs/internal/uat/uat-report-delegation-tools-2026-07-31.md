# UAT Final Report — Delegation & Tooling Platform

**Date:** 2026-07-31
**Tester:** Worker (UAT)
**Duration:** ~25 minutes
**Workspace:** 01KYVJCB2S577KQC5HK5X23EKX

---

## Executive Summary

Executed a comprehensive UAT across 11 test suites covering delegation, monitoring, communication, plan/task lifecycle, bash lifecycle, concurrency, edge cases (invalid inputs, boundary values, security, resource limits, timing), file operations, and agent policy. **3 P0-Blocker issues** were found that break core functionality: the parent-child messaging channel, subagent cancellation, and plan task attachment. Despite these, basic dispatch/monitoring, bash lifecycle, concurrent dispatch, file operations, and input validation all work correctly.

**Overall verdict: NOT READY FOR PRODUCTION** — 3 P0 blockers must be fixed before the platform can be used reliably.

---

## Test Results by Suite

### Suite A: Dispatch & Monitor — PASS (with minor issues)
| Test | Result | Notes |
|------|--------|-------|
| TC-01 (Async Dispatch) | PASS | Subagent dispatched, ran, completed |
| TC-02 (Sync Dispatch) | PASS | Blocking dispatch returns result inline |
| TC-03 (create_task) | PASS | Task created with status "next" |
| TC-04 (Status Polling) | PASS | Status transitions tracked correctly |
| TC-05 (Peek) | PASS | Returns latest checkpoint/progress |
| TC-06 (list_jobs Full) | PASS | Returns all running jobs; ISSUE-007, ISSUE-008 |
| TC-07 (list_jobs Filter Kind) | PASS | Kind filter works |
| TC-08 (list_jobs Filter Status) | PASS | Status filter works |
| TC-09 (list_jobs Label Search) | PASS | Label substring search works |

### Suite B: Communicate & Control — 4 FAILs (P0 Blockers)
| Test | Result | Notes |
|------|--------|-------|
| TC-10 (Steer) | PARTIAL | Steer works on running subagents at tool boundaries; silently accepted on terminal sessions (ISSUE-004) |
| TC-11 (Inbox) | FAIL | message_parent broken — no messages ever arrive (ISSUE-003) |
| TC-12 (inbox_ack) | BLOCKED | Cannot test — no messages to ack |
| TC-13 (Respond) | FAIL | Child cannot ask questions (ISSUE-003) |
| TC-14 (Soft Cancel) | FAIL | Cancel has no effect — subagent runs to completion (ISSUE-001) |
| TC-15 (Hard Cancel) | FAIL | Cancel has no effect — subagent runs to completion (ISSUE-002) |
| TC-16 (Follow-Up) | FAIL | Follow_up does not inject new instructions (ISSUE-005) |

### Suite C: Plan/Task Lifecycle — PARTIAL (1 P0 issue)
| Test | Result | Notes |
|------|--------|-------|
| TC-22 (create_plan) | PASS | Plan created in draft state with plan_id |
| TC-23 (create_task with blocked_by) | PASS | blocked_by set correctly; task shows "blocked" status |
| TC-24 (update_task) | PASS | Works for self-assigned tasks; priority, title, blocked_by all work |
| TC-25 (run_task) | BLOCKED | Consent gate timeout — tool denied |
| TC-26 (Cycle Detection) | PASS | Cycle in blocked_by correctly rejected |
| TC-27 (create_task with plan_id) | FAIL | "plan store is not configured" — cannot attach tasks to plans (ISSUE-009) |
| TC-28 (execute_plan) | BLOCKED | Cannot execute — no members can be attached (ISSUE-009) |
| TC-29 (stop_plan) | BLOCKED | Cannot test — no plans with members to stop |

### Suite D: Bash Lifecycle — PASS (with safety guard issues)
| Test | Result | Notes |
|------|--------|-------|
| TC-17 (Foreground bash) | PASS | Output returned inline |
| TC-18 (Background bash + poll) | PASS | Session dispatched, polled, completed |
| TC-19 (Background bash kill) | PASS | Kill succeeded, exit code -1 |
| TC-20 (Background bash timeout) | PASS | Timed out correctly |
| TC-21 (for loop) | FAIL | `for` loops blocked by safety guard (ISSUE-010) |
| TC-22 (Command substitution) | FAIL | `$(...)` blocked by safety guard (ISSUE-011) |
| TC-23 (Special chars in echo) | FAIL | `>` in quoted string blocked (ISSUE-012) |

### Suite E: Bash Boundary Values — PASS (1 minor issue)
| Test | Result | Notes |
|------|--------|-------|
| TC-01 (timeout=1) | PASS | Accepted |
| TC-02 (timeout=3600) | PASS | Accepted |
| TC-03 (timeout=3601) | PASS | Correctly rejected |
| TC-04 (timeout=0) | PASS | Correctly rejected |
| TC-05 (timeout=-1) | PASS | Correctly rejected |
| TC-06 (exit code propagation) | PASS | Exit code 1 returned correctly |
| TC-07 (timeout=1, echo ok) | FAIL | Status shows "timeout" despite successful completion (ISSUE-013) |

### Suite F: Bash Security — PASS
| Test | Result | Notes |
|------|--------|-------|
| TC-01 (Path traversal) | PASS | `../../etc/passwd` blocked |
| TC-02 (Absolute cwd) | PASS | `cwd=/etc` blocked |
| TC-03 (Parent dir escape) | PASS | `cwd=..` blocked |
| TC-04 (No-op command) | PASS | `true` returns exit 0 |
| TC-05 (Immediate non-zero exit) | PASS | `false` returns exit 1 |

### Suite G: Concurrency — PASS
| Test | Result | Notes |
|------|--------|-------|
| TC-01 (Concurrent dispatch x5) | PASS | All 5 dispatched simultaneously, all completed |
| TC-02 (Concurrent bash x3) | PASS | 3 background bash sessions all completed |
| TC-03 (list_jobs during concurrent) | PASS | All visible in list_jobs while running |

### Suite H: Invalid Inputs — PASS
| Test | Result | Notes |
|------|--------|-------|
| TC-01 (Empty bash command) | PASS | Correctly rejected |
| TC-02 (Empty delegate task) | PASS | Correctly rejected |
| TC-03 (Invalid delegate action) | PASS | Correctly rejected |
| TC-04 (Invalid list_tasks role) | PASS | Correctly rejected |
| TC-05 (Invalid list_tasks status) | PASS | Correctly rejected |
| TC-06 (Invalid update_task status) | PASS | Correctly rejected |
| TC-07 (Invalid message_parent kind) | PASS | Correctly rejected |
| TC-08 (Invalid bash action) | PASS | Correctly rejected |
| TC-09 (Nonexistent task_id) | PASS | Correctly rejected |
| TC-10 (Nonexistent session_id) | PASS | Correctly rejected |
| TC-11 (Nonexistent file read) | PASS | Correctly rejected |
| TC-12 (Edit with nonexistent old_text) | PASS | Correctly rejected |
| TC-13 (Overwrite without flag) | PASS | Correctly rejected |
| TC-14 (Priority out of range) | PASS | Priority 6 and 0 correctly rejected |
| TC-15 (Self-blocked_by) | PASS | Correctly rejected |
| TC-16 (Nonexistent blocker) | PASS | Correctly rejected |

### Suite I: Resource Limits — PASS
| Test | Result | Notes |
|------|--------|-------|
| TC-01 (100-line output) | PASS | — |
| TC-02 (1000-char single-line output) | PASS | — |
| TC-03 (Long-running background session) | PASS | Killed successfully |

### Suite J: File Operations — PASS
| Test | Result | Notes |
|------|--------|-------|
| TC-01 (write_file) | PASS | — |
| TC-02 (read_file) | PASS | — |
| TC-03 (read_file with offset/length) | PASS | Pagination works |
| TC-04 (edit_file) | PASS | — |
| TC-05 (list_directory) | PASS | — |
| TC-06 (write to new subdir) | PASS | Auto-creates directory |
| TC-07 (overwrite with flag) | PASS | — |
| TC-08 (path escape blocked) | PASS | — |

### Suite K: Agent Policy — PARTIAL
| Test | Result | Notes |
|------|--------|-------|
| TC-01 (delegate denied) | PASS | Correctly denied — no permitted delegation target |
| TC-02 (workspace task tools denied) | FAIL | ISSUE-014 — all workspace task tools denied |
| TC-03 (create_plan consent gate) | BLOCKED | Consent gate timeout |
| TC-04 (run_task consent gate) | BLOCKED | Consent gate timeout |

---

## Issues Summary

### P0 — Blockers (must fix before production)

#### ISSUE-002: Hard cancel has no effect on subagent execution
- **Affected tests:** TC-15
- **Description:** Both soft cancel (`hard=false`) and hard cancel (`hard=true`) return success messages but have zero effect on the subagent. Cancelled subagents continue running their full task and complete normally. Tested with 60-second sleep tasks and a 1000-count counting task — all ran to completion despite multiple cancel attempts.
- **Reproduced:** 5+ hard cancel attempts across 3 sessions.

#### ISSUE-003: message_parent completely non-functional for delegated subagents
- **Affected tests:** TC-11, TC-12, TC-13, TC-21
- **Description:** The entire parent-child messaging channel (progress, checkpoint, artifact, blocker, question, handback) is broken. `message_parent` fails with "no durable session record for this session" for all delegated subagents, regardless of agent or profile. Parent inbox always returns empty.
- **Root cause hypothesis:** Delegated subagent sessions are not being registered as durable session records.
- **Reproduced:** 6+ attempts across 2 agents, both profiles.

#### ISSUE-009: create_task with plan_id fails — "plan store is not configured"
- **Affected tests:** TC-27, TC-28, TC-29
- **Description:** `create_task` with `plan_id` parameter fails with "plan store is not configured." Plans can be created (create_plan works and returns a plan_id in draft state) but tasks cannot be attached to them. This means the entire plan execution lifecycle is broken — `execute_plan` fails with "zero member tasks" because no tasks can be added.
- **Reproduced:** 3+ attempts with different plan_ids.

### P1 — Critical (should fix before production)

#### ISSUE-001: Soft cancel does not cancel
- **Affected tests:** TC-14
- **Description:** Soft cancel accepted but subagent continues to completion.
- **Reproduced:** 4 times.

#### ISSUE-005: Follow_up does not inject new instructions
- **Affected tests:** TC-16
- **Description:** `delegate(action="follow_up")` on a completed session returns the original result without processing the new instruction.
- **Reproduced:** 2 attempts.

#### ISSUE-014: Workspace-level task tools denied by agent policy
- **Affected tests:** TC-E01
- **Description:** `create_task_in_workspace`, `list_tasks_in_workspace`, `update_task_in_workspace`, `stop_plan`, `plan_correct` all denied by Worker agent policy.
- **Reproduced:** Confirmed via `load_tool`.

### P2 — Major

#### ISSUE-004: Steer on terminal session silently accepted
- **Description:** `delegate(action="steer")` on a completed session returns "Steering message queued" instead of an error.

#### ISSUE-006: Default 5-minute delegation timeout kills long-running subagents
- **Description:** `timeout_seconds=0` maps to 5 minutes, which is insufficient for complex tasks. No warning before timeout.
- **Workaround:** Explicitly set `timeout_seconds` to a large value.

#### ISSUE-010: Bash for-loops blocked by safety guard
- **Description:** `for i in $(seq 1 5); do ...` blocked as "dangerous pattern detected."

#### ISSUE-011: Bash command substitution blocked by safety guard
- **Description:** `$(...)` blocked as "dangerous pattern detected."

### P3 — Minor

#### ISSUE-007: list_jobs actionable=false for running subagents
- **Description:** Running subagents show `actionable: false` even though they can be steered, cancelled, peeked, etc.

#### ISSUE-012: Bash safety guard blocks echo with redirect-like characters
- **Description:** `>` character in quoted echo string triggers path safety check.

#### ISSUE-013: Background session shows "timeout" status when command completed successfully
- **Description:** Session 76089c75 ran `echo ok` with `timeout_seconds=1`. Output "ok\n" is present, but status shows "timeout" instead of "done."

### P4 — Trivial

#### ISSUE-008: list_jobs terminal rows suppressed without clear indication
- **Description:** Terminal rows are omitted by default. The `notes.terminal_suppressed` count is present but easy to miss.

---

## What Works

- **Async/sync dispatch** — both work flawlessly
- **Status polling & peek** — accurate, real-time
- **list_jobs** — filtering, label search, kind/status filters all work
- **create_task (standalone)** — task creation, blocked_by, cycle detection all work
- **update_task** — priority, title, blocked_by, status transitions all work for self-assigned tasks
- **Concurrent dispatch** — 5 simultaneous subagents all completed successfully
- **Bash lifecycle** — foreground, background, poll, kill, timeout, cwd, path traversal protection all work
- **Bash boundary validation** — timeout 1-3600 enforced, exit codes propagated correctly
- **File operations** — write, read, edit, list, pagination, overwrite, path escape protection all work
- **Input validation** — all invalid inputs (empty strings, invalid enums, nonexistent IDs) correctly rejected
- **create_plan** — plan creation works, returns draft state

## What's Broken

- **Parent-child messaging** — completely non-functional (P0)
- **Subagent cancellation** — no effect whatsoever (P0)
- **Plan task attachment** — cannot add tasks to plans (P0)
- **Follow_up** — doesn't inject new instructions (P1)
- **Workspace task tools** — denied by agent policy (P1)
- **Bash safety guard** — blocks for-loops, command substitution, redirect-like chars (P2)
- **Background session status** — false "timeout" when command completes before timeout (P3)

---

## Recommendations

1. **Fix ISSUE-003 (message_parent)** first — this is the highest-impact bug. The entire two-way communication channel between parent and child is broken. Investigate why delegated sessions aren't registered as durable session records.
2. **Fix ISSUE-002 (cancel)** — this is a safety/cost issue. Runaway subagents cannot be stopped. Investigate why cancel returns success but has no effect.
3. **Fix ISSUE-009 (plan store)** — the plan execution lifecycle is broken. Plans can be created but not populated with tasks.
4. **Fix ISSUE-005 (follow_up)** — warm-resume is non-functional.
5. **Fix ISSUE-014 (workspace task tools)** — Worker agent needs access to workspace-level task management tools.
6. **Relax bash safety guard** — whitelist for-loops, command substitution, and redirect-like characters in quoted strings.
7. **Fix ISSUE-013 (false timeout status)** — background session status should reflect actual completion, not timeout timer expiry.
8. **Document timeout_seconds=0 behavior** — clarify that it means 5 minutes, not "no timeout."
