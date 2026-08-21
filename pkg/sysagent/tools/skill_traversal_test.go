// Omnipus — System Agent Tool Tests: remove_skill path traversal
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// newRemoveSkillFixture builds a workspace that looks like a live install — an
// operator data file at the workspace root plus one real installed skill — and
// wires a remove_skill tool against it.
func newRemoveSkillFixture(t *testing.T) (tool *systools.SkillRemoveTool, workspace, operatorFile, installedSkill string) {
	t.Helper()
	workspace = t.TempDir()

	operatorFile = filepath.Join(workspace, "operator-data.txt")
	if err := os.WriteFile(operatorFile, []byte("the operator's workspace"), 0o600); err != nil {
		t.Fatalf("seed operator file: %v", err)
	}

	installedSkill = filepath.Join(workspace, "skills", "keep-me")
	if err := os.MkdirAll(installedSkill, 0o755); err != nil {
		t.Fatalf("seed skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(installedSkill, "SKILL.md"),
		[]byte("# keep-me\n\nA skill that must survive.\n"), 0o644); err != nil {
		t.Fatalf("seed SKILL.md: %v", err)
	}

	installer, err := skills.NewSkillInstaller(workspace, "", "")
	if err != nil {
		t.Fatalf("NewSkillInstaller: %v", err)
	}
	deps, _ := newTestDeps()
	deps.SkillInstaller = installer
	return systools.NewSkillRemoveTool(deps), workspace, operatorFile, installedSkill
}

// TestSkillRemoveTool_RefusesPathTraversal is the end-to-end proof for the HIGH
// severity finding: remove_skill passed the LLM-supplied name straight to the
// installer with no validation, and the installer's split-on-"/" only stripped
// separators. name=".." resolved to the workspace root and os.RemoveAll deleted
// the operator's whole workspace; name="." wiped every installed skill.
//
// The confirmation prompt was no defence — a seeded "ask" policy renders as a
// benign-looking "remove skill '..'".
func TestSkillRemoveTool_RefusesPathTraversal(t *testing.T) {
	malicious := []struct {
		name   string
		effect string
	}{
		{"..", "deletes the operator's entire workspace"},
		{".", "deletes every installed skill"},
		{"../..", "deletes the operator's entire workspace (the split takes the last \"..\")"},
		{"foo/../..", "deletes the operator's entire workspace (the split takes the last \"..\")"},
		{"skills/..", "deletes the operator's entire workspace"},
		{"/", "deletes every installed skill (the split yields no segment)"},
		{"/etc/passwd", "escapes the skills directory via an absolute path"},
		{"..\\..", "escapes via Windows-style separators"},
		{"keep-me/..", "deletes the operator's entire workspace"},
	}

	for _, m := range malicious {
		t.Run(m.name, func(t *testing.T) {
			tool, workspace, operatorFile, installedSkill := newRemoveSkillFixture(t)

			result := tool.Execute(context.Background(), map[string]any{
				"name":    m.name,
				"confirm": true,
			})
			if !result.IsError {
				t.Fatalf("remove_skill(%q) succeeded — it %s: %s", m.name, m.effect, result.ForLLM)
			}
			// A refused traversal must never be reported as a missing skill: that
			// presents an attack as a benign typo in both the tool result and the
			// audit trail.
			m2 := parseError(t, result.ForLLM)
			errBlock, _ := m2["error"].(map[string]any)
			if code, _ := errBlock["code"].(string); code == "NOT_FOUND" {
				t.Errorf("remove_skill(%q) reported NOT_FOUND for a refused traversal: %s",
					m.name, result.ForLLM)
			}

			// The blast radius the finding described, asserted directly.
			if _, err := os.Stat(workspace); err != nil {
				t.Fatalf("the operator workspace was deleted by remove_skill(%q): %v", m.name, err)
			}
			if _, err := os.Stat(operatorFile); err != nil {
				t.Fatalf("the operator's data file was deleted by remove_skill(%q): %v", m.name, err)
			}
			if _, err := os.Stat(installedSkill); err != nil {
				t.Fatalf("the installed skill was deleted by remove_skill(%q): %v", m.name, err)
			}
		})
	}
}

// TestSkillRemoveTool_LegitimateRemovalStillWorks is the positive control:
// without it every assertion above would also pass against a build that refuses
// every name, which would be a broken tool rather than a fixed one.
func TestSkillRemoveTool_LegitimateRemovalStillWorks(t *testing.T) {
	tool, workspace, operatorFile, installedSkill := newRemoveSkillFixture(t)

	result := tool.Execute(context.Background(), map[string]any{
		"name":    "keep-me",
		"confirm": true,
	})
	if result.IsError {
		t.Fatalf("remove_skill(keep-me) failed: %s", result.ForLLM)
	}
	m := parseSuccess(t, result.ForLLM)
	if success, _ := m["success"].(bool); !success {
		t.Errorf("expected success=true, got: %s", result.ForLLM)
	}
	if _, err := os.Stat(installedSkill); !os.IsNotExist(err) {
		t.Errorf("skill directory still exists after a legitimate removal")
	}
	// The rest of the workspace is untouched by a legitimate removal.
	if _, err := os.Stat(workspace); err != nil {
		t.Errorf("workspace missing after a legitimate removal: %v", err)
	}
	if _, err := os.Stat(operatorFile); err != nil {
		t.Errorf("operator data file missing after a legitimate removal: %v", err)
	}
}
