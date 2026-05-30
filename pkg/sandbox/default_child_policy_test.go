// Tests for DefaultChildPolicy (v0.2 #155 item 8 — secrets-subtree carve-out).
//
// DefaultChildPolicy returns a SandboxPolicy that:
//   - omits the broad $OMNIPUS_HOME RWX rule that DefaultPolicy installs
//   - grants RWX on each existing subdirectory of $OMNIPUS_HOME
//   - grants RW on each top-level non-secret file in $OMNIPUS_HOME
//   - DOES NOT grant master.key or credentials.json (the carve-out)
//
// The end-to-end kernel-blocking proof lives in redteam_master_key_test.go.
// This file is the unit-level shape proof: given a fresh tempdir layout,
// assert the rule list matches the documented contract.

package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDefaultChildPolicy_OmitsHomeRoot verifies that the $OMNIPUS_HOME root
// is NOT granted as an RWX tree by DefaultChildPolicy. DefaultPolicy DOES
// grant it; DefaultChildPolicy must surgically remove that rule.
func TestDefaultChildPolicy_OmitsHomeRoot(t *testing.T) {
	home := t.TempDir()

	policy := DefaultChildPolicy(home, nil, nil, nil)

	cleanHome := filepath.Clean(home)
	for _, r := range policy.FilesystemRules {
		if filepath.Clean(r.Path) == cleanHome {
			t.Errorf(
				"DefaultChildPolicy must NOT grant $OMNIPUS_HOME root (%q) — secrets carve-out is defeated when the parent tree is granted",
				cleanHome,
			)
		}
	}
}

// TestDefaultChildPolicy_GrantsSubdirsRWX verifies that each existing
// subdirectory of $OMNIPUS_HOME is granted RWX individually so the child
// can still read/write within workspace/, sessions/, memory/, etc.
func TestDefaultChildPolicy_GrantsSubdirsRWX(t *testing.T) {
	home := t.TempDir()
	for _, sub := range []string{"workspace", "sessions", "memory", "skills", "logs", "system"} {
		if err := os.MkdirAll(filepath.Join(home, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}

	policy := DefaultChildPolicy(home, nil, nil, nil)

	wantPaths := []string{"workspace", "sessions", "memory", "skills", "logs", "system"}
	for _, want := range wantPaths {
		fullPath := filepath.Join(home, want)
		var found bool
		for _, r := range policy.FilesystemRules {
			if filepath.Clean(r.Path) != filepath.Clean(fullPath) {
				continue
			}
			if r.Access&AccessRead == 0 ||
				r.Access&AccessWrite == 0 ||
				r.Access&AccessExecute == 0 {
				t.Errorf("subdir %q access %#x must include RWX", want, r.Access)
			}
			found = true
			break
		}
		if !found {
			t.Errorf("DefaultChildPolicy must grant subdir %q individually after stripping the home root", fullPath)
		}
	}
}

// TestDefaultChildPolicy_OmitsSecretFiles verifies the carve-out: master.key
// and credentials.json sitting at the top level of $OMNIPUS_HOME must NOT
// appear in any rule.
func TestDefaultChildPolicy_OmitsSecretFiles(t *testing.T) {
	home := t.TempDir()
	for _, name := range SecretFilesRelative {
		if err := os.WriteFile(filepath.Join(home, name), []byte("seed"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	controlPath := filepath.Join(home, "config.json")
	if err := os.WriteFile(controlPath, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	policy := DefaultChildPolicy(home, nil, nil, nil)

	for _, secret := range SecretFilesRelative {
		secretPath := filepath.Clean(filepath.Join(home, secret))
		for _, r := range policy.FilesystemRules {
			if filepath.Clean(r.Path) == secretPath {
				t.Errorf("DefaultChildPolicy MUST NOT grant secret file %q — carve-out failed (rule access %#x)",
					secretPath, r.Access)
			}
		}
	}

	var controlGranted bool
	cleanControl := filepath.Clean(controlPath)
	for _, r := range policy.FilesystemRules {
		if filepath.Clean(r.Path) == cleanControl {
			controlGranted = true
			if r.Access&AccessRead == 0 || r.Access&AccessWrite == 0 {
				t.Errorf("control file %q must have RW; got %#x", cleanControl, r.Access)
			}
		}
	}
	if !controlGranted {
		t.Errorf("control file %q was unexpectedly stripped — only secrets should be omitted", cleanControl)
	}
}

// TestDefaultChildPolicy_PreservesSystemPaths verifies that the system
// read-only paths and /tmp are still granted (carve-out must NOT regress
// the rest of the policy).
func TestDefaultChildPolicy_PreservesSystemPaths(t *testing.T) {
	home := t.TempDir()
	policy := DefaultChildPolicy(home, nil, nil, nil)

	expected := []string{"/tmp", "/lib", "/usr/lib", "/usr/bin", "/etc/ssl", "/dev/null"}
	for _, want := range expected {
		var found bool
		for _, r := range policy.FilesystemRules {
			if r.Path == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DefaultChildPolicy must preserve system path %q from DefaultPolicy", want)
		}
	}
}

// TestDefaultChildPolicy_NoEnumerationFailsSafe verifies the documented
// failure mode: when $OMNIPUS_HOME does not exist, DefaultChildPolicy
// returns a policy that grants no $OMNIPUS_HOME content (the safe default).
func TestDefaultChildPolicy_NoEnumerationFailsSafe(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	policy := DefaultChildPolicy(missing, nil, nil, nil)

	cleanMissing := filepath.Clean(missing)
	for _, r := range policy.FilesystemRules {
		if filepath.Clean(r.Path) == cleanMissing {
			t.Errorf("DefaultChildPolicy must not grant a missing home root %q (got rule %#v)", cleanMissing, r)
		}
	}
}
