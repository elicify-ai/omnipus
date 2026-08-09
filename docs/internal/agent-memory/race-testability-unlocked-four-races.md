---
name: race-testability-unlocked-four-races
description: Making pkg/gateway race-testable exposed 4 real production data races that had been invisible for months
metadata: 
  node_type: memory
  type: project
  originSessionId: 9a5cc9d5-94c8-4246-b11e-938e082e3387
  modified: 2026-07-29T10:42:55.438Z
---

On `feature/plan-swimlane-board` (2026-07-29), commit `8a399b46` removed a blanket
`//go:build !cgo` tag from 312 files and `7c043e60` added `pkg/gateway` + `tests/integration`
to the CI race job. **The race detector had never run on `pkg/gateway`.** That single
infrastructure change is what surfaced every race below — all pre-existing, none new.

Four real production races found:

1. **`pkg/channels/manager.go` `Reload`** closed a removed channel's worker queue without
   waiting for in-flight dispatchers → send-on-closed-channel **panic**. Twin of the
   `StopAll` bug fixed in `f5cfa241`. The trap: `dispatchLoop` takes `m.mu.RLock`, so
   waiting under `Reload`'s write lock deadlocks — the fix releases and reacquires, and is
   only safe because the gateway's single reload-consumer loop serializes Reload/StopAll.
2. **`runWorker` read `m.config` unlocked** against `Reload`'s locked write. Fixed by
   passing a config snapshot into `runWorker` (all 4 spawn sites already hold the lock),
   NOT by adding `RLock` inside — that would deadlock against Reload's wait-for-exit.
3. **`pkg/logger/logger.go::logMessage` read the `logger`/`fileLogger` globals with no
   `mu.RLock()`** while four mutators wrote under `mu.Lock()`. On the path of EVERY log
   call in the binary. Untouched since `4529925f` (2026-06-16). Fix = RLock, copy both
   values, RUnlock **before** any zerolog I/O (holding across the write would block every
   logger during a slow file write). Kept RWMutex over atomics because `fileLogger` and
   `logFile` must swap as one unit or a reader sees a torn state.
4. **Concurrent fast-upserts re-wiring a shared agent's `DelegateTool`** (found while
   fixing #571) — `SetSteeringSink` is a bare field write. Closed with `fastUpsertMu`.

**The diagnostic lesson that cost the most time.** Race #3 presented as a flaky test:
`TestReplay_ToolCallPairsEmitted` "fails contended, passes isolated." It was investigated
as a test bug across multiple sessions. The tell: the race detector fails the **whole
package** regardless of individual assertions, so the output is *every test printing
`--- PASS`, then a bare trailing `FAIL` with no `--- FAIL: TestX` line to attribute it to*.
If you see that signature, look for a race in a shared global, not at the named test.
The test itself was correct and was never modified.

Related: [[mechanism-not-property-defect-class]], [[ci-worker-deployed-script-drift]].
