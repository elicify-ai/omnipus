//go:build !windows

package agent

// Round-five items 2/3: refuse every otherwise-valid saved program while a real
// mount record cannot be read in full. The user supplied the new check name
// and cause/log-guidance sentence; no expected text comes from CheckFailedText.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExternalDispatch_IncompleteMountRecordRefusesEveryProgram_R5(t *testing.T) {
	for _, fault := range []string{"unreadable_record", "malformed_json", "dropped_entry"} {
		t.Run(fault, func(t *testing.T) {
			for _, location := range []string{"first_location", "other_location"} {
				t.Run(location, func(t *testing.T) {
					home := t.TempDir()
					t.Setenv(config.EnvHome, home)
					al, ts := newExternalTestLoop(t, "codex", "")
					ts.agent.MaxIterations = extTestEffectiveLimit
					program, marker := t11bNativeProgram(t, t11bNativeCLIs()[1], "codex-"+location)
					t11bSaveRunSettings(t, al, "codex", program, "")
					require.Empty(t, al.GetConfig().Sandbox.AllowedPaths, "isolate the incomplete mount list")
					ws, ok := workspace.FindForAgent(home, ts.agent.ID)
					require.True(t, ok, "instrument: real dispatching agent belongs to this workspace")
					ts.opts.WorkspaceID = ws
					record, err := workspace.MountStorePath(home, ws)
					require.NoError(t, err)
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second) // existing real-driver fixture bound
					defer cancel()
					passed := t.Run("missing_record_before", func(t *testing.T) {
						require.NoFileExists(t, record)
						mounts, readable, dropped := workspace.LoadMountsWithDropStatus(home, ws)
						require.True(t, readable, "control: a missing record is a valid empty state")
						require.False(t, dropped)
						require.Empty(t, mounts)
						_, runErr := runExternalCLISubTurn(ctx, al, ts, "valid program with no mount record")
						require.NoError(t, runErr, "control: initial real native conversation succeeds")
						require.Len(t, t11bNativeCalls(t, marker), 1, "instrument: the actual native driver executed this file")
					})
					require.True(t, passed, "do not mistake a broken driver fixture for a safety refusal")
					before := t11bNativeCalls(t, marker)
					allBefore, err := os.ReadFile(marker + ".all")
					require.NoError(t, err, "instrument: real executable records all starts, including --version")
					t11bDamageRunMountRecord(t, home, ws, fault)
					ts.opts.ExternalCLIResume = true
					t.Run("damaged_record", func(t *testing.T) {
						_, runErr := runExternalCLISubTurn(ctx, al, ts, "must not execute when the mount list is incomplete")
						if assert.Error(t, runErr, "R5: even an unrelated saved program must be refused") {
							text := runErr.Error()
							assert.Regexp(t, `^This agent cannot run: `, text, "R5: preserve the run-refusal form")
							assert.Regexp(t, `\blocation_unverifiable\b`, text, "R5: the distinct failed-check name must reach actual run refusals")
							assert.Contains(t, text, "Omnipus could not read the list of mounted folders, so it cannot check this location. Details are in the server log.",
								"R5: name the unreadable mount list and the server log, not a location the person can repair by moving the program")
							assert.NotContains(t, text, "agent_writable_location", "R5: unknown location is not proven writable")
							assert.NotContains(t, text, "it is inside a folder agents can write to")
						}
						assert.Equal(t, before, t11bNativeCalls(t, marker), "R5: no additional native work process")
						allAfter, readErr := os.ReadFile(marker + ".all")
						require.NoError(t, readErr)
						assert.Equal(t, string(allBefore), string(allAfter), "R5: not even a --version probe may start before refusal")
					})
					require.NoError(t, os.Remove(record))
					t.Run("missing_record_after", func(t *testing.T) {
						require.NoFileExists(t, record)
						_, runErr := runExternalCLISubTurn(ctx, al, ts, "same retained conversation after the record is removed")
						require.NoError(t, runErr, "control: missing record must not leave a sticky refusal")
						require.Len(t, t11bNativeCalls(t, marker), 2, "control: the same file actually resumed after the damaged record was removed")
					})
				})
			}
		})
	}
}

func t11bDamageRunMountRecord(t *testing.T, home, ws, fault string) {
	t.Helper()
	record, err := workspace.MountStorePath(home, ws)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(record), 0o700))
	switch fault {
	case "unreadable_record":
		// Deterministic read failure, including under privileged users. Do not
		// use chmod as a substitute for proving the record really is unreadable.
		require.NoError(t, os.Mkdir(record, 0o700))
		require.DirExists(t, record)
		_, err := os.ReadFile(record)
		require.Error(t, err, "instrument: the existing mount record cannot be read")
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
	require.Empty(t, mounts, "instrument: neither program is actually inside a known mount")
	require.Equal(t, fault == "dropped_entry", ok, "instrument: distinct unreadable and dropped-entry paths")
	require.Equal(t, fault == "dropped_entry", dropped)
}
