// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/policy"
)

// TestAuditBridge_LogPolicyDecision verifies that the auditBridge correctly
// converts a policy.AuditEntry into an audit.Entry and writes it to the
// audit JSONL file (SEC-15, ADR W-3).
func TestAuditBridge_LogPolicyDecision(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	defer logger.Close()

	bridge := &auditBridge{logger: logger}

	// Verify compile-time interface satisfaction.
	var _ policy.AuditLogger = bridge

	entry := &policy.AuditEntry{
		Timestamp:  time.Date(2026, 1, 15, 10, 30, 0, 0, time.UTC),
		Event:      "tool_call",
		Decision:   "deny",
		AgentID:    "researcher",
		SessionID:  "sess-abc",
		Tool:       "exec",
		PolicyRule: "tool 'exec' not in tools.allow for agent 'researcher'",
	}

	err = bridge.LogPolicyDecision(entry)
	require.NoError(t, err)
	logger.Close()

	// Read and verify the JSONL output.
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err, "audit entry must be valid JSON")

	assert.Equal(t, "tool_call", parsed["event"])
	assert.Equal(t, "deny", parsed["decision"])
	assert.Equal(t, "researcher", parsed["agent_id"])
	assert.Equal(t, "sess-abc", parsed["session_id"])
	assert.Equal(t, "exec", parsed["tool"])
	assert.Contains(t, parsed["policy_rule"], "not in tools.allow")
}

// TestAuditBridge_ExecDecision verifies that exec policy decisions are
// correctly bridged with the command field populated.
func TestAuditBridge_ExecDecision(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	defer logger.Close()

	bridge := &auditBridge{logger: logger}

	entry := &policy.AuditEntry{
		Timestamp:  time.Now().UTC(),
		Event:      "exec",
		Decision:   "allow",
		AgentID:    "coder",
		Command:    "git status",
		PolicyRule: "exec allowed: command matched pattern \"git *\"",
	}

	err = bridge.LogPolicyDecision(entry)
	require.NoError(t, err)
	logger.Close()

	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "exec", parsed["event"])
	assert.Equal(t, "allow", parsed["decision"])
	assert.Equal(t, "git status", parsed["command"])
	assert.Contains(t, parsed["policy_rule"], "git")
}
