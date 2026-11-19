package omnpkg_test

// RED T-2 — TestFieldMapping_Import (spec TDD plan order 10).
// Spec: FR-033 (stored values follow Field mapping's Import column and D5;
// each drop, tightening, default or local binding MUST be listed), FR-048.
// Scope: the transformations pkg/omnpkg itself owns — skills filtered against
// the target, MCP assignments always dropped, tool policies dropped or
// tightened to the seed, legacy/retired fields not carried, provider and
// max_tool_iterations checked against the target. The create-rule outcomes
// (trimming, fallback pairing, canonical figure/role/colour, name length,
// blank prompt) live in the shared create function (T-3) and are asserted by
// TestImportCreateParity / TestPackagesImport_Route / TestPackagesApply_NewMode.
// Datasets: D5 rows 1–11, D3 rows 5–6 (import column), BDD "References the
// target lacks", "Confirmed import creates…".

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// importRecord marshals a record map for MapAgentForImport.
func importRecord(t *testing.T, rec map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(rec)
	require.NoError(t, err)
	return raw
}

// plannedRecord parses an AgentPlan's record for assertions.
func plannedRecord(t *testing.T, plan *omnpkg.AgentPlan) map[string]any {
	t.Helper()
	var rec map[string]any
	require.NoError(t, json.Unmarshal(plan.Record, &rec))
	return rec
}

func TestFieldMapping_Import(t *testing.T) {
	// D5 1: installed skill kept and bound, listed skill_bound_local.
	t.Run("D5-1 installed skill kept and listed as skill_bound_local", func(t *testing.T) {
		target := newFakeTarget()
		target.installedSkills["writing"] = true
		rec := baseRecord(testSourceID)
		rec["skills"] = []string{"writing"}
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		require.Equal(t, []any{"writing"}, plannedRecord(t, plan)["skills"], "the installed skill is kept")
		bound := warningsOf(plan, "skill_bound_local")
		require.Len(t, bound, 1, "the local binding is listed (FR-033)")
		require.True(t, hasWarningItem(bound, "writing"))
	})

	// D5 2: absent skill dropped before the create rule, listed skill_dropped.
	t.Run("D5-2 absent skill dropped and listed as skill_dropped", func(t *testing.T) {
		target := newFakeTarget()
		rec := baseRecord(testSourceID)
		rec["skills"] = []string{"my-custom", "writing"}
		target.installedSkills["writing"] = true
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		require.Equal(t, []any{"writing"}, plannedRecord(t, plan)["skills"], "only the installed skill survives")
		dropped := warningsOf(plan, "skill_dropped")
		require.NotEmpty(t, dropped, "the drop is listed")
		require.True(t, hasWarningItem(dropped, "my-custom"))
	})

	// D5 3: an invalid slug is dropped like any missing skill.
	t.Run("D5-3 invalid slug dropped and listed", func(t *testing.T) {
		target := newFakeTarget()
		rec := baseRecord(testSourceID)
		rec["skills"] = []string{"Bad Slug!"}
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		require.Nil(t, plannedRecord(t, plan)["skills"], "nothing survives")
		require.NotEmpty(t, warningsOf(plan, "skill_dropped"))
		require.True(t, hasWarningItem(warningsOf(plan, "skill_dropped"), "Bad Slug!"))
	})

	// D5 4: connector assignments are ALWAYS dropped (ADR A3, until S3),
	// listed mcp_binding_dropped — even when the target has the server.
	t.Run("D5-4 mcp assignment always dropped and listed", func(t *testing.T) {
		target := newFakeTarget()
		rec := baseRecord(testSourceID)
		rec["tools"] = map[string]any{
			"mcp": map[string]any{"servers": []any{map[string]any{"id": "github"}}},
		}
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		planned := plannedRecord(t, plan)
		tools, has := planned["tools"].(map[string]any)
		if has {
			mcp, has := tools["mcp"].(map[string]any)
			if has {
				require.Empty(t, mcp["servers"], "no MCP assignment is ever carried")
			}
		}
		require.NotEmpty(t, warningsOf(plan, "mcp_binding_dropped"), "the drop is listed")
	})

	// D5 5: a known tool's policy is kept unchanged, no warning (Dataset D5 row 5, warning "—").
	// Defect fixed: dropped baseRecord's uninstalled skill "writing" (D5-2) and marked provider "openrouter" configured (D5-10); those rows require warnings, this row does not.
	t.Run("D5-5 known tool deny kept without warning", func(t *testing.T) {
		target := newFakeTarget()
		target.knownTools["bash"] = true
		target.providers["openrouter"] = true
		rec := baseRecord(testSourceID)
		delete(rec, "skills")
		rec["tools"] = map[string]any{"builtin": map[string]any{"policies": map[string]any{"bash": "deny"}}}
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		policies := mustMap(t, mustPath(t, plannedRecord(t, plan), "tools", "builtin", "policies"), "policies")
		require.Equal(t, "deny", policies["bash"])
		require.Empty(t, plan.Warnings, "no drop or tightening for a kept policy")
	})

	// D5 6: an unknown tool name is dropped, listed tool_policy_dropped.
	t.Run("D5-6 unknown tool policy dropped and listed", func(t *testing.T) {
		target := newFakeTarget()
		target.knownTools["bash"] = true
		rec := baseRecord(testSourceID)
		rec["tools"] = map[string]any{"builtin": map[string]any{"policies": map[string]any{"retired_tool_x": "allow"}}}
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		policies := mustMap(t, mustPath(t, plannedRecord(t, plan), "tools", "builtin", "policies"), "policies")
		require.NotContains(t, policies, "retired_tool_x", "the unknown tool is dropped")
		require.NotEmpty(t, warningsOf(plan, "tool_policy_dropped"))
	})

	// D5 7: an mcp_* wildcard is not a static tool — dropped and listed.
	t.Run("D5-7 mcp wildcard policy dropped and listed", func(t *testing.T) {
		target := newFakeTarget()
		rec := baseRecord(testSourceID)
		rec["tools"] = map[string]any{"builtin": map[string]any{"policies": map[string]any{"mcp_github_*": "deny"}}}
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		policies := mustMap(t, mustPath(t, plannedRecord(t, plan), "tools", "builtin", "policies"), "policies")
		require.NotContains(t, policies, "mcp_github_*")
		require.NotEmpty(t, warningsOf(plan, "tool_policy_dropped"))
	})

	// D5 8: allow on an always-ask tool becomes ask, listed tightened.
	t.Run("D5-8 allow on always-ask tool tightened to ask and listed", func(t *testing.T) {
		target := newFakeTarget()
		target.knownTools["bash"] = true
		target.alwaysAsk["bash"] = true
		rec := baseRecord(testSourceID)
		rec["tools"] = map[string]any{"builtin": map[string]any{"policies": map[string]any{"bash": "allow"}}}
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		policies := mustMap(t, mustPath(t, plannedRecord(t, plan), "tools", "builtin", "policies"), "policies")
		require.Equal(t, "ask", policies["bash"], "allow on an always-ask tool becomes ask")
		require.NotEmpty(t, warningsOf(plan, "tool_policy_tightened"))
	})

	// D5 9: a tool missing from the map gets the type's seed value, no warning (Dataset D5 row 9, warning "—").
	// Defect fixed: dropped baseRecord's uninstalled skill "writing" (D5-2) and marked provider "openrouter" configured (D5-10); a seeded default is still not a warning.
	t.Run("D5-9 tool missing from the map gets the seed value", func(t *testing.T) {
		target := newFakeTarget()
		target.knownTools["bash"] = true
		target.knownTools["web_search"] = true
		target.providers["openrouter"] = true
		target.seeds["custom"] = map[string]string{"bash": "ask", "web_search": "ask"}
		rec := baseRecord(testSourceID)
		delete(rec, "skills")
		rec["tools"] = map[string]any{"builtin": map[string]any{"policies": map[string]any{"bash": "deny"}}}
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		policies := mustMap(t, mustPath(t, plannedRecord(t, plan), "tools", "builtin", "policies"), "policies")
		require.Equal(t, "deny", policies["bash"])
		require.Equal(t, "ask", policies["web_search"], "missing tools get the seed value for the type")
		require.Empty(t, plan.Warnings, "a seeded default is not a listed warning")
	})

	// D5 10: a provider the target lacks is kept, listed provider_not_configured.
	t.Run("D5-10 provider not configured kept and listed", func(t *testing.T) {
		target := newFakeTarget()
		rec := baseRecord(testSourceID) // model.provider "openrouter"
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		model := mustMap(t, mustPath(t, plannedRecord(t, plan), "model"), "model")
		require.Equal(t, "openrouter", model["provider"], "the provider name is kept")
		require.NotEmpty(t, warningsOf(plan, "provider_not_configured"))
	})

	// D5 11: max_tool_iterations above the target's global is refused.
	t.Run("D5-11 max_tool_iterations over the target global refused", func(t *testing.T) {
		target := newFakeTarget()
		target.globalMax = 200
		rec := baseRecord(testSourceID)
		rec["max_tool_iterations"] = 900
		_, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		wantCode(t, err, omnpkg.CodeElementNotCreatable)
		var pe *omnpkg.PackageError
		require.ErrorAs(t, err, &pe)
		require.Equal(t, "max_tool_iterations", pe.Field)
	})

	// D3-6 import column: legacy model.fallbacks left out, listed
	// field_not_carried.
	t.Run("D3-6 legacy model.fallbacks left out and listed", func(t *testing.T) {
		target := newFakeTarget()
		rec := baseRecord(testSourceID)
		rec["model"] = map[string]any{
			"primary":   "claude-sonnet-4.6",
			"provider":  "openrouter",
			"fallbacks": []string{"x"},
		}
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		model := mustMap(t, mustPath(t, plannedRecord(t, plan), "model"), "model")
		require.NotContains(t, model, "fallbacks", "legacy fallback strings are not carried")
		require.NotEmpty(t, warningsOf(plan, "field_not_carried"))
	})

	// D3-5 import column: retired/unused subagents fields are not carried,
	// even from a hand-made package; no warning is specified for them.
	t.Run("D3-5 hand-made allow_agents and subagents.model not carried", func(t *testing.T) {
		target := newFakeTarget()
		rec := baseRecord(testSourceID)
		rec["subagents"] = map[string]any{
			"allow_agents": []string{"mia"},
			"model":        map[string]any{"primary": "m"},
		}
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		sub, has := plannedRecord(t, plan)["subagents"].(map[string]any)
		if has {
			require.NotContains(t, sub, "allow_agents")
			require.NotContains(t, sub, "model")
		}
	})

	// FR-041 shape check: every warning names its element.
	t.Run("FR-033 warnings carry the element source id", func(t *testing.T) {
		target := newFakeTarget()
		rec := baseRecord(testSourceID)
		rec["skills"] = []string{"my-custom"}
		plan, err := omnpkg.MapAgentForImport(importRecord(t, rec), testSourceID, target)
		require.NoError(t, err)
		require.NotEmpty(t, plan.Warnings)
		for _, w := range plan.Warnings {
			require.Equal(t, testSourceID, w.ElementSourceID, "each warning names its element")
		}
	})
}
