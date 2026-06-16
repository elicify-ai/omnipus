# Feature Specification: Wave 5a — Wire UI Screens to Backend

**Created**: 2026-03-29
**Status**: Draft
**Input**: BRD Appendix C (UI Spec) §C.6.1–C.6.5, Appendix E (Data Model), existing Wave 0 UI shell + Wave 1 backend gateway

> **Backend Note**: The backend REST API and WebSocket handlers were implemented as part of the core foundation (Wave 1) and subsequent feature waves. Wave 5a covers frontend implementation only. Backend specifications are covered in the relevant feature specs (`agent-reliability-spec`, `agent-task-management-spec`). The Ambiguity #1 clarification ("Wave 5a implements both frontend + backend together") reflects that the two workstreams run in parallel with frontend-lead and backend-lead working concurrently — not that the backend spec lives in this document. See `docs/protocol/websocket-protocol.md` for the canonical WebSocket frame contract that both leads implement against.

---

## User Stories & Acceptance Criteria

### User Story 1 — WebSocket Chat Streaming (Priority: P0)

A user wants to send a message in the Chat screen and see the agent's response stream in real-time, token-by-token, so that they can read the response as it generates without waiting for completion.

The Chat screen is currently a static welcome page. The backend exposes a WebSocket endpoint (`ws://` upgrade from `/api/v1/chat/ws`) for bi-directional streaming. This story connects them: user types a message via WebSocket, the backend streams response tokens back over the same connection, and tokens render incrementally with a cursor indicator.

**Why this priority**: Chat is the home screen where 80% of the user experience lives. Without streaming chat, the app has zero utility.

**Independent Test**: Open the app in a browser, type a message, verify the WebSocket connection is established, tokens appear incrementally with a blinking cursor, and a "done" state renders the final markdown.

**Acceptance Scenarios**:

1. **Given** the gateway is running and a provider is configured, **When** the user types a message and presses Enter (or clicks send), **Then** a JSON message `{"type":"message","content":"..."}` is sent over the WebSocket connection, and the server streams response frames back as JSON messages, rendering tokens incrementally in the message thread.
2. **Given** a streaming response is in progress, **When** a `{"type":"token","content":"..."}` WebSocket frame arrives, **Then** the token is appended to the current assistant message with a blinking cursor (█) at the end.
3. **Given** a streaming response is in progress, **When** a `{"type":"done","stats":{}}` WebSocket frame arrives, **Then** the cursor disappears, the full message renders as markdown (headings, code blocks, lists, links), and token count + cost appear below the message.
4. **Given** no streaming response is active, **When** the user views the chat, **Then** a "thinking" indicator (`Thinking...`) appears between sending a message and receiving the first token.
5. **Given** a streaming response is in progress, **When** the WebSocket connection drops or a `{"type":"error","message":"..."}` frame arrives, **Then** an error message renders inline ("Connection lost. Retrying...") and a retry button appears.
6. **Given** the user sends a message, **When** the message is sent, **Then** the user's message appears immediately in the thread (optimistic rendering) with a timestamp.

---

### User Story 2 — Session Bar (Priority: P0)

A user wants to see the active agent, model, heartbeat timer, session cost, and token count in the session bar so that they have at-a-glance awareness of the current chat context.

The AppShell header has a `#session-bar-slot` placeholder. This story fills it with the session bar elements from C.6.1.1.

**Why this priority**: The session bar provides critical context (which agent, cost, tokens) that users need during every chat interaction.

**Independent Test**: Load the chat screen, verify the session bar shows agent name, model name, heartbeat timer, cost, and token count — all populated from the backend.

**Acceptance Scenarios**:

1. **Given** a chat session is active, **When** the session bar renders, **Then** it displays: agent selector dropdown, model display, heartbeat timer, session cost, and token count.
2. **Given** multiple agents exist, **When** the user clicks the agent selector, **Then** a dropdown lists all active agents with their avatar icon and name.
3. **Given** the user selects a different agent, **When** the selection completes, **Then** the chat switches to that agent's most recent session (or starts a new one), and the session bar updates.
4. **Given** tokens are streaming, **When** the token count updates, **Then** the `⟳` token counter increments in real-time.
5. **Given** cost data is available, **When** the session bar renders, **Then** cost shows cumulative session cost formatted as `$X.XX`.
6. **Given** heartbeat is configured, **When** the session bar renders, **Then** the heartbeat indicator shows time until next heartbeat (e.g., "28m").

---

### User Story 3 — Message History (Priority: P0)

A user wants to see their previous messages when returning to a session so that they can resume context from where they left off.

Sessions are stored as day-partitioned JSONL files. This story loads and renders historical messages.

**Why this priority**: Without message history, every page refresh loses all context — the chat is unusable for any real work.

**Independent Test**: Send messages, refresh the page, verify all previous messages (user + assistant) are visible with correct formatting.

**Acceptance Scenarios**:

1. **Given** a session with previous messages exists, **When** the user navigates to that session, **Then** all messages load in chronological order with correct roles (user/assistant), timestamps, and markdown rendering.
2. **Given** a session spans multiple day partitions, **When** the session loads, **Then** messages from all partitions are merged in chronological order.
3. **Given** a session has many messages, **When** the user scrolls up, **Then** older messages load progressively (pagination) without blocking the UI.
4. **Given** a message contains tool calls, **When** it renders, **Then** tool call badges appear with correct status (success/error) and are collapsed by default.
5. **Given** a compaction entry exists in the transcript, **When** it renders, **Then** it appears as a system message ("Context compacted — older messages summarized").

---

### User Story 4 — Tool Call Components (Priority: P1)

A user wants to see structured, visually distinct tool call results inline in chat so that they can understand what the agent did and inspect parameters/results.

The BRD defines a tool component registry (C.6.1.4) with custom components for built-in tools and a generic fallback.

**Why this priority**: Tool calls are how agents do work. Without visual tool components, the user can't verify what the agent is doing.

**Independent Test**: Trigger a tool call (e.g., web_search), verify the tool badge renders with running/success/error states, and that expanding it shows parameters and results.

**Acceptance Scenarios**:

1. **Given** the agent invokes a tool, **When** `{"type":"tool_call_start","tool":"...","params":{}}` and `{"type":"tool_call_result","tool":"...","result":{}}` WebSocket frames arrive, **Then** a tool call badge renders inline showing: tool icon, tool name, spinner (running state), and elapsed time.
2. **Given** a tool call completes successfully, **When** the success event arrives, **Then** the badge shows a green checkmark, duration, and collapses by default with "Click to expand" hint.
3. **Given** a tool call fails, **When** the error event arrives, **Then** the badge shows a red X, the error message is visible, and a "Retry" button appears.
4. **Given** a tool call badge is collapsed, **When** the user clicks it, **Then** it expands to show parameters and result in a formatted card.
5. **Given** a known built-in tool (e.g., `exec`, `web_search`, `file.read`), **When** it renders, **Then** the registered custom component is used (terminal block for exec, search cards for web_search, syntax-highlighted preview for file.read).
6. **Given** an unknown tool (MCP/ClawHub), **When** it renders, **Then** the generic fallback component is used showing JSON parameters and result.

---

### User Story 5 — Exec Approval UI (Priority: P1)

A user wants to see an inline approval prompt when an agent requests exec permission so that they can allow, deny, or permanently allow the command.

The BRD defines the approval block (C.6.1.5) with command, working directory, matched policy, and three action buttons.

**Why this priority**: Exec approval is a critical security gate. Without this UI, agents requiring approval cannot function.

**Independent Test**: Configure exec with `approval=ask`, trigger a command, verify the approval block appears with Allow/Deny/Always Allow buttons, and that clicking each works correctly.

**Acceptance Scenarios**:

1. **Given** the agent requests exec approval, **When** a `{"type":"exec_approval_request","command":"...","id":"..."}` WebSocket frame arrives, **Then** an inline approval block renders showing: the command, working directory, and matched policy rule.
2. **Given** an approval block is displayed, **When** the user clicks "Allow", **Then** the approval is sent to the backend, the block updates to "Allowed", and the agent proceeds.
3. **Given** an approval block is displayed, **When** the user clicks "Deny", **Then** the denial is sent to the backend, the block updates to "Denied", and the agent receives the denial.
4. **Given** an approval block is displayed, **When** the user clicks "Always Allow", **Then** the "always allow" preference is sent to the backend, the block updates to "Always Allowed", and future identical commands auto-approve.
5. **Given** the user has not responded to an approval, **When** the approval block renders, **Then** the three buttons are prominently displayed with a yellow/warning border.

---

### User Story 6 — Agent List & Profile Cards (Priority: P1)

A user wants to browse their agents on the Agents screen so that they can see all configured agents, their status, and navigate to their profiles.

The Agents screen is currently an empty placeholder. This story populates it with agent cards from the `GET /api/v1/agents` endpoint.

**Why this priority**: Agent management is core to Omnipus's multi-agent architecture. Users need to see and manage their agents.

**Independent Test**: Navigate to the Agents screen, verify agent cards render with correct avatars, names, statuses, models, and types. Click a card to navigate to the profile.

**Acceptance Scenarios**:

1. **Given** agents are configured, **When** the user navigates to the Agents screen, **Then** a responsive grid of agent profile cards renders with: avatar (Phosphor icon on color circle), name, status badge, description, model, type badge (system/core/custom).
2. **Given** fewer than 4 agents, **When** the grid renders, **Then** cards display in a single column. With 4+ agents, the grid uses 3 columns on desktop.
3. **Given** the user clicks an agent card, **When** the click event fires, **Then** the app navigates to that agent's profile page.
4. **Given** agents exist, **When** the agent list renders, **Then** a `[+ New Agent]` button appears at the bottom of the grid.
5. **Given** an agent is active with a running session, **When** the card renders, **Then** a green dot and "active" status appear.

---

### User Story 7 — Agent Profile Page (Priority: P1)

A user wants to view and edit an agent's configuration on a profile page so that they can customize model, tools, heartbeat, and other settings.

Each agent type (system/core/custom) has different editable sections per C.6.3.3.

**Why this priority**: Agent configuration is essential for customizing behavior — model selection, tool access, rate limits.

**Independent Test**: Navigate to a core agent's profile, verify all sections render (model, tools, heartbeat, stats, activity, sessions), edit the model, verify the change persists.

**Acceptance Scenarios**:

1. **Given** the user navigates to an agent's profile, **When** the page loads, **Then** it displays a single-column scrollable layout with sections appropriate to the agent type (per C.6.3.3 matrix).
2. **Given** a core agent, **When** the profile renders, **Then** editable sections include: avatar/name/description, profile picture/color, model+fallbacks, HEARTBEAT.md, tools & skills, rate limits, stats, recent activity, sessions, workspace files, memory.
3. **Given** a system agent, **When** the profile renders, **Then** only name (editable), model (editable), stats, recent activity, and sessions are shown.
4. **Given** the user edits a model dropdown, **When** they select a new model and save, **Then** the change is sent to `PUT /api/v1/agents/{id}` and the profile updates.
5. **Given** rate limits section, **When** it renders, **Then** it shows inherited global defaults with a "Use global defaults" toggle (default on), and override fields disabled unless toggle is off.

---

### User Story 8 — Create Agent Modal (Priority: P2)

A user wants to create a new custom agent so that they can have a specialized agent for a particular task domain.

The create agent modal (C.6.3.6) has four fields: profile picture (icon + color or upload), name, description, model.

**Why this priority**: Custom agents are a key differentiator but not required for basic usage with core agents.

**Independent Test**: Click "+ New Agent", fill in name and select model, click "Create Agent", verify the agent appears in the list.

**Acceptance Scenarios**:

1. **Given** the user clicks "+ New Agent", **When** the modal opens, **Then** it shows fields: icon picker (Phosphor icon categories + color palette), name (required), description (optional), model dropdown with Advanced expander.
2. **Given** the user fills in a name and selects a model, **When** they click "Create Agent", **Then** a POST request is sent to `/api/v1/agents`, the modal closes, and the new agent card appears in the grid.
3. **Given** the user leaves the name field empty, **When** they click "Create Agent", **Then** a validation error appears and the request is not sent.
4. **Given** the user clicks "Cancel", **When** the modal closes, **Then** no agent is created.

---

### User Story 9 — Settings: Providers (Priority: P1)

A user wants to configure API provider credentials so that they can connect to LLM providers (Anthropic, OpenAI, etc.) and test the connection.

Settings screen is currently empty. This story implements the Providers section (C.6.5.2).

**Why this priority**: Without provider configuration, no agent can function. This is the prerequisite for chat.

**Independent Test**: Navigate to Settings > Providers, add an API key for Anthropic, click "Save & Connect", verify connection test passes and models appear.

**Acceptance Scenarios**:

1. **Given** the user navigates to Settings > Providers, **When** the page loads, **Then** configured providers show with connection status indicators (green/red), and available unconfigured providers show with a "+ Configure" button.
2. **Given** the user clicks "+ Configure" on a provider, **When** the form expands, **Then** it shows: API key input (password masked), optional endpoint URL, and "Save & Connect" button.
3. **Given** the user enters an API key and clicks "Save & Connect", **When** the connection test runs, **Then** a spinner shows during test, and on success the status turns green and available models populate.
4. **Given** the connection test fails, **When** the error returns, **Then** the error message displays inline and the status remains red.
5. **Given** a provider is configured, **When** the user wants to update the key, **Then** they can click "Edit", the masked key field becomes editable, and they can save the new key.
6. **Given** the API key is saved, **When** stored to disk, **Then** it is written to `credentials.json` (encrypted), never to `config.json`.

---

### User Story 10 — Settings: Security & Policy (Priority: P2)

A user wants to configure security policies and rate limits so that they can control agent behavior boundaries.

Implements C.6.5.4 — policy mode, prompt injection defense, rate limits, cost cap, credentials vault, audit log viewer.

**Why this priority**: Security settings are important but have sensible defaults. Most users won't touch these initially.

**Independent Test**: Navigate to Settings > Security, change the policy mode, adjust the daily cost cap, verify changes persist after page refresh.

**Acceptance Scenarios**:

1. **Given** the user navigates to Settings > Security & Policy, **When** the page loads, **Then** it shows sections: Policy, Prompt Injection Defense, Rate Limits & Cost Control, Credentials Vault, Audit Log, Device Trust, Diagnostics.
2. **Given** the Policy section, **When** it renders, **Then** the default policy mode (allow/deny) is displayed with a toggle, and exec approval setting is shown.
3. **Given** the Rate Limits section, **When** it renders, **Then** the daily cost cap shows with a progress bar of today's spend, and per-agent default limits are editable.
4. **Given** the user changes the daily cost cap, **When** they save, **Then** the new cap is sent to the backend and persists.
5. **Given** the Audit Log section, **When** the user clicks "View Audit Log", **Then** a fullscreen overlay opens with searchable, filterable audit log entries.

---

### User Story 11 — Settings: Gateway (Priority: P2)

A user wants to configure gateway bind address, port, and auth token so that they can control how the gateway listens and who can access it.

Implements C.6.5.5.

**Why this priority**: Gateway settings have safe defaults (localhost only). Configuration is for advanced/remote users.

**Independent Test**: Navigate to Settings > Gateway, change the port, verify the change is reflected in config.

**Acceptance Scenarios**:

1. **Given** the user navigates to Settings > Gateway, **When** the page loads, **Then** it shows: bind address (localhost/LAN/all), port, auth mode, gateway token with rotate/copy buttons, and connection status.
2. **Given** the user changes the bind address, **When** they save, **Then** the change is written to config and a warning appears that a restart is required.
3. **Given** a gateway token exists, **When** the user clicks "Copy", **Then** the token is copied to clipboard with confirmation.
4. **Given** the user clicks "Rotate", **When** confirmed, **Then** a new token is generated and the old one is invalidated.

---

### User Story 12 — Settings: Data & Backup (Priority: P2)

A user wants to see storage stats and manage session retention so that they can monitor disk usage and clean up old data.

Implements C.6.5.6.

**Why this priority**: Data management is important for long-term usage but not critical for initial setup.

**Independent Test**: Navigate to Settings > Data & Backup, verify storage stats (workspace size, session count, memory entries) display correctly.

**Acceptance Scenarios**:

1. **Given** the user navigates to Settings > Data & Backup, **When** the page loads, **Then** it shows: session retention setting (days), storage stats (workspace size, session count, memory entries), and backup/restore options.
2. **Given** the user changes session retention, **When** they save, **Then** the new value persists in config.
3. **Given** storage stats, **When** displayed, **Then** they show accurate counts from the backend.
4. **Given** the user clicks "Create Backup", **When** the backup runs, **Then** a progress indicator shows and the backup appears in the previous backups list.

---

### User Story 13 — Command Center: Status & Tasks (Priority: P1)

A user wants to see system status and a task list on the Command Center so that they have an operational overview of all agents and work items.

The Command Center is currently empty. This story implements the status bar (C.6.2.1), attention section (C.6.2.2), task list (C.6.2.3), and agent summary rows (C.6.2.4).

**Why this priority**: The Command Center is the operational overview — critical for multi-agent workflows.

**Independent Test**: Navigate to Command Center, verify the status bar shows gateway status, agent count, channel count, and daily cost. Verify tasks are listed.

**Acceptance Scenarios**:

1. **Given** the gateway is running, **When** the Command Center loads, **Then** the status bar shows: "Gateway online", agent count, channel count, and today's cost.
2. **Given** tasks exist, **When** the task section renders, **Then** tasks display in a list view (default) grouped by status, with each row showing: task name, assigned agent, status badge, and cost.
3. **Given** the user toggles to Board view, **When** the toggle is clicked, **Then** a GTD kanban board renders with 5 columns (Inbox, Next, Active, Waiting, Done) and task cards are draggable.
4. **Given** agents are configured, **When** the agent summary section renders, **Then** compact rows show each agent with: status, model, task count, and cost, expandable on click to show session/heartbeat/context details.
5. **Given** an exec approval is pending, **When** the attention section renders, **Then** the pending approval appears with inline Allow/Deny/Always Allow buttons.
6. **Given** the user clicks "+ Task", **When** the create task form appears, **Then** they can enter a name, optional description, assign an agent, and set a project.

---

### User Story 14 — Skills Browser (Priority: P2)

A user wants to browse installed skills and MCP servers on the Skills & Tools screen so that they can manage agent capabilities.

The Skills screen is currently empty. This story implements the tabs: Installed Skills, MCP Servers, Channels, Built-in Tools (C.6.4).

**Why this priority**: Skills management extends agent capabilities but isn't required for basic chat functionality.

**Independent Test**: Navigate to Skills & Tools, verify installed skills list renders with name/version/status, MCP servers tab shows connected servers, and Channels tab shows enabled/available channels.

**Acceptance Scenarios**:

1. **Given** skills are installed, **When** the Installed Skills tab loads, **Then** skill cards render showing: name, version, verification status, description, author, agent assignment. Note (MIN-003): The verification status values and their visual treatment are not yet specified. Pending a Wave 3 UI spec amendment, implementors should use three states — `verified` (green checkmark), `unverified` (yellow warning), `untrusted` (red X) — as placeholder values aligned with Wave 3's trust model. This assumption must be confirmed against the Wave 3 spec before shipping.
2. **Given** the MCP Servers tab is selected, **When** it loads, **Then** connected MCP servers show with: name, transport type, connection status, discovered tools count.
3. **Given** the Channels tab is selected, **When** it loads, **Then** three sections appear: Enabled channels (with status and Configure/Disable), Available channels (with Enable button), and Community section. Note (MIN-004): The WhatsApp channel (Wave 4) requires QR code display for pairing. Where the QR code renders is not specified in this spec. Pending a follow-up spec, implementors should render the QR code inside the WhatsApp channel's "Configure" modal/panel. A BDD scenario for this path should be added to either Wave 4 or a dedicated channel configuration spec.
4. **Given** the Built-in Tools tab is selected, **When** it loads, **Then** all built-in tools are listed with expandable inline configuration (approval mode, timeout, agent access, usage stats).

---

### User Story 15 — Session/Agent Hierarchy Panel (Priority: P1)

A user wants to browse their agents and sessions in a slide-over panel so that they can quickly switch between conversations.

The session bar has a "Sessions" trigger (C.6.2). Clicking it opens the agent/session hierarchy.

**Why this priority**: Multi-session navigation is essential for any user working on more than one task.

**Independent Test**: Click "Sessions" in the session bar, verify the slide-over panel shows agents with their sessions in accordion format, and clicking a session loads it.

**Acceptance Scenarios**:

1. **Given** the user clicks "Sessions" in the session bar, **When** the panel opens, **Then** it slides in from the left showing agents in accordion format with their sessions listed underneath.
2. **Given** an agent has sessions, **When** its accordion is expanded, **Then** sessions are listed by title with a green dot on the active session and a "+ New session" option.
3. **Given** the user clicks a session, **When** the click fires, **Then** the chat loads that session's history and the panel closes.
4. **Given** the user clicks "+ New session" under an agent, **When** the new session is created, **Then** the chat switches to a blank session for that agent.

---

### User Story 16 — Settings: Channels Configuration (Priority: P2)

A user wants to configure channel routing and enable/disable channels from Settings so that they can control how messages reach their agents.

Implements C.6.5.3 (Routing & Policies) — which channels route to which agents, allow_from restrictions.

**Why this priority**: Channel configuration is important for multi-channel setups but most single-channel users won't need it initially.

**Independent Test**: Navigate to Settings > Routing & Policies, verify channel routing rules display, and that changes persist.

**Acceptance Scenarios**:

1. **Given** the user navigates to Settings > Routing & Policies, **When** the page loads, **Then** it shows channel routing rules (which users/groups route to which agents), allow_from restrictions per channel, and DM policy configuration.
2. **Given** the user adds a routing rule, **When** they save, **Then** the rule persists and takes effect.
3. **Given** channels are enabled, **When** the routing page loads, **Then** each channel shows its current routing configuration.

---

### User Story 17 — Cancel/Interrupt Streaming (Priority: P0)

A user wants to cancel an in-progress agent response or tool execution so that they can stop unwanted work, redirect the agent, or abort a long-running operation without waiting for it to complete.

During streaming, the send button transforms into a "Stop" button. Clicking it sends a cancel signal over WebSocket. The backend stops the LLM call, saves the partial response, and the UI preserves what was generated so far.

**Why this priority**: Without cancel, users are trapped waiting for long responses or runaway tool executions. This is a fundamental UX requirement for any streaming chat interface.

**Independent Test**: Start a long streaming response, click the Stop button, verify the response stops within 1 second, the partial response is preserved and readable, and the chat returns to an input-ready state.

**Acceptance Scenarios**:

1. **Given** a streaming response is in progress, **When** the user clicks the "Stop" button (which replaces the send button during streaming), **Then** a `{"type":"cancel","session_id":"..."}` frame is sent over the WebSocket, the streaming stops, and the partial response is preserved in the chat with `status: "interrupted"`.
2. **Given** a tool execution is in progress, **When** the user clicks the "Stop" button, **Then** a cancel frame is sent, the tool execution is aborted, and the tool badge updates to show "Cancelled" with an orange indicator.
3. **Given** no streaming or tool execution is active (idle state), **When** the user triggers cancel (e.g., via keyboard shortcut), **Then** nothing happens (no-op) — the cancel is silently ignored.
4. **Given** a cancel has been sent, **When** the backend acknowledges the cancel, **Then** the UI transitions back to the input-ready state within 1 second: the Stop button reverts to the Send button, and the input field is re-enabled.
5. **Given** a partial response has been preserved after cancel, **When** the user views the message, **Then** the partial content is displayed with a muted "(interrupted)" label below the message, and the message is not marked as an error.

---

## Behavioral Contract

Primary flows:
- When the user sends a chat message, the system sends it over the WebSocket connection and streams the response token-by-token via WebSocket JSON frames, rendering incrementally with a cursor indicator.
- When streaming completes (a `done` frame arrives), the system renders the full response as formatted markdown with token count and cost.
- When the user cancels a streaming response, the system sends a `{"type":"cancel","session_id":"..."}` frame, stops rendering, and preserves the partial response.
- When the user navigates to the Agents screen, the system displays agent cards fetched from `/api/v1/agents`.
- When the user navigates to Settings > Providers, the system shows configured and available providers with connection status.
- When the user navigates to the Command Center, the system shows gateway status, task list, and agent summaries.
- When the user opens the session panel, the system shows agents with their sessions in accordion format.

Error flows:
- When the WebSocket connection drops mid-stream, the system shows an inline error with a retry button and attempts auto-reconnect with exponential backoff.
- When an API request fails (network error, 500), the system shows a toast notification with the error and does not crash.
- When the provider connection test fails, the system shows the error inline and keeps the status indicator red.
- When loading message history fails, the system shows a "Could not load messages" placeholder with a retry button.

Boundary conditions:
- When no agents are configured, the system shows a helpful empty state directing the user to create one.
- When no provider is configured, the system shows the onboarding provider setup.
- When a session has no messages, the system shows the mascot welcome state.
- When the token count exceeds the context window, the system shows a context usage warning in the session bar.

---

## Edge Cases

- What happens when the user sends an empty message? Expected: the send button is disabled; empty messages are not submitted.
- What happens when the WebSocket reconnects after a brief network drop? Expected: a new WebSocket connection is established, any in-progress streaming resumes from where it left off or the full response loads on reconnect.
- What happens when two browser tabs are open to the same session? Expected: each tab has its own WebSocket connection; both receive streaming updates; no data corruption.
- What happens when the agent list returns an agent with an unrecognized icon name? Expected: a default icon (Robot) is used as fallback.
- What happens when a very long message (>50KB) is loaded from history? Expected: it renders without freezing; code blocks are virtualized if necessary.
- What happens when the gateway is unreachable? Expected: the UI shows a "Disconnected" banner with retry/reconnect logic.
- What happens when a tool call has no result yet (pending)? Expected: the badge shows a spinner and "Running..." state.
- What happens when credentials.json is corrupted? Expected: the Settings > Providers page shows an error state, not a crash.
- What happens when session retention deletes old partitions while the user is viewing them? Expected: the UI handles missing data gracefully with a "Messages no longer available" notice.

---

## Explicit Non-Behaviors

- The system must not store API keys in `config.json` because credentials must only be in the encrypted `credentials.json` (BRD SEC-23).
- The system must not send chat messages without user action because agents should only respond to explicit user input in webchat (no auto-send).
- The system must not render emoji in UI chrome or stored data because Omnipus uses Phosphor Icons exclusively (emoji only in LLM chat output via translator).
- The system must not expose the system agent's prompts in the agent profile because system and core agent prompts are hardcoded and hidden (C.6.3.2).
- The system must not auto-approve exec commands because the approval flow is a security gate that requires explicit user interaction (C.6.1.5).
- The system must not poll the backend for chat responses because WebSocket is the streaming mechanism — no polling fallback.
- The system must not use SSE (Server-Sent Events) for chat streaming because the protocol has been upgraded to WebSocket for bi-directional communication (cancel, approval responses).
- The system must not implement Electron-specific features because Wave 5a targets the browser-embedded open source variant only.
- The system must not implement the onboarding flow because that is scoped to Wave 5b.
- The system must not render the system agent (omnipus-system) in the agent selector dropdown for chat because the system agent is accessed separately. Note (MIN-005): The system agent SHOULD appear in the session hierarchy panel (User Story 15) because users need to navigate to Omnipus sessions. The "not in agent selector dropdown" rule applies only to the chat agent picker, not the session accordion panel.
- The system must not implement the pin feature because it is explicitly deferred. Note (MIN-006): The BRD Appendix C §C.6.1.6–C.6.1.7 references a `[Pin]` action on rich chat components. The pin data model is defined in Appendix E (`pins/` directory), but the full pin UI (pinned items list, lifecycle management) is out of scope for Wave 5a. A dedicated pin spec is required before implementation.
- The system must not implement light mode in Wave 5a because it is explicitly deferred. Note (MIN-008): Settings §C.6.5.1 lists "theme (light/dark/system)" as a preference, but no spec defines the light mode color palette or implementation. Light mode is deferred to a later wave. Until a light mode spec exists, the theme toggle setting SHOULD be omitted or shown as "coming soon" to avoid shipping a broken feature.

---

## Integration Boundaries

### Backend REST API (`/api/v1/*`)

- **Data in**: JSON request bodies (agent config, settings changes, task CRUD) via REST; chat messages via WebSocket JSON frames (`{"type":"message","content":"..."}`, `{"type":"cancel","session_id":"..."}`, `{"type":"exec_approval_response","id":"...","decision":"allow|deny|always"}`)
- **Data out**: JSON response bodies (agent list, session metadata, config, task list, storage stats) via REST; streaming via WebSocket JSON frames (`{"type":"token","content":"..."}`, `{"type":"tool_call_start","tool":"...","params":{}}`, `{"type":"tool_call_result","tool":"...","result":{}}`, `{"type":"exec_approval_request","command":"...","id":"..."}`, `{"type":"done","stats":{}}`, `{"type":"error","message":"..."}`)
- **Contract**: REST with JSON for CRUD operations. WebSocket (`ws://` upgrade from `/api/v1/chat/ws`) for bi-directional chat streaming. WebSocket lifecycle: connect → authenticate via first message with `{"type":"auth","token":"..."}` when `OMNIPUS_BEARER_TOKEN` is set → keep-alive via WebSocket ping/pong → reconnect on disconnect. REST auth via `Authorization: Bearer <token>` header. CORS restricted to gateway origin.
- **On failure**: Network errors → toast notification + retry button. 401 → redirect to auth/token entry. 500 → inline error + retry. WebSocket drop → auto-reconnect with exponential backoff (max 3 retries, then manual retry button).
- **Development**: Real gateway service running locally — no mocks for WebSocket streaming. Unit tests may mock WebSocket for component isolation.

### File System (via backend)

- **Data in**: Config reads, session transcript reads, agent workspace reads
- **Data out**: Config writes, credential writes, task CRUD
- **Contract**: All file operations go through the backend API — the frontend never accesses the filesystem directly.
- **On failure**: Backend returns appropriate HTTP errors; frontend displays them inline.
- **Development**: Real file system — backend manages `~/.omnipus/` directly.

### Zustand (UI State)

- **Data in**: User interactions (sidebar state, active view, selected agent, selected session, modal states)
- **Data out**: Reactive UI state consumed by React components
- **Contract**: In-memory only. No persistence to disk except sidebar pin preference via localStorage.
- **On failure**: N/A — in-memory state. Page refresh resets to defaults.
- **Development**: Real Zustand stores.

### TanStack Query (Server State)

- **Data in**: API responses from backend
- **Data out**: Cached, deduplicated data for React components with automatic refetch, stale-while-revalidate
- **Contract**: Each API endpoint has a corresponding query key. Mutations invalidate relevant queries.
- **On failure**: TanStack Query retry logic (3 retries with exponential backoff). Error state exposed to components.
- **Development**: Real queries against local gateway.

---

## BDD Scenarios

### Feature: Chat Streaming

#### Scenario: User sends a message and receives streaming response

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the gateway is running with a configured provider
- **And** the user is on the Chat screen
- **When** the user types "Hello, world!" and presses Enter
- **Then** the user message appears immediately in the thread
- **And** a `{"type":"message","content":"Hello, world!"}` frame is sent over the WebSocket connection
- **And** a thinking indicator appears
- **And** `{"type":"token","content":"..."}` WebSocket frames render incrementally as they arrive

---

#### Scenario: Streaming response completes with markdown rendering

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a streaming response is in progress with partial content
- **When** a `{"type":"done","stats":{}}` WebSocket frame arrives
- **Then** the blinking cursor disappears
- **And** the full message renders with markdown formatting (headings, code blocks, lists)
- **And** token count and cost appear below the message

---

#### Scenario: Thinking indicator shows before first token

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the user has just sent a message
- **When** no tokens have arrived yet
- **Then** a "Thinking..." indicator displays below the user message
- **And** when the first token arrives, the thinking indicator is replaced by the streaming content

---

#### Scenario: WebSocket connection error during streaming

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Error Path

- **Given** a streaming response is in progress
- **When** the WebSocket connection drops
- **Then** an inline error message renders ("Connection lost. Retrying...")
- **And** auto-reconnect attempts with exponential backoff
- **And** a "Retry" button appears if auto-reconnect fails after 3 attempts

---

#### Scenario: User message appears optimistically

**Traces to**: User Story 1, Acceptance Scenario 6
**Category**: Happy Path

- **Given** the user types a message
- **When** they press Enter
- **Then** the user message appears in the thread immediately with a timestamp
- **And** the message appears before the server acknowledges the POST request

---

### Feature: Cancel/Interrupt

#### Scenario: Cancel during streaming preserves partial response

**Traces to**: User Story 17, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a streaming response is in progress with partial content "Here is the analysis of..."
- **And** the send button has transformed into a "Stop" button
- **When** the user clicks the "Stop" button
- **Then** a `{"type":"cancel","session_id":"..."}` frame is sent over the WebSocket
- **And** streaming stops within 1 second
- **And** the partial response "Here is the analysis of..." is preserved in the chat
- **And** the message shows a muted "(interrupted)" label
- **And** the Stop button reverts to the Send button

---

#### Scenario: Cancel during tool execution

**Traces to**: User Story 17, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a tool execution (`web_search`) is in progress with a running spinner badge
- **When** the user clicks the "Stop" button
- **Then** a cancel frame is sent over the WebSocket
- **And** the tool badge updates to show "Cancelled" with an orange indicator
- **And** the chat returns to input-ready state

---

#### Scenario: Cancel when idle is a no-op

**Traces to**: User Story 17, Acceptance Scenario 3
**Category**: Edge Case

- **Given** no streaming response or tool execution is in progress
- **And** the input field shows the normal Send button
- **When** the user presses the cancel keyboard shortcut (Escape)
- **Then** nothing happens — no WebSocket frame is sent, no UI change occurs

---

### Feature: Session Bar

#### Scenario: Session bar renders with agent and session info

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a chat session is active with the "General Assistant" agent on "claude-opus"
- **When** the session bar renders
- **Then** the agent selector shows "General Assistant" with its green robot avatar
- **And** the model shows "claude-opus"
- **And** the heartbeat timer shows time until next heartbeat
- **And** the cost shows the session total as "$X.XX"
- **And** the token counter shows current context usage

---

#### Scenario: Switching agents via session bar

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the session bar is visible with "General Assistant" selected
- **And** "Researcher" agent is also active
- **When** the user clicks the agent selector and selects "Researcher"
- **Then** the chat switches to the Researcher's most recent session
- **And** the session bar updates to show Researcher's avatar, model, and stats

---

#### Scenario: Token counter updates during streaming

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Happy Path

- **Given** tokens are streaming from the agent
- **When** new tokens arrive
- **Then** the token counter in the session bar increments in real-time

---

### Feature: Message History

#### Scenario: Previous messages load on session navigation

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a session "AWS Pricing Research" has 12 messages
- **When** the user navigates to that session
- **Then** all 12 messages load in chronological order
- **And** user messages show on the right, assistant messages on the left
- **And** markdown is rendered correctly (headings, code, links)
- **And** timestamps are displayed

---

#### Scenario: Multi-day session merges partitions

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a session has messages from 2026-03-28 and 2026-03-29
- **When** the session loads
- **Then** messages from both days merge in chronological order seamlessly

---

#### Scenario: Compaction entries render as system messages

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** a session contains a compaction entry
- **When** the history renders
- **Then** the compaction appears as a centered, muted system message: "Context compacted — older messages summarized"

---

#### Scenario: Empty session shows welcome state

**Traces to**: User Story 3, Edge Case
**Category**: Edge Case

- **Given** a session has no messages
- **When** the user views the session
- **Then** the mascot welcome state is displayed

---

### Feature: Tool Call Components

#### Scenario: Running tool call shows spinner

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the agent invokes the `web_search` tool
- **When** a `{"type":"tool_call_start","tool":"web_search","params":{}}` WebSocket frame arrives
- **Then** a tool badge renders with: search icon, "web_search" label, spinner animation, and elapsed time counter

---

#### Scenario: Successful tool call collapses by default

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a `web_search` tool call is running
- **When** the success event arrives
- **Then** the badge shows a green checkmark and duration
- **And** the badge is collapsed showing "Click to expand"

---

#### Scenario: Failed tool call shows error with retry

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Error Path

- **Given** a tool call is running
- **When** an error event arrives with message "Timeout after 30s"
- **Then** the badge shows a red X icon
- **And** the error message "Timeout after 30s" is visible
- **And** a "Retry" button appears

---

#### Scenario: Expanding a collapsed tool call shows details

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a collapsed successful `web_search` tool badge
- **When** the user clicks the badge
- **Then** it expands to show: parameters (`query: "AWS m5 instance pricing"`) and results (search result cards)
- **And** Copy and Rerun action buttons appear

---

#### Scenario Outline: Built-in tool uses custom component

**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the agent invokes the `<tool_name>` built-in tool
- **When** the tool call result renders
- **Then** the `<custom_component>` is used for display

**Examples**:

| tool_name | custom_component |
|-----------|-----------------|
| exec | TerminalOutput (command + stdout + exit code badge) |
| web_search | WebSearchResult (result cards with title, URL, snippet) |
| file.read | FileReadPreview (filename + syntax-highlighted preview) |
| file.write | FileWriteConfirm (filename + path + diff preview) |
| file.list | FileTreeView (tree view with file icons) |
| browser.navigate | BrowserScreenshot (inline screenshot thumbnail) |

---

#### Scenario: Unknown tool uses generic component

**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** the agent invokes an MCP tool "custom_mcp_tool"
- **When** the tool call result renders
- **Then** the GenericToolCall component renders with JSON parameters and JSON result

---

### Feature: Exec Approval

#### Scenario: Approval block renders with command details

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the agent requests exec approval for `git pull origin main` in `~/projects/omnipus`
- **When** a `{"type":"exec_approval_request","command":"git pull origin main","id":"..."}` WebSocket frame arrives
- **Then** an inline block renders with a warning border
- **And** the command shows: `$ git pull origin main`
- **And** the working directory shows: `~/projects/omnipus`
- **And** the matched policy shows: `tools.exec.approval=ask`
- **And** three buttons appear: "Allow", "Deny", "Always Allow"

---

#### Scenario Outline: User responds to approval prompt

**Traces to**: User Story 5, Acceptance Scenarios 2-4
**Category**: Happy Path

- **Given** an exec approval block is displayed for `git pull origin main`
- **When** the user clicks `<button>`
- **Then** the `<action>` is sent to the backend
- **And** the block updates to show `<status_text>`

**Examples**:

| button | action | status_text |
|--------|--------|-------------|
| Allow | approval=allow | "Allowed" with green indicator |
| Deny | approval=deny | "Denied" with red indicator |
| Always Allow | approval=always_allow | "Always Allowed" with green indicator |

---

### Feature: Agent List

#### Scenario: Agent cards render in responsive grid

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** 4 agents are configured (system, general-assistant, researcher, content-creator)
- **When** the user navigates to the Agents screen
- **Then** 4 agent cards render in a 3-column grid (desktop)
- **And** each card shows: avatar (Phosphor icon on color circle), name, status, description, model, type badge

---

#### Scenario: Agent card navigation to profile

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Happy Path

- **Given** agent cards are displayed
- **When** the user clicks the "General Assistant" card
- **Then** the app navigates to `/agents/general-assistant`
- **And** the agent profile page loads

---

#### Scenario: Empty agent list

**Traces to**: User Story 6, Edge Case
**Category**: Edge Case

- **Given** no agents are configured (edge case — should not happen in normal usage)
- **When** the Agents screen loads
- **Then** an empty state renders with guidance to create an agent

---

### Feature: Settings — Providers

#### Scenario: Configured provider shows connection status

**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path

- **Given** Anthropic is configured with a valid API key
- **When** the user navigates to Settings > Providers
- **Then** Anthropic shows with a green "Connected" indicator
- **And** available models are listed below

---

#### Scenario: Adding a new provider

**Traces to**: User Story 9, Acceptance Scenarios 2-3
**Category**: Happy Path

- **Given** OpenAI is listed as "Available" (not configured)
- **When** the user clicks "+ Configure"
- **Then** the form expands with API key input, optional endpoint, and "Save & Connect" button
- **And** when the user enters a key and clicks "Save & Connect"
- **Then** a spinner shows during connection test
- **And** on success, the status turns green and models populate

---

#### Scenario: Provider connection test fails

**Traces to**: User Story 9, Acceptance Scenario 4
**Category**: Error Path

- **Given** the user enters an invalid API key
- **When** they click "Save & Connect"
- **Then** the connection test runs and fails
- **And** an error message displays inline: "Authentication failed — check your API key"
- **And** the status indicator remains red

---

#### Scenario: API key stored securely

**Traces to**: User Story 9, Acceptance Scenario 6
**Category**: Happy Path

- **Given** the user saves an API key
- **When** the key is persisted
- **Then** it is written to `credentials.json` (AES-256-GCM encrypted)
- **And** `config.json` contains only a reference (`_ref` suffix), not the key value

---

### Feature: Command Center

#### Scenario: Status bar shows system overview

**Traces to**: User Story 13, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the gateway is running with 3 agents and 5 channels
- **When** the Command Center loads
- **Then** the status bar shows: "Gateway online · 3 agents · 5/6 channels · $4.82 today"

---

#### Scenario: Task list renders with status grouping

**Traces to**: User Story 13, Acceptance Scenario 2
**Category**: Happy Path

- **Given** 5 tasks exist (2 active, 1 next, 1 waiting, 1 done)
- **When** the task section renders in list view
- **Then** tasks are grouped by status
- **And** each row shows: task name, assigned agent avatar, status badge, cost

---

#### Scenario: GTD board view with drag-and-drop

**Traces to**: User Story 13, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the task section is in list view
- **When** the user clicks the "Board" toggle
- **Then** a kanban board renders with 5 columns: Inbox, Next, Active, Waiting, Done
- **And** task cards are positioned in their respective columns

---

#### Scenario: Agent summary rows expand on click

**Traces to**: User Story 13, Acceptance Scenario 4
**Category**: Happy Path

- **Given** agent summary rows are visible
- **When** the user clicks the "Work" agent row
- **Then** it expands to show: current session, today's token/tool stats, heartbeat status, context usage progress bar

---

#### Scenario: Pending approval appears in attention section

**Traces to**: User Story 13, Acceptance Scenario 5
**Category**: Happy Path

- **Given** an exec approval is pending
- **When** the attention section renders
- **Then** the approval shows with command, inline Allow/Deny/Always Allow buttons

---

### Feature: Session Hierarchy Panel

#### Scenario: Panel slides open with agent/session tree

**Traces to**: User Story 15, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the user is on the Chat screen
- **When** the user clicks "Sessions" in the session bar
- **Then** a panel slides in from the left
- **And** it shows agents in accordion format with session lists

---

#### Scenario: Selecting a session loads chat history

**Traces to**: User Story 15, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the session panel is open showing sessions
- **When** the user clicks "AWS Pricing Research" under "General Assistant"
- **Then** the chat loads that session's message history
- **And** the panel closes

---

### Feature: Skills Browser

#### Scenario: Installed skills tab shows skill cards

**Traces to**: User Story 14, Acceptance Scenario 1
**Category**: Happy Path

- **Given** 3 skills are installed
- **When** the Installed Skills tab loads
- **Then** 3 skill cards render with: name, version, verification status, description, author, agent assignment

---

#### Scenario: Channels tab shows enabled/available sections

**Traces to**: User Story 14, Acceptance Scenario 3
**Category**: Happy Path

- **Given** Telegram and Discord are enabled, Slack is available
- **When** the Channels tab loads
- **Then** "Enabled" section shows Telegram and Discord with status and Configure/Disable buttons
- **And** "Available" section shows Slack with an "Enable" button

---

### Feature: Create Agent

#### Scenario: Creating a custom agent

**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the user clicks "+ New Agent" on the Agents screen
- **When** the modal opens
- **And** the user enters name "DevOps Agent", selects Robot icon with Blue color, picks "claude-sonnet" model
- **And** clicks "Create Agent"
- **Then** a POST to `/api/v1/agents` is sent
- **And** the modal closes
- **And** a new "DevOps Agent" card appears in the agent grid

---

#### Scenario: Validation prevents empty agent name

**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Error Path

- **Given** the create agent modal is open
- **When** the user leaves the name empty and clicks "Create Agent"
- **Then** a validation error "Name is required" appears on the name field
- **And** no API request is sent

---

### Feature: Settings — Security & Policy

#### Scenario: Security settings page loads with current config

**Traces to**: User Story 10, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the user navigates to Settings > Security & Policy
- **When** the page loads
- **Then** sections render: Policy, Prompt Injection Defense, Rate Limits & Cost Control, Credentials Vault, Audit Log, Device Trust, Diagnostics
- **And** current values are populated from backend config

---

#### Scenario: Daily cost cap with progress bar

**Traces to**: User Story 10, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the daily cost cap is $10.00 and today's spend is $4.82
- **When** the Rate Limits section renders
- **Then** the cost cap shows "$10.00" with a progress bar at 48.2%
- **And** today's spend shows "$4.82"

---

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------|-------|---------|
| Unit | React components, Zustand stores, utility functions | Validates rendering logic, state transitions, data transformations in isolation |
| Integration | Components + TanStack Query + mock API | Validates data fetching, caching, mutation, and component interaction |
| E2E | Full browser + running gateway | Validates complete user workflows from UI to backend |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | test_websocket_message_parser | Unit | Scenario: User sends message, streaming response | Parses WebSocket JSON frames into typed objects (token, done, error, tool_call_start, tool_call_result, exec_approval_request) |
| 2 | test_chat_message_component_user | Unit | Scenario: User message appears optimistically | User message renders with text, timestamp, correct alignment |
| 3 | test_chat_message_component_assistant | Unit | Scenario: Streaming completes with markdown | Assistant message renders markdown (headings, code, lists) |
| 4 | test_thinking_indicator | Unit | Scenario: Thinking indicator shows | Thinking component shows/hides based on streaming state |
| 5 | test_streaming_cursor | Unit | Scenario: User sends message, streaming response | Blinking cursor appears during streaming, disappears on done |
| 6 | test_tool_call_badge_states | Unit | Scenario: Running tool call shows spinner | Tool badge renders running/success/error states correctly |
| 7 | test_tool_call_collapse_expand | Unit | Scenario: Expanding collapsed tool call | Tool badge toggles between collapsed and expanded views |
| 8 | test_tool_component_registry | Unit | Scenario Outline: Built-in tool custom component | Registry returns correct component for known tools, generic for unknown |
| 9 | test_exec_approval_block | Unit | Scenario: Approval block renders | Approval UI renders command, directory, policy, three buttons |
| 10 | test_exec_approval_actions | Unit | Scenario Outline: User responds to approval | Button clicks dispatch correct approval actions |
| 11 | test_session_bar_elements | Unit | Scenario: Session bar renders | Session bar renders agent selector, model, cost, tokens, heartbeat |
| 12 | test_agent_card_component | Unit | Scenario: Agent cards render | Agent card shows avatar, name, status, type badge |
| 13 | test_agent_profile_sections | Unit | Scenario: Agent profile renders | Profile page shows correct sections per agent type |
| 14 | test_create_agent_modal | Unit | Scenario: Creating custom agent | Modal fields, validation, submit flow |
| 15 | test_task_list_component | Unit | Scenario: Task list renders | Tasks grouped by status with correct data |
| 16 | test_task_board_component | Unit | Scenario: GTD board view | Kanban columns render with cards |
| 17 | test_status_bar_component | Unit | Scenario: Status bar shows overview | Status bar formats agent/channel/cost info |
| 18 | test_provider_card_component | Unit | Scenario: Configured provider shows status | Provider card shows connection status, models, edit option |
| 19 | test_skill_card_component | Unit | Scenario: Installed skills tab | Skill card renders name, version, status |
| 20 | test_session_panel_component | Unit | Scenario: Panel slides open | Agent/session hierarchy renders in accordion |
| 21 | test_sidebar_store | Unit | N/A (existing) | Sidebar pin state, open/close toggle |
| 22 | test_chat_store | Unit | Scenario: User sends message | Chat state: messages, streaming status, active session |
| 23 | test_agent_store | Unit | Scenario: Switching agents | Agent selection, active agent state |
| 24 | test_chat_streaming_integration | Integration | Scenario: User sends message, streaming response | Component + WebSocket mock → tokens render incrementally |
| 25 | test_message_history_load | Integration | Scenario: Previous messages load | Component + API mock → history loads and renders |
| 26 | test_agent_list_fetch | Integration | Scenario: Agent cards render | Component + TanStack Query → agents fetched and displayed |
| 27 | test_provider_save_connect | Integration | Scenario: Adding new provider | Component + API mock → key saved, test runs, status updates |
| 28 | test_task_crud | Integration | Scenario: Task list renders | Component + API mock → tasks fetched, created, updated |
| 29 | test_create_agent_flow | Integration | Scenario: Creating custom agent | Modal + API mock → agent created, list refreshes |
| 30 | test_approval_flow | Integration | Scenario Outline: User responds to approval | Approval block + API mock → action sent, status updates |
| 31 | test_e2e_chat_send_receive | E2E | Scenario: User sends message, streaming response | Full browser: type message → see streamed response |
| 32 | test_e2e_agent_navigation | E2E | Scenario: Agent card navigation | Full browser: agents screen → click card → profile loads |
| 33 | test_e2e_settings_provider | E2E | Scenario: Adding new provider | Full browser: configure provider → connection test → status green |
| 34 | test_e2e_session_switch | E2E | Scenario: Selecting a session | Full browser: session panel → click session → chat loads history |
| 35 | test_e2e_command_center | E2E | Scenario: Status bar, task list | Full browser: command center → status + tasks visible |
| 36 | test_cancel_button_states | Unit | Scenario: Cancel during streaming | Stop button appears during streaming, reverts to Send on cancel/done |
| 37 | test_cancel_preserves_partial | Unit | Scenario: Cancel during streaming | Partial response preserved with "interrupted" label after cancel |
| 38 | test_cancel_during_tool | Unit | Scenario: Cancel during tool execution | Tool badge shows "Cancelled" with orange indicator |
| 39 | test_cancel_idle_noop | Unit | Scenario: Cancel when idle | Cancel in idle state sends no WebSocket frame, no UI change |
| 40 | test_cancel_integration | Integration | Scenario: Cancel during streaming | Component + WebSocket mock → cancel sent, streaming stops, partial preserved |
| 41 | test_e2e_cancel_streaming | E2E | Scenario: Cancel during streaming | Full browser: stream response → click Stop → partial preserved |

### Test Datasets

#### Dataset: WebSocket Message Parsing

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `{"type":"token","content":"Hello"}` | Happy path | `{type: "token", content: "Hello"}` | Scenario: Streaming response | Standard token frame |
| 2 | `{"type":"done","stats":{"tokens":150,"cost":0.02}}` | Happy path | `{type: "done", stats: {...}}` | Scenario: Streaming completes | Done frame with stats |
| 3 | `{"type":"error","message":"timeout"}` | Error | `{type: "error", message: "timeout"}` | Scenario: WebSocket connection error | Error frame |
| 4 | `{"type":"token","content":""}` | Boundary | `{type: "token", content: ""}` | Edge case | Empty token (should not crash) |
| 5 | `{"type":"token","content":"<script>alert(1)</script>"}` | Security | Rendered as escaped HTML text | Edge case | XSS in token content |
| 6 | `{"type":"unknown_type"}` | Boundary | Ignored (no error) | Edge case | Unknown message type |
| 7 | `{"type":"tool_call_start","tool":"exec","params":{"command":"ls"}}` | Happy path | `{type: "tool_call_start", ...}` | Scenario: Running tool call | Tool call start frame |
| 8 | `{"type":"tool_call_result","tool":"exec","result":{"exit_code":0,"stdout":"..."}}` | Happy path | `{type: "tool_call_result", ...}` | Scenario: Successful tool call | Tool call result frame |
| 9 | `{"type":"exec_approval_request","command":"rm -rf /tmp","id":"appr_1"}` | Happy path | `{type: "exec_approval_request", ...}` | Scenario: Approval block renders | Approval request frame |
| 10 | `not valid json` | Error | Parse error handled gracefully, logged | Edge case | Malformed WebSocket message |

#### Dataset: Agent Card Rendering

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `{id:"general-assistant", type:"core", icon:"robot", color:"green", status:"active"}` | Happy path | Green Robot icon on green circle, "core" badge | Scenario: Agent cards render | Standard core agent |
| 2 | `{id:"omnipus-system", type:"system", icon:"custom-octopus", status:"active"}` | Happy path | Custom SVG, "system" badge | Scenario: Agent cards render | System agent |
| 3 | `{id:"custom-1", type:"custom", icon:"unknown-icon", color:"blue"}` | Boundary | Fallback Robot icon on blue circle | Edge case | Unrecognized icon name |
| 4 | `{id:"custom-2", type:"custom", icon:"robot", color:null}` | Boundary | Robot icon on default (gray) circle | Edge case | Missing color |
| 5 | `{id:"", name:""}` | Error | Should not render (filtered out) | Edge case | Empty agent data |

#### Dataset: Message Rendering

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `{role:"user", content:"Hello"}` | Happy path | User message bubble, right-aligned | Scenario: Previous messages load | Simple user message |
| 2 | `{role:"assistant", content:"# Heading\n\nParagraph with **bold** and `code`"}` | Happy path | Rendered markdown with heading, bold, inline code | Scenario: Streaming completes | Markdown rendering |
| 3 | `{role:"assistant", content:"```python\nprint('hello')\n```"}` | Happy path | Syntax-highlighted code block with copy button | Scenario: Streaming completes | Code block |
| 4 | `{type:"compaction", summary:"User discussed pricing..."}` | Alternate | Centered muted system message | Scenario: Compaction entries | Compaction entry |
| 5 | `{role:"assistant", content:""}` | Boundary | Empty message placeholder or hidden | Edge case | Empty content |
| 6 | `{role:"assistant", content: "A".repeat(100000)}` | Boundary | Renders without freezing | Edge case | Very large message |

#### Dataset: Provider Configuration

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | Valid Anthropic API key | Happy path | Connection succeeds, green status, models populate | Scenario: Adding new provider | Valid key |
| 2 | Empty API key | Boundary | Validation error, no request sent | Edge case | Empty input |
| 3 | Invalid API key (wrong format) | Error | Connection fails, error inline, red status | Scenario: Connection test fails | Invalid key |
| 4 | Valid key but network timeout | Error | Timeout error, red status, retry option | Edge case | Network issue |
| 5 | API key with leading/trailing whitespace | Boundary | Whitespace trimmed before saving | Edge case | Common user mistake |

### Regression Test Requirements

> No regression impact — new capability. All UI screens are currently empty placeholders being replaced with functional implementations. Integration seams protected by:
> - Existing `src/store/sidebar.test.ts` — sidebar state remains unchanged
> - Existing `src/components/ui/button.test.tsx`, `card.test.tsx`, `input.test.tsx` — base UI components unchanged
> - Existing `src/components/layout/Sidebar.test.tsx` — sidebar component unchanged
> - Existing `src/test/screens.test.tsx` — route structure unchanged (routes remain, content changes)
> - Existing `src/test/theme.test.ts` — CSS custom properties unchanged

---

## Functional Requirements

- **FR-001**: System MUST stream chat responses token-by-token via WebSocket (`ws://` upgrade from `/api/v1/chat/ws`) using JSON frames with a `type` field.
- **FR-002**: System MUST render streaming tokens incrementally with a blinking cursor indicator during active streaming.
- **FR-003**: System MUST render completed messages as formatted markdown (headings, code blocks, lists, tables, links) using react-markdown + remark-gfm.
- **FR-004**: System MUST display a thinking indicator between message send and first token arrival.
- **FR-005**: System MUST show inline error messages with retry capability when WebSocket connections fail, with auto-reconnect using exponential backoff (max 3 retries).
- **FR-006**: System MUST render user messages optimistically (before server acknowledgment).
- **FR-007**: System MUST populate the session bar with: agent selector, model display, heartbeat timer, cost, token count.
- **FR-008**: System MUST allow switching agents via the session bar dropdown.
- **FR-009**: System MUST load and render message history from session transcript partitions.
- **FR-010**: System MUST merge messages from multiple day partitions in chronological order.
- **FR-011**: System MUST render tool call badges with states: running (spinner), success (green check, collapsed), error (red X, visible error, retry button).
- **FR-012**: System MUST use custom components for known built-in tools and a generic JSON fallback for unknown tools.
- **FR-013**: System MUST render exec approval blocks with command, working directory, policy reference, and Allow/Deny/Always Allow buttons.
- **FR-014**: System MUST send approval decisions to the backend and update the block status.
- **FR-015**: System MUST display agent profile cards in a responsive grid on the Agents screen.
- **FR-016**: System MUST render agent profile pages with sections appropriate to agent type (system/core/custom per C.6.3.3).
- **FR-017**: System MUST provide a create agent modal with: icon picker, name (required), description, model selector.
- **FR-018**: System MUST display configured providers with connection status and support adding/editing API keys.
- **FR-019**: System MUST store API keys only in `credentials.json`, never in `config.json`.
- **FR-020**: System MUST display security settings with editable policy mode, cost cap, rate limits.
- **FR-021**: System MUST display gateway settings with bind address, port, auth token management.
- **FR-022**: System MUST display data/backup settings with storage stats and session retention config.
- **FR-023**: System MUST render the Command Center with status bar, attention section, task list (list/board views), and agent summaries.
- **FR-024**: System MUST support task board toggling between list view (default) and GTD kanban board view.
- **FR-025**: System MUST render the Skills & Tools screen with tabs: Installed Skills, MCP Servers, Channels, Built-in Tools.
- **FR-026**: System MUST provide a session/agent hierarchy slide-over panel for navigating between sessions.
- **FR-027**: System SHOULD render settings > routing & policies for channel-to-agent routing configuration.
- **FR-028**: System MUST use Zustand for all UI state (sidebar, modals, active session, selected agent).
- **FR-029**: System MUST use TanStack Query for all server state (agents, sessions, tasks, config, credentials).
- **FR-030**: System MUST use Phosphor Icons exclusively — no emoji in UI chrome.
- **FR-031**: System MUST be responsive across 3 breakpoints: desktop (>1024px), tablet (640-1024px), phone (<640px). Note: Breakpoints updated to Desktop >1024px, Tablet 640-1024px, Phone <640px (aligned with Tailwind's `sm` breakpoint). The BRD Appendix C Section C.5 specifies 768px as the phone/tablet boundary, but this spec uses 640px to match Tailwind v4's `sm` breakpoint, minimizing custom breakpoint configuration. This decision was applied consistently in the implementation (MIN-001 resolution).
- **FR-032**: System MUST support bi-directional WebSocket communication for chat, carrying all event types: token, tool_call_start, tool_call_result, exec_approval_request, done, error (server→client) and message, cancel, exec_approval_response (client→server). Additionally, the frontend MUST handle system-initiated interruption events: `timeout` (turn timed out mid-stream, partial content preserved) and `compaction` (context window compacted, summary provided). The frontend MUST also handle `task_status_changed` events for real-time task board updates. See `docs/protocol/websocket-protocol.md` for full frame schemas (MAJ-004).
- **FR-033**: System MUST allow the user to cancel an in-progress streaming response or tool execution by clicking a "Stop" button that replaces the Send button during streaming.
- **FR-034**: System MUST preserve partial responses after cancellation, displaying them with an "(interrupted)" label and `status: "interrupted"`.
- **FR-035**: System MUST complete cancel operations within 1 second of the user clicking Stop.
- **FR-036**: System MUST manage WebSocket connection lifecycle: connect, authenticate (Bearer token via first message), keep-alive (ping/pong), auto-reconnect on disconnect with exponential backoff.

---

## Success Criteria

- **SC-001**: First token renders within 200ms of WebSocket frame receipt at p95 on desktop Chrome.
- **SC-002**: All 5 main screens (Chat, Command Center, Agents, Skills, Settings) load data from the backend and render meaningful content (not empty placeholders).
- **SC-003**: Message history loads within 500ms for sessions with up to 100 messages at p95.
- **SC-004**: All unit tests pass (minimum 34 test cases covering all BDD scenarios including cancel/interrupt).
- **SC-005**: All integration tests pass (minimum 7 test cases covering data fetching flows).
- **SC-006**: Provider configuration flow completes end-to-end: add key → test connection → see models.
- **SC-007**: Agent CRUD flow completes end-to-end: create → see in list → view profile → edit → save.
- **SC-008**: No XSS vulnerabilities in rendered markdown or tool call results (verified by test dataset #5).
- **SC-009**: The UI renders correctly at all 3 breakpoints without horizontal scroll or content overflow.
- **SC-010**: Zero emoji in UI chrome — all icons are Phosphor components (verified by automated scan).

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|------------------|---------------|
| FR-001 | US-1 | Scenario: User sends message, streaming response | test_websocket_message_parser, test_chat_streaming_integration, test_e2e_chat_send_receive |
| FR-002 | US-1 | Scenario: Streaming response; Scenario: Thinking indicator | test_streaming_cursor, test_thinking_indicator |
| FR-003 | US-1 | Scenario: Streaming completes with markdown | test_chat_message_component_assistant |
| FR-004 | US-1 | Scenario: Thinking indicator shows | test_thinking_indicator |
| FR-005 | US-1 | Scenario: WebSocket connection error | test_chat_streaming_integration |
| FR-006 | US-1 | Scenario: User message appears optimistically | test_chat_message_component_user |
| FR-007 | US-2 | Scenario: Session bar renders | test_session_bar_elements |
| FR-008 | US-2 | Scenario: Switching agents | test_agent_store |
| FR-009 | US-3 | Scenario: Previous messages load | test_message_history_load |
| FR-010 | US-3 | Scenario: Multi-day session merges | test_message_history_load |
| FR-011 | US-4 | Scenario: Running tool call; Scenario: Successful tool call; Scenario: Failed tool call | test_tool_call_badge_states |
| FR-012 | US-4 | Scenario Outline: Built-in tool; Scenario: Unknown tool | test_tool_component_registry |
| FR-013 | US-5 | Scenario: Approval block renders | test_exec_approval_block |
| FR-014 | US-5 | Scenario Outline: User responds to approval | test_exec_approval_actions, test_approval_flow |
| FR-015 | US-6 | Scenario: Agent cards render | test_agent_card_component, test_agent_list_fetch |
| FR-016 | US-7 | Scenario: Agent profile renders | test_agent_profile_sections |
| FR-017 | US-8 | Scenario: Creating custom agent; Scenario: Validation | test_create_agent_modal, test_create_agent_flow |
| FR-018 | US-9 | Scenario: Configured provider; Scenario: Adding provider; Scenario: Test fails | test_provider_card_component, test_provider_save_connect |
| FR-019 | US-9 | Scenario: API key stored securely | test_provider_save_connect |
| FR-020 | US-10 | Scenario: Security settings page loads; Scenario: Daily cost cap | test_e2e_settings_provider |
| FR-021 | US-11 | Scenario: Gateway settings | test_e2e_settings_provider |
| FR-022 | US-12 | Scenario: Data & Backup settings | test_e2e_settings_provider |
| FR-023 | US-13 | Scenario: Status bar; Scenario: Task list; Scenario: Agent summary rows | test_status_bar_component, test_task_list_component, test_e2e_command_center |
| FR-024 | US-13 | Scenario: GTD board view | test_task_board_component |
| FR-025 | US-14 | Scenario: Installed skills tab; Scenario: Channels tab | test_skill_card_component |
| FR-026 | US-15 | Scenario: Panel slides open; Scenario: Selecting a session | test_session_panel_component, test_e2e_session_switch |
| FR-027 | US-16 | Scenario: Routing settings | N/A (SHOULD, lower priority) |
| FR-028 | US-1, US-2, US-13 | All interactive scenarios | test_chat_store, test_agent_store, test_sidebar_store |
| FR-029 | US-3, US-6, US-9, US-13, US-14 | All data-fetching scenarios | test_message_history_load, test_agent_list_fetch, test_provider_save_connect, test_task_crud |
| FR-030 | US-6, US-4 | All rendering scenarios | test_agent_card_component, test_tool_call_badge_states |
| FR-031 | All | All rendering scenarios | test_e2e_chat_send_receive (multi-viewport) |
| FR-032 | US-1, US-17 | Scenario: User sends message; Scenario: Cancel during streaming | test_websocket_message_parser, test_chat_streaming_integration |
| FR-033 | US-17 | Scenario: Cancel during streaming; Scenario: Cancel during tool execution | test_cancel_button_states, test_cancel_integration |
| FR-034 | US-17 | Scenario: Cancel during streaming preserves partial | test_cancel_preserves_partial |
| FR-035 | US-17 | Scenario: Cancel during streaming | test_cancel_integration, test_e2e_cancel_streaming |
| FR-036 | US-1 | Scenario: WebSocket connection error during streaming | test_chat_streaming_integration |

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|------------------|------------------------|---------------------|
| 1 | Backend API endpoints for agents, sessions, tasks, config, skills are currently 501 stubs. Wave 5a requires real endpoints. | Agent will implement the REST handlers on the Go backend alongside the frontend wiring. | Confirm: should Wave 5a include implementing the backend REST endpoints, or is that a separate wave? |
| ~~2~~ | ~~SSE event types for tool calls and approval~~ | **RESOLVED**: WebSocket carries all event types (token, tool_call_start, tool_call_result, exec_approval_request, done, error) on a single bi-directional connection. SSE is no longer used. | Resolved 2026-03-29. |
| 3 | The session bar "Sessions" trigger opens a slide-over. It's unclear if this is a separate route or an overlay component. | Agent will implement it as an overlay component (not a route), consistent with the sidebar pattern. | Accept assumption: overlay component, not a route. |
| 4 | TanStack Query cache invalidation strategy is not specified. | Agent will use standard query key invalidation on mutations (e.g., creating an agent invalidates the agent list query). | Accept assumption: standard TanStack Query patterns. |
| 5 | ~~RESOLVED~~ The BRD mentions `AssistantUI` for chat primitives. | **RESOLVED: Use AssistantUI.** Build a custom gateway runtime adapter that connects AssistantUI's chat components to our WebSocket endpoint. AssistantUI handles message rendering, streaming display, tool call components, and markdown. We write the adapter layer translating our WebSocket JSON frames to AssistantUI's expected format. |
| 6 | Agent profile page is described as "single-column scrollable" but responsive behavior for phone isn't specified. | Agent will use the same single-column layout at all breakpoints (naturally responsive). | Accept assumption: single-column layout is inherently responsive. |
| 7 | Task drag-and-drop in board view requires a library. | Agent will use `@hello-pangea/dnd` (maintained fork of react-beautiful-dnd) or native HTML5 DnD. | Accept assumption: agent picks the lightest viable library. |

---

## Evaluation Scenarios (Holdout)

> **Note**: These scenarios are for post-implementation evaluation only.
> They must NOT be visible to the implementing agent during development.
> Do not reference these in the TDD plan or traceability matrix.

### Scenario: Full chat round-trip with tool call
- **Setup**: Gateway running, Anthropic provider configured, General Assistant active.
- **Action**: Type "Search the web for the current price of Bitcoin" and send.
- **Expected outcome**: Message sends, thinking indicator appears, tokens stream in, a web_search tool badge appears (running → success), final markdown response includes search results, session bar cost and token count update.
- **Category**: Happy Path

### Scenario: Agent switch preserves session context
- **Setup**: Two agents active, both with existing sessions.
- **Action**: Chat with Agent A, switch to Agent B via session bar, then switch back to Agent A.
- **Expected outcome**: Agent A's previous messages are still visible when switching back. No message loss or duplication.
- **Category**: Happy Path

### Scenario: Provider key error recovery
- **Setup**: No providers configured.
- **Action**: Go to Settings > Providers, enter an invalid key, see error, correct the key, save again.
- **Expected outcome**: First attempt shows inline error. Second attempt succeeds. Status turns green. No stale error messages remain.
- **Category**: Happy Path

### Scenario: Network disconnection during streaming
- **Setup**: Gateway running, mid-stream response.
- **Action**: Kill the gateway process while tokens are streaming.
- **Expected outcome**: UI shows error state (not a blank screen or frozen cursor). Partial response is visible. Retry button appears. No console errors or unhandled promise rejections.
- **Category**: Error

### Scenario: Rapid message sending
- **Setup**: Chat screen open, gateway running.
- **Action**: Send 5 messages rapidly without waiting for responses.
- **Expected outcome**: All 5 user messages appear in order. Responses stream without interleaving or corruption. No messages are lost.
- **Category**: Edge Case
- **Note (MIN-007)**: This scenario tests a backend message queuing behavior that is not covered by any FR or BDD scenario in this spec. The backend-lead must ensure the WebSocket handler serializes concurrent requests per session (i.e., processes one message at a time, queuing subsequent sends). Without this, responses will interleave. This evaluation scenario is retained as a cross-boundary integration check rather than a pure frontend test.

### Scenario: Phone breakpoint navigation
- **Setup**: Browser resized to 375px width (iPhone SE equivalent).
- **Action**: Navigate through all 5 screens via hamburger menu.
- **Expected outcome**: All screens render without horizontal scroll. Session bar collapses to minimal mode. Sidebar is full-width overlay. Task board swipes between columns.
- **Category**: Edge Case

### Scenario: Settings persistence across page refresh
- **Setup**: Configured provider, changed security settings, modified retention.
- **Action**: Refresh the browser page.
- **Expected outcome**: All settings retain their saved values. Provider stays connected. Security policy stays as configured. No data loss.
- **Category**: Happy Path

---

## Assumptions

- The Go gateway backend is running and accessible at the configured host:port during all frontend operations.
- The WebSocket endpoint (`/api/v1/chat/ws`) will handle all chat event types bi-directionally: server→client (token, tool_call_start, tool_call_result, exec_approval_request, done, error) and client→server (message, cancel, exec_approval_response).
- REST API endpoints (currently 501 stubs) will be implemented either as part of Wave 5a or a prerequisite wave.
- The `@omnipus/ui` package structure is not yet created — Wave 5a implements directly in `src/` following the existing Vite + React setup.
- `credentials.json` encryption/decryption is handled by the Go backend; the frontend only sends plaintext keys to the API which encrypts before storing.
- The emoji-to-icon translator (C.3.2) is deferred to a separate task — it only applies to LLM output text and is not needed for the core UI wiring.
- Drag-and-drop for task board requires a third-party library (to be selected during implementation).

## Clarifications

### 2026-03-29

- Q: Should backend REST endpoints be implemented in Wave 5a or assumed to exist? -> A: **Resolved** — Wave 5a implements both frontend + backend together. Frontend-lead and backend-lead work in parallel. See Backend Note at top of this spec.
- Q: Should AssistantUI be used for chat or plain WebSocket + react-markdown? -> A: **Resolved** — Use AssistantUI with a custom gateway runtime adapter for our WebSocket protocol.
- Q: Are tool_call and approval events on the same stream? -> A: **Resolved** — WebSocket carries all event types on a single bi-directional connection. SSE replaced with WebSocket.
- Q: SSE vs WebSocket for chat streaming? -> A: **Resolved** — WebSocket. Enables bi-directional communication needed for cancel, approval responses, and future features.

### 2026-04-01

- Q: How should the UI handle system-initiated interruptions (timeouts, compaction) that arrive without a preceding user cancel? -> A: **Resolved (MAJ-004)** — Two additional server→client frame types are defined: `timeout` (backend timed out the turn mid-stream; partial content is preserved; UI renders partial with a muted "(timed out)" label analogous to cancel's "(interrupted)" label) and `compaction` (context window was compacted; UI renders a system message "Context compacted — older messages summarized" analogous to the compaction entry in message history). These must NOT be silently ignored — they are semantically significant events. Full frame schemas in `docs/protocol/websocket-protocol.md`.
- Q: How does the UI stay in sync with task status changes without polling? -> A: **Resolved (MAJ-004)** — The backend emits `task_status_changed` WebSocket frames when a task transitions state. The frontend WebSocket router handles this frame type by invalidating the relevant TanStack Query cache entry, triggering a re-render of the task board. Full schema in `docs/protocol/websocket-protocol.md`.
