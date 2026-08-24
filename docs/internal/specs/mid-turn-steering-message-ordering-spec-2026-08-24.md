# Feature Specification: Mid-turn steering — correct message ordering

**Created**: 2026-08-24
**Status**: Draft
**Input**: `docs/internal/architecture/ADR-070-mid-turn-steering-message-ordering.md`
(Accepted, grill-spec round 1 addressed). This spec operationalizes that ADR into
TDD/BDD-traceable requirements. Reading order: ADR first, this spec second — the
ADR is the source of truth for *why*; this document is the source of truth for
*what must be tested and how*.

---

## Existing Codebase Context

> GitNexus MCP tools were not connected in this session (`ToolSearch` for
> `query`/`context`/`impact`/`trace`/`explain` returned no matches). Per this
> skill's own fallback rule ("If the GitNexus index is stale or empty ... fall
> back to manual codebase exploration. Do not block on a stale index."), the
> codebase context below was gathered by direct reading, and is the same
> evidence base the ADR itself was built and grill-spec-reviewed against.

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `sendMessage` (`src/store/chat.ts`) | modify | Mid-turn (`isStreaming`) branch appends the steer's user message; must also close the currently-open assistant bubble. |
| `findLastAssistantMessageId` (`src/store/chat.ts`) | unchanged, still used | Backward role-scan. Stays valid for its other callers (`findAssistantMessageIdByTurnId`'s sibling use, C8 sweep's `lastMsgId`, `done`/`error` sweeps) — only its use as an implicit "safe to reuse" signal in the unguarded sites is being corrected. |
| `findOpenAssistantMessageId` (new, `src/store/chat.ts`) | add | Shared eligibility helper: raw-tail-is-assistant, or backward-scan match ONLY if still `isStreaming`. |
| `case 'token'` (`src/store/chat.ts`) | unchanged | Already correct once `isStreaming` reflects steer-time closure (traced in ADR §2.1). |
| `case 'tool_call_start'` (`src/store/chat.ts`) | unchanged | Already has an equivalent raw-tail/`isStreaming` guard (traced in ADR §2.1). |
| `case 'subagent_start'` (`src/store/chat.ts`) | modify | Currently a bare `findLastAssistantMessageId` call, no guard — ADR §2.1/F2. |
| `case 'media'` (`src/store/chat.ts`, 3 branches) | modify | Same bare-scan defect — ADR §2.1/F2. |
| `replay_message` handler, same-turn merge block (`src/store/chat.ts`) | modify | Two branches (coalesce-into-empty-placeholder; general merge) need a raw-tail guard — ADR §2.2. |
| `case 'error'` C8 sweep (`src/store/chat.ts`) | modify | Must exclude a `closedBySteer` bubble from re-stamping — ADR §2.6. |
| `ChatMessage` type (`src/store/chat.ts`) | modify | Add `closedBySteer?: boolean` (internal-only; NOT a new `AssistantMessage.status` value — ADR §2.4 explicitly rejects that design). |
| `hasStreamingMessage` / `VirtualizedMessageListInner` (`src/components/chat/ChatScreen.tsx`) | unchanged, test target | Self-heals once the live fix lands (ADR §2.1) — no code change, but needs its own committed test (ADR §4 F3; previously only verified by a deleted throwaway). |
| `findAssistantMessageIdByTurnId` (`src/store/chat.ts`) | unchanged | Already correct for multiple same-`turnId` bubbles (ADR §2.3). |
| `toolCallOwnerMessageId` / `toolCalls` maps (`src/store/chat.ts`) | unchanged | Keyed by `call_id`, position-independent (ADR §1.3) — needs a spanning-the-boundary test to move from "traced by reading" to "verified" (ADR §4 F4). |
| `markLastMessageInterrupted` (`src/store/chat.ts:1837-1878`) | modify | Bare `findLastAssistantMessageId` scan (`:1840`), invoked by Stop/Escape/`/cancel`. Grill-spec round 2 (NEW-001): in the steer→next-frame gap, this scan resolves to the just-closed `closedBySteer` bubble and cancelling mislabels it `'interrupted'`. ADR §2.7. |
| `lastAssistantMessageId` / `bucketToForeground` (`src/store/chat.ts:1539`) | modify | Bare scan, derived store field. Its only consumer anywhere in the codebase is the ARIA "New response from {agent}" announcement (`ChatScreen.tsx:2871-2883,2990-2993`). Grill-spec round 2 (NEW-002): fires prematurely at steer-time, then again at true completion. ADR §2.7. |

### Impact Assessment (manual, GitNexus unavailable)

| Symbol Modified | Risk Level | Direct dependents that must be re-verified |
|-----------------|------------|---------------------------------------------|
| `sendMessage` (isStreaming branch) | MEDIUM | Every `token`/`tool_call_start` handler behavior downstream of a steer (self-healing, but must be tested, not assumed); `src/store/chat.mid-turn-send.test.ts` (existing tests currently assert the pre-fix shape and must be rewritten). |
| `ChatMessage` type (`closedBySteer` field) | LOW | Purely additive optional field; TypeScript structural typing means no existing object literal becomes invalid. |
| `replay_message` same-turn merge | MEDIUM | All existing replay/reconnect tests exercising same-turn multi-segment merges (tool-call interleaved narration) must be confirmed to still pass — the new raw-tail guard must not fire for the ALREADY-correct non-steer case (turn_id-same, no intervening message). |
| `subagent_start`, `media` handlers | LOW–MEDIUM | Existing subagent/delegation and media-attachment tests must keep passing; new guard must not change behavior for the non-steer path (raw tail already assistant in the common case). |
| C8 error sweep | LOW | Existing error-frame tests (non-steer case) must keep passing — `closedBySteer` is `undefined`/falsy for them, so the added condition is a no-op. |
| `markLastMessageInterrupted` | MEDIUM | Existing cancel/Stop tests — confirmed by reading that `ChatScreen.mid-turn-send.test.tsx`'s cancel-related tests mock `cancelStream` itself (`useChatStore.setState({ cancelStream: mockCancelStream })`) and never exercise the real bubble-mutation logic, so they are unaffected; any test that DOES exercise the real function for the ordinary (no-steer, or new-bubble-already-open) case must keep passing unchanged. |
| `lastAssistantMessageId` / `bucketToForeground` | LOW | Single consumer (`ChatScreen.tsx`'s ARIA announcement) — no other dependent exists per codebase search, so the blast radius is fully contained to the new test added for this fix. |

### Cluster Placement

Single cluster: SPA chat state management + rendering (`src/store/chat.ts`,
`src/components/chat/ChatScreen.tsx`). No backend/contract cluster involved
(ADR §1.3, §4 — confirmed no wire-protocol change needed).

---

## Available Reference Patterns

Not applicable — this is a client-side (TypeScript/React/Zustand) bug fix in an
existing, mature module. No `docs/reference/go-implementation/` pattern applies
(those cover Go backend infrastructure: auth, payments, webhooks, etc.).

---

## User Stories & Acceptance Criteria

### User Story 1 — A follow-up sent while the agent is replying appears in the right place (Priority: P1)

A user is chatting with an agent. The agent is still composing its reply when the
user sends a follow-up message (a supported, working feature — the follow-up
does reach the agent and does influence its reply). Today, once the agent
continues talking after receiving that follow-up, the continuation appears
**above** the follow-up in the transcript, making the conversation read out of
order — visually, the agent looks like it answered before the question was
asked. This story fixes the reading order for the live (currently-open) chat
session.

**Why this priority**: Mid-turn steering is an existing, advertised capability;
a wrong reading order actively damages trust in a feature that otherwise works
correctly. P1 (high, not P0/critical) because there is no data loss and no
functional breakage — only a confusing visual presentation.

**Independent Test**: Open a chat, send a message, send a second message before
the first reply finishes, and read the transcript top to bottom — it must match
the order the messages/replies actually happened in.

**Acceptance Scenarios**:

1. **Given** an agent is actively replying to a first message, **When** the
   user sends a second message before that reply finishes, **Then** the agent's
   further reply content (produced after the second message was sent) appears
   below the second message, not above it.
2. **Given** an agent is actively replying and the user sends a second message,
   **When** the agent produces no further visible content and the reply simply
   ends, **Then** the transcript still reads: first message, the (now-complete)
   reply, second message — with nothing reordered or duplicated.
3. **Given** an agent is actively replying, **When** the user sends three
   follow-up messages in quick succession before any of them are answered,
   **Then** each follow-up appears in the order it was sent, and any further
   agent content appears after the last one sent so far when it arrives.

### User Story 2 — The correct order survives a page reload (Priority: P1)

The same conversation, after being refreshed or reopened, must show the exact
same reading order as it did live — a user should never see a "fixed" order
live and a "broken" order after reload of the same conversation.

**Why this priority**: Equal to Story 1 — a fix that only holds until the next
reload is not a fix a user can rely on, and is worse than an obviously-broken
feature because it looks fixed until it silently isn't.

**Independent Test**: Reproduce Story 1's scenario 1, then reload the page (or
simulate a reconnect) and confirm the reading order is unchanged from what was
shown live.

**Acceptance Scenarios**:

1. **Given** a past conversation where a follow-up was sent mid-reply and the
   agent continued after it, **When** the conversation is reloaded from
   scratch, **Then** the reconstructed transcript shows the same order as the
   live session did: message, reply-before-the-follow-up, follow-up,
   reply-after-the-follow-up.
2. **Given** the same past conversation, **When** it is reloaded, **Then** the
   agent's tool actions (if any) remain attached to whichever part of the reply
   they actually happened during, not shifted to the wrong segment.

### User Story 3 — Related live activity (agent sub-tasks, attached files) also respects the new order (Priority: P2)

If the agent delegates work to a sub-agent, or sends a file/image, immediately
after receiving a follow-up (before it has said anything else yet), that
activity must also appear correctly positioned relative to the follow-up — not
just plain text replies.

**Why this priority**: P2 (medium) — narrower window (only the specific case
where a sub-task or attachment is the very first thing to happen after a
follow-up, before any text), lower likelihood than the plain-text case in
Story 1, but the same visible defect if it occurs.

**Independent Test**: Trigger a scenario where the agent starts a sub-task (or
sends a file) as the first thing it does after receiving a mid-reply follow-up,
and confirm it's positioned after the follow-up, not before it.

**Acceptance Scenarios**:

1. **Given** an agent is actively replying and receives a follow-up message,
   **When** the very next thing the agent does is start a sub-task (delegate to
   another agent) rather than say more text, **Then** that sub-task activity
   appears after the follow-up in the transcript.
2. **Given** the same setup, **When** the agent instead sends a file/image as
   the next thing it does, **Then** the attachment appears after the follow-up.

---

## Behavioral Contract

Primary flows:
- When a user sends a message while the agent is still replying, the system
  appends it after the reply-so-far and marks that reply-so-far as finished.
- When the agent produces further content after that point, the system starts
  a new, separate reply entry positioned after the user's message.
- When a conversation containing a mid-reply follow-up is reloaded, the system
  reconstructs the same two-part (before/after) reply split it showed live.

Error flows:
- When the agent's turn ends in an error after a follow-up was sent but before
  any further reply content arrived, the system must not overwrite the
  already-finished reply segment that preceded the follow-up.

Boundary conditions:
- When a follow-up is sent before the agent has produced any visible text at
  all, the system still closes out that (empty) reply segment correctly and
  does not let it be mistaken for the *current* turn by anything that runs
  afterward.
- When multiple follow-ups are sent in a row before the agent responds to any
  of them, the system preserves the sending order of all of them and starts
  a new reply segment only once, after the last one at the time the agent's
  next content arrives.

---

## Edge Cases

- What happens when a sub-task the agent started **before** the follow-up
  finishes reporting its result **after** the follow-up? Expected: the result
  still attaches to the reply segment the sub-task actually belongs to (the one
  open when the sub-task started), not to whatever segment is newest by the
  time the result arrives.
- What happens when the agent's turn is cancelled (Stop) after a follow-up was
  sent? Expected: only the currently-open (post-follow-up) segment is marked
  cancelled; the already-finished pre-follow-up segment is untouched.
- What happens when the conversation is trimmed (old messages evicted to keep
  the view lightweight) and a mid-reply follow-up's two segments straddle the
  trim boundary? Expected: each segment is evicted independently and
  correctly, like any other message — no special coupling between the two
  halves is assumed to survive trimming.
- What happens when the network drops and reconnects while a mid-reply
  follow-up's second segment is still being produced? Expected: on reconnect,
  the reconstructed transcript shows the same two-part order as before the
  drop.

---

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not change what the follow-up message DOES (mid-turn
  steering's actual behavior — the follow-up being incorporated into the
  agent's reasoning) — this fix is presentation/ordering only, per ADR §5.3.
- The system must not introduce any new network message format or change what
  the server sends — the ADR traced this as unnecessary and it must stay that
  way (no `contracts/` changes).
- The system must not make the previously-shown (pre-follow-up) portion of a
  reply disappear, become uneditable in a way it wasn't before, or lose any
  content — it must remain fully visible, just correctly positioned and marked
  finished.
- The system must not let a later, unrelated error accidentally overwrite or
  relabel a reply segment that was already correctly finished because of a
  follow-up (this is the specific failure mode ADR §2.6/grill-spec F5
  identifies and must not regress).
- The system must not silently drop a sub-task's activity or an attachment
  when it cannot find an open reply segment to attach to immediately after a
  follow-up — for a sub-task specifically, a new segment must be started
  rather than losing the activity (ADR §2.1/F2); for a purely cosmetic
  "attachment could not be displayed" notice with no eligible segment, no
  content is lost by not showing the notice (nothing was displayable anyway).

### Machine-Verifiable Constraints

**Data Constraints**:
- The internal marker distinguishing "this reply segment ended because of a
  follow-up" MUST NOT be part of the same value used to say "streaming vs.
  finished vs. errored vs. cancelled" — it is a separate, additional signal
  (ADR §2.4). A reply segment closed this way MUST report itself as "finished"
  through the existing finished-state signal, identically to an ordinarily
  completed reply.
- The ordering-boundary rule ("was anything else added after this segment
  since it was last written to?") MUST be evaluated identically for the live
  session and for a reloaded session — the same logical rule, expressed
  through whichever mechanism each of those two code paths already uses to
  represent "still open" (ADR §2.1 vs §2.2).

**Scope Boundaries**:
- The system MUST NOT alter the ordering/attachment logic for tool-call
  ownership, sub-task result routing, or turn-cancellation correlation, beyond
  what §2.1–§2.6 of ADR-070 specify — these are traced as already correct
  (ADR §1.3, §2.3) and must be left untouched, with regression tests proving
  they still behave the same for the non-follow-up case.

### Conservative Type Design

The new internal marker is a plain optional boolean, not a new nominal type or
enum — it carries no invariants or methods beyond "true means this bubble was
closed by a follow-up," matching ADR §2.4's explicit choice against widening
the existing status enumeration for this purpose.

---

## Prerequisites

- **Hardware / OS**: Existing project prerequisites — no new requirement.
- **Required runtimes**: Existing project toolchain (Node/npm for the SPA;
  Go for the backend, unaffected). No version changes.
- **Required services**: None new. Live UAT (Playwright) needs a running
  instance of the application (existing embedded-SPA Go binary) and, per
  operator resolution (ADR §5.2), a working LLM provider key configured in
  the environment used for UAT.
- **Network assumptions**: Same as today — outbound to whichever LLM provider
  is configured for the UAT run; the fix itself requires no new network access.
- **Accounts / credentials**: An LLM provider API key for the live UAT step
  only (not for the unit/component tests, which do not call a real model).

---

## Development Setup

No new setup — this is a fix within the existing SPA test/build pipeline.

1. `npm run typecheck` — must stay green (the `closedBySteer` field addition
   must not break the build).
2. `npx vitest run` — the target suite for all new/rewritten tests in this
   spec.
3. For live UAT: build and run the embedded-SPA Go binary per this project's
   existing documented procedure (`CLAUDE.md`'s "Running the embedded SPA"
   section) — not the Vite dev server.

**Expected first-run behaviour**: `npm run typecheck` and `npx vitest run`
both exit 0 before and after the change (before: confirms baseline; after:
confirms the fix didn't break anything else).

**Common first-run failures**: `tsc --noEmit` without `-b` is a silent no-op
in this repo (documented pitfall) — always use `npm run typecheck`.

---

## Tech Stack

| Category | Choice | Version / Pin | Source |
|----------|--------|---------------|--------|
| Language | TypeScript | project-pinned | `CLAUDE.md` Tech Stack |
| Framework | React 19, Zustand (state) | project-pinned | `CLAUDE.md` Tech Stack |
| Test framework | Vitest, Testing Library | project-pinned | existing `*.test.ts(x)` files in `src/` |
| E2E / UAT | Playwright MCP | — | this spec's UAT phase (ADR §5.2) |
| Build tool | Vite | project-pinned | `CLAUDE.md` |

---

## Deployment / Runtime

- **Target environment**: Unchanged — embedded SPA served by the existing Go
  gateway binary.
- **Online / offline**: Unchanged.
- **Resource limits**: Unchanged — this is a small, targeted logic change with
  no new allocations of consequence.
- **Start / stop commands**: Unchanged (see `CLAUDE.md`'s gateway run
  instructions).
- **Health check**: Unchanged.
- **Logs / telemetry**: No new logging planned; existing `console.warn`/
  `logDiagnostic` calls in the touched handlers are unaffected. This is a
  deliberate choice, not an oversight (grill-spec round 1, OBS-003): the
  project is no-telemetry by design, and the regression-guard tests added by
  this spec (particularly the 3 REWRITE tests plus Tests #4/#5/#7/#8, which
  pin the exact bug this fix corrects) are considered the sufficient
  detection mechanism for a future silent regression of this defect class —
  not a runtime signal.

---

## Integration Boundaries

### LLM Provider (UAT only)

- **Data in**: A prompt engineered to stream for several seconds, giving a
  reliable window to send a mid-turn follow-up during live UAT.
- **Data out**: Streamed token frames over the existing WebSocket protocol
  (unchanged).
- **Contract**: Existing `contracts/asyncapi.yaml` `TokenFrame`/`DoneFrame`/etc.
  — unmodified by this fix.
- **On failure**: If no provider key is available in the UAT environment, the
  UAT step must say so explicitly and fall back to scripted WebSocket frames
  (ADR §5.2) rather than silently skipping live verification.
- **Development**: Real service preferred (operator resolution, ADR §5.2);
  scripted-frame twin as an explicitly-flagged fallback only.

---

## BDD Scenarios

### Feature: Mid-turn steering message ordering

#### Scenario: A reply continues into a new segment after a follow-up is sent

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the agent has started replying to a first message and has produced
  some visible text
- **And** the user sends a second message before the reply finishes
- **When** the agent produces further content
- **Then** that further content appears in a reply segment positioned after
  the second message
- **And** the reply segment produced before the second message is shown as
  finished, not still "in progress"

---

#### Scenario: A follow-up with no further reply content leaves the transcript correctly ordered

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the agent has produced some visible text and the user sends a
  follow-up
- **When** the turn ends without any further content being produced
- **Then** the transcript shows exactly: first message, the finished reply,
  the follow-up — with no extra or duplicated entries

---

#### Scenario Outline: Multiple rapid follow-ups preserve their own order

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Edge Case

- **Given** the agent is actively replying
- **When** the user sends `<count>` follow-up messages in quick succession
  before any further reply content arrives
- **Then** all `<count>` follow-ups appear in the transcript in the order they
  were sent
- **And** exactly one reply segment exists before them, still finished

**Examples**:

| count | expected follow-up positions |
|-------|-------------------------------|
| 2     | both after the finished reply segment, in send order |
| 3     | all three after the finished reply segment, in send order |

---

#### Scenario: A follow-up sent before any reply text exists still closes correctly

**Traces to**: Edge Cases (empty pre-follow-up segment)
**Category**: Edge Case

- **Given** the agent's reply segment has not produced any visible text yet
- **When** the user sends a follow-up
- **Then** that (empty) reply segment is marked finished
- **And** it is not mistaken for the currently-active segment by anything that
  happens afterward (specifically: a subsequent turn-level error must not
  relabel it)

---

#### Scenario: The order survives a page reload

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a past conversation where a follow-up was sent mid-reply and the
  agent continued afterward
- **When** the conversation is reloaded from scratch
- **Then** the reconstructed transcript shows: message, pre-follow-up reply
  segment (finished), follow-up, post-follow-up reply segment (finished)
- **And** this matches exactly what was shown live before the reload

---

#### Scenario: Tool actions stay attached to the correct segment after a reload

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** a past conversation where the agent used a tool before a follow-up
  and a different tool after it
- **When** the conversation is reloaded
- **Then** each tool action is shown attached to the reply segment it actually
  happened during

---

#### Scenario: A tool call spanning the follow-up boundary resolves onto the correct segment

**Traces to**: Edge Cases (in-flight tool call across the boundary); ADR §4 F4
**Category**: Edge Case

- **Given** the agent starts a tool action before the user sends a follow-up
- **And** that tool action has not finished yet when the follow-up is sent
- **When** the tool action's result arrives after the follow-up
- **Then** the result is shown attached to the reply segment that was open
  when the tool action started, not to any segment created afterward

---

#### Scenario: A sub-task started immediately after a follow-up appears after it

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Edge Case

- **Given** the agent is actively replying and receives a follow-up
- **When** the next thing the agent does is start a sub-task, with no further
  text produced first
- **Then** the sub-task activity is shown after the follow-up, not before it

---

#### Scenario: An attachment sent immediately after a follow-up appears after it

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Edge Case

- **Given** the agent is actively replying and receives a follow-up
- **When** the next thing the agent does is send a file/image, with no further
  text produced first
- **Then** the attachment is shown after the follow-up, not before it

---

#### Scenario: A cancelled turn after a follow-up only cancels the open segment

**Traces to**: Edge Cases (cancellation after a follow-up)
**Category**: Error Path

- **Given** the agent is actively replying, the user sends a follow-up, and the
  agent has started a new reply segment after it
- **When** the turn is cancelled
- **Then** only the new (post-follow-up) segment is marked cancelled
- **And** the already-finished pre-follow-up segment is untouched

---

#### Scenario: A cancellation sent immediately after a follow-up, before any new segment starts, does not mislabel the finished segment

**Traces to**: FR-009 (grill-spec round 2, NEW-001)
**Category**: Edge Case

- **Given** the agent is actively replying and the user sends a follow-up
- **And** no further reply content has arrived yet (no new segment has started)
- **When** the turn is cancelled (Stop / Escape / `/cancel`)
- **Then** the already-finished pre-follow-up segment keeps its finished status
  and is NOT relabeled as cancelled
- **And** a new, properly-marked cancelled placeholder is created instead of
  reusing the finished segment

---

#### Scenario: The "new response" announcement fires exactly once per steered turn

**Traces to**: FR-010 (grill-spec round 2, NEW-002)
**Category**: Edge Case

- **Given** the agent has produced visible text and the user sends a follow-up
- **When** that pre-follow-up segment closes as a result
- **Then** no "new response" announcement is made yet
- **When** the agent's true final reply segment later completes
- **Then** exactly one "new response" announcement is made, for that final
  segment

---

## Test-Driven Development Plan

### Test Hierarchy

| Level       | Scope                                            | Purpose |
|-------------|---------------------------------------------------|---------|
| Unit        | `findOpenAssistantMessageId`, the `sendMessage` steer branch's closure logic, the C8 sweep exclusion | Validates the core boundary rule in isolation |
| Integration | Full `src/store/chat.ts` frame-handling sequences (`sendMessage` → `token`/`tool_call_start`/`subagent_start`/`media` → `done`/`error`), and `replay_message` sequences | Validates the store behaves correctly across a realistic sequence of frames |
| E2E (component) | `ChatScreen.tsx` rendering with a seeded steered-session store state | Validates the DOM actually reflects the fix (ADR §4 F3) |
| E2E (live) | Playwright-driven browser against the running embedded SPA with a real (or scripted-frame) mid-turn steer | Validates the fix from an actual user's vantage point (UAT, holdout — see below) |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|--------------|
| 1 | `findOpenAssistantMessageId: returns the raw tail when it is an assistant message` | Unit | Scenario: A reply continues into a new segment | Baseline: no steer in play, normal reuse. |
| 2 | `findOpenAssistantMessageId: returns null when the raw tail is a user message and the last assistant message is closed` | Unit | Scenario: A reply continues into a new segment | The steered case — must not return the closed bubble. |
| 3 | `findOpenAssistantMessageId: returns the last assistant message when it is still isStreaming despite not being the raw tail` | Unit | (supports `tool_call_start`'s existing, unchanged behavior) | Regression guard — confirms the helper doesn't change the non-steer in-flight-tail-is-something-else case if one exists. |
| 4 | `sendMessage (mid-turn branch): closes the currently-open assistant bubble (isStreaming:false, closedBySteer:true) when a steer is sent` | Unit | Scenario: A reply continues into a new segment | Core fix, ADR §2.1. |
| 5 | `sendMessage (mid-turn branch): closing an empty pre-steer bubble still marks it closedBySteer` | Unit | Scenario: A follow-up sent before any reply text exists | Edge case, empty content. |
| 6 | `case 'error' C8 sweep: does not re-stamp a closedBySteer bubble` | Unit | Scenario: A follow-up sent before any reply text exists | ADR §2.6/F5 fix. |
| 7 | `token → sendMessage(steer) → token: two distinct assistant bubbles, correct order, correct content split` | Integration | Scenario: A reply continues into a new segment | End-to-end store behavior for the primary bug. |
| 8 | `sendMessage(steer) → done (no further tokens): single finished bubble, steer message after it` | Integration | Scenario: A follow-up with no further reply content | Confirms the ADR's traced "unchanged" existing test still holds under the new design. |
| 9 | `sendMessage(steer) x3 in a row → token: three follow-ups in send order, one bubble before them` | Integration | Scenario Outline: Multiple rapid follow-ups | ADR-anticipated generalization to N steers. |
| 10 | `tool_call_start (pre-steer) → sendMessage(steer) → tool_call_result (post-steer) → token → done: result attaches to the pre-steer bubble` | Integration | Scenario: A tool call spanning the follow-up boundary | ADR §4 F4 — closes the "traced by reading, not verified" gap. |
| 11 | `subagent_start immediately after sendMessage(steer), before any token: opens a new bubble after the steer, does not attach to the closed one` | Integration | Scenario: A sub-task started immediately after a follow-up | ADR §2.1/F2. |
| 12 | `media frame (real attachment) immediately after sendMessage(steer), before any token: attaches to a new bubble after the steer` | Integration | Scenario: An attachment sent immediately after a follow-up | ADR §2.1/F2. |
| 13 | `media frame (invalid/zero parts notice) immediately after sendMessage(steer), no eligible bubble: notice is dropped, not attached to the closed pre-steer bubble` | Integration | (supports Scenario: An attachment sent immediately after a follow-up) | ADR §2.1/F2, documented drop-not-misattach decision. |
| 14 | `sendMessage(steer) → new bubble opens → cancel: only the new bubble is marked interrupted, the pre-steer bubble is untouched` | Integration | Scenario: A cancelled turn after a follow-up | Regression guard on existing cancellation sweep behavior. |
| 15 | `replay_message: two same-turn assistant entries separated by a user entry do NOT merge; a new bubble is created for the second` | Integration | Scenario: The order survives a page reload | ADR §2.2 — the core replay-path fix. |
| 16 | `replay_message: two same-turn assistant entries with NO intervening entry still merge (regression guard, non-steer case unchanged)` | Integration | (regression) | Confirms the existing, correct interleaved-narration merge behavior (tool-call segments within one uninterrupted turn) is preserved. |
| 17 | `replay_message: tool calls before/after a replayed steer boundary attach to their correct respective bubble` | Integration | Scenario: Tool actions stay attached to the correct segment after a reload | ADR §2.2 + §1.3 combined verification. |
| 18 | `ChatScreen renders streaming-message-anchor pinned to the post-steer bubble once it exists` | E2E (component) | Scenario: A reply continues into a new segment | ADR §4 F3 — the previously-only-throwaway-verified render claim, now committed. |
| 19 | `ChatScreen: DOM order top-to-bottom matches [user, finished-reply, follow-up, live-reply] for a steered session` | E2E (component) | Scenario: A reply continues into a new segment | Direct DOM-order assertion for the reported symptom. |
| 20 | `sendMessage(steer) → markLastMessageInterrupted (cancel), no intervening frame: the closed pre-steer bubble keeps status:'done'/closedBySteer:true; a NEW placeholder is created with status:'interrupted'` | Integration | Scenario: A cancellation sent immediately after a follow-up... | Grill-spec round 2, NEW-001/FR-009 — closes the one ordering Test #14 does not cover. |
| 21 | `ChatScreen: the "New response from {agent}" ARIA announcement fires exactly once for a steered turn (not at steer-time, only at true completion)` | E2E (component) | Scenario: The "new response" announcement fires exactly once per steered turn | Grill-spec round 2, NEW-002/FR-010. |

### Test Datasets

#### Dataset: Steer timing relative to prior content

| # | Input (pre-steer bubble state) | Boundary Type | Expected Output | Traces to | Notes |
|---|-------------------------------|----------------|------------------|-----------|-------|
| 1 | Non-empty content, `isStreaming:true` | Happy path | Closed (`isStreaming:false`, `closedBySteer:true`), content unchanged | Scenario: A reply continues into a new segment | The common case |
| 2 | Empty content (`''`), `isStreaming:true` | Boundary (zero) | Closed (`isStreaming:false`, `closedBySteer:true`), content stays `''` | Scenario: A follow-up sent before any reply text exists | Zero-content edge case, F5's precondition |
| 3 | Non-empty content, tool call in flight (no result yet) | Edge case | Closed; the in-flight tool call remains attributed to it by `call_id` once resolved | Scenario: A tool call spanning the follow-up boundary | Verifies position-independence claim |

#### Dataset: Number of rapid steers

| # | Input (steers before any further token) | Boundary Type | Expected Output | Traces to | Notes |
|---|------------------------------------------|----------------|------------------|-----------|-------|
| 1 | 1 | Happy path | 1 finished bubble, 1 steer message, then new bubble on next token | Scenario: A reply continues into a new segment | — |
| 2 | 2 | Boundary (multiple) | 1 finished bubble, 2 steer messages in order, then ONE new bubble on next token | Scenario Outline: Multiple rapid follow-ups | Confirms no bubble is opened per-steer — the boundary this dataset exists to prove. N=3 dropped here (grill-spec round 1, MIN-004): the close logic is one unconditional flag-flip per `sendMessage` call with no per-count branching anywhere in the design, so N=3 would exercise the identical code path a third time with no new condition crossed. The BDD Scenario Outline above keeps its own N=2/N=3 examples for readability; this TDD dataset trims to the minimum that proves the claim. |

#### Dataset: Replay merge eligibility

| # | Input (two same-`turnId` assistant entries) | Boundary Type | Expected Output | Traces to | Notes |
|---|-----------------------------------------------|----------------|------------------|-----------|-------|
| 1 | No entry of any other role between them | Happy path (regression) | MERGE into one bubble (existing, correct behavior) | (regression guard) | Must not regress the tool-call-interleaved-narration case |
| 2 | One `role:'user'` entry between them | Boundary (the fix) | Do NOT merge — two separate bubbles | Scenario: The order survives a page reload | The core replay-path fix |
| 3 | One `role:'user'` entry between them, both entries also carry different `agentId` | Edge case (compound) | Do NOT merge — `compatibleProducer` alone would already refuse this; confirms the two guards compose correctly | (regression guard) | Belt-and-braces |

### Regression Test Requirements

**Modifying existing functionality — yes.**

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---------------------|----------------|------------------------------|-------|
| Mid-turn steer appends only a user message, no second placeholder | `chat.mid-turn-send.test.ts:117`, `'appends only a user message after the streaming assistant bubble...'` | **YES — REWRITE (lines 130-131 only).** Message-count/role/content assertions (126-129, 132-134, 138, 141) are unaffected and stay; `expect(messages[1].isStreaming).toBe(true)` / `expect(messages[1].status).toBe('streaming')` must become `false`/`'done'`, plus a new `expect(messages[1].closedBySteer).toBe(true)` assertion | **Grill-spec round 1 on this spec, MAJ-001**: the table originally (wrongly) claimed this test was unaffected — self-contradictory, since it's asserting the exact field (`isStreaming`) the fix flips at the exact moment (steer-send, no intervening frame) the fix flips it. Verified directly against `chat.ts:2471-2531`, which today performs no bubble mutation at all in this branch. This is the 3rd REWRITE test alongside `:421`/`:511` — see SC-003. |
| Steer forwards the WS frame with the same shape a new turn uses | `chat.mid-turn-send.test.ts`: `'forwards the steering message over the WS...'` | No | Unaffected by this fix |
| 3 rapid steers append 3 distinct messages, no dedupe | `chat.mid-turn-send.test.ts`: `'3 rapid mid-turn sends append 3 distinct user messages...'` | Extend (Test #9 above) to also assert bubble-closure/count, not just message content | The existing assertions stay valid; new assertions are additive |
| Failed steer marks only the steer message as error | `chat.mid-turn-send.test.ts`: `'a failed mid-turn send marks only the steering message as error...'` | No | Unaffected — this fires before the bubble-closure change is reached (offline-branch precedes it) |
| Live tool calls are not baked mid-turn | `chat.mid-turn-send.test.ts`: `'does NOT bake live tool calls onto the previous assistant message...'` | No | Unaffected — baking logic untouched |
| `steer → tool_call_start → token → done: exactly one assistant bubble...` | `chat.mid-turn-send.test.ts:421` | **YES — REWRITE.** Must now assert TWO bubbles (ADR §4) | This test currently encodes the bug |
| `steer → done (no tool calls): unchanged — single assistant bubble...` | `chat.mid-turn-send.test.ts:487` | No — traced to still hold under the new design (ADR §4); add an explicit assertion on `closedBySteer` state for completeness | Confirms the ADR's own "should keep passing" claim empirically |
| `a tool call BEFORE the steer and one AFTER both keep correct owner/offset on the same bubble` | `chat.mid-turn-send.test.ts:511` | **YES — REWRITE.** Must now assert the pre-steer and post-steer tool calls land on their DIFFERENT respective bubbles | This test currently encodes the bug |
| Existing non-steer same-turn replay merge (interleaved narration/tool calls within one uninterrupted turn) | (search `replay_message`/same-turn merge tests, e.g. `chat.tool-call-offset.test.ts`'s "WS-replay same-turn merge" block referenced in `chat.ts`'s own comment) | Confirm still green; add Dataset "Replay merge eligibility" row 1 explicitly if not already covered by name | Must not regress — the raw-tail guard must only fire when something NEW actually intervened |
| Existing subagent/delegation attachment tests (non-steer case) | Existing `subagent_start`-related tests in `chat.ts`'s test suite | Confirm still green | The new guard must be a no-op when the raw tail is already the correct assistant bubble |
| Existing media-attachment tests (non-steer case) | Existing `media`-frame tests | Confirm still green | Same reasoning |
| Composer UI gates while streaming (Enter-to-steer bypass, mid-stream Send button, Attach/drag-drop while streaming) | `src/components/chat/ChatScreen.mid-turn-send.test.tsx` (585 lines) | No | **Grill-spec round 1 on this spec, MIN-002**: this file was absent from the original regression audit. Confirmed by reading: it seeds `useChatStore` with only a flat `isStreaming: true`, never a `messagesById`/per-message `isStreaming` structure — its assertions are about composer input handling, orthogonal to the message-array ordering this fix touches. No change required, now explicitly audited rather than silently assumed. |
| Cancellation marks the last assistant message interrupted (ordinary case: no steer, or a new segment already open) | Existing cancel/Stop tests | No, for the ordinary cases — but see Test #20 (new) for the previously-uncovered "steer, then cancel with no intervening frame" ordering | **Grill-spec round 2, NEW-001**: `ChatScreen.mid-turn-send.test.tsx`'s cancel-related tests mock `cancelStream` itself, never exercising the real `markLastMessageInterrupted` bubble-mutation logic — confirmed by reading. Any test that DOES exercise the real function for the ordinary case must keep passing; Test #20 covers the one sequence that previously had no coverage at all. |
| The "new response" ARIA announcement (ordinary, non-steer turn completion) | No existing test found (confirmed by search: `lastAssistantMessageId` and `"New response from"` appear in no test file today) | Yes — new coverage, not a rewrite (Test #21) | **Grill-spec round 2, NEW-002**: this consumer had zero test coverage before this spec, steered or not. Test #21 is net-new coverage for both the steered case (the bug) and, incidentally, the first coverage of the ordinary case too. |

---

## Functional Requirements

- **FR-001**: The system MUST close the assistant reply segment that is open at
  the moment a mid-turn follow-up is sent, marking it as finished and
  internally distinguishable as closed-by-follow-up, without changing its
  visible content.
- **FR-002**: The system MUST start any further reply content, produced after
  a mid-turn follow-up, in a new reply segment positioned after that
  follow-up in the transcript.
- **FR-003**: The system MUST apply FR-001/FR-002's boundary rule
  consistently to plain text, sub-task activity, and file/image attachments —
  not only to plain text.
- **FR-004**: The system MUST reconstruct the same segment split, in the same
  order, when a conversation containing a mid-turn follow-up is reloaded, as
  it showed live.
- **FR-005**: The system MUST attach a tool action's result to the reply
  segment that was open when that tool action started, regardless of whether
  a follow-up was sent while the tool action was still in flight.
- **FR-006**: The system MUST NOT let a later turn-level error re-label or
  overwrite a reply segment that was already correctly closed by a follow-up.
- **FR-007**: The system MUST NOT introduce any new network wire format or
  change existing wire formats to achieve FR-001–FR-006.
- **FR-008**: The system MUST preserve the exact ordering of multiple
  follow-ups sent in quick succession before any further reply content
  arrives, and MUST start only one new reply segment (not one per follow-up)
  once further content does arrive. (Grill-spec round 1, MIN-003: changed
  from SHOULD to MUST — the original SHOULD was inconsistent with Acceptance
  Scenario 3, the BDD Scenario Outline, and Test #9's unconditional
  assertion, all of which already treated ordering-preservation as
  non-negotiable.)
- **FR-009** (added, grill-spec round 2 on the spec, NEW-001): The system
  MUST NOT let a user-initiated cancellation (Stop / Escape / `/cancel`)
  re-label or overwrite a reply segment that was already correctly closed by
  a follow-up, even when that segment is the only assistant message present
  at the moment of cancellation (i.e. cancellation arriving in the gap
  between a follow-up closing one segment and the next segment starting).
  When no reply segment is currently open at cancellation time, the system
  MUST create a new, properly-marked cancelled placeholder rather than
  reusing the closed one — mirroring FR-006's error-path guarantee for the
  cancellation path.
- **FR-010** (added, grill-spec round 2 on the spec, NEW-002): The system
  MUST announce a new reply to assistive technology (screen readers) exactly
  once per steered turn — when the turn's true final reply segment
  completes — and MUST NOT announce prematurely when an earlier segment
  closes solely because a follow-up was sent.

---

## Success Criteria

- **SC-001**: In a live session, sending a follow-up mid-reply and then
  observing the agent's continued reply results in a transcript where the
  continued reply's content renders strictly below the follow-up message —
  verified by DOM order assertion in the E2E (component) tests and by direct
  observation in the Playwright UAT.
- **SC-002**: Reloading a conversation containing at least one mid-turn
  follow-up reproduces matching role/order sequencing (message count, role
  sequence, tool-call attachment) to what the live session showed — verified
  by structural assertions on the reconstructed store state in the
  integration replay tests (Test #15–#17). (Grill-spec round 1, OBS-001:
  softened from "byte-identical," which overclaimed relative to what these
  store-shape assertions actually check — no test in this plan performs a
  literal live-vs-reload snapshot diff.)
- **SC-003**: 100% of the Test Implementation Order table (21 tests, after
  round 2's additions #20/#21) passes,
  and 100% of the three existing tests listed as "REWRITE" in the Regression
  Test Requirements table (`chat.mid-turn-send.test.ts:117`, `:421`, `:511`)
  are updated and passing — verified by `npx vitest run` exiting 0. (`:487`
  is the fourth pre-existing steer test in that file; it is traced to keep
  passing UNMODIFIED, not rewritten — see the Regression table.)
- **SC-004**: `npm run typecheck` exits 0 after the `closedBySteer` field
  addition and all touched-file edits.
- **SC-005**: Live UAT (or its explicitly-flagged scripted-frame fallback,
  per ADR §5.2) demonstrates the fix against the actual running application,
  not only against unit/component tests — a screenshot or DOM snapshot
  showing correct order is captured as evidence.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|-------------------|----------------|
| FR-001 | US-1 | A reply continues into a new segment; A follow-up sent before any reply text exists | Tests #1, #2, #4, #5 |
| FR-002 | US-1 | A reply continues into a new segment; A follow-up with no further reply content | Tests #1, #2, #4, #7, #8, #18, #19 |
| FR-003 | US-3 | A sub-task started immediately after a follow-up; An attachment sent immediately after a follow-up | Tests #11, #12, #13 |
| FR-004 | US-2 | The order survives a page reload; Tool actions stay attached to the correct segment after a reload | Tests #15, #16, #17 |
| FR-005 | US-2 | A tool call spanning the follow-up boundary resolves onto the correct segment | Test #10, #17 |
| FR-006 | (edge case) | A follow-up sent before any reply text exists still closes correctly | Test #6 |
| FR-007 | (non-behavior, ADR §1.3/§4) | (all scenarios — no wire-format scenario exists because none is introduced) | (verified by absence of `contracts/` changes; no dedicated test — a contract-drift CI gate, `make verify-contracts`, already guards this) |
| FR-008 | US-1 | Multiple rapid follow-ups preserve their own order | Test #9 |
| FR-009 | (edge case, added round 2) | A cancellation sent immediately after a follow-up... | Test #20 |
| FR-010 | (edge case, added round 2) | The "new response" announcement fires exactly once per steered turn | Test #21 |

Test #3 (`findOpenAssistantMessageId` regression guard for the non-steer
in-flight-tail case) supports FR-001/FR-002 by protecting the helper's
correctness generally; it has no standalone BDD scenario because it guards
against a hypothetical regression rather than asserting a new user-facing
behavior — listed here rather than in a matrix row to avoid implying a BDD
scenario exists for it that does not (grill-spec round 1, MIN-001).

**Completeness check**: Every FR-xxx has at least one BDD scenario and test
except FR-007, which is a negative requirement (absence of a wire change) —
its verification is the existing `make verify-contracts` CI gate reporting no
drift, not a new test, consistent with this spec's "no contract change" scope.
Every BDD scenario above appears in at least one row.

---

## Ambiguity Warnings

All ambiguities surfaced during discovery were already resolved by the
operator during ADR-070's own review cycle (§5 of the ADR) or by grill-spec
round 1's findings (§6 of the ADR, addressed in §2.1/§2.4/§2.6). No unresolved
ambiguity remains that would require an assumption at implementation time.
Table intentionally near-empty per the skill's own instruction to record
genuine gaps only:

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|-------------------|---------------------------|------------------------|
| 1 | Exact model/prompt to use for the live Playwright UAT run, to reliably create a multi-second streaming window | Pick a prompt/model combination known to stream slowly enough to interact with (e.g. asking for a longer explanation) | Resolved at UAT execution time by checking what provider key is actually configured in this environment (ADR §5.2) — not a spec-time blocker. |

---

## Evaluation Scenarios (Holdout)

> For post-implementation evaluation only. Not referenced in the TDD plan or
> traceability matrix above.

### Scenario: Live conversation, real follow-up, real continued reply
- **Setup**: A running instance of the application, a configured agent and
  provider, a fresh chat session.
- **Action**: Send a message likely to produce a multi-second streaming reply;
  while it is still streaming, send a second, different message.
- **Expected outcome**: Reading the chat top to bottom, the second message
  appears below the first reply's visible content so far, and any further
  reply content appears below the second message — never above it.
- **Category**: Happy Path

### Scenario: Reload immediately after a live steered exchange
- **Setup**: Continuation of the scenario above, immediately after the turn
  finishes.
- **Action**: Reload the page.
- **Expected outcome**: The same reading order is shown after reload as was
  shown live, with no merged-out-of-order bubble.
- **Category**: Happy Path

### Scenario: Three follow-ups sent back-to-back
- **Setup**: A running instance, a streaming reply in progress.
- **Action**: Send three short follow-up messages in immediate succession,
  before the agent has replied to any of them.
- **Expected outcome**: All three appear in the order sent; the agent's
  eventual further reply appears after the third one, not interleaved among
  them.
- **Category**: Happy Path

### Scenario: Follow-up sent, then the turn errors out
- **Setup**: A running instance where a turn can be made to fail after a
  follow-up is sent (e.g. by disconnecting a required credential mid-turn, if
  the test environment allows deliberately inducing this).
- **Action**: Send a follow-up mid-reply, then force the turn to end in error
  before any further content is produced.
- **Expected outcome**: The pre-follow-up reply content is still shown intact
  and correctly marked finished — not overwritten with the error.
- **Category**: Error

### Scenario: Follow-up sent before the agent has said anything
- **Setup**: A running instance, a turn just started (agent hasn't produced
  any visible text yet).
- **Action**: Send a follow-up immediately, before any reply text appears.
- **Expected outcome**: No blank/broken bubble appears; the transcript reads
  cleanly: message, follow-up, (eventual) reply.
- **Category**: Edge Case

### Scenario: A very long conversation trims old messages around a steered exchange
- **Setup**: A conversation long enough to trigger the existing message-count
  trimming behavior, with a steered exchange positioned near the trim
  boundary.
- **Action**: Continue the conversation until trimming occurs.
- **Expected outcome**: No crash, no orphaned/mismatched half of a steered
  exchange, no console error — the trim behaves the same as for any other
  pair of adjacent messages.
- **Category**: Edge Case

---

## Assumptions

- The existing `pkg/agent/loop.go` steering-injection persistence ordering
  (confirmed correct by direct code reading in ADR §1.3) does not change
  during this work — this spec assumes the backend is out of scope, per the
  ADR's explicit ruling.
- The project's existing Vitest/Testing Library setup (already used by
  `src/store/chat.mid-turn-send.test.ts` and `ChatScreen.virtualization.test.tsx`)
  is sufficient for all non-live tests in this spec; no new test tooling is
  introduced.
- GitNexus was unavailable this session; the manual codebase context above is
  treated as equivalent-confidence evidence since it is the same evidence base
  the accepted ADR and its grill-spec review were built on.
- `closedBySteer` (like several pre-existing internal-only `ChatMessage` fields,
  e.g. `errorDetail`, `mergedReplayIds`) is assumed to never need explicit
  wire-serialization testing — it follows an established, unenforced-by-test
  convention already shared by those sibling fields (grill-spec round 1 on
  this spec, OBS-002 — accepted as a pre-existing pattern, not a new gap).
- No test exists proving `closedBySteer` cannot leak state across an unrelated
  later turn; reading `chat.ts` shows no code path reuses a bubble across
  turns in a way this could matter, so this is assumed a non-issue rather than
  independently verified (grill-spec round 1 on this spec, OBS-004 — accepted
  as a low-probability, low-cost-if-wrong gap given the belt-and-braces
  coverage already present elsewhere in this spec).

## Grill-spec review log (this spec)

- **Round 1** (2026-08-24, background `/grill-spec` run against this spec +
  current source): verdict **REVISE**. 1 MAJOR (MAJ-001: regression table
  falsely claimed `chat.mid-turn-send.test.ts:117` was unaffected — it needed
  to move to the REWRITE list), 4 MINOR (MIN-001 traceability under-citation,
  MIN-002 missing `ChatScreen.mid-turn-send.test.tsx` regression row, MIN-003
  FR-008 modal-verb inconsistency, MIN-004 redundant N=3 dataset row), 4
  OBSERVATION (SC-002 wording, wire-serialization test gap, telemetry
  statement, cross-turn-leak test gap). All addressed above. Full review
  preserved at
  `docs/internal/specs/mid-turn-steering-message-ordering-spec-2026-08-24-review.md`.
- **Round 2** (2026-08-24, background `/grill-spec` run against the round-1-fixed
  spec + current source): verdict **REVISE**. Part A independently re-verified
  every round-1 fix (MAJ-001, SC-003 consistency, Traceability Matrix
  additions, all underlying engineering citations) and confirmed all correct
  — nothing to redo. Part B, a fresh adversarial pass, found 2 new MAJOR
  findings neither ADR-070 nor round 1 had caught: **NEW-001**
  (`markLastMessageInterrupted` mislabels a `closedBySteer` bubble as
  `'interrupted'` if Stop/Escape/`/cancel` fires in the steer→next-frame gap)
  and **NEW-002** (the ARIA "New response" announcement fires twice per
  steered turn — once prematurely at steer-time). Both addressed: ADR §2.7
  (new guarded call sites), spec FR-009/FR-010, two new BDD scenarios, two
  new tests (#20/#21), Symbols Involved / Impact Assessment / Regression
  table rows added above. Full review appended to
  `docs/internal/specs/mid-turn-steering-message-ordering-spec-2026-08-24-review.md`
  as its own "Round 2 Review" section.
- Grilling requirement (goal: "grilling twice") is now satisfied — both
  rounds landed real, fixed findings; proceeding to implementation.

## Clarifications

### 2026-08-24

- Q: Should the closed pre-follow-up reply segment carry a new, visibly
  different status, or reuse the existing "finished" status? -> A: Operator
  asked for internal distinguishability only, no visible difference required.
  Grill-spec round 1 (F1) found the originally-proposed "new status value"
  design had an undisclosed, silently-wrong UI consequence (Copy-button
  eligibility). Resolved: reuse the existing "finished" status for all visible
  behavior, add a separate internal-only marker not connected to any UI
  branch (ADR §2.4).
- Q: What should the live UAT step use to create a reliable mid-turn window?
  -> A: A live provider key, if one is configured in the environment; explicit
  fallback to scripted WebSocket frames only if none is available, stated
  openly rather than silently substituted (ADR §5.2).
- Q: Is anything else in the mid-turn-steering feature in scope for this fix?
  -> A: No — ordering only, across the live path, its self-healing render
  side-effect, and the reload/replay path, plus grill-spec F2's extension of
  the live-path fix to two additional frame handlers that share the same
  defect pattern (not new scope — the same named mechanism, completely
  implemented) (ADR §5.3).
