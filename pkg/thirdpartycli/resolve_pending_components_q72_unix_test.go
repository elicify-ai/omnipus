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

// T11/r17: link expansion retains the remaining input components, and the
// returned identity chain names the physical final file and its ancestors.
func TestT11Resolver_RelativeDirectoryLinkThenFile(t *testing.T) {
	base := t11combined.TrustedProgramDir(t)
	source := filepath.Join(base, "source")
	target := filepath.Join(base, "target", "programs")
	require.NoError(t, os.Mkdir(source, 0o700))
	require.NoError(t, os.MkdirAll(target, 0o700))
	program := filepath.Join(target, "codex")
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	link := filepath.Join(source, "tools")
	require.NoError(t, os.Symlink(filepath.Join("..", "target"), link))
	entered := filepath.Join(link, "programs", "codex")
	assertT11ResolvedPathIdentities(t, entered, program)
}

// An absolute directory target resets the source ancestry before a pending
// .. steps out of that target directory. Neither abandoned ancestry belongs
// to the returned file. Raw concatenation preserves .. for the actual walk.
func TestT11Resolver_ParentAfterAbsoluteReset(t *testing.T) {
	base := t11combined.TrustedProgramDir(t)
	source := filepath.Join(base, "source", "deep")
	target := filepath.Join(base, "target")
	abandoned := filepath.Join(target, "abandoned")
	require.NoError(t, os.MkdirAll(source, 0o700))
	require.NoError(t, os.MkdirAll(abandoned, 0o700))
	program := filepath.Join(target, "codex")
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	link := filepath.Join(source, "tools")
	require.NoError(t, os.Symlink(abandoned, link))
	entered := link + string(filepath.Separator) + ".." + string(filepath.Separator) + "codex"
	assertT11ResolvedPathIdentities(t, entered, program)
}

func assertT11ResolvedPathIdentities(t *testing.T, entered, expected string) {
	t.Helper()
	physical, osErr := filepath.EvalSymlinks(entered)
	require.NoError(t, osErr, "instrument: the OS resolves the actual link and pending components")
	require.Equal(t, expected, physical)
	got, identities, hops, walkErr := resolveChain("codex", entered, nil)
	require.NoError(t, walkErr)
	assert.Equal(t, physical, got, "return the physical final file, not the link spelling or an intermediate directory")
	assert.Equal(t, 1, hops, "the fixture contains exactly one symlink")
	var reverse []os.FileInfo
	for path := physical; path != "/"; path = filepath.Dir(path) {
		info, statErr := os.Lstat(path)
		require.NoError(t, statErr, "independently read physical ancestry %s", path)
		reverse = append(reverse, info)
	}
	require.Len(t, identities, len(reverse), "no stale source/abandoned identities and no missing physical ancestors")
	for index, info := range reverse {
		position := len(identities) - 1 - index
		assert.True(t, os.SameFile(info, identities[position]), "physical identity differs at root-to-file position %d", position)
	}
}
