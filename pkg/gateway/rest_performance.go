// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// HandlePerformance handles GET and PUT /api/v1/performance.
//
// GET returns the current max_parallel_agents config, the effective
// (auto-detected or explicit) value actually in use, and goal_max_rounds —
// the global goal try limit (GOAL-FR-024/FR-045, D-D/D-E): how many tries a
// goal gets, in chat and within one task run alike. There is no per-goal
// override. It does not bound task attempts (how many fresh runs a task gets):
// that is the separate planning.task_max_attempts config value, overridden per
// task by max_attempts (founder decision 2026-09-14, issue #710).
//
// PUT accepts a partial update of {max_parallel_agents, tools_on_demand,
// goal_max_rounds} and updates config.json atomically. The dispatch
// semaphore is resized in-memory immediately so a changed max_parallel_agents
// takes effect without a restart; goal_max_rounds takes effect for every goal
// that STARTS after the config reload below completes — a chat goal set after
// it, and a task run started after it — since every reader resolves it live
// via cfg.Planning.EffectiveGoalMaxRounds() rather than snapshotting it at
// boot. A goal already running keeps the limit it started with (it is
// snapshotted onto the goal record when the goal starts).
//
// Admin-only: enforced by the adminWrap registration in rest.go.
func (a *restAPI) HandlePerformance(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getPerformance(w, r)
	case http.MethodPut:
		a.putPerformance(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// wireMaxParallelAgentsMinimum is PerformanceSettings.yaml's declared
// `minimum` for max_parallel_agents on the wire (response) side — NOT the
// same number as PerformanceSettingsUpdate.yaml's request-side `minimum: 0`
// (0 there means "reset to auto"; a response echoing 0 back is invalid,
// since 0 on disk is an internal sentinel, never a real concurrency value).
const wireMaxParallelAgentsMinimum = 1

// wireMaxParallelAgents resolves the on-the-wire value for
// PerformanceSettings.max_parallel_agents from the raw on-disk configured
// value. 0 (and, defensively, any value below the schema floor) is the
// "unconfigured / auto-detect" sentinel and is never valid to echo back
// (PerformanceSettings.yaml declares `minimum: 1`) — it is substituted with
// the resolved effective value instead, so the wire payload is always
// schema-valid and shows the concurrency actually in use. Any configured
// value >= 1 (including an operator's deliberate single-flight choice of 1)
// is surfaced exactly as configured — never silently overridden — matching
// this project's ban on the ADR-037 silent-clamping anti-pattern.
//
// THIS SUBSTITUTION IS WHY max_parallel_agents_configured EXISTS (FR-069).
// Because an unconfigured host echoes the resolved effective value here, its
// payload is byte-identical in this field to a host where an operator typed
// that same number in. A client reading only these two integers cannot tell
// "the operator chose 2000" from "nothing is configured and 2000 is the
// physical backstop" — and rendering the second as a recommendation is
// exactly the defect FR-069 names. The boolean carries the distinction the
// integers cannot.
//
// Shared by getPerformance and putPerformance so both return the identical
// shape: before this helper existed, putPerformance skipped this floor
// entirely and echoed the raw on-disk 0 straight onto the wire (MAJOR-3,
// code review 2026-08-04) — a successful PUT of 0 (the documented "reset to
// auto" contract) produced a schema-invalid body that the SPA's zod
// validation then rejected, surfacing a false "failed to save" toast on a
// write that had, in fact, correctly persisted.
func wireMaxParallelAgents(configured, effective int) int {
	if configured < wireMaxParallelAgentsMinimum {
		return effective
	}
	return configured
}

func (a *restAPI) getPerformance(w http.ResponseWriter, _ *http.Request) {
	resp := performanceSettingsResponse(a.agentLoop.GetConfig())
	resp.PendingApply = a.pendingApply.snapshot()
	jsonOK(w, resp)
}

// performanceSettingsResponse builds the PerformanceSettings body from cfg;
// shared by GET and PUT so both return the identical shape.
func performanceSettingsResponse(cfg *config.Config) gen.PerformanceSettings {
	effective, capped := cfg.Performance.EffectiveMaxParallelAgents()
	configured := wireMaxParallelAgents(cfg.Performance.MaxParallelAgents, effective)
	// tools_on_demand mirrors cfg.Tools.Manifest.Compressed:
	// true (default) = load tools on demand; false = all tools every message.
	toolsOnDemand := cfg.Tools.Manifest.Compressed
	// goal_max_rounds (GOAL-FR-024/FR-045, D-D/D-E): the SINGLE, GLOBAL
	// adjudication-round ceiling for every goal — task-owned and
	// session-owned alike. There is no per-goal override (D-E/NQ-2), so this
	// is always the resolved value of the one config field, never echoed
	// back as an unresolved 0. Always present in responses per the schema.
	goalMaxRounds := cfg.Planning.EffectiveGoalMaxRounds()
	ps := gen.PerformanceSettings{
		MaxParallelAgents:           &configured,
		EffectiveMaxParallelAgents:  &effective,
		MaxParallelAgentsConfigured: &capped,
		ToolsOnDemand:               &toolsOnDemand,
		GoalMaxRounds:               &goalMaxRounds,
	}
	// #904: the global tool-iteration limit in force, plus the D13 saved
	// state (and raw value when out of range) for the Settings warning.
	applyPerformanceMaxToolIterations(&ps, cfg)
	return ps
}

// putPerformance applies a partial PerformanceSettingsUpdate. For the #904
// global tool-iteration limit it follows the spec's D11/D16 write order:
// (1) decode and bound-check; (2) drift pre-check before the step-up token
// is consumed; (3) requireReAuth; (4–6) under configMu: the deciding drift
// and revision check, the confirmed agents lowered, then config.json written
// once (see writePerformanceLocked); (7) registry reload.
func (a *restAPI) putPerformance(w http.ResponseWriter, r *http.Request) {
	// Re-auth gate (Spec-6 FR-12.2 / Spec-3 FR-6.6): the performance settings
	// are a sensitive HTTP-layer change and require the single-use re-auth
	// consent token — the same gate the Integrations PUT enforces.
	// RequireNotBypass (already in adminWrap) is a 503 dev-mode guard, NOT
	// this consent check. The user is guaranteed in context here because the
	// route is admin-wrapped. The token is consumed only after the body is
	// validated and the #904 drift pre-check passed (spec step 3).
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.PerformanceSettingsUpdate
	if !decodeAndValidate(w, r, "PerformanceSettingsUpdate", &req, validateEnabled) {
		return
	}

	// At least one field must be present — a PUT with no recognized fields is
	// a no-op that almost certainly indicates a client bug.
	if req.MaxParallelAgents == nil && req.ToolsOnDemand == nil && req.GoalMaxRounds == nil &&
		req.MaxToolIterations == nil {
		jsonErr(w, http.StatusBadRequest,
			"at least one of max_parallel_agents, tools_on_demand, goal_max_rounds or max_tool_iterations is required")
		return
	}

	// Validate max_parallel_agents when present.
	if req.MaxParallelAgents != nil && *req.MaxParallelAgents < 0 {
		jsonErr(w, http.StatusBadRequest, "max_parallel_agents must be >= 0")
		return
	}

	// Validate goal_max_rounds when present (GOAL-FR-025/FR-045, D-E): the
	// single global budget is still a real round count and a value below 1
	// is rejected outright — there is no "reset to auto" sentinel for this
	// field the way 0 is for max_parallel_agents.
	if req.GoalMaxRounds != nil && *req.GoalMaxRounds < 1 {
		jsonErr(w, http.StatusBadRequest, "goal_max_rounds must be >= 1")
		return
	}

	// #904 step 1 (bound, well-formed confirmation) and step 2 (drift
	// pre-check, before the token is consumed).
	limitUpd, ok := parseMaxToolIterationsUpdate(w, &req)
	if !ok {
		return
	}
	if limitUpd != nil && !a.preCheckMaxToolIterationsDrift(w, limitUpd) {
		return
	}

	if !a.requireReAuth(w, r, user.Username) {
		return
	}

	changed := performanceChangedFields(&req)
	outcome, werr := a.writePerformanceLocked(r.Context(), limitUpd, changed, func(m map[string]any) error {
		// Partial update: only touch the fields that were provided.
		if req.MaxParallelAgents != nil {
			// Accept 0 as "reset to auto-detect"; values < 0 rejected above.
			perf := ensureMap(m, "performance")
			perf["max_parallel_agents"] = *req.MaxParallelAgents
		}
		if req.ToolsOnDemand != nil {
			// tools_on_demand == true ⇔ cfg.Tools.Manifest.Compressed == true
			tools := ensureMap(m, "tools")
			manifest := ensureMap(tools, "manifest")
			manifest["compressed"] = *req.ToolsOnDemand
		}
		if req.GoalMaxRounds != nil {
			planning := ensureMap(m, "planning")
			planning["goal_max_rounds"] = *req.GoalMaxRounds
		}
		if limitUpd != nil {
			defaults := ensureMap(m, "agents", "defaults")
			defaults["max_tool_iterations"] = limitUpd.value
			// D6: an admin-set global ends the one-time env import for
			// good — the retired env var must never overwrite it on a
			// later boot, even when the import itself never ran.
			defaults["max_tool_iterations_env_imported"] = true
		}
		return nil
	})
	if werr != nil {
		writeJSON(w, werr.status, werr.body)
		return
	}
	if outcome.notApplied != nil {
		// Saved but not applied: config.json (and any lowered agents) are
		// written, the in-memory refresh failed — same answer as a failed
		// registry reload below.
		// writePerformanceLocked already marked the fields pending apply.
		writePerformanceReloadFailed(w, outcome, gen.PerformanceReloadFailedDetailsStageRefresh, changed)
		return
	}

	// Resize the in-memory dispatch semaphore immediately so the new parallel cap
	// takes effect without a restart (no-op when max_parallel_agents was not updated).
	te := agent.GetTaskExecutor(a.agentLoop)
	if te != nil {
		newCfg := a.agentLoop.GetConfig()
		newEffective, _ := newCfg.Performance.EffectiveMaxParallelAgents()
		te.ResizeDispatchSema(newEffective)
	}

	// #904 step 7 / FR-005: every agent instance snapshots its limit at
	// construction, so a changed global (or a lowered agent) needs a
	// registry reload for the next turn to use it. A turn already running
	// keeps the limit it started with (D18). While an earlier save is
	// pending apply the registry is reloaded whatever this request changed:
	// that is how a later PUT applies it. reloadAgentsAndConfirm also fails
	// when the reload ran but its rebuild failed (gate round 3) — plain
	// triggerReloadAndWait reports that as success.
	if outcome.globalChanged || len(outcome.lowered) > 0 || a.pendingApply.isSet() {
		if err := a.reloadAgentsAndConfirm(); err != nil {
			a.pendingApply.mark(gen.PerformanceReloadFailedDetailsStageReload, changed,
				a.reloadOutcome.startedCount(), a.reloadOutcome.configReadsCount())
			writePerformanceReloadFailed(w, outcome, gen.PerformanceReloadFailedDetailsStageReload, changed)
			return
		}
	}
	// The refresh and (when needed) the registry reload both succeeded:
	// everything saved up to this request's refresh is in force.
	a.pendingApply.clearIfEpoch(outcome.appliedEpoch)

	resp := performanceSettingsResponse(a.agentLoop.GetConfig())
	resp.PendingApply = a.pendingApply.snapshot()
	if len(outcome.lowered) > 0 {
		lowered := outcome.lowered
		resp.MaxToolIterationsLoweredAgents = &lowered
	}
	jsonOK(w, resp)
}

// writePerformanceReloadFailed answers a PUT whose writes are COMMITTED
// (config.json and any lowered agents are on disk and audited) but not in
// force: 500 with code performance_reload_failed and a
// PerformanceReloadFailedError body, so the client can say "saved, not
// applied yet" instead of "failed". stage says how far the apply got —
// refresh: the in-memory config was NOT swapped (GET /performance still
// shows the old values); reload: the in-memory config was swapped but the
// agent registry reload failed. The lowered agents travel in
// details.lowered_agents — GET /performance does not carry them
// (PerformanceSettings.max_tool_iterations_lowered_agents is PUT-only), so
// this response is the only place the summary survives.
func writePerformanceReloadFailed(w http.ResponseWriter, outcome performanceWriteOutcome,
	stage gen.PerformanceReloadFailedDetailsStage, changed []gen.PerformanceReloadFailedDetailsChangedFields,
) {
	// The cause is NOT logged here: its text can carry a credential
	// reference name (CodeQL clear-text logging, PR #932). For the refresh
	// stage, refreshConfigAndRewireServices logs its load, roster and
	// credential causes itself; the reload stage's cause is logged by
	// waitForReloadOutcome ("config reload failed": the reload could not
	// start), waitForReload (timeout) or runReloadCycle ("Config reload
	// failed": the rebuild failed).
	logsafeError("rest: PUT /performance: settings saved but not applied",
		"stage", string(stage), "lowered_agents", len(outcome.lowered))
	lowered := outcome.lowered
	if lowered == nil {
		lowered = []gen.MaxToolIterationAgentChange{}
	}
	writeJSON(w, http.StatusInternalServerError, gen.PerformanceReloadFailedError{
		Error: performanceReloadFailedMessage(stage, changed),
		Code:  performanceReloadFailedCode,
		Details: gen.PerformanceReloadFailedDetails{
			Stage:         stage,
			ChangedFields: changed,
			LoweredAgents: lowered,
		},
	})
}

// performanceChangedFields lists, in contract enum order, the Performance
// fields present in the PUT body — the settings this request changed.
func performanceChangedFields(req *gen.PerformanceSettingsUpdate) []gen.PerformanceReloadFailedDetailsChangedFields {
	out := make([]gen.PerformanceReloadFailedDetailsChangedFields, 0, 4)
	if req.MaxParallelAgents != nil {
		out = append(out, gen.PerformanceReloadFailedDetailsChangedFieldsMaxParallelAgents)
	}
	if req.ToolsOnDemand != nil {
		out = append(out, gen.PerformanceReloadFailedDetailsChangedFieldsToolsOnDemand)
	}
	if req.GoalMaxRounds != nil {
		out = append(out, gen.PerformanceReloadFailedDetailsChangedFieldsGoalMaxRounds)
	}
	if req.MaxToolIterations != nil {
		out = append(out, gen.PerformanceReloadFailedDetailsChangedFieldsMaxToolIterations)
	}
	return out
}

// performanceSettingLabels names each Performance field in user-facing text.
var performanceSettingLabels = map[gen.PerformanceReloadFailedDetailsChangedFields]string{
	gen.PerformanceReloadFailedDetailsChangedFieldsMaxParallelAgents: "the parallel-agent limit",
	gen.PerformanceReloadFailedDetailsChangedFieldsToolsOnDemand:     "tools on demand",
	gen.PerformanceReloadFailedDetailsChangedFieldsGoalMaxRounds:     "the goal round budget",
	gen.PerformanceReloadFailedDetailsChangedFieldsMaxToolIterations: "the tool-iteration limit",
}

// performanceReloadFailedMessage builds the human-readable error from the
// fields this request actually changed and the stage the apply reached.
func performanceReloadFailedMessage(stage gen.PerformanceReloadFailedDetailsStage,
	changed []gen.PerformanceReloadFailedDetailsChangedFields,
) string {
	names := make([]string, 0, len(changed))
	for _, f := range changed {
		names = append(names, performanceSettingLabels[f])
	}
	what := "the new performance settings"
	switch len(names) {
	case 0:
	case 1:
		what = names[0]
	default:
		what = strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
	if stage == gen.PerformanceReloadFailedDetailsStageRefresh {
		return "performance settings saved but not applied: the running configuration could not be refreshed; " +
			what + " will apply after the next reload or restart"
	}
	return "performance settings saved but the agent reload failed; " +
		what + " will apply to agents after the next reload or restart"
}
