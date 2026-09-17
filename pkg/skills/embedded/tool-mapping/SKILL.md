---
name: tool-mapping
description: Inspect and change an agent's sparse tool-policy overrides without bypassing the global ceiling.
---
# Tool Mapping
## Prerequisites
Read the target with get_agent_tools and discover catalog names with ToolSearch.
## Steps
1. Compare effective policy, explicit override, and global ceiling.
2. Propose exact set and remove operations; explain grants blocked by the ceiling.
3. Confirm the combined proposal once, submit the revision returned by get_agent_tools, apply it, and read back effective runtime policy.
## Expected output
Verified override changes with inherited entries left absent.
## Stop and handoff
Never manufacture a deny-all backfill. A denied management operation remains incomplete. On a conflict, stop remaining writes, reread, and reconfirm a materially changed proposal.
