// Omnipus — System Agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Map-coverage regression test (Fix B).
//
// Iterates every tool returned by systools.AllTools(nil, nil) and asserts that
// each tool name has an entry in:
//   - toolPermissions (RBAC map, rbac.go)
//   - toolConfirmation (confirmation map, confirmation.go)
//   - toolCategory (rate-limit category map, ratelimit.go)
//
// A missing entry in any map is a gating-gap bug: the missing tool either
// falls through to an unsafe default (fail-open) or silently gets the wrong
// security posture. This test catches rename/add regressions that forget to
// update one of the three maps.
//
// After fixes A and the metadata-tool additions, all 35 tools pass.

package sysagent

import (
	"testing"

	"github.com/stretchr/testify/assert"

	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
)

// TestMapCoverageAll35Tools asserts that every tool in AllTools() has an entry
// in all three gating maps (toolPermissions, toolConfirmation, toolCategory).
//
// Traces to: gating-gap fix — list_models was missing from toolCategory
// (fail-open); create_skill/edit_skill were absent from toolConfirmation.
//
// BDD: Given the 35 tools returned by AllTools(nil, nil),
//
//	When each tool name is looked up in toolPermissions, toolConfirmation,
//	and toolCategory,
//	Then every lookup must succeed (no missing entries).
func TestMapCoverageAll35Tools(t *testing.T) {
	tools := systools.AllTools(nil, nil)

	if len(tools) != 35 {
		t.Errorf("AllTools returned %d tools; expected 35 — update test if tool count changed intentionally", len(tools))
	}

	for _, tool := range tools {
		name := tool.Name()
		t.Run("tool_"+name, func(t *testing.T) {
			// 1. toolPermissions — RBAC map must cover the tool.
			// CheckRBAC with RoleAdmin returns nil iff the tool is in the map.
			// With RoleAdmin, an unknown tool returns PermissionDeniedError (deny-by-default);
			// a known tool returns nil.
			rbacErr := CheckRBAC(RoleAdmin, name)
			assert.NoError(t, rbacErr,
				"toolPermissions missing %q: admin RBAC check failed (deny-by-default hit — add the tool to toolPermissions in rbac.go)", name)

			// 2. toolConfirmation — confirmation map must cover the tool.
			// RequiresConfirmation returns ConfirmationUI for unknown tools (deny-by-default).
			// For a safe tool that should be ConfirmationNone the unintended ConfirmationUI
			// would block all calls; for create/edit_skill the map was simply absent.
			// We only assert that the name IS in the map (not what level it has).
			_, inConfirmMap := toolConfirmation[name]
			assert.True(t, inConfirmMap,
				"toolConfirmation missing %q: add it to confirmation.go with the correct ConfirmationLevel", name)

			// 3. toolCategory — rate-limit map must cover the tool.
			// A missing entry makes Check() return nil (fail-open — unlimited calls).
			_, inRateMap := toolCategory[name]
			assert.True(t, inRateMap,
				"toolCategory missing %q: add it to ratelimit.go with the correct rateLimitCategory", name)
		})
	}
}
