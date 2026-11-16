`Status: Approved`

`ADR: none — no open design decision` (the founder chose option A on 2026-11-15; the architect ruled on the wire shape, reload and nested helpers on 2026-11-15 — ruling recorded in Clarifications below)

# Feature Specification: Helper model notices reach the helper's chat and a named line in the main chat

**Created**: 2026-11-15 (revised 2026-11-15 after grill-spec round 1 and the architect ruling)
**Input**: founder decision Q1 A on #1537 (relayed by the chief in #cross-team, 2026-11-15 14:59Z); probe result `/workspace/logs/1537/RESULT-1537.md`; tester report `/workspace/logs/1537/tester/report.md`; validator report `/workspace/logs/1537/validator/report.md` (VALIDATOR VERDICT: CONFIRMED); architect ruling `/workspace/logs/1537/architect/ruling.md`; grill round 1 `docs/internal/specs/helper-model-notices-spec-review.md`. No interview-me file exists for this issue: the founder's input is the A/B/C/D options page and the one-line answer "Q1 A".
**Epic**: #1143 "[Epic] Release v0.1.1: fix the issues found in the current release" (chief, 2026-11-15 16:19Z). Related open issue: #1280.
**Riders**: #1850 "Delegated agents: model-delay notices overwrite each other in one chat" (same frames, same browser slots; folded in as User Story 4; chief confirmed it stays a rider, 2026-11-15 15:05Z; owner Team B). Not a rider: #1851 "Delegated agents: a helper's model retry is never announced to chat channels" (channels stay as they are; see Non-Behaviors).
**Agent tool field table**: no agent-managed object changed.

---

## Overview

A helper is an agent that another agent (the main agent) hands work to. It runs in its own chat. When a helper's model is slow or refuses for a moment, Omnipus has three notices: "the model is rate-limiting, retrying in 0:10" (retry), "the model hasn't responded for 31s, still waiting" (silence), and "connection came back after 1 retry" (recovered).

Today, for the cases the 15 Nov probe tested (a rate-limit retry and a 30-second silence), these notices for a helper go only to the **main** chat. They are drawn there only while the main agent is still writing its own reply, and they don't say which agent they are about. In the common case the main agent hands off the work and finishes its reply. Then **nobody sees the notice**: the main chat is idle, and the helper's own chat shows only "Waiting for response…". Two other retry kinds (the model not starting to answer in time, and an empty reply) already reach the helper's own chat but never the main chat.

The founder chose option A. A helper's notices go to the **helper's own chat**, where the waiting happens, just as the "switched to the fallback model" note already does. The main chat also shows a short line that **names the helper** ("Researcher: …"). That line is shown whether or not the main agent is still writing, and it comes back after a reload while the helper still waits. The rider #1850 is folded in: a helper's notice must never overwrite or clear the main agent's own notice, and two helpers' notices must never overwrite each other.

## Existing Codebase Context

GitNexus was not available on this pod. The facts below come from ripgrep and reading the release tree at `f408d81c7`, checked by the architect and by grill round 1.

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `pkg/agent/provider_retry_plan.go::providerRetryObserver` | modifies | Fallback-chain retries (rate limit and the #1280 causes): `provider_retry` stamped with `routingSessionID` (the top chat for a helper turn) |
| `pkg/agent/loop_run_turn_response.go::agentLoopRunTurnResponseCallLLMWithRetries.retryTimeout` (with `timeoutRetryPayload`) | modifies | The #1664 retries (`no_response`, `empty_reply`): emits `EventKindLLMRetry` addressed to the **helper's own chat** (`transcriptSessionID`); the gateway's `pkg/gateway/context_window_notice.go::WSHandler.hubLLMRetry` turns them into `provider_retry` |
| `pkg/agent/provider_status.go::emitProviderStatus`, `startProviderSilenceWatch`, `noteProviderRecovered` | modifies | `provider_status` waiting / responding / recovered, stamped with `routingSessionID`; recovered is emitted only when retries ≥ 1 |
| `pkg/agent/events.go::LLMRetryPayload`, `pkg/agent/provider_status.go::ProviderStatusPayload` | modifies | In-process payloads; gain the helper's own chat id |
| `pkg/agent/fallback_note.go::queueProviderFallbackNote` | precedent, unchanged | The fallback note already goes to `transcriptSessionID`, the helper's own chat, and is saved there |
| `pkg/agent/provider_retry_plan.go::publishRetryNotice` | reads, unchanged | Channel retry notice; suppressed for steered helpers (that is #1851, out of scope) |
| `pkg/gateway/websocket_forward_hub.go::WSHandler.hubProviderRetry`, `hubPublishMetaAlsoTo`, `hubTurnEnd` | modifies | Publishes the frame into one chat's hub (each hub numbers its own frames); `hubTurnEnd` forgets a helper turn's projection items but sends no clearing frame |
| `pkg/gateway/websocket_forward_provider_status.go::WSHandler.hubProviderStatus` | modifies | Same for `provider_status` |
| `pkg/gateway/ws_hub_projection.go::activeTurnProjection` (`update`, `item`, `projectionFrames`) | modifies | Re-sends a chat's live notices to a freshly attached tab. Today `done` clears the **whole** projection, and items keep their first-created order |
| `pkg/gateway/ws_tool_approval.go::WSHandler.sessionStateBytes` | modifies (additive) | `session_state.emitted_at` (unchanged) and the new `session_state.emitted_at_ms`: the server's current time, from one clock reading, sent on every attach |
| `src/store/chat/slices/provider-frames.ts::handleProviderFrame` | modifies | One retry slot and one status slot per chat; any newer frame replaces the slot; status frames clear the retry slot; builds the recovered line as a system message. This is the #1850 mechanism |
| `src/store/chat/messages.ts::endTurnState` | modifies (scope only) | Clears the chat's own retry slot at its turn end regardless of turn id; must keep touching the own slot only |
| `src/components/chat/ProviderRetryIndicator.tsx` (`remainingRetrySeconds`), `src/components/chat/ProviderWaitingIndicator.tsx` | modifies | Own hidden polite live region plus a ticking visible span; countdown computed from the single frame's `sent_at` |
| `src/components/chat/ChatScreen.tsx` (provider notice block above the composer) | modifies | Notices mount only when `isStreaming`; this is why an idle main chat shows nothing |
| `src/hooks/useRunningActivity.ts::resolveAgent` | reads, not reused as is | Returns `'Unknown agent'` for an absent id but **the raw id** for an id missing from the agent list |
| `src/lib/ws.ts::_parseServerFrame`, `src/lib/api/generated/ws-schemas.ts` | reads | Frame schemas are strict: a frame with unknown fields or an unknown type is dropped |
| `pkg/agent/steer_launcher.go::walkVerifiedRoot`, `pkg/agent/steer_frames.go::deliverSubagentEnd` | reads | A helper of a helper routes its notices to the top chat; its `subagent_end` goes to its direct parent only |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|----------------|------------|----------------|----------------|
| `providerRetryObserver`, `retryTimeout` path | MEDIUM | all four retry causes | every chat that shows a retry countdown |
| `emitProviderStatus` | MEDIUM | silence watch, recovered note | silence notice and recovered line in every chat |
| `activeTurnProjection` (done clearing, item order) | **HIGH** | every frame a freshly attached tab receives for an active turn | reload behaviour of tool cards, media and errors |
| `handleProviderFrame` | **HIGH** | every `provider_retry` / `provider_status` frame for every chat | own notices (#1478/#1280/#1664 tests) |
| Retry/waiting indicators (clock, live region) | MEDIUM | own notices on reload | design-system screenshot baselines if the region changes |

**HIGH rows:** the chief signs off before GREEN starts (2026-11-15 15:05Z). The main agent's own live notices must behave exactly as today (Regression Test Requirements). The projection change is limited to the `provider_retry:` and `provider_status:` keys.

---

## Contract Changes (contract-first — Hard Constraint #8)

Shape settled by the architect (2026-11-15): two additive optional fields plus one new status value. No new frame type. Chief ruling Q1 A (2026-11-16) adds one more additive optional field, `SessionStateFrame.emitted_at_ms`.

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| ProviderRetryFrame | changed (additive) | `contracts/components/schemas/ProviderRetryFrame.yaml` **and** the inline `components.schemas.ProviderRetryFrame` in `contracts/asyncapi.yaml` (the generator reads the inline copy) | Two new optional properties: `child_session_id` (string, 1–255) and `agent_id` (string, 1–255). Set only on the main-chat copy of a helper's notice: `child_session_id` is the helper's own chat (the session whose model call this is), `agent_id` is that helper's agent. Both present or both absent. Absent on a frame about the chat's own turn and on the copy sent to the helper's own chat. The description's "the turn-end frame clears it" gains: "a main-chat copy is cleared by the helper's `provider_status` `ended` or `responding` frame, never by the main chat's own turn end." |
| ProviderStatusFrame | changed (additive) | `contracts/components/schemas/ProviderStatusFrame.yaml` **and** the inline `components.schemas.ProviderStatusFrame` in `contracts/asyncapi.yaml` | The same two optional properties with the same meaning and the same both-or-neither rule, and a new `state` value `ended`: sent only on a main-chat copy (child fields set) when that helper turn has ended; the main chat removes that helper's named line. `ended` never appears on a frame without `child_session_id`. |
| `contracts/asyncapi.yaml` message descriptions | changed | `provider_retry` / `provider_status` | A helper's notice is published twice: once to the helper's chat (no child fields) and once to the top chat (child fields set). When the helper's turn ends, the gateway sends `provider_status` `ended` to the top chat for every helper turn that left a named line there. |
| SessionStateFrame | changed (additive) | `contracts/components/schemas/SessionStateFrame.yaml` **and** the inline `components.schemas.SessionStateFrame` in `contracts/asyncapi.yaml` | New optional `emitted_at_ms` (integer, int64, minimum 0, Unix epoch milliseconds of the same instant as `emitted_at`, from one clock reading). `emitted_at` keeps its meaning and whole-second format. The browser prefers `emitted_at_ms` for the FR-008 server-clock sample and falls back to `emitted_at` when it is absent. Parity test: `TestContract_SessionStateFrame_EmittedAtMs` checks both YAML copies and the generated SPA schema. A browser tab still running an older SPA rejects the new field and must be reloaded after a gateway upgrade. (Correction 2026-11-16, chief ruling Q1 A: an additive millisecond field replaces the whole-second clock sample; `emitted_at` is unchanged.) |

Sequence (Hard Constraint #8): backend edits both representations of each schema → runs `scripts/gen-contracts.sh` → commits the generated `pkg/api/generated/` and `src/lib/api/generated/` with the contract change in one commit → consumers use the generated types only → `make verify-contracts` shows no drift.

Extends only these three schemas. It must not collide with:
- `SubagentStartFrame` / `SubagentStateFrame`: same names, same meaning. For a direct helper, `child_session_id` is the same value those frames carry. For a helper of a helper, the top chat has no `subagent_start` for it; the named line is the only trace of it there.
- `ProviderFallbackFrame`: unchanged. It still goes to the helper's chat only.

`turn_id` on the main-chat copy stays the helper's turn id. The main agent's own turn end therefore never clears it; only `ended` or `responding` for that turn does. A later turn of the same helper chat has a new `turn_id`, so a late `ended` for an old turn cannot clear a new wait.

A frame that breaks the both-or-neither rule, or `ended` without child fields, is a contract violation: the browser logs it and ignores it, like today's unknown-state branch.

## API and Data

- No REST endpoint changes.
- **One rule for every retry producer:** the fallback-chain retry (rate limit and #1280 causes) and the #1664 retries (`no_response`, `empty_reply`) all get the same two copies. Context-window notices and other loop retries that are not forwarded today stay out of scope.
- **Where the copy is made:** in the gateway, from one agent event. The agent adds the helper's own chat id to the retry and status payloads when the turn's own chat differs from its top chat. The gateway publishes two copies from the same payload. The copies are byte-equal except `session_id`, `child_session_id`, `agent_id` and `seq`.
- **Clearing the named line in the main chat:**
  - Silence then data: the helper's `responding` frame is copied as well.
  - Retry then success with the same model: the `recovered` frame is copied.
  - Retry then success **without** a `recovered` frame (a fallback model answered on its first call, or an outer `no_response` / `empty_reply` retry was followed by a prompt answer): the top chat receives a `provider_status` `responding` copy for that helper turn when the model answers, and the named line clears. No retry count is invented, and the fallback note itself is not copied to the main chat. The helper's own chat keeps today's own-chat rule for this case (FR-004).
  - Helper turn ends (finished, failed or stopped): `ended` to the top chat.
- **Producer order (silence then retry):** when a retry is scheduled while a silence notice is active, the `responding` that clears the silence is published **before** the new `provider_retry`, in both chats. A `responding` therefore only ever clears notices published before it, and the new countdown survives, live and on replay.
- **Same turn, later retry:** a helper turn can retry, recover, run a tool and later retry again under the same `turn_id`. The later retry is current and replaces the recovered row; only an older retry superseded by a later recovery must not come back on replay.
- **Reload and reconnect** use the existing active-turn projection; the snapshot gains only the additive `session_state.emitted_at_ms` clock field (Correction 2026-11-16, chief ruling Q1 A: an additive millisecond field replaces the whole-second clock sample; `emitted_at` is unchanged.) Three changes make it correct:
  1. The top chat's projection keeps a helper's main-chat copies when the main agent's own `done` arrives. They leave only when that helper's turn ends; the gateway then sends `ended` (sequenced in the top chat, so an incremental catch-up replays it).
  2. The browser keeps one server-clock estimate per connection: the largest of (`sent_at`, or `session_state.emitted_at_ms` — else `session_state.emitted_at` when the field is absent — plus the time elapsed since that frame arrived) over the frames it received. The retry countdown and the silence time use this estimate instead of the single frame's `sent_at`. This is the rule `ProviderRetryFrame.sent_at` already documents.
  3. On attach, a turn's provider notices are re-sent in the order they were last published (scoped to the `provider_retry:` / `provider_status:` items), so an older retry superseded by a later recovered line cannot reappear.
  4. **Non-incremental attach reconciles the named rows.** When an attach completes successfully as a full snapshot or as `live_only` (history unavailable, too-old cursor, or a gateway restart), the browser replaces that chat's named helper rows with exactly the rows the attach's projection delivered. Loaded history, the main agent's own notices and live frames released after the completion boundary are kept. A failed or superseded attach changes nothing. An incremental attach keeps relying on the replayed `ended`. No new snapshot field, stored tombstone or polling.
- Nothing new is stored. Retry, silence and recovered notices stay live only and are never saved to a transcript.

---

## User Stories & Acceptance Criteria

### User Story 1 — See a helper's model trouble in the helper's own chat (Priority: P1)

A user opens a helper's chat to see why it is taking long. Today that chat shows only "Waiting for response…" while the helper's model is rate-limited or silent. With this story, the helper's chat shows the same retry countdown, silence notice and recovered line that a normal chat shows for its own model.

**Why this priority**: the founder chose it, and the waiting happens in that chat. The user guide already promises it (`docs/troubleshooting.md`, retry section); today that promise is false.

**Independent Test**: scripted fake model; the helper's first model call gets HTTP 429 with a 10-second wait, the second gets 200. Open the helper's chat during the wait, before anything has streamed.

**Acceptance Scenarios**:

1. **Given** a helper's model call was refused with "too many requests" and a retry is scheduled, **When** the user views the helper's chat, **Then** the helper's chat shows the retry line with the provider, a countdown and "attempt 2 of N", even though nothing has been streamed in that chat yet.
2. **Given** a helper's model has sent nothing for 30 seconds, **When** the user views the helper's chat, **Then** the helper's chat shows the silence notice.
3. **Given** a helper's retried call succeeded, **When** the user views the helper's chat, **Then** the helper's chat shows the recovered line with the retry count.
4. **Given** the helper's model answers again after a silence notice, **When** the answer starts arriving, **Then** the silence notice disappears from the helper's chat.
5. **Given** a helper's model did not start answering in time, or ended with an empty reply, and a retry is scheduled, **When** the user views the helper's chat, **Then** the retry line shows the matching wording for that cause.

### User Story 2 — See a named line in the main chat, also when it is idle (Priority: P1)

The user stays in the main chat after the main agent has handed off the work and finished its reply. When a helper's model is rate-limited, slow to start, or silent, the main chat shows a short line that starts with the helper's name, so the user knows which agent is waiting and that nothing is broken.

**Why this priority**: this is the "nobody sees it" case from the probe, and the most common one.

**Independent Test**: same scripted 429-then-200 and 35-second-silence helper runs, with the main agent's reply already finished; watch the main chat only.

**Acceptance Scenarios**:

1. **Given** the main agent has finished its reply and its helper's model call is waiting on a retry, **When** the user looks at the main chat, **Then** a line starting with the helper's name shows the same retry wording and countdown the helper's chat shows.
2. **Given** the main agent is still writing its reply and its helper's model call is waiting on a retry, **When** the user looks at the main chat, **Then** the same named line is shown, as a separate line from the main agent's own notices.
3. **Given** a helper's model has been silent for 30 seconds while the main chat is idle, **When** the user looks at the main chat, **Then** a line starting with the helper's name shows the silence wording.
4. **Given** a named line for a helper is shown, **When** that helper's retried call succeeds, **Then** the line changes to the helper's named recovered line.
5. **Given** a named line for a helper is shown, **When** that helper's work ends (finished, failed or stopped), **Then** the named line disappears from the main chat.
6. **Given** a named silence line is shown, **When** the helper's model starts answering again, **Then** the named line disappears.
7. **Given** a named retry line is shown, **When** the helper's fallback model answers at once and the helper keeps working, **Then** the named line disappears.
8. **Given** a helper is waiting on a retry and the main agent finishes its reply during that wait, **When** the user reloads the main chat, **Then** the named line is back with the countdown measured from the server's current time.
9. **Given** a helper's model did not start answering in time, or ended with an empty reply, **When** the retry is scheduled, **Then** the main chat shows the named line with the matching wording.

### User Story 3 — A screen reader announces which agent is waiting (Priority: P2)

A user with a screen reader hears the helper's name with the notice, instead of a sentence that sounds like the main agent is stuck.

**Why this priority**: the same wording risk as User Story 2 for screen-reader users.

**Independent Test**: accessibility check on the main chat with named lines shown, plus one manual screen-reader pass.

**Acceptance Scenarios**:

1. **Given** a named line appears in the main chat, **When** a screen reader is running, **Then** it announces the change once, politely, starting with the helper's name.
2. **Given** a named retry line is counting down, **When** each second passes, **Then** nothing new is announced.
3. **Given** two helpers' named lines change state, **When** a screen reader is running, **Then** each change is announced once with its own helper's name.

### User Story 4 — Notices never overwrite each other (rider #1850) (Priority: P2)

The main agent and its helpers can all wait on a model at the same time. Each one's notice stays its own: a helper's "recovered" never clears the main agent's own retry countdown, and two helpers never replace each other's line.

**Why this priority**: today a helper notice in the main chat can wipe the main agent's own notice (Inferred from code; #1850). The named line would make this visible, so it ships together.

**Independent Test**: scripted run where the main agent's own call and two helpers' calls are rate-limited at overlapping times.

**Acceptance Scenarios**:

1. **Given** the main agent's own retry countdown is shown, **When** a helper's retried call succeeds, **Then** the main agent's own countdown is still shown, unchanged.
2. **Given** helper "Researcher" has a named retry line, **When** helper "Writer" gets a silence notice, **Then** both named lines are shown.
3. **Given** two helpers' named lines are shown, **When** one helper's work ends, **Then** only that helper's line disappears.

### Edge Cases

- **Helper name cannot be found** (the agent was deleted, or the agent list has not loaded or failed to load): the line starts with "Helper". Expected: never an agent id or a session id.
- **Same helper agent, two helpers at once** (two copies of one agent): each copy has its own line, both with the same name. Expected: two lines, matched by helper chat, not by name.
- **More than three helpers shown at once** (waiting or recovered): rows are ordered by the time of each helper turn's first notice, oldest first. The first three are shown; the rest are summed up in one summary line that counts them without saying they are waiting (exact wording: chief's copy review). When a shown row clears, the next one moves up. Order survives updates and reloads. Expected: never more than four rows.
- **Helper of a helper** (possible on a stock install: General Purpose, Planner and Jim may delegate, and every team member has a self-delegation edge of depth 10): the named line appears in the top chat (the chat the user started) and names the agent whose model is waiting. The grandchild's own chat shows its notices like any helper chat. The middle helper's chat gets no named line. The top chat's line leaves on `ended` when the grandchild's turn ends, because the top chat never receives the grandchild's `subagent_end`.
- **Main chat reloaded while a helper is still waiting**: the named line is shown within 2 seconds of the chat finishing loading, with the countdown measured from the server's current time.
- **Main chat disconnects briefly and the helper ends meanwhile**: on an incremental reconnect the catch-up replays `ended`; on a full or `live_only` reconnect the rows are reconciled with the delivered projection. Either way the ended helper's line is gone and a still-waiting helper's line stays.
- **Silence, then a retry is scheduled**: the silence clears and the new retry countdown stays (both chats, live and after reload).
- **Retry, recovered, tool call, then a new retry in the same turn**: the new retry is shown; it is not suppressed by the earlier recovered line.
- **Timeout or empty-reply retry, then the model answers promptly and the helper runs a long tool call**: the named line clears when the model answers.
- **Same helper chat, a later turn**: a late `ended` for the earlier turn does not clear the later turn's line.
- **Helper chat opened after the notice was sent**: same as a normal chat that attaches late; the current notice is shown with the right countdown.
- **Retry wait ends while the line is shown, and no new notice comes**: the countdown stops at 0:00 and the line stays until the next notice or the helper's end (same as the main agent's own retry line today).
- **The main agent's own retry and a helper's line at the same time**: both shown; the main agent's own line keeps its current position and wording, and named lines come below it.
- **Old browser tab without this change** (open across the upgrade): the tab's strict frame check drops the main-chat copy and re-syncs that chat once through the normal catch-up. It shows no named line; its own notices are unchanged; a reload gives the new behaviour. Expected: no error shown to the user, no duplicate messages.
- **Long helper name at 320 CSS px**: the line wraps; the composer and Stop stay usable.

---

## UI Screens and States

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Helper's chat, notice area above the composer | chat loading as today | no notice: nothing drawn | retry line (countdown, any of the four causes) or silence notice, shown while the helper's turn is active even before anything streamed | — | recovered line; notices clear when the model answers |
| Main chat, named helper rows (new; above the composer, below the main agent's own notices) | chat loading: rows appear within 2 s after load if a helper is still waiting | no affected helper: nothing drawn, no empty state | one row per affected helper: "{Name}: {retry or silence wording}" | more than 3 rows: three rows plus one summary row | "{Name}: {recovered wording}" until the helper ends or its model answers |
| Main chat, the main agent's own notices | unchanged (except the reload clock, FR-003) | unchanged | unchanged | unchanged | unchanged |

## User Journey

1. The user asks the main agent for real work. The main agent starts a helper and finishes its own reply (main chat idle).
2. The helper's model is rate-limited. Main chat: the row "Researcher: openrouter is rate-limiting requests right now. Retrying automatically in 0:10 (attempt 2 of 6)." appears (US2-1).
3. The user clicks into the helper's chat (Activity bar or sidebar). Helper chat: the same retry line without the name prefix (US1-1).
4. The retry works. Both chats show the recovered line; the main chat's row is named (US1-3, US2-4).
5. The helper finishes. The main chat's named row disappears (US2-5); the helper's report arrives in the main chat as today.

## Accessibility and Keyboard

- The named rows are text only: no new focusable control, no change to tab order, no dismiss control. Stop stays on the helper's own chat and in the Activity panel.
- **One owner for announcements:** the named-row area has exactly one polite live region. It holds only frozen state-change text that starts with the helper's name (for example on new retry, silence, recovered, cleared). Ticking countdown text stays outside that live region. When the retry and waiting indicators are rendered inside the named-row area, their own hidden live regions are not rendered; in their standalone use (each chat's own notices) they keep them unchanged.
- Each state change is announced once (US3). Countdown ticks are not announced.
- Focus ring rules are unchanged (no new focusable element).
- Reflow: each row wraps at 320 CSS px; no horizontal scroll.
- Verification: automated DOM/axe checks prove the live-region structure and text; one manual screen-reader pass (UAT-H9) is the only evidence for "announced once". The two are reported separately.

## Design-System Components

- Helper's chat: the existing `ProviderRetryIndicator`, `ProviderWaitingIndicator` and the existing recovered line, unchanged.
- Main chat named rows: the same two indicators with a name prefix and a switch to leave out their own live region (see Accessibility). The recovered wording is reused as text in the named row; no extra system message is added to the main chat. These components live in `src/components/chat/`, not the catalog, so no catalog entry is expected. Checked against `design-system/catalog.json`: no existing "named notice" component; no new public component is proposed.
- Colours, spacing and type are tokens only (design-system locks).

## Security and User Promises

- No tool access, approval or shell-rule change. No focus-area package is touched.
- The frames stay built from named fields only, never provider text. The new fields carry ids, never shown. The display name comes from the browser's own agent list, not from the wire.
- A helper's notice is published only to the helper's chat and the top chat. The steered-helper rule for **channels** is not changed.
- Review: the security seat of the feature's 5-reviewer gate at the cut; after fixes, only the reviewers whose findings were fixed re-review, scoped to those fixes (root `CLAUDE.md`, "Change sizes and the review gate").

---

## Behavioral Contract

Primary flows:
- When a helper's model call is retried (any of the four retry causes), the helper's chat shows the retry line, and the main chat shows the same line starting with the helper's name, whether or not the main agent is writing.
- When a helper's model has been silent for 30 seconds, the helper's chat shows the silence notice, and the main chat shows it named.
- When a helper's retried call succeeds, both chats show the recovered line (named in the main chat).
- When the helper's model answers again after a silence notice, or a fallback model answers at once, the notice clears in both chats.
- When a helper's work ends, its named row leaves the main chat.

Error flows:
- When the helper's name cannot be found, the row starts with "Helper".
- When the browser reloads or reconnects during a helper's wait, the current notice comes back in both chats, measured from the server's current time.
- When an old browser tab receives a main-chat copy, it drops it and re-syncs once.

Boundary conditions:
- At 29 seconds of helper silence no silence notice is shown; at 30 seconds it is (existing threshold, unchanged).
- With three rows, three rows; with four, three rows plus one summary row.
- The main agent's own live notices look and behave exactly as before.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not change the main agent's own retry, silence or recovered notices (wording, position, the "only while writing" rule) because the founder's decision covers helpers only. The one exception is the reload clock correction in FR-003.
- The system must not let a helper's notice replace, clear or restart the main agent's own notice or another helper's row (#1850).
- The system must not send a helper's notice to chat channels (Telegram and similar) because #1851 is a separate open question.
- The system must not save retry, silence or recovered notices to any transcript.
- The system must not copy the fallback-model note into the main chat.
- The system must not put provider error text, agent ids or session ids into any visible row.
- The system must not add a dismiss button or a Stop button to the named rows.
- The system must not weaken the strict frame validation to make old tabs accept the new fields.
- The system must not add a polling loop, a stored notice log or a feature flag for this feature, nor any snapshot field other than the additive `session_state.emitted_at_ms` (Correction 2026-11-16, chief ruling Q1 A: an additive millisecond field replaces the whole-second clock sample; `emitted_at` is unchanged.)

### Machine-Verifiable Constraints

**Wire**:
- A turn gets a main-chat copy exactly when its own chat differs from its top chat.
- The helper copy has `session_id` = the helper's chat and no child fields. The main-chat copy has `session_id` = the top chat, `child_session_id` = the helper's chat, `agent_id` = the helper's agent; every other field is byte-equal to the helper copy except `seq`.
- A frame about a chat's own turn never carries `child_session_id` or `agent_id`.
- When a helper turn that left a main-chat copy ends, the top chat receives exactly one `provider_status` with `state: ended`, the helper's `turn_id`, `child_session_id` and `agent_id`.
- `ended` is never published without `child_session_id`. `child_session_id` and `agent_id` are both present or both absent.
- Both copies validate against the generated schemas.

**Timing**: the named row is visible within 2 s of the main-chat copy arriving; after a reload during the wait, within 2 s of the chat finishing loading. After a reload during the wait, the countdown reads the true remaining time at the moment the countdown is painted, ±1 s (for example, after a reload at second 5 of a 30-second wait that paints 1 s after the reload, 0:23–0:25); the 2 s display budget above is measured separately, from the reload to that paint. (Correction 2026-11-16, chief ruling T9 A: the earlier fixed range 0:24–0:26 ignored the load time the same sentence allows.)

**Count**: at most 4 named-area rows (3 rows plus 1 summary) at any time.

**Scope**: no change to `ProviderFallbackFrame`, channel messages or transcripts.

## Integration Boundaries

### Model provider (fake in tests)

- **Data in / out**: the helper's model calls; HTTP 429 with Retry-After, a delayed first byte, an empty stream, or a silent stream; a second candidate for the fallback case.
- **Contract**: OpenAI-compatible chat completions.
- **On failure**: the existing retry plan, timeout retry and silence watch; this spec changes only where the notices go and how they clear.
- **Development**: scripted fake model (`/workspace/logs/1537/recipe/`, `provider.cjs`), extended with the no-response, empty-reply and two-candidate scripts. Fake-model rows prove deterministic routing and rendering, not real-provider behaviour.

### Gateway to browser (WebSocket)

- **Data in / out**: `provider_retry` / `provider_status`, two copies for a helper turn, plus `ended`.
- **Contract**: `contracts/asyncapi.yaml` and the three schemas above.
- **On failure**: a dropped socket recovers through the existing per-session catch-up; each copy is sequenced in its own chat, so `ended` is replayed on incremental reconnect.
- **Development**: real gateway, isolated instance (`--allow-empty --sandbox=off`), never the live instance.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|------------------|------------------------|---------------------|
| 1 | Wire shape | — | Resolved by the architect (2026-11-15): two optional fields plus `ended`. |
| 2 | Reload during a helper's wait | — | Resolved by the architect (2026-11-15): three fixes under "API and Data". Amended by chief ruling Q1 A (2026-11-16): one additive snapshot field, `session_state.emitted_at_ms`, because the whole-second `emitted_at` cannot hold FR-008's ±1 s. |
| 3 | Exact wording of the name prefix, the "Helper" fallback and the summary row | — | Resolved by the chief's voice review (2026-11-15 16:19Z): prefix "{Helper name}: " + the helper chat's own sentence, unchanged; unknown name "Helper: "; summary "1 more helper has a model notice" / "{N} more helpers have model notices" (never "waiting"); the announcement is the same sentence as the visible line with the time frozen at notice start, recovered e.g. "Researcher: Connection came back — the model answered after 1 retry." |
| 4 | Helper of a helper | — | Resolved by the architect (2026-11-15): reachable today; top chat only; middle chat unchanged; cleared by `ended`. Revisit the middle chat only if UAT-H7 shows confusion. |
| 5 | Whether a helper's own chat counts as "streaming" while its turn waits on the model before the first byte | The developer reuses the streaming gate and the helper chat still shows nothing | RED must cover "helper chat, helper turn active, nothing streamed yet" (order 6b); GREEN makes the notice show whenever that chat's turn is active. |

---

## BDD Scenarios

### Feature: Helper model notices

#### Scenario: Helper's chat shows the retry line after a 429, before anything streamed

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the fake model answers the helper's first call with HTTP 429 and `Retry-After: 10`, and its second call with 200
- **And** nothing has been streamed in the helper's chat yet
- **When** the user opens the helper's chat during the wait
- **Then** the helper's chat shows the retry line with the provider's name, a countdown of at most 0:10, and "attempt 2 of" the configured total
- **But** the line does not start with a helper name

#### Scenario Outline: Helper's chat shows the timeout and empty-reply retries

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** the fake model `<misbehaves>` on the helper's first call and answers the second
- **When** the retry is scheduled
- **Then** the helper's chat shows the retry line with the `<cause>` wording

| misbehaves | cause |
|---|---|
| sends no first byte within the request timeout | no response |
| closes the stream without sending anything | empty reply |

#### Scenario: Helper's chat shows the silence notice at 30 seconds

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the fake model sends nothing on the helper's call for 35 seconds, then answers
- **When** 30 seconds of silence have passed
- **Then** the helper's chat shows the silence notice

#### Scenario: Helper's chat shows the recovered line

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the 429-then-200 helper script
- **When** the second call succeeds
- **Then** the helper's chat shows the recovered line saying 1 retry

#### Scenario: Silence notice clears in the helper's chat when the model answers

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** the helper's chat shows the silence notice
- **When** the fake model starts streaming the answer
- **Then** the silence notice is gone from the helper's chat

#### Scenario Outline: Named retry row in the main chat, idle or writing

**Traces to**: User Story 2, Acceptance Scenarios 1 and 2
**Category**: Happy Path

- **Given** the main agent is `<main state>` and its helper "Researcher" gets the 429-then-200 script
- **When** the retry is scheduled
- **Then** the main chat shows a row starting with "Researcher" followed by the same retry wording and countdown the helper's chat shows
- **And** the main agent's own notice area is empty

| main state |
|---|
| finished its reply (idle) |
| still writing (fake model streams slowly for 60 s) |

#### Scenario Outline: Named row for the timeout and empty-reply retries

**Traces to**: User Story 2, Acceptance Scenario 9
**Category**: Alternate Path

- **Given** the main chat is idle and Researcher's model `<misbehaves>` on its first call
- **When** the retry is scheduled
- **Then** the main chat shows a row starting with "Researcher" followed by the `<cause>` retry wording

| misbehaves | cause |
|---|---|
| sends no first byte within the request timeout | no response |
| closes the stream without sending anything | empty reply |

#### Scenario: Named silence row in an idle main chat

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the main agent finished its reply and helper "Researcher" gets the 35-second silence script
- **When** 30 seconds of silence have passed
- **Then** the main chat shows a row starting with "Researcher" followed by the silence wording

#### Scenario: Named row turns into the recovered row

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the main chat shows Researcher's named retry row
- **When** Researcher's second call succeeds
- **Then** the main chat shows a row starting with "Researcher" followed by the recovered wording with 1 retry
- **And** no extra unnamed system message appears in the main chat

#### Scenario Outline: Named row leaves when the helper ends

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** the main chat shows Researcher's named row
- **When** Researcher's work `<ends how>`
- **Then** Researcher's named row is gone from the main chat

| ends how |
|---|
| finishes |
| fails (fake model returns 500 on every call until the retries run out) |
| is stopped by the user |

#### Scenario: Named silence row clears when the helper's model answers

**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** the main chat shows Researcher's named silence row
- **When** the fake model starts streaming Researcher's answer
- **Then** the named row is gone from the main chat

#### Scenario: Named retry row clears when a fallback model answers at once

**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Edge Case

- **Given** Researcher's first model gets 429 with `Retry-After: 2` on all three of its allowed calls and its fallback model answers on its first call with a tool request
- **And** the fake model's request log shows three calls to the first model and one to the fallback (positive control)
- **And** Researcher then runs a 30-second tool call
- **When** the fallback model's answer arrives
- **Then** Researcher's named row is gone from the main chat while Researcher is still working
- **But** no fallback note appears in the main chat

#### Scenario: Reload after the main agent finished during the wait

**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Error Path

- **Given** Researcher's named retry row is shown with `Retry-After: 30` while the main agent is still writing
- **And** the main agent finishes its reply at second 3
- **When** the user reloads the main chat at second 5
- **Then** within 2 s of the chat loading, Researcher's named row is back with a countdown showing the true remaining time at the moment the countdown is painted, ±1 s

#### Scenario: Reload of the helper's chat during the wait

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Error Path

- **Given** the helper's chat shows the retry line with `Retry-After: 30`
- **When** the user reloads the helper's chat at second 5
- **Then** within 2 s of loading the retry line is back with a countdown showing the true remaining time at the moment the countdown is painted, ±1 s

#### Scenario: Stale retry does not come back after recovery on reload

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Edge Case

- **Given** Researcher's turn had a silence notice, then a retry, then a recovered status, and Researcher is still working
- **When** the user reloads the main chat
- **Then** Researcher's row shows the recovered wording
- **But** no retry countdown for Researcher is shown

#### Scenario Outline: Missed end during a disconnect

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Error Path

- **Given** the main chat shows named rows for Researcher and Writer
- **And** the main chat's connection drops
- **When** Researcher finishes while Writer still waits, and the connection comes back with a `<catch-up kind>` attach
- **Then** Researcher's named row is gone after the catch-up
- **And** Writer's named row is still shown

| catch-up kind |
|---|
| incremental (journal replays `ended`) |
| full snapshot (cursor too old) |
| live_only (history unavailable) |

#### Scenario: Missed end across a gateway restart

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Error Path

Correction 2026-11-16 (chief ruling U1 (a), 00:57Z). This row used to be "full snapshot after a gateway restart" in the outline above, and it expected Writer's row to stay. That cannot hold. Notices live only in memory ("Nothing new is stored"), so a restart loses them. Boot also stops the interrupted helper turn and resumes it as a new turn. Evidence: run green-e2e-3, /workspace/logs/1537/build/green-fe/e2e-green-e2e-3.log (frames: `session_snapshot` reason `boot_mismatch` with no provider notices; the fake model's new Writer request in the same second).

- **Given** the main chat shows named rows for Researcher and Writer
- **And** the main chat's connection drops
- **When** Researcher finishes while Writer still waits, the gateway restarts, and the connection comes back with a full snapshot
- **Then** after the catch-up Researcher's named row is gone, and Writer has no named row yet (its interrupted turn resumes as a new turn)
- **And** Writer's named row returns when the resumed turn reaches its next notice: the silence notice after 30 s of no answer, or a retry, whichever comes first
- **But** if the resumed turn never reaches a notice, Writer's row must not reappear

#### Scenario: Silence clear does not erase the new retry

**Traces to**: User Story 2, Acceptance Scenarios 1 and 3
**Category**: Edge Case

- **Given** Researcher's named silence row is shown after 30 s of silence
- **When** Researcher's call then fails with 429 (`Retry-After: 20`) and a retry is scheduled
- **Then** the main chat shows Researcher's named retry countdown, and the helper's chat shows the same countdown
- **And** after a reload 5 s later both still show the countdown, each reading the true remaining time at the moment the countdown is painted, ±1 s, painted within 2 s of loading

#### Scenario: A later retry in the same turn is shown

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Edge Case

- **Given** Researcher's turn had a retry, then a recovered status, then a tool call
- **When** Researcher's next model call in the same turn gets 429 and a retry is scheduled
- **Then** Researcher's named row shows the new retry countdown, live and after a reload

#### Scenario Outline: Named row clears after a timeout or empty-reply retry succeeds

**Traces to**: User Story 2, Acceptance Scenarios 7 and 9
**Category**: Alternate Path

- **Given** Researcher's first call `<misbehaves>` and the next call answers promptly with a tool request
- **And** Researcher then runs a 30-second tool call
- **When** the model's answer arrives
- **Then** Researcher's named row is gone from the main chat while Researcher is still working, also after a reload during the tool call

| misbehaves |
|---|
| sends no first byte within the request timeout |
| closes the stream without sending anything |

#### Scenario: A late end of an earlier turn does not clear a new wait

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case

- **Given** Researcher's chat has a second turn whose model is waiting on a retry, with a named row in the main chat
- **When** an `ended` frame for Researcher's first turn arrives
- **Then** the named row for the second turn is still shown

#### Scenario: Screen reader hears each helper's name once

**Traces to**: User Story 3, Acceptance Scenarios 1, 2 and 3
**Category**: Happy Path

- **Given** the main chat is idle
- **When** Researcher's named retry row and then Writer's named silence row appear
- **Then** the single polite live region receives one text starting with "Researcher" and one starting with "Writer"
- **And** its text does not change on countdown ticks
- **And** no other live region inside the named-row area exists

#### Scenario: Helper's recovered line does not clear the main agent's own countdown

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Edge Case

- **Given** the main agent's own call got 429 with `Retry-After: 20` and its countdown is shown
- **When** helper Researcher's retried call succeeds at second 5
- **Then** the main agent's own countdown is still shown, still counting down from the same retry time
- **And** Researcher's named recovered row is shown below it

#### Scenario: Two helpers keep their own rows

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Edge Case

- **Given** Researcher has a named retry row
- **When** Writer reaches 30 seconds of silence
- **Then** the main chat shows both "Researcher …" and "Writer …" rows

#### Scenario: One helper ending leaves the other's row

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Edge Case

- **Given** Researcher and Writer both have named rows
- **When** Writer finishes
- **Then** only Researcher's row is shown

#### Scenario: Unknown helper name

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Error Path

- **Given** the helper's agent id is not in the browser's agent list (deleted, not loaded, or the list failed to load)
- **When** its named retry row appears
- **Then** the row starts with "Helper"
- **But** it shows no agent id and no session id

#### Scenario Outline: More than three rows

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Edge Case

- **Given** `<rows>` helper rows exist, in order of their first notice
- **When** the main chat renders the named-row area
- **Then** the first three rows by first-notice time are shown and the summary row counts `<summary count>`

| rows | summary count |
|---|---|
| 3 waiting | no summary row |
| 4 waiting | 1 |
| 4 recovered and still working | 1 (the summary does not say "waiting") |
| 2 waiting + 2 recovered | 1 |

#### Scenario: Row promotion when a shown row clears

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Edge Case

- **Given** four helper rows exist and the fourth is in the summary
- **When** the first helper finishes
- **Then** the fourth helper's row is shown as the third row and the summary row is gone

#### Scenario: Grandchild's notice reaches the top chat and leaves on its turn end

**Traces to**: User Story 2, Acceptance Scenarios 1 and 5
**Category**: Alternate Path

- **Given** the top chat's General Purpose delegated to itself, and that child delegated once more
- **And** the grandchild's first call gets 429 (`Retry-After: 10`), then 200
- **When** the grandchild finishes while the middle helper is still running
- **Then** the top chat showed a named row for the grandchild, then its recovered row, and the row is gone now
- **And** the middle helper's chat never showed a named row

#### Scenario: Notices are not saved

**Traces to**: User Story 1, Acceptance Scenario 3; User Story 2, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a completed run in which Researcher had a retry, a silence notice and a recovered status, and a fallback note was saved in Researcher's chat
- **When** the user opens the main chat and Researcher's chat fresh after all turns ended
- **Then** neither chat shows any retry, silence or recovered notice
- **And** both chats show their normal saved messages, and Researcher's chat still shows the saved fallback note

#### Scenario: Channels stay unchanged

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Edge Case

- **Given** the main chat is a channel chat (fake channel) and its own model call is retried
- **And** a helper's model call is also retried
- **When** both retries are scheduled
- **Then** the channel receives one "Retrying — please wait..." message for the main agent's own call
- **But** none for the helper's call

#### Scenario: Old tab drops the main-chat copy and re-syncs

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Error Path

- **Given** a browser that validates frames with the previous generated schemas
- **When** it receives a main-chat copy with child fields
- **Then** it drops that frame, re-attaches the chat once, shows no named row and no error, and its own notices are unchanged

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit (Go) | `pkg/agent` payloads, `pkg/gateway` copy, `ended`, projection | Two copies for every retry producer and status; `ended` on helper turn end; projection keeps helper copies across root `done`; last-publish replay order |
| Unit (SPA) | `provider-frames.ts`, server clock, named-row area | Per-helper rows; own slots untouched; shown without streaming; clearing rules; clock; overflow; live region |
| Contract | schema validation and generation | Both copies validate; both-or-neither rule; `ended` only with child fields; generated types in sync |
| E2E (stubs shard) | real gateway + SPA + scripted fake model | US1–US4 in a real browser, no paid key |
| Hands-on UAT | tester and independent validator on the exact cut commit | The probe re-run on the fixed build, plus a manual screen-reader pass |

### Hands-on UAT rows (written at spec time)

Setup for every row: the scripted fake-model instance from `/workspace/logs/1537/recipe/RECIPE.md` (isolated gateway, `sk-test-placeholder`, two agents in `core_team`, delegation edge), the build under test stamped with its commit, and Chromium 1228 on a runner. The tester and the validator run on different models.

| Row | Acceptance criterion | Steps | Expected result | Affected area | Validator critical path | Automation candidate |
|---|---|---|---|---|---|---|
| UAT-H1 | US1-1, US1-3 | Main agent delegates; helper gets 429 (Retry-After 10) then 200; open the helper's chat during the wait, before anything streamed | Helper chat: retry line, then the recovered line; frame log shows `session_id` = helper, no child fields | backend addressing, helper chat | Re-drive; frame log plus helper-chat screenshot | yes |
| UAT-H2 | US2-1, US2-4, US2-5 | Same script; main agent finishes its reply first; stay in the main chat | Named row with countdown, then named recovered row, then gone after the helper finishes | main chat named rows | Screenshots at wait, recovered, end | yes |
| UAT-H3 | US2-2 | Same script; main agent's own model streams slowly for 60 s | Named row shown while the main agent writes; the main agent's own notice area empty | main chat while writing | Screenshot + frame log | yes |
| UAT-H4 | US1-2, US2-3, US2-6 | Helper silent 35 s then answers; main chat idle | Silence notice in the helper chat; named silence row in the main chat at ≥30 s; both clear on the answer | silence watch | Screenshots at 29 s and 31 s | yes |
| UAT-H5 | US4-1 | Main agent's own call 429 (Retry-After 20); helper recovers at second 5 | Main agent's own countdown unchanged; named recovered row below it | #1850 | Screenshot + DOM check of both | yes |
| UAT-H6 | US4-2, US4-3 | Two helpers: one 429, one silent; then one finishes | Two named rows; one removed on finish | #1850 | Screenshot sequence | yes |
| UAT-H7 | US2-1, US2-5 (helper of a helper) | Top chat asks General Purpose to delegate to itself, and the child to delegate once more; the grandchild's first call gets 429 (Retry-After 10), then 200; the grandchild finishes while the middle helper is still running | Top chat: a named row for the grandchild, then the recovered row, then gone when the grandchild's turn ends. Middle chat: no named row. Grandchild chat: retry and recovered lines | nested delegation | Screenshots of top, middle and grandchild chats | yes |
| UAT-H8 | US2-8 | Retry-After 30 while the main agent still writes; main agent finishes at second 3; reload the main chat at second 5 | Named row back within 2 s of loading; countdown shows the true remaining time at the moment the countdown is painted, ±1 s (about 0:23–0:25 for a 1 s load) | projection, clock | Screenshot after reload | yes |
| UAT-H9 | US3 | Real screen reader (Orca or VoiceOver) on UAT-H2 and UAT-H6's main chat | Each state change announced once with the helper's name; no tick announcements | accessibility | Recorded announcement log | no: needs a human screen-reader pass |
| UAT-H10 | US2-7 | Helper's first model gets 429 on all three calls (Retry-After 2), its fallback answers at once with a tool request, then a 30 s tool call; check the fake model's request log (3 + 1 calls) | Named row gone while the helper still works; no fallback note in the main chat | fallback clearing | Screenshot during the tool call | yes |
| UAT-H11 | US1-5, US2-9 | Helper's first call: no first byte within the request timeout; then (second run) an empty stream; each time the next call answers with a tool request and a 30 s tool call follows | Retry lines with the no-response / empty-reply wording in both chats; the main chat's named row clears when the model answers, while the tool still runs | #1664 retries | Frame log + screenshots | yes |
| UAT-H12 | Missed end (full and live_only catch-up, and gateway restart) | Two helpers waiting; cut the main chat's network; one helper finishes; restore the network after the journal is gone. Repeat with a gateway restart instead | Without a restart: the finished helper's row is gone and the waiting helper's row is kept. With a restart: the finished helper's row is gone, the waiting helper has no row right after reconnect, and its row returns at its resumed turn's next notice (silence after 30 s, or a retry). Correction 2026-11-16, chief ruling U1 (a) | catch-up reconciliation | Screenshot after reconnect | yes |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `TestHelperProviderRetryPublishesToHelperAndNamedRootCopy` (placeholder) | Unit (Go) | Named retry row (outline); Helper's chat retry | Two copies, right ids, byte-equal except the listed fields |
| 1a | `TestHelperTimeoutAndEmptyRetryPublishBothCopies` (placeholder) | Unit (Go) | Named row for timeout and empty reply; Helper's chat timeout/empty | The #1664 producers get the same two copies |
| 1b | `TestHelperTurnEndPublishesEndedToTopChat` (placeholder) | Unit (Go) | Named row leaves (outline); Missed end; Late end of an earlier turn; Grandchild | One `ended` per helper turn with a main-chat copy, sequenced in the top chat |
| 1c | `TestProjectionKeepsHelperCopyAcrossRootDone` (placeholder) | Unit (Go) | Reload after the main agent finished | Root `done` leaves a helper copy; the helper's turn end removes it; main Stop and new main turn likewise |
| 1d | `TestProjectionReplaysProviderItemsInLastPublishOrder` (placeholder) | Unit (Go) | Stale retry does not come back; A later retry in the same turn is shown | Permutations of waiting / retry / recovered, plus retry → recovered → tool → new retry under one turn id; replayed final state equals live state; other item kinds keep their order |
| 1e | `TestHelperAnswerWithoutRecoveredSendsRespondingCopy` (placeholder) | Unit (Go) | Named retry row clears on fallback; Named row clears after a timeout or empty-reply retry succeeds | Main-chat `responding` copy on first-call fallback success and on outer timeout/empty retry success; no invented count; no fallback note in the top chat |
| 1f | `TestSilenceClearPublishedBeforeNewRetry` (placeholder) | Unit (Go, real producer to gateway) | Silence clear does not erase the new retry | waiting → retry scheduled: `responding` precedes `provider_retry` in both chats; projected replay keeps the countdown |
| 2 | `TestHelperProviderStatusPublishesToHelperAndNamedRootCopy` (placeholder) | Unit (Go) | Named silence row; silence clears; recovered | waiting / responding / recovered |
| 3 | `TestOwnTurnProviderFramesCarryNoChildFields` (placeholder) | Unit (Go) | Recovered does not clear own countdown | Regression control: root's own frames unchanged (may pass on the release head) |
| 3a | `TestHelperNoticesAreNotPersisted` (placeholder) | Unit (Go) | Notices are not saved | No transcript write for any notice; fallback note still saved (positive control) |
| 4 | contract fixture tests (placeholder) | Contract | Old tab drops the copy; all wire scenarios | Both copies validate; both-or-neither; `ended` only with child fields; old schema rejects the copy |
| 5 | `provider-frames.helper-rows.test.ts` (placeholder) | Unit (SPA) | Two helpers; one ending; recovered does not clear own; late end; fallback clearing | Per-helper-turn keying; own slots untouched |
| 5a | `catchup.helper-rows-reconcile.test.ts` (placeholder) | Unit (SPA) | Missed end (outline) | Full snapshot, restart and live_only attach replace named rows with the delivered projection; ended row leaves, active row stays; failed/superseded attach changes nothing |
| 5b | `provider-clock.reattach.test.ts` (placeholder) | Unit (SPA) | Reload after the main agent finished; Reload of the helper's chat | A re-sent retry with `sent_at` 5 s before `emitted_at` counts down from 25 s; same for silence time; own notices too |
| 6 | `ChatScreen.helper-named-rows.test.tsx` (placeholder) | Unit (SPA) | Named row idle/writing; unknown name; more than three; promotion; screen reader | Render without streaming; prefix; "Helper" fallback; overflow; one live region |
| 6b | `ChatScreen.helper-chat-before-first-byte.test.tsx` (placeholder) | Unit (SPA) | Helper's chat retry before anything streamed | Notice shows while the chat's turn is active and nothing streamed |
| 7 | `tests/e2e/helper-model-notices.spec.ts` (placeholder) | E2E (stubs shard) | all scenarios except the channel and old-tab ones | Scripted fake model, real gateway, both chat states, reload, fallback, #1664 causes, helper of a helper |
| 7a | channel containment test (placeholder; extends the existing BoundaryRetryNotice test) | Unit (Go) | Channels stay unchanged | Root-channel positive control plus no helper message |

RED rule: new-behaviour tests fail on the release head with a saved log. Regression controls (orders 3, 4's own-frame cases, 7a's positive control) are expected to pass on the release head and are never weakened to make them fail.

### Gates and where they run

- Author checks before each push (own-tests rule, local limits, `heavy`): Go build of the touched packages, linter on the touched packages, the targeted Go tests (`-p 1 -tags=goolm,stdjson`), `npm run typecheck`, eslint on touched files, the targeted Vitest files, `scripts/gen-contracts.sh` plus `make verify-contracts`, `npm run lint:design-system-locks`, and the e2e spec 3 runs in a row with retries=0 on a runner.
- At the cut: the single full CI run, the feature's 5-reviewer gate, and hands-on UAT (tester plus independent validator) on the exact cut commit, because this is agent-engine and chat-surface code.
- CHECK: an independent auditor on a different model re-runs the tests and runs mutants (at least: copy sent to the top chat only; named row gated on streaming; per-helper key replaced by one slot; root `done` clears helper copies; `ended` not sent; replay in first-created order; clock uses the frame's `sent_at`; helper turn end never emitted).

### Test Datasets

#### Dataset: Fake-model scripts for the helper's call

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 429, Retry-After 10, then 200 | happy | retry line then recovered (1 retry) in both chats | Named retry row; recovered | probe run-03 shape |
| 2 | 429, Retry-After 1, then 200 | min wait | countdown starts at 0:01; recovered | Named retry row | smallest wait the frame allows |
| 3 | 429, Retry-After 120, then 200 | at the cap | countdown starts at 2:00; recovered | Named retry row | largest wait the retry policy honours |
| 3b | 429, Retry-After 300 on the helper's first model; its fallback model answers 200 | over the cap | no retry is scheduled and no retry line or named retry row appears in either chat; the fallback model answers at once (fake-model request log: 1 + 1 calls) | Named retry row (absent) | the unchanged retry policy skips a wait above 120 s and moves to the next model; it is not capped |

Correction 2026-11-15 (chief, Q1 A, 20:28Z): dataset row 3 used to expect `Retry-After: 300` to show a countdown capped at 2:00. That was wrong: the unchanged retry policy (`pkg/providers/retry_plan.go::FallbackChain.inPlaceRetry`, the A-4 ceiling) skips any wait above 120 s before scheduling or announcing a retry. Row 3 now tests exactly 120 s, and row 3b tests the over-the-cap skip. The retry policy is not changed by this feature.
| 4 | silent 29 s, then answer | below threshold | no silence notice anywhere | Named silence row | boundary |
| 5 | silent 35 s, then answer | above threshold | silence notice at ≥30 s in both chats, cleared on answer | Named silence row; clears | |
| 6 | 500 on every call | error | helper fails; named row removed when the helper ends | Named row leaves (fails) | |
| 7 | 429 (Retry-After 60), user stops helper at second 5 | cancel | named row removed | Named row leaves (stopped) | |
| 8 | no first byte within the request timeout, then 200 | #1664 | no-response retry wording in both chats | Timeout outlines | |
| 9 | empty stream, then 200 | #1664 | empty-reply retry wording in both chats | Timeout outlines | |
| 10 | first model 429 ×3 (Retry-After 2), fallback 200 with a tool request at once, then 30 s tool call | fallback | request log 3 + 1; named row cleared during the tool call; no fallback note in the main chat | Fallback clearing | |
| 12 | silent 31 s, then 429 (Retry-After 20), then 200 | silence then retry | silence cleared, retry countdown kept live and after reload | Silence clear does not erase the new retry | producer order |
| 13 | no first byte, then a prompt tool request, then 30 s tool | timeout success | named row cleared on answer, no recovered count invented | Named row clears after a timeout retry succeeds | |
| 14 | 429 → 200 (tool) → 429 → 200 in one turn | same-turn retry | second retry shown live and after reload | A later retry in the same turn is shown | |
| 11 | Retry-After 30, main agent done at s3, reload at s5 | reload | painted within 2 s of loading; countdown = the true remaining time at the moment the countdown is painted, ±1 s | Reload after the main agent finished | |

#### Dataset: Main chat composition

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 0 helpers affected | empty | nothing drawn | Named retry row (idle) | |
| 2 | 1 helper | single | 1 named row | Named retry row | |
| 3 | 3 helpers waiting | at the cap | 3 rows, no summary | More than three rows | |
| 4 | 4 helpers waiting | over the cap | 3 rows + summary counting 1 | More than three rows | |
| 5 | 4 helpers recovered, still working | over the cap, mixed meaning | 3 rows + summary counting 1, not saying "waiting" | More than three rows | |
| 6 | 2 waiting + 2 recovered | mixed | 3 oldest rows + summary counting 1 | More than three rows | |
| 7 | main agent's own retry + 1 helper recovered | overlap | own countdown unchanged + 1 named row | Recovered does not clear own | #1850 |
| 8 | helper agent missing / list not loaded / list failed | error | row starts with "Helper" | Unknown helper name | |
| 9 | two copies of the same agent | duplicate name | 2 rows, same name, independent | Two helpers keep their own rows | keyed by helper turn |
| 10 | 4 rows, first helper finishes | promotion | fourth row moves up, summary gone | Row promotion | |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Main agent's own retry countdown shown only while it writes | ChatScreen provider-retry tests (#1280/#1664) | Yes: orders 3 and 5 assert the own slot is unchanged | must stay green unmodified; a test asserting the old wrong reload countdown is raised as a finding, not edited |
| Main agent's own silence notice at 30 s | provider_status tests (#1478) | No | must stay green unmodified |
| #1664 retries become retry frames | `pkg/gateway/provider_status_1664_test.go` (B5 tests) | No | must stay green; order 1a extends the helper case |
| Fallback note goes to the helper's chat and is saved | `pkg/agent` fallback note tests | No | unchanged; positive control in order 3a |
| Channel retry notice suppressed for steered helpers | `pkg/agent/steer_boundary_containment_test.go` (BoundaryRetryNotice) | Yes: order 7a adds the root-channel positive control | #1851 out of scope |
| Snapshot attach order for tool cards, media and errors | gateway projection tests | No | must stay green unmodified; order 1d checks the other kinds keep their order |
| Closed set of `routingSessionID` readers (names `providerRetryObserver` and `emitProviderStatus`) | `pkg/agent/routing_session_id_consumer_set_adr057_test.go::TestRoutingSessionID_ConsumerSetIsClosed` | No new test; run it as an affected regression | keep it unchanged where possible; if a reader legitimately moves or is added, update only that entry with its reason and keep the closed-set and positive-control assertions; never delete, skip or broaden it |

---

## Functional Requirements

- **FR-001**: System MUST publish a helper turn's `provider_retry` and `provider_status` frames, for all four retry causes, to the helper's own chat without `child_session_id` / `agent_id`.
- **FR-002**: System MUST also publish a copy of each such frame to the top chat, with `child_session_id` and `agent_id` set and all other fields equal except `seq`.
- **FR-003**: System MUST NOT set `child_session_id` / `agent_id` on frames about a chat's own turn, and MUST NOT change how those frames are shown live. Exception: after a reload or reconnect, the main agent's own countdown and silence time are measured with the server-clock estimate of FR-008.
- **FR-004**: The helper's chat MUST show the retry line, silence notice and recovered line for the helper's model calls, with the same rules a chat uses for its own model calls, also while nothing has been streamed in that chat yet.
- **FR-005**: The main chat MUST show one row per affected helper turn, starting with the helper's display name (or "Helper" when the name is unknown, never an id), followed by the same wording the helper's chat shows, whether or not the main agent is writing.
- **FR-006**: A named row MUST update on the same helper turn's next notice (including a later retry in the same turn); clear when that helper's model answers after a silence notice, or answers after an announced retry without a `recovered` frame (first-call fallback success, or a successful outer `no_response` / `empty_reply` retry); and disappear when that helper turn ends (finished, failed or stopped), including when the end happened while the main chat was disconnected, whatever kind of catch-up follows.
- **FR-013**: When a retry is scheduled while a silence notice is active, the silence clear MUST be published before the new retry, so the new countdown survives in both chats, live and on replay.
- **FR-014**: A successfully completed full-snapshot or `live_only` attach MUST replace the chat's named helper rows with exactly those in the delivered projection; a failed or superseded attach MUST change nothing.
- **FR-007**: A helper's notice MUST NOT replace, clear or restart the main agent's own notice or another helper turn's row, and an `ended` for one turn MUST NOT clear another turn's row.
- **FR-008**: After a reload or reconnect during a helper's wait — including after the main agent finished its reply — both chats MUST show the current notice within 2 s of loading, with the countdown and silence time measured against the server's current time (`session_state.emitted_at_ms`, else `session_state.emitted_at` when the field is absent), not the notice's original send time. The countdown digits show the remaining time rounded to the nearest second, and at least 0:01 while any time remains, so the retry line with its cause sentence is shown for every wait, however short; "Retrying now…" appears exactly when the retry time is reached, measured in milliseconds, never before. (Correction 2026-11-16, chief ruling Q8 A: the earlier "can appear up to 0.5 s before the retry time" let a 500 ms wait skip the cause sentence entirely.) An older retry superseded by a later recovered line MUST NOT reappear; a later retry in the same turn remains current. The only new snapshot field is the additive `session_state.emitted_at_ms` (Correction 2026-11-16, chief ruling Q1 A: an additive millisecond field replaces the whole-second clock sample; `emitted_at` is unchanged.)
- **FR-009**: The main chat MUST show at most three named rows, ordered by each helper turn's first notice, plus one summary row counting the rest without calling them waiting; a cleared row is replaced by the next one.
- **FR-010**: The named-row area MUST have exactly one polite live region, whose text starts with the helper's name, changes only on a state change, and never on countdown ticks.
- **FR-011**: System MUST NOT save retry, silence or recovered notices to any transcript, MUST NOT copy the fallback note to the main chat, and MUST NOT change channel retry messages.
- **FR-012**: When a helper turn that left a main-chat copy ends, the top chat MUST receive exactly one `provider_status` `ended` for that turn.

## Success Criteria

- **SC-001**: In the scripted 429-then-200 run with the main chat idle, the main chat shows a named row within 2 s of the retry being scheduled (today: nothing).
- **SC-002**: In the same run, the helper's chat shows the retry line (today: only "Waiting for response…").
- **SC-003**: The existing own-notice tests for #1280, #1478 and #1664 pass unmodified.
- **SC-004**: In the overlap run (UAT-H5), the main agent's own countdown is still shown 1 s after the helper's recovered frame.
- **SC-005**: After a reload at second 5 of a 30-second helper wait (main agent already finished), the named countdown is painted within 2 s of loading and reads the true remaining time at the moment the countdown is painted, ±1 s.
- **SC-006**: The e2e spec passes 3 runs in a row with retries=0 in its stubs shard.

---

## Reachability

- **Tool registration**: none, no new tool.
- **Screen**: the chat screen (`src/components/chat/ChatScreen.tsx`), in two places: the helper's chat (any helper chat opened from the Activity bar/panel or the sidebar) and the main chat's notice area above the composer. Every user who delegates reaches both; nested delegation reaches the top chat on a stock install.
- **Test plan execution**: RED: the qa-lead test author writes the orders above with saved failing logs for new behaviour. GREEN: one backend developer (agent payloads, gateway copy, `ended`, projection, contract) and one frontend developer (store, clock, named-row area) as a pair. CHECK: an independent auditor on a different model re-runs them and runs the mutants listed under "Gates". Then, at the cut, hands-on rows UAT-H1 to UAT-H11 run with a tester and an independent validator on the exact cut commit.

## User-facing documentation

| TODO | UI / behaviour change | User-facing page | Matching update |
|---|---|---|---|
| DOC-001 | The helper's chat shows its own retry, silence and recovered notices for every retry cause | `docs/troubleshooting.md` (retry section and silence section) | Keep the sentence "A helper's own chat shows the same line for the helper's requests" (now true). Add that the silence notice and the recovered line also show in the helper's chat |
| DOC-002 | The main chat shows a named row per waiting helper, also when idle and after a reload | `docs/troubleshooting.md` (same sections) and `docs/agents.md` ("Real work runs in helpers") | Say that the main chat shows "{helper name}: …" while a helper waits on its model, even after the main agent has finished replying; at most three rows plus a summary row; also for a helper's own helper |
| DOC-003 | Notices stay live only; channels unchanged; an open old tab needs a reload | `docs/troubleshooting.md` | One sentence that these notices are not saved and not sent to chat channels such as Telegram (the single "Retrying — please wait..." message there covers only the agent you talk to); one sentence that a tab left open across an upgrade shows the named rows after a reload |

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) | Documentation TODO(s) |
|---|---|---|---|---|
| FR-001 | US-1 | Helper's chat retry; timeout/empty outline; silence; recovered | 1, 1a, 2, 7; UAT-H1, H11 | DOC-001 |
| FR-002 | US-2 | Named retry row (outline); timeout/empty named outline; Named silence row; Old tab | 1, 1a, 2, 4, 7; UAT-H2, H3, H11 | DOC-002, DOC-003 |
| FR-003 | US-4 | Recovered does not clear own countdown; Reload of the helper's chat | 3, 5, 5b; UAT-H5 | none for the live part (regression guard); DOC-002 for reload |
| FR-004 | US-1 | Helper's chat retry before anything streamed; silence; recovered; silence clears; reload of the helper's chat | 6b, 7; UAT-H1, H4 | DOC-001 |
| FR-005 | US-2 | Named retry row; Named silence row; Unknown helper name; Grandchild | 6, 7; UAT-H2, H3, H4, H7 | DOC-002 |
| FR-006 | US-2 | Named row turns into recovered; Named row leaves (outline); silence clears; fallback clears; timeout/empty success clears (outline); later retry in the same turn; Missed end (outline) | 1b, 1d, 1e, 5, 5a, 6, 7; UAT-H2, H4, H10, H11, H12 | DOC-002 |
| FR-013 | US-2 | Silence clear does not erase the new retry | 1f, 7 | DOC-002 |
| FR-014 | US-2 | Missed end (outline: full snapshot, restart, live_only) | 5a, 7; UAT-H12 | DOC-002 |
| FR-007 | US-4 | Recovered does not clear own; Two helpers; One helper ending; Late end of an earlier turn | 1b, 5, 7; UAT-H5, H6 | DOC-002 |
| FR-008 | US-1, US-2 | Reload after the main agent finished; Reload of the helper's chat; Stale retry does not come back; A later retry in the same turn is shown | 1c, 1d, 5b, 7; UAT-H8 | DOC-002 |
| FR-009 | US-4 | More than three rows (outline); Row promotion | 6 | DOC-002 |
| FR-010 | US-3 | Screen reader hears each helper's name once | 6; UAT-H9 | none: the visible text is documented in DOC-002 |
| FR-011 | US-1, US-2 | Notices are not saved; Channels stay unchanged; Named retry row clears on fallback | 3a, 7a, 1e | DOC-003 |
| FR-012 | US-2 | Named row leaves (outline); Missed end; Grandchild | 1b, 7; UAT-H7 | DOC-002 |

**Completeness check**: every FR has at least one scenario and one test; every BDD scenario appears at least once; DOC-001 to DOC-003 cover every user-visible change.

---

## Assumptions

- The scripted fake model and isolated gateway from the 15 Nov probe (`/workspace/logs/1537/recipe/`) are the test base, extended with the no-response, empty-reply and fallback scripts; no paid model key and never the live instance.
- The 30-second silence threshold, the 120-second retry-wait cap and the retry-attempt totals are unchanged (existing #1478/#1280/#1664 behaviour).
- `ProviderRetryIndicator`, `ProviderWaitingIndicator` and the recovered line live in `src/components/chat/` / the chat store, not in the design-system catalog.
- That a helper turn always emits a turn-end event is Inferred from the gateway's own comment; CHECK covers it with the "helper turn end never emitted" mutant.
- No section of the template was removed.

## Clarifications

### 2026-11-15

- Q: Where do a helper's retry, silence and recovered notices go? → A: Option A: to the helper's chat, plus a named line in the main chat, also when the main chat is idle (founder, relayed by the chief 14:59Z).
- Q: Is #1850 part of this work? → A: Yes, as a rider (chief, 15:05Z).
- Q: Do chat channels follow the web? → A: Not in this spec; #1851 tracks it separately (filed by the chief).
- Q: Should the notices be saved? → A: No. Option A did not ask for it; kept live only (FR-011).
- Q: Wire shape? → A: Two optional fields `child_session_id` / `agent_id` plus a new `provider_status` state `ended`; no new frame type (architect ruling, 2026-11-15).
- Q: Reload as a MUST, with a snapshot field? → A: MUST, without a new snapshot field: projection keeps helper copies across the main agent's `done`, a server-clock estimate from `session_state.emitted_at`, and last-publish replay order (architect ruling, 2026-11-15). Amended 2026-11-16 (chief ruling Q1 A): the clock estimate uses the new additive `session_state.emitted_at_ms`, because whole-second `emitted_at` plus rounding up made the countdown read up to about 2 s high.
- Q: Helper of a helper? → A: Reachable on a stock install; named row in the top chat only; middle chat unchanged; cleared by `ended` (architect ruling, 2026-11-15).
- Q: Grill round 1 (REVISE, 8 MAJOR, 6 MINOR, 3 OBS) — founder questions? → A: None; the grill raised no new product choice. Round 2 found MAJ-006, MAJ-007 and MIN-002/005 only partly fixed (see below).
- Q: Grill round 2 (REVISE, 3 MAJOR, 4 MINOR) — founder questions? → A: None. Fix round 2: producer order silence-clear before retry (FR-013); non-incremental attach reconciles named rows (FR-014); a `responding` copy clears the named row after a first-call fallback or a successful timeout/empty retry, with no invented count and own-chat behaviour unchanged (FR-006); fallback fixture uses three primary 429s; same-turn later retry stays current (FR-008); routing-reader guard added to the regression list. Under the chief's review cap this is the last fixing round; the next step is the build.
- Q: Epic and wording? → A: Epic #1143; wording A–D approved as written (chief, 16:19Z). Build approved: Opus backend + Opus frontend pair, ChatGPT test author (RED first), Sonnet CHECK, chief sign-off on handleProviderFrame before GREEN, hands-on on the exact commit, 3 reviewers.
