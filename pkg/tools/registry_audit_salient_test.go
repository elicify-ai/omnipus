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

// TestToolRegistry_AuditCarriesSalientArgsAndSession pins UAT 2026-09-13
// D-85: a tool_call audit entry names WHAT the call acted on (op, path,
// property) and WHICH session ran it, and never the content it wrote.
func TestToolRegistry_AuditCarriesSalientArgsAndSession(t *testing.T) {
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	defer logger.Close()

	reg := NewToolRegistry()
	reg.SetAuditLogger(logger)
	reg.Register(&mockRegistryTool{
		name: "stub_edit_tool",
		desc: "stub",
		params: map[string]any{"type": "object", "properties": map[string]any{
			"op": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"},
			"property": map[string]any{"type": "string"}, "value": map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
		}},
		result: &ToolResult{ForLLM: "ok", ForUser: "ok"},
	})

	ctx := WithAgentID(context.Background(), "uat-builder")
	ctx = WithToolContext(ctx, "cli", "")
	ctx = WithTranscriptSessionID(ctx, "session_01UATSESSION")
	reg.ExecuteWithContext(ctx, "stub_edit_tool", map[string]any{
		"op":       "set_property",
		"path":     "Projects/Alpha.md",
		"property": "status",
		"value":    "active",
		"content":  "SECRET BODY THAT MUST NOT BE AUDITED",
	}, "cli", "", nil)
	logger.Close()

	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed), "audit entry must be valid JSON: %s", string(data))

	assert.Equal(t, "tool_call", parsed["event"])
	assert.Equal(t, "session_01UATSESSION", parsed["session_id"])
	details, _ := parsed["details"].(map[string]any)
	require.NotNil(t, details)
	args, _ := details["args"].(map[string]any)
	require.NotNil(t, args, "details.args must carry the identifying arguments: %s", string(data))
	assert.Equal(t, "set_property", args["op"])
	assert.Equal(t, "Projects/Alpha.md", args["path"])
	assert.Equal(t, "status", args["property"])
	assert.NotContains(t, string(data), "SECRET BODY", "content must never reach the audit log")
	assert.NotContains(t, args, "value")
}
