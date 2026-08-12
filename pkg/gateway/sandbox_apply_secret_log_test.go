// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for review findings 3 and 4 on applySandbox
// (pkg/gateway/sandbox_apply.go):
//
//   - Finding 3 (MAJOR): the sandbox.secret_set.protected/UNPROTECTED boot
//     log used to switch on runtime.GOOS alone, after the real capability
//     (confinesChildren/kernelConfiner, isLinux) was already known — and
//     ignored it. A degraded backend (FallbackBackend selected on any
//     platform, or Seatbelt under mode=permissive) still logged a
//     "protected" mechanism name. These tests assert the log is now honest:
//     UNPROTECTED whenever nothing is actually enforcing, "protected" only
//     when the real capability + mode combination proves it.
//   - Finding 4 (MINOR): ADR-060 §10.1 requires sandbox.applied to log the
//     filesystem model. Neither of the two emission sites did. These tests
//     assert both now carry "model".
//
// Every test captures slog output via a JSON handler (the established
// pattern in rest_sandbox_config_test.go) rather than asserting on
// applySandbox's return value, because the dishonesty here was specifically
// in what gets WRITTEN TO THE BOOT LOG — an operator reads logs, not Go
// structs, to decide whether a deployment is safe.
package gateway

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// captureSlogJSON redirects the default slog logger to a JSON buffer for the
// duration of the test and restores it on cleanup. Returns a function that
// parses every captured line into a slice of event maps.
func captureSlogJSON(t *testing.T) (lines func() []map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	return func() []map[string]any {
		var out []map[string]any
		for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
			if line == "" {
				continue
			}
			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("parse log line %q: %v", line, err)
			}
			out = append(out, entry)
		}
		return out
	}
}

// findEvent returns the first captured log entry whose "msg" matches, or nil.
func findEvent(entries []map[string]any, msg string) map[string]any {
	for _, e := range entries {
		if e["msg"] == msg {
			return e
		}
	}
	return nil
}

// fakeLinuxBackend simulates a real LinuxBackend for tests that need to
// exercise the isLinux==true path (Landlock actually selected and capable)
// without depending on a real Linux kernel or Landlock syscalls. It
// implements linuxApplier (ApplyWithMode), abiReporter (ABIVersion), and
// policyApplyReporter (PolicyApplied) — the same trio the real LinuxBackend
// satisfies — so DescribeBackendWithState and applySandbox's own isLinux type
// assertion treat it identically to the genuine kernel backend.
type fakeLinuxBackend struct {
	applied bool
}

func (f *fakeLinuxBackend) Name() string    { return "landlock-v3" }
func (f *fakeLinuxBackend) Available() bool { return true }
func (f *fakeLinuxBackend) Apply(policy sandbox.SandboxPolicy) error {
	return f.ApplyWithMode(policy, sandbox.ModeEnforce)
}
func (f *fakeLinuxBackend) ApplyToCmd(_ *exec.Cmd, _ sandbox.SandboxPolicy) error { return nil }
func (f *fakeLinuxBackend) ApplyWithMode(_ sandbox.SandboxPolicy, _ sandbox.Mode) error {
	f.applied = true
	return nil
}
func (f *fakeLinuxBackend) ABIVersion() int     { return 4 }
func (f *fakeLinuxBackend) PolicyApplied() bool { return f.applied }

// TestApplySandbox_SecretSetLog_FallbackBackend_UNPROTECTED is the core
// regression for finding 3: a FallbackBackend confines nothing at the kernel
// layer regardless of platform, so the boot log must say UNPROTECTED, never
// name a kernel mechanism. Runs on every platform (this is exactly the case
// that was silently wrong on both Linux, pre-fix, and macOS).
func TestApplySandbox_SecretSetLog_FallbackBackend_UNPROTECTED(t *testing.T) {
	entries := captureSlogJSON(t)

	cfg := &config.Config{}
	cfg.Sandbox.Mode = "enforce"

	_, err := applySandbox(SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  sandbox.NewFallbackBackend(),
		GetEnv:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("applySandbox: %v", err)
	}

	captured := entries()
	if protected := findEvent(captured, "sandbox.secret_set.protected"); protected != nil {
		t.Errorf("FallbackBackend must never log sandbox.secret_set.protected; got %v", protected)
	}
	unprotected := findEvent(captured, "sandbox.secret_set.UNPROTECTED")
	if unprotected == nil {
		t.Fatalf("expected sandbox.secret_set.UNPROTECTED to be logged; captured events: %v", captured)
	}
	if unprotected["backend"] != "fallback" {
		t.Errorf("UNPROTECTED log backend = %v, want fallback", unprotected["backend"])
	}
}

// TestApplySandbox_SecretSetLog_Linux_Enforce_Protected is the positive
// control for the Linux branch: a backend that actually implements
// linuxApplier (isLinux==true) under mode=enforce IS the real protection
// mechanism, so the log must say "protected"/landlock_never_granted. Without
// this test, a build that always logs UNPROTECTED would also pass the
// FallbackBackend test above.
func TestApplySandbox_SecretSetLog_Linux_Enforce_Protected(t *testing.T) {
	entries := captureSlogJSON(t)

	cfg := &config.Config{}
	cfg.Sandbox.Mode = "enforce"

	_, err := applySandbox(SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  &fakeLinuxBackend{},
		GetEnv:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("applySandbox: %v", err)
	}

	captured := entries()
	if unprotected := findEvent(captured, "sandbox.secret_set.UNPROTECTED"); unprotected != nil {
		t.Errorf("a real, enforcing Landlock backend must not log UNPROTECTED; got %v", unprotected)
	}
	protected := findEvent(captured, "sandbox.secret_set.protected")
	if protected == nil {
		t.Fatalf("expected sandbox.secret_set.protected to be logged; captured events: %v", captured)
	}
	if protected["mechanism"] != "landlock_never_granted" {
		t.Errorf("protected log mechanism = %v, want landlock_never_granted", protected["mechanism"])
	}
}

// TestApplySandbox_SecretSetLog_Linux_Permissive_UNPROTECTED asserts that
// mode=permissive on an otherwise-capable Landlock backend does NOT claim
// protection: permissive computes and audit-logs the policy but does not
// block anything, so the secret set is not actually denied to a child in
// that mode. This is the "Landlock actually enforcing" half of the finding 3
// fix guidance, distinct from merely "isLinux".
func TestApplySandbox_SecretSetLog_Linux_Permissive_UNPROTECTED(t *testing.T) {
	entries := captureSlogJSON(t)

	cfg := &config.Config{}
	cfg.Sandbox.Mode = "permissive"

	_, err := applySandbox(SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		Backend:  &fakeLinuxBackend{},
		GetEnv:   func(string) string { return "" },
	})
	if err != nil {
		t.Fatalf("applySandbox: %v", err)
	}

	captured := entries()
	if protected := findEvent(captured, "sandbox.secret_set.protected"); protected != nil {
		t.Errorf("mode=permissive must not claim secret-set protection; got %v", protected)
	}
	if findEvent(captured, "sandbox.secret_set.UNPROTECTED") == nil {
		t.Errorf("expected sandbox.secret_set.UNPROTECTED under mode=permissive; captured events: %v", captured)
	}
}

// TestApplySandbox_SecretSetLog_ModelIncludedInBothVariants pins that the
// "model" field (ADR-060 §10.1) is present on both the protected and the
// UNPROTECTED shape of the secret-set log, and on both sandbox.applied
// emission sites (finding 4).
func TestApplySandbox_SecretSetLog_ModelIncludedInBothVariants(t *testing.T) {
	t.Run("UNPROTECTED via fallback backend", func(t *testing.T) {
		entries := captureSlogJSON(t)
		cfg := &config.Config{}
		cfg.Sandbox.Mode = "enforce"
		cfg.Sandbox.FilesystemModel = string(sandbox.FilesystemModelOpen)

		_, err := applySandbox(SandboxApplyOptions{
			Cfg:      cfg,
			HomePath: t.TempDir(),
			Backend:  sandbox.NewFallbackBackend(),
			GetEnv:   func(string) string { return "" },
		})
		if err != nil {
			t.Fatalf("applySandbox: %v", err)
		}
		e := findEvent(entries(), "sandbox.secret_set.UNPROTECTED")
		if e == nil {
			t.Fatal("expected sandbox.secret_set.UNPROTECTED")
		}
		if e["model"] != "open" {
			t.Errorf("model = %v, want %q", e["model"], "open")
		}
	})

	t.Run("protected via fake Landlock backend", func(t *testing.T) {
		entries := captureSlogJSON(t)
		cfg := &config.Config{}
		cfg.Sandbox.Mode = "enforce"
		cfg.Sandbox.FilesystemModel = string(sandbox.FilesystemModelConfined)

		_, err := applySandbox(SandboxApplyOptions{
			Cfg:      cfg,
			HomePath: t.TempDir(),
			Backend:  &fakeLinuxBackend{},
			GetEnv:   func(string) string { return "" },
		})
		if err != nil {
			t.Fatalf("applySandbox: %v", err)
		}
		captured := entries()

		protected := findEvent(captured, "sandbox.secret_set.protected")
		if protected == nil {
			t.Fatal("expected sandbox.secret_set.protected")
		}
		if protected["model"] != "confined" {
			t.Errorf("secret_set.protected model = %v, want %q", protected["model"], "confined")
		}

		applied := findEvent(captured, "sandbox.applied")
		if applied == nil {
			t.Fatal("expected sandbox.applied to be logged for a successful enforce apply")
		}
		if applied["model"] != "confined" {
			t.Errorf("sandbox.applied model = %v, want %q (ADR-060 section:10.1)", applied["model"], "confined")
		}
	})
}

// TestApplySandbox_SecretSetLog_Darwin exercises the macOS branch with the
// real SeatbeltBackend where available, covering:
//   - mode=permissive degrades to application-level enforcement (Seatbelt has
//     no audit-only mode) → UNPROTECTED, not seatbelt_deny.
//   - mode=enforce actually installs a profile → protected/seatbelt_deny, and
//     sandbox.applied carries "model" (finding 4, macOS emission site).
//
// Skips on non-darwin or when sandbox-exec is unavailable, matching the
// pattern in sandbox_apply_darwin_test.go.
func TestApplySandbox_SecretSetLog_Darwin(t *testing.T) {
	backend := sandbox.NewSeatbeltBackend()
	if !backend.Available() {
		t.Skip("sandbox-exec unavailable on this host, or not darwin")
	}

	t.Run("permissive degrades to UNPROTECTED, not seatbelt_deny", func(t *testing.T) {
		entries := captureSlogJSON(t)
		cfg := &config.Config{}
		cfg.Sandbox.Mode = "permissive"

		tmp, err := os.CreateTemp(t.TempDir(), "stderr")
		if err != nil {
			t.Fatalf("temp stderr: %v", err)
		}
		defer func() { _ = tmp.Close() }()

		_, err = applySandbox(SandboxApplyOptions{
			Cfg:      cfg,
			HomePath: t.TempDir(),
			Backend:  sandbox.NewSeatbeltBackend(),
			GetEnv:   func(string) string { return "" },
			Stderr:   tmp,
		})
		if err != nil {
			t.Fatalf("applySandbox: %v", err)
		}

		captured := entries()
		if protected := findEvent(captured, "sandbox.secret_set.protected"); protected != nil {
			t.Errorf("mode=permissive on Seatbelt must not claim protection (no profile is installed); got %v", protected)
		}
		if findEvent(captured, "sandbox.secret_set.UNPROTECTED") == nil {
			t.Errorf("expected sandbox.secret_set.UNPROTECTED under mode=permissive; captured events: %v", captured)
		}
	})

	t.Run("enforce actually installs a profile → protected/seatbelt_deny", func(t *testing.T) {
		entries := captureSlogJSON(t)
		cfg := &config.Config{}
		cfg.Sandbox.Mode = "enforce"

		fresh := sandbox.NewSeatbeltBackend()
		_, err := applySandbox(SandboxApplyOptions{
			Cfg:      cfg,
			HomePath: t.TempDir(),
			Backend:  fresh,
			GetEnv:   func(string) string { return "" },
		})
		if err != nil {
			t.Fatalf("applySandbox: %v", err)
		}
		if !fresh.PolicyApplied() {
			t.Fatal("precondition failed: enforce must install a kernel profile")
		}

		captured := entries()
		if unprotected := findEvent(captured, "sandbox.secret_set.UNPROTECTED"); unprotected != nil {
			t.Errorf("mode=enforce with an installed Seatbelt profile must not log UNPROTECTED; got %v", unprotected)
		}
		protected := findEvent(captured, "sandbox.secret_set.protected")
		if protected == nil {
			t.Fatalf("expected sandbox.secret_set.protected; captured events: %v", captured)
		}
		if protected["mechanism"] != "seatbelt_deny" {
			t.Errorf("mechanism = %v, want seatbelt_deny", protected["mechanism"])
		}

		applied := findEvent(captured, "sandbox.applied")
		if applied == nil {
			t.Fatal("expected sandbox.applied to be logged")
		}
		if applied["model"] == nil || applied["model"] == "" {
			t.Errorf("sandbox.applied (macOS emission site) is missing \"model\" (ADR-060 section:10.1); got %v", applied)
		}
	})
}
