# Tool iteration limit — global ceiling, per-agent tightening (founder interview record)

Status: Interview complete — input to `plan-spec`
Issue: https://github.com/elicify-ai/omnipus/issues/904
Captured at commit: d35e386 (`release/v0.1.1`)
Interview date: 2026-09-26 (team-lead, founder via question tool)

This is the founder-interview output that `plan-spec` turns into the feature spec. It records
what the founder decided and the as-is findings those decisions rest on. It is not the spec.

## As-is findings (architect investigation, 2026-09-26, spot-checked by team-lead)

- A global value already exists: `pkg/config/config.go::AgentDefaults.MaxToolIterations`
  (`agents.defaults.max_tool_iterations`), shipped default 200 in
  `pkg/config/defaults.go::defaultAgentsConfig`. Env override
  `OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS` (applied by `LoadConfig` after the file).
- Today the per-agent value (`AgentConfig.MaxToolIterations`) WINS over the global — it can
  raise as well as lower. `pkg/agent/instance.go::resolveRuntimeLimits` resolves
  per-agent → global → literal `200` (a hidden third default).
- The rule is duplicated: `instance.go::resolveRuntimeLimits`,
  `pkg/gateway/rest_agents.go::buildAgentDefaults`, `::applyAgentOverrides`, the PUT echo in
  `pkg/gateway/rest_agents_update.go`, and hardcoded `200` in `src/components/agents/AgentProfile.tsx`,
  `CreateAgentWizard.tsx`, `wizard/Advanced.tsx`.
- No global control in the UI and no REST field for it. Per-agent field shown in
  `AgentProfile.tsx` (displays effective, writes override; cannot be cleared).
- The value is snapshotted per agent instance; `AgentLoop.SwapConfig` does not rebuild
  instances, so a global change needs a registry reload (`triggerReloadAndWait` precedent in
  `rest_context_settings.go`).
- Found bugs (in scope): REST create drops `max_tool_iterations`
  (`rest_agents_create.go::normalizeVariant`); `AgentUpdateRequest.max_tool_iterations` has no
  bounds and the PUT echoes the raw value; external-executor preview shows `--max-turns 50`
  while runtime uses the resolved limit; SPA hardcodes 200 in three places.
- `AgentProfile.tsx` is grandfathered in `scripts/budgets/files.txt` and may only shrink — the
  per-agent control must be extracted into its own component.

## Requirements

1. One global limit in config, editable in **Settings → Performance**, applying to every agent
   without its own value.
2. A per-agent value may only be **lower** than the global. Effective = min(global, agent).
3. One resolution function, used by the runtime and by every API response; the SPA renders the
   server-computed effective value and its source (e.g. "global (200)" / "lowered to 50") and
   never recomputes the rule. No hidden literal fallback anywhere.
4. Raising the global applies to all non-overriding agents without editing each (reload).
5. Allowed range 1–1000 for both layers; rejected values get a message naming the bound.

## Decisions Log

| ID | Topic | Decision | Rationale | Source | Date |
|---|---|---|---|---|---|
| D1 | Upgrade: stored agent value above the global | Keep the stored value untouched; cap at the global; UI shows "above the global limit, has no effect"; log lists affected agents once per boot | Nothing silently rewritten on upgrade (issue acceptance #6) | Interview | 2026-09-26 |
| D2 | Accepted range | 1–1000, global and per agent | Room for long tasks with a clear upper bound (note the loop's internal 2x hard stop) | Interview | 2026-09-26 |
| D3 | Where the global control lives | Settings → Performance, beside "Tries per goal" | The existing home of global agent-loop budgets | Interview | 2026-09-26 |
| D4 | External-CLI workers | Follow the same global limit; fix the executor preview that shows 50 | One limit for all agents; matches current effective behaviour | Interview | 2026-09-26 |
| D5 | Env var `OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS` | Removed as a setting source | Founder choice: settings screen and config file are the only sources | Interview | 2026-09-26 |
| D6 | Env var still set after upgrade | Copy its value into the saved config once, then ignore it; log that it is no longer used | Nothing changes for an install that relied on it | Interview | 2026-09-26 |
| D7 | Env value outside 1–1000 at the one-time copy | Copy the nearest bound (1 or 1000) and log a warning naming the old value; always start | Never an outage (cf. #908) | Interview | 2026-09-26 |
| D8 | Who may change the global | Admin with step-up re-auth, same as every other Performance setting | Single-user app today (founder is admin); stays safe when more users arrive. Founder first said "any signed-in user", then chose to keep the page's protection on follow-up | Interview | 2026-09-26 |
| D9 | "Use global limit" on the agent profile | Yes — one click clears the agent's own value | Today an override can never be cleared | Interview | 2026-09-26 |
| D10 | Saving an agent value above the global | Refuse the save with a message naming the global limit | Founder choice (architect had recommended accept-and-flag) | Interview | 2026-09-26 |
| D11 | Lowering the global below some agents' own values | Allowed; those agents' stored values are lowered to the new global. A confirm dialog lists each affected agent (old → new) before anything changes; each change is audited | Deliberate operator action; visible, cancellable. Old values are NOT restored if the global is raised again (founder informed) | Interview | 2026-09-26 |
| D13 | Saved global value outside 1–1000 at startup (0, missing, 5000) | Correct in memory only: 1001+ → 1000; 0 or missing → shipped default (200); never rewrite the file; WARN in the log and a warning in Settings; never refuse to start | Matches D1 (upgrade never rewrites) and D7 (never an outage) | Interview | 2026-09-26 |
| D14 | External-CLI workers' own limit | They get the same per-agent "lower than global" control and "Use global limit" reset as other agents (create form + profile) | Founder choice (squad lead had recommended global-only); today `agent_field_rules.go` rejects the field for this variant — the spec must lift that | Interview | 2026-09-26 |
| D15 | System-agent chat path (`create_agent`/`update_agent` tools) | Bound by the same 1–1000 range and the D10 refuse-above-global rule (no second way around them) | Found by the squad's code check; direct consequence of D2/D10, not a new founder choice | Interview | 2026-09-26 |
| D12 | Relationship D1 vs D11 | Upgrade never rewrites (D1); an explicit global lowering does rewrite, after confirmation (D11). The capped-and-flagged state (D1) therefore only arises from upgrade or hand-edited config | Keeps both founder answers consistent | Interview | 2026-09-26 |

## Open points for plan-spec (not founder decisions)

- Contract shape (architect's proposal): keep `Agent.max_tool_iterations` as the effective
  value; add `max_tool_iterations_override` (nullable), `max_tool_iterations_source`
  (`global|agent`), `max_tool_iterations_override_ignored`; add the global to
  `PerformanceSettings`/`PerformanceSettingsUpdate`; nullable request field with 1–1000 bounds
  (`null` clears). The "lower the agents" preview for D11 needs a way for the SPA to learn the
  affected agents before confirming (e.g. a dry-run field on the PUT response or a preview
  endpoint) — spec decides.
- ADR-066 text ("per-agent → defaults → hardcoded 200") needs a dated correction.
- Squad code check (2026-09-26): `pkg/agent/external_dispatch.go::DefaultExternalMaxTurns` = 50
  is a second hidden literal (the executor preview falls back to it) — remove under the
  no-hidden-default rule; `pkg/agent/loop.go::toolLimitResponse` tells users to edit
  config.json — point it to Settings; `PUT /api/v1/performance` consumes a single-use step-up
  token (`requireReAuth`), so the D11 affected-agents preview must be a separate read-only
  admin endpoint followed by the confirmed PUT.

## Dependency Graph & Implementation Order

1. Spec (`plan-spec`) → two `grill-spec` rounds with founder interviews between.
2. Wave 1 (parallel): contract + regeneration (backend-lead) · resolver + config migration
   (backend-lead) · RED tests (qa-lead).
3. Wave 2 (parallel): runtime wiring · API (+ the four found bugs) · Performance global control
   (frontend-lead) · extracted per-agent field with source + reset + D11 confirm (frontend-lead).
4. Wave 3: docs + ADR-066 correction · UAT lane · 8-reviewer gate · founder's yes · land.
