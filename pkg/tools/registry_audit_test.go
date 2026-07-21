// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/logger"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// TestToolRegistry_AuditLogging verifies that ExecuteWithContext writes an
// audit entry after each tool execution (SEC-15).
func TestToolRegistry_AuditLogging(t *testing.T) {
	t.Run("successful tool execution logs allow decision", func(t *testing.T) {
		dir := t.TempDir()
		logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
		require.NoError(t, err)
		defer logger.Close()

		reg := NewToolRegistry()
		reg.SetAuditLogger(logger)
		reg.Register(&mockRegistryTool{
			name:   "test_tool",
			desc:   "a test tool",
			params: map[string]any{"type": "object", "properties": map[string]any{}},
			result: &ToolResult{ForLLM: "ok", ForUser: "ok"},
		})

		ctx := WithAgentID(context.Background(), "test-agent")
		ctx = WithToolContext(ctx, "cli", "")
		reg.ExecuteWithContext(ctx, "test_tool", map[string]any{}, "cli", "", nil)
		logger.Close()

		data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
		require.NoError(t, err)

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err, "audit entry must be valid JSON: %s", string(data))

		assert.Equal(t, "tool_call", parsed["event"])
		assert.Equal(t, "allow", parsed["decision"])
		assert.Equal(t, "test-agent", parsed["agent_id"])
		assert.Equal(t, "test_tool", parsed["tool"])
	})

	t.Run("failed tool execution logs error decision", func(t *testing.T) {
		dir := t.TempDir()
		logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
		require.NoError(t, err)
		defer logger.Close()

		reg := NewToolRegistry()
		reg.SetAuditLogger(logger)
		reg.Register(&mockRegistryTool{
			name:   "failing_tool",
			desc:   "fails",
			params: map[string]any{"type": "object", "properties": map[string]any{}},
			result: &ToolResult{ForLLM: "error", ForUser: "error", IsError: true},
		})

		ctx := WithAgentID(context.Background(), "test-agent")
		ctx = WithToolContext(ctx, "cli", "")
		reg.ExecuteWithContext(ctx, "failing_tool", map[string]any{}, "cli", "", nil)
		logger.Close()

		data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
		require.NoError(t, err)

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err)

		assert.Equal(t, "tool_call", parsed["event"])
		assert.Equal(t, "error", parsed["decision"])
		assert.Equal(t, "failing_tool", parsed["tool"])
	})

	t.Run("no audit logger means no crash", func(t *testing.T) {
		reg := NewToolRegistry()
		// No SetAuditLogger called
		reg.Register(&mockRegistryTool{
			name:   "safe_tool",
			desc:   "safe",
			params: map[string]any{"type": "object", "properties": map[string]any{}},
			result: &ToolResult{ForLLM: "ok", ForUser: "ok"},
		})

		ctx := WithToolContext(context.Background(), "cli", "")
		result := reg.ExecuteWithContext(ctx, "safe_tool", map[string]any{}, "cli", "", nil)
		assert.False(t, result.IsError, "tool should succeed without audit logger")
	})

	t.Run("clone preserves audit logger", func(t *testing.T) {
		dir := t.TempDir()
		logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
		require.NoError(t, err)
		defer logger.Close()

		reg := NewToolRegistry()
		reg.SetAuditLogger(logger)
		reg.Register(&mockRegistryTool{
			name:   "cloned_tool",
			desc:   "cloned",
			params: map[string]any{"type": "object", "properties": map[string]any{}},
			result: &ToolResult{ForLLM: "ok", ForUser: "ok"},
		})

		clone := reg.Clone()
		assert.NotNil(t, clone.auditLogger, "cloned registry should retain audit logger")
	})
}
