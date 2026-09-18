---
name: agent-authoring
description: Design or update an agent through Ava's protected-field, proposal-first workflow.
---
# Agent Authoring
## Prerequisites
Discover needed tool schemas with `tool:ToolSearch` before using them. Read available models with `tool:list_models`. For updates, read the current agent with `tool:get_agent` and its permissions with `tool:get_agent_tools` using its existing `id`. For native creation, inspect `tool:create_agent` and call `tool:get_agent_tools` with `new_agent_type: Main` or `new_agent_type: Subagent` before drafting; do not invent an existing ID or create a temporary agent to inspect defaults. External CLI creation does not support this native preview.

Base the proposal's permissions on that readback plus the requested changes, constrained by global restrictions. A sparse policy patch changes only named tools; it is not an exclusive allowlist. Skills describe procedures and grant no tools or connector access. Inspect assigned skills through `tool:list_skills` management scope; identify missing prerequisites as unavailable rather than listing them as effective tools.
## Steps
1. Choose a supported type only for creation; never change an existing type.
2. Draft the complete identity and soul that will be saved, model/provider, effective tools (including retained defaults and restrictions), connectors, skills, team, and delegation effects. Never add Admin to a workspace team or delegation graph.
3. Present one combined proposal in the conversation and wait for the actual user's apply, change, or cancel reply. When delegated, send that proposal through `tool:message_parent` and wait for the relayed user answer; the parent's own approval is not the user's confirmation. Use `tool:AskUserQuestion` only for a real unknown that changes the proposal, never as permission to apply. Apply the confirmed proposal in the current run. Do not use an approval tool or approval token.
4. After confirmation, submit the revision from `tool:get_agent` for updates. `tool:create_agent` may set initial native skills, mcp_servers, tool_policy_changes, and the other fields that tool advertises in one persist; do not invent fields it does not advertise. External CLI (`subagent_3p`) rejects Omnipus skills, connectors, and policy fields. Apply later assignment edits with `tool:update_agent` and team or graph changes with `tool:update_workspace`. Empty `[]` clears skills or connector bindings. Read back saved and active state.
## Expected output
A usable agent or a precise protected-field/conflict/incomplete-capability result.
## Stop and handoff
Do not alter ordinary built-in souls or hidden-agent fixed capabilities. On a conflict, stop remaining writes, reread, preserve unrelated changes, and reconfirm any materially changed proposal.
