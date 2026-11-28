//go:build goolm && stdjson

package systools_test

// Order 48 follow-up (C-22 row 3) — TestSetConfig_RefusesRetiredKeysThroughSectionWrites.
//
// The retired tool-loading keys are exactly three (skills-tools-redesign-spec.md,
// C-22 row 3 "Moves the guard", C-21 row "retired manifest switches", C-6's
// closing bullet of "Starting levels", BDD S-44):
//
//   - tools.manifest.compressed
//   - tools.manifest.preview_all_lazy
//   - tools.mcp.servers.<id>.deferred
//
// C-22 row 3: "`set_config` and `PUT /config` refuse `tools.manifest.compressed`,
// `tools.manifest.preview_all_lazy` and `tools.mcp.servers.<id>.deferred` with
// 'This setting was replaced by levels in Skills & Tools.'". C-21: set_config
// "refuse[s] a change to them" with that text, "so the key is never unguarded".
//
// The sibling leaf (retired_manifest_keys_red_test.go) pins the three keys
// written one by one. This file pins the other way a retired key changes: a
// WRITE AT AN ANCESTOR whose value carries it — "a change to them" is a change
// however it is spelled: a write at tools.manifest, at tools (for example one
// that changes preview_all_lazy), or at any MCP ancestor of a server's deferred
// is refused with that text.
//
// Spec gap, named rather than guessed: the spec does not say which text wins
// when another guard (the security block list that refuses tools, tools.mcp and
// tools.mcp.servers as ancestors of blocked keys) also refuses the same write.
// These rows read C-22 literally — the text is the refusal of a retired key,
// whichever way the write arrives — so every row asserts BOTH that the write is
// refused AND that the refusal carries the C-22 text. The write is also
// required to store nothing, on disk and in the live config (a refusal that
// left a half-applied section behind is a change).

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"

	"github.com/stretchr/testify/require"
)

func TestSetConfig_RefusesRetiredKeysThroughSectionWrites(t *testing.T) {
	const wantReason = "This setting was replaced by levels in Skills & Tools."

	for _, tc := range []struct {
		name  string
		key   string
		value any
	}{
		// The parent of the two retired manifest keys, once per key.
		{"at tools.manifest, value turns preview_all_lazy on", "tools.manifest",
			map[string]any{"preview_all_lazy": true}},
		{"at tools.manifest, value turns compressed off", "tools.manifest",
			map[string]any{"compressed": false}},
		// The whole tools section, carrying each retired key.
		{"at tools, value carries manifest.compressed", "tools",
			map[string]any{"manifest": map[string]any{"compressed": false}}},
		{"at tools, value carries manifest.preview_all_lazy", "tools",
			map[string]any{"manifest": map[string]any{"preview_all_lazy": true}}},
		{"at tools, value carries a server's deferred", "tools",
			map[string]any{"mcp": map[string]any{"servers": map[string]any{
				"github": map[string]any{"deferred": true}}}}},
		// The MCP ancestors of tools.mcp.servers.<id>.deferred, nearest last.
		{"at tools.mcp, value carries a server's deferred", "tools.mcp",
			map[string]any{"servers": map[string]any{"github": map[string]any{"deferred": true}}}},
		{"at tools.mcp.servers, value carries a server's deferred", "tools.mcp.servers",
			map[string]any{"github": map[string]any{"deferred": true}}},
		{"at one server, value carries its deferred", "tools.mcp.servers.github",
			map[string]any{"deferred": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, cfgPath := newTestDepsWithRealSave(t)
			t.Setenv("OMNIPUS_HOME", deps.Home)

			diskBefore, err := os.ReadFile(cfgPath)
			require.NoError(t, err, "fixture: the seeded config.json must be readable")
			liveBefore, err := json.Marshal(deps.GetCfg())
			require.NoError(t, err, "fixture: the live config must marshal")

			res := systools.NewConfigSetTool(deps).Execute(context.Background(),
				map[string]any{"key": tc.key, "value": tc.value})

			require.True(t, res.IsError,
				"C-22 row 3: a write at %q that carries a retired tool-loading key must be refused, got success: %s",
				tc.key, res.ForLLM)
			require.Contains(t, res.ForLLM, wantReason,
				"C-22 row 3: the refusal of a write at %q carries the spec's text, got: %s", tc.key, res.ForLLM)

			diskAfter, err := os.ReadFile(cfgPath)
			require.NoError(t, err)
			require.Equal(t, string(diskBefore), string(diskAfter),
				"a refused write at %q stores nothing: config.json must be byte-identical", tc.key)
			liveAfter, err := json.Marshal(deps.GetCfg())
			require.NoError(t, err)
			require.Equal(t, string(liveBefore), string(liveAfter),
				"a refused write at %q changes nothing in the live config either", tc.key)
		})
	}

	// Control (S-44: only the retired keys leave): a section write at a
	// section this package does not retire is still accepted — the refusals
	// above are not a blanket refusal of every write under tools.
	t.Run("control: a section write that carries no retired key is accepted", func(t *testing.T) {
		deps, cfgPath := newTestDepsWithRealSave(t)
		t.Setenv("OMNIPUS_HOME", deps.Home)

		res := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
			"key": "tools.read_file", "value": map[string]any{"max_read_file_size": 4096},
		})
		require.False(t, res.IsError,
			"S-44: a write under tools that carries no retired key must stay settable, got: %s", res.ForLLM)

		raw, err := os.ReadFile(cfgPath)
		require.NoError(t, err)
		var stored struct {
			Tools struct {
				ReadFile struct {
					MaxReadFileSize int `json:"max_read_file_size"`
				} `json:"read_file"`
			} `json:"tools"`
		}
		require.NoError(t, json.Unmarshal(raw, &stored))
		require.Equal(t, 4096, stored.Tools.ReadFile.MaxReadFileSize,
			"the accepted section write must have landed in config.json (the control must be able to see a write)")
	})
}
