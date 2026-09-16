# pkg/coreagent — agent roster and seeding

## Agent types — wire names vs persisted constants

Wire taxonomy (`contracts/components/schemas/Agent.yaml`): `Main` (chat
colleague), `Subagent` (delegation-only worker on the Omnipus engine),
`subagent_3p` (delegation-only worker on an external CLI: claude-code, codex,
opencode). The build-in roster (Mia / Jim / Ava / Ray) returns `type: core`
with `locked: true`, seeded via `SeedConfig`; the legacy `system` value
survives on the wire for old configs, and `SeedConfig` seeds none.

`core.go::ResolveType` is the ONLY place wire names translate to the persisted
constants (`custom` / `worker`). Those constants have ~110 internal
references — translate at the handler; do not rename the persisted values.

## Custom-agent format

Structured `AGENT.md` (singular) + `SOUL.md` (prompt) + `HEARTBEAT.md`
(periodic). Legacy `AGENTS.md` (plural) still loads as a fallback — keep it
working, but never author a new agent in it.

## Seeding and the default-agent singleton

`SeedConfig` seeds the roster and, on fresh install, stamps
`DefaultAgentID` (Mia) plus her legacy per-entity `Default: true` for display
compatibility. Resolution consults only the singleton — see
`pkg/gateway/CLAUDE.md` for the two-ladder trap before touching either.
