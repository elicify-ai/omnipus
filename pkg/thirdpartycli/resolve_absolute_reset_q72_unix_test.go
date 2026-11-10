//go:build darwin || linux

package thirdpartycli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T11 §3.2 checks the physical file and its real ancestors. The Q72 walk's
// returned identities must describe that ancestry, not the abandoned source
// of an absolute link. Public path-only assertions cannot see stale cache IDs.
func TestT11Resolver_AbsoluteTargetResetsAncestorIdentities(t *testing.T) {
	base := t11combined.TrustedProgramDir(t)
	source := filepath.Join(base, "source", "deep", "link-folder")
	targetDir := filepath.Join(base, "target")
	require.NoError(t, os.MkdirAll(source, 0o700))
	require.NoError(t, os.Mkdir(targetDir, 0o700))
	target := filepath.Join(targetDir, "codex")
	require.NoError(t, os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	entered := filepath.Join(source, "codex-link")
	require.NoError(t, os.Symlink(target, entered))
	want, resolveErr := filepath.EvalSymlinks(entered)
	require.NoError(t, resolveErr, "instrument: the OS resolves the actual absolute link")
	require.Equal(t, target, want)

	// Expected identities are independent reads of the physical ancestry,
	// collected file-to-root. The walk's documented order is root-to-file.
	var wantReverse []os.FileInfo
	for path := want; path != "/"; path = filepath.Dir(path) {
		info, statErr := os.Lstat(path)
		require.NoError(t, statErr, "SETUP: read physical ancestor %s", path)
		wantReverse = append(wantReverse, info)
	}
	walk, present := any(resolveChain).(func(string, string, []forbiddenRoot) (string, []os.FileInfo, int, error))
	if !present {
		t.Fatal("BLOCKED: identity-returning resolveChain not implemented — required by the Q72 absolute-target cache coverage brief")
	}
	got, identities, hops, walkErr := walk("codex", entered, nil)
	require.NoError(t, walkErr)
	assert.Equal(t, want, got, "an absolute target replaces the source walk")
	assert.Equal(t, 1, hops, "exactly one real symlink was created")
	require.Len(t, identities, len(wantReverse), "only physical target ancestors remain, with no stale source identities")
	for index, expected := range wantReverse {
		actualIndex := len(identities) - 1 - index
		assert.True(t, os.SameFile(expected, identities[actualIndex]), "physical ancestor identity differs at root-to-file position %d", actualIndex)
	}
}
