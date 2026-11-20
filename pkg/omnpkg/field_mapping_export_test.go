package omnpkg_test

// RED T-2 — TestFieldMapping_Export (spec TDD plan order 9).
// Spec: FR-002 (format, version, scope, includes_sessions, counts), FR-012
// (packaged record follows Field mapping's Export column), FR-015 (SOUL.md /
// AGENT.md byte-identical; AGENT.md only when present), FR-017 (no file left
// in the data folder — at engine level: nothing but the writer's bytes);
// BDD "Export a Main agent with prompt and instructions", "The exported record
// follows Field mapping" (Dataset D3 export column), "Export an agent with no
// AGENT.md".
//
// JSON names are the spec's "stored JSON name" column (pkg/config tags).

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// fullRecord is a record carrying a non-default value for every Export-column
// row of Field mapping, including the removed rows (D3 5).
func fullRecord(id string) map[string]any {
	return map[string]any{
		"id":          id,
		"name":        "Writer",
		"description": "  Drafts  ", // D3 3: kept raw at export (import trims)
		"type":        "custom",
		"workspace":   "/home/dev/.omnipus/agents/" + id, // removed (ADR F2)
		"created_at":  "2026-01-02T03:04:05Z",            // removed
		"updated_at":  "2026-02-03T04:05:06Z",            // removed
		"locked":      false,                             // removed
		"model": map[string]any{
			"primary":   "claude-sonnet-4.6",
			"provider":  "openrouter",
			"fallbacks": []string{"x"}, // D3 6: legacy strings kept at export
		},
		"fallback_models": []any{
			map[string]any{"model": "claude-opus-4.7", "provider": "openrouter", "reasoning_effort": "high"},
		},
		"reasoning_effort":    "high",
		"max_tool_iterations": 40,
		"model_params":        map[string]any{"temperature": 0.2, "max_tokens": 4096},
		"skills":              []string{"writing"},
		"skill_grants":        map[string]any{"writing": "gen-1"}, // removed (rebuilt by the store)
		"tools": map[string]any{
			"builtin": map[string]any{"policies": map[string]any{"bash": "deny"}},
			"mcp":     map[string]any{"servers": []any{map[string]any{"id": "github"}}},
		},
		"voice":                   "alloy",
		"figure":                  "Robot",
		"role":                    "editor",
		"color":                   "#1f6feb",
		"memory_enabled":          false,
		"context_window_override": 64000,
		"subagents": map[string]any{ // allow_agents/model removed (ADR A3); executor is S1b
			"allow_agents": []string{"mia"},
			"model":        map[string]any{"primary": "m"},
		},
	}
}

func TestFieldMapping_Export(t *testing.T) {
	exportWriter := func(t *testing.T, src *fakeSource, id string) *bytes.Buffer {
		t.Helper()
		var out bytes.Buffer
		require.NoError(t, omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{id}}, &out))
		return &out
	}

	t.Run("BDD package layout: manifest first, agent files under omnipus/agents/<id>", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID, fullRecord(testSourceID), []byte(testSoulBody), []byte(testAgentBody))
		dec := decodePackagePlain(t, exportWriter(t, src, testSourceID).Bytes())
		require.Equal(t, []string{
			omnpkg.ManifestEntry, recordPath(testSourceID), soulPath(testSourceID), instructionsPath(testSourceID),
		}, dec.Order, "FR-001: manifest.json first, then exactly the agent's files")
	})

	t.Run("BDD manifest fields: format, version, scope, schema, counts, per-file SHA-256", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID, fullRecord(testSourceID), []byte(testSoulBody), []byte(testAgentBody))
		dec := decodePackagePlain(t, exportWriter(t, src, testSourceID).Bytes())
		var m omnpkg.Manifest
		require.NoError(t, json.Unmarshal(dec.Payload[omnpkg.ManifestEntry], &m))
		require.Equal(t, "omnpkg", m.Format)
		require.Equal(t, 1, m.FormatVersion, "FR-002: format_version 1")
		require.Equal(t, "custom", m.Scope, "FR-002: scope custom")
		require.False(t, m.IncludesSessions, "FR-002: includes_sessions false")
		require.Equal(t, 1, m.Counts["agent"], "FR-002: counts matching the elements")
		require.Len(t, m.Elements, 1)
		require.Equal(t, "agent", m.Elements[0].Type)
		require.Equal(t, 1, m.Elements[0].Schema, "BDD: one agent element with schema 1")
		require.Equal(t, testSourceID, m.Elements[0].SourceID)
		require.Equal(t, "Writer", m.Elements[0].Name)
		require.Len(t, m.Files, 3)
		for name, body := range dec.Payload {
			if name == omnpkg.ManifestEntry {
				continue
			}
			require.Equal(t, sha256hex(body), m.Files[name].SHA256, "SHA-256 per file")
			require.Equal(t, int64(len(body)), m.Files[name].Size, "size per file")
		}
		require.Empty(t, m.NeedsSecrets, "a native S1a export carries no needs_secrets")
	})

	t.Run("D3-5 removed rows are absent from the packaged record", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID, fullRecord(testSourceID), []byte(testSoulBody), []byte(testAgentBody))
		dec := decodePackagePlain(t, exportWriter(t, src, testSourceID).Bytes())
		var rec map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(dec.Payload[recordPath(testSourceID)], &rec))
		for _, removed := range []string{"workspace", "created_at", "updated_at", "locked", "skill_grants"} {
			require.NotContains(t, rec, removed, "Field mapping: %q is removed at export", removed)
		}
		var sub map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec["subagents"], &sub))
		require.NotContains(t, sub, "allow_agents", "subagents.allow_agents is removed (ADR A3)")
		require.NotContains(t, sub, "model", "subagents.model is removed (ADR A3)")
	})

	t.Run("D3 kept rows are present with their stored values", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID, fullRecord(testSourceID), []byte(testSoulBody), []byte(testAgentBody))
		dec := decodePackagePlain(t, exportWriter(t, src, testSourceID).Bytes())
		var rec map[string]any
		require.NoError(t, json.Unmarshal(dec.Payload[recordPath(testSourceID)], &rec))
		require.Equal(t, testSourceID, rec["id"], "id kept (source ID)")
		require.Equal(t, "Writer", rec["name"], "name kept")
		require.Equal(t, "  Drafts  ", rec["description"], "D3-3: description kept raw at export")
		require.Equal(t, "custom", rec["type"], "type kept")
		require.Equal(t, "alloy", rec["voice"], "voice kept at export")
		params := mustMap(t, mustPath(t, rec, "model_params"), "model_params")
		require.InDelta(t, 0.2, mustFloat(t, params["temperature"], "temperature"), 1e-9)
		require.Equal(t, float64(4096), params["max_tokens"])
		require.Equal(t, float64(40), rec["max_tool_iterations"])
		require.Equal(t, "high", rec["reasoning_effort"])
		require.Equal(t, false, rec["memory_enabled"])
		require.Equal(t, float64(64000), rec["context_window_override"])
		require.Equal(t, "#1f6feb", rec["color"])
		require.Equal(t, "Robot", rec["figure"])
		require.Equal(t, "editor", rec["role"])

		model := mustMap(t, mustPath(t, rec, "model"), "model")
		require.Equal(t, "claude-sonnet-4.6", model["primary"], "model.primary kept")
		require.Equal(t, "openrouter", model["provider"], "model.provider kept (name only)")
		require.Equal(t, []any{"x"}, model["fallbacks"], "D3-6: legacy model.fallbacks kept at export")

		fbs := mustSlice(t, mustPath(t, rec, "fallback_models"), "fallback_models")
		require.Len(t, fbs, 1)
		fb := mustMap(t, fbs[0], "fallback_models[0]")
		require.Equal(t, "claude-opus-4.7", fb["model"])
		require.Equal(t, "openrouter", fb["provider"])
		require.Equal(t, "high", fb["reasoning_effort"], "fallback_models[].reasoning_effort kept")

		tools := mustMap(t, mustPath(t, rec, "tools"), "tools")
		policies := mustMap(t, mustPath(t, tools, "builtin", "policies"), "policies")
		require.Equal(t, "deny", policies["bash"], "tools.builtin.policies kept")
		servers := mustSlice(t, mustPath(t, tools, "mcp", "servers"), "servers")
		require.Len(t, servers, 1, "tools.mcp.servers kept so the preview can list them (dropped at import)")
		require.Equal(t, "github", mustMap(t, servers[0], "servers[0]")["id"])
		require.Equal(t, []any{"writing"}, rec["skills"], "skills kept")
	})

	t.Run("D3-1/D3-2 SOUL.md and AGENT.md byte-identical", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID, fullRecord(testSourceID), []byte(testSoulBody), []byte(testAgentBody))
		dec := decodePackagePlain(t, exportWriter(t, src, testSourceID).Bytes())
		require.Equal(t, []byte(testSoulBody), dec.Payload[soulPath(testSourceID)], "D3-1")
		require.Equal(t, []byte(testAgentBody), dec.Payload[instructionsPath(testSourceID)], "D3-2")
	})

	t.Run("FR-017 export writes nothing but the caller's bytes", func(t *testing.T) {
		// The engine gets only the fake source (a temp dir) and the writer;
		// this asserts no stray sibling file appears beside the home folder
		// while exporting (the data-folder claim is pinned by T-5 integration).
		src := newFakeSource()
		home := src.withAgent(t, testSourceID, fullRecord(testSourceID), []byte(testSoulBody), []byte(testAgentBody))
		exportWriter(t, src, testSourceID)
		entries, err := os.ReadDir(home)
		require.NoError(t, err)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		require.ElementsMatch(t, []string{"SOUL.md", "AGENT.md"}, names, "export leaves no file behind")
	})
}
