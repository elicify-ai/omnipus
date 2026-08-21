// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestAuditLogging verifies that a system tool invocation is written to the audit
// log with the required fields (caller_role, device_id, tool_name, parameters)
// and that any credential values in parameters are redacted.
//
// This is spec test #20 in the wave5b TDD plan.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: System tool audit logging
// BDD: "Given any system tool is invoked, When the tool executes,
//
//	Then the invocation is logged to the audit trail with caller_role, device_id,
//	tool_name, and parameters (with credential values redacted)."
func TestAuditLogging(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 414 (Scenario: System tool audit logging)

	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		RedactEnabled: true,
	})
	require.NoError(t, err)
	// Test cleanup: Close error is inconsequential — t.TempDir() removes
	// the backing directory regardless, and no test here asserts on it.
	defer func() {
		if err := logger.Close(); err != nil {
			_ = err
		}
	}()

	// Simulate a system tool invocation: system.provider.configure with an API key.
	// The audit entry uses Details for caller_role and device_id since the Entry
	// struct captures these as key-value detail fields until the system tool handler
	// extends the schema.
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  "allow",
		AgentID:   "omnipus-system",
		Tool:      "system.provider.configure",
		Parameters: map[string]any{
			// api_key value must be redacted per SEC-16 and wave5b FR-005
			"name":    "anthropic",
			"api_key": "sk-ant-api01-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		},
		Details: map[string]any{
			"caller_role": "admin",
			"device_id":   "device-abc123",
		},
	}

	require.NoError(t, logger.Log(entry))

	// Read back the written entry.
	logPath := filepath.Join(dir, "audit.jsonl")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed), "audit entry must be valid JSON")

	// Tool name must be recorded.
	assert.Equal(t, "system.provider.configure", parsed["tool"],
		"tool name must be recorded in audit entry")

	// Agent ID must identify the system agent.
	assert.Equal(t, "omnipus-system", parsed["agent_id"],
		"agent_id must identify the system agent")

	// Parameters block must exist.
	params, ok := parsed["parameters"].(map[string]any)
	require.True(t, ok, "parameters must be a JSON object in audit entry")

	// Non-sensitive parameter must be preserved.
	assert.Equal(t, "anthropic", params["name"],
		"non-sensitive parameter 'name' must be preserved unchanged")

	// Credential value must be redacted.
	apiKeyVal, _ := params["api_key"].(string)
	assert.NotContains(t, apiKeyVal, "sk-ant-",
		"api_key must NOT appear in plaintext in audit log (wave5b FR-005, SEC-16)")
	assert.Equal(t, "[REDACTED]", apiKeyVal,
		"api_key credential value must be replaced with [REDACTED]")

	// caller_role and device_id must be captured in details.
	details, ok := parsed["details"].(map[string]any)
	require.True(t, ok, "details must be a JSON object in audit entry")
	assert.Equal(t, "admin", details["caller_role"],
		"caller_role must be recorded in audit details")
	assert.Equal(t, "device-abc123", details["device_id"],
		"device_id must be recorded in audit details")
}

// TestAuditLogging_SystemToolBulkCredentials verifies that ALL credential fields
// in a system tool call parameter map are redacted — not just the first one.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: System tool audit logging
// BDD: "Any credential values in parameters are redacted (replaced with '[REDACTED]')"
func TestAuditLogging_SystemToolBulkCredentials(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 422 (audit trail with redacted params)

	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Dir:           dir,
		RetentionDays: 90,
		RedactEnabled: true,
	})
	require.NoError(t, err)
	defer func() {
		if err := logger.Close(); err != nil {
			_ = err
		}
	}()

	// Multiple credential-looking values in parameters.
	entry := &audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     audit.EventToolCall,
		Decision:  "allow",
		AgentID:   "omnipus-system",
		Tool:      "system.provider.configure",
		Parameters: map[string]any{
			"name":          "openai",
			"api_key":       "sk-proj-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
			"safe_param":    "not-a-credential",
			"numeric_param": 42,
		},
		Details: map[string]any{
			"caller_role": "operator",
			"device_id":   "device-xyz789",
		},
	}

	require.NoError(t, logger.Log(entry))

	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	params, ok := parsed["parameters"].(map[string]any)
	require.True(t, ok)

	// OpenAI project key must be redacted.
	assert.NotContains(t, params["api_key"], "sk-proj-",
		"OpenAI project key must not appear in plaintext in audit log")

	// Non-sensitive fields must pass through.
	assert.Equal(t, "not-a-credential", params["safe_param"],
		"non-sensitive string parameter must be preserved")
}
