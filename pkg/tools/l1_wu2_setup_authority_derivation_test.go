package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func l1WU2AuthorityFixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	home := t.TempDir()
	realHome, homeErr := filepath.EvalSymlinks(home)
	require.NoError(t, homeErr)
	home = realHome
	t.Setenv("OMNIPUS_HOME", home)
	own := filepath.Join(home, "agents", "omnipus")
	foreign := filepath.Join(home, "workspaces", "foreign", "work")
	for _, dir := range []string{own, foreign, filepath.Join(home, "workspaces", "foreign", ".omnipus")} {
		require.NoError(t, os.MkdirAll(dir, 0o700))
	}
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{ID: "foreign", Name: "Foreign", Status: "active"}))
	mount := t.TempDir()
	grant, _, mountErr := workspace.CreateMount(home, "foreign", "shared-fixture", mount)
	require.NoError(t, mountErr, "real mount-store positive control")
	return home, own, foreign, grant.HostPath
}

// F05/M22/M23: exact root derivation distinguishes a work folder from its
// parent, and includes the FOREIGN workspace's actual mount-store grants.
func TestL1WU2SetupAuthority_ExactWorkAndMountRootsNeverWorkspaceMetadata(t *testing.T) {
	home, own, work, mount := l1WU2AuthorityFixture(t)
	ctx := WithAgentID(context.Background(), "omnipus")
	policy, resolveErr := ResolveTurnFSPolicy(ctx, own, true)
	require.NoError(t, resolveErr)
	assert.Equal(t, []string{work}, policy.SetupRoots, "M22 must die: not filepath.Dir(work), nor any metadata folder")
	assert.ElementsMatch(t, []string{work, mount}, policy.AllowedRoots, "M23 must die: the foreign mount grant cannot be silently omitted")
	for _, path := range []string{filepath.Dir(work), filepath.Join(filepath.Dir(work), ".omnipus", "private.txt"), filepath.Join(home, "workspaces", "foreign.json")} {
		assert.True(t, fspolicy.IsCarveOut(path, policy), "A8: setup must not exempt private workspace data")
	}
	assert.False(t, fspolicy.IsCarveOut(filepath.Join(work, "proof.txt"), policy), "work-folder positive control")
	ordinary, ordinaryErr := ResolveTurnFSPolicy(WithAgentID(context.Background(), "mia"), filepath.Join(home, "agents", "mia"), true)
	require.NoError(t, ordinaryErr)
	assert.Empty(t, ordinary.SetupRoots, "Mia must not acquire cross-workspace setup authority")
	assert.NotContains(t, ordinary.AllowedRoots, mount)
}

func TestL1WU2SetupAuthority_WorkspaceListingErrorPropagatesWithoutPartialPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	own := filepath.Join(home, "agents", "omnipus")
	require.NoError(t, os.MkdirAll(own, 0o700))
	// Deterministic ENOTDIR, not a chmod failure root could bypass.
	require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces"), []byte("not-a-directory"), 0o600))
	policy, resolveErr := ResolveTurnFSPolicy(WithAgentID(context.Background(), "omnipus"), own, true)
	assert.Error(t, resolveErr, "F05: listing errors must not masquerade as no foreign workspaces")
	assert.Equal(t, fspolicy.FSPolicy{}, policy, "failed authority must expose no partial grants")
	if resolveErr != nil {
		assert.Contains(t, resolveErr.Error(), "setup authority: list workspaces")
		var pathErr *os.PathError
		assert.True(t, errors.As(resolveErr, &pathErr), "filesystem cause must remain in the propagated error chain")
	}
}

// Chief ruling, 2026-10-14: corrupt/unreadable mount storage drops ONLY
// that workspace's grants; it does not abort other valid authority.
func TestL1WU2SetupAuthority_CorruptForeignMountStoreDropsOnlyItsGrant(t *testing.T) {
	home, own, work, mount := l1WU2AuthorityFixture(t)
	ctx := WithAgentID(context.Background(), "omnipus")
	before, beforeErr := ResolveTurnFSPolicy(ctx, own, true)
	require.NoError(t, beforeErr)
	assert.ElementsMatch(t, []string{work, mount}, before.AllowedRoots, "known real grants prove the read instrument")
	legit := make(map[string]string)
	for _, id := range []string{"default", "healthy"} {
		record := workspace.Workspace{ID: id, Name: id, Status: "active"}
		if id == "default" {
			record.IsDefault = true
			record.CoreTeam = []string{"omnipus"}
		}
		require.NoError(t, workspace.SaveRecord(home, record))
		grant, _, mountErr := workspace.CreateMount(home, id, "still-valid", t.TempDir())
		require.NoError(t, mountErr)
		legit[id] = grant.HostPath
	}
	ctx = WithWorkspaceID(ctx, "default")
	ctx = WithTurnWorkspaceDir(ctx, filepath.Join(home, "workspaces", "default", "work"))
	path, pathErr := workspace.MountStorePath(home, "foreign")
	require.NoError(t, pathErr)
	require.NoError(t, os.WriteFile(path, []byte("malformed-mount-record"), 0o600))
	policy, resolveErr := ResolveTurnFSPolicy(ctx, own, true)
	require.NoError(t, resolveErr, "ruling: loss of a corrupt store's grants does not abort valid authority")
	assert.ElementsMatch(t, []string{work, filepath.Join(home, "workspaces", "default", "work"), filepath.Join(home, "workspaces", "healthy", "work")}, policy.SetupRoots)
	assert.NotContains(t, policy.AllowedRoots, mount, "failed foreign store grants NO access")
	for _, row := range []struct{ name, target string }{
		{"own_work", policy.WorkDir}, {"foreign_work", work}, {"own_mount", legit["default"]}, {"other_valid_mount", legit["healthy"]},
	} {
		t.Run(row.name, func(t *testing.T) {
			handle, accessErr := ResolvePath(ctx, policy, "write_file", "valid-grant", FSOpWrite, filepath.Join(row.target, "valid.txt"))
			require.NoError(t, accessErr, "unrelated legitimate grants remain usable")
			require.NotNil(t, handle)
			defer handle.Close()
			require.NoError(t, handle.WriteFile([]byte("valid-after-corrupt-store")))
			data, readErr := os.ReadFile(filepath.Join(row.target, "valid.txt"))
			require.NoError(t, readErr)
			assert.Equal(t, "valid-after-corrupt-store", string(data))
		})
	}
	handle, deniedErr := ResolvePath(ctx, policy, "write_file", "corrupt-grant", FSOpWrite, filepath.Join(mount, "denied.txt"))
	if handle != nil {
		defer handle.Close()
	}
	assert.ErrorIs(t, deniedErr, ErrOutsideScope, "corrupt foreign storage must not retain its old write grant")
	assert.Nil(t, handle)
	_, statErr := os.Stat(filepath.Join(mount, "denied.txt"))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestL1WU2SetupAuthority_LoopingWorkRootPropagatesWithoutPartialPolicy(t *testing.T) {
	_, own, work, _ := l1WU2AuthorityFixture(t)
	ctx := WithAgentID(context.Background(), "omnipus")
	before, beforeErr := ResolveTurnFSPolicy(ctx, own, true)
	require.NoError(t, beforeErr, "valid authority before path-resolution corruption")
	assert.Equal(t, []string{work}, before.SetupRoots)
	// The real mount lifecycle may initialise evidence files in work. Move the
	// whole fixture aside rather than assuming the folder contains only a link.
	require.NoError(t, os.Rename(work, work+"-before-loop"))
	require.NoError(t, os.Symlink(work, work))
	policy, resolveErr := ResolveTurnFSPolicy(ctx, own, true)
	assert.Error(t, resolveErr, "F05: an unresolvable root cannot become a successful, truncated grant set")
	assert.Equal(t, fspolicy.FSPolicy{}, policy)
	if resolveErr != nil {
		assert.Contains(t, resolveErr.Error(), "resolve setup root")
		cause := resolveErr
		for errors.Unwrap(cause) != nil {
			cause = errors.Unwrap(cause)
		}
		assert.Equal(t, "EvalSymlinks: too many links", cause.Error(), "the actual Go resolution error remains wrapped")
	}
}

// A8 is an identity boundary: a directory named work cannot grant the
// private metadata it targets through a symlink during real policy derivation.
func TestL1WU2SetupAuthority_WorkDirectorySymlinkCannotGrantPrivateMetadata(t *testing.T) {
	home, own, work, _ := l1WU2AuthorityFixture(t)
	ctx := WithAgentID(context.Background(), "omnipus")
	before, beforeErr := ResolveTurnFSPolicy(ctx, own, true)
	require.NoError(t, beforeErr)
	assert.Equal(t, []string{work}, before.SetupRoots, "valid work-root instrument before changing its identity")
	private := filepath.Join(home, "workspaces", "foreign", ".omnipus")
	require.NoError(t, os.Rename(work, work+"-before-private-link"))
	require.NoError(t, os.Symlink(private, work))
	policy, resolveErr := ResolveTurnFSPolicy(ctx, own, true)
	assert.Error(t, resolveErr, "F05: a canonical private metadata root cannot become setup authority merely because its alias is named work")
	assert.Equal(t, fspolicy.FSPolicy{}, policy, "refused setup authority must return no partial grant")
	if resolveErr != nil {
		assert.Contains(t, resolveErr.Error(), "SetupRoots")
	} else {
		assert.True(t, fspolicy.IsCarveOut(filepath.Join(private, "private.txt"), policy), "actual derivation must never exempt private metadata")
	}
}
