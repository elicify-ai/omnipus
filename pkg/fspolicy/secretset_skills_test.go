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
// D10.1's regression (spec test #21, T7f), as amended by D10.3: the installed
// skill registry ($OMNIPUS_HOME/skills) must be denied by every KERNEL
// path-denial consumer — DeniedPathsFor, SecretPathsAlways,
// KernelDeniedPathsFor — while staying out of BOTH lists that would over-reach:
//
//   - SecretEntriesAlways, which pkg/tools/shell.go's buildSecretGuardPatterns
//     compiles into a literal-text command guard (D10.1: "skills" is an
//     ordinary English word an agent types constantly).
//   - the APP layer's carve-out roots (buildCarveOuts), because D10.3 gates
//     the registry shelf at file granularity instead — a skill's instruction
//     file, not its whole directory, in pkg/tools/resolvepath.go. See
//     TestSecretEntries_SkillsIsNotAnAppLayerCarveOut below, and
//     SecretEntriesAlwaysPathOnly's own doc comment for why the two layers
//     deliberately differ here.
//
// Asserted directly against fspolicy's own data, independent of whatever
// pkg/tools/resolvepath.go does with it.
func TestSecretEntries_SkillsDeniedForPathsNotTextGuard(t *testing.T) {
	home := filepath.FromSlash("/omnh")
	skillsRoot := filepath.Join(home, "skills")

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
		// SecretPaths is the whole vocabulary, and its one remaining
		// production consumer is pkg/sandbox/derive_from_fspolicy.go's
		// fail-closed KERNEL fallback — which must name the strictest set it
		// can, skills included. It is NOT the app layer's carve-out list any
		// more; that is appCarveOutSecretPaths.
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

// TestSecretEntries_SkillsIsNotAnAppLayerCarveOut is ADR-072 D10.3's
// regression, and it is the assertion that would have caught the defect it
// fixes: `skills` must NOT be one of the app layer's carve-out roots.
//
// While it was, IsCarveOut refused every path under $OMNIPUS_HOME/skills — and
// because pkg/tools.ResolvePath consults IsCarveOut BEFORE D10.3's
// instruction-file classifier, that coarse deny answered first and the whole
// narrowing was dead code in production: a skill's bundled helper script was
// refused exactly as before, while the gate's own unit tests passed because
// they build FSPolicy literals with CarveOuts left nil.
//
// The complement — every OTHER context-free secret must still be here — is
// asserted too. A fix that removed the wrong entries would otherwise satisfy
// this test while opening a real hole.
func TestSecretEntries_SkillsIsNotAnAppLayerCarveOut(t *testing.T) {
	home := filepath.FromSlash("/omnh")
	skillsRoot := filepath.Join(home, "skills")
	skillFile := filepath.Join(skillsRoot, "plan-spec", "SKILL.md")

	carveOuts := buildCarveOuts(home)

	if containsPath(carveOuts, skillsRoot) {
		t.Errorf("buildCarveOuts(%q) = %v, must NOT contain %q — the app layer gates the "+
			"registry shelf at file granularity (pkg/tools/resolvepath.go's instruction-file "+
			"gate), and a whole-directory carve-out here both breaks skills that bundle files "+
			"and silently overrides that gate (ADR-072 D10.3)", home, carveOuts, skillsRoot)
	}

	policy := FSPolicy{
		WorkDir:   filepath.Join(home, "agents", "self"),
		Scope:     FSScopeConfined,
		CarveOuts: carveOuts,
	}
	for _, p := range []string{
		skillsRoot,
		skillFile,
		filepath.Join(skillsRoot, "plan-spec", "templates", "spec-template.md"),
	} {
		if IsCarveOut(p, policy) {
			t.Errorf("IsCarveOut(%q) = true, want false — the app layer no longer denies the "+
				"skills subtree wholesale (ADR-072 D10.3)", p)
		}
	}

	// Complement: nothing else lost its app-layer carve-out.
	for _, name := range append(append([]string{}, SecretEntriesAlways...), SecretEntriesPerTurn...) {
		root := filepath.Join(home, name)
		if !containsPath(carveOuts, root) {
			t.Errorf("buildCarveOuts(%q) = %v, want it to still include %q — D10.3 removes "+
				"exactly one entry, and only because a finer gate replaced it", home, carveOuts, root)
		}
	}

	// And the kernel layer keeps the whole-directory deny (the deliberate
	// asymmetry: narrowing it needs ADR-072 D10.2/§6.8's Linux spike).
	if !containsPath(SecretPathsAlways(home), skillsRoot) {
		t.Errorf("SecretPathsAlways(%q) must still include %q — only the APP layer narrows",
			home, skillsRoot)
	}
}

func containsPath(haystack []string, needle string) bool {
	for _, p := range haystack {
		if p == needle {
			return true
		}
	}
	return false
}
