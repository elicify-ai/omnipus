// Package tools — path-guard audit emission helpers ( / ).
//
// This file owns the audit-logging side-effect that every file/exec tool's
// Execute path emits when validatePathWithAllowPaths (or the equivalent
// shell.go cwd / guard-command path check) rejects a request. The shape of
// each entry follows — fields go into Entry.Details (no top-level
// schema change to pkg/audit.Entry).
//
// Reason heuristic ( — caller side, not validator side):
//
//	if validator-error mentions "symlink" → "symlink_escape"
//	else if len(allowPaths) > 0 → "not_in_allow_list"
//	else → "outside_workspace"
//
// Threat assumption: the audit logger is best-effort. A nil logger means the
// tool was constructed before SetAuditLogger ran (test boot ordering, or
// audit logging disabled via cfg.Sandbox.AuditLog=false). In either case we
// must NOT panic and MUST NOT change enforcement — the deny still propagates
// to the LLM as an ErrorResult.

package tools

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// PathAccessDeniedEvent is the canonical event name used by every
// path-guard rejection emitted from a tool's Execute path. Centralized here
// so audit-grep tooling and downstream consumers have a single literal to
// match.
const PathAccessDeniedEvent = "path.access_denied"

// Reason discriminators for path.access_denied entries.
const (
	ReasonOutsideWorkspace = "outside_workspace"
	ReasonNotInAllowList   = "not_in_allow_list"
	ReasonSymlinkEscape    = "symlink_escape"
)

// classifyPathDenialReason picks the audit reason for a path-guard error.
//
// `validatorErr` is the error returned by validatePathWithAllowPaths or by
// the shell guardCommand path checker. `allowPathsLen` is the number of
// configured AllowReadPaths/AllowWritePaths entries — it discriminates
// "outside workspace, no allow-list configured" from "outside workspace,
// allow-list configured but no entry matched".
//
// Pure function — no side effects, safe to call without locks.
func classifyPathDenialReason(validatorErr error, allowPathsLen int) string {
	if validatorErr == nil {
		// Defensive: caller is supposed to gate this on a non-nil error.
		// Fall back to the most common reason rather than panicking.
		return ReasonOutsideWorkspace
	}
	msg := strings.ToLower(validatorErr.Error())
	if strings.Contains(msg, "symlink") {
		return ReasonSymlinkEscape
	}
	if allowPathsLen > 0 {
		return ReasonNotInAllowList
	}
	return ReasonOutsideWorkspace
}

// canonicalDeniedPath returns the path that should be logged in the audit
// entry. It tries filepath.EvalSymlinks first (per wording —
// "canonical path post-EvalSymlinks if reachable"); on failure it falls
// back to a lexically-cleaned absolute path. It never returns "" — even
// for empty input it returns "." so the entry has a non-empty path field.
func canonicalDeniedPath(path string) string {
	if path == "" {
		return "."
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

// emitPathAccessDenied writes one path.access_denied audit entry on a
// path-guard rejection. The entry shape follows :
//
//	Event: "path.access_denied"
//	Decision: "deny"
//	AgentID: from ctx (ToolAgentID)
//	SessionID: from ctx (ToolTranscriptSessionID)
//	Tool: caller's tool name
//	Details:
//	 path: canonical (EvalSymlinks if reachable; else cleaned)
//	 reason: outside_workspace | not_in_allow_list | symlink_escape
//
// `auditLog` is the *audit.Logger handed to the tool via SetAuditLogger;
// pass nil to skip emission silently.
//
// `validatorErr` is the original error from validatePathWithAllowPaths
// (or shell guardCommand). Must be non-nil — callers gate on this.
//
// `allowPathsLen` is len(allowPaths) at the call site and is used for the
// reason heuristic
//
// The function never returns an error: audit logging is best-effort. On
// write failure (degraded mode) a slog.Error is recorded by the audit
// package itself; we don't double-log here.
func emitPathAccessDenied(
	ctx context.Context,
	auditLog *audit.Logger,
	toolName string,
	rawPath string,
	validatorErr error,
	allowPathsLen int,
) {
	if auditLog == nil {
		return
	}
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     PathAccessDeniedEvent,
		Decision:  audit.DecisionDeny,
		AgentID:   ToolAgentID(ctx),
		SessionID: ToolTranscriptSessionID(ctx),
		Tool:      toolName,
		Details: map[string]any{
			"path":   canonicalDeniedPath(rawPath),
			"reason": classifyPathDenialReason(validatorErr, allowPathsLen),
		},
	}
	if err := auditLog.Log(entry); err != nil {
		slog.Error("path_audit: log write failed", "error", err, "session_id", entry.SessionID)
	}
}
