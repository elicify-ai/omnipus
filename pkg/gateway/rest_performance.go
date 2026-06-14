// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"net/http"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
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
	configured := cfg.Performance.MaxParallelAgents
	effective := cfg.Performance.EffectiveMaxParallelAgents()
	jsonOK(w, gen.PerformanceSettings{
		MaxParallelAgents:          &configured,
		EffectiveMaxParallelAgents: &effective,
	})
}

func (a *restAPI) putPerformance(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var req gen.PerformanceSettingsUpdate
	if !decodeAndValidate(w, r, "PerformanceSettingsUpdate", &req, validateEnabled) {
		return
	}

	if req.MaxParallelAgents == nil {
		jsonErr(w, http.StatusBadRequest, "max_parallel_agents is required")
		return
	}
	requested := *req.MaxParallelAgents
	// Accept 0 as "reset to auto-detect"; values < 0 are rejected.
	if requested < 0 {
		jsonErr(w, http.StatusBadRequest, "max_parallel_agents must be >= 0")
		return
	}

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		perf := ensureMap(m, "performance")
		perf["max_parallel_agents"] = requested
		return nil
	}); err != nil {
		jsonErr(w, http.StatusInternalServerError,
			fmt.Sprintf("could not update performance settings: %v", err))
		return
	}

	// Resize the in-memory dispatch semaphore immediately so the new cap takes
	// effect without a restart.
	te := agent.GetTaskExecutor(a.agentLoop)
	if te != nil {
		newCfg := a.agentLoop.GetConfig()
		te.ResizeDispatchSema(newCfg.Performance.EffectiveMaxParallelAgents())
	}

	newCfg := a.agentLoop.GetConfig()
	configured := newCfg.Performance.MaxParallelAgents
	effective := newCfg.Performance.EffectiveMaxParallelAgents()
	jsonOK(w, gen.PerformanceSettings{
		MaxParallelAgents:          &configured,
		EffectiveMaxParallelAgents: &effective,
	})
}
