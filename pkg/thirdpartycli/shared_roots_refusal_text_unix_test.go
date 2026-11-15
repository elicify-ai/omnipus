//go:build darwin || linux

package thirdpartycli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Plan §1.3 + Q37 A: the same truthful cause must work for every listed kind
// of discovery failure, not just mount-record errors. Unexaminable roots have
// their own actual-filesystem test in shared_combined_unix_test.go.
func TestSharedProgramPath_IncompleteRootsRefusalText_Q37(t *testing.T) {
	for _, fault := range []string{"blank_home", "workspace_listing", "unsafe_workspace", "mount_record"} {
		t.Run(fault, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			t.Setenv(config.EnvHome, home)
			program := t11WriteExe(t, t.TempDir(), "codex", 0o755)
			switch fault {
			case "blank_home":
				home = ""
			case "workspace_listing":
				require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces"), []byte("not a directory"), 0o600))
			case "unsafe_workspace":
				require.NoError(t, os.MkdirAll(filepath.Join(home, "workspaces"), 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces", "unsafe..workspace.json"), []byte(`{}`), 0o600))
				ids, listErr := workspace.ListWorkspaceIDs(home)
				require.NoError(t, listErr)
				require.Contains(t, ids, "unsafe..workspace", "instrument: do not silently drop the unsafe id")
			case "mount_record":
				require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: "q37-mount", Name: "Q37"}))
				record, recordErr := workspace.MountStorePath(home, "q37-mount")
				require.NoError(t, recordErr)
				require.NoError(t, os.MkdirAll(filepath.Dir(record), 0o700))
				require.NoError(t, os.WriteFile(record, []byte("{"), 0o600))
			default:
				t.Fatalf("SETUP: unknown commissioned root fault %q", fault)
			}
			roots, rootsErr := thirdpartycli.AgentWritableRoots(home, &config.Config{})
			assert.Error(t, rootsErr, "plan §1.2: incomplete discovery fails loudly")
			assert.Empty(t, roots, "plan §1.2: never return a usable partial list")
			resolved, checkErr := thirdpartycli.CheckProgramPath("codex", program, roots, rootsErr)
			require.Error(t, checkErr)
			require.Empty(t, resolved)
			t11RequireCheck(t, checkErr, "location_unverifiable")
			var ce *thirdpartycli.CheckError
			require.ErrorAs(t, checkErr, &ce)
			assert.Equal(t, program, ce.At)
			assert.Equal(t, t11combined.CodexSaveRefusal, ce.SaveRefusal(), "Q37: exact same approved Save sentence for each cause")
			_, refusal := thirdpartycli.RunProgram("codex", program, roots, rootsErr)
			assert.Regexp(t, `^This agent cannot run: `, refusal)
			assert.Contains(t, refusal, "location_unverifiable")
			assert.Contains(t, refusal, t11combined.LocationUnverifiableCause)
			assert.NotContains(t, refusal, "Omnipus could not read the list of mounted folders")
		})
	}
}
