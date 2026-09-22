# pkg/coreagent — agent roster and seeding

## Running tests here

Scope to one symbol (`CGO_ENABLED=0 go test -tags goolm,stdjson -run
'^TestSeedConfig_FreshInstallSeedsCoreGrants$' -p 1 ./pkg/coreagent/`) — the
fresh-install seeding contract for the roster and its compiled-in skill
grants. CI is the authority for full-suite results.

## Agent types — wire names vs persisted constants

Wire taxonomy (`contracts/components/schemas/Agent.yaml`): `Main` (chat
colleague), `Subagent` (delegation-only worker on the Omnipus engine),
`subagent_3p` (delegation-only worker on an external CLI: claude-code, codex,
opencode). The built-in chat roster (Mia / Jim / Ava / Admin) returns `type: core`
with `locked: true`. Planner, Researcher and General Purpose are native workers.
Judge and Plan Supervisor are hidden engine agents with persisted `system` type.
ADR-090 defines field-level editing: ordinary built-in identity and base prompts
are fixed, while tool policies, connector assignments and skills are editable.
Hidden agents have editable instructions and supported model tuning, with fixed
capabilities. Ray, Explorer and Max are not seeded.

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
