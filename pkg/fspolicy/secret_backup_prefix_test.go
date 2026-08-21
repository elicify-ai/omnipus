// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backupHome lays out an install with NO backup files on disk yet and returns
// its realpath. The absence is the point: the defect these tests cover is that
// the secret set discovered backups by LISTING, so a file that did not exist
// when the policy was built was not covered by it.
func backupHome(t *testing.T) (home string, policy FSPolicy) {
	t.Helper()
	raw := t.TempDir()
	home, err := filepath.EvalSymlinks(raw)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(home, "agents", "mia"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), []byte("{}"), 0o600))

	return home, FSPolicy{
		WorkDir:   filepath.Join(home, "agents", "mia"),
		Scope:     FSScopeConfined,
		CarveOuts: buildCarveOuts(home),
	}
}

// TestIsCarveOut_BackupNotYetOnDiskIsCovered is the write half of the defect.
//
// Before the prefix rule, buildCarveOuts listed $OMNIPUS_HOME and found no
// backups, so config.json.bak-9999 was not in the list and IsCarveOut returned
// false for it — a write landed on a path the secret set claims to protect.
func TestIsCarveOut_BackupNotYetOnDiskIsCovered(t *testing.T) {
	home, policy := backupHome(t)

	for _, name := range []string{
		"config.json.bak-9999",
		"config.json.bak-20260812T101500Z",
		"credentials.json.old",
		"master.key.2",
		// Case-folded spellings: DENY-side, so over-matching is the safe
		// direction and these must be caught on any volume.
		"CONFIG.JSON.bak-1",
		"Master.Key.backup",
	} {
		p := filepath.Join(home, name)
		require.NoFileExists(t, p, "the whole point is that it is not on disk")
		assert.True(t, IsCarveOut(p, policy),
			"%s must be a carve-out BEFORE it exists; enumerating the directory at policy-build "+
				"time cannot cover a file that is not there yet, which is why the rule is a prefix "+
				"match and not a listing", name)
	}
}

// TestIsCarveOut_BackupWrittenMidTurnIsCovered is the read half, and the one
// that actually leaks: the gateway writes config.json.bak-<ts> when a migration
// rewrites the config, and that happens WHILE a turn is running against a
// policy built before it.
func TestIsCarveOut_BackupWrittenMidTurnIsCovered(t *testing.T) {
	home, policy := backupHome(t)

	// The policy is built. Only now does the gateway create the backup.
	late := filepath.Join(home, "config.json.bak-1755000000")
	require.NoError(t, os.WriteFile(late, []byte(`{"gateway":{"cli_token":"LIVE-TOKEN"}}`), 0o600))

	assert.True(t, IsCarveOut(late, policy),
		"a backup created after the policy was built holds the same live gateway token as the "+
			"config it copies; a carve-out list fixed at build time is readable straight through")
}

// TestIsCarveOut_OrdinaryFilesAreNotSweptUp is the control. A prefix rule that
// denies too much is a different defect, not a fix: these names all sit in
// $OMNIPUS_HOME and none of them is a secret backup.
func TestIsCarveOut_OrdinaryFilesAreNotSweptUp(t *testing.T) {
	home, policy := backupHome(t)

	for _, name := range []string{
		"config.jsonl", // a different file, not "config.json." + suffix
		"configuration.json",
		"notes.md",
		"master.keyring", // prefix "master.key" WITHOUT the dot
	} {
		assert.False(t, IsCarveOut(filepath.Join(home, name), policy),
			"%s is not a secret backup and must stay reachable", name)
	}
}

// TestIsCarveOut_BackupRuleIsAnchoredToOmnipusHome: the prefix rule must not
// deny a file merely because of its NAME. An agent legitimately working on a
// file called config.json.bak-1 inside its OWN tree is not touching a secret.
func TestIsCarveOut_BackupRuleIsAnchoredToOmnipusHome(t *testing.T) {
	home, policy := backupHome(t)

	inOwnTree := filepath.Join(home, "agents", "mia", "config.json.bak-1")
	assert.False(t, IsCarveOut(inOwnTree, policy),
		"the rule is anchored on the directory the carve-out roots live in, not on the basename "+
			"alone; a same-named file inside the agent's own work dir is its own file")

	elsewhere := filepath.Join(t.TempDir(), "config.json.bak-1")
	assert.False(t, IsCarveOut(elsewhere, policy),
		"a file with the same name outside $OMNIPUS_HOME entirely must be unaffected")
}
