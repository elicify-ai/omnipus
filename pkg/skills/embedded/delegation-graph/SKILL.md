---
name: delegation-graph
description: Design and verify workspace delegation edges, modes, self-helper rules, and depth limits.
---
# Delegation Graph
## Prerequisites
Read the workspace team and current graph with `tool:get_workspace`.
## Steps
1. Build candidate edges only between valid members.
2. Check modes and depth; self-edges are limited to Jim and General Purpose.
3. Include additions and removals in one proposal confirmed once with the actual user — `tool:AskUserQuestion` directly, or `tool:message_parent` relay when delegated.
4. Apply with `tool:update_workspace` using the revision from `tool:get_workspace` and read back the usable graph.
## Expected output
A valid graph matching the proposal.
## Stop and handoff
Reject unknown endpoints, prohibited self-edges, cycles, invalid modes, and depth overflow. On a conflict, stop remaining writes, reread, and reconfirm a materially changed proposal.
