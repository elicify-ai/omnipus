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
	defer func() {
		if closeErr := ln.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			if closeErr := conn.Close(); closeErr != nil {
				_ = closeErr
			}
		}
	}()

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok, "listener address has unexpected type %T, want *net.TCPAddr", ln.Addr())
	port := tcpAddr.Port
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
	policy := DefaultPolicy(t.TempDir(), nil, nil, nil, nil)
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

// execFixture writes a runnable script into dir and returns its path.
func execFixture(t *testing.T, dir, marker string) string {
	t.Helper()
	p := filepath.Join(dir, "tool.sh")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\necho "+marker+"\n"), 0o755))
	return p
}

// TestSeatbelt_RealChild_ExecutesBinaryInAllowedExecPath is the positive
// control for sandbox.allowed_exec_paths: a binary in a granted directory must
// actually run. Before this existed, an agent on macOS could not run node, npm
// or anything else installed by Homebrew or a version manager.
//
// A temp dir is used rather than a real Homebrew path so the test passes on a
// runner with no Homebrew installed.
func TestSeatbelt_RealChild_ExecutesBinaryInAllowedExecPath(t *testing.T) {
	ws, policy := seatbeltTestWorkspace(t)

	toolDir := t.TempDir()
	tool := execFixture(t, toolDir, "TOOL-RAN")
	policy.FilesystemRules = append(policy.FilesystemRules,
		PathRule{Path: toolDir, Access: AccessRead | AccessExecute})

	out, err := runSandboxed(t, policy, ws, "/bin/sh", tool)
	require.NoError(t, err, "a binary in an allowed exec path must run; output=%s", out)
	assert.Contains(t, out, "TOOL-RAN")
}

// TestSeatbelt_RealChild_DeniesExecOutsideAllowedExecPaths is the negative
// control. Without it, the positive test above could pass simply because the
// sandbox was not enforcing anything.
func TestSeatbelt_RealChild_DeniesExecOutsideAllowedExecPaths(t *testing.T) {
	ws, policy := seatbeltTestWorkspace(t)

	grantedDir := t.TempDir()
	policy.FilesystemRules = append(policy.FilesystemRules,
		PathRule{Path: grantedDir, Access: AccessRead | AccessExecute})

	// Identical script, different directory, deliberately NOT in the policy.
	ungrantedDir := t.TempDir()
	tool := execFixture(t, ungrantedDir, "SHOULD-NOT-RUN")

	out, err := runSandboxed(t, policy, ws, "/bin/sh", tool)
	require.Error(t, err, "exec outside the granted paths must fail; output=%s", out)
	assert.NotContains(t, out, "SHOULD-NOT-RUN")
}

// TestSeatbelt_RealChild_ExecPathIsNotWritable proves the central safety claim
// of allowed_exec_paths at the kernel level rather than by inspection: the
// directory an agent may execute from is one it cannot write to. Note the
// temp dir is owned and writable by the test user — so a pass here shows
// Seatbelt overriding ordinary Unix permissions, which is exactly why granting
// exec on a user-owned Homebrew prefix does not let an agent plant a binary.
func TestSeatbelt_RealChild_ExecPathIsNotWritable(t *testing.T) {
	ws, policy := seatbeltTestWorkspace(t)

	toolDir := t.TempDir()
	policy.FilesystemRules = append(policy.FilesystemRules,
		PathRule{Path: toolDir, Access: AccessRead | AccessExecute})

	planted := filepath.Join(toolDir, "planted.sh")
	out, err := runSandboxed(t, policy, ws, "/bin/bash", "-c", "echo x > "+planted)
	require.Error(t, err, "writing into an exec-only path must fail; output=%s", out)
	assert.NoFileExists(t, planted, "sandbox allowed a write into a read+exec directory")
}

// TestSeatbelt_RealChild_ExecPathViaSymlinkChain mirrors the Homebrew layout
// (bin/tool -> ../Cellar/tool/1.0/bin/tool). Homebrew resolves every binary
// through such a chain, so if this broke, granting the prefix would appear to
// work in unit tests and fail for every real tool.
func TestSeatbelt_RealChild_ExecPathViaSymlinkChain(t *testing.T) {
	ws, policy := seatbeltTestWorkspace(t)

	prefix := t.TempDir()
	realDir := filepath.Join(prefix, "Cellar", "tool", "1.0", "bin")
	require.NoError(t, os.MkdirAll(realDir, 0o755))
	realTool := execFixture(t, realDir, "SYMLINKED-TOOL-RAN")

	binDir := filepath.Join(prefix, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	link := filepath.Join(binDir, "tool.sh")
	require.NoError(t, os.Symlink(realTool, link))

	policy.FilesystemRules = append(policy.FilesystemRules,
		PathRule{Path: prefix, Access: AccessRead | AccessExecute})

	out, err := runSandboxed(t, policy, ws, "/bin/sh", link)
	require.NoError(t, err, "a Homebrew-style symlink chain must resolve; output=%s", out)
	assert.Contains(t, out, "SYMLINKED-TOOL-RAN")
}

// TestSeatbelt_RealChild_CanUseTMPDIR is the regression test for a failure that
// would have made the sandbox unusable in practice while every existing test
// stayed green.
//
// DefaultPolicy grants /tmp, but on macOS almost nothing writes there:
// os.TempDir(), mktemp, npm, pip, git and `go build` all honour $TMPDIR, which
// is a per-user directory under /var/folders. hardened_exec forwards TMPDIR to
// the child, so before this the child was handed a temp directory the profile
// denied — and the resulting failures ("operation not permitted" from mktemp,
// EACCES from npm) never mention the sandbox at all.
func TestSeatbelt_RealChild_CanUseTMPDIR(t *testing.T) {
	home := t.TempDir()
	policy := DefaultPolicy(home, nil, nil, nil, nil)

	// mktemp is what real tooling ends up calling; use it rather than a
	// hand-rolled path so the test exercises the same code path npm would.
	out, err := runSandboxed(t, policy, home, "/bin/bash", "-c",
		`f=$(mktemp) && echo ok > "$f" && cat "$f" && rm -f "$f"`)
	require.NoError(t, err, "a child must be able to use $TMPDIR; output=%s", out)
	assert.Contains(t, out, "ok")
}

// TestDefaultPolicy_GrantsPerUserTempDir pins the rule itself, so the grant
// cannot be dropped by a future edit without a platform-independent failure.
func TestDefaultPolicy_GrantsPerUserTempDir(t *testing.T) {
	policy := DefaultPolicy(t.TempDir(), nil, nil, nil, nil)

	want := filepath.Clean(os.TempDir())
	if want == "/tmp" {
		t.Skip("$TMPDIR is /tmp on this host; the separate rule is unnecessary")
	}

	var found bool
	for _, r := range policy.FilesystemRules {
		if r.Path == want {
			found = true
			assert.NotZero(t, r.Access&AccessWrite, "$TMPDIR must be writable")
			assert.NotZero(t, r.Access&AccessRead, "$TMPDIR must be readable")
		}
	}
	assert.True(t, found, "policy must grant the per-user temp dir %q", want)
}

// TestSeatbelt_RealChild_UDPFollowsThePortAllowList proves the UDP fix against a
// real child and a real socket, with a control.
//
// Before this, the renderer emitted only TCP allows, so UDP was denied outright
// no matter what the policy said. That silently broke raw DNS resolvers (dig,
// nslookup, Go's pure-Go resolver) and QUIC/HTTP3. It stayed hidden because the
// preamble's mDNSResponder allow covers ordinary name lookups, so DNS "worked"
// right up until something bypassed the system resolver.
func TestSeatbelt_RealChild_UDPFollowsThePortAllowList(t *testing.T) {
	ws, policy := seatbeltTestWorkspace(t)

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			_ = closeErr
		}
	}()
	go func() {
		buf := make([]byte, 64)
		for {
			n, addr, readErr := conn.ReadFrom(buf)
			if readErr != nil {
				return
			}
			if _, writeErr := conn.WriteTo([]byte("UDP-REPLY"), addr); writeErr != nil {
				_ = writeErr
			}
			_ = n
		}
	}()

	udpAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	require.True(t, ok, "listener address has unexpected type %T, want *net.UDPAddr", conn.LocalAddr())
	port := udpAddr.Port
	probe := fmt.Sprintf("echo ping | nc -u -w2 127.0.0.1 %d", port)

	// Control: the port is NOT allow-listed, so UDP must be denied. Without
	// this, the positive case below could pass simply because nothing enforces.
	out, ctrlErr := runSandboxed(t, policy, ws, "/bin/bash", "-c", probe)
	if ctrlErr != nil {
		_ = ctrlErr // a non-zero exit is one possible symptom of the expected denial; only the output content is asserted
	}
	assert.NotContains(t, out, "UDP-REPLY", "UDP to a non-allow-listed port must be denied")

	// Allow-listing the port must permit UDP, not just TCP.
	allowed := policy
	allowed.ConnectPortRules = []NetPortRule{{Port: uint16(port)}}
	out, err = runSandboxed(t, allowed, ws, "/bin/bash", "-c", probe)
	require.NoError(t, err, "UDP to an allow-listed port must succeed; output=%s", out)
	assert.Contains(t, out, "UDP-REPLY")
}
