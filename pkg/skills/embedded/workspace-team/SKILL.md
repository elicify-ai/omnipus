---
name: workspace-team
description: Change workspace membership while preserving unrelated members and coordinating delegation edges.
---
# Workspace Team
## Prerequisites
Read current membership and delegation through `tool:get_workspace`.
## Steps
1. Calculate the exact member additions/removals without dropping unrelated members.
2. Add or remove affected delegation edges in the same proposal.
3. Confirm once with the actual user — `tool:AskUserQuestion` directly, or `tool:message_parent` relay when delegated — then call `tool:update_workspace` with the revision from `tool:get_workspace` and read back both team and graph.
## Expected output
Exactly the proposed membership and graph changes.
## Stop and handoff
On conflict, stop remaining writes, reread, preserve unrelated changes, and reconfirm any materially changed proposal; never overwrite a concurrent edit.
