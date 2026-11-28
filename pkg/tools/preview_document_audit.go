// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/preview"
)

// previewRunAuditEvent is the audit record of one completed document preview.
// It carries the two run disclosures the result text also shows (office
// preview spec AD-7 "converted without sandbox", §3.1 D1.4 "memory limit not
// enforced"), so an operator can find unconfined or unlimited runs later.
const previewRunAuditEvent = "preview_document.run"

func emitPreviewRunAudit(ctx context.Context, auditLog *audit.Logger, toolName, source string, res *preview.Result) {
	if auditLog == nil || res == nil {
		return
	}
	details := map[string]any{
		"path":     source,
		"validity": res.Validity,
	}
	if res.Unconfined != "" {
		details["converted_without_sandbox"] = res.Unconfined
	}
	if res.MemoryUnenforced != "" {
		details["memory_limit_not_enforced"] = res.MemoryUnenforced
	}
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     previewRunAuditEvent,
		Decision:  audit.DecisionAllow,
		AgentID:   ToolAgentID(ctx),
		SessionID: ToolTranscriptSessionID(ctx),
		Tool:      toolName,
		Details:   adjudicationAuditDetails(ctx, details),
	}
	if err := auditLog.Log(entry); err != nil {
		slog.Error("preview_document: run audit log write failed",
			"error", err, "session_id", entry.SessionID, "agent_id", entry.AgentID, "path", source)
	}
}
