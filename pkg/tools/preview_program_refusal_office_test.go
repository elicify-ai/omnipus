//go:build darwin || linux

package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T-85 / §3.2: a scripted approver must NEVER be consulted for an overlap.
// Execute is real, not just a call to a classifier: the marker detects a
// refusal check that runs too late, after the command already ran.
func TestOfficePreviewShellRefusesProgramWriteApproval(t *testing.T) {
	if !officepreview.AtPackagedExecutable(t) {
		return
	}
	for _, answer := range []string{"Allow", "Allow_once"} {
		for _, shape := range []string{"root", "file_inside", "contains", "empty_packaged_slot", "link"} {
			t.Run(answer+"/"+shape, func(t *testing.T) {
				tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)
				requester.recordGrant = answer == "Allow"
				home := config.OmnipusHomeDir()
				root := filepath.Join(t.TempDir(), "office")
				officepreview.TrustedOffice(t, home, root)
				root, err := filepath.EvalSymlinks(root)
				require.NoError(t, err)
				path := root
				switch shape {
				case "file_inside":
					path = filepath.Join(root, "new.txt")
				case "contains":
					path = filepath.Dir(root)
				case "empty_packaged_slot":
					slotResolver, slotErr := component.NewResolver(home)
					require.NoError(t, slotErr)
					root = filepath.Clean(slotResolver.PackagedCandidates(component.KindOffice)[0])
					require.NoError(t, os.MkdirAll(root, 0700))
					require.NoFileExists(t, filepath.Join(root, "manifest.json"), "instrument: empty slots are protected without a manifest")
					path = root
				case "link":
					path = filepath.Join(t.TempDir(), "office-link")
					require.NoError(t, os.Symlink(root, path))
				}
				resolved, err := filepath.EvalSymlinks(filepath.Dir(path))
				if shape != "file_inside" {
					resolved, err = filepath.EvalSymlinks(path)
				} else {
					resolved = filepath.Join(resolved, filepath.Base(path))
				}
				require.NoError(t, err)
				marker := filepath.Join(root, "command-ran")
				command := "touch '" + path + "'; printf 'ran' > '" + marker + "'"
				before := tool.approvalGrants.AllPathGrants()
				result := tool.Execute(ctx, map[string]any{"command": command})
				assert.Equal(t, 0, requester.callCount(), "T-85: overlapping write is never offered for approval")
				assert.Equal(t, before, tool.approvalGrants.AllPathGrants(), "no Allow/Allow once grant may be recorded")
				assert.NoFileExists(t, marker, "command must not run before refusal")
				require.NotNil(t, result)
				assert.True(t, result.IsError)
				assert.Equal(t, officepreview.ProgramShellRefusal(resolved, "LibreOffice", root), result.ForLLM)
			})
		}
	}
	t.Run("unrelated_write_asks_once", func(t *testing.T) {
		tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)
		outside, err := filepath.EvalSymlinks(t.TempDir())
		require.NoError(t, err)
		_, result := tool.enforceShellPermissionMode(ctx, "printf 'control' > '"+filepath.Join(outside, "control")+"'")
		require.Nil(t, result)
		require.Equal(t, 1, requester.callCount(), "instrument: this requester really sees outside writes")
	})
	t.Run("program_read_not_refused", func(t *testing.T) {
		tool, ctx, requester, _ := permTestFixture(t, ShellModeAuto, true)
		root := filepath.Join(t.TempDir(), "office")
		file := officepreview.TrustedOffice(t, config.OmnipusHomeDir(), root)
		_, result := tool.enforceShellPermissionMode(ctx, "cat '"+file+"'")
		require.Nil(t, result, "S2 refuses writes, not reads")
		require.Equal(t, 0, requester.callCount())
	})
}

// T-86's actual tool surface must use the SAME fixed sentence as REST, not
// merely wrap the workspace error with request_mount's generic prefix.
func TestOfficePreviewRequestMountRefusesProgramFolders(t *testing.T) {
	for _, shape := range []string{"root", "inside", "contains", "link"} {
		t.Run(shape, func(t *testing.T) {
			home, wsID, _ := newMountFixture(t)
			t.Setenv(config.EnvHome, home)
			root := filepath.Join(t.TempDir(), "office")
			officepreview.TrustedOffice(t, home, root)
			root, err := filepath.EvalSymlinks(root)
			require.NoError(t, err)
			target := root
			switch shape {
			case "inside":
				target = filepath.Join(root, "subfolder")
				require.NoError(t, os.Mkdir(target, 0700))
			case "contains":
				target = filepath.Dir(root)
			case "link":
				target = filepath.Join(t.TempDir(), "office-link")
				require.NoError(t, os.Symlink(root, target))
			}
			res := NewRequestMountTool(home).Execute(WithWorkspaceID(context.Background(), wsID), map[string]any{"host_path": target, "reason": "T-86 harmless refusal probe"})
			assert.True(t, res.IsError)
			assert.Equal(t, officepreview.ProgramMountRefusal("LibreOffice", root), res.ForLLM)
			mounts, _ := workspace.LoadMounts(home, wsID)
			assert.Empty(t, mounts, "refused tool call must not write any mount record")
			work, err := workspace.EnsureWorkDir(home, wsID)
			require.NoError(t, err)
			entries, err := os.ReadDir(work)
			require.NoError(t, err)
			// EnsureWorkDir auto-initializes the git-evidence repository
			// (FR-151) unless an ancestor of the home already is one — so
			// `.git` is the only legitimate entry, and its presence depends
			// on where the test's temporary home lives. Anything else —
			// above all the mount's own link — must not exist.
			for _, entry := range entries {
				assert.Equal(t, ".git", entry.Name(),
					"refused tool call must create nothing but the evidence layer's own repository")
			}
		})
	}
}
