# UAT Issues Log — Final Consolidated

**Date:** 2026-07-31
**Tester:** Worker (UAT)
**Scope:** Delegation, monitoring, communication, control, plan/task lifecycle, bash lifecycle, concurrency, edge cases (invalid inputs, boundary values, security, race conditions, resource limits, timing).

---

## Summary

- **Total test cases executed:** 70+
- **PASS:** 45+
- **FAIL:** 10
- **BLOCKED:** 5
- **Issues filed:** 14

---

## Issues Found

### ISSUE-001: Soft cancel does not cancel — subagent completes normally
- **Test Case:** TC-14 (Soft Cancel)
- **Severity:** P1-Critical
- **Description:** Dispatched a subagent with task "Sleep for 60 seconds, then return 'done'." After 5 seconds, called `delegate(action="cancel", hard=false)`. The cancel was accepted ("cooperatively cancelled; checkpoint flush expected within 5s, hard cancel backstop"). However, the subagent continued running and completed normally with result "done" after the full 60 seconds. The soft cancel had no effect.
- **Expected:** Subagent should reach terminal state (cancelled/failed) within 30 seconds, NOT complete normally.
- **Actual:** Subagent completed normally with result "done" after ~60 seconds.
- **Reproducible:** Yes — reproduced 4 times across multiple sessions.

### ISSUE-002: Hard cancel does not cancel — subagent completes normally
- **Test Case:** TC-15 (Hard Cancel)
- **Severity:** P0-Blocker
- **Description:** Dispatched a subagent with task "Sleep for 60 seconds, then return 'done'." After 5 seconds, called `delegate(action="cancel", hard=true)`. The cancel was accepted ("hard-cancelled immediately"). However, the subagent continued running and completed normally with result "done" after the full 60 seconds. The hard cancel had no effect whatsoever.
- **Expected:** Subagent should reach terminal state (cancelled/failed) within 5 seconds.
- **Actual:** Subagent completed normally with result "done" after ~60 seconds.
- **Reproducible:** Yes — reproduced 4 times across multiple sessions. Also confirmed with a blocking subagent (delegate-8) that was hard-cancelled 3 times and still completed normally.

### ISSUE-003: message_parent tool unavailable to delegated child sessions
- **Test Case:** TC-11 (Inbox), TC-13 (Respond)
- **Severity:** P0-Blocker
- **Description:** Dispatched specialist-profile subagents to both Worker and Ava with tasks to call `message_parent`. Worker: `message_parent` fails with "no durable session record for this session — message_parent may only be called from within a delegated child session." Ava: `message_parent` tool is denied by agent policy. Parent inbox always returns empty.
- **Expected:** Child subagent can call `message_parent` to push messages to the parent's inbox.
- **Actual:** `message_parent` fails for both agents. Parent `inbox` always returns empty.
- **Impact:** The entire parent-child messaging channel (progress, checkpoint, artifact, blocker, question, handback) is non-functional. Blocks TC-11, TC-12, TC-13, and all edge cases involving inbox/respond.
- **Reproducible:** Yes — confirmed across 2 agents, 6+ attempts, both utility and specialist profiles.

### ISSUE-004: Steer on completed/terminal session silently accepted
- **Test Case:** TC-10 / EC-04
- **Severity:** P2-Major
- **Description:** Called `delegate(action="steer")` on a session that had already completed. The steer was accepted ("Steering message queued") instead of returning an error. A completed session has no next tool boundary, so the steer is silently dropped.
- **Expected:** Error message like "session already terminal."
- **Actual:** Steer accepted silently, never applied.
- **Reproducible:** Yes.

### ISSUE-005: Follow_up does not inject new instructions
- **Test Case:** TC-16 (Follow-Up)
- **Severity:** P1-Critical
- **Description:** Dispatched a subagent with task "Return 'first'". It completed with result "first". Called `delegate(action="follow_up", text="Now also return 'goodbye'")`. The follow_up completed but returned "first" again — the new instruction was NOT incorporated. The subagent did not process the follow-up instruction.
- **Expected:** Follow_up should warm-resume the subagent with the new instruction, and the subagent should act on it.
- **Actual:** Subagent returned the original result "first" without processing the new instruction.
- **Reproducible:** Yes — reproduced twice.

### ISSUE-006: Default 5-minute delegation timeout kills long-running subagents
- **Test Case:** N/A (discovered during UAT plan review)
- **Severity:** P2-Major
- **Description:** Ray timed out after 5 minutes with no output on a substantial review task. The default `timeout_seconds=0` maps to 5 minutes, which is insufficient for complex tasks. No warning or progressive feedback before timeout.
- **Expected:** Longer default, or progressive feedback (checkpoint/warning) before timeout.
- **Actual:** Subagent killed silently at 5-minute mark.
- **Reproducible:** Yes.
- **Workaround:** Always set `timeout_seconds` explicitly to a large value for complex tasks.

### ISSUE-007: list_jobs `actionable` field is false for running subagents
- **Test Case:** TC-06
- **Severity:** P3-Minor
- **Description:** Running subagents show `actionable: false` in `list_jobs` results, even though they can be steered, cancelled, peeked, etc.
- **Expected:** Running subagents should have `actionable: true`.
- **Actual:** Running subagents have `actionable: false`.
- **Reproducible:** Yes.

### ISSUE-008: list_jobs terminal rows suppressed without clear indication
- **Test Case:** TC-06, TC-08
- **Severity:** P4-Trivial
- **Description:** `list_jobs` omits terminal rows by default. The `notes.terminal_suppressed` count is present but easy to miss.
- **Reproducible:** Yes.

### ISSUE-009: create_task with plan_id fails — "plan store is not configured"
- **Test Case:** TC-22 (Plan Lifecycle)
- **Severity:** P0-Blocker
- **Description:** `create_task` with `plan_id` parameter fails with "cannot verify plan_id: plan store is not configured." Plans can be created via `create_plan` (returns a plan_id and draft state) but no tasks can be attached to them. `execute_plan` then returns "zero member tasks; nothing to execute." The entire plan execution lifecycle is broken — plans are dead drafts that can never be populated or executed.
- **Expected:** `create_task(plan_id=<id>)` should attach the task as a plan member. `execute_plan` should then dispatch all member tasks.
- **Actual:** `create_task` with plan_id always fails. Plans remain empty drafts forever.
- **Impact:** Blocks TC-22, TC-23, TC-27, TC-28, TC-29, TC-30, and all plan-related edge cases. The entire plan/task lifecycle is untestable.
- **Reproducible:** Yes — failed on 3 different plan IDs.

### ISSUE-010: Bash safety guard blocks simple for-loops
- **Test Case:** TC-18 (Incremental output)
- **Severity:** P2-Major
- **Description:** The command `for i in $(seq 1 5); do echo "line$i"; sleep 1; done` was blocked by the safety guard with "dangerous pattern detected." This is a simple counting loop — a common, safe pattern. The safety guard is overly aggressive and blocks legitimate shell scripting.
- **Expected:** Simple for-loops with seq should be allowed.
- **Actual:** Blocked with "dangerous pattern detected."
- **Reproducible:** Yes — blocked every time this pattern was attempted.
- **Workaround:** Use `yes | head` or `printf` with literal strings instead of loops.

### ISSUE-011: Bash safety guard blocks command substitution
- **Test Case:** TC-L04 (Long command)
- **Severity:** P2-Major
- **Description:** The command `echo "testing $(python3 -c "print('x' * 1000)")"` was blocked by the safety guard with "dangerous pattern detected." Command substitution is a fundamental shell feature. The safety guard is overly aggressive.
- **Expected:** Command substitution should be allowed.
- **Actual:** Blocked with "dangerous pattern detected."
- **Reproducible:** Yes.

### ISSUE-012: Bash safety guard blocks echo with redirect-like characters
- **Test Case:** TC-H02 (Special characters)
- **Severity:** P3-Minor
- **Description:** The command `echo "special chars: !@#$%^&*()_+-={}[]|\\:;'<>,./?"` was blocked by the safety guard with "path outside working dir." The `>` character in the echo string is being interpreted as a redirect, triggering the path safety check.
- **Expected:** Characters inside quoted strings should not trigger redirect detection.
- **Actual:** Blocked with "path outside working dir."
- **Reproducible:** Yes.

### ISSUE-013: Background session shows "timeout" status when command completed successfully
- **Test Case:** TC-I01 (Bash timeout minimum)
- **Severity:** P3-Minor
- **Description:** Background session 76089c75 ran `echo ok` with `timeout_seconds=1`. The command completed successfully (output "ok\n" is present), but the session status shows "timeout" instead of "done." The session appears to have completed before the timeout, but the timeout timer fired anyway and overwrote the status.
- **Expected:** If the command completes before the timeout, status should be "done" with exit code 0.
- **Actual:** Status is "timeout" with exit code -1, despite output being present.
- **Reproducible:** Yes — reproduced with `echo ok` and `timeout_seconds=1`.

### ISSUE-014: Workspace-level task tools denied by agent policy
- **Test Case:** TC-E01 (Plan/Task Lifecycle)
- **Severity:** P1-Critical
- **Description:** The workspace-level task management tools — `create_task_in_workspace`, `list_tasks_in_workspace`, `update_task_in_workspace`, `stop_plan`, `plan_correct` — are all denied by the Worker agent's policy. Additionally, `list_jobs` and `create_task` (the non-workspace versions) are also denied. This means the Worker agent cannot create tasks on the workspace board, list tasks across workspaces, or manage plans.
- **Expected:** Worker agent should have access to workspace-level task management tools.
- **Actual:** All workspace-level task tools denied by policy.
- **Reproducible:** Yes — confirmed via `load_tool`.

---

## Test Results Summary

### Suite A: Dispatch & Monitor
| Test | Result | Issue |
|------|--------|-------|
| TC-01 (Async Dispatch) | PASS | — |
| TC-02 (Sync Dispatch) | PASS | — |
| TC-03 (create_task) | PASS | — |
| TC-04 (Status Polling) | PASS | — |
| TC-05 (Peek) | PASS | — |
| TC-06 (list_jobs Full) | PASS | ISSUE-007, ISSUE-008 |
| TC-07 (list_jobs Filter Kind) | PASS | — |
| TC-08 (list_jobs Filter Status) | PASS | — |
| TC-09 (list_jobs Label Search) | PASS | — |

### Suite B: Communicate & Control
| Test | Result | Issue |
|------|--------|-------|
| TC-10 (Steer) | PARTIAL | Steer works on running sessions; ISSUE-004 for terminal sessions |
| TC-11 (Inbox) | FAIL | ISSUE-003 — message_parent broken |
| TC-12 (inbox_ack) | BLOCKED | Cannot test — no messages to ack (ISSUE-003) |
| TC-13 (Respond) | FAIL | ISSUE-003 — child cannot ask questions |
| TC-14 (Soft Cancel) | FAIL | ISSUE-001 — cancel has no effect |
| TC-15 (Hard Cancel) | FAIL | ISSUE-002 — cancel has no effect |
| TC-16 (Follow-Up) | FAIL | ISSUE-005 — follow_up doesn't inject new instructions |

### Suite C: Plan/Task Lifecycle
| Test | Result | Issue |
|------|--------|-------|
| TC-22 (create_plan) | PASS | Plan created in draft state |
| TC-23 (create_task with plan_id) | FAIL | ISSUE-009 — plan store not configured |
| TC-24 (update_task) | PASS | update_task works for self-assigned tasks; blocked_by works |
| TC-25 (run_task) | BLOCKED | Consent gate timeout — tool denied |
| TC-26 (Cycle Detection) | PASS | Cycle in blocked_by correctly rejected |
| TC-27 (execute_plan) | FAIL | ISSUE-009 — zero member tasks, cannot execute |
| TC-28 (stop_plan) | BLOCKED | Cannot test — no plans with members to stop |

### Suite D: Bash Lifecycle
| Test | Result | Issue |
|------|--------|-------|
| TC-17 (Foreground bash) | PASS | — |
| TC-18 (Background bash + poll) | PASS | — |
| TC-19 (Background bash kill) | PASS | — |
| TC-20 (Background bash timeout) | PASS | — |
| TC-21 (for loop) | FAIL | ISSUE-010 — for-loops blocked by safety guard |
| TC-22 (Command substitution) | FAIL | ISSUE-011 — command substitution blocked |
| TC-23 (Special chars in echo) | FAIL | ISSUE-012 — redirect-like chars blocked |

### Suite E: Bash Boundary Values
| Test | Result | Issue |
|------|--------|-------|
| TC-01 (timeout=1) | PASS | Accepted |
| TC-02 (timeout=3600) | PASS | Accepted |
| TC-03 (timeout=3601) | PASS | Correctly rejected |
| TC-04 (timeout=0) | PASS | Correctly rejected |
| TC-05 (timeout=-1) | PASS | Correctly rejected |
| TC-06 (exit code propagation) | PASS | Exit code 1 returned correctly |
| TC-07 (timeout=1, echo ok) | FAIL | ISSUE-013 — status shows "timeout" despite successful completion |

### Suite F: Bash Security
| Test | Result | Issue |
|------|--------|-------|
| TC-01 (Path traversal) | PASS | `../../etc/passwd` blocked |
| TC-02 (Absolute cwd) | PASS | `cwd=/etc` blocked |
| TC-03 (Parent dir escape) | PASS | `cwd=..` blocked |
| TC-04 (No-op command) | PASS | `true` returns exit 0 |
| TC-05 (Immediate non-zero exit) | PASS | `false` returns exit 1 |

### Suite G: Concurrency
| Test | Result | Issue |
|------|--------|-------|
| TC-01 (Concurrent dispatch x5) | PASS | 5 subagents dispatched simultaneously, all completed |
| TC-02 (Concurrent bash x3) | PASS | 3 background bash sessions all completed |
| TC-03 (list_jobs during concurrent) | PASS | All visible in list_jobs while running |

### Suite H: Invalid Inputs
| Test | Result | Issue |
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

### Suite I: Resource Limits
| Test | Result | Issue |
|------|--------|-------|
| TC-01 (100-line output) | PASS | — |
| TC-02 (1000-char single-line output) | PASS | — |
| TC-03 (Long-running background session) | PASS | Killed successfully |

### Suite J: File Operations
| Test | Result | Issue |
|------|--------|-------|
| TC-01 (write_file) | PASS | — |
| TC-02 (read_file) | PASS | — |
| TC-03 (read_file with offset/length) | PASS | Pagination works |
| TC-04 (edit_file) | PASS | — |
| TC-05 (list_directory) | PASS | — |
| TC-06 (write to new subdir) | PASS | Auto-creates directory |
| TC-07 (overwrite with flag) | PASS | — |
| TC-08 (path escape blocked) | PASS | — |

### Suite K: Agent Policy
| Test | Result | Issue |
|------|--------|-------|
| TC-01 (delegate denied) | PASS | Correctly denied — no permitted delegation target |
| TC-02 (workspace task tools denied) | FAIL | ISSUE-014 — all workspace task tools denied |
| TC-03 (create_plan consent gate) | BLOCKED | Consent gate timeout |
| TC-04 (run_task consent gate) | BLOCKED | Consent gate timeout |

---

## Priority Summary

| Priority | Count | Issues |
|----------|-------|--------|
| P0-Blocker | 3 | ISSUE-002, ISSUE-003, ISSUE-009 |
| P1-Critical | 3 | ISSUE-001, ISSUE-005, ISSUE-014 |
| P2-Major | 4 | ISSUE-004, ISSUE-006, ISSUE-010, ISSUE-011 |
| P3-Minor | 3 | ISSUE-007, ISSUE-012, ISSUE-013 |
| P4-Trivial | 1 | ISSUE-008 |

---

## Key Takeaways

1. **The plan execution lifecycle is completely broken** (ISSUE-009). Plans can be created but never populated with member tasks. This is the most impactful bug — it blocks the entire plan-based workflow.

2. **The parent-child messaging channel is non-functional** (ISSUE-003). `message_parent` fails from all delegated subagents regardless of profile. This breaks inbox, respond, and all question-based flows.

3. **Cancel has no effect** (ISSUE-001, ISSUE-002). Both soft and hard cancel return success but do not terminate the subagent. This is a safety and cost concern — runaway subagents cannot be stopped.

4. **Follow_up does not inject new instructions** (ISSUE-005). The warm-resume mechanism re-runs the original task without processing the new instruction.

5. **Steer works correctly** when the subagent is running and hits tool boundaries. This is a bright spot — the steer mechanism is functional.

6. **Bash safety guard is overly aggressive** (ISSUE-010, ISSUE-011, ISSUE-012). It blocks for-loops, command substitution, and redirect-like characters in quoted strings. This prevents legitimate shell scripting.

7. **Background session status can be incorrect** (ISSUE-013). A session that completes before its timeout shows "timeout" instead of "done."

8. **Workspace-level task tools are denied by agent policy** (ISSUE-014). The Worker agent cannot access `create_task_in_workspace`, `list_tasks_in_workspace`, `update_task_in_workspace`, `stop_plan`, or `plan_correct`.

9. **Concurrent dispatch works** — 5 simultaneous subagents all completed successfully.

10. **Task lifecycle (non-plan) works** — create_task, update_task (for self-assigned), blocked_by, cycle detection all function correctly.

11. **Bash lifecycle is solid** except for the overly aggressive safety guard.

12. **File operations all work correctly** — write, read, edit, list, pagination, overwrite, path escape protection.
