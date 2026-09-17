---
name: delegation-graph
description: Design and verify workspace delegation edges, modes, self-helper rules, and depth limits.
---
# Delegation Graph
## Prerequisites
Read the workspace team and current graph with get_workspace.
## Steps
1. Build candidate edges only between valid members.
2. Check modes and depth; self-edges are limited to Jim and General Purpose.
3. Include additions and removals in one confirmed proposal.
4. Apply with update_workspace and read back the usable graph.
## Expected output
A valid graph matching the proposal.
## Stop and handoff
Reject unknown endpoints, prohibited self-edges, cycles, invalid modes, and depth overflow.
