---
name: team-setup
description: Use when the user wants a whole team set up for a workspace — the Set up a team flow or an accepted workspace offer (W18).
metadata:
  display_name: Set Up a Team (W18)
---
# Set up a whole team for a workspace (W18)

## Prerequisites
`tool:get_workspace` for the (empty) team; `tool:list_agents` for agents worth reusing; `tool:list_skills`, `tool:find_skills`, `tool:list_mcp_servers` for shared coverage. The target workspace is target data throughout.

## Steps
1. Ask what the workspace is for: the kind of work, which channels and people, any model or cost limits.
2. Discover existing agents worth reusing and the installed skills and connectors.
3. Design the team: a lead conversation agent, 2–4 specialists, workers as needed; default delegation lead → specialists → workers, plus the links the work needs. Load `capability-plan` per role; shared connectors and skills are planned once.
4. Load `dependency-check` for the whole team's installs.
5. Present ONE team summary: a graph preview **and** a text list of agents and edges, one line per agent (new or reused, role, model, skills, connectors), and the gaps. A reused agent appears as "reused, also works in X" and is added **unchanged**. A reused agent that would need a change is a **separate proposal** — before/after plus every affected workspace — never folded into this summary.
6. One Go / Change / Cancel question (Change can target one agent). Never call a change tool before Go.
7. After Go, apply in a helper in this order: shared connectors, skills and dependencies → create the new agents → add the reused agents unchanged → set the delegation graph (`tool:update_workspace`).
8. Each tool checks itself; then verify the delegation graph by read-back.

## Expected output
Per-part outcomes — working / saved but not working: reason / partly saved / failed: nothing saved — plus "Open chat" landing in the lead agent's main chat. The lead is placed first in the team order.

## Stop and handoff
The offer is skippable: declining creates nothing. Cancel leaves any already-submitted secrets stored, unused, listed and removable. A revision or expected-revision refusal mid-apply: re-read; if the plan's meaning changed, return a revised summary for a new Go. Chat-only: a task or plan run cannot carry this workflow — report what is blocked instead of half-running it.
