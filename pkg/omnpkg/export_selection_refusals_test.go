package omnpkg_test

// RED T-2 — TestExportSelection_Refusals (spec TDD plan order 11).
// Spec: FR-010 (exactly one user-created agent; external workers refused in
// S1a), FR-011 (Dataset D4 built-ins refused), FR-019 (export refuses what
// import would refuse: blank native prompt, worker without a description);
// BDD "Built-in agents are refused" (D4), "[S1a only] External worker export
// and unknown element refused until S1b", "Export refusals leave no file"
// (engine level: no bytes on the writer).

import (
	"bytes"
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

	// S1a: an external worker is refused at export, with or without runtime
	// values (FR-010, FR-020; D1 rows 30–31 are the upload twins).
	t.Run("S1a external worker refused at export", func(t *testing.T) {
		for _, withRuntime := range []bool{false, true} {
			src := newFakeSource()
			rec := baseRecord(testSourceID)
			rec["type"] = "worker"
			rec["description"] = "Runs codex tasks"
			exec := map[string]any{"kind": "external-cli", "cli": "codex"}
			if withRuntime {
				exec["cli_path"] = "/usr/local/bin/codex"
				exec["cli_args"] = "--key sk-TEST-123"
				exec["env_overrides"] = map[string]any{"OPENAI_API_KEY": "sk-TEST-456"}
			}
			rec["subagents"] = map[string]any{"executor": exec}
			src.withAgent(t, testSourceID, rec, []byte(testSoulBody), nil)
			var out bytes.Buffer
			err := omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &out)
			wantCode(t, err, omnpkg.CodeExternalWorkerNotSupported)
			require.Empty(t, out.Bytes(), "no package bytes for a refused external worker")
		}
	})
}
