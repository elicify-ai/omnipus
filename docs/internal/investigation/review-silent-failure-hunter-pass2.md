# Silent Failure Audit — Pass 2 — `feature/iframe-preview-tier13`

**Summary:** 9 original findings — 7 RESOLVED, 2 PARTIAL, 0 NOT-FIXED. 4 NEW silent failures discovered in the fix code itself (1 user-visible regression, 2 race-condition drops, 1 nit). The work materially improves the situation but introduces a new silent-drop on the worker idle-exit race and one cosmetic dead code drop.

Scope: re-verified all 9 findings from `review-silent-failure-hunter.md` against the post-fix code in commits `c27ff7f`, `132bb46`, `b7f3e98`. Then audited the new code paths called out in the prompt (`admission.go::TryAdmit`/`release`, `session_worker.go` defer ordering, `loop.go` unroutable goroutine, `websocket.go` replayMu drain deadline, `steering.go` interrupt goroutines).

---

## VERIFICATION — original findings

### #1 Unroutable goroutine discards error reply — **RESOLVED**

`pkg/agent/loop.go:1552-1570`. Goroutine now captures `(response, ag, err)` from `processMessage`, synthesises an `"Error processing message: %v"` string when `err != nil && response == ""`, and calls `al.publishResponseIfNeeded`. Wrapped in `defer recover()` with structured stack. Matches the recommended fix verbatim, including the channel/chat_id panic-log context.

Residual nit (see new finding **N4** below): when `err != nil && response != ""` (partial response with error), the error is silently discarded — only the response is published. Pre-existing semantics, not a regression.

### #2 `processTurn` publish failure bypasses fallback guard — **PARTIAL**

`pkg/agent/session_worker.go:222-289`. The ordering was changed: `published = true` is now set **after** `publishResponseIfNeeded` returns (lines 246-247, 287-288). Improvement: a *panic* inside the publish path now leaves `published == false`, the inner recover at 216-221 absorbs it, and the outer deferred-guard re-publishes via line 223 — closing the panic case.

**Still missing:** `publishResponseIfNeeded` (loop.go:1657-1698) returns `nothing`. A non-panic publish error (`bus.PublishOutbound` failure — closed-channel hook, ctx-cancelled mid-shutdown, channel adapter persistence error) logs `ErrorCF` and returns silently. The worker then runs `published = true` unconditionally and the deferred-guard never re-arms. The user gets one log line and zero response.

**To fully resolve:** change `publishResponseIfNeeded` to return `error`, then in the worker do `if err := al.publishResponseIfNeeded(...); err == nil { published = true }`. The deferred fallback is otherwise a paper tiger for any non-panic failure mode.

### #3 `enqueueSteeringFromMessage` swallows error — **RESOLVED**

`pkg/agent/session_worker.go:107-112`. The fallback now logs `DebugCF("agent.worker", "Steering enqueue rejected — falling back to inbox", ...)` with scope and error. Operator can now correlate the two log lines.

### #4 Inbox-full drop is silent to user — **RESOLVED** (with caveat → N1)

`pkg/agent/session_worker.go:117-134`. Drop now publishes a user-visible reply ("Your message could not be queued — the agent is busy. Please resend in a few seconds.") via `bus.PublishOutbound`. Symmetric with the at-capacity reply in `Run` at loop.go:1597-1604. See **N1** for the new secondary silent failure this introduces.

### #5 Worker `runLoop` panic recovery — **RESOLVED**

`pkg/agent/session_worker.go:146-155`. `defer func() { if r := recover(); r != nil { logger.ErrorCF(... "stack": string(debug.Stack()) ...) } }()`. Recovers correctly: defer ordering is `close(done)` → `sessionWorkers.Delete` → `admissionRelease` → `recover` (LIFO ⇒ recover runs FIRST, so panic is absorbed before the other defers fire). Worker exits cleanly, slot released, scope deleted.

### #6 `Run()` system + unroutable goroutine panic recovery — **RESOLVED**

`pkg/agent/loop.go:1532-1544` (system) and `1552-1570` (unroutable). Both have `defer recover` with channel/chat_id panic context.

### #7 `recordSyntheticDeny` blank-identifier discard — **NOT-FIXED**

All 3 callsites (loop.go:3600, 4450, 4489) still do `if shouldAbort, _ := al.recordSyntheticDeny(ts); shouldAbort`. The unused `abortMsg` return remains in the function signature (line 5085). The original finding rated this MEDIUM/no-user-impact-today — a future-maintainer foot-gun. Still applies. Trivially fixable: remove the second return value (caller never uses it).

### #8 `isRunningInDocker` swallows EACCES — **RESOLVED**

`pkg/gateway/sandbox_apply.go:187-198`. The `else if !os.IsNotExist(err)` branch logs a `slog.Warn` with the error and the recovery hint `"set OMNIPUS_IN_DOCKER=1 if running inside a container"`. Matches the recommended fix.

Side note: this is correct but ironic — the warn fires once at boot, and an operator who actually hit this would still need to read the log to discover why their sandbox went to `enforce`. The fix is correct as far as it goes; for cleanest UX the boot stderr could surface the mismatch alongside the existing sandbox banner. Out of scope for this review.

### #9 InterruptSession cascade panic recovery — **RESOLVED**

`pkg/agent/steering.go:462-467` (InterruptSession) and `521-526` (InterruptSessionHard). Each goroutine has both `defer wg.Done()` AND `defer func() { recover() }()`. Defer LIFO: recover runs first (absorbs panic), then wg.Done. `wg.Wait()` in the caller is never starved.

---

## NEW silent failures introduced or missed

### N1. Worker idle-exit race — message lost into a dying worker's inbox

**File:** `pkg/agent/session_worker.go:162-186` interacting with `pkg/agent/loop.go:1574-1577`.
**Severity:** HIGH — user-visible.

Race window:

1. Worker has been idle. `idleTimer.C` fires.
2. `select` at line 163 picks the timeout case; `return` triggers.
3. **Before** the deferred `sessionWorkers.Delete(scope)` runs, dispatcher in `Run()` (loop.go:1575) executes `existing, ok := al.sessionWorkers.Load(scope)` — finds the dying worker (delete defer hasn't fired yet).
4. Dispatcher calls `existing.(*sessionWorker).enqueue(msg)` (loop.go:1576).
5. `enqueue` sees `inTurn == false`, lands the msg in `w.inbox` (capacity > 0, non-blocking).
6. Worker's `runLoop` is already past the select; defers run; goroutine exits.
7. Msg sits in the closed-but-not-actually-closed inbox forever. Nothing reads it.

No log, no reply, no transcript entry. User typed a message after the idle timeout fired and it vanishes.

**Hidden errors:** any inbound message arriving in the ~microsecond window between `idleTimer.C` firing and `sessionWorkers.Delete` executing. Probability is low per-message but the population is bursty (load tests with 60s+ pauses will hit this).

**User impact:** silent drop of one message; the user sends a follow-up assuming the agent is working on the first one.

**Fix:** Have `runLoop` drain `w.inbox` after the for-loop exits, e.g.:
```go
defer func() {
    for {
        select {
        case msg := <-w.inbox:
            // publish a "session resumed but had to restart" reply
            // OR re-dispatch through al.bus.PublishInbound
            ...
        default:
            return
        }
    }
}()
```
Alternatively, take an explicit `closing` atomic.Bool that `enqueue` checks before sending; if true, enqueue fails over to the bus's inbound path (re-dispatch through Run), which will spawn a new worker.

### N2. Inbox-full reply itself can silently fail — secondary drop

**File:** `pkg/agent/session_worker.go:126-133`.
**Severity:** MEDIUM — user-visible.

The fix for original #4 publishes a capacity-reply when the inbox is full. If `bus.PublishOutbound` returns an error (e.g. the channel adapter is itself busy / disconnected / context-deadlined), the code logs `WarnCF` and returns. The original user message is already dropped (line 117 path), and now the apology message is dropped too. Net effect: same silent failure the original #4 was supposed to fix, just two layers down.

**Hidden errors:** Discord/Telegram/Slack outbound hook returning an error during a load spike, ctx cancellation, persistence failure in the channel's outbound queue.

**User impact:** Same as #4 — user sees no acknowledgement at all.

**Fix:** Retry once with a longer deadline (e.g. 10s instead of 3s), then if it still fails, emit a `system` event into the audit log so the operator can correlate at least. The user can't be reached if the channel itself is failing, so audit-side observability is the best we can do.

### N3. `publishResponseIfNeeded` panic-on-typing-stop is silently absorbed by runLoop

**File:** `pkg/agent/session_worker.go:202-206`.
**Severity:** LOW — observability blind spot.

```go
defer func() {
    if al.channelManager != nil {
        al.channelManager.InvokeTypingStop(msg.Channel, msg.ChatID)
    }
}()
```

No local recover. If `InvokeTypingStop` panics (nil adapter, type-assertion failure, etc.), the panic propagates out of `processTurn` and is caught by `runLoop`'s outer recover (good — worker doesn't crash the process), but the log line attributes the panic to `"Panic in session worker runLoop — worker exiting"` instead of `"InvokeTypingStop panic"`. The worker also dies on this path — losing the session for an operation that should be best-effort.

**Hidden errors:** Nil channelManager during shutdown races; misregistered channel adapter; type assertion failures in adapter hot paths.

**User impact:** The session worker dies and the user must re-message to spawn a new one. A purely cosmetic typing-stop hiccup terminates the session.

**Fix:** Wrap the typing-stop call in its own `func() { defer recover() ... }()` so a typing panic logs at WARN and the worker continues.

### N4. Unroutable goroutine drops `err` when `response != ""`

**File:** `pkg/agent/loop.go:1563-1569`.
**Severity:** LOW — debugging hazard.

```go
response, ag, err := al.processMessage(runCtx, msg)
if err != nil && response == "" {
    response = fmt.Sprintf("Error processing message: %v", err)
}
if response != "" {
    al.publishResponseIfNeeded(runCtx, ag, msg.Channel, msg.ChatID, response)
}
```

When `err != nil && response != ""` — i.e. `processMessage` returned a partial response AND an error — the `err` is silently dropped. The user sees only the partial response; an operator can't tell from logs that an error occurred. `processMessage` can return this combination in `pkg/agent/loop.go::processMessage` after a failed but recoverable LLM call.

**Hidden errors:** Partial LLM completion before stream error; tool execution succeeded but post-processing failed; rate-limit hit mid-stream.

**Fix:** Add `if err != nil { logger.WarnCF("agent", "Unroutable processMessage error with partial response", map[string]any{"channel": msg.Channel, "error": err.Error()}) }` before the `if response != ""` block.

---

## Specific concerns from the audit prompt

### `TryAdmit` / `release` — double-release & no-release

**Double-release:** safe. `release` does `delete(activeScopes, scope)` under the mutex; `delete` on a missing key is a no-op. Calling release twice acquires the lock twice, does nothing the second time, returns. The function literal at admission.go:60-64 closes over `scope`, so there's no aliasing risk. No silent failure.

**Never called:** the only path is the worker's `defer w.admissionRelease()` at session_worker.go:144. The defer fires unconditionally as long as the goroutine starts. The risk window is between `TryAdmit` returning `release` (loop.go:1585) and `go w.runLoop()` (loop.go:1613). Three statements in between:
- `newSessionWorker(scope, al, release)` — panics only if parent is nil, which is statically impossible here.
- `al.sessionWorkers.Store(scope, w)` — sync.Map.Store does not panic.
- The goroutine creation itself — only fails on extreme OOM.

So the leak window is essentially nil for practical purposes. **Acceptable.**

**Same scope active twice:** if two dispatchers race to spawn workers for the same scope, `sessionWorkers.Store(scope, w)` (loop.go:1612) overwrites the first. The first worker is orphaned (no future enqueues, will idle-exit in 60s, and its admissionRelease IS still called when it exits). But — its `defer sessionWorkers.Delete(scope)` (session_worker.go:143) will delete the SECOND worker's entry from the map after it exits, because the map now points at the second worker. The second worker becomes unreachable via `sessionWorkers.Load`, every future message creates yet another worker, and admission slots leak until softCap is hit and the system grinds.

Wait — `TryAdmit(scope)` for the SECOND dispatcher would see the scope already in `activeScopes` (set by the first dispatcher) and return `(true, no-op-release)`. So no extra slot is consumed. But the orphan worker still holds its slot, and Delete-after-exit removes the wrong worker. **Real bug** but only triggerable if two messages for the same scope arrive in the dispatcher within the lock-free window between `sessionWorkers.Load` (line 1575) and `sessionWorkers.Store` (line 1612).

Since `Run()` reads from a single inbound channel sequentially in one goroutine, this race **can't actually happen** unless multiple `Run()` goroutines coexist. Code review of `AgentLoop` shows only one `Run` per loop. **Acceptable in practice; fragile in principle.** Consider `LoadOrStore` instead of `Load`+`Store` to make it explicit.

### Session worker defer ordering

Verified: `defer close(done)` → `defer Delete(scope)` → `defer admissionRelease()` → `defer recover()`. LIFO: recover first (panic absorbed in scope), then admissionRelease (safe — closure can't panic), then Delete (sync.Map can't panic), then close(done) last. `admissionRelease` panicking would NOT be re-recovered (recover already ran), but admissionRelease is a closure over `delete()` and a mutex — it cannot panic. **Safe.**

### Unroutable goroutine publish error logging

`publishResponseIfNeeded` (loop.go:1683-1691) logs `ErrorCF` on publish failure with channel + chat_id + error. **Sufficient.** The recover defer in the goroutine (loop.go:1553-1561) is registered correctly and includes channel/chat_id. **Verified.**

### `replayMu.RLock()` + 1s send deadline

The RLock holder in `sendRawFrameBytes` (websocket.go:1371-1430) reads the flag and snapshots `targetCh`, then **releases RLock at line 1377 BEFORE doing any time-consuming send**. RLock hold time is microseconds. The 1s send deadline in the drain (line 1157) is on `sendCh`, not on RLock. So an RLock holder cannot "get stuck >1s in the select" because the RLock is released before the select runs.

The only realistic stuck-Lock scenario is the drain itself: `replayMu.Lock()` is held across the entire drain loop. If `replayDivertCh` has N frames and each send to `sendCh` waits up to 1s, the drain could block for `N seconds`. During that time, all replay-mode writers wait on RLock. **By design.** The 1s per-frame deadline + N-bounded queue depth caps the worst case. **Acceptable.**

Critical frames (`"done"`, `"error"`, `"exec_approval_request"`, `"exec_approval_expired"`) take the fast path at line 1366 and never touch replayMu — so an approval frame can't be starved by a slow drain. **Correct.**

### Steering cascade defer ordering

Verified for both `InterruptSession` (steering.go:460-486) and `InterruptSessionHard` (steering.go:519-550). Each goroutine: `defer wg.Done()` registered first, then `defer recover()` registered second. LIFO ⇒ recover runs first (absorbs), wg.Done runs second (always increments). `wg.Wait()` is never starved by a panic.

---

## Recommended fix order (delta over original)

1. **N1** (worker idle-exit race) — HIGH user-visible. Drain inbox on runLoop exit, or use a `closing` flag to redirect to a fresh worker. This is the most material silent failure introduced by the new architecture.
2. **#2 PARTIAL** (publishResponseIfNeeded return-error) — convert to `error` return and check before setting `published = true`. Closes the only remaining hole in the FR-004 deferred-fallback guarantee.
3. **N2** (inbox-full apology drop) — retry-with-longer-deadline + audit emission.
4. **N3** (typing-stop defer recover) — wrap in its own recover so typing hiccups don't kill the worker.
5. **#7 NOT-FIXED** + **N4** — opportunistic cleanup. Neither has live user impact today.

Items #1, #3-#6, #8, #9 have been correctly addressed; no further action required on those.
