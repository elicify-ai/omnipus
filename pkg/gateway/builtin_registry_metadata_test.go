// Omnipus — Central Builtin Registry Metadata Tests (Issue #350)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the combined (system + general) builtin registry population required
// by Spec-1 US-1/AC2/AC3 (FR-101, SC-101, SC-108, TDD T1/T2/T3).
//
// These tests live in pkg/gateway because they must import both:
//   - pkg/tools (GeneralBuiltinMetadata, BuiltinRegistry)
//   - pkg/sysagent/tools (AllTools — the 33 system.* tools)
//
// pkg/tools cannot import pkg/sysagent/tools (import cycle), so the
// combined registry test belongs in a package that can import both.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestCentralBuiltinRegistry_ContainsGeneralAndSystemTools asserts that a
// BuiltinRegistry populated the same way as gateway boot (system tools first,
// then general-builtin metadata) satisfies Spec-1 SC-101:
//   - contains all general builtins (exec, read_file, web_search, web_fetch minimum)
//   - contains all 33 system.* tools
//   - total count > 35
//   - no tool name appears twice (duplicate guard — prevents the double-count regression)
//
// BDD: Given the corrected gateway boot-time registry population,
//
//	When GET /api/v1/tools is backed by this registry,
//	Then the response contains both general-builtin and system.* entries,
//	And the total count is greater than 35.
//
// Traces to: US-1/AC2, FR-101, SC-101, TDD T1/T3, Issue #350.
func TestCentralBuiltinRegistry_ContainsGeneralAndSystemTools(t *testing.T) {
	// Mirror the gateway boot sequence: system tools first, then general.
	reg := tools.NewBuiltinRegistry()

	// Register all 33 system tools with nil deps (metadata-only mode, same as boot).
	sysToolList := systools.AllTools(nil)
	for _, tool := range sysToolList {
		err := reg.RegisterBuiltin(tool)
		require.NoError(t, err,
			"system tool %q must register without error in metadata mode", tool.Name())
	}
	systemCount := reg.Count()
	assert.Equal(t, 33, systemCount,
		"systools.AllTools must produce exactly 33 system tools (see sysagent/tools/registry.go)")

	// Register general-builtin metadata (deps-free instances, never Execute()d).
	generalToolList := tools.GeneralBuiltinMetadata()
	generalRegistered := 0
	for _, tool := range generalToolList {
		if err := reg.RegisterBuiltin(tool); err != nil {
			// Name conflict with a system tool would be a design bug — fail fast.
			t.Errorf("general builtin %q failed to register: %v", tool.Name(), err)
		} else {
			generalRegistered++
		}
	}
	require.Positive(t, generalRegistered,
		"at least one general builtin must register successfully")

	total := reg.Count()
	assert.Greater(t, total, 35,
		"central BuiltinRegistry must contain more than 35 tools after adding general builtins (SC-101)")
	assert.Equal(t, systemCount+generalRegistered, total,
		"Count must equal systemCount + generalRegistered (no silent duplicates)")

	// Assert the five mandatory general builtin names are present.
	for _, name := range []string{"bash", "read_file", "write_file", "search_web", "fetch_url"} {
		_, ok := reg.Get(name)
		assert.True(t, ok,
			"central registry must contain general builtin %q after population (SC-101)", name)
	}

	// Describe() must return the same count with no duplicate names — guards
	// the double-count regression (Issue #350).
	entries := reg.Describe()
	assert.Len(t, entries, total,
		"Describe() must return an entry for every registered tool")
	seen := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		_, dup := seen[e.Name]
		assert.False(t, dup,
			"Describe() must not list tool %q more than once (Spec-1 AC1, double-count guard)", e.Name)
		seen[e.Name] = struct{}{}
	}
}

// TestCentralBuiltinRegistry_NoDoubleCountSystemTools asserts that registering
// system tools exactly once produces exactly 33 system entries — guards the
// double-count regression described in Issue #350.
//
// BDD: Given systools.AllTools registered exactly once,
//
//	When Count() is called,
//	Then it returns 33 (not 66 or any other value).
//
// Traces to: Issue #350 double-count bug, FR-101, TDD T2.
func TestCentralBuiltinRegistry_NoDoubleCountSystemTools(t *testing.T) {
	reg := tools.NewBuiltinRegistry()
	for _, tool := range systools.AllTools(nil) {
		if err := reg.RegisterBuiltin(tool); err != nil {
			t.Logf("skipping system tool %q (err: %v)", tool.Name(), err)
		}
	}
	assert.Equal(t, 33, reg.Count(),
		"system tools registered once must produce exactly 33 entries — no double-count (Issue #350)")
}

// TestHandleToolsRegistry_WithCombinedRegistry verifies that HandleToolsRegistry
// (GET /api/v1/tools) returns both general and system tool entries when the central
// builtinRegistry is populated the same way as the corrected gateway boot path.
//
// BDD: Given a restAPI with a correctly-populated builtinRegistry (system + general),
//
//	When GET /api/v1/tools is called,
//	Then the response is a JSON array with more than 36 entries,
//	And the array contains entries for exec and list_agents.
//
// Traces to: US-1/AC2, FR-101, SC-101, TDD T3, Issue #350.
func TestHandleToolsRegistry_WithCombinedRegistry(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Build a combined registry (system + general builtins) and wire it into the api.
	reg := tools.NewBuiltinRegistry()
	for _, tool := range systools.AllTools(nil) {
		if err := reg.RegisterBuiltin(tool); err != nil {
			t.Logf("system tool %q skipped: %v", tool.Name(), err)
		}
	}
	for _, tool := range tools.GeneralBuiltinMetadata() {
		if err := reg.RegisterBuiltin(tool); err != nil {
			t.Logf("general builtin %q skipped: %v", tool.Name(), err)
		}
	}
	api.builtinRegistry = reg

	r := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleToolsRegistry(w, r)

	require.Equal(t, http.StatusOK, w.Code,
		"GET /api/v1/tools must return 200: %s", w.Body)

	var entries []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries),
		"response must unmarshal as a JSON array")

	assert.Greater(t, len(entries), 36,
		"GET /api/v1/tools must return more than 36 entries with the combined registry (SC-101)")

	// Verify both general and system entries are present.
	nameSet := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		name, _ := e["name"].(string)
		if name != "" {
			nameSet[name] = struct{}{}
		}
	}
	for _, expected := range []string{"bash", "read_file", "search_web", "list_agents"} {
		assert.Contains(t, nameSet, expected,
			"response must include tool %q (system + general combined registry check)", expected)
	}

	// No tool may appear twice.
	assert.Equal(t, len(entries), len(nameSet),
		"each tool must appear exactly once in GET /api/v1/tools (no duplicate names)")
}
