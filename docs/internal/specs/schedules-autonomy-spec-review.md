# Spec Grill Report — ROUND 3 (focused): Revision-3 wiring fixes (W-1..W-8)

**Spec**: `docs/internal/specs/schedules-autonomy-spec.md` (Revision 3 — Wiring fixes block)
**Reviewed**: 2026-06-02
**Round**: 3 — focused verification only. Round 2 was REVISE (0 CRITICAL, 6 MAJOR). This round verifies ONLY the 8 Revision-3 wiring fixes against the actual code; the rest of the spec is not re-grilled.
**Verdict**: **implementation-ready = YES** (with two small must-fix-in-code notes folded into W-3 and W-7 below — both are spec-accurate, the notes are implementation guardrails, not spec defects).

---

## Per-fix verdicts

### W-1 — `ProcessScheduled` owner-pinning + abort — **HOLDS**

Every load-bearing claim verified against code:

- **`transcriptSessionID` gates turn registration AND abort.** `processMessage` sets `transcriptSessionID = msg.SessionID` only when non-empty (`loop.go:3016-3019`). `runAgentLoop` → `newTurnState` copies `opts.TranscriptSessionID` into `ts.transcriptSessionID` (`turn.go` newTurnState: `ts.transcriptSessionID = opts.TranscriptSessionID`), and `registerActiveTurn` stores the turn (`turn.go:195-196`). `GetActiveTurnHookForSession` matches **by `ts.transcriptSessionID`** (`turn.go:308-331`), and `RequestCancel(CancelScope{SessionID})` resolves the hook via that function (`cancel.go:145-147`) → `Fired:true` only when a turn is registered under that session id. So passing a concrete `sessionID` as `TranscriptSessionID` makes the run cancellable. **HOLDS.**
- **`agentSessionKey` per-owner key.** `agentSessionKey(agentID, msg)` yields `agent:<id>:session:<sessionID>` when `msg.SessionID != ""` (`loop.go:3223-3227`). With a per-run/per-schedule id this is collision-free across isolated runs. **HOLDS** — but note `ProcessScheduled` should set `processOptions.SessionKey` to this `agent:<owner>:session:<id>` string itself (it does not go through `agentSessionKey`, which lives inside `resolveMessageRoute`); building the key in `ProcessScheduled` is trivial and is the intended design.
- **Owner-pinned entry without touching `sessionActiveAgent`.** `runAgentLoop(ctx, agent *AgentInstance, opts processOptions)` (`loop.go:3320-3324`) takes an explicit agent and runs the turn directly — it never calls `resolveMessageRoute`, so it cannot hit the priority-1 `sessionActiveAgent.Delete` at `loop.go:3133-3135`. `processOptions` carries `SessionKey`, `TranscriptSessionID`, `TranscriptStore`, `Channel`, `ChatID`, `UserMessage` (`loop.go:256-275`) — everything `ProcessScheduled` needs. `ProcessHeartbeat` (`loop.go:2924-2950`) is the existing proof that you can look up an agent and call `runAgentLoop` directly, bypassing routing. **HOLDS.**

**Remaining net-new work:** add `ProcessScheduled(ctx, ownerAgentID, sessionID, content, channel, chatID)` that (a) `registry.GetAgent(ownerAgentID)` → error if missing (FR-001, no default fallback), (b) `ResolveSessionStore(sessionID)` for `TranscriptStore`, (c) builds `SessionKey = "agent:"+ownerAgentID+":session:"+sessionID`, (d) calls `runAgentLoop` with `TranscriptSessionID = sessionID`. Small, contained, mirrors `ProcessHeartbeat`.

### W-2 — `GetOrCreateScheduledSession` — **HOLDS**

- **`writeMetaLocked` / meta-by-id read exist.** `readMetaLocked` (`unified.go:307`) and `writeMetaLocked` (`unified.go:311-313`) are present; `GetMeta` (`unified.go:236-243`) is the public meta-by-id reader. A get-or-create-by-id = try `GetMeta(id)`, and on not-found do the `NewSession` body but with the supplied id instead of `NewSessionID()` (`unified.go:189-234`). All the pieces (mkdir, writeMetaLocked, empty transcript) are inline and copyable. **HOLDS.**
- **`SessionTypeScheduled` adds cleanly.** `UnifiedSessionType` is a string enum with `chat`/`task`/`channel` consts (`unified.go:22-28`); adding `SessionTypeScheduled = "scheduled"` is a one-line additive change. **HOLDS** (O-1 from round 2 still applies: assert the new value round-trips through the SPA/contract enum for `AgentSession`).
- **`main` → reserved id avoids `processSystemMessage` forcing.** Confirmed the hazard the spec routes around: `processSystemMessage` hard-codes `agent := al.GetRegistry().GetDefaultAgent()` (`loop.go:3299`) and `BuildAgentMainSessionKey(agent.ID)` — it cannot run as the owner. Collapsing `main` into "continue with reserved id `sched-main-<ownerAgentID>`" and routing it through `ProcessScheduled` (owner-pinned, W-1) sidesteps `processSystemMessage` entirely. **HOLDS.**

**Remaining net-new work:** the get-or-create-by-id method (refactor the `NewSession` body to accept an optional id) + the `SessionTypeScheduled` const + `TriggeredBy` provenance field (FR-005). Bounded.

### W-3 — Lane registers with the shutdown drain — **HOLDS** (with one implementation note)

- **`activeRequests` accounting is automatic for any synchronous turn — including a direct `ProcessScheduled` call.** Critical correction to the round-2 worry: `activeRequests.Add(1)/Done()` wraps the **per-LLM-call** inside `callLLM` in `runTurn` (`loop.go:3954-3955`) and the recap path (`loop.go:5802-5804`) — NOT only the bus-dispatch goroutines (`loop.go:1589,1619`). So a scheduled run executing through `runAgentLoop`→`runTurn` is counted by `activeRequests` for the duration of each provider call, exactly as the *current* synchronous cron path already is. `WaitForActiveRequests()` (`loop.go:1799-1800`, `activeRequests.Wait()`) therefore covers in-flight scheduled turns **without the lane needing to touch `activeRequests` itself**. **HOLDS** — and is actually simpler than the spec implies.
  - **Implementation note (not a spec defect):** `activeRequests` is a private `sync.WaitGroup` (`loop.go:90`) with no exported `Add/Done`. The lane (in `pkg/cron`/`pkg/tools`) therefore CANNOT and NEED NOT increment it directly — the coverage comes from `ProcessScheduled` running inside the loop. The spec's phrase "increment on lane dispatch, decrement on completion" should be read as "the turn the lane dispatches is already accounted" — the lane itself does no `activeRequests` bookkeeping. If the spec author wants belt-and-suspenders coverage for the *queued-but-not-yet-dispatched* window, that requires a lane-local `sync.WaitGroup` that `CronService.Stop()` waits on (next bullet), not `activeRequests`.
- **`CronService.Stop()` can be made to block.** Today it just sets `running=false`, closes `stopChan`, nils it (`service.go:111-124`) and returns. Adding a lane `context.CancelFunc` + a lane `sync.WaitGroup` that `Stop()` cancels-then-`Wait()`s is a self-contained change to `CronService`. **HOLDS.**
- **`shutdown.go` order drains within budget.** Confirmed `CronService.Stop()` is called at shutdown step 1 BEFORE `agentLoop.WaitForActiveRequests()` (`shutdown.go` step-2 block). Confirmed the budget figure: `maxActiveTurnWait := int((omnipusShutdownTimeout - 5*time.Second).Seconds()) // 65 s` (the in-code comment literally says `// 65 s`) — the spec's "~65s" is correct (round-2 corrected the earlier "70s"). One real ordering subtlety the spec already calls out: for a *parallel* lane, `Stop()` must block on the lane WaitGroup (so in-flight lane turns finish or are cancelled) and it is called before the drain — both true after the W-3 changes. **HOLDS.**

**Remaining net-new work:** lane ctx + WaitGroup inside `CronService`; make `Stop()` cancel+wait (bounded by the 65s budget via the shutdown caller, which already `select`s on `time.After`). The in-flight cancellation at the deadline is the W-1 `RequestCancel` path. Bounded, and the design is coherent.

### W-4 — `ExecuteJob` `(string,error)` + adapter — **HOLDS**

Verified exactly as the spec describes:
- `JobHandler` is **already** `func(job *CronJob) (string, error)` (`service.go:59`).
- The defect is the gateway adapter at `gateway.go:1779-1782`: `cronService.SetOnJob(func(job *cron.CronJob) (string, error) { result := cronTool.ExecuteJob(context.Background(), job); return result, nil })` — it discards the error (`return result, nil`) and `ExecuteJob` itself returns only `string` (`tools/cron.go:306`) and stringifies errors into successes (`tools/cron.go:382` `return fmt.Sprintf("Error: %v", err)`).
- The fix is contained to two spots: change `ExecuteJob` to `(string, error)` and the adapter to propagate. `executeJobByID` already records `error` status from the handler return, so the chain works once the error stops being swallowed. **HOLDS.**

### W-5 — Clock seam for `checkJobs`, not the timer — **HOLDS**

- `checkJobs` reads `time.Now().UnixMilli()` inline (`service.go:181`) and is the due-detection logic; `runLoop` owns the production `time.NewTimer`/`Reset` (`service.go:127-170`) and is structurally separate.
- An injected `now func() time.Time` (or `Clock`) used by `checkJobs` + the state math (`computeNextRun` at `service.go:286,304`, `recomputeNextRuns` at `service.go:348`) is addable without touching `runLoop`'s real timer. An exported `RunDueJobs(now)`/`Tick(now)` can re-use the exact collect-due-then-execute body of `checkJobs` (`service.go:184-212`) and be driven synchronously by tests with zero wall-clock sleeps. **HOLDS.** (Round-2 m-2 is satisfied: the seam is on `checkJobs`/state math, and tests bypass `runLoop` rather than faking its timer.)

**Remaining net-new work:** thread `now` through `checkJobs`/`computeNextRun`/`recomputeNextRuns`; export `RunDueJobs(now time.Time)`. Bounded.

### W-6 — Authz via `AuthorizeAgentAccess` — **HOLDS**

`config.AuthorizeAgentAccess(user *config.UserConfig, agent *config.AgentConfig) error` exists (`agent_ownership.go`, confirmed signature + body): system/core agents → any authenticated user; custom agent with empty `OwnerUsername` → `ErrAgentOrphan`; otherwise owner-OR-admin (`user.Role == UserRoleAdmin || user.Username == agent.OwnerUsername`). REST schedule create/update handlers have the authenticated `*config.UserConfig` (REST auth resolves `User.Username`) and can look up the chosen owner `*AgentConfig`, so the call is wirable as-is. Not a new ACL. **HOLDS.**

**Remaining net-new work:** call it at schedule create/update with the chosen owner; map `ErrAgentOrphan`/access-denied to the documented HTTP code (503 for orphan per existing convention, 403/400 for cross-owner). Bounded.

### W-7 — Notification ownership — **HOLDS** (with one gap the spec already self-flags)

- **Per-user identity exists.** REST auth resolves `User.Username` (`UserConfig.Username`); the WS connection stores `wsConn.userID` set to `user.Username` at auth time (`websocket.go:149` field decl `userID string`, `websocket.go:517` `wc.userID = user.Username`). So a `notification` WS frame can be filtered per-connection by `wsConn.userID`, reusing the #283 per-connection-interest pattern. **HOLDS.**
- **Agent owner-username field exists.** `AgentConfig.OwnerUsername string \`json:"owner_username,omitempty"\`` (`config/config.go:473`). So the spec's "owning agent's owner user" target is resolvable. **HOLDS.**
- **Self-flagged gap (already covered by the spec's fallback):** core/system agents have **no** `OwnerUsername` (it is `omitempty` and `IsSystemAgent` agents are owner-less by design — `AuthorizeAgentAccess` treats them as universally accessible). For a headless failure of a schedule owned by a core agent, neither (a) `created_by` (if that user is gone) nor (b) agent `OwnerUsername` may resolve. The spec's W-7 already specifies the correct terminal fallback: **"If neither resolvable, notify all admins."** That closes the orphan case. **HOLDS**, no net-new gap — just confirm the implementation actually reaches the all-admins branch when `OwnerUsername == ""`.

**Remaining net-new work:** store `created_by` on `CronJob`; resolve notification recipients = {created_by user} ∪ {owner agent's `OwnerUsername` if set} else admins; filter the live frame by `wsConn.userID`. Bounded; the Notifications entity + WS frame are the genuinely net-new (correctly framed) primitives.

### W-8 — Migration nil-default — **HOLDS**

`GetDefaultAgent()` is explicitly nilable: it returns `nil` when no agent is routing-default, no override, no `main` sentinel, and zero agents registered (`registry.go:238-240` `if len(ids) == 0 { return nil }`). The W-8 design — backfill empty `owner` with the default id **only if one exists**, else leave empty + skip firing + warn (no alert-spam), and persist the backfill once (idempotent, only fills empties) — is implementable directly against this nilable API. This resolves round-2 M-5. **HOLDS.**

**Remaining net-new work:** at load, for each owner-less job: if `GetDefaultAgent() != nil` set `owner` and persist; else mark non-firing + warn. Persist write makes it idempotent. Bounded.

---

## Summary table

| Fix | Verdict | Net-new work (all bounded) |
|---|---|---|
| W-1 ProcessScheduled | **HOLDS** | new `ProcessScheduled` method (mirrors `ProcessHeartbeat`), builds session key itself |
| W-2 GetOrCreateScheduledSession | **HOLDS** | get-or-create-by-id (refactor `NewSession` body) + `SessionTypeScheduled` + `TriggeredBy` |
| W-3 lane + shutdown | **HOLDS** | lane ctx + WaitGroup; `Stop()` cancel+block; turns auto-counted by `activeRequests` (no lane bookkeeping needed) |
| W-4 ExecuteJob (string,error) | **HOLDS** | change `ExecuteJob` sig + adapter at `gateway.go:1779`; `JobHandler`/`executeJobByID` already correct |
| W-5 clock seam | **HOLDS** | inject `now` into `checkJobs`/state math; export `RunDueJobs(now)`; leave `runLoop` timer |
| W-6 authz | **HOLDS** | call `AuthorizeAgentAccess` at create/update |
| W-7 notification ownership | **HOLDS** | recipients = created_by ∪ owner `OwnerUsername` else admins; filter frame by `wsConn.userID` |
| W-8 migration nil-default | **HOLDS** | backfill-if-default-exists else skip+warn; persist once |

**All 8 Revision-3 wiring fixes HOLD against the actual code.** Two implementation notes (not spec defects): (W-3) `activeRequests` is private and covers turns automatically — the lane does no `activeRequests` bookkeeping; queued-but-undispatched runs need the lane-local WaitGroup, which the spec already describes via the blocking `Stop()`. (W-7) the all-admins fallback (already in the spec) is the load-bearing path for core-agent-owned schedules with no `OwnerUsername`.

The round-2 MAJORs J-1..J-4 and M-5 are each addressed by a corresponding W-fix with a verified, connected call path:
- J-1 (owner-pin + abort wiring) → W-1 (concrete `sessionID` → `TranscriptSessionID` → registered turn → `RequestCancel Fired:true`).
- J-2 (handoff-map mutation) → W-1 (`runAgentLoop` direct call bypasses `resolveMessageRoute`, never touches `sessionActiveAgent`).
- J-3 (`main` mechanism) → W-2 (reserved id `sched-main-<owner>` via get-or-create, routed through owner-pinned `ProcessScheduled`, not `processSystemMessage`).
- J-4 (shutdown drain) → W-3 (blocking `Stop()` + `activeRequests` auto-coverage + 65s budget, all verified).
- M-5 (nil default at load) → W-8 (nilable `GetDefaultAgent()` confirmed; skip+warn+persist-once).

---

## Verdict

**Implementation-ready: YES.**

The Revision-3 block connects every Rev-2 primitive into a verified call path against real code. The remaining work is bounded, additive plumbing (one new loop method, one session get-or-create, a cron clock seam + blocking Stop, two signature fixes, two authz/notification wirings, one migration) — no fictional symbols, no missing building blocks. Contract authoring (hard-constraint #8) can proceed: the contract-shaping decisions from round 2 (Notification coalescing key, run-history-of-20 shape) are settled in Revision 2's dispositions.

Proceed to task decomposition:
  /taskify docs/internal/specs/schedules-autonomy-spec.md
