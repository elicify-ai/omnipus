// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
)

// rest_rate_limits.go — rate-limits endpoint.
//
// GET  /api/v1/security/rate-limits — returns current per-agent sliding-window
//                                      rate-limit config.
// PUT  /api/v1/security/rate-limits — partial update to the same.
//
// TokenBudget is the sole app-level spend brake; see pkg/agent/budget.go (D12 / R§8.3).
// This endpoint handles ONLY per-agent sliding-window rate limits (LLM/hr, tool/min).
//
// PUT requires authentication only (single-user model). Strict type
// validation rejects JSON strings in numeric fields, floats in integer
// fields, negative values, NaN/Inf, and overflow.
// Changes are hot-reloaded (requires_restart: false) via the 2-second config
// poll in the agent loop.

// HandleRateLimits handles GET and PUT /api/v1/security/rate-limits.
func (a *restAPI) HandleRateLimits(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getRateLimits(w, r)
	case http.MethodPut:
		a.putRateLimits(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// getRateLimits returns the current per-agent sliding-window rate-limit config.
func (a *restAPI) getRateLimits(w http.ResponseWriter, r *http.Request) {
	rlCfg := a.agentLoop.GetConfig().Sandbox.RateLimits
	enabled := rlCfg.MaxAgentLLMCallsPerHour > 0 ||
		rlCfg.MaxAgentToolCallsPerMinute > 0

	jsonOK(w, gen.RateLimitsResponse{
		Enabled:                    enabled,
		MaxAgentLlmCallsPerHour:    int64(rlCfg.MaxAgentLLMCallsPerHour),
		MaxAgentToolCallsPerMinute: int64(rlCfg.MaxAgentToolCallsPerMinute),
	})
}

// putRateLimits is the body of PUT /api/v1/security/rate-limits.
// withAuth has already confirmed the bearer token is valid — single-user
// model, no additional role gate.
func (a *restAPI) putRateLimits(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	// decodeAndValidate validates the body against the RateLimitsUpdateRequest
	// JSON Schema (when validate_inbound=true) before decoding. When the schema
	// reports type:integer for LLM/tool count fields, fractional floats are
	// rejected at the schema level; the bespoke parseInt64Field check below
	// provides defense-in-depth regardless.
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var raw map[string]json.RawMessage
	if !decodeAndValidate(w, r, "RateLimitsUpdateRequest", &raw, validateEnabled) {
		return
	}

	// ADR-053 D12: reject any daily_cost_cap_usd field. The SEC-26 USD cap
	// was retired; TokenBudget (set via /api/v1/settings/token-budget) is
	// the sole app-level spend brake. Operators who set this field get a
	// clear 400 instead of a silent no-op.
	//
	// SECURITY: do NOT echo the raw field value back into the response body.
	// MaxBytesReader caps the body at 1<<20 — a crafted payload could land up
	// to ~1 MB of arbitrary content inside the 400. The field name + ADR
	// cite is enough for operators to find the migration path.
	if _, ok := raw["daily_cost_cap_usd"]; ok {
		jsonErr(w, http.StatusBadRequest,
			"daily_cost_cap_usd: SEC-26 USD cap retired per ADR-053 D12; "+
				"use /api/v1/settings/token-budget to set the app-level OVERALL token budget")
		return
	}

	// Parse and validate each present field.
	var newLLM, newTool *int64

	if v, ok := raw["max_agent_llm_calls_per_hour"]; ok {
		i, err := parseInt64Field("max_agent_llm_calls_per_hour", v)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		newLLM = &i
	}

	if v, ok := raw["max_agent_tool_calls_per_minute"]; ok {
		i, err := parseInt64Field("max_agent_tool_calls_per_minute", v)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		newTool = &i
	}

	// Snapshot old values for the audit entry IMMEDIATELY before the mutation
	// (snapshot ordering: read → mutate → reload → audit). Holding the snapshot
	// across the prepare hook would race with concurrent REST config writes that
	// mutate the same Sandbox.RateLimits map via safeUpdateConfigJSON; reading
	// right before the call closes that TOCTOU window.
	oldCfg := a.agentLoop.GetConfig().Sandbox.RateLimits

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		rl := ensureMap(m, "sandbox", "rate_limits")
		if newLLM != nil {
			rl["max_agent_llm_calls_per_hour"] = *newLLM
		}
		if newTool != nil {
			rl["max_agent_tool_calls_per_minute"] = *newTool
		}
		return nil
	}); err != nil {
		slog.Error("rest: update rate limits", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not save config")
		return
	}

	// Try hot-reload; on failure the config is persisted but the live AgentLoop
	// still holds the old values until the operator restarts.
	reloadErr := a.triggerReloadAndWait()
	newCfg := a.agentLoop.GetConfig().Sandbox.RateLimits

	// Emit the audit ONCE, after safeUpdateConfigJSON succeeds, regardless of
	// reload outcome. The audit entry is the operator-facing record of the
	// persisted change — not of the runtime effect — so it must fire even when
	// the hot-reload fails (the operator still needs to know what was saved).
	if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
		if err := audit.EmitSecuritySettingChange(
			r.Context(), auditLogger, "sandbox.rate_limits",
			map[string]any{
				"max_agent_llm_calls_per_hour":    oldCfg.MaxAgentLLMCallsPerHour,
				"max_agent_tool_calls_per_minute": oldCfg.MaxAgentToolCallsPerMinute,
			},
			map[string]any{
				"max_agent_llm_calls_per_hour":    newCfg.MaxAgentLLMCallsPerHour,
				"max_agent_tool_calls_per_minute": newCfg.MaxAgentToolCallsPerMinute,
			},
		); err != nil {
			slog.Error("rest: audit log rate limits update", "error", err)
		}
	}

	if reloadErr != nil {
		slog.Warn("rest: rate limits saved but hot-reload failed; restart required",
			"reload_error", reloadErr,
			"max_agent_llm_calls_per_hour", newCfg.MaxAgentLLMCallsPerHour,
			"max_agent_tool_calls_per_minute", newCfg.MaxAgentToolCallsPerMinute,
		)
		warnMsg := "config saved to disk but hot-reload failed; restart the gateway to apply"
		jsonOK(w, gen.RateLimitsUpdateResponse{
			Saved:           true,
			RequiresRestart: true,
			Warning:         &warnMsg,
		})
		return
	}

	slog.Info("rest: rate limits updated",
		"max_agent_llm_calls_per_hour", newCfg.MaxAgentLLMCallsPerHour,
		"max_agent_tool_calls_per_minute", newCfg.MaxAgentToolCallsPerMinute,
	)

	llmCalls := int64(newCfg.MaxAgentLLMCallsPerHour)
	toolCalls := int64(newCfg.MaxAgentToolCallsPerMinute)
	jsonOK(w, gen.RateLimitsUpdateResponse{
		Saved:           true,
		RequiresRestart: false,
		Applied: &struct {
			MaxAgentLlmCallsPerHour    *int64 `json:"max_agent_llm_calls_per_hour,omitempty"`
			MaxAgentToolCallsPerMinute *int64 `json:"max_agent_tool_calls_per_minute,omitempty"`
		}{
			MaxAgentLlmCallsPerHour:    &llmCalls,
			MaxAgentToolCallsPerMinute: &toolCalls,
		},
	})
}

// parseInt64Field decodes a JSON raw value as a strict int64.
// Rejects: JSON strings, null, floats with fractional parts, negative values,
// and values exceeding math.MaxInt64.
func parseInt64Field(name string, raw json.RawMessage) (int64, error) {
	// Reject JSON strings and null.
	if len(raw) > 0 && (raw[0] == '"' || string(raw) == "null") {
		return 0, fmt.Errorf("%s: must be a non-negative integer", name)
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("%s: must be a non-negative integer", name)
	}
	// Attempt exact integer parse first.
	i, intErr := n.Int64()
	if intErr == nil {
		if i < 0 {
			return 0, fmt.Errorf("%s: must be >= 0 (0 = unlimited)", name)
		}
		return i, nil
	}
	// If Int64() failed, check whether the value is a float.
	f, floatErr := n.Float64()
	if floatErr != nil {
		// Neither int nor float — token is probably too large.
		return 0, fmt.Errorf("%s: value overflows int64", name)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%s: NaN and Inf are not allowed", name)
	}
	// If f > MaxInt64 it's an overflow.
	if f > math.MaxInt64 {
		return 0, fmt.Errorf("%s: value overflows int64", name)
	}
	// It parsed as a float but not as int64 — must be a fractional value.
	return 0, fmt.Errorf("%s: must be an integer, not a float", name)
}
