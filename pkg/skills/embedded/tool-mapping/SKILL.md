---
name: tool-mapping
description: Inspect and change an agent's sparse tool-policy overrides without bypassing the global ceiling.
---
# Tool Mapping
## Prerequisites
Read the target with `tool:get_agent_tools` and discover catalog names with `tool:ToolSearch`.
## Steps
1. Compare effective policy, explicit override, and global ceiling.
2. Propose the intended grants, asks, denials, and removals; explain grants blocked by the ceiling.
3. Present one combined proposal in the conversation and wait for the actual user's apply, change, or cancel reply. When delegated, send it through `tool:message_parent` and wait for the relayed user answer. Use `tool:AskUserQuestion` only for a real unknown, never as permission to apply.
4. After confirmation, call `tool:update_agent` with the id and revision returned by `tool:get_agent_tools`. Send only fields that tool advertises. If the advertised schema cannot express a proposed override, report that part incomplete rather than inventing a payload. Read back effective runtime policy.
## Expected output
Verified override changes with inherited entries left absent, or a precise incomplete-schema result.
## Stop and handoff
Never manufacture a deny-all backfill. A denied management operation remains incomplete. On a conflict, stop remaining writes, reread, and reconfirm a materially changed proposal.
