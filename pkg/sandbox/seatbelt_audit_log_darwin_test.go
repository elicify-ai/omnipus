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

// TestSeatbelt_RealChildCannotTamperWithAuditLog is the kernel-layer proof
// for adversarial-review finding #1: $OMNIPUS_HOME/system/ (which holds
// audit.jsonl) was not in any part of the secret set, so a sandboxed child
// could truncate or delete the audit log outright. The v0.2 HMAC chain
// (pkg/audit/hmac.go) detects a child MODIFYING a logged entry; it detects
// nothing about `rm audit.jsonl` or `: > audit.jsonl` — neither needs a read,
// and a deleted file leaves no chain to verify at all.
//
// Modeled directly on TestSeatbelt_RealChildCannotReachSecrets (this
// package's master.key coverage) — same real-child-under-sandbox-exec
// mechanism, applied to system/audit.jsonl instead.
func TestSeatbelt_RealChildCannotTamperWithAuditLog(t *testing.T) {
	if _, err := os.Stat(seatbeltExecPath); err != nil {
		t.Skipf("sandbox-exec unavailable: %v", err)
	}
	home := t.TempDir()
	systemDir := filepath.Join(home, "system")
	if err := os.MkdirAll(systemDir, 0o700); err != nil {
		t.Fatalf("seed system dir: %v", err)
	}
	auditPath := filepath.Join(systemDir, "audit.jsonl")
	const auditContent = `{"event":"tool_call","decision":"allow","agent_id":"mia"}` + "\n"
	if err := os.WriteFile(auditPath, []byte(auditContent), 0o600); err != nil {
		t.Fatalf("seed audit log: %v", err)
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

	t.Run("read denied", func(t *testing.T) {
		if err := run("/bin/cat", auditPath); err == nil {
			t.Error("child read audit.jsonl — the deny did not hold")
		}
	})

	t.Run("truncate denied", func(t *testing.T) {
		if err := run("/bin/sh", "-c", ": > "+auditPath); err == nil {
			t.Error("child truncated audit.jsonl — the record of what it did would be gone")
		}
		data, err := os.ReadFile(auditPath)
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
		if len(data) == 0 {
			t.Fatalf("BREACH: audit.jsonl truncated by a sandboxed child; len=0")
		}
		if string(data) != auditContent {
			t.Errorf("audit.jsonl was modified: %q", data)
		}
	})

	t.Run("delete denied", func(t *testing.T) {
		if err := run("/bin/rm", auditPath); err == nil {
			t.Error("child deleted audit.jsonl — the deny did not hold")
		}
		if _, statErr := os.Stat(auditPath); statErr != nil {
			t.Fatalf("BREACH: audit.jsonl DELETED by a sandboxed child: %v", statErr)
		}
	})

	t.Run("rename out denied", func(t *testing.T) {
		// FR-3.2a's rationale applies identically here: a rename evades a
		// read-only deny entirely, and for a log the equivalent attack is
		// "move it aside and drop a fresh, empty one in its place" — same
		// practical effect as truncation, reached through a different
		// syscall.
		moved := filepath.Join(home, "moved-audit.jsonl")
		if err := run("/bin/mv", auditPath, moved); err == nil {
			t.Error("child renamed audit.jsonl out from under the deny")
		}
		if _, err := os.Stat(auditPath); err != nil {
			t.Errorf("audit.jsonl must still be in place after a denied rename: %v", err)
		}
	})

	t.Run("planting a new file inside system/ denied", func(t *testing.T) {
		// system/ is denied as a whole directory (not just the audit.jsonl
		// name), so a child cannot work around the deny by writing a
		// differently-named file into the same directory either.
		planted := filepath.Join(systemDir, "planted.json")
		if err := run("/bin/sh", "-c", "echo hi > "+planted); err == nil {
			t.Error("child created a new file inside system/ — the directory-level deny did not hold")
		}
		if _, err := os.Stat(planted); err == nil {
			t.Error("a new file was planted inside system/ despite the deny")
		}
	})
}

// TestSeatbelt_GatewayItselfStaysUnconfined proves the OTHER half of finding
// #1's fix: the gateway process — which is what actually WRITES audit.jsonl
// on every tool call — must keep working after system/ joins the secret set.
//
// On macOS this is structural rather than something the deny list could ever
// break: sandbox-exec confines only the child process named in its argv, and
// the gateway is never wrapped in it — Apply/ApplyToCmd on this backend only
// ever touch a *exec.Cmd for a SPAWNED child (see backend_darwin_seatbelt.go),
// never the gateway's own process. This test makes that structural claim
// concrete rather than asserted only in a doc comment: it writes to
// audit.jsonl directly, exactly as pkg/audit.Logger's append-only writer
// does, with NO sandbox-exec wrapper at all — standing in for "the gateway,
// which is unconfined" — and confirms the write lands, in the same directory
// a spawned child was just denied all access to above.
func TestSeatbelt_GatewayItselfStaysUnconfined(t *testing.T) {
	home := t.TempDir()
	systemDir := filepath.Join(home, "system")
	if err := os.MkdirAll(systemDir, 0o700); err != nil {
		t.Fatalf("seed system dir: %v", err)
	}
	auditPath := filepath.Join(systemDir, "audit.jsonl")
	if err := os.WriteFile(auditPath, []byte(`{"event":"boot"}`+"\n"), 0o600); err != nil {
		t.Fatalf("seed audit log: %v", err)
	}

	// system/ is now part of the secret set this package's DeniedPaths
	// derives from — rendering a profile at all must not somehow reach back
	// and affect an unrelated, unwrapped write.
	if _, err := renderSeatbeltProfile(openPolicy(home)); err != nil {
		t.Fatalf("render (as the gateway would, to hand a profile to some OTHER spawned child): %v", err)
	}

	f, err := os.OpenFile(auditPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("gateway-equivalent append-open must succeed: %v", err)
	}
	if _, err := f.WriteString(`{"event":"tool_call","decision":"allow"}` + "\n"); err != nil {
		f.Close()
		t.Fatalf("gateway-equivalent write must succeed: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(data), `"event":"tool_call"`) {
		t.Fatalf("gateway-equivalent write did not persist: %q", data)
	}
}
