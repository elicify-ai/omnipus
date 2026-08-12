//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// openPolicy builds an open-model policy over home, matching what the gateway
// computes at boot.
func openPolicy(home string) SandboxPolicy {
	return DefaultPolicyForModel(FilesystemModelOpen, home, nil, nil, nil, nil)
}

// TestSeatbelt_DenyBlockIsLast is spec FR-3.2, and it is the assertion that
// catches the ordering bug ADR-062 §4.1 documents rather than trusting a
// reviewer to notice it.
//
// $OMNIPUS_HOME is granted RWX — a FILTERED allow over the directory the secret
// set lives in — and a filtered allow placed after a deny defeats it, as
// TestSeatbelt_DenyPrecedenceIsMeasuredNotAssumed demonstrates against real
// children. Move that grant below the deny block and the profile reads exactly
// as it does now, every deny present and correctly spelled, while protecting
// nothing.
//
// The check also rejects trailing BLANKET allows, which the measurements show
// are harmless. That is deliberate over-strictness: "nothing after the deny
// block" is a rule a reviewer can apply without first re-deriving which allows
// are filtered.
func TestSeatbelt_DenyBlockIsLast(t *testing.T) {
	home := t.TempDir()
	profile, err := renderSeatbeltProfile(openPolicy(home))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	lastDeny := strings.LastIndex(profile, "(deny file-read* (subpath")
	if lastDeny < 0 {
		t.Fatal("open profile emitted no secret denies at all")
	}
	tail := profile[lastDeny:]
	for _, forbidden := range []string{"(allow file-read*", "(allow file-write*", "(allow process-exec"} {
		if strings.Contains(tail, forbidden) {
			t.Errorf("%q appears AFTER the deny block. A filtered allow there silently re-opens "+
				"every secret; nothing may be appended after the deny block.", forbidden)
		}
	}
}

// TestSeatbelt_OpenModelEmitsBlanketAllows covers FR-3.1. Both allows must be
// present: opening execute while leaving read enumerated stops every child
// starting, because a binary cannot be mmap'd unreadable.
func TestSeatbelt_OpenModelEmitsBlanketAllows(t *testing.T) {
	profile, err := renderSeatbeltProfile(openPolicy(t.TempDir()))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"(allow file-read*)\n", "(allow process-exec)\n"} {
		if !strings.Contains(profile, want) {
			t.Errorf("open profile must contain %q", strings.TrimSpace(want))
		}
	}

	confined, err := renderSeatbeltProfile(
		DefaultPolicyForModel(FilesystemModelConfined, t.TempDir(), nil, nil, nil, nil))
	if err != nil {
		t.Fatalf("render confined: %v", err)
	}
	for _, forbidden := range []string{"(allow file-read*)\n", "(allow process-exec)\n"} {
		if strings.Contains(confined, forbidden) {
			t.Errorf("confined profile must NOT contain the blanket allow %q", strings.TrimSpace(forbidden))
		}
	}
}

// TestSeatbelt_DenyCoversWriteNotOnlyRead is spec FR-3.2a. A read-only deny is
// defeated in two syscalls and the file is then read normally, so the write
// half is not defence in depth — it is the difference between the deny working
// and not working.
func TestSeatbelt_DenyCoversWriteNotOnlyRead(t *testing.T) {
	home := t.TempDir()
	profile, err := renderSeatbeltProfile(openPolicy(home))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, secret := range SecretPaths(home) {
		resolved, _ := resolveSeatbeltPath(secret)
		for _, verb := range []string{"file-read*", "file-write*"} {
			want := "(deny " + verb + " (subpath \"" + resolved + "\"))"
			if !strings.Contains(profile, want) {
				t.Errorf("missing %s deny for %s\nwanted line: %s", verb, secret, want)
			}
		}
	}
}

// TestSeatbelt_DenyEmittedForNonExistentPath covers FR-3.4. On a fresh install
// master.key does not exist until the gateway mints it; a deny emitted only for
// extant paths would leave a window in which the file is created and readable.
func TestSeatbelt_DenyEmittedForNonExistentPath(t *testing.T) {
	home := t.TempDir()
	if _, err := os.Stat(filepath.Join(home, "master.key")); !os.IsNotExist(err) {
		t.Fatalf("precondition: master.key must not exist, stat err = %v", err)
	}
	profile, err := renderSeatbeltProfile(openPolicy(home))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(profile, "master.key") {
		t.Error("deny for a not-yet-created master.key must still be emitted")
	}
}

// TestSeatbelt_OpenProfileKeepsIPCDefaultDeny is spec FR-10.1/FR-10.2.
//
// A path deny does not stop a daemon-mediated read: securityd will service a
// keychain query for a sandboxed client, and launchctl outside the sandbox
// exfiltrates cleanly. Both are blocked ONLY by the preamble's (deny default)
// over mach-lookup. This test exists so a future "make the toolchain work" edit
// cannot widen mach-lookup and silently delete that protection.
func TestSeatbelt_OpenProfileKeepsIPCDefaultDeny(t *testing.T) {
	profile, err := renderSeatbeltProfile(openPolicy(t.TempDir()))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, line := range strings.Split(profile, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "(allow mach-lookup") {
			continue
		}
		if strings.Contains(trimmed, "com.apple.mDNSResponder") {
			continue // the one sanctioned entry; without it name resolution dies
		}
		t.Errorf("open profile widens mach-lookup beyond mDNSResponder: %q — "+
			"this re-opens the securityd and launchctl paths that no file deny can close", trimmed)
	}
}

// TestSeatbelt_RealChildCannotReachSecrets executes the policy rather than
// reading it. Every claim above is about rendered text; this one is about what
// the kernel actually does, which is the only claim that matters.
func TestSeatbelt_RealChildCannotReachSecrets(t *testing.T) {
	if _, err := os.Stat(seatbeltExecPath); err != nil {
		t.Skipf("sandbox-exec unavailable: %v", err)
	}
	home := t.TempDir()
	secret := filepath.Join(home, "master.key")
	if err := os.WriteFile(secret, []byte("SUPER-SECRET-KEY"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	control := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(control, []byte("ordinary"), 0o600); err != nil {
		t.Fatalf("seed control: %v", err)
	}

	profile, err := renderSeatbeltProfile(openPolicy(home))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	run := func(args ...string) error {
		cmd := exec.Command(seatbeltExecPath, append([]string{"-p", profile}, args...)...)
		return cmd.Run()
	}

	// Control: the open model must actually be open, or a passing deny below
	// would prove nothing more than a broken profile.
	if err := run("/bin/cat", control); err != nil {
		t.Fatalf("control read must succeed under the open model: %v", err)
	}
	if err := run("/bin/cat", "/etc/hosts"); err != nil {
		t.Fatalf("open model must permit reads outside any enumerated list: %v", err)
	}

	t.Run("read denied", func(t *testing.T) {
		if err := run("/bin/cat", secret); err == nil {
			t.Error("child read master.key — the deny did not hold")
		}
	})

	// FR-3.2a: rename is not a read. Without the write deny this succeeds and
	// the key is then read under its new name, defeating the read deny entirely.
	t.Run("rename denied", func(t *testing.T) {
		moved := filepath.Join(home, "k")
		if err := run("/bin/mv", secret, moved); err == nil {
			t.Error("child renamed master.key out from under the deny")
		}
		if _, err := os.Stat(secret); err != nil {
			t.Errorf("master.key must still be in place after a denied rename: %v", err)
		}
	})

	// FR-3.2c / S-3c: integrity, not just confidentiality. A zero-length master
	// key destroys every stored credential irreversibly, and needs no read.
	t.Run("truncate denied", func(t *testing.T) {
		if err := run("/bin/sh", "-c", ": > "+secret); err == nil {
			t.Error("child truncated master.key — the vault would be irrecoverable")
		}
		data, err := os.ReadFile(secret)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if string(data) != "SUPER-SECRET-KEY" {
			t.Errorf("master.key was modified: %q", data)
		}
	})
}

// TestSeatbelt_DenyPrecedenceIsMeasuredNotAssumed pins the platform behaviour
// the deny block's position depends on, by executing real children rather than
// inspecting text.
//
// It exists because the obvious shorthand — "Seatbelt is last-match-wins" — is
// wrong in a way that matters. An unfiltered (allow file-read*) does NOT
// override an earlier deny, while a filtered allow over the enclosing directory
// DOES. Since $OMNIPUS_HOME is granted exactly such a filtered allow, that
// second case is the one protecting the secret set, and a future reader
// reasoning from the shorthand would draw the wrong conclusion about which
// reorderings are safe.
//
// If macOS ever changes this, this test fails here — loudly and in one place —
// rather than silently unprotecting every secret in production.
func TestSeatbelt_DenyPrecedenceIsMeasuredNotAssumed(t *testing.T) {
	if _, err := os.Stat(seatbeltExecPath); err != nil {
		t.Skipf("sandbox-exec unavailable: %v", err)
	}
	dir := t.TempDir()
	secret := filepath.Join(dir, "k.txt")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Seatbelt matches RESOLVED paths. Filters must name the resolved form or
	// they match nothing — which silently turns any assertion below into a
	// tautology about a rule that never fired.
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	resolvedSecret := filepath.Join(resolvedDir, "k.txt")

	base := "(version 1)\n(deny default)\n" + seatbeltSystemPreamble
	allowDir := "(allow file-read* (subpath \"" + resolvedDir + "\"))\n"
	denySecret := "(deny file-read* (subpath \"" + resolvedSecret + "\"))\n"
	blanketAllow := "(allow file-read*)\n"

	canRead := func(extra string) bool {
		return exec.Command(seatbeltExecPath, "-p", base+extra, "/bin/cat", secret).Run() == nil
	}

	for _, tc := range []struct {
		name    string
		profile string
		want    bool
	}{
		// Without this control passing, every "denied" below could just mean
		// the child never started.
		{"control: filtered allow alone permits the read", allowDir, true},
		{"filtered allow THEN deny: denied", allowDir + denySecret, false},
		{"deny THEN filtered allow: DEFEATED", denySecret + allowDir, true},
		{"blanket allow THEN deny: denied", blanketAllow + denySecret, false},
		{"deny THEN blanket allow: still denied", denySecret + blanketAllow, false},
		{"what we actually ship: blanket + filtered allow THEN deny", blanketAllow + allowDir + denySecret, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := canRead(tc.profile); got != tc.want {
				t.Errorf("read succeeded = %v, want %v — Seatbelt precedence has changed, "+
					"and the deny block's position in renderSeatbeltProfile may no longer protect the secret set", got, tc.want)
			}
		})
	}
}
