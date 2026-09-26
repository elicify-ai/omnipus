Status: Approved

ADR: none — no open design decision (the founder interview settled every design point; the existing [ADR-066 — Context overflow: the sliding window extended mid-turn, tool results emptied with a recall mark, and a per-result cap at the door](../architecture/ADR-066-context-budget-and-tool-result-routing.md) carries a dated 2026-09-26 correction to its §14 item 6 pointing here)

# Feature Specification: Tool iteration limit — one global ceiling, per-agent tightening

**Created**: 2026-09-26
**Issue**: #904
**Input**: founder interview record `docs/internal/specs/tool-iteration-limit-interview.md` (Decisions Log D1–D15, final); code read at `feature/904-tool-iteration-limit` @ `0090f43`.

---

## Overview

Every agent turn is capped at a number of tool steps ("Max tool calls per turn") so a runaway agent cannot loop forever. Today that cap is set in three inconsistent ways: a global value exists in `config.json` but has no screen; each agent can set its own value that *overrides* the global in either direction; and when nothing is set the code silently falls back to a hard-wired 200 (native agents) or 50 (external-CLI preview). The SPA also hard-codes 200 in several places, so what the screen shows is not always what the runtime does.

This feature makes the rule simple and visible: **one global limit** (range 1–1000, shipped default 200) that an admin edits in **Settings → Performance**, and a per-agent value that may only **lower** the limit for that agent. The limit an agent actually runs with is always *the smaller of the two*. One piece of server code computes it, and every screen, API response and system-agent tool shows that server-computed value together with where it came from ("global" or "lowered for this agent"). There is no hidden fallback number anywhere.

Upgrades never silently rewrite saved values: an agent whose stored value is above the global keeps it on disk but is capped and flagged on screen. The one deliberate rewrite is when an admin lowers the global below some agents' own values — a confirm dialog lists each affected agent first, and each change is audited.

## Existing Codebase Context

GitNexus was not available in this container; every row below was read with Read/Grep on this branch. Impact rows are therefore **Inferred** (caller sweep by Grep, no call-graph run) — backend-lead must re-run `impact` where GitNexus is available before GREEN.

### Symbols Involved

| Symbol | Role | Context (as read) |
|--------|------|---------|
| `pkg/config/config.go::AgentDefaults.MaxToolIterations` | modifies | Global value, JSON key `agents.defaults.max_tool_iterations`, carries the `env:"OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS"` tag applied by `env.Parse` inside `loadConfigInternal` / `freshInstallConfig` |
| `pkg/config/defaults.go::defaultAgentsConfig` | reads | Ships `MaxToolIterations: 200` |
| `pkg/config/config.go::AgentConfig.MaxToolIterations` | modifies | Per-agent value, `omitempty`; doc comment says "0 = inherit … (which itself falls back to 200)" |
| `pkg/agent/instance.go::resolveRuntimeLimits` | modifies | Per-agent → global → literal `200`; snapshots into `AgentInstance.MaxIterations` |
| `pkg/agent/loop_run_turn.go` / `loop_run_turn_iterations.go` | reads | Loop bound `< MaxIterations`; hard ceiling `2 * MaxIterations` |
| `pkg/agent/loop.go::toolLimitResponse` | modifies | Text tells the user to "Increase `max_tool_iterations` in config.json" |
| `pkg/agent/external_dispatch.go::DefaultExternalMaxTurns` (=50) and `::prepareRunOptions` | removes / modifies | External run uses `agent.MaxIterations`, falls back to 50 only when ≤0 (never, in practice, because `resolveRuntimeLimits` already produced 200) |
| `pkg/gateway/rest_executor_preview.go::postAgentsExecutorPreview` | modifies | Previews `--max-turns` from the request value, else `DefaultExternalMaxTurns` (50) — diverges from runtime (200) |
| `pkg/gateway/rest_agents.go::buildAgentDefaults`, `::applyAgentOverrides` | modifies | Duplicate of the runtime rule (global else 200; per-agent >0 wins) |
| `pkg/gateway/rest_agents_update.go` (update flow response build and persist step) | modifies | PUT echoes raw `req.MaxToolIterations` onto the response; persists it with no bounds |
| `pkg/gateway/rest_agents_create.go::normalizeVariant` | modifies | Main and Subagent branches never copy `vreq.MaxToolIterations` — the create request value is silently dropped |
| `pkg/gateway/agent_field_rules.go::subagent3pForbiddenUpdateFields`, `::firstForbiddenSubagent3pField` | modifies | `max_tool_iterations` is forbidden on subagent_3p PUT |
| `pkg/agentmutation/policy.go::externalUnsupported` | modifies | Lists `max_tool_iterations` as unsupported for external-CLI agents (drives field descriptors and the system-agent mutation path) |
| `pkg/sysagent/tools/agent.go` (`create_agent` parameter schema and `prepareConfig`), `pkg/sysagent/tools/agent_apply_args.go` (`update_agent` arg application) | modifies | Accept any integer ≥0, "0 = inherit"; no upper bound; no global check |
| `pkg/gateway/rest_performance.go::getPerformance`, `::putPerformance` | extends | Registered with `adminWrap` (`pkg/gateway/rest.go::adminWrap` = `withAuth` → `middleware.RequireNotBypass`; under the single-account model there is **no role check**, only authentication plus the dev-mode-bypass 503 guard). PUT is additionally gated by the single-use step-up token (`requireReAuth`), writes via `safeUpdateConfigJSON`, no registry reload |
| `pkg/gateway/rest_context_settings.go` (PUT handler) | precedent | Calls `triggerReloadAndWait` after a write so snapshotted agent values refresh |
| `pkg/agent/loop_config.go::SwapConfig` | reads | Swaps the config pointer only; agent instances keep their snapshot |
| `pkg/agentstore/state.go::Store.MutateState` | calls | Per-agent config persistence (revisioned per-agent files — not `config.json`) |
| `pkg/audit/security_change.go::EmitSecuritySettingChange` | calls | Actor/resource/old/new audit record (fsync'd) |
| `pkg/config/cli_token_migration.go::migrateCLITokenOutOfUsers` | precedent | Load-time self-heal write reported through `SelfHealWriteHook` |
| `src/components/agents/AgentProfile.tsx` | modifies (shrink only) | `useState(200)`, `useState('200')`, `agent.max_tool_iterations ?? 200` (×2), caption "Default: 200"; control hidden for subagent_3p; sends `max_tool_iterations` on every autosave. Grandfathered at 3,211 lines in `scripts/budgets/files.txt`; currently 3,069 |
| `src/components/agents/CreateAgentWizard.tsx` | modifies | Seeds `max_tool_iterations: 200` for non-3p types |
| `src/components/agents/wizard/Advanced.tsx` | modifies | Caption "Default 200."; no field in the external variant |
| `src/components/agents/wizard/Step1Identity.tsx`, `src/hooks/useCommandPreview.ts` | modifies | Preview request sends `max_tool_iterations ?? 0`, comments cite the "(50)" server default |
| `src/components/settings/PerformanceSection.tsx` | extends | Hosts "Tries per goal" with `useStepUp` gating |
| `docs/internal/architecture/agent-types-field-matrix.md` | modifies | Decisions #1 "excluded for subagent_3p" — superseded by D14 |

### Impact Assessment (Inferred — Grep caller sweep)

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|----------------|------------|----------------|----------------|
| `AgentConfig.MaxToolIterations` semantics | MEDIUM | `resolveRuntimeLimits`, `applyAgentOverrides`, agent PUT persist, sysagent create/update | every agent turn; every Agent API response |
| `AgentDefaults.MaxToolIterations` env tag removal | MEDIUM | `loadConfigInternal`, `freshInstallConfig` (env.Parse) | installs that set the env var (D6 migration covers them) |
| `Agent` response schema (new required fields) | MEDIUM | `buildAgentDefaults`/`applyAgentOverrides` (list/get/create/update), SPA fixtures `src/test/factories.ts` | every SPA agent screen |
| `DefaultExternalMaxTurns` removal | LOW | `prepareRunOptions`, `postAgentsExecutorPreview` | external-CLI runs |
| `putPerformance` (reload + agent lowering) | MEDIUM | Settings → Performance | every running agent instance (reload) |

No HIGH/CRITICAL row found by the sweep; this is not a substitute for a GitNexus `impact` run.

---

## Contract Changes (contract-first — Hard Constraint #8)

All shapes below are added to `contracts/` and regenerated with `scripts/gen-contracts.sh` **before** any Go or TypeScript code (5-step procedure, root `CLAUDE.md` "Contract regeneration"). backend-lead edits and regenerates; the SPA and Go consume only `pkg/api/generated/` / `src/lib/api/generated/`.

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `MaxToolIterationsSource` | new | `contracts/components/schemas/MaxToolIterationsSource.yaml` | `type: string`, `enum: [global, agent]`. `global` = effective value is the global limit (no own value, or own value ignored because above the global); `agent` = the agent's own lower-or-equal value applies. $ref'd, never inlined (mirrors `ContextWindowSource`). |
| `Agent` | changed | `contracts/components/schemas/Agent.yaml` | `max_tool_iterations`: now **the effective value** — `minimum: 1`, `maximum: 1000` (was `minimum: 0`). New **required** `max_tool_iterations_source` ($ref `MaxToolIterationsSource`) and **required** `max_tool_iterations_override_ignored` (boolean; true when the stored own value is above the global — D1). New optional `max_tool_iterations_override` (integer, `minimum: 1`, no maximum — a hand-edited stored value may exceed 1000 and must be shown truthfully): the agent's own stored value, absent when it has none. |
| `AgentUpdateRequest` | changed | `contracts/components/schemas/AgentUpdateRequest.yaml` | `max_tool_iterations`: `integer`, `minimum: 1`, `maximum: 1000`, `nullable: true`. Omitted = unchanged; `null` = clear the own value ("Use global limit", D9); a number = set the own value, refused if above the current global (D10). Allowed on every agent type including subagent_3p (D14). |
| `AgentCreateRequestMain`, `AgentCreateRequestSubagent` | changed | `contracts/components/schemas/AgentCreateRequestMain.yaml`, `…Subagent.yaml` | `max_tool_iterations`: `minimum: 1`, `maximum: 1000` (was `minimum: 0`). Omitted = the agent rides the global. |
| `AgentCreateRequestSubagent3p` | changed | `contracts/components/schemas/AgentCreateRequestSubagent3p.yaml` | **Add** `max_tool_iterations` (`minimum: 1`, `maximum: 1000`, optional) — D14; remove it from the description's "do not exist on this variant" list. |
| `ExecutorCommandPreviewRequest` | changed | `contracts/components/schemas/ExecutorCommandPreviewRequest.yaml` | `max_tool_iterations`: `minimum: 1`, `maximum: 1000`, optional; meaning becomes "the agent's own value being previewed (omit when none)". The server previews the resolver's effective value (min(global, value), or the global when omitted). Description loses the "(50)" fallback text. |
| `PerformanceSettings` | changed | `contracts/components/schemas/PerformanceSettings.yaml` | Add `max_tool_iterations` (integer 1–1000, always present in responses): the global limit in force. Add `max_tool_iterations_saved_state` ($ref `MaxToolIterationsSavedState`, always present) and `max_tool_iterations_saved_raw` (integer, optional; present only for `below_min` / `above_max`, carrying the value found in `config.json`) — D13's Settings warning. Add `max_tool_iterations_lowered_agents` (array of `MaxToolIterationAgentChange`, optional; present only on a PUT response that lowered agents — D11). |
| `MaxToolIterationsSavedState` | new | `contracts/components/schemas/MaxToolIterationsSavedState.yaml` | `enum: [ok, missing, below_min, above_max]`. `missing` = key absent; `below_min` = saved value < 1 (0 included); `above_max` = saved value > 1000. |
| `PerformanceSettingsUpdate` | changed | `contracts/components/schemas/PerformanceSettingsUpdate.yaml` | Add `max_tool_iterations` (integer, `minimum: 1`, `maximum: 1000`). Add `confirmed_lowering` (array of `MaxToolIterationsConfirmedAgent`, optional; absent = empty): the exact agent snapshot (id + old value) the admin saw in the preview and confirmed (D11, D16). Rule: the server recomputes, at write time, the set of agents whose own value is above the new global; the PUT succeeds only if that set equals `confirmed_lowering` **compared as a set keyed by `agent_id` — order-independent** (same ids, and for each id the same old value; array order on either side is irrelevant). A `confirmed_lowering` that names the same `agent_id` twice is malformed → 400 `confirmed_lowering lists agent <id> more than once`. Any difference — an extra agent, a missing agent, or a changed old value, including the case where the field is absent but agents would be lowered — is drift: nothing is written and the PUT answers 409 `MaxToolIterationsLoweringConflict`. Replaces the round-0 boolean `lower_agent_limits` (grill F2). |
| `MaxToolIterationsConfirmedAgent` | new | `contracts/components/schemas/MaxToolIterationsConfirmedAgent.yaml` | `{agent_id: string, old_value: integer}`, both required, `additionalProperties: false`. The new value is implied by the PUT's `max_tool_iterations`. |
| `MaxToolIterationsLoweringConflict` | new | `contracts/components/schemas/MaxToolIterationsLoweringConflict.yaml` | 409 body for D16 drift. Envelope-compatible with `ErrorResponse`: `error` (string, required), `code` (string, required, always `max_tool_iterations_lowering_drift`), `preview` (`MaxToolIterationsLoweringPreview`, required — the fresh list computed at refusal time, so the SPA can re-open the dialog without a second preview call). |
| `MaxToolIterationAgentChange` | new | `contracts/components/schemas/MaxToolIterationAgentChange.yaml` | `{agent_id: string, agent_name: string, old_value: integer, new_value: integer}` all required, `additionalProperties: false`. |
| `MaxToolIterationsLoweringPreview` | new | `contracts/components/schemas/MaxToolIterationsLoweringPreview.yaml` | `{value: integer 1–1000, agents: MaxToolIterationAgentChange[]}` both required; `agents` empty when nothing would change. |
| `GET /performance/max-tool-iterations/preview` | new path | `contracts/openapi.yaml` | `operationId: previewMaxToolIterationsLowering`, query `value` (required integer 1–1000). 200 → `MaxToolIterationsLoweringPreview`; 400 → `ErrorResponse` (missing/out-of-range value); 401; 503 `503BypassActive`. Registered with `adminWrap`, the same gate as `GET /performance`. Read-only; **no step-up token** (the PUT consumes it — interview "Open points"). Tag `Settings`. |
| `PUT /performance` | changed | `contracts/openapi.yaml` | Description gains the limit, the D11 consent rule and the D16 drift rule; add a `409` response with schema `MaxToolIterationsLoweringConflict`. |

Must not collide with: `ContextWindowSource` (separate ladder, untouched); `Agent.timeout_seconds` (untouched); `goal_max_rounds` (separate limit on the same screen, untouched). No WebSocket (`asyncapi.yaml`) change: no new frame carries the limit.

## API and Data

- **Stored data.** The global stays at `agents.defaults.max_tool_iterations` in `config.json`. Each agent's own value stays in its agent-store record (`pkg/agentstore`), key `max_tool_iterations`; "no own value" is the key absent (0 is treated as absent). Nothing new is persisted except the D6 migration marker below.
- **Resolver.** One function in `pkg/config` (placed beside `PlanningConfig.EffectiveGoalMaxRounds`, the precedent) returns, for (global config, agent config): effective value, source, own value (if any), override-ignored flag; and a companion returns the in-force global plus its saved-state (D13). `pkg/agent`, `pkg/gateway` and `pkg/sysagent/tools` all call it; no package re-implements the rule, and no literal other than the single shipped-default constant exists (FR-004).
- **Env migration marker (D6).** The one-time copy writes `agents.defaults.max_tool_iterations_env_imported: true` alongside the copied value, through the load-time self-heal path (`SelfHealWriteHook`, precedent `migrateCLITokenOutOfUsers`). With the marker present the env var is never read again. The marker is config-file-only, not on the wire.
- **Endpoints.** `GET/PUT /api/v1/performance` (extended), `GET /api/v1/performance/max-tool-iterations/preview` (new), `GET/PUT/POST /api/v1/agents…` (fields changed), `POST /api/v1/agents/executor-preview` (semantics changed). All described by the schemas above.
- **Explicit `null` vs omitted on agent PUT (grill F1).** The generated `AgentUpdateRequest.MaxToolIterations` is a `*int` with `omitempty`, so "field omitted" and "field sent as `null`" both decode to `nil`. The handler MUST use the same raw-body peek that `pkg/gateway/rest_agents_update.go` already uses for `context_window_override` (the update flow's `windowPeek`: unmarshal `uf.rawBody` into `map[string]json.RawMessage` and treat a present key whose trimmed value is `null` as "clear"). Omitted = unchanged; `null` = clear the own value; a number = set (subject to the bound and D10). The RED test for the clear case MUST send raw JSON bytes (`{"max_tool_iterations":null}`), never a marshalled generated struct — a marshalled `nil` omits the key and would make an implementation that only checks `!= nil` pass for the wrong reason. The system-agent `update_agent` path does **not** get this for free (grill G2, verified): in `pkg/sysagent/tools/agent_apply_args.go` the `max_tool_iterations` branch hands `raw` straight to `jsonInt`, whose `raw.(float64)` assertion fails for a JSON `null` (`nil`) and returns "max_tool_iterations must be an integer". The fix: inside `if raw, present := args["max_tool_iterations"]; present`, add an explicit `if raw == nil` check **before** calling `jsonInt` that clears the own value (sets it to 0, which the store omits — the same "key removed" outcome as the REST clear) and skips the bound/D10 checks; only a non-nil `raw` falls through to `jsonInt`, the 1–1000 bound and the D10 global check. The map-level `present` check already distinguishes omitted (unchanged) from present. Test row 11 covers this with a literal `nil` arg.
- **D11/D16 write order.** `PUT /performance` with a changed `max_tool_iterations`: (1) decode and bound-check the body; (2) **pre-check** drift against the live agent set *before* consuming the step-up token, so a stale dialog costs the admin no password re-entry — on drift, 409 with the fresh preview; (3) `requireReAuth`; (4) under the config write lock (`configMu`), **before the first write**, run every check: recompute the affected set, compare it to `confirmed_lowering` (set-based), and read each confirmed agent's current revision and own value from the agent store; any mismatch → 409 with the fresh preview and **nothing written** (D16) — there is no retry and no partial skip; (5) still under `configMu`, lower each confirmed agent via `agentstore` `MutateState` using the revision read in step 4, auditing each; (6) write the global and audit it; (7) registry reload.
  - **A failure during step 5 or 6 after at least one agent was written** (an agent-store I/O error, or a revision conflict from a writer that does not take `configMu` — e.g. the system-agent tools, which serialise on the agent loop's lock, not `configMu`): stop; **roll back** every agent already lowered in this request to its old value via `MutateState` with the revision its lowering write returned, auditing each rollback (`audit.EmitSecuritySettingChange`, old = new global value, new = restored old value); leave the global unchanged (it is written only after all agents succeed, step 6 — if step 6 itself fails, all agents are rolled back the same way). Response: a revision conflict → 409 `MaxToolIterationsLoweringConflict` with the fresh preview; an I/O failure → 500 `ErrorResponse`, `code: "max_tool_iterations_lowering_failed"`, `error` naming the failing agent and cause and stating that nothing was changed. A failure on the very first write needs no rollback and answers the same way.
  - **A rollback that itself fails** is never silent: the response is 500 `ErrorResponse`, `code: "max_tool_iterations_rollback_incomplete"`, `error` = `limit not changed; could not restore <agent> (now <new>, was <old>)[, …] — set their limits again on each agent's profile`, listing every agent left at the new value; the global is unchanged; each such agent gets an ERROR log line (agent id, old, current value, cause) and its lowering audit record stands (the rollback audit is written only for successful rollbacks). This state is safe for the ceiling — those agents run at or below what the admin asked — and is visible on their profiles as an ordinary own value. The pre-check discloses nothing the preview endpoint does not already disclose under the same gate.
- **Reload.** A successful PUT that changes the global, and the D11 lowering, end with a registry reload (`triggerReloadAndWait`, precedent `rest_context_settings.go`) so every agent's next turn uses the new limit.

---

## User Stories & Acceptance Criteria

### User Story 1 — Admin sets one global limit in Settings (Priority: P0)

An admin wants to change how many tool steps every agent may take per turn, from one place, without editing each agent or `config.json`. Today there is no screen for it.

**Why this priority**: it is the issue's core; every other story depends on the global existing on screen.

**Independent Test**: with no agent own values, change the global in Settings → Performance and observe every agent's profile and its next turn use the new value.

**Acceptance Scenarios**:

1. **Given** a fresh install, **When** the admin opens Settings → Performance, **Then** "Max tool calls per turn" shows 200 beside "Tries per goal".
2. **Given** the global is 200 and no agent has an own value, **When** the admin saves 350 (after re-typing their password), **Then** the setting shows 350, every agent's profile shows "Global limit (350)", and each agent's next turn stops after at most 350 tool steps.
3. **Given** the admin types 0 or 1001, **When** they try to save, **Then** the save is refused with a message naming the bound ("must be between 1 and 1000") and the stored value is unchanged.
4. **Given** the admin cancels the password prompt, **When** the save is abandoned, **Then** nothing is written and the field returns to the saved value.
5. **Given** the gateway runs with dev-mode bypass on (or the request is unauthenticated), **When** the performance settings or the lowering preview are requested, **Then** the request is refused (503 / 401) exactly like the other Performance settings, and nothing is shown or changed.
6. **Given** an agent turn is already running with limit 200, **When** the admin lowers the global to 100, **Then** that running turn may continue up to 200 tool steps, and the agent's next turn uses 100 (D18).

### User Story 2 — Operator lowers the limit for one agent (Priority: P0)

An operator wants a specific agent (for example a cheap triage worker) to stop sooner than everyone else, and to undo that with one click.

**Why this priority**: the per-agent rule change (lower-only) is the second half of the issue and fixes today's "cannot be cleared" defect.

**Independent Test**: set an own value on one agent, confirm it runs with that value and shows its source; reset it and confirm it rides the global again.

**Acceptance Scenarios**:

1. **Given** the global is 200, **When** the operator sets the agent's limit to 50, **Then** the profile shows "Lowered for this agent: 50 (global limit 200)" and the agent's next turn stops after at most 50 tool steps.
2. **Given** the agent's own value is 50, **When** the operator clicks "Use global limit", **Then** the own value is removed, the profile shows "Global limit (200)", and the next turn uses 200.
3. **Given** the global is 200, **When** the operator tries to save 300 for an agent, **Then** the save is refused with a message naming the global limit (200) and where to raise it, and the stored value is unchanged (D10).
4. **Given** the global is 200, **When** the operator saves exactly 200 for an agent, **Then** it is accepted and shown as the agent's own value (source "agent", effective 200).
5. **Given** the operator types 0, a negative number, a decimal or 1001, **When** they try to save, **Then** it is refused with a message naming the 1–1000 bound.
6. **Given** an agent profile with an unrelated field edited (e.g. its description), **When** the profile autosaves, **Then** the save succeeds without re-sending the limit — even for an agent in the capped-and-flagged state of Story 5.

### User Story 3 — Create an agent with its own limit (Priority: P1)

An operator creating an agent (any type, including an external-CLI worker) wants to set a lower limit up front, or leave it on the global.

**Why this priority**: today the create request silently drops the value — a real bug — but agents can be fixed after creation, so it ranks below P0.

**Independent Test**: create an agent with 40 and one without a value; read both back.

**Acceptance Scenarios**:

1. **Given** the global is 200, **When** an operator creates a Main or Subagent agent with limit 40, **Then** the created agent reads back with own value 40, effective 40, source "agent".
2. **Given** the create form, **When** the operator leaves the limit empty, **Then** the agent is created with no own value and reads back as "Global limit (200)"; the form never pre-fills 200.
3. **Given** the global is 200, **When** an operator creates an agent with 500, **Then** the create is refused with a message naming the global limit, and no agent is created.
4. **Given** an external-CLI worker being created, **When** the operator sets limit 30, **Then** it is accepted and the worker's runs pass 30 as the CLI's turn cap (D14).

### User Story 4 — External-CLI workers follow the same rule (Priority: P1)

An operator running Claude Code / Codex / OpenCode workers wants them bound by the same global limit, with the same per-agent lowering and reset, and wants the command preview to show the number actually used.

**Why this priority**: D4/D14; today the preview shows 50 while runs use 200.

**Independent Test**: with global 200 and no own value, the preview's turn-cap argument and a real run's turn cap are both 200; with own value 30 both are 30.

**Acceptance Scenarios**:

1. **Given** global 200 and a worker with no own value, **When** the operator views its command preview, **Then** the turn-cap argument shows 200, equal to what a real run passes.
2. **Given** a worker with own value 30, **When** the operator views the preview, **Then** it shows 30, and a real run passes 30.
3. **Given** a worker profile, **When** the operator opens its Advanced tab, **Then** the same limit control, source line and "Use global limit" reset appear as for native agents.

### User Story 5 — Upgrades never silently rewrite (Priority: P0)

An operator upgrading an install whose agents were given values above the (default) global wants nothing silently changed and wants to see which agents are affected.

**Why this priority**: issue acceptance #6; a silent rewrite of saved configuration is unacceptable.

**Independent Test**: boot with an agent stored at 500 and global 200; the file still says 500, the agent runs at 200, the profile flags it, the log lists it once.

**Acceptance Scenarios**:

1. **Given** an agent stored at 500 and global 200, **When** the gateway starts, **Then** the agent's stored value remains 500, its effective limit is 200, and exactly one warning log line at startup lists every such agent with its stored value and the global (D19) — one line in total, however many agents are capped.
2. **Given** that agent, **When** the operator opens its profile, **Then** it shows "Own value 500 is above the global limit (200) and has no effect" with a "Use global limit" action.
3. **Given** that agent, **When** the admin later raises the global to 600, **Then** the agent's own 500 now applies (effective 500, source "agent") and the flag disappears.

### User Story 6 — Lowering the global below agents' own values (Priority: P1)

An admin who lowers the global wants to know which agents will be changed before anything happens, and a record afterwards.

**Why this priority**: D11; a deliberate, visible, cancellable bulk change.

**Independent Test**: global 300, agents A=250 and B=100; lower global to 200; the dialog lists only A (250 → 200); confirm; A stored 200, B stays 100; audit has A's change.

**Acceptance Scenarios**:

1. **Given** global 300 and agent A with own value 250, **When** the admin enters 200 and saves, **Then** before any password prompt or write, a confirm dialog lists "A: 250 → 200".
2. **Given** that dialog, **When** the admin cancels, **Then** nothing is written — neither the global nor any agent.
3. **Given** that dialog, **When** the admin confirms (and re-types their password), **Then** A's stored value becomes 200, the global becomes 200, one audit record per changed agent (old → new, actor) and one for the global are written, and a confirmation names the agents actually lowered.
4. **Given** the admin lowers the global and no agent's own value is above it, **When** they save, **Then** no dialog appears and only the global changes.
5. **Given** the global is later raised back to 300, **When** the admin saves, **Then** A stays at 200 (old values are not restored — founder informed, D11).
6. **Given** the dialog lists "A: 250 → 200" and, before the admin confirms, agent B's own value is changed to 220 (or A's to 260), **When** the admin confirms, **Then** nothing is saved — neither the global nor any agent — and the dialog reloads with the new list for a fresh confirmation (D16).
7. **Given** the admin confirmed lowering agents A and B, **When** writing B fails after A was already lowered, **Then** A is restored to its old value, the global is unchanged, the admin sees an error saying nothing was changed, and the restore is audited; if A cannot be restored, the error names A with its current and old value.

### User Story 7 — Configuration outside the rules never causes an outage (Priority: P1)

An operator with an old environment variable, or a hand-edited `config.json`, wants the gateway to start and tell them what it did.

**Why this priority**: D5–D7, D13; never an outage (cf. #908).

**Independent Test**: boot with the env var set to 5000 → saved config holds 1000, log names 5000, later boots ignore the env var; boot with a saved global of 5000 → runs at 1000, file untouched, Settings warns.

**Acceptance Scenarios**:

1. **Given** `OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS=80` set and never imported, **When** the gateway starts, **Then** `config.json` now holds a global of 80 plus the import marker, and one log line says the env var was copied and is no longer used.
2. **Given** that import already happened and the admin later saves 150 in Settings, **When** the gateway restarts with the env var still 80, **Then** the global stays 150 and a log line says the env var is ignored.
3. **Given** the env var is 5000 (or 0) and not yet imported, **When** the gateway starts, **Then** 1000 (or 1) is saved, a warning names the original value, and the gateway starts.
4. **Given** `config.json` holds a global of 5000, **When** the gateway starts, **Then** the effective global is 1000, the file still says 5000, a warning is logged, and Settings → Performance shows a warning naming 5000 and the 1000 in force.
5. **Given** `config.json` holds a global of 0 or no key at all, **When** the gateway starts, **Then** the effective global is 200, the file is not rewritten, a warning is logged, and Settings shows the corresponding warning.
6. **Given** the env var is not a whole number (e.g. `abc`), **When** the gateway starts, **Then** it is not imported, a warning names it, the file is untouched, and the gateway starts (D17).

### User Story 8 — The system agent obeys the same rules (Priority: P1)

An operator asking the system agent in chat to create or change an agent's limit must get the same bounds and the same refusal as the UI — no second way around them (D15).

**Why this priority**: closes a bypass.

**Independent Test**: ask `create_agent` / `update_agent` for 300 with global 200 → refused with the same message; ask for 40 → applied; ask to clear → rides the global.

**Acceptance Scenarios**:

1. **Given** global 200, **When** `update_agent` is called with `max_tool_iterations: 300`, **Then** it fails with a message naming the global limit and nothing changes.
2. **Given** global 200, **When** `create_agent` is called with `max_tool_iterations: 40`, **Then** the new agent has own value 40.
3. **Given** an agent with own value 40, **When** `update_agent` is called with `max_tool_iterations: null`, **Then** the own value is cleared.
4. **Given** any value outside 1–1000 (including 0), **When** either tool is called, **Then** it fails with a message naming the 1–1000 bound.

### User Story 9 — The limit message points to Settings (Priority: P2)

A user whose turn hit the limit wants to be told where to change it — not to edit `config.json`.

**Why this priority**: copy fix; small but user-visible.

**Independent Test**: force a turn to the limit; read the final message.

**Acceptance Scenarios**:

1. **Given** an agent whose turn reaches its limit without a final answer, **When** the turn ends, **Then** the message names Settings → Performance (and the agent's own limit on its profile) and does not mention `config.json`.

### Edge Cases

- Own value equal to the global → accepted; source "agent"; effective unchanged.
- Global lowered to exactly an agent's own value → that agent is **not** in the D11 list (not above).
- Own value stored as 0 or negative (hand-edited) → treated as "no own value"; the agent rides the global (D17).
- Own value stored above 1000 (hand-edited) → capped at the global and flagged (D1); shown truthfully (e.g. 5000) with no upper bound in the response.
- Global saved as a negative number → treated like 0 → 200 in memory, file not rewritten, WARN, state `below_min` (D17).
- Env var not a whole number (e.g. `abc`) → not imported, WARN logged, **no** marker and no file write, gateway starts (D17). The WARN repeats on each start while the variable is set.
- Agent PUT races the D11 lowering → every drift and revision check runs under `configMu` before the first write (API and Data, "D11/D16 write order" step 4); a conflict found there → 409 with the fresh preview, nothing written (D16). A conflict or I/O failure part-way through the writes → the already-lowered agents are rolled back and the global is left unchanged (step 5 failure rule); there is no retry and no partial success.
- Reload fails after the global is written → 500 "written but the reload failed" (precedent `rest_context_settings.go`); on the next successful reload or restart the new value applies.
- A turn already running when the limit changes → keeps the limit in force when it started; the new value applies from the next turn (D18, same rule as `goal_max_rounds`).
- Several agents capped at startup → one WARN line listing all of them (D19).
- Preview and confirm disagree (an agent's own value changed, an agent was created/deleted above the new global, or a D11 lowering from another tab landed first) → 409, nothing written, dialog reloads (D16).
- Hard stop: the loop's internal ceiling (twice the effective limit) scales with the effective value, so at 1000 it is 2000 — no separate setting.

---

## UI Screens and States

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Settings → Performance: "Max tool calls per turn" | `Skeleton` row, same as the section's other controls | n/a (always present) | Inline `FormError` naming the bound for 1–1000 violations; query failure uses the section's existing error state; reload-failure message on 500 | D13 warning banner when `max_tool_iterations_saved_state ≠ ok`, naming the saved raw value (or "missing") and the value in force | `AutoSaveIndicator` "Saved"; after a D11 lowering, a toast "Lowered N agents: A 250 → 200, …" **and** a persistent inline summary under the field (same text) that stays until the admin next edits the field or leaves the page |
| D11 confirm dialog (`ConfirmDialog`) | Preview fetch in flight: Save button shows busy state, no dialog yet | Preview returns no agents → no dialog, save proceeds | Preview fetch failure → inline error, nothing saved; on a 409 drift answer the dialog stays open (or re-opens), replaces its list with the 409's `preview`, shows the notice "The list of affected agents changed — review and confirm again." and requires a fresh Confirm (D16) | n/a | Lists each agent old → new; Confirm / Cancel |
| Agent profile → Advanced tab: extracted `ToolIterationLimitField` (all agent types incl. subagent_3p) | Profile skeleton (existing) | No own value → empty input with placeholder "Global limit (200)" and source line "Using the global limit (200)" | Refusal message from the server shown inline via `FormError` (names global limit or bound); autosave indicator shows error | D1 flag: "Own value 500 is above the global limit (200) and has no effect" + "Use global limit" | Source line "Lowered for this agent: 50 (global limit 200)"; "Use global limit" visible only when an own value exists |
| Create wizard → Advanced (`wizard/Advanced.tsx`, native and external variants) | n/a | Empty input, placeholder "Global limit (N)" from the performance/agent defaults the server returns | Server refusal on create shown by the wizard's existing create-error surface | n/a | Created agent opens with the server-returned source line |
| Command preview (subagent_3p) | Existing preview loading | n/a | Existing preview error | n/a | Turn-cap argument equals the server-resolved effective value |

**Where the create wizard learns the global.** `GET /api/v1/performance` is readable by any authenticated session (single-account model — `adminWrap` has no role check), so the wizard reads `PerformanceSettings.max_tool_iterations` for its placeholder. If that request fails (e.g. 503 under dev-mode bypass) the placeholder reads "Global limit" without a number — the SPA must never print a literal 200.

## User Journey

1. Admin opens **Settings → Performance**, sees "Max tool calls per turn: 200" beside "Tries per goal" (US-1 AS-1).
2. Admin types 150 → on save the SPA first asks the preview endpoint which agents would be lowered (US-6 AS-1). None → password prompt → saved (US-1 AS-2). Some → confirm dialog → Cancel ends (US-6 AS-2) or Confirm → password prompt → saved, toast and inline summary list lowered agents (US-6 AS-3). If the list changed in between, the dialog reloads with the new list and asks again (US-6 AS-6).
3. Operator opens an agent's profile → **Advanced** tab → sees the source line (US-2 AS-1 / US-5 AS-2). Types 50 → autosaves → source line updates. Clicks "Use global limit" → cleared (US-2 AS-2).
4. Operator creates an agent; the Advanced step leaves the limit empty by default (US-3 AS-2).
5. A user in chat hits the limit → the message names Settings (US-9).

## Accessibility and Keyboard

- Both number inputs are real `<input type="number">` via `Input` inside `Field`, each with a visible `Label` and `aria-describedby` pointing at the caption/source line and at any `FormError` (error announced via the `FormError` live region).
- "Use global limit" is a `Button` (variant ghost/secondary per the design system), reachable by Tab directly after the input; focus stays on the input after reset.
- The D11 `ConfirmDialog` traps focus, opens with focus on Cancel (destructive-safe default), Escape cancels, and the agent list is a plain list readable by a screen reader ("A, 250 to 200").
- The D11 result toast uses the existing toast container (`src/components/ui/toast-container.tsx`), which renders success toasts with `role="status"` (implicit `aria-live="polite"`), so it is announced without interrupting. Because toasts auto-dismiss (`src/store/ui.ts` `addToast`, default 4,000 ms), the SPA passes a longer `duration` for this toast (10,000 ms), and the same text is also rendered as the persistent inline summary under the field inside its own `role="status"` region — the list stays readable for as long as the admin needs (grill F4).
- The D16 drift notice inside the dialog is announced via `role="alert"` and focus moves to the dialog's first list item so the changed list is read out.
- The D13 warning is text, not colour alone; uses the design-system warning tone.
- Focus-visible styling is owned by the primitives (`omnipus-design-system` skill) — no local focus styles. Touch targets per the design-system definition.

## Design-System Components

All from `design-system/catalog.json` — no new primitive or composite:
`Field`, `Input`, `Label`, `Button`, `FormError` (domain), `ConfirmDialog` (composite), `AutoSaveIndicator` (domain), `Skeleton`, `Card`. The new `ToolIterationLimitField` is a **domain** component under `src/components/agents/` (not `src/components/ui/`), composed only of catalogued parts, used by both `AgentProfile.tsx` and the wizard's Advanced step. frontend-lead loads `omnipus-design-system` before touching these files.

## Security and User Promises

- The global keeps the **same protection as every other Performance setting** (D8): authenticated, blocked under dev-mode bypass, and step-up re-auth on PUT. Note (verified): under today's single-account model `adminWrap` performs no admin-vs-user role check — "admin" in D8 means the one account plus step-up. The new preview endpoint uses the same `adminWrap` gate and is read-only; it discloses only agent names and limits the same session can already read via `GET /api/v1/agents`.
- D11 writes other agents' records on the admin's behalf → each change is audited with actor, agent, old and new value (`audit.EmitSecuritySettingChange`), plus one record for the global.
- The system-agent tools get the same bounds and refuse-above-global rule (D15); `max_tool_iterations` stays agent-writable (it is not added to `agentmutation` `operatorOnly`) — an agent may lower another agent but can never exceed the admin-set global.
- No tool-policy, approval or shell-rule surface changes; `docs/security.md` / `docs/tools.md` promises are unaffected.
- Focus-area packages: `pkg/audit` (new call sites only) and gateway auth (`adminWrap`/`requireReAuth` reused, not changed) → **security-lead on-demand review** of the preview endpoint and the D11 write path.

---

## Behavioral Contract

Primary flows:
- When an agent turn starts, the system caps its tool steps at the smaller of the global limit and the agent's own value (or the global when there is no own value).
- When any agent is read through the API, the system returns the effective limit, where it comes from, the agent's own value if any, and whether that own value is being ignored.
- When the admin saves a new global, the system persists it, reloads agents, and every agent's next turn uses the new effective limit.
- When the admin lowers the global below agents' own values and confirms, the system lowers exactly those agents' own values to the new global and audits each change.
- When an operator clears an agent's own value, the agent rides the global.

Error flows:
- When a value outside 1–1000 is submitted anywhere (Settings, agent PUT/create, executor preview, system-agent tools), the system refuses it with a message naming the bound.
- When a per-agent value above the current global is submitted, the system refuses it with a message naming the global limit.
- When a global lowering would change a set of agents (or old values) different from the set the admin confirmed — including no confirmation at all — the system refuses with a conflict, changes nothing, and returns the current list (D16).
- When a write fails part-way through a confirmed lowering, the system restores the agents it already lowered, leaves the global unchanged and says nothing was changed; if a restore fails, it names every agent left at the new value (FR-021).
- When the reload after a global write fails, the system reports that the value was written but not yet applied.

Boundary conditions:
- When a stored own value is above the global (upgrade/hand edit), the system caps it at the global, keeps the stored value, flags it, and logs it once at startup.
- When a turn is already running and the limit changes, that turn keeps the limit in force when it started; the next turn uses the new limit (D18).
- When the saved global is missing, below 1 or above 1000, the system corrects it in memory only (missing/below 1 → 200; above 1000 → 1000), warns in the log and in Settings, and starts.
- When the old env var is present and not yet imported, the system copies it once (clamped to 1–1000) and ignores it afterwards.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not let a per-agent value raise an agent above the global, by any path (REST, SPA, system-agent tools, hand-edited config), because the global is a ceiling (Requirement 2).
- The system must not silently rewrite any stored value on upgrade or startup (D1, D13); the only rewrites are the one-time env import (D6) and the confirmed D11 lowering.
- The system must not contain a literal fallback for the limit other than the one shipped-default constant, and the SPA must not contain any literal default at all (Requirement 3).
- The SPA must not compute the effective value or the source; it renders the server's fields.
- The system must not restore agents' old values when the global is raised after a D11 lowering (D11).
- The system must not refuse to start because of any limit value (D7, D13).
- The system must not read `OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS` after the import marker is set (D5).
- The system must not add a per-goal, per-session or per-channel limit layer — two layers only.

### Machine-Verifiable Constraints

**Error codes / messages** (all bodies `ErrorResponse`):
- Global out of range on `PUT /api/v1/performance` → **400**, message `max_tool_iterations must be between 1 and 1000`.
- Lowering drift (D16) → **409** `MaxToolIterationsLoweringConflict`, `code: "max_tool_iterations_lowering_drift"`, `error: "the agents affected by lowering the limit to <N> changed since the preview; review the updated list and confirm again"`, `preview` = the fresh list. No file or agent record changes.
- Preview `value` missing/out of range → **400**, message `value must be between 1 and 1000`.
- Agent PUT/create out of range → **400**, `max_tool_iterations must be between 1 and 1000`.
- Agent PUT/create above global → **400**, `max_tool_iterations <N> is above the global limit (<G>); lower it, or raise the global limit in Settings → Performance`.
- System-agent tools: the same two texts, returned as the tool's `INVALID_INPUT` error result.
- Dev-mode bypass on → **503** `503BypassActive` (existing `RequireNotBypass`); unauthenticated → **401**; PUT without a step-up token → **403** (existing `requireReAuth` text).

**Tool-limit message** (`toolLimitResponse`): exactly `I've reached this agent's limit of tool steps for one turn without a final response. An admin can raise the limit in Settings → Performance ("Max tool calls per turn"), and each agent's own lower limit is on its profile's Advanced tab.` — must not contain `config.json`.

**Scope boundaries**: range 1–1000 inclusive for every writable field; the effective value in every response is within 1–1000; `max_tool_iterations_override` may exceed 1000 only when stored that way by hand.

## Integration Boundaries

### Gateway ↔ SPA (REST)
- **Data in / out**: Agent limit fields, Performance global, preview list.
- **Contract**: the schemas in Contract Changes; generated types only.
- **On failure**: 400/403/409/500 as above; SPA shows inline errors, never a false "saved"; on 409 drift the dialog reloads from the body's `preview`; the 500 codes above are shown as their `error` text.
- **409 consumption (grill G3).** The SPA's generic `ApiError.fromResponse` (`src/lib/api-error.ts`) keeps a non-2xx body only as an opaque string, so the `preview` is unreachable through it. frontend-lead adds, in the Performance API module, a `MaxToolIterationsLoweringConflictError extends ApiError` carrying a typed `preview`, an `isMaxToolIterationsLoweringConflict(err)` guard, and a from-response function that reads the 409 body once and re-parses it against the **generated** Zod schema for `MaxToolIterationsLoweringConflict` (from `src/lib/api/generated/`) — the same pattern as `src/lib/api/library.ts::LibraryVersionConflictError` / `::isLibraryVersionConflict` / `libraryConflictErrorFromResponse`. A 409 whose body does not match still surfaces as a plain 409 `ApiError` (shown as an error, dialog not reloaded). `PerformanceSection`'s confirm-save branches on the guard to re-open the dialog from `.preview`.
- **Development**: real gateway in vitest via MSW-free handler tests on the Go side; SPA tests use generated types and fixtures from `src/test/factories.ts` updated with the new required fields.

### Gateway ↔ agent store / `config.json`
- **Data**: global in `config.json` (`safeUpdateConfigJSON`); own values in `pkg/agentstore` records (`MutateState`, revision-checked).
- **On failure**: all checks run before the first write (409, nothing written — D16). A failure after writes began rolls back the already-lowered agents (each rollback audited), leaves the global unchanged, and answers 409 (revision conflict, with fresh preview) or 500 `max_tool_iterations_lowering_failed` (I/O). If a rollback fails, 500 `max_tool_iterations_rollback_incomplete` names every agent left at the new value, with an ERROR log line each — never silent. Full rule: API and Data, "D11/D16 write order".
- **Development**: real temp-dir store in Go integration tests.

### Agent loop ↔ external CLI
- **Data out**: the effective limit as the CLI's turn cap (`RunOptions.MaxTurns`).
- **Development**: argv inspection via the executor-preview endpoint and the dispatch unit seam; no real CLI needed.

### Config loader ↔ environment
- **Data in**: `OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS` (import only).
- **Development**: `t.Setenv` in Go unit tests with a temp config path.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|------------------|------------------------|---------------------|
| 1 | D8 says "admin", but the code has no admin role (single-account model; `adminWrap` = auth + bypass guard). | Add a new role check to the preview endpoint. | Spec decision: reuse `adminWrap` exactly like the rest of `/performance`; a role check arrives with multi-user support, not here. Founder informed (report of 2026-09-26). |
| 2 | Whether a later reload (not a restart) that creates newly capped agents logs the D19 line again. | Log on every reload. | Spec decision: the D19 line is emitted at startup only; later cases are visible on the profile flag. Residual, not a founder decision. |

---

## BDD Scenarios

### Feature: Tool iteration limit

#### Scenario: Fresh install shows the shipped global in Settings
**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a fresh install with no `agents.defaults.max_tool_iterations` override beyond the shipped default
- **When** the admin opens Settings → Performance
- **Then** "Max tool calls per turn" shows 200
- **And** `GET /api/v1/performance` returns `max_tool_iterations: 200` and `max_tool_iterations_saved_state: "ok"`

#### Scenario: Admin raises the global and every non-overriding agent follows
**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path
- **Given** global 200 and agents A and B with no own value
- **When** the admin PUTs `max_tool_iterations: 350` with a valid step-up token
- **Then** the response carries `max_tool_iterations: 350`
- **And** `GET /api/v1/agents/A` returns `max_tool_iterations: 350`, `max_tool_iterations_source: "global"`, no `max_tool_iterations_override`, `max_tool_iterations_override_ignored: false`
- **And** A's next turn ends with the tool-limit message after exactly 350 tool rounds

#### Scenario: Global out of range is refused
**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Error Path
- **Given** global 200
- **When** the admin PUTs `max_tool_iterations` with a value from Dataset "Global bounds" marked invalid
- **Then** the response is 400 with `max_tool_iterations must be between 1 and 1000`
- **And** `config.json` still holds 200

#### Scenario: Cancelling the password prompt writes nothing
**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Alternate Path
- **Given** the admin typed 150 in the field
- **When** they cancel the re-auth dialog
- **Then** no PUT is sent and the field shows 200 again

#### Scenario: Preview is blocked under dev-mode bypass
**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Error Path
- **Given** the gateway runs with `gateway.dev_mode_bypass` on
- **When** a client GETs `/api/v1/performance/max-tool-iterations/preview?value=100`
- **Then** the response is 503 and no agent data is returned

#### Scenario: PUT without step-up token is refused
**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Error Path
- **Given** an admin session without a fresh re-auth token
- **When** they PUT `max_tool_iterations: 150`
- **Then** the response is 403 and the global is unchanged

#### Scenario: Operator lowers one agent
**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path
- **Given** global 200 and agent A with no own value
- **When** the operator PUTs `/api/v1/agents/A` with `max_tool_iterations: 50`
- **Then** the response has `max_tool_iterations: 50`, `max_tool_iterations_source: "agent"`, `max_tool_iterations_override: 50`
- **And** the profile shows "Lowered for this agent: 50 (global limit 200)"
- **And** A's next turn stops after exactly 50 tool rounds

#### Scenario: Use global limit clears the own value
**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path
- **Given** agent A with own value 50 and global 200
- **When** the operator clicks "Use global limit", which sends the raw body `{"max_tool_iterations":null}`
- **Then** A's stored record has no `max_tool_iterations` key
- **And** the response has `max_tool_iterations: 200`, `source: "global"`, no override

#### Scenario: Per-agent value above the global is refused
**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Error Path
- **Given** global 200 and agent A with no own value
- **When** the operator PUTs `max_tool_iterations: 300`
- **Then** the response is 400 with `max_tool_iterations 300 is above the global limit (200); lower it, or raise the global limit in Settings → Performance`
- **And** A's stored record is unchanged

#### Scenario: Per-agent value equal to the global is accepted
**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Edge Case
- **Given** global 200
- **When** the operator PUTs `max_tool_iterations: 200` for A
- **Then** the response has `max_tool_iterations: 200`, `source: "agent"`, `override: 200`

#### Scenario: Per-agent value out of range is refused
**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Error Path
- **Given** global 1000
- **When** the operator PUTs a value from Dataset "Per-agent bounds" marked invalid
- **Then** the response is 400 naming the 1–1000 bound (schema-invalid types such as 2.5 are 400 from request validation)

#### Scenario: Unrelated autosave does not resend the limit
**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Edge Case
- **Given** agent A stored at 500, global 200 (capped-and-flagged)
- **When** the operator edits A's description and the profile autosaves
- **Then** the PUT body has no `max_tool_iterations` key and the save succeeds
- **And** A's stored limit is still 500

#### Scenario: Create keeps the requested own value
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path
- **Given** global 200
- **When** the operator creates a Main agent with `max_tool_iterations: 40`
- **Then** the 201 response and a later GET both show `override: 40`, effective 40, source "agent"

#### Scenario: Create without a value rides the global
**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path
- **Given** global 200
- **When** the operator creates a Subagent through the wizard leaving the limit empty
- **Then** the request body has no `max_tool_iterations` key
- **And** the created agent has no own value, effective 200, source "global"

#### Scenario: Create above the global is refused
**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Error Path
- **Given** global 200
- **When** the operator creates an agent with `max_tool_iterations: 500`
- **Then** the response is 400 naming the global limit (200)
- **And** no agent record exists afterwards

#### Scenario: External-CLI worker created with its own limit
**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Happy Path
- **Given** global 200
- **When** the operator creates a subagent_3p worker with `max_tool_iterations: 30`
- **Then** it is accepted (201) with `override: 30`, effective 30
- **And** a real dispatch of that worker passes a turn cap of 30

#### Scenario: Worker preview equals runtime with no own value
**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path
- **Given** global 200 and a claude-code worker with no own value
- **When** the SPA posts the executor preview without `max_tool_iterations`
- **Then** the returned argv's turn-cap argument is 200
- **And** a real dispatch of the same worker passes 200

#### Scenario: Worker preview equals runtime with own value
**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Happy Path
- **Given** global 200 and a worker with own value 30
- **When** the SPA posts the executor preview with `max_tool_iterations: 30`
- **Then** the turn-cap argument is 30

#### Scenario: Worker PUT of the limit is accepted
**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Happy Path
- **Given** a subagent_3p worker and global 200
- **When** the operator PUTs `max_tool_iterations: 60`
- **Then** the response is 200 with `override: 60` (no "subagent_3p agents do not support" error)
- **And** its profile's Advanced tab shows the limit control and the "Use global limit" reset

#### Scenario: Upgrade keeps a stored value above the global
**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Edge Case
- **Given** agent A stored at 500, agent B stored at 300, agent C stored at 100, and global 200 on disk
- **When** the gateway starts
- **Then** A's stored record still says 500 and B's still says 300
- **And** A's and B's effective limits are 200; C's is 100
- **And** exactly one startup WARN log line is emitted for capped agents, naming A (stored 500) and B (stored 300) and the global 200, and not naming C (D19)

#### Scenario: Profile flags an ignored own value
**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Edge Case
- **Given** agent A stored at 500 and global 200
- **When** the operator opens A's profile Advanced tab
- **Then** the API returned `override: 500`, `override_ignored: true`, `source: "global"`, effective 200
- **And** the profile shows "Own value 500 is above the global limit (200) and has no effect" and a "Use global limit" button

#### Scenario: Raising the global activates a previously ignored own value
**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Alternate Path
- **Given** A stored at 500 and global 200
- **When** the admin raises the global to 600
- **Then** A's response shows effective 500, `source: "agent"`, `override_ignored: false`

#### Scenario: Lowering preview lists only agents above the new value
**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path
- **Given** global 300, A own 250, B own 100, C own 200, D no own value
- **When** the admin GETs the preview with `value=200`
- **Then** `agents` is exactly `[{A, 250 → 200}]` (C equal, B lower, D none — all excluded)
- **And** nothing is written

#### Scenario: Cancel in the confirm dialog writes nothing
**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** the confirm dialog lists A 250 → 200
- **When** the admin clicks Cancel
- **Then** no PUT is sent; global 300 and A 250 remain

#### Scenario: Confirmed lowering rewrites and audits
**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Happy Path
- **Given** global 300, A own 250, B own 100
- **When** the admin PUTs `max_tool_iterations: 200, confirmed_lowering: [{agent_id: A, old_value: 250}]` with a step-up token
- **Then** A's stored value is 200, B's is 100, the global is 200
- **And** the audit log has one `security_setting_change` record for A (old 250, new 200, actor = the admin) and one for the global (old 300, new 200)
- **And** the response's `max_tool_iterations_lowered_agents` is exactly `[{A, 250, 200}]`

#### Scenario: Lowering without confirmation is refused
**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Error Path
- **Given** global 300 and A own 250
- **When** an API client PUTs `max_tool_iterations: 200` without `confirmed_lowering`
- **Then** the response is 409 with `code: "max_tool_iterations_lowering_drift"` and `preview.agents` = `[{A, 250 → 200}]`
- **And** nothing is written and the step-up token is not consumed

#### Scenario: Drift between preview and confirm refuses and returns the fresh list
**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Error Path
- **Given** global 300, A own 250, B own 150; the admin previewed value 200 and saw only `[{A, 250 → 200}]`
- **And** B's own value was then changed to 220
- **When** the admin PUTs `max_tool_iterations: 200, confirmed_lowering: [{A, 250}]`
- **Then** the response is 409 with `preview.agents` = `[{A, 250 → 200}, {B, 220 → 200}]`
- **And** global 300, A 250 and B 220 are all unchanged, and no audit record is written
- **And** the SPA dialog shows the new two-agent list with the "list changed" notice and requires a fresh Confirm

#### Scenario: Changed old value is drift
**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Edge Case
- **Given** global 300, A own 250; the admin previewed value 200 and confirmed `[{A, 250}]`
- **And** A's own value was then changed to 260
- **When** the PUT arrives
- **Then** the response is 409 with `preview.agents` = `[{A, 260 → 200}]` and A is still 260

#### Scenario: Lowering with no affected agents shows no dialog
**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Alternate Path
- **Given** global 300 and every own value ≤ 150
- **When** the admin saves 150
- **Then** no confirm dialog is shown and only the global changes

#### Scenario: Raising again does not restore lowered values
**Traces to**: User Story 6, Acceptance Scenario 5
**Category**: Edge Case
- **Given** A was lowered from 250 to 200
- **When** the admin raises the global to 300
- **Then** A's stored value is still 200

#### Scenario: Env var imported once
**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path
- **Given** `config.json` with global 200, no import marker, and the env var set to 80
- **When** the gateway starts
- **Then** `config.json` holds global 80 and `max_tool_iterations_env_imported: true`
- **And** one log line says the env var was copied and is no longer used

#### Scenario: Env var ignored after import
**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Alternate Path
- **Given** the import marker is set, the global is 150, and the env var is 80
- **When** the gateway starts
- **Then** the effective global is 150 and a log line says the env var is ignored

#### Scenario: Out-of-range env var imported at the nearest bound
**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Edge Case
- **Given** no import marker and the env var set per Dataset "Env import"
- **When** the gateway starts
- **Then** the saved global equals the dataset's expected value and a WARN names the original value
- **And** the gateway is serving

#### Scenario: Saved global out of range is corrected in memory only
**Traces to**: User Story 7, Acceptance Scenarios 4 and 5
**Category**: Edge Case
- **Given** `config.json` holds a global per Dataset "Saved global"
- **When** the gateway starts
- **Then** the effective global and `max_tool_iterations_saved_state` equal the dataset's expected values
- **And** the file bytes for that key are unchanged, a WARN is logged, and Settings shows the warning

#### Scenario: System agent cannot exceed the global
**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Error Path
- **Given** global 200
- **When** `update_agent` is called with `max_tool_iterations: 300`
- **Then** the tool returns an `INVALID_INPUT` error naming the global limit (200) and nothing changes

#### Scenario: System agent creates with an own value
**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Happy Path
- **Given** global 200
- **When** `create_agent` is called with `max_tool_iterations: 40`
- **Then** the created agent has own value 40

#### Scenario: System agent clears an own value
**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Happy Path
- **Given** A with own value 40
- **When** `update_agent` is called with `max_tool_iterations: null`
- **Then** A has no own value

#### Scenario: System agent out of range
**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Error Path
- **Given** global 1000
- **When** `create_agent` is called with `max_tool_iterations: 0`
- **Then** the tool returns an error naming the 1–1000 bound

#### Scenario: Reordered confirmation is not drift
**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Edge Case
- **Given** global 300, A own 250, B own 280; the preview for 200 listed `[{A, 250}, {B, 280}]`
- **When** the admin PUTs `max_tool_iterations: 200, confirmed_lowering: [{B, 280}, {A, 250}]`
- **Then** the response is 200 and A, B and the global are all 200 (Dataset "Confirmed lowering", row 2)

#### Scenario: Mid-write failure rolls back already-lowered agents
**Traces to**: User Story 6, Acceptance Scenario 7
**Category**: Error Path
- **Given** global 300, A own 250, B own 280, a matching confirmation for 200, and an agent store that fails the write of B with an I/O error after A's write succeeded
- **When** the admin PUTs the confirmed lowering
- **Then** the response is 500 with `code: "max_tool_iterations_lowering_failed"` and a message stating nothing was changed
- **And** A's stored value is 250 again, B's is 280, the global is 300
- **And** the audit log holds A's lowering record and A's rollback record (250 restored), and nothing for the global

#### Scenario: Failed rollback is reported, not silent
**Traces to**: User Story 6, Acceptance Scenario 7
**Category**: Error Path
- **Given** the same setup, and the store also fails the rollback write of A
- **When** the admin PUTs the confirmed lowering
- **Then** the response is 500 with `code: "max_tool_iterations_rollback_incomplete"` naming A (now 200, was 250)
- **And** the global is 300, A's stored value is 200, and one ERROR log line names A with old 250 and current 200

#### Scenario: Running turn keeps the limit it started with
**Traces to**: User Story 1, Acceptance Scenario 6
**Category**: Edge Case
- **Given** global 200, agent A with no own value, and a turn of A in progress at tool step 150
- **When** the admin lowers the global to 100 (no agent affected, so no dialog)
- **Then** the in-progress turn is not stopped at step 100 and may continue up to 200 steps
- **And** A's next turn stops after exactly 100 tool steps (D18)

#### Scenario: Omitted field leaves the own value unchanged
**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Alternate Path
- **Given** agent A with own value 50
- **When** the operator PUTs the raw body `{"description":"x"}` (no `max_tool_iterations` key)
- **Then** A's own value is still 50

#### Scenario: Non-numeric env var is not imported
**Traces to**: User Story 7, Acceptance Scenario 6
**Category**: Edge Case
- **Given** no import marker, global 200 in `config.json`, and the env var set to `abc`
- **When** the gateway starts
- **Then** the saved global is still 200, no marker is written, the file bytes are unchanged, a WARN names the value, and the gateway is serving (D17)

#### Scenario: Tool-limit message points to Settings
**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path
- **Given** an agent with effective limit 2 and a model that always calls a tool
- **When** the turn reaches the limit
- **Then** the final assistant message equals the exact text in Machine-Verifiable Constraints and does not contain `config.json`

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------------|--------------------------------|----------------------------------------------|
| Unit (Go) | resolver in `pkg/config`; env import; saved-global correction; sysagent arg validation; field-rule tables | The rule in isolation, every boundary |
| Integration (Go) | `pkg/gateway` handlers (agents create/get/update, performance GET/PUT/preview, executor preview) against a temp config + agent store; `pkg/agent` loop with a scripted provider | Wire shapes, persistence, reload, audit, loop cap |
| Component (vitest) | `ToolIterationLimitField`, `PerformanceSection` limit control + D11 dialog, wizard Advanced | Rendering of server fields, no literals, request bodies |
| E2E (Playwright) | Settings → Performance change + agent profile source line; D11 dialog | Reachability through the real UI |

### Test Implementation Order

| Order | Test Name (placeholder — qa-lead names the real test in RED) | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|-------------------------|--------------|
| 1 | TestResolveMaxToolIterations_Table | Unit | Per-agent above/equal/below; Upgrade keeps stored value; Raising activates | Dataset "Resolver" |
| 2 | TestEffectiveGlobal_SavedStates | Unit | Saved global out of range | Dataset "Saved global" |
| 3 | TestEnvImport_OnceAndClamp | Unit | Env var imported once; ignored after; out-of-range | Dataset "Env import" |
| 4 | TestNoHiddenLiteral_Guard | Unit/guard | (FR-004) | Grep guard: no `200`/`50` fallback for the limit outside the shipped-default constant; no `DefaultExternalMaxTurns` |
| 5 | TestPerformancePut_MaxToolIterations_* | Integration | Admin raises; out of range; no step-up; lowering without consent; confirmed lowering | Includes reload and audit assertions |
| 6 | TestPerformancePreview_* | Integration | Preview lists only above; blocked under bypass (503); bad value 400 | |
| 7 | TestAgentUpdate_MaxToolIterations_* | Integration | Lower one; clear; above global; equal; out of range; worker PUT accepted | |
| 8 | TestAgentCreate_MaxToolIterations_* | Integration | Create keeps value; rides global; above global; worker create | Guards the `normalizeVariant` drop |
| 9 | TestAgentResponses_UseResolver | Integration | Profile flags ignored own value | list/get/create/update all return the same four fields |
| 10 | TestExecutorPreview_MatchesDispatch | Integration | Worker preview (both) | Preview argv turn cap == dispatch `RunOptions.MaxTurns` |
| 11 | TestSysagentAgentTools_MaxToolIterations | Integration | System agent (four) | Includes `update_agent` with a literal `nil` arg for `max_tool_iterations` → cleared, no error (G2) |
| 12 | TestLoop_StopsAtEffectiveLimit / TestToolLimitResponse_Text | Integration | Admin raises (loop leg); Lower one (loop leg); Tool-limit message | Scripted provider |
| 13 | TestUpgradeBootLog_ListsCappedAgents | Integration | Upgrade keeps stored value | One WARN line for A and B, not C (D19) |
| 13a | TestPerformancePut_LoweringDrift_* | Integration | Lowering without confirmation; Drift between preview and confirm; Changed old value is drift; Reordered confirmation is not drift | Dataset "Confirmed lowering"; 409 body, nothing written, token not consumed on pre-check drift |
| 13e | TestPerformancePut_LoweringRollback_* | Integration | Mid-write failure rolls back…; Failed rollback is reported… | Fault-injecting agent-store seam; asserts stored values, audit and ERROR log |
| 13f | maxToolIterationsLoweringConflict.test.ts | Component | Drift between preview and confirm (SPA leg) | Typed error class + guard; a non-matching 409 body stays a plain ApiError |
| 13b | TestAgentUpdate_MaxToolIterations_NullVsOmitted | Integration | Use global limit clears…; Omitted field leaves… | Raw JSON bodies only (F1) |
| 13c | TestLoop_RunningTurnKeepsStartLimit | Integration | Running turn keeps the limit it started with | Scripted provider; reload mid-turn |
| 13d | PerformanceSection.maxToolIterations drift/toast tests | Component | Drift… (SPA leg); Confirmed lowering (toast + inline summary roles) | |
| 14 | ToolIterationLimitField.test.tsx | Component | Lower one (UI); Use global limit; Profile flags; Unrelated autosave | Asserts no literal 200 rendered without a server value |
| 15 | PerformanceSection.maxToolIterations.test.tsx | Component | Fresh install shows; Cancel re-auth; Cancel dialog; No dialog when none affected; D13 warning | |
| 16 | wizard Advanced / CreateAgentWizard tests | Component | Create without a value rides the global | Body has no key |
| 17 | tool-iteration-limit.spec.ts | E2E | Admin raises; Confirmed lowering | Real gateway |

### Test Datasets

#### Dataset: Resolver (global G, stored own value O → effective, source, override, ignored)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | G=200, O=none | default | 200, global, absent, false | Scenario: Admin raises the global… | |
| 2 | G=200, O=50 | below | 50, agent, 50, false | Scenario: Operator lowers one agent | |
| 3 | G=200, O=200 | equal | 200, agent, 200, false | Scenario: Per-agent value equal to the global is accepted | |
| 4 | G=200, O=201 | just above | 200, global, 201, true | Scenario: Profile flags an ignored own value | D1 |
| 5 | G=200, O=500 | above | 200, global, 500, true | Scenario: Upgrade keeps a stored value above the global | |
| 6 | G=600, O=500 | raised | 500, agent, 500, false | Scenario: Raising the global activates… | |
| 7 | G=1, O=none | min | 1, global, absent, false | Scenario: Admin raises the global… | |
| 8 | G=1000, O=1000 | max | 1000, agent, 1000, false | Scenario: Per-agent value equal… | |
| 9 | G=200, O=5000 (hand-edited) | above max | 200, global, 5000, true | Scenario: Profile flags… | override unbounded on the wire |
| 10 | G=200, O=0 | zero stored | 200, global, absent, false | Scenario: Admin raises the global… | 0 = none |
| 11 | G=200, O=-5 (hand-edited) | negative stored | 200, global, absent, false | Scenario: Admin raises the global… | D17 |

#### Dataset: Confirmed lowering (live own values A=250, B=280; global 300 → PUT 200)

| # | Input (`confirmed_lowering`) | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `[{A,250},{B,280}]` | exact, same order | 200; A, B, global = 200 | Scenario: Confirmed lowering rewrites and audits | |
| 2 | `[{B,280},{A,250}]` | exact, reordered | 200 (not drift) | Scenario: Reordered confirmation is not drift | G4 |
| 3 | `[{A,250}]` | missing agent | 409, preview `[A,B]`, nothing written | Scenario: Drift between preview and confirm… | D16 |
| 4 | `[{A,250},{B,280},{C,260}]` | extra agent | 409, nothing written | Scenario: Drift between preview and confirm… | |
| 5 | `[{A,250},{B,270}]` | changed old value | 409, nothing written | Scenario: Changed old value is drift | |
| 6 | absent | no confirmation | 409, token not consumed | Scenario: Lowering without confirmation is refused | |
| 7 | `[{A,250},{A,250},{B,280}]` | duplicate id | 400 duplicate message | Scenario: Lowering without confirmation is refused | malformed |

#### Dataset: Global bounds (PUT /performance `max_tool_iterations`)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | 1 | min valid | 200 OK, value 1 | Scenario: Admin raises the global… | lowers every own value >1 only with consent |
| 2 | 1000 | max valid | 200 OK | Scenario: Admin raises the global… | |
| 3 | 0 | below min | 400 bound message | Scenario: Global out of range is refused | |
| 4 | -1 | negative | 400 | Scenario: Global out of range is refused | |
| 5 | 1001 | above max | 400 | Scenario: Global out of range is refused | |
| 6 | 2.5 | non-integer | 400 (request validation) | Scenario: Global out of range is refused | |

#### Dataset: Per-agent bounds (PUT/POST agents, sysagent tools; global 1000 unless stated)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | 1 | min | accepted | Scenario: Operator lowers one agent | |
| 2 | 1000 | max = global | accepted | Scenario: Per-agent value equal… | |
| 3 | 0 | below min | 400 bound | Scenario: Per-agent value out of range is refused | 0 no longer means "inherit" |
| 4 | -3 | negative | 400 bound | Scenario: Per-agent value out of range… | |
| 5 | 1001 | above max | 400 bound | Scenario: Per-agent value out of range… | |
| 6 | null | clear | accepted, no own value | Scenario: Use global limit clears… | PUT and `update_agent` only |
| 7 | 300 with global 200 | above global | 400 global message | Scenario: Per-agent value above the global… | |

#### Dataset: Env import (no marker; file global 200)

| # | Input (env value) | Boundary Type | Expected Output (saved global; log) | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | 80 | in range | 80; info "copied, no longer used" | Scenario: Env var imported once | |
| 2 | 1 | min | 1 | Scenario: Env var imported once | |
| 3 | 1000 | max | 1000 | Scenario: Env var imported once | |
| 4 | 0 | below min | 1; WARN names 0 | Scenario: Out-of-range env var… | D7 nearest bound |
| 5 | 5000 | above max | 1000; WARN names 5000 | Scenario: Out-of-range env var… | |
| 6 | -7 | negative | 1; WARN names -7 | Scenario: Out-of-range env var… | |
| 7 | abc | non-numeric | 200 unchanged; WARN; no marker; file unchanged | Scenario: Non-numeric env var is not imported | D17 |
| 8 | unset | absent | 200 unchanged; no marker written | Scenario: Env var ignored after import | |

#### Dataset: Saved global (file value → effective; saved_state; raw)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | 200 | valid | 200; ok; absent | Scenario: Saved global out of range… | |
| 2 | key missing | missing | 200; missing; absent | Scenario: Saved global out of range… | D13 |
| 3 | 0 | below min | 200; below_min; 0 | Scenario: Saved global out of range… | D13 |
| 4 | -4 | negative | 200; below_min; -4 | Scenario: Saved global out of range… | D17 |
| 5 | 1000 | max | 1000; ok | Scenario: Saved global out of range… | |
| 6 | 1001 | just above | 1000; above_max; 1001 | Scenario: Saved global out of range… | |
| 7 | 5000 | above | 1000; above_max; 5000 | Scenario: Saved global out of range… | |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|--------------------|----------------|------------------------------|-------|
| subagent_3p rejects `max_tool_iterations` on create/PUT | `TestCreateAgent_ValidateInbound_Subagent3pMaxToolIterationsRejected` (`rest_agents_create_test.go`), `TestSubagent3pForbiddenFieldsDrift` (`agent_field_rules_test.go`) | Yes — invert: accepted (D14); drift test keeps the table in sync | Intended behaviour change, not a weakening |
| Tool-limit message text | loop test comparing `toolLimitResponse` (`pkg/agent/loop_test.go`) | Yes — exact new text | |
| Executor preview default 50 | tests in `rest_executor_preview*_test.go` (qa-lead to locate) | Yes — expects resolver value | |
| SPA fixtures with `max_tool_iterations: 50` | `src/test/factories.ts` | Yes — add required source/ignored fields | |
| Performance PUT partial update of other fields | `PerformanceSection.test.tsx`, `rest_performance` tests | Yes — other fields untouched when only the limit changes, and vice versa | |

---

## Functional Requirements

- **FR-001**: System MUST hold one global limit at `agents.defaults.max_tool_iterations`, range 1–1000, shipped default 200, editable in Settings → Performance by an admin with step-up re-auth (Req 1, D2, D3, D8).
- **FR-002**: System MUST compute effective = min(global, own value), or the global when there is no own value, through one resolver used by the runtime, every Agent API response, the executor preview and the system-agent tools (Req 2, Req 3, D15).
- **FR-003**: Every Agent response MUST carry the effective value, `source`, `override` (when stored) and `override_ignored`; the SPA MUST render these and MUST NOT compute them (Req 3).
- **FR-004**: System MUST NOT contain any fallback literal for the limit other than one shipped-default constant; `DefaultExternalMaxTurns` and the `200` rungs in the runtime and API MUST be removed; the SPA MUST contain no literal default (Req 3, D4).
- **FR-005**: A change of the global MUST reload agents so each agent's next turn uses the new effective value without a restart (Req 4).
- **FR-006**: Every writable limit field MUST reject values outside 1–1000 with a message naming the bound (Req 5, D2).
- **FR-007**: A per-agent save (REST create/update, system-agent tools) above the current global MUST be refused with a message naming the global (D10, D15).
- **FR-008**: "Use global limit" MUST clear an agent's own value (explicit `null` on update — REST via the raw-body peek, `update_agent` via the explicit nil check before `jsonInt`, both in API and Data); an omitted field MUST leave the own value unchanged (D9, grill F1).
- **FR-009**: A stored own value above the global MUST be kept, capped, flagged in the API and profile, and listed in exactly one startup WARN line that names every capped agent with its stored value and the global (D1, D12, D19).
- **FR-010**: Lowering the global below agents' own values MUST require explicit consent (preview → confirm dialog → PUT carrying `confirmed_lowering`), MUST refuse with 409 and write nothing when the live affected set or any old value differs from `confirmed_lowering`, compared as an order-independent set keyed by `agent_id` (D16, G4), MUST lower exactly the confirmed agents to the new global, MUST audit each agent change and the global change, and MUST NOT restore values later (D11).
- **FR-011**: The env var MUST be imported once into the saved config (clamped to 1–1000, WARN when clamped), marked as imported, and never read again (D5, D6, D7).
- **FR-012**: A saved global that is missing or below 1 MUST run as 200 and above 1000 as 1000, in memory only, with a WARN log and a Settings warning; startup MUST never be refused (D13).
- **FR-013**: External-CLI workers MUST accept the per-agent field on create and update, show the same control and reset, and pass the effective value as their turn cap; the preview MUST show that same value (D4, D14).
- **FR-014**: The REST create path MUST persist a supplied per-agent value (fixes the dropped field).
- **FR-015**: The tool-limit message MUST point to Settings → Performance and the agent profile, not `config.json`.
- **FR-016**: The per-agent control MUST be extracted from `AgentProfile.tsx` into its own component; `AgentProfile.tsx` MUST shrink (grandfathered budget may only shrink).
- **FR-017**: The SPA MUST send `max_tool_iterations` on an agent PUT only when the operator changed it (or reset it).
- **FR-018**: The lowering preview MUST use the same gate as `GET /api/v1/performance` (authenticated, blocked under dev-mode bypass), and changing the global MUST additionally require the step-up token.
- **FR-019**: A turn already running when the limit changes MUST keep the limit in force when it started; the new effective limit MUST apply from that agent's next turn (D18).
- **FR-020**: Below-range or non-numeric inputs MUST be treated as missing: saved global < 1 → 200 in memory with WARN and no file rewrite; non-numeric env var → not imported, WARN, no file write; stored per-agent value ≤ 0 → no own value (D17).
- **FR-021**: The D11 lowering MUST run every drift and revision check before its first write; a failure after writes began MUST roll back the already-lowered agents (each rollback audited) and leave the global unchanged; a failed rollback MUST be reported in the response (every agent left lowered, with current and old value) and logged at ERROR — never silent (D16, grill G1).

## Success Criteria

- **SC-001**: For every row of Dataset "Resolver", the runtime cap, the Agent API response and (for workers) the preview argv agree — 0 mismatches across the dataset.
- **SC-002**: `grep` over `pkg/` and `src/` (excluding generated and tests) finds 0 limit fallbacks other than the shipped-default constant, and 0 occurrences of `DefaultExternalMaxTurns`.
- **SC-003**: `AgentProfile.tsx` line count after the change is below 3,069 (today) and its `scripts/budgets/files.txt` entry is lowered accordingly.
- **SC-004**: Every out-of-range dataset row returns the documented status and message; every "unchanged" assertion reads the stored value back.
- **SC-005**: Booting with each "Saved global" and "Env import" row starts the gateway (HTTP health 200) — 0 boot refusals.
- **SC-006**: `make verify-contracts` passes with the new schemas; no hand-written wire type added.

---

## Reachability

- **Tool registration**: none new. The existing system-agent tools `create_agent` and `update_agent` gain bounds and the global check; their registration and policy entries are unchanged (`config.ReconcileToolPolicyCeiling` / the ceiling in `config.json`, Hard Constraint #6) — they are already callable by the system agent.
- **Screen**: Settings → Performance (`src/components/settings/PerformanceSection.tsx`) for the global and the D11 dialog; agent profile **Advanced** tab (`src/components/agents/AgentProfile.tsx` rendering the new `src/components/agents/ToolIterationLimitField.tsx`) for every agent type including subagent_3p; create wizard Advanced step (`src/components/agents/wizard/Advanced.tsx`, both native and external variants). Runtime reach: every agent turn (`pkg/agent` loop) and every external-CLI dispatch.
- **Test plan execution**: RED — qa-lead writes the Go and vitest tests above and shows them failing on this branch (CI tests-only commit); GREEN — backend-lead then frontend-lead; CHECK — qa-lead audit; the Playwright spec and a UAT lane (Settings change → agent profile source line → chat turn hitting a limit of 2 → message text) are **executed** before the 8-reviewer gate, with `uat-validator` checking the PASS claims.

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|---------------------------|--------------------------|
| FR-001 | US-1 | Fresh install shows…; Admin raises the global…; PUT without step-up token… | TestPerformancePut_MaxToolIterations_*, PerformanceSection.maxToolIterations.test.tsx |
| FR-002 | US-2, US-4, US-8 | Operator lowers one agent; Per-agent value equal to the global is accepted; System agent creates with an own value; Worker preview equals runtime (both); Upgrade keeps… | TestResolveMaxToolIterations_Table, TestExecutorPreview_MatchesDispatch, TestLoop_StopsAtEffectiveLimit |
| FR-003 | US-2, US-5 | Profile flags an ignored own value; Operator lowers one agent | TestAgentResponses_UseResolver, ToolIterationLimitField.test.tsx |
| FR-004 | US-1, US-4 | Worker preview equals runtime with no own value; Create without a value rides the global | TestNoHiddenLiteral_Guard |
| FR-005 | US-1 | Admin raises the global and every non-overriding agent follows | TestPerformancePut_MaxToolIterations_Reload |
| FR-006 | US-1, US-2, US-8 | Global out of range is refused; Per-agent value out of range is refused; System agent out of range | TestPerformancePut_*, TestAgentUpdate_* |
| FR-007 | US-2, US-3, US-8 | Per-agent value above the global is refused; Create above the global is refused; System agent cannot exceed the global | TestAgentUpdate_*, TestAgentCreate_*, TestSysagentAgentTools_* |
| FR-008 | US-2, US-8 | Use global limit clears the own value; Omitted field leaves the own value unchanged; System agent clears an own value | TestAgentUpdate_MaxToolIterations_NullVsOmitted, TestSysagentAgentTools_MaxToolIterations, ToolIterationLimitField.test.tsx |
| FR-009 | US-5 | Upgrade keeps a stored value above the global (D19 multi-agent line); Profile flags…; Raising the global activates… | TestUpgradeBootLog_ListsCappedAgents, TestResolveMaxToolIterations_Table |
| FR-010 | US-6 | Lowering preview lists only…; Cancel in the confirm dialog…; Confirmed lowering rewrites and audits; Lowering without confirmation is refused; Drift between preview and confirm…; Changed old value is drift; Reordered confirmation is not drift; Lowering with no affected agents…; Raising again does not restore… | TestPerformancePreview_*, TestPerformancePut_Lowering_*, TestPerformancePut_LoweringDrift_*, maxToolIterationsLoweringConflict.test.ts, PerformanceSection.maxToolIterations.test.tsx, tool-iteration-limit.spec.ts |
| FR-011 | US-7 | Env var imported once; Env var ignored after import; Out-of-range env var… | TestEnvImport_OnceAndClamp |
| FR-012 | US-7 | Saved global out of range is corrected in memory only | TestEffectiveGlobal_SavedStates, PerformanceSection.maxToolIterations.test.tsx |
| FR-013 | US-3, US-4 | External-CLI worker created with its own limit; Worker PUT of the limit is accepted; Worker preview (both) | TestAgentCreate_Worker, TestAgentUpdate_Worker, TestExecutorPreview_MatchesDispatch |
| FR-014 | US-3 | Create keeps the requested own value | TestAgentCreate_MaxToolIterations_Persists |
| FR-015 | US-9 | Tool-limit message points to Settings | TestToolLimitResponse_Text |
| FR-016 | US-2 | Operator lowers one agent (UI leg) | budget gate `make lint-budgets`, ToolIterationLimitField.test.tsx |
| FR-017 | US-2 | Unrelated autosave does not resend the limit | ToolIterationLimitField.test.tsx / AgentProfile autosave test |
| FR-018 | US-1 | Preview is blocked under dev-mode bypass; PUT without step-up token is refused; Cancelling the password prompt writes nothing | TestPerformancePreview_Bypass, TestPerformancePut_NoStepUp, PerformanceSection.maxToolIterations.test.tsx |
| FR-019 | US-1 | Running turn keeps the limit it started with | TestLoop_RunningTurnKeepsStartLimit |
| FR-020 | US-5, US-7 | Non-numeric env var is not imported; Saved global out of range is corrected in memory only; Admin raises the global… (Resolver rows 10–11) | TestEnvImport_OnceAndClamp, TestEffectiveGlobal_SavedStates, TestResolveMaxToolIterations_Table |
| FR-021 | US-6 | Mid-write failure rolls back already-lowered agents; Failed rollback is reported, not silent | TestPerformancePut_LoweringRollback_* |

**Completeness check**: all 21 FRs have ≥1 scenario and ≥1 test; all 45 scenarios appear at least once (verified by the author's final self-check).

---

## Assumptions

- The registry reload used by `PUT /settings/context` is safe to call from `PUT /performance` (same mechanism, same handler family).
- Sections removed: none. Contract context is merged into "Contract Changes" (the template's order), not a separate section.

## Clarifications

### 2026-09-26

- Q: Is the global a ceiling or a default? -> A: Ceiling; per-agent may only lower (Requirement 2, D10).
- Q: Upgrade with stored values above the global? -> A: Keep, cap, flag, log once (D1, D12).
- Q: Range? -> A: 1–1000 both layers (D2).
- Q: External-CLI workers? -> A: Same global and same per-agent control; fix the 50 preview (D4, D14).
- Q: Env var? -> A: Not a setting source; imported once, clamped to the nearest bound (D5–D7). Mechanism (spec decision): persisted marker `agents.defaults.max_tool_iterations_env_imported`.
- Q: How does the SPA learn the D11 affected agents given the single-use step-up token? -> A: Separate read-only admin preview endpoint, then the confirmed PUT carrying the `confirmed_lowering` snapshot (revised after grill F2 / D16).
- Q: Saved global outside 1–1000? -> A: In-memory correction, warn in log and Settings, never refuse (D13).
- Q: D8's "admin" vs the code? -> A: Maps to today's single-account gate (authenticated + dev-bypass guard + step-up); no role check (verified `pkg/gateway/rest.go::adminWrap`; founder informed).
- Q: Agent store the only per-agent persistence? -> A: Yes — verified: `pkg/config/config.go::AgentsConfig.List` is tagged `json:"-"` and `pkg/agentstore` persists `config.AgentConfig` (grill F5).

### 2026-09-26 (after grill round 1)

- Q: Preview/confirm drift? -> A: Refuse, save nothing, reload the dialog with the new list (D16). Contract: `confirmed_lowering` + 409 `MaxToolIterationsLoweringConflict`.
- Q: Below-range and non-numeric values? -> A: Treat as missing (D17).
- Q: Running turn when the limit changes? -> A: Keeps the limit in force when it started (D18).
- Q: Startup warning for several capped agents? -> A: One WARN line listing all, with stored value and the global (D19).
- Q: Explicit null vs omitted on agent PUT (grill F1)? -> A: Raw-body peek, same as `context_window_override`; RED test uses raw JSON.
- Q: D11 result toast accessibility (grill F4)? -> A: `role="status"` toast with a 10 s duration plus a persistent inline summary.

### 2026-09-26 (after grill round 2 — final)

- G1: one outcome for a D11 write conflict — all checks before the first write (409, nothing written); a later failure rolls back and leaves the global unchanged; a failed rollback is reported and logged (FR-021).
- G2: `update_agent` clear needs an explicit nil check before `jsonInt` in `pkg/sysagent/tools/agent_apply_args.go` (the earlier "same distinction" claim was wrong).
- G3: SPA consumes the 409 through a typed `MaxToolIterationsLoweringConflictError` + guard, mirroring `src/lib/api/library.ts::LibraryVersionConflictError`.
- G4: `confirmed_lowering` compared as an order-independent set keyed by `agent_id`; duplicates are 400.
- G5: Functional Requirements and the traceability table are in numeric order.
