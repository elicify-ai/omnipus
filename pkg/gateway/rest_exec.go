//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
)

const (
	// Hard caps on allowlist size and pattern length to prevent DoS via
	// enormous pattern lists or pattern strings.
	maxAllowlistPatterns  = 256
	maxAllowlistPatternLn = 256
)

// HandleExecAllowlist handles GET/PUT /api/v1/security/exec-allowlist.
//
// GET returns the current exec allowlist and approval mode.
// PUT accepts {"allowed_binaries": [...]} and atomically updates config.json
// via safeUpdateConfigJSON so that the raw JSON map is mutated without
// destroying credential refs stored elsewhere in the config (see rest.go).
//
// This is the Wave 2 operator-facing control for SEC-05: binary allowlist.
// The list is evaluated on every exec call by ExecTool via PolicyAuditor.
// Changes are audit-logged (SEC-15) and take effect after agent loop restart
// per SEC-12 (security-critical policies are loaded once at startup).
func (a *restAPI) HandleExecAllowlist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := a.agentLoop.GetConfig()
		// Coerce nil slice to empty slice — never null (Ava-chat bug class).
		allowedBinaries := append([]string(nil), cfg.Tools.Exec.AllowedBinaries...)
		if allowedBinaries == nil {
			allowedBinaries = []string{}
		}
		approval := cfg.Tools.Exec.Approval
		restartRequired := false
		jsonOK(w, gen.ExecAllowlist{
			AllowedBinaries: allowedBinaries,
			Approval:        &approval,
			RestartRequired: &restartRequired,
		})
	case http.MethodPut:
		var body struct {
			AllowedBinaries []string `json:"allowed_binaries"`
		}
		validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
		if !decodeAndValidate(w, r, "ExecAllowlist", &body, validateEnabled) {
			return
		}
		// Normalise a nil slice to an empty slice so the config always contains
		// a concrete (if empty) array rather than omitting the field.
		if body.AllowedBinaries == nil {
			body.AllowedBinaries = []string{}
		}
		// Validate and normalise patterns: trim whitespace, reject empties,
		// dedupe, enforce hard caps. The evaluator trusts these patterns so
		// validation here is the enforcement boundary.
		sanitized, validationErr := sanitiseAllowlist(body.AllowedBinaries)
		if validationErr != nil {
			jsonErr(w, http.StatusBadRequest, validationErr.Error())
			return
		}
		if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
			toolsRaw, _ := m["tools"].(map[string]any)
			if toolsRaw == nil {
				toolsRaw = map[string]any{}
				m["tools"] = toolsRaw
			}
			execRaw, _ := toolsRaw["exec"].(map[string]any)
			if execRaw == nil {
				execRaw = map[string]any{}
				toolsRaw["exec"] = execRaw
			}
			// json.Marshal natively handles []string inside map[string]any,
			// and config.Config unmarshals it back to []string on reload —
			// no need for a manual []any conversion.
			execRaw["allowed_binaries"] = sanitized
			return nil
		}); err != nil {
			slog.Error("rest: update exec allowlist", "error", err)
			jsonErr(w, http.StatusInternalServerError, "could not save config")
			return
		}

		// SEC-15: Audit-log the policy change. The allowlist is a
		// security-critical config; every mutation must be recorded.
		if a.agentLoop != nil {
			if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
				if err := auditLogger.Log(&audit.Entry{
					Event:    audit.EventPolicyEval,
					Decision: audit.DecisionAllow,
					Details: map[string]any{
						"action":           "exec_allowlist_update",
						"pattern_count":    len(sanitized),
						"allowed_binaries": sanitized,
					},
				}); err != nil {
					slog.Warn("rest: audit log exec allowlist update", "error", err)
				}
			}
		}

		slog.Info("rest: exec allowlist updated", "pattern_count", len(sanitized))

		// Echo the sanitized, persisted list directly. Note that the in-memory
		// config is NOT hot-reloaded — changes take effect on next agent loop
		// restart per SEC-12. `restart_required: true` tells the UI to surface
		// a badge so operators are not confused about enforcement state.
		restartRequired := true
		jsonOK(w, gen.ExecAllowlist{
			AllowedBinaries: sanitized,
			RestartRequired: &restartRequired,
		})
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// sanitiseAllowlist trims, validates, and dedupes exec allowlist patterns.
// Returns an error when any pattern is empty/too-long or when the list
// exceeds maxAllowlistPatterns.
func sanitiseAllowlist(in []string) ([]string, error) {
	if len(in) > maxAllowlistPatterns {
		return nil, fmt.Errorf("too many patterns: %d (max %d)", len(in), maxAllowlistPatterns)
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for i, pat := range in {
		trimmed := strings.TrimSpace(pat)
		if trimmed == "" {
			return nil, fmt.Errorf("pattern at index %d is empty or whitespace-only", i)
		}
		if len(trimmed) > maxAllowlistPatternLn {
			return nil, fmt.Errorf(
				"pattern at index %d is too long: %d chars (max %d)",
				i,
				len(trimmed),
				maxAllowlistPatternLn,
			)
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out, nil
}
