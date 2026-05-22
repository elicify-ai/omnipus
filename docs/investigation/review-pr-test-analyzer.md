# PR Test Coverage Review — `feature/iframe-preview-tier13` (5 bug fixes)

**Summary: Coverage inadequate; 4 critical gaps + 1 build-breaker.**

The five fixes ship with substantial regression coverage across unit, integration, and E2E layers, and the test files demonstrate good DAMP discipline (BDD comments, "Traces to: Bug-N" headers, scenario names). However: (1) `pkg/gateway/sandbox_apply_test.go` will not compile (the `resolveMode` signature changed from 3 to 4 args but the pre-existing tests in that file were not updated); (2) `isRunningInDocker` itself has zero unit tests and the only Docker coverage uses the `OMNIPUS_IN_DOCKER` override — the `/.dockerenv` codepath is never exercised; (3) bug-3 worker lifecycle has gaps (handoff mid-session, idle-timeout while a turn is in-flight); (4) bug-5 has no test for disconnect→reconnect→disconnect→reconnect (only single-cycle); (5) the Playwright spec ships with `.catch(() => {})` swallowing assertion timeouts in two places — load-bearing assertions are silently downgraded.

Hard-constraint #7 ("fix everything, no excuses") is currently violated: the package will not vet/build with the new resolveMode signature.

---

## Bug 1 — Skip onboarding button removed (`src/routes/onboarding.tsx`)

**Load-bearing? YES.** `src/routes/onboarding.test.tsx:103` asserts `queryByRole('button', { name: /skip/i })).not.toBeInTheDocument()` on the Welcome step and `:333-334` adds a dedicated suite that also asserts no `/skip/i` text anywhere. Reverting the fix (re-adding the Skip button) would fail both assertions.

**Tests outcome, not implementation:** YES. Checks the rendered DOM by accessible name and visible text — refactor-resilient.

**Gaps:**
- None critical. The Playwright `(Bug-1-a)` spec adds a second guard at the embedded-SPA layer (path `/onboarding` against the gateway-served SPA — `playwright.config.ts` should be confirmed to point at the Go binary rather than `vite dev`, see "Test Quality Issues" below).
- Minor: no test asserts that the existing user flow (skip-equivalent shortcut via keyboard, deep-link bypass, or stale local-storage `onboarded=true`) still cannot strand the user. Rating 3/10.

**Suggested additions:**
- None (criticality below threshold).

---

## Bug 2 — Model selector search + group (`src/components/ui/model-selector.tsx`)

**Load-bearing? YES.** `src/components/ui/model-selector.test.tsx`:
- `:97-105` filters `'haiku'` → only `claude-3-haiku` visible (would fail before the fix because cmdk's default `shouldFilter` indexes the `value` prop which included provider name);
- `:149-154` asserts `'Anthropic'` and `'OpenAI'` headings render when 2+ providers passed;
- `:129-141` asserts NO heading appears for a single provider;
- `:177-185` asserts searching by provider name does not pull in that provider's models.

Each ties directly to a fix delta in `model-selector.tsx` (`shouldFilter={false}`, custom `filterModel`, `useGrouped = groupsWithModels.length >= 2`).

**Tests outcome, not implementation:** YES. Asserts rendered text and DOM presence, not internal state.

**Gaps:**
- **(rating 6/10) Mixed-case query.** Tests only check lowercase queries. The component lowercases both sides (`queryTrimmed.toLowerCase()`), so a search for `'HAIKU'` should still match — easy to break if anyone removes a `toLowerCase()` call. No assertion guards this.
- **(rating 5/10) "Use ..." custom-slug option in grouped mode.** `:116-121` covers the custom-slug path in flat mode only. The component also renders the custom CommandItem inside the grouped branch (lines 148-160 of the component) — never asserted.
- **(rating 5/10) Keyboard selection via Enter.** Tests click items (via `onSelect`). No assertion that typing + Enter on the cmdk highlighted row fires `onChange` with the model name — cmdk's keyboard navigation behavior depends on `shouldFilter` and would silently regress.
- **(rating 4/10) Empty filter result.** No test verifies `CommandEmpty` ("No models found.") shows when the query matches nothing AND has no custom-slug fallback (e.g. zero-length trimmed input with whitespace).
- **(rating 4/10) Provider with empty `models` array.** Component logic includes `filter((g) => g.models.length > 0)`. No test passes a 3-group input where one group is empty to verify it is skipped (and that 2 non-empty groups still trigger grouped rendering).

**Suggested additions:**
- `it('case-insensitive search matches uppercase query against lowercase model names')`
- `it('grouped mode also shows "Use ..." for an unknown slug')`
- `it('keyboard Enter on highlighted item commits the selection')`
- `it('empty providerGroups entry is skipped from grouped rendering')`

---

## Bug 3 — Per-session workers (`pkg/agent/session_worker.go`, `admission.go`, `loop.go::Run`)

**Load-bearing? PARTIALLY.**

- `session_worker_test.go::TestSessionWorker_TwoSessionsConcurrent` (`:143-188`) — claims to verify the regression but the comment at `:138-142` admits the mock provider returns immediately, so this test would pass before the fix as well (two messages, two replies — even a sequential dispatcher delivers both within 5 s). **It tests dispatch, not concurrency.**
- `session_worker_test.go::TestSessionWorker_FiveParallelSessions` (`:192-235`) — same caveat. Without a slow provider, sequential processing of 5 fast messages finishes well under 10 s.
- `tests/integration/concurrent_sessions_test.go::TestConcurrentSessions_TwoSessions_BothReply` (`:33-96`) — uses the mock-LLM gateway. The mock returns immediately too, so the assertion "both reply within 5 s" would also have passed with a sequential dispatcher. The only test that would genuinely *fail without the fix* is the same-agent variant at `tests/integration/concurrent_sessions_same_agent_test.go` IF the sequential dispatcher's per-message latency × 5 > 8 s — with the mock LLM that's unlikely.
- `TestSessionWorker_AdmissionRejection` (`:240-318`) — **load-bearing.** Uses `blockingMockProvider` so slots stay held, then verifies session 3 receives the capacity-rejection reply. This is the only Go test that actually requires concurrent slot-holding to pass.
- `TestSessionWorker_IdleTimeout` (`:96-131`) — **NOT load-bearing for the production idle timeout.** The test cancels `w.cancel()` directly to simulate exit; it does not wait for `workerIdleTimeout` (60 s) and does not exercise the `idleTimer.C` branch. The test would pass even if the idle-timer logic were entirely deleted from `runLoop`.

**Tests outcome, not implementation:** Mostly yes (assert outbound messages exist), but several tests reach into internals (`al.sessionWorkers.Load(scope)`, `al.admission = newAdmissionController(2)`, `go w.runLoop()` directly). The Close/Cleanup tests at `:322-355` are implementation-coupled — they verify the sync.Map is empty rather than verifying user-observable behavior.

**Gaps (critical):**
- **(rating 9/10) Concurrency is not actually proven.** Need at least one test with a `blockingMockProvider` where session 2's message is sent *while* session 1's provider call is blocked, and session 2 must reply BEFORE session 1's provider releases. Today's tests use the blocking provider only for admission, not for the "two sessions in flight at once" assertion.
- **(rating 9/10) Idle-timeout firing during a turn.** No test verifies that if a turn runs longer than `workerIdleTimeout`, the worker does NOT self-exit mid-turn. The current `runLoop` resets the idle timer on inbox receive but not during `processTurn` execution — a turn that takes >60 s with no follow-up messages would expire the timer, but the timer fires only between selects so it should be safe. This invariant is unverified.
- **(rating 8/10) Handoff changes `agentID` mid-session.** `sessionWorker.agentID` is set at construction (`session_worker.go:40-42`). The comment says "A handoff within the session starts a new worker with the new agent ID" — but no test exercises the path where a session's resolved scope/agent changes between turns. The dispatcher at `loop.go:1549` keys on `scope` only; if `resolveSteeringTarget` returns the same scope but a different `agentID` after handoff, the existing worker keeps the stale ID.
- **(rating 8/10) Subagent admission.** Subagent invocations also call into `processMessage` via `loop.Run` paths. No test verifies whether `admission.OnTurnStart` is incremented for subagents (which would double-count one user-visible turn) or skipped (which would allow runaway parallelism for an agent that spawns subagents).
- **(rating 7/10) `AgentLoop.Close()` with workers mid-turn.** `TestSessionWorker_CloseStopsWorkers` (`:322-355`) starts workers with empty inboxes — they exit immediately on cancel. No test verifies the 5-s budget actually fires when a worker is blocked inside `processTurn` (e.g., a synthetic provider that blocks indefinitely). The `WarnCF "did not drain within shutdown budget"` codepath at `loop.go:1607` is unreachable from the test suite.
- **(rating 7/10) `enqueueSteeringFromMessage` fallback.** `session_worker.go:108-113` says "Falls back to the inbox path if the steering enqueue rejects". No test forces a rejection (steering queue full, no active turn state) to verify the fallback writes to the inbox.
- **(rating 6/10) `inbox` overflow at cap=8.** `enqueue` (`:114-122`) drops the message and logs a warning when the inbox is full. No test asserts the drop counter increments or that the user receives a queue-full reply. Silent drops fail this PR's hard-constraint #7 hidden in plain sight.
- **(rating 6/10) Race on `sessionWorkers` map between create + delete.** Worker `B` could be evicted (`runLoop` defers `Delete(scope)`) while dispatcher is mid-`Load(scope)` and decides to spawn a new worker for the same scope. Two workers could briefly coexist for the same scope. `-race` may not detect this because the writes go through `sync.Map`. No test reproduces this.

**Concurrent-sessions eval (`evals/scenarios/capability/concurrent-sessions.yaml`):**
- **DOES NOT verify the bug-3 outcome.** The YAML defines a single scenario ("Say exactly: '...reply'"). The comment at `:8-12` acknowledges the eval harness lacks a concurrency primitive and the eval will be marked SKIP. As-shipped, this file is documentation only — it cannot fail when bug-3 regresses. Recommend either implementing harness-level concurrency or removing the file to avoid false confidence.

**Suggested additions:**
- `TestSessionWorker_TwoSessionsTrulyParallel` — session A's provider blocks on a chan; session B's message arrives 50 ms later; assert session B's reply arrives BEFORE the chan is closed (proving non-sequential dispatch).
- `TestSessionWorker_IdleTimerDoesNotFireDuringTurn` — block a turn for `workerIdleTimeout + 1s`, assert worker is still alive (not removed from the map).
- `TestSessionWorker_HandoffStartsNewWorkerOrUpdatesAgentID` — exercise a handoff and assert the dispatcher routes the next message correctly.
- `TestSessionWorker_CloseBudgetExceeded` — block a turn indefinitely, assert `Close()` returns within `workerShutdownBudget + grace` and logs the warn.
- `TestSessionWorker_InboxOverflowLogsDrop` — fill inbox to cap+1, assert the dropped count is observable (counter, metric, or log capture).
- Implement (or remove) the eval scenario so a regression is actually caught by `make eval`.

---

## Bug 4 — Docker auto-detect sandbox mode (`pkg/gateway/sandbox_apply.go::resolveMode`, `isRunningInDocker`)

**Load-bearing? PARTIALLY — and the build is broken.**

- **BUILD-BREAKER (rating 10/10).** `pkg/gateway/sandbox_apply_test.go` has 5 call sites for `resolveMode(...)` using the OLD 3-arg signature; the production function now takes 4 args `(cliMode, cfgMode, configTouched, getEnv func(string) string)`. `go vet ./pkg/gateway/` fails with `not enough arguments in call to resolveMode`. The whole package is unbuildable until these are updated.
- `tests/integration/docker_exec_test.go::TestDockerDefault_SandboxMode_IsNotEnforce` (`:46-88`) — load-bearing. Sets `OMNIPUS_IN_DOCKER=1` and asserts the resolved mode is not `"enforce"`. Reverting the fix returns `enforce` and the test fails.
- `tests/integration/docker_exec_test.go::TestDockerDefault_ExplicitModeNotOverridden` (`:101-140`) — load-bearing. Verifies explicit operator config (`sandbox.mode=off` here) wins over Docker auto-downgrade. Good test.

**Tests outcome, not implementation:** YES — uses the `/api/v1/sandbox/config` endpoint to read the resolved mode rather than peeking at internal state.

**Gaps (critical):**
- **(rating 10/10 — build-breaker.) Existing `TestResolveMode_*` tests do not compile** with the new 4-arg signature. Must be updated to pass `nil` (or a `func(string)string` returning `""`) as the 4th argument. Until this is fixed, `make test` cannot pass — hard-constraint #7 violation.
- **(rating 9/10) No unit test for `isRunningInDocker`.** The function (`sandbox_apply.go:178-186`) is reachable only via integration tests, and only via the env-var path. The `/.dockerenv` `os.Stat` branch is *never executed by any test*. A unit test passing a fake `getEnv` returning `""` AND running on a host without `/.dockerenv` would prove "not docker" — and the file-presence path is fundamentally untested.
- **(rating 8/10) `OMNIPUS_IN_DOCKER` set outside Docker.** No test sets `OMNIPUS_IN_DOCKER=1` on a non-Docker host and verifies the env var is treated as the source of truth (downgrade applies even when not actually containerized). This is the documented behavior — make it load-bearing.
- **(rating 8/10) `/.dockerenv` absent inside Docker (rootless, BuildKit, Podman).** This is the explicit "edge cases" item from the scope. Rootless Docker often doesn't drop `/.dockerenv`; Podman drops `/run/.containerenv` instead. The fix only checks `/.dockerenv` and `OMNIPUS_IN_DOCKER` — so a rootless Docker install with no env var will still default to `enforce` and still fail with `fork/exec: permission denied`. No test covers this and the spec note in `sandbox_apply.go:175-177` only mentions standard Docker.
- **(rating 8/10) Operator override via `OMNIPUS_SANDBOX_MODE=enforce` inside Docker.** The comment at `sandbox_apply.go:157-159` describes this override path ("env tag on cfg.Sandbox.Mode") but no test verifies that `OMNIPUS_SANDBOX_MODE=enforce` + Docker results in enforce (the existing `TestDockerDefault_ExplicitModeNotOverridden` covers the *config-file* override only, not the env-var override).
- **(rating 7/10) `--sandbox=enforce` CLI flag inside Docker.** Per the CLI > config > default precedence, CLI should win even inside Docker. Not tested.
- **(rating 5/10) `OMNIPUS_IN_DOCKER` set to something other than `"1"` (e.g., `"true"`, `"yes"`, `"0"`).** The implementation strictly compares `== "1"`. No test pins this contract.

**Suggested additions:**
- Fix the 5 compile errors in `sandbox_apply_test.go` by passing `func(string) string { return "" }` (or `os.Getenv`) as the 4th argument.
- `TestResolveMode_DockerAutodetectViaEnv` — pure unit test for resolveMode with `getEnv("OMNIPUS_IN_DOCKER") == "1"`.
- `TestIsRunningInDocker_EnvSignal` and `TestIsRunningInDocker_DockerenvFile` — unit tests for both branches of `isRunningInDocker` (the file branch can use a fake stat shim or be skipped on non-Linux).
- `TestResolveMode_DockerAutodetect_CLIOverride` — `OMNIPUS_IN_DOCKER=1` + `cliMode="enforce"` → result is `enforce`.
- `TestResolveMode_DockerAutodetect_ConfigExplicitEnforce` — `OMNIPUS_IN_DOCKER=1` + `cfgMode="enforce"` → result is `enforce` (operator opted in, source=`"config"`).
- Add a TODO or note in `isRunningInDocker` that Podman/rootless are not auto-detected, so operators get a discoverable error path rather than silent enforce-mode breakage.

---

## Bug 5 — Replay/event order on reconnect (`pkg/gateway/websocket.go`)

**Load-bearing? YES (mostly).**

- `pkg/gateway/websocket_replay_order_test.go::TestReplay_DivertedLiveFramesArriveBeforePostReplayFrames` (`:50-193`) — **load-bearing.** Pre-loads `replayDivertCh` with a buffered frame, arms the divert, then runs a goroutine that fires `sendConnGenFrame` repeatedly during `handleAttachSession`. The invariant `postDoneBeforeBuffered == false` is exactly the bug-5 condition. Reverting the drain-before-disarm order would cause the racing post-done frame to interleave before the buffered frame. Good test.
- `TestReplay_DivertFlagClearedAfterDrain_FlagState` (`:205-231`) — checks the post-condition `isReplayingLive == false`. Light but useful.
- `TestReplay_DivertDrainedBeforeFlag_OrderWithRealConcurrency` (`:245-318`) — verifies all 3 pre-loaded divert frames appear in `sendCh` after attach completes. Good.
- `TestWsStreamer_Update_RespectsReplayDivert` (`:331-360`) and `TestWsStreamer_Update_DirectToSendChWhenNotReplaying` (`:372-399`) — Fix-B coverage (token frames route through the divert path). Both load-bearing.
- `tests/integration/replay_ordering_test.go::TestReplayOrdering_ToolCallStartBeforeResult` (`:49-106`) — seeds a transcript with `tool_call_start + tool_call_result`, asserts start precedes result. Reasonable, but the bug was about post-replay ordering vs pre-replay ordering, and this just verifies the saved transcript replays in transcript-order (no divert race involved). Less load-bearing than the unit test.
- `TestReplayOrdering_EarlierTurnBeforeLaterTurn` (`:117-159`) — multi-turn ordering invariant. Good.

**Tests outcome, not implementation:** Unit tests reach into `wsConn{}` fields directly (`sendCh`, `replayDivertCh`, `isReplayingLive`). Integration tests use the WS protocol (better). The unit test setup is tightly coupled to the `wsConn` struct layout — any refactor of `wsConn` to use a single channel + tagged frames would force a test rewrite even if behavior is identical.

**Gaps:**
- **(rating 9/10) Disconnect → reconnect → disconnect → reconnect.** The whole scope item is unaddressed. The current unit and integration tests do a single `attach_session` cycle. A second cycle (the user navigates away again mid-replay) is the most likely source of latent bugs — `replayDivertCh` allocation is lazy (`websocket.go:1037`), `isReplayingLive` is per-connection, but state across two attach cycles on the same wc is untested.
- **(rating 8/10) Late-arriving live frame during replay drain.** The unit test pre-loads `replayDivertCh` before arming. The actual production path is: replay drains while the eventForwarder goroutine is *still pushing into* `replayDivertCh`. No test fires divert writes from another goroutine *during* the drain loop. The drain stops on `default:` (empty channel) — a frame arriving 1 µs later goes straight to `sendCh` and could land before the next drain iteration would have placed it. This is the production-realistic race; the test simulates only the pre-loaded variant.
- **(rating 8/10) `replayDivertCh` full / frame larger than channel buffer.** `replayLiveBufferCap = 1000`. Push 1001 frames during replay — what happens? The code likely drops or blocks; no test pins the behavior. A long-running tool-call burst could exceed 1000 frames easily.
- **(rating 6/10) Replay completes with zero buffered frames.** Sanity case — replay finishes, drain finds nothing, normal flow resumes. Tested incidentally by `_FlagState` but no positive assertion that no spurious frames appeared.
- **(rating 6/10) `streamReplay` error path.** What if `streamReplay` returns an error mid-replay? Does the divert get drained anyway? The defer logic should handle it but no test forces the error path.
- **(rating 5/10) E2E navigates via `page.goto('/#/agents')` and back.** The Playwright `(Bug-5-a)` test uses `goto` rather than a soft `Link`/`router.navigate` — `goto` is a full page reload which forces a fresh WS connection. That's not the production "navigate within SPA" path. A SPA-internal route change uses the same WS but a new attach_session frame — `goto` does NOT exercise that. The test is closer to a refresh-test than a navigation test.

**Suggested additions:**
- `TestReplay_TwoConsecutiveAttachCycles` — attach → buffer frames → finish → attach again → buffer different frames → verify ordering invariants on both cycles.
- `TestReplay_LiveFrameArrivesDuringDrain` — push divert frames from a goroutine while the drain is running (`sync.WaitGroup` synchronization).
- `TestReplay_DivertChannelOverflow` — push `replayLiveBufferCap + 100` frames, assert documented behavior (drop with metric / block with deadline / etc.) — and *that the test fails if the implementation silently drops*.
- `TestReplay_StreamReplayError_StillDrainsDivert` — inject an error into `streamReplay` and assert `isReplayingLive` is still cleared and pending divert frames still reach `sendCh`.
- Convert Playwright `Bug-5-a` to use SPA-internal navigation (`page.click` on a router link) rather than `page.goto` so the WS path stays open across navigation.

---

## Test Quality Issues (cross-cutting)

1. **`tests/e2e/bug-regression.spec.ts:55-58` silently swallows the assertion.** The `.catch(() => {})` after the `await expect(skipButton).not.toBeVisible({ timeout: 5_000 })` block converts an assertion timeout into a pass. The follow-up `count()` checks at `:61-71` re-do the check correctly, so the test isn't broken — but the swallowed-catch pattern hides intent and is a red flag for future maintainers. Fix: remove the `.catch` and let the assertion fail on its own.

2. **`tests/e2e/bug-regression.spec.ts:137-153` wraps assertions in `.catch((e) => { throw new Error(...) })`.** This is a custom error message wrapper, fine in isolation, but it loses the Playwright timeout context (last-rendered DOM snapshot, etc.). Prefer `await expect(...).toHaveCount(N, { timeout })` and let Playwright's native error machinery report.

3. **`pkg/agent/session_worker_test.go::TestSessionWorker_IdleTimeout` does NOT test the idle timer.** As noted in Bug-3 above, it calls `w.cancel()` instead of waiting for the timer. The test would pass even if every `idleTimer.Reset`, `<-idleTimer.C`, and `workerIdleTimeout` reference were deleted from `session_worker.go`. The test name is misleading.

4. **`tests/integration/concurrent_sessions_test.go::TestConcurrentSessions_TwoSessions_BothReply` proves dispatch, not concurrency.** The mock LLM returns immediately, so even a sequential dispatcher passes the 5-second deadline. The variable name `concurrent` and the BUG-3 comments suggest the test guards against starvation, but starvation only manifests with a slow provider. The actual load-bearing test for bug-3 is the admission-rejection test in the unit suite — and even that proves slot-counting, not session-level parallelism. **The most fundamental bug-3 invariant — "session B replies before session A's slow turn finishes" — is currently unverified at any layer.**

5. **Tight time margins.**
   - `session_worker_test.go:294 — time.Sleep(150 * time.Millisecond)` — flaky on a loaded CI runner. Should use a synchronization primitive (`provider-acquired-slot` channel).
   - `tests/integration/concurrent_sessions_same_agent_test.go:36 — replyTimeout = 8 * time.Second` — generous, fine.
   - `session_worker_test.go:65,153,202 — Run() did not stop within 3 s` — borderline; CI cold-start can push beyond.

6. **Playwright `(Bug-3-a)` uses `newPage()` in the same context with shared storage state.** Both tabs share the pre-authenticated admin session — fine for the bug being tested, but if the SPA caches a session ID in storage, both tabs could attach to the same session and the test would still pass while actually masking the bug. Verify the SPA mints unique session IDs per tab.

7. **Playwright spec testing target.** The Playwright config wasn't read here, but the test scope says "tests the actual rendered UI on the embedded SPA, or against the Vite dev server" — CLAUDE.md mandates embedded-SPA testing. Confirm `playwright.config.ts` `baseURL` points at the gateway port (5000) and not Vite (3000/5173), and that `global-setup.ts` builds + embeds the SPA before the tests run. If it doesn't, every E2E in this PR is testing the wrong artifact.

8. **Direct internal-state coupling in `session_worker_test.go`** (`al.sessionWorkers.Load`, `al.admission = newAdmissionController(2)`, `go w.runLoop()` outside the production dispatcher) — these will break on the next refactor of the worker lifecycle. Acceptable for unit tests but the coupling should be flagged for the v0.3 rooms-redesign.

---

## Well-written tests (use as templates for future work)

1. **`pkg/gateway/websocket_replay_order_test.go::TestReplay_DivertedLiveFramesArriveBeforePostReplayFrames`** — exemplary: BDD comment, "Traces to:" header, an adversarial concurrent goroutine that forces the race window open, a single clear failure assertion with a constructed frame-sequence dump in the failure message. Use as the template for any "ordering across concurrent writers" regression test.

2. **`pkg/gateway/websocket_replay_order_test.go::TestWsStreamer_Update_RespectsReplayDivert` / `_DirectToSendChWhenNotReplaying`** — paired positive/negative tests for the same conditional branch. Clean, behaviour-focused, no internal-state assertions beyond the channel contents. Template for testing a flag-controlled dispatch path.

3. **`tests/integration/docker_exec_test.go::TestDockerDefault_ExplicitModeNotOverridden`** — tests the "fix doesn't trample explicit config" invariant. Often missing from auto-detect features; here it's first-class. Template for any auto-detect/auto-default feature: always pair with an explicit-override test.

4. **`src/routes/onboarding.test.tsx::OnboardingWizard — step navigation`** — clean structure: each `it()` has a "Traces to:" reference, a BDD `Given/When/Then` comment, and a single load-bearing assertion. `beforeAll` caches the dynamic component import to avoid 20s-per-test transform cost — a real production benefit worth copying.

5. **`pkg/agent/admission_test.go`** — small, fast, focused unit tests of an isolated controller. No mocks, no goroutines, no time-based flakiness. Good template for testing pure logic components extracted from a larger module.

6. **`src/components/ui/model-selector.test.tsx::ModelSelector — search by model name :: shows "Use ..." custom option when query has no exact match`** — guards the custom-slug fallback behaviour, a corner of the component most likely to break under refactor. Template for "verify the escape hatch still exists" tests.
