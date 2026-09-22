---
name: interview
metadata:
  display_name: Interview
description: Gather missing facts before personal-assistant, project-planning, agent-configuration, or system-setup work.
---
# Interview
Use the branch matching the built-in identity. Assignment does not grant tools.
## Prerequisites
Know the caller identity and retain answers already supplied.
## Steps
1. branch:jim Jim: ask outcome, done, scope, constraints, dependencies, parallel work, prior work, risks, research versus making, repository, and concurrent file writers; output a brief for Planner.
2. branch:mia Mia: ask only what is needed now and distinguish inbox, files, reminder, or a project; hand projects to Jim.
3. branch:ava Ava: cover purpose/type, editable fields, effective tools, installed connectors, skills, workspace team, delegation edges, and a real model from `tool:list_models`; output one combined proposal. Never add Admin to a team.
4. branch:admin Admin: distinguish MCP, provider, or channel; identify server/app, credential reference, test, and enable expectations; output a setup request.
5. Other callers: clarify outcome, constraints, and evidence without inventing role privileges.
## Expected output
A concise role-appropriate brief, proposal, or setup request.
## Stop and handoff
Skip answered questions. Stop on a material ambiguity.
branch:jim Ask the actual user conversationally, using `tool:AskUserQuestion` only for a real unknown; when this interview is itself delegated, use `tool:message_parent`.
branch:mia Ask the actual user conversationally, using `tool:AskUserQuestion` only for a real unknown; when delegated, use `tool:message_parent`.
branch:ava Ask the actual user conversationally, using `tool:AskUserQuestion` only for a real unknown; when delegated, use `tool:message_parent`.
branch:admin Ask the actual user conversationally, using `tool:AskUserQuestion` only for a real unknown. Do not use message_parent.
