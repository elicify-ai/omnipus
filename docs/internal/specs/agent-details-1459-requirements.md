# Agent details — requirements (#1459)

Status: Requirements (founder interview 2026-10-27). Input to the feature spec.
Captured at: release/v0.1.1 `75d48f041`.
Related: #1455, #1458, #815 (fallbacks visible in the UI).

## Problem

An agent cannot fully see how another agent is set up. `list_agents` (`pkg/tools/task_query.go::AgentListTool`) returns id, name and type. `get_agent` (`pkg/sysagent/tools/agent_read.go::AgentGetTool`) returns a hand-built "sanitized" subset that includes `model` but **not** `provider`, **not** `fallback_models`, and no view of what actually runs. `update_agent` can write `fallback_models`, but nothing reads them back. Field case (2026-10-27): Jarvis's team could not verify the two reviewer agents' fallback chains; the files showed they had none (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/jarvis-blockers-20261027/REPORT.md`, blocker 3). Editing an agent's JSON file directly is not the way (founder 2026-10-27); agent files under `entities/` are already protected from file tools.

## Goals

- R1. **List:** `list_agents` stays a light list (id, name, type, short summary) for every agent.
- R2. **Detail:** selecting one agent (by id or exact name) returns its **complete current definition**: every stored field, including provider, model, fallback chain, tools, skills, connector bindings, identity and its personal instructions (soul). Secret values are never included, only references.
- R3. **What actually runs:** for each model in the chain (primary and each fallback): its provider, whether that provider is set up, whether the provider offers the model, and whether it accepts images. A broken entry gets a plain warning, e.g. "fallback 2 has no configured provider — will never be used". (D2)
- R4. The model checks reuse the real resolver (`pkg/agent/model_resolution.go::ResolveModelPairCfg`, `pkg/agent/instance.go::resolveAgentFallbackModels`) rather than a copy, so the view cannot drift from runtime behaviour.

## Permissions

- R5. **The tool permission decides.** An agent with the "read agent details" tool permission sees the full definition of every agent in its workspace, instructions included. Everyone sees the light list. (D1)

## Non-goals

- No direct JSON editing by agents (unchanged protection of `entities/`).
- No extra fields beyond the full definition and R3 (founder: "nothing more").

## Security considerations

| Risk | Handling |
|---|---|
| Personal instructions (soul) of other agents are visible to any agent with the permission | Founder decision (D1); default policies decide who has the tool. |
| Secret values | Never included; only reference names (consistent with #1455). |
| Provider/endpoint names | Shown; they are configuration, not secrets. |

## Contract notes

The REST `Agent` schema already has `provider`, `model` and `fallback_models`. The tool result should use a generated contract type shared with REST (Hard Constraint #8), extended with the R3 checks, instead of a hand-built map.

## Decisions Log

| ID | Topic | Decision | Rationale | Source | Date |
|---|---|---|---|---|---|
| D1 | Who may read other agents in full | The tool permission decides; with it, an agent sees every agent of its workspace in full, instructions included; everyone sees the light list | Consistent with the general-permission model chosen for skills | Interview | 2026-10-27 |
| D2 | Model chain checks | Each model in the chain is checked (provider set up, model offered, images) with a plain warning when it cannot work | Exactly what Jarvis's team was missing | Interview | 2026-10-27 |
| D3 | Extras | Nothing beyond R1–R4; delegation links, secret access and readback after update not added | Founder: "nothing more" | Interview | 2026-10-27 |

## Dependency Graph & Implementation Order

1. Contract: agent-detail schema (full definition + model-chain checks) shared by REST and the tool.
2. Backend: `get_agent` returns the generated type; checks via the real resolver (R4).
3. `list_agents` short summary (R1).
4. Docs (`docs/agents.md`, tools reference) and a hands-on UAT row (an agent reads a reviewer's chain and sees a warning for a broken fallback).

Independent of #1455 and #1458; can be built first.
