---
name: workspace-team
description: Change workspace membership while preserving unrelated members and coordinating delegation edges.
---
# Workspace Team
## Prerequisites
Read current membership and delegation through get_workspace.
## Steps
1. Calculate the exact member additions/removals without dropping unrelated members.
2. Add or remove affected delegation edges in the same proposal.
3. Confirm once, call update_workspace, and read back both team and graph.
## Expected output
Exactly the proposed membership and graph changes.
## Stop and handoff
On conflict, reread and propose again; never overwrite a concurrent edit.
