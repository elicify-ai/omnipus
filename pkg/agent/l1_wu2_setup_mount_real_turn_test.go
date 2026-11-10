//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func l1WU2DecodeFilesystemRefusal(t *testing.T, tool, result string) gen.PermissionDenied {
	t.Helper()
	payload := result
	if tool == "read_file" {
		// PromptGuard.wrapUntrusted preserves these exact delimiters on
		// read results. Keep/assert that boundary; decode only its body.
		const open = "[UNTRUSTED_CONTENT]\n"
		const closeTag = "\n[/UNTRUSTED_CONTENT]"
		require.True(t, strings.HasPrefix(result, open))
		require.True(t, strings.HasSuffix(result, closeTag))
		payload = strings.TrimSuffix(strings.TrimPrefix(result, open), closeTag)
	}
	var refusal gen.PermissionDenied
	require.NoError(t, json.Unmarshal([]byte(payload), &refusal), "denied tool must return the actual structured filesystem refusal, not unrelated prose: %s", result)
	return refusal
}

// FR-006 / amendment §2 A8 / CHECK F05: run a REAL turn whose actual
// registered file tools cross into another workspace and its real mount.
// Only the external model edge is scripted. M22 must die on private data;
// M23 must die on the mounted write, not merely a policy helper assertion.
func TestL1WU2SetupAuthority_RealTurnForeignMountAndPrivateMetadataBoundaries(t *testing.T) {
	for _, row := range []struct {
		name, id, operand, tool, deniedReason string
		allowed                               bool
	}{
		{"own_workspace_write", "mia", "own_work", "write_file", "", true},
		{"foreign_work_write", "omnipus", "foreign_work", "write_file", "", true},
		{"foreign_mount_absolute_write", "omnipus", "mount", "write_file", "", true},
		{"foreign_mount_alias_write", "omnipus", "mount_alias", "write_file", "", true},
		{"ordinary_agent_foreign_mount_refused", "mia", "mount", "write_file", tools.ErrOutsideScope.Error(), false},
		{"private_metadata_read_refused", "omnipus", "private", "read_file", tools.ErrCarveOut.Error(), false},
		{"private_metadata_write_refused", "omnipus", "private", "write_file", tools.ErrCarveOut.Error(), false},
		{"workspace_record_read_refused", "omnipus", "record", "read_file", tools.ErrCarveOut.Error(), false},
		{"workspace_record_write_refused", "omnipus", "record", "write_file", tools.ErrCarveOut.Error(), false},
		{"private_symlink_read_refused", "omnipus", "private_alias", "read_file", tools.ErrCarveOut.Error(), false},
		{"private_symlink_write_refused", "omnipus", "private_alias", "write_file", tools.ErrCarveOut.Error(), false},
		{"workspace_parent_write_refused", "omnipus", "parent", "write_file", tools.ErrCarveOut.Error(), false},
		{"outside_mount_write_refused", "omnipus", "mount_neighbor", "write_file", tools.ErrOutsideScope.Error(), false},
	} {
		t.Run(row.name, func(t *testing.T) {
			provider := testutil.NewScenario()
			al, instance, _ := l1WU2RealTurnLoop(t, row.id, provider)
			home := config.OmnipusHomeDir()
			foreignWork := filepath.Join(home, "workspaces", "research", "work")
			privateDir := filepath.Join(home, "workspaces", "research", ".omnipus")
			require.NoError(t, os.MkdirAll(privateDir, 0o700))
			privateFile := filepath.Join(privateDir, "private.txt")
			const privateMarker = "private-workspace-fixture-must-not-reach-model"
			require.NoError(t, os.WriteFile(privateFile, []byte(privateMarker), 0o600))
			require.NoError(t, os.Symlink(privateDir, filepath.Join(foreignWork, "private-alias")))
			external := t.TempDir()
			mounted := filepath.Join(external, "granted")
			require.NoError(t, os.MkdirAll(mounted, 0o700))
			mount, _, mountErr := workspace.CreateMount(home, "research", "shared-fixture", mounted)
			require.NoError(t, mountErr, "the foreign mount must be real, not a fabricated AllowedRoots value")
			assert.Equal(t, []string{mount.HostPath}, workspace.AllowedMountRoots(home, "research"), "stored mount positive control")
			paths := map[string]string{
				"own_work":       filepath.Join(home, "workspaces", "sales", "work", "proof.txt"),
				"foreign_work":   filepath.Join(foreignWork, "proof.txt"),
				"mount":          filepath.Join(mount.HostPath, "proof.txt"),
				"mount_alias":    filepath.Join(foreignWork, "shared-fixture", "proof.txt"),
				"private":        privateFile,
				"private_alias":  filepath.Join(foreignWork, "private-alias", "private.txt"),
				"record":         filepath.Join(home, "workspaces", "research.json"),
				"parent":         filepath.Join(filepath.Dir(foreignWork), "not-work.txt"),
				"mount_neighbor": filepath.Join(external, "outside-grant.txt"),
			}
			target := paths[row.operand]
			require.NotEmpty(t, target)
			before, beforeErr := os.ReadFile(target)
			if beforeErr != nil {
				require.ErrorIs(t, beforeErr, os.ErrNotExist)
			}
			args := map[string]any{"path": target}
			const written = "foreign-setup-from-real-turn"
			if row.tool == "write_file" {
				args["content"] = written
				args["overwrite"] = true
			}
			encoded, encodeErr := json.Marshal(args)
			require.NoError(t, encodeErr)
			provider.WithToolCall(row.tool, string(encoded)).WithText("setup boundary turn complete")
			anchor := "default"
			if row.id == "mia" {
				anchor = "sales"
			}
			answer, turnErr := al.runAgentLoop(context.Background(), instance, processOptions{
				SessionKey: "wu2-setup-boundary-" + row.name, Channel: "webchat", ChatID: "operator",
				WorkspaceID: anchor, UserInitiated: true, UserMessage: "Use the named file tool on the supplied setup path.",
			})
			require.NoError(t, turnErr, "the turn must run; a routing/model failure is not filesystem-boundary RED")
			assert.Equal(t, "setup boundary turn complete", answer)
			result := toolResultText(t, provider, row.tool+"-0")
			if row.allowed {
				data, readErr := os.ReadFile(target)
				require.NoError(t, readErr, "M23: a real foreign mount write must complete; result=%s", result)
				assert.Equal(t, written, string(data))
				assert.NotContains(t, result, "permission_denied")
			} else {
				refusal := l1WU2DecodeFilesystemRefusal(t, row.tool, result)
				assert.Equal(t, "permission_denied", refusal.Error)
				assert.Equal(t, row.tool, refusal.Tool)
				assert.True(t, refusal.Permanent)
				assert.Contains(t, refusal.Reason, row.deniedReason, "refusal must originate at the assigned filesystem boundary")
				assert.NotContains(t, result, privateMarker, "private data must not reach the model even on a refused read")
				data, readErr := os.ReadFile(target)
				if beforeErr == nil {
					require.NoError(t, readErr)
					assert.Equal(t, before, data, "a refused overwrite must preserve workspace record/private data byte-for-byte")
				} else {
					assert.ErrorIs(t, readErr, os.ErrNotExist, "a refused write must create no file")
				}
			}
		})
	}
}
