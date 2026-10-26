//go:build linux || darwin

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// R11a / R9: on Linux and macOS every byte except '/' and NUL is a valid name
// character. A user's file named with ':' and an em dash (seen on a real
// install) and names with a backslash, trailing space or trailing dot are
// recorded under their exact names; they never refuse the inventory.
func TestHistoryRepairCommand_NativeUnixNamesRecorded(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	root, sid := repairMissingIndexFixture(t)
	project := filepath.Join(root, "workspaces", "w1", "work", "vault", "02-Projects", "Proj")
	require.NoError(t, os.MkdirAll(project, 0o700))
	names := map[string]string{
		"Programme — AI Architect: Innovating Business Processes": "colon and em dash",
		`back\slash.txt`: "backslash",
		"notes ":         "trailing space",
		"draft.":         "trailing dot",
	}
	for name, body := range names {
		require.NoError(t, os.WriteFile(filepath.Join(project, name), []byte(body), 0o600))
	}
	before := repairTree(t, root)
	out := repairRun(t, tool, 0, "dry-run", "--data-root", root)
	require.Contains(t, out, "mode=dry-run sessions=1 create=1 ")
	require.Contains(t, out, " refused=0 ")
	require.Equal(t, before, repairTree(t, root))

	out, backup, manifest := repairGuardedApply(t, tool, helper, root)
	repairAssertPublicationCounts(t, out, 1, 0, 0)
	require.NoError(t, session.VerifyChatHistoryIndex(t.Context(), filepath.Join(root, "sessions", sid)), "real session published")
	_, byPath := repairManifestEntries(t, manifest)
	for name, body := range names {
		rel := "workspaces/w1/work/vault/02-Projects/Proj/" + name
		e, ok := byPath[rel]
		require.True(t, ok, "exact name recorded: %q", rel)
		require.Equal(t, "file", e["kind"])
		require.Equal(t, repairDigest([]byte(body)), e["sha256"])
		b, err := os.ReadFile(filepath.Join(project, name))
		require.NoError(t, err)
		require.Equal(t, body, string(b), "user file untouched")
	}
	require.NoError(t, os.Remove(filepath.Join(backup, "workspaces", "w1", "work", "vault", "02-Projects", "Proj", "notes ")))
	out = repairRun(t, tool, 2, "verify", "--data-root", root, "--backup-root", backup, "--backup-manifest", manifest)
	require.Contains(t, out, "class=backup_tampered", "a missing oddly named file is still detected")
}

// R11c: two names differing only in case refuse where they can both exist.
func TestHistoryRepairCommand_OnDiskCaseDuplicateStillRefused(t *testing.T) {
	tool := repairBinary(t)
	root := repairCanonicalFixture(t)
	upper, lower := filepath.Join(root, "Notes.txt"), filepath.Join(root, "notes.txt")
	require.NoError(t, os.WriteFile(upper, []byte("A"), 0o600))
	require.NoError(t, os.WriteFile(lower, []byte("b"), 0o600))
	a, err := os.Stat(upper)
	require.NoError(t, err)
	b, err := os.Stat(lower)
	require.NoError(t, err)
	if os.SameFile(a, b) {
		t.Skip("case-insensitive file system: an on-disk case duplicate cannot exist; the manifest-level duplicate is covered by TestHistoryRepairCommand_ManifestUnsafeNamesStillRefused")
	}
	out := repairRun(t, tool, 2, "dry-run", "--data-root", root)
	require.Contains(t, out, "class=unsafe_path")
}
