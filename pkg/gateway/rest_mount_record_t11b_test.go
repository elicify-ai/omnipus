package gateway

// Round-five dispatch items 2/3 and its corrected architect ruling (b): an
// incomplete mount list refuses every program, with a distinct failed CHECK
// name under cli_path_invalid, not a new error-envelope code. Oracles below
// are the user's ruling, never the validator's current output.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

func TestSettingsThirdPartyAgents_IncompleteMountRecordRefusesEveryProgram_R5(t *testing.T) {
	for _, fault := range []string{"unreadable_record", "malformed_json", "dropped_entry"} {
		t.Run(fault, func(t *testing.T) {
			f := t8SettingsAPI(t)
			ws := seedLibraryWorkspace(t, f.api, "T11-B incomplete mount list")
			record, err := workspace.MountStorePath(f.api.homePath, ws)
			require.NoError(t, err)
			programs := []string{t11bWriteExe(t, "codex-first-location"), t11bWriteExe(t, "codex-other-location")}
			save := func(t *testing.T, program string) {
				t.Helper()
				body := t11bSettingsBody(t, gen.CliPreset{}, gen.CliPreset{Path: program}, gen.CliPreset{})
				w := f.call(t, http.MethodPut, t8SettingsRoute, body, "person")
				require.Equal(t, http.StatusOK, w.Code, "control: valid program with no mount record: %s", w.Body.String())
				var disk config.Config
				require.NoError(t, json.Unmarshal(f.disk(t), &disk))
				require.Equal(t, program, disk.Sandbox.ThirdPartyCLI("codex").Path, "control: real save reached disk")
				require.Equal(t, program, f.api.agentLoop.GetConfig().Sandbox.ThirdPartyCLI("codex").Path,
					"control: the running Settings row also refreshed")
			}
			t.Run("missing_record_before", func(t *testing.T) {
				require.NoFileExists(t, record, "control: a missing record, not a mocked empty result")
				mounts, ok, dropped := workspace.LoadMountsWithDropStatus(f.api.homePath, ws)
				require.True(t, ok, "control: missing is a valid empty mount list")
				require.False(t, dropped)
				require.Empty(t, mounts)
				for _, program := range programs {
					save(t, program)
				}
			})
			t11bDamageSettingsMountRecord(t, f.api.homePath, ws, fault)
			for i, program := range programs {
				t.Run([]string{"first_program", "other_program"}[i], func(t *testing.T) {
					body := t11bSettingsBody(t, gen.CliPreset{}, gen.CliPreset{Path: program}, gen.CliPreset{})
					// The existing real-boundary helper asserts HTTP 400, envelope,
					// field, exact sentence/check/entered path, and unchanged bytes.
					f.expectRejected(t, body, "codex", "path", "cli_path_invalid", "location_unverifiable", program,
						"The program path for Codex was not saved: Omnipus could not read the list of mounted folders, so it cannot check this location. Details are in the server log.")
				})
			}
			require.NoError(t, os.Remove(record)) // restores the specified valid empty state
			t.Run("missing_record_after", func(t *testing.T) {
				require.NoFileExists(t, record)
				for _, program := range programs {
					save(t, program)
				}
			})
		})
	}
}

func t11bDamageSettingsMountRecord(t *testing.T, home, ws, fault string) {
	t.Helper()
	record, err := workspace.MountStorePath(home, ws)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(record), 0o700))
	switch fault {
	case "unreadable_record":
		// A directory at the real record filename gives a deterministic read
		// error even for a privileged user; chmod-only denial would not.
		require.NoError(t, os.Mkdir(record, 0o700))
		require.DirExists(t, record)
		_, err := os.ReadFile(record)
		require.Error(t, err, "instrument: the existing record cannot be read as a file")
	case "malformed_json":
		require.NoError(t, os.WriteFile(record, []byte("{"), 0o600))
		require.FileExists(t, record)
	case "dropped_entry":
		data, err := json.Marshal(map[string]any{"workspace_id": ws,
			"mounts": []workspace.Mount{{Name: "damaged", HostPath: "relative-target"}}})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(record, data, 0o600))
		require.FileExists(t, record)
	default:
		t.Fatalf("SETUP: unknown mount-record fault %q", fault)
	}
	mounts, ok, dropped := workspace.LoadMountsWithDropStatus(home, ws)
	require.Empty(t, mounts, "instrument: no known mount contains either candidate program")
	require.Equal(t, fault == "dropped_entry", ok, "instrument: exercise !ok and dropped separately")
	require.Equal(t, fault == "dropped_entry", dropped)
}
