// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/security"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// ---- system.doctor.run ----

type DoctorRunTool struct{ deps *Deps }

func NewDoctorRunTool(d *Deps) *DoctorRunTool   { return &DoctorRunTool{deps: d} }
func (t *DoctorRunTool) Name() string           { return "system.doctor.run" }
func (t *DoctorRunTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *DoctorRunTool) Description() string {
	return "Run security diagnostics and return a risk score (0-100) with actionable recommendations. No parameters required."
}

func (t *DoctorRunTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *DoctorRunTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	type issue struct {
		Severity       string `json:"severity"`
		Message        string `json:"message"`
		Recommendation string `json:"recommendation"`
	}

	var issues []issue
	checksPassed := 0
	checksFailed := 0

	// Check exec egress via the existing security.CheckExecEgress function.
	// ExecProxyEnabled reflects the cfg.Tools.Exec.EnableProxy flag (SEC-28
	// wiring, Wave 3). The agent loop starts/stops the proxy based on this
	// field; diagnostics read the same field so the doctor output matches
	// the enforcement state the operator configured.
	currentCfg := t.deps.GetCfg()
	execCfg := security.DiagnosticConfig{
		// Tools are always registered; policy (allow/ask/deny) decides invocation.
		ExecToolEnabled:     true,
		ExecProxyEnabled:    currentCfg.Tools.Exec.EnableProxy,
		ExecAllowedBinaries: currentCfg.Tools.Exec.AllowedBinaries,
	}
	for _, w := range security.CheckExecEgress(execCfg) {
		issues = append(issues, issue{
			Severity:       "high",
			Message:        w.Message,
			Recommendation: fmt.Sprintf("Address %s by reviewing your security settings", w.Code),
		})
		checksFailed++
	}

	// Check credentials file permissions.
	credPath := filepath.Join(t.deps.Home, "credentials.json")
	if info, err := os.Stat(credPath); err == nil {
		if info.Mode()&0o077 != 0 {
			issues = append(issues, issue{
				Severity:       "high",
				Message:        "credentials.json is world/group-readable (permissions too open)",
				Recommendation: "Run: chmod 600 ~/.omnipus/credentials.json",
			})
			checksFailed++
		} else {
			checksPassed++
		}
	}

	// Check config file permissions.
	if info, err := os.Stat(t.deps.ConfigPath); err == nil {
		if info.Mode()&0o077 != 0 {
			issues = append(issues, issue{
				Severity:       "medium",
				Message:        "config.json is world/group-readable",
				Recommendation: "Run: chmod 600 ~/.omnipus/config.json",
			})
			checksFailed++
		} else {
			checksPassed++
		}
	}

	// Check audit log directory.
	auditDir := filepath.Join(t.deps.Home, "system")
	if _, err := os.Stat(auditDir); os.IsNotExist(err) {
		issues = append(issues, issue{
			Severity:       "medium",
			Message:        "Audit log directory ~/.omnipus/system/ does not exist",
			Recommendation: "Restart Omnipus to recreate the directory structure",
		})
		checksFailed++
	} else {
		checksPassed++
	}

	total := checksPassed + checksFailed
	if total == 0 {
		total = 1
	}
	riskScore := checksFailed * 100 / total

	return tools.NewToolResult(successJSON(map[string]any{
		"risk_score":    riskScore,
		"issues":        issues,
		"checks_passed": checksPassed,
		"checks_failed": checksFailed,
		"run_at":        time.Now().UTC().Format(time.RFC3339),
	}))
}

// ---- system.backup.create ----

type BackupCreateTool struct{ deps *Deps }

func NewBackupCreateTool(d *Deps) *BackupCreateTool { return &BackupCreateTool{deps: d} }
func (t *BackupCreateTool) Name() string            { return "system.backup.create" }
func (t *BackupCreateTool) Scope() tools.ToolScope  { return tools.ScopeCore }
func (t *BackupCreateTool) Description() string {
	return "[NOT IMPLEMENTED] Creating a backup archive of the data directory is not yet built. " +
		"This tool always returns a NOT_IMPLEMENTED error and does NOT create any file."
}

func (t *BackupCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"encrypt": map[string]any{"type": "boolean"}},
	}
}

func (t *BackupCreateTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return tools.ErrorResult(errorJSON("NOT_IMPLEMENTED",
		"system.backup.create is not implemented: no archive is produced.",
		"Back up the data directory manually (copy ~/.omnipus/) until this tool is built."))
}

// ---- system.cost.query ----

type CostQueryTool struct{ deps *Deps }

func NewCostQueryTool(d *Deps) *CostQueryTool   { return &CostQueryTool{deps: d} }
func (t *CostQueryTool) Name() string           { return "system.cost.query" }
func (t *CostQueryTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *CostQueryTool) Description() string {
	return "[NOT IMPLEMENTED] Querying LLM cost data by period/agent is not yet built. " +
		"The only cost data Omnipus persists today is a single running total for the current UTC day " +
		"(no historical periods, per-agent breakdown, or grouping). This tool always returns a NOT_IMPLEMENTED error."
}

func (t *CostQueryTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"period":     map[string]any{"type": "string"},
			"start_date": map[string]any{"type": "string"},
			"end_date":   map[string]any{"type": "string"},
			"agent_id":   map[string]any{"type": "string"},
			"group_by":   map[string]any{"type": "string"},
		},
	}
}

func (t *CostQueryTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return tools.ErrorResult(errorJSON("NOT_IMPLEMENTED",
		"system.cost.query is not implemented: no per-period cost store exists. "+
			"Omnipus persists only a single running total for the current UTC day.",
		"View today's spend in the UI; a queryable cost-history store is not yet built."))
}
