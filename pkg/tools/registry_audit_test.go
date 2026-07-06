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

	"github.com/elicify-ai/omnipus/pkg/audit"
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

// mockMemoryRateLimiterAwareTool is a minimal tool implementing
// memoryRateLimiterAware, used to prove that Clone/CloneExcept propagate the
// registry's memoryRateLimiter field to tools registered AFTER cloning (the
// path a delegate sub-turn registry exercises when it registers a fresh
// RememberTool/RetrospectiveTool for the child turn).
type mockMemoryRateLimiterAwareTool struct {
	mockRegistryTool
	limiter *MemoryRateLimiter
}

func (m *mockMemoryRateLimiterAwareTool) SetMemoryRateLimiter(limiter *MemoryRateLimiter) {
	m.limiter = limiter
}

// TestToolRegistry_CloneAndCloneExcept_PropagateMemoryRateLimiter is the
// regression test for the Clone()/CloneExcept() memoryRateLimiter gap: both
// methods copied mediaStore and auditLogger onto the cloned registry but
// omitted memoryRateLimiter, so any subagent-delegated remember/
// run_retrospective call (via the delegate tool's sub-turn tool registry)
// silently bypassed the v0.2 #155 item 6 memory-write rate limiter.
func TestToolRegistry_CloneAndCloneExcept_PropagateMemoryRateLimiter(t *testing.T) {
	limiter := NewMemoryRateLimiter(MemoryRateLimitConfig{})

	t.Run("Clone propagates memoryRateLimiter", func(t *testing.T) {
		reg := NewToolRegistry()
		reg.SetMemoryRateLimiter(limiter)
		reg.Register(&mockRegistryTool{
			name:   "pre_clone_tool",
			desc:   "registered before cloning",
			params: map[string]any{"type": "object", "properties": map[string]any{}},
			result: &ToolResult{ForLLM: "ok", ForUser: "ok"},
		})

		clone := reg.Clone()
		require.Same(t, limiter, clone.memoryRateLimiter,
			"cloned registry must retain the SAME memoryRateLimiter instance")

		// Functional proof: a tool registered on the CLONE after cloning must
		// pick up the limiter via registerToolLocked's memoryRateLimiterAware
		// propagation — this is the exact path a delegate sub-turn registry
		// exercises when it registers a fresh RememberTool for the child turn.
		post := &mockMemoryRateLimiterAwareTool{
			mockRegistryTool: mockRegistryTool{name: "post_clone_tool", desc: "registered after cloning"},
		}
		clone.Register(post)
		assert.Same(t, limiter, post.limiter,
			"a tool registered on the clone after cloning must receive the propagated limiter")
	})

	t.Run("CloneExcept propagates memoryRateLimiter", func(t *testing.T) {
		reg := NewToolRegistry()
		reg.SetMemoryRateLimiter(limiter)
		reg.Register(&mockRegistryTool{
			name:   "delegate",
			desc:   "excluded from the clone",
			params: map[string]any{"type": "object", "properties": map[string]any{}},
			result: &ToolResult{ForLLM: "ok", ForUser: "ok"},
		})

		clone := reg.CloneExcept(ExcludedDelegate)
		require.Same(t, limiter, clone.memoryRateLimiter,
			"CloneExcept must retain the SAME memoryRateLimiter instance for the same reason as Clone")

		post := &mockMemoryRateLimiterAwareTool{
			mockRegistryTool: mockRegistryTool{name: "post_cloneexcept_tool", desc: "registered after cloning"},
		}
		clone.Register(post)
		assert.Same(t, limiter, post.limiter,
			"a tool registered on the CloneExcept clone after cloning must receive the propagated limiter")
	})
}
