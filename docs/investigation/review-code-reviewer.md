# Code Review — feature/iframe-preview-tier13 (5-bug batch)

**Summary: 14 issues found, 3 blocking, 6 should-fix, 5 nits.**

Scope reviewed: the 5-bug working-tree diff plus the new files (`pkg/agent/admission.go`, `pkg/agent/session_worker.go`, `pkg/gateway/websocket_replay_order_test.go`, `tests/integration/*.go`, `tests/e2e/bug-regression.spec.ts`, `evals/scenarios/capability/concurrent-sessions.yaml`, `docs/investigation/bug-5-replay-order.md`, `src/components/ui/model-selector.test.tsx`). The full origin/main..HEAD diff was inspected only for cross-cutting interaction with the 5-bug files.

---

## BLOCKING

### 1. `pkg/agent/loop.go:1554-1576` — admission cap is bypassed by every follow-up turn in an existing session

The dispatcher checks `al.admission.ShouldAdmit()` only on the "no worker yet" branch (line 1554). When a worker already exists for the scope (line 1549), every subsequent message is enqueued straight onto the worker's inbox with no admission check. A single chatty session can pin a slot indefinitely while N other sessions are turned away with the capacity reply.

`OnTurnStart`/`OnTurnEnd` (session_worker.go:209-210) correctly track the in-flight count, but `ShouldAdmit` is the only place that gates new work, and it is unreachable for in-session follow-ups. The asymmetry also means soft-cap behaviour is sticky in time: once you've crossed the cap, every new *session* is rejected but existing sessions can still queue unbounded follow-ups, which is precisely the load shape (a few long-running sessions) the soft cap is meant to protect against.

**Fix:** Run `ShouldAdmit` inside `sessionWorker.processTurn` *before* `al.admission.OnTurnStart()`. When it returns false, publish the same capacity-rejection reply and skip the turn. Alternatively, move the admission decision into a single chokepoint (`processTurn` start) and delete the dispatcher-side check entirely — that removes the duplicated user-visible reply logic too.

### 2. `pkg/gateway/websocket.go:1130-1164` + 1858-1869 — drain-before-disarm still races with `wsStreamer.Update`

The fix's correctness comment (lines 1141-1147) claims "no new items can enter `replayDivertCh` after the select hits default" because `isReplayingLive` is still true. That guarantee is false for any caller that uses a snapshot-then-write pattern (`wsStreamer.Update` lines 1861-1869, `sendCancelStageFrame` lines 884-886, and any future caller that reads the flag once and stores `targetCh`). Concrete interleaving:

1. Writer A (Update): `targetCh = replayDivertCh` (after seeing `isReplayingLive == true`).
2. Writer A is descheduled before executing `select { case targetCh <- data: }`.
3. Drain loop: `case raw := <-replayDivertCh: ... default: goto drainDone` — fires `default` because the channel is empty at this instant.
4. Drain disarms: `wc.isReplayingLive.Store(false)`.
5. Writer A resumes and completes the send to `replayDivertCh`.
6. That frame is now orphaned — nothing drains `replayDivertCh` again until the next `attach_session`, which may be never. The client loses a token.

`sendRawFrameBytes` (line 1342-1343) has the same shape but is one statement closer together; the race window is smaller but not closed. Bug-5 fix B (route `Update` through `sendRawFrameBytes`) was applied partially — the duplicated fast path at line 1859-1869 reintroduces the bypass it was supposed to eliminate.

**Fix (one of):**
- Drop the fast-path entirely from `Update` and `sendCancelStage`; always go through `sendRawFrameBytes`, which centralises the divert decision and avoids the cross-statement TOCTOU.
- Or, keep the fast path but read `isReplayingLive` *after* the channel send completes (e.g. by always sending into a sentinel and letting `writePump` route based on the flag). The current "snapshot then send" pattern is unfixable while two channels are externally exposed.
- Either way: update the comment at lines 1141-1147 to stop claiming the drain is safe — the guarantee depends on the writer using `sendRawFrameBytes`, which is not a guarantee any other goroutine can be forced to honour.

### 3. `src/components/ui/model-selector.tsx:49 + 152` — custom model entry is lower-cased before save

`queryTrimmed` is computed as `query.trim().toLowerCase()` (line 49) and then handed straight to `handleSelect` for the custom-model fallback (line 152: `onSelect={() => handleSelect(queryTrimmed)}`). Provider model slugs are case-sensitive in many cases (`MiniMax-M2.7`, `Claude-3-Opus`, `Llama-3.1-70B-Instruct`); typing one of these into the search box and choosing "Use 'x'" persists the lower-cased form, which then fails at the provider call site or quietly resolves to the wrong route. The placeholder in the text-input fallback at line 34 even shows `MiniMax-M2.7` as an example — directly contradicting what the combobox path produces.

The displayed "Use 'x'" label at line 156 also shows the lower-cased form, so the user has no visual cue that case was dropped.

**Fix:** Keep two separate values: a `queryLower` used only for filtering/`exactMatch`, and the raw `query.trim()` used in `handleSelect` and the display label. Add a test `'preserves case when entering custom value'` to lock the behaviour.

---

## SHOULD-FIX

### 4. `pkg/agent/loop.go:1530-1545` — system messages and unroutable messages spawn unbounded goroutines

`if msg.Channel == "system"` and the `if !ok` unroutable branch both call `go func() { ... }()` with no admission control, no cancellation hook into `stopSessionWorkers`, and no rate limit. A burst of malformed or system messages spawns an arbitrary number of goroutines that survive past `Run()`'s `return nil` — `stopSessionWorkers` only sees the registered session workers. Combined with finding 1, this gives an attacker / buggy channel an unbounded goroutine path that bypasses every cap.

**Fix:** Either route these through the session-worker dispatch (creating a per-`msg.Channel` worker when one doesn't exist) or maintain a `sync.WaitGroup` and call `Wait()` in `stopSessionWorkers` so shutdown blocks until they drain. At minimum, gate the system/unroutable goroutines on `al.admission.ShouldAdmit()` and reject when over cap.

### 5. `pkg/gateway/sandbox_apply.go:445-456` — `disabledBy="docker_autodetect"` never reaches the boot log

When `resolveMode` returns `(ModePermissive, "docker_autodetect", nil)`, the value is stored in `result.DisabledBy` (line 270) but is *not* included in the `slog.Warn("sandbox.permissive", ...)` call at lines 448-455. The operator sees a permissive nag banner and a `sandbox.permissive` audit line but no indication that the cause was Docker detection vs. an explicit operator choice. CLAUDE.md asks specifically for clarity on this. This is also the audit signal that lets ops triage "is this the Docker default or did somebody change config".

**Fix:** Add `"disabled_by", disabledBy` to the slog call at line 448 (and to `sandbox.applied` at line 458 for symmetry). At a separate log point right after `resolveMode` returns, emit `slog.Info("sandbox.mode_resolved", "mode", ..., "disabled_by", disabledBy)` so the decision provenance is on a single line.

### 6. `pkg/agent/session_worker.go:101-123` — full-inbox drop is silent message loss

When `inTurn=false` and the inbox is full (`select default` at line 116), the message is logged at WARN and discarded. The user gets no reply, no error, no retry. This violates the spirit of Hard Constraint #7 (no silent failure): the user *sent* a message and it disappeared. The dispatcher in `Run()` (loop.go:1565-1572) sends a polite capacity reply when admission rejects — the inbox-overflow path should do the same.

The inbox-full case is also reachable in *non-pathological* situations: when a user rapid-fires 9+ messages between turns (the cap is `workerInboxCap = 8`).

**Fix:** Publish an outbound `OutboundMessage` with content "Your message could not be queued; try again in a few seconds." instead of just logging. Or block briefly (e.g. 100 ms) with a timeout before declaring the inbox full.

### 7. `pkg/agent/session_worker.go:102-113` — `inTurn` check is not atomic with the steering-enqueue side effect

`enqueue` reads `w.inTurn.Load()` (line 102), then calls `enqueueSteeringFromMessage` (line 109). Between those two lines, `processTurn`'s deferred `w.inTurn.Store(false)` (line 181 LIFO) can fire — meaning a message that was supposed to be a *steering* late-append (because the turn is still running) ends up enqueued via the steering queue *after* the turn has just ended. The post-turn drain at lines 243-269 will then process it correctly, but the symmetric race in the other direction also exists: `inTurn=false` reads happen at the moment processTurn is about to set it to true (line 180), and the message goes to the inbox rather than steering, producing a duplicate fresh-turn instead of an append.

**Fix:** Hold `inTurn` as a state of the worker (`{idle, running}`) under a small mutex, or wrap the read+enqueue in a single critical section. The current pattern is racy by construction; tests pass only because the mock LLM returns instantly.

### 8. `pkg/gateway/websocket_replay_order_test.go:50-193` — primary regression test cannot fail without the fix

The "load-bearing" test pre-populates `replayDivertCh` *before* calling `handleAttachSession`, then `handleAttachSession` itself sets `isReplayingLive=true`. The concurrent goroutine (lines 96-117) calls `sendConnGenFrame` *while the flag is true* — so its frames also land in `replayDivertCh`. The drain reads them all. There is no point in the test where the drain races a writer that has already snapshotted `targetCh=sendCh`. Reverting the fix (swapping `Store(false)` back before the drain) does not make this test fail — the race the test claims to cover (post-disarm writer arriving during the drain) is not actually exercised.

A test that *would* be load-bearing: hold a writer goroutine that calls `Store(false)`+immediate send into `replayDivertCh` from outside the divert path, or stub `sendRawFrameBytes` to inject a sleep between the flag-read and the channel send to simulate scheduling.

**Fix:** Rewrite the test using a controllable scheduling primitive (e.g. a goroutine that snapshots `targetCh` while `isReplayingLive=true`, then waits on a channel for the test to signal "now send", and the test signals after `Store(false)` has been called). Confirm the test fails on the pre-fix code (`git revert` the websocket.go diff, run; must FAIL). If it doesn't fail, the test is decorative.

### 9. `tests/integration/replay_ordering_test.go:49-159` — integration test asserts the wrong invariant for bug 5

Both `TestReplayOrdering_ToolCallStartBeforeResult` and `TestReplayOrdering_EarlierTurnBeforeLaterTurn` seed a static transcript, attach via WS, and verify the replay frames come out in their persisted order. This is a replay-fidelity test, not a bug-5 test. Bug 5 was about live-frame ordering during the divert/drain window; this test has no live producer at all. The unit test at `pkg/gateway/websocket_replay_order_test.go` is the only one that even attempts to cover the divert race (and per finding 8, it does not actually exercise it).

**Fix:** Either rename these tests to "TestReplayFidelity_*" so they don't mislead, or extend them to drive a concurrent live producer during attach (e.g. spawn a goroutine that triggers a tool-call via the REST API while attach is replaying, then assert the live frame appears after the drained frames). The current naming pretends to cover bug 5; in practice it covers nothing the existing replay test suite doesn't already cover.

---

## NITS

### 10. `pkg/agent/admission.go:25` — local variable shadows built-in `cap`

`cap := softCap` shadows Go's built-in `cap`. golangci-lint with `predeclared` enabled flags this. Rename to `softCapV` or `effective`.

### 11. `pkg/gateway/sandbox_apply.go:178-187` — Docker detection has no Podman / OCI handling and no docs

`isRunningInDocker` checks `/.dockerenv` only. Podman drops `/run/.containerenv` (note `/run`, not `/`), some OCI runtimes drop neither, and rootless Podman behaves identically to default Docker for the seccomp-blocked-syscall problem. The function name "isRunningInDocker" is also misleading — the *symptom* (default-seccomp blocks RLIMIT_NPROC / prctl / Landlock) is container-runtime-agnostic. The function should be `isRunningInRestrictiveContainer` or similar, check `/run/.containerenv` too, and the comment block at lines 174-177 should call out the fallback escape hatch (`OMNIPUS_IN_DOCKER=1` works for Podman by accident).

### 12. `evals/scenarios/capability/concurrent-sessions.yaml:28-29` — references a sibling scenario that does not exist

The rubric says "this scenario is always run concurrently with `capability.concurrent-sessions-b`" but `concurrent-sessions-b.yaml` is not in the directory. The eval harness comment at lines 7-12 also acknowledges that the harness lacks concurrency primitives. As written this scenario is decorative — it's a single-session run that can never test concurrency. Either commit the sibling scenario + a harness change, or replace the file with a comment in `docs/` that says "this regression is covered by Go integration tests, see tests/integration/concurrent_sessions*.go" — don't ship a YAML that pretends to be runnable.

### 13. `pkg/gateway/websocket_replay_order_test.go:1` — `//go:build !cgo` constraint silently skips test when CGO is enabled

The build tag follows the existing pattern in `replay_test.go:1`, so this is not a new sin — but the bug-5 regression test is exactly the kind of test that should be unconditional. The CLAUDE.md quality gate runs with `CGO_ENABLED=0`, but the production build is `CGO_ENABLED=1` (the cgo gateway). Run the test under both. If the constraint is only there to share `replayFrameDecoder` from `replay_test.go`, move the decoder into a non-test file (or `_test_helpers.go` with no build tag) and drop the constraint.

### 14. `src/routes/onboarding.test.tsx:51 + 81` — `completeOnboarding` mock remains but is no longer called

The mock setup at line 51 and the `mockResolvedValue` at line 81 still wire up `completeOnboarding`, but the welcome step no longer calls it (the Skip button is gone). Harmless dead code; remove the mock and the import at line 62 to keep the test file tidy.

---

## Couldn't review

- **SPA behaviour at runtime**: per the prompt, I did not run the embedded SPA or Playwright. Bug-1 / Bug-2 frontend tests were read but not executed.
- **Verifying the bug-5 fix end-to-end**: requires a running gateway with a slow LLM and a controllable disconnect window. The race I describe in finding 2 is provable by code reading but not by the regression suite as committed (finding 8).
- **`pkg/agent/loop.go` cross-cutting interaction with the rest of the file**: I read the dispatcher rewrite and `Close()` changes, but I did not audit every existing callsite of `pendingSteeringCountForScope`, `Continue`, `processMessage` to verify the per-session worker assumption holds for cancellation, hooks, the `RunOnce` path, and the steering test cohort outside `session_worker_test.go`. The architect / backend-lead review should sweep these.
- **`/.dockerenv` false positives on real platforms**: I noted Podman in finding 11 but did not enumerate (LXC, systemd-nspawn, GitHub Actions runners — some of which mount `/.dockerenv` via the docker daemon they're running). Worth a follow-up scan.
- **Perf JSON results under `tests/perf/results/`**: excluded by the prompt.
