//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The app layer's carve-out check (tools.ResolvePath) stops read_file and
// send_file. It does NOT stop `bash`, which is a spawned child — that is the
// kernel layer's job, and under ADR-062's open-read model the ONLY thing
// standing between a child and any file is the deny block.
//
// So the two entries review found missing from the secret set need proving
// twice, at two independent layers. This is the kernel half, executed against
// a real child under /usr/bin/sandbox-exec:
//
//	backups/*.tar.gz  an archive of the ENTIRE vault
//	auth.json         plaintext OAuth access and refresh tokens
//
// Without the secret-set entries these tests fail with the file contents
// printed, which is exactly what a `bash -c 'cat ...'` tool call would have
// returned to the model.

// secretSetKernelPolicy builds the per-turn kernel policy for an agent-home
// turn under a realistic $OMNIPUS_HOME, going through the production seam
// (DeriveKernelPolicy) rather than hand-assembling a SandboxPolicy — so the
// test would not keep passing if the secret set stopped reaching the renderer.
func secretSetKernelPolicy(t *testing.T) (home, agentHome string, policy SandboxPolicy) {
	t.Helper()

	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	home = base

	agentHome = filepath.Join(home, "agents", "mia")
	require.NoError(t, os.MkdirAll(agentHome, 0o700))

	require.NoError(t, os.MkdirAll(filepath.Join(home, "backups"), 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(home, "backups", "backup-20260812T090000Z.tar.gz"),
		[]byte("VAULT-TARBALL-BYTES"), 0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(home, "auth.json"),
		[]byte(`{"credentials":{"anthropic":{"refresh_token":"OAUTH-REFRESH-TOKEN"}}}`), 0o600))

	// Controls: an ordinary file in the same parent as both secrets, and one
	// in the agent's own tree. If these stopped being readable the test would
	// be proving "the home is unreachable", not "these entries are".
	require.NoError(t, os.WriteFile(filepath.Join(home, "notes.txt"), []byte("ORDINARY-HOME-FILE"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(agentHome, "own.txt"), []byte("OWN-WORKDIR-FILE"), 0o600))

	authored := fspolicy.FSPolicy{
		WorkDir:   agentHome,
		Scope:     fspolicy.FSScopeUnrestricted,
		CarveOuts: fspolicy.SecretPaths(home),
	}
	policy = DeriveKernelPolicy(authored, TurnPolicyInput{
		HomePath: home,
		Model:    FilesystemModelOpen,
	})
	return home, agentHome, policy
}

// TestSeatbelt_ChildCannotReadBackupsOrAuthJSON is the kernel half of the
// BLOCKER-B proof: a real sandboxed child, under the real per-turn policy.
func TestSeatbelt_ChildCannotReadBackupsOrAuthJSON(t *testing.T) {
	home, agentHome, policy := secretSetKernelPolicy(t)

	backend := NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}

	cases := []struct {
		name       string
		path       string
		marker     string
		wantDenied bool
	}{
		{"backups tarball", filepath.Join(home, "backups", "backup-20260812T090000Z.tar.gz"), "VAULT-TARBALL-BYTES", true},
		{"auth.json", filepath.Join(home, "auth.json"), "OAUTH-REFRESH-TOKEN", true},
		{"control: ordinary file in $OMNIPUS_HOME", filepath.Join(home, "notes.txt"), "ORDINARY-HOME-FILE", false},
		{"control: file in the agent's own work dir", filepath.Join(agentHome, "own.txt"), "OWN-WORKDIR-FILE", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("/bin/cat", tc.path)
			cmd.Dir = agentHome
			require.NoError(t, backend.ApplyToCmd(cmd, policy))
			out, err := cmd.CombinedOutput()

			if tc.wantDenied {
				require.Error(t, err,
					"a sandboxed child must not read %s — under the open-read model the deny block is the only protection; output=%s",
					tc.path, out)
				assert.NotContains(t, string(out), tc.marker)
				return
			}

			require.NoError(t, err,
				"control must stay readable — an over-broad deny is not protection; output=%s", out)
			assert.Contains(t, string(out), tc.marker)
		})
	}
}

// TestSeatbelt_ChildCannotTruncateBackupsOrAuthJSON covers the other half of
// the deny. A read-only deny is defeated in one syscall: truncate destroys the
// backup archive (and the OAuth store) without ever reading a byte, and rename
// moves a file to a name the deny does not cover so it reads normally after.
// The renderer emits file-write* denies alongside file-read* for exactly this
// reason; this asserts it against a real child rather than against the text.
func TestSeatbelt_ChildCannotTruncateBackupsOrAuthJSON(t *testing.T) {
	home, agentHome, policy := secretSetKernelPolicy(t)

	backend := NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}

	for _, target := range []string{
		filepath.Join(home, "backups", "backup-20260812T090000Z.tar.gz"),
		filepath.Join(home, "auth.json"),
	} {
		t.Run(filepath.Base(target), func(t *testing.T) {
			before, err := os.ReadFile(target)
			require.NoError(t, err)

			cmd := exec.Command("/bin/sh", "-c", ": > '"+target+"'")
			cmd.Dir = agentHome
			require.NoError(t, backend.ApplyToCmd(cmd, policy))
			out, runErr := cmd.CombinedOutput()
			assert.Error(t, runErr, "truncate must be denied; output=%s", out)

			after, err := os.ReadFile(target)
			require.NoError(t, err)
			assert.Equal(t, before, after, "file was modified despite the write deny")
		})
	}
}
