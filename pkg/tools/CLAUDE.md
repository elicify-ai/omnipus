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

## God mode floors the GLOBAL layer, not the agent's own

`ToolPolicyCfg.GodMode` sets the GLOBAL side of the merge to "allow" for
every tool and then runs the normal global×agent merge unchanged (O14, as
amended by issue #761). So no GLOBAL "ask" or "deny" survives, but a
PER-AGENT one does: god mode lifts the operator's restrictions, never an
agent's own ceiling. A system agent's narrow surface (the Judge's
`"mcp_*": deny`, PlanSupervisor's, …) stays in force under god mode.

It does NOT short-circuit the merge, and `agentToolsCfgToPolicy`
(pkg/agent/instance.go) MUST populate both policy maps under god mode — a
nil agent map resolves to "" and `case a == "": return g` would hand back
the god-mode "allow" for everything, relocating the defect a layer up.

Non-destructive throughout: the policy maps are not mutated (only the
local global verdict is replaced), so clearing GodMode restores prior
decisions exactly. Second, independent site: `ExecToolDeps.GodMode` in
shell.go skips ApplyChildHardening/sandbox.Run entirely (and the egress
proxy), resolved ONCE at wiring time — that one is unchanged by #761.
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
- `delegate` launches **a worker in its own session** through the
  `pkg/steer.SessionLauncher` interface — there is no special-case delegate
  class. It is a single tool (formerly the merged spawn / run_subagent /
  check_spawn_status) and it returns as soon as launch and dispatch have
  returned; the child may already be running. The delegating agent's chat
  sees only the one line `delegate` produces; the side panel lists each
  worker with a one-line status and an Open control. The remaining
  `delegate` actions — `steer`, `respond`, `peek`, `inbox`, `inbox_ack`,
  `follow_up`, `cancel`, `status` — operate on any steered session for which
  the caller holds authority (any ancestor, or the human operator at the
  keyboard). There is **no wait-inline**: `async:false` and
  `allow_blocking_question` were deleted in ADR-091 D4; if the concurrency
  cap is reached the launch is queued and the parent's tool result tells it
  its place in line. The upward path from a worker to its parent goes
  through the injected `pkg/steer.UpwardDeliverer`, which replaces the
  previous `MessageParentWaker`. `create_task` exposes the same surface;
  one primitive, two front doors.
- ADR-090 makes `ToolSearch` infrastructure that is always registered,
  discoverable, and non-deniable. Its resolution-time availability is
  intentional and must not be replaced with seeded policy data. This does
  not grant access to any discovered target: the target tool still passes
  its own effective policy, MCP assignment, session, and execution checks.
