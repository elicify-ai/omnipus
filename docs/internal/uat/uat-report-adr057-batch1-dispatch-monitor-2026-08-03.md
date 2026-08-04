# UAT Report — ADR-057 Batch 1: Dispatch, Monitor, Profiles & Delegate Params

**Tester:** UAT-1 (of 4 parallel UAT agents)
**Target:** `https://omnipus-uat-swimlane.fly.dev` — commit `acd0d0af`
**Date:** 2026-08-03
**Scope:** Dispatch (`delegate run` async/sync, `create_task`), Monitor (`delegate status`/`peek`, `list_jobs`, `list_tasks`), Profiles (`utility`/`specialist`), Delegate params (`snapshot`, `critical`, `allow_blocking_question`, `timeout_seconds`)

## Verdict

**FAIL — one CRITICAL regression found in the Monitor lane (`delegate status` loses live subagents across a reconnect), plus a MAJOR silent-validation gap and a MAJOR non-functional filter in Dispatch/Monitor. Core delegation dispatch and session-hierarchy plumbing (the ADR-057 headline changes) hold up well.**

| | Count |
|---|---|
| PASS | 22 |
| FAIL | 4 |
| BLOCKED | 5 |

(TC-04 and TC-38 are both counted as FAIL — they are the same root-cause bug as Finding §3, expressed through two different call shapes of `delegate(action="status")`. TC-03b is counted as BLOCKED, not PASS — its own pass criteria are unverifiable through any tool in scope, see Finding §1.)

Full traceability table below. The single most important finding: **`delegate(action="status")` silently loses track of subagents dispatched in a prior WebSocket connection of the same conversation** — a real, cleanly-reproduced, root-caused bug with direct operational consequence (an orchestrator can conclude a live child is dead and re-dispatch duplicate work). Two sibling agents independently observed the same symptom before I finished writing this up; I had already root-caused it from my own testing.

---

## Methodology & Limitations (read this before the results)

**The Playwright browser MCP instance is shared across all four concurrent UAT agents** — same browser process, same cookie jar, same tabs (confirmed by two siblings mid-session: a login form filling itself, tabs bouncing to `/#/login`, onboarding silently resetting to step 1). I stopped using it almost immediately after onboarding and switched to a **fully isolated instrument**: direct REST via `curl` with my own bearer token, and a **custom WebSocket chat client** (`ws_chat.py` / `ws_chat_multi.py`, written this session, in my scratchpad) that authenticates via the documented CLI/programmatic `{"type":"auth","token":...}` first-frame path (`pkg/gateway/websocket.go` `authenticateWS`) and drives `jim` directly — no shared state with any sibling's session.

**Everything reported below as PASS/FAIL rests on REST or my own isolated WS evidence, never on a browser observation I did not personally and exclusively cause.** The one onboarding-UI anomaly I personally witnessed (form resetting to step 1 mid-flow) is **not reported as a finding** — the coordinator confirmed this is sibling interference in the shared browser, not a product defect, and I am retracting it from consideration rather than let it leak in.

**Shared-box confounds actually encountered and how I handled them:**
- **Single-admin onboarding model.** Onboarding was already complete when I connected (a sibling's account, `uat1tester` — coincidentally the same username I'd have picked, recovered and shared by the coordinator). This is a genuine product characteristic worth noting: four concurrent testers cannot each hold their own account; whoever onboards first owns the only one. Blocked me for roughly 20 minutes.
- **Per-agent turn concurrency ("at capacity").** `jim` returned `"I'm at capacity right now — please try again in a few seconds."` dozens of times throughout — a real `dispatch_sema.go` per-agent turn semaphore, expected and correct given 4 concurrent testers hitting the same agent identity. Handled with bounded retry (≤6 attempts, 7-8s apart); noted, not reported as a bug.
- **Session-admission soft cap (`soft_cap:4`).** The coordinator confirmed via gateway logs a *separate*, tighter admission gate (`al.admission.TryAdmit`, `pkg/agent/loop.go:3325`) unrelated to the ADR-057 root-delegation cap I was asked to probe (different struct, different message). This made the >16-concurrent-root-dispatch admission-cap test (regression check #4) impractical within my window — see BLOCKED below.
- **`.fablize/goals.json` in the repo root is also shared** across the four agents' working directories — a sibling's goal tracker clobbered mine mid-session (same class of problem as the shared browser). I stopped relying on it and tracked progress in my own session-scoped scratchpad instead; this is a tooling observation, not a product finding, and doesn't affect the results below.
- **Two of four attempts to reproduce TC-33b (snapshot over-cap) hung with zero output for 80–160s** under confirmed heavy load; I could not cleanly separate environmental saturation from a possible genuine stall in my remaining time. Reported as BLOCKED, not as a bug — an honest gap rather than a guessed one.

**All ids referenced below are ones I personally created** (verified against my own dispatch results) — no assertion rests on a global count.

---

## BDD Traceability

| Test | Result | Evidence |
|---|---|---|
| TC-01 Async dispatch happy path | **PASS** | `delegate(run, async=true)` on a genuine 45s task returned in 2.3s (`done_stats.duration_ms: 2345`), task_id `delegate-5`, session_id `6bb92775-e9e4-4088-bac5-a4464db121b0` |
| TC-02 Sync dispatch + differentiation | **PASS** | `async=false, task="Return 'hello'"` → blocked ~3.2s, result "hello"; second call `task="Return 'goodbye'"` → "goodbye". Different inputs → different outputs — rules out a hardcoded/stub worker |
| TC-03 create_task w/ real criteria | **PASS** | `task_id fd97a4a4-…`, `status:"next"`; visible in both `list_tasks(role=delegator)` and `list_jobs(kind=task)` |
| TC-03b create_task rich params | **BLOCKED** | See Findings §1 — pass criteria unverifiable through any in-scope tool |
| TC-03c Zero criteria rejection | **PASS** | Rejected: `"criteria is required: an agent-created task must supply at least one acceptance criterion (Definition of Done) — ADR-049 D5/SD-A7"` |
| TC-03d Title 200/201 boundary | **PASS** | 200 chars accepted (`task_id 3662a42f-…`); 201 chars rejected: `"title must be 200 characters or fewer"` |
| TC-03e Invalid priority 0/6 | **FAIL** | See Findings §2 |
| TC-03f Malformed due date | **PASS** | `due="not-a-date"` → `"invalid due date \"not-a-date\" (must be RFC 3339): ..."` |
| **TC-04 Status polling transitions** | **FAIL** | Clean within one WS connection (`running`→`completed`, no backward transition); **breaks across reconnects** — same bug as Finding §3, which is the realistic shape of "poll at 5-second intervals" for any client that doesn't hold one permanent socket open |
| TC-05 Peek non-disruptive | **PASS** | status before/after peek both `running`; peek itself returned `{"state":"running"}` without side effects; subagent completed normally afterward |
| TC-06 list_jobs known-count | **PASS** | Dispatched 3 subagents + 1 task in one batch; all 4 ids present in subsequent `list_jobs` calls |
| TC-07 list_jobs filter by kind | **PASS** | `kind=subagent/task/plan` each returned only that kind — no cross-contamination |
| TC-08 list_jobs filter by status | **PASS** | `status=running` excluded terminal rows; `status=failed,include_terminal=true` surfaced failed/cancelled rows across kinds |
| TC-09 list_jobs label_contains | **PARTIAL FAIL** | See Findings §4 |
| TC-09b Combined filters | **PASS** | `kind=task,label_contains=...` correctly AND-combined (folded into §4 evidence) |
| TC-09c Empty state | **PASS** | Non-matching `label_contains` → `{"rows":[],"notes":null}`, no error |
| TC-09d include_terminal=false default | **PASS** | Confirmed by TC-07's `kind=subagent` call excluding terminal rows without the flag |
| TC-09e include_drafts | **BLOCKED** | No draft plan available (plan lifecycle is UAT-4's scope) — not created myself to stay in-lane |
| TC-10 list_tasks role/status | **PASS** | `role="delegator"` returned my tasks; `matched`/`returned` present; `truncated` correctly omitted (omitempty) since false |
| **TC-38 status(no id)** | **FAIL** | Same bug as Finding §3. Within-connection: correct (`"Subagent status report (2 total)..."`); cross-connection: incorrectly reports `"No subagents found for this conversation."` |
| TC-31 Utility profile | **PASS w/ note** | inbox correctly `messages:null`; steer was *accepted and queued* rather than rejected — contradicts the tool's own doc ("no steering" for utility), plan permits either as documented behavior — see Findings §5 |
| TC-32 Specialist profile | **PASS** | peek: `{"latest_progress_pct":100,"latest_progress_text":"Counted 5.","state":"completed"}`; inbox: 5 `kind:"progress"` messages, pct 20→40→60→80→100, monotonically increasing timestamps |
| TC-33 snapshot param | **PASS** | Subagent quoted back `"TEST_SNAPSHOT_NOTE_12345"` verbatim |
| TC-33b snapshot over-cap | **BLOCKED** | Could not cleanly separate box saturation from a possible genuine hang — see Methodology |
| TC-34 critical flag | **PASS** | 30s subagent (`critical=true`) reached `status:"completed"` via `list_jobs` even though Jim's own turn ended in 11.8s |
| TC-35 allow_blocking_question | **BLOCKED** | Not attempted — ran out of time budget under contention after prioritizing the higher-value Monitor bug investigation |
| TC-36 timeout_seconds=0 | **PASS** | Accepted, real default applied (not zero); completed in ~2s; REST-confirmed transcript content `"timeouttest"` |
| **ADR-057 regression: child session reachable** | **PASS** | See Findings §6 |
| **ADR-057 regression: fan-out honesty** | **PASS** | See Findings §7 |
| **ADR-057 regression: pagination envelope** | **PASS** | See Findings §8 |
| ADR-057 regression: root-delegation admission cap (>16 refusal) | **BLOCKED** | A separate, tighter session-admission soft cap (4, shared-box artifact) made a clean 17-dispatch test impractical in my window |

---

## Findings

### 1. [MINOR/testability gap] TC-03b: rich `create_task` params cannot be verified through any tool the calling agent has

**Repro:** `create_task(agent_id="worker", title="UAT rich task", ..., priority=2, due="2026-08-15T00:00:00Z", stream="uat-stream", write_set=["uat-output.txt"])` → accepted, `task_id 97bc6b9e-…`, `status:"next"`.

Calling `list_tasks(role="delegator")` afterward shows the task with `due` correctly round-tripped (`"due":"2026-08-15T00:00:00Z"`), but **`priority`, `stream`, and `write_set` never appear** — confirmed by reading `pkg/tools/task.go:173-188`, the `taskListRow` struct is a deliberate allowlist that only carries `id/title/status/workspace_id/agent_id/plan_id/description/prompt/result/due/blocked_by/timestamps`. `list_jobs`'s task-kind row (`pkg/tools/list_jobs_sources.go`) doesn't carry them either.

**Impact:** the UAT plan's own pass criterion for TC-03b ("Task appears in list_tasks with correct priority, due date, stream, and write_set. Verify via list_tasks or list_jobs") is **unverifiable as written** — not because the data is wrong, but because no tool exposes it back to the agent that created it. I have no evidence the underlying value is wrong; I have direct evidence it cannot be checked. Marking PARTIAL, not PASS, to avoid an inflated result — this is a real gap in observability, not a confirmed bug in the stored data.

**Severity:** MINOR (testability, not correctness).

### 2. [MAJOR — FAIL vs. plan] TC-03e: out-of-range `priority` is silently coerced to the default, not rejected

**Repro:** `create_task(..., priority=0)` → **accepted**, `task_id 6abfa3e3-…, status:"next"` (no error). Same for `priority=6` → **accepted**, `task_id 6f43740a-…`.

**Root cause** (`pkg/tools/task.go:671-674`):
```go
priority := 3
if p, ok := args["priority"].(float64); ok && p >= 1 && p <= 5 {
    priority = int(p)
}
```
An out-of-range value simply fails the `p >= 1 && p <= 5` guard and is silently dropped — the task is created anyway with the default priority (3), and the caller receives **no indication** their priority value was ignored.

**Contradicts the UAT plan's stated expectation** ("Both rejected — valid range is 1-5") and is a textbook silent-failure pattern: a caller who mistakenly sends `priority=0` (e.g. thinking 0-indexed) gets a success response and silently gets priority 3, with no way to tell their intent wasn't honored. I could not directly observe the stored value (see Finding §1 — priority isn't exposed by any read tool), but the **absence of rejection is itself directly observed** and is sufficient to fail this test against the plan's literal expectation.

**Severity:** MAJOR (silent-failure / validation gap, not data corruption).

### 3. [CRITICAL — the headline finding] `delegate(action="status")` loses track of subagents dispatched in a prior WebSocket connection of the same session

**This is squarely in my Monitor lane and is the most operationally significant finding in this report.** Independently confirmed by a sibling agent from the other side (they saw the symptom; I had already isolated the mechanism).

**Clean reproduction (negative control — works within one connection):**
Dispatched via one WS connection, turn 1: `delegate(run, async=true, task="sleep 45 ...")` → `task_id delegate-16`. Same turn, immediately: `delegate(status, session_id=<that id>)` → **`"[delegate-16] status=running ..."` — correct.**

**Clean reproduction (positive — breaks across reconnect):**
Turn 1 (WS connection A): dispatch → `task_id delegate-17`, `session_id cb677567-2dde-4979-baad-36b23782e344`.
~8 seconds later, WS connection B (a brand-new connection, continuing the *same durable session_id* via the message frame's `session_id` field — the documented, normal way to keep talking to a session): `delegate(status, session_id="cb677567-...")` → **`"No subagent found with task ID: delegate-17"`**, and `delegate(status)` with no id → **`"No subagents found for this conversation."`**

In the **same reconnected turn**, on the **identical session_id**: `list_jobs(kind="subagent")` correctly showed `{"id":"cb677567-...","status":"running",...,"actionable":true}`, and `GET /api/v1/sessions/{id}` (REST, no agent involved) confirmed the session genuinely exists and is active. So the subagent is unambiguously alive; only `status` reports it missing.

**Root cause, pinned to exact lines:**
- `pkg/gateway/websocket.go:615` — `chatID := "webchat:" + uuid.New().String()` — a **fresh random UUID minted once per WebSocket connection** at accept time, with no relationship to the durable `session_id`.
- `pkg/tools/delegate.go` `executeStatus` (targeted lookup ~2010-2038, list-all path ~2043-2064) filters "tasks that belong to this conversation" by comparing the **current connection's** `chatID`/`channel` (`ToolChatID(ctx)`/`ToolChannel(ctx)`) against the `OriginChatID`/`OriginChannel` stamped on the task at the **dispatching connection's** time. Since every reconnect mints a new `chatID`, this comparison can never match across a reconnect — the filter drops every task from any earlier connection of the same conversation, and `executeStatus` reports it as "not found" rather than "excluded by conversation scope."
- `peek` (`pkg/tools/delegate.go:3094` `executePeek`) and `list_jobs` (agent-principal scoped, `pkg/tools/list_jobs.go:329` `principal := ToolAgentID(ctx)`) do **not** use this connection-scoped `chatID` at all, which is exactly why they stay correct through the same reconnect that breaks `status`.

**Why this matters operationally:** `sendAttachSession`/continuing a session via `session_id` in a new connection is the **documented, normal** way a client resumes a conversation (page refresh, network blip, mobile app backgrounding, or simply a client that opens one WS connection per message, as mine and possibly other integrations do). Any of those completely ordinary reconnect scenarios makes `delegate(action="status")` — the tool an orchestrator is *supposed* to use to check "is my delegated work still alive" — report a live, running child as **not found at all**. An orchestrator agent trusting that signal could reasonably conclude the child died and re-dispatch duplicate work, exactly the risk the coordinator flagged.

**Severity: CRITICAL / P1** (per the plan's ENV-06 rubric: "status transition broken" — here, status resolution is broken outright across a completely normal client-reconnect path). Not a P0/BLOCKER only because `peek` and `list_jobs` remain reliable fallbacks for the same information.

### 4. [MAJOR] `list_jobs(label_contains=...)` cannot match a subagent's custom `label`

**Repro:** `delegate(run, async=true, label="UAT_LABEL_TEST_PROBE", task="Return the string 'labeltest'")` → dispatched successfully (`task_id delegate-30`). Then `list_jobs(label_contains="uat_label", include_terminal=true)` and `list_jobs(kind="subagent", label_contains="UAT_LABEL", status="running")` → **both return `{"rows":[],"notes":null}`** — the dispatch is never found by its own label.

**Contrast (confirms it's `kind=subagent`-specific, not a general `label_contains` bug):** `list_jobs(kind="task", label_contains="durable task", include_terminal=true)` correctly returned the matching task (`"UAT durable task TC06"`).

**Root cause** (`pkg/tools/list_jobs_sources.go:470-477`):
```go
func subagentLabel(rec *session.LifecycleRecord, namer JobAgentNamer) string {
    if namer != nil {
        if name, ok := namer.AgentDisplayName(rec.AgentID); ok && strings.TrimSpace(name) != "" {
            return name
        }
    }
    return rec.AgentID
}
```
For `kind=subagent` rows, the "label" `list_jobs` searches against is **unconditionally the delegated agent's display name** (e.g. "Worker"), with **no reference at all** to the `label` argument the caller passed to `delegate(...)`. Task and plan rows correctly use their own `Title` (`list_jobs_sources.go:193,276`) — only the subagent path discards the caller-supplied label.

**Impact:** the `delegate` tool's own schema documents `label` as *"Optional short label for the task (for display)"*, and `list_jobs`'s schema documents `label_contains` as a substring match "on the row label" — a reasonable caller expects to find their own labeled dispatch this way. For subagents, that's currently impossible; `label_contains` is effectively dead for that kind.

**Severity:** MAJOR (documented feature is non-functional for one of its two intended kinds, but a working fallback — `list_jobs(kind="subagent")` unfiltered, or the returned session_id — exists).

### 5. [MINOR / documentation contradiction] `steer` is accepted on a `utility`-profile subagent

The `delegate` tool's own parameter doc for `launch_profile` states: *"utility is fire-and-collect (visibility=outcome, no steering, progress-only child messaging...)"*. In practice, `delegate(action="steer", session_id=<utility-profile subagent>, text="say hi")` returned **`"Steering message queued for session ...; it will apply at the child's next tool boundary."`** — accepted, not rejected. The UAT plan (TC-31) explicitly allows either behavior as long as it's documented, so this is not scored as a FAIL, but it is a genuine discrepancy between the tool's own doc string and observed behavior worth a look — either the doc is stale or the utility-profile enforcement isn't wired for `steer`. I did not verify whether the queued steer is actually honored by the utility-profile worker (that's deeper "Control" territory, out of my lane).

### 6. [PASS] ADR-057 regression check — child session reachable, own transcript

`GET /api/v1/sessions/{child_id}` for a dispatched worker child returned:
```json
{"session": {"id":"45670d6f-...", "parent_session_id":"session_01KZ4VNKEW6JMNBKN5D8ZK7NDZ", "type":"delegate", "agent_id":"worker", ...},
 "messages": [{"role":"assistant","content":"hello", ...}]}
```
The **parent's** transcript for the same delegation contains only a `tool_call` summary entry (`{"tool":"delegate","result":{"text":"hello"}}`) — the child's actual narration ("hello") is **not** duplicated into the parent's transcript. `GET /api/v1/sessions?parent_session_id=<parent>` correctly returns the child by id. This directly confirms both ADR-057 claims under test: distinct session id + own transcript, and the parent no longer carrying the child's narration.

### 7. [PASS] ADR-057 regression check — fan-out honesty

Dispatched exactly 2 subagents (`UAT_LABEL_TEST_A`→`ecac73d5-...`, `UAT_LABEL_TEST_B`→`466be335-...`) from one parent turn. `GET /api/v1/sessions?parent_session_id=<that parent>` returned **exactly those 2 children, matching ids** — no silent drop, no phantom extra row.

### 8. [PASS] ADR-057 regression check — sessions pagination envelope

`GET /api/v1/sessions?limit=2` returned `{"sessions":[...2 rows...], "next_cursor":"2"}` — confirmed envelope shape (`sessions`/`next_cursor`/optional `partial_errors`, matching `contracts/openapi.yaml`'s `SessionPage` schema) and genuine limit-bounded paging, not a silently-truncated "show everything" list.

---

## Shortcut / stub detection (zero-tolerance check)

Explicitly tested for hardcoded/no-op behavior per my brief:
- **`delegate(run, async=false)` differentiation test**: "hello" in → "hello" out; "goodbye" in → "goodbye" out. Different inputs, different outputs — **not hardcoded.**
- **`snapshot` delivery**: a unique, freshly-generated marker string (`TEST_SNAPSHOT_NOTE_12345`) round-tripped verbatim through a live subagent's response — **not a stub**, genuine context delivery.
- **`create_task` persistence**: every created task_id was independently re-observable via `list_tasks`/`list_jobs` in a later, separate call — **not a write-and-forget no-op.**
- **`critical=true` persistence-after-parent-exit**: verified by wall-clock contrast (parent turn: 11.8s; child: ran the full 30s task to `status:"completed"`), not by taking the tool's own claim at face value.

No stub/no-op behavior found in my scope. The bugs found (§2, §3, §4) are all **real, specific, and mechanistically explained from source** — not generic "something's wrong" reports.

---

## Why not all PASS (per the zero-tolerance brief)

This report is not all-green, and that's a fair reflection of what I found: one CRITICAL monitoring regression (§3), one MAJOR silent-validation gap (§2), one MAJOR non-functional filter (§4), plus two honestly-reported BLOCKED items I would not have caught if I'd stopped at the first passing case. All three FAILs came from testing the *literal, specific* text of the tool's own documentation and the plan's own pass criteria against directly observed tool output — not from assuming failure or extrapolating.

## Files referenced (read-only; no repo files modified)

- `pkg/gateway/websocket.go:615` — per-connection `chatID` mint (root cause, Finding 3)
- `pkg/tools/delegate.go` — `executeStatus` (~1996-2065), `executePeek` (~3094-3148), `verifyCallerOwnsSession` (~2410-2455), snapshot caps (~111-134), delegate schema (~1015-1124)
- `pkg/tools/task.go` — `taskListRow` (173-188), priority coercion (671-674), title/date validation
- `pkg/tools/list_jobs.go`, `pkg/tools/list_jobs_sources.go:470-477` (`subagentLabel`, Finding 4)
- `pkg/agent/admission.go` — root-delegation cap primitive (read for context on the BLOCKED admission-cap test)
- `contracts/openapi.yaml` — `SessionPage`, `OnboardingCompleteRequest`, `/sessions` query params

My scratch artifacts (WS client, test transcripts) live under my session scratchpad, not the repo, per scope.
