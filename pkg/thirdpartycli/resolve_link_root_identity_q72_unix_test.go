//go:build darwin || linux

package thirdpartycli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T11/r17 checks the link itself, not only its target or containing folder.
// Canonically equivalent Unicode names evade a case-only text comparison.
// A normalization-sensitive filesystem gets a real hard-linked symlink alias
// so it exercises the same physical-identity rule, without skipping coverage.
func TestT11Resolver_AliasedWritableRootLinkIdentity(t *testing.T) {
	base := t11combined.TrustedProgramDir(t)
	target := filepath.Join(base, "safe-programs")
	require.NoError(t, os.Mkdir(target, 0o700))
	program := filepath.Join(target, "codex")
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	link := filepath.Join(base, "Caf\u00e9")
	alias := filepath.Join(base, "Cafe\u0301")
	require.NotEqual(t, link, alias, "fixture: NFC and NFD are byte-distinct")
	require.NoError(t, os.Symlink(target, link))
	if _, aliasErr := os.Lstat(alias); errors.Is(aliasErr, os.ErrNotExist) {
		require.NoError(t, os.Link(link, alias), "SETUP: create the same symlink inode under its NFD alias on a normalization-sensitive volume")
	} else {
		require.NoError(t, aliasErr, "SETUP: inspect the actual Unicode alias")
	}
	linkInfo, linkErr := os.Lstat(link)
	require.NoError(t, linkErr)
	aliasInfo, aliasErr := os.Lstat(alias)
	require.NoError(t, aliasErr)
	require.True(t, linkInfo.Mode()&os.ModeSymlink != 0)
	require.True(t, aliasInfo.Mode()&os.ModeSymlink != 0)
	require.True(t, os.SameFile(linkInfo, aliasInfo), "instrument: both spellings identify the symlink itself, not just its target")
	require.False(t, pathWithin(alias, link), "instrument: the lexical rule must not already detect this alias")
	roots, rootsErr := expandRoots([]string{alias})
	require.NoError(t, rootsErr)
	entered := filepath.Join(link, "codex")
	control, _, controlHops, controlErr := resolveChain("codex", entered, nil)
	require.NoError(t, controlErr, "instrument: without a forbidden root the actual directory link resolves")
	require.Equal(t, program, control)
	require.Equal(t, 1, controlHops)
	got, identities, _, checkErr := resolveChain("codex", entered, roots)
	var refused *CheckError
	require.ErrorAs(t, checkErr, &refused, "the link's own identity must match the aliased forbidden root before following its target")
	assert.Equal(t, CheckAgentWritableLocation, refused.Check)
	assert.Equal(t, link, refused.At, "refuse the link itself, not a later target-file check")
	assert.Empty(t, got, "a refused link yields no program to launch")
	assert.Empty(t, identities, "a refused walk yields no usable ancestor cache")
}
