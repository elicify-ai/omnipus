# Silent Failure Audit — `feature/iframe-preview-tier13` Bug Fixes

**Scope:** Uncommitted + untracked changes on `feature/iframe-preview-tier13`. Specifically the 5 bug-fix surfaces:
`pkg/agent/session_worker.go`, `pkg/agent/admission.go`, `pkg/agent/loop.go` (Run dispatcher), `pkg/agent/steering.go` (InterruptSession cascade), `pkg/gateway/sandbox_apply.go::isRunningInDocker`, plus committed worker-related code on the branch.

**Summary line:** 9 silent failures found, 4 user-visible.

---

## Findings

### 1. Unroutable goroutine discards user-facing error reply
**File:** `pkg/agent/loop.go:1542-1544`
**Severity:** CRITICAL — user-visible
**Code:**
```go
go func() {
    _, _, _ = al.processMessage(runCtx, msg)
}()
```

`processMessage` returns `(response string, agent *AgentInstance, err error)`. When `resolveSteeringTarget` returns ok=false (no matching agent, no default agent), the goroutine path runs but discards all three return values. In the OLD Run() implementation, the `func(){...}()` closure captured `response` and `err`, set `finalResponse = "Error processing message: %v"`, and the deferred response guard published it. The new fallback path does NEITHER — the comment on lines 1539-1541 explicitly claims "channels with no configured agent still get a 'no agent' error reply", but that reply is never sent.

**Hidden errors:** `processMessage` returns `("", nil, "no agent available for route (agent_id=X)")` (loop.go:2974) on the unroutable path. The error is dropped; the user sees no acknowledgement at all.

**User impact:** A user sending a message to a channel with no configured agent (e.g. Discord with no Discord-side agent assignment) gets total silence. They cannot distinguish "the gateway is down" from "I'm not allowed to message this agent" from "the gateway is just slow." The comment lies about the behavior.

**Fix:**
```go
go func() {
    defer func() {
        if r := recover(); r != nil {
            logger.ErrorCF("agent", "panic in unroutable goroutine",
                map[string]any{"panic": r, "channel": msg.Channel, "chat_id": msg.ChatID})
        }
    }()
    response, ag, err := al.processMessage(runCtx, msg)
    if err != nil && response == "" {
        response = fmt.Sprintf("Error processing message: %v", err)
    }
    if response != "" {
        al.publishResponseIfNeeded(runCtx, ag, msg.Channel, msg.ChatID, response)
    }
}()
```

---

### 2. `processTurn` publish failure bypasses fallback guard
**File:** `pkg/agent/session_worker.go:229-235, 271-274`
**Severity:** HIGH — user-visible
**Code:**
```go
if target == nil {
    if finalResponse != "" {
        published = true                              // ← set BEFORE the call
        al.publishResponseIfNeeded(ctx, ...)          // ← swallows publish error
    }
    return
}
...
if finalResponse != "" {
    published = true                                  // ← set BEFORE the call
    al.publishResponseIfNeeded(ctx, ...)
}
```

`publishResponseIfNeeded` (loop.go:1623-1664) logs `ErrorCF` on `bus.PublishOutbound` failure and returns silently. The worker has already set `published = true`, so the deferred fallback guard at session_worker.go:196-206 (`if finalResponse != "" && !published`) is short-circuited. A publish failure mid-flow logs to gateway.log and produces no user reply, no retry, no transcript entry.

**Hidden errors:** Any bus error from `PublishOutbound` (closed channel, dead subscriber, context expiry, persistence failure inside the channel's outbound hook). The bus is in-process so this is rare, but channel adapters (Discord/Telegram/Slack) hook into outbound and can fail — those failures are NOT propagated back.

**User impact:** Agent computed a response, the LLM ran, tokens were spent, but the user sees nothing. The deferred fallback was specifically added (FR-004 per the comment) to prevent this — but the order of operations defeats it.

**Fix:** Set `published = true` only after `publishResponseIfNeeded` returns successfully. Refactor `publishResponseIfNeeded` to return an error so the worker can decide whether to re-arm the fallback.

---

### 3. `enqueueSteeringFromMessage` fallback swallows the steering error
**File:** `pkg/agent/session_worker.go:101-113`
**Severity:** MEDIUM — debugging hazard
**Code:**
```go
if w.inTurn.Load() {
    if w.parent != nil {
        if err := w.parent.enqueueSteeringFromMessage(msg); err == nil {
            return
        }
    }
}
select {
case w.inbox <- msg:
default:
    logger.WarnCF("agent.worker", "Session worker inbox full — dropping message", ...)
}
```

The error from `enqueueSteeringFromMessage` is silently discarded — only `enqueueSteeringMessage` inside steering.go logs `"Failed to enqueue steering message"`, but that log does NOT include the fallback context. An operator reading the log sees a steering-queue rejection but cannot tell whether the message was lost or fell back to the inbox path. If the fallback inbox is ALSO full (the next select line), the message is dropped with a separate WARN — but the relationship between the two events is invisible.

**Hidden errors:** route resolution failure (no agent), `steering queue is not initialized`, `pushScope` failure (queue full, scope mismatch). The doc comment claims "queue full, no active turn state, etc." but the caller has no way to distinguish these so it cannot apply different fallback strategies.

**User impact:** When the steering queue rejects mid-turn AND the inbox is full, the message is lost and the operator cannot reconstruct what happened from logs alone. Two unrelated WARN lines from different packages.

**Fix:**
```go
if w.inTurn.Load() && w.parent != nil {
    if err := w.parent.enqueueSteeringFromMessage(msg); err == nil {
        return
    } else {
        logger.DebugCF("agent.worker", "Steering enqueue rejected — falling back to inbox",
            map[string]any{"scope": w.scope, "error": err.Error()})
    }
}
```

---

### 4. Inbox-full drop is silent to user
**File:** `pkg/agent/session_worker.go:114-123`
**Severity:** HIGH — user-visible
**Code:**
```go
select {
case w.inbox <- msg:
default:
    logger.WarnCF("agent.worker", "Session worker inbox full — dropping message", ...)
}
```

When the per-session inbox (capacity 8) is full, the message is dropped with a WARN log. The user receives no acknowledgement — no "I'm overloaded" reply like the admission rejection at loop.go:1566. Inbox 8 is small; a user typing fast follow-ups while the agent is on a long tool loop will trip this. Drop is correct (blocking would deadlock the dispatcher), but the user MUST be told.

**Hidden errors:** Genuine overload, slow agent on a long tool chain, stuck agent that's not advancing.

**User impact:** User types 9 messages back-to-back during a long agent run. Messages 9+ vanish. The user sees a normal-looking conversation that's missing turns. Re-asking is the only recovery.

**Fix:** When dropping, publish a user-visible "your last message wasn't queued — please resend in a few seconds" reply via `al.bus.PublishOutbound`. Mirror the at-capacity reply at loop.go:1566-1573.

---

### 5. Worker `runLoop` has no top-level panic recovery
**File:** `pkg/agent/session_worker.go:130-168`
**Severity:** HIGH — observability blind spot
**Code:**
```go
func (w *sessionWorker) runLoop() {
    defer close(w.done)
    defer w.parent.sessionWorkers.Delete(w.scope)
    ...
    w.processTurn(ctx, msg)
}
```

`processTurn` has an inner `defer recover()` ONLY for the response-publish goroutine (line 197-202). The outer `processTurn` body — `processMessage`, `buildContinuationTarget`, `Continue`, `pendingSteeringCountForScope` — has no recover. A panic anywhere in those paths kills the worker goroutine. Because Go propagates unrecovered goroutine panics to crash the entire process, ONE buggy session can take down the gateway.

Per CLAUDE.md hard-constraint #7 ("fix everything, no excuses") and the existing pattern at loop.go:1531-1533 (which ALSO lacks recover but is now isolated to system messages), this MUST be guarded.

**Hidden errors:** Nil-pointer panics in tool wiring, map-iteration races (sync.Map mostly protects but the per-session bucket reads from `agent.Sessions` are not guarded), out-of-bounds in providers, etc.

**User impact:** ALL active sessions die. Gateway restart required. No clue in logs about which session caused it because the panic kills the writer before the log flushes (and there's no `defer logger.Flush()` on shutdown either).

**Fix:** Wrap runLoop body in `defer func() { if r := recover(); r != nil { logger.ErrorCF("agent.worker", "worker panic", map[string]any{"panic": r, "scope": w.scope, "stack": string(debug.Stack())}) } }()`.

---

### 6. `Run()` system-message and unroutable goroutines have no panic recovery
**File:** `pkg/agent/loop.go:1531-1535, 1542-1545`
**Severity:** HIGH — observability blind spot
**Code:**
```go
if msg.Channel == "system" {
    go func() {
        _, _ = al.processSystemMessage(runCtx, msg)
    }()
    continue
}
...
go func() {
    _, _, _ = al.processMessage(runCtx, msg)
}()
```

Same panic-propagation issue as #5. A system message that triggers a panic in `processSystemMessage` (e.g. a malformed audit payload, a nil channel manager dereference) crashes the gateway. The OLD code inside `func() { ... }()` had `defer func() { if r := recover(); r != nil { logger.ErrorCF("agent", "panic in deferred response guard", ...) } }()`. That guard is GONE from the new code path.

**Hidden errors:** Panics during system-event processing (memory-tier consolidation, audit, dreamcatcher); panics in resolveMessageRoute due to malformed config reloads.

**User impact:** Gateway-wide crash from a single rogue system message. No recovery without restart.

**Fix:** Add `defer recover()` to both goroutines, with structured logging including `msg.Channel`, `msg.ChatID`, and `debug.Stack()`.

---

### 7. `recordSyntheticDeny` return value silently dropped
**File:** `pkg/agent/loop.go:3566, 4416, 4455`
**Severity:** MEDIUM — correctness regression risk
**Code (3 sites):**
```go
if shouldAbort, _ := al.recordSyntheticDeny(ts); shouldAbort {
    turnStatus = TurnEndStatusAborted
    return al.abortTurn(ts)
}
```

The previous code captured `abortMsg` and appended it to the local `messages` slice before calling `abortTurn`. The new code drops `abortMsg`. The function signature still returns it (loop.go:5051), and `recordSyntheticDeny` still persists the same message to the session via `AddMessage` (line 5062). The drop is functionally benign in the abort case because `abortTurn` returns immediately and `messages` is never used again — BUT this is a foot-gun: a future maintainer might add LLM-call logic after `recordSyntheticDeny` (e.g. emit a "turn-aborted" notification to the channel) and reasonably expect the message to be in scope.

The unused-blank-identifier hides intent.

**Hidden errors:** None today, but any caller-side migration that needs the message string will silently break.

**User impact:** None today.

**Fix:** Either rename the function to not return the unused value, or comment the drop explicitly: `if shouldAbort, _ /* abortMsg already persisted via AddMessage */ := al.recordSyntheticDeny(ts); shouldAbort {`. Better: remove `abortMsg` from the return signature and update the doc comment (which still says "The caller is responsible for appending abortMsg to messages").

---

### 8. `isRunningInDocker` treats permission-denied `os.Stat` as "not in Docker"
**File:** `pkg/gateway/sandbox_apply.go:178-186`
**Severity:** MEDIUM — config-correctness hazard
**Code:**
```go
func isRunningInDocker(getEnv func(string) string) bool {
    if getEnv("OMNIPUS_IN_DOCKER") == "1" {
        return true
    }
    if _, err := os.Stat("/.dockerenv"); err == nil {
        return true
    }
    return false
}
```

`os.Stat` returns `(nil, err)` for any error — ENOENT (file doesn't exist), EACCES (permission denied), ENOTDIR, EIO. The current code treats all of these as "not in Docker." A hardened container that mounts `/` with restrictive AppArmor or a chroot that doesn't expose `/.dockerenv` will fall through to `ModeEnforce`, breaking exec inside what IS a container — the exact bug the auto-detect was added to fix.

**Hidden errors:** EACCES (AppArmor restricting open on `/`), EPERM (some LSMs), ELOOP (symlinked `/.dockerenv` with broken target), filesystem corruption.

**User impact:** A user running Omnipus in a hardened Docker setup (e.g. distroless + read-only root + restricted seccomp) sees `mode=enforce` chosen by the gateway, exec tools fail with permission denied, and they have no idea their `os.Stat` is being silently swallowed. They'll set `OMNIPUS_IN_DOCKER=1` manually only if they read the source.

**Fix:**
```go
if _, err := os.Stat("/.dockerenv"); err == nil {
    return true
} else if !os.IsNotExist(err) {
    slog.Warn("sandbox: /.dockerenv stat failed — defaulting to non-docker mode",
        "error", err,
        "hint", "set OMNIPUS_IN_DOCKER=1 if you are running inside a container")
}
return false
```

---

### 9. `InterruptSession` cascade goroutines have no panic recovery
**File:** `pkg/agent/steering.go:464-488, 514-535`
**Severity:** MEDIUM — observability blind spot
**Code:**
```go
for _, ts := range matches {
    wg.Add(1)
    go func() {
        defer wg.Done()
        ts.mu.Lock()
        pc := ts.providerCancel
        ts.mu.Unlock()
        if pc != nil {
            pc()
        }
        if ts.requestGracefulInterrupt(hint) { ... }
    }()
}
wg.Wait()
```

`pc()` is `context.CancelFunc` (no-panic by contract), but `ts.requestGracefulInterrupt` and `al.emitEvent` can panic on bad state (e.g. nil eventBus during shutdown). `wg.Done()` runs on defer; the panic then propagates out of the goroutine and crashes the process.

**Hidden errors:** Race between InterruptSession and Close() where the eventBus has been torn down; nil-pointer in emitEvent's hook chain.

**User impact:** Cancel button crashes the gateway.

**Fix:** Add `defer func() { if r := recover(); r != nil { slog.Error(...) } }()` inside each goroutine.

---

## Acceptable (intentional / correct)

### A1. Admission `OnTurnStart` / `OnTurnEnd` pairing
**File:** `pkg/agent/session_worker.go:209-210`
```go
al.admission.OnTurnStart()
defer al.admission.OnTurnEnd()
```
Adjacent statements with `defer` on the second; the only path between them is the OnTurnStart function call itself, which is atomic-add (admission.go:41). A panic between the two is essentially impossible. This is fine.

### A2. Worker idle-timer Stop/drain pattern
**File:** `pkg/agent/session_worker.go:157-163`
The `Stop` + non-blocking drain + `Reset` pattern is the canonical Go pattern. There IS a subtle race where `idleTimer.C` could fire between Stop's return and the message being processed, but the select at line 140 only re-checks the timer on the NEXT iteration after `processTurn` returns — `processTurn` has just reset the timer to a fresh 60s, so a stale Tick on `C` would have been drained by the `select { case <-idleTimer.C: default: }`. This is correct.

### A3. CancelStage frame drop on backpressure
**File:** `pkg/gateway/websocket.go:sendCancelStageFrame`
After 0/10ms/50ms retries, the frame is dropped at `slog.Debug` level. Acceptable because cancel-stage frames are informational (the cancel itself proceeds regardless of the frame's arrival), and dropping at WARN here would alert-flood on slow clients. The Debug level is appropriate. The cancel state machine itself does NOT depend on the frame being received.

### A4. `lazy CloseSession` panic recovery in WS handler
**File:** `pkg/gateway/websocket.go` (around the attach_session path on origin/main; partially refactored away in the diff)
The previous code had `defer func() { if r := recover(); r != nil { slog.Error("ws: lazy CloseSession panic recovered", ...) } }()` inside its goroutine. The new diff removes the lazy-CAS logic entirely (it moved into the contract-first migration). The removal is fine because the new code path doesn't spawn a goroutine for that work.

### A5. Inbox capacity = 8
The choice of 8 is justified in the comment ("rapid-fire follow-ups without blocking the dispatcher"). Higher capacity hides backpressure; lower wastes capacity. The drop behavior at 8+ is the right design — but it needs user-visible feedback (see finding #4).

### A6. `defer ts.Finish(false)` ordering in runTurn
**File:** `pkg/agent/loop.go:3398` (committed earlier on the branch, not in the active diff)
`defer ts.Finish(false)` registered BEFORE `defer al.clearActiveTurn(ts)` — LIFO means clearActiveTurn runs first, Finish second. The comment correctly identifies the idempotency contract (`closeOnce.Do` + `isFinished.Store(true)`). Safe.

---

## Recommended fix order

1. **#1 + #4** (user-visible silent drops) — these directly hide bugs from users and must be fixed for v0.1 release per hard-constraint #7.
2. **#2** (publish-failure short-circuits the deferred fallback) — undermines the explicit FR-004 design.
3. **#5 + #6 + #9** (missing panic recovery) — a single rogue session can crash the gateway; this is a regression from the pre-worker code that had a recover.
4. **#8** (Docker detect fallback) — fixable in a one-line `else if !os.IsNotExist(err)` log.
5. **#3 + #7** (debugging hazards) — fix opportunistically.
