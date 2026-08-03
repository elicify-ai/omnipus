# UAT Report — Fix Verification for the 9 Confirmed Delegation/Task/Plan Bugs

**Date:** 2026-08-03
**Tester:** Jim (Orchestrator), driven via Playwright browser automation against the live chat UI
**Workspace:** `01KZ2YW5Q6PG0MCNYWX8A6CMN3` ("My Workspace") — fresh-install auto-seed
**App:** `omnipus-uat-swimlane` (Fly.io, machine `7812791a464028`, **version 4**, image `deployment-01KZ2YK5J86EXS0SNJQZQP15EE`, started `2026-08-03T04:42:16Z`)
**Model:** `z-ai/glm-5.2` via OpenRouter
**Purpose:** Verify that the 9 previously-confirmed bugs (#576–#588-N9), all of which reproduced identically in the [2026-07-31 batched re-run](uat-report-batched-rerun-2026-07-31.md), are **actually fixed** on the freshly deployed build — exercising real behavior through the chat UI, not reading code.

---

## Executive Summary

**All 9 bugs verified FIXED. 9 PASS / 0 FAIL / 0 BLOCKED.**

Every result below is grounded in **server-side ground truth** — session transcripts (`tool_call.status`, `duration_ms`), the durable lifecycle store (`session_lifecycle/*.jsonl`), the task/plan stores, and `gateway.log` — not in the model's own narration. This distinction mattered: in Batch 2 the agent's self-reported verdict was **wrong** (it reported #576 as still broken when it was fixed; the agent had simply called `inbox` with the wrong ID), and in Batch 6 the agent's "approximately 10 seconds" timing claim needed the `duration_ms: 10003` field to become evidence rather than assertion.

**The chat UI stayed responsive in all 6 batches, with zero console errors across the entire run** — no recurrence of the #573 freeze, including through a model-side retry loop in Batch 4 that fired ~10 redundant `create_task` calls.

Five new observations were recorded (N-A … N-E), one of which (N-A) is a real usability gap sitting directly on top of the otherwise-fixed #576.

---

## Summary Table

| Bug | Previously (2026-07-31) | Now (2026-08-03) | Verdict | Primary evidence |
|---|---|---|---|---|
| **#576** `message_parent` | Always failed: *"no durable session record for this session"*; parent inbox always empty | `message_parent` → `{"accepted":true,"message_id":"8ba2f248-…"}`; parent's `inbox` returns the message with `"text":"UAT576-PROBE-ALPHA"` | **PASS** | Message persisted to `session_messages/session_01KZ2Z6X0HV1M11M0Y02V8M65K.jsonl`; lifecycle record now exists with `parent_durable_key` set |
| **#577** `cancel` | Soft **and** hard cancel returned success but the subagent ran to full completion | Both cancels **actually terminated** the worker ~24–29 s before its natural end; no straggler completion | **PASS** | `duration_ms` 37136 / 32130 (not ≥60000); lifecycle `cancelled` @ 04:52:13.7 / 04:52:08.7 |
| **#578** `create_task(plan_id=…)` | Always failed: *"cannot verify plan_id: plan store is not configured"* → `execute_plan` saw zero members | Task created and **attached**; `execute_plan` approved and ran the plan | **PASS** | Two task files carry `"plan_id": "01KZ2ZK082EZEQ3F7FKWP7XY83"`; plan reached `state: running`, `approved_at`/`started_at` set |
| **#579** `follow_up` | Silently dropped the new instruction, returned the original result ("first") | New instruction honored — `result: goodbye` | **PASS** | Child lifecycle shows a **second generation** (gen1 @ 04:58:54–55) after gen0 ("first") |
| **#580** `timeout_seconds` | Argument never read; every delegation used the hardcoded 5-min default | Timed out at **exactly 10.003 s**; `99999` rejected with a clear range error | **PASS** | `tool_call duration_ms: 10003`; lifecycle `failed` exactly 10.003 s after `running`; gateway.log confirms both |
| **#581** steer on terminal | Returned *"Steering message queued"* (false success) | *"session … is terminal (completed) and cannot be steered"* | **PASS** | Verbatim tool result + `gateway.log` error entry |
| **#582** bash guard | `for i in $(seq 1 5); …` blocked as "dangerous pattern" | Benign loop **runs**; genuinely dangerous substitutions **still blocked** | **PASS** | `tool_call status:"success"`, `duration_ms: 5008`; `$(curl …)` and `$(awk … system("id"))` both `status:"error"` |
| **#584** `update_task` by creator | Creator rejected: *"you can only update tasks assigned to you"* | Creator successfully updated an assignee's task | **PASS** | Task `1ca43b5c` on disk: `agent_id: worker`, `created_by: jim`, `status: in_progress` |
| **#588-N9** cancel-on-terminal | Returned success-shaped *"cooperatively cancelled"* | *"is already terminal (cancelled) — nothing to cancel"* | **PASS** | Verbatim tool result |

---

## Batch Log & UI Responsiveness

Each batch ran in its own fresh chat session, per the #573 batching mitigation.

| Batch | Coverage | Chat session | Tokens | UI responsive | Notes |
|---|---|---|---|---|---|
| 1 | #582 | `session_01KZ2Z3KG40P12Q7AQ23RZJBAS` | 51.3k | **Y** | Cleanest batch, ~12 s end-to-end |
| 2 | #576 | `session_01KZ2Z6X0HV1M11M0Y02V8M65K` | 141.1k | **Y** | Required a corrective second turn (see N-A) |
| 3 | #577, #588-N9 | `session_01KZ2ZCWG3Y1T7A2ZM3ZRB1M35` | 108.3k | **Y** | Timing controlled manually across 3 turns |
| 4 | #578, #584 | `session_01KZ2ZJW6MVJ3FS3TVER80GY04` | 247.7k | **Y** | Model retry loop (~10 redundant calls) — UI never degraded |
| 5 | #579, #581 | `session_01KZ2ZRM5KHYPXKSXKTDNFDV5Z` | 120.4k | **Y** | Clean |
| 6 | #580 | `session_01KZ2ZX03G316TBC94RBQACYTN` | 38.9k | **Y** | Clean |

**Console errors across the whole run: 0** (`browser_console_messages` with `all: true`, level `error` → 0 of 3 total messages). At end of run the UI was still fully live: `/api/v1/workspaces` returned **200 in 75 ms**, composer present and enabled.

No `fly machine restart` was performed at any point (per the standing no-volume constraint).

---

## Per-Bug Detail

### #576 — `message_parent` → parent inbox — **PASS**

Jim delegated to `worker` (sync) with a probe task instructing one `message_parent` call carrying `UAT576-PROBE-ALPHA`.

The child reported, verbatim:

```
{"accepted":true,"message_id":"8ba2f248-30fe-49bb-88e4-f79994571859"}
```

The old failure text (*"no durable session record for this session"*) did not appear. Three independent confirmations:

1. **The message persisted.** `session_messages/session_01KZ2Z6X0HV1M11M0Y02V8M65K.jsonl`:
   ```json
   {"kind":"message","message":{"kind":"progress","message_id":"8ba2f248-30fe-49bb-88e4-f79994571859",
    "sender_identity":"worker","session_id":"51451738-521b-49ba-83ba-d991d5b52506",
    "text":"UAT576-PROBE-ALPHA","untrusted_origin":true,"depth":1}}
   ```
2. **The durable record whose absence caused #576 now exists.** `session_lifecycle/51451738-….jsonl` carries `parent_agent_id: "jim"` and `parent_durable_key: "session_01KZ2Z6X0HV1M11M0Y02V8M65K"` — exactly the file the message landed in.
3. **The parent could read it back.** `inbox(session_id="51451738-…")` returned:
   ```json
   {"has_more":false,"messages":[{"kind":"progress","message_id":"8ba2f248-…","sender_identity":"worker",
     "text":"UAT576-PROBE-ALPHA","untrusted_origin":true}],"next_cursor":"1"}
   ```

> **Important methodology note.** On its first attempt the agent concluded **"NO — the message did not arrive"** and attributed it to #576. That verdict was wrong. It had called `inbox` with a task ID rather than the child's `session_id`, got `lifecycle record not found`, and generalised from the error. Only after being handed the correct `session_id` did the drain succeed. Had this run trusted agent narration, #576 would have been reported as still-broken. See **N-A**.

### #577 — `delegate(action="cancel")` — **PASS** (soft *and* hard)

Two workers were dispatched async at `04:51:37.6`, each running `sleep 60` (natural completion ≈ `04:52:37.6`). Cancels were issued ~28 s in.

| Session | Mode | Cancel message | Lifecycle `cancelled` at | Elapsed | Early by | `duration_ms` |
|---|---|---|---|---|---|---|
| `176c6fab-…` | `hard=false` | *"cooperatively cancelled; a checkpoint flush is expected within 5s, after which a hard cancel backstop fires…"* | `04:52:13.735` | 36.1 s | **23.9 s** | 37136 |
| `9e93634a-…` | `hard=true` | *"hard-cancelled immediately."* | `04:52:08.737` | 31.1 s | **28.9 s** | 32130 |

Four corroborating signals that termination was **real**, not merely reported:

- Both `duration_ms` values are well under 60000 — the delegate spans ended at cancel time, not at the sleep's natural end.
- The 5 s gap between the hard cancel (04:52:08.7) and the soft cancel (04:52:13.7) matches the documented "checkpoint flush within 5 s, then hard backstop" contract exactly.
- Re-checked at **04:53:27**, a full 50 s past natural completion: **no further lifecycle transitions**, no `completed` state appended.
- `LONG_RUN_COMPLETE_A` / `_B` appear **only** in the prompt text and the delegate call `parameters` echo — **never in a tool result**. The previous run's signature failure mode (a late straggler completion carrying the real output) did not occur.

### #578 — `create_task(plan_id=…)` + `execute_plan` — **PASS**

```
P1  create_plan   → {"plan_id":"01KZ2ZK082EZEQ3F7FKWP7XY83","state":"draft","workspace_id":"01KZ2YW5Q6PG0MCNYWX8A6CMN3"}
P2  create_task   → {"task_id":"b6f38f7c-1005-4e89-af47-f8fb807045b7","status":"next"}
P3  execute_plan  → {"plan_id":"01KZ2ZK082EZEQ3F7FKWP7XY83","state":"approved",
                     "note":"approved; queued behind cap — the engine promotes it to running
                             under the global concurrency cap when a slot is available"}
```

Neither old error appeared: no *"cannot verify plan_id: plan store is not configured"* on P2, and no *"has zero member tasks; nothing to execute"* on P3.

Server-side confirmation that attachment was real, not just a 200:

- **Two** task files carry the linkage — `tasks/b6f38f7c-….json` and `tasks/d0b43d58-….json` both contain `"plan_id": "01KZ2ZK082EZEQ3F7FKWP7XY83"`.
- The plan record progressed `draft → approved → running`, with `approved_at: 04:55:23`, `started_at: 04:55:31`, `plan_phase: dispatching`.
- `last_unmet_terminal_signature` enumerates both members reaching `done`, and the DoD judge ran on them — i.e. `execute_plan` saw a **non-zero** member set and actually executed it.

### #579 — `delegate(action="follow_up")` — **PASS**

Session `f53c5407-…` first completed with `result: first`. A `follow_up` was then issued using the **`text`** parameter (the fix's primary spelling; `task` is the deprecated alias):

The dispatch echo carried **both** the original and the new instruction, and the final status was:

```
[delegate-4] status=completed  agent=worker  created=2026-08-03 04:58:54 UTC
  task:   Original task: Reply with exactly the single word: first …
          Ignore your earlier instruction about the word first. Now reply with exactly the single word: goodbye . Nothing else.
  result: goodbye
```

`result: goodbye`, not `first` — the new instruction was honored. Server-side, the child's lifecycle shows a genuine **second generation**:

```
gen0 queued/running @ 04:57:56.37 → completed @ 04:57:57.33     (produced "first")
gen1 queued/running @ 04:58:54.32 → completed @ 04:58:55.39     (produced "goodbye")
```

No recursive re-dispatch or nesting was observed (cf. the unconfirmed lead N12 in the prior report — not reproduced here).

### #580 — `delegate(timeout_seconds=…)` — **PASS**

| Call | Result | Evidence |
|---|---|---|
| `timeout_seconds=10` on a `sleep 60` task | `Delegate execution failed: turn timed out` | `tool_call status:"error", duration_ms: 10003`; child lifecycle `running @ 05:00:23.994` → `failed @ 05:00:33.998` = **10.003 s** |
| `timeout_seconds=99999` | `timeout_seconds must be between 1 and 3600 when non-zero (got 99999); 0 means use the default` | `tool_call status:"error"` with **no** `duration_ms` — rejected before dispatch |

`gateway.log` independently records both:

```
{"tool":"delegate","duration":10003,"error":"Delegate execution failed: turn timed out"}
{"tool":"delegate","duration":0,"error":"timeout_seconds must be between 1 and 3600 when non-zero (got 99999); 0 means use the default"}
```

10.003 s against a hardcoded 300 s default is unambiguous: the argument is now read and enforced.

### #581 — steer on a terminal session — **PASS**

```
delegate: steer: session f53c5407-87b4-45c5-9e76-b47da57f7156 is terminal (completed) and cannot be steered
```

An explicit terminal error, not the old false *"Steering message queued"*. Mirrored in `gateway.log` as a `Tool execution failed` entry.

### #582 — bash safety guard — **PASS** (fixed **and** not over-opened)

| ID | Command | `tool_call.status` | Outcome |
|---|---|---|---|
| B1 | `for i in $(seq 1 5); do echo "line$i"; sleep 1; done` | **`success`**, `duration_ms: 5008` | **RAN** — output `line1`…`line5` |
| B2 | `echo $(curl http://example.com)` | `error` | **BLOCKED** — *"Command blocked by safety guard (dangerous pattern detected)"* |
| B3 | `echo $(awk 'BEGIN{system("id")}')` | `error` | **BLOCKED** — *"Command blocked by safety guard (dangerous command inside $(...): awk)"* |

The `duration_ms: 5008` on B1 is itself proof of execution (five 1-second sleeps); B2/B3 were rejected pre-execution with no duration. The guard now distinguishes a benign command substitution from a dangerous one, and B3's error text names the specific offending binary — a more precise message than the generic pattern rejection.

The guard remains actively enforcing elsewhere: `gateway.log` shows it blocking further `dangerous pattern detected` attempts at 04:58:32–43 originating from the plan supervision loop's own workers, unrelated to this test.

### #584 — `update_task` by the creator — **PASS**

```
create_task  → {"task_id":"1ca43b5c-639e-4e2e-81fa-11fa678d1916","status":"next"}     (assigned to worker)
update_task  → {"task_id":"1ca43b5c-639e-4e2e-81fa-11fa678d1916","status":"in_progress","updated_fields":["status"]}
```

The old *"you can only update tasks assigned to you"* did not appear. The test only proves the fix if the roles are genuinely split, so the on-disk record was checked:

```
"id": "1ca43b5c-639e-4e2e-81fa-11fa678d1916"
"title": "UAT584 creator perm task"
"agent_id": "worker"            ← assignee is NOT jim
"created_by": "jim"             ← creator is jim
"created_by_agent_id": "jim"
"status": "in_progress"         ← the update persisted
```

Creator ≠ assignee, and the creator's update landed.

### #588-N9 — cancel on an already-terminal session — **PASS**

```
delegate: cancel: session 176c6fab-d34e-4581-a935-a9e8e15af068 is already terminal (cancelled) — nothing to cancel
```

An explicit terminal error, replacing the old success-shaped *"cooperatively cancelled"*.

A second probe — cancelling a session owned by a *different* parent chat — returned `delegate: cancel: session 51451738-… is not owned by the calling session`. The ownership check fires before the terminal check, which is correct precedence (see **N-E**).

---

## New Findings

### N-A. `inbox` requires a `session_id` the sync-delegation path never surfaces
**Severity: medium — usability gap directly on top of the fixed #576.**

`inbox` with no arguments returns `session_id is required`. But a synchronous delegation (`async=false`) returns only the child's *result text* — it does **not** surface the child's `session_id` anywhere the parent can see. So after a sync delegation, the parent has a message durably waiting in its inbox and **no way to name the session that would let it drain that message**. Only `async=true` (which returns the `session_id`) makes the inbox reachable.

This is exactly what caused the agent to mis-diagnose #576 as unfixed in Batch 2. It is worth an independent issue: either surface the child `session_id` in the sync-delegate result, or let `inbox` default to "all children of the calling session" when `session_id` is omitted.

### N-B. `delegate(action="status")` reports `completed` for a session the lifecycle store records as `cancelled`
**Severity: low–medium — misleading state reporting.**

For session `176c6fab-…`, `status` returned `[delegate-1] status=completed agent=worker`, while `session_lifecycle/176c6fab-….jsonl` records the terminal state as **`cancelled`** — and the same session's `cancel` call correctly reported *"already terminal (**cancelled**)"*. So two delegate sub-actions disagree about the same session's terminal state.

The cancellation itself definitively worked (see #577), so this is a reporting inconsistency, not a control-plane failure. Possible benign reading: `status` may use "completed" to mean "finished" generically. Even so it is misleading, it contradicts the sibling `cancel` message, and it discards a distinction the durable store takes care to preserve.

### N-C. The autonomous plan-execution loop outlived its batch and stalled
**Severity: medium — worth its own investigation.**

Batch 4's `execute_plan` started the ADR-052 supervision loop. Long after that chat batch ended:

- Plan `01KZ2ZK082EZEQ3F7FKWP7XY83`: `state: running`, `plan_phase: dispatching`, `active_loop: true`, `correction_rounds: 3`, and **`last_activity_at` frozen at `04:58:28`**.
- Two worker sessions stuck in `running` with no lifecycle progression: `session_01KZ2ZPK5GYZWD13GQYH1A2J9V` (since `04:56:48`) and `session_01KZ2ZSN6J8EJ7TTJ149N46MPQ` (since `04:58:28`).

Still unchanged at `05:02:28` — i.e. **~4 minutes of zero plan activity while two sessions remain nominally `running`**, and ~5.7 minutes for the older session. `judge_rounds` also read `3` against a plan configured with `judge_rounds: 2`, so it appears to be a counter rather than a bound; whether the loop is bounded at all could not be determined from the outside.

This is reminiscent of finding N2 in the prior report (background work outliving a stopped turn) but the mechanism is different — here it is the plan supervisor, not a stuck chat turn, and no Stop was ever clicked. **Notably, this did not affect UI responsiveness**, and no cross-session notification bleed (prior N3) was observed in any batch.

### N-D. The glm-5.2 optional-parameter retry loop recurred
**Severity: low — model-side, matches prior finding N5.**

In Batch 4, the model could not get `plan_id` into a `create_task` call and fired ~10 near-identical calls while narrating "I keep omitting the plan_id," each creating a real task record (12 task files on disk afterwards). It eventually succeeded. Two things are worth separating:

- **Not a platform regression** — the same pattern is documented as N5 on 2026-07-31 and is specific to `z-ai/glm-5.2`'s tool-argument handling.
- **The platform held up well under it.** The UI stayed fully responsive, no approval-dialog backlog formed, the turn peaked at 247.7k tokens (versus 5.3M in the equivalent prior incident), and the batch completed normally.

### N-E. Cross-session cancel is correctly rejected by an ownership check
**Positive finding.**

`cancel` on a session belonging to a different parent chat returned `delegate: cancel: session 51451738-… is not owned by the calling session`. Ownership is checked before terminal state, which is the right precedence — a caller should not learn a foreign session's lifecycle state from an error message. Worth recording so it is not mistaken for a bug in future runs.

---

## Recommendations

1. **File N-A** (sync-delegate hides the `session_id` needed to drain `inbox`) — it is a small change that removes the main remaining sharp edge around the now-working `message_parent` path, and it actively causes misdiagnosis.
2. **File N-B** (`status` says `completed` for a `cancelled` session) — cheap, well-isolated, single-call reproduction.
3. **Investigate N-C** (plan supervision loop stalled with two sessions pinned `running`) — this is the one finding here that could plausibly leak resources over time; it deserves the same scrutiny the prior report's N2 received.
4. **Keep batching as standing practice.** Six small sessions, zero freezes, zero console errors, and — unlike the prior run — no stuck turns and no cross-session bleed. The one runaway that did occur (N-D) stayed contained.
5. **Do not read agent self-narration as a UAT result.** Two of the nine verdicts in this run would have been wrong on narration alone (#576 falsely FAIL, #580 unverified). Server-side `tool_call.status` / `duration_ms` / lifecycle timestamps are the authority and were decisive every time.
6. **The no-volume constraint still applies.** This run began from a wiped `$OMNIPUS_HOME` caused by the v4 deploy, exactly as prior finding N1 predicted. Attach a Fly volume or continue treating every session as disposable.

---

## Run Stats

- Wall-clock: ~20 minutes (04:43 onboarding → 05:02 final checks)
- 6 chat batches, all in isolated sessions; 9 bugs verified
- Cumulative tokens across batches: ~708k
- Console errors: **0**; UI freezes: **0**; `fly machine restart`: **0**
- Evidence sources: session transcripts, `session_lifecycle/`, `session_messages/`, `tasks/`, `plans/`, `gateway.log`, live REST responses
