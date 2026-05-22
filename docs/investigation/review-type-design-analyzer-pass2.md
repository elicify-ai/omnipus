# Type Design Review — Pass 2 — feature/iframe-preview-tier13

**Scope.** Verify the rewrites flagged in pass 1 actually resolve the type-design defects, and look for any new invariant or encapsulation issues introduced by the rewrites.

**TL;DR.** The AdmissionController rewrite is genuinely good — TOCTOU is closed, `release` is a token returned only on success, and the existing-scope short-circuit makes the cap a per-session-worker counter rather than a per-turn counter (correct mental model). `sessionWorker.admissionRelease` lifecycle invariant is enforced by `deferred runLoop cleanup + non-nil constructor precondition`. The `replayMu` is a guard-style invariant, not a type-level one, but is appropriately documented and exercised by tests. The remaining pass-1 cosmetic items were not addressed (out of scope of pass 2). One borderline issue from pass-1 (integration anonymous struct) is still present.

---

## Pass-1 resolution table

| Pass-1 finding | Severity | Pass-2 status | Notes |
|---|---|---|---|
| `AdmissionController` TOCTOU between `ShouldAdmit` + `OnTurnStart` | medium | **Resolved** | Replaced with `TryAdmit(scope) (bool, func())` — single mutex-guarded test-and-insert. `admission_test.go:185-234` `TestAdmissionController_TryAdmit_NoOvercommit` is the load-bearing regression test (100 goroutines vs cap=5, asserts admitted ≤ 5). |
| `AdmissionController` doc overpromises "concurrent session workers" — subagent spawn is not gated | medium (docs) | **Resolved** | `pkg/agent/admission.go:13-18` now reads: *"Phase 1: gates inbound user-message dispatch only … Subagent spawn and task-executor dispatch paths are NOT gated; see the v0.2 follow-up issue."* Verified by grep: only `loop.go:1585` calls `TryAdmit`, no other dispatch path enters via the admission controller. |
| `sessionWorker.lastActive` write-only dead state | low-medium | **Resolved** | Field removed entirely from `pkg/agent/session_worker.go`. No `lastActive` references in production or tests. |
| `sessionWorker.parent` inconsistent nil-checks | low | **Resolved** | `newSessionWorker` now panics on nil parent (`session_worker.go:79-81`), and the doc comment at L65 explicitly says *"Always non-nil — newSessionWorker panics if parent is nil."* The defensive `if w.parent != nil` in `enqueue` was however **retained at L107** — see Finding 1 below. |
| `tests/integration/replay_ordering_test.go` inline `struct { ID string }` instead of `generated.CreateSessionResponse` | low | **Not addressed** | Still inline at L441. Pass-1 marked this low; pass-2 keeps it on the table as a follow-up. |
| `pkg/gateway/sandbox_apply.go:226-231` in-place mutation of `opts` defaults | low | **Not addressed** | Out of scope for the bug-fix work that pass-2 is verifying. No regression. |
| `pkg/config/sandbox.go:95-100` `SandboxProfile.IsValid`/`.String` pointer receivers | cosmetic | **Not addressed** | Cosmetic, no regression. |
| `pkg/agent/steering_test.go:540` `lateSteeringProvider.Chat` ignores `ctx.Done()` | low | **Not addressed** | Test-only; no regression. Pass-1 already low. |

Net: every medium-severity finding from pass-1 is resolved; the unresolved items are all `low` or `cosmetic` and explicitly carried forward.

---

## Rewritten types — new ratings

### `AdmissionController` — `pkg/agent/admission.go:19-79`

#### Invariants now identified

- `softCap > 0` (constructor enforces; immutable).
- `activeScopes` is the **set** of admitted scope strings — not a count. The cap is `len(activeScopes) ≤ softCap`.
- For a given scope, at most one slot is held at a time. A second `TryAdmit(sameScope)` always returns `(true, noop)` and does NOT increment.
- The returned `release` function decrements iff the scope was newly inserted; for re-entry calls it is a `func() {}` no-op.
- `release` is returned **only on success**. On rejection, `TryAdmit` returns `(false, nil)`; callers cannot mis-pair release with a failed admission.

#### Ratings

- **Encapsulation: 5/5.** `mu` and `activeScopes` are package-private; only `TryAdmit`, `ActiveScopes`, and `SoftCap` are exposed. Internal map cannot be reached externally. Map size is the source of truth — no separate counter to drift.
- **Invariant expression: 5/5.** Returning the release closure on success and `nil` on rejection makes "release without admit" literally impossible — there is no release token to mis-use. Combined with the existing-scope no-op, the caller cannot accidentally double-decrement.
- **Invariant usefulness: 4/5.** The threat model is now honest (inbound bus only; subagent spawn not gated). The per-scope (not per-turn) counter is the right granularity for the **session worker fan-out** threat — a chatty session in a tool loop cannot pin admission slots indefinitely. Subagent gating is still future work.
- **Invariant enforcement: 5/5.** Atomic test-and-insert under `mu`. The TOCTOU regression test at `admission_test.go:185-234` proves the atomic claim under contention; the existing-scope test at `admission_test.go:246-274` proves the re-entrant case does not consume new slots. No underflow possible because `delete` from a missing key is a Go no-op.

**Net: every score improved.** Pass 1: Enc 5/5, Expr 3/5, Useful 2/5, Enforce 2/5 → Pass 2: 5/5, 5/5, 4/5, 5/5.

#### New concerns

1. **`pkg/agent/admission.go:60-64` — captured `scope` in release closure.** The closure captures `scope` by reference (Go closes over variables). This is fine for the current call site because `scope` is a function parameter and lives on the goroutine's stack. **Severity: cosmetic** — note for future maintainers that the release closure is single-shot semantically (calling it twice deletes a non-existent key, harmless) but not idempotent over a re-admission of the same scope: if scope X is released, re-admitted by a new worker, and the **old** release closure is called again, it will incorrectly delete the new admission. In practice this cannot happen because the closure is held only by the worker that owns the scope and is called exactly once in `defer w.admissionRelease()`. Document the single-shot contract on the `TryAdmit` doc comment.

2. **`pkg/agent/admission.go:46` — `TryAdmit` takes a `string` scope; nothing prevents `""`.** An empty-string scope is a valid map key and would be admitted. Severity: low. Either `if scope == ""` reject, or document that callers must validate. The actual call site (`loop.go:1585`) gets `scope` from `resolveSteeringTarget` which returns `ok=false` for empty, so this is defended in practice — but the type itself doesn't enforce it.

---

### `sessionWorker` — `pkg/agent/session_worker.go:33-67`

#### Invariants now identified

- `scope`, `parent`, `admissionRelease` immutable after construction.
- `parent != nil` — enforced by constructor panic at L80.
- `admissionRelease != nil` — **NOT enforced by constructor.** Caller may pass `func(){}` for tests (and does in `session_worker_test.go:336,373`) but a `nil` here would panic in `defer w.admissionRelease()` at runLoop exit. See Finding 2.
- Exactly one `runLoop` goroutine reads `inbox`.
- `inTurn == true` ⟺ `processTurn` is currently executing on that goroutine.
- `done` closed exactly once by `defer close(w.done)`.
- On exit, the worker calls (in defer-LIFO order) the recover-guard, `admissionRelease`, `sessionWorkers.Delete(scope)`, then `close(done)`.

#### Ratings

- **Encapsulation: 4/5.** Same as pass-1 — the reach into `parent.*` (15+ methods) is the leakiest part, but that's an in-package coupling we explicitly accepted in pass-1. No regression.
- **Invariant expression: 4/5.** Improved from pass-1's 3/5. The new constructor panic on nil parent + the explicit doc comment on `admissionRelease` ("MUST be the func() returned by AdmissionController.TryAdmit; it is called once when the worker's goroutine exits") makes the lifecycle invariant readable. Still not type-enforced for the admissionRelease nil case.
- **Invariant usefulness: 4/5.** Same as pass-1 — `inTurn` and `done` continue to serve real purposes; the new field strengthens the lifecycle story.
- **Invariant enforcement: 4/5.** Improved from pass-1's 3/5. `lastActive` dead state is gone. `parent` non-nil is a hard precondition. The runLoop defer chain (L142-L155) gives `admissionRelease` a single, structural call site that cannot be skipped except by goroutine never starting (which is a caller error documented at L70: *"Callers must call go w.runLoop() immediately after spawning"*).

**Net: Enforcement and Expression each improved by 1.** Pass 1: 4/3/4/3 → Pass 2: 4/4/4/4.

#### New concerns / new findings

3. **`pkg/agent/session_worker.go:107` — `w.parent != nil` check in `enqueue` is now dead code.** With the constructor panic at L79-L81, `parent` is provably non-nil. The `enqueue` path defensively does:

   ```go
   if err := w.parent.enqueueSteeringFromMessage(msg); err == nil {
   ```

   No nil check now, which is consistent with `processTurn`'s `al := w.parent` at L194. **Resolved in this rewrite.** (Pass-1 finding C2 closed.)

4. **`admissionRelease` nil-safety is by convention, not by type.** `newSessionWorker(scope, parent, nil)` compiles and panics at exit. Two options:
   - Constructor precondition: `if admissionRelease == nil { panic("nil admissionRelease") }` — symmetric with the parent check, costs nothing.
   - Default to no-op: `if admissionRelease == nil { admissionRelease = func(){} }` — silent fallback.
   
   Pass-1 principle says **make illegal states unrepresentable** → prefer the panic. The fact that tests use `func(){}` explicitly is correct behaviour; a `nil` callsite would be a programmer error and should fail loudly. **Severity: low.** Recommend the panic for symmetry.

5. **`pkg/agent/session_worker.go:142-144` — defer ordering relies on Go LIFO semantics being read correctly.** The cleanup order is:
   1. `close(w.done)` runs LAST (deferred first).
   2. `sessionWorkers.Delete(w.scope)` runs second-to-last.
   3. `admissionRelease()` runs third-to-last.
   4. The recover defer at L146-L155 runs FIRST.
   
   This means: a panic inside runLoop will be caught, then admission released, then worker self-removed from the map, then `done` closed. The `Close()` waiter at `loop.go:1639` blocks on `done`, so by the time `Close()` proceeds the admission slot is freed and the map entry is gone — both invariants the caller expects. **Good.** Suggestion: add a one-line comment at L142-L144 explicitly stating the LIFO order *"defers fire bottom-up: recover → admissionRelease → sessionWorkers.Delete → close(done)"* so future maintainers don't reorder them. **Severity: cosmetic.**

---

### `wsConn.replayMu` — `pkg/gateway/websocket.go:162`

This is a guard, not a type-level invariant. The invariant being protected is:

> The pair (`isReplayingLive`, `replayDivertCh`) is atomically consistent during a writer's channel-select decision. Specifically: if `sendRawFrameBytes` reads `isReplayingLive == true` and proceeds to send to `replayDivertCh`, that send completes before `handleAttachSession` drains and disarms the flag.

#### Ratings (this is a guard, scored against the guard's surface area)

- **Encapsulation: 3/5.** `replayMu`, `isReplayingLive`, and `replayDivertCh` are all package-private fields on `wsConn` but exposed to every function in the package. The lock-discipline is "writers RLock, drain Lock" — documented in 30 lines of comment at L144-L165 and again at L1124-L1150. The discipline is enforced by code review, not by the type.
- **Invariant expression: 2/5.** Three fields cooperate; the invariant is verbal, not structural. A future maintainer could write a new emit path that touches `isReplayingLive`/`replayDivertCh` without taking `replayMu` and break the invariant silently.
- **Invariant usefulness: 5/5.** The race it fixes is real and was the bug-5 regression. Tests at `websocket_replay_order_test.go:50-193` and `:245-318` cover it.
- **Invariant enforcement: 3/5.** Enforced at exactly the two sites that touched the variables before (`sendRawFrameBytes` and `handleAttachSession`). A grep for `isReplayingLive` / `replayDivertCh` confirms no other writers — but the type itself does not stop you from adding one.

#### Strengths

- Comment block at `websocket.go:144-165` is exemplary — it names the failure mode (TOCTOU), the fix (RLock for writers / Lock for drain), and the fast-path optimisation (skip the lock when `isReplayingLive == false` on the first atomic load).
- Fast path is genuinely lock-free — the common case (no replay in flight) hits the atomic load at L1366 and never touches `replayMu`.
- Drain-then-disarm under `replayMu.Lock()` (L1151-L1176) preserves FIFO ordering of buffered live frames vs concurrent live writers — the regression test at L50-L193 fails without this fix.

#### Concerns

6. **The guard is on a field, not a type.** Pass-1 principle: *"types should make illegal states unrepresentable"*. The current shape forces every new writer to remember three things: (a) read `isReplayingLive` atomically first, (b) if true, take `RLock()`, (c) re-check the flag under the lock before sending. A future writer who forgets steps (b)-(c) silently regresses bug-5.
   
   **Pragmatic suggestion (not a blocker):** extract a small `replayDivert` helper type on `wsConn` with a single `sendOrDivert(data []byte, isCritical bool, sendCh chan []byte)` method that owns all three steps. Three fields collapse into one method call, and the type makes the discipline impossible to forget. Estimated 40-line change with a clear test surface. **Severity: low** — the current implementation is correct and tested; the suggestion is a structural cleanup, not a bug fix. If you do it, do it in v0.2.

7. **`sync.RWMutex` is heavier than the alternative.** Some Go codebases prefer a single `sync.Mutex` + atomic counters over an RWMutex when readers vastly outnumber writers (the fast-path case here). Benchmark would resolve which is faster on the hot path. **Severity: cosmetic.** The current code is correct; performance tuning is v0.2 territory.

---

## `enqueueSteeringFromMessage` simplification — `pkg/agent/steering.go:193-206`

The function now reads:

```go
func (al *AgentLoop) enqueueSteeringFromMessage(msg bus.InboundMessage) error {
    route, ag, err := al.resolveMessageRoute(msg)
    if err != nil || ag == nil {
        return fmt.Errorf("enqueueSteeringFromMessage: route resolution failed: %w", err)
    }
    pmsg := providers.Message{...}
    return al.enqueueSteeringMessage(route.SessionKey, ag.ID, pmsg)
}
```

#### Verification

- **Signature:** `func (al *AgentLoop) enqueueSteeringFromMessage(msg bus.InboundMessage) error` — single message in, single error out. Clean.
- **Scope/key usage:** Uses `route.SessionKey` (the `"agent:<id>:<sid>"` key registered by `runTurn` in `activeTurnStates`). This matches the key that `enqueueSteeringMessage` → `steering.pushScope` writes under, and that `pendingSteeringCountForScope` reads. **Correct.**
- **Error wrapping:** Uses `%w` to preserve the original error for caller fallback decisions (`session_worker.go:107-112` logs the wrapped error before falling through to inbox).
- **No swallowed errors.** All three failure modes (`err != nil`, `ag == nil`, `steering nil`) return a non-nil error.

**Net: clean.** No new concerns.

---

## Anonymous struct in `replay_ordering_test.go`

Status: **not fixed.** `tests/integration/replay_ordering_test.go:441-443` still uses:

```go
var body struct {
    ID string `json:"id"`
}
```

Pass-1 already marked this as `low` severity (test scaffolding, ID-only extraction, gateway-side `CreateSessionResponse` is the contract-of-record). Carry forward; not a pass-2 blocker.

---

## New findings (pass 2 only)

| File:line | Severity | Finding |
|---|---|---|
| `pkg/agent/admission.go:60-64` | cosmetic | release closure is single-shot semantically; document the contract on `TryAdmit` doc comment. |
| `pkg/agent/admission.go:46` | low | `TryAdmit("")` admits empty-scope; defend in the type or document caller responsibility. |
| `pkg/agent/session_worker.go:78-92` | low | `admissionRelease == nil` panics at runLoop exit; add a constructor precondition for symmetry with `parent`. |
| `pkg/agent/session_worker.go:142-144` | cosmetic | Add a one-liner naming the LIFO defer order so future maintainers don't reorder. |
| `pkg/gateway/websocket.go:144-165, 1356-1432` | low | Three-field replay-divert guard is correct but not type-enforced. Optional v0.2 refactor: wrap in a `replayDivert` helper method on `wsConn`. |

No medium or above findings introduced by the rewrites.

---

## Approval

**Approved for v0.1.**

- All pass-1 medium-severity findings are resolved.
- The AdmissionController rewrite genuinely closes the TOCTOU window and uses the right granularity (per-scope, not per-turn) for the threat being mitigated.
- The `sessionWorker` lifecycle invariants are now structurally enforced (constructor panic + defer chain) where it matters.
- The `replayMu` guard is correct, well-documented, and covered by regression tests; the only outstanding concern is a structural refactor opportunity that should land in v0.2, not v0.1.
- Pass-2 new findings are all low/cosmetic.

Carry-forward to v0.2: `admissionRelease` nil-precondition, `TryAdmit("")` defense, optional `replayDivert` helper, and the long-standing pass-1 cosmetic items.

---

## Relevant files

- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/admission.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/admission_test.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/session_worker.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/session_worker_test.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/loop.go` (L1500-L1645)
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/agent/steering.go` (L184-L266)
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/gateway/websocket.go` (L133-L165, L1124-L1432)
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/pkg/gateway/websocket_replay_order_test.go`
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/tests/integration/replay_ordering_test.go` (L441-L450 — still anon struct)
- `/mnt/volume_sgp1_01/projects/omnipus-security-wiring/docs/investigation/review-type-design-analyzer.md` (pass 1)
