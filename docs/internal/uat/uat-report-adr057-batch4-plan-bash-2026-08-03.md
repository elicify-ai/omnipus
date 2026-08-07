# UAT-4 Report — ADR-057 Batch: Plan/Task Lifecycle & Background Bash

**Verdict: MOSTLY PASS, no blockers found in my lane. 32 PASS / 1 FAIL / 1 BLOCKED, plus 3 non-blocking findings worth follow-up.**
*(Corrected 2026-08-03 after independent coordinator re-test: the TC-03e priority-validation FAIL is real but was originally mischaracterized as symmetric across both bounds — see the TC-03e section below for the corrected, asymmetric root cause and the tool-vs-REST split.)*

**Date:** 2026-08-03 · **Tester:** UAT-4 (of 4 parallel agents) · **Target:** `https://omnipus-uat-swimlane.fly.dev`, commit `acd0d0af`, Fly machine `7812791a464028` (version progressed 5→7 during the run as image tags updated; no restart/redeploy performed by me)
**Workspace:** `01KZ4TWYRZ4CVVS81PP2XJ51QS` ("My Workspace") · **Scope:** Plan/Task lifecycle (`create_plan`, `execute_plan`, `stop_plan`, `run_task`, `update_task`, `create_task`) and Background bash (`bash` foreground/`run_in_background`/`poll`/`read`/`kill`)

| Result | Count |
|---|---|
| PASS | 32 |
| FAIL | 1 (TC-03e, MAJOR — asymmetric: lower bound (0) broken on both the tool and REST paths; upper bound (6) broken on the tool path only, correctly rejected via REST) |
| BLOCKED | 1 (boot-sweep reconciliation — cannot test without a forbidden restart) |
| Findings (non-blocking, filed for follow-up) | 3 |

---

## Methodology & Limitations (read this before the results)

**The Playwright MCP browser is shared across all 4 parallel UAT agents** — one browser process/context/cookie jar, not four isolated browsers. I personally observed the onboarding wizard jump from step 1 to step 3 with no input from me, a login form fill itself in with a sibling's credentials, and both of my tabs bounce to `/#/login` with 401s mid-session. A sibling and the coordinator independently confirmed the same thing. **I do not treat any browser observation I did not personally and deliberately cause as a finding.** I used the browser only for initial reconnaissance (onboarding) and reported the shared-credential situation back to the coordinator (`main`) rather than guess-logging in.

Given that, my **primary instrument for everything reported below is NOT the browser**:

1. **An isolated WS chat driver** (`chat.mjs`, written for this run) — a small Node script using the `ws` package that connects directly to `wss://omnipus-uat-swimlane.fly.dev/api/v1/chat/ws` and authenticates via the WS protocol's documented legacy **auth-frame path** (`{"type":"auth","token":"<bearer>"}` — confirmed in `pkg/gateway/websocket.go::authenticateWS`/`resolveBearerIdentity`), using a personal bearer token obtained from my own `POST /api/v1/auth/login`. This is the **same production WS protocol the SPA itself uses** (per `contracts/asyncapi.yaml`) — not a mock, not a REST shortcut — but running in my own process with my own connection, completely unaffected by the shared browser's single-slot session cookie (confirmed in `pkg/gateway/middleware/session_cookie.go` that `SessionTokenHash` is a singular per-user field — any concurrent login invalidates every other cookie-based session for that user; bearer tokens are stored in a list and do not have this problem).
2. **Direct REST calls via `curl`** with my own `Authorization: Bearer` token for durable-state verification (`/tasks/{id}`, `/plans/{id}`, `/sessions/{id}`, `/sessions?type=...`) — independent ground truth, not narration.
3. **Read-only SSH into the live container** (`fly ssh console`, no `restart`/`deploy`) to run `ps aux` and independently confirm OS-level process state for `bash(action="kill")` and timeout enforcement — this is the actual "prove the effect, not the return message" mechanism for background bash, since `list_jobs` does not cover bash sessions at all (its `kind` enum is `plan|subagent|task` only).

I never ran `fly machine restart` or `fly deploy`. All task/plan/session ids below were created by me and are traceable to "UAT4"-prefixed titles or ids captured directly from my own tool calls.

One environmental note: I hit **"I'm at capacity right now — please try again in a few seconds"** from the model repeatedly, almost certainly from 4 agents sharing one LLM concurrency slot/API key. This is noted for context, not scored as a product bug in my lane.

---

## Suite E: Plan/Task Lifecycle

### TC-17 — `create_plan` happy path — **PASS**
`create_plan(title="UAT4 Test Plan", dod=[...], rationale=...)` → `{"plan_id":"01KZ4W2KGWVGEERYD9XMMY0JJ6","state":"draft","workspace_id":"01KZ4TWYRZ4CVVS81PP2XJ51QS"}`. Independently confirmed via `GET /api/v1/plans/01KZ4W2KGWVGEERYD9XMMY0JJ6` → full durable record (`state:"draft"`, `owner_agent_id:"jim"`, `dod` array intact).

### TC-18 — `create_task` with `blocked_by` — **PASS**
`create_task(title="UAT4 Task B", blocked_by=["<TaskA id>"], plan_id=...)` → `{"task_id":"3cfad043-...","status":"blocked"}`. Status correctly reflects the dependency at creation time.

### TC-19 — `execute_plan` autonomous execution — **PASS**
`execute_plan(plan_id="01KZ4W2KGWVGEERYD9XMMY0JJ6")` → `{"state":"approved","note":"...queued behind cap..."}`. Polled via REST: `draft → approved → running` (confirmed `active_loop:true`). The member task (`TaskA`, "Return the string created") was genuinely dispatched to a real session (`session_01KZ4WCRZ7QB507V8A6J069DPB`), and — notable bonus finding, not a bug — the **evidence-ladder judge correctly rejected** the worker's bare textual claim ("no machine-check evidence... fail-closed rules, a criterion cannot be marked met based solely on the worker's untrusted claim"), then the **plan owner-loop correction mechanism re-dispatched the task with the judge's feedback folded into the new prompt** (`plan_phase` cycled `idle → stalled → dispatching`, new session each round). This is exactly the "zero tolerance for unverifiable success" behavior this release is supposed to guarantee, observed end-to-end via REST, not narration.

### TC-20 — `stop_plan` — cancel in-flight plan — **PASS (strongest evidence in this report)**
Built a second plan (`01KZ4W7Y0HTESDDK3VDNT0KRGN`) with one member task whose prompt instructed the worker to (a) start a **background bash job** (`sleep 120 && echo NEVERSEEN_PLANBG12345`) and (b) run a **foreground bash** (`sleep 90 && echo LONG_PLAN_TASK_DONE`). After `execute_plan`, confirmed via `ps aux` over SSH that both were **real running OS processes**:
```
889 sleep 120     (the plan-task's own background bash job)
891 sleep 90      (the plan-task's foreground bash)
```
Called `stop_plan(plan_id="01KZ4W7Y0HTESDDK3VDNT0KRGN")` → `{"state":"failed","failed_reason":"stopped_by_user", ...}`. Then, **without trusting that message**, re-ran `ps aux` immediately:
```
(both processes gone — only an unrelated sibling's own background job remained)
```
Cross-checked via REST: plan `state:"failed"`, `active_loop:false`; member task `status:"failed"`, `result:"[reason:stopped_by_user] Cancelled by jim via Stop."`; the task's session `status:"interrupted"`. **All four signals (tool response, plan record, task record, session record, and the actual OS process table) agree, and the process table is proof stop_plan did real work, not just a status flip.** Repeated on the first plan (`01KZ4W2KGWVGEERYD9XMMY0JJ6`, mid owner-loop-correction) with the same clean result (`status:"failed"`, `result:"execution error: turn canceled"`).

This directly confirms the brief's key requirement: **`stop_plan` genuinely stops the work, including sub-turns and the background processes those sub-turns started.**

### TC-21 — `run_task` — retry/steering loop — **PASS (fully evidenced via two full transcripts)**
Created a standalone task with a prompt that could never satisfy its own criterion on the first try (`prompt:"Return the exact string HELLO_UAT4_RUNTASK"`, `criteria:"contains MAGIC_WORD_UAT4_XYZZY"` — a string never mentioned anywhere). `run_task(task_id=...)` dispatched immediately (`session_01KZ4WG3AVR1PHK3DR2J8Q1VN5`). Fetched both attempt sessions' full transcripts via REST:
- **Attempt 1** transcript: worker returns only `HELLO_UAT4_RUNTASK` → judge system message: `round:1, met:false`, per-criterion reason: *"does not contain the required substring 'MAGIC_WORD_UAT4_XYZZY'"*.
- **Attempt 2** transcript (new session `session_01KZ4WG6FKECCEZ3QQMHQQPPQF`) opens with the judge's feedback injected as the turn's context (*"## Feedback from attempt 1 — address this before re-claiming success..."*); worker now returns both strings → judge `round:2, met:true` → task `status:"done"`.

Textbook run → judge → retry-with-steering → terminal state, independently confirmed via the durable transcripts, not the model's own summary.

### TC-22 / TC-22b–e — `update_task` — **PASS (all 5 sub-cases)**
| Case | Call | Result | Independent REST confirmation |
|---|---|---|---|
| Status mutation | `status:"in_progress"` then `status:"done"` | in_progress succeeded; **force-`done` on a criteria-bearing task was correctly REJECTED** (`"this task has acceptance criteria — completion is adjudicated by the judge during a task run; it cannot be force-completed here"`) | `GET /tasks/{id}` confirms `status:"in_progress"` persisted |
| Reassign (22b) | `agent_id:"ray"` | `updated_fields:["agent_id"]` | `GET /tasks/{id}` → `agent_id:"ray"` |
| Invalid id (22c) | `task_id:"fake-nonexistent-id-12345"` | `"task \"fake-nonexistent-id-12345\" not found"` | clean 1-line error, no crash |
| Cycle detection (22d) | set A's `blocked_by` to C, where C is already `blocked_by`-chained to A | `"blocked_by cycle detected: ... is reachable from ... through blocked_by"` | rejected before persistence |
| Replace semantics (22e) | task C had `blocked_by:[A,B]`; updated to `blocked_by:[D]` | `updated_fields:["blocked_by"]` | `GET /tasks/{id}` → `blocked_by:["<D>"]` **exactly**, not `[A,B,D]` |

Note: the rejection of force-completing a criteria-bearing task is a deliberate business rule (not in the original spec's literal expectation) that I consider a **positive finding** — it prevents callers from bypassing the judge, consistent with this release's anti-shortcut theme.

### TC-03b — `create_task` rich parameters — **PASS**
`priority:2, due:"2026-08-15T00:00:00Z", stream:"uat4-stream", write_set:["uat4-output.txt"]` all round-tripped exactly via `GET /tasks/{id}`.

### TC-03c — zero criteria — **PASS** — rejected: `"criteria is required: ... ADR-049 D5/SD-A7"`.

### TC-03d — title boundary — **PASS** — 200 chars accepted (`task_id` returned); 201 chars rejected (`"title must be 200 characters or fewer"`).

### TC-03e — invalid priority (0, 6) — **FAIL (MAJOR), but the two bounds are NOT symmetric — corrected 2026-08-03**

**Correction to my original write-up.** My original evidence for this test came entirely through the `create_task` **tool** (via the chat/WS path), where I observed both `priority:0` and `priority:6` accepted with a success response and silently coerced to the default (3). I reported this as "both bounds fail." That conclusion was **half wrong** — the coordinator independently re-tested via the **direct REST endpoint** (`POST /api/v1/tasks`) and found `priority:6` **correctly rejected** with a 400 and a clear message. I have now independently reproduced the coordinator's REST result myself (own bearer token, own curl calls, not taken on trust), and it holds:

| Path | `priority:0` | `priority:6` | `priority:2` (control) |
|---|---|---|---|
| **`create_task` tool** (via chat, my original evidence, `evidence.jsonl`) | success, silently persisted as `3` | **success, silently persisted as `3`** — no rejection | (not tested via tool; REST control below covers it) |
| **`POST /api/v1/tasks` REST** (independently re-verified by me just now) | `201`, persisted `"priority":3` — silently coerced | `400 {"error":"task validation: priority must be between 1 and 5, got 6"}` — **correctly rejected** | `201`, persisted `"priority":2` — correct |

So there are genuinely **two separate findings** here, not one:

1. **The lower bound (0) is broken on both paths.** The diagnosis, per the coordinator: `0` is Go's zero value for an `int` field, so an explicitly-sent `priority:0` is indistinguishable from "field absent" once it crosses the JSON-to-struct boundary, and the absent-field default-fill (3) fires for both cases alike. The upper bound (6) has a real, working `1–5` range check; the lower bound is defeated by this ambiguity before that check is ever reached. This is the same root-cause shape as the `subturn.max_concurrent` fail-open the coordinator fixed elsewhere in this release (an unresolvable/zero value silently meaning "no gate" instead of "invalid, reject"): a zero value used as a sentinel for "unset" swallows a legitimate, explicit zero. The fix is to distinguish unset-from-explicit-zero (a pointer field, or an explicit-presence check before defaulting), not to bolt on another range check that would just repeat the same bug.
2. **The `create_task` tool path is additionally missing the upper-bound check the REST path has.** `priority:6` is silently coerced to `3` via the tool with no error at all, while the same value is correctly rejected via REST. This means the tool and the REST endpoint do not share (or do not both reach) the same validation logic for this field — a caller going through chat/tool-calling has strictly weaker input validation than a caller hitting the REST API directly.

**User-facing impact:** a caller who explicitly requests `priority:0` (either path) or `priority:6` (tool path only) believes their request succeeded as stated; the actual stored priority silently differs with no warning. Low blast radius (priority is advisory scheduling metadata, not a security boundary), but a genuine contract violation, and the tool/REST inconsistency means the same input can be accepted or rejected depending on which entry point issues it. **Severity: MAJOR** (silent data-fidelity loss on an explicit, well-formed call, present on at least one bound via both entry points, and on both bounds via the tool path).

### TC-03f — malformed due date — **PASS** — `due:"not-a-date"` rejected: `"invalid due date ... must be RFC 3339 ..."`.

---

## Suite F: Background Bash

### TC-23 — Foreground bash happy path — **PASS** — `echo 'hello bash'` → `"hello bash"`.

### TC-28 — `cwd` parameter — **PASS** (with one MINOR note)
`cwd:""` → correct workspace root. `cwd:"realsubdir"` (created via `mkdir -p` first) → correct nested path, twice, consistently. **MINOR finding:** on a *non-existent* subdir, the error is `"sandbox.Run failed: hardened_exec: start sh: fork/exec /bin/sh: no such file or directory"` — a confusing low-level sandbox/exec error that doesn't say "directory does not exist"; worth a clearer message, but not incorrect behavior (the command is correctly refused either way).

### TC-28b — `cwd` escape rejection — **PASS**
`cwd:"../../etc"` → `"path escapes workspace: access denied: path is a protected carve-out ($OMNIPUS_HOME-anchored)"`. `cwd:"/etc"` → `"path escapes workspace: absolute path not allowed (use a relative path)"`. Both clear, both rejected.

### TC-29 / TC-29b — timeout boundary & invalid values — **PASS**
`timeout_seconds:1` → executes normally. `3601` → rejected (`"must be between 1 and 3600 (got 3601)"`). `0` → rejected (`"got 0"`). `-1` → rejected (`"got -1"`). Note: `bash`'s `timeout_seconds:0` is **rejected**, unlike `delegate`'s `timeout_seconds:0` which means "use default" per the 2026-08-03 fix-verification report — an asymmetry worth documenting for consistency, not a bug in either tool individually.

### TC-24/TC-25 — Background dispatch, poll, incremental read — **PASS**
Dispatched a 15-line, ~15s job. Immediate read (same turn) → `{"status":"running","output":"LN\n"}` (1 line). A second read ~10s later → `{"status":"done","output":"LN\nLN\n...(14 lines)"}`. Monotonic growth and correct running→done transition observed across two independent snapshots.

### TC-26 — Background kill — **PASS (independently verified via OS process state)**
Dispatched `sleep 120` in background → confirmed via SSH `ps aux` as a real running process (pid 822: parent `sh -c sleep 120`, child `sleep 120`). Called `bash(action="kill", session_id=...)` → `{"status":"killed","exitCode":-1}`. **Re-ran `ps aux` immediately: the process was gone.** This directly answers the brief's warning that "a background-shell kill was wired to nothing yet returned success anyway" — in this build, the kill is real, confirmed independent of the tool's own return message.

### TC-26b — Kill already-killed — **PASS** — second `kill` call on the same session returns the same terminal snapshot (`{"status":"killed",...}`), no error, no crash, no false "freshly killed" claim.

### TC-27 — Timeout enforcement — **PASS (independently verified)**
Dispatched `sleep 300` with `timeout_seconds:5`. Polled: `{"status":"timeout","exitCode":-1}`. SSH `ps aux` (checked twice, both before and after an unrelated kill test) showed **no `sleep 300` process ever present** at either check — consistent with it being killed at the ~5s mark, well before my first check. The tool's own reported "timeout" status is corroborated by the process actually being gone, not orphaned.

### TC-30 / TC-30b — Immediate exit (0 / non-zero) — **PASS**
`true` (background) → `{"status":"done"}` (no `exitCode` field — consistent with Go `omitempty` on a zero value, not an error). `false` (background, run twice) → both `{"status":"done","exitCode":1}` — clearly and correctly recorded.

### EC-07 — Empty command — **PASS** — `bash(command="")` → `"command is required and must be a non-empty string"`, clean rejection, no crash.

---

## ADR-057-specific checks (the point of this release)

### 1. "A plan's task executions appear as real, navigable sessions" — **PASS**
Every dispatched task carried a real `session_id` (e.g. `session_01KZ4W8CPFV42NTQ3A9Y31DQJX` for the stop_plan test's member task). `GET /api/v1/sessions/{id}` returned full metadata (`type:"task"`, `task_id` linkage, `status`, `agent_id`) **and** a complete ordered transcript (assistant messages, tool calls, judge verdicts as system messages). `GET /api/v1/sessions?agent_id=worker&type=task` listed these sessions alongside my other task sessions, each with its `task_id` — confirming they are indexed and drillable through the general sessions listing, not just reachable by id if you already know it.

### 2. "`stop_plan` genuinely stops the work, incl. sub-turns and background bash" — **PASS**
See TC-20 above — this is the best-evidenced result in the whole report (tool response + plan record + task record + session record + actual OS process table all agree, checked twice on two separate plans).

### 3. "Background bash sessions are attributed to the session that started them" — **PASS for cleanup cascade; FINDING for access scoping (MEDIUM, not a blocker)**
The cleanup-cascade half of this claim is solidly confirmed: `stop_plan` correctly found and killed the background bash job (`sleep 120`, "PLANBG12345") that was started **inside the delegated child's own session**, not just the child's foreground work — proven via the same before/after `ps aux` check in TC-20.

However, I also tested whether the *access* side is session-scoped: from Jim's **root session** (a different session than the one that started it), I called `bash(action="poll", session_id="6242cc2b")` for a background job a delegated worker task had started. **What I actually proved:** the poll succeeded and returned the live status (`{"status":"running"}`), not a "not found"/"access denied" error — this alone is conclusive that `poll` from a foreign session is not blocked. **What I did NOT prove:** whether `kill` from a foreign session actually kills a still-live job. My follow-up `kill` call from the root session returned a terminal status (`{"status":"done"}`, not `"killed"`), but by the time I issued it the job may well have already finished naturally on its own — I have no independent (e.g. `ps aux`) evidence that my `kill` call, rather than natural completion, was what ended it. I am not claiming cross-session kill works; I am only claiming cross-session **poll** works, and explicitly flagging the kill question as untested/timing-ambiguous rather than answered either way. **Finding:** background bash session ids appear to be resolved from a flat/global registry rather than being access-scoped to the session that created them — confirmed for read access (`poll`), unconfirmed for destructive access (`kill`). This does not contradict the "belongs to that child for cleanup purposes" guarantee (which I independently confirmed works via `stop_plan`), but it is a distinct, worth-investigating gap if session-level isolation was also intended for *access*, not just cleanup-cascade ownership — and if cross-session `kill` also works, that would be more security-relevant (one agent's session terminating another's in-flight work) than cross-session `poll` alone. Filing as a **MEDIUM** finding, not a blocker: confirmed impact so far is read-only information leakage (job status visible cross-session), not confirmed unauthorized termination.

### 4. "`bash(action="kill")` actually kills the process" — **PASS** — see TC-26; independently confirmed via `ps aux`, not the tool's return message.

### 5. "`list_jobs` reports honestly" — **PASS**
Checked `list_jobs(kind="plan", include_terminal=true)` after stopping both my plans: both correctly show `status:"failed"`, `native_status:"failed/stopped_by_user"`, `intentionally_stopped:true` — no lingering "running". Checked `list_jobs(kind="task", include_terminal=true, label_contains="UAT4")`: every task's reported status matched its independently-verified REST state — `failed` tasks showed `failed` (including two tasks that were auto-picked-up by an autonomous worker loop and correctly failed with an honest "no actionable objective" reason rather than fabricating success), a `blocked` task correctly stayed `blocked` because its actual blocker never reached `done`, and completed tasks showed `completed`/`done`. No case of a killed/failed/stopped job still reading as running.

**Non-blocking finding (MINOR):** while investigating the above, I noticed that **intermediate task-attempt sessions retain `status:"active"` indefinitely** once superseded by a retry (e.g. three separate `TaskA` attempt sessions all still show `status:"active"` in `GET /sessions` well after the parent plan was stopped and the task reached `status:"failed"`) — only the *final* attempt's session gets flipped to a terminal status (`"interrupted"`). This doesn't affect `list_jobs` (which correctly reports at the task/plan level), but someone drilling into the raw sessions list could be misled into thinking several sessions for one task are concurrently "active" when the task is long since finished.

### Durability check: kill mid-flight → boot-sweep reconciliation — **BLOCKED**
The brief explicitly forbids `fly machine restart`/`fly deploy` in this shared, volumeless environment (a restart would wipe all 4 agents' work). I verified the **cooperative** stop path thoroughly (`stop_plan` cleanly reconciles a mid-flight task without any restart — see TC-20), but the **crash/restart → boot-sweep reconciliation** path specifically requires triggering a restart, which I could not do. Recording this as BLOCKED rather than assuming it works.

---

## Shortcut Detection Results

Explicitly checking the release's named historical defect class ("reports success while doing nothing"):

- **`bash(action="kill")`** — checked independently via `ps aux` before and after; the process is genuinely gone. **Not a shortcut in this build.**
- **`stop_plan`** — checked independently via `ps aux` for both a nested background bash job and a foreground job; both genuinely terminated. **Not a shortcut in this build.**
- **`create_task(priority=0)`** — **IS a shortcut**, on both the tool and REST paths: each returns success (`201`/task id), giving every appearance of having honored the request, but silently discards the explicit `0` and substitutes the default (`3`) with no indication to the caller. Caught by comparing the success claim against the independently-fetched persisted record. **`create_task(priority=6)`** — the **REST** path correctly rejects this (`400`, clear message) — not a shortcut there. The **tool** path, however, silently coerces it to `3` exactly like the `0` case — a shortcut on that path only. Root cause for the `0` case (both paths): Go's `int` zero value makes an explicit `0` indistinguishable from "field absent" before the range check runs.
- **`update_task` force-completing a criteria-bearing task to `"done"`** — correctly *refused* rather than silently accepted; this is the opposite of a shortcut and a positive sign for the release's intent.

## Why not everything is a clean PASS

I found one genuine MAJOR gap (TC-03e) and one MEDIUM access-scoping question (background bash cross-session poll) through direct, reproducible testing — not through narration or assumption. Both are backed by exact reproduction steps below. The overwhelming majority of my scope (32 of 34 checks) passed with independent, non-tool-reported evidence (OS process state, REST records, full session transcripts), which is why the verdict is a genuine PASS-heavy result rather than a suspiciously uniform "everything worked."

---

## Reproduction Steps for the One FAIL

**TC-03e — `create_task` priority validation (MAJOR) — asymmetric across bounds and across entry points**

*Tool path (`create_task`, via chat/WS — my original evidence):*
1. `create_task(agent_id="worker", title="UAT4 prio0", prompt="p", criteria=[{kind:"prose",text:"t"}], priority=0)` → returns `{"task_id":"e04e3ae6-...","status":"next"}` (no error).
2. `GET /api/v1/tasks/e04e3ae6-1aa5-4804-92b7-286d4a1e6258` → `"priority": 3`.
3. `create_task(agent_id="worker", title="UAT4 prio6", prompt="p", criteria=[{kind:"prose",text:"t"}], priority=6)` → returns `{"task_id":"933ff871-...","status":"next"}` (no error) — raw tool-call evidence (`call_id f321242b-...`, `status:"success"`) preserved in my `evidence.jsonl`.
4. `GET /api/v1/tasks/933ff871-a0c3-4385-8b86-6dff60294b12` → `"priority": 3`.
- On the tool path, **both** `0` and `6` are silently accepted and coerced to the default (`3`).

*REST path (`POST /api/v1/tasks`) — independently re-verified by me after the coordinator's report:*
5. `POST /api/v1/tasks {"title":"UAT4-REST-PRIO0",...,"priority":0,...}` → `201`, persisted `"priority":3` — **silently coerced, same bug as the tool path.**
6. `POST /api/v1/tasks {"title":"UAT4-REST-PRIO6",...,"priority":6,...}` → `400 {"error":"task validation: priority must be between 1 and 5, got 6"}` — **correctly rejected.** (This is the one case that differs from the tool path.)
7. `POST /api/v1/tasks {"title":"UAT4-REST-PRIO2",...,"priority":2,...}` (control) → `201`, persisted `"priority":2` — correct, confirming valid values round-trip properly on both paths and this is specifically an invalid-value handling gap, not a general priority-field bug.

- **Expected:** rejection with a clear error for both `0` and `6` (valid range 1–5), per spec and per the project's general "explicit, no-silent-fallback" posture.
- **Actual:** `0` is silently coerced to `3` on **both** paths. `6` is correctly rejected on REST but silently coerced to `3` on the tool path.
- **Root cause (diagnosis, not just symptom):** `0` is Go's zero value for an `int` field — an explicitly-sent `priority:0` cannot be distinguished from "the field was omitted" once decoded, so the same default-fill logic that legitimately handles "no priority given" also swallows an explicit, invalid `0` before any range check runs. The `1–5` range check itself works correctly (proven by REST's clean rejection of `6`) — it just never gets a chance to see `0`. The tool path additionally lacks (or doesn't reach) that same range check at all, since even `6` slips through there.
- **User-facing impact:** a caller who explicitly requests `priority:0` (either entry point) or `priority:6` (tool only) believes their request succeeded as stated; the actual stored priority silently differs with no warning. Low blast radius (priority is advisory scheduling metadata, not a security boundary), but a genuine contract violation, compounded by the fact that the same input can be accepted-and-mangled or cleanly-rejected depending on which entry point issues it.
- **Suggested fix direction (per coordinator):** distinguish "unset" from "explicitly zero" at the decode boundary (pointer field or explicit-presence check) rather than adding a second range check that would repeat the same defect; separately, ensure the tool path invokes the same validation the REST path already has for the upper bound.
- **Severity: MAJOR** (silent data-fidelity loss on an explicit, well-formed call — not a crash, not a security issue).

---

## Key IDs for traceability (all created by me, "UAT4"-prefixed)

- Plans: `01KZ4W2KGWVGEERYD9XMMY0JJ6` ("UAT4 Test Plan"), `01KZ4W7Y0HTESDDK3VDNT0KRGN` ("UAT4 StopPlan Test") — both stopped/failed by me at end of test.
- Tasks: `35987326-...` (Task A), `3cfad043-...` (Task B), `301d7444-...` (Task C), `2037c815-...` (Task D), `a297ac18-...` (stop_plan member task), `7d41d1c3-...` (run_task retry-loop task), `f43c26c3-...` (bg-attribution task), `2d596ed5-...` (rich-params task), plus the priority/title/date boundary tasks (`e04e3ae6-...` / `933ff871-...` via the tool path). REST-path corroboration tasks added during the priority-finding correction: `3f979d81-49a1-4ee8-971a-8d02efb24cbb` (`UAT4-REST-PRIO0`) and `ce93a461-218e-4d66-9e8c-b972c04317a2` (`UAT4-REST-PRIO2`) — both inert, `inbox` status, never dispatched; the `priority:6` REST attempt was rejected pre-creation and left no task record.
- Sessions referenced directly: `session_01KZ4W8CPFV42NTQ3A9Y31DQJX`, `session_01KZ4WG3AVR1PHK3DR2J8Q1VN5`, `session_01KZ4WG6FKECCEZ3QQMHQQPPQF`, `session_01KZ4WJXSHA27XB8NEB1JFN3RT`.

## Scripts/evidence artifacts (not part of the repo, scratchpad only)

- `/tmp/claude-1000/.../scratchpad/uat4/chat.mjs` — the isolated WS chat driver described above.
- `/tmp/claude-1000/.../scratchpad/uat4/evidence.jsonl` — append-only raw WS frame log for every call I made (session_started/tool_call_start/tool_call_result/done), the ground truth behind every quoted JSON result in this report.
