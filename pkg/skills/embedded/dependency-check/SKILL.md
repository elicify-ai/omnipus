---
name: dependency-check
description: Use inside create-agent, install-skill, team-setup and add-connector before the summary to list every software install the plan needs.
metadata:
  display_name: Dependency Check
---
# Dependency check (pre-summary, W1 / W6 / W10 / W18)

## Prerequisites
The skill or task-plan guidance of everything the plan installs (recipes live there — none are hard-coded here), plus `tool:list_skills`, `tool:list_mcp_servers`, and `tool:get_workspace` for the target.

## Steps
1. Read the declared needs of each skill, connector and agent the plan creates (for example a document converter, an MCP server's runtime).
2. Check what the target already has. Shared runtimes are application-managed — if one is missing, report it as a machine-level need; never install a shared runtime yourself.
3. For each need, take the install command from the declaring skill's or task plan's guidance, and name the target workspace it installs into.

## Expected output
The installs list for the calling workflow's summary: what installs, where, and the exact command. After the user approves and the install runs, the report line is "installed; readiness confirmed at the agent's first use" unless readiness was proven in the target agent's own sandbox — exit 0 is never readiness.

## Stop and handoff
A need with no guidance to install it is a gap for the summary, not a guess. This skill never installs; `tool:environment_setup` in the calling workflow does, behind its own approval.
