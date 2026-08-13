// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writableIn reports whether rules grant AccessWrite on exactly path.
func writableIn(rules []PathRule, path string) (found, writable bool) {
	want := filepath.Clean(path)
	for _, r := range rules {
		if filepath.Clean(r.Path) != want {
			continue
		}
		found = true
		if r.Access&AccessWrite != 0 {
			writable = true
		}
	}
	return found, writable
}

// TestPerTurnPolicy_DoesNotGrantSharedTmpWrite is the regression for the
// divergence UAT found and no test had: `write_file("/tmp/x")` was DENIED by the
// app layer while `echo > /tmp/x` SUCCEEDED through bash, from the same turn.
//
// The cause was a blanket /tmp read+write+execute grant in DefaultPolicyForModel
// that the per-turn policy inherited. The authored policy confines writes to the
// work dir and mounts, so the kernel granting all of /tmp on top is precisely
// the two-layer split ADR-063 exists to eliminate.
func TestPerTurnPolicy_DoesNotGrantSharedTmpWrite(t *testing.T) {
	workDir := t.TempDir()
	authored := fspolicy.FSPolicy{WorkDir: workDir}

	policy := DeriveKernelPolicy(authored, TurnPolicyInput{
		HomePath: t.TempDir(),
		Model:    FilesystemModelOpen,
	})

	found, writable := writableIn(policy.FilesystemRules, "/tmp")
	if found {
		assert.False(t, writable,
			"/tmp must not be WRITABLE in a per-turn policy: the app layer refuses a write "+
				"there, so granting it at the kernel layer lets bash do what write_file cannot")
	}
	// Same check against the symlink-resolved spelling, which is what macOS
	// actually matches on. Checking only one spelling would pass while the
	// platform that needs it stays broken.
	found, writable = writableIn(policy.FilesystemRules, "/private/tmp")
	if found {
		assert.False(t, writable, "/private/tmp is /tmp on macOS — the same rule must be narrowed")
	}
}

// TestPerTurnPolicy_KeepsTheTempDirToolsActuallyUse guards the other direction.
// os.TempDir() is the per-user directory hardened_exec forwards to every child
// as $TMPDIR; mktemp, npm, pip, git and `go build` all use it. Narrowing THAT
// would break ordinary toolchain work with a bare "operation not permitted".
func TestPerTurnPolicy_KeepsTheTempDirToolsActuallyUse(t *testing.T) {
	tmpDir := filepath.Clean(os.TempDir())
	if tmpDir == "/tmp" {
		t.Skip("this host's TMPDIR IS /tmp; the two grants are indistinguishable here")
	}

	policy := DeriveKernelPolicy(
		fspolicy.FSPolicy{WorkDir: t.TempDir()},
		TurnPolicyInput{HomePath: t.TempDir(), Model: FilesystemModelOpen},
	)

	found, writable := writableIn(policy.FilesystemRules, tmpDir)
	require.True(t, found, "the per-user temp dir must still be granted: %s", tmpDir)
	assert.True(t, writable,
		"$TMPDIR must stay WRITABLE — it is what os.TempDir() returns and what every "+
			"child is handed; narrowing it breaks mktemp/npm/pip/git/go build")
}

// TestPerTurnPolicy_KeepsWorkDirAndMountsWritable proves the narrowing is
// surgical. A work dir or a mount that happens to live under /tmp is granted by
// the AUTHORED policy and must survive — only the blanket grant on the directory
// itself is removed.
func TestPerTurnPolicy_KeepsWorkDirAndMountsWritable(t *testing.T) {
	workDir := t.TempDir()
	mount := t.TempDir()

	policy := DeriveKernelPolicy(
		fspolicy.FSPolicy{WorkDir: workDir, AllowedRoots: []string{mount}},
		TurnPolicyInput{HomePath: t.TempDir(), Model: FilesystemModelOpen},
	)

	for _, p := range []string{workDir, mount} {
		found, writable := writableIn(policy.FilesystemRules, p)
		require.True(t, found, "authored path must be granted: %s", p)
		assert.True(t, writable,
			"a work dir or mount must stay writable even when it sits under a temp root: %s", p)
	}
}

// TestSharedTmpNarrowing_LeavesReadAndExec: /tmp is still legitimately READ from
// and executed out of. Removing the rule entirely would be a wider change than
// the divergence requires, and would break reads the app layer permits — turning
// one divergence into another in the opposite direction.
func TestSharedTmpNarrowing_LeavesReadAndExec(t *testing.T) {
	in := []PathRule{{Path: "/tmp", Access: AccessRead | AccessWrite | AccessExecute}}
	out := narrowSharedTmpWrite(in)

	require.Len(t, out, 1, "the rule must be narrowed, not dropped")
	assert.Equal(t, AccessRead|AccessExecute, out[0].Access,
		"write removed; read and execute kept")
}

// TestSharedTmpNarrowing_IgnoresPathsMerelyUnderTmp pins the boundary. Only the
// directory ITSELF is narrowed; something inside it was granted deliberately.
func TestSharedTmpNarrowing_IgnoresPathsMerelyUnderTmp(t *testing.T) {
	in := []PathRule{
		{Path: "/tmp/my-workspace/work", Access: AccessRead | AccessWrite},
		{Path: "/private/tmp/other", Access: AccessRead | AccessWrite},
	}
	out := narrowSharedTmpWrite(in)

	require.Len(t, out, 2)
	for _, r := range out {
		assert.NotZero(t, r.Access&AccessWrite,
			"a path UNDER /tmp comes from the authored policy and must keep its write grant: %s", r.Path)
	}
}
