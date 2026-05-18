//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"log/slog"
	"net/http"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// validSkillTrustLevels is the closed set of accepted values for sandbox.skill_trust.
// Comparison is exact (case-sensitive): no silent normalization.
var validSkillTrustLevels = [3]string{
	string(config.SkillTrustBlockUnverified),
	string(config.SkillTrustWarnUnverified),
	string(config.SkillTrustAllowAll),
}

// skillTrustInvalidMsg is returned verbatim in 400 responses so the caller sees
// all three canonical values in a single error.
const skillTrustInvalidMsg = `level must be exactly one of: "block_unverified", "warn_unverified", "allow_all"`

// HandleSkillTrust handles GET/PUT /api/v1/security/skill-trust.
//
// GET returns the current skill trust level from config.
// PUT accepts {"level": string} and persists sandbox.skill_trust via safeUpdateConfigJSON.
// Only the three canonical values are accepted; any other string (including
// case-variants) is rejected with 400 — no silent normalization.
//
// Response shape (PUT 200):
//
//	{"saved": true, "requires_restart": false, "applied_level": "<level>"}
//
// When level=="allow_all" the response also includes:
//
//	{"warning": "skill_trust=allow_all disables hash verification — configure a trusted skills registry instead"}
func (a *restAPI) HandleSkillTrust(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := a.agentLoop.GetConfig()
		level := string(cfg.Sandbox.SkillTrust)
		if level == "" {
			level = string(config.SkillTrustWarnUnverified)
		}
		jsonOK(w, gen.SkillTrustResponse{
			Level: gen.SkillTrustResponseLevel(level),
		})

	case http.MethodPut:
		a.putSkillTrust(w, r)

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// putSkillTrust is the admin-only body of PUT /api/v1/security/skill-trust.
// Called only after RequireAdmin has confirmed the caller holds the admin role.
func (a *restAPI) putSkillTrust(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var body gen.SkillTrustUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SkillTrustUpdateRequest", &body, validateEnabled) {
		return
	}

	if string(body.Level) == "" {
		jsonErr(w, http.StatusBadRequest, skillTrustInvalidMsg)
		return
	}

	valid := false
	for _, v := range validSkillTrustLevels {
		if string(body.Level) == v {
			valid = true
			break
		}
	}
	if !valid {
		jsonErr(w, http.StatusBadRequest, skillTrustInvalidMsg)
		return
	}

	cfg := a.agentLoop.GetConfig()
	oldLevel := string(cfg.Sandbox.SkillTrust)
	if oldLevel == "" {
		oldLevel = string(config.SkillTrustWarnUnverified)
	}

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		ensureMap(m, "sandbox")["skill_trust"] = string(body.Level)
		return nil
	}); err != nil {
		slog.Error("rest: update skill trust", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not save config")
		return
	}

	if a.agentLoop != nil {
		if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
			if err := audit.EmitSecuritySettingChange(
				r.Context(),
				auditLogger,
				"sandbox.skill_trust",
				oldLevel,
				string(body.Level),
			); err != nil {
				slog.Error("rest: audit emit skill trust change", "error", err)
			}
		}
	}

	slog.Info("rest: skill trust updated", "level", string(body.Level))

	resp := gen.SkillTrustUpdateResponse{
		Saved:           true,
		RequiresRestart: false,
		AppliedLevel:    gen.SkillTrustUpdateResponseAppliedLevel(body.Level),
	}
	if string(body.Level) == string(config.SkillTrustAllowAll) {
		warnMsg := fmt.Sprintf(
			"skill_trust=%s disables hash verification — configure a trusted skills registry instead",
			string(body.Level),
		)
		resp.Warning = &warnMsg
	}
	jsonOK(w, resp)
}
