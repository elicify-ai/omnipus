package fspolicy_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracles: FR-006; L1 binding amendment §2 A8; independent CHECK F05.
// Setup authority is foreign WORK folders only, never workspace parents,
// records or private metadata. These tests use real filesystem identity.
func l1WU2SetupFixture(t *testing.T) (string, string, fspolicy.FSPolicy) {
	t.Helper()
	home := t.TempDir()
	canonical, homeErr := filepath.EvalSymlinks(home)
	require.NoError(t, homeErr)
	home = canonical
	own := filepath.Join(home, "agents", "omnipus")
	foreign := filepath.Join(home, "workspaces", "foreign", "work")
	for _, dir := range []string{own, foreign, filepath.Join(home, "workspaces", "foreign", ".omnipus"), filepath.Join(home, "workspaces", "foreign", "work-shadow"), filepath.Join(home, "agents", "mia")} {
		require.NoError(t, os.MkdirAll(dir, 0o700))
	}
	policy, policyErr := fspolicy.EffectiveFSPolicy(context.Background(), own, "", true, home, "omnipus", "default")
	require.NoError(t, policyErr)
	require.NoError(t, policy.Validate(), "ordinary base-policy positive control")
	return home, foreign, policy
}

func TestL1WU2SetupRoots_CanonicalWorkOnlyAndInputPolicyUnchanged(t *testing.T) {
	home, work, base := l1WU2SetupFixture(t)
	out, buildErr := fspolicy.WithSetupRoots(base, []string{work})
	require.NoError(t, buildErr, "A8: valid foreign work root must be a real grant")
	require.NoError(t, out.Validate())
	assert.Equal(t, []string{work}, out.SetupRoots)
	assert.Equal(t, []string{work}, out.AllowedRoots)
	assert.Empty(t, base.SetupRoots, "constructor must not mutate the base policy")
	assert.Empty(t, base.AllowedRoots)
	for _, row := range []struct {
		name, path string
		denied     bool
	}{
		{"work_root", work, false},
		{"work_child", filepath.Join(work, "setup.txt"), false},
		{"workspace_parent", filepath.Dir(work), true},
		{"workspace_record", filepath.Join(home, "workspaces", "foreign.json"), true},
		{"private_metadata", filepath.Join(filepath.Dir(work), ".omnipus", "private.json"), true},
		{"mount_definition", filepath.Join(filepath.Dir(work), "mounts.json"), true},
		{"prefix_sibling", filepath.Join(filepath.Dir(work), "work-shadow", "private.txt"), true},
		{"other_agent", filepath.Join(home, "agents", "mia", "SOUL.md"), true},
		{"config", filepath.Join(home, "config.json"), true},
		{"master_key", filepath.Join(home, "master.key"), true},
	} {
		t.Run(row.name, func(t *testing.T) {
			assert.Equal(t, row.denied, fspolicy.IsCarveOut(row.path, out), "F05: only the named foreign work subtree may be exempt")
		})
	}
}

func TestL1WU2WithSetupRoots_AppendsWithoutMutatingExistingGrantBackings(t *testing.T) {
	home, work, base := l1WU2SetupFixture(t)
	prior := filepath.Join(home, "workspaces", "prior", "work")
	require.NoError(t, os.MkdirAll(prior, 0o700))
	// Spare capacity makes a shallow append observable; nil slices would
	// allocate even in a broken constructor and miss the aliasing fault.
	setupBacking := []string{prior, "untouched-setup-spare"}
	allowedBacking := []string{prior, "untouched-allowed-spare"}
	base.SetupRoots = setupBacking[:1]
	base.AllowedRoots = allowedBacking[:1]
	require.NoError(t, base.Validate())
	out, buildErr := fspolicy.WithSetupRoots(base, []string{work})
	require.NoError(t, buildErr)
	assert.Equal(t, []string{prior, work}, out.SetupRoots)
	assert.Equal(t, []string{prior, work}, out.AllowedRoots)
	assert.Equal(t, []string{prior}, base.SetupRoots)
	assert.Equal(t, []string{prior}, base.AllowedRoots)
	assert.Equal(t, []string{prior, "untouched-setup-spare"}, setupBacking)
	assert.Equal(t, []string{prior, "untouched-allowed-spare"}, allowedBacking)
}

func TestL1WU2SetupRoots_ValidateRejectsMalformedAndNonWorkAuthority(t *testing.T) {
	home, work, base := l1WU2SetupFixture(t)
	for _, row := range []struct{ name, root string }{
		{"empty", ""}, {"whitespace", "  "}, {"relative", "foreign/work"},
		{"home_ancestor", home}, {"carve_out_root", filepath.Join(home, "workspaces")},
		{"whole_workspace", filepath.Dir(work)},
		{"private_metadata", filepath.Join(filepath.Dir(work), ".omnipus")},
		{"lexical_parent", filepath.Join(work, "..")},
	} {
		t.Run(row.name, func(t *testing.T) {
			candidate := base
			candidate.SetupRoots = []string{row.root}
			validationErr := candidate.Validate()
			assert.Error(t, validationErr, "A8/F05: a malformed or non-WORK authority root must be refused, not broadened")
			if validationErr != nil {
				assert.Contains(t, validationErr.Error(), "SetupRoots", "the refusal must identify the setup-root defect")
			}
		})
	}
}

func TestL1WU2WithSetupRoots_BroadPrivateAndResolutionFailuresReturnZeroPolicy(t *testing.T) {
	home, work, base := l1WU2SetupFixture(t)
	for _, row := range []struct{ name, root string }{
		{"empty", ""}, {"whitespace", "  "},
		{"home_ancestor", home}, {"carve_out_root", filepath.Join(home, "workspaces")},
		{"whole_workspace", filepath.Dir(work)},
		{"private_metadata", filepath.Join(filepath.Dir(work), ".omnipus")},
		{"nul_path", work + string(rune(0))},
	} {
		t.Run(row.name, func(t *testing.T) {
			out, buildErr := fspolicy.WithSetupRoots(base, []string{row.root})
			assert.Error(t, buildErr, "F05: forbidden setup root must fail visibly")
			assert.Equal(t, fspolicy.FSPolicy{}, out, "failed setup construction must not leak a partially broadened policy")
			if buildErr != nil {
				assert.Contains(t, strings.ToLower(buildErr.Error()), "setup", "specific failed boundary must remain visible regardless of identifier capitalisation")
			}
		})
	}
	assert.Empty(t, base.SetupRoots)
	assert.Empty(t, base.AllowedRoots)
}

func TestL1WU2SetupRoots_SymlinkIdentityNeverReopensPrivateMetadata(t *testing.T) {
	home, work, base := l1WU2SetupFixture(t)
	alias := filepath.Join(base.WorkDir, "foreign-work-alias")
	require.NoError(t, os.Symlink(work, alias))
	out, aliasErr := fspolicy.WithSetupRoots(base, []string{alias})
	require.NoError(t, aliasErr, "symlink to a valid work folder is a positive identity control")
	assert.Equal(t, []string{work}, out.SetupRoots)
	private := filepath.Join(filepath.Dir(work), ".omnipus")
	escape := filepath.Join(work, "escape-to-private")
	require.NoError(t, os.Symlink(private, escape))
	resolved, resolveErr := filepath.EvalSymlinks(escape)
	require.NoError(t, resolveErr)
	assert.True(t, fspolicy.IsCarveOut(resolved, out), "a symlink operand is judged by destination identity, not its spelling inside work")
	privateAlias := filepath.Join(base.WorkDir, "private-root-alias")
	require.NoError(t, os.Symlink(private, privateAlias))
	forbidden, privateErr := fspolicy.WithSetupRoots(base, []string{privateAlias})
	assert.Error(t, privateErr, "F05: canonicalising a private metadata alias cannot turn it into authority")
	assert.Equal(t, fspolicy.FSPolicy{}, forbidden)
	loop := filepath.Join(home, "setup-symlink-loop")
	require.NoError(t, os.Symlink(loop, loop))
	failed, loopErr := fspolicy.WithSetupRoots(base, []string{loop})
	assert.Error(t, loopErr, "resolution failure must propagate")
	assert.Equal(t, fspolicy.FSPolicy{}, failed)
	if loopErr != nil {
		assert.Contains(t, loopErr.Error(), "resolve setup root")
		cause := loopErr
		for errors.Unwrap(cause) != nil {
			cause = errors.Unwrap(cause)
		}
		// Go filepath.walkSymlinks defines this plain error (not os.PathError).
		assert.Equal(t, "EvalSymlinks: too many links", cause.Error(), "the actual resolution cause must remain wrapped, not replaced with prose")
	}
}
