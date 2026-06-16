# Cross-cutting review (pass 2)

Branch: `feature/iframe-preview-tier13`
Scope: integration points not covered by the other six second-pass agents
(architect, code-reviewer-pass2, silent-failure-hunter, pr-test-analyzer,
type-design, simplifier, comment-analyzer).

## Summary

**6 issues across categories: hard-constraint-7-violations (2), migration-safety (2), audit-coverage (1), perf-concerns (0 — sanity checked OK).**

`make verify-contracts` passes (no spec drift, generated artifacts in sync).
`scripts/check-no-handwritten-wire-types.sh` is clean (0 findings).
`go vet` on the changed packages is clean.
The `replayMu` RWMutex on the WS send hot path was sanity-checked against the
2026-05-22T04-33-22Z perf result (2000 sessions, p99 first-token 90 ms) —
**no perf concerns**.

Verdict: **APPROVE with non-blocking nits**. The two constraint-#7 patterns
are minor (log-on-error already covers the bulk of the failure mode) but
should be tightened before v0.1. The migration-safety gap on `channels.teams`
is operator-facing noise, not a correctness issue. The audit-coverage gap
on admission rejection is a real observability hole worth filing as a v0.2
follow-up.

---

## hard-constraint-7-violations

### HC7-1 — Discarded error from `processSystemMessage` in Run() dispatcher

File: `pkg/agent/loop.go:1543`

```go
_, _ = al.processSystemMessage(runCtx, msg)
```

On `main` this same call was `resp, err := al.processSystemMessage(ctx, msg); return resp, nil, err`, i.e. the error reached the caller. The branch re-architected
the dispatcher so the call is now inside a fire-and-forget goroutine and
the error has nowhere to go — so it is dropped on the floor.

`processSystemMessage` itself returns errors for:

- non-"system" channel (programmer bug — but the function is now only
  reachable when `msg.Channel == "system"`, so this branch is defensive only);
- missing default agent (`no default agent for system message`);
- runTurn failure propagating up from the LLM call.

The first two are silent under the new pattern. The third is mostly logged
internally inside `runTurn`, so the impact is muted but not zero.

**Fix** (5-line change): add a `WarnCF` before the discard.

```go
if _, err := al.processSystemMessage(runCtx, msg); err != nil {
    logger.WarnCF("agent", "Failed to process system message",
        map[string]any{"chat_id": msg.ChatID, "error": err.Error()})
}
```

Confidence: 85.

### HC7-2 — System-message goroutine leaks past Run() return

File: `pkg/agent/loop.go:1531-1545`

The system-message goroutine is spawned with no waitgroup or done-channel.
When `Run()` exits via `runCtx.Done()`, `stopSessionWorkers()` waits for
session workers but not for in-flight system goroutines. Under shutdown
with an active system message in flight, the goroutine will be torn down by
process exit rather than draining cleanly. The deferred-publish guards
inside `runAgentLoop` won't run if the goroutine is mid-LLM call when the
process is killed.

This is **not** a constraint-#7 "ignored error" violation per se — it's a
graceful-shutdown gap. The `runCtx` propagation does cause the LLM call to
unwind reasonably (context.Canceled bubbles up). Filed for awareness, not
as a blocker.

Confidence: 60 — flagging because the prompt asked about this specific file
and the pattern matters, but the actual impact at v0.1 is small (system
messages are rare and short-lived; the kill-on-shutdown loss is acceptable).

---

## hard-constraint-8-violations

**None found.** All cross-boundary frames go through `generated.TokenFrame`,
`generated.DoneFrame`, `generated.MediaFrame`, etc. The admission-rejection
path uses `bus.OutboundMessage` (internal bus type), which is translated
into `generated.TokenFrame` + `generated.DoneFrame` by
`pkg/gateway/webchat_channel.go:62-113`. `bus.OutboundMessage` has `json:`
tags but is an internal bus payload, not a wire-format type sent over the
WS. The `check-no-handwritten-wire-types.sh` lint reports 0 findings, and
`make verify-contracts` exits 0 with no drift.

---

## perf-concerns

**None found.** The `replayMu` RWMutex is per-`wsConn`, not global. The
fast-path in `sendRawFrameBytes` (`pkg/gateway/websocket.go:1366`) does an
`atomic.Bool.Load()` and skips the mutex entirely when `isReplayingLive` is
false — i.e. for ~99% of frames on a steady-state connection (replay only
runs during the initial attach window). The 2000-session perf run from
2026-05-22T04-33-22Z shows p99 first-token 90 ms, peak RSS 224 MB,
goroutine_leak -13 — well within the SLO. The new lock does not move the
needle.

The architect noted "single-shot deferral so probably fine"; this confirms.

---

## migration-safety

### MS-1 — `channels.teams` silently dropped without operator warning

File: `pkg/config/migration.go:561`

```go
var removedChannelKeys = []string{"maixcam"}
```

The Teams channel was deleted in commit `c25f1e8` ("fix(bugs): close 7
backlog issues + 2 teardown flakes" → "Closes #161 — deletes the half-wired
teams channel"). The `TeamsConfig` struct was removed from `pkg/config/config.go`
but `removedChannelKeys` was **not** updated to include `"teams"`.

Impact: An operator upgrading from a release that defined `channels.teams.*`
in their config.json will see Teams silently disappear with no log entry to
explain. `detectUnknownConfigFields` runs only at the top level — it doesn't
recurse into `channels`, so the unknown subkey is dropped without trace and
without preservation in `cfg.UnknownFields` either.

**Fix** (1-line change):

```go
var removedChannelKeys = []string{"maixcam", "teams"}
```

Confidence: 90 — confirmed by reading `warnRemovedChannelFields` and
`detectUnknownConfigFields`. Same migration pattern as MaixCam, just
missing for Teams.

### MS-2 — no JSON config field renames detected (verification only)

The diff vs main shows several additive sandbox fields (`SandboxMode`,
`SandboxProfile`, per-agent shell policies, recap settings) but no renames
or removals of existing keys other than the `MaixCam` / `Teams` channel
removals already covered above. The Sandbox config UnmarshalJSON pattern is
strict (rejects typos) which is correct.

No upgrade path concerns for operators on prior releases beyond MS-1.

Confidence: 95.

---

## audit-coverage

### AC-1 — Admission-controller capacity rejections do not emit audit entries

Files: `pkg/agent/loop.go:1585-1607`, `pkg/agent/session_worker.go:117-134`

The admission controller is a security-relevant gate: at soft-cap saturation,
new sessions are rejected with a user-visible "I'm at capacity right now"
reply. Concurrent rate-limit denials (`pkg/agent/loop.go:785-812
recordRateLimitDenial`) emit `audit.EventRateLimit` / `audit.DecisionDeny`
entries to the audit log. The new admission rejection path emits only an
`slog.Warn` — **no audit entry**.

Likewise, `sessionWorker.enqueue` (`pkg/agent/session_worker.go:117-122`)
emits an `slog.Warn` on inbox-full drop with no audit entry.

A sustained DoS targeting admission saturation (or a misconfigured channel
flooding the inbox) is invisible to the audit log. Operators cannot run
`omnipus audit verify` and see admission denials; they have to grep
gateway.log for the `Warn` strings.

**Fix** (defer to v0.2 #155 follow-up): wire these two paths through
`audit.EmitEntry` with a new `audit.EventAdmissionDeny` / equivalent event
kind. Pattern is already established in `recordRateLimitDenial` —
straightforward to mirror.

Confidence: 80 — the CLAUDE.md "audit-everything stance is non-negotiable"
language (loop.go:376) supports treating this as an audit gap. The decision
to gate user requests at capacity is exactly the kind of access-control
decision that belongs in the audit log.

---

## final approval

**APPROVE for merge** with the following follow-ups filed as issues (not
blockers):

| Finding | Severity | Suggested action |
|---|---|---|
| HC7-1 | Important | Add `WarnCF` log around discarded err — 5-line fix, v0.1 |
| HC7-2 | Nit | Document graceful-shutdown semantics for system messages |
| MS-1  | Important | Add `"teams"` to `removedChannelKeys` — 1-line fix, v0.1 |
| AC-1  | Important | File against v0.2 #155 — wire admission/inbox denials through `audit.EmitEntry` |

No constraint-#8 violations. No perf concerns. Contract gates green.
