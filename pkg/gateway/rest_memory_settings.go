//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Memory settings endpoint (FR-019 / US-6).
//
// GET  /api/v1/settings/memory — read memory/recap/retention configuration
// PUT  /api/v1/settings/memory — write memory/recap/retention configuration
//
// Both routes are wrapped with withAuth (any authenticated user per A2/G-02).
// They read from / write to agents.defaults.* and storage.retention in
// config.json via safeUpdateConfigJSON (no SaveConfig — keys are preserved).

import (
	"log/slog"
	"net/http"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
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
	ret := cfg.Storage.Retention

	resp := gen.MemorySettings{
		AutoRecapEnabled:           boolPtr(d.AutoRecapEnabled),
		BootstrapRecapEnabled:      boolPtr(d.BootstrapRecapEnabled),
		BootstrapRecapMaxPerMinute: intPtr(d.BootstrapRecapMaxPerMinute),
		IdleTimeoutMinutes:         intPtr(d.IdleTimeoutMinutes),
		SessionDays:                intPtr(ret.RetentionSessionDays()),
		MemoryRetrosDays:           intPtr(ret.RetentionMemoryRetrosDays()),
	}
	// RecapModel: always emit a string (empty string when not configured).
	recapModel := d.RecapModel
	resp.RecapModel = &recapModel
	// RecapFallbackModels: translate config FallbackModel → wire FallbackModel.
	// Always emit an array (empty slice, not null) so the SPA gets [] rather than null.
	wireFallbacks := make([]gen.FallbackModel, 0, len(d.RecapFallbackModels))
	for _, fm := range d.RecapFallbackModels {
		wireFallbacks = append(wireFallbacks, gen.FallbackModel{
			Model:    fm.Model,
			Provider: &fm.Provider,
		})
	}
	resp.RecapFallbackModels = &wireFallbacks
	jsonOK(w, resp)
}

// putMemorySettings updates memory/recap/retention settings from the request body.
// Only fields present in the JSON body (non-null) are updated; absent fields are
// left unchanged (merge semantics).
func (a *restAPI) putMemorySettings(w http.ResponseWriter, r *http.Request) {
	var req gen.UpdateMemorySettingsJSONRequestBody
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "UpdateMemorySettingsJSONRequestBody", &req, validateEnabled) {
		return
	}

	// Validate values before touching disk.
	if req.BootstrapRecapMaxPerMinute != nil && *req.BootstrapRecapMaxPerMinute < 0 {
		jsonErr(w, http.StatusBadRequest, "bootstrap_recap_max_per_minute must be ≥ 0")
		return
	}
	if req.IdleTimeoutMinutes != nil && *req.IdleTimeoutMinutes < 0 {
		jsonErr(w, http.StatusBadRequest, "idle_timeout_minutes must be ≥ 0")
		return
	}
	if req.SessionDays != nil && *req.SessionDays < 0 {
		jsonErr(w, http.StatusBadRequest, "session_days must be ≥ 0")
		return
	}
	if req.MemoryRetrosDays != nil && *req.MemoryRetrosDays < 0 {
		jsonErr(w, http.StatusBadRequest, "memory_retros_days must be ≥ 0")
		return
	}

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error { //nolint:gocritic
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
				if fm.Provider != nil && *fm.Provider != "" {
					entry["provider"] = *fm.Provider
				}
				raw = append(raw, entry)
			}
			defaults["recap_fallback_models"] = raw
		}

		// storage.retention section. The canonical layout is top-level
		// "storage" → "retention" (config.OmnipusStorageConfig, json:"storage").
		// setRetentionFields writes only that path — there is no "try both keys"
		// fallback and no "omnipus" block alternative.
		if req.SessionDays != nil || req.MemoryRetrosDays != nil {
			if err := setRetentionFields(m, req.SessionDays, req.MemoryRetrosDays); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		slog.Error("rest: PUT /settings/memory: failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, "failed to update memory settings")
		return
	}

	// Return the updated effective settings by re-reading from the live config.
	a.getMemorySettings(w, r)
}

// setRetentionFields writes session_days and memory_retros_days into the raw
// config JSON map. The canonical layout is the top-level "storage" → "retention"
// (config.OmnipusStorageConfig, json:"storage").
func setRetentionFields(m map[string]any, sessionDays, memoryRetrosDays *int) error {
	storage, _ := m["storage"].(map[string]any)
	if storage == nil {
		storage = make(map[string]any)
		m["storage"] = storage
	}
	retention, _ := storage["retention"].(map[string]any)
	if retention == nil {
		retention = make(map[string]any)
		storage["retention"] = retention
	}
	if sessionDays != nil {
		retention["session_days"] = *sessionDays
	}
	if memoryRetrosDays != nil {
		retention["memory_retros_days"] = *memoryRetrosDays
	}
	return nil
}
