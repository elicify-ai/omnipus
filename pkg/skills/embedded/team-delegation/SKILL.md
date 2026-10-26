---
name: team-delegation
description: Use when the user wants to change team membership links or who may delegate to whom (W7).
metadata:
  display_name: Team and Delegation (W7)
---
# Team and delegation (W7)

## Prerequisites
`tool:get_workspace` for current members and the delegation graph; `tool:list_agents` for candidate members.

## Steps
1. Read current membership and delegation edges; ask what should change.
2. Calculate the exact additions and removals without dropping unrelated members.
3. Present one card: the changed edges and members only.
4. One Go / Change / Cancel question. Never call a change tool before Go.
5. After Go: re-check the workspace revision, then `tool:update_workspace`, then read both team and graph back.

## Expected output
The graph read-back: working / saved but not working: reason / failed: nothing saved — an edge to a non-member is refused, never stored.

## Stop and handoff
On conflict, stop remaining writes, re-read, preserve unrelated changes, and return a materially changed proposal for a new Go. A room, squad or team room is not part of this workflow — group chats are created by the user in the web app.
