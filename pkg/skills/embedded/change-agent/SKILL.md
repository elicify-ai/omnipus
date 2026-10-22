---
name: change-agent
description: Use when the user wants to change an existing agent — instructions, model, tools, connector assignments or skills (W3).
metadata:
  display_name: Change Agent (W3)
---
# Change an agent (W3)

## Prerequisites
`tool:get_agent` for the stored configuration, overrides and current revision; `tool:get_agent_tools` for the effective policies — a compact plain-text inventory of tool names, effective allow/ask/deny and short descriptions, including denied tools; its existing-agent header carries the current revision (a preview header has no id or revision) and no separate per-agent override or ceiling metadata is returned; `tool:read_agent_metadata`; `tool:list_workspaces` to name every workspace the agent works in. Compare the effective policies with the stored overrides; report ceiling details as unavailable rather than inferring them from the effective list.

## Steps
1. Read the agent's current settings and ask what should change.
2. If the agent works in several workspaces, the summary names every workspace the change affects.
3. Present one card: before → after for each field. Tool policies and connector assignments of an existing agent change only through `tool:set_agent_permissions`, sent as a sparse `tool_policy_changes` patch — only the named tools change, inherited entries stay absent, and the patch is never an exclusive allowlist or a deny-all backfill — while `tool:update_agent` refuses them, so they appear as their own line on the card.
4. One Go / Change / Cancel question. Never call a change tool before Go.
5. After Go: re-check the agent's revision, then `tool:update_agent` (and `tool:set_agent_permissions` for the permission lines); a proposed override the advertised schema cannot express is reported incomplete, never invented. Read the saved configuration back.

## Expected output
One line per changed field from its tool outcome — working / saved but not working: reason / partly saved / failed: nothing saved.

## Stop and handoff
Cancel runs no change tool. A revision conflict means another writer moved first: re-read, and if the intended change still makes sense return a revised before → after card for a new Go. Never silently retry over a concurrent edit.
