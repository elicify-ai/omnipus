# #1449 Group chat reuses the normal chat — requirements (founder interview)

Status: Requirements — interview complete 2026-10-27; architecture decision (ADR) and spec pending
Issue: #1449 (related: #1068 messaging scope — out of scope here; #1461 presence awareness — split out)
Captured at: release/v0.1.1 75d48f041 (via feature/1452-followups 45e6c56df)
Interviewer: team-lead (navigation). Source: founder interview in the team-lead chat, 2026-10-27.

## 1. Problem

Group chats (team room, squads, ad-hoc groups; `src/components/rooms/`, `pkg/rooms`) are live (`ROOMS_ENABLED = true`) but use their own screen: plain-text transcript (`RoomTranscript.tsx`), own composer (`RoomComposer.tsx`), a member header with every member and status (`RoomHeader.tsx`), full-width layout, own store and paging (`src/store/rooms.ts`). Every chat feature and fix must be done twice, and the group chat lacks most display features of the normal chat. Founder decision 2026-10-22: one chat experience everywhere.

## 2. Founder requirements (founder's words where available)

| ID | Requirement |
|---|---|
| R1 | "in our regular chat we have markdown rendering, mermaid rendering and many things more that should be available in the group chat as well" |
| R2 | "image rendering and all the display features the normal chat has should the group chat have as well" |
| R3 | "the composer should almost look identically without auto and model selector etc." |
| R4 | "the participant list at the top is way too much; for the participants a side panel list should open on demand" |
| R5 | "the chat width goes over the full width; in the normal chat that is limited" |
| R6 | "it is really just adding participants into the chat but in a new session" |
| R7 | "we must reuse UI components, try to avoid reinventing" |
| R8 | "I do not want to see header borders or anything; we need to stick to our minimal design; that applies also to the breadcrumb to return to parent sessions and the add participant; it must be way more minimalistic and no borders; this affects the normal chats as well as the group chat" |
| R9 | "the existing header should stay on the left and not change location" |

## 3. Decisions log

| ID | Topic | Decision | Rationale | Source | Date |
|---|---|---|---|---|---|
| D1 | Approach | Option B: one chat screen (message list, composer, history behaviour) for normal and group chats; reuse existing UI components; group server storage (`pkg/rooms`) unchanged for now, read through an adapter | No double work; option C (full server merge) rejected as far larger and riskier (agent engine assumes one agent per session; agents answer groups from their own main chats) | Interview | 2026-10-27 |
| D2 | Display | Group messages render everything the normal chat renders: markdown, Mermaid, math, code + copy, tables, images + lightbox, files | R1/R2 | Interview | 2026-10-27 |
| D3 | Rich content | Both directions: user attachments into the group, and agents' images/files/diagrams in group answers (server change: group entries are text-only today, `RoomEntry.text`) | R2 | Interview | 2026-10-27 |
| D4 | Composer | Group composer = normal composer WITH attachments, Stop (stops the work of the addressed participants), slash menu reduced to group commands, @-mentions; WITHOUT model picker, auto-approve, token counter, skills | R3 | Interview | 2026-10-27 |
| D5 | Live view | Now: a per-agent "… is working" line, then the finished answer; live writing possibly later | Calm with several agents; small server change | Interview | 2026-10-27 |
| D6 | Participants | Header: group name + avatar stack (the stack + count is the Participants button). Side panel on demand (like Tasks/Team): status per member, link to the member's own chat, Ask again, mute/remove, Add participant, cost so far, invitation context | R4 | Interview | 2026-10-27 |
| D7 | Width | Same centred column as the normal chat (messages `max-w-4xl`, composer `max-w-3xl`) | R5 | Interview | 2026-10-27 |
| D8 | Approvals | Approval/question cards appear in the group ONLY when they carry THIS group's reference (`RoomRef` + "for group <name>" origin, `pkg/agent/participant.go`, `pkg/agent/tool_approver.go`); answer once, both places update. Mixed-origin turns (e.g. messaging channel + group: channel wins) and unrelated work stay in the agent's own chat. Helper (delegated) approvals: follow what the architect finds | Founder: "what if the agent has a task not related to the group chat — the card here does not make sense" | Interview | 2026-10-27 |
| D9 | Starting a group | "Add participant" in a 1:1 chat starts a NEW group chat; the 1:1 chat stays unchanged | R6 | Interview | 2026-10-27 |
| D10 | Invitation context | Invisible background summary written with the starting agent's model, sent with the invitation, never posted in the chat. Safeguards: same workspace only; never key/password values; viewable on demand in the participant panel as "Invitation context" | Founder accepted the safeguards (privacy) | Interview | 2026-10-27 |
| D11 | History | Same behaviour as the normal chat: paging, Return to latest, open-chat loading cover (#1266/#1452) | Follows from D1 | Interview | 2026-10-27 |
| D12 | Sidebar | Keep the existing group section under each workspace | Already exists (`WorkspaceGroupChats`) | Interview | 2026-10-27 |
| D13 | Minimal chrome (all chats) | No chat header bar and no borders. The existing workspace header line (workspace name + Tasks/Calendar/Library/Mail/Team) stays unchanged on the left; chat controls sit at the right end of the same line as icon-only ghost buttons with tooltips: normal chat → Add participant; helper chat → small muted breadcrumb "‹ Parent / Helper" (parent is the link; replaces the grey `FeedKindLabel` bar with its border); group chat → avatar stack + count as the Participants button. Wireframe approved by the founder ("the wireframe is excellent, I'm very happy with that") | R8/R9 | Interview | 2026-10-28 |
| D14 | Presence | Agents knowing sign-in/open chat, and sign-in/return triggers, are NOT part of #1449 — tracked as #1461 | Own feature | Interview | 2026-10-27 |
| D15 | Helper approvals | Approval/question cards from helpers an agent started for group work appear ONLY in that agent's main chat, not in the group. In the group, the agent's avatar shows the attention animation plus a link to its main chat. The ADR's proposal to show helper cards in the group is NOT adopted | Founder answer to ADR question Q1 | Interview | 2026-10-28 |
| D16 | Stop with mixed work | Stop in a group withdraws only the group request; other work in the same run (for example a Telegram message) continues | Founder answer to ADR question Q2 (ADR proposal accepted) | Interview | 2026-10-28 |
| D17 | Add participant placement | The Add participant icon appears in main agent chats only, as today | Founder answer to ADR question Q3 | Interview | 2026-10-28 |

## 4. Wireframe

Approved wireframe (design tokens from `src/styles/tokens.generated.css`, Phosphor icons as in the app, header strip as in `WorkspaceTabBar.tsx`): `docs/internal/design/1449/index.html`, screenshot `docs/internal/design/1449/wireframe-full.png`. Frames: 1 normal chat, 2 helper chat, 3 group chat, 4 group chat with the Participants panel. It is a demonstration, not production code.

## 5. Open design questions for the architect (ADR before the spec)

1. Adapter shape: how the normal list/composer/history stack (`ChatScreen`, `ChatMessageList`, `HistoryBoundary`, `useHistoryViewport`, `useChatHistory`, single-session `useChatStore`/`useSessionStore`) reads a group (room id, integer `seq` cursor, `getRoomHistory`, `attach_room` frames) without forking the components; whether `OmnipusComposer` must first be extracted from `ChatScreen.tsx` (2,961 lines).
2. Multi-author rows: per-message author attribution in the shared row components.
3. Rich content in group entries (D3): contract shape (Hard Constraint #8: contract-first), storage in `pkg/rooms` journals, mirroring into each member's main chat.
4. Approvals in the group (D8): delivery of group-referenced cards to the group view and answer-once semantics; whether helper/delegate approvals during group work carry the group reference today.
5. Stop in a group (D4): what exactly is stopped (the addressed members' room-request turns only).
6. Invitation context (D10): where it is generated, stored and shown; model cost attribution; never secrets.
7. Untrusted agent text: group text is plain today on purpose (FR-168, "input only, never runs as a command"); rendering through the normal markdown pipeline is display-only — confirm the same sanitisation as the normal chat.
8. Existing room tests (vitest `src/components/rooms/*`, `src/store/rooms.*`, e2e `tests/e2e/room-history-paging.spec.ts`, Go `pkg/rooms`, `pkg/gateway` room tests): which assertions move to the shared components.

## 6. Out of scope

#1068 messaging scope (session-to-session channel, other human participants); #1461 presence; live writing of answers in groups (later); server merge of rooms into sessions (option C).

## 7. User-facing documentation TODO

`docs/group-chats.md` (screen, composer, participant panel, approvals, invitation context, starting a group from a chat); `docs/using-omnipus-ui.md` (minimal header controls, helper-chat breadcrumb). `docs-verifier` audits the drafts.

## 8. Next steps

ADR (architect, Opus) on section 5 → one grill-spec round (ADR mode, ChatGPT) → founder interview on its questions → ADR correction → plan-spec spec with UAT rows → two grill rounds with founder interviews → plan and build (one frontend owner, one backend owner, one test author, CHECK by a different instance; feature 5-reviewer gate).
