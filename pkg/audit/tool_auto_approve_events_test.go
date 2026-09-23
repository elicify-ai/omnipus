// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestEventToolAutoApproved_IsValidEventName pins ADR-092 §5.5's
// requirement that tool.auto_approved is registered in
// audit.IsValidEventName's allowlist — an unregistered event name trips the
// "unknown Event value" warn-once path on every real emission.
func TestEventToolAutoApproved_IsValidEventName(t *testing.T) {
	t.Parallel()
	require.True(t, audit.IsValidEventName(audit.EventName(audit.EventToolAutoApproved)),
		"EventToolAutoApproved must be registered in validEventNames")
	assert.Equal(t, "tool.auto_approved", audit.EventToolAutoApproved)
}

// TestEmitToolAutoApproved_WritesOneEntryWithRequiredFields is the §5.5
// contract test: Emitted once for each call Auto ran without a prompt, with
// Details `{tool, agent_id, session_id, class, reason, paths}`.
func TestEmitToolAutoApproved_WritesOneEntryWithRequiredFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  1024 * 1024,
		RetentionDays: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		if closeErr := logger.Close(); closeErr != nil {
			_ = closeErr
		}
	})

	audit.EmitToolAutoApproved(context.Background(), logger,
		"general-assistant", "sess-abc123", "read_file", "runs_if_args",
		"path resolved inside the workspace", []string{"/home/user/workspace/notes.md"})

	require.NoError(t, logger.Close())

	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	require.Len(t, lines, 1, "exactly one audit entry must be written")

	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &parsed))

	assert.Equal(t, "tool.auto_approved", parsed["event"])
	assert.Equal(t, "allow", parsed["decision"])
	assert.Equal(t, "general-assistant", parsed["agent_id"])
	assert.Equal(t, "sess-abc123", parsed["session_id"])
	assert.Equal(t, "read_file", parsed["tool"])
	assert.Equal(t, "path resolved inside the workspace", parsed["policy_rule"])
	assert.NotEmpty(t, parsed["timestamp"])

	details, ok := parsed["details"].(map[string]any)
	require.True(t, ok, "details must be present and be an object: %v", parsed["details"])
	assert.Equal(t, "read_file", details["tool"])
	assert.Equal(t, "runs_if_args", details["class"])
	assert.Equal(t, "path resolved inside the workspace", details["reason"])
	assert.Equal(t, []any{"/home/user/workspace/notes.md"}, details["paths"])
}

// TestEmitToolAutoApproved_OptionalFieldsOmittedWhenEmpty covers a call with
// no reason and no paths (e.g. a plain AutoRuns tool with no RUNS-IF
// condition to explain) — those two Details keys must not appear at all
// rather than appearing empty, matching the sibling ADR-092 shell emitters'
// convention (see EmitShellApprovalDecision's reason handling).
func TestEmitToolAutoApproved_OptionalFieldsOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		MaxSizeBytes:  1024 * 1024,
		RetentionDays: 1,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		if closeErr := logger.Close(); closeErr != nil {
			_ = closeErr
		}
	})

	audit.EmitToolAutoApproved(context.Background(), logger,
		"general-assistant", "sess-abc123", "list_agents", "runs", "", nil)

	require.NoError(t, logger.Close())

	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(string(data))), &parsed))

	details, ok := parsed["details"].(map[string]any)
	require.True(t, ok)
	_, hasReason := details["reason"]
	_, hasPaths := details["paths"]
	assert.False(t, hasReason, "reason key must be omitted when empty")
	assert.False(t, hasPaths, "paths key must be omitted when empty")
	// policy_rule mirrors reason (SEC-17 explainability); empty reason means
	// no policy_rule key at all, since Entry.PolicyRule is `omitempty`.
	_, hasPolicyRule := parsed["policy_rule"]
	assert.False(t, hasPolicyRule, "policy_rule key must be omitted when reason is empty")
}

// TestEmitToolAutoApproved_NilLoggerIsNoOp — the audit subsystem can be
// disabled (nil logger); emitting against it must never panic or block the
// tool call it would have audited.
func TestEmitToolAutoApproved_NilLoggerIsNoOp(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil-logger emit panicked: %v", r)
		}
	}()
	audit.EmitToolAutoApproved(context.Background(), nil,
		"general-assistant", "sess-abc123", "read_file", "runs_if_args", "reason", []string{"/tmp/x"})
}
