package audit

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// UAT 2026-09-13 D-85: the identifying subset of a tool call's arguments.
func TestSalientToolArgs_KeepsIdentityDropsContent(t *testing.T) {
	got := SalientToolArgs("knowledge_edit", map[string]any{
		"op":       "set_property",
		"path":     "Projects/Alpha.md",
		"property": "status",
		"value":    "active",
		"content":  "a whole document body that must never reach the audit log",
		"paths":    []any{"a.md", "b.md", 3, map[string]any{"nested": true}},
		"api_key":  "sk-live-123",
		"nested":   map[string]any{"path": "x"},
	})
	assert.Equal(t, "set_property", got["op"])
	assert.Equal(t, "Projects/Alpha.md", got["path"])
	assert.Equal(t, "status", got["property"])
	assert.NotContains(t, got, "value", "payload keys are not identity")
	assert.NotContains(t, got, "content")
	assert.NotContains(t, got, "nested")
	assert.NotContains(t, got, "api_key")
	assert.Equal(t, []any{"a.md", "b.md", 3}, got["paths"], "lists keep scalars only")
}

func TestSalientToolArgs_EmptyAndTruncation(t *testing.T) {
	assert.Nil(t, SalientToolArgs("x", nil))
	assert.Nil(t, SalientToolArgs("x", map[string]any{"content": "only content"}))
	long := make([]byte, salientToolArgMaxValueLen+50)
	for i := range long {
		long[i] = 'p'
	}
	got := SalientToolArgs("x", map[string]any{"path": string(long)})
	assert.Len(t, got["path"], salientToolArgMaxValueLen+len("…"))
	assert.Equal(t, "op=set path=a.md", SalientToolArgsSummary("x", map[string]any{"path": "a.md", "op": "set", "content": "z"}))
}
