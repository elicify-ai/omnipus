//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// HandleRetention handles GET and PUT /api/v1/security/retention.
//
// GET returns the current retention config:
//
//	{"session_days": int, "disabled": bool}
//
// PUT accepts a partial body — any subset of {session_days, disabled}.
// Both fields are strictly typed: session_days must be a non-negative integer
// (floats and strings rejected with 400), disabled must be a JSON boolean
// (string "true"/"false" rejected with 400). An empty body {} is accepted as
// a no-op (200, values unchanged).
//
// Changes are hot-reload (requires_restart: false) — the nightly goroutine
// re-reads cfg fresh every tick.
//
// Admin-only: non-admin PUT returns 403. Emits audit with resource="storage.retention".
func (a *restAPI) HandleRetention(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getRetention(w, r)
	case http.MethodPut:
		a.putRetention(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *restAPI) getRetention(w http.ResponseWriter, r *http.Request) {
	cfg := a.agentLoop.GetConfig()
	ret := cfg.Storage.Retention
	// Use pointers so both fields are always present in the JSON response.
	sessionDays := ret.SessionDays
	disabled := ret.Disabled
	jsonOK(w, gen.RetentionConfig{
		SessionDays: &sessionDays,
		Disabled:    &disabled,
	})
}

func (a *restAPI) putRetention(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	// decodeAndValidate validates the body against the RetentionUpdateRequest
	// JSON Schema (when validate_inbound=true) before decoding. The schema
	// enforces type:integer for session_days and type:boolean for disabled;
	// the bespoke parseRetentionSessionDays / parseRetentionDisabled checks
	// below provide defense-in-depth regardless of the validate_inbound flag.
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	var raw map[string]json.RawMessage
	if !decodeAndValidate(w, r, "RetentionUpdateRequest", &raw, validateEnabled) {
		return
	}

	var newSessionDays *int
	var newDisabled *bool

	if v, ok := raw["session_days"]; ok {
		days, err := parseRetentionSessionDays(v)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		newSessionDays = &days
	}

	if v, ok := raw["disabled"]; ok {
		b, err := parseRetentionDisabled(v)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		newDisabled = &b
	}

	// Empty body is accepted as a no-op.
	if newSessionDays == nil && newDisabled == nil {
		cfg := a.agentLoop.GetConfig()
		ret := cfg.Storage.Retention
		jsonOK(w, gen.RetentionUpdateResponse{
			Saved:           true,
			RequiresRestart: false,
			SessionDays:     ret.SessionDays,
			Disabled:        ret.Disabled,
		})
		return
	}

	oldCfg := a.agentLoop.GetConfig()
	oldRet := oldCfg.Storage.Retention

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		retention := ensureMap(m, "storage", "retention")
		if newSessionDays != nil {
			retention["session_days"] = *newSessionDays
		}
		if newDisabled != nil {
			retention["disabled"] = *newDisabled
		}
		return nil
	}); err != nil {
		slog.Error("rest: update retention config", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not save config")
		return
	}

	newCfg := a.agentLoop.GetConfig()
	newRet := newCfg.Storage.Retention

	if a.agentLoop != nil {
		if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
			if err := audit.EmitSecuritySettingChange(
				r.Context(),
				auditLogger,
				"storage.retention",
				map[string]any{
					"session_days": oldRet.SessionDays,
					"disabled":     oldRet.Disabled,
				},
				map[string]any{
					"session_days": newRet.SessionDays,
					"disabled":     newRet.Disabled,
				},
			); err != nil {
				slog.Error("rest: audit emit retention change", "error", err)
			}
		}
	}

	slog.Info("rest: retention config updated",
		"session_days", newRet.SessionDays,
		"disabled", newRet.Disabled,
	)

	jsonOK(w, gen.RetentionUpdateResponse{
		Saved:           true,
		RequiresRestart: false,
		SessionDays:     newRet.SessionDays,
		Disabled:        newRet.Disabled,
	})
}

// HandleRetentionSweep handles POST /api/v1/security/retention/sweep.
//
// Triggers an on-demand retention sweep. Acquires retentionSweepMu via
// TryLock; if the nightly sweep is already in progress, responds 409 immediately.
// When retention is disabled (config.storage.retention.disabled = true) the
// sweep is skipped and the response includes skipped_reason="disabled".
// Admin-only; emits audit with resource="storage.retention.sweep".
func (a *restAPI) HandleRetentionSweep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.postRetentionSweep(w, r)
}

func (a *restAPI) postRetentionSweep(w http.ResponseWriter, r *http.Request) {
	if !retentionSweepMu.TryLock() {
		jsonErr(w, http.StatusConflict, "sweep in progress")
		return
	}
	defer retentionSweepMu.Unlock()

	cfg := a.agentLoop.GetConfig()
	ret := cfg.Storage.Retention

	if ret.IsDisabled() {
		skippedReason := "disabled"
		jsonOK(w, gen.RetentionSweepResult{
			Removed:       0,
			SkippedReason: &skippedReason,
		})
		return
	}

	store := a.agentLoop.GetSessionStore()
	if store == nil {
		jsonErr(w, http.StatusInternalServerError, "sweep failed: session store unavailable")
		return
	}

	days := ret.RetentionSessionDays()
	removed, err := store.RetentionSweep(days)
	if err != nil {
		slog.Error("rest: on-demand retention sweep failed", "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("sweep failed: %s", err.Error()))
		return
	}

	if a.agentLoop != nil {
		if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
			if auditErr := audit.EmitSecuritySettingChange(
				r.Context(),
				auditLogger,
				"storage.retention.sweep",
				map[string]any{"days": days},
				map[string]any{"removed": removed},
			); auditErr != nil {
				slog.Error("rest: audit emit retention sweep", "error", auditErr)
			}
		}
	}

	slog.Info("rest: on-demand retention sweep completed", "removed", removed, "days", days)

	jsonOK(w, gen.RetentionSweepResult{
		Removed: removed,
	})
}

// parseRetentionSessionDays decodes a JSON raw value as a strict non-negative integer.
// Rejects: JSON strings, null, floats with fractional parts, and negative values.
func parseRetentionSessionDays(raw json.RawMessage) (int, error) {
	if len(raw) > 0 && (raw[0] == '"' || string(raw) == "null") {
		return 0, fmt.Errorf("session_days: must be a non-negative integer")
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("session_days: must be a non-negative integer")
	}
	i, intErr := n.Int64()
	if intErr == nil {
		if i < 0 {
			return 0, fmt.Errorf("session_days: must be >= 0")
		}
		return int(i), nil
	}
	// If Int64 failed, check for float (fractional).
	f, floatErr := n.Float64()
	if floatErr != nil {
		return 0, fmt.Errorf("session_days: value overflows int")
	}
	if f != float64(int64(f)) {
		return 0, fmt.Errorf("session_days: must be an integer, not a float")
	}
	// It's a whole number that failed Int64 — overflow.
	return 0, fmt.Errorf("session_days: value overflows int")
}

// parseRetentionDisabled decodes a JSON raw value as a strict bool.
// Rejects strings, numbers, and null — only JSON true/false are accepted.
func parseRetentionDisabled(raw json.RawMessage) (bool, error) {
	if len(raw) == 0 {
		return false, fmt.Errorf("disabled: must be a boolean")
	}
	// Only JSON literal true/false are valid. Strings ("true"), numbers (1), and
	// null are rejected.
	if raw[0] == '"' || raw[0] == '{' || raw[0] == '[' || string(raw) == "null" {
		return false, fmt.Errorf("disabled: must be a boolean (true or false), not a string or null")
	}
	// Attempt to parse as a number — reject that too.
	if raw[0] >= '0' && raw[0] <= '9' || raw[0] == '-' {
		return false, fmt.Errorf("disabled: must be a boolean (true or false), not a number")
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, fmt.Errorf("disabled: must be a boolean (true or false)")
	}
	return b, nil
}
