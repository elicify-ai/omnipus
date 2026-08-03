# ADR-057: Unify delegate sub-turns onto the own-session execution path

- **Status:** Proposed (v2 — rewritten on verified grounding; v1 was a "parity" draft that two independent reviews returned REVISE)
- **Date:** 2026-08-03
- **Related:** [ADR-053](ADR-053-unified-goal-plan-subagent.md) D1 (this ADR deliberately supersedes D1's dual-namespace ratification — see §7); [ADR-052](ADR-052-autonomous-agent-plan-execution.md) §6.4(a) (plan Stop fan-out); [ADR-056](ADR-056-background-job-visibility.md) (`list_jobs`, and the `shell` kind cut this ADR unblocks); `docs/internal/specs/cancel-cross-channel-spec.md` FR-6a
- **Deciders:** Operator (Daniel Piatkowski)
- **Evidence level:** 1 — direct codebase verification (two independent grounding passes, 2026-08-03; every claim below carries a file:line citation)

> **v2 changelog.** v1 framed this as "parent/child session parity" and was reviewed REVISE by both an architecture red-team and an impact assessment. Three of v1's premises were **factually wrong** and are corrected here: (1) v1's Open Q3 asked whether an explicit parent→child edge exists — **it already does, durably** (`OwnerScopeID`/`ParentAgentID`); (2) v1 claimed children's conversations are ephemeral — they are **fully persisted into the parent's transcript** and merely hidden by a filter; (3) v1 sized the cancel cascade as new work — a childID-keyed cascade **already exists** for hard-abort. A follow-up validation also **disproved** the largest feared blocker (that `list_jobs` would go blind). The framing is now: *this is a simplification that removes a special case*, not a parity change that adds one.

## 1. Context

Omnipus has **two mechanisms for running an agent**, and only one of them is a special case.

**The normal path — own session.** Task execution and plan execution mint a real session and stamp their own transcript id:
- `pkg/agent/task_executor.go:528`, `:1958` — `sessStore.NewSession(session.SessionTypeTask, "system", t.AgentID)`
- `pkg/agent/task_executor.go:1252`, `pkg/agent/plan_engine.go:2787` — `TranscriptSessionID: taskSessionID` (its **own** id)
- `pkg/agent/plan_engine.go:3965` — same, for the plan owner session

**The special case — delegate sub-turn.** A delegated child does neither:
- `pkg/agent/subturn.go:1034` — `TranscriptSessionID: parentTS.transcriptSessionID` (inherits the **parent's** id, "FR-6a")
- `pkg/tools/delegate.go:1248` — *"UnifiedStore.NewSession is never called for a child turn"*

This split gives a delegated child **two identity namespaces**: a unique `delegateSessionID` (== `childID` == `sessionKey`) for per-delegation operations, and the parent's `transcriptSessionID` for cascade-grouping and transcript writes. Confusing the two is a silent no-op, and that is not hypothetical — it is the confirmed root cause of two P0 bugs found in live UAT (2026-07-31): **#576** (`message_parent` read the wrong key → failed 100% of the time) and **#577** (`cancel` resolved the wrong key → returned success while cancelling nothing). Both are now fixed, but the *shape* that produced them remains.

## 2. Decision

**Route the delegate sub-turn onto the existing own-session path.** A delegated child becomes an ordinary session: it mints its own session record with its **existing** id, stamps that id as its own `TranscriptSessionID`, and is discovered for cancellation via the **durable parent→child edge that is already written today**.

This is a **net deletion**. What goes away:
- the FR-6a transcript-id inheritance (`subturn.go:1034`),
- the `IsDelegateChildEntry()` hide-filter (`pkg/session/daypartition.go:332-334`) and both server-side filter sites (`pkg/gateway/replay.go:298`, `pkg/gateway/rest.go:823-832`) — child content is no longer in the parent's file, so there is nothing to filter,
- the dual-namespace class of silent-no-op bugs,
- the `NoHistory: true` sub-turn special-casing (`subturn.go:1032`).

What is added: one exact-id session-create wrapper, and a durable descendant walk for cancel.

### D1 — One id, four roles
The child's `delegateSessionID` becomes its session id, its `sessionKey`, and its `transcriptSessionID`. `UnifiedStore.NewSession` mints its own id (`pkg/session/unified.go:405-418`) and therefore cannot be used as-is — but the exact-id primitive already exists (`createSessionLocked`, `unified.go:441-479`, doc: *"creates a session directory with the EXACT supplied id"*) and is **already exported for another caller** (`GetOrCreateScheduledSession`, `unified.go:566-583`). A ~6-line sibling wrapper preserves the id. **Consequence: `steer`/`respond`/`cancel`/`peek`/`inbox`/`follow_up` keep taking exactly the id they take today.**

### D2 — Cancel by durable edge, not by shared-id scan
Today `InterruptSession` finds descendants by scanning `activeTurnStates` for `ts.transcriptSessionID == sessionID` (`pkg/agent/steering.go:449-464`). That is replaced by a walk over the durable edge already being written at delegation time: `OwnerScopeID = ToolDelegateSessionID(ctx)` with `OwnerScopeKind = OwnerScopeParentSession` (`pkg/tools/delegate.go:1119-1122`), plus `ParentAgentID` (`:1149`).

The in-memory `turnState.childTurnIDs` (`pkg/agent/turn.go:148`, appended `subturn.go:1151`) is **not** adequate as the authoritative edge: it is append-only with no removal anywhere in the non-test tree, and it dies with the parent's `turnState` — so it cannot reach `critical=true` children that outlive their parent. It remains usable as the best-effort hard-abort list it already is (`turn.go:1383-1393`).

### D3 — Plan cancel is unchanged, and is the model
`StopPlan` (`pkg/agent/plan_engine.go:2044-2135`) already builds an explicit `[]string` of member + verifier + owner + supervision session ids under `planDecisionMu` and calls `RequestCancelForSession` once per id (`:2330-2385`). It is **already an explicit list-walk, not a cascade** — the same shape D2 adopts. No change to plan cancel.

### D4 — Trigger policy stays differentiated
This ADR unifies the **execution primitive**, not the orchestration. Plan-owned tasks keep their deterministic engine trigger, dependency DAG, judge-round adjudication, and the ADR-053 D7 rule that members have no individual start/cancel. Delegate-owned tasks keep their parent-free-will trigger and per-delegation control. **One primitive, two trigger policies.**

### D5 — Ownership gate narrows deliberately (operator decision required)
`callerOwnerKey(ctx)` returns `ToolTranscriptSessionID(ctx)` (`pkg/tools/delegate.go:1966-1968`) and is compared against `rec.ParentDurableKey` (`:1973-1979`), which was stamped from the same accessor at `run` time (`:1106`). Because every descendant currently inherits the root chat's transcript id, this gate is **effectively chat-subtree-wide today** — a parent can `steer`/`cancel`/`peek` its *grandchildren*, and a child can address its *siblings*. The codebase documents this sharing in three places (`pkg/session/lifecycle.go:225-228`, `pkg/tools/list_jobs_sources.go:311-315`, `pkg/tools/delegate.go:1130-1131`).

Under this ADR it narrows to a **strict direct-parent check**. That is a security improvement and a behavioural change. It gates exactly six actions: `inbox`, `steer`, `respond`, `cancel`, `follow_up`, `peek`.

> **OPEN — operator decision.** If root-level control over a deep subtree must survive, `verifyCallerOwnsSession` walks the `OwnerScopeID` chain instead of a single equality (small, once decided). Default if undecided: **narrow** (fail-closed).

## 3. What this does NOT break (validated)

A dedicated validation pass (2026-08-03) checked the recently-shipped steering surface and the background-activity dashboard, because preserving them is a hard requirement.

**Steering is unaffected — CONFIRMED.** The steering queue's scope key is already the child's own `sessionKey`, never the parent transcript id: producer `EnqueueSteeringMessage(sessionID, …)` → `sq.queues[scope]` (`pkg/agent/steering.go:75-85`, `:218-227`); consumer drains `ts.sessionKey` (`pkg/agent/loop.go:7223`, `:7227`, `:8234`, `:9138`, `:9211`); binding `childID → opts.SessionKey` (`subturn.go:698`, `:1020`), with the code comment stating it outright (`subturn.go:693-695`). Since D1 preserves `delegateSessionID == sessionKey`, both ends are unchanged. `cancel` likewise resolves via `activeTurnStates.Load(sessionKey)` (`steering.go:611-619`).

**`list_jobs` is unaffected — CONFIRMED, and the feared blocker is disproven.** All three shipped kinds scope by **agent id**, not by any session id: `plan` via `p.OwnerAgentID` (`list_jobs_sources.go:176`), `task` via assignee-or-creator (`:255`), `subagent` via `ParentAgentID` in the store filter plus an in-process re-check (`:324`, `:430`). The tool **deliberately refuses** to infer parentage from the shared key — `list_jobs_sources.go:311-315` warns it "leaks grandchildren". `actionable` resolves through `DelegateTool.ResolvableSessionIDs` keyed by delegate session id (`delegate.go:1603-1612`), which D1 preserves. **Same rows before and after.**

**WS span brackets are safe — CONFIRMED.** `SubTurnSpawnPayload.SessionID` / `SubTurnEndPayload.SessionID` are sourced from `parentTS` as deliberate separate fields (`subturn.go:1183`, `:1424`), with the child's id riding the same payload as `Label` (`:1177`). They stay parent-scoped by construction; the requirement is simply not to "tidy" them to `childTS`.

## 4. Consequences

**Gained**
- One execution mechanism instead of two; the sub-turn special case is deleted, not extended.
- The dual-namespace silent-no-op class (#576/#577 shape) becomes structurally impossible.
- UI drill-down into a child session becomes possible with the **existing** session components (`src/routes/_app/sessions.$sessionId.tsx` → `<ChatScreen />`), because `GET /api/v1/sessions/{childID}` will resolve — today it 404s (no `UnifiedMeta`, `delegate.go:1248`).
- **Unblocks ADR-056's cut `shell` kind (#564).** That kind was cut precisely because *"a background shell carries no agent id, and a delegated child shares its parent's transcript session, so a shell row cannot be attributed to a principal at all"* (`pkg/tools/list_jobs.go:25-30`). This ADR supplies the missing precondition.

**Costs / risks**
- **R-1 (silent, must not be missed).** `delegateStatusExtra` → `recentActivityLines(task.SessionID, …)` (`delegate.go:1823`, `:1855`) reads the **parent's** transcript. Under this change it reads the wrong file, finds nothing, and **returns nil silently** — the function is documented to treat every failure as "no snapshot available" (`:1846-1850`). One-line fix (`task.DelegateSessionID`), but it is the project's known green-but-broken defect class.
- **R-2 (silent).** `KillAllForSession` kills `ProcessSession`s by `OwnerSessionID` (`pkg/tools/session.go:399-404`), invoked from `RequestCancel` via `hooks.KillBackgroundSessions` (`pkg/agent/cancel.go:233-234`). Today a chat-level Stop kills background shells started by *children* because they share the chat id. After this change it would not — orphaned detached processes. Requires the D2 descendant walk to cascade shell-kill too. (`InterruptBySessionKey` does not call it today.)
- **R-3 (the one materially hard piece).** In-span tool frames (`ToolExecStartPayload`/`ToolExecEndPayload`, `loop.go:8688`, `:9038`) are stamped with `ts.transcriptSessionID`, which becomes the child's. They survive on the originating connection (`matchesEvent` matches `ChatID` first, `pkg/gateway/websocket.go:2968-2978`) but **after a browser reload** (reattach by session id) they stop matching, while the span brackets keep arriving → **empty spans in SubagentBlock/ActivityPanel**. Needs a parent-scoped routing id threaded onto those payloads; crosses the contract boundary, so Constraint #8's 5-step pipeline applies.
- **R-4.** Losing the free subtree sweep: today one chat-level cancel reaches every descendant including `critical=true` children that outlive their parent turn. D2's edge must be durable to match that, or orphaned async children survive a Stop.
- **R-5.** Session-count growth: every delegation becomes a real on-disk session directory (`unified.go:462-475`). A 24-way fan-out is 24 directories. Retention, listing, and the sessions UI need to treat these as subordinate.
- **R-6.** Where child narration renders in a *reloaded* chat changes (today inline-but-hidden in the parent's transcript). **INFERRED — the write path is confirmed, history replay was not traced.** Must be closed in the spec.

## 5. Work items

1. Exact-id session-create wrapper (mirror `GetOrCreateScheduledSession`); call from `spawnSubTurn` with `childID`; set `opts.TranscriptSessionID = childID`. Must mint into the **same** shared `*session.UnifiedStore` the delegate tool holds (`loop.go:1727-1728`).
2. Durable descendant walk for cancel over `OwnerScopeID`/`ParentAgentID`; extend the graceful path, not just hard-abort; must survive parent-turn death (R-4).
3. Cascade `KillBackgroundSessions` to descendants (R-2).
4. Fix `recentActivityLines` to use the delegate session id, and make its empty path log (R-1).
5. Parent-scoped routing id on in-span tool payloads (R-3) — contract change.
6. Delete `IsDelegateChildEntry` + both filter sites; decide the replacement drill-down surface (R-6).
7. Re-point `DelegateTaskState.SessionID` (`delegate.go:1303`) deliberately; pin `SubTurnSpawnPayload.SessionID` to `parentTS` with a regression test.
8. Resolve D5 (ownership narrowing) per operator decision.
9. **Unify the throttles.** Three independent ones exist: `turnState.concurrencySem` (per-parent-turn, `turn.go:150`, set only at `subturn.go:1051`), `TaskExecutor.dispatchSema` (global, `task_executor.go:58-60`), `TaskExecutor.maxConcurrent` (per-agent, checked only at `:453`). Two holes to close while here: **root-level delegation is entirely ungated** (the semaphore is never set on a root turn, so `subturn.go:607`'s guard is false — matching the live "24 parallel against a cap of 16" observation in `docs/internal/uat/max-parallel-concurrency-gap-2026-07-31.md` §G1), and **`StartTaskNow` skips the per-agent cap** (`:1944` takes only `dispatchSema`). Note the semantics differ: `concurrencySem` blocks-with-timeout, the task caps try-and-refuse.

## 6. Alternatives considered

**A. Named ID types only** (`type SessionKey string` / `type TranscriptSessionID string`). The architecture red-team argued this captures the real #576/#577 safety win at a fraction of the cost, since those were type-confusion bugs between two bare `string`s. **Rejected as the endpoint, adopted as a companion:** it prevents *future* confusion but keeps both mechanisms, so it delivers none of the deletions in §2, does not unblock #564, and does not enable drill-down. Recommend doing it *alongside* work item 1 — it is cheap and makes the migration compiler-checked.

**B. Keep the split, patch bugs as found.** Rejected: the fix wave already patched five instances of this shape; the shape itself is the defect generator.

**C. Full parity including per-child transcript retention semantics (v1's R6).** Deferred, not rejected — §5 item 6 carries the decision.

## 7. Relationship to ADR-053 D1

ADR-053 D1 was amended **2026-07-31** to state FR-6a is *retained and load-bearing*. That amendment was a **truth-in-documentation fix**: D1 originally claimed "FR-6a dropped", which contradicted the code and would have re-broken the chat-wide Stop if implemented in good faith. The amendment correctly described the system **as built**.

This ADR **deliberately supersedes that amendment** by changing the system rather than the description. D1's "isolated-but-linked" intent becomes literally true in code. This is a considered reversal with a migration (§5), not a contradiction.

## 8. Open questions for the spec

1. **D5** — narrow the ownership gate, or walk the `OwnerScopeID` chain to preserve root-over-subtree control? (operator decision)
2. **R-6** — where does child narration render in a reloaded chat once it leaves the parent's transcript? (needs history-replay trace)
3. **R-5** — retention and listing policy for subordinate sessions.
4. **Item 9** — one throttle or two, and which back-pressure semantics win (block-with-timeout vs try-and-refuse)?
5. Migration for sessions in flight at deploy time.

## 9. Verification requirements (non-negotiable)

R-1 and R-2 both fail **silently** — success-shaped results while doing nothing. This is the exact "mechanism not property" class this project has been bitten by repeatedly (precedent: `plan_engine.go:3937-3944`, a derived `plan:<id>` id that cancelled nothing in production for months while every test passed, because the fake canceller recorded the string it was handed and returned success).

Therefore: each of R-1, R-2, R-3 gets its **own explicit acceptance criterion, verified against real store-backed state and a real registered turn — never a spy or a mock that records the argument**. R-3's failure mode (empty spans *only after reload*) must be exercised with an actual reconnect, not a static assertion.
