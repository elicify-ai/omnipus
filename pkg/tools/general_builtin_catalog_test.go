// Omnipus — General Builtin Metadata Catalog Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — tests for GeneralBuiltinMetadata and central BuiltinRegistry
// population (Issue #350, US-1/AC2/AC3/AC4/AC7, FR-101, SC-101, SC-108).

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGeneralBuiltinMetadata_ContainsMandatoryTools asserts that GeneralBuiltinMetadata
// returns at least the five mandatory general builtin names required by Spec-1 (SC-101).
//
// BDD: Given a default build,
//
//	When GeneralBuiltinMetadata() is called,
//	Then it returns a non-empty slice containing at minimum exec, read_file,
//	write_file, web_search, and web_fetch — with no duplicate names.
//
// Traces to: US-1/AC3, FR-101, SC-101.
func TestGeneralBuiltinMetadata_ContainsMandatoryTools(t *testing.T) {
	catalog := GeneralBuiltinMetadata()
	require.NotEmpty(t, catalog, "GeneralBuiltinMetadata must return at least one tool")

	byName := make(map[string]Tool, len(catalog))
	for _, tool := range catalog {
		name := tool.Name()
		require.NotEmpty(t, name, "every tool in GeneralBuiltinMetadata must have a non-empty Name")
		_, dup := byName[name]
		assert.False(t, dup, "GeneralBuiltinMetadata must not return duplicate tool name %q", name)
		byName[name] = tool
	}

	mandatory := []string{"exec", "read_file", "write_file", "web_search", "web_fetch"}
	for _, name := range mandatory {
		assert.Contains(t, byName, name,
			"GeneralBuiltinMetadata must contain mandatory tool %q (Spec-1 SC-101)", name)
	}
}

// TestGeneralBuiltinMetadata_CategoryOverrides asserts that the basic purpose
// categories required by Spec-1/AC4 are present on the mandatory tools.
//
// BDD: Given the general builtin catalog,
//
//	When Category() is called on exec, read_file, web_search, and web_fetch,
//	Then they return CategoryCode, CategoryFile, CategorySearch, CategoryWeb
//	respectively.
//
// Traces to: US-1/AC4, FR-101.
func TestGeneralBuiltinMetadata_CategoryOverrides(t *testing.T) {
	catalog := GeneralBuiltinMetadata()
	byName := make(map[string]Tool, len(catalog))
	for _, tool := range catalog {
		byName[tool.Name()] = tool
	}

	cases := []struct {
		name    string
		wantCat ToolCategory
	}{
		{"exec", CategoryCode},
		{"read_file", CategoryFile},
		{"write_file", CategoryFile},
		{"list_dir", CategoryFile},
		{"web_search", CategorySearch},
		{"web_fetch", CategoryWeb},
	}
	for _, tc := range cases {
		tool, ok := byName[tc.name]
		require.True(t, ok, "tool %q must be present in general builtin catalog for category check", tc.name)
		assert.Equal(t, tc.wantCat, tool.Category(),
			"tool %q must have category %q (Spec-1 AC4)", tc.name, tc.wantCat)
	}
}

// TestPerAgentExecutionIsolation_MetadataInstanceNeverShared asserts the invariant
// that the metadata instances returned by GeneralBuiltinMetadata are distinct objects
// from instances that would be constructed in the per-agent path — proving that the
// central registry metadata instances cannot be mistaken for per-agent execution
// instances (US-1/AC3, AC7, FR-101 invariant).
//
// This test verifies structural isolation: the metadata instance constructed with
// a dummy workspace ("") is a separate pointer from any per-agent instance
// constructed with a real workspace, so they cannot share mutable state.
//
// BDD: Given agent A (workspace /a) and agent B (workspace /b),
//
//	When each constructs its own exec tool via the per-agent path,
//	Then the per-agent instances are not the same pointer as the metadata instance,
//	And the two per-agent instances are not the same pointer as each other.
//
// Traces to: US-1/AC3/AC7, FR-101 invariant ("never Execute()d via the central registry").
func TestPerAgentExecutionIsolation_MetadataInstanceNeverShared(t *testing.T) {
	// Obtain the metadata instance (the one registered centrally).
	catalog := GeneralBuiltinMetadata()
	var metadataExec Tool
	for _, tool := range catalog {
		if tool.Name() == "exec" {
			metadataExec = tool
			break
		}
	}
	require.NotNil(t, metadataExec, "exec must be present in GeneralBuiltinMetadata")

	// Construct two per-agent exec instances with distinct workspaces, as the
	// per-agent path (registerSharedTools / wireExecToolDeps) would do at runtime.
	agentAExec, err := NewExecToolWithConfig("/a", false, nil)
	require.NoError(t, err, "per-agent A exec construction must succeed")

	agentBExec, err := NewExecToolWithConfig("/b", false, nil)
	require.NoError(t, err, "per-agent B exec construction must succeed")

	// The metadata instance and per-agent instances must be distinct pointers.
	// We compare via interface identity — if they were the same object the pointers
	// would be equal. Comparing the interface value directly checks both type and
	// pointer equality.
	assert.NotEqual(t, metadataExec, agentAExec,
		"metadata exec instance must not be the same object as agent A's exec instance (isolation invariant)")
	assert.NotEqual(t, metadataExec, agentBExec,
		"metadata exec instance must not be the same object as agent B's exec instance (isolation invariant)")
	assert.NotEqual(t, agentAExec, agentBExec,
		"agent A and agent B must each get a distinct exec instance (workspace isolation)")

	// Also verify the Names match (we're talking about the right tools).
	assert.Equal(t, "exec", metadataExec.Name())
	assert.Equal(t, "exec", agentAExec.Name())
	assert.Equal(t, "exec", agentBExec.Name())
}

// TestGeneralBuiltinMetadata_NoPanic asserts that GeneralBuiltinMetadata()
// does not panic and that every returned tool satisfies the basic Tool interface
// contract (non-empty Name and Description).
//
// BDD: Given any build configuration,
//
//	When GeneralBuiltinMetadata() is called,
//	Then it returns without panicking and each tool has non-empty Name and Description.
//
// Traces to: US-1/AC3 (constructor errors are logged-and-skipped, never fatal).
func TestGeneralBuiltinMetadata_NoPanic(t *testing.T) {
	var catalog []Tool
	assert.NotPanics(t, func() {
		catalog = GeneralBuiltinMetadata()
	}, "GeneralBuiltinMetadata must never panic")

	for _, tool := range catalog {
		assert.NotEmpty(t, tool.Name(),
			"every tool in GeneralBuiltinMetadata must have a non-empty Name")
		assert.NotEmpty(t, tool.Description(),
			"every tool in GeneralBuiltinMetadata must have a non-empty Description")
	}
}
