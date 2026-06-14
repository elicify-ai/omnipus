//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// TestGetPerformance_ZeroConfig_SchemaValid is a regression test for the UAT
// finding that GET /api/v1/performance returned max_parallel_agents:0 on a
// fresh install (unconfigured), violating the PerformanceSettings contract
// minimum of 2 and tripping the SPA's zod edge-validation. The handler must
// surface the effective clamped value (always >= 2) when the on-disk value is
// the "auto/unset" zero.
func TestGetPerformance_ZeroConfig_SchemaValid(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/performance", nil)
	w := httptest.NewRecorder()
	api.HandlePerformance(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/performance: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp gen.PerformanceSettings
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	// Contract: PerformanceSettings.max_parallel_agents has minimum 2.
	if resp.MaxParallelAgents == nil || *resp.MaxParallelAgents < 2 {
		t.Fatalf("max_parallel_agents must be >= 2 (contract minimum) even when unconfigured; got %v", resp.MaxParallelAgents)
	}
	if resp.EffectiveMaxParallelAgents == nil || *resp.EffectiveMaxParallelAgents < 2 {
		t.Fatalf("effective_max_parallel_agents must be >= 2; got %v", resp.EffectiveMaxParallelAgents)
	}
}
