// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// HandlePerformance handles GET and PUT /api/v1/performance.
//
// GET returns the current max_parallel_agents config and the effective
// (auto-detected or explicit) value actually in use.
//
// PUT accepts {max_parallel_agents: int} and updates config.json atomically.
// The dispatch semaphore is resized in-memory immediately so the new value
// takes effect without a restart.
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
	cfg := a.agentLoop.GetConfig()
	effective, capped := cfg.Performance.EffectiveMaxParallelAgents()
	configured := wireMaxParallelAgents(cfg.Performance.MaxParallelAgents, effective)
	// tools_on_demand mirrors cfg.Tools.Manifest.Compressed:
	// true (default) = load tools on demand; false = all tools every message.
	toolsOnDemand := cfg.Tools.Manifest.Compressed
	jsonOK(w, gen.PerformanceSettings{
		MaxParallelAgents:           &configured,
		EffectiveMaxParallelAgents:  &effective,
		MaxParallelAgentsConfigured: &capped,
		ToolsOnDemand:               &toolsOnDemand,
	})
}

func (a *restAPI) putPerformance(w http.ResponseWriter, r *http.Request) {
	// Re-auth gate (Spec-6 FR-12.2 / Spec-3 FR-6.6): the Max-parallel-agents
	// performance setting is a sensitive HTTP-layer change and requires the
	// single-use re-auth consent token — the same gate the Integrations PUT
	// enforces. RequireNotBypass (already in adminWrap) is a 503 dev-mode guard,
	// NOT this consent check. The user is guaranteed in context here because the
	// route is admin-wrapped.
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if !a.requireReAuth(w, r, user.Username) {
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
	if req.MaxParallelAgents == nil && req.ToolsOnDemand == nil {
		jsonErr(w, http.StatusBadRequest, "at least one of max_parallel_agents or tools_on_demand is required")
		return
	}

	// Validate max_parallel_agents when present.
	if req.MaxParallelAgents != nil && *req.MaxParallelAgents < 0 {
		jsonErr(w, http.StatusBadRequest, "max_parallel_agents must be >= 0")
		return
	}

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
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
		return nil
	}); err != nil {
		jsonErr(w, http.StatusInternalServerError,
			fmt.Sprintf("could not update performance settings: %v", err))
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

	newCfg := a.agentLoop.GetConfig()
	effective, capped := newCfg.Performance.EffectiveMaxParallelAgents()
	configured := wireMaxParallelAgents(newCfg.Performance.MaxParallelAgents, effective)
	toolsOnDemand := newCfg.Tools.Manifest.Compressed
	jsonOK(w, gen.PerformanceSettings{
		MaxParallelAgents:           &configured,
		EffectiveMaxParallelAgents:  &effective,
		MaxParallelAgentsConfigured: &capped,
		ToolsOnDemand:               &toolsOnDemand,
	})
}
