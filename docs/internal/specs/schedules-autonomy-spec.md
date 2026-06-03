# Feature Specification: Scheduled Agent Autonomy & Schedules (#264)

**Created**: 2026-06-02
**Status**: Draft
**Input**: GitHub issue #264 (P0) — "cron → full agent autonomy + Schedules". Branch `feat/264-autonomous-schedules` off `hotfix/v0.1.0`. Design locked via interview + research into OpenClaw / Hermes / Claude Cowork autonomy models. This is the last v0.1.0 feature.

---

## Summary

Omnipus already ships a cron service (`pkg/cron`, jobs in `<workspace>/cron/jobs.json`) and a heartbeat service (`pkg/heartbeat`, `HEARTBEAT.md`), mirroring the OpenClaw/Hermes autonomy model — but the **cron → agent fire path is broken**: a fired job has no owner, runs under `context.Background()` (no deadline), routes to the **default** agent instead of the job's owner, and has no run traceability, guardrails, failure alerting, REST API, or UI.

This feature finishes that model: a scheduled job **wakes its owning agent** in a session whose mode the schedule chooses (`isolated` / `continue` / `main`), bounded by guardrails (timeout, concurrency cap, skip-if-running, headless auto-deny, transient retry, process cleanup), persists each run's outcome, alerts the owning agent's default channel on failure (+ Command Center Attention + toast), and exposes everything through a **contract-first `/api/v1/schedules` CRUD** and an **A+C Schedules UI** (global Command Center view + per-agent tab) built on the existing endless-feed card design system.

---

## Revision 2 — FINAL locked design + `/grill-spec` corrections

> This block is authoritative. Where any detail below it conflicts, this wins. It folds in the `/grill-spec` BLOCK findings (`schedules-autonomy-spec-review.md`) and the final interview decisions (2026-06-02).

**Agent↔session binding (resolves the dilemma).** Omnipus already namespaces per-agent context: the session key is `agent:<id>:session:<id>` (`pkg/agent/loop.go:3220`), so each agent keeps its own `context.jsonl` even inside a shared, multi-agent session. **Scheduled runs pin their OWNER** via the existing priority-1 "explicit `agent_id`" route (`loop.go:3118-3135`) and the owner-namespaced key — they never consult `ActiveAgentID`, so a human switching agents in a session cannot hijack a scheduled run, and there is **no default-agent fallback**. This mirrors OpenClaw's two independent axes: *agent binding* (which agent) × *session target* (which session).
- `isolated` → `agent:<owner>:session:<fresh scheduled id>` per run (new `SessionTypeScheduled`).
- `continue` → `agent:<owner>:session:<stable per-schedule id>` (builds on history; immune to human switching).
- `main` → `agent:<owner>:session:main` — a **reserved per-agent session id**; mode = "inject a system event + run now". The `wake=next-heartbeat` *timing* nicety is **deferred to a follow-up** (it needs a heartbeat enqueue API that does not exist yet); `main` ships as inject+run-now with no heartbeat dependency.

**Concurrency (C-4/C-5 fix).** Cron currently fires **sequentially** (`pkg/cron/service.go:209-212`). Build a **parallel autonomous lane**: a semaphore-bounded worker pool, **configurable `schedules.max_concurrent_runs` default 8**, with a bounded queue for excess due runs. Track lane goroutines so **shutdown drains/cancels them** (extend the #265 drain; `CronService.Stop()` must block on in-flight runs). On shutdown, in-flight scheduled runs are **cancelled** (shutdown budget ~70s wins over the 300s run timeout).

**Timeout (M-1 fix).** Do NOT reuse `agents.defaults.timeout_seconds` (deliberately `0`/disabled due to OpenRouter queue delays). Add a **separate `schedules.run_timeout_seconds` (default 300s)** + per-schedule override, applied **only** to scheduled runs.

**Abort primitive (C-1 fix).** "Session-ownership clear" does not exist. The real abort path is `pkg/agent/cancel.go::RequestCancel(CancelScope{SessionID})` + `SetSessionInterrupted`; the drain primitive is `WaitForActiveRequests()`. On timeout: `RequestCancel` the run's session, allow a short cleanup window, then proceed. FR-006/SC-002 are re-expressed in these terms.

**Run outcome typing (M-2 fix).** `cronTool.ExecuteJob` currently returns only a `string` and stringifies errors into `"Error: …"` successes (so `LastStatus` is always `"ok"`). It MUST return `(string, error)` so a failed/errored run is recorded as `error` and alerted.

**Clock seam (M-10 fix).** `pkg/cron` is hardwired to `time.Now()`. Introduce a `Clock` interface (injected) so tests fire jobs deterministically with **zero wall-clock sleeps** (protects the #265 flake class).

**Authz (M-4 fix).** **Any authenticated user** may create/edit/delete schedules; a schedule's **owner is restricted to agents the caller is permitted to use** (RBAC check at create/update). Scheduled runs execute the owner's full `allow` tool policy unattended; `ask` is auto-denied.

**Failure surface → dedicated Notifications store (C-3 fix, supersedes "Attention").** There is no "Command Center Attention". Build a **dedicated, contract-first Notifications entity** (persisted, per-user read state, retention) surfaced as a **header notification center**: a **bell** icon + unread badge (99+), a dropdown (~320px, tabs All/Unread, read/unread states, mark-as-read, empty state, click-through to the schedule/session). A run failure creates a notification AND publishes a channel alert to the **owning agent's default channel** AND emits an Activity event. Notifications are delivered live over a new AsyncAPI `notification` WS frame.

**jobs.json back-compat (M-6/M-7 fix).** On load, existing **owner-less** jobs are migrated by assigning `owner = current default agent id` (one-time, logged) so they keep working instead of hitting the owner-missing path and alert-spamming. New `CronJob`/`CronJobState` fields are optional/default-zero.

**Run history shape.** Keep the **last 20 run records inline** per schedule (`{ran_at, status, error, session_id, duration_ms}`); full history via the linked sessions. This is the contract's run-history array shape.

**Ambiguity dispositions:** #1 do NOT auto-pause (keep alerting) — but stop alert-spam via Notification coalescing; #2 `continue` sessions follow normal retention, prune→fresh fallback; #3 timeout max = configurable, no hard cap but warn above 600s; #4 `main`/heartbeat does NOT consume a lane slot; #5 retain last 20; #6 **coalesce** notifications per schedule (one item updated, not one per failure).

---

## Revision 3 — Wiring fixes (round-2 grill: REVISE → implementation-ready)

> Round-2 `/grill-spec` retired all 5 round-1 criticals but found the Rev-2 fixes don't connect into a working call path. These wiring decisions make them connect. This block is authoritative over Rev-2 where more specific.

**W-1 — Dedicated scheduled entry point (fixes J-1/J-2 owner-pinning + J-1 abort).** `ProcessDirectWithChannel` (`loop.go:2899-2920`) sets neither `agent_id` metadata nor `SessionID`, so it never hits the priority-1 explicit-agent route, the `agentSessionKey` namespacing collapses to `agent:<owner>:chat:<ch>:<chat>` (collides across isolated runs), and there is no cancellable turn. **Add a new `AgentLoop.ProcessScheduled(ctx, ownerAgentID, sessionID string, content, channel, chatID string) (string, error)`** that:
- requires a **concrete pre-created `sessionID`** (so `transcriptSessionID` is set, the turn registers under it via `GetActiveTurnHookForSession`, `RequestCancel(CancelScope{SessionID})` works, AND `agentSessionKey` yields the per-owner `agent:<owner>:session:<id>` key);
- routes to `ownerAgentID` **directly** (look up the agent, run the loop pinned to it) **without** going through the human priority-1 path at `loop.go:3128-3135` — it MUST NOT read/write/delete the `sessionActiveAgent` handoff map (a scheduled run must never disturb a human's in-flight agent switch in that session);
- returns `(string, error)` so a failed run is a failure, not a stringified-success.

**W-2 — Session id is created by the scheduler BEFORE the run (fixes J-1 abort + J-3 main).** Add `UnifiedStore.GetOrCreateScheduledSession(id string, ownerAgentID string) (*UnifiedMeta, error)` (get-or-create by deterministic id; `NewSession` today only mints fresh ULIDs). Session id per mode, all `SessionType=scheduled`, owner = `ActiveAgentID`:
- `isolated` → mint a fresh id each run (`NewSession(SessionTypeScheduled, …, owner)`).
- `continue` → a stable per-schedule id persisted on the `CronJob` (`SessionID` field); get-or-create.
- `main` → a **reserved deterministic id** `sched-main-<ownerAgentID>`; get-or-create. **This collapses `main` into "continue with a reserved shared id"** — no heartbeat/`processSystemMessage` dependency (which forces `GetDefaultAgent()` at `loop.go:3299` and could not run as the owner). The message is framed as a system-style note in the owner's reserved session. `wake=next-heartbeat` timing stays deferred.

**W-3 — Lane registers with the shutdown drain (fixes J-4).** Each scheduled run executes inside the same `activeRequests` accounting the agent loop uses (increment on lane dispatch, decrement on completion) so `WaitForActiveRequests()` covers it. `CronService.Stop()` must **cancel the lane context and block (bounded) on in-flight runs**; today it returns immediately (`service.go:111-124`) and is called before the drain (`shutdown.go:66,89`). Correct budget figure: **~65s** (`shutdown.go:71`), which wins over the 300s run timeout — on shutdown, in-flight scheduled runs are cancelled.

**W-4 — ExecuteJob/adapter (fixes m-1).** `JobHandler` is **already** `(string,error)` and `executeJobByID` already records errors; the defect is the gateway adapter swallowing the error (`gateway.go:1781`) + `ExecuteJob`'s `string`-only return. Make `ExecuteJob` return `(string,error)` and the adapter propagate it.

**W-5 — Clock seam is for `checkJobs`, not the timer (fixes m-2).** Inject `now func() time.Time` used by `checkJobs`/state math; expose an exported `RunDueJobs(now)` (or `Tick(now)`) that tests call **synchronously** to fire due jobs deterministically. Leave `runLoop`'s real `time.NewTimer`/`Reset` as production-only (do not fake timers) — tests bypass the loop and drive `RunDueJobs` directly. Zero wall-clock sleeps.

**W-6 — Authz uses the existing primitive (corrects M-4 framing).** "Owner restricted to agents the caller may use" = the existing `config.AuthorizeAgentAccess(user, agent)` (owner-OR-admin, `agent_ownership.go:34-49`). Call it at schedule create/update to validate the chosen owner. Not a new ACL.

**W-7 — Notification ownership (fills the headless gap).** Per-user identity exists (`User.Username` in REST auth; `wsConn.userID` at `websocket.go:149,517`). A failed scheduled run creates a notification for: (a) the schedule's **`created_by`** user (store it on the `CronJob`), and (b) the owning agent's **owner user** (`config` agent ownership `OwnerUsername`) if different. If neither resolvable, notify **all admins**. The live `notification` WS frame is filtered per-connection by `wsConn.userID` (reuse the #283 per-connection-interest pattern).

**W-8 — Migration nil-default (fixes M-5).** On load, backfill empty `owner` with the current default agent id **only if one exists**; if there is **no default agent**, leave `owner` empty, **skip firing** that job, and log a warning (do not alert-spam). The backfill is **persisted once** and is idempotent (only fills empties).

**Net new primitives introduced by this revision (small, bounded):** `ProcessScheduled`, `GetOrCreateScheduledSession`, the cron `Clock`/`RunDueJobs` seam, `CronService.Stop()` blocking drain, and the Notifications entity + `notification` WS frame. Everything else reuses existing code.

---

## Available Reference Patterns

> No `docs/reference/go-implementation/` library applies. The authoritative external reference is the **OpenClaw automation model** (cron session modes, heartbeat, guardrails), which Omnipus already partially mirrors. Internal reference patterns reused:

| Reference | Pattern | Relevance |
|---|---|---|
| `pkg/heartbeat/service.go` | Periodic agent-turn invocation, due-task batching, `HEARTBEAT_OK` ack, publish-with-timeout | The `main`/heartbeat-wake session mode hooks here; the fire path mirrors how heartbeat invokes the agent and publishes. |
| `pkg/cancel` + `pkg/agent/loop.go` cancel paths | Session-ownership clear on cancel (used by #265 shutdown drain) | Timeout abort must force-clear the run's session ownership so queued chat work isn't stranded. |
| `ChannelConfigPanel.tsx` / `channels.tsx` | `space-y-2` endless-feed card list + `Sheet` slide-over + `useState` form (no form lib) | The Schedules feed, detail sheet, and create/edit form copy this exact pattern (per UI design rules — NO tables). |
| #283 contract-first frame work (just merged) | `contracts/components/schemas/*.yaml` → `gen-contracts.sh` → generated Go+TS+zod | The `/schedules` REST schemas and any run/attention WS frames follow the same 5-step contract process. |

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|---|---|---|
| `cron.CronJob` (`pkg/cron/service.go:42-52`) | **modify** | Add `AgentID`, `SessionMode`, `TimeoutSeconds`, `SessionID` (for `continue`). Currently no owner. |
| `cron.CronJobState` (`pkg/cron/service.go:35-40`) | **modify** | Add run history / counters: `LastSessionID`, `ConsecutiveFailures`, `RunningSince` (overlap guard). Keep `NextRun/LastRun/LastStatus/LastError`. |
| `cron.JobHandler` (`pkg/cron/service.go:59`) | **modify/extend** | `func(job *CronJob)(string,error)` → carries a context with deadline; or the service owns the deadline + concurrency lane. |
| `cron.Service` runLoop/checkJobs/executeJobByID (`pkg/cron/service.go:84-336`) | **extend** | Add concurrency cap, skip-if-running (`RunningSince`), retry/backoff scheduling. |
| `tools.cronTool.ExecuteJob` (`pkg/tools/cron.go:305-388`) | **modify** | Wake **owner** in the chosen session mode; capture response; apply deliver semantics; auto-deny `ask`. |
| `tools.cronTool` schema (`pkg/tools/cron.go:81-125`) | **extend** | Add `session_mode`, `owner` (defaults to calling agent), `timeout_seconds`; keep `deliver`. |
| `gateway` cron wiring (`pkg/gateway/gateway.go:~1779`) | **modify** | Replace `context.Background()`; wire owner-aware fire path + guardrail lane + alert hook. |
| `agent.AgentLoop.ProcessDirectWithChannel` (`pkg/agent/loop.go:3118-3198`) | **extend** | New owner-targeted entry (e.g. `ProcessScheduled(ctx, ownerAgentID, sessionMode, ...)`) that does NOT fall back to default agent and runs under a non-interactive (auto-deny `ask`) policy. |
| `agent.resolveMessageRoute` (`pkg/agent/loop.go:~3118`) | **extend** | Honor an explicit `agent_id`/owner override so cron routes to the owner. |
| `session.UnifiedStore.NewSession` + `SessionType` (`pkg/session/unified.go:23-29,189-234`) | **modify** | Add `SessionTypeScheduled`; fired isolated/continue runs use it; add `TriggeredBy` provenance. |
| `routing.ResolveRoute` (`pkg/routing/route.go`) | **read** | Owner override bypasses this; default resolution unchanged for human traffic. |
| `heartbeat.Service` (`pkg/heartbeat/service.go`) | **extend** | `main` mode wakes/queues into the agent's main session / heartbeat lane (`wake now` / `wake next-heartbeat`). |
| `bus.MessageBus.PublishOutbound` (`pkg/bus`) | **call** | Failure alert routed to the owning agent's default channel. |
| `gateway` REST (`pkg/gateway/rest.go`) | **extend** | New `/api/v1/schedules` handlers (contract-first). |
| Command Center "Attention" + Activity feed | **extend** | Failure raises an Attention item + Activity event; run badged `scheduled` in history. |

### Impact Assessment

| Symbol Modified | Risk | d=1 Dependents | d=2 Dependents |
|---|---|---|---|
| `cron.CronJob` / `CronJobState` (struct + JSON) | **MEDIUM** | `pkg/cron` service, `pkg/tools/cron.go`, jobs.json on disk (back-compat: new fields optional, default-zero) | Any cron tool callers; gateway wiring |
| `agent.ProcessDirectWithChannel` / new `ProcessScheduled` | **HIGH** | `pkg/tools/cron.go`, heartbeat, gateway | All agent-loop message paths — must not regress human chat routing |
| `session.SessionType` enum + `NewSession` | **MEDIUM** | session store, history UI, `pkg/gateway` session list | SPA session history grouping |
| `gateway.go` cron wiring | **MEDIUM** | boot path | autonomous run lane lifecycle on shutdown (must drain like #265) |

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| Cron tick → `checkJobs` → `executeJobByID` → `onJob` → agent | The core fire path being redesigned. |
| Heartbeat tick → due-task batch → agent turn → publish | `main` session mode integrates here; the fire path mirrors its publish step. |
| Inbound message → `resolveMessageRoute` → agent turn → `PublishOutbound` | Owner override is inserted at route resolution; response capture mirrors this. |
| Shutdown drain (#265) | The autonomous-run lane must drain/abort cleanly on shutdown without stranding sessions. |

### Cluster Placement

Backend **autonomy/scheduling** cluster (`pkg/cron`, `pkg/agent`, `pkg/heartbeat`, `pkg/session`) + gateway REST/contract + SPA. Spans backend + frontend; reuses existing session model (NOT the v0.3 Rooms topology).

---

## User Stories & Acceptance Criteria

### User Story 1 — Scheduled job wakes its OWNING agent (Priority: P0)

An operator schedules a recurring instruction for a specific agent ("Mia: summarize today's PRs at 18:00"). When it fires, **Mia** must run it — not whatever agent happens to be the default. Today the fire path drops the owner and routes to the default agent, so the wrong agent (or none) responds.

**Why this priority**: This is the core defect #264 exists to fix; every other story depends on the owner-aware fire path.

**Independent Test**: Create a job owned by a non-default agent, fire it synchronously with a mock LLM, assert the owning agent's loop handled it and the default agent did not.

**Acceptance Scenarios**:
1. **Given** a cron job with `agent_id` = a non-default agent, **When** it fires, **Then** the owning agent processes the turn and the default agent is not invoked.
2. **Given** a cron job whose `agent_id` references a deleted/disabled agent, **When** it fires, **Then** the run is recorded as an error (owner-missing) and no default-agent fallback occurs.
3. **Given** a job created by the cron tool with no explicit owner, **When** it is created, **Then** the owner defaults to the agent that created it.

### User Story 2 — Per-schedule session mode (Priority: P0)

Different schedules need different memory behavior. A daily report should run in a **fresh** session (clean history, cheap). A "daily standup that builds on yesterday" needs a **persistent** session. A reminder/system nudge should land in the agent's **main** session / wake the heartbeat. The schedule picks one of `isolated` (default) / `continue` / `main`.

**Why this priority**: The user explicitly chose 3 modes; the fire path and session creation differ per mode, so it's foundational, not additive.

**Independent Test**: Fire the same instruction three times under each mode; assert isolated creates a new `scheduled` session each run, continue reuses one session id across runs, main injects into the agent's main session.

**Acceptance Scenarios**:
1. **Given** a schedule with `session_mode=isolated`, **When** it fires twice, **Then** two distinct `scheduled`-type sessions are created, each carrying safe prefs (model/thinking) but not channel routing/elevation.
2. **Given** a schedule with `session_mode=continue`, **When** it fires twice, **Then** both runs use the same persistent session id and the second run can see the first run's transcript.
3. **Given** a schedule with `session_mode=main` and `wake=now`, **When** it fires, **Then** a system event is enqueued into the owning agent's main session immediately; with `wake=next-heartbeat` it defers to the next heartbeat tick.

### User Story 3 — Guardrails bound autonomous runs (Priority: P0)

A scheduled run has no human watching. It must be bounded: time out at 300s (configurable), never pile up (skip if the previous run of the same job is still going), respect a global concurrency cap (8), auto-deny any `ask`-gated tool (no one to approve), retry transient provider errors with backoff, and clean up child/browser processes.

**Why this priority**: Without guardrails, a hung or runaway autonomous run can strand sessions, exhaust resources, or stall forever on an approval prompt — unacceptable for an always-on server.

**Independent Test**: With a mock LLM that hangs/errs/asks, assert: deadline abort + session-ownership clear; second concurrent fire of the same job is skipped; the 6th concurrent run queues behind the cap; an `ask` tool call is auto-denied; a transient error retries on the backoff schedule.

**Acceptance Scenarios**:
1. **Given** a run exceeding its `timeout_seconds`, **When** the deadline passes, **Then** the agent run is aborted, a short cleanup window is allowed, and the run's session ownership is force-cleared.
2. **Given** a job whose previous run is still in progress, **When** the schedule fires again, **Then** the new fire is skipped (logged + Activity event), not run concurrently.
3. **Given** 5 autonomous runs already executing, **When** a 6th becomes due, **Then** it waits for a free slot rather than running immediately.
4. **Given** a scheduled run, **When** the agent calls a tool whose policy is `ask`, **Then** the call is auto-denied (run does not stall).
5. **Given** a run that hits a transient provider error (rate-limit/overload/network/5xx), **When** it fails, **Then** it is retried up to 3 times with backoff `[60s,120s,300s]`; the counter resets after a success.
6. **Given** a run that spawned a browser/child process, **When** the run completes (success, error, or timeout), **Then** tracked child processes for that run are best-effort cleaned up.

### User Story 4 — Run outcome persisted + failure alerting (Priority: P0)

Operators need to know a schedule ran and whether it failed. Each run records last-run time, status, error, and the linked session id. On failure (including a model/provider failure with no reply payload), an alert message is routed to the **owning agent's default channel**, and a Command Center **Attention** item + toast are raised.

**Why this priority**: Silent autonomous failure is the worst failure mode; alerting is a locked requirement.

**Independent Test**: Force a run failure with a mock LLM; assert `CronJobState` records `error` + the failure reason, an outbound alert is published to the owner's default channel, and an Attention item is created.

**Acceptance Scenarios**:
1. **Given** a run that fails, **When** it completes, **Then** `CronJobState.LastStatus="error"`, `LastError` is set, and `ConsecutiveFailures` increments.
2. **Given** a failed run for an agent bound to a channel, **When** the failure is recorded, **Then** an alert message is published via the message bus to that agent's default channel.
3. **Given** a failed run, **When** the failure is recorded, **Then** a Command Center Attention item + an Activity-feed event are created.
4. **Given** a run that returns no reply payload but the agent run errored, **When** it completes, **Then** it is treated as a failure (not a silent success).

### User Story 5 — `/api/v1/schedules` CRUD (Priority: P0)

The SPA (and operators via API) manage schedules over a contract-first REST surface: list, create, get, update, delete, plus run-now and pause/resume.

**Why this priority**: The UI cannot exist without the contract + endpoints; contract-first is hard-constraint #8.

**Independent Test**: Against the gateway, exercise each endpoint and assert the generated `Schedule` wire type round-trips and persists to `cron/jobs.json`.

**Acceptance Scenarios**:
1. **Given** an authenticated admin, **When** they POST a valid schedule, **Then** a `CronJob` is persisted with an owner and the created `Schedule` is returned (201, `Content-Type: application/json`).
2. **Given** an existing schedule, **When** they PUT changes (name/trigger/message/session_mode/timeout/enabled), **Then** the job is updated and the next run recomputed.
3. **Given** an existing schedule, **When** they POST `{id}/run`, **Then** it fires immediately (respecting guardrails) and a run is recorded.
4. **Given** an existing schedule, **When** they POST `{id}/pause`, **Then** `enabled` toggles and the scheduler stops/starts firing it.
5. **Given** a schedule id that does not exist, **When** any single-resource endpoint is called, **Then** it returns 404 with a JSON error.

### User Story 6 — Schedules UI (A+C) (Priority: P1)

Operators see and manage schedules in two places sharing one data source: a **global Schedules view** in Command Center (with a Tasks|Schedules toggle) and a **per-agent Schedules tab** on the agent profile. Both are endless-feed **card** lists (no tables); detail and create/edit are `Sheet` slide-overs. Scheduled runs are badged `scheduled` in history.

**Why this priority**: P1 (UI) — backend (US1–5) must land first; the feature is operator-facing and needed for the v0.1 demo.

**Independent Test**: Render the Schedules feed from mock data; create/edit/delete/run-now/pause via the Sheet form; assert cards, badges, and toasts.

**Acceptance Scenarios**:
1. **Given** schedules exist, **When** the Command Center Schedules view loads, **Then** they render as a `space-y-2` card feed with status `Badge` + `Circle` dot, owner, trigger summary, next-run, and last-status.
2. **Given** the agent profile, **When** the Schedules tab is opened, **Then** the same feed appears filtered to that agent.
3. **Given** the create form (Sheet), **When** the operator fills Owner/Name/Trigger/Message/Session-mode/Delivery/Timeout/Enabled and saves, **Then** a toast confirms and the feed refreshes.
4. **Given** a schedule card, **When** Run-now is clicked, **Then** the run fires and the run history in the detail Sheet updates with a link to the run's session.
5. **Given** a scheduled run produced a session, **When** session history is viewed, **Then** that session shows a `scheduled` badge.

---

## Behavioral Contract

Primary flows:
- When a due job fires, the system runs it as the **owning** agent under a deadline-bounded context in the schedule's session mode.
- When `deliver=false`, the owning agent processes the message (autonomy); when `deliver=true`, the message is sent straight to the configured channel with no agent turn.
- When a run finishes, the system persists last-run/status/error/linked-session and emits an Activity event.
- When the operator manages schedules via `/api/v1/schedules`, the system creates/reads/updates/deletes the underlying `CronJob` and recomputes next-run.

Error flows:
- When a run exceeds its timeout, the system aborts the agent run, allows a short cleanup window, and force-clears the run's session ownership.
- When a run fails, the system records the error, alerts the owning agent's default channel, and raises a Command Center Attention item + toast.
- When a transient provider error occurs, the system retries up to 3× on backoff `[60s,120s,300s]` and resets the counter after a success.
- When the owning agent is missing/disabled, the system records an owner-missing error and does NOT fall back to the default agent.

Boundary conditions:
- When a job's previous run is still in progress, the system skips the new fire.
- When the global concurrency cap (8) is reached, the system queues additional due runs.
- When a scheduled run calls an `ask`-gated tool, the system auto-denies it.
- When `session_mode=main` with `wake=next-heartbeat`, the system defers the event to the next heartbeat tick.

---

## Edge Cases

- **Owner deleted between create and fire** → run recorded as owner-missing error + alert; job optionally auto-paused after N consecutive owner-missing errors (see Ambiguity #1).
- **`continue` session was deleted/retention-pruned** → fall back to creating a fresh `scheduled` session for that run and log it (don't crash).
- **`main` wake when the agent has no main session yet** → create the agent's main session, then enqueue the event.
- **`deliver=true` with a channel the agent can't reach / not configured** → record delivery error + alert (same failure path).
- **Clock skew / job due far in the past on boot** (server was down) → fire once on next tick, not once per missed interval (no thundering herd); recompute next-run forward.
- **Timeout fires but the agent run ignores cancellation** → after the cleanup window, force-clear session ownership regardless (mirrors #265 shutdown).
- **Retry exhausts all 3 attempts** → final failure recorded + alert; backoff state cleared.
- **Concurrency cap reached AND a one-shot (`at`) job becomes due** → it queues; it must not be dropped or double-fired.
- **Run-now while a scheduled run of the same job is active** → skipped by the overlap guard (same as a natural fire), with a clear UI toast.
- **Invalid cron expression / negative timeout on create** → 400 with a JSON validation error; nothing persisted.

---

## Explicit Non-Behaviors

- The system must **not** fall back to the default agent when a cron job's owner is unresolved, because that silently runs the wrong agent (the core #264 bug).
- The system must **not** run a scheduled turn under `context.Background()` (no deadline), because a hung run would block the lane and strand sessions.
- The system must **not** stall a scheduled run on an `ask`-gated tool waiting for human approval, because no human is present; it auto-denies instead.
- The system must **not** merge schedules into `pkg/taskstore` or the Task model, because that collides with the v0.3 Rooms tasks redesign (#156).
- The system must **not** introduce a new sandbox/room topology or background-session model; it reuses the existing `UnifiedStore` session model.
- The system must **not** add web-push or any new notification transport; alerting reuses the message bus + Command Center Attention (web-push is v0.3).
- The system must **not** fire a missed schedule once per missed interval after downtime; it fires once and recomputes forward.
- The system must **not** persist any hand-written wire type; all `/schedules` types are generated from the contract (hard-constraint #8).
- The system must **not** add wall-clock `sleep`s to tests; firing is driven by an injected clock (avoid re-introducing the #265 flake class).

---

## Integration Boundaries

### LLM provider (via agent loop)
- **Data in**: the schedule's message + owning agent's prompt/context (per session mode).
- **Data out**: the agent's reply (captured), routed per `deliver`.
- **Contract**: existing agent-loop provider call; tool-use required.
- **On failure**: transient errors retried on backoff `[60s,120s,300s]` ×3; terminal failure → alert.
- **Development**: **mock LLM** (deterministic, can be told to hang / error / call an `ask` tool). No live provider in tests.

### Message bus / channels (`pkg/bus`)
- **Data in**: `OutboundMessage{channel, chatID, content}` for alerts and `deliver=true` sends.
- **Data out**: delivered to the channel manager.
- **Contract**: `PublishOutbound`.
- **On failure**: delivery error recorded + alert (no crash).
- **Development**: in-memory bus with a capturing subscriber.

### Cron store (`<workspace>/cron/jobs.json`)
- **Data in/out**: `CronStore{Version, Jobs[]}` with new fields (owner, session_mode, timeout, run state). Atomic temp+rename.
- **Contract**: JSON on disk; new fields optional/default-zero for back-compat.
- **On failure**: load error aborts cron start (logged); write error logged, in-memory state retained.
- **Development**: temp-dir store.

### Heartbeat service (`pkg/heartbeat`)
- **Data in**: a `main`-mode wake event (now / next-heartbeat).
- **Contract**: internal Go call (enqueue system event / wake).
- **On failure**: logged; falls back to immediate enqueue.
- **Development**: real heartbeat service with injected clock.

### Gateway/SPA (contract-first)
- **Data in/out**: `Schedule`, `ScheduleCreate`, `ScheduleRun`, `ScheduleList` wire types over `/api/v1/schedules`; optional WS `schedule_run`/`attention` frames.
- **Contract**: `contracts/openapi.yaml` + `contracts/asyncapi.yaml` → generated types.
- **On failure**: standard REST JSON errors; SPA zod-validates frames.
- **Development**: generated types + `make verify-contracts`.

---

## BDD Scenarios

### Feature: Owner-aware scheduled fire

#### Scenario: Scheduled job runs as its owning agent, not the default
**Traces to**: US1, AS1
**Category**: Happy Path
- **Given** an enabled cron job owned by agent "mia" and a different agent "max" is the default
- **And** a deterministic mock LLM
- **When** the job becomes due and fires
- **Then** the agent loop processes the turn as "mia"
- **And** "max" is never invoked for this run

#### Scenario: Missing owner does not fall back to the default agent
**Traces to**: US1, AS2
**Category**: Error Path
- **Given** an enabled cron job whose `agent_id` references a deleted agent
- **When** the job fires
- **Then** the run is recorded with status "error" and reason "owner unavailable"
- **But** the default agent is not invoked

#### Scenario: Cron tool defaults the owner to the creating agent
**Traces to**: US1, AS3
**Category**: Happy Path
- **Given** agent "mia" calls the cron tool to create a job with no explicit owner
- **When** the job is created
- **Then** the persisted job's `agent_id` equals "mia"

### Feature: Session modes

#### Scenario Outline: Session mode determines per-run session behavior
**Traces to**: US2, AS1/AS2/AS3
**Category**: Happy Path
- **Given** a schedule with `session_mode=<mode>` owned by "mia"
- **When** it fires twice
- **Then** the session usage is `<result>`

**Examples**:
| mode | result |
|---|---|
| isolated | two distinct `scheduled`-type sessions, no channel routing/elevation carried |
| continue | one persistent session reused across both runs; run 2 sees run 1 transcript |
| main | a system event enqueued into "mia"'s main session each run |

#### Scenario: Main mode with next-heartbeat defers the event
**Traces to**: US2, AS3
**Category**: Alternate Path
- **Given** a schedule with `session_mode=main` and `wake=next-heartbeat`
- **When** it fires
- **Then** no immediate turn runs
- **And** the system event is delivered on the next heartbeat tick

#### Scenario: Continue-mode session was pruned
**Traces to**: US2 (Edge)
**Category**: Edge Case
- **Given** a `continue` schedule whose persistent session no longer exists
- **When** it fires
- **Then** a fresh `scheduled` session is created for that run and a warning is logged
- **But** the run does not crash

### Feature: Guardrails

#### Scenario: Run exceeding timeout is aborted and session ownership cleared
**Traces to**: US3, AS1
**Category**: Error Path
- **Given** a schedule with `timeout_seconds=1` and a mock LLM that never returns
- **When** the run exceeds the deadline
- **Then** the agent run is aborted within a short cleanup window
- **And** the run's session ownership is force-cleared

#### Scenario: Overlapping fire of the same job is skipped
**Traces to**: US3, AS2
**Category**: Edge Case
- **Given** a job whose previous run is still in progress
- **When** the schedule fires again
- **Then** the second fire is skipped and an Activity event records the skip
- **But** no second concurrent run starts

#### Scenario: Global concurrency cap queues excess runs
**Traces to**: US3, AS3
**Category**: Edge Case
- **Given** the global autonomous concurrency cap is 5 and 5 runs are executing
- **When** a 6th run becomes due
- **Then** it waits for a free slot before starting

#### Scenario: Ask-gated tool is auto-denied in a scheduled run
**Traces to**: US3, AS4
**Category**: Error Path
- **Given** a scheduled run whose agent calls a tool with policy `ask`
- **When** the tool call is evaluated
- **Then** it is auto-denied and the run continues without stalling

#### Scenario Outline: Transient provider error retries on backoff
**Traces to**: US3, AS5
**Category**: Error Path
- **Given** a scheduled run that fails with a `<error>` provider error
- **When** the run fails
- **Then** it is retried after `<delay_ms>` (attempt `<attempt>`)

**Examples**:
| error | attempt | delay_ms |
|---|---|---|
| rate_limit | 1 | 60000 |
| overloaded | 2 | 120000 |
| network | 3 | 300000 |

#### Scenario: Child processes cleaned up after a run
**Traces to**: US3, AS6
**Category**: Edge Case
- **Given** a scheduled run that spawned a tracked browser/child process
- **When** the run completes (success, error, or timeout)
- **Then** tracked child processes for that run are best-effort terminated

### Feature: Run outcome + alerting

#### Scenario: Failed run records error and alerts the owner's channel
**Traces to**: US4, AS1/AS2
**Category**: Error Path
- **Given** an enabled schedule owned by an agent bound to channel "telegram"
- **And** a mock LLM that errors terminally
- **When** the run fails
- **Then** `CronJobState.LastStatus="error"` and `LastError` is set
- **And** an alert message is published to the owner's "telegram" channel

#### Scenario: Failure raises a Command Center Attention item
**Traces to**: US4, AS3
**Category**: Error Path
- **Given** a failing scheduled run
- **When** the failure is recorded
- **Then** a Command Center Attention item and an Activity-feed event are created

#### Scenario: Errored run with no reply payload counts as failure
**Traces to**: US4, AS4
**Category**: Error Path
- **Given** a scheduled run where the agent run errors and produces no reply
- **When** it completes
- **Then** it is recorded as a failure (not a silent success) and alerted

### Feature: /api/v1/schedules CRUD

#### Scenario: Create schedule returns 201 JSON and persists with owner
**Traces to**: US5, AS1
**Category**: Happy Path
- **Given** an authenticated admin
- **When** they POST a valid `ScheduleCreate`
- **Then** the response is 201 with `Content-Type: application/json` and a `Schedule` body
- **And** the underlying `CronJob` is persisted with the given owner

#### Scenario: Run-now fires immediately and records a run
**Traces to**: US5, AS3
**Category**: Happy Path
- **Given** an existing schedule
- **When** the admin POSTs `{id}/run`
- **Then** the job fires (respecting overlap/concurrency) and a run is recorded

#### Scenario: Pause toggles enabled and stops firing
**Traces to**: US5, AS4
**Category**: Alternate Path
- **Given** an enabled schedule
- **When** the admin POSTs `{id}/pause`
- **Then** `enabled` becomes false and the scheduler no longer fires it

#### Scenario: Unknown schedule id returns 404 JSON
**Traces to**: US5, AS5
**Category**: Error Path
- **Given** no schedule with id "nope"
- **When** the admin GETs `/api/v1/schedules/nope`
- **Then** the response is 404 with a JSON error body

#### Scenario: Invalid trigger is rejected
**Traces to**: US5 (Edge)
**Category**: Error Path
- **Given** an authenticated admin
- **When** they POST a schedule with an invalid cron expression
- **Then** the response is 400 with a JSON validation error and nothing is persisted

### Feature: Schedules UI (A+C)

#### Scenario: Command Center Schedules feed renders as cards
**Traces to**: US6, AS1
**Category**: Happy Path
- **Given** schedules exist
- **When** the Command Center Schedules view loads
- **Then** they render as a `space-y-2` card feed with status badge + dot, owner, trigger summary, next-run, last-status
- **But** no table is rendered

#### Scenario: Per-agent Schedules tab filters to the agent
**Traces to**: US6, AS2
**Category**: Happy Path
- **Given** schedules for agents "mia" and "max"
- **When** the Schedules tab on "mia"'s profile loads
- **Then** only "mia"'s schedules appear

#### Scenario: Create via Sheet form shows a toast and refreshes
**Traces to**: US6, AS3
**Category**: Happy Path
- **Given** the create Sheet is open
- **When** the operator fills the fields and saves
- **Then** a success toast appears and the feed refreshes with the new card

#### Scenario: Run-now from a card updates run history
**Traces to**: US6, AS4
**Category**: Happy Path
- **Given** a schedule card
- **When** Run-now is clicked
- **Then** the detail Sheet's run history adds the run with a link to its session

#### Scenario: Scheduled session shows a 'scheduled' badge in history
**Traces to**: US6, AS5
**Category**: Happy Path
- **Given** a scheduled run created a session
- **When** session history is viewed
- **Then** that session shows a `scheduled` badge

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | cron scheduling math, guardrail predicates, session-mode selection, owner resolution, retry/backoff, alert builder, contract validation | Logic in isolation, deterministic clock |
| Integration | cron→agent fire path with mock LLM + in-memory bus + temp store; `/api/v1/schedules` handlers against a real mux | Components together, no wall-clock |
| E2E | SPA Schedules feed/detail/form + Playwright create→run-now→scheduled-badge | Full operator flow on the embedded SPA |

### Test Implementation Order

Write tests BEFORE implementation. Unit → Integration → E2E.

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `TestSchedule_OwnerDefaultsToCreatingAgent` | Unit | Cron tool defaults owner | Owner falls back to caller |
| 2 | `TestFire_RoutesToOwnerNotDefault` | Integration | Runs as owning agent | Owner-aware fire (mock LLM) |
| 3 | `TestFire_MissingOwner_NoDefaultFallback` | Integration | Missing owner error | No default fallback; error recorded |
| 4 | `TestSessionMode_IsolatedCreatesFreshScheduledSession` | Integration | Session mode outline (isolated) | New `scheduled` session/run |
| 5 | `TestSessionMode_ContinueReusesSession` | Integration | Session mode outline (continue) | Same session id across runs |
| 6 | `TestSessionMode_MainEnqueuesIntoMainSession` | Integration | Session mode outline (main) | Heartbeat/main enqueue |
| 7 | `TestSessionMode_MainNextHeartbeatDefers` | Integration | Main next-heartbeat defers | Deferred wake |
| 8 | `TestSessionMode_ContinuePrunedFallsBackFresh` | Integration | Continue session pruned | Graceful fresh fallback |
| 9 | `TestGuardrail_TimeoutAbortsAndClearsOwnership` | Integration | Timeout abort | Deadline + ownership clear |
| 10 | `TestGuardrail_SkipIfRunning` | Unit/Integration | Overlapping fire skipped | Overlap guard |
| 11 | `TestGuardrail_ConcurrencyCapQueues` | Integration | Concurrency cap | 6th run queues |
| 12 | `TestGuardrail_AskToolAutoDenied` | Integration | Ask auto-denied | Non-interactive policy |
| 13 | `TestGuardrail_TransientRetryBackoff` | Unit | Retry backoff outline | `[60s,120s,300s]` ×3, reset |
| 14 | `TestGuardrail_ChildProcessCleanup` | Integration | Child cleanup | Best-effort kill |
| 15 | `TestRun_FailureRecordsErrorAndAlertsOwnerChannel` | Integration | Failed run alert | State + bus alert |
| 16 | `TestRun_FailureRaisesAttention` | Integration | Attention item | Attention + Activity |
| 17 | `TestRun_ErroredNoReplyIsFailure` | Integration | Errored no-reply | Not silent success |
| 18 | `TestSchedulesAPI_CreateReturns201JSONWithOwner` | Integration | Create 201 | Contract + persist |
| 19 | `TestSchedulesAPI_RunNow` | Integration | Run-now | Fires + records |
| 20 | `TestSchedulesAPI_Pause` | Integration | Pause toggles | Enable/disable |
| 21 | `TestSchedulesAPI_NotFound404` | Integration | Unknown id 404 | JSON error |
| 22 | `TestSchedulesAPI_InvalidTrigger400` | Integration | Invalid trigger | Validation reject |
| 23 | `TestContract_ScheduleStatusEnumMatches` | Unit | (contract) | Go consts == contract enum (mirrors #283 drift guard) |
| 24 | `schedules.feed.test.tsx` | Unit (vitest) | CC feed renders cards | Card feed, badges, no table |
| 25 | `schedules.form.test.tsx` | Unit (vitest) | Create via Sheet | Form + toast |
| 26 | `agentProfile.schedulesTab.test.tsx` | Unit (vitest) | Per-agent tab filter | Filtered feed |
| 27 | `e2e/schedules.spec.ts` | E2E | Run-now + scheduled badge | Playwright on embedded SPA |

### Test Datasets

#### Dataset: Session mode → per-run session behavior
| # | Input (mode, runs) | Boundary Type | Expected | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | isolated, 2 | happy | 2 distinct scheduled sessions | Session mode outline | default |
| 2 | continue, 2 | happy | 1 reused session | Session mode outline | builds on history |
| 3 | main, 2 (wake=now) | happy | 2 main-session enqueues | Session mode outline | |
| 4 | main, 1 (wake=next-heartbeat) | alt | deferred to next tick | Main defers | |
| 5 | continue, session pruned | edge | fresh fallback + warn | Continue pruned | no crash |

#### Dataset: Guardrail predicates
| # | Input | Boundary Type | Expected | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | timeout=1s, LLM hangs | error | abort + ownership clear | Timeout abort | |
| 2 | prev run active | edge | skip | Overlap skip | |
| 3 | 8 active + 1 due | max | queue | Concurrency cap | cap=8 |
| 4 | 0 active + 1 due | min | run immediately | Concurrency cap | |
| 5 | ask tool call | error | auto-deny | Ask auto-denied | |
| 6 | rate_limit×1 | error | retry @60s | Retry outline | |
| 7 | network×3 then ok | error→happy | retry @300s, counter reset | Retry outline | |
| 8 | terminal error | error | no retry, alert | Failed run alert | |

#### Dataset: /schedules API
| # | Input | Boundary Type | Expected | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | valid ScheduleCreate | happy | 201 JSON + persisted | Create 201 | owner set |
| 2 | missing required field | error | 400 JSON | Invalid trigger | |
| 3 | invalid cron expr | error | 400 JSON | Invalid trigger | |
| 4 | negative timeout | error | 400 JSON | (edge) | |
| 5 | unknown id GET | error | 404 JSON | Not found | |
| 6 | {id}/run existing | happy | run recorded | Run-now | |
| 7 | {id}/pause existing | alt | enabled=false | Pause | |

### Regression Test Requirements

**Modifying existing functionality** (cron + agent loop + session + heartbeat):

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Human chat routing → correct agent/default | existing loop/routing tests | Yes — `TestRoute_HumanTrafficUnaffectedByOwnerOverride` | Owner override must not change human routing |
| `deliver=true` direct-to-channel send | existing cron tool tests (if any) | Yes — `TestDeliverTrue_SendsDirectNoAgentTurn` | Preserve direct send |
| Heartbeat normal tick (no schedules) | existing heartbeat tests | Yes — `TestHeartbeat_UnaffectedWhenNoMainModeSchedules` | main-mode must not perturb normal heartbeat |
| Session list/history for chat/task/channel | existing session tests | Yes — `TestSessionType_ExistingTypesUnaffected` | Adding `scheduled` must not break grouping |
| jobs.json load of pre-existing jobs (no new fields) | existing cron load test | Yes — `TestCronLoad_BackCompatDefaultsOwnerEmpty` | Old jobs load with zero-value new fields |
| Shutdown drain (#265) | integration drain test | Yes — `TestShutdown_DrainsAutonomousLane` | Lane must drain/abort cleanly |

---

## Functional Requirements

- **FR-001**: System MUST add an owning `AgentID` to `CronJob` and route a fired job to that agent, never falling back to the default agent.
- **FR-002**: System MUST default a cron-tool-created job's owner to the creating agent when no owner is specified.
- **FR-003**: System MUST run every scheduled agent turn under a context with a deadline derived from the schedule's `timeout_seconds` (default 300s, configurable; global default configurable).
- **FR-004**: System MUST support three session modes per schedule — `isolated` (default), `continue`, `main` — and create/continue/enqueue the session accordingly.
- **FR-005**: System MUST add `SessionTypeScheduled` and create isolated/continue runs as that type, recording `TriggeredBy` provenance.
- **FR-006**: System MUST, on timeout, abort the agent run, allow a short cleanup window, then force-clear the run's session ownership.
- **FR-007**: System MUST enforce a global concurrency cap (default 8, configurable) on a dedicated autonomous-run lane and queue excess due runs.
- **FR-008**: System MUST skip a fire when the same job's previous run is still in progress, recording the skip.
- **FR-009**: System MUST auto-deny `ask`-gated tool calls during a scheduled run (non-interactive policy), never stalling on approval.
- **FR-010**: System MUST retry transient provider errors (rate-limit/overload/network/5xx) up to 3 times with backoff `[60000,120000,300000]` ms, resetting after a success.
- **FR-011**: System MUST best-effort clean up tracked child/browser processes per run on completion.
- **FR-012**: System MUST persist each run's outcome (last-run, status, error, linked session id, consecutive failures) to `CronJobState`.
- **FR-013**: System MUST, on run failure (including an errored run with no reply payload), publish an alert to the owning agent's default channel via the message bus and raise a Command Center Attention item + Activity event.
- **FR-014**: System MUST preserve `deliver=true` (direct-to-channel, no agent turn) and `deliver=false` (agent processes it).
- **FR-015**: System MUST expose `/api/v1/schedules` CRUD + `{id}/run` + `{id}/pause`, defined contract-first with generated types only; create returns 201 JSON; unknown id returns 404 JSON; invalid input returns 400 JSON.
- **FR-016**: System MUST render schedules as an endless-feed card list (NO tables) in a global Command Center view and a per-agent profile tab, with create/edit/detail in `Sheet` slide-overs, using the existing design system.
- **FR-017**: System MUST badge scheduled-run sessions as `scheduled` in session history and link a run to its session.
- **FR-018**: System MUST fire a schedule missed during downtime once on the next tick and recompute next-run forward (no per-missed-interval catch-up storm).
- **FR-019**: System SHOULD support `session_mode=main` wake timing `now` and `next-heartbeat`.
- **FR-020**: System MUST keep schedules a separate entity from Tasks (no `pkg/taskstore` merge) and add no new sandbox/room topology.

---

## Success Criteria

- **SC-001**: In `TestFire_RoutesToOwnerNotDefault`, the owning agent handles the run and the default agent's loop receives zero invocations.
- **SC-002**: A run with `timeout_seconds=1` against a hanging mock LLM aborts and clears session ownership within ≤ (1s + cleanup window ≤ 5s); no session remains owned afterward.
- **SC-003**: With cap=8 and 8 active runs, a 9th due run does not start until a slot frees (observed start-order in a deterministic harness).
- **SC-004**: An `ask`-gated tool call in a scheduled run returns a denied result with zero approval-wait time.
- **SC-005**: A transient-error run records retry timestamps matching `[60s,120s,300s]` offsets on the injected clock; a subsequent success resets `ConsecutiveFailures` to 0.
- **SC-006**: A failed run publishes exactly one alert to the owning agent's default channel and creates exactly one Attention item.
- **SC-007**: `make verify-contracts` is idempotent with the new `/schedules` schemas; `TestContract_ScheduleStatusEnumMatches` passes.
- **SC-008**: All `/schedules` endpoints return `Content-Type: application/json`; create=201, unknown=404, invalid=400.
- **SC-009**: The Schedules UI renders zero `<table>` elements; cards use the `space-y-2` feed; Playwright create→run-now shows a `scheduled`-badged session.
- **SC-010**: Full CI green (gofmt, golangci-lint, `go test` incl. `-race` where applicable, govulncheck, typecheck, vitest, verify-contracts) with zero new wall-clock sleeps in the added tests.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US1 | Runs as owning agent; Missing owner no fallback | `TestFire_RoutesToOwnerNotDefault`, `TestFire_MissingOwner_NoDefaultFallback` |
| FR-002 | US1 | Cron tool defaults owner | `TestSchedule_OwnerDefaultsToCreatingAgent` |
| FR-003 | US3 | Timeout abort | `TestGuardrail_TimeoutAbortsAndClearsOwnership` |
| FR-004 | US2 | Session mode outline; Main defers | `TestSessionMode_*` |
| FR-005 | US2, US6 | Session mode outline; Scheduled badge | `TestSessionMode_IsolatedCreatesFreshScheduledSession`, `agentProfile.schedulesTab.test.tsx` |
| FR-006 | US3 | Timeout abort | `TestGuardrail_TimeoutAbortsAndClearsOwnership` |
| FR-007 | US3 | Concurrency cap | `TestGuardrail_ConcurrencyCapQueues` |
| FR-008 | US3 | Overlap skipped | `TestGuardrail_SkipIfRunning` |
| FR-009 | US3 | Ask auto-denied | `TestGuardrail_AskToolAutoDenied` |
| FR-010 | US3 | Retry outline | `TestGuardrail_TransientRetryBackoff` |
| FR-011 | US3 | Child cleanup | `TestGuardrail_ChildProcessCleanup` |
| FR-012 | US4 | Failed run alert | `TestRun_FailureRecordsErrorAndAlertsOwnerChannel` |
| FR-013 | US4 | Failed run alert; Attention; Errored no-reply | `TestRun_FailureRecordsErrorAndAlertsOwnerChannel`, `TestRun_FailureRaisesAttention`, `TestRun_ErroredNoReplyIsFailure` |
| FR-014 | US1 | (regression) | `TestDeliverTrue_SendsDirectNoAgentTurn` |
| FR-015 | US5 | Create 201; Run-now; Pause; Not found; Invalid trigger | `TestSchedulesAPI_*` |
| FR-016 | US6 | CC feed cards; Per-agent tab; Create via Sheet | `schedules.feed.test.tsx`, `schedules.form.test.tsx`, `agentProfile.schedulesTab.test.tsx` |
| FR-017 | US6 | Scheduled badge; Run-now updates history | `e2e/schedules.spec.ts` |
| FR-018 | US3/edge | (edge: missed schedule) | `TestSchedule_MissedDowntimeFiresOnceForward` |
| FR-019 | US2 | Main defers | `TestSessionMode_MainNextHeartbeatDefers` |
| FR-020 | (constraint) | — | `TestCronLoad_BackCompatDefaultsOwnerEmpty` (no taskstore coupling) |

**Completeness check**: Every FR maps to ≥1 BDD scenario and ≥1 test; every BDD scenario above appears in the TDD plan.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|---|---|---|
| 1 | Auto-pause after repeated owner-missing / consecutive failures? | Assume: do NOT auto-pause; keep alerting each run | Should a schedule auto-pause after N consecutive failures (and what N)? |
| 2 | `continue` session retention vs. the 90-day session retention sweep | Assume: persistent session is subject to normal retention; on prune, fall back to fresh | Should `continue` sessions be exempt from retention? |
| 3 | Per-schedule timeout upper bound | Assume: cap at the heartbeat ceiling (600s) unless overridden | Is there a hard max `timeout_seconds`? |
| 4 | Does `main` mode count against the concurrency cap? | Assume: `main`/heartbeat enqueue does NOT consume an autonomous-lane slot (it runs in the heartbeat/main lane) | Confirm `main` is outside the cap. |
| 5 | Run history depth retained in `CronJobState` | Assume: keep last N=20 run records inline; full history via linked sessions | How many run records to retain per schedule? |
| 6 | Attention item dedup for a flapping schedule | Assume: coalesce into one Attention item per schedule, updated; not one per failure | Coalesce or one-per-failure? |

> **GATE**: These six should be resolved or explicitly accepted (defaults noted) before/early in implementation. None blocks contract authoring; #1, #5, #6 affect the contract's run-history/attention shape.

---

## Evaluation Scenarios (Holdout)

> Post-implementation verification only. NOT referenced in the TDD plan or matrix.

### Scenario: Daily report by a non-default agent
- **Setup**: Two agents; make a non-default agent own a schedule "post a one-line status now".
- **Action**: Run-now from the UI.
- **Expected outcome**: The status is produced by the owning agent and delivered to its channel; a `scheduled` session appears in history.
- **Category**: Happy Path

### Scenario: Standup builds on yesterday
- **Setup**: A `continue` schedule that asks "what changed since your last run?".
- **Action**: Run-now twice.
- **Expected outcome**: The second run references the first run's content.
- **Category**: Happy Path

### Scenario: Main-mode reminder
- **Setup**: A `main` schedule "remind me to review PRs", wake=now.
- **Action**: Run-now.
- **Expected outcome**: A system event appears in the agent's main session.
- **Category**: Happy Path

### Scenario: Hung run is bounded
- **Setup**: A schedule whose instruction induces a long/stuck tool loop, `timeout_seconds=30`.
- **Action**: Run-now and wait.
- **Expected outcome**: The run ends near the deadline; no session stays "processing"; an error/timeout is recorded and alerted.
- **Category**: Error

### Scenario: Provider outage
- **Setup**: Point the agent at an unreachable provider; run a schedule.
- **Action**: Run-now.
- **Expected outcome**: Retries on backoff, then a failure alert to the owner's channel + an Attention item.
- **Category**: Error

### Scenario: Overlap protection
- **Setup**: A schedule whose run takes ~60s; trigger it, then Run-now again immediately.
- **Action**: Two fires within the run window.
- **Expected outcome**: The second is skipped with a clear toast; only one run executes.
- **Category**: Edge Case

### Scenario: Wrong-owner regression check
- **Setup**: Delete the agent that owns an enabled schedule.
- **Action**: Let it fire.
- **Expected outcome**: An owner-missing error + alert; the default agent does NOT answer.
- **Category**: Edge Case

---

## Assumptions

- Reuses the existing `UnifiedStore` session model and `pkg/cron` store; no v0.3 Rooms topology.
- The mock LLM used in tests can be instructed to hang, error terminally, return a transient error, or call an `ask`-gated tool.
- "Owning agent's default channel" = the channel resolved from the agent's bindings / default routing (US4 alert target); if the agent has no channel, the alert still records an Attention item + Activity event.
- Config keys live under `agents.defaults` / per-schedule fields (timeout, concurrency, wake) consistent with existing config conventions.
- Contract-first: all `/schedules` types and any WS run/attention frames are generated; no hand-written wire types.

## Clarifications

### 2026-06-02
- Q: Where do failed-run alerts go? → A: The **owning agent's default channel** only (+ Command Center Attention + toast).
- Q: How should a fired schedule use the session model? → A: **Per-schedule session mode** — `isolated` (default), `continue`, `main` (with wake now / next-heartbeat) — modeled on OpenClaw's per-job session modes; "it depends on the kind of schedule".
- Q: What guardrails? → A: **Core + retry/cleanup** — 300s timeout (configurable) + abort/ownership-clear, global concurrency cap 5, skip-if-running, headless auto-deny `ask`, transient retry `[60s,120s,300s]`×3, child-process cleanup — validated against OpenClaw/Cowork.
- Q: Data model? → A: Cron jobs stay their own entity; `/api/v1/schedules` is a contract-first projection over `CronJob`; no Task merge.
- Q: Research basis? → A: OpenClaw automation docs (cron session modes + heartbeat + maxConcurrentRuns/timeoutSeconds + retry backoff), Hermes (fresh isolated sessions, jobs.json), Claude Cowork (prompt guardrails, headless auto-deny, run-now).
