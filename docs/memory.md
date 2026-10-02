# Memory

Memory is what your agents keep between conversations. When a session ends, Omnipus writes a short recap of it, and anything an agent saved during it stays available to the team. This page explains what agents remember, where it lives, and which settings control it.

## What it is

Three kinds of memory, all stored as plain files on your machine:

- **Saved notes.** An agent saves a fact, decision, reference, or lesson with the `remember` tool. A note lives in one of two rooms: a shared room the whole workspace team reads, or a private room for one agent.
- **Recaps and retrospectives.** When a conversation has been idle for a while, a background call summarizes it — what it was about, what went well, what to improve. The recap is loaded back into the agent's context at the start of its next session, so it picks up where it left off.
- **The transcript.** The full conversation stays on disk. Only the recent part sits in the agent's live view; the rest can be read back on demand.

Every new session starts with the last recap and up to twenty recent saved notes already in the agent's context. The agent does not wait to be asked.

## When you would use it

- You tell an agent something worth keeping — "Remember that we invoice on the 15th" — and any teammate in the workspace can recall it in a later conversation.
- You return the next day and the agent already knows what happened yesterday, without you restating it.
- A conversation runs long, and the agent reads an earlier exchange back instead of guessing at it.
- You change how often recaps happen, or which model writes them, in Settings.

## How to save and recall a fact

1. In a chat, tell the agent what to keep: "Remember that the staging server is called elm." The agent calls `remember`, and the tool call appears in the thread.
2. The note lands in the shared room by default, where every agent in the workspace can recall it. Ask for a private note when only one agent should see it.
3. In any later conversation, ask about it. The agent searches with `recall_memory` and answers from saved notes and past retrospectives.
4. For something said earlier in the same conversation, the agent uses `recall_conversation` instead. That tool never crosses into other conversations.

The two rooms:

| Room | Who can read it | Good for |
|---|---|---|
| Shared (the default for saves) | Every agent in the workspace | Project decisions, conventions, references |
| Private | One agent only | Personal working notes, individual lessons |

## The four memory tools

Every agent has all four. The agent calls them on its own judgment; you trigger them by asking in chat.

| Tool | What it does | Bounds |
|---|---|---|
| `remember` | Saves a note as a decision, reference, or lesson | Up to 4,096 characters; shared room by default |
| `recall_memory` | Keyword search over saved notes and past retrospectives | Both rooms by default; 20 results, at most 50 |
| `recall_conversation` | Reads back earlier context from the current conversation | By keyword, turn numbers, time window, an addressed tool result, or an inclusive archive-line range |
| `run_retrospective` | Records what went well and what to improve | Always the agent's private room |

## What happens when a session ends

With Auto recap on (Settings → Memory; it is on by default on a new install):

1. A session closes after it has been idle for the timeout you set — 30 minutes by default. After a gateway restart, any session that never got a recap gets one then, spread out a few per minute.
2. A background model call summarizes the conversation: a recap of at most 150 words, up to five wins, and up to five items to improve.
3. The recap is written to the agent's private room and injected into its next session. The retrospective is filed under its date and appears in `recall_memory` results.
4. If the summary call fails, the most recent user messages are carried forward word for word, and the file is marked as a fallback — the session is not silently forgotten.
5. The summary also proposes items worth remembering long-term. Nothing from that list is saved automatically; the agent still has to call `remember` for anything it wants to keep.

Delegated sub-task sessions never get a recap — only real conversations do.

## How long conversations stay in view

The live window is the part of the conversation sent to the model. Omnipus can shorten it between your messages **and during one long turn**, after each tool result and just before the next model request. It checks the assembled request, including tool definitions, notes and media, rather than treating a result's initial size limit as proof that everything fits.

| What changes under pressure | What stays |
|---|---|
| An injected block of recalled conversation can leave first. | Its source remains in the conversation archive. |
| Old whole turns, or older complete assistant steps within the current turn, can leave the live view. A step includes the assistant's calls and all their matching results. An older assistant reply that made no tool calls can leave the same way — Omnipus always removes the oldest of the two first, and the user message that started that part of the conversation leaves with it. | The newest and incomplete call/result groups stay together. The newest assistant reply stays even when it is plain text. |
| Retained tool-result text can shrink to its beginning and end, with an addressed recall mark between them. Even newest results can eventually become mark-only. | The call, its arguments, the result slot and its correlation identity remain. Full admitted, filtered result text stays in the archive. |
| The model receives a short notice identifying what left or was shortened. | The original message that started the turn, including media, instructions and not-yet-consumed steering messages remain. The relief notice is model context, not a new chat message. |

This is literal removal and text projection, not a summary, and it makes no extra model call. The same saved projection is applied when the live window is loaded again. `recall_conversation` can retrieve addressed earlier results; shortened text is not presented as a complete result.

Even when older steps leave during one turn, without another user message, the agent receives a breadcrumb naming the full evicted prefix as a **zero-based, inclusive archive-line range**. Its bounded list shows recent literal snippets first. Tool-result entries pair `tool_call_id` with `archive_line`, so repeated call IDs still identify the exact result. The full range address stays available even when some entries cannot fit in the breadcrumb.

| Address the agent can use with `recall_conversation` | What it retrieves |
|---|---|
| `archive_range: {"from": 0, "to": 3}` | The archive's literal JSON Lines text for records 0 through 3, including both endpoints. These are archive-line numbers, not turn numbers. |
| `tool_call_id` together with `archive_line` | One exact archived tool result, including text that was shortened or removed from the live view. |

The agent uses one recall mode at a time. Range pages are **quoted archive data in the current tool result**, not revived historical instructions or tool calls. Both range and addressed-result recall support `offset` and `length` paging by Unicode characters (runes). Each page reports the total size and the next offset when more remains. Pages still pass through normal result caps and context-pressure handling; a shortened page is not proof that the whole range or result reached the model.

By default, combined tool results trigger relief above an estimated **50% of the model's context window**. The separate total-request budget also reserves space for the reply, safety margin and core instructions. These are estimates, not a promise that the provider will accept the request. See [settings](settings.md) for the tool-result share setting and [troubleshooting](troubleshooting.md#the-provider-rejects-your-model-requests) for provider rejections.

Relief deletes no admitted archive bytes. If the turn is aborted, its archive appends and window metadata are rolled back to the actual turn-start snapshot; do not assume an aborted turn's results remain available for recall.

```mermaid
flowchart LR
  You[You] -->|chat with| Agent[Agent]
  Agent -->|turns accumulate| Window[Live window]
  Window -->|fills up, even mid-turn| Trim[Remove complete old steps or shorten result text]
  Trim -->|leaves admitted source unchanged| Disk[Conversation archive on disk]
  Agent -->|recall_conversation| Disk
  Disk -->|earlier turns back| Agent
```

When the window fills, older turns or complete steps can leave the live view while their admitted source text stays available for recall. Aborted-turn rollback and session retention still apply.

## After interrupted tool work

If the gateway is killed before a tool-call group finishes, Omnipus checks for unfinished calls when the conversation is next used. It records restart cancellation rather than resuming that abandoned group. The original assistant calls, any partial tool results, and the cancellation records remain in the conversation archive, subject to normal retention. Only that canceled group and its recovery records are left out of the model's view; other messages, including steering instructions, remain. Reloading the conversation or shortening its context does not bring the canceled group back.

Cancellation applies to one assistant step, not to every use of its tool-call ID. If a later interrupted step reuses that ID, it gets its own cancellation record; checking the same canceled step again does not add another. Users, completed steps, narration and unrelated controls stay in their original order, including controls after the interrupted step. Surviving results keep their original archive-line addresses for recall.

Restart recovery is different from context pressure. An unfinished group in a running turn is not silently removed to make a request valid. Malformed cancellation records or invalid call/result structure do not authorize removal: those messages remain visible at their original archive addresses. Without a valid recorded cancellation, an incomplete group can still stop a model request with a visible error.

Cancellation is not proof that an action never happened: a tool may have acted before the gateway stopped without saving its result. Before retrying work that changes something outside the conversation — such as sending a message or editing a file — check its actual state.

## Limits and things to watch

| Limit | What it means for you |
|---|---|
| Search matches words, not meaning | A note saved as "invoice" will not surface for "billing" unless the words overlap. There is no meaning-based search. |
| No memory browser | No screen in the app lists saved notes, and no tool deletes one. To remove a note, delete its file under `~/.omnipus/`. |
| Retrospectives expire | A nightly sweep deletes retrospectives older than the retention setting — 180 days by default. |
| Transcripts expire | Session transcripts are kept 90 days by default (Settings → Data). `recall_conversation` cannot read past that. |
| Saved notes never expire | Nothing prunes them; a workspace in use for years keeps every note. |
| Writes are rate-limited | An agent can save at most 60 notes per minute, and one caller at most 600. Beyond that the save is rejected and the agent is told to wait. |
| Sharing needs a workspace | The shared room exists only for work in a workspace. Outside one, every note is saved privately and the agent is told so. |

Recap settings live in Settings → Memory: the Auto recap switch, the idle timeout in minutes, the restart catch-up pass and its per-minute cap, and the summarization model with its fallbacks.

## Related pages

- [workspaces](workspaces.md) — the team that shares the shared memory room
- [agents](agents.md) — who reads and writes memory
- [tools](tools.md) — the tool catalog the memory tools belong to
- [knowledge](knowledge.md) — notes and records you write yourself, separate from agent memory
- [settings](settings.md) — where recap and retention are configured
