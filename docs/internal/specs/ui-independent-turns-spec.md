# Spec — UI-independent turns and session-bound webchat streaming (ADR-082)

- **ADR:** `docs/internal/architecture/ADR-082-ui-independent-turns-and-session-bound-streaming.md`
- **Status:** Draft for ratification — 2026-09-08
- **Greenfield:** no back-compat for the deleted watchdog or its config key.

## 1. Actors and problem

- **Operator/user** on the web SPA: reloads, loses Wi-Fi, opens a second tab, or is logged out elsewhere while an agent is mid-turn.
- **Agent turn** (`pkg/agent`): must run to completion and persist regardless of viewers.
- **Goal keeper** (`pkg/agent/goal_*`): dispatches follow-up turns hours after the original connection died.

Today a lost connection (a) silently degrades the rest of the turn to non-streaming under a 120 s cap, (b) starves any other viewer once the dead buffer fills, (c) leaves a reconnecting SPA unaware a turn is in flight, and (d) makes keeper deliveries fail twice per fire. A legacy watchdog that *ends* turns on lost connections still exists, disabled.

## 2. Functional requirements

| ID | Requirement | ADR |
|---|---|---|
| FR-001 | The system MUST NOT end, cancel, or degrade a turn because zero UI connections are bound to its session. | D1 |
| FR-002 | The ADR-045 watchdog, its config key `gateway.orphaned_turn_grace_seconds`, env var, audit event, and all listed symbols MUST be absent from the tree; the guard script MUST fail on reappearance. | D1, D7 |
| FR-003 | Every webchat turn with a session id MUST obtain a streamer, including with zero bound connections; the webchat path MUST use `ChatStream` for every LLM round of the turn. | D2 |
| FR-004 | Each streamed frame MUST be delivered to every connection bound to the session **at send time**, not to a connection captured at turn start. | D2 |
| FR-005 | A drop or backpressure on one connection MUST NOT prevent delivery to any other connection, and MUST NOT cause the streamer to skip the fan-out. | D2 |
| FR-006 | With zero bound connections, per-frame delivery MUST cost no backoff wait (the producer is not slowed by absent viewers). | D2 |
| FR-007 | When a connection binds to a session with an in-flight turn, it MUST receive, after replay history and before any live frame, exactly one catch-up `token` frame whose content equals the turn's accumulated text at bind time; subsequent live frames MUST continue from that point with no duplicate and no gap. | D3 |
| FR-008 | `session_state` MUST carry `active_turn {turn_id, agent_id, started_at}` when a turn is in flight, and omit it otherwise. Defined contract-first; generated types committed atomically. | D4 |
| FR-009 | The SPA, on `session_state.active_turn`, MUST render the streaming assistant bubble and the Stop control; on catch-up/live tokens it MUST append to that bubble; on `done` it MUST finalize once. | D4, D5 |
| FR-010 | On every WebSocket reopen the SPA MUST re-attach its active session with its `since` cursor, without user action. | D5 |
| FR-011 | Post-turn outbound publication MUST carry the transcript session id; webchat `Send` MUST resolve targets by session id first. | D6 |
| FR-012 | A webchat `Send` with zero bound connections MUST succeed (no `ErrSendFailed`, no drop notice); the content is durable in the transcript. | D6 |
| FR-013 | Keeper follow-up turns (idle steer, zero-output push, nudge, fallback compile) MUST stream to whichever connections are bound to the session at that time, and MUST log no `Send failed` when none is. | D2, D6 |
| FR-014 | Per-connection drop counters remain per connection; `DoneStats.TokensDropped` MUST reflect the receiving connection only. | D2 |
| FR-015 | Explicit Stop (`RequestCancel`) behaviour is unchanged and MUST continue to pass every existing cancel test. | D1 |
| FR-016 | `set_goal`'s success result MUST carry `goal_id` and the full registered record (`definition`, `criteria`, `dod`, `assessment`). | D9 |
| FR-017 | The goal card MUST render at the chronological position of its `set_goal` call, on both the live and the replay/reload path, from the call's result; no tail-mounted goal card component exists. | D9 |
| FR-018 | Live `goal_status` frames MUST overlay progress onto the card by `goal_id` without moving it. | D9 |
| FR-019 | A `mode: amend` call MUST render the amended record at the amendment's own position; the earlier card remains at its position with its own record. | D9 |

## 3. Scenarios (Given/When/Then)

**S-01 Turn survives the only viewer leaving** — Given a webchat turn streaming on connection A, When A closes, Then the turn runs to completion, the assistant entry is persisted, and no cancel is recorded. *(FR-001)*

**S-02 No viewer ever** — Given a keeper follow-up dispatched for a session with zero bound connections, When it runs, Then it completes and persists, `Send` returns nil, and the log has no `Send failed`/`no active connection`. *(FR-001, FR-012, FR-013)*

**S-03 Reconnect mid-turn, exact reproduction** — Given A received N tokens then dropped, When B attaches on a new connection while the turn is still generating, Then B receives replay, then one catch-up `token` whose content is exactly the concatenation of everything generated so far, then every remaining token, then exactly one `done`; the final bubble text equals the persisted transcript entry. *(FR-004, FR-005, FR-007)*

**S-04 Dead buffer does not starve peers** — Given A's connection is dead but not yet closed (send buffer full), When tokens continue, Then B receives every token; A's drop counter increments; the fan-out is never skipped. *(FR-005, FR-014)*

**S-05 Streaming never downgrades** — Given a turn whose first round streamed and whose only connection then closed, When the next LLM round begins (after a tool call), Then it is a `ChatStream` call with no request timeout, not a `Chat` call. *(FR-003)*

**S-06 Catch-up ordering under concurrency** — Given tokens arriving at 1 ms intervals, When 20 connections bind at random moments, Then each connection's catch-up + live stream reassembles to the identical full text (no duplicates, no gaps). *(FR-007)*

**S-07 session_state announces the in-flight turn** — Given an in-flight turn, When a connection attaches, Then `session_state.active_turn` is present with the turn id and agent id; When no turn is in flight, Then the field is absent. *(FR-008)*

**S-08 SPA reload mid-turn** — Given a long turn streaming in the browser, When the page reloads, Then within 5 s the assistant bubble shows the text so far, the Stop control is visible, tokens keep appending, and `done` finalizes once. *(FR-009, FR-010)*

**S-09 Second tab** — Given a turn streaming in tab 1, When tab 2 opens the same session, Then tab 2 shows catch-up + live tail, and both tabs end with identical text. *(FR-004, FR-009)*

**S-10 Network blip** — Given a turn streaming, When the browser goes offline for 10 s and back online, Then the SPA re-attaches automatically and the bubble continues without user action. *(FR-010)*

**S-11 Stop still works from the new tab** — Given S-09, When tab 2 clicks Stop, Then the turn cancels via `RequestCancel` and both tabs see the interrupted state. *(FR-015)*

**S-12 Guard** — Given any guarded symbol planted in a non-comment position, When the guard script runs, Then it exits non-zero and names the symbol; on a clean tree it exits 0. *(FR-002)*

**S-13 Config key removed** — Given a `config.json` carrying `gateway.orphaned_turn_grace_seconds`, When the gateway loads, Then it boots, the key has no effect, and no watchdog is armed on connection close. *(FR-002)*

**S-14 Keeper after reconnect** — Given a goal activated on connection A, A closed, B attached, When the idle keeper fires, Then the follow-up turn streams to B and `GoalLatestReason` records no routing-lost note. *(FR-013)*

**S-15 Card sits where the goal was set** — Given `/goal …` then a follow-up user message, When the thread renders (live) and again after a reload (replay), Then the goal card appears between the assistant's goal-setting text and the follow-up message, and nothing goal-related renders at the thread tail. *(FR-016, FR-017)*

**S-16 Amendment renders in place** — Given a registered goal and a later `set_goal` amend call, When the thread renders, Then two cards exist, each at its own call position with its own record. *(FR-019)*

**S-17 Live overlay does not move the card** — Given a card rendered from its result, When a `goal_status` frame marks a criterion met, Then the card updates in place. *(FR-018)*

## 4. Test matrix

| # | Test | Level | Location | Scenarios |
|---|---|---|---|---|
| T-01 | `TestWSStreamer_ResolvesBindingsPerFrame` | unit | `pkg/gateway/ws_streamer_session_bound_test.go` | S-03, S-04 |
| T-02 | `TestWSStreamer_ZeroListeners_NoBackoff` (asserts elapsed < backoff floor over 1000 frames) | unit | same | FR-006 |
| T-03 | `TestWSStreamer_PeerDropDoesNotSkipFanOut` | unit | same | S-04 |
| T-04 | `TestWSStreamer_DoneStatsPerConnection` | unit | same | FR-014 |
| T-05 | `TestGetStreamer_WebchatAlwaysNonNil` | unit | `pkg/gateway/ws_get_streamer_test.go` | S-05 |
| T-06 | `TestAttach_CatchUpSnapshotOrdering` (race-run, 20 binders, 1 ms tokens) | unit, `-race` | `pkg/gateway/ws_catchup_test.go` | S-06 |
| T-07 | `TestSessionStateFrame_ActiveTurn` (+ generated contract test) | unit | `pkg/gateway/ws_session_state_test.go`, `pkg/api/generated/contract_test.go` | S-07 |
| T-08 | `TestWebchatSend_BySessionID_ZeroConnsSucceeds` | unit | `pkg/gateway/webchat_channel_test.go` | S-02, FR-012 |
| T-09 | `TestRunAgentLoop_PublishOutboundCarriesSessionID` | unit | `pkg/agent/outbound_session_id_test.go` | FR-011 |
| T-10 | `TestTurn_ContinuesWhenOnlyConnectionCloses` (fake streaming provider, 200 tokens, close after 5, assert persisted length) | integration | `pkg/gateway/turn_survives_disconnect_test.go` | S-01 |
| T-11 | `TestReconnectMidTurn_CatchUpThenLive` — the E2 reproduction end-to-end in-process | integration | same | S-03 |
| T-12 | `TestTurn_NextRoundStreamsAfterDisconnect` (tool call between rounds; provider fake records `Chat` vs `ChatStream`) | integration | same | S-05 |
| T-13 | `TestKeeperFollowUp_ReachesRebindedConnection` | integration | `pkg/agent/goal_keeper_reconnect_test.go` | S-14, S-02 |
| T-14 | `TestConfig_OrphanGraceKeyIgnored` | unit | `pkg/config/config_test.go` | S-13 |
| T-15 | `TestNoOrphanWatchArmedOnClose` (asserts no goroutine/timer registered on close) | unit | `pkg/gateway/ws_close_test.go` | S-13 |
| T-16 | existing cancel suites unchanged: `cancel_orchestration_adr057_test.go`, `cancel_armed_ack_test.go`, `tests/e2e/cancel-cross-channel.spec.ts` | regression | existing | S-11, FR-015 |
| T-17 | `check-no-orphan-turn-watchdog.test.sh` (plant + clean) | guard | `scripts/` | S-12 |
| T-18 | `reconnect-mid-turn.spec.ts`: reload, second tab, offline/online | e2e | `tests/e2e/` | S-08, S-09, S-10 |
| T-19 | SPA unit: `chat.store` handles `session_state.active_turn`, catch-up token, single `done` | vitest | `src/store/__tests__/chat.reconnect.test.ts` | FR-009 |
| T-20 | SPA unit: `ws.ts` reopen → `attach_session` with `since` | vitest | `src/lib/__tests__/ws.reattach.test.ts` | FR-010 |
| T-21 | `TestSetGoal_ResultCarriesGoalIDAndRecord` | unit | `pkg/tools/set_goal_test.go` | S-15 |
| T-22 | SPA unit: `set_goal` tool UI renders `GoalEchoCard` at the part index (live), `VirtualAssistantMessageRow` renders it at the part index (replay), `GoalThreadTailCards` no longer exists, amend renders twice, overlay by id | vitest | `src/components/chat/__tests__/goal-card-anchoring.test.tsx` | S-15, S-16, S-17 |
| T-23 | `goal-card-position.spec.ts`: `/goal`, follow-up message, assert DOM order; reload; assert order again | e2e | `tests/e2e/` | S-15 |

Determinism rules: integration tests use a fake provider that streams a fixed token list on a controllable clock; no live model. E2E uses the real binary with a live model but asserts only structure (bubble present, Stop visible, `done` once, final text equals transcript), never content.

## 5. Wave plan (parallel worktrees, same discipline as ADR-081)

| Wave | Scope | Agent | Depends on |
|---|---|---|---|
| W1 | D1 deletion + guard script + CI/Makefile wiring + doc annotations (§5 of ADR) | backend-lead | — |
| W2 | Contract: `SessionStateFrame.active_turn` schema + regen | backend-lead | — |
| W3 | D2/D3/D6 gateway: session-bound `wsStreamer`, per-frame resolution, zero-listener path, catch-up snapshot, `Send` by session id, `PublishOutbound` session id | backend-lead | W2 |
| W4 | SPA: reopen → re-attach, `active_turn` handling, catch-up bubble, single `done` | frontend-lead | W2 |
| W5 | Tests T-01..T-17, T-19, T-20 | qa-lead | W1, W3, W4 |
| W6 | E2E T-18 + Playwright UAT on the test instance | qa-lead | W3, W4 |
| W7 | D9: `set_goal` result payload (Go) + `set_goal` tool UI, replay branch, delete `GoalThreadTailCards` (SPA) + T-21..T-23 | backend-lead + frontend-lead, then qa-lead | — (independent of W1–W6) |
| Gate | `/code-review large` on the whole diff, 7-reviewer gate, CI green, then manual UAT prompt | lead | all |

## 6. Holdout (manual, post-implementation — not referenced by the matrix)

1. Start a long goal, close the laptop lid for 5 minutes, reopen: the bubble is complete or still streaming, Stop is available, no error toast.
2. Start a turn on the phone, open the same session on the desktop: desktop shows the full text so far and continues.
3. Kill the browser entirely mid-turn; reopen an hour later: transcript complete; no `Send failed` in `gateway.log`.
4. Log in from a second device (once the deferred multi-session ADR lands): the first device's running turn is unaffected.
