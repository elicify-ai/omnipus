// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- minimal test tool for builtin registry tests ---

type builtinTestTool struct {
	name  string
	scope ToolScope
}

func (t *builtinTestTool) Name() string               { return t.name }
func (t *builtinTestTool) Description() string        { return "builtin test tool: " + t.name }
func (t *builtinTestTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (t *builtinTestTool) Scope() ToolScope           { return t.scope }
func (t *builtinTestTool) RequiresAdminAsk() bool     { return false }
func (t *builtinTestTool) Category() ToolCategory     { return CategoryCore }
func (t *builtinTestTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	return SilentResult("ok")
}

// --- TestBuiltinRegistry_Register_Lookup ---

// TestBuiltinRegistry_Register_Lookup verifies that a registered builtin tool
// can be retrieved by name via Get().
//
// BDD: Given an empty BuiltinRegistry,
//
//	When a tool is registered via RegisterBuiltin,
//	Then Get(name) returns the tool and true;
//	And Get(unknown) returns nil and false.
//
// Traces to: pkg/tools/builtin_registry.go — RegisterBuiltin + Get.
func TestBuiltinRegistry_Register_Lookup(t *testing.T) {
	reg := NewBuiltinRegistry()

	tool := &builtinTestTool{name: "test.read_file", scope: ScopeCore}
	err := reg.RegisterBuiltin(tool)
	require.NoError(t, err, "first registration must succeed")

	got, ok := reg.Get("test.read_file")
	require.True(t, ok, "Get must return true for a registered tool")
	assert.Equal(t, "test.read_file", got.Name(), "Get must return the registered tool")

	_, ok2 := reg.Get("nonexistent")
	assert.False(t, ok2, "Get must return false for an unregistered tool")
}

// TestBuiltinRegistry_Describe_AllRegistered verifies that Describe() returns
// an entry for every registered tool.
//
// BDD: Given 3 tools registered in a BuiltinRegistry,
//
//	When Describe() is called,
//	Then the result contains exactly 3 entries with non-empty Name fields.
//
// Traces to: pkg/tools/builtin_registry.go — Describe.
func TestBuiltinRegistry_Describe_AllRegistered(t *testing.T) {
	reg := NewBuiltinRegistry()

	for _, name := range []string{"tool.a", "tool.b", "tool.c"} {
		require.NoError(t, reg.RegisterBuiltin(&builtinTestTool{name: name, scope: ScopeCore}))
	}

	entries := reg.Describe()
	require.Len(t, entries, 3, "Describe must return one entry per registered tool")
	for _, e := range entries {
		assert.NotEmpty(t, e.Name, "each entry must have a non-empty Name")
	}
}

// TestBuiltinRegistry_NameCollision_ReturnsError verifies that registering a tool
// whose name is already in the registry returns an error (builtin-wins invariant).
//
// BDD: Given a tool "search_web" already registered,
//
//	When a second tool with name "search_web" is registered,
//	Then RegisterBuiltin returns a non-nil error containing the collision details.
//
// Traces to: pkg/tools/builtin_registry.go — RegisterBuiltin duplicate guard (FR-034).
func TestBuiltinRegistry_NameCollision_ReturnsError(t *testing.T) {
	reg := NewBuiltinRegistry()

	first := &builtinTestTool{name: "search_web", scope: ScopeGeneral}
	require.NoError(t, reg.RegisterBuiltin(first), "first registration must succeed")

	second := &builtinTestTool{name: "search_web", scope: ScopeGeneral}
	err := reg.RegisterBuiltin(second)
	require.Error(t, err, "duplicate registration must return an error")
	assert.Contains(t, err.Error(), "search_web", "error must name the colliding tool")

	// The original tool must still be retrievable.
	got, ok := reg.Get("search_web")
	require.True(t, ok)
	assert.Equal(t, "search_web", got.Name(), "original tool must survive the duplicate attempt")
}

// TestBuiltinRegistry_ValidateMCPName_RejectsSystemPrefix verifies that MCP tool
// names beginning with "system." are rejected (FR-060).
//
// BDD: Given a BuiltinRegistry,
//
//	When ValidateMCPName is called with "system.some_tool",
//	Then it returns an error containing the reserved prefix.
//	When ValidateMCPName is called with "my_custom_tool",
//	Then it returns nil.
//
// Traces to: pkg/tools/builtin_registry.go — ValidateMCPName (FR-060).
func TestBuiltinRegistry_ValidateMCPName_RejectsSystemPrefix(t *testing.T) {
	reg := NewBuiltinRegistry()

	err := reg.ValidateMCPName("system.some_tool")
	require.Error(t, err, "system.* prefix must be rejected")
	assert.Contains(t, err.Error(), "system.", "error must mention the reserved prefix")

	err2 := reg.ValidateMCPName("my_custom_tool")
	assert.NoError(t, err2, "non-reserved MCP name must be accepted")
}

// TestBuiltinRegistry_Count verifies that Count() returns the number of registered tools.
//
// BDD: Given 0, then 1, then 2 tools registered,
//
//	When Count() is called,
//	Then it returns 0, 1, 2 respectively.
//
// Traces to: pkg/tools/builtin_registry.go — Count.
func TestBuiltinRegistry_Count(t *testing.T) {
	reg := NewBuiltinRegistry()
	assert.Equal(t, 0, reg.Count())

	require.NoError(t, reg.RegisterBuiltin(&builtinTestTool{name: "a", scope: ScopeGeneral}))
	assert.Equal(t, 1, reg.Count())

	require.NoError(t, reg.RegisterBuiltin(&builtinTestTool{name: "b", scope: ScopeGeneral}))
	assert.Equal(t, 2, reg.Count())
}
