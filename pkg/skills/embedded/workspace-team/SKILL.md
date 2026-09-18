---
name: workspace-team
description: Change workspace membership while preserving unrelated members and coordinating delegation edges.
metadata:
  display_name: Workspace Team
---
# Workspace Team
## Prerequisites
Read current membership and delegation through `tool:get_workspace`.
## Steps
1. Calculate the exact member additions/removals without dropping unrelated members. Never add Admin; Admin is a standalone operator and not a teammate.
2. Add or remove affected delegation edges in the same proposal, still excluding Admin.
3. Present one combined proposal in the conversation and wait for the actual user's apply, change, or cancel reply. When delegated, send it through `tool:message_parent` and wait for the relayed user answer. Use `tool:AskUserQuestion` only for a real unknown, never as permission to apply.
4. After confirmation, call `tool:update_workspace` with the revision from `tool:get_workspace` and read back both team and graph.
## Expected output
Exactly the proposed membership and graph changes.
## Stop and handoff
On conflict, stop remaining writes, reread, preserve unrelated changes, and reconfirm any materially changed proposal; never overwrite a concurrent edit.
