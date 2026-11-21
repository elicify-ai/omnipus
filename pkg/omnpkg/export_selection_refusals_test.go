package omnpkg_test

// T-2 — TestExportSelection_Refusals (spec TDD plan order 11).
// Spec: FR-010 (exactly one user-created agent, including external workers
// in S1b), FR-011 (Dataset D4 built-ins refused), FR-019 (blank native prompt,
// worker without a description). Test-plan row 33 replaces the former S1a
// external refusal with the runtime-stripping acceptance case.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// d4CoreIDs is Dataset D4 rows 1–8: the shipped agents.
var d4CoreIDs = []string{"mia", "jim", "omnipus", "worker", "planner", "researcher", "judge", "plansupervisor"}

func TestExportSelection_Refusals(t *testing.T) {
	t.Run("D4-1..8 built-in agents refused as core_agent_not_exportable", func(t *testing.T) {
		for _, id := range d4CoreIDs {
			src := newFakeSource()
			src.coreIDs[id] = true
			var out bytes.Buffer
			err := omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{id}}, &out)
			wantCode(t, err, omnpkg.CodeCoreAgentNotExportable)
			require.Empty(t, out.Bytes(), "no bytes for a refused export (D4)")
		}
	})

	t.Run("D4-9 user-created Subagent exports", func(t *testing.T) {
		src := newFakeSource()
		rec := baseRecord(testSourceID)
		rec["type"] = "worker"
		rec["description"] = "Summarises texts"
		src.withAgent(t, testSourceID, rec, []byte(testSoulBody), nil)
		var out bytes.Buffer
		require.NoError(t, omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &out))
		require.NotEmpty(t, out.Bytes())
	})

	// FR-019 / D10-7: a native agent whose prompt is blank is refused.
	t.Run("FR-019 blank native prompt refused as agent_prompt_missing", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID, baseRecord(testSourceID), []byte("\n  \n"), nil) // whitespace-only SOUL.md
		var out bytes.Buffer
		err := omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &out)
		wantCode(t, err, omnpkg.CodeAgentPromptMissing)
		require.Empty(t, out.Bytes())
	})

	// FR-019: a worker without a description is refused.
	t.Run("FR-019 worker without description refused as element_not_creatable", func(t *testing.T) {
		src := newFakeSource()
		rec := baseRecord(testSourceID)
		rec["type"] = "worker"
		rec["description"] = ""
		src.withAgent(t, testSourceID, rec, []byte(testSoulBody), nil)
		var out bytes.Buffer
		err := omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &out)
		wantCode(t, err, omnpkg.CodeElementNotCreatable)
		var pe *omnpkg.PackageError
		require.ErrorAs(t, err, &pe)
		require.Equal(t, "description", pe.Field)
		require.Empty(t, out.Bytes())
	})

	// S1b replaces the S1a refusal (test-plan row 33, FR-010/012/014).
	// Keep both runtime variants, using a valid promptless external record
	// rather than the native-only fields in baseRecord.
	t.Run("S1b external worker exports without runtime values", func(t *testing.T) {
		for _, withRuntime := range []bool{false, true} {
			src := newFakeSource()
			var runtime map[string]any
			wantSecrets := []omnpkg.NeedsSecret{}
			if withRuntime {
				runtime = map[string]any{
					"cli_path": "/usr/local/bin/codex", "cli_args": "--key sk-TEST-123",
					"env_overrides": map[string]any{"OPENAI_API_KEY": "sk-TEST-456"},
				}
				wantSecrets = []omnpkg.NeedsSecret{{Name: "OPENAI_API_KEY", UsedBy: testSourceID, Kind: "executor_env"}}
			}
			home := src.withAgent(t, testSourceID, s1bExternalRecord(runtime), nil, nil)
			require.NoError(t, os.Remove(filepath.Join(home, "SOUL.md")), "external source has no prompt")
			before := append([]byte(nil), src.records[testSourceID]...)
			var out bytes.Buffer
			err := omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &out)
			require.NoError(t, err, "FR-010: S1b supports external-worker export")
			decoded := decodePackagePlain(t, out.Bytes())
			require.Equal(t, []string{"manifest.json", recordPath(testSourceID)}, decoded.Order, "external package has no SOUL.md")
			var record map[string]any
			require.NoError(t, json.Unmarshal(decoded.Payload[recordPath(testSourceID)], &record))
			require.Equal(t, s1bExternalRecord(nil), record, "only executor kind and CLI survive")
			var manifest omnpkg.Manifest
			require.NoError(t, json.Unmarshal(decoded.Payload["manifest.json"], &manifest))
			require.Equal(t, wantSecrets, manifest.NeedsSecrets, "complete secret-name tuples, never values")
			require.Equal(t, map[string]int{"agent": 1}, manifest.Counts)
			for _, value := range []string{"/usr/local/bin/codex", "sk-TEST-123", "sk-TEST-456"} {
				require.NotContains(t, out.String(), value, "runtime value in compressed archive")
				for path, body := range decoded.Payload {
					require.NotContains(t, string(body), value, "runtime value in decoded entry %s", path)
				}
			}
			require.Equal(t, before, src.records[testSourceID], "export does not clean the source in place")
		}
	})
}
