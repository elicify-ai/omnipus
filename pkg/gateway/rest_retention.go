// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
)

// The former GET/PUT /api/v1/security/retention route is removed (Settings
// redesign #1519, FR-047): retention is written only through
// PUT /api/v1/settings/retention (rest_settings_retention.go). The on-demand
// sweep below stays unchanged.

// HandleRetentionSweep handles POST /api/v1/security/retention/sweep.
//
// Triggers an on-demand retention sweep. Acquires retentionSweepMu via
// TryLock; if the nightly sweep is already in progress, responds 409 immediately.
// When retention is disabled (config.storage.retention.disabled = true) the
// sweep is skipped and the response includes skipped_reason="disabled".
// Gated by adminWrap (withAuth → RequireNotBypass); dev_mode_bypass returns 503.
// Emits audit with resource="storage.retention.sweep".
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
		jsonServerFailure(w, http.StatusInternalServerError, "sweep failed", err)
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
