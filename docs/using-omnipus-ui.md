# Using the Omnipus interface

A tour of the Omnipus web app: what you see from the sign-in screen onward, and where everything lives. Each stop that has its own page gets one sentence and a link — this page is the map, not the manual.

## What it is

The web interface is every screen of Omnipus in your browser: sign-in, sidebar, workspaces, chat, and settings. Installing the server is covered by [getting started](getting-started.md); the big picture belongs to [concepts](concepts.md).

The layout has two fixed points, and everything hangs off one of them:

```mermaid
flowchart LR
  SignIn[Sign-in screen] -->|opens| Workspace[Your default workspace]
  Bar[Sidebar] -->|lists| Workspaces[All your workspaces]
  Bar -->|links to| Screens[Admin chat, Agents, Connectors, Skills and Tools, Library]
  Workspace -->|panel toggles| Panels[Tasks, Calendar, Library, Mail, Team]
  Workspace -->|base page is| Chat[Chat: replies, tool calls, activity]
```

The diagram shows the whole shape: signing in drops you into a workspace, the sidebar reaches every workspace and every app-wide screen, and workspace panels open beside Chat, the base page.

## When you would use it

Open this page in your first week, or later when you are hunting a control you have seen once and cannot find again. To learn what a screen *does* rather than where it is, follow its link.

## How to get from sign-in to a running agent

1. Open the app's address in your browser. On the sign-in screen, enter your username and password, then select **Sign in**. A wrong combination is refused; too many attempts in a row asks you to wait. (A fresh install has no account yet — [getting started](getting-started.md) covers creating one.)
2. You land in your default [workspace](workspaces.md), on **Chat**, its base page. If setup is not finished, onboarding runs first.
3. Type in the message box at the bottom of the chat and press **Enter** to send; **Shift+Enter** adds a new line instead. The reply streams in as the agent writes it. The [agent](agents.md) is chosen in the sidebar, not beside the box. Above the box are the model and this chat's **Auto-approve** switch. On a wide window the session's token count sits on that same row.
4. While the agent works, you can steer or stop it (see the table below).
5. To hand the agent a file, use the **+** button next to the message box, drag a file onto the chat, or paste an image in.

## The sidebar

Unless you pinned it, the sidebar starts closed and the header shows **Show sidebar** (the hamburger). Show is visible only while the sidebar is off screen, and opening it never pins it: a click shows the sidebar over the page. To keep it there, **Pin sidebar** — inside the sidebar, shown on a window at least 1024 pixels wide — docks it beside the page and remembers that for your next visits; while pinned, a click outside no longer closes it. The same control then reads **Unpin sidebar**: it un-pins but leaves the sidebar open, so the next click outside closes it. While the sidebar is open but not pinned, **Escape**, a click outside it, or choosing a destination closes it (a pin you saved on a wider window stays saved). When focus is outside a field or text editor, **Cmd+B** (Mac) or **Ctrl+B** (Windows and Linux) hides a visible sidebar and shows a hidden one. Inside a field or editor, the shortcut stays with that control.

What the sidebar holds:

| Section | What you find there |
|---|---|
| Workspaces | Every active workspace. Selecting a name opens its chat. Under the name are that workspace's chat agents, not recent conversations. Workers are not listed. Admin is listed only on the default workspace, even when Admin is not on the team. Select an agent's name to open that agent's main chat. **Past sessions** opens earlier chats for that workspace and that agent. **New chat** starts an extra chat with that agent and leaves the main chat in place. The control beside the name is **Show <workspace> agents** or **Hide <workspace> agents**. The agent names stay listed either way: that control switches the waiting cue and refreshes the team list. The **+** creates a workspace once you type a name. **Archive** reaches the retired ones. |
| Assets | **Admin chat** for the standalone operator; app-wide screens: [agents](agents.md), [tools](tools.md) (the Skills & Tools screen), [connectors](connectors.md), and the [library](library.md) panel for files and [knowledge base](knowledge.md) notes. |
| Account menu | Your username at the very bottom opens notifications, [usage](#where-settings-and-account-live), [profile](#where-settings-and-account-live), [settings](settings.md), and sign out. |

The magnifier at the top of the sidebar searches conversations across workspaces, with no workspace or agent filter. **Past sessions** opens that same search already limited to one workspace and one agent. Each limit shows as a filter you can remove on its own.

The agents stay listed whether the workspace is collapsed or expanded. Collapsed, a workspace shows an amber dot and the words **1 main chat needs your attention**, or the number of main chats, when that count is known and at least one needs you. The dot pulses unless reduced motion is on; the cue stays, the pulse does not. Expanded, that count is replaced by an amber halo on the agent's icon, and the halo follows the same reduced-motion rule. The cue is only for a main chat. Opening that main chat and letting its history finish loading, while the tab is visible and that chat is the one in front, asks Omnipus to clear it. The dot or halo disappears when the server records that you have seen it. An extra chat, a helper, or a tab in the background does not ask, and opening the chat does not clear the cue by itself.

If the count cannot be known, the sidebar says **Attention unavailable** and offers **Retry**, and it does not show zero. If a main chat cannot be identified, it says **Main chat unavailable** and offers **Retry**. Selecting an agent whose main chat is not in the list says the same under that row. If the team list fails before any agents have loaded, an expanded workspace says **Could not load team** and offers **Retry**. A failure that still has an earlier list keeps showing that list.

Opening a workspace shows **Restoring your conversation…** while Omnipus chooses the chat. It opens the conversation this browser last opened in that workspace, when that conversation still belongs there. If there is none, or it was deleted, or it belongs to another workspace, Omnipus opens Ava's main chat. If the conversation list cannot be loaded, a warning says **Could not restore your last conversation. Retry to try again.** and offers **Retry**, which tries that choice again. The chat already on screen stays open, and Omnipus does not switch to Ava. When there is no remembered conversation and Ava's main chat cannot be identified, the message box says **This chat is unavailable right now**, **Retry** tries again, and sending is turned off.

If the sidebar shows **Could not load workspaces**, select **Retry** to reload the workspace list and **Archive** when it is expanded. If loading fails again, the message stays so you can retry again. If loading the list pauses because you are offline, the sidebar says **Offline — workspaces will load when you reconnect.** and loads the list once you are back online.

## A workspace and its panels

A [workspace](workspaces.md) is where one effort lives: conversations, task board, files, team. Switch workspaces from the sidebar. Chat is the base page, not a workspace-bar entry. The bar offers five panel toggles, in this order:

| Panel | What it is for |
|---|---|
| Tasks | The workspace's work as a [board, list, or graph](tasks.md), including its [plans](plans.md). |
| Calendar | [Scheduled and recurring work](calendar.md), shown by when it fires. |
| Library | The workspace's files and notes; opens the [Library](library.md) panel. |
| Mail | The workspace agents' configured mailboxes; opens the [Mail](mail.md) panel. |
| Team | Who is on this workspace's team and which [agent](agents.md) may delegate to which. |

Select a toggle to open its panel beside Chat, select it again to close it, or choose another to replace the open panel. The header uses the widest layout that fits the space actually available, leaving room for **Open browser**. Opening a panel, pinning or hiding the sidebar, resizing the window, or changing the font size can change that space.

| Available space | Workspace header |
|---|---|
| Enough for the full row | The workspace name, then **Tasks**, **Calendar**, **Library**, **Mail**, and **Team**, each with an icon and label. Select the workspace name to open its settings. |
| Enough for icons, but not all labels | The workspace name stays; the five panel toggles show icons only. Hover an icon or focus it with the keyboard to see its label. The open panel's icon stays gold. Select the workspace name to open settings. |
| Too narrow for the icon row | The workspace name and a downward caret open a menu with **Settings** and the same five panel toggles. Select the name, then the item you want. |

The hamburger means **Show sidebar** only; there is no second hamburger for panels. Panel names and keyboard access stay the same in all three layouts. Workspace settings remain a page, not a panel.

## The chat area

The chat is a conversation with the workspace's agents. Above the messages, a status line names the open conversation: **Main chat** (nothing after it), **Extra chat**, or **Extra chat —** plus the title when that chat has one, **Task run**, or **Helper**. That line stays while older messages are loading. A task transcript that is not the conversation this workspace remembers still shows **Task:** and the task title. Replies stream in; each tool an agent uses appears as a card you can expand or collapse ([tools](tools.md) explains what agents can do). When an agent wants to do something sensitive, an approval dialog asks you to approve it once, deny it, or always allow it — [security](security.md) covers the rules behind it. Each active [goal](goals.md) shows as its own small pill under the message box.

A failure or refusal routed to an existing chat appears in that conversation, including when no reply has started. It does not turn into an app-wide connection banner when you open another conversation or start a new chat. Connection and routing-protocol failures remain separate app-wide problems.

**Open browser** at the top of the chat shows the agent's [live browser](browser.md). Library is in the sidebar, and opens every workspace's files. When an agent builds something reviewable, like a small site, the chat links to it ([previews](previews.md)).

While a turn is running, the message box stays yours:

| Control | What it does |
|---|---|
| **Enter** (with text typed) | Sends your message into the running turn — the agent takes it into account without stopping. A send button with the same effect appears next to Stop. |
| **Stop** or **Escape** | The first activation asks only this chat's current turn to stop and shows **Stopping...**. A second activation in the same chat within three seconds confirms Stop all, including its helpers. |
| Activity pill | Below the message box: the Agents number counts helpers whose current state is running. Select it to open the Activity panel. **Running now** excludes stopped and finished helpers; queued helpers and helpers waiting for an answer have their own sections. Stopped helpers remain inspectable with **Open**, without a growing elapsed timer or a failed-work label. A resumed helper returns to Running now when its state is running again. |

### Stop and redirect commands

Three slash commands in the message box control the current chat directly:

| Command | Where it works | What it does |
|---|---|---|
| `/stop` | Any chat | Exactly one **Stop** activation: first asks only this conversation's current turn to stop and opens the same three-second confirmation window. |
| `/cancel` | Any chat | Immediately requests Stop all for this chat and every helper below it. It never stops a parent or sibling chat. |
| `/stop-redirect <instruction>` | Any chat, root or helper | Stops this chat's current turn, then continues this same chat with your instruction — for example `/stop-redirect focus on the failing tests`. Its helpers keep working; the command does not target one of them. |

After a first **Stop**, **Escape**, or `/stop`, the same Stop button stays available for three seconds. Press Stop or Escape again in that window to confirm Stop all; `/stop` follows the same activation rule. There is no separate **Stop all** button. The window closes after three seconds, when the window loses focus, or when you switch chats; the next activation is a first, current-chat-only Stop again. `/cancel` needs no second activation.

`/stop-redirect` without an instruction (or with only spaces) replies with usage and changes nothing. The instruction is required and sent as typed after the command, with surrounding spaces removed. If no active session is attached to the current chat, a visible message asks you to reopen it; no redirect is sent. If the connection is down, the redirect cannot be sent: a visible error says so, this chat's turn keeps running, and you can run the command again once you are reconnected.

### If the first message loses its connection

Your first message is kept in the open tab while Omnipus checks delivery. If loading a saved chat's history fails, the message list may temporarily show **Could not load messages.** instead; select that screen's **Retry** to reload the history. Reconnecting does not resend the message automatically. **Retry** is available only while connected; delivery Retry uses the original message and delivery ID, not the current agent, model, attachment or Auto-approve choices.

| Message status | What it means and what you can do |
|---|---|
| **Sending…** | Waiting for confirmation that the first message was saved. |
| **Delivery not confirmed · Retry** | Omnipus cannot yet confirm whether the message was saved. This can follow a lost connection or acknowledgement, an uncertain save, or an older gateway's acknowledgement that does not identify your message. Select **Retry** while connected; reconnect first if needed. |
| **Checking delivery…** | Your explicit Retry is in progress. Another Retry is not available at the same time. |
| **Saved** | The server confirmed that this message was saved using its delivery ID, directly or through the saved chat's history. A chat-created acknowledgement alone does not confirm a save. The answer may still be starting or running. |
| **Checking chat…** | Omnipus knows the chat ID and is loading its history to check whether the first message was saved and whether an answer exists or is still running. |
| **Could not check this chat · Retry** | Checking delivery or loading the saved chat failed. This does not establish whether an unconfirmed message was saved. Your message is kept in the open tab; **Retry** uses the original delivery request until a save is confirmed, even if a chat ID is known. After a confirmed save, it checks the saved chat again. |
| **Could not save message · Retry** | Omnipus reported that it could not save the message. You can retry delivery. |
| **Message saved, but no answer started · Generate again** | The message was saved, but no answer began. **Generate again** deliberately starts a new answer in the saved chat. |
| **Couldn't finish · Generate again** | Checking the recovered chat finished with your message still unanswered and no answer running. You can deliberately generate a new answer. |

An older gateway's acknowledgement can still open the chat and apply its agent and Auto-approve settings without showing **Saved**. Messages buffered while that chat was being created resume after the current turn ends.

An unconfirmed message does not offer **Generate again**. That action is separate from delivery Retry: it sends a new request and may repeat work or tool actions. The saved chat's current Auto-approve setting applies, not a restored setting from the original send.

The slash menu and `/help` do not list `/new` or `/clear`. Typing `@` does not switch the agent; `@` is ordinary text. Another conversation is **New chat** on that agent's sidebar row. When the current first message has no known chat ID, **New chat** asks **Start a new chat?** and shows **Delivery not confirmed. Copy your message before starting a new chat.** Choose **Keep this chat** to stay, or **Start a new chat** to leave it. Copy the message first: that bubble will not be in the new chat. The new chat is an extra chat, so the agent's main chat stays in place. If a chat ID is already known, **New chat** starts the extra chat without this question.

**Limits:** recovery before a chat ID is known lives only in the open tab; a full browser reload can lose it. The gateway remembers delivery IDs only for its current process, and only for a limited number of recent first messages: up to 256 per account and 10,000 in total. When those limits are reached, the oldest are forgotten first, so a Retry of a very old unconfirmed first message can create a second chat and answer twice, exactly as after a restart. If it crashes after saving the message but before acknowledging it, Retry after that restart can likewise create a second chat and answer twice. Reusing a delivery ID for a different message is refused with a conflict error instead of being treated as the same message. Delivery Retry is not a guarantee of exactly-once answers or tool actions. After a gateway upgrade, reload any browser tab that was already open: an older tab does not understand the new delivery messages, so starting a new chat there can appear to hang until the page is reloaded.

## Panels beside chat

[Tasks](tasks.md), [Calendar](calendar.md), [Library](library.md), [Mail](mail.md), and Team open from the workspace bar or its compact panels menu and use the shared panel controls, as does [Browser](browser.md). **Library** is also in the sidebar. **Open browser** at the top of the chat opens the agent's live Browser panel when a browser session is available. Only one panel sits beside Chat at a time. Choosing another replaces it; if the outgoing editor reports protected unsaved changes, you are asked before it leaves.

| Control | What it does |
|---|---|
| Panel heading and **Close** (X) | Shows which panel is open; X closes it and returns focus to Chat. Pressing **Escape** also closes a panel (not Browser), except while you are typing in a field or a dialog is open; in a full-screen panel it behaves like **Back to chat**. On a narrow screen, the panel replaces the chat area and also has **Back** at the top to return to Chat. |
| Divider between Chat and the panel | Drag it to change the panel's width. You can focus the divider and press the left or right arrow key; **Home** and **End** jump to its narrowest and widest sizes. Double-click it to reset the width. |
| **Expand** icon (named **Expand** followed by the panel's name, for example **Expand Library panel**) | In a normal browser tab, Library, Browser, Mail, Tasks, Team and Calendar open their full-screen view in a new browser tab, without the app sidebar or workspace bar. When Omnipus recognises an existing tab for the same panel and workspace (or the same Browser session and agent), it focuses that tab or offers **Switch** (or asks you to switch manually) instead of opening another. Reuse depends on Omnipus still recognising that tab; it is not guaranteed if cross-tab communication is unavailable. Reloading or closing the original Chat tab does not close an expanded panel tab. After a reload, Omnipus can offer Switch when cross-tab communication still recognises that panel; the browser may require you to switch tabs manually. After opening or reusing a tab, the docked panel closes in the source tab. If pop-ups are blocked, the docked panel stays open and Omnipus shows a short notice, for example **Library was blocked. Allow pop-ups and try again.** If opening the tab fails with an error instead, the panel stays open and a short notice says, for example **Library could not open full screen. The panel remains here.** |
| **Back to chat** in any full-screen panel | In a normal browser tab, **Back to chat** tries to close the full-screen tab. A tab that Omnipus opened can restore the panel beside Chat only in its original tab while that tab still owns the expanded panel, when that tab is still available and no different panel is open there. Reloading the original Chat tab releases that ownership, so **Back to chat** no longer restores the panel there automatically after the reload; you may need to reopen the panel. Restoration uses the last context received by the original tab. If cross-tab context updates are unavailable, it can restore an older selection. A tab that only switched to or reused an existing full-screen tab does not get that restoration. If the full-screen tab cannot close itself, the button opens workspace Chat with the panel open in that same tab. If Chat cannot be opened, an error stays visible so you can try again. |

**Installed app windows:** when Omnipus runs as an installed app or standalone Chrome window, **Expand** opens the full-screen panel in that same window, not another window. **Back to chat** returns to the workspace view in the same window with the panel reopened. Mail keeps its current mailbox, folder and selected message; Browser keeps its session and agent; Tasks, Team and Calendar keep their workspace. Library returns without reopening the selected file; select it again. If a workspace is selected in Library, it reopens that workspace. If Library is showing the all-workspaces root, returning instead opens Library in the Chat workspace; select the **Library** breadcrumb to show all workspaces again. On wider layouts the reopened panel sits beside Chat. On narrow layouts it takes Chat's place; use the panel heading's **Back** or **Close** control to reveal Chat. Library and Mail still ask before expansion or return when their editors have protected unsaved changes; cancelling keeps you where you are. This also applies to minimal-UI, window-controls-overlay and full-screen display modes. Normal browser-tab expansion is unchanged.

Tasks and Calendar follow the workspace in their full-screen tab's address, including browser Back and Forward. When child-context updates reach the original tab, its recognised workspace mapping follows too: after a child moves from workspace A to B, choosing the panel for B tries to switch to it and choosing it for A can open a docked panel at A. If cross-tab updates are unavailable, the original tab may retain an older mapping. Automatic restoration to the original Chat tab requires that tab to still own the expanded panel and have no different panel open; it uses the last workspace context that the original tab received. Installed-app returns and the same-tab fallback described above do not require an original-tab owner.

Library and Mail ask before an app-initiated close, panel switch, expansion, or full-screen return when their editors report protected unsaved changes; cancel the prompt to stay. If the unsaved-change check fails during expansion, the panel stays open and an error notice asks you to try again—for example **Library could not expand because its unsaved-change check failed. Try again.** In Mail, that covers changed drafts and Compose messages with a nonempty body, **not** a new Compose message with only recipients or subject filled in. The Browser panel has no equivalent text-edit warning. See the [Mail guide](mail.md#work-with-drafts) for Save and draft-sending steps.

## Where settings and account live

App-wide settings live behind **Settings** in the account menu, on tabs from providers and models to security, data, and chat behavior; [settings](settings.md) walks each one. Opening Settings stays on Settings, including while the app is still opening your workspace after sign-in. Your **Profile** (preferences, password, and what agents should know about you) and **Usage** (token history) are separate entries in the same menu. Settings for one workspace live in that workspace, under its name.

## Limits and things to watch

- The sidebar starts closed unless you pinned it. **Show sidebar** (the hamburger in the header) appears only while the sidebar is off screen and shows it without pinning. **Pin sidebar**, inside the sidebar, appears at 1024 pixels and wider and docks it for this and future visits; **Unpin sidebar** un-pins but leaves it open, so the next click outside closes it. While it is open but not pinned, Escape, a click outside, or choosing a destination closes it, and a pin saved on a wider window stays saved. When focus is outside a field or text editor, **Cmd+B** (Mac) or **Ctrl+B** (Windows and Linux) hides a visible sidebar and shows a hidden one. Inside a field or editor, the shortcut stays with that control.
- Sending mid-turn steers the running turn; it does not queue a message for afterwards. To let the agent finish first, wait for the reply before sending.
- Stop is a request, not a switch: the agent halts where it is, and work already finished stays finished.
- The activity pill disappears when everything has ended successfully. A failed background item keeps it visible, so failures do not vanish silently.
- The **Library** and **Mail** entries open panels beside Chat on wider screens, rather than separate workspace pages; on narrower screens a panel takes over the chat area.
- For readability, some routine tool activity is hidden from the conversation by default. **Settings**, then **Chat**, then **Verbose chat** reveals every call.
- If a turn ends in an error after text has started streaming, the failure message replaces that text in the same reply. The message is visible even with **Verbose chat** off; **Verbose chat** shows technical details when available. A turn you stop still keeps its partial reply. **(interrupted)** belongs to that reply's message, not to the whole conversation; a later successful answer does not inherit it. Likewise, **(cut off at the output limit)** appears only on the affected reply.
- An agent saying what it plans to do is not a finished answer. If it reaches its tool-call limit before a final answer, chat keeps the narration and adds a separate limit notice. The notice remains in the saved conversation when you reopen it. An admin can raise **Max tool calls per turn** under **Settings → Performance**; an agent's own lower limit is on its profile's **Advanced** tab.
- If conflicting tool names stop a turn, chat shows the standard failure notice instead of ending without an explanation: “This turn didn’t finish, and we can’t tell why. Retry — if it keeps happening, open Verbose chat for details, or try a different model.” Any earlier narration and tool activity remain in the saved conversation, followed by the failure notice.

## Related pages

- [getting-started](getting-started.md) — install, create the account, finish onboarding.
- [workspaces](workspaces.md) — what a workspace is and what lives inside one.
- [agents](agents.md) — the roster, the default agent, building your own.
- [tasks](tasks.md) — the board, list, and graph views, and plans.
- [tools](tools.md) — what agents can do, and the permissions that gate them.
- [mail](mail.md) — read, compose, and manage drafts in the Mail panel.
- [browser](browser.md) — watch or drive an agent's live browser in its panel.
- [settings](settings.md) — every settings tab in detail.
