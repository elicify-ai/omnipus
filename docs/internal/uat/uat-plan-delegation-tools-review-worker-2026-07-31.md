# Critical Review: UAT Plan for Delegation, Monitoring & Communication Tools

Reviewer: Worker (critical UAT reviewer)
Date: 2026-07-31
Target: uat-plan.md

---

## 1. COMPLETENESS — Missing Tools, Actions, and Parameters

### [CRITICAL] C-01: `update_task` is entirely untested
`update_task` is a core orchestration tool (update status, title, priority, due date, agent_id, blocked_by, artifacts, write_set, stream, is_join). It appears nowhere in the plan — not in happy-path tests, not in edge cases. This is the primary mechanism for mutating durable tasks after creation, and it has complex parameters (blocked_by cycle detection, write_set replacement semantics, stream reassignment). Its absence is the single biggest gap.

### [CRITICAL] C-02: `create_plan` has no dedicated happy-path test
`create_plan` is only mentioned in EC-12 (include_drafts filter). There is no test that creates a plan, attaches member tasks via `create_task`, and verifies the plan structure. Plan creation, member attachment, and Definition-of-Done-driven grouping are untested.

### [CRITICAL] C-03: `execute_plan` is entirely untested
Autonomous plan execution (`execute_plan`) — starting a draft plan with no human approval, running the criteria gate — is not tested at all. This is a major control-flow path.

### [CRITICAL] C-04: `run_task` is entirely untested
`run_task` (dispatch a standalone task immediately, driving the full attempt loop: run, judge, retry with steering) is not tested. The retry/steering loop is a core resilience feature.

### [MAJOR] C-05: `list_tasks` has no dedicated test
Mentioned only as a verify step in TC-03. No test for its `role` filter (`assignee` vs `delegator`), `status` filter, or the 100-result bound / `truncated` + `matched` fields. The role semantics (assignee = tasks for me, delegator = tasks I created) are unverified.

### [MAJOR] C-06: `delegate` `snapshot` parameter untested
The `snapshot` (notes, references) parameter on `delegate(action="run")` — discretionary curated context, deny-by-default, hard-capped, over-cap rejected — is not tested. Over-cap rejection behavior is a specific edge case that should be covered.

### [MAJOR] C-07: `delegate` `critical` flag untested
`critical=true` (continue running after parent finishes gracefully) is not tested. This changes lifecycle semantics and should have a dedicated case.

### [MAJOR] C-08: `delegate` `allow_blocking_question` untested
The `allow_blocking_question` parameter (permits bounded human-routed wait on a child question) is not tested. This is a sync-delegation interaction feature.

### [MAJOR] C-09: Foreground `bash` (run_in_background=false/default) untested
Every bash test uses `run_in_background=true`. Foreground bash execution — blocking, timeout enforcement (default 300s, range 1-3600), stdout capture — is not tested. The timeout range enforcement (reject <1 or >3600) is an edge case that's missing.

### [MAJOR] C-10: `bash` `cwd` parameter untested
The `cwd` parameter (relative to workspace, no absolute paths, no `..` escapes) is not tested. The SSRF/path-escape protections (rejecting absolute paths, rejecting `..`) are security-relevant edge cases that must be covered.

### [MAJOR] C-11: `bash` `persistent` parameter untested
The `persistent` flag (reserved for long-lived session mode, only meaningful with run_in_background=true) is not tested.

### [MAJOR] C-12: `delegate(action="inbox")` `since_cursor` and `max` parameters untested
`since_cursor` (opaque cursor, return only messages after a point) and `max` (maximum messages to return) are not tested. Cursor-based incremental draining is unverified.

### [MINOR] C-13: `create_task` rich parameters untested
`create_task` is tested only with title/prompt/criteria. The `priority` (1-5), `due` (RFC 3339), `blocked_by` (dependency list), `stream` (parallel-group id), `is_join`, `write_set`, and `artifacts` parameters are not tested.

### [MINOR] C-14: `list_jobs` combined filters untested
No test combines multiple filters (e.g., `kind="subagent"` AND `status="running"` AND `label_contains="UAT"`). Combined-filter interaction is unverified.

### [MINOR] C-15: 3P agent profile degradation untested
The specialist profile spec says a 3P (external-CLI) child degrades to fire-and-collect. This degradation path is not tested.

---

## 2. EDGE CASE COVERAGE — Missing Edge Cases

### [CRITICAL] E-01: No race condition tests
The plan has zero tests for concurrent operations on the same session:
- `steer` + `cancel` simultaneously on the same session.
- `inbox_ack` while new messages are still arriving from the child.
- `follow_up` called while the session is still running (not completed).
- Two `steer` calls in quick succession — ordering, last-wins, or queueing?
- `peek` + `cancel` concurrently.
- `inbox` drain + `inbox_ack` interleaved with child writing new messages.

### [CRITICAL] E-02: No resource-limit / scale tests
- Many concurrent background bash sessions (what's the cap? what happens at the limit?).
- Many concurrent delegations to the same agent.
- Very large bash output (buffer/memory limits — does `read` truncate? cap?).
- Very long task prompt text (is there a max length?).
- Large `message_ids` array in `inbox_ack`.
- Many tasks in a single plan.

### [MAJOR] E-03: Invalid input edge cases missing
- `delegate` with empty `task` text (empty string).
- `delegate` with empty/blank `agent_id`.
- `bash` with empty `command`.
- `create_task` with empty `title` or title > 200 chars (the spec says 1-200 chars).
- `create_task` with empty `prompt`.
- `update_task` with invalid/non-existent `task_id`.
- `update_task` reassigning to non-existent `agent_id`.
- `bash` with `timeout_seconds` of -1, 0, 3601 (out of range).
- `delegate` with `timeout_seconds` > 3600.
- `create_task` with `priority` of 0 or 6 (out of 1-5 range).
- `create_task` with malformed `due` date (not RFC 3339).

### [MAJOR] E-04: Boundary-value edge cases missing
- `bash` `timeout_seconds=1` (minimum) — does a 1-second timeout actually fire?
- `bash` `timeout_seconds=3600` (maximum) — accepted?
- Chain depth exactly AT the limit (depth 3 if max is 3) — is depth-3 allowed or denied? The plan only tests exceeding, not the boundary.
- `create_task` `title` of exactly 200 chars (boundary) and 201 chars (over).
- `inbox` `max=1` (minimum meaningful) and `max=0`.

### [MAJOR] E-05: Permission/security boundary cases missing
- `bash` command attempting `..` path escape (e.g., `cat ../../etc/passwd`) — should be rejected.
- `bash` with absolute path in `cwd` — should be rejected.
- `delegate` to an agent_id not in the allowlist (distinct from non-existent — EC-01 tests nonexistent, but allowlist denial is a different path).
- `bash` command that is a no-op or exits immediately (exit code 0, no output) — does `read`/`poll` handle gracefully?
- `bash` command that exits with non-zero immediately.

### [MAJOR] E-06: Timing edge cases for steer/respond missing
- `steer` called before the subagent has made its first tool call (is it queued? lost?).
- `steer` called after the subagent's final tool call but before completion (race between steer and finish).
- `respond` called when no question is pending (correlation_id matches a session but no open question).
- `respond` called after the question has timed out / child has moved on.

### [MINOR] E-07: `inbox_ack` partial-failure case missing
EC-08 tests all-invalid message_ids. Missing: a mix of valid + invalid message_ids in one call — does it ack the valid ones and error on the invalid, or reject atomically?

### [MINOR] E-08: `follow_up` on a running session missing
EC-05 tests follow_up on a failed session. Missing: follow_up on a session that is still running (not completed). What's the expected behavior — error, queue, or allow?

### [MINOR] E-09: Sync delegate timeout behavior missing
`delegate(async=false)` that runs longer than `timeout_seconds` — does it return an error? Does the child get cancelled? The plan doesn't test sync-delegation timeout.

### [MINOR] E-10: `list_jobs` empty-state case missing
`list_jobs()` when there is zero in-flight work — does it return an empty list or error?

---

## 3. TEST DESIGN QUALITY

### [CRITICAL] D-01: TC-03 uses a placeholder for criteria
`criteria=[...]` is not a real value. The test cannot be executed as written. The criteria content matters because it defines the Definition-of-Done and affects `execute_plan` / `run_task` judging.

### [MAJOR] D-02: TC-04 "Status transitions are logical" is untestable
"queued -> running -> completed" is asserted but not pinned. What if the system skips `queued` (goes straight to `running`)? Is that a pass or fail? The expected status sequence must be enumerated exactly, including which transitions are required vs optional.

### [MAJOR] D-03: TC-05 "without side effects" has no verification method
"Verify: subagent continues running normally after peek" — but how? There's no defined method to detect side effects. Need: capture state (status, message count, output) before peek, compare after. The test should specify what state to compare.

### [MAJOR] D-04: TC-10 is timing-dependent with no timing control
"Dispatch a long-running subagent" — how long? What if it finishes before the steer call lands? The test has no mechanism to guarantee the subagent is still running when `steer` is called. Needs: a subagent task that blocks on a question or sleeps for a known duration, with the steer called within a specific window.

### [MAJOR] D-05: TC-11 "Messages are correctly typed and ordered" is vague
What is the expected order? FIFO by creation time? By type priority? The test doesn't define the expected ordering rule, so it cannot fail in a well-defined way.

### [MAJOR] D-06: TC-20 and TC-21 "Behavior matches profile contract" is circular
"Verify: behavior matches utility profile contract" — the contract IS the thing being tested. The test must enumerate concrete observable differences: e.g., "utility profile: no checkpoint messages delivered to inbox; steer returns error or no-op; specialist profile: checkpoint messages ARE delivered; steer succeeds." Without concrete assertions, these are not tests, they are vibes.

### [MAJOR] D-07: TC-06 "All in-flight work is visible" has no completeness oracle
How do you know all work is visible if you don't have an independent count? The test should create a known number of jobs (e.g., 3 subagents + 1 plan + 2 tasks) and assert the exact count and identifiers returned.

### [MINOR] D-08: No latency/performance assertions
No test measures response time. For a monitoring toolset, `status` and `list_jobs` latency under load matters. At minimum, flag if any call takes > N seconds.

### [MINOR] D-09: EC-09 "max depth 3" assumes an unverified constant
The plan asserts the chain depth limit is 3, but doesn't verify what the actual configured limit is. If the real limit is different, the test is wrong. Should first probe the actual limit, then test at boundary and boundary+1.

### [MINOR] D-10: EC-13 doesn't verify session cleanup
"Poll shows timeout/terminated" — but does the session get cleaned up? Does a subsequent `read` on a timed-out session error gracefully? The test stops too early.

---

## 4. ORDERING & DEPENDENCIES

### [MAJOR] O-01: Hidden dependencies are not declared
- TC-04 depends on TC-01's task_id.
- TC-05, TC-10, TC-14, TC-15 depend on a "long-running subagent" that must still be running.
- TC-12 depends on TC-11's message_ids.
- TC-16 depends on a completed subagent from an earlier test.
- EC-03, EC-04, EC-05 depend on completed/failed subagents.
These dependencies are implicit. The plan says "execute in isolation where possible" but never resolves the contradiction with tests that explicitly require prior state.

### [MAJOR] O-02: No setup or teardown phase
No defined setup (ensure worker agent exists, baseline empty state, known configuration) and no teardown (kill orphaned background sessions, cancel orphaned subagents, delete test tasks/plans). Failed tests will leave orphaned processes and pollute subsequent test runs.

### [MAJOR] O-03: State pollution between tests
TC-03 (create_task) and EC-12 (create_plan) create durable state that will appear in `list_jobs` (TC-06, TC-07, TC-08, TC-09, EC-11). If these run after the creation tests, the "all in-flight work" assertion is contaminated. No isolation strategy is defined.

### [MINOR] O-04: No grouping by independence
Tests should be grouped into independent suites (dispatch suite, monitoring suite, control suite, bash suite) with explicit inter-test dependencies declared within each suite. The flat numbering obscures the dependency graph.

### [MINOR] O-05: Edge cases should specify which happy-path test they attach to
EC-03/04/05 attach to a completed/failed subagent but don't say which test produces that subagent. Should reference a specific TC.

---

## 5. RISKS

### [CRITICAL] R-01: Timing-dependent tests are fragile and likely to flake
TC-05, TC-10, TC-14, TC-15, TC-17, TC-18 all depend on "long-running" subagents or specific sleep durations. There is no control over subagent execution speed. A subagent may finish in <1 second, making steer/cancel/peek tests impossible to execute as written. No retry strategy, no flake mitigation, no minimum-duration guarantee for the subagent task.

### [MAJOR] R-02: No precondition/environment spec
The plan doesn't state what environment is required: which agents are configured, what the chain-depth limit actually is, whether the worker agent supports delegation (for EC-09), what the default timeout is. Tests may fail for environmental reasons that look like product bugs.

### [MAJOR] R-03: Orphaned resources on test failure
If TC-14 (soft cancel) fails mid-test, the subagent may keep running. If TC-19 (kill) fails, the sleep-60 session lingers. No cleanup-on-failure path is defined. Over a full run, this could accumulate orphaned processes and skew later results.

### [MAJOR] R-04: EC-09 (chain depth) depends on subagent cooperation
The test requires delegated agents to themselves delegate. This depends on the worker agent having delegation capability and being configured to allow it. If the worker can't delegate, the test can't run. This precondition is not checked.

### [MINOR] R-05: Non-deterministic message ordering
TC-11 asserts message ordering, but if messages arrive concurrently from the child, ordering may be non-deterministic. The test may flake.

### [MINOR] R-06: No bug-severity rubric
"File bugs for any deviation" — but no severity classification. A cosmetic deviation and a data-loss bug get the same treatment. Need a severity rubric to triage findings.

---

## 6. OVERALL ASSESSMENT

### Verdict: NOT FIT FOR PURPOSE as a comprehensive UAT

The plan covers the delegation happy path and a reasonable set of basic edge cases, but it has three structural failures:

1. **Missing tool coverage is severe.** Four core orchestration tools — `update_task`, `create_plan`, `execute_plan`, `run_task` — are entirely absent from happy-path testing. These are not minor parameters; they are distinct tools with distinct semantics. A UAT that doesn't test plan execution or task mutation cannot certify the orchestration toolset.

2. **Timing-dependent tests are unexecutable as written.** Multiple tests depend on "long-running subagents" with no mechanism to guarantee the subagent is still running when the test action fires. These will flake or be impossible to execute.

3. **Pass/fail criteria are too vague for half the cases.** "Status transitions are logical," "without side effects," "matches profile contract" are not testable assertions. A UAT must have binary pass/fail per case.

### Single Most Important Improvement

**Add a dedicated test suite for the plan/task management lifecycle (`create_plan` -> `create_task` with dependencies -> `execute_plan` -> `run_task` with retry/steering loop -> `update_task` status transitions), with concrete, observable pass/fail criteria for each step.** This closes the largest coverage gap (four untested core tools) and forces the specificity that the existing timing-dependent tests lack. Without this, the UAT certifies only half the toolset.
