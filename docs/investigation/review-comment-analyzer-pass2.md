# Comment-Analyzer Review Pass 2 — feature/iframe-preview-tier13

## Summary

Verified the 17 items from `review-comment-analyzer.md` against commits
`c27ff7f`, `132bb46`, `b7f3e98`. **15 of 17 are fully resolved**, 1 is partially
resolved with a different (acceptable) shape, and 1 remains as a residual
INACCURATE. No new bug-N markers, US-N markers, or speculative "in the future"
comments were introduced. The implementing agents went further than
pass-1 asked in a few places — most notably by deleting the entire
`OnTurnStart`/`OnTurnEnd`/`ActiveTurns` API surface (replaced with `TryAdmit`
that has genuine WHY documentation) and by adding a real `TestSessionWorker_IdleExits`
test with a `workerIdleTimeout` test knob.

The one residual issue is small but real: `pkg/agent/admission.go:14-17` and
`pkg/agent/loop.go:221-225` still describe resource-aware admission as a
"v0.2 follow-up" when CLAUDE.md's release-routing rule places admission work
under v0.3.

---

## Pass-1 resolution table

| # | Pass-1 location | Severity | Status | Notes |
|---|---|---|---|---|
| 1 | `websocket.go:1854-1882` droppedTokens double-count | INACCURATE | RESOLVED | Comment rewritten at `websocket.go:1938-1953` to say `sendRawFrameBytes` is "the single authoritative counter" and the duplicate `s.conn.droppedTokens.Add(1)` is gone. Reads dropped-count delta via `originalDropped := s.conn.droppedTokens.Load()` instead — accurate. |
| 2 | `steering.go:198-206` dead `scope` var + self-contradictory comment | INACCURATE | RESOLVED | `steering.go:193-206` now uses `route.SessionKey` directly with a one-line comment ("The steering queue uses route.SessionKey — the same key that runTurn registered the active turn under in activeTurnStates"). No dead variable, no suffix-strip mental gymnastics. |
| 3 | `sandbox_apply.go` + `Dockerfile` + `docker_exec_test.go` conflicting Docker root cause | INACCURATE | RESOLVED | All three files now blame the **same** thing: "Docker's default unprivileged seccomp profile blocks syscalls the hardened-exec path requires (RLIMIT_NPROC manipulation, prctl, Landlock prctl)". The original test-header claim about "Landlock filesystem rules" is gone. Pass-1 also recommended an `strace -f` verification; that verification is not visible in the diff, but at least the three files no longer contradict each other. |
| 4 | `admission.go:14-16` + `loop.go:221-223` "v0.2 follow-up #175" misleading | INACCURATE / STALE-RISK | PARTIAL | Issue number `#175` is correctly removed. Both comments now just say "v0.2 follow-up issue" (admission.go:17) / "filed as a follow-up" (loop.go:224). However per CLAUDE.md's routing rule, admission work belongs to **v0.3**, not v0.2. Recommend: replace "v0.2 follow-up issue" with "out of scope for v0.1; will be revisited in v0.3 alongside the rooms redesign". See "Residual issues" below. |
| 5 | `session_worker_test.go:89-95` `TestSessionWorker_IdleTimeout` misnamed | INACCURATE | RESOLVED + EXCEEDED | Renamed to `TestSessionWorker_CancelExits` (line 98) with accurate comment noting "exercises the ctx-cancel exit path only — it does NOT test the workerIdleTimeout path". A new genuine `TestSessionWorker_IdleExits` test was added at line 364 that flips `workerIdleTimeout` to 50 ms via a test-only var override and waits for real timer firing. |
| 6 | `loop.go:3024` "bug-3 serialization" | STALE-RISK | RESOLVED | No remaining "bug-3" reference in `pkg/agent/loop.go` (`grep -nE "bug-[0-9]"` returns no production-code hits in pkg/agent/). |
| 7 | `websocket.go` "bug-5 fix" / "bug-5 fix B" inline labels | STALE-RISK | RESOLVED (different shape) | The orphan `(bug-5 fix)` / `(bug-5 fix B)` labels were not just stripped — they were replaced with anchored references to `docs/investigation/bug-5-replay-order.md` (at `websocket.go:149`, `:1127`, `:1346`, `:1946`). This is **better** than pass-1's recommendation: the bug number now resolves to a durable rationale doc rather than being merely deleted. Accept. |
| 8 | `sandbox_apply.go:134` "Bug 4 / Docker compat" label | STALE-RISK | RESOLVED | Opens with "Docker compat:" at `sandbox_apply.go:150` — no leading "Bug 4 /" prefix. |
| 9 | `steering_test.go:343-348` refers to deleted predecessor test | STALE-RISK | RESOLVED | The "TestSessionWorker_DifferentScopesGetIndependentWorkers replaces the old…" sentence is deleted. Only the "With per-session workers, messages for different scopes are routed into separate workers" sentence (which describes current behavior) remains at `steering_test.go:346-347`. |
| 10 | `onboarding.tsx:28` dangling US-8 reference | STALE-RISK / NIT | RESOLVED | Line 28 is now "First-launch onboarding flow — full-screen, outside AppShell". No US-8 / US-7 markers anywhere in onboarding.tsx. |
| 11 | `session_worker.go:179` "stuck stuck-in-turn" typo | NIT | RESOLVED | Now reads "Cleared even on panic so the worker doesn't get stuck in-turn." at `session_worker.go:198`. |
| 12 | `admission.go:38-48` method-doc restatements | WHAT-NOT-WHY | RESOLVED (by API redesign) | The entire `OnTurnStart`/`OnTurnEnd`/`ActiveTurns`/`SoftCap` quartet was replaced with a `TryAdmit`/`ActiveScopes`/`SoftCap` triad. The new `TryAdmit` doc at `admission.go:37-46` documents the genuinely non-obvious "follow-up turn → no new slot consumed" semantic — that's a real WHY comment, not a restatement. `ActiveScopes` keeps a brief "used in tests and observability" line which is borderline but defensible since the method has no body the reader can squint at. |
| 13 | `session_worker.go:43-44` "buffered so dispatcher never blocks" inaccuracy | WHAT-NOT-WHY | RESOLVED | Now reads "inbox receives messages dispatched by Run(). / Full → drop with WARN and notify user (see enqueue)." at `session_worker.go:38-40` — the misleading "never blocks" phrasing is gone and the load-bearing drop behavior is documented inline. |
| 14 | `session_worker.go:46-48` ctx field comment duplicates constructor doc | WHAT-NOT-WHY | KEPT (acceptable) | The field-level comment at `session_worker.go:42-46` survives; the constructor doc at `session_worker.go:73-77` also covers the same point. This is mild duplication but defensible — the field doc tells readers what `ctx` *is*, the constructor doc tells them why it was chosen. Not a blocker. |
| 15 | `session_worker.go:56-58` `lastActive` "safe to read from outside" speculation | WHAT-NOT-WHY | RESOLVED | The `lastActive` field is entirely removed. Speculative speculation removed by removing the speculator's target. |
| 16 | `session_worker.go:156` "Reset idle timer on every message" narration | WHAT-NOT-WHY | RESOLVED | No standalone "Reset idle timer" comment in `session_worker.go` — the drain+reset block at lines 176-182 stands without prose narration, which is correct since the pattern is idiomatic. |
| 17 | `loop.go:328` `// 0 → default: NumCPU() * 4` inline restatement | WHAT-NOT-WHY | RESOLVED | `loop.go:329` is now just `admission: newAdmissionController(0),` with no inline comment. The constructor's `newAdmissionController` doc at `admission.go:25-26` is the single source of truth. |
| (extra) | `loop.go:1592` "Collect workers first, then cancel them all" | WHAT-NOT-WHY | RESOLVED (different shape) | At `loop.go:1625-1626` the comment now reads "Collect first, then cancel — avoids holding sync.Map's range lock while cancelling (which could deadlock against concurrent Store calls)." That's a *why* comment, not the *what* narration pass-1 flagged. Net improvement. |
| (extra) | `loop.go:1692-1695` "stopSessionWorkers is idempotent" | NIT | KEPT (acceptable) | At `loop.go:1727-1729` the comment was lightly trimmed ("safe to call here even if Run() has already called it on context-cancellation, because workers cancel their own context; a double-cancel is a no-op"). Pass-1 said trim further; this is borderline but harmless — readers don't need to be told `cancel()` is idempotent, but the cost of having the line is low. Acceptable. |

**Net:** 15 fully resolved, 2 with mild residual (#4 release-phase wording; #14
unchanged field-comment duplication), 0 worsened.

---

## New comments added by implementing agents — audit

### `pkg/agent/admission.go` (full rewrite)

The file's API changed from "turn-counter" to "scope-counter with reusable
slots". The new doc-comments are clean:

- **Type-level doc** (`admission.go:12-18`) — explains the per-scope semantic
  and what is NOT gated (subagent spawn, task-executor). Load-bearing.
- **`TryAdmit` doc** (`admission.go:37-46`) — documents the non-obvious
  "follow-up turn → no new slot" rule with a "MUST be called (typically via
  defer)" lifecycle note. Excellent — this is the kind of comment that earns
  its place. Without it, a future maintainer would either skip the
  defer (slot leak) or duplicate-claim slots on follow-up turns.
- **`activeScopes[scope]` lookup comment** (`admission.go:51`) — "Existing
  scope — follow-up turn, always admitted, no new slot consumed." One line,
  reinforces the non-obvious rule at the point of the conditional. Keep.

### `pkg/agent/session_worker.go` — `inTurn`, `admissionRelease`, panic-on-nil-parent

- **`inTurn` field doc** (`session_worker.go:53-57`) — explains the
  steering-queue-vs-inbox routing decision triggered by this atomic. Genuinely
  non-obvious. Keep.
- **`admissionRelease` field doc** (`session_worker.go:60-61`) — concise; ties
  the field to its lifecycle contract ("Set at construction time by the
  dispatcher"). Keep.
- **`parent` field doc** (`session_worker.go:64-66`) — "Always non-nil —
  newSessionWorker panics if parent is nil." This is a hard precondition worth
  documenting at the field level since it eliminates a nil-check that callers
  might otherwise feel obligated to add. Keep.
- **`processTurn` `inTurn.Store(true)` block** (`session_worker.go:196-200`) —
  the "Cleared even on panic so the worker doesn't get stuck in-turn"
  rationale is correct (the deferred `Store(false)` runs even if a panic walks
  through `defer w.parent.sessionWorkers.Delete(w.scope)`'s recover chain).
  Keep.

### `pkg/agent/loop.go` unroutable-message goroutine (`loop.go:1547-1571`)

The "Unroutable — fall through to the original single-shot path so channels
with no configured agent still get an error reply" comment is a *why* comment,
not a narration. Keep. The accompanying `recover()` block also has an
appropriate `logger.ErrorCF` call so panics in the unroutable path are
diagnosable rather than silent.

### `pkg/gateway/websocket.go` `replayMu` rationale (`websocket.go:144-160`, `:1124-1150`, `:1346-1355`)

Three coordinated comment blocks at the field declaration, the drain site,
and the writer site. They all reference `docs/investigation/bug-5-replay-order.md`
and `code-reviewer Finding #2 / architect Finding #4`. The technical content
is the *why* of holding `replayMu` (preventing TOCTOU between flag read and
channel send) plus the back-pressure defense at the drain.

This is *the* example of how a complex concurrency invariant should be
documented in this codebase: short paragraph at each site, all pointing to
one canonical doc, with the lock ordering rule spelled out once.

### `pkg/gateway/sandbox_apply.go` Docker autodetect (`sandbox_apply.go:149-165`, `:174-184`)

- Lines 149-159 explain *why* permissive is the default inside Docker (seccomp
  blocks syscalls, Docker is the outer isolation layer). Good rationale.
- Lines 179-184 explain the two-signal detection (`OMNIPUS_IN_DOCKER=1` env
  override + `/.dockerenv` presence). Pass-1 already flagged this as good
  *why* documentation; unchanged. Keep.

### `docker/Dockerfile` (`Dockerfile:55-60`)

Compressed from the 16-line operator narrative pass-1 flagged. Now: "Default
sandbox mode = permissive inside Docker. The default unprivileged container
seccomp profile blocks syscalls the hardened-exec path requires, causing
'fork/exec /bin/sh: permission denied' with sandbox=enforce. Override with
OMNIPUS_SANDBOX_MODE=enforce if you have configured custom capabilities. See
pkg/gateway/sandbox_apply.go for the full rationale."

Five lines of actionable content + a pointer to the canonical Go-side
rationale. Exactly the trim pass-1 asked for.

### `tests/integration/docker_exec_test.go` (`docker_exec_test.go:3-17`)

The header now says: "Docker's default unprivileged seccomp profile blocks
several syscalls the hardened-exec path requires (RLIMIT_NPROC manipulation,
prctl, Landlock prctl)". The original "landlock_restrict_self succeeds but
then no process can fork/exec because the filesystem rules prevent executing
anything outside the locked tree" wording is gone. Track B fix #6 (the test
header should say seccomp not Landlock) is satisfied. Note that "Landlock
prctl" still appears in the syscall list — but here it refers to the
`prctl(PR_SET_NO_NEW_PRIVS)` and related calls that the hardened-exec path
makes *before* enabling Landlock, not to Landlock's filesystem-restriction
rules. So the new wording is internally consistent.

---

## Residual issues

### `pkg/agent/admission.go:17` + `pkg/agent/loop.go:224` — "v0.2 follow-up" should be v0.3

**Severity:** INACCURATE (mild — release-phase routing)
**Issue:** Both comments describe resource-aware admission as a "v0.2 follow-up
issue" / "filed as a follow-up". CLAUDE.md's release strategy explicitly routes
**room-topology, memory, projects, and tasks** work to v0.3, and pass-1
already flagged this same routing rule. Admission gating is conceptually
adjacent to the rooms redesign (it shapes the per-session concurrency model
the new room topology will inherit), so a future maintainer looking for the
follow-up under v0.2 / #155 will not find it — they need to look at v0.3 / #156.
**Suggestion:** Rephrase to one of:
- "Resource-aware admission (CPU load, RSS, goroutine count) is out of scope
  for v0.1 and will be revisited in v0.3 alongside the rooms redesign (#156)."
- "Resource-aware admission is filed for v0.3 / #156 (rooms redesign), not v0.2."

This is small but it's worth fixing now, while the context is fresh.
Leaving "v0.2 follow-up" in place will mislead the next reader who follows
the release-routing rule literally.

### `pkg/agent/session_worker.go:42-46` ctx field doc duplicates constructor doc

**Severity:** WHAT-NOT-WHY (mild)
**Issue:** Pass-1 flagged this; the duplication survives. The field-level
comment and the constructor doc both explain the
`context.Background()`-derivation rationale. The cost is two paragraphs
saying the same thing.
**Suggestion:** Delete `session_worker.go:42-45`'s rationale paragraph; keep
only "ctx is the worker's own context; AgentLoop.Close() uses cancel to stop
the worker." The constructor doc (`session_worker.go:73-77`) is the better
place for the design-choice rationale because it lives at the call site that
enacts the choice.

(Borderline-NIT — defer if other priorities crowd it out.)

---

## New comment issues introduced by the implementing fixes

None of severity higher than NIT. The implementing agents resisted the
temptation to add new bug-N markers, new US-N markers, or speculative
"someday we will…" forward-references. The new comments they added are all
either:

1. *Why* comments at lock-discipline / lifecycle-contract sites
   (admission TryAdmit, replayMu rationale block, inTurn routing)
2. Cross-file pointers to durable rationale docs
   (`docs/investigation/bug-5-replay-order.md`)
3. Test-header BDD-style scenario docs (acceptable house style)

No "in the future X" speculation found anywhere in the new comments.

---

## Comments worth keeping as examples — anchor the team on good-comment style

These four comment blocks are exemplars for future PRs to mimic:

### Example 1 — Lock discipline at the field declaration site

`pkg/gateway/websocket.go:144-160` (`replayMu` ordering invariant)

> Writers must NOT snapshot isReplayingLive and then send to the snapshotted
> channel as two separate operations — the drain can empty replayDivertCh
> and disarm the flag between those two steps, orphaning the frame.
>
> replayMu serialises the "read flag + select channel" decision in
> sendRawFrameBytes against the "drain channel + disarm flag" sequence in
> handleAttachSession.

**Why it works:** States the invariant ("Writers must NOT…"), names both
sides of the race in code-identifier terms, and tells the reader where to
look for each half of the invariant. A future maintainer who only reads
this comment can answer "where am I allowed to read `isReplayingLive`?"
without grepping.

### Example 2 — Non-obvious behavioral contract at the method site

`pkg/agent/admission.go:37-46` (`TryAdmit` doc)

> TryAdmit atomically claims a slot for scope. Returns (true, release) when
> the scope is admitted; release MUST be called (typically via defer) when
> the scope's worker exits.
>
> If scope is already active (follow-up turn in an existing session), the
> call always succeeds without consuming an additional slot — the slot was
> already claimed when the worker was first spawned.

**Why it works:** Documents two things the body alone does not make obvious:
(a) the "MUST defer release" lifecycle, (b) the "follow-up turn → reuse
existing slot" semantic. A future caller who reads only the body would
discover both rules through painful debugging.

### Example 3 — Cross-file pointer to a durable rationale doc

`pkg/gateway/websocket.go:1946` and the matching test-file headers
(`pkg/gateway/websocket_replay_order_test.go:49, :204, :244`).

> Traces to: docs/investigation/bug-5-replay-order.md

**Why it works:** The bug-N label is paired with a path to the rationale
doc, so the label remains meaningful even after the branch is merged. This
is the *right* way to preserve traceability without leaving orphan markers.

### Example 4 — Why-not-what at a deceptively idiomatic site

`pkg/agent/loop.go:1625-1626` (`stopSessionWorkers` worker collection):

> Collect first, then cancel — avoids holding sync.Map's range lock while
> cancelling (which could deadlock against concurrent Store calls).

**Why it works:** The code shape ("two loops: collect then act") is itself
not obviously safer than a single `Range` with a callback that calls
`cancel()`. The comment names the deadlock-against-Store risk that justifies
the two-pass shape. Without it, a "simplifier" pass would inevitably
collapse the two loops and regress the bug.

---

## Aggregate counts (pass-2)

- INACCURATE (FIX-CLAIM): 1 residual (release-phase wording in
  admission.go:17 + loop.go:224)
- STALE-RISK (TRIM): 0 — all pass-1 STALE-RISK items resolved (some by
  anchoring to durable docs rather than deletion, which is acceptable)
- WHAT-NOT-WHY: 1 mild residual (session_worker.go:42-46 ctx field doc)
- NIT: 0 new
- NEW PROBLEMS introduced by implementing fixes: 0

Recommendation: fix the v0.2-vs-v0.3 routing wording in admission.go and
loop.go in a one-line follow-up commit before merge. Everything else is at
or above the bar.
