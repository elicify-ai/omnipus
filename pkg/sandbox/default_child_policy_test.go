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

	policy := DefaultChildPolicy(home, nil, nil, nil, nil)

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
	for _, sub := range []string{"workspace", "sessions", "memory", "skills", "logs"} {
		if err := os.MkdirAll(filepath.Join(home, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}

	policy := DefaultChildPolicy(home, nil, nil, nil, nil)

	// "system" was in this list until the audit log joined the secret set: it
	// holds audit.jsonl and its HMAC chain anchor, and a child that can truncate
	// or delete it erases the record of what it did. The HMAC chain detects
	// modification; it does not survive rm. So system/ must now be carved out
	// like any other secret rather than granted as an ordinary subdirectory.
	//
	// "skills" left this list for the same kind of reason (ADR-072 D10 Part A /
	// D10.1, corrected here by D10.3): the installed skill registry is a
	// context-free secret at the KERNEL layer, so a spawned child must not be
	// granted it. This test asserted the opposite — that DefaultChildPolicy
	// grants $OMNIPUS_HOME/skills full RWX — because SecretEntriesAlwaysRelative
	// was never updated when SecretEntriesAlwaysPathOnly was introduced as a
	// third list. It is asserted as DENIED below.
	wantPaths := []string{"workspace", "sessions", "memory", "logs"}
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

	// The registry skills directory must NOT be granted. DefaultChildPolicy
	// carves out the context-free secret set, and ADR-072 D10 Part A put
	// $OMNIPUS_HOME/skills in it (via fspolicy.SecretEntriesAlwaysPathOnly).
	// The kernel layer deliberately keeps this as a WHOLE-DIRECTORY deny even
	// though the app layer narrowed to instruction files in D10.3: narrowing
	// the kernel deny requires D10.2/§6.8's Linux child-only spike, which has
	// not happened.
	skillsPath := filepath.Join(home, "skills")
	for _, r := range policy.FilesystemRules {
		if filepath.Clean(r.Path) == filepath.Clean(skillsPath) {
			t.Errorf("DefaultChildPolicy MUST NOT grant %q (access %#x) — the installed skill "+
				"registry is a context-free secret at the kernel layer (ADR-072 D10 Part A / D10.1)",
				skillsPath, r.Access)
		}
	}
}

// TestDefaultChildPolicy_OmitsSecretFiles verifies the carve-out: master.key
// and credentials.json sitting at the top level of $OMNIPUS_HOME must NOT
// appear in any rule.
func TestDefaultChildPolicy_OmitsSecretFiles(t *testing.T) {
	home := t.TempDir()
	// Seed every secret. The set is no longer files-only: ADR-062 added
	// entities/, a DIRECTORY, so seeding blindly with WriteFile would create a
	// regular file named "entities" and the carve-out would be tested against a
	// shape that never occurs on a real install.
	for _, name := range SecretFilesRelative {
		if name == "entities" || name == "agents" || name == "workspaces" {
			if err := os.MkdirAll(filepath.Join(home, name), 0o700); err != nil {
				t.Fatalf("mkdir %s: %v", name, err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(home, name), []byte("seed"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// The control must be a file that is genuinely NOT in the secret set.
	// This was config.json until ADR-062 widened the set to include it — a
	// child that can write config.json can set sandbox.mode: off and remove its
	// own confinement on the next boot. Keeping config.json as the "must still
	// be granted" control would have asserted the hole stays open.
	controlPath := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(controlPath, []byte("not a secret"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}

	// agents/ vs entities/ — the distinction ADR-062 §4.0 turns on. agents/
	// holds agent WORKSPACES and must stay writable; entities/ holds their tool
	// POLICY and must not. Naming the wrong one would break every agent's
	// working directory while looking like hardening, so both are asserted.
	// agents/ is seeded by the loop above now that it is part of the secret
	// vocabulary. It must still be GRANTED here: DefaultChildPolicy carves out
	// the boot set, and agents/ is deliberately not in that — see
	// TestSecretPaths.

	policy := DefaultChildPolicy(home, nil, nil, nil, nil)

	// Iterate the BOOT set, which is what DefaultChildPolicy carves out. The
	// combined list additionally contains the coarse agents/ and workspaces/
	// roots, and those are granted here on purpose — their exclusion is a
	// per-turn decision that needs a work dir (see DeniedPathsFor). Asserting
	// against the combined list here demands a carve-out this function must not
	// perform, and the two explicit assertions at the end of this test pin that
	// distinction directly.
	for _, secret := range SecretEntriesAlwaysRelative {
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

	granted := func(rel string) bool {
		want := filepath.Clean(filepath.Join(home, rel))
		for _, r := range policy.FilesystemRules {
			if filepath.Clean(r.Path) == want {
				return true
			}
		}
		return false
	}
	if !granted("agents") {
		t.Error("agents/ MUST stay granted — it holds agent workspaces, not policy; " +
			"stripping it makes every agent's own working directory unreachable")
	}
	if granted("entities") {
		t.Error("entities/ MUST NOT be granted — it holds per-agent tool policy, " +
			"and a child that can write it can flip its own tools to allow")
	}
}

// TestDefaultChildPolicy_PreservesSystemPaths verifies that the system
// read-only paths and /tmp are still granted (carve-out must NOT regress
// the rest of the policy).
func TestDefaultChildPolicy_PreservesSystemPaths(t *testing.T) {
	home := t.TempDir()
	policy := DefaultChildPolicy(home, nil, nil, nil, nil)

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
	policy := DefaultChildPolicy(missing, nil, nil, nil, nil)

	cleanMissing := filepath.Clean(missing)
	for _, r := range policy.FilesystemRules {
		if filepath.Clean(r.Path) == cleanMissing {
			t.Errorf("DefaultChildPolicy must not grant a missing home root %q (got rule %#v)", cleanMissing, r)
		}
	}
}
