//go:build linux

// Package sandbox_test — insider-LLM red-team coverage for credential exfil.
//
// These tests model the threat that a hostile prompt steers an agent into
// reading the gateway's master key or credentials.json out from under the
// active sandbox.
//
//	C1 — master.key exfil.
//	C2 — credentials.json exfil.
//
// **Scope of these tests.** They re-exec the test binary as a sandboxed
// child and apply DefaultChildPolicy directly. They prove that
// DefaultChildPolicy's structure (subdirectory enumeration with secret-file
// carve-out) blocks reads at the kernel layer when applied. They do NOT
// prove that the production sandbox-apply path uses DefaultChildPolicy —
// it does not, as of v0.2. See the doc comment on DefaultChildPolicy in
// pkg/sandbox/sandbox.go for the production threat model (regex backstop +
// HardenGatewaySelf + kernel sandbox baseline).
//
// Production wiring (Landlock per-thread re-restriction in hardened-exec)
// is tracked as a v0.3 follow-up. Until it lands, these tests serve as:
//
//	(a) policy-correctness tests for DefaultChildPolicy — the carve-out
//	    structure works at the kernel level when applied
//	(b) regression tests to prevent the carve-out from being broken when
//	    the v0.3 wiring eventually lands

package sandbox_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// nonTmpHome returns a fresh tempdir that is NOT under /tmp. The DefaultPolicy
// (and hence DefaultChildPolicy by inheritance) grants RWX on /tmp, so the
// secrets carve-out is meaningless when the test home sits inside /tmp — the
// /tmp rule alone permits the read regardless of whether the home root is
// granted. Putting the test home under /var/tmp/<random> sidesteps this:
// /var/tmp is not granted by the policy, so the child has access only to
// the explicitly-granted subdirs of $OMNIPUS_HOME — and the carved-out
// secret files are unreachable.
func nonTmpHome(t *testing.T) string {
	t.Helper()
	const base = "/var/tmp"
	if _, err := os.Stat(base); err != nil {
		t.Skipf("non-/tmp tempdir base %q unavailable on this host: %v", base, err)
	}
	dir, err := os.MkdirTemp(base, "omnipus-redteam-")
	if err != nil {
		t.Skipf("cannot create non-/tmp tempdir under %q: %v", base, err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

// runSecretReadChild is the in-child phase of TestRedteam_MasterKey_Exfil_Blocked
// and TestRedteam_Credentials_Exfil_Blocked. The parent re-execs the test binary
// with one of two sentinel env vars set; this helper dispatches based on which.
//
// Exit codes follow the existing convention from backend_linux_subprocess_test.go:
//
//	42 — the read was blocked at the kernel layer (EACCES/EPERM). PASS.
//	77 — Landlock unavailable in the child. The parent skips.
//	1  — the read SUCCEEDED. The control is missing — FAIL by design.
//	2  — the read failed with an unexpected errno (not EACCES/EPERM and not nil).
//	     Diagnostic — surfaces test-environment problems separately from the
//	     "control missing" signal.
func runSecretReadChild(target string) {
	runtime.LockOSThread()

	home := os.Getenv("OMNIPUS_REDTEAM_HOME")
	if home == "" {
		fmt.Fprintln(os.Stderr, "redteam_secret_read: missing OMNIPUS_REDTEAM_HOME")
		os.Exit(77)
	}

	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		os.Exit(77)
	}

	// Mirror the production wave2 hardened-exec policy as closely as we can:
	// $OMNIPUS_HOME gets RWX (this is the open hole the test documents),
	// /tmp gets RWX, and the standard system read paths get R+X. This is
	// EXACTLY the policy DefaultPolicy returns, so if a real production
	// child can read master.key under it, this child will too.
	policy := sandbox.DefaultChildPolicy(home, nil, nil, nil)

	if err := backend.Apply(policy); err != nil {
		fmt.Fprintf(os.Stderr, "Apply failed (skip): %v\n", err)
		os.Exit(77)
	}

	// Now attempt the read. Anything that succeeds counts as a documented
	// gap because production policy explicitly grants R on $OMNIPUS_HOME.
	_, err := os.ReadFile(target)
	if err == nil {
		fmt.Fprintf(os.Stderr,
			"redteam_secret_read: read of %q SUCCEEDED — kernel sandbox did NOT block it\n", target)
		os.Exit(1)
	}
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		fmt.Fprintf(os.Stderr, "redteam_secret_read: %q blocked: %v\n", target, err)
		os.Exit(42)
	}
	fmt.Fprintf(os.Stderr, "redteam_secret_read: unexpected error reading %q: %v\n", target, err)
	os.Exit(2)
}

// TestRedteam_MasterKey_Exfil_Blocked documents C1 from the insider-pentest
// report. It builds an OMNIPUS_HOME containing a master.key file with mode
// 0600, then re-execs the test binary as a sandboxed child and asks it to
// read master.key. Production policy (DefaultPolicy) grants RWX on
// $OMNIPUS_HOME, so the read is expected to succeed today — the test FAILS
// by design until v0.2 (#155) lands the secrets-subtree carve-out.
//
// Reference: docs/architecture/AS-IS-architecture.md (sandbox carve-out for
// $OMNIPUS_HOME), pkg/sandbox/sandbox.go::DefaultPolicy.
func TestRedteam_MasterKey_Exfil_Blocked(t *testing.T) {
	t.Logf(
		"documents C1 (master.key exfil) from insider-pentest report; closes when v0.2 #155 secrets-subtree carve-out lands",
	)

	if os.Getenv("OMNIPUS_REDTEAM_MASTER_KEY_CHILD") == "1" {
		runSecretReadChild(os.Getenv("OMNIPUS_REDTEAM_TARGET"))
		return // unreachable
	}

	if os.Getuid() == 0 {
		// Root bypasses Landlock entirely. Don't pretend to test it.
		t.Skip("redteam tests must run as non-root (root bypasses Landlock)")
	}

	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		t.Skipf("Landlock backend not available (backend=%q) — cannot exercise C1 on this kernel", name)
	}
	_ = backend

	home := nonTmpHome(t)
	target := filepath.Join(home, "master.key")
	// 64 hex chars — same shape as the real master key, content irrelevant.
	keyBlob := strings.Repeat("a1b2c3d4", 8)
	if err := os.WriteFile(target, []byte(keyBlob), 0o600); err != nil {
		t.Fatalf("seed master.key: %v", err)
	}

	//nolint:gosec // intentional self-exec
	cmd := exec.Command(os.Args[0],
		"-test.run=TestRedteam_MasterKey_Exfil_Blocked",
		"-test.count=1",
		"-test.v",
	)
	cmd.Env = append(os.Environ(),
		"OMNIPUS_REDTEAM_MASTER_KEY_CHILD=1",
		"OMNIPUS_REDTEAM_HOME="+home,
		"OMNIPUS_REDTEAM_TARGET="+target,
	)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("child failed to spawn: %v\n%s", err, out)
		}
	}

	switch exitCode {
	case 42:
		// Kernel blocked the read — the v0.2 fix has landed.
		t.Logf("C1 closed: kernel blocked master.key read inside sandboxed child")
	case 77:
		t.Skipf("Landlock unavailable in child (exit 77):\n%s", out)
	case 1:
		// EXPECTED FAIL by design. The control is missing.
		t.Errorf(
			"C1 GAP CONFIRMED: sandboxed child READ master.key — secrets-subtree carve-out not yet implemented (#155)\nchild stderr:\n%s",
			out,
		)
	default:
		t.Fatalf("child exit %d (unexpected — expected 1=gap, 42=fixed, 77=skip):\n%s", exitCode, out)
	}
}

// TestRedteam_Credentials_Exfil_Blocked documents C2. Same shape as C1 but
// targets credentials.json (the AES-256-GCM-encrypted credential store).
// Decryption requires the master key, so an attacker who only obtains
// credentials.json cannot trivially read the channel tokens — but combined
// with C1, the master key is reachable too, so credential decryption is end
// to end feasible. Listing C2 separately makes the regression coverage
// explicit: even if an alternate fix narrows only master.key, credentials.json
// must still be denied.
func TestRedteam_Credentials_Exfil_Blocked(t *testing.T) {
	t.Logf(
		"documents C2 (credentials.json exfil) from insider-pentest report; closes when v0.2 #155 secrets-subtree carve-out lands",
	)

	if os.Getenv("OMNIPUS_REDTEAM_CREDS_CHILD") == "1" {
		runSecretReadChild(os.Getenv("OMNIPUS_REDTEAM_TARGET"))
		return
	}

	if os.Getuid() == 0 {
		t.Skip("redteam tests must run as non-root (root bypasses Landlock)")
	}

	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		t.Skipf("Landlock backend not available (backend=%q)", name)
	}
	_ = backend

	home := nonTmpHome(t)
	target := filepath.Join(home, "credentials.json")
	// Realistic shape of an encrypted credentials file. Content is opaque
	// AES-GCM ciphertext in production; whatever bytes we put here is fine
	// — the test only cares whether the read returned data or EACCES.
	if err := os.WriteFile(target,
		[]byte(`{"version":1,"records":{"openrouter":{"ciphertext":"AABBCC..."}}}`),
		0o600); err != nil {
		t.Fatalf("seed credentials.json: %v", err)
	}

	//nolint:gosec // intentional self-exec
	cmd := exec.Command(os.Args[0],
		"-test.run=TestRedteam_Credentials_Exfil_Blocked",
		"-test.count=1",
		"-test.v",
	)
	cmd.Env = append(os.Environ(),
		"OMNIPUS_REDTEAM_CREDS_CHILD=1",
		"OMNIPUS_REDTEAM_HOME="+home,
		"OMNIPUS_REDTEAM_TARGET="+target,
	)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("child failed to spawn: %v\n%s", err, out)
		}
	}

	switch exitCode {
	case 42:
		t.Logf("C2 closed: kernel blocked credentials.json read inside sandboxed child")
	case 77:
		t.Skipf("Landlock unavailable in child (exit 77):\n%s", out)
	case 1:
		t.Errorf(
			"C2 GAP CONFIRMED: sandboxed child READ credentials.json — secrets-subtree carve-out not yet implemented (#155)\nchild stderr:\n%s",
			out,
		)
	default:
		t.Fatalf("child exit %d (unexpected — expected 1=gap, 42=fixed, 77=skip):\n%s", exitCode, out)
	}
}
