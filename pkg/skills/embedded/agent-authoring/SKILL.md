---
name: agent-authoring
description: Design or update an agent through Ava's protected-field, proposal-first workflow.
---
# Agent Authoring
## Prerequisites
Read the current agent with `tool:get_agent` and available models with `tool:list_models`.
## Steps
1. Choose a supported type only for creation; never change an existing type.
2. Draft identity, soul, model, tools, connectors, skills, team, and delegation effects.
3. Present one combined proposal and confirm it once with the actual user — directly through `tool:AskUserQuestion`, or through `tool:message_parent` when delegated so the parent relays the user's answer; the parent's own approval is not the user's confirmation.
4. For an update, submit the revision from `tool:get_agent`. Use `tool:create_agent` or `tool:update_agent`, then read back the saved and active result.
## Expected output
A usable agent or a precise protected-field/conflict result.
## Stop and handoff
Do not alter ordinary built-in souls or hidden-agent fixed capabilities. On a conflict, stop remaining writes, reread, preserve unrelated changes, and reconfirm any materially changed proposal.
