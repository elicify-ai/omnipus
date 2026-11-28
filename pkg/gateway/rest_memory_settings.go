// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Memory settings endpoint (FR-019 / US-6).
//
// GET  /api/v1/settings/memory — read memory/recap configuration
// PUT  /api/v1/settings/memory — write memory/recap configuration
//
// Both routes are wrapped with withAuth (any authenticated user per A2/G-02).
// They read from / write to agents.defaults.* in config.json via
// safeUpdateConfigJSON (no SaveConfig — keys are preserved). Retention moved
// out (Settings redesign #1519, FR-045): a body carrying session_days or
// memory_retros_days is refused; retention is written only through
// PUT /api/v1/settings/retention.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// HandleMemorySettings dispatches GET and PUT /api/v1/settings/memory.
func (a *restAPI) HandleMemorySettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getMemorySettings(w, r)
	case http.MethodPut:
		a.putMemorySettings(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// getMemorySettings returns the current memory/recap/retention settings.
// It reads from the live config (a.agentLoop.GetConfig()) so the response
// always reflects the most recently written values.
func (a *restAPI) getMemorySettings(w http.ResponseWriter, _ *http.Request) {
	cfg := a.agentLoop.GetConfig()
	if cfg == nil {
		jsonErr(w, http.StatusInternalServerError, "config not available")
		return
	}

	d := cfg.Agents.Defaults

	resp := gen.MemorySettings{
		AutoRecapEnabled:           boolPtr(d.AutoRecapEnabled),
		BootstrapRecapEnabled:      boolPtr(d.BootstrapRecapEnabled),
		BootstrapRecapMaxPerMinute: intPtr(d.GetBootstrapRecapMaxPerMinute()),
		IdleTimeoutMinutes:         intPtr(d.GetIdleTimeoutMinutes()),
	}
	// RecapModel: always emit a string (empty string when not configured).
	recapModel := d.RecapModel
	resp.RecapModel = &recapModel
	// RecapFallbackModels: translate config FallbackModel → wire FallbackModel.
	// Always emit an array (empty slice, not null) so the SPA gets [] rather than null.
	wireFallbacks := make([]gen.FallbackModel, 0, len(d.RecapFallbackModels))
	for _, fm := range d.RecapFallbackModels {
		wireFallbacks = append(wireFallbacks, gen.FallbackModel{
			Model:           fm.Model,
			Provider:        &fm.Provider,
			ReasoningEffort: wireEffort(fm.ReasoningEffort),
		})
	}
	resp.RecapFallbackModels = &wireFallbacks
	applyRecapSettingsRead(&resp, &d)
	jsonOK(w, resp)
}

// putMemorySettings updates memory/recap/retention settings from the request body.
// Only fields present in the JSON body (non-null) are updated; absent fields are
// left unchanged (merge semantics).
func (a *restAPI) putMemorySettings(w http.ResponseWriter, r *http.Request) {
	var req gen.UpdateMemorySettingsJSONRequestBody
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return
	}
	// Retired retention fields are refused first, in both validation modes,
	// before anything in the body is applied (FR-045, review MAJ-004).
	if msg := retiredMemoryRetentionField(raw); msg != "" {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}
	// Effort members are checked on the raw body in both validation modes,
	// before any decode or write (FR-060, S-94).
	if refuseInvalidEffortBody(w, raw) {
		return
	}
	// The ranged keys are checked before schema validation too, so the
	// refusal names the key in both validation modes (S-81). A body that
	// does not decode is left to decodeAndValidate's own refusal.
	var ranged gen.MemorySettings
	if json.Unmarshal(raw, &ranged) == nil {
		if msg := recapLimitRefusal(&ranged); msg != "" {
			jsonErr(w, http.StatusBadRequest, msg)
			return
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	// The embedded inbound mirror carries this body as MemorySettings (the
	// request body is the same schema).
	if !decodeAndValidate(w, r, "MemorySettings", &req, validateEnabled) {
		return
	}

	// Validate values before touching disk: every ranged key (FR-074, S-81).
	if msg := recapLimitRefusal(&req); msg != "" {
		jsonErr(w, http.StatusBadRequest, msg)
		return
	}
	if req.RecapFallbackModels != nil {
		for i, fm := range *req.RecapFallbackModels {
			if strings.TrimSpace(fm.Model) == "" {
				jsonErr(w, http.StatusBadRequest, fmt.Sprintf("recap_fallback_models[%d]: model must not be empty", i))
				return
			}
		}
	}

	storedFallbacks := a.agentLoop.GetConfig().Agents.Defaults.RecapFallbackModels
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		// agents.defaults section.
		agents, _ := m["agents"].(map[string]any)
		if agents == nil {
			agents = make(map[string]any)
			m["agents"] = agents
		}
		defaults, _ := agents["defaults"].(map[string]any)
		if defaults == nil {
			defaults = make(map[string]any)
			agents["defaults"] = defaults
		}
		if req.AutoRecapEnabled != nil {
			defaults["auto_recap_enabled"] = *req.AutoRecapEnabled
		}
		if req.BootstrapRecapEnabled != nil {
			defaults["bootstrap_recap_enabled"] = *req.BootstrapRecapEnabled
		}
		if req.BootstrapRecapMaxPerMinute != nil {
			defaults["bootstrap_recap_max_per_minute"] = *req.BootstrapRecapMaxPerMinute
		}
		if req.IdleTimeoutMinutes != nil {
			defaults["idle_timeout_minutes"] = *req.IdleTimeoutMinutes
		}
		if req.RecapModel != nil {
			defaults["recap_model"] = *req.RecapModel
		}
		if req.RecapFallbackModels != nil {
			// Translate wire []FallbackModel → plain []map[string]any for JSON storage.
			raw := make([]map[string]any, 0, len(*req.RecapFallbackModels))
			for _, fm := range *req.RecapFallbackModels {
				entry := map[string]any{"model": fm.Model}
				provider := ""
				if fm.Provider != nil && *fm.Provider != "" {
					provider = *fm.Provider
					entry["provider"] = provider
				}
				if e := fallbackWithEffort(fm.Model, provider, fm.ReasoningEffort, storedFallbacks).ReasoningEffort; e != "" {
					entry["reasoning_effort"] = e
				}
				raw = append(raw, entry)
			}
			defaults["recap_fallback_models"] = raw
		}
		writeRecapSettings(defaults, &req)

		return nil
	}); err != nil {
		slog.Error("rest: PUT /settings/memory: failed", "error", err)
		if writeRequestRefusal(w, err) {
			return
		}
		jsonErr(w, http.StatusInternalServerError, "failed to update memory settings")
		return
	}

	// Return the updated effective settings by re-reading from the live config.
	a.getMemorySettings(w, r)
}

// retiredMemoryRetentionFields are the retention members MemorySettings no
// longer carries (FR-045). Matched the way encoding/json binds a key
// (jsonFoldKey), so a differently cased spelling is refused too.
var retiredMemoryRetentionFields = []string{"session_days", "memory_retros_days"}

// retiredMemoryRetentionField returns the 400 message for a body that carries
// a retired retention field, or "" when it carries none. A body that is not a
// JSON object is left to the regular decode and its own refusal.
func retiredMemoryRetentionField(raw []byte) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	for key := range fields {
		for _, retired := range retiredMemoryRetentionFields {
			if jsonFoldKey(key) == jsonFoldKey(retired) {
				return fmt.Sprintf("%s is no longer a memory setting: chat history and memory "+
					"retention are changed only through PUT /api/v1/settings/retention", key)
			}
		}
	}
	return ""
}
