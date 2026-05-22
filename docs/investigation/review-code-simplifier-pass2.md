# Code Simplifier Review — Pass 2 — feature/iframe-preview-tier13

Second-pass audit. Verifies the 6 pass-1 cleanups landed cleanly, then scans the new admission/worker/replay code for any over-engineering introduced by the fix commits.

**Headline.** All 6 pass-1 items are resolved. The pass-1 hypothesis that the new code may have over-engineered the admission map or the replay RW-mutex turned out to be wrong — both are correctly sized. One small `if/else` anti-pattern survives in `sessionWorker.enqueue`, and one test (`TestSessionWorker_RecoverPanic`) is mislabeled because it does not actually inject a panic. Total new removable LOC: ~10.

---

## Pass-1 resolution table

| # | Pass-1 finding | File | Status | Evidence |
|---|---|---|---|---|
| 1 | Drop `lastActive` field + stores | `pkg/agent/session_worker.go` | RESOLVED | `grep lastActive pkg/agent/session_worker.go` returns no hits; struct now lines 33–67, no atomic Pointer field |
| 2 | Drop `agentID` field + parameter | `pkg/agent/session_worker.go` + 4 call sites | RESOLVED | `grep agentID pkg/agent/session_worker.go` returns no hits; constructor signature is now `newSessionWorker(scope, parent, admissionRelease)`; `steering_test.go:403-404` and `session_worker_test.go:105,336,373,410` updated |
| 3 | Drop dead `scope` assignment in `enqueueSteeringFromMessage` | `pkg/agent/steering.go` | RESOLVED | `pkg/agent/steering.go:193-206` now goes straight from `resolveMessageRoute` to `enqueueSteeringMessage(route.SessionKey, ...)`; the dead 4-line computation is gone |
| 4 | Drop defensive `w.parent != nil` check | `pkg/agent/session_worker.go:107` | RESOLVED | Current line is `if err := w.parent.enqueueSteeringFromMessage(msg); err == nil` — direct deref, consistent with the rest of `runLoop` |
| 5 | Typo "stuck stuck" | `pkg/agent/session_worker.go:198` | RESOLVED | Now reads `// Cleared even on panic so the worker doesn't get stuck in-turn.` |
| 7 | Variable rename `cap` → `softCap` | `pkg/agent/admission.go:27-30` | RESOLVED | Builtin no longer shadowed; reassigns parameter directly |

Pass-1 also flagged items 6, 8, 9, 10, 11 as deliberately-left-alone; those are still left alone (no regression).

---

## Answers to the four pass-2 questions

### Q1. Are all 6 pass-1 items resolved?

Yes. See the table above. Verified by `grep` against the live tree.

### Q2. Could `AdmissionController` use `map[string]struct{}` + plain `sync.Mutex` instead of `sync.Map`?

**Premise is incorrect — it already does.** `pkg/agent/admission.go:19-23`:

```go
type AdmissionController struct {
    softCap      int
    mu           sync.Mutex
    activeScopes map[string]struct{}
}
```

No `sync.Map` anywhere in `admission.go`. The struct uses exactly the shape the question proposed. Admission ops are also genuinely infrequent (one Lock per scope-spawn — not per turn, not per token), so the lock is not on any hot path. Leave as-is.

The only `sync.Map` introduced by this branch is `AgentLoop.sessionWorkers` (`pkg/agent/loop.go:218`) and that one is justified: `Run()` calls `sessionWorkers.Load(scope)` on **every inbound message** (hot path), and `runLoop` calls `sessionWorkers.Delete(w.scope)` on worker exit. The read-mostly pattern is exactly what `sync.Map` is documented for, and the alternative (`map + RWMutex`) would either need an RLock on the read path or a Lock-upgrade dance on the spawn path. Stay with `sync.Map`.

### Q3. Could `wsConn.replayMu` be a plain `sync.Mutex` instead of `sync.RWMutex`?

No — the RW distinction is load-bearing.

`sendRawFrameBytes` (`pkg/gateway/websocket.go:1356-1432`) is called by every frame-write path the gateway has: the agent loop's per-token `wsStreamer.Update`, every `sendConnGenFrame` (tool calls, status, errors), and every replay-emit. During a live agent turn the call rate is on the order of 50–500 calls/s (one per streamed token). All of those callers acquire `RLock` simultaneously while replay is active. Switching to plain `Mutex` would serialize every concurrent token writer through one critical section the moment any reconnect attaches mid-turn.

The drain in `handleAttachSession` (`pkg/gateway/websocket.go:1151-1176`) is the only `Lock` caller, and it runs exactly once per attach. Standard RW-mutex shape: many readers, one writer per phase.

Additionally, on the fast path (`!isReplayingLive.Load()`) `sendRawFrameBytes` doesn't acquire `replayMu` at all — line 1366 returns through the lock-free path. So `RWMutex` is only paid during replay, which is rare. The cost of the RWMutex over plain Mutex is exactly the case it's optimizing.

Leave as-is.

### Q4. Test helpers and tests-of-tests

**Helper `newConcurrentTestAgentLoop`** (`session_worker_test.go:20-36`, 16 lines): used by 6 tests, all of which need the same `tmpDir + Defaults{Workspace,ModelName,MaxTokens,MaxToolIterations}` config + bus + `mockProvider`. Inlining would add ~12 LOC per call site and duplicate the config-shape across 6 tests. Keep.

**Helper `makeSessionMsg`** (`session_worker_test.go:40-48`, 9 lines): used by 5 tests, all of which want the same web-channel + chat-id/sender-id-derived-from-session-id pattern. Removing it would force callers to spell out 6 fields each time. Keep.

**`TestSessionWorker_AdmissionRejection`** (`session_worker_test.go:244-322`): correctly does NOT use `newConcurrentTestAgentLoop` because it needs the blocking provider, not the standard `mockProvider`. The 14 lines of config setup at lines 246-256 are duplicate but unavoidable without extracting a `newTestAgentLoopWithProvider` helper. Not worth a refactor for one call site.

**`blockingMockProvider`** (`session_worker_test.go:438-461`, 24 lines): one type, one implementation, used by exactly one test. Could be inlined as a function-typed struct literal but `LLMProvider` is a 2-method interface — inlining would need both methods and end up the same length. Keep.

**`TestSessionWorker_RecoverPanic`** (`session_worker_test.go:395-433`): **mislabeled** — the comment at lines 401-406 admits the test does NOT actually inject a panic, it just tests the cancel path. Almost entirely duplicates `TestSessionWorker_CancelExits` (`session_worker_test.go:98-135`); the only thing it tests *additionally* is that `admissionRelease` is called on exit (lines 408, 430-432).

**Proposed fix:** either (a) rename it to `TestSessionWorker_AdmissionReleaseCalledOnExit` and reduce its scope to that one assertion, or (b) delete the `sessionWorkers.Load` and `<-w.done` blocks already covered by `TestSessionWorker_CancelExits`. The current 39-line test could be ~12 lines if narrowed to its one unique assertion.

---

## New simplification candidates

| # | File:line | Issue | Proposed LOC savings |
|---|---|---|---|
| A | `pkg/agent/session_worker.go:107-112` | `if err == nil { return } else { log }` — golangci-lint `indent-error-flow` anti-pattern. Flatten to: `if err := w.parent.enqueueSteeringFromMessage(msg); err != nil { logger.Debug(...) } else { return }` — or invert: check `err != nil`, log, fallthrough; check `err == nil`, return. | 2 lines |
| B | `pkg/agent/session_worker_test.go:395-433` | `TestSessionWorker_RecoverPanic` is mislabeled; comment admits it tests the cancel-exit path. Either rename + narrow to the unique `admissionRelease` assertion, or fold the assertion into `TestSessionWorker_CancelExits`. | ~8 lines |
| **Total** | | | **~10 lines** |

### A. `sessionWorker.enqueue` — `if/else` after return

`pkg/agent/session_worker.go:107-112`:

```go
if err := w.parent.enqueueSteeringFromMessage(msg); err == nil {
    return
} else {
    logger.DebugCF("agent.worker", "Steering enqueue rejected — falling back to inbox",
        map[string]any{"scope": w.scope, "error": err.Error()})
}
```

The `else` after `return` is dead structurally — control either returns or falls through to the inbox `select`. Recommended shape:

```go
if err := w.parent.enqueueSteeringFromMessage(msg); err != nil {
    logger.DebugCF("agent.worker", "Steering enqueue rejected — falling back to inbox",
        map[string]any{"scope": w.scope, "error": err.Error()})
} else {
    return
}
```

Or, cleaner with a named bool intent:

```go
err := w.parent.enqueueSteeringFromMessage(msg)
if err == nil {
    return
}
logger.DebugCF("agent.worker", "Steering enqueue rejected — falling back to inbox",
    map[string]any{"scope": w.scope, "error": err.Error()})
```

The second form is what `revive`'s `indent-error-flow` rule expects and matches the rest of the file's `if err != nil { log; return/fallthrough }` style. ~2 lines saved + lint fix.

### B. `TestSessionWorker_RecoverPanic` — rename or fold

The function's own docstring (`session_worker_test.go:401-406`) reads:

> Build a worker whose processTurn will not be called (no message injected). We verify the recover() guard on the cancel path… A direct panic injection would require mocking a large call path; the cancel path validates that all deferred cleanups… run regardless of internal state.

This is honest about not testing what the name says. Two options:

1. **Narrow + rename:** keep only the `released` / `admissionRelease was not called` assertion (the one thing `TestSessionWorker_CancelExits` doesn't cover) and rename to `TestSessionWorker_AdmissionReleaseFiresOnExit`. Drops ~8 LOC.
2. **Fold:** add the `released bool / releaseFunc` capture to `TestSessionWorker_CancelExits` so that test asserts both removal-from-map *and* release-called. Drops the whole function (~33 LOC).

Option 1 keeps the test as its own named unit; option 2 keeps one function. Either way the test name is no longer false advertising.

A third option is to actually inject a panic — e.g. wrap `parent.processMessage` in a panicking decorator or add a `panicProvider` mock. That would justify the name and add genuine coverage of the panic-recovery path. ~15 LOC to do that properly; tracked as nice-to-have, not blocking.

---

## Deliberately complex but correct

These look heavy at first glance but are correctly sized for what they do.

- **`AdmissionController.TryAdmit` returning `(bool, func())`.** Tempting to return just `bool` and have callers compute their own release closure. Bad idea: the release closure has to (a) acquire `a.mu`, (b) `delete(a.activeScopes, scope)` *and* (c) be a no-op when the scope was an existing follow-up. Centralizing the close-over-scope-and-mutex in `TryAdmit` is the only way to keep all three invariants in one place. Keep the closure-return shape.

- **`AdmissionController.SoftCap()` and `ActiveScopes()` getters.** Pass-1 flagged these as candidates for inlining; pass-2 confirms they are still load-bearing because the log statement at `loop.go:1590-1591` is the only caller outside tests. Removing them means writing `int64(a.softCap)` and `len(a.activeScopes)` inline at the log site — but `activeScopes` is unexported and requires `a.mu` to be held, so a direct `len()` from outside the package would be a data race. The getter encapsulates the lock. Keep.

- **`wsConn.replayMu` as `sync.RWMutex`.** See Q3 above. Many concurrent token-writers + one drain-writer is the canonical RW case.

- **Two-pass drain in `sessionWorker.processTurn`.** `processTurn` has both a "drain steering during turn" loop (lines 256-284) and the final publish (286-289). The two phases handle different states (queued-during-turn vs final-response). Collapsing them would require either always-Continue (wrong: skips the final publish path) or always-publish-then-continue (wrong: publishes incomplete state). Keep both phases.

- **`workerInboxCap = 8` and `workerIdleTimeout = 60s` as package-level identifiers.** Pass-1 already flagged this as fine; pass-2 confirms — `session_worker_test.go:365-367` mutates `workerIdleTimeout` in place for the idle-exit test (`TestSessionWorker_IdleExits`), which works only because it's a `var` not a `const`. Don't switch it to `const`.

- **`blockingMockProvider` in `session_worker_test.go:438-461`.** Could be replaced with a generic configurable mock, but no other test currently needs a blocking provider, and refactoring would create speculative shared-mock complexity. Keep local.

- **Pass-1's leave-alones (#6, #8, #9, #10, #11).** Still leave alone. The branching in `model-selector.tsx` mirrors the three real UI states; `isRunningInDocker` test injection is the only race-safe shape; `processTurn`'s stacked defers are intentionally layered for crash semantics; `Run()` dispatcher is at the right cognitive size.

---

## Summary

| Category | LOC |
|---|---|
| Pass-1 cleanups (already landed) | ~25 |
| Pass-2 new candidates (A + B) | ~10 |
| **Total opportunity on this branch** | **~35** |

The new admission/worker/replay code is in good shape. Pass-2 found one real anti-pattern (`if/else` after return in `enqueue`) worth fixing for lint cleanliness, and one mislabeled test worth narrowing or folding. Neither is blocking; both are nice-to-have polish.

The pass-2 hypothesis that the admission map or replay mutex were over-engineered turned out to be wrong — both are correctly sized for their access patterns. The only structural mismatch is the test name vs test behaviour for `TestSessionWorker_RecoverPanic`.

---

## Files touched in this review (read-only)

Absolute paths:

- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/admission.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/admission_test.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/session_worker.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/session_worker_test.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/steering.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/steering_test.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/loop.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/test_helpers_test.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/gateway/websocket.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/gateway/websocket_replay_order_test.go`
