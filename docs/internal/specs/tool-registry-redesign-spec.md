# Feature Specification: Central Tool Registry & Per-Agent Policy Filter

**Created**: 2026-04-28
**Revision**: 6 (resolves grill-review CRIT-001..CRIT-003 + MAJ-001..MAJ-009 + MIN-001..MIN-007 + OBS-001..OBS-005; reconciles default-value contradictions; adds missing BDD scenarios; specifies args_hash, session_state schema, mid-turn policy re-check, dedup recovery, boot step renumbering)
**Status**: Draft (revised)
**Input**: `docs/internal/plan/central-tool-registry-todo.md`, codebase scan, grill-spec reviews (rev 1–3), and the Q1–Q9 ambiguity decisions captured in Clarifications.

---

## Summary

Replace today's per-agent `ToolRegistry` instances and the hand-maintained `builtinCatalog` slice with **one central tool registry that has two sources** — builtins and connected MCP servers — and a per-agent flat `allow|ask|deny` policy filter applied **before each LLM call**. Skills are unchanged: they continue to feed `BuildSystemPrompt` via the existing `SkillsLoader`; they never enter the LLM `tools[]` array. The "skills registry" concept is **removed** from this redesign (the `SkillsLoader` is already a registry under another name). Auto-loading skills is split out to issue #152.

The core defect this fixes: `FilterToolsByPolicy` already exists (`pkg/tools/compositor.go:298-374`) and already implements the `global × agent` × `deny > ask > allow` resolution, but is only called from the REST API for config display. The LLM today receives every tool registered for the agent. The redesign moves the filter to the LLM-call site, makes the registry the source of truth, and preserves every existing security control (notably `GlobalPolicies` / `GlobalDefaultPolicy` and the `ScopeCore`-on-custom-agent gate).

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `Tool` interface (`pkg/tools/base.go:22-30`) | extends | Adds `RequiresAdminAsk() bool` (FR-059) and `Category() ToolCategory` (FR-067). Both have default-`false` / default-`CategoryCore` implementations on `BaseTool` so existing tool implementors don't all need touching; tools in `pkg/sysagent/tools/` override `RequiresAdminAsk` to return `true`. |
| `ToolRegistry` (`pkg/tools/registry.go:25-90`) | replaces | Per-agent today. Becomes a single shared `BuiltinRegistry` instance + a separate `MCPRegistry` (same shape, dynamic membership). |
| `FilterToolsByPolicy` (`pkg/tools/compositor.go:286-374`) | extends + relocates | **Logic preserved verbatim**, including `GlobalPolicies` / `GlobalDefaultPolicy` and the `ScopeCore`-vs-custom-agent gate. Call site moves from REST handler to LLM-call assembly path. |
| `ToolPolicyCfg` (`pkg/tools/compositor.go:286-296`) | unchanged | Already carries `Policies`, `DefaultPolicy`, `GlobalPolicies`, `GlobalDefaultPolicy`. |
| `AgentBuiltinToolsCfg` (`pkg/config/config.go:495-523`) | modifies | `ResolvePolicy` stays. Legacy `Mode`/`Visible` fields are **deleted outright** from the struct (no compat shim, no migrator — Omnipus has no production users yet). |
| `builtinCatalog` (`pkg/tools/catalog.go:45-147`) | replaces | Static slice deleted. Replaced by `BuiltinRegistry.Describe()` derived from registered tools. |
| `registerSharedTools` (`pkg/agent/loop.go:672-960`) | replaces | Today registers ~20 tools per-agent in a loop. Becomes one-time registration on the central builtins registry. |
| `WireSystemTools` (`pkg/agent/loop.go:5371-5409`) | modifies | `system.*` tools become ordinary builtins. Per-agent policy decides exposure. |
| `WireAvaAgentTools` (`pkg/agent/loop.go:5417-5513`) | deletes | Replaced by Ava's per-agent policy granting the 4 `system.*` tools. |
| `wireTier13DepsLocked` (`pkg/agent/loop.go:595-659`) | replaces | Per-agent re-wiring loop deleted. Tier13 deps held by `*atomic.Pointer[Tier13Deps]`. |
| `ReloadProviderAndConfig` (`pkg/agent/loop.go:1683-1807`) | modifies | Stops rebuilding registries. Updates the deps atomic pointer + recomputes per-agent policy maps. |
| `ContextBuilder.BuildSystemPrompt` (`pkg/agent/context.go:208-272`) | unchanged | Skill summary path stays exactly as-is. |
| `ToolCompositor.ComposeAndRegister` (`pkg/tools/compositor.go:83-182`) | deletes (dead code) | Never called. |
| `HandleTools`, `HandleBuiltinTools`, `getAgentTools` (`pkg/gateway/rest.go:3047-3206`) | collapses | Two endpoints remain; `/tools/builtin` returns 404. |
| `ToolsAndPermissions.tsx` (`src/components/agents/ToolsAndPermissions.tsx:64-200`) | modifies | Switches `fetchBuiltinTools` to `/api/v1/tools`. Presets stay; apply is a confirmed replace. |
| `MCPCaller` interface (`pkg/tools/compositor.go:25-28`) | extends | Source of MCP tool entries for the central MCP registry. |
| `coreagent.GetPrompt(id)` (`pkg/coreagent/core.go`) | reused | Used as the "is core agent" predicate. |
| Audit subsystem (`pkg/audit/`) | extends | New audit event types for stale-state deny attempts, approvals, MCP collisions, boot-time corrupt-config validation. |
| WebSocket bus (`pkg/gateway/websocket*.go`) | extends | New event type `tool_approval_required` and matching response handlers. |

### Impact Assessment

| Symbol Modified | Risk Level | Direct Dependents | Indirect Dependents |
|----------------|------------|-------------------|---------------------|
| `ToolRegistry` (per-agent → shared) | HIGH | `AgentInstance.Tools` field readers, every `Wire*` function, every `Register` call site | All turn-execution paths reading `agent.Tools.ToProviderDefs()` (`loop.go:2794, 3018, 3261`) |
| `builtinCatalog` removal | MEDIUM | `HandleBuiltinTools`, `CatalogAsMapSlice` callers | Frontend `fetchBuiltinTools` |
| `FilterToolsByPolicy` call-site move | HIGH | LLM-call assembly in `loop.go` (3 sites) | All agent turn execution |
| `AgentBuiltinToolsCfg.Mode`/`Visible` deletion | LOW | Code paths reading these fields | None — pre-1.0, no on-disk configs to migrate |
| `WireAvaAgentTools` deletion | LOW | `loop.go:1743` | Ava's effective tool set (preserved by seeded policy) |
| `system.*` exposure model change | HIGH | All custom agents (default-deny rail via prefix wildcard) | Constructor seeds `system.*: deny` on every new custom agent (no migration needed; no existing custom agents) |
| Atomic pointer deps contract | MEDIUM | Every tool whose `Execute` reads tier13/provider/sandbox deps | Test must assert per-call deref (F-07) |
| Audit event additions | LOW | `pkg/audit/` event sinks | Operators reading audit log |

### Relevant Execution Flows

| Flow Name | Relevance |
|-----------|-----------|
| Agent turn execution | The only flow where `tools[]` is sent to the LLM. The new filter call lands here, **per LLM call** (a single turn may issue several). |
| Boot — registry population | Builtins registered synchronously before any MCP server is allowed to connect (resolves F-14, F-19). |
| Config PUT → hot reload | Atomic-pointer deps swap + per-agent policy-map recompute. Registries are NOT rebuilt. Per-agent policy maps stored as `*atomic.Pointer[ResolvedPolicy]` (F-12). |
| Agent creation (system + core + custom) | Agent constructor seeds `default_policy` and `policies`. Custom agents get `system.* → deny` via prefix wildcard. |
| Skill install/remove | Updates `SkillsLoader`; does not touch the tool registry. |
| MCP server connect/disconnect | Adds/removes entries scoped by server ID in the MCP registry. Builtin-vs-MCP and MCP-vs-MCP collision rules apply. |
| Boot-time config validation | If an `agent.json` is unparseable, gateway logs HIGH audit, refuses to start that agent, and aborts boot with a non-zero exit if the failed agent is a core agent. No quarantine, no rewrite — just refuse and surface the incident. |
| Approval round-trip | LLM emits `tool_call` → filter resolves `ask` → loop suspends turn at this LLM call → WS event → user response (or timeout) → resume or synthetic deny. State is in-process only; gateway restart cancels all pending approvals. |

### Cluster Placement

This feature belongs to the **agentic core** cluster (`pkg/tools/`, `pkg/agent/`, `pkg/gateway/`, `pkg/audit/`, `pkg/config/`). It crosses into the **frontend** cluster only at one endpoint rename in `ToolsAndPermissions.tsx`.

---

## User Stories & Acceptance Criteria

### User Story 1 — Per-agent policy actually filters tools sent to the LLM (Priority: P0)

A custom agent author sets `exec` to `deny`. They expect the LLM running that agent to never receive `exec` in its tools array. Today the filter is only applied to REST responses; the LLM still sees `exec`. Closing this gap is the primary motivation.

**Why this priority**: security-relevant correctness. The advertised contract is not enforced at the call site that matters.

**Independent Test**: configure an agent with `policies: {exec: deny}`. Inspect the wire-level request body to the LLM (capture via fake provider). Assert `tools[]` contains no entry named `exec`.

**Acceptance Scenarios**:

1. **Given** an agent with `default_policy: allow` and `policies: {exec: deny}`, **When** the agent loop assembles a turn, **Then** the LLM request `tools[]` contains every other registered builtin but does not contain `exec`.
2. **Given** an agent with `default_policy: deny` and `policies: {read_file: allow, list_dir: allow}`, **When** the agent loop assembles a turn, **Then** the LLM request `tools[]` array contains exactly `read_file` and `list_dir` plus any policy-allowed MCP tools.
3. **Given** a tool is policy `ask`, **When** the agent loop assembles a turn, **Then** the tool appears in `tools[]` (the LLM is allowed to call it; the gate fires at call time).
4. **Given** an operator-global `GlobalPolicies: {exec: deny}` and an agent with `policies: {exec: allow}`, **When** the agent loop assembles a turn, **Then** `exec` does **not** appear in `tools[]` (global deny wins per existing precedence). [resolves F-01]
5. **Given** `GlobalDefaultPolicy: deny` and an agent with `default_policy: allow` and no per-tool entries, **When** the agent loop assembles a turn, **Then** the LLM request `tools[]` is empty.
6. **Given** policy is updated mid-turn (e.g., user toggles a denial during a multi-call turn), **When** the loop assembles its next LLM call inside the same turn, **Then** the new policy is applied (filter is evaluated per LLM call, not per turn). [resolves Unasked Q1]

---

### User Story 2 — One source of truth for the tool catalog (Priority: P0)

A developer adds a builtin tool by registering it once. The catalog endpoint, the per-agent policy editor, and the LLM tools array see it immediately. No hand-maintained `builtinCatalog` slice.

**Why this priority**: catalog drift caused 5 missing entries, name mismatches, and a ghost. Fixing the structure prevents recurrence.

**Independent Test**: register a new builtin. Hit `GET /api/v1/tools` — the new entry appears with `source: "builtin"`. Reload the SPA — it appears in the per-agent policy editor.

**Acceptance Scenarios**:

1. **Given** a developer registers a new tool `T` on the central builtins registry at boot, **When** `GET /api/v1/tools` is called, **Then** the response contains `T` with name, description, scope, category, and `source: "builtin"`.
2. **Given** the central builtins registry, **When** `GET /api/v1/tools` is called, **Then** the response contains `serve_workspace`, `run_in_workspace`, `build_static`, `handoff`, `return_to_default`, and `remove_skill`.
3. **Given** the search tools, **When** their entries appear in the registry, **Then** their names are `tool_search_tool_bm25` and `tool_search_tool_regex`.
4. **Given** `NewRemoveSkillTool` has dependencies (skills installer + path), **When** the registry is populated, **Then** those dependencies are confirmed available; otherwise the tool is registered with an error-returning `Execute` that emits a stable, model-readable error string. [resolves F-06, F-16]

---

### User Story 3 — Hot reload swaps deps without rebuilding registries (Priority: P1)

An operator updates the OpenRouter API key. The change takes effect on the next LLM call, with zero risk of tools "disappearing" mid-reload.

**Why this priority**: the Tier13 bug class is structurally eliminated. Today's rebuild-on-reload mechanism is a long-tail liability.

**Independent Test**: swap the provider config via `PUT /api/v1/config`. Assert registry entry counts unchanged before vs. after, and that the next agent turn uses the new provider.

**Acceptance Scenarios**:

1. **Given** the agent loop running, **When** `ReloadProviderAndConfig` is invoked with a new key, **Then** the central builtins registry's entry count is unchanged and the next LLM call uses the new provider via the atomic-pointer deps swap.
2. **Given** Tier13 deps wired at boot, **When** any number of `ReloadProviderAndConfig` calls occur, **Then** `serve_workspace` and `run_in_workspace` remain in the registry continuously.
3. **Given** an MCP server connects after boot, **When** the MCP registry is updated, **Then** the new MCP tool appears in the next turn's filtered tools array for any agent whose policy allows it; the builtins registry is untouched.
4. **Given** a tool's `Execute` has been registered, **When** the deps atomic pointer is swapped, **Then** the same already-registered tool's next `Execute` call observes the post-swap deps (per-call deref contract). [resolves F-07]
5. **Given** concurrent reloads and turn assemblies, **When** both happen at the same time, **Then** every assembled `tools[]` reflects either the pre-reload or post-reload policy snapshot (never a torn read). [resolves F-12]

---

### User Story 4 — `system.*` tools governed by per-agent policy (Priority: P0)

The 35 `system.*` tools become ordinary builtins. Custom agents default-deny `system.*` via a **prefix wildcard** in the policy map, seeded by the agent constructor at create time. Core agents (Ava, etc.) get explicit allow/ask entries seeded into their policies. Because Omnipus has no production users, there are no existing on-disk custom agents to retrofit — constructor seeding alone is sufficient. [resolves F-02, F-05]

**Why this priority**: privilege-escalation hazard. Default-deny on custom agents is the structural rail and must extend to upgraded existing agents.

**Independent Test**: create a custom agent with no explicit policy. Inspect LLM tools array — no `system.*` tool appears. Switch to Ava — the 4 known `system.*` tools appear.

**Acceptance Scenarios**:

1. **Given** a newly created custom agent with default config, **When** the agent loop assembles a turn, **Then** no tool with name prefix `system.` appears in `tools[]`.
2. **Given** the Ava core agent, **When** the agent loop assembles a turn, **Then** `tools[]` contains `system.agent.create`, `system.agent.update`, `system.agent.delete`, and `system.models.list`.
3. **Given** any agent, **When** a user explicitly sets `policies: {system.config.set: allow}`, **Then** `system.config.set` appears in the agent's `tools[]`.
4. **Given** the policy map contains a key ending in `.*` (e.g., `system.*`), **When** `FilterToolsByPolicy` resolves the policy for a tool name `system.agent.create`, **Then** the wildcard entry is applied if no exact match exists; exact matches always win over wildcards. [resolves F-05]
5. **Given** the 4 non-Ava core agents (other than the system-tool-bearing one), **When** they are constructed, **Then** their seeded policy is `default_policy: allow` plus `policies: {"system.*": "deny"}` (they get the same custom-agent rail unless explicitly allowed). [resolves Unasked Q9]

---

### User Story 5 — Ask-policy execution protocol (Priority: P0)

When the LLM calls a tool whose effective policy is `ask`, the loop pauses, emits a WebSocket approval event with a correlation token, and waits for an authenticated, authorized user response. Restart, batching, concurrency, late approval, and auth are specified explicitly. [resolves F-03, F-08, F-09]

**Why this priority**: ask-mode is the user-visible contract advertised by the UI; underspecified failure modes are guaranteed in production.

**Independent Test**: configure a tool to `ask`. Trigger an agent turn that calls it. Observe the WS event, send an approval, observe the tool executing. Repeat with deny. Repeat with timeout. Repeat across a gateway restart.

**Acceptance Scenarios**:

1. **Given** a tool with effective policy `ask`, **When** the LLM emits a `tool_call` for it, **Then** the agent loop pauses and emits a WebSocket `tool_approval_required` event with `{approval_id, tool_call_id, tool_name, args, agent_id, session_id, turn_id, expires_at}`. [resolves F-08]
2. **Given** a paused approval, **When** an authenticated user POSTs `/api/v1/tool-approvals/{approval_id}` with `{action: "approve"}`, **Then** the agent loop runs the tool with the original args and resumes the turn with the real result.
3. **Given** a paused approval, **When** the user denies, **Then** the agent loop appends a synthetic tool result `{error: "permission_denied", message: "User denied tool execution.", approval_id}` and resumes.
4. **Given** a paused approval, **When** the configured timeout (default **300s**) elapses, **Then** the loop treats it as deny with `message: "Approval timed out."`.
5. **Given** the LLM emits **N>1** `tool_call`s in a single assistant message and at least 2 of them resolve to `ask`, **When** the loop processes them, **Then** each is approved/denied individually, sequentially, in the order the LLM emitted them; only the currently-paused call has an outstanding `approval_id`. [resolves F-03]
6. **Given** two concurrent sessions for the same agent both hit `ask`, **When** their approval events fire, **Then** each event has a distinct `approval_id` keyed by `{session_id, turn_id, tool_call_id}` and approvals do not cross sessions.
7. **Given** the gateway restarts while an approval is pending, **When** the gateway comes back up, **Then** the pending approval is **lost** (in-process only); the prior turn is considered cancelled; the SPA is informed via a `session_state` reset event on reconnect. No on-disk persistence of pending approvals. [resolves F-03]
8. **Given** an approval was already timed-out and a synthetic deny injected, **When** a late `approve` arrives, **Then** the gateway responds with HTTP 410 Gone and emits a WARN log; no double-execution. [resolves F-03]
9. **Given** a non-admin user attempts to approve a `system.*` tool call, **When** the approve endpoint is hit, **Then** the gateway returns HTTP 403; the approval remains pending until an admin responds or it times out. [resolves F-09]
10. **Given** an unauthenticated request to the approve/deny endpoint, **When** the request is received, **Then** the gateway returns HTTP 401 (the endpoint is bound to existing `withAuth`).
11. **Given** a maximum of `gateway.tool_approval_max_pending` pending approvals (default 32) is reached, **When** the LLM emits another ask call, **Then** the loop synthesises an immediate deny with `message: "approval queue saturated"` and the WS event is not emitted. [resolves Unasked Q4]
12. **Given** an in-flight approval, **When** the user issues a `cancel` action via the same endpoint, **Then** the loop synthesises a deny with `message: "user cancelled"`. [resolves Unasked Q5]

---

### User Story 6 — Two REST endpoints, no third (Priority: P2)

Two endpoints: `/api/v1/tools` (registry snapshot) and `/api/v1/agents/{id}/tools` (policy-filtered view). `/tools/builtin` returns 404.

**Why this priority**: cleanup. Reduces SPA confusion and maintenance surface.

**Independent Test**: SPA agent profile load. Network tab shows two requests; no `/tools/builtin`.

**Acceptance Scenarios**:

1. **Given** the central registries, **When** `GET /api/v1/tools` is called, **Then** the response is a list of all builtin and all currently-connected MCP tools, each with `{name, description, scope, category, source}`.
2. **Given** an agent, **When** `GET /api/v1/agents/{id}/tools` is called, **Then** the response includes the effective policy per tool and the filtered tool set.
3. **Given** the gateway, **When** `GET /api/v1/tools/builtin` is called, **Then** the response is HTTP 404.
4. **Given** the SPA, **When** the user clicks a policy preset, **Then** a confirmation dialog warns that the existing per-tool entries will be replaced; on confirm, the preset replaces the policy map. [resolves F-17]

---

### User Story 7 — Boot-time corrupt-config handling (Priority: P1)

If an `agent.json` exists on disk but is unparseable, the gateway must surface the failure rather than silently boot with a default policy. There is no migration path — Omnipus has no production users — so the only "config evolution" the gateway handles at boot is "this file is broken."

**Why this priority**: silent acceptance of a broken core-agent config would degrade Ava (or another core agent) to its constructor defaults, hiding an availability incident.

**Independent Test**: place a corrupt `agent.json` for a custom agent and another for a core agent. Boot. Verify the custom-agent file is logged + the gateway continues; the core-agent file aborts boot with a non-zero exit.

**Acceptance Scenarios**:

1. **Given** a custom agent's `agent.json` is unparseable, **When** the gateway boots, **Then** an audit event `agent.config.corrupt` with severity HIGH is emitted, the agent is **not** activated, and boot continues for the remaining agents.
2. **Given** a core agent's (`coreagent.GetPrompt(id) != ""`) `agent.json` is unparseable, **When** the gateway boots, **Then** an audit event `agent.config.corrupt` with severity HIGH is emitted, **and** the gateway exits with a non-zero code.
3. **Given** a config file held open / read-only / inaccessible due to OS-level permissions or locks, **When** the gateway attempts to read it, **Then** the same severity rules apply (custom = log + skip; core = abort).

---

## Behavioral Contract

**Primary flows**:
- When an agent turn assembles **each** LLM call, the system filters `(builtins ∪ MCP)` through `FilterToolsByPolicy(allTools, agentType, policyCfg)` and sends only the result. The filter applies `global × agent` × `deny > ask > allow`, and the existing `ScopeCore`-on-custom-agent gate.
- When `GET /api/v1/tools` is called, the system returns the snapshot of (builtins ∪ MCP) registries with per-entry `source` discriminator.
- When `GET /api/v1/agents/{id}/tools` is called, the system returns the per-agent effective policy map plus the filtered tool set.
- When a developer registers a new builtin, the system makes it visible to all listing endpoints and to every agent whose policy allows it, with no extra wiring.
- When `BuildSystemPrompt` runs, the system continues to inject the existing skill summary unchanged.
- When MCP servers connect, the system adds entries to the MCP registry **after** builtin registration is complete.

**Error flows**:
- When the LLM emits a `tool_call` for a `deny`-effective tool (only reachable via stale model state), the system synthesises `permission_denied` and continues.
- When the LLM emits a `tool_call` for an `ask`-effective tool, the system pauses and emits the WS approval event; resumes on user response, timeout (deny), saturation (deny), or cancel.
- When boot encounters a corrupt agent config: HIGH audit; agent not activated for custom; non-zero gateway exit for core. No quarantine, no rewrite.
- When an MCP server's name collides with a builtin: builtin wins; MCP entry rejected; warning + audit event.
- When two MCP servers register the same tool name: **first-server-wins** with a warning + audit event; rejected entry is not exposed via any endpoint or sent to any LLM. [resolves F-11]

**Boundary conditions**:
- Tool-call time and wire-time both apply the filter; both observe the same atomic snapshot.
- `ReloadProviderAndConfig` swaps the deps atomic pointer and replaces the per-agent `*atomic.Pointer[ResolvedPolicy]`; registries themselves are stable for process lifetime.
- Pending approvals are in-process only; restart cancels them all.
- `system.*` is honored as a prefix wildcard in the policy map: keys ending in `.*` match any tool whose name has the prefix, exact matches winning.

---

## Approval State Table [resolves H-10, G-19]

This is binding. The approval state machine has exactly **8** states (1 active, 7 terminal). No state or transition may be added without spec amendment. Late actions on terminal states do not transition (the state remains the same); they only generate an HTTP 410 Gone response — see "Late-action response" below the table.

| State | Type | Reachable from | HTTP/WS effect on observer |
|-------|------|----------------|----------------------------|
| `pending` | active | Initial state on `tool.policy.ask.requested` emission | WS `tool_approval_required` event |
| `approved` | terminal | `pending → approve` (admin if `RequiresAdminAsk`) | Tool runs; audit `granted`; HTTP 200 |
| `denied_user` | terminal | `pending → deny` | Synthetic `permission_denied` to LLM; audit `denied reason=user`; HTTP 200 |
| `denied_timeout` | terminal | `pending` after `tool_approval_timeout` (default 300s) | Synthetic deny `reason=timeout`; audit `denied reason=timeout`; no HTTP response (timer-driven) |
| `denied_cancel` | terminal | `pending → cancel` | Synthetic deny `reason=cancel`; audit `denied reason=cancel`; HTTP 200 |
| `denied_restart` | terminal | `pending → gateway shutdown` (graceful or ungraceful, recovery path FR-069) | Synthetic deny `reason=restart`; audit `denied reason=restart`; SPA `session_state` reset on reconnect |
| `denied_saturated` | terminal | New ask request when `tool_approval_max_pending` reached (skips `pending`) | Synthetic deny `reason=saturated` to LLM immediately; audit `denied reason=saturated`; no WS event |
| `denied_batch_short_circuit` | terminal | Sibling call cancelled by user `deny`/`cancel` of a prior call in the same batch (FR-065) | Synthetic deny `reason=batch_short_circuit` for K+1..N; combined audit event with `cancelled_tool_call_ids`; no individual WS events |

**Late-action response (not a state).** Any incoming action (`approve`, `deny`, `cancel`) targeting an approval already in a terminal state returns HTTP 410 Gone. The approval's state is unchanged. (Resolves MIN-002.)

**Action precedence on a paused approval** (incoming action × current state):

| Incoming \ State | `pending` | any terminal |
|-----------------|-----------|---------------|
| `approve` (auth ok, admin if required) | → `approved` | HTTP 410 |
| `deny` | → `denied_user` | HTTP 410 |
| `cancel` | → `denied_cancel` | HTTP 410 |
| timeout fires | → `denied_timeout` | (irrelevant; timer detached on terminal) |
| sibling-batch cancel/deny | → `denied_batch_short_circuit` | (n/a) |
| gateway shutdown | → `denied_restart` (transcript on shutdown OR session-load recovery) | (n/a) |
| saturation fires (initial) | (skipped pending; → `denied_saturated`) | (n/a) |

The state is held in the in-process approval registry (`pkg/gateway/approvals.go` — new file, A3 lane). Test `TestApprovalStateMachine_AllTransitions` exercises every (state × action) pair systematically via a generated table.

---

## Retiring the "System Agent" Fiction [resolves G-01]

The pre-redesign codebase carries a documentation-vs-runtime mismatch: the BRD and earlier CLAUDE.md describe `omnipus-system` as a "distinct always-on agent with 35 exclusive `system.*` tools," but no such runtime agent exists. The 35 tools are wired onto whichever agent is `DefaultAgentID = "main"` via `WireSystemTools`, with 4 of them additionally registered onto Ava via `WireAvaAgentTools` using a scope override. The `Scope: ScopeSystem` enforcement in `passesScopeGate` (`pkg/tools/compositor.go:184`) only admits these tools for `agentType == "system"`, which is the very type that does not exist as a live agent — and the override on Ava silently bypasses the gate. The structural rail is already perforated.

**Decision (option (a) per Phase 1.7 follow-up):** drop `ScopeSystem` enforcement entirely. `system.*` is a **naming convention** indicating privileged tools but is **not** a runtime enforcement boundary. Per-agent policy is the sole gate, backed by:

1. **Constructor-seeded `system.*: deny` wildcard** on every newly created custom agent (FR-022).
2. **CI grep guard** asserting no code references the deleted `WireSystemTools`, `WireAvaAgentTools`, `ScopeSystem` constant, or the `omnipus-system` string (SC-007 extended).
3. **Audit on stale-state denial attempts** (`tool.policy.deny.attempted`) so any privilege-relevant call the LLM tries to make is logged regardless of how it slipped past policy.

This eliminates G-01: there is no `ScopeSystem` to mismatch against; Ava's seeded `system.*: allow` entries are honoured by the policy filter unambiguously.

**What changes in code (binding):**

- `pkg/sysagent/tools/*.go`: every tool's `Scope()` method changes from `return ScopeSystem` to `return ScopeCore`. The 35 affected files are touched in one commit.
- `pkg/tools/scope.go` (or wherever `ScopeSystem` is defined): the `ScopeSystem` constant is **deleted**. Any leftover reference fails compilation, surfacing the cleanup.
- `pkg/agent/loop.go`: `WireSystemTools` and `WireAvaAgentTools` are **deleted**. System tools register on the central builtins registry like any other tool.
- `pkg/coreagent/*.go` (or the equivalent core-agent factory): each core agent's constructor seeds its policy explicitly. Ava: `default_policy: allow, policies: {"system.*": "deny", "system.agent.create": "allow", "system.agent.update": "allow", "system.agent.delete": "allow", "system.models.list": "allow"}`. Other core agents: `default_policy: allow, policies: {"system.*": "deny"}`.
- `pkg/tools/compositor.go:184` (`passesScopeGate`): the `ScopeSystem` branch is removed. The remaining scope logic (`ScopeCore` vs custom agents) is preserved verbatim.
- `CLAUDE.md` and `docs/internal/architecture/AS-IS-architecture.md`: updated to remove the "system agent" framing (CLAUDE.md update is part of revision 4; AS-IS update is part of Phase E).
- `docs/internal/BRD/Omnipus_BRD_AppendixD_System_Agent.md`: superseded notice added; the appendix stays for historical record but is no longer authoritative.

**Why this is safe even though the structural rail is gone:** the rail was already not a rail (`WireAvaAgentTools` was the proof). The seeded `system.*: deny` on every custom agent plus the CI grep guard plus the ask-mode admin gate (FR-015, now scope-bound rather than name-prefix-bound — see G-06 fix below) provide a defense that is **explicit in policy** rather than **buried in a scope constant**. Reviewers can audit policy by reading the agent's config; reviewers cannot audit a runtime-only `agentType == "system"` check.

---

## Boot Order [resolves F-14, F-19]

This is binding — implementation must follow. Failure at any step aborts boot.

1. `NewStore → Unlock → LoadConfigWithStore → InjectFromConfig → ResolveBundle → RegisterSensitiveValues` (existing credential boot contract; unchanged).
2. **`BuiltinRegistry` populated synchronously** with every builtin tool (including the 6 previously-affected names + the 35 `system.*`). Atomic deps pointers initialised. No tool's `Execute` runs in this phase.
3. **Constructor-seed disposition map computed.** A static, in-memory `map[agentID]bool` is built from each registered agent factory's compile-time seed: `true` if the seed contains explicit `system.*` allow entries, `false` otherwise. Today only Ava maps to `true`. (FR-062.)
4. **Boot-time config validation** runs against `~/.omnipus/agents/*/agent.json`. Parseable + valid-policy-value configs proceed. Unparseable or invalid-value configs trigger HIGH audit (`agent.config.corrupt` or `agent.config.invalid_policy_value`); the disposition consults the step-3 disposition map: agents whose seed contains `system.*` allows abort boot non-zero; all others skip + continue. If the audit emit itself fails, a structured stderr line is printed before exit (FR-063).
5. **Per-agent policy maps computed** from the on-disk configs (or constructor seeds where no file exists) and stored in per-agent `*atomic.Pointer[ResolvedPolicy]`. Custom agents seeded by their constructor with `default_policy: allow, policies: {"system.*": "deny"}`. Core agents seeded with the same rail plus their explicit `system.*` allowances (e.g., Ava's 4). Wildcard ordering is precomputed and cached in the snapshot (FR-071).
6. **System tools registered** on the central builtins registry (one-shot; no per-agent loop). Note: legacy `WireSystemTools`/`WireAvaAgentTools` are deleted (FR-045); registration happens via the unified registry init path.
7. **MCP registry empty; MCP server connections are now permitted** to begin (in background; first-connect-wins for MCP-vs-MCP collisions).
8. Channel manager starts; HTTP listeners come up; first turn allowed.

(Boot Order steps renumbered in revision 6 — resolves MAJ-007. Previous draft had "step 3a before step 3"; canonical numbering is now linear.)

---

## Audit & Observability [resolves F-10, F-24]

The following audit events MUST be emitted via `pkg/audit/`:

| Event | Severity | Sampling | Fields | When |
|-------|----------|----------|--------|------|
| `tool.policy.deny.attempted` | WARN | 1:1 (unsampled) | `agent_id, tool_name, source ("global"/"agent"), session_id, turn_id, tool_call_id` | LLM emitted a `tool_call` for a tool whose effective policy is `deny`. Reachable only via stale model state (e.g., mid-turn policy change). Rare; security-relevant. |
| `tool.policy.ask.requested` | INFO | 1:1 | `approval_id, tool_call_id, tool_name, agent_id, session_id, turn_id, args_hash` | Approval event emitted. |
| `tool.policy.ask.granted` | INFO | 1:1 | `approval_id, approver_user_id, latency_ms` | User approved. |
| `tool.policy.ask.denied` | INFO | 1:1 | `approval_id, approver_user_id, reason ("user"/"timeout"/"cancel"/"restart")` | User denied or system-deny path. |
| `tool.collision.mcp_rejected` | WARN | 1:1 | `mcp_server_id, tool_name, conflict_with ("builtin"/<other_mcp_server_id>)` | Collision rejection. |
| `agent.config.corrupt` | HIGH | 1:1 | `agent_id, agent_type ("core"/"custom"), path, error` | Boot-time validation rejected an unparseable config. |

**Note: routine filter denies are NOT audited.** Filtering a tool out of `tools[]` at request-assembly time is the steady-state of how the filter works — the agent never sees the tool, never tries to call it. Counts are tracked by the metric `omnipus_tool_filter_total` (with `effective_policy="deny"` label) for ops dashboards. Only the rare case where an LLM **attempts** to call a denied tool (stale state) is audited as `tool.policy.deny.attempted`.

Metrics (Prometheus-style, optional but specified now):

| Metric | Type | Labels |
|--------|------|--------|
| `omnipus_tool_filter_total` | counter | `agent_type`, `effective_policy` |
| `omnipus_tool_approval_pending` | gauge | (none) |
| `omnipus_tool_approval_latency_seconds` | histogram | `outcome` |
| `omnipus_tool_mcp_collision_total` | counter | `conflict_with` |

---

## Edge Cases

- **Tool name collision (builtin vs. MCP)**: builtin wins, audit event, MCP entry rejected.
- **MCP-vs-MCP collision**: first-server-wins, audit event, second entry rejected. [F-11]
- **Policy refers to a non-existent tool**: silently ignored.
- **Same MCP server reconnects**: registry replaces existing entries for that server (server ID-scoped).
- **Per-agent policy contains the legacy `Mode`/`Visible` keys**: not possible — fields are deleted from the struct; Go unmarshal ignores unknown keys, so any leftover keys in a hand-edited file are silently dropped on next write.
- **`ask`-policy tool, no WS client connected**: timeout path fires; LLM sees `permission_denied`. SPA on reconnect sees `session_state` reset.
- **Concurrent registry reads during MCP connect**: registries protected by `sync.RWMutex`; reads see consistent snapshot.
- **Default agent renamed/absent**: irrelevant under the new design; no symbol relies on `DefaultAgentID`.
- **Custom agent created with no policy at all**: agent constructor seeds `default_policy: allow` plus `policies: {"system.*": "deny"}`.
- **Tier13-deps-dependent tool fails to wire**: tool is registered with a stable error-returning `Execute`; LLM gets a model-readable error rather than the tool silently disappearing. [F-16]
- **Mid-turn policy update**: per-LLM-call evaluation observes the new snapshot. [Unasked Q1]
- **MCP server reconnect mid-turn**: turn finishes against the prior snapshot (atomic policy pointer); next turn sees the updated set.

---

## Explicit Non-Behaviors

- The system must **not** auto-promote a skill's `allowed-tools` declaration into the agent's tool array. Tracked in #152.
- The system must **not** introduce profiles, roles, or hierarchies in the policy model. Flat map + single `default_policy` only.
- The system must **not** add a meta-discovery tool.
- The system must **not** keep legacy `Mode`/`Visible` fields in the `AgentBuiltinToolsCfg` struct. They are deleted outright.
- The system must **not** rebuild registries on `ReloadProviderAndConfig`.
- The system must **not** allow custom agents to receive `system.*` tools without an explicit policy entry granting them.
- The system must **not** call `ToolCompositor.ComposeAndRegister` from any live path. Code is deleted.
- The system must **not** persist pending approvals to disk.
- The system must **not** allow non-admin users to approve `system.*` tool calls.
- The system must **not** allow `tools[]` to contain duplicate names from the LLM's perspective.
- The system must **not** introduce a third "skills registry" — the existing `SkillsLoader` is the registry. [F-15]
- The system must **not** capture `*deps` in a closure at tool construction; it MUST deref the atomic pointer per call. [F-07]

---

## Integration Boundaries

### LLM Provider

- **Data in**: `tools[]` filtered through `FilterToolsByPolicy` per LLM call.
- **Data out**: `tool_calls[]`.
- **Contract**: existing provider abstraction, unchanged wire format.
- **On failure**: existing retry / error paths.
- **Development**: real provider in production + E2E; `FakeProvider` in unit/integration tests captures `tools[]` for assertion.

### MCP Servers

- **Data in**: tool name, schema, metadata via `MCPCaller.GetAllTools()`. Server identity included.
- **Data out**: tool invocation results via existing MCP bridge.
- **Contract**: `MCPCaller`, with a documented "first-server-wins" collision rule for duplicate tool names across servers.
- **On failure**: server disconnect → entries removed from MCP registry; agents see updated set on next LLM call.
- **Development**: real MCP servers in integration; in-memory `mockMCPCaller` in unit tests.

### Frontend (SPA)

- **Data in**: `GET /api/v1/tools` and `GET /api/v1/agents/{id}/tools`. Plus WS `tool_approval_required` and `session_state` events.
- **Data out**: `PUT /api/v1/agents/{id}` with policy fields. `POST /api/v1/tool-approvals/{approval_id}` with action.
- **Contract**: documented JSON schemas; `Mode`/`Visible` not present in the struct.
- **On failure**: SPA shows stale snapshot until next refresh; pending approvals on the SPA after a gateway restart are cleared by the `session_state` reset.
- **Development**: real binary with embedded SPA per CLAUDE.md SPA Embed Pipeline.

### On-Disk Agent Config

- **Data in**: `~/.omnipus/agents/<id>/agent.json` (read-only at boot for validation).
- **Data out**: nothing — the gateway never rewrites these files.
- **Contract**: `AgentBuiltinToolsCfg` shape only (`default_policy`, `policies` map). Legacy `Mode`/`Visible` fields do not exist in the struct.
- **On failure**: corrupt config → HIGH audit + agent skipped (custom) or non-zero exit (core).
- **Development**: fixture set under `pkg/config/testdata/agents/` covering valid + corrupt files for both core and custom agent types.

### Audit Subsystem

- **Data in**: events listed in Audit & Observability above.
- **Data out**: persisted in existing audit log.
- **Contract**: existing audit event schema; new event types declared in `pkg/audit/events.go`.
- **On failure**: best-effort; audit failure does not block tool execution but is logged at WARN.
- **Development**: in-memory audit sink in unit tests; on-disk file in integration.

---

## BDD Scenarios

### Feature: Central Tool Registry

#### Scenario: Filter excludes deny-policy tools from LLM tools array

**Traces to**: US-1, AS-1
**Category**: Happy Path

- **Given** the central builtins registry contains `read_file`, `write_file`, and `exec`
- **And** an agent `agent-X` is configured with `default_policy: allow` and `policies: {exec: deny}`
- **When** the agent loop assembles the next LLM call for `agent-X`
- **Then** the LLM request `tools[]` contains `read_file` and `write_file`
- **And** the LLM request `tools[]` does not contain `exec`

#### Scenario: Default-deny custom agent sees only explicitly allowed tools

**Traces to**: US-1, AS-2
**Category**: Happy Path

- **Given** an agent `agent-Y` configured with `default_policy: deny` and `policies: {read_file: allow, list_dir: allow}`
- **When** the agent loop assembles the next LLM call for `agent-Y`
- **Then** the LLM request `tools[]` contains exactly `read_file` and `list_dir` plus any policy-allowed MCP tools

#### Scenario: Ask-policy tool is sent to the LLM

**Traces to**: US-1, AS-3
**Category**: Happy Path

- **Given** a tool `web_fetch` with effective policy `ask` for `agent-Z`
- **When** the agent loop assembles the next LLM call for `agent-Z`
- **Then** the LLM request `tools[]` contains `web_fetch`

#### Scenario: Operator-global deny overrides agent allow

**Traces to**: US-1, AS-4
**Category**: Happy Path

- **Given** `GlobalPolicies: {exec: deny}` and an agent with `policies: {exec: allow}`
- **When** the agent loop assembles the next LLM call
- **Then** the LLM request `tools[]` does not contain `exec`
- **And** an audit event `tool.policy.deny` with `source: "global"` is emitted (sampled)

#### Scenario: Global default-deny strips an unrestricted agent

**Traces to**: US-1, AS-5
**Category**: Happy Path

- **Given** `GlobalDefaultPolicy: deny` and an agent with `default_policy: allow` and `policies: {}`
- **When** the agent loop assembles the next LLM call
- **Then** the LLM request `tools[]` is empty

#### Scenario: Mid-turn policy change applies on next LLM call

**Traces to**: US-1, AS-6
**Category**: Happy Path

- **Given** an in-progress turn that has just made one LLM call with `web_fetch` allowed
- **When** the operator updates the agent's policy to `policies: {web_fetch: deny}` mid-turn
- **And** the loop assembles its next LLM call within the same turn
- **Then** the new LLM call's `tools[]` does not contain `web_fetch`

#### Scenario: New builtin tool appears in registry without catalog edit

**Traces to**: US-2, AS-1
**Category**: Happy Path

- **Given** a developer registers tool `T` with name `"new_tool"` on the central builtins registry at boot
- **When** `GET /api/v1/tools` is called
- **Then** the response contains an entry with `name: "new_tool"`, `source: "builtin"`, and the description, scope, and category derived from `T`

#### Scenario Outline: Previously missing builtins now in registry

**Traces to**: US-2, AS-2
**Category**: Happy Path

- **Given** the central builtins registry is populated at boot
- **When** `GET /api/v1/tools` is called
- **Then** the response contains an entry with `name: <tool_name>`

**Examples**:

| tool_name           |
|---------------------|
| serve_workspace     |
| run_in_workspace    |
| build_static        |
| handoff             |
| return_to_default   |
| remove_skill        |

#### Scenario: Search tool names match runtime

**Traces to**: US-2, AS-3
**Category**: Happy Path

- **Given** the central builtins registry is populated at boot
- **When** `GET /api/v1/tools` is called
- **Then** the response contains entries with names `tool_search_tool_bm25` and `tool_search_tool_regex`
- **And** the response does not contain `bm25_search` or `regex_search`

#### Scenario: Tool with unavailable construction-time deps is registered with error-Execute

**Traces to**: US-2, AS-4
**Category**: Edge Case

- **Given** `serve_workspace`'s Tier13 deps cannot be wired (workspace dir absent)
- **When** the boot phase populates the central builtins registry
- **Then** `serve_workspace` is registered with an `Execute` that returns the stable error `"tool unavailable: tier13 deps not initialised"`
- **And** `GET /api/v1/tools` lists `serve_workspace` as present

#### Scenario: Hot reload preserves registry contents

**Traces to**: US-3, AS-1
**Category**: Happy Path

- **Given** the agent loop is running with a populated central builtins registry of N entries
- **When** `ReloadProviderAndConfig` is invoked with a new provider config
- **Then** the central builtins registry still has N entries
- **And** the next LLM call uses the new provider's auth/endpoint

#### Scenario: Tier13 tools survive arbitrary hot reloads

**Traces to**: US-3, AS-2
**Category**: Happy Path

- **Given** Tier13 deps are wired at boot
- **When** `ReloadProviderAndConfig` is invoked 100 times
- **Then** the central builtins registry continuously contains both `serve_workspace` and `run_in_workspace`

#### Scenario: MCP tool appears after server connect with no rebuild

**Traces to**: US-3, AS-3
**Category**: Alternate Path

- **Given** the agent loop is running with the builtins registry populated
- **And** the MCP registry is initially empty
- **When** an MCP server connects and registers tool `mcp_search`
- **And** an agent `agent-A` is configured with `default_policy: allow`
- **Then** the next LLM call for `agent-A` contains `mcp_search` in `tools[]`
- **And** the builtins registry's entry count is unchanged

#### Scenario: Tool Execute observes post-swap deps via atomic deref

**Traces to**: US-3, AS-4
**Category**: Happy Path

- **Given** a tool `T` registered before a deps swap, with an `Execute` that reads `deps := al.deps.Load()`
- **When** the deps atomic pointer is swapped
- **And** `T.Execute` is called
- **Then** `T.Execute` observes the post-swap deps value

#### Scenario: Concurrent reload + turn assembly never returns a torn read

**Traces to**: US-3, AS-5
**Category**: Edge Case

- **Given** a goroutine repeatedly calling `ReloadProviderAndConfig`
- **And** another goroutine repeatedly assembling LLM calls for the same agent
- **When** both run for 10 seconds
- **Then** every assembled `tools[]` reflects either the pre-reload or post-reload policy snapshot
- **And** no goroutine reads a half-updated policy map

#### Scenario: Custom agent default-denies system.* via wildcard

**Traces to**: US-4, AS-1
**Category**: Happy Path

- **Given** a newly created custom agent `agent-Custom` with constructor-seeded `policies: {"system.*": "deny"}`
- **When** the agent loop assembles the next LLM call
- **Then** no entry in `tools[]` has a name starting with `system.`

#### Scenario: Ava receives its seeded system.* allowance

**Traces to**: US-4, AS-2
**Category**: Happy Path

- **Given** the Ava core agent with seeded policy `policies: {"system.agent.create": "allow", "system.agent.update": "allow", "system.agent.delete": "allow", "system.models.list": "allow", "system.*": "deny"}`
- **When** the agent loop assembles the next LLM call for Ava
- **Then** `tools[]` contains the four explicitly-allowed `system.*` entries
- **And** no other `system.*` entry appears

#### Scenario: Explicit policy can grant any system.* tool

**Traces to**: US-4, AS-3
**Category**: Alternate Path

- **Given** an agent `agent-Op` with `policies: {"system.config.set": "allow", "system.*": "deny"}`
- **When** the agent loop assembles the next LLM call
- **Then** `tools[]` contains `system.config.set`
- **And** does not contain other `system.*` entries

#### Scenario: Wildcard precedence — exact match wins

**Traces to**: US-4, AS-4
**Category**: Happy Path

- **Given** an agent with `policies: {"system.config.set": "allow", "system.*": "deny"}`
- **When** `FilterToolsByPolicy` resolves the policy for `system.config.set`
- **Then** the resolved policy is `allow`

#### Scenario: Other core agents inherit the system.*: deny rail

**Traces to**: US-4, AS-5
**Category**: Happy Path

- **Given** core agent `aria` (not the system-tool-bearing one)
- **When** `aria` is constructed
- **Then** `aria`'s seeded policy is `default_policy: allow, policies: {"system.*": "deny"}`

#### Scenario: Ask-policy emits WS event with correlation token

**Traces to**: US-5, AS-1
**Category**: Happy Path

- **Given** an agent with `policies: {web_fetch: ask}`
- **When** the LLM emits a `web_fetch` tool call
- **Then** the gateway emits a WebSocket `tool_approval_required` event with fields `approval_id, tool_call_id, tool_name="web_fetch", args, agent_id, session_id, turn_id, expires_at`
- **And** the loop pauses without executing the tool

#### Scenario: Authenticated approve resumes turn with real result

**Traces to**: US-5, AS-2
**Category**: Happy Path

- **Given** the loop is paused awaiting `approval_id = X`
- **When** an authenticated user POSTs `/api/v1/tool-approvals/X` with `{"action": "approve"}`
- **Then** the agent loop executes the original tool call and resumes
- **And** an audit event `tool.policy.ask.granted` is emitted with the approver's `user_id`

#### Scenario: Authenticated deny resumes with permission_denied

**Traces to**: US-5, AS-3
**Category**: Error Path

- **Given** the loop is paused awaiting `approval_id = X`
- **When** an authenticated user denies
- **Then** the loop appends a synthetic tool result `{error: "permission_denied", message: "User denied tool execution.", approval_id: "X"}`
- **And** resumes

#### Scenario: Approval timeout treated as deny

**Traces to**: US-5, AS-4
**Category**: Edge Case

- **Given** the loop is paused awaiting approval with the default 300-second timeout
- **When** 301 seconds elapse with no response
- **Then** the loop appends a synthetic tool result with `message: "Approval timed out."`
- **And** an audit event `tool.policy.ask.denied` with `reason: "timeout"` is emitted

#### Scenario: Multi-call ask batch is sequential

**Traces to**: US-5, AS-5
**Category**: Edge Case

- **Given** the LLM emits `[tool_call_a (ask), tool_call_b (ask), tool_call_c (allow)]` in one assistant message
- **When** the loop processes them
- **Then** approval is requested for `tool_call_a` first
- **And** only after `tool_call_a` resolves is approval requested for `tool_call_b`
- **And** `tool_call_c` runs without an approval prompt
- **And** at any moment at most one approval is pending for this turn

#### Scenario: Concurrent sessions for same agent — independent approvals

**Traces to**: US-5, AS-6
**Category**: Edge Case

- **Given** two sessions `S1` and `S2` of the same agent both hit `ask` for `web_fetch` simultaneously
- **When** the gateway emits the WS events
- **Then** the two events have distinct `approval_id`s
- **And** an approve on `S1`'s `approval_id` does not advance `S2`

#### Scenario: Gateway restart cancels pending approvals

**Traces to**: US-5, AS-7
**Category**: Edge Case

- **Given** an in-flight approval for `agent-A`, session `S`
- **When** the gateway process restarts
- **Then** on reconnect the SPA receives a `session_state` event indicating the pending approval is gone
- **And** an audit event `tool.policy.ask.denied` with `reason: "restart"` is emitted

#### Scenario: Late approve after timeout returns 410

**Traces to**: US-5, AS-8
**Category**: Error Path

- **Given** an approval that already timed out (synthetic deny injected)
- **When** the user POSTs an `approve` for the same `approval_id`
- **Then** the gateway returns HTTP 410 Gone
- **And** the prior turn is unchanged (no double-execution)

#### Scenario: Non-admin attempting to approve system.* — 403

**Traces to**: US-5, AS-9
**Category**: Error Path

- **Given** an `ask` approval pending for `system.config.set`
- **When** a non-admin authenticated user POSTs `approve`
- **Then** the gateway returns HTTP 403
- **And** the approval remains pending

#### Scenario: Unauthenticated approve — 401

**Traces to**: US-5, AS-10
**Category**: Error Path

- **Given** an `ask` approval pending
- **When** an unauthenticated request hits the approve endpoint
- **Then** the gateway returns HTTP 401

#### Scenario: Approval queue saturation — synthetic deny

**Traces to**: US-5, AS-11
**Category**: Edge Case

- **Given** `gateway.tool_approval_max_pending` is at its default value of **64**
- **And** 64 approvals are currently pending
- **When** the LLM emits the 65th ask call
- **Then** the loop synthesises a deny with `reason: "saturated"`
- **And** no `tool_approval_required` WS event is emitted for this call
- **And** a system message `{type: "saturation_block"}` is appended to the affected session's transcript (FR-016, MAJ-009)

#### Scenario: User cancel resumes with synthetic deny

**Traces to**: US-5, AS-12
**Category**: Alternate Path

- **Given** an approval pending
- **When** the user POSTs `{"action": "cancel"}`
- **Then** the loop appends a synthetic deny with `message: "user cancelled"`

#### Scenario: GET /api/v1/tools returns full registry snapshot

**Traces to**: US-6, AS-1
**Category**: Happy Path

- **Given** the central builtins registry has B entries and the MCP registry has M_accepted entries (after collision rejection)
- **When** `GET /api/v1/tools` is called
- **Then** the response is a JSON list of `B + M_accepted` entries with `{name, description, scope, category, source}`

#### Scenario: GET /api/v1/agents/{id}/tools returns filtered view

**Traces to**: US-6, AS-2
**Category**: Happy Path

- **Given** an agent `agent-A`
- **When** `GET /api/v1/agents/agent-A/tools` is called
- **Then** the response includes the per-tool effective policy map and the filtered tool set the LLM would see

#### Scenario: Legacy /api/v1/tools/builtin returns 404

**Traces to**: US-6, AS-3
**Category**: Error Path

- **Given** the gateway is booted post-redesign
- **When** `GET /api/v1/tools/builtin` is called
- **Then** the response is HTTP 404

#### Scenario: Preset apply replaces policy map after confirmation

**Traces to**: US-6, AS-4
**Category**: Happy Path

- **Given** the SPA agent profile with custom per-tool entries
- **When** the user clicks the "Cautious" preset
- **Then** the SPA shows a confirmation dialog
- **And** on confirm, the policy map is replaced (not merged) with the preset's contents

#### Scenario: Corrupt custom-agent config — agent skipped, boot continues

**Traces to**: US-7, AS-1
**Category**: Error Path

- **Given** a custom agent's `agent.json` is unparseable (invalid JSON)
- **When** the gateway boots
- **Then** an audit event `agent.config.corrupt` with severity HIGH is emitted (fields: `agent_id, agent_type: "custom", path, error`)
- **And** the agent is not activated
- **And** the gateway boot continues for the remaining agents

#### Scenario: Corrupt core-agent config — gateway exits non-zero

**Traces to**: US-7, AS-2
**Category**: Error Path

- **Given** Ava's `agent.json` is unparseable
- **When** the gateway boots
- **Then** an audit event `agent.config.corrupt` with severity HIGH is emitted
- **And** the gateway exits with a non-zero code

#### Scenario: Inaccessible config file — same severity rules apply

**Traces to**: US-7, AS-3
**Category**: Edge Case

- **Given** a custom agent's `agent.json` exists but cannot be opened (permissions / OS lock)
- **When** the gateway boots
- **Then** an audit event `agent.config.corrupt` is emitted with `error` describing the access failure
- **And** the agent is not activated
- **And** boot continues

#### Scenario: Builtin-vs-MCP collision — builtin wins

**Traces to**: Edge Cases / FR-018
**Category**: Edge Case

- **Given** the central builtins registry contains `web_fetch`
- **When** an MCP server attempts to register a tool named `web_fetch`
- **Then** the MCP entry is rejected
- **And** an audit event `tool.collision.mcp_rejected` is emitted with `conflict_with: "builtin"`

#### Scenario: MCP-vs-MCP collision — first wins

**Traces to**: Edge Cases / FR-024
**Category**: Edge Case

- **Given** MCP server `srv-A` has registered a tool named `mcp_search`
- **When** MCP server `srv-B` attempts to register a tool also named `mcp_search`
- **Then** `srv-B`'s entry is rejected
- **And** an audit event with `conflict_with: "srv-A"` is emitted

#### Scenario: Admin-ask fence downgrades allow→ask on custom agents

**Traces to**: US-4, AS-3 / FR-061
**Category**: Happy Path

- **Given** a custom agent `agent-Custom` with `policies: {"system.config.set": "allow"}`
- **And** `system.config.set` returns `RequiresAdminAsk()` true
- **When** the agent loop assembles the next LLM call
- **Then** `tools[]` contains `system.config.set`
- **And** the per-tool effective-policy map for that tool reports `ask` (not `allow`)
- **And** when the LLM calls the tool, the loop pauses and emits `tool_approval_required`

#### Scenario: Admin-ask fence does not downgrade core agents

**Traces to**: US-4, AS-2 / FR-061
**Category**: Happy Path

- **Given** the Ava core agent with `policies: {"system.agent.create": "allow", ...}`
- **When** the agent loop assembles the next LLM call
- **Then** the per-tool effective-policy map for `system.agent.create` reports `allow` (fence does not apply to core agents)

#### Scenario: Admin-ask fence is scoped to RequiresAdminAsk tools only

**Traces to**: US-4, AS-3 / FR-061
**Category**: Happy Path

- **Given** a custom agent with `policies: {"read_file": "allow"}` (read_file does not require admin)
- **When** the agent loop assembles the next LLM call
- **Then** the per-tool effective-policy map for `read_file` reports `allow` (fence is scoped — only triggers for `RequiresAdminAsk` tools)

#### Scenario: Mixed-policy ask batch — deny short-circuits all subsequent calls

**Traces to**: US-5, AS-5 / FR-065
**Category**: Edge Case

- **Given** the LLM emits `[A (ask), B (allow), C (ask), D (allow)]` in one assistant message
- **When** the user denies A
- **Then** B, C, and D are all short-circuited with synthetic deny `reason: "batch_short_circuit"`
- **And** a single combined audit event `tool.policy.ask.denied` is emitted with `cancelled_tool_call_ids: [B_id, C_id, D_id]` and `reason: "batch_short_circuit"`
- **And** no per-call audit events are emitted for B, C, or D

#### Scenario: Tools[] dedup — duplicate name dropped deterministically and call continues

**Traces to**: (Behavioral / FR-066)
**Category**: Edge Case

- **Given** the registry inadvertently contains two entries named `web_fetch` (one builtin, one MCP from server `srv-A` that slipped past FR-034 due to a race)
- **When** the agent loop assembles the next LLM call
- **Then** the duplicate is dropped using deterministic source-tag ordering (`builtin` < `mcp:srv-A` < `mcp:srv-B`); the builtin entry is kept
- **And** an audit event `tool.assembly.duplicate_name` is emitted at HIGH severity with `{tool_name: "web_fetch", sources: ["builtin", "mcp:srv-A"], kept: "builtin"}`
- **And** the LLM call proceeds with the deduplicated `tools[]`
- **And** the LLM call is **not** failed (resolves MAJ-006: graceful recovery, not fail-everywhere)

#### Scenario: MCP server rename — atomic eviction and addition

**Traces to**: (Edge / FR-068)
**Category**: Edge Case

- **Given** the MCP registry contains entries from server `my_search`
- **And** an in-flight turn that has just made one LLM call with `my_search.query` available
- **When** the operator updates `mcp.servers.my_search` config to rename it to `team_search`
- **And** `ReloadProviderAndConfig` runs
- **Then** under a single write-lock acquisition, the `my_search.*` entries are evicted and `team_search.*` entries are added
- **And** an audit event `mcp.server.renamed` (HIGH) is emitted with `{old_name: "my_search", new_name: "team_search"}`
- **And** the next LLM call in the same turn observes only the `team_search.*` set (no torn intermediate state)

#### Scenario: SIGKILL recovery — orphaned tool_call gets synthetic deny on next boot

**Traces to**: US-5, AS-7 / FR-069
**Category**: Edge Case

- **Given** a session JSONL transcript whose tail contains a `tool_call` with no matching `tool_result` (gateway was SIGKILL'd while paused awaiting approval)
- **And** no in-process pending approval matches the orphaned `tool_call_id` (process was killed)
- **When** the gateway restarts and the session is loaded
- **Then** a synthetic entry `{role: "system", type: "turn_cancelled_restart", tool_call_id: <orphan>, reason: "ungraceful_shutdown_recovery"}` is appended
- **And** an audit event `tool.policy.ask.denied` with `reason: "restart"` is emitted at session-load time
- **And** the orphaned turn is not resumed; the next user message starts a fresh turn

#### Scenario: Wildcard tie-break — equal-segment-count, longer prefix wins

**Traces to**: US-1 / FR-071
**Category**: Edge Case

- **Given** an agent with `policies: {"system.alerts.long_thing.*": "deny", "system.agent.subagent.*": "ask"}` (both wildcards have 4 segments)
- **When** `FilterToolsByPolicy` resolves the policy for `system.alerts.long_thing.fire`
- **Then** the resolved policy is `deny` (longer prefix string wins)
- **When** resolving for `system.agent.subagent.run`
- **Then** the resolved policy is `ask`

#### Scenario: session_state — pending approvals scoped to authenticated user

**Traces to**: US-5, AS-7 / FR-052, FR-073
**Category**: Edge Case

- **Given** two users `alice` (admin) and `bob` (user); each with sessions that have pending approvals
- **When** `bob` opens a fresh WS connection
- **Then** the gateway emits `session_state` with `{type: "session_state", user_id: "bob", pending_approvals: [<bob's only>], emitted_at: <iso8601>}`
- **And** the payload does NOT include `alice`'s pending approvals
- **When** `alice` opens her WS connection
- **Then** the payload includes both `alice`'s and `bob`'s pending approvals (admin scope)

#### Scenario: Tool-execution-time policy re-check — deny mid-turn aborts in-flight call

**Traces to**: US-1, AS-6 / FR-079 (revision 6 addition)
**Category**: Edge Case

- **Given** an LLM has just emitted a `tool_call` for `exec` whose effective policy at filter time was `allow`
- **When** the operator PUTs an updated config setting `policies: {exec: deny}` for the agent
- **And** the policy pointer swaps before the loop calls `Execute` on the in-flight tool call
- **Then** the loop re-resolves the effective policy for `exec` immediately before execution
- **And** observes `deny`
- **And** synthesises `permission_denied` instead of running `exec`
- **And** an audit event `tool.policy.deny.attempted` (WARN) is emitted with `tool_call_id` and a note `mid_turn_policy_change`

#### Scenario: Stale policy entry for nonexistent tool is silently ignored

**Traces to**: Edge Cases / FR-019
**Category**: Edge Case

- **Given** an agent config with `policies: {removed_tool: allow, read_file: allow}` where `removed_tool` is not registered
- **When** the agent loop assembles the next LLM call
- **Then** `tools[]` contains `read_file` and no error is raised

---

## Test-Driven Development Plan

### Test Hierarchy

| Level       | Scope                        | Purpose                                    |
|-------------|------------------------------|--------------------------------------------|
| Unit        | Registry, filter, boot validator, constructor seeds, atomic deps deref | Logic in isolation |
| Integration | Agent loop turn assembly + REST + WS + audit | Module composition |
| E2E         | SPA → embedded binary → real LLM | Full workflow including provider |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1  | `TestBuiltinRegistry_Register_Lookup` | Unit | (foundation) | Register N tools; Get(name) works for each. |
| 2  | `TestBuiltinRegistry_Describe_AllRegistered` | Unit | "New builtin tool appears..." | Snapshot covers every registered tool. |
| 3  | `TestBuiltinRegistry_NameCollision_BuiltinWins` | Unit | "Builtin-vs-MCP collision" | First-call wins; second errors. |
| 4  | `TestMCPRegistry_DynamicAddRemove` | Unit | "MCP tool appears..." | Add/remove entries; snapshot reflects state. |
| 5  | `TestMCPRegistry_FirstServerWins` | Unit | "MCP-vs-MCP collision" | Second registration of same name from different server is rejected; warning + audit. |
| 6  | `TestFilterToolsByPolicy_DenyExcludes` | Unit | "Filter excludes deny..." | Per-tool deny removes entry. |
| 7  | `TestFilterToolsByPolicy_DefaultDenyOnlyExplicitAllow` | Unit | "Default-deny custom agent..." | `default_policy: deny` + 2 allow. |
| 8  | `TestFilterToolsByPolicy_AskIncluded` | Unit | "Ask-policy tool is sent..." | `ask` returns tool with effective policy `ask`. |
| 9  | `TestFilterToolsByPolicy_GlobalDenyOverridesAgentAllow` | Unit | "Operator-global deny overrides..." | Global deny wins; audit emitted. |
| 10 | `TestFilterToolsByPolicy_GlobalDefaultDenyStripsAll` | Unit | "Global default-deny strips..." | Empty `tools[]`. |
| 11 | `TestFilterToolsByPolicy_StaleEntryIgnored` | Unit | "Stale policy entry..." | Unknown name → no error, ignored. |
| 12 | `TestFilterToolsByPolicy_PrefixWildcard` | Unit | "Custom agent default-denies..." + "Wildcard precedence" | `system.*` wildcard match; exact wins. |
| 13 | `TestFilterToolsByPolicy_ScopeCoreCustomAgentGate` | Unit | (regression of existing scope gate) | Existing `ScopeCore` + custom agent rules preserved. |
| 14 | `TestConfigValidator_CorruptCustom_SkipsAndAudits` | Unit | "Corrupt custom-agent config..." | Unparseable custom config → HIGH audit; agent not activated; boot continues. |
| 15 | `TestConfigValidator_CorruptCore_AbortsBoot` | Unit | "Corrupt core-agent config..." | Unparseable core config → HIGH audit; gateway exits non-zero. |
| 16 | `TestConfigValidator_InaccessibleFile_SameRules` | Unit | "Inaccessible config file..." | Permission/lock error treated identically to parse error. |
| 17 | `TestAgentConstructor_CustomAgent_SeedsSystemDeny` | Unit | "Custom agent default-denies..." | Newly constructed custom agent has `policies: {"system.*": "deny"}`. |
| 18 | `TestAgentConstructor_CoreAgent_SeedsRailPlusAllowances` | Unit | "Other core agents inherit..." + Ava | Core agent constructor seeds the rail + explicit allowances (Ava: 4 system.* allows). |
| — | (orders 19–23 reserved; previously held migrator tests removed in revision 3) | — | — | — |
| 24 | `TestDepsAtomicPointer_HotSwap` | Unit | "Tool Execute observes post-swap deps..." | Concrete tool sees post-swap value via per-call deref. |
| 25 | `TestPolicyAtomicPointer_NoTornReads` | Unit | "Concurrent reload + turn assembly..." | Race-detector test. |
| 26 | `TestAgentLoop_TurnAssembly_FilterApplied` | Integration | "Filter excludes deny..." | FakeProvider captures `tools[]`. |
| 27 | `TestAgentLoop_TurnAssembly_AvaSeededSystemTools` | Integration | "Ava receives its seeded..." | 4 known names present. |
| 28 | `TestAgentLoop_TurnAssembly_CustomAgentNoSystem` | Integration | "Custom agent default-denies..." | Zero `system.` in `tools[]`. |
| 29 | `TestAgentLoop_TurnAssembly_OtherCoreAgentsDeniedSystem` | Integration | "Other core agents inherit..." | F-02 / Q9. |
| 30 | `TestAgentLoop_HotReload_RegistryStable` | Integration | "Hot reload preserves registry..." | 100 reloads; entry count stable. |
| 31 | `TestAgentLoop_Tier13_SurvivesReloads` | Integration | "Tier13 tools survive..." | concurrent reloads + reads. |
| 32 | `TestAgentLoop_MCPConnect_NextTurnIncludes` | Integration | "MCP tool appears after..." | dynamic. |
| 33 | `TestAgentLoop_MidTurn_PolicyChange` | Integration | "Mid-turn policy change..." | new snapshot on next call. |
| 34 | `TestAgentLoop_AskPolicy_EmitsApprovalEvent` | Integration | "Ask-policy emits WS event..." | event payload includes correlation token. |
| 35 | `TestAgentLoop_AskApprove_ResumesWithResult` | Integration | "Authenticated approve resumes..." | authenticated path. |
| 36 | `TestAgentLoop_AskDeny_ResumesWithPermissionDenied` | Integration | "Authenticated deny resumes..." | denial path. |
| 37 | `TestAgentLoop_AskTimeout_TreatedAsDeny` | Integration | "Approval timeout..." | timeout path + audit. |
| 38 | `TestAgentLoop_AskBatchSequential` | Integration | "Multi-call ask batch..." | one-at-a-time. |
| 39 | `TestAgentLoop_AskConcurrentSessions` | Integration | "Concurrent sessions..." | distinct approval_ids. |
| 40 | `TestAgentLoop_AskRestart_CancelsPending` | Integration | "Gateway restart cancels..." | reconnect resets. |
| 41 | `TestAgentLoop_AskLateApprove_Returns410` | Integration | "Late approve after timeout..." | post-timeout safety. |
| 42 | `TestAgentLoop_AskSaturation_SyntheticDeny` | Integration | "Approval queue saturation..." | max_pending guard. |
| 43 | `TestAgentLoop_AskCancel` | Integration | "User cancel resumes..." | cancel action. |
| 44 | `TestREST_ApproveAuth_NonAdminSystemTool403` | Integration | "Non-admin attempting to approve system.*..." | RBAC check. |
| 45 | `TestREST_ApproveAuth_Unauthenticated401` | Integration | "Unauthenticated approve — 401" | withAuth bound. |
| 46 | `TestREST_GetTools_FullSnapshot` | Integration | "GET /api/v1/tools returns full..." | source field present. |
| 47 | `TestREST_GetAgentTools_FilteredView` | Integration | "GET /api/v1/agents/{id}/tools..." | effective policies + filtered set. |
| 48 | `TestREST_GetBuiltinTools_Returns404` | Integration | "Legacy /api/v1/tools/builtin..." | endpoint removed. |
| 49 | `TestREST_GetTools_PreviouslyMissingPresent` | Integration | "Previously missing builtins..." | 6 names. |
| 50 | `TestREST_GetTools_SearchToolNamesCanonical` | Integration | "Search tool names match runtime" | `tool_search_tool_*`. |
| 51 | `TestAudit_Events_Emitted` | Integration | (Audit & Observability) | Each event type fires under its trigger. |
| 52 | `TestProviderDefs_ShapeUnchanged` | Integration | (regression FR-026) | byte-stable wire shape. |
| 53 | `TestE2E_AgentProfileFlow` | E2E | US-6 | SPA + embedded binary, two endpoints, profile renders. |
| 54 | `TestE2E_AskApprovalFlow_RealLLM` | E2E | US-5 | Real LLM provider, ask round-trip approve + deny + cancel. |
| 55 | `TestE2E_AskRestart_RealLLM` | E2E | "Gateway restart cancels..." | Restart mid-pause; SPA reset on reconnect. |
| 56 | `TestE2E_DenyPolicy_LLMDoesNotCall` | E2E | "Filter excludes deny..." | Real model; observed not to attempt the denied tool. |
| 57 | `TestE2E_PrivilegeEscalationGuard_RealLLM` | E2E | "Custom agent default-denies..." | Custom agent + prompt that would trigger `system.config.set`; never appears in `tools[]`; LLM responds without making the call. |
| 58 | `TestE2E_HotReload_NoToolDrop` | E2E | "Tier13 tools survive..." | Reload mid-flight; in-progress turn completes; next turn uses new provider. |

### Test Datasets

#### Dataset: Per-agent + global policy resolution

| # | global_default | global_policies | default_policy | policies | tool requested | Expected Output | Traces to | Notes |
|---|----------------|-----------------|----------------|----------|----------------|-----------------|-----------|-------|
| 1 | allow | {} | allow | {} | read_file | allow | "Filter excludes deny..." | base case |
| 2 | allow | {} | allow | {exec: deny} | exec | deny | "Filter excludes deny..." | per-tool override |
| 3 | allow | {} | allow | {exec: deny} | read_file | allow | "Filter excludes deny..." | sibling unaffected |
| 4 | allow | {} | deny | {read_file: allow} | read_file | allow | "Default-deny custom agent..." | per-tool grants reach |
| 5 | allow | {} | deny | {read_file: allow} | exec | deny | "Default-deny custom agent..." | default denial preserved |
| 6 | allow | {} | allow | {web_fetch: ask} | web_fetch | ask | "Ask-policy tool is sent..." | tool stays in array |
| 7 | allow | {} | allow | {nonexistent: allow} | read_file | allow | "Stale policy entry..." | unknown ignored |
| 8 | allow | {} | (unset) | (unset) | read_file | allow | (foundation) | empty config |
| 9 | allow | {} | allow | {"system.config.set": allow, "system.*": deny} | system.config.set | allow | "Wildcard precedence..." | exact wins |
| 10 | allow | {} | allow | {"system.*": deny} | system.agent.list | deny | "Custom agent default-denies..." | wildcard match |
| 11 | allow | {exec: deny} | allow | {exec: allow} | exec | deny | "Operator-global deny..." | global wins |
| 12 | deny | {} | allow | {} | read_file | deny | "Global default-deny strips..." | global default wins |
| 13 | allow | {} | ask | {} | read_file | ask | (F-18) | default_policy=ask |
| 14 | allow | {} | deny | {web_fetch: ask} | web_fetch | ask | (F-18) | per-tool ask over default deny |
| 15 | ask | {} | allow | {} | read_file | ask | (F-18) | global default ask |
| 16 | allow | {tool_a: ask} | allow | {tool_a: deny} | tool_a | deny | (precedence) | strictest wins |

#### Dataset: Boot-time config validation

| # | Input file state | Agent type | Expected | Traces to | Notes |
|---|------------------|------------|----------|-----------|-------|
| 1 | Valid JSON, well-formed `default_policy` + `policies` | custom | Loaded; agent activated | (foundation) | base case |
| 2 | Valid JSON, well-formed | core (Ava) | Loaded; agent activated | (foundation) | base case |
| 3 | Unparseable JSON | custom | HIGH audit; agent not activated; boot continues | "Corrupt custom-agent config..." | F-04 |
| 4 | Unparseable JSON | core | HIGH audit; gateway exits non-zero | "Corrupt core-agent config..." | F-04 |
| 5 | Permission denied / file locked | custom | HIGH audit; agent not activated | "Inaccessible config file..." | OS-level error |
| 6 | Permission denied / file locked | core | HIGH audit; gateway exits non-zero | "Inaccessible config file..." | OS-level error |
| 7 | File missing entirely | custom | Constructor seeds defaults; agent activated | (foundation) | new agent path |
| 8 | File missing entirely | core | Constructor seeds defaults; agent activated | (foundation) | new agent path |

#### Dataset: Registry registration

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | Register builtin T1 with name "foo" | happy | Lookup returns T1; Describe lists foo | "New builtin tool appears..." | base |
| 2 | Register builtin T2 with name "" | error | Returns error; not registered | (defensive) | empty rejected |
| 3 | Register builtins T1, T2 with same name | error | Second errors; first preserved | "Builtin-vs-MCP collision" + dup detection | dup |
| 4 | Register builtin T1, then MCP T1' with same name | edge | MCP rejected; audit | "Builtin-vs-MCP collision" | builtin wins |
| 5 | MCP server srv-A registers `t`; srv-B registers `t` | edge | srv-B rejected; audit | "MCP-vs-MCP collision" | first-wins |
| 6 | MCP server adds 100 tools | scale | All 100 in MCP registry | (perf bound) | volume |
| 7 | MCP server disconnects | happy | Tools removed (server-scoped) | (Behavioral) | dynamic remove |
| 8 | Builtin T's deps unavailable at boot | edge | Registered with error-Execute | "Tool with unavailable construction-time deps..." | F-16 |

#### Dataset: Ask-mode protocol cases

| # | Setup | Action | Expected | Traces to |
|---|-------|--------|----------|-----------|
| 1 | Single ask call | approve | tool runs | "Authenticated approve resumes..." |
| 2 | Single ask call | deny | synthetic deny | "Authenticated deny resumes..." |
| 3 | Single ask call | wait > timeout | synthetic deny "Approval timed out." | "Approval timeout..." |
| 4 | 3 ask calls in one assistant message | approve A, approve B, approve C | sequential; only one pending at a time | "Multi-call ask batch..." |
| 5 | Two sessions same agent both hit ask | approve only S1 | S1 resumes, S2 still pending | "Concurrent sessions..." |
| 6 | Pending approval | restart gateway | SPA `session_state` reset on reconnect; audit `reason=restart` | "Gateway restart cancels..." |
| 7 | Already-timed-out approval | late approve | 410 Gone; no double-execution | "Late approve after timeout..." |
| 8 | system.config.set ask | non-admin approve | 403 | "Non-admin attempting..." |
| 9 | any ask | unauthenticated | 401 | "Unauthenticated approve — 401" |
| 10 | 32 pending; 33rd ask call | (none) | synthetic deny "approval queue saturated"; no WS event | "Approval queue saturation..." |
| 11 | pending approval | cancel | synthetic deny "user cancelled" | "User cancel resumes..." |

### Regression Test Requirements

This feature **modifies existing functionality**. Preservation table:

| Existing Behaviour | Existing Test | New Regression Test | Notes |
|--------------------|---------------|---------------------|-------|
| Skill summary in system prompt | `pkg/agent/context_test.go` | No — confirm pass | unchanged path |
| `ToProviderDefs` JSON shape | turn-execution tests | Yes — `TestProviderDefs_ShapeUnchanged` | wire format stable |
| `ReloadProviderAndConfig` swaps provider | existing reload tests | Yes — extend to assert registry stability | mechanism changes; outcome identical |
| Ava's 4 `system.*` runtime availability | existing `WireAvaAgentTools` tests | Yes — `TestAgentLoop_TurnAssembly_AvaSeededSystemTools` | seeded policy path |
| `ScopeCore` + custom agent gate | existing filter tests | Yes — `TestFilterToolsByPolicy_ScopeCoreCustomAgentGate` | preserved verbatim |
| `GlobalPolicies` precedence | (none verified end-to-end) | Yes — items 9 + 10 above | new coverage of existing feature |
| `getAgentTools` response shape | existing tests | Yes — `TestREST_GetAgentTools_ResponseShapeStable` | SPA depends on shape |

**Regression dataset**:

| # | Setup | Action | Expected (preserved) |
|---|-------|--------|----------------------|
| R1 | Default agent | Turn calling `read_file` | Identical behaviour |
| R2 | Ava agent | Turn calling `system.agent.create` | Tool runs (mechanism changed; result identical) |
| R3 | Skill `foo` installed | `BuildSystemPrompt` for any agent | Output contains `# Skills` block with `foo`'s summary |
| R4 | `GlobalPolicies: {exec: deny}` | Any agent attempts to call `exec` | `exec` not in `tools[]` |

---

## Functional Requirements

- **FR-001**: System MUST expose two central registries — `BuiltinRegistry` and `MCPRegistry` — each independent of any agent's lifecycle.
- **FR-002**: System MUST register every builtin tool exactly once at boot on the central builtins registry. No per-agent registration loop.
- **FR-003**: System MUST apply `FilterToolsByPolicy` to `(builtins ∪ MCP_accepted)` **before each LLM call** and send only the filtered result as the LLM `tools[]`.
- **FR-004**: System MUST resolve effective policy as `global × agent` × `deny > ask > allow`, exactly matching the existing `pkg/tools/compositor.go:286-374` logic.
- **FR-005**: System MUST preserve `GlobalPolicies` and `GlobalDefaultPolicy` semantics from `ToolPolicyCfg`. [F-01]
- **FR-006**: System MUST preserve the `ScopeCore`-vs-custom-agent gate from the existing filter implementation.
- **FR-007**: System MUST treat `system.*` tools as ordinary builtins; per-agent policy alone determines exposure.
- **FR-008**: System MUST seed newly created custom agents with `default_policy: allow` and `policies: {"system.*": "deny"}`.
- **FR-009**: System MUST resolve policy-map keys with the following deterministic rules (resolves G-02):
  1. **Wildcard syntax**: only a trailing `.*` is a wildcard (e.g., `system.*`, `system.agent.*`). Any other use of `*` (leading, embedded, `*` alone) is a syntactic error and rejected at config-load time with HIGH audit `agent.config.invalid_policy_value`.
  2. **Precedence**: exact-name match wins over any wildcard. Among wildcards, **longest-prefix wins**. Map iteration order is irrelevant — implementation MUST sort wildcard keys by descending prefix length before evaluation.
  3. **Uniformity**: precedence rules apply identically to `Policies` and `GlobalPolicies`. The two layers compose via the existing `deny > ask > allow` precedence (`global × agent` resolution unchanged).
  4. **Catch-all**: there is no `*` catch-all key. The catch-all role is filled exclusively by `default_policy` / `GlobalDefaultPolicy`. A literal `"*"` key is a syntax error.
  5. **Resolution worked example**: for tool `system.agent.create` against policy `{"system.agent.*": "ask", "system.*": "deny", "system.agent.create": "allow"}`, the exact match wins → `allow`. For `system.agent.delete` against the same policy, exact does not match; among wildcards, `system.agent.*` (longer prefix) wins over `system.*` → `ask`.
- **FR-010**: System MUST seed core agents (Ava and others, identified by `coreagent.GetPrompt(id) != ""`) with `default_policy: allow, policies: {"system.*": "deny"}` plus any explicit allow/ask entries that core agent requires (Ava: 4 `system.*` allows). [Q9]
- **FR-011**: System MUST pause the agent loop when an `ask`-policy tool is invoked, emit a WS `tool_approval_required` event with `{approval_id, tool_call_id, tool_name, args, agent_id, session_id, turn_id, expires_at}`, and resume on user response, timeout, saturation, restart, or cancel.
- **FR-012**: System MUST process multiple `ask`-policy tool calls in a single assistant message **sequentially**; at most one approval is pending per turn.
- **FR-013**: System MUST persist no pending-approval state to disk; gateway restart cancels all pending approvals and emits `tool.policy.ask.denied` audit events with `reason: "restart"`.
- **FR-014**: System MUST bind the `POST /api/v1/tool-approvals/{approval_id}` endpoint to existing `withAuth`; unauthenticated requests return 401.
- **FR-015**: System MUST require admin role to approve any ask call where the tool's `RequiresAdminAsk` capability flag is set (resolves G-06). The admin predicate is the existing user-role check `User.Role == config.UserRoleAdmin` (`pkg/gateway/rest_users.go:54-56,116`); no new RBAC infrastructure is required (resolves H-09). Non-admin approve/deny/cancel requests against such an approval return HTTP 403. The flag is a tool-level structural attribute, not a name-prefix check, so MCP servers cannot inherit admin protection by registering a `system.`-prefixed name. The central registry MUST reject any MCP registration whose tool name begins with `system.` (audit `tool.collision.mcp_rejected` with `conflict_with: "reserved_prefix"`).
- **FR-016**: System MUST cap pending approvals at `gateway.tool_approval_max_pending`; **default value is 64** for all variants (resolves G-04, CRIT-002). The Cloud variant may override at deployment time. The sentinel value `0` means "unlimited"; setting it MUST emit a WARN log on boot ("approval saturation guard disabled — DoS risk") and an audit event `gateway.startup.guard_disabled`. **Negative values are rejected** at boot with HIGH audit `gateway.config.invalid_value`; the gateway exits non-zero (resolves MIN-005). Only `0` and positive integers are accepted. When the cap is reached, excess `ask` calls receive a synthetic deny with `reason: "saturated"`, **no `tool_approval_required` WS event is emitted**, but a one-line system message MUST be appended to the affected session's transcript (`{role: "system", type: "saturation_block", message: "Agent action blocked: approval queue saturated. Retry later or contact your administrator."}`) so the user has visibility (resolves MAJ-009). A `system_overload` WS event scoped to the affected session MAY be emitted for SPA UX use.
- **FR-017**: System MUST support `cancel` as a third action on the approval endpoint; result is a synthetic deny `"user cancelled"`.
- **FR-018**: System MUST return HTTP 410 Gone on approve/deny for an `approval_id` that has already resolved (timeout, restart, cancel, etc.).
- **FR-019**: System MUST hold construction-time tool dependencies in an `*atomic.Pointer[T]` and tools' `Execute` MUST `Load()` per call; capturing deps in closures is forbidden.
- **FR-020**: System MUST hold each agent's resolved policy in `*atomic.Pointer[ResolvedPolicy]`; `ReloadProviderAndConfig` MUST `Store` a new pointer (no in-place map mutation). [F-12]
- **FR-021**: System MUST delete the legacy `AgentBuiltinToolsCfg.Mode` and `AgentBuiltinToolsCfg.Visible` fields outright; no migrator, no compat shim. (Pre-1.0; no production users.)
- **FR-022**: Agent constructors MUST seed every newly created custom agent with `default_policy: allow, policies: {"system.*": "deny"}`. Core agent constructors MUST additionally inject any explicit allow/ask entries that core agent requires (Ava: 4 `system.*` allows). [F-02 — addressed via constructor seeding rather than migration since there are no existing on-disk configs to retrofit.]
- **FR-023**: System MUST emit `agent.config.corrupt` (HIGH severity) when an existing `agent.json` cannot be parsed or read at boot. The corrupt-config disposition is determined by **whether the agent's seeded policy contains explicit `system.*` allow entries** (resolves G-03), not by core/custom membership alone:
  - Agents with **explicit `system.*` allows in their constructor seed** (today: only Ava): the gateway exits with a non-zero code. Losing such an agent silently is an availability incident because privileged operations can no longer be invoked through the loop.
  - All other agents (custom, plus non-Ava core agents whose seed is just `default_policy: allow, policies: {"system.*": "deny"}`): the agent is not activated; boot continues. The constructor default is the safe rail and the operator notices via the HIGH audit event.
  The predicate `hasSystemAllowsInConstructorSeed(agentID)` is exposed as a helper in the agent factory and used by both the boot validator and the test fixtures.
- **FR-024**: (reserved — formerly migrator atomicity; no longer applicable.)
- **FR-025**: (reserved — formerly migrator Windows file-lock handling; no longer applicable.)
- **FR-026**: System MUST keep the `ToProviderDefs` JSON shape byte-stable for unchanged inputs (regression guard).
- **FR-027**: System MUST expose `GET /api/v1/tools` returning `(builtins ∪ MCP_accepted)` with `source` discriminator per entry.
- **FR-028**: System MUST expose `GET /api/v1/agents/{id}/tools` returning the per-agent effective policy map and filtered tool set.
- **FR-029**: System MUST return HTTP 404 for `GET /api/v1/tools/builtin`.
- **FR-030**: System MUST register `RemoveSkillTool` via `NewRemoveSkillTool` exactly once at boot.
- **FR-031**: System MUST use names `tool_search_tool_bm25` and `tool_search_tool_regex` across runtime registry and REST.
- **FR-032**: System MUST register `serve_workspace`, `run_in_workspace`, `build_static`, `handoff`, and `return_to_default` on the central builtins registry at boot.
- **FR-033**: When a builtin's construction-time deps are unavailable, the system MUST register the tool with an `Execute` that returns a stable error string (rather than skipping registration). [F-16]
- **FR-034**: System MUST reject duplicate-name registrations; on builtin-vs-MCP collision, builtin wins; on MCP-vs-MCP collision, the first-registered server wins; rejection emits `tool.collision.mcp_rejected` audit. [F-11]
- **FR-035**: System MUST silently exclude policy entries that reference unregistered tool names from the filter output.
- **FR-036**: System MUST preserve `BuildSystemPrompt`'s skill-summary path unchanged.
- **FR-037**: System MUST NOT call `ToolCompositor.ComposeAndRegister` from any live path; the function is deleted.
- **FR-038**: System MUST emit the audit events listed in the Audit & Observability table at the specified severity with the specified fields.
- **FR-039**: System MUST emit the metrics listed in the Audit & Observability table.
- **FR-040**: System MUST follow the Boot Order in the dedicated section above; builtin registry population completes before the MCP layer is permitted to accept connections.
- **FR-041**: System MUST evaluate the policy filter **per LLM call** (not per turn); a policy update mid-turn is observed by the next LLM call. [Unasked Q1]
- **FR-042**: System MUST notify the SPA via a `session_state` WS event on reconnect after restart, indicating any prior pending approval is gone. [F-03]
- **FR-043**: SPA MUST show a confirmation dialog before applying a policy preset (replace semantics, not merge). [F-17]
- **FR-044**: System SHOULD keep the four frontend policy presets as client-side conveniences.

### Revision-4 additions (resolve grill findings G-01 through G-22)

- **FR-045**: System MUST delete the `ScopeSystem` constant and all references thereto. The 35 `system.*` tools' `Scope()` MUST return `ScopeCore`. `WireSystemTools` and `WireAvaAgentTools` MUST be deleted. Any compilation references to those symbols cause CI to fail. (Resolves G-01.)
- **FR-046**: **Superseded by FR-065** (revision 6 reconciliation, MAJ-003). The auto-cascade behaviour is unchanged; the audit shape is the **single combined event** specified in FR-065. This FR remains in place as a back-reference target only.
- **FR-047**: The `tool.policy.ask.denied` audit event's `reason` enum MUST include all of: `user`, `timeout`, `cancel`, `restart`, `saturated`, `batch_short_circuit`. (Resolves G-08.)
- **FR-048**: On graceful shutdown with paused turns, the system MUST append a synthetic terminal entry `{role: "system", type: "turn_cancelled_restart", approval_id, tool_call_id}` to each affected session's JSONL transcript before exit. Paused turns are NOT resumed on next boot. LLM provider usage from any in-flight request mid-cancellation is attributed to the cancelled turn on a best-effort basis and logged. (Resolves G-09.)
- **FR-049**: Boot-time validation MUST reject any `default_policy`, `GlobalDefaultPolicy`, or per-tool policy value not in `{"allow", "ask", "deny"}` (empty string is treated as `"allow"` by `ResolvePolicy` and remains permitted). Rejection emits HIGH audit `agent.config.invalid_policy_value` and applies the same skip-or-abort disposition as a parse failure (per FR-023). (Resolves G-10.)
- **FR-050**: Tools registered on the central builtins registry MUST use **pointer receivers** and MUST hold construction-time deps as `*atomic.Pointer[T]` fields, never as direct struct fields populated at construction. A reflection-based test (`TestRegistry_ToolDepsContract`) walks the registry and asserts the constraint. (Resolves G-11.)
- **FR-051**: When an MCP server is renamed in config (config-name change), the system MUST treat the new name as a new server: previous-name entries are removed when the prior connection drops; agent policies referring to the old name fall through to `default_policy`; an audit event `mcp.server.renamed` (HIGH) is emitted. (Resolves G-12.) Issue #153 tracks the durable-identity follow-up.
- **FR-052**: `session_state` reset is a per-WS-connection one-shot emitted on every WS connection establishment. Payload includes the current pending-approval set scoped to the session (empty after a restart). The SPA reconciles its own state on connect; no server-side persistence required. (Resolves G-14.)
- **FR-053**: `TestProviderDefs_ShapeUnchanged` MUST compare against a golden file at `pkg/tools/testdata/provider_defs.golden.json` covering the full registered-builtins set sorted by name. Regeneration: `go test ./pkg/tools/ -run TestProviderDefs_ShapeUnchanged -update`. (Resolves G-17.)
- **FR-054**: `tool.policy.ask.granted` and `tool.policy.ask.denied` audit events MUST carry `tool_name, agent_id, session_id, turn_id` in addition to `approval_id`. (Resolves G-18.)
- **FR-055**: Frontend MUST be audited and updated alongside the catalog deletion: any reference to `bm25_search` or `regex_search` in `src/` and `packages/ui/` is replaced with `tool_search_tool_bm25` / `tool_search_tool_regex`. (Resolves G-16.)
- **FR-056**: Documentation cleanup as part of Phase E: CLAUDE.md updated (already applied in revision 4); `docs/internal/architecture/AS-IS-architecture.md` updated to match; `docs/internal/BRD/Omnipus_BRD_AppendixD_System_Agent.md` carries a "superseded — kept for historical record" notice. The Phase A1 implementing subagent cites the exact constructor symbol that seeds Ava's `system.*` allowances (lives in `pkg/coreagent/`). (Resolves G-15.)
- **FR-057**: Boot validation MUST emit a WARN audit `agent.config.unknown_tool_in_policy` per agent listing any policy entries referring to unregistered tool names. (Resolves Unasked Q1.)
- **FR-058**: Concurrent SPA writes to `agent.json` MUST use the existing `fileutil.WriteFileAtomic` pattern. Boot validation reads each file once at boot order step 3; SPA writes after boot trigger a per-agent policy recompute via `(*atomic.Pointer[ResolvedPolicy]).Store`. (Resolves Unasked Q7.)
- **FR-059**: `RequiresAdminAsk()` is a new method on the `Tool` interface (default implementation returns `false` via an embedded `BaseTool` mixin to avoid mass-modifying every existing tool). Every tool in `pkg/sysagent/tools/` overrides it to `return true`. The approve endpoint dispatches on this flag, not on tool name prefix. MCP tools opt in **only** via explicit operator config per FR-064 (no implicit elevation; the MCP adapter defaults to `false` unless the server config lists the tool in `requires_admin_ask`). (Resolves G-06; CRIT-001 reconciliation.)
- **FR-060**: The central builtins registry MUST reject any registration whose name begins with `system.` from a non-builtin source (i.e., MCP). Audit event `tool.collision.mcp_rejected` with `conflict_with: "reserved_prefix"`. (Resolves G-06.)

### Revision-5 additions (resolve grill findings H-01 through H-22)

- **FR-061**: **Structural fence on `RequiresAdminAsk` tools for custom agents** (resolves H-01). The policy resolver MUST downgrade an effective `allow` to `ask` whenever (a) the resolved tool's `RequiresAdminAsk()` returns `true` AND (b) the agent is **not** a core agent (`coreagent.GetPrompt(id) == ""`). This is applied **after** the global × agent precedence resolution but **before** the filter emits the per-tool effective-policy map. Concrete effect: a custom agent with `policies: {"system.config.set": "allow"}` sees `system.config.set` in `tools[]` with effective policy `ask`, not `allow` — the human-in-the-loop approval gate is **structurally guaranteed**, regardless of operator typing. Core agents (Ava etc.) are unaffected: their `allow` stays `allow`. The fence is documented; the implementing test (`TestFilterToolsByPolicy_AdminAskFenceOnCustomAgents`) asserts the downgrade for at least three `RequiresAdminAsk` tools and one non-`RequiresAdminAsk` tool to confirm the fence is scoped correctly.
- **FR-062**: **Boot Order step 3a — constructor-seed disposition map** (resolves H-02). Before step 3 reads any `agent.json`, step 3a builds an in-memory `map[agentID]bool` indicating whether each agent's **static factory seed** (i.e., the compile-time constructor seed in `pkg/coreagent/`) contains explicit `system.*` allows. This is a static computation against the agent factory's declared seeds, not against on-disk files. Step 3's invalid-policy-value rejection (FR-049) and corrupt-config disposition (FR-023) consult this map. An agent whose factory seed contains explicit `system.*` allows aborts boot on parse/validation failure; all others skip + continue. Today only Ava qualifies for the abort path. Adding a new factory-seeded `system.*: allow` agent is a deliberate change with reviewer attention; the map is computed at compile time and tested via `TestBoot_ConstructorSeedDispositionMap`.
- **FR-063**: **Audit-emit failure during boot abort** (resolves H-17, H-02). When the audit subsystem is unavailable during a boot-abort path (`agent.config.corrupt`, `agent.config.invalid_policy_value`), the gateway MUST print a structured stderr line of the form `BOOT_ABORT_REASON=<event_name> agent_id=<id> path=<path> error=<msg>` **before** exiting non-zero. Format is documented for log shippers to grep.
- **FR-064**: **MCP-side opt-in for `RequiresAdminAsk`** (resolves H-04). MCP server config (`mcp.servers.<name>`) MAY declare a per-tool `requires_admin_ask: [tool_a, tool_b]` array. The MCP adapter inspects this list at registration; tools listed there have their `RequiresAdminAsk()` return `true`. MCP tools NOT in any such list default to `false`. A test (`TestRegistry_AllSysagentToolsRequireAdminAsk`) walks the central builtins registry and asserts every tool whose package path is `pkg/sysagent/tools/...` returns `true`; the test fails if a new privileged tool is added without the override.
- **FR-065**: **Mixed-policy batch deny short-circuit** (resolves H-05). When the user denies or cancels the K-th call in a sequential ask batch, ALL subsequent calls K+1..N — regardless of their individual policy (`allow`, `ask`, or `deny`) — are short-circuited with synthetic deny `reason: "batch_short_circuit"`. The audit MUST emit a single combined event `tool.policy.ask.denied` with `reason: "batch_short_circuit"` and a list of `cancelled_tool_call_ids` rather than one event per cancelled call. The user's deny/cancel acts as a "stop the agent's current step" command.
- **FR-066**: **`tools[]` deduplication invariant** (resolves H-06). After filter + assembly, the resulting `tools[]` MUST be name-unique. The assembly path performs a final dedup pass: encountering a duplicate name is treated as an internal invariant violation that emits HIGH audit `tool.assembly.duplicate_name` with `{tool_name, sources: [...]}` and **fails the LLM call** with a `synthetic_error` returned to the loop, rather than silently dropping the duplicate. A unit test (`TestAssembly_RejectsDuplicateName`) constructs a controlled-duplicate registry and asserts both the audit emission and the call failure.
- **FR-067**: **`Category() ToolCategory` interface method** (resolves H-16, Unasked Q1). The `Tool` interface gains a `Category() ToolCategory` method, default implementation on `BaseTool` returning `CategoryCore`. Categories: `CategoryCore`, `CategorySystem` (sysagent tools), `CategorySkills`, `CategoryWeb`, `CategoryFilesystem`, `CategoryMCP`, `CategoryWorkspace`. The `GET /api/v1/tools` endpoint sources `category` from this method. Existing categories defined in `pkg/tools/catalog.go:24-32` carry over.
- **FR-068**: **MCP server rename atomicity** (resolves H-07). On `ReloadProviderAndConfig` detecting an MCP server config-name change, the MCP registry MUST evict the old-named entries and add the new-named entries **atomically** under the same write-lock acquisition; per-agent policy pointer recomputes happen **after** the registry update, so any LLM call assembling `tools[]` between the rename and the policy recompute will not observe a torn intermediate state.
- **FR-069**: **SIGKILL / OOM recovery** (resolves H-08). On every session resume / next gateway boot, the loop MUST inspect the tail of each session's JSONL transcript. If the last entry is a `tool_call` without a matching `tool_result` AND there is no in-process pending approval matching that `tool_call_id`, the loop MUST append a synthetic `{role: "system", type: "turn_cancelled_restart", approval_id: null, tool_call_id: <...>, reason: "ungraceful_shutdown_recovery"}` entry as part of session-load and emit `tool.policy.ask.denied` with `reason: "restart"`. This makes shutdown-recovery idempotent under SIGKILL, OOM-kill, and power-loss.
- **FR-070**: **Approval state machine — terminal states and transitions** (resolves H-10, G-19). The complete approval state table is binding (see "Approval State Table" subsection below). Implementations MUST NOT introduce new states or transitions without spec amendment.
- **FR-071**: **Wildcard tie-break** (resolves H-11). Among wildcards of equal segment count, the ordering rule is: longest by **character count of the prefix preceding `.*`**; ties (equal char count) broken by **lexicographic comparison of the prefix string**, ascending (deterministic). Compiled into the resolved-policy snapshot at recompute time, not at filter time (resolves H-21).
- **FR-072**: **Empty-string policy value handling** (resolves H-12, H-20). Empty string `""` for `default_policy`, `GlobalDefaultPolicy`, or any per-tool entry is treated as `"allow"` by `ResolvePolicy` (existing behaviour preserved) **but** the boot validator MUST emit `agent.config.empty_policy_value_coerced` (INFO severity) listing the affected entries so the silent coercion is operator-visible.
- **FR-073**: **`session_state` scoping** (resolves H-13). The `session_state` payload's pending-approval set is scoped to the WS connection's authenticated user identity. Admins see all pending approvals; non-admins see only their own sessions' approvals. Two browser tabs for the same user each receive their own one-shot reset, both with the same payload.
- **FR-074**: **Audit field enrichment on `requested → granted/denied`** (resolves H-14). `tool.policy.ask.granted` and `tool.policy.ask.denied` MUST carry `args_hash` in addition to the fields specified in FR-054, so a single audit row fully answers "what arguments were approved/denied."
- **FR-075**: **Reload-time read consistency** (resolves H-15). Post-boot reads of `agent.json` triggered by `ReloadProviderAndConfig` MAY observe either side of an SPA write — this is acceptable because both sides are valid configurations. The reload reads, validates, and stores the policy pointer in one operation; if the validation fails (invalid value, malformed), the prior policy pointer is retained and `agent.config.invalid_policy_value` audit fires (no in-flight policy break). No new locking required.
- **FR-076**: **Frontend canonical-name regression test** (resolves H-22). Frontend MUST include a unit test (`tests/canonicalToolNames.test.ts` or equivalent) asserting `bm25_search` and `regex_search` do not appear as literal strings in `src/` and `packages/ui/`; `tool_search_tool_bm25` and `tool_search_tool_regex` are the only canonical names. Test runs in Phase D3.
- **FR-077**: **Approval triage in Phase B/C** (resolves H-18). The Implementation Orchestration Plan's Phase C is amended: CRITICAL and MAJOR review findings MUST be fixed before merge. MINOR and OBSERVATION findings MAY be fixed in the same revision OR filed as follow-up issues at the lead's discretion. Where reviewers' findings conflict, `architect` adjudicates and the lead documents the decision in the PR description.
- **FR-078**: **Drop Cloud-specific saturation default for now** (resolves H-19). FR-016 is amended: default `gateway.tool_approval_max_pending` is **64** for all variants until Cloud bring-up. The Cloud variant config can override at deployment time.

### Revision-6 additions (resolve grill findings CRIT-001..CRIT-003 + MAJ-001..MAJ-009 + MIN-001..MIN-007 + OBS-001..OBS-005)

- **FR-079**: **Tool-execution-time policy re-check** (resolves MAJ-002). Before each tool's `Execute` runs, the loop MUST `Load()` the per-agent policy pointer and re-resolve the tool's effective policy. If `deny`, synthesise `permission_denied` and skip execution; if `ask`, treat as a new ask call (re-pause, re-emit WS event with a fresh `approval_id`); if `allow`, run. Closes the TOCTOU window between filter-time `tools[]` assembly and tool execution. Audit `tool.policy.deny.attempted` MUST include the note `"mid_turn_policy_change"` when the re-check observes a flip from the filter snapshot.
- **FR-080**: **`args_hash` algorithm specification** (resolves MAJ-004). `args_hash` is `sha256(canonicaljson(args))` rendered as a 64-character lowercase hex string, where `canonicaljson` is JSON with sorted object keys, no whitespace, UTF-8, RFC 8785 compatible. The hash is an identity/correlation field, **not** a confidentiality protection. Audit emitters that capture sensitive-arg tools (e.g., `system.config.set` with API keys) MUST additionally record a redacted preview field `args_preview` (first 32 chars of each top-level value, with values matching common-secret regex masked as `"<redacted>"`). Test `TestAuditArgsHash_Deterministic` asserts byte-equality across 100 runs with `map`-iteration noise.
- **FR-081**: **`session_state` WS event payload schema** (resolves MAJ-005). The payload shape is binding:
  ```json
  {
    "type": "session_state",
    "user_id": "<authenticated user uid>",
    "pending_approvals": [
      {"approval_id": "<id>", "session_id": "<id>", "tool_name": "<name>", "agent_id": "<id>", "expires_in_ms": <number>}
    ],
    "emitted_at": "<RFC3339 timestamp>"
  }
  ```
  Empty pending set is `"pending_approvals": []` (not null). Tests assert schema conformance.
- **FR-082**: **`tool_approval_required` payload uses `expires_in_ms`** (resolves OBS-004). Replace `expires_at` (absolute timestamp, clock-skew-sensitive) with `expires_in_ms` (relative duration from receipt). FR-011's payload is amended accordingly.
- **FR-083**: **MCP rename detection** (resolves MIN-003). A rename is detected on `ReloadProviderAndConfig` when the new config has a server with a different `name` key but identical `transport_type` + `endpoint_url` (or, where defined, identical `serverInfo` fingerprint reported by the prior connection). Two adds with different endpoints are treated as add+remove, not rename. The detection rule is documented; durable identity follow-up tracked in #153.
- **FR-084**: **Synthetic-error turn-abort floor** (resolves MIN-004). After **N consecutive synthetic-deny tool results within a single turn (N=8 default, configurable as `gateway.turn_synthetic_error_floor`)**, the loop MUST abort the turn with a system message `{role: "system", type: "turn_aborted", reason: "synthetic_error_loop"}` and emit audit `turn.aborted_synthetic_loop`. Counter resets per turn.
- **FR-085**: **Empty-string policy values rejected as invalid** (resolves MIN-001). Revision 6 supersedes FR-072: empty string `""` for any policy field is treated identically to invalid-value cases (FR-049). Boot validator emits HIGH audit `agent.config.invalid_policy_value` and applies the same skip-or-abort disposition. The `ResolvePolicy` legacy coercion in `pkg/config/config.go:519-521` is removed alongside the `Mode`/`Visible` deletion.
- **FR-086**: **REST agent-tools endpoint exposes fence visibility** (resolves MAJ-008). `GET /api/v1/agents/{id}/tools` returns per-tool entries with shape `{name, configured_policy, effective_policy, fence_applied, requires_admin_ask}`. When `fence_applied=true`, the SPA MUST render a visual badge ("downgraded to ask: admin-required tool on custom agent") so operators can validate posture from the UI without reading audit logs. FR-043 / preset confirmation dialog mentions fence semantics.
- **FR-087**: **Real-LLM E2E gating** (resolves OBS-005). Tests 54, 55, 56, 57, 58 (real-LLM E2Es) MUST be gated behind a separate CI job (`make test-e2e-llm`) that runs nightly and pre-release, not on every PR. The PR pipeline runs a recorded-fixture mode (provider replay) of the same tests for fast feedback. CI cost/flake risk for per-PR runs is unacceptable.
- **FR-088**: **SIGKILL-recovery LLM-context hygiene** (resolves OBS-003). On session resume after SIGKILL recovery (FR-069), the loop MUST also strip the orphaned `tool_call` entry from the LLM context window when re-prompting (the synthetic `turn_cancelled_restart` system entry is retained in the JSONL transcript for audit/replay, but the next LLM prompt is rebuilt without the orphaned `tool_call` to avoid hallucinated results). Tests assert the rebuilt prompt does not contain the orphaned tool_call.

---

## Success Criteria

- **SC-001**: For any agent with at least one `deny`-policy tool, the LLM provider receives `tools[]` not containing that tool. Verified via `TestAgentLoop_TurnAssembly_FilterApplied` and `TestE2E_DenyPolicy_LLMDoesNotCall`.
- **SC-002**: `len(GET /api/v1/tools) == len(BuiltinRegistry.Describe()) + len(MCP_accepted)` (i.e., MCP collisions are excluded). All 6 previously-affected names appear. [F-20]
- **SC-003**: 100 successive `ReloadProviderAndConfig` calls leave registry counts identical at start and end (asserted in `TestAgentLoop_HotReload_RegistryStable`).
- **SC-004**: Custom agents created with default config emit zero tools with prefix `system.` in `tools[]`.
- **SC-005**: Ask-policy approval round-trip completes within 200 ms of the user's WS approval message at p95 in the integration test (`TestAgentLoop_AskApprove_ResumesWithResult`). The boundary is validated empirically pre-commit (suite run 50×, p99 + 50% headroom).
- **SC-006**: Default `gateway.tool_approval_timeout` is **300 seconds**. Default `gateway.tool_approval_max_pending` is **64** for all variants. Sentinel `0` ("unlimited") emits a startup WARN log + `gateway.startup.guard_disabled` audit; negative values rejected at boot.
- **SC-013**: After Phase A1 lands, `grep -rn "ScopeSystem\|WireSystemTools\|WireAvaAgentTools\|omnipus-system" pkg/ cmd/ internal/` returns zero matches outside `_test.go` files and historical doc strings. (Resolves G-01 / FR-045.)
- **SC-007**: Zero references to `builtinCatalog`, `CatalogAsMapSlice`, `WireAvaAgentTools`, `ToolCompositor.ComposeAndRegister`, `AgentBuiltinToolsCfg.Mode`, or `AgentBuiltinToolsCfg.Visible` exist in the post-redesign codebase (verified by `grep -rn` returning empty).
- **SC-008**: `TestProviderDefs_ShapeUnchanged` confirms byte-identical wire shape for unchanged inputs.
- **SC-009**: Audit log contains every event type listed in the Audit & Observability table when its trigger fires (verified by `TestAudit_Events_Emitted`).
- **SC-010**: Privilege-escalation guard passes: `TestE2E_PrivilegeEscalationGuard_RealLLM` confirms a custom agent's `tools[]` contains zero `system.*` entries even when the user asks for them.
- **SC-011**: Gateway restart during pending approval results in zero double-executions across 100 trials (`TestAgentLoop_AskRestart_CancelsPending` + `TestE2E_AskRestart_RealLLM`).
- **SC-012**: Concurrent reload + turn-assembly race test completes 10 seconds under `-race` with zero detected races (`TestPolicyAtomicPointer_NoTornReads`).

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|------------------|---------------|
| FR-001 | US-2, US-3 | "New builtin tool...", "MCP tool appears..." | tests 1, 2, 4 |
| FR-002 | US-2 | "New builtin tool...", "Previously missing..." | tests 1, 2, 49 |
| FR-003 | US-1 | "Filter excludes deny..." (all variants) | tests 6–13, 26 |
| FR-004 | US-1 | "Filter excludes deny..." | tests 6–13 |
| FR-005 | US-1 | "Operator-global deny...", "Global default-deny..." | tests 9, 10 |
| FR-006 | US-1 | (regression) | test 13 |
| FR-007 | US-4 | "Explicit policy can grant..." | test 28 |
| FR-008 | US-4 | "Custom agent default-denies..." | test 28 |
| FR-009 | US-4 | "Custom agent default-denies...", "Wildcard precedence..." | test 12 |
| FR-010 | US-4 | "Other core agents inherit..." | tests 27, 29 |
| FR-011 | US-5 | "Ask-policy emits WS event..." | tests 34, 35, 36, 37 |
| FR-012 | US-5 | "Multi-call ask batch..." | test 38 |
| FR-013 | US-5 | "Gateway restart cancels..." | tests 40, 55 |
| FR-014 | US-5 | "Unauthenticated approve — 401" | test 45 |
| FR-015 | US-5 | "Non-admin attempting..." | test 44 |
| FR-016 | US-5 | "Approval queue saturation..." | test 42 |
| FR-017 | US-5 | "User cancel resumes..." | test 43 |
| FR-018 | US-5 | "Late approve after timeout..." | test 41 |
| FR-019 | US-3 | "Tool Execute observes post-swap deps..." | test 24 |
| FR-020 | US-3 | "Concurrent reload + turn assembly..." | test 25 |
| FR-021 | (post-condition) | (no BDD — schema-level deletion verified by SC-007 grep) | grep CI check |
| FR-022 | US-4 | "Custom agent default-denies...", "Other core agents inherit..." | tests 17, 18 |
| FR-023 | US-7 | "Corrupt custom-agent config...", "Corrupt core-agent config...", "Inaccessible config file..." | tests 14, 15, 16 |
| FR-024 | (reserved) | — | — |
| FR-025 | (reserved) | — | — |
| FR-026 | (regression) | (regression) | test 52 |
| FR-027 | US-6 | "GET /api/v1/tools..." | test 46 |
| FR-028 | US-6 | "GET /api/v1/agents/{id}/tools..." | test 47 |
| FR-029 | US-6 | "Legacy /api/v1/tools/builtin..." | test 48 |
| FR-030 | US-2 | "Previously missing builtins..." (remove_skill row) | test 49 |
| FR-031 | US-2 | "Search tool names match runtime" | test 50 |
| FR-032 | US-2 | "Previously missing builtins..." | test 49 |
| FR-033 | US-2 | "Tool with unavailable construction-time deps..." | (unit; piggybacks on test 1 fixture variant) |
| FR-034 | (Edge) | "Builtin-vs-MCP collision...", "MCP-vs-MCP collision..." | tests 3, 5 |
| FR-035 | (Edge) | "Stale policy entry..." | test 11 |
| FR-036 | US-2 (regression) | (regression) | existing context_test.go + R3 |
| FR-037 | US-2 (post-condition) | (SC-007) | grep CI check |
| FR-038 | (Audit) | (Audit & Observability) | test 51 |
| FR-039 | (Audit) | (Audit & Observability) | test 51 (metrics counterparts) |
| FR-040 | (Boot) | (Boot Order) | covered by integration boot tests (existing) + tests 26, 32 |
| FR-041 | US-1 | "Mid-turn policy change..." | test 33 |
| FR-042 | US-5 | "Gateway restart cancels..." | tests 40, 55 |
| FR-043 | US-6 | "Preset apply replaces..." | test 53 (E2E) |
| FR-044 | US-6 | (UX-only) | test 53 |
| FR-045 | (system-agent retirement) | "Custom agent default-denies..." + "Ava receives..." | test 17, 18 + SC-013 grep |
| FR-046 | US-5 | "Multi-call ask batch is sequential" | test 38 |
| FR-047 | (Audit) | (Audit & Observability) | test 51 |
| FR-048 | US-5 | "Gateway restart cancels..." | tests 40, 55 |
| FR-049 | US-7 | "Corrupt custom-agent config..." (invalid-value variant) | tests 14–16 |
| FR-050 | US-3 | "Tool Execute observes post-swap deps..." | TestRegistry_ToolDepsContract (new unit, ~T19) |
| FR-051 | (Edge) | (MCP rename — see FR-068 for atomicity) | TestMCPRegistry_ServerRename (new unit) |
| FR-052 | US-5 | "Gateway restart cancels..." | tests 40, 55 |
| FR-053 | (regression) | (regression) | test 52 |
| FR-054 | (Audit) | (Audit & Observability) | test 51 |
| FR-055 | US-2 | "Search tool names match runtime" | test 50 + FR-076 frontend |
| FR-056 | (Phase E) | — | doc cleanup |
| FR-057 | US-7 | (boot warning) | test 14 variant |
| FR-058 | US-7 | (atomic write) | regression suite |
| FR-059 | US-5 | "Non-admin attempting to approve system.*..." | test 44 |
| FR-060 | (Edge) | "Builtin-vs-MCP collision" (reserved-prefix variant) | test 3 + new fixture |
| FR-061 | US-4 | "Custom agent default-denies..." + new "AdminAsk fence" BDD | TestFilterToolsByPolicy_AdminAskFenceOnCustomAgents (new unit) |
| FR-062 | (Boot Order) | (Boot Order step 3a) | TestBoot_ConstructorSeedDispositionMap (new unit) |
| FR-063 | US-7 | "Corrupt core-agent..." (audit-failure variant) | TestBoot_AuditFailureStderrFallback (new unit) |
| FR-064 | US-5 | "Non-admin attempting..." (MCP opt-in variant) | TestRegistry_AllSysagentToolsRequireAdminAsk (new unit) |
| FR-065 | US-5 | "Multi-call ask batch is sequential" + new "Mixed-policy batch" BDD | TestAgentLoop_AskBatch_MixedPolicyShortCircuit (new integration) |
| FR-066 | (Behavioral) | new "Tools[] dedup invariant" BDD | TestAssembly_RejectsDuplicateName (new unit) |
| FR-067 | US-6 | "GET /api/v1/tools..." | test 46 (extended for `category`) |
| FR-068 | (Edge) | new "MCP rename atomicity" BDD | TestAgentLoop_MCPRename_AtomicEviction (new integration) |
| FR-069 | US-5 | new "SIGKILL recovery" BDD | TestSession_LoadRecoversOrphanedToolCall (new unit/integration) |
| FR-070 | US-5 | (Approval State Table) | TestApprovalStateMachine_AllTransitions (new unit) |
| FR-071 | US-1 | "Wildcard precedence — exact match wins" + new tie-break BDD | dataset rows 9–10 + new tie-break row |
| FR-072 | US-7 | "Corrupt custom-agent config..." (empty-coercion variant) | dataset row + audit assertion |
| FR-073 | US-5 | "Gateway restart cancels..." (per-user scoping variant) | new BDD + integration test |
| FR-074 | (Audit) | (Audit & Observability) | test 51 (extended) |
| FR-075 | US-3 | "Concurrent reload + turn assembly..." | test 25 (extended) |
| FR-076 | US-2 | "Search tool names match runtime" | new frontend unit test |
| FR-077 | (Orchestration) | — | Phase B/C process |
| FR-078 | (config) | (none — default value) | TestAgentLoop_AskSaturation_DefaultCap (new integration; default config exercises path) |
| FR-079 | US-1 | "Tool-execution-time policy re-check..." | TestAgentLoop_MidTurnPolicyDeny_AbortsExecution (new integration) |
| FR-080 | (Audit) | (Audit & Observability) | TestAuditArgsHash_Deterministic (new unit) |
| FR-081 | US-5 | "session_state — pending approvals scoped..." | TestWS_SessionStatePayloadSchema (new integration) |
| FR-082 | US-5 | "Ask-policy emits WS event..." (extended) | test 34 (extended) |
| FR-083 | (Edge) | "MCP server rename — atomic eviction..." | TestMCPRegistry_RenameDetection (new unit) |
| FR-084 | (Behavioral) | (Behavioral / synthetic-error floor) | TestAgentLoop_SyntheticErrorFloor_AbortsTurn (new integration) |
| FR-085 | US-7 | "Corrupt custom-agent config..." (empty-string variant) | dataset row + reuse test 14 |
| FR-086 | US-6 | "GET /api/v1/agents/{id}/tools..." (extended) | test 47 (extended) |
| FR-087 | (Phase D) | — | CI job split |
| FR-088 | US-5 | "SIGKILL recovery..." (extended) | TestSession_RebuiltPromptOmitsOrphanedToolCall (new unit) |

**Completeness check**: every FR appears with at least one BDD and one test. Every BDD scenario above is covered.

---

## Implementation Orchestration Plan

This section is binding. The lead orchestrates the work via parallel subagents per CLAUDE.md "Subagent Workflow", drives the 6-agent PR review pipeline + `/grill-code` after implementation, fixes **every** finding regardless of severity, and only then runs the full test suite (including E2E with real LLM responses) to a green state. Implementation does not start until the user has resolved the Ambiguity Warnings below.

### Phase A — Parallel implementation (subagents in parallel where independent)

Per CLAUDE.md, lead spawns implementing subagents in parallel when their work is independent.

**A1 — Backend / agent-core lane** (`backend-lead`, scope `pkg/agent/`, `pkg/tools/` non-security, `pkg/coreagent/`, `pkg/sysagent/`, `pkg/config/`):
- Build `BuiltinRegistry` + `MCPRegistry` types.
- Delete `builtinCatalog` and `CatalogAsMapSlice`. Replace with registry `Describe()`.
- **Retire the system-agent fiction (FR-045)**: change `Scope()` from `ScopeSystem` to `ScopeCore` on every tool in `pkg/sysagent/tools/`; delete the `ScopeSystem` constant; delete the `ScopeSystem` branch in `passesScopeGate` (`pkg/tools/compositor.go:184`); delete `WireSystemTools` and `WireAvaAgentTools` entirely.
- Implement deps `*atomic.Pointer[T]` and per-call deref contract; pointer-receiver invariant test (FR-050).
- Implement per-agent `*atomic.Pointer[ResolvedPolicy]`.
- Move `FilterToolsByPolicy` call site from REST to LLM-call assembly (3 sites in `loop.go`).
- Implement deterministic wildcard resolver (FR-009): trailing-only `.*`, longest-prefix-wins, exact > wildcard, uniform across global + agent layers; reject `*` alone or non-trailing wildcards at config-load time.
- Refactor `ReloadProviderAndConfig` to swap pointers, not rebuild registries.
- Implement Boot Order including invalid-policy-value rejection (FR-049) and unknown-tool warning (FR-057).
- Boot-time config validation (`pkg/config/validate.go`): parse-or-reject; emit `agent.config.corrupt` HIGH audit; selective abort per FR-023 (only agents whose constructor seed contains explicit `system.*` allows — i.e., today, only Ava); skip + continue otherwise. Delete `Mode` and `Visible` from `AgentBuiltinToolsCfg` outright.
- Constructor-time seeding (`pkg/coreagent/`): every newly created custom agent gets `default_policy: allow, policies: {"system.*": "deny"}`. Ava gets the rail plus explicit `system.agent.create/update/delete: allow` and `system.models.list: allow`. Other core agents get the rail only. The implementing subagent MUST cite the exact factory symbol path in its completion report (resolves FR-056 / G-15).
- Add `RequiresAdminAsk()` method to `Tool` interface via the `BaseTool` mixin (default `false`); override to `return true` on every tool in `pkg/sysagent/tools/` (FR-059).
- Reject MCP registrations whose name begins with `system.` at the registry level (FR-060).
- Graceful-shutdown path writes `turn_cancelled_restart` synthetic transcript entries to affected sessions before exit (FR-048).

**A2 — Security lane** (`security-lead`, scope `pkg/audit/`, `pkg/policy/`, security-touching parts of `pkg/tools/`):
- Add audit event types and emission at stale-state deny attempts, approval lifecycle, MCP collisions, boot-time corrupt-config validation.
- Bind approve/deny endpoint to `withAuth`; implement `system.*` admin check.
- Pending-approval saturation guard.
- Privilege-escalation invariants and tests.

**A3 — Gateway/REST/WS lane** (`backend-lead`, scope `pkg/gateway/`):
- `GET /api/v1/tools` (snapshot with `source` field).
- Update `GET /api/v1/agents/{id}/tools` against central registry.
- `GET /api/v1/tools/builtin` → 404.
- `POST /api/v1/tool-approvals/{approval_id}` (`approve`/`deny`/`cancel`), with auth + RBAC.
- WS event types `tool_approval_required` and `session_state` reset.
- Approval registry (in-process, capped, correlation token minted server-side).
- `gateway.tool_approval_timeout` and `gateway.tool_approval_max_pending` config keys.
- Metrics counter/gauge/histogram registration.

**A4 — Frontend lane** (`frontend-lead`, scope `src/`, `packages/ui/`):
- `ToolsAndPermissions.tsx`: switch `fetchBuiltinTools` to `/api/v1/tools` with `source` field handling.
- Preset apply: confirmation dialog + replace semantics.
- Approval modal (renders WS event payload, posts to approve/deny/cancel endpoint).
- `session_state` reset handler clears any stale approval modal on reconnect.
- SPA Embed Pipeline: rebuild and re-sync per CLAUDE.md when frontend changes.

**A5 — QA lane** (`qa-lead`, after A1–A4 produce buildable code):
- Implement every test in the TDD plan (items 1–58).
- Test fixtures under `pkg/config/testdata/agents/` covering datasets above.
- Race tests under `-race` for atomic pointers.
- E2E suite must include real-LLM tests (54, 55, 56, 57, 58) using a tool-capable model per CLAUDE.md (e.g., `anthropic/claude-3.5-haiku` or `z-ai/glm-5-turbo`).

Lanes A1, A2, A3, A4 may run **in parallel**. A5 begins as soon as A1's first commit is buildable; QA tests are written against the implementation as it lands.

### Phase B — Mandatory review pipeline (run all in parallel)

After A5 passes locally, run all six pr-review-toolkit agents and `/grill-code` **in parallel** per CLAUDE.md:

1. `pr-review-toolkit:code-reviewer` — CLAUDE.md compliance, bugs, quality.
2. `pr-review-toolkit:code-simplifier` — clarity and maintainability.
3. `pr-review-toolkit:comment-analyzer` — comment accuracy.
4. `pr-review-toolkit:pr-test-analyzer` — test coverage quality.
5. `pr-review-toolkit:silent-failure-hunter` — silent failures and bad error handling.
6. `pr-review-toolkit:type-design-analyzer` — type/interface design.
7. `/grill-code` — adversarial implementation audit against this spec.

### Phase C — Fix all findings (every severity)

Every finding emitted by Phase B is treated as in-scope, **regardless of severity**. Lead spawns the appropriate implementing subagent (frontend-lead / backend-lead / security-lead / qa-lead) per CLAUDE.md, fixes the issue, and re-runs the failed reviewers + `/grill-code`. Loop until all reviewers and `/grill-code` are clean.

### Phase D — Full test suite green-gate

After Phase C is clean, run **the entire test suite** (no skips, no flake retries hidden):

1. `go test ./... -race -count=1` — all unit + integration tests pass.
2. `go test ./... -tags=e2e -count=1` — E2E suite passes against the embedded SPA + real LLM (per CLAUDE.md "E2E Testing with the Embedded SPA"). Use a tool-capable model. Tests that exercise real LLM responses (54, 56, 57) MUST pass with the model's actual behaviour, not mocked.
3. Frontend type check + lint + unit tests in `src/` and `packages/ui/`.
4. CI grep guard for SC-007 (zero references to deleted symbols).
5. Manual SPA walkthrough using the E2E Test Checklist in CLAUDE.md.

A merge / PR is only opened when D1–D5 are all green.

### Phase E — Documentation cleanup

- ✅ CLAUDE.md updated in revision 4 to retire the system-agent fiction (Status section + Architecture Patterns + Doc/code drift list).
- Update `docs/internal/architecture/AS-IS-architecture.md` to match (remove any "system agent" framing; describe `system.*` as policy-governed builtins).
- Add a "Superseded — kept for historical record" header to `docs/internal/BRD/Omnipus_BRD_AppendixD_System_Agent.md` linking to this spec.
- Mark `docs/internal/plan/central-tool-registry-todo.md` as superseded by this spec.
- Cross-link issue #152 (auto-load skills) and issue #153 (MCP server durable identity).

---

## Ambiguity Warnings

All Phase 1 ambiguities resolved 2026-04-28 by user decision (see Clarifications, revision 3 entry). Locked-in values:

| # | Topic | Decision |
|---|-------|----------|
| Q1 | `gateway.tool_approval_timeout` default | **300 seconds** |
| Q2 | `gateway.tool_approval_max_pending` default | **64** (revision 5+ supersedes the original "unlimited" answer per grill review G-04). Sentinel `0` retains "unlimited" with a startup WARN. |
| Q3 | `.bak` retention | N/A — migrator removed |
| Q4 | Audit handling for filter denies | High-volume `tool.policy.deny` event **dropped** (replaced by metric `omnipus_tool_filter_total`). New `tool.policy.deny.attempted` (WARN, unsampled) for the rare stale-state case where the LLM emits a `tool_call` for a denied tool. |
| Q5 | Schema version label | N/A — migrator removed |
| Q6 | Cancel auth/RBAC | Same as approve/deny: `withAuth` always; admin required for `system.*`. |
| Q7 | MCP server identity for first-server-wins | MCP server **config name**. Known limitation — see issue #153 for the planned improvement (fingerprint or pinned key). |
| Q8 | Migrator seeding rule | N/A — migrator removed |
| Q9 | `default_policy: ask` valid? | **Yes**, accepted; SPA SHOULD warn at the input but does not block. |

No open ambiguities. Spec is implementation-ready.

---

## Evaluation Scenarios (Holdout)

> Post-implementation evaluation only. Not in TDD plan or traceability.

### H1: Naïve denial enforcement
Setup: custom agent `policies: {exec: deny}`, model that often issues `exec`. Action: prompt that would normally trigger `exec`. Expected: model makes no `exec` call; offers alternative. Category: Happy.

### H2: Cold-boot catalog drift gone
Setup: fresh install. Action: SPA → Default Agent → Tools panel. Expected: 6 previously-missing tools present with correct descriptions. Category: Happy.

### H3: Hot-reload tools-don't-disappear
Setup: gateway running; chat view on; turn in progress about to call `serve_workspace`. Action: change provider config from second tab. Expected: turn completes; `serve_workspace` never observed missing. Category: Happy.

### H4: Ask-mode UX
Setup: `policies: {web_fetch: ask}`. Action: prompt to fetch a URL. Expected: approval dialog renders; approve runs the fetch; deny shows acknowledgement. Category: Edge.

### H5: Stale-policy resilience
Setup: agent config refers to `removed_legacy_tool: allow` (not registered). Action: any turn. Expected: no error; turn completes. Category: Error.

### H6: Boot with corrupt custom-agent config
Setup: place an unparseable `agent.json` for a custom agent on disk; place a valid `agent.json` for Ava. Action: boot the gateway. Expected: HIGH audit event for the corrupt file; the custom agent does not appear in the agent list; Ava and the rest of the gateway boot normally; gateway exit code 0. Category: Error.

### H7: Privilege-escalation guard
Setup: custom agent created via SPA with default settings. Action: prompt that asks for `system.config.set`. Expected: `tools[]` has no `system.config.set`; model responds without making the call. Category: Error.

### H8: Concurrent operator approvals
Setup: two admin users connected; one ask call pending. Action: both attempt approve. Expected: first approve resolves; second receives 410. Category: Edge.

### H9: Restart while paused
Setup: pending approval. Action: kill -TERM gateway and restart. Expected: SPA shows reset state; turn does not auto-resume; audit `reason=restart`. Category: Edge.

### H10: Real-LLM denial
Setup: real Anthropic / OpenRouter call. Action: agent with `exec: deny` asked to run a shell command. Expected: model responds without `exec` in the tool array; produces a graceful refusal or alternative. Category: Happy (real LLM path).

---

## Assumptions

- Skills remain prompt-augmentation (not callable tools); auto-load deferred to #152.
- `coreagent.GetPrompt(id) != ""` is a reliable "is core agent" predicate.
- Pre-1.0 codebase: no external consumers, no production users, no on-disk configs in the legacy shape. Therefore no migrator is needed and the legacy `Mode`/`Visible` fields can be deleted outright.
- WebSocket infrastructure is reusable; no transport-level redesign needed.
- `FakeProvider` test double captures `tools[]` for byte-level assertions.
- `pkg/audit/` event sink supports new event types without schema migration.
- Real-LLM E2E tests run in CI against a tool-capable model with a CI-only API key.

---

## Clarifications

### 2026-04-28 (initial)

- Q1 → ordinary catalog entries; default-deny rail on custom agents (option `a`).
- Q2 → one tool registry with two sources (builtins, MCP); skills stay in `SkillsLoader` (no third registry).
- Q3 → atomic-pointer dep swap, registries built once at boot.
- Q4 → keep four presets (Unrestricted / Cautious / Standard / Minimal).
- Q5 → rename catalog to match runtime (`tool_search_tool_bm25/regex`).
- Q6 → drop legacy `Mode`/`Visible` (revision 3 supersedes: deleted outright; no migrator).
- Q7 → confirmed: pause + WS approval + resume or `permission_denied`.
- Q8 → collapse to two endpoints; legacy returns 404.
- Q2.1 → keep existing progressive-disclosure for skills; auto-load filed as #152.

### 2026-04-28 (revision 6 — CRIT/MAJ/MIN/OBS grill findings)

- **CRIT-001**: FR-059 reconciled with FR-064. MCP tools opt in to admin-required only via explicit per-server config (`requires_admin_ask` array); the adapter defaults to `false`. The "MCP cannot opt in" sentence in FR-059 was the contradiction; revision 6 makes the opt-in path explicit.
- **CRIT-002**: max_pending default is **64** everywhere (FR-016, FR-078, SC-006, Q2 Ambiguity row all aligned). Sentinel `0` retains "unlimited" with WARN. Negatives rejected (MIN-005).
- **CRIT-003**: timeout default is **300 seconds** everywhere (US-5 AS-4, BDD timeout scenario, Q1, SC-006 aligned).
- **MAJ-001**: 7 missing BDD scenarios added — admin-ask fence (×3 cases), mixed-policy batch deny, dedup invariant, MCP rename atomicity, SIGKILL recovery, wildcard tie-break, session_state per-user scoping, mid-turn TOCTOU.
- **MAJ-002**: Tool-execution-time policy re-check (FR-079). Closes TOCTOU window between filter and execution.
- **MAJ-003**: FR-046 marked superseded by FR-065 (single combined audit event with `cancelled_tool_call_ids`).
- **MAJ-004**: `args_hash` algorithm specified (FR-080) — SHA-256 over RFC 8785 canonicaljson, lowercase hex; redacted preview for sensitive args.
- **MAJ-005**: `session_state` payload schema specified (FR-081).
- **MAJ-006**: Dedup-violation recovery — drop deterministically by source-tag ordering, audit, continue (FR-066 amended via the new BDD).
- **MAJ-007**: Boot order steps renumbered linearly (3 → constructor-seed map; 4 → validation; 5 → policy maps; 6 → system tools; 7 → MCP enabled; 8 → channels/HTTP).
- **MAJ-008**: Agent-tools endpoint shape (FR-086) returns `{configured_policy, effective_policy, fence_applied}`; SPA renders fence badge.
- **MAJ-009**: Saturation user feedback — system message `saturation_block` appended to session transcript; optional `system_overload` WS event (FR-016 amended).
- **MIN-001**: Empty-string policy values now rejected as invalid (FR-085 supersedes FR-072 coercion).
- **MIN-002**: `gone` removed from state list; described as a late-action HTTP response code.
- **MIN-003**: MCP rename detection criteria specified (FR-083).
- **MIN-004**: Synthetic-error turn-abort floor (FR-084, default N=8).
- **MIN-005**: Negative `tool_approval_max_pending` values rejected at boot.
- **MIN-006**: BDD test 42 setup updated from 32 to 64 (default).
- **MIN-007**: Wildcard `.*` matching semantics noted (matches any non-empty trailing suffix; does not match the prefix alone).
- **OBS-001**: Scope creep noted; spec retained as a single document for ease of cross-reference. Future redesigns may split.
- **OBS-002**: No feature flag added; pre-1.0 acceptable per user direction.
- **OBS-003**: SIGKILL-recovery LLM-context hygiene specified (FR-088).
- **OBS-004**: `expires_at` replaced with `expires_in_ms` to immune against clock skew (FR-082).
- **OBS-005**: Real-LLM E2Es gated behind separate CI job (FR-087).

### 2026-04-28 (revision 5 — H-series grill findings)

- **H-01**: structural fence added (FR-061). On a custom agent, an effective `allow` for any `RequiresAdminAsk` tool is downgraded to `ask` at filter time. Operator typo cannot reach a privileged tool without the human-in-the-loop approval.
- **H-02**: Boot Order step 3a inserted — constructor-seed disposition map computed before validation runs. FR-062.
- **H-03**: Symbols Involved row updated; `Tool` interface now marked "extends" with `RequiresAdminAsk()` and `Category()`.
- **H-04**: MCP-side opt-in for `RequiresAdminAsk` via per-server config; reflection-based test asserts every `pkg/sysagent/tools/` tool returns true. FR-064.
- **H-05**: mixed-policy batch deny short-circuit specified (FR-065): a deny/cancel cancels all subsequent calls regardless of their individual policy.
- **H-06**: `tools[]` deduplication invariant enforced at assembly with HIGH audit + LLM-call failure (FR-066).
- **H-07**: MCP server rename atomicity — eviction + addition under same write lock; per-agent policy recompute follows (FR-068).
- **H-08**: SIGKILL recovery — session-load detects orphaned `tool_call`s and writes synthetic deny on next boot (FR-069).
- **H-09**: admin predicate cited — `User.Role == config.UserRoleAdmin` (`pkg/gateway/rest_users.go:54-56,116`); not theatre (updated in FR-015).
- **H-10**: full Approval State Table added (9 states, action × state precedence matrix). FR-070.
- **H-11**: wildcard tie-break specified (char-count + lexicographic; compiled into resolved-policy snapshot). FR-071, FR-021 sort optimization.
- **H-12**: empty-string policy value coercion now visible via INFO audit (FR-072).
- **H-13**: `session_state` payload scoped to authenticated user (FR-073).
- **H-14**: `args_hash` carried on `granted`/`denied` audit events (FR-074).
- **H-15**: reload-time read consistency — both sides of an SPA write are valid; no new locks (FR-075).
- **H-16**: `Category()` is a new `Tool` method via `BaseTool` mixin; documented categories (FR-067).
- **H-17**: audit-emit failure during boot abort prints structured stderr line before exit (FR-063).
- **H-18**: orchestration plan triages by severity — CRITICAL/MAJOR before merge, MINOR/OBSERVATION at lead's discretion (FR-077).
- **H-19**: dropped Cloud-specific saturation default; single 64 default for now (FR-078).
- **H-20**: dataset rows for invalid policy value + empty-string coercion added.
- **H-21**: wildcard sort cached in resolved-policy snapshot (no per-call O(W log W)) — FR-071.
- **H-22**: frontend canonical-name regression test (FR-076).

### 2026-04-28 (revision 4 — system-agent fiction retired in code; grill-3 findings)

- **System-agent fiction removed from the codebase** (option (a) per user decision):
  - All 35 `system.*` tools change `Scope()` from `ScopeSystem` to `ScopeCore`.
  - The `ScopeSystem` constant is deleted along with its branch in `passesScopeGate`.
  - `WireSystemTools` and `WireAvaAgentTools` are deleted.
  - Per-agent policy is the sole gate, backed by constructor seeds + CI grep guard + audit on stale-state denial attempts.
  - "Retiring the System Agent Fiction" section added to the spec.
  - CLAUDE.md updated in this revision (Status, Architecture Patterns, drift list).
  - `docs/internal/architecture/AS-IS-architecture.md` and `docs/internal/BRD/Omnipus_BRD_AppendixD_System_Agent.md` updated in Phase E.
- **G-02**: complete wildcard semantics specified in FR-009 — trailing-only `.*`, longest-prefix-wins, exact > wildcard, uniform across global and agent layers, `*` alone rejected.
- **G-03**: corrupt-config abort narrowed from "any core agent" to "any agent whose constructor seed contains explicit `system.*` allows" (today: only Ava). Other core agents are skipped like custom agents.
- **G-04**: `tool_approval_max_pending` default is now **finite**: 64 (OSS / desktop), 256 (Cloud variant). Sentinel `0` retains unlimited but emits a WARN + audit on boot.
- **G-06**: admin gate is now a **scope-bound capability** (`RequiresAdminAsk()` on the `Tool` interface) rather than a name-prefix string match. MCP servers cannot register `system.`-prefixed names.
- **G-07**: deny on call K of an N-call ask batch auto-denies K+1..N with `reason: "batch_short_circuit"`.
- **G-08**: `tool.policy.ask.denied` reason enum extended with `saturated` and `batch_short_circuit`.
- **G-09**: shutdown writes `turn_cancelled_restart` transcript entries; paused turns not resumed on next boot.
- **G-10**: invalid policy values rejected at boot with HIGH audit `agent.config.invalid_policy_value`.
- **G-11**: pointer-receiver invariant for tools holding deps; reflection-based test added.
- **G-12**: MCP server rename detected; old-name entries dropped on disconnect; HIGH audit `mcp.server.renamed` (durable identity follow-up tracked in #153).
- **G-13**: default-config saturation path is now reachable (G-04 fix).
- **G-14**: `session_state` is a per-WS-connection one-shot.
- **G-15**: Phase A1 implementing subagent must cite the exact factory symbol for core-agent seeding.
- **G-16**: frontend updated for `tool_search_tool_*` names alongside catalog deletion.
- **G-17**: `TestProviderDefs_ShapeUnchanged` golden file path and update procedure specified.
- **G-18**: `tool.policy.ask.granted/denied` audit events carry `tool_name, agent_id, session_id, turn_id`.
- **G-19/G-20/G-21/G-22**: observation-grade; addressed where cheap (state diagram pending; tie-break authority noted in Phase B; selected E2E tests considered for FakeProvider downgrade in a future cleanup; sync primitive choice left to implementer subject to FR-020 contract).
- **Unasked Q1 / Q7**: `agent.config.unknown_tool_in_policy` warning audit added; concurrent SPA/boot validation contract specified.

### 2026-04-28 (revision 3 — ambiguity resolution + migrator removal)

- **Migrator removed entirely.** Rationale: Omnipus has no production users yet; there are no on-disk agent configs in the legacy shape that need conversion. Concretely:
  - User Story 7 rewritten from "migrator" to "boot-time corrupt-config handling."
  - FR-021 (was migrator) → "delete `Mode`/`Visible` outright."
  - FR-022 (was migrator-seeded `system.*: deny`) → "constructor-seeded on every new custom agent."
  - FR-023 → boot-time validation (HIGH audit; abort on core; skip on custom).
  - FR-024, FR-025 → reserved (no longer applicable).
  - SC-006 → covers the locked-in default values (300s, unlimited).
  - Audit events `agent.config.migrated` and `agent.config.system_seed_applied` removed; `agent.config.corrupt` retained as a generic boot-time validation event.
  - "Migrator conversion" dataset replaced with "Boot-time config validation" dataset.
  - Migrator BDD scenarios replaced with three corrupt-config scenarios (custom skip, core abort, inaccessible file).
  - Boot Order step 3 changed from "config migrator runs" to "boot-time config validation runs."
- **Q1**: tool approval timeout default = **300 seconds**.
- **Q2**: tool approval max pending = **unlimited (no cap)**. Flagged as a multi-tenant DoS vector that operators must configure.
- **Q4**: high-volume `tool.policy.deny` audit event **dropped**; the agent never sees filtered tools, so filter-time denials are not security events. Counts captured by metric `omnipus_tool_filter_total`. New `tool.policy.deny.attempted` (WARN, unsampled) audited only when an LLM emits a `tool_call` for a tool that policy denies (rare; reachable only via stale model state).
- **Q6**: cancel action uses the same auth as approve/deny.
- **Q7**: MCP server identity = config name. Known limitation; follow-up tracked in issue #153.
- **Q9**: `default_policy: ask` is a valid value; SPA SHOULD surface a warning at config-write time but does not block.

### 2026-04-28 (revision 2 — post-grill)

- F-01 → `GlobalPolicies`/`GlobalDefaultPolicy` preserved; FR-005 added; BDD + dataset rows added.
- F-02 → migrator now seeds `system.*: deny` on existing custom agents (FR-022).
- F-03 → ask-mode protocol fully specified (US-5 expanded; FR-011 through FR-018, FR-042).
- F-04 → corrupt-config quarantine + audit + non-zero exit on core (FR-023).
- F-05 → prefix wildcard `system.*` in policy map; exact > wildcard (FR-009).
- F-06 → tool-with-unavailable-deps registers with error-Execute (FR-033).
- F-07 → per-call atomic deref contract codified (FR-019).
- F-08 → correlation token (`approval_id` + `tool_call_id`) on WS event (FR-011).
- F-09 → endpoint bound to `withAuth`, admin required for `system.*` (FR-014, FR-015).
- F-10 → Audit & Observability section added; FR-038, FR-039.
- F-11 → MCP-vs-MCP first-server-wins (FR-034); BDD + dataset added.
- F-12 → policy map under `*atomic.Pointer[ResolvedPolicy]` (FR-020).
- F-13 → migrator: per-file independent, `.bak`, Windows file-lock skip (FR-024, FR-025).
- F-14 → Boot Order section added.
- F-15 → "skills registry" framing dropped; one registry with two sources (builtins, MCP); existing `SkillsLoader` continues unchanged.
- F-16 → tool-with-unavailable-deps pattern (FR-033).
- F-17 → preset apply = replace, with confirmation dialog (FR-043).
- F-18 → dataset rows for `default_policy: ask` and per-tool `ask` over default-deny added.
- F-19 → boot order makes builtin-first synchronous (Boot Order step 2).
- F-20 → SC-002 reworded.
- F-21 → E2E coverage extended for restart + auth + privilege-escalation (tests 53–58).
- F-22 → adopted: one registry, two sources.
- F-23 → noted; four presets retained per Q4 lock-in.
- F-24 → metrics added to Audit & Observability.

### Verified bug claims (against `feature/iframe-preview-tier13`)

- 5 missing catalog entries: confirmed.
- Search tool name mismatch: confirmed.
- `remove_skill` ghost: confirmed.
- `ToolCompositor.ComposeAndRegister` dead code: confirmed.
- Tier13 hot-reload bug: already patched in `loop.go:1756-1757`. Redesign eliminates the class structurally.
- `FilterToolsByPolicy` only used in REST path: confirmed (single call site `pkg/gateway/rest.go:3167`).
- `GlobalPolicies` / `GlobalDefaultPolicy` already implemented: confirmed at `pkg/tools/compositor.go:286-296`.
