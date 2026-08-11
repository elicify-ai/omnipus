//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-052 Phase-3 AC-6, checklist item 1: "a macOS integration test that
// exercises a real child under sandbox-exec".
//
// These tests spawn REAL processes under REAL Seatbelt profiles and assert the
// boundary holds. That distinction matters: every assertion here would still
// pass against a profile that renders beautifully and enforces nothing, if it
// were written against the rendered string instead of a live child. The whole
// reason AC-6 blocked on "requires macOS" is that string assertions cannot
// tell you whether the kernel agrees with your profile.
//
// They are darwin-gated by the build tag and skip cleanly if sandbox-exec is
// absent, so a Linux CI run is unaffected.

// seatbeltTestWorkspace builds a workspace directory with a known file and
// returns the policy granting RWX on it (and nothing else).
func seatbeltTestWorkspace(t *testing.T) (dir string, policy SandboxPolicy) {
	t.Helper()

	// t.TempDir() on macOS lives under /var/folders, and /var is a symlink into
	// /private. Passing the unresolved path is deliberate: it exercises the
	// symlink resolution that the renderer must perform for the profile to
	// match anything at all.
	dir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inside.txt"), []byte("workspace-visible"), 0o600))

	policy = SandboxPolicy{
		FilesystemRules: []PathRule{
			{Path: dir, Access: AccessRead | AccessWrite | AccessExecute},
		},
	}
	return dir, policy
}

// runSandboxed executes argv under the given policy and returns combined output.
func runSandboxed(t *testing.T, policy SandboxPolicy, workdir string, argv ...string) (string, error) {
	t.Helper()

	backend := NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = workdir
	require.NoError(t, backend.ApplyToCmd(cmd, policy))

	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestSeatbelt_Available_OnDarwin(t *testing.T) {
	backend := NewSeatbeltBackend()
	assert.True(t, backend.Available(), "sandbox-exec should be present on a stock macOS host")

	// The kill-switch must actually degrade, or graceful degradation (HC #4) is
	// a claim rather than a behaviour.
	t.Setenv(seatbeltDisableEnv, "1")
	assert.False(t, backend.Available(), "operator kill-switch must disable the backend")
}

func TestSeatbelt_SelectBackend_PrefersSeatbelt(t *testing.T) {
	backend, name := selectBackendPlatform()
	require.NotNil(t, backend)
	assert.Equal(t, "seatbelt", name, "darwin must select the Seatbelt backend, not fallback")

	t.Setenv(seatbeltDisableEnv, "1")
	_, fallbackName := selectBackendPlatform()
	assert.Equal(t, "fallback", fallbackName, "kill-switch must route to application-level enforcement")
}

// TestSeatbelt_RealChild_WorkspaceReadWrite proves the profile lets a real
// child do what the policy permits. Without the empirically derived system
// preamble this fails at exec time (SIGABRT, empty stderr), which is precisely
// the failure mode the preamble exists to prevent.
func TestSeatbelt_RealChild_WorkspaceReadWrite(t *testing.T) {
	dir, policy := seatbeltTestWorkspace(t)

	out, err := runSandboxed(t, policy, dir, "/bin/cat", filepath.Join(dir, "inside.txt"))
	require.NoError(t, err, "child must start and read an allowed file; output=%s", out)
	assert.Contains(t, out, "workspace-visible")

	target := filepath.Join(dir, "written.txt")
	out, err = runSandboxed(t, policy, dir, "/bin/bash", "-c", "echo written-by-child > "+target)
	require.NoError(t, err, "child must write inside the workspace; output=%s", out)

	body, readErr := os.ReadFile(target)
	require.NoError(t, readErr)
	assert.Equal(t, "written-by-child\n", string(body))
}

// TestSeatbelt_RealChild_DeniesOutsideWorkspace is the assertion that actually
// matters: the boundary blocks what the policy does not grant.
func TestSeatbelt_RealChild_DeniesOutsideWorkspace(t *testing.T) {
	dir, policy := seatbeltTestWorkspace(t)

	// A file that exists and is readable WITHOUT the sandbox, so a failure here
	// is attributable to Seatbelt and not to a missing file.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("must-not-be-readable"), 0o600))
	require.FileExists(t, outside)

	out, err := runSandboxed(t, policy, dir, "/bin/cat", outside)
	require.Error(t, err, "reading outside the policy must fail; got output=%s", out)
	assert.NotContains(t, out, "must-not-be-readable", "file contents leaked past the sandbox")

	// Writes outside the workspace must fail AND leave nothing behind.
	forbidden := filepath.Join(t.TempDir(), "should-not-exist.txt")
	out, err = runSandboxed(t, policy, dir, "/bin/bash", "-c", "echo leaked > "+forbidden)
	require.Error(t, err, "writing outside the policy must fail; output=%s", out)
	assert.NoFileExists(t, forbidden, "sandbox permitted a write outside the policy")
}

// TestSeatbelt_RealChild_NetworkDefaultDeny verifies the network posture with a
// real listener rather than an internet call, so the test is hermetic.
func TestSeatbelt_RealChild_NetworkDefaultDeny(t *testing.T) {
	dir, policy := seatbeltTestWorkspace(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	probe := fmt.Sprintf("exec 3<>/dev/tcp/127.0.0.1/%d && echo CONNECTED", port)

	// No ConnectPortRules → every outbound connection is denied.
	out, err := runSandboxed(t, policy, dir, "/bin/bash", "-c", probe)
	assert.Error(t, err, "outbound connect must be denied with an empty port allow-list; output=%s", out)
	assert.NotContains(t, out, "CONNECTED")

	// Same policy plus an explicit allow for that port → connection succeeds.
	// This is the control: it shows the denial above came from the port
	// allow-list, not from the child being unable to use the network at all.
	allowed := policy
	allowed.ConnectPortRules = []NetPortRule{{Port: uint16(port)}}
	out, err = runSandboxed(t, allowed, dir, "/bin/bash", "-c", probe)
	require.NoError(t, err, "allow-listed port must connect; output=%s", out)
	assert.Contains(t, out, "CONNECTED")
}

// TestSeatbelt_ApplyToCmd_RejectsDoubleWrap guards against nesting sandbox-exec
// inside sandbox-exec, where the inner profile would apply to the sandbox-exec
// binary itself and the child would fail for reasons unrelated to the policy.
func TestSeatbelt_ApplyToCmd_RejectsDoubleWrap(t *testing.T) {
	_, policy := seatbeltTestWorkspace(t)
	backend := NewSeatbeltBackend()

	cmd := exec.Command("/bin/echo", "hi")
	require.NoError(t, backend.ApplyToCmd(cmd, policy))
	assert.Equal(t, seatbeltExecPath, cmd.Path)

	err := backend.ApplyToCmd(cmd, policy)
	require.Error(t, err, "second wrap must be refused")
	assert.Contains(t, err.Error(), "double-wrap")
}

// TestSeatbelt_ApplyToCmd_PassesProfileInline asserts the profile travels in
// argv and no .sb file is created. Writing the policy to disk would open a
// window where another process could swap it between write and exec, choosing
// the sandbox policy outright.
func TestSeatbelt_ApplyToCmd_PassesProfileInline(t *testing.T) {
	_, policy := seatbeltTestWorkspace(t)

	backend := NewSeatbeltBackend()
	cmd := exec.Command("/bin/echo", "hi")
	require.NoError(t, backend.ApplyToCmd(cmd, policy))

	require.GreaterOrEqual(t, len(cmd.Args), 4)
	assert.Equal(t, seatbeltExecPath, cmd.Args[0])
	assert.Equal(t, "-p", cmd.Args[1], "profile must be inline, not a -f file reference")
	assert.Contains(t, cmd.Args[2], "(version 1)")
	assert.Equal(t, "--", cmd.Args[3])

	for _, a := range cmd.Args {
		assert.False(t, strings.HasSuffix(a, ".sb"), "no profile file should be referenced: %s", a)
	}
}

// TestSeatbelt_ProductionScaleProfile_Spawns guards the inline-profile size
// assumption against a REALISTIC policy, not a toy one.
//
// Passing the profile in argv trades a temp file for an ARG_MAX budget. A
// minimal policy renders to ~1 KB, which makes the limit look irrelevant — but
// the gateway expands its dev-server port range to one rule per port, so a
// real boot on this host produced 1000 bind + 1003 connect rules and a ~95 KB
// profile. That is still far under the 1 MB ARG_MAX, and this test proves a
// child actually spawns at that size rather than failing with E2BIG in
// production while every small-policy test stays green.
func TestSeatbelt_ProductionScaleProfile_Spawns(t *testing.T) {
	policy := DefaultPolicy(t.TempDir(), nil, nil, nil)
	for p := 20000; p < 21000; p++ {
		policy.BindPortRules = append(policy.BindPortRules, NetPortRule{Port: uint16(p)})
		policy.ConnectPortRules = append(policy.ConnectPortRules, NetPortRule{Port: uint16(p)})
	}

	profile, err := renderSeatbeltProfile(policy)
	require.NoError(t, err)
	t.Logf("production-scale profile: %d bytes", len(profile))
	require.Less(t, len(profile), 1<<20, "profile must stay under ARG_MAX")

	backend := NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}
	cmd := exec.Command("/bin/echo", "size-ok")
	require.NoError(t, backend.ApplyToCmd(cmd, policy))

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "child must spawn at production profile size; output=%s", out)
	assert.Contains(t, string(out), "size-ok")
}

// TestSeatbelt_HardenedExec_WrapsChildren verifies the production seam. The
// unit tests above call ApplyToCmd directly; this one goes through
// applyPlatformHardening, which is what the real spawn path uses. A backend
// that works only when a test calls it explicitly would be worthless.
func TestSeatbelt_HardenedExec_WrapsChildren(t *testing.T) {
	_, policy := seatbeltTestWorkspace(t)

	// No policy installed → children must be left alone.
	currentSeatbeltBackend.Store(nil)
	plain := exec.Command("/bin/echo", "hi")
	require.NoError(t, applyPlatformHardening(plain, Limits{}))
	assert.Equal(t, "/bin/echo", plain.Path, "child must not be wrapped when no policy is installed")

	// Installing a policy must cause subsequent spawns to be wrapped.
	backend := NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host")
	}
	require.NoError(t, backend.Apply(policy))
	t.Cleanup(func() { currentSeatbeltBackend.Store(nil) })

	wrapped := exec.Command("/bin/echo", "hi")
	require.NoError(t, applyPlatformHardening(wrapped, Limits{}))
	assert.Equal(t, seatbeltExecPath, wrapped.Path, "installed policy must confine spawned children")
	assert.True(t, wrapped.SysProcAttr.Setpgid, "Setpgid must survive the wrap")
}
