# Critical Review: UAT Plan for Delegation/Monitoring/Communication Tools

**Reviewer:** Ray (Scout)
**Date:** 2026-07-31
**Source:** `uat-plan.md`

---

## 1. COMPLETENESS — Missing tools/actions/parameters

- **[CRITICAL] `message_parent` is entirely absent.** The plan scopes itself to "communication tools" but omits the child→parent push channel (`message_parent` with kinds progress/checkpoint/artifact/blocker/question/handback). This is the inverse of `inbox`/`inbox_ack` and is untestable in isolation without it, yet it is never mentioned. The inbox tests (TC-11/12) assume messages exist but never verify the producing side.
- **[CRITICAL] `create_plan`, `execute_plan`, `stop_plan` are missing.** The plan claims to cover "the full delegation lifecycle" and `list_jobs` filters by `kind="plan"`, but no test creates, executes, or stops a plan. EC-12 references a "draft plan" with no setup step that produces one. The plan DAG is half-covered.
- **[MAJOR] `run_task` (standalone task execution) is not tested.** `create_task` is filed (TC-03) but the actual execution driver `run_task` — which dispatches and runs the attempt loop — is never invoked. Filing a task and never running it tests only persistence, not the execution path.
- **[MAJOR] `delegate(action="run")` is never explicitly tested as the default action.** TC-01/02 rely on the default `action="run"` but never assert it. A regression where the default action breaks while explicit `action="run"` works would pass this plan.
- **[MAJOR] `snapshot` parameter on `delegate` is untested.** The `delegate` tool accepts a discretionary `snapshot` (notes, references) passed to the child. No test verifies the child actually receives curated context vs. deny-by-default behavior, or that over-cap snapshots are rejected (not truncated).
- **[MAJOR] `critical` flag on `delegate` is untested.** The plan never tests that a `critical=true` subagent continues after the parent finishes gracefully.
- **[MAJOR] `since_cursor` on `inbox` is untested.** This is the pagination/delta mechanism for incremental inbox drains — essential for long-running sessions. Without it, TC-11 only tests a single full drain.
- **[MAJOR] `max` parameter on `inbox` is untested.** Bounded drain behavior is never verified.
- **[MINOR] `allow_blocking_question` on `delegate(async=false)` is untested.** The plan tests sync dispatch but never the human-routed wait path for child questions.
- **[MINOR] `list_jobs` `limit` and clamping behavior untested.** The tool caps at 200 rows and clamps; no test verifies pagination or the 75-row terminal threshold behavior.
- **[MINOR] No test for `delegate` to `researcher` agent.** All delegation tests use `worker`. The `researcher` delegation target is never exercised.

---

## 2. EDGE CASE COVERAGE — Missing race conditions, resource limits, invalid inputs, security boundaries

- **[CRITICAL] No concurrency/race-condition tests.** EC-10 tests two concurrent delegations to the same agent, but there is no test for: concurrent `steer` + `cancel` on the same session; concurrent `inbox_ack` of the same message from two callers; `follow_up` racing with `cancel`; or `peek` racing with session termination. These are the realistic failure modes for a multi-action control plane.
- **[CRITICAL] No resource-limit tests.** The `list_jobs` tool documents hard caps (25 queued / 25 running / 25 blocked / 20 terminal, 75-row terminal limit requiring `include_terminal`). No test saturates these limits to verify enforcement, clamping, or error behavior. Similarly, `snapshot` has an over-cap rejection path that is never tested.
- **[MAJOR] No test for `delegate` with both `task_id` and `session_id` present.** The tool spec notes `session_id` wins when both are present — this collision behavior is untested.
- **[MAJOR] No test for `delegate(action="status")` with no `task_id`/`session_id`.** The spec says omitting both lists all visible tasks. This is a distinct code path from filtered status.
- **[MAJOR] No invalid-input tests for `create_task`.** Missing: empty title, empty prompt, invalid `agent_id`, malformed `criteria`. EC-16 only tests empty criteria.
- **[MAJOR] No test for `list_jobs` with invalid `kind` or `status` values.** What happens with `kind="invalid"` or `status="bogus"`?
- **[MAJOR] No test for `bash` with invalid/empty `command`.** No test for shell injection vectors (the plan tests `sleep` and `echo` only).
- **[MAJOR] No test for `delegate` with empty `task` string.**
- **[MINOR] No test for `inbox_ack` with empty `message_ids` array.**
- **[MINOR] No test for `follow_up` on a session that was hard-cancelled (vs. soft-cancelled vs. completed).** EC-05 only covers failed; cancelled is a distinct terminal state.
- **[MINOR] No test for `steer` on a session that has not yet started (queued state).**
- **[MINOR] No security-boundary test for `browser_navigate` SSRF protection** — out of declared scope but the plan mentions browser tools nowhere despite them being part of the agent's toolset.

---

## 3. TEST DESIGN QUALITY — Vague or untestable pass/fail criteria

- **[CRITICAL] TC-03 pass criterion is untestable as written.** "Task is trackable and assignable" — no concrete verification step. What does "assignable" mean operationally? No assertion that the task actually executes.
- **[CRITICAL] TC-20 / TC-21 (profile tests) are circular.** TC-20 says "Verify: Behavior matches utility profile contract" and TC-21 says "Verify: Checkpoints and child messages are delivered." But the plan never defines what the "utility profile contract" or "specialist profile contract" actually requires. These are tautological — they pass if the system behaves like itself.
- **[MAJOR] TC-04 "Status transitions are logical" is vague.** Which transitions are valid? Is `queued → completed` (skipping `running`) acceptable? Is `running → queued` (re-queue) possible? No state machine is defined.
- **[MAJOR] TC-05 "Subagent continues running normally after peek" — no definition of "normally."** How is this measured? No assertion on output or final status.
- **[MAJOR] TC-10 "Final output reflects the steered instruction" — no concrete assertion.** What exact string should appear? The test injects "Add 'steered' to your output" but never asserts the literal substring "steered" is present.
- **[MAJOR] TC-14 "Status shows cancelled/failed after grace period" — which one?** Cancelled and failed are different terminal states with different semantics. The test accepts either, masking bugs.
- **[MAJOR] TC-15 same issue — "cancelled/failed immediately."** Ambiguous.
- **[MINOR] TC-11 "Messages are correctly typed and ordered" — ordered how?** Chronologically? By priority? No ordering spec.
- **[MINOR] EC-05 "Either resumes with retry or clear error" — accepts two contradictory outcomes.** This makes it impossible to fail: any behavior passes.

---

## 4. ORDERING & DEPENDENCIES — Hidden dependencies, setup/teardown

- **[CRITICAL] No teardown/cleanup strategy.** The plan creates tasks, subagents, background bash sessions, and (implicitly) plans, but never specifies cleanup. Residual state from TC-01 will pollute TC-06 (list_jobs dashboard) and every subsequent `list_jobs` test. Test isolation is claimed in Execution Notes but not enforced.
- **[CRITICAL] EC-12 (draft plan) has no setup.** It says "Create a draft plan" but no preceding test case creates one. `create_plan` is not in the test list. This test cannot execute.
- **[MAJOR] TC-04 depends on TC-01 but doesn't state the dependency explicitly.** TC-05, TC-10, TC-11, TC-14, TC-15 all require a "long-running subagent" but no test case defines how to create one (what task produces a long run?). This is a hidden setup dependency.
- **[MAJOR] TC-12 depends on TC-11's message_ids but doesn't specify how to capture them.** The ack test needs real message IDs from a prior drain — the handoff is implicit.
- **[MAJOR] TC-13 (respond to question) requires a subagent that asks a question, but no test case defines a task prompt that triggers a question.** This is a hidden dependency on subagent behavior that may not be deterministic.
- **[MAJOR] EC-09 (chain depth) requires cooperative nested delegation but provides no concrete task prompt that causes a child to delegate further.** The test is aspirational.
- **[MINOR] No defined execution order.** The plan says "edge cases after happy paths" but doesn't sequence the happy paths. TC-16 (follow-up) must run after a completed subagent — which one?

---

## 5. RISKS — What could go wrong during execution

- **[CRITICAL] State pollution between tests.** Without teardown, completed/failed/cancelled sessions accumulate. `list_jobs` tests (TC-06 through TC-09, EC-11) become unreliable because prior test artifacts appear in results. False positives and false negatives both likely.
- **[CRITICAL] Non-deterministic subagent behavior.** TC-10 (steer), TC-11 (inbox messages), TC-13 (respond to question) all depend on the subagent producing specific behaviors (long runtime, emitting messages, asking questions). If the subagent finishes too fast or doesn't ask a question, the test cannot execute. No fallback or controlled prompt is provided.
- **[MAJOR] Timing sensitivity.** TC-05 (peek during long run), TC-14 (soft cancel grace), TC-17/18 (background bash polling) all depend on the subagent/bash being in a specific state at call time. No synchronization mechanism (wait-for-state) is specified. Flaky tests likely.
- **[MAJOR] EC-09 chain-depth test may be impossible to trigger.** If the worker agent doesn't autonomously delegate, the test can't reach depth 3. The plan acknowledges this ("requires cooperation") but provides no mitigation.
- **[MAJOR] No bug-severity triage guidance.** Execution Notes say "file bugs for any deviation" but provide no severity rubric. A cosmetic status-label mismatch and a data-loss cancel bug would be filed identically.
- **[MINOR] No environment reset procedure.** If tests are run against a workspace with pre-existing jobs, `list_jobs` baseline is unknown.
- **[MINOR] Background bash tests (TC-17–19, EC-13/14) assume `bash` tool availability and shell semantics** but don't verify the sandbox permits `sleep` or `seq`.

---

## 6. OVERALL ASSESSMENT

**Verdict: Not fit for purpose as a formal UAT plan. Usable as a draft checklist that needs significant rework.**

The plan covers the happy paths of the core delegation lifecycle reasonably well and demonstrates awareness of the tool surface. However, it has three structural defects that disqualify it from being a reliable acceptance gate:

1. **Incomplete tool coverage** — entire tools (`message_parent`, `create_plan`/`execute_plan`/`stop_plan`, `run_task`) and major parameters (`snapshot`, `critical`, `since_cursor`, `max`) are absent despite being in declared scope.
2. **Untestable pass criteria** — at least 6 test cases have circular or ambiguous success conditions that cannot fail, making them incapable of catching bugs.
3. **No isolation or teardown** — state pollution across tests makes the `list_jobs` family and all post-completion edge cases unreliable.

**Single most important improvement:** Define a concrete, deterministic setup/teardown protocol — including a workspace reset before the suite, a controlled "long-running subagent" task prompt (e.g., a task that sleeps or loops on a condition), explicit message-producing and question-asking prompts for TC-11/13, and cleanup of every created session/task/plan after each test. Without deterministic setup and teardown, every other improvement is built on sand.

---

*Review complete. 6 areas covered, 38 findings (8 CRITICAL, 17 MAJOR, 13 MINOR).*
