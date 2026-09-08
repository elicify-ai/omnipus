# ADR-082 — UI-independent turns and session-bound webchat streaming

- **Status:** Proposed (awaiting operator ratification) — 2026-09-08
- **Supersedes:** ADR-045 (orphaned-foreground-turn timeout) in full
- **Amends:** ADR-057 (only where it describes the ADR-045 watchdog as a cancel surface)
- **Branch:** `feat/adr-081-work-first-goal` @ `c298726a`
- **Spec:** `docs/internal/specs/ui-independent-turns-spec.md`

## 1. Operator direction (verbatim, 2026-09-08)

> 1. biggest problem is the reconnect stability and that it defaults than to none streaming
> 2. the mechanism that ends turns based on no ui/user connection must be deleted not just set to 0, important is that the turns continue, sessions need to continue without ui
> 3. please plan to fix it properly with good test coverage
> 1. first we need to solve 2 and 3 [multi-session login is deferred]

Two principles are ratified by that direction and this ADR makes them structural:

- **P1 — A turn never depends on a UI connection.** The browser is a *viewer* of a session, not its owner. Losing every viewer changes nothing about execution.
- **P2 — Streaming is a property of the session, not of a socket.** Any connection that is bound to a session sees the session's live output, from the moment it binds, regardless of which connection started the turn.

## 2. Evidence (all reproduced on the `:11001` test instance, branch build)

| # | Observation | Where in code |
|---|---|---|
| E1 | Client A starts a turn, drops after 5 tokens; the turn **keeps running and persists** a 505-word answer (`transcript.jsonl`, 04:50:27Z). Turn execution is already UI-independent. | `pkg/agent/loop.go::runTurn` never consults connection liveness |
| E2 | Client B attaches to the same session on a new connection: receives replay, then **live tokens via peer fan-out** — until exactly the point where A's dead send buffer (256 slots) fills. From then on every `Update` logs `ws: token backpressure` and returns **before** the fan-out. B receives 225 tokens, then nothing, and never a `done`. | `pkg/gateway/websocket.go::(*wsStreamer).Update` (drop-probe early return precedes `fanOutToSessionPeers`); `sendCh` cap 256; `wsConn.close` never drains it |
| E3 | Each dropped frame costs a 3-attempt backoff (0/10/50 ms) **inline in the LLM stream callback**, so a dead viewer slows the producer. | `sendRawFrameBytes` |
| E4 | The streamer is keyed by the **per-connection** `chatID` (`"webchat:"+uuid`, minted at connect). Once that connection closes, `h.sessions[chatID]` is deleted and `GetStreamer` returns `(nil,false)` → the turn's *next* LLM round runs **non-streaming** under the provider's 120 s `DefaultRequestTimeout`. | `WSHandler.GetStreamer`; `pkg/providers/common.go::DefaultRequestTimeout`; close `defer` in `handleWS` |
| E5 | Keeper-originated turns (idle steer, zero-output push, nudge ladder, fallback compile) carry the **stale** `GoalRouteChatID`; their `Send` fails twice per fire (`no active connection` + `notifyDrop` notice) every 60–90 s until the goal clears. `collectSessionConnsLocked` has a session-id fallback but it is dead: `OutboundMessage.SessionID` is never set. | `pkg/agent/goal_triggers.go::dispatchGoalAsyncFollowUp`; `pkg/gateway/webchat_channel.go::Send`; `pkg/agent/loop.go` post-turn `PublishOutbound` |
| E6 | The ADR-045 watchdog is disabled by default (`DefaultOrphanedTurnGraceSeconds = 0`) but still armed on every close, still a config key, env var, audit event, CI env (`=20` in `pr.yml` and `runci.sh`), and 7 test files. | inventory in §5 |
| E7 | The attach-time `session_state` frame carries only `pending_approvals`/`pending_asks`; a reconnecting SPA has **no way to know a turn is in flight**, so it shows an idle composer while the server is still generating. | `contracts/asyncapi.yaml` `SessionStateFrame` |

Withdrawn hypotheses (for the record): "streaming is never used" (log-level artifact), "the keeper turn is denied a streamer because its channel is `system`" (it is `webchat`; denial is the `h.sessions` miss), "a slow model explains the latency" (harness overhead measured at ≈0.6–0.9 s; the orphan mechanism above explains the stalls).

## 3. Decisions

### D1 — Delete the ADR-045 orphaned-foreground-turn watchdog in full (greenfield, no back-compat)

Not disabled, not defaulted to 0: **removed**, with a mechanical guard so a merge cannot resurrect it. Full inventory in §5. The ADR-057 cancel state machine (`RequestCancel`, `InterruptSessionHard`) is untouched — it remains the only way a turn ends early, and only on an explicit Stop/cancel surface. Two *different* mechanisms share the word "orphan" and are **kept**: the subagent-span forwarder watchdog (`startOrphanWatchdog`/`orphanWatchdogTimeout`, synthesizes a closing span frame) and `SubTurnOrphan`.

### D2 — The webchat streamer is bound to the session, resolved per frame

`WSHandler.GetStreamer(channel, chatID, sessionID)` returns a streamer for **every** webchat turn that has a session id — including when zero connections are bound. `wsStreamer` drops its `conn` field. On every `Update`/`Finalize`/tool-call/status frame it resolves the **current** set of connections bound to `sessionID` under `h.mu` and sends to each through `sendRawFrameBytes` (which already honours per-connection replay divert and backpressure). Consequences:

- A drop on one connection is that connection's problem only. There is no early return before other viewers are served. `DoneStats.TokensDropped` is computed per target connection.
- With zero bound connections, frames are discarded at the streamer **without** entering the per-frame backoff (no channel to wait on). The transcript remains the durable record.
- Because a streamer always exists, the "no streamer → non-streaming `Chat` under 120 s" path (E4) can no longer be entered for webchat. `ChatStream` is the only webchat provider path.

### D3 — Catch-up on bind

When a connection binds to a session (new message, `attach_session`, reconnect), and that session has an in-flight turn, the gateway sends — after the replay history and before any live frame — a single `token` frame carrying the turn's **accumulated-so-far** text, then the live stream continues. Ordering is guaranteed by binding and snapshotting under one `h.mu` critical section: any `Update` after the bind reaches the new connection (diverted during replay, drained after), the snapshot precedes them. No duplication, no gap.

### D4 — `session_state` announces the in-flight turn (contract-first)

`SessionStateFrame` gains an optional `active_turn` object (`turn_id`, `agent_id`, `started_at`). Schema in `contracts/components/schemas/`, referenced from `asyncapi.yaml`, regenerated via `scripts/gen-contracts.sh`, committed atomically. The SPA, on receiving it, opens the streaming bubble and shows Stop exactly as if it had started the turn.

### D5 — SPA reconnect re-binds automatically

On every WebSocket reopen the SPA re-attaches its active session (`attach_session` with its `since` cursor). Combined with D3/D4 a reload, a network blip, or a second tab all converge on the same state: history, catch-up, live tail, Stop available.

### D6 — Outbound and keeper delivery is session-addressed

`runAgentLoop`'s post-turn `PublishOutbound` sets `OutboundMessage.SessionID = opts.TranscriptSessionID`. `webchatChannel.Send` resolves connections by session id first, chat id second. With zero bound connections a webchat `Send` **succeeds** (the message is already in the transcript and replays on the next bind) instead of failing and triggering `notifyDrop`. `goalRoute.chatID` becomes advisory; `GoalRouteSessionID` is the key that matters. E5's twin `Send failed` lines disappear.

### D7 — Mechanical guard

`scripts/check-no-orphan-turn-watchdog.sh` fails the build if any symbol in §5's guard list reappears as a definition or a non-comment reference. Wired into `.github/workflows/pr.yml`, `deploy/ci-worker/runci.sh`'s `lint` gate, and `make lint-no-orphan-turn-watchdog`, matching the ADR-081 and ADR-077 guards.

### D9 — The goal card is anchored at its `set_goal` call, not at the thread tail

**Evidence.** `GoalThreadTailCards` is mounted after the message list in three places (`src/components/chat/ChatScreen.tsx`: `PlainMessageList`, `VirtualizedMessageListInner`, and the empty-thread branch) and renders `GoalEchoCard` from `goalPills` in the store. The `set_goal` tool-call part exists at its correct chronological index on both the live path (`FallbackToolUI`) and the replay path (`VirtualAssistantMessageRow`'s `splitMessageParts`), but `src/lib/toolVisibility.ts` returns `false` for `set_goal` unconditionally and `GenericToolCall` renders `null`. The `goal_id` is minted server-side and only ever reaches the SPA on the `goal_status` frame; `set_goal`'s arguments and result carry no id, so nothing at the call site can look the record up.

**Decision.** The card renders **in place of the `set_goal` tool call**, from the call's own result, and stays there in history:

- `set_goal`'s result payload (`pkg/tools/set_goal.go`, the success return) gains `goal_id` and the full registered record (`definition`, `criteria`, `dod`, `assessment`) — the tool already holds the session id and the record-access seam. Contract-first: the result shape is documented in the tool's schema; no WS frame changes.
- A dedicated `set_goal` tool UI is registered (`makeAssistantToolUI`, live path) and an explicit branch is added to `VirtualAssistantMessageRow`'s parts loop (replay path), both rendering `GoalEchoCard` from the result. `toolVisibility.ts` keeps the *raw* call hidden but no longer suppresses the card; `GenericToolCall`'s self-gate is bypassed by the dedicated UI.
- Live progress (criterion met, state changes) overlays by `goal_id` from `goalPills`; the result is the fallback when no frame has arrived yet, so the card is correct on first render and after reload alike.
- Each `set_goal` call renders its own card at its own position (a `mode: amend` call shows the amended record where the amendment happened). `GoalThreadTailCards.tsx` and its three mounts are deleted; the empty-thread branch needs nothing because a `/goal` in an empty session produces an assistant message containing the call.
- The store's `goalPills` remains the source for the header pill and for live overlay only.

### D8 — Deferred (explicitly out of scope here)

- **Multi-session login** (single-slot cookie `SessionTokenHash`, `pkg/gateway/rest_auth.go`): operator ordered "first 2 and 3". Filed as the next ADR; D2/D6 make it safe to do because a kicked socket no longer harms a turn.
- **Non-webchat 120 s `DefaultRequestTimeout`** on non-streaming `Chat` (Telegram/Discord/…): unchanged; separate issue.
- Reasoning-effort mapping, judge deadline/model: tracked separately (session table). Goal-card ordering is now D9.

## 4. Consequences

- Turns are provably viewer-independent (tests in spec §6 close the socket mid-turn and assert completion and persistence).
- A reconnect mid-turn is indistinguishable from never having left.
- No more silent degrade to non-streaming for webchat; no more backoff-induced slowdown from dead viewers.
- The goal keeper's follow-ups reach whichever tab is open, or none, without error noise.
- Deleting a config key is a greenfield break: an operator config carrying `gateway.orphaned_turn_grace_seconds` is ignored (config loading does not reject unknown keys). Documented in the changelog.

## 5. Deletion inventory (ADR-045)

**Delete outright**

| Path | What |
|---|---|
| `pkg/agent/orphan_watch.go` | whole file (`ArmOrphanForegroundTurnWatch`, `DisarmOrphanForegroundTurnWatch`, `fireOrphanForegroundTurnWatch`, `orphanWatch*` types) |
| `pkg/agent/orphan_watch_test.go` | whole file |
| `pkg/gateway/orphan_foreground_turn_watch_test.go` | whole file |
| `pkg/config/orphan_grace_test.go` | whole file |
| `pkg/agent/loop.go` | `AgentLoop.orphanWatches` field + comment (~L139) |
| `pkg/gateway/websocket.go` | arm block in the `handleWS` close `defer` (the `armOrphanWatch` computation and the `ArmOrphanForegroundTurnWatch` call, ~L668–692); `reapOrphanForegroundTurn` (~L2424); `sessionStillOrphaned` (~L2459); every `DisarmOrphanForegroundTurnWatch` call (attach/rebind sites, incl. ~L1749, ~L2684) |
| `pkg/agent/turn.go` | `getActiveRootTurnStateForSession` (~L1199) — sole caller is `orphan_watch.go` |
| `pkg/agent/steering.go` | `hasLiveCriticalDelegate` (~L913) — sole caller is `orphan_watch.go` |
| `pkg/config/config.go` | `GatewayConfig.OrphanedTurnGraceSeconds` (+ env tag `OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS`), `DefaultOrphanedTurnGraceSeconds`, `EffectiveOrphanedTurnGraceSeconds` |
| `pkg/config/keys.go` | `GatewayOrphanedTurnGraceSeconds` |
| `pkg/config/resolve.go` | the usage example comment (~L29–31) |
| `pkg/audit/events.go` | `EventTurnOrphanTimeout` (`turn.orphan_timeout`) — no SPA/contract consumer (verified: zero hits in `src/`, `contracts/`) |
| `.github/workflows/pr.yml` ~L2094–2108, `deploy/ci-worker/runci.sh` ~L601–603 | the `OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS=20` env + comments |

**Edit**

| Path | What |
|---|---|
| `pkg/agent/routing_session_id_consumer_set_adr057_test.go` ~L202–213 | remove the hard-coded `"orphan_watch.go"` consumer entry |
| `pkg/agent/turn_adr057_test.go`, `cancel_orchestration_adr057_test.go`, `pkg/gateway/cancel_armed_ack_test.go` | drop the watchdog-specific cases/helpers; keep every cancel assertion |
| `pkg/agent/turn.go` ~L770/804, `pkg/agent/subturn.go` ~L1573 | comments cite `orphan_watch.go` as the CompareAndDelete precedent — reword |
| `pkg/gateway/rest_pending_restart.go` ~L69 | comment about the key not being restart-gated — remove |
| `tests/e2e/cancel-cross-channel.spec.ts` ~L995 | comment referencing the CI env — remove |
| `docs/internal/architecture/ADR-045-…md` | Status → Superseded by ADR-082 |
| `docs/internal/architecture/ADR-057-…md`, `…-review.md`, `docs/internal/specs/adr-057-…spec.md`, `…-review.md`, `adr-058-…spec.md`, `unified-goal-plan-subagent-spec.md` | annotate references (do not rewrite history) |
| `CLAUDE.md` | add a "Retired surfaces" entry mirroring the ADR-081 one |

**Guard list** (`scripts/check-no-orphan-turn-watchdog.sh`): `ArmOrphanForegroundTurnWatch`, `DisarmOrphanForegroundTurnWatch`, `fireOrphanForegroundTurnWatch`, `reapOrphanForegroundTurn`, `sessionStillOrphaned`, `hasLiveCriticalDelegate`, `getActiveRootTurnStateForSession`, `OrphanedTurnGraceSeconds`, `DefaultOrphanedTurnGraceSeconds`, `EffectiveOrphanedTurnGraceSeconds`, `GatewayOrphanedTurnGraceSeconds`, `OMNIPUS_GATEWAY_ORPHANED_TURN_GRACE_SECONDS`, `EventTurnOrphanTimeout`, `turn.orphan_timeout`. Explicitly **not** guarded (kept): `startOrphanWatchdog`, `orphanWatchdogTimeout`, `orphanWatchdogMaxRechecks`, `SubTurnOrphan`.

## 6. Test coverage (summary — full matrix in the spec)

- **Unit (Go)** — streamer resolves current bindings per frame; zero-listener path takes no backoff; one dead viewer never starves another; catch-up snapshot ordering under concurrent `Update`; `GetStreamer` non-nil for webchat with zero bindings; `Send` by session id, success with zero bindings; config key gone; no `orphanWatches` field.
- **Integration (Go, fake streaming provider)** — the exact E2 reproduction: A drops after N tokens, B attaches, asserts catch-up text == A's received prefix, all remaining tokens, one `done`, transcript persisted; **no** connection at all: turn completes and persists; keeper follow-up after reconnect reaches the new connection with zero `Send failed` logs.
- **E2E (Playwright, real binary)** — reload mid-turn: bubble continues, Stop visible, `done` arrives; second tab attaches mid-turn and sees the same; network drop (`context.setOffline`) and recovery.
- **Guard** — `scripts/check-no-orphan-turn-watchdog.sh` self-test with a planted symbol.
- **Goal card (D9)** — Go: `set_goal` result carries `goal_id` + record; vitest: card renders at the tool-call index on live and replay paths, tail component gone, amend renders at the amend position, live overlay by id; e2e: after `/goal` and a follow-up message, the card sits between the user's goal message and the follow-up, and stays there after reload.
