// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grantedSet flattens rules to the set of granted paths. Named distinctly from
// the package's existing grantedPaths(t, rules) helper rather than shadowing it.
func grantedSet(rules []PathRule) map[string]bool {
	out := make(map[string]bool, len(rules))
	for _, r := range rules {
		out[r.Path] = true
	}
	return out
}

// TestExpandRules_DeniesABackupCreatedAfterThePolicyWasBuilt is the regression
// for a HIGH finding: DeniedPathPrefixes was enforced on macOS only.
//
// The two layers get the same field and did different things with it. macOS
// renders each prefix as an anchored regex deny, which matches whenever the file
// appears. Linux ENUMERATES the home's children and grants them individually —
// and that enumeration runs at SPAWN, while the exact deny list was captured
// earlier at policy-build.
//
// So a backup appearing in between (pkg/migrate writes config.json.bak) was, by
// enumeration time, an existing child that no exact deny path matched. It was
// granted the parent's full access on Linux while macOS denied it.
//
// The test creates the backup AFTER building the policy, which is the whole
// point — creating it first would pass against the old code too.
func TestExpandRules_DeniesABackupCreatedAfterThePolicyWasBuilt(t *testing.T) {
	home := t.TempDir()
	secret := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(secret, []byte("{}"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "agents"), 0o700))

	// Policy built now: the exact deny list can only name what exists.
	denied := []string{secret}
	prefixes := []string{secret + "."}

	// ... and the backup appears afterwards, exactly as a migration would write
	// it while the gateway is already running.
	backup := filepath.Join(home, "config.json.bak")
	require.NoError(t, os.WriteFile(backup, []byte("{}"), 0o600))

	rules, err := ExpandRulesExcluding(
		[]PathRule{{Path: home, Access: AccessRead | AccessWrite}},
		denied, nil, prefixes,
	)
	require.NoError(t, err)

	granted := grantedSet(rules)
	assert.False(t, granted[backup],
		"a backup of a secret created AFTER the policy was built must not be granted — "+
			"this is the exact path macOS denies by regex and Linux was granting by enumeration")
	assert.False(t, granted[secret], "the secret itself is denied outright")
	assert.True(t, granted[filepath.Join(home, "agents")],
		"an ordinary sibling must still be granted; the fix must not deny the whole home")
}

// TestExpandRules_PrefixMatchIsCaseInsensitive covers the case-insensitive
// volume. This is a DENY-side test, so folding is the safe direction: on APFS
// `Config.json.BAK` IS the backup, and on a case-sensitive volume folding only
// withholds a grant from a distinctly-named sibling in a directory Omnipus owns.
func TestExpandRules_PrefixMatchIsCaseInsensitive(t *testing.T) {
	home := t.TempDir()
	secret := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(secret, []byte("{}"), 0o600))

	oddCase := filepath.Join(home, "Config.json.BAK")
	require.NoError(t, os.WriteFile(oddCase, []byte("{}"), 0o600))

	rules, err := ExpandRulesExcluding(
		[]PathRule{{Path: home, Access: AccessRead | AccessWrite}},
		[]string{secret}, nil, []string{secret + "."},
	)
	require.NoError(t, err)

	assert.False(t, grantedSet(rules)[oddCase],
		"a differently-cased backup must not be granted — on APFS it is the same file")
}

// TestExpandRules_PrefixDoesNotOverDeny pins the boundary. The prefixes are
// absolute paths, so an unrelated file that merely shares a leading substring in
// its NAME must keep its grant. Denying it would break ordinary files for the
// sake of a secret they have nothing to do with.
func TestExpandRules_PrefixDoesNotOverDeny(t *testing.T) {
	home := t.TempDir()
	secret := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(secret, []byte("{}"), 0o600))

	// Shares the "config" stem but is not a backup of the secret.
	unrelated := filepath.Join(home, "config-notes.md")
	require.NoError(t, os.WriteFile(unrelated, []byte("notes"), 0o600))

	rules, err := ExpandRulesExcluding(
		[]PathRule{{Path: home, Access: AccessRead | AccessWrite}},
		[]string{secret}, nil, []string{secret + "."},
	)
	require.NoError(t, err)

	assert.True(t, grantedSet(rules)[unrelated],
		"a file sharing only a stem is not a backup and must keep its grant")
}

// TestExpandRules_NoPrefixesBehavesAsBefore guards against a regression in the
// common path: with no prefixes configured, expansion is unchanged.
func TestExpandRules_NoPrefixesBehavesAsBefore(t *testing.T) {
	home := t.TempDir()
	secret := filepath.Join(home, "master.key")
	require.NoError(t, os.WriteFile(secret, []byte("k"), 0o600))
	sibling := filepath.Join(home, "sessions")
	require.NoError(t, os.MkdirAll(sibling, 0o700))

	rules, err := ExpandRulesExcluding(
		[]PathRule{{Path: home, Access: AccessRead}},
		[]string{secret}, nil, nil,
	)
	require.NoError(t, err)

	granted := grantedSet(rules)
	assert.True(t, granted[sibling])
	assert.False(t, granted[secret])
}
