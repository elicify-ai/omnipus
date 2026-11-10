# pkg/coreagent — agent roster and seeding

## Running tests here

Scope to one symbol (`CGO_ENABLED=0 go test -tags goolm,stdjson -run
'^TestSeedConfig_FreshInstallSeedsCoreGrants$' -p 1 ./pkg/coreagent/`) — the
fresh-install seeding contract for the roster and its compiled-in skill
grants. CI is the authority for full-suite results.

## Agent types — wire names vs persisted constants

Wire types are `Main` for custom chat agents, `Subagent` for native workers,
and `subagent_3p` for external-runner workers. Persisted types remain
`custom` / `worker`; translate at the boundary through
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/coreagent/core.go::ResolveType`
and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/coreagent/core.go::ToWireType`
instead of renaming stored constants.

The built-in chat roster is Mia, Jim and Omnipus (`core`, locked), as defined
by `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/coreagent/core.go::BaseAgents`.
Planner, Researcher and General Purpose are locked `worker` agents, seeded
with native executors by
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/coreagent/seed.go::SeedConfig`.
Hidden Judge and Plan Supervisor are separately seeded as locked `system`
agents by `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/coreagent/seed_system.go::seedSystemAgents`.
Omnipus is a chat-target `core` agent, not a hidden engine agent;
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/config/config_agents.go::AgentConfig.IsChatTarget`
excludes `worker` and `system` types from chat targets.

Field-level editing follows
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/agentmutation/policy.go::ValidateFields`:
ordinary built-in identity and soul fields are protected; hidden souls are
editable while hidden capabilities are fixed. The gateway exposes the exact
permissions as `editable_fields` through
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/gateway/rest_agents.go::applyAgentEditableFields`.

## Custom-agent format

Structured `AGENT.md` (singular) + `SOUL.md` (prompt) + `HEARTBEAT.md`
(periodic). Legacy `AGENTS.md` (plural) still loads as a fallback — keep it
working, but never author a new agent in it.

## Seeding — no default agent

`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/coreagent/seed.go::SeedConfig`
creates missing roster entries and re-enforces locked identities and types.
On an empty roster,
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/a2-module-text/pkg/coreagent/seed.go::seedFreshInstallDefaults`
enables automatic and bootstrap recap only; it does not choose a routing
recipient. Mia remains a seeded chat colleague, not a default agent.
