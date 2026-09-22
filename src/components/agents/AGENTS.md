# Agents (UI)

The screen shell `AgentListScreen.tsx` lives in `src/components/screens/`;
this folder holds the profile, form, wizard, and tool-approval pieces.

## Design system

Read `.claude/skills/omnipus-design-system/SKILL.md` before adding or changing any
control, color, spacing, or type value here — it states the CI-enforced rules and cites
the script or test for each.

## Agent types (spec: `docs/internal/specs/agent-form-requirements.md`)

- Wire taxonomy: `Main` (chat colleague) / `Subagent` (delegation-only worker
  on the Omnipus engine) / `subagent_3p` (delegation-only worker on an external
  CLI). The built-in colleagues (Mia/Jim/Ava/Admin) are `type: core, locked: true`.
  ADR-090 protects their identity and base instructions while allowing capability
  configuration. Judge and Plan Supervisor allow instruction edits with fixed
  capabilities. Use backend editable-field descriptors, including
  `tool_policy_changes` for the Tools editor; never infer capability locks from
  `locked` alone. `CreateAgentWizard` offers only the three user types.
- Worker detection is `src/lib/api/agents.ts::isWorker` (recognises `Subagent`,
  `subagent_3p`, legacy `worker`). One helper — do not inline a second
  type-string check per component.

## Creation has one path

`CreateAgentWizard` (steps in `./wizard/`), hosted by `CreateAgentModal` and
opened via `openCreateAgentModal(type, cli?)` from AgentListScreen. A second
creation form for some new agent type is a regression — extend the wizard.

## Boundaries

- Custom-agent file format: `AGENT.md` (singular) + `SOUL.md` + `HEARTBEAT.md`.
  Legacy `AGENTS.md` (plural) still loads as fallback but is not for new agents.
- Delegation is NOT an agent attribute: `PUT /api/v1/agents/{id}` rejects a
  `delegation_policy` field with 400. Configure delegation on the workspace
  Team tab (see `src/components/workspaces/CLAUDE.md`); do not add an
  agent-level delegation control here.

## Tests

CI group `components-agents-settings` (pattern includes
`src/components/agents/`). Local: `npx vitest run src/components/agents/`.
A test file matching no group pattern runs in NO CI job while CI stays green;
`scripts/check-vitest-coverage.mjs` is the tripwire.
