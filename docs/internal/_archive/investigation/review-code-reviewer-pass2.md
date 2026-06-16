# Code Review — feature/iframe-preview-tier13 (post-fix verification)

**Summary: Pass-1 resolved: 11/14 (3 PARTIAL, 0 NOT-FIXED that block). New findings: 2 (1 BLOCKING, 1 SHOULD-FIX).**

Scope: verified my 14 pass-1 findings against the current tree after commits `c27ff7f` (frontend), `132bb46` (tests), `b7f3e98` (agent hardening), `26bbf0e` (integration helpers). Also scanned the rewritten admission controller, new `replayMu`, and refactored steering/turn machinery for races and leaks.

---

## Pass-1 resolution status

### 1. loop.go admission bypass for follow-ups — **RESOLVED**
`pkg/agent/admission.go:46-66` — `TryAdmit(scope)` now tracks unique active scopes via `activeScopes map[string]struct{}`. One slot per session, held for the worker's lifetime via the `admissionRelease` closure stored on the worker (`session_worker.go:60-61, 144`). Follow-up turns hit `existing.(*sessionWorker).enqueue(msg)` at `loop.go:1576` and bypass admission *correctly* because the slot was already claimed at worker spawn time. The TOCTOU regression test in `admission_test.go:185-234` (100 goroutines @ cap=5) locks the no-overcommit guarantee.

### 2. websocket.go drain-disarm race — **PARTIAL** → see New Finding A (BLOCKING)
The fix added `wc.replayMu sync.RWMutex` and routes `Update` + `sendCancelStage` through `sendRawFrameBytes`. The drain path (`websocket.go:1151-1176`) holds `Lock()` for the entire drain+disarm. **But** `sendRawFrameBytes` releases `RUnlock()` *before* sending to `replayDivertCh` (line 1371), reintroducing a smaller version of the same TOCTOU. See New Finding A.

### 3. model-selector lowercasing — **RESOLVED**
`src/components/ui/model-selector.tsx:51-52` splits `queryRaw` (preserved case) from `queryLower` (filter-only). `queryRaw` is passed to `handleSelect` at line 155 and displayed at line 159. Locked by `model-selector.test.tsx:123-141` ("preserves case when user types a custom model slug").

### 4. system / unroutable goroutines unbounded — **PARTIAL**
`loop.go:1532-1546` (system) and `loop.go:1552-1571` (unroutable) now both have `defer recover()` blocks logging panics — good. **But** there's still no `sync.WaitGroup`, no admission gate, and `stopSessionWorkers` (line 1622) only waits on registered session workers. Under shutdown or DoS, these goroutines remain unbounded and uncoordinated. Acceptable for now because both paths are bounded by `runCtx` cancellation and recover prevents process crashes, but the original concern about unbounded goroutine spawn is not fully addressed.

### 5. docker_autodetect not in boot log — **RESOLVED**
`pkg/gateway/sandbox_apply.go:464-476` — `warnArgs` now appends `"disabled_by", disabledBy` to the `sandbox.permissive` slog.Warn call. Symmetrically, `/health` info map at line 533 carries `disabled_by` when set. The enforce-mode `sandbox.applied` line at 479 doesn't add it (correct — no disabled reason in that case).

### 6. session_worker inbox-full silent loss — **RESOLVED**
`session_worker.go:117-134` — on inbox-full drop, the worker publishes an OutboundMessage ("Your message could not be queued — the agent is busy. Please resend in a few seconds.") via `bus.PublishOutbound` with a 3-second timeout. No longer silent.

### 7. inTurn TOCTOU with steering enqueue — **RESOLVED (by single-writer invariant)**
The race was theoretical but the call-site analysis closes it: `enqueue` is only called from the dispatcher goroutine in `Run()` (`loop.go:1576, 1614`). For a given scope, calls to `enqueue` are therefore serial. The fallback at `session_worker.go:107-112` — if `enqueueSteeringFromMessage` returns an error (no active turn), fall back to inbox — handles the remaining narrow race where processTurn cleared `inTurn` between the dispatcher's `Load` and the steering enqueue. No message can be silently lost from this path.

### 8. websocket_replay_order_test not load-bearing — **RESOLVED**
`websocket_replay_order_test.go:50-193` now actually races: a goroutine fires `sendConnGenFrame` *during* `handleAttachSession`, and the test asserts `postDoneBeforeBuffered == false` (line 183). Reverting the fix would fail this assertion. Two additional tests at lines 205-231 and 245+ exercise flag-state and `-race`-instrumented concurrency.

### 9. integration replay tests asserting wrong invariant — **RESOLVED**
`tests/integration/replay_ordering_test.go:254-320` adds `TestReplayOrdering_LateLiveFrameDuringDrain` which sends a live `sendMessage` immediately after `attach_session` and asserts no `replay_message` frame appears after the first live token. Plus `TestReplayOrdering_DisconnectReconnectDisconnectReconnect` cycles 4 times. The "wrong invariant" pair (`ToolCallStartBeforeResult`, `EarlierTurnBeforeLaterTurn`) remain — they were not renamed, but they're now augmented by the proper bug-5 coverage, not standing alone.

### 10. `cap` shadowing built-in — **RESOLVED**
`pkg/agent/admission.go:20, 27-29` — renamed to `softCap` throughout. golangci `predeclared` will pass.

### 11. Docker detection no Podman/OCI handling — **NOT-FIXED (nit; intentional)**
`sandbox_apply.go:179-198` still checks only `/.dockerenv` and `OMNIPUS_IN_DOCKER`. The function gained better error logging (line 193-196) for EACCES/EPERM cases, but `/run/.containerenv` (Podman) and `KUBERNETES_SERVICE_HOST` (k8s) are not probed. The test file at `tests/integration/docker_exec_test.go::TestIsRunningInDocker_KubernetesNoDockerenv` explicitly documents the gap. Nit-level — leave for v0.2.

### 12. concurrent-sessions.yaml decorative — **RESOLVED**
`evals/scenarios/capability/concurrent-sessions.yaml` removed (per commit `132bb46`); regression covered instead by `tests/integration/concurrent_sessions_test.go::TestConcurrentSessions_TwoSessions_TimingProof` and `..._FiveSessions_SameAgent_TimingProof`.

### 13. `//go:build !cgo` constraint on replay test — **NOT-FIXED (nit)**
`pkg/gateway/websocket_replay_order_test.go:1` still has `//go:build !cgo`. The bug-5 unit test should run under both CGO settings. Leave for follow-up.

### 14. `completeOnboarding` dead mock — **RESOLVED**
`src/routes/onboarding.test.tsx` — mock factory entry, named import, and `mockResolvedValue` all removed. Only a clarifying comment at line 11 references the (correct) `completeOnboardingTransaction`.

---

## New findings

### A. `pkg/gateway/websocket.go:1371` — `sendRawFrameBytes` releases RLock before the divert send (BLOCKING)

The replay-divert fix attempted to close my pass-1 Finding #2 by holding `replayMu.RLock()` across the "read flag + select channel + send" sequence. The code (and its comment block at lines 1346-1355 + 1363-1364) claim:

> "the channel-selection decision (read isReplayingLive + pick targetCh) and the channel send are performed while holding wc.replayMu.RLock()"

The code does **not** match the comment. At line 1371 `wc.replayMu.RUnlock()` is called *before* the `select { case targetCh <- data: ... }` at lines 1374-1421. The original TOCTOU race I described in pass-1 Finding #2 is partially still open:

1. Writer A: `RLock()`, reads `isReplayingLive == true`, captures `targetCh := wc.replayDivertCh`, `RUnlock()`.
2. Writer A is descheduled before the channel send.
3. Drain: `Lock()` (no RLocks held), drains `replayDivertCh` (empty), `isReplayingLive.Store(false)`, `Unlock()`.
4. Writer A resumes, sends `data` to `targetCh` (the now-abandoned `replayDivertCh`).
5. `replayDivertCh` is never drained again for the connection's lifetime — frame orphaned, client loses a token.

The race window is narrower than pre-fix (the re-check under RLock at line 1368 eliminates the case where the writer saw a stale-true flag), but the post-RUnlock window is exactly the same shape and any descheduling between line 1371 and the send re-opens the race.

**Fix:** Move `wc.replayMu.RUnlock()` to *after* the channel send (or to the same `defer` that runs on every exit path). Concretely: change the structure to

```go
wc.replayMu.RLock()
defer wc.replayMu.RUnlock()  // held across the send
if !wc.isReplayingLive.Load() || wc.replayDivertCh == nil {
    return sendToSendCh(...)  // fall through, lock-free path
}
targetCh := wc.replayDivertCh
// send to targetCh while still holding RLock
...
```

Note that the drain at `websocket.go:1151-1176` already holds `Lock()` for its full duration including the per-frame `case wc.sendCh <- raw` send. Mirror that on the writer side.

This MUST be fixed before merge — the unit test at `websocket_replay_order_test.go:50-193` does not exercise this specific window (the post-RUnlock orphan-send variant) and so it passes on the buggy code. The test is load-bearing for the OUTER ordering invariant (drain frames before post-disarm frames), but not for the orphan-send variant.

### B. `pkg/agent/admission.go:46-66` — `TryAdmit` release closure can free a wrong slot if scope is re-spawned (SHOULD-FIX)

The release closure captures `scope` and calls `delete(a.activeScopes, scope)`:

```go
release := func() {
    a.mu.Lock()
    delete(a.activeScopes, scope)
    a.mu.Unlock()
}
```

The deferred-order in `sessionWorker.runLoop` is `admissionRelease() → sessionWorkers.Delete(scope) → close(done)` (LIFO of the three defers). That ordering means: when worker W1 for scope S exits, it (1) frees the admission slot, (2) removes itself from the map, (3) signals done. There is a narrow window between (1) and (2) where the dispatcher's `Run` could see scope S still in `sessionWorkers` (so it skips `TryAdmit`) AND, more interestingly, between when W1 calls `admissionRelease` and when a *concurrent* dispatcher iteration begins a fresh `TryAdmit(S)`. The fresh `TryAdmit` would succeed (slot is now free) and create a new release closure that — if both releases fire — could pull the new worker's slot out from under it.

In practice the dispatcher is single-goroutine so this is theoretical, but to keep `TryAdmit` correct under future multi-dispatcher refactors, the release closure should idempotently no-op after first use, e.g. via `sync.Once`:

```go
var once sync.Once
release := func() {
    once.Do(func() {
        a.mu.Lock()
        delete(a.activeScopes, scope)
        a.mu.Unlock()
    })
}
```

This is defensive — there's no failing test today, and the dispatcher is serial. SHOULD-FIX, not blocking.

---

## Approval

**BLOCKED-ON-A.** Finding A is a real race that the implementing agent's own test does not catch. The fix is a 2-line edit (move `RUnlock` to defer-position, or to after the send). After that's applied, this branch is mergeable from the code-reviewer perspective.

Pass-1 findings 4, 11, 13 are PARTIAL/NOT-FIXED but acceptable to defer:
- #4 has recover() guards that prevent panics; full bounded-goroutine treatment can wait for v0.2.
- #11, #13 are nits that don't affect correctness.

All other 11 pass-1 findings are properly resolved with regression tests where the fix calls for them.
