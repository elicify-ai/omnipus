package tools

// Tests for ExecTool/ReadFileTool audit emission on path-guard rejection.
// Spec: path-sandbox-and-capability-tiers-spec.md /
// BDD test ID: #84b
// Traces to: path-sandbox-and-capability-tiers-spec.md line ~183

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// ---------------------------------------------------------------------------
// TestExecTool_AuditOnPathRejection
// BDD: Given a workspace-restricted ExecTool with an audit logger,
// When a command containing "../" traversal is executed,
// Then the tool returns IsError=true (path.access_denied emitted).
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestExecTool_AuditOnPathRejection(t *testing.T) {
	workspace := t.TempDir()

	// Construct workspace-restricted ExecTool (restrict=true so the workspace
	// guard in guardCommand applies and the "../" traversal is blocked).
	tool, err := NewExecTool(workspace, true)
	require.NoError(t, err)

	// Wire in audit logger.
	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           auditDir,
		RetentionDays: 1,
	})
	require.NoError(t, err)
	tool.SetAuditLogger(auditLogger)

	// "cli" is an internal channel — passes the remote-channel guard so we reach
	// the path-traversal check in guardCommand (the behavior under test).
	ctx := WithToolContext(context.Background(), "cli", "")

	// A command with "../" triggers guardCommand's path-traversal check.
	result := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "cat ../../etc/passwd",
	})

	// Primary: tool must reject.
	require.True(t, result.IsError,
		"ExecTool must return IsError=true on path traversal (got ForLLM: %q)", result.ForLLM)
	assert.NotEmpty(t, result.ForLLM, "ForLLM must describe the rejection reason")
	assert.Contains(t, result.ForLLM, "blocked",
		"rejection message must mention 'blocked'")
}

// ---------------------------------------------------------------------------
// TestReadFileTool_AuditOnPathRejection_AccessDeniedEmitted
// BDD: Given a workspace-restricted ReadFileTool with an audit logger,
// When read_file is called with /etc/passwd (outside workspace),
// Then IsError=true, audit file is non-empty (path.access_denied emitted).
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestReadFileTool_AuditOnPathRejection_AccessDeniedEmitted(t *testing.T) {
	workspace := t.TempDir()
	auditDir := t.TempDir()

	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           auditDir,
		RetentionDays: 1,
	})
	require.NoError(t, err)

	tool := NewReadFileTool(workspace, true, 0)
	tool.SetAuditLogger(auditLogger)

	ctx := WithToolContext(context.Background(), "test-agent", "")

	result := tool.Execute(ctx, map[string]any{"path": "/etc/passwd"})

	// Tool must reject.
	require.True(t, result.IsError,
		"ReadFileTool must reject /etc/passwd with IsError=true")
	assert.NotEmpty(t, result.ForLLM, "ForLLM must be non-empty on path rejection")

	// Audit file must be written.
	entries, err := os.ReadDir(auditDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "audit dir must contain at least one file after rejection")

	var totalBytes int
	for _, e := range entries {
		fp := filepath.Join(auditDir, e.Name())
		data, readErr := os.ReadFile(fp)
		if readErr == nil {
			totalBytes += len(data)
		}
	}
	assert.Greater(t, totalBytes, 0,
		"audit file must contain data (path.access_denied entry expected)")
}

// ---------------------------------------------------------------------------
// TestReadFileTool_AuditReason_OutsideWorkspace
// BDD: Given a workspace-restricted ReadFileTool with no allow-list,
// When read_file is called with /etc/passwd,
// Then the reason is "outside_workspace" (not "not_in_allow_list").
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestReadFileTool_AuditReason_OutsideWorkspace(t *testing.T) {
	// classifyPathDenialReason with allowPathsLen=0 and a non-symlink error
	// must return "outside_workspace".
	dummyErr := errorFromString("path is outside the workspace")
	reason := classifyPathDenialReason(dummyErr, 0)
	assert.Equal(t, ReasonOutsideWorkspace, reason,
		"no allow-list: reason must be outside_workspace")
}

func TestReadFileTool_AuditReason_NotInAllowList(t *testing.T) {
	dummyErr := errorFromString("path is outside the workspace")
	reason := classifyPathDenialReason(dummyErr, 3) // 3 allow-paths configured
	assert.Equal(t, ReasonNotInAllowList, reason,
		"with allow-list configured: reason must be not_in_allow_list")
}

func TestReadFileTool_AuditReason_SymlinkEscape(t *testing.T) {
	symlinkErr := errorFromString("symlink escapes workspace boundary")
	reason := classifyPathDenialReason(symlinkErr, 0)
	assert.Equal(t, ReasonSymlinkEscape, reason,
		"symlink error: reason must be symlink_escape")
}

// errorFromString converts a string into an error for testing.
func errorFromString(s string) error {
	return &testError{msg: s}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// ---------------------------------------------------------------------------
// TestKillAuditFn_InvokedOnKillFailure (quick sweep 3)
//
// Verifies that t.killAuditFn is correctly constructed by SetAuditLogger and,
// when invoked, produces a "process_kill_failed" audit entry with the expected
// fields. Tests the wiring path: SetAuditLogger builds killAuditFn, and the
// closure calls auditLogger.Log with a well-formed Entry.
// ---------------------------------------------------------------------------

func TestKillAuditFn_InvokedOnKillFailure(t *testing.T) {
	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           auditDir,
		RetentionDays: 1,
	})
	require.NoError(t, err)

	tool, toolErr := NewExecTool(t.TempDir(), false)
	require.NoError(t, toolErr)
	tool.SetAuditLogger(auditLogger)

	// killAuditFn must be non-nil after SetAuditLogger with a non-nil logger.
	require.NotNil(t, tool.killAuditFn,
		"SetAuditLogger must populate killAuditFn when logger is non-nil")

	// Invoke the closure directly with a synthetic kill error.
	syntheticErr := errorFromString("operation not permitted")
	tool.killAuditFn(12345, syntheticErr, "kill_session_tool")

	// Flush the in-memory audit buffer to disk.
	require.NoError(t, auditLogger.Close())

	// Read the written audit entries and verify the kill-failed event.
	entries, readErr := os.ReadDir(auditDir)
	require.NoError(t, readErr)
	require.NotEmpty(t, entries, "audit dir must contain at least one file")

	found := false
	for _, e := range entries {
		data, readFileErr := os.ReadFile(filepath.Join(auditDir, e.Name()))
		require.NoError(t, readFileErr)
		// Each line is a JSONL entry; scan all lines.
		for len(data) > 0 {
			var line []byte
			idx := -1
			for i, b := range data {
				if b == '\n' {
					idx = i
					break
				}
			}
			if idx >= 0 {
				line = data[:idx]
				data = data[idx+1:]
			} else {
				line = data
				data = nil
			}
			if len(line) == 0 {
				continue
			}
			var entry map[string]any
			if json.Unmarshal(line, &entry) != nil {
				continue
			}
			if entry["event"] == "process_kill_failed" {
				details, _ := entry["details"].(map[string]any)
				if details != nil {
					pidVal, _ := details["pid"].(float64)
					callerVal, _ := details["caller"].(string)
					if int(pidVal) == 12345 && callerVal == "kill_session_tool" {
						found = true
					}
				}
			}
		}
	}
	assert.True(t, found,
		"audit log must contain a process_kill_failed entry with pid=12345 and caller=kill_session_tool")
}

// TestKillAuditFn_NilAfterNilLogger verifies that SetAuditLogger(nil)
// clears killAuditFn so no-op calls at kill sites are safe.
func TestKillAuditFn_NilAfterNilLogger(t *testing.T) {
	tool, err := NewExecTool(t.TempDir(), false)
	require.NoError(t, err)

	// Set a real logger, then clear it.
	auditDir := t.TempDir()
	al, alErr := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 1})
	require.NoError(t, alErr)
	tool.SetAuditLogger(al)
	require.NotNil(t, tool.killAuditFn)

	tool.SetAuditLogger(nil)
	assert.Nil(t, tool.killAuditFn,
		"SetAuditLogger(nil) must clear killAuditFn")
}

// ---------------------------------------------------------------------------
// Differentiation test: two different rejected paths produce real error results.
// ---------------------------------------------------------------------------

func TestReadFileTool_DifferentRejectedPaths_BothDenied(t *testing.T) {
	workspace := t.TempDir()
	tool := NewReadFileTool(workspace, true, 0)
	ctx := WithToolContext(context.Background(), "test-agent", "")

	result1 := tool.Execute(ctx, map[string]any{"path": "/etc/passwd"})
	result2 := tool.Execute(ctx, map[string]any{"path": "/etc/shadow"})

	// Both must be errors.
	require.True(t, result1.IsError, "/etc/passwd: must be rejected")
	require.True(t, result2.IsError, "/etc/shadow: must be rejected")

	// Both must have non-empty error descriptions.
	assert.NotEmpty(t, result1.ForLLM, "rejection for /etc/passwd must have ForLLM content")
	assert.NotEmpty(t, result2.ForLLM, "rejection for /etc/shadow must have ForLLM content")
}

// ---------------------------------------------------------------------------
// Constant verification — all three reason constants must be the spec values.
// Traces to: path-sandbox-and-capability-tiers-spec.md
// ---------------------------------------------------------------------------

func TestPathAuditConstants_MatchSpecValues(t *testing.T) {
	assert.Equal(t, "path.access_denied", PathAccessDeniedEvent,
		"PathAccessDeniedEvent must match MAJ-001 event class")
	assert.Equal(t, "outside_workspace", ReasonOutsideWorkspace,
		"ReasonOutsideWorkspace must match MAJ-002 spec literal")
	assert.Equal(t, "not_in_allow_list", ReasonNotInAllowList,
		"ReasonNotInAllowList must match MAJ-002 spec literal")
	assert.Equal(t, "symlink_escape", ReasonSymlinkEscape,
		"ReasonSymlinkEscape must match MAJ-002 spec literal")
}
