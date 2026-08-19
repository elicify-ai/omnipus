// Omnipus — Skill installer path-traversal regression tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// traversalFixture is a workspace that looks like a live install. The fields
// are the paths a successful traversal would destroy, which is what each test
// asserts still exists afterwards.
//
// A struct rather than four return values: most callers want a subset, and the
// four-value form made the one that wants only the workspace read
// `workspace, _, _, _ :=` — three blanks that say nothing about which paths
// were deliberately ignored.
type traversalFixture struct {
	workspace      string
	operatorFile   string
	skillsDir      string
	installedSkill string
}

// newTraversalFixture builds a workspace with an operator data file at its
// root, a skills/ directory, and one real installed skill inside it.
func newTraversalFixture(t *testing.T) traversalFixture {
	t.Helper()
	f := traversalFixture{workspace: t.TempDir()}

	f.operatorFile = filepath.Join(f.workspace, "operator-data.txt")
	if err := os.WriteFile(f.operatorFile, []byte("the operator's workspace"), 0o600); err != nil {
		t.Fatalf("seed operator file: %v", err)
	}

	f.skillsDir = filepath.Join(f.workspace, "skills")
	f.installedSkill = filepath.Join(f.skillsDir, "keep-me")
	if err := os.MkdirAll(f.installedSkill, 0o755); err != nil {
		t.Fatalf("seed skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.installedSkill, "SKILL.md"),
		[]byte("# keep-me\n\nA skill that must survive.\n"), 0o644); err != nil {
		t.Fatalf("seed SKILL.md: %v", err)
	}
	return f
}

func mustExist(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s no longer exists at %s: %v", what, path, err)
	}
}

// TestUninstall_RefusesPathTraversal is the regression for the HIGH severity
// finding: SkillInstaller.Uninstall split the name on "/" and took the last
// segment, which strips separators but NOT "..". A name of ".." produced
// filepath.Join(workspace, "skills", "..") — Clean collapses that to the
// workspace root — and os.RemoveAll then deleted the operator's entire
// workspace. "." wiped every installed skill.
func TestUninstall_RefusesPathTraversal(t *testing.T) {
	malicious := []struct {
		name   string
		effect string
	}{
		{"..", "deletes the operator's entire fx.workspace"},
		{".", "deletes every installed skill"},
		{"../..", "deletes the operator's entire fx.workspace (the split takes the last \"..\")"},
		{"foo/../..", "deletes the operator's entire fx.workspace (the split takes the last \"..\")"},
		{"skills/..", "deletes the operator's entire fx.workspace"},
		{"/", "deletes every installed skill (the split yields no segment)"},
		{"/etc/passwd", "escapes the skills directory via an absolute path"},
		{"..\\..", "escapes via Windows-style separators"},
		{"keep-me/..", "deletes the operator's entire fx.workspace"},
		{"", "has no resolvable target"},
		{"   ", "has no resolvable target"},
	}

	for _, m := range malicious {
		t.Run(m.name, func(t *testing.T) {
			fx := newTraversalFixture(t)
			installer, err := NewSkillInstaller(fx.workspace, "", "")
			if err != nil {
				t.Fatalf("NewSkillInstaller: %v", err)
			}

			err = installer.Uninstall(m.name)
			if err == nil {
				t.Fatalf("Uninstall(%q) returned nil — it %s", m.name, m.effect)
			}
			// A refused traversal must NOT be reported as "not found": callers
			// (remove_skill's isNotFound, the REST delete handler) map that
			// substring to NOT_FOUND/404, which would present an attack as a
			// benign typo.
			if strings.Contains(err.Error(), "not found") {
				t.Errorf("Uninstall(%q) error %q is reported as a missing skill, not a refusal",
					m.name, err.Error())
			}

			mustExist(t, fx.workspace, "the operator fx.workspace")
			mustExist(t, fx.operatorFile, "the operator's data file")
			mustExist(t, fx.skillsDir, "the skills directory")
			mustExist(t, fx.installedSkill, "the installed skill")
		})
	}
}

// TestUninstall_LegitimateRemovalStillWorks is the positive control. Without it
// the test above passes against a build that refuses every name — which would
// be a broken tool, not a fixed one.
func TestUninstall_LegitimateRemovalStillWorks(t *testing.T) {
	t.Run("bare skill id", func(t *testing.T) {
		fx := newTraversalFixture(t)
		installer, err := NewSkillInstaller(fx.workspace, "", "")
		if err != nil {
			t.Fatalf("NewSkillInstaller: %v", err)
		}
		if err := installer.Uninstall("keep-me"); err != nil {
			t.Fatalf("Uninstall(keep-me) = %v, want nil", err)
		}
		if _, err := os.Stat(fx.installedSkill); !os.IsNotExist(err) {
			t.Errorf("skill directory still exists after a legitimate uninstall")
		}
		mustExist(t, fx.workspace, "the operator fx.workspace")
		mustExist(t, fx.operatorFile, "the operator's data file")
		mustExist(t, fx.skillsDir, "the skills directory")
	})

	// The namespaced forms the installer has always accepted must keep working:
	// the guard validates the extracted final segment, it does not forbid the
	// "owner/repo/skill" spelling.
	for _, name := range []string{"owner/repo/keep-me", "keep-me/", "owner/keep-me"} {
		t.Run(name, func(t *testing.T) {
			fx := newTraversalFixture(t)
			installer, err := NewSkillInstaller(fx.workspace, "", "")
			if err != nil {
				t.Fatalf("NewSkillInstaller: %v", err)
			}
			if err := installer.Uninstall(name); err != nil {
				t.Fatalf("Uninstall(%q) = %v, want nil", name, err)
			}
			if _, err := os.Stat(fx.installedSkill); !os.IsNotExist(err) {
				t.Errorf("skill directory still exists after Uninstall(%q)", name)
			}
		})
	}
}

// TestUninstall_MissingSkillStillReportsNotFound pins the error contract the
// callers depend on: a well-formed name that simply is not installed must still
// produce a "not found" error, so remove_skill returns NOT_FOUND and the REST
// handler returns 404.
func TestUninstall_MissingSkillStillReportsNotFound(t *testing.T) {
	fx := newTraversalFixture(t)
	installer, err := NewSkillInstaller(fx.workspace, "", "")
	if err != nil {
		t.Fatalf("NewSkillInstaller: %v", err)
	}
	err = installer.Uninstall("no-such-skill")
	if err == nil {
		t.Fatal("Uninstall(no-such-skill) = nil, want a not-found error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want it to contain \"not found\"", err.Error())
	}
}
