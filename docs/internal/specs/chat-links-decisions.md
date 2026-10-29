# chat-links decisions log

Source: Interview, 2026-10-15 (team-lead; founder via the question tool for Q1, Q4 and Q5), the architect fix round the same day, a later founder decision the same day that replaces working assumption A (Q1-panel), and the founder's answers of 2026-10-29 to the drift check (F1, F2; Mac terminal, question tool, relayed by the coordinator; drift report `(maintainer-local path)`).
Each row names who decided it. A row not marked Founder is not founder-final.

## Founder-final

| ID | Decision |
|---|---|
| Q1 | `show_to_user` is a global-ceiling `allow`, own workspace only, rate-limited. The cap of 5 calls per minute per chat was accepted with Q1 (handover, 2026-10-15). How the counter is stored is an architect choice below, not a second founder ruling. |
| Q4 | Linking to saved knowledge-base views is out of scope. Notes and `.base` files stay linkable. A view gets no special link. The follow-up is recorded at planning (spec unit U11); it does not wait on another founder ask. |
| Q5 | `show_to_user` auto-open is limited to the turn's own workspace. |
| R3 | When the user asks where a file is, the agent gives the absolute path and a link. |
| R4 | A file outside every Library is shown as the full path, with no link. |
| R5 | On web chat, a `send_file` card for a workspace file also shows "Open in Library". |
| D-A | Ready-made `link:` lines are produced on web chat only. Other channels get the file name only; `send_file` still delivers the file there. |
| Q1-panel | **Founder, 2026-10-15. Replaces working assumption A.** `show_to_user` auto-opens the Library only when the user's chat (the session the agent is replying in) currently has focus in a browser tab: the tab is visible, and that chat is the view on screen. Otherwise it does not open. A clickable "Open in Library" notice is placed in the chat instead. |
| D-watch | **Founder, 2026-10-15.** The agent can tell whether the user is watching its own chat right now. Agents should use that fact, for example only call `show_to_user` when the user is watching. Presence is only for the agent's own chat. |
| F1 | **Founder, 2026-10-29 (answer A).** Chat links win over base-instruction rule 27. Agents show a file by name or relative path plus a link by default; the absolute path only when the user asks, or when the file is outside every Library. Rule 27 of `pkg/agent/context.go` `<operating_rules>` ("After reading back saved artifacts, provide their verified full absolute paths.", added 2026-10-21 in `ed39230ab`) and the matching sentence in `docs/agents.md` are rewritten to match, as a requirement of this feature (spec FR-027). |
| F2 | **Founder, 2026-10-29 (answer A).** Omnipus is not granted `show_to_user`. It keeps its fixed tool list (`omnipusSupportedTools`, omnipus-system-agent spec section 6.5). Files in Omnipus's chat still get passive links. Supersedes architect row D-admin. |

## Working assumption

None. Q1-panel is no longer an assumption.

## Team-lead (technical, not founder)

| ID | Decision |
|---|---|
| Q2 | `omnipus://file/...` plus bare workspace-relative links. |
| Q3 | A new live-only `open_in_library` WebSocket frame (contract-first) plus an optional `library_ref` on `MediaPart`. |
| Q6 | A ready-made `link:` line only on `write_file`, `edit_file`, `append_file`, `send_file`, `show_to_user` and `knowledge_read`. |
| D-B | Every tab already attached to the chat receives the live frame and acts on its own state. The founder focus rule (Q1-panel) decides open versus notice per tab: a tab that is watching opens; a tab that is not watching shows the notice. A tab that was disconnected does not open later. "Any tab watching" is the session's presence bit (D-watch), which is separate from which tab opens. |

## Architect (this spec, not founder)

Recorded in the spec on 2026-10-15, fix round 2. Closed there; not re-opened in planning.

| ID | Decision |
|---|---|
| D-D | "Never on reload" is guaranteed on the server: the frame is delivered unsequenced and unjournaled. The client guard is defence in depth only. |
| D-dedupe | The client guard keys on a server-minted `request_id` (128 random bits, 32 hex characters), not on the provider's `call_id`. |
| D-seam | The open target travels on a typed optional field from `tools.ToolResult` onto `agent.ToolExecEndPayload`. No per-call map. No parsing of the result text. |
| D-hub | New hub method `deliverLive`: look up, do not create; same overflow unbind as `publishMeta`; an evicted hub drops the frame; a connection in hold mode receives it after catch-up. |
| D-takeover | **Superseded by Q1-panel (founder).** Phone width is not an auto-open gate. `historyPushed` and `panelRowWidth` are not inputs. A phone the user is watching auto-opens, same as a wide window. |
| D-limit | The 5-per-minute cap is founder (Q1). The counter is process-wide, keyed by the root chat session id, with an injectable clock. It is not stored on the tool instance. |
| D-admin | **Superseded by F2 (founder, 2026-10-29).** Admin no longer exists (replaced by Omnipus, WU-11). Original text: Admin is not granted `show_to_user` (inventory default deny). This spec's Admin chat has no workspace, so the tool could only refuse. |
| D-omnipus-links | **Architect, drift sync 2026-10-29** (implements F2's "passive links"). Omnipus's chat has no workspace for bare relative links (plain text); `omnipus://` links stay clickable. For an Omnipus turn the resolver also tries every other workspace, so a file Omnipus writes into W2 gets a W2 link; its own agent-home files stay outside (full path). |
| D-grant | **Architect, drift sync 2026-10-29.** `show_to_user` is granted through the founder's 2026-10-22 default tool table (`seed.go::NewCustomAgentToolsCfg` allow; `workerTierDeniedTools` deny), not through per-role grants. Mia and Jim allow; Planner, Researcher, General Purpose and custom workers deny. Replaces the round-2 "Mia, Jim and Ava" grant (Ava no longer exists). |
| D-manifest | `show_to_user` is not added to the pinned full-manifest list. It stays lazy (found through ToolSearch). Role prompt text names it. |
| D-boundary | No new ADR-091 publication boundary. A steered child is refused because it is not the root chat session, before any frame is built. |
| D-presence | How watching is reported and how it expires. Client→server `chat_presence` frame (not a field on attach). A tab is watching when `document.visibilityState === 'visible'` and the chat screen for that session is mounted (`sessions.$sessionId` or `WorkspaceChatTab`). `admin.chat` is a redirect, not a chat view. Any such tab makes the session watching. Claims refresh every 15s, expire after 45s, and drop on `watching: false`, unmount, or disconnect. Unknown or expired is not watching. The turn context line is exactly `User watching: yes` or `User watching: no`, for that turn's own session only. The open-versus-notice choice is made when the frame arrives, not when the tab later gains focus. |
| D-leave | Unsaved Library edits still block auto-open (founder R6, the leave gate). A side panel that is not dirty does not. When the user is watching, Calendar or Mail is replaced. This is the consequence of Q1-panel replacing assumption A, not a second founder ruling. **Amended 2026-10-29 (C2, F3):** unsaved edits in whichever side panel is open (Library or Mail) block auto-open, because the outgoing panel's leave gate would otherwise raise a discard prompt the user did not ask for. |
| D-chat-only | **Architect, 2026-10-29 (C2, F4).** Chat links are enabled only in chat message rendering (live `MarkdownText`, `HistoricalMessageMarkdown` in `ChatScreen` and `AskUserQuestionCard`), by explicit opt-in. Received mail (`MailPreviewPane`) and Library/knowledge previews keep today's link handling: mail is content from outside senders, and a note's relative link is relative to the note's folder, not the workspace root. |
| D-offscreen | **Architect, 2026-10-29 (C2, F1)**, applying founder Q1-panel and D-B. An `open_in_library` frame for a session that is not the active session is not dropped; it is decided as not watching, and its notice waits in that chat. |
| D-presence-mounts | **Architect, 2026-10-29 (drift sync E10; corrects D-presence).** `admin.chat` no longer exists. The chat views are `sessions.$sessionId`, `WorkspaceChatTab` and `OmnipusChatScreen` (`/omnipus`); the presence hook mounts on all three. |
