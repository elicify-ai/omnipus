// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---- system.doctor.run ----

type DoctorRunTool struct{ deps *Deps }

func NewDoctorRunTool(d *Deps) *DoctorRunTool   { return &DoctorRunTool{deps: d} }
func (t *DoctorRunTool) Name() string           { return "run_doctor" }
func (t *DoctorRunTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *DoctorRunTool) Description() string {
	return "Run a narrow set of security diagnostic checks (exec egress config, credentials.json/config.json " +
		"file permissions, audit log directory presence) and return checks_failed_pct — the percentage of " +
		"THESE FEW checks that failed — plus actionable recommendations. This is NOT a comprehensive security " +
		"audit: it never looks at sandbox.mode, god-mode, dev_mode_bypass, or Landlock/seccomp availability, " +
		"so an install with every real protection disabled can still score 0% (\"no risk\"). A low " +
		"checks_failed_pct means only these specific checks passed, not that the install is secure. No " +
		"parameters required."
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
	checksFailedPct := checksFailed * 100 / total

	return tools.NewToolResult(successJSON(map[string]any{
		"checks_failed_pct": checksFailedPct,
		"issues":            issues,
		"checks_passed":     checksPassed,
		"checks_failed":     checksFailed,
		"run_at":            time.Now().UTC().Format(time.RFC3339),
	}))
}

// ---- get_usage ----

// UsageQueryTool implements the get_usage system tool.  It reads all sessions
// via deps.ListSessions, aggregates token usage using session.AggregateUsage,
// and returns a JSON report.  No dollar amounts are included.
//
// ADR-057 U25 verification note (W16i, [grill2 C2-1]): deps.ListSessions kept
// its pre-existing zero-arg func() ([]*session.UnifiedMeta, []error) shape
// across U9's AgentLoop.ListAllSessions pagination rewrite — see
// Deps.ListSessions' doc comment (deps.go) for why — so this call site
// requires NO change. It transitively benefits from the production wiring's
// flat=true choice (pkg/gateway/gateway.go's u25AllSessionsForUsage): every
// session, including delegated children, is included in this tool's
// aggregates (FR-104).
type UsageQueryTool struct{ deps *Deps }

func NewUsageQueryTool(d *Deps) *UsageQueryTool  { return &UsageQueryTool{deps: d} }
func (t *UsageQueryTool) Name() string           { return "get_usage" }
func (t *UsageQueryTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *UsageQueryTool) Description() string {
	return "Query token usage, cost, and spend by period, agent, model, or session.\n" +
		"Parameters:\n" +
		"  period   — time window: day, week, month (default), or all\n" +
		"  by       — grouping dimension: agent (default), model, or session\n" +
		"  agent_id — optional: restrict to a single agent\n" +
		"  session_id — optional: restrict to a single session\n" +
		"Returns input, output, cache-read, cache-write, and total token counts.\n" +
		"No dollar amounts — token counts only.\n" +
		"External CLI subagents (subagent_3p) run on a separate engine and are excluded.\n" +
		"If some sessions could not be read the response sets partial=true with " +
		"partial_error_count — treat the totals as a lower bound in that case."
}

func (t *UsageQueryTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"period": map[string]any{
				"type":        "string",
				"enum":        []string{"day", "week", "month", "all"},
				"description": "Time window to aggregate (default: month)",
			},
			"by": map[string]any{
				"type":        "string",
				"enum":        []string{"agent", "model", "session"},
				"description": "Grouping dimension (default: agent)",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "Optional: restrict report to a single agent ID",
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "Optional: restrict report to a single session ID",
			},
		},
	}
}

func (t *UsageQueryTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	// Nil ListSessions is a production wiring bug — fail loudly rather than
	// returning an empty report indistinguishable from genuine zero usage.
	if t.deps == nil || t.deps.ListSessions == nil {
		return tools.ErrorResult(errorJSON(
			"USAGE_UNAVAILABLE",
			"usage data source is not available",
			"The session store is not wired to the get_usage tool. This is a server-side configuration issue; contact your administrator.",
		))
	}

	// Parse period via the authoritative constructor (empty → month; unrecognized → error).
	periodStr, _ := args["period"].(string)
	period, ok := session.ParseUsagePeriod(periodStr)
	if !ok {
		return tools.ErrorResult(errorJSON("INVALID_PARAM",
			"period must be one of: day, week, month, all",
			"Pass period=month to use the default monthly window."))
	}
	if periodStr == "" {
		periodStr = string(period) // normalise for the response field
	}

	// Parse by via the authoritative constructor (empty → agent; unrecognized → error).
	byStr, _ := args["by"].(string)
	dim, ok := session.ParseUsageDimension(byStr)
	if !ok {
		return tools.ErrorResult(errorJSON("INVALID_PARAM",
			"by must be one of: agent, model, session",
			"Pass by=agent to group by agent (the default)."))
	}
	if byStr == "" {
		byStr = string(dim) // normalise for the response field
	}

	// Optional filters.
	agentID, _ := args["agent_id"].(string)
	sessionID, _ := args["session_id"].(string)

	// Collect sessions; surface partial errors in the report.
	metas, errs := t.deps.ListSessions()
	for _, e := range errs {
		slog.Warn("get_usage: partial session list error", "error", e)
	}

	// Build NameResolver and Exclude from live config.
	var nameResolver func(string) string
	var excludeFn func(string) bool
	if t.deps.GetCfg != nil {
		cfg := t.deps.GetCfg()
		nameResolver = func(id string) string {
			for i := range cfg.Agents.List {
				if cfg.Agents.List[i].ID == id {
					return cfg.Agents.List[i].Name
				}
			}
			return ""
		}
		excludeFn = func(id string) bool {
			return cfg.IsExternalCLIWorkerID(id)
		}
	}

	report := session.AggregateUsage(metas, session.UsageOptions{
		Period:       period,
		Now:          time.Now().UTC(),
		Dimension:    dim,
		AgentID:      agentID,
		SessionID:    sessionID,
		NameResolver: nameResolver,
		Exclude:      excludeFn,
	})

	// Build the breakdown slice.
	type bucketJSON struct {
		Key        string `json:"key"`
		Label      string `json:"label"`
		In         int    `json:"in"`
		Out        int    `json:"out"`
		CacheRead  int    `json:"cache_read"`
		CacheWrite int    `json:"cache_write"`
		Total      int    `json:"total"`
	}
	breakdown := make([]bucketJSON, 0, len(report.Buckets))
	for _, b := range report.Buckets {
		breakdown = append(breakdown, bucketJSON{
			Key:        b.Key,
			Label:      b.Label,
			In:         b.In,
			Out:        b.Out,
			CacheRead:  b.CacheRead,
			CacheWrite: b.CacheWrite,
			Total:      b.Total,
		})
	}

	periodStart := ""
	if !report.PeriodStart.IsZero() {
		periodStart = report.PeriodStart.Format(time.RFC3339)
	}

	result := map[string]any{
		"period":       periodStr,
		"by":           byStr,
		"period_start": periodStart,
		"period_end":   report.PeriodEnd.Format(time.RFC3339),
		"total": map[string]any{
			"in":          report.Total.In,
			"out":         report.Total.Out,
			"cache_read":  report.Total.CacheRead,
			"cache_write": report.Total.CacheWrite,
			"total":       report.Total.Total,
		},
		"breakdown": breakdown,
	}
	// Surface partial data to the calling agent so it can caveat its answer.
	if len(errs) > 0 {
		result["partial"] = true
		result["partial_error_count"] = len(errs)
	}

	return tools.NewToolResult(successJSON(result))
}
