# Group chats

A group chat (also called a room) is one conversation between you and two or more conversation agents of the same workspace. You write once, and Omnipus creates a request for each addressed member, subject to that member's permissions and availability. Agents answer from their own main chats and can address other members in their answers. Addressing an agent does not guarantee an answer; the member states below show what happened.

**Where to find them.** Each workspace in the sidebar has a **Group chats** section under its agents, listing its groups (team room first, then squads, then ad-hoc rooms). Its **+** menu offers **New squad…** and **New quick group…** (an ad-hoc room). Selecting a group opens its room page. **Add participant** at the top-left of an agent's main chat starts an ad-hoc room from there. Features marked **not implemented** still need more work. Scripts and integrations can use the same groups through the REST API ([API summary](#api-summary) at the end).

**Glossary.** A group-chat room is not a memory room. The *shared* and *private* memory rooms of the `remember` and `recall_memory` tools are where agents keep notes ([memory](memory.md)). They have nothing to do with group chats.

## The three kinds of group chat

| Kind | Who creates it | Members | Can you delete it? |
|---|---|---|---|
| **Team room** | Created automatically the first time the workspace has two conversation agents | Every conversation agent of the workspace. It follows the team on its own as agents join or leave | No |
| **Squad** | You. You give it a name (1 to 80 characters; a few characters, such as some emoji and rare CJK characters, count as two) | Two or more agents you choose | Yes |
| **Ad-hoc room** | You. No name needed: it is named after its members, for example "Mia, Jim"; a long name is shortened with "…" | Two or more agents you choose | Yes |

A conversation agent is an agent you can chat with directly. Workers, system agents and Omnipus never join a group chat. A squad or ad-hoc room has at most 256 agents: creating one and adding members both refuse a larger group, and one membership change names at most 256 agents. The team room is the workspace's whole conversation team, including teams larger than 256. Its metadata, snapshots and message fan-out are accepted without a 256-member output limit; the 256-id limit still applies to each creation or mutation request. The workspace lists its team room first, then squads, then ad-hoc rooms, each oldest first.

### At least two agents

Every group chat needs at least two different agents.

| Situation | What happens |
|---|---|
| You create a group with fewer than two different agents | Refused. The server's room-rule text is "a room needs at least two agents". With inbound schema validation switched on, a list with fewer than two entries can instead be rejected earlier with a 400 validation error. The same agent listed twice does not count as two |
| You remove a member and fewer than two would be left | Refused: "a room needs at least two agents — delete the room instead". Muted members count toward the two |
| An agent leaves the workspace, and a squad or ad-hoc room is left with one agent | The group becomes read-only: "This group has fewer than two agents — add another agent to continue." Nothing is deleted |
| The workspace drops below two conversation agents | The team room is hidden and read-only: "The team room needs at least two conversation agents in this workspace." Its history, mutes and cost are kept, and it comes back with all of them when a second agent joins |

You cannot add or remove team-room members directly. Change the workspace's team instead: "Team room members are the workspace's agents — add the agent to the workspace". Muting works in the team room as in any other group.

### One workspace only

A group chat holds agents of exactly one workspace. Adding any other agent is refused with "<Agent> is not in <Workspace>". When an agent is removed from a workspace, it leaves every group chat of that workspace, and its requests that have not started yet are cancelled ("cancelled — removed"). This happens when the workspace's team is saved, and again whenever a group is next opened.

Removing a member from a squad or ad-hoc room, or taking that agent off the workspace team, does not stop work it already started in its main chat. If it later tries to answer the group, that answer is refused because the agent is no longer a member. It is not posted under a former member's name.

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
- An @-name that matches no member stays plain text. If none of the message's @-names matched anyone and there is no `@all`, the message is saved in the group but sent to no one.
- `@all` is reserved. A member actually named "all" can only be picked from the @-picker in the room's message box.

**Picked members win.** When members are picked (in the @-picker, or in the API's `mentions` list), exactly those members receive the message, and the text is not read for names at all. A picked member that shares its name with another member is therefore addressed, not refused, and a renamed member is still reached by its pick.

Your message can be up to 65,536 bytes long by default. Your operator can change this with `session_messaging.steer_body`, the same limit that applies to messages between sessions. The same limit applies to an agent's answer in a group: a longer answer is refused, and nothing of it is posted, copied or passed on.

## How agents answer: from their own main chat

No agent works inside a group chat. Each agent's main chat is its one-to-one chat with you in that workspace, and that is where it answers.

1. Each addressed member gets your message as a request in its own main chat. The request says who wrote it and quotes the group messages that member has not seen since its previous request (at most 20 messages and 8,192 bytes). The quoted messages are marked as quotes, not instructions.
2. The agent works on it there, with its own tools, permissions and settings. A new request never joins a turn that is already running. It waits for the agent's next turn. A request that waited (the agent was stopped, or its turn could not be started) is handed to the turn that starts the agent again, as one of that turn's first inputs.
3. The agent answers with its `answer_request` tool. The server first saves the answer in the group under the agent's name, then copies it into the agent's main chat, then passes on any @-names in it. The copy is written once. A plain reply in the main chat does not post an answer to the group. If copying fails after the answer was saved, the answer stays in the group and the tool reports the error; see "Storage failures" below.
4. If a turn ends without an answer, the agent gets one reminder. If it still does not answer after that reminder turn, its outstanding request is marked "failed: no answer". A failed turn marks its still-outstanding requests "failed: turn failed"; cancellation marks them "interrupted". Answers already posted stay answered. These outcome writes also need storage. If one fails, the turn reports a completion error and group reads can fail until the recorded outcome can be repaired. A cached "working" label is not proof that the turn is still running. Repairing the outcome does not repeat the model work.

The same one-reminder rule applies when a background update started the turn. Stop in the member's main chat interrupts its active group requests, including a request that started the turn. A later message or Retry does not automatically repeat interrupted work.

**After a crash, a fresh message from you can start the member again.** A new, authenticated human group request can restart a main chat stopped by the gateway restart; you do not also have to send a message in that agent's main chat. Its usual permissions, approval and group checks still apply. An intentional Stop still holds, including a Stop accepted while the new request is arriving. Agent messages, synthetic notifications and old delivery IDs cannot claim this human restart authority.

The interrupted request's work is retired from active model input, not deleted. Its chat history, files and completed tool results remain. A later main-chat turn, helper outcome or reload does not restore its pending instruction, unfinished narration or reminder. A helper continuation derived from that old request is not automatically resumed; ask for new work with a new request or delegation. Unrelated tasks, schedules and independently admitted inputs are not cleared. If the server cannot establish the exact old-input correlation, it stops the affected continuation with a context-repair report rather than guessing which history to remove.

**Ask again is new work, not Resume.** It prefills the member's @-name and starts nothing until you send. Send a new message with a new ID. Retrying the old ID repairs an unconfirmed delivery where possible; it does not revive an interrupted request that a turn already consumed. A fresh group request also cannot accept an uncertain file operation on your behalf: `reconciliation_required` still needs explicit continuation after its report in the affected chat or an authorized parent action.

**In the main chat.** The request and the answer each appear in the agent's main chat as a collapsed entry, "From group <name>: <first line>". Expanding one shows the full text, its author and a link to the group.

### Approvals and questions

- **Approving the request itself.** If an agent's `send_message` permission is Allow, the request reaches that agent without approval. If it is Deny, the request is refused. If it is Ask, the request needs your approval unless a standing grant already covers `send_message` for that agent; then no new card is raised. When a card is raised, it belongs to that agent and is labelled "for group <name>", and the group shows the member as awaiting your approval.
- **Approvals during the work.** Approvals and questions the agent raises while working on a group request belong to its main chat, not to the group. While one is open, the group shows the member as waiting for you. While a member is awaiting your approval or waiting for you, the group's header shows an **Open <Agent>'s main chat** link that takes you to the card (shown when Omnipus can find that main chat).
- **Where they appear.** The approval card appears inside the agent's main chat, labelled "for group <name>" when a group request raised it. While it waits, that main chat needs your attention in the sidebar.
- **Not supported yet.** Secret requests from group work are not handled in this version.

An agent's message in a group is only ever text for the other members. It can never approve anything, answer a question card or run a command.

### Storage failures

| What failed | What you see | What to do |
|---|---|---|
| Appending the request to a member's main-chat transcript | The member can show "unavailable: storage". That failed append leaves no committed request capture and starts no turn | Once storage works again, retry the message with its original `client_message_id` |
| Saving the member's request record after its main-chat transcript entry was appended | The member can show "unavailable: storage" even though its main-chat entry is already saved. This failed attempt does not publish the wake that would start the request | Once storage works again, retry the original message with its original `client_message_id`. The server checks the member's existing request record and reuses the same transcript-entry ID, so recovery does not add a second main-chat entry |
| Starting the member's turn after the request was recorded | The member shows "queued". The request is not lost: it is handed to the member's next turn | Retry the original message to attempt another wake, or write to the agent to start its next turn. Retry does not override Stop. If another wake fails, the recorded request stays queued |
| Checking the group, or saving "working", just before the member's turn takes the request | The request is not run on an unchecked group. It remains queued or held if it was held already | Retry the original message after storage recovers. A still-stopped member stays held; a runnable member is woken without a second capture |
| Handing a checked request to a turn, then returning it to the queue | The request did not reach the turn. If its queue reset cannot be saved, the server logs a pending repair and group reads fail while repair is blocked. An unrelated reminder does not fail this unstarted request | Once storage recovers, open the group or retry the original message to repair the reset. After restart, recovery needs either a saved queue reset or a saved undo record. If neither was saved before the process stopped, the request is interrupted, not replayed; ask again with a new message and ID |
| Copying an answer into the main chat, or recording that copy | The answer stays in the group. The agent gets an error saying the main-chat copy is missing, and the answer's @-names are not passed on yet | The agent calls `answer_request` again with the same request and the same text. That repairs the copy without posting a second answer, and then passes the @-names on once. Loading the group again after a restart also repairs it and passes them on |
| Saving a pause, a direct room-member removal or a room deletion | That room change is refused with an error; its cancellation step has not been applied | Try again after storage recovers |
| Updating groups after a workspace team change was saved | The workspace changes stay saved, but the request returns a partial-save error. A group's stored roster or pending-request labels may not yet match the saved team. Removed members' heartbeat jobs are still reconciled from the saved team | Read the workspace and groups again before retrying. The response carries the saved revision and fields; clients must handle it as an uncertain partial save, not a refusal. Opening or listing groups retries room reconciliation. Do not assume the workspace was rolled back |
| Recording a failed or cancelled turn's outcome | The request is not discarded while its outcome is unrecorded. A stored repair record lets later reads finish the status update; no old model task is rerun | Restore storage and open the group. Across restart, an outcome needs a saved terminal state or repair record; when no outcome could be saved, interrupted work is not replayed |
| Marking requests cancelled after a pause or removal was saved | You get an error, including when removal came from a workspace team save. Membership remains changed and the requests cannot run. Reads refuse stale labels while the repair write is still failing | Once storage recovers, send `/pause` again or open the group; the labels are repaired |

If only recording a completed main-chat copy failed, the copy may already exist even though the tool says it is missing. Repeating the same request and answer text records completion without duplicating the copy. A later failure or cancellation of the owning turn preserves this live repair route: the room answer stays answered, and the agent can repair it without restarting Omnipus.

The `answer_request` tool's error starts with "reply refused, nothing was sent". For a failed main-chat copy that prefix is not accurate: the answer is already in the group. Do not treat a tool error at this stage as proof that nothing was posted.

### Member states

The group shows one state per member: its most recent request. The labels in this table are the ones the room page shows. The server sends the state names in brackets.

| Label | Meaning |
|---|---|
| sending… (`recorded`) | Saved in the group, on its way to the agent |
| queued (`admitted`) | In the agent's main chat, waiting for its next turn |
| held — <Agent> is stopped (`held`) | The request is kept for when the agent runs again. Usually its main chat is stopped. The server also reports `held` when it cannot read that chat's run state, so the label does not prove the agent was explicitly stopped |
| awaiting your approval — in <Agent>'s main chat (`awaiting_approval`) | The agent's Ask permission needs your decision first |
| working (`working`) | The agent's turn is working on it |
| waiting for you in <Agent>'s main chat (`waiting_on_user`) | The agent asked you something, or needs an approval, while working on it |
| answered | The answer is in the group |
| refused | The agent's permission refused the request |
| failed: turn failed, or failed: no answer (`failed`) | The turn failed (`turn failed`), or ended with no answer after the reminder (`no answer`) |
| interrupted | The request's execution ended incomplete, was stopped, or was cut off by a restart. Its old work stays historical rather than becoming pending work again. **Ask again** puts "@<Agent> " in the composer so you can send a new request; it does not resume the old one. A fresh authenticated human request can restart a crash-stopped main, but cannot override intentional Stop or a reconciliation hold |
| unavailable (`unavailable`) | Not delivered. The reason is shown, for example "rate limit", "not a member" or "storage" |
| cancelled, or cancelled — removed (`cancelled`) | Cancelled by `/pause` or deleting the group (`cancelled`). Removing the member shows `cancelled — removed` |
| unknown | After a restart, Omnipus could not prove what happened |

A group needs your attention while any of its requests waits for your approval or your answer, even if a newer request of the same member has already finished.

## Agents talking to each other

- An agent's answer goes only to the members it @-names.
- `@all` in an agent's answer goes to every other member that is not muted, never back to the agent itself.
- An answer with no @-names reaches no other agent.
- There is **no limit** on how many times agents can reply to each other. This is a deliberate choice. You can stop further passing-on with `/pause`. The room page's header shows each group's allocated cost.

**A rate limit controls the pace; it does not cap the total conversation.** By default, each sender-and-member pair can make at most 60 room delivery attempts a minute. Your messages share one sender identity; each sending agent has its own. The allowance is shared across that pair's groups, not reset for each group. Your operator can change the number with `session_messaging.steer_rate`. An attempt over the limit is not delivered automatically later: the member shows "unavailable: rate limit". Once the minute has passed, retrying the message delivers it.

**Retries must reuse the original `client_message_id`.** Re-post the same request with the same ID after the limiting or storage problem is resolved. A new ID creates a new message and can ask members who already got it to work again. A retry acts only on that message and on the @-names its answers passed on: members who did not get it are sent it again (or woken, if it is recorded but their turn never started), and members removed since show "cancelled — removed". Other messages of the group are not touched. Requests already taken by a turn, or answered, are never run again. Interrupted work is different: ask again with a new message and a new ID. Two overlapping retries of the same message deliver it once. On the room page, a message whose save was not confirmed shows "Not confirmed — your message may not have been recorded." with **Retry**, which re-sends it with its original ID.

### Allocated cost

Each group has an allocated cost: what its members' turns spent on it. The room page's header shows it as **allocated cost**.

- **How it is shared.** For each member turn, the group receives the turn's cost multiplied by the number of requests from this group that the turn newly consumed, divided by the number of all inputs it newly consumed. This counts inputs, not their length or token size. One group input and one direct input give the group half of that turn's cost.
- **Each turn counts once per group.** Each share is rounded to the nearest millionth of a dollar. Exact halves round up: half of a millionth becomes one millionth, and one and a half millionths become two. The header shows the total rounded to cents. The counted shares are kept on disk turn by turn, so a share survives a restart even if the group's summary could not be saved at the time.
- **Missing prices.** Prices come from each model call's provider catalog entry (see [model-call cache accounting](operations/observability.md#model-call-cache-accounting)). If any call in a turn lacks a complete known price — the call is unpriced, or part of its tokens has no price in the catalog — the whole turn adds no money and marks the total incomplete; the header labels this "plus unpriced turns". A ChatGPT sign-in call costs no API dollars and does not set the mark, and a call that reports no token usage adds nothing. The same mark is set if a turn's share could not be recorded.

## Room commands

Only you can use commands in a group. An agent's text is never a command.

| Command | What it does in this version |
|---|---|
| `/pause` | Stops the group from sending to agents (see below) |
| `/resume` | Starts sending new messages again |
| `/mute @Agent` | The agent no longer receives your default or `@all` messages. It still answers when @-named, stays a member and still counts toward the two-agent minimum |
| `/unmute @Agent` | Undoes `/mute` |
| `/members` | Accepted by the server without returning or saving a list. The planned on-screen member list is **not implemented** yet |
| `/help` | Accepted by the server without returning or saving help. The planned on-screen list of these six commands is **not implemented** yet |

`/members` and `/help` return 202 without changing the group, adding a transcript entry or publishing a reply. Until their on-screen notices exist, use the room metadata and the live member-state frames to see membership, mutes and request states.

**Why steering commands are refused.** Any other command, such as `/model`, `/stop` or `/goal`, is refused with "Rooms don't steer agents — use <Agent>'s main chat". The server names the @-named members, or every member if none is named, and includes their main-chat IDs. The room page turns those into links to each named member's main chat; the API error itself is plain text. A group chat is a conversation, not a control panel. Steering commands change how one agent works, so they belong in that agent's main chat, where they affect only that agent.

### /pause and /resume

- `/pause` cancels every request that has not started yet. That includes queued requests, held requests and requests awaiting your approval; their pending approvals are withdrawn.
- Work already running in an agent's main chat is not touched. It finishes, and its answer is posted in the group marked **after pause**. Any @-names in that answer are not passed on.
- Messages you write while the group is paused are saved in the group but not sent. `/resume` does not send them either; it only affects new messages.
- After `/resume`, an answer to work started before the pause is still marked "after pause".
- If the pause is saved but some requests could not be marked cancelled, `/pause` reports an error. Those requests will not run. Send `/pause` again to mark them.

## Add participant

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

- The deletion is saved first. If it cannot be saved, you get an error and nothing changes.
- Its requests that have not started yet are then cancelled, and their pending approvals are withdrawn.
- Work already running finishes in the agents' main chats. Its answer is not posted, and the agent is told that the group is no longer available.
- If the group's files cannot be removed right away, the group is still deleted and never shown again; the files are removed the next time the server loads its groups.

## Opening a group: "Earlier messages aren't shown"

When a group is opened or reconnects, the server sends at most the newest 10,000 messages after the last received position. A reconnect normally adds those messages to the ones already on screen, in order and without duplicates. If the server leaves out earlier messages, the returned messages replace the displayed list so it contains no hidden gap. The room then shows a quiet line, "Earlier messages aren't shown", above that list. The line does not block sending. Sending waits only for the first live snapshot (the server's current view of the group) after each opening or reconnect, unless the connection is down or the group is read-only. The full history stays saved on the server.

**Scrolling back.** A group opens at the newest message, and switching to another group lands at that group's newest message too. Saved messages may appear immediately while the group catches up; the opening position settles at the newest message once that catch-up arrives, not at the end of the saved messages. Scroll to the top of the room to load the previous page of messages, up to 100 at a time. Each page is loaded only when you reach the top — opening a group never loads its whole history. If the messages shown don't yet fill the room (so there is nothing to scroll), the "Earlier messages aren't shown" line itself loads the previous page when used. The room keeps your place while older messages are added above it, including when the loading line or earlier-history hint disappears. The "Earlier messages aren't shown" line stays until the beginning of the group is reached. While a page loads, the room shows "Loading earlier messages…". If loading fails, "Could not load earlier messages." appears with **Retry**, which asks for the same page again; the messages already shown stay. A page with messages from another group, messages that are not older than the requested position, or messages out of order is rejected through that same error and Retry path. Loading older messages never blocks sending.

The snapshot always includes every request that is still open. When there are more than 10,000 member states, older finished ones give way first; an open request is never left out.

**Group list refresh.** If refreshing the Group chats list fails after it has loaded, the last loaded groups stay available. The list says "Could not refresh group chats — showing cached, out-of-date groups" and offers Retry. The hint stays after another failed retry and disappears when Retry loads a fresh list. A first-load failure says "Could not load group chats" with Retry; a successful empty list says "No group chats yet".

## Audit

Group changes are written to the audit log as `room.changed` events. Each event records the room, the workspace, the action, the agents involved and who made the change. A signed-in browser session records that account's user name, including a workspace team change that moved agents in or out of its groups. The CLI token records `cli`. Development bypass records `_dev_bypass`. The legacy environment token records `_env_token`. The server's own updates record `system`, for example when opening or listing a group brings its membership up to date outside the workspace team-save request.

| Action | When |
|---|---|
| `created` / `deleted` | A squad or ad-hoc room was created or deleted |
| `members_add` / `members_remove` | Members were added or removed |
| `members_mute` / `members_unmute` | Someone was muted or unmuted, through the API or with `/mute` and `/unmute` |
| `team_room_created` | The workspace got its team room |
| `members_joined_workspace` / `members_left_workspace` | Group membership followed a change to the workspace's team |

## Limits in this version

| Limit | Detail |
|---|---|
| Informational commands | `/members` and `/help` are accepted no-ops. Their on-screen notices are not implemented yet |
| Secret requests | Not handled for group work |
| Deleting a workspace | Does not remove its separately stored group-chat files yet. The missing workspace makes those groups unavailable through the room API; the retained files are not a promise that the groups can still be opened |

## API summary

For developers. These routes use gateway authentication: a signed-in browser uses its session cookie, and programmatic clients use an accepted bearer token, including the CLI token. The gateway's development-bypass setting is an explicit exception to authentication and is not a production access guarantee.

| Route | What it does |
|---|---|
| `GET /api/v1/workspaces/{id}/rooms` | Lists the workspace's groups (team room first) |
| `POST /api/v1/workspaces/{id}/rooms` | Creates a squad or an ad-hoc room |
| `GET /api/v1/rooms/{id}` | Reads one group's metadata |
| `DELETE /api/v1/rooms/{id}` | Deletes a squad or an ad-hoc room |
| `PATCH /api/v1/rooms/{id}/members` | Adds, removes, mutes or unmutes members |
| `GET /api/v1/rooms/{id}/messages` | Returns one page of older messages, oldest first. `before_seq` is an exclusive entry sequence (omit it for the newest page); `limit` is 1 to 1000 (default 100). The response has `entries`, `has_older` and, only when `has_older` is true, `next_before_seq` — the `before_seq` for the page before it. Invalid query values and a `before_seq` beyond the newest entry return 400; a missing group returns 404 |
| `POST /api/v1/rooms/{id}/messages` | Accepts a message or one of the six room commands; returns an empty 202 on acceptance. A normal message has been saved, but individual members can still be held, refused or unavailable. Commands need not create transcript entries |

A message request needs `client_message_id`, `text` and `mentions`, all present and not null. Use an empty array for `mentions` to rely on typed names or the default. Ids in `mentions` select current members (the reserved value `all` selects every unmuted member); when any are given, the text is not read for names. Keep the original id and payload when retrying (see the retry rules above). Missing fields are rejected with 400, room-rule refusals generally use 422, and missing groups or workspaces use 404. If the gateway cannot read, list or save the group operation itself because of a server or storage failure, it returns a 500 error, not an invented empty list. A message already saved in the group can still return 202 when delivery to an individual member fails on storage; inspect that member's request state as well as the HTTP response.

If a team room has already been created, the workspace room list keeps returning it with `hidden: true` and a read-only reason when the team drops below two conversation agents. A workspace that has never reached two conversation agents has no team room yet. Hidden does not mean deleted. A room GET returns its metadata; use the WebSocket for its messages and current request states.

To read live traffic, attach the web app's WebSocket to the room with `type: "attach_room"`, `room_id` and a non-negative `after_seq` (`0` for the first load). The server answers with a snapshot, then sends live room-entry, member-state and metadata frames. The snapshot holds at most the newest 10,000 entries after the cursor, in order. `has_older: true` means entries after that cursor were left out; `complete` is always true and reserved, not evidence of full history. Older entries are read page by page with `GET /api/v1/rooms/{id}/messages` (above).

The exact request and response shapes are in `contracts/openapi.yaml` and `contracts/asyncapi.yaml`. Clients need the matching response validators: older validators that cap outputs at 256 members or 10,000 live request states reject valid larger responses. This server's generated response schemas have no such output caps.

## See also

- [Workspaces](workspaces.md) — the team a group chat draws its agents from
- [Memory](memory.md) — memory rooms, which are not group chats
- [Security](security.md) — permissions, approvals and the audit log
