// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// This file drives the REAL chokepoint — tools.ResolvePath, which every
// filesystem tool funnels through — rather than fspolicy.IsCarveOut directly.
// The defect it covers was invisible from inside pkg/fspolicy's own tests
// because those all used FILE-shaped secrets (credentials.json, master.key),
// which identity handles exactly. The hole was DIRECTORY-shaped secrets:
//
//	credentials.json          (file, control)  -> DENIED
//	master.key                (file, control)  -> DENIED
//	backups/full.tar.gz       (the WHOLE VAULT) -> LEAKED via read AND send
//	system/audit.jsonl                          -> LEAKED
//	entities/agents/mia.json                    -> LEAKED
//
// identityRelation asks "is any ancestor of the candidate the same FILE as the
// container". A hard link's inode is a file inode; a directory container's is
// not; and every ancestor of the alias is inside the work dir. So the answer
// was a confident "outside".

// linkVictimHome builds an install with both file- and directory-shaped
// secrets populated, plus the agent's own work dir, and returns the realpath'd
// home and a policy rooted at the agent's home.
func linkVictimHome(t *testing.T) (home string, policy fspolicy.FSPolicy) {
	t.Helper()
	raw := t.TempDir()
	home, err := filepath.EvalSymlinks(raw)
	require.NoError(t, err)

	for _, d := range []string{
		filepath.Join("agents", "mia"),
		filepath.Join("agents", "victim"),
		"backups",
		"system",
		filepath.Join("entities", "agents"),
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, d), 0o750))
	}
	write := func(rel, content string) {
		require.NoError(t, os.WriteFile(filepath.Join(home, rel), []byte(content), 0o600))
	}
	write("credentials.json", "ENCRYPTED-VAULT")
	write("master.key", "MASTER-KEY-SECRET")
	write(filepath.Join("backups", "full.tar.gz"), "WHOLE-VAULT-ARCHIVE")
	write(filepath.Join("system", "audit.jsonl"), `{"event":"secret"}`)
	write(filepath.Join("entities", "agents", "mia.json"), `{"tool_policies":{}}`)
	write(filepath.Join("agents", "victim", "SOUL.md"), "VICTIM-SOUL")
	write(filepath.Join("agents", "mia", "own.txt"), "my own file")

	workDir := filepath.Join(home, "agents", "mia")
	return home, fspolicy.FSPolicy{
		WorkDir:   workDir,
		Scope:     fspolicy.FSScopeConfined,
		CarveOuts: fspolicy.SecretPaths(home),
	}
}

// plantLink hard links target to an innocuous name inside the agent's own work
// dir — the only place the agent can write, and therefore the only place an
// alias can be planted.
func plantLink(t *testing.T, policy fspolicy.FSPolicy, target, aliasName string) string {
	t.Helper()
	alias := filepath.Join(policy.WorkDir, aliasName)
	if err := os.Link(target, alias); err != nil {
		t.Skipf("hard links unavailable on this filesystem: %v", err)
	}
	return alias
}

// TestHardLinkIntoDirectorySecret_IsDeniedForReadAndSend is the finding,
// asserted closed through the production chokepoint, for every
// directory-shaped secret rather than for the one someone thought to test.
func TestHardLinkIntoDirectorySecret_IsDeniedForReadAndSend(t *testing.T) {
	home, policy := linkVictimHome(t)

	for _, tc := range []struct {
		name   string
		target string
	}{
		// Controls: file-shaped secrets, which identity already caught. They
		// are here so a regression that broke the ORIGINAL protection while
		// adding the new one cannot pass.
		{"credentials.json (file secret, control)", filepath.Join(home, "credentials.json")},
		{"master.key (file secret, control)", filepath.Join(home, "master.key")},

		// The leak.
		{"backups/full.tar.gz (whole vault)", filepath.Join(home, "backups", "full.tar.gz")},
		{"system/audit.jsonl", filepath.Join(home, "system", "audit.jsonl")},
		{"entities/agents/mia.json", filepath.Join(home, "entities", "agents", "mia.json")},
		{"another agent's SOUL.md", filepath.Join(home, "agents", "victim", "SOUL.md")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			alias := plantLink(t, policy, tc.target, "innocent-"+filepath.Base(tc.target))
			t.Cleanup(func() { _ = os.Remove(alias) })

			for _, op := range []FSOp{FSOpRead, FSOpSend} {
				_, err := ResolvePath(context.Background(), policy, "read_file", "", op, alias)
				require.Error(t, err,
					"op=%s: a hard link is the SAME FILE as %s. Its path is innocuous by "+
						"construction — that is the whole technique — so a path-shaped check "+
						"cannot see it.", op, tc.target)
				assert.True(t, errors.Is(err, ErrCarveOut),
					"op=%s: must be refused as a carve-out specifically, got %v", op, err)
			}
		})
	}
}

// TestHardLinkInsideOwnTree_StillWorks is the control that stops the fix from
// being "deny every multiply-linked file". Two files in the agent's own work
// dir linked to each other are its own files — pnpm and npm produce this shape
// constantly, and denying it would break ordinary work while every security
// assertion above still passed.
func TestHardLinkInsideOwnTree_StillWorks(t *testing.T) {
	_, policy := linkVictimHome(t)

	own := filepath.Join(policy.WorkDir, "own.txt")
	alias := plantLink(t, policy, own, "own-alias.txt")
	t.Cleanup(func() { _ = os.Remove(alias) })

	for _, op := range []FSOp{FSOpRead, FSOpSend} {
		h, err := ResolvePath(context.Background(), policy, "read_file", "", op, alias)
		require.NoError(t, err,
			"op=%s: a hard link between two files the agent already owns must stay usable; "+
				"the scan skips the caller's own tree for exactly this reason", op)
		require.NotNil(t, h)
	}

	// And the ordinary, single-linked file beside it is untouched.
	h, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, own)
	require.NoError(t, err)
	require.NotNil(t, h)
}

// TestHardLinkScan_DoesNotRunForOrdinaryFiles pins the cost gate. The scan is
// unbounded in principle (agents/ holds every agent's whole tree), so it must
// be unreachable for a file with a single link — which is every ordinary file.
//
// Proved by making the scan FATAL if entered: an unreadable secret directory
// makes any scan that touches it fail closed and deny. An ordinary read that
// still succeeds is proof the scan never ran.
func TestHardLinkScan_DoesNotRunForOrdinaryFiles(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permissions, so the unreadable case cannot be staged")
	}
	home, policy := linkVictimHome(t)

	backups := filepath.Join(home, "backups")
	require.NoError(t, os.Chmod(backups, 0o000))
	t.Cleanup(func() { _ = os.Chmod(backups, 0o750) })

	own := filepath.Join(policy.WorkDir, "own.txt")
	h, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, own)
	require.NoError(t, err,
		"an ordinary single-linked file must not trigger the scan at all; if it did, the "+
			"unreadable backups/ would fail closed and this read would be denied")
	require.NotNil(t, h)
}

// TestHardLinkScan_UnreadableSecretDirectoryFailsClosed is the other half of
// the same staging: once the file IS multiply linked, a scan that cannot see
// inside a secret directory has ruled nothing out and must deny.
func TestHardLinkScan_UnreadableSecretDirectoryFailsClosed(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses directory permissions, so the unreadable case cannot be staged")
	}
	home, policy := linkVictimHome(t)

	own := filepath.Join(policy.WorkDir, "own.txt")
	alias := plantLink(t, policy, own, "own-alias.txt")
	t.Cleanup(func() { _ = os.Remove(alias) })

	backups := filepath.Join(home, "backups")
	require.NoError(t, os.Chmod(backups, 0o000))
	t.Cleanup(func() { _ = os.Chmod(backups, 0o750) })

	_, err := ResolvePath(context.Background(), policy, "read_file", "", FSOpRead, alias)
	require.Error(t, err,
		"a scan that could not list a secret directory has not shown the link points elsewhere; "+
			"the deny side is where this decision fails safe")
	assert.True(t, errors.Is(err, ErrCarveOut), "got %v", err)
}
