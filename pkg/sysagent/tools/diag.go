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

	"github.com/dapicom-ai/omnipus/pkg/security"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// ---- system.doctor.run ----

type DoctorRunTool struct{ deps *Deps }

func NewDoctorRunTool(d *Deps) *DoctorRunTool   { return &DoctorRunTool{deps: d} }
func (t *DoctorRunTool) Name() string           { return "run_doctor" }
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

// ---- get_usage ----

// UsageQueryTool implements the get_usage system tool.  It reads all sessions
// via deps.ListSessions, aggregates token usage using session.AggregateUsage,
// and returns a JSON report.  No dollar amounts are included.
type UsageQueryTool struct{ deps *Deps }

func NewUsageQueryTool(d *Deps) *UsageQueryTool  { return &UsageQueryTool{deps: d} }
func (t *UsageQueryTool) Name() string           { return "get_usage" }
func (t *UsageQueryTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *UsageQueryTool) Description() string {
	return "Query token usage by period, agent, model, or session.\n" +
		"Parameters:\n" +
		"  period   — time window: day, week, month (default), or all\n" +
		"  by       — grouping dimension: agent (default), model, or session\n" +
		"  agent_id — optional: restrict to a single agent\n" +
		"  session_id — optional: restrict to a single session\n" +
		"Returns input, output, cache-read, cache-write, and total token counts.\n" +
		"No dollar amounts — token counts only.\n" +
		"External CLI subagents (subagent_3p) run on a separate engine and are excluded."
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
	// Parse period (default: month).
	periodStr, _ := args["period"].(string)
	if periodStr == "" {
		periodStr = "month"
	}
	var period session.UsagePeriod
	switch periodStr {
	case "day":
		period = session.UsagePeriodDay
	case "week":
		period = session.UsagePeriodWeek
	case "month":
		period = session.UsagePeriodMonth
	case "all":
		period = session.UsagePeriodAll
	default:
		return tools.ErrorResult(errorJSON("INVALID_PARAM",
			"period must be one of: day, week, month, all",
			"Pass period=month to use the default monthly window."))
	}

	// Parse by (default: agent).
	byStr, _ := args["by"].(string)
	if byStr == "" {
		byStr = "agent"
	}
	var dim session.UsageDimension
	switch byStr {
	case "agent":
		dim = session.UsageDimensionAgent
	case "model":
		dim = session.UsageDimensionModel
	case "session":
		dim = session.UsageDimensionSession
	default:
		return tools.ErrorResult(errorJSON("INVALID_PARAM",
			"by must be one of: agent, model, session",
			"Pass by=agent to group by agent (the default)."))
	}

	// Optional filters.
	agentID, _ := args["agent_id"].(string)
	sessionID, _ := args["session_id"].(string)

	// Collect sessions.  Handle nil ListSessions gracefully.
	var metas []*session.UnifiedMeta
	if t.deps != nil && t.deps.ListSessions != nil {
		var errs []error
		metas, errs = t.deps.ListSessions()
		for _, e := range errs {
			// Non-fatal: log and proceed with partial data.
			slog.Warn("get_usage: partial session list error", "error", e)
		}
	}

	// Build NameResolver and Exclude from live config.
	var nameResolver func(string) string
	var excludeFn func(string) bool
	if t.deps != nil && t.deps.GetCfg != nil {
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
			for i := range cfg.Agents.List {
				a := &cfg.Agents.List[i]
				if a.ID != id {
					continue
				}
				// Exclude external CLI workers: Type=="worker" AND Subagents.Executor.Kind=="external-cli".
				if a.IsWorker() && a.Subagents != nil &&
					a.Subagents.Executor != nil && a.Subagents.Executor.Kind == "external-cli" {
					return true
				}
			}
			return false
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

	return tools.NewToolResult(successJSON(map[string]any{
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
	}))
}
