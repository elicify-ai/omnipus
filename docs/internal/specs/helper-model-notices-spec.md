`Status: In review`

`ADR: none — no open design decision` (the founder chose option A on 2026-11-15; the one contract-shape choice left is listed in Ambiguity Warnings #1 for the architect to confirm at the contract step)

# Feature Specification: Helper model notices reach the helper's chat and a named line in the main chat

**Created**: 2026-11-15
**Input**: founder decision Q1 A on #1537 (relayed by the chief in #cross-team, 2026-11-15 14:59Z); probe result `/workspace/logs/1537/RESULT-1537.md` (shared page "#1537 helper agent model notices — probe result"); tester report `/workspace/logs/1537/tester/report.md`; validator report `/workspace/logs/1537/validator/report.md` (VALIDATOR VERDICT: CONFIRMED). No interview-me file exists for this issue: the founder's input is the A/B/C/D options page and the one-line answer "Q1 A".
**Epic**: none set (#1537 has no milestone; labels `priority:P2-medium`, `status:parked`). Related open issue: #1280 (provider/network failures: classify, retry, show the retry state).
**Riders**: #1850 "Delegated agents: model-delay notices overwrite each other in one chat" (same frames, same browser slots; folded in as User Story 4). Not a rider: #1851 "Delegated agents: a helper's model retry is never announced to chat channels" (channels stay as they are; see Non-Behaviors).
**Agent tool field table**: no agent-managed object changed.

---

## Overview

A helper is an agent that another agent (the main agent) hands work to. It runs in its own chat. When a helper's model is slow or refuses for a moment, Omnipus has three notices: "the model is rate-limiting, retrying in 0:10" (retry), "the model hasn't responded for 31s, still waiting" (silence), and "connection came back after 1 retry" (recovered).

Today these notices for a helper go only to the **main** chat. They are drawn there only while the main agent is still writing its own reply, and they don't say which agent they are about. In the common case, the main agent hands off the work and finishes its reply. Then **nobody sees the notice**: the main chat is idle, and the helper's own chat shows only "Waiting for response…". Both lanes of the 15 Nov probe proved this on a scripted fake model.

The founder chose option A. A helper's notices go to the **helper's own chat**, where the waiting happens, just as the "switched to the fallback model" note already does. The main chat also shows a short line that **names the helper** ("Researcher: …"). That line is shown whether or not the main agent is still writing. The rider #1850 is folded in: a helper's notice must never overwrite or clear the main agent's own notice, and two helpers' notices must never overwrite each other.

## Existing Codebase Context

GitNexus was not available on this pod. The facts below come from ripgrep and reading the release tree at `f408d81c7`, cross-checked against the probe lanes' code citations.

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `pkg/agent/provider_retry_plan.go::providerRetryObserver` | modifies | Builds the `provider_retry` frame with `SessionID: rt.ts.routingSessionID`, which is the root chat for a helper turn |
| `pkg/agent/provider_retry_plan.go::publishRetryNotice` | reads, unchanged | Channel retry notice; `audienceFor(..., steer.BoundaryRetryNotice, transcriptSessionID)` suppresses it for steered helpers (that is #1851, out of scope) |
| `pkg/agent/provider_status.go::emitProviderStatus` | modifies | `provider_status` waiting / responding / recovered, stamped with `routingSessionID` |
| `pkg/agent/provider_status.go::startProviderSilenceWatch` / `noteProviderRecovered` | calls | The 30 s silence watch and the recovered note feed `emitProviderStatus` |
| `pkg/agent/fallback_note.go::queueProviderFallbackNote` | precedent | The fallback note already goes to `transcriptSessionID`, the helper's own chat, and is saved there |
| `src/store/chat/slices/provider-frames.ts::handleProviderFrame` | modifies | One `providerRetryEvent` slot and one `providerStatusEvent` slot per session bucket. Any newer frame replaces the slot, and status frames clear the retry slot. This is the #1850 mechanism |
| `src/components/chat/ChatScreen.tsx` (the provider notice block above the composer) | modifies | `ProviderRetryIndicator` and the silence notice mount only when `isStreaming`. This is why an idle main chat shows nothing |
| `src/hooks/useRunningActivity.ts` | calls | Already turns a helper's `agent_id` into its display name for the Activity bar; falls back to `'Unknown agent'` |
| `src/components/chat/ActivityBar.tsx` | reads, unchanged | The existing idle-visible helper surface below the composer (pill and panel) |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|----------------|------------|----------------|----------------|
| `providerRetryObserver` | MEDIUM | fallback-chain retry and the #1664 no-response retry (both emit through it) | every chat that shows a retry countdown |
| `emitProviderStatus` | MEDIUM | silence watch, recovered note | silence notice and recovered line in every chat |
| `handleProviderFrame` | **HIGH** | every `provider_retry` / `provider_status` frame for every chat; turn-end clearing | `ChatScreen` above-composer notices, the #1478/#1280/#1664 specs' tests |
| `ChatScreen` notice area | MEDIUM | the composer region layout | the design-system screenshot baselines if the region changes |

**HIGH row:** `handleProviderFrame` carries every provider notice. The chief must sign off before GREEN starts. The main agent's own notices must behave exactly as today (Regression Test Requirements below).

---

## Contract Changes (contract-first — Hard Constraint #8)

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| ProviderRetryFrame | changed (additive) | `contracts/components/schemas/ProviderRetryFrame.yaml` | Two new optional properties: `child_session_id` (string, 1–255) and `agent_id` (string, 1–255). Both are present only on the **main-chat copy** of a helper's notice and name the helper whose model call this is. Absent on a frame about the session's own turn, and absent on the copy sent to the helper's own chat |
| ProviderStatusFrame | changed (additive) | `contracts/components/schemas/ProviderStatusFrame.yaml` | The same two optional properties, with the same meaning |
| `contracts/asyncapi.yaml` | changed | message descriptions for `provider_retry` / `provider_status` | Says that a helper's notice is published twice: once to the helper's chat (no child fields) and once to the main chat (child fields set) |

Extends only these two schemas. It must not collide with:
- `SubagentStartFrame` / `SubagentStateFrame`: same names, same meaning. `child_session_id` is the same value those frames carry for the same helper.
- `ProviderFallbackFrame`: unchanged. It already goes to the helper's chat.

`turn_id` on the main-chat copy stays the helper's turn id, so the main agent's turn-end frame cannot clear it by accident.

## API and Data

- No REST endpoint changes.
- WebSocket: `provider_retry` and `provider_status` gain the publication rule above. Both copies are sequenced by their own chat's per-session hub, as today.
- Nothing new is stored. As today, the retry, silence and recovered notices are live only and are not saved to any transcript (see Non-Behaviors).

---

## User Stories & Acceptance Criteria

### User Story 1 — See a helper's model trouble in the helper's own chat (Priority: P1)

A user opens a helper's chat to see why it is taking long. Today that chat shows only "Waiting for response…", even while the helper's model is rate-limited or silent. With this story, the helper's chat shows the same retry countdown, silence notice and recovered line that a normal chat shows for its own model.

**Why this priority**: the founder chose it, and the waiting happens in that chat. The user guide already promises it ("A helper's own chat shows the same line for the helper's requests", `docs/troubleshooting.md`, retry section); today that promise is false.

**Independent Test**: scripted fake model; the helper's first model call gets HTTP 429 with a 10-second wait, the second gets 200. Open the helper's chat during the wait.

**Acceptance Scenarios**:

1. **Given** a helper's model call was refused with "too many requests" and a retry is scheduled, **When** the user views the helper's chat, **Then** the helper's chat shows the retry line with the provider, a countdown and "attempt 2 of N".
2. **Given** a helper's model has sent nothing for 30 seconds, **When** the user views the helper's chat, **Then** the helper's chat shows the silence notice.
3. **Given** a helper's retried call succeeded, **When** the user views the helper's chat, **Then** the helper's chat shows the recovered line with the retry count.
4. **Given** the helper's model answers again after a silence notice, **When** the answer starts arriving, **Then** the silence notice disappears from the helper's chat.

### User Story 2 — See a named line in the main chat, also when it is idle (Priority: P1)

The user stays in the main chat after the main agent has handed off the work and finished its reply. When a helper's model is rate-limited or silent, the main chat shows a short line that starts with the helper's name, so the user knows which agent is waiting and that nothing is broken.

**Why this priority**: this is the "nobody sees it" case from the probe, and the most common one.

**Independent Test**: same scripted 429-then-200 and 35-second-silence helper runs, with the main agent's reply already finished; watch the main chat only.

**Acceptance Scenarios**:

1. **Given** the main agent has finished its reply and its helper's model call is waiting on a retry, **When** the user looks at the main chat, **Then** a line starting with the helper's name shows the same retry wording and countdown the helper's chat shows.
2. **Given** the main agent is still writing its reply and its helper's model call is waiting on a retry, **When** the user looks at the main chat, **Then** the same named line is shown, and it is a separate line from the main agent's own notices.
3. **Given** a helper's model has been silent for 30 seconds while the main chat is idle, **When** the user looks at the main chat, **Then** a line starting with the helper's name shows the silence wording.
4. **Given** a named line for a helper is shown, **When** that helper's retried call succeeds, **Then** the line changes to the helper's named recovered line.
5. **Given** a named line for a helper is shown, **When** that helper's work ends (finished, failed or stopped), **Then** the named line disappears from the main chat.
6. **Given** a named silence line is shown, **When** the helper's model starts answering again, **Then** the named line disappears.

### User Story 3 — A screen reader announces which agent is waiting (Priority: P2)

A user with a screen reader hears the helper's name with the notice, instead of a sentence that sounds like the main agent is stuck.

**Why this priority**: the same wording risk as User Story 2 for screen-reader users; the probe flagged it (Inferred).

**Independent Test**: accessibility check on the main chat with a named line shown: the line is in a polite live region and its accessible text starts with the helper's name.

**Acceptance Scenarios**:

1. **Given** a named line appears in the main chat, **When** a screen reader is running, **Then** it announces the line once, politely, starting with the helper's name.
2. **Given** a named retry line is counting down, **When** each second passes, **Then** the screen reader does not announce every tick.

### User Story 4 — Notices never overwrite each other (rider #1850) (Priority: P2)

The main agent and its helpers can all wait on a model at the same time. Each one's notice stays its own: a helper's "recovered" never clears the main agent's own retry countdown, and two helpers never replace each other's line.

**Why this priority**: today a helper notice in the main chat can wipe the main agent's own notice (Inferred from code; #1850). The named line would make this visible, so it ships together.

**Independent Test**: scripted run where the main agent's own call and two helpers' calls are rate-limited at overlapping times.

**Acceptance Scenarios**:

1. **Given** the main agent's own retry countdown is shown, **When** a helper's retried call succeeds, **Then** the main agent's own countdown is still shown, unchanged.
2. **Given** helper "Researcher" has a named retry line, **When** helper "Writer" gets a silence notice, **Then** both named lines are shown.
3. **Given** two helpers' named lines are shown, **When** one helper's work ends, **Then** only that helper's line disappears.

### Edge Cases

- **Helper name cannot be found** (the agent was deleted while its helper ran): the line starts with "Helper" instead of a name. Expected: line shown, never an agent id or a session id.
- **Same helper agent, two helpers at once** (two copies of one agent): each copy has its own line. Both start with the same name. Expected: two lines, matched by helper chat, not by name.
- **More than three helpers affected at once**: show the three earliest named lines and one summary line "and N more helpers are waiting on their model". Expected: never more than four lines in the area. (Copy subject to omnipus-voice; see Ambiguity Warnings #3.)
- **Helper of a helper** (an agent that is allowed to delegate further, unlike Omnipus itself): the named line appears in the top chat (the chat the user started) and names the agent whose model is affected. The middle helper's chat is not changed by this spec. Not tested by the 15 Nov probe; covered by one scripted row (UAT-H7).
- **Main chat opened or reloaded while a helper is still waiting**: the named line is shown within 2 seconds of the chat finishing loading, with the countdown still right.
- **Helper chat opened after the notice was sent**: same as a normal chat that attaches late. The current notice is shown with the right countdown.
- **Retry wait ends while the line is shown, and no new notice comes**: the countdown stops at 0:00 and the line stays until the next notice or the helper's end (same as the main agent's own retry line today).
- **The main agent's own retry and a helper's line at the same time**: both shown; the main agent's own line keeps its current position and wording, and named lines come below it.
- **Old browser tab without this change**: it ignores the two unknown fields and treats the main-chat copy like today's frame. That is today's behaviour, no worse.

---

## UI Screens and States

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Helper's chat, notice area above the composer | chat loading as today | no notice: nothing drawn | retry line (countdown) or silence notice | — | recovered line; notices clear when the model answers |
| Main chat, named helper lines (new; above the composer, below the main agent's own notices) | chat loading: lines appear within 2 s after load if a helper is still waiting | no affected helper: nothing drawn, no empty state | one line per affected helper: "{Name}: {retry or silence wording}" | more than 3: three lines plus "and N more…" | "{Name}: {recovered wording}" until the helper ends |
| Main chat, the main agent's own notices | unchanged | unchanged | unchanged | unchanged | unchanged |

## User Journey

1. The user asks the main agent for real work. The main agent starts a helper and finishes its own reply (main chat idle).
2. The helper's model is rate-limited. Main chat: the line "Researcher: openrouter is rate-limiting requests right now. Retrying automatically in 0:10 (attempt 2 of 6)." appears (US2-1).
3. The user clicks into the helper's chat (Activity bar or sidebar). Helper chat: the same retry line without the name prefix (US1-1).
4. The retry works. Both chats show the recovered line; the main chat's line is named (US1-3, US2-4).
5. The helper finishes. The main chat's named line disappears (US2-5); the helper's report arrives in the main chat as today.

## Accessibility and Keyboard

- The named lines are text only: no new focusable control, no change to tab order, and no dismiss control (same as the main agent's own notices: Stop is the only early exit, and it stays on the helper's own chat).
- The named-line area is one `role="status"` (polite) live region. A new line or a changed state is announced once. Countdown ticks are not announced (US3-2).
- Accessible text starts with the helper's name, the same as the visible text.
- Focus ring rules are unchanged (no new focusable element).
- Reflow: each line wraps at 320 CSS px; no horizontal scroll.

## Design-System Components

- Helper's chat: the existing `ProviderRetryIndicator` and the existing silence/recovered notice, unchanged.
- Main chat named lines: the same two components with a name prefix. No new catalogued component. If the prefix needs a prop on a catalogued component, that change goes through the catalog's manifest (design-system rule 9); `ProviderRetryIndicator` lives in `src/components/chat/`, not `src/components/ui/`, so no catalog entry is expected. Checked against `design-system/catalog.json`: no existing "named notice" component.
- Colours, spacing and type are tokens only (design-system locks).

## Security and User Promises

- No tool access, approval or shell-rule change. No focus-area package is touched.
- The frames stay built from named fields only, never provider text (existing ProviderStatusFrame rule). The new fields carry ids, not text. The display name comes from the browser's own agent list, not from the wire.
- A helper's notice is published only to the helper's chat and the chat the user started. The steered-helper rule for **channels** (`BoundaryRetryNotice`) is not changed.
- Security review: once, on the fresh implementation (team rule); not after fix rounds.

---

## Behavioral Contract

Primary flows:
- When a helper's model call is retried, the helper's chat shows the retry line, and the main chat shows the same line starting with the helper's name, whether or not the main agent is writing.
- When a helper's model has been silent for 30 seconds, the helper's chat shows the silence notice, and the main chat shows it named.
- When a helper's retried call succeeds, both chats show the recovered line (named in the main chat).
- When the helper's model answers again after a silence notice, the notice clears in both chats.
- When a helper's work ends, its named line leaves the main chat.

Error flows:
- When the helper's name cannot be found, the line starts with "Helper".
- When the browser reconnects or reloads during a helper's wait, the current notice comes back in both chats with the right countdown.

Boundary conditions:
- At 29 seconds of helper silence no silence notice is shown; at 30 seconds it is (existing threshold, unchanged).
- With three helpers affected, three named lines; with four, three lines plus one summary line.
- The main agent's own notices look and behave exactly as before.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not change the main agent's own retry, silence or recovered notices (wording, position, the "only while writing" rule) because the founder's decision covers helpers only.
- The system must not let a helper's notice replace, clear or restart the main agent's own notice, or another helper's line (#1850).
- The system must not send a helper's notice to chat channels (Telegram and similar) because #1851 is a separate open question.
- The system must not save retry, silence or recovered notices to any transcript. They stay live only, as today; option A did not ask for saving, and saving would change reload behaviour and history for every chat.
- The system must not put provider error text, agent ids or session ids into any visible line.
- The system must not add a dismiss button or a Stop button to the named lines in the main chat. Stopping a helper stays where it is today (Activity panel and the helper's chat).
- The system must not change the "fallback model" note (`provider_fallback`). It already goes to the helper's chat.

### Machine-Verifiable Constraints

**Wire**:
- A helper turn's `provider_retry` / `provider_status` frame is published with `session_id` = the helper's session, without `child_session_id` / `agent_id`.
- The main-chat copy has `session_id` = the chat the user started, `child_session_id` = the helper's session, and `agent_id` = the helper's agent, with every other field byte-equal to the helper copy except `seq`.
- A frame about a chat's own turn never carries `child_session_id` or `agent_id`.
- Both copies validate against `ProviderRetryFrame.yaml` / `ProviderStatusFrame.yaml`.

**Timing**: the named line is visible within 2 s of the main-chat copy arriving; after a reload during the wait, within 2 s of the chat finishing loading.

**Count**: at most 4 named-line rows (3 lines plus 1 summary) at any time.

**Scope**: no change to `ProviderFallbackFrame`, channel messages or transcripts.

## Integration Boundaries

### Model provider (fake in tests)

- **Data in / out**: the helper's model calls; HTTP 429 with Retry-After, or a delayed or silent stream.
- **Contract**: OpenAI-compatible chat completions.
- **On failure**: the existing retry plan and silence watch; this spec changes only where the notices are addressed.
- **Development**: scripted fake model (`/workspace/logs/1537/recipe/`, `provider.cjs`): deterministic 429/200 and silence scripts, and no paid key.

### Gateway to browser (WebSocket)

- **Data in / out**: `provider_retry` / `provider_status`, two copies for a helper turn.
- **Contract**: `contracts/asyncapi.yaml`, the two schemas above.
- **On failure**: a dropped socket recovers through the existing per-session catch-up; each copy is sequenced in its own chat.
- **Development**: real gateway, isolated instance (`--allow-empty --sandbox=off`), never the live instance.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|------------------|------------------------|---------------------|
| 1 | Wire shape for the main-chat copy: two optional fields on the existing frames (proposed here) or a new frame type | Add the fields (the smallest additive change; old tabs behave as today) | Architect confirms at the contract step, before RED. If a new frame type is chosen, only the Contract Changes section and the FR-002 test names change |
| 2 | Reload during a helper's wait: catch-up may not replay a notice whose main-chat turn already ended | The implementer drops reload support as "too hard" | Kept as a MUST (FR-008). If the architect finds it needs a new snapshot field, raise it to the chief before GREEN rather than dropping it |
| 3 | Exact wording of the name prefix ("Researcher: …"), the "Helper" fallback and the "and N more…" summary line | Copied from this spec without voice review | omnipus-voice review of the three strings at GREEN; the oracle is "starts with the helper's name, then the same sentence the helper's chat shows" |
| 4 | Helper of a helper: only the top chat gets the named line | The middle chat also gets one | Accepted scope for this spec (Omnipus helpers cannot delegate further); raise again only if UAT-H7 shows confusion |

---

## BDD Scenarios

### Feature: Helper model notices

#### Scenario: Helper's chat shows the retry line after a 429

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the fake model answers the helper's first call with HTTP 429 and `Retry-After: 10`, and its second call with 200
- **When** the user opens the helper's chat during the wait
- **Then** the helper's chat shows the retry line with the provider's name, a countdown at most 0:10, and "attempt 2 of" the configured total
- **But** the line does not start with a helper name

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

#### Scenario Outline: Named retry line in the main chat, idle or writing

**Traces to**: User Story 2, Acceptance Scenarios 1 and 2
**Category**: Happy Path

- **Given** the main agent is `<main state>` and its helper "Researcher" gets the 429-then-200 script
- **When** the retry is scheduled
- **Then** the main chat shows a line starting with "Researcher" followed by the same retry wording and countdown the helper's chat shows
- **And** the main agent's own notice area is `<own notice>`

| main state | own notice |
|---|---|
| finished its reply (idle) | empty |
| still writing (fake model streams slowly for 60 s) | empty (the main agent's own model is fine) |

#### Scenario: Named silence line in an idle main chat

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the main agent finished its reply and helper "Researcher" gets the 35-second silence script
- **When** 30 seconds of silence have passed
- **Then** the main chat shows a line starting with "Researcher" followed by the silence wording

#### Scenario: Named line turns into the recovered line

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the main chat shows Researcher's named retry line
- **When** Researcher's second call succeeds
- **Then** the main chat shows a line starting with "Researcher" followed by the recovered wording with 1 retry

#### Scenario Outline: Named line leaves when the helper ends

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** the main chat shows Researcher's named line
- **When** Researcher's work `<ends how>`
- **Then** Researcher's named line is gone from the main chat

| ends how |
|---|
| finishes |
| fails (fake model returns 500 on every call until the retries run out) |
| is stopped by the user |

#### Scenario: Named silence line clears when the helper's model answers

**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** the main chat shows Researcher's named silence line
- **When** the fake model starts streaming Researcher's answer
- **Then** the named line is gone from the main chat

#### Scenario: Screen reader hears the helper's name once

**Traces to**: User Story 3, Acceptance Scenarios 1 and 2
**Category**: Happy Path

- **Given** the main chat is idle
- **When** Researcher's named retry line appears
- **Then** the polite live region's text starts with "Researcher"
- **And** the live region's text does not change on countdown ticks

#### Scenario: Helper's recovered line does not clear the main agent's own countdown

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Edge Case

- **Given** the main agent's own call got 429 with `Retry-After: 20` and its countdown is shown
- **When** helper Researcher's retried call succeeds at second 5
- **Then** the main agent's own countdown is still shown, still counting down from the same retry time
- **And** Researcher's named recovered line is shown below it

#### Scenario: Two helpers keep their own lines

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Edge Case

- **Given** Researcher has a named retry line
- **When** Writer reaches 30 seconds of silence
- **Then** the main chat shows both "Researcher …" and "Writer …" lines

#### Scenario: One helper ending leaves the other's line

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Edge Case

- **Given** Researcher and Writer both have named lines
- **When** Writer finishes
- **Then** only Researcher's line is shown

#### Scenario: Unknown helper name

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Error Path

- **Given** the helper's agent is not in the browser's agent list
- **When** its named retry line appears
- **Then** the line starts with "Helper"
- **But** it shows no agent id and no session id

#### Scenario: Reload during the wait

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Error Path

- **Given** Researcher's named retry line is shown with `Retry-After: 30`
- **When** the user reloads the main chat at second 5
- **Then** within 2 s of the chat loading, Researcher's named line is back with a countdown of at most 0:25

#### Scenario: More than three helpers

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Edge Case

- **Given** four helpers are each waiting on a retry
- **When** the fourth helper's notice arrives
- **Then** the main chat shows three named lines and one line "and 1 more helper is waiting on its model"

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit (Go) | `pkg/agent` notice addressing | Helper turn publishes two copies with the right session ids and fields; own turn publishes one copy without the fields |
| Unit (SPA) | `src/store/chat/slices/provider-frames.ts`, the main chat's named-line area | Named lines keyed per helper chat; own slots untouched; shown without streaming; clear on helper end |
| Contract | schema validation | Both copies validate; old frames still validate |
| E2E (stubs shard) | real gateway + SPA + scripted fake model | US1–US4 against the real browser, no paid key |
| Hands-on UAT | tester and independent validator | The probe re-run on the fixed build |

### Hands-on UAT rows (written at spec time)

Setup for every row: the scripted fake-model instance from `/workspace/logs/1537/recipe/RECIPE.md` (isolated gateway, `sk-test-placeholder`, two agents in `core_team`, delegation edge), the build under test stamped with its commit, and Chromium 1228 on a runner. The tester and the validator run on different models.

| Row | Acceptance criterion | Steps | Expected result | Affected area | Validator critical path | Automation candidate |
|---|---|---|---|---|---|---|
| UAT-H1 | US1-1, US1-3 | Main agent delegates; helper gets 429 (Retry-After 10) then 200; open the helper's chat during the wait | Helper chat: retry line, then the recovered line; frame log shows `session_id` = helper | backend addressing, helper chat | Re-drive and check the frame log plus a screenshot of the helper chat | yes: e2e "helper chat shows retry and recovered" |
| UAT-H2 | US2-1, US2-4, US2-5 | Same script; main agent finishes its reply first; stay in the main chat | Named line "{helper}: …" with countdown, then the named recovered line, then gone after the helper finishes | main chat named lines | Screenshots at wait, recovered, end | yes |
| UAT-H3 | US2-2 | Same script; main agent's own model streams slowly for 60 s | Named line shown while the main agent writes; the main agent's own notice area empty | main chat while writing | Screenshot + frame log | yes |
| UAT-H4 | US1-2, US2-3, US2-6 | Helper silent 35 s then answers; main chat idle | Silence notice in the helper chat; named silence line in the main chat at ≥30 s; both clear on the answer | silence watch | Screenshots at 29 s and 31 s | yes |
| UAT-H5 | US4-1 | Main agent's own call 429 (Retry-After 20); helper recovers at second 5 | Main agent's own countdown unchanged; named recovered line below it | #1850 | Screenshot + DOM check of both lines | yes |
| UAT-H6 | US4-2, US4-3 | Two helpers: one 429, one silent; then one finishes | Two named lines; one removed on finish | #1850 | Screenshot sequence | yes |
| UAT-H7 | Edge: helper of a helper | Agent allowed to delegate further; grandchild gets 429 | Named line in the top chat names the grandchild's agent; middle chat unchanged | nested delegation | Screenshot of the top and middle chats | later: needs a delegating worker setup |
| UAT-H8 | Edge: reload | Retry-After 30; reload the main chat at second 5 | Named line back within 2 s, countdown ≤ 0:25 | catch-up | Screenshot after reload | yes |
| UAT-H9 | US3 | Screen-reader check (axe + live-region text) on UAT-H2's main chat | Live region starts with the name; no per-tick change | accessibility | axe report + live-region text log | yes |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `TestHelperProviderRetryPublishesToHelperAndNamedRootCopy` (placeholder) | Unit (Go) | Named retry line in the main chat; Helper's chat shows the retry line | Two copies, right ids, fields byte-equal except seq |
| 2 | `TestHelperProviderStatusPublishesToHelperAndNamedRootCopy` (placeholder) | Unit (Go) | Named silence line; Helper's chat silence; recovered | Same for waiting / responding / recovered |
| 3 | `TestOwnTurnProviderFramesCarryNoChildFields` (placeholder) | Unit (Go) | Recovered line does not clear the main agent's own countdown | Regression: the root's own frames unchanged |
| 4 | contract fixture test for both schemas (placeholder) | Contract | all wire scenarios | Both copies validate; unknown fields rejected as before |
| 5 | `provider-frames.helper-lines.test.ts` (placeholder) | Unit (SPA) | Two helpers keep their own lines; one helper ending; recovered does not clear own | Per-helper keying; own slots untouched |
| 6 | `ChatScreen.helper-named-lines.test.tsx` (placeholder) | Unit (SPA) | Named line idle/writing; unknown name; more than three; screen reader | Render without streaming; prefix; cap; live region |
| 7 | `tests/e2e/helper-model-notices.spec.ts` (placeholder) | E2E (stubs shard) | all US1–US4 scenarios except helper of a helper | Scripted fake model, real gateway, both chat states, reload |

### Test Datasets

#### Dataset: Fake-model scripts for the helper's call

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 429, Retry-After 10, then 200 | happy | retry line then recovered (1 retry) in both chats | Named retry line; recovered | probe run-03 shape |
| 2 | 429, Retry-After 1, then 200 | min wait | countdown starts at 0:01; recovered | Named retry line | smallest wait the frame allows (≥1 s) |
| 3 | 429, Retry-After 300, then 200 | over the cap | countdown starts at most 2:00 | Named retry line | the frame caps the honoured wait at 120 s |
| 4 | silent 29 s, then answer | below threshold | no silence notice anywhere | Named silence line | boundary |
| 5 | silent 35 s, then answer | above threshold | silence notice at ≥30 s in both chats, cleared on answer | Named silence line; clears | |
| 6 | 500 on every call | error | helper fails; named line removed when the helper ends | Named line leaves when the helper ends (fails) | |
| 7 | 429 (Retry-After 60), user stops helper at second 5 | cancel | named line removed | Named line leaves (stopped) | |

#### Dataset: Main chat composition

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 0 helpers affected | empty | no named line, nothing drawn | Named retry line (idle) | |
| 2 | 1 helper | single | 1 named line | Named retry line | |
| 3 | 3 helpers | at the cap | 3 named lines, no summary | More than three helpers | |
| 4 | 4 helpers | over the cap | 3 named lines + "and 1 more helper…" | More than three helpers | |
| 5 | main agent's own retry + 1 helper recovered | overlap | own countdown unchanged + 1 named line | Recovered does not clear own | #1850 |
| 6 | helper agent missing from the agent list | error | line starts with "Helper" | Unknown helper name | |
| 7 | two copies of the same agent | duplicate name | 2 lines, same name, independent | Two helpers keep their own lines | keyed by helper chat |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Main agent's own retry countdown shown only while it writes | ChatScreen provider-retry tests (#1280/#1664) | Yes: order 3 and order 5 assert the own slot is unchanged | must stay green unmodified |
| Main agent's own silence notice at 30 s | provider_status tests (#1478) | No: existing tests cover it; must stay green unmodified | |
| Fallback note goes to the helper's chat and is saved | `pkg/agent` fallback note tests | No | unchanged |
| Channel retry notice suppressed for steered helpers | `pkg/agent/steer_boundary_containment_test.go` (BoundaryRetryNotice) | No: must stay green | #1851 out of scope |

---

## Functional Requirements

- **FR-001**: System MUST publish a helper turn's `provider_retry` and `provider_status` frames to the helper's own chat, without `child_session_id` / `agent_id`.
- **FR-002**: System MUST also publish a copy of each such frame to the chat the user started, with `child_session_id` and `agent_id` set and all other fields equal except `seq`.
- **FR-003**: System MUST NOT set `child_session_id` / `agent_id` on frames about a chat's own turn, and MUST NOT change how those frames are shown.
- **FR-004**: The helper's chat MUST show the retry line, silence notice and recovered line for the helper's model calls, with the same rules a chat uses for its own model calls.
- **FR-005**: The main chat MUST show one line per affected helper, starting with the helper's display name (or "Helper" when unknown), followed by the same wording the helper's chat shows, whether or not the main agent is writing.
- **FR-006**: A named line MUST update on the same helper's next notice, clear when that helper's model answers again after silence, and disappear when that helper's work ends (finished, failed or stopped).
- **FR-007**: A helper's notice MUST NOT replace, clear or restart the main agent's own notice or another helper's named line.
- **FR-008**: After a reload or reconnect during a helper's wait, both chats MUST show the current notice within 2 s of loading, with the countdown measured from the original retry time.
- **FR-009**: The main chat MUST show at most three named lines plus one summary line counting the rest.
- **FR-010**: The named lines MUST be in one polite live region whose text starts with the helper's name and does not change on countdown ticks.
- **FR-011**: System MUST NOT save retry, silence or recovered notices to any transcript, and MUST NOT change channel retry messages.

## Success Criteria

- **SC-001**: In the scripted 429-then-200 run with the main chat idle, the main chat shows a named line within 2 s of the retry being scheduled (today: nothing).
- **SC-002**: In the same run, the helper's chat shows the retry line (today: only "Waiting for response…").
- **SC-003**: The existing own-notice tests for #1280, #1478 and #1664 pass unmodified.
- **SC-004**: In the overlap run (UAT-H5), the main agent's own countdown is still shown 1 s after the helper's recovered frame.
- **SC-005**: The e2e spec passes 3 runs in a row with retries=0 in its stubs shard.

---

## Reachability

- **Tool registration**: none, no new tool.
- **Screen**: the chat screen (`src/components/chat/ChatScreen.tsx`), in two places: the helper's chat (any helper chat opened from the Activity bar/panel or the sidebar) and the main chat's notice area above the composer. Every user who delegates reaches both.
- **Test plan execution**: RED: the qa-lead test author writes orders 1–7, all failing on the release head. GREEN: one backend developer (Go addressing, contract) and one frontend developer (store and screen) as a pair; orders 1–7 pass. CHECK: an independent auditor on a different model re-runs them and runs mutants (for example: the copy sent with root `session_id` only; the named line gated on streaming; per-helper key replaced by one slot). Then the hands-on rows UAT-H1 to UAT-H9 run with a tester and a validator on the fake-model instance.

## User-facing documentation

| TODO | UI / behaviour change | User-facing page | Matching update |
|---|---|---|---|
| DOC-001 | The helper's chat shows its own retry, silence and recovered notices | `docs/troubleshooting.md` (retry section and silence section) | Keep the sentence "A helper's own chat shows the same line for the helper's requests" (now true). Add that the silence notice and the recovered line also show in the helper's chat |
| DOC-002 | The main chat shows a named line per waiting helper, also when idle | `docs/troubleshooting.md` (same sections) and `docs/agents.md` ("Real work runs in helpers") | Say that the main chat shows "{helper name}: …" while a helper waits on its model, even after the main agent has finished replying; three lines at most plus "and N more" |
| DOC-003 | Notices stay live only; channels unchanged | `docs/troubleshooting.md` | One sentence: these notices are not saved and are not sent to chat channels such as Telegram (where the existing single "Retrying — please wait..." message applies only to the agent you talk to) |

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) | Documentation TODO(s) |
|---|---|---|---|---|
| FR-001 | US-1 | Helper's chat shows the retry line; silence; recovered | order 1, 2, 7; UAT-H1 | DOC-001 |
| FR-002 | US-2 | Named retry line (outline); Named silence line | order 1, 2, 4, 7; UAT-H2, H3 | DOC-002 |
| FR-003 | US-4 | Recovered line does not clear the main agent's own countdown | order 3, 5; UAT-H5 | none: no user-visible change (regression guard) |
| FR-004 | US-1 | Helper's chat retry; silence; recovered; silence clears | order 7; UAT-H1, H4 | DOC-001 |
| FR-005 | US-2 | Named retry line (outline); Named silence line; Unknown helper name | order 6, 7; UAT-H2, H3, H4 | DOC-002 |
| FR-006 | US-2 | Named line turns into recovered; Named line leaves (outline); Named silence line clears | order 5, 6, 7; UAT-H2, H4 | DOC-002 |
| FR-007 | US-4 | Recovered does not clear own; Two helpers; One helper ending | order 5, 7; UAT-H5, H6 | DOC-002 |
| FR-008 | US-2 | Reload during the wait | order 7; UAT-H8 | DOC-002 |
| FR-009 | US-4 | More than three helpers | order 6 | DOC-002 |
| FR-010 | US-3 | Screen reader hears the helper's name once | order 6; UAT-H9 | none: the visible text is already documented (DOC-002) |
| FR-011 | US-1, US-2 | Reload during the wait (nothing saved: the line comes back only while the helper still waits) | order 1, 2 (no transcript write), existing BoundaryRetryNotice test | DOC-003 |

**Completeness check**: every FR has at least one scenario and one test; every BDD scenario appears at least once; DOC-001 to DOC-003 cover every user-visible change.

---

## Assumptions

- The scripted fake model and isolated gateway from the 15 Nov probe (`/workspace/logs/1537/recipe/`) are the test base; no paid model key and never the live instance.
- The 30-second silence threshold, the 120-second retry-wait cap and the retry-attempt totals are unchanged (existing #1478/#1280/#1664 behaviour).
- `ProviderRetryIndicator` and the silence/recovered notice live in `src/components/chat/`, not in the design-system catalog.
- No section of the template was removed.

## Clarifications

### 2026-11-15

- Q: Where do a helper's retry, silence and recovered notices go? → A: Option A: to the helper's chat, plus a named line in the main chat, also when the main chat is idle (founder, relayed by the chief 14:59Z).
- Q: Is #1850 (notices overwriting each other) part of this work? → A: Yes, as a rider: the named line is keyed per helper, and the main agent's own slot is never touched (this spec, User Story 4; #1850 filed by the chief).
- Q: Do chat channels follow the web? → A: Not in this spec; #1851 tracks it separately (filed by the chief).
- Q: Should the notices be saved? → A: No. Option A did not ask for it; kept live only as today (FR-011). Can be raised as its own issue if wanted.
