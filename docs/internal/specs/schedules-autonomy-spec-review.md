# Spec Grill Report: Scheduled Agent Autonomy & Schedules (#264)

**Spec**: `docs/internal/specs/schedules-autonomy-spec.md`
**Reviewed**: 2026-06-02
**Detected mode**: `plan-spec` (full BDD + traceability + TDD plan present)
**Verdict**: **BLOCK** — 5 CRITICAL, 11 MAJOR, 7 MINOR, 4 OBSERVATION

---

## Executive Summary

This spec is well-structured and traces cleanly, but it is grounded on **three
reference mechanisms that do not exist in the codebase as described**: `pkg/cancel`
("session-ownership clear"), a per-agent **main session / heartbeat enqueue** API,
and a **Command Center "Attention"** surface. Two of the locked guardrails
(the 300s timeout, the concurrency-cap=5 lane) **collide with existing deliberate
design** (`agents.defaults.timeout_seconds = 0` by design; cron executes jobs
**sequentially** with no goroutine lane today). An implementing agent following
this spec literally will wire calls to symbols that aren't there and contradict
an existing tuning decision. The contract section names wire types (`Attention`,
`schedule_run`) with no schema sketch, and the security model for *who may
schedule whom* is entirely absent.

---

## CRITICAL Findings

### C-1 — `pkg/cancel` / "session-ownership clear" mechanism does not exist
**Lens**: Incorrectness / Infeasibility · **Sections**: Reference table row 2 (line 24), FR-006, AS US3.1, SC-002, Behavioral Contract, Edge Cases.

The spec repeatedly cites "`pkg/cancel` + session-ownership clear (used by #265 shutdown drain)" as the reusable pattern for the timeout path, and FR-006/SC-002 require "force-clear the run's session ownership." **There is no `pkg/cancel` package.** The actual cancel entry point is `pkg/agent/cancel.go::RequestCancel` operating on `CancelScope{SessionID}` + `CancelHooks{SetSessionInterrupted, CancelPendingApprovals}`. There is **no "session ownership" concept** anywhere in `pkg/session`/`pkg/agent` (only `attach_hydrate.go`'s unrelated `sessionOwner` string). #265's shutdown (`pkg/gateway/shutdown.go`) drains via `agentLoop.WaitForActiveRequests()` + `Close()`; it does **not** "clear session ownership."

**Fix**: Replace every "session-ownership clear" reference with the real mechanism. Define precisely what "force-clear" means in terms of existing primitives: e.g. "call `agentLoop.RequestCancel(CancelScope{SessionID: runSessionID}, ...)` with `SetSessionInterrupted` so the session's `meta.json` Status becomes `interrupted` and pending approvals are auto-denied." Cite `pkg/agent/cancel.go`, not `pkg/cancel`. If a new "ownership" notion is genuinely needed, the spec must define it as new work, not reuse.

### C-2 — `main` session mode has no existing mechanism; heartbeat runs the DEFAULT agent, not a per-agent main session
**Lens**: Infeasibility / Incompleteness · **Sections**: US2 (mode=main), FR-004, FR-019, Reference table row 1, Edge case "main wake when the agent has no main session yet", Dataset row 3/4.

The spec assumes `heartbeat.Service` can "enqueue a system event into the owning agent's main session / wake the heartbeat lane (`wake now` / `wake next-heartbeat`)." Inspection of `pkg/heartbeat/service.go` shows: (a) **no** enqueue/wake/system-event API of any kind; (b) `executeHeartbeat()` calls a single handler that ultimately runs `ProcessHeartbeat`, which **always uses `GetDefaultAgent()`** (`pkg/agent/loop.go:2935`) and runs with `NoHistory:true`. There is **no per-agent "main session" concept** in the codebase. So "wake the *owning* agent's main session" requires building: a per-agent main-session identity, an enqueue queue, and a heartbeat hook that targets a specific agent — none of which exist.

**Fix**: Either (a) descope `main` mode from v0.1.0 and ship only `isolated`+`continue` (FR-019 is already "SHOULD"), or (b) add an explicit sub-spec section defining the new heartbeat enqueue API, what "the agent's main session" *is* (session id derivation), how `wake=next-heartbeat` is buffered and drained, and how it interacts with the default-agent-only heartbeat today. Do not present `main` as "reuse existing heartbeat."

### C-3 — Command Center "Attention" surface does not exist in backend or contract
**Lens**: Incompleteness / Infeasibility · **Sections**: Summary, US4 (AS3), FR-013, SC-006, Behavioral Contract, Integration Boundaries (Gateway), Ambiguity #6.

FR-013/SC-006 require raising "a Command Center **Attention** item." Searching `pkg/gateway/*.go` and `contracts/components/schemas/` finds **no Attention type, endpoint, store, or WS frame** (the only "Attention" hits are minified strings in the prebuilt SPA bundle and unrelated audit comments). `ActivityEvent` exists, but its `type` is a **closed enum** (`session_start|task_created|task_updated`) — it cannot represent a scheduled-run or attention event without a contract change.

**Fix**: The spec must (a) define the Attention concept as **new** work — its persistence (where stored? cron state? a new file?), its wire schema (`Attention.yaml`), the endpoint(s) to list/dismiss it, and its WS frame — OR (b) descope "Attention" to "reuse the existing Activity feed" and extend `ActivityEvent.type` enum with `schedule_run_failed`/`schedule_run` via the 5-step contract process. Either way, name the new schemas and list them in the Integration Boundaries. As written, FR-013 is not implementable.

### C-4 — Concurrency cap of 5 assumes a parallel run lane; cron executes jobs SEQUENTIALLY today
**Lens**: Incorrectness / Overcomplexity · **Sections**: US3 (AS3), FR-007, SC-003, Impact row 4, Edge case "concurrency cap reached".

`CronService.checkJobs()` collects due job IDs and runs `executeJobByID` **one at a time in a `for` loop on the cron goroutine** (`pkg/cron/service.go:209-212`). There is no goroutine-per-run, no worker pool, no lane. A "global concurrency cap of 5 with queueing" only makes sense if runs are concurrent — which means this feature must **introduce parallel execution** plus a semaphore plus a queue, a non-trivial concurrency redesign (with its own race/shutdown surface). The spec frames this as a guardrail "cap" as if parallelism already exists. Worse: today's sequential model already gives natural overlap protection that the spec's "skip-if-running" (FR-008) duplicates.

**Fix**: State explicitly that this feature changes the cron execution model from sequential to a bounded-parallel lane (N=5 semaphore + queue), and specify: where the lane lives (cron service vs. gateway), how it is created/sized, how queued runs are ordered (FIFO? due-time?), and how the lane drains on shutdown (ties to C-5). Add a test asserting two independent jobs *can* run concurrently (the parallelism the cap presupposes), not just that the 6th queues. Re-justify FR-008 vs. FR-007 — confirm both are needed once parallelism exists.

### C-5 — Shutdown drain (#265) does NOT wait for in-flight cron `onJob`; spec's "drain the autonomous lane" is unspecified and currently broken
**Lens**: Incompleteness / Inoperability · **Sections**: Impact row 4, Relevant Flows ("Shutdown drain #265"), Regression `TestShutdown_DrainsAutonomousLane`, Edge case "timeout fires but agent ignores cancellation."

`shutdown.go` calls `CronService.Stop()` (step 1), which only sets `running=false` and closes `stopChan` — it returns **immediately and does not block on a goroutine running inside `executeJobByID`/`onJob`**. Today that's fine because `onJob` runs synchronously inside `ProcessDirectWithChannel`, so its turn is counted by `activeRequests` and `WaitForActiveRequests()` covers it. But once C-4 introduces a parallel lane with its **own** goroutines and a **queue**, those queued/in-flight runs are NOT tracked by `activeRequests` unless explicitly wired, and `Stop()` won't drain them. The spec asserts "the lane must drain like #265" without saying *how*. The interaction with the per-run 300s timeout + cancel (C-1) during a 70s shutdown budget is undefined: does a run get its full 300s, or is it cancelled at the shutdown deadline?

**Fix**: Specify the lane lifecycle on shutdown: (1) `CronService.Stop()` must stop *accepting* new due jobs AND block (bounded) until in-flight lane goroutines finish or are cancelled; (2) each lane run must register with `activeRequests` (or an equivalent waitgroup the shutdown path waits on); (3) define the precedence between the per-run timeout and the shutdown budget (recommend: shutdown cancels runs immediately via the run context, marking them `interrupted`, not waiting 300s). Make `TestShutdown_DrainsAutonomousLane` assert a queued run is dropped/cancelled cleanly and no goroutine leaks.

---

## MAJOR Findings

### M-1 — 300s timeout contradicts an existing deliberate design (`timeout_seconds = 0` by design)
**Lens**: Inconsistency · FR-003, US3, SC-002.
`config/defaults.go:71` sets `Agents.Defaults.TimeoutSeconds = 0` with the comment *"disabled; OpenRouter queue delays make fixed timeouts unreliable."* The spec mandates a hard 300s scheduled-run deadline. These can both be true (per-turn vs. whole-run), but the spec never reconciles them: is the 300s a *new* schedule-scoped config key distinct from `agents.defaults.timeout_seconds`? Does it override the per-turn disable? A long legitimate OpenRouter-queued scheduled run could be killed at 300s, producing false-failure alerts (alert-loop risk).
**Fix**: Name the new config key explicitly (e.g. `agents.defaults.scheduled_timeout_seconds`), state it is independent of the per-turn timeout, document why a fixed deadline is acceptable here despite the OpenRouter-queue caveat, and define behaviour when the model is slow-but-progressing vs. truly hung.

### M-2 — JobHandler already returns `(string, error)`; spec's "modify signature" is stale and under-specified for context/deadline
**Lens**: Incorrectness · Symbols table rows for `JobHandler`, `ExecuteJob`, gateway wiring.
The spec says `JobHandler` is `func(job *CronJob)(string,error)` and proposes it "carries a context with deadline." But `ExecuteJob(ctx context.Context, job *cron.CronJob) string` already takes a ctx — the gateway wiring just passes `context.Background()` (`gateway.go:1780`). The real change is *who creates the deadline-bound ctx and how the owner is threaded*, not the signature. The spec also lists `ExecuteJob` returning `string` at "305-388" but the handler adapter wraps it to `(string,nil)` — errors from a failed agent run are swallowed (`ExecuteJob` returns `"Error: %v"` as a *success string*, never an `error`). FR-013 ("errored run with no reply = failure") cannot work until this is fixed.
**Fix**: Specify that `ExecuteJob` must return `(string, error)` (or the adapter must map run errors to a non-nil error) so `executeJobByID` records `LastStatus="error"`. Today every run records `ok` regardless. Add this to the impact analysis and a regression test.

### M-3 — Owner threading: no metadata path exists on the cron→agent message
**Lens**: Incompleteness · FR-001, US1, `resolveMessageRoute` extension.
`ProcessDirectWithChannel` constructs `InboundMessage{SenderID:"cron"}` with **no `agent_id` metadata**, so `resolveMessageRoute` falls to `ResolveRoute`→default agent (the bug). The spec proposes a new `ProcessScheduled(ctx, ownerAgentID, ...)` — good — but `resolveMessageRoute`'s explicit-agent path keys off `inboundMetadata(msg,"agent_id")` and also **clears/loads `sessionActiveAgent` handoff override**. A scheduled fire must NOT mutate a human's handoff override for that session scope. The spec's "honor an explicit agent_id override" doesn't address the handoff side-effect.
**Fix**: Specify that `ProcessScheduled` bypasses `resolveMessageRoute` entirely (resolve owner directly via `registry.GetAgent(ownerID)`, error if missing — FR-001) rather than injecting `agent_id` metadata, so it cannot perturb `sessionActiveAgent`. Add the regression `TestRoute_HumanTrafficUnaffectedByOwnerOverride` to assert the override map is untouched.

### M-4 — Security: no authorization model for *who may schedule which agent*
**Lens**: Insecurity (Elevation of Privilege) · US5, FR-015, Assumptions.
The spec says "authenticated admin" creates schedules but never states: (a) may a non-admin (`UserRoleUser`) create/edit/delete schedules? (b) may any user schedule *any* agent, including a privileged agent with `system.*` allow? (c) the cron *tool* path lets an agent self-schedule — can agent A create a schedule owned by agent B, escalating into B's tool policy? Roles exist (`UserRoleAdmin`/`UserRoleUser`, `pkg/config`). A scheduled run executes with the **owner agent's** full tool policy and auto-denies only `ask` (auto-*approves* nothing, but `allow`-policy privileged tools run unattended). Scheduling a privileged agent is therefore a privilege-amplification vector that the spec doesn't gate.
**Fix**: Add an authz requirement: which role may CRUD schedules; whether owner must be an agent the caller is permitted to drive; and an explicit STRIDE note that a scheduled run inherits the owner's `allow` tools with no human present (document the residual risk and whether `system.*`-allowed agents may be scheduled at all). Add a 403 test for non-admin (or whatever the decision is).

### M-5 — Auto-deny `ask` interaction with `PolicyApprover` is under-specified (fail-open risk)
**Lens**: Insecurity / Silent failure · US3 (AS4), FR-009.
The approval gate is `pkg/agent/tool_approver.go::PolicyApprover.RequestApproval` (reasons include `"timeout"`, `"cancel"`, `"saturated"`). The spec says scheduled runs "auto-deny `ask`" but doesn't say *how*: a new non-interactive `PolicyApprover` implementation? a per-run flag read by the existing approver? If wired wrong, a scheduled run could hit the **default `nopPolicyApprover`** (denies all) accidentally, or worse, hit a real approver that *blocks* waiting for a human who will never come (stalling the run until the 300s timeout — defeating the point). The spec also doesn't define what the agent *sees* after auto-deny (does it retry the same tool in a loop, burning the timeout?).
**Fix**: Specify the exact mechanism (recommend: pass a `nonInteractive`/`autoDeny` flag through `processOptions` so the loop denies with reason `"scheduled_auto_deny"` *without* calling any approver). Define loop-guard behaviour so a model that re-requests the denied tool can't spin until timeout. Add a test that an `ask` tool is denied with **zero** approver invocation, not just "auto-denied."

### M-6 — Run-history shape that the contract must encode is unspecified (the prompt's explicit concern)
**Lens**: Incompleteness · US5/US6, Ambiguity #5, Integration Boundaries (Gateway).
US6 AS4 requires the detail Sheet to show "run history … with a link to the run's session," but `CronJobState` only holds `LastRun/LastStatus/LastError` (single last run). Ambiguity #5 *assumes* "keep last N=20 run records inline" but this is unresolved AND it directly shapes the `Schedule` wire type and on-disk `jobs.json`. The contract cannot be authored without deciding: is run history an array on the job (`runs[]: {ranAt, status, error, sessionId}`), a separate file, or derived from sessions? This is load-bearing for back-compat (M-7).
**Fix**: Resolve Ambiguity #5 *before* contract authoring (the spec's own GATE note admits #5 affects the contract). Define the `ScheduleRun` schema fields explicitly (`ran_at`, `status`, `error`, `session_id`, `attempt`, `duration_ms`), the retention (N), and where it lives. Add to `CronJobState` accordingly.

### M-7 — jobs.json back-compat is asserted but not validated for run-history / enum additions
**Lens**: Incompleteness · Integration Boundaries (Cron store), Impact row 1, `TestCronLoad_BackCompatDefaultsOwnerEmpty`.
"New fields optional/default-zero" is fine for scalars (`AgentID:""`, `TimeoutSeconds:0`), but: (a) an old job with `AgentID:""` will hit the **owner-missing error path** (FR-001) and alert on every fire — a silent regression for every pre-existing job (e.g. a user's "remind me" cron). The spec must define the migration: empty owner on a legacy job ⇒ default to ? (the historical default-agent behaviour, or migrate to a designated owner) — NOT "owner-missing error + alert spam." (b) Adding run-history arrays (M-6) changes the on-disk shape; the back-compat test only checks owner-empty.
**Fix**: Add an explicit migration rule for legacy ownerless jobs (recommend: on load, backfill `AgentID` to the current default agent and log once, preserving today's behaviour) so existing crons don't all start erroring. Extend the back-compat test to cover a legacy job firing successfully, and a job with no `runs[]` array loading.

### M-8 — "Default channel" alert target is ambiguous and can produce alert loops / silent drops
**Lens**: Ambiguity / Insecurity (DoS) · US4, FR-013, Assumptions, Ambiguity #6.
"Owning agent's default channel" is defined in Assumptions as "resolved from the agent's bindings / default routing," but `pkg/routing` resolves *inbound* routing (which agent answers a channel), not *outbound* (which channel an agent posts to). An agent can be bound to many channels or none. If the alert itself fails to deliver (channel down), does it alert again (loop)? A flapping schedule (fails every minute) with `next-heartbeat` off could spam the channel. Ambiguity #6 flags Attention dedup but not channel-alert dedup.
**Fix**: Define outbound alert-channel resolution precisely (first binding? a configured `alert_channel`? Command Center only if none). Add rate-limiting/coalescing for repeated failures of the same schedule (e.g. alert once, then suppress until success or N minutes), and make alert-delivery failure record-only (never re-alert). Resolve Ambiguity #1 (auto-pause after N failures) as the loop circuit-breaker.

### M-9 — Missed-schedule "fire once forward" relies on `NextRunAtMS` semantics the spec doesn't pin
**Lens**: Incorrectness · FR-018, Edge case "clock skew / due far in past", `TestSchedule_MissedDowntimeFiresOnceForward`.
Current `Start()`→`recomputeNextRuns()` recomputes `NextRunAtMS` for **enabled** jobs at boot using `computeNextRun(now)` for `every`/`cron`, which already pushes forward (no catch-up). But for `at` jobs whose time passed during downtime, `computeNextRun("at")` returns `nil` (since `AtMS <= now`), so the one-shot **never fires** — it's silently lost, not "fired once forward." The spec's FR-018 says "fire once on next tick" but the code path for a missed `at` job is "drop." This is the opposite of the stated requirement for one-shots.
**Fix**: Specify the missed-`at` behaviour explicitly: fire once immediately on boot if `now - AtMS` is within a grace window, else drop with an Activity event. Distinguish `every`/`cron` (already forward, no fire) from `at` (fire-once-or-drop). The test must cover a missed `at` job, not just recurring.

### M-10 — Test determinism: the entire `pkg/cron` is hardwired to `time.Now()`; "injected clock" is asserted but absent
**Lens**: Infeasibility / Test determinism · Non-Behaviors ("no wall-clock sleeps"), TDD plan ("deterministic clock"), SC-010.
`service.go` calls `time.Now()` in `runLoop`, `checkJobs`, `executeJobByID`, `computeNextRun`, `AddJob`, etc. — there is **no clock seam**. Backoff `[60s,120s,300s]`, the 300s timeout, overlap windows, and "fire once forward" all need a controllable clock to test without wall-clock waits (SC-010 forbids sleeps; #265 flake class). The spec mandates an injected clock but never specifies introducing one into `pkg/cron`/the lane.
**Fix**: Add a requirement to introduce a `Clock` interface (or `now func() time.Time`) into `CronService` and the run lane, threaded so tests inject a fake clock and advance it. Specify backoff scheduling as clock-driven (not `time.AfterFunc` against wall-clock). Without this, FR-010/SC-005 cannot be tested deterministically.

### M-11 — `continue` session id storage and concurrency vs. session sharding
**Lens**: Incompleteness / Concurrency · US2 (continue), `CronJob.SessionID`, Edge case "continue pruned."
`continue` mode stores a persistent `SessionID` on the job and reuses it. But: (a) two overlapping fires are skipped (FR-008), so within one job there's no race — fine. (b) The spec doesn't say who writes `LastSessionID`/`SessionID` back to `jobs.json` and under what lock, given `executeJobByID` already re-locks after the run. (c) `continue`+`isolated` both write `CronJobState` post-run; the spec must confirm the post-run state write (M-2) is the single writer. (d) Pruned-session fallback "create fresh" — does the job's stored `SessionID` get rewritten to the new one (so the *next* run continues from the fallback) or stay pointing at the dead id (re-fallback every run)?
**Fix**: Specify `SessionID` write-back ownership/locking and the pruned-fallback rewrite policy (recommend: rewrite to the fresh session so continuity resumes).

---

## MINOR Findings

- **m-1 (Ambiguity)** — "short cleanup window" (FR-006, US3.1) has no value. SC-002 says "≤5s." State the constant and make it a named config or const.
- **m-2 (Incompleteness)** — Child-process cleanup (FR-011) says "tracked child/browser processes" but the spec doesn't reference the actual tracking mechanism (`pkg/sandbox/spawn_bg.go` exists). Cite how a run's children are *tracked* so cleanup can find them; otherwise "best-effort" is unimplementable.
- **m-3 (Ambiguity)** — `timeout_seconds` upper bound (Ambiguity #3) "cap at heartbeat ceiling 600s" — but heartbeat has no 600s ceiling in code; verify the source of 600s or pick a defined max.
- **m-4 (Inconsistency)** — `CronPayload.Command` (shell command scheduling) exists today and runs via `execTool`. The spec is silent on whether command-jobs get owner/session-mode/guardrails or remain a separate path. Define interaction (recommend: commands are deliver-style, no agent turn, but still owner-stamped for audit).
- **m-5 (Incompleteness)** — `deliver=true` with unreachable channel (Edge case) routes to "same failure path" → alert to "owner's default channel," which may be the *same* unreachable channel → loop. Tie to M-8.
- **m-6 (Ambiguity)** — "Activity event" for skips/runs: `ActivityEvent.type` is a closed enum; emitting skip/run events needs new enum values via contract. Not noted in the contract steps.
- **m-7 (Overcomplexity)** — Three session modes ×2 wake timings + retry + cap + cleanup is a lot for "the last v0.1.0 feature." `main` mode (C-2) is the heaviest and is only `SHOULD` (FR-019). Recommend descoping `main` to a fast-follow; ship `isolated`+`continue` solidly.

## OBSERVATIONS

- **O-1** — `SessionTypeScheduled` addition (FR-005) must be checked against session-history grouping AND any closed enum in the session contract (`AgentSession.yaml`); the regression test `TestSessionType_ExistingTypesUnaffected` should also assert the SPA grouping/zod schema accepts the new value.
- **O-2** — Consider whether `run-now` (FR-015) should bypass the schedule's `enabled=false` (paused) state. Unspecified.
- **O-3** — The spec reuses `CronJob` as the persisted entity and projects `Schedule` over it. Good (avoids a second store), but document the field mapping table (`Schedule` ↔ `CronJob`) so the projection is unambiguous for the contract author.
- **O-4** — `at`-job + `DeleteAfterRun` + run-history: a one-shot deletes itself after firing, so its run history vanishes. If operators expect to see "did my 3pm reminder fire?", deletion erases the evidence. Consider retaining a tombstone or moving one-shot history into the Activity feed.

---

## Structural Integrity Results (plan-spec mode)

| Check | Result |
|---|---|
| Every user story has acceptance scenarios | PASS |
| Every acceptance scenario has a BDD scenario | PASS |
| Every BDD scenario has `Traces to:` | PASS |
| Every BDD scenario has a TDD test | PASS |
| Every FR in traceability matrix | PASS (FR-001…FR-020 all mapped) |
| Every BDD scenario in matrix | PASS |
| Boundary/edge/error datasets present | PASS (3 dataset tables) |
| Regression impact addressed | PASS (6 regression tests named) |
| Success criteria measurable | MOSTLY — SC-002 "cleanup window ≤5s" relies on undefined constant (m-1); SC-006 "exactly one alert/Attention" depends on non-existent Attention (C-3) |
| **Referenced symbols exist** | **FAIL** — `pkg/cancel` (C-1), heartbeat enqueue API (C-2), Command Center Attention (C-3) do not exist; cron is sequential not lane-based (C-4) |

The traceability is clean, but it traces to **mechanisms that aren't in the code**. Structural form is good; structural *grounding* fails.

## Test Coverage Assessment

- **Determinism**: BLOCKED on M-10 — no clock seam in `pkg/cron`; backoff/timeout/overlap tests cannot avoid wall-clock without it. SC-010's "zero wall-clock sleeps" is currently impossible to satisfy.
- **Parallelism gap**: The cap test (`TestGuardrail_ConcurrencyCapQueues`) presupposes concurrent runs that the current sequential model can't produce (C-4). Missing: a test that two *different* jobs run concurrently.
- **Error-path coverage**: Good breadth (timeout, missing owner, ask-deny, transient retry, no-reply). Missing: alert-delivery-failure path (M-8), missed-`at`-job (M-9), legacy-ownerless-job firing (M-7), auto-deny loop-guard (M-5).
- **Negative auth**: No test for non-admin/cross-agent scheduling (M-4).

## STRIDE Threat Summary

| Component | Threats |
|---|---|
| `/api/v1/schedules` CRUD | **E**: no authz model — non-admin scheduling, cross-agent owner = privilege amplification (M-4). **R**: run history may be deleted with one-shot jobs, weakening audit (O-4). |
| Scheduled run lane | **E**: runs with owner's full `allow` tool policy unattended; `ask` auto-denied but `allow` auto-runs (M-4). **D**: hung/runaway run; cap+timeout mitigate but timeout collides with design (M-1, C-4). |
| Failure alerting | **D**: alert loops on flapping schedule / unreachable channel (M-8, m-5). **I**: alert content may echo error detail to a channel (consider redaction). |
| jobs.json | **T**: legacy ownerless jobs silently change behaviour to error-spam (M-7). |
| Auto-deny gate | **E/Silent-fail**: mis-wired approver could stall (wait for human) or fail-open (M-5). |

## Unasked Questions

1. Does a scheduled run inherit the owner agent's **sandbox/exec** permissions unattended, and is scheduling a `system.*`-allowed agent permitted at all?
2. Who may CRUD schedules — admin only, or any user? May a user schedule an agent they don't "own"?
3. Is the 300s timeout a **new** config key distinct from the deliberately-disabled `agents.defaults.timeout_seconds`, and what happens to a legitimately slow OpenRouter-queued run?
4. On shutdown, does an in-flight scheduled run get cancelled at the shutdown budget or granted its full timeout? How are *queued* runs handled?
5. What is the exact outbound channel resolution for alerts, and what stops alert loops on repeated failure / unreachable channel?
6. What happens to **every existing ownerless cron job** on first load after this ships — error-spam, or backfilled owner?
7. Does `main` mode survive v0.1.0 scope, given there is no per-agent main-session or heartbeat-enqueue mechanism today?
8. Is run history an array on the job, a separate file, or derived from sessions — and how deep (resolve Ambiguity #5 before contract authoring)?

---

**Verdict: BLOCK.** Resolve C-1…C-5 (non-existent mechanisms + sequential-vs-lane + shutdown drain) and M-1/M-4/M-6/M-7/M-10 (timeout-design conflict, authz, run-history contract shape, jobs.json migration, clock seam) before contract authoring or implementation. The contract-affecting items (C-3 Attention, M-6 run-history, m-6 Activity enum) must be settled first since hard-constraint #8 requires the schema before any code.
