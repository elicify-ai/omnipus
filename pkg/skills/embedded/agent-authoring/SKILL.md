---
name: agent-authoring
description: Design or update an agent through Ava's protected-field, proposal-first workflow.
---
# Agent Authoring
## Prerequisites
Read the current agent with get_agent and available models with list_models.
## Steps
1. Choose a supported type only for creation; never change an existing type.
2. Draft identity, soul, model, tools, connectors, skills, team, and delegation effects.
3. Present one combined proposal and obtain one user confirmation: use AskUserQuestion directly, or send the proposal through message_parent when delegated and wait for the parent to relay the user's answer. Apply the confirmed proposal in the current run; no separate Ava session is required. The parent's own approval is not user confirmation.
4. For an update, submit the revision from get_agent. Use create_agent or update_agent, then read back the saved and active result.
## Expected output
A usable agent or a precise protected-field/conflict result.
## Stop and handoff
Do not alter ordinary built-in souls or hidden-agent fixed capabilities. On a conflict, stop remaining writes, reread, preserve unrelated changes, and reconfirm any materially changed proposal.
