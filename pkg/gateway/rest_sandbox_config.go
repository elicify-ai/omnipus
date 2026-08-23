// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	sandboxpkg "github.com/elicify-ai/omnipus/pkg/sandbox"
)

// SandboxConfigUpdate request body is defined in
// contracts/components/schemas/SandboxConfigUpdate.yaml and generated into
// pkg/api/generated/. Use gen.SandboxConfigUpdate directly in putSandboxConfig.

// SandboxConfigUpdate.Ssrf (nested) is inlined in the generated type;
// see contracts/components/schemas/SandboxConfigUpdate.yaml.

// validFilesystemModels is the canonical set accepted for filesystem_model.
// ADR-062 defines exactly two; anything else is a caller error, not a value to
// coerce into a default.
var validFilesystemModels = map[string]bool{"confined": true, "open": true}

// validSandboxModes is the canonical set accepted by putSandboxConfig.
var validSandboxModes = map[string]bool{
	"off":        true,
	"permissive": true,
	"enforce":    true,
}

// HandleSandboxConfig handles GET/PUT /api/v1/security/sandbox-config.
//
// GET returns the full sandbox config. See pkg/gateway/rest_sandbox_config_test.go
// for the exact response and request shapes.
//
// PUT accepts a partial body — any subset of
// {mode, allow_network_outbound, allowed_paths, ssrf_enabled,
// ssrf_allow_internal, ssrf.allow_internal}. On validation success each
// changed field is persisted atomically via safeUpdateConfigJSON.
// mode and allowed_paths are restart-gated (requires_restart=true).
// ssrf.allow_internal is hot-reload (requires_restart=false).
//
// Gated by adminWrap (withAuth → RequireNotBypass); dev_mode_bypass returns 503.
func (a *restAPI) HandleSandboxConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getSandboxConfig(w, r)
	case http.MethodPut:
		a.putSandboxConfig(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *restAPI) getSandboxConfig(w http.ResponseWriter, r *http.Request) {
	if a.agentLoop == nil {
		jsonErr(w, http.StatusServiceUnavailable, "sandbox: agent loop not initialized")
		return
	}
	cfg := a.agentLoop.GetConfig()

	// Coerce nil slices to empty slices — never null (Ava-chat bug class).
	allowedPaths := append([]string(nil), cfg.Sandbox.AllowedPaths...)
	if allowedPaths == nil {
		allowedPaths = []string{}
	}
	allowInternal := append([]string(nil), cfg.Sandbox.SSRF.AllowInternal...)
	if allowInternal == nil {
		allowInternal = []string{}
	}
	shellDenyPatterns := append([]string(nil), cfg.Sandbox.ShellDenyPatterns...)
	if shellDenyPatterns == nil {
		shellDenyPatterns = []string{}
	}

	// applied_mode reflects what the gateway is ACTUALLY running with. It
	// differs from mode when the operator saved a change but hasn't restarted.
	applied := ""
	if a.sandboxResult != nil {
		applied = string(a.sandboxResult.ApplyState.Mode)
	}

	// Collect scalar values into pointers for the generated type.
	resolvedMode := gen.SandboxConfigMode(cfg.Sandbox.ResolvedMode())
	allowNetOut := cfg.Sandbox.AllowNetworkOutbound
	ssrfEnabled := cfg.Sandbox.SSRF.Enabled

	// O14 god-mode state for the UI. enabled is reported false when god mode is
	// unavailable (the switch is inert), so the UI never shows "on" for a setting
	// that has no effect.
	godModeAvail := a.godModeAvailable()
	godModeOn := godModeAvail && cfg.Sandbox.GodMode

	// ADR-068 §6: the in-process bash path guard, reported separately from the
	// kernel sandbox (mode) because they are different boundaries and the UI
	// must not merge them — conflating the two is UAT defect 002 itself.
	//
	// Report the RESOLVED value (Agents.Defaults.RestrictToWorkspace), not the
	// raw tri-state pointer: applyWorkspacePathGuard has already settled the
	// env > key > default precedence there, so this is the value actually in
	// force. Reading the pointer would show nil (=> nothing) on a fresh install
	// whose effective setting is true.
	workspacePathGuard := cfg.Agents.Defaults.RestrictToWorkspace
	// When the env hatch is set it outranks any saved value, so a write from
	// the UI would persist and change nothing until restart. Say so rather than
	// offering a control that silently does nothing.
	workspaceGuardEnvLocked := config.WorkspacePathGuardEnvLocked()

	// Return both the flat-field shape and the nested ssrf object.
	// The flat fields are the canonical wire format; the nested ssrf block is
	// included for backward-compatible clients. Both are safe to include — JSON
	// consumers pick what they need.
	// Report the CONFIGURED model, resolved through the same parser the boot
	// path uses (ParseFilesystemModel with the confined default), so the value
	// shown is the one that would take effect — not a raw string that may be
	// empty or misspelled in config.json.
	fsModel, _ := sandboxpkg.ParseFilesystemModel(
		cfg.Sandbox.FilesystemModel, sandboxpkg.FilesystemModelConfined)
	resolvedFsModel := gen.SandboxConfigFilesystemModel(fsModel)

	jsonOK(w, gen.SandboxConfig{
		Mode:                 &resolvedMode,
		FilesystemModel:      &resolvedFsModel,
		AllowNetworkOutbound: &allowNetOut,
		AllowedPaths:         &allowedPaths,
		SsrfEnabled:          &ssrfEnabled,
		SsrfAllowInternal:    &allowInternal,
		AppliedMode:          &applied,
		ShellDenyPatterns:    &shellDenyPatterns,
		GodMode:              &godModeOn,
		GodModeAvailable:     &godModeAvail,

		WorkspacePathGuard:            &workspacePathGuard,
		WorkspacePathGuardEnvOverride: &workspaceGuardEnvLocked,
		// Nested ssrf object for backward-compatible clients.
		Ssrf: &struct {
			AllowInternal *[]string `json:"allow_internal,omitempty"`
			Enabled       *bool     `json:"enabled,omitempty"`
		}{
			Enabled:       &ssrfEnabled,
			AllowInternal: &allowInternal,
		},
	})
}

func (a *restAPI) putSandboxConfig(w http.ResponseWriter, r *http.Request) {
	// Re-auth gate (Spec-6 FR-12.2): a sandbox-config mutation is a sensitive
	// HTTP-layer security change and requires the single-use re-auth consent
	// token — the same gate the Integrations PUT enforces. RequireNotBypass
	// (already in adminWrap) is a 503 dev-mode guard, NOT this consent check; the
	// two are layered. The user is guaranteed in context here (admin-wrapped).
	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if !a.requireReAuth(w, r, user.Username) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var body gen.SandboxConfigUpdate
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SandboxConfigUpdate", &body, validateEnabled) {
		return
	}

	// Resolve which fields are being updated. Flat ssrf_allow_internal takes
	// precedence over nested ssrf.allow_internal when both are present.
	changedMode := body.Mode != nil
	changedAllowNetworkOutbound := body.AllowNetworkOutbound != nil
	changedAllowedPaths := body.AllowedPaths != nil
	changedSSRFEnabled := body.SsrfEnabled != nil

	// Resolve allow_internal source: flat field takes precedence over nested.
	var resolvedAllowInternal *[]string
	if body.SsrfAllowInternal != nil {
		resolvedAllowInternal = body.SsrfAllowInternal
	} else if body.Ssrf != nil && body.Ssrf.AllowInternal != nil {
		resolvedAllowInternal = body.Ssrf.AllowInternal
	}
	changedAllowInternal := resolvedAllowInternal != nil
	changedShellDenyPatterns := body.ShellDenyPatterns != nil
	changedFilesystemModel := body.FilesystemModel != nil
	changedWorkspacePathGuard := body.WorkspacePathGuard != nil

	if !changedMode && !changedAllowNetworkOutbound && !changedAllowedPaths &&
		!changedSSRFEnabled && !changedAllowInternal && !changedShellDenyPatterns &&
		!changedFilesystemModel && !changedWorkspacePathGuard {
		jsonErr(
			w,
			http.StatusBadRequest,
			"at least one field required — expected mode, filesystem_model, allowed_paths, ssrf.allow_internal, shell_deny_patterns, or workspace_path_guard",
		)
		return
	}

	// Validate mode value before any disk writes.
	if changedMode {
		if !validSandboxModes[string(*body.Mode)] {
			jsonErr(w, http.StatusBadRequest, `invalid sandbox mode — must be one of "off", "permissive", "enforce"`)
			return
		}
	}

	if changedFilesystemModel {
		if !validFilesystemModels[string(*body.FilesystemModel)] {
			jsonErr(w, http.StatusBadRequest,
				`invalid filesystem_model — must be "confined" or "open"`)
			return
		}
	}

	// Strict validation — one bad entry fails the whole PUT, nothing persists.
	if changedAllowedPaths {
		if err := validateAllowedPaths(*body.AllowedPaths); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	var ssrfWarnings []string
	if changedAllowInternal {
		warnings, err := validateSSRFAllowInternal(*resolvedAllowInternal)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		ssrfWarnings = warnings
	}
	if changedShellDenyPatterns {
		if err := validateShellDenyPatterns(*body.ShellDenyPatterns); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// Capture old values for auditing inside the safeUpdateConfigJSON callback
	// so the snapshot is taken atomically with the write. Reading before the
	// lock can yield a stale value when two writers race.
	var (
		oldMode              string
		oldAllowedPaths      []string
		oldAllowInternal     []string
		oldShellDenyPatterns []string
		// Defaults to true so an audit entry for the first-ever write records
		// the value that was actually in force (the fail-closed default), not
		// Go's zero value, which would read as "it was off" when it was on.
		oldWorkspacePathGuard = true
	)

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		sandbox := ensureMap(m, "sandbox")

		// Snapshot old values from the just-read map so the audit diff is
		// consistent with the actual atomic state, not a pre-lock race copy.
		if changedMode {
			if s, ok := sandbox["mode"].(string); ok {
				oldMode = s
			}
			sandbox["mode"] = string(*body.Mode)
			// Strip any stale legacy "enabled" bool from older configs so
			// the on-disk shape matches the current schema.
			delete(sandbox, "enabled")
		}
		if changedFilesystemModel {
			// Restart-gated for the same reason as mode: the kernel profile was
			// built from this value at boot and is not rebuilt in place, so
			// applying it live would leave config and enforcement disagreeing —
			// the precise condition the restart gate exists to prevent.
			sandbox["filesystem_model"] = string(*body.FilesystemModel)
		}
		if changedAllowNetworkOutbound {
			sandbox["allow_network_outbound"] = *body.AllowNetworkOutbound
		}
		if changedAllowedPaths {
			if raw, ok := sandbox["allowed_paths"].([]any); ok {
				for _, v := range raw {
					if s, ok := v.(string); ok {
						oldAllowedPaths = append(oldAllowedPaths, s)
					}
				}
			}
			sandbox["allowed_paths"] = toAnySlice(*body.AllowedPaths)
		}
		if changedSSRFEnabled || changedAllowInternal {
			ssrf := ensureMap(m, "sandbox", "ssrf")
			if changedSSRFEnabled {
				ssrf["enabled"] = *body.SsrfEnabled
			}
			if changedAllowInternal {
				if raw, ok := ssrf["allow_internal"].([]any); ok {
					for _, v := range raw {
						if s, ok := v.(string); ok {
							oldAllowInternal = append(oldAllowInternal, s)
						}
					}
				}
				ssrf["allow_internal"] = toAnySlice(*resolvedAllowInternal)
			}
		}
		if changedShellDenyPatterns {
			if raw, ok := sandbox["shell_deny_patterns"].([]any); ok {
				for _, v := range raw {
					if s, ok := v.(string); ok {
						oldShellDenyPatterns = append(oldShellDenyPatterns, s)
					}
				}
			}
			sandbox["shell_deny_patterns"] = toAnySlice(*body.ShellDenyPatterns)
		}
		// ADR-068 §6. Restart-gated: applyWorkspacePathGuard resolves this into
		// AgentDefaults.RestrictToWorkspace at boot, and the guard reads that
		// resolved value, so a running gateway keeps its current setting until
		// restarted. Persisted as an explicit bool, never removed on false —
		// the tri-state nil means "unset, use the default", which is NOT the
		// same as an operator deliberately choosing false.
		if changedWorkspacePathGuard {
			if prev, ok := sandbox["workspace_path_guard"].(bool); ok {
				oldWorkspacePathGuard = prev
			}
			sandbox["workspace_path_guard"] = *body.WorkspacePathGuard
		}
		return nil
	}); err != nil {
		slog.Error("rest: update sandbox config", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not save config")
		return
	}

	// FR-053 invalidation fires inside safeUpdateConfigJSON above.
	// Audit each changed field. Errors are logged, never surface to the
	// caller — the mutation has already been persisted atomically.
	if a.agentLoop != nil {
		if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
			if changedMode {
				if err := audit.EmitSecuritySettingChange(
					r.Context(), auditLogger,
					"sandbox.mode",
					oldMode, string(*body.Mode),
				); err != nil {
					slog.Error("rest: audit emit sandbox.mode change", "error", err)
				}
			}
			if changedAllowedPaths {
				if err := audit.EmitSecuritySettingChange(
					r.Context(), auditLogger,
					"sandbox.allowed_paths",
					oldAllowedPaths, *body.AllowedPaths,
				); err != nil {
					slog.Error("rest: audit emit allowed_paths change", "error", err)
				}
			}
			if changedAllowInternal {
				if err := audit.EmitSecuritySettingChange(
					r.Context(), auditLogger,
					"sandbox.ssrf.allow_internal",
					oldAllowInternal, *resolvedAllowInternal,
				); err != nil {
					slog.Error("rest: audit emit ssrf.allow_internal change", "error", err)
				}
			}
			if changedShellDenyPatterns {
				if err := audit.EmitSecuritySettingChange(
					r.Context(), auditLogger,
					"sandbox.shell_deny_patterns",
					oldShellDenyPatterns, *body.ShellDenyPatterns,
				); err != nil {
					slog.Error("rest: audit emit shell_deny_patterns change", "error", err)
				}
			}
			if changedWorkspacePathGuard {
				if err := audit.EmitSecuritySettingChange(
					r.Context(), auditLogger,
					"sandbox.workspace_path_guard",
					oldWorkspacePathGuard, *body.WorkspacePathGuard,
				); err != nil {
					slog.Error("rest: audit emit workspace_path_guard change", "error", err)
				}
			}
		}
	}

	// Wildcard SSRF entries (0.0.0.0/0, ::/0) validate successfully but
	// effectively disable internal-block protection. Log each one with
	// the actor username so security review catches the divergence.
	if len(ssrfWarnings) > 0 {
		actor := actorUsername(r)
		for _, warn := range ssrfWarnings {
			slog.Warn("ssrf: wildcard allow_internal accepted",
				"event", "ssrf_wildcard_accepted",
				"entry", warn,
				"actor", actor)
		}
	}

	// mode and allowed_paths are restart-gated (each is consumed at boot or
	// agent-wiring time). ssrf.allow_internal and shell_deny_patterns are
	// hot-reload via the config-poll loop.
	partialRestartRequired := changedMode || changedAllowedPaths || changedWorkspacePathGuard

	// Return the updated config so the UI can cache-update without a follow-up GET.
	// Include both flat fields and nested ssrf object for backward-compatible clients.
	saved := true
	if a.agentLoop != nil {
		updatedCfg := a.agentLoop.GetConfig()
		updatedAllowedPaths := append([]string(nil), updatedCfg.Sandbox.AllowedPaths...)
		if updatedAllowedPaths == nil {
			updatedAllowedPaths = []string{}
		}
		updatedAllowInternal := append([]string(nil), updatedCfg.Sandbox.SSRF.AllowInternal...)
		if updatedAllowInternal == nil {
			updatedAllowInternal = []string{}
		}
		updatedApplied := ""
		if a.sandboxResult != nil {
			updatedApplied = string(a.sandboxResult.ApplyState.Mode)
		}
		updatedShellDenyPatterns := append([]string(nil), updatedCfg.Sandbox.ShellDenyPatterns...)
		if updatedShellDenyPatterns == nil {
			updatedShellDenyPatterns = []string{}
		}
		updatedMode := gen.SandboxConfigMode(updatedCfg.Sandbox.ResolvedMode())
		updatedAllowNetOut := updatedCfg.Sandbox.AllowNetworkOutbound
		updatedSsrfEnabled := updatedCfg.Sandbox.SSRF.Enabled
		jsonOK(w, gen.SandboxConfig{
			Saved:                &saved,
			Mode:                 &updatedMode,
			AllowNetworkOutbound: &updatedAllowNetOut,
			AllowedPaths:         &updatedAllowedPaths,
			SsrfEnabled:          &updatedSsrfEnabled,
			SsrfAllowInternal:    &updatedAllowInternal,
			AppliedMode:          &updatedApplied,
			ShellDenyPatterns:    &updatedShellDenyPatterns,
			RequiresRestart:      &partialRestartRequired,
			Ssrf: &struct {
				AllowInternal *[]string `json:"allow_internal,omitempty"`
				Enabled       *bool     `json:"enabled,omitempty"`
			}{
				Enabled:       &updatedSsrfEnabled,
				AllowInternal: &updatedAllowInternal,
			},
		})
		return
	}

	// Fallback when agentLoop is nil (test harness or startup race).
	jsonOK(w, gen.SandboxConfig{
		Saved:           &saved,
		RequiresRestart: &partialRestartRequired,
	})
}

// toAnySlice converts []string to []any so the JSON map round-trip
// through safeUpdateConfigJSON produces a stable shape (map[string]any
// parse would turn a []string marshal into []any anyway — doing it
// here keeps the mutation function honest).
func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// actorUsername pulls the authenticated username from the request's
// context, falling back to "" when no user is attached (system-initiated
// or test request). Mirrors the extractor in pkg/audit but lives here so
// the gateway does not need to import audit internals.
func actorUsername(r *http.Request) string {
	if r == nil {
		return ""
	}
	v := r.Context().Value(ctxkey.UserContextKey{})
	if v == nil {
		return ""
	}
	if u, ok := v.(*config.UserConfig); ok && u != nil {
		return u.Username
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
