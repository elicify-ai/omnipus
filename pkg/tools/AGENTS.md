# pkg/tools — builtin catalog + policy compositor

Every tool an agent can call, and the single authority that decides
allow/ask/deny. The child package `pkg/tools/browser` has its own
CLAUDE.md — this file is the parent only.

## Running tests here

The package is enormous; scope to one symbol
(`CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run
'^TestFilterToolsByPolicy_GlobalAsk_AgentAllow_EffectiveAsk$'
./pkg/tools/`). CI is the authority for full-suite results.

## Strictest-wins — compositor.go::resolveEffectivePolicyWith

deny on either side beats everything; then ask; "allow" only when both
sides are allow. A per-agent allow NEVER overrides a global deny — nor a
global ask. When only ONE side has an entry, that side alone decides
(coverage requires an entry on one side, not both); when both are empty:
logged Error + fail-closed deny; nil cfg → deny. There is no hardcoded
default anywhere (Hard Constraint #6): `DefaultPolicy`/
`GlobalDefaultPolicy` were removed project-wide, and the retired
fail-closed per-agent deny backfill (`RepairIncompleteToolPolicyCoverage`,
`ValidateAgentOwnToolPolicyCoverage` — ADR-077) must not come back
(guard: `scripts/check-no-fail-closed-backfill.sh`). Both approval paths
(loop-sent defs and the gateway WS exec gate) call
`compositor.go::EffectiveToolPolicy` precisely so the two verdicts cannot
drift.

## God mode floors everything

`ToolPolicyCfg.GodMode` short-circuits the merge: every tool resolves
"allow" (O14), non-destructively — the policy maps are not mutated, so
clearing GodMode restores prior decisions exactly. Second, independent
site: `ExecToolDeps.GodMode` in shell.go skips ApplyChildHardening/
sandbox.Run entirely (and the egress proxy), resolved ONCE at wiring time.
`compositor.go::BuildFallbackPolicyCfg` deliberately never sets GodMode.

## Wildcards: rejected at write time, still parsed at resolution

Write-time, `pkg/config`'s `ValidateSubmittedToolPolicyMap` rejects any
wildcard key for the static builtin catalog; the one carve-out is keys
prefixed `mcp_` (`config.MCPToolPolicyKeyPrefix`). MCP tool names
(`mcp_<server>_<tool>`, composed at runtime in mcp_tool.go) are not
knowable until an operator connects the server.

Resolution-time, `compositor.go::buildWildcardIndex` still honours
trailing `.*` (dot-namespaced builtins, e.g. a leftover `system.*`) and
trailing `_*` (MCP). Exact match always wins; among wildcards,
most-specific prefix first. Do not delete the `.*` resolver as "dead
MCP-only code" — a hand-edited config.json can still carry one.

## The registry is global and boot-frozen

`builtin_registry.go::RegisterBuiltin` fills one name→Tool map per
process, populated synchronously at boot by
`pkg/gateway/central_builtin_registry.go` (built twice — metadata pass,
then live deps — via one shared function so the passes cannot drift) and
read-only afterwards; duplicate registration is boot-fatal.
`builtin_registry.go::ValidateMCPName` reserves builtin names and the
`system.` prefix.

## Behaviour that surprises

- `ask_user_question` NEVER returns the answer as a tool result: Execute
  validates, persists the pending set, returns a `ParksTurn` stub; the
  answer arrives server-side (`askuser.Registry.Submit`) and starts the
  resume turn. Web-SPA channel only (`webchat`).
- `delegate` is ONE merged tool (formerly spawn / run_subagent /
  check_spawn_status) with one task-status map — the pre-merge bug was
  spawn writing a map status never read. Assuming separate spawn/status
  tools is wrong.
- The old unconditional "infra force-allow" for ToolSearch was deleted;
  it now ships as literal seeded data. Re-adding a resolution-time
  shortcut violates Hard Constraint #6.
