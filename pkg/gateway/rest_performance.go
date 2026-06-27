//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"net/http"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// HandlePerformance handles GET and PUT /api/v1/performance.
//
// GET returns the current max_parallel_agents config and the effective
// (clamped) value actually in use.
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

func (a *restAPI) getPerformance(w http.ResponseWriter, _ *http.Request) {
	cfg := a.agentLoop.GetConfig()
	effective := cfg.Performance.EffectiveMaxParallelAgents()
	// max_parallel_agents in the response must satisfy the contract minimum (2).
	// A value of 0 means "unconfigured / auto" on disk; surface the effective
	// clamped value (always >= 2) so the wire payload is schema-valid and the UI
	// shows the concurrency actually in use.
	configured := cfg.Performance.MaxParallelAgents
	if configured < 2 {
		configured = effective
	}
	// tools_on_demand mirrors cfg.Tools.Manifest.Compressed:
	// true (default) = load tools on demand; false = all tools every message.
	toolsOnDemand := cfg.Tools.Manifest.Compressed
	jsonOK(w, gen.PerformanceSettings{
		MaxParallelAgents:          &configured,
		EffectiveMaxParallelAgents: &effective,
		ToolsOnDemand:              &toolsOnDemand,
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
		te.ResizeDispatchSema(newCfg.Performance.EffectiveMaxParallelAgents())
	}

	newCfg := a.agentLoop.GetConfig()
	configured := newCfg.Performance.MaxParallelAgents
	effective := newCfg.Performance.EffectiveMaxParallelAgents()
	toolsOnDemand := newCfg.Tools.Manifest.Compressed
	jsonOK(w, gen.PerformanceSettings{
		MaxParallelAgents:          &configured,
		EffectiveMaxParallelAgents: &effective,
		ToolsOnDemand:              &toolsOnDemand,
	})
}
