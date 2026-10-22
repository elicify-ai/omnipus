---
name: change-agent
description: Use when the user wants to change an existing agent — instructions, model, tools, connector assignments or skills (W3).
metadata:
  display_name: Change Agent (W3)
---
# Change an agent (W3)

## Prerequisites
`tool:get_agent`, `tool:get_agent_tools`, `tool:read_agent_metadata` for current settings; `tool:list_workspaces` to name every workspace the agent works in.

## Steps
1. Read the agent's current settings and ask what should change.
2. If the agent works in several workspaces, the summary names every workspace the change affects.
3. Present one card: before → after for each field. Tool policies and connector assignments of an existing agent change only through `tool:set_agent_permissions` — `tool:update_agent` refuses them, so they appear as their own line on the card.
4. One Go / Change / Cancel question. Never call a change tool before Go.
5. After Go: re-check the agent's revision, then `tool:update_agent` (and `tool:set_agent_permissions` for the permission lines). Read the saved configuration back.

## Expected output
One line per changed field from its tool outcome — working / saved but not working: reason / partly saved / failed: nothing saved.

## Stop and handoff
Cancel runs no change tool. A revision conflict means another writer moved first: re-read, and if the intended change still makes sense return a revised before → after card for a new Go. Never silently retry over a concurrent edit.
