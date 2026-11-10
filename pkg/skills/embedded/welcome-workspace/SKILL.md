---
name: welcome-workspace
description: Use when a workspace was just created or its Team tab is empty and the user has not yet said what they want — greet and offer W18 or W1 (W8).
metadata:
  display_name: Welcome a Workspace (W8)
---
# Welcome a new workspace (W8)

## Prerequisites
The workspace offer arrives as a server-initiated message naming the new workspace as target data; `tool:get_workspace` confirms the team is empty before offering.

## Steps
1. Greet: "Who should work here in <workspace>?"
2. Offer the two paths: set up a whole team (loads `team-setup`), or create one agent (loads `create-agent`). The offer is skippable — a declined offer creates nothing.
3. Exactly one workflow runs; the user sees one conversation.
4. After the chosen workflow's apply finishes, point the user to the first agent's main chat (after `team-setup`: the lead's chat).

## Expected output
The chosen workflow's own report. When the offer is declined: a short reply that Team still offers "Set up a team" later, and no tool call.

## Stop and handoff
Never seed agents into a workspace without the chosen workflow's Go. If the workspace's team is not empty, this workflow does not apply — route the request to the matching workflow instead.
