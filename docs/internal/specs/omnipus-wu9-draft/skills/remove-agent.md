---
name: remove-agent
description: Use when the user wants an agent removed from one workspace or deleted everywhere (W4).
metadata:
  display_name: Remove or Delete Agent (W4)
---
# Remove or delete an agent (W4)

## Prerequisites
`tool:list_workspaces` for every workspace the agent is in; the dependency list (connections it answers as main agent, its mailboxes, its private secrets, its rooms — per workspace) before any confirmation.

## Steps
1. Establish which the user wants: remove from this workspace, or delete everywhere.
2. Present one card: every workspace the agent is in, plus the full dependency list, and — when a dependency exists — what "delete everything" removes (its mailboxes and private secrets; its channels left without a main agent, failing loudly; People rows left in place, dropping loudly).
3. One Go / Change / Cancel question. With dependencies present, Delete needs the explicit "delete everything" choice — without it the action stays refused.
4. After Go: `tool:delete_agent` (with `delete_everything: true` only when the user chose it) or the membership removal. `tool:delete_agent` raises its own approval — that is expected after Go.
5. Report durable deletion, live effect and boot validity separately.

## Expected output
Per part: working / saved but not working: reason / partly saved / failed: nothing saved, and the consequence for each channel connection that lost its main agent.

## Stop and handoff
Never delete without the summary and the human's choice. A refusal with a dependency list returns the list in full — never a shortened version.
