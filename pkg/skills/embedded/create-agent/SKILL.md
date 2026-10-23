---
name: create-agent
description: Use when the user wants a new agent created in a workspace, or an existing agent added to that workspace unchanged — the Add agent flow (W1).
metadata:
  display_name: Create Agent (W1)
---
# Create an agent in this workspace (W1)

## Prerequisites
Read before planning: `tool:list_agents` and `tool:get_agent` for a fitting existing agent; `tool:list_skills` and `tool:find_skills` for coverage; `tool:list_mcp_servers` for connectors; `tool:list_providers` for models; `tool:get_workspace` for the target team. The target workspace comes from the request (the Add agent button names it) — confirm it when free text leaves it open, and never change your own session's workspace. For a new agent, preview its defaults before drafting: `tool:get_agent_tools` with `new_agent_type` set to `Main` (main defaults), `Subagent` or `subagent_3p` (worker defaults). Never invent an existing id or create a throwaway agent to inspect defaults.

## Steps
1. Ask what the agent should do in this workspace.
2. Match existing agents first: one fits the purpose, propose adding it to this workspace **unchanged** — that is the exception path, and it skips the rest of planning.
3. Otherwise load `capability-plan` and derive abilities → tools, skills, connectors, dependencies. For every gap pick the fix in this order: assign an installed skill; install from the catalogue (W6 step); write a new skill (W5 step); add a connector (W10 step, secret on the card); install software (`dependency-check`, then `tool:environment_setup` to the target). An ability nothing covers stays listed under "not covered" — never hidden and never silently dropped.
4. Load `dependency-check` and list every needed install in the summary.
5. Present ONE summary: name, role, model, tool permissions, skills (have / install / create), connectors, dependencies, target workspace, who may delegate to it, uncovered gaps and unavailable prerequisites — then one Go / Change / Cancel card. Never call a change tool before Go.
6. After Go, apply in this order: create/install skills → connectors → `tool:create_agent` with the explicit target workspace → assign skills and connectors → join the team → main chat exists. Use `tool:set_agent_permissions` for tool policies — a sparse `tool_policy_changes` patch changes only the named tools on top of the retained positive defaults, and is never an exclusive allowlist or a deny-all backfill; `tool:create_agent` takes no management-family grants.
7. Verify each part from its own tool outcome and re-check coverage.

## Expected output
"covered" or "partly covered" with one line per part — working / saved but not working: reason / partly saved / failed: nothing saved — plus "Open chat" for the new agent's main chat. For installs: "installed; readiness confirmed at the agent's first use" unless readiness was proven in the new agent's own sandbox.

## Stop and handoff
Cancel before apply runs no change tool; a submitted secret stays stored, unused, listed and removable. A revision or expected-revision refusal: re-read, and if the plan's meaning changed return a revised summary for a new Go. Never resume a half-applied plan by replaying it — re-read what exists and continue from there.
