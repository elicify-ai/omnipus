package omnpkg_test

// S1b T-9: oracle = export-import-s1-spec.md, US-1 AS-3, Field mapping,
// FR-010/012/014/015/033 and the external-worker SOUL.md invalid_element row.
// Source reads and target lookups are process edges; the package engine is real.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func s1bExternalRecord(runtime map[string]any) map[string]any {
	executor := map[string]any{"kind": "external-cli", "cli": "codex"}
	for key, value := range runtime {
		executor[key] = value
	}
	return map[string]any{
		"id": testSourceID, "name": "Coder", "type": "worker",
		"description": "  Runs Codex tasks  ",
		// D3-8: non-default fields that the external create contract supports.
		"model":               map[string]any{"primary": "m-1", "provider": "openrouter"},
		"max_tool_iterations": float64(40), // JSON numbers decode to float64.
		"figure":              "Robot", "role": "developer", "color": "#6E8BA8",
		"subagents": map[string]any{"executor": executor},
	}
}

func TestS1bExternalExport_StripsRuntimeAndListsSecretNames(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime map[string]any
		secrets []omnpkg.NeedsSecret
		agentMD []byte
	}{
		{name: "runtime absent", secrets: []omnpkg.NeedsSecret{}},
		{
			name: "runtime empty", agentMD: []byte("  worker instructions\n"),
			runtime: map[string]any{"cli_path": "", "cli_args": "", "env_overrides": map[string]string{}},
			secrets: []omnpkg.NeedsSecret{},
		},
		{
			name: "runtime values and every env name removed", agentMD: []byte("  worker instructions\n"),
			runtime: map[string]any{
				"cli_path": "/source-host/private/codex",
				"cli_args": "--key sk-TEST-123",
				"env_overrides": map[string]string{
					"OPENAI_API_KEY": "sk-TEST-456", "EMPTY_KEY": "",
				},
			},
			// FR-014: names, including an empty-valued override, not values.
			secrets: []omnpkg.NeedsSecret{
				{Name: "OPENAI_API_KEY", UsedBy: testSourceID, Kind: "executor_env"},
				{Name: "EMPTY_KEY", UsedBy: testSourceID, Kind: "executor_env"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := newFakeSource()
			home := src.withAgent(t, testSourceID, s1bExternalRecord(tc.runtime), nil, tc.agentMD)
			require.NoError(t, os.Remove(filepath.Join(home, "SOUL.md")), "external source has no prompt")
			before := append([]byte(nil), src.records[testSourceID]...)
			var out bytes.Buffer
			err := omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &out)
			require.NoError(t, err, "FR-010 S1b replaces external_worker_not_supported_yet with a usable export")

			decoded := decodePackagePlain(t, out.Bytes())
			wantOrder := []string{"manifest.json", recordPath(testSourceID)}
			if tc.agentMD != nil {
				wantOrder = append(wantOrder, instructionsPath(testSourceID))
				assert.Equal(t, tc.agentMD, decoded.Payload[instructionsPath(testSourceID)], "FR-015 instructions byte-identical")
			}
			assert.Equal(t, wantOrder, decoded.Order, "external layout has no SOUL.md")
			var record map[string]any
			require.NoError(t, json.Unmarshal(decoded.Payload[recordPath(testSourceID)], &record))
			assert.Equal(t, s1bExternalRecord(nil), record, "only kind and cli survive; all runtime keys must be absent")
			var manifest omnpkg.Manifest
			require.NoError(t, json.Unmarshal(decoded.Payload["manifest.json"], &manifest))
			assert.ElementsMatch(t, tc.secrets, manifest.NeedsSecrets, "FR-014 complete (name, source ID, executor_env) tuples")
			assert.Equal(t, map[string]int{"agent": 1}, manifest.Counts)
			assert.Equal(t, []omnpkg.ManifestElement{{
				Type: "agent", Schema: 1, SourceID: testSourceID, Name: "Coder", Files: wantOrder[1:],
			}}, manifest.Elements)
			assert.Len(t, manifest.Files, len(wantOrder)-1)
			for path, body := range decoded.Payload {
				if path == "manifest.json" {
					continue
				}
				assert.Equal(t, omnpkg.FileInfo{SHA256: sha256hex(body), Size: int64(len(body))}, manifest.Files[path], path)
			}
			for _, forbidden := range []string{"sk-TEST-123", "sk-TEST-456", "/source-host/private/codex"} {
				assert.NotContains(t, out.String(), forbidden, "secret/path in compressed archive")
				for path, body := range decoded.Payload {
					assert.NotContains(t, string(body), forbidden, "secret/path in decompressed entry %s", path)
				}
			}
			assert.Equal(t, before, src.records[testSourceID], "export cannot clean the source in-place")
		})
	}
}

func TestS1bExternalElements_RejectSoulButAcceptPromptlessWorker(t *testing.T) {
	for _, withSoul := range []bool{false, true} {
		name := "promptless external accepted"
		if withSoul {
			name = "external SOUL.md refused as invalid_element"
		}
		t.Run(name, func(t *testing.T) {
			manifest, payload := validManifest(testSourceID, false)
			raw, err := json.Marshal(s1bExternalRecord(nil))
			require.NoError(t, err)
			manifest.Elements[0].Name = "Coder"
			payload[recordPath(testSourceID)] = raw
			manifest.Files[recordPath(testSourceID)] = omnpkg.FileInfo{SHA256: sha256hex(raw), Size: int64(len(raw))}
			if !withSoul {
				delete(payload, soulPath(testSourceID))
				delete(manifest.Files, soulPath(testSourceID))
				manifest.Elements[0].Files = []string{recordPath(testSourceID)}
			}
			m, files, err := omnpkg.ReadPackage(bytes.NewReader(buildPackage(t, manifest, payload)))
			require.NoError(t, err, "fixture must pass format, layout, and checksum checks")
			err = omnpkg.ValidateElements(m, files)
			if withSoul {
				wantCode(t, err, "invalid_element")
			} else {
				require.NoError(t, err, "S1b accepts a worker without a native prompt")
			}
		})
	}
}

func TestS1bExternalImportMapping_DropsAllRuntimeValues(t *testing.T) {
	// BDD: Executor values in a hand-made package are ignored (D5-12).
	raw, err := json.Marshal(s1bExternalRecord(map[string]any{
		"cli_path": "/source-host/private/codex", "cli_args": "--key sk-TEST-123",
		"env_overrides": map[string]string{"OPENAI_API_KEY": "sk-TEST-456"},
	}))
	require.NoError(t, err)
	before := append([]byte(nil), raw...)
	plan, err := omnpkg.MapAgentForImport(raw, testSourceID, newFakeTarget())
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, json.Unmarshal(plan.Record, &record))
	assert.Equal(t, s1bExternalRecord(nil), record, "FR-033: no hand-made runtime values reach create")
	warnings := warningsOf(plan, "runtime_settings_removed")
	require.Len(t, warnings, 1, "FR-043: exactly one runtime-removal warning for this worker")
	assert.Equal(t, testSourceID, warnings[0].ElementSourceID)
	assert.Contains(t, warnings[0].Message, "Edit → Runtime")
	assert.Contains(t, warnings[0].Message, "environment overrides")
	assert.Contains(t, warnings[0].Message, "CLI arguments")
	assert.Equal(t, before, raw, "mapping does not mutate package bytes")
}
