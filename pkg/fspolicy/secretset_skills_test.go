// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package fspolicy

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSecretEntries_SkillsDeniedForPathsNotTextGuard is ADR-072 D10 Part A /
// D10.1's regression (spec test #21, T7f): the installed skill registry
// ($OMNIPUS_HOME/skills) must be an ordinary carve-out for every PATH-DENIAL
// consumer — SecretPaths/IsCarveOut (app layer, ResolvePath), DeniedPathsFor
// and SecretPathsAlways (kernel boot/per-turn) — while staying OUT of
// SecretEntriesAlways itself, which is the list pkg/tools/shell.go's
// buildSecretGuardPatterns compiles into a literal-text command guard. This
// is asserted directly against fspolicy's own data, independent of whatever
// pkg/tools/resolvepath.go does with it (a separate, independent leg of the
// same D10 Part A protection — see that file's own doc comment).
func TestSecretEntries_SkillsDeniedForPathsNotTextGuard(t *testing.T) {
	home := filepath.FromSlash("/omnh")
	skillsRoot := filepath.Join(home, "skills")
	skillFile := filepath.Join(skillsRoot, "plan-spec", "SKILL.md")

	t.Run("skills is NOT in SecretEntriesAlways", func(t *testing.T) {
		for _, name := range SecretEntriesAlways {
			if name == "skills" {
				t.Fatalf("SecretEntriesAlways must not contain %q — it also feeds "+
					"pkg/tools/shell.go's buildSecretGuardPatterns literal-text guard, "+
					"and \"skills\" is an ordinary English word an agent legitimately "+
					"types constantly (ADR-072 D10.1)", name)
			}
		}
	})

	t.Run("skills IS in SecretEntriesAlwaysPathOnly", func(t *testing.T) {
		found := false
		for _, name := range SecretEntriesAlwaysPathOnly {
			if name == "skills" {
				found = true
			}
		}
		if !found {
			t.Fatal("SecretEntriesAlwaysPathOnly must contain \"skills\"")
		}
	})

	t.Run("registry skill path is denied via IsCarveOut (app layer)", func(t *testing.T) {
		policy := FSPolicy{
			WorkDir:   filepath.Join(home, "agents", "self"),
			Scope:     FSScopeConfined,
			CarveOuts: buildCarveOuts(home),
		}
		if !IsCarveOut(skillFile, policy) {
			t.Errorf("IsCarveOut(%q) = false, want true — a registry skill file must be denied", skillFile)
		}
		if !IsCarveOut(skillsRoot, policy) {
			t.Errorf("IsCarveOut(%q) = false, want true — the skills root itself must be denied", skillsRoot)
		}
	})

	t.Run("registry skill path is denied via IsCarveOut under FSScopeUnrestricted too", func(t *testing.T) {
		policy := FSPolicy{
			WorkDir:   filepath.Join(home, "agents", "self"),
			Scope:     FSScopeUnrestricted,
			CarveOuts: buildCarveOuts(home),
		}
		if !IsCarveOut(skillFile, policy) {
			t.Errorf("IsCarveOut(%q) under FSScopeUnrestricted = false, want true", skillFile)
		}
	})

	t.Run("skills is in DeniedPathsFor regardless of work dir", func(t *testing.T) {
		for _, workDir := range []string{
			"",
			filepath.Join(home, "agents", "self"),
			filepath.Join(home, "workspaces", "w1", "work"),
			skillsRoot, // even a (misconfigured) turn rooted at skills/ itself
		} {
			denied := DeniedPathsFor(home, workDir)
			if !containsPath(denied, skillsRoot) {
				t.Errorf("DeniedPathsFor(%q, %q) = %v, want it to include %q",
					home, workDir, denied, skillsRoot)
			}
		}
	})

	t.Run("skills is in SecretPathsAlways (kernel boot policy)", func(t *testing.T) {
		always := SecretPathsAlways(home)
		if !containsPath(always, skillsRoot) {
			t.Errorf("SecretPathsAlways(%q) = %v, want it to include %q", home, always, skillsRoot)
		}
	})

	t.Run("skills is in SecretPaths / SecretEntriesRelative", func(t *testing.T) {
		all := SecretPaths(home)
		if !containsPath(all, skillsRoot) {
			t.Errorf("SecretPaths(%q) = %v, want it to include %q", home, all, skillsRoot)
		}
		if !IsSecretName("skills") {
			t.Error("IsSecretName(\"skills\") = false, want true")
		}
	})

	t.Run("skills is in the kernel deny list for spawned children (POSIX)", func(t *testing.T) {
		// A real directory tree, unlike the synthetic "/omnh" used above:
		// KernelDeniedPathsFor walks $OMNIPUS_HOME/agents on disk to separate
		// the caller's own tree from every sibling agent.
		realHome := t.TempDir()
		selfDir := filepath.Join(realHome, "agents", "self")
		if err := os.MkdirAll(selfDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", selfDir, err)
		}
		realSkillsRoot := filepath.Join(realHome, "skills")

		denied, err := KernelDeniedPathsFor(realHome, selfDir)
		if err != nil {
			t.Fatalf("KernelDeniedPathsFor(%q, %q) returned an error: %v", realHome, selfDir, err)
		}
		if !containsPath(denied, realSkillsRoot) {
			t.Errorf("KernelDeniedPathsFor(%q, %q) = %v, want it to include %q",
				realHome, selfDir, denied, realSkillsRoot)
		}
	})
}

func containsPath(haystack []string, needle string) bool {
	for _, p := range haystack {
		if p == needle {
			return true
		}
	}
	return false
}
