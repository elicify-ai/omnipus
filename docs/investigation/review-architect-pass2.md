# Architecture Review (Pass 2): 5-Bug Fix Batch + Per-Session Worker

**Reviewer:** architect
**Branch:** `feature/iframe-preview-tier13`
**Date:** 2026-05-22
**Inputs:** `docs/investigation/review-architect.md` (pass 1), commits `c27ff7f`, `132bb46`, `b7f3e98`

## Verdict

**APPROVED-FOR-MERGE.** All three pass-1 SHOULD-FIX items have been resolved or honestly down-graded. The new architecture (admission TryAdmit, replayMu, non-closing pendingResults) is internally consistent and free of lock-ordering or lifecycle hazards I can find by static read. Defer the remaining items (SystemOverloadFrame WS path, subagent admission accounting, idle-worker cap) to v0.2 (#175) with the issue text below.

---

## Pass-1 SHOULD-FIX Disposition

### 1. WS capacity rejection wire format — DEFERRED-WITH-RATIONALE

**Pass-1 ask:** emit `SystemOverloadFrame` on WS, plain text on other channels.

**What landed:** Plain-text `OutboundMessage` only. `pkg/agent/loop.go:1597-1604`. Commit message item #8 explicitly names the deferral: *"SystemOverloadFrame WS path deferred — requires gateway package changes, blocked per constraint."*

**Verification:**
- `grep -rn "SystemOverloadFrame\|system_overload" pkg/agent/ pkg/gateway/ pkg/bus/` returns only contract-generated code (`pkg/api/generated/asyncapi_types.gen.go:267`, `pkg/api/generated/fixtures.go:507`). Zero production callers.
- The SPA chat store does handle `system_overload` (`src/store/chat.ts` per pass-1 review), so the type is one wiring step away from useful.

**Why this is acceptable for v0.1:**
- The pkg/agent track was scoped to agent-package fixes; cross-package wiring is a separate change of similar size.
- Hard-constraint #8 is **not** violated — no new wire type was hand-written; the existing typed frame is simply left un-wired in this turn. Adding the wiring is a strict extension, not a contract change.
- Plain-text capacity reply is functional and only mildly worse UX (it lands as agent chat content rather than a toast).

**Mandatory follow-up:** open the v0.2 issue below with concrete acceptance criteria so this doesn't drift.

### 2. Subagent admission accounting — RESOLVED (option 2: documented)

**Pass-1 ask:** option (a) wrap `runSubTurn` with `OnTurnStart/OnTurnEnd`, OR option (b) document the scope honestly.

**What landed:** Option (b), executed well. `pkg/agent/admission.go:12-18`:

> *"Phase 1: gates inbound user-message dispatch only. The counter tracks unique active scopes (one per spawned session worker) — not per-turn, so a single chatty session cannot pin admission slots indefinitely. Subagent spawn and task-executor dispatch paths are NOT gated; see the v0.2 follow-up issue for resource-aware admission that covers those paths as well."*

**Side benefit:** the rewrite from per-turn `OnTurnStart/OnTurnEnd` to per-scope `TryAdmit(scope)` also closes the TOCTOU race I called out in pass-1 finding #1 implicitly (two dispatchers racing past `ShouldAdmit`). `TryAdmit` is atomic under `a.mu` and returns the release closure, so the check-then-claim window is gone. `admission_test.go::TestAdmissionController_TryAdmit_NoOvercommit` (100 goroutines, cap=5) locks the invariant.

**Net:** the scoping doc-comment is honest, the implementation is now structurally stronger than pass-1 envisaged, and the follow-up issue text below makes the v0.2 expansion concrete.

### 3. Replay drain timeout — RESOLVED (exactly as recommended)

**Pass-1 ask:** option 1 (1s per-frame deadline, log degraded, exit drain).

**What landed:** `pkg/gateway/websocket.go:1152-1170`. The drain inner-select adds `case <-time.After(1 * time.Second)` with a Warn + `droppedFrames.Add(1)`. `ctx.Done()` still wins when the connection is torn down; the new timeout wins when sendCh is back-pressured but the connection is alive. The comment block (1146-1150) explicitly cites this review finding.

**Acceptance gap:** the pass-1 review asked for *"a unit test that fills `sendCh`, fills `replayDivertCh`, calls `handleAttachSession`, asserts the function returns within a bounded time."* I did **not** find this test in the new `pkg/gateway/websocket_replay_order_test.go`. The existing test covers FIFO ordering, not the back-pressure escape hatch.

**Recommendation:** this is a v0.1-quality gate — the SHOULD-FIX implementation is right, but the test that locks it in is missing. Either:
- Add the back-pressure test before merge (lightweight: ~50 lines), OR
- File as a follow-up regression test under #175 and merge.

Not a blocker because the code change itself is mechanically correct and provably terminates.

---

## New Architectural Concerns Raised in the Prompt

### A. Scope-key consistency across admission / sessionWorkers map / steering — CLEAN

Three distinct keys, all intentional, all consistent end-to-end:

| Site | Key form | Code |
|---|---|---|
| `sessionWorkers` map (per-session worker dispatch) | `route.SessionKey + ":" + msg.SessionID` (long) | `loop.go:3059-3062` (`resolveSteeringTarget`) → `loop.go:1612` (`sessionWorkers.Store(scope, w)`) |
| `admission.activeScopes` (admission accounting) | same long form as the worker map | `loop.go:1585` (`TryAdmit(scope)` with scope returned by `resolveSteeringTarget`) |
| `steering.pushScope` queue + `pendingSteeringCountForScope` | `route.SessionKey` (short, no `:SessionID` suffix) | `steering.go:205` (`enqueueSteeringMessage(route.SessionKey, …)`); `session_worker.go:258` (`pendingSteeringCountForScope(target.SessionKey)`); `target.SessionKey = resolveScopeKey(route, msg.SessionKey)` in `loop.go:1711`. |

The worker-map key and the admission key are now the SAME form (both consume `scope` from `resolveSteeringTarget`). The steering key is intentionally shorter because it must match what `runAgentLoop` registered in `activeTurnStates` (which uses `opts.SessionKey == route.SessionKey`, not the `:SessionID`-suffixed worker scope). Pass-1's "two scope keys" observation still holds and is still correct — admission did not add a third drift; it joined the worker side.

**Risk check:** the only fragile assumption is that `agentSessionKey(agentID, msg)` (used inside the routing layer to build `route.SessionKey`) already encodes `msg.SessionID` when present (`loop.go:3036-3041`). When `SessionID == ""` the route key falls back to `chat:<channel>:<chatID>` — and the worker scope appends `":"` (empty SessionID) which is a no-op. No drift. No third form lurking.

### B. `wsConn.replayMu` lock ordering — CLEAN

Acquisition pattern:

| Site | Lock held | Inside critical section |
|---|---|---|
| Drain (`handleAttachSession`, `websocket.go:1151-1176`) | `wc.replayMu.Lock()` | only channel ops on `wc.replayDivertCh` and `wc.sendCh`. **`h.mu` is acquired AFTER `wc.replayMu.Unlock()` at line 1178.** |
| Writer slow path (`sendRawFrameBytes`, `websocket.go:1371-1430`) | `wc.replayMu.RLock()` | only channel ops on `wc.replayDivertCh`. RUnlock fires BEFORE any potential blocking send. |
| All `h.mu.Lock()` critical sections | `h.mu` | `grep` inside `h.mu`-held regions for `sendRawFrameBytes`/`replayDivertCh <-` / `sendCh <-` returns zero hits. |

No lock is ever held nested with the other. The pattern is "acquire one, release, acquire the other." Lock graph stays a forest; no deadlock cycle possible.

One observation, not a finding: `wc.replayMu` is RWMutex but the writer path takes only RLock and the drain takes Lock — i.e. it's used as a reader/exclusive-writer pair where the "writer" is the rare drain operation. This is the right shape; the fast path's lock-free `isReplayingLive.Load()` check (line 1366) means the RLock is taken only on the slow path. Common-case overhead is one atomic load.

### C. `turn.go::Finish()` no longer closes `pendingResults` — LIFECYCLE IS SOUND

**New lifecycle (well-documented at `turn.go:609-617`):**
1. `Finish()` closes `finishedChan` only (the stop signal).
2. `pendingResults` is left open; consumers (`steering.go:642`, `loop.go:3530`, `loop.go:4961`) use `select+default` — non-blocking receive that never depends on a close to terminate.
3. `deliverSubTurnResult` (`subturn.go:640-697`) recovers panics, reads the channel reference once via `parentTS.pendingResults`, and uses `select` with `Finished()` as the abort path.
4. `activeTurnStates.Delete(sessionKey)` (`turn.go:190`, called by `clearActiveTurn`) drops the last persistent reference to `turnState` once the turn is done.
5. After step 4, any in-flight sender goroutine still holding a local channel ref can complete its select; once it returns, `pendingResults` has no live references and is GC'd.

**Why this is safer than the old close-on-Finish:**
The old pattern had an unavoidable race between `closechan()` (called by `Finish().closeOnce.Do`) and `chansend()` (called by `deliverSubTurnResult` with a captured local ref). The race detector caught it even after field-nil under mutex, because the runtime's chansend cannot back out once it has decided to deliver. The new pattern uses lifecycle ordering ("activeTurnStates ref drop → GC") instead of synchronisation primitives ("close-and-cleanup").

**One subtle invariant** (not a problem, worth noting): a slow `deliverSubTurnResult` goroutine that captured a `pendingResults` ref BEFORE the parent finished can still successfully send to the channel AFTER `clearActiveTurn`. The send just goes into the channel buffer (cap=16) and is never dequeued. This is intentional — the result is silently dropped because nobody is listening, which matches the "subturn.orphan" semantics already named in subturn.go's existing comments. The orphan log event (`subturn.orphan`) is the audit trail.

**Potential leak surface:** if a turn's pendingResults buffer is full (16 entries) when the turn finishes and a sender is mid-select, the sender's `default` branch will fire and log `subturn.orphan`. No goroutine leak. The 16-entry buffer is reclaimed when all senders return.

---

## v0.2 Follow-Up List

These are tracked-deferred items; file as GitHub issues with the text below, all under #175 (Phase-2 admission redesign) or #155 (security hardening) as noted.

### Issue A: `SystemOverloadFrame` WS wire path

- **Title:** ws: emit SystemOverloadFrame on capacity rejection instead of chat text
- **Labels:** `v0.2`, `area/gateway`, `area/agent`, related-to #175
- **Acceptance:**
  - When `bus.PublishOutbound` content matches the capacity-reject canonical text AND the resolved channel is `webchat`, the gateway converts the outbound to a typed `SystemOverloadFrame` and routes via `sendConnGenFrame`.
  - Alternatively (cleaner): admission rejection in `loop.go:1597-1604` resolves the target session's `wsConn` (when `msg.Channel == "webchat"`), calls `sendConnGenFrame(wc, "system_overload", ...)` directly, and only falls through to `bus.PublishOutbound` for non-webchat channels.
  - Non-webchat channels (Telegram, Slack, IRC, etc.) keep the plain-text reply unchanged.
  - Test: WS client → fills cap → rejection arrives as `{"type":"system_overload",...}`, not as `token`/`done`.

### Issue B: Replay-drain back-pressure regression test

- **Title:** ws: add regression test for replay drain back-pressure timeout
- **Labels:** `v0.1` (this branch, if not blocking merge) or `v0.2`, `area/gateway`
- **Acceptance:** fill `sendCh` to cap, fill `replayDivertCh` to cap, call `handleAttachSession`, assert it returns within 30 s and `wc.droppedFrames > 0`.

### Issue C: Subagent admission accounting

- **Title:** admission: include subagent spawns (or formalise the dispatcher-turns-only scope)
- **Labels:** `v0.2`, `area/agent`, depends on #175
- **Acceptance:** either wrap `runSubTurn`/`runAgentLoop` with `admission.TryAdmit` (using a child-scope key derived from parent scope + child turnID; ungated — once parent admitted, children always succeed), or upgrade admission to track concurrent goroutines rather than scopes. Test: 1 parent + N children = activeScopes/activeGoroutines = N+1.

### Issue D: Idle worker cap + LRU eviction

- **Title:** session worker pool: bound goroutine count under slow-loris abuse
- **Labels:** `v0.2`, `area/agent`, depends on #175
- **Acceptance:** config `agent.max_idle_workers` (default `min(2048, NumCPU()*64)`), LRU eviction by last-active timestamp, metric `omnipus_session_worker_count` + `omnipus_session_worker_evicted_total`. Note: pass-1 finding #7 noted the `lastActive atomic.Pointer` was removed in cleanup item #11 of commit `b7f3e98` — restore it as part of this issue.

### Issue E: Container-runtime auto-detect widening

- **Title:** sandbox: widen docker_autodetect to k8s / podman / containerd / crio
- **Labels:** `v0.2`, `area/sandbox`, `area/operations`, related-to #155
- **Acceptance:** unchanged from pass-1 finding #3. (Already filed mentally as ADR seed; promote to a real issue.)

### Issue F: Mid-handoff steering routing (v0.3 / Rooms)

- **Title:** agent: mid-handoff steering message goes to new agent's queue
- **Labels:** `v0.3`, related-to #156
- **Acceptance:** fold into Rooms redesign.

---

## What the Implementation Team Got Right (Pass 2)

1. **The admission API rewrite is a genuine upgrade, not just a fix.** `TryAdmit` returning `(bool, release func())` is the right primitive — atomic, no TOCTOU, release-or-leak is structurally enforced by the closure pattern. The `activeScopes map` (unique scopes, not turn counts) is the correct cardinality: a chatty session can no longer pin admission slots indefinitely. This wasn't asked for explicitly in pass-1; it was inferred from the comment-doc honesty work and delivered as a structural improvement.

2. **The "stop closing pendingResults" race fix in `turn.go::Finish()` is exactly the right call.** Closing a channel while a select-sender holds a captured ref is an unavoidable runtime race that no amount of nil-under-mutex can defend against. Pivoting to "finishedChan is the sole stop signal; let GC reclaim pendingResults" is structurally cleaner — it replaces synchronisation with lifecycle ordering. The doc comment (`turn.go:609-617`) names the race honestly.

3. **The defer-recover discipline is uniform.** Every long-lived goroutine spawned by this branch (`runLoop`, system-message handler, unroutable-message handler, `InterruptSession`/`InterruptSessionHard` goroutines) has a `defer func() { recover() }` with a structured log. A panic in one worker cannot take down the whole agent runtime now. This is the right invariant for a long-running agent process.

4. **The 1-second replay-drain timeout matches the recommended fix exactly.** No over-engineering with watchdog goroutines; the inner select naturally absorbs the timeout case with a single line. The doc comment cites this review finding so future readers know why the bound exists.

5. **The admission docstring is honest about what's NOT covered.** Naming the subagent / task-executor exclusion explicitly in the type-level comment means the next person reading admission.go to extend it cannot accidentally assume full coverage. This is the right pattern: when you defer scope, document the deferral *in the type*, not just in an issue.

---

## Footnote: One Cleanup Item Worth Tracking

Commit `b7f3e98` cleanup item #11 removed `lastActive atomic.Pointer` from `sessionWorker` as a "dead field." This is correct for the current code (nothing reads it) but means **Issue D above will need to restore the field** when the LRU-eviction work lands. The field-removal is cleaner than carrying dead code; just note in Issue D's acceptance criteria that the LRU policy requires this metadata back.

This is a textbook example of "YAGNI now, restore later" — defensible at this point in the release. Not a finding.
