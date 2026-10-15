# Group chats

A group chat (also called a room) is one conversation between you and two or more agents of the same workspace. You write once, and every addressed agent answers. Agents can also talk to each other there.

> **Status in this version: the group-chat screens are switched off.** The server part of group chats is built. The screens are not shown yet (issue #1314): there is no "Group chats" section in the sidebar, no "Add participant" button and no room page, and a direct room link opens the workspace's chat instead. Sections marked **(screen switched off)** describe what those screens do once they are turned on. Until then, group chats can only be used through the web app's REST API ([API summary](#api-summary) at the end).

**Glossary.** A group-chat room is not a memory room. The *shared* and *private* memory rooms of the `remember` and `recall_memory` tools are where agents keep notes ([memory](memory.md)). They have nothing to do with group chats.

## The three kinds of group chat

| Kind | Who creates it | Members | Can you delete it? |
|---|---|---|---|
| **Team room** | Created automatically the first time the workspace has two conversation agents | Every conversation agent of the workspace. It follows the team on its own as agents join or leave | No |
| **Squad** | You. You give it a name (1 to 80 characters) | Two or more agents you choose | Yes |
| **Ad-hoc room** | You. No name needed: it is named after its members, for example "Mia, Jim" | Two or more agents you choose | Yes |

A conversation agent is an agent you can chat with directly. Workers, system agents and Omnipus never join a group chat. A group has at most 256 agents. The workspace lists its team room first, then squads, then ad-hoc rooms, each oldest first.

### At least two agents

Every group chat needs at least two agents.

| Situation | What happens |
|---|---|
| You create a group with fewer than two different agents | Refused: "a room needs at least two agents" |
| You remove a member and fewer than two would be left | Refused: "a room needs at least two agents — delete the room instead". Muted members count toward the two |
| An agent leaves the workspace, and a squad or ad-hoc room is left with one agent | The group becomes read-only: "This group has fewer than two agents — add another agent to continue." Nothing is deleted |
| The workspace drops below two conversation agents | The team room is hidden and read-only: "The team room needs at least two conversation agents in this workspace." Its history, mutes and cost are kept, and it comes back with all of them when a second agent joins |

You cannot add or remove team-room members directly. Change the workspace's team instead: "Team room members are the workspace's agents — add the agent to the workspace". Muting works in the team room as in any other group.

### One workspace only

A group chat holds agents of exactly one workspace. Adding any other agent is refused with "<Agent> is not in <Workspace>". When an agent is removed from a workspace, it leaves every group chat of that workspace, and its requests that have not started yet are cancelled ("cancelled — removed"). This happens when the workspace's team is saved, and again whenever a group is next opened.

## Who receives your message

| You write | Who receives it |
|---|---|
| No @-name | Every member that is not muted (the `@all` default) |
| `@all` | Every member that is not muted |
| `@Jim plan it` | Only Jim, even if Jim is muted |
| `@Jim` and `@all` together | Jim plus every member that is not muted |

How a typed @-name is matched:

- It must match a member's full current name. Upper or lower case does not matter. When several names fit, the longest one wins.
- A name that several members share is refused: "Several members are called <name> — pick from the list".
- An @-name that matches no member stays plain text. If none of the message's @-names matched anyone, nobody was picked and there is no `@all`, the message is saved in the group but sent to no one.
- `@all` is reserved. A member actually named "all" can only be picked from the @-picker (screen switched off).

A message can be up to 65,536 bytes long. Your operator can change this with `session_messaging.steer_body`, the same limit that applies to messages between sessions.

## How agents answer: from their own main chat

No agent works inside a group chat. Each agent's main chat is its one-to-one chat with you in that workspace, and that is where it answers.

1. Each addressed member gets your message as a request in its own main chat. The request says who wrote it and quotes the group messages that member has not seen since its previous request (at most 20 messages and 8,192 bytes). The quoted messages are marked as quotes, not instructions.
2. The agent works on it there, with its own tools, permissions and settings. A new request never joins a turn that is already running. It waits for the agent's next turn. A held request is delivered with the turn that starts the agent again.
3. The agent answers with its `answer_request` tool. The answer is posted in the group under the agent's name and copied once into its main chat. A plain reply in its main chat does not reach the group.
4. If a turn ends without an answer, the agent gets one reminder. If it still does not answer, the group shows the member as "failed: no answer".

**In the main chat (screen switched off).** Once the screens are on, the request and the answer each appear in the agent's main chat as a collapsed entry, "From group <name>: <first line>". Expanding one shows the full text and a link to the group. In this version they appear as ordinary messages.

### Approvals and questions

- **Approving the request itself.** If an agent's `send_message` permission is set to Ask, a group request needs your approval before it reaches that agent. The approval is raised for that agent and labelled "for group <name>", and the group shows the member as awaiting your approval. If the permission is Deny, the request is refused.
- **Approvals during the work.** Approvals and questions the agent raises while working on a group request belong to its main chat, not to the group. While one is open, the group shows the member as waiting for you.
- **Where they appear in this version.** Approvals still appear in the app-wide approval dialog. That dialog does not show the "for group <name>" label. The card inside the agent's main chat is switched off until later work lands.
- **Not supported yet.** Secret requests from group work are not handled in this version.

An agent's message in a group is only ever text for the other members. It can never approve anything, answer a question card or run a command.

### Member states

The group shows one state per member: its most recent request. The labels in this table are the ones the room screen uses (screen switched off). The server sends the state names in brackets.

| Label | Meaning |
|---|---|
| sending… (`recorded`) | Saved in the group, on its way to the agent |
| queued (`admitted`) | In the agent's main chat, waiting for its next turn |
| held — <Agent> is stopped (`held`) | The agent's main chat is stopped. The request waits and is delivered when the agent runs again |
| awaiting your approval — in <Agent>'s main chat (`awaiting_approval`) | The agent's Ask permission needs your decision first |
| working (`working`) | The agent's turn is working on it |
| waiting for you in <Agent>'s main chat (`waiting_on_user`) | The agent asked you something, or needs an approval, while working on it |
| answered | The answer is in the group |
| refused | The agent's permission refused the request |
| failed (`failed`) | The turn failed, or ended with no answer after the reminder |
| interrupted | The turn was stopped, or Omnipus restarted while it ran. Interrupted work is never re-run |
| unavailable (`unavailable`) | Not delivered. The reason is shown, for example "rate limit", "not a member" or "storage" |
| cancelled | Cancelled by `/pause`, by removing the member, or by deleting the group |
| unknown | After a restart, Omnipus could not prove what happened |

## Agents talking to each other

- An agent's answer goes only to the members it @-names.
- `@all` in an agent's answer goes to every other member that is not muted, never back to the agent itself.
- An answer with no @-names reaches no other agent.
- There is **no limit** on how many times agents can reply to each other. This is a deliberate choice. You stay in control with `/pause`, and the cost is always visible.

**A rate limit slows things down; it does not cap them.** Each sender and member pair gets at most 60 requests a minute. You count as one sender, and each agent counts as one sender. Your operator can change the number with `session_messaging.steer_rate`. A request over that pace is not delivered, and the member shows "unavailable: rate limit". The limit does not cap how many replies a conversation can have in total. Sending the same message again (Retry) delivers it to the members that did not get it.

### Allocated cost

Each group shows an allocated cost: what its members' turns spent on the group. The header shows it once the screen is on (screen switched off). The server sends it with every group.

- **How it is shared.** A turn's cost is split by how much of that turn's input came from this group. A turn that worked on one group message and one direct message counts half to the group.
- **Each turn counts once.** Shares are rounded to the nearest millionth of a dollar.
- **Missing prices.** A turn on a model with no known price adds nothing, and the cost is marked as incomplete ("unpriced").

## Room commands

Only you can use commands in a group. An agent's text is never a command.

| Command | What it does |
|---|---|
| `/pause` | Stops the group from sending to agents (see below) |
| `/resume` | Starts sending new messages again |
| `/mute @Agent` | The agent no longer receives your default or `@all` messages. It still answers when @-named, stays a member and still counts toward the two-agent minimum |
| `/unmute @Agent` | Undoes `/mute` |
| `/members` | Lists the members with their state and mute status |
| `/help` | Lists these six commands |

The server accepts `/members` and `/help` and changes nothing. The room screen shows the list itself (screen switched off).

**Why steering commands are refused.** Any other command, such as `/model`, `/stop` or `/goal`, is refused with "Rooms don't steer agents — use <Agent>'s main chat". The refusal names the @-named members, or every member if you named none, and links to their main chats. A group chat is a conversation, not a control panel. Steering commands change how one agent works, so they belong in that agent's main chat, where they affect only that agent.

### /pause and /resume

- `/pause` cancels every request that has not started yet. That includes queued requests, held requests and requests awaiting your approval; their pending approvals are withdrawn.
- Work already running in an agent's main chat is not touched. It finishes, and its answer is posted in the group marked **after pause**. Any @-names in that answer are not passed on.
- Messages you write while the group is paused are saved in the group but not sent. `/resume` does not send them either; it only affects new messages.
- After `/resume`, an answer to work started before the pause is still marked "after pause".

## Add participant (screen switched off)

"Add participant" sits at the top-left of a chat header and adds agents only. It never adds people from your chat platforms.

| Where you use it | What happens |
|---|---|
| In an agent's main chat | Creates a new ad-hoc room with that agent and the one you pick. The room opens empty, with a link back to the main chat. The main chat itself stays one-to-one |
| In a squad or ad-hoc room | Adds the agent to the group |
| In the team room | Disabled: "Team room members are the workspace's agents — add the agent to the workspace" |
| In Omnipus's chat | Not shown |

The server checks that a room started this way really comes from a main chat of that workspace. If not, it refuses: "the chat this group is started from is not a main chat of <Workspace>".

## Deleting a group

You can delete squads and ad-hoc rooms. The team room cannot be deleted. When you delete a group:

- Its requests that have not started yet are cancelled.
- Work already running finishes in the agents' main chats. Its answer is not posted, and the agent is told that the group is no longer available.

## Opening a group: "Earlier messages aren't shown"

When a group is opened or reconnects, the server sends at most the newest 10,000 messages. If there are older ones, it says so. The room screen is meant to show a quiet line, "Earlier messages aren't shown". That line is not built yet, and there is no scrolling back to older messages. The full history stays saved on the server.

## Audit

Group changes are written to the audit log as `room.changed` events. Each event records the room, the workspace, the action, the agents involved and, for changes you made, your user name.

| Action | When |
|---|---|
| `created` / `deleted` | You created or deleted a squad or ad-hoc room |
| `members_add` / `members_remove` | You added or removed members |
| `members_mute` / `members_unmute` | Someone was muted or unmuted, through the API or with `/mute` and `/unmute` |
| `team_room_created` | The workspace got its team room |
| `members_joined_workspace` / `members_left_workspace` | Group membership followed a change to the workspace's team |

## Limits in this version

| Limit | Detail |
|---|---|
| Screens switched off | No sidebar section, room page, "Add participant" or collapsed main-chat entries yet (#1314) |
| No older history | Only the newest 10,000 messages are sent when a group opens |
| Approvals | Shown in the app-wide approval dialog, without the "for group" label |
| Secret requests | Not handled for group work |
| Deleting a workspace | Does not delete its group chats yet |

## API summary

For developers. Every route needs a signed-in web user.

| Route | What it does |
|---|---|
| `GET /api/v1/workspaces/{id}/rooms` | Lists the workspace's groups (team room first) |
| `POST /api/v1/workspaces/{id}/rooms` | Creates a squad or an ad-hoc room |
| `GET /api/v1/rooms/{id}` | Reads one group |
| `DELETE /api/v1/rooms/{id}` | Deletes a squad or an ad-hoc room |
| `PATCH /api/v1/rooms/{id}/members` | Adds, removes, mutes or unmutes members |
| `POST /api/v1/rooms/{id}/messages` | Sends a message or a room command (answers 202 once saved) |

Live updates arrive over the web app's WebSocket after an `attach_room` frame. The exact request and response shapes are in `contracts/openapi.yaml` and `contracts/asyncapi.yaml`.

## See also

- [Workspaces](workspaces.md) — the team a group chat draws its agents from
- [Memory](memory.md) — memory rooms, which are not group chats
- [Security](security.md) — permissions, approvals and the audit log
