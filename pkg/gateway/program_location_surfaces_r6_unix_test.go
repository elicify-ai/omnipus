//go:build !windows

package gateway

// T11 §3.2 / Q28 / Q37, gate W2: preview and both sysagent warning surfaces
// enforce the same writable-root union as R7. Classification oracles are
// literals from the spec, never the checker output. Q48 text is not pinned.

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t11r6WarningKind(t *testing.T, warning *string, want string) {
	t.Helper()
	if want == "" {
		require.Nil(t, warning, "outside positive control has no program warning")
		return
	}
	require.NotNil(t, warning, "the actual caller must expose its refusal")
	got := regexp.MustCompile(`\b(agent_writable_location|location_unverifiable)\b`).FindString(*warning)
	assert.Equal(t, want, got, "specified refusal kind; no assertion of Q48's path-bearing text")
}

func t11r6Preview(t *testing.T, f *t8SettingsFixture, program, check string) {
	t.Helper()
	w := f.call(t, http.MethodPost, "/api/v1/agents/executor-preview",
		gen.ExecutorCommandPreviewRequest{Cli: gen.ExternalCliToolClaudeCode}, "person")
	if check == "" {
		require.Equal(t, http.StatusOK, w.Code, "real preview positive control: %s", w.Body.String())
		var out gen.ExecutorCommandPreviewResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
		assert.Equal(t, program, out.Binary, "preview uses this row's saved program")
		return
	}
	require.Equal(t, http.StatusBadRequest, w.Code, "preview must refuse this location: %s", w.Body.String())
	var out gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	t11r6WarningKind(t, &out.Error, check)
}

func t11r6GetWarning(t *testing.T, deps *systools.Deps, id, program, check string) {
	t.Helper()
	result := systools.NewAgentGetTool(deps).Execute(t.Context(), map[string]any{
		"id": id, "fields": []any{"executor", "runnable", "program_warning"},
	})
	require.NotNil(t, result)
	require.False(t, result.IsError, "real get_agent must return its read model: %s", result.ForLLM)
	var out gen.AgentDetail
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &out))
	require.Equal(t, id, out.Id)
	require.NotNil(t, out.Runnable)
	assert.Equal(t, check == "", *out.Runnable, "runnable agrees with the independently expected classification")
	require.NotNil(t, out.Executor)
	require.NotNil(t, out.Executor.EffectiveCliPath)
	assert.Equal(t, program, *out.Executor.EffectiveCliPath)
	t11r6WarningKind(t, out.ProgramWarning, check)
	t11r6WarningKind(t, out.Executor.ProgramWarning, check)
}

func t11r6ExecutorWarning(t *testing.T, deps *systools.Deps, store *agentstore.Store, id, stage, check string) {
	t.Helper()
	state, err := store.ReadState(id)
	require.NoError(t, err)
	args := "--model r6-" + stage
	result := systools.NewAgentUpdateTool(deps).Execute(t.Context(), map[string]any{
		"id": id, "revision": state.Revision, "cli_args": args,
	})
	require.NotNil(t, result)
	require.False(t, result.IsError, "real executor-settings update returns its outcome: %s", result.ForLLM)
	after, err := store.ReadState(id)
	require.NoError(t, err)
	require.Equal(t, args, after.Agent.Subagents.Executor.CLIArgs, "instrument: the actual update reached executor settings")
	var carrier map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &carrier))
	require.Contains(t, carrier, "executor", "the actual typed executor-settings outcome is required")
	var out gen.AgentExecutorSettingsResult
	require.NoError(t, json.Unmarshal(carrier["executor"], &out))
	require.NotNil(t, out.CliArgs)
	assert.Equal(t, []string{"--model", "r6-" + stage}, out.CliArgs.Argv)
	t11r6WarningKind(t, out.ProgramWarning, check)
}

func TestCombinedProgramLocation_PreviewAndSysagentWarnings_R7_W2(t *testing.T) {
	marker := filepath.Join(t11cCanonicalDir(t), "starts")
	source := t11BuildMarkerProgram(t, marker)
	// Compilation is complete. No installed CLI can be found by this table.
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct { // not-wire-format: independently specified test classifications
		name, check string
	}{
		{"outside", ""},
		{"data_home", "agent_writable_location"},
		{"agent_home", "agent_writable_location"},
		{"allowed_path", "agent_writable_location"},
		{"mount", "agent_writable_location"},
		{"incomplete_list", "location_unverifiable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := t8SettingsAPI(t)
			chain := f.handler
			f.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.RemoteAddr = t.Name() + ":0"
				chain.ServeHTTP(w, r)
			})
			outside := t11cProgramAt(t, source, t11cCanonicalDir(t))
			agentHome, allowed, mountHost := t11cCanonicalDir(t), t11cCanonicalDir(t), t11cCanonicalDir(t)
			ws := seedLibraryWorkspace(t, f.api, "R7 preview and tool warning agreement")
			mount, _, err := workspace.CreateMount(f.api.homePath, ws, "agreement", mountHost)
			require.NoError(t, err)
			worker := config.AgentConfig{ID: "r7-warning-worker", Name: "R7 Warning Worker", Type: config.AgentTypeWorker,
				Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"}}}
			store := agentstore.New(f.api.homePath)
			_, err = store.CreateState(worker.ID, &worker, "")
			require.NoError(t, err)
			t11cSaveAllowed(t, f, outside) // Save/reload before installing the exact R7 union.
			t11cSetConfig(t, f, func(cfg *config.Config) {
				cfg.Agents.List = []config.AgentConfig{worker, {ID: "agreement-home", Home: agentHome}}
				cfg.Sandbox.AllowedPaths = []string{allowed}
			})
			deps := &systools.Deps{Home: f.api.homePath, GetCfg: f.api.agentLoop.GetConfig}
			// Each row proves all three REAL surfaces accept this executable
			// before changing only its configured location/list completeness.
			t.Run("positive_control", func(t *testing.T) {
				t11r6Preview(t, f, outside, "")
				t11r6GetWarning(t, deps, worker.ID, outside, "")
				t11r6ExecutorWarning(t, deps, store, worker.ID, "positive", "")
			})
			program := outside
			switch tc.name {
			case "data_home":
				program = t11cProgramAt(t, source, filepath.Join(f.api.homePath, "bin"))
			case "agent_home":
				program = t11cProgramAt(t, source, agentHome)
			case "allowed_path":
				program = t11cProgramAt(t, source, allowed)
			case "mount":
				program = t11cProgramAt(t, source, mount.HostPath)
			case "incomplete_list":
				t11bDamageSettingsMountRecord(t, f.api.homePath, ws, "dropped_entry")
			}
			// Existing installs can become unsafe after a mount/home grant.
			// Fixture the saved configuration; do not pretend Save accepts it.
			t11cSetConfig(t, f, func(cfg *config.Config) {
				cfg.Sandbox.ThirdPartyAgents.CLIs["claude-code"] = config.ThirdPartyCLIConfig{Path: program}
			})
			t.Run("preview", func(t *testing.T) { t11r6Preview(t, f, program, tc.check) })
			t.Run("get_agent", func(t *testing.T) { t11r6GetWarning(t, deps, worker.ID, program, tc.check) })
			t.Run("executor_settings", func(t *testing.T) {
				t11r6ExecutorWarning(t, deps, store, worker.ID, "changed", tc.check)
			})
			if tc.name == "incomplete_list" {
				record, err := workspace.MountStorePath(f.api.homePath, ws)
				require.NoError(t, err)
				require.NoError(t, os.Remove(record))
				t.Run("recovery", func(t *testing.T) {
					t11r6Preview(t, f, program, "")
					t11r6GetWarning(t, deps, worker.ID, program, "")
					t11r6ExecutorWarning(t, deps, store, worker.ID, "recovered", "")
				})
			}
			assert.NoFileExists(t, marker, "preview and warning reads never execute the program")
		})
	}
	// Positive marker control, AFTER every no-spawn assertion, executes only
	// the harmless compiled fixture. An absent marker cannot pass vacuously.
	require.NoError(t, exec.CommandContext(t.Context(), source).Run())
	require.FileExists(t, marker)
}
