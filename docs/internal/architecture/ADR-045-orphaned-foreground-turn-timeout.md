# ADR-045: Orphaned Foreground Turn Timeout

**Status:** Accepted
**Date:** 2026-07-16
**Deciders:** architect (+ backend-lead, qa-lead for implementation/review)

**Implementation note (2026-07-16):** Initially implemented as a bespoke
turn-scoped PHASE A/B/C escalation in commit
`03f4558f0d6d35a12e13ea57607451a354a7cff0` (`feat(agent,gateway): orphaned-foreground-turn
watchdog (ADR-045) to bound leaked turns`). A 7-reviewer gate on that
implementation found 8 real bugs, including three sev-9 correctness/safety
issues (MA-1, elaborated below; MA-3 = a mid-flight escalation that could not
be disarmed on reconnect, and MA-6 = an unsynchronised `ow.timer` write race —
both now covered by regression tests in `pkg/agent/orphan_watch_test.go`) — the
root cause of most was that the
bespoke design REIMPLEMENTED cancellation instead of reusing the existing,
battle-tested `RequestCancel` state machine (`pkg/agent/cancel.go`), silently
dropping side effects (approval auto-deny, background-session kill,
session-status-interrupted) that every other cancel surface gets.

**Redesign (2026-07-16, same day, before merge):** commit
`cba4c562` (`fix(agent,gateway): redesign
orphan watchdog to reap via RequestCancel (gate round-1)`) replaces the bespoke escalation
entirely with a single grace timer that hands the abandoned session to
`AgentLoop.RequestCancel` — the SAME cancellation path every other cancel
surface (web SPA Stop button, Tier A `/cancel`, Tier B text-parsing
channels, CLI) already uses — gated by three conditions checked at fire time
(a genuine live root turn, no surviving Critical/background delegate, no
reconnect). This is strictly safer than the original turn-scoped-hard-abort
approach: rather than inventing a parallel, turn-scoped-only hard-abort
primitive to avoid touching a live delegate, the redesign defers reaping
entirely for good whenever a Critical delegate survives, and otherwise
reuses RequestCancel's full, audited, side-effect-complete escalation
unmodified. See "Mechanism" below for the current design; the bespoke
PHASE A/B/C escalation, `TurnCancelHook.RequestHardAbort`, and the
`turn.orphan_hard_aborted` audit event it introduced are all retired — see
"Alternatives Considered" for why the original rejection of reusing
`RequestCancel` no longer applies. Pending human merge-gate approval per
CLAUDE.md.

## Context

The full e2e suite (single worker, serial, one gateway, one shared rate-limited
OpenRouter key) degrades late in the run: ~20 streaming/delegation tests time
out under suite load even though they pass standalone (documented as "Group-A
variance" in `playwright.config.ts`).

Code-confirmed root cause, three contributing facts:

1. **The gateway deliberately keeps a turn's agent loop running after its
   originating WebSocket closes.** `tests/e2e/fixtures/console-errors.ts:22-36`
   documents this as intentional: headless channels (Slack/Telegram/…) have no
   WS at all, and the since-cursor replay path expects a turn to complete
   regardless of who is watching. "Cancel only fires on explicit user action"
   (`pkg/gateway/websocket.go:1388-1451`, `handleCancel` → `agent.RequestCancel`,
   `pkg/agent/cancel.go:108`). A Playwright test that navigates away mid-turn
   without clicking Stop leaves a real agent loop running, which queues
   against the shared OpenRouter rate-limit window and starves later tests'
   tool calls.
2. **There is no server-side per-turn LLM-call timeout.**
   `pkg/config/defaults.go:45` sets `TimeoutSeconds: 0` with the comment
   "disabled; OpenRouter queue delays make fixed timeouts unreliable";
   `pkg/agent/loop.go:5556-5561` only applies `context.WithTimeout` when
   `TimeoutSeconds > 0`, otherwise `context.WithCancel` (unbounded).
3. **`cancelOnTeardown` is best-effort** (a 2s Stop-button click at Playwright
   teardown) — a gap, not a guarantee (page already navigating/closing,
   store not initialized, etc.).

This is not purely a test-infra problem: an orphaned FOREGROUND (root, web
chat) turn that nobody is watching and that never reconnects is a real
production waste-of-provider-budget risk, distinct from the DELIBERATE design
where a background/Critical delegate (`delegate async=true`,
`SubTurnConfig.Critical`, `pkg/agent/subturn.go:149-158`) is meant to outlive
its triggering WebSocket and complete unattended.

Investigation of the existing cancel state machine
(`pkg/agent/cancel.go:85-363`) establishes the constraint that shapes this
decision: `RequestCancel`'s PHASE B/C escalation (`InterruptSessionHard` /
`sessionTurnsStillAlive`, `pkg/agent/steering.go:493-599`) is **session-wide** —
it hard-aborts every `turnState` sharing a `transcriptSessionID`, Critical
descendants included, once they're still alive past the 3s mark. This is
correct for an explicit user Stop-click (a strong "stop everything on this
session" signal) but would be wrong to reuse verbatim for an automatic,
WS-close-triggered timeout, which must NOT touch a legitimate Critical
background delegate no matter how long the user stays away.

Separately, `InterruptSession`'s PHASE-A graceful cascade
(`pkg/agent/steering.go:436-491`) is ALSO session-wide but is safe to reuse
as-is: it only fires `providerCancel()` (aborts the current in-flight LLM
HTTP call) and sets the `gracefulInterrupt` flag on every matching turn. A
Critical descendant that ignores this nudge is explicitly proven safe by
`TestRequestCancel_OrphanedBackgroundDelegate_HardAbortedAfterParentGracefullyFinishes`
(`pkg/agent/cancel_orphan_delegate_test.go:127-208`) — the graceful nudge
alone never terminates it; only the session-wide *hard*-abort stage would.
Async/Critical delegates are additionally rooted on `context.Background()`
(`pkg/agent/loop.go:4142`, "deliberately independent so a Critical sub-turn
survives its parent's graceful finish"), so a hard-abort scoped to only the
ROOT `turnState`'s own context/`providerCancel` cannot reach them at all —
they don't share a context tree.

BRD/constraint grounding: CLAUDE.md's Release Strategy routes "structural"
security/reliability fixes to v0.3 and quick fixes to v0.2, but turn-lifecycle
correctness (bounding an unattended foreground turn) is closer to a
correctness/security-adjacent fix than a feature; this ADR treats it as a
cross-cutting reliability fix, not gated to a specific release phase, and
notes the routing question explicitly for the user (see Consequences/Neutral).

## Decision

Add a narrowly-scoped **orphaned foreground turn watchdog** in `pkg/agent`,
wired from the gateway's WebSocket layer, that bounds only the ROOT turn of
a webchat session when its last watching connection disappears — WITHOUT
ever touching Critical/background descendant sub-turns.

This is option (a) from the brief (orphaned-turn timeout), and **not**
option (b) (a bounded server-side LLM-call timeout) because
`TimeoutSeconds`'s own doc comment establishes that fixed per-call timeouts
are unreliable under exactly the queued/high-load conditions e2e itself
creates. Option (c) (harness-only `cancelOnTeardown` hardening) is adopted
as a secondary, low-risk defense-in-depth measure but not the primary fix,
because it does nothing for the real production risk of an abandoned tab.

**Redesign (same day, before merge):** the mechanism REUSES
`RequestCancel` directly — a deliberate reversal of the original decision to
avoid it (see "Alternatives Considered" for why the original rejection no
longer applies). The watchdog's own job shrinks to: decide, once, whether
reaping via `RequestCancel` is safe; if so, call it; if not, do nothing and
give up for this arm (no bespoke escalation timers of its own).

### Mechanism

1. **Arm**: `pkg/gateway/websocket.go`'s `ServeHTTP` teardown defer
   (`websocket.go:605-650`) — when the closing connection's `chatID` was the
   **last** connection watching its `sessionID` (checked via `h.sessionIDs`
   before/after the existing map deletes), and only when the session is a
   foreground **CHAT** session (`session.SessionTypeChat`; Task/Scheduled/
   Channel/Heartbeat sessions are never watched) — calls
   `AgentLoop.ArmOrphanForegroundTurnWatch(sessionID, graceSeconds, reap, stillOrphaned)`.
   `reap` and `stillOrphaned` are gateway-supplied closures (see step 3).
   Arm starts a single `time.AfterFunc` grace timer keyed by `sessionID` in
   the `orphanWatches sync.Map` field on `AgentLoop`
   (`pkg/agent/orphan_watch.go`).
2. **Disarm**: `handleAttachSession` (`websocket.go`) and the
   session-continuation path of `handleChatMessage` (`websocket.go`) call
   `AgentLoop.DisarmOrphanForegroundTurnWatch(sessionID)` as soon as a live
   connection is confirmed on that session — covering the common
   browser-refresh/reconnect case with zero user-visible effect. Disarming
   AFTER the grace timer has already fired-and-reaped is a harmless no-op —
   the watch's map entry is removed the instant it fires — and a reconnect
   landing during `RequestCancel`'s OWN subsequent escalation window behaves
   exactly like a user clicking Stop and then reconnecting (there is no
   "abort an in-flight cancel" mechanism for that case, for ANY cancel
   surface, today; the orphan-reap path does not invent one either).
3. **Fire** (grace period elapsed, no reattachment): reap the session's
   current root turn — by calling the gateway's `reap` closure, which itself
   calls `al.RequestCancel(ctx, agent.CancelScope{SessionID: sessionID}, canceller, hooks)`
   with the SAME `agent.CancelHooks` a web-SPA Stop-click gets
   (`buildCancelHooks`, `pkg/gateway/websocket.go`, shared between
   `handleCancel` and the orphan path) and a dedicated
   `agent.CancelCanceller{UserID: "system", Channel: "orphan-watchdog"}` —
   **ONLY IF ALL THREE** conditions hold, checked by
   `AgentLoop.fireOrphanForegroundTurnWatch` (`pkg/agent/orphan_watch.go`):
   1. A genuine **LIVE ROOT** turn exists for the session — resolved via a
      dedicated root-ONLY resolver (`getActiveRootTurnStateForSession`,
      `pkg/agent/turn.go`), never `GetActiveTurnHookForSession`'s non-root
      `anyMatch` fallback (that fallback existing at all is exactly what let
      the pre-redesign implementation's mismatch check silently resolve a
      live delegate instead of "no root" — see Bug MA-1 below).
   2. **No surviving Critical/background/async delegate** is alive on the
      session (`hasLiveCriticalDelegate`, `pkg/agent/steering.go`) — checked
      even when the root IS still alive, covering the case where the root is
      still running concurrently with an async delegate it just spawned.
      `delegate async=true` unconditionally sets `Critical:true`
      (`pkg/tools/delegate.go`'s `executeAsync`), so this single check covers
      every name ADR-045 uses for the same `turnState.critical` flag.
   3. Nobody has reconnected since arming — the gateway's `stillOrphaned`
      closure re-checks its live `chatID -> sessionID` mappings at fire time,
      closing the race between the timer firing and a reattach that landed
      just before `Disarm` could run.

   If ALL three hold: emit the `turn.orphan_timeout` audit event (attributed
   to `system:orphan-watchdog`, distinguishing this from a real user cancel)
   and call `reap("orphan_timeout")` exactly once. `RequestCancel` then
   performs its full, uniform state machine — abuse-detection record,
   `ClaimCancel` first-cancel-wins, `turn_cancel_attempt`/`turn_cancelled`
   audit, transcript `MarkLastEntryTruncated` + `turn_canceled` entry,
   approval auto-deny, background bash/exec session kill, session-status-
   interrupted, and the 3s-graceful/5s-hard/detached escalation — identically
   to every other cancel surface. Because condition 2 already guarantees no
   live Critical delegate remains, `RequestCancel`'s session-wide escalation
   cannot cascade into anything this mechanism needs to protect.

   If ANY condition fails: no-op. There is no retry/reschedule — the watch
   was single-shot and its map entry is already gone; only a fresh WS
   teardown re-arms it.

### Configuration

New `GatewayConfig` field `OrphanedTurnGraceSeconds *int`
(`json:"orphaned_turn_grace_seconds,omitempty"
env:"OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS"`), resolved via a new
`ResolveInt(v *int, def int) int` helper (`pkg/config/resolve.go`, mirroring
`ResolveBool`). Semantic default **300 seconds (5 minutes)** when unset —
`[INFERRED]`, no BRD-specified number exists; the user/operator should
confirm this is an acceptable production default. `0` or negative disables
the watchdog entirely (matches the `TimeoutSeconds: 0`-disabled convention).
Read live (not restart-gated, matching `GatewayPreviewEnabled`'s precedent,
`pkg/config/keys.go:36-38`) since each WS teardown reads current config fresh.

The e2e harness sets `OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS=20`
(`.github/workflows/pr.yml`, `deploy/ci-worker/runci.sh`) as a single env var
on the gateway process the suite drives — achieving the harness-tuning goal
of option (b) without inheriting its documented unreliability, because this
timer only fires on genuine abandonment (no reconnect), not on every LLM
call regardless of provider queuing.

## Consequences

### Positive
- Bounds real production waste: an abandoned web-chat tab no longer burns
  the shared provider budget indefinitely.
- e2e suite gets a fast, deterministic cleanup (configurable to seconds)
  without weakening the production default or touching the documented
  "cancel only fires on explicit user action" contract for channels that
  have no WS at all.
- Critical/background delegate turns are provably unaffected — the watchdog
  DEFERS reaping entirely (never calls `RequestCancel` at all) whenever one
  is found alive, rather than relying on a bespoke escalation stage being
  correctly scoped.
- There is now only ONE cancellation state machine in the codebase, not two
  similar-but-subtly-different ones — every cancel surface (web SPA, Tier A
  `/cancel`, Tier B channels, CLI, and now the orphan watchdog) gets the
  exact same audit trail, transcript writes, approval auto-deny, and
  background-session-kill side effects for free, by construction.

### Negative
- The 300s default is a judgment call, not derived from a requirement —
  needs operator sign-off.
- Multi-tab-on-one-session bookkeeping (checking "is any other connection
  still watching this session" before arming) adds a small amount of new
  state-scanning logic to the WS teardown path.
- If a Critical/background delegate is still alive at fire time, the
  watchdog gives up on that specific arm permanently (no retry/reschedule) —
  a genuinely abandoned session with a long-running delegate keeps its root
  turn running unwatched until the delegate finishes AND a fresh WS teardown
  re-arms the watch. This trade (never risk a live delegate) is accepted as
  strictly preferable to any design that could touch one.

### Neutral
- This is being proposed independent of the v0.1/v0.2/v0.3 release-phase
  routing in CLAUDE.md; the user should confirm which phase it targets
  (arguably v0.2 "security/reliability hardening" given the production
  resource-exhaustion angle, but it is not on the v0.2 issue #155 list
  today).

## Alternatives Considered

### (b) Bounded server-side LLM-call timeout via `TimeoutSeconds`
- Pros: minimal code (config value already threaded through
  `pkg/agent/loop.go:5556-5561`); e2e sets it low via existing agent config.
- Cons: `pkg/config/defaults.go:45`'s own doc comment says fixed timeouts are
  unreliable under OpenRouter queuing delays — exactly the condition present
  under e2e suite load. Would risk killing legitimate, merely-queued turns
  (false positives) in the very environment it's meant to fix, and does
  nothing for the production abandoned-tab risk since it fires regardless of
  whether anyone is watching.
- Why rejected: wrong axis (bounds call latency, not attention/abandonment).

### (c) Harness-only: reliable `cancelOnTeardown` via a test-only cancel-by-session endpoint
- Pros: zero production code change; fully addresses e2e determinism.
- Cons: does nothing for the real production risk (an abandoned tab still
  runs forever); the task brief explicitly asks for a fix "correct for real
  production ... AND makes the suite reliable" — this only satisfies the
  second half.
- Why rejected as primary: kept as a secondary, complementary hardening
  (see Risk Analysis / Regression Tests below) since it further tightens
  e2e determinism independent of the new grace-period's exact value.

### Reuse `RequestCancel`/`InterruptSessionHard` wholesale for the WS-close case — ADOPTED (redesign), in a GUARDED form
- Pros: no new escalation-timer code at all; every other cancel surface's
  audit/transcript/approval/background-session side effects apply uniformly
  and automatically; there is only one cancellation state machine in the
  codebase to reason about, test, and review, ever.
- Cons (the ORIGINAL objection, at the ORIGINAL decision time): calling
  `RequestCancel` **unconditionally** on a WS-close timeout would be a
  correctness violation — `InterruptSessionHard`/`sessionTurnsStillAlive`
  are session-wide by design (`pkg/agent/steering.go`) and would hard-abort
  a live Critical/background delegate on the same session once the
  escalation fires, directly violating the requirement that background/
  Critical turns survive their originating WS's closure indefinitely.
- Why the original rejection no longer applies: the redesign does not call
  `RequestCancel` unconditionally. `fireOrphanForegroundTurnWatch` checks,
  BEFORE ever calling `reap`, that no live Critical/background delegate
  exists on the session (`hasLiveCriticalDelegate`) — if one does, `RequestCancel`
  is never invoked at all for that fire. The **guard** moved from "escalate,
  but scope the escalation to just this turn" (the original, bug-prone
  approach — a bespoke, turn-scoped hard-abort primitive that a 7-reviewer
  gate found three sev-9 bugs in) to "check first, then either reuse the
  existing session-wide primitive freely, or don't call it at all". This is
  strictly safer: the invariant ("never touch a live Critical delegate") is
  enforced by never reaching the session-wide escalation in the first place,
  rather than by a second, parallel, turn-scoped-only escalation family that
  has to be independently proven equivalent-but-narrower every time either
  one changes.
- Why adopted now: a 7-reviewer gate on the original bespoke implementation
  found 8 real bugs, including three sev-9 correctness/safety issues, whose
  common root cause was reimplementing cancellation instead of reusing
  `RequestCancel`. Reusing it directly — with the pre-check above — dissolves
  that entire class of bug by construction.

## Affected Components

- Backend: `pkg/agent/loop.go` (`orphanWatches` field on `AgentLoop`),
  `pkg/agent/turn.go` (`getActiveRootTurnStateForSession`, a root-ONLY
  resolver — NOT `TurnCancelHook.RequestHardAbort`, which was added by the
  original implementation and removed again by the redesign: nothing outside
  `pkg/agent` needs to hard-abort a single turn anymore, since reaping now
  goes through the ordinary `RequestCancel` entry point), `pkg/agent/steering.go`
  (`hasLiveCriticalDelegate`), `pkg/agent/orphan_watch.go` (Arm/Disarm/fire —
  a single grace timer plus the three-condition fire gate; no escalation
  timers of its own), `pkg/gateway/websocket.go` (`ServeHTTP` teardown,
  `handleAttachSession`, `handleChatMessage` wiring, `buildCancelHooks`
  shared with `handleCancel`, `reapOrphanForegroundTurn`,
  `sessionStillOrphaned`), `pkg/config/config.go` (`GatewayConfig` field),
  `pkg/config/resolve.go` (`ResolveInt`), `pkg/config/keys.go` (`ConfigKey`),
  `pkg/audit/events.go` + `pkg/audit/audit.go` (ONE event kind,
  `turn.orphan_timeout` — the original implementation's second event,
  `turn.orphan_hard_aborted`, is retired: the escalation itself is now
  `RequestCancel`'s own `turn_cancelled` event, cancel_method `"hard"`, same
  as every other cancel surface). No `pkg/session` changes — the redesign
  writes no transcript entry of its own; `RequestCancel`'s existing
  `onCancelFinish` callback already writes the `turn_canceled` transcript
  entry for every cancel it performs, orphan-reaps included.
- Frontend: none required (no new wire frame types needed — the existing
  `cancel_stage`-style frame can be reused for observability if desired, but
  is optional since the tab that abandoned the turn is, by definition, gone).
- Variants: Open Source / Desktop / SaaS all share this code path — no
  variant-specific behavior. Non-Go bridge/sidecar channels are unaffected
  (mechanism triggers only from the webchat WS `ServeHTTP` path).

## Integration Contract

No new frontend-facing wire contract is required. No new persisted-shape
addition either (post-redesign): the reaped turn's transcript entry is the
SAME `EntryTypeTurnCancelled` entry `RequestCancel`'s `onCancelFinish`
callback already writes for every cancel, with `CancelledByUser: "system"`
and `CancelledByChannel: "orphan-watchdog"` (the `CancelCanceller` the
gateway's `reapOrphanForegroundTurn` passes to `RequestCancel`) —
distinguishing an orphan-reap from a real user cancel needs no new field,
just the existing `CancelledByUser`/`CancelledByChannel` values a reader
already knows how to interpret.
