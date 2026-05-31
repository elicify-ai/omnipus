# Architecture Review: 5-Bug Fix Batch + Per-Session Worker Pattern

**Reviewer:** architect
**Branch:** `feature/iframe-preview-tier13`
**Date:** 2026-05-22
**Status:** APPROVE WITH FOLLOW-UPS (no blockers; two SHOULD-FIX before merge; rest tracked for v0.2)

## Summary

The per-session worker pattern (Bug 3) is the only structurally significant change in this batch; everything else is a localized fix. The worker model is well-scoped — `sessionWorker` is a thin layer that delegates to existing `AgentLoop.processMessage / Continue` rather than reinventing turn execution, so the existing hookManager / eventBus / channelManager / browserMgr / DevServerRegistry singletons need no changes and don't get cloned per worker. Scope-key derivation in `enqueueSteeringFromMessage`, `buildContinuationTarget`, and `pendingSteeringCountForScope` is consistent (all use `route.SessionKey` from the steering side, while the worker map uses the longer `:SessionID`-suffixed form — those are two intentionally different keys). The replay-order fix (Bug 5) is correct on its happy path but has one residual blocking-send concern under back-pressure. The Docker sandbox auto-detect (Bug 4) does the right thing but its detection signal is too narrow for the deployment matrix Omnipus claims. The admission controller (Bug 3 supporting infra) has two non-blocking-but-real holes that are appropriately deferred to v0.2 (#175) — one of which (subagent spawns bypass admission entirely) should be documented in the deferral so v0.2 picks it up.

The lead's instinct to defer resource-aware admission to v0.2 is correct: `NumCPU()*4` buys enough headroom for the v0.1 release and the goroutine cost of an idle worker is in the hundreds of bytes range, well under the 10 MB CLAUDE.md #3 budget. The main risk is goroutine accumulation under slow-loris-style WS abuse, which is also appropriately tracked for v0.2.

---

## Findings

### 1. WS capacity rejection bypasses the existing `SystemOverloadFrame` contract

**Severity:** SHOULD-FIX before merge
**Component:** `pkg/agent/loop.go:1564-1574`

The admission rejection path publishes a plain `bus.OutboundMessage{Content: "I'm at capacity right now…"}`. For WebSocket clients this lands as an ordinary chat text frame in the stream. But the contract already defines a typed `SystemOverloadFrame` (`contracts/components/schemas/SystemOverloadFrame.yaml`, `WsFrameType=system_overload`) for exactly this signal — and the SPA already handles it via `src/store/chat.ts:1759` and surfaces it as a toast rather than chat content. The contract was added under FR-016/MAJ-009 specifically for the "system at capacity, action blocked" case.

Today: the rejection text inflates the transcript and looks like an agent reply.
Should: emit a `SystemOverloadFrame` on WS (and only fall back to a chat-text reply on non-WS channels like Telegram/Slack/etc. that have no typed-frame concept).

**Recommendation:** Push capacity rejection through the WS streamer if a connected WS session can be resolved from `msg.Channel + msg.ChatID`; fall through to the existing `OutboundMessage` text path otherwise. This avoids polluting transcripts on the primary surface and reuses the existing toast UI. CLAUDE.md hard-constraint #8 is technically satisfied either way (no new wire type is introduced), but the spirit — "wire types are the single source of truth, use them" — points at the typed frame.

**Acceptance:** A WS session that gets rejected receives `{"type":"system_overload","session_id":"…","message":"…"}` rather than a `token`/`done` text pair. A Telegram/Slack channel that gets rejected still gets a plain text reply (no behavior change).

---

### 2. Subagent spawns bypass the admission counter

**Severity:** SHOULD-FIX before merge (document at minimum; ideally fix)
**Component:** `pkg/agent/subturn.go:459-501`, interaction with `pkg/agent/admission.go`

`sessionWorker.processTurn` calls `al.admission.OnTurnStart()` / `OnTurnEnd()` so user-message turns are counted. `runAgentLoop` (called by sub-turns) does NOT. A parent agent that spawns 10 concurrent subagents during one user turn consumes one admission slot. With the spawn tool the multiplier can be arbitrarily large in principle.

This is not an immediate crash risk because:
- `subagentManager` is per-agent and the spawn tool has its own concurrency bookkeeping (`childTS.concurrencySem`).
- The activeTurns counter is just a gate, not a hard limit; over-counting/under-counting changes the *new-session* admission decision, not in-flight turn behavior.

But it means an active long-running fan-out parent can consume far more goroutines + RAM than the soft cap (`NumCPU()*4`) suggests. That's exactly the failure mode the cap was supposed to prevent.

**Recommendation:** Two paths, in order of preference:
1. (preferred) Wrap `runSubTurn` (or `runAgentLoop`) with `admission.OnTurnStart/OnTurnEnd` calls but DO NOT gate on `ShouldAdmit` for sub-turns — once admitted, a turn's children execute. Accounting matches reality.
2. (lighter touch) Document in `admission.go` that the counter tracks dispatcher-level user turns only, and add the proper accounting to the v0.2 #175 plan as a known item.

Either is acceptable for v0.1 if it's an *explicit* decision documented in the admission.go comment. Today, the comment says the counter "tracks the number of concurrently active session turns" — which subagent spawns would naturally fall under.

---

### 3. Docker auto-detect detection signal is narrow

**Severity:** FUTURE (v0.2 #155 follow-up)
**Component:** `pkg/gateway/sandbox_apply.go:178-186`

`isRunningInDocker` checks two signals: `OMNIPUS_IN_DOCKER=1` env override and `/.dockerenv` presence. This correctly catches:
- Docker (which always drops `/.dockerenv`).

It MISSES:
- **Kubernetes pods** running with the default runc/containerd runtime — these do not drop `/.dockerenv`. The hardened-exec syscall set fails identically inside a default unprivileged k8s pod because k8s applies the same containerd seccomp profile that Docker does. An operator running `kubectl run omnipus --image=omnipus:tag` against the published image gets `OMNIPUS_SANDBOX_MODE=permissive` from the Dockerfile env (line 73), but anyone building their own image without that env gets enforce → fails on every exec.
- **Podman default mode** — Podman 4.x in rootless mode DOES drop `/.dockerenv` (per upstream issue 4757 since ~v3.x). So this auto-detect probably works there. Worth a comment.
- **GitHub Actions container jobs**, GitLab runner Docker executors, dev containers (`.devcontainer.json`) — most of these inherit Docker's `/.dockerenv`. OK.

The Dockerfile bakes `OMNIPUS_SANDBOX_MODE=permissive` (`docker/Dockerfile:73`) so the OFFICIAL image is covered regardless of detection. The auto-detect is for users running `docker run -it omnipus-binary ./omnipus gateway` from an arbitrary base image, or k8s pods built from non-official images.

**Recommendation:** Add a Linux-only secondary signal: check `/proc/1/cgroup` for `containerd`, `kubepods`, `docker`, `podman` substrings. If any match, set the same `docker_autodetect` path. Name the signal more honestly (`container_autodetect` or `containerized_autodetect`) since the trigger isn't Docker-specific.

This is not a v0.1 blocker because the official Dockerfile carries the env. Worth a tracked issue.

**Future ADR seed:** "Container-runtime autodetect: signals & matrix" — enumerate the runtime matrix Omnipus supports, the per-runtime detection signal, and what falls back when no signal fires.

---

### 4. Replay drain has a back-pressure deadlock window (sendCh full + dead client)

**Severity:** SHOULD-FIX (defensive — low probability but unbounded latency)
**Component:** `pkg/gateway/websocket.go:1148-1164`

The drain loop:
```
for {
    select {
    case raw := <-wc.replayDivertCh:
        select {
        case wc.sendCh <- raw:          // ← BLOCKING send
        case <-ctx.Done(): …
        }
    default:
        goto drainDone
    }
}
```

While `isReplayingLive` is still `true`, the drain blocks on `wc.sendCh <- raw` if `sendCh` is full. `sendCh` capacity is 256. If the client is slow and `writePump` is blocked on the wire, sendCh can fill. While the drain is blocked, NEW live frames keep going into `replayDivertCh` (which has capacity 1000), and the readLoop / event forwarder threads continue to enqueue. If `ctx.Done()` never fires (client is slow but not dead — TCP back-pressure rather than disconnect), the drain blocks indefinitely, and `isReplayingLive` is never cleared.

Consequences:
- `replayDivertCh` will eventually fill (capacity 1000) → new frames get dropped via `sendConnGenFrame`'s already-degraded path (a system_overload-style fallback per the existing comment at line 40).
- The connection stays in "still replaying" state indefinitely from the SPA's perspective.
- When the slow client eventually catches up, the drain proceeds but the SPA may have already given up.

**Recommendation:** Either:
1. Use a non-blocking send with a small deadline (e.g. 1 second per frame) on the drain — if exceeded, log degraded and bail out, accepting some replay frames will be lost rather than blocking the connection state machine forever.
2. Track the drain in a timeout watchdog: if the drain hasn't finished within `2 * len(replayDivertCh) * 5s` (the per-frame timeout `emitFn` uses), force-disarm.

Option 1 is simpler and aligns with the already-degraded back-pressure semantics elsewhere in this file.

**Acceptance:** A unit test that fills `sendCh`, fills `replayDivertCh`, calls `handleAttachSession`, asserts the function returns within a bounded time (e.g. 30 s) rather than blocking indefinitely.

---

### 5. `disabled_by="docker_autodetect"` is not surfaced in the permissive INFO log

**Severity:** FUTURE (operability)
**Component:** `pkg/gateway/sandbox_apply.go:440-456`

When mode resolves to permissive via `docker_autodetect`, the `sandbox.applied` (permissive branch) INFO log at line 440-456 does NOT include `disabled_by`. The `/health` endpoint info map at line 512-514 does include it. So:

- An operator reading `gateway.log` sees `sandbox.applied mode=permissive backend=linux …` and cannot tell whether they configured permissive (their decision) or got auto-downgraded (the boot path's decision).
- An operator hitting `/health` sees `disabled_by=docker_autodetect` and can tell.

Operationally this is annoying enough to fix as a one-line change: add `"disabled_by", disabledBy` to the permissive log line when `disabledBy != ""`. The "OFF" log line at 281-284 already does this — copy the pattern.

**Recommendation:** Fix as a follow-on commit on this branch (zero-risk one-liner).

---

### 6. Mid-handoff steering message can be stranded under the new agent's scope

**Severity:** FUTURE (edge case — appropriately deferred)
**Component:** `pkg/agent/session_worker.go:174-275`, `pkg/agent/steering.go:184-213`

Sequence:
1. Mia is mid-turn (mia-worker's `inTurn=true`).
2. Inside the turn, mia calls the `handoff` tool → `sessionActiveAgent[session:sid] = "jim"`.
3. A new user message arrives while mia's worker is still in `processTurn`.
4. `sessionWorker.enqueue` sees `w.inTurn=true` → calls `enqueueSteeringFromMessage(msg)`.
5. `enqueueSteeringFromMessage` re-resolves the route — NOW returns jim (handoff already in effect).
6. The steering message is pushed under JIM's `route.SessionKey`, not mia's.
7. Mia's `processTurn` post-drain checks `pendingSteeringCountForScope(target.SessionKey)` — target is the original message's resolved scope at processTurn start (mia's). Drain finds 0.
8. Mia's turn ends. The message sits in jim's steering queue.
9. If no further input arrives, the message is invisible until the next turn that targets jim — which `runAgentLoop`'s initial steering poll will pick up.

This is not data loss per se (the message will fire on the next jim turn), but the user experience is "I sent a message and the agent said nothing until I sent ANOTHER message." Probability is low (requires a same-session user follow-up DURING a handoff turn, before the handoff turn ends).

**Recommendation:** Track for v0.3 (the Rooms redesign #156 reworks routing/handoff fundamentally — easier to fix there). For v0.1, document the behavior in `session_worker.go`'s `enqueue` comment.

---

### 7. The 60-second worker idle timeout is fixed; not yet load-tested under slow-loris

**Severity:** FUTURE (v0.2 #175)
**Component:** `pkg/agent/session_worker.go:25`

`workerIdleTimeout = 60 * time.Second`. An attacker holding 10k WS sessions and sending one keep-alive message every 50 seconds keeps 10k goroutines alive. Each worker is approximately:
- 1 goroutine: ~8 KB initial stack, can grow.
- 1 buffered channel (cap 8): ~ a few hundred bytes.
- `atomic.Pointer[time.Time]`, atomic.Bool, ctx + cancel: trivial.

Floor: ~10 KB per idle worker. At 10k workers, that's ~100 MB. Well over the CLAUDE.md #3 10 MB security overhead — but #3 is about the SECURITY subsystem (sandbox / RBAC / audit), not the agent runtime, so this isn't a direct violation. Still, an operator running with `sandbox.mode=enforce` and a 1 GB VM cap notices.

The admission controller's soft cap (`NumCPU()*4`) limits *active turns* but not *idle workers*. On a 4-core machine the cap is 16 active turns; 10k idle workers is unaffected.

**Recommendation:** v0.2 #175 should add a max-worker count (separate from active-turn admission) and an LRU eviction policy when capacity is hit. Recommended cap: `min(2048, NumCPU() * 64)` — generous enough that legitimate use never sees eviction, low enough that slow-loris hits the wall.

---

### 8. CLAUDE.md hard-constraint scan

| Constraint | Status | Notes |
|---|---|---|
| #1 Single Go binary | OK | No new external dependencies. |
| #2 Pure Go | OK | `golang.org/x/sys/unix` only. |
| #3 < 10 MB security overhead | OK for v0.1 | Worker overhead is in agent runtime, not security. Finding #7 tracks agent runtime overhead separately. |
| #4 Graceful degradation | OK | Docker autodetect IS the degradation path for unprivileged containers. Finding #3 widens it. |
| #5 Ecosystem compatibility | OK | No conventions broken. |
| #6 Deny-by-default for security | PARTIAL | Auto-downgrade to permissive on Docker detect is a deliberate, scoped exception with audit-only enforcement and a loud nag banner. Documented in the source comment. Aligns with the existing pattern. |
| #7 Release responsibility | OK | All 5 bugs fully fixed; no "deferred" excuses. |
| #8 Contract-first wire formats | PARTIAL — see Finding #1 | The capacity-rejection path reuses an existing wire format (channel text) but ignores a more-appropriate existing one (`SystemOverloadFrame`). Not a constraint violation; an opportunity. |

---

## Future ADR Seeds

These are not blockers; they're follow-ups worth filing as GitHub issues with the proposed ADR/scope.

### Issue: "Container-runtime detection matrix for sandbox auto-downgrade"

**Title:** sandbox: widen docker_autodetect to k8s / podman / containerd
**Labels:** `v0.2`, `area/sandbox`, `area/operations`, related-to #155
**Acceptance:**
- `isRunningInDocker` renamed to `isRunningInContainer` and checks `/.dockerenv`, `OMNIPUS_IN_DOCKER`, and `/proc/1/cgroup` for `containerd|kubepods|docker|podman|crio`.
- Matrix documented in `docs/operations/sandbox-modes.md` (one row per runtime, signal that fires, resulting mode).
- Health endpoint reports the specific signal in `disabled_by` (e.g. `kubernetes_autodetect`, `podman_autodetect`).
- Operators with custom seccomp + caps can still override via `OMNIPUS_SANDBOX_MODE=enforce`.

### Issue: "Admission counter: include subagent spawns or document deferral"

**Title:** admission: subagent spawns are uncounted; clarify scope or fix
**Labels:** `v0.2`, `area/agent`, depends on #175
**Acceptance:**
- Decision: either count subagent turns or explicitly document the counter's scope as "user-initiated dispatcher turns only."
- If counting: wrap `runSubTurn` / `runAgentLoop` with `admission.OnTurnStart` / `OnTurnEnd` (no `ShouldAdmit` gate — once parent admitted, children proceed).
- Test that demonstrates: 1 parent + N children = activeTurns = N+1.

### Issue: "Worker pool: cap idle worker count, evict LRU under pressure"

**Title:** session worker pool: bound goroutine count under slow-loris abuse
**Labels:** `v0.2`, `area/agent`, depends on #175
**Acceptance:**
- New config: `agent.max_idle_workers` (default `min(2048, NumCPU()*64)`).
- When count would exceed cap, evict the LRU worker by `lastActive` (already tracked via atomic.Pointer).
- Evicted worker's pending inbox messages are re-routed via the dispatcher (lose ordering for that scope but preserve delivery).
- Metric: `omnipus_session_worker_count`, `omnipus_session_worker_evicted_total`.

### Issue: "Replay drain: bound max blocking time under sendCh back-pressure"

**Title:** ws: replay drain can block indefinitely when client is slow
**Labels:** `v0.1` (this branch) OR `v0.2`, `area/gateway`
**Acceptance:**
- Per-frame drain send has a deadline (recommend 1s).
- On timeout, log degraded and exit drain (accepting frame loss over connection hang).
- Test: fill sendCh, fill replayDivertCh, call handleAttachSession → asserts return within 30s.

### Issue: "Mid-handoff same-session message routing"

**Title:** agent: mid-handoff steering message goes to new agent's queue, not visible until next turn
**Labels:** `v0.3`, related-to #156 (Rooms redesign)
**Acceptance:** Fold into Rooms redesign — handoff and routing semantics change fundamentally there.

---

## Good Decisions to Highlight

These are architectural moves the team should hold up as well-executed:

1. **Worker delegates rather than re-implements.** `sessionWorker.processTurn` calls `al.processMessage` / `al.Continue` / `al.publishResponseIfNeeded` rather than duplicating that logic. The hookManager, eventBus, channelManager, browserMgr, and DevServerRegistry remain singletons on `AgentLoop`; workers carry no per-worker copies. This is the right structural decision — it means future changes to turn execution apply to all workers automatically, and the existing tool-call wiring (which assumes shared singletons) keeps working without touching tool code.

2. **Two distinct scope keys, both intentional.** The worker map key (`agent:<id>:session:<sid>:<sid>` — note double-SID) and the steering queue key (`agent:<id>:session:<sid>` — single-SID from `route.SessionKey`) are deliberately different. The worker map key disambiguates per-session workers for the same agent; the steering key matches what `runAgentLoop`'s active-turn registration uses (`opts.SessionKey == route.SessionKey`). The inline comment at `loop.go:3019-3024` calls out the necessity. Two scope keys is more nuanced than one, but the right call given the existing turn-state registration shape.

3. **Worker idle-timeout is a property of the worker, not a centralized janitor.** The 60s idle timer lives inside `runLoop` and the worker self-deletes from `sessionWorkers` on exit. No central reaper goroutine, no contention. This is the correct pattern for long-tail-of-mostly-idle workers.

4. **Steering enqueue has a fallback to inbox enqueue.** `sessionWorker.enqueue` tries `enqueueSteeringFromMessage` first when `inTurn=true`, but falls back to the inbox if the steering enqueue fails. The message is never silently lost — the worst case is "user message starts a new turn instead of mid-turn continuation." This is the right failure mode for a non-trivial concurrency invariant: degrade UX, never lose data.

5. **Replay-drain ordering fix is grounded in a load-bearing invariant** ("flag must still be set during the drain so concurrent writers continue to divert"). The `bug-5-replay-order.md` investigation correctly identifies why drain-then-disarm is safe and drain-after-disarm is not. The regression test in `websocket_replay_order_test.go` is FIFO-ordering specific, not a flake-prone timing test. The fix is mechanically obvious once the invariant is stated; the investigation document is the load-bearing artifact.

6. **Docker autodetect is conservative and reversible.** The Dockerfile bakes `OMNIPUS_SANDBOX_MODE=permissive` so the official image is deterministic; the runtime auto-detect only fires for ad-hoc `docker run` of non-official images on a fresh-install config. Operators with hardened Docker (caps + custom seccomp) can override with `OMNIPUS_SANDBOX_MODE=enforce`. This three-layer composition (image default + runtime fallback + env override) is the right shape for "secure by default but escapable when needed."

7. **Soft cap deferral to v0.2 is honest.** The `admission.go` comment explicitly names what Phase 1 doesn't do: "Phase 1 only — not resource-aware. Resource-aware admission (CPU load, RSS vs cgroup mem, goroutine count) is filed as a follow-up for v0.2 (#175)." This is the correct pattern — ship the easy mechanism now, defer the hard policy decisions with an issue link. The alternative ("don't ship admission at all until we have the right policy") would have left Bug 3's per-session worker pattern unbounded.

---

## Verdict

**APPROVE** for merge after Findings #1, #4, and #5 are addressed (the SHOULD-FIX items are small, scoped, and don't change architecture). Finding #2 (subagent admission accounting) is borderline — at minimum, update the `admission.go` comment to scope the counter explicitly to "dispatcher-level user turns" so future readers don't expect different semantics. All FUTURE findings are appropriately deferred; the issue seeds above give the next sprint a concrete picking order.

The branch is a net structural improvement to the agent runtime. The single-threaded `Run()` bottleneck was a real correctness bug, and the per-session-worker model resolves it without inventing new abstractions for the rest of the codebase to learn. Ship it.
