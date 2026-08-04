# UAT Report — Batched Re-Run of Delegation/Task/Plan Tools

**Date:** 2026-07-31
**Tester:** Jim (Orchestrator), driven via Playwright browser automation against the live chat UI
**Workspace:** `01KYVTATD0V59Z1HH4J6J0DX2H` ("My Workspace") — see "Environment note" below for why this differs from the workspace ID in the original briefing
**App:** `omnipus-uat-swimlane` (Fly.io, machine `7812791a464028`)
**Plan executed:** `docs/internal/uat/uat-plan-delegation-tools-v2-2026-07-31.md` (TC-01–TC-38, EC-01–EC-12)
**Purpose:** Re-run the full delegation-tools UAT plan in 9 small, isolated chat sessions (instead of one ~47-minute/~4,700-tool-call session) to test whether batching is a viable interim mitigation for GitHub issue #573 (chat UI freezes under heavy subagent/delegation traffic), and to check whether the 9 previously-filed bugs (#576–#584) still reproduce.

---

## Environment note (read first — this materially affects how to read this report)

Before this run started, the box was restarted (`fly machine restart 7812791a464028`) to clear a leaked task-scheduler concurrency counter. The restart was believed to be non-destructive. It was not: **this Fly app has no persistent volume**, so the restart wiped `$OMNIPUS_HOME` entirely — the original hand-configured workspace (`01KYVJCB2S577KQC5HK5X23EKX`), its 256 queued tasks, all sessions, and `credentials.json` were all destroyed. This was confirmed directly via `flyctl ssh console` (fresh `config.json` with `agents.list: []`, `onboarding_complete: false`, all files under `.omnipus` timestamped to the restart second) and independently by the dispatching agent.

Recovery: onboarding was re-run from scratch (admin/admin123, OpenRouter provider, `z-ai/glm-5.2` model). The gateway's fresh-install auto-seed created a **new default workspace** (`01KYVTATD0V59Z1HH4J6J0DX2H`, "My Workspace") with an 8-agent `core_team` (mia, jim, ava, ray, worker, planner, explorer, researcher) and 9 delegation edges — structurally identical to what the original workspace needed (jim→ava, jim→ray, jim→worker, ava→worker, ray→worker, ray→researcher, planner→explorer, planner→researcher, mia→worker; worker is a leaf with no outgoing edges). This is system default-seed behavior, not a workspace I hand-built, so all 9 batches below ran against this workspace rather than the one named in the original briefing. All batch results should be read as applying to this equivalent, re-seeded workspace.

This is itself the most significant new finding of this exercise — see [Finding N1](#n1-no-persistent-volume-any-restart-discards-all-data) below.

---

## Executive Summary

All 9 planned batches ran to completion. **62 PASS / 15 FAIL / 5 BLOCKED** across 82 test cases. **The chat UI did not fully freeze in any of the 9 batches** — no batch reproduced the original #573 symptom (approval dialogs stop responding, page stops re-rendering). All 6 of the 9 previously-filed bugs that this plan can directly exercise reproduced identically and consistently (#576, #577, #578, #579, #582, #584); 2 were not re-exercised by this plan's test design (#580, #583 — see [Known-issue status](#known-issue-reproduction-status)); 1 (#581) showed circumstantially consistent but not cleanly isolated behavior.

However, **batching alone is not a clean mitigation**. Three batches (1, 3, 7) triggered severe model-side retry loops that:
- ballooned a single "batch" turn from an expected ~10–20 tool calls to hundreds, and in one case (Batch 1) to **5.3M tokens in one turn**;
- left the backend agent loop for that session **running long after "Stop generation" was clicked** — in Batch 3's case, a self-referential `recall_conversation` loop was still executing **20+ minutes** after the first stop attempt, and a similar stuck loop was observed for Batch 7 as late as report time;
- caused stray completion notifications from these stuck sessions to **bleed into subsequently-opened, unrelated "new chat" sessions** (confirmed recurring into Batches 4, 6, and 7's views), undermining the assumption that starting a new chat cleanly isolates a batch from prior activity.

So the honest answer to "does batching prevent the freeze" is: **yes, for the specific hard-freeze failure mode of #573, in this run** — but batching's protection depends on the previous turn actually having stopped, and this exercise shows that assumption does not reliably hold when the model (not the operator) is what drives runaway tool-call volume within a single turn.

---

## Summary Table

| Batch | Suite | Test cases | PASS | FAIL | BLOCKED | UI stayed responsive | Notable |
|---|---|---|---|---|---|---|---|
| 1 | Dispatch basics | 11 (TC-01–TC-06 incl. sub-variants) | 8 | 1 | 2 | **Y** (but see notes) | Model retry-loop on `create_task` optional params → 5.3M tokens, ~7-item approval-dialog backlog, real orphan tasks created |
| 2 | Monitor | 8 (TC-07–TC-10) | 8 | 0 | 0 | **Y** | Clean, 256.9k tokens, reproduced #577 via a late "straggler" completion |
| 3 | Communicate | 17 (TC-11–TC-16c) | 13 | 3 | 1 | **Y** | Session ballooned to 2,341 transcript lines; got stuck in a persistent background loop that outlived multiple Stop clicks |
| 4 | Control (cancel) | 3 (TC-15–TC-15c) | 1 | 2 | 0 | **Y** | Confused briefly by Batch 3's stray bleed-through; own results were clean once isolated |
| 5 | Plan/Task Lifecycle | 10 (TC-17–TC-22e) | 5 | 4 | 1 | **Y** | Clean, 718.4k tokens, no runaway |
| 6 | Background Bash | 12 (TC-23–TC-30b) | 11 | 1 | 0 | **Y** | Clean, 401.4k tokens; revealed a `max_tool_iterations` circuit breaker exists |
| 7 | Profiles & Params | 9 (TC-31–TC-38) | 7 | 2 | 0 | **Y** | Own retry-loop on TC-33b's oversized string; session still running a background loop at report time |
| 8 | Edge cases (cheap) | 9 (EC-01–EC-08, EC-12) | 6 | 2 | 1 | **Y** | Fastest, cleanest batch (~45s); 2 genuinely new findings |
| 9 | Edge cases (race/concurrency) | 3 (EC-09–EC-11) | 3 | 0 | 0 | **Y** | Clean; confirms #577 again under a real concurrent steer-vs-cancel race |
| **Total** | | **82** | **62** | **15** | **5** | **9/9** | |

---

## Per-Batch Detail

### Batch 1 — Dispatch basics (TC-01–TC-06)

| TC | Result | Notes |
|---|---|---|
| TC-01 | PASS | Async dispatch, session_id returned immediately, polled to completed, result "hello" |
| TC-02 | PASS | Sync dispatch blocked and returned "hello" inline |
| TC-03 | PASS | Task created (status "next"), visible in `list_tasks(role="delegator")` and `list_jobs(kind="task")` |
| TC-03b | **FAIL (new)** | `create_task` does not expose `due`/`stream`/`write_set`/`is_join`; `create_task_in_workspace` exposes those but not `priority`. No single tool call can set all "rich" params together as the test case assumed. See [Finding N6](#n6-create_task--create_task_in_workspace-have-an-inconsistent-rich-parameter-surface). |
| TC-03c | PASS | Empty `criteria=[]` rejected: "criteria is required... ADR-049 D5/SD-A7" |
| TC-03d | PASS | 200-char title accepted; 201-char title rejected ("must be 200 characters or fewer") |
| TC-03e | BLOCKED | Model (z-ai/glm-5.2) could not construct a `create_task` call that included `priority` — omitted it on every retry across ~20 attempts. Not a server-side result; a client/model tool-invocation issue. |
| TC-03f | BLOCKED | Same tool-invocation issue, for `due` |
| TC-04 | PASS | running→completed, no backward transitions |
| TC-05 | PASS | Peek returned a result without disturbing state (running before/after); subagent completed normally |
| TC-06 | PASS | 3 subagents + 1 task all visible in `list_jobs()`; all had status/attention/actionable/notes fields; teardown cancelled all 3 |

**What actually happened in this session:** while retrying TC-03e/TC-03f, the model got stuck in a genuine loop — repeatedly calling `create_task` without the optional params it intended to include, each time actually creating a new (valid, if incomplete) task record. This is confirmed by direct filesystem inspection: `find /home/omnipus/.omnipus/tasks -type f | wc -l` returned real leftover task files after the fact, and the sidebar's session list showed ~10 distinct "UAT rich task"/"UAT_LABEL_TEST case"-titled sessions spawned during this loop. The model then unprompted decided to clean up after itself, firing dozens of `delete_task`/`delete_task_in_workspace` calls, each individually consent-gated — this produced a real approval-dialog backlog that peaked at **7 pending dialogs simultaneously**. Clicking through them (and eventually using "Always Allow" once, though that only suppressed dialogs for future identical-argument calls, not all future calls) worked without the UI ever becoming unresponsive, but the turn's cost had already reached **5.3M tokens** — a specific single-turn token count vastly larger than any other batch's (next-highest: Batch 5 at 718.4k). Model narration self-reported "~139 leftover tasks" during this loop, but direct on-disk inspection at the time found only ~10 real task files (the model's own internal count was not reliable — see [Finding N13](#n13-model-self-reported-counts-are-not-reliable-ground-truth)).

I ended the turn with "Stop generation" once the final PASS/FAIL/BLOCKED table had been delivered; this worked cleanly for Batch 1's own turn (unlike Batch 3, see below).

### Batch 2 — Monitor (TC-07–TC-10)

| TC | Result | Notes |
|---|---|---|
| TC-07 | PASS | `list_jobs(kind=...)` for subagent/task/plan each returned only that kind |
| TC-08 | PASS (test), also **reproduces #577** | Dispatched, `list_jobs(status="running")` showed it; hard-cancel returned "failed" status immediately in `list_jobs(status="failed", include_terminal=true)` — but a **late "straggler" completion notification arrived afterward showing the sleep-45 command had actually run to full completion and printed `LONG_RUN_COMPLETE` regardless of the cancel**. This is direct evidence that hard cancel reports success without actually terminating execution. |
| TC-09 | PASS | Label search `label_contains="UAT_LABEL"` returned only the matching row |
| TC-09b | PASS | Combined kind+status+label filters applied simultaneously, no cross-contamination |
| TC-09c | PASS | No-filter call returned a well-formed rows array (workspace already had clutter from Batch 1, so an explicit "empty" assertion was not used — correctly adapted per the guardrail given) |
| TC-09d | PASS | Default (no `include_terminal`) excluded the cancelled TC-08 job |
| TC-09e | PASS | `include_drafts=true` showed a freshly-created draft plan; `include_drafts=false` excluded it |
| TC-10 | PASS | `role="assignee"` returned 0 (nothing assigned to jim); `role="delegator"` returned 10 (created for worker); `matched`/`returned` fields present |

Clean session throughout — 256.9k tokens, no approval dialogs, no stray content, ended naturally with the model's own final summary.

### Batch 3 — Communicate (TC-11–TC-16c)

| TC | Result | Notes |
|---|---|---|
| TC-11 | **FAIL — reproduces #576** | Specialist-profile worker's `message_parent` calls fail with "no durable session record for this session"; inbox always empty |
| TC-11b | PASS | `inbox(max=1)` returned ≤1 message (0, since inbox was empty), no error |
| TC-11c | PASS | `since_cursor` incremental drain returned no duplicates |
| TC-11d | PASS | Utility-profile task: inbox null/empty, no error |
| TC-12 | BLOCKED | No real `message_id`s available (downstream of #576) |
| TC-12b | PASS | `inbox_ack` with a fake ID returned a graceful "Acknowledged N message(s)" no-op, no crash |
| TC-12c | PASS (with note) | Mixed valid+invalid IDs — no real ID was available, so only fake IDs were tested; both acknowledged gracefully |
| TC-13 | **FAIL — reproduces #576** | Same root cause: specialist-profile `message_parent` failure means the question never reaches the inbox, so `respond` has nothing to answer |
| TC-13b | PASS | `respond` with a fake `correlation_id` returned a clear error, no crash |
| TC-13c | PASS | `respond` on a session that never asked a question returned "not parked on correlation_id ...", no crash |
| TC-14 | PASS | Steer on a running long-task: final result contained literal "STEERED" |
| TC-14b | PASS | Empty-text steer rejected with a clear error |
| TC-14c | PASS (mostly) | Steering within 1s of dispatch mostly succeeded and was reflected in the result; one run out of several showed the steer silently not applied (task completed without the steered text) — behavior consistent with, but not a clean isolated reproduction of, the TOCTOU race described in #581 |
| TC-14d | PASS | Steer on a completed session returned a clear "cannot steer a finished session"-type error |
| TC-16 | **FAIL — reproduces #579**, plus a new observation | `follow_up` resumed the same `session_id` correctly but the result did not incorporate the new instruction ("first" instead of "goodbye"). Additionally, the model's own narration across several attempts described `follow_up` as triggering "a recursive loop" / "the follow-up prompt got nested repeatedly" — this is a **possible elaboration of #579 worth independent code-level investigation**, but was not independently verified against server logs within this exercise; treat as an unconfirmed lead, not a new confirmed bug. |
| TC-16b | PASS (with note) | `follow_up` on a hard-cancelled session did not error — it warm-resumed/re-dispatched the session instead (documented actual behavior, acceptable per the plan) |
| TC-16c | PASS | `follow_up` on a still-running session returned a clear error ("is not terminal... follow_up only resumes a finished session") |

**What actually happened in this session (the most consequential batch of the run):** the model repeatedly re-verified test cases it had already resolved (the raw transcript shows TC-14 alone reported ~18 times with minor wording variations, TC-16 ~10 times), driving the session to **2,341 transcript lines** — by far the largest of any batch, and comparable in scale to a meaningful fraction of what caused the original #573 incident (~4,700 tool calls total). Server logs show `windowTrim: evicted oldest Turns from live window` firing with `kept_msgs=800`, `turns_evicted=106` — i.e., the live context window itself had grown large enough to require active eviction, something that did not happen in any other batch. When I clicked "Stop generation," the logs recorded **more than a dozen simultaneous `streaming read error: http2: client connection lost` events in the same second** — direct evidence that many concurrent LLM streaming connections (one per actively-running sub-turn) were open at once, not just one.

Critically, the backend agent loop for this session **did not actually stop**. Repeated checks over the following ~30 minutes showed `session_key=agent:jim:session:session_01KYVVSXNWD0TEZ69RQ0EEMPWB` continuing to log `recall_conversation: span installed ... turns=1` roughly every 3 seconds, each time with an incrementing token count — a self-referential loop that outlived the "Stop generation" click, outlived navigating to "New chat," and outlived three subsequent batches being started and completed. Its stray "Batch 3 is already complete" notifications were confirmed bleeding into the views for Batches 4, 6, and 7 (see [Finding N2](#n2-stop-generation-does-not-reliably-terminate-a-chat-turns-backend-agent-loop)). Given the earlier discovery that this Fly app has no persistent volume, restarting the process to clear the stuck loop was judged too risky (any process/machine restart appears to wipe all data) and was deliberately not attempted; the loop was still consuming resources as of the last check in this exercise (~11:04, roughly matching when it seems to have finally quieted — see the Batch 7 note below for a similar loop that was still active at report time).

### Batch 4 — Control: cancel (TC-15–TC-15c)

| TC | Result | Notes |
|---|---|---|
| TC-15 | **FAIL — reproduces #577** | Soft cancel (`hard=false`) issued at 3s; subagent ran to full completion (45s) regardless, reporting the real `LONG_RUN_COMPLETE` output |
| TC-15b | **FAIL — reproduces #577** | Hard cancel (`hard=true`) returned "hard-cancelled immediately" but the subagent again ran to full completion (~50s) with full output |
| TC-15c | PASS (with new minor note) | Cancel on an already-completed session did not corrupt state (stayed "completed") but returned a misleading "cooperatively cancelled" success message rather than a clear "session already terminal" error — see [Finding N9](#n9-cancel-on-an-already-terminal-session-returns-a-misleading-success-message) |

**Session isolation note:** partway through, this session's view in the browser began showing content that was actually from Batch 3's still-running stuck loop (a "self-delegation to worker is denied" message, which on closer inspection via the raw transcript came from `agent_id: "worker"` — a WORKER sub-agent correctly being denied for trying to delegate to itself, a tangential but correct behavior, not a bug). Once I re-fetched the session's own transcript file directly and let it settle, Batch 4's actual final summary (screenshotted) was clean and matched the table above. 353.7k tokens for the batch overall — reasonable, no runaway of its own.

### Batch 5 — Plan/Task Lifecycle (TC-17–TC-22e)

| TC | Result | Notes |
|---|---|---|
| TC-17 | PASS | `create_plan` succeeded, draft status, visible via `list_jobs(kind="plan", include_drafts=true)` |
| TC-18 | **FAIL — reproduces #578** | `create_task(plan_id=...)` failed with "plan store is not configured" for both Task A and Task B; `blocked_by` itself worked correctly (Task B showed "blocked") |
| TC-19 | **FAIL — reproduces #578 (downstream)** | `execute_plan` failed: "has zero member tasks; nothing to execute" (a direct consequence of TC-18's failure) |
| TC-20 | BLOCKED | Dependent on TC-19 producing a running plan, which it did not |
| TC-21 | PASS | `run_task` on a criteria-designed-to-fail task: initially failed ("banana" ≠ "MAGIC"), retried with steering feedback, eventually passed and reached `done` — the retry/judge loop works as designed |
| TC-22 | **FAIL — reproduces #584** | Both `update_task` calls (status→in_progress, status→done) failed: "you can only update tasks assigned to you" (task was assigned to worker; Jim was the creator, not the assignee) |
| TC-22b | **FAIL — reproduces #584** | Same permission error blocked reassigning `agent_id` to "ray" |
| TC-22c | PASS | `update_task("fake-id-99999", ...)` returned a clear "task not found" error, no crash |
| TC-22d | PASS | Cycle detection correctly rejected an A→B→A cycle — tested via a workaround (self-assigned tasks, to route around the #584 permission gate that otherwise blocks reaching the cycle-check code path at all) |
| TC-22e | PASS | `blocked_by` update correctly **replaced** the prior list rather than merging with it |

Clean session, 718.4k tokens, no runaway, no stray bleed-through observed.

### Batch 6 — Background Bash lifecycle (TC-23–TC-30b)

| TC | Result | Notes |
|---|---|---|
| TC-23 | PASS | Foreground `echo 'hello bash'` blocked and returned inline, exit 0 |
| TC-24 | PASS | Background `sleep 10 && echo done`: session_id returned immediately, poll showed running→done |
| TC-25 | **FAIL — reproduces #582** | `for i in $(seq 1 5); do ...; done` blocked by the safety guard ("dangerous pattern detected") — the `$(seq...)` command substitution |
| TC-26 | PASS | Background `sleep 60`, killed; poll showed "killed", exit code -1 |
| TC-26b | PASS | Kill-already-killed returned a graceful no-op |
| TC-27 | PASS | `timeout_seconds=5` on a `sleep 300`: timed out at ~5s, poll/read showed "timeout" status, no output, no error |
| TC-28 | PASS | `cwd=""` → workspace root; `cwd="subdir"` → `<root>/subdir` |
| TC-28b | PASS | `cwd="../../etc"` and `cwd="/etc"` both rejected with clear path-escape errors |
| TC-29 | PASS | `timeout_seconds=1` accepted and ran; `timeout_seconds=3601` rejected ("must be between 1 and 3600") |
| TC-29b | PASS | `timeout_seconds=0` and `=-1` both rejected with the same range error |
| TC-30 | PASS | Background `true`: poll "done" quickly, read empty, no error |
| TC-30b | PASS | Background `false`: poll "done" with exit code 1, read empty |

**Summary reported by the model: 12 PASS, 1 FAIL, 0 BLOCKED** — the cleanest full-suite batch besides Batch 8/9. 401.4k tokens. Also surfaced a **positive** finding: after the final summary, a system message appeared — *"I've reached max_tool_iterations without a final response. Increase max_tool_iterations in config.json if this task needs more tool steps."* — confirming the platform does have a per-turn tool-iteration ceiling (see [Finding N4](#n4-a-max_tool_iterations-circuit-breaker-exists-and-does-eventually-fire)). The frontend's "Stop generation" button also got stuck showing "Stopping..." for well over a minute after the backend had actually gone quiet (per server logs), requiring a full page reload to clear — a separate, more minor UI-state-desync symptom from the "Stop doesn't work at all" pattern seen in Batch 3/7.

### Batch 7 — Profiles & Delegate Params (TC-31–TC-38)

| TC | Result | Notes |
|---|---|---|
| TC-31 | PASS | Utility profile: inbox empty as expected; steer returned a clear terminal-session error (task completed before steer could be tested mid-run) |
| TC-32 | **FAIL — reproduces #576** | Specialist profile: `message_parent` failed ("no durable session record"); inbox empty; peek returned only `{"state":"completed"}`, no checkpoint data |
| TC-33 | PASS | Snapshot `notes`/`references` passthrough: worker correctly quoted "TEST_SNAPSHOT_NOTE" and listed "uat-plan.md" |
| TC-33b | PASS | Over-cap snapshot notes (constructed as ~40KB) rejected with a clear error: "40307 bytes exceeds snapshot_max_bytes (8192) — narrow the snapshot" — not truncated, as intended |
| TC-34 | PASS | `critical=true` accepted without error, dispatched normally |
| TC-35 | **FAIL — reproduces #576** | Same `message_parent` failure blocked the blocking-question flow entirely |
| TC-36 | PASS | `timeout_seconds=0` used the default timeout (not zero); task completed normally |
| TC-37 | PASS | Both `task_id` and `session_id` passed to `status`; `session_id` won, no error |
| TC-38 | PASS | `status` with no ID returned the full visible list — **225 total dispatches at that point** (1 running, 208 completed, 14 failed, 2 canceled) — a useful cumulative-load data point for the whole exercise |

**Summary reported by the model: 7 PASS, 2 FAIL.** Constructing TC-33b's oversized string triggered the same kind of parameter-passing confusion loop seen in Batch 1 (repeated attempts, filler "AAAA..." content, eventually resolved via a bash-script workaround) — smaller in scale than Batch 1's but real. This session was **still actively running a `recall_conversation` self-loop as of the last check (11:12)**, well after `BATCH-7-COMPLETE` was delivered, and its stray output was confirmed bleeding into the view used to screenshot Batch 9's "final state" (the screenshot at that point showed Batch 7's summary table, not Batch 9's, despite Batch 9 having been sent and completed afterward — see [Finding N3](#n3-a-new-chat-does-not-isolate-you-from-a-still-running-prior-turn)).

### Batch 8 — Edge Cases: invalid inputs, boundary values, security (EC-01–EC-08, EC-12)

| TC | Result | Notes |
|---|---|---|
| EC-01 | PASS | `delegate(agent_id="nonexistent")` denied with a clear error, no subagent spawned |
| EC-02 | **FAIL (new)** | `delegate(agent_id="mia")` (an agent that exists but has no delegation edge from jim) and a genuinely nonexistent agent both produce the **same generic denial message** — the error does not distinguish "doesn't exist" from "not in allowlist." See [Finding N7](#n7-delegate-denial-errors-dont-distinguish-doesnt-exist-from-not-trusted). |
| EC-03 | PASS | `status(session_id="invalid-id-99999")` → clear error, no crash |
| EC-04 | PASS | `peek(session_id="fake-id")` → clear error |
| EC-05 | PASS | `delegate(task="")` → "task is required and must be a non-empty string" |
| EC-06 | **FAIL (new)** | `delegate(agent_id="")` was **silently accepted** and spawned a generic/default subagent instead of being rejected. See [Finding N8](#n8-delegate-silently-accepts-an-empty-agent_id-instead-of-rejecting-it). |
| EC-07 | PASS | `bash(command="")` → "command is required and must be a non-empty string" |
| EC-08 | BLOCKED (expected) | Worker is a leaf agent with no outgoing delegation edges in this workspace — chain-depth cannot be tested, as anticipated |
| EC-12 | PASS | `list_jobs(kind="invalid")` and `list_jobs(status="bogus")` both rejected with clear enum-validation errors (not silently empty) |

**This was the fastest and cleanest batch of the entire run — complete in well under a minute, no retries, no dialogs, no runaway.** This is the strongest single piece of evidence that pure single-call validation edge cases are cheap and safe to batch generously, exactly as anticipated. It also produced the two most concrete, independently-actionable new findings of the whole exercise (EC-02, EC-06).

### Batch 9 — Edge Cases: race conditions & resource limits (EC-09–EC-11)

| TC | Result | Notes |
|---|---|---|
| EC-09 | PASS | Two parallel async dispatches (`concurrent-A`, `concurrent-B`) both accepted, both completed independently with correct distinct results, no collision |
| EC-10 | PASS | Parallel `steer("Output RACESTEER")` + `cancel(hard=true)` on the same running session: no crash. **Steer won** — the hard cancel returned "hard-cancelled immediately" but had no actual effect; the session completed with "RACESTEER" applied. This is a real concurrent-race reproduction of #577 (cancel has no effect), not just a sequential one. |
| EC-11 | PASS | Two parallel steer calls ("Output FIRST", "Output SECOND") on the same session: no crash, both accepted ("Steering message queued" for each), and **both were applied** — the worker's final output contained both FIRST and SECOND. Documented as "both queued" behavior, not last-wins or error-on-second. |

Clean completion, confirmed via the session's own transcript file (the live browser view had already drifted to showing stale Batch 7 content by the time of the final screenshot — see Finding N3). This was the intended final batch and closes out the plan.

---

## Known-issue reproduction status

| Issue | Title | Status this run |
|---|---|---|
| #576 | message_parent fails for every delegated subagent | **Reproduced identically** — TC-11, TC-13, TC-32, TC-35, same error text each time ("no durable session record" / "may only be called from within a delegated child session") |
| #577 | delegate cancel (soft+hard) has no effect on the target subagent | **Reproduced identically, repeatedly** — TC-08 (Batch 2, via a late straggler notification), TC-15/TC-15b (Batch 4, direct), EC-10 (Batch 9, under an actual concurrent race). This is the most consistently reproduced of all 9 issues. |
| #578 | create_task(plan_id=...) always fails | **Reproduced identically** — TC-18/TC-19 (Batch 5), same "plan store is not configured" error, cascading to "zero member tasks" on `execute_plan` |
| #579 | delegate follow_up silently drops the new instruction | **Reproduced identically** — TC-16 (Batch 3), result contained the original text ("first") not the follow-up text ("goodbye"). Additional unconfirmed lead: model narration across retries suggested `follow_up` may cause recursive re-dispatch/nesting in some cases — not independently verified, flagged for code-level follow-up. |
| #580 | delegate timeout_seconds argument is never read | **Not re-tested this round.** TC-36 only tests the `timeout_seconds=0` → default-applies case (which passed as expected); this plan's test design does not include setting a non-zero override (e.g. `timeout_seconds=600`) and verifying whether it is actually honored versus silently ignored, which is what #580 specifically describes. Neither confirmed nor refuted here. |
| #581 | delegate steer on a just-completed session can silently no-op (TOCTOU race) | **Circumstantially consistent, not cleanly isolated.** TC-14c (steer within 1s of dispatch) mostly succeeded across several runs but one run showed the steer silently not reflected in the final result. This is suggestive of the same race window but was not deliberately isolated the way #581's own report presumably did. |
| #582 | bash safety guard blocks benign command substitutions like $(seq ...) | **Reproduced identically** — TC-25 (Batch 6), blocked with "dangerous pattern detected," exactly as before |
| #583 | list_jobs always reports actionable:false for running subagents | **Not re-tested this round.** `list_jobs` was called on running subagents multiple times (Batch 1 TC-06, Batch 2 TC-08/TC-09b) and the `actionable` field was present each time, but no batch prompt included an explicit assertion on its *value* for a running row. Neither confirmed nor refuted here — a gap in this run's test design, not a claim that it's fixed. |
| #584 | update_task rejects the task creator, only allows the assignee | **Reproduced identically** — TC-22/TC-22b (Batch 5), "you can only update tasks assigned to you," blocking both a status update and an agent reassignment from the creator |

**Working-as-designed items re-confirmed:** Worker has no delegation targets (leaf agent) — reconfirmed via EC-08 and via a worker sub-agent's own self-delegation attempt in Batch 4 being correctly denied. `list_jobs` hides terminal rows by default — reconfirmed via TC-09c/TC-09d (Batch 2). `create_plan` was not consent-gated for Jim (the orchestrator) in this run, consistent with the documented "consent-gated for non-orchestrator agents only" design.

**Concurrency-limit-leak recurrence:** the specific symptom the coordinator warned about (worker/jim hitting a stuck max-concurrency counter, tasks not dispatching, heartbeat warnings) was **not observed at any point in this run**, including during Batch 3's heaviest concurrent fan-out (~20–28 simultaneous activities) and Batch 9's deliberate concurrent dispatch tests. No `fly machine restart` was performed during this run.

---

## New findings (not among the original 9)

### N1. No persistent volume — any restart discards all data
`omnipus-uat-swimlane`'s `fly.toml` has no `[[mounts]]` and no volume attached (`flyctl volumes list` returns empty). Confirmed directly: the `fly machine restart` performed before this run, intended only to clear an in-memory concurrency counter, instead produced a completely fresh `$OMNIPUS_HOME` (new master key auto-generated, `onboarding_complete: false`, empty `agents.list`) — destroying the entire prior UAT setup, including 256 queued task records that were believed to have survived. **This is an operational/deployment gap in the UAT box, not an Omnipus product bug** — but it is a serious one for any team relying on this box for iterative testing. Recommend: attach a Fly volume for `$OMNIPUS_HOME` before any further live testing on this app, or explicitly treat every session on it as fully ephemeral.

### N2. "Stop generation" does not reliably terminate a chat turn's backend agent loop
Confirmed directly via server logs: Batch 3's session (`session_01KYVVSXNWD0TEZ69RQ0EEMPWB`) continued executing a self-referential `recall_conversation: span installed` loop (~110 tokens added roughly every 3 seconds) for at least ~26 minutes after the first "Stop generation" click (10:38→11:04+), and Batch 7's session showed the same pattern still active as late as 11:12. This did not happen for the batches that stayed on-script (2, 4, 5, 6, 8, 9) — it specifically correlates with sessions where the model had already entered a confused/retry-loop state before Stop was clicked, suggesting Stop can interrupt the visible streaming response but does not reliably tear down whatever background/recall machinery the turn had already spun up. Given the no-volume constraint (N1), restarting the process to clear this was judged unsafe and was not attempted — the loop(s) may still be running.

### N3. A "New chat" does not isolate you from a still-running prior turn
Direct consequence of N2. Confirmed at least three times: Batch 3's stray "Batch 3 is already complete" notifications appeared in the views for Batch 4, Batch 6, and Batch 7 — sessions started well after Batch 3's own `BATCH-3-COMPLETE` marker and multiple "Stop" clicks. The final screenshot intended to confirm Batch 9's end-state instead showed Batch 7's summary table, because Batch 7's own stuck loop was still actively pushing updates to whatever view was current. **This means the "start a fresh chat per batch" mitigation for #573 only isolates a new batch from a prior one if the prior turn has genuinely stopped** — which this exercise shows cannot be assumed from the UI alone (Send button reappearing, no "Stop" button visible) nor even from a full page reload (both were observed to still reflect a "generating" state that had actually gone quiet server-side, in Batch 6's case — a related but distinct minor UI-state-desync symptom).

### N4. A `max_tool_iterations` circuit breaker exists and does eventually fire
Positive finding. Observed once, at the end of Batch 6: *"I've reached max_tool_iterations without a final response. Increase max_tool_iterations in config.json if this task needs more tool steps."* This confirms the platform has a per-turn ceiling on tool-call iterations. However, it did not appear to actually terminate the underlying session (Batch 3/7's stuck loops continued well past whatever count would trigger this), and it took a very large number of iterations to fire at all, given how much runaway activity occurred in Batches 1, 3, and 7 without ever surfacing this message.

### N5. Model-side runaway tool-call retry loops amplify token cost and pollute workspace state
Observed twice with real, verifiable side effects: Batch 1's `create_task` optional-parameter confusion (real orphan task records created on disk, confirmed via `find /home/omnipus/.omnipus/tasks`) and Batch 7's TC-33b oversized-string construction (repeated malformed `delegate` calls with filler "AAAA..." content). In the Batch 1 case a single turn reached 5.3M tokens. This is likely at least partly an artifact of the model chosen for this run (`z-ai/glm-5.2`, not necessarily representative of a stronger tool-calling model), but the platform itself has no apparent loop-detection for "the same tool call, same malformed shape, repeated N times in a row" short of the very high `max_tool_iterations` ceiling (N4).

### N6. `create_task` / `create_task_in_workspace` have an inconsistent "rich parameter" surface
From Batch 1 TC-03b: `create_task`'s schema lists `priority` but the model could never get a value to actually apply, and the tool has no `due`/`stream`/`write_set`/`is_join` fields at all; `create_task_in_workspace` has `due`/`stream`/`write_set`/`is_join` but no `priority` field. No single call can set all "rich" parameters together the way the original test case's design assumed — likely because these fields are (per the tool's own description) "meaningful only alongside `plan_id`," which the v2 plan's TC-03b did not account for. Low severity; a documentation/schema-consistency item, not a functional break.

### N7. `delegate` denial errors don't distinguish "doesn't exist" from "not trusted"
From Batch 8 EC-02: attempting to delegate to `mia` (an agent that exists in the workspace but has no delegation edge from `jim`) and to a genuinely nonexistent agent both return the identical generic denial text. Minor debuggability/UX gap.

### N8. `delegate` silently accepts an empty `agent_id` instead of rejecting it
From Batch 8 EC-06: `delegate(agent_id="")` was accepted and spawned a generic/default subagent rather than being rejected as a missing required field. This is a genuine input-validation gap, independently reproducible with a single call.

### N9. Cancel on an already-terminal session returns a misleading success message
From Batch 4 TC-15c: cancelling an already-completed session does not corrupt state (it correctly stays "completed") but returns "cooperatively cancelled" — a success-shaped message — rather than a clear "session already terminal" error. Low severity, adjacent to but distinct from #577/#581.

### N10. Chat frontend logs a graceful-degradation path at console ERROR level
Observed once during Batch 3's peak load: a `chatOrphanFrameReleased` telemetry event (console ERROR) accompanied by a `[chat] orphan frame: parent_call_id=... subagent_start never arrived within 10000ms. Releasing as flat tool calls.` warning. The actual behavior is a sensible fallback (render as flat tool calls instead of a nested subagent card) rather than a crash, but it technically violates a literal "zero console errors" bar under heavy concurrent subagent load.

### N11. A transient 502 was observed under peak concurrent load
`GET /api/v1/workspaces?status=active` returned a 502 once during Batch 3's peak fan-out. Consistent with the small `shared-cpu-1x:512MB` box straining under many simultaneous LLM streaming connections; an operational capacity observation, not a code defect per se.

### N12. `follow_up` may cause recursive re-dispatch/nesting (unconfirmed lead)
See the TC-16/TC-16b entries in Batch 3 above — the model's own narration described "a recursive loop" and "nested repeatedly" behavior on top of the already-known #579 (dropped instruction). Not independently verified against server-side logs within this exercise; flagged for someone with code access to check `pkg/agent/subturn.go`'s follow_up path.

### N13. Model self-reported counts are not reliable ground truth
Batch 1's model narration claimed "~139 leftover tasks" mid-loop; direct filesystem inspection at the same rough time found roughly 10 real task files. Not a product bug — a methodology note for anyone reading agent self-narration as if it were verified telemetry.

---

## Did batching prevent the freeze? — Conclusion

**For the specific symptom #573 describes — approval dialogs stop responding, the page stops re-rendering entirely — batching worked in every one of the 9 batches run here, including the three (1, 3, 7) that experienced severe internal runaway behavior.** At no point did the chat UI become genuinely unresponsive: every approval dialog I clicked responded normally, every "New chat" navigation worked, every screenshot showed a live, interactive composer. This is a real, positive result: keeping each live chat session's *planned* scope to a handful of test cases does appear to keep the *visible* UI safe from the specific unbounded-render-cost mechanism #573 describes, which is keyed to the currently-streaming message's own accumulated subagent-activity list — and no single message in this run accumulated activity anywhere near the scale of the original ~4,700-tool-call session.

**But this is a narrower result than "batching solves the problem."** Three separate mechanisms surfaced in this run that batching by test-case-count does not address:

1. **The model, not just the operator, controls how large a single turn gets.** A "batch of 8–17 test cases" was the unit of *planning*, not a hard ceiling — when the model got confused (Batch 1's `create_task` params, Batch 3's redundant re-verification, Batch 7's oversized-string construction), a single turn still grew to thousands of tool calls and, in Batch 3's case, to a session scale (2,341 transcript lines, 800 kept + 106 evicted messages) that is a meaningful fraction of what caused the original incident. Smaller planned batches reduce the *frequency* of this happening (6 of 9 batches stayed clean) but do not prevent it.
2. **"Stop generation" is not a reliable circuit breaker.** Once a turn enters this runaway state, clicking Stop does not reliably end the backend work — confirmed by server logs showing continued activity 20+ minutes later for Batch 3, and still active at report time for Batch 7. This means the operator's own safety valve for "this batch is getting too big, abandon it and move on" does not fully work, which is exactly the intervention the task briefing asked me to use as the signal to end a batch early.
3. **A still-running "stopped" session keeps contaminating whatever you open next.** Because Stop doesn't fully work, starting a fresh chat for the next batch does not give you a clean slate — stray notifications from the abandoned session keep arriving in whatever view is current, which happened repeatedly across batches 4, 6, 7, and again at the very end (Batch 9's own final-state screenshot).

**Net assessment:** batching is a real, useful *reduction* in freeze risk and should be kept as an interim practice while #573 awaits a proper fix, but it is not a complete substitute for fixing the underlying unbounded-render-cost bug, and it surfaces two new problems of its own (unkillable runaway turns, cross-session notification bleed) that are worth their own follow-up, particularly N2/N3 above, since they undermine the very isolation batching is meant to provide.

---

## Recommendations

1. Attach a Fly volume to `omnipus-uat-swimlane` for `$OMNIPUS_HOME` before any further live testing, or treat every session there as fully disposable (N1).
2. Investigate why `recall_conversation`/turn cancellation does not reliably propagate a stop signal all the way through a session's background machinery (N2) — this is arguably higher-priority than #573 itself, since it means an operator cannot reliably abandon a runaway turn at all right now.
3. Investigate whether background completion/notification delivery is scoped correctly per session — the repeated cross-session bleed (N3) suggests WS/SSE delivery may be keyed to "current view" rather than strictly to originating session.
4. File N6–N9 as new, independently-actionable issues (schema-consistency for create_task family, denial-message clarity, empty agent_id validation gap, misleading cancel-on-terminal message) — all are cheap, well-isolated, single-call reproductions.
5. Follow up on N12 (`follow_up` recursion) with someone who has direct log/code access — this exercise could only observe it through model narration, not verify it independently.
6. Re-test #580 and #583 specifically — this plan's test design did not actually exercise the exact scenarios those two issues describe (see Known-issue status table); their current status remains unknown, not fixed.
7. Consider whether `max_tool_iterations` (N4) should trigger at a much lower threshold, or whether hitting it should also forcibly tear down the session's background state, given it currently does neither in a way that prevented the runaway sessions seen here.

---

## Total run stats

- Wall-clock: ~60 minutes across onboarding + 9 sequential batches (10:10–11:12)
- 82 test cases executed (all planned cases from the v2 UAT plan)
- Cumulative subagent dispatches by end of run: 225+ (per TC-38's live count, Batch 7; likely higher by Batch 9 given continued background activity)
- No `fly machine restart` performed during this run (per the corrected recovery guidance)
- No recurrence of the previously-reported concurrency-counter-leak symptom
