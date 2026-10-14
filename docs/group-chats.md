# Group chats

A group chat lets you talk to several agents of one workspace at once. You write once; each agent you address gets your message in its own main chat, works on it there, and its answer comes back into the group. Group chats need the matching server update; without it the sidebar shows **Could not load group chats** with **Retry**.

## The three kinds

| Kind | Who creates it | Members |
|---|---|---|
| **Team room** | Omnipus, automatically | Every conversation agent of the workspace. It appears once the workspace has two of them, and hides (keeping its history) when fewer than two remain. |
| **Squad** | You | A named group of two or more agents you pick. |
| **Quick group** | You | Two or more agents, no name needed — also created by **Add participant** in an agent's main chat. |

Only you create group chats and change who is in them, from the web app. Agents cannot create groups or change membership. Members are always conversation agents of the same workspace — never workers, hidden system agents, Omnipus, or people from your chat platforms.

## Finding and creating group chats

Under each workspace's agent rows, the sidebar has a **Group chats** section: the team room first, then squads, then quick groups. A dot marks a group where a member is waiting for you (an approval or a question in its main chat). An empty list says **No group chats yet**; if the list cannot be loaded you see **Could not load group chats** with **Retry** instead — that is not the same as having none.

To create one, use the **+** next to **Group chats**:

- **New squad…** — give it a name (up to 80 characters) and tick at least two agents.
- **New quick group…** — tick at least two agents.

**Create** stays off until the choice is valid. The new group opens straight away. If the server's answer is lost, the dialog says **Not confirmed — the group may have been created**; check the sidebar before creating it again.

## Add participant

At the top left of an agent's main chat, **Add participant** lists the other conversation agents of the workspace. Picking one starts a new, empty quick group with both agents and a **Back to <agent>'s main chat** link. The main chat itself stays one-to-one and unchanged. **Add participant** is not offered in Omnipus's chat.

Inside a squad or quick group, **Add participant** adds an agent who is not a member yet. In the team room it is turned off — team room members are the workspace's agents, so add the agent to the workspace instead.

## Writing in a group

- **Without @-names**, your message goes to every member who is not muted.
- **Type @** to open the picker: **@all** or a member. A picked name is tied to that agent, so it still reaches them if they are renamed before you send. If you delete the picked name from your text, that agent is no longer addressed.
- **A name you type yourself** (without the picker) is sent as text; the server matches it only to a member's exact full name. If several members share that name, the message is refused with "Several members are called <Name> — pick from the list".
- **Muted members** do not get messages addressed to everyone, but you can still address them by name.

Agents answer into the group under their own name. An agent's message reaches only the members it names (`@all` from an agent reaches everyone except itself); there is no limit on agents answering each other.

## Commands in a group

A group accepts six commands: `/pause`, `/resume`, `/mute @Agent`, `/unmute @Agent`, `/members` and `/help`. They are sent as group messages and only work when you type them — an agent writing `/pause` is just text.

- `/pause` stops the group from sending to agents and cancels requests that have not started. Messages you write while paused stay in the group. Work already running in an agent's main chat finishes; its answer appears marked **after pause**, and that mark stays after `/resume`.
- `/resume` starts sending new messages again.

Any other command — `/model`, `/clear`, `/stop`, `/goal`, `/remember`, `/add` and so on — is refused with "Rooms don't steer agents — use <Agent>'s main chat" and a link to each member's main chat (or only to the agent you named in the command).

## The group header

The header shows the members, each member's latest request, and the group's **allocated cost** — the part of each member's work that this group's requests used, rounded to the cent. **plus unpriced turns** means some of that work had no known price, so the real cost is higher.

| Shown | Meaning |
|---|---|
| sending… | Recorded in the group, not yet delivered |
| awaiting your approval — in <Agent>'s main chat | The agent asks permission first; open its main chat to decide |
| queued | Delivered; waiting for the agent's current work to finish |
| held — <Agent> is stopped | Delivered to a stopped agent; it runs when the agent runs again |
| working | The agent is working on it |
| waiting for you in <Agent>'s main chat | The agent asked you something in its main chat |
| answered | The answer is in the group |
| refused | You declined the agent's request for permission |
| failed: <reason> | The work failed (for example a provider error) |
| interrupted | The work was cut off, for example by a restart. **Ask again** puts that member in the message box so you can send a new request; nothing is re-run automatically |
| unavailable: <reason> | It could not be delivered (for example **rate limit**) |
| cancelled / cancelled — removed | Cancelled by `/pause`, or the member left the group |
| unknown | The state could not be confirmed after a restart — it is never shown as working |

Approvals, questions and secret requests always appear in the agent's own main chat, never in the group. Each member's menu (**⋯**) offers **Mute**/**Unmute** and, in squads and quick groups, **Remove from group**; a group needs at least two agents, so removing one that would leave fewer is refused. Squads and quick groups can be deleted with **Delete group**.

When a group has fewer than two agents (for example after an agent left the workspace), it becomes read-only and the message box shows why.

## Sending and connection problems

**Send** is off while the group is loading, while the connection is down (**Reconnecting…**), and while the group is read-only. If a message's delivery is uncertain, the group says **Not confirmed — your message may not have been recorded** and offers **Retry**; Retry sends the very same message again, so it is never recorded twice. If the group cannot be loaded at all, you see **Could not load this group chat** with **Retry**.

## Group traffic in an agent's main chat

A member's main chat shows each group request and the agent's answer as one compact line, **From group <name>: …**. Select it to expand the full text — the request with the recent group messages it carried, or the answer with the agent's name — and **Open group <name>** to jump to the group.

## See also

- [Workspaces](workspaces.md) — the team a group's members come from
- [Web app](using-omnipus-ui.md) — the chat window and sidebar
- [Security](security.md) — approvals in an agent's main chat
