# Code Simplifier Review — feature/iframe-preview-tier13

**Summary.** The 5 bug-fix files are mostly tight, but I found one piece of straight dead code (`sessionWorker.lastActive`), one unused parameter that propagates through three call sites (`sessionWorker.agentID`), one block of dead variable assignment in `enqueueSteeringFromMessage` (4 lines compute a scope that the next line overwrites), one defensive nil-check on a constructor invariant, two trivial wrapper methods on `AdmissionController`, and a small typo. The admission controller and `resolveMode`/`isRunningInDocker` are otherwise well-sized for what they do. Estimated removable LOC: ~25 lines + 1 typo, with no behaviour change.

---

## Findings

### 1. `pkg/agent/session_worker.go:56-59,92,154` — `lastActive` is write-only dead code

The `lastActive atomic.Pointer[time.Time]` field is **stored twice** (in `newSessionWorker` and on every inbox receive) and **never read** anywhere in the codebase (`grep lastActive` confirms 4 hits, all writes). The idle-timeout behaviour is enforced entirely by the `time.NewTimer(workerIdleTimeout)` + `idleTimer.Reset(workerIdleTimeout)` pattern at lines 134/163 — the timer is the real source of truth.

**Minimal diff:** Delete the field, its initialization in `newSessionWorker` (line 82-83 `now := time.Now()` + line 92 `w.lastActive.Store(&now)`), and the per-message store at lines 153-154. ~6 lines + 1 import cleanup if `atomic` is no longer used (it still is, for `inTurn`).

```go
// session_worker.go - delete:
// - lines 56-59 (field + comment)
// - line 82 (`now := time.Now()`)
// - line 92 (`w.lastActive.Store(&now)`)
// - lines 153-154 (`now := time.Now(); w.lastActive.Store(&now)`)
```

### 2. `pkg/agent/session_worker.go:40,79,84` — `sessionWorker.agentID` is never read

The `agentID` field on `sessionWorker` is captured at construction and stored in the struct, but a `grep` shows it is **never read** anywhere — not in `session_worker.go`, not in `loop.go`, not in tests. Three call sites pass it: `loop.go:1579`, `steering_test.go:405-406`, `session_worker_test.go:101,331`.

The comment at lines 38-40 claims it's preserved "so handoffs within the session start a new worker with the new agent ID" — but no code consults `w.agentID` to detect a handoff. Worker handoff in this codebase happens by registering a *new* scope key in `sessionWorkers`; the old worker's `agentID` field is irrelevant.

**Minimal diff:** Remove the field, drop the parameter from `newSessionWorker`, update the three call sites.

```go
// session_worker.go:
//   - Delete field (lines 38-40)
//   - newSessionWorker(scope string, parent *AgentLoop) *sessionWorker
//   - Drop agentID: scope in literal
// loop.go:1579: w := newSessionWorker(scope, al)  // drop agentID arg
// steering_test.go:405-406, session_worker_test.go:101,331: drop "default" arg
```

~8 lines removed, signature cleaner. The unused `agentID` parameter in `resolveSteeringTarget`'s return triple still has a real caller (`steering_test.go:395`) and reflects a real bit of routing data, so leave that signature.

### 3. `pkg/agent/steering.go:198-201` — dead variable assignment in `enqueueSteeringFromMessage`

```go
scope := resolveScopeKey(route, msg.SessionKey)
if msg.SessionID != "" {
    scope = scope + ":" + msg.SessionID
}
// The steering queue expects the SessionKey form ("agent:<id>:<sid>"),
// not the worker scope form — strip the SessionID suffix back off and use
// the route's session_key, which is what runTurn registered the active
// turn under via activeTurnStates.
turnScope := route.SessionKey
```

`scope` is computed and never used — the very next line replaces it with `route.SessionKey`. The 4 lines of computation + comment are entirely dead. The "strip the SessionID suffix back off" comment is misleading: there is no suffix to strip because the value was never threaded anywhere. Looks like an artifact of an earlier draft that built `scope` for a different purpose then got rewritten.

**Minimal diff:**

```go
func (al *AgentLoop) enqueueSteeringFromMessage(msg bus.InboundMessage) error {
    route, ag, err := al.resolveMessageRoute(msg)
    if err != nil || ag == nil {
        return fmt.Errorf("enqueueSteeringFromMessage: route resolution failed: %w", err)
    }
    pmsg := providers.Message{
        Role:    "user",
        Content: msg.Content,
        Media:   append([]string(nil), msg.Media...),
    }
    return al.enqueueSteeringMessage(route.SessionKey, ag.ID, pmsg)
}
```

~8 lines removed. Behaviour identical because the dead variable was never read.

### 4. `pkg/agent/session_worker.go:108` — defensive nil-check on a constructor invariant

```go
if w.parent != nil {
    if err := w.parent.enqueueSteeringFromMessage(msg); err == nil {
        return
    }
}
```

`w.parent` is set unconditionally in `newSessionWorker` (line 90) and has no setter — there is no path that leaves it nil. The check is dead defence, and inconsistent with `runLoop` at line 132 (`defer w.parent.sessionWorkers.Delete(w.scope)`) which deref's parent unconditionally and would panic if nil. Either both check or neither check; the runtime invariant is "neither, parent is always set."

**Minimal diff:** Remove the wrapping `if`:

```go
if w.inTurn.Load() {
    if err := w.parent.enqueueSteeringFromMessage(msg); err == nil {
        return
    }
}
```

~2 lines + 1 indent level.

### 5. `pkg/agent/session_worker.go:179` — typo "stuck stuck"

```go
// Cleared even on panic so the worker doesn't get stuck stuck-in-turn.
```

One-word typo. Fix to "stuck in-turn".

### 6. `pkg/agent/admission.go:50-58` — `ActiveTurns()` and `SoftCap()` are one-line getters used only in one log statement

`ActiveTurns()` and `SoftCap()` exist solely so `loop.go:1559-1560` can log structured values. Neither method appears anywhere else outside tests. They are pure wrappers around `a.activeTurns.Load()` and `a.softCap`. The encapsulation is OK if more callers are expected — but the comment "Used in tests and observability" describes the current state, not a planned interface.

**Decision: leave alone.** Two trivial getters cost ~6 lines and they make the log line readable. Removing them would only save lines at the cost of inlining `int64(a.softCap)` and `a.activeTurns.Load()` into one log call — that crosses into "leaky abstraction" territory once `softCap` becomes `int` (Go would require `int64(a.softCap)` at the call site). The current shape is fine; flagged only to document the deliberate non-removal.

### 7. `pkg/agent/admission.go:24-30` — local variable `cap` shadows builtin

```go
func newAdmissionController(softCap int) *AdmissionController {
    cap := softCap
    if cap <= 0 {
        cap = runtime.NumCPU() * 4
    }
    return &AdmissionController{softCap: cap}
}
```

`cap` shadows the Go builtin. Trivial readability fix:

```go
func newAdmissionController(softCap int) *AdmissionController {
    if softCap <= 0 {
        softCap = runtime.NumCPU() * 4
    }
    return &AdmissionController{softCap: softCap}
}
```

Same line count, no shadowing. ~1 net line saved (variable declaration).

### 8. `pkg/gateway/sandbox_apply.go:178-186` — `isRunningInDocker` is already minimal

The function uses an injected `getEnv func(string) string` so tests can set `OMNIPUS_IN_DOCKER=1` without polluting the process environment. The `os.Stat("/.dockerenv")` line is the actual production signal. The function-parameter seam adds one extra line but is the only reasonable way to keep the function testable without `t.Setenv`-style global state, which is widely avoided elsewhere in this repo for race-safety.

**Decision: leave alone.** The "complexity vs simple os.Stat" question is answered by the test injection — there is no simpler form that preserves testability. Two signals, ~9 lines, no abstractions.

### 9. `src/components/ui/model-selector.tsx:42-60` — branching grouping logic

The component computes:

- `allModels` — flattened model list, used only for `exactMatch` (line 55)
- `groupsWithModels` — `providerGroups.filter(g => g.models.length > 0)`
- `useGrouped` — `groupsWithModels.length >= 2`

And then on lines 124-130 has a second branch:

```jsx
(providerGroups && groupsWithModels.length === 1
  ? groupsWithModels[0].models
  : models
)
```

This handles "single provider supplied as a group" vs "no groups supplied — use flat models" — three states (≥2 groups grouped, 1 group flat, no groups flat) cleanly expressed.

**Decision: leave alone.** The branching mirrors the three real UI states (no groups / one group flat / multiple groups headered) and the test file `model-selector.test.tsx` exercises all three. Collapsing into a single render path would require always-render-with-heading or always-flatten, both of which change visible behaviour (the empty heading on a 1-provider list is what the test asserts NOT to render). The current shape is exactly what the three rendering states need.

### 10. `pkg/agent/loop.go:1500-1585` — Run() dispatcher is well-shaped

The new Run() body is ~85 lines and does six well-separated things: cancel-wrap, hook init, MCP init, then a `for { select { ... } }` with three message-classification arms (system / unroutable / scoped). The unroutable arm falls back to a fire-and-forget goroutine, matching pre-existing behaviour for channels with no agent.

**Decision: leave alone.** The "if existing worker, enqueue; else admit + spawn" sequence at lines 1549-1582 is genuinely sequential and reads top-to-bottom. The admission-rejection block (1554-1576) inlines a small amount of bus-publish code that could in principle move to a `publishCapacityRejection` helper, but it's used exactly once, takes 12 lines, and lives in the function that owns the decision — extracting it would be premature.

### 11. `pkg/agent/session_worker.go:174-275` — `processTurn` has two stacked deferred recover/publish blocks

`processTurn` defers (in order):
1. `w.inTurn.Store(false)` reset
2. `channelManager.InvokeTypingStop(...)` if present
3. An inner func with its own `recover()` that publishes `finalResponse` if not yet published
4. `al.admission.OnTurnEnd()`

The function is ~100 lines and intentionally mirrors the original Run() closure logic. Stacking is correct: typing-stop must fire even if the response publish panics; admission must release even if everything panics. Removing any layer changes failure semantics.

**Decision: leave alone.** This is exactly the kind of multi-layer defer the original Run() inline closure had, just extracted into a method. Simplifying further would require either inlining everything (defeats the per-worker isolation) or splitting into three methods (adds friction without saving cognitive load).

---

## Items reviewed and intentionally not flagged

- **`AdmissionController` as a struct vs free functions.** Counter has to be shared between dispatcher and worker; a struct with `OnTurnStart`/`OnTurnEnd` is the natural shape. Two getter methods (#6) are documented as deliberate.
- **`continuationTarget` struct in loop.go.** Three-field return that's reused in two call sites (build + use). Worth its 4 lines.
- **`stopSessionWorkers` two-pass collect-then-cancel pattern.** Avoids holding sync.Map's range lock while cancelling. Correct, not over-engineered.
- **`workerInboxCap = 8` and `workerIdleTimeout = 60s` as package consts.** Could be struct fields for testability but every test that needs different values constructs workers directly. Constants are fine.
- **`isRunningInDocker` taking a `getEnv` func.** Test-injection seam, not abstraction (#8).

---

## Estimated LOC reduction

| Finding | LOC removed |
|---|---|
| 1. Drop `lastActive` field + 2 stores | ~6 |
| 2. Drop `agentID` field + parameter at 4 call sites | ~8 |
| 3. Drop dead `scope` assignment in `enqueueSteeringFromMessage` | ~8 |
| 4. Drop defensive nil-check on `w.parent` | ~2 |
| 5. Typo fix | 0 (1 word) |
| 7. Variable rename `cap` → `softCap` | ~1 |
| **Total** | **~25 lines** |

No behaviour change. All flagged removals are either unused fields, dead assignments, or defensive checks on constructor invariants. Tests should continue to pass without modification (with the trivial edit of dropping the `"default"` arg from `newSessionWorker` calls in the two test files).
