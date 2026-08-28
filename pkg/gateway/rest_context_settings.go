// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Context-budget settings endpoint (ADR-066 D9, FR-036 / US-11).
//
// GET  /api/v1/settings/context — read the per-surface caps, the absolute
//                                 trigger, the ingest bound, the global
//                                 default window and the per-(provider,
//                                 model) window overrides.
// PUT  /api/v1/settings/context — partial update of the same.
//
// Both routes are wrapped with withAuth (any authenticated user), matching
// the /settings/memory precedent named by the contract — deliberately NOT
// RequireNotBypass.
//
// The PUT writes through safeUpdateConfigJSON (raw-map read-modify-write, so
// sibling config keys and secrets survive) and then triggers a registry
// reload and waits for it, so the next turn resolves windows and caps from
// the new values without a restart. This is the ONLY write path for
// ContextSettings.ModelOverrides — the D2 rung-2 override that the
// context_window_unknown refusal points a local-provider operator at.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// contextCapCeiling is the contract's shared ceiling for the three D4
// per-surface caps (ContextSettings.yaml: maximum 150000).
const contextCapCeiling = 150_000

// contextIngestBoundCeiling is the exclusive upper bound for
// ingest_bound_bytes: it must stay strictly below 0.8 × the archive line
// size (D10).
const contextIngestBoundCeiling = 8_388_608

// HandleContextSettings dispatches GET and PUT /api/v1/settings/context.
func (a *restAPI) HandleContextSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getContextSettings(w, r)
	case http.MethodPut:
		a.putContextSettings(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// contextSettingsWire projects the stored config.ContextSettings onto the
// generated wire type. WarnThreshold is config-internal and deliberately not
// on the wire (spec §1.1).
func contextSettingsWire(cs config.ContextSettings) gen.ContextSettings {
	overrides := make([]gen.ContextModelOverride, 0, len(cs.ModelOverrides))
	for _, o := range cs.ModelOverrides {
		overrides = append(overrides, gen.ContextModelOverride{
			Provider:      o.Provider,
			Model:         o.Model,
			ContextWindow: o.ContextWindow,
		})
	}
	out := gen.ContextSettings{
		McpResultCap:         cs.McpResultCap,
		BuiltinSuccessCap:    cs.BuiltinSuccessCap,
		BuiltinFailureCap:    cs.BuiltinFailureCap,
		AbsoluteTriggerChars: cs.AbsoluteTriggerChars,
		IngestBoundBytes:     cs.IngestBoundBytes,
		ModelOverrides:       overrides,
	}
	if cs.DefaultContextWindow != nil {
		v := *cs.DefaultContextWindow
		out.DefaultContextWindow = &v
	}
	return out
}

// getContextSettings returns the live context-budget settings.
func (a *restAPI) getContextSettings(w http.ResponseWriter, _ *http.Request) {
	cfg := a.agentLoop.GetConfig()
	if cfg == nil {
		jsonErr(w, http.StatusInternalServerError, "config not available")
		return
	}
	jsonOK(w, contextSettingsWire(cfg.Context))
}

// validateContextSettingsUpdate enforces the contract's 400 rules, each
// message naming the field and the limit it violated.
func validateContextSettingsUpdate(req *gen.ContextSettingsUpdate) string {
	caps := []struct {
		name string
		val  *int
	}{
		{"mcp_result_cap", req.McpResultCap},
		{"builtin_success_cap", req.BuiltinSuccessCap},
		{"builtin_failure_cap", req.BuiltinFailureCap},
	}
	for _, c := range caps {
		if c.val == nil {
			continue
		}
		if *c.val < 1 {
			return fmt.Sprintf("%s must be ≥ 1", c.name)
		}
		if *c.val > contextCapCeiling {
			return fmt.Sprintf("%s must be ≤ %d", c.name, contextCapCeiling)
		}
	}
	if req.AbsoluteTriggerChars != nil && *req.AbsoluteTriggerChars < 1 {
		return "absolute_trigger_chars must be ≥ 1"
	}
	if req.IngestBoundBytes != nil {
		if *req.IngestBoundBytes < 1 {
			return "ingest_bound_bytes must be ≥ 1"
		}
		if *req.IngestBoundBytes >= contextIngestBoundCeiling {
			return fmt.Sprintf("ingest_bound_bytes must be < %d", contextIngestBoundCeiling)
		}
	}
	if req.DefaultContextWindow != nil && *req.DefaultContextWindow < 1 {
		return "default_context_window must be ≥ 1 (send null to clear it)"
	}
	if req.ModelOverrides != nil {
		for i, o := range *req.ModelOverrides {
			if o.ContextWindow < 1 {
				return fmt.Sprintf("model_overrides[%d].context_window must be ≥ 1", i)
			}
			if strings.TrimSpace(o.Provider) == "" {
				return fmt.Sprintf("model_overrides[%d].provider must not be empty", i)
			}
			if strings.TrimSpace(o.Model) == "" {
				return fmt.Sprintf("model_overrides[%d].model must not be empty", i)
			}
		}
	}
	return ""
}

// pruneDeadOverrides drops the rows whose provider no longer exists — the
// same predicate ResolveWindow applies when it ignores them (US-1.AC10).
func pruneDeadOverrides(cfg *config.Config, rows []config.ContextModelOverride) []config.ContextModelOverride {
	kept := make([]config.ContextModelOverride, 0, len(rows))
	for _, o := range rows {
		if !agent.WindowProviderKnown(cfg, o.Provider) {
			slog.Info("rest: PUT /settings/context: pruned a model override for an unknown provider",
				"provider", o.Provider, "model", o.Model)
			continue
		}
		kept = append(kept, o)
	}
	return kept
}

// putContextSettings applies a partial ContextSettingsUpdate. An omitted
// field is unchanged; model_overrides, when present, replaces the whole
// list; default_context_window explicitly null clears it.
func (a *restAPI) putContextSettings(w http.ResponseWriter, r *http.Request) {
	cfg := a.agentLoop.GetConfig()
	if cfg == nil {
		jsonErr(w, http.StatusInternalServerError, "config not available")
		return
	}

	// The raw body is needed to distinguish "default_context_window absent"
	// (leave alone) from "default_context_window: null" (clear it) — the
	// generated struct collapses both to a nil *int. Same raw-body-peek
	// pattern updateAgent uses for sandbox_profile / delegation_policy; the
	// body is restored so decodeAndValidate below still sees it.
	rawBody, readErr := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if readErr != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(rawBody))
	clearsDefaultWindow := false
	var peek map[string]json.RawMessage
	if json.Unmarshal(rawBody, &peek) == nil {
		if v, present := peek["default_context_window"]; present &&
			string(bytes.TrimSpace(v)) == "null" {
			clearsDefaultWindow = true
		}
	}

	var req gen.ContextSettingsUpdate
	validateEnabled := cfg.Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "ContextSettingsUpdate", &req, validateEnabled) {
		return
	}

	if msg := validateContextSettingsUpdate(&req); msg != "" {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}

	// Start from the live values so an omitted field is genuinely unchanged.
	next := cfg.Context
	if req.McpResultCap != nil {
		next.McpResultCap = *req.McpResultCap
	}
	if req.BuiltinSuccessCap != nil {
		next.BuiltinSuccessCap = *req.BuiltinSuccessCap
	}
	if req.BuiltinFailureCap != nil {
		next.BuiltinFailureCap = *req.BuiltinFailureCap
	}
	if req.AbsoluteTriggerChars != nil {
		next.AbsoluteTriggerChars = *req.AbsoluteTriggerChars
	}
	if req.IngestBoundBytes != nil {
		next.IngestBoundBytes = *req.IngestBoundBytes
	}
	switch {
	case req.DefaultContextWindow != nil:
		v := *req.DefaultContextWindow
		next.DefaultContextWindow = &v
	case clearsDefaultWindow:
		next.DefaultContextWindow = nil
	}
	if req.ModelOverrides != nil {
		rows := make([]config.ContextModelOverride, 0, len(*req.ModelOverrides))
		for _, o := range *req.ModelOverrides {
			rows = append(rows, config.ContextModelOverride{
				Provider:      strings.TrimSpace(o.Provider),
				Model:         strings.TrimSpace(o.Model),
				ContextWindow: o.ContextWindow,
			})
		}
		next.ModelOverrides = rows
	}
	// Prune on every write, incoming list or not (contract: "overrides whose
	// provider no longer exists are pruned on write").
	next.ModelOverrides = pruneDeadOverrides(cfg, next.ModelOverrides)

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		ctxSection := ensureMap(m, "context")
		ctxSection["mcp_result_cap"] = next.McpResultCap
		ctxSection["builtin_success_cap"] = next.BuiltinSuccessCap
		ctxSection["builtin_failure_cap"] = next.BuiltinFailureCap
		ctxSection["absolute_trigger_chars"] = next.AbsoluteTriggerChars
		ctxSection["ingest_bound_bytes"] = next.IngestBoundBytes
		if next.DefaultContextWindow != nil {
			ctxSection["default_context_window"] = *next.DefaultContextWindow
		} else {
			delete(ctxSection, "default_context_window")
		}
		rows := make([]map[string]any, 0, len(next.ModelOverrides))
		for _, o := range next.ModelOverrides {
			rows = append(rows, map[string]any{
				"provider":       o.Provider,
				"model":          o.Model,
				"context_window": o.ContextWindow,
			})
		}
		ctxSection["model_overrides"] = rows
		return nil
	}); err != nil {
		slog.Error("rest: PUT /settings/context: failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to update context settings")
		return
	}

	// Every 200 write triggers a registry reload so the next turn resolves
	// windows and caps from the new values without a restart.
	if err := a.triggerReloadAndWait(); err != nil {
		slog.Error("rest: PUT /settings/context: reload failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "context settings written but the reload failed")
		return
	}

	a.getContextSettings(w, r)
}
