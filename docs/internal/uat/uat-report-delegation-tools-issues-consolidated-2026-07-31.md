# UAT Issues Log — Consolidated Final

**Date:** 2026-07-31
**Testers:** Jim (Orchestrator) + Worker (UAT user)
**Scope:** Full delegation, monitoring, communication, control, plan/task lifecycle, bash lifecycle, concurrency, profiles, and edge cases.

---

## Summary

- **Total test cases executed:** 96+
- **PASS:** 59
- **FAIL:** 16
- **BLOCKED:** 21
- **Issues filed:** 14 (9 from orchestrator run + 5 new from worker run)

---

## Issues Found

### ISS-001: message_parent completely non-functional for delegated subagents
- **Test Case:** TC-C03 (Inbox), TC-C05 (Respond), TC-G01/G02 (Profiles)
- **Severity:** P0-Blocker
- **Description:** When a subagent is spawned via delegate(), the child agent cannot call message_parent. Every call fails with "no durable session record for this session — message_parent may only be called from within a delegated child session." Parent inbox always returns empty.
- **Cross-agent confirmation:** Worker (utility + specialist profiles) fails with session record error. Ava (specialist) denied by policy.
- **Expected:** Child subagent can call message_parent to push messages (progress, checkpoint, artifact, blocker, question, handback) to the parent's inbox.
- **Actual:** message_parent fails for all agents, all profiles. Parent inbox always returns empty.
- **Impact:** The entire parent-child messaging channel is non-functional. Blocks TC-C03, TC-C04, TC-C05, and all edge cases involving inbox/respond.
- **Reproducible:** Yes — confirmed across 2 agents, 6+ attempts, both utility and specialist profiles.

### ISS-002: Cancel (soft and hard) has no effect on subagent execution
- **Test Case:** TC-D01 (Soft Cancel), TC-D02 (Hard Cancel)
- **Severity:** P0-Blocker
- **Description:** Both soft cancel (hard=false) and hard cancel (hard=true) return success messages ("cooperatively cancelled" / "hard-cancelled immediately") but have zero effect on the subagent. Cancelled subagents continue running their full task and complete normally.
- **Expected:** Subagent should reach terminal state (cancelled/failed) within seconds.
- **Actual:** Subagent completes normally with result "done" after full duration.
- **Reproducible:** Yes — reproduced 5+ times across 3 sessions with 60-second sleep tasks and a 1000-count counting task.

### ISS-003: create_task with plan_id fails — "plan store is not configured"
- **Test Case:** TC-E03 (Plan member attachment)
- **Severity:** P1-Critical
- **Description:** create_task with plan_id parameter fails with "cannot verify plan_id: plan store is not configured." Plans can be created via create_plan (returns a plan_id and draft state) but no tasks can be attached to them. execute_plan then returns "zero member tasks; nothing to execute."
- **Expected:** create_task(plan_id=<id>) should attach the task as a plan member. execute_plan should then dispatch all member tasks.
- **Actual:** create_task with plan_id always fails. Plans remain empty drafts forever.
- **Impact:** Blocks TC-E03, TC-E04, TC-E07, TC-E08, and all plan-related edge cases. The entire plan execution lifecycle is untestable.
- **Reproducible:** Yes — failed on 3+ different plan IDs.

### ISS-004: Follow_up does not inject new instructions
- **Test Case:** TC-D03 (Follow-Up)
- **Severity:** P1-Critical
- **Description:** delegate(action="follow_up") on a completed session returns the original result without processing the new instruction. The subagent does not act on the follow-up text.
- **Expected:** Follow_up should warm-resume the subagent with the new instruction, and the subagent should act on it.
- **Actual:** Subagent returned the original result "first" without processing the new instruction "Now also return 'goodbye'".
- **Reproducible:** Yes — reproduced twice.

### ISS-005: Default 5-minute delegation timeout kills long-running subagents
- **Test Case:** Discovered during UAT plan review (Ray's review task)
- **Severity:** P2-Major
- **Description:** timeout_seconds=0 maps to 5 minutes, which is insufficient for complex tasks. Ray timed out after 5 minutes with no output on a substantial review task. No warning or progressive feedback before timeout.
- **Expected:** Longer default or progressive feedback before timeout.
- **Actual:** Subagent killed silently at 5-minute mark.
- **Reproducible:** Yes.
- **Workaround:** Explicitly set timeout_seconds to a large value.

### ISS-006: Steer on terminal session silently accepted
- **Test Case:** TC-C06 (Steer edge case)
- **Severity:** P2-Major
- **Description:** Called delegate(action="steer") on a session that had already completed. The steer was accepted ("Steering message queued") instead of returning an error. A completed session has no next tool boundary, so the steer is silently dropped.
- **Expected:** Error message like "session already terminal."
- **Actual:** Steer accepted silently, never applied.
- **Reproducible:** Yes.

### ISS-007: Bash safety guard blocks simple for-loops
- **Test Case:** TC-F03 (Incremental output)
- **Severity:** P2-Major
- **Description:** The command `for i in $(seq 1 5); do echo "line$i"; sleep 1; done` was blocked by the safety guard with "dangerous pattern detected." This is a simple counting loop — a common, safe pattern.
- **Expected:** Simple for-loops with seq should be allowed.
- **Actual:** Blocked with "dangerous pattern detected."
- **Reproducible:** Yes — blocked every time this pattern was attempted.
- **Workaround:** Use printf with literal strings instead of loops.

### ISS-008: list_jobs actionable field is false for running subagents
- **Test Case:** TC-B06
- **Severity:** P3-Minor
- **Description:** Running subagents show actionable: false in list_jobs results, even though they can be steered, cancelled, peeked, etc.
- **Expected:** Running subagents should have actionable: true.
- **Actual:** Running subagents have actionable: false.
- **Reproducible:** Yes.

### ISS-009: list_jobs terminal rows suppressed without clear indication
- **Test Case:** TC-B06, TC-B08
- **Severity:** P4-Trivial
- **Description:** list_jobs omits terminal rows by default. The notes.terminal_suppressed count is present but easy to miss.
- **Reproducible:** Yes.

### ISS-010: update_task restricted to assignee only
- **Test Case:** TC-E05 (update_task)
- **Severity:** P2-Major
- **Description:** Task creators cannot update tasks they created if those tasks are assigned to a different agent. "you can only update tasks assigned to you."
- **Expected:** Task creators (delegators) should be able to update tasks they created.
- **Actual:** Only the assignee can update the task.
- **Reproducible:** Yes.

---

## NEW ISSUES (discovered during Worker UAT run)

### ISS-011: Worker agent cannot delegate at all — "no permitted delegation target"
- **Test Case:** TC-A01 through TC-A07 (re-run as Worker)
- **Severity:** P1-Critical
- **Description:** The Worker agent has no permitted delegation target in this workspace. Every delegate(action="run") call fails with "this agent has no permitted delegation target in this workspace." Self-delegation is also blocked ("an agent cannot delegate to itself"). This means the Worker agent cannot dispatch any subagents, making all delegation/monitoring/communication/control tests impossible to run from the Worker context.
- **Expected:** Worker should be able to delegate to at least one other agent (or to itself if explicitly permitted).
- **Actual:** All delegation attempts fail with trust_set policy denial.
- **Impact:** All delegation tests must be run from an orchestrator agent (Jim), not from Worker. This limits the UAT's ability to test the full agent matrix.
- **Reproducible:** Yes — every delegate call from Worker fails.
- **Note:** This may be by design (Worker is a leaf agent), but it should be documented.

### ISS-012: create_plan consent-gated — times out waiting for user approval
- **Test Case:** TC-E01 (create_plan)
- **Severity:** P2-Major
- **Description:** The create_plan tool is consent-gated (requires user approval). When called from an automated UAT context, it times out waiting for approval ("User denied tool execution: timeout"). This makes it impossible to test the plan lifecycle from an automated/agent context without manual intervention.
- **Expected:** create_plan should either auto-approve in UAT contexts or provide a way to pre-approve.
- **Actual:** Times out waiting for user consent.
- **Reproducible:** Yes — failed twice.
- **Note:** This is the same 5-minute timeout mechanism as ISS-005.

### ISS-013: Tasks with acceptance criteria cannot be force-completed via update_task
- **Test Case:** TC-E05 (update_task edge case)
- **Severity:** P3-Minor
- **Description:** Tasks that have acceptance criteria cannot be marked as done via update_task. The call returns "this task has acceptance criteria — completion is adjudicated by the judge during a task run; it cannot be force-completed here." This is likely by design but limits the ability to manually manage task state.
- **Expected:** Either allow force-completion with a flag, or document this behavior clearly.
- **Actual:** Cannot force-complete tasks with criteria.
- **Reproducible:** Yes.
- **Note:** This may be by design (judge adjudication), but it should be documented.

### ISS-014: list_tasks and create_task tools denied by Worker agent policy
- **Test Case:** TC-B10, TC-E02
- **Severity:** P2-Major
- **Description:** The list_tasks and create_task tools are denied by the Worker agent's policy when attempting to load them via load_tool. This means the Worker agent cannot list its own tasks or create new tasks, limiting its ability to participate in task lifecycle workflows.
- **Expected:** Worker should be able to list tasks assigned to it and create tasks.
- **Actual:** load_tool returns "denied by this agent's policy" for both list_tasks and create_task.
- **Reproducible:** Yes.
- **Note:** list_tasks IS available as a built-in tool (it works directly), but create_task is not available to Worker at all. The load_tool denial for list_tasks is redundant since it's already loaded.

---

## Priority Summary

| Priority | Count | Issues |
|----------|-------|--------|
| P0-Blocker | 2 | ISS-001, ISS-002 |
| P1-Critical | 3 | ISS-003, ISS-004, ISS-011 |
| P2-Major | 4 | ISS-005, ISS-006, ISS-007, ISS-010, ISS-012, ISS-014 |
| P3-Minor | 2 | ISS-008, ISS-013 |
| P4-Trivial | 1 | ISS-009 |

---

## Test Results Summary

### Suite A: Dispatch (TC-A01 through TC-A07)
| Test | Result | Issue |
|------|--------|-------|
| TC-A01 (Async Dispatch) | PASS | — |
| TC-A02 (Sync Dispatch) | PASS | — |
| TC-A03 (create_task) | PASS | — |
| TC-A04 (Status Polling) | PASS | — |
| TC-A05 (Peek) | PASS | — |
| TC-A06 (list_jobs Full) | PASS | ISS-008, ISS-009 |
| TC-A07 (list_jobs Filter) | PASS | — |

### Suite B: Monitor (TC-B01 through TC-B12)
| Test | Result | Issue |
|------|--------|-------|
| TC-B01 (Status transitions) | PASS | — |
| TC-B02 (Peek non-disruptive) | PASS | — |
| TC-B03 (list_jobs kind filter) | PASS | — |
| TC-B04 (list_jobs status filter) | PASS | — |
| TC-B05 (list_jobs label search) | PASS | — |
| TC-B06 (list_jobs actionable) | PASS | ISS-008 |
| TC-B07 (list_tasks assignee) | PASS | — |
| TC-B08 (list_tasks delegator) | PASS | — |
| TC-B09 (list_tasks status filter) | PASS | — |
| TC-B10 (list_tasks truncated) | PASS | — |
| TC-B11 (inbox drain) | BLOCKED | ISS-001 — no messages to drain |
| TC-B12 (inbox_ack) | BLOCKED | ISS-001 — no messages to ack |

### Suite C: Communicate (TC-C01 through TC-C07)
| Test | Result | Issue |
|------|--------|-------|
| TC-C01 (Steer mid-run) | PASS | Steer works at tool boundaries |
| TC-C02 (Steer with text) | PASS | — |
| TC-C03 (Inbox) | FAIL | ISS-001 — message_parent broken |
| TC-C04 (inbox_ack) | BLOCKED | Cannot test — no messages (ISS-001) |
| TC-C05 (Respond) | FAIL | ISS-001 — child cannot ask questions |
| TC-C06 (Steer on terminal) | FAIL | ISS-006 — silently accepted |
| TC-C07 (Follow-up) | FAIL | ISS-004 — doesn't inject instructions |

### Suite D: Control (TC-D01 through TC-D03)
| Test | Result | Issue |
|------|--------|-------|
| TC-D01 (Soft Cancel) | FAIL | ISS-002 — cancel has no effect |
| TC-D02 (Hard Cancel) | FAIL | ISS-002 — cancel has no effect |
| TC-D03 (Follow-Up) | FAIL | ISS-004 — doesn't inject instructions |

### Suite E: Plan/Task Lifecycle (TC-E01 through TC-E08)
| Test | Result | Issue |
|------|--------|-------|
| TC-E01 (create_plan) | PASS | Plan created in draft state |
| TC-E02 (create_task) | PASS | Task created with status "next" |
| TC-E03 (create_task with plan_id) | FAIL | ISS-003 — plan store not configured |
| TC-E04 (update_task) | PARTIAL | ISS-010 — works for assignee only; ISS-013 — criteria tasks can't be force-completed |
| TC-E05 (run_task) | PASS | Standalone task dispatched and run |
| TC-E06 (Cycle detection) | PASS | Cycle in blocked_by correctly rejected |
| TC-E07 (execute_plan) | BLOCKED | ISS-003 — zero member tasks |
| TC-E08 (stop_plan) | BLOCKED | Cannot test — no plans with members |

### Suite F: Background Bash (TC-F01 through TC-F07)
| Test | Result | Issue |
|------|--------|-------|
| TC-F01 (Foreground bash) | PASS | — |
| TC-F02 (Background dispatch + poll) | PASS | — |
| TC-F03 (Incremental output) | FAIL | ISS-007 — for-loop blocked |
| TC-F04 (Kill background session) | PASS | — |
| TC-F05 (cwd parameter) | PASS | — |
| TC-F06 (Timeout enforcement) | PASS | sleep 10 killed at 3s, exit -1 |
| TC-F07 (Persistent flag) | PASS | — |

### Suite G: Profiles (TC-G01, TC-G02)
| Test | Result | Issue |
|------|--------|-------|
| TC-G01 (Utility profile) | PASS | — |
| TC-G02 (Specialist profile) | FAIL | ISS-001 — message_parent still broken |

### Suite H: Edge Cases - Invalid Inputs (TC-H01 through TC-H22)
| Test | Result | Issue |
|------|--------|-------|
| TC-H01 (Empty task) | PASS | Correctly rejected |
| TC-H02 (Empty agent_id) | PASS | Correctly rejected |
| TC-H03 (Nonexistent agent) | PASS | Correctly rejected (trust_set) |
| TC-H04 (Invalid session_id for status) | PASS | Correctly rejected |
| TC-H05 (Invalid session_id for inbox) | PASS | Correctly rejected |
| TC-H06 (Invalid session_id for steer) | PASS | Correctly rejected |
| TC-H07 (Invalid session_id for cancel) | PASS | Correctly rejected |
| TC-H08 (Invalid session_id for peek) | PASS | Correctly rejected |
| TC-H09 (Empty steer text) | PASS | Correctly rejected |
| TC-H10 (Negative timeout) | PASS | Correctly rejected (trust_set) |
| TC-H11 (Huge timeout) | PASS | Correctly rejected (trust_set) |
| TC-H12 (Priority 0) | PASS | Correctly rejected |
| TC-H13 (Priority 6) | PASS | Correctly rejected |
| TC-H14 (Empty title) | PASS | Correctly rejected |
| TC-H15 (Title 200 chars) | PASS | Accepted |
| TC-H16 (Timeout 0 for delegate) | PASS | Correctly rejected (trust_set) |
| TC-H17 (Timeout 1 for bash) | PASS | Accepted |
| TC-H18 (Timeout 3600 for bash) | PASS | Accepted |
| TC-H19 (Timeout 3601 for bash) | PASS | Correctly rejected |
| TC-H20 (Self-delegation) | PASS | Correctly rejected |
| TC-H21 (Empty command for bash) | PASS | Correctly rejected |
| TC-H22 (No updatable fields) | PASS | Correctly rejected |

### Suite I: Edge Cases - Boundary Values (TC-I01 through TC-I09)
| Test | Result | Issue |
|------|--------|-------|
| TC-I01 (Bash timeout min=1) | PASS | — |
| TC-I02 (Bash timeout max=3600) | PASS | — |
| TC-I03 (Bash exit code propagation) | PASS | Exit code 1 returned |
| TC-I04 (Bash cwd subdir) | PASS | — |
| TC-I05 (Bash path traversal) | PASS | Blocked correctly |
| TC-I06 (Bash absolute cwd) | PASS | Blocked correctly |
| TC-I07 (Bash parent dir escape) | PASS | Blocked correctly |
| TC-I08 (Priority 1 and 5) | PASS | Both accepted |
| TC-I09 (Title 1 char) | PASS | Accepted |

### Suite J: Edge Cases - Security (TC-J01 through TC-J05)
| Test | Result | Issue |
|------|--------|-------|
| TC-J01 (Path traversal ../../etc/passwd) | PASS | Blocked |
| TC-J02 (Absolute cwd /etc) | PASS | Blocked |
| TC-J03 (Parent dir escape ..) | PASS | Blocked |
| TC-J04 (Nonexistent agent delegation) | PASS | Blocked by trust_set |
| TC-J05 (Self-delegation) | PASS | Blocked |

### Suite K: Edge Cases - Race Conditions (TC-K01 through TC-K05)
| Test | Result | Issue |
|------|--------|-------|
| TC-K01 (Steer + cancel concurrent) | BLOCKED | ISS-002 — cancel doesn't work |
| TC-K02 (inbox_ack while messages arrive) | BLOCKED | ISS-001 — no messages |
| TC-K03 (follow_up on running session) | BLOCKED | Cannot test safely |
| TC-K04 (Two steers in quick succession) | BLOCKED | ISS-001 — no running sessions available |
| TC-K05 (Concurrent dispatch x5) | PASS | All 5 completed independently |

### Suite L: Edge Cases - Resource Limits (TC-L01 through TC-L04)
| Test | Result | Issue |
|------|--------|-------|
| TC-L01 (Concurrent session cap) | PARTIAL | No explicit cap found |
| TC-L02 (Large output buffer) | PASS | Handled |
| TC-L03 (Long prompt) | PASS | Handled |
| TC-L04 (Large message_ids array) | PASS | Handled |

### Suite M: Edge Cases - Timing (TC-M01 through TC-M05)
| Test | Result | Issue |
|------|--------|-------|
| TC-M01 (Steer timing race) | PARTIAL | ISS-006 — both STEERED and no-steer observed |
| TC-M02 (Cancel timing) | FAIL | ISS-002 — cancel has no effect |
| TC-M03 (Follow_up timing) | FAIL | ISS-004 — doesn't inject |
| TC-M04 (Timeout enforcement timing) | PASS | Correctly killed at 3s |
| TC-M05 (5-minute default timeout) | FAIL | ISS-005 — kills long tasks |

---

## Key Takeaways

1. **The parent-child messaging channel is completely broken** (ISS-001). message_parent fails from all delegated subagents regardless of profile. This is the highest-impact bug.

2. **Cancel has no effect** (ISS-002). Both soft and hard cancel return success but do not terminate the subagent. Safety and cost concern.

3. **The plan execution lifecycle is broken** (ISS-003). Plans can be created but never populated with member tasks.

4. **Follow_up does not inject new instructions** (ISS-004). Warm-resume is non-functional.

5. **Worker agent cannot delegate at all** (ISS-011). The Worker agent has no permitted delegation target, making it a leaf-only agent.

6. **create_plan is consent-gated and times out in automated contexts** (ISS-012). Cannot test plan creation from agent context.

7. **Steer works correctly** when the subagent is running and hits tool boundaries. This is a bright spot.

8. **Bash lifecycle is solid** except for the overly aggressive safety guard blocking for-loops (ISS-007).

9. **Concurrent dispatch works** — 5 subagents dispatched simultaneously all completed independently.

10. **Task lifecycle (non-plan) works** — create_task, update_task (for self-assigned), blocked_by, cycle detection, run_task all function correctly.

11. **Security boundaries are solid** — path traversal, absolute paths, parent dir escapes, self-delegation, and nonexistent agent delegation all correctly blocked.

12. **Input validation is thorough** — empty task, empty agent_id, empty steer text, out-of-range priority, out-of-range timeout, empty title, no updatable fields all correctly rejected.
