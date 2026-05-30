# Comment-Analyzer Review — feature/iframe-preview-tier13

## Summary

Scope: comments added in the recent bug-3/bug-4/bug-5 fix work across
`pkg/agent/session_worker.go`, `pkg/agent/admission.go`,
`pkg/agent/steering.go`, `pkg/gateway/sandbox_apply.go`,
`pkg/gateway/websocket.go`, `pkg/agent/loop.go`, `docker/Dockerfile`,
`pkg/config/sandbox.go`, `src/components/ui/model-selector.tsx`,
`src/routes/onboarding.tsx`, `tests/integration/*`, and
`docs/internal/investigation/bug-5-replay-order.md`.

The headline issues are factual:

1. The bug-4 (Docker exec) root-cause comment in `pkg/gateway/sandbox_apply.go`
   and `docker/Dockerfile` blames Docker's default seccomp profile blocking
   `RLIMIT_NPROC` / `prctl` / "Landlock prctl"; the regression-test header in
   `tests/integration/docker_exec_test.go` blames Landlock filesystem rules
   preventing exec. Both versions are speculative and inconsistent — pick one,
   verify it against an actual `strace`, or drop the syscall list.
2. The new `wsStreamer.Update` comment claims `sendRawFrameBytes` increments
   `droppedFrames` but *not* `droppedTokens`. The code at
   `pkg/gateway/websocket.go:1383-1384` increments **both**. The delegated path
   can therefore double-count the same dropped token.
3. The `enqueueSteeringFromMessage` body and its preceding comment block disagree
   about which scope key is used: the function computes a worker-scope `scope`
   variable that it then never reads, before falling back to `route.SessionKey`.
   The dead local makes the explanatory comment self-contradictory.
4. Bug-numbered references ("bug-3", "bug-5 fix B", "Phase 2 (v0.2 follow-up
   #175)") are scattered through production code. Issue #175 exists but is
   unrelated to resource-aware admission.
5. The `TestSessionWorker_IdleTimeout` comment claims to verify "workerIdleTimeout
   elapses with no inbox activity" — the test never exercises the idle-timer
   path; it calls `w.cancel()`.

Recommended pattern going forward: keep the WHY (e.g., "drain before disarm or
FIFO inverts ordering") and delete the WHAT-the-PR-was, the bug numbers, and
the speculative syscall lists.

---

## Critical (INACCURATE)

### `pkg/gateway/websocket.go:1854-1882` — droppedTokens accounting comment is wrong
**Severity:** INACCURATE
**Issue:** Comment block says "sendRawFrameBytes increments droppedFrames
(not droppedTokens), so we still need to track droppedTokens here".
`sendRawFrameBytes` at `pkg/gateway/websocket.go:1383-1384` increments **both**
`droppedTokens.Add(1)` and `droppedFrames.Add(1)`. The justification for the
extra `s.conn.droppedTokens.Add(1)` at line 1879 is wrong, and on the backoff
path a single token drop is now counted twice.
**Action:** FIX-CLAIM. Either remove the redundant `droppedTokens.Add(1)`
inside `Update` (since `sendRawFrameBytes` already does it) or, if there is a
real reason to keep it pessimistic, rewrite the comment to say "we
intentionally double-count on the delegated path because the backoff outcome
isn't observable here". As written, the comment misleads any future maintainer.

### `pkg/agent/steering.go:198-206` — explanatory comment doesn't match dead code below it
**Severity:** INACCURATE
**Issue:** Lines 198-201 compute `scope := resolveScopeKey(...)` and conditionally
append `:" + msg.SessionID`. That value is never read. Line 206 then declares
`turnScope := route.SessionKey` and uses *that*. The comment block at 202-205
tries to explain the "strip the SessionID suffix back off" mental step — but
the suffix was never added to the variable that gets used. The comment
explains a transformation the code doesn't actually perform.
**Action:** FIX-CLAIM. Delete the dead `scope`/`if msg.SessionID != ""` block
or, if it was meant to be passed somewhere (e.g., for the
worker-scope-vs-turn-scope log), wire it in. Then rewrite the comment to
describe what the surviving code does in two lines.

### `pkg/gateway/sandbox_apply.go:149-159` and `docker/Dockerfile:55-71` — conflicting Docker root-cause claims
**Severity:** INACCURATE (cross-file inconsistency)
**Issue:** `sandbox_apply.go` and `Dockerfile` both claim the failure mode is
Docker's default seccomp profile blocking "RLIMIT_NPROC manipulation, prctl,
Landlock prctl". `tests/integration/docker_exec_test.go:6-10` blames Landlock
filesystem rules: "landlock_restrict_self succeeds but then no process can
fork/exec because the filesystem rules prevent executing anything outside the
locked tree". The two explanations are mutually exclusive. Whichever is right,
the other one is going to mislead the next operator who reads it after a
related sandbox regression.
**Action:** FIX-CLAIM. Verify with `strace -f` inside an unprivileged Docker
container which syscall actually fails, settle on one root cause, and replicate
the same wording in all three files (or, better, drop the syscall-level
explanation in `sandbox_apply.go` and just say "the default Docker container
configuration is incompatible with sandbox=enforce — see
docs/operations/docker.md").

### `pkg/agent/admission.go:14-16` and `pkg/agent/loop.go:221-223` — "v0.2 follow-up #175" reference is misleading
**Severity:** INACCURATE / STALE-RISK
**Issue:** Both comments file resource-aware admission as "v0.2 follow-up
(#175)". Issue #175 exists, but its title is "perf: TestLoad2000Sessions SLO
breaches" — it's about load-test SLO failures, not admission policy.
A future reader following the link will not find a resource-aware-admission
discussion there.
**Action:** FIX-CLAIM. Either open a real follow-up issue and reference that,
or drop the number and just say "Phase 2: resource-aware admission is out of
scope for v0.1". CLAUDE.md's release rule routes admission work to v0.3, not
v0.2, anyway — see the "Routing rule" paragraph.

### `pkg/agent/session_worker_test.go:89-95` — TestSessionWorker_IdleTimeout misnamed
**Severity:** INACCURATE (test header makes a false claim about what is verified)
**Issue:** Comment: "verifies that a worker removes itself from the parent's
sessionWorkers map after workerIdleTimeout elapses with no inbox activity".
The test body at line 116 calls `w.cancel()` directly — it exercises the
ctx-cancel exit path, not the 60 s idle-timer path. No test in the package
actually drives `idleTimer.C`.
**Action:** FIX-CLAIM. Either rename the test to
`TestSessionWorker_ContextCancelExits` and rewrite the comment, or stub
`workerIdleTimeout` (e.g., a package-private `idleTimeoutForTest` variable)
and write a test that genuinely waits for the timer.

---

## STALE-RISK

### `pkg/agent/loop.go:3024` — references "bug-3" by number in production code
**Severity:** STALE-RISK
**Issue:** "re-introducing the bug-3 serialization the per-session-worker
design exists to fix" — once the branch is merged there is no durable "bug-3"
identifier. The serialization concern is real; the label is not.
**Action:** TRIM. Replace "the bug-3 serialization" with "the
serialization regression the per-session-worker design exists to fix".

### `pkg/gateway/websocket.go:1133` and `:1849` — "bug-5 fix" / "bug-5 fix B" inline
**Severity:** STALE-RISK
**Issue:** Two production-code references to "bug-5 fix" / "bug-5 fix B". The
bug numbering is local to the feature branch's investigation doc; outside
this branch it carries no meaning. The technical content of the comments
(drain-before-disarm rationale, divert-routing for tokens) is good and worth
keeping; the labels are not.
**Action:** TRIM. Strip "(bug-5 fix)" / "(bug-5 fix B)" from both blocks.
The rest of each comment is load-bearing.

### `pkg/gateway/sandbox_apply.go:134` — "Bug 4 / Docker compat" label
**Severity:** STALE-RISK
**Issue:** Same problem as the bug-5 references — the bug number won't
survive the merge.
**Action:** TRIM. Open with "Docker compat:" and delete "Bug 4 /".

### `pkg/agent/steering_test.go:343-348` — refers to deleted predecessor test
**Severity:** STALE-RISK
**Issue:** "TestSessionWorker_DifferentScopesGetIndependentWorkers replaces
the old TestDrainBusToSteering_RequeuesDifferentScopeMessage test. With
per-session workers, messages for different scopes are routed into separate
workers — there is no requeue path." The deleted test will not be found by
anyone reading this in six months; the renaming history belongs in the commit
message, not the source file.
**Action:** TRIM. Keep the second sentence ("With per-session workers,
messages for different scopes…") which describes current behavior. Delete the
first sentence.

### `src/routes/onboarding.tsx:28` — dangling "US-8" reference
**Severity:** STALE-RISK / NIT
**Issue:** The diff removed "US-7: First-launch onboarding flow" but left
"US-8: Provider setup with API key input + test connection" right below it.
The user-story numbers are not maintained anywhere a reader can resolve them.
**Action:** DELETE. The user can see this is the provider setup step from the
component structure.

### `pkg/agent/session_worker.go:179` — "stuck stuck-in-turn" typo
**Severity:** NIT
**Issue:** Duplicated word in the panic-recovery comment.
**Action:** TRIM. Fix to "stuck in-turn". Also consider whether the comment is
needed at all — a panic in `processTurn` kills the worker goroutine, and the
defer at `runLoop` (`sessionWorkers.Delete` + `close(done)`) cleans up regardless.
The `inTurn.Store(false)` defer running on panic is essentially a no-op since
the worker is gone.

---

## WHAT-NOT-WHY (delete or trim)

### `pkg/agent/admission.go:38-48` — method-doc restatement of single-line bodies
**Severity:** WHAT-NOT-WHY
**Issue:** `OnTurnStart` ("must be called when a turn begins executing... It
increments the active-turn counter") and `OnTurnEnd` ("must be called
(typically via defer) when a turn finishes. It decrements the active-turn
counter") explain what the one-line body already shows. `ActiveTurns` and
`SoftCap` ("Used in tests and observability" / "returns the configured soft
cap value") are pure restatement.
**Action:** TRIM. Keep `ShouldAdmit`'s comment (the "<" semantic is non-obvious).
Reduce the others to one-line `// OnTurnStart records the start of a turn.`
form or delete entirely — the type-level doc already says the controller is a
counter.

### `pkg/agent/session_worker.go:43-44` — restates field type
**Severity:** WHAT-NOT-WHY
**Issue:** "inbox receives messages dispatched by Run(). Buffered so the
dispatcher never blocks on a slow session." The "buffered" part is visible
from the declaration `inbox chan bus.InboundMessage` plus
`make(chan ..., workerInboxCap)`. The "never blocks" claim is also wrong —
`enqueue`'s `select { default: drop }` shows the dispatcher *does not* block
but *also does not* deliver. The drop behavior is the load-bearing fact.
**Action:** TRIM. Either delete or replace with "// inbox is the worker's
message queue. Full → drop with WARN (see enqueue)."

### `pkg/agent/session_worker.go:46-48` — restates context derivation in three lines
**Severity:** WHAT-NOT-WHY
**Issue:** "ctx is the worker's own context, derived from context.Background()
so the worker's lifetime is independent of Run()'s context. AgentLoop.Close()
uses cancel to stop the worker explicitly." This duplicates the same explanation
that `newSessionWorker`'s doc-comment already gives. Pick one location.
**Action:** TRIM. Delete the field-level comment; keep the constructor's
version since that's where the design choice is enacted.

### `pkg/agent/session_worker.go:56-58` — "Used in tests and observability" pattern
**Severity:** WHAT-NOT-WHY
**Issue:** `lastActive` comment explains the atomic store choice. The current
code doesn't read `lastActive` from outside the goroutine — `runLoop` is its
only reader. The "safe to read from outside" justification anticipates a
future caller that doesn't exist; that makes this speculative documentation.
**Action:** TRIM. Either remove the field if nothing reads it from outside,
or drop the "safe to read from outside the goroutine" sentence.

### `pkg/agent/session_worker.go:156` — `// Reset idle timer on every message.`
**Severity:** WHAT-NOT-WHY
**Issue:** The four-line drain-and-Reset block is idiomatic Go; the comment
just describes what `idleTimer.Reset` does.
**Action:** DELETE.

### `pkg/agent/loop.go:328` — `// 0 → default: NumCPU() * 4`
**Severity:** WHAT-NOT-WHY (mild)
**Issue:** The constructor doc-comment already says exactly this. Inline
re-comment is redundant.
**Action:** DELETE.

### `pkg/agent/loop.go:1592` — `// Collect workers first, then cancel them all.`
**Severity:** WHAT-NOT-WHY
**Issue:** Comment narrates the next four lines verbatim.
**Action:** DELETE. The "collect-then-cancel" pattern is self-evident.

### `pkg/agent/loop.go:1692-1695` — "stopSessionWorkers is idempotent"
**Severity:** NIT (borderline keep)
**Issue:** Useful enough to keep, but the second sentence ("because workers
cancel their own context; a double-cancel is a no-op") repeats Go semantics.
**Action:** TRIM to "Idempotent — safe even if Run() already called it."

### `docker/Dockerfile:55-71` — multi-paragraph operator narrative
**Severity:** WHAT-NOT-WHY / over-long
**Issue:** 16 lines of operator narrative for one `ENV` line. The actionable
content is two facts: (a) Docker containers default to permissive, (b) override
with `OMNIPUS_SANDBOX_MODE=enforce` if you have configured custom caps. The
syscall list and the "bare-metal unaffected" paragraph belong in
`docs/operations/docker.md`, not the Dockerfile.
**Action:** TRIM to ~3 lines: "Default sandbox mode = permissive inside Docker.
The hardened-exec path is incompatible with the default unprivileged container.
Override with OMNIPUS_SANDBOX_MODE=enforce if you have configured custom
capabilities. See docs/operations/docker.md."

---

## NIT

### `src/components/ui/model-selector.tsx:88` — `{/* shouldFilter=false: we handle filtering ourselves… */}`
**Severity:** NIT (keep — it earns its place)
**Issue:** Explains the *why* of disabling shadcn/cmdk's built-in filter.
Without the comment, a future maintainer is likely to turn it back on and
break provider-group search.
**Action:** KEEP.

### `src/components/ui/model-selector.tsx:17` — `// Named onChange (not onValueChange)…`
**Severity:** NIT (keep — pre-existing, but holds up)
**Action:** KEEP.

### `pkg/agent/steering.go:184-192` — `enqueueSteeringFromMessage` header
**Severity:** NIT (keep most, but see Critical above on the in-body block)
**Issue:** Header is a good *why* doc. The mismatch is only with the inner
"strip the SessionID suffix back off" comment, not this header.
**Action:** KEEP header.

### `pkg/gateway/sandbox_apply.go:174-184` — `isRunningInDocker` doc
**Severity:** NIT (keep)
**Issue:** Two-signal detection (env var + `/.dockerenv`) is non-obvious to
someone reading the function for the first time. Good *why* documentation.
**Action:** KEEP.

### `pkg/config/sandbox.go:278` — `env:"OMNIPUS_SANDBOX_MODE"` tag added without inline comment
**Severity:** NIT
**Issue:** The struct-level comment above the field already covers
"unknown values rejected at config-load time" and "empty Mode → enforce on
capable kernels". The env-tag addition doesn't need its own comment as long
as the Dockerfile / sandbox_apply.go side documents the env override path.
**Action:** No change needed.

---

## Kept (passed review)

These comments are good as written or with minor edits; future reviews should
not re-litigate them:

- `pkg/agent/session_worker.go:17-26` — constant docs (`workerInboxCap`,
  `workerIdleTimeout`) — concise WHY.
- `pkg/agent/session_worker.go:28-31` — type-level doc explaining the
  per-session-worker design.
- `pkg/agent/session_worker.go:62-66` — `inTurn` field doc — explains the
  steering-queue-vs-inbox routing decision (non-obvious).
- `pkg/agent/session_worker.go:73-78` — `newSessionWorker` doc — explains the
  `context.Background()` choice with rationale.
- `pkg/agent/session_worker.go:96-100` — `enqueue` doc — explains the
  full-inbox drop policy (load-bearing for ops).
- `pkg/agent/session_worker.go:103-107` — explains the steering-fallback path
  inside `enqueue`. Load-bearing.
- `pkg/agent/session_worker.go:170-173` — `processTurn` doc, modulo the
  "previously inlined in Run()" historical aside which can go in the commit
  message.
- `pkg/agent/admission.go:12-17` — type-level doc with explicit
  "not resource-aware" caveat. (See Critical for the issue-number fix.)
- `pkg/agent/steering.go:184-192` — header for
  `enqueueSteeringFromMessage` (see NIT note above).
- `pkg/gateway/websocket.go:1127-1144` — drain-then-disarm rationale block.
  The technical reasoning is the load-bearing content; strip "(bug-5 fix)"
  per STALE-RISK above and keep the rest.
- `pkg/gateway/websocket.go:1158-1160` — short "Disarm AFTER drain" coda.
- `pkg/gateway/sandbox_apply.go:174-184` — `isRunningInDocker` two-signal doc.
- `pkg/config/sandbox.go` Mode field doc — unchanged, still accurate.

---

## Aggregate counts

- INACCURATE (FIX-CLAIM): 5
- STALE-RISK (TRIM bug-numbers/PR labels): 5
- WHAT-NOT-WHY (DELETE/TRIM): 7
- NIT: 4
- KEPT: 12
