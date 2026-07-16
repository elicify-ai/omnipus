# ADR-045: Orphaned Foreground Turn Timeout

**Status:** Accepted
**Date:** 2026-07-16
**Deciders:** architect (+ backend-lead, qa-lead for implementation/review)

**Implementation note (2026-07-16):** Implemented as designed in commit
`4677b6f75b14731d645ef1498759425cca8b6a4b` (`feat(agent,gateway): orphaned-foreground-turn
watchdog (ADR-045) to bound leaked turns`) — `pkg/agent/orphan_watch.go` (Arm/Disarm/fire),
`TurnCancelHook.RequestHardAbort` (`pkg/agent/turn.go`), gateway wiring in
`pkg/gateway/websocket.go`, `GatewayConfig.OrphanedTurnGraceSeconds` (`pkg/config`), two new
audit events, and the e2e harness `OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS=20` override
(`.github/workflows/pr.yml`, `deploy/ci-worker/runci.sh`). Pending human merge-gate approval
per CLAUDE.md.

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

Add a new, narrowly-scoped **orphaned foreground turn watchdog** in
`pkg/agent`, wired from the gateway's WebSocket layer, that bounds only the
ROOT turn of a webchat session when its last watching connection disappears
— WITHOUT ever touching Critical/background descendant sub-turns.

This is option (a) from the brief (orphaned-turn timeout), deliberately
**not** implemented by reusing `RequestCancel`/`InterruptSessionHard`
wholesale (which would cascade to Critical children), and **not** option (b)
(a bounded server-side LLM-call timeout) because `TimeoutSeconds`'s own
doc comment establishes that fixed per-call timeouts are unreliable under
exactly the queued/high-load conditions e2e itself creates. Option (c)
(harness-only `cancelOnTeardown` hardening) is adopted as a secondary,
low-risk defense-in-depth measure but not the primary fix, because it does
nothing for the real production risk of an abandoned tab.

### Mechanism

1. **Arm**: `pkg/gateway/websocket.go`'s `ServeHTTP` teardown defer
   (`websocket.go:605-633`) — when the closing connection's `chatID` was the
   **last** connection watching its `sessionID` (checked via `h.sessionIDs`
   before/after the existing map deletes) — calls a new
   `AgentLoop.ArmOrphanForegroundTurnWatch(sessionID, graceSeconds, hooks)`.
   This resolves the session's current root turn via the existing
   `GetActiveTurnHookForSession` (`pkg/agent/turn.go:415-441`, already
   root-preferring), captures its `TurnID()`, and starts a `time.AfterFunc`
   grace timer keyed by `sessionID` in a new `orphanWatches sync.Map` field
   on `AgentLoop` (alongside `activeTurnStates`, `pkg/agent/loop.go:100`).
2. **Disarm**: `handleAttachSession` (`websocket.go:1548`) and the
   session-continuation path of `handleChatMessage` (`websocket.go:1053`)
   call `AgentLoop.DisarmOrphanForegroundTurnWatch(sessionID)` as soon as a
   live connection is confirmed on that session — covering the common
   browser-refresh/reconnect case with zero user-visible effect.
3. **Fire** (grace period elapsed, no reattachment): re-resolve the root
   turn hook for `sessionID`; if it's gone or its `TurnID()` no longer
   matches the armed one, no-op (already finished or replaced). Otherwise:
   - PHASE A (graceful, session-wide, reused as-is):
     `al.InterruptSession(sessionID, hint)` — safe for Critical descendants
     per the existing regression test cited above.
   - PHASE B (hard, **turn-scoped only**, new): after a short escalation
     window (3s, mirroring `RequestCancel`'s own timing), if that SAME
     `turnID` is still the resolved root and still alive, call a new
     `RequestHardAbort() bool` method added to the `TurnCancelHook`
     interface (`pkg/agent/turn.go:377-392`) — a thin exported wrapper
     around the existing unexported `ts.requestHardAbort()`
     (`pkg/agent/turn.go:771-789`). This hard-aborts **only** that one
     `turnState`'s own `providerCancel`/`turnCancel`; it never walks
     `transcriptSessionID` matches, so a Critical/background descendant
     (independent context tree) is structurally unreachable by it.
   - PHASE C (detached, turn-scoped, new, defensive symmetry with
     `RequestCancel`'s own PHASE C): +5s more, if still alive, call the
     existing `MarkAbandoned()` on that turn only.
4. **Audit + transcript**: emit new audit events (`turn.orphan_timeout`,
   `turn.orphan_hard_aborted`) distinguishing this from a real user cancel
   (`canceller_channel: "system:orphan-watchdog"` equivalent attribution),
   and append a transcript entry so a user who does return later sees why
   the turn stopped (mirrors `RequestCancel`'s `onCancelFinish` transcript
   annotation, since this path does not go through `ClaimCancel`/
   `onCancelFinish` at all and would otherwise leave the transcript silent).

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

The e2e harness sets `OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS=5` (or
similar) as a single env var on the gateway process the suite drives —
achieving the harness-tuning goal of option (b) without inheriting its
documented unreliability, because this timer only fires on genuine
abandonment (no reconnect), not on every LLM call regardless of provider
queuing.

## Consequences

### Positive
- Bounds real production waste: an abandoned web-chat tab no longer burns
  the shared provider budget indefinitely.
- e2e suite gets a fast, deterministic cleanup (configurable to seconds)
  without weakening the production default or touching the documented
  "cancel only fires on explicit user action" contract for channels that
  have no WS at all.
- Critical/background delegate turns are provably unaffected — the
  hard-abort stage is turn-scoped, and PHASE A's graceful nudge alone
  cannot terminate them (existing regression test already proves this).
- Reuses proven primitives (`InterruptSession`, `GetActiveTurnHookForSession`,
  the graceful→hard→detached staging shape) rather than inventing a new
  cancellation state machine from scratch.

### Negative
- Adds a second, parallel escalation timer family (turn-scoped) alongside
  `RequestCancel`'s existing session-scoped one — two similar-but-distinct
  "3s then hard" patterns to reason about. Mitigated by keeping the new one
  a thin, well-documented wrapper and cross-referencing both in code
  comments.
- The 300s default is a judgment call, not derived from a requirement —
  needs operator sign-off.
- Multi-tab-on-one-session bookkeeping (checking "is any other connection
  still watching this session" before arming) adds a small amount of new
  state-scanning logic to the WS teardown path.

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

### Reuse `RequestCancel`/`InterruptSessionHard` wholesale for the WS-close case
- Pros: no new escalation-timer code at all.
- Cons: `InterruptSessionHard`/`sessionTurnsStillAlive` are session-wide by
  design (`pkg/agent/steering.go:493-599`) — would hard-abort a live
  Critical/background delegate on the same session once the escalation
  fires, directly violating the requirement that background/Critical turns
  must survive their originating WS's closure indefinitely.
- Why rejected: correctness violation, not a style preference.

## Affected Components

- Backend: `pkg/agent/loop.go` (new `orphanWatches` field on `AgentLoop`),
  `pkg/agent/turn.go` (new `RequestHardAbort()` on `TurnCancelHook`), a new
  `pkg/agent/orphan_turn.go` (Arm/Disarm/fire + escalation timers),
  `pkg/gateway/websocket.go` (`ServeHTTP` teardown, `handleAttachSession`,
  `handleChatMessage` wiring), `pkg/config/config.go` (`GatewayConfig` field),
  `pkg/config/resolve.go` (`ResolveInt`), `pkg/config/keys.go` (new
  `ConfigKey`), `pkg/audit/events.go` + `pkg/audit/audit.go` (two new event
  kinds), `pkg/session` (optional new transcript entry type/fields for
  orphan-timeout annotation).
- Frontend: none required (no new wire frame types needed — the existing
  `cancel_stage`-style frame can be reused for observability if desired, but
  is optional since the tab that abandoned the turn is, by definition, gone).
- Variants: Open Source / Desktop / SaaS all share this code path — no
  variant-specific behavior. Non-Go bridge/sidecar channels are unaffected
  (mechanism triggers only from the webchat WS `ServeHTTP` path).

## Integration Contract

No new frontend-facing wire contract is required for the core mechanism.
Optional (recommended) additions if transcript-annotation is implemented:

- `session.TranscriptEntry` gains an entry usable for orphan-timeout
  (either a new `EntryTypeTurnOrphanTimeout` or reuse
  `EntryTypeTurnCancelled` with `CancelMethod: "orphan_timeout"` and
  `CancelledByUser: "system"`) — a backend-internal persisted shape, not a
  new REST/WS contract, so Constraint #8 (contract-first wire types) does
  not apply unless a NEW field is added to an existing generated wire type
  that crosses the gateway/SPA boundary.
