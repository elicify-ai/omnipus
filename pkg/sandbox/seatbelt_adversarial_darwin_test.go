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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-052 Phase-3 AC-6, checklist item 2: "an adversarial security review of
// the rendered profile (escape attempts, path traversal, symlink races,
// profile injection)".
//
// Each test here is an ATTACK that must fail, executed against a real child.
// One of them documents an attack that SUCCEEDS — see
// TestSeatbeltAdversarial_PreexistingHardlink_IsAKnownGap. That is deliberate:
// a security boundary's honest limits belong in the test suite, not only in a
// document nobody re-reads.

// advWorkspace builds a workspace plus a secret file OUTSIDE it, and returns
// the workspace dir, the outside secret path, and a policy granting RWX on the
// workspace only.
func advWorkspace(t *testing.T) (ws string, secret string, policy SandboxPolicy) {
	t.Helper()

	ws = t.TempDir()
	secret = filepath.Join(t.TempDir(), "secret-outside.txt")
	require.NoError(t, os.WriteFile(secret, []byte("SECRET-CONTENT"), 0o600))

	policy = SandboxPolicy{
		FilesystemRules: []PathRule{{Path: ws, Access: AccessRead | AccessWrite | AccessExecute}},
	}
	return ws, secret, policy
}

func advRun(t *testing.T, policy SandboxPolicy, ws string, script string) (string, error) {
	t.Helper()

	backend := NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}
	cmd := exec.Command("/bin/bash", "-c", script)
	cmd.Dir = ws
	require.NoError(t, backend.ApplyToCmd(cmd, policy))
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// A symlink planted inside the workspace pointing outside must NOT grant
// access. Seatbelt evaluates the resolved path, so the allow on the workspace
// does not extend to wherever a symlink inside it happens to point.
func TestSeatbeltAdversarial_SymlinkEscapeDenied(t *testing.T) {
	ws, secret, policy := advWorkspace(t)
	link := filepath.Join(ws, "escape")
	require.NoError(t, os.Symlink(secret, link))

	out, err := advRun(t, policy, ws, "cat "+link)
	assert.Error(t, err, "symlink escape must be denied; output=%s", out)
	assert.NotContains(t, out, "SECRET-CONTENT", "symlink escape leaked the file")
}

// A symlink created by the child at RUNTIME must be no more effective than one
// planted ahead of time — this is the symlink-race variant, where the attacker
// controls the link after the profile was rendered.
func TestSeatbeltAdversarial_RuntimeSymlinkRaceDenied(t *testing.T) {
	ws, secret, policy := advWorkspace(t)

	out, err := advRun(t, policy, ws, "ln -sf "+secret+" ./late && cat ./late")
	assert.Error(t, err, "runtime-created symlink must not escape; output=%s", out)
	assert.NotContains(t, out, "SECRET-CONTENT")
}

func TestSeatbeltAdversarial_DotDotTraversalDenied(t *testing.T) {
	ws, secret, policy := advWorkspace(t)

	out, err := advRun(t, policy, ws, "cat "+filepath.Join(ws, "..", filepath.Base(secret)))
	assert.Error(t, err, "../ traversal must be denied; output=%s", out)
	assert.NotContains(t, out, "SECRET-CONTENT")
}

// The confined child must not be able to MANUFACTURE a hardlink into the
// workspace. This is the control that bounds the known gap documented below:
// without it, that gap would be exploitable by the sandboxed code itself
// rather than requiring an unconfined actor.
func TestSeatbeltAdversarial_ChildCannotCreateHardlinkOutward(t *testing.T) {
	ws, secret, policy := advWorkspace(t)

	out, err := advRun(t, policy, ws, "ln "+secret+" ./linked && cat ./linked")
	assert.Error(t, err, "child must not be able to hardlink an out-of-policy file; output=%s", out)
	assert.NotContains(t, out, "SECRET-CONTENT")

	// Also must not link a file it cannot even read.
	out, err = advRun(t, policy, ws, "ln /etc/sudoers ./sudoers-link && echo LINKED")
	assert.Error(t, err, "hardlinking an unreadable system file must fail; output=%s", out)
	assert.NotContains(t, out, "LINKED")
}

// KNOWN GAP — documented, bounded, and asserted so a change in behaviour is
// noticed rather than discovered.
//
// Seatbelt confines by PATH, not by inode. A hardlink inside the workspace
// pointing at an out-of-policy file is, as far as the kernel is concerned, a
// file inside the workspace — so the child may read it.
//
// Why this is not a sandbox-defeating hole in practice:
//   - The confined child cannot create such a link (proven by the test above:
//     it cannot reach the target to link it).
//   - Exploitation therefore requires an UNCONFINED actor to pre-place the
//     hardlink inside the agent workspace. An attacker with that capability
//     can already write arbitrary content into the workspace directly.
//   - Hardlinks cannot cross filesystems, which further limits reach.
//
// This is a property of path-based mandatory access control, not a defect
// specific to this implementation. It is asserted here so the limitation stays
// visible in the suite; if it ever starts failing, Seatbelt semantics changed
// and this note needs revisiting.
func TestSeatbeltAdversarial_PreexistingHardlink_IsAKnownGap(t *testing.T) {
	ws, secret, policy := advWorkspace(t)

	link := filepath.Join(ws, "prelinked")
	if err := os.Link(secret, link); err != nil {
		t.Skipf("cannot create hardlink on this filesystem: %v", err)
	}

	out, _ := advRun(t, policy, ws, "cat "+link)
	assert.Contains(t, out, "SECRET-CONTENT",
		"documented limitation: a pre-placed hardlink is readable through the workspace. "+
			"If this now FAILS, path-based confinement has gained inode awareness and the "+
			"known-gap note in this test should be updated.")
}

// Paths containing Scheme/shell metacharacters must be enforced correctly, not
// merely rendered without crashing. Parentheses and semicolons are meaningful
// in the Seatbelt dialect, so a path carrying them is the natural
// profile-injection probe.
func TestSeatbeltAdversarial_MetacharacterPathsStillEnforce(t *testing.T) {
	ws, secret, policy := advWorkspace(t)

	weird := filepath.Join(ws, "weird (dir) ;drop")
	require.NoError(t, os.Mkdir(weird, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(weird, "f.txt"), []byte("meta-ok"), 0o600))

	// Allowed content inside a metacharacter path is still readable...
	out, err := advRun(t, policy, ws, "cat '"+filepath.Join(weird, "f.txt")+"'")
	require.NoError(t, err, "metacharacter path must remain usable; output=%s", out)
	assert.Contains(t, out, "meta-ok")

	// ...and the boundary is not widened by their presence.
	out, err = advRun(t, policy, ws, "cat "+secret)
	assert.Error(t, err, "metacharacter path must not widen the policy; output=%s", out)
	assert.NotContains(t, out, "SECRET-CONTENT")
}

// A path engineered to terminate the (subpath "...") filter and append its own
// directive must be REJECTED at render time. Rendering a widened profile would
// be far worse than failing: the caller would believe a policy was applied.
func TestSeatbeltAdversarial_ProfileInjectionRejected(t *testing.T) {
	injection := `/tmp/ws") (allow file-read* (subpath "/`

	_, err := renderSeatbeltProfile(SandboxPolicy{
		FilesystemRules: []PathRule{{Path: injection, Access: AccessRead}},
	})
	require.Error(t, err, "a path containing a quote must never reach the profile")
	assert.Contains(t, err.Error(), "forbidden character")
}

// TestSeatbeltAdversarial_CannotDropBinaryIntoExecPath executes the attack the
// allowed_exec_paths design is judged against: the agent authors a payload in
// its own writable workspace, then tries every way of getting it into a
// directory it is allowed to execute from.
//
// If any placement succeeded, granting exec on a user-owned prefix such as
// Homebrew's would be equivalent to granting arbitrary code execution outside
// the workspace.
func TestSeatbeltAdversarial_CannotDropBinaryIntoExecPath(t *testing.T) {
	ws, _, policy := advWorkspace(t)

	execDir := t.TempDir()
	policy.FilesystemRules = append(policy.FilesystemRules,
		PathRule{Path: execDir, Access: AccessRead | AccessExecute})

	payload := filepath.Join(ws, "payload.sh")
	require.NoError(t, os.WriteFile(payload, []byte("#!/bin/sh\necho PWNED\n"), 0o755))

	for _, tc := range []struct {
		name   string
		script string
	}{
		{"copy", "cp " + payload + " " + filepath.Join(execDir, "p.sh")},
		{"move", "mv " + payload + " " + filepath.Join(execDir, "p.sh")},
		{"symlink", "ln -s " + payload + " " + filepath.Join(execDir, "p.sh")},
		{"hardlink", "ln " + payload + " " + filepath.Join(execDir, "p.sh")},
		{"redirect", "echo '#!/bin/sh' > " + filepath.Join(execDir, "p.sh")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := advRun(t, policy, ws, tc.script)
			assert.Error(t, err, "planting a binary in an exec path must fail; output=%s", out)
			assert.NoFileExists(t, filepath.Join(execDir, "p.sh"),
				"a payload reached a directory the agent can execute from")
		})
	}
}

// TestSeatbeltAdversarial_ExecGrantDoesNotWidenReadBoundary guards against a
// future widening of the granted subtree. The seeded config names
// /usr/local/bin and friends specifically, NOT /usr/local, because the parent
// holds things like /usr/local/etc/wireguard. This asserts a sibling of the
// granted directory stays unreadable.
func TestSeatbeltAdversarial_ExecGrantDoesNotWidenReadBoundary(t *testing.T) {
	ws, _, policy := advWorkspace(t)

	prefix := t.TempDir()
	execDir := filepath.Join(prefix, "bin")
	secretDir := filepath.Join(prefix, "etc")
	require.NoError(t, os.MkdirAll(execDir, 0o755))
	require.NoError(t, os.MkdirAll(secretDir, 0o755))

	secret := filepath.Join(secretDir, "private.key")
	require.NoError(t, os.WriteFile(secret, []byte("PRIVATE-KEY-MATERIAL"), 0o600))

	policy.FilesystemRules = append(policy.FilesystemRules,
		PathRule{Path: execDir, Access: AccessRead | AccessExecute})

	out, err := advRun(t, policy, ws, "cat "+secret)
	assert.Error(t, err, "a sibling of the exec path must stay unreadable; output=%s", out)
	assert.NotContains(t, out, "PRIVATE-KEY-MATERIAL")
}
